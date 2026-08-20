// Package install carries the whole meaning of installing jobfinder: deciding
// the paths, provisioning configuration without ever overwriting an existing
// one, placing the resident binary, mounting the platform's scheduling
// mechanism and proving the result on the effect surface.
//
// The bootstrap scripts (install.sh, install.ps1) only fetch an artifact and
// verify its checksum; every decision below is the same Go code on Linux and
// Windows, and only the scheduling mount differs. A user who downloads the
// artifact by hand can therefore run the unpacked executable directly and get
// an identical install.
//
// The executable that performs the install is the installation medium, not the
// installation: it copies itself to the layout's Binary path, and that resident
// copy is what the scheduler executes. The download directory can be deleted
// afterwards.
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/version"
)

// Options are the inputs a caller can vary; everything else is derived.
type Options struct {
	// AssetDir holds the unpacked artifact: the configuration examples and the
	// scheduling templates. Defaults to the directory of the running executable.
	AssetDir string
	// Source is the executable to install. Defaults to the running executable.
	Source string
	// Out receives the progress report.
	Out io.Writer
	// SkipVerify drops the effect-surface checks. It exists for environments
	// with no usable user session (a container image build, an unattended
	// provisioning step) and is never the normal path — an install that was not
	// verified on the running process has not been shown to work.
	SkipVerify bool
	// Now is the clock, injectable for tests.
	Now func() time.Time
	// scheduler overrides the platform's mechanism. Unexported: it exists so the
	// provisioning sequence can be exercised without a service manager, not as a
	// way for a caller to install something other than what the platform uses.
	scheduler scheduler
}

func (o *Options) mechanism(goos string) scheduler {
	if o.scheduler != nil {
		return o.scheduler
	}
	return newScheduler(goos)
}

func (o *Options) fill() error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Source == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("install: locate the running executable: %w", err)
		}
		resolved, err := filepath.EvalSymlinks(executable)
		if err == nil {
			executable = resolved
		}
		o.Source = executable
	}
	if o.AssetDir == "" {
		o.AssetDir = filepath.Dir(o.Source)
	}
	return nil
}

// Install provisions a first install, or repairs one, without touching any
// configuration, profile, denylist or database that already exists.
func Install(ctx context.Context, opts Options) error {
	if err := opts.fill(); err != nil {
		return err
	}
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	sched := opts.mechanism(layout.OS)
	report(opts.Out, "install %s for %s (%s scheduling)", version.Current(), layout.OS, sched.name())
	if err := sched.preflight(ctx); err != nil {
		return err
	}

	if err := provisionDirs(layout, opts.Out); err != nil {
		return err
	}
	if err := provisionConfig(layout, opts.AssetDir, opts.Out); err != nil {
		return err
	}
	if err := lintProfile(layout); err != nil {
		return err
	}
	if err := placeBinaries(layout, opts.Source, opts.AssetDir, opts.Out); err != nil {
		return err
	}
	if err := sched.mount(ctx, layout, opts.AssetDir, opts.Out); err != nil {
		return err
	}
	if err := registerPath(ctx, layout, opts.Out); err != nil {
		return err
	}

	marker := opts.Now()
	if err := sched.start(ctx, layout, opts.Out); err != nil {
		return err
	}
	if err := verifyEffect(ctx, sched, layout, marker, opts); err != nil {
		return err
	}
	if err := writeManifest(layout, "install", opts.Now()); err != nil {
		return err
	}
	summarise(opts.Out, layout, sched)
	return nil
}

// Update replaces the resident binary and the scheduling definitions with the
// ones in the artifact this executable came from, keeping the previous binary
// so Rollback has somewhere to go. Configuration and data are untouched.
func Update(ctx context.Context, opts Options) error {
	if err := opts.fill(); err != nil {
		return err
	}
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	if _, err := os.Stat(layout.Binary); err != nil {
		return fmt.Errorf("install: no existing install at %s; run `jobfinder install` first", layout.Binary)
	}
	sched := opts.mechanism(layout.OS)
	report(opts.Out, "update to %s", version.Current())
	if err := sched.preflight(ctx); err != nil {
		return err
	}
	if err := placeBinaries(layout, opts.Source, opts.AssetDir, opts.Out); err != nil {
		return err
	}
	if err := sched.mount(ctx, layout, opts.AssetDir, opts.Out); err != nil {
		return err
	}

	marker := opts.Now()
	if err := sched.restart(ctx, layout, opts.Out); err != nil {
		return err
	}
	if err := verifyEffect(ctx, sched, layout, marker, opts); err != nil {
		return err
	}
	if err := writeManifest(layout, "update", opts.Now()); err != nil {
		return err
	}
	report(opts.Out, "update complete")
	return nil
}

