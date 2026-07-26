# 變更 — Cake 由全自動來源改為半被動擷取來源

## 1. 背景與動機

原規劃（PRD R2.2）將 Cake 列為第二個全自動 adapter，假設「搜尋頁為 SSR、內嵌職缺資料可解析」，並把解析方式列為 B6 開工前的 spike。

B6 spike 於 2026-07-25 自 GCP VM 實測，該假設不成立：**Cake 的內容是開放的，搜尋是受保護的**。凡是帶任何搜尋條件的請求一律被 Cloudflare 人機驗證攔下，伺服器端無法以 Profile 關鍵字取得目標職缺集合。依零繞過原則（PRD R2.7），Cake 不能作為全自動來源。

## 2. Spike 實測結果

| 路徑 | 結果 |
|---|---|
| `robots.txt` | 全開，僅宣告 sitemap；無 Disallow |
| `GET /jobs?page=N`（不帶任何條件） | 200；`<script id="__NEXT_DATA__">` 內嵌職缺陣列，共 1858 筆 / 186 頁（10 筆/頁），含台灣以外職缺，非時間排序 |
| `GET /jobs?query=…`、`?location_list[]=…`、`?profession[]=…` | **403，`cf-mitigated: challenge`** |
| `GET /jobs/<職類或關鍵字>`（sitemap 收錄的 SEO 落地頁） | **403，`cf-mitigated: challenge`** |
| `GET /companies/{company}/jobs` | 200，但 `companyJobSearch` 的 SSR 狀態為空陣列——職缺由前端另行請求，伺服器端取不到 |
| `GET /companies/{company}/jobs/{job}`（職缺內頁） | 200；`__NEXT_DATA__` 的 `props.pageProps.job` 含結構化全文 |
| 站內搜尋實作 | Algolia；search key 內嵌於頁面 |

補充判斷：

- 唯一放行的自動入口是**無條件** `/jobs?page=N` 的 1858 筆結果集。這是 Algolia 無條件查詢的預設結果，**是否為 Cake 全量職缺無法驗證**，也無合法手段可驗證覆蓋率。
- 以頁面內嵌的 Algolia key 直接查詢 Algolia，等同繞過 Cloudflare 對搜尋路徑的管控，違反 PRD R2.7，不採用。
- 內容面完全開放：職缺內頁與無條件列表皆可取得，**限制只在「以條件挑出目標職缺」這件事**。這正是半被動路線要解決的問題——挑選由使用者在自己的瀏覽器完成。

## 3. 決策摘要

| 項目 | 決策 |
|---|---|
| Cake 的來源型態 | 半被動擷取（同 104），不做伺服器端抓取 |
| 全自動來源 | 僅 Yourator |
| 擷取入口 | Chrome extension content script：整站單一注入，依 URL 路由為列表模式（收割）或內頁模式（補全文） |
| 列表的資料來源 | 優先讀頁面 `__NEXT_DATA__`；與目前 URL 條件不符（使用者在頁內互動後）時退回 DOM 收割 |
| 內頁的資料來源 | 一律讀渲染後的 DOM——Cake 內頁不帶自己的 listing 狀態 |
| 列表項目 | 一律 partial（`description` 為 NULL）→ `discovered`，與 104 同政策 |
| 巡邏動線 | `jobfinder queries urls --source cake` 生成搜尋 URL 供使用者自行開啟 |
| 既有 `Source` 介面 | 不變；Cake 不實作 `Source`，改提供 `parsecake` 解析器，與 `parse104` 同契約 |

專案的長期定位隨此變更收斂：**能全自動的平台少之又少，半被動擷取才是可擴展到多平台的主路線**。新增平台的預設做法是「新增一個解析器＋插件 URL pattern」，全自動 adapter 是例外而非常態。

## 4. 相對舊狀態的差異

