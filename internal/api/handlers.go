package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/dccoding1118/job-finder/internal/agents"
	"github.com/dccoding1118/job-finder/internal/crawler"
	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, pageErr := parsePagination(r); pageErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", pageErr.Error())
		return
	}
	jobs, err := s.store.ListJobs(r.Context(), filter, store.JobSortScore)
	if err != nil {
		writeError(w, 500, "internal", "unable to list jobs")
		return
	}
	page, err := pageJobs(jobs, r, s.activeRevisions())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid_request", "job id must be a positive integer")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		s.readJob(w, r, id)
	case len(parts) == 2 && parts[1] == "apply" && r.Method == http.MethodPost:
		s.applyJob(w, r, id)
	case len(parts) == 2 && parts[1] == "letter" && r.Method == http.MethodPost:
		s.requestLetter(w, r, id)
	case len(parts) == 2 && parts[1] == "reprocess" && r.Method == http.MethodPost:
		s.reprocessJob(w, r, id)
	case len(parts) == 2 && parts[1] == "process" && r.Method == http.MethodPost:
		s.processJobNow(w, r, id)
	case len(parts) == 2 && parts[1] == "unmerge" && r.Method == http.MethodPost:
		s.unmergeJob(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route was not found")
	}
}

func (s *Server) readJob(w http.ResponseWriter, r *http.Request, id int64) {
	detail, found, err := s.store.GetJobDetail(r.Context(), id)
	if err != nil {
		writeError(w, 500, "internal", "unable to read job")
		return
	}
	if !found {
		writeError(w, 404, "not_found", "job was not found")
		return
	}
	writeJSON(w, 200, s.jobViewWithGroup(r, detail))
}

func (s *Server) applyJob(w http.ResponseWriter, r *http.Request, id int64) {
	var request struct {
		ApplyState string `json:"apply_state"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ApplyState == "" {
		writeError(w, 400, "invalid_request", "apply_state is required")
		return
	}
	if err := s.store.TransitionApply(r.Context(), id, request.ApplyState, request.Note); err != nil {
		s.transitionError(w, err)
		return
	}
	detail, _, _ := s.store.GetJobDetail(r.Context(), id)
	writeJSON(w, 200, s.jobViewWithGroup(r, detail))
}

// requestLetter is the only entry through which a letter is ever drafted: the
// user expressing interest in one recommended job. It accepts the request and
// returns; the worker picks the job up on its own.
func (s *Server) requestLetter(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireProfile(w) {
		return
	}
	if s.pipeline == nil {
		writeError(w, 500, "internal", "letter requests are unavailable")
		return
	}
	if err := s.pipeline.RequestLetter(r.Context(), id); err != nil {
		s.transitionError(w, err)
		return
	}
	detail, _, err := s.store.GetJobDetail(r.Context(), id)
	if err != nil {
		writeError(w, 500, "internal", "unable to read job")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "requested", "job": s.jobViewWithGroup(r, detail)})
}

// reprocessJob returns a single job to the start of the pipeline. It exists so
// one wrong verdict — a screening rejection as much as a score — can be
// corrected on its own, without spending Agent budget on every other job.
func (s *Server) reprocessJob(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireProfile(w) {
		return
	}
	if s.pipeline == nil {
		writeError(w, 500, "internal", "reprocess requests are unavailable")
		return
	}
	switch err := s.pipeline.RequestReprocess(r.Context(), id); {
	case errors.Is(err, store.ErrReprocessNotAllowed):
		writeError(w, http.StatusConflict, "reprocess_not_allowed", "a job with a letter history or a merged job cannot be reprocessed")
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "job was not found")
		return
	case err != nil:
		s.transitionError(w, err)
		return
	}
	detail, _, err := s.store.GetJobDetail(r.Context(), id)
	if err != nil {
		writeError(w, 500, "internal", "unable to read job")
		return
	}
	writeJSON(w, 200, map[string]any{"status": detail.Job.ProcessState, "job": s.jobViewWithGroup(r, detail)})
}

// processJobNow puts one job the user is looking at through screening and
// scoring right away, ahead of the resident worker's batch and regardless of
// whether automatic processing is switched on or the day's budget is spent —
// it is the user asking for this one job, on the job they are looking at.
//
// The request is accepted and the work runs in the background: an Agent call
// takes far longer than a request should be held open, and the job views the
// Side Panel already polls report the result. Only the reasons the job cannot
// be processed at all are answered synchronously.
func (s *Server) processJobNow(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireProfile(w) {
		return
	}
	if s.pipeline == nil || !s.cfg.ResidentWorker {
		writeError(w, http.StatusConflict, "worker_not_resident", "this service does not carry the worker, so stages are driven by hand")
		return
	}
	detail, found, err := s.store.GetJobDetail(r.Context(), id)
	if err != nil {
		writeError(w, 500, "internal", "unable to read job")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "job was not found")
		return
	}
	if detail.Job.ProcessState != "new" && detail.Job.ProcessState != "queued" {
		writeError(w, http.StatusConflict, "not_waiting", "only a job waiting to be screened or scored can be processed now")
		return
	}
	// The request's context ends with the response, so the work carries a context
	// detached from it and keeps the request's values.
	ctx := context.WithoutCancel(r.Context())
	go func() {
		if err := s.pipeline.ProcessJobNow(ctx, id); err != nil {
			slog.Error("immediate processing failed", "job_id", id, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "processing", "job": s.jobViewWithGroup(r, detail)})
}

// settings reads and writes the runtime settings the user owns from the Side
// Panel. They live in the database rather than in config.yaml because the API
// service owns them while it runs, and a restart must not undo them.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, s.settingsView(r))
	case http.MethodPut:
		var request struct {
			AutoProcessing *bool `json:"auto_processing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AutoProcessing == nil {
			writeError(w, 400, "invalid_request", "auto_processing is required")
			return
		}
		if err := s.store.SetAutoProcessing(r.Context(), *request.AutoProcessing); err != nil {
			writeError(w, 500, "internal", "unable to save settings")
			return
		}
		writeJSON(w, 200, s.settingsView(r))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
	}
}

