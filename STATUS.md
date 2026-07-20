# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-21。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- e2e 驗證運作模式（題目卷↔答案卷）規則已蒸餾進 `_inbox` 捕捉 `20260720-1000-general-verify-doc-atomic-literal-oracle-format`（topic `verify-doc-exam-answersheet-model`）；**待 curate 進 `project-docs-workflow` skill**（目前該 skill 對 verify 只給「看 agent-manager 當範例」，本質未落文字）。job-finder 端的 `docs/verify.md`＋`run-mock.sh` 已依此模式落地。

- 檔案配置維持 XDG 三分（設定 `~/.config/jobfinder/`、資料 `~/.local/share/jobfinder/`、binary `~/.local/lib/jobfinder/`），不改為單一 `~/.job-finder/`；`docs/deploy.md` §2 已載明。「單一入口好找」已由 `scripts/deploy/install.sh` 收尾時印出三個位置解決，不新增 `jobfinder paths` 子命令。

## §2 未完成任務

開發順序：Wave 1（mock 綠燈的 B0–B5 主體）已上版並合併進 `main`；Wave 2 做依賴真實環境的部署與 live 驗收。B6（Cake、反向校準）暫不開發。

**Wave 2 — 部署與 live 驗收**

執行順序為 V3 live → `scripts/deploy/` 與 B4 部署驗收 → Chrome gate；先跑 live 以便真來源與真 Agent 契約的產品問題早於部署工作暴露。V3 live 已於本機（真 Yourator、已授權 claude/codex CLI）13/13 PASS 並上版。

- [ ] **（已實作、待 `/ship` 上版）** `scripts/deploy/`（`install.sh`／`update.sh`／`rollback.sh`＋`lib.sh`，另有 `mise run deploy-*`）已建立，與 verify harness 分離、不由 `e2e-*` 呼叫；驗證打在生效面（`/proc/<pid>/exe`＋啟動時間）。B4 正式部署驗收已於本機通過：install 完整安裝並 enable、API 生效且僅 loopback、token 驗證正確、run one-shot 實際抓回 148 筆真 Yourator 職缺並經常駐 worker 篩選（144 unfit）／評分（4 → not_recommended，僅 4 次真 LLM 呼叫）；update 的 unit drift 告警＋`try-restart` 生效面驗證、rollback 還原前一版皆通過。**任務待上版後刪除**。
- [ ] crawler 對 Yourator 的 HTTP 請求無 per-request timeout（`http.DefaultClient` fallback），真來源若真的 stall 會無限 hang。目前以 `jobfinder-run.service` 的 `TimeoutStartSec=1800` 作部署層 backstop；產品層應補 crawler HTTP client timeout（`internal/crawler/yourator.go`／`cmd/jobfinder/cli/runtime.go`），尚未實作。
- [ ] 完成日常 Chrome compatibility gate 與同一 artifact 的 evidence 附加機制；自動隔離 Chromium 不得視為實際 Chrome 驗收（詳見 `docs/verify.md` §4 V4、§9）。本機無 Chrome 且 `DISPLAY=none`，實際載入須由使用者在桌機執行。
