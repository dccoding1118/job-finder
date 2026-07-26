package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// StateMerged is the terminal state of an alias: a job the user's other source
// already carries. It is picked up by no stage, exported with no verdict, and
// listed nowhere, which is what keeps a duplicate from costing a second score.
const StateMerged = "merged"

var (
	// ErrCandidateDecided reports a duplicate candidate the user already ruled on.
	ErrCandidateDecided = errors.New("store: duplicate candidate was already decided")
	// ErrNotMerged reports an unmerge request for a job that is not an alias.
	ErrNotMerged = errors.New("store: job is not merged")
)

// candidate reasons: why a pair was left for the user instead of merged.
const (
	ReasonTitleSimilar     = "title_similar"
	ReasonLocationMismatch = "location_mismatch"
	ReasonHasOutput        = "has_output"
)

// defaultSourcePriority breaks a canonical tie when both sides carry the same
// amount of JD. 104 first: its detail page is the richest of the three.
var defaultSourcePriority = []string{"104", "cake", "yourator"}

// DedupeOptions carries the configured thresholds. They live in config rather
// than here so the normalization rules can be retuned without a migration.
type DedupeOptions struct {
	// TitleSimilarityThreshold is the lower bound for registering a grey-zone
	// candidate; below it two titles are simply different jobs.
	TitleSimilarityThreshold float64
	// SourcePriority orders the sources a canonical job is preferred from.
	SourcePriority []string
}

func (o DedupeOptions) threshold() float64 {
	if o.TitleSimilarityThreshold <= 0 {
		return 0.6
	}
	return o.TitleSimilarityThreshold
}

func (o DedupeOptions) priority() []string {
	if len(o.SourcePriority) == 0 {
		return defaultSourcePriority
	}
	return o.SourcePriority
}

// DedupeOutcome is what one grouping pass decided. CanonicalJobID is the job any
// caller must report and act on: an alias never carries its own verdict.
type DedupeOutcome struct {
	GroupID        int64
	CanonicalJobID int64
	// Merged reports that this pass folded the job into another group.
	Merged bool
	// CandidateIDs are the grey-zone pairs left for the user to rule on.
	CandidateIDs []int64
}

// GroupMember is one source's copy of the same job.
type GroupMember struct {
	JobID      int64  `json:"job_id"`
	Source     string `json:"source"`
	URL        string `json:"url"`
	ExternalID string `json:"external_id"`
	Merged     bool   `json:"merged"`
}

// JobGroup is the cross-source identity of one job: the canonical copy that
// carries the processing plus every other source the same listing appeared on.
type JobGroup struct {
	GroupID                 int64         `json:"group_id"`
	CanonicalJobID          int64         `json:"canonical_job_id"`
	Members                 []GroupMember `json:"members"`
	DuplicateCandidateCount int           `json:"duplicate_candidate_count"`
}

// DuplicateCandidate is one suspected pair awaiting the user's decision.
type DuplicateCandidate struct {
	ID         int64            `json:"id"`
	Similarity float64          `json:"similarity"`
	Reason     string           `json:"reason"`
	CreatedAt  string           `json:"created_at"`
	A          CandidateJobView `json:"a"`
	B          CandidateJobView `json:"b"`
}

// CandidateJobView is the side-by-side comparison one candidate is decided from.
type CandidateJobView struct {
	GroupID     int64  `json:"group_id"`
	JobID       int64  `json:"job_id"`
	Title       string `json:"title"`
	CompanyName string `json:"company_name"`
	Location    string `json:"location"`
	Source      string `json:"source"`
	URL         string `json:"url"`
}

// mergeNotePattern reads back the note a merge wrote, which is the only record
// an unmerge restores the alias from.
var mergeNotePattern = regexp.MustCompile(`^merged into job (\d+) from ([a-z_]+)$`)

func mergeNote(canonicalJobID int64, from string) string {
	return fmt.Sprintf("merged into job %d from %s", canonicalJobID, from)
}

