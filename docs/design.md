# 系統設計 — job-finder（MVP）

需求見 [PRD](PRD.md)。本文件定義系統層的技術棧、架構、模組邊界與開發順序；各模組細節見 `docs/designs/design-<module>.md`。

## 1. 主要技術棧

| 層面 | 選型 | 說明 |
|---|---|---|
| 語言 | Go | 家族預設；`module github.com/dccoding1118/job-finder` |
| 工具鏈 | mise（pin go / golangci-lint / gofumpt）＋ `.golangci.yaml` | `mise run build/test/lint/fmt` |
| CLI | spf13/cobra | 入口 `./cmd/jobfinder`，每子命令一檔 |
| 資料庫 | SQLite（`modernc.org/sqlite`，免 cgo） | 單檔、零維運；路徑由設定檔指定 |
| 智能層 | headless CLI Runner：**claude CLI 為主、codex CLI 為輔** | subprocess 呼叫、JSON 輸出解析；不直串 LLM API（訂閱內零 API 費用） |
| API | Go `net/http` JSON API，僅監聽 localhost | extension page 與 content script 的唯一後端介面 |
| 前端 | Chrome MV3 extension page、service worker、content script 與 sidebar | extension page 是 MVP 唯一日常操作入口；vanilla JavaScript、無建置工具鏈 |
| 排程 | systemd user timer → `jobfinder run`（one-shot、冪等） | 不做常駐排程 daemon |
| 設定 | `config.yaml` 與 `profile.yaml`（repo 內僅有去敏感 example） | config 保存執行與連線設定；Profile 保存使用者履歷與求職條件；env 不承載行為參數 |
| 時區 | Asia/Taipei，時間戳 RFC3339 | `main.go` 設 `time.Local` |

## 2. 架構圖

```
                       ┌────────────────────────────────────────────┐
 systemd timer ──────► │  jobfinder run（pipeline，one-shot 冪等）    │
 extension page / CLI ─► │                                            │
                       │  fetch ─► 條件篩選 ─► AI 評分 ─►（推薦）    │
                       │  求職信生成：使用者要求後才取件               │
                       │  ingest（104 半被動，由 capture API 轉入）    │
                       └────┬─────────┬────────────┬─────────┬──────┘
                            │         │            │         │
              ┌─────────────▼──┐   ┌──▼───────┐ ┌──▼─────────▼───────┐
              │ crawler         │   │ profile  │ │ agents             │
              │ Source adapters │   │ conditions│ │ Runner: claude CLI │
              │ Yourator│Cake   │   └──────────┘ │        codex CLI   │
              │ ＋104 解析器     │                │ Scorer/Drafter/    │
              └─────────────────┘                │ Reviewer           │
                            │                    └─────────┬──────────┘
                            ▼                              │
                       ┌───────────────────────────────────▼┐
                       │ store（SQLite）：jobs/scores/letters │◄── profile.yaml
                       │ status_events / runs                │    （版控外）
                       └──────────────▲──────────────────────┘
                                      │
                       ┌──────────────┴──────────┐      JSON API      ┌──────────────────┐
                       │ jobfinder serve（API）   │ ◄────────────────  │ Chrome extension │
                       └─────────────────────────┘  （SSH tunnel 可） │ page／104 scripts │
                                                                    └────────▲─────────┘
                                                              使用者：儀表板與 104 瀏覽
```

## 3. 模組職責與依賴

