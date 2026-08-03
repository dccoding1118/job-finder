# 模組設計 — schema/store（資料契約）

對應需求：PRD §2 §3、R8。本文件是 DB 實體與狀態機的**唯一權威**；其他模組經 `internal/store` 存取，不得繞過。

## 1. 職責邊界

- 定義 SQLite schema 與 migration（embedded SQL，`PRAGMA user_version` 控版）。
- 提供實體 CRUD 與**狀態轉換函式**（含合法性檢查、自動寫入 `status_events`）。
- 不含業務邏輯（篩選規則、評分、UI 皆在上層）。

## 2. 資料表

### 2.1 `jobs`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | 內部流水號 |
| `source` | TEXT | 來源代碼：`yourator` / `cake` / `104` |
| `group_id` | INTEGER NULL FK→job_groups | 所屬職缺群組（§2.7）；每筆入庫即歸屬一個群組，legacy 可為 NULL |
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
| `filter_hits` | TEXT NULL | 結構化硬規則未通過時命中的條件名稱（JSON array 字串） |
| `filter_revision` | TEXT NULL | 該 Job 現行篩選判定所屬的 Profile `filter_revision`；legacy 或受保護歷史可為 NULL |
| `score_revision` | TEXT NULL | 該 Job 現行評分判定所屬的 Profile `score_revision`；未評分、legacy 或受保護歷史可為 NULL |
| `apply_state` | TEXT NULL | 見 §3；僅 `letter_ready` 後有值，初始 `pending` |
| `discovered_by_run_id` | INTEGER NULL FK→runs | 首次入庫的抓取輪次；104 等使用者導覽 capture 入庫者為 NULL |
| `first_seen_at` / `last_seen_at` | TEXT | RFC3339 |
| `updated_at` | TEXT | RFC3339 |

索引：`(process_state)`、`(apply_state)`、`(discovered_by_run_id)`、`(group_id)`、`(source, external_id)` UNIQUE。

`discovered_by_run_id` 只在新建時寫入，後續 upsert 不更動——它記錄「這筆是哪一輪抓進來的」，供 Run 歷史即時查出該輪職缺的現行判定分布（R8.1）。

### 2.2 `scores`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER FK→jobs | 一 Job 可有多筆（重新評分時新增，不覆蓋） |
| `dim_content` / `dim_benefit` / `dim_bonus` / `dim_industry` | INTEGER | 四維各 0–100 |
| `total` | REAL | Go 依權重計算的加權總分 |
| `reason` | TEXT | 推薦/不推薦理由；長度契約由 Agent 契約把關（[design-agents](design-agents.md) §3.2），此處僅有 ≤500 字元的儲存防線 |
| `runner` | TEXT | 產出此評分的 runner（`claude` / `codex`） |
| `score_revision` | TEXT NULL | 產生此 Score 的 Profile `score_revision`；新資料必填，legacy 可為 NULL |
| `created_at` | TEXT | RFC3339 |

現行有效評分＝與 `jobs.score_revision` 相同的最新一筆；revision 不同或為 legacy NULL 的 Score 保留供稽核，但不可當成現行評分。

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
| `filter_revision` / `score_revision` | TEXT NULL | 產生此 Letter 的實際 Profile revision 對；新資料必填，legacy 可為 NULL |
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
| `role` | TEXT | `filter` / `scorer` / `drafter` / `reviewer` / `calibrator` |
| `runner` | TEXT | `claude` / `codex` |
| `input` / `output` | TEXT | 完整 prompt 與原始輸出（不得含 PII） |
| `ok` | INTEGER | 0/1 |
| `duration_ms` | INTEGER | |
| `filter_revision` / `score_revision` | TEXT NULL | 呼叫開始時的對應 Profile revision：filter 呼叫填前者、scorer 填後者、draft／review 兩者皆填；與 Profile 無關的呼叫為 NULL |
| `created_at` | TEXT | RFC3339 |

### 2.7 `job_groups`（跨來源同一職缺）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `canonical_job_id` | INTEGER FK→jobs | 承載處理、評分、求職信與投遞的那一筆 |
| `dedupe_key` | TEXT NULL | §4.1 的正規化比對鍵；人工合併產生的群組為 NULL |
| `created_at` / `updated_at` | TEXT | RFC3339 |

