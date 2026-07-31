# 模組設計 — api（localhost JSON API）

對應需求：R1、R6、R7、R9。`jobfinder serve` 啟動只供 Chrome extension 使用的 localhost JSON API；Side Panel 與 extension Profile editor 負責全部日常使用者介面。

## 1. 職責與邊界

- 提供 Profile 條件式讀寫、Job、Run、待看清單的讀取，求職信生成要求、單筆重新處理、投遞狀態的受控寫入，以及非同步手動抓取。
- 將 list／job capture payload 依其 `source` 交給對應的 crawler 解析器與 pipeline ingest 入口，並回傳可直接呈現的判定。
- server process 內另承載 pipeline 的常駐 worker（見 [design-pipeline](design-pipeline.md) §2.2）；worker 不經 API 路由，兩者只共用 process 生命週期與 store。
- 導出 verdict（§3.1）——所有前端呈現判定的唯一來源。
- 驗證 API token、extension origin 與 loopback 監聽位址；回應 JSON，不渲染 HTML、模板或靜態資產。
- 資料讀寫只經 store；狀態變更只經 store 轉換函式。pipeline 仍是唯一流程編排者。

## 2. 安全與部署契約

| 設定 | 規則 |
|---|---|
| `api.addr` | 必須是 loopback 位址，預設 `127.0.0.1:8686`；拒絕 wildcard 與非 loopback 位址。 |
| `api.token` | owner-only `config.yaml` 的非空隨機值，作為 extension 專用、可撤換的 credential；所有 endpoint 必須以 `Authorization: Bearer <token>` 驗證。 |
| `api.extension_origin` | 已安裝插件的精確 `chrome-extension://<id>` origin。request 帶 `Origin` 時必須完全相符；Chromium MV3 privileged fetch 未帶 `Origin` 時由有效 token 驗證。 |
| CORS | preflight 與帶 Origin 的 request 僅對精確 extension origin 允許必要方法（含 `PUT`）與 `Authorization`、`If-Match`；錯誤 Origin 即使 token 正確仍拒絕。無 Origin request 不回 CORS header。 |

extension 的 Options 儲存 API endpoint 與 token，service worker 代為發送所有 API request，避免將 API token 交給 104 頁面的 content script。遠端 VM 使用時，使用者先建立 SSH local forward，再將 endpoint 設為本機轉送位址。

驗收可啟用安全 request observer，只記錄 method、path、Origin 值或 absent、preflight/actual 與 Authorization 是否存在；不得記錄 token、body、JD、Profile 或 Agent 內容。

## 3. 共通回應與資料模型

- 成功回應為 `application/json`；錯誤一律為 `{ "error": { "code": "...", "message": "..." } }`，不可含 token、設定內容、Agent 原始輸入輸出或內部堆疊。
- Job 清單與單筆 Job 都回傳 Side Panel 呈現所需的 Job 欄位、`verdict`、現行 Score、核准 Letter、StatusEvent；無資料的欄位回 `null`，不得以零或猜測值替代。

### 3.1 verdict（判定）

`verdict` 由 viewmodel 從 `process_state` 與現行 score 導出，**不是 DB 欄位**；Job 回應、待看清單與兩個 capture endpoint 一律附帶，讓清單標記與 Side Panel 對同一筆 Job 顯示同一判定（PRD R9.6）。前端不得自行從 `process_state` 推導判定。

| `verdict` | 導出自 | 使用者用語 | 附帶欄位 |
|---|---|---|---|
| `unfit` | `filtered_out` | 不適合 | `filter_hits`（未通過的條件名稱）與現行篩選結果的逐條判定 |
| `recommended` | `shortlisted` / `letter_requested` / `letter_ready` / `letter_failed` | 推薦 | 現行 score 的四維與總分、`letter_state` |
| `not_recommended` | `scored` | 不推薦 | 現行 score 的四維與總分 |
| `pending_detail` | `discovered` | 待看 | 附原始連結，供使用者點開補全文 |
| `pending_screen` | `new` | 篩選中 | 無 |
| `pending_score` | `queued` | 評分中 | 無 |

判定共六類。判定名稱說的是**系統正在對這筆職缺做什麼**，不是狀態的內部名稱：`new` 與 `queued` 刻意分開，前者篩選尚未完成、後者已通過篩選正等待評分。`discovered` 等的則是使用者——只有點開原始頁面才拿得到 JD 全文。

`merged`（被判定為其他 Job 的重複刊登）**不導出 verdict**，也不出現在任何清單；命中它的查詢與 capture 一律改回該群組的 canonical Job（PRD R2.8）。

