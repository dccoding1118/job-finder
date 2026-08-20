package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Condition kinds and verdicts. A bonus condition is carried through screening
// without weight: it is extracted here so the scoring gate can reuse it rather
// than break the same JD down a second time.
const (
	ConditionRequired = "required"
	ConditionBonus    = "bonus"

	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictUnknown = "unknown"
)

// conditionCategories are the condition types the Filter Agent may report. The
// three year-and-industry categories are the ones whose verdict this package
// discards: arithmetic belongs in Go, not in a language model.
var conditionCategories = map[string]bool{
	"education": true, "skill": true, "certification": true, "language": true,
	"experience_years": true, "management_years": true, "industry": true, "other": true,
}

// FilterCondition is one JD condition as the Agent broke it out and judged it.
type FilterCondition struct {
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	Group    int    `json:"group"`
	Category string `json:"category"`
	Verdict  string `json:"verdict"`
	// YearsRequired and YearsMax are read from the JD, never compared here.
	YearsRequired *float64 `json:"years_required"`
	YearsMax      *float64 `json:"years_max"`
	// IndustryKeys maps a JD industry requirement onto the profile's own keys.
	IndustryKeys []string `json:"industry_keys"`
}

// FilterOutput is the Filter Agent's whole answer: the JD broken into
// conditions, each judged. There is no score and no summary verdict — the
// summary is computed from the conditions by the caller.
type FilterOutput struct {
	Conditions []FilterCondition `json:"conditions"`
	Runner     string            `json:"-"`
}

type Filter struct {
	Primary, Fallback Runner
	Audit             Audit
}

// Screen breaks a JD into conditions and judges each against the hard-rule
// subset of the Profile. profileYAML must be the filter view: the Agent sees no
// intents and no resume narrative.
func (f Filter) Screen(ctx context.Context, profileYAML string, job Job) (FilterOutput, error) {
	prompt := filterPrompt(profileYAML, job)
	var lastErr error
	for _, runner := range []Runner{f.Primary, f.Primary, f.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		reply, err := runner.Invoke(ctx, prompt)
		raw := reply.Text
		result, parseErr := parseFilter(raw)
		ok := err == nil && parseErr == nil
		if !ok {
			lastErr = invocationError(err, parseErr)
		}
		if f.Audit != nil {
			if auditErr := f.Audit(AuditRecord{Role: "filter", Runner: runner.Name(), Model: runner.Model(), Input: prompt, Output: raw, OK: ok, Duration: time.Since(start), Usage: reply.Usage}); auditErr != nil {
				return FilterOutput{}, fmt.Errorf("agents: audit filter call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return FilterOutput{}, runnersFailed("filter", lastErr)
}

func parseFilter(raw string) (FilterOutput, error) {
	match := extractObject(raw)
	if match == "" {
		return FilterOutput{}, fmt.Errorf("agents: response has no JSON object")
	}
	var result FilterOutput
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		return result, fmt.Errorf("agents: invalid filter JSON: %w", err)
	}
	for _, condition := range result.Conditions {
		if err := validateCondition(condition); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validateCondition(condition FilterCondition) error {
	switch {
	case strings.TrimSpace(condition.Text) == "":
		return fmt.Errorf("agents: invalid condition: text is required")
	case condition.Kind != ConditionRequired && condition.Kind != ConditionBonus:
		return fmt.Errorf("agents: invalid condition: kind %q", condition.Kind)
	case condition.Group < 1:
		return fmt.Errorf("agents: invalid condition: group must be a positive integer")
	case !conditionCategories[condition.Category]:
		return fmt.Errorf("agents: invalid condition: category %q", condition.Category)
	case condition.Verdict != VerdictPass && condition.Verdict != VerdictFail && condition.Verdict != VerdictUnknown:
		return fmt.Errorf("agents: invalid condition: verdict %q", condition.Verdict)
	case condition.YearsRequired != nil && *condition.YearsRequired < 0:
		return fmt.Errorf("agents: invalid condition: years_required must not be negative")
	case condition.YearsMax != nil && *condition.YearsMax < 0:
		return fmt.Errorf("agents: invalid condition: years_max must not be negative")
	}
	return nil
}

// BonusTexts lists the conditions the JD marks as a plus. They leave screening
// untouched and feed `bonus_fit` instead.
func (o FilterOutput) BonusTexts() []string {
	texts := []string{}
	for _, condition := range o.Conditions {
		if condition.Kind == ConditionBonus {
			texts = append(texts, condition.Text)
		}
	}
	return texts
}

func filterPrompt(profileYAML string, j Job) string {
	return `你是求職硬條件篩選器。僅輸出單一 JSON 物件，不要說明。

步驟一：把 JD 拆成逐條條件。每條標記 kind（required 必備／bonus 加分），並以 group 表示選言關係——「A 或 B」給同一個 group 編號，獨立條件各自一個編號（正整數）。
步驟二：逐條與 Profile 比對，給 verdict（pass／fail／unknown）。

規則：
- 判斷不出來一律回 unknown，絕不猜測為 fail。
- 學歷須由「同一筆」學歷同時滿足級別與科系相容性才算 pass。
- 年資類（experience_years／management_years）只回 years_required（JD 的下限）與 years_max（JD 明確設的上限，沒有就 null），不要自行比較大小。
- 產業類（industry）只回 industry_keys：JD 要求對應到 Profile 哪些 industry key，以及 years_required。
- category 只能是 education／skill／certification／language／experience_years／management_years／industry／other。
- 不要給任何分數。

Profile（硬條件子集）:
` + profileYAML + `
Job:
title: ` + j.Title + `
company: ` + j.CompanyName + `
location: ` + j.Location + `
remote: ` + j.RemoteType + `
description: ` + j.Description + `

回傳 conditions 陣列，每項含 text（≤40 中文字）、kind、group、category、verdict、years_required、years_max、industry_keys。`
}
