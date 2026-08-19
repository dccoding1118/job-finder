package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dccoding1118/job-finder/internal/store"
)

const defaultYouratorBaseURL = "https://www.yourator.co"

// defaultRequestTimeout bounds a single HTTP request (connection, redirects and
// body read) when the caller does not supply its own Client, so a stalled real
// source cannot hang the fetch indefinitely.
const defaultRequestTimeout = 30 * time.Second

var (
	tagPattern             = regexp.MustCompile(`(?s)<[^>]*>`)
	blockTagPattern        = regexp.MustCompile(`(?is)</?(?:br|div|h[1-6]|hr|li|ol|p|section|ul)\b[^>]*>`)
	jobSectionStartPattern = regexp.MustCompile(`(?is)<section[^>]*\bjob-description\b[^>]*>`)
	sectionTagPattern      = regexp.MustCompile(`(?is)</?section\b[^>]*>`)
	// The JD section encloses the page's own script elements, whose bodies are
	// never prose: they carry the component state the page hydrates from. Their
	// contents are dropped whole rather than flattened, because stripping only
	// the tags would turn that state into JD text (see htmlToText).
	scriptElementPattern = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleElementPattern  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	salaryPattern        = regexp.MustCompile(`(?i)(?:NT\$\s*)?([\d,]+)\s*-\s*([\d,]+)`)
)

type Yourator struct {
	// Logger receives one line per query and per page. It defaults to the process
	// logger, so an adapter built without one still reports progress.
	Logger                           *slog.Logger
	BaseURL                          string
	Client                           *http.Client
	UserAgent, Referer               string
	RequestDelayMin, RequestDelayMax time.Duration
	RetryMax                         int
	RetryBackoff                     time.Duration
	CheckRobots                      bool
	Sleep                            func(context.Context, time.Duration) error
	RandomFloat                      func() float64
}

type requestState struct{ count int }

func (y Yourator) Name() string { return "yourator" }

