# 模組設計 — extension（Chrome MV3 Side Panel 與半被動擷取）

對應需求：R1、R6、R9。Chrome MV3 extension 是 MVP 唯一的使用者前端：原生 Side Panel 提供儀表板與 Profile 入口，全頁 editor 編輯 Profile，104 與 Cake 的 content script 提供半被動擷取與列表快速判定。

## 1. 職責邊界

- 原生 Side Panel 提供目前職缺、待看清單、推薦職缺、篩選、JD／評分對照、求職信生成入口與複製、投遞狀態、手動 run、Run 歷史與 Profile 狀態卡；toolbar action 只負責開啟 Side Panel，不顯示 popup。
- extension 自有全頁編輯器以結構化表單建立／編輯單一 Profile；不解析 YAML、不自動儲存，也不保存草稿副本。
- Options 儲存 localhost API endpoint 與 token；service worker 是唯一 API client，統一加入認證、處理錯誤與轉送訊息。
- content script 只處理使用者已載入的 104 與 Cake 列表頁與職缺內頁；列表頁顯示小型判定標記，內頁把擷取結果作為目前分頁 context 提供給 Side Panel，不注入完整評分 overlay。
- 後端解析、狀態轉換、判定導出、評分與信件生成分別屬 crawler、store、api、pipeline、agents；插件不重複實作業務規則——**判定一律取用 API 回傳的 `verdict`，不自行從 `process_state` 或分數推導**。
- **不做**：背景自動開分頁、批次抓取、任何非使用者導覽觸發的對來源平台請求、繞過任何防護。定位等同剪藏工具，只記錄使用者本人看到的內容。

## 2. 元件與資料流

| 元件 | 位置 | 職責 |
|---|---|---|
| Side Panel | `extension/dashboard/` | R6 儀表板；向 service worker 要求目前分頁 context、Job／Run 資料與動作 |
| Profile editor | `extension/profile/` | extension 全頁結構化表單、ETag 儲存、衝突與驗證問題處理 |
| Options | `extension/options/` | 驗證並儲存 API endpoint 與 token；不記錄 JD 或求職信 |
| service worker | `extension/service-worker.js` | API gateway、訊息協調、認證 header、錯誤標準化 |
| list content script | `extension/content/list-104.js`、`list-cake.js` | 僅在使用者載入該平台列表頁時擷取可見項目、顯示 status badge；平台專屬 selector，送出前正規化為同一組 payload |
| job content script | `extension/content/job-104.js`、`job-cake.js` | 僅在使用者載入該平台職缺頁時擷取素材（104 為 JSON-LD／DOM，Cake 為 `__NEXT_DATA__`），回報目前分頁 context |

Side Panel 與 content script 只透過 `chrome.runtime.sendMessage` 交給 service worker；只有 service worker 向 [API](design-api.md) 發送 request。因此來源平台頁面無法讀取 API token，且所有後端請求都可套用同一個逾時、錯誤與重送策略。Side Panel 查詢目前職缺時，由 service worker 找出 active tab，再向該 tab 的 content script 取得記憶體中的 capture context；context 只含頁面類型、擷取狀態與 Job ID，不含 token，也不持久化 JD 或求職信。

manifest 以固定 key 產生穩定 unpacked extension ID，最低支援 Chrome 114，供 API 設定與隔離 Chromium 自動模擬使用。Chromium 的 MV3 privileged fetch 可能不送 `Origin`；service worker 不嘗試設定瀏覽器限制的 `Origin` header，而以 extension storage 中可撤換的 Bearer token 驗證。API 對任何實際帶入的錯誤 Origin 仍拒絕。自動模擬不代表實際 Chrome 安裝、權限或相容性通過。

## 3. Side Panel 儀表板（B4）

「系統」頁的 Profile 卡依 API 呈現 `missing`／`invalid`／`ready`：missing 提供「開始設定」，invalid 顯示安全問題摘要與人工修復提示，ready 顯示物化年資、技能數、經歷數、方向與兩個 revision 短碼。卡片開啟 extension 自有的全頁編輯器，不建立獨立 Web UI。

