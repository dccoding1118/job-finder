package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrStaleRevision = errors.New("store: stale profile revision")

type ActivationStats struct {
	PartialScreened int
	Requeued        int
	Protected       int
	Unchanged       int
}

// EstimateActivation reports the work a different revision would schedule.
func (s *Store) EstimateActivation(ctx context.Context, revision string) (ActivationStats, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT process_state, description, profile_revision FROM jobs")
	if err != nil {
		return ActivationStats{}, fmt.Errorf("estimate profile activation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	stats := ActivationStats{}
	for rows.Next() {
		var state string
		var description, current sql.NullString
		if err := rows.Scan(&state, &description, &current); err != nil {
			return ActivationStats{}, err
		}
		if current.Valid && current.String == revision {
			stats.Unchanged++
		} else if protectedProfileState(state) {
			stats.Protected++
		} else if !description.Valid {
			stats.PartialScreened++
		} else {
			stats.Requeued++
		}
	}
	return stats, rows.Err()
}

// ActivateProfile applies the revision state matrix in one SQLite transaction.
// screen is pure and must only inspect the supplied Job.
func (s *Store) ActivateProfile(ctx context.Context, revision string, screen func(Job) []string) (ActivationStats, error) {
	if revision == "" || screen == nil {
		return ActivationStats{}, errors.New("store: revision and screen function are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivationStats{}, fmt.Errorf("begin profile activation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs ORDER BY id")
	if err != nil {
		return ActivationStats{}, fmt.Errorf("list activation jobs: %w", err)
	}
	jobs := make([]Job, 0)
	for rows.Next() {
		job, scanErr := scanJobRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return ActivationStats{}, fmt.Errorf("scan activation job: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Close(); err != nil {
		return ActivationStats{}, err
	}
	stats := ActivationStats{}
	now := s.timestamp()
	for _, job := range jobs {
		if job.ProfileRevision != nil && *job.ProfileRevision == revision {
			stats.Unchanged++
			continue
		}
		if protectedProfileState(job.ProcessState) {
			stats.Protected++
			continue
		}
		from := job.ProcessState
		var to string
		var filterHits any
		if job.Description == nil {
			hits := screen(job)
			encoded, encodeErr := encodeFilterHits(hits)
			if encodeErr != nil {
				return ActivationStats{}, encodeErr
			}
			filterHits = encoded
			if len(hits) > 0 {
				to = "filtered_out"
			} else {
				to = "discovered"
			}
			stats.PartialScreened++
		} else {
			to = "new"
			filterHits = nil
			stats.Requeued++
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, filter_hits=?, profile_revision=?, updated_at=? WHERE id=?", to, filterHits, revision, now, job.ID); err != nil {
			return ActivationStats{}, fmt.Errorf("activate job %d: %w", job.ID, err)
		}
		if from != to {
			if err := insertEvent(ctx, tx, job.ID, "process", from, to, "profile revision activated", now); err != nil {
				return ActivationStats{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ActivationStats{}, fmt.Errorf("commit profile activation: %w", err)
	}
	return stats, nil
}

// CommitFilter atomically persists hits and transitions a job only when the
// work still belongs to its expected Profile revision.
func (s *Store) CommitFilter(ctx context.Context, jobID int64, revision string, hits []string) error {
	encoded, err := encodeFilterHits(hits)
	if err != nil {
		return err
	}
	to := "queued"
	if len(hits) > 0 {
		to = "filtered_out"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.timestamp()
	result, err := tx.ExecContext(ctx, "UPDATE jobs SET filter_hits=?, process_state=?, updated_at=? WHERE id=? AND process_state='new' AND profile_revision=?", encoded, to, now, jobID, revision)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrStaleRevision
	}
	if err := insertEvent(ctx, tx, jobID, "process", "new", to, "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitScore atomically appends a score and completes the queued state using
// expected-state and expected-revision compare-and-set semantics.
func (s *Store) CommitScore(ctx context.Context, input ScoreInput, to string) error {
	if err := validateScore(input); err != nil {
		return err
	}
	if input.ProfileRevision == "" || (to != "scored" && to != "shortlisted") {
		return errors.New("store: invalid revision-aware score")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE id=? AND process_state='queued' AND profile_revision=?", input.JobID, input.ProfileRevision).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrStaleRevision
	}
	now := s.timestamp()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scores (job_id, dim_hard_skill, dim_domain, dim_seniority, dim_condition, dim_direction, total, reason, runner, profile_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.HardSkill, input.Domain, input.Seniority, input.Condition, input.Direction, input.Total, input.Reason, input.Runner, input.ProfileRevision, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, updated_at=? WHERE id=?", to, now, input.JobID); err != nil {
		return err
	}
	if err := insertEvent(ctx, tx, input.JobID, "process", "queued", to, "", now); err != nil {
		return err
	}
	return tx.Commit()
}

func protectedProfileState(state string) bool {
	return state == "letter_requested" || state == "letter_ready" || state == "letter_failed"
}

func encodeFilterHits(hits []string) (any, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(hits)
	if err != nil {
		return nil, fmt.Errorf("encode filter hits: %w", err)
	}
	return string(encoded), nil
}
