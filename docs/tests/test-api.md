# 測試規格 — api（`internal/api`）

對應 [api 模組設計](../designs/design-api.md)、PRD R1、R6、R7、R9。L1 使用 `httptest`、暫存 YAML／SQLite 與合成 Profile／Job／Score／Letter／Run 資料；不啟動真實瀏覽器、不呼叫 Agent、來源或 104。

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
| Profile | 使用暫存 YAML、fake provider／activator 與合成 JSON；可控制 missing、invalid、ready、ETag conflict 與手動 reprocess 統計 |
| HTTP | `httptest`；斷言 status、CORS、JSON schema、資料庫狀態與禁止外洩欄位 |
| 時間 | 注入固定 clock；排序與事件時間可重現 |

## 3. B4 單元測試案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-01 | 正確 token 與 extension origin 查詢 Job，含各 process state 與分數 | 僅回符合篩選的資料，依現行分數降冪；空值為 `null`；每筆附 verdict |
| AT-02 | 第一頁、下一頁、末頁，以及非法篩選、limit、cursor 或 Job ID | 每頁維持排序且不重複；有後續資料才回 `next_cursor`；末頁為 `null`；非法輸入回 400，不查詢或寫入猜測資料 |
| AT-03 | 單筆 `letter_ready` Job | 回 JD、評分、核准信件與 StatusEvent；特殊字元維持 JSON 安全編碼 |
| AT-04 | 不存在 Job | 回 404；不洩漏內部錯誤 |
| AT-05 | 各 process state 的 Job 導出 verdict 與 `letter_state` | 逐項符合 [design-api](../designs/design-api.md) §3.1：`filtered_out`⇒`unfit`＋filter hits、`scored`⇒`not_recommended`、`shortlisted`／`letter_requested`／`letter_ready`／`letter_failed`⇒`recommended` 且 `letter_state` 依序為 `none`／`requested`／`ready`／`failed`、`discovered`⇒`pending_detail`、`new`⇒`pending_screen`、`queued`⇒`pending_score` |
| AT-07 | Job 有現行篩選結果 | 回 `filter_result` 的 `outcome`、`stage` 與逐條 `conditions`（含必備／加分標記）；無篩選結果時為 `null` |
| AT-06 | 以 `verdict` 篩選 Job 清單 | 僅回傳該判定涵蓋的 process state；非法 verdict 值回 400 |
| AT-10 | 合法投遞狀態與選填 note | 經 store 更新並新增 StatusEvent |
| AT-11 | 非法 apply 狀態、非 `letter_ready` Job 或不存在 Job | 回 4xx；DB 狀態與事件數不變 |
| AT-13 | `GET /queue` | 只回 `discovered`，附原始連結 |
| AT-60 | 對 `new` 或 `queued` Job 送出 `POST /jobs/{id}/process` | 回 202 `processing` 與當下 Job；pipeline 的 `ProcessJobNow` 於背景收到該 job id；handler 不等待 Agent 完成 |
| AT-61 | 對 `scored`、`filtered_out`、`letter_ready` 或不存在的 Job 送出同一路由 | 回 409 `not_waiting`（不存在回 404）；不呼叫 pipeline |
| AT-62 | 服務未帶常駐 worker（`worker.paused`）時送出同一路由 | 回 409 `worker_not_resident`；不呼叫 pipeline |
| AT-63 | `GET`／`PUT /api/v1/settings` | GET 回 `auto_processing`（預設 true）與 `resident_worker`；PUT 寫入後回新值並存入 store；缺 `auto_processing` 回 400 |
| AT-64 | 關閉自動處理後讀 `GET /api/v1/status` | `settings.auto_processing` 為 false，與 `/settings` 一致 |

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
| AT-53 | 合法 capture job payload 且通過結構化硬規則 | 呼叫 `IngestJob`；回傳 job ID 與 `pending_score`，四維分數與 reason 為 null；handler 不呼叫任何 Agent、不等待語意篩選或評分 |
| AT-54 | capture job 的 Job 已有現行評分 | 回傳快取結果（verdict、四維分數、reason）且 `is_cached` 為真 |
| AT-55 | payload 缺必要欄位、Job URL 不合法或 ingest 失敗 | 回 4xx／5xx 安全錯誤；不寫入猜測資料 |
| AT-56 | 兩個 capture endpoint 的成功與失敗回應 | 不含 Agent 原始輸入輸出、prompt 或 token |
| AT-57 | capture job 的 Job 被結構化硬規則淘汰 | 同步回傳 `unfit` 與 `filter_hits`，不進入待處理 |
| AT-58 | capture job 通過篩選但當日評分預算已用盡 | 回傳 `pending_score` 且 `budget_exhausted` 為真 |

