package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

type letterRunner struct {
	name    string
	replies []string
	calls   int
}

func (r *letterRunner) Name() string  { return r.name }
func (r *letterRunner) Model() string { return r.name }
func (r *letterRunner) Invoke(context.Context, string) (agents.Reply, error) {
	r.calls++
	if len(r.replies) == 0 {
		return agents.Reply{}, errors.New("no reply")
	}
	reply := r.replies[0]
	r.replies = r.replies[1:]
	return agents.Reply{Text: reply}, nil
}

type mockSource struct{ jobs []crawler.RawJob }

func (s mockSource) Name() string { return "yourator" }
func (s mockSource) Fetch(context.Context, crawler.SearchSpec) ([]crawler.RawJob, error) {
	return s.jobs, nil
}

func TestMockEndToEndPipeline(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "mock-e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	approved := "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"
	revise := "我使用 Go 進行服務開發。[你的姓名][你的聯絡方式]"
	scores := &letterRunner{name: "claude", replies: []string{
		`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`,
		`{"content_fit":90,"benefit_fit":90,"bonus_fit":90,"industry_fit":90,"reason":"符合方向"}`,
	}}
	drafter := &letterRunner{name: "claude", replies: []string{
		`{"letter":"` + approved + `"}`,
		`{"letter":"` + revise + `"}`,
		`{"letter":"` + revise + `"}`,
		`{"letter":"` + revise + `"}`,
	}}
	reviewer := &letterRunner{name: "codex", replies: []string{
		`{"verdict":"approve","issues":[]}`,
		`{"verdict":"revise","issues":["具體化"]}`,
		`{"verdict":"revise","issues":["精簡"]}`,
		`{"verdict":"revise","issues":["仍需修改"]}`,
	}}
	p := Pipeline{
		Store: db,
		Source: mockSource{jobs: []crawler.RawJob{
			{Source: "yourator", ExternalID: "mock-approved", URL: "https://example.test/jobs/mock-approved", Title: "Platform Engineer", CompanyName: "Example Platform", CompanyInfo: "software", Description: "Go platform work", Location: "Taipei", RemoteType: "hybrid"},
			{Source: "yourator", ExternalID: "mock-revise", URL: "https://example.test/jobs/mock-revise", Title: "Backend Engineer", CompanyName: "Example Services", CompanyInfo: "software", Description: "Go backend work", Location: "Taipei", RemoteType: "hybrid"},
		}},
		Filter:          filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable"}),
		Scorer:          agents.Scorer{Primary: scores},
		Drafter:         agents.Drafter{Primary: drafter},
		Reviewer:        agents.Reviewer{Primary: reviewer},
		ProfileYAML:     "synthetic: true",
		Profile:         syntheticProfile(),
		Weights:         [4]float64{.25, .25, .25, .25},
		Threshold:       75,
		MaxScorePerDay:  2,
		MaxLetterPerDay: 2,
		MaxLetterLength: 600,
	}
	spec := crawler.SearchSpec{Queries: []crawler.SearchQuery{{Direction: "P1", Keywords: []string{"Go"}}}, MaxPages: 1}
	fetched, runErr := p.Fetch(ctx, spec, nil)
	if runErr != nil || fetched.Fetched != 2 || fetched.New != 2 {
		t.Fatalf("Fetch = %+v, %v", fetched, runErr)
	}
	n, runErr := p.FilterJobs(ctx, 2)
	if runErr != nil || n != 2 {
		t.Fatalf("FilterJobs = %d, %v", n, runErr)
	}
	n, runErr = p.Score(ctx, 2)
	if runErr != nil || n != 2 {
		t.Fatalf("Score = %d, %v", n, runErr)
	}
	// Both jobs are recommended, and both stay put until the user asks.
	n, runErr = p.Letter(ctx, 2)
	if runErr != nil || n != 0 {
		t.Fatalf("Letter without a request = %d, %v", n, runErr)
	}
	if drafter.calls != 0 {
		t.Fatalf("drafter was called %d times without a request", drafter.calls)
	}
	shortlisted, listErr := db.ListJobs(ctx, store.JobFilter{ProcessState: "shortlisted"}, store.JobSortNewest)
	if listErr != nil || len(shortlisted) != 2 {
		t.Fatalf("shortlisted jobs = %d, %v", len(shortlisted), listErr)
	}
	for _, job := range shortlisted {
		if reqErr := p.RequestLetter(ctx, job.ID); reqErr != nil {
			t.Fatal(reqErr)
		}
	}
	n, runErr = p.Letter(ctx, 2)
	if runErr != nil || n != 2 {
		t.Fatalf("Letter = %d, %v", n, runErr)
	}
	// Both jobs end with a letter: the approved one on its first review, the other
	// as the finalized last round after the reviewer kept asking for changes.
	ready, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "letter_ready"}, store.JobSortNewest)
	if err != nil || len(ready) != 2 {
		t.Fatalf("letter_ready jobs = %d, %v", len(ready), err)
	}
	failed, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "letter_failed"}, store.JobSortNewest)
	if err != nil || len(failed) != 0 {
		t.Fatalf("letter_failed jobs = %d, %v", len(failed), err)
	}
	statuses := map[string]int{}
	for _, job := range ready {
		detail, _, detailErr := db.GetJobDetail(ctx, job.ID)
		if detailErr != nil || detail.Letter == nil {
			t.Fatalf("letter detail for job %d = %v, %v", job.ID, detail.Letter, detailErr)
		}
		if detail.Letter.Content == "" {
			t.Fatalf("job %d kept a %s letter with no content", job.ID, detail.Letter.Status)
		}
		statuses[detail.Letter.Status]++
	}
	if statuses["approved"] != 1 || statuses["finalized"] != 1 {
		t.Fatalf("letter statuses = %v", statuses)
	}
	if repeat, err := p.Fetch(ctx, spec, nil); err != nil || repeat.New != 0 || repeat.Fetched != 2 {
		t.Fatalf("repeat Fetch = %+v, %v", repeat, err)
	}
	for _, stage := range []func(context.Context, int) (int, error){p.FilterJobs, p.Score, p.Letter} {
		if n, err := stage(ctx, 2); err != nil || n != 0 {
			t.Fatalf("repeat stage = %d, %v", n, err)
		}
	}
	// Four drafts, three reviews: the third round of the second job is drafted from
	// the accumulated history and kept without a further review.
	if scores.calls != 2 || drafter.calls != 4 || reviewer.calls != 3 {
		t.Fatalf("agent calls score/draft/review = %d/%d/%d, want 2/4/3", scores.calls, drafter.calls, reviewer.calls)
	}
}

func TestLetterApprovesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	description := "Go platform work"
	job, err := db.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "synthetic-letter", URL: "https://example.test/jobs/synthetic-letter", Title: "Platform Engineer", CompanyName: "Example Platform", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted", "letter_requested"} {
		if transitionErr := db.TransitionProcess(ctx, job.Job.ID, state); transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}
	letter := "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"
	p := Pipeline{Store: db, ProfileYAML: "synthetic: true", Profile: syntheticProfile(), MaxLetterPerDay: 1, MaxLetterLength: 600, Drafter: agents.Drafter{Primary: &letterRunner{name: "claude", replies: []string{`{"letter":"` + letter + `"}`}}}, Reviewer: agents.Reviewer{Primary: &letterRunner{name: "codex", replies: []string{`{"verdict":"approve","issues":[]}`}}}}
	n, err := p.Letter(ctx, 1)
	if err != nil || n != 1 {
		t.Fatalf("Letter = %d, %v", n, err)
	}
	jobs, err := db.ListJobs(ctx, store.JobFilter{ProcessState: "letter_ready"}, store.JobSortNewest)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ready jobs = %v, %v", jobs, err)
	}
	n, err = p.Letter(ctx, 1)
	if err != nil || n != 0 {
		t.Fatalf("second Letter = %d, %v", n, err)
	}
}

