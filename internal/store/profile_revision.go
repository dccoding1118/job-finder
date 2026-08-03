package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrStaleRevision = errors.New("store: stale profile revision")

// Revisions is one Profile's pair of gate revisions. They move independently:
// changing a soft rule must not send screened-out jobs back through screening.
type Revisions struct {
	Filter string
	Score  string
}

type ActivationStats struct {
	PartialScreened int
	Refiltered      int
	Requeued        int
	Protected       int
	Unchanged       int
}

// FilterCondition is one JD condition as the screening gate decided it. A bonus
// condition carries no weight here and is kept only for the scoring gate to
// reuse, so the JD is broken down once rather than once per gate.
type FilterCondition struct {
	Text         string   `json:"text"`
	Kind         string   `json:"kind"`
	Group        int      `json:"group"`
	Category     string   `json:"category"`
	Verdict      string   `json:"verdict"`
	YearsMin     *float64 `json:"years_required,omitempty"`
	YearsMax     *float64 `json:"years_max,omitempty"`
	IndustryKeys []string `json:"industry_keys,omitempty"`
}

// FilterResult is one screening pass over one job.
type FilterResult struct {
	Outcome    string            `json:"outcome"`
	Conditions []FilterCondition `json:"conditions"`
	Stage      string            `json:"stage"`
	Runner     *string           `json:"runner"`
	Revision   *string           `json:"filter_revision"`
	// Partial records that the job carried only a list excerpt when it was
	// screened, which is what decides where an undecided outcome sends it.
	Partial bool `json:"partial"`
}

const (
	FilterPass    = "pass"
	FilterFail    = "fail"
	FilterUnknown = "unknown"
)

// stateForOutcome is the split screening produces. Missing information never
// becomes unfit: an excerpt that is still undecided goes back to the 待看 list
// to have its full text fetched, which is the one thing that can still resolve
// it. A complete JD is never left undecided — the aggregate turns its remaining
// unknowns into a pass — so that combination is a defect, not a state.
func stateForOutcome(outcome string, partial bool) (string, error) {
	switch {
	case outcome == FilterFail:
		return "filtered_out", nil
	case outcome == FilterUnknown && partial:
		return "discovered", nil
	case outcome == FilterPass:
		return "queued", nil
	case outcome == FilterUnknown:
		return "", errors.New("store: screening left a complete JD undecided")
	default:
		return "", fmt.Errorf("store: invalid filter outcome %q", outcome)
	}
}

// SummarizeConditions reports the outcome a set of conditions adds up to. Only
// required conditions count; a bonus condition never decides fitness.
func SummarizeConditions(conditions []FilterCondition) string {
	groups := map[int]string{}
	for _, condition := range conditions {
		if condition.Kind != "required" {
			continue
		}
		// A disjunctive group ("A or B") is satisfied by any one member, so the best
		// verdict in the group is the group's verdict.
		if best, seen := groups[condition.Group]; !seen || betterVerdict(condition.Verdict, best) {
			groups[condition.Group] = condition.Verdict
		}
	}
	outcome := FilterPass
	for _, verdict := range groups {
		switch verdict {
		case FilterFail:
			return FilterFail
		case FilterUnknown:
			outcome = FilterUnknown
		}
	}
	return outcome
}

func betterVerdict(candidate, current string) bool {
	rank := map[string]int{FilterFail: 0, FilterUnknown: 1, FilterPass: 2}
	return rank[candidate] > rank[current]
}

