package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestResolveXDGHonoursOverrides(t *testing.T) {
	layout, err := resolve("linux", "/home/u", env(map[string]string{
		"XDG_CONFIG_HOME": "/cfg",
		"XDG_DATA_HOME":   "/data",
	}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join("/cfg", "jobfinder", "config.yaml"); layout.Config != want {
		t.Fatalf("config = %q, want %q", layout.Config, want)
	}
	if want := filepath.Join("/data", "jobfinder", "jobs.db"); layout.DB != want {
		t.Fatalf("db = %q, want %q", layout.DB, want)
	}
	if want := filepath.Join("/home/u", ".local", "bin", "jobfinder"); layout.Binary != want {
		t.Fatalf("binary = %q, want %q", layout.Binary, want)
	}
	if want := filepath.Join("/cfg", "systemd", "user"); layout.UnitDir != want {
		t.Fatalf("unit dir = %q, want %q", layout.UnitDir, want)
	}
}

func TestResolveXDGDefaults(t *testing.T) {
	layout, err := resolve("darwin", "/home/u", env(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join("/home/u", ".config", "jobfinder", "config.yaml"); layout.Config != want {
		t.Fatalf("config = %q, want %q", layout.Config, want)
	}
	if want := filepath.Join("/home/u", ".local", "share", "jobfinder", "logs", "jobfinder.log"); layout.LogFile != want {
		t.Fatalf("log file = %q, want %q", layout.LogFile, want)
	}
}

// The Windows layout must be assertable from a Linux test run: its whole point
// is that path bugs stop being things only a release build can discover.
func TestResolveWindowsUsesLocalAppData(t *testing.T) {
	layout, err := resolve("windows", `C:\Users\u`, env(map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	root := filepath.Join(`C:\Users\u\AppData\Local`, "jobfinder")
	if want := filepath.Join(root, "config", "config.yaml"); layout.Config != want {
		t.Fatalf("config = %q, want %q", layout.Config, want)
	}
	if want := filepath.Join(root, "bin", "jobfinder.exe"); layout.Binary != want {
		t.Fatalf("binary = %q, want %q", layout.Binary, want)
	}
	if !strings.HasSuffix(layout.Previous, "jobfinder.exe.prev") {
		t.Fatalf("previous = %q, want the .exe rollback copy", layout.Previous)
	}
	if layout.UnitDir != "" {
		t.Fatalf("unit dir = %q, want empty on Windows", layout.UnitDir)
	}
}

func TestResolveWindowsFallsBackToProfile(t *testing.T) {
	layout, err := resolve("windows", `C:\Users\u`, env(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(`C:\Users\u`, "AppData", "Local", "jobfinder", "data", "jobs.db"); layout.DB != want {
		t.Fatalf("db = %q, want %q", layout.DB, want)
	}
}

// Only Windows needs a second executable. Everywhere else the two names must
// resolve to the same file, because that equality is what lets the installer,
// the templates and the effect-surface checks name the service binary
// unconditionally without changing what any other platform does.
func TestServiceBinaryIsSeparateOnlyOnWindows(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		layout, err := resolve(goos, "/home/u", env(nil))
		if err != nil {
			t.Fatalf("resolve %s: %v", goos, err)
		}
		if layout.SeparateServiceBinary() {
			t.Fatalf("%s: service binary = %q, want the same file as %q", goos, layout.ServiceBinary, layout.Binary)
		}
		if layout.ServicePrevious != layout.Previous || layout.ServiceBad != layout.Bad {
			t.Fatalf("%s: the rollback copies must coincide too", goos)
		}
	}
	windows, err := resolve("windows", `C:\Users\u`, env(nil))
	if err != nil {
		t.Fatalf("resolve windows: %v", err)
	}
	if !windows.SeparateServiceBinary() {
		t.Fatal("Windows must install a separate console-free service binary")
	}
	if want := filepath.Join(filepath.Dir(windows.Binary), "jobfinderw.exe"); windows.ServiceBinary != want {
		t.Fatalf("service binary = %q, want %q", windows.ServiceBinary, want)
	}
	if !strings.HasSuffix(windows.ServicePrevious, "jobfinderw.exe.prev") {
		t.Fatalf("service previous = %q, want its own rollback copy", windows.ServicePrevious)
	}
}

func TestResolveRejectsEmptyHome(t *testing.T) {
	if _, err := resolve("linux", "", env(nil)); err == nil {
		t.Fatal("want an error when the home directory is empty")
	}
}

func TestRowsCoverEveryUserFacingLocation(t *testing.T) {
	layout, err := resolve("linux", "/home/u", env(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	seen := map[string]bool{}
	for _, row := range layout.Rows() {
		seen[row[0]] = true
	}
	for _, label := range []string{"config", "profile", "denylist", "database", "log file", "binary", "systemd units"} {
		if !seen[label] {
			t.Fatalf("paths output is missing %q", label)
		}
	}
	windows, err := resolve("windows", `C:\Users\u`, env(nil))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, row := range windows.Rows() {
		if row[0] == "systemd units" {
			t.Fatal("Windows paths output must not mention systemd units")
		}
	}
}
