# 模組設計 — pipeline（排程層 / 流程編排與硬規則篩選）

對應需求：R3、R7、R8。`jobfinder run` 的實作主體。

## 1. 職責邊界

- 抓取的排程編排（fetch），以及 filter（硬規則）→ score（軟規則）→ letter 三階段的**常駐消化**；各階段從 store 取件、呼叫對應模組、寫回狀態。求職信階段只處理使用者已要求的職缺（`letter_requested`）。
- Profile 硬規則的篩選實作（結構化比對於本模組、語意條件交 agents）、Profile activation 與既有 Job 重新處理，以及供半被動擷取使用的 ingest 入口。
- 冪等、序列化、LLM rate limit、每日預算、Run 紀錄。
- 不負責：抓取細節（crawler）、LLM 呼叫（agents）、狀態轉換合法性（store）。

## 2. 執行模型

抓取與處理是**兩個獨立驅動的面**：抓取是 IO 密集、可排程、成批的；處理是 LLM 密集、逐項、隨時有件的（排程 fetch、CLI 與 extension capture 三個入口都會產生待處理職缺）。兩者不共用生命週期。

### 2.1 抓取（排程驅動）

```
jobfinder run [--source NAME]
  1. StartRun(trigger)
  2. 逐全自動 source（Yourator）抓取 → store.UpsertJob（記 discovered_by_run_id）
     → store.LinkOrSuggestDuplicate（跨來源分群，見 §3.5）
     source 級錯誤記 stats.errors 續行
  3. FinishRun(stats)
```

`run` 只做 fetch，抓完即退出，不等待任何 LLM 階段。Run stats 只記**抓取事實**：`fetched`（本輪取得筆數）、`new`（本輪新建筆數）、`queries`（實際展開的搜尋條件）、`errors`。相同來源內容重跑時 `new` 為零。

該輪職缺的判定分布（推薦／不推薦／評分中／不適合）**不寫入 stats**，而是展開該輪時經 `jobs.discovered_by_run_id` 即時查詢——worker 是非同步的，fetch 結束時該批職缺尚未評分完，任何當下的統計快照都會過期（PRD R8.1）。

104 與 Cake 為半被動來源，不經 `run` 抓取；其職缺由使用者導覽觸發的 capture 入庫（§2.3），不屬於任何 run，`discovered_by_run_id` 為 NULL。

排程：systemd user timer 每日一次（台北時間，預設 08:30）呼叫 `jobfinder run`；部署細節見 [deploy](../deploy.md)。

### 2.2 處理（常駐 worker）

worker 隨 API server process 常駐（同 binary、同 systemd service），持續掃描 store 的待處理狀態並逐項消化：

| 階段 | 取件狀態 | 動作 | 結果 |
|---|---|---|---|
| score | `queued` | Scorer → SaveScore | `scored`（total < 閾值）∣ `shortlisted`（≥ 閾值） |
| filter | `new` | 硬規則判定（§3）：程式比對結構化條件；全過者呼叫 Filter Agent 取條件拆解與語意條件判定 → SaveFilterResult；判定為 `queued` 者於同一輪接著評分 | 依 §3.4 彙總：`filtered_out`（任一 `fail`）∣ `queued`（其餘）∣ `discovered`（該筆其實只有摘要，退回待看補全文） |
| letter | `letter_requested` | Drafter／Reviewer | `letter_ready` ∣ `letter_failed` |

**取件順序是 score → filter，且 filter 通過者於同一輪接著評分**：判準是**每一筆多久拿到最終判定**。score 取件**重複到取不到為止**才輪到 filter——單次取件受批次上限（50）與當日剩餘預算所限，等待評分的職缺可能多於一次取件量，必須全數處理完才開始篩選新職缺。已篩過的職缺只差一次評分呼叫就有結論，若讓 filter 先跑，它會被整批尚未開始的職缺擋在後面；而剛篩過的職缺若排到批次尾端才評分，一批數十筆下來第一筆要等上一小時才有分數。兩者合起來讓單筆從「開始處理」到「有結論」等於它自己的兩次呼叫。此順序與 LLM prompt cache 無關——每次呼叫的 prompt 尾端都是該筆 JD，快取命中與否取決於前綴（角色指令與 Profile 子集），不受兩個角色交錯與否影響。

`run --stage filter` 是使用者指名的單一階段，不做接續評分：指名哪個階段就只做哪個階段。

