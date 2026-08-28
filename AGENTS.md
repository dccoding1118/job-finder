# job-finder 專案開發指南 (AGENTS.md)

為接手開發或維運本專案的人與 coding agent 準備：專案定位、環境、結構索引、開發規則與驗證方式。使用者面的簡介、安裝與操作見 [README.md](README.md)。

## 1. 專案概覽

`jobfinder` 是匿名 AI 求職媒合工具：從合規的職缺來源收集職缺，以本地硬規則與 Agent 兩關判定篩出高匹配職缺並標示判定；使用者對推薦職缺要求後才產出經審查的客製化求職信，最後手動投遞並追蹤狀態。

**產品形態**：Chrome extension 是產品本體——它同時是唯一 UI，也是瀏覽輔助模式（`semi_passive`）下取得職缺資訊的唯一載體，不會被 Web UI 取代。後端是可替換的媒合引擎，同一份程式碼支撐「使用者自部署」與「代管雲端」兩種部署，extension 對後端只認 endpoint ＋ auth。目前實作到自部署形態（單機、SQLite、loopback API、Bearer token）；帳號、計費與多租戶不在本 repo。本專案採 AGPL-3.0，不接受外部 PR。

**執行模型**：排程只驅動 fetch，filter／score／letter 由 API service 內的常駐 worker 非同步消化。排程批次、CLI 與 extension 三個入口寫入的職缺共用同一條消化路徑；`runs` 只記錄該輪批次的執行事實，判定分布於檢視時即時查詢。

### 架構總覽

```
   ┌──────────────────────────┐        ┌───────────────────────────────┐
   │  Chrome extension (MV3)  │        │  排程（systemd timer /        │
   │  ├ Side Panel（唯一 UI） │        │        Task Scheduler）       │
   │  ├ Profile 全頁編輯器    │        └───────────────┬───────────────┘
   │  └ 瀏覽輔助模組          │                        │ jobfinder run
   └────────────┬─────────────┘                        │（one-shot、冪等）
                │ loopback JSON API                    ▼
                │ (Bearer token)              ┌──────────────────┐
                ▼                             │  來源模組        │
   ┌──────────────────────────┐               │  （批次比對）    │
   │  jobfinder serve         │◄──────────────┴────────┬─────────┘
   │  ├ JSON API              │                        │
   │  └ 常駐 worker           │                        │
   │     filter → score       │                        ▼
   │           → letter       │──────────────►  ┌─────────────┐
   └────────────┬─────────────┘                 │   SQLite    │
                │                               └─────────────┘
                ▼                                      ▲
   ┌──────────────────────────┐                        │
   │  headless agent CLI      │      profile.yaml ──────┘
   │  claude（主）／codex（輔）│      （版控外的單一真相）
   │  Filter / Scorer /       │
   │  Drafter / Reviewer      │
   └──────────────────────────┘
```

| 模組 | 職責 | 設計文件 |
|---|---|---|
| `internal/store` | SQLite schema、migration、實體 CRUD、狀態轉換的唯一入口、跨來源分群與合併 | `design-schema` |
| `internal/profile` | strict 載入與 PII 檢核、canonical YAML／ETag／雙 revision、`derived` 加總、runtime snapshot provider | `design-profile` |
| `internal/crawler` | Source adapter 介面與批次比對實作（Yourator）、瀏覽輔助來源的頁面解析器、去重與內容變更偵測 | `design-crawler` |
| `internal/pipeline` | fetch／filter／score／letter 編排、常駐 worker、Profile activation 與重新處理、每日預算與鎖 | `design-pipeline` |
| `internal/agents` | CLI Runner 抽象、Filter／Scorer／Drafter／Reviewer／Calibrator、輸出驗證與防幻覺防線 | `design-agents` |
| `internal/api` | loopback JSON API：Profile 條件式讀寫、Job／Run／狀態／verdict、各平台 capture、重複裁決 | `design-api` |
| `extension/` | Chrome MV3：Side Panel、Profile 編輯器、瀏覽輔助模組（清單就地標記與職缺頁資訊補全） | `design-extension` |
| `internal/install`、`internal/paths` | 跨平台路徑決策與 `install`／`update`／`rollback` 語意 | `docs/deploy.md` |
| `cmd/jobfinder/cli` | cobra 命令樹，薄殼呼叫各模組 | 各模組文件的「CLI 介面」節 |

