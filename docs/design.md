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
| API | Go `net/http` JSON API，僅監聽 localhost | Side Panel 與 content script 的唯一後端介面 |
| 前端 | Chrome MV3 Side Panel、service worker 與 content script | Side Panel 是 MVP 唯一日常操作入口；vanilla JavaScript、無建置工具鏈 |
| 排程 | systemd user timer → `jobfinder run`（one-shot、冪等） | 不做常駐排程 daemon |
| 設定 | `config.yaml` 與 `profile.yaml`（repo 內僅有去敏感 example） | config 保存執行與連線設定；Profile 保存使用者履歷與求職條件；env 不承載行為參數 |
| 時區 | Asia/Taipei，時間戳 RFC3339 | `main.go` 設 `time.Local` |

## 2. 架構圖

```
systemd timer／Side Panel／CLI
              │
              ▼
 jobfinder run（one-shot、只 fetch）──► crawler adapters ──► store（SQLite）
                                                              ▲
 Chrome extension ── localhost JSON API ──► jobfinder serve ──┤
   ├ Side Panel／Profile editor                 ├ capture ingest│
   └ 104 content scripts                        └ resident worker
                                                      │
                    profile.yaml ──► Profile provider ├ filter ─► agents（語意條件）
                          ▲             snapshot       ├ score ──► agents
                          └── GET／PUT Profile API     └ letter ─► agents

Profile 儲存：原子寫入 YAML ──► provider snapshot 切換
手動重新處理：POST reprocess ──► store revision transaction ──► worker 消化
```

## 3. 模組職責與依賴

| 模組 | 位置 | 職責 | 詳細設計 |
|---|---|---|---|
| schema/store | `internal/store` | SQLite schema、migration、實體 CRUD、狀態轉換的唯一入口 | [design-schema](designs/design-schema.md) |
| profile | `internal/profile` | strict 載入/驗證與 PII 檢核、canonical YAML／ETag／雙 revision、`derived` 加總物化、原子寫入、runtime snapshot provider、（S1）校準建議 | [design-profile](designs/design-profile.md) |
| crawler | `internal/crawler` | Source adapter 介面與全自動平台實作（Yourator）、104／Cake 半被動解析器（輸入來自插件擷取）、去重與變更偵測輸入 | [design-crawler](designs/design-crawler.md) |
| pipeline | `internal/pipeline` | 抓取排程編排、revision-aware 常駐 worker、Profile activation 與既有 Job 重新處理、ingest、硬規則篩選（結構化比對與語意篩選編排）、跨來源分群鉤點、rate limit、每日預算與冪等 | [design-pipeline](designs/design-pipeline.md) |
| agents | `internal/agents` | Runner 抽象（CLI subprocess）、Filter/Scorer/Drafter/Reviewer/Calibrator、輸出驗證與防幻覺防線 | [design-agents](designs/design-agents.md) |
| api | `internal/api` | localhost JSON API：Profile 條件式讀寫、Job／Run 查詢、狀態變更、手動 run、重複裁決、各平台 capture；驗證 extension origin 與 token | [design-api](designs/design-api.md) |
| extension | `extension/` | Chrome MV3 插件：原生 Side Panel、全頁 Profile 編輯器、service worker、104／Cake 列表收割與內頁擷取 | [design-extension](designs/design-extension.md) |
| cli | `cmd/jobfinder/cli` | cobra 命令樹，薄殼呼叫各模組 | 各模組文件的「CLI 介面」節 |

依賴方向：`cli / api → pipeline → (crawler, agents, profile) → store`；extension 僅經 api 對接；store 不依賴任何上層。**契約先行**：schema、agents JSON 輸出與 API 契約先定，其餘模組依賴之。

## 4. 資料契約

