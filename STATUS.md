# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-21。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- e2e 驗證運作模式（題目卷↔答案卷）規則已蒸餾進 `_inbox` 捕捉 `20260720-1000-general-verify-doc-atomic-literal-oracle-format`（topic `verify-doc-exam-answersheet-model`）；**待 curate 進 `project-docs-workflow` skill**（目前該 skill 對 verify 只給「看 agent-manager 當範例」，本質未落文字）。job-finder 端的 `docs/verify.md`＋`run-mock.sh` 已依此模式落地。

- 檔案配置維持 XDG 三分（設定 `~/.config/jobfinder/`、資料 `~/.local/share/jobfinder/`、binary `~/.local/lib/jobfinder/`），不改為單一 `~/.job-finder/`；`docs/deploy.md` §2 已載明。「單一入口好找」已由 `scripts/deploy/install.sh` 收尾時印出三個位置解決，不新增 `jobfinder paths` 子命令。

## §2 未完成任務

開發順序：Wave 1（mock 綠燈的 B0–B5 主體）已上版並合併進 `main`；Wave 2 做依賴真實環境的部署與 live 驗收。B6（Cake、反向校準）暫不開發。

**Wave 2 — 部署與 live 驗收**

執行順序為 V3 live → `scripts/deploy/` 與 B4 部署驗收 → Chrome gate；先跑 live 以便真來源與真 Agent 契約的產品問題早於部署工作暴露。V3 live 已於本機（真 Yourator、已授權 claude/codex CLI）13/13 PASS 並上版；`scripts/deploy/` 三入口與 B4 部署驗收已於本機通過（fetch 148 → filter 144 unfit → score 4），PR #3 已合併進 `main`。crawler per-request timeout（`sources.yourator.request_timeout`，預設 30s）已補上，剩 Chrome gate 需人工在桌機執行。

- [ ] 完成日常 Chrome compatibility gate 與同一 artifact 的 evidence 附加機制；自動隔離 Chromium 不得視為實際 Chrome 驗收（詳見 `docs/verify.md` §4 V4、§9）。本機無 Chrome 且 `DISPLAY=none`，實際載入須由使用者在桌機執行。人工操作手冊見 `docs/runbook-extension.md`（Windows Chrome ↔ GCP VM API 走 SSH loopback forward）。

**Roadmap — 部署標準化（暫不實作）**

- [ ] deploy 整合 GitHub release：建立「正式版工件」路徑——由 CI/release 產出帶版號與 checksum 的正式工件（binary），正式環境只從該工件部署，不再從開發 checkout 直接 build+install（目前 `scripts/deploy/install.sh` 走 preflight→build→install 的開發目錄直裝路徑）。一併規劃 Chrome extension 隨 release 的上版流程如何整合（打包、版號對齊、`extension_origin` 更新）。目標：收斂「開發目錄→正式環境」的直接安裝路徑。
- [ ] 檢視並訂定檔案部署標準：目前 runtime 檔案／配置走 XDG 三分而散落三處（設定 `~/.config/jobfinder/`、資料 `~/.local/share/jobfinder/`、binary `~/.local/lib/jobfinder/`）；對照 agent-manager、gogents 改用單一 `~/.xxx/` 專用目錄。釐清兩種做法的規劃取捨（XDG 慣例 vs. 單一目錄好搬移／好備份／好清除），選定一套可跨專案共用的部署標準。**此項會重開 §1 已定案的「維持 XDG 三分、不改單一目錄」結論（現已載於 `docs/deploy.md` §2），若改標準需同步更新該文件與 install 腳本。**
