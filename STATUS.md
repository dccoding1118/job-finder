# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-20。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- e2e 驗證運作模式（題目卷↔答案卷）規則已蒸餾進 `_inbox` 捕捉 `20260720-1000-general-verify-doc-atomic-literal-oracle-format`（topic `verify-doc-exam-answersheet-model`）；**待 curate 進 `project-docs-workflow` skill**（目前該 skill 對 verify 只給「看 agent-manager 當範例」，本質未落文字）。job-finder 端的 `docs/verify.md`＋`run-mock.sh` 已依此模式落地。

## §2 未完成任務

開發順序：Wave 1（mock 綠燈的 B0–B5 主體 + 首個 PR）已完成上版；Wave 2 做依賴真實環境的部署與 live 驗收。B6（Cake、反向校準）暫不開發。

**Wave 1 — 完成 B0–B5 主體並首次上版**：已於 PR #1（`feat/initial-release-b0-b5`）整個工作樹首次上版，待使用者審核。審核不過時於同一 PR 續修，屆時列新任務。

**Wave 2 — 部署與 live 驗收（Wave 1 上版後）**

- [ ] 建立 `scripts/deploy/` 的明確安裝、更新與回滾入口；不得由 `e2e-*` 任務呼叫。完成後執行 B4 正式部署驗收。
- [ ] 跑通 V3 deployed live 並保存安全 evidence；正式來源零筆或資料格式錯誤為 FAIL，來源／CLI 未授權或不可達才是 `ENVIRONMENT_BLOCKED`（詳見 `docs/verify.md` §6、§8）。
- [ ] 完成日常 Chrome compatibility gate 與同一 artifact 的 evidence 附加機制；自動隔離 Chromium 不得視為實際 Chrome 驗收（詳見 `docs/verify.md` §4 V4、§9）。