| 主題 | 舊 | 新 |
|---|---|---|
| Cake 角色 | 全自動來源 #2（`sourcecake/` 實作 `Source`） | 半被動來源（`parsecake/` 解析器，無抓取） |
| Cake 職缺入庫路徑 | `jobfinder run` fetch | capture API（`discovered_by_run_id` 為 NULL） |
| PRD R2.2 | 「第二批 Cake（搜尋頁 SSR 解析）」 | Cake 移入 R2.5 的半被動來源 |
| PRD R9 範圍 | 104 專屬 | 泛化為「瀏覽器插件半被動擷取」，104 與 Cake 共用同一組需求 |
| B6 驗收 | 兩個全自動來源皆通 | Cake 半被動全流程可跑 |
| S0 退出標準 | 三來源皆通（兩全自動、一半被動） | 三來源皆通（一全自動、兩半被動） |
| design-crawler §4 | Cake adapter（待 spike） | Cake 解析器（欄位映射已定案） |
| design-extension §4 | 104 擷取 | 104／Cake 擷取，各一組 URL pattern 與 selector |

## 5. 落點

| 文件 | 更新內容 |
|---|---|
| `docs/PRD.md` | §1 定位、§2 Source、R2.2／R2.5、R9 標題與各條泛化、§5 流程圖入口 B/C、§7 B6、§9 風險表（Cake spike 結案） |
| `docs/roadmap.md` | S0 形態與退出標準 |
| `docs/design.md` | §3 模組職責、§5 關鍵技術決策（Cake 供給方式）、§7 開發順序 B6 |
| `docs/designs/design-crawler.md` | §1 來源表、§4 Cake 解析器欄位映射、§5 巡邏 URL、§7 測試、§8 交付物、§9 待決 |
| `docs/designs/design-extension.md` | §1 職責、§4 擷取表與 §4.0 頁面結構、§5 權限 |
| `docs/tests/test-crawler.md` | CT-30／CT-31 改寫為 Cake 解析器案例 |
| `docs/tests/test-extension.md` | Cake content script 案例 |
| `docs/verify.md` | §2 覆蓋度地圖 R2／R9、V6 案例、§9 累加順序 |
| `AGENTS.md` | 文件索引與專案現況 |

## 6. 待實作進度

- [x] `internal/crawler` 的 Cake 解析器：列表與內頁＋合成 fixture 與 L1 案例
- [x] `queries show` 與 `queries urls --source 104|cake`
- [x] `extension/content/cake-list.js`、`cake-job.js`、路由 `cake.js` 與 SPA 換頁通知 `nav.js`，manifest 以 `https://www.cake.me/*` 單一注入（列表標記與 104 共用 `content/mark.js`）
- [x] capture API 的來源分派（依 payload 的 `source` 選解析器，未知或缺漏回 400）
- [ ] V6 的驗收 harness 步驟（S40–S45）與實際 Chrome 人工 gate

## 7. 已知殘留限制

- Cake 的列表 `description` 在 `__NEXT_DATA__` 中看似完整純文字，但無法驗證是否被截斷，因此比照 104 一律不採信為 JD 全文；全文只來自內頁。
- Cake 前端 class name 帶 build hash（`JobSearchItem-module-scss-module___szW4W__…`），不可作為 selector 常數；掛載點以穩定的 `a[href^="/companies/"][href*="/jobs/"]` 與 `[class*="JobSearchItem"]` 前綴比對定位。
- 使用者在頁內改條件或翻頁時，`__NEXT_DATA__` 不會更新，必須退回 DOM 收割；判定依據是頁面 `ssr.search` 與目前 `location.search` 是否一致。
- Cake 內頁的 `__NEXT_DATA__` **不含 listing**：頁面上那份描述的是使用者進來前的列表畫面，重新整理也不會出現職缺狀態。內頁因此只能讀 DOM。
- Cake 內頁的地點、待遇與遠端形式渲染為位置不固定的自由文字，只能以語意規則從 metadata 行辨識；辨識不到時停在 `unknown`／NULL，該筆仍可評分。
- 整站單一注入使 content script 出現在所有 `www.cake.me` 頁面。落在列表與內頁以外的頁面時模式為「無」，不讀取也不送出任何內容——這是 SPA 路由的必要條件，Chrome 每份文件只注入一次 content script。