// settingsView reports the switch together with whether this process is the one
// that could act on it, so the view can say why a switch has no effect.
func (s *Server) settingsView(r *http.Request) map[string]any {
	enabled, err := s.store.AutoProcessing(r.Context())
	if err != nil {
		enabled = true
	}
	return map[string]any{"auto_processing": enabled, "resident_worker": s.cfg.ResidentWorker}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	counts, err := s.store.CountJobsByState(r.Context())
	if err != nil {
		writeError(w, 500, "internal", "unable to read job states")
		return
	}
	calls, err := s.store.RecentAgentCalls(r.Context(), recentAgentCallLimit)
	if err != nil {
		writeError(w, 500, "internal", "unable to read agent calls")
		return
	}
	usage, err := s.store.AgentUsageByDay(r.Context(), agentUsageDayWindow)
	if err != nil {
		writeError(w, 500, "internal", "unable to read agent usage")
		return
	}
	value := map[string]any{"jobs": counts, "agent_calls": agentCallViews(calls), "agent_usage_daily": usage, "settings": s.settingsView(r)}
	if s.pipeline != nil {
		if remaining, limited, err := s.pipeline.FilterBudgetRemaining(r.Context()); err == nil {
			value["filter_budget"] = map[string]any{"remaining": remaining, "limited": limited}
		}
		if remaining, limited, err := s.pipeline.ScoreBudgetRemaining(r.Context()); err == nil {
			value["score_budget"] = map[string]any{"remaining": remaining, "limited": limited}
		}
	}
	writeJSON(w, 200, value)
}

// recentAgentCallLimit keeps the progress view to the recent past.
const recentAgentCallLimit = 20

// agentUsageDayWindow bounds the daily token-usage breakdown to a couple of
// weeks; older days age out of the view rather than growing it without bound.
const agentUsageDayWindow = 14

// maxAgentCallDetail bounds how much of a rejected response the progress view
// quotes. The classified failure kind, not the quote, is what the view reads.
const maxAgentCallDetail = 400