依賴方向：`cli / api → pipeline → (crawler, agents, profile) → store`；extension 僅經 api 對接，store 不依賴任何上層。

## 2. 主要技術與環境

| 項目 | 內容 |
|---|---|
| 語言 | Go 1.23.12；module `github.com/dccoding1118/job-finder`；入口 `./cmd/jobfinder` |
| CLI | `spf13/cobra`；`cmd/jobfinder/cli/` 每個子命令一檔 |
| 工具鏈 | `mise` 管理 Go、gofumpt、golangci-lint、node；任務定義於 `mise.toml` |
| 資料庫 | SQLite，使用 `modernc.org/sqlite`，不依賴 cgo |
| 設定與 Profile | `config.yaml` 與 `profile.yaml` 為本機檔案，均不得納入版控 |
| 智能層 | headless CLI Runner：claude CLI 為主、codex CLI 為輔；不直接串接 LLM API |
| API 與前端 | Go `net/http` JSON API 僅監聽 loopback；Chrome MV3 原生 Side Panel 為唯一日常操作入口 |
| 排程 | Linux：systemd user timer；Windows：Task Scheduler。皆觸發一次性且冪等的 `jobfinder run` |
| 部署平台 | Linux 與 Windows；路徑決策集中在 `internal/paths`，安裝語意集中在 `jobfinder install`／`update`／`rollback` |
| 時間 | `Asia/Taipei`，時間戳採 RFC3339 |

Go 不保證在裸 PATH；以 `mise run <task>` 或 `mise exec -- go <args>` 執行。

## 3. 結構與維護索引

**一句話結構**：`cmd/jobfinder/` 是薄入口與 Cobra 命令樹；`internal/` 承載業務模組；`extension/` 是提供操作介面與瀏覽輔助的 Chrome MV3 extension；`docs/` 是需求與設計的權威來源。

**目錄結構**：

| 路徑 | 內容 |
|---|---|
| `cmd/jobfinder/` | 薄入口與 Cobra 命令樹（`run`／`serve`／`install`／`profile`／`paths` 等，每個子命令一檔） |
| `internal/` | 業務模組：`store`／`profile`／`crawler`／`pipeline`／`agents`／`api`／`install`／`paths`／`logging`／`version` |
| `extension/` | Chrome MV3 extension：Side Panel、Profile 編輯器、瀏覽輔助模組 |
| `deploy/production/` | `systemd/` 三個 unit 與 `windows/` 兩個 Task Scheduler 模板 |
| `scripts/` | `bootstrap/`（下載安裝）、`deploy/`（開發 checkout 安裝）、`verify/`（驗收 harness） |
| `configs/` | 設定與 Profile 範例、驗收用設定；安裝流程由此渲染實際 `config.yaml` |
| `docs/` | 需求與設計的權威來源 |

`bin/`、`.local-dev/`、`node_modules/`、`test-results/` 為 gitignored。

**維護索引**：要動某主題 → 先看設計文件、再改對應碼。

