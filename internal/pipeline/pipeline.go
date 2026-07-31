// Package pipeline orchestrates scheduled fetching and the resident worker that
// consumes the filter, score, and letter stages.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

type Pipeline struct {
	Store       *store.Store
	Source      crawler.Source
	Provider    *profile.Provider
	Filter      Filter
	Screener    agents.Filter
	Scorer      agents.Scorer
	Drafter     agents.Drafter
	Reviewer    agents.Reviewer
	ProfileYAML string
	Profile     profile.Profile
	Denylist    []string
	// Weights are the four soft dimensions in order: content, benefit, bonus,
	// industry.
	Weights   [4]float64
	Threshold float64
	// Baseline is the score each dimension starts from, so "no information to
	// judge this on" reads as neutral rather than as a bad fit.
	Baseline int
	// DedupeEnabled turns cross-source grouping on; with it off every source keeps
	// its own copy of a job, which is the escape hatch while the normalization
	// rules are being retuned.
	DedupeEnabled   bool
	Dedupe          store.DedupeOptions
	MaxFilterPerDay int
	MaxScorePerDay  int
	MaxLetterPerDay int
	MaxLetterLength int
	MinInterval     time.Duration
	// Logger receives one structured record per Agent-backed unit of work, which
	// is what makes a long or failing stage observable while it runs.
	Logger *slog.Logger
	// Now supplies the clock the Taipei day boundary is derived from.
	Now func() time.Time
}

func (p Pipeline) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// workProfile is one unit of work's immutable view of the Profile. It carries
// both revisions because the two gates are versioned apart.
type workProfile struct {
	Value     profile.Profile
	YAML      string
	Revisions store.Revisions
	Filter    Filter
}

func (p Pipeline) snapshot() (workProfile, error) {
	value, yaml := p.Profile, p.ProfileYAML
	if p.Provider != nil {
		snapshot, err := p.Provider.Ready()
		if err != nil {
			return workProfile{}, err
		}
		value, yaml = *snapshot.Profile, snapshot.YAML
		return workProfile{Value: value, YAML: yaml, Revisions: store.Revisions{Filter: snapshot.FilterRevision, Score: snapshot.ScoreRevision}, Filter: FilterFromProfile(value)}, nil
	}
	filterRevision, scoreRevision, _ := profile.RevisionPair(value)
	return workProfile{Value: value, YAML: yaml, Revisions: store.Revisions{Filter: filterRevision, Score: scoreRevision}, Filter: p.Filter}, nil
}

func (p Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// FilterBudgetRemaining reports how many semantic screening calls today's
// budget still allows; limited is false when no cap is configured.
func (p Pipeline) FilterBudgetRemaining(ctx context.Context) (int, bool, error) {
	return p.budgetRemaining(ctx, "filter", p.MaxFilterPerDay)
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
	Queued      int
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
	// The letter stage records the revision it actually drafted under rather than
	// requiring the job to already carry it, so it picks by state alone.
	jobs, err := p.Store.PickForStage(ctx, "letter", "", limit)
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
		audit := func(role, runner, model, input, output string, ok bool, duration time.Duration, usage agents.Usage) error {
			return p.Store.SaveAgentCall(ctx, store.AgentCallInput{JobID: &jobID, Role: role, Runner: runner, Model: model, Input: input, Output: output, OK: ok, DurationMS: duration.Milliseconds(), Usage: storeUsage(usage), FilterRevision: snapshot.Revisions.Filter, ScoreRevision: snapshot.Revisions.Score})
		}
		drafter, reviewer := p.Drafter, p.Reviewer
		drafter.Audit, reviewer.Audit = audit, audit
		desc := ""
		if job.Description != nil {
			desc = *job.Description
		}
		view, viewErr := profile.MarshalView(snapshot.Value.LetterView())
		if viewErr != nil {
			return stats, viewErr
		}
		p.logger().Info("drafting letter", "stage", "letter", "job_id", jobID, "filter_revision", snapshot.Revisions.Filter, "score_revision", snapshot.Revisions.Score)
		startedAt := time.Now()
		result, e := agents.GenerateLetter(ctx, drafter, reviewer, view, snapshot.Value, agents.Job{Title: job.Title, CompanyName: job.CompanyName, Description: desc, Location: job.Location, RemoteType: job.RemoteType, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax}, p.Denylist, p.MaxLetterLength)
		if e != nil {
			if ctx.Err() != nil {
				return stats, e
			}
			p.logger().Error("letter failed", "stage", "letter", "job_id", jobID, "duration_ms", time.Since(startedAt).Milliseconds(), "error", e)
			failures = append(failures, fmt.Errorf("letter job %d: %w", job.ID, e))
			continue
		}
		if result.Status == "failed" {
			result.Content = "[你的姓名]\n[你的聯絡方式]"
		}
		if err := p.Store.SaveLetter(ctx, store.LetterInput{JobID: job.ID, Content: result.Content, Status: result.Status, ReviewLog: result.ReviewLog, Rounds: result.Rounds, RunnerDraft: result.DraftRunner, RunnerReview: result.ReviewRunner, FilterRevision: snapshot.Revisions.Filter, ScoreRevision: snapshot.Revisions.Score}); err != nil {
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
		p.logger().Info("letter completed", "stage", "letter", "job_id", jobID, "state", state, "rounds", result.Rounds, "duration_ms", time.Since(startedAt).Milliseconds())
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
		result, e := p.Store.UpsertJob(ctx, jobInput(r, snapshot.Revisions.Filter), runID)
		if e != nil {
			return stats, e
		}
		if _, e := p.link(ctx, result.Job.ID); e != nil {
			return stats, e
		}
		stats.Fetched++
		if result.Created {
			stats.New++
		}
	}
	return stats, nil
}

// storeUsage carries an Agent call's token accounting into the store's own
// type, keeping the store package free of a dependency on internal/agents.
func storeUsage(usage agents.Usage) store.AgentCallUsage {
	return store.AgentCallUsage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		ReasoningTokens:  usage.ReasoningTokens,
		CostUSD:          usage.CostUSD,
	}
}

