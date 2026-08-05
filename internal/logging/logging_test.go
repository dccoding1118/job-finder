package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupWithoutPathIsANoOp(t *testing.T) {
	closer, err := Setup("", 0, 0)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestRotatorKeepsAFixedNumberOfGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobfinder.log")
	r, err := newRotator(path, 32, 3)
	if err != nil {
		t.Fatalf("new rotator: %v", err)
	}
	defer func() { _ = r.Close() }()
	for i := 0; i < 10; i++ {
		if _, writeErr := r.Write([]byte(strings.Repeat("x", 20) + "\n")); writeErr != nil {
			t.Fatalf("write: %v", writeErr)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 3 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("kept %v, want 3 generations", names)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("oldest kept generation missing: %v", err)
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatal("generation beyond the keep limit was not discarded")
	}
}

// A record must never be cut in half by a rotation, otherwise a log line that
// records a failure becomes unparseable exactly when it matters.
func TestRotatorNeverSplitsARecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobfinder.log")
	r, err := newRotator(path, 40, 2)
	if err != nil {
		t.Fatalf("new rotator: %v", err)
	}
	defer func() { _ = r.Close() }()
	record := strings.Repeat("a", 30) + "\n"
	for i := 0; i < 3; i++ {
		if _, writeErr := r.Write([]byte(record)); writeErr != nil {
			t.Fatalf("write: %v", writeErr)
		}
	}
	contents, err := os.ReadFile(path) // #nosec G304 -- path is inside the test's temporary directory.
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
		if line != strings.TrimSuffix(record, "\n") {
			t.Fatalf("record was truncated by rotation: %q", line)
		}
	}
}

// A log larger than the limit still has to be written; refusing it would lose
// the very record that is worth keeping.
func TestRotatorWritesAnOversizedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobfinder.log")
	r, err := newRotator(path, 8, 2)
	if err != nil {
		t.Fatalf("new rotator: %v", err)
	}
	defer func() { _ = r.Close() }()
	big := strings.Repeat("z", 64)
	n, err := r.Write([]byte(big))
	if err != nil || n != len(big) {
		t.Fatalf("write = (%d, %v), want (%d, nil)", n, err, len(big))
	}
}
