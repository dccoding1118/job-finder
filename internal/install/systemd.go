package install

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

const (
	apiService = "jobfinder-api.service"
	runService = "jobfinder-run.service"
	runTimer   = "jobfinder-run.timer"
)

var systemdUnits = []string{apiService, runService, runTimer}

// systemdScheduler keeps the API alive as a user service and triggers the daily
// fetch with a user timer. Everything runs as the logged-in user; linger is what
// keeps both alive across logout, and the whole daily-fetch model depends on it.
type systemdScheduler struct{}

func (systemdScheduler) name() string { return "systemd user units" }

func (systemdScheduler) preflight(ctx context.Context) error {
	if !have("systemctl") {
		return fmt.Errorf("install: systemctl not found; a systemd user session is required")
	}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		return fmt.Errorf("install: XDG_RUNTIME_DIR is unset; run this from a logged-in user session")
	}
	if _, err := run(ctx, "systemctl", "--user", "show-environment"); err != nil {
		return fmt.Errorf("install: the systemd --user bus is unavailable: %w", err)
	}
	return nil
}

// mount renders each unit from the artifact's template and reports drift: a
// unit whose new render differs from the installed one is either a template
// update or an operator edit about to be replaced, and neither is swallowed
// silently.
func (systemdScheduler) mount(ctx context.Context, layout paths.Layout, assetDir string, out io.Writer) error {
	templates, err := asset(assetDir, "systemd")
	if err != nil {
		return err
	}
	stash := filepath.Join(layout.LibDir, "units.prev")
	if err := os.MkdirAll(stash, 0o700); err != nil {
		return fmt.Errorf("install: create %s: %w", stash, err)
	}
	for _, unit := range systemdUnits {
		template, err := os.ReadFile(filepath.Join(templates, unit)) // #nosec G304 -- template path derives from the artifact directory.
		if err != nil {
			return fmt.Errorf("install: read unit template %s: %w", unit, err)
		}
		rendered := renderUnit(string(template), layout)
		installed := filepath.Join(layout.UnitDir, unit)
		if previous, err := os.ReadFile(installed); err == nil { // #nosec G304 -- path derives from the resolved layout.
			if err := os.WriteFile(filepath.Join(stash, unit), previous, 0o600); err != nil { // #nosec G703 -- path comes from the resolved layout.
				return fmt.Errorf("install: stash unit %s: %w", unit, err)
			}
			if string(previous) != rendered {
				report(out, "unit %s changed; replacing (previous kept in %s)", unit, stash)
			}
		}
		if err := os.WriteFile(installed, []byte(rendered), 0o600); err != nil { // #nosec G703 -- path comes from the resolved layout.
			return fmt.Errorf("install: write unit %s: %w", unit, err)
		}
	}
	report(out, "installed units into %s", layout.UnitDir)
	if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "--user", "enable", apiService, runTimer); err != nil {
		return err
	}
	reportLinger(ctx, out)
	return nil
}

// renderUnit substitutes the three jobfinder-owned locations into the template.
// The PATH= line keeps systemd's %h specifier: it points at ~/.local/bin and the
// mise shims, which live under $HOME regardless of where XDG puts the rest.
func renderUnit(template string, layout paths.Layout) string {
	replacer := strings.NewReplacer(
		"%h/.local/lib/jobfinder/jobfinder", layout.Binary,
		"%h/.config/jobfinder/config.yaml", layout.Config,
		"%h/.local/share/jobfinder", layout.DataDir,
	)
	return replacer.Replace(template)
}

func (systemdScheduler) restoreDefinitions(ctx context.Context, layout paths.Layout, out io.Writer) error {
	stash := filepath.Join(layout.LibDir, "units.prev")
	restored := 0
	for _, unit := range systemdUnits {
		contents, err := os.ReadFile(filepath.Join(stash, unit)) // #nosec G304 -- path derives from the resolved layout.
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(layout.UnitDir, unit), contents, 0o600); err != nil { // #nosec G703 -- path comes from the resolved layout.
			return fmt.Errorf("install: restore unit %s: %w", unit, err)
		}
		restored++
	}
	if restored == 0 {
		report(out, "no stashed units at %s; units left as they are", stash)
		return nil
	}
	report(out, "restored %d units from %s", restored, stash)
	_, err := run(ctx, "systemctl", "--user", "daemon-reload")
	return err
}

func (s systemdScheduler) start(ctx context.Context, layout paths.Layout, out io.Writer) error {
	if _, err := run(ctx, "systemctl", "--user", "restart", apiService); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "--user", "start", runTimer); err != nil {
		return err
	}
	waitFor(ctx, 15*time.Second, func() bool { return apiResponding(layout) })
	report(out, "started %s and %s", apiService, runTimer)
	return nil
}

