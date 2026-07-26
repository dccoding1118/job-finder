package api

import (
	"net/http"

	"github.com/dccoding1118/job-finder/internal/store"
)

// verdict is the single source every frontend reads a decision from. It is
// derived here rather than stored, so the list marks, the sidebar, and the
// dashboard cannot disagree about the same job.
const (
	verdictUnfit          = "unfit"
	verdictRecommended    = "recommended"
	verdictNotRecommended = "not_recommended"
	verdictPendingDetail  = "pending_detail"
	verdictPendingScore   = "pending_score"
)

var verdictByState = map[string]string{
	"filtered_out":     verdictUnfit,
	"shortlisted":      verdictRecommended,
	"letter_requested": verdictRecommended,
	"letter_ready":     verdictRecommended,
	"letter_failed":    verdictRecommended,
	"scored":           verdictNotRecommended,
	"discovered":       verdictPendingDetail,
	"new":              verdictPendingScore,
	"queued":           verdictPendingScore,
}

// letterStateByState tells the dashboard whether to offer the generate entry,
// report work in progress, show the letter, or offer another attempt.
var letterStateByState = map[string]string{
	"shortlisted":      "none",
	"letter_requested": "requested",
	"letter_ready":     "ready",
	"letter_failed":    "failed",
}

var statesByVerdict = func() map[string][]string {
	states := map[string][]string{}
	for _, state := range []string{"discovered", "new", "queued", "filtered_out", "scored", "shortlisted", "letter_requested", "letter_ready", "letter_failed"} {
		verdict := verdictByState[state]
		states[verdict] = append(states[verdict], state)
	}
	return states
}()

func verdictOf(processState string) string { return verdictByState[processState] }

func jobListView(job store.Job, revisions ...string) map[string]any {
	current := ""
	if len(revisions) > 0 {
		current = revisions[0]
	}
	value := map[string]any{
		"id": job.ID, "source": job.Source, "url": job.URL, "title": job.Title, "company_name": job.CompanyName,
		"salary_min": job.SalaryMin, "salary_max": job.SalaryMax, "location": job.Location,
		"score_total": job.ScoreTotal, "process_state": job.ProcessState, "apply_state": job.ApplyState,
		"verdict": verdictOf(job.ProcessState), "filter_hits": nullableHits(job.FilterHits),
		"current_profile_revision": nullableRevision(current), "evaluation_profile_revision": job.ProfileRevision,
	}
	if job.ScoreTotal != nil {
		value["score_profile_revision"] = job.ProfileRevision
		value["score_stale"] = job.ProfileRevision == nil || current == "" || *job.ProfileRevision != current
	}
	if state, ok := letterStateByState[job.ProcessState]; ok {
		value["letter_state"] = state
	}
	return value
}

// jobViewWithGroup adds the cross-source group to one job view. It is what lets
// the user choose the platform to apply on, and what surfaces the aliases whose
// assessment this job now carries.
func (s *Server) jobViewWithGroup(r *http.Request, detail store.JobDetail) map[string]any {
	value := jobView(detail, s.currentProfileRevision())
	group, err := s.store.GroupOf(r.Context(), detail.Job.ID)
	if err != nil || group.GroupID == 0 {
		value["group"] = nil
		return value
	}
	value["group"] = group
	return value
}

func jobView(detail store.JobDetail, revisions ...string) map[string]any {
	value := jobListView(detail.Job, revisions...)
	current := ""
	if len(revisions) > 0 {
		current = revisions[0]
	}
	value["description"] = detail.Job.Description
	value["remote_type"] = detail.Job.RemoteType
	value["score"] = detail.Score
	value["letter"] = detail.Letter
	value["status_events"] = detail.Events
	value["score_profile_revision"] = nil
	value["letter_profile_revision"] = nil
	value["score_stale"] = false
	value["letter_stale"] = false
	if detail.Score != nil {
		value["score_profile_revision"] = detail.Score.ProfileRevision
		value["score_stale"] = detail.Score.ProfileRevision == nil || current == "" || *detail.Score.ProfileRevision != current
	}
	if detail.Letter != nil {
		value["letter_profile_revision"] = detail.Letter.ProfileRevision
		value["letter_stale"] = detail.Letter.ProfileRevision == nil || current == "" || *detail.Letter.ProfileRevision != current
	}
	if detail.Score != nil && detail.Job.ScoreTotal == nil {
		value["score_total"] = detail.Score.Total
	}
	return value
}

// runView pairs the fetch facts a run recorded with the current verdicts of the
// jobs it discovered. The distribution is queried now, not snapshotted then:
// the worker keeps scoring those jobs long after the run finished.
func runView(run store.Run, states map[string]int) map[string]any {
	return map[string]any{
		"id": run.ID, "started_at": run.StartedAt, "finished_at": run.FinishedAt, "trigger": run.Trigger,
		"stats": run.Stats, "error": run.Error, "verdicts": verdictCounts(states),
	}
}

func verdictCounts(states map[string]int) map[string]int {
	counts := map[string]int{verdictRecommended: 0, verdictNotRecommended: 0, verdictPendingScore: 0, verdictPendingDetail: 0, verdictUnfit: 0}
	for state, count := range states {
		if verdict := verdictOf(state); verdict != "" {
			counts[verdict] += count
		}
	}
	return counts
}

func nullableHits(hits []string) any {
	if len(hits) == 0 {
		return nil
	}
	return hits
}
