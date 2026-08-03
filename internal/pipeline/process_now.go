package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/dccoding1118/job-finder/internal/store"
)

// ErrNotWaiting reports that a job is in no state the user's own processing
// request can act on: it is already assessed, waiting for its JD text, or in
// the letter stages, and the answer there is a reprocess rather than a push.
var ErrNotWaiting = errors.New("pipeline: job is not waiting to be screened or scored")

// ProcessJobNow screens and, when the job passes, scores one job right away,
// ahead of the resident worker's batch. It is the user's own request on one
// job they are looking at, so it runs whether or not automatic processing is
// switched on and it is not held back by the daily budgets — the calls are
// still audited in `agent_calls`, so the day's usage reports what was spent.
//
// It runs the job as far as it goes in one call: a job at `new` is screened and
// then, if screening queued it, scored. Callers run it in the background; the
// state the job reaches is read back through the job views.
//
// A superseded Profile revision never turns the request away. Clicking this on
// one job is the user asking for that job to be assessed under the Profile as
// it stands now, which is the same consent a reprocess carries — so a job
// queued on a stale screening is returned to screening here rather than left
// waiting for a bulk reprocess it would only be a single row of.
func (p Pipeline) ProcessJobNow(ctx context.Context, jobID int64) error {
	if p.Store == nil {
		return fmt.Errorf("pipeline: store is required")
	}
	// Returning a stale job to screening, screening it, and scoring it are three
	// units of work, each read back from the state the previous one left.
	for step := 0; step < 3; step++ {
		snapshot, err := p.snapshot()
		if err != nil {
			return err
		}
		detail, found, err := p.Store.GetJobDetail(ctx, jobID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("pipeline: not found: job %d", jobID)
		}
		job := detail.Job
		switch job.ProcessState {
		case "new":
			// screenAndStore adopts the active screening revision, so a job that was
			// waiting on an older Profile is simply screened under the current one.
			result, stored, err := p.screenAndStore(ctx, job, snapshot, false, true)
			if err != nil {
				return fmt.Errorf("filter job %d: %w", jobID, err)
			}
			if !stored || result.Outcome == store.FilterFail {
				return nil
			}
		case "queued":
			// The screening that queued this job is superseded, so its screening
			// verdict is bought again before a score is derived from it.
			if !usesRevision(job.FilterRevision, snapshot.Revisions.Filter) {
				if err := p.RequestReprocess(ctx, jobID); err != nil {
					return fmt.Errorf("reprocess job %d: %w", jobID, err)
				}
				continue
			}
			if _, _, err := p.scoreAndStore(ctx, job, snapshot, false, true); err != nil {
				return fmt.Errorf("score job %d: %w", jobID, err)
			}
			return nil
		default:
			// Anything else on the first pass is a job the request cannot act on.
			// Later it is simply where the previous step left the job: a verdict, or
			// `discovered` for an excerpt whose JD text has to be opened again.
			if step == 0 {
				return ErrNotWaiting
			}
			return nil
		}
	}
	return nil
}
