package pipeline

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// orderedRunner records the order the roles were called in. The reply it gives
// is decided by the prompt, so one runner can stand in for both roles.
type orderedRunner struct {
	order  []string
	filter string
	score  string
}

func (r *orderedRunner) Name() string  { return "claude" }
func (r *orderedRunner) Model() string { return "claude" }
func (r *orderedRunner) Invoke(_ context.Context, prompt string) (agents.Reply, error) {
	if strings.Contains(prompt, "硬條件篩選器") {
		r.order = append(r.order, "filter")
		return agents.Reply{Text: r.filter}, nil
	}
	r.order = append(r.order, "score")
	return agents.Reply{Text: r.score}, nil
}

// One pass scores what is already screened before screening anything new, and
// then carries each newly screened job straight on to its own score. A job the
// user is waiting on therefore reaches a verdict in the two calls it takes,
// rather than after the whole screening batch.
func TestWorkerScoresQueuedFirstThenCarriesEachScreenedJobToItsScore(t *testing.T) {
	ctx := context.Background()
	runner := &orderedRunner{
		filter: `{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`,
		score:  `{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`,
	}
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	p.Screener, p.Scorer, p.Gate = agents.Filter{Primary: runner}, agents.Scorer{Primary: runner}, &Gate{}
	snapshot, snapshotErr := p.snapshot()
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}

	// One job already screened and waiting for its score, one still to screen.
	if _, err := p.IngestJob(ctx, captureRow("already-queued", "Platform Engineer", "Taipei")); err != nil {
		t.Fatal(err)
	}
	waiting, listErr := db.ListJobs(ctx, store.JobFilter{ProcessState: "new"}, store.JobSortNewest)
	if listErr != nil || len(waiting) != 1 {
		t.Fatalf("listed %d jobs waiting to be screened (%v)", len(waiting), listErr)
	}
	passed := store.FilterResult{
		Outcome:    store.FilterPass,
		Conditions: []store.FilterCondition{{Text: "Taipei", Kind: "required", Group: 1, Category: "other", Verdict: store.FilterPass}},
		Stage:      "structural",
	}
	if err := db.SaveFilterResult(ctx, waiting[0].ID, passed, snapshot.Revisions); err != nil {
		t.Fatal(err)
	}
	if _, err := p.IngestJob(ctx, captureRow("to-screen", "Platform Engineer", "Taipei")); err != nil {
		t.Fatal(err)
	}

	worker := &Worker{Pipeline: p}
	stats, err := worker.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Filtered != 1 || stats.Scored != 2 {
		t.Fatalf("worker pass = %+v, want one screened and two scored", stats)
	}
	if want := []string{"score", "filter", "score"}; strings.Join(runner.order, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", runner.order, want)
	}
}

// "Score everything waiting first" has to hold for a queue larger than one
// scoring pass takes, which is the case a backlog produces: one pass is capped
// at stageBatch, and screening must still wait for the passes after it.
func TestWorkerDrainsAQueueLargerThanOneScoringPassBeforeScreening(t *testing.T) {
	ctx := context.Background()
	runner := &orderedRunner{
		filter: `{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`,
		score:  `{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`,
	}
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	p.Screener, p.Scorer, p.Gate = agents.Filter{Primary: runner}, agents.Scorer{Primary: runner}, &Gate{}
	snapshot, snapshotErr := p.snapshot()
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}

	passed := store.FilterResult{
		Outcome:    store.FilterPass,
		Conditions: []store.FilterCondition{{Text: "Taipei", Kind: "required", Group: 1, Category: "other", Verdict: store.FilterPass}},
		Stage:      "structural",
	}
	waiting := stageBatch + 1
	for i := 0; i < waiting; i++ {
		result, err := p.IngestJob(ctx, captureRow(fmt.Sprintf("queued-%d", i), "Platform Engineer", "Taipei"))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveFilterResult(ctx, result.JobID, passed, snapshot.Revisions); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.IngestJob(ctx, captureRow("to-screen", "Platform Engineer", "Taipei")); err != nil {
		t.Fatal(err)
	}

	worker := &Worker{Pipeline: p}
	stats, err := worker.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Filtered != 1 || stats.Scored != waiting+1 {
		t.Fatalf("worker pass = %+v, want one screened and %d scored", stats, waiting+1)
	}
	first := slices.Index(runner.order, "filter")
	if first != waiting {
		t.Fatalf("screening started at call %d, want it after all %d queued jobs were scored", first, waiting)
	}
}

// The switch is the token brake, so it has to bite while a pass is running: a
// pass can hold dozens of jobs and take an hour, and a user who has just turned
// it off must not keep paying for the rest of that pass.
func TestSwitchingAutomaticProcessingOffStopsThePassInFlight(t *testing.T) {
	ctx := context.Background()
	runner := &orderedRunner{
		filter: `{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`,
		score:  `{"content_fit":40,"benefit_fit":40,"bonus_fit":40,"industry_fit":40,"reason":"方向不同"}`,
	}
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	p.Screener, p.Scorer, p.Gate = agents.Filter{Primary: runner}, agents.Scorer{Primary: runner}, &Gate{}
	// The switch reads on until the first job of the pass has been taken, which
	// stands in for the user flipping it off while the pass runs.
	var reads atomic.Int64
	p.AutoProcessing = func(context.Context) (bool, error) { return reads.Add(1) <= 1, nil }

	for _, externalID := range []string{"first", "second", "third"} {
		if _, err := p.IngestJob(ctx, captureRow(externalID, "Platform Engineer", "Taipei")); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := p.FilterJobsWithStats(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Processed != 1 {
		t.Fatalf("screened %d jobs after the switch went off, want 1", stats.Processed)
	}
	left, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "new"}, store.JobSortNewest)
	if err != nil || len(left) != 2 {
		t.Fatalf("jobs left waiting = %d (%v), want the two the pass never took", len(left), err)
	}
}