// dedupeRow is one group's canonical job as the comparison reads it.
type dedupeRow struct {
	groupID     int64
	jobID       int64
	source      string
	title       string
	companyName string
	location    string
	remoteType  string
	description *string
	firstSeenAt string
	key         DedupeKey
	hasOutput   bool
}

// LinkOrSuggestDuplicate places one freshly upserted job in the cross-source
// group it belongs to. A confident match on company, title, and location merges
// in one transaction; anything less certain is registered as a candidate for the
// user, because an automatic merge must never swallow work already done.
func (s *Store) LinkOrSuggestDuplicate(ctx context.Context, jobID int64, options DedupeOptions) (DedupeOutcome, error) {
	if jobID <= 0 {
		return DedupeOutcome{}, fmt.Errorf("store: invalid job id %d", jobID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DedupeOutcome{}, fmt.Errorf("begin dedupe: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	self, err := s.ensureGroupTx(ctx, tx, jobID)
	if err != nil {
		return DedupeOutcome{}, err
	}
	outcome := DedupeOutcome{GroupID: self.groupID, CanonicalJobID: self.jobID}
	// An alias reached here because its own source listed it again; the group it
	// was folded into stays authoritative and nothing is recomputed.
	if self.jobID != jobID {
		if commitErr := tx.Commit(); commitErr != nil {
			return DedupeOutcome{}, fmt.Errorf("commit dedupe: %w", commitErr)
		}
		return outcome, nil
	}
	if _, keyErr := tx.ExecContext(ctx, "UPDATE job_groups SET dedupe_key=?, updated_at=? WHERE id=?", nullableString(self.key.Hash()), s.timestamp(), self.groupID); keyErr != nil {
		return DedupeOutcome{}, fmt.Errorf("store dedupe key: %w", keyErr)
	}

	others, err := s.candidateRowsTx(ctx, tx, self)
	if err != nil {
		return DedupeOutcome{}, err
	}
	for _, other := range others {
		similarity := self.key.Similarity(other.key)
		switch {
		case self.key.Title == other.key.Title && self.key.LocationCompatible(other.key):
			if self.hasOutput || other.hasOutput {
				id, regErr := s.registerCandidateTx(ctx, tx, self.groupID, other.groupID, similarity, ReasonHasOutput)
				if regErr != nil {
					return DedupeOutcome{}, regErr
				}
				outcome.CandidateIDs = append(outcome.CandidateIDs, id)
				continue
			}
			merged, mergeErr := s.mergeGroupsTx(ctx, tx, self.groupID, other.groupID, options)
			if mergeErr != nil {
				return DedupeOutcome{}, mergeErr
			}
			outcome.GroupID, outcome.CanonicalJobID, outcome.Merged = merged.groupID, merged.jobID, true
			// The merged group is the one every later comparison must use.
			self = merged
		case self.key.Title == other.key.Title:
			id, regErr := s.registerCandidateTx(ctx, tx, self.groupID, other.groupID, similarity, ReasonLocationMismatch)
			if regErr != nil {
				return DedupeOutcome{}, regErr
			}
			outcome.CandidateIDs = append(outcome.CandidateIDs, id)
		case similarity >= options.threshold():
			id, regErr := s.registerCandidateTx(ctx, tx, self.groupID, other.groupID, similarity, ReasonTitleSimilar)
			if regErr != nil {
				return DedupeOutcome{}, regErr
			}
			outcome.CandidateIDs = append(outcome.CandidateIDs, id)
		}
	}
	if err := tx.Commit(); err != nil {
		return DedupeOutcome{}, fmt.Errorf("commit dedupe: %w", err)
	}
	return outcome, nil
}

// MergeCandidate applies the user's decision that a suspected pair is one job.
func (s *Store) MergeCandidate(ctx context.Context, candidateID int64, options DedupeOptions) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin candidate merge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	groupA, groupB, err := s.pendingCandidateTx(ctx, tx, candidateID)
	if err != nil {
		return 0, err
	}
	merged, err := s.mergeGroupsTx(ctx, tx, groupA, groupB, options)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit candidate merge: %w", err)
	}
	return merged.jobID, nil
}

// IgnoreCandidate records that the user judged a suspected pair to be different
// jobs; the pair is never suggested again.
func (s *Store) IgnoreCandidate(ctx context.Context, candidateID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin candidate ignore: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, _, err := s.pendingCandidateTx(ctx, tx, candidateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE job_dupe_candidates SET state='ignored' WHERE id=?", candidateID); err != nil {
		return fmt.Errorf("ignore candidate: %w", err)
	}
	return tx.Commit()
}

// UnmergeJob restores one alias to the state and the standalone group it had
// before the merge. Scores and letters are never deleted: a wrong merge costs
// the user a click, not their history.
func (s *Store) UnmergeJob(ctx context.Context, jobID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unmerge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	job, found, err := findJobIDTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	if job.ProcessState != StateMerged {
		return ErrNotMerged
	}
	var note sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT note FROM status_events WHERE job_id=? AND axis='process' AND to_state=? ORDER BY created_at DESC, id DESC LIMIT 1", jobID, StateMerged).Scan(&note)
	if err != nil {
		return fmt.Errorf("read merge event of job %d: %w", jobID, err)
	}
	match := mergeNotePattern.FindStringSubmatch(note.String)
	if len(match) != 3 {
		return fmt.Errorf("store: job %d has no readable merge record", jobID)
	}
	restored := match[2]
	now := s.timestamp()
	key := NewDedupeKey(job.CompanyName, job.Title, job.Location, job.RemoteType)
	result, err := tx.ExecContext(ctx, "INSERT INTO job_groups (canonical_job_id, dedupe_key, created_at, updated_at) VALUES (?, ?, ?, ?)", jobID, nullableString(key.Hash()), now, now)
	if err != nil {
		return fmt.Errorf("create group for unmerged job %d: %w", jobID, err)
	}
	groupID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get unmerged group id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET group_id=?, process_state=?, updated_at=? WHERE id=?", groupID, restored, now, jobID); err != nil {
		return fmt.Errorf("restore unmerged job %d: %w", jobID, err)
	}
	if err := insertEvent(ctx, tx, jobID, "process", StateMerged, restored, "unmerged by user", now); err != nil {
		return err
	}
	return tx.Commit()
}

