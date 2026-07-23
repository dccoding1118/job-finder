package profile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictCodecAndRevision(t *testing.T) {
	value, err := DecodeYAML([]byte(validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := Revision(value)
	if err != nil {
		t.Fatal(err)
	}
	withComment, err := DecodeYAML([]byte("# formatting only\n" + validProfileYAML()))
	if err != nil {
		t.Fatal(err)
	}
	revisionWithComment, _ := Revision(withComment)
	if revision != revisionWithComment || !strings.HasPrefix(revision, "sha256:") {
		t.Fatalf("revision changed for formatting: %q != %q", revision, revisionWithComment)
	}
	if _, err := DecodeYAML([]byte(validProfileYAML() + "unknown: true\n")); err == nil {
		t.Fatal("unknown YAML field was accepted")
	}
	jsonBody := strings.Replace(`{"summary":"ok"}`, `}`, `,"unknown":true}`, 1)
	if _, err := DecodeJSON(bytes.NewBufferString(jsonBody)); err == nil {
		t.Fatal("unknown JSON field was accepted")
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
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if !result.SemanticChanged {
		t.Fatalf("save result = %+v", result)
	}
	mutable := provider.Current()
	mutable.Profile.Skills.Expert[0] = "mutated"
	if provider.Current().Profile.Skills.Expert[0] == "mutated" {
		t.Fatal("provider exposed mutable Profile slices")
	}
	if _, err := provider.Save(`"missing"`, value); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ETag error = %v", err)
	}
	value.Summary = "private-name"
	if _, err := provider.Save(result.Snapshot.ETag, value); err == nil {
		t.Fatal("PII Profile was saved")
	} else {
		var validation ValidationError
		if !errors.As(err, &validation) || len(validation.Issues) == 0 || validation.Issues[0].Path != "summary" {
			t.Fatalf("PII error = %#v", err)
		}
	}
}

func TestProviderSavePublishesNewSnapshotWithoutChangingExistingJobs(t *testing.T) {
	path := writeProfile(t, validProfileYAML())
	provider, err := NewProvider(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := *provider.Current().Profile
	value.Summary = "changed anonymous profile"
	result, err := provider.Save(provider.Current().ETag, value)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path) // #nosec G304 -- path is created by this test.
	if !bytes.Contains(after, []byte("changed anonymous profile")) || provider.Current().Revision != result.Snapshot.Revision {
		t.Fatal("saved Profile was not published to disk and the runtime snapshot")
	}
}
