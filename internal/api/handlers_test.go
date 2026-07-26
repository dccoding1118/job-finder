package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/store"
)

// testRevision is the Profile revision the fake processor scores under.
const testRevision = "sha256:test-revision"

// fakeProcessor records the calls the API forwards so the handlers can be
// tested without the real pipeline.
type fakeProcessor struct {
	store         *store.Store
	listResults   []pipeline.IngestResult
	jobResult     pipeline.IngestResult
	requested     []int64
	scoreRemain   int
	scoreLimited  bool
	requestLetter func(int64) error
	rescored      []int64
}

func (f *fakeProcessor) IngestList(context.Context, []crawler.RawJob) ([]pipeline.IngestResult, error) {
	return f.listResults, nil
}

func (f *fakeProcessor) IngestJob(context.Context, crawler.RawJob) (pipeline.IngestResult, error) {
	return f.jobResult, nil
}

func (f *fakeProcessor) RequestLetter(_ context.Context, id int64) error {
	f.requested = append(f.requested, id)
	if f.requestLetter != nil {
		return f.requestLetter(id)
	}
	return f.store.TransitionProcess(context.Background(), id, "letter_requested")
}

func (f *fakeProcessor) RequestRescore(ctx context.Context, id int64) error {
	f.rescored = append(f.rescored, id)
	return f.store.RequeueScore(ctx, id, testRevision)
}

func (f *fakeProcessor) ScoreBudgetRemaining(context.Context) (int, bool, error) {
	return f.scoreRemain, f.scoreLimited, nil
}

func authedRequest(method, target string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Origin", "chrome-extension://test-id")
	request.Header.Set("Authorization", "Bearer test-token")
	return request
}

func newTestServer(t *testing.T, processor Processor) (*Server, *store.Store) {
	t.Helper()
	data, err := store.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	server, err := New(Config{Addr: "127.0.0.1:0", Token: "test-token", ExtensionOrigin: "chrome-extension://test-id"}, data, nil, processor)
	if err != nil {
		t.Fatal(err)
	}
	return server, data
}

