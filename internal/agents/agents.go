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
	"runtime"
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
	c := agentCommand(ctx, r.Command, args)
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

// agentCommand builds the process for one Agent invocation, resolving the
// executable and then applying the platform's window policy to it.
func agentCommand(ctx context.Context, command string, args []string) *exec.Cmd {
	cmd := resolveAgentCommand(ctx, command, args)
	hideConsole(cmd)
	return cmd
}

// resolveAgentCommand decides what to execute.
//
// On Windows an npm-installed CLI is a .cmd shim, and CreateProcess cannot
// execute a batch file: handed one directly the call fails with an unhelpful
// "not a valid application" rather than anything that points at the shim. Such
// a command is therefore run through the command interpreter. Everywhere else,
// and for a real executable on Windows, the command is executed directly.
func resolveAgentCommand(ctx context.Context, command string, args []string) *exec.Cmd {
	// #nosec G204 -- Command and flags are fixed runner definitions; prompt is one CLI argument.
	direct := func() *exec.Cmd { return exec.CommandContext(ctx, command, args...) }
	if runtime.GOOS != "windows" {
		return direct()
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return direct()
	}
	switch strings.ToLower(filepath.Ext(resolved)) {
	case ".cmd", ".bat":
		interpreter := os.Getenv("COMSPEC")
		if interpreter == "" {
			interpreter = "cmd.exe"
		}
		// #nosec G204 G702 -- the interpreter is fixed and the shim path comes from PATH lookup of a configured runner.
		return exec.CommandContext(ctx, interpreter, append([]string{"/c", resolved}, args...)...)
	default:
		// #nosec G204 -- resolved is the PATH lookup of a configured runner name.
		return exec.CommandContext(ctx, resolved, args...)
	}
}

// ClaudeRunner and CodexRunner take an optional command override so an
// installation can name the executable outright when the plain name is not on
// the service's PATH.
func ClaudeRunner(command, model string, timeout time.Duration) Runner {
	if command == "" {
		command = "claude"
	}
	return CommandRunner{
		RunnerName:     "claude",
		Command:        command,
		RunnerModel:    model,
		Args:           []string{"-p", "--model", model, "--output-format", "json"},
		PromptViaStdin: true,
		ResultEnvelope: true,
		Timeout:        timeout,
	}
}

func CodexRunner(command, model string, timeout time.Duration) Runner {
	if command == "" {
		command = "codex"
	}
	return CommandRunner{
		RunnerName:       "codex",
		Command:          command,
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
	Verdict      string       `json:"verdict"`
	Issues       ReviewIssues `json:"issues"`
	EditedLetter *string      `json:"edited_letter"`
	Runner       string
}

// ReviewIssues is a list of one-sentence problems. The contract is an array of
// strings, but a model that answers with objects is not wrong about the review
// itself — flattening those keeps one loose field from discarding a whole draft
// and review round.
type ReviewIssues []string

func (r *ReviewIssues) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	issues := make(ReviewIssues, 0, len(raw))
	for _, item := range raw {
		var text string
		if err := json.Unmarshal(item, &text); err == nil {
			issues = append(issues, text)
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(item, &fields); err != nil {
			return fmt.Errorf("agents: issue is neither string nor object")
		}
		issues = append(issues, flattenIssue(fields))
	}
	*r = issues
	return nil
}

// flattenIssue turns an object-shaped issue into the one sentence the contract
// asks for, preferring the fields that carry the problem itself over labels.
func flattenIssue(fields map[string]any) string {
	parts := make([]string, 0, 2)
	for _, key := range []string{"issue", "problem", "description", "detail", "message", "text", "comment", "suggestion"} {
		if text, ok := fields[key].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
			break
		}
	}
	if len(parts) == 0 {
		for _, key := range []string{"type", "category", "field", "quote", "excerpt"} {
			if text, ok := fields[key].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
				break
			}
		}
	}
	if quote, ok := fields["quote"].(string); ok && strings.TrimSpace(quote) != "" && len(parts) == 1 && parts[0] != strings.TrimSpace(quote) {
		parts = append(parts, "原文："+strings.TrimSpace(quote))
	}
	return strings.Join(parts, "；")
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

// LetterRound is one produced draft together with the issues raised against it.
// The drafter receives every past round, not only the latest issues: without the
// text that was criticised it rewrites from scratch each time and repeats the
// mistakes earlier rounds already paid to find.
type LetterRound struct {
	Letter string
	Issues []string
}