索引：`(dedupe_key)`。單成員群組是常態——每筆 Job 入庫即建立自己的群組，合併只是把成員收斂到同一個。

### 2.8 `filter_results`（硬規則判定與 JD 條件拆解）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `job_id` | INTEGER FK→jobs | 一 Job 可有多筆（重篩時新增，不覆蓋） |
| `outcome` | TEXT | `fail` / `unknown` / `pass`，即該次彙總結論 |
| `conditions` | TEXT | JSON 陣列：逐條的 `text`／`kind`／`group`／`category`／`verdict`，含結構化與語意兩類條件 |
| `stage` | TEXT | `structural`（只跑程式比對即結束）／`semantic`（含 Filter Agent 判定） |
| `runner` | TEXT NULL | 語意判定使用的 runner；純結構化判定為 NULL |
| `filter_revision` | TEXT NULL | 產生此判定的 Profile `filter_revision`；新資料必填，legacy 可為 NULL |
| `created_at` | TEXT | RFC3339 |

索引：`(job_id)`。現行有效判定＝與 `jobs.filter_revision` 相同的最新一筆；其餘保留供稽核。`conditions` 中標為 `bonus` 的條目由評分關的 `bonus_fit` 重用，不參與 `outcome` 彙總。

### 2.9 `job_dupe_candidates`（疑似重複，待使用者裁決）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `group_a_id` / `group_b_id` | INTEGER FK→job_groups | 以較小 id 為 a；`UNIQUE(group_a_id, group_b_id)` |
| `similarity` | REAL | 正規化職稱的 token Jaccard |
| `reason` | TEXT | `title_similar` / `location_mismatch` / `has_output` |
| `state` | TEXT | `pending` / `merged` / `ignored` |
| `created_at` | TEXT | RFC3339 |

### 2.10 `settings`（使用者可在 Side Panel 改動的執行期設定）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `key` | TEXT PK | 設定鍵 |
| `value` | TEXT | 設定值（字串編碼；布林為 `on`／`off`） |
| `updated_at` | TEXT | RFC3339 |

| 鍵 | 值 | 未設定時 | 意義 |
|---|---|---|---|
| `auto_processing` | `on` / `off` | 視為 `on` | 常駐 worker 是否自動消化 filter 與 score（見 [design-pipeline](design-pipeline.md) §2.2） |

執行期設定存於資料庫而非 `config.yaml`：服務執行期間由 API service 擁有，重啟不得默默還原使用者關掉的開關。`config.yaml` 只保留部署期決定、使用者不會在 UI 改動的項目。

## 3. 狀態機（權威定義）

### 3.1 `process_state`（系統擁有）

| From | To | 觸發 |
|---|---|---|
| （新增，含全文） | `new` | fetch／內頁擷取 upsert 新職缺 |
| （新增，partial） | `discovered` | 插件列表收割 upsert（僅列表可見欄位，無 JD 全文） |
| `discovered` | `filtered_out` | 欄位可用的結構化硬規則判 `fail` |
| `discovered` | `new` | 內頁擷取補入全文 |
| `new` | `filtered_out` | 硬規則任一條判 `fail` |
| `new` | `discovered` | 篩選時發現該筆其實沒有 JD 全文，退回待看補全文 |
| `new` | `queued` | 硬規則全條 `pass` |
| `queued` | `scored` | 評分完成且 total < 75 |
| `queued` | `shortlisted` | 評分完成且 total ≥ 75 |
| `filtered_out` | `new`／`discovered` | **使用者**要求重新處理單筆職缺（Side Panel）；有 JD 全文者回 `new`，只有摘要者回 `discovered` |
| `scored` | `new`／`discovered` | 同上 |
| `shortlisted` | `new`／`discovered` | 同上 |
| `shortlisted` | `letter_requested` | **使用者**要求生成求職信（Side Panel／CLI） |
| `letter_requested` | `letter_ready` | Reviewer 過審 |
| `letter_requested` | `letter_failed` | 重寫上限仍不過審 |
| `letter_failed` | `letter_requested` | 使用者再次要求生成（Side Panel／CLI） |
| 任一非終態 | `new` | JD 內容雜湊變更（重新走流程；既有 scores/letters 保留為歷史） |
| 任一狀態 | `merged` | 該筆被判定為其他 Job 的重複刊登，成為 alias（§4.2）；事件 note 記錄合併前狀態與 canonical job id |
| `merged` | 合併前狀態 | 使用者取消合併，依合併事件還原 |

