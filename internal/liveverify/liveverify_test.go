package liveverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/paths"
	"github.com/dccoding1118/job-finder/internal/store"
)

func ptr[T any](value T) *T { return &value }

func youratorJob(id int64, externalID string) store.VerificationJob {
	return store.VerificationJob{
		ID: id, Source: "yourator", ExternalID: externalID, URL: "https://www.yourator.co/companies/x/jobs/" + externalID,
		Title: "Platform engineer", CompanyName: "Example", Location: "Taipei", RemoteType: "hybrid",
		DescriptionLength: 120, DescriptionSHA256: digest("jd " + externalID), ContentHash: ptr(digest("content " + externalID)),
		SalaryMin: ptr(90000), SalaryMax: ptr(120000), ProcessState: "new",
	}
}

func isFail(err error) bool {
	var stop *stopError
	return errors.As(err, &stop) && !stop.blocked
}

func isBlocked(err error) bool {
	var stop *stopError
	return errors.As(err, &stop) && stop.blocked
}

func TestJudgeSchemaAcceptsAPopulatedDatabase(t *testing.T) {
	snapshot := store.VerificationSnapshot{
		SchemaVersion: expectedSchemaVersion, JournalMode: "WAL", ForeignKeys: true, Tables: expectedTables,
		Jobs: []store.VerificationJob{youratorJob(1, "100")},
	}
	if err := judgeSchema(snapshot); err != nil {
		t.Fatalf("a long-lived database with jobs must pass the schema check: %v", err)
	}
	snapshot.SchemaVersion = expectedSchemaVersion - 1
	if err := judgeSchema(snapshot); err == nil {
		t.Fatal("an older schema version must fail")
	}
}

func TestJudgeSourceChecksOnlyYouratorJobs(t *testing.T) {
	other := store.VerificationJob{ID: 9, Source: "104", ExternalID: "x", URL: "https://www.104.com.tw/job/x"}
	snapshot := store.VerificationSnapshot{Jobs: []store.VerificationJob{youratorJob(1, "100"), youratorJob(2, "101"), other}}
	summary, err := judgeSource(snapshot)
	if err != nil {
		t.Fatalf("well-formed Yourator jobs must pass: %v", err)
	}
	if !strings.HasPrefix(summary, "yourator_jobs=2 ") {
		t.Fatalf("summary %q must count the two Yourator jobs only", summary)
	}

	cases := map[string]func(*store.VerificationJob){
		"foreign host":     func(job *store.VerificationJob) { job.URL = "https://example.com/jobs/1" },
		"empty company":    func(job *store.VerificationJob) { job.CompanyName = " " },
		"no description":   func(job *store.VerificationJob) { job.DescriptionLength = 0 },
		"one salary bound": func(job *store.VerificationJob) { job.SalaryMax = nil },
		"inverted salary":  func(job *store.VerificationJob) { job.SalaryMin = ptr(200000) },
		"remote enum":      func(job *store.VerificationJob) { job.RemoteType = "anywhere" },
		"bad content hash": func(job *store.VerificationJob) { job.ContentHash = ptr("nothex") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			job := youratorJob(1, "100")
			mutate(&job)
			if _, err := judgeSource(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}); err == nil {
				t.Fatal("the contract violation must fail")
			}
		})
	}
	if _, err := judgeSource(store.VerificationSnapshot{Jobs: []store.VerificationJob{youratorJob(1, "100"), youratorJob(2, "100")}}); err == nil {
		t.Fatal("a duplicated external ID must fail")
	}
	if _, err := judgeSource(store.VerificationSnapshot{Jobs: []store.VerificationJob{other}}); err == nil {
		t.Fatal("a database without Yourator jobs must fail")
	}
}

