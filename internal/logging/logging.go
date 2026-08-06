// Package logging installs the process-wide structured logger and, where the
// platform has no log collector of its own, a size-rotating file sink.
//
// On Linux the services run under systemd and journald collects stderr, so a
// file is optional. Windows Task Scheduler discards a task's output entirely:
// without this sink a scheduled fetch that failed leaves no trace at all, which
// is why the installer configures a log file there.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
)

// Defaults bound the log directory to a few tens of megabytes, which is far
// more than a daily fetch plus worker chatter produces and still small enough
// to leave in a user profile indefinitely.
const (
	DefaultMaxSizeMB = 8
	DefaultKeep      = 4
)

// fileSink records whether Setup installed a file sink, which is what decides
// where ReportFatal can still put an error once the process is on its way out.
var fileSink atomic.Bool

// Setup writes to stderr and, when path is non-empty, to a rotating file as
// well. The returned Closer flushes and releases the file; callers that pass an
// empty path get a no-op.
func Setup(path string, maxSizeMB, keep int) (io.Closer, error) {
	if path == "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
		return io.NopCloser(nil), nil
	}
	if maxSizeMB <= 0 {
		maxSizeMB = DefaultMaxSizeMB
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	file, err := newRotator(path, int64(maxSizeMB)<<20, keep)
	if err != nil {
		return nil, err
	}
	// The file comes first and stderr is explicitly best-effort. io.MultiWriter
	// stops at the first writer that returns an error, and the Windows service
	// binary is a GUI-subsystem executable with no console at all: leaving stderr
	// in front would let its failing write discard every record before the file —
	// the only sink that platform has — was ever offered one.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(file, optional{os.Stderr}), nil)))
	fileSink.Store(true)
	return file, nil
}

// optional is a sink that is allowed to be absent: a write that fails is
// dropped rather than reported, so it cannot stop the writers behind it.
type optional struct{ w io.Writer }

func (o optional) Write(p []byte) (int, error) {
	_, _ = o.w.Write(p)
	return len(p), nil
}

// ReportFatal records an error that is ending the process.
//
// A failed command is reported on stderr, which is enough wherever something
// collects it — journald does, and so does a terminal. Windows has neither for
// the resident service: Task Scheduler discards a task's output, and the
// service binary has no console to write to in the first place. Without this,
// a service that cannot start leaves no trace anywhere at all, which is the
// hardest possible failure to diagnose.
func ReportFatal(err error) {
	if err == nil {
		return
	}
	if fileSink.Load() {
		slog.Error("exiting", "error", err.Error())
		return
	}
	// No file sink yet means the failure happened before the configuration was
	// parsed. Away from Windows stderr is collected and nothing more is wanted;
	// on Windows the platform's default log location is the only place left.
	if runtime.GOOS != "windows" {
		return
	}
	appendFallback(err)
}

// appendFallback writes one line to the location the installer configures for
// this platform, resolved from the same decision point the rest of the program
// uses. Every step is best-effort: this runs while the process is already
// failing, and a failure to record the failure must not replace it.
func appendFallback(err error) {
	layout, resolveErr := paths.Resolve()
	if resolveErr != nil || layout.LogFile == "" {
		return
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(layout.LogFile), 0o700); mkdirErr != nil {
		return
	}
	file, openErr := os.OpenFile(layout.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- path comes from the resolved layout.
	if openErr != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintf(file, "time=%s level=ERROR msg=exiting error=%q\n",
		time.Now().Format(time.RFC3339), err.Error())
}

// rotator writes to one file and renames it aside once it passes the size
// limit, keeping a fixed number of generations. Rotation happens before a write
// that would cross the limit, so a single record is never split across files.
type rotator struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	file *os.File
	size int64
}

func newRotator(path string, max int64, keep int) (*rotator, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logging: create log directory: %w", err)
	}
	r := &rotator{path: path, max: max, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotator) open() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- path comes from the resolved layout or explicit configuration.
	if err != nil {
		return fmt.Errorf("logging: open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("logging: stat log file: %w", err)
	}
	r.file, r.size = file, info.Size()
	return nil
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate discards the oldest generation, shifts jobfinder.log.N up by one and
// moves the live file to .1, so keep counts every file the directory holds —
// the live one included. The oldest is removed explicitly rather than left to
// be overwritten by the rename, which Windows refuses to do.
func (r *rotator) rotate() error {
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("logging: close log file: %w", err)
	}
	if r.keep <= 1 {
		if err := os.Remove(r.path); err != nil {
			return fmt.Errorf("logging: discard log file: %w", err)
		}
		return r.open()
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep-1))
	for i := r.keep - 2; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", r.path, i)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if err := os.Rename(from, fmt.Sprintf("%s.%d", r.path, i+1)); err != nil {
			return fmt.Errorf("logging: rotate log file: %w", err)
		}
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return fmt.Errorf("logging: rotate log file: %w", err)
	}
	return r.open()
}

func (r *rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}
