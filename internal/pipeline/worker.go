package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// DefaultScanInterval is how long the worker sleeps when nothing is pending.
const DefaultScanInterval = 5 * time.Second

// Worker consumes the filter, score, and letter stages continuously. It is the
// single in-process consumer, so jobs written by the scheduled fetch, the CLI,
// and extension capture all take the same path with no wait for a next round.
type Worker struct {
	Pipeline     Pipeline
	ScanInterval time.Duration
	Logger       *slog.Logger

	mu sync.Mutex
}

// WorkerStats counts what one pass consumed.
type WorkerStats struct{ Filtered, Scored, Lettered int }

func (s WorkerStats) total() int { return s.Filtered + s.Scored + s.Lettered }

// Run consumes pending jobs until ctx is cancelled. A stage error leaves its
// job in place and is retried on the next pass, so one failing job neither
// stops the worker nor blocks the others.
func (w *Worker) Run(ctx context.Context) error {
	interval := w.ScanInterval
	if interval <= 0 {
		interval = DefaultScanInterval
	}
	for {
		stats, err := w.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger().Error("worker stage failed", "error", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stats.total() > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Tick runs one pass of every stage. Passes are serialized by a process-local
// mutex, which is what keeps LLM calls sequential without a lock file.
func (w *Worker) Tick(ctx context.Context) (WorkerStats, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stats := WorkerStats{}
	var failures []error
	filtered, err := w.Pipeline.FilterJobsWithStats(ctx, 0)
	stats.Filtered = filtered.Processed
	failures = append(failures, err)
	if ctx.Err() != nil {
		return stats, ctx.Err()
	}
	scored, err := w.Pipeline.ScoreWithStats(ctx, 0)
	stats.Scored = scored.Processed
	failures = append(failures, err)
	if ctx.Err() != nil {
		return stats, ctx.Err()
	}
	lettered, err := w.Pipeline.LetterWithStats(ctx, 0)
	stats.Lettered = lettered.Processed
	failures = append(failures, err)
	return stats, errors.Join(failures...)
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
