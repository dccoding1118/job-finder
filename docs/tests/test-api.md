# 測試規格 — api（`internal/api`）

對應 [api 模組設計](../designs/design-api.md)、PRD R6、R7、R9。L1 使用 `httptest`、暫存 SQLite 與合成 Job／Score／Letter／Run 資料；不啟動真實瀏覽器、不呼叫 Agent、來源或 104。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件已實作批次的 AT-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| Store | 每案例使用暫存 SQLite 與真實 migration；以 store API 建立 Job、分數、信件、Run 與 StatusEvent |
| 合成資料 | 職稱、公司、JD、評分理由與信件皆使用無識別性的合成字串；求職信含兩個固定佔位符 |
| Pipeline | 以可觀測 fake triggerer／ingester／letter requester 取代真實 pipeline；記錄 trigger、payload 與呼叫次數，不呼叫外部程序 |
| HTTP | `httptest`；斷言 status、CORS、JSON schema、資料庫狀態與禁止外洩欄位 |
| 時間 | 注入固定 clock；排序與事件時間可重現 |

## 3. B4 單元測試案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-01 | 正確 token 與 extension origin 查詢 Job，含各 process state 與分數 | 僅回符合篩選的資料，依現行分數降冪；空值為 `null`；每筆附 verdict |
| AT-02 | 非法篩選、limit、cursor 或 Job ID | 回 400；不查詢或寫入猜測資料 |
| AT-03 | 單筆 `letter_ready` Job | 回 JD、評分、核准信件與 StatusEvent；特殊字元維持 JSON 安全編碼 |
| AT-04 | 不存在 Job | 回 404；不洩漏內部錯誤 |
| AT-05 | 各 process state 的 Job 導出 verdict 與 `letter_state` | 逐項符合 [design-api](../designs/design-api.md) §3.1：`filtered_out`⇒`unfit`＋filter hits、`scored`⇒`not_recommended`、`shortlisted`／`letter_requested`／`letter_ready`／`letter_failed`⇒`recommended` 且 `letter_state` 依序為 `none`／`requested`／`ready`／`failed`、`discovered`⇒`pending_detail`、`new`／`queued`⇒`pending_score` |
| AT-06 | 以 `verdict` 篩選 Job 清單 | 僅回傳該判定涵蓋的 process state；非法 verdict 值回 400 |
| AT-10 | 合法投遞狀態與選填 note | 經 store 更新並新增 StatusEvent |
| AT-11 | 非法 apply 狀態、非 `letter_ready` Job 或不存在 Job | 回 4xx；DB 狀態與事件數不變 |
| AT-13 | `GET /queue` | 僅回 `discovered` 與原始連結 |

### 3.1 求職信生成要求（PRD R5.0）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-14 | 對 `shortlisted` Job 送出 `POST /jobs/{id}/letter` | 呼叫 pipeline `RequestLetter` 一次；回 `requested` 與更新後 Job；handler 不等待生成完成 |
| AT-15 | 對 `letter_failed` Job 送出同一路由 | 同樣受理並轉為 `letter_requested`；既有 failed letter 保留 |
| AT-16 | 對 `letter_requested` Job 重複送出 | 冪等回 `requested`；不重複呼叫 `RequestLetter`、不新增事件 |
| AT-17 | 對 `scored`、`discovered`、`letter_ready` 或不存在的 Job 送出 | 回 4xx；不呼叫 pipeline、DB 狀態不變 |
| AT-20 | `POST /runs` 且 pipeline 閒置 | 立即回 `started`；fake triggerer 收到 `manual-extension`，handler 不等待工作完成 |
| AT-21 | `POST /runs` 且抓取已在進行中 | 回 `already_running`；不建立第二次工作 |
| AT-22 | Run 歷史 | 依時間新到舊回傳 trigger、抓取事實（fetched／new／queries／errors）與安全錯誤摘要 |
| AT-23 | Run 歷史中某輪的職缺其後已完成評分 | 該輪回應的判定分布反映職缺**現行**狀態，而非抓取當下的快照；分布經 `SummarizeRunJobs` 即時導出 |
| AT-24 | capture 入庫（`discovered_by_run_id` 為 NULL）的職缺 | 不計入任何 Run 的判定分布 |

## 4. 安全案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-30 | 缺失、錯誤或非 Bearer token；含無 Origin 的 MV3 request | 回 401；不呼叫 store、pipeline 或 ingest |
| AT-31 | 帶入不符的 Origin，token 正確或錯誤 | 回 403；不回 CORS header、不執行業務動作 |
| AT-32 | 正確 origin 的 preflight／request，以及無 Origin 且 token 正確的 MV3 request | preflight 與帶 Origin request 僅回精確 CORS header；無 Origin request 成功但不回 CORS header |
| AT-33 | 非 loopback `api.addr` | 設定驗證拒絕啟動 |
| AT-34 | 錯誤與正常 response | 不含 token、設定內容、Agent 原始輸入輸出或堆疊 |

## 5. B5 擴充案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-50 | 合法 capture list payload | 呼叫 `IngestList` 一次；每筆回傳 job ID、verdict、總分（無則 null）、filter hits（無則 null）與 `is_new` |
| AT-51 | capture list 的項目對應既有 Job | 回傳該 Job 的現行 verdict 與總分，`is_new` 為偽；不觸發評分 |
| AT-52 | capture list 的項目被欄位可用條件淘汰 | verdict 為 `unfit` 並附命中的條件名稱，供插件標記 |
| AT-53 | 合法 capture job payload 且通過條件篩選 | 呼叫 `IngestJob`；回傳 job ID 與 `pending_score`，五維分數與 reason 為 null；handler 不呼叫 Scorer、不等待評分 |
| AT-54 | capture job 的 Job 已有現行評分 | 回傳快取結果（verdict、五維分數、reason）且 `is_cached` 為真 |
| AT-55 | payload 缺必要欄位、Job URL 不合法或 ingest 失敗 | 回 4xx／5xx 安全錯誤；不寫入猜測資料 |
| AT-56 | 兩個 capture endpoint 的成功與失敗回應 | 不含 Agent 原始輸入輸出、prompt 或 token |
| AT-57 | capture job 的 Job 被條件篩選淘汰 | 同步回傳 `unfit` 與 `filter_hits`，不進入待評分 |
| AT-58 | capture job 通過篩選但當日評分預算已用盡 | 回傳 `pending_score` 且 `budget_exhausted` 為真 |

extension 的實機互動不由 API L1 取代，最終以 [verify](../verify.md) 的 B5 手動 Chrome gate 驗收。