// Rollback restores the binary kept by the previous Install or Update. The
// database is never touched: it is only restored from the backup directory by
// an operator who has confirmed corruption.
func Rollback(ctx context.Context, opts Options) error {
	if err := opts.fill(); err != nil {
		return err
	}
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	// Every executable moves together, so all of them are checked before any of
	// them is touched. Rolling back only one of a pair would leave the binary the
	// user types and the one the scheduler runs on different versions, which is a
	// worse state than the one being rolled back from.
	for _, binary := range rollbackSet(layout) {
		if _, err := os.Stat(binary.previous); err != nil {
			return fmt.Errorf("install: no previous binary at %s; nothing to roll back to", binary.previous)
		}
	}
	sched := opts.mechanism(layout.OS)
	report(opts.Out, "rollback")
	if err := sched.preflight(ctx); err != nil {
		return err
	}
	for _, binary := range rollbackSet(layout) {
		// The current binary is kept aside so a forward roll is still possible
		// after a rollback that turns out to have been the wrong call.
		if err := copyFile(binary.current, binary.bad, 0o755); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := copyFile(binary.previous, binary.current, 0o755); err != nil {
			return err
		}
		report(opts.Out, "restored %s from %s", binary.current, binary.previous)
	}
	if err := sched.restoreDefinitions(ctx, layout, opts.Out); err != nil {
		return err
	}

	marker := opts.Now()
	if err := sched.restart(ctx, layout, opts.Out); err != nil {
		return err
	}
	if err := verifyEffect(ctx, sched, layout, marker, opts); err != nil {
		return err
	}
	if err := writeManifest(layout, "rollback", opts.Now()); err != nil {
		return err
	}
	report(opts.Out, "rollback complete; the database at %s was not touched", layout.DB)
	report(opts.Out, "restore it from %s only if corruption is confirmed", layout.BackupDir)
	return nil
}

// provisionDirs creates the layout. On Unix the config, data and backup
// directories are owner-only. Windows has no chmod equivalent: the whole tree
// sits under the user's %LocalAppData%, whose ACL already denies other
// non-administrative users, and that is the protection the token-bearing
// config gets.
func provisionDirs(layout paths.Layout, out io.Writer) error {
	private := []string{layout.ConfigDir, layout.DataDir, layout.BackupDir, layout.LogDir, layout.LibDir}
	shared := []string{layout.BinDir}
	if layout.UnitDir != "" {
		shared = append(shared, layout.UnitDir)
	}
	for _, dir := range private {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("install: create %s: %w", dir, err)
		}
		if layout.OS != "windows" {
			if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- dir is a directory; 0700 is the owner-only mode it needs.
				return fmt.Errorf("install: restrict %s: %w", dir, err)
			}
		}
	}
	for _, dir := range shared {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("install: create %s: %w", dir, err)
		}
	}
	report(out, "provisioned %s", layout.ConfigDir)
	return nil
}

// lintProfile is the gate that keeps an install from starting a service around
// a profile that carries PII or does not parse.
func lintProfile(layout paths.Layout) error {
	_, contents, err := profile.Load(layout.Profile)
	if err != nil {
		return err
	}
	denylist, err := profile.LoadDenylist(layout.Denylist)
	if err != nil {
		return err
	}
	if err := profile.LintText(contents, denylist); err != nil {
		return fmt.Errorf("install: profile lint: %w", err)
	}
	return nil
}

// rollbackTarget is one executable and the two copies rollback moves between:
// the version kept by the last install or update, and where the version being
// rolled back from is parked so a forward roll is still possible.
type rollbackTarget struct{ current, previous, bad string }

// rollbackSet is every executable an install placed on this platform, in the
// order they are restored.
func rollbackSet(layout paths.Layout) []rollbackTarget {
	set := []rollbackTarget{{layout.Binary, layout.Previous, layout.Bad}}
	if layout.SeparateServiceBinary() {
		set = append(set, rollbackTarget{layout.ServiceBinary, layout.ServicePrevious, layout.ServiceBad})
	}
	return set
}

// placeBinaries puts every executable this platform needs into its resident
// location. The installation medium is the one the user ran, so it supplies the
// binary they type; a platform that also needs a separate service binary takes
// that one out of the same artifact, because the two must always be the same
// build.
func placeBinaries(layout paths.Layout, source, assetDir string, out io.Writer) error {
	if err := placeBinary(source, layout.Binary, layout.Previous, out); err != nil {
		return err
	}
	if !layout.SeparateServiceBinary() {
		return nil
	}
	serviceSource, err := binaryAsset(assetDir, filepath.Base(layout.ServiceBinary))
	if err != nil {
		return err
	}
	return placeBinary(serviceSource, layout.ServiceBinary, layout.ServicePrevious, out)
}

