package liveverify

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var unreachable = regexp.MustCompile(`(?i)timeout|timed out|connection refused|could not resolve|no such host|network is unreachable|tls|certificate|proxy|temporary failure|robots`)

// fetchOnce triggers the installed fetch the way the schedule does and returns
// the run row it wrote once that run has finished.
func (v *verifier) fetchOnce(ctx context.Context) (apiRun, error) {
	var before int64
	latest, err := v.api.runs(ctx, 1)
	if err != nil {
		return apiRun{}, failf("%v", err)
	}
	if len(latest) > 0 {
		before = latest[0].ID
	}
	if err := v.platform.triggerFetch(ctx); err != nil {
		return apiRun{}, failf("the installed fetch could not be started: %v", err)
	}
	started := time.Now()
	for {
		runs, err := v.api.runs(ctx, 5)
		if err != nil {
			return apiRun{}, failf("%v", err)
		}
		var found *apiRun
		for i := range runs {
			if runs[i].ID > before && (found == nil || runs[i].ID < found.ID) {
				found = &runs[i]
			}
		}
		running, _ := v.platform.fetchRunning(ctx)
		if found != nil && found.FinishedAt != nil && !running {
			return *found, nil
		}
		if found == nil && !running && time.Since(started) > time.Minute {
			return apiRun{}, failf("starting the installed fetch wrote no run")
		}
		if time.Since(started) > fetchTimeout {
			return apiRun{}, failf("the scheduled fetch did not finish within %s", fetchTimeout)
		}
		if err := pause(ctx, pollInterval); err != nil {
			return apiRun{}, err
		}
	}
}

func checkFetchRun(run apiRun) error {
	if run.Trigger != "timer" {
		return failf("the scheduled fetch recorded trigger %s, want timer", run.Trigger)
	}
	errorsCount := run.Stats["errors"]
	if run.State != "done" || errorsCount != 0 {
		if run.Error != nil && unreachable.MatchString(*run.Error) {
			return blockedf("the fetch could not reach Yourator (state %s, errors %d)", run.State, errorsCount)
		}
		return failf("the scheduled fetch ended in state %s with %d errors", run.State, errorsCount)
	}
	if run.Stats["fetched"] < 1 {
		return failf("the scheduled fetch returned zero jobs from Yourator")
	}
	return nil
}

func (v *verifier) fetch(ctx context.Context) error {
	run, err := v.fetchOnce(ctx)
	if err != nil {
		return err
	}
	if err = checkFetchRun(run); err != nil {
		return err
	}
	v.note(fmt.Sprintf("- 經已安裝排程觸發：trigger=timer、state=done、fetched=%d、new=%d、errors=0", run.Stats["fetched"], run.Stats["new"]))
	snapshot, err := v.snapshotOf()
	if err != nil {
		return err
	}
	summary, err := judgeSource(snapshot)
	if err != nil {
		return failf("Yourator data violates the normalization contract: %v", err)
	}
	v.note("- 安全摘要：" + summary)
	return nil
}

func (v *verifier) idempotentFetch(ctx context.Context) error {
	snapshot, err := v.snapshotOf()
	if err != nil {
		return err
	}
	beforeCount, beforeSum := fingerprint(snapshot)
	run, err := v.fetchOnce(ctx)
	if err != nil {
		return err
	}
	if err = checkFetchRun(run); err != nil {
		return err
	}
	if run.Stats["new"] != 0 {
		return failf("the repeated fetch inserted %d new jobs", run.Stats["new"])
	}
	snapshot, err = v.snapshotOf()
	if err != nil {
		return err
	}
	afterCount, afterSum := fingerprint(snapshot)
	if beforeCount != afterCount || beforeSum != afterSum {
		return failf("the repeated fetch changed stored content hashes or processing states (%d:%s → %d:%s)", beforeCount, beforeSum, afterCount, afterSum)
	}
	v.note(fmt.Sprintf("- 第二次抓取 new=0；%d 筆職缺的 content_hash 與 process_state 逐筆未變", beforeCount))
	return nil
}

func pause(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
