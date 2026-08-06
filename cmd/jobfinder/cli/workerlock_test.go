package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerLockExcludesASecondHolder(t *testing.T) {
	db := filepath.Join(t.TempDir(), "jobs.db")
	held, err := lockWorker(db)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	defer held.release()

	if _, err := lockWorker(db); err == nil {
		t.Fatal("a second holder was allowed to consume the same stages")
	} else if !strings.Contains(err.Error(), "another process") {
		t.Fatalf("err = %v, want the contended-lock message", err)
	}
}

func TestWorkerLockIsReclaimableAfterRelease(t *testing.T) {
	db := filepath.Join(t.TempDir(), "jobs.db")
	first, err := lockWorker(db)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	first.release()

	second, err := lockWorker(db)
	if err != nil {
		t.Fatalf("second claim after release: %v", err)
	}
	second.release()
}

// A lock file left behind by a process that was terminated rather than shut
// down must not claim anything. Windows Task Scheduler stops a task by
// terminating it, so a claim that survived in the file system would make every
// stop the last one: the service would never start again.
func TestWorkerLockIgnoresAFileLeftByATerminatedProcess(t *testing.T) {
	db := filepath.Join(t.TempDir(), "jobs.db")
	if err := os.WriteFile(workerLockPath(db), nil, 0o600); err != nil {
		t.Fatalf("stage the stale lock file: %v", err)
	}
	lock, err := lockWorker(db)
	if err != nil {
		t.Fatalf("claim over a stale lock file: %v", err)
	}
	lock.release()
}