filter 階段是**兩段式**：先跑不耗 token 的結構化比對，任一條 `fail` 即結束（不呼叫 Agent）；只有結構化條件全過的職缺才付一次 Filter Agent 呼叫。LLM 不可用時（額度、認證、服務中斷）該筆不寫任何結果、不改狀態，留在 `new` 由下一輪重跑整套篩選。

worker 是 process 內背景消化者，與使用者的單筆插隊請求（§2.4）共用一道 process 內閘門；資料正確性仍由 store 的 expected state ＋ expected revision CAS 保證，不能以閘門取代。無待處理件時休眠等待，有件即取，因此排程 fetch、CLI 與 extension capture 三個入口寫進來的職缺走的是同一條消化路徑，沒有「等下一輪」的空窗。

**自動處理開關**：使用者可在 Side Panel 關閉自動篩選與評分（設定存於 store，見 [design-api](design-api.md) §4）。關閉時 worker 每輪跳過 filter 與 score 取件，職缺停留 `new`／`queued`；**批次進行中關閉也立即生效**——開關在每筆之間重讀，不是每輪只讀一次：一趟批次可能夾帶數十筆、跑上一小時，剛關掉開關的使用者不該為那一小時繼續付費。已在進行中的那一次 Agent 呼叫照常完成並記錄，其餘職缺保持原狀態等下次。收集（fetch 與 capture）、結構化硬規則與 letter 階段皆不受影響——letter 只處理使用者已明確要求的職缺，關閉它等於讓使用者的要求無故落空。開關的唯一效果是停止**自動**的 token 消耗；單筆插隊處理仍可用。設定檔的 `worker.paused` 是另一個軸：它讓 serve 完全不啟動 worker、也不持有 worker 鎖，改由 `run --stage` 手動批次消化，此模式下插隊入口一律拒絕。

filter、score 與 letter 每筆工作開始時各自從 Profile provider 取得一次 immutable snapshot。舊 revision 的職缺該等使用者、還是該直接沿用 active revision，取決於**該筆是否已握有判定**：

| 狀態 | revision 過時時的處理 | 理由 |
|---|---|---|
| `new`（等待篩選） | 該階段先以 store 的 `AdoptStageRevision` 將 `filter_revision` 換成 active（`score_revision` 清空），再以當下 Profile 篩選 | 尚無任何判定可作廢，本來就要篩一次，換 revision 不增加任何呼叫成本 |
| `queued` 且 `filter_revision` 為 active | 同上換 `score_revision`，再評分 | 尚無分數可作廢，評分呼叫本來就要付 |
| `queued` 且 `filter_revision` 過時 | 取件即排除，等使用者重新處理 | 該筆的篩選判定已作廢，重篩要再付一次 Filter 呼叫，該由使用者決定 |
| `filtered_out`／`scored`／`shortlisted` 等已有判定者 | 不屬任何階段取件範圍，等使用者重新處理 | 同上，且會覆寫使用者看過的判定 |

**該限制下推到取件查詢**（`PickForStage`）：`new` 與 `letter_requested` 不限 revision，score 階段則限 `jobs.filter_revision` 為 active。否則被排除的職缺以較舊的 `updated_at` 永遠排在最前面、取滿每次取件上限後被逐筆丟棄，相符的職缺永遠輪不到。letter 記錄工作開始時實際取得的 revision，不要求與既有 Score 相同，取件也不限 revision。Profile 為 `missing`、`invalid` 或 `degraded` 時 worker 暫停取件。

被「篩選判定過時」擋住的職缺數量改變時，worker 記一行 Info（§6.1），因此消化停滯時 log 有可讀的原因與筆數，而非靜默。

**letter 階段只處理使用者已要求的職缺**（PRD R5.0）：`shortlisted` 不是取件狀態，達閾值的推薦職缺停留在該狀態直到使用者要求。使用者的要求由 API（[design-api](design-api.md)）或 `jobfinder letter request --job ID` 經 store 轉為 `letter_requested`，worker 才取件。無待處理要求時，letter 階段自然是零筆、零 Agent 呼叫、零費用。

`RequestLetter(jobID)`：pipeline 提供此入口供 API 呼叫——經 store 將 `shortlisted` 或 `letter_failed` 轉為 `letter_requested` 後即回。worker 自然取件，呼叫端不等待 Agent 完成。


