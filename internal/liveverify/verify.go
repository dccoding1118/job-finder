// Package liveverify is the live verification of an installed test environment
// (docs/verify.md §6), run as `jobfinder verify live`.
//
// A test environment is a long-lived installation: the resident worker is
// running and the user sets its switches. Each run therefore arranges the state
// it needs, verifies one job per Agent role through the resident worker's
// single-job entries, and puts the settings back the way it found them. Linux
// and Windows share this implementation; only the service and schedule
// operations differ (platform.go).
package liveverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/paths"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

const (
	// maxStructuralSkips bounds the candidates a structural rule rejects before
	// any Agent is called; they cost nothing but prove nothing either.
	maxStructuralSkips = 10
	// stageTimeout is how long one job may spend in a stage before it is not
	// merely slow.
	stageTimeout  = 30 * time.Minute
	letterTimeout = time.Hour
	fetchTimeout  = 35 * time.Minute
	// statusCallWindow is how many recent calls GET /api/v1/status lists.
	statusCallWindow = 20
)

// pollInterval is how often a waiting step reads the API again; a variable so
// tests need not wait in real time.
var pollInterval = 3 * time.Second

// Settings are the parts of the installed config.yaml the verification reads.
type Settings struct {
	ConfigPath      string
	APIAddr         string
	Token           string
	DBPath          string
	ProfilePath     string
	DenylistPath    string
	YouratorBaseURL string
	// Endpoints lists every role endpoint as role/slot with its agent and model.
	Endpoints    []Endpoint
	ScanInterval time.Duration
	MinInterval  time.Duration
}

// Endpoint is one role's primary or fallback route.
type Endpoint struct {
	Name  string
	Agent string
	Model string
}

// Options configure one run.
type Options struct {
	Layout paths.Layout
	Config Settings
	// RecheckLetter judges only the letter a previous run left pending.
	RecheckLetter bool
	GOOS          string
	Out           io.Writer
}

// Result is the verdict and where its report is.
type Result struct {
	Verdict  string
	ExitCode int
	Report   string
}

const (
	verdictPass    = "PASS"
	verdictFail    = "FAIL"
	verdictBlocked = "ENVIRONMENT_BLOCKED"
)

// stopError ends a run with a verdict: a product failure or a blocked
// environment. Any other error is an unexpected failure of the run itself.
type stopError struct {
	blocked bool
	message string
}

func (e *stopError) Error() string { return e.message }

func failf(format string, args ...any) error {
	return &stopError{message: fmt.Sprintf(format, args...)}
}

func blockedf(format string, args ...any) error {
	return &stopError{blocked: true, message: fmt.Sprintf(format, args...)}
}

type verifier struct {
	opts     Options
	layout   paths.Layout
	cfg      Settings
	api      *apiClient
	platform platform
	dir      string
	report   string
	out      io.Writer
	step     string
	blocked  []string
	record   restoreRecord
	// pendingUnrestored carries the items a restore inside the flow could not
	// put back, so the verdict reports them even once the flow has moved on.
	pendingUnrestored []string

	// snapshotOf reads the installed database; tests substitute a fixed one.
	snapshotOf func() (store.VerificationSnapshot, error)

	baseline   int64
	letterJobs map[int64]bool
	screened   map[int64]bool
	scoredJob  int64
}

