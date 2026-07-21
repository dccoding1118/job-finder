# 模組設計 — api（localhost JSON API）

對應需求：R6、R7、R9。`jobfinder serve` 啟動只供 Chrome extension 使用的 localhost JSON API；Side Panel 負責全部日常使用者介面。

## 1. 職責與邊界

- 提供 Job、Run、待看清單的讀取，求職信生成要求、投遞狀態的受控寫入，以及非同步手動抓取。
- 將 104 list／job capture payload 交給 crawler 解析器與 pipeline ingest 入口，並回傳可直接呈現的判定。
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
| CORS | preflight 與帶 Origin 的 request 僅對精確 extension origin 回 `Access-Control-Allow-Origin`、`Authorization` 與必要方法；錯誤 Origin 即使 token 正確仍拒絕。無 Origin request 不回 CORS header。 |

extension 的 Options 儲存 API endpoint 與 token，service worker 代為發送所有 API request，避免將 API token 交給 104 頁面的 content script。遠端 VM 使用時，使用者先建立 SSH local forward，再將 endpoint 設為本機轉送位址。

驗收可啟用安全 request observer，只記錄 method、path、Origin 值或 absent、preflight/actual 與 Authorization 是否存在；不得記錄 token、body、JD、Profile 或 Agent 內容。

## 3. 共通回應與資料模型

- 成功回應為 `application/json`；錯誤一律為 `{ "error": { "code": "...", "message": "..." } }`，不可含 token、設定內容、Agent 原始輸入輸出或內部堆疊。
- Job 清單與單筆 Job 都回傳 Side Panel 呈現所需的 Job 欄位、`verdict`、現行 Score、核准 Letter、StatusEvent；無資料的欄位回 `null`，不得以零或猜測值替代。

### 3.1 verdict（判定）

`verdict` 由 viewmodel 從 `process_state` 與現行 score 導出，**不是 DB 欄位**；Job 回應、待看清單與兩個 capture endpoint 一律附帶，讓清單標記與 Side Panel 對同一筆 Job 顯示同一判定（PRD R9.6）。前端不得自行從 `process_state` 推導判定。

| `verdict` | 導出自 | 使用者用語 | 附帶欄位 |
|---|---|---|---|
| `unfit` | `filtered_out` | 不適合 | `filter_hits`（命中的條件名稱） |
| `recommended` | `shortlisted` / `letter_requested` / `letter_ready` / `letter_failed` | 推薦 | 現行 score 的五維與總分、`letter_state` |
| `not_recommended` | `scored` | 不推薦 | 現行 score 的五維與總分 |
| `pending_detail` | `discovered` | 待補全文 | 原始連結 |
| `pending_score` | `new` / `queued` | 待評分 | 無 |

推薦職缺另附 `letter_state`，供前端決定呈現生成入口、處理中或求職信：`none`（`shortlisted`，未要求）／`requested`（`letter_requested`，處理中）／`ready`（`letter_ready`）；`letter_failed` 的 verdict 仍為 `recommended`，`letter_state` 為 `failed`。
- Job 的原始 JD、信件與評分理由仍只在本機 API 回應，不寫入 extension storage 或 log。

## 4. API 路由

| 方法／路徑 | 請求 | 成功結果 | 錯誤 |
|---|---|---|---|
| `GET /api/v1/jobs` | 選填 `process_state`、`verdict`、`apply_state`、`source`、`limit`、`cursor` | 分數降冪的 Job page（含 verdict）與下一頁 cursor | 非法篩選或分頁回 400 |
| `GET /api/v1/jobs/{id}` | Job ID | Job、verdict、現行 Score、核准 Letter、StatusEvent | ID 非法 400；不存在 404 |
| `POST /api/v1/jobs/{id}/letter` | Job ID | `shortlisted` 或 `letter_failed` 經 store 轉為 `letter_requested`；回 `{ "status": "requested" }` 與更新後 Job | 非法來源狀態或不存在 4xx |
| `POST /api/v1/jobs/{id}/apply` | `apply_state`、選填 `note` | 更新後 Job 狀態與新 StatusEvent | 非法轉換或不存在 4xx |
| `GET /api/v1/queue` | 選填 `limit`、`cursor` | `discovered` Job page 與原始連結 | 非法分頁 400 |
| `POST /api/v1/runs` | 無 | `{ "status": "started" }` 或 `{ "status": "already_running" }` | 啟動失敗 500 |
| `GET /api/v1/runs` | 選填 `limit`、`cursor` | Run page，依開始時間新到舊；每輪含抓取事實與該輪職缺的現行判定分布 | 非法分頁 400 |
| `POST /api/v1/capture/list` | 104 列表 items | 每筆的 job ID、`verdict`、總分（無則 null）、`filter_hits`（無則 null）與是否本次新建 | payload 不合法 400；ingest 失敗 500 |
| `POST /api/v1/capture/job` | 104 內頁素材 | 該筆的 job ID、`verdict`、現行五維分數與 reason（無則 null）、`filter_hits`（無則 null）、是否為快取結果 | payload 不合法 400；ingest 失敗 500 |

