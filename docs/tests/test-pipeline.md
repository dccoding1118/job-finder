# 測試規格 — pipeline（`internal/pipeline`）

對應 [pipeline 模組設計](../designs/design-pipeline.md)、[agents 模組設計](../designs/design-agents.md)、PRD R1、R3、R4、R5、R7、R8、R9。本文件涵蓋 `letter` 按需生成、104 ingest、兩關判定，以及 Profile activation／revision-aware worker；L1 以真 SQLite、合成 Job 與 fake Runner 驗證狀態推進、冪等與稽核，不呼叫真實來源或 CLI Runner。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件已實作批次的 PT-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| Store | 每個案例使用獨立暫存 SQLite，經公開 store 介面建立各狀態的 Job 與檢查狀態事件、letter、run、agent call |
| Job、Profile 與信件 | 全為合成內容；不得含姓名、聯絡方式、學校、公司、真實 JD 或 PII pattern |
| Agent | fake Runner 依角色回傳預定 JSON 序列或錯誤；可觀測每次呼叫與其順序 |
| 時間與 rate limit | 注入 clock 與 sleeper；測試不得實際等待 |
| 設定 | 明確指定 `max_filter_per_day`／`max_score_per_day`／`max_letter_per_day`、timeout、角色 primary/fallback、四維權重與基準分、字數上限與 denylist；硬規則由合成 Profile 導出，不讀取使用者本機檔案 |

## 3. B3 單元測試案例：`letter` 階段與按需生成

以下案例除 PT-20–PT-23 外，起始 Job 均為使用者已要求生成的 `letter_requested`。

### 3.1 按需生成的觸發（PRD R5.0）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-20 | 存在達閾值的 `shortlisted` Job，但無任何使用者要求 | letter 階段取件為零；不呼叫 Drafter 或 Reviewer、不新增 letter 或 agent call；Job 維持 `shortlisted` |
| PT-21 | 對 `shortlisted` Job 呼叫 `RequestLetter` | 僅經 store 轉為 `letter_requested` 並寫 process event；worker 於下次掃描取得該 Job |
| PT-22 | 對 `letter_failed` Job 呼叫 `RequestLetter` | 轉為 `letter_requested`；既有 failed letter 與 review log 保留為歷史 |
| PT-23 | 對非 `shortlisted`／`letter_failed` Job（如 `scored`、`letter_ready`），或對已是 `letter_requested` 的 Job 重複呼叫 `RequestLetter` | 前者回傳非法轉換錯誤且狀態不變；後者為冪等，不新增事件、不重複啟動工作 |

### 3.2 `letter` 階段編排

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-30 | 一筆 `letter_requested` Job 的草稿通過防線，Reviewer 首輪核准 | 儲存一筆 approved letter 與審查歷程，Job 僅經 store 轉為 `letter_ready`，並建立 process event 與各次 agent call |
| PT-31 | Reviewer 前兩輪要求 revise，第三輪核准 | 依序傳遞各輪 issues，保存第三份通過的信件與完整 review log；重寫次數不超過兩次 |
| PT-32 | 三輪審查均為 revise | 保存 failed letter 與 review log，Job 轉為 `letter_failed`；不得留下可當成完成稿的 letter |
| PT-33 | 草稿或 Reviewer 編輯稿被佔位符、技術詞、PII 或字數防線拒絕 | 不接受該版本；若重寫上限耗盡，依 PT-32 結束；錯誤摘要不含信件全文或敏感片段 |
| PT-34 | Drafter 或 Reviewer 經重試與 fallback 後仍失敗 | Job 維持 `letter_requested`，不儲存 letter、不建立 process transition；全部 agent calls 正確記錄，使用者的要求保留待 worker 下次取件重試 |
| PT-35 | 當日 `letter_requested` Job 筆數超過 `max_letter_per_day` | 僅處理上限內的工作，剩餘 Job 維持 `letter_requested` 且不記為錯誤；跨台北日界後恢復取件 |
| PT-36 | 先前已成功轉為 `letter_ready` 的 Job 再次被 worker 掃描 | 不取件、不再次呼叫 Drafter 或 Reviewer，不新增 letter 或 agent call |
| PT-37 | letter 階段完成一部分後 worker 中斷，再重啟 | 已轉為終態的 Job 不重複呼叫；殘留 `letter_requested` Job 續作 |
| PT-38 | `--job` 指向 `letter_requested` Job | 僅處理指定 Job，且遵守相同的防線、上限與稽核語意 |
| PT-39 | 同一 run 的連續 Drafter／Reviewer 呼叫 | 依設定的最小間隔序列化；注入 sleeper 的等待次數與時長符合設定 |
| PT-40 | Profile 儲存變更可接受地點、薪資下限或任一硬規則項目 | worker 不重啟即對新 ingest 使用新 snapshot；既有 Job revision 與狀態不變，不讀取或要求 `config.yaml` 的重複求職條件 |
| PT-41 | 兩輪 run 之間變更 `llm.roles` | 新一輪的評分、起草與審查使用新路由；每次 Agent 呼叫記錄實際 runner |

