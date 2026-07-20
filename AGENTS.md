# job-finder 開發指南 (AGENTS.md)

為接手開發或維運本專案的人與 coding agent 準備：專案定位、環境、結構索引、開發規則與驗證方式。使用者層級通則另見 `~/.claude/CLAUDE.md`；本檔記錄專案層級事實。

---

## 1. 專案概覽

`jobfinder` 是單人單機使用的匿名 AI 求職媒合工具：從合規的職缺來源收集職缺，以本地規則與 Agent 評分篩選並標示判定；使用者對推薦職缺要求後才產出經審查的客製化求職信，最後手動投遞並追蹤狀態。

- 需求與範圍：`docs/PRD.md`
- 系統架構與開發順序：`docs/design.md`
- 模組契約：`docs/designs/design-<module>.md`
- 模組測試規格：`docs/tests/test-<module>.md`
- 累加式整合與驗收：`docs/verify.md`
- 開發交接：`STATUS.md`

B0–B5 的核心程式已完成並通過 `mise run fmt/lint/test`：schema/store、profile、crawler（Yourator adapter 與 104 列表／內頁解析）、pipeline（排程抓取編排、常駐 worker、條件篩選、每日預算）、agents（Scorer／Drafter／Reviewer 與 `llm.roles` primary／fallback 路由）、localhost API、extension page 與 systemd unit。求職信按需生成（`letter_requested` 取件）、verdict 導出與 104 清單就地標記均已落地。執行模型為「排程只驅動 fetch，filter／score／letter 由 API service 內常駐 worker 非同步消化」，`runs` 只記抓取事實、判定分布於檢視時即時查詢。

e2e 驗收 harness（`scripts/verify/`、`assert-positive.mjs`）已對齊非同步／按需模型：`run` 只 fetch、手動 `--stage` 或常駐 worker 消化 filter／score／letter、`runs.stats` 只記抓取事實、求職信「未要求不生成 → `RequestLetter` 後生成」、轉換經 `letter_requested`、`schema_version=2`，`mise run e2e-mock` 的 V1／V2／V4／V5 全綠（25 步，詳見 `docs/verify.md` §4）。尚待收尾與驗收的項目見 `STATUS.md` §2：`scripts/deploy/` 安裝·更新·回滾入口、真 Yourator live（V3）與實際 Chrome 人工 gate。第一版（B0–B5）於這些收尾完成後才首次上版。

## 2. 主要技術與環境

| 項目 | 內容 |
|---|---|
| 語言 | Go 1.23.12；module `github.com/dccoding1118/job-finder`；入口 `./cmd/jobfinder` |
| CLI | `spf13/cobra`；`cmd/jobfinder/cli/` 每個子命令一檔 |
| 工具鏈 | `mise` 管理 Go、gofumpt、golangci-lint；任務定義於 `mise.toml` |
| 資料庫 | SQLite，使用 `modernc.org/sqlite`，不依賴 cgo |
| 設定與 Profile | `config.yaml` 與 `profile.yaml` 為本機檔案，均不得納入版控 |
| 智能層 | headless CLI Runner：claude CLI 為主、codex CLI 為輔；不直接串接 LLM API |
| API 與前端 | Go `net/http` JSON API 僅監聽 localhost；Chrome MV3 extension page 為唯一日常操作入口 |
| 排程 | systemd user timer 觸發一次性且冪等的 `jobfinder run` |
| 時間 | `Asia/Taipei`，時間戳採 RFC3339 |

Go 不保證在裸 PATH；以 `mise run <task>` 或 `mise exec -- go <args>` 執行。

## 3. 結構與維護索引

**一句話結構**：`cmd/jobfinder/` 是薄入口與 Cobra 命令樹；`internal/` 承載業務模組；`extension/` 是 104 半被動擷取的 Chrome MV3 插件；`docs/` 是需求與設計的權威來源。