func TestFingerprintFollowsContentAndState(t *testing.T) {
	jobs := []store.VerificationJob{youratorJob(1, "100"), youratorJob(2, "101")}
	count, before := fingerprint(store.VerificationSnapshot{Jobs: jobs})
	reordered := []store.VerificationJob{jobs[1], jobs[0]}
	if _, same := fingerprint(store.VerificationSnapshot{Jobs: reordered}); same != before || count != 2 {
		t.Fatal("the fingerprint must not depend on row order")
	}
	jobs[0].ProcessState = "queued"
	if _, changed := fingerprint(store.VerificationSnapshot{Jobs: jobs}); changed == before {
		t.Fatal("a changed processing state must change the fingerprint")
	}
}

func TestJudgeScreenedRequiresAScoreOnceScored(t *testing.T) {
	job := youratorJob(7, "107")
	job.FilterOutcome = ptr("pass")
	job.FilterConditions = []store.VerificationFilterCondition{{Verdict: "pass"}, {Verdict: "unknown"}}
	job.ProcessState = "filtered_out"
	if _, err := judgeScreened(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}, 7); err != nil {
		t.Fatalf("a screened job without a score must pass: %v", err)
	}
	job.ProcessState = "shortlisted"
	if _, err := judgeScreened(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}, 7); err == nil {
		t.Fatal("a shortlisted job without a score must fail")
	}
	job.Score = &store.VerificationScore{Content: 80, Benefit: 70, Bonus: 60, Industry: 90, Total: 77.5, ReasonSHA256: digest("fits")}
	if _, err := judgeScreened(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}, 7); err != nil {
		t.Fatalf("a well-formed score must pass: %v", err)
	}
	job.Score.Bonus = 101
	if _, err := judgeScreened(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}, 7); err == nil {
		t.Fatal("a dimension over 100 must fail")
	}
	job.Score.Bonus = 60
	job.FilterConditions = append(job.FilterConditions, store.VerificationFilterCondition{Verdict: "maybe"})
	if _, err := judgeScreened(store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}, 7); err == nil {
		t.Fatal("a condition verdict outside the enum must fail")
	}
}

func TestJudgeLetteredChecksPlaceholdersUnlessFailed(t *testing.T) {
	job := youratorJob(5, "105")
	snapshot := store.VerificationSnapshot{Jobs: []store.VerificationJob{job}}
	if _, err := judgeLettered(snapshot, 5, "failed"); err != nil {
		t.Fatalf("a failed generation leaves no letter to check: %v", err)
	}
	if _, err := judgeLettered(snapshot, 5, "approved"); err == nil {
		t.Fatal("an approved generation without a letter must fail")
	}
	job.Letter = &store.VerificationLetter{Status: "finalized", Rounds: 3, HasNamePlaceholder: true, HasContactPlaceholder: true}
	snapshot.Jobs[0] = job
	if _, err := judgeLettered(snapshot, 5, "finalized"); err != nil {
		t.Fatalf("a finalized letter with both placeholders must pass: %v", err)
	}
	if _, err := judgeLettered(snapshot, 5, "approved"); err == nil {
		t.Fatal("a letter whose status differs from its generation must fail")
	}
	job.Letter.HasContactPlaceholder = false
	if _, err := judgeLettered(snapshot, 5, "finalized"); err == nil {
		t.Fatal("a letter without the contact placeholder must fail")
	}
	if _, err := judgeLettered(snapshot, 5, "running"); err == nil {
		t.Fatal("a generation still running is not a terminal status")
	}
}

func TestAttributeCallsRejectsCallsOutsideTheChosenJobs(t *testing.T) {
	calls := []apiCall{
		{ID: 40, JobID: ptr(int64(1)), Role: "filter", OK: true},
		{ID: 41, JobID: ptr(int64(7)), Role: "filter", OK: true},
		{ID: 42, JobID: ptr(int64(7)), Role: "scorer", OK: true},
		{ID: 43, JobID: ptr(int64(9)), Role: "drafter", OK: true},
	}
	allowed := map[string]map[int64]bool{
		"filter": {7: true}, "scorer": {7: true}, "drafter": {9: true}, "reviewer": {9: true},
	}
	summary, err := attributeCalls(calls, 40, allowed)
	if err != nil {
		t.Fatalf("calls on the chosen jobs after the baseline must pass: %v", err)
	}
	if summary != "filter_jobs=1 scorer_jobs=1 calls=3" {
		t.Fatalf("summary = %q", summary)
	}
	calls = append(calls, apiCall{ID: 44, JobID: ptr(int64(8)), Role: "scorer", OK: true})
	if _, err := attributeCalls(calls, 40, allowed); !isFail(err) {
		t.Fatalf("a scorer call on another job must fail, got %v", err)
	}
}