// A letter job whose Agent call fails leaves `letter_requested` instead of
// staying at the head of the queue: the stage picks by `updated_at`, so a job
// that can never succeed would otherwise block every other request and spend a
// day's budget by itself.
func TestLetterFailedCallEndsTheJobAndUnblocksTheQueue(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	description := "Go platform work"
	ids := make([]int64, 0, 2)
	for _, external := range []string{"letter-broken", "letter-fine"} {
		job, upsertErr := db.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: external, URL: "https://example.test/jobs/" + external, Title: "Platform Engineer", CompanyName: "Example Platform", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
		if upsertErr != nil {
			t.Fatal(upsertErr)
		}
		for _, state := range []string{"queued", "shortlisted", "letter_requested"} {
			if transitionErr := db.TransitionProcess(ctx, job.Job.ID, state); transitionErr != nil {
				t.Fatal(transitionErr)
			}
		}
		ids = append(ids, job.Job.ID)
	}
	letter := "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"
	// The first job exhausts the reviewer's runners; the second is reviewed and
	// approved in the same batch.
	drafter := &letterRunner{name: "claude", replies: []string{`{"letter":"` + letter + `"}`, `{"letter":"` + letter + `"}`}}
	reviewer := &letterRunner{name: "codex", replies: []string{"not json", "not json", `{"verdict":"approve","issues":[]}`}}
	p := Pipeline{Store: db, ProfileYAML: "synthetic: true", Profile: syntheticProfile(), MaxLetterLength: 600, MaxLetterRounds: 3, Drafter: agents.Drafter{Primary: drafter}, Reviewer: agents.Reviewer{Primary: reviewer}}
	// The stage does not fail: both jobs reached a terminal state, and the failed
	// one is diagnosable from its letter row and its audited calls.
	stats, err := p.LetterWithStats(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LettersOK != 1 || stats.LettersFail != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	failed, _, err := db.GetJobDetail(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if failed.Job.ProcessState != "letter_failed" {
		t.Fatalf("failed job state = %q", failed.Job.ProcessState)
	}
	if failed.Letter != nil {
		t.Fatalf("a failed run recorded a letter: %+v", failed.Letter)
	}
	ready, _, err := db.GetJobDetail(ctx, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if ready.Job.ProcessState != "letter_ready" {
		t.Fatalf("second job state = %q, it was blocked by the first", ready.Job.ProcessState)
	}
	// Neither job is picked up again: both left `letter_requested`.
	if n, repeatErr := p.Letter(ctx, 2); repeatErr != nil || n != 0 {
		t.Fatalf("repeat Letter = %d, %v", n, repeatErr)
	}
}

func TestLetterRoundsComeFromConfiguration(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	description := "Go platform work"
	job, err := db.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "letter-rounds", URL: "https://example.test/jobs/letter-rounds", Title: "Platform Engineer", CompanyName: "Example Platform", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted", "letter_requested"} {
		if transitionErr := db.TransitionProcess(ctx, job.Job.ID, state); transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}
	letter := "我使用 Go 建立可靠服務。[你的姓名][你的聯絡方式]"
	drafter := &letterRunner{name: "claude", replies: []string{`{"letter":"` + letter + `"}`, `{"letter":"` + letter + `"}`}}
	reviewer := &letterRunner{name: "codex", replies: []string{`{"verdict":"revise","issues":["精簡"]}`}}
	p := Pipeline{Store: db, ProfileYAML: "synthetic: true", Profile: syntheticProfile(), MaxLetterLength: 600, MaxLetterRounds: 2, Drafter: agents.Drafter{Primary: drafter}, Reviewer: agents.Reviewer{Primary: reviewer}}
	if n, letterErr := p.Letter(ctx, 1); letterErr != nil || n != 1 {
		t.Fatalf("Letter = %d, %v", n, letterErr)
	}
	if drafter.calls != 2 || reviewer.calls != 1 {
		t.Fatalf("draft/review calls = %d/%d, want 2/1 for a two-round limit", drafter.calls, reviewer.calls)
	}
	detail, _, err := db.GetJobDetail(ctx, job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Job.ProcessState != "letter_ready" || detail.Letter == nil || detail.Letter.Status != "finalized" || detail.Letter.Rounds != 2 {
		t.Fatalf("job state %q, letter %+v", detail.Job.ProcessState, detail.Letter)
	}
}

// The screening rules come from `requirements` alone; `search` only states the
// directions a job is looked for under and must not narrow what passes.
func TestFilterFromProfileUsesRequirements(t *testing.T) {
	p := profile.Profile{
		Search: profile.Search{Directions: []profile.Direction{{Key: "P1", Title: "cloud", Keywords: []string{"cloud"}}}},
		Requirements: profile.Requirements{
			SalaryMin: 80000, Locations: []string{"taipei"}, Remote: "acceptable",
			ExcludeTitleKeywords: []string{"intern"}, ExcludeDescriptionKeywords: []string{"shift"},
			ExcludeCompanies: []string{"agency"},
		},
	}
	f := FilterFromProfile(p)
	if f.Requirements.SalaryMin != 80000 || len(f.Requirements.Locations) != 1 || f.Requirements.Locations[0] != "taipei" {
		t.Fatalf("unexpected filter: %+v", f)
	}
	description := "regular day shift"
	job := store.Job{Title: "Backend Intern", CompanyName: "Example", Location: "Taipei", RemoteType: "onsite", Description: &description}
	hits := f.Match(job)
	if len(hits) != 2 {
		t.Fatalf("hits = %v, want the title and description rules", hits)
	}
	// An undisclosed salary is recorded as undecided, never as a rejection. What
	// that undecided condition then means for the job is the aggregate's call,
	// covered by TestUndecidedConditionsOnlyHoldBackListExcerpts.
	for _, condition := range f.Evaluate(job, false) {
		if condition.Text == "salary_floor" && condition.Verdict != store.FilterUnknown {
			t.Fatalf("undisclosed salary judged %q", condition.Verdict)
		}
	}
}