| 主題 | 先看 | 實作位置 |
|---|---|---|
| SQLite schema、migration、實體 CRUD、狀態轉換 | `docs/designs/design-schema.md` | `internal/store/` |
| 匿名 Profile、PII 檢核、校準建議 | `docs/designs/design-profile.md` | `internal/profile/` |
| Source adapter、去重、內容變更偵測、104 payload 解析 | `docs/designs/design-crawler.md` | `internal/crawler/` |
| fetch/filter/score/letter 編排、Profile 條件篩選、冪等與鎖 | `docs/designs/design-pipeline.md` | `internal/pipeline/` |
| CLI Runner、Scorer、Drafter、Reviewer 與輸出驗證 | `docs/designs/design-agents.md` | `internal/agents/` |
| Job／Run／狀態／判定（verdict）導出／求職信要求／手動 run 與 104 capture API | `docs/designs/design-api.md` | `internal/api/` |
| extension page 儀表板、104 列表就地標記與內頁擷取、sidebar | `docs/designs/design-extension.md` | `extension/` |
| CLI 命令樹 | 本檔 §4 與各模組的 CLI 介面 | `cmd/jobfinder/cli/` |

`docs/PRD.md` 定義需求；`docs/design.md` 定義系統邊界與模組依賴；詳細設計文件定義各模組契約。`STATUS.md` 只保留未完成任務與未歸檔結論，不承載專案設計。

## 4. 開發規則

1. **文件為主要依循體**：實作前先對照 `docs/PRD.md`、`docs/design.md` 與目標模組設計。若文件與程式碼衝突，停止實作並請開發者裁決；不得自行讓任一方遷就另一方。
2. **契約先行**：先定 schema、Profile 格式與 Agent JSON 契約，再實作依賴它們的模組。所有狀態轉換只能經 `internal/store` 的轉換函式，並寫入 `status_events`。
3. **Zero-PII**：資料庫、版控內容、fixture、log 與求職信不得包含姓名、Email、電話、身分證字號、學校或公司名稱。求職信落款固定使用 `[你的姓名]`、`[你的聯絡方式]`。
4. **Human-in-the-Loop**：系統只產生建議與求職信；求職信只在使用者對推薦職缺要求後才生成（`letter_requested` 是 letter 階段的唯一取件狀態），外部平台投遞必須由使用者手動完成。Profile 校準只產生 diff 建議，不得自動改寫。
5. **來源合規**：只處理免登入公開頁、遵守 robots.txt 且不繞過防護。104 僅處理使用者瀏覽器已載入的內容；不得背景開分頁、批次抓取或規避驗證。
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

MVP 以 `jobfinder run` 作為 one-shot pipeline，由 systemd user timer 每日觸發；API service 只綁定 localhost，extension page 透過其設定的 localhost endpoint 存取。遠端 VM 使用時，先建立 SSH local forward，再將 extension 設定指向本機轉送埠。

正式部署的唯一入口是 `scripts/deploy/` 的三個腳本（`install.sh`／`update.sh`／`rollback.sh`，共用 `lib.sh`；亦有 `mise run deploy-install`／`deploy-update`／`deploy-rollback`）。它們寫入 XDG 三分位置與 systemd user unit，與 `scripts/verify/` 的驗收 harness 分離、**不由任何 `e2e-*` 任務呼叫**、不碰 `.local-dev/`。驗證一律打在生效面（執行中 process 的 `/proc/<pid>/exe` 與啟動時間），非安裝面。完整步驟與各腳本契約見 `docs/deploy.md` §4。

### 已知雷

- systemd、CI 與其他非互動環境的 PATH 必須含 mise shims 目錄 `~/.local/share/mise/shims`，不可依賴 `mise activate` 或 `bash -lc`。
- systemd user service 需啟用 linger，否則使用者登出後 timer 與 service 不會持續運作。
- pipeline 必須可安全重啟：狀態即進度，已完成的 Agent 呼叫不得重複計費。

## 7. 怎麼上版

- commit、push 與建立 PR 僅在使用者要求時執行。
- 開發前先確認工作樹中的既有變更，避免覆蓋未提交內容。
- 上版前執行 `mise run fmt`、`mise run lint` 與 `mise run test`。
- 標準「開發完成後上版並開 PR」流程使用 `/ship` skill。
