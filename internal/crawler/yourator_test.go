package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dccoding1118/job-finder/internal/store"
)

func TestYouratorGroupsDirectionQueriesAndDeduplicatesDetails(t *testing.T) {
	var mu sync.Mutex
	listQueries := []string{}
	details := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /r/\n"))
	})
	mux.HandleFunc("/api/v4/jobs", func(w http.ResponseWriter, r *http.Request) {
		terms := append([]string(nil), r.URL.Query()["term[]"]...)
		sort.Strings(terms)
		key := strings.Join(terms, ",")
		mu.Lock()
		listQueries = append(listQueries, key)
		mu.Unlock()
		ids := map[string][]int64{
			"cloud,platform":         {1, 2},
			"Go,backend":             {2, 3},
			"Kubernetes,reliability": {3, 4},
		}[key]
		jobs := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			jobs = append(jobs, map[string]any{"id": id, "name": fmt.Sprintf("Synthetic %d", id), "path": fmt.Sprintf("/jobs/%d", id), "salary": "NT$ 10,000 - 20,000", "location": "Taipei", "company": map[string]string{"brand": "Synthetic Org"}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"payload": map[string]any{"hasMore": false, "jobs": jobs}})
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		details[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<section class="job-description">Synthetic description %s</section>`, html.EscapeString(r.URL.Path))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	jobs, err := (Yourator{BaseURL: server.URL, CheckRobots: true}).Fetch(context.Background(), threeQuerySpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 || strings.Join(listQueries, "|") != "cloud,platform|Go,backend|Kubernetes,reliability" {
		t.Fatalf("jobs/queries = %d/%v", len(jobs), listQueries)
	}
	for id := 1; id <= 4; id++ {
		if details[fmt.Sprintf("/jobs/%d", id)] != 1 {
			t.Fatalf("detail counts = %v", details)
		}
	}
}

func TestYouratorKeepsPageWhenListItemsAreUnusable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/jobs", func(w http.ResponseWriter, _ *http.Request) {
		jobs := []map[string]any{
			{"id": 1, "name": "Stated locality", "path": "/jobs/1", "salary": "NT$ 10,000 - 20,000", "location": "Taipei", "company": map[string]string{"brand": "Synthetic Org"}},
			{"id": 2, "name": "No locality", "path": "/jobs/2", "salary": "面議", "location": nil, "company": map[string]string{"brand": "Synthetic Org"}},
			{"id": 3, "name": "No company", "path": "/jobs/3", "location": "Taipei", "company": map[string]string{}},
			{"id": 0, "name": "No id", "path": "/jobs/4", "location": "Taipei", "company": map[string]string{"brand": "Synthetic Org"}},
			{"id": 5, "name": "", "path": "", "location": "Taipei", "company": map[string]string{"brand": "Synthetic Org"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"payload": map[string]any{"hasMore": false, "jobs": jobs}})
	})
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<section class="job-description">Synthetic description %s</section>`, html.EscapeString(r.URL.Path))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	jobs, err := (Yourator{BaseURL: server.URL}).Fetch(context.Background(), oneQuerySpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want the two identifiable listings", len(jobs))
	}
	if jobs[0].ExternalID != "1" || jobs[0].Location != "Taipei" {
		t.Fatalf("stated locality job = %+v", jobs[0])
	}
	if jobs[1].ExternalID != "2" || jobs[1].Location != store.LocationUnknown {
		t.Fatalf("job without locality = %+v", jobs[1])
	}
}

func TestExtractJobDescriptionIncludesNestedYouratorSections(t *testing.T) {
	source := `<html><body>
<section class="relative job-description row main-info">
  <div class="job__content">
    <div><h2 class="job-heading">工作內容</h2><section class="content__area"><p>Build cloud services.</p></section></div>
    <div><h2 class="job-heading">條件要求</h2><section class="content__area"><p>Requires Node.js &amp;amp; TypeScript.</p></section></div>
    <div><h2 class="job-heading">加分條件</h2><section class="content__area"><p>Experience with containers.</p></section></div>
  </div>
</section>
</body></html>`

	description := extractJobDescription(source)
	for _, expected := range []string{"工作內容\nBuild cloud services.", "條件要求\nRequires Node.js & TypeScript.", "加分條件\nExperience with containers."} {
		if !strings.Contains(description, expected) {
			t.Fatalf("description %q does not contain %q", description, expected)
		}
	}
	if strings.Contains(description, "</section>") {
		t.Fatalf("description contains HTML: %q", description)
	}
}

// The JD section on a real listing encloses the page's own hydration state,
// which differs on every request. Keeping it would make an unchanged listing
// look changed on every fetch, and that resets the job to `new` — re-screening
// and re-scoring the whole batch each run.
func TestExtractJobDescriptionDropsPageStateAndStaysStable(t *testing.T) {
	page := func(events string) string {
		return `<section class="relative job-description row main-info">
  <div><h2>工作內容</h2><section class="content__area"><p>Build cloud services.</p></section></div>
  <div class="recommendations">
    <script type="application/json" data-dom-id="RelativeEventWrapper-react-component-` + events + `">{"title":"相關的主題專區","events":[{"id":` + events + `,"name":"遠端工作職缺專區 Remote Jobs"}]}</script>
    <style>.recommendations { display: none; }</style>
  </div>
</section>`
	}

	first := extractJobDescription(page("71"))
	second := extractJobDescription(page("82"))
	if first != second {
		t.Fatalf("description is not stable across requests:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "工作內容\nBuild cloud services.") {
		t.Fatalf("description lost the job content: %q", first)
	}
	for _, leaked := range []string{"相關的主題專區", "RelativeEventWrapper", "display: none"} {
		if strings.Contains(first, leaked) {
			t.Fatalf("description carries page state %q: %q", leaked, first)
		}
	}
}

func TestExtractJobDescriptionRejectsIncompleteOuterSection(t *testing.T) {
	if description := extractJobDescription(`<section class="job-description"><section>incomplete</section>`); description != "" {
		t.Fatalf("description = %q, want empty", description)
	}
}

func TestYouratorRetriesAndUsesInjectedDelay(t *testing.T) {
	attempts := 0
	delays := []time.Duration{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"payload": map[string]any{"hasMore": false, "jobs": []any{}}})
	}))
	defer server.Close()
	y := Yourator{
		BaseURL: server.URL, RetryMax: 2, RetryBackoff: 20 * time.Millisecond,
		RequestDelayMin: 10 * time.Millisecond, RequestDelayMax: 20 * time.Millisecond,
		RandomFloat: func() float64 { return .5 },
		Sleep:       func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil },
	}
	jobs, err := y.Fetch(context.Background(), SearchSpec{Queries: []SearchQuery{{Direction: "P1", Keywords: []string{"cloud"}}}, MaxPages: 1})
	if err != nil || len(jobs) != 0 || attempts != 2 {
		t.Fatalf("jobs/attempts/error = %d/%d/%v", len(jobs), attempts, err)
	}
	if len(delays) != 2 || delays[0] != 20*time.Millisecond || delays[1] != 15*time.Millisecond {
		t.Fatalf("delays = %v", delays)
	}
}

