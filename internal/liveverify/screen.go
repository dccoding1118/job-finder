package liveverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// processJob sends one job through ProcessJobNow and waits for the result:
// done, skipped, or stuck when the job stayed waiting after a failed call.
func (v *verifier) processJob(ctx context.Context, job, mark int64) (string, error) {
	status, code, _, err := v.api.post(ctx, fmt.Sprintf("/jobs/%d/process", job))
	if err != nil {
		return "", failf("processing job %d: %v", job, err)
	}
	switch {
	case status == http.StatusAccepted:
	case status == http.StatusConflict && code == "not_waiting":
		return "skipped", nil
	case status == http.StatusConflict && code == "profile_not_ready":
		return "", blockedf("the Profile is not ready, so no job can be processed")
	default:
		return "", failf("processing job %d answered HTTP %d %s", job, status, code)
	}
	started := time.Now()
	idle := 0
	for {
		if err := pause(ctx, pollInterval); err != nil {
			return "", err
		}
		current, err := v.api.job(ctx, job)
		if err != nil {
			return "", failf("%v", err)
		}
		progress, err := v.api.status(ctx)
		if err != nil {
			return "", failf("%v", err)
		}
		switch {
		case inFlight(progress, job):
			idle = 0
		case current.ProcessState != "new" && current.ProcessState != "queued":
			return "done", nil
		case len(failedCalls(progress, job, mark)) > 0:
			idle++
			if idle >= 3 {
				return "stuck", nil
			}
		}
		if time.Since(started) > stageTimeout {
			return "", failf("job %d was still %s after %s", job, current.ProcessState, stageTimeout)
		}
	}
}

func inFlight(status apiStatus, job int64) bool {
	for _, unit := range status.InFlight {
		if unit.JobID != nil && *unit.JobID == job {
			return true
		}
	}
	return false
}

func callsFor(status apiStatus, job, mark int64) []apiCall {
	var calls []apiCall
	for _, call := range status.AgentCalls {
		if call.ID > mark && call.JobID != nil && *call.JobID == job {
			calls = append(calls, call)
		}
	}
	return calls
}

func failedCalls(status apiStatus, job, mark int64) []apiCall {
	var failed []apiCall
	for _, call := range callsFor(status, job, mark) {
		if !call.OK {
			failed = append(failed, call)
		}
	}
	return failed
}

func hasCall(calls []apiCall, role string, requireOK bool) bool {
	for _, call := range calls {
		if call.Role == role && (call.OK || !requireOK) {
			return true
		}
	}
	return false
}

func failureKinds(calls []apiCall) string {
	kinds := make([]string, 0, len(calls))
	for _, call := range calls {
		kinds = append(kinds, call.Role+":"+call.FailureKind)
	}
	return strings.Join(kinds, ",")
}

type candidate struct {
	state string
	job   int64
}

// candidates lists the jobs screening may use, in order: a job that passed
// screening before is the likeliest to pass again, so scoring and the letter can
// land on the same job.
func (v *verifier) candidates(ctx context.Context) ([]candidate, error) {
	var list []candidate
	for _, state := range []string{"shortlisted", "scored", "new"} {
		ids, err := v.api.jobIDs(ctx, state, 100)
		if err != nil {
			return nil, failf("%v", err)
		}
		for _, id := range ids {
			list = append(list, candidate{state: state, job: id})
		}
	}
	return list, nil
}