終態：`filtered_out`、`letter_ready`（處理軸而言）、`merged`（僅由使用者取消合併離開）。`filtered_out` 對來源內容變更是終局的——列表摘要與 JD 全文用的是同一組硬規則，摘要階段的 `fail` 是「已陳述事實不符」的結論，補到全文並不推翻它，因此補全文只更新內容不重開判定，也不再付一次 Filter Agent；推翻它是使用者的權利，經單筆重新處理行使。`discovered` 是**停留狀態**：不被任何階段取件，只由使用者點開原始頁面補全文後離開。`merged` 的 Job 不被任何階段取件、不導出 verdict、不出現在任何清單，因此不產生 LLM 費用。`scored` 只由使用者明確要求的單筆重新處理離開。`shortlisted` 與 `letter_failed` 是**停留狀態**——系統不會自行推進，只有使用者要求才轉入 `letter_requested`（PRD R5.0）。`letter_requested` 是 letter 階段的唯一取件狀態。

### 3.2 Profile activation 專用轉換

Profile activation 不是一般 `TransitionProcess`，只能經 store 專用交易入口執行，且重跑範圍依變更的 revision 決定：

| 變更 | 行為 |
|---|---|
| `filter_revision` 改變 | `discovered`／partial `filtered_out` 重做可用條件；有全文的 `new`、`queued`、`filtered_out`、`scored`、`shortlisted` 設定新 revision 並回到／維持 `new` |
| 只有 `score_revision` 改變 | `queued`／`scored`／`shortlisted` 設定新 `score_revision` 並回到／維持 `queued`，篩選結果保留；`filtered_out`、`discovered` 完全不動 |

`letter_requested`、`letter_ready`、`letter_failed`、Letter、apply state 與 apply event 全部受保護，不回退或重送。

activation、filter 結果、Score 保存與 process transition 均以 expected state ＋ expected revision compare-and-set（filter 面用 `filter_revision`、score 面用 `score_revision`）。相同 revision 的 activation 是 no-op，不新增重複事件。

### 3.3 `apply_state`（使用者擁有）

| From | To |
|---|---|
| （進入 `letter_ready`） | `pending` |
| `pending` | `applied` / `dropped` |
| `applied` | `interview` / `ghosted` / `dropped` |
| `interview` | `offer` / `ghosted` / `dropped` |

不允許回退（誤標時以反向事件＋note 修正，事件流保留真相）。

## 4. 去重與變更偵測

### 4.1 同來源去重與變更偵測

- 唯一鍵 `(source, external_id)`：已存在則更新 `last_seen_at`。
- `content_hash = sha256(title + "\n" + description + "\n" + salary_min/max + location + remote_type)`——**只含來源端內容欄位，不含任何 revision 或本系統回寫欄位**。Job 內容與 Profile 是兩個獨立變動軸。partial 職缺不計 hash（NULL），補入全文時才首次計算。
- 雜湊變更 ⇒ 更新內容欄位並將 `process_state` 重置為 `new`（§3.1）；僅適用已有全文的職缺。
- 兩個 revision 欄位跟隨**處理**而非內容：只有重置回 `new` 的職缺才寫入本次 ingest 的 revision。狀態不變者（`scored`／`filtered_out`／`letter_ready` 等終端狀態）保留其現行判定所屬的 revision——`score_revision` 是所有讀取面把 Job 與 Score 配對的依據、`filter_revision` 則配對篩選結果，覆寫它們會使既有產出讀不出來。
- partial upsert（列表收割）遇既有職缺（任何狀態）只更新 `last_seen_at`，不覆蓋內容、不改狀態——待看清單天然為增量。

### 4.2 跨來源同一職缺（R2.8）

同一職缺常同時刊登於三個來源。辨識**一律為程式規則，不呼叫 LLM**：

