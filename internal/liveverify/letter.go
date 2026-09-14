package liveverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type letterPending struct {
	JobID       int64  `json:"job_id"`
	RequestedAt string `json:"requested_at"`
	AttemptMark int64  `json:"attempt_mark"`
}

func (v *verifier) pendingPath() string { return filepath.Join(v.dir, "letter-pending.json") }

func (v *verifier) loadPending() (letterPending, bool) {
	data, err := os.ReadFile(v.pendingPath())
	if err != nil {
		return letterPending{}, false
	}
	var pending letterPending
	if json.Unmarshal(data, &pending) != nil || pending.JobID <= 0 {
		return letterPending{}, false
	}
	return pending, true
}

func (v *verifier) letter(ctx context.Context) error {
	if _, err := os.Stat(v.pendingPath()); err == nil {
		if err := v.judgePendingLetter(ctx); err != nil {
			return err
		}
	} else if err := v.requestLetter(ctx); err != nil {
		return err
	}
	summary, err := v.attribute(ctx)
	if err != nil {
		return err
	}
	v.note("- 本趟 Agent 呼叫歸屬：" + summary)
	return nil
}

func (v *verifier) requestLetter(ctx context.Context) error {
	var job int64
	if v.scoredJob != 0 {
		if current, err := v.api.job(ctx, v.scoredJob); err == nil && current.ProcessState == "shortlisted" {
			job = v.scoredJob
		}
	}
	for _, state := range []string{"shortlisted", "letter_failed", "letter_ready"} {
		if job != 0 {
			break
		}
		ids, err := v.api.jobIDs(ctx, state, 1)
		if err != nil {
			return failf("%v", err)
		}
		if len(ids) > 0 {
			job = ids[0]
		}
	}
	if job == 0 {
		v.softBlock("no shortlisted, letter_failed or letter_ready job to request a letter for")
		return nil
	}
	v.letterJobs[job] = true
	attempts, err := v.api.attempts(ctx, job)
	if err != nil {
		return failf("%v", err)
	}
	var mark int64
	for _, attempt := range attempts {
		if attempt.ID > mark {
			mark = attempt.ID
		}
	}
	requestedAt := time.Now()
	status, code, body, err := v.api.post(ctx, fmt.Sprintf("/jobs/%d/letter", job))
	if err != nil {
		return failf("requesting a letter for job %d: %v", job, err)
	}
	var accepted struct {
		Status string `json:"status"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &accepted) != nil || accepted.Status != "requested" {
		return failf("requesting a letter for job %d answered HTTP %d %s", job, status, code)
	}
	v.note(fmt.Sprintf("- 對 job %d 要求求職信", job))
	outcome, err := v.waitLetter(ctx, job, mark, requestedAt)
	if err != nil {
		return err
	}
	if outcome == "pending" {
		pending := letterPending{JobID: job, RequestedAt: requestedAt.In(taipei).Format(time.RFC3339), AttemptMark: mark}
		data, _ := json.MarshalIndent(pending, "", "  ")
		if err = os.WriteFile(v.pendingPath(), append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write letter-pending.json: %w", err)
		}
		v.softBlock(fmt.Sprintf("求職信當日額度不足，未判定：job %d 仍停在 letter_requested，已寫入 letter-pending.json，隔日以 --recheck-letter 補測", job))
		return nil
	}
	_, err = v.judgeLetter(ctx, job, mark)
	return err
}

// waitLetter polls a requested letter until the worker finishes it, or until
// the window in which the worker would have picked it up has passed unused: the
// request is the only sign an exhausted daily allowance leaves.
func (v *verifier) waitLetter(ctx context.Context, job, mark int64, requestedAt time.Time) (string, error) {
	window := 3*v.cfg.ScanInterval + v.cfg.MinInterval
	for {
		current, err := v.api.job(ctx, job)
		if err != nil {
			return "", failf("%v", err)
		}
		if current.ProcessState != "letter_requested" {
			return "done", nil
		}
		status, err := v.api.status(ctx)
		if err != nil {
			return "", failf("%v", err)
		}
		attempts, err := v.api.attempts(ctx, job)
		if err != nil {
			return "", failf("%v", err)
		}
		started := false
		for _, attempt := range attempts {
			started = started || attempt.ID > mark
		}
		letterInFlight := false
		for _, unit := range status.InFlight {
			letterInFlight = letterInFlight || unit.Stage == "letter"
		}
		if !letterInFlight && !started && time.Since(requestedAt) >= window {
			return "pending", nil
		}
		if time.Since(requestedAt) > letterTimeout {
			return "", failf("job %d letter generation did not finish within %s", job, letterTimeout)
		}
		if err := pause(ctx, pollInterval); err != nil {
			return "", err
		}
	}
}

// judgeLetter decides the generation started after mark on job. It reports
// whether the generation was judged; one that cannot be is soft-blocked.
func (v *verifier) judgeLetter(ctx context.Context, job, mark int64) (bool, error) {
	attempts, err := v.api.attempts(ctx, job)
	if err != nil {
		return false, failf("%v", err)
	}
	var attempt *apiAttempt
	for i := range attempts {
		if attempts[i].ID > mark && (attempt == nil || attempts[i].ID > attempt.ID) {
			attempt = &attempts[i]
		}
	}
	if attempt == nil {
		v.softBlock(fmt.Sprintf("job %d left letter_requested without a generation recorded after the request", job))
		return false, nil
	}
	drafted, reviewed := false, false
	for _, call := range attempt.Calls {
		drafted = drafted || (call.Role == "drafter" && call.OK)
		reviewed = reviewed || (call.Role == "reviewer" && call.OK)
	}
	switch attempt.Status {
	case "approved", "finalized", "failed":
	default:
		return false, failf("job %d letter generation ended in status %s", job, attempt.Status)
	}
	if attempt.Status == "failed" && !drafted {
		status, statusErr := v.api.status(ctx)
		if statusErr != nil {
			return false, failf("%v", statusErr)
		}
		var drafterFailures []apiCall
		for _, call := range failedCalls(status, job, 0) {
			if call.Role == "drafter" {
				drafterFailures = append(drafterFailures, call)
			}
		}
		kinds := failureKinds(drafterFailures)
		if strings.Contains(kinds, "runner_error") {
			v.softBlock(fmt.Sprintf("job %d letter generation failed on the drafter CLI itself (runner_error: quota, authentication or timeout)", job))
			return false, nil
		}
		return false, failf("job %d letter generation failed without a single valid draft (%s)", job, kinds)
	}
	if !drafted {
		return false, failf("job %d letter generation %s has no successful drafter call", job, attempt.Status)
	}
	if attempt.Status == "approved" && !reviewed {
		return false, failf("job %d letter was approved without a successful reviewer call", job)
	}
	snapshot, err := v.snapshotOf()
	if err != nil {
		return false, err
	}
	summary, err := judgeLettered(snapshot, job, attempt.Status)
	if err != nil {
		return false, failf("job %d letter violates the contract: %v", job, err)
	}
	v.note("- 求職信：" + summary)
	return true, nil
}

// judgePendingLetter judges the job letter-pending.json points at. The record
// stays while the request still waits and goes once judged.
func (v *verifier) judgePendingLetter(ctx context.Context) error {
	pending, ok := v.loadPending()
	if !ok {
		return failf("letter-pending.json is malformed")
	}
	v.letterJobs[pending.JobID] = true
	v.note(fmt.Sprintf("- 判定先前未產製的求職信要求：job %d（要求時間 %s）", pending.JobID, pending.RequestedAt))
	current, err := v.api.job(ctx, pending.JobID)
	if err != nil {
		return failf("%v", err)
	}
	if current.ProcessState == "letter_requested" {
		outcome, waitErr := v.waitLetter(ctx, pending.JobID, pending.AttemptMark, time.Now())
		if waitErr != nil {
			return waitErr
		}
		if outcome == "pending" {
			v.softBlock(fmt.Sprintf("job %d is still letter_requested; the day's letter allowance has not been renewed yet", pending.JobID))
			return nil
		}
	}
	judged, err := v.judgeLetter(ctx, pending.JobID, pending.AttemptMark)
	if err != nil {
		return err
	}
	if judged {
		_ = os.Remove(v.pendingPath())
	}
	return nil
}