func TestAttributeCallsFailsWhenTheWindowHidesCalls(t *testing.T) {
	var calls []apiCall
	for id := int64(60); id < 60+statusCallWindow; id++ {
		calls = append(calls, apiCall{ID: id, JobID: ptr(int64(7)), Role: "filter"})
	}
	allowed := map[string]map[int64]bool{"filter": {7: true}}
	if _, err := attributeCalls(calls, 40, allowed); !isFail(err) {
		t.Fatalf("calls older than the status window must be treated as over budget, got %v", err)
	}
}

func TestCheckFetchRunSeparatesAnUnreachableSource(t *testing.T) {
	done := apiRun{Trigger: "timer", State: "done", Stats: map[string]int{"fetched": 3, "new": 1, "errors": 0}}
	if err := checkFetchRun(done); err != nil {
		t.Fatalf("a finished scheduled fetch must pass: %v", err)
	}
	manual := done
	manual.Trigger = "manual-cli"
	if err := checkFetchRun(manual); !isFail(err) {
		t.Fatal("a fetch not started by the schedule must fail")
	}
	unreachable := apiRun{Trigger: "timer", State: "failed", Stats: map[string]int{"errors": 1}, Error: ptr("dial tcp: i/o timeout")}
	if err := checkFetchRun(unreachable); !isBlocked(err) {
		t.Fatalf("a network failure must block rather than fail, got %v", err)
	}
	broken := apiRun{Trigger: "timer", State: "failed", Stats: map[string]int{"errors": 1}, Error: ptr("parse listing: unexpected shape")}
	if err := checkFetchRun(broken); !isFail(err) {
		t.Fatal("a parse failure must fail")
	}
	empty := done
	empty.Stats = map[string]int{"fetched": 0}
	if err := checkFetchRun(empty); !isFail(err) {
		t.Fatal("zero fetched jobs must fail")
	}
}

func TestRewritePausedTouchesOnlyTheWorkerKey(t *testing.T) {
	original := "api:\r\n  paused: true\r\nworker:\r\n  scan_interval: 5s\r\n  # comment\r\n  paused: true\r\nllm:\r\n  timeout: 5m\r\n"
	rewritten, found := rewritePaused([]byte(original))
	if !found {
		t.Fatal("worker.paused must be found")
	}
	want := strings.Replace(original, "worker:\r\n  scan_interval: 5s\r\n  # comment\r\n  paused: true", "worker:\r\n  scan_interval: 5s\r\n  # comment\r\n  paused: false", 1)
	if string(rewritten) != want {
		t.Fatalf("rewritten config:\n%q\nwant:\n%q", rewritten, want)
	}
	if _, found := rewritePaused([]byte("worker:\n  scan_interval: 5s\n")); found {
		t.Fatal("a config without worker.paused must report it")
	}
}

// fakePlatform records the service operations a restore performs.
type fakePlatform struct {
	operations []string
	running    bool
}

func (f *fakePlatform) name() string                             { return "fake" }
func (f *fakePlatform) preflight(context.Context) error          { return nil }
func (f *fakePlatform) apiRunning(context.Context) (bool, error) { return f.running, nil }
func (f *fakePlatform) startAPI(context.Context) error {
	f.operations = append(f.operations, "start")
	f.running = true
	return nil
}

func (f *fakePlatform) stopAPI(context.Context) error {
	f.operations = append(f.operations, "stop")
	f.running = false
	return nil
}

