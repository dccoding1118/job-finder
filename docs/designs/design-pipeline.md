# 模組設計 — pipeline（排程層 / 流程編排與初篩）

對應需求：R3、R7、R8。`jobfinder run` 的實作主體。

## 1. 職責邊界

- 抓取的排程編排（fetch），以及 filter → score → letter 三階段的**常駐消化**；各階段從 store 取件、呼叫對應模組、寫回狀態。求職信階段只處理使用者已要求的職缺（`letter_requested`）。
- Profile 求職條件的篩選實作，以及供 104 半被動擷取使用的 ingest 入口。
- 冪等、序列化、LLM rate limit、每日預算、Run 紀錄。
- 不負責：抓取細節（crawler）、LLM 呼叫（agents）、狀態轉換合法性（store）。

## 2. 執行模型

抓取與處理是**兩個獨立驅動的面**：抓取是 IO 密集、可排程、成批的；處理是 LLM 密集、逐項、隨時有件的（排程 fetch、CLI 與 extension capture 三個入口都會產生待處理職缺）。兩者不共用生命週期。

### 2.1 抓取（排程驅動）

```
jobfinder run [--source NAME]
  1. StartRun(trigger)
  2. 逐全自動 source（Yourator/Cake）抓取 → store.UpsertJob（記 discovered_by_run_id）
     source 級錯誤記 stats.errors 續行
  3. FinishRun(stats)
```

`run` 只做 fetch，抓完即退出，不等待任何 LLM 階段。Run stats 只記**抓取事實**：`fetched`（本輪取得筆數）、`new`（本輪新建筆數）、`queries`（實際展開的搜尋條件）、`errors`。相同來源內容重跑時 `new` 為零。

該輪職缺的判定分布（推薦／不推薦／評分中／不適合）**不寫入 stats**，而是展開該輪時經 `jobs.discovered_by_run_id` 即時查詢——worker 是非同步的，fetch 結束時該批職缺尚未評分完，任何當下的統計快照都會過期（PRD R8.1）。

104 為半被動來源，不經 `run` 抓取；其職缺由使用者導覽觸發的 capture 入庫（§2.3），不屬於任何 run，`discovered_by_run_id` 為 NULL。

排程：systemd user timer 每日一次（台北時間，預設 08:30）呼叫 `jobfinder run`；部署細節見 [deploy](../deploy.md)。

### 2.2 處理（常駐 worker）

worker 隨 API server process 常駐（同 binary、同 systemd service），持續掃描 store 的待處理狀態並逐項消化：

| 階段 | 取件狀態 | 動作 | 結果 |
|---|---|---|---|
| filter | `new` | 條件判定（§3） | `filtered_out`（記 `filter_hits`）∣ `queued` |
| score | `queued` | Scorer → SaveScore | `scored`（total < 閾值）∣ `shortlisted`（≥ 閾值） |
| letter | `letter_requested` | Drafter／Reviewer | `letter_ready` ∣ `letter_failed` |

worker 是 process 內單一消化者，以 process 內 mutex 序列化，不需 flock。無待處理件時休眠等待，有件即取，因此排程 fetch、CLI 與 extension capture 三個入口寫進來的職缺走的是同一條消化路徑，沒有「等下一輪」的空窗。

**letter 階段只處理使用者已要求的職缺**（PRD R5.0）：`shortlisted` 不是取件狀態，達閾值的推薦職缺停留在該狀態直到使用者要求。使用者的要求由 API（[design-api](design-api.md)）或 `jobfinder letter request --job ID` 經 store 轉為 `letter_requested`，worker 才取件。無待處理要求時，letter 階段自然是零筆、零 Agent 呼叫、零費用。

`RequestLetter(jobID)`：pipeline 提供此入口供 API 呼叫——經 store 將 `shortlisted` 或 `letter_failed` 轉為 `letter_requested` 後即回。worker 自然取件，呼叫端不等待 Agent 完成。

`jobfinder run --stage filter|score|letter [--job ID]` 是**除錯用**的第二 process 入口，以 DB 同目錄 lock file（flock）與常駐 worker 互斥。worker 常駐時該鎖多半被占用，此入口僅供 worker 停止時的人工重跑，不是常態路徑。

- **冪等**：狀態即進度。中斷後重啟自然從殘留狀態續作；已完成的 Agent 呼叫不重複（該 job 已離開取件狀態）。

### 2.3 ingest 入口（104 半被動）

pipeline 提供 **ingest 入口**供 API capture endpoint 呼叫（見 [design-api](design-api.md)）。兩個入口都同步回傳每筆職缺的**現行 process_state 與現行 score**，讓插件能就地標記判定（PRD R9.1、R9.6）；判定字彙本身由 API viewmodel 導出，pipeline 不定義呈現用語。

