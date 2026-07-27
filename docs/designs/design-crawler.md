# 模組設計 — crawler（來源層 / Source adapter 與半被動解析器）

對應需求：R2、R9。負責把各平台職缺轉為統一的 `RawJob`，交由 pipeline upsert。

## 1. 來源策略與合法抓取判準

**合法抓取判準（各來源共用）**：只碰免登入公開頁、遵守 robots.txt、禮貌速率、**零繞過**（遇人機驗證、驗證碼、加密參數一律停止，不嘗試突破）、內容不重散布（DB 私有；公開 repo 的測試 fixture 結構仿真、內容一律合成，不含真實 JD）。

來源選型（可及性為 2026-07 自 GCP VM 實測）：

| 平台 | 可及性 | 角色 |
|---|---|---|
| Yourator | robots.txt 僅擋 `/r/*`；`GET /api/v4/jobs` 免認證回 JSON | 全自動來源 #1（B1） |
| Cake | robots.txt 全開、內容開放；但**帶任何搜尋條件的請求**（關鍵字、地區、職類、SEO 職類頁）一律回 403 `cf-mitigated: challenge` | **半被動來源（B6）**：內容取得無礙，但無法以條件挑出目標職缺；挑選改由使用者在自己的瀏覽器完成，本模組僅負責解析 |
| 104 | 全站（含 robots.txt）在 Cloudflare 人機驗證後 | **半被動來源（B5）**：不做伺服器端抓取；由瀏覽器插件於使用者瀏覽時擷取，本模組僅負責解析（見 [design-extension](design-extension.md)） |
| 台灣就業通開放資料 | data.gov.tw dataset 44062，官方開放授權 | 僅作測資／fixture 素材，不進正式抓取 |
| 1111 / Indeed TW | Cloudflare challenge／403 | 排除（未來可走插件路線） |
| Meet.jobs / 518熊班 / yes123 | 已收站／受眾不符／科技職缺偏少 | 排除 |

## 2. 介面與型別

| 型別 | 欄位/方法 | 說明 |
|---|---|---|
| `Source`（介面） | `Name() string` | 來源代碼；僅全自動來源實作（目前只有 `yourator`） |
| | `Fetch(ctx, spec SearchSpec) ([]RawJob, error)` | 依搜尋條件抓取一批職缺 |
| `SearchSpec` | `Queries []SearchQuery`、`Area []string`、`MaxPages int` | 由 Profile directions 展開（見 §5）；每個方向一組 query，每個來源最多三組 |
| `SearchQuery` | `Direction string`、`Keywords []string` | 同一方向的 keywords 一起送入平台搜尋；不同方向不混入同一 request |
| `RawJob` | 對應 `jobs` 表的來源端欄位（external_id、url、title、company_name、company_info、description、salary_min/max、location、remote_type） | `description` 可為空（partial，僅列表可見欄位）→ upsert 為 `discovered`；含全文 → `new`。見 [design-schema](design-schema.md) §3 |

104 與 Cake 不實作 `Source`（無伺服器端抓取）；本模組為兩者各提供一個**半被動解析器**：輸入插件擷取的原始素材（列表頁項目、內頁 JSON-LD 或內嵌 JSON），輸出 `RawJob`，由 API capture endpoint 依 payload 的 `source` 分派（見 [design-api](design-api.md)）。

新增全自動平台＝新增一個 `Source` 實作＋設定檔掛載；新增插件平台＝新增一個解析器＋插件 URL pattern，pipeline 皆不改。**半被動是多平台擴充的主路線**——可全自動的平台是例外（僅 Yourator），多數台灣平台的搜尋路徑都在人機驗證之後。

**列表解析逐項容錯（各半被動來源共用）**：列表解析器對**單一項目**欠缺必要欄位（識別不出職缺路徑、或缺職稱／公司／地區）一律**略過該項**，不使整批擷取失敗；擷取回應只涵蓋成功解析的項目。列表擷取的產物是使用者眼前整頁的標記，整批回錯會讓一張版面配置略有差異的卡片令**同頁其他每一筆**都失去標記。內頁解析相反：素材無法解析時回 error，因為該頁只有那一筆職缺，沒有可保住的其他結果。