編輯器依 Profile 六個區段依序呈現：搜尋條件（方向與關鍵字）、硬性條件（薪資下限、地區（下拉選單，台灣各直轄市與縣市＋台灣＋海外，另有「＋ 全台」快捷鈕一次填入海外以外的全部地區）、遠端四值、工作型態（四選項複選）、排除產業／職稱／內文／公司）、軟性偏好（目標薪資、想做與不想做的工作內容、期許產業）、工作經歷（一份工作一列：產業、年資、是否管理職、是否計入加總、技能、角色與量化成就）、資格（學歷與狀態、技能與熟練度、證照與狀態、語言與程度；技能、證照與語言各為一項一列，便於一眼看出漏了什麼）、誠實邊界；陣列可新增、刪除與排序。表單以文案說明每區段影響哪一關：硬性條件決定「適合與否」、軟性偏好決定「推不推薦」。

`derived` 的年資加總為唯讀，隨經歷列表即時重算顯示，不可手動編輯。`qualifications.skills` 提供「從經歷載入」按鈕，把各段經歷列到的技能補進總表（預設熟練度 `expert`），帶入項標示來源；已在表上的技能不重複帶入且熟練度不被改寫，故重複按下不會覆蓋使用者調過的值。使用者再自行調整熟練度並補上工作外取得的技能。欄位使用業務用語，前端即時檢查只提供提示，後端驗證是唯一權威。

- 草稿只存在 editor page 記憶體；重新整理、關閉或 extension reload 後不保留，有未儲存內容時離頁確認。
- 儲存確認說明新擷取職缺會立即使用新版、既有職缺保留原評分；確認後才以 GET 的 ETag 送出 `PUT`。
- 儲存成功顯示 revision 短碼；`412 profile_conflict` 保留草稿並要求重新載入，不提供強制覆蓋。
- Job 清單與詳情將分數四捨五入為整數，並以綠色「最新」或黃色「待重評」分別呈現篩選版本（`filter_stale`）與評分版本（`score_stale`）；Letter stale 另行提示，不改 verdict／apply。
- 動態陣列新增後，焦點移至該按鈕所屬區塊的新欄位並以最近距離捲入畫面，不跳到其他同型清單。

Side Panel 固定提供「目前職缺、待看、推薦、系統」四個頁籤。sticky header 顯示品牌、API 連線、目前頁面脈絡、主題切換與重新整理；內容在 320px 以上維持單欄；目前職缺的主要動作置於 sticky action dock。toolbar action 以 `chrome.sidePanel.setPanelBehavior({openPanelOnActionClick: true})` 開啟 Side Panel。

| 頁籤 | 資料 | 使用者動作 |
|---|---|---|
| 目前職缺 | active tab capture context 對應的 Job 詳情；或使用者從推薦清單選取的 Job | 檢視判定、總分／篩選逐條結論、理由、四維、JD、求職信與投遞狀態；檢視同一職缺的其他來源連結；馬上處理或重新處理這一筆；開啟原始連結 |
| 待看 | `discovered` Job 的職稱、公司、薪資、地點與原始連結 | 以明確使用者動作開啟原始頁面補全文；以「載入更多」追加下一頁 |
| 推薦 | 預設 `verdict=recommended` 且按 Match Score 排序的清單；進階篩選可切換判定、處理、投遞與來源 | 選取一筆後切到目前職缺；不自動開啟原始頁面 |
| 系統 | 依序呈現「連線與設定」「Profile」「自動篩選與評分」「疑似重複」「自動與手動批次」「處理進度」「Agent 呼叫紀錄」「批次歷程」群組 | 開啟 Options、開始設定／編輯 Profile、更新過時評分、開關自動篩選與評分、查看每日 08:30 排程、手動抓取、檢視待處理量、裁決疑似重複職缺、檢視 Agent 呼叫結果與歷程 |

清單預設顯示判定為推薦的職缺，依最新 Match Score 由高至低排序。推薦與待看清單各自讀取 API 第一頁；回應有 `next_cursor` 時在該清單底部顯示「載入更多」，使用者按下後帶回 cursor（推薦清單並帶回相同篩選）追加下一頁，已載入項目不被覆蓋、重複 id 不重複追加。請求期間停用該按鈕；末頁不顯示按鈕。追加完成不得改變目前捲動位置。變更篩選或重新整理會清除兩份 cursor 並從第一頁載入，過期分頁回應不得寫回新篩選結果。四個頁籤在頁面生命週期內各自保存捲動位置；從推薦清單選取職缺後切至目前職缺，返回推薦頁時恢復原位置，待看頁在頁籤往返後同樣恢復原位置。使用者開啟過的推薦職缺於當次頁面標示「已看」，不持久化。無分數或投遞狀態時顯示「—」。複製使用 `navigator.clipboard.writeText`，manifest 僅為此功能宣告 `clipboardRead`／`clipboardWrite`；失敗時保留可選取文字並顯示說明。四維分數以工作內容、待遇與制度、加分條件、產業與總分呈現；判不適合或待看者以逐條清單呈現未通過與資訊不足的條件（必備與加分分區），不只顯示條件名稱字串。Run 統計逐項顯示，不得顯示為物件字串。判定與狀態不得只以顏色表達，須同時有文字或圖示；所有控制項可用鍵盤操作並有可辨識名稱。

