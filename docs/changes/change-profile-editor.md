# Profile 編輯器與可追溯 Profile Revision

> **變更紀錄檔**（非 canonical）。正式需求與設計的最新狀態於本文件核准後，就地寫入「Canonical 落點」所列文件。
> 類型：update。日期：2026-07-23（Asia/Taipei）。狀態：開發與自動化驗收中，待 Chrome 人工 gate 與正式發布。

## 背景／動機

`profile.yaml` 同時承載去識別化專業履歷與求職條件，是條件篩選、AI 評分與求職信生成的個人資料單一真相。現行操作要求使用者登入後端主機、找到版控外檔案並人工編輯 YAML；這使一般履歷維護依賴檔案系統與 YAML 知識，也無法在使用者明確儲存時統一執行結構驗證、PII 檢核與安全寫入。

`jobfinder serve` 目前在啟動時載入 Profile，常駐 worker 之後持續使用該份記憶體內容。即使磁碟檔案被更新，執行中的服務也不會立即採用。既有 Job、Score、Letter 與 Agent 稽核資料亦未記錄使用哪份 Profile 產生，因此 Profile 改變後，系統無法區分現行結果與過期結果，也不會重新檢驗已被篩除或已評分的職缺。

本變更在 Chrome extension 內提供單一 Profile 的建立與編輯介面，保留 YAML 為磁碟真相源；同時加入內容定址的 `profile_revision`，使篩選、評分、求職信與 Agent 呼叫可判斷所使用的 Profile 是否仍為現行版本。

## 目標與非目標

### 目標

- 使用者可從 Chrome extension 建立、檢視與編輯完整 Profile，不需直接操作後端檔案系統。
- `profile.yaml` 維持單一真相；Profile 不搬入 SQLite，也不在 extension storage 保存副本。
- 只有使用者明確按下儲存，系統才可驗證並寫入 Profile；系統不得自行改寫內容。
- 儲存前執行完整結構驗證與 PII 檢核，失敗時不得改動磁碟檔案或 active Profile。
- 有效儲存後立即更新常駐服務的 Profile snapshot，不需重新啟動或部署。
- 以 `profile_revision` 追蹤條件篩選、Score、Letter 與 Agent 呼叫所使用的 Profile 語意版本。
- Profile 語意內容改變時，新擷取職缺立即使用新 revision；既有職缺保留舊評分，只有使用者明確要求才重新處理，所有 AI 呼叫仍受每日預算限制。
- Profile 尚未建立時，API 仍可啟動並提供建立 Profile 的必要介面，避免啟動死結。

### 非目標

- 不匯入 PDF、Word、Markdown 或平台履歷，也不由 Agent 自動填寫表單。
- 不支援多 Profile、多履歷切換或同一 Job 對多份 Profile 評分。
- 不讓反向校準功能自動套用建議；校準仍只產生建議，由使用者在編輯器確認後儲存。
- 不自動重新生成、覆蓋或刪除既有求職信，不改寫投遞狀態與投遞事件。
- 不保存可還原的 Profile 歷史版本；revision 用於身分、冪等與 stale 判斷，不是履歷版本庫。
- 不保證保留 YAML 註解、空白或人工欄位排序。

匯入履歷自動填寫與多 Profile 保留於專案 Roadmap，不屬本次實作。

## 決策摘要