// Run performs one verification and always attempts to restore what it
// arranged, whatever ended the run.
func Run(ctx context.Context, opts Options) Result {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	v := &verifier{
		opts: opts, layout: opts.Layout, cfg: opts.Config, out: out,
		api:        newAPIClient(opts.Config.APIAddr, opts.Config.Token),
		platform:   newPlatform(opts.GOOS, opts.Layout),
		dir:        filepath.Join(opts.Layout.DataDir, "verify"),
		letterJobs: map[int64]bool{}, screened: map[int64]bool{},
	}
	v.snapshotOf = v.snapshot
	if err := v.openReport(); err != nil {
		_, _ = fmt.Fprintf(out, "environment blocked: %v\n", err)
		return Result{Verdict: verdictBlocked, ExitCode: 2}
	}

	err := v.verify(ctx)
	verdict, reason := verdictPass, ""
	var stop *stopError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		verdict, reason = verdictBlocked, "interrupted; the product was not judged"
		v.note("- 狀態：ENVIRONMENT_BLOCKED")
		v.note("- 中斷：" + reason)
	case errors.As(err, &stop) && stop.blocked:
		verdict, reason = verdictBlocked, stop.message
		v.note("- 狀態：ENVIRONMENT_BLOCKED")
		v.note("- 環境阻塞：" + reason)
	default:
		verdict, reason = verdictFail, err.Error()
		v.note("- 狀態：FAIL")
		v.note("- 原因：" + reason)
	}

	var unrestored []string
	if _, statErr := os.Stat(v.recordPath()); statErr == nil {
		v.section("復原")
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		unrestored = v.restore(restoreCtx)
		cancel()
	} else {
		unrestored = v.pendingUnrestored
	}

	if verdict == verdictPass && len(v.blocked) > 0 {
		verdict = verdictBlocked
	}
	code := map[string]int{verdictPass: 0, verdictFail: 1, verdictBlocked: 2}[verdict]
	v.section("結果")
	if len(unrestored) > 0 {
		code = 3
		v.note("- 結果：復原未完成（壓過驗證結果）")
		v.note("- 驗證結果：" + verdict)
		for _, item := range unrestored {
			v.note("- 未復原：" + item)
		}
		v.note("- 復原紀錄保留在 " + v.recordPath() + "，下一趟開跑時會再試一次")
	} else {
		v.note("- 結果：" + verdict)
	}
	if reason != "" {
		v.note("- 原因：" + reason)
	}
	for _, item := range v.blocked {
		v.note("- 無結果：" + item)
	}
	_, _ = fmt.Fprintf(out, "live verification %s (exit %d): %s\n", verdict, code, v.report)
	return Result{Verdict: verdict, ExitCode: code, Report: v.report}
}

// ---------------------------------------------------------------------------
// Report

func (v *verifier) openReport() error {
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", v.dir, err)
	}
	suffix := "-live.md"
	mode := ""
	if v.opts.RecheckLetter {
		suffix, mode = "-live-recheck.md", "，求職信補測模式"
	}
	now := time.Now().In(taipei)
	v.report = filepath.Join(v.dir, now.Format("20060102-150405-0700")+suffix)
	header := strings.Join([]string{
		fmt.Sprintf("# jobfinder live 驗收報告（%s%s）", v.platform.name(), mode),
		"",
		"- 產生時間：" + now.Format(time.RFC3339),
		"- 對象：測試環境的實際安裝（真 Yourator、真 claude/codex CLI、已安裝的設定、SQLite 與排程）",
		"- 成本上限：Filter Agent 至多兩筆職缺、Scorer 一筆、求職信產製一次",
		"- 資料保護：只記錄筆數、hash、狀態與布林結果，不記錄 JD、Profile、信件、token 或 Agent 原始輸出",
	}, "\n") + "\n"
	return os.WriteFile(v.report, []byte(header), 0o600)
}

var taipei = func() *time.Location {
	location, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.FixedZone("Asia/Taipei", 8*60*60)
	}
	return location
}()

