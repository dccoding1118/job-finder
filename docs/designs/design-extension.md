# 模組設計 — extension（Chrome MV3 Side Panel 與 104 半被動擷取）

對應需求：R6、R9。Chrome MV3 extension 是 MVP 唯一的使用者前端：原生 Side Panel 提供儀表板，104 content script 提供半被動擷取與列表快速判定。B4 交付 Side Panel；B5 交付 104 擷取組。

## 1. 職責邊界

- 原生 Side Panel 提供目前職缺、待看清單、推薦職缺、篩選、JD／評分對照、求職信生成入口與複製、投遞狀態、手動 run 與 Run 歷史；toolbar action 只負責開啟 Side Panel，不顯示 popup。
- Options 儲存 localhost API endpoint 與 token；service worker 是唯一 API client，統一加入認證、處理錯誤與轉送訊息。
- content script 只處理使用者已載入的 104 搜尋結果、通知頁與職缺內頁；列表頁顯示小型判定標記，內頁把擷取結果作為目前分頁 context 提供給 Side Panel，不注入完整評分 overlay。
- 後端解析、狀態轉換、判定導出、評分與信件生成分別屬 crawler、store、api、pipeline、agents；插件不重複實作業務規則——**判定一律取用 API 回傳的 `verdict`，不自行從 `process_state` 或分數推導**。
- **不做**：背景自動開分頁、批次抓取、任何非使用者導覽觸發的對 104 請求、繞過任何防護。定位等同剪藏工具，只記錄使用者本人看到的內容。

## 2. 元件與資料流

| 元件 | 位置 | 職責 |
|---|---|---|
| Side Panel | `extension/dashboard/` | R6 儀表板；向 service worker 要求目前分頁 context、Job／Run 資料與動作 |
| Options | `extension/options/` | 驗證並儲存 API endpoint 與 token；不記錄 JD 或求職信 |
| service worker | `extension/service-worker.js` | API gateway、訊息協調、認證 header、錯誤標準化 |
| list content script | `extension/content/list.js` | 僅在使用者載入 104 搜尋／通知頁時擷取可見項目、顯示 status badge |
| job content script | `extension/content/job.js` | 僅在使用者載入 104 職缺頁時擷取 JSON-LD／DOM 素材，回報目前分頁 context |

Side Panel 與 content script 只透過 `chrome.runtime.sendMessage` 交給 service worker；只有 service worker 向 [API](design-api.md) 發送 request。因此 104 頁面無法讀取 API token，且所有後端請求都可套用同一個逾時、錯誤與重送策略。Side Panel 查詢目前職缺時，由 service worker 找出 active tab，再向該 tab 的 content script 取得記憶體中的 capture context；context 只含頁面類型、擷取狀態與 Job ID，不含 token，也不持久化 JD 或求職信。

manifest 以固定 key 產生穩定 unpacked extension ID，最低支援 Chrome 114，供 API 設定與隔離 Chromium 自動模擬使用。Chromium 的 MV3 privileged fetch 可能不送 `Origin`；service worker 不嘗試設定瀏覽器限制的 `Origin` header，而以 extension storage 中可撤換的 Bearer token 驗證。API 對任何實際帶入的錯誤 Origin 仍拒絕。自動模擬不代表實際 Chrome 安裝、權限或相容性通過。

## 3. Side Panel 儀表板（B4）

Side Panel 固定提供「目前職缺、待看、推薦、系統」四個頁籤。sticky header 顯示品牌、API 連線、目前頁面脈絡、主題切換與重新整理；內容在 320px 以上維持單欄；目前職缺的主要動作置於 sticky action dock。toolbar action 以 `chrome.sidePanel.setPanelBehavior({openPanelOnActionClick: true})` 開啟 Side Panel。

| 頁籤 | 資料 | 使用者動作 |
|---|---|---|
| 目前職缺 | active tab capture context 對應的 Job 詳情；或使用者從推薦清單選取的 Job | 檢視判定、總分／命中條件、理由、五維、JD、求職信與投遞狀態；開啟原始連結 |
| 待看 | `discovered` Job 的職稱、公司、薪資、地點與原始連結 | 以明確使用者動作開啟原始頁面 |
| 推薦 | 預設 `verdict=recommended` 且按 Match Score 排序的清單；進階篩選可切換判定、處理、投遞與來源 | 選取一筆後切到目前職缺；不自動開啟原始頁面 |
| 系統 | API 連線狀態、手動 run、Run 歷史 | 觸發 run；檢視抓取事實與現行判定分布 |

清單預設顯示判定為推薦的職缺，依最新 Match Score 由高至低排序。無分數或投遞狀態時顯示「—」。複製使用 `navigator.clipboard.writeText`，manifest 僅為此功能宣告 `clipboardRead`／`clipboardWrite`；失敗時保留可選取文字並顯示說明。五維分數以技能、領域、資歷、條件、方向與總分呈現；Run 統計逐項顯示，不得顯示為物件字串。判定與狀態不得只以顏色表達，須同時有文字或圖示；所有控制項可用鍵盤操作並有可辨識名稱。

