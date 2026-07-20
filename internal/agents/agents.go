// Package agents provides structured LLM scoring through CLI runners.
package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/profile"
)

type (
	Runner interface {
		Name() string
		Invoke(context.Context, string) (string, error)
	}
	CommandRunner struct {
		RunnerName, Command string
		Args                []string
		TempRoot            string
		Timeout             time.Duration
	}
)

func (r CommandRunner) Name() string { return r.RunnerName }
func (r CommandRunner) Invoke(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(r.Command) == "" {
		return "", fmt.Errorf("agents: %s: command is required", r.RunnerName)
	}
	dir, err := os.MkdirTemp(r.TempRoot, "jobfinder-agent-*")
	if err != nil {
		return "", fmt.Errorf("agents: %s: create temporary directory: %w", r.RunnerName, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	// #nosec G204 -- Command and flags are fixed runner definitions; prompt is one CLI argument.
	c := exec.CommandContext(ctx, r.Command, append(r.Args, prompt)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return string(out), fmt.Errorf("agents: %s: %w", r.RunnerName, ctx.Err())
		}
		return string(out), fmt.Errorf("agents: %s: %w", r.RunnerName, err)
	}
	return string(out), nil
}

func ClaudeRunner(model string, timeout time.Duration) Runner {
	return CommandRunner{RunnerName: "claude", Command: "claude", Args: []string{"-p", "--model", model, "--output-format", "text", "--tools", ""}, Timeout: timeout}
}

func CodexRunner(model string, timeout time.Duration) Runner {
	return CommandRunner{RunnerName: "codex", Command: "codex", Args: []string{"exec", "--model", model, "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never"}, Timeout: timeout}
}

type (
	ScoreResult struct {
		HardSkill int    `json:"hard_skill"`
		Domain    int    `json:"domain"`
		Seniority int    `json:"seniority"`
		Condition int    `json:"condition"`
		Direction int    `json:"direction"`
		Reason    string `json:"reason"`
		Runner    string
	}
	Audit  func(role, runner, input, output string, ok bool, duration time.Duration) error
	Scorer struct {
		Primary, Fallback Runner
		Audit             Audit
	}
)

func (s Scorer) Score(ctx context.Context, profileYAML string, job Job) (ScoreResult, error) {
	prompt := scorePrompt(profileYAML, job)
	for _, runner := range []Runner{s.Primary, s.Primary, s.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		raw, err := runner.Invoke(ctx, prompt)
		result, parseErr := parseScore(raw)
		ok := err == nil && parseErr == nil
		if s.Audit != nil {
			if auditErr := s.Audit("scorer", runner.Name(), prompt, raw, ok, time.Since(start)); auditErr != nil {
				return ScoreResult{}, fmt.Errorf("agents: audit scorer call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return ScoreResult{}, fmt.Errorf("agents: all scorer runners failed")
}

type Job struct {
	Title, CompanyName, Description, Location, RemoteType string
	SalaryMin, SalaryMax                                  *int
}

type DraftResult struct {
	Letter string `json:"letter"`
	Runner string
}

type ReviewResult struct {
	Verdict      string   `json:"verdict"`
	Issues       []string `json:"issues"`
	EditedLetter *string  `json:"edited_letter"`
	Runner       string
}

type (
	Drafter struct {
		Primary, Fallback Runner
		Audit             Audit
	}
	Reviewer struct {
		Primary, Fallback Runner
		Audit             Audit
	}
)

type LetterResult struct {
	Content, Status, ReviewLog, DraftRunner, ReviewRunner string
	Rounds                                                int
}

func (d Drafter) Draft(ctx context.Context, profileYAML string, job Job, issues []string) (DraftResult, error) {
	prompt := draftPrompt(profileYAML, job, issues)
	for _, runner := range []Runner{d.Primary, d.Primary, d.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		raw, err := runner.Invoke(ctx, prompt)
		result, parseErr := parseDraft(raw)
		ok := err == nil && parseErr == nil
		if d.Audit != nil {
			if auditErr := d.Audit("drafter", runner.Name(), prompt, raw, ok, time.Since(start)); auditErr != nil {
				return DraftResult{}, fmt.Errorf("agents: audit drafter call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return DraftResult{}, fmt.Errorf("agents: all drafter runners failed")
}

func (r Reviewer) Review(ctx context.Context, profileYAML string, job Job, letter string) (ReviewResult, error) {
	prompt := reviewPrompt(profileYAML, job, letter)
	for _, runner := range []Runner{r.Primary, r.Primary, r.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		raw, err := runner.Invoke(ctx, prompt)
		result, parseErr := parseReview(raw)
		ok := err == nil && parseErr == nil
		if r.Audit != nil {
			if auditErr := r.Audit("reviewer", runner.Name(), prompt, raw, ok, time.Since(start)); auditErr != nil {
				return ReviewResult{}, fmt.Errorf("agents: audit reviewer call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return ReviewResult{}, fmt.Errorf("agents: all reviewer runners failed")
}

func GenerateLetter(ctx context.Context, drafter Drafter, reviewer Reviewer, profileYAML string, p profile.Profile, job Job, denylist []string, maxLength int) (LetterResult, error) {
	if maxLength <= 0 {
		maxLength = 600
	}
	issues := []string(nil)
	log := make([]string, 0, 3)
	draftRunner, reviewRunner := "", ""
	for round := 1; round <= 3; round++ {
		draft, err := drafter.Draft(ctx, profileYAML, job, issues)
		if err != nil {
			return LetterResult{}, err
		}
		draftRunner = draft.Runner
		if guardErr := Guard(draft.Letter, p, job.Description, denylist, maxLength); guardErr != nil {
			issues = []string{guardErr.Error()}
			log = append(log, "guard: "+guardErr.Error())
			continue
		}
		review, err := reviewer.Review(ctx, profileYAML, job, draft.Letter)
		if err != nil {
			return LetterResult{}, err
		}
		reviewRunner = review.Runner
		if review.Verdict == "approve" {
			content := draft.Letter
			if review.EditedLetter != nil {
				content = *review.EditedLetter
			}
			if guardErr := Guard(content, p, job.Description, denylist, maxLength); guardErr != nil {
				issues = []string{guardErr.Error()}
				log = append(log, "guard: "+guardErr.Error())
				continue
			}
			log = append(log, "approve")
			return LetterResult{Content: content, Status: "approved", ReviewLog: strings.Join(log, "\n"), Rounds: round, DraftRunner: draftRunner, ReviewRunner: reviewRunner}, nil
		}
		log = append(log, "revise: "+strings.Join(review.Issues, "; "))
		issues = review.Issues
	}
	return LetterResult{Status: "failed", ReviewLog: strings.Join(log, "\n"), Rounds: 3, DraftRunner: draftRunner, ReviewRunner: reviewRunner}, nil
}

func Guard(letter string, p profile.Profile, description string, denylist []string, maxLength int) error {
	if !strings.Contains(letter, "[你的姓名]") || !strings.Contains(letter, "[你的聯絡方式]") {
		return fmt.Errorf("guard: missing required signature placeholder")
	}
	for _, placeholder := range regexp.MustCompile(`\[[^\]]+\]`).FindAllString(letter, -1) {
		if placeholder != "[你的姓名]" && placeholder != "[你的聯絡方式]" {
			return fmt.Errorf("guard: unresolved placeholder")
		}
	}
	if len([]rune(letter)) > maxLength {
		return fmt.Errorf("guard: letter exceeds maximum length")
	}
	if err := profile.LintText(letter, denylist); err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	allowed := strings.ToLower(description)
	for _, skills := range [][]string{p.Skills.Expert, p.Skills.Proficient, p.Skills.Familiar} {
		allowed += " " + strings.ToLower(strings.Join(skills, " "))
	}
	for _, term := range regexp.MustCompile(`(?i)\b(?:java|go|golang|python|rust|kubernetes|docker|terraform|aws|gcp|azure|sql|react|typescript)\b`).FindAllString(letter, -1) {
		if !strings.Contains(allowed, strings.ToLower(term)) {
			return fmt.Errorf("guard: unsupported technical term")
		}
	}
	return nil
}

func draftPrompt(profileText string, j Job, issues []string) string {
	return "你是求職信起草器。僅輸出單一 JSON 物件。只可使用 Profile 中的事實，遵守 honesty_bounds，300-450字，結尾必含 [你的姓名] 與 [你的聯絡方式]。\nProfile YAML:\n" + profileText + "\nJob:\n" + j.Title + "\n" + j.Description + "\nReviewer issues:\n" + strings.Join(issues, "\n") + "\n回傳 letter。"
}

func reviewPrompt(profileText string, j Job, letter string) string {
	return "你是嚴格的求職信審查器。僅輸出單一 JSON 物件。檢查 Profile 無依據的技能、經歷、數字、空泛或誇大文字與落款佔位符。回傳 verdict（approve 或 revise）、issues，approve 時可回傳 edited_letter。\nProfile YAML:\n" + profileText + "\nJob:\n" + j.Title + "\n" + j.Description + "\nDraft:\n" + letter
}

func parseDraft(raw string) (DraftResult, error) {
	match := objectPattern.FindString(raw)
	if match == "" {
		return DraftResult{}, fmt.Errorf("agents: response has no JSON object")
	}
	var result DraftResult
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		return result, fmt.Errorf("agents: invalid draft JSON: %w", err)
	}
	if strings.TrimSpace(result.Letter) == "" {
		return result, fmt.Errorf("agents: invalid letter")
	}
	return result, nil
}

func parseReview(raw string) (ReviewResult, error) {
	match := objectPattern.FindString(raw)
	if match == "" {
		return ReviewResult{}, fmt.Errorf("agents: response has no JSON object")
	}
	var result ReviewResult
	if err := json.Unmarshal([]byte(match), &result); err != nil {
		return result, fmt.Errorf("agents: invalid review JSON: %w", err)
	}
	if result.Verdict != "approve" && result.Verdict != "revise" {
		return result, fmt.Errorf("agents: invalid verdict")
	}
	if result.Verdict == "revise" {
		if result.EditedLetter != nil || len(result.Issues) == 0 {
			return result, fmt.Errorf("agents: invalid revise result")
		}
		for _, issue := range result.Issues {
			if strings.TrimSpace(issue) == "" {
				return result, fmt.Errorf("agents: invalid issue")
			}
		}
	}
	return result, nil
}

func scorePrompt(profile string, j Job) string {
	return "你是求職媒合評分器。僅輸出單一 JSON 物件，不要說明。\nProfile YAML:\n" + profile + "\nJob:\ntitle: " + j.Title + "\ncompany: " + j.CompanyName + "\ndescription: " + j.Description + "\nlocation: " + j.Location + "\n請回傳 hard_skill、domain、seniority、condition、direction（皆為 0-100 整數）與 reason（最多50字）。不要計算 total。"
}

var objectPattern = regexp.MustCompile(`(?s)\{.*\}`)

func parseScore(raw string) (ScoreResult, error) {
	match := objectPattern.FindString(raw)
	if match == "" {
		return ScoreResult{}, fmt.Errorf("agents: response has no JSON object")
	}
	var r ScoreResult
	if err := json.Unmarshal([]byte(match), &r); err != nil {
		return r, fmt.Errorf("agents: invalid score JSON: %w", err)
	}
	for _, v := range []int{r.HardSkill, r.Domain, r.Seniority, r.Condition, r.Direction} {
		if v < 0 || v > 100 {
			return r, fmt.Errorf("agents: score out of range")
		}
	}
	if strings.TrimSpace(r.Reason) == "" || len([]rune(r.Reason)) > 50 {
		return r, fmt.Errorf("agents: invalid reason")
	}
	return r, nil
}