| 主題 | 決策 |
|---|---|
| 前端入口 | Side Panel「系統」頁顯示 Profile 狀態與摘要；「編輯履歷與求職條件」開啟 extension 自有的全頁編輯器。 |
| 編輯資料 | 前端使用結構化 JSON 表單；不解析、不產生 YAML。 |
| 磁碟真相 | `config.yaml` 指定的 `profile.path` 仍是唯一 Profile 真相源。 |
| 內容擁有權 | 使用者擁有內容；API 寫入只代表執行使用者明確送出的儲存命令。 |
| 儲存 | 後端驗證 JSON、PII 與 schema 後產生 canonical YAML，以 owner-only 權限原子替換。 |
| 寫入衝突 | `GET` 回傳檔案 ETag；`PUT` 強制 `If-Match`，檔案已被外部修改時拒絕覆蓋。 |
| 語意版本 | `profile_revision` 是 canonical 結構化內容的 SHA-256；不受 YAML 註解、空白或欄位排版影響。 |
| 相同內容 | 語意相同的重複儲存不產生新 revision、不重新入隊，也不增加 AI 成本。 |
| Runtime | 單一同步化 Profile provider 提供 immutable snapshot；每個工作開始時固定取得一份 snapshot。 |
| 重新處理 | 儲存不重新處理既有職缺；系統頁提供「更新過時評分職缺」，按下後才將未進入求職信流程的既有職缺切換至 active revision。 |
| 歷史保護 | `letter_requested`、`letter_ready`、`letter_failed`、Letter、apply state 與 apply event 不因 Profile 儲存自動改動。 |
| 安全 | Profile 與 API body 不寫入 extension storage、log、evidence 或 telemetry；denylist 不傳到前端。 |
| Bootstrap | Profile 缺少時 `serve` 以 `missing` 模式啟動，讀取型 API 與 Profile 編輯 API 可用，抓取與 worker 暫停。 |

## 相對現行狀態的差異

| 面向 | 現行狀態 | 本變更狀態 |
|---|---|---|
| Profile 編輯 | 人工修改後端 `profile.yaml` | extension 全頁表單；後端受控寫入同一 YAML |
| API | 無 Profile route | 提供 Profile 讀取與條件式儲存 |
| Profile 缺少 | `serve` 啟動失敗 | `serve` 進入 setup 模式，允許首次建立 |
| 生效時機 | 啟動時載入後固定 | 儲存成功後立即替換 runtime snapshot |
| 結果追溯 | 無法得知使用哪份 Profile | Job／Score／Letter／Agent call 帶 `profile_revision` |
| 舊結果 | Profile 改變後仍被視為現行 | 全部依 revision 標示最新／待重評；可重新處理者由使用者手動入隊，受保護歷史只標示 stale |
| YAML 相容 | 人工格式與註解自然保留 | 欄位資料相容；UI 儲存後輸出 canonical YAML，不保留註解與排版 |

## Profile 編輯體驗

### 入口與頁面

Side Panel 維持「目前職缺、待看、推薦、系統」四個頁籤。「系統」頁新增 Profile 卡片，呈現下列資訊：

| Profile 狀態 | 呈現與動作 |
|---|---|
| `missing` | 顯示「尚未建立履歷與求職條件」，提供「開始設定」。 |
| `invalid` | 顯示安全的驗證摘要與檔案需修復提示；不可啟動抓取或 worker。 |
| `ready` | 顯示年資、技能數、經歷數、求職方向與目前 revision 短碼，提供「編輯」。 |
| `saving` | 停用重複儲存與離頁動作，保留當前表單內容。 |
| `conflict` | 說明後端檔案已被其他方式修改，提供重新載入；不得以舊資料強制覆蓋。 |

完整表單使用 extension 自有頁面，不建立獨立 Web UI。頁面依序分為：

1. 專業摘要與總年資。
2. 學歷。
3. 工作經歷與量化成就。
4. 技能分級與證照。
5. 求職方向、薪資、地點與遠端意願。
6. 產業避開與條件篩選。
7. 誠實邊界。

陣列欄位可新增、刪除與排序。前端執行必填、型別與 enum 的即時提示；後端驗證仍是唯一權威。欄位名稱使用業務用語，不顯示 pipeline 內部狀態名稱。

陣列新增後，焦點精確落在觸發按鈕所屬區塊的新欄位，並只捲動到讓該欄位可見的最近位置；不得因其他同型清單或前一次 focus 跳離目前編輯區塊。

