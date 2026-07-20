# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-21。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- e2e 驗證運作模式（題目卷↔答案卷）規則已蒸餾進 `_inbox` 捕捉 `20260720-1000-general-verify-doc-atomic-literal-oracle-format`（topic `verify-doc-exam-answersheet-model`）；**待 curate 進 `project-docs-workflow` skill**（目前該 skill 對 verify 只給「看 agent-manager 當範例」，本質未落文字）。job-finder 端的 `docs/verify.md`＋`run-mock.sh` 已依此模式落地。

- 檔案配置維持 XDG 三分（設定 `~/.config/jobfinder/`、資料 `~/.local/share/jobfinder/`、binary `~/.local/lib/jobfinder/`），不改為單一 `~/.job-finder/`；`docs/deploy.md` §2 已載明。若要補「單一入口好找」，改以 `jobfinder paths` 子命令印出三個位置，於 W2-2 部署腳本階段再定，尚無 docs 落點。

## §2 未完成任務

開發順序：Wave 1（mock 綠燈的 B0–B5 主體）已上版並合併進 `main`；Wave 2 做依賴真實環境的部署與 live 驗收。B6（Cake、反向校準）暫不開發。

**Wave 2 — 部署與 live 驗收**

執行順序為 V3 live → `scripts/deploy/` 與 B4 部署驗收 → Chrome gate；先跑 live 以便真來源與真 Agent 契約的產品問題早於部署工作暴露。V3 live 已於本機（真 Yourator、已授權 claude/codex CLI）13/13 PASS 並上版。

- [ ] 建立 `scripts/deploy/` 的明確安裝、更新與回滾入口；不得由 `e2e-*` 任務呼叫。完成後執行 B4 正式部署驗收。實際安裝已獲授權可在本機執行。
- [ ] 完成日常 Chrome compatibility gate 與同一 artifact 的 evidence 附加機制；自動隔離 Chromium 不得視為實際 Chrome 驗收（詳見 `docs/verify.md` §4 V4、§9）。本機無 Chrome 且 `DISPLAY=none`，實際載入須由使用者在桌機執行。