主題以 `data-theme="light|dark"` 套用 `ui-design/DESIGN.md` 的語意 token。使用者選擇存於 extension local storage 的 `theme` 欄位；切換只改視覺，不重設 active tab、選取 Job、表單或 busy 狀態。JD、求職信與 Job response 只存在當次頁面記憶體，離線時可繼續閱讀，但不得寫入 extension storage。

**求職信生成入口**（PRD R5.0、R6.8）：對照區依 API 回傳的 `letter_state` 決定呈現——`none` 顯示「產生求職信」按鈕；按下後送出 `POST /api/v1/jobs/{id}/letter`，立即轉為處理中並停用按鈕（回應為受理，不等待完成）；`requested` 顯示處理中與說明「下一輪執行完成」；`ready` 顯示求職信與複製；`failed` 顯示未過審與「再次產生」。生成結果由使用者重新整理或下次載入時取得，插件不得為此輪詢高頻請求。

## 4. 104 擷取與目前分頁 context（B5）

| 模式 | 觸發頁面 | 擷取內容 | API 行為 |
|---|---|---|---|
| 列表收割 | 搜尋結果頁、職缺通知頁 | 每筆可見項目的 external_id、url、職稱、公司、薪資、地區 | `POST /api/v1/capture/list` → 既有 Job 直接回現行判定；新職缺 → 可用條件篩選 → `discovered` ∣ `filtered_out`。回傳每筆 verdict 供就地標記 |
| 內頁擷取 | 職缺內頁載入完成 | JSON-LD `JobPosting` 優先、DOM 片段備援 | `POST /api/v1/capture/job` → 補全文 → 條件篩選；同步回 `unfit` 或快取評分，否則回 `pending_score` 由 Side Panel 輪詢（§4.2） |

### 4.0 頁面結構與掛載點

三個頁面的 URL pattern 與容器如下；兩個列表頁的 DOM 結構**不同**，各自一套 selector，正規化後才送同一個 `capture/list`。

| 頁面 | URL pattern | 項目容器 | 載入模型 |
|---|---|---|---|
| 搜尋結果頁 | `https://www.104.com.tw/jobs/search/*` | `.job-summary` | Vue 虛擬捲動（`.vue-recycle-scroller`）：DOM 只保留可視項目，捲離即回收；下滾自動載入次頁 |
| 職缺通知頁 | `https://pda.104.com.tw/work/mate/list/*` | `.job-list-container[pagenumber]` | 實 DOM 全留，項目帶 `pagenumber`／`ispagelastitem`；分頁為真實 href |
| 職缺內頁 | `https://www.104.com.tw/job/*` | `script[type="application/ld+json"]` 內的 `JobPosting` | 靜態 |

**搜尋頁必須以 MutationObserver 持續收割**，不可在載入完成時掃一次了事——虛擬捲動會回收捲離的節點，單次掃描只拿得到當下可視的少數項目。通知頁無此問題，但仍以同一 observer 機制涵蓋下滾載入的次頁項目。

搜尋頁**第一筆是廣告職缺**（`hotjob_chr_exp`）而非搜尋結果，必須排除；判別依據是連結 query 的 `jobsource` 前綴為 `hotjob`，佐證特徵為日期欄是 icon 而非日期、且缺 `.job-summary__close` 隱藏鈕。

搜尋頁分頁列的「共 N 筆」是**當下已渲染筆數**而非結果總數（頁數才是正確的），不得據以判斷收割是否完整；通知頁頂列的「共 N 筆」則為真實總數。收割完整性一律以實際送出的項目為準，插件不比對任何頁面宣告的筆數。

| 掛載點 | 位置 |
|---|---|
| 列表標記 | 各項目容器內，Shadow DOM 掛載 |
| Side Panel | Chrome 原生 Side Panel；由 active tab 的 content script 提供 Job ID |

### 4.1 列表就地標記（PRD R9.1）

每筆已收割的項目依回傳的 `verdict` 掛上標記，讓使用者在清單上直接看出取捨：

| `verdict` | 標記 | 附帶資訊 |
|---|---|---|
| `unfit` | 紅底＋排除圖示，項目降低視覺權重 | 命中的條件名稱（`filter_hits`） |
| `recommended` | 綠底＋推薦圖示 | 總分 |
| `not_recommended` | 灰底＋淡化 | 總分 |
| `pending_detail` | 中性標記「待看」 | 提示點開內頁可取得完整評估 |

