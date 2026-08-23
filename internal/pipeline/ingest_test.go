package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// filterFor builds the structural filter from the hard rules alone, which is
// the only place screening rules come from.
func filterFor(requirements profile.Requirements) Filter { return Filter{Requirements: requirements} }

// syntheticProfile is the minimal Profile the stages need: a skill for the
// letter guard and a remote preference for the screening matrix.
func syntheticProfile() profile.Profile {
	return profile.Profile{
		Requirements:   profile.Requirements{Remote: "acceptable"},
		Qualifications: profile.Qualifications{Skills: []profile.SkillEntry{{Name: "Go", Level: "expert"}}},
	}
}

func openPipeline(t *testing.T, filter Filter) (Pipeline, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Pipeline{Store: db, Filter: filter, Profile: syntheticProfile(), Threshold: 75, Weights: [4]float64{.25, .25, .25, .25}}, db
}

func TestIngestListScreensNewPartialJobs(t *testing.T) {
	p, _ := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
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

// A list-page rejection is a conclusion, not a provisional guess: capturing the
// full JD of a screened-out job keeps its content current and its verdict as it
// was, so no Agent is ever paid to re-open a decision the hard rules made.
func TestCapturingTheJDOfAScreenedOutJobKeepsTheRejection(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	ctx := context.Background()
	listRow := crawler.RawJob{Source: "104", ExternalID: "drop", URL: "https://www.104.com.tw/job/drop", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Location: "Kaohsiung", RemoteType: "unknown"}
	if _, err := p.IngestList(ctx, []crawler.RawJob{listRow}); err != nil {
		t.Fatal(err)
	}

	detailRow := listRow
	detailRow.Description = "Go platform work"
	detailRow.Location = "Taipei"
	result, err := p.IngestJob(ctx, detailRow)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcessState != "filtered_out" || len(result.FilterHits) == 0 {
		t.Fatalf("captured job = %+v, want the rejection and its hits to stand", result)
	}
	pending, err := db.PickForStage(ctx, "filter", store.Revisions{}, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("a screened-out job must reach no stage: %d %v", len(pending), err)
	}
	detail, _, err := db.GetJobDetail(ctx, result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.Description == nil {
		t.Fatal("the captured JD must still be stored")
	}
}

func TestIngestJobPassesScreeningAndLeavesScoringToWorker(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "full", URL: "https://www.104.com.tw/job/full", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	// Only a structural rejection is conclusive on the capture path. A job that
	// passes them still owes the semantic conditions, which the worker runs.
	if result.ProcessState != "new" || result.Score != nil {
		t.Fatalf("captured job = %+v, want a job awaiting semantic screening", result)
	}
	pending, err := db.PickForStage(ctx, "filter", store.Revisions{}, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("jobs awaiting screening = %d, %v", len(pending), err)
	}
}

func TestIngestJobReturnsCachedScore(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Remote: "acceptable"}))
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "cached", URL: "https://www.104.com.tw/job/cached", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	first, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.FilterJobsWithStats(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err = db.TransitionProcess(ctx, first.JobID, "scored"); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveScore(ctx, store.ScoreInput{JobID: first.JobID, Content: 50, Benefit: 50, Bonus: 50, Industry: 50, Total: 50, Reason: "ok", Runner: "claude"}); err != nil {
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

// A manual reprocess is the one way back for a verdict the user disagrees with:
// it returns the job to screening under the active revisions, hits cleared.
func TestRequestReprocessReturnsAScreenedOutJobToScreening(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "drop", URL: "https://www.104.com.tw/job/drop", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Kaohsiung", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcessState != "filtered_out" {
		t.Fatalf("captured job = %+v, want filtered_out", result)
	}

	if reprocessErr := p.RequestReprocess(ctx, result.JobID); reprocessErr != nil {
		t.Fatal(reprocessErr)
	}
	detail, _, err := db.GetJobDetail(ctx, result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "new" || detail.Job.FilterHits != nil || detail.Filter != nil {
		t.Fatalf("reprocessed job = %+v hits=%+v filter=%+v", detail.Job.ProcessState, detail.Job.FilterHits, detail.Filter)
	}
	pending, err := db.PickForStage(ctx, "filter", store.Revisions{}, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("reprocessed job must await screening: %d %v", len(pending), err)
	}
}

func TestRequestLetterIsIdempotent(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Remote: "acceptable"}))
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "req", URL: "https://www.104.com.tw/job/req", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.FilterJobsWithStats(ctx, 0); err != nil {
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

func TestRequestLetterRerunsAReadyLetter(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Remote: "acceptable"}))
	ctx := context.Background()
	row := crawler.RawJob{Source: "104", ExternalID: "again", URL: "https://www.104.com.tw/job/again", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
	result, err := p.IngestJob(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.FilterJobsWithStats(ctx, 0); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"shortlisted", "letter_requested", "letter_ready"} {
		if err = db.TransitionProcess(ctx, result.JobID, state); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if err = p.RequestLetter(ctx, result.JobID); err != nil {
		t.Fatalf("a ready letter could not be requested again: %v", err)
	}
	detail, _, err := db.GetJobDetail(ctx, result.JobID)
	if err != nil || detail.Job.ProcessState != "letter_requested" {
		t.Fatalf("job state = %q, %v", detail.Job.ProcessState, err)
	}
}

func TestScoreBudgetStopsAtDailyLimit(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Remote: "acceptable"}))
	day := time.Date(2026, 7, 14, 9, 0, 0, 0, taipei)
	p.Now = func() time.Time { return day }
	p.MaxScorePerDay = 1
	p.Scorer = agents.Scorer{Primary: &letterRunner{name: "claude", replies: []string{
		`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"fit"}`,
		`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"fit"}`,
	}}}
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		row := crawler.RawJob{Source: "104", ExternalID: id, URL: "https://www.104.com.tw/job/" + id, Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"}
		if _, err := p.IngestJob(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.FilterJobsWithStats(ctx, 0); err != nil {
		t.Fatal(err)
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

// A job waiting to be screened carries no verdict, so a Profile change costs it
// nothing: the worker adopts the active revision and screens it. A job queued
// on a superseded screening does carry one, and buying it again is the user's
// call — the score stage leaves it where it is and calls no Agent for it.
func TestWorkerAdoptsStaleScreeningButLeavesQueuedJobsForReprocess(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Remote: "acceptable"}))
	runner := &letterRunner{name: "claude", replies: []string{`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"fit"}`}}
	p.Scorer = agents.Scorer{Primary: runner}
	ctx := context.Background()
	description := "Go platform work"
	for _, input := range []store.JobInput{
		{Source: "104", ExternalID: "stale-filter", URL: "https://www.104.com.tw/job/stale-filter", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid", FilterRevision: "sha256:old"},
		{Source: "104", ExternalID: "stale-score", URL: "https://www.104.com.tw/job/stale-score", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid", FilterRevision: "sha256:old"},
	} {
		created, err := db.UpsertJob(ctx, input, nil)
		if err != nil {
			t.Fatal(err)
		}
		if input.ExternalID == "stale-score" {
			if err := db.TransitionProcess(ctx, created.Job.ID, "queued"); err != nil {
				t.Fatal(err)
			}
		}
	}

	active, err := p.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := p.FilterJobsWithStats(ctx, 0)
	if err != nil || filtered.Processed != 1 || filtered.Queued != 1 {
		t.Fatalf("stale filter processed=%d queued=%d err=%v", filtered.Processed, filtered.Queued, err)
	}
	scored, err := p.ScoreWithStats(ctx, 0)
	if err != nil || scored.Processed != 1 || runner.calls != 1 {
		t.Fatalf("stale score processed=%d calls=%d err=%v", scored.Processed, runner.calls, err)
	}
	waiting, err := db.CountAwaitingReprocess(ctx, active.Revisions)
	if err != nil || waiting != 1 {
		t.Fatalf("jobs awaiting reprocess = %d (%v), want the one queued on a superseded screening", waiting, err)
	}
	held, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "queued"}, store.JobSortNewest)
	if err != nil || len(held) != 1 || held[0].ExternalID != "stale-score" {
		t.Fatalf("only the job queued on a superseded screening stays put: %+v (%v)", held, err)
	}
}

func TestWorkerConsumesEveryStage(t *testing.T) {
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}))
	p.Scorer = agents.Scorer{Primary: &letterRunner{name: "claude", replies: []string{`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"fit"}`}}}
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