func (v *verifier) screenAndScore(ctx context.Context) error {
	candidates, listErr := v.candidates(ctx)
	if listErr != nil {
		return listErr
	}
	skips, agentJobs, reason := 0, 0, ""
	for _, next := range candidates {
		job := next.job
		if next.state != "new" {
			status, _, body, postErr := v.api.post(ctx, fmt.Sprintf("/jobs/%d/reprocess", job))
			if postErr != nil {
				return failf("reprocessing job %d: %v", job, postErr)
			}
			if status == http.StatusConflict {
				continue
			}
			var reprocessed struct {
				Status string `json:"status"`
			}
			if status != http.StatusOK || json.Unmarshal(body, &reprocessed) != nil {
				return failf("reprocessing job %d answered HTTP %d", job, status)
			}
			if reprocessed.Status != "new" {
				continue
			}
		}
		before, statusErr := v.api.status(ctx)
		if statusErr != nil {
			return failf("%v", statusErr)
		}
		mark := latestCallID(before)
		outcome, err := v.processJob(ctx, job, mark)
		if err != nil {
			return err
		}
		if outcome == "skipped" {
			continue
		}
		after, err := v.api.status(ctx)
		if err != nil {
			return failf("%v", err)
		}
		calls := callsFor(after, job, mark)
		if outcome == "stuck" {
			failed := failedCalls(after, job, mark)
			if strings.Contains(failureKinds(failed), "runner_error") {
				v.screened[job] = true
				reason = fmt.Sprintf("job %d stayed waiting because the Agent CLI itself failed (%s)", job, failureKinds(failed))
				break
			}
			return failf("job %d stayed waiting after an Agent response failed validation (%s)", job, failureKinds(failed))
		}
		if !hasCall(calls, "filter", false) {
			skips++
			if skips > maxStructuralSkips {
				reason = fmt.Sprintf("%d candidates were rejected by structural rules before any Agent call", maxStructuralSkips)
				break
			}
			continue
		}
		v.screened[job] = true
		agentJobs++
		snapshot, err := v.snapshotOf()
		if err != nil {
			return err
		}
		summary, err := judgeScreened(snapshot, job)
		if err != nil {
			return failf("job %d screening result violates the contract: %v", job, err)
		}
		v.note("- 篩選：" + summary)
		final, err := findJob(snapshot, job)
		if err != nil {
			return failf("%v", err)
		}
		if final.ProcessState == "scored" || final.ProcessState == "shortlisted" {
			if !hasCall(calls, "scorer", true) {
				return failf("job %d reached %s without a successful scorer call", job, final.ProcessState)
			}
			v.scoredJob = job
			break
		}
		if agentJobs == 2 {
			reason = "neither of the two jobs that reached the Filter Agent passed screening, so scoring was not exercised"
			break
		}
	}
	switch {
	case agentJobs == 0:
		if reason == "" {
			reason = "no candidate job reached the Filter Agent"
		}
		v.softBlock("screening and scoring were not exercised: " + reason)
	case v.scoredJob == 0:
		if reason == "" {
			reason = "no screened job reached scoring"
		}
		v.softBlock("scoring was not exercised: " + reason)
	}
	summary, err := v.attribute(ctx)
	if err != nil {
		return err
	}
	v.note("- 本趟 Agent 呼叫歸屬：" + summary)
	return nil
}

// attribute fails the run when any call made since the baseline landed on a job
// this run did not choose. The status view lists only the most recent calls, so
// a run that made more than it shows is itself over its budget.
func (v *verifier) attribute(ctx context.Context) (string, error) {
	status, err := v.api.status(ctx)
	if err != nil {
		return "", failf("%v", err)
	}
	return attributeCalls(status.AgentCalls, v.baseline, map[string]map[int64]bool{
		"filter":   v.screened,
		"scorer":   {v.scoredJob: v.scoredJob != 0},
		"drafter":  v.letterJobs,
		"reviewer": v.letterJobs,
	})
}

func attributeCalls(calls []apiCall, baseline int64, allowed map[string]map[int64]bool) (string, error) {
	if len(calls) >= statusCallWindow {
		oldest := calls[0].ID
		for _, call := range calls {
			if call.ID < oldest {
				oldest = call.ID
			}
		}
		if oldest > baseline+1 {
			return "", failf("more Agent calls were made since the run began than the status view lists")
		}
	}
	var foreign []string
	jobsByRole := map[string]map[int64]bool{}
	count := 0
	for _, call := range calls {
		if call.ID <= baseline {
			continue
		}
		count++
		var job int64
		if call.JobID != nil {
			job = *call.JobID
		}
		if call.JobID == nil || !allowed[call.Role][job] {
			foreign = append(foreign, fmt.Sprintf("%s@job %d", call.Role, job))
			continue
		}
		if jobsByRole[call.Role] == nil {
			jobsByRole[call.Role] = map[int64]bool{}
		}
		jobsByRole[call.Role][job] = true
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return "", failf("Agent calls landed outside the chosen jobs: %s", strings.Join(foreign, ", "))
	}
	return fmt.Sprintf("filter_jobs=%d scorer_jobs=%d calls=%d", len(jobsByRole["filter"]), len(jobsByRole["scorer"]), count), nil
}