- DB 實體與狀態機：見 [design-schema](designs/design-schema.md)（唯一權威）。
- Profile 檔案格式：見 [design-profile](designs/design-profile.md)。
- Profile revision 欄位、activation 與現行 Score 查詢：見 [design-schema](designs/design-schema.md)。
- Profile GET／PUT、ETag 與 Job stale viewmodel：見 [design-api](designs/design-api.md)。
- Agent JSON 輸出契約（FilterResult / ScoreResult / DraftResult / ReviewResult）：見 [design-agents](designs/design-agents.md)。

## 5. 關鍵技術決策

| 決策 | 選擇 | 理由 |
|---|---|---|
| 抓取範圍 | 依 Profile `search.directions` 導出的搜尋條件抓取，非全量；再依 Profile 求職條件排除不合適職缺 | 平台量體過大且不禮貌；Profile 是使用者求職條件的單一真相，來源設定僅處理平台專屬覆寫或增補。地區只有 `requirements.locations` 一份：搜得比可接受地區更廣的職缺一律被同一份硬規則淘汰，兩份設定的交集才是實際結果 |
| 判定結構 | 拆成兩關：篩選（硬規則）決定適合與否、評分（軟規則）決定推不推薦；兩關分離呼叫，不合併成一次 | 硬性條件不符是「不適合」而非「低分」，混入加權會稀釋分數意義；分離讓不合格職缺不進入完整評分（省 token），並使 Profile 的硬／軟設定在使用者心智上對應到兩個獨立結果 |
| 硬規則的落點 | 結構化條件由程式比對；學歷、必備技能、產業對應等語意條件交 Filter Agent；年資與各項加總一律由程式以 `derived` 計算 | 程式能判的不付 token，也不受 LLM 算術錯誤影響；LLM 只做它擅長的語意對應（JD 要求對應到哪些產業／科系相容性） |
| 資訊不足的處置 | 缺資訊絕不判不適合。逐條判定照實記 `unknown`，彙總時：清單摘要有 `unknown` 歸「待看」等補全文，全文 JD 有 `unknown` 則照常進「待評分」 | 漏掉機會的代價遠高於多評一筆的 token。摘要是節錄，補上全文後可能就判得出來；全文則不會再有新資訊，扣住它只會讓它永遠停在待看 |
| 評分輸入 | 只餵 `intents` 與判定所需的 `qualifications` 子集，不餵履歷敘事（成就、角色、誠實邊界） | 成就敘事會被讀成「擅長 ⇒ 適配高」，使用者「做過但不想再做」的內容只會加分不會扣分；切斷這條污染是分離設計的核心動作 |
| 雙 revision | `filter_revision` 與 `score_revision` 各自只 hash 自己涵蓋的欄位 | 只調軟偏好時不必重跑硬篩，重跑成本與變更範圍對齊；跨關共用的欄位同時進兩組 hash |
| Agent 路由與模型 | `config.yaml` 的 `llm.roles.<role>.primary/fallback` 各自指定 agent CLI 與 model | 下一輪 one-shot run 重新讀取設定；每個角色與 fallback 的 agent、model 均明確傳入 CLI，不依賴 CLI 預設值 |
| 104／Cake 供給方式 | 半被動：插件於使用者瀏覽時擷取（列表收割＋內頁擷取），不做伺服器端抓取 | 104 全站在 Cloudflare 防護後；Cake 內容開放但搜尋路徑同樣在人機驗證後，伺服器端無法以求職條件挑出目標職缺。零繞過原則下兩者的自動抓取皆不可行；插件只記錄使用者已載入的頁面（剪藏定位），判斷仍全在 pipeline，人的介入退化為點開頁面。**半被動是多平台擴充的主路線，全自動是例外** |
| 跨來源重複職缺 | 程式規則分群（公司＋職稱正規化＋地區），高信心自動合併、灰帶交使用者裁決；群組內只有 canonical 承載處理與投遞，alias 轉 `merged` | 同一職缺重複刊登是常態，不合併就是重複評分、重複生成信件與投遞狀態分裂。規則法可測試、零 LLM 成本；自動只做確定的部分，不確定的交人，符合 Human-in-the-Loop。選 canonical＋alias 而非把狀態搬到 group，是為了不動既有狀態機與 revision CAS 契約 |
| 抓取合規邊界 | 只碰免登入公開頁、遵守 robots.txt、零繞過、fixture 內容一律合成 | 工具須可作為公開 repo 與作品集；法律風險集中在「繞過防護」與「重散布內容」兩點，皆從設計上排除 |
| 智能層串接方式 | headless CLI（claude 主 / codex 輔）而非直串 API | 訂閱內零邊際成本；Runner 介面抽象保留日後換直串 API 的空間 |
| 資料層 | SQLite 而非 PostgreSQL/YAML | 單人單機零維運；職缺量、狀態追蹤與排序查詢非檔案型儲存所長 |
| 排程 | systemd timer + one-shot `run` 而非常駐 daemon 內建排程 | 觸發/存活/正確性三關注點分離；one-shot 冪等天然支援手動重跑 |
| Profile 儲存 | 版控外 YAML 檔而非 DB；extension 經受控 API 編輯 | 保留可攜的單一檔案真相；含薪資期望等敏感值不入 repo/DB，外部修復仍可用 ETag 偵測衝突 |
| Profile runtime | 同步化 provider 管理 immutable snapshot；工作開始時固定取得 Profile、canonical YAML、ETag 與兩個 revision | 儲存成功可立即生效，同時避免單一工作途中混用兩個版本 |
| Profile 身分與衝突 | 各關涵蓋欄位的 canonical 結構 SHA-256 作 `filter_revision`／`score_revision`；精確檔案 bytes 的 ETag 配合 `If-Match` | revision 判斷語意 stale／冪等，ETag 防止 UI 覆蓋外部檔案修改 |
| Profile 缺少 | `serve` 以 setup 模式啟動，讀取與 Profile API 可用，處理型入口與 worker 暫停 | 避免首次建立 Profile 必須先人工造檔的啟動死結 |
| Profile 變更 | 儲存只切換 active snapshot；store 專用 activation 僅由使用者手動要求，依 revision 重新處理尚未進入求職信流程的 Job；letter 與 apply 歷史受保護 | 新職缺立即採用新條件，既有評分先保留供使用者辨識與控制重評成本 |
| 加權總分 | Go 程式計算，Agent 只回各維分數 | 權重調整不需重跑 LLM；避免 LLM 算術錯誤 |
| 評分計分方式 | 基準分制：各維以門檻分為起點加減、clamp 0–100，無資訊可判時回基準分；`bonus_fit` 只加不減 | 絕對給分會讓「沒寫」與「不符」不可區分；加分條件未滿足若扣分，列越多加分項的 JD 分數越低，方向就反了 |
| 求職信生成時機 | 使用者對推薦職缺按下生成才跑（`letter_requested` 取件），非評分後自動生成 | letter 是最耗 token 的階段，且系統不代投；未經使用者決定投遞的求職信不會被使用。以獨立狀態承載使用者意願，可沿用 PickForStage 的冪等取件與中斷重跑語意，不需同步長請求 |
| 判定（verdict）的導出 | 由 API viewmodel 從 `process_state` ＋現行 score 導出，不存 DB 欄位 | 判定是既有狀態的呈現層投影；存成欄位會與狀態機產生雙真相與同步問題。清單標記與 Side Panel 共用同一份導出結果 |
| 清單頁快速判定 | 只跑欄位可用的結構化硬規則，不呼叫 LLM | 清單欄位不含 JD 全文，不足以支撐語意篩選與四維評分；使用者仍停在該頁面，回應必須即時 |
| 防幻覺 | Reviewer Agent ＋ 程式端詞表比對雙防線 | 不把正確性全押在 LLM 自審 |
| MVP 前端 | Chrome 原生 Side Panel | 日常操作與 104 瀏覽動線收斂為可持續顯示的單一窄幅介面；避免維護 popup 與網站 overlay 兩套完整前端 |