`RequestReprocess(jobID)`：pipeline 提供此入口供 API 呼叫——取當下 active snapshot，經 store 把該筆送回管線起點（有 JD 全文者 `new`、只有摘要者 `discovered`）、寫入 active `filter_revision`、清除舊命中與舊篩選結果後即回，worker 隨後以最新 Profile 重新篩選，通過者再評分。scores 為 append-only，舊 score 保留為歷史，新 score 寫入後才成為現行分數。求職信階段與 `merged` 的職缺不得重新處理，因此它不改寫求職信、投遞歷史與合併裁決。它的成本至多是該筆的一次 Filter 與一次 Scorer 呼叫，與整批 activation 重新處理互不取代——後者依 revision 決定範圍，前者是使用者對單一判定的異議。

`jobfinder run --stage filter|score|letter [--job ID]` 是**除錯用**的第二 process 入口，以 DB 同目錄 lock file（flock）與常駐 worker 互斥。worker 常駐時該鎖多半被占用，此入口僅供 worker 停止時的人工重跑，不是常態路徑。

- **冪等**：狀態即進度。中斷後重啟自然從殘留狀態續作；已完成的 Agent 呼叫不重複（該 job 已離開取件狀態）。

### 2.3 ingest 入口（半被動來源）

pipeline 提供 **ingest 入口**供 API capture endpoint 呼叫（見 [design-api](design-api.md)），104 與 Cake 共用同一入口，差別只在 API 依 payload 的 `source` 選用哪個解析器。兩個入口都同步回傳每筆職缺的**現行 process_state 與現行 score**，讓插件能就地標記判定（PRD R9.1、R9.6）；判定字彙本身由 API viewmodel 導出，pipeline 不定義呈現用語。

| 入口 | 行為 | 回傳 | LLM |
|---|---|---|---|
| `IngestList(items)` | 解析器 → 逐筆比對 `(source, external_id)`：**既有 Job** 只更新 `last_seen_at`（[design-schema](design-schema.md) §4 partial upsert 語意），不重跑任何階段；**新職缺** upsert partial（`discovered`）→ 同步套用欄位可用的結構化硬規則（§3）→ `filtered_out` ∣ 留在 `discovered` | 每筆的 job ID、現行 `process_state`、現行 score（無則 NULL）、未通過的條件（無則 NULL）、是否本次新建 | 不呼叫 |
| `IngestJob(capture)` | 解析全文 → upsert（partial 補全文 ⇒ `new`，或新建 `new`）→ 同步跑結構化硬規則（§3，全欄位） → `filtered_out` ∣ 留在 `new` 待 worker 跑語意篩選；已有現行評分且內容雜湊未變者直接回傳快取 | 該筆的現行 `process_state`、現行 score（尚未評分則 NULL）、未通過的條件（無則 NULL）、是否為快取結果 | 不呼叫 |

兩個 ingest 入口都要求 ready Profile snapshot，並把本次判定綁定其 `filter_revision`；Profile 未 ready 時回 `profile_not_ready`。兩者都**不呼叫 LLM**，皆為同步且毫秒級：結構化硬規則是純字串與數值比對，不需網路也不需 Agent。差別只在可用的輸入——

| 入口 | 輸入 | 可套用的條件 |
|---|---|---|
| `IngestList` | partial（無 JD 全文） | §3 表中「partial 適用」為 ✓ 者 |
| `IngestJob` | 全文 | §3 全部結構化條件；語意條件交 worker |

因此同一份規則在兩個時機各跑一次並非重複判定，而是第二次補上第一次做不到的內文條件；第一次即 `filtered_out` 的職缺不會有第二次（已離開取件範圍）。

`IngestJob` 通過結構化條件者留在 `new` 由 worker 非同步完成語意篩選與評分，**不在 capture 路徑上等待任何 Agent**（PRD R9.2）。被篩掉者的 `filtered_out` 則在同步回應中即得——插件據此立即呈現「不適合」，只有通過結構化條件的才需等待後續結果（Side Panel 的呈現見 [design-extension](design-extension.md) §4.2）。`IngestJob` 不生成求職信——推薦職缺一律停留在 `shortlisted` 等待使用者決定（PRD R5.0）。

`IngestList` 的設計約束是**即時性**：使用者仍停在 104 清單頁，回應必須在該頁面可用的時間內完成，因此整條路徑不含任何 LLM 呼叫與網路抓取（PRD R3.7、R9.1）。

### 2.4 單筆插隊處理（`ProcessJobNow`）

