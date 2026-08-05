package install

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// recordingScheduler stands in for systemd and Task Scheduler so the
// provisioning sequence can be exercised without a service manager. It records
// the order of the calls, because the order is the contract: a binary placed
// after the definitions were mounted, or a verification taken before the
// restart, would each pass their own check and still leave a broken install.
type recordingScheduler struct {
	calls []string
}

func (r *recordingScheduler) note(call string) error {
	r.calls = append(r.calls, call)
	return nil
}

func (r *recordingScheduler) name() string                    { return "recording" }
func (r *recordingScheduler) preflight(context.Context) error { return r.note("preflight") }
func (r *recordingScheduler) hints(paths.Layout) []string     { return nil }

func (r *recordingScheduler) mount(_ context.Context, _ paths.Layout, _ string, _ io.Writer) error {
	return r.note("mount")
}

func (r *recordingScheduler) restoreDefinitions(_ context.Context, _ paths.Layout, _ io.Writer) error {
	return r.note("restore")
}

func (r *recordingScheduler) start(_ context.Context, _ paths.Layout, _ io.Writer) error {
	return r.note("start")
}

func (r *recordingScheduler) restart(_ context.Context, _ paths.Layout, _ io.Writer) error {
	return r.note("restart")
}

func (r *recordingScheduler) assertEffective(_ context.Context, _ paths.Layout, _ time.Time, _ io.Writer) error {
	return r.note("assert")
}

// sandbox points the layout at a temporary home so the sequence tests never
// touch the operator's real installation.
func sandbox(t *testing.T) paths.Layout {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	layout, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return layout
}

// artifact builds a stand-in for the unpacked release artifact: an executable to
// install plus the configuration examples the installer renders from.
func artifact(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jobfinder"), []byte(body), 0o755); err != nil { // #nosec G306 -- test fixture stands in for an executable.
		t.Fatalf("write executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "configs"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "configs"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for _, name := range []string{"config.example.yaml", "profile.example.yaml"} {
		contents, readErr := os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- reads the repository's own shipped examples.
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(dir, "configs", name), contents, 0o600); writeErr != nil { // #nosec G703 -- destination is the test's own temporary directory.
			t.Fatalf("write %s: %v", name, writeErr)
		}
	}
	return dir
}

func TestInstallProvisionsAndVerifiesInOrder(t *testing.T) {
	layout := sandbox(t)
	assets := artifact(t, "#!/bin/sh\nexit 0\n")
	sched := &recordingScheduler{}
	opts := Options{
		AssetDir: assets, Source: filepath.Join(assets, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: sched,
	}
	if err := Install(context.Background(), opts); err != nil {
		t.Fatalf("install: %v", err)
	}
	want := "preflight,mount,start"
	if got := strings.Join(sched.calls, ","); got != want {
		t.Fatalf("scheduler calls = %q, want %q", got, want)
	}
	for _, path := range []string{layout.Config, layout.Profile, layout.Denylist, layout.Binary, layout.Manifest} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("install did not provision %s: %v", path, err)
		}
	}
	rendered, err := os.ReadFile(layout.Config) // #nosec G304 -- path comes from the sandboxed layout.
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(rendered), "CHANGE_ME") {
		t.Fatal("the API token placeholder survived the install")
	}
	if !strings.Contains(string(rendered), layout.DB) {
		t.Fatalf("the config does not point at the resolved database path:\n%s", rendered)
	}
	if info, statErr := os.Stat(layout.Config); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, %v; the token-bearing file must be owner-only", info.Mode().Perm(), statErr)
	}
	manifest, err := os.ReadFile(layout.Manifest) // #nosec G304 -- path comes from the sandboxed layout.
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "action=install") || !strings.Contains(string(manifest), "binary_sha256=") {
		t.Fatalf("manifest does not record the install:\n%s", manifest)
	}
}

