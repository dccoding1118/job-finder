package liveverify

import (
	"context"
	"fmt"
	"os"
)

func (v *verifier) arrange(ctx context.Context) error {
	running, err := v.platform.apiRunning(ctx)
	if err != nil {
		return blockedf("read the API service state: %v", err)
	}
	v.record.APIRunning = &running
	if err = v.saveRecord(); err != nil {
		return err
	}
	if !running {
		if err = v.platform.startAPI(ctx); err != nil {
			return failf("the API service could not be started: %v", err)
		}
	}
	if !v.api.waitResponding(ctx) {
		return failf("the API at %s did not respond", v.cfg.APIAddr)
	}
	if running {
		v.note("- API 服務：原本執行中")
	} else {
		v.note("- API 服務：原本未執行，已啟動")
	}

	settings, err := v.api.settings(ctx)
	if err != nil {
		return failf("%v", err)
	}
	if !settings.ResidentWorker {
		if err = v.enableResidentWorker(ctx); err != nil {
			return err
		}
		v.note("- worker.paused：原本 true，已備份 config.yaml 並改為 false、重啟 API 服務")
	} else {
		v.note("- worker.paused：原本 false，不安排")
	}

	auto := settings.AutoProcessing
	v.record.AutoProcessing = &auto
	if err = v.saveRecord(); err != nil {
		return err
	}
	if err = v.api.setAutoProcessing(ctx, false); err != nil {
		return failf("the automatic processing switch could not be turned off: %v", err)
	}
	if current, settingsErr := v.api.settings(ctx); settingsErr != nil || current.AutoProcessing {
		return failf("the automatic processing switch did not stay off")
	}
	v.note(fmt.Sprintf("- 自動處理開關：原本 %t，已關閉", auto))

	disabled, err := v.platform.fetchDisabled(ctx)
	if err != nil {
		return blockedf("read the fetch task state: %v", err)
	}
	if disabled {
		yes := true
		v.record.FetchDisabled = &yes
		if err = v.saveRecord(); err != nil {
			return err
		}
		if err = v.platform.enableFetch(ctx); err != nil {
			return failf("the fetch task could not be enabled: %v", err)
		}
		if still, _ := v.platform.fetchDisabled(ctx); still {
			return failf("the fetch task stayed disabled")
		}
		v.note("- 每日抓取工作：原本停用，已啟用")
	} else {
		v.note("- 每日抓取工作：可手動觸發，不安排")
	}

	// Work the worker had already started finishes before the baseline is taken,
	// so its calls are not mistaken for this run's.
	var status apiStatus
	idle := waitUntil(ctx, stageTimeout, pollInterval, func() bool {
		status, err = v.api.status(ctx)
		return err == nil && len(status.InFlight) == 0
	})
	if !idle {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return blockedf("the resident worker is still busy with earlier work")
	}
	v.baseline = latestCallID(status)
	requested, err := v.api.jobIDs(ctx, "letter_requested", 100)
	if err != nil {
		return failf("%v", err)
	}
	for _, id := range requested {
		v.letterJobs[id] = true
	}
	if pending, ok := v.loadPending(); ok {
		v.letterJobs[pending.JobID] = true
	}
	v.note(fmt.Sprintf("- 本趟基準：Agent 呼叫 id %d 之後；基準當下已在 letter_requested 的職缺 %d 筆", v.baseline, len(requested)))
	return nil
}

func (v *verifier) enableResidentWorker(ctx context.Context) error {
	backup := v.cfg.ConfigPath + ".verify-live.bak"
	sum, err := fileSHA256(v.cfg.ConfigPath)
	if err != nil {
		return failf("read the installed config: %v", err)
	}
	if err = copyFile(v.cfg.ConfigPath, backup); err != nil {
		return failf("back up the installed config: %v", err)
	}
	v.record.ConfigBackup, v.record.ConfigSHA256 = backup, sum
	if err = v.saveRecord(); err != nil {
		return err
	}
	original, err := os.ReadFile(backup) // #nosec G304 -- backup path derives from the installed config path.
	if err != nil {
		return failf("read the config backup: %v", err)
	}
	rewritten, found := rewritePaused(original)
	if !found {
		return failf("the service runs without the resident worker, yet config.yaml carries no worker.paused")
	}
	if err = os.WriteFile(v.cfg.ConfigPath, rewritten, 0o600); err != nil { // #nosec G703 -- the installed config path from the resolved layout.
		return failf("write the installed config: %v", err)
	}
	if err = v.platform.restartAPI(ctx); err != nil {
		return failf("the API service could not be restarted: %v", err)
	}
	if !v.api.waitResponding(ctx) {
		return failf("the API at %s did not respond after the restart", v.cfg.APIAddr)
	}
	settings, err := v.api.settings(ctx)
	if err != nil || !settings.ResidentWorker {
		return failf("the API service still runs without the resident worker after worker.paused was set to false")
	}
	return nil
}

func latestCallID(status apiStatus) int64 {
	var latest int64
	for _, call := range status.AgentCalls {
		if call.ID > latest {
			latest = call.ID
		}
	}
	return latest
}
