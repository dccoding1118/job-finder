package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/dccoding1118/job-finder/internal/pipeline"
	"github.com/dccoding1118/job-finder/internal/store"
)

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

func decodeInto(t *testing.T, response *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// dupeJob stores one synthetic listing of a source.
func dupeJob(t *testing.T, data *store.Store, source, externalID, title, company, location string, description *string) int64 {
	t.Helper()
	result, err := data.UpsertJob(context.Background(), store.JobInput{
		Source: source, ExternalID: externalID, URL: "https://example.test/" + source + "/" + externalID,
		Title: title, CompanyName: company, CompanyInfo: "public listing",
		Description: description, Location: location, RemoteType: "onsite",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.Job.ID
}

// linkJob groups one job the way ingest does: right after it was stored, before
// the next source's copy arrives.
func linkJob(t *testing.T, data *store.Store, jobID int64) store.DedupeOutcome {
	t.Helper()
	outcome, err := data.LinkOrSuggestDuplicate(context.Background(), jobID, store.DedupeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func serve(t *testing.T, server *Server, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

// AT-80 / AT-81: lists carry only canonical jobs, and one job carries its group.
func TestJobListsExcludeAliasesAndJobCarriesGroup(t *testing.T) {
	server, data := newTestServer(t, &fakeProcessor{})
	description := "合成職缺說明"
	canonical := dupeJob(t, data, "104", "a1", "Backend Engineer", "Example", "台北市", &description)
	linkJob(t, data, canonical)
	alias := dupeJob(t, data, "cake", "c1", "Backend Engineer", "Example", "台北市", nil)
	if outcome := linkJob(t, data, alias); !outcome.Merged || outcome.CanonicalJobID != canonical {
		t.Fatalf("the two copies were not merged into %d: %+v", canonical, outcome)
	}

	var list struct {
		Items []struct {
			ID      int64  `json:"id"`
			Verdict string `json:"verdict"`
		} `json:"items"`
	}
	decodeInto(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/jobs", nil)), &list)
	if len(list.Items) != 1 || list.Items[0].ID != canonical {
		t.Fatalf("job list = %+v, want only the canonical job", list.Items)
	}
	decodeInto(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/queue", nil)), &list)
	for _, item := range list.Items {
		if item.ID == alias {
			t.Fatal("the queue must not carry an alias")
		}
	}

	body := decode(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/jobs/"+itoa(canonical), nil)))
	group, ok := body["group"].(map[string]any)
	if !ok {
		t.Fatalf("job carries no group: %v", body["group"])
	}
	if int64(group["canonical_job_id"].(float64)) != canonical || len(group["members"].([]any)) != 2 {
		t.Fatalf("group = %v", group)
	}
	aliasBody := decode(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/jobs/"+itoa(alias), nil)))
	if aliasBody["verdict"] != "" {
		t.Fatalf("an alias must export no verdict, got %v", aliasBody["verdict"])
	}
}

// AT-82 / AT-83 / AT-84 / AT-85 / AT-86: the user's duplicate decisions are
// synchronous, idempotent about a decided pair, and reversible.
func TestDuplicateDecisionRoutes(t *testing.T) {
	processor := &fakeProcessor{}
	server, data := newTestServer(t, processor)
	processor.store = data
	first := dupeJob(t, data, "104", "a1", "Backend Platform Engineer", "Example", "台北市", nil)
	linkJob(t, data, first)
	second := dupeJob(t, data, "cake", "c1", "Backend Platform Engineer II", "Example", "台北市", nil)
	outcome := linkJob(t, data, second)
	if outcome.Merged || len(outcome.CandidateIDs) != 1 {
		t.Fatalf("outcome = %+v, want one pending candidate", outcome)
	}
	candidateID := outcome.CandidateIDs[0]

	var page struct {
		Items []struct {
			ID     int64  `json:"id"`
			Reason string `json:"reason"`
			A      struct {
				Title  string `json:"title"`
				Source string `json:"source"`
			} `json:"a"`
			B struct {
				Location string `json:"location"`
			} `json:"b"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	decodeInto(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/duplicates", nil)), &page)
	if len(page.Items) != 1 || page.Items[0].Reason != store.ReasonTitleSimilar || page.Items[0].A.Source != "104" || page.Items[0].B.Location != "台北市" {
		t.Fatalf("duplicates page = %+v", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("next_cursor = %v, want null on the last page", *page.NextCursor)
	}
	if code := serve(t, server, authedRequest(http.MethodGet, "/api/v1/duplicates?limit=0", nil)).Code; code != http.StatusBadRequest {
		t.Fatalf("invalid pagination status = %d", code)
	}

	merged := serve(t, server, authedRequest(http.MethodPost, "/api/v1/duplicates/"+itoa(candidateID)+"/merge", nil))
	if merged.Code != http.StatusOK {
		t.Fatalf("merge status = %d", merged.Code)
	}
	body := decode(t, merged)
	job := body["job"].(map[string]any)
	if int64(job["id"].(float64)) != first {
		t.Fatalf("merge returned job %v, want the canonical %d", job["id"], first)
	}
	if _, hasLetter := job["letter"]; !hasLetter {
		t.Fatal("the response must be the job view")
	}
	if repeat := serve(t, server, authedRequest(http.MethodPost, "/api/v1/duplicates/"+itoa(candidateID)+"/merge", nil)); repeat.Code != http.StatusConflict {
		t.Fatalf("second merge status = %d, want 409", repeat.Code)
	}
	if missing := serve(t, server, authedRequest(http.MethodPost, "/api/v1/duplicates/9999/ignore", nil)); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown candidate status = %d, want 404", missing.Code)
	}

	unmerged := serve(t, server, authedRequest(http.MethodPost, "/api/v1/jobs/"+itoa(second)+"/unmerge", nil))
	if unmerged.Code != http.StatusOK {
		t.Fatalf("unmerge status = %d", unmerged.Code)
	}
	restored := decode(t, unmerged)["job"].(map[string]any)
	if restored["process_state"] != "discovered" {
		t.Fatalf("restored state = %v, want discovered", restored["process_state"])
	}
	if again := serve(t, server, authedRequest(http.MethodPost, "/api/v1/jobs/"+itoa(second)+"/unmerge", nil)); again.Code != http.StatusConflict {
		t.Fatalf("second unmerge status = %d, want 409", again.Code)
	}
	// None of these decisions may reach the pipeline: they are store transactions.
	if len(processor.requested) != 0 || len(processor.rescored) != 0 {
		t.Fatalf("duplicate decisions reached the pipeline: %v %v", processor.requested, processor.rescored)
	}
}

func TestIgnoredCandidateLeavesTheList(t *testing.T) {
	server, data := newTestServer(t, &fakeProcessor{})
	first := dupeJob(t, data, "104", "a1", "Backend Platform Engineer", "Example", "台北市", nil)
	linkJob(t, data, first)
	second := dupeJob(t, data, "cake", "c1", "Backend Platform Engineer II", "Example", "台北市", nil)
	candidateID := linkJob(t, data, second).CandidateIDs[0]
	if code := serve(t, server, authedRequest(http.MethodPost, "/api/v1/duplicates/"+itoa(candidateID)+"/ignore", nil)).Code; code != http.StatusOK {
		t.Fatalf("ignore status = %d", code)
	}
	var page struct {
		Items []any `json:"items"`
	}
	decodeInto(t, serve(t, server, authedRequest(http.MethodGet, "/api/v1/duplicates", nil)), &page)
	if len(page.Items) != 0 {
		t.Fatalf("ignored candidate still listed: %+v", page.Items)
	}
	if repeat := serve(t, server, authedRequest(http.MethodPost, "/api/v1/duplicates/"+itoa(candidateID)+"/ignore", nil)); repeat.Code != http.StatusConflict {
		t.Fatalf("second ignore status = %d, want 409", repeat.Code)
	}
}

// AT-59: the capture payload names its source and is refused without it.
func TestCaptureDispatchesOnSource(t *testing.T) {
	processor := &fakeProcessor{
		jobResult:   pipeline.IngestResult{JobID: 4, ProcessState: "queued"},
		listResults: []pipeline.IngestResult{{JobID: 5, ProcessState: "discovered", Created: true}},
	}
	server, _ := newTestServer(t, processor)
	cakeJob := `{"props":{"pageProps":{"job":{"path":"senior-backend-engineer","title":"Senior Backend Engineer","description":"<p>合成內容</p>","locations":[{"full_name":"Taipei City, Taiwan"}],"remote":"no_remote_work","aasm_state":"published"},"company":{"path":"example-cloud","name":"Example Cloud","products_or_services":"合成平台"}}}}`
	detail := serve(t, server, authedRequest(http.MethodPost, "/api/v1/capture/job", map[string]any{
		"source": "cake", "url": "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer", "next_data": cakeJob,
	}))
	if detail.Code != http.StatusOK {
		t.Fatalf("cake detail capture status = %d", detail.Code)
	}
	// The Cake detail path the extension actually takes: the rendered page, since a
	// Cake detail page carries no listing state of its own.
	domCapture := serve(t, server, authedRequest(http.MethodPost, "/api/v1/capture/job", map[string]any{
		"source": "cake", "url": "https://www.cake.me/companies/example-cloud/jobs/senior-backend-engineer",
		"cake_dom": map[string]any{
			"title": "Senior Backend Engineer", "company_name": "Example Cloud",
			"sections": []map[string]string{{"title": "職缺描述", "body": "合成內容"}},
			"meta":     []string{"台北市內湖區", "月薪 80,000 ~ 120,000 元"},
		},
	}))
	if domCapture.Code != http.StatusOK {
		t.Fatalf("cake DOM capture status = %d body = %s", domCapture.Code, domCapture.Body.String())
	}
	cakeList := `{"props":{"pageProps":{"ssr":{"search":{"query":"backend","page":1,"filters":{}}},"initialState":{"jobSearch":{"entityByPathId":{"example-cloud:senior-backend-engineer":{"path":"senior-backend-engineer","title":"Senior Backend Engineer","locations":["台北市"],"page":{"path":"example-cloud","name":"Example Cloud","geo":"Taipei","country":"Taiwan"}}}}}}}}`
	list := serve(t, server, authedRequest(http.MethodPost, "/api/v1/capture/list", map[string]any{
		"source": "cake", "url": "https://www.cake.me/jobs?query=backend", "next_data": cakeList,
	}))
	if list.Code != http.StatusOK {
		t.Fatalf("cake list capture status = %d", list.Code)
	}
	for _, source := range []any{"linkedin", nil} {
		payload := map[string]any{"url": "https://www.cake.me/companies/example-cloud/jobs/x", "next_data": cakeJob}
		if source != nil {
			payload["source"] = source
		}
		response := serve(t, server, authedRequest(http.MethodPost, "/api/v1/capture/job", payload))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("capture with source %v status = %d, want 400", source, response.Code)
		}
	}
}
