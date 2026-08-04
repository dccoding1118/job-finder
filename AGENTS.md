# job-finder 開發指南 (AGENTS.md)

為接手開發或維運本專案的人與 coding agent 準備：專案定位、環境、結構索引、開發規則與驗證方式。使用者層級通則另見 `~/.claude/CLAUDE.md`；本檔記錄專案層級事實。

---

## 1. 專案概覽

`jobfinder` 是匿名 AI 求職媒合工具：從合規的職缺來源收集職缺，以本地規則與 Agent 評分篩選並標示判定；使用者對推薦職缺要求後才產出經審查的客製化求職信，最後手動投遞並追蹤狀態。

**產品形態**：Chrome extension 是產品本體——它同時是唯一 UI 與受保護平台（104／Cake）的唯一資料採集器，不會被 Web UI 取代。後端是可替換的媒合引擎，同一份程式碼支撐「使用者自部署」與「代管雲端」兩種部署，extension 對後端只認 endpoint ＋ auth。目前實作到自部署形態（單機、SQLite、localhost API、Bearer token）；帳號、計費與多租戶不在本 repo。定位與階段見 `docs/roadmap.md`，架構決策見 `docs/changes/change-productization-architecture.md`。本專案採 AGPL-3.0，不接受外部 PR。

- 需求與範圍：`docs/PRD.md`
- 系統架構與開發順序：`docs/design.md`
- 模組契約：`docs/designs/design-<module>.md`
- 模組測試規格：`docs/tests/test-<module>.md`
- 累加式整合與驗收：`docs/verify.md`
- 開發交接：`STATUS.md`

B0–B6 的核心程式已完成並通過 `mise run fmt/lint/test`：schema/store、profile、crawler（Yourator adapter 與 104／Cake 列表／內頁解析）、pipeline（排程抓取編排、常駐 worker、條件篩選、每日預算）、agents（Scorer／Drafter／Reviewer 與 `llm.roles` primary／fallback 路由）、localhost API、Chrome 原生 Side Panel 與 systemd unit。求職信按需生成（`letter_requested` 取件）、verdict 導出、cursor 清單分頁、推薦與待看清單載入更多與 104 清單就地標記均已落地。單筆職缺重新處理（`POST /api/v1/jobs/{id}/reprocess`：回到管線起點重跑篩選與評分，`filtered_out` 亦可）與處理進度讀取面（`GET /api/v1/status`：各處理狀態筆數、當日評分預算、最近 Agent 呼叫、執行期設定）已具備；自動篩選與評分可由系統頁開關（`GET`／`PUT /api/v1/settings`，存於 store `settings` 表），等待中的職缺可由「馬上處理」插隊（`POST /api/v1/jobs/{id}/process`）——閘門守的是單次 Agent 呼叫而非整趟批次，批次在每筆前讓位，因此插隊最多等一次呼叫；插隊不受開關與每日預算限制，用量照常計入。pipeline 各階段以 `log/slog` 輸出結構化執行記錄，正式部署由 `journalctl --user -u jobfinder-api` 檢視。執行模型為「排程只驅動 fetch，filter／score／letter 由 API service 內常駐 worker 非同步消化」，`runs` 只記抓取事實、判定分布於檢視時即時查詢。

Cake 半被動擷取與跨來源同一職缺合併已實作：`parsecake` 解析列表（`__NEXT_DATA__` 或 DOM 收割）與內頁（DOM 收割）、capture API 依 payload 的 `source` 分派解析器。半被動來源的搜尋條件由使用者在該平台自行設定，系統不生成搜尋 URL；`jobfinder queries show` 只列印全自動來源的展開 query。Cake 是 SPA，因此 `extension/content/` 以 `https://www.cake.me/*` 單一注入，由 `cake.js` 依 URL 路由列表／內頁模式、`nav.js` 通知 SPA 換頁；內頁不讀 `__NEXT_DATA__`（Cake 內頁不帶自己的 listing 狀態），只讀渲染後 DOM。合併以純程式規則（公司／職稱正規化＋地區相容性）判定且**只比較來源未重疊的群組**——同平台的兩筆是兩個開口，不合併也不提出裁決：高信心於單一交易合併並將 alias 轉入 `merged`，灰帶登記 `job_dupe_candidates` 由使用者在系統頁裁決，`POST /api/v1/jobs/{id}/unmerge` 可還原。反向校準（Calibrator）不在 MVP 範圍，屬 roadmap S1 且入口為 Side Panel，不做 CLI 指令。