Side Panel「系統」頁依序以卡片群組呈現：

1. 「連線與設定」：localhost API 狀態與開啟 Options 的入口。
2. 「Profile」：摘要、revision、過時職缺統計、編輯入口與手動更新按鈕。
3. 「自動與手動批次」：每日 08:30（Asia/Taipei）排程資訊與立即手動抓取。
4. 「批次歷程」：維持抓取事實與現行判定分布。

職缺總分與五維評分一律四捨五入為整數。已有 Score 時同時顯示短版 Profile revision：與 active revision 相同為綠色「最新」，不同或無法證明為黃色「待重評」。

### 儲存互動

- 不自動儲存。只有按下「儲存 Profile」才送出寫入要求。
- Profile 草稿只存在編輯頁的記憶體；重新整理、關閉頁面或 extension reload 後不保留。
- 有未儲存內容時，離開頁面前顯示確認。
- 儲存前說明新擷取職缺會立即使用新版 Profile，既有職缺保留原評分與 revision；使用者確認後才送出。
- 儲存成功顯示新 revision 短碼，並提示可在系統頁手動更新過時評分。
- 結構或 PII 驗證失敗時，依欄位路徑顯示問題；API 不回傳 denylist 內容或完整 Profile。
- `412 profile_conflict` 時保留當前草稿，使用者可自行複製內容後重新載入；本次不提供強制覆蓋。

## Profile Schema 與 YAML 相容性

Profile 欄位維持 `design-profile.md` 定義的現行 schema。API JSON 使用相同的欄位名稱、型別、陣列順序與 enum；`profile_revision`、檔案 ETag 與 UI 狀態不是 Profile 內容，不寫入 YAML。

後端將 YAML 的解析、結構驗證、PII 檢核與 canonical 序列化收斂至 `internal/profile`：

| 規則 | 行為 |
|---|---|
| 已知欄位 | 完整 round-trip；陣列順序保留。 |
| 未知欄位 | 編輯 API 拒絕載入或儲存，避免 decode 後無聲遺失。 |
| 註解與排版 | 不屬資料契約；第一次由 UI 儲存後可能移除或重排。 |
| 空白字串與非法 enum | 驗證失敗，不寫檔。 |
| PII | 逐欄位執行 denylist 與內建 pattern 檢查，任一命中即拒絕。 |
| 檔案權限 | 新檔與替換後檔案為 `0600`；不得擴大既有父目錄權限。 |
| 檔案不存在 | 以空白表單開始；第一次有效儲存建立檔案。 |
| YAML 語法損壞 | 服務進入 `invalid`；UI 不提供無條件覆蓋，避免毀損仍可復原的人工內容。 |

## Revision 模型

### `profile_revision`

`profile_revision` 識別 Profile 的語意內容，格式為 `sha256:<hex>`。計算輸入為固定 schema 版本標記與 Profile 結構的 canonical JSON；物件鍵順序固定、數值格式固定、陣列順序保留。YAML 註解、縮排、換行與欄位排版不參與計算。

此 revision 具備下列語意：

- 相同 Profile 資料必須得到相同 revision。
- 任一影響篩選、評分或求職信的欄位改變，revision 必須改變。
- revision 不由使用者輸入，也不採單純時間戳或每次儲存遞增。
- 同一 revision 的重複啟用為冪等操作，不得再次重新入隊。
- revision 只證明內容身分；系統不保存舊 Profile 內容，因此無法只靠 revision 還原歷史 Profile。

### 檔案 ETag

ETag 識別 `profile.yaml` 的精確檔案 bytes，用於 optimistic concurrency，與 `profile_revision` 分工：

