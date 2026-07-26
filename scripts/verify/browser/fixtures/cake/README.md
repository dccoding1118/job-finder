# Cake 合成 DOM fixture

結構仿真、內容合成的 Cake 頁面樣本，供 V6（Cake 半被動擷取與就地判定）自動化驗收使用。頁面結構取樣自免登入公開頁的伺服器端單次取樣（`robots.txt` 全開、無條件列表與內頁皆回 200），改寫為虛構內容；真實 JD、公司與個人資料一律不進 repo（Zero-PII）。

| 檔案 | 對應頁面 | URL pattern | 資料來源 | 讀取邏輯 |
|---|---|---|---|---|
| `search.html` | 搜尋／職類列表頁 | `https://www.cake.me/jobs*` | `script#__NEXT_DATA__` 優先、DOM 收割備援 | `extension/content/cake-list.js` |
| `job.html` | 職缺內頁 | `https://www.cake.me/companies/*/jobs/*` | 渲染後的 DOM | `extension/content/cake-job.js`；解析見 `internal/crawler/parsecake.go` |

兩支模式腳本由 `extension/content/cake.js` 依 URL 路由，`extension/content/nav.js` 提供 SPA 換頁通知。

欄位映射與 selector 的權威來源：`docs/designs/design-crawler.md` §4、`docs/designs/design-extension.md` §4.0.1。

## 涵蓋的解析情境

**列表頁（`search.html`）**
- `__NEXT_DATA__` 的 `ssr.search` 為物件 `{"query":"platform","page":1,"filters":{}}`：URL 條件僅有 `query`／`page` 且與其一致時走內嵌狀態，翻頁、帶 filter 或改條件則退回 DOM 收割。
- `highlightedTitle`／`highlightedName`／`highlightedDescription` 皆存在，職稱與公司必須取原始欄位。
- 一筆為 TWD 月薪明確區間，一筆為 USD 年薪（薪資一律 NULL）。
- DOM 的 class name 帶 build hash（`JobSearchItem-module-scss-module___a1B2c__…`），定位只能依 `a[href^="/companies/"][href*="/jobs/"]` 與 `[class*="JobSearchItem"]` 前綴；該前綴同時出現在項目容器與連結所在的標題節點上，項目容器取**最外層**符合者。
- 公司連結在項目內出現多次（logo 錨點只含圖片、無文字），公司名取第一個有文字者。
- 列表 `description` 為搜尋摘要，一律不得映射為 `description`。

**內頁（`job.html`）**
- 頁面的 `__NEXT_DATA__` 只帶列表條件、不帶 listing——真實內頁即是如此，因此內頁一律只讀 DOM。
- class name 帶 build hash（`JobDescriptionLeftColumn-module-scss-module__16Kv_a__title`），定位只能依元件前綴與角色後綴，並以標籤名消除前綴共用（`…__titleRow` 對 `…__title`）。
- 兩個 `ContentSection` 區塊（`職缺描述`、`職務需求`）串接為單一全文，各自保留標題；區塊內文以 `:scope >` 限定直接子層，避免匹配到區塊自身。
- 公司名在 `…__name` 錨點的 `h2` 內；同頁另有只含圖片的 logo 錨點。
- metadata 行位於 `…__rightColumn` 與 `…__inlineJobMeta`：`月薪 80,000 ~ 120,000 元` → `salary_min/max`、`台北市內湖區` → `location`、`部分遠端工作` → `remote_type=hybrid`；`職缺 6 天前更新` 不對應任何欄位。
- `external_id` 為 `example-cloud/senior-platform-engineer`，取自 URL 且與列表項目一致。
