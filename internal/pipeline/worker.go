package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dccoding1118/job-finder/internal/profile"
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

	mu        sync.Mutex
	lastAuto  atomic.Bool
	autoRead  atomic.Bool
	lastStale atomic.Int64
	staleRead atomic.Bool
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
		if stats.total() > 0 {
			w.logger().Info("worker pass consumed jobs", "filtered", stats.Filtered, "scored", stats.Scored, "lettered", stats.Lettered)
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
		case <-w.profileChanges():
		case <-time.After(interval):
		}
	}
}

func (w *Worker) profileChanges() <-chan struct{} {
	if w.Pipeline.Provider != nil {
		return w.Pipeline.Provider.Changes()
	}
	return nil
}

// Tick runs one pass of every stage. Passes are serialized by a process-local
// mutex, which is what keeps LLM calls sequential without a lock file.
func (w *Worker) Tick(ctx context.Context) (WorkerStats, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stats := WorkerStats{}
	var failures []error
	auto, autoErr := w.autoProcessing(ctx)
	if autoErr != nil {
		return stats, autoErr
	}
	if auto {
		// Scoring runs first, and to exhaustion: a job already screened is one call
		// away from the verdict the user is waiting for, and letting screening go
		// first would bury it behind a whole batch of jobs that have not started
		// yet. One scoring pass only takes as many jobs as its batch size and the
		// day's budget allow, so it is repeated until it finds nothing left to
		// take. The screening pass then carries each job it queues straight on to
		// scoring, so it normally finds only what a previous pass left behind.
		scored, scoreErr := w.drainScoring(ctx)
		if errors.Is(scoreErr, profile.ErrNotReady) {
			return stats, nil
		}
		stats.Scored = scored
		failures = append(failures, scoreErr)
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		filtered, filterErr := w.Pipeline.filterJobs(ctx, 0, true)
		if errors.Is(filterErr, profile.ErrNotReady) {
			return stats, nil
		}
		stats.Filtered = filtered.Processed
		stats.Scored += filtered.Scored
		failures = append(failures, filterErr)
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		failures = append(failures, w.reportAwaitingReprocess(ctx))
	}
	lettered, err := w.Pipeline.LetterWithStats(ctx, 0)
	stats.Lettered = lettered.Processed
	failures = append(failures, err)
	return stats, errors.Join(failures...)
}

// drainScoring runs the score stage until it has nothing left to take, and
// reports how many jobs it scored in total. One pass is bounded by its batch
// size and by the day's remaining budget, so "score everything waiting before
// screening anything new" takes as many passes as those bounds impose.
//
// It stops on the first pass that scores nothing, which is what ends the loop
// once the queue is drained, held back by a superseded screening, or stopped by
// the switch. A pass that reports a failure also ends it: the jobs it could not
// take keep their state and are picked up on the next tick, rather than being
// retried — and paid for again — inside this one.
func (w *Worker) drainScoring(ctx context.Context) (int, error) {
	total := 0
	for {
		pass, err := w.Pipeline.ScoreWithStats(ctx, 0)
		total += pass.Processed
		if err != nil || pass.Processed == 0 || ctx.Err() != nil {
			return total, err
		}
	}
}

// autoProcessing reads the switch and reports one line whenever it changes, so
// a database that stops consuming jobs says why in the service log.
func (w *Worker) autoProcessing(ctx context.Context) (bool, error) {
	enabled, err := w.Pipeline.autoAllowed(ctx)
	if err != nil {
		return false, err
	}
	if !w.autoRead.Swap(true) || w.lastAuto.Load() != enabled {
		w.lastAuto.Store(enabled)
		w.logger().Info("automatic processing setting read", "enabled", enabled)
	}
	return enabled, nil
}

// reportAwaitingReprocess reports one line whenever the number of jobs held
// back by a superseded screening changes, so a worker that consumes nothing
// says why instead of going quiet. Those jobs are released by the Profile
// reprocess on the system page, or one at a time by processing a job now.
func (w *Worker) reportAwaitingReprocess(ctx context.Context) error {
	if w.Pipeline.Store == nil {
		return nil
	}
	snapshot, err := w.Pipeline.snapshot()
	if err != nil {
		return nil
	}
	count, err := w.Pipeline.Store.CountAwaitingReprocess(ctx, snapshot.Revisions)
	if err != nil {
		return err
	}
	if !w.staleRead.Swap(true) || w.lastStale.Load() != int64(count) {
		w.lastStale.Store(int64(count))
		if count > 0 {
			w.logger().Info("jobs held back by a superseded screening revision", "jobs", count, "filter_revision", snapshot.Revisions.Filter)
		}
	}
	return nil
}

func (w *Worker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}