## 6. 狀態機

權威定義在 [design-schema](designs/design-schema.md)。摘要：

```
process_state：discovered ─►（內頁擷取補全文）─► new    ※ discovered 亦可 ─► filtered_out（可用條件命中）
               任一狀態 ─►（判定為重複刊登）─► merged ─►（使用者取消合併）─► 合併前狀態
               new ─► filtered_out（任一硬規則 fail）
                ├──► discovered（僅有摘要，待看）─►（補全文）─► new
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

Profile activation 是一般狀態機之外、僅由使用者手動要求的 store 專用入口，以 expected state ＋ expected revision 做 compare-and-set。它可將尚未進入求職信流程的 Job 切到 active revision 並依變更的 revision 重新篩選或重新評分；Profile 儲存與服務啟動皆不自動呼叫。求職信與投遞歷史不回退。stale 是由 active revision 與產出 revision 比對所得的 viewmodel，不是資料庫狀態。

## 7. 開發順序

對應 PRD 交付計畫 B0–B7，逐模組垂直切（design → 實作 ＋ 單元測試 → fmt/lint/test 綠）：

1. **B0**：store（schema/migration）→ profile（載入＋PII 檢核）
2. **B1**：crawler（介面＋Yourator）→ pipeline（fetch＋filter）→ CLI 檢視
3. **B2**：agents（Runner＋Scorer）→ pipeline 接上 score 階段
4. **B3**：agents（Drafter＋Reviewer）→ pipeline 接上 letter 階段
5. **B4**：api → extension Side Panel dashboard → systemd timer 部署（[deploy](deploy.md)）
6. **B5**：104 半被動組——crawler 104 解析器、pipeline ingest＋`discovered` 流程、capture API 與判定回傳、Chrome extension content script 與清單就地標記
7. **B6**：crawler（Cake 解析器）→ extension Cake content script → store／pipeline／api 跨來源分群與裁決
8. **B7**：profile（六區段 schema、`derived`、雙 revision）→ store（schema v6：篩選結果、新狀態、既有職缺重置）→ agents（Filter agent、四維 Scorer）→ pipeline（兩關 filter、雙 revision 重跑範圍）→ api（verdict 擴充、篩選逐條結果）→ extension（六區段 Profile 表單、篩選未通過原因）

介面若需變動，回頭改本文件的模組邊界，不只改單一模組文件。

## 8. 測試策略

| 層級 | 作法 |
|---|---|
| L1 單元 | 每模組同檔 `_test.go`；store 用暫存目錄真 SQLite；crawler 用 `httptest` 假伺服器餵結構仿真、內容合成的回應 fixture；agents 用 fake Runner（罐頭 JSON）；API 用 `httptest`；extension 用單元測試或 mock API，不進真實瀏覽器 |
| L2 mock | `e2e-mock` 以物化 binary、合成來源、fake Runner、真 SQLite/API/systemd 與隔離 Chromium extension 模擬驗證可重現的跨模組流程 |
| Live 驗收 | `verify-live` 在測試環境的實際安裝上驗證真實公開來源與已授權 CLI Runner；至少取得一筆真資料並驗證格式，外部能力不足明確標為 `ENVIRONMENT_BLOCKED` |
| 人工 gate | 驗收者在實際 Chrome 載入同一 extension artifact；自動隔離 Chromium 不取代安裝、權限、origin 與相容性結論 |
| 負向 | Agent 輸出非法 JSON、缺欄位、幻覺技能、缺佔位符；來源回應被擋/改版；capture payload 缺欄位；`run` 中斷重跑冪等 |

`docs/tests/test-<module>.md` 與 `docs/deploy.md` 依交付節奏維護；B0–B7 已有對應測試規格，B4 部署步驟見 [deploy](deploy.md)。
