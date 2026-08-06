// Package paths is the single decision point for where jobfinder keeps its
// configuration, data, binary, logs and rollback material on each supported
// platform. Every entry that needs one of those locations — the CLI's flag
// defaults, the installer, the scheduling templates — resolves it here, so a
// platform's layout is described once instead of being restated in a shell
// script, a unit file and a flag default that can drift apart.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Layout is one platform's resolved locations. Directory fields are the
// containers the installer creates; file fields are the exact paths every
// component reads and writes.
type Layout struct {
	// OS is the platform the layout was resolved for.
	OS string

	ConfigDir string
	DataDir   string
	LibDir    string
	BinDir    string
	LogDir    string
	BackupDir string
	// UnitDir is the systemd user unit directory. Empty on Windows, where
	// scheduling is mounted in Task Scheduler rather than in a file tree.
	UnitDir string

	Config   string
	Profile  string
	Denylist string
	DB       string
	LogFile  string

	// Binary is where the resident copy of jobfinder lives — the one the user
	// types. It is never the artifact the user unpacked and ran the installer
	// from.
	Binary   string
	Previous string
	Bad      string

	// ServiceBinary is the copy the scheduler executes. On Windows it is a
	// second, GUI-subsystem build of the same program: a console-subsystem
	// executable started by Task Scheduler in the user's own session is given a
	// console window, and a resident service must not put one on the desktop.
	// Everywhere else there is nothing to hide and it is the same file as Binary,
	// so every caller can name it unconditionally.
	ServiceBinary   string
	ServicePrevious string
	ServiceBad      string

	Manifest string
}

// SeparateServiceBinary reports whether the scheduler runs a different
// executable from the one the user types, which is what decides whether an
// install has one binary to place and roll back or two.
func (l Layout) SeparateServiceBinary() bool { return l.ServiceBinary != l.Binary }

// Resolve returns the layout for the running platform.
func Resolve() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("paths: resolve home directory: %w", err)
	}
	return resolve(runtime.GOOS, home, os.Getenv)
}

// resolve takes its platform, home directory and environment as arguments so
// the layout of either platform can be asserted from a test on the other.
func resolve(goos, home string, getenv func(string) string) (Layout, error) {
	if home == "" {
		return Layout{}, fmt.Errorf("paths: home directory is empty")
	}
	if goos == "windows" {
		return windowsLayout(home, getenv), nil
	}
	return xdgLayout(home, getenv), nil
}

// xdgLayout follows the XDG base directory split on Linux and macOS: settings
// under the config home, state under the data home, the executable on the
// user's own PATH.
func xdgLayout(home string, getenv func(string) string) Layout {
	configHome := getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	layout := Layout{
		OS:        "unix",
		ConfigDir: filepath.Join(configHome, "jobfinder"),
		DataDir:   filepath.Join(dataHome, "jobfinder"),
		LibDir:    filepath.Join(home, ".local", "lib", "jobfinder"),
		BinDir:    filepath.Join(home, ".local", "bin"),
		UnitDir:   filepath.Join(configHome, "systemd", "user"),
	}
	return fill(layout, "jobfinder", "jobfinder")
}

// windowsLayout keeps everything under one per-user root below %LocalAppData%.
// That root is the permission boundary too: Windows has no chmod equivalent, so
// confidentiality of the token-bearing config rests on the ACL the user profile
// already carries.
func windowsLayout(home string, getenv func(string) string) Layout {
	local := getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(home, "AppData", "Local")
	}
	root := filepath.Join(local, "jobfinder")
	layout := Layout{
		OS:        "windows",
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
		LibDir:    filepath.Join(root, "lib"),
		BinDir:    filepath.Join(root, "bin"),
	}
	return fill(layout, "jobfinder.exe", "jobfinderw.exe")
}

// fill derives every file location from the already-decided directories, so the
// two platform functions differ only in where those directories sit. Passing the
// same name twice is what makes ServiceBinary equal Binary on a platform that
// needs only one executable.
func fill(layout Layout, binaryName, serviceBinaryName string) Layout {
	layout.LogDir = filepath.Join(layout.DataDir, "logs")
	layout.BackupDir = filepath.Join(layout.DataDir, "backups")
	layout.Config = filepath.Join(layout.ConfigDir, "config.yaml")
	layout.Profile = filepath.Join(layout.ConfigDir, "profile.yaml")
	layout.Denylist = filepath.Join(layout.ConfigDir, "pii-denylist.txt")
	layout.DB = filepath.Join(layout.DataDir, "jobs.db")
	layout.LogFile = filepath.Join(layout.LogDir, "jobfinder.log")
	layout.Binary = filepath.Join(layout.BinDir, binaryName)
	layout.Previous = filepath.Join(layout.LibDir, binaryName+".prev")
	layout.Bad = filepath.Join(layout.LibDir, binaryName+".bad")
	layout.ServiceBinary = filepath.Join(layout.BinDir, serviceBinaryName)
	layout.ServicePrevious = filepath.Join(layout.LibDir, serviceBinaryName+".prev")
	layout.ServiceBad = filepath.Join(layout.LibDir, serviceBinaryName+".bad")
	layout.Manifest = filepath.Join(layout.LibDir, "manifest.txt")
	return layout
}

// Rows renders the layout as ordered label/path pairs for `jobfinder paths`,
// which exists so a support question about "which config is it actually
// reading" is answered by the program rather than by guesswork.
func (l Layout) Rows() [][2]string {
	rows := [][2]string{
		{"platform", l.OS},
		{"config", l.Config},
		{"profile", l.Profile},
		{"denylist", l.Denylist},
		{"database", l.DB},
		{"log file", l.LogFile},
		{"binary", l.Binary},
		{"backups", l.BackupDir},
		{"rollback", l.LibDir},
	}
	if l.SeparateServiceBinary() {
		rows = append(rows, [2]string{"service binary", l.ServiceBinary})
	}
	if l.UnitDir != "" {
		rows = append(rows, [2]string{"systemd units", l.UnitDir})
	}
	return rows
}

// DefaultConfig is the config path used when no --config flag is given. It
// falls back to a working-directory config.yaml when the home directory cannot
// be resolved, which keeps a development checkout runnable in the odd
// environment that has no HOME.
func DefaultConfig() string {
	layout, err := Resolve()
	if err != nil {
		return "config.yaml"
	}
	return layout.Config
}

// DefaultProfile and DefaultDenylist back the `profile` subcommand's flags for
// the same reason DefaultConfig backs `--config`.
func DefaultProfile() string {
	layout, err := Resolve()
	if err != nil {
		return "profile.yaml"
	}
	return layout.Profile
}

func DefaultDenylist() string {
	layout, err := Resolve()
	if err != nil {
		return "pii-denylist.txt"
	}
	return layout.Denylist
}
