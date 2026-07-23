// Package pipeline orchestrates scheduled fetching and the resident worker that
// consumes the filter, score, and letter stages.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // daily budgets reset on the Taipei day boundary regardless of host tz data

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// stageBatch caps one stage pass when no daily budget narrows it further.
const stageBatch = 50

var taipei = mustLoadTaipei()

func mustLoadTaipei() *time.Location {
	location, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		panic(fmt.Sprintf("pipeline: load Asia/Taipei: %v", err))
	}
	return location
}

type Filter struct {
	ExcludeTitleKeywords, ExcludeBodyKeywords, RequireAnyKeywords, Locations, ExcludeCompanies []string
	SalaryFloor                                                                                int
}

// FilterFromProfile derives every deterministic screening rule from Profile.
// Job-search preferences intentionally have no duplicate config representation.
func FilterFromProfile(p profile.Profile) Filter {
	return Filter{
		ExcludeTitleKeywords: p.Preferences.Screening.ExcludeTitleKeywords,
		ExcludeBodyKeywords:  p.Preferences.Screening.ExcludeDescriptionKeywords,
		RequireAnyKeywords:   p.Preferences.Screening.RequireAnyKeywords,
		ExcludeCompanies:     p.Preferences.Screening.ExcludeCompanies,
		Locations:            p.Preferences.Locations,
		SalaryFloor:          p.Preferences.SalaryMin,
	}
}

func (f Filter) Match(j store.Job) []string {
	hits := []string{}
	contains := func(value string, terms []string) bool {
		value = strings.ToLower(value)
		for _, term := range terms {
			if strings.Contains(value, strings.ToLower(term)) {
				return true
			}
		}
		return false
	}
	if contains(j.Title, f.ExcludeTitleKeywords) {
		hits = append(hits, "exclude_title_keywords")
	}
	if contains(j.CompanyName, f.ExcludeCompanies) {
		hits = append(hits, "exclude_companies")
	}
	if j.SalaryMax != nil && *j.SalaryMax < f.SalaryFloor {
		hits = append(hits, "salary_floor")
	}
	if j.RemoteType != "remote" && len(f.Locations) > 0 && !contains(j.Location, f.Locations) {
		hits = append(hits, "locations")
	}
	if j.Description != nil {
		if contains(*j.Description, f.ExcludeBodyKeywords) {
			hits = append(hits, "exclude_body_keywords")
		}
		if len(f.RequireAnyKeywords) > 0 && !contains(j.Title+"\n"+*j.Description, f.RequireAnyKeywords) {
			hits = append(hits, "require_any_keywords")
		}
	}
	return hits
}

type Pipeline struct {
	Store           *store.Store
	Source          crawler.Source
	Provider        *profile.Provider
	Filter          Filter
	Scorer          agents.Scorer
	Drafter         agents.Drafter
	Reviewer        agents.Reviewer
	ProfileYAML     string
	Profile         profile.Profile
	Denylist        []string
	Weights         [5]float64
	Threshold       float64
	MaxScorePerDay  int
	MaxLetterPerDay int
	MaxLetterLength int
	MinInterval     time.Duration
	// Now supplies the clock the Taipei day boundary is derived from.
	Now func() time.Time
}

type workProfile struct {
	Value    profile.Profile
	YAML     string
	Revision string
	Filter   Filter
}

func (p Pipeline) snapshot() (workProfile, error) {
	if p.Provider != nil {
		snapshot, err := p.Provider.Ready()
		if err != nil {
			return workProfile{}, err
		}
		return workProfile{Value: *snapshot.Profile, YAML: snapshot.YAML, Revision: snapshot.Revision, Filter: FilterFromProfile(*snapshot.Profile)}, nil
	}
	revision, _ := profile.Revision(p.Profile)
	return workProfile{Value: p.Profile, YAML: p.ProfileYAML, Revision: revision, Filter: p.Filter}, nil
}

func (p Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// ScoreBudgetRemaining reports how many scoring calls today's budget still
// allows; limited is false when no cap is configured.
func (p Pipeline) ScoreBudgetRemaining(ctx context.Context) (int, bool, error) {
	return p.budgetRemaining(ctx, "scorer", p.MaxScorePerDay)
}

// LetterBudgetRemaining reports how many letter drafts today's budget still
// allows; limited is false when no cap is configured.
func (p Pipeline) LetterBudgetRemaining(ctx context.Context) (int, bool, error) {
	return p.budgetRemaining(ctx, "drafter", p.MaxLetterPerDay)
}

// budgetRemaining derives the allowance from the audited calls of the day
// rather than a stored counter, so restarts neither reset nor lose it.
func (p Pipeline) budgetRemaining(ctx context.Context, role string, max int) (int, bool, error) {
	if max <= 0 {
		return 0, false, nil
	}
	used, err := p.Store.CountAgentCallsSince(ctx, role, taipeiDayStart(p.now()))
	if err != nil {
		return 0, true, err
	}
	remaining := max - used
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true, nil
}

func taipeiDayStart(at time.Time) time.Time {
	local := at.In(taipei)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, taipei)
}

