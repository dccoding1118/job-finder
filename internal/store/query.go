package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type JobFilter struct {
	ProcessState string
	// ProcessStates restricts the result to any of the listed states; it carries
	// the API's verdict filter, which maps one verdict to several states.
	ProcessStates []string
	ApplyState    string
	Source        string
}

// Score is the latest matching score retained for a job.
type Score struct {
	Content       int       `json:"content_fit"`
	Benefit       int       `json:"benefit_fit"`
	Bonus         int       `json:"bonus_fit"`
	Industry      int       `json:"industry_fit"`
	Total         float64   `json:"total"`
	Reason        string    `json:"reason"`
	Runner        string    `json:"runner"`
	CreatedAt     time.Time `json:"created_at"`
	ScoreRevision *string   `json:"score_revision"`
}

// Letter is an approved or failed letter retained for a job.
type Letter struct {
	Content        string    `json:"content"`
	Status         string    `json:"status"`
	Rounds         int       `json:"rounds"`
	ReviewLog      string    `json:"review_log"`
	CreatedAt      time.Time `json:"created_at"`
	FilterRevision *string   `json:"filter_revision"`
	ScoreRevision  *string   `json:"score_revision"`
}

// StatusEvent records a process or application-state transition.
type StatusEvent struct {
	Axis      string    `json:"axis"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// JobDetail is the data required by the local API for one job.
type JobDetail struct {
	Job    Job
	Score  *Score
	Letter *Letter
	Filter *FilterResult
	Events []StatusEvent
}

// Run is a recorded pipeline execution.
type Run struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt *time.Time
	// HeartbeatAt is when the run last reported progress. It is nil only for runs
	// recorded before runs reported any, so its absence never means "stalled".
	HeartbeatAt *time.Time
	Trigger     string
	Stats       RunStats
	Error       *string
}

func (s *Store) GetJobDetail(ctx context.Context, id int64) (JobDetail, bool, error) {
	if id <= 0 {
		return JobDetail{}, false, fmt.Errorf("store: invalid job id")
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", id)
	job, found, err := scanJob(row)
	if err != nil || !found {
		return JobDetail{}, found, err
	}
	detail := JobDetail{Job: job, Events: []StatusEvent{}}
	detail.Score, err = s.LatestScore(ctx, id)
	if err != nil {
		return JobDetail{}, false, err
	}
	detail.Filter, err = s.CurrentFilterResult(ctx, id)
	if err != nil {
		return JobDetail{}, false, err
	}
	var letter Letter
	var letterCreatedAt string
	err = s.db.QueryRowContext(ctx, `SELECT l.content, l.status, COALESCE(a.rounds, 0), COALESCE(a.review_log, ''), l.created_at, l.filter_revision, l.score_revision
		FROM letters l LEFT JOIN letter_attempts a ON a.id = l.attempt_id
		WHERE l.job_id=? ORDER BY l.created_at DESC, l.id DESC LIMIT 1`, id).Scan(&letter.Content, &letter.Status, &letter.Rounds, &letter.ReviewLog, &letterCreatedAt, &letter.FilterRevision, &letter.ScoreRevision)
	if err == nil {
		letter.CreatedAt, err = parseTimestamp(letterCreatedAt)
		if err != nil {
			return JobDetail{}, false, fmt.Errorf("decode letter timestamp: %w", err)
		}
		detail.Letter = &letter
	} else if !errors.Is(err, sql.ErrNoRows) {
		return JobDetail{}, false, fmt.Errorf("get letter: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT axis, from_state, to_state, note, created_at FROM status_events WHERE job_id=? ORDER BY created_at, id`, id)
	if err != nil {
		return JobDetail{}, false, fmt.Errorf("list status events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var event StatusEvent
		var createdAt string
		if scanErr := rows.Scan(&event.Axis, &event.FromState, &event.ToState, &event.Note, &createdAt); scanErr != nil {
			return JobDetail{}, false, fmt.Errorf("scan status event: %w", scanErr)
		}
		event.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return JobDetail{}, false, fmt.Errorf("decode status event timestamp: %w", err)
		}
		detail.Events = append(detail.Events, event)
	}
	if err := rows.Err(); err != nil {
		return JobDetail{}, false, fmt.Errorf("iterate status events: %w", err)
	}
	return detail, true, nil
}

