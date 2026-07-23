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
| ST-01 | 對空資料庫執行 migration | 建立 `jobs`、`scores`、`letters`、`status_events`、`runs`、`agent_calls`，並將 `PRAGMA user_version` 設為最新版本 |
| ST-02 | 對已在最新版本的資料庫再次執行 migration | 成功完成；schema、資料與版本不變 |
| ST-03 | 建立重複 `(source, external_id)` 的 Job | 寫入被唯一鍵拒絕；store 的 upsert 不產生第二筆 Job |
| ST-04 | 寫入依附不存在 Job 的 score、letter、status event 或 agent call | 外鍵約束拒絕寫入 |
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
| ST-20 | 依設計表執行各合法轉換 | `discovered→filtered_out/new`、`new→filtered_out/queued`、`queued→scored/shortlisted`、`shortlisted→letter_requested`、`letter_requested→letter_ready/letter_failed`、`letter_failed→letter_requested` 成功 |
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
| ST-40 | 為同一 Job 連續儲存不同 revision 的 score | 兩筆皆保留；取得現行評分時只回與 `jobs.profile_revision` 相同的最新一筆 |
| ST-41 | 儲存 score 的任一維度超出 0–100、reason 超過 50 字、runner 非法 | 被拒絕且不寫入資料 |
| ST-42 | 儲存 approved 或 failed letter | 完整保留內容、輪數、審查紀錄、draft/review runner 與建立時間 |
| ST-43 | 儲存 letter 時 status、rounds、runner 或內容不合法 | 被拒絕且不寫入資料 |
| ST-44 | `ListJobs` 以 process state、apply state、source 篩選與評分排序 | 僅回傳符合篩選的 Job，排序與指定條件一致 |
| ST-45 | `PickForStage` 分別取得 filter、score、letter 階段工作 | 僅選取 `new`、`queued`、`letter_requested` Job，並遵守 limit；`shortlisted` Job 不被任何階段取件 |

### 3.6 Run 與 Agent 稽核紀錄

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-50 | `StartRun` 後 `FinishRun` | 同一 Run 具有 started/finished 時間、合法 trigger、抓取事實 stats JSON（fetched／new／queries／errors）與可選 error 摘要 |
| ST-51 | 使用不合法 trigger 或無效 stats JSON 建立/結束 Run | 被拒絕，資料庫無部分紀錄 |
| ST-52 | `UpsertJob` 新建職缺並帶入 runID | 寫入 `discovered_by_run_id`；同一職缺後續 upsert（含 partial 補全文）不更動該欄位 |
| ST-53 | `UpsertJob` 以 NULL runID 新建職缺（capture 入庫） | `discovered_by_run_id` 為 NULL |
| ST-54 | `SummarizeRunJobs(runID)` | 依 `discovered_by_run_id` 回傳該輪職缺的現行判定分布；不含其他輪次與 capture 入庫的職缺 |
| ST-55 | `CountAgentCallsSince(role, since)` | 只計該 role 於區間內的成功呼叫；失敗呼叫不計入每日預算 |
| ST-52 | 儲存成功與失敗的 Job 相關 agent call | 保留角色、runner、input/output、ok、duration 與時間；Job 外鍵正確 |
| ST-53 | 儲存校準用途的 agent call | `job_id` 可為 NULL；其餘必填欄位仍受驗證 |
| ST-54 | agent call 含非法 role、runner、ok 值、負 duration 或 PII 命中 | 被拒絕且不寫入資料 |

### 3.7 Profile revision 與 activation

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| ST-60 | migration 升級既有資料 | Job／Score／Letter／Agent call 新欄位存在；legacy rows 保持 NULL，不猜測 revision |
| ST-61 | 保存新 Score、Letter 與 Profile 相關 agent call | 寫入實際 revision；歷史 append-only，不覆蓋舊產出 |
| ST-62 | 查詢現行 Score | 只取與 `jobs.profile_revision` 相同的最新 Score；不同 revision／NULL 不當成現行值 |
| ST-63 | 新 revision activation 狀態矩陣 | partial 與 full Job 依設計重設；letter_requested／ready／failed、Letter 與 apply 歷史完全不變 |
| ST-64 | 同 revision activation | no-op；不新增狀態事件、不重設狀態 |
| ST-65 | 舊 worker 以舊 revision 寫 filter／Score／transition | expected state＋revision CAS 拒絕；現行 Job 與 Score 不變 |

## 4. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- 空 SQLite 資料庫可由 migration 建立，重複執行不破壞資料。
- Job upsert、內容變更重置、兩條狀態軸及其事件寫入符合 [design-schema](../designs/design-schema.md) 的唯一權威定義。
- 非法輸入或轉換不留下部分資料；後續模組只能經 `internal/store` 操作狀態。
