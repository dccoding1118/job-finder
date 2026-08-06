package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// assetCandidates maps each asset to the places it can legitimately live: the
// unpacked release artifact keeps templates at the top level, while a
// development checkout keeps them under deploy/production/. One installer
// serves both, so neither has to carry a copy of the other's layout.
var assetCandidates = map[string][]string{
	"configs": {"configs"},
	"systemd": {"systemd", filepath.Join("deploy", "production", "systemd")},
	"windows": {"windows", filepath.Join("deploy", "production", "windows")},
}

// asset resolves one asset directory inside the artifact.
func asset(assetDir, kind string) (string, error) {
	for _, candidate := range assetCandidates[kind] {
		path := filepath.Join(assetDir, candidate)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("install: %s templates not found under %s; run the installer from the unpacked artifact or pass --assets", kind, assetDir)
}

// binaryAsset finds an executable the artifact ships alongside the one being
// run. A release artifact keeps them side by side at the top level; a
// development checkout puts what it builds in bin/. The lookup is by file
// rather than by directory because both candidates exist in a checkout and only
// one of them holds the binary.
func binaryAsset(assetDir, name string) (string, error) {
	for _, candidate := range []string{".", "bin"} {
		path := filepath.Join(assetDir, candidate, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("install: %s not found under %s; this platform installs it alongside the binary you ran, so the artifact must contain both", name, assetDir)
}

// provisionConfig renders config.yaml on a first install and leaves an existing
// one strictly alone — the token and the extension origin in it are the user's,
// and an install must never invalidate a working extension connection.
func provisionConfig(layout paths.Layout, assetDir string, out io.Writer) error {
	configs, err := asset(assetDir, "configs")
	if err != nil {
		return err
	}
	if seedErr := seedFile(filepath.Join(configs, "profile.example.yaml"), layout.Profile, out,
		"seeded the anonymised example profile at %s; replace it with your own"); seedErr != nil {
		return seedErr
	}
	if _, statErr := os.Stat(layout.Denylist); os.IsNotExist(statErr) {
		header := "# jobfinder PII denylist: one forbidden marker per line (kept out of version control)\n"
		if writeErr := os.WriteFile(layout.Denylist, []byte(header), 0o600); writeErr != nil {
			return fmt.Errorf("install: create denylist: %w", writeErr)
		}
		report(out, "created an empty denylist at %s; add your PII markers to it", layout.Denylist)
	}

	if _, statErr := os.Stat(layout.Config); statErr == nil {
		report(out, "config exists, kept: %s", layout.Config)
		return harden(layout, layout.Config)
	}
	example, err := os.ReadFile(filepath.Join(configs, "config.example.yaml")) // #nosec G304 -- path derives from the artifact directory.
	if err != nil {
		return fmt.Errorf("install: read config example: %w", err)
	}
	token, err := generateToken()
	if err != nil {
		return err
	}
	rendered, err := renderConfig(string(example), layout, token)
	if err != nil {
		return err
	}
	if err := os.WriteFile(layout.Config, []byte(rendered), 0o600); err != nil { // #nosec G703 -- path comes from the resolved layout.
		return fmt.Errorf("install: write config: %w", err)
	}
	report(out, "rendered %s with a generated API token", layout.Config)
	report(out, "set api.extension_origin to the real chrome-extension:// id before loading the extension")
	return harden(layout, layout.Config)
}

// renderConfig turns the shipped example into an absolute-path configuration.
// Every substitution is required to match exactly once: a silently unmatched
// placeholder would leave the install pointing at a relative development path
// and only fail much later, somewhere unrelated.
func renderConfig(example string, layout paths.Layout, token string) (string, error) {
	substitutions := []struct{ prefix, line string }{
		{"  path: .local-dev/jobfinder.db", "  path: " + layout.DB},
		{"  path: .local-dev/profile.yaml", "  path: " + layout.Profile},
		{"  denylist: .local-dev/pii-denylist.txt", "  denylist: " + layout.Denylist},
		{"  token: CHANGE_ME", "  token: " + token},
	}
	// Windows Task Scheduler discards a task's output, so the file sink is the
	// only place a scheduled fetch can report anything. Linux leaves it empty and
	// reads the same records out of journald.
	if layout.OS == "windows" {
		substitutions = append(substitutions, struct{ prefix, line string }{`  file: ""`, "  file: " + layout.LogFile})
	}
	lines := strings.Split(example, "\n")
	for _, substitution := range substitutions {
		matches := 0
		for i, line := range lines {
			if strings.TrimRight(line, "\r") == substitution.prefix {
				lines[i] = substitution.line
				matches++
			}
		}
		if matches != 1 {
			return "", fmt.Errorf("install: config example has %d lines matching %q, want exactly 1", matches, substitution.prefix)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func seedFile(src, dst string, out io.Writer, message string) error {
	if _, err := os.Stat(dst); err == nil {
		report(out, "kept existing %s", dst)
		return nil
	}
	if err := copyFile(src, dst, 0o600); err != nil {
		return err
	}
	report(out, message, dst)
	return nil
}

// harden applies owner-only permissions where the platform has them. On Windows
// it is a no-op by design: the inherited %LocalAppData% ACL is the protection,
// and pretending otherwise with a chmod that does nothing would be worse than
// stating the concession.
func harden(layout paths.Layout, path string) error {
	if layout.OS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("install: restrict %s: %w", path, err)
	}
	return nil
}

func generateToken() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("install: generate API token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// registerPath makes `jobfinder` runnable by name after the install, which is
// what later `jobfinder update` and every diagnostic command depend on. On
// Windows the user PATH is a registry value, so it is amended in place;
// elsewhere ~/.local/bin is conventional enough that a warning is the right
// action rather than editing the user's shell profile behind their back.
func registerPath(ctx context.Context, layout paths.Layout, out io.Writer) error {
	if onPath(layout.BinDir) {
		return nil
	}
	if layout.OS != "windows" {
		report(out, "%s is not on PATH; add it to your shell profile to run `jobfinder` by name", layout.BinDir)
		return nil
	}
	script := fmt.Sprintf(
		`$dir = %q; $current = [Environment]::GetEnvironmentVariable('Path','User'); `+
			`if (-not $current) { $current = '' }; `+
			`if (($current -split ';') -notcontains $dir) { `+
			`[Environment]::SetEnvironmentVariable('Path', ($current.TrimEnd(';') + ';' + $dir).Trim(';'), 'User') }`,
		layout.BinDir,
	)
	if _, err := run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
		report(out, "could not add %s to the user PATH (%v); add it manually", layout.BinDir, err)
		return nil
	}
	report(out, "added %s to the user PATH; open a new terminal for it to take effect", layout.BinDir)
	return nil
}

func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(dir)) {
			return true
		}
	}
	return false
}
