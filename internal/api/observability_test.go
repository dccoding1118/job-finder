package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/store"
)

// A finished run and a run whose process was killed both stop writing. Only the
// heartbeat separates them, so the state the view reports is what a reader
// actually acts on.
func TestRunStateSeparatesRunningFromStalled(t *testing.T) {
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	started := now.Add(-30 * time.Minute)
	recent := now.Add(-time.Minute)
	old := now.Add(-2 * time.Hour)
	failure := "source refused the request"

	for _, testCase := range []struct {
		name string
		run  store.Run
		want string
	}{
		{"a run reporting progress is running", store.Run{StartedAt: started, HeartbeatAt: &recent}, runStateRunning},
		{"a run silent past the window has stalled", store.Run{StartedAt: started, HeartbeatAt: &old}, runStateStalled},
		{"a run that predates heartbeats is never called running", store.Run{StartedAt: started}, runStateStalled},
		{"a finished run is done", store.Run{StartedAt: started, FinishedAt: &recent, HeartbeatAt: &recent}, runStateDone},
		{"a finished run carrying an error failed", store.Run{StartedAt: started, FinishedAt: &recent, HeartbeatAt: &recent, Error: &failure}, runStateFailed},
	} {
		if got := runState(testCase.run, now); got != testCase.want {
			t.Errorf("%s: got %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// The elapsed time is the whole point of reporting what is in flight: an
// audited call already says how long it took, but only once it is over.
func TestStatusReportsWorkInFlightWithItsElapsedTime(t *testing.T) {
	processor := &fakeProcessor{inFlight: []pipeline.Unit{{Stage: "score", JobID: 42, StartedAt: time.Now().Add(-90 * time.Second)}}}
	server, _ := newTestServer(t, processor)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status returned %d", response.Code)
	}
	var body struct {
		InFlight []struct {
			Stage     string `json:"stage"`
			JobID     int64  `json:"job_id"`
			ElapsedMS int64  `json:"elapsed_ms"`
		} `json:"in_flight"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.InFlight) != 1 {
		t.Fatalf("expected the one unit in flight, got %+v", body.InFlight)
	}
	unit := body.InFlight[0]
	if unit.Stage != "score" || unit.JobID != 42 || unit.ElapsedMS < 90000 {
		t.Fatalf("expected the running score call and how long it has run, got %+v", unit)
	}
}

// An idle process reports an empty list rather than omitting the field, so the
// view can tell "nothing is running" from "this build cannot say".
func TestStatusReportsAnEmptyListWhenNothingIsInFlight(t *testing.T) {
	server, _ := newTestServer(t, &fakeProcessor{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	units, ok := body["in_flight"].([]any)
	if !ok || len(units) != 0 {
		t.Fatalf("expected an empty in-flight list, got %v", body["in_flight"])
	}
}