`ProcessJobNow(jobID)`：pipeline 提供此入口供 API 呼叫（[design-api](design-api.md) §4），對使用者當下正在看的**單一等待中職缺**立即完成篩選，通過者於同一次呼叫續完成評分。來源狀態只有 `new` 與 `queued`，其餘狀態回 `ErrNotWaiting`。

**revision 過時不擋此入口**：對單筆按下「馬上處理」就是使用者要求以當下 Profile 判定這一筆，與重新處理是同一份同意。因此 `new` 直接沿用 active revision 篩選；`queued` 但篩選判定已過時者，先以 `RequestReprocess` 送回管線起點，再於同一次呼叫完成篩選與評分（有 JD 全文者續作，只有摘要者停在 `discovered` 等補全文）。

| 面向 | 規則 |
|---|---|
| 自動處理開關 | 不受限：開關治理的是自動消化，插隊是使用者對這一筆的明確要求 |
| 每日預算 | 不受限；呼叫照常寫入 `agent_calls`，當日用量如實反映實際支出 |
| 呼叫間隔 | 不套用 `llm.min_interval`：間隔是連續批次的節流，單筆請求沒有前一筆 |
| 序列化 | 與 worker 共用閘門，仍是一次一個 Agent 呼叫 |
| 完成通知 | 無：API 受理即回，結果由前端輪詢職缺讀取面取得 |

**插隊機制**：閘門守的是 **Agent 呼叫本身**而非整趟批次——一趟批次可能消化數十筆，若以整趟為單位，插隊請求得等上數分鐘。批次迴圈在每筆之前檢查是否有插隊請求在等待、以及自動處理開關是否仍為開，任一成立即收工（未處理的職缺保持原狀態，下一輪續作），因此插隊最多只等當下這一次 Agent 呼叫。閘門另以 job id 記錄 process 內認領：worker 取件與插隊請求可能指向同一筆，未認領者跳過，避免同一筆付兩次呼叫；結果正確性仍由 store CAS 保證。

## 3. 硬規則篩選（R3）

搜尋條件由 Profile `search.directions` 導出，目的是找齊可能合適的職缺；硬規則篩選使用同一份 Profile 判定「適合與否」，排除平台搜尋難以表達的限制，以及搜尋結果中的贊助或模糊命中職缺。硬規則的結論不是分數：條件不符即淘汰。

### 3.1 結構化條件（程式比對，零 token）

規則由 `requirements` 導出。**partial 職缺（`discovered`，無 JD 全文）只套用欄位可用的條件**——需要內文的條件留待補入全文（→ `new`）後執行，避免以標題錯殺錯類別但實際相關的職缺：

每條逐條判定照常記錄自己的真實結果，包含 `unknown`；`unknown` 對職缺的意義由 §3.4 的彙總決定，不在單條規則上做特例。

| Profile 欄位 | 條件 | `fail` 判定 | `unknown` 判定 | partial 適用 |
|---|---|---|---|---|
| `requirements.exclude_title_keywords[]` | 職稱排除 | 職稱含任一關鍵字（如「實習」「約聘」「業務」） | — | ✓ |
| `requirements.exclude_description_keywords[]` | 工作內容排除 | 工作內容含任一關鍵字（如「需輪班」「駐點外派」） | — | ✗ |
| `requirements.exclude_companies[]` | 公司排除 | 公司名含黑名單字串（如派遣人力公司） | — | ✓ |
| `requirements.locations[]` | 地點 | 地點不在可接受地區且非遠端 | 來源未陳述地點（欄位為空或存哨兵值 `unknown`） | ✓ |
| `requirements.remote` | 遠端 | 依 §3.3 矩陣 | 不產生 | ✓ |
| `requirements.employment_types[]` | 工作型態 | JD 型態不在所選型態內 | JD 未揭露型態 | ✓ |
| `requirements.salary_min` | 薪資 | `salary_max` 有值且低於下限 | 薪資面議／未揭露（NULL） | ✓ |
| `requirements.industry_avoid[]` | 排除產業 | 公司產業命中排除項 | 產業無從判斷 | ✓ |

關鍵字比對不分大小寫。partial 判定於 `IngestList` 入庫時同步執行（§2.3）；worker 的 filter 階段只處理 `new`。

需要內文的條件在 partial 上**不可近似執行**：104 搜尋頁的列表摘要是繞著關鍵字命中處拼接的片段而非 JD 前綴（見 [design-crawler](design-crawler.md) §2.2），片段未出現某詞不表示 JD 無該詞，據此判定會產生假淘汰。此類條件一律等補入全文（→ `new`）後才執行。

