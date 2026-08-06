package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// workerLock is one process's exclusive claim on the pipeline stages, so a
// hand-driven `run --stage` batch and the API server's resident worker cannot
// consume the same queue at once.
//
// The claim is an operating-system lock held on an open file handle, not the
// existence of a lock file. That distinction is the whole point: a lock the
// kernel owns is released when the process ends however it ends, including
// being terminated outright. Windows Task Scheduler stops a task by terminating
// it and so does a shutdown, so a claim that depended on the program running
// its own cleanup would survive every stop and leave the service permanently
// unable to start again.
type workerLock struct{ file *os.File }

func lockWorker(dbPath string) (*workerLock, error) {
	// #nosec G304 -- the lock is derived solely from the explicit local SQLite path.
	file, err := os.OpenFile(workerLockPath(dbPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create worker lock: %w", err)
	}
	held, err := tryLock(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("claim worker lock: %w", err)
	}
	if !held {
		_ = file.Close()
		return nil, fmt.Errorf("another process is consuming the pipeline stages")
	}
	return &workerLock{file: file}, nil
}

// release drops the claim. The file itself is left behind on purpose: the claim
// lives in the handle, so deleting it would only race a process that has just
// opened it, and a lock file nobody holds a lock on claims nothing.
func (l *workerLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlock(l.file)
	_ = l.file.Close()
}

func workerLockPath(dbPath string) string { return filepath.Clean(dbPath) + ".worker.lock" }