`POST /api/v1/jobs/{id}/letter` 是使用者表達投遞意願的唯一 API 入口（PRD R5.0），對 `shortlisted` 與 `letter_failed` 皆適用——因此它同時取代了「重試求職信」這個獨立動作。handler 只呼叫 pipeline 的 `RequestLetter`，立即回應且不等待 Agent 完成；對已是 `letter_requested` 的 Job 重複呼叫為冪等（回 `requested`，不重複啟動工作）。

`POST /api/v1/runs` 觸發一次**抓取**（fetch），建立 request context 以外的背景工作，trigger 記為 `manual-extension`；server shutdown 時停止未完成工作。filter／score／letter 不由此觸發——那三階段由常駐 worker 持續消化，無需手動啟動（見 [design-pipeline](design-pipeline.md) §2.2）。

`GET /api/v1/runs` 的每輪回應含兩部分：`runs.stats` 的抓取事實（`fetched`／`new`／`queries`／`errors`），以及該輪職缺的**現行**判定分布（推薦／不推薦／評分中／不適合筆數），後者由 viewmodel 經 `SummarizeRunJobs(runID)` 即時查詢導出，不是抓取當下的快照（PRD R8.1）。因此展開一輪舊 run 看到的是那批職缺此刻的進度。

`POST /api/v1/capture/list` 與 `POST /api/v1/capture/job` **都是同步請求且路徑上不含 LLM 呼叫**（PRD R3.4、R9.2）：前者是使用者仍停留在 104 清單頁時的就地標記來源；後者建立完整 JD 資料並同步完成條件篩選後即回應，評分交由 worker 非同步進行。

因此 `capture/job` 的回應有三種形態：

| 情形 | `verdict` | 附帶 |
|---|---|---|
| 條件篩選淘汰 | `unfit` | `filter_hits`；**同步即得**，插件無須等待 |
| 已有現行評分且內容雜湊未變 | `recommended` ∣ `not_recommended` | 五維分數與 reason、`cached: true` |
| 通過篩選、待評分 | `pending_score` | 無；插件依 §4.2 輪詢 `GET /api/v1/jobs/{id}` 取得結果 |

兩者的結果都不含 Agent 原始輸入輸出。當日評分預算已用盡時，`pending_score` 另附 `budget_exhausted: true`，供前端呈現「已達今日評分上限」而非「處理中」。

## 5. 實作結構與測試

| 位置 | 職責 |
|---|---|
| `internal/api/server.go` | server 組裝、路由、認證、CORS 與 graceful shutdown |
| `internal/api/handlers.go` | 輸入驗證、錯誤映射、JSON 回應與背景抓取啟動 |
| `internal/api/viewmodel.go` | store 實體轉為 API response，含 verdict／`letter_state` 導出（§3.1）與 Run 判定分布組裝；不含業務規則 |
| `cmd/jobfinder/cli/serve.go` | Cobra `serve` 命令、設定載入，並隨 server 啟動／停止 pipeline 常駐 worker |

- L1 案例見 [test-api](../tests/test-api.md)：以 `httptest`、暫存 SQLite、合成資料與 fake pipeline 驗證 API 契約。
- L2 Side Panel 與 API 的端對端驗收見 [verify](../verify.md) B4；104 capture 與 Chrome 實機流程見 B5。
- 交付 `internal/api/`、`cmd/jobfinder/cli/serve.go`、B4 systemd 部署資源與 [deploy](../deploy.md) 的操作步驟。

## 6. 待決

（無。）
