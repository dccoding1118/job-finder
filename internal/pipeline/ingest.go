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
	for _, row := range rows {
		if !row.Partial() {
			return nil, fmt.Errorf("pipeline: list item %q must not carry a description", row.ExternalID)
		}
		upsert, err := p.Store.UpsertJob(ctx, jobInput(row), nil)
		if err != nil {
			return nil, err
		}
		job := upsert.Job
		if upsert.Created {
			if job, err = p.screen(ctx, job); err != nil {
				return nil, err
			}
		}
		result, err := p.result(ctx, job, upsert.Created, false)
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
	upsert, err := p.Store.UpsertJob(ctx, jobInput(row), nil)
	if err != nil {
		return IngestResult{}, err
	}
	job := upsert.Job
	if job.ProcessState == "new" {
		if job, err = p.screen(ctx, job); err != nil {
			return IngestResult{}, err
		}
	}
	return p.result(ctx, job, upsert.Created, !upsert.Created && !upsert.Changed)
}

// screen applies the screening rules a job's available fields support and moves
// it out of its intake state accordingly.
func (p Pipeline) screen(ctx context.Context, job store.Job) (store.Job, error) {
	hits := p.Filter.Match(job)
	if len(hits) > 0 {
		if err := p.Store.SetFilterHits(ctx, job.ID, hits); err != nil {
			return job, err
		}
		if err := p.Store.TransitionProcess(ctx, job.ID, "filtered_out"); err != nil {
			return job, err
		}
		job.ProcessState, job.FilterHits = "filtered_out", hits
		return job, nil
	}
	if job.ProcessState == "new" {
		if err := p.Store.TransitionProcess(ctx, job.ID, "queued"); err != nil {
			return job, err
		}
		job.ProcessState = "queued"
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
