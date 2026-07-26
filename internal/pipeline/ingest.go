package pipeline

import (
	"context"
	"fmt"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/store"
)

// IngestResult is what one captured job looks like right after ingest. It
// carries the current state and score rather than a presentation verdict: the
// wording belongs to the API viewmodel.
type IngestResult struct {
	JobID        int64
	ProcessState string
	Score        *store.Score
	FilterHits   []string
	// Created reports whether this capture stored the job for the first time.
	Created bool
	// Cached reports that an unchanged job already had a current score.
	Cached bool
}

// IngestList stores the partial jobs harvested from a 104 list page and applies
// the screening rules the visible fields support. It calls no Agent and fetches
// nothing: the user is still on the list page waiting for the marks.
func (p Pipeline) IngestList(ctx context.Context, rows []crawler.RawJob) ([]IngestResult, error) {
	if p.Store == nil {
		return nil, fmt.Errorf("pipeline: store is required")
	}
	results := make([]IngestResult, 0, len(rows))
	snapshot, err := p.snapshot()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if !row.Partial() {
			return nil, fmt.Errorf("pipeline: list item %q must not carry a description", row.ExternalID)
		}
		upsert, err := p.Store.UpsertJob(ctx, jobInput(row, snapshot.Revision), nil)
		if err != nil {
			return nil, err
		}
		job := upsert.Job
		if upsert.Created {
			if job, err = p.screen(ctx, job, snapshot); err != nil {
				return nil, err
			}
		}
		job, redirected, err := p.canonical(ctx, job)
		if err != nil {
			return nil, err
		}
		result, err := p.result(ctx, job, upsert.Created, redirected)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// IngestJob stores one captured 104 detail page. Screening runs synchronously
// because it needs no LLM; scoring is left to the worker, so a job that passes
// screening comes back as pending rather than blocking the capture request.
func (p Pipeline) IngestJob(ctx context.Context, row crawler.RawJob) (IngestResult, error) {
	if p.Store == nil {
		return IngestResult{}, fmt.Errorf("pipeline: store is required")
	}
	if row.Partial() {
		return IngestResult{}, fmt.Errorf("pipeline: captured job %q requires a description", row.ExternalID)
	}
	snapshot, err := p.snapshot()
	if err != nil {
		return IngestResult{}, err
	}
	upsert, err := p.Store.UpsertJob(ctx, jobInput(row, snapshot.Revision), nil)
	if err != nil {
		return IngestResult{}, err
	}
	job := upsert.Job
	if job.ProcessState == "new" {
		if job, err = p.screen(ctx, job, snapshot); err != nil {
			return IngestResult{}, err
		}
	}
	job, redirected, err := p.canonical(ctx, job)
	if err != nil {
		return IngestResult{}, err
	}
	return p.result(ctx, job, upsert.Created, redirected || (!upsert.Created && !upsert.Changed))
}

// canonical resolves a captured job to the copy that carries the verdict. When
// another source already covers the same listing, the user is shown that copy's
// existing assessment: the same job is never scored twice.
func (p Pipeline) canonical(ctx context.Context, job store.Job) (store.Job, bool, error) {
	canonicalID, err := p.link(ctx, job.ID)
	if err != nil {
		return job, false, err
	}
	if canonicalID == job.ID {
		return job, false, nil
	}
	detail, found, err := p.Store.GetJobDetail(ctx, canonicalID)
	if err != nil {
		return job, false, err
	}
	if !found {
		return job, false, fmt.Errorf("pipeline: canonical job %d of job %d is missing", canonicalID, job.ID)
	}
	return detail.Job, true, nil
}

// screen applies the screening rules a job's available fields support and moves
// it out of its intake state accordingly.
func (p Pipeline) screen(ctx context.Context, job store.Job, snapshot workProfile) (store.Job, error) {
	hits := snapshot.Filter.Match(job)
	if job.ProcessState == "new" {
		if err := p.Store.CommitFilter(ctx, job.ID, snapshot.Revision, hits); err != nil {
			return job, err
		}
		job.ProcessState = "queued"
		if len(hits) > 0 {
			job.ProcessState = "filtered_out"
		}
		job.FilterHits = hits
		return job, nil
	}
	if len(hits) > 0 && job.ProcessState == "discovered" {
		if err := p.Store.SetFilterHits(ctx, job.ID, hits); err != nil {
			return job, err
		}
		if err := p.Store.TransitionProcess(ctx, job.ID, "filtered_out"); err != nil {
			return job, err
		}
		job.ProcessState, job.FilterHits = "filtered_out", hits
	}
	return job, nil
}

func (p Pipeline) result(ctx context.Context, job store.Job, created, unchanged bool) (IngestResult, error) {
	score, err := p.Store.CurrentScore(ctx, job.ID)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{JobID: job.ID, ProcessState: job.ProcessState, Score: score, FilterHits: job.FilterHits, Created: created, Cached: unchanged && score != nil}, nil
}

// RequestLetter records a user's request to draft a letter. It returns as soon
// as the state is stored; the worker picks the job up on its own.
func (p Pipeline) RequestLetter(ctx context.Context, jobID int64) error {
	if p.Store == nil {
		return fmt.Errorf("pipeline: store is required")
	}
	if _, err := p.snapshot(); err != nil {
		return err
	}
	detail, found, err := p.Store.GetJobDetail(ctx, jobID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("pipeline: not found: job %d", jobID)
	}
	if detail.Job.ProcessState == "letter_requested" {
		return nil
	}
	return p.Store.TransitionProcess(ctx, jobID, "letter_requested")
}

// RequestRescore returns one already scored job to the score stage under the
// active Profile revision, so a single wrong score can be redone without
// reprocessing every job. It returns as soon as the state is stored; the worker
// picks the job up on its own and appends a new score beside the old one.
func (p Pipeline) RequestRescore(ctx context.Context, jobID int64) error {
	if p.Store == nil {
		return fmt.Errorf("pipeline: store is required")
	}
	snapshot, err := p.snapshot()
	if err != nil {
		return err
	}
	if err := p.Store.RequeueScore(ctx, jobID, snapshot.Revision); err != nil {
		return err
	}
	p.logger().Info("job requeued for rescore", "stage", "score", "job_id", jobID, "profile_revision", snapshot.Revision)
	return nil
}
