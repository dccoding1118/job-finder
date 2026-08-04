// Package version is the single decision point for the binary's version
// identity. Values come from two complementary sources: the tag injected by
// the release pipeline via ldflags, and the build info recorded by the Go
// toolchain. Neither alone covers every build path.
package version

import (
	"runtime/debug"
	"strings"
)

// tag is injected at build time via
// -ldflags "-X github.com/dccoding1118/job-finder/internal/version.tag=<tag>".
// The symbol path is bound to this package path and variable name: renaming
// either silently disables injection, leaving releases reporting "dev".
var tag = ""

// Info describes the running binary.
type Info struct {
	// Version is the release tag, or "dev" when built outside the release
	// pipeline.
	Version string
	// Revision is the VCS commit the binary was built from, or "unknown".
	Revision string
	// Modified reports whether the working tree was dirty at build time.
	Modified bool
}

// String renders the version identity for humans. Local builds carry no tag,
// so they are identified by revision instead of a fabricated version number.
func (i Info) String() string {
	if i.Version != "dev" || i.Revision == "unknown" {
		return i.Version
	}
	out := i.Version + " (" + i.Revision + ")"
	if i.Modified {
		out += " (dirty)"
	}
	return out
}

// Current reports the running binary's version identity.
func Current() Info {
	return resolve(debug.ReadBuildInfo)
}

// resolve takes the build info reader as a parameter so tests can supply
// arbitrary build metadata. Each field falls back through injected value →
// build info → literal default, so no build path yields an empty string.
func resolve(readBuildInfo func() (*debug.BuildInfo, bool)) Info {
	info := Info{Version: tag, Revision: "unknown"}

	build, ok := readBuildInfo()
	if !ok {
		if info.Version == "" {
			info.Version = "dev"
		}
		return info
	}

	// A plain `go build` inside a VCS tree records "(devel)", which identifies
	// nothing and must be treated as absent.
	if info.Version == "" && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = build.Main.Version
	}
	if info.Version == "" {
		info.Version = "dev"
	}

	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				info.Revision = shortRevision(setting.Value)
			}
		case "vcs.modified":
			info.Modified = setting.Value == "true"
		}
	}
	return info
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return strings.ToLower(revision[:12])
	}
	return strings.ToLower(revision)
}