func jobInput(r crawler.RawJob, filterRevision string) store.JobInput {
	description := r.Description
	var ptr *string
	if !r.Partial() {
		ptr = &description
	}
	return store.JobInput{Source: r.Source, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, CompanyName: r.CompanyName, CompanyInfo: companyInfo(r.CompanyInfo), Description: ptr, SalaryMin: r.SalaryMin, SalaryMax: r.SalaryMax, Location: r.Location, RemoteType: r.RemoteType, FilterRevision: filterRevision}
}

func (p Pipeline) FilterJobs(ctx context.Context, limit int) (int, error) {
	stats, err := p.FilterJobsWithStats(ctx, limit)
	return stats.Processed, err
}

// FilterJobsWithStats applies the hard rules in two passes. The structural pass
// costs nothing, so a job it rejects never reaches the Agent; only a job that
// passes every deterministic condition is worth one semantic call.
func (p Pipeline) FilterJobsWithStats(ctx context.Context, limit int) (StageStats, error) {
	active, err := p.snapshot()
	if err != nil {
		return StageStats{}, err
	}
	remaining, limited, err := p.FilterBudgetRemaining(ctx)
	if err != nil {
		return StageStats{}, err
	}
	limit, ok := stageLimit(limit, remaining, limited)
	if !ok {
		p.logger().Debug("filter stage skipped", "stage", "filter", "reason", "daily budget exhausted", "max_per_day", p.MaxFilterPerDay)
		return StageStats{}, nil
	}
	jobs, err := p.Store.PickForStage(ctx, "filter", active.Revisions.Filter, limit)
	if err != nil {
		return StageStats{}, err
	}
	stats := StageStats{}
	var failures []error
	log := p.logger()
	if len(jobs) > 0 {
		log.Info("filter stage picked jobs", "stage", "filter", "jobs", len(jobs), "budget_remaining", remaining, "budget_limited", limited)
	}
	for i, job := range jobs {
		snapshot, snapshotErr := p.snapshot()
		if snapshotErr != nil {
			return stats, snapshotErr
		}
		if !usesRevision(job.FilterRevision, snapshot.Revisions.Filter) {
			continue
		}
		result, semantic, err := p.screenJob(ctx, job, snapshot, i > 0)
		if err != nil {
			if ctx.Err() != nil {
				return stats, err
			}
			log.Error("filter failed", "stage", "filter", "job_id", job.ID, "error", err)
			failures = append(failures, fmt.Errorf("filter job %d: %w", job.ID, err))
			continue
		}
		if err := p.Store.SaveFilterResult(ctx, job.ID, result, snapshot.Revisions); errors.Is(err, store.ErrStaleRevision) {
			log.Info("filter result discarded as stale", "stage", "filter", "job_id", job.ID, "revision", snapshot.Revisions.Filter)
			continue
		} else if err != nil {
			return stats, err
		}
		switch result.Outcome {
		case store.FilterFail:
			stats.FilteredOut++
		default:
			stats.Queued++
		}
		log.Info("job screened", "stage", "filter", "job_id", job.ID, "filter_revision", snapshot.Revisions.Filter, "verdict", result.Outcome, "stage_kind", result.Stage, "semantic", semantic)
		stats.Processed++
	}
	if stats.Processed > 0 {
		log.Info("filter stage completed", "stage", "filter", "processed", stats.Processed, "filtered_out", stats.FilteredOut, "queued", stats.Queued)
	}
	return stats, errors.Join(failures...)
}

