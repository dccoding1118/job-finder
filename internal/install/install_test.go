package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dccoding1118/job-finder/internal/paths"
)

func linuxLayout(root string) paths.Layout {
	return paths.Layout{
		OS:        "unix",
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
		Config:    filepath.Join(root, "config", "config.yaml"),
		Profile:   filepath.Join(root, "config", "profile.yaml"),
		Denylist:  filepath.Join(root, "config", "pii-denylist.txt"),
		DB:        filepath.Join(root, "data", "jobs.db"),
		LogFile:   filepath.Join(root, "data", "logs", "jobfinder.log"),
		Binary:    filepath.Join(root, "bin", "jobfinder"),
	}
}

const exampleConfig = `db:
  path: .local-dev/jobfinder.db
profile:
  path: .local-dev/profile.yaml
  denylist: .local-dev/pii-denylist.txt
log:
  file: ""
api:
  token: CHANGE_ME
`

func TestRenderConfigSubstitutesEveryPlaceholder(t *testing.T) {
	layout := linuxLayout("/opt/jf")
	rendered, err := renderConfig(exampleConfig, layout, "deadbeef")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"  path: " + layout.DB,
		"  path: " + layout.Profile,
		"  denylist: " + layout.Denylist,
		"  token: deadbeef",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, ".local-dev") {
		t.Fatalf("a development path survived rendering:\n%s", rendered)
	}
	// Linux reads the same records from journald, so the file sink stays off.
	if !strings.Contains(rendered, `  file: ""`) {
		t.Fatalf("the Unix render must leave log.file empty:\n%s", rendered)
	}
}

// Task Scheduler discards a task's output, so an install that left log.file
// empty on Windows would leave a failed scheduled fetch with nowhere to report.
func TestRenderConfigEnablesTheFileSinkOnWindows(t *testing.T) {
	layout := linuxLayout("/opt/jf")
	layout.OS = "windows"
	rendered, err := renderConfig(exampleConfig, layout, "deadbeef")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rendered, "  file: "+layout.LogFile) {
		t.Fatalf("the Windows render must point log.file at the layout:\n%s", rendered)
	}
}

// A placeholder that stops matching must fail the install rather than quietly
// leaving it pointing at a relative development path.
func TestRenderConfigRefusesAnUnmatchedPlaceholder(t *testing.T) {
	_, err := renderConfig("db:\n  path: somewhere/else\n", linuxLayout("/opt/jf"), "deadbeef")
	if err == nil || !strings.Contains(err.Error(), "want exactly 1") {
		t.Fatalf("err = %v, want a report of the unmatched placeholder", err)
	}
}

func TestAssetPrefersTheArtifactLayoutAndFallsBackToTheCheckout(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "deploy", "production", "systemd")
	if err := os.MkdirAll(checkout, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resolved, err := asset(root, "systemd")
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	if resolved != checkout {
		t.Fatalf("asset = %q, want the checkout location %q", resolved, checkout)
	}
	artifact := filepath.Join(root, "systemd")
	if mkdirErr := os.MkdirAll(artifact, 0o750); mkdirErr != nil {
		t.Fatalf("mkdir: %v", mkdirErr)
	}
	if resolved, err = asset(root, "systemd"); err != nil || resolved != artifact {
		t.Fatalf("asset = (%q, %v), want the artifact location %q", resolved, err, artifact)
	}
	if _, err := asset(root, "windows"); err == nil {
		t.Fatal("want an error when the templates are absent")
	}
}

func TestAssertLoopbackRefusesARoutableAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8686", "[::1]:8686"} {
		if err := assertLoopback(addr); err != nil {
			t.Fatalf("assertLoopback(%q) = %v, want nil", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8686", "192.168.1.10:8686", "example.test:8686", "8686"} {
		if err := assertLoopback(addr); err == nil {
			t.Fatalf("assertLoopback(%q) = nil, want an error", addr)
		}
	}
}