| 主題 | 先看 | 實作位置 |
|---|---|---|
| SQLite schema、migration、實體 CRUD、狀態轉換、跨來源職缺分群與合併 | `docs/designs/design-schema.md` | `internal/store/` |
| 匿名 Profile、PII 檢核、canonical serialization、ETag／雙 revision、`derived` 加總、runtime provider、校準建議 | `docs/designs/design-profile.md` | `internal/profile/` |
| Source adapter（批次比對來源）、去重、內容變更偵測、瀏覽輔助來源的 payload 解析 | `docs/designs/design-crawler.md` | `internal/crawler/` |
| fetch／filter／score／letter 編排、Profile activation 與重新處理入隊、執行記錄、revision-aware CAS、每日預算與鎖 | `docs/designs/design-pipeline.md` | `internal/pipeline/` |
| CLI Runner、Filter、Scorer、Drafter、Reviewer、Calibrator 與輸出驗證 | `docs/designs/design-agents.md` | `internal/agents/` |
| Profile 讀寫、Job stale viewmodel、Job／Run／狀態／verdict／求職信要求與產製歷程／單筆重新處理／處理進度／手動 run／重複裁決與各平台 capture API | `docs/designs/design-api.md` | `internal/api/` |
| Side Panel、全頁 Profile editor、清單就地標記與職缺頁資訊補全、SPA 模式路由、疑似重複裁決 | `docs/designs/design-extension.md` | `extension/` |
| 搜尋條件展開（只用於批次比對來源） | `docs/designs/design-crawler.md` §5 | `cmd/jobfinder/cli/queries.go` |
| 跨平台路徑決策、`jobfinder paths` | `docs/deploy.md` §2 | `internal/paths/` |
| 安裝／更新／回滾、排程掛載、生效面驗證 | `docs/deploy.md` §4 | `internal/install/`、`deploy/production/systemd/`、`deploy/production/windows/` |
| 日誌出口與輪替 | `docs/deploy.md` §2 | `internal/logging/` |
| 版號解析與注入 | `docs/deploy.md` §7 | `internal/version/` |
| CLI 命令樹 | 各模組的 CLI 介面節 | `cmd/jobfinder/cli/` |

**頂層文件**：`docs/PRD.md`（需求與範圍）、`docs/design.md`（系統架構、關鍵技術決策與開發順序）、`docs/roadmap.md`（產品定位與階段規劃）、`docs/deploy.md`（部署契約）、`docs/verify.md`（累加式整合與驗收）、`docs/guides/getting-started.md`（上手與操作）、`docs/guides/runbook-upgrade.md`（發版後三條 lane 的換版步驟）。各模組實作契約在 `docs/designs/design-<module>.md`、單元測試規劃在 `docs/tests/test-<module>.md`。

**變更紀錄**：`docs/changes/change-<slug>.md`——記某次變更的動機、決策與落點（**非 canonical**，最新狀態一律讀被覆蓋的 canonical 文件本身）。既有主題的變更先寫此檔、再就地更新 canonical。

**交接熱副本**：`STATUS.md` 只保留未歸檔結論與未完成任務，不承載專案設計。

### 快速定位

- **新增一個 CLI 子命令**：`cmd/jobfinder/cli/` 加一個檔案並掛到 `root.go`；`main.go` 維持薄入口。
- **新增一個職缺來源**：先判斷該網站的搜尋條件能否由後端直接組成——可以的話在 `internal/crawler/` 實作批次比對 adapter，否則走瀏覽輔助（一組頁面解析器 ＋ 一組 URL pattern ＋ `extension/content/` 的注入設定）。
- **改判定規則**：結構化硬條件在 `internal/pipeline/`，語意條件與 prompt 在 `internal/agents/`；兩者的分工判準見 §4 開發規則 2 與 `docs/design.md` §5。
- **改 Profile 欄位**：`internal/profile/` 的 schema 與 migration、`derived` 加總、雙 revision 涵蓋範圍，再連動 `extension/profile/` 的表單。改動涵蓋欄位會改變 revision，須確認重跑範圍符合預期。
- **改路徑或安裝行為**：只改 `internal/paths` 與 `internal/install`，不要在 CLI 旗標預設值另寫一份；排程模板在 `deploy/production/`。
- **改 release 工件或平台矩陣**：`.github/workflows/release.yml`；版號注入的符號路徑與 `internal/version` 的變數名是字串綁定，改名須同步兩處。
- **排查「到底讀了哪份設定」**：`jobfinder paths` 是第一站。

## 4. 開發規則

