package pipeline

import (
	"strings"

	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// Filter is the structural half of the hard rules: the conditions that are pure
// string and number comparison and therefore cost no tokens. It is derived from
// Profile alone — job-search conditions have no duplicate config representation.
type Filter struct {
	Requirements profile.Requirements
}

func FilterFromProfile(p profile.Profile) Filter { return Filter{Requirements: p.Requirements} }

// employmentTypeTerms are every wording a JD may state an employment type
// with. They decide whether the JD stated a type at all, and are only consulted
// when the user actually restricted the types.
var employmentTypeTerms = profile.EmploymentTypes.AllAliases()

// acceptedEmploymentTerms expands the user's chosen type keys into the JD
// wordings that count as those types.
func acceptedEmploymentTerms(keys []string) []string {
	terms := make([]string, 0, len(keys)*4)
	for _, key := range keys {
		terms = append(terms, profile.EmploymentTypes.Aliases(key)...)
	}
	return terms
}

// structuralRule is one deterministic condition. partial marks the rules whose
// inputs a list-page job already carries; the rest wait for the full JD, because
// a list summary is an excerpt and "the word is absent" would be a false reject.
type structuralRule struct {
	name    string
	partial bool
	judge   func(Filter, store.Job) string
}

var structuralRules = []structuralRule{
	{"exclude_title_keywords", true, func(f Filter, j store.Job) string {
		return failIf(containsAny(j.Title, f.Requirements.ExcludeTitleKeywords))
	}},
	{"exclude_companies", true, func(f Filter, j store.Job) string {
		return failIf(containsAny(j.CompanyName, f.Requirements.ExcludeCompanies))
	}},
	{"industry_avoid", true, func(f Filter, j store.Job) string {
		if len(f.Requirements.IndustryAvoid) == 0 {
			return ""
		}
		if strings.TrimSpace(j.CompanyInfo) == "" || j.CompanyInfo == "public listing" {
			return store.FilterUnknown
		}
		return failIf(containsAny(j.CompanyInfo, f.Requirements.IndustryAvoid))
	}},
	{"locations", true, func(f Filter, j store.Job) string {
		if len(f.Requirements.Locations) == 0 || j.RemoteType == "remote" {
			return ""
		}
		if location := strings.TrimSpace(j.Location); location == "" || location == store.LocationUnknown {
			return store.FilterUnknown
		}
		return failIf(!containsAny(j.Location, profile.LocationTerms(f.Requirements.Locations)))
	}},
	{"remote", true, func(f Filter, j store.Job) string { return judgeRemote(f.Requirements.Remote, j.RemoteType) }},
	{"employment_types", true, func(f Filter, j store.Job) string {
		if len(f.Requirements.EmploymentTypes) == 0 {
			return ""
		}
		text := j.Title
		if j.Description != nil {
			text += "\n" + *j.Description
		}
		if containsAny(text, acceptedEmploymentTerms(f.Requirements.EmploymentTypes)) {
			return store.FilterPass
		}
		// A stated type that is not on the user's list rejects; no stated type at
		// all leaves the condition undecided.
		if containsAny(text, employmentTypeTerms) {
			return store.FilterFail
		}
		return store.FilterUnknown
	}},
	{"salary_floor", true, func(f Filter, j store.Job) string {
		if f.Requirements.SalaryMin <= 0 {
			return ""
		}
		if j.SalaryMax == nil {
			return store.FilterUnknown
		}
		return failIf(*j.SalaryMax < f.Requirements.SalaryMin)
	}},
	{"exclude_description_keywords", false, func(f Filter, j store.Job) string {
		if j.Description == nil {
			return store.FilterUnknown
		}
		return failIf(containsAny(*j.Description, f.Requirements.ExcludeDescriptionKeywords))
	}},
}

// judgeRemote is the remote matrix. `required` and `rejected` are absolute
// statements, and a JD that does not mention remote work is not offering it —
// silence here is the answer, not a gap. So `required` admits only a JD that
// says it is fully remote, and `rejected` turns away anything that mentions
// remote or hybrid; neither ever leaves the condition undecided.
func judgeRemote(preference, jobRemote string) string {
	if preference != "required" && preference != "rejected" {
		return store.FilterPass
	}
	if preference == "required" {
		return verdictOf(jobRemote == "remote")
	}
	return verdictOf(jobRemote != "remote" && jobRemote != "hybrid")
}

// Evaluate judges every structural condition a job's available fields support.
// partial restricts it to the rules a list-page job can answer.
func (f Filter) Evaluate(job store.Job, partial bool) []store.FilterCondition {
	conditions := make([]store.FilterCondition, 0, len(structuralRules))
	group := 0
	for _, rule := range structuralRules {
		if partial && !rule.partial {
			continue
		}
		verdict := rule.judge(f, job)
		if verdict == "" {
			continue
		}
		group++
		conditions = append(conditions, store.FilterCondition{
			Text: rule.name, Kind: "required", Group: group, Category: "other", Verdict: verdict,
		})
	}
	return conditions
}

// Match reports the structural conditions a job fails outright. It is what the
// list-page mark and the partial re-screen are decided by: a failure is a
// conclusion those paths can state without any further input.
func (f Filter) Match(job store.Job) []string {
	partial := job.Description == nil
	hits := []string{}
	for _, condition := range f.Evaluate(job, partial) {
		if condition.Verdict == store.FilterFail {
			hits = append(hits, condition.Text)
		}
	}
	return hits
}

// resolveDerivedConditions overwrites the verdict of every condition whose
// answer is arithmetic. The Agent supplies what the JD asked for; the comparison
// is done here, against the totals derived from the experience list.
func resolveDerivedConditions(conditions []store.FilterCondition, derived profile.Derived) []store.FilterCondition {
	resolved := make([]store.FilterCondition, 0, len(conditions))
	for _, condition := range conditions {
		switch condition.Category {
		case "experience_years":
			condition.Verdict = judgeYears(derived.TotalYears, condition)
		case "management_years":
			condition.Verdict = judgeYears(derived.ManagementYears, condition)
		case "industry":
			condition.Verdict = judgeIndustry(derived, condition)
		}
		resolved = append(resolved, condition)
	}
	return resolved
}

// judgeYears reads "N 年以上" as a floor. Exceeding it is not a defect, so more
// experience than asked for never rejects; only an explicit ceiling can.
func judgeYears(actual float64, condition store.FilterCondition) string {
	if condition.YearsMin == nil && condition.YearsMax == nil {
		return store.FilterUnknown
	}
	if condition.YearsMin != nil && actual < *condition.YearsMin {
		return store.FilterFail
	}
	if condition.YearsMax != nil && actual > *condition.YearsMax {
		return store.FilterFail
	}
	return store.FilterPass
}

func judgeIndustry(derived profile.Derived, condition store.FilterCondition) string {
	if len(condition.IndustryKeys) == 0 {
		return store.FilterUnknown
	}
	total := 0.0
	for _, key := range condition.IndustryKeys {
		total += derived.IndustryYears[key]
	}
	required := 0.0
	if condition.YearsMin != nil {
		required = *condition.YearsMin
	}
	if total <= 0 || total < required {
		return store.FilterFail
	}
	return store.FilterPass
}

func containsAny(value string, terms []string) bool {
	value = strings.ToLower(value)
	for _, term := range terms {
		if term != "" && strings.Contains(value, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func failIf(failed bool) string { return verdictOf(!failed) }

func verdictOf(passed bool) string {
	if passed {
		return store.FilterPass
	}
	return store.FilterFail
}

// resolveOutcome turns the condition list into the job's screening outcome. A
// single failure is decisive whatever else holds. Otherwise an undecided
// condition only holds the job back while more of the JD is still to come: on a
// list excerpt that means 待看, and on the full JD it means the fact is simply
// not stated, which is no reason to withhold the job from scoring.
func resolveOutcome(conditions []store.FilterCondition, partial bool) string {
	outcome := store.SummarizeConditions(conditions)
	if outcome == store.FilterUnknown && !partial {
		return store.FilterPass
	}
	return outcome
}
