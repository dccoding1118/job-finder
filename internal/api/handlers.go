package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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
	jobs, err := s.store.ListJobs(r.Context(), filter, store.JobSortScore)
	if err != nil {
		writeError(w, 500, "internal", "unable to list jobs")
		return
	}
	writeJSON(w, 200, pageJobs(jobs, r, s.currentProfileRevision()))
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
	writeJSON(w, 200, jobView(detail, s.currentProfileRevision()))
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
	writeJSON(w, 200, jobView(detail, s.currentProfileRevision()))
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
	writeJSON(w, 200, map[string]any{"status": "requested", "job": jobView(detail, s.currentProfileRevision())})
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method_not_allowed", "method is not allowed")
		return
	}
	jobs, err := s.store.ListJobs(r.Context(), store.JobFilter{ProcessState: "discovered"}, store.JobSortNewest)
	if err != nil {
		writeError(w, 500, "internal", "unable to list queue")
		return
	}
	writeJSON(w, 200, pageJobs(jobs, r, s.currentProfileRevision()))
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runs, err := s.store.ListRuns(r.Context())
		if err != nil {
			writeError(w, 500, "internal", "unable to list runs")
			return
		}
		page, err := s.pageRuns(r, runs)
		if err != nil {
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
	Items []struct {
		Href        string `json:"href"`
		Title       string `json:"title"`
		CompanyName string `json:"company_name"`
		CompanyInfo string `json:"company_info"`
		Location    string `json:"location"`
		SalaryText  string `json:"salary_text"`
		Remote      bool   `json:"remote"`
	} `json:"items"`
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
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Items) == 0 {
		writeError(w, 400, "invalid_request", "at least one list item is required")
		return
	}
	items := make([]crawler.ListItem, 0, len(request.Items))
	for _, item := range request.Items {
		items = append(items, crawler.ListItem{Href: item.Href, Title: item.Title, CompanyName: item.CompanyName, CompanyInfo: item.CompanyInfo, Location: item.Location, SalaryText: item.SalaryText, Remote: item.Remote})
	}
	rows, err := crawler.ParseListItems(items)
	if err != nil {
		writeError(w, 400, "invalid_request", "list items could not be parsed")
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
	URL    string   `json:"url"`
	JSONLD []string `json:"json_ld"`
	DOM    *struct {
		Title       string `json:"title"`
		CompanyName string `json:"company_name"`
		CompanyInfo string `json:"company_info"`
		Location    string `json:"location"`
		Description string `json:"description"`
		SalaryText  string `json:"salary_text"`
		Remote      bool   `json:"remote"`
	} `json:"dom"`
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
	capture := crawler.JobCapture{URL: request.URL, JSONLD: request.JSONLD}
	if request.DOM != nil {
		capture.DOM = &crawler.JobDOM{Title: request.DOM.Title, CompanyName: request.DOM.CompanyName, CompanyInfo: request.DOM.CompanyInfo, Location: request.DOM.Location, Description: request.DOM.Description, SalaryText: request.DOM.SalaryText, Remote: request.DOM.Remote}
	}
	row, err := crawler.ParseJobCapture(capture)
	if err != nil {
		writeError(w, 400, "invalid_request", "the captured page could not be parsed")
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
		estimate, err := s.store.EstimateActivation(r.Context(), snapshot.Revision)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "Unable to estimate Profile reprocessing")
			return
		}
		var summary any
		if snapshot.Profile != nil {
			directions := make([]string, 0, len(snapshot.Profile.Preferences.Directions))
			for _, direction := range snapshot.Profile.Preferences.Directions {
				directions = append(directions, direction.Title)
			}
			summary = map[string]any{
				"years_of_experience": snapshot.Profile.YearsOfExperience,
				"skill_count":         len(snapshot.Profile.Skills.Expert) + len(snapshot.Profile.Skills.Proficient) + len(snapshot.Profile.Skills.Familiar),
				"experience_count":    len(snapshot.Profile.Experiences), "directions": directions,
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": snapshot.Status, "profile": snapshot.Profile, "profile_revision": nullableRevision(snapshot.Revision),
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
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "profile_revision": result.Snapshot.Revision, "semantic_changed": result.SemanticChanged})
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
	activation, err := s.activate(r.Context(), snapshot.Revision, *snapshot.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_reprocess_failed", "Unable to reprocess stale jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "queued", "profile_revision": snapshot.Revision, "activation": activation,
	})
}

func activationView(value store.ActivationStats) map[string]int {
	return map[string]int{"partial_screened": value.PartialScreened, "requeued": value.Requeued, "protected": value.Protected, "unchanged": value.Unchanged}
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

func pageSlice[T any](items []T, r *http.Request) []T {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return []T{}
		}
		limit = parsed
	}
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func pageJobs(jobs []store.Job, r *http.Request, revisions ...string) map[string]any {
	selected := pageSlice(jobs, r)
	values := make([]any, 0, len(selected))
	for _, job := range selected {
		values = append(values, jobListView(job, revisions...))
	}
	return map[string]any{"items": values, "next_cursor": nil}
}

func (s *Server) currentProfileRevision() string {
	if s.profiles == nil {
		return ""
	}
	snapshot, err := s.profiles.Ready()
	if err != nil {
		return ""
	}
	return snapshot.Revision
}

func (s *Server) pageRuns(r *http.Request, runs []store.Run) (map[string]any, error) {
	selected := pageSlice(runs, r)
	values := make([]any, 0, len(selected))
	for _, run := range selected {
		states, err := s.store.SummarizeRunJobs(r.Context(), run.ID)
		if err != nil {
			return nil, err
		}
		values = append(values, runView(run, states))
	}
	return map[string]any{"items": values, "next_cursor": nil}, nil
}