// CurrentScore returns the latest score of a job, or nil when it has none.
func (s *Store) CurrentScore(ctx context.Context, jobID int64) (*Score, error) {
	var score Score
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT s.dim_content, s.dim_benefit, s.dim_bonus, s.dim_industry, s.total, s.reason, s.runner, s.created_at, s.score_revision
		FROM scores s JOIN jobs j ON j.id=s.job_id
		WHERE s.job_id=? AND s.score_revision IS j.score_revision
		ORDER BY s.created_at DESC, s.id DESC LIMIT 1`, jobID).Scan(&score.Content, &score.Benefit, &score.Bonus, &score.Industry, &score.Total, &score.Reason, &score.Runner, &createdAt, &score.ScoreRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get score: %w", err)
	}
	score.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, fmt.Errorf("decode score timestamp: %w", err)
	}
	return &score, nil
}

// LatestScore returns the newest historical score regardless of active revision.
func (s *Store) LatestScore(ctx context.Context, jobID int64) (*Score, error) {
	var score Score
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT dim_content, dim_benefit, dim_bonus, dim_industry, total, reason, runner, created_at, score_revision FROM scores WHERE job_id=? ORDER BY created_at DESC, id DESC LIMIT 1`, jobID).Scan(&score.Content, &score.Benefit, &score.Bonus, &score.Industry, &score.Total, &score.Reason, &score.Runner, &createdAt, &score.ScoreRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest score: %w", err)
	}
	score.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return nil, fmt.Errorf("decode score timestamp: %w", err)
	}
	return &score, nil
}

func (s *Store) ListRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, started_at, finished_at, heartbeat_at, trigger, stats, error FROM runs ORDER BY started_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	runs := []Run{}
	for rows.Next() {
		var run Run
		var stats, startedAt string
		var finishedAt, heartbeatAt sql.NullString
		if scanErr := rows.Scan(&run.ID, &startedAt, &finishedAt, &heartbeatAt, &run.Trigger, &stats, &run.Error); scanErr != nil {
			return nil, fmt.Errorf("scan run: %w", scanErr)
		}
		run.StartedAt, err = parseTimestamp(startedAt)
		if err != nil {
			return nil, fmt.Errorf("decode run start timestamp: %w", err)
		}
		if finishedAt.Valid {
			finished, parseErr := parseTimestamp(finishedAt.String)
			if parseErr != nil {
				return nil, fmt.Errorf("decode run finish timestamp: %w", parseErr)
			}
			run.FinishedAt = &finished
		}
		if heartbeatAt.Valid {
			heartbeat, parseErr := parseTimestamp(heartbeatAt.String)
			if parseErr != nil {
				return nil, fmt.Errorf("decode run heartbeat timestamp: %w", parseErr)
			}
			run.HeartbeatAt = &heartbeat
		}
		if err := json.Unmarshal([]byte(stats), &run.Stats); err != nil {
			return nil, fmt.Errorf("decode run stats: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runs: %w", err)
	}
	return runs, nil
}

// CountAgentCallsSince counts successful calls of one role since a wall-clock
// instant. The daily budget is derived from it rather than a stored counter, so
// a restart never resets the remaining allowance.
func (s *Store) CountAgentCallsSince(ctx context.Context, role string, since time.Time) (int, error) {
	if !validAgentRole(role) {
		return 0, fmt.Errorf("store: invalid agent role %q", role)
	}
	count := 0
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_calls WHERE role=? AND ok=1 AND created_at >= ?", role, since.In(time.Local).Format(time.RFC3339)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count agent calls: %w", err)
	}
	return count, nil
}