func TestRenderUnitReplacesEveryJobfinderOwnedPath(t *testing.T) {
	layout := linuxLayout("/opt/jf")
	template := "ExecStart=%h/.local/lib/jobfinder/jobfinder serve --config %h/.config/jobfinder/config.yaml\n" +
		"WorkingDirectory=%h/.local/share/jobfinder\n" +
		"Environment=PATH=%h/.local/bin:%h/.local/share/mise/shims\n"
	rendered := renderUnit(template, layout)
	if !strings.Contains(rendered, "ExecStart="+layout.Binary+" serve --config "+layout.Config) {
		t.Fatalf("ExecStart was not rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "WorkingDirectory="+layout.DataDir) {
		t.Fatalf("WorkingDirectory was not rendered:\n%s", rendered)
	}
	// The PATH line keeps systemd's %h: those directories live under $HOME
	// whatever XDG says, and rewriting them would break the Agent CLIs.
	if !strings.Contains(rendered, "Environment=PATH=%h/.local/bin:%h/.local/share/mise/shims") {
		t.Fatalf("the PATH specifier must be left alone:\n%s", rendered)
	}
}

func TestRenderTaskSubstitutesAndEscapes(t *testing.T) {
	layout := linuxLayout(`C:\Users\a b`)
	layout.OS = "windows"
	t.Setenv("USERNAME", "user&name")
	t.Setenv("USERDOMAIN", "")
	rendered := renderTask("<Command>{{BINARY}}</Command><Arguments>run --config \"{{CONFIG}}\"</Arguments>"+
		"<WorkingDirectory>{{DATA_DIR}}</WorkingDirectory><UserId>{{USER}}</UserId>", layout)
	if strings.Contains(rendered, "{{") {
		t.Fatalf("a placeholder survived rendering:\n%s", rendered)
	}
	if !strings.Contains(rendered, "<UserId>user&amp;name</UserId>") {
		t.Fatalf("the user name was not XML-escaped:\n%s", rendered)
	}
	if !strings.Contains(rendered, "<Command>"+layout.Binary+"</Command>") {
		t.Fatalf("the binary path was not substituted:\n%s", rendered)
	}
}

func TestNewSchedulerPicksTheMechanismPerPlatform(t *testing.T) {
	if _, ok := newScheduler("windows").(taskScheduler); !ok {
		t.Fatal("Windows must use Task Scheduler")
	}
	if _, ok := newScheduler("linux").(systemdScheduler); !ok {
		t.Fatal("Linux must use systemd user units")
	}
}

func TestCopyFileReplacesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new")
	dst := filepath.Join(dir, "sub", "installed")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	if err := os.WriteFile(src, []byte("newer"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("second copy: %v", err)
	}
	contents, err := os.ReadFile(dst) // #nosec G304 -- path is inside the test's temporary directory.
	if err != nil || string(contents) != "newer" {
		t.Fatalf("destination = %q, %v", contents, err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(dst)
		if statErr != nil {
			t.Fatalf("stat: %v", statErr)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
		}
	}
}

// Installing the resident copy over itself would truncate the very file being
// read, so the medium and the installation must never be the same file.
func TestPlaceBinaryRefusesToInstallOverItself(t *testing.T) {
	dir := t.TempDir()
	layout := linuxLayout(dir)
	if err := os.MkdirAll(filepath.Dir(layout.Binary), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(layout.Binary, []byte("binary"), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write: %v", err)
	}
	err := placeBinary(layout, layout.Binary, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "over itself") {
		t.Fatalf("err = %v, want a refusal to install over itself", err)
	}
}

func TestPlaceBinaryKeepsThePreviousCopyForRollback(t *testing.T) {
	dir := t.TempDir()
	layout := linuxLayout(dir)
	layout.Previous = filepath.Join(dir, "lib", "jobfinder.prev")
	if err := os.MkdirAll(filepath.Dir(layout.Binary), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(layout.Binary, []byte("old"), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write: %v", err)
	}
	source := filepath.Join(dir, "artifact", "jobfinder")
	if err := os.MkdirAll(filepath.Dir(source), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write: %v", err)
	}
	if err := placeBinary(layout, source, os.Stderr); err != nil {
		t.Fatalf("place: %v", err)
	}
	if contents, err := os.ReadFile(layout.Binary); err != nil || string(contents) != "new" {
		t.Fatalf("installed = %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(layout.Previous); err != nil || string(contents) != "old" {
		t.Fatalf("previous = %q, %v; rollback would have nowhere to go", contents, err)
	}
}
