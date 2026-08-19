package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// revisions is the pair a test activates or commits under.
func revisions(filter, score string) Revisions { return Revisions{Filter: filter, Score: score} }

// passFilter records a screening pass, which is what moves a job to the score
// stage under a given pair of revisions.
func passFilter(t *testing.T, data *Store, jobID int64, pair Revisions) error {
	t.Helper()
	result := FilterResult{Outcome: FilterPass, Conditions: []FilterCondition{{Text: "locations", Kind: "required", Group: 1, Category: "other", Verdict: FilterPass}}, Stage: "structural"}
	return data.SaveFilterResult(context.Background(), jobID, result, pair)
}

func scoreInput(jobID int64, total float64, revision string) ScoreInput {
	return ScoreInput{JobID: jobID, Content: 60, Benefit: 60, Bonus: 60, Industry: 60, Total: total, Reason: "fit", Runner: "claude", ScoreRevision: revision}
}

// rewindRevisionSchema returns the statements that put a v6 database back into
// the single-revision, five-dimension shape an older upgrade step expects.
// withProfileRevision is false for the pre-v2 shape, which had no revision
// column at all.
func rewindRevisionSchema(withProfileRevision bool) []string {
	statements := []string{
		"ALTER TABLE runs DROP COLUMN heartbeat_at",
		"DROP TABLE IF EXISTS filter_results",
		"ALTER TABLE jobs DROP COLUMN filter_revision",
		"ALTER TABLE jobs DROP COLUMN score_revision",
		"ALTER TABLE letters DROP COLUMN filter_revision",
		"ALTER TABLE letters DROP COLUMN score_revision",
		"ALTER TABLE agent_calls DROP COLUMN filter_revision",
		"ALTER TABLE agent_calls DROP COLUMN score_revision",
		"DROP INDEX agent_calls_runner_model_created_idx",
		"ALTER TABLE agent_calls DROP COLUMN model",
		"ALTER TABLE agent_calls DROP COLUMN input_tokens",
		"ALTER TABLE agent_calls DROP COLUMN output_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cache_read_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cache_write_tokens",
		"ALTER TABLE agent_calls DROP COLUMN reasoning_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cost_usd",
		"DROP TABLE scores",
		`CREATE TABLE scores (id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES jobs(id),
			dim_hard_skill INTEGER NOT NULL, dim_domain INTEGER NOT NULL, dim_seniority INTEGER NOT NULL,
			dim_condition INTEGER NOT NULL, dim_direction INTEGER NOT NULL, total REAL NOT NULL,
			reason TEXT NOT NULL, runner TEXT NOT NULL, created_at TEXT NOT NULL)`,
	}
	if !withProfileRevision {
		return statements
	}
	return append(
		statements,
		"ALTER TABLE jobs ADD COLUMN profile_revision TEXT",
		"ALTER TABLE letters ADD COLUMN profile_revision TEXT",
		"ALTER TABLE agent_calls ADD COLUMN profile_revision TEXT",
		"ALTER TABLE scores ADD COLUMN profile_revision TEXT",
	)
}

