# 模組設計 — schema/store（資料契約）

對應需求：PRD §2 §3、R8。本文件是 DB 實體與狀態機的**唯一權威**；其他模組經 `internal/store` 存取，不得繞過。

## 1. 職責邊界

- 定義 SQLite schema 與 migration（embedded SQL，`PRAGMA user_version` 控版）。
- 提供實體 CRUD 與**狀態轉換函式**（含合法性檢查、自動寫入 `status_events`）。
- 不含業務邏輯（初篩規則、評分、UI 皆在上層）。

## 2. 資料表

### 2.1 `jobs`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | 內部流水號 |
| `source` | TEXT | 來源代碼：`yourator` / `cake` / `104` |
| `external_id` | TEXT | 平台端職缺 ID；`UNIQUE(source, external_id)` |
| `url` | TEXT | 原始職缺連結 |
| `title` | TEXT | 職稱 |
| `company_name` | TEXT | 公司名（公開資訊，非 PII） |
| `company_info` | TEXT | 公司產業/規模等摘要 |
| `description` | TEXT NULL | JD 全文（純文字化）；partial 職缺（`discovered`）為 NULL |
| `salary_min` / `salary_max` | INTEGER NULL | 月薪範圍（元）；面議為 NULL |
| `location` | TEXT | 工作地點 |
| `remote_type` | TEXT | `onsite` / `hybrid` / `remote` / `unknown` |
| `content_hash` | TEXT | 內容雜湊（見 §4 去重與變更偵測） |
| `process_state` | TEXT | 見 §3 狀態機 |
| `filter_hits` | TEXT NULL | 條件篩選淘汰時命中的條件名稱（JSON array 字串） |
| `apply_state` | TEXT NULL | 見 §3；僅 `letter_ready` 後有值，初始 `pending` |
| `discovered_by_run_id` | INTEGER NULL FK→runs | 首次入庫的抓取輪次；104 等使用者導覽 capture 入庫者為 NULL |
| `first_seen_at` / `last_seen_at` | TEXT | RFC3339 |
| `updated_at` | TEXT | RFC3339 |

索引：`(process_state)`、`(apply_state)`、`(discovered_by_run_id)`、`(source, external_id)` UNIQUE。

`discovered_by_run_id` 只在新建時寫入，後續 upsert 不更動——它記錄「這筆是哪一輪抓進來的」，供 Run 歷史即時查出該輪職缺的現行判定分布（R8.1）。

### 2.2 `scores`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER FK→jobs | 一 Job 可有多筆（JD 變更重評時新增，不覆蓋） |
| `dim_hard_skill` / `dim_domain` / `dim_seniority` / `dim_condition` / `dim_direction` | INTEGER | 五維各 0–100 |
| `total` | REAL | Go 依權重計算的加權總分 |
| `reason` | TEXT | ≤50 字推薦/不推薦理由 |
| `runner` | TEXT | 產出此評分的 runner（`claude` / `codex`） |
| `created_at` | TEXT | RFC3339 |

現行有效評分＝該 job 最新一筆。

### 2.3 `letters`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER FK→jobs | |
| `content` | TEXT | 最終求職信（含佔位符落款） |
| `status` | TEXT | `approved` / `failed` |
| `rounds` | INTEGER | 起草＋重寫總輪數 |
| `review_log` | TEXT | 各輪審查意見（JSON 字串），供稽核 |
| `runner_draft` / `runner_review` | TEXT | 各角色使用的 runner |
| `created_at` | TEXT | RFC3339 |

### 2.4 `status_events`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER FK→jobs | |
| `axis` | TEXT | `process` / `apply` |
| `from_state` / `to_state` | TEXT | |
| `note` | TEXT NULL | 使用者備註（apply 軸） |
| `created_at` | TEXT | RFC3339 |

反向校準（R1.3）以 `axis='apply' AND to_state='interview'` 計數與取樣。

### 2.5 `runs`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `started_at` / `finished_at` | TEXT | RFC3339 |
| `trigger` | TEXT | `timer` / `manual-cli` / `manual-extension` |
| `stats` | TEXT | 抓取事實（JSON 字串：fetched/new/queries/errors） |
| `error` | TEXT NULL | 致命錯誤摘要（來源級錯誤入 stats.errors） |

`runs` 只記錄**抓取**（`jobfinder run` 的 fetch），不涵蓋 filter／score／letter——後三者由常駐 worker 連續消化，不屬於任何輪次（見 [design-pipeline](design-pipeline.md) §2）。該輪職缺的判定分布不入 `stats`，由 `discovered_by_run_id` 於查詢時即時導出。

