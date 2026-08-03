package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/store"
)

// passTestFilter records the screening pass that puts a job into the score
// stage under the test revision.
func passTestFilter(data *store.Store, ctx context.Context, jobID int64) error {
	result := store.FilterResult{
		Outcome:    store.FilterPass,
		Conditions: []store.FilterCondition{{Text: "locations", Kind: "required", Group: 1, Category: "other", Verdict: store.FilterPass}},
		Stage:      "structural",
	}
	return data.SaveFilterResult(ctx, jobID, result, store.Revisions{Filter: testRevision, Score: testRevision})
}

func seedScoredJob(t *testing.T, data *store.Store, externalID, to string) int64 {
	t.Helper()
	ctx := context.Background()
	description := "Synthetic job description"
	created, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: externalID, URL: "https://example.test/jobs/" + externalID, Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid", FilterRevision: testRevision}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := passTestFilter(data, ctx, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err := data.CommitScore(ctx, store.ScoreInput{JobID: created.Job.ID, Content: 4, Benefit: 4, Bonus: 4, Industry: 4, Total: 70, Reason: "first pass", Runner: "claude", ScoreRevision: testRevision}, to); err != nil {
		t.Fatal(err)
	}
	return created.Job.ID
}

func TestReprocessReturnsOneJobToScreening(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "reprocess-job", "scored")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/reprocess", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("reprocess status = %d", response.Code)
	}
	body := decode(t, response)
	if body["status"] != "new" || len(processor.reprocessed) != 1 || processor.reprocessed[0] != jobID {
		t.Fatalf("reprocess body = %v forwarded = %v", body, processor.reprocessed)
	}
	job := body["job"].(map[string]any)
	if job["process_state"] != "new" || job["verdict"] != "pending_screen" {
		t.Fatalf("job after reprocess = %v", job)
	}
}

// A screening rejection is exactly what a manual reprocess exists to undo, so
// the entry accepts it where the previous rescore-only entry refused it.
func TestReprocessAcceptsScreenedOutJob(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	ctx := context.Background()
	description := "Synthetic job description"
	created, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "unfit-job", URL: "https://example.test/jobs/unfit-job", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid", FilterRevision: testRevision}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := store.FilterResult{
		Outcome:    store.FilterFail,
		Conditions: []store.FilterCondition{{Text: "locations", Kind: "required", Group: 1, Category: "other", Verdict: store.FilterFail}},
		Stage:      "structural",
	}
	if err := data.SaveFilterResult(ctx, created.Job.ID, result, store.Revisions{Filter: testRevision, Score: testRevision}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/reprocess", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("reprocess status = %d", response.Code)
	}
	job := decode(t, response)["job"].(map[string]any)
	if job["verdict"] != "pending_screen" || job["filter_hits"] != nil || job["filter_result"] != nil {
		t.Fatalf("screened-out job after reprocess = %v", job)
	}
}

func TestReprocessRejectsLetterHistoryAndUnknownJob(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "letter-job", "shortlisted")
	if err := data.TransitionProcess(context.Background(), jobID, "letter_requested"); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/reprocess", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("letter history reprocess status = %d, want 409", response.Code)
	}
	if code := decode(t, response)["error"].(map[string]any)["code"]; code != "reprocess_not_allowed" {
		t.Fatalf("error code = %v", code)
	}

	missing := httptest.NewRecorder()
	server.Handler().ServeHTTP(missing, authedRequest(http.MethodPost, "/api/v1/jobs/999/reprocess", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown job reprocess status = %d, want 404", missing.Code)
	}
}