// EstimateActivation reports the work a different revision pair would schedule.
func (s *Store) EstimateActivation(ctx context.Context, revisions Revisions) (ActivationStats, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT process_state, description, filter_revision, score_revision FROM jobs")
	if err != nil {
		return ActivationStats{}, fmt.Errorf("estimate profile activation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	stats := ActivationStats{}
	for rows.Next() {
		var state string
		var description, filterRevision, scoreRevision sql.NullString
		if err := rows.Scan(&state, &description, &filterRevision, &scoreRevision); err != nil {
			return ActivationStats{}, err
		}
		job := Job{ProcessState: state}
		if description.Valid {
			value := description.String
			job.Description = &value
		}
		if filterRevision.Valid {
			value := filterRevision.String
			job.FilterRevision = &value
		}
		if scoreRevision.Valid {
			value := scoreRevision.String
			job.ScoreRevision = &value
		}
		countActivation(&stats, job, revisions)
	}
	return stats, rows.Err()
}

// activationPlan is what one job does under a new revision pair.
type activationPlan struct {
	kind string // "unchanged", "protected", "partial", "refilter", "requeue"
}

// planActivation decides a job's fate from which revision actually changed.
// A screening change invalidates both verdicts; a scoring-only change leaves
// the screening result — and everything screening rejected — untouched.
func planActivation(job Job, revisions Revisions) activationPlan {
	filterCurrent := job.FilterRevision != nil && *job.FilterRevision == revisions.Filter
	scoreCurrent := job.ScoreRevision != nil && *job.ScoreRevision == revisions.Score
	if protectedProfileState(job.ProcessState) {
		if filterCurrent && (scoreCurrent || job.ScoreRevision == nil) {
			return activationPlan{"unchanged"}
		}
		return activationPlan{"protected"}
	}
	if !filterCurrent {
		if job.Description == nil {
			return activationPlan{"partial"}
		}
		return activationPlan{"refilter"}
	}
	// Screening is current, so only the score can be out of date — and only for a
	// job that got as far as being scoreable.
	if !scoreCurrent && (job.ProcessState == "queued" || job.ProcessState == "scored" || job.ProcessState == "shortlisted") {
		return activationPlan{"requeue"}
	}
	return activationPlan{"unchanged"}
}

func countActivation(stats *ActivationStats, job Job, revisions Revisions) {
	switch planActivation(job, revisions).kind {
	case "protected":
		stats.Protected++
	case "partial":
		stats.PartialScreened++
	case "refilter":
		stats.Refiltered++
	case "requeue":
		stats.Requeued++
	default:
		stats.Unchanged++
	}
}

// ActivateProfile applies the revision state matrix in one SQLite transaction.
// screen is pure and must only inspect the supplied Job.
func (s *Store) ActivateProfile(ctx context.Context, revisions Revisions, screen func(Job) []string) (ActivationStats, error) {
	if revisions.Filter == "" || revisions.Score == "" || screen == nil {
		return ActivationStats{}, errors.New("store: both revisions and a screen function are required")
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
		plan := planActivation(job, revisions)
		switch plan.kind {
		case "unchanged":
			stats.Unchanged++
			continue
		case "protected":
			stats.Protected++
			continue
		}
		from, to := job.ProcessState, "new"
		var filterHits any
		scoreRevision := any(nil)
		switch plan.kind {
		case "partial":
			hits := screen(job)
			encoded, encodeErr := encodeFilterHits(hits)
			if encodeErr != nil {
				return ActivationStats{}, encodeErr
			}
			filterHits, to = encoded, "discovered"
			if len(hits) > 0 {
				to = "filtered_out"
			}
			stats.PartialScreened++
		case "refilter":
			stats.Refiltered++
		case "requeue":
			// The screening result stands; only the score is redone, so the job goes
			// back no further than the score stage.
			to, scoreRevision = "queued", revisions.Score
			filterHits = nullableHitsValue(job.FilterHits)
			stats.Requeued++
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET process_state=?, filter_hits=?, filter_revision=?, score_revision=?, updated_at=? WHERE id=?", to, filterHits, revisions.Filter, scoreRevision, now, job.ID); err != nil {
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

// AdoptStageRevision stamps one job with the active revision of the stage that
// is about to run it. It applies only to the states that hold no assessment of
// their own — `new` before screening, `queued` before scoring — where a Profile
// change invalidates nothing and costs nothing: the call the job is already
// waiting for simply runs under the current Profile. Everything that already
// carries a verdict keeps waiting for the user's reprocess, which is where the
// re-spend is decided.
//
// It reports false when the job left that state in the meantime, in which case
// the caller must not run the stage: the stage's own CAS would discard it.
func (s *Store) AdoptStageRevision(ctx context.Context, stage string, jobID int64, revisions Revisions) (bool, error) {
	var query string
	var args []any
	switch stage {
	case "filter":
		// Screening decides the score gate's revision when it queues the job, so an
		// adopted screening revision leaves the score one to be set by the result.
		query = "UPDATE jobs SET filter_revision=?, score_revision=NULL WHERE id=? AND process_state='new'"
		args = []any{revisions.Filter, jobID}
	case "score":
		// A superseded screening is not the score stage's to adopt: the job needs a
		// new screening first, which is a call only the user asks for.
		query = "UPDATE jobs SET score_revision=? WHERE id=? AND process_state='queued' AND filter_revision=?"
		args = []any{revisions.Score, jobID, revisions.Filter}
	default:
		return false, fmt.Errorf("store: stage %q adopts no revision", stage)
	}
	// `updated_at` is deliberately left alone: adopting a revision is bookkeeping
	// for work that has not happened yet, not a change to the job.
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("adopt %s revision for job %d: %w", stage, jobID, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// CountAwaitingReprocess counts the jobs a stage can no longer pick up because
// their screening verdict is superseded. They are exactly the jobs the user's
// Profile reprocess releases, so a queue that stops moving has a number to
// report rather than going quiet.
func (s *Store) CountAwaitingReprocess(ctx context.Context, revisions Revisions) (int, error) {
	if revisions.Filter == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE process_state='queued' AND (filter_revision IS NULL OR filter_revision <> ?)", revisions.Filter).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count jobs awaiting reprocess: %w", err)
	}
	return count, nil
}

// SaveFilterResult appends one screening result and moves the job to the state
// its outcome calls for, in a single transaction guarded by the expected state
// and screening revision. A passing job also takes the active score revision:
// it is entering the score stage under it.
func (s *Store) SaveFilterResult(ctx context.Context, jobID int64, result FilterResult, revisions Revisions) error {
	to, err := stateForOutcome(result.Outcome, result.Partial)
	if err != nil {
		return err
	}
	if revisions.Filter == "" {
		return errors.New("store: filter revision is required")
	}
	if result.Stage != "structural" && result.Stage != "semantic" {
		return fmt.Errorf("store: invalid filter stage %q", result.Stage)
	}
	conditions, err := json.Marshal(nonNilConditions(result.Conditions))
	if err != nil {
		return fmt.Errorf("encode filter conditions: %w", err)
	}
	hits, err := encodeFilterHits(FailedConditionTexts(result.Conditions))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.timestamp()
	scoreRevision := any(nil)
	if to == "queued" {
		scoreRevision = revisions.Score
	}
	update, err := tx.ExecContext(ctx, "UPDATE jobs SET filter_hits=?, process_state=?, score_revision=?, updated_at=? WHERE id=? AND process_state='new' AND filter_revision=?", hits, to, scoreRevision, now, jobID, revisions.Filter)
	if err != nil {
		return err
	}
	if count, _ := update.RowsAffected(); count != 1 {
		return ErrStaleRevision
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO filter_results (job_id, outcome, conditions, stage, runner, filter_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)", jobID, result.Outcome, string(conditions), result.Stage, nullableRevision(result.Runner), revisions.Filter, now); err != nil {
		return fmt.Errorf("save filter result: %w", err)
	}
	if err := insertEvent(ctx, tx, jobID, "process", "new", to, "", now); err != nil {
		return err
	}
	return tx.Commit()
}

// CurrentFilterResult returns the screening result that produced a job's
// present verdict, which is the newest one recorded under its own revision.
func (s *Store) CurrentFilterResult(ctx context.Context, jobID int64) (*FilterResult, error) {
	var result FilterResult
	var conditions string
	err := s.db.QueryRowContext(ctx, `SELECT f.outcome, f.conditions, f.stage, f.runner, f.filter_revision
		FROM filter_results f JOIN jobs j ON j.id = f.job_id
		WHERE f.job_id=? AND f.filter_revision IS j.filter_revision
		ORDER BY f.created_at DESC, f.id DESC LIMIT 1`, jobID).Scan(&result.Outcome, &conditions, &result.Stage, &result.Runner, &result.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get filter result: %w", err)
	}
	if err := json.Unmarshal([]byte(conditions), &result.Conditions); err != nil {
		return nil, fmt.Errorf("decode filter conditions: %w", err)
	}
	return &result, nil
}

// CommitScore atomically appends a score and completes the queued state using
// expected-state and expected-revision compare-and-set semantics.
func (s *Store) CommitScore(ctx context.Context, input ScoreInput, to string) error {
	if err := validateScore(input); err != nil {
		return err
	}
	if input.ScoreRevision == "" || (to != "scored" && to != "shortlisted") {
		return errors.New("store: invalid revision-aware score")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE id=? AND process_state='queued' AND score_revision=?", input.JobID, input.ScoreRevision).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrStaleRevision
	}
	now := s.timestamp()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scores (job_id, dim_content, dim_benefit, dim_bonus, dim_industry, total, reason, runner, score_revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.JobID, input.Content, input.Benefit, input.Bonus, input.Industry, input.Total, input.Reason, input.Runner, input.ScoreRevision, now); err != nil {
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

// dropFilterResults discards the per-condition results of a job that is going
// back through screening. They described the assessment being withdrawn, and a
// screening the job has yet to receive can have no result to show.
func dropFilterResults(ctx context.Context, tx *sql.Tx, jobID int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM filter_results WHERE job_id=?", jobID); err != nil {
		return fmt.Errorf("drop filter results of job %d: %w", jobID, err)
	}
	return nil
}

// FailedConditionTexts lists the required conditions that rejected the job,
// which is what a list mark and the sidebar name as the reason. An undecided
// condition is not among them: it did not reject anything, and naming it would
// read as a rejection reason on a job that was in fact let through.
func FailedConditionTexts(conditions []FilterCondition) []string {
	texts := []string{}
	for _, condition := range conditions {
		if condition.Kind == "required" && condition.Verdict == FilterFail {
			texts = append(texts, condition.Text)
		}
	}
	return texts
}

func nonNilConditions(conditions []FilterCondition) []FilterCondition {
	if conditions == nil {
		return []FilterCondition{}
	}
	return conditions
}

// protectedProfileState reports the states a revision activation must not touch:
// the letter stages, whose output belongs to the user, and `merged`, which only
// the user's own unmerge may leave.
func protectedProfileState(state string) bool {
	return state == "letter_requested" || state == "letter_ready" || state == "letter_failed" || state == StateMerged
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

func nullableHitsValue(hits []string) any {
	encoded, err := encodeFilterHits(hits)
	if err != nil {
		return nil
	}
	return encoded
}
