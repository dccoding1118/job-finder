package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRescoreNotAllowed reports that a job is not in a state a manual rescore
// may leave: only a completed score may be redone, and never a letter history.
var ErrRescoreNotAllowed = errors.New("store: job cannot be rescored")

// AgentCall is one audited Agent invocation, the record every processing
// progress question is answered from.
type AgentCall struct {
	ID         int64     `json:"id"`
	JobID      *int64    `json:"job_id"`
	Role       string    `json:"role"`
	Runner     string    `json:"runner"`
	OK         bool      `json:"ok"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
	// Output carries the raw response of a failed call and stays empty for a
	// successful one. Callers decide how much of it, if any, to expose.
	Output string
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
	rows, err := s.db.QueryContext(ctx, "SELECT id, job_id, role, runner, ok, duration_ms, output, created_at FROM agent_calls ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("list agent calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	calls := make([]AgentCall, 0, limit)
	for rows.Next() {
		var call AgentCall
		var ok int
		var output, createdAt string
		if scanErr := rows.Scan(&call.ID, &call.JobID, &call.Role, &call.Runner, &ok, &call.DurationMS, &output, &createdAt); scanErr != nil {
			return nil, fmt.Errorf("scan agent call: %w", scanErr)
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

// RequeueScore returns one already scored job to the score stage under the
// given revision. Scores are append-only, so the previous score stays as
// history until the new one lands; letter states are refused, which keeps a
// manual rescore from rewriting a letter or an application record.
func (s *Store) RequeueScore(ctx context.Context, jobID int64, revision string) error {
	if jobID <= 0 || revision == "" {
		return errors.New("store: job id and profile revision are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var from string
	if err := tx.QueryRowContext(ctx, "SELECT process_state FROM jobs WHERE id=?", jobID).Scan(&from); err != nil {
		return fmt.Errorf("read job %d: %w", jobID, err)
	}
	if from == "queued" {
		return nil
	}
	if from != "scored" && from != "shortlisted" {
		return ErrRescoreNotAllowed
	}
	now := s.timestamp()
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state='queued', profile_revision=?, updated_at=? WHERE id=?", revision, now, jobID); err != nil {
		return fmt.Errorf("requeue job %d: %w", jobID, err)
	}
	if err := insertEvent(ctx, tx, jobID, "process", from, "queued", "manual rescore", now); err != nil {
		return err
	}
	return tx.Commit()
}