1. **文件為主要依循體**：實作前先對照 `docs/PRD.md`、`docs/design.md` 與目標模組設計。若文件與程式碼衝突，停止實作並請開發者裁決；不得自行讓任一方遷就另一方。
2. **契約先行**：先定 schema、Profile 格式與 Agent JSON 契約，再實作依賴它們的模組。所有狀態轉換只能經 `internal/store` 的轉換函式，並寫入 `status_events`；禁止直接 UPDATE 狀態欄位。
3. **Zero-PII**：資料庫、版控內容、fixture、log 與求職信不得包含姓名、Email、電話、身分證字號、學校或公司名稱。求職信落款固定使用 `[你的姓名]`、`[你的聯絡方式]`。JD 原文可能夾帶招募方的 Email 或手機：`UpsertJob` 於入庫前遮罩為 `[EMAIL]`／`[PHONE]`（`content_hash` 亦以遮罩後文字計算），`SaveAgentCall` 同樣遮罩後才寫入、遮罩後仍命中則拒寫。因此資料庫、下游 prompt 與稽核副本一律不帶聯絡資訊。
4. **Human-in-the-Loop**：系統只產生建議與求職信；求職信只在使用者對推薦職缺要求後才生成（`letter_requested` 是 letter 階段的唯一取件狀態），投遞必須由使用者手動完成。Profile 內容只有使用者明確儲存時才可寫入；revision 變更不得自動重生或覆蓋求職信，也不得改寫投遞歷史。Profile 校準只產生 diff 建議，不得自動改寫。
5. **來源合規**：只處理免登入的公開頁面、遵守 robots.txt 且不繞過任何防護。瀏覽輔助只作用於使用者瀏覽器中已經開啟的頁面；不得背景開分頁、不得成批取得內容或規避驗證，也不得繞過人機驗證直接呼叫該網站前端所用的搜尋 API。**瀏覽輔助是新增來源的預設路線**，批次比對只在該網站的搜尋條件可由後端直接組成時採用。
6. **Cobra 慣例**：`main.go` 維持薄入口；每個 resource 或子命令各有一個 `cli` 檔案；輸出使用 `cmd.OutOrStdout()` 或 `cmd.OutOrStderr()`，便於測試取得輸出。
7. **正式文件與註解只寫最新狀態**：變更歷程不散落正文；詳細規格以文字與表格呈現，不把整段程式碼當文件內容。

### 領域不變量（改動判定邏輯前必讀）

這些規則不是偏好，違反其中任何一條都會產生靜默的錯誤判定或重複計費。

| 不變量 | 違反的後果 |
|---|---|
| 缺資訊絕不判不適合：逐條記 `pass`／`fail`／`unknown`，任一 `fail` 即不適合；無 `fail` 時摘要留 `discovered`（待看）、全文進 `queued` | 把 `unknown` 當 `fail` 會靜默淘汰機會，且使用者看不出被淘汰的理由 |
| `location` 是必填欄位，來源未陳述時存哨兵值 `store.LocationUnknown`；地區規則對它與空字串一律判未決 | 哨兵值被當一般字串比對時，「缺資訊」會變成「不適合」 |
| 地區鍵只命中它自己所指的地點：`taiwan` 專指只寫國別、未寫縣市的 JD；「不限台灣任何地點」＝選入 `overseas` 以外的全部鍵 | 讓縣市鍵命中「台灣」會使地區條件形同虛設 |
| 年資與產業年資的 verdict 一律由 Go 以 `derived` 覆寫，LLM 只讀出 JD 要求的數值與 industry key | LLM 算術錯誤會直接變成錯誤淘汰 |
| `remote` 的 `required`／`rejected` 視「未提及」為現場，不產生 `unknown` | 產生 `unknown` 會讓絕大多數 JD 卡在待看 |
| `bonus` 條件不進篩選彙總，只由評分關的 `bonus_fit` 重用；`bonus_fit` 只加不減 | 加分條件未滿足若扣分，列越多加分項的 JD 分數越低，方向就反了 |
| 評分關 prompt 不含 `experiences` 的敘事欄位與 `honesty_bounds` | 成就敘事會被讀成「擅長 ⇒ 適配高」，使用者「做過但不想再做」的內容只會加分不會扣分 |
| `filtered_out` 對來源內容變更是終局的：補全文只更新內容，不重開判定、不再付一次 Filter Agent。推翻它的唯一路徑是使用者的單筆重新處理（`POST /api/v1/jobs/{id}/reprocess`） | 每次內容變更都重開判定＝重複計費，且摘要階段的 `fail` 本就用同一組硬規則判出 |
| `jobs` 的 revision 跟隨**處理**而非內容：內容變更但狀態不重置（`scored` 等終端狀態）時必須保留原 revision | 覆寫會使既有評分在清單上讀成無分數 |
| 跨來源合併**只比較來源未重疊的群組**；同平台的兩筆是兩個開口，不合併也不提出裁決 | 合併同平台的兩個開口會讓使用者失去投遞管道的選擇 |
| Scorer 的 `reason` 由 prompt 要求 40~60 字、驗證容忍到 100 字，兩者不得相等 | 相等會讓略微超出就整筆重跑，白付 token |
| 單筆插隊的閘門守的是**單次 Agent 呼叫**而非整趟批次；插隊不受自動處理開關與每日預算限制，用量照常計入 | 守整趟批次會讓插隊等到整批跑完，失去插隊的意義 |
| 瀏覽輔助來源的搜尋條件由使用者在該網站自行設定，系統不生成搜尋 URL；`jobfinder queries show` 只列印批次比對來源的展開 query | 代為組裝搜尋連結等同跨過該網站的人機驗證邊界 |
| 抓取一律邊抓邊寫：來源逐批交付，pipeline 收到即入庫並更新 `runs` 的心跳。`TouchRun` 對已收尾的輪次是 no-op | 整批回傳會讓十分鐘的抓取在資料庫上完全靜止，正常與卡死無從分辨，中途中止則全批作廢；遲到的回報若能改寫已收尾的輪次，歷史統計會被覆寫 |
| 進行中的 Agent 工作只存行程記憶體（`pipeline.Activity`），不落 DB | 落 DB 會在每次異常結束後留下永遠清不掉的假進行中，比沒有這個訊號更糟 |
| Cake 是 SPA：`extension/content/` 對 `https://www.cake.me/*` 單一注入，由腳本內自行路由；職缺頁只讀渲染後 DOM，不讀 `__NEXT_DATA__` | MV3 content script 只對文件載入求值，soft navigation 後活著的是舊頁腳本且無錯誤，功能靜默失效 |