推薦職缺另附 `letter_state`，供前端決定呈現生成入口、處理中或求職信：`none`（`shortlisted`，未要求）／`requested`（`letter_requested`，處理中）／`ready`（`letter_ready`）；`letter_failed` 的 verdict 仍為 `recommended`，`letter_state` 為 `failed`。
- Job 的原始 JD、信件與評分理由仍只在本機 API 回應，不寫入 extension storage 或 log。

Job viewmodel 另回 `group`：`{ group_id, canonical_job_id, members: [{ job_id, source, url, external_id }], duplicate_candidate_count }`，供前端列出同一職缺的其他來源連結。單成員群組同樣回傳，`members` 只有自己。

Job viewmodel 另回 `filter_result`：`{ outcome, stage, conditions: [{ text, kind, group, category, verdict }] }`，供前端呈現「為什麼判不適合」與「缺哪項資訊」。無篩選結果時為 `null`；標為 `bonus` 的條件一併回傳，前端可分區呈現必備與加分。

Job viewmodel 另回四組 revision：`current_filter_revision`／`current_score_revision`（active）與 `filter_result_revision`／`score_result_revision`／`letter_revision`（各產出實際使用的值），以及由各產出 revision 是否為 NULL／不同於 active 導出的 `filter_stale`、`score_stale`、`letter_stale`。stale 不是 DB 狀態，不改變 verdict 或 apply state。

## 4. API 路由

| 方法／路徑 | 請求 | 成功結果 | 錯誤 |
|---|---|---|---|
| `GET /api/v1/jobs` | 選填 `process_state`、`verdict`、`apply_state`、`source`、`limit`、`cursor` | 分數降冪的 Job page（含 verdict 與 group），只含各群組的 canonical | 非法篩選或分頁回 400 |
| `GET /api/v1/jobs/{id}` | Job ID | Job、verdict、現行 Score、核准 Letter、StatusEvent | ID 非法 400；不存在 404 |
| `POST /api/v1/jobs/{id}/letter` | Job ID | `shortlisted` 或 `letter_failed` 經 store 轉為 `letter_requested`；回 `{ "status": "requested" }` 與更新後 Job | 非法來源狀態或不存在 4xx |
| `POST /api/v1/jobs/{id}/reprocess` | Job ID | 經 store 回到管線起點（有 JD 全文者 `new`，只有摘要者 `discovered`）並採用 active revision；回 `{ "status": "<新 process_state>" }` 與更新後 Job | 有求職信歷史或 `merged` 409 `reprocess_not_allowed`；不存在 404；Profile 未 ready 409 |
| `POST /api/v1/jobs/{id}/apply` | `apply_state`、選填 `note` | 更新後 Job 狀態與新 StatusEvent | 非法轉換或不存在 4xx |
| `GET /api/v1/queue` | 選填 `limit`、`cursor` | `discovered` Job page，含原始連結 | 非法分頁 400 |
| `POST /api/v1/runs` | 無 | `{ "status": "started" }` 或 `{ "status": "already_running" }` | 啟動失敗 500 |
| `GET /api/v1/runs` | 選填 `limit`、`cursor` | Run page，依開始時間新到舊；每輪含抓取事實與該輪職缺的現行判定分布 | 非法分頁 400 |
| `POST /api/v1/capture/list` | `source` ＋列表 items | 每筆的 job ID、`verdict`、總分（無則 null）、`filter_hits`（無則 null）與是否本次新建 | payload 不合法 400；ingest 失敗 500 |
| `POST /api/v1/capture/job` | `source` ＋內頁素材 | 該筆的 job ID、`verdict`、現行四維分數與 reason（無則 null）、`filter_hits`（無則 null）、是否為快取結果 | payload 不合法 400；ingest 失敗 500 |
| `GET /api/v1/profile` | 無 | `status`、結構化 `profile`（含程式物化的 `derived`）、兩個 revision、摘要、issues、重新處理預估；header 帶 ETag | 認證或檔案 I/O 失敗 |
| `PUT /api/v1/profile` | `If-Match`＋完整 Profile JSON | 新 ETag、兩個 revision、`filter_changed`／`score_changed` | 缺條件 428；衝突 412；驗證 422；儲存失敗 500 |
| `POST /api/v1/profile/reprocess` | 無 | active 兩個 revision 與重新篩選／重新評分／排隊／受保護統計 | Profile 未 ready 409；重新處理失敗 500 |
| `GET /api/v1/duplicates` | 選填 `limit`、`cursor` | `pending` 候選 page：雙方的 job id、職稱、公司、地區、來源、相似度與原因 | 非法分頁 400 |
| `POST /api/v1/duplicates/{id}/merge` | 候選 ID | 合併兩群組並回更新後的 canonical Job；候選轉 `merged` | 候選不存在 404；已裁決 409 |
| `POST /api/v1/duplicates/{id}/ignore` | 候選 ID | 候選轉 `ignored`，回 `{ "status": "ignored" }` | 同上 |
| `POST /api/v1/jobs/{id}/unmerge` | Job ID | alias 還原為合併前狀態與獨立群組，回更新後 Job | 非 `merged` 狀態 409；不存在 404 |
| `GET /api/v1/status` | 無 | 各 `process_state` 的職缺筆數、當日篩選與評分預算餘額、最近 20 筆 Agent 呼叫摘要 | 讀取失敗 500；非 GET 405 |

