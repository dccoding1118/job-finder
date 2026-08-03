// Package agents provides structured LLM scoring through CLI runners.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/profile"
)

type (
	// Usage is the token accounting an Agent call reports, read straight out of
	// the CLI's own structured output rather than estimated. CostUSD is 0 when
	// the runner does not price its own calls (codex reports tokens, not cost).
	Usage struct {
		InputTokens      int
		OutputTokens     int
		CacheReadTokens  int
		CacheWriteTokens int
		ReasoningTokens  int
		CostUSD          float64
	}
	// Reply is a Runner's answer: the text every parser reads, plus the usage
	// that answer cost.
	Reply struct {
		Text  string
		Usage Usage
	}
	Runner interface {
		Name() string
		Model() string
		Invoke(context.Context, string) (Reply, error)
	}
	CommandRunner struct {
		RunnerName, Command, RunnerModel string
		Args                             []string
		// PromptViaStdin writes the prompt to stdin instead of appending it as the final argument.
		PromptViaStdin bool
		// ResultEnvelope reads stdout as the `claude --output-format json` envelope,
		// whose "result" field carries the response and "usage"/"total_cost_usd" carry cost.
		ResultEnvelope bool
		// LastMessageFlag, when set, receives a temporary file that the CLI writes its final message to.
		LastMessageFlag string
		// JSONLUsageEvents reads stdout as `codex exec --json` event lines and takes
		// usage from the last "turn.completed" event.
		JSONLUsageEvents bool
		TempRoot         string
		Timeout          time.Duration
	}
)