// placeBinary copies one executable to its resident location, keeping the
// outgoing copy for rollback. Copying rather than renaming is what lets the
// user delete the download directory afterwards.
func placeBinary(source, target, previous string, out io.Writer) error {
	if same, err := sameFile(source, target); err != nil {
		return err
	} else if same {
		return fmt.Errorf("install: refusing to install %s over itself; run the unpacked artifact, not the installed copy", source)
	}
	if _, err := os.Stat(target); err == nil {
		// Keeping the outgoing copy is what rollback goes back to, so it is kept
		// only when it is a different build. Re-running the same version would
		// otherwise overwrite the previous version with the current one and leave
		// rollback pointing at the very build it is meant to undo.
		identical, err := sameContent(source, target)
		if err != nil {
			return err
		}
		if identical {
			report(out, "%s is already this build; keeping the existing rollback copy", target)
		} else if err := copyFile(target, previous, 0o755); err != nil {
			return err
		}
	}
	if err := copyFile(source, target, 0o755); err != nil {
		return err
	}
	report(out, "installed %s", target)
	return nil
}

// writeManifest records what is installed so the provenance of a running binary
// can be established later without guessing.
func writeManifest(layout paths.Layout, action string, now time.Time) error {
	sum, err := fileSHA256(layout.Binary)
	if err != nil {
		return err
	}
	taipei := time.FixedZone("CST", 8*60*60)
	contents := fmt.Sprintf(
		"action=%s\ninstalled_at=%s\nversion=%s\nplatform=%s\nbinary_sha256=%s\nconfig=%s\ndb=%s\nlog=%s\n",
		action, now.In(taipei).Format(time.RFC3339), version.Current(), layout.OS, sum, layout.Config, layout.DB, layout.LogFile,
	)
	if err := os.WriteFile(layout.Manifest, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("install: write manifest: %w", err)
	}
	return nil
}

func summarise(out io.Writer, layout paths.Layout, sched scheduler) {
	report(out, "install complete")
	for _, row := range layout.Rows() {
		report(out, "  %-14s %s", row[0], row[1])
	}
	for _, line := range sched.hints(layout) {
		report(out, "  %s", line)
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- path comes from the resolved layout.
	if err != nil {
		return "", fmt.Errorf("install: checksum %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("install: checksum %s: %w", path, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// copyFile writes through a temporary file in the destination directory and
// renames it into place, so an interrupted copy cannot leave a truncated
// executable where the scheduler expects a working one.
func copyFile(src, dst string, mode os.FileMode) error {
	source, err := os.Open(src) // #nosec G304 -- paths come from the resolved layout or an explicit CLI argument.
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if mkdirErr := os.MkdirAll(filepath.Dir(dst), 0o750); mkdirErr != nil {
		return fmt.Errorf("install: create %s: %w", filepath.Dir(dst), mkdirErr)
	}
	temp, err := os.CreateTemp(filepath.Dir(dst), ".jobfinder-*")
	if err != nil {
		return fmt.Errorf("install: stage %s: %w", dst, err)
	}
	staged := temp.Name()
	defer func() { _ = os.Remove(staged) }()
	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		return fmt.Errorf("install: copy to %s: %w", dst, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("install: copy to %s: %w", dst, err)
	}
	if err := os.Chmod(staged, mode); err != nil {
		return fmt.Errorf("install: set mode on %s: %w", dst, err)
	}
	// Windows refuses to replace a file that is currently executing; the outgoing
	// binary is moved aside first so a running service does not block the update.
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err == nil {
			defer func() { _ = os.Remove(dst + ".old") }()
		}
	}
	if err := os.Rename(staged, dst); err != nil {
		return fmt.Errorf("install: place %s: %w", dst, err)
	}
	return nil
}

// indentLines makes borrowed output visibly borrowed, so a multi-line reason
// reads as one block under the error rather than as more errors.
func indentLines(text string) string {
	lines := []string(nil)
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, "  "+trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

// tailFile returns the last n non-empty lines of a file, or empty when it
// cannot be read. It is best-effort diagnosis, never a reason to fail.
func tailFile(path string, n int) string {
	contents, err := os.ReadFile(path) // #nosec G304 -- path comes from the resolved layout.
	if err != nil {
		return ""
	}
	lines := []string(nil)
	for _, line := range strings.Split(string(contents), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// sameContent compares two executables by digest, which is what separates
// "installing the same build again" from a real version change; the two files
// are distinct paths either way, so identity of the file cannot answer it.
func sameContent(a, b string) (bool, error) {
	digestA, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	digestB, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return digestA == digestB, nil
}

func sameFile(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("install: stat %s: %w", a, err)
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false, nil
	}
	return os.SameFile(infoA, infoB), nil
}

func report(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format+"\n", args...)
}