清單 endpoint 預設每頁 20 筆，`limit` 可設為 1–100。`next_cursor` 是 API 產生的不透明字串；有後續資料時回傳字串，末頁回 `null`。client 只能原樣帶回 `cursor`，不得解析或自行產生；非法 `limit` 或 `cursor` 回 `400 invalid_request`。Job cursor 沿用當次篩選與分數排序，篩選條件變更時必須從第一頁重新查詢。

`GET /api/v1/profile` 的 `status` 為 `missing`／`invalid`／`ready`；只有可安全解析時才回完整結構化 Profile。正常授權 GET 是唯一可回 Profile 內容的 response；錯誤、observer、log 與 evidence 不得包含 Profile、薪資、經歷、denylist 命中值或 YAML。

`PUT` 必須先通過 ETag、strict schema 與 PII 驗證，再原子寫入並切換 active snapshot；不得修改既有 Job revision 或呼叫 activation。回應分別以 `filter_changed`／`score_changed` 表達兩個 revision 是否改變，兩者皆為偽即語意 no-op；只改 `search`、`achievements` 等不進 hash 的欄位時兩者皆為偽（ETag 仍會變）。`POST /api/v1/profile/reprocess` 取得當下 ready snapshot，經 pipeline 專用入口將可更新的 stale Job 切至該 revision，重跑範圍依變更的 revision 決定（見 [design-pipeline](design-pipeline.md) §4）；求職信狀態與投遞歷史受保護。Profile 未 ready 時，手動 run、reprocess 與 capture 等處理型 route 回 `409 profile_not_ready`；Profile API 與既有 Job／Run 讀取仍可用。

`POST /api/v1/jobs/{id}/letter` 是使用者表達投遞意願的唯一 API 入口（PRD R5.0），對 `shortlisted` 與 `letter_failed` 皆適用——因此它同時取代了「重試求職信」這個獨立動作。handler 只呼叫 pipeline 的 `RequestLetter`，立即回應且不等待 Agent 完成；對已是 `letter_requested` 的 Job 重複呼叫為冪等（回 `requested`，不重複啟動工作）。

`POST /api/v1/jobs/{id}/reprocess` 是單筆判定重做的唯一入口：handler 呼叫 pipeline 的 `RequestReprocess`，立即回應且不等待 Agent 完成，該筆由常駐 worker 以 active revision 重新篩選、通過者再評分。重做的是整條判定鏈而非只有分數——錯的判定同樣可能出在篩選關，`filtered_out` 因此是可重做的來源狀態。舊命中與舊篩選逐條結論隨之清除，舊 score 於新 score 寫入前仍是該 Job 的現行分數。求職信階段（`letter_requested`／`letter_ready`／`letter_failed`）與 `merged` 一律回 `409 reprocess_not_allowed`，因此重新處理不會改寫求職信、投遞歷史與使用者裁決過的合併。重複呼叫為冪等。


`GET /api/v1/status` 是處理進度的唯一讀取面：回 `jobs`（各 `process_state` 筆數，`new` 與 `queued` 即常駐 worker 的待消化量）、`filter_budget` 與 `score_budget`（各含 `remaining`、`limited`）與 `agent_calls`（最近 20 筆的 `role`、`runner`、`ok`、`duration_ms`、`job_id`、`created_at`）。`ok` 表示「runner 有回應且回應通過契約驗證」，與評分高低無關——低分或不推薦仍是成功呼叫。成功呼叫不附任何 Agent 輸出；未通過的呼叫附 `failure_kind` 與截斷至 400 字元的 `detail`。`failure_kind` 由 agents 模組分類：`runner_error`（CLI 自報錯誤，含額度、認證與逾時，優先於內容驗證）、`empty_output`、`no_json`、`invalid_json`、`reason_too_long`、`score_out_of_range`、`invalid_condition`、`invalid_content`。此 route 不含 Profile 內容、JD、薪資與信件內容。