func (r CommandRunner) Name() string  { return r.RunnerName }
func (r CommandRunner) Model() string { return r.RunnerModel }
func (r CommandRunner) Invoke(ctx context.Context, prompt string) (Reply, error) {
	if strings.TrimSpace(r.Command) == "" {
		return Reply{}, fmt.Errorf("agents: %s: command is required", r.RunnerName)
	}
	dir, err := os.MkdirTemp(r.TempRoot, "jobfinder-agent-*")
	if err != nil {
		return Reply{}, fmt.Errorf("agents: %s: create temporary directory: %w", r.RunnerName, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	args := append([]string(nil), r.Args...)
	lastMessage := filepath.Join(dir, "last-message.txt")
	if r.LastMessageFlag != "" {
		args = append(args, r.LastMessageFlag, lastMessage)
	}
	if !r.PromptViaStdin {
		args = append(args, prompt)
	}
	// #nosec G204 -- Command and flags are fixed runner definitions; prompt is one CLI argument.
	c := exec.CommandContext(ctx, r.Command, args...)
	c.Dir = dir
	if r.PromptViaStdin {
		c.Stdin = strings.NewReader(prompt)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
		if ctx.Err() != nil {
			return Reply{Text: detail}, fmt.Errorf("agents: %s: %w", r.RunnerName, ctx.Err())
		}
		return Reply{Text: detail}, fmt.Errorf("agents: %s: %w: %s", r.RunnerName, err, detail)
	}
	usage := Usage{}
	if r.JSONLUsageEvents {
		usage = jsonlUsage(stdout.String())
	}
	if r.LastMessageFlag != "" {
		message, err := os.ReadFile(lastMessage) // #nosec G304 -- path is created inside the per-invocation temporary directory.
		if err != nil {
			return Reply{Text: stdout.String(), Usage: usage}, fmt.Errorf("agents: %s: read final message: %w", r.RunnerName, err)
		}
		return Reply{Text: string(message), Usage: usage}, nil
	}
	if r.ResultEnvelope {
		text, usage, err := envelopeResult(r.RunnerName, stdout.String())
		return Reply{Text: text, Usage: usage}, err
	}
	return Reply{Text: stdout.String()}, nil
}

// envelopeResult unwraps the JSON envelope that `claude --output-format json`
// prints: "result" carries the response text, "usage" and "total_cost_usd"
// carry what that response cost.
func envelopeResult(runner, raw string) (string, Usage, error) {
	var envelope struct {
		Subtype      string  `json:"subtype"`
		IsError      bool    `json:"is_error"`
		Result       string  `json:"result"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &envelope); err != nil {
		return raw, Usage{}, fmt.Errorf("agents: %s: invalid result envelope: %w", runner, err)
	}
	usage := Usage{
		InputTokens:      envelope.Usage.InputTokens,
		OutputTokens:     envelope.Usage.OutputTokens,
		CacheReadTokens:  envelope.Usage.CacheReadInputTokens,
		CacheWriteTokens: envelope.Usage.CacheCreationInputTokens,
		CostUSD:          envelope.TotalCostUSD,
	}
	if envelope.IsError || envelope.Subtype != "success" {
		return raw, usage, fmt.Errorf("agents: %s: reported %q", runner, envelope.Subtype)
	}
	return envelope.Result, usage, nil
}

// jsonlUsage reads `codex exec --json` event lines and takes usage from the
// last "turn.completed" event; codex does not price its own calls, so CostUSD
// stays 0.
func jsonlUsage(raw string) Usage {
	var usage Usage
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Usage struct {
				InputTokens           int `json:"input_tokens"`
				CachedInputTokens     int `json:"cached_input_tokens"`
				CacheWriteInputTokens int `json:"cache_write_input_tokens"`
				OutputTokens          int `json:"output_tokens"`
				ReasoningOutputTokens int `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type != "turn.completed" {
			continue
		}
		usage = Usage{
			InputTokens:      event.Usage.InputTokens,
			OutputTokens:     event.Usage.OutputTokens,
			CacheReadTokens:  event.Usage.CachedInputTokens,
			CacheWriteTokens: event.Usage.CacheWriteInputTokens,
			ReasoningTokens:  event.Usage.ReasoningOutputTokens,
		}
	}
	return usage
}

func ClaudeRunner(model string, timeout time.Duration) Runner {
	return CommandRunner{
		RunnerName:     "claude",
		Command:        "claude",
		RunnerModel:    model,
		Args:           []string{"-p", "--model", model, "--output-format", "json"},
		PromptViaStdin: true,
		ResultEnvelope: true,
		Timeout:        timeout,
	}
}

func CodexRunner(model string, timeout time.Duration) Runner {
	return CommandRunner{
		RunnerName:       "codex",
		Command:          "codex",
		RunnerModel:      model,
		Args:             []string{"exec", "--model", model, "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never", "--json"},
		LastMessageFlag:  "-o",
		JSONLUsageEvents: true,
		Timeout:          timeout,
	}
}

type (
	// ScoreResult is the soft-rule assessment. Each dimension starts from the
	// configured baseline and moves from there, so "nothing in the JD to judge
	// this on" answers with the baseline rather than a zero.
	ScoreResult struct {
		Content  int    `json:"content_fit"`
		Benefit  int    `json:"benefit_fit"`
		Bonus    int    `json:"bonus_fit"`
		Industry int    `json:"industry_fit"`
		Reason   string `json:"reason"`
		Runner   string
	}
	Audit  func(role, runner, model, input, output string, ok bool, duration time.Duration, usage Usage) error
	Scorer struct {
		Primary, Fallback Runner
		Audit             Audit
	}
)

func (s Scorer) Score(ctx context.Context, profileYAML string, job Job, baseline int) (ScoreResult, error) {
	prompt := scorePrompt(profileYAML, job, baseline)
	var lastErr error
	for _, runner := range []Runner{s.Primary, s.Primary, s.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		reply, err := runner.Invoke(ctx, prompt)
		raw := reply.Text
		result, parseErr := parseScore(raw)
		ok := err == nil && parseErr == nil
		if !ok {
			lastErr = invocationError(err, parseErr)
		}
		if s.Audit != nil {
			if auditErr := s.Audit("scorer", runner.Name(), runner.Model(), prompt, raw, ok, time.Since(start), reply.Usage); auditErr != nil {
				return ScoreResult{}, fmt.Errorf("agents: audit scorer call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return ScoreResult{}, runnersFailed("scorer", lastErr)
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
	var lastErr error
	for _, runner := range []Runner{d.Primary, d.Primary, d.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		reply, err := runner.Invoke(ctx, prompt)
		raw := reply.Text
		result, parseErr := parseDraft(raw)
		ok := err == nil && parseErr == nil
		if !ok {
			lastErr = invocationError(err, parseErr)
		}
		if d.Audit != nil {
			if auditErr := d.Audit("drafter", runner.Name(), runner.Model(), prompt, raw, ok, time.Since(start), reply.Usage); auditErr != nil {
				return DraftResult{}, fmt.Errorf("agents: audit drafter call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return DraftResult{}, runnersFailed("drafter", lastErr)
}

func (r Reviewer) Review(ctx context.Context, profileYAML string, job Job, letter string) (ReviewResult, error) {
	prompt := reviewPrompt(profileYAML, job, letter)
	var lastErr error
	for _, runner := range []Runner{r.Primary, r.Primary, r.Fallback} {
		if runner == nil {
			continue
		}
		start := time.Now()
		reply, err := runner.Invoke(ctx, prompt)
		raw := reply.Text
		result, parseErr := parseReview(raw)
		ok := err == nil && parseErr == nil
		if !ok {
			lastErr = invocationError(err, parseErr)
		}
		if r.Audit != nil {
			if auditErr := r.Audit("reviewer", runner.Name(), runner.Model(), prompt, raw, ok, time.Since(start), reply.Usage); auditErr != nil {
				return ReviewResult{}, fmt.Errorf("agents: audit reviewer call: %w", auditErr)
			}
		}
		if ok {
			result.Runner = runner.Name()
			return result, nil
		}
	}
	return ReviewResult{}, runnersFailed("reviewer", lastErr)
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
	// The whitelist is every skill the Profile states — the totals list plus each
	// experience's own — together with the JD itself.
	allowed := strings.ToLower(description) + " " + strings.ToLower(strings.Join(p.SkillNames(), " "))
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
	match := extractObject(raw)
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
	match := extractObject(raw)
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

// scorePrompt asks only what the soft rules can answer. baseline is the score a
// dimension takes when the JD says nothing to judge it on — a neutral answer,
// not a bad one.
func scorePrompt(profileYAML string, j Job, baseline int) string {
	return `你是求職媒合評分器。僅輸出單一 JSON 物件，不要說明。

四個維度皆以基準分 ` + strconv.Itoa(baseline) + ` 為起點加減，範圍 0-100；該維度在 JD 中無資訊可判時，回基準分。
- content_fit：工作內容對照 intents.content_likes（加分）與 content_dislikes（扣分）。
- benefit_fit：薪資對照 salary_target，另計優於勞基法的休假、不打卡或彈性工時、額外獎金；遠端形式依 remote 意願加分（preferred 時 full 加較多、hybrid 加較少、onsite 不加）。
- bonus_fit：JD 的加分條件對照 skills／certifications／languages。**只加不減**：JD 列出而你沒有的加分項不扣分。
- industry_fit：公司產品或服務所屬領域對照 intents.industry_interests。

Profile（軟條件子集）:
` + profileYAML + `
Job:
title: ` + j.Title + `
company: ` + j.CompanyName + `
location: ` + j.Location + `
remote: ` + j.RemoteType + `
description: ` + j.Description + `

回傳 content_fit、benefit_fit、bonus_fit、industry_fit（皆為 0-100 整數）與 reason（40~60 字，勿超過；中文一字算一字，連續的英文詞或數字整段只算一字）。不要計算 total。`
}

// extractObject returns the last balanced top-level JSON object in raw, ignoring
// braces inside strings. A CLI may print its answer more than once or wrap it in
// prose or code fences; taking the last complete object keeps those intact.
func extractObject(raw string) string {
	var last string
	depth, start := 0, 0
	inString, escaped := false, false
	for i, ch := range raw {
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 {
					last = raw[start : i+len("}")]
				}
			}
		}
	}
	return last
}

func parseScore(raw string) (ScoreResult, error) {
	match := extractObject(raw)
	if match == "" {
		return ScoreResult{}, fmt.Errorf("agents: response has no JSON object")
	}
	var r ScoreResult
	if err := json.Unmarshal([]byte(match), &r); err != nil {
		return r, fmt.Errorf("agents: invalid score JSON: %w", err)
	}
	for _, v := range []int{r.Content, r.Benefit, r.Bonus, r.Industry} {
		if v < 0 || v > 100 {
			return r, fmt.Errorf("agents: score out of range")
		}
	}
	if strings.TrimSpace(r.Reason) == "" || ReasonLength(r.Reason) > maxScoreReason {
		return r, fmt.Errorf("agents: invalid reason")
	}
	return r, nil
}

// invocationError reports why one runner attempt was rejected: the process error
// when the CLI itself failed, otherwise the response validation error.
func invocationError(invokeErr, parseErr error) error {
	if invokeErr != nil {
		return invokeErr
	}
	return parseErr
}

// runnersFailed reports that every runner for a role was exhausted, keeping the
// last underlying cause so failures are diagnosable without reading the audit table.
func runnersFailed(role string, cause error) error {
	if cause == nil {
		return fmt.Errorf("agents: all %s runners failed", role)
	}
	return fmt.Errorf("agents: all %s runners failed: %w", role, cause)
}