// restart uses try-restart rather than "enable --now": enabling does nothing to
// a service that is already running, which is exactly how a replaced binary
// ends up not being the one in memory.
func (s systemdScheduler) restart(ctx context.Context, layout paths.Layout, out io.Writer) error {
	if _, err := run(ctx, "systemctl", "--user", "try-restart", apiService); err != nil {
		return err
	}
	waitFor(ctx, 15*time.Second, func() bool { return apiResponding(layout) })
	report(out, "restarted %s", apiService)
	return nil
}

func (systemdScheduler) assertEffective(ctx context.Context, layout paths.Layout, marker time.Time, out io.Writer) error {
	state, err := run(ctx, "systemctl", "--user", "show", apiService, "-p", "ActiveState", "--value")
	if err != nil {
		return err
	}
	if state != "active" {
		return fmt.Errorf("install: %s is %s, want active", apiService, state)
	}
	pid, err := run(ctx, "systemctl", "--user", "show", apiService, "-p", "MainPID", "--value")
	if err != nil {
		return err
	}
	number, convErr := strconv.Atoi(pid)
	if convErr != nil || number <= 0 {
		return fmt.Errorf("install: %s has no MainPID", apiService)
	}
	// The running process's own executable is the only thing that proves the
	// installed binary is the one in memory; "file copied" and "service active"
	// are both true of a stale process holding a since-replaced inode.
	exe, err := os.Readlink("/proc/" + pid + "/exe")
	if err != nil {
		return fmt.Errorf("install: read /proc/%s/exe: %w", pid, err)
	}
	if strings.TrimSuffix(exe, " (deleted)") != layout.Binary {
		return fmt.Errorf("install: the running process (pid %s) executes %q, want %q — a stale process is still live", pid, exe, layout.Binary)
	}
	started, err := run(ctx, "systemctl", "--user", "show", apiService, "-p", "ExecMainStartTimestampMonotonic", "--value")
	if err != nil {
		return err
	}
	if startedErr := assertStartedAfter(ctx, started, marker); startedErr != nil {
		return startedErr
	}
	report(out, "API service effective: pid %s running %s", pid, layout.Binary)

	load, err := run(ctx, "systemctl", "--user", "show", runService, "-p", "LoadState", "--value")
	if err != nil {
		return err
	}
	if load != "loaded" {
		return fmt.Errorf("install: %s LoadState=%s, want loaded", runService, load)
	}
	timerState, err := run(ctx, "systemctl", "--user", "show", runTimer, "-p", "ActiveState", "--value")
	if err != nil {
		return err
	}
	if timerState != "active" {
		return fmt.Errorf("install: %s ActiveState=%s, want active", runTimer, timerState)
	}
	next, err := run(ctx, "systemctl", "--user", "show", runTimer, "-p", "NextElapseUSecRealtime", "--value")
	if err != nil {
		return err
	}
	if next == "" || next == "0" || next == "n/a" {
		return fmt.Errorf("install: %s has no scheduled next trigger", runTimer)
	}
	report(out, "fetch is armed: %s loaded, %s active (next %s)", runService, runTimer, next)
	return nil
}

// assertStartedAfter compares the service's monotonic start stamp against the
// same clock the marker was taken on, so an update that left the old process
// running is caught rather than reported as a success.
func assertStartedAfter(ctx context.Context, started string, marker time.Time) error {
	stamp, err := strconv.ParseInt(started, 10, 64)
	if err != nil || stamp == 0 {
		return nil
	}
	wall, err := run(ctx, "systemctl", "--user", "show", apiService, "-p", "ExecMainStartTimestamp", "--value")
	if err != nil || wall == "" {
		return nil
	}
	// systemd renders "Mon 2026-08-04 23:59:00 CST"; the day name and zone name
	// make it unparseable by a fixed layout, so only the date and time are read.
	fields := strings.Fields(wall)
	if len(fields) < 3 {
		return nil
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", fields[1]+" "+fields[2], time.Local)
	if err != nil {
		return nil
	}
	if at.Before(marker.Add(-2 * time.Second)) {
		return fmt.Errorf("install: %s did not restart (started %s, before the replacement)", apiService, wall)
	}
	return nil
}

// reportLinger only reports: enabling linger needs root, so it is the
// operator's action. Without it the timer and the API stop at logout and the
// daily fetch quietly never happens.
func reportLinger(ctx context.Context, out io.Writer) {
	user := os.Getenv("USER")
	if user == "" {
		return
	}
	state, err := run(ctx, "loginctl", "show-user", user, "-p", "Linger", "--value")
	if err != nil || state == "yes" {
		return
	}
	report(out, "linger is not enabled; run: sudo loginctl enable-linger %s", user)
}

func (systemdScheduler) hints(layout paths.Layout) []string {
	return []string{
		"fetch on demand: systemctl --user start " + runService,
		"diagnostics:     journalctl --user -u " + apiService,
		"paths:           " + layout.Binary + " paths",
	}
}