// GroupOf reports the cross-source group of one job, which is what lets the user
// pick the platform to apply on. A single-member group is the normal case.
func (s *Store) GroupOf(ctx context.Context, jobID int64) (JobGroup, error) {
	var groupID, canonicalJobID int64
	err := s.db.QueryRowContext(ctx, "SELECT g.id, g.canonical_job_id FROM job_groups g JOIN jobs j ON j.group_id=g.id WHERE j.id=?", jobID).Scan(&groupID, &canonicalJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return JobGroup{}, nil
	}
	if err != nil {
		return JobGroup{}, fmt.Errorf("read job group: %w", err)
	}
	group := JobGroup{GroupID: groupID, CanonicalJobID: canonicalJobID, Members: []GroupMember{}}
	rows, err := s.db.QueryContext(ctx, "SELECT id, source, url, external_id, process_state FROM jobs WHERE group_id=? ORDER BY id", groupID)
	if err != nil {
		return JobGroup{}, fmt.Errorf("list group members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member GroupMember
		var state string
		if scanErr := rows.Scan(&member.JobID, &member.Source, &member.URL, &member.ExternalID, &state); scanErr != nil {
			return JobGroup{}, fmt.Errorf("scan group member: %w", scanErr)
		}
		member.Merged = state == StateMerged
		group.Members = append(group.Members, member)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return JobGroup{}, fmt.Errorf("iterate group members: %w", iterErr)
	}
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_dupe_candidates WHERE state='pending' AND (group_a_id=? OR group_b_id=?)", groupID, groupID).Scan(&group.DuplicateCandidateCount)
	if err != nil {
		return JobGroup{}, fmt.Errorf("count group candidates: %w", err)
	}
	return group, nil
}

// ListDuplicateCandidates returns the pairs still awaiting a decision, newest
// first, with both sides as the user compares them.
func (s *Store) ListDuplicateCandidates(ctx context.Context) ([]DuplicateCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.group_a_id, c.group_b_id, c.similarity, c.reason, c.created_at,
			a.canonical_job_id, ja.title, ja.company_name, ja.location, ja.source, ja.url,
			b.canonical_job_id, jb.title, jb.company_name, jb.location, jb.source, jb.url
		FROM job_dupe_candidates c
		JOIN job_groups a ON a.id=c.group_a_id
		JOIN job_groups b ON b.id=c.group_b_id
		JOIN jobs ja ON ja.id=a.canonical_job_id AND ja.group_id=a.id
		JOIN jobs jb ON jb.id=b.canonical_job_id AND jb.group_id=b.id
		WHERE c.state='pending'
		ORDER BY c.created_at DESC, c.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list duplicate candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	candidates := []DuplicateCandidate{}
	for rows.Next() {
		var c DuplicateCandidate
		if err := rows.Scan(&c.ID, &c.A.GroupID, &c.B.GroupID, &c.Similarity, &c.Reason, &c.CreatedAt,
			&c.A.JobID, &c.A.Title, &c.A.CompanyName, &c.A.Location, &c.A.Source, &c.A.URL,
			&c.B.JobID, &c.B.Title, &c.B.CompanyName, &c.B.Location, &c.B.Source, &c.B.URL); err != nil {
			return nil, fmt.Errorf("scan duplicate candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duplicate candidates: %w", err)
	}
	return candidates, nil
}

// ensureGroupTx returns the group row of a job, creating the single-member group
// every job starts in when it has none yet.
func (s *Store) ensureGroupTx(ctx context.Context, tx *sql.Tx, jobID int64) (dedupeRow, error) {
	var groupID sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT group_id FROM jobs WHERE id=?", jobID).Scan(&groupID); err != nil {
		return dedupeRow{}, fmt.Errorf("read group of job %d: %w", jobID, err)
	}
	// A job stored before this schema version has no group yet; it gets the
	// single-member one every job is created with today.
	if !groupID.Valid {
		job, found, err := findJobIDTx(ctx, tx, jobID)
		if err != nil {
			return dedupeRow{}, err
		}
		if !found {
			return dedupeRow{}, sql.ErrNoRows
		}
		key := NewDedupeKey(job.CompanyName, job.Title, job.Location, job.RemoteType)
		if err := s.createGroupTx(ctx, tx, jobID, key, s.timestamp()); err != nil {
			return dedupeRow{}, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT group_id FROM jobs WHERE id=?", jobID).Scan(&groupID); err != nil {
			return dedupeRow{}, fmt.Errorf("read group of job %d: %w", jobID, err)
		}
	}
	return s.canonicalRowTx(ctx, tx, groupID.Int64)
}

// createGroupTx gives one job the single-member group it belongs to until another
// source turns out to carry the same listing.
func (s *Store) createGroupTx(ctx context.Context, tx *sql.Tx, jobID int64, key DedupeKey, now string) error {
	result, err := tx.ExecContext(ctx, "INSERT INTO job_groups (canonical_job_id, dedupe_key, created_at, updated_at) VALUES (?, ?, ?, ?)", jobID, nullableString(key.Hash()), now, now)
	if err != nil {
		return fmt.Errorf("create group for job %d: %w", jobID, err)
	}
	groupID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get created group id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE jobs SET group_id=? WHERE id=?", groupID, jobID); err != nil {
		return fmt.Errorf("attach job %d to its group: %w", jobID, err)
	}
	return nil
}