| 情境 | ETag | `profile_revision` | 結果 |
|---|---|---|---|
| 只修改 YAML 註解或排版 | 改變 | 不變 | 防止 UI 覆蓋外部修改；不重新評分。 |
| 修改 Profile 欄位 | 改變 | 改變 | 寫入新內容並切換 active snapshot；既有職缺保持原 revision。 |
| 重送完全相同內容 | 不變或僅 canonical 格式改變 | 不變 | 儲存冪等，不重新處理。 |
| 檔案原本不存在 | `"missing"` | `null` | `If-Match: "missing"` 可建立第一份 Profile。 |

## API 契約

所有 Profile route 沿用現有 Bearer token、extension origin、loopback-only 與安全錯誤契約。service worker 仍是唯一 API client；content script 不可存取 Profile 或 token。

### 路由

| 方法／路徑 | 請求 | 成功結果 | 主要錯誤 |
|---|---|---|---|
| `GET /api/v1/profile` | 無 | Profile 狀態、結構化內容、`profile_revision`、摘要、重新處理預估；header 帶 ETag | 認證失敗；檔案 I/O 失敗 |
| `PUT /api/v1/profile` | `If-Match` header ＋完整 Profile JSON | 新 ETag、`profile_revision`、是否語意改變 | `412 profile_conflict`、`422 profile_invalid`、`500 profile_save_failed` |
| `POST /api/v1/profile/reprocess` | 無 | active revision、重新條件篩選／排隊／受保護統計 | `409 profile_not_ready`、`500 profile_reprocess_failed` |

`PUT` 必須帶 `If-Match`；缺少時回 `428 precondition_required`。CORS 允許 `PUT` 與 `If-Match`，且只對正確 extension origin 回應。

### `GET /api/v1/profile` 回應

| 欄位 | 型別 | 說明 |
|---|---|---|
| `status` | `missing` ∣ `invalid` ∣ `ready` | Profile 可用狀態。 |
| `profile` | object ∣ null | 可安全解析時的完整結構化 Profile；不存在或 YAML 語法損壞時為 null。 |
| `profile_revision` | string ∣ null | `ready` 時的語意 revision。 |
| `summary` | object ∣ null | Side Panel 卡片所需的去識別化統計，不含薪資或自由文字。 |
| `issues` | array | 安全錯誤代碼、欄位路徑與訊息；不含 denylist 值或完整輸入。 |
| `reprocess_estimate` | object | 若目前內容被另一份有效 Profile 取代，預估需重新篩選、重新評分與受保護筆數。 |

### `PUT /api/v1/profile` 回應

| 欄位 | 型別 | 說明 |
|---|---|---|
| `status` | `ready` | 儲存後狀態。 |
| `profile_revision` | string | active Profile revision。 |
| `semantic_changed` | bool | 相對儲存前的 Profile 語意是否改變。 |

除授權成功的 `GET /api/v1/profile` 結構化 `profile` 欄位外，錯誤回應、request observer、log 與 evidence 不得包含 Profile 原文、薪資、經歷、denylist 命中值或 YAML 內容。

## Runtime 與啟動模型

### Profile provider

`serve` 內使用單一 Profile provider 管理 immutable snapshot。snapshot 包含解析後 Profile、供 Agent 使用的 canonical YAML、`profile_revision` 與檔案 ETag。

- filter、score 與 letter 工作開始時各取得一次 snapshot；單一工作途中不得換版。
- pipeline 不長期複製 Profile struct、YAML 或 Filter；需要時向 provider 取得 snapshot。
- Profile 更新與 worker 寫入共用 revision-aware 同步／compare-and-set 契約，不以單純記憶體 mutex 取代 store 不變量。
- API 讀取只回 snapshot 的結構化副本，不暴露內部可變物件。

### Setup 模式

`profile.path` 不存在時，`serve` 仍載入 config、store 與 API，Profile provider 狀態為 `missing`：

- `GET/PUT /api/v1/profile`、健康檢查、既有 Job／Run 讀取可用。
- 手動 run、依 Profile 產生查詢、capture 的條件篩選、worker filter／score／letter 回 `409 profile_not_ready` 或保持暫停。
- 第一次有效儲存後 provider 轉為 `ready`；新 ingest 與 worker 立即可使用 active snapshot，既有 Job 不自動切換 revision。