func (f *fakePlatform) restartAPI(context.Context) error {
	f.operations = append(f.operations, "restart")
	return nil
}
func (f *fakePlatform) fetchDisabled(context.Context) (bool, error) { return false, nil }
func (f *fakePlatform) enableFetch(context.Context) error {
	f.operations = append(f.operations, "enable-fetch")
	return nil
}

func (f *fakePlatform) disableFetch(context.Context) error {
	f.operations = append(f.operations, "disable-fetch")
	return nil
}
func (f *fakePlatform) triggerFetch(context.Context) error         { return nil }
func (f *fakePlatform) fetchRunning(context.Context) (bool, error) { return false, nil }
func (f *fakePlatform) checkDefinitions(context.Context, paths.Layout) (string, error) {
	return "", nil
}

func (f *fakePlatform) apiExecutable(context.Context, paths.Layout) (string, error) {
	return "", nil
}

func TestRestorePutsItemsBackInReverseOrder(t *testing.T) {
	var puts []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			AutoProcessing bool `json:"auto_processing"`
		}
		if r.Method == http.MethodPut {
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			puts = append(puts, body.AutoProcessing)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"auto_processing": body.AutoProcessing, "resident_worker": true})
	}))
	defer server.Close()

	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(config, []byte("worker:\n  paused: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := config + ".verify-live.bak"
	originalConfig := []byte("worker:\n  paused: true\n")
	if err := os.WriteFile(backup, originalConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _ := fileSHA256(backup)

	fake := &fakePlatform{running: true}
	v := &verifier{
		cfg: Settings{ConfigPath: config}, dir: dir, report: filepath.Join(dir, "report.md"), out: io.Discard,
		api: &apiClient{base: server.URL, token: "secret", http: server.Client()}, platform: fake,
		record: restoreRecord{APIRunning: ptr(false), ConfigBackup: backup, ConfigSHA256: sum, AutoProcessing: ptr(true), FetchDisabled: ptr(true)},
	}
	if err := os.WriteFile(v.report, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.saveRecord(); err != nil {
		t.Fatal(err)
	}

	if unrestored := v.restore(context.Background()); len(unrestored) != 0 {
		t.Fatalf("every item must be restored, left: %v", unrestored)
	}
	if got := strings.Join(fake.operations, ","); got != "disable-fetch,restart,stop" {
		t.Fatalf("operations = %s; the fetch task, the config restart and the API stop must run in reverse of arrangement", got)
	}
	if len(puts) != 1 || !puts[0] {
		t.Fatalf("the automatic processing switch must be put back to true once, got %v", puts)
	}
	if data, _ := os.ReadFile(config); string(data) != string(originalConfig) { // #nosec G304 -- test temp directory.
		t.Fatalf("config.yaml = %q, want the original bytes", data)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("the config backup must be removed once restored")
	}
	if _, err := os.Stat(v.recordPath()); !os.IsNotExist(err) {
		t.Fatal("the restore record must be removed once everything is back")
	}
}

func TestRestoreKeepsTheRecordWhenAnItemCannotBeRestored(t *testing.T) {
	dir := t.TempDir()
	fake := &fakePlatform{running: true}
	v := &verifier{
		cfg: Settings{ConfigPath: filepath.Join(dir, "config.yaml")}, dir: dir, report: filepath.Join(dir, "report.md"), out: io.Discard,
		api: newAPIClient("127.0.0.1:1", "secret"), platform: fake,
		record: restoreRecord{ConfigBackup: filepath.Join(dir, "missing.bak"), ConfigSHA256: digest("x")},
	}
	if err := v.saveRecord(); err != nil {
		t.Fatal(err)
	}
	unrestored := v.restore(context.Background())
	if len(unrestored) != 1 || !strings.Contains(unrestored[0], "config.yaml") {
		t.Fatalf("the missing backup must be reported, got %v", unrestored)
	}
	if _, err := os.Stat(v.recordPath()); err != nil {
		t.Fatal("the restore record must stay for the next run")
	}
}

// fakeAPI is a scripted test backend: each job moves to its final state on the
// process request and records the calls the pipeline would have audited.
type fakeAPI struct {
	lists     map[string][]int64
	refuse    map[int64]bool   // reprocess answers 409
	finals    map[int64]string // state a job ends in once processed
	roles     map[int64][]string
	states    map[int64]string
	calls     []apiCall
	processed []int64
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		var id int64
		switch {
		case r.URL.Path == "/api/v1/jobs":
			items := []map[string]any{}
			for _, job := range f.lists[r.URL.Query().Get("process_state")] {
				items = append(items, map[string]any{"id": job})
			}
			write(map[string]any{"items": items})
		case r.URL.Path == "/api/v1/status":
			write(map[string]any{"agent_calls": f.calls, "in_flight": []any{}})
		case sscan(r.URL.Path, "/api/v1/jobs/%d/reprocess", &id):
			if f.refuse[id] {
				w.WriteHeader(http.StatusConflict)
				write(map[string]any{"error": map[string]string{"code": "reprocess_not_allowed"}})
				return
			}
			f.states[id] = "new"
			write(map[string]any{"status": "new"})
		case sscan(r.URL.Path, "/api/v1/jobs/%d/process", &id):
			f.processed = append(f.processed, id)
			for _, role := range f.roles[id] {
				job := id
				f.calls = append(f.calls, apiCall{ID: int64(len(f.calls) + 100), JobID: &job, Role: role, OK: true})
			}
			f.states[id] = f.finals[id]
			w.WriteHeader(http.StatusAccepted)
			write(map[string]any{"status": "processing"})
		case sscan(r.URL.Path, "/api/v1/jobs/%d", &id):
			write(map[string]any{"id": id, "process_state": f.states[id]})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func sscan(path, format string, id *int64) bool {
	var rest string
	n, _ := fmt.Sscanf(path+"|", format+"%s", id, &rest)
	return n == 2 && rest == "|"
}

func TestScreenAndScoreSettlesOnTheFirstJobThatReachesTheAgent(t *testing.T) {
	previous := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = previous })

	api := &fakeAPI{
		lists:  map[string][]int64{"shortlisted": {3}, "scored": {4}, "new": {5, 6}},
		refuse: map[int64]bool{3: true},
		finals: map[int64]string{4: "filtered_out", 5: "scored", 6: "shortlisted"},
		roles:  map[int64][]string{5: {"filter", "scorer"}, 6: {"filter", "scorer"}},
		states: map[int64]string{3: "shortlisted", 4: "scored", 5: "new", 6: "new"},
	}
	server := httptest.NewServer(api.handler(t))
	defer server.Close()

	scored := youratorJob(5, "105")
	scored.ProcessState = "scored"
	scored.FilterOutcome = ptr("pass")
	scored.Score = &store.VerificationScore{Content: 60, Benefit: 60, Bonus: 50, Industry: 70, Total: 60, ReasonSHA256: digest("partly")}
	dir := t.TempDir()
	v := &verifier{
		dir: dir, report: filepath.Join(dir, "report.md"), out: io.Discard, step: "06",
		api:        &apiClient{base: server.URL + "/api/v1", token: "secret", http: server.Client()},
		letterJobs: map[int64]bool{}, screened: map[int64]bool{},
		snapshotOf: func() (store.VerificationSnapshot, error) {
			return store.VerificationSnapshot{Jobs: []store.VerificationJob{scored}}, nil
		},
	}
	if err := os.WriteFile(v.report, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := v.screenAndScore(context.Background()); err != nil {
		t.Fatalf("screenAndScore: %v", err)
	}
	if v.scoredJob != 5 {
		t.Fatalf("scored job = %d, want 5", v.scoredJob)
	}
	if fmt.Sprint(api.processed) != "[4 5]" {
		t.Fatalf("processed %v: the refused job must be skipped and processing must stop at the first scored job", api.processed)
	}
	if !v.screened[5] || v.screened[4] {
		t.Fatalf("screened = %v: only a job that reached the Filter Agent counts", v.screened)
	}
	if len(v.blocked) != 0 {
		t.Fatalf("nothing should be undecided, got %v", v.blocked)
	}
}