func TestMigrationFromVersionTwoAddsRevisionColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	created := openTestStore(t, path)
	closeTestStore(t, created)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind the file to the v2 shape: one revision column per table, the older
	// five scoring dimensions, and none of the grouping or screening tables.
	statements := append([]string{
		"DROP TABLE job_dupe_candidates",
		"DROP TABLE job_groups",
		"DROP INDEX jobs_group_idx",
		"ALTER TABLE jobs DROP COLUMN group_id",
	}, rewindRevisionSchema(false)...)
	for _, statement := range append(statements, "PRAGMA user_version = 2") {
		if _, err := raw.Exec(statement); err != nil {
			_ = raw.Close()
			t.Fatalf("prepare v2 database: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	migrated := openTestStore(t, path)
	defer closeTestStore(t, migrated)
	for table, want := range map[string][]string{
		"jobs":        {"filter_revision", "score_revision"},
		"scores":      {"score_revision", "dim_content", "dim_benefit", "dim_bonus", "dim_industry"},
		"letters":     {"filter_revision", "score_revision"},
		"agent_calls": {"filter_revision", "score_revision"},
	} {
		columns := tableColumns(t, migrated, table)
		for _, column := range want {
			if !columns[column] {
				t.Fatalf("%s.%s is missing after migration", table, column)
			}
		}
		if columns["profile_revision"] {
			t.Fatalf("%s still carries the single profile_revision column", table)
		}
	}
}

// The hard/soft split resets every screened or scored job: both verdicts were
// produced by rules that no longer exist. Letter history is the user's own
// output and must survive untouched.
func TestMigrationToVersionSixResetsAssessedJobsAndKeepsLetters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	created := openTestStore(t, path)
	ctx := context.Background()
	pair := revisions("sha256:old", "sha256:old")

	scored := fullJob("scored description")
	scored.ExternalID, scored.FilterRevision = "scored", pair.Filter
	scoredJob, err := created.UpsertJob(ctx, scored, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, created, scoredJob.Job.ID, pair); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := created.CommitScore(ctx, scoreInput(scoredJob.Job.ID, 60, pair.Score), "scored"); stepErr != nil {
		t.Fatal(stepErr)
	}
	// A job the list page rejected on its title alone never had a JD. The reset
	// must not offer it to the screen as if it did.
	excerpt := fullJob("")
	excerpt.ExternalID, excerpt.URL, excerpt.Description, excerpt.FilterRevision = "excerpt", "https://example.test/jobs/excerpt", nil, pair.Filter
	excerptJob, err := created.UpsertJob(ctx, excerpt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := created.SetFilterHits(ctx, excerptJob.Job.ID, []string{"exclude_title_keywords"}); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := created.TransitionProcess(ctx, excerptJob.Job.ID, "filtered_out"); stepErr != nil {
		t.Fatal(stepErr)
	}

	lettered := fullJob("lettered description")
	lettered.ExternalID, lettered.FilterRevision = "lettered", pair.Filter
	letteredJob, err := created.UpsertJob(ctx, lettered, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, created, letteredJob.Job.ID, pair); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := created.CommitScore(ctx, scoreInput(letteredJob.Job.ID, 90, pair.Score), "shortlisted"); stepErr != nil {
		t.Fatal(stepErr)
	}
	for _, state := range []string{"letter_requested", "letter_ready"} {
		if stepErr := created.TransitionProcess(ctx, letteredJob.Job.ID, state); stepErr != nil {
			t.Fatal(stepErr)
		}
	}
	if stepErr := created.SaveLetter(ctx, LetterInput{JobID: letteredJob.Job.ID, Content: "letter", Status: "approved", Rounds: 1, ReviewLog: "approve", RunnerDraft: "claude", RunnerReview: "codex"}); stepErr != nil {
		t.Fatal(stepErr)
	}
	closeTestStore(t, created)

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := raw.Exec("PRAGMA user_version = 5"); execErr != nil {
		_ = raw.Close()
		t.Fatal(execErr)
	}
	// Reinstate the columns the v6 upgrade expects to replace.
	for _, statement := range rewindRevisionSchema(true) {
		if _, execErr := raw.Exec(statement); execErr != nil {
			_ = raw.Close()
			t.Fatalf("prepare v5 database: %v", execErr)
		}
	}
	if closeErr := raw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	migrated := openTestStore(t, path)
	defer closeTestStore(t, migrated)
	scoredDetail, _, err := migrated.GetJobDetail(ctx, scoredJob.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scoredDetail.Job.ProcessState != "new" || scoredDetail.Score != nil {
		t.Fatalf("scored job after v6 = %s score=%+v, want a reset job with no score", scoredDetail.Job.ProcessState, scoredDetail.Score)
	}
	excerptDetail, _, err := migrated.GetJobDetail(ctx, excerptJob.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if excerptDetail.Job.ProcessState != "discovered" {
		t.Fatalf("text-less job after v6 = %s, want discovered", excerptDetail.Job.ProcessState)
	}
	letteredDetail, _, err := migrated.GetJobDetail(ctx, letteredJob.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if letteredDetail.Job.ProcessState != "letter_ready" || letteredDetail.Letter == nil {
		t.Fatalf("letter history was not preserved: %+v", letteredDetail.Job)
	}
}

func tableColumns(t *testing.T, data *Store, table string) map[string]bool {
	t.Helper()
	rows, err := data.db.Query("PRAGMA table_info(" + table + ")") // #nosec G202 -- table names are fixed test constants.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	return columns
}

func TestActivateProfileReprocessesEligibleAndProtectsLetterHistory(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	old := revisions("sha256:old", "sha256:old")

	partial := fullJob("")
	partial.ExternalID, partial.Description, partial.FilterRevision = "partial", nil, old.Filter
	partialJob, err := data.UpsertJob(ctx, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	full := fullJob("complete description")
	full.ExternalID, full.FilterRevision = "full", old.Filter
	fullJobResult, err := data.UpsertJob(ctx, full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, data, fullJobResult.Job.ID, old); stepErr != nil {
		t.Fatal(stepErr)
	}
	protected := fullJob("protected description")
	protected.ExternalID, protected.FilterRevision = "protected", old.Filter
	protectedJob, err := data.UpsertJob(ctx, protected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, data, protectedJob.Job.ID, old); stepErr != nil {
		t.Fatal(stepErr)
	}
	for _, state := range []string{"shortlisted", "letter_requested"} {
		if transitionErr := data.TransitionProcess(ctx, protectedJob.Job.ID, state); transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}

	stats, err := data.ActivateProfile(ctx, revisions("sha256:new", "sha256:new"), func(job Job) []string {
		if job.ID == partialJob.Job.ID {
			return []string{"locations"}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.PartialScreened != 1 || stats.Refiltered != 1 || stats.Protected != 1 {
		t.Fatalf("activation stats = %+v", stats)
	}
	partialDetail, _, _ := data.GetJobDetail(ctx, partialJob.Job.ID)
	fullDetail, _, _ := data.GetJobDetail(ctx, fullJobResult.Job.ID)
	protectedDetail, _, _ := data.GetJobDetail(ctx, protectedJob.Job.ID)
	if partialDetail.Job.ProcessState != "filtered_out" || fullDetail.Job.ProcessState != "new" || protectedDetail.Job.ProcessState != "letter_requested" {
		t.Fatalf("states = %s/%s/%s", partialDetail.Job.ProcessState, fullDetail.Job.ProcessState, protectedDetail.Job.ProcessState)
	}
	if partialDetail.Job.FilterRevision == nil || *partialDetail.Job.FilterRevision != "sha256:new" || protectedDetail.Job.FilterRevision == nil || *protectedDetail.Job.FilterRevision != "sha256:old" {
		t.Fatal("activation changed the wrong revisions")
	}
	second, err := data.ActivateProfile(ctx, revisions("sha256:new", "sha256:new"), func(Job) []string { return nil })
	if err != nil || second.Unchanged != 2 || second.Protected != 1 {
		t.Fatalf("idempotent activation = %+v err=%v", second, err)
	}
}

// A soft-rule change reruns the score alone. Everything screening decided —
// including what it rejected — stays exactly as it was.
func TestActivateProfileWithOnlyScoreRevisionChangedKeepsScreening(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	old := revisions("sha256:filter", "sha256:score-old")

	scored := fullJob("scored description")
	scored.ExternalID, scored.FilterRevision = "scored", old.Filter
	scoredJob, err := data.UpsertJob(ctx, scored, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, data, scoredJob.Job.ID, old); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := data.CommitScore(ctx, scoreInput(scoredJob.Job.ID, 60, old.Score), "scored"); stepErr != nil {
		t.Fatal(stepErr)
	}
	rejected := fullJob("rejected description")
	rejected.ExternalID, rejected.FilterRevision = "rejected", old.Filter
	rejectedJob, err := data.UpsertJob(ctx, rejected, nil)
	if err != nil {
		t.Fatal(err)
	}
	failing := FilterResult{Outcome: FilterFail, Conditions: []FilterCondition{{Text: "locations", Kind: "required", Group: 1, Category: "other", Verdict: FilterFail}}, Stage: "structural"}
	if stepErr := data.SaveFilterResult(ctx, rejectedJob.Job.ID, failing, old); stepErr != nil {
		t.Fatal(stepErr)
	}

	stats, err := data.ActivateProfile(ctx, revisions(old.Filter, "sha256:score-new"), func(Job) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requeued != 1 || stats.Refiltered != 0 || stats.Unchanged != 1 {
		t.Fatalf("score-only activation = %+v", stats)
	}
	scoredDetail, _, _ := data.GetJobDetail(ctx, scoredJob.Job.ID)
	rejectedDetail, _, _ := data.GetJobDetail(ctx, rejectedJob.Job.ID)
	if scoredDetail.Job.ProcessState != "queued" || rejectedDetail.Job.ProcessState != "filtered_out" {
		t.Fatalf("states = %s/%s", scoredDetail.Job.ProcessState, rejectedDetail.Job.ProcessState)
	}
	if scoredDetail.Filter == nil {
		t.Fatal("the screening result was discarded by a score-only activation")
	}
}

// A job that keeps its state through a content change keeps the revisions its
// screening result and score were produced under: those are what every reader
// pairs it with them by.
func TestUpsertKeepsRevisionOfAKeptAssessment(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	old := revisions("sha256:old", "sha256:old")
	input := fullJob("first description")
	input.FilterRevision = old.Filter
	created, err := data.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stepErr := passFilter(t, data, created.Job.ID, old); stepErr != nil {
		t.Fatal(stepErr)
	}
	if stepErr := data.CommitScore(ctx, scoreInput(created.Job.ID, 40, old.Score), "scored"); stepErr != nil {
		t.Fatal(stepErr)
	}

	changed := fullJob("a revised description")
	changed.FilterRevision = "sha256:new"
	updated, err := data.UpsertJob(ctx, changed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Changed || updated.Job.ProcessState != "scored" {
		t.Fatalf("re-ingested job = %+v, want a changed job still scored", updated.Job)
	}
	if updated.Job.ScoreRevision == nil || *updated.Job.ScoreRevision != "sha256:old" {
		t.Fatalf("returned revision = %v, want the revision the score was produced under", updated.Job.ScoreRevision)
	}
	// The list reads a job's score through the revision the job records, so a
	// revision the score cannot be found under reads as no score at all.
	listed, err := data.ListJobs(ctx, JobFilter{ProcessState: "scored"}, JobSortScore)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ScoreTotal == nil || *listed[0].ScoreTotal != 40 {
		t.Fatalf("listed job = %+v, want the score it carries", listed)
	}
	current, err := data.CurrentScore(ctx, created.Job.ID)
	if err != nil || current == nil {
		t.Fatalf("current score = %+v err=%v", current, err)
	}
}

func TestRevisionCASRejectsOldFilterAndScore(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	input := fullJob("description")
	input.FilterRevision = "sha256:new"
	created, err := data.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if commitErr := passFilter(t, data, created.Job.ID, revisions("sha256:old", "sha256:old")); !errors.Is(commitErr, ErrStaleRevision) {
		t.Fatalf("old filter CAS error = %v", commitErr)
	}
	if commitErr := passFilter(t, data, created.Job.ID, revisions("sha256:new", "sha256:new")); commitErr != nil {
		t.Fatal(commitErr)
	}
	score := scoreInput(created.Job.ID, 80, "sha256:old")
	if commitErr := data.CommitScore(ctx, score, "shortlisted"); !errors.Is(commitErr, ErrStaleRevision) {
		t.Fatalf("old score CAS error = %v", commitErr)
	}
	score.ScoreRevision = "sha256:new"
	if commitErr := data.CommitScore(ctx, score, "shortlisted"); commitErr != nil {
		t.Fatal(commitErr)
	}
	current, err := data.CurrentScore(ctx, created.Job.ID)
	if err != nil || current == nil || current.ScoreRevision == nil || *current.ScoreRevision != "sha256:new" {
		t.Fatalf("current score = %+v err=%v", current, err)
	}
}

// Screening resolves to one of two states for a complete JD, and to the 待看
// list for an excerpt that still cannot be decided.
func TestSaveFilterResultRoutesByOutcomeAndCompleteness(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	pair := revisions("sha256:filter", "sha256:score")

	for name, testCase := range map[string]struct {
		conditions []FilterCondition
		partial    bool
		wantState  string
	}{
		"fail": {[]FilterCondition{{Text: "locations", Kind: "required", Group: 1, Category: "other", Verdict: FilterFail}}, false, "filtered_out"},
		"undecided excerpt": {[]FilterCondition{
			{Text: "salary", Kind: "required", Group: 1, Category: "other", Verdict: FilterUnknown},
			{Text: "a nice-to-have", Kind: "bonus", Group: 2, Category: "skill", Verdict: FilterFail},
		}, true, "discovered"},
		"pass": {[]FilterCondition{
			{Text: "either A", Kind: "required", Group: 1, Category: "skill", Verdict: FilterFail},
			{Text: "or B", Kind: "required", Group: 1, Category: "skill", Verdict: FilterPass},
		}, false, "queued"},
	} {
		t.Run(name, func(t *testing.T) {
			input := fullJob("description for " + name)
			input.ExternalID, input.FilterRevision = name, pair.Filter
			created, err := data.UpsertJob(ctx, input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.partial {
				// Only a job that reached `new` without its text can take this path,
				// which is what the v6 reset guard exists to prevent.
				if _, execErr := data.db.ExecContext(ctx, "UPDATE jobs SET description=NULL WHERE id=?", created.Job.ID); execErr != nil {
					t.Fatal(execErr)
				}
			}
			outcome := SummarizeConditions(testCase.conditions)
			result := FilterResult{Outcome: outcome, Conditions: testCase.conditions, Stage: "semantic", Partial: testCase.partial}
			if stepErr := data.SaveFilterResult(ctx, created.Job.ID, result, pair); stepErr != nil {
				t.Fatal(stepErr)
			}
			detail, _, err := data.GetJobDetail(ctx, created.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Job.ProcessState != testCase.wantState {
				t.Fatalf("state = %s, want %s", detail.Job.ProcessState, testCase.wantState)
			}
			if detail.Filter == nil || len(detail.Filter.Conditions) != len(testCase.conditions) {
				t.Fatalf("stored screening result = %+v", detail.Filter)
			}
			if testCase.wantState == "queued" && (detail.Job.ScoreRevision == nil || *detail.Job.ScoreRevision != pair.Score) {
				t.Fatalf("a passing job did not take the score revision: %+v", detail.Job.ScoreRevision)
			}
		})
	}
}

// A complete JD is never left undecided: the aggregate turns its remaining
// unknowns into a pass, so reaching the store with one is a defect to report,
// not a state to persist.
func TestSaveFilterResultRejectsAnUndecidedCompleteJD(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	pair := revisions("sha256:filter", "sha256:score")
	input := fullJob("description")
	input.FilterRevision = pair.Filter
	created, err := data.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	undecided := FilterResult{Outcome: FilterUnknown, Conditions: []FilterCondition{{Text: "salary", Kind: "required", Group: 1, Category: "other", Verdict: FilterUnknown}}, Stage: "semantic"}
	if err := data.SaveFilterResult(ctx, created.Job.ID, undecided, pair); err == nil {
		t.Fatal("an undecided complete JD was accepted")
	}
}

// Adoption covers exactly the states that hold no assessment: a job waiting to
// be screened takes the active screening revision, a queued job takes the
// active scoring one — but only while the screening that queued it still
// stands, because a superseded screening has to be bought again.
func TestAdoptStageRevisionOnlyCoversJobsWithNothingToProtect(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	stale := revisions("sha256:old-filter", "sha256:old-score")
	active := revisions("sha256:filter", "sha256:score")

	create := func(externalID string) int64 {
		t.Helper()
		input := fullJob("a full description")
		input.ExternalID, input.URL, input.FilterRevision = externalID, "https://example.test/jobs/"+externalID, stale.Filter
		created, err := data.UpsertJob(ctx, input, nil)
		if err != nil {
			t.Fatal(err)
		}
		return created.Job.ID
	}
	adopt := func(stage string, jobID int64) bool {
		t.Helper()
		adopted, err := data.AdoptStageRevision(ctx, stage, jobID, active)
		if err != nil {
			t.Fatal(err)
		}
		return adopted
	}

	waiting := create("waiting")
	if !adopt("filter", waiting) {
		t.Fatal("a job waiting to be screened must adopt the active screening revision")
	}
	if err := passFilter(t, data, waiting, active); err != nil {
		t.Fatalf("the adopted job must pass the screening CAS: %v", err)
	}
	if adopt("filter", waiting) {
		t.Fatal("a job that has left `new` must not adopt a screening revision")
	}
	if !adopt("score", waiting) {
		t.Fatal("a queued job on a current screening must adopt the active scoring revision")
	}

	superseded := create("superseded")
	if err := passFilter(t, data, superseded, stale); err != nil {
		t.Fatal(err)
	}
	if adopt("score", superseded) {
		t.Fatal("a job queued on a superseded screening waits for the user's reprocess")
	}
	count, err := data.CountAwaitingReprocess(ctx, active)
	if err != nil || count != 1 {
		t.Fatalf("jobs awaiting reprocess = %d (%v), want 1", count, err)
	}
}

// Only `new` and `queued` are picked up. A job waiting on the user to open its
// page is not work the worker may take.
func TestPickForStageIgnoresJobsWaitingOnTheUser(t *testing.T) {
	data := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, data)
	ctx := context.Background()
	pair := revisions("sha256:filter", "sha256:score")
	input := fullJob("description")
	input.Description, input.FilterRevision = nil, pair.Filter
	if _, err := data.UpsertJob(ctx, input, nil); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"filter", "score"} {
		jobs, err := data.PickForStage(ctx, stage, pair, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 0 {
			t.Fatalf("%s stage picked up a job waiting on the user: %+v", stage, jobs)
		}
	}
}
