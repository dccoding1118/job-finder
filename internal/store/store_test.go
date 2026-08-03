package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "jobs.db")
	store := openTestStore(t, path)
	defer closeTestStore(t, store)

	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	for _, table := range []string{"jobs", "scores", "letters", "status_events", "runs", "agent_calls"} {
		var count int
		if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %q does not exist", table)
		}
	}
	if err := store.migrate(context.Background()); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}

func TestUpsertJobDeduplicatesAndResetsNonterminalChangedContent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()

	input := fullJob("first description")
	created, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Changed || created.Job.ProcessState != "new" {
		t.Fatalf("unexpected first upsert result: %+v", created)
	}
	unchanged, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Created || unchanged.Changed {
		t.Fatalf("same content should not change job: %+v", unchanged)
	}
	if transitionErr := store.TransitionProcess(ctx, created.Job.ID, "queued"); transitionErr != nil {
		t.Fatal(transitionErr)
	}
	changedInput := fullJob("revised description")
	changed, err := store.UpsertJob(ctx, changedInput, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Changed || changed.Job.ProcessState != "new" {
		t.Fatalf("changed job = %+v, want changed new job", changed)
	}
	var jobs, events int
	if err := store.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM status_events WHERE job_id=?", created.Job.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || events != 3 {
		t.Fatalf("jobs/events = %d/%d, want 1/3", jobs, events)
	}
}