| 對象 | 正規化 |
|---|---|
| 公司名 | 全形轉半形 → 小寫 → 去空白與標點 → 去法人與分支尾綴（`股份有限公司`／`有限公司`／`公司`／`台灣分公司`／`inc`／`ltd`／`co`／`corp`／`limited`） |
| 職稱 | 全形轉半形 → 小寫 → 去括號補述 → 去資歷修飾（`senior`／`sr`／`junior`／`jr`／`資深`／`中高階`；`實習`／`intern` **不得**去除，屬不同職缺）→ 中英同義詞對照正規化 → 去空白與標點 |
| 地區 | 取縣市層級；`remote` 或未知與任何地區相容 |

`dedupe_key = sha256(正規化公司 + "|" + 正規化職稱 + "|" + 縣市)`。分群鍵是正規化公司——不同公司一律不比較。

**比較對象限跨來源**：只與**尚未涵蓋本群組任一來源**的群組比較。去重要解決的是同一則職缺刊登在兩個平台（例如 104 與 Cake 各有一筆），同平台上的兩筆是兩個不同的職位開口——平台不會把同一則職缺刊兩次——比較它們只會產生使用者必然否決的裁決。群組已因合併涵蓋多個來源時，這些來源全部排除。

| 判定 | 條件 | 動作 |
|---|---|---|
| 高信心 | 來源未重疊 ＋ 公司相等 ＋ 職稱**完全相等** ＋ 地區相容 ＋ 雙方皆無 score／letter／apply 產出 | 合併為同一群組 |
| 灰帶 | 來源未重疊 ＋ 公司相等，且（職稱 Jaccard ≥ `dedupe.title_similarity_threshold`、或地區不相容、或任一方已有產出） | 寫入 `job_dupe_candidates` 待使用者裁決 |
| 不同 | 其餘（含來源重疊） | 不建立關聯 |

已有評分、求職信或投遞歷史的 Job **不自動合併**（`reason='has_output'`），避免自動動作吃掉既有產出。

**canonical 選擇順序**：有 JD 全文者 → `description` 較長者 → 設定檔 `dedupe.source_priority`（預設 `104` > `cake` > `yourator`）→ `first_seen_at` 較早者。其餘成員轉入 `merged`。

capture 或 fetch 命中 alias 時，回傳的一律是 **canonical 的 job id 與 verdict**——使用者在某平台看到的職缺若已由另一來源評過分，就地標記直接顯示既有判定，不重跑也不重複計費。

## 5. store 介面（概念）