## 2.1 104 解析器 — 列表項目（partial）

搜尋頁與通知頁的 DOM 結構不同（頁面與掛載點見 [design-extension](design-extension.md) §4.0），欄位來源各異，正規化後輸出同一組 partial `RawJob`：

| `RawJob` 欄位 | 搜尋頁 | 通知頁 |
|---|---|---|
| `external_id` | `a.info-job__text` href 的 `/job/{id}` 路徑段 | 同左 |
| `url` | 同上 href 去除 query（`jobsource` 等追蹤參數不入庫） | 同左 |
| `title` | `a.info-job__text` 的 **`title` 屬性** | 同左 |
| `company_name` | `a.info-company__text` 文字 | 同左 |
| `company_info` | `.info-company-addon-type` 文字（產業別） | 同左 |
| `location` | `.info-tags__text` 中 `data-gtm-joblist` 前綴為 `職缺-地區-` 者 | `.info-tags__text` 第 1 項 |
| 薪資 | `.info-tags__text` 中前綴為 `職缺-薪資-` 者 | `.info-othertags__text` 中符合薪資格式者 |
| `remote_type` | `.info-othertags` 含「遠端工作」標籤 ⇒ `remote`，否則 `unknown` | 同左（無標籤時 `unknown`） |
| `description` | 一律 NULL（見 §2.2） | 一律 NULL |

職稱取 `title` 屬性而非節點文字，因為關鍵字命中處會被包成 `<span class="text-highlight">`，直接取文字會得到被切碎的片段。

通知頁的 `.info-tags__text` **不帶** `data-gtm-joblist` 屬性（搜尋頁有），只能依序取值：地區、經歷、學歷。其薪資與其他資訊（如「員工50人」）混在同一組 `.info-othertags__text` 中，須依格式辨識而非位置。

列表項目一律為 **partial**：`description` 為 NULL、`salary_min/max` 僅在明確金額格式時解析（「待遇面議」為 NULL），upsert 為 `discovered`。

## 2.2 104 解析器 — 列表摘要不可作為 JD

兩個列表頁都有 `.info-description` 區塊，但**一律不得映射至 `description`**，理由不同且都是硬性的：

- **搜尋頁**：該區塊是繞著關鍵字命中處拼接的**摘要片段**（sample 佐證：內文自第 1 點跳至第 3 點、結尾斷在半句中間；同頁無關鍵字命中的廣告職缺則回完整內文）。片段未出現某詞**不表示 JD 無該詞**，據以執行 `exclude_description_keywords`／`require_any_keywords` 會產生假淘汰。
- **通知頁**：內容雖較完整（推測因無關鍵字搜尋而回原文），但僅涵蓋「工作內容」，缺【相關條件】【其他條件】【公司福利】三段，仍非全文。

因此清單一律視為 partial，全文只來自內頁 JSON-LD（§2.3）。對應的篩選限制見 [design-pipeline](design-pipeline.md) §3。

## 2.3 104 解析器 — 內頁（全文）

內頁的 `script[type="application/ld+json"]` 陣列中，取 `@type` 為 `JobPosting` 的物件：

| `RawJob` 欄位 | JSON-LD 來源 | 處理方式 |
|---|---|---|
| `external_id` | `identifier.value` | 與 `url` 的 `/job/{id}` 路徑段一致，取任一即可 |
| `url` | `mainEntityOfPage.@id` | 已為無 query 的正規 URL |
| `title` | `title` | |
| `company_name` | `hiringOrganization.name` | |
| `company_info` | `industry` | |
| `description` | `description` | **兩層跳脫**：先解碼 HTML entity（`&lt;br&gt;` → `<br>`）→ strip tag → 解碼實體 → 正規化空白。解碼後含【工作內容】、職務類別／待遇／性質／地點／上班時段、【相關條件】、【其他條件】、【公司福利】全部區段，是完整 JD |
| `location` | `jobLocation.address.addressLocality` | |
| `salary_min` / `salary_max` | `baseSalary` | 僅在 `value.value` 為明確區間且 `unitText` 為 `MONTH` 時解析；見下方限制 |
| `remote_type` | `jobLocationType` ＋ `description` 內文 | 見下方限制 |

