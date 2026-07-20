# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-20。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- （無）B0–B5 核心程式完成、非同步 worker＋按需生成已落地、harness 待改寫等現況已寫入 `AGENTS.md` §1 與 `docs/verify.md` §4；第一版（B0–B5）於 §2 Wave 1 收尾完成後才首次上版。
- e2e 驗證運作模式（題目卷↔答案卷）規則已蒸餾進 `_inbox` 捕捉 `20260720-1000-general-verify-doc-atomic-literal-oracle-format`（topic `verify-doc-exam-answersheet-model`）；**待 curate 進 `project-docs-workflow` skill**（目前該 skill 對 verify 只給「看 agent-manager 當範例」，本質未落文字）。job-finder 端的 `docs/verify.md`＋`run-mock.sh` 已依此模式落地（見 §2）。

## §2 未完成任務

開發順序：先完成 mock 綠燈的 B0–B5 主體並開首個 PR（Wave 1），再做依賴真實環境的部署與 live 驗收（Wave 2）。B6（Cake、反向校準）暫不開發。

**Wave 1 — 完成 B0–B5 主體並首次上版**

- [x] 修正文件不同步：`AGENTS.md` §1 對齊「非同步 worker＋按需生成已落地、harness 待改寫」；`docs/verify.md` V2–V4 現況由 ✅ 改 ⏳ 並加註 harness 仍編碼同步模型。
- [x] 產出 104 合成 fixture（搜尋頁／通知頁／內頁各一，結構仿真、內容合成）：`scripts/verify/browser/fixtures/104/{search,notification,job}.html`＋README，已對 `extension/content/list.js`／`job.js` selector 與 `internal/crawler` JSON-LD 解析驗證。欄位映射與 selector 見 `docs/designs/design-crawler.md` §2.1–§2.3／§5 與 `docs/designs/design-extension.md` §4.0。
- [x] 改寫 e2e 驗收 harness（`scripts/verify/` runbook、`assert-positive.mjs`、browser E2E）對齊非同步／按需模型：`run` 只 fetch＋縮減後 `runs.stats`（fetched/new/queries/errors）、手動 `--stage` 或常駐 worker 消化 filter/score/letter；求職信「未要求不生成 → `RequestLetter` 後生成」、轉換經 `letter_requested`；`schema_version=2`；新增 capture list/job 與 verdict／letter_state 斷言（V5）；harness cleanup 強制回收 fixture server。`mise run e2e-mock`（V1/V2/V4/V5，25 步）連跑兩次皆綠、確定性、埠自清。
- [x] verify script 整併：`run-mock.sh`／`run-live.sh` 共用的 report/preflight/cleanup/perm primitives 下沉 `lib.sh`（`record`／`pass_step`／`fail`／`environment_blocked`／`require_commands`／`stop_transient_units`／`assert_profile_perms`）；兩支 runbook 改用之，`shellcheck -x` clean、`fmt/lint/test` 全綠。
- [x] 修好 browser E2E flake：`extension-e2e.js` 對「手動抓取」「更新投遞狀態」「判定篩選」三處改斷言持久狀態（run history 出現 `manual-extension`／API 讀回 `apply_state`／輪詢 `#jobs` 收斂到單一職缺），不再取 dashboard 被 `load()` 覆寫的 transient `#status` 或 mid-render 取樣。根因：`dashboard.js` change handler 以未 await 的 `load()` 覆寫／重排渲染，測試 sample-once 撞到過渡態。`mise run e2e-mock` 連跑 25 次全綠。
- [ ] 【下個獨立 session 執行】`mise run fmt/lint/test` 全綠後，initial commit 並開首個 PR（整個工作樹首次上版，含上述 verify 整併與 flake 修復、B0–B4 live E2E 無外部成本改動，詳見 `docs/changes/change-e2e-live-b0-b4.md`）。
- [x] 依捕捉 `verify-doc-exam-answersheet-model` 重構 job-finder e2e 驗證（題目卷↔答案卷），隨上一項 Wave 1 首個 PR 上版：
  - `docs/verify.md` 改為題目卷：加 §1「怎麼跑起來」、§3「測資與標準答案＋使用者故事」、§4 25 步原子案例（S01–S25，字面 oracle＋案例·R＋狀態）、§5 N 骨架、§7「答案卷如何對答案」；標準答案機器真相源指向 `assert-positive.mjs`（不第三次重嵌）。
  - `run-mock.sh` 答案卷：步驟改 `S01–S25 (V·R)` 對齊 §4；收尾補 PASS/FAIL/SKIP tally、需求覆蓋（由實跑步驟推導）、使用者故事重建與對答案指引。`mise run e2e-mock` 連跑兩次皆綠、收尾區塊逐字相同（確定性）。

**Wave 2 — 部署與 live 驗收（Wave 1 上版後）**

- [ ] 建立 `scripts/deploy/` 的明確安裝、更新與回滾入口；不得由 `e2e-*` 任務呼叫。完成後執行 B4 正式部署驗收。
- [ ] 跑通 V3 deployed live 並保存安全 evidence；正式來源零筆或資料格式錯誤為 FAIL，來源／CLI 未授權或不可達才是 `ENVIRONMENT_BLOCKED`（詳見 `docs/verify.md` §6、§8）。
- [ ] 完成日常 Chrome compatibility gate 與同一 artifact 的 evidence 附加機制；自動隔離 Chromium 不得視為實際 Chrome 驗收（詳見 `docs/verify.md` §4 V4、§9）。