| 函式群 | 說明 |
|---|---|
| `UpsertJob(raw, runID)` | 去重、變更偵測、狀態初始/重置（partial→`discovered`、全文→`new`、既有 partial 補全文→`new`），新建時寫入 `discovered_by_run_id`（capture 入庫傳 NULL），回傳是否新增/變更 |
| `TransitionProcess(jobID, to, meta)` / `TransitionApply(jobID, to, note)` | 驗證合法轉換 → 更新欄位 → 寫 `status_events`（同一交易） |
| `ListJobs(filter, sort)` | UI/CLI 查詢：依狀態、來源、分數排序；預設只回各群組的 canonical，`merged` 不出現 |
| `PickForStage(stage, revisions, limit)` | 常駐 worker 各階段取件（`new`→filter、`queued`→score、`letter_requested`→letter）；`shortlisted`、`discovered` 不是任何階段的取件狀態，`merged` 一律排除。filter 與 letter 不限 revision（該狀態尚無判定，直接沿用 active）；score 只取 `filter_revision` 與傳入 active 值相符者，篩選判定過時者留給使用者重新處理 |
| `AdoptStageRevision(stage, jobID, revisions)` | 讓尚無判定的職缺換上該階段的 active revision：`filter` 限 `new`（同時清空 `score_revision`），`score` 限 `queued` 且 `filter_revision` 為 active。回報是否採用；已離開該狀態或篩選判定過時者回 false，呼叫端不得執行該階段。不動 `updated_at`——這是尚未發生的工作的簿記，不是職缺本身的變更 |
| `CountAwaitingReprocess(revisions)` | 計算因篩選判定過時而無法被任何階段取件的職缺筆數（`queued` 且 `filter_revision` 非 active），供 worker 在消化停滯時記錄原因 |
| `SaveFilterResult(jobID, result, revision)` | 在單一交易內附加一筆 `filter_results` 並依 `outcome` 與該筆是否只有摘要轉換狀態（`fail`→`filtered_out` 並寫 `filter_hits`、摘要且 `unknown`→`discovered`、`pass`→`queued`；全文而 `unknown` 為契約違反，回錯）；以 expected state ＋ expected `filter_revision` CAS |
| `LinkOrSuggestDuplicate(jobID)` | upsert 後依 §4.2 計算 `dedupe_key`：高信心則於單一交易合併（選定 canonical、alias 轉 `merged`、收斂 `group_id`），灰帶則 upsert 一筆 `pending` 候選；回傳實際動作 |
| `MergeGroups(a, b)` / `UnmergeJob(jobID)` | 使用者裁決：前者依 canonical 選擇順序合併並將候選標記 `merged`；後者依合併事件還原 alias 狀態與獨立群組，不刪除既有 score／letter |
| `ListDuplicateCandidates(limit, cursor)` / `IgnoreCandidate(id)` | 疑似重複清單與忽略 |
| `ReprocessJob(jobID, revisions)` | 單筆重新處理：於單一交易回到管線起點（有 JD 全文者 `new`、只有摘要者 `discovered`）、寫入 active `filter_revision`、清空 `filter_hits`／`score_revision` 與該 Job 的 `filter_results`，並寫 `manual reprocess` 事件；已在起點者為冪等（仍校正 revision）；求職信階段與 `merged` 回 `ErrReprocessNotAllowed` |
| `CountJobsByState()` / `RecentAgentCalls(limit)` | 處理進度查詢：各處理狀態的職缺筆數、最近的 Agent 呼叫稽核（失敗才附截斷輸出） |
| `ActivateProfile(from, to)` | `from`／`to` 各為一組 `{filter_revision, score_revision}`；依 §3.2 判斷哪一組變更、在單一交易內切換可重新處理的 Job；回傳 partial screened、refiltered、requeued、protected、unchanged 統計 |
| revision-aware CAS | filter／score／transition 寫入皆驗證 expected state 與該面的 expected revision；舊 snapshot 結果不得成為現行判定 |
| `CountAgentCallsSince(role, since)` | 每日預算計數（見 [design-pipeline](design-pipeline.md) §5） |
| `SummarizeRunJobs(runID)` | 依 `discovered_by_run_id` 即時導出該輪職缺的現行判定分布 |
| `SaveScore / SaveLetter / SaveAgentCall / StartRun / FinishRun` | 寫入各實體 |

migration 新增 revision 欄位時全部允許 legacy NULL，不猜測歷史資料使用的 Profile。升級與服務啟動不自動 activation；legacy Job 維持 stale，直到使用者明確要求更新過時評分。migration 本身不呼叫 Agent。

**schema v6（硬／軟分離）migration**：新增 `filter_results` 表、`jobs` 與 `agent_calls`／`letters` 的雙 revision 欄位與 `scores` 的四維欄位。既有 `scores` 的五維資料與舊維度欄位一併移除——維度定義已改，舊分數無從換算。**全部既有職缺重置回篩選前狀態**（`filtered_out`／`queued`／`scored`／`shortlisted` 中有 JD 全文者回到 `new`、無全文者回到 `discovered`；原本就是 `discovered` 者維持），兩個 revision 欄位清為 NULL，之後由 worker 重篩、通過者重評。求職信階段的職缺（`letter_requested`／`letter_ready`／`letter_failed`）、既有 Letter 與投遞歷史不得因此改寫或刪除。

**schema v8 migration**：新增 `settings` 表。既有資料不受影響；未曾寫入的鍵由讀取端各自帶預設值，migration 不預先塞入任何列。

## 6. 交付物

- `internal/store/`：schema.sql（embedded）、migration、上述介面實作與單元測試（暫存目錄真 SQLite）。
- 測試涵蓋：唯一鍵去重、雜湊變更重置、非法狀態轉換被拒、事件寫入與交易一致性、跨來源正規化與合併／取消合併的交易一致性、篩選結果保存與彙總後的狀態轉換、雙 revision CAS 與 v6 重置 migration。

## 7. 待決

（無——欄位如需增補由後續模組設計回饋本文件。）