// screenJob runs the structural conditions and, only when they all hold, one
// Filter Agent call. semantic reports whether the Agent was actually consulted.
func (p Pipeline) screenJob(ctx context.Context, job store.Job, snapshot workProfile, pace bool) (store.FilterResult, bool, error) {
	partial := job.Description == nil
	conditions := snapshot.Filter.Evaluate(job, partial)
	// A structural failure is decisive and costs no call. Anything else still
	// buys the semantic half, because an undecided structural condition says
	// nothing about the conditions the JD text carries.
	if store.SummarizeConditions(conditions) == store.FilterFail || p.Screener.Primary == nil {
		return store.FilterResult{Outcome: resolveOutcome(conditions, partial), Conditions: conditions, Stage: "structural", Partial: partial}, false, nil
	}
	if pace && p.MinInterval > 0 {
		select {
		case <-ctx.Done():
			return store.FilterResult{}, false, ctx.Err()
		case <-time.After(p.MinInterval):
		}
	}
	view, err := profile.MarshalView(snapshot.Value.FilterView())
	if err != nil {
		return store.FilterResult{}, false, err
	}
	jobID := job.ID
	screener := p.Screener
	screener.Audit = func(role, runner, model, input, output string, ok bool, duration time.Duration, usage agents.Usage) error {
		return p.Store.SaveAgentCall(ctx, store.AgentCallInput{JobID: &jobID, Role: role, Runner: runner, Model: model, Input: input, Output: output, OK: ok, DurationMS: duration.Milliseconds(), Usage: storeUsage(usage), FilterRevision: snapshot.Revisions.Filter})
	}
	description := ""
	if job.Description != nil {
		description = *job.Description
	}
	output, err := screener.Screen(ctx, view, agents.Job{Title: job.Title, CompanyName: job.CompanyName, Description: description, Location: job.Location, RemoteType: job.RemoteType, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax})
	if err != nil {
		return store.FilterResult{}, false, err
	}
	semantic := resolveDerivedConditions(agentConditions(output.Conditions), snapshot.Value.DerivedTotals())
	conditions = append(conditions, semantic...)
	runner := output.Runner
	return store.FilterResult{Outcome: resolveOutcome(conditions, partial), Conditions: conditions, Stage: "semantic", Runner: &runner, Partial: partial}, true, nil
}

// agentConditions renumbers the Agent's disjunction groups above the structural
// ones, so two independently numbered sets cannot collide into one group.
func agentConditions(conditions []agents.FilterCondition) []store.FilterCondition {
	out := make([]store.FilterCondition, 0, len(conditions))
	for _, condition := range conditions {
		out = append(out, store.FilterCondition{
			Text: condition.Text, Kind: condition.Kind, Group: condition.Group + agentGroupOffset,
			Category: condition.Category, Verdict: condition.Verdict,
			YearsMin: condition.YearsRequired, YearsMax: condition.YearsMax, IndustryKeys: condition.IndustryKeys,
		})
	}
	return out
}

// agentGroupOffset separates the two condition sources' group numbering.
const agentGroupOffset = 1000

func (p Pipeline) Score(ctx context.Context, limit int) (int, error) {
	stats, err := p.ScoreWithStats(ctx, limit)
	return stats.Processed, err
}

