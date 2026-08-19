# 測試規格 — crawler（`internal/crawler`）

對應 [crawler 模組設計](../designs/design-crawler.md)、PRD R2 與 R9。本規格的 B1 範圍為 `Source` 介面、共用抓取 helper 與 Yourator adapter；104 與 Cake 的半被動解析器分別於 B5、B6 依相同契約擴充案例。L1 測試使用 `httptest` 假伺服器與結構仿真、內容合成的 fixture，不連線真實職缺端點。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件已實作批次的 CT-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| HTTP 端點 | 每個案例使用獨立 `httptest` server；驗證 method、query parameter、header 與請求次數 |
| 回應 fixture | JSON 與 HTML 結構仿真，職稱、組織、地點與 JD 內容均為合成字串；不含真實職缺或可識別資訊 |
| 時間與延遲 | 注入固定 clock 與可觀測 sleeper／backoff；測試不得實際等待隨機延遲 |
| 搜尋條件 | 使用合成技能詞、地區與頁數；每個案例明確指定 `SearchSpec` |
| 結果驗證 | 斷言 `RawJob` 的來源、外部識別、URL、標題、組織資訊、地點、薪資、遠端型態與全文／partial 語意；不直接寫入 SQLite |
| 錯誤驗證 | adapter 回傳錯誤時，不回傳部分成功或猜測欄位；呼叫端可據此將該來源記入 run stats 並繼續其他來源 |

## 3. B1 單元測試案例

### 3.1 `Source` 契約與搜尋請求

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| CT-01 | 建立 Yourator source | `Name()` 回傳 `yourator`；實作可作為 `Source` 使用 |
| CT-02 | 以三個 direction query、地區與頁數執行 `Fetch` | 每個 direction 的 keywords 組成一次搜尋；不同 direction 不混入同一 request；最多三組 |
| CT-03 | `SearchSpec` 的 queries、direction、keywords、地區或 `MaxPages` 為空或非法 | 在送出請求前回傳欄位錯誤；不產生 HTTP 請求 |
| CT-04 | 設定自訂 UA、Referer 或平台必要 header | 每個請求均帶正確 header；未設定時使用安全的預設值 |
| CT-05 | 執行 `Fetch` 並收集交付的批次 | 每筆解析出的職缺各成一個批次，另有每頁結束的不帶職缺批次；累積結果等同該 spec 應抓到的全部職缺 |
| CT-06 | `emit` 在第一個批次回錯 | `Fetch` 立即回傳該錯誤；後續列表頁與內頁不再送出請求 |

### 3.2 Yourator 回應映射與分頁

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| CT-10 | 單頁合法列表與可取得的 Yourator 全文資料；外層 `section.job-description` 含巢狀工作內容、條件要求與加分條件 | 每筆資料映射為完整 `RawJob`；`source`、`external_id` 與 canonical URL 穩定；三個區段的標題與內容均進入保留換行的純文字 `description` |
| CT-11 | 列表僅含可識別欄位、無全文 | 產生 partial `RawJob`；`description` 為空，其他列表可得欄位保留，不自行杜撰內容 |
| CT-12 | 多頁回應以 `hasMore`／`nextPage` 指示下一頁 | 依序抓取至無下一頁或 `MaxPages`，合併結果且每一頁只請求一次 |
| CT-13 | 回應仍有下一頁但已達 `MaxPages` | 停止抓取，不請求超出上限的頁面 |
| CT-14 | 同一批或跨頁出現相同平台 ID | 回傳結果不含重複 `external_id`；保留首次取得的完整資料，partial 不覆蓋完整資料 |
| CT-17 | 不同 direction queries 回傳相同平台 ID | 所有結果進入同一池並去重；相同職缺全文只抓取一次 |
| CT-15 | 合法空列表 | 成功回傳空集合；不視為來源錯誤 |
| CT-16 | 薪資與遠端標記使用 B1 spike 已定義的合法格式 | 依 §3 欄位映射表轉為 `salary_min`、`salary_max` 與 `remote_type`；無法表達時保留設計定義的空值，不猜測數值 |

### 3.3 韌性、錯誤與合規邊界

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| CT-20 | 搜尋或全文請求先回傳可重試的 5xx／暫時網路錯誤，後續成功 | 依指數退避最多重試 2 次後成功；clock／sleeper 顯示沒有超出設定次數的等待 |
| CT-21 | 請求持續失敗，或收到不可重試的 4xx | 回傳來源錯誤及安全摘要；不無限重試、不回傳部分資料 |
| CT-22 | 回應為非預期 content type，或整體無法解析的 JSON | 回傳結構錯誤，指出無法解析之處；不回傳部分猜測資料 |
| CT-22a | 列表中個別項目缺 ID、標題、路徑或公司 | 略過該項並保留同批其他項目；整批不失敗 |
| CT-22b | 列表中個別項目的 `location` 為 null 或空字串 | 該筆仍收錄，`location` 為 `unknown`；其他欄位照常映射 |
| CT-23 | 回應顯示人機驗證、驗證碼或需登入頁面 | 停止來源並回傳合規錯誤；不嘗試替代參數、解碼或繞過 |
| CT-24 | 連續請求同一來源 | 每次請求之間使用設定範圍內的隨機延遲；測試以注入亂數驗證上下界與可重現性 |
| CT-25 | robots.txt 禁止目標路徑或無法安全確認規則 | 在搜尋前停止來源；不發送搜尋或 detail request |

## 4. 後續批次擴充案例