// agentCallViews reports each audited call with the reason it was rejected. A
// call is ok when the runner answered and the answer passed validation, so a
// low score is a successful call: only runner errors and rejected responses are
// failures, and the kind says which.
func agentCallViews(calls []store.AgentCall) []map[string]any {
	views := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		view := map[string]any{
			"id": call.ID, "job_id": call.JobID, "role": call.Role, "runner": call.Runner, "model": call.Model,
			"ok": call.OK, "duration_ms": call.DurationMS, "created_at": call.CreatedAt,
			"input_tokens": call.Usage.InputTokens, "output_tokens": call.Usage.OutputTokens,
			"cache_read_tokens": call.Usage.CacheReadTokens, "cache_write_tokens": call.Usage.CacheWriteTokens,
			"reasoning_tokens": call.Usage.ReasoningTokens, "cost_usd": call.Usage.CostUSD,
		}
		if !call.OK {
			view["failure_kind"] = agents.ClassifyFailure(call.Role, call.Output)
			view["detail"] = truncateDetail(call.Output, maxAgentCallDetail)
		}
		views = append(views, view)
	}
	return views
}

func truncateDetail(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method_not_allowed", "method is not allowed")
		return
	}
	if _, pageErr := parsePagination(r); pageErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", pageErr.Error())
		return
	}
	// The queue is what waits on the user rather than on the worker: a job whose
	// JD text only they can fetch, by opening its page.
	jobs, err := s.store.ListJobs(r.Context(), store.JobFilter{ProcessStates: []string{"discovered"}}, store.JobSortNewest)
	if err != nil {
		writeError(w, 500, "internal", "unable to list queue")
		return
	}
	page, err := pageJobs(jobs, r, s.activeRevisions())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, pageErr := parsePagination(r); pageErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", pageErr.Error())
			return
		}
		runs, err := s.store.ListRuns(r.Context())
		if err != nil {
			writeError(w, 500, "internal", "unable to list runs")
			return
		}
		page, err := s.pageRuns(r, runs)
		if err != nil {
			if errors.Is(err, errInvalidPagination) {
				writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			writeError(w, 500, "internal", "unable to summarize runs")
			return
		}
		writeJSON(w, 200, page)
	case http.MethodPost:
		if !s.requireProfile(w) {
			return
		}
		if s.trigger == nil {
			writeError(w, 500, "internal", "manual run is unavailable")
			return
		}
		if !s.trigger.Start(context.Background()) {
			writeJSON(w, 200, map[string]string{"status": "already_running"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	default:
		writeError(w, 405, "method_not_allowed", "method is not allowed")
	}
}

type captureListRequest struct {
	// Source selects the parser. It is required and never guessed: two platforms
	// word the same page differently, and a wrong parser invents data.
	Source string `json:"source"`
	// URL is the list page the user had open; Cake derives nothing from it, 104
	// derives nothing either, but it is what a capture is attributed to.
	URL string `json:"url"`
	// NextData is Cake's embedded page state, sent only when it still matches the
	// conditions on screen.
	NextData string `json:"next_data"`
	Items    []struct {
		Href        string `json:"href"`
		Title       string `json:"title"`
		CompanyName string `json:"company_name"`
		CompanyInfo string `json:"company_info"`
		Location    string `json:"location"`
		SalaryText  string `json:"salary_text"`
		Remote      bool   `json:"remote"`
	} `json:"items"`
}

// parse turns one list capture into jobs through the parser its source names.
func (request captureListRequest) parse() ([]crawler.RawJob, error) {
	switch request.Source {
	case source104:
		items := make([]crawler.ListItem, 0, len(request.Items))
		for _, item := range request.Items {
			items = append(items, crawler.ListItem{Href: item.Href, Title: item.Title, CompanyName: item.CompanyName, CompanyInfo: item.CompanyInfo, Location: item.Location, SalaryText: item.SalaryText, Remote: item.Remote})
		}
		return crawler.ParseListItems(items)
	case sourceCake:
		items := make([]crawler.CakeListItem, 0, len(request.Items))
		for _, item := range request.Items {
			items = append(items, crawler.CakeListItem{Href: item.Href, Title: item.Title, CompanyName: item.CompanyName, Location: item.Location, SalaryText: item.SalaryText})
		}
		return crawler.ParseCakeList(crawler.CakeCapture{URL: request.URL, NextData: request.NextData, Items: items})
	default:
		return nil, errUnknownSource
	}
}

// captureList marks up a list page the user is still looking at. Nothing on
// this path calls an Agent or fetches a page: screening is string comparison.
func (s *Server) captureList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "method is not allowed")
		return
	}
	if !s.requireProfile(w) {
		return
	}
	if s.pipeline == nil {
		writeError(w, 500, "internal", "capture is unavailable")
		return
	}
	var request captureListRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || (len(request.Items) == 0 && strings.TrimSpace(request.NextData) == "") {
		writeError(w, 400, "invalid_request", "at least one list item is required")
		return
	}
	rows, err := request.parse()
	if err != nil {
		writeError(w, 400, "invalid_request", captureParseMessage(err))
		return
	}
	results, err := s.pipeline.IngestList(r.Context(), rows)
	if err != nil {
		writeError(w, 500, "internal", "unable to ingest list items")
		return
	}
	values := make([]any, 0, len(results))
	for i, result := range results {
		value := map[string]any{
			"id": result.JobID, "external_id": rows[i].ExternalID, "verdict": verdictOf(result.ProcessState), "process_state": result.ProcessState,
			"score_total": nil, "filter_hits": nullableHits(result.FilterHits), "created": result.Created,
		}
		if result.Score != nil {
			value["score_total"] = result.Score.Total
		}
		values = append(values, value)
	}
	writeJSON(w, 200, map[string]any{"items": values})
}

