package api

import (
	"net/http"
	"time"

	"github.com/dccoding1118/job-finder/internal/pipeline"
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
	verdictPendingScreen  = "pending_screen"
	verdictPendingScore   = "pending_score"
)

// Each verdict names what the system is doing with the job, not the internal
// state's name. `new` and `queued` are deliberately apart: one is still being
// screened, the other has passed screening and is waiting to be scored.
var verdictByState = map[string]string{
	"filtered_out":     verdictUnfit,
	"shortlisted":      verdictRecommended,
	"letter_requested": verdictRecommended,
	"letter_ready":     verdictRecommended,
	"letter_failed":    verdictRecommended,
	"scored":           verdictNotRecommended,
	// `discovered` waits on the user, not on the system: only opening the page
	// yields the JD text.
	"discovered": verdictPendingDetail,
	"new":        verdictPendingScreen,
	"queued":     verdictPendingScore,
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

// activeRevisions is the pair a view compares each stored result against to
// report staleness. Both are empty when no Profile is ready.
type activeRevisions struct {
	Filter string
	Score  string
}

func jobListView(job store.Job, active activeRevisions) map[string]any {
	value := map[string]any{
		"id": job.ID, "source": job.Source, "url": job.URL, "title": job.Title, "company_name": job.CompanyName,
		"salary_min": job.SalaryMin, "salary_max": job.SalaryMax, "location": job.Location,
		"score_total": job.ScoreTotal, "process_state": job.ProcessState, "apply_state": job.ApplyState,
		"verdict": verdictOf(job.ProcessState), "filter_hits": nullableHits(job.FilterHits),
		"current_filter_revision": nullableRevision(active.Filter), "current_score_revision": nullableRevision(active.Score),
		"filter_result_revision": job.FilterRevision, "score_result_revision": job.ScoreRevision,
		"filter_stale": isStale(job.FilterRevision, active.Filter),
	}
	if job.ScoreTotal != nil {
		value["score_stale"] = isStale(job.ScoreRevision, active.Score)
	}
	if state, ok := letterStateByState[job.ProcessState]; ok {
		value["letter_state"] = state
	}
	return value
}

// isStale reports a result produced under a revision that is no longer active.
// Staleness is derived, never stored: it changes no verdict and no apply state.
func isStale(recorded *string, active string) bool {
	return recorded == nil || active == "" || *recorded != active
}

// jobViewWithGroup adds the cross-source group to one job view. It is what lets
// the user choose the platform to apply on, and what surfaces the aliases whose
// assessment this job now carries.
func (s *Server) jobViewWithGroup(r *http.Request, detail store.JobDetail) map[string]any {
	value := jobView(detail, s.activeRevisions())
	group, err := s.store.GroupOf(r.Context(), detail.Job.ID)
	if err != nil || group.GroupID == 0 {
		value["group"] = nil
		return value
	}
	value["group"] = group
	return value
}

func jobView(detail store.JobDetail, active activeRevisions) map[string]any {
	value := jobListView(detail.Job, active)
	value["description"] = detail.Job.Description
	value["remote_type"] = detail.Job.RemoteType
	value["score"] = detail.Score
	value["letter"] = detail.Letter
	value["status_events"] = detail.Events
	// The per-condition screening result is what answers "why is this unfit" and
	// "what is missing" — a list of hit names alone cannot.
	value["filter_result"] = detail.Filter
	value["score_result_revision"] = nil
	value["letter_revision"] = nil
	value["score_stale"] = false
	value["letter_stale"] = false
	if detail.Score != nil {
		value["score_result_revision"] = detail.Score.ScoreRevision
		value["score_stale"] = isStale(detail.Score.ScoreRevision, active.Score)
	}
	if detail.Letter != nil {
		value["letter_revision"] = detail.Letter.ScoreRevision
		value["letter_stale"] = isStale(detail.Letter.ScoreRevision, active.Score) || isStale(detail.Letter.FilterRevision, active.Filter)
	}
	if detail.Score != nil && detail.Job.ScoreTotal == nil {
		value["score_total"] = detail.Score.Total
	}
	return value
}

// runView pairs the fetch facts a run recorded with the current verdicts of the
// jobs it discovered. The distribution is queried now, not snapshotted then:
// the worker keeps scoring those jobs long after the run finished.
// runStaleAfter is how long a run may go without reporting progress before it
// is presented as stalled rather than as running. A single source request can
// take a minute and a half on its own — the configured delay, a 30 second
// timeout and two retries — so the window has to clear that comfortably or a
// healthy fetch would be reported as dead.
const runStaleAfter = 5 * time.Minute

// runState names what a reader needs to know about a run and cannot see from
// its counts: a finished run and a fetch whose process was killed both stop
// writing, and only the heartbeat separates them.
const (
	runStateRunning = "running"
	runStateStalled = "stalled"
	runStateDone    = "done"
	runStateFailed  = "failed"
)

func runView(run store.Run, states map[string]int, now time.Time) map[string]any {
	return map[string]any{
		"id": run.ID, "started_at": run.StartedAt, "finished_at": run.FinishedAt, "heartbeat_at": run.HeartbeatAt,
		"state": runState(run, now), "trigger": run.Trigger,
		"stats": run.Stats, "error": run.Error, "verdicts": verdictCounts(states),
	}
}

// runState derives a run's state. A run recorded before heartbeats existed has
// none, and an unfinished one of those is reported as stalled rather than as
// running: it belongs to a process that ended long ago.
func runState(run store.Run, now time.Time) string {
	if run.FinishedAt != nil {
		if run.Error != nil {
			return runStateFailed
		}
		return runStateDone
	}
	if run.HeartbeatAt != nil && now.Sub(*run.HeartbeatAt) < runStaleAfter {
		return runStateRunning
	}
	return runStateStalled
}

// inFlightViews reports the work a process is running right now with how long
// each unit has been running, which is the one thing an audited call cannot
// say: it is only written once the call is over.
func inFlightViews(units []pipeline.Unit, now time.Time) []map[string]any {
	views := make([]map[string]any, 0, len(units))
	for _, unit := range units {
		view := map[string]any{"stage": unit.Stage, "started_at": unit.StartedAt, "elapsed_ms": now.Sub(unit.StartedAt).Milliseconds()}
		if unit.JobID > 0 {
			view["job_id"] = unit.JobID
		}
		views = append(views, view)
	}
	return views
}

func verdictCounts(states map[string]int) map[string]int {
	counts := map[string]int{verdictRecommended: 0, verdictNotRecommended: 0, verdictPendingScore: 0, verdictPendingScreen: 0, verdictPendingDetail: 0, verdictUnfit: 0}
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