主題以 `data-theme="light|dark"` 套用 `ui-design/DESIGN.md` 的語意 token。使用者選擇存於 extension local storage 的 `theme` 欄位；切換只改視覺，不重設 active tab、選取 Job、表單或 busy 狀態。JD、求職信與 Job response 只存在當次頁面記憶體，離線時可繼續閱讀，但不得寫入 extension storage。

**求職信生成入口**（PRD R5.0、R6.8）：對照區依 API 回傳的 `letter_state` 決定呈現——`none` 顯示「產生求職信」按鈕；按下後送出 `POST /api/v1/jobs/{id}/letter`，立即轉為處理中並停用按鈕（回應為受理，不等待完成）；`requested` 顯示處理中與說明「後端完成後可重新載入」；`ready` 顯示求職信與複製，`letters.status` 為 `finalized` 時另標示該版已達輪數上限、未經最後一次審查，建議投遞前自行過目；`failed` 顯示產製失敗與「再次產生」，失敗原因（Agent 呼叫失敗或最終版未通過內容防線）於系統頁的 Agent 呼叫紀錄可查。生成結果由使用者重新整理或下次載入時取得，插件不得為此輪詢高頻請求。

**單筆重新處理**：`process_state` 為 `filtered_out`、`scored` 或 `shortlisted` 且連線正常時，action dock 顯示次要按鈕「重新處理」；按下後送出 `POST /api/v1/jobs/{id}/reprocess`，請求期間停用按鈕，成功後該筆立即呈現為篩選中並依 §4.2 輪詢結果。判定為不適合的職缺同樣提供此入口——使用者不同意的判定既可能出在評分關，也可能出在篩選關。處理中的職缺與求職信階段的職缺不顯示此按鈕；後端拒絕時以 toast 說明，不改變畫面狀態。


**單筆馬上處理**：`verdict` 為 `pending_screen` 或 `pending_score` 時，action dock 的主要按鈕是「馬上處理」；按下後送出 `POST /api/v1/jobs/{id}/process`，該筆立即呈現為正在篩選／正在評分並依 §4.2 輪詢結果。等待中的職缺沒有可推翻的判定，因此它取代（而非並列）「重新處理」；求職信階段與已有判定的職缺仍只顯示「重新處理」。此按鈕在自動處理關閉或當日額度用盡時同樣可用——那兩者限制的是自動消化，不是使用者對這一筆的要求；按下後即使額度已滿也照常輪詢。`resident_worker` 為偽（設定檔停用常駐 worker）時按鈕停用並說明該筆等待手動批次。等待狀態的說明文字須說出它為何在等：正在背景處理、自動處理已關閉、今日額度已用盡，或已插隊處理中。

**自動篩選與評分開關**：系統頁依 `GET /api/v1/status` 的 `settings` 呈現目前狀態與說明，按鈕送出 `PUT /api/v1/settings`；關閉後新職缺仍照常收集，只是停在待篩選／待評分，求職信與收集不受影響。`resident_worker` 為偽時開關停用並說明篩選與評分改由 CLI 批次驅動。

**同一職缺的其他來源**（PRD R2.8、R6.12）：Job 回應帶 `group` 時，目前職缺於原始連結旁列出同群其他來源的平台與連結，供使用者選擇從哪個平台投遞；不重複顯示評分與求職信——那些屬於整個 group，只有一份。

**疑似重複**：系統頁讀取 `GET /api/v1/duplicates`，逐筆併排兩邊的職稱、公司、地區、來源與相似度，提供「合併」與「忽略」；合併後清單即少一筆，忽略後不再出現。合併與忽略都是使用者的明確動作，插件不自動裁決。已合併的職缺可於目前職缺以「取消合併」還原。

