package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/store"
)

func openPipeline(t *testing.T, filter Filter) (Pipeline, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Pipeline{Store: db, Filter: filter, Threshold: 75, Weights: [5]float64{.2, .2, .2, .2, .2}}, db
}

func TestIngestListScreensNewPartialJobs(t *testing.T) {
	p, _ := openPipeline(t, Filter{Locations: []string{"Taipei"}})
	ctx := context.Background()
	rows := []crawler.RawJob{
		{Source: "104", ExternalID: "keep", URL: "https://www.104.com.tw/job/keep", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Location: "Taipei", RemoteType: "unknown"},
		{Source: "104", ExternalID: "drop", URL: "https://www.104.com.tw/job/drop", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Location: "Kaohsiung", RemoteType: "unknown"},
	}
	results, err := p.IngestList(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].ProcessState != "discovered" || !results[0].Created {
		t.Fatalf("kept job = %+v", results[0])
	}
	if results[1].ProcessState != "filtered_out" || len(results[1].FilterHits) == 0 {
		t.Fatalf("dropped job = %+v", results[1])
	}
	// A second capture of an existing job re-reports its state and never
	// re-runs a stage.
	again, err := p.IngestList(ctx, rows[:1])
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Created || again[0].ProcessState != "discovered" {
		t.Fatalf("re-captured job = %+v", again[0])
	}
}

func TestIngestJobPassesScreeningAndLeavesScoringToWorker(t *testing.T) {
	p, db := openPipeline(t, Filter{Locations: []string{"Taipei"}})
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "full", URL: "https://www.104.com.tw/job/full", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcessState != "queued" || result.Score != nil {
		t.Fatalf("captured job = %+v, want queued with no score", result)
	}
	// The worker's score stage is what advances it; capture leaves it queued.
	queued, err := db.PickForStage(ctx, "score", 10)
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued jobs = %d, %v", len(queued), err)
	}
}

func TestIngestJobReturnsCachedScore(t *testing.T) {
	p, db := openPipeline(t, Filter{})
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "cached", URL: "https://www.104.com.tw/job/cached", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	first, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionProcess(ctx, first.JobID, "scored"); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveScore(ctx, store.ScoreInput{JobID: first.JobID, HardSkill: 50, Domain: 50, Seniority: 50, Condition: 50, Direction: 50, Total: 50, Reason: "ok", Runner: "claude"}); err != nil {
		t.Fatal(err)
	}
	second, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.Score == nil || second.ProcessState != "scored" {
		t.Fatalf("cached capture = %+v", second)
	}
}

func TestRequestLetterIsIdempotent(t *testing.T) {
	p, db := openPipeline(t, Filter{})
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "req", URL: "https://www.104.com.tw/job/req", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionProcess(ctx, result.JobID, "shortlisted"); err != nil {
		t.Fatal(err)
	}
	if err = p.RequestLetter(ctx, result.JobID); err != nil {
		t.Fatal(err)
	}
	if err = p.RequestLetter(ctx, result.JobID); err != nil {
		t.Fatalf("second request was not idempotent: %v", err)
	}
	detail, _, err := db.GetJobDetail(ctx, result.JobID)
	if err != nil || detail.Job.ProcessState != "letter_requested" {
		t.Fatalf("job state = %q, %v", detail.Job.ProcessState, err)
	}
}

func TestScoreBudgetStopsAtDailyLimit(t *testing.T) {
	p, db := openPipeline(t, Filter{})
	day := time.Date(2026, 7, 14, 9, 0, 0, 0, taipei)
	p.Now = func() time.Time { return day }
	p.MaxScorePerDay = 1
	p.Scorer = agents.Scorer{Primary: &letterRunner{name: "claude", replies: []string{
		`{"hard_skill":90,"domain":90,"seniority":90,"condition":90,"direction":90,"reason":"fit"}`,
		`{"hard_skill":90,"domain":90,"seniority":90,"condition":90,"direction":90,"reason":"fit"}`,
	}}}
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		row := crawler.RawJob{Source: "104", ExternalID: id, URL: "https://www.104.com.tw/job/" + id, Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
		if _, err := p.IngestJob(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := p.ScoreWithStats(ctx, 0)
	if err != nil || stats.Processed != 1 {
		t.Fatalf("scored %d under a budget of 1 (%v)", stats.Processed, err)
	}
	// The budget is used up, so the next pass picks nothing up.
	stats, err = p.ScoreWithStats(ctx, 0)
	if err != nil || stats.Processed != 0 {
		t.Fatalf("scored %d after the budget was spent (%v)", stats.Processed, err)
	}
	remaining, limited, err := p.ScoreBudgetRemaining(ctx)
	if err != nil || !limited || remaining != 0 {
		t.Fatalf("remaining budget = %d limited=%v (%v)", remaining, limited, err)
	}
	_ = db
}

func TestWorkerConsumesEveryStage(t *testing.T) {
	p, db := openPipeline(t, Filter{Locations: []string{"Taipei"}})
	p.Scorer = agents.Scorer{Primary: &letterRunner{name: "claude", replies: []string{`{"hard_skill":90,"domain":90,"seniority":90,"condition":90,"direction":90,"reason":"fit"}`}}}
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "w1", URL: "https://www.104.com.tw/job/w1", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	if _, err := p.IngestList(ctx, []crawler.RawJob{{Source: "104", ExternalID: "w1", URL: row.URL, Title: row.Title, CompanyName: row.CompanyName, CompanyInfo: row.CompanyInfo, Location: row.Location, RemoteType: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	// Bring the partial job to `new` so the worker's filter stage can take it.
	if _, err := p.IngestJob(ctx, row); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{Pipeline: p}
	stats, err := worker.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Scored != 1 {
		t.Fatalf("worker scored %d jobs in one tick", stats.Scored)
	}
	shortlisted, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "shortlisted"}, store.JobSortNewest)
	if err != nil || len(shortlisted) != 1 {
		t.Fatalf("shortlisted = %d, %v", len(shortlisted), err)
	}
}