## 4. B5 單元測試案例：ingest 入口

以合成的解析結果直接呼叫 ingest 入口；不啟動 HTTP server、不解析真實頁面。104 與 Cake 共用同一入口與同一組案例，差異只在解析器產出的 `source`。

### 4.1 列表 ingest（PRD R9.1、R3.7）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-50 | 全新的列表項目通過欄位可用的結構化硬規則 | 建立 `discovered` Job；回傳現行狀態、`is_new` 為真、score 與 filter hits 為空；全程零 Agent 呼叫 |
| PT-51 | 全新的列表項目命中職稱、地點、薪資、工作型態或公司黑名單 | Job 經 store 轉為 `filtered_out` 並記錄未通過的條件名稱；回傳同一組 filter hits，供插件標記不適合 |
| PT-52 | 列表項目對應已存在的 Job（涵蓋 `discovered`、`filtered_out`、`scored`、`shortlisted`、`letter_ready` 各狀態） | 僅更新 `last_seen_at`；不覆蓋內容、不改狀態、不重跑篩選或評分；回傳該 Job 的現行狀態與現行 score，`is_new` 為偽 |
| PT-53 | 同一批次含重複 external ID 或需要內文的條件（工作內容排除） | 每個 external ID 只建立一筆 Job；需要內文與語意判斷的條件不在此階段套用，不因缺 JD 全文而誤判為淘汰 |
| PT-54 | 任一列表項目解析後缺必要欄位 | 該筆回傳項目錯誤，其餘項目仍完成 ingest；不寫入猜測資料 |
| PT-55 | 整批列表 ingest | 注入的 Runner 完全未被呼叫；rate limiter 未被觸發 |

### 4.2 內頁 ingest（PRD R9.2）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-60 | 內頁素材補全既有 `discovered` Job 的 JD 全文，且通過結構化與語意條件 | Job 依序經 store 轉為 `new`、`queued`，完成評分後為 `scored` 或 `shortlisted`；ingest 同步回應為 `new`／`pending_score`，四維分數與 reason 由後續查詢取得；`is_cached` 為偽 |
| PT-61 | 內頁素材建立全新 Job（未經列表收割） | 建立 `new` Job 後走完同一條篩選與評分路徑，結果與 PT-60 一致 |
| PT-62 | 內頁素材補全文後命中需要內文的條件 | Job 同步轉為 `filtered_out` 並記錄未通過條件；不呼叫任何 Agent；回傳 filter hits 且 score 為空 |
| PT-63 | Job 已有現行評分且內容雜湊未變 | 直接回傳既有四維分數與 reason，`is_cached` 為真；不新增 score、不呼叫 Agent |
| PT-64 | Job 已有評分但內頁全文的內容雜湊已變 | 依 store 語意重置為 `new` 後重走篩選與評分，新增一筆 score；既有 score 保留為歷史 |
| PT-65 | 內頁 ingest 使一筆 Job 成為 `shortlisted` | 不呼叫 Drafter 或 Reviewer、不建立 letter；Job 停留 `shortlisted` 等待使用者要求 |
| PT-66 | 內頁 ingest 後 worker 的 Filter 或 Scorer 經重試與 fallback 後仍失敗 | Job 分別維持 `new`／`queued` 留待下次取件；回傳安全錯誤且不含 Agent 原始輸出 |