既有檔案無法解析或驗證時使用 `invalid` 模式；服務不得偷偷退回預設 Profile，也不得以未驗證內容執行 Agent。

### 寫入與手動 activation

Profile 更新依下列順序協調檔案與 SQLite：

1. 取得 Profile update lock，讀取並比對 `If-Match`。
2. 解析 JSON，執行結構驗證與逐欄位 PII 檢核。
3. 產生 canonical YAML、ETag 與 `profile_revision`。
4. 語意 revision 未改變時，只在需要時 canonicalize 檔案，不執行重新入隊。
5. 以同目錄暫存檔、`fsync`、`chmod 0600` 與 rename 原子替換 YAML。
6. 替換 provider snapshot；既有 Job revision 與狀態不變。
7. 使用者另行 POST reprocess 時，pipeline 取得當下 active snapshot，才以 SQLite transaction 切換 eligible Job revision 並入隊。

Profile 檔案儲存與 Job activation 分為兩個明確的使用者命令，沒有跨檔案與 SQLite 的半提交狀態。reprocess transaction 失敗時既有 Job 不變，active Profile 仍可供新 ingest 使用；使用者可安全重試。服務啟動只由磁碟重算 active revision，不自動重新處理舊資料。

## Schema 與資料所有權

| 實體／欄位 | 型別 | 所有權與語意 |
|---|---|---|
| `jobs.profile_revision` | TEXT NULL | 該 Job 目前應使用、或現行 process 判定所屬的 Profile revision；legacy 或受保護舊資料可為 null。 |
| `scores.profile_revision` | TEXT NULL | 產生該 Score 的 Profile revision；新資料必填，legacy 可為 null。 |
| `letters.profile_revision` | TEXT NULL | 產生該 Letter 的 Profile revision；新資料必填，legacy 可為 null。 |
| `agent_calls.profile_revision` | TEXT NULL | score／draft／review 呼叫開始時使用的 Profile revision；與 Profile 無關的呼叫為 null。 |

`content_hash` 只描述來源 Job 內容，不加入 `profile_revision`。Job 內容與 Profile 是兩個獨立變動軸：任一者改變都可能使評估過期，但兩者不得混成同一 hash。

Score 與 Letter 維持 append-only 歷史。查詢現行 Score 時，優先取得與 `jobs.profile_revision` 相同的最新 Score；不同 revision 的舊 Score 不可當成現行評分，但可保留供稽核。API 對單筆 Job 另回：

| 欄位 | 說明 |
|---|---|
| `current_profile_revision` | provider 的 active revision。 |
| `evaluation_profile_revision` | `jobs.profile_revision`。 |
| `score_profile_revision` | 所呈現 Score 的 revision。 |
| `letter_profile_revision` | 所呈現 Letter 的 revision。 |
| `score_stale` | Score revision 為 null 或不同於 active revision。 |
| `letter_stale` | Letter revision 為 null 或不同於 active revision。 |

stale 是導出值，不新增為 DB 狀態，也不改變 apply state。

### 既有資料 migration

新增欄位皆允許 legacy `NULL`，migration 不猜測舊資料曾使用哪份 Profile：

- 既有 Score、Letter 與 Agent call 保留原值，`profile_revision=NULL` 代表版本不可證明。
- 服務升級後第一次載入有效 Profile 時只建立 active snapshot；legacy 資料維持 stale，等待使用者手動更新。
- `discovered`、`new`、`queued`、`filtered_out`、`scored`、`shortlisted` 依下節矩陣套用現行 revision；需要評分者受每日預算逐步消化。
- `letter_requested`、`letter_ready`、`letter_failed` 與 apply 歷史不回退，舊 revision 維持 null 並導出 stale。
- migration 與程序重啟皆不呼叫 Agent或重新入隊。

