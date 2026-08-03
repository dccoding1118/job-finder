# 測試規格 — schema/store（`internal/store`）

對應 [schema/store 模組設計](../designs/design-schema.md)、PRD R2.3、R7.1、R8。本模組以暫存目錄中的真 SQLite 資料庫執行 L1 單元測試；不使用 mock SQLite。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件的所有 ST-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| 資料庫 | 每個測試建立獨立暫存 SQLite 檔案，開啟時執行全部 migration |
| 時間 | 注入固定 UTC 時間；資料庫儲存與比較採 RFC3339 |
| Job fixture | 使用合成的公開職缺內容；不含姓名、聯絡方式、學校或公司名稱 |
| Job 識別 | `source` 與 `external_id` 組合唯一；測試使用無真實平台意義的識別值 |
| 交易驗證 | 失敗操作後重新查詢資料庫，確認目標實體與 `status_events` 均未被部分寫入 |

## 3. 單元測試案例

### 3.1 Migration 與 schema

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-01 | 對空資料庫執行 migration | 建立 `jobs`、`scores`、`filter_results`、`letters`、`status_events`、`runs`、`agent_calls`、`job_groups`、`job_dupe_candidates`，並將 `PRAGMA user_version` 設為最新版本 |
| ST-02 | 對已在最新版本的資料庫再次執行 migration | 成功完成；schema、資料與版本不變 |
| ST-03 | 建立重複 `(source, external_id)` 的 Job | 寫入被唯一鍵拒絕；store 的 upsert 不產生第二筆 Job |
| ST-04 | 寫入依附不存在 Job 的 score、letter、status event 或 agent call | 外鍵約束拒絕寫入 |
| ST-06 | 讀寫 `settings` 的 `auto_processing` | 未曾寫入時視為開啟；寫入 off 後重開資料庫仍為 off；再寫回 on 生效 |
| ST-05 | 查詢索引與欄位定義 | `jobs` 具有 process/apply state 查詢索引與 `(source, external_id)` 唯一索引；所有設計定義欄位存在 |

### 3.2 Job upsert、去重與內容變更

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-10 | 首次 upsert 含全文 Job | 建立一筆 `new` Job；設定內容雜湊與 `first_seen_at`、`last_seen_at`、`updated_at` |
| ST-11 | 再次 upsert 相同 `(source, external_id)` 與相同內容 | 只更新 `last_seen_at`；不重置狀態、不新增 Job |
| ST-12 | 已處理的全文 Job 以變更後的 JD upsert | 更新來源內容與雜湊，狀態轉為 `new`；既有 scores 與 letters 保留 |
| ST-13 | 僅變更系統回寫欄位以外的輸入時間，來源內容不變 | 不視為內容變更，不重置狀態 |
| ST-14 | 首次 partial Job upsert | 建立 `discovered` Job；`description` 與 `content_hash` 為 NULL |
| ST-15 | 以完整內容 upsert 已存在 partial Job | 補入內容、首次計算雜湊並轉為 `new` |
| ST-16 | partial Job upsert 遇到既有 Job（含非 `discovered` 狀態） | 僅更新 `last_seen_at`；不覆蓋來源內容、不改變狀態 |
| ST-17 | Job 欄位含不支援的 source、空 external ID、空 title 或非法 remote type | store 拒絕寫入並回傳欄位錯誤 |

### 3.3 `process_state` 狀態轉換與事件

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-20 | 依設計表執行各合法轉換 | `discovered→filtered_out/new`、`new→filtered_out/queued/discovered`、`queued→scored/shortlisted`、`shortlisted→letter_requested`、`letter_requested→letter_ready/letter_failed`、`letter_failed→letter_requested` 成功 |
| ST-25 | 嘗試 `shortlisted→letter_ready`／`letter_failed`，即跳過使用者要求直接生成 | 回傳非法轉換錯誤；`letter_requested` 是進入 letter 終態的唯一前置狀態 |
| ST-21 | 有全文的任一非終態 Job 因內容雜湊變更而轉為 `new` | 狀態重置成功，並寫入一筆 process event |
| ST-22 | 嘗試跳過流程、回退或自終態轉換 | 回傳非法轉換錯誤；Job 狀態與事件數均不變 |
| ST-23 | 合法 process 轉換 | 同一交易更新 Job 狀態與 `updated_at`，新增一筆 axis=`process`、from/to 正確的 event |
| ST-24 | event 寫入失敗 | 整筆轉換回滾，Job 維持原狀態 |

### 3.4 `apply_state` 狀態轉換與事件

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-30 | `letter_requested→letter_ready` | Job 的 `apply_state` 初始化為 `pending`，並保留 process event |
| ST-31 | `pending→applied/dropped`、`applied→interview/ghosted/dropped`、`interview→offer/ghosted/dropped` | 每個合法 apply 轉換成功 |
| ST-32 | 非 `letter_ready` Job 嘗試改 apply state | 被拒絕，不產生 event |
| ST-33 | 嘗試 apply 狀態回退、跳關或自終態狀態轉換 | 被拒絕，現有 apply state 不變 |
| ST-34 | 合法 apply 轉換含使用者 note | 寫入 axis=`apply`、from/to 與 note 正確的 event |

