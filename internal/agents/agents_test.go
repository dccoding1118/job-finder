package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/profile"
)

type fakeRunner struct {
	name    string
	replies []string
	err     error
}

func (f *fakeRunner) Name() string  { return f.name }
func (f *fakeRunner) Model() string { return f.name }
func (f *fakeRunner) Invoke(context.Context, string) (Reply, error) {
	if f.err != nil {
		return Reply{}, f.err
	}
	if len(f.replies) == 0 {
		return Reply{}, errors.New("no reply")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return Reply{Text: reply}, nil
}

func TestGenerateLetterApprovesEditedLetter(t *testing.T) {
	p := profile.Profile{Qualifications: profile.Qualifications{Skills: []profile.SkillEntry{{Name: "Go", Level: "expert"}}}}
	draft := "我使用 Go 交付服務。[你的姓名][你的聯絡方式]"
	edited := "我使用 Go 交付服務並持續改善。[你的姓名][你的聯絡方式]"
	result, err := GenerateLetter(context.Background(), Drafter{Primary: &fakeRunner{name: "claude", replies: []string{`{"letter":"` + draft + `"}`}}}, Reviewer{Primary: &fakeRunner{name: "codex", replies: []string{`{"verdict":"approve","issues":[],"edited_letter":"` + edited + `"}`}}}, "profile", p, Job{Description: "Go services"}, nil, 600)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "approved" || result.Content != edited || result.Rounds != 1 || result.DraftRunner != "claude" || result.ReviewRunner != "codex" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGenerateLetterFailsAfterThreeRevisions(t *testing.T) {
	p := profile.Profile{Qualifications: profile.Qualifications{Skills: []profile.SkillEntry{{Name: "Go", Level: "expert"}}}}
	letter := "Go。[你的姓名][你的聯絡方式]"
	drafter := &fakeRunner{name: "claude", replies: []string{`{"letter":"` + letter + `"}`, `{"letter":"` + letter + `"}`, `{"letter":"` + letter + `"}`}}
	reviewer := &fakeRunner{name: "codex", replies: []string{`{"verdict":"revise","issues":["精簡"]}`, `{"verdict":"revise","issues":["具體化"]}`, `{"verdict":"revise","issues":["仍需修改"]}`}}
	result, err := GenerateLetter(context.Background(), Drafter{Primary: drafter}, Reviewer{Primary: reviewer}, "profile", p, Job{Description: "Go"}, nil, 600)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || result.Rounds != 3 || !strings.Contains(result.ReviewLog, "仍需修改") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestGuardRejectsUnsupportedTermAndPII(t *testing.T) {
	p := profile.Profile{Qualifications: profile.Qualifications{Skills: []profile.SkillEntry{{Name: "Go", Level: "expert"}}}}
	base := "Go 與 Rust。\n[你的姓名]\n[你的聯絡方式]"
	if err := Guard(base, p, "Go", nil, 600); err == nil {
		t.Fatal("Guard accepted unsupported technical term")
	}
	mail := strings.Join([]string{"contact", "@", "example", ".invalid"}, "")
	if err := Guard("Go "+mail+"\n[你的姓名]\n[你的聯絡方式]", p, "Go", nil, 600); err == nil {
		t.Fatal("Guard accepted PII")
	}
}

func TestGuardAcceptsSkillFromExperience(t *testing.T) {
	// SQL is a real skill listed under an experience, not the top-level skills
	// buckets; the guard must treat it as supported rather than a hallucination.
	p := profile.Profile{
		Qualifications: profile.Qualifications{Skills: []profile.SkillEntry{{Name: "Go", Level: "proficient"}}},
		Experiences:    []profile.Experience{{Skills: []string{"Go", "SQL"}}},
	}
	if err := Guard("使用 Go 與 SQL 交付服務。\n[你的姓名]\n[你的聯絡方式]", p, "backend role", nil, 600); err != nil {
		t.Fatalf("Guard rejected a skill listed under experiences: %v", err)
	}
}

func TestCommandRunnerUsesConfiguredArgsAndTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	command := writeTestExecutable(t, "#!/bin/sh\nprintf '%s\\n' \"$PWD\"\nprintf '%s\\n' \"$@\"\n")
	runner := CommandRunner{RunnerName: "test", Command: command, Args: []string{"--model", "configured-model"}, TempRoot: root, Timeout: time.Second}
	reply, err := runner.Invoke(context.Background(), "synthetic prompt")
	if err != nil {
		t.Fatal(err)
	}
	output := reply.Text
	if !strings.Contains(output, "--model\nconfigured-model\nsynthetic prompt") || !strings.Contains(output, root+string(os.PathSeparator)+"jobfinder-agent-") {
		t.Fatalf("unexpected command output: %q", output)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary directory was not cleaned: %v, %v", entries, err)
	}
}

func TestCommandRunnerReportsTimeoutAndNonZeroExit(t *testing.T) {
	slow := writeTestExecutable(t, "#!/bin/sh\nsleep 2\n")
	if _, err := (CommandRunner{RunnerName: "slow", Command: slow, Timeout: 10 * time.Millisecond}).Invoke(context.Background(), "prompt"); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("timeout error = %v", err)
	}
	failing := writeTestExecutable(t, "#!/bin/sh\nexit 7\n")
	if _, err := (CommandRunner{RunnerName: "failing", Command: failing, Timeout: time.Second}).Invoke(context.Background(), "prompt"); err == nil {
		t.Fatal("non-zero command succeeded")
	}
}

func TestRunnerDefinitionsPinModelsAndNonInteractiveSafetyFlags(t *testing.T) {
	claude := ClaudeRunner("", "claude-sonnet-5", time.Minute).(CommandRunner)
	codex := CodexRunner("", "gpt-5.6-terra", time.Minute).(CommandRunner)
	if claude.Command != "claude" || codex.Command != "codex" {
		t.Fatalf("an empty override must fall back to the plain command name: %q, %q", claude.Command, codex.Command)
	}
	if overridden := ClaudeRunner(`C:\tools\claude.cmd`, "claude-sonnet-5", time.Minute).(CommandRunner); overridden.Command != `C:\tools\claude.cmd` {
		t.Fatalf("command override = %q, want the configured path", overridden.Command)
	}
	claudeArgs := strings.Join(claude.Args, " ")
	codexArgs := strings.Join(codex.Args, " ")
	if !strings.Contains(claudeArgs, "--model claude-sonnet-5") || !strings.Contains(claudeArgs, "--output-format json") {
		t.Fatalf("unsafe or incomplete Claude args: %v", claude.Args)
	}
	if !claude.PromptViaStdin || !claude.ResultEnvelope {
		t.Fatalf("Claude runner must send the prompt on stdin and read the result envelope: %+v", claude)
	}
	if !strings.Contains(codexArgs, "--model gpt-5.6-terra") || !strings.Contains(codexArgs, "--ephemeral") || !strings.Contains(codexArgs, "--sandbox read-only") || !strings.Contains(codexArgs, "--skip-git-repo-check") {
		t.Fatalf("unsafe or incomplete Codex args: %v", codex.Args)
	}
	if codex.LastMessageFlag != "-o" || codex.PromptViaStdin {
		t.Fatalf("Codex runner must take the prompt as an argument and read its final message file: %+v", codex)
	}
}

func TestCommandRunnerReadsStdinPromptAndResultEnvelope(t *testing.T) {
	command := writeTestExecutable(t, "#!/bin/sh\nprompt=$(cat)\nprintf '{\"subtype\":\"success\",\"is_error\":false,\"result\":\"%s\",\"usage\":{\"input_tokens\":12,\"output_tokens\":34},\"total_cost_usd\":0.5}\\n' \"$prompt\"\n")
	runner := CommandRunner{RunnerName: "test", Command: command, PromptViaStdin: true, ResultEnvelope: true, Timeout: time.Second}
	reply, err := runner.Invoke(context.Background(), "synthetic prompt")
	if err != nil || reply.Text != "synthetic prompt" {
		t.Fatalf("envelope result = %q, %v", reply.Text, err)
	}
	if reply.Usage.InputTokens != 12 || reply.Usage.OutputTokens != 34 || reply.Usage.CostUSD != 0.5 {
		t.Fatalf("envelope usage = %+v", reply.Usage)
	}
	failing := writeTestExecutable(t, "#!/bin/sh\nprintf '{\"subtype\":\"error_during_execution\",\"is_error\":true,\"result\":\"\"}\\n'\n")
	if _, err := (CommandRunner{RunnerName: "test", Command: failing, PromptViaStdin: true, ResultEnvelope: true, Timeout: time.Second}).Invoke(context.Background(), "prompt"); err == nil {
		t.Fatal("accepted an error envelope")
	}
}

func TestCommandRunnerReadsFinalMessageFile(t *testing.T) {
	command := writeTestExecutable(t, "#!/bin/sh\nprintf 'noisy transcript\\n'\nwhile [ \"$1\" != '-o' ]; do shift; done\nprintf '{\"letter\":\"final\"}\\n' >\"$2\"\n")
	runner := CommandRunner{RunnerName: "test", Command: command, LastMessageFlag: "-o", Timeout: time.Second}
	reply, err := runner.Invoke(context.Background(), "synthetic prompt")
	if err != nil || strings.TrimSpace(reply.Text) != `{"letter":"final"}` {
		t.Fatalf("final message = %q, %v", reply.Text, err)
	}
}

func TestExtractObjectIgnoresTranscriptNoiseAndRepeatedAnswers(t *testing.T) {
	answer := `{"content_fit":5,"reason":"合成"}`
	// A CLI that echoes the prompt and then prints its answer twice must not be
	// parsed as one object spanning the noise between the copies.
	raw := "user\n請回傳 {範例}\ncodex\n" + answer + "\ntokens used\n8,006\n" + answer
	if got := extractObject(raw); got != answer {
		t.Fatalf("extractObject = %q", got)
	}
	nested := `{"verdict":"approve","issues":[],"meta":{"a":1}}`
	if got := extractObject("prose before\n```json\n" + nested + "\n```\n"); got != nested {
		t.Fatalf("nested extractObject = %q", got)
	}
	if got := extractObject(`{"reason":"brace } inside string"}`); got != `{"reason":"brace } inside string"}` {
		t.Fatalf("string-aware extractObject = %q", got)
	}
	if got := extractObject("no object here"); got != "" {
		t.Fatalf("expected no match, got %q", got)
	}
}

func writeTestExecutable(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- this isolated test fixture must be executable.
		t.Fatal(err)
	}
	return path
}

