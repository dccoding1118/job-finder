package liveverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (v *verifier) definitions(ctx context.Context) error {
	summary, err := v.platform.checkDefinitions(ctx, v.layout)
	if err != nil {
		return failf("%v", err)
	}
	v.note("- " + summary)
	return nil
}

func (v *verifier) runningAPI(ctx context.Context) error {
	for _, path := range []string{"/jobs", "/runs"} {
		var page struct {
			Items *[]json.RawMessage `json:"items"`
		}
		if err := v.api.getJSON(ctx, path, &page); err != nil {
			return failf("%v", err)
		}
		if page.Items == nil {
			return failf("GET %s carries no items", path)
		}
		status, _, err := v.api.do(ctx, http.MethodGet, path, nil, false)
		if err != nil {
			return failf("GET %s without a token: %v", path, err)
		}
		if status != http.StatusUnauthorized {
			return failf("GET %s without a token answered HTTP %d, want 401", path, status)
		}
	}
	executable, err := v.platform.apiExecutable(ctx, v.layout)
	if err != nil {
		return failf("locate the running API process: %v", err)
	}
	if !sameFile(v.opts.GOOS, executable, v.layout.ServiceBinary) {
		return failf("the running API process executes %s, want %s", executable, v.layout.ServiceBinary)
	}
	v.note(fmt.Sprintf("- 帶 token 讀 /jobs、/runs 回 200 且含 items；未帶 token 回 401；執行中 API process 的執行檔是安裝放置的 %s", filepath.Base(v.layout.ServiceBinary)))
	return nil
}

func sameFile(goos, a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return samePath(goos, a, b)
}

func (v *verifier) restoreStep(ctx context.Context) error {
	v.pendingUnrestored = v.restore(ctx)
	if len(v.pendingUnrestored) > 0 {
		v.note("- 狀態：復原未完成")
		return nil
	}
	v.note("- 各項設定已依原值復原，復原紀錄已刪除")
	return nil
}

var reportLeak = regexp.MustCompile(`\[你的姓名\]|\[你的聯絡方式\]|"description"\s*:|"content"\s*:`)

func (v *verifier) evidence(context.Context) error {
	data, err := os.ReadFile(v.report)
	if err != nil {
		return failf("read the report: %v", err)
	}
	if strings.Contains(string(data), v.cfg.Token) {
		return failf("the report contains the API token")
	}
	if reportLeak.Match(data) {
		return failf("the report contains letter placeholders, letter content or JD text")
	}
	v.note("- 報告只含筆數、hash、狀態與布林結果")
	return nil
}
