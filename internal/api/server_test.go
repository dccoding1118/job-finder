package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dccoding1118/job-finder/internal/store"
)

func TestJobsRequireExtensionTokenAndRejectWrongOrigin(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	description := "Synthetic job description"
	if _, upsertErr := data.UpsertJob(context.Background(), store.JobInput{Source: "yourator", ExternalID: "api-test", URL: "https://example.test/jobs/api-test", Title: "Synthetic Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil); upsertErr != nil {
		t.Fatal(upsertErr)
	}
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("MV3 request = %d, cors=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	request.Header.Set("Origin", "chrome-extension://wrong-id")
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("wrong origin request = %d, cors=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	request.Header.Set("Origin", "chrome-extension://test-id")
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "chrome-extension://test-id" {
		t.Fatalf("valid request = %d, cors=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestPreflightRequiresExactOriginWithoutToken(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil)
	request.Header.Set("Origin", "chrome-extension://test-id")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "chrome-extension://test-id" {
		t.Fatalf("preflight = %d, cors=%q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing preflight origin = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestJobsListIncludesLatestScoreAndSourceFilter(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	description := "Synthetic job description"
	created, err := data.UpsertJob(context.Background(), store.JobInput{Source: "yourator", ExternalID: "scored-job", URL: "https://example.test/jobs/scored-job", Title: "Synthetic Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scoreErr := data.SaveScore(context.Background(), store.ScoreInput{JobID: created.Job.ID, Content: 80, Benefit: 80, Bonus: 80, Industry: 80, Total: 80, Reason: "Synthetic", Runner: "claude"}); scoreErr != nil {
		t.Fatal(scoreErr)
	}
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?source=yourator", nil)
	request.Header.Set("Origin", "chrome-extension://test-id")
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d", response.Code)
	}
	var body struct {
		Items []struct {
			Source     string   `json:"source"`
			ScoreTotal *float64 `json:"score_total"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Source != "yourator" || body.Items[0].ScoreTotal == nil || *body.Items[0].ScoreTotal != 80 {
		t.Fatalf("unexpected list response: %#v", body.Items)
	}
}

func TestRejectsNonLoopbackAddress(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	if _, err := New(Config{Addr: "0.0.0.0:8686", Token: "token", ExtensionOrigin: "chrome-extension://id"}, data, nil, nil); err == nil {
		t.Fatal("accepted a non-loopback address")
	}
}