func (s *Store) canonicalRowTx(ctx context.Context, tx *sql.Tx, groupID int64) (dedupeRow, error) {
	row := tx.QueryRowContext(ctx, `SELECT g.id, j.id, j.source, j.title, j.company_name, j.location, j.remote_type, j.description, j.first_seen_at
		FROM job_groups g JOIN jobs j ON j.id=g.canonical_job_id WHERE g.id=?`, groupID)
	return s.scanDedupeRow(ctx, tx, row)
}

func (s *Store) scanDedupeRow(ctx context.Context, tx *sql.Tx, row rowScanner) (dedupeRow, error) {
	var out dedupeRow
	if err := row.Scan(&out.groupID, &out.jobID, &out.source, &out.title, &out.companyName, &out.location, &out.remoteType, &out.description, &out.firstSeenAt); err != nil {
		return dedupeRow{}, fmt.Errorf("read group canonical job: %w", err)
	}
	out.key = NewDedupeKey(out.companyName, out.title, out.location, out.remoteType)
	hasOutput, err := hasOutputTx(ctx, tx, out.jobID)
	if err != nil {
		return dedupeRow{}, err
	}
	out.hasOutput = hasOutput
	return out, nil
}

// candidateRowsTx narrows the comparison to the canonical jobs of other groups
// whose company normalizes to the same name: different companies are never
// compared, so the blocking key is the company.
//
// Groups that already list the job on a source this group covers are excluded.
// Dedupe exists to recognize one listing published on two platforms; two entries
// on the same platform are two distinct openings, because a platform does not
// publish the same job twice. Comparing them can only produce a decision the
// user has to reject.
func (s *Store) candidateRowsTx(ctx context.Context, tx *sql.Tx, self dedupeRow) ([]dedupeRow, error) {
	if self.key.Company == "" || self.key.Title == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id, j.id, j.source, j.title, j.company_name, j.location, j.remote_type, j.description, j.first_seen_at
		FROM job_groups g JOIN jobs j ON j.id=g.canonical_job_id
		WHERE g.id <> ? AND j.process_state <> ? AND j.group_id = g.id
		  AND NOT EXISTS (
			SELECT 1 FROM jobs other JOIN jobs mine ON mine.group_id = ?
			WHERE other.group_id = g.id AND other.source = mine.source
		)
		ORDER BY g.id`, self.groupID, StateMerged, self.groupID)
	if err != nil {
		return nil, fmt.Errorf("list dedupe candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	matches := []dedupeRow{}
	for rows.Next() {
		var out dedupeRow
		if err := rows.Scan(&out.groupID, &out.jobID, &out.source, &out.title, &out.companyName, &out.location, &out.remoteType, &out.description, &out.firstSeenAt); err != nil {
			return nil, fmt.Errorf("scan dedupe candidate: %w", err)
		}
		out.key = NewDedupeKey(out.companyName, out.title, out.location, out.remoteType)
		if out.key.Company != self.key.Company || out.key.Title == "" {
			continue
		}
		matches = append(matches, out)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dedupe candidates: %w", err)
	}
	for i := range matches {
		hasOutput, err := hasOutputTx(ctx, tx, matches[i].jobID)
		if err != nil {
			return nil, err
		}
		matches[i].hasOutput = hasOutput
	}
	return matches, nil
}

// hasOutputTx reports whether a job already carries work worth protecting: a
// score, a letter, or an application record.
func hasOutputTx(ctx context.Context, tx *sql.Tx, jobID int64) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM scores WHERE job_id=?) +
			(SELECT COUNT(*) FROM letters WHERE job_id=?) +
			(SELECT COUNT(*) FROM jobs WHERE id=? AND apply_state IS NOT NULL)`, jobID, jobID, jobID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read outputs of job %d: %w", jobID, err)
	}
	return count > 0, nil
}