**JSON-LD 的結構化欄位多數不可信**，值只存在於 `description` 文字中：

| 欄位 | 實測狀況 | 處置 |
|---|---|---|
| `skills` / `educationRequirements` | 恆為空陣列（值在 description 的【相關條件】內） | 不映射 |
| 語文條件 | 104 自身渲染錯誤，description 內為 `英文--[object Object]` | 不解析；正規化時原樣保留 |
| `baseSalary` | 「待遇面議」職缺會填入 placeholder 下限（如 `40000元以上`），與內文實際敘述（如年薪 81~108 萬）不符 | 非明確區間一律 NULL（面議不猜測），避免污染 `salary_min` 觸發薪資淘汰 |
| `jobLocationType` | 部分遠端職缺標為 `TELECOMMUTE`，但內文為「每月 10 天居家辦公」 | 不單獨採信；`TELECOMMUTE` ＋內文含部分遠端字樣（「每月」「天」「部分」「混合」）⇒ `hybrid`，否則 `remote` |

`description` 解碼失敗或 `JobPosting` 不存在時回 error 而非靜默略過（DOM 備援僅在 JSON-LD 缺失時使用）。

## 3. Yourator adapter（B1）

- **方式**：公開 JSON API（已實測免認證可用）：
  - 搜尋列表：`GET https://www.yourator.co/api/v4/jobs?term[]=…&page=…`，回應含 `id`、`name`、`path`、`salary`、`location`、`lastActiveAt` 等欄位與 `hasMore`/`nextPage` 分頁。
  - 職缺全文：列表回應若不含 JD 全文，以職缺 JSON endpoint 或內頁補齊（B1 spike 定案）。
- **欄位映射**：B1 spike 後把「Yourator JSON 欄位 → RawJob 欄位」映射表補進本節（含薪資字串解析、遠端標記解讀）。

| Yourator 資料 | `RawJob` 欄位 | 處理方式 |
|---|---|---|
| 列表 JSON `id` | `external_id` | 轉為十進位字串。 |
| 列表 JSON `name`、`path`、`company.brand`、`salary`、`location` | `title`、`url`、`company_name`、薪資、`location` | URL 為 `https://www.yourator.co` 加 `path`；薪資僅在 `NT$ min - max` 月薪格式時解析，其他格式為 NULL。 |
| 公開職缺 HTML 外層 `section.job-description` | `description` | 依巢狀 `section` 平衡邊界擷取完整容器，包含工作內容、條件要求、遠端型態、加分條件與其他職缺資訊；移除 HTML tag、解碼 entity 並保留標題與段落換行。容器不存在或結構不完整時保留 partial 職缺。 |
| 職稱與 JD 中的 `remote`／`遠端`／`hybrid`／`混合` | `remote_type` | 依序判定 remote、hybrid，其他為 onsite。 |

## 4. Cake 解析器（B6，半被動）

素材由插件在使用者瀏覽時取出（見 [design-extension](design-extension.md) §4.0.1），兩種頁面型態的素材不同：

| 頁面 | 素材 |
|---|---|
| 列表 | `<script id="__NEXT_DATA__" type="application/json">`（Next.js SSR 狀態）優先，過時或缺漏時退回 DOM 收割的項目陣列 |
| 內頁 | 渲染後 DOM 的收割結果 |

**巡邏 URL 實務上走 DOM 收割**：`__NEXT_DATA__` 的內嵌列表狀態只在 URL 條件僅含 `query`／`page` 時可信；帶其他搜尋條件的請求一律被 Cake 擋下（§1），因此 `ssr.search.filters` 與 URL 參數的映射無從取樣驗證，也不得據以判斷收割完整性。