## Profile 變更後的重新處理

Profile activation 只由系統頁的明確使用者動作，經 API、pipeline 與 store 專用入口完成，不開放一般狀態轉換任意回退。Profile 儲存不呼叫此入口。所有既有 Score、Letter、Agent call 與 status event 均保留。

| 現行狀態／資料 | Profile 語意改變後的行為 |
|---|---|
| `discovered`（partial） | 套用新 revision 並重新執行 partial 可用條件；結果維持 `discovered` 或轉為 `filtered_out`。 |
| `filtered_out`（partial） | 清除舊 `filter_hits`，以新 revision 重做 partial 條件；可回到 `discovered`。 |
| `new`／`queued`（有全文） | 設為新 revision 並回到／維持 `new`，由 worker 重新完整篩選。 |
| `filtered_out`／`scored`／`shortlisted`（有全文） | 清除舊 `filter_hits`，設為新 revision 並轉為 `new`；通過篩選者重新排入 score。 |
| `letter_requested` | 不取消、不重送；已開始的工作以其取得的 snapshot 完成，Letter 記錄實際 revision。 |
| `letter_ready` | 不重跑、不覆蓋 approved Letter；Score／Letter 可顯示 stale。 |
| `letter_failed` | 不自動重試；使用者再次要求時才以當時 active Profile 生成。 |
| apply state／apply event | 完全不改動；只在畫面呈現相關 Score／Letter 是否 stale。 |

Profile activation 不直接呼叫 LLM。它只做同步的本地 partial 篩選與狀態重新入隊；score worker 依既有每日 `max_score_per_day` 預算逐步消化。預算耗盡時 Job 停留 `queued`，跨台北日界後繼續。

### 競態與冪等

- filter／score worker 取件時同時取得 `jobs.profile_revision` 與同 revision 的 active Profile snapshot；revision 不相符時不執行該筆工作。
- letter worker 使用工作開始時的 active Profile snapshot，並把實際 revision 寫入 Letter 與 Agent call；它不要求與 Job 的既有評分 revision 相同。
- filter 結果、Score 保存與 process transition 必須以 expected job state ＋ expected `profile_revision` 做 compare-and-set。
- Profile activation 已把 Job 切到新 revision 時，使用舊 snapshot 完成的 worker 不得把 Job 推回舊判定；舊 Agent 呼叫仍記錄在 `agent_calls`，但結果不可成為現行 Score。
- 相同 `profile_revision` 的 activation 重送不得再次重設狀態或新增重複狀態事件。
- Job 在新 revision 下完成篩選／評分後，重新 ingest 相同 `content_hash` 不得再次重跑。

## 求職信與投遞歷史

Profile 變更不代表使用者撤回投遞意願，也不允許系統重寫已核准內容：

- `letter_requested` 保持使用者已發出的要求；執行中的 Agent 使用工作開始時取得的 Profile snapshot。
- 新 Letter 保存其實際 `profile_revision`。若完成時 active revision 已不同，Letter 仍保留但立即導出 `letter_stale=true`。
- `letter_ready`、既有 approved Letter、`apply_state` 與 apply events 都是歷史事實，不自動回退。
- `letter_failed` 的重試仍需使用者明確操作；重試使用操作當下的 active Profile。
- 本次不提供「以新 Profile 重新生成已核准 Letter」；未來若加入，必須是新的明確使用者動作並保留舊 Letter。

## 安全與隱私

- Profile API 只監聽 loopback，沿用 Bearer token 與 extension origin 驗證。
- service worker 是唯一 API client；104 content script 與頁面不得取得 Profile。
- 完整 Profile、草稿、YAML、薪資、自由文字與驗證 body 不寫入 `chrome.storage`、console、server log、request observer、e2e evidence 或錯誤回應。
- denylist 只存在後端。API 驗證問題只回欄位路徑與安全類別，不回禁詞本身。
- PII 檢核必須與 `profile lint` 共用同一實作，避免 CLI 與 API 規則分岔。
- Profile 寫入檔維持 `0600`；暫存檔、復原檔若存在，也必須採相同權限並於操作完成後清除。
- 測試只使用合成 Profile，不得讀取實際 `profile.yaml` 或輸出其內容。

