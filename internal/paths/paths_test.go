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
