package version

import (
	"runtime/debug"
	"testing"
)

func buildInfo(mainVersion string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main:     debug.Module{Version: mainVersion},
			Settings: settings,
		}, true
	}
}

func TestResolvePrefersInjectedTag(t *testing.T) {
	original := tag
	tag = "v1.2.3"
	t.Cleanup(func() { tag = original })

	got := resolve(buildInfo("v9.9.9", debug.BuildSetting{Key: "vcs.revision", Value: "ABCDEF0123456789"}))
	if got.Version != "v1.2.3" {
		t.Fatalf("Version = %q, want the injected tag v1.2.3", got.Version)
	}
	if got.Revision != "abcdef012345" {
		t.Fatalf("Revision = %q, want the lowercased 12-char prefix", got.Revision)
	}
	if got.String() != "v1.2.3" {
		t.Fatalf("String() = %q, want the bare tag for released builds", got.String())
	}
}

func TestResolveTreatsDevelAsAbsent(t *testing.T) {
	original := tag
	tag = ""
	t.Cleanup(func() { tag = original })

	got := resolve(buildInfo(
		"(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	))
	if got.Version != "dev" {
		t.Fatalf("Version = %q, want dev — (devel) identifies nothing", got.Version)
	}
	if !got.Modified {
		t.Fatal("Modified = false, want true for a dirty working tree")
	}
	if want := "dev (0123456789ab) (dirty)"; got.String() != want {
		t.Fatalf("String() = %q, want %q", got.String(), want)
	}
}

func TestResolveUsesModuleVersionFromGoInstall(t *testing.T) {
	original := tag
	tag = ""
	t.Cleanup(func() { tag = original })

	got := resolve(buildInfo("v0.4.0"))
	if got.Version != "v0.4.0" {
		t.Fatalf("Version = %q, want the module version recorded by go install", got.Version)
	}
	if got.Revision != "unknown" {
		t.Fatalf("Revision = %q, want unknown when no VCS settings are recorded", got.Revision)
	}
}

func TestResolveWithoutBuildInfo(t *testing.T) {
	original := tag
	tag = ""
	t.Cleanup(func() { tag = original })

	got := resolve(func() (*debug.BuildInfo, bool) { return nil, false })
	if got.Version != "dev" || got.Revision != "unknown" {
		t.Fatalf("resolve() = %+v, want dev/unknown fallbacks", got)
	}
	if got.Modified {
		t.Fatal("Modified = true, want false without build info")
	}
}