## 失敗處理

| 情況 | 系統行為 |
|---|---|
| JSON／schema 非法 | `422 profile_invalid`；回安全欄位 issues，不寫檔、不切 revision。 |
| PII 命中 | `422 profile_invalid`；不回 denylist 值，不寫檔。 |
| ETag 不符 | `412 profile_conflict`；保留 UI 草稿，不覆蓋磁碟。 |
| 檔案不可寫 | `500 profile_save_failed`；active snapshot 與 Job revision 不變。 |
| SQLite activation 失敗 | Job revision 與狀態不變；active Profile 不受影響，回安全錯誤供使用者重試。 |
| Profile 缺少 | setup 模式；Profile API 可用，處理型動作回 `409 profile_not_ready`。 |
| Profile YAML 損壞 | invalid 模式；不得用預設值或舊 snapshot 靜默繼續處理。 |
| 更新時 worker 使用舊 snapshot | compare-and-set 拒絕舊結果成為現行判定；新 revision 工作照常排隊。 |
| 重複送出相同內容 | 回既有 revision，`semantic_changed=false`，不重跑。 |

## 測試與驗收

### L1 測試

| 模組 | 必要案例 |
|---|---|
| profile | JSON／YAML round-trip、canonical revision 穩定、語意變更 revision 改變、註解／排版不改 revision、unknown field 拒絕、PII 拒絕且不落檔、`0600` 與原子替換。 |
| schema/store | migration 保留 legacy rows；Job／Score／Letter／Agent call revision；activation 狀態矩陣；同 revision 冪等；歷史資料不刪；apply state 不變。 |
| pipeline | partial 重新篩選、有全文 Job 重新入隊、daily budget、舊 worker CAS 失敗、同 revision 不重跑、letter 狀態不自動推進。 |
| api | missing／invalid／ready、GET ETag、PUT `If-Match`、412／422／428、token／Origin／CORS PUT、回應不洩漏 Profile 或 denylist。 |
| extension | setup／ready／invalid／conflict、動態陣列表單、離頁提醒、無 autosave、儲存統計、Profile 不進 storage／log。 |

### L2／E2E 驗收

`e2e-mock` 增加合成 Profile 流程：

1. 以不存在的 Profile 啟動 `serve`，API 可連線且 worker 暫停。
2. 從 extension 建立合法 Profile，磁碟產生 `0600` YAML，worker 不重啟即啟用。
3. 建立 filtered／scored／shortlisted／letter-ready／applied 等合成 Job。
4. 修改會影響篩選與評分的欄位，確認儲存後既有 Job 保留舊 revision 並顯示待重評；按下更新後 eligible Job 才使用新 revision 重新處理，letter／apply 歷史不變。
5. 重送相同 Profile，確認 revision 與 Agent 呼叫數不變。
6. 模擬外部修改 YAML，舊 ETag 儲存回 412 且磁碟未被覆蓋。
7. 送入 PII 與非法欄位，確認 UI 顯示安全錯誤，log／evidence 不含 payload。
8. 在 worker 執行中更新 Profile，確認舊 revision 結果不會成為現行 Score。

Chrome 人工 gate 使用合成資料核對完整表單、窄幅 Side Panel 入口、全頁編輯、鍵盤操作、錯誤聚焦、衝突提示與 light／dark 主題。驗收不得載入實際使用者 Profile。

## Canonical 落點

本文件核准後，先把最新狀態就地寫入下列 canonical 文件，再開始實作：