| 模組 | 位置 | 職責 | 詳細設計 |
|---|---|---|---|
| schema/store | `internal/store` | SQLite schema、migration、實體 CRUD、狀態轉換的唯一入口 | [design-schema](designs/design-schema.md) |
| profile | `internal/profile` | 載入/驗證 `profile.yaml`、PII 檢核、（B6）校準建議 | [design-profile](designs/design-profile.md) |
| crawler | `internal/crawler` | Source adapter 介面與全自動平台實作、104 解析器（輸入來自插件擷取）、去重與變更偵測輸入 | [design-crawler](designs/design-crawler.md) |
| pipeline | `internal/pipeline` | 抓取排程編排、常駐 worker（初篩／評分／求職信）、ingest 入口（104 半被動）、條件篩選、rate limit、每日預算與冪等 | [design-pipeline](designs/design-pipeline.md) |
| agents | `internal/agents` | Runner 抽象（CLI subprocess）、Scorer/Drafter/Reviewer、輸出驗證與防幻覺防線 | [design-agents](designs/design-agents.md) |
| api | `internal/api` | localhost JSON API：Job／Run 查詢、狀態變更、手動 run、104 capture；驗證 extension origin 與 token | [design-api](designs/design-api.md) |
| extension | `extension/` | Chrome MV3 插件：extension page 儀表板、service worker、104 列表收割、內頁擷取與評分 sidebar | [design-extension](designs/design-extension.md) |
| cli | `cmd/jobfinder/cli` | cobra 命令樹，薄殼呼叫各模組 | 各模組文件的「CLI 介面」節 |

依賴方向：`cli / api → pipeline → (crawler, agents, profile) → store`；extension 僅經 api 對接；store 不依賴任何上層。**契約先行**：schema、agents JSON 輸出與 API 契約先定，其餘模組依賴之。

## 4. 資料契約

- DB 實體與狀態機：見 [design-schema](designs/design-schema.md)（唯一權威）。
- Profile 檔案格式：見 [design-profile](designs/design-profile.md)。
- Agent JSON 輸出契約（ScoreResult / DraftResult / ReviewResult）：見 [design-agents](designs/design-agents.md)。

## 5. 關鍵技術決策

| 決策 | 選擇 | 理由 |
|---|---|---|
| 抓取範圍 | 依 Profile directions 導出的搜尋條件抓取，非全量；再依 Profile 求職條件排除不合適職缺 | 平台量體過大且不禮貌；Profile 是使用者求職條件的單一真相，來源設定僅處理平台專屬覆寫或增補 |
| Agent 路由與模型 | `config.yaml` 的 `llm.roles.<role>.primary/fallback` 各自指定 agent CLI 與 model | 下一輪 one-shot run 重新讀取設定；每個角色與 fallback 的 agent、model 均明確傳入 CLI，不依賴 CLI 預設值 |
| 104 供給方式 | 半被動：插件於使用者瀏覽時擷取（列表收割＋內頁擷取），不做伺服器端抓取 | 104 全站在 Cloudflare 防護後，零繞過原則下伺服器端抓取不可行；插件只記錄使用者已載入的頁面（剪藏定位），判斷仍全在 pipeline，人的介入退化為點開頁面 |
| 抓取合規邊界 | 只碰免登入公開頁、遵守 robots.txt、零繞過、fixture 內容一律合成 | 工具須可作為公開 repo 與作品集；法律風險集中在「繞過防護」與「重散布內容」兩點，皆從設計上排除 |
| 智能層串接方式 | headless CLI（claude 主 / codex 輔）而非直串 API | 訂閱內零邊際成本；Runner 介面抽象保留日後換直串 API 的空間 |
| 資料層 | SQLite 而非 PostgreSQL/YAML | 單人單機零維運；職缺量、狀態追蹤與排序查詢非檔案型儲存所長 |
| 排程 | systemd timer + one-shot `run` 而非常駐 daemon 內建排程 | 觸發/存活/正確性三關注點分離；one-shot 冪等天然支援手動重跑 |
| Profile 儲存 | 版控外 YAML 檔而非 DB | 人工編修頻繁、需版本化比對；含薪資期望等敏感值不入 repo/DB |
| 加權總分 | Go 程式計算，Agent 只回各維分數 | 權重調整不需重跑 LLM；避免 LLM 算術錯誤 |
| 求職信生成時機 | 使用者對推薦職缺按下生成才跑（`letter_requested` 取件），非評分後自動生成 | letter 是最耗 token 的階段，且系統不代投；未經使用者決定投遞的求職信不會被使用。以獨立狀態承載使用者意願，可沿用 PickForStage 的冪等取件與中斷重跑語意，不需同步長請求 |
| 判定（verdict）的導出 | 由 API viewmodel 從 `process_state` ＋現行 score 導出，不存 DB 欄位 | 判定是既有狀態的呈現層投影；存成欄位會與狀態機產生雙真相與同步問題。清單標記、sidebar 與 dashboard 共用同一份導出結果 |
| 清單頁快速判定 | 只跑欄位可用的條件篩選，不呼叫 LLM | 清單欄位不含 JD 全文，不足以支撐五維評分；使用者仍停在該頁面，回應必須即時 |
| 防幻覺 | Reviewer Agent ＋ 程式端詞表比對雙防線 | 不把正確性全押在 LLM 自審 |
| MVP 前端 | Chrome extension page | 日常操作與 104 瀏覽動線收斂為單一介面；避免維護兩套前端 |

