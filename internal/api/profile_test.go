package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

func TestProfileAPISetupCreateConflictAndValidation(t *testing.T) {
	directory := t.TempDir()
	data, err := store.Open(filepath.Join(directory, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	provider, err := profile.NewProvider(filepath.Join(directory, "profile.yaml"), []string{"private-name"})
	if err != nil {
		t.Fatal(err)
	}
	activationCalls := 0
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, nil, ProfileConfig{Provider: provider, Activate: func(context.Context, profile.Revisions, profile.Profile) (profile.Activation, error) {
		activationCalls++
		return profile.Activation{Requeued: 1}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodGet, "/api/v1/profile", nil))
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"missing"` || decode(t, response)["status"] != "missing" {
		t.Fatalf("missing GET code=%d headers=%v", response.Code, response.Header())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/runs", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("setup run code=%d body=%s", response.Code, response.Body.String())
	}

	value := validAPIProfile()
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPut, "/api/v1/profile", value))
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match code=%d", response.Code)
	}

	request := authedRequest(http.MethodPut, "/api/v1/profile", value)
	request.Header.Set("If-Match", `"missing"`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("create code=%d body=%s", response.Code, response.Body.String())
	}
	if activationCalls != 0 {
		t.Fatalf("Profile save triggered %d reprocessing calls", activationCalls)
	}
	info, err := os.Stat(filepath.Join(directory, "profile.yaml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode=%v err=%v", info.Mode().Perm(), err)
	}

	request = authedRequest(http.MethodPut, "/api/v1/profile", value)
	request.Header.Set("If-Match", `"missing"`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale save code=%d", response.Code)
	}

	value.HonestyBounds = []string{"private-name"}
	request = authedRequest(http.MethodPut, "/api/v1/profile", value)
	request.Header.Set("If-Match", provider.Current().ETag)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || response.Body.String() == "" {
		t.Fatalf("PII save code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProfileReprocessRequiresReadyProfileAndRunsOnlyOnExplicitPost(t *testing.T) {
	directory := t.TempDir()
	data, err := store.Open(filepath.Join(directory, "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = data.Close() }()
	provider, err := profile.NewProvider(filepath.Join(directory, "profile.yaml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, nil, ProfileConfig{Provider: provider, Activate: func(_ context.Context, revisions profile.Revisions, value profile.Profile) (profile.Activation, error) {
		calls++
		if revisions.Filter == "" || revisions.Score == "" || len(value.Experiences) == 0 {
			t.Fatal("reprocess did not receive the active Profile and both revisions")
		}
		return profile.Activation{Requeued: 2, Protected: 1}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/profile/reprocess", nil))
	if response.Code != http.StatusConflict || calls != 0 {
		t.Fatalf("missing Profile reprocess code=%d calls=%d", response.Code, calls)
	}

	request := authedRequest(http.MethodPut, "/api/v1/profile", validAPIProfile())
	request.Header.Set("If-Match", `"missing"`)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 0 {
		t.Fatalf("save code=%d calls=%d", response.Code, calls)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authedRequest(http.MethodPost, "/api/v1/profile/reprocess", nil))
	body := decode(t, response)
	if response.Code != http.StatusOK || calls != 1 || body["status"] != "queued" {
		t.Fatalf("reprocess code=%d calls=%d body=%v", response.Code, calls, body)
	}
}

func TestProfileCORSAllowsConditionalPut(t *testing.T) {
	server, _ := newTestServer(t, nil)
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/profile", nil)
	request.Header.Set("Origin", "chrome-extension://test-id")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, OPTIONS" || response.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type, If-Match" {
		t.Fatalf("preflight code=%d headers=%v", response.Code, response.Header())
	}
}

func validAPIProfile() profile.Profile {
	return profile.Profile{
		Search: profile.Search{
			Directions: []profile.Direction{{Key: "P1", Title: "cloud architecture", Keywords: []string{"cloud"}}},
		},
		Requirements: profile.Requirements{Locations: []string{"taipei"}, Remote: "preferred"},
		Intents:      profile.Intents{ContentLikes: []string{"operable services"}},
		Experiences: []profile.Experience{{
			Industry: "technology services", Years: 4, Skills: []string{"Go"},
			OrgType: "technology provider", Role: "backend engineer", Achievements: []string{"reliable delivery"},
		}},
		Qualifications: profile.Qualifications{
			Education: []profile.EducationEntry{{Level: "master", Field: "computer science", Status: "graduated"}},
			Skills:    []profile.SkillEntry{{Name: "Go", Level: "proficient"}},
		},
		HonestyBounds: []string{"configuration focused"},
	}
}
