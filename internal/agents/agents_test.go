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

func (f *fakeRunner) Name() string { return f.name }
func (f *fakeRunner) Invoke(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if len(f.replies) == 0 {
		return "", errors.New("no reply")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}

func TestGenerateLetterApprovesEditedLetter(t *testing.T) {
	p := profile.Profile{Skills: profile.Skills{Expert: []string{"Go"}}}
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
	p := profile.Profile{Skills: profile.Skills{Expert: []string{"Go"}}}
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
	p := profile.Profile{Skills: profile.Skills{Expert: []string{"Go"}}}
	base := "Go 與 Rust。\n[你的姓名]\n[你的聯絡方式]"
	if err := Guard(base, p, "Go", nil, 600); err == nil {
		t.Fatal("Guard accepted unsupported technical term")
	}
	mail := strings.Join([]string{"contact", "@", "example", ".invalid"}, "")
	if err := Guard("Go "+mail+"\n[你的姓名]\n[你的聯絡方式]", p, "Go", nil, 600); err == nil {
		t.Fatal("Guard accepted PII")
	}
}

func TestCommandRunnerUsesConfiguredArgsAndTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	command := writeTestExecutable(t, "#!/bin/sh\nprintf '%s\\n' \"$PWD\"\nprintf '%s\\n' \"$@\"\n")
	runner := CommandRunner{RunnerName: "test", Command: command, Args: []string{"--model", "configured-model"}, TempRoot: root, Timeout: time.Second}
	output, err := runner.Invoke(context.Background(), "synthetic prompt")
	if err != nil {
		t.Fatal(err)
	}
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
	claude := ClaudeRunner("claude-sonnet-5", time.Minute).(CommandRunner)
	codex := CodexRunner("gpt-5.6-terra", time.Minute).(CommandRunner)
	claudeArgs := strings.Join(claude.Args, " ")
	codexArgs := strings.Join(codex.Args, " ")
	if !strings.Contains(claudeArgs, "--model claude-sonnet-5") || !strings.Contains(claudeArgs, "--tools ") || !strings.Contains(claudeArgs, "--output-format text") {
		t.Fatalf("unsafe or incomplete Claude args: %v", claude.Args)
	}
	if !strings.Contains(codexArgs, "--model gpt-5.6-terra") || !strings.Contains(codexArgs, "--ephemeral") || !strings.Contains(codexArgs, "--sandbox read-only") || !strings.Contains(codexArgs, "--skip-git-repo-check") {
		t.Fatalf("unsafe or incomplete Codex args: %v", codex.Args)
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