Profile 面已實作：extension 全頁表單編輯單一 YAML 真相、ETag 條件式儲存、runtime snapshot 即時切換、setup mode、既有職缺手動 revision-aware 重新處理與 stale viewmodel。Profile 儲存與服務啟動不自動重評舊職缺，新 ingest 立即使用 active revision。`jobs` 的 revision 欄位記錄「該 Job 現行判定所屬的 Profile revision」，是所有讀取面把 Job 與 Score／篩選結果配對的鍵；它跟隨**處理**而非內容：內容變更但狀態不重置（`scored` 等終端狀態）時必須保留原 revision，覆寫會使既有評分在清單上讀成無分數。Scorer 的 `reason` 由 prompt 要求 40~60 字、驗證容忍到 100 字——兩者相等會讓略微超出就整筆重跑，白付 token。

e2e 驗收 harness（`scripts/verify/`、`assert-positive.mjs`）已對齊非同步／按需模型：`run` 只 fetch、手動 `--stage` 或常駐 worker 消化 filter／score／letter、`runs.stats` 只記抓取事實、求職信「未要求不生成 → `RequestLetter` 後生成」、轉換經 `letter_requested`。`mise run e2e-mock` 依序跑一般 mock、Profile mock 與 worker mock 三支 runbook，V1／V2／V4／V5／V6／V7 S30–S36 與 S39 系列全綠（詳見 `docs/verify.md` §4）；真 Yourator live（V3）、`scripts/deploy/` 安裝／更新／回滾與既有 Side Panel、Profile editor 的實際 Chrome 人工 gate 均已通過（人工 gate 進度另見 `STATUS.md`）。

硬規則篩選與軟規則評分分離（PRD B7）已實作：Profile schema v6 六區段與 `derived` 加總（舊版檔案於載入時自動遷移：v5 的 `nationwide` 展開為 `overseas` 以外的全部地區鍵，v4 的 `search.locations` 併入 `requirements.locations`，pre-v4 整份對映）、`filter_revision`／`score_revision` 雙 revision、兩段式篩選（結構化條件零 token，全過者才付一次 Filter agent）、四維評分與基準分、store schema v6 與既有職缺重置。**判定與資料重點**：逐條照實記 `pass`／`fail`／`unknown`，彙總時綜合全部條件——任一 `fail` 即不適合；無 `fail` 時清單摘要留 `discovered`（待看，等補全文）、全文 JD 進 `queued`（待評分）；缺資訊絕不判不適合；`remote` 的 `required`／`rejected` 視「未提及」為現場，不產生 `unknown`；年資與產業年資的 verdict 一律由 Go 以 `derived` 覆寫（LLM 只讀出 JD 要求的數值與對應 industry key）；`bonus` 條件不進篩選彙總，只由評分關的 `bonus_fit` 重用；評分關 prompt 不含 `experiences` 的敘事欄位與 `honesty_bounds`。地區只有 `requirements.locations[]` 一份（同時決定收集與判定，半被動來源的收集範圍實際由使用者的搜尋條件決定），存地區鍵並以簡繁英別名比對 JD 地點；**每個鍵只命中它自己所指的地點**——`taiwan` 專指只寫國別、未寫出縣市的 JD 地點，縣市鍵不命中只寫「台灣」者，「不限台灣任何地點」是選入 `overseas` 以外的全部鍵（編輯器以「＋ 全台」快捷鈕一次填入）。`location` 是必填欄位，來源未陳述地點時存哨兵值 `store.LocationUnknown`（`unknown`），地區規則對它與空字串一律判未決——哨兵值若被當成一般字串比對，缺資訊就會變成不適合。`filtered_out` 對來源內容變更是終局的：列表摘要與全文用的是同一組硬規則，補到全文不推翻摘要階段的 `fail`，因此補全文只更新內容、不重開判定也不再付一次 Filter Agent。推翻它是使用者的權利，經單筆重新處理（`POST /api/v1/jobs/{id}/reprocess`）行使——該入口把職缺送回管線起點重跑篩選與評分，是誤判與局部重跑的唯一救援路徑。判定共六類：`unfit`／`pending_detail`（待看）／`pending_screen`（篩選中，`new`）／`pending_score`（評分中，`queued`）／`not_recommended`／`recommended`；判定名稱說的是系統正在做什麼，不是狀態名。

## 2. 主要技術與環境

| 項目 | 內容 |
|---|---|
| 語言 | Go 1.23.12；module `github.com/dccoding1118/job-finder`；入口 `./cmd/jobfinder` |
| CLI | `spf13/cobra`；`cmd/jobfinder/cli/` 每個子命令一檔 |
| 工具鏈 | `mise` 管理 Go、gofumpt、golangci-lint；任務定義於 `mise.toml` |
| 資料庫 | SQLite，使用 `modernc.org/sqlite`，不依賴 cgo |
| 設定與 Profile | `config.yaml` 與 `profile.yaml` 為本機檔案，均不得納入版控 |
| 智能層 | headless CLI Runner：claude CLI 為主、codex CLI 為輔；不直接串接 LLM API |
| API 與前端 | Go `net/http` JSON API 僅監聽 localhost；Chrome MV3 原生 Side Panel 為唯一日常操作入口 |
| 排程 | systemd user timer 觸發一次性且冪等的 `jobfinder run` |
| 時間 | `Asia/Taipei`，時間戳採 RFC3339 |