| 入口 | 行為 | 回傳 | LLM |
|---|---|---|---|
| `IngestList(items)` | 104 解析器 → 逐筆比對 `(source, external_id)`：**既有 Job** 只更新 `last_seen_at`（§5 partial upsert 語意），不重跑任何階段；**新職缺** upsert partial（`discovered`）→ 同步套用欄位可用的條件篩選（§3）→ `filtered_out` ∣ 留在 `discovered` | 每筆的 job ID、現行 `process_state`、現行 score（無則 NULL）、`filter_hits`（無則 NULL）、是否本次新建 | 不呼叫 |
| `IngestJob(capture)` | 解析全文 → upsert（partial 補全文 ⇒ `new`，或新建 `new`）→ 同步條件篩選（§3，全欄位） → `filtered_out` ∣ `queued`；已有現行評分且內容雜湊未變者直接回傳快取 | 該筆的現行 `process_state`、現行 score（尚未評分則 NULL）、`filter_hits`（無則 NULL）、是否為快取結果 | 不呼叫 |

兩個 ingest 入口都**不呼叫 LLM**，皆為同步且毫秒級：條件篩選是純字串比對，不需網路也不需 Agent。差別只在可用的輸入——

| 入口 | 輸入 | 可套用的條件 |
|---|---|---|
| `IngestList` | partial（無 JD 全文） | §3 表中「partial 適用」為 ✓ 者 |
| `IngestJob` | 全文 | §3 全部條件 |

因此同一份篩選規則在兩個時機各跑一次並非重複判定，而是第二次補上第一次做不到的內文條件；第一次即 `filtered_out` 的職缺不會有第二次（已離開取件範圍）。

`IngestJob` 通過篩選者留在 `queued` 由 worker 非同步評分，**不在 capture 路徑上等待 Scorer**（PRD R9.2）。被篩掉者的 `filtered_out` 則在同步回應中即得——插件據此立即呈現「不適合」，只有通過篩選的才需等待評分結果（sidebar 的呈現見 [design-extension](design-extension.md) §4.2）。`IngestJob` 不生成求職信——推薦職缺一律停留在 `shortlisted` 等待使用者決定（PRD R5.0）。

`IngestList` 的設計約束是**即時性**：使用者仍停在 104 清單頁，回應必須在該頁面可用的時間內完成，因此整條路徑不含任何 LLM 呼叫與網路抓取（PRD R3.4、R9.1）。

## 3. 條件篩選（R3）

搜尋條件由 Profile directions 導出，目的是找齊可能合適的職缺；條件篩選使用同一份 Profile 排除平台搜尋難以表達的薪資、地點、公司與關鍵字限制，以及搜尋結果中的贊助或模糊命中職缺。

規則由 `profile.yaml` 的 `preferences` 與 `preferences.screening` 導出，任一淘汰條件命中即 `filtered_out`。**partial 職缺（`discovered`，無 JD 全文）只套用欄位可用的條件**——需要內文的條件留待補入全文（→ `new`）後執行，避免以標題錯殺錯類別但實際相關的職缺：

| Profile 欄位 | 淘汰條件 | 判定 | partial 適用 |
|---|---|---|---|
| `preferences.screening.exclude_title_keywords[]` | 職稱排除 | 職稱含任一關鍵字（如「實習」「約聘」「主管」「業務」） | ✓ |
| `preferences.screening.exclude_description_keywords[]` | 工作內容排除 | 工作內容含任一關鍵字（如「需輪班」「駐點外派」） | ✗ |
| `preferences.screening.require_any_keywords[]` | 必要關鍵字 | 職稱與工作內容都未命中任何必要關鍵字 | ✗ |
| `preferences.locations[]` | 地點 | 地點不在可接受地點且非 remote | ✓ |
| `preferences.salary_min` | 薪資 | `salary_max` 有值且低於下限（面議 NULL 不淘汰） | ✓ |
| `preferences.screening.exclude_companies[]` | 公司排除 | 公司名含黑名單字串（如派遣人力公司） | ✓ |

partial 條件篩選於 `IngestList` 入庫時同步執行（§2.3）；worker 的 filter 階段只處理 `new`。命中條件名稱寫入 `jobs.filter_hits`，供使用者調整求職條件與稽核（R3.2）。關鍵字比對不分大小寫。

需要內文的條件在 partial 上**不可近似執行**：104 搜尋頁的列表摘要是繞著關鍵字命中處拼接的片段而非 JD 前綴（見 [design-crawler](design-crawler.md) §2.2），片段未出現某詞不表示 JD 無該詞，據此判定會產生假淘汰。此類條件一律等補入全文（→ `new`）後才執行。

批次來源（Yourator／Cake）若列表回應不含全文亦會產生 partial 職缺，該類職缺不套用 partial 篩選，停留 `discovered` 進入待看清單——partial 篩選只在 104 清單 capture 路徑上執行，因為只有該路徑需要同步回傳就地標記。

## 4. Rate limit 與每日預算