// Fetch walks every query's list pages and reads each listing's detail page.
// One job is one batch, so the caller stores it the moment it is parsed and a
// fetch interrupted after nine minutes keeps the nine minutes of work. Every
// query and every page is logged: the source imposes a delay between requests,
// so a healthy fetch and a hung one look identical from the outside without it.
func (y Yourator) Fetch(ctx context.Context, spec SearchSpec, emit func(Batch) error) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if emit == nil {
		return fmt.Errorf("crawler: an emit function is required")
	}
	base := strings.TrimRight(y.BaseURL, "/")
	if base == "" {
		base = defaultYouratorBaseURL
	}
	client := y.Client
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	state := &requestState{}
	if y.CheckRobots {
		if err := y.checkRobots(ctx, client, base, state); err != nil {
			return err
		}
	}
	log := y.logger()
	seen := make(map[string]struct{})
	harvested := 0
	for _, query := range spec.Queries {
		log.Info("fetching a search query", "source", y.Name(), "direction", query.Direction, "keywords", strings.Join(query.Keywords, ","), "max_pages", spec.MaxPages)
		for page := 1; page <= spec.MaxPages; page++ {
			pageStartedAt := time.Now()
			u, _ := url.Parse(base + "/api/v4/jobs")
			q := u.Query()
			for _, term := range query.Keywords {
				q.Add("term[]", term)
			}
			q.Set("page", strconv.Itoa(page))
			u.RawQuery = q.Encode()
			body, err := y.get(ctx, client, u.String(), "application/json", state)
			if err != nil {
				return err
			}
			var response struct {
				Payload struct {
					HasMore bool `json:"hasMore"`
					Jobs    []struct {
						ID                           int64 `json:"id"`
						Name, Path, Salary, Location string
						Company                      struct {
							Brand string `json:"brand"`
						} `json:"company"`
					} `json:"jobs"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(body, &response); err != nil {
				return fmt.Errorf("crawler: decode Yourator list: %w", err)
			}
			if response.Payload.Jobs == nil {
				return fmt.Errorf("crawler: Yourator response has no jobs")
			}
			log.Info("read a list page", "source", y.Name(), "direction", query.Direction, "page", page, "listings", len(response.Payload.Jobs), "has_more", response.Payload.HasMore)
			for _, item := range response.Payload.Jobs {
				// A listing without an id, title, path or company cannot be
				// identified, fetched or judged, so it is skipped rather than
				// failing the page: one unusable entry would otherwise discard
				// every other job of this run, across every source.
				if item.ID == 0 || item.Name == "" || item.Path == "" || item.Company.Brand == "" {
					continue
				}
				externalID := strconv.FormatInt(item.ID, 10)
				if _, exists := seen[externalID]; exists {
					continue
				}
				seen[externalID] = struct{}{}
				detailStartedAt := time.Now()
				detail, err := y.get(ctx, client, base+item.Path, "text/html", state)
				if err != nil {
					return err
				}
				description := extractJobDescription(string(detail))
				min, max := parseSalary(item.Salary)
				location := item.Location
				if location == "" {
					location = store.LocationUnknown
				}
				job := RawJob{Source: y.Name(), ExternalID: externalID, URL: base + item.Path, Title: item.Name, CompanyName: item.Company.Brand, CompanyInfo: "", Description: description, SalaryMin: min, SalaryMax: max, Location: location, RemoteType: remoteType(item.Name + "\n" + description)}
				harvested++
				log.Debug("read a listing", "source", y.Name(), "direction", query.Direction, "page", page, "external_id", externalID, "description_chars", len(description), "duration_ms", time.Since(detailStartedAt).Milliseconds(), "harvested", harvested)
				if err := emit(Batch{Direction: query.Direction, Page: page, Jobs: []RawJob{job}}); err != nil {
					return err
				}
			}
			log.Info("finished a list page", "source", y.Name(), "direction", query.Direction, "page", page, "harvested", harvested, "duration_ms", time.Since(pageStartedAt).Milliseconds())
			// The empty batch reports the page even when every listing on it was a
			// repeat, so a fetch that harvests nothing for several pages still shows
			// as making progress rather than as stalled.
			if err := emit(Batch{Direction: query.Direction, Page: page}); err != nil {
				return err
			}
			if !response.Payload.HasMore {
				break
			}
		}
	}
	log.Info("fetch finished", "source", y.Name(), "queries", len(spec.Queries), "harvested", harvested, "requests", state.count)
	return nil
}

func (y Yourator) logger() *slog.Logger {
	if y.Logger != nil {
		return y.Logger
	}
	return slog.Default()
}

func (y Yourator) get(ctx context.Context, client *http.Client, address, expectedContentType string, state *requestState) ([]byte, error) {
	for attempt := 0; attempt <= y.RetryMax; attempt++ {
		if state.count > 0 {
			if err := y.wait(ctx, y.randomDelay()); err != nil {
				return nil, err
			}
		}
		state.count++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		ua := y.UserAgent
		if ua == "" {
			ua = "jobfinder/1.0 (+local personal job matcher)"
		}
		req.Header.Set("User-Agent", ua)
		if y.Referer != "" {
			req.Header.Set("Referer", y.Referer)
		}
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			if attempt < y.RetryMax {
				if err := y.wait(ctx, y.retryDelay(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("crawler: request Yourator: %w", requestErr)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("crawler: read Yourator response: %w", readErr)
		}
		if isChallenge(body) {
			return nil, fmt.Errorf("crawler: Yourator returned a login or verification challenge")
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt < y.RetryMax {
				if err := y.wait(ctx, y.retryDelay(attempt)); err != nil {
					return nil, err
				}
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("crawler: Yourator returned %s", resp.Status)
		}
		if expectedContentType != "" && !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), expectedContentType) {
			return nil, fmt.Errorf("crawler: Yourator returned unexpected content type")
		}
		return body, nil
	}
	return nil, fmt.Errorf("crawler: Yourator retry limit exceeded")
}

func (y Yourator) checkRobots(ctx context.Context, client *http.Client, base string, state *requestState) error {
	body, err := y.get(ctx, client, base+"/robots.txt", "text/plain", state)
	if err != nil {
		return fmt.Errorf("crawler: verify Yourator robots.txt: %w", err)
	}
	for _, path := range []string{"/api/v4/jobs", "/jobs/"} {
		if robotsDisallow(string(body), path) {
			return fmt.Errorf("crawler: Yourator robots.txt disallows %s", path)
		}
	}
	return nil
}

func (y Yourator) randomDelay() time.Duration {
	if y.RequestDelayMax <= y.RequestDelayMin {
		return y.RequestDelayMin
	}
	random := y.RandomFloat
	if random == nil {
		random = rand.Float64
	}
	return y.RequestDelayMin + time.Duration(random()*float64(y.RequestDelayMax-y.RequestDelayMin))
}

func (y Yourator) retryDelay(attempt int) time.Duration {
	return y.RetryBackoff * time.Duration(1<<attempt)
}

func (y Yourator) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if y.Sleep != nil {
		return y.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isChallenge(body []byte) bool {
	value := strings.ToLower(string(body))
	for _, marker := range []string{"captcha", "cloudflare challenge", "verify you are human", "請先登入", "人機驗證", "驗證碼"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func robotsDisallow(source, target string) bool {
	active := false
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		switch name {
		case "user-agent":
			active = value == "*" || strings.EqualFold(value, "jobfinder")
		case "disallow":
			if active && value != "" && strings.HasPrefix(target, value) {
				return true
			}
		}
	}
	return false
}

func extractJobDescription(source string) string {
	outer := jobSectionStartPattern.FindStringIndex(source)
	if outer == nil {
		return ""
	}
	fragment := source[outer[0]:]
	depth := 0
	bodyStart := -1
	for _, bounds := range sectionTagPattern.FindAllStringIndex(fragment, -1) {
		tag := strings.ToLower(fragment[bounds[0]:bounds[1]])
		if strings.HasPrefix(tag, "</") {
			depth--
			if depth == 0 && bodyStart >= 0 {
				return htmlToText(fragment[bodyStart:bounds[0]])
			}
			continue
		}
		depth++
		if bodyStart < 0 {
			bodyStart = bounds[1]
		}
	}
	return ""
}

// htmlToText flattens one HTML fragment into the plain text the database keeps.
// Script and style bodies are removed first: they are markup-free, so tag
// stripping alone would keep them, and the state a page hydrates from is both
// far larger than the JD around it and different on every request — which would
// make the content hash of an unchanged listing change on every fetch, sending
// the job back through screening and scoring each time.
func htmlToText(source string) string {
	value := scriptElementPattern.ReplaceAllString(source, " ")
	value = styleElementPattern.ReplaceAllString(value, " ")
	value = blockTagPattern.ReplaceAllString(value, "\n")
	value = tagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(html.UnescapeString(value))
	lines := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func parseSalary(value string) (*int, *int) {
	m := salaryPattern.FindStringSubmatch(value)
	if len(m) != 3 {
		return nil, nil
	}
	parse := func(s string) *int {
		n, e := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
		if e != nil {
			return nil
		}
		return &n
	}
	return parse(m[1]), parse(m[2])
}

func remoteType(value string) string {
	v := strings.ToLower(value)
	if strings.Contains(v, "remote") || strings.Contains(v, "遠端") {
		return "remote"
	}
	if strings.Contains(v, "hybrid") || strings.Contains(v, "混合") {
		return "hybrid"
	}
	return "onsite"
}
