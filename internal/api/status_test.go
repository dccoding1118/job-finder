package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dccoding1118/job-finder/internal/store"
)

func seedScoredJob(t *testing.T, data *store.Store, externalID, to string) int64 {
	t.Helper()
	ctx := context.Background()
	description := "Synthetic job description"
	created, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: externalID, URL: "https://example.test/jobs/" + externalID, Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid", ProfileRevision: testRevision}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := data.CommitFilter(ctx, created.Job.ID, testRevision, nil); err != nil {
		t.Fatal(err)
	}
	if err := data.CommitScore(ctx, store.ScoreInput{JobID: created.Job.ID, HardSkill: 4, Domain: 4, Seniority: 4, Condition: 3, Direction: 4, Total: 70, Reason: "first pass", Runner: "claude", ProfileRevision: testRevision}, to); err != nil {
		t.Fatal(err)
	}
	return created.Job.ID
}

func TestRescoreRequeuesOneJob(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "rescore-job", "scored")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/rescore", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("rescore status = %d", response.Code)
	}
	body := decode(t, response)
	if body["status"] != "queued" || len(processor.rescored) != 1 || processor.rescored[0] != jobID {
		t.Fatalf("rescore body = %v forwarded = %v", body, processor.rescored)
	}
	job := body["job"].(map[string]any)
	if job["process_state"] != "queued" || job["verdict"] != "pending_score" {
		t.Fatalf("job after rescore = %v", job)
	}
}

func TestRescoreRejectsLetterHistoryAndUnknownJob(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "letter-job", "shortlisted")
	if err := data.TransitionProcess(context.Background(), jobID, "letter_requested"); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/jobs/1/rescore", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("letter history rescore status = %d, want 409", response.Code)
	}
	if code := decode(t, response)["error"].(map[string]any)["code"]; code != "rescore_not_allowed" {
		t.Fatalf("error code = %v", code)
	}

	missing := httptest.NewRecorder()
	server.Handler().ServeHTTP(missing, authedRequest(http.MethodPost, "/api/v1/jobs/999/rescore", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown job rescore status = %d, want 404", missing.Code)
	}
}

func TestStatusReportsBacklogBudgetAndAgentCalls(t *testing.T) {
	processor := &fakeProcessor{scoreRemain: 12, scoreLimited: true}
	server, data := newTestServer(t, processor)
	processor.store = data
	jobID := seedScoredJob(t, data, "status-job", "scored")
	for _, call := range []store.AgentCallInput{
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: `{"hard_skill":10,"domain":10,"seniority":20,"condition":30,"direction":5,"reason":"技能與方向皆不符"}`, OK: true, DurationMS: 8000, ProfileRevision: testRevision},
		{JobID: &jobID, Role: "scorer", Runner: "claude", Input: "prompt", Output: `{"type":"result","is_error":true,"api_error_status":429,"result":"weekly limit"}`, OK: false, DurationMS: 90000, ProfileRevision: testRevision},
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