批次來源（Yourator）若列表回應不含全文亦會產生 partial 職缺，該類職缺不套用 partial 篩選，停留 `discovered` 進入待看清單——partial 篩選只在清單 capture 路徑上執行，因為只有該路徑需要同步回傳就地標記。

### 3.2 語意條件（Filter Agent ＋ 程式加總）

結構化條件全過的職缺才呼叫 Filter Agent 一次（契約見 [design-agents](design-agents.md) §3.1），取得 JD 的條件拆解與語意條件判定：

| 條件 | Agent 負責 | 程式負責 |
|---|---|---|
| 學歷／科系 | 判斷是否存在某筆 `qualifications.education[]` 同時滿足級別與科系相容性 | 無 |
| 必備技能、必要證照、必要語言 | 比對 `qualifications` 的三個清單 | 無 |
| 年資、管理年資 | 讀出 JD 要求的下限／上限 | 以 `derived.total_years`／`derived.management_years` 比較 |
| 必要產業經驗 | 回答 JD 要求對應到 profile 的哪些 `industry` key、要求幾年 | 取 `derived.industry_years` 相加後比較 |

年資的「N 年以上」是下限：`derived.total_years >= N` 即 `pass`，年資超出**不扣分、不判不適合**；只有 JD 明確設上限（如「限 3 年以下」）才可能 `fail`。學歷要求必須被**同一筆學歷同時滿足**：例如 profile 為 `[碩士·機械, 學士·資工]` 時，「大學資工」`pass`、「碩士資工」`fail`、「碩士不限科系」`pass`。

拆解中標為**加分**的條件不列入篩選，只進評分關的 `bonus_fit`；標為**必備**且為選言（「A 或 B」）者滿足任一即 `pass`。

### 3.3 遠端矩陣

| `requirements.remote` | 篩選 | `benefit_fit` |
|---|---|---|
| `required` | 只有 JD 明寫全遠端才通過；`hybrid`／`onsite`／未提及 ⇒ `fail` | `full` 加分 |
| `preferred` | 全部通過 | `full` 加較多、`hybrid` 加較少、`onsite` 不加 |
| `acceptable` | 全部通過 | 不加不減 |
| `rejected` | JD 提及全遠端或部分遠端 ⇒ `fail`；`onsite` 與未提及皆通過 | 不加不減 |

JD 未提及遠端即等於現場：`required` 與 `rejected` 都據此定案，此條永遠不會是 `unknown`。`required` 與 `rejected` 是絕對要求，沉默是答案而非資訊缺口。

### 3.4 彙總與保存

彙總一律看**全部條件的綜合結果**，不對個別欄位開特例：

| 綜合結果 | partial（清單摘要） | 全文 JD |
|---|---|---|
| 任一必備條件 `fail` | `filtered_out`（不適合） | `filtered_out`（不適合） |
| 無 `fail`、有 `unknown` | 維持 `discovered`（待看），待補全文後重判 | `queued`（待評分） |
| 全 `pass` | 維持 `discovered` | `queued`（待評分） |

`fail` 恆為決定性，不論同時有多少條 `unknown`。`unknown` 只在「還有資料會進來」時才擋得住職缺：清單摘要是節錄，補上全文後可能就判得出來；全文 JD 則不會再有新資訊，此時扣住它只會讓它永遠停在待看，故一律放行進評分。**缺資訊絕不判不適合。**

待看完全由等待補全文的 `discovered` 承載——沒有另一個「資訊不足」狀態。全文 JD 走到這裡若仍是 `unknown`，是彙總的契約違反，store 會回錯而非落地成狀態。

逐條判定（條件名稱、`pass`／`fail`／`unknown`、必備／加分標記）與 JD 條件拆解一併保存（見 [design-schema](design-schema.md) §2.8），供 UI 呈現「為什麼判不適合」、供使用者調整求職條件（R3.5），並由評分關的 `bonus_fit` 重用，兩關不各自重解一次。

### 3.5 跨來源分群（R2.8）

每次 upsert 之後（fetch 與兩個 capture 入口皆同）呼叫 `store.LinkOrSuggestDuplicate`，規則與交易語意由 store 定義（見 [design-schema](design-schema.md) §4.2）。pipeline 的責任只有三件：

