package liveverify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// platform is the only part of the verification that differs by operating
// system: how the installed API service and the scheduled fetch are driven and
// inspected. Both implementations compile everywhere — they are process
// invocations, not platform APIs — mirroring internal/install.
type platform interface {
	name() string
	preflight(ctx context.Context) error
	apiRunning(ctx context.Context) (bool, error)
	startAPI(ctx context.Context) error
	// stopAPI returns once the service process is gone.
	stopAPI(ctx context.Context) error
	// restartAPI waits for the old process to go away before starting again.
	restartAPI(ctx context.Context) error
	// fetchDisabled reports whether the fetch cannot be triggered by hand.
	fetchDisabled(ctx context.Context) (bool, error)
	enableFetch(ctx context.Context) error
	disableFetch(ctx context.Context) error
	// triggerFetch starts the installed fetch the way the schedule does.
	triggerFetch(ctx context.Context) error
	fetchRunning(ctx context.Context) (bool, error)
	// checkDefinitions proves the installed scheduling definitions point at the
	// installed binary and config.
	checkDefinitions(ctx context.Context, layout paths.Layout) (string, error)
	// apiExecutable is the executable of the running API process.
	apiExecutable(ctx context.Context, layout paths.Layout) (string, error)
}

func newPlatform(goos string, layout paths.Layout) platform {
	if goos == "windows" {
		return taskScheduler{layout: layout}
	}
	return systemdUser{}
}