type captureJobRequest struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	// NextData is Cake's embedded page state; the JD of a Cake listing has no
	// other source.
	NextData string   `json:"next_data"`
	JSONLD   []string `json:"json_ld"`
	// CakeDOM is the rendered Cake detail page. Cake's detail pages carry no
	// listing state to read, so this is the only source of a Cake JD.
	CakeDOM *struct {
		Title       string `json:"title"`
		CompanyName string `json:"company_name"`
		Sections    []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"sections"`
		Meta []string `json:"meta"`
	} `json:"cake_dom"`
	DOM *struct {
		Title       string `json:"title"`
		CompanyName string `json:"company_name"`
		CompanyInfo string `json:"company_info"`
		Location    string `json:"location"`
		Description string `json:"description"`
		SalaryText  string `json:"salary_text"`
		Remote      bool   `json:"remote"`
	} `json:"dom"`
}

// parse turns one detail capture into a job through the parser its source names.
func (request captureJobRequest) parse() (crawler.RawJob, error) {
	switch request.Source {
	case source104:
		capture := crawler.JobCapture{URL: request.URL, JSONLD: request.JSONLD}
		if request.DOM != nil {
			capture.DOM = &crawler.JobDOM{Title: request.DOM.Title, CompanyName: request.DOM.CompanyName, CompanyInfo: request.DOM.CompanyInfo, Location: request.DOM.Location, Description: request.DOM.Description, SalaryText: request.DOM.SalaryText, Remote: request.DOM.Remote}
		}
		return crawler.ParseJobCapture(capture)
	case sourceCake:
		capture := crawler.CakeCapture{URL: request.URL, NextData: request.NextData}
		if request.CakeDOM != nil {
			dom := crawler.CakeJobDOM{Title: request.CakeDOM.Title, CompanyName: request.CakeDOM.CompanyName, Meta: request.CakeDOM.Meta}
			for _, section := range request.CakeDOM.Sections {
				dom.Sections = append(dom.Sections, crawler.CakeJobSection{Title: section.Title, Body: section.Body})
			}
			capture.JobDOM = &dom
		}
		return crawler.ParseCakeJob(capture)
	default:
		return crawler.RawJob{}, errUnknownSource
	}
}

// captureJob stores the full JD and screens it synchronously. Scoring is left
// to the worker, so a job that passes screening comes back pending and the
// sidebar polls for the result instead of holding the request open.
func (s *Server) captureJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "method is not allowed")
		return
	}
	if !s.requireProfile(w) {
		return
	}
	if s.pipeline == nil {
		writeError(w, 500, "internal", "capture is unavailable")
		return
	}
	var request captureJobRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.URL) == "" {
		writeError(w, 400, "invalid_request", "the captured page url is required")
		return
	}
	row, err := request.parse()
	if err != nil {
		writeError(w, 400, "invalid_request", captureParseMessage(err))
		return
	}
	result, err := s.pipeline.IngestJob(r.Context(), row)
	if err != nil {
		writeError(w, 500, "internal", "unable to ingest the captured job")
		return
	}
	verdict := verdictOf(result.ProcessState)
	value := map[string]any{
		"id": result.JobID, "verdict": verdict, "process_state": result.ProcessState,
		"score": result.Score, "filter_hits": nullableHits(result.FilterHits), "cached": result.Cached,
	}
	if verdict == verdictPendingScore {
		remaining, limited, budgetErr := s.pipeline.ScoreBudgetRemaining(r.Context())
		if budgetErr != nil {
			writeError(w, 500, "internal", "unable to read the scoring budget")
			return
		}
		if limited && remaining <= 0 {
			value["budget_exhausted"] = true
		}
	}
	writeJSON(w, 200, value)
}

// The capture sources the extension may name. A payload without one is rejected
// rather than assumed: the parsers are not interchangeable.
const (
	source104  = "104"
	sourceCake = "cake"
)

var errUnknownSource = errors.New("api: capture source must be 104 or cake")

func captureParseMessage(err error) string {
	if errors.Is(err, errUnknownSource) {
		return "capture source must be 104 or cake"
	}
	return "the captured page could not be parsed"
}

func (s *Server) transitionError(w http.ResponseWriter, err error) {
	if errors.Is(err, profile.ErrNotReady) {
		writeError(w, http.StatusConflict, "profile_not_ready", "Profile must be ready before processing jobs")
	} else if strings.Contains(err.Error(), "not found") {
		writeError(w, 404, "not_found", "job was not found")
	} else {
		writeError(w, 400, "invalid_transition", "requested state transition is not allowed")
	}
}

func (s *Server) requireProfile(w http.ResponseWriter) bool {
	if s.profiles == nil {
		return true
	}
	if _, err := s.profiles.Ready(); err != nil {
		writeError(w, http.StatusConflict, "profile_not_ready", "Profile must be ready before processing jobs")
		return false
	}
	return true
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	if s.profiles == nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile service is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		snapshot := s.profiles.Current()
		w.Header().Set("ETag", snapshot.ETag)
		estimate, err := s.store.EstimateActivation(r.Context(), store.Revisions{Filter: snapshot.FilterRevision, Score: snapshot.ScoreRevision})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Unable to estimate Profile reprocessing")
			return
		}
		var summary any
		if snapshot.Profile != nil {
			view := snapshot.Profile.SummaryView()
			directions := make([]string, 0, len(view.Directions))
			for _, direction := range view.Directions {
				directions = append(directions, direction.Title)
			}
			summary = map[string]any{
				"total_years": view.TotalYears, "management_years": view.ManagementYears,
				"skill_count": len(view.Skills), "education_count": len(view.Education),
				"experience_count": view.ExperienceCount, "remote": view.Remote, "directions": directions,
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": snapshot.Status, "profile": snapshot.Profile,
			"filter_revision": nullableRevision(snapshot.FilterRevision), "score_revision": nullableRevision(snapshot.ScoreRevision),
			"summary": summary, "issues": snapshot.Issues, "reprocess_estimate": activationView(estimate),
		})
	case http.MethodPut:
		expected := r.Header.Get("If-Match")
		if expected == "" {
			writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required")
			return
		}
		value, err := profile.DecodeJSON(r.Body)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "profile_invalid", "message": "Profile is invalid", "issues": []profile.Issue{{Code: "schema_invalid", Message: "Profile JSON does not match the schema"}}}})
			return
		}
		result, err := s.profiles.Save(expected, value)
		if errors.Is(err, profile.ErrConflict) {
			writeError(w, http.StatusPreconditionFailed, "profile_conflict", "Profile file changed; reload before saving")
			return
		}
		var validation profile.ValidationError
		if errors.As(err, &validation) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "profile_invalid", "message": "Profile is invalid", "issues": validation.Issues}})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "profile_save_failed", "Unable to save Profile")
			return
		}
		w.Header().Set("ETag", result.Snapshot.ETag)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ready", "filter_revision": result.Snapshot.FilterRevision, "score_revision": result.Snapshot.ScoreRevision,
			"filter_changed": result.FilterChanged, "score_changed": result.ScoreChanged,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
	}
}

func (s *Server) reprocessProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	if s.profiles == nil || s.activate == nil {
		writeError(w, http.StatusNotFound, "not_found", "Profile reprocessing is unavailable")
		return
	}
	snapshot, err := s.profiles.Ready()
	if errors.Is(err, profile.ErrNotReady) {
		writeError(w, http.StatusConflict, "profile_not_ready", "Profile must be ready before reprocessing jobs")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Unable to read active Profile")
		return
	}
	activation, err := s.activate(r.Context(), profile.Revisions{Filter: snapshot.FilterRevision, Score: snapshot.ScoreRevision}, *snapshot.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_reprocess_failed", "Unable to reprocess stale jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "queued", "filter_revision": snapshot.FilterRevision, "score_revision": snapshot.ScoreRevision, "activation": activation,
	})
}

func activationView(value store.ActivationStats) map[string]int {
	return map[string]int{
		"partial_screened": value.PartialScreened, "refiltered": value.Refiltered, "requeued": value.Requeued,
		"protected": value.Protected, "unchanged": value.Unchanged,
	}
}

func nullableRevision(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func parseFilter(r *http.Request) (store.JobFilter, error) {
	f := store.JobFilter{ProcessState: r.URL.Query().Get("process_state"), ApplyState: r.URL.Query().Get("apply_state"), Source: r.URL.Query().Get("source")}
	for _, value := range []string{f.ProcessState, f.ApplyState, f.Source} {
		if len(value) > 40 {
			return store.JobFilter{}, errors.New("filter is invalid")
		}
	}
	if verdict := r.URL.Query().Get("verdict"); verdict != "" {
		states, ok := statesByVerdict[verdict]
		if !ok {
			return store.JobFilter{}, errors.New("verdict is invalid")
		}
		f.ProcessStates = states
	}
	return f, nil
}

var errInvalidPagination = errors.New("invalid pagination")

type pagination struct {
	limit  int
	offset int
}

func parsePagination(r *http.Request) (pagination, error) {
	page := pagination{limit: 20}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return pagination{}, fmt.Errorf("%w: limit must be between 1 and 100", errInvalidPagination)
		}
		page.limit = parsed
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return pagination{}, fmt.Errorf("%w: cursor is invalid", errInvalidPagination)
		}
		parsed, err := strconv.Atoi(string(decoded))
		if err != nil || parsed < 1 {
			return pagination{}, fmt.Errorf("%w: cursor is invalid", errInvalidPagination)
		}
		page.offset = parsed
	}
	return page, nil
}

func pageSlice[T any](items []T, r *http.Request) ([]T, *string, error) {
	page, err := parsePagination(r)
	if err != nil {
		return nil, nil, err
	}
	if page.offset >= len(items) {
		return []T{}, nil, nil
	}
	end := min(page.offset+page.limit, len(items))
	selected := items[page.offset:end]
	if end == len(items) {
		return selected, nil, nil
	}
	next := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	return selected, &next, nil
}

func pageJobs(jobs []store.Job, r *http.Request, active activeRevisions) (map[string]any, error) {
	selected, next, err := pageSlice(jobs, r)
	if err != nil {
		return nil, err
	}
	values := make([]any, 0, len(selected))
	for _, job := range selected {
		values = append(values, jobListView(job, active))
	}
	return map[string]any{"items": values, "next_cursor": next}, nil
}

func (s *Server) activeRevisions() activeRevisions {
	if s.profiles == nil {
		return activeRevisions{}
	}
	snapshot, err := s.profiles.Ready()
	if err != nil {
		return activeRevisions{}
	}
	return activeRevisions{Filter: snapshot.FilterRevision, Score: snapshot.ScoreRevision}
}

func (s *Server) pageRuns(r *http.Request, runs []store.Run) (map[string]any, error) {
	selected, next, err := pageSlice(runs, r)
	if err != nil {
		return nil, err
	}
	values := make([]any, 0, len(selected))
	for _, run := range selected {
		states, err := s.store.SummarizeRunJobs(r.Context(), run.ID)
		if err != nil {
			return nil, err
		}
		values = append(values, runView(run, states))
	}
	return map[string]any{"items": values, "next_cursor": next}, nil
}
