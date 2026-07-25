# 模組設計 — crawler（來源層 / Source adapter 與 104 解析器）

對應需求：R2、R9。負責把各平台職缺轉為統一的 `RawJob`，交由 pipeline upsert。

## 1. 來源策略與合法抓取判準

**合法抓取判準（各來源共用）**：只碰免登入公開頁、遵守 robots.txt、禮貌速率、**零繞過**（遇人機驗證、驗證碼、加密參數一律停止，不嘗試突破）、內容不重散布（DB 私有；公開 repo 的測試 fixture 結構仿真、內容一律合成，不含真實 JD）。

來源選型（可及性為 2026-07 自 GCP VM 實測）：

| 平台 | 可及性 | 角色 |
|---|---|---|
| Yourator | robots.txt 僅擋 `/r/*`；`GET /api/v4/jobs` 免認證回 JSON | 全自動來源 #1（B1） |
| Cake | robots.txt 全開；搜尋頁 SSR 內嵌職缺資料 | 全自動來源 #2（B6） |
| 104 | 全站（含 robots.txt）在 Cloudflare 人機驗證後 | **半被動來源（B5）**：不做伺服器端抓取；由瀏覽器插件於使用者瀏覽時擷取，本模組僅負責解析（見 [design-extension](design-extension.md)） |
| 台灣就業通開放資料 | data.gov.tw dataset 44062，官方開放授權 | 僅作測資／fixture 素材，不進正式抓取 |
| 1111 / Indeed TW | Cloudflare challenge／403 | 排除（1111 未來可走插件路線） |
| Meet.jobs / 518熊班 / yes123 | 已收站／受眾不符／科技職缺偏少 | 排除 |

## 2. 介面與型別

| 型別 | 欄位/方法 | 說明 |
|---|---|---|
| `Source`（介面） | `Name() string` | 來源代碼（`yourator` / `cake`）；僅全自動來源實作 |
| | `Fetch(ctx, spec SearchSpec) ([]RawJob, error)` | 依搜尋條件抓取一批職缺 |
| `SearchSpec` | `Queries []SearchQuery`、`Area []string`、`MaxPages int` | 由 Profile directions 展開（見 §5）；每個方向一組 query，每個來源最多三組 |
| `SearchQuery` | `Direction string`、`Keywords []string` | 同一方向的 keywords 一起送入平台搜尋；不同方向不混入同一 request |
| `RawJob` | 對應 `jobs` 表的來源端欄位（external_id、url、title、company_name、company_info、description、salary_min/max、location、remote_type） | `description` 可為空（partial，僅列表可見欄位）→ upsert 為 `discovered`；含全文 → `new`。見 [design-schema](design-schema.md) §3 |

104 不實作 `Source`（無伺服器端抓取）；本模組提供 **104 解析器**：輸入插件擷取的原始素材（列表頁項目、內頁 JSON-LD／DOM 片段），輸出 `RawJob`，由 API capture endpoint 呼叫（見 [design-api](design-api.md)）。

新增全自動平台＝新增一個 `Source` 實作＋設定檔掛載；新增插件平台＝新增一個解析器＋插件 URL pattern，pipeline 皆不改。

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

## 4. Cake adapter（B6）

- 搜尋頁為 SSR、內嵌職缺資料；解析方式（內嵌 JSON 或 HTML `goquery`）於 B6 spike 定案，補進本文件。

## 5. 搜尋條件的生成（R2.6，各來源共用）

1. **預設來源＝Profile**：由 `profile.preferences.directions[]` 展開；每個方向的 keywords 組成一次 query，每個來源每輪最多取前三個方向。不同 query 與頁面取得的資料進入同一結果池，依平台 external ID 去重；完整資料優先於 partial。地區條件取 `preferences.locations` ＋ remote 標記，映射為該平台的搜尋參數。
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
5. **CLI**：
   - `jobfinder queries show`：列印各來源實際展開後的 query 清單，供調參確認。
   - `jobfinder queries urls --source 104`：生成 104 巡邏搜尋 URL 清單（技能詞主網＋大類補刀網），供使用者點開、插件收割；同組條件供使用者在 104 註冊職缺通知（通知頁同樣以插件列表模式收割）。
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
- 104 內頁：JSON-LD 兩層跳脫的 `description` 解碼為含全部區段的純文字；`baseSalary` 為面議 placeholder 時 `salary_min/max` 為 NULL；`TELECOMMUTE` ＋部分遠端內文映射為 `hybrid`；空 `skills`／`educationRequirements` 不映射。
- 負向：非 200、JSON 結構變更（缺欄位時報 error 而非靜默略過）、空結果、內頁缺 JSON-LD。
- 真實端點 smoke 為手動案例（`docs/verify.md`），不進 CI。

## 8. 交付物

- `internal/crawler/`：介面、共用 fetch helper、`sourceyourator/`（B1）、`parse104/`（B5，解析器，無抓取）、`sourcecake/`（B6）＋各自測試與 fixture。

## 9. 待決

- Cake 解析方式（B6 spike 回填 §4）。

104 頁面結構屬半被動來源，只由使用者本人導覽時取樣，取樣方式見 [design-extension](design-extension.md) §4。