### 2.6 `agent_calls`（R8.2 稽核）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER NULL FK | 校準等非職缺呼叫為 NULL |
| `role` | TEXT | `scorer` / `drafter` / `reviewer` / `calibrator` |
| `runner` | TEXT | `claude` / `codex` |
| `input` / `output` | TEXT | 完整 prompt 與原始輸出（不得含 PII） |
| `ok` | INTEGER | 0/1 |
| `duration_ms` | INTEGER | |
| `created_at` | TEXT | RFC3339 |

## 3. 狀態機（權威定義）

### 3.1 `process_state`（系統擁有）

| From | To | 觸發 |
|---|---|---|
| （新增，含全文） | `new` | fetch／內頁擷取 upsert 新職缺 |
| （新增，partial） | `discovered` | 插件列表收割 upsert（僅列表可見欄位，無 JD 全文） |
| `discovered` | `filtered_out` | 欄位可用的條件篩選命中 |
| `discovered` | `new` | 內頁擷取補入全文 |
| `new` | `filtered_out` | 條件篩選命中 |
| `new` | `queued` | 通過條件篩選 |
| `queued` | `scored` | 評分完成且 total < 75 |
| `queued` | `shortlisted` | 評分完成且 total ≥ 75 |
| `shortlisted` | `letter_requested` | **使用者**要求生成求職信（Side Panel／CLI） |
| `letter_requested` | `letter_ready` | Reviewer 過審 |
| `letter_requested` | `letter_failed` | 重寫上限仍不過審 |
| `letter_failed` | `letter_requested` | 使用者再次要求生成（Side Panel／CLI） |
| 任一非終態 | `new` | JD 內容雜湊變更（重新走流程；既有 scores/letters 保留為歷史） |

終態：`filtered_out`、`scored`、`letter_ready`（處理軸而言）。`shortlisted` 與 `letter_failed` 是**停留狀態**——系統不會自行推進，只有使用者要求才轉入 `letter_requested`（PRD R5.0）。`letter_requested` 是 letter 階段的唯一取件狀態。

### 3.2 `apply_state`（使用者擁有）

| From | To |
|---|---|
| （進入 `letter_ready`） | `pending` |
| `pending` | `applied` / `dropped` |
| `applied` | `interview` / `ghosted` / `dropped` |
| `interview` | `offer` / `ghosted` / `dropped` |

不允許回退（誤標時以反向事件＋note 修正，事件流保留真相）。

## 4. 去重與變更偵測

- 唯一鍵 `(source, external_id)`：已存在則更新 `last_seen_at`。
- `content_hash = sha256(title + "\n" + description + "\n" + salary_min/max + location + remote_type)`——**只含來源端內容欄位，不含任何本系統回寫欄位**，確保比對可收斂。partial 職缺不計 hash（NULL），補入全文時才首次計算。
- 雜湊變更 ⇒ 更新內容欄位並將 `process_state` 重置為 `new`（§3.1）；僅適用已有全文的職缺。
- partial upsert（列表收割）遇既有職缺（任何狀態）只更新 `last_seen_at`，不覆蓋內容、不改狀態——待看清單天然為增量。

## 5. store 介面（概念）

| 函式群 | 說明 |
|---|---|
| `UpsertJob(raw, runID)` | 去重、變更偵測、狀態初始/重置（partial→`discovered`、全文→`new`、既有 partial 補全文→`new`），新建時寫入 `discovered_by_run_id`（capture 入庫傳 NULL），回傳是否新增/變更 |
| `TransitionProcess(jobID, to, meta)` / `TransitionApply(jobID, to, note)` | 驗證合法轉換 → 更新欄位 → 寫 `status_events`（同一交易） |
| `ListJobs(filter, sort)` | UI/CLI 查詢：依狀態、來源、分數排序 |
| `PickForStage(stage, limit)` | 常駐 worker 各階段取件（`new`→filter、`queued`→score、`letter_requested`→letter）；`shortlisted` 不是任何階段的取件狀態 |
| `CountAgentCallsSince(role, since)` | 每日預算計數（見 [design-pipeline](design-pipeline.md) §4） |
| `SummarizeRunJobs(runID)` | 依 `discovered_by_run_id` 即時導出該輪職缺的現行判定分布 |
| `SaveScore / SaveLetter / SaveAgentCall / StartRun / FinishRun` | 寫入各實體 |

## 6. 交付物

- `internal/store/`：schema.sql（embedded）、migration、上述介面實作與單元測試（暫存目錄真 SQLite）。
- 測試涵蓋：唯一鍵去重、雜湊變更重置、非法狀態轉換被拒、事件寫入與交易一致性。

## 7. 待決

（無——欄位如需增補由後續模組設計回饋本文件。）
