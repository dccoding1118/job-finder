package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	picked, err := store.PickForStage(ctx, "filter", revisions(revision, revision), 10)
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

// A job queued on a superseded screening must not occupy the score stage's
// pick: it sorts first and needs a new screening the stage cannot buy, so an
// unfiltered pick would keep every eligible job waiting forever.
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

	picked, err := store.PickForStage(ctx, "score", revisions(active, active), 1)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if len(picked) != 1 || picked[0].ID != fresh {
		t.Fatalf("picked %+v, want only the job on the active revision", picked)
	}
}

// A v6 database predates token accounting. Upgrading it must add the columns
// without inventing usage for calls that were already paid for: their real
// numbers are gone, and a guess would make every daily total wrong.
func TestAgentUsageColumnsArriveEmptyOnAnUpgradedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage-upgrade.db")
	created := openTestStore(t, path)
	jobID := scoredJob(t, created, "rev-1", "scored")
	if err := created.SaveAgentCall(ctx, AgentCallInput{
		JobID: &jobID, Role: "scorer", Runner: "claude", Model: "claude-sonnet-5", Input: "prompt", Output: "ok", OK: true, DurationMS: 10,
		Usage: AgentCallUsage{InputTokens: 900, OutputTokens: 90, CostUSD: 0.01},
	}); err != nil {
		t.Fatal(err)
	}
	closeTestStore(t, created)

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX agent_calls_runner_model_created_idx",
		"ALTER TABLE agent_calls DROP COLUMN model",
		"ALTER TABLE agent_calls DROP COLUMN input_tokens",
		"ALTER TABLE agent_calls DROP COLUMN output_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cache_read_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cache_write_tokens",
		"ALTER TABLE agent_calls DROP COLUMN reasoning_tokens",
		"ALTER TABLE agent_calls DROP COLUMN cost_usd",
		"PRAGMA user_version = 6",
	} {
		if _, execErr := raw.Exec(statement); execErr != nil {
			_ = raw.Close()
			t.Fatalf("prepare v6 database: %v", execErr)
		}
	}
	if closeErr := raw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	migrated := openTestStore(t, path)
	defer closeTestStore(t, migrated)
	columns := tableColumns(t, migrated, "agent_calls")
	for _, column := range []string{"model", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "cost_usd"} {
		if !columns[column] {
			t.Fatalf("agent_calls is missing %q after the upgrade", column)
		}
	}
	calls, err := migrated.RecentAgentCalls(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("agent calls = %d, want the one legacy row", len(calls))
	}
	if calls[0].Model != "" || calls[0].Usage != (AgentCallUsage{}) {
		t.Fatalf("legacy call must carry no invented usage: %+v", calls[0])
	}
	if saveErr := migrated.SaveAgentCall(ctx, AgentCallInput{
		JobID: &jobID, Role: "scorer", Runner: "claude", Model: "claude-sonnet-5", Input: "prompt", Output: "ok", OK: true, DurationMS: 10,
		Usage: AgentCallUsage{InputTokens: 1000, OutputTokens: 100, CostUSD: 0.02},
	}); saveErr != nil {
		t.Fatal(saveErr)
	}
	usage, err := migrated.AgentUsageByDay(ctx, 14)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy call has no model, so it groups on its own — and contributes
	// nothing, which is the honest total for a call whose usage was never recorded.
	byModel := map[string]DailyAgentUsage{}
	for _, row := range usage {
		byModel[row.Model] = row
	}
	if legacy := byModel[""]; legacy.Calls != 1 || legacy.InputTokens != 0 || legacy.CostUSD != 0 {
		t.Fatalf("legacy row = %+v, want one call counted at zero usage", legacy)
	}
	if current := byModel["claude-sonnet-5"]; current.Calls != 1 || current.InputTokens != 1000 {
		t.Fatalf("post-upgrade row = %+v", current)
	}
}

// The daily view groups on the Taipei calendar day, which is the boundary the
// budget resets on: two calls three hours apart can belong to different days,
// and anything older than the window is not the recent past at all.
func TestAgentUsageByDayGroupsOnTheTaipeiBoundaryAndDropsOldCalls(t *testing.T) {
	ctx := context.Background()
	data := openTestStore(t, filepath.Join(t.TempDir(), "usage-days.db"))
	defer closeTestStore(t, data)
	jobID := scoredJob(t, data, "rev-1", "scored")

	now := data.now()
	for _, at := range []time.Time{now, now.Add(-24 * time.Hour), now.AddDate(0, 0, -30)} {
		if err := data.SaveAgentCall(ctx, AgentCallInput{
			JobID: &jobID, Role: "scorer", Runner: "claude", Model: "claude-sonnet-5", Input: "prompt", Output: "ok", OK: true, DurationMS: 10,
			Usage: AgentCallUsage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.001},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := data.db.ExecContext(ctx, "UPDATE agent_calls SET created_at = ? WHERE id = (SELECT MAX(id) FROM agent_calls)",
			at.In(time.Local).Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}

	usage, err := data.AgentUsageByDay(ctx, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 2 {
		t.Fatalf("daily usage rows = %d, want the two inside the window: %+v", len(usage), usage)
	}
	if usage[0].Date <= usage[1].Date {
		t.Fatalf("rows must run newest first: %+v", usage)
	}
	for _, row := range usage {
		if row.Calls != 1 || row.InputTokens != 100 {
			t.Fatalf("each day holds its own call only: %+v", row)
		}
	}
}