**內頁不使用 `__NEXT_DATA__`**：Cake 的內頁不帶自己的 listing 狀態，頁面上的那份描述的是使用者進來前的列表畫面。內頁解析仍接受帶 `__NEXT_DATA__` 的擷取（見 §4.3.1 的欄位映射），但 DOM 收割優先且是插件實際採用的路徑。

### 4.1 列表項目（partial）

取 `props.pageProps.initialState.jobSearch.entityByPathId` 的每個項目，其 `page` 子物件是刊登公司：

| `RawJob` 欄位 | `__NEXT_DATA__` 來源 | 處理方式 |
|---|---|---|
| `external_id` | `page.path` ＋ `path` | 組為 `{companyPath}/{jobPath}`；Cake 無數字職缺 ID，此組合是穩定唯一鍵 |
| `url` | 同上 | `https://www.cake.me/companies/{companyPath}/jobs/{jobPath}` |
| `title` | `title` | 取原始 `title`，不取 `highlightedTitle`（後者含關鍵字命中標記） |
| `company_name` | `page.name` | 同樣不取 `highlightedName` |
| `company_info` | `page.geo` ＋ `page.country` | 正規化為「國別／城市」摘要；Cake 列表無產業別欄位 |
| `location` | `locations[]` | 取第一個非空值；該欄位已依頁面語系本地化 |
| `salary_min` / `salary_max` | `salary.min` / `salary.max` | 僅在 `salary.currency` 為 `TWD` 且 `salary.type` 為 `per_month` 且值非 null 時採用；其餘為 NULL |
| `remote_type` | — | 列表無遠端欄位，一律 `unknown` |
| `description` | — | **一律 NULL**（見 §4.2） |

### 4.2 列表 `description` 不採計為 JD

列表項目帶 `description` 欄位且內容看似完整純文字，但無從驗證是否被截斷，且 `highlightedDescription` 的存在顯示該欄位服務於搜尋摘要用途。比照 104（§2.2），列表一律視為 partial、`description` 為 NULL，upsert 為 `discovered`；全文只來自內頁。

### 4.3 內頁（全文，DOM 收割）

素材是插件送來的職稱、公司名、依序的 JD 區塊（各含標題與純文字內文）與 metadata 行陣列：

| `RawJob` 欄位 | 來源 | 處理方式 |
|---|---|---|
| `external_id` / `url` | 頁面 URL 路徑 | 與 §4.1 同一組合（`{companyPath}/{jobPath}`），確保內頁補全文命中同一筆 |
| `title` / `company_name` | 收割的同名欄位 | 兩者皆必填，缺任一項回 error |
| `company_info` | — | 內頁 DOM 無可靠的公司描述欄位，一律 `public listing` |
| `description` | JD 區塊 | 有內文的區塊以「標題＋內文」串接，空內文的區塊略過；無任何區塊時回 error |
| `location` | metadata 行 | 取第一個含地名語意（`市`／`縣`／`區`／`Taiwan`／`Taipei`／`Remote`／`遠端`）的行；無命中為 `unknown` |
| `salary_min` / `salary_max` | metadata 行 | 僅解析明確的月薪區間（與列表 DOM 收割同一規則）；無命中為 NULL |
| `remote_type` | metadata 行 | 命中部分遠端／混合語意 ⇒ `hybrid`；命中完全遠端 ⇒ `remote`；無命中 ⇒ `unknown` |

metadata 的辨識規則刻意寬鬆：誤判為地點只會讓該職缺被地區條件篩選，漏判則使其停在 `unknown` 而仍可評分——後者是安全的失敗方向。

### 4.3.1 內頁（全文，`__NEXT_DATA__`）

擷取帶 `props.pageProps.job` 與 `props.pageProps.company` 時的映射：

