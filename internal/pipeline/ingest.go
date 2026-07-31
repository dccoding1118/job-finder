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
		upsert, err := p.Store.UpsertJob(ctx, jobInput(row, snapshot.Revisions.Filter), nil)
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
	upsert, err := p.Store.UpsertJob(ctx, jobInput(row, snapshot.Revisions.Filter), nil)
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

// screen applies the structural hard rules a job's available fields support.
// Only a rejection is conclusive here: a job that passes them still owes the
// semantic conditions, which the worker runs asynchronously, so the capture
// request never waits on an Agent.
func (p Pipeline) screen(ctx context.Context, job store.Job, snapshot workProfile) (store.Job, error) {
	partial := job.Description == nil
	conditions := snapshot.Filter.Evaluate(job, partial)
	hits := failedTexts(conditions)
	if job.ProcessState == "new" {
		if len(hits) == 0 {
			return job, nil
		}
		result := store.FilterResult{Outcome: store.FilterFail, Conditions: conditions, Stage: "structural"}
		if err := p.Store.SaveFilterResult(ctx, job.ID, result, snapshot.Revisions); err != nil {
			return job, err
		}
		job.ProcessState, job.FilterHits = "filtered_out", hits
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

// failedTexts lists the conditions a job outright failed. Undecided conditions
// are deliberately absent: they are not a reason to reject anything.
func failedTexts(conditions []store.FilterCondition) []string {
	texts := []string{}
	for _, condition := range conditions {
		if condition.Verdict == store.FilterFail {
			texts = append(texts, condition.Text)
		}
	}
	return texts
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

// RequestReprocess returns one job to the start of the pipeline under the active
// Profile revisions, so a single wrong verdict — a screening rejection as much
// as a score — can be redone without reprocessing every job. It returns as soon
// as the state is stored; the worker picks the job up on its own.
func (p Pipeline) RequestReprocess(ctx context.Context, jobID int64) error {
	if p.Store == nil {
		return fmt.Errorf("pipeline: store is required")
	}
	snapshot, err := p.snapshot()
	if err != nil {
		return err
	}
	if err := p.Store.ReprocessJob(ctx, jobID, snapshot.Revisions); err != nil {
		return err
	}
	p.logger().Info("job requeued for reprocess", "stage", "filter", "job_id", jobID, "filter_revision", snapshot.Revisions.Filter)
	return nil
}