`POST /api/v1/runs` 觸發一次**抓取**（fetch），建立 request context 以外的背景工作，trigger 記為 `manual-extension`；server shutdown 時停止未完成工作。filter／score／letter 不由此觸發——那三階段由常駐 worker 持續消化，無需手動啟動（見 [design-pipeline](design-pipeline.md) §2.2）。

`GET /api/v1/runs` 的每輪回應含兩部分：`runs.stats` 的抓取事實（`fetched`／`new`／`queries`／`errors`），以及該輪職缺的**現行**判定分布（推薦／不推薦／評分中／不適合筆數），後者由 viewmodel 經 `SummarizeRunJobs(runID)` 即時查詢導出，不是抓取當下的快照（PRD R8.1）。因此展開一輪舊 run 看到的是那批職缺此刻的進度。

兩個 capture endpoint 的 payload 必帶 `source`（`104` / `cake`），據以選用解析器；未知或缺 `source` 回 `400 invalid_request`，不猜測平台。素材欄位依來源與頁面型態而異：

| endpoint | `source` | 素材欄位 |
|---|---|---|
| `capture/list` | `104` | `items[]`（DOM 收割的項目） |
| `capture/list` | `cake` | `next_data`（SSR 狀態仍相符時）或 `items[]` |
| `capture/job` | `104` | `json_ld[]`，缺時 `dom`（單一物件） |
| `capture/job` | `cake` | `cake_dom`（`title`、`company_name`、`sections[]` 的 `title`／`body`、`meta[]`）；`next_data` 仍受理但插件不送 |

重複裁決 route 都是同步的：handler 呼叫 store 的合併／還原交易後直接回應，不建立背景工作、不呼叫 Agent。合併不刪除任何既有 score 或 letter；alias 的產出保留供稽核，只是不再呈現。

`POST /api/v1/capture/list` 與 `POST /api/v1/capture/job` **都是同步請求且路徑上不含 LLM 呼叫**（PRD R3.7、R9.2）：前者是使用者仍停留在清單頁時的就地標記來源；後者建立完整 JD 資料並同步完成結構化硬規則比對後即回應，語意篩選與評分交由 worker 非同步進行。

因此 `capture/job` 的回應有三種形態：

| 情形 | `verdict` | 附帶 |
|---|---|---|
| 結構化硬規則未通過 | `unfit` | `filter_hits`；**同步即得**，插件無須等待 |
| 已有現行評分且內容雜湊未變 | `recommended` ∣ `not_recommended` | 四維分數與 reason、`cached: true` |
| 通過結構化條件，待語意篩選與評分 | `pending_screen` | 無；插件依 §4.2 輪詢 `GET /api/v1/jobs/{id}` 取得結果（可能轉為 `unfit` 或評分終態） |

兩者的結果都不含 Agent 原始輸入輸出。當日篩選或評分預算已用盡時，`pending_screen`／`pending_score` 另附 `budget_exhausted: true`，供前端呈現「已達今日上限」而非「處理中」。

## 5. 實作結構與測試

| 位置 | 職責 |
|---|---|
| `internal/api/server.go` | server 組裝、路由、認證、CORS 與 graceful shutdown |
| `internal/api/handlers.go` | 輸入驗證、錯誤映射、JSON 回應與背景抓取啟動 |
| `internal/api/viewmodel.go` | store 實體轉為 API response，含 verdict／`letter_state`／`group` 導出（§3.1）與 Run 判定分布組裝；不含業務規則 |
| `cmd/jobfinder/cli/serve.go` | Cobra `serve` 命令、設定載入，並隨 server 啟動／停止 pipeline 常駐 worker |

- L1 案例見 [test-api](../tests/test-api.md)：以 `httptest`、暫存 SQLite、合成資料與 fake pipeline 驗證 API 契約。
- L2 Side Panel 與 API 的端對端驗收見 [verify](../verify.md) V4；104 capture 與 Chrome 實機流程見 V5；Cake capture 與跨來源合併見 V6。
- 交付 `internal/api/`、`cmd/jobfinder/cli/serve.go`、B4 systemd 部署資源與 [deploy](../deploy.md) 的操作步驟。

## 6. 待決

（無。）