// The token and the extension origin in an existing config belong to the user:
// re-running an install must not invalidate a working extension connection.
func TestInstallNeverOverwritesExistingConfigOrProfile(t *testing.T) {
	layout := sandbox(t)
	assets := artifact(t, "#!/bin/sh\nexit 0\n")
	opts := Options{
		AssetDir: assets, Source: filepath.Join(assets, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	}
	if err := Install(context.Background(), opts); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before, err := os.ReadFile(layout.Config) // #nosec G304 -- path comes from the sandboxed layout.
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if secondErr := Install(context.Background(), opts); secondErr != nil {
		t.Fatalf("second install: %v", secondErr)
	}
	after, err := os.ReadFile(layout.Config) // #nosec G304 -- path comes from the sandboxed layout.
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("a repeated install rewrote the existing config")
	}
}

func TestUpdateThenRollbackRestoresThePreviousBinary(t *testing.T) {
	layout := sandbox(t)
	first := artifact(t, "#!/bin/sh\necho v1\n")
	opts := Options{
		AssetDir: first, Source: filepath.Join(first, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	}
	if err := Install(context.Background(), opts); err != nil {
		t.Fatalf("install: %v", err)
	}

	second := artifact(t, "#!/bin/sh\necho v2\n")
	sched := &recordingScheduler{}
	updated := Options{
		AssetDir: second, Source: filepath.Join(second, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: sched,
	}
	if err := Update(context.Background(), updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	// An update must restart: replacing the file leaves the old process serving.
	if got := strings.Join(sched.calls, ","); got != "preflight,mount,restart" {
		t.Fatalf("update calls = %q, want preflight,mount,restart", got)
	}
	if contents, err := os.ReadFile(layout.Binary); err != nil || !strings.Contains(string(contents), "v2") { // #nosec G304 -- sandboxed layout.
		t.Fatalf("installed binary = %q, %v", contents, err)
	}

	if err := Rollback(context.Background(), Options{
		AssetDir: second, Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if contents, err := os.ReadFile(layout.Binary); err != nil || !strings.Contains(string(contents), "v1") { // #nosec G304 -- sandboxed layout.
		t.Fatalf("rolled back binary = %q, %v", contents, err)
	}
	// The rolled-back-from version is kept so a forward roll is still possible.
	if contents, err := os.ReadFile(layout.Bad); err != nil || !strings.Contains(string(contents), "v2") { // #nosec G304 -- sandboxed layout.
		t.Fatalf("bad binary = %q, %v", contents, err)
	}
}

func TestUpdateRefusesWithoutAnExistingInstall(t *testing.T) {
	sandbox(t)
	assets := artifact(t, "#!/bin/sh\nexit 0\n")
	err := Update(context.Background(), Options{
		AssetDir: assets, Source: filepath.Join(assets, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	})
	if err == nil || !strings.Contains(err.Error(), "jobfinder install") {
		t.Fatalf("err = %v, want a pointer at the install subcommand", err)
	}
}

func TestRollbackRefusesWithoutAPreviousBinary(t *testing.T) {
	sandbox(t)
	err := Rollback(context.Background(), Options{
		Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to roll back to") {
		t.Fatalf("err = %v, want a refusal to roll back to nothing", err)
	}
}

// A profile carrying PII must stop the install before any service is started
// around it.
func TestInstallStopsOnAProfileThatFailsTheLintGate(t *testing.T) {
	layout := sandbox(t)
	assets := artifact(t, "#!/bin/sh\nexit 0\n")
	if err := os.MkdirAll(layout.ConfigDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(layout.Profile, []byte("version: 6\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	err := Install(context.Background(), Options{
		AssetDir: assets, Source: filepath.Join(assets, "jobfinder"),
		Out: io.Discard, SkipVerify: true, scheduler: &recordingScheduler{},
	})
	if err == nil {
		t.Fatal("want the install to stop on an unusable profile")
	}
	if _, statErr := os.Stat(layout.Binary); statErr == nil {
		t.Fatal("the binary was placed despite the profile gate failing")
	}
}