判定共六類：`unfit`／`pending_detail`（待看）／`pending_screen`（篩選中，`new`）／`pending_score`（評分中，`queued`）／`not_recommended`／`recommended`。判定名稱說的是系統正在做什麼，不是狀態名；由 API viewmodel 從 `process_state` ＋現行 score 導出，不存 DB 欄位。

## 5. 怎麼跑測試

```bash
mise run fmt          # 格式化 Go 原始碼
mise run build        # 產生 bin/jobfinder
mise run test         # 執行 Go 單元測試
mise run lint         # 執行 golangci-lint
```

| 層級 | 範圍 | 方式 |
|---|---|---|
| L1 單元 | 模組內邏輯與負向案例 | 同檔 `*_test.go`；store 使用暫存目錄中的真 SQLite；crawler 使用 `httptest`；agents 使用 fake Runner |
| L2 整合 | pipeline 跨模組流程 | `mise run e2e-mock` 以合成來源、fake Runner 與 extension 模擬驗證；不代表真外部依賴 |
| Live E2E | 真實來源與 CLI Runner | `mise run e2e-live` 使用獨立設定與 SQLite；至少抓回一筆真資料並驗證格式，不得退回 mock |
| 人工 gate | 實際 Chrome 插件、實機部署 | 依 `docs/verify.md` 由驗收者載入同一 artifact；自動隔離 Chromium 不得替代 |

每個完成的模組都應先通過 `mise run fmt`、`mise run lint`、`mise run test`，再進入下一個模組。

開發批次完成後，先以 `scripts/verify/harness/deploy.sh` 把受測 binary 與驗收資源物化到 `.local-dev/verify/`，再執行該批次的 `scripts/verify/run-*.sh` 與 `docs/verify.md` 案例。入口 runbook（`run-*.sh`、`reset.sh`）與共用 `lib.sh` 在 `scripts/verify/`；建置 harness（`deploy.sh`、`fake-agent.sh`）在 `scripts/verify/harness/`、斷言 oracle 在 `scripts/verify/oracle/`、browser E2E 與 Playwright 設定在 `scripts/verify/browser/`；mock／live 各自的 SQLite 與證據位於 gitignored 的 `.local-dev/verify/` 且不得共用外部依賴設定。正式排程模板位於 `deploy/production/`，不由驗收腳本安裝。

## 6. 怎麼部署

