//go:build windows

package cli

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes a non-blocking exclusive byte-range lock, the Windows
// equivalent of flock. LOCKFILE_FAIL_IMMEDIATELY is what makes a contended lock
// return instead of queueing behind the holder.
func tryLock(file *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return false, nil
	default:
		return false, err
	}
}

func unlock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