// stageLimit narrows a caller's limit to the remaining budget; ok is false when
// the budget is exhausted and the stage must not pick anything up.
func stageLimit(limit, remaining int, limited bool) (int, bool) {
	if limited {
		if remaining <= 0 {
			return 0, false
		}
		if limit <= 0 || limit > remaining {
			limit = remaining
		}
	}
	if limit <= 0 {
		limit = stageBatch
	}
	return limit, true
}

// StageStats distinguishes work attempted from the business outcomes persisted by a stage.
type StageStats struct {
	Processed   int
	FilteredOut int
	Shortlisted int
	LettersOK   int
	LettersFail int
}

func (p Pipeline) Letter(ctx context.Context, limit int) (int, error) {
	stats, err := p.LetterWithStats(ctx, limit)
	return stats.Processed, err
}

// LetterWithStats drafts letters for the jobs a user has requested. Jobs that
// stay `shortlisted` are never picked up, so an unrequested job costs nothing.
func (p Pipeline) LetterWithStats(ctx context.Context, limit int) (StageStats, error) {
	if _, err := p.snapshot(); err != nil {
		return StageStats{}, err
	}
	remaining, limited, err := p.LetterBudgetRemaining(ctx)
	if err != nil {
		return StageStats{}, err
	}
	limit, ok := stageLimit(limit, remaining, limited)
	if !ok {
		return StageStats{}, nil
	}
	jobs, err := p.Store.PickForStage(ctx, "letter", limit)
	if err != nil {
		return StageStats{}, err
	}
	stats := StageStats{}
	var failures []error
	for i, job := range jobs {
		snapshot, snapshotErr := p.snapshot()
		if snapshotErr != nil {
			return stats, snapshotErr
		}
		if i > 0 && p.MinInterval > 0 {
			select {
			case <-ctx.Done():
				return stats, ctx.Err()
			case <-time.After(p.MinInterval):
			}
		}
		jobID := job.ID
		audit := func(role, runner, input, output string, ok bool, duration time.Duration) error {
			return p.Store.SaveAgentCall(ctx, store.AgentCallInput{JobID: &jobID, Role: role, Runner: runner, Input: input, Output: output, OK: ok, DurationMS: duration.Milliseconds(), ProfileRevision: snapshot.Revision})
		}
		drafter, reviewer := p.Drafter, p.Reviewer
		drafter.Audit, reviewer.Audit = audit, audit
		desc := ""
		if job.Description != nil {
			desc = *job.Description
		}
		result, e := agents.GenerateLetter(ctx, drafter, reviewer, snapshot.YAML, snapshot.Value, agents.Job{Title: job.Title, CompanyName: job.CompanyName, Description: desc, Location: job.Location, RemoteType: job.RemoteType, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax}, p.Denylist, p.MaxLetterLength)
		if e != nil {
			if ctx.Err() != nil {
				return stats, e
			}
			failures = append(failures, fmt.Errorf("letter job %d: %w", job.ID, e))
			continue
		}
		if result.Status == "failed" {
			result.Content = "[你的姓名]\n[你的聯絡方式]"
		}
		if err := p.Store.SaveLetter(ctx, store.LetterInput{JobID: job.ID, Content: result.Content, Status: result.Status, ReviewLog: result.ReviewLog, Rounds: result.Rounds, RunnerDraft: result.DraftRunner, RunnerReview: result.ReviewRunner, ProfileRevision: snapshot.Revision}); err != nil {
			return stats, err
		}
		state := "letter_failed"
		if result.Status == "approved" {
			state = "letter_ready"
			stats.LettersOK++
		} else {
			stats.LettersFail++
		}
		if err := p.Store.TransitionProcess(ctx, job.ID, state); err != nil {
			return stats, err
		}
		stats.Processed++
	}
	return stats, errors.Join(failures...)
}

// FetchStats are the fetch facts one run records.
type FetchStats struct{ Fetched, New int }

// Fetch stores every job one search spec returns and attributes new jobs to the
// run. It performs no filtering or scoring: the resident worker consumes those.
func (p Pipeline) Fetch(ctx context.Context, spec crawler.SearchSpec, runID *int64) (FetchStats, error) {
	if p.Store == nil || p.Source == nil {
		return FetchStats{}, fmt.Errorf("pipeline: store and source are required")
	}
	snapshot, err := p.snapshot()
	if err != nil {
		return FetchStats{}, err
	}
	rows, err := p.Source.Fetch(ctx, spec)
	if err != nil {
		return FetchStats{}, err
	}
	stats := FetchStats{}
	for _, r := range rows {
		result, e := p.Store.UpsertJob(ctx, jobInput(r, snapshot.Revision), runID)
		if e != nil {
			return stats, e
		}
		stats.Fetched++
		if result.Created {
			stats.New++
		}
	}
	return stats, nil
}

