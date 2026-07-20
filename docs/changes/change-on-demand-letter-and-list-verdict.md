# 求職信按需生成與清單就地判定

## 背景與動機

求職信是全流程最耗 token 的階段，而系統不支援自動投遞，也不產生履歷——為每筆達閾值職缺預先生成求職信，多數產出不會被使用。生成時機因此收斂到使用者對推薦職缺表達投遞意願的那一刻。

同時，插件在 104 清單頁（搜尋結果、推薦與通知清單）收割的職缺欄位不含 JD 全文，不足以支撐五維評分，但足以套用職稱、地點、薪資與公司黑名單等欄位可用的條件。這個判定不耗用 LLM、可即時完成，應該在使用者仍停留於該頁面時就地標記出來；已存在於資料庫的職缺則直接顯示既有判定，不重跑任何階段。

## 決策摘要

| 主題 | 最新狀態 |
|---|---|
| 求職信生成時機 | 達閾值的推薦職缺停留 `shortlisted`，系統不主動生成；使用者按下「產生求職信」才轉入 `letter_requested`，由 letter 階段取件。`letter_failed` 同樣由使用者再次要求才重跑。 |
| 狀態機 | 新增 `letter_requested`：`shortlisted` / `letter_failed` → `letter_requested` → `letter_ready` / `letter_failed`。`letter_requested` 是 letter 階段的唯一取件狀態，`shortlisted` 不被任何階段取件。 |
| 生成入口 | `POST /api/v1/jobs/{id}/letter` 對 `shortlisted` 與 `letter_failed` 皆適用，取代獨立的 retry-letter 動作；handler 立即回應受理，不等待 Agent。CLI 對應 `jobfinder letter request --job ID`。 |
| 判定（verdict） | 由 API viewmodel 從 `process_state` ＋現行 score 導出，非 DB 欄位：`unfit`／`recommended`／`not_recommended`／`pending_detail`／`pending_score`；推薦職缺另附 `letter_state`。清單標記、sidebar 與 dashboard 共用同一份導出結果，前端不自行推導。 |
| 清單頁快速判定 | 列表收割路徑不含 LLM 呼叫與網路抓取：既有 Job 直接回現行判定與總分；新職缺入庫 `discovered` 後套用欄位可用條件，命中即 `filtered_out` 並標記為不適合。 |
| 內頁完整評估 | 內頁擷取補入 JD 全文後執行條件篩選與 Agent 評分，sidebar 顯示判定與五維分數；已有現行評分者回快取。此路徑不生成求職信。 |
| ScoreResult | 維持五維分數＋ ≤50 字 reason，不擴充 pros/cons。更深入的推薦理由與外部評價改以 S1 的「深入評估」按需功能承載（見 [roadmap](../roadmap.md) S1）。 |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [PRD](../PRD.md) | 核心原則、`process_state` 表與擁有權（R5.0 按需生成、R3.4 快速判定、R6.8 生成入口、R9.1／R9.2／R9.6 標記與判定一致）、§5 三入口流程。 |
| [系統設計](../design.md) | 求職信生成時機、verdict 導出與清單頁快速判定的關鍵決策；§6 狀態機摘要。 |
| [schema 設計](../designs/design-schema.md) | 狀態機權威定義與 `PickForStage` 取件狀態。 |
| [pipeline 設計](../designs/design-pipeline.md) | letter 階段取件、`RequestLetter` 入口、ingest 入口的回傳與即時性約束。 |
| [api 設計](../designs/design-api.md) | verdict／`letter_state` 導出表、`POST /jobs/{id}/letter`、capture 回應。 |
| [extension 設計](../designs/design-extension.md) | dashboard 生成入口、列表就地標記表、sidebar 呈現。 |
| [測試與驗證](../verify.md) | V2／V3／V4 的按需生成與判定條件；V5 的就地標記與零 Agent 呼叫條件。對應 L1 案例見 test-schema、test-pipeline、test-api、test-extension。 |

## 待實作進度

| 項目 | 完成條件 |
|---|---|
| 狀態機（B0 回溯） | store 支援 `letter_requested` 及其轉換；`shortlisted→letter_ready/letter_failed` 被拒；`PickForStage` letter 階段改取 `letter_requested`。 |
| 按需生成（B3 回溯） | pipeline 提供 `RequestLetter` 與背景 letter 觸發；無使用者要求時 letter 階段零取件、零 Agent 呼叫。 |
| API（B4 回溯） | viewmodel 導出 verdict／`letter_state`；新增 `POST /jobs/{id}/letter` 並移除 retry-letter；Job 清單支援 verdict 篩選。 |
| dashboard（B4 回溯） | 對照區依 `letter_state` 呈現生成入口、處理中、信件或未過審；清單顯示判定並可依判定篩選。 |
| capture 與標記（B5） | 兩個 capture endpoint 回傳 verdict 與附帶欄位；列表路徑零 LLM 呼叫；content script 依 verdict 就地標記，既有職缺直接標記且不重跑。 |
| 驗收 | mock E2E 覆蓋「未要求不生成 → 要求後生成」；V5 覆蓋列表就地標記與判定一致性。 |