| AT-59 | capture payload 的 `source` 為 `104`、`cake`、未知值或缺漏 | 前兩者分派至對應解析器；後兩者回 `400 invalid_request`，不猜測平台 |
| AT-87 | `capture/job` 帶 `source=cake` 與 `cake_dom` 素材 | 走 Cake 內頁 DOM 解析並正常入庫；回應形態與其他 capture 路徑一致 |

extension 的實機互動不由 API L1 取代，最終以 [verify](../verify.md) 的 V5／V6 手動 Chrome gate 驗收。

## 5.1 B6 重複職缺案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-80 | GET jobs／queue 中存在 `merged` Job | 清單只含各群組 canonical；`merged` 不出現、不導出 verdict |
| AT-81 | GET 單筆 Job | 回 `group`：`group_id`、`canonical_job_id`、成員的 job id／來源／連結／external_id、候選筆數；單成員群組亦回傳 |
| AT-82 | GET duplicates | 只回 `pending` 候選，含雙方職稱、公司、地區、來源、相似度與原因；支援 limit／cursor 與非法分頁 400 |
| AT-83 | POST duplicates/{id}/merge | 呼叫 store 合併一次並回更新後的 canonical Job；重複裁決回 409；候選不存在回 404 |
| AT-84 | POST duplicates/{id}/ignore | 候選轉 `ignored` 且不再出現於清單；重複呼叫回 409 |
| AT-85 | POST jobs/{id}/unmerge | `merged` Job 還原為獨立群組與合併前狀態；非 `merged` 回 409、不存在回 404 |
| AT-86 | 合併與裁決 route 的回應 | 皆為同步回應，不建立背景工作、不呼叫 Agent；不含 JD、求職信或 Agent 輸出 |

## 6. Profile、setup 與 stale 案例

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-60 | GET Profile 的 missing／invalid／ready | 回對應 status、ETag、safe issues；ready 才回結構化 Profile（含唯讀 `derived`）、兩個 revision、摘要與預估 |
| AT-61 | PUT 缺 `If-Match`、ETag 不符 | 分別回 428、412；不寫檔；衝突回應不含 Profile |
| AT-62 | PUT 含非法欄位或 PII | 回 422 與安全欄位 issue；不回 denylist 值或完整 payload |
| AT-63 | PUT 只改硬規則欄位／只改 `intents`／只改 `search` 或成就／相同內容 | 依序回 `filter_changed` 為真、只 `score_changed` 為真、兩者皆偽（ETag 變）、兩者皆偽；皆切換 snapshot、既有 Job 不變且不 activation |
| AT-64 | Profile missing／invalid 時呼叫 run 或 capture | 回 409 `profile_not_ready`；健康、Profile、Job／Run 讀取仍可用 |
| AT-65 | Job 的篩選／Score／Letter revision 與 active 相同或不同 | 各 revision 欄位與 `filter_stale`／`score_stale`／`letter_stale` 正確；verdict 與 apply state 不變 |
| AT-66 | Profile missing 或 ready 時 POST reprocess | missing 回 409 且不呼叫 activator；ready 只在 POST 時以 active snapshot 呼叫一次並回重新篩選／重新評分／受保護統計 |
| AT-67 | Profile CORS 與觀測安全 | preflight 允許 PUT／If-Match；observer、log、錯誤與 evidence 不含 Profile body、薪資、經歷或 YAML |
| AT-70 | 對 `scored` Job POST reprocess | 呼叫 pipeline 一次；回 `new` 與更新後 Job（`process_state=new`、verdict `pending_screen`）|
| AT-71 | 對信件階段 Job 或不存在的 Job POST reprocess | 分別回 409 `reprocess_not_allowed` 與 404；狀態不變 |
| AT-71B | 對 `filtered_out` Job POST reprocess | 回 200 與 verdict `pending_screen`；`filter_hits` 與 `filter_result` 皆清空 |
| AT-72 | GET status | 回各處理狀態筆數（含 `new` 與 `queued`）、篩選與評分預算餘額與最近 Agent 呼叫（含 `role=filter`）；失敗呼叫附失敗類別與截斷回應、成功呼叫不附任何一者（低分仍是成功）；非 GET 回 405 |
| AT-86 | GET status 的用量欄位 | 每筆 Agent 呼叫附 `model` 與六個用量欄位；另回 `agent_usage_daily`，每列含 `date`、`runner`、`model`、`calls` 與六個用量欄位加總，依日期新到舊；無任何呼叫時為空陣列而非缺欄位 |