**處理進度**：系統頁讀取 `GET /api/v1/status`，以待補全文、待篩選、待看（資訊不足）、待評分、信件產生中五項筆數呈現常駐 worker 的待消化量，並顯示當日篩選與評分額度餘額（未設上限時顯示「不限」）。Agent 呼叫紀錄逐筆顯示角色、runner（有 model 時併列）、該次輸入與輸出 token 與費用、呼叫成功或未完成、職缺 ID 與耗時秒數；費用為 0 時不顯示金額，避免把「runner 不自報費用」讀成「這次免費」。未完成者另以中文說明後端回傳的失敗類別並附截斷回應。低分或不推薦的評分是成功呼叫，不得呈現為失敗。

**每日 Token 用量**：系統頁以 `GET /api/v1/status` 的 `agent_usage_daily` 獨立成區，依日期新到舊分卡；卡首為日期與當日 token 合計（有費用時併列金額），卡內每列是一組 runner／model，顯示呼叫次數、輸入與輸出 token、該組合計 token 與費用。日界為台北時間 00:00，與每日額度重置一致，區塊標題即說明此事。尚無呼叫時顯示空狀態而非 0。

處理進度、Agent 呼叫紀錄與每日用量只在重新整理或載入時取得，不輪詢。

## 4. 半被動擷取與目前分頁 context（104：B5；Cake：B6）

| 模式 | 觸發頁面 | 擷取內容 | API 行為 |
|---|---|---|---|
| 列表收割 | 104 搜尋結果頁／職缺通知頁、Cake 搜尋與職類列表頁 | 每筆可見項目的 external_id、url、職稱、公司、薪資、地區 | `POST /api/v1/capture/list` → 既有 Job 直接回現行判定；新職缺 → 可用的結構化硬規則 → `discovered` ∣ `filtered_out`。回傳每筆 verdict 供就地標記 |
| 內頁擷取 | 職缺內頁載入完成 | 104：JSON-LD `JobPosting` 優先、DOM 片段備援；Cake：`__NEXT_DATA__` 的 `pageProps.job` | `POST /api/v1/capture/job` → 補全文 → 結構化硬規則；同步回 `unfit` 或快取評分，否則回 `pending_screen` 由 Side Panel 輪詢語意篩選與評分結果（§4.2） |

payload 一律帶 `source`（`104` / `cake`），由 API 分派至對應解析器（[design-crawler](design-crawler.md) §2.1、§4）。判定回傳與就地標記兩平台共用同一套語意；插件不因平台不同而改變呈現規則。

### 4.0 104 頁面結構與掛載點

三個 104 頁面的 URL pattern 與容器如下；兩個列表頁的 DOM 結構**不同**，各自一套 selector，正規化後才送同一個 `capture/list`。

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

### 4.0.1 Cake 頁面結構與掛載點（B6）

Cake 是單頁應用（SPA）：使用者從列表頁點進職缺內頁**不會重新載入文件**，只改 URL 並在原文件內置換內容。Chrome 的 content script 每份文件只注入一次，因此整站以**單一注入**掛載，由路由模組依當前 URL 決定模式，而非依「Chrome 注入了哪支腳本」。

| 頁面 | URL pattern | 模式 | 資料來源 | 載入模型 |
|---|---|---|---|---|
| 搜尋／職類列表頁 | `/jobs`、`/jobs/*` | list | `script#__NEXT_DATA__` 優先、DOM 收割備援 | 首次載入為 SSR，頁內改條件或翻頁時結果由前端注入 |
| 職缺內頁 | `/companies/*/jobs/*` | job | 渲染後的 DOM | 由列表 soft navigation 進入，或直接載入 |
| 其他 Cake 頁面 | 上述以外 | 無 | 不讀取任何內容 | — |

**掛載範圍與模式路由**：`content_scripts` 的 match 為 `https://www.cake.me/*`，載入順序為 `mark.js`、`nav.js`、`cake-list.js`、`cake-job.js`、`cake.js`。兩支模式腳本只註冊自己的 `start`／`stop`，`cake.js` 是唯一持有 page context、回應 `get-page-context` 的一方。`nav.js` 包裝 `history.pushState`／`replaceState` 並監聽 `popstate`／`hashchange`，把 SPA 換頁通知路由模組；換到不同模式時先 `stop()` 舊模式（停掉 observer 與 timer）再啟動新模式，換頁但模式不變時不重啟——列表模式本來就會跟著頁內注入的項目繼續收割。落在無模式的頁面時，page context 為 `unsupported`，不讀取也不送出任何內容。