- **在 upsert 之後、判定回傳之前**呼叫，確保 capture 的同步回應已反映合併結果。
- capture 回傳的 job id 與 verdict 一律取 **canonical** 那一筆，alias 不獨立呈現。
- 取件一律排除 `merged`，因此 alias 不消耗任何 filter／score／letter 工作與 LLM 費用。

`dedupe.enabled` 為 false 時整段跳過，各來源職缺維持獨立——此開關是為了在正規化規則調整期間可快速停用，不是常態設定。

`MergeGroups`／`UnmergeJob`／`IgnoreCandidate` 由 API 直接呼叫 store，不經 worker：它們是使用者的同步裁決，不產生非同步工作。

## 4. 手動 Profile activation 與重新處理

Profile 儲存產生新語意 revision 時只切換 provider snapshot；既有 Job、Score 與處理狀態保持原 revision，服務啟動也不自動 activation。新擷取職缺由 ingest 寫入當下 snapshot revision。使用者在系統頁明確要求更新過時評分後，pipeline 以當下 active snapshot 呼叫 store activation transaction，完成本地重新篩選與入隊；常駐 worker 隨後依既有輪詢消化。

**重跑範圍依變更的 revision 決定**：`filter_revision` 改變 ⇒ 全部重篩，通過者再重評；只有 `score_revision` 改變 ⇒ 只重評，不重篩（已 `filtered_out` 與 `discovered` 者不動，硬規則結論未變）。

| 現行資料 | `filter_revision` 改變 | 只有 `score_revision` 改變 |
|---|---|---|
| `discovered`／partial `filtered_out` | 切換 revision、清除舊判定，重做 partial 條件；可在 `discovered` 與 `filtered_out` 間改判 | 不變 |
| 有全文的 `new`／`filtered_out` | 切換 revision、清除舊判定，回到／維持 `new` 重新走兩段篩選 | 不變 |
| 無全文的職缺（僅有清單摘要） | 一律回到 `discovered` 等補全文，不進 filter 階段 | 不變 |
| `queued`／`scored`／`shortlisted` | 同上，回到 `new` | 切換 `score_revision`、回到／維持 `queued` 重新評分；篩選結果保留 |
| `letter_requested`／`letter_ready`／`letter_failed`、Letter、apply history | 保留狀態與歷史，不取消、不重送、不覆寫；由 API 導出 stale | 同左 |

activation 本身只做狀態切換與重新入隊，不呼叫 LLM；重篩的 Filter Agent 呼叫由 worker 逐筆消化。重新評分沿用 `max_score_per_day`，預算用盡時停留 `queued` 跨台北日界續作。相同 revision 重送不得重設狀態或增加事件／Agent 呼叫。Profile 在 activation 或 worker 執行途中再次改變時，舊 snapshot 結果仍由 CAS 拒絕成為現行判定；Agent call 稽核保留實際 revision。

## 5. Rate limit 與每日預算

| 參數（設定檔） | 預設 | 說明 |
|---|---|---|
| `llm.min_interval` | 20s | 相鄰 LLM 呼叫最小間隔（序列化執行） |
| `llm.max_filter_per_day` | 60 | 每日語意篩選上限，超出留待隔日；職缺停留 `new` |
| `llm.max_score_per_day` | 30 | 每日評分上限，超出留待隔日 |
| `llm.max_letter_per_day` | 10 | 每日求職信上限；未處理的 `letter_requested` 留待隔日，使用者的要求不會遺失 |
| `llm.max_letter_rounds` | 3 | 單筆求職信的輪數上限，語意為最多產出第幾版；前 N-1 輪起草並審查，第 N 輪只起草（見 [design-agents](design-agents.md) §5） |
| `llm.timeout` | 300s | 單次 Agent 呼叫逾時 |

每日預算以**台北時間日界**重置，計數依 `agent_calls` 當日該 role 的成功呼叫數導出，不另存計數器（重啟後預算不歸零）。worker 常駐後沒有「輪」可作為上限單位，而 extension capture 由使用者隨時觸發，時間窗預算是成本封頂的唯一著力點。

預算用盡時 worker 停止該階段取件，職缺停留 `new`／`queued`／`letter_requested` 至隔日；此為刻意的成本封頂，不記為錯誤。API 據此讓 Side Panel 呈現「已達今日上限」而非「處理中」。預算封的是自動消化；使用者對單筆的插隊處理（§2.4）不受它限制。

## 6. 錯誤處理