// registerCandidateTx records a suspected pair once. The pair is keyed by the
// two group ids, so repeated passes never queue the same decision twice.
func (s *Store) registerCandidateTx(ctx context.Context, tx *sql.Tx, groupA, groupB int64, similarity float64, reason string) (int64, error) {
	if groupA > groupB {
		groupA, groupB = groupB, groupA
	}
	var id int64
	var state string
	err := tx.QueryRowContext(ctx, "SELECT id, state FROM job_dupe_candidates WHERE group_a_id=? AND group_b_id=?", groupA, groupB).Scan(&id, &state)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read duplicate candidate: %w", err)
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO job_dupe_candidates (group_a_id, group_b_id, similarity, reason, state, created_at) VALUES (?, ?, ?, ?, 'pending', ?)", groupA, groupB, similarity, reason, s.timestamp())
	if err != nil {
		return 0, fmt.Errorf("register duplicate candidate: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get duplicate candidate id: %w", err)
	}
	return id, nil
}

func (s *Store) pendingCandidateTx(ctx context.Context, tx *sql.Tx, candidateID int64) (int64, int64, error) {
	var groupA, groupB int64
	var state string
	err := tx.QueryRowContext(ctx, "SELECT group_a_id, group_b_id, state FROM job_dupe_candidates WHERE id=?", candidateID).Scan(&groupA, &groupB, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, sql.ErrNoRows
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read duplicate candidate %d: %w", candidateID, err)
	}
	if state != "pending" {
		return 0, 0, ErrCandidateDecided
	}
	return groupA, groupB, nil
}