Go 不保證在裸 PATH；以 `mise run <task>` 或 `mise exec -- go <args>` 執行。

## 3. 結構與維護索引

**一句話結構**：`cmd/jobfinder/` 是薄入口與 Cobra 命令樹；`internal/` 承載業務模組；`extension/` 是 104／Cake 半被動擷取的 Chrome MV3 插件；`docs/` 是需求與設計的權威來源。

| 主題 | 先看 | 實作位置 |
|---|---|---|
| SQLite schema、migration、實體 CRUD、狀態轉換、跨來源職缺分群與合併 | `docs/designs/design-schema.md` | `internal/store/` |
| 匿名 Profile、PII 檢核、canonical serialization、ETag／雙 revision、`derived` 加總、runtime provider、校準建議 | `docs/designs/design-profile.md` | `internal/profile/` |
| Source adapter（Yourator）、去重、內容變更偵測、104／Cake payload 解析 | `docs/designs/design-crawler.md` | `internal/crawler/` |
| fetch/filter/score/letter 編排、Profile activation／重新處理、單筆重新處理入隊、執行記錄、revision-aware CAS、每日預算與鎖 | `docs/designs/design-pipeline.md` | `internal/pipeline/` |
| CLI Runner、Filter、Scorer、Drafter、Reviewer、Calibrator 與輸出驗證 | `docs/designs/design-agents.md` | `internal/agents/` |
| Profile GET／PUT／手動 reprocess、Job stale viewmodel、Job／Run／狀態／verdict／求職信要求／單筆重新處理／處理進度／手動 run／重複職缺裁決與各平台 capture API | `docs/designs/design-api.md` | `internal/api/` |
| Side Panel、全頁 Profile editor、104／Cake 列表就地標記與內頁擷取、Cake SPA 模式路由、疑似重複裁決 | `docs/designs/design-extension.md` | `extension/` |
| 搜尋條件展開（只用於全自動來源） | `docs/designs/design-crawler.md` §5 | `cmd/jobfinder/cli/queries.go` |
| CLI 命令樹 | 本檔 §4 與各模組的 CLI 介面 | `cmd/jobfinder/cli/` |

`docs/PRD.md` 定義需求；`docs/design.md` 定義系統邊界與模組依賴；詳細設計文件定義各模組契約。`STATUS.md` 只保留未完成任務與未歸檔結論，不承載專案設計。

## 4. 開發規則

1. **文件為主要依循體**：實作前先對照 `docs/PRD.md`、`docs/design.md` 與目標模組設計。若文件與程式碼衝突，停止實作並請開發者裁決；不得自行讓任一方遷就另一方。
2. **契約先行**：先定 schema、Profile 格式與 Agent JSON 契約，再實作依賴它們的模組。所有狀態轉換只能經 `internal/store` 的轉換函式，並寫入 `status_events`。
3. **Zero-PII**：資料庫、版控內容、fixture、log 與求職信不得包含姓名、Email、電話、身分證字號、學校或公司名稱。求職信落款固定使用 `[你的姓名]`、`[你的聯絡方式]`。JD 原文可能夾帶招募方的 Email 或手機：`UpsertJob` 於入庫前將其遮罩為 `[EMAIL]`／`[PHONE]`（`content_hash` 亦以遮罩後文字計算），因此資料庫、下游 prompt 與 `agent_calls` 稽核副本一律不帶聯絡資訊，系統不會把擷取到的個資轉交外部模型。`SaveAgentCall` 同樣遮罩後才寫入，遮罩後仍命中才拒寫。
4. **Human-in-the-Loop**：系統只產生建議與求職信；求職信只在使用者對推薦職缺要求後才生成（`letter_requested` 是 letter 階段的唯一取件狀態），外部平台投遞必須由使用者手動完成。Profile 校準只產生 diff 建議，不得自動改寫。
   Profile 內容只有使用者明確儲存時才可寫入；revision 變更不得自動重生或覆蓋求職信，也不得改寫投遞歷史。
