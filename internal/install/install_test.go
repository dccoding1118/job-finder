package install

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dccoding1118/job-finder/internal/paths"
)

func linuxLayout(root string) paths.Layout {
	binary := filepath.Join(root, "bin", "jobfinder")
	return paths.Layout{
		OS:        "unix",
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
		LibDir:    filepath.Join(root, "lib"),
		Config:    filepath.Join(root, "config", "config.yaml"),
		Profile:   filepath.Join(root, "config", "profile.yaml"),
		Denylist:  filepath.Join(root, "config", "pii-denylist.txt"),
		DB:        filepath.Join(root, "data", "jobs.db"),
		LogFile:   filepath.Join(root, "data", "logs", "jobfinder.log"),
		Binary:    binary,
		Previous:  filepath.Join(root, "lib", "jobfinder.prev"),
		Bad:       filepath.Join(root, "lib", "jobfinder.bad"),
		// One executable, so both names are the same file — the invariant every
		// non-Windows platform relies on.
		ServiceBinary:   binary,
		ServicePrevious: filepath.Join(root, "lib", "jobfinder.prev"),
		ServiceBad:      filepath.Join(root, "lib", "jobfinder.bad"),
	}
}

// windowsShapedLayout is the two-executable layout, built by hand so the Windows
// install path is exercised wherever the tests run rather than only on Windows.
func windowsShapedLayout(root string) paths.Layout {
	layout := linuxLayout(root)
	layout.OS = "windows"
	layout.Binary = filepath.Join(root, "bin", "jobfinder.exe")
	layout.Previous = filepath.Join(root, "lib", "jobfinder.exe.prev")
	layout.Bad = filepath.Join(root, "lib", "jobfinder.exe.bad")
	layout.ServiceBinary = filepath.Join(root, "bin", "jobfinderw.exe")
	layout.ServicePrevious = filepath.Join(root, "lib", "jobfinderw.exe.prev")
	layout.ServiceBad = filepath.Join(root, "lib", "jobfinderw.exe.bad")
	return layout
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
	layout := windowsShapedLayout(`C:\Users\a b`)
	t.Setenv("USERNAME", "user&name")
	t.Setenv("USERDOMAIN", "")
	rendered := renderTask("<Command>{{SERVICE_BINARY}}</Command><Arguments>run --config \"{{CONFIG}}\"</Arguments>"+
		"<WorkingDirectory>{{DATA_DIR}}</WorkingDirectory><UserId>{{USER}}</UserId>", layout)
	if strings.Contains(rendered, "{{") {
		t.Fatalf("a placeholder survived rendering:\n%s", rendered)
	}
	if !strings.Contains(rendered, "<UserId>user&amp;name</UserId>") {
		t.Fatalf("the user name was not XML-escaped:\n%s", rendered)
	}
	if !strings.Contains(rendered, "<Command>"+layout.ServiceBinary+"</Command>") {
		t.Fatalf("the service binary path was not substituted:\n%s", rendered)
	}
}

// A scheduled task must run the console-free build. Substituting the CLI binary
// there is the mistake this guards: it would work, and it would put a console
// window on the user's desktop for as long as the service is up.
func TestWindowsTaskTemplatesCommandTheServiceBinary(t *testing.T) {
	for _, name := range []string{"jobfinder-api.xml", "jobfinder-run.xml"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "windows", name)) // #nosec G304 -- reads the repository's own shipped templates.
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(contents), "<Command>{{SERVICE_BINARY}}</Command>") {
			t.Fatalf("%s does not command the service binary", name)
		}
	}
}

// The daily fetch has to be attributable to the schedule. Without the flag the
// run lands in the database as a hand-driven one and the Run history cannot tell
// an unattended fetch from an operator's.
func TestSystemdFetchUnitMarksTheTimerTrigger(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "deploy", "production", "systemd", "jobfinder-run.service"))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(contents), "--trigger timer") {
		t.Fatal("the fetch unit must pass --trigger timer, as the Windows task does")
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
	err := placeBinary(layout.Binary, layout.Binary, layout.Previous, os.Stderr)
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
	if err := placeBinary(source, layout.Binary, layout.Previous, os.Stderr); err != nil {
		t.Fatalf("place: %v", err)
	}
	if contents, err := os.ReadFile(layout.Binary); err != nil || string(contents) != "new" {
		t.Fatalf("installed = %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(layout.Previous); err != nil || string(contents) != "old" {
		t.Fatalf("previous = %q, %v; rollback would have nowhere to go", contents, err)
	}
}

// Re-running an update with the artifact already installed must not touch the
// rollback copy: overwriting it with the current build would leave rollback
// pointing at the very version it is meant to undo.
func TestPlaceBinaryKeepsTheRollbackCopyWhenTheBuildIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	layout := linuxLayout(dir)
	layout.Previous = filepath.Join(dir, "lib", "jobfinder.prev")
	if err := os.MkdirAll(filepath.Dir(layout.Binary), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.Previous), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for path, contents := range map[string]string{layout.Binary: "new", layout.Previous: "old"} {
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
			t.Fatalf("write: %v", err)
		}
	}
	source := filepath.Join(dir, "artifact", "jobfinder")
	if err := os.MkdirAll(filepath.Dir(source), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write: %v", err)
	}
	if err := placeBinary(source, layout.Binary, layout.Previous, os.Stderr); err != nil {
		t.Fatalf("place: %v", err)
	}
	if contents, err := os.ReadFile(layout.Previous); err != nil || string(contents) != "old" {
		t.Fatalf("previous = %q, %v; the rollback copy was overwritten by the current build", contents, err)
	}
	if contents, err := os.ReadFile(layout.Binary); err != nil || string(contents) != "new" {
		t.Fatalf("installed = %q, %v", contents, err)
	}
}

