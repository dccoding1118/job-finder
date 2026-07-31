package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrReprocessNotAllowed reports that a job is in a state a manual reprocess
// must not leave: a letter history, which belongs to the user, or an alias,
// which only an unmerge may release.
var ErrReprocessNotAllowed = errors.New("store: job cannot be reprocessed")

// AgentCall is one audited Agent invocation, the record every processing
// progress question is answered from.
type AgentCall struct {
	ID         int64  `json:"id"`
	JobID      *int64 `json:"job_id"`
	Role       string `json:"role"`
	Runner     string `json:"runner"`
	Model      string `json:"model"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Usage      AgentCallUsage
	CreatedAt  time.Time `json:"created_at"`
	// Output carries the raw response of a failed call and stays empty for a
	// successful one. Callers decide how much of it, if any, to expose.
	Output string
}

// DailyAgentUsage is one runner+model's token/cost total for one Asia/Taipei
// calendar day (the day boundary a `+8 hours` shift onto UTC-stored
// timestamps computes, matching the budget resets the resident worker uses).
type DailyAgentUsage struct {
	Date             string  `json:"date"`
	Runner           string  `json:"runner"`
	Model            string  `json:"model"`
	Calls            int     `json:"calls"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// CountJobsByState reports how many jobs sit in each process state. It is the
// backlog view: what the resident worker still has to consume.
func (s *Store) CountJobsByState(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT process_state, COUNT(*) FROM jobs GROUP BY process_state")
	if err != nil {
		return nil, fmt.Errorf("count jobs by state: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan job state count: %w", err)
		}
		counts[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job state counts: %w", err)
	}
	return counts, nil
}

// RecentAgentCalls returns the newest audited Agent calls.
func (s *Store) RecentAgentCalls(ctx context.Context, limit int) ([]AgentCall, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("store: invalid agent call limit %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, job_id, role, runner, model, ok, duration_ms, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_usd, output, created_at FROM agent_calls ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list agent calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	calls := make([]AgentCall, 0, limit)
	for rows.Next() {
		var call AgentCall
		var ok int
		var model *string
		var output, createdAt string
		if scanErr := rows.Scan(&call.ID, &call.JobID, &call.Role, &call.Runner, &model, &ok, &call.DurationMS,
			&call.Usage.InputTokens, &call.Usage.OutputTokens, &call.Usage.CacheReadTokens, &call.Usage.CacheWriteTokens, &call.Usage.ReasoningTokens, &call.Usage.CostUSD,
			&output, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scan agent call: %w", scanErr)
		}
		if model != nil {
			call.Model = *model
		}
		call.OK = ok == 1
		if !call.OK {
			call.Output = output
		}
		call.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, fmt.Errorf("decode agent call timestamp: %w", err)
		}
		calls = append(calls, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent calls: %w", err)
	}
	return calls, nil
}

// AgentUsageByDay reports token/cost totals per Asia/Taipei calendar day,
// broken out by runner and model, for the last `days` days. Every call is
// counted, not only successful ones: a call that burned tokens but failed
// validation still cost real usage. The Taipei boundary is computed in SQL by
// shifting each stored (offset-qualified) timestamp onto UTC and adding 8
// hours before truncating to a date, so it lands on the same day the
// resident worker's own budget reset uses.
func (s *Store) AgentUsageByDay(ctx context.Context, days int) ([]DailyAgentUsage, error) {
	if days <= 0 {
		days = 14
	}
	since := s.now().AddDate(0, 0, -days).In(time.Local).Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(created_at, '+8 hours') AS taipei_date, runner, model, COUNT(*),
		       SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(reasoning_tokens), SUM(cost_usd)
		FROM agent_calls
		WHERE created_at >= ?
		GROUP BY taipei_date, runner, model
		ORDER BY taipei_date DESC, runner, model`, since)
	if err != nil {
		return nil, fmt.Errorf("list agent usage by day: %w", err)
	}
	defer func() { _ = rows.Close() }()
	usage := []DailyAgentUsage{}
	for rows.Next() {
		var row DailyAgentUsage
		var model *string
		if scanErr := rows.Scan(&row.Date, &row.Runner, &model, &row.Calls,
			&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens, &row.ReasoningTokens, &row.CostUSD); scanErr != nil {
			return nil, fmt.Errorf("scan agent usage by day: %w", scanErr)
		}
		if model != nil {
			row.Model = *model
		}
		usage = append(usage, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent usage by day: %w", err)
	}
	return usage, nil
}

// ReprocessJob returns one job to the start of the pipeline under the given
// revisions, so a single wrong verdict can be redone without reprocessing every
// job. It is deliberately not a rescore: screening and scoring are one decision
// chain, and a verdict that came out wrong is as often a screening result as a
// score. How far back the job goes is decided by what it carries — a full JD is
// screened again from `new`, an excerpt returns to `discovered` to be opened,
// which is the same rule a revision activation applies.
//
// The job's previous hits go with it and its score revision is dropped: they
// described an assessment that no longer stands. Scores are append-only, so the
// previous score stays as history until a new one lands. Letter states and
// aliases are refused, which keeps a reprocess from rewriting a letter, an
// application record, or a merge the user decided.
func (s *Store) ReprocessJob(ctx context.Context, jobID int64, revisions Revisions) error {
	if jobID <= 0 || revisions.Filter == "" {
		return errors.New("store: job id and screening revision are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var from string
	var description *string
	if err := tx.QueryRowContext(ctx, "SELECT process_state, description FROM jobs WHERE id=?", jobID).Scan(&from, &description); err != nil {
		return fmt.Errorf("read job %d: %w", jobID, err)
	}
	if protectedProfileState(from) {
		return ErrReprocessNotAllowed
	}
	to := "new"
	if description == nil {
		to = "discovered"
	}
	now := s.timestamp()
	// The revisions are rewritten even when the state does not change: a job
	// already waiting at the start of the pipeline but carrying a superseded
	// revision would never be picked up by any stage.
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, filter_hits=NULL, filter_revision=?, score_revision=NULL, updated_at=? WHERE id=?", to, revisions.Filter, now, jobID); err != nil {
		return fmt.Errorf("reprocess job %d: %w", jobID, err)
	}
	if err := dropFilterResults(ctx, tx, jobID); err != nil {
		return err
	}
	if from != to {
		if err := insertEvent(ctx, tx, jobID, "process", from, to, "manual reprocess", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