// SummarizeRunJobs counts the current process states of the jobs a run first
// stored. The distribution is derived at query time because the worker keeps
// processing those jobs long after the run finished.
func (s *Store) SummarizeRunJobs(ctx context.Context, runID int64) (map[string]int, error) {
	if runID <= 0 {
		return nil, fmt.Errorf("store: invalid run id %d", runID)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT process_state, COUNT(*) FROM jobs WHERE discovered_by_run_id=? GROUP BY process_state", runID)
	if err != nil {
		return nil, fmt.Errorf("summarize run jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan run job summary: %w", err)
		}
		counts[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run job summary: %w", err)
	}
	return counts, nil
}

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

type JobSort string

const (
	JobSortNewest JobSort = "newest"
	JobSortScore  JobSort = "score"
)

// ListJobs returns jobs matching optional state and source filters.
func (s *Store) ListJobs(ctx context.Context, filter JobFilter, sort JobSort) ([]Job, error) {
	if sort != "" && sort != JobSortNewest && sort != JobSortScore {
		return nil, fmt.Errorf("store: invalid job sort %q", sort)
	}
	query := `SELECT j.id, j.source, j.external_id, j.url, j.title, j.company_name, j.company_info, j.description, j.salary_min, j.salary_max, j.location, j.remote_type, j.process_state, j.apply_state, j.content_hash, j.filter_hits, j.filter_revision, j.score_revision, latest_score.total
		FROM jobs j
		LEFT JOIN scores latest_score ON latest_score.id = (
			SELECT id FROM scores WHERE job_id = j.id AND score_revision IS j.score_revision ORDER BY created_at DESC, id DESC LIMIT 1
		)
		WHERE j.process_state <> 'merged'
		  AND (? = '' OR j.process_state = ?)
		  AND (? = '' OR j.apply_state = ?)
		  AND (? = '' OR j.source = ?)`
	args := []any{
		filter.ProcessState, filter.ProcessState,
		filter.ApplyState, filter.ApplyState,
		filter.Source, filter.Source,
	}
	if len(filter.ProcessStates) > 0 {
		// #nosec G202 -- only bind-parameter placeholders are concatenated; the states bind through args.
		query += " AND j.process_state IN (?" + strings.Repeat(", ?", len(filter.ProcessStates)-1) + ")"
		for _, state := range filter.ProcessStates {
			args = append(args, state)
		}
	}
	if sort == JobSortScore {
		query += " ORDER BY latest_score.total DESC NULLS LAST, j.updated_at DESC, j.id DESC"
	} else {
		query += " ORDER BY j.updated_at DESC, j.id DESC"
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]Job, 0)
	for rows.Next() {
		var total *float64
		job, err := scanJobRow(trailingScanner{row: rows, trailing: []any{&total}})
		if err != nil {
			return nil, fmt.Errorf("scan listed job: %w", err)
		}
		job.ScoreTotal = total
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listed jobs: %w", err)
	}
	return jobs, nil
}

// PickForStage selects the next jobs for one pipeline stage. What a stale
// Profile revision means depends on what the job already carries: `new` and
// `letter_requested` hold no assessment of their own, so a revision change
// costs them nothing and they are picked whatever revision they were stamped
// with — the stage adopts the active one and does the work the job was waiting
// for anyway. A `queued` job carries a screening verdict, so it is picked only
// while that verdict is current; a superseded screening has to be bought again
// and waits for the user's own reprocess. `filter_unknown` belongs to no stage,
// because only the user releases a job from it.
func (s *Store) PickForStage(ctx context.Context, stage string, revisions Revisions, limit int) ([]Job, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("store: invalid pick limit %d", limit)
	}
	stateByStage := map[string]string{"filter": "new", "score": "queued", "letter": "letter_requested"}
	state, ok := stateByStage[stage]
	if !ok {
		return nil, fmt.Errorf("store: invalid pipeline stage %q", stage)
	}
	// `merged` is never one of the stage states, and the guard states that: an
	// alias must cost no filter, score, or letter work whatever else changes.
	query := "SELECT " + jobColumns + " FROM jobs WHERE process_state = ? AND process_state <> 'merged'"
	args := []any{state}
	if stage == "score" && revisions.Filter != "" {
		query += " AND filter_revision = ?"
		args = append(args, revisions.Filter)
	}
	query += " ORDER BY updated_at, id LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("pick stage jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]Job, 0, limit)
	for rows.Next() {
		job, err := scanJobRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan picked job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate picked jobs: %w", err)
	}
	return jobs, nil
}