// A platform that runs a different executable than the user types must install
// both from the same artifact: two builds of different vintages would give the
// user a `jobfinder version` that does not describe what is actually serving.
func TestPlaceBinariesInstallsTheServiceCopyFromTheArtifact(t *testing.T) {
	dir := t.TempDir()
	layout := windowsShapedLayout(dir)
	assets := t.TempDir()
	for name, body := range map[string]string{"jobfinder.exe": "cli", "jobfinderw.exe": "service"} {
		if err := os.WriteFile(filepath.Join(assets, name), []byte(body), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := placeBinaries(layout, filepath.Join(assets, "jobfinder.exe"), assets, io.Discard); err != nil {
		t.Fatalf("place: %v", err)
	}
	for path, want := range map[string]string{layout.Binary: "cli", layout.ServiceBinary: "service"} {
		if contents, err := os.ReadFile(path); err != nil || string(contents) != want { // #nosec G304 -- path is inside the test's temporary directory.
			t.Fatalf("%s = %q, %v; want %q", path, contents, err, want)
		}
	}
}

// The artifact must be self-consistent. An artifact missing the service binary
// would otherwise install a CLI whose scheduled tasks point at a file that is
// not there, and the failure would surface as a task that will not start.
func TestPlaceBinariesRejectsAnArtifactMissingTheServiceCopy(t *testing.T) {
	dir := t.TempDir()
	assets := t.TempDir()
	source := filepath.Join(assets, "jobfinder.exe")
	if err := os.WriteFile(source, []byte("cli"), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write: %v", err)
	}
	err := placeBinaries(windowsShapedLayout(dir), source, assets, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "jobfinderw.exe") {
		t.Fatalf("err = %v, want the missing service binary named", err)
	}
}

// Rollback moves every executable or none. Restoring one of a pair would leave
// the CLI and the running service on different versions.
func TestRollbackSetCoversEveryInstalledExecutable(t *testing.T) {
	if got := rollbackSet(linuxLayout("/opt/jf")); len(got) != 1 {
		t.Fatalf("unix rollback set has %d entries, want 1", len(got))
	}
	windows := rollbackSet(windowsShapedLayout(`C:\jf`))
	if len(windows) != 2 {
		t.Fatalf("windows rollback set has %d entries, want 2", len(windows))
	}
	if windows[1].current == windows[0].current || windows[1].previous == windows[0].previous {
		t.Fatal("the two executables must roll back through separate copies")
	}
}

// Task Scheduler receives the task definition as a string, which is UTF-16 in
// memory, and rejects the entire document as malformed if the encoding
// declaration claims anything else. The declaration therefore is not a
// description of the bytes in the repository — it is part of the contract with
// the API, and "correcting" it to match the file on disk breaks installation on
// every Windows machine.
func TestWindowsTaskTemplatesDeclareUTF16(t *testing.T) {
	templates, err := filepath.Glob(filepath.Join("..", "..", "deploy", "production", "windows", "*.xml"))
	if err != nil || len(templates) == 0 {
		t.Fatalf("no task templates found: %v", err)
	}
	for _, template := range templates {
		contents, readErr := os.ReadFile(template) // #nosec G304 -- reads the repository's own templates.
		if readErr != nil {
			t.Fatalf("read %s: %v", template, readErr)
		}
		first, _, _ := strings.Cut(string(contents), "\n")
		if !strings.Contains(first, `encoding="UTF-16"`) {
			t.Fatalf("%s declares %q, want encoding=\"UTF-16\"", filepath.Base(template), strings.TrimSpace(first))
		}
	}
}

func TestUTF16LEEncodesWithAByteOrderMark(t *testing.T) {
	encoded := utf16LE("<?xml?>")
	if len(encoded) != 2+len("<?xml?>")*2 {
		t.Fatalf("length = %d, want a BOM plus two bytes per unit", len(encoded))
	}
	if encoded[0] != 0xFF || encoded[1] != 0xFE {
		t.Fatalf("prefix = %#v, want a little-endian byte order mark", encoded[:2])
	}
	if encoded[2] != '<' || encoded[3] != 0x00 {
		t.Fatalf("first unit = %#v, want '<' little endian", encoded[2:4])
	}
	// Content outside the basic plane must survive as a surrogate pair rather
	// than being truncated to a single unit.
	if got := len(utf16LE("\U0001F600")) - 2; got != 4 {
		t.Fatalf("astral character encoded to %d bytes, want 4", got)
	}
}