// run is the single exit to the service manager; a variable so tests can
// record commands without one.
var run = func(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed service-manager commands with layout-derived arguments.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		if detail == "" {
			return strings.TrimSpace(stdout.String()), fmt.Errorf("%s: %w", name, err)
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("%s: %w: %s", name, err, detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ---------------------------------------------------------------------------
// systemd user units

const (
	apiService = "jobfinder-api.service"
	runService = "jobfinder-run.service"
	runTimer   = "jobfinder-run.timer"
)

type systemdUser struct{}

func (systemdUser) name() string { return "systemd user units" }

func (systemdUser) preflight(ctx context.Context) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found")
	}
	if _, err := run(ctx, "systemctl", "--user", "show-environment"); err != nil {
		return fmt.Errorf("the user systemd bus is unavailable: %w", err)
	}
	return nil
}

func (systemdUser) apiRunning(ctx context.Context) (bool, error) {
	state, _ := run(ctx, "systemctl", "--user", "is-active", apiService)
	return state == "active", nil
}

func (systemdUser) startAPI(ctx context.Context) error {
	_, err := run(ctx, "systemctl", "--user", "start", apiService)
	return err
}

func (systemdUser) stopAPI(ctx context.Context) error {
	_, err := run(ctx, "systemctl", "--user", "stop", apiService)
	return err
}

func (systemdUser) restartAPI(ctx context.Context) error {
	_, err := run(ctx, "systemctl", "--user", "restart", apiService)
	return err
}

// fetchDisabled is always false: a disabled timer does not stop the one-shot
// service from being started by hand.
func (systemdUser) fetchDisabled(context.Context) (bool, error) { return false, nil }
func (systemdUser) enableFetch(context.Context) error           { return nil }
func (systemdUser) disableFetch(context.Context) error          { return nil }

// triggerFetch starts the one-shot, which blocks until the fetch ends. A failed
// fetch is judged from its run row, so the start error is not the verdict.
func (systemdUser) triggerFetch(ctx context.Context) error {
	_, _ = run(ctx, "systemctl", "--user", "start", runService)
	return nil
}

func (systemdUser) fetchRunning(ctx context.Context) (bool, error) {
	state, _ := run(ctx, "systemctl", "--user", "is-active", runService)
	return state == "activating", nil
}

func (systemdUser) checkDefinitions(ctx context.Context, layout paths.Layout) (string, error) {
	units := map[string]string{}
	for _, unit := range []string{apiService, runService, runTimer} {
		data, err := os.ReadFile(filepath.Join(layout.UnitDir, unit)) // #nosec G304 -- path derives from the resolved layout.
		if err != nil {
			return "", fmt.Errorf("no installed unit %s: %w", unit, err)
		}
		units[unit] = string(data)
	}
	expectations := []struct{ unit, line string }{
		{apiService, "ExecStart=" + layout.ServiceBinary + " serve --config " + layout.Config},
		{runService, "ExecStart=" + layout.ServiceBinary + " run --config " + layout.Config + " --trigger timer"},
		{runTimer, "OnCalendar=*-*-* 08:30:00 Asia/Taipei"},
	}
	for _, expected := range expectations {
		if !containsLine(units[expected.unit], expected.line) {
			return "", fmt.Errorf("the installed %s does not carry %q", expected.unit, expected.line)
		}
	}
	args := []string{"--user", "verify"}
	for _, unit := range []string{apiService, runService, runTimer} {
		args = append(args, filepath.Join(layout.UnitDir, unit))
	}
	if _, err := run(ctx, "systemd-analyze", args...); err != nil {
		return "", fmt.Errorf("the installed units failed systemd-analyze verify: %w", err)
	}
	return "API／run／timer 三個 unit 通過 systemd-analyze，指向安裝的 binary 與設定，timer 為每日 08:30 台北時間", nil
}

func (systemdUser) apiExecutable(ctx context.Context, _ paths.Layout) (string, error) {
	pid, err := run(ctx, "systemctl", "--user", "show", apiService, "-p", "MainPID", "--value")
	if err != nil {
		return "", err
	}
	if n, convErr := strconv.Atoi(pid); convErr != nil || n <= 0 {
		return "", fmt.Errorf("the API service has no main process")
	}
	return os.Readlink(filepath.Join("/proc", pid, "exe"))
}

func containsLine(text, line string) bool {
	for _, candidate := range strings.Split(text, "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Windows Task Scheduler

const (
	taskFolder = `\jobfinder\`
	apiTask    = "api"
	runTask    = "run"
)

// taskScheduler drives the two tasks through the ScheduledTasks cmdlets: state
// is read as object properties, never from schtasks text, which is localized.
type taskScheduler struct{ layout paths.Layout }

func (taskScheduler) name() string { return "Windows Task Scheduler" }

func powershell(ctx context.Context, script string) (string, error) {
	return run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "+script)
}

func (taskScheduler) preflight(ctx context.Context) error {
	if _, err := exec.LookPath("powershell"); err != nil {
		return fmt.Errorf("powershell not found")
	}
	if _, err := powershell(ctx, fmt.Sprintf("Get-ScheduledTask -TaskPath '%s' | Out-Null", taskFolder)); err != nil {
		return fmt.Errorf("the %s task folder cannot be read: %w", taskFolder, err)
	}
	return nil
}

func (taskScheduler) taskState(ctx context.Context, task string) (string, error) {
	return powershell(ctx, fmt.Sprintf("[string](Get-ScheduledTask -TaskPath '%s' -TaskName '%s').State", taskFolder, task))
}

// serveProcess finds the running API process by its executable name and the
// serve subcommand, the same lookup the installer's effect check uses.
func serveProcess(ctx context.Context, layout paths.Layout) (string, error) {
	return powershell(ctx, fmt.Sprintf("$p = Get-CimInstance Win32_Process -Filter \"Name='%s'\" | "+
		"Where-Object { $_.CommandLine -like '*serve*' } | Select-Object -First 1; if ($null -ne $p) { $p.ExecutablePath }",
		filepath.Base(layout.ServiceBinary)))
}

func (t taskScheduler) apiRunning(ctx context.Context) (bool, error) {
	state, err := t.taskState(ctx, apiTask)
	return state == "Running", err
}

func (taskScheduler) startAPI(ctx context.Context) error {
	_, err := powershell(ctx, fmt.Sprintf("Start-ScheduledTask -TaskPath '%s' -TaskName '%s'", taskFolder, apiTask))
	return err
}

func (t taskScheduler) stopAPI(ctx context.Context) error {
	if _, err := powershell(ctx, fmt.Sprintf("Stop-ScheduledTask -TaskPath '%s' -TaskName '%s' -ErrorAction SilentlyContinue", taskFolder, apiTask)); err != nil {
		return err
	}
	gone := waitUntil(ctx, 30*time.Second, time.Second, func() bool {
		path, lookErr := serveProcess(ctx, t.layout)
		return lookErr == nil && path == ""
	})
	if !gone {
		return fmt.Errorf("the API process did not exit after the task was stopped")
	}
	return nil
}

// restartAPI stops before starting: Start-ScheduledTask on a running task is a
// no-op, which would leave the old process serving.
func (t taskScheduler) restartAPI(ctx context.Context) error {
	if err := t.stopAPI(ctx); err != nil {
		return err
	}
	return t.startAPI(ctx)
}

func (t taskScheduler) fetchDisabled(ctx context.Context) (bool, error) {
	state, err := t.taskState(ctx, runTask)
	return state == "Disabled", err
}

func (taskScheduler) enableFetch(ctx context.Context) error {
	_, err := powershell(ctx, fmt.Sprintf("Enable-ScheduledTask -TaskPath '%s' -TaskName '%s' | Out-Null", taskFolder, runTask))
	return err
}

func (taskScheduler) disableFetch(ctx context.Context) error {
	_, err := powershell(ctx, fmt.Sprintf("Disable-ScheduledTask -TaskPath '%s' -TaskName '%s' | Out-Null", taskFolder, runTask))
	return err
}

func (taskScheduler) triggerFetch(ctx context.Context) error {
	_, err := powershell(ctx, fmt.Sprintf("Start-ScheduledTask -TaskPath '%s' -TaskName '%s'", taskFolder, runTask))
	return err
}

func (t taskScheduler) fetchRunning(ctx context.Context) (bool, error) {
	state, err := t.taskState(ctx, runTask)
	return state == "Running", err
}

func (taskScheduler) checkDefinitions(ctx context.Context, layout paths.Layout) (string, error) {
	for task, subcommand := range map[string]string{apiTask: "serve", runTask: "run"} {
		line, err := powershell(ctx, fmt.Sprintf("$a = @((Get-ScheduledTask -TaskPath '%s' -TaskName '%s').Actions)[0]; '{0}|{1}' -f $a.Execute, $a.Arguments", taskFolder, task))
		if err != nil {
			return "", fmt.Errorf("the %s task is not registered under %s: %w", task, taskFolder, err)
		}
		execute, arguments, _ := strings.Cut(line, "|")
		if !strings.EqualFold(filepath.Clean(execute), filepath.Clean(layout.ServiceBinary)) {
			return "", fmt.Errorf("the %s task runs %s, want %s", task, execute, layout.ServiceBinary)
		}
		if !strings.HasPrefix(arguments, subcommand+" ") || !strings.Contains(strings.ToLower(arguments), strings.ToLower(layout.Config)) {
			return "", fmt.Errorf("the %s task does not start %q with the installed config", task, subcommand)
		}
	}
	return fmt.Sprintf("api 與 run 兩個工作存在，action 指向安裝的 %s 與設定", filepath.Base(layout.ServiceBinary)), nil
}

func (taskScheduler) apiExecutable(ctx context.Context, layout paths.Layout) (string, error) {
	path, err := serveProcess(ctx, layout)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("no running serve process")
	}
	return path, nil
}