## 4.3 跨來源分群鉤點（B6，PRD R2.8）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-110 | fetch 與兩個 capture 入口各 upsert 一筆職缺 | 三條路徑都在 upsert 之後、回傳判定之前呼叫一次分群；回傳的 job id 與 verdict 皆為群組 canonical |
| PT-111 | capture 命中已與其他來源合併的 alias | 同步回傳 canonical 的既有 verdict 與分數；不建立新 Job、不重跑篩選或評分、零 Agent 呼叫 |
| PT-112 | 合併發生後 worker 取件 | `merged` 的 alias 不被 filter／score／letter 任一階段取件 |
| PT-113 | `dedupe.enabled` 為 false | 不呼叫分群；各來源職缺維持獨立，判定回傳其自身 |

## 4.4 B7 單元測試案例：兩關判定

### 4.4.1 結構化硬規則

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-80 | 逐項 `requirements` 條件的正例、反例與缺資訊例（清單摘要與全文各一） | `pass`／`fail`／`unknown` 依 [design-pipeline](../designs/design-pipeline.md) §3.2 表；同一筆缺資訊在摘要判 `unknown`、在全文判 `pass`；規則由合成 Profile 導出，不讀 `config.yaml` |
| PT-81 | `requirements.remote` 四值 × JD `full`／`hybrid`／`onsite`／未提及 | 完全符合 §3.3 矩陣；未提及一律視同 `onsite`（`required` 判 `fail`、`rejected` 判 `pass`），此條不產生 `unknown` |
| PT-82 | 任一結構化條件判 `fail` | 該筆同步 `filtered_out`，**不呼叫 Filter Agent**；agent call 數為零 |
| PT-83 | 結構化條件全過 | 恰呼叫 Filter Agent 一次 |
| PT-84 | 選定地區鍵 × JD 地點寫成簡體、繁體、英文或含行政區後綴；`nationwide` × 台灣地點與海外地點；`overseas` × 兩者 | 同一鍵的各種寫法皆判 `pass`；`nationwide` 對台灣任一縣市判 `pass`、對海外判 `fail`；`overseas` 反之；地點為空字串或哨兵值 `unknown` 皆判 `unknown`，不判 `fail` |

### 4.4.2 語意篩選與彙總

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-85 | Filter Agent 回的條件含一筆 `fail` | Job 轉 `filtered_out`；逐條判定與拆解保存；不呼叫 Scorer |
| PT-86 | Filter Agent 對全文 JD 回的條件無 `fail` 但有 `unknown` | Job 轉 `queued`；該條件仍以 `unknown` 保存可讀出；`filter_hits` 為空（沒有任何條件擋下它） |
| PT-87 | Filter Agent 回的必備條件全 `pass`，另含未滿足的 `bonus` 條件 | Job 轉 `queued`；加分條件不影響彙總，且保存供 `bonus_fit` 重用 |
| PT-88 | 選言分組內一條 `pass`、一條 `fail` | 該組視為 `pass`；彙總不因組內 `fail` 判不適合 |
| PT-89 | JD 要求「N 年以上」而 `derived.total_years` 高於、等於、低於 N | 前兩者 `pass`、後者 `fail`；年資超出不判不適合也不扣分；Agent 回的年資 verdict 一律被程式覆寫 |
| PT-90 | JD 明確設年資上限且 profile 超出 | `fail` |
| PT-91 | JD 要求管理年資或特定產業年資 | 以 `derived.management_years`／對應 `industry_years` 加總比較，Agent 只提供對應 key 與要求年數 |
| PT-92 | 學歷要求：profile 為 `[碩士·機械, 學士·資工]` | 「大學資工」`pass`、「碩士資工」`fail`、「碩士不限科系」`pass` |
| PT-93 | LLM 篩選呼叫失敗（額度、認證、服務中斷） | 該筆不寫 `filter_results`、狀態維持 `new`；下一輪 worker 重跑整套篩選 |
| PT-94 | 當日語意篩選超過 `max_filter_per_day` | 僅處理上限內工作，其餘停留 `new` 且不記為錯誤；跨台北日界後恢復 |