func decode(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func TestJobViewDerivesVerdictAndLetterState(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	ctx := context.Background()
	description := "Synthetic job description"
	created, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "verdict-job", URL: "https://example.test/jobs/verdict-job", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted"} {
		if err := data.TransitionProcess(ctx, created.Job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := data.SaveScore(ctx, store.ScoreInput{JobID: created.Job.ID, HardSkill: 80, Domain: 80, Seniority: 80, Condition: 80, Direction: 80, Total: 80, Reason: "fit", Runner: "claude"}); err != nil {
		t.Fatal(err)
	}
	request := authedRequest(http.MethodGet, "/api/v1/jobs/1", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	body := decode(t, response)
	if body["verdict"] != "recommended" || body["letter_state"] != "none" {
		t.Fatalf("verdict/letter_state = %v/%v, want recommended/none", body["verdict"], body["letter_state"])
	}
}

func TestRequestLetterForwardsToPipeline(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	ctx := context.Background()
	description := "Synthetic job description"
	created, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "letter-job", URL: "https://example.test/jobs/letter-job", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted"} {
		if err := data.TransitionProcess(ctx, created.Job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	request := authedRequest(http.MethodPost, "/api/v1/jobs/1/letter", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("letter request status = %d", response.Code)
	}
	body := decode(t, response)
	if body["status"] != "requested" || len(processor.requested) != 1 {
		t.Fatalf("letter request = %v, forwarded=%v", body["status"], processor.requested)
	}
	job := body["job"].(map[string]any)
	if job["letter_state"] != "requested" || job["verdict"] != "recommended" {
		t.Fatalf("job after request = %v", job)
	}
}

func TestCaptureJobReportsPendingAndBudget(t *testing.T) {
	processor := &fakeProcessor{jobResult: pipeline.IngestResult{JobID: 7, ProcessState: "queued"}, scoreLimited: true, scoreRemain: 0}
	server, _ := newTestServer(t, processor)
	posting := `{"@type":"JobPosting","title":"Engineer","hiringOrganization":{"name":"Example"},"industry":"software","description":"Go platform work","jobLocation":{"address":{"addressLocality":"Taipei"}}}`
	request := authedRequest(http.MethodPost, "/api/v1/capture/job", map[string]any{"source": "104", "url": "https://www.104.com.tw/job/abc", "json_ld": []string{posting}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capture status = %d", response.Code)
	}
	body := decode(t, response)
	if body["verdict"] != "pending_score" || body["budget_exhausted"] != true {
		t.Fatalf("capture body = %v", body)
	}
}

func TestCaptureListReturnsVerdictPerItem(t *testing.T) {
	processor := &fakeProcessor{listResults: []pipeline.IngestResult{
		{JobID: 1, ProcessState: "filtered_out", FilterHits: []string{"locations"}, Created: true},
		{JobID: 2, ProcessState: "discovered", Created: true},
	}}
	server, _ := newTestServer(t, processor)
	request := authedRequest(http.MethodPost, "/api/v1/capture/list", map[string]any{"source": "104", "items": []map[string]any{
		{"href": "https://www.104.com.tw/job/aaa", "title": "A", "company_name": "C", "location": "Taipei"},
		{"href": "https://www.104.com.tw/job/bbb", "title": "B", "company_name": "C", "location": "Taipei"},
	}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capture list status = %d", response.Code)
	}
	var body struct {
		Items []struct {
			Verdict    string   `json:"verdict"`
			FilterHits []string `json:"filter_hits"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].Verdict != "unfit" || body.Items[0].FilterHits[0] != "locations" || body.Items[1].Verdict != "pending_detail" {
		t.Fatalf("capture list items = %#v", body.Items)
	}
}

func TestJobsVerdictFilterMapsToStates(t *testing.T) {
	server, data := newTestServer(t, nil)
	ctx := context.Background()
	description := "Synthetic job description"
	recommended, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "rec", URL: "https://example.test/jobs/rec", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &description, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "shortlisted"} {
		if err = data.TransitionProcess(ctx, recommended.Job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	other := description
	pendingJob, err := data.UpsertJob(ctx, store.JobInput{Source: "yourator", ExternalID: "pend", URL: "https://example.test/jobs/pend", Title: "Engineer", CompanyName: "Example", CompanyInfo: "software", Description: &other, Location: "Taipei", RemoteType: "hybrid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = pendingJob
	request := authedRequest(http.MethodGet, "/api/v1/jobs?verdict=recommended", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body struct {
		Items []struct {
			Verdict string `json:"verdict"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Verdict != "recommended" {
		t.Fatalf("verdict filter items = %#v", body.Items)
	}
}

func TestJobsCursorPagination(t *testing.T) {
	server, data := newTestServer(t, nil)
	ctx := context.Background()
	description := "Synthetic job description"
	for index := 1; index <= 3; index++ {
		_, err := data.UpsertJob(ctx, store.JobInput{
			Source:      "yourator",
			ExternalID:  "page-" + strconv.Itoa(index),
			URL:         "https://example.test/jobs/page-" + strconv.Itoa(index),
			Title:       "Synthetic Engineer",
			CompanyName: "Example",
			CompanyInfo: "software",
			Description: &description,
			Location:    "Taipei",
			RemoteType:  "hybrid",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	type pageBody struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	readPage := func(path string) pageBody {
		t.Helper()
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, authedRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", path, response.Code, response.Body.String())
		}
		var body pageBody
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	first := readPage("/api/v1/jobs?limit=2")
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	second := readPage("/api/v1/jobs?limit=2&cursor=" + url.QueryEscape(*first.NextCursor))
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
	seen := map[int64]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatalf("job %d appeared on more than one page", item.ID)
		}
		seen[item.ID] = true
	}

	for _, path := range []string{"/api/v1/jobs?limit=0", "/api/v1/jobs?cursor=not-a-cursor"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, authedRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want %d", path, response.Code, http.StatusBadRequest)
		}
	}
}