MVP 以 `jobfinder run` 作為 one-shot 的批次更新，由每日排程觸發（Linux systemd user timer、Windows Task Scheduler）；API service 只綁 loopback 並承載常駐 worker，Side Panel 透過其設定的 loopback endpoint 存取。支援的形態是後端與瀏覽器同機。extension 的 `host_permissions` 只涵蓋 loopback，endpoint 恆為本機位址；後端放在別台機器時，把那台的 loopback port 轉送到本機由使用者自理。

**安裝語意集中在 binary 的 `install`／`update`／`rollback` 子命令**（`internal/install`），Linux 與 Windows 共用同一份實作，平台差異只剩排程掛載與執行檔數量：Windows 另裝一支 GUI subsystem 的 `jobfinderw.exe` 給排程執行（否則常駐服務會在桌面留一個主控台視窗），兩支同版、一起更新與回滾。`scripts/bootstrap/install.sh`／`install.ps1` 只負責下載、驗 `SHA256SUMS` 與解壓，分三種模式（不帶旗標只裝後端，`--extension`／`-Extension` 只裝 extension，`--all`／`-All` 兩者都裝）：後端模式解壓後依常駐 binary 是否存在交棒 `install` 或 `update`；extension 模式把 extension zip 解壓到 `<資料目錄>/jobfinder/extension/<tag>` 並印出 Chrome 的人工步驟——extension 沒有任何安裝語意，所以這條路只在腳本內，不進子命令；`scripts/deploy/*.sh`（`mise run deploy-*`）是開發 checkout 的 wrapper，跑完 `fmt`／`lint`／`test`／`build` 後把剛建置的 binary 交給同一組子命令。這些入口與 `scripts/verify/` 的驗收 harness 分離、**不由任何 `e2e-*` 任務呼叫**、不碰 `.local-dev/`。驗證一律打在生效面（執行中 process 的執行檔與啟動時間），非安裝面。完整步驟與契約見 `docs/deploy.md` §2–§4。

### 已知雷

