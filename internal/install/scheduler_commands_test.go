package install

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// recordCommands swaps the service-manager exit for a recorder, so the commands
// a scheduler sends can be read back without systemd or Task Scheduler present.
// reply supplies the output a command would have printed.
func recordCommands(t *testing.T, reply func(command string) string) *[]string {
	t.Helper()
	var commands []string
	original := run
	run = func(_ context.Context, name string, args ...string) (string, error) {
		command := strings.Join(append([]string{name}, args...), " ")
		commands = append(commands, command)
		return reply(command), nil
	}
	t.Cleanup(func() { run = original })
	return &commands
}

// listeningLayout is a sandboxed layout whose configured API address accepts
// connections, so the settling wait after a restart returns at once.
func listeningLayout(t *testing.T) paths.Layout {
	t.Helper()
	layout := sandbox(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	for _, dir := range []string{filepath.Dir(layout.Config), layout.LibDir, layout.UnitDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	config := "api:\n  addr: " + listener.Addr().String() + "\n  token: test-token\n"
	if err := os.WriteFile(layout.Config, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return layout
}

// stashDefinitions leaves the material rollback restores from.
func stashDefinitions(t *testing.T, layout paths.Layout, dir string, names []string) {
	t.Helper()
	stash := filepath.Join(layout.LibDir, dir)
	if err := os.MkdirAll(stash, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", stash, err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(stash, name), []byte("stashed "+name), 0o600); err != nil {
			t.Fatalf("stash %s: %v", name, err)
		}
	}
}

// Install, update and rollback each end in start or restart, and each must leave
// the daily fetch armed whatever state the schedule was in — an operator may have
// disabled it — while never running a fetch. Arming a schedule and running the
// job it schedules are different acts: a redeploy that fetched would spend Agent
// quota on every install.
func TestSchedulersArmTheFetchScheduleWithoutRunningAFetch(t *testing.T) {
	windowsTaskNames := make([]string, 0, len(windowsTasks))
	for _, task := range windowsTasks {
		windowsTaskNames = append(windowsTaskNames, task.template)
	}
	platforms := []struct {
		name     string
		sched    scheduler
		stashDir string
		stashed  []string
		reply    func(string) string
		armed    []string
		fetches  func(string) bool
	}{
		{
			name:     "systemd",
			sched:    systemdScheduler{},
			stashDir: "units.prev",
			stashed:  systemdUnits,
			reply:    func(string) string { return "" },
			armed: []string{
				"systemctl --user enable " + runTimer,
				"systemctl --user restart " + runTimer,
			},
			fetches: func(command string) bool {
				return strings.Contains(command, runService) && !strings.Contains(command, " show ")
			},
		},
		{
			name:     "task scheduler",
			sched:    taskScheduler{},
			stashDir: "tasks.prev",
			stashed:  windowsTaskNames,
			reply: func(command string) string {
				if strings.Contains(command, ").State") {
					return "Ready"
				}
				return ""
			},
			armed: []string{"Enable-ScheduledTask -TaskPath '" + taskFolder + "' -TaskName '" + runTask + "'"},
			fetches: func(command string) bool {
				return strings.Contains(command, "Start-ScheduledTask") &&
					strings.Contains(command, "-TaskName '"+runTask+"'")
			},
		},
	}
	// The repository checkout carries the same templates the artifact ships.
	checkout, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for _, platform := range platforms {
		routes := []struct {
			name  string
			steps func(context.Context, paths.Layout) error
		}{
			{"install", func(ctx context.Context, layout paths.Layout) error {
				if err := platform.sched.mount(ctx, layout, checkout, io.Discard); err != nil {
					return err
				}
				return platform.sched.start(ctx, layout, io.Discard)
			}},
			{"update", func(ctx context.Context, layout paths.Layout) error {
				if err := platform.sched.mount(ctx, layout, checkout, io.Discard); err != nil {
					return err
				}
				return platform.sched.restart(ctx, layout, io.Discard)
			}},
			{"rollback", func(ctx context.Context, layout paths.Layout) error {
				stashDefinitions(t, layout, platform.stashDir, platform.stashed)
				if err := platform.sched.restoreDefinitions(ctx, layout, io.Discard); err != nil {
					return err
				}
				return platform.sched.restart(ctx, layout, io.Discard)
			}},
		}
		for _, route := range routes {
			t.Run(platform.name+"/"+route.name, func(t *testing.T) {
				layout := listeningLayout(t)
				commands := recordCommands(t, platform.reply)
				if err := route.steps(context.Background(), layout); err != nil {
					t.Fatalf("%s: %v", route.name, err)
				}
				for _, want := range platform.armed {
					if !containsCommand(*commands, want) {
						t.Errorf("no command arms the fetch schedule with %q; sent:\n%s", want, strings.Join(*commands, "\n"))
					}
				}
				for _, command := range *commands {
					if platform.fetches(command) {
						t.Errorf("%s runs a fetch: %q", route.name, command)
					}
				}
			})
		}
	}
}

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if strings.Contains(command, want) {
			return true
		}
	}
	return false
}