**職缺內頁只能讀 DOM**：Cake 的內頁**不帶自己的 listing 狀態**——頁面上的 `__NEXT_DATA__` 描述的是使用者進來前那個列表畫面，soft navigation 也不會更新它。因此內頁一律以渲染後的 DOM 為唯一來源，不讀 `__NEXT_DATA__`。

**列表以 `__NEXT_DATA__` 為主、DOM 為輔**：`__NEXT_DATA__` 是結構化資料，欄位比 DOM 穩定得多，且 content script 可直接讀取該 `<script>` 的文字節點，不需注入 page world。但它只反映**首次 SSR 的那組條件**——使用者在頁內改條件或翻頁後不會更新。因此每次收割先比對 `props.pageProps.ssr.search` 與目前 `location.search`。該欄位是**物件**（`query`、`page`、`filters`），不是 URL 的 query string，比對一律保守：

- URL 的條件只有 `query` 與 `page`、且與 `ssr.search` 的同名值一致（`filters` 為空）⇒ 直接取 `initialState.jobSearch.entityByPathId`。
- 其餘一切情況（帶任何其他參數、`filters` 非空、缺 `__NEXT_DATA__`）⇒ 退回 DOM 收割，並以 MutationObserver 涵蓋後續注入的項目。使用者一旦在 Cake 加上搜尋條件即落入此情況，因此實務上走的是 DOM 收割。

`filters` 與 URL 參數之間的映射不得靠推測補齊：條件對不上就當作過時，否則會把別組條件的職缺歸給畫面上這組搜尋。

**DOM selector 不可依賴 class 常數**：Cake 的 class name 帶 build hash（`JobSearchItem-module-scss-module___szW4W__jobTitle`），每次前端改版即失效。定位一律以穩定屬性為主——職缺連結取 `a[href^="/companies/"][href*="/jobs/"]`，項目容器取該連結**最外層**的 `[class*="JobSearchItem"]` 祖先（前綴語意穩定，只有 hash 段會變）。取最外層而非最近者：該前綴掛在項目內幾乎每個節點上，連結所在的標題節點也帶它，停在第一個符合者會只框住標題，公司名與標籤都讀不到，整筆項目因缺欄位而無法擷取。

**同一職缺可能在頁面上被連結多處**：Cake 除結果清單外，另有推薦與最近瀏覽等區塊連向同一職缺，且後者出現的正是使用者已點開過、因此已有後端判定的職缺。收割因此以 external_id 收集**該筆出現過的每個節點**並全部標記——只標記第一個出現處，會讓已知職缺的標記落在非結果區塊，結果清單那列反而沒有標記。欄位讀取則以**結果項目**（有 `[class*="JobSearchItem"]` 祖先者）優先：只有結果項目帶得出公司名、地區與待遇。

公司名取項目內第一個**有文字**的 `a[href^="/companies/"]:not([href*="/jobs/"])`：同一項目內的公司連結出現多次，logo 錨點只含圖片。

**內頁 DOM 收割的定位規則**：同樣不依賴 hash 段，只認元件前綴與角色後綴，並以標籤名消除前綴共用造成的誤配（`…__titleRow` 會被針對 `…__title` 的選擇器命中）。

| 欄位 | 定位 | 備援 |
|---|---|---|
| 職稱 | `h1[class*="JobDescriptionLeftColumn"]` | 頁內第一個 `h1` |
| 公司名 | `a[class*="JobDescriptionLeftColumn"][class*="__name"]` | 第一個有文字的 `a[href^="/companies/"]:not([href*="/jobs/"])` |
| JD 區塊 | `[class*="ContentSection"][class*="contentSection"]`，標題取 `h3[class*="ContentSection"]`、內文取 `:scope > [class*="__content"]` | 無 |
| metadata 行 | `[class*="rightColumn"]`、`[class*="inlineJobMeta"]` 內所有葉節點文字（去重、長度 80 字以內） | 無 |

JD 區塊內文以 `:scope >` 限定直接子層：區塊自身的 class 就帶 content 後綴，不限層級會匹配到自己。區塊標題與內文一併送出並保留，讓評分把「職務需求」讀成需求而非混入描述。