| 文件 | 應寫入的最新狀態 |
|---|---|
| [PRD](../PRD.md) | R1 Profile 建立／編輯、使用者明確儲存、revision 與重新評估；R6 extension 入口；setup 流程；非目標與 Roadmap 邊界。 |
| [產品 Roadmap](../roadmap.md) | 區分本機單 Profile extension 編輯器與未來 SaaS Web Profile／多租戶能力。 |
| [系統設計](../design.md) | YAML 真相、Profile provider、setup 模式、revision 資料流、模組責任與重新處理邊界。 |
| [Profile 設計](../designs/design-profile.md) | JSON/YAML schema、strict decode、PII、canonical serialization、ETag、revision、原子寫入與 provider。 |
| [Schema 設計](../designs/design-schema.md) | Job／Score／Letter／Agent call revision 欄位、migration、stale 導出與 activation 狀態矩陣。 |
| [Pipeline 設計](../designs/design-pipeline.md) | revision-aware snapshot、activation、partial／full 重新處理、CAS、成本預算與 letter 保護。 |
| [Agents 設計](../designs/design-agents.md) | Agent call 與產出記錄實際 Profile revision。 |
| [API 設計](../designs/design-api.md) | Profile GET／PUT、ETag、CORS、setup mode、Job stale 欄位與安全錯誤。 |
| [Extension 設計](../designs/design-extension.md) | Side Panel Profile 卡片、全頁表單、狀態、衝突處理、隱私與無障礙。 |
| [Profile 測試](../tests/test-profile.md) | serialization、revision、strict schema、PII 與檔案安全案例。 |
| [Schema 測試](../tests/test-schema.md) | migration、revision 歷史、activation 與 apply／letter 保護案例。 |
| [Pipeline 測試](../tests/test-pipeline.md) | requeue、partial、budget、CAS、冪等與 in-flight 行為。 |
| [API 測試](../tests/test-api.md) | Profile routes、setup、ETag、CORS、stale viewmodel 與錯誤安全。 |
| [Extension 測試](../tests/test-extension.md) | 表單、狀態、conflict、storage/log 禁止與 service worker 路由。 |
| [整合驗收](../verify.md) | missing→create、立即生效、revision 變更、重新處理、相同內容冪等與競態案例。 |
| [Extension 操作手冊](../guides/runbook-extension.md) | Profile 建立／編輯、儲存衝突、重新評分與 stale 標示操作。 |
| [開發指南](../../AGENTS.md) | Profile UI、revision 與重新處理的專案現況和維護索引。 |

## 實作進度

- [x] 撰寫本 change 草案供人工審核。
- [x] 人工核准本 change 文件。
- [x] 依「Canonical 落點」更新需求、設計、測試、驗收、操作手冊與開發指南。
- [x] 實作 Profile canonical serialization、PII 欄位問題、ETag 與 revision provider。
- [x] 實作 schema migration、revision-aware store query／CAS 與 Profile activation。
- [x] 實作 setup mode、Profile API 與 runtime 即時切換。
- [x] 實作 extension Profile 卡片與全頁編輯器。
- [x] 完成 L1、`e2e-mock` 及既有自動化回歸驗證。
- [ ] 完成 Chrome 人工 gate 與正式發布。

## 已知殘留限制

- revision 不保存舊 Profile 內容；歷史 Score 或 Letter 可證明屬於哪個 revision，但無法由系統還原當時完整 Profile。
- UI 儲存會 canonicalize YAML，人工註解與排版可能消失；需要保留的說明應成為 schema 欄位，而非註解。
- YAML 語法已損壞時，UI 不提供強制覆蓋；需先由檔案系統修復或移走損壞檔案，以避免不可逆資料遺失。
- `letter_ready`／apply 歷史不自動重跑，即使其 Score 或 Letter 已相對 active Profile stale；本次只呈現 stale 狀態。
- 使用者手動更新大量過時職缺時，既有每日預算會限制成本與速率，結果可能跨多個台北日才全部收斂。
