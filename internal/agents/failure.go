package agents

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Failure kinds classify why one runner attempt was rejected. They exist so the
// progress view can say what went wrong without exposing the raw Agent output,
// and so a rejected-but-well-formed answer is never read as a runner outage.
const (
	FailureRunnerError     = "runner_error"
	FailureEmptyOutput     = "empty_output"
	FailureNoJSON          = "no_json"
	FailureInvalidJSON     = "invalid_json"
	FailureReasonTooLong   = "reason_too_long"
	FailureScoreOutOfRange = "score_out_of_range"
	// FailureInvalidCondition covers a Filter answer whose condition list broke
	// the contract: an unknown category, a non-positive group, or negative years.
	FailureInvalidCondition = "invalid_condition"
	FailureInvalidContent   = "invalid_content"
)

// ClassifyFailure reports why an audited call with ok=false was rejected. The
// runner envelope is inspected first: a CLI that reports its own error (rate
// limit, auth, timeout) never produced an answer to validate.
func ClassifyFailure(role, output string) string {
	if strings.TrimSpace(output) == "" {
		return FailureEmptyOutput
	}
	if runnerReportedError(output) {
		return FailureRunnerError
	}
	match := extractObject(output)
	if match == "" {
		return FailureNoJSON
	}
	switch role {
	case "filter":
		if _, err := parseFilter(output); err != nil {
			return classifyFilter(err)
		}
	case "scorer":
		return classifyScore(match)
	case "drafter":
		if _, err := parseDraft(output); err != nil {
			return classifyGeneric(err)
		}
	case "reviewer":
		if _, err := parseReview(output); err != nil {
			return classifyGeneric(err)
		}
	}
	return FailureInvalidContent
}

// maxScoreReason is the reason length the Scorer contract tolerates, counted in
// the units ReasonLength measures. It sits well above the 40~60 the prompt asks
// for: a rejected response costs a whole second call, so the wording carries the
// target and this bound only catches an answer that ignored it outright.
const maxScoreReason = 100

// ReasonLength measures a reason the way the Scorer prompt asks it to be
// counted: one CJK character is one unit, and one run of Latin letters or digits
// is one unit however many letters the term has. Counting a term by its letters
// is what a bound on a bilingual reason must not do — a handful of English
// technical terms would overrun it while the reason itself stays short, and the
// price of that is a second call whose answer is worse than the first.
func ReasonLength(reason string) int {
	length, inTerm := 0, false
	for _, r := range reason {
		if termRune(r) {
			if !inTerm {
				length++
				inTerm = true
			}
			continue
		}
		inTerm = false
		if !unicode.IsSpace(r) {
			length++
		}
	}
	return length
}

// termRune reports whether r continues a Latin term. The joining marks keep
// forms like "Node.js", "C++" and "Go/Rust" as the single terms they read as.
func termRune(r rune) bool {
	if r > unicode.MaxASCII {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("+#-_./", r)
}

func classifyScore(match string) string {
	var parsed ScoreResult
	if err := json.Unmarshal([]byte(match), &parsed); err != nil {
		return FailureInvalidJSON
	}
	for _, value := range []int{parsed.Content, parsed.Benefit, parsed.Bonus, parsed.Industry} {
		if value < 0 || value > 100 {
			return FailureScoreOutOfRange
		}
	}
	if ReasonLength(parsed.Reason) > maxScoreReason {
		return FailureReasonTooLong
	}
	return FailureInvalidContent
}

func classifyFilter(err error) string {
	if strings.Contains(err.Error(), "invalid condition") {
		return FailureInvalidCondition
	}
	return classifyGeneric(err)
}

func classifyGeneric(err error) string {
	if strings.Contains(err.Error(), "JSON") {
		return FailureInvalidJSON
	}
	return FailureInvalidContent
}

// runnerReportedError detects a CLI envelope that carries the runner's own
// failure instead of an answer.
func runnerReportedError(output string) bool {
	match := extractObject(output)
	if match == "" {
		return false
	}
	var envelope struct {
		IsError        *bool `json:"is_error"`
		APIErrorStatus *int  `json:"api_error_status"`
	}
	if err := json.Unmarshal([]byte(match), &envelope); err != nil {
		return false
	}
	return (envelope.IsError != nil && *envelope.IsError) || envelope.APIErrorStatus != nil
}