// LetterAttempt is one letter generation with the Agent calls it made. Calls are
// ordered as they happened; a legacy attempt built by migration has none, which
// is why the field is a possibly empty slice rather than a promise.
type LetterAttempt struct {
	ID           int64             `json:"id"`
	Status       string            `json:"status"`
	Rounds       int               `json:"rounds"`
	ReviewLog    string            `json:"review_log"`
	RunnerDraft  string            `json:"runner_draft"`
	RunnerReview string            `json:"runner_review"`
	Error        *string           `json:"error"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   *time.Time        `json:"finished_at"`
	Content      *string           `json:"content"`
	Calls        []LetterAgentCall `json:"calls"`
}

// LetterAgentCall is one audited drafter or reviewer call. It carries the output
// and never the prompt: the draft and the review verdict are both in the output,
// while the prompt embeds the whole Profile view.
type LetterAgentCall struct {
	Round      int       `json:"round"`
	Role       string    `json:"role"`
	Runner     string    `json:"runner"`
	Model      string    `json:"model"`
	OK         bool      `json:"ok"`
	DurationMS int64     `json:"duration_ms"`
	Output     string    `json:"output"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListLetterAttempts returns every generation run for one job, newest first, so
// a re-run reads as another entry rather than as a replacement.
func (s *Store) ListLetterAttempts(ctx context.Context, jobID int64) ([]LetterAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.status, a.rounds, a.review_log, a.runner_draft, a.runner_review, a.error, a.started_at, a.finished_at, l.content
		FROM letter_attempts a LEFT JOIN letters l ON l.attempt_id = a.id
		WHERE a.job_id=? ORDER BY a.started_at DESC, a.id DESC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list letter attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	attempts := []LetterAttempt{}
	for rows.Next() {
		var attempt LetterAttempt
		var startedAt string
		var finishedAt *string
		if scanErr := rows.Scan(&attempt.ID, &attempt.Status, &attempt.Rounds, &attempt.ReviewLog, &attempt.RunnerDraft, &attempt.RunnerReview,
			&attempt.Error, &startedAt, &finishedAt, &attempt.Content); scanErr != nil {
			return nil, fmt.Errorf("scan letter attempt: %w", scanErr)
		}
		attempt.StartedAt, err = parseTimestamp(startedAt)
		if err != nil {
			return nil, fmt.Errorf("decode letter attempt timestamp: %w", err)
		}
		if finishedAt != nil {
			finished, parseErr := parseTimestamp(*finishedAt)
			if parseErr != nil {
				return nil, fmt.Errorf("decode letter attempt timestamp: %w", parseErr)
			}
			attempt.FinishedAt = &finished
		}
		attempt.Calls = []LetterAgentCall{}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list letter attempts: %w", err)
	}
	for i := range attempts {
		calls, callErr := s.letterAttemptCalls(ctx, attempts[i].ID)
		if callErr != nil {
			return nil, callErr
		}
		attempts[i].Calls = calls
	}
	return attempts, nil
}

func (s *Store) letterAttemptCalls(ctx context.Context, attemptID int64) ([]LetterAgentCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(round, 0), role, runner, COALESCE(model, ''), ok, duration_ms, output, created_at
		FROM agent_calls WHERE attempt_id=? ORDER BY id`, attemptID)
	if err != nil {
		return nil, fmt.Errorf("list letter attempt calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	calls := []LetterAgentCall{}
	for rows.Next() {
		var call LetterAgentCall
		var ok int
		var createdAt string
		if scanErr := rows.Scan(&call.Round, &call.Role, &call.Runner, &call.Model, &ok, &call.DurationMS, &call.Output, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scan letter attempt call: %w", scanErr)
		}
		call.OK = ok == 1
		call.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, fmt.Errorf("decode letter attempt call timestamp: %w", err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list letter attempt calls: %w", err)
	}
	return calls, nil
}