### 3.5 實體 CRUD 與查詢

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-40 | 為同一 Job 連續儲存不同 revision 的 score | 兩筆皆保留；取得現行評分時只回與 `jobs.score_revision` 相同的最新一筆 |
| ST-41 | 儲存 score 的任一維度超出 0–100、reason 超過 500 字元的儲存防線、runner 非法 | 被拒絕且不寫入資料 |
| ST-46 | `SaveFilterResult` 的 `outcome` 分別為 `fail`／摘要 `unknown`／`pass`／全文 `unknown` | 同一交易內附加一筆 `filter_results` 並轉為 `filtered_out`（含 `filter_hits`）／`discovered`／`queued`；全文而 `unknown` 回錯且不落地；逐條 `conditions` 完整保留 |
| ST-47 | 為同一 Job 連續儲存不同 `filter_revision` 的篩選結果 | 兩筆皆保留；現行判定只取與 `jobs.filter_revision` 相同的最新一筆 |
| ST-48 | `SaveFilterResult` 的 `outcome`、`conditions` 列舉或 `stage` 非法 | 被拒絕且不寫入資料，狀態不變 |
| ST-42 | 儲存 approved 或 failed letter | 完整保留內容、輪數、審查紀錄、draft/review runner 與建立時間 |
| ST-43 | 儲存 letter 時 status、rounds、runner 或內容不合法 | 被拒絕且不寫入資料 |
| ST-44 | `ListJobs` 以 process state、apply state、source 篩選與評分排序 | 僅回傳符合篩選的 Job，排序與指定條件一致 |
| ST-45 | `PickForStage` 分別取得 filter、score、letter 階段工作 | 僅選取 `new`、`queued`、`letter_requested` Job，並遵守 limit；`shortlisted` 與 `discovered` Job 不被任何階段取件 |
| ST-80 | `PickForStage` 帶 revision，score 佇列中同時有篩選判定過時（`updated_at` 較早）與 active 的 Job | 只取得 `filter_revision` 為 active 者；篩選判定過時的 Job 不佔用取件上限。filter 階段不限 revision，`new` 一律取得 |
| ST-80A | `AdoptStageRevision` 分別對 `new`、`filter_revision` 為 active 的 `queued`、篩選判定過時的 `queued`、已離開該狀態的 Job | 前兩者換上 active revision 並回報成功（`new` 另清空 `score_revision`），後兩者回報未採用且不改任何欄位；`updated_at` 一律不變 |
| ST-80B | `CountAwaitingReprocess` 帶 active revision | 只計 `queued` 且 `filter_revision` 非 active（含 NULL）的筆數 |

### 3.6 Run 與 Agent 稽核紀錄

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-50 | `StartRun` 後 `FinishRun` | 同一 Run 具有 started/finished 時間、合法 trigger、抓取事實 stats JSON（fetched／new／queries／errors）與可選 error 摘要 |
| ST-51 | 使用不合法 trigger 或無效 stats JSON 建立/結束 Run | 被拒絕，資料庫無部分紀錄 |
| ST-52 | `UpsertJob` 新建職缺並帶入 runID | 寫入 `discovered_by_run_id`；同一職缺後續 upsert（含 partial 補全文）不更動該欄位 |
| ST-53 | `UpsertJob` 以 NULL runID 新建職缺（capture 入庫） | `discovered_by_run_id` 為 NULL |
| ST-54 | `SummarizeRunJobs(runID)` | 依 `discovered_by_run_id` 回傳該輪職缺的現行判定分布；不含其他輪次與 capture 入庫的職缺 |
| ST-55 | `CountAgentCallsSince(role, since)` | 只計該 role 於區間內的成功呼叫；失敗呼叫不計入每日預算 |
| ST-56 | 儲存成功與失敗的 Job 相關 agent call | 保留角色、runner、input/output、ok、duration 與時間；Job 外鍵正確 |
| ST-57 | 儲存校準用途的 agent call | `role` 為 `calibrator`、`job_id` 可為 NULL；其餘必填欄位仍受驗證 |
| ST-58 | agent call 含非法 role、runner、ok 值、負 duration 或 PII 命中 | 被拒絕且不寫入資料 |

