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
	input.FilterRevision = revision
	created, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := passFilter(t, store, created.Job.ID, revisions(revision, revision)); err != nil {
		t.Fatal(err)
	}
	score := ScoreInput{JobID: created.Job.ID, Content: 4, Benefit: 4, Bonus: 4, Industry: 4, Total: 70, Reason: "first pass", Runner: "claude", ScoreRevision: revision}
	if err := store.CommitScore(ctx, score, to); err != nil {
		t.Fatal(err)
	}
	return created.Job.ID
}

func TestReprocessJobReturnsJobToScreeningAndKeepsOldScore(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	jobID := scoredJob(t, store, revision, "scored")

	if err := store.ReprocessJob(ctx, jobID, revisions(revision, revision)); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	detail, found, err := store.GetJobDetail(ctx, jobID)
	if err != nil || !found {
		t.Fatalf("read job: %v found=%v", err, found)
	}
	if detail.Job.ProcessState != "new" {
		t.Fatalf("process state = %q, want new", detail.Job.ProcessState)
	}
	if detail.Job.FilterHits != nil || detail.Job.ScoreRevision != nil {
		t.Fatalf("a job awaiting screening keeps no hits and no score revision: %+v %+v", detail.Job.FilterHits, detail.Job.ScoreRevision)
	}
	if detail.Filter != nil {
		t.Fatalf("the superseded screening result must not be reported as current: %+v", detail.Filter)
	}
	if detail.Score == nil || detail.Score.Reason != "first pass" {
		t.Fatalf("previous score must stay readable until the new one lands: %+v", detail.Score)
	}
	picked, err := store.PickForStage(ctx, "filter", revision, 10)
	if err != nil || len(picked) != 1 || picked[0].ID != jobID {
		t.Fatalf("reprocessed job must be picked for filter: %v %+v", err, picked)
	}
	var noted bool
	for _, event := range detail.Events {
		if event.ToState == "new" && event.Note != nil && *event.Note == "manual reprocess" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("manual reprocess must be recorded in status_events: %+v", detail.Events)
	}
}

// A job screened off a list page carries no JD, so a reprocess sends it back to
// the待看 list to be opened rather than offering an excerpt to be scored.
func TestReprocessJobReturnsPartialJobToDiscovered(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	input := fullJob("a full description")
	input.Description = nil
	input.FilterRevision = revision
	created, err := store.UpsertJob(ctx, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hitsErr := store.SetFilterHits(ctx, created.Job.ID, []string{"locations"}); hitsErr != nil {
		t.Fatal(hitsErr)
	}
	if transitionErr := store.TransitionProcess(ctx, created.Job.ID, "filtered_out"); transitionErr != nil {
		t.Fatal(transitionErr)
	}

	if reprocessErr := store.ReprocessJob(ctx, created.Job.ID, revisions(revision, revision)); reprocessErr != nil {
		t.Fatalf("reprocess: %v", reprocessErr)
	}
	detail, _, err := store.GetJobDetail(ctx, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "discovered" || detail.Job.FilterHits != nil {
		t.Fatalf("partial job = %q hits=%+v, want discovered without hits", detail.Job.ProcessState, detail.Job.FilterHits)
	}
}

func TestReprocessJobAdoptsActiveRevisionAndIsIdempotent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	jobID := scoredJob(t, store, "sha256:rev-a", "shortlisted")

	if err := store.ReprocessJob(ctx, jobID, revisions("sha256:rev-b", "sha256:rev-b")); err != nil {
		t.Fatalf("reprocess: %v", err)
	}
	if err := store.ReprocessJob(ctx, jobID, revisions("sha256:rev-b", "sha256:rev-b")); err != nil {
		t.Fatalf("second reprocess must be a no-op: %v", err)
	}
	detail, _, err := store.GetJobDetail(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.FilterRevision == nil || *detail.Job.FilterRevision != "sha256:rev-b" {
		t.Fatalf("reprocessed job must adopt the active revision: %+v", detail.Job.FilterRevision)
	}
	if detail.Job.ProcessState != "new" {
		t.Fatalf("process state = %q, want new", detail.Job.ProcessState)
	}
}

func TestReprocessJobRefusesLetterAndUnknownJobs(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "jobs.db"))
	defer closeTestStore(t, store)
	ctx := context.Background()
	const revision = "sha256:rev-a"
	jobID := scoredJob(t, store, revision, "shortlisted")
	if err := store.TransitionProcess(ctx, jobID, "letter_requested"); err != nil {
		t.Fatal(err)
	}

	if err := store.ReprocessJob(ctx, jobID, revisions(revision, revision)); !errors.Is(err, ErrReprocessNotAllowed) {
		t.Fatalf("letter history must be protected: %v", err)
	}
	if err := store.ReprocessJob(ctx, jobID+999, revisions(revision, revision)); !errors.Is(err, sql.ErrNoRows) {
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
		{
			JobID: &jobID, Role: "scorer", Runner: "claude", Model: "claude-sonnet-5", Input: "prompt", Output: "ok", OK: true, DurationMS: 1200, ScoreRevision: revision,
			Usage: AgentCallUsage{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 50, CacheWriteTokens: 10, CostUSD: 0.0123},
		},
		{
			JobID: &jobID, Role: "scorer", Runner: "codex", Model: "gpt-5.6-terra", Input: "prompt", Output: failure, OK: false, DurationMS: 90000, ScoreRevision: revision,
			Usage: AgentCallUsage{InputTokens: 500, OutputTokens: 5, ReasoningTokens: 20},
		},
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
	if newest.Model != "gpt-5.6-terra" || newest.Usage.InputTokens != 500 || newest.Usage.ReasoningTokens != 20 {
		t.Fatalf("failed call must still carry its token usage: %+v", newest)
	}
	if calls[1].Output != "" {
		t.Fatalf("successful call must not carry Agent output: %q", calls[1].Output)
	}
	if calls[1].Model != "claude-sonnet-5" || calls[1].Usage.InputTokens != 1000 || calls[1].Usage.OutputTokens != 200 || calls[1].Usage.CostUSD != 0.0123 {
		t.Fatalf("successful call usage/model mismatch: %+v", calls[1])
	}
	if _, limitErr := store.RecentAgentCalls(ctx, 0); limitErr == nil {
		t.Fatal("non-positive limit must be rejected")
	}

	usage, err := store.AgentUsageByDay(ctx, 14)
	if err != nil {
		t.Fatal(err)
	}
	byRunner := map[string]DailyAgentUsage{}
	for _, row := range usage {
		byRunner[row.Runner] = row
	}
	claudeUsage, codexUsage := byRunner["claude"], byRunner["codex"]
	if claudeUsage.Calls != 1 || claudeUsage.InputTokens != 1000 || claudeUsage.OutputTokens != 200 || claudeUsage.Model != "claude-sonnet-5" {
		t.Fatalf("claude daily usage = %+v", claudeUsage)
	}
	if codexUsage.Calls != 1 || codexUsage.InputTokens != 500 || codexUsage.ReasoningTokens != 20 || codexUsage.Model != "gpt-5.6-terra" {
		t.Fatalf("codex daily usage = %+v", codexUsage)
	}
	if codexUsage.CostUSD != 0 {
		t.Fatalf("codex does not price its own calls: cost_usd = %v", codexUsage.CostUSD)
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
		input.FilterRevision = revision
		created, err := store.UpsertJob(ctx, input, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := passFilter(t, store, created.Job.ID, revisions(revision, revision)); err != nil {
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
