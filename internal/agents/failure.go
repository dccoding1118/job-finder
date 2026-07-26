package agents

import (
	"encoding/json"
	"strings"
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
	FailureInvalidContent  = "invalid_content"
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

// maxScoreReason is the reason length the Scorer contract tolerates. It sits
// well above the 40~60 characters the prompt asks for: a rejected response costs
// a whole second call, so the wording carries the target and this bound only
// catches an answer that ignored it outright.
const maxScoreReason = 100

func classifyScore(match string) string {
	var parsed ScoreResult
	if err := json.Unmarshal([]byte(match), &parsed); err != nil {
		return FailureInvalidJSON
	}
	for _, value := range []int{parsed.HardSkill, parsed.Domain, parsed.Seniority, parsed.Condition, parsed.Direction} {
		if value < 0 || value > 100 {
			return FailureScoreOutOfRange
		}
	}
	if len([]rune(parsed.Reason)) > maxScoreReason {
		return FailureReasonTooLong
	}
	return FailureInvalidContent
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