| `RawJob` 欄位 | 來源 | 處理方式 |
|---|---|---|
| `external_id` / `url` | 頁面 URL 路徑 | 與 §4.1 同一組合，確保內頁補全文命中同一筆 |
| `title` | `job.title` | |
| `company_name` | `company.name` | |
| `company_info` | `company.products_or_services`、`company.founded_year`、`company.geo_formatted_address` | 正規化為單行摘要；不取 `contact_name`／`email`／`phone`（PII，不入庫） |
| `description` | `job.description` ＋ `job.requirements` ＋ `job.interview_process` | 三段皆為 HTML：strip tag、解碼 entity、正規化空白後以區段標題串接；`requirements` 常為空字串，空段落略過 |
| `location` | `job.locations[]` 的 `full_name`（缺則 `full_name_en`） | 多地點以頓號串接；`locations` 為空時退回 `company` 的 `geo_state_name_l` ＋ `geo_city_l`（缺則未本地化的 `geo_state_name` ＋ `geo_city`），完整街道地址不採用 |
| `salary_min` / `salary_max` | `job.salary_min` / `job.salary_max` | `job.hide_salary_completely` 為真 ⇒ 兩者 NULL；`job.hide_salary_max` 為真 ⇒ `salary_max` NULL；`job.salary_type` 非月薪時換算不可靠，一律 NULL |
| `remote_type` | `job.remote` | `no_remote_work` ⇒ `onsite`；`partial_remote_work`／混合語意 ⇒ `hybrid`；`full_remote_work` ⇒ `remote`；未知值 ⇒ `unknown` |

`__NEXT_DATA__` 不存在、非合法 JSON 或缺 `pageProps.job` 時回 error 而非靜默略過。`job.aasm_state` 非上架狀態者不入庫。

Cake 的職缺常不自帶地點（遠端與混合型尤其如此），而地點未知會被 Profile 的地區條件淘汰，因此以刊登公司的城市補位；街道地址對篩選無用，一律不取。

## 5. 搜尋條件的生成（R2.6，各來源共用）

1. **預設來源＝Profile**：由 `profile.preferences.directions[]` 展開；每個方向的 keywords 組成一次 query，每個來源每輪最多取前三個方向。全自動來源以此發送請求；半被動來源（104／Cake）以此生成供使用者開啟的巡邏 URL。不同 query 與頁面取得的資料進入同一結果池，依平台 external ID 去重；完整資料優先於 partial。地區條件取 `preferences.locations` ＋ remote 標記，映射為該平台的搜尋參數。
2. **關鍵字以技能詞為主**：職稱與平台職務類別在台灣平台不可靠（類別錯放常見——雲端／DevOps／SRE 職缺散落於軟體、網路、MIS 工程師等類），技能詞直接命中 JD 內文，recall 與 precision 俱佳。展開時補同義詞（如 K8s/Kubernetes、IaC/Terraform），輔以少量職稱變體；職務大類×薪資/地區過濾僅作補刀網。

   104 的 keyword **確實涵蓋工作內容欄位**，非僅比對職稱：`keyword=gcp` 的結果中，命中標記（`.text-highlight`）同時出現在職稱與工作內容摘要內，且有職稱不含該詞、僅內文命中而入列的職缺。技能詞主網成立。

3. **104 搜尋 URL 參數**（供 `queries urls` 生成）：

   | 參數 | 意義 |
   |---|---|
   | `keyword` | 關鍵字（涵蓋職稱與工作內容） |
   | `area` | 地區代碼，逗號分隔（如 `6001001000` 台北市） |
   | `jobcat` | 職務類別代碼（補刀網用） |
   | `order` / `mode` / `page` | 排序（`15`＝最近更新）／模式／頁次 |
   | `remoteWork` | 遠端（`1,2`） |
   | `jobexp` / `edu` / `sr` | 經歷／學歷／薪資級距 |
4. **設定檔覆寫/增補**：`sources.<name>.queries[]` 有值時整組取代自動展開；`extra_queries[]` 為增補。平台專屬參數在 adapter／URL 生成器內映射。
4. **Cake 搜尋 URL 參數**（供 `queries urls` 生成）：

   | 參數 | 意義 |
   |---|---|
   | `query` | 關鍵字 |
   | `location_list[]` | 地區（如 `Taipei City, Taiwan`） |
   | `profession[]` | 職類代碼（如 `it_back-end-engineer`，補刀網用） |
   | `page` | 頁次 |

   這些 URL **只供使用者在自己的瀏覽器開啟**；伺服器端請求同一組 URL 會被人機驗證擋下（§1），本模組不發送。