**metadata 不在插件端分派欄位**：Cake 把地點、待遇、遠端形式渲染成位置不固定的自由文字，因此收割只把這些行**原樣**送出，由 API 端以與列表收割相同的規則辨識（見 [design-crawler](design-crawler.md) §4）。職稱、公司名、至少一個有內文的 JD 區塊三者齊備才算可擷取；缺任一項回報擷取失敗，不猜測。

Cake 沒有 104 那種置頂廣告職缺，不需排除規則；`external_id` 由連結路徑的 `{companyPath}/{jobPath}` 組成，與內頁一致。

**頁面結構的取樣方式**：Cake 的結構由**伺服器端對免登入公開頁的單次取樣**取得（`robots.txt` 全開、無條件列表與內頁皆回 200，見 [design-crawler](design-crawler.md) §1）；帶條件的搜尋路徑不取樣、不請求。取樣素材同樣改寫為結構仿真、內容合成的 fixture。

### 4.1 列表就地標記（PRD R9.1）

每筆已收割的項目依回傳的 `verdict` 掛上標記，讓使用者在清單上直接看出取捨：

| `verdict` | 標記 | 附帶資訊 |
|---|---|---|
| `unfit` | 紅底＋排除圖示，項目降低視覺權重 | 未通過的條件名稱（`filter_hits`） |
| `recommended` | 綠底＋推薦圖示 | 總分 |
| `not_recommended` | 紫底＋淡化 | 總分 |
| `pending_detail` | 藍底「待看」 | 提示點開內頁可取得完整評估 |
| `pending_score` | 黃底「處理中」 | 無 |

五種標記各有專屬底色與外框，彼此可一眼區分。

`unfit` 與 `pending_detail` 兩者，是清單頁以「較少欄位」能得到的全部結論——列表沒有 JD 全文，不足以支撐語意篩選與四維評分，插件不得在此模式顯示或臆測分數。`recommended` 與 `not_recommended` 只會出現在**已存在於資料庫**的職缺（先前批次抓取或已點開過內頁），屬直接取用既有判定，不重跑任何階段。

**判定快取的失效**（兩平台共用）：已取得判定的項目在該份文件內以 external_id 快取重用，節點被虛擬捲動回收再出現時直接重貼。分頁自隱藏轉為可見時，該快取與每筆的次數配額一併清除並重新收割——使用者點開內頁會改變該筆的判定，而那發生在另一個分頁，清單自己不會知道。已存在的職缺在 `capture/list` 只被讀取、不重新篩選，因此重問不產生任何 Agent 呼叫。

**送出時機與重試**（兩平台共用）：收割以 MutationObserver 觸發，項目累積成一批後，於**清單停止變動 400ms** 才送出，最長不超過首次變動後 1.5 秒；換頁與改條件是把新結果注入同一份文件，故此延遲確保讀到的是渲染完成的欄位，而非半渲染狀態下缺漏的職稱或公司名。回應未涵蓋的 external_id **不視為已處理**：該筆保留可再問狀態，由後續收割最多再問 2 次（同一份結果最多 3 次）；URL 改變時重置每筆的次數配額。請求失敗不計入次數。若無此重試，任何一次讀取不全都會讓該筆在使用者重新整理前永遠無標記。

標記須同時有圖示或文字，不只靠底色（無障礙）；收割與標記皆為使用者導覽該頁面所觸發，插件不因標記結果自動開啟任何頁面。

### 4.2 內頁 Side Panel（PRD R9.2）

語意篩選與評分都是非同步的（[design-pipeline](design-pipeline.md) §2.2），因此 Side Panel 依 `capture/job` 回應與後續輪詢的 `verdict` 呈現：

| `verdict` | 呈現 | 何時出現 |
|---|---|---|
| `unfit` | 不適合＋未通過的 `filter_hits` | `capture/job` 同步回應即得（結構化條件） |
| `recommended`／`not_recommended`（`cached`） | 總分、四維與 reason，標示「快取」 | `capture/job` 同步回應即得 |
| `pending_score` | 處理中 | `capture/job` 同步回應；轉入輪詢 |
| `pending_score` ＋ `budget_exhausted` | 已達今日上限，並說明可用「馬上處理」單獨處理這一筆 | 同上；未按下「馬上處理」則不輪詢 |
| `unfit` | 不適合＋未通過的條件逐條清單 | 輪詢取得（語意篩選） |
| `pending_screen` | 篩選中 | 擷取當下（尚未完成篩選） |
| `recommended`／`not_recommended` | 總分、四維與 reason | 輪詢取得 |