// note appends one line to the report and echoes it, so a run stopped by any
// means keeps everything it observed so far.
func (v *verifier) note(line string) {
	file, err := os.OpenFile(v.report, os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- report path under the resolved data directory.
	if err == nil {
		_, _ = fmt.Fprintln(file, line)
		_ = file.Close()
	}
	_, _ = fmt.Fprintln(v.out, line)
}

func (v *verifier) section(title string) {
	v.note("")
	v.note("## " + title)
}

func (v *verifier) begin(number, title string) {
	v.step = number
	v.section(fmt.Sprintf("[%s] %s", number, title))
}

func (v *verifier) pass() {
	for _, item := range v.blocked {
		if strings.HasPrefix(item, "["+v.step+"]") {
			return
		}
	}
	v.note("- 狀態：PASS")
}

// softBlock marks one verification undecided and lets the run go on.
func (v *verifier) softBlock(message string) {
	v.blocked = append(v.blocked, "["+v.step+"] "+message)
	v.note("- 無結果（ENVIRONMENT_BLOCKED）：" + message)
}

// ---------------------------------------------------------------------------
// Flow

func (v *verifier) verify(ctx context.Context) error {
	if err := v.preflight(ctx); err != nil {
		return err
	}
	if v.opts.RecheckLetter {
		v.begin("07", "單筆求職信（補測）")
		if err := v.judgePendingLetter(ctx); err != nil {
			return err
		}
		v.pass()
		return nil
	}
	steps := []struct {
		number, title string
		run           func(context.Context) error
	}{
		{"01", "安裝身分與設定契約", v.identity},
		{"02", "Profile、權限與 schema", v.profileAndSchema},
		{"03", "情境安排", v.arrange},
		{"04", "真來源抓取", v.fetch},
		{"05", "抓取冪等", v.idempotentFetch},
		{"06", "單筆篩選與評分", v.screenAndScore},
		{"07", "單筆求職信", v.letter},
		{"08", "已安裝排程定義", v.definitions},
		{"09", "執行中的 loopback API", v.runningAPI},
		{"10", "復原", v.restoreStep},
		{"11", "evidence 安全性", v.evidence},
	}
	for _, step := range steps {
		v.begin(step.number, step.title)
		if err := step.run(ctx); err != nil {
			return err
		}
		if step.number != "10" || len(v.pendingUnrestored) == 0 {
			v.pass()
		}
	}
	return nil
}

func (v *verifier) preflight(ctx context.Context) error {
	v.begin("00", "環境預檢")
	if _, err := os.Stat(v.layout.Binary); err != nil {
		return blockedf("no installed binary at %s", v.layout.Binary)
	}
	if err := v.platform.preflight(ctx); err != nil {
		return blockedf("%v", err)
	}
	if _, err := os.Stat(v.recordPath()); err == nil {
		v.note("- 上一趟留下復原紀錄，開跑前先復原")
		if unrestored := v.restore(ctx); len(unrestored) > 0 {
			v.pendingUnrestored = unrestored
			return failf("the settings an earlier run arranged could not be restored; fix them by hand before verifying again")
		}
	}
	if v.opts.RecheckLetter {
		if _, err := os.Stat(v.pendingPath()); err != nil {
			return blockedf("no letter-pending.json: there is no pending letter to recheck")
		}
		if !v.api.responding(ctx) {
			return blockedf("the API service at %s is not responding; the recheck mode arranges nothing", v.cfg.APIAddr)
		}
		v.pass()
		return nil
	}
	for _, agent := range []string{"claude", "codex"} {
		resolved, err := exec.LookPath(agent)
		if err != nil {
			return blockedf("missing required command: %s", agent)
		}
		slashed := filepath.ToSlash(resolved)
		if strings.Contains(slashed, "/scripts/verify/") || strings.Contains(slashed, "/dev-verify/") {
			return blockedf("%s resolves to the verification fake", agent)
		}
		versionCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err = agents.ResolveCommand(versionCtx, agent, []string{"--version"}).Run()
		cancel()
		if err != nil {
			return blockedf("%s CLI is not executable: %v", agent, err)
		}
	}
	v.note(fmt.Sprintf("- 執行前提齊備：jobfinder、%s、claude、codex", v.platform.name()))
	v.pass()
	return nil
}

var devVersion = regexp.MustCompile(`^jobfinder dev \([0-9a-f]+\)`)

func (v *verifier) identity(ctx context.Context) error {
	output, err := run(ctx, v.layout.Binary, "version")
	if err != nil {
		return failf("the installed binary could not report its version: %v", err)
	}
	if !devVersion.MatchString(output) {
		return failf("the installed binary is not a dev package build: %s", output)
	}
	host, _, err := net.SplitHostPort(v.cfg.APIAddr)
	if err != nil || (host != "127.0.0.1" && host != "::1") {
		return failf("api.addr is not a loopback address: %s", v.cfg.APIAddr)
	}
	if v.cfg.Token == "" {
		return failf("the installed config carries no api.token")
	}
	if !samePath(v.opts.GOOS, v.cfg.DBPath, v.layout.DB) {
		return failf("db.path does not point at the database jobfinder paths reports")
	}
	// The rendered config leaves base_url out and the crawler falls back to the
	// official host, so only an explicit value pointing anywhere else disqualifies.
	if base := strings.TrimSuffix(v.cfg.YouratorBaseURL, "/"); base != "" && base != "https://www.yourator.co" {
		return failf("the installed config does not target official Yourator: %s", base)
	}
	// Every role carries a primary and a fallback that name the agent and the
	// model outright: a live run must never reach an external CLI through an
	// implicit default.
	for _, endpoint := range v.cfg.Endpoints {
		if endpoint.Agent != "claude" && endpoint.Agent != "codex" {
			return failf("llm.roles.%s does not name claude or codex as its agent", endpoint.Name)
		}
		if strings.TrimSpace(endpoint.Model) == "" {
			return failf("llm.roles.%s does not name a model", endpoint.Name)
		}
	}
	v.note("- 安裝身分：" + output + "；設定、Profile、denylist 與 SQLite 皆取自 jobfinder paths")
	v.note(fmt.Sprintf("- api.addr 為 loopback 且有 token；Yourator 端點為官方主機；%d 個 role endpoint 皆明確指定 agent 與 model", len(v.cfg.Endpoints)))
	return nil
}

func samePath(goos, a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (v *verifier) profileAndSchema(context.Context) error {
	if v.opts.GOOS != "windows" {
		for name, path := range map[string]string{"config": v.cfg.ConfigPath, "profile": v.cfg.ProfilePath, "denylist": v.cfg.DenylistPath} {
			info, err := os.Stat(path)
			if err != nil {
				return failf("installed %s is unreadable: %v", name, err)
			}
			if info.Mode().Perm() != 0o600 {
				return failf("installed %s mode is %o, want 600", name, info.Mode().Perm())
			}
		}
	}
	_, contents, err := profile.Load(v.cfg.ProfilePath)
	if err != nil {
		return failf("profile does not load: %v", err)
	}
	denylist, err := profile.LoadDenylist(v.cfg.DenylistPath)
	if err != nil {
		return failf("denylist does not load: %v", err)
	}
	if err = profile.LintText(contents, denylist); err != nil {
		return failf("profile lint did not pass")
	}
	snapshot, err := v.snapshotOf()
	if err != nil {
		return err
	}
	if err := judgeSchema(snapshot); err != nil {
		return failf("the installed SQLite violates the schema contract: %v", err)
	}
	if v.opts.GOOS == "windows" {
		v.note("- profile lint 通過；schema 通過判準；設定檔的保護靠 %LocalAppData% 繼承的 ACL，不驗權限位元")
	} else {
		v.note("- 設定、Profile、denylist 為 0600；profile lint 通過；schema 通過判準")
	}
	return nil
}

func (v *verifier) snapshot() (store.VerificationSnapshot, error) {
	data, err := store.Open(v.layout.DB)
	if err != nil {
		return store.VerificationSnapshot{}, failf("open the installed SQLite: %v", err)
	}
	defer func() { _ = data.Close() }()
	snapshot, err := data.SnapshotForVerification(context.Background())
	if err != nil {
		return store.VerificationSnapshot{}, failf("snapshot the installed SQLite: %v", err)
	}
	return snapshot, nil
}