func (p Pipeline) ScoreWithStats(ctx context.Context, limit int) (StageStats, error) {
	active, err := p.snapshot()
	if err != nil {
		return StageStats{}, err
	}
	remaining, limited, err := p.ScoreBudgetRemaining(ctx)
	if err != nil {
		return StageStats{}, err
	}
	limit, ok := stageLimit(limit, remaining, limited)
	if !ok {
		p.logger().Debug("score stage skipped", "stage", "score", "reason", "daily budget exhausted", "max_per_day", p.MaxScorePerDay)
		return StageStats{}, nil
	}
	jobs, err := p.Store.PickForStage(ctx, "score", active.Revisions.Score, limit)
	if err != nil {
		return StageStats{}, err
	}
	stats := StageStats{}
	var failures []error
	log := p.logger()
	if len(jobs) > 0 {
		log.Info("score stage picked jobs", "stage", "score", "jobs", len(jobs), "budget_remaining", remaining, "budget_limited", limited)
	}
	for i, job := range jobs {
		snapshot, snapshotErr := p.snapshot()
		if snapshotErr != nil {
			return stats, snapshotErr
		}
		if !usesRevision(job.ScoreRevision, snapshot.Revisions.Score) {
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
		scorer.Audit = func(role, runner, model, input, output string, ok bool, duration time.Duration, usage agents.Usage) error {
			return p.Store.SaveAgentCall(ctx, store.AgentCallInput{JobID: &jobID, Role: role, Runner: runner, Model: model, Input: input, Output: output, OK: ok, DurationMS: duration.Milliseconds(), Usage: storeUsage(usage), ScoreRevision: snapshot.Revisions.Score})
		}
		// The bonus conditions the screening gate already extracted are handed over
		// rather than re-derived: the JD is broken down once, by one gate.
		view, viewErr := p.scoreView(ctx, snapshot, job.ID)
		if viewErr != nil {
			return stats, viewErr
		}
		log.Info("scoring job", "stage", "score", "job_id", jobID, "source", job.Source, "score_revision", snapshot.Revisions.Score)
		startedAt := time.Now()
		score, e := scorer.Score(ctx, view, agents.Job{Title: job.Title, CompanyName: job.CompanyName, Description: desc, Location: job.Location, RemoteType: job.RemoteType, SalaryMin: job.SalaryMin, SalaryMax: job.SalaryMax}, p.baseline())
		if e != nil {
			if ctx.Err() != nil {
				return stats, e
			}
			log.Error("score failed", "stage", "score", "job_id", jobID, "duration_ms", time.Since(startedAt).Milliseconds(), "error", e)
			failures = append(failures, fmt.Errorf("score job %d: %w", job.ID, e))
			continue
		}
		total := float64(score.Content)*p.Weights[0] + float64(score.Benefit)*p.Weights[1] + float64(score.Bonus)*p.Weights[2] + float64(score.Industry)*p.Weights[3]
		state := "scored"
		if total >= p.Threshold {
			state = "shortlisted"
			stats.Shortlisted++
		}
		if err := p.Store.CommitScore(ctx, store.ScoreInput{JobID: job.ID, Content: score.Content, Benefit: score.Benefit, Bonus: score.Bonus, Industry: score.Industry, Total: total, Reason: score.Reason, Runner: score.Runner, ScoreRevision: snapshot.Revisions.Score}, state); errors.Is(err, store.ErrStaleRevision) {
			log.Info("score discarded as stale", "stage", "score", "job_id", jobID, "score_revision", snapshot.Revisions.Score)
			continue
		} else if err != nil {
			return stats, err
		}
		log.Info("job scored", "stage", "score", "job_id", jobID, "total", total, "state", state, "runner", score.Runner, "duration_ms", time.Since(startedAt).Milliseconds())
		stats.Processed++
	}
	return stats, errors.Join(failures...)
}

// link places one freshly upserted job in its cross-source group and reports the
// job every caller must act on and report: an alias carries no verdict of its
// own, so the canonical copy is what a capture answers with.
func (p Pipeline) link(ctx context.Context, jobID int64) (int64, error) {
	if !p.DedupeEnabled {
		return jobID, nil
	}
	outcome, err := p.Store.LinkOrSuggestDuplicate(ctx, jobID, p.Dedupe)
	if err != nil {
		return 0, err
	}
	if outcome.Merged {
		p.logger().Info("job merged across sources", "stage", "dedupe", "job_id", jobID, "canonical_job_id", outcome.CanonicalJobID, "group_id", outcome.GroupID)
	}
	for _, candidate := range outcome.CandidateIDs {
		p.logger().Info("duplicate candidate registered", "stage", "dedupe", "job_id", jobID, "candidate_id", candidate)
	}
	return outcome.CanonicalJobID, nil
}

// usesRevision reports whether a job's recorded gate revision is the active
// one; work produced under any other revision would only be discarded by CAS.
func usesRevision(recorded *string, active string) bool {
	return recorded != nil && *recorded == active
}

// scoreView renders the scoring subset of the Profile, carrying over the bonus
// conditions the screening gate already extracted from this job's JD.
func (p Pipeline) scoreView(ctx context.Context, snapshot workProfile, jobID int64) (string, error) {
	bonus := []string{}
	result, err := p.Store.CurrentFilterResult(ctx, jobID)
	if err != nil {
		return "", err
	}
	if result != nil {
		for _, condition := range result.Conditions {
			if condition.Kind == "bonus" {
				bonus = append(bonus, condition.Text)
			}
		}
	}
	return profile.MarshalView(snapshot.Value.ScoreView(bonus))
}

// baseline is the neutral score a dimension takes when the JD offers nothing to
// judge it on. It defaults to the recommendation threshold, which leaves such a
// job exactly on the line rather than pushing it either way.
func (p Pipeline) baseline() int {
	if p.Baseline > 0 {
		return p.Baseline
	}
	return int(p.Threshold)
}

func companyInfo(v string) string {
	if strings.TrimSpace(v) == "" {
		return "public listing"
	}
	return v
}