func jobInput(r crawler.RawJob, revision string) store.JobInput {
	description := r.Description
	var ptr *string
	if !r.Partial() {
		ptr = &description
	}
	return store.JobInput{Source: r.Source, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, CompanyName: r.CompanyName, CompanyInfo: companyInfo(r.CompanyInfo), Description: ptr, SalaryMin: r.SalaryMin, SalaryMax: r.SalaryMax, Location: r.Location, RemoteType: r.RemoteType, ProfileRevision: revision}
}

func (p Pipeline) FilterJobs(ctx context.Context, limit int) (int, error) {
	stats, err := p.FilterJobsWithStats(ctx, limit)
	return stats.Processed, err
}

func (p Pipeline) FilterJobsWithStats(ctx context.Context, limit int) (StageStats, error) {
	if _, err := p.snapshot(); err != nil {
		return StageStats{}, err
	}
	limit, _ = stageLimit(limit, 0, false)
	jobs, err := p.Store.PickForStage(ctx, "filter", limit)
	if err != nil {
		return StageStats{}, err
	}
	stats := StageStats{}
	for _, job := range jobs {
		snapshot, err := p.snapshot()
		if err != nil {
			return stats, err
		}
		if !jobUsesRevision(job, snapshot.Revision) {
			continue
		}
		hits := snapshot.Filter.Match(job)
		if err := p.Store.CommitFilter(ctx, job.ID, snapshot.Revision, hits); errors.Is(err, store.ErrStaleRevision) {
			continue
		} else if err != nil {
			return stats, err
		}
		if len(hits) > 0 {
			stats.FilteredOut++
		}
		stats.Processed++
	}
	return stats, nil
}

func (p Pipeline) Score(ctx context.Context, limit int) (int, error) {
	stats, err := p.ScoreWithStats(ctx, limit)
	return stats.Processed, err
}

func (p Pipeline) ScoreWithStats(ctx context.Context, limit int) (StageStats, error) {
	if _, err := p.snapshot(); err != nil {
		return StageStats{}, err
	}
	remaining, limited, err := p.ScoreBudgetRemaining(ctx)
	if err != nil {
		return StageStats{}, err
	}
	limit, ok := stageLimit(limit, remaining, limited)
	if !ok {
		return StageStats{}, nil
	}
	jobs, err := p.Store.PickForStage(ctx, "score", limit)
	if err != nil {
		return StageStats{}, err
	}
	stats := StageStats{}
	var failures []error
	for i, job := range jobs {
		snapshot, snapshotErr := p.snapshot()
		if snapshotErr != nil {
			return stats, snapshotErr
		}
		if !jobUsesRevision(job, snapshot.Revision) {
			continue
		}
		if i > 0 && p.MinInterval > 0 {
			select {
			case <-ctx.Done():
				return stats, ctx.Err()
			case <-time.After(p.MinInterval):
			}
		}
		desc := ""
		if job.Description != nil {
			desc = *job.Description
		}
		jobID := job.ID
		scorer := p.Scorer
		scorer.Audit = func(role, runner, input, output string, ok bool, duration time.Duration) error {
			return p.Store.SaveAgentCall(ctx, store.AgentCallInput{JobID: &jobID, Role: role, Runner: runner, Input: input, Output: output, OK: ok, DurationMS: duration.Milliseconds(), ProfileRevision: snapshot.Revision})
		}
		score, e := scorer.Score(ctx, snapshot.YAML, agents.Job{Title: job.Title, CompanyName: job.CompanyName, Description: desc, Location: job.Location, RemoteType: job.RemoteType, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax})
		if e != nil {
			if ctx.Err() != nil {
				return stats, e
			}
			failures = append(failures, fmt.Errorf("score job %d: %w", job.ID, e))
			continue
		}
		total := float64(score.HardSkill)*p.Weights[0] + float64(score.Domain)*p.Weights[1] + float64(score.Seniority)*p.Weights[2] + float64(score.Condition)*p.Weights[3] + float64(score.Direction)*p.Weights[4]
		state := "scored"
		if total >= p.Threshold {
			state = "shortlisted"
			stats.Shortlisted++
		}
		if err := p.Store.CommitScore(ctx, store.ScoreInput{JobID: job.ID, HardSkill: score.HardSkill, Domain: score.Domain, Seniority: score.Seniority, Condition: score.Condition, Direction: score.Direction, Total: total, Reason: score.Reason, Runner: score.Runner, ProfileRevision: snapshot.Revision}, state); errors.Is(err, store.ErrStaleRevision) {
			continue
		} else if err != nil {
			return stats, err
		}
		stats.Processed++
	}
	return stats, errors.Join(failures...)
}

func jobUsesRevision(job store.Job, revision string) bool {
	return job.ProfileRevision != nil && *job.ProfileRevision == revision
}

func companyInfo(v string) string {
	if strings.TrimSpace(v) == "" {
		return "public listing"
	}
	return v
}