// mergeGroupsTx folds two groups into one in a single transaction: the canonical
// job is chosen, every other member becomes an alias in `merged`, and the pair's
// candidate rows are closed. Existing scores and letters are left untouched so
// the audit trail of an alias survives its merge.
func (s *Store) mergeGroupsTx(ctx context.Context, tx *sql.Tx, groupA, groupB int64, options DedupeOptions) (dedupeRow, error) {
	if groupA == groupB {
		return s.canonicalRowTx(ctx, tx, groupA)
	}
	surviving, absorbed := groupA, groupB
	if absorbed < surviving {
		surviving, absorbed = absorbed, surviving
	}
	members, err := groupMembersTx(ctx, tx, surviving, absorbed)
	if err != nil {
		return dedupeRow{}, err
	}
	if len(members) == 0 {
		return dedupeRow{}, fmt.Errorf("store: groups %d and %d have no members", surviving, absorbed)
	}
	canonical := chooseCanonical(members, options.priority())
	now := s.timestamp()
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET group_id=?, updated_at=? WHERE id=?", surviving, now, member.jobID); err != nil {
			return dedupeRow{}, fmt.Errorf("move job %d into group %d: %w", member.jobID, surviving, err)
		}
		if member.jobID == canonical.jobID {
			if member.processState == StateMerged {
				return dedupeRow{}, fmt.Errorf("store: canonical job %d is an alias", member.jobID)
			}
			continue
		}
		if member.processState == StateMerged {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, updated_at=? WHERE id=?", StateMerged, now, member.jobID); err != nil {
			return dedupeRow{}, fmt.Errorf("mark job %d merged: %w", member.jobID, err)
		}
		if err := insertEvent(ctx, tx, member.jobID, "process", member.processState, StateMerged, mergeNote(canonical.jobID, member.processState), now); err != nil {
			return dedupeRow{}, err
		}
	}
	key := NewDedupeKey(canonical.companyName, canonical.title, canonical.location, canonical.remoteType)
	if _, err := tx.ExecContext(ctx, "UPDATE job_groups SET canonical_job_id=?, dedupe_key=?, updated_at=? WHERE id=?", canonical.jobID, nullableString(key.Hash()), now, surviving); err != nil {
		return dedupeRow{}, fmt.Errorf("update surviving group %d: %w", surviving, err)
	}
	if err := closeCandidatesTx(ctx, tx, surviving, absorbed); err != nil {
		return dedupeRow{}, err
	}
	// The absorbed group row stays: the candidate rows the user's decision closed
	// still reference it, and that record is the audit trail of the merge. It owns
	// no members any more, and every enumeration of groups requires a group to own
	// its canonical job, so an emptied group takes part in nothing.
	return s.canonicalRowTx(ctx, tx, surviving)
}

// memberRow is one job of a group during a merge.
type memberRow struct {
	jobID        int64
	source       string
	title        string
	companyName  string
	location     string
	remoteType   string
	processState string
	description  *string
	firstSeenAt  string
}