| 情境 | 處置 |
|---|---|
| 單一 source 抓取失敗 | 記 run stats.errors，其他 source 續行 |
| 單筆 Agent 呼叫失敗（重試與 fallback 後仍失敗） | filter 與 score：該 job 停留原狀態（worker 下次掃描重試），記入該 job 的 `agent_calls`；不屬於任何 run |
| letter 階段的 Agent 呼叫失敗 | 該筆立即轉 `letter_failed`，不寫入 Letter，等使用者再次要求。自動重試在此階段是有害的：`letter_requested` 依 `updated_at` 取件，停留原狀態的失敗職缺會永遠排在隊首擋住其他要求，且每次重試付掉一格 `max_letter_per_day`。該筆已離開取件狀態，因此階段本身不回報錯誤；原因記於 Error log 與 `agent_calls` |
| 每日預算用盡 | worker 停止取件至隔日日界；非錯誤，不記 errors |
| fetch 致命錯誤（DB 打不開等） | FinishRun(error) 後非零退出 |
| worker 致命錯誤 | 記錄後由 systemd 重啟 API service；狀態即進度，重啟後續作 |
| Profile 缺少或無效 | setup／invalid 模式；抓取、ingest 與 worker 暫停，Profile 讀寫 API 保持可用 |
| 舊 revision worker 寫回 | store CAS 拒絕，保留 Agent call 稽核，不改現行狀態或 Score |
| activation transaction 失敗 | Profile snapshot 與既有 Job revision 均不變；API 回錯誤，使用者可重試 |

### 6.1 執行可觀測性

worker 與各階段以 `log/slog` 輸出結構化記錄至 stderr，由 systemd 收進 journald（`journalctl --user -u jobfinder-api`）。每筆 Agent 呼叫另有 `agent_calls` 稽核列，經 [design-api](design-api.md) 的 `GET /api/v1/status` 對外呈現。

| 事件 | 級別 | 欄位 |
|---|---|---|
| 取得一批待處理職缺 | Info | `stage`、`jobs`、`budget_remaining`、`budget_limited` |
| 單筆語意篩選開始／完成 | Info | `stage`、`job_id`、`filter_revision`；完成另附 `verdict`（`fail`／`unknown`／`pass`）、`state`、`runner`、`duration_ms` |
| 單筆評分開始／完成 | Info | `stage`、`job_id`、`source`、`score_revision`；完成另附 `total`、`state`、`runner`、`duration_ms` |
| 單筆評分失敗 | Error | `stage`、`job_id`、`duration_ms`、`error` |
| 判定結果因 revision 過期被丟棄 | Info | `stage`、`job_id`、`revision` |
| 批次因自動處理開關關閉而收工 | Info | `stage`、`remaining` |
| 單筆沿用 active revision（`new` 換 `filter_revision`／`queued` 換 `score_revision`） | Info | `stage`、`job_id`、該階段的 revision |
| 因篩選判定過時而待重新處理的筆數改變 | Info | `jobs`、`filter_revision` |
| 單筆重新處理入隊 | Info | `stage`、`job_id`、`filter_revision` |
| 單筆求職信開始／完成／失敗 | Info／Error | `stage`、`job_id`、`state`、`rounds`、`duration_ms`、`error` |
| filter 階段完成一批 | Info | `stage`、`processed`、`filtered_out`、`queued` |
| worker 單次消化 | Info | `filtered`、`scored`、`lettered` |
| 每日預算用盡而略過取件 | Debug | `stage`、`reason`、`max_per_day` |

log 不得含 JD、Profile、薪資、求職信內容或 Agent 原始輸入輸出；只記識別子、狀態與計量。無待處理件的空轉不產生記錄。

## 7. 設定檔（`config.yaml`）