| 批次 | 編號 | 測試情境 | 預期結果 |
|---|---|---|---|
| B5 | CT-40 | 104 搜尋頁與通知頁擷取素材（兩套 fixture） | 各自解析為 partial `RawJob` 且欄位一致，來源固定為 `104`，不發送任何 HTTP 請求 |
| B5 | CT-41 | 104 內頁 JSON-LD／DOM 素材含完整 JD | 解析為完整 `RawJob`；與既有 partial 項目使用同一外部識別 |
| B5 | CT-42 | 104 素材缺 JSON-LD、必要欄位或 URL pattern 不符 | 回傳解析錯誤；不產生猜測資料，且不觸發伺服器端抓取 |
| B5 | CT-43 | 列表項目職稱含 `text-highlight` 命中標記 | 職稱取自 `title` 屬性，為未切碎的完整字串 |
| B5 | CT-44 | 列表項目含 `.info-description` 摘要 | `description` 恆為 NULL；摘要不入 `RawJob`（片段非全文） |
| B5 | CT-45 | 內頁 `description` 為兩層跳脫的 HTML | 解碼 entity → strip tag → 正規化空白後含【工作內容】【相關條件】【其他條件】【公司福利】全部區段 |
| B5 | CT-46 | 內頁 `baseSalary` 為面議 placeholder（如 `40000元以上`） | `salary_min`／`salary_max` 為 NULL，不採信 placeholder |
| B5 | CT-47 | 內頁 `jobLocationType` 為 `TELECOMMUTE` 且內文為部分遠端 | `remote_type` 為 `hybrid`；內文無部分遠端字樣時才為 `remote` |
| B5 | CT-48 | 內頁 `skills`／`educationRequirements` 為空陣列、語文條件為 `[object Object]` | 不映射空結構化欄位；不因 104 端渲染錯誤而報錯或產生猜測值 |
| B6 | CT-50 | Cake 列表 `__NEXT_DATA__` 素材（合成 fixture） | 每筆映射為 partial `RawJob`：`external_id` 為 `{companyPath}/{jobPath}`、`url` 為完整內頁連結、`description` 恆為 NULL、來源固定 `cake`；不發送任何 HTTP 請求 |
| B6 | CT-51 | 列表項目含 `highlightedTitle`／`highlightedName` | 職稱與公司取原始欄位，不取含命中標記者 |
| B6 | CT-52 | 列表項目薪資為非 TWD、非月薪或 null | `salary_min`／`salary_max` 一律 NULL，不換算、不猜測 |
| B6 | CT-53 | Cake 內頁 `pageProps.job` 含 `description`、`requirements`、`interview_process` | 三段 HTML strip tag、解碼 entity、正規化空白後串接為單一全文；空段落略過；`external_id` 與列表項目一致 |
| B6 | CT-54 | 內頁 `hide_salary_completely`／`hide_salary_max` 為真 | 對應薪資欄位為 NULL，不採用底層數值 |
| B6 | CT-55 | 內頁 `remote` 為 `no_remote_work`／部分遠端／完全遠端／未知值 | 依序映射為 `onsite`／`hybrid`／`remote`／`unknown` |
| B6 | CT-56 | 內頁公司資料含 `contact_name`／`email`／`phone` | 這些欄位不進入 `company_info` 或任何 `RawJob` 欄位 |
| B6 | CT-57 | 素材缺 `__NEXT_DATA__`、非合法 JSON、缺 `pageProps.job` 或職缺非上架狀態 | 回傳解析錯誤或不入庫；不產生猜測資料，且不觸發伺服器端抓取 |
| B6 | CT-62 | Cake 內頁的 DOM 收割素材（職稱、公司名、依序 JD 區塊、metadata 行） | `external_id` 取自頁面 URL；JD 以「區塊標題＋內文」串接；地點、月薪區間與遠端形式由 metadata 行辨識，不依賴其順序或位置 |
| B6 | CT-63 | 內頁 DOM 素材缺 metadata 行／缺 JD 區塊／區塊內文皆空／缺職稱／缺公司名 | 缺 metadata 者地點與遠端為 `unknown`、薪資為 NULL 且仍可入庫；後四者回傳解析錯誤 |
| B5／B6 | CT-59 | 列表素材中單筆項目缺職缺路徑、職稱、公司或（104）地區 | 該筆略過、其餘照常映射；整批不回錯，同頁其他項目不因此失去標記 |
| B6 | CT-60 | Cake 列表或內頁素材未陳述地點 | `location` 存哨兵值 `unknown`，不以公司地址或任何猜測值補上；地區條件據此判為未決 |
| B6 | CT-60 | Cake 內頁 `job.locations` 為空陣列 | `location` 退回公司的 `geo_state_name_l` ＋ `geo_city_l`；街道地址不進入 `location` |
| B6 | CT-61 | `ssr.search` 為物件，URL 帶 `query`／`page` 之外的參數 | 判定為過時，走 DOM 收割；不以推測的 filter 映射認定新鮮 |

## 5. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- B1 的 Yourator adapter 透過公開 API 契約，以 `SearchSpec` 取得去重後的 `RawJob`，並正確區分完整與 partial 資料。
- 分頁、頁數上限、header、延遲與重試均可由注入依賴測試；來源錯誤不產生部分或臆測結果。
- fixture、測試輸出與錯誤訊息不含真實職缺內容、PII 或任何繞過反爬措施。
- 真實 Yourator 端點僅由 opt-in 的 `e2e-live` 依 [verify](../verify.md) V3 檢查，不進 CI；必須至少取得一筆真資料並驗證實際欄位映射，零筆不得算 PASS。
