//go:build !windows

package cli

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes a non-blocking exclusive flock. A lock already held elsewhere
// is a "no", not an error: the caller reports it as another process consuming
// the stages, which is a normal situation rather than a fault.
func tryLock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func unlock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