5. **來源合規**：只處理免登入公開頁、遵守 robots.txt 且不繞過防護。104 與 Cake 僅處理使用者瀏覽器已載入的內容；不得背景開分頁、批次抓取或規避驗證，也不得繞過人機驗證直接呼叫平台前端所用的搜尋 API。**半被動是新增來源的預設路線**，全自動抓取只在平台搜尋路徑本身開放時採用。
6. **Cobra 慣例**：`main.go` 維持薄入口；每個 resource 或子命令各有一個 `cli` 檔案；輸出使用 `cmd.OutOrStdout()` 或 `cmd.OutOrStderr()`，讓測試可擷取。
7. **正式文件與註解只寫最新狀態**：設計與程式碼變更的歷程不散落在正文；詳細規格以文字與表格呈現。

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
| L2 整合 | pipeline 跨模組流程 | `e2e-mock` 以合成來源、fake Runner 與 extension 模擬驗證；不代表真外部依賴 |
| Live E2E | 真實來源與 CLI Runner | `e2e-live` 使用獨立設定與 SQLite；至少抓回一筆真資料並驗證格式，不得退回 mock |
| 人工 gate | 實際 Chrome 插件 | 依 `docs/verify.md` 由驗收者載入同一 artifact；自動隔離 Chromium 不得替代 |

每個完成的模組都應先通過 `mise run fmt`、`mise run lint`、`mise run test`，再進入下一個模組。

開發批次完成後，先以 `scripts/verify/harness/deploy.sh` 把受測 binary 與驗收資源物化到 `.local-dev/verify/`，再執行該批次的 `scripts/verify/run-*.sh` 與 `docs/verify.md` 案例。入口 runbook（`run-*.sh`、`reset.sh`）與共用 `lib.sh` 在 `scripts/verify/`；建置 harness（`deploy.sh`、`fake-agent.sh`）在 `scripts/verify/harness/`、斷言 oracle 在 `scripts/verify/oracle/`、browser E2E 與 Playwright 設定在 `scripts/verify/browser/`；mock/live 各自的 SQLite 與證據位於 gitignored 的 `.local-dev/verify/` 且不得共用外部依賴設定。正式 systemd template 位於 `deploy/production/systemd/`，不由驗收腳本安裝；Chrome compatibility 必須由驗收者將同一插件 artifact 實際載入 Chrome 驗收。

## 6. 怎麼部署

MVP 以 `jobfinder run` 作為 one-shot pipeline，由 systemd user timer 每日觸發；API service 只綁定 localhost，Side Panel 透過其設定的 localhost endpoint 存取。Windows Chrome 存取遠端 GCP VM 時，以登入後背景常駐的 SSH／IAP local forward 將 `127.0.0.1:18686` 轉送到 VM `127.0.0.1:8686`；完整設定、自動重連、驗證與排障見 `docs/guides/runbook-extension.md`。

正式部署的唯一入口是 `scripts/deploy/` 的三個腳本（`install.sh`／`update.sh`／`rollback.sh`，共用 `lib.sh`；亦有 `mise run deploy-install`／`deploy-update`／`deploy-rollback`）。它們寫入 XDG 三分位置與 systemd user unit，與 `scripts/verify/` 的驗收 harness 分離、**不由任何 `e2e-*` 任務呼叫**、不碰 `.local-dev/`。驗證一律打在生效面（執行中 process 的 `/proc/<pid>/exe` 與啟動時間），非安裝面。完整步驟與各腳本契約見 `docs/deploy.md` §4。

### 已知雷

- systemd、CI 與其他非互動環境的 PATH 必須含 mise shims 目錄 `~/.local/share/mise/shims`，不可依賴 `mise activate` 或 `bash -lc`。
- systemd user service 需啟用 linger，否則使用者登出後 timer 與 service 不會持續運作。
- pipeline 必須可安全重啟：狀態即進度，已完成的 Agent 呼叫不得重複計費。

## 7. 怎麼上版

- commit、push 與建立 PR 僅在使用者要求時執行。
- 開發前先確認工作樹中的既有變更，避免覆蓋未提交內容。
- 上版前執行 `mise run fmt`、`mise run lint` 與 `mise run test`；PR 與 `main` 另由 `.github/workflows/ci.yml` 把關。
- 標準「開發完成後上版並開 PR」流程使用 `/ship` skill。
- 發佈版本：推 tag `v<MAJOR>.<MINOR>.<PATCH>`，由 `.github/workflows/release.yml` 產出帶版號與 checksum 的 binary 與 extension zip（見 `docs/deploy.md` §7）。版號不寫進原始碼。
- 版號的唯一決策點是 `internal/version`：release 以 `-ldflags "-X github.com/dccoding1118/job-finder/internal/version.tag=<tag>"` 注入。**該符號路徑是字串綁定**——package 搬家或變數 `tag` 改名會讓注入靜默失效（不報錯，版號悄悄變回 `dev`），改動時必須同步 `release.yml`。
