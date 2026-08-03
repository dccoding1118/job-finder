package pipeline

import (
	"context"
	"testing"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// Only the two absolute remote preferences can reject. A JD that says nothing
// about remote work is not offering it, so silence counts as onsite for both of
// them and the condition is never left undecided.
func TestRemoteMatrix(t *testing.T) {
	want := map[string]map[string]string{
		"required":   {"remote": store.FilterPass, "hybrid": store.FilterFail, "onsite": store.FilterFail, "unknown": store.FilterFail},
		"preferred":  {"remote": store.FilterPass, "hybrid": store.FilterPass, "onsite": store.FilterPass, "unknown": store.FilterPass},
		"acceptable": {"remote": store.FilterPass, "hybrid": store.FilterPass, "onsite": store.FilterPass, "unknown": store.FilterPass},
		"rejected":   {"remote": store.FilterFail, "hybrid": store.FilterFail, "onsite": store.FilterPass, "unknown": store.FilterPass},
	}
	for preference, cases := range want {
		for jobRemote, verdict := range cases {
			if got := judgeRemote(preference, jobRemote); got != verdict {
				t.Fatalf("remote %s × JD %s = %q, want %q", preference, jobRemote, got, verdict)
			}
		}
	}
}

// Screening always runs every condition it can and then reads the results as a
// whole: one failure rejects, and otherwise an undecided condition only holds
// the job back while more of the JD is still to come. A list excerpt may yet be
// completed, so it waits in 待看; the full JD will say no more, so it goes on to
// scoring rather than resting undecided forever.
func TestUndecidedConditionsOnlyHoldBackListExcerpts(t *testing.T) {
	filter := Filter{Requirements: profile.Requirements{
		SalaryMin: 90000, Locations: []string{"taipei"}, IndustryAvoid: []string{"gambling"},
		EmploymentTypes: []string{profile.EmploymentFullTime}, Remote: "acceptable", ExcludeTitleKeywords: []string{"intern"},
	}}
	description := "Build platform services"

	// Nothing states the salary, the locality, the industry or the type. Every
	// one of those conditions is honestly undecided in both readings.
	silent := store.Job{Title: "platform engineer", RemoteType: "onsite", Description: &description}
	for _, condition := range filter.Evaluate(silent, false) {
		if condition.Text != "exclude_title_keywords" && condition.Text != "exclude_companies" &&
			condition.Text != "remote" && condition.Text != "exclude_description_keywords" &&
			condition.Verdict != store.FilterUnknown {
			t.Fatalf("%s = %q, want unknown", condition.Text, condition.Verdict)
		}
	}
	if outcome := resolveOutcome(filter.Evaluate(silent, false), false); outcome != store.FilterPass {
		t.Fatalf("full JD outcome = %q, want pass", outcome)
	}
	excerpt := store.Job{Title: "platform engineer", RemoteType: "onsite"}
	if outcome := resolveOutcome(filter.Evaluate(excerpt, true), true); outcome != store.FilterUnknown {
		t.Fatalf("list excerpt outcome = %q, want unknown", outcome)
	}

	// A condition that does resolve to a rejection is decisive in both readings,
	// however many others are undecided: plainly out of range is unfit, not 待看.
	elsewhere := store.Job{Title: "platform engineer", Location: "Kaohsiung", RemoteType: "onsite"}
	if outcome := resolveOutcome(filter.Evaluate(elsewhere, true), true); outcome != store.FilterFail {
		t.Fatalf("out-of-range excerpt outcome = %q, want fail", outcome)
	}
	withText := elsewhere
	withText.Description = &description
	if outcome := resolveOutcome(filter.Evaluate(withText, false), false); outcome != store.FilterFail {
		t.Fatalf("out-of-range full JD outcome = %q, want fail", outcome)
	}
}

// "N 年以上" is a floor. More experience than asked for is not a defect, so it
// never rejects; only an explicit ceiling can.
func TestYearAndIndustryConditionsAreDecidedByTheProgram(t *testing.T) {
	derived := profile.Derived{TotalYears: 8, ManagementYears: 2, IndustryYears: map[string]float64{"finance": 3, "retail": 1}}
	floor, ceiling := 5.0, 3.0
	conditions := []store.FilterCondition{
		{Text: "5 年以上", Kind: "required", Group: 1, Category: "experience_years", Verdict: store.FilterFail, YearsMin: &floor},
		{Text: "限 3 年以下", Kind: "required", Group: 2, Category: "experience_years", Verdict: store.FilterPass, YearsMax: &ceiling},
		{Text: "5 年以上管理經驗", Kind: "required", Group: 3, Category: "management_years", Verdict: store.FilterPass, YearsMin: &floor},
		{Text: "金融業經驗 2 年", Kind: "required", Group: 4, Category: "industry", Verdict: store.FilterUnknown, YearsMin: &ceiling, IndustryKeys: []string{"finance"}},
		{Text: "醫療業經驗", Kind: "required", Group: 5, Category: "industry", Verdict: store.FilterPass, IndustryKeys: []string{"healthcare"}},
		{Text: "未指明產業", Kind: "required", Group: 6, Category: "industry", Verdict: store.FilterPass},
	}
	want := []string{store.FilterPass, store.FilterFail, store.FilterFail, store.FilterPass, store.FilterFail, store.FilterUnknown}
	for index, condition := range resolveDerivedConditions(conditions, derived) {
		if condition.Verdict != want[index] {
			t.Fatalf("condition %q = %q, want %q", condition.Text, condition.Verdict, want[index])
		}
	}
}

// A bonus condition never decides fitness, and a disjunctive group is satisfied
// by any one member.
func TestSummarizeConditionsIgnoresBonusAndHonoursDisjunction(t *testing.T) {
	conditions := []store.FilterCondition{
		{Text: "either A", Kind: "required", Group: 1, Verdict: store.FilterFail},
		{Text: "or B", Kind: "required", Group: 1, Verdict: store.FilterPass},
		{Text: "nice to have", Kind: "bonus", Group: 2, Verdict: store.FilterFail},
	}
	if outcome := store.SummarizeConditions(conditions); outcome != store.FilterPass {
		t.Fatalf("outcome = %q, want pass", outcome)
	}
}

func screeningPipeline(t *testing.T, reply string) (Pipeline, *store.Store, *letterRunner) {
	t.Helper()
	runner := &letterRunner{name: "claude", replies: []string{reply}}
	p, db := openPipeline(t, filterFor(profile.Requirements{Locations: []string{"taipei"}, Remote: "acceptable", ExcludeTitleKeywords: []string{"intern"}}))
	p.Screener = agents.Filter{Primary: runner}
	return p, db, runner
}

func captureRow(externalID, title, location string) crawler.RawJob {
	return crawler.RawJob{
		Source: "104", ExternalID: externalID, URL: "https://www.104.com.tw/job/" + externalID,
		Title: title, CompanyName: "Example", CompanyInfo: "software",
		Description: "Go platform work", Location: location, RemoteType: "hybrid",
	}
}

// The semantic pass over a full JD splits two ways: a failed required condition
// rejects, and anything else goes on to scoring. An undecided condition is
// recorded as undecided but does not hold the job back — the JD is complete, so
// waiting would never resolve it.
func TestSemanticScreeningOfAFullJDSplitsTwoWays(t *testing.T) {
	for name, testCase := range map[string]struct {
		reply     string
		wantState string
	}{
		"fail": {`{"conditions":[{"text":"需具備 Rust","kind":"required","group":1,"category":"skill","verdict":"fail"}]}`, "filtered_out"},
		"unknown": {`{"conditions":[
			{"text":"需相關證照","kind":"required","group":1,"category":"certification","verdict":"unknown"},
			{"text":"熟 Kubernetes 尤佳","kind":"bonus","group":2,"category":"skill","verdict":"fail"}
		]}`, "queued"},
		"pass": {`{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`, "queued"},
	} {
		t.Run(name, func(t *testing.T) {
			p, db, runner := screeningPipeline(t, testCase.reply)
			ctx := context.Background()
			result, err := p.IngestJob(ctx, captureRow(name, "Backend Engineer", "Taipei"))
			if err != nil {
				t.Fatal(err)
			}
			if _, stageErr := p.FilterJobsWithStats(ctx, 0); stageErr != nil {
				t.Fatal(stageErr)
			}
			detail, _, err := db.GetJobDetail(ctx, result.JobID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Job.ProcessState != testCase.wantState {
				t.Fatalf("state = %s, want %s", detail.Job.ProcessState, testCase.wantState)
			}
			if runner.calls != 1 {
				t.Fatalf("the Filter Agent was called %d times", runner.calls)
			}
			if detail.Filter == nil || detail.Filter.Stage != "semantic" {
				t.Fatalf("stored screening result = %+v", detail.Filter)
			}
		})
	}
}

// A job the structural rules already reject never reaches the Agent: that is
// what the two-pass split is for.
func TestStructuralRejectionSkipsTheAgent(t *testing.T) {
	p, db, runner := screeningPipeline(t, `{"conditions":[]}`)
	ctx := context.Background()
	result, err := p.IngestJob(ctx, captureRow("rejected", "Backend Intern", "Taipei"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ProcessState != "filtered_out" || len(result.FilterHits) == 0 {
		t.Fatalf("captured job = %+v, want a synchronous rejection", result)
	}
	if _, stageErr := p.FilterJobsWithStats(ctx, 0); stageErr != nil {
		t.Fatal(stageErr)
	}
	if runner.calls != 0 {
		t.Fatalf("the Filter Agent was called %d times for a structurally rejected job", runner.calls)
	}
	detail, _, err := db.GetJobDetail(ctx, result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Filter == nil || detail.Filter.Stage != "structural" {
		t.Fatalf("stored screening result = %+v", detail.Filter)
	}
}

// The semantic pass has its own daily cap, because it is its own Agent call.
func TestSemanticScreeningRespectsItsDailyBudget(t *testing.T) {
	p, _, runner := screeningPipeline(t, `{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`)
	runner.replies = append(runner.replies, `{"conditions":[{"text":"熟 Go","kind":"required","group":1,"category":"skill","verdict":"pass"}]}`)
	p.MaxFilterPerDay = 1
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := p.IngestJob(ctx, captureRow(id, "Backend Engineer", "Taipei")); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := p.FilterJobsWithStats(ctx, 0)
	if err != nil || stats.Processed != 1 {
		t.Fatalf("screened %d under a budget of 1 (%v)", stats.Processed, err)
	}
	stats, err = p.FilterJobsWithStats(ctx, 0)
	if err != nil || stats.Processed != 0 {
		t.Fatalf("screened %d after the budget was spent (%v)", stats.Processed, err)
	}
}

// The user picks a type, not a wording. A JD that says 正職 states the same
// arrangement as 全職, so matching runs on the type's aliases — otherwise the
// condition would read as undecided on a JD that plainly answered it.
func TestEmploymentTypeMatchesEveryWordingOfTheChosenType(t *testing.T) {
	filter := Filter{Requirements: profile.Requirements{
		Remote: "acceptable", EmploymentTypes: []string{profile.EmploymentFullTime},
	}}
	for wording, want := range map[string]string{
		"正職":        store.FilterPass,
		"full-time": store.FilterPass,
		"兼職":        store.FilterFail,
		"無說明":       store.FilterUnknown,
	} {
		description := "Build platform services（" + wording + "）"
		job := store.Job{Title: "platform engineer", Description: &description}
		var got string
		for _, condition := range filter.Evaluate(job, false) {
			if condition.Text == "employment_types" {
				got = condition.Verdict
			}
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", wording, got, want)
		}
	}
}

// The locality condition matches the JD's own wording from the picked key, so a
// source reporting 臺北市, 台北市 or Taipei all satisfy the same choice. Each key
// admits only its own locality: `taiwan` admits a JD that stated the country and
// no county, and `overseas` admits only a JD stated as such.
func TestLocationConditionMatchesEveryWordingOfTheKey(t *testing.T) {
	verdictFor := func(keys []string, location string) string {
		filter := Filter{Requirements: profile.Requirements{Locations: keys, Remote: "acceptable"}}
		for _, condition := range filter.Evaluate(store.Job{Title: "platform engineer", Location: location, RemoteType: "onsite"}, true) {
			if condition.Text == "locations" {
				return condition.Verdict
			}
		}
		return ""
	}
	for _, testCase := range []struct {
		keys     []string
		location string
		want     string
	}{
		{[]string{"taipei"}, "臺北市", store.FilterPass},
		{[]string{"taipei"}, "台北市大安區", store.FilterPass},
		{[]string{"taipei"}, "Taipei, Taiwan", store.FilterPass},
		{[]string{"taipei"}, "高雄市", store.FilterFail},
		{[]string{"taipei"}, "台灣", store.FilterFail},
		{[]string{profile.LocationTaiwan}, "台灣", store.FilterPass},
		{[]string{profile.LocationTaiwan}, "全台", store.FilterPass},
		{[]string{profile.LocationTaiwan}, "臺東縣", store.FilterFail},
		{[]string{profile.LocationTaiwan}, "Taipei, Taiwan", store.FilterFail},
		{[]string{profile.LocationTaiwan}, "Tokyo, Japan", store.FilterFail},
		{profile.TaiwanLocationKeys(), "臺東縣", store.FilterPass},
		{profile.TaiwanLocationKeys(), "台灣", store.FilterPass},
		{profile.TaiwanLocationKeys(), "Tokyo, Japan", store.FilterFail},
		{[]string{profile.LocationOverseas}, "海外", store.FilterPass},
		{[]string{profile.LocationOverseas}, "台北市", store.FilterFail},
		{[]string{"taipei"}, "", store.FilterUnknown},
		// A source that stated no locality says nothing about the job's own; the
		// condition is undecided, never failed.
		{[]string{"taipei"}, store.LocationUnknown, store.FilterUnknown},
	} {
		if got := verdictFor(testCase.keys, testCase.location); got != testCase.want {
			t.Fatalf("%v × %q = %q, want %q", testCase.keys, testCase.location, got, testCase.want)
		}
	}
}
