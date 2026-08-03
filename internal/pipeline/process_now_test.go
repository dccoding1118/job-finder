package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// waitingPipeline is a pipeline whose screening costs no Agent call, so a job
// ingested with a full JD waits at `queued` for exactly one scoring call.
func waitingPipeline(t *testing.T, scorer agents.Runner) (Pipeline, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "process-now.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := Pipeline{
		Store:     db,
		Filter:    filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}),
		Scorer:    agents.Scorer{Primary: scorer},
		Profile:   syntheticProfile(),
		Weights:   [4]float64{.25, .25, .25, .25},
		Threshold: 75,
		Gate:      &Gate{},
	}
	return p, db
}

func ingestFullJob(t *testing.T, p Pipeline, externalID string) int64 {
	t.Helper()
	result, err := p.IngestJob(context.Background(), crawler.RawJob{
		Source: "yourator", ExternalID: externalID, URL: "https://example.test/jobs/" + externalID,
		Title: "Platform Engineer", CompanyName: "Example Platform", CompanyInfo: "software",
		Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid",
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return result.JobID
}

// The user asking for one job they are looking at is not the automatic spend the
// daily budget exists to cap, so the push runs with the budget already spent.
func TestProcessJobNowScoresPastAnExhaustedBudget(t *testing.T) {
	ctx := context.Background()
	scorer := &letterRunner{name: "claude", replies: []string{`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`}}
	p, db := waitingPipeline(t, scorer)
	p.MaxScorePerDay = 1
	jobID := ingestFullJob(t, p, "budget-spent")
	if _, err := p.FilterJobsWithStats(ctx, 0); err != nil {
		t.Fatalf("screen: %v", err)
	}
	if err := db.SaveAgentCall(ctx, store.AgentCallInput{Role: "scorer", Runner: "claude", Input: "prompt", Output: "result", OK: true, DurationMS: 1}); err != nil {
		t.Fatal(err)
	}

	stats, err := p.ScoreWithStats(ctx, 0)
	if err != nil || stats.Processed != 0 {
		t.Fatalf("exhausted budget must stop the batch: processed=%d err=%v", stats.Processed, err)
	}
	if pushErr := p.ProcessJobNow(ctx, jobID); pushErr != nil {
		t.Fatalf("process now: %v", pushErr)
	}
	detail, _, err := db.GetJobDetail(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "shortlisted" || detail.Score == nil {
		t.Fatalf("pushed job must be scored: state=%q score=%v", detail.Job.ProcessState, detail.Score)
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1", scorer.calls)
	}
}

// A job at `new` is carried the whole way in one push: screening queues it and
// the same call scores it, which is what "process this one now" means.
func TestProcessJobNowCarriesANewJobThroughScoring(t *testing.T) {
	ctx := context.Background()
	scorer := &letterRunner{name: "claude", replies: []string{`{"content_fit":40,"benefit_fit":40,"bonus_fit":40,"industry_fit":40,"reason":"方向不同"}`}}
	p, db := waitingPipeline(t, scorer)
	jobID := ingestFullJob(t, p, "from-new")
	if err := p.ProcessJobNow(ctx, jobID); err != nil {
		t.Fatalf("process now: %v", err)
	}
	detail, _, err := db.GetJobDetail(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "scored" {
		t.Fatalf("pushed job state = %q, want scored", detail.Job.ProcessState)
	}
}

// Nothing waiting means nothing to push: an assessed job's route back is the
// reprocess.
func TestProcessJobNowRefusesJobsItCannotAdvance(t *testing.T) {
	ctx := context.Background()
	p, db := waitingPipeline(t, &letterRunner{name: "claude"})
	jobID := ingestFullJob(t, p, "already-done")
	if err := db.TransitionProcess(ctx, jobID, "filtered_out"); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessJobNow(ctx, jobID); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("assessed job error = %v, want ErrNotWaiting", err)
	}
}

// Asking for one job to be processed is consent to assess it under the Profile
// as it stands, so a superseded revision is adopted rather than refused —
// whether the job is still waiting to be screened or already queued to score.
func TestProcessJobNowAdoptsASupersededRevision(t *testing.T) {
	ctx := context.Background()
	scorer := &letterRunner{name: "claude", replies: []string{
		`{"content_fit":40,"benefit_fit":40,"bonus_fit":40,"industry_fit":40,"reason":"方向不同"}`,
		`{"content_fit":40,"benefit_fit":40,"bonus_fit":40,"industry_fit":40,"reason":"方向不同"}`,
	}}
	p, db := waitingPipeline(t, scorer)
	active, err := p.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	stale := store.Revisions{Filter: "sha256:old", Score: "sha256:old"}

	waiting := ingestFullJob(t, p, "stale-waiting")
	if err := db.ReprocessJob(ctx, waiting, stale); err != nil {
		t.Fatal(err)
	}
	queued := ingestFullJob(t, p, "stale-queued")
	if err := db.ReprocessJob(ctx, queued, stale); err != nil {
		t.Fatal(err)
	}
	passed := store.FilterResult{
		Outcome:    store.FilterPass,
		Conditions: []store.FilterCondition{{Text: "Taipei", Kind: "hard", Category: "location", Verdict: store.FilterPass}},
		Stage:      "structural",
	}
	if err := db.SaveFilterResult(ctx, queued, passed, stale); err != nil {
		t.Fatal(err)
	}

	for _, jobID := range []int64{waiting, queued} {
		if err := p.ProcessJobNow(ctx, jobID); err != nil {
			t.Fatalf("process now job %d: %v", jobID, err)
		}
		detail, _, err := db.GetJobDetail(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Job.ProcessState != "scored" {
			t.Fatalf("job %d state = %q, want scored", jobID, detail.Job.ProcessState)
		}
		if detail.Job.ScoreRevision == nil || *detail.Job.ScoreRevision != active.Revisions.Score {
			t.Fatalf("job %d must be scored under the active revision: %v", jobID, detail.Job.ScoreRevision)
		}
	}
}

// A batch pass stops handing itself work while a push waits, so the push waits
// for the call in flight rather than for the whole pass.
func TestBatchStagesYieldToAWaitingPush(t *testing.T) {
	ctx := context.Background()
	scorer := &letterRunner{name: "claude", replies: []string{`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`}}
	p, _ := waitingPipeline(t, scorer)
	ingestFullJob(t, p, "batch-job")

	// The gate stands in for a call in flight; the goroutine is the user's push
	// queueing up behind it.
	p.Gate.Acquire()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Gate.AcquirePriority()
		p.Gate.Release()
	}()
	for !p.Gate.Yield() {
		runtime.Gosched()
	}

	stats, err := p.ScoreWithStats(ctx, 0)
	if err != nil || stats.Processed != 0 {
		t.Fatalf("batch must yield while a push waits: processed=%d err=%v", stats.Processed, err)
	}
	p.Gate.Release()
	wg.Wait()
	if scorer.calls != 0 {
		t.Fatalf("yielded batch must not have called the runner: calls=%d", scorer.calls)
	}
}

// With automatic processing off the worker leaves screening and scoring alone,
// which is the whole point of the switch: no Agent call is made until the user
// asks for one job. The letter stage is unaffected, because a letter only ever
// exists once the user has asked for it.
func TestWorkerLeavesScreeningAndScoringAloneWhenSwitchedOff(t *testing.T) {
	ctx := context.Background()
	scorer := &letterRunner{name: "claude", replies: []string{`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`}}
	p, db := waitingPipeline(t, scorer)
	jobID := ingestFullJob(t, p, "switched-off")

	p.AutoProcessing = func(context.Context) (bool, error) { return false, nil }
	worker := &Worker{Pipeline: p}
	stats, err := worker.Tick(ctx)
	if err != nil || stats.Filtered != 0 || stats.Scored != 0 {
		t.Fatalf("switched-off worker consumed jobs: %+v err=%v", stats, err)
	}
	detail, _, err := db.GetJobDetail(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "new" {
		t.Fatalf("job state = %q, want new", detail.Job.ProcessState)
	}
	if err := p.ProcessJobNow(ctx, jobID); err != nil {
		t.Fatalf("push must still run with the switch off: %v", err)
	}
	if scorer.calls != 1 {
		t.Fatalf("scorer calls = %d, want 1", scorer.calls)
	}
}

// Two paths naming the same job must not both pay for it.
func TestGateClaimIsExclusive(t *testing.T) {
	gate := &Gate{}
	if !gate.Claim(7) {
		t.Fatal("first claim must succeed")
	}
	if gate.Claim(7) {
		t.Fatal("second claim on the same job must fail")
	}
	gate.Unclaim(7)
	if !gate.Claim(7) {
		t.Fatal("claim must be available again after unclaim")
	}
}