func TestStatusReportsBacklogBudgetAndAgentCalls(t *testing.T) {
	processor := &fakeProcessor{scoreRemain: 12, scoreLimited: true}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "status-job", "scored")
	for _, call := range []store.AgentCallInput{
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: `{"content_fit":10,"benefit_fit":10,"bonus_fit":20,"industry_fit":30,"reason":"技能與方向皆不符"}`, OK: true, DurationMS: 8000, ScoreRevision: testRevision},
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: `{"type":"result","is_error":true,"api_error_status":429,"result":"weekly limit"}`, OK: false, DurationMS: 90000, ScoreRevision: testRevision},
	} {
		if err := data.SaveAgentCall(context.Background(), call); err != nil {
			t.Fatal(err)
		}
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d", response.Code)
	}
	body := decode(t, response)
	jobs := body["jobs"].(map[string]any)
	if jobs["scored"] != float64(1) {
		t.Fatalf("backlog = %v", jobs)
	}
	budget := body["score_budget"].(map[string]any)
	if budget["remaining"] != float64(12) || budget["limited"] != true {
		t.Fatalf("score budget = %v", budget)
	}
	calls := body["agent_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("agent calls = %v", calls)
	}
	failed := calls[0].(map[string]any)
	if failed["ok"] != false || failed["failure_kind"] != "runner_error" || failed["duration_ms"] != float64(90000) {
		t.Fatalf("failed agent call = %v", failed)
	}
	// A low score is a successful call: nothing about the score value may make
	// the progress view read it as a failure.
	succeeded := calls[1].(map[string]any)
	if succeeded["ok"] != true {
		t.Fatalf("a low score must stay a successful call: %v", succeeded)
	}
	if _, quoted := succeeded["detail"]; quoted {
		t.Fatalf("successful call must not quote Agent output: %v", succeeded)
	}
	if _, classified := succeeded["failure_kind"]; classified {
		t.Fatalf("successful call must carry no failure kind: %v", succeeded)
	}

	rejected := httptest.NewRecorder()
	server.Handler().ServeHTTP(rejected, authedRequest(http.MethodPost, "/api/v1/status", nil))
	if rejected.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status POST = %d, want 405", rejected.Code)
	}
}

// The push is accepted and the work runs in the background, so the response
// says only that the job is being processed.
func TestProcessNowAcceptsAWaitingJobAndRunsItInBackground(t *testing.T) {
	processor := &fakeProcessor{processedNow: make(chan int64, 1)}
	server, data := newTestServer(t, processor)
	processor.store = data
	ctx := context.Background()
	jobID := seedScoredJob(t, data, "push-job", "scored")
	if err := data.ReprocessJob(ctx, jobID, store.Revisions{Filter: testRevision, Score: testRevision}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/process", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("process status = %d", response.Code)
	}
	if body := decode(t, response); body["status"] != "processing" {
		t.Fatalf("process body = %v", body)
	}
	select {
	case pushed := <-processor.processedNow:
		if pushed != jobID {
			t.Fatalf("pushed job = %d, want %d", pushed, jobID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the accepted push never reached the pipeline")
	}
}

// An assessed job has nothing to push: its route back is a reprocess.
func TestProcessNowRefusesAJobThatIsNotWaiting(t *testing.T) {
	processor := &fakeProcessor{processedNow: make(chan int64, 1)}
	server, data := newTestServer(t, processor)
	processor.store = data
	seedScoredJob(t, data, "assessed-job", "scored")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/process", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("process status = %d, want 409", response.Code)
	}
	if len(processor.processedNow) != 0 {
		t.Fatal("a refused push must not reach the pipeline")
	}
}

// Without a resident worker the stages belong to a hand-driven batch this
// process cannot see, so it must not run one job beside it.
func TestProcessNowIsRefusedWithoutAResidentWorker(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	processor := &fakeProcessor{store: data, processedNow: make(chan int64, 1)}
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, processor)
	if err != nil {
		t.Fatal(err)
	}
	seedScoredJob(t, data, "paused-job", "scored")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/process", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("process status = %d, want 409", response.Code)
	}
}

// The switch is the user's, so it is readable and writable from the Side Panel
// and reported alongside the progress it governs.
func TestSettingsReportAndStoreTheAutoProcessingSwitch(t *testing.T) {
	server, data := newTestServer(t, &fakeProcessor{})

	read := httptest.NewRecorder()
	server.Handler().ServeHTTP(read, authedRequest(http.MethodGet, "/api/v1/settings", nil))
	if body := decode(t, read); body["auto_processing"] != true || body["resident_worker"] != true {
		t.Fatalf("default settings = %v", body)
	}

	write := httptest.NewRecorder()
	server.Handler().ServeHTTP(write, authedRequest(http.MethodPut, "/api/v1/settings", map[string]any{"auto_processing": false}))
	if write.Code != http.StatusOK {
		t.Fatalf("settings write status = %d", write.Code)
	}
	if body := decode(t, write); body["auto_processing"] != false {
		t.Fatalf("settings after write = %v", body)
	}
	if enabled, err := data.AutoProcessing(context.Background()); err != nil || enabled {
		t.Fatalf("stored switch = %v, %v; want off", enabled, err)
	}

	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, authedRequest(http.MethodGet, "/api/v1/status", nil))
	settings, ok := decode(t, status)["settings"].(map[string]any)
	if !ok || settings["auto_processing"] != false {
		t.Fatalf("status settings = %v", settings)
	}
}