| 參數（設定檔） | 預設 | 說明 |
|---|---|---|
| `llm.min_interval` | 20s | 相鄰 LLM 呼叫最小間隔（序列化執行） |
| `llm.max_score_per_day` | 30 | 每日評分上限，超出留待隔日 |
| `llm.max_letter_per_day` | 10 | 每日求職信上限；未處理的 `letter_requested` 留待隔日，使用者的要求不會遺失 |
| `llm.timeout` | 300s | 單次 Agent 呼叫逾時 |

每日預算以**台北時間日界**重置，計數依 `agent_calls` 當日該 role 的成功呼叫數導出，不另存計數器（重啟後預算不歸零）。worker 常駐後沒有「輪」可作為上限單位，而 extension capture 由使用者隨時觸發，時間窗預算是成本封頂的唯一著力點。

預算用盡時 worker 停止取件，職缺停留 `queued`／`letter_requested` 至隔日；此為刻意的成本封頂，不記為錯誤。API 據此讓 sidebar 與 extension page 呈現「已達今日上限」而非「處理中」。

## 5. 錯誤處理

| 情境 | 處置 |
|---|---|
| 單一 source 抓取失敗 | 記 run stats.errors，其他 source 續行 |
| 單筆 Agent 呼叫失敗（重試與 fallback 後仍失敗） | 該 job 停留原狀態（worker 下次掃描重試），記入該 job 的 `agent_calls`；不屬於任何 run |
| 每日預算用盡 | worker 停止取件至隔日日界；非錯誤，不記 errors |
| fetch 致命錯誤（DB 打不開等） | FinishRun(error) 後非零退出 |
| worker 致命錯誤 | 記錄後由 systemd 重啟 API service；狀態即進度，重啟後續作 |

## 6. 設定檔（`config.yaml`）

| 區段 | 內容 |
|---|---|
| `db.path` | SQLite 檔路徑 |
| `profile.path` / `profile.denylist` | Profile 與 PII denylist 路徑 |
| `sources.<name>` | enabled、max_pages、request_delay_min/max、retry_max、retry_backoff、check_robots；query 預設由 Profile directions 依順序展開，每個方向一組、每來源最多三組；`sources.yourator.base_url` 為選填端點覆寫，預設正式 Yourator 網域，僅供隔離驗收以本機 fixture 驗證 adapter |
| `scoring` | 五維權重、閾值（預設 75） |
| `calibration.min_interviews` | 反向校準門檻（預設 5） |
| `llm` | §4 每日預算、呼叫間隔與 timeout；`llm.roles` 為各角色的 primary/fallback 分別指定 agent CLI 與 model（見 design-agents） |
| `worker.scan_interval` | 常駐 worker 無待處理件時的掃描間隔（預設 5s） |
| `api.addr` | B4 API 監聽位址，預設 `127.0.0.1:8686` |
| `api.token` / `api.extension_origin` | API 驗證 token 與允許的 extension origin |

repo 內提供 `configs/config.example.yaml`；實際 `config.yaml` 含本機 token 等執行設定，gitignore。薪資、地點與條件篩選只存在 `profile.yaml`，不在 config 重複保存。

## 7. 測試

- 全流程整合：Yourator-compatible loopback fixture 經 production adapter 完成 fetch，加上 artifact 內 fake Runner 由 worker 消化至終態，精確斷言來源 request、正規化欄位、各狀態筆數與 run stats。
- fetch 邊界：`run` 只產生 `new`／`discovered` 職缺即退出，斷言不呼叫 Scorer、不寫入判定統計。
- worker：待處理件出現後於掃描間隔內被取件；三個入口（fetch、CLI、capture）寫入的職缺走同一消化路徑。
- 冪等：於 score 階段中斷後重啟 worker，斷言不重複呼叫已完成項。
- 每日預算：超出 `max_score_per_day` 後停止取件、職缺停留 `queued` 且不記 errors；跨台北日界後恢復；計數由 `agent_calls` 導出，重啟不歸零。
- 條件篩選：表驅動測試逐項求職條件的正反例，並驗證由 Profile 導出而非讀取 config。
- 按需生成：`shortlisted` 職缺在無使用者要求時不被 letter 階段取件、不產生 Agent 呼叫；`RequestLetter` 後才進入生成。
- ingest：列表與內頁 ingest 全程無 LLM 呼叫；內頁 ingest 對通過篩選者留 `queued` 並回 NULL score，對淘汰者同步回 `filtered_out` 與 `filter_hits`；快取命中回現行 score。
- 兩次篩選：partial 入庫只套用「partial 適用」條件；補全文後套用全部條件，且第一次已 `filtered_out` 者不再被取件。
- flock：worker 常駐時第二 process 的 `jobfinder run --stage` 立即退出。

## 8. 交付物

- `internal/pipeline/`：fetch run 編排、常駐 worker、ingest 入口、`RequestLetter`、filter 規則、rate limiter、每日預算、lock、設定載入（或獨立 `internal/config`）＋測試。
- `cmd/jobfinder/cli/run.go`、`cmd/jobfinder/cli/letter.go`。

## 9. 待決

（無。）