| 區段 | 內容 |
|---|---|
| `db.path` | SQLite 檔路徑 |
| `profile.path` / `profile.denylist` | Profile 與 PII denylist 路徑 |
| `sources.<name>` | enabled、max_pages、request_delay_min/max、retry_max、retry_backoff、check_robots；query 預設由 Profile directions 依順序展開，每個方向一組、每來源最多三組；`sources.yourator.base_url` 為選填端點覆寫，預設正式 Yourator 網域，僅供隔離驗收以本機 fixture 驗證 adapter |
| `scoring` | 四維權重（`content_fit` 0.50、`benefit_fit` 0.20、`bonus_fit` 0.15、`industry_fit` 0.15）、閾值（預設 75）與基準分（預設同閾值） |
| `calibration.min_interviews` | 反向校準門檻（預設 5） |
| `dedupe.enabled` | 是否啟用跨來源分群（預設 true） |
| `dedupe.title_similarity_threshold` | 灰帶候選的職稱相似度下限（預設 0.6） |
| `dedupe.source_priority` | canonical 選擇的來源優先序（預設 `104` > `cake` > `yourator`） |
| `llm` | §5 每日預算、呼叫間隔、求職信輪數上限與 timeout；`llm.roles` 為各角色的 primary/fallback 分別指定 agent CLI 與 model（見 design-agents） |
| `worker.scan_interval` | 常駐 worker 無待處理件時的掃描間隔（預設 5s） |
| `worker.paused` | true 時 serve 不啟動常駐 worker、不持有 worker 鎖，filter／score／letter 改由 `run --stage` 手動批次消化（預設 false）；與 Side Panel 的自動處理開關是不同的軸（§2.2） |
| `api.addr` | B4 API 監聽位址，預設 `127.0.0.1:8686` |
| `api.token` / `api.extension_origin` | API 驗證 token 與允許的 extension origin |

repo 內提供 `configs/config.example.yaml`；實際 `config.yaml` 含本機 token 等執行設定，gitignore。薪資、地點與硬規則條件只存在 `profile.yaml`，不在 config 重複保存。

## 8. 測試

- 全流程整合：Yourator-compatible loopback fixture 經 production adapter 完成 fetch，加上 artifact 內 fake Runner 由 worker 消化至終態，精確斷言來源 request、正規化欄位、各狀態筆數與 run stats。
- fetch 邊界：`run` 只產生 `new`／`discovered` 職缺即退出，斷言不呼叫 Scorer、不寫入判定統計。
- worker：待處理件出現後於掃描間隔內被取件；三個入口（fetch、CLI、capture）寫入的職缺走同一消化路徑。
- 冪等：於 score 階段中斷後重啟 worker，斷言不重複呼叫已完成項。
- 每日預算：超出 `max_score_per_day` 後停止取件、職缺停留 `queued` 且不記 errors；跨台北日界後恢復；計數由 `agent_calls` 導出，重啟不歸零。
- 結構化硬規則：表驅動測試逐項條件的 `pass`／`fail`／`unknown` 正反例，並驗證由 Profile 導出而非讀取 config；遠端四值 × JD 三態的完整矩陣。
- 語意篩選：結構化條件已 `fail` 者不呼叫 Filter Agent；彙總分流（`filtered_out`／`queued`／退回 `discovered`）與逐條判定保存；年資下限不因超出而 `fail`；選言條件滿足任一即 `pass`；加分條件不影響篩選結論。
- `derived` 加總：`exclude_from_totals`、管理年資與各產業年資的比較由程式執行，Agent 只回語意對應。
- 雙 revision 重跑範圍：只改 `intents` 時 `filtered_out`／`discovered` 不動、`scored`／`shortlisted` 回 `queued`；改硬規則欄位時全部回 `new` 重篩。
- 按需生成：`shortlisted` 職缺在無使用者要求時不被 letter 階段取件、不產生 Agent 呼叫；`RequestLetter` 後才進入生成。
- letter 呼叫失敗：該筆轉 `letter_failed` 且不寫入 Letter，同批其餘 `letter_requested` 於同一輪照常被取件消化。
- ingest：列表與內頁 ingest 全程無 LLM 呼叫；內頁 ingest 對通過篩選者留 `queued` 並回 NULL score，對淘汰者同步回 `filtered_out` 與 `filter_hits`；快取命中回現行 score。
- 兩次篩選：partial 入庫只套用「partial 適用」條件；補全文後套用全部條件，且第一次已 `filtered_out` 者不再被取件。
- flock：worker 常駐時第二 process 的 `jobfinder run --stage` 立即退出。
- Profile activation：partial／full 狀態矩陣、相同 revision no-op、重新評分受每日預算、letter／apply 保護、舊 worker CAS 失敗與 in-flight letter 記錄實際 revision。

## 9. 交付物

- `internal/pipeline/`：fetch run 編排、常駐 worker、ingest 入口、`RequestLetter`、`RequestReprocess`、兩段式硬規則篩選、rate limiter、每日預算、lock、設定載入（或獨立 `internal/config`）＋測試。
- `cmd/jobfinder/cli/run.go`、`cmd/jobfinder/cli/letter.go`。

## 10. 待決

（無。）