5. **CLI**：
   - `jobfinder queries show`：列印各來源實際展開後的 query 清單，供調參確認。
   - `jobfinder queries urls --source 104|cake`：生成該平台的巡邏搜尋 URL 清單（技能詞主網＋大類補刀網），供使用者點開、插件收割；104 的同組條件另供使用者註冊職缺通知（通知頁同樣以插件列表模式收割）。
6. 與條件篩選的分工：搜尋條件只縮小抓取範圍；精準淘汰交給 pipeline 依 Profile 套用的排除條件（見 [design-pipeline](design-pipeline.md) §3）。不做全量抓取。

## 6. 禮貌抓取與韌性（全自動來源）

| 面向 | 規則 |
|---|---|
| 請求間隔 | 隨機 1.5–3.5 秒（設定檔可調） |
| 頁數上限 | 每 query `MaxPages`（預設 3） |
| 單請求逾時 | 每個 HTTP 請求（連線、轉址、讀 body）以 `request_timeout`（預設 30s，設定檔可調）為上限；來源 stall 時該請求逾時失敗並走重試／source error，不無限等待 |
| 失敗處理 | 單請求重試 2 次（指數退避）；整個 source 失敗回傳 error，pipeline 記入 run stats，不中斷其他 source |
| robots.txt | 每輪來源抓取前確認目標搜尋與職缺路徑未被禁止；無法取得或規則禁止時停止來源，不發送資料請求 |
| 驗證頁 | 回應顯示登入、人機驗證或驗證碼時停止來源並回傳合規錯誤，不切換參數或嘗試繞過 |
| UA / header | 設定檔指定；預設一般瀏覽器 UA ＋必要 Referer |
| 內容純文字化 | HTML JD 一律轉純文字（strip tags、保留區段標題與換行），DB 不存原始 HTML |

## 7. 測試

- 各 adapter／解析器以 `httptest` 假伺服器或本地 fixture 餵**結構仿真、內容合成**的回應，驗證欄位映射與分頁；不使用真實 JD 內容。Yourator fixture 的外層 `section.job-description` 含巢狀工作內容、條件要求與加分條件，三個區段均須進入 `description`。
- 104 解析器：搜尋頁與通知頁兩套 fixture 各自映射至同一組 partial `RawJob`；廣告職缺被排除；職稱取自 `title` 屬性而非含 `text-highlight` 的節點文字；列表 `description` 恆為 NULL。
- Cake 解析器：列表 `__NEXT_DATA__` 映射為 partial `RawJob`（`external_id` 為 `{companyPath}/{jobPath}`、`description` 恆為 NULL、非 TWD 月薪不解析）；內頁 `pageProps.job` 的三段 HTML 串接為純文字全文，`hide_salary_completely` 時薪資為 NULL，`remote` 各值映射正確；缺 `__NEXT_DATA__` 或缺 `pageProps.job` 回 error。
- 104 內頁：JSON-LD 兩層跳脫的 `description` 解碼為含全部區段的純文字；`baseSalary` 為面議 placeholder 時 `salary_min/max` 為 NULL；`TELECOMMUTE` ＋部分遠端內文映射為 `hybrid`；空 `skills`／`educationRequirements` 不映射。
- 負向：非 200、JSON 結構變更（缺欄位時報 error 而非靜默略過）、空結果、內頁缺 JSON-LD。
- 真實端點 smoke 為手動案例（`docs/verify.md`），不進 CI。

## 8. 交付物

- `internal/crawler/`：介面、共用 fetch helper、`sourceyourator/`（B1）、`parse104/`（B5，解析器，無抓取）、`parsecake/`（B6，解析器，無抓取）＋各自測試與 fixture。

## 9. 待決

（無。）

104 頁面結構屬半被動來源，只由使用者本人導覽時取樣，取樣方式見 [design-extension](design-extension.md) §4。
