package profile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStrictCodecAndRevisions(t *testing.T) {
	value, err := DecodeYAML([]byte(validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	filterRevision, scoreRevision, err := RevisionPair(value)
	if err != nil {
		t.Fatal(err)
	}
	withComment, err := DecodeYAML([]byte("# formatting only\n" + validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	filterAgain, scoreAgain, _ := RevisionPair(withComment)
	if filterRevision != filterAgain || scoreRevision != scoreAgain || !strings.HasPrefix(filterRevision, "sha256:") {
		t.Fatalf("revisions changed for formatting: %q/%q vs %q/%q", filterRevision, scoreRevision, filterAgain, scoreAgain)
	}
	if filterRevision == scoreRevision {
		t.Fatal("the two gates produced the same revision")
	}
	if _, err := DecodeYAML([]byte(validProfileYAML() + "unknown: true\n")); err == nil {
		t.Fatal("unknown YAML field was accepted")
	}
	jsonBody := strings.Replace(`{"honesty_bounds":["ok"]}`, `}`, `,"unknown":true}`, 1)
	if _, err := DecodeJSON(bytes.NewBufferString(jsonBody)); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
}

// Each revision covers only the fields its own gate reads, which is what keeps
// a soft-rule edit from sending every job back through screening.
func TestRevisionCoverage(t *testing.T) {
	base, err := DecodeYAML([]byte(validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	baseFilter, baseScore, _ := RevisionPair(base)
	for name, testCase := range map[string]struct {
		edit                    func(*Profile)
		filterMoves, scoreMoves bool
	}{
		"intents_only":      {func(p *Profile) { p.Intents.ContentLikes = append(p.Intents.ContentLikes, "new") }, false, true},
		"hard_rule_only":    {func(p *Profile) { p.Requirements.SalaryMin = 900000 }, true, false},
		"employment_types":  {func(p *Profile) { p.Requirements.EmploymentTypes = []string{"全職"} }, true, false},
		"shared_remote":     {func(p *Profile) { p.Requirements.Remote = remoteRequired }, true, true},
		"shared_skills":     {func(p *Profile) { p.Qualifications.Skills[0].Level = "expert" }, true, true},
		"search_only":       {func(p *Profile) { p.Search.Directions[0].Keywords = append(p.Search.Directions[0].Keywords, "new") }, false, false},
		"achievements_only": {func(p *Profile) { p.Experiences[0].Achievements = []string{"rewritten"} }, false, false},
		"honesty_bounds":    {func(p *Profile) { p.HonestyBounds = []string{"other"} }, false, false},
		"experience_years":  {func(p *Profile) { p.Experiences[0].Years = 9 }, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			value := cloneProfile(base)
			testCase.edit(&value)
			filter, score, err := RevisionPair(value)
			if err != nil {
				t.Fatal(err)
			}
			if (filter != baseFilter) != testCase.filterMoves {
				t.Fatalf("filter revision moved=%v, want %v", filter != baseFilter, testCase.filterMoves)
			}
			if (score != baseScore) != testCase.scoreMoves {
				t.Fatalf("score revision moved=%v, want %v", score != baseScore, testCase.scoreMoves)
			}
		})
	}
}

func TestProviderMissingSaveConflictPIIAndImmutableSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "profile.yaml")
	provider, err := NewProvider(path, []string{"private-name"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := provider.Current(); snapshot.Status != "missing" || snapshot.ETag != `"missing"` {
		t.Fatalf("missing snapshot = %+v", snapshot)
	}
	value, err := DecodeYAML([]byte(validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Save(`"missing"`, value)
	if err != nil {
		t.Fatal(err)
	}
	// The saved Profile must be owner-only where the platform has file
	// permissions. Windows has no chmod equivalent; there the confidentiality
	// of the config directory rests on the ACL it inherits.
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode=%v err=%v", info.Mode().Perm(), statErr)
		}
	}
	if !result.FilterChanged || !result.ScoreChanged {
		t.Fatalf("save result = %+v", result)
	}
	if result.Snapshot.Profile.Derived.TotalYears != 4.5 {
		t.Fatalf("saved snapshot did not materialize derived: %+v", result.Snapshot.Profile.Derived)
	}
	mutable := provider.Current()
	mutable.Profile.Qualifications.Skills[0].Name = "mutated"
	if provider.Current().Profile.Qualifications.Skills[0].Name == "mutated" {
		t.Fatal("provider exposed mutable Profile slices")
	}
	if _, err := provider.Save(`"missing"`, value); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ETag error = %v", err)
	}
	value.HonestyBounds = []string{"private-name"}
	if _, err := provider.Save(result.Snapshot.ETag, value); err == nil {
		t.Fatal("PII Profile was saved")
	} else {
		var validation ValidationError
		if !errors.As(err, &validation) || len(validation.Issues) == 0 || !strings.HasPrefix(validation.Issues[0].Path, "honesty_bounds") {
			t.Fatalf("PII error = %#v", err)
		}
	}
}

func TestProviderSaveReportsWhichGateChanged(t *testing.T) {
	path := writeProfile(t, validProfileYAML())
	provider, err := NewProvider(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := *provider.Current().Profile
	value.Intents.ContentDislikes = append(value.Intents.ContentDislikes, "on-call rotations")
	result, err := provider.Save(provider.Current().ETag, value)
	if err != nil {
		t.Fatal(err)
	}
	if result.FilterChanged || !result.ScoreChanged {
		t.Fatalf("a soft-rule edit reported %+v", result)
	}
	after, _ := os.ReadFile(path) // #nosec G304 -- path is created by this test.
	if !bytes.Contains(after, []byte("on-call rotations")) || provider.Current().ScoreRevision != result.Snapshot.ScoreRevision {
		t.Fatal("saved Profile was not published to disk and the runtime snapshot")
	}
	unchanged, err := provider.Save(provider.Current().ETag, value)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.FilterChanged || unchanged.ScoreChanged {
		t.Fatalf("resaving identical content reported a change: %+v", unchanged)
	}
}