// The JSONL runner reports tokens but never a price. Reading it must keep the
// cost at zero rather than deriving one — an audited cost has to be what the
// runner actually charged, not what a local price table guessed.
func TestJSONLUsageTakesTheCompletedTurnAndPricesNothing(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"turn.started"}`,
		"not a JSON line at all",
		`{"type":"turn.completed","usage":{"input_tokens":700,"cached_input_tokens":500,"cache_write_input_tokens":20,"output_tokens":80,"reasoning_output_tokens":40}}`,
	}, "\n")
	usage := jsonlUsage(raw)
	if usage.InputTokens != 700 || usage.CacheReadTokens != 500 || usage.CacheWriteTokens != 20 || usage.OutputTokens != 80 || usage.ReasoningTokens != 40 {
		t.Fatalf("jsonl usage = %+v", usage)
	}
	if usage.CostUSD != 0 {
		t.Fatalf("a runner that does not price its calls must report no cost: %v", usage.CostUSD)
	}
	if empty := jsonlUsage(`{"type":"turn.started"}`); empty != (Usage{}) {
		t.Fatalf("a stream without a completed turn reports no usage: %+v", empty)
	}
}

// An envelope without usage is still a valid answer; only the accounting is
// missing, and it must read as zero rather than failing the call.
func TestResultEnvelopeWithoutUsageStillParses(t *testing.T) {
	command := writeTestExecutable(t, "#!/bin/sh\ncat >/dev/null\nprintf '{\"subtype\":\"success\",\"is_error\":false,\"result\":\"answer\"}\\n'\n")
	runner := CommandRunner{RunnerName: "test", Command: command, PromptViaStdin: true, ResultEnvelope: true, Timeout: time.Second}
	reply, err := runner.Invoke(context.Background(), "prompt")
	if err != nil || reply.Text != "answer" {
		t.Fatalf("reply = %q, %v", reply.Text, err)
	}
	if reply.Usage != (Usage{}) {
		t.Fatalf("missing usage must read as zero: %+v", reply.Usage)
	}
}
