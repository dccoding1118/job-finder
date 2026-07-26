package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func scoredJob(t *testing.T, store *Store, revision, to string) int64 {
	t.Helper()
	ctx := context.Background()
	input := fullJob("a full description")
	input.ProfileRevision = revision
	created, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitFilter(ctx, created.Job.ID, revision, nil); err != nil {
		t.Fatal(err)
	}
	score := ScoreInput{JobID: created.Job.ID, HardSkill: 4, Domain: 4, Seniority: 4, Condition: 3, Direction: 4, Total: 70, Reason: "first pass", Runner: "claude", ProfileRevision: revision}
	if err := store.CommitScore(ctx, score, to); err != nil {
		t.Fatal(err)
	}
	return created.Job.ID
}

func TestRequeueScoreReturnsJobToScoreStageAndKeepsOldScore(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	jobID := scoredJob(t, store, revision, "scored")

	if err := store.RequeueScore(ctx, jobID, revision); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	detail, found, err := store.GetJobDetail(ctx, jobID)
	if err != nil || !found {
		t.Fatalf("read job: %v found=%v", err, found)
	}
	if detail.Job.ProcessState != "queued" {
		t.Fatalf("process state = %q, want queued", detail.Job.ProcessState)
	}
	if detail.Score == nil || detail.Score.Reason != "first pass" {
		t.Fatalf("previous score must stay readable until the new one lands: %+v", detail.Score)
	}
	picked, err := store.PickForStage(ctx, "score", "", 10)
	if err != nil || len(picked) != 1 || picked[0].ID != jobID {
		t.Fatalf("requeued job must be picked for score: %v %+v", err, picked)
	}
	var noted bool
	for _, event := range detail.Events {
		if event.ToState == "queued" && event.Note != nil && *event.Note == "manual rescore" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("manual rescore must be recorded in status_events: %+v", detail.Events)
	}
}

func TestRequeueScoreAdoptsActiveRevisionAndIsIdempotent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	jobID := scoredJob(t, store, "sha256:rev-a", "shortlisted")

	if err := store.RequeueScore(ctx, jobID, "sha256:rev-b"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if err := store.RequeueScore(ctx, jobID, "sha256:rev-b"); err != nil {
		t.Fatalf("second requeue must be a no-op: %v", err)
	}
	detail, _, err := store.GetJobDetail(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProfileRevision == nil || *detail.Job.ProfileRevision != "sha256:rev-b" {
		t.Fatalf("requeued job must adopt the active revision: %+v", detail.Job.ProfileRevision)
	}
}

func TestRequeueScoreRefusesLetterAndUnknownJobs(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	jobID := scoredJob(t, store, revision, "shortlisted")
	if err := store.TransitionProcess(ctx, jobID, "letter_requested"); err != nil {
		t.Fatal(err)
	}

	if err := store.RequeueScore(ctx, jobID, revision); !errors.Is(err, ErrRescoreNotAllowed) {
		t.Fatalf("letter history must be protected: %v", err)
	}
	if err := store.RequeueScore(ctx, jobID+999, revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown job = %v, want no rows", err)
	}
}

func TestCountJobsByStateAndRecentAgentCalls(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	jobID := scoredJob(t, store, revision, "scored")

	counts, err := store.CountJobsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["scored"] != 1 {
		t.Fatalf("state counts = %v, want one scored job", counts)
	}

	failure := strings.Repeat("x", 450)
	for _, call := range []AgentCallInput{
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: "ok", OK: true, DurationMS: 1200, ProfileRevision: revision},
		{JobID: &jobID, Role: "scorer", Runner: "codex", Input: "prompt", Output: failure, OK: false, DurationMS: 90000, ProfileRevision: revision},
	} {
		if saveErr := store.SaveAgentCall(ctx, call); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	calls, err := store.RecentAgentCalls(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("agent calls = %d, want 2", len(calls))
	}
	newest := calls[0]
	if newest.OK || newest.DurationMS != 90000 || newest.Role != "scorer" {
		t.Fatalf("newest call must be the failed one: %+v", newest)
	}
	if newest.Output != failure {
		t.Fatalf("failed call must carry its response for classification: %d runes", len([]rune(newest.Output)))
	}
	if calls[1].Output != "" {
		t.Fatalf("successful call must not carry Agent output: %q", calls[1].Output)
	}
	if _, err := store.RecentAgentCalls(ctx, 0); err == nil {
		t.Fatal("non-positive limit must be rejected")
	}
}

// A job left behind on an older Profile revision must not occupy the stage's
// pick: it sorts first and the stage can only discard it, so an unfiltered pick
// would keep every eligible job waiting forever.
func TestPickForStageSkipsOtherRevisionsSoQueueDoesNotStarve(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const stale, active = "sha256:rev-stale", "sha256:rev-active"

	queued := func(externalID, revision string) int64 {
		t.Helper()
		input := fullJob("a full description")
		input.ExternalID = externalID
		input.URL = "https://example.test/jobs/" + externalID
		input.ProfileRevision = revision
		created, err := store.UpsertJob(ctx, input, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CommitFilter(ctx, created.Job.ID, revision, nil); err != nil {
			t.Fatal(err)
		}
		return created.Job.ID
	}
	queued("stale-1", stale)
	fresh := queued("active-1", active)

	picked, err := store.PickForStage(ctx, "score", active, 1)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if len(picked) != 1 || picked[0].ID != fresh {
		t.Fatalf("picked %+v, want only the job on the active revision", picked)
	}
}