## 6. 狀態機

權威定義在 [design-schema](designs/design-schema.md)。摘要：

```
process_state：discovered ─►（內頁擷取補全文）─► new    ※ discovered 亦可 ─► filtered_out（可用條件命中）
               new ─► filtered_out
                └──► queued ─► scored（<75，不推薦）
                          └──► shortlisted（≥75，推薦；停留待使用者決定）
                                  │ 使用者要求生成
                                  ▼
                               letter_requested ─► letter_ready
                                             └──► letter_failed ─►（使用者再次要求）letter_requested
apply_state（letter_ready 後，使用者擁有）：
  pending ─► applied ─► interview ─► offer
                   └──► ghosted        └─（任一階段可 ─► dropped）
```

`shortlisted` 是推薦職缺的停留點，不是待辦佇列：pipeline 不會主動把它推進 letter 階段（PRD R5.0）。`letter_requested` 是使用者意願的唯一表達方式，也是 letter 階段的取件狀態——這讓「按需生成」不必犧牲既有的冪等取件模型。

所有狀態變更一律經 store 的轉換函式並寫入 `status_events`，禁止直接 UPDATE 狀態欄位。

## 7. 開發順序

對應 PRD 交付計畫 B0–B6，逐模組垂直切（design → 實作 ＋ 單元測試 → fmt/lint/test 綠）：

1. **B0**：store（schema/migration）→ profile（載入＋PII 檢核）
2. **B1**：crawler（介面＋Yourator）→ pipeline（fetch＋filter）→ CLI 檢視
3. **B2**：agents（Runner＋Scorer）→ pipeline 接上 score 階段
4. **B3**：agents（Drafter＋Reviewer）→ pipeline 接上 letter 階段
5. **B4**：api → extension page dashboard → systemd timer 部署（[deploy](deploy.md)）
6. **B5**：104 半被動組——`queries urls` 生成、crawler 104 解析器、pipeline ingest＋`discovered` 流程、capture API 與判定回傳、Chrome extension content script／sidebar 與清單就地標記
7. **B6**：crawler（Cake）→ profile 校準

介面若需變動，回頭改本文件的模組邊界，不只改單一模組文件。

## 8. 測試策略

| 層級 | 作法 |
|---|---|
| L1 單元 | 每模組同檔 `_test.go`；store 用暫存目錄真 SQLite；crawler 用 `httptest` 假伺服器餵結構仿真、內容合成的回應 fixture；agents 用 fake Runner（罐頭 JSON）；API 用 `httptest`；extension 用單元測試或 mock API，不進真實瀏覽器 |
| L2 mock | `e2e-mock` 以物化 binary、合成來源、fake Runner、真 SQLite/API/systemd 與隔離 Chromium extension 模擬驗證可重現的跨模組流程 |
| Live E2E | `e2e-live` 以獨立設定／SQLite 驗證真實公開來源與已授權 CLI Runner；至少取得一筆真資料並驗證格式，外部能力不足明確標為 `ENVIRONMENT_BLOCKED` |
| 人工 gate | 驗收者在實際 Chrome 載入同一 extension artifact；自動隔離 Chromium 不取代安裝、權限、origin 與相容性結論 |
| 負向 | Agent 輸出非法 JSON、缺欄位、幻覺技能、缺佔位符；來源回應被擋/改版；capture payload 缺欄位；`run` 中斷重跑冪等 |

`docs/tests/test-<module>.md` 與 `docs/deploy.md` 依交付節奏維護；B0–B4 已有對應測試規格，B4 部署步驟見 [deploy](deploy.md)。
