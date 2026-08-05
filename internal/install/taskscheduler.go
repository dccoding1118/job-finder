package install

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

const (
	taskFolder = `\jobfinder\`
	apiTask    = "api"
	runTask    = "run"
)

var windowsTasks = []struct{ name, template string }{
	{apiTask, "jobfinder-api.xml"},
	{runTask, "jobfinder-run.xml"},
}

// taskScheduler is the Windows counterpart of the systemd units: one task that
// starts the API at logon and keeps it up, one that triggers the daily fetch.
//
// Everything is driven through PowerShell's ScheduledTasks module rather than
// schtasks.exe, because schtasks reports state in the display language of the
// installation — parsing it would make the effect-surface checks pass or fail
// depending on the user's locale.
type taskScheduler struct{}

func (taskScheduler) name() string { return "Windows Task Scheduler" }

func (taskScheduler) preflight(ctx context.Context) error {
	if !have("powershell") {
		return fmt.Errorf("install: powershell not found; it is required to register the scheduled tasks")
	}
	if _, err := powershell(ctx, "Get-Command Register-ScheduledTask -ErrorAction Stop | Out-Null"); err != nil {
		return fmt.Errorf("install: the ScheduledTasks PowerShell module is unavailable: %w", err)
	}
	return nil
}

func (taskScheduler) mount(ctx context.Context, layout paths.Layout, assetDir string, out io.Writer) error {
	templates, err := asset(assetDir, "windows")
	if err != nil {
		return err
	}
	stash := filepath.Join(layout.LibDir, "tasks.prev")
	if err := os.MkdirAll(stash, 0o700); err != nil {
		return fmt.Errorf("install: create %s: %w", stash, err)
	}
	for _, task := range windowsTasks {
		// Export the outgoing definition before replacing it, so rollback has the
		// same material the systemd path keeps in units.prev.
		if exported, err := powershell(ctx, fmt.Sprintf(
			"Export-ScheduledTask -TaskPath '%s' -TaskName '%s'", taskFolder, task.name,
		)); err == nil && exported != "" {
			if err := os.WriteFile(filepath.Join(stash, task.template), []byte(exported), 0o600); err != nil {
				return fmt.Errorf("install: stash task %s: %w", task.name, err)
			}
		}
		template, err := os.ReadFile(filepath.Join(templates, task.template)) // #nosec G304 -- template path derives from the artifact directory.
		if err != nil {
			return fmt.Errorf("install: read task template %s: %w", task.template, err)
		}
		rendered := renderTask(string(template), layout)
		if err := registerTask(ctx, layout, task.name, rendered); err != nil {
			return err
		}
	}
	report(out, "registered the scheduled tasks under %s", strings.Trim(taskFolder, `\`))
	return nil
}

// renderTask substitutes the layout into the task XML. The placeholders are
// explicit markers rather than paths to be pattern-matched, because an XML
// document offers no equivalent of systemd's %h specifier.
func renderTask(template string, layout paths.Layout) string {
	user := os.Getenv("USERNAME")
	if domain := os.Getenv("USERDOMAIN"); domain != "" && user != "" {
		user = domain + `\` + user
	}
	return strings.NewReplacer(
		"{{BINARY}}", xmlEscape(layout.Binary),
		"{{CONFIG}}", xmlEscape(layout.Config),
		"{{DATA_DIR}}", xmlEscape(layout.DataDir),
		"{{USER}}", xmlEscape(user),
	).Replace(template)
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(value)
}

// registerTask writes the rendered XML to a temporary file and registers it.
// Register-ScheduledTask reads the XML as one argument, and a path with spaces
// in it is far easier to pass safely than a multi-line document.
func registerTask(ctx context.Context, layout paths.Layout, name, xml string) error {
	file := filepath.Join(layout.LibDir, "task-"+name+".xml")
	if err := os.WriteFile(file, []byte(xml), 0o600); err != nil { // #nosec G703 -- path comes from the resolved layout.
		return fmt.Errorf("install: stage task %s: %w", name, err)
	}
	defer func() { _ = os.Remove(file) }()
	_, err := powershell(ctx, fmt.Sprintf(
		"Register-ScheduledTask -Xml (Get-Content -Raw -LiteralPath '%s') -TaskPath '%s' -TaskName '%s' -Force | Out-Null",
		file, taskFolder, name,
	))
	if err != nil {
		return fmt.Errorf("install: register scheduled task %s: %w", name, err)
	}
	return nil
}

func (taskScheduler) restoreDefinitions(ctx context.Context, layout paths.Layout, out io.Writer) error {
	stash := filepath.Join(layout.LibDir, "tasks.prev")
	restored := 0
	for _, task := range windowsTasks {
		xml, err := os.ReadFile(filepath.Join(stash, task.template)) // #nosec G304 -- path derives from the resolved layout.
		if err != nil {
			continue
		}
		if err := registerTask(ctx, layout, task.name, string(xml)); err != nil {
			return err
		}
		restored++
	}
	if restored == 0 {
		report(out, "no stashed task definitions at %s; tasks left as they are", stash)
		return nil
	}
	report(out, "restored %d task definitions from %s", restored, stash)
	return nil
}

func (t taskScheduler) start(ctx context.Context, layout paths.Layout, out io.Writer) error {
	if err := t.restartTask(ctx, layout); err != nil {
		return err
	}
	report(out, "started the %s task; the fetch task is armed for its daily trigger", apiTask)
	return nil
}

func (t taskScheduler) restart(ctx context.Context, layout paths.Layout, out io.Writer) error {
	if err := t.restartTask(ctx, layout); err != nil {
		return err
	}
	report(out, "restarted the %s task", apiTask)
	return nil
}

// restartTask stops the API task and waits for its process to actually go away
// before starting it again. Start-ScheduledTask on a running task is a no-op,
// which is the Windows shape of the same trap "enable --now" sets on systemd:
// the new binary sits on disk while the old one keeps serving.
func (t taskScheduler) restartTask(ctx context.Context, layout paths.Layout) error {
	if _, err := powershell(ctx, fmt.Sprintf(
		"Stop-ScheduledTask -TaskPath '%s' -TaskName '%s' -ErrorAction SilentlyContinue", taskFolder, apiTask,
	)); err != nil {
		return err
	}
	waitFor(ctx, 15*time.Second, func() bool {
		state, err := t.taskState(ctx, apiTask)
		return err == nil && state != "Running"
	})
	if _, err := powershell(ctx, fmt.Sprintf(
		"Start-ScheduledTask -TaskPath '%s' -TaskName '%s'", taskFolder, apiTask,
	)); err != nil {
		return fmt.Errorf("install: start the %s task: %w", apiTask, err)
	}
	waitFor(ctx, 30*time.Second, func() bool { return apiResponding(layout) })
	return nil
}

func (taskScheduler) taskState(ctx context.Context, name string) (string, error) {
	return powershell(ctx, fmt.Sprintf(
		"(Get-ScheduledTask -TaskPath '%s' -TaskName '%s' -ErrorAction Stop).State", taskFolder, name,
	))
}

func (t taskScheduler) assertEffective(ctx context.Context, layout paths.Layout, marker time.Time, out io.Writer) error {
	state, err := t.taskState(ctx, apiTask)
	if err != nil {
		return fmt.Errorf("install: read the %s task state: %w", apiTask, err)
	}
	if state != "Running" {
		return fmt.Errorf("install: the %s task is %s, want Running", apiTask, state)
	}
	// Win32_Process carries the executable path and the start time, which is the
	// Windows equivalent of reading /proc/<pid>/exe: the point is to prove that
	// the process in memory is the binary just placed, not that a file was copied.
	line, err := powershell(ctx, "$p = Get-CimInstance Win32_Process -Filter \"Name='jobfinder.exe'\" | "+
		"Where-Object { $_.CommandLine -like '*serve*' } | Select-Object -First 1; "+
		"if ($null -eq $p) { throw 'no serve process' }; "+
		"'{0}|{1}|{2}' -f $p.ProcessId, $p.ExecutablePath, $p.CreationDate.ToUniversalTime().ToString('o')")
	if err != nil {
		return fmt.Errorf("install: locate the running serve process: %w", err)
	}
	fields := strings.Split(line, "|")
	if len(fields) != 3 {
		return fmt.Errorf("install: unexpected process description %q", line)
	}
	pid, executable, created := fields[0], fields[1], fields[2]
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(layout.Binary)) {
		return fmt.Errorf("install: the running process (pid %s) executes %q, want %q — a stale process is still live", pid, executable, layout.Binary)
	}
	if started, parseErr := time.Parse(time.RFC3339Nano, created); parseErr == nil {
		if started.Before(marker.Add(-2 * time.Second)) {
			return fmt.Errorf("install: the %s task did not restart (process started %s, before the replacement)", apiTask, created)
		}
	}
	report(out, "API task effective: pid %s running %s", pid, layout.Binary)

	next, err := powershell(ctx, fmt.Sprintf(
		"(Get-ScheduledTaskInfo -TaskPath '%s' -TaskName '%s' -ErrorAction Stop).NextRunTime.ToString('o')", taskFolder, runTask,
	))
	if err != nil || next == "" {
		return fmt.Errorf("install: the %s task has no scheduled next trigger: %w", runTask, err)
	}
	report(out, "fetch is armed: the %s task next triggers at %s", runTask, next)
	return nil
}

func (taskScheduler) hints(layout paths.Layout) []string {
	return []string{
		`fetch on demand: Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'`,
		"diagnostics:     " + layout.LogFile,
		"paths:           " + layout.Binary + " paths",
	}
}

// powershell runs one statement non-interactively. -NoProfile keeps a user's
// profile script from changing what the installer sees, and stopping on error
// turns a silently-ignored cmdlet failure into a failed install.
func powershell(ctx context.Context, script string) (string, error) {
	return run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", "$ErrorActionPreference='Stop'; "+script)
}
