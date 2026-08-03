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

// A reason is bounded by how long it reads, not by how many letters its English
// terms spell. A short bilingual reason must never cost a second call.
func TestScoreReasonCountsAnEnglishTermAsOneUnit(t *testing.T) {
	t.Parallel()
	reason := "後端 Golang 與 Kubernetes 經驗吻合，需具備 PostgreSQL、Elasticsearch 與 Terraform，另有 GitHub Actions 與 Prometheus 監控，方向為 FinTech 支付平台，遠端形式與地點皆符合期待"
	if runes := len([]rune(reason)); runes <= maxScoreReason {
		t.Fatalf("the sample reason is %d runes, it must exceed the old rune bound to be worth testing", runes)
	}
	if length := ReasonLength(reason); length > maxScoreReason {
		t.Fatalf("ReasonLength = %d, want it within the %d bound", length, maxScoreReason)
	}
	raw := `{"content_fit":80,"benefit_fit":70,"bonus_fit":60,"industry_fit":75,"reason":"` + reason + `"}`
	if _, err := parseScore(raw); err != nil {
		t.Fatalf("a short bilingual reason must parse: %v", err)
	}
}

func TestReasonLengthMeasuresTermsAndCharacters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason string
		want   int
	}{
		{"", 0},
		{"符合", 2},
		{"Kubernetes", 1},
		{"熟 Node.js 與 C++", 4},
		{"Go/Rust 皆可", 3},
		{"技能吻合，方向一致", 9},
	}
	for _, testCase := range cases {
		if got := ReasonLength(testCase.reason); got != testCase.want {
			t.Fatalf("ReasonLength(%q) = %d, want %d", testCase.reason, got, testCase.want)
		}
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
