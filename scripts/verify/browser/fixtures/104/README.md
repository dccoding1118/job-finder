# 104 合成 DOM fixture

結構仿真、內容合成的 104 頁面樣本，供 V5（104 半被動擷取與就地判定）自動化驗收使用。
頁面結構取樣自使用者本人瀏覽時的人工取樣，改寫為虛構內容；真實 JD、公司與個人資料一律不進 repo（Zero-PII）。

| 檔案 | 對應頁面 | URL pattern | 項目容器 | 讀取邏輯 |
|---|---|---|---|---|
| `search.html` | 搜尋結果頁 | `https://www.104.com.tw/jobs/search/*` | `.job-summary`（`.vue-recycle-scroller` 內） | `extension/content/list.js` `searchPage.read` |
| `notification.html` | 職缺通知頁 | `https://pda.104.com.tw/work/mate/list/*` | `.job-list-container[pagenumber]` | `extension/content/list.js` `notificationPage.read` |
| `job.html` | 職缺內頁 | `https://www.104.com.tw/job/*` | `script[type="application/ld+json"]` 的 `JobPosting` | `extension/content/job.js`；解析見 `internal/crawler/parse104.go` |

欄位映射與 selector 的權威來源：`docs/designs/design-crawler.md` §2.1–§2.3／§5、`docs/designs/design-extension.md` §4.0。

## 涵蓋的解析情境

**搜尋頁（`search.html`）**
- 第一筆為廣告職缺（`jobsource=hotjob_chr_exp`、缺 `.job-summary__close`）→ 必須排除。
- 職稱含關鍵字命中 `<span class="text-highlight">`，職稱取自 `a.info-job__text` 的 `title` 屬性而非節點文字。
- `.info-tags__text` 帶 `data-gtm-joblist`，地區以 `職缺-地區-`、薪資以 `職缺-薪資-` 前綴辨識。
- 一筆遠端＋明確月薪區間、一筆待遇面議（薪資 NULL）、一筆非遠端。
- `.info-description` 為摘要片段，一律不得映射為 `description`。

**通知頁（`notification.html`）**
- `.info-tags__text` 不帶 `data-gtm-joblist`，依序為地區、經歷、學歷。
- 薪資與員工人數混在 `.info-othertags__text`，依格式辨識而非位置；遠端以 `遠端工作` 標籤判定。

**內頁（`job.html`）**
- `description` 兩層跳脫：`&lt;br&gt;` → `<br>`、`&amp;amp;` → `&amp;` → `&`，解碼後含【工作內容】【相關條件】【其他條件】【公司福利】全部區段。
- `baseSalary` 明確月薪區間 → `salary_min/max` = 60000/90000。
- `jobLocationType=TELECOMMUTE` ＋內文「每月 10 天」→ `remote_type=hybrid`。
- `skills`／`educationRequirements` 為空陣列 → 不映射；語文條件 `英文--[object Object]` 原樣保留。