### 4.4.3 四維評分

prompt 組裝與各維給分規則屬 agents 模組（見 [test-agents](test-agents.md) §4.4）；本節只驗 pipeline 端。

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-96 | Scorer 回四維分數 | 依設定權重（0.50／0.20／0.15／0.15）由程式計算總分並依閾值分流；Agent 不回總分 |

## 5. Profile activation、競態與預算

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-70 | `filter_revision` 變更後啟用，涵蓋 partial／full 各可重處理狀態 | 清除舊 filter hits 與判定、切換 revision、依矩陣重新篩選；通過者重新排入 score |
| PT-70B | 只有 `score_revision` 變更後啟用 | `scored`／`shortlisted` 回 `queued` 重評；`filtered_out`／`discovered` 狀態與篩選結果完全不變 |
| PT-70A | 只儲存新 revision，未送手動 reprocess | 既有 partial／full Job、Score 與狀態完全不變；worker 在 Filter／Scorer 前跳過 stale revision，不呼叫 Agent；新 ingest 使用新 revision |
| PT-71 | `letter_requested`／`letter_ready`／`letter_failed` 與 applied Job | 狀態、Letter、apply state／event 不變；不自動呼叫 Drafter／Reviewer |
| PT-72 | 同 revision 重複啟用 | 狀態、事件、Score 與 Agent call 數量不變 |
| PT-73 | 重新排入 score 的數量超過每日預算 | 上限內逐步處理；其餘停留 queued，跨台北日界續作 |
| PT-74 | score worker 持舊 snapshot 執行時，儲存新 Profile 後手動 reprocess | 舊 Agent call 保留實際 revision；activation 切換 Job revision後，舊結果 CAS 失敗，不成為現行 Score |
| PT-75 | letter worker 執行時啟用新 revision | 工作以起始 snapshot 完成並保存其 revision；Letter 保留且導出 stale，不自動重跑 |
| PT-76 | Profile missing／invalid／degraded | worker、run 與 ingest 處理暫停或回 profile_not_ready；provider ready 後自動喚醒 |
| PT-77 | 對已評分 Job 呼叫 `RequestReprocess` | 以當下 active revision 排回 `new` 並立即回；worker 下一輪重篩該筆、通過者重評並附加新 Score，其他 Job 不產生 Agent 呼叫 |
| PT-77B | 對 `filtered_out` Job 呼叫 `RequestReprocess` | 回到 `new`（只有摘要者回 `discovered`）、`filter_hits` 與 `filter_results` 清空、可被 filter 階段取件 |
| PT-77C | 擷取 `filtered_out` 職缺的 JD 內頁 | 狀態與命中維持不變、JD 全文入庫、不被任何階段取件、不產生 Agent 呼叫 |

## 6. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- B3 僅讓使用者已要求（`letter_requested`）且通過程式防線與 Reviewer 核准的 Job 進入 `letter_ready`；重寫上限耗盡者進入 `letter_failed`。未經要求的 `shortlisted` 不產生任何 Agent 呼叫。
- B5 的列表 ingest 全程不呼叫 LLM，且對既有 Job 只回報既有判定；內頁 ingest 同步完成結構化判定，語意篩選與評分交 worker，且不生成求職信。
- B7 的硬規則彙總正確：任一 `fail` 判不適合；無 `fail` 時，清單摘要歸待看等補全文、全文 JD 進待評分；缺資訊一律不判不適合；評分輸入不含履歷敘事。
- worker 重啟、每日上限與 Agent 失敗不得造成已完成 Job 重複計費，且狀態、letter、Run 與 agent call 保持可稽核。
- 真實 CLI Runner 與真實職缺僅依 [verify](../verify.md) 的 V3 手動驗收；不進 L1 或例行 CI。