### 3.7 Profile revision 與 activation

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-60 | migration 升級既有資料至 v6 | Job／Score／Letter／Agent call 的雙 revision 欄位與 `scores` 四維欄位存在；`filter_results` 表建立；既有 `filtered_out`／`queued`／`scored`／`shortlisted` 職缺一律重置為 `new` 且兩個 revision 清為 NULL；`discovered` 維持；求職信階段職缺、既有 Letter 與 apply 歷史不被改寫或刪除 |
| ST-61 | 保存新 Score、Letter 與 Profile 相關 agent call | 寫入實際 revision；歷史 append-only，不覆蓋舊產出 |
| ST-62 | 查詢現行 Score | 只取與 `jobs.score_revision` 相同的最新 Score；不同 revision／NULL 不當成現行值 |
| ST-63 | `filter_revision` 變更的 activation 狀態矩陣 | 有全文者重設回 `new`、只有摘要者重做可用條件後留在 `discovered`；letter_requested／ready／failed、Letter 與 apply 歷史完全不變 |
| ST-63A | 只有 `score_revision` 變更的 activation | `queued`／`scored`／`shortlisted` 切新 `score_revision` 並回到／維持 `queued`、篩選結果保留；`filtered_out`／`discovered` 完全不動 |
| ST-64 | 同 revision activation | no-op；不新增狀態事件、不重設狀態 |
| ST-65 | 舊 worker 以舊 revision 寫篩選結果／Score／transition | expected state＋該面 revision 的 CAS 拒絕；現行 Job、篩選結果與 Score 不變 |
| ST-66 | 有 JD 全文的 Job 呼叫 `ReprocessJob` | 狀態改為 `new`、採用傳入 `filter_revision`、清空 `filter_hits`／`score_revision`／`filter_results`、寫 `manual reprocess` 事件；舊 Score 保留且仍是現行分數；該 Job 可被 filter 階段取件 |
| ST-66B | 只有摘要的 `filtered_out` Job 呼叫 `ReprocessJob` | 狀態改為 `discovered` 而非 `new`，命中清空 |
| ST-67 | 重複 reprocess、對信件階段 Job 或不存在的 Job reprocess | 重複呼叫為冪等且不重複寫事件；信件階段與 `merged` 回 `ErrReprocessNotAllowed` 且狀態不變；不存在的 Job 回無資料錯誤 |
| ST-68 | 查詢處理進度 | 各處理狀態筆數正確；最近 Agent 呼叫依時間新到舊，失敗附截斷輸出、成功不附任何輸出；非正整數 limit 被拒 |

### 3.8 跨來源分群與合併（B6）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-70 | 公司名分別為「○○股份有限公司」「○○ Co., Ltd.」「○○台灣分公司」 | 三者正規化後相等，落入同一分群鍵 |
| ST-71 | 職稱為「資深後端工程師」與「Senior Backend Engineer」 | 正規化後相等（去資歷修飾＋中英同義詞對照）；「後端實習生」不與兩者相等 |
| ST-72 | 同公司、同正規化職稱、地區分別為同縣市／不同縣市／一方為 remote | 同縣市與 remote 相容者自動合併；不同縣市寫入 `location_mismatch` 候選，不自動合併 |
| ST-73 | 高信心合併 | 單一交易內：canonical 依「有全文 → 較長 → 來源優先序 → 較早」選出、alias 轉 `merged` 並寫事件（note 含合併前狀態與 canonical id）、成員 `group_id` 收斂 |
| ST-74 | alias 已有 score／letter／apply 歷史 | 不自動合併；寫入 `has_output` 候選，雙方狀態與產出皆不變 |
| ST-75 | 職稱 Jaccard 落在門檻上下 | ≥ 門檻且不完全相等 ⇒ `title_similar` 候選；< 門檻 ⇒ 不建立任何關聯 |
| ST-76 | 重複觸發同一組合併或候選 | 候選唯一鍵不重複寫入；已合併的再次呼叫為 no-op，不新增事件 |
| ST-77 | `PickForStage` 與 `ListJobs` 遇 `merged` | 一律排除；alias 不被任何階段取件、不出現在清單 |
| ST-81 | 同一來源的兩筆職缺，公司相同且職稱完全相等／高度相似 | 皆不合併也不寫入候選；跨來源的同一職缺仍正常合併，且合併後該群組涵蓋的來源全部排除於後續比較 |
| ST-82 | 已 `scored` 的職缺以新 revision 重新 ingest 且內容雜湊變更 | 內容欄位更新、狀態維持 `scored`、兩個 revision 保留該判定所屬值；`ListJobs` 與 `CurrentScore` 仍讀得到該評分 |
| ST-78 | `UnmergeJob` | alias 還原為合併事件記錄的合併前狀態與獨立群組；既有 score／letter 不被刪除 |
| ST-79 | `dedupe.enabled` 為 false | 完全不建立群組關聯與候選；既有已合併資料不受影響 |
| ST-83 | 對 v6 資料庫執行 migration 後寫入帶 model 與用量的 Agent 呼叫 | `agent_calls` 具備 `model` 與六個用量欄位，既有資料列 `model` 為 NULL、用量為 0；新資料列原樣保存 runner 自報值 |
| ST-84 | 跨兩個台北日、兩個 runner／model 且含失敗呼叫的 `AgentUsageByDay` | 依「台北日界 × runner × model」分組加總筆數與六個用量欄位；失敗呼叫同樣計入；不自報費用者 `cost_usd` 為 0；早於視窗的呼叫不出現 |

## 4. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- 空 SQLite 資料庫可由 migration 建立，重複執行不破壞資料。
- Job upsert、內容變更重置、兩條狀態軸及其事件寫入符合 [design-schema](../designs/design-schema.md) 的唯一權威定義。
- 非法輸入或轉換不留下部分資料；後續模組只能經 `internal/store` 操作狀態。
