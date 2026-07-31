package agents

import (
	"strings"
	"testing"
)

func TestClassifyFailureSeparatesRunnerErrorsFromRejectedAnswers(t *testing.T) {
	t.Parallel()
	longReason := strings.Repeat("理", maxScoreReason+10)
	cases := []struct {
		name, role, output, want string
	}{
		{"rate limited runner", "scorer", `{"type":"result","is_error":true,"api_error_status":429,"result":"weekly limit"}`, FailureRunnerError},
		{"empty output", "scorer", "  ", FailureEmptyOutput},
		{"prose only", "scorer", "抱歉，我無法評分。", FailureNoJSON},
		{"broken json", "scorer", `{"content_fit": 10,`, FailureNoJSON},
		{"malformed types", "scorer", `{"content_fit": "high", "reason": "ok"}`, FailureInvalidJSON},
		{"reason over the cap", "scorer", "```json\n{\"content_fit\":20,\"benefit_fit\":30,\"bonus_fit\":40,\"industry_fit\":50,\"reason\":\"" + longReason + "\"}\n```", FailureReasonTooLong},
		{"dimension out of range", "scorer", `{"content_fit":120,"benefit_fit":30,"bonus_fit":40,"industry_fit":50,"reason":"合理"}`, FailureScoreOutOfRange},
		{"empty reason", "scorer", `{"content_fit":20,"benefit_fit":30,"bonus_fit":40,"industry_fit":50,"reason":""}`, FailureInvalidContent},
		{"letter missing", "drafter", `{"letter":""}`, FailureInvalidContent},
		{"verdict missing", "reviewer", `{"verdict":"maybe"}`, FailureInvalidContent},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyFailure(testCase.role, testCase.output); got != testCase.want {
				t.Fatalf("ClassifyFailure = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestScoreReasonCapAcceptsUpToTheLimit(t *testing.T) {
	t.Parallel()
	build := func(runes int) string {
		return `{"content_fit":20,"benefit_fit":30,"bonus_fit":40,"industry_fit":50,"reason":"` + strings.Repeat("理", runes) + `"}`
	}
	if _, err := parseScore(build(maxScoreReason)); err != nil {
		t.Fatalf("a reason at the cap must parse: %v", err)
	}
	if _, err := parseScore(build(maxScoreReason + 1)); err == nil {
		t.Fatal("a reason over the cap must be rejected")
	}
}

// A low score is a successful call: the answer parses, so nothing about the
// score value may turn it into a failure.
func TestLowScoreIsAValidAnswer(t *testing.T) {
	t.Parallel()
	raw := `{"content_fit":0,"benefit_fit":30,"bonus_fit":40,"industry_fit":50,"reason":"技能與方向皆不符"}`
	if _, err := parseScore(raw); err != nil {
		t.Fatalf("a zero score must parse: %v", err)
	}
}