func TestYouratorStopsForRobotsAndChallenge(t *testing.T) {
	requests := 0
	robotsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /api/\n"))
	}))
	defer robotsServer.Close()
	if _, err := (Yourator{BaseURL: robotsServer.URL, CheckRobots: true}).Fetch(context.Background(), oneQuerySpec()); err == nil || !strings.Contains(err.Error(), "disallows") || requests != 1 {
		t.Fatalf("robots error/requests = %v/%d", err, requests)
	}

	challengeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>Verify you are human</title>"))
	}))
	defer challengeServer.Close()
	if _, err := (Yourator{BaseURL: challengeServer.URL}).Fetch(context.Background(), oneQuerySpec()); err == nil || !strings.Contains(err.Error(), "verification challenge") {
		t.Fatalf("challenge error = %v", err)
	}
}

func TestYouratorBoundsStalledRequestWithClientTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // stall until the test releases the handler
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	defer close(release)

	y := Yourator{BaseURL: server.URL, Client: &http.Client{Timeout: 100 * time.Millisecond}}
	start := time.Now()
	_, err := y.Fetch(context.Background(), oneQuerySpec())
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "request Yourator") {
		t.Fatalf("stalled fetch error = %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("client timeout did not bound the stalled request: %v", elapsed)
	}
}

func TestSearchSpecRejectsMoreThanThreeQueriesBeforeRequest(t *testing.T) {
	spec := threeQuerySpec()
	spec.Queries = append(spec.Queries, SearchQuery{Direction: "P4", Keywords: []string{"extra"}})
	if err := spec.Validate(); err == nil {
		t.Fatal("accepted four direction queries")
	}
	if err := (SearchSpec{Queries: []SearchQuery{{Direction: "P1", Keywords: []string{""}}}, MaxPages: 1}).Validate(); err == nil {
		t.Fatal("accepted empty keyword")
	}
}

func oneQuerySpec() SearchSpec {
	return SearchSpec{Queries: []SearchQuery{{Direction: "P1", Keywords: []string{"cloud"}}}, MaxPages: 1}
}

func threeQuerySpec() SearchSpec {
	return SearchSpec{Queries: []SearchQuery{
		{Direction: "P1", Keywords: []string{"cloud", "platform"}},
		{Direction: "P2", Keywords: []string{"backend", "Go"}},
		{Direction: "P3", Keywords: []string{"Kubernetes", "reliability"}},
	}, MaxPages: 1}
}