func (d Drafter) Draft(ctx context.Context, profileYAML string, job Job, history []LetterRound) (DraftResult, error) {
	prompt := draftPrompt(profileYAML, job, history)
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

// GenerateLetter runs at most maxRounds rounds. The first maxRounds-1 rounds are
// draft plus review; the last round is drafted from the accumulated history and
// returned unreviewed. A reviewer can always raise something, so treating its
// verdict as a gate on the final round would discard every round's work; the
// version it has not seen is the deliberate result instead.
//
// A failed Agent call ends the whole generation: the runner layer already tried
// primary, primary and fallback, so retrying the round only burns the daily
// budget while the service is down. A guard failure is not a failed call — the
// draft arrived and is merely unusable, so it becomes the next round's issue.
func GenerateLetter(ctx context.Context, drafter Drafter, reviewer Reviewer, profileYAML string, p profile.Profile, job Job, denylist []string, maxLength, maxRounds int) (LetterResult, error) {
	if maxLength <= 0 {
		maxLength = 600
	}
	if maxRounds <= 0 {
		maxRounds = 3
	}
	history := []LetterRound(nil)
	log := make([]string, 0, maxRounds)
	draftRunner, reviewRunner := "", ""
	result := func(content, status string, rounds int) LetterResult {
		return LetterResult{Content: content, Status: status, ReviewLog: strings.Join(log, "\n"), Rounds: rounds, DraftRunner: draftRunner, ReviewRunner: reviewRunner}
	}
	for round := 1; round <= maxRounds; round++ {
		draft, err := drafter.Draft(ctx, profileYAML, job, history)
		if err != nil {
			log = append(log, "error: "+err.Error())
			return result("", "failed", round), err
		}
		draftRunner = draft.Runner
		guardErr := Guard(draft.Letter, p, job.Description, denylist, maxLength)
		if round == maxRounds {
			if guardErr != nil {
				log = append(log, "guard: "+guardErr.Error())
				return result("", "failed", round), nil
			}
			log = append(log, "finalized")
			return result(draft.Letter, "finalized", round), nil
		}
		if guardErr != nil {
			log = append(log, "guard: "+guardErr.Error())
			history = append(history, LetterRound{Letter: draft.Letter, Issues: []string{guardErr.Error()}})
			continue
		}
		review, err := reviewer.Review(ctx, profileYAML, job, draft.Letter)
		if err != nil {
			log = append(log, "error: "+err.Error())
			return result("", "failed", round), err
		}
		reviewRunner = review.Runner
		if review.Verdict == "approve" {
			content := draft.Letter
			if review.EditedLetter != nil {
				content = *review.EditedLetter
			}
			if editedErr := Guard(content, p, job.Description, denylist, maxLength); editedErr != nil {
				log = append(log, "guard: "+editedErr.Error())
				history = append(history, LetterRound{Letter: content, Issues: []string{editedErr.Error()}})
				continue
			}
			log = append(log, "approve")
			return result(content, "approved", round), nil
		}
		log = append(log, "revise: "+strings.Join(review.Issues, "; "))
		history = append(history, LetterRound{Letter: draft.Letter, Issues: review.Issues})
	}
	return result("", "failed", maxRounds), fmt.Errorf("agents: letter loop ended without a result")
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

func draftPrompt(profileText string, j Job, history []LetterRound) string {
	return "你是求職信起草器。僅輸出單一 JSON 物件。只可使用 Profile 中的事實，遵守 honesty_bounds，300-450字，結尾必含 [你的姓名] 與 [你的聯絡方式]。\n" +
		"[你的姓名] 與 [你的聯絡方式] 是刻意保留的落款佔位符，原樣輸出，不得替換為任何真實姓名或聯絡方式，也不得出現其他 [ ] 佔位符。\n" +
		"Profile YAML:\n" + profileText + "\nJob:\n" + j.Title + "\n" + j.Description + "\n" + draftHistory(history) + "回傳 letter。"
}

// draftHistory lays out every past version with the issues raised against it, so
// the next draft fixes those problems while keeping what was not criticised.
func draftHistory(history []LetterRound) string {
	if len(history) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("以下是先前各版草稿與對它們的審查意見。請逐條修正這些問題，保留未被指出問題的內容，產出新的一版。\n")
	for i, round := range history {
		version := strconv.Itoa(i + 1)
		b.WriteString("第" + version + "版草稿:\n" + round.Letter + "\n第" + version + "版審查意見:\n" + strings.Join(round.Issues, "\n") + "\n")
	}
	return b.String()
}

func reviewPrompt(profileText string, j Job, letter string) string {
	return "你是嚴格的求職信審查器。僅輸出單一 JSON 物件。檢查 Profile 無依據的技能、經歷、數字，以及空泛或誇大的文字。\n" +
		"落款的 [你的姓名] 與 [你的聯絡方式] 是刻意保留的成品形態，由使用者投遞前自行填寫；要求以真實姓名或聯絡方式取代它們屬於錯誤意見，不得提出。應檢查的是這兩個佔位符是否完整存在，以及是否出現其他未解析的 [ ] 佔位符。\n" +
		"輸出格式：verdict 為 approve 或 revise；issues 為字串陣列，每個元素是一個完整句子、描述一項具體問題，不得為物件或巢狀結構；verdict 為 revise 時 issues 不得為空；approve 時可回傳 edited_letter（字串），revise 時不得回傳 edited_letter。\n" +
		"Profile YAML:\n" + profileText + "\nJob:\n" + j.Title + "\n" + j.Description + "\nDraft:\n" + letter
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