**結構化硬規則不耗用 LLM，因此該類 `unfit` 一律是同步的**——被排除的職缺當場就有結論，不進入輪詢。只有通過結構化條件、確實要送 LLM 的職缺才等待。

`pending_screen` 與 `pending_score` 時 Side Panel 以 3 秒間隔輪詢 `GET /api/v1/jobs/{id}`，至 verdict 轉為終態或達 5 分鐘上限為止；逾時後停止輪詢、顯示「仍在處理，可稍後重新整理」，並讓該筆回到可再次按下「馬上處理」的狀態。當日額度已用盡或自動處理已關閉時不輪詢——該筆不會自行前進；使用者按下「馬上處理」的那一筆則照常輪詢。輪詢對象是 localhost API，不觸及 104 也不觸發 LLM，因此與 §3 求職信「不得為此輪詢」的規則不衝突——該規則針對的是使用者未必在看的清單場景，而目前職缺畫面是使用者正在等待的互動情境。分頁關閉不影響後端消化，重新開啟時 `capture/job` 會直接命中快取。

使用流程：使用者在 104／Cake 自行設定搜尋條件瀏覽結果，或開啟 104 通知頁；列表收割後就地看到判定，不適合者當場排除，待看者在 Side Panel 的待看清單累積；使用者點開一筆原始連結時才由內頁 script 擷取，Side Panel 顯示完整評估與下一步。email 僅作提醒，不解析。

**頁面結構的取樣方式**：104 的搜尋頁、通知頁與內頁結構由**使用者本人瀏覽時人工取樣**取得——使用者開啟自己要看的頁面，複製一筆項目的容器片段與內頁的 JSON-LD 供結構分析。不做伺服器端抓取，也不為取樣以插件或腳本自動開頁（[design-crawler](design-crawler.md) §1 判準）。取樣素材改寫為**結構仿真、內容合成**的 fixture；真實 JD 與公司資料不進 repo。欄位映射屬 104 解析器，見 [design-crawler](design-crawler.md) §2.2。

## 5. 設定、權限與失敗處理

- manifest 採 MV3，B4 宣告 `storage`、`sidePanel`、`clipboardRead`、`clipboardWrite` 與 loopback API host permission；B5 另宣告 §4.0 三個 104 URL pattern 的 content scripts（含 `pda.104.com.tw` 通知頁）；B6 另宣告 §4.0.1 兩個 Cake URL pattern。不得請求未使用權限。
- API endpoint 預設 `http://127.0.0.1:8686`；Options 儲存前驗證為 `http` loopback URL 或使用者建立 SSH forward 後的 loopback URL。token 使用 `chrome.storage.local`，不顯示於 Side Panel 或 log。
- Profile、草稿、薪資、自由文字與 Profile API body 不得寫入 `chrome.storage`、console、trace 或 screenshot evidence；service worker 是 Profile API 的唯一 client，content script 不可取得 Profile。
- editor 的 `saving` 狀態停用重複儲存與離頁；`conflict`、validation issues 與離線錯誤保留表單，並把焦點移到可修正的第一個欄位或錯誤摘要。
- list／job 擷取失敗、API 離線或 token 無效時，Side Panel 顯示可理解的錯誤與使用者觸發的重送；不得背景高頻重試或暫存 JD。
- 列表標記採 Shadow DOM，避免與 104 頁面樣式互相污染。

## 6. 測試與交付物

- Side Panel、Options、service worker 訊息與 API error mapping 以 mock API 單元測試；不使用真實 JD、Profile、token 或 104 頁面。
- B4 隔離 Chromium 自動模擬使用固定 unpacked ID，保存不含 token/body 的 request 摘要與 Side Panel screenshot，並驗證 light／dark 主題、來源／流程／投遞／判定篩選、四維對照與篩選逐條結論、求職信生成要求、clipboard、SQLite apply 回寫與結構化 Run history；實際 Chrome 另走人工 gate。
- 104 script 的實機驗收見 [verify](../verify.md) V5、Cake script 見 V6；兩者都必須由驗收者載入測試版插件，以使用者導覽完成列表就地標記、內頁 context 與待看流程。
- 交付 `extension/` 的 MV3 manifest、Side Panel、Profile editor、Options、service worker、各平台 content scripts、列表標記、疑似重複裁決介面與測試；API capture endpoint 與 crawler 的 `parse104/`、`parsecake/` 分屬各自模組交付。

## 7. 待決

（無。）