func TestPartialJobCompletesWithoutBeingOverwritten(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	partial := fullJob("")
	partial.Description = nil
	created, err := store.UpsertJob(ctx, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.Job.ProcessState != "discovered" || created.Job.ContentHash != nil {
		t.Fatalf("partial job = %+v", created.Job)
	}
	completed, err := store.UpsertJob(ctx, fullJob("full description"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Changed || completed.Job.ProcessState != "new" || completed.Job.ContentHash == nil {
		t.Fatalf("completed job = %+v", completed.Job)
	}
	if transitionErr := store.TransitionProcess(ctx, created.Job.ID, "queued"); transitionErr != nil {
		t.Fatal(transitionErr)
	}
	partialAgain, err := store.UpsertJob(ctx, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if partialAgain.Job.ProcessState != "queued" {
		t.Fatalf("partial upsert changed process state to %q", partialAgain.Job.ProcessState)
	}
}

func TestTransitionsWriteEventsAndRejectIllegalTransitions(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	result, err := store.UpsertJob(ctx, fullJob("description"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionProcess(ctx, result.Job.ID, "shortlisted"); err == nil {
		t.Fatal("direct new -> shortlisted transition succeeded")
	}
	if err := store.TransitionProcess(ctx, result.Job.ID, "queued"); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionProcess(ctx, result.Job.ID, "shortlisted"); err != nil {
		t.Fatal(err)
	}
	// A recommended job waits for the user: the letter states are only
	// reachable through a request.
	if err := store.TransitionProcess(ctx, result.Job.ID, "letter_ready"); err == nil {
		t.Fatal("shortlisted -> letter_ready transition succeeded without a request")
	}
	for _, state := range []string{"letter_requested", "letter_ready"} {
		if err := store.TransitionProcess(ctx, result.Job.ID, state); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if err := store.TransitionApply(ctx, result.Job.ID, "applied", "submitted manually"); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionApply(ctx, result.Job.ID, "pending", ""); err == nil {
		t.Fatal("apply state rollback succeeded")
	}
	var processEvents, applyEvents int
	if err := store.db.QueryRow("SELECT count(*) FROM status_events WHERE job_id=? AND axis='process'", result.Job.ID).Scan(&processEvents); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM status_events WHERE job_id=? AND axis='apply'", result.Job.ID).Scan(&applyEvents); err != nil {
		t.Fatal(err)
	}
	if processEvents != 5 || applyEvents != 2 {
		t.Fatalf("process/apply events = %d/%d, want 5/2", processEvents, applyEvents)
	}
}

func TestLetterStagePicksOnlyRequestedJobs(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	result, err := store.UpsertJob(ctx, fullJob("description"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted"} {
		if err = store.TransitionProcess(ctx, result.Job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	picked, err := store.PickForStage(ctx, "letter", Revisions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 0 {
		t.Fatalf("letter stage picked %d shortlisted jobs, want none", len(picked))
	}
	if err = store.TransitionProcess(ctx, result.Job.ID, "letter_requested"); err != nil {
		t.Fatal(err)
	}
	picked, err = store.PickForStage(ctx, "letter", Revisions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 1 || picked[0].ID != result.Job.ID {
		t.Fatalf("letter stage picked %+v, want the requested job", picked)
	}
}

func TestRunAttributionAndDailyBudgetCounting(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	runID, err := store.StartRun(ctx, RunTriggerTimer)
	if err != nil {
		t.Fatal(err)
	}
	fetched, err := store.UpsertJob(ctx, fullJob("description"), &runID)
	if err != nil {
		t.Fatal(err)
	}
	captured := fullJob("captured description")
	captured.ExternalID, captured.Source = "104-1", "104"
	if _, err = store.UpsertJob(ctx, captured, nil); err != nil {
		t.Fatal(err)
	}
	states, err := store.SummarizeRunJobs(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states["new"] != 1 {
		t.Fatalf("run job states = %+v, want only the fetched job", states)
	}
	if err = store.TransitionProcess(ctx, fetched.Job.ID, "queued"); err != nil {
		t.Fatal(err)
	}
	if states, err = store.SummarizeRunJobs(ctx, runID); err != nil || states["queued"] != 1 {
		t.Fatalf("run job states = %+v (%v), want the job's current state", states, err)
	}
	jobID := fetched.Job.ID
	for _, call := range []AgentCallInput{
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: "result", OK: true},
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: "result", OK: false},
		{JobID: &jobID, Role: "drafter", Runner: "claude", Input: "prompt", Output: "result", OK: true},
	} {
		if err = store.SaveAgentCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	dayStart := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	count, err := store.CountAgentCallsSince(ctx, "scorer", dayStart)
	if err != nil || count != 1 {
		t.Fatalf("scorer calls = %d (%v), want the successful one only", count, err)
	}
	if count, err = store.CountAgentCallsSince(ctx, "scorer", dayStart.AddDate(0, 0, 1)); err != nil || count != 0 {
		t.Fatalf("scorer calls after the day boundary = %d (%v), want 0", count, err)
	}
}

func TestForeignKeysAndRunAndAgentValidation(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	if err := store.SaveScore(ctx, ScoreInput{JobID: 999, Content: 1, Benefit: 1, Bonus: 1, Industry: 1, Runner: "claude"}); err == nil {
		t.Fatal("score for unknown job succeeded")
	}
	runID, err := store.StartRun(ctx, RunTriggerManualCLI)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, runID, RunStats{"fetched": 1}, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, runID, RunStats{}, ""); err == nil {
		t.Fatal("second finish succeeded")
	}
	if _, err := store.StartRun(ctx, RunTriggerManualExtension); err != nil {
		t.Fatalf("manual extension run: %v", err)
	}
	if _, err := store.StartRun(ctx, "manual-ui"); err == nil {
		t.Fatal("legacy manual-ui trigger succeeded")
	}
	if err := store.SaveAgentCall(ctx, AgentCallInput{Role: "scorer", Runner: "claude", Input: "prompt", Output: "result", DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgentCall(ctx, AgentCallInput{Role: "scorer", Runner: "claude", Input: "contact me@example.com", Output: "result"}); err == nil {
		t.Fatal("agent call with email succeeded")
	}
}

func TestListRunsReadsCompletedAndRunningRuns(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	first, err := store.StartRun(ctx, RunTriggerManualCLI)
	if err != nil {
		t.Fatal(err)
	}
	if finishErr := store.FinishRun(ctx, first, RunStats{"fetched": 1}, ""); finishErr != nil {
		t.Fatal(finishErr)
	}
	if _, startErr := store.StartRun(ctx, RunTriggerManualExtension); startErr != nil {
		t.Fatal(startErr)
	}
	runs, err := store.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Trigger != RunTriggerManualExtension || runs[1].FinishedAt == nil {
		t.Fatalf("unexpected runs: %+v", runs)
	}
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC) }
	return store
}

func closeTestStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func fullJob(description string) JobInput {
	return JobInput{
		Source:      "yourator",
		ExternalID:  "synthetic-1",
		URL:         "https://example.test/jobs/synthetic-1",
		Title:       "Backend Engineer",
		CompanyName: "Example Platform",
		CompanyInfo: "Software services",
		Description: &description,
		Location:    "Taipei",
		RemoteType:  "hybrid",
	}
}

var _ = sql.ErrNoRows