func groupMembersTx(ctx context.Context, tx *sql.Tx, groups ...int64) ([]memberRow, error) {
	placeholders := strings.Repeat(", ?", len(groups)-1)
	args := make([]any, 0, len(groups))
	for _, group := range groups {
		args = append(args, group)
	}
	// #nosec G202 -- only bind-parameter placeholders are concatenated.
	rows, err := tx.QueryContext(ctx, "SELECT id, source, title, company_name, location, remote_type, process_state, description, first_seen_at FROM jobs WHERE group_id IN (?"+placeholders+") ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	members := []memberRow{}
	for rows.Next() {
		var member memberRow
		if err := rows.Scan(&member.jobID, &member.source, &member.title, &member.companyName, &member.location, &member.remoteType, &member.processState, &member.description, &member.firstSeenAt); err != nil {
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

// chooseCanonical picks the copy that carries the most usable job: a full JD
// first, then the longer one, then the richest source, then the one seen first.
func chooseCanonical(members []memberRow, priority []string) memberRow {
	rank := map[string]int{}
	for i, source := range priority {
		rank[source] = i
	}
	sourceRank := func(source string) int {
		if value, ok := rank[source]; ok {
			return value
		}
		return len(priority)
	}
	length := func(member memberRow) int {
		if member.description == nil {
			return -1
		}
		return len(*member.description)
	}
	sorted := append([]memberRow(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		// An alias only becomes canonical again if nothing else can: a job the
		// user unmerged is preferred over one still marked merged.
		if (left.processState == StateMerged) != (right.processState == StateMerged) {
			return right.processState == StateMerged
		}
		if (length(left) >= 0) != (length(right) >= 0) {
			return length(left) >= 0
		}
		if length(left) != length(right) {
			return length(left) > length(right)
		}
		if sourceRank(left.source) != sourceRank(right.source) {
			return sourceRank(left.source) < sourceRank(right.source)
		}
		if left.firstSeenAt != right.firstSeenAt {
			return left.firstSeenAt < right.firstSeenAt
		}
		return left.jobID < right.jobID
	})
	return sorted[0]
}

// closeCandidatesTx marks the decided pair merged and re-points every other
// candidate of the absorbed group at the surviving one, dropping the rows that
// would become self-comparisons or duplicates.
func closeCandidatesTx(ctx context.Context, tx *sql.Tx, surviving, absorbed int64) error {
	// The pair the merge settled is recorded as decided, whoever decided it.
	if _, err := tx.ExecContext(ctx, "UPDATE job_dupe_candidates SET state='merged' WHERE (group_a_id=? AND group_b_id=?) OR (group_a_id=? AND group_b_id=?)", surviving, absorbed, absorbed, surviving); err != nil {
		return fmt.Errorf("close decided candidate: %w", err)
	}
	// Its remaining pending pairs move to the surviving group; a pair that already
	// exists there is dropped rather than duplicated.
	rows, err := tx.QueryContext(ctx, "SELECT id, group_a_id, group_b_id FROM job_dupe_candidates WHERE state='pending' AND (group_a_id=? OR group_b_id=?)", absorbed, absorbed)
	if err != nil {
		return fmt.Errorf("list absorbed candidates: %w", err)
	}
	type pending struct{ id, a, b int64 }
	remaining := []pending{}
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.a, &row.b); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan absorbed candidate: %w", err)
		}
		remaining = append(remaining, row)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close absorbed candidates: %w", err)
	}
	for _, row := range remaining {
		a, b := row.a, row.b
		if a == absorbed {
			a = surviving
		}
		if b == absorbed {
			b = surviving
		}
		if a > b {
			a, b = b, a
		}
		if a == b {
			if _, err := tx.ExecContext(ctx, "DELETE FROM job_dupe_candidates WHERE id=?", row.id); err != nil {
				return fmt.Errorf("drop self-referencing candidate: %w", err)
			}
			continue
		}
		var exists int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM job_dupe_candidates WHERE group_a_id=? AND group_b_id=? AND id<>?", a, b, row.id).Scan(&exists)
		if err == nil {
			if _, deleteErr := tx.ExecContext(ctx, "DELETE FROM job_dupe_candidates WHERE id=?", row.id); deleteErr != nil {
				return fmt.Errorf("drop duplicated candidate: %w", deleteErr)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read candidate conflict: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE job_dupe_candidates SET group_a_id=?, group_b_id=? WHERE id=?", a, b, row.id); err != nil {
			return fmt.Errorf("repoint candidate: %w", err)
		}
	}
	return nil
}