`unfit` 與 `pending_detail` 兩者，是清單頁以「較少欄位」能得到的全部結論——列表沒有 JD 全文，不足以支撐五維評分，插件不得在此模式顯示或臆測分數。`recommended` 與 `not_recommended` 只會出現在**已存在於資料庫**的職缺（先前批次抓取或已點開過內頁），屬直接取用既有判定，不重跑任何階段。

標記須同時有圖示或文字，不只靠底色（無障礙）；收割與標記皆為使用者導覽該頁面所觸發，插件不因標記結果自動開啟任何頁面。

### 4.2 內頁 Side Panel（PRD R9.2）

評分是非同步的（[design-pipeline](design-pipeline.md) §2.2），因此 Side Panel 依 `capture/job` 回應與後續輪詢的 `verdict` 呈現：

| `verdict` | 呈現 | 何時出現 |
|---|---|---|
| `unfit` | 不適合＋命中的 `filter_hits` | `capture/job` 同步回應即得 |
| `recommended`／`not_recommended`（`cached`） | 總分、五維與 reason，標示「快取」 | `capture/job` 同步回應即得 |
| `pending_score` | 評分中 | `capture/job` 同步回應；轉入輪詢 |
| `pending_score` ＋ `budget_exhausted` | 已達今日評分上限 | 同上；不輪詢 |
| `recommended`／`not_recommended` | 總分、五維與 reason | 輪詢取得 |

**條件篩選不耗用 LLM，因此 `unfit` 一律是同步的**——被排除的職缺當場就有結論，不進入輪詢。只有通過篩選、確實要送 LLM 的職缺才等待。

`pending_score` 時 Side Panel 以 3 秒間隔輪詢 `GET /api/v1/jobs/{id}`，至 verdict 轉為終態或達 5 分鐘上限為止；逾時後停止輪詢並顯示「仍在處理，可稍後重新整理」。輪詢對象是 localhost API，不觸及 104 也不觸發 LLM，因此與 §3 求職信「不得為此輪詢」的規則不衝突——該規則針對的是使用者未必在看的清單場景，而目前職缺畫面是使用者正在等待的互動情境。分頁關閉不影響後端消化，重新開啟時 `capture/job` 會直接命中快取。

使用流程：使用者以 `jobfinder queries urls --source 104` 產生巡邏 URL，或開啟 104 通知頁；列表收割後就地看到判定，不適合者當場排除，待看者在 Side Panel 的待看清單累積；使用者點開一筆原始連結時才由內頁 script 擷取，Side Panel 顯示完整評估與下一步。email 僅作提醒，不解析。

**頁面結構的取樣方式**：104 的搜尋頁、通知頁與內頁結構由**使用者本人瀏覽時人工取樣**取得——使用者開啟自己要看的頁面，複製一筆項目的容器片段與內頁的 JSON-LD 供結構分析。不做伺服器端抓取，也不為取樣以插件或腳本自動開頁（[design-crawler](design-crawler.md) §1 判準）。取樣素材改寫為**結構仿真、內容合成**的 fixture；真實 JD 與公司資料不進 repo。欄位映射屬 104 解析器，見 [design-crawler](design-crawler.md) §2.2。

## 5. 設定、權限與失敗處理

- manifest 採 MV3，B4 宣告 `storage`、`sidePanel`、`clipboardRead`、`clipboardWrite` 與 loopback API host permission；B5 另宣告 §4.0 三個 104 URL pattern 的 content scripts（含 `pda.104.com.tw` 通知頁）。不得請求未使用權限。
- API endpoint 預設 `http://127.0.0.1:8686`；Options 儲存前驗證為 `http` loopback URL 或使用者建立 SSH forward 後的 loopback URL。token 使用 `chrome.storage.local`，不顯示於 Side Panel 或 log。
- list／job 擷取失敗、API 離線或 token 無效時，Side Panel 顯示可理解的錯誤與使用者觸發的重送；不得背景高頻重試或暫存 JD。
- 列表標記採 Shadow DOM，避免與 104 頁面樣式互相污染。

## 6. 測試與交付物

- Side Panel、Options、service worker 訊息與 API error mapping 以 mock API 單元測試；不使用真實 JD、Profile、token 或 104 頁面。
- B4 隔離 Chromium 自動模擬使用固定 unpacked ID，保存不含 token/body 的 request 摘要與 Side Panel screenshot，並驗證 light／dark 主題、來源／流程／投遞／判定篩選、五維對照、求職信生成要求、clipboard、SQLite apply 回寫與結構化 Run history；實際 Chrome 另走人工 gate。
- 104 script 的實機驗收見 [verify](../verify.md) V5；必須由驗收者載入測試版插件，以使用者導覽完成列表就地標記、內頁 context 與待看流程。
- 交付 `extension/` 的 MV3 manifest、Side Panel、Options、service worker、content scripts、列表標記與測試；API capture endpoint 與 crawler `parse104/` 分屬各自模組交付。

## 7. 待決

（無。）