- systemd、CI 與其他非互動環境的 PATH 必須含 mise shims 目錄 `~/.local/share/mise/shims`，不可依賴 `mise activate` 或 `bash -lc`。
- systemd user service 需啟用 linger，否則使用者登出後 timer 與 service 不會持續運作。
- pipeline 必須可安全重啟：狀態即進度，已完成的 Agent 呼叫不得重複計費。
- Windows 無 `chmod` 等價物：帶 token 的設定靠 `%LocalAppData%` 繼承的 ACL 保護，不得宣稱套了檔案權限。
- Windows 上 npm 裝的 `claude` 是 `.cmd` shim，CreateProcess 無法直接執行；`internal/agents` 解析後改經 `%COMSPEC% /c`。
- `Start-ScheduledTask` 對已在執行的工作是 no-op，與 systemd `enable --now` 同一個陷阱：更新必須先停、等 process 消失、再啟動。
- Task Scheduler 丟棄工作的 stdout／stderr：Windows 的日誌出口只有 `log.file`，不是 journald。
- 排程執行的 `jobfinderw.exe` 是 GUI subsystem，**完全沒有 stderr**。日誌的多重寫入必須把檔案排在 stderr 前面（`io.MultiWriter` 遇第一個錯誤即停止），啟動失敗的錯誤另有一條寫進 `log.file` 的路徑。前景診斷一律用 console 的 `jobfinder.exe serve`。
- 服務沒有主控台，Windows 會替每個 console 子行程另配一個並顯示；Agent 子行程一律帶 `CREATE_NO_WINDOW`。
- worker 互斥鎖必須是核心持有於檔案 handle 的鎖，不是「鎖檔存在與否」：Task Scheduler 停止工作與關機都直接終止行程，靠自行清理的鎖會永久殘留，服務再也起不來。
- Windows PowerShell 5.1 的 `Set-Content`／`>`／`Out-File` 預設不是 UTF-8（分別是 ANSI code page 與 UTF-16）。改 `config.yaml` 一律用 `[System.IO.File]::WriteAllText(..., UTF8Encoding($false))`；寫壞的設定會讓 `serve` 在掛上日誌前就失敗，Windows 上看不到任何錯誤。
- extension Options 接受的 host 必須與 `manifest.json` 的 `host_permissions` 一致（`127.0.0.1`、`[::1]`）。多接受一個 `localhost` 會存得進去卻在 fetch 被擋，症狀是「存好了但離線」；IPv6 從 URL 解析出來帶方括號。
- 排程狀態不得靠 `schtasks` 的文字輸出判定——那是安裝語系相依的；一律走 PowerShell 的 ScheduledTasks cmdlet 取物件屬性。
- `go.mod` 的 `go` directive 釘在 1.23.0；升相依套件時必看 `git diff go.mod` 的 `go` 行。`go get` 會為了滿足新相依悄悄改寫 directive 且不發警告，本機因 Go 自動下載對應 toolchain 而毫無症狀，到 `GOTOOLCHAIN=local` 的環境才編不動。抬高 directive 的代價落在靜態分析工具層而非編譯器：golangci-lint、staticcheck、gopls 各自綁死一個可解析的 Go 版本，directive 超前時本機 lint 與 IDE 無法 typecheck。要維持原版本用 `go mod edit -go=1.23.0 -toolchain=none` 再挑相容的相依版本，各版本的 directive 以 `grep -m1 "^go " $(go env GOMODCACHE)/<module>@<ver>/go.mod` 查。仍相容的上緣為 `modernc.org/sqlite` 1.39.0 與 `golang.org/x/sys` 0.35.0，更高版已由 `.github/dependabot.yml` 擋下。
- extension 與後端共用同一個安裝根目錄（Linux `~/.local/share/jobfinder/`、Windows `%LocalAppData%\jobfinder\`）：bootstrap 的 extension 模式把工件解壓到該根目錄下的 `extension/<tag>/`，而 Chrome 每次啟動都要從那裡讀檔。移除後端時整棵刪掉會一併帶走它，Chrome 的卡片隨即失效。逐項刪，步驟見 `docs/guides/getting-started.md` §10.4。
- `gh release download` 遇到目的地已有同名檔案時整條命令失敗，不是跳過該檔。重跑同一段下載（換版驗收、checksum 對不上重來、安裝到一半中斷）必然踩到，且工件與 `SHA256SUMS` 都會被擋。文件裡的每一條 `gh release download` 一律帶 `--clobber`。
- `jdx/mise-action` 的 `version` 輸入釘的是 **mise CLI 本身**，與 `mise.toml` 裡的工具版本無關，落後太多會讓 action 呼叫到該版沒有的子命令。症狀只在**快取命中**時出現：action 於快取命中時走 `mise version --json` 判斷已裝版本，命中失敗即 `exit code 2`，且該呼叫帶 `silent: true`，log 裡看不到任何錯誤原因。快取未命中時 action 直接下載 mise、不走這條路徑，所以升級後的第一次執行會綠、第二次起才紅。改動 `version` 會連帶改變快取鍵，舊的快取自然被繞開。

## 7. 怎麼上版

- commit、push 與建立 PR 僅在使用者要求時執行。
- 開發前先確認工作樹中的既有變更，避免覆蓋未提交內容。
- 上版前執行 `mise run fmt`、`mise run lint` 與 `mise run test`；PR 與 `main` 另由 `.github/workflows/ci.yml` 的 `check`（ubuntu）與 `windows` 兩個 job 把關。
- 標準「開發完成後上版並開 PR」流程使用 `/ship` skill。
- 發佈版本：推 tag `v<MAJOR>.<MINOR>.<PATCH>`，由 `.github/workflows/release.yml` 產出帶版號與 checksum 的 binary 與 extension zip（見 `docs/deploy.md` §7）。版號不寫進原始碼。
- **發版後必附部署步驟**：推完 tag、確認工件無誤之後，回報除了 Release 連結，還要附上 `docs/guides/runbook-upgrade.md` 的三條 lane（Linux 後端、Windows 後端、Windows Chrome extension），版號填實際 tag、指令可直接複製。步驟的最新狀態一律以該 runbook 為準，不即席重編。
- 版號的唯一決策點是 `internal/version`：release 以 `-ldflags "-X github.com/dccoding1118/job-finder/internal/version.tag=<tag>"` 注入。**該符號路徑是字串綁定**——package 搬家或變數 `tag` 改名會讓注入靜默失效（不報錯，版號悄悄變回 `dev`），改動時必須同步 `release.yml`。
