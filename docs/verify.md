# 驗收測試 — job-finder

> **單一累加式題目卷**：每個 V/N 案例＝**題目**（驅動動作）＋**標準答案**（字面可比對的預期）＋**具體測資**；模組完成即補上可執行案例，全案例最終串成使用者的完整日常求職迴圈。
> **題目卷 ↔ 答案卷**：本檔是題目卷；`mise run e2e-mock` 每趟產生 `evidence/<timestamp>-mock.md` 答案卷（逐案例的實際觀察值與判定，案例 ID 與本檔 §4 對齊）。**人工驗收＝拿答案卷逐案例對本檔標準答案**（§7）。
> **只驗真程式真的跑得出來的部分**。mock fixture（合成 Yourator／104 頁、fake CLI Agent、隔離 Chromium）**允許但明標「模擬，不等於真來源、真 CLI Agent 或實際 Chrome 已驗收」**；真依賴另走 V3 live（§6）與人工 Chrome gate（§9）。
> **三態判定**：`PASS` ／ `ENVIRONMENT_BLOCKED`（exit 2，外部依賴不可用，未判定產品）／ `FAIL`（exit 1，產品行為不符），不得以「安全完成」當 PASS。
> 標準答案的機器真相源是 `scripts/verify/oracle/assert-positive.mjs`（mock）與 `internal/liveverify` 的判準（live）；本檔為人重述並指向它們，兩者不得分歧。對應 `docs/design.md` §8 測試策略。最後更新：2026-09-14。

負向案例（N）目前僅保留骨架（§5），待正向流程穩定後，以相同的需求對照與 evidence 格式累加；不阻礙目前 V 的交付。

## 1. 怎麼跑起來

在專案根目錄執行：

```bash
mise run e2e-mock     # 物化隔離 artifact → 依序跑 V1/V2/V4/V5/V7 → 產生答案卷
mise run e2e-deploy   # 在隔離根內生成無既有安裝的環境 → 跑部署驗收自動組（§6.1）
```

前兩者是**開發階段驗收**，跑在隔離沙盒內。真來源、真 Agent 與真 Chrome 的驗收跑在**測試環境**（§6、§6.1 人工組），環境分界見 [deploy](deploy.md) §1。

- **沙盒**：`.local-dev/dev-verify/`（gitignored 隔離根，0700，不碰日常 Profile/設定/SQLite）。
  `artifact/` 承載 product binary、extension 與 production unit templates；
  `harness/` 承載 mock fixture/fake Agent；`runtime/` 產生 config、SQLite、browser profile 與 rendered units；
  `evidence/` 保留歷次答案卷。整個沙盒是可重建的產物，刪掉再跑一次即回到同一狀態。腳本位於 `scripts/verify/`（沙盒 runbook 於根層——一般 mock、Profile mock 與 worker mock 各一支；browser E2E 於 `browser/`、Playwright 設定 `playwright.config.js`）。打在測試環境的實機驗收內建於 binary（`jobfinder verify live`，實作在 `internal/liveverify/`），不在沙盒內執行。
- **跑完要檢查哪些產物**（人工照著看一遍）：

  | 產物 | 位置 | 看什麼 |
  |---|---|---|
  | 答案卷 | `.local-dev/dev-verify/evidence/<最新>-{mock,profile-mock,worker-mock}.md` | 逐案例觀察值＋PASS/FAIL；收尾 tally 與使用者故事重建 |
  | SQLite snapshot | `<binary> verify snapshot --db .local-dev/dev-verify/runtime/mock.db` | 5 筆 Job 的終態、篩選逐條判定、四維分數、letter 輪次、狀態事件 |
  | browser evidence | `evidence/extension-browser.json`、`evidence/extension-dashboard.png` | extension 模擬互動的安全摘要與截圖 |

- **`e2e-deploy` 的沙盒**：`.local-dev/dev-verify/deploy/`，每趟開始前重建、結束後刪除。它把 `HOME`（Linux）或 `LOCALAPPDATA`（Windows）指向該目錄，因此 `jobfinder install` 的每個落點都在隔離根內，日常使用的設定、Profile 與 SQLite 不受影響。判準與涵蓋範圍見 §6.1。

- **跑到哪停**：`mise run e2e-mock` 一趟涵蓋 V1／V2／V4／V5、V6 全部步驟、V7 的 S30–S36 與 S39 系列，以及 V8 的 S46、S52（✅），依序產生一般 mock、Profile mock 與 worker mock 三份答案卷。V3 live 需真 Yourator＋已授權 `claude`/`codex` CLI，在 Linux 與 Windows 測試環境各執行 `jobfinder verify live`（⏳，§6）。實際 Chrome 安裝與相容性一律人工 gate（👤，§9）。

## 2. 覆蓋度地圖（需求 ←→ 案例）

| 需求 | 成品流程中的驗證 | 正向案例 | 負向案例 | 現況 |
|---|---|---|---|---|
| R1 Profile editor、revision 與 PII 防線 | V1、V7 | S1–S3、S30–S37 | N-P、N-PRF | ◑ |
| R2 Yourator／104／Cake 進同一 Job 流程與跨來源合併 | V2、V5、V6 | S4、S7、S24–S25、S40–S45 | N-SRC | ◑ |
| R3 硬規則篩選（結構化＋語意）、彙總分流與可稽核判定 | V2、V5、V8 | S8、S24、S51–S52 | N-FLT | ◑ |
| R4 CLI Runner 四維評分、閾值分流、設定路由 | V2、V3 | S9 | — | ◑ |
| R5 要求後才生成的 Drafter–Reviewer 與程式防線 | V2、V3 | S5、S10 | N-LTR | ◑ |
| R6 extension 清單/判定/對照/生成/複製/投遞/手動/Run | V3、V4 | S17–S23 | — | ◑ |
| R7 one-shot／timer／手動／冪等／執行上限／自動處理開關與插隊 | V2、V3、V4、V7 | S11、S14、S16、S39、S39B、S39C | — | ◑ |
| R8 Run 摘要、Agent 稽核、安全 evidence | V1–V6 | S12、S46、S54 | — | ◑ |
| R9 半被動列表快速判定、內頁完整評估、待看清單 | V5、V6 | S24–S25、S40–S41 | — | ◑ |

- **✅ 可執行**：已有驗收入口且本次 artifact 可驗證完整案例。**L1**：由模組單元測試涵蓋，e2e 不重跑——S53 的升級前資料庫只有單元測試造得出來。**◑ 部分可驗**：已有可執行子流程，未覆蓋需求完整結果。**⏳ 待實作**：規格已保留、產品或 verifier 尚未交付。**👤 人工 gate**：只能在實機 Chrome 由驗收者操作，**沒有自動化終點**——每次相關改動後由驗收者當場驗，結論不回寫本檔、不附截圖或證據檔，因此此標記是終態，不會轉為 ✅ 或 ⏳。
- 本檔的 ⏳ 一律指「harness 尚未實作」這件待辦；人工 gate 不用 ⏳，以免每次翻閱都被讀成新的待辦。
- V1、V2、V4、V5 共用同一份 mock 答案卷（§4）；V3 走 live（§6）；V6 待 B6 交付；V8 的 S52 併入 Profile mock 一趟，S51 與 S53 由 L1 涵蓋。

## 3. 測資與標準答案（fixture answer key）

mock 一趟用固定合成測資；下表即「標準答案」，逐值由 `assert-positive.mjs` 機器斷言。V 案例（§4）的每一步都對照這裡的具體值。

### 3.1 使用者故事（mock 一趟走完的劇本）

> 載入 **5 筆 Yourator 職缺** → `#1000`「intern」命中排除關鍵字，**當場篩掉**（unfit / filtered_out）→ 其餘 4 筆評分（`#1004` 的薪資與一條必要條件無從判定，但 JD 完整故不擋）：`#1004` 得 **70**、`#1003` 得 **60**（皆 not_recommended）、`#1001` 得 **80**、`#1002` 得 **90**（後兩者達閾值停在 shortlisted，**未經要求不生成求職信**）→ 使用者對 2 筆 shortlisted **要求生成求職信** → `#1002` 首輪即**核准**（approved / round 1）、`#1001` 三輪仍**退回**（failed / round 3）→ 使用者在 dashboard **複製** `#1002` 的信、標記**已投遞**（applied）→ 另在 **104 搜尋頁**載入 2 筆：intern 當場篩掉、senior 列入**待看清單** → 點開 senior **內頁**補全文、過結構化條件後停在 `new`，語意篩選與評分由常駐 worker 接手。

### 3.2 Yourator 五筆測資（標準答案）

| ext_id | 標題 | 公司 | 薪資 | remote | 篩選 | 四維／total | reason | process 終態 | letter | API verdict／letter_state |
|---|---|---|---|---|---|---|---|---|---|---|
| 1000 | Verification intern platform engineer | Example Learning | 60000–70000 | onsite | 命中 `exclude_title_keywords` 與 `salary_floor` | —（null） | — | `filtered_out` | null | `unfit`／— |
| 1001 | Verification failure remote platform engineer | Example Platform | 100000–120000 | remote | pass | 80 | 合成重試情境 | `letter_failed` | null（reviewer 回不合法輸出，全部 runner 耗盡，不產出信件） | `recommended`／`failed` |
| 1002 | Verification ready hybrid backend engineer | Example Services | 110000–130000 | hybrid | pass | 90/90/90/90 → 90 | 合成核准情境 | `letter_ready` | approved／rounds=1／apply pending→applied | `recommended`／`ready` |
| 1003 | Verification low score cloud engineer | Example Operations | 90000–100000 | onsite | pass | 60/60/60/60 → 60 | 合成低分情境 | `scored` | null | `not_recommended`／— |
| 1004 | Verification unknown salary platform engineer | Example Ventures | 未揭露（NULL） | onsite | `salary_floor` 與一條必要條件皆判 `unknown`，JD 完整故彙總為 pass | 70/70/70/70 → 70 | 合成資訊不足情境 | `scored` | null | `not_recommended`／— |

- 共同欄位：`source=yourator`、`url=http://127.0.0.1:18787/jobs/<id>`、`location=Taipei`、`company_info=public listing`、`content_hash=sha256(標題\n描述\n薪資min\n薪資max\nTaipei\nremote)`。
- **Agent 呼叫累計**：篩選階段 `filter=4`（`#1000` 結構化即淘汰，不呼叫）；評分階段 `scorer=4`；要求生成後 `drafter=4`、`reviewer=4`（信件 runner：draft=`claude`、review=`codex`；scorer runner=`claude`）。冪等重跑與列表路徑**零新增**。
- **letter placeholder**：核准信含 `[你的姓名]`、`[你的聯絡方式]` 兩個 placeholder（程式防線，不外洩 PII）。
- **JD 內的招募聯絡方式**：`1004` 的原始頁面附 `Contact hr@verification.invalid or 0912345678`，用以驅動 S54；入庫時即遮罩，`jobs.description` 與其 `content_hash` 皆以 `Contact [EMAIL] or [PHONE]` 為準，下游 prompt 與 `agent_calls` 稽核副本一併帶著佔位符。

### 3.3 104 兩筆測資（V5）

| item | 標題 | 列表判定 | 內頁 capture 後 |
|---|---|---|---|
| v5intern | backend intern engineer | 命中 exclude → `unfit`／`filtered_out` | — |
| v5senior | Senior backend engineer | `discovered`／`pending_detail`，進待看清單 | 同步過結構化條件 → 停在 `new`／`pending_screen`；語意篩選與評分由 worker 接手 |

### 3.3.1 Cake 測資（V6）

Cake 列表以 `__NEXT_DATA__` 與 DOM 收割兩種素材各擷取一次；內頁一律走 `cake_dom`。

| item | 標題／公司 | 列表素材 | 列表判定 | 內頁 capture 後 |
|---|---|---|---|---|
| v6intern | backend intern engineer／Example Services | `__NEXT_DATA__` | 命中 exclude → `unfit`／`filtered_out` | — |
| v6dup | Senior backend engineer／Beta Co., Ltd. | `__NEXT_DATA__` | 與 V5 的 `v5senior`（104，Beta Co）判定為同一職缺 ⇒ 自動合併，回 canonical 的既有判定 | 全文寫入 alias；metadata 行讀出地點 台北市、月薪 120000–150000、remote `hybrid` |
| v6nometa | Platform reliability engineer／Cake Only Labs | `__NEXT_DATA__` | `discovered`／`pending_detail` | metadata 行辨識不到地點、月薪與遠端形式 ⇒ `unknown`／null |
| v6same1、v6same2 | Backend engineer、Backend engineer（Platform），同為 Delta Works | DOM 收割 | 兩筆各自 `discovered`／`pending_detail` | — |
| v6grey | Data platform engineer, Core／Grey Labs 股份有限公司 | DOM 收割 | 與 104 的 `v6greybase`（Senior data platform engineer／Grey Labs）成為灰帶候選 | — |
| v6ignore | Mobile platform engineer, Core／Ignore Works Co., Ltd. | DOM 收割 | 與 104 的 `v6ignorebase`（Senior mobile platform engineer／Ignore Works）成為灰帶候選 | — |

- `v6dup` 與 `v5senior` 的公司名寫法與職稱大小寫刻意不同，用以驗證正規化；地區相容。
- `v6same1`／`v6same2` 公司相同且正規化職稱相等，但同屬 Cake：同來源兩筆一律不進比較（見 [design-schema](designs/design-schema.md) §4.2）。
- 灰帶兩對的職稱 Jaccard 相似度 0.75（相似但不相等），來源分屬 104 與 Cake——同來源的兩筆一律不進比較。`v6grey` 那對由使用者 `merge`、`v6ignore` 那對由使用者 `ignore`。
- 自動合併只在**雙方皆無 score／letter／apply 產出**時成立，因此 Cake 列表 capture 排在 V5 的 104 列表判定之後、104 內頁補全文之前執行。

### 3.4 Profile 與來源請求測資

- Profile（匿名，schema v6）：`experiences` 兩筆（合計 `derived.total_years=8`，管理年資 0）、`qualifications.education=[master · computer science]`、`qualifications.skills=[Java:expert, Go:proficient]`、`requirements`（`salary_min=90000`、`locations=[taipei]`、`remote=acceptable`、`exclude_title_keywords=[intern]`）、`intents`（`salary_target=120000`、`content_likes`／`content_dislikes` 各一條、`industry_interests` 一項）、`search.directions=[P1:cloud architecture, P2:backend engineering, P3:platform reliability]`。
- V7 bootstrap 另以不存在的 Profile 啟動；首次儲存使用同一份合成內容。revision 由 canonical 結構計算，evidence 只記 revision 短碼與筆數，不保存 Profile request／response body。
- 來源請求序：先 `GET /robots.txt` → 3 個方向各一次 `GET /api/v4/jobs`（`term[]`＝`{cloud,platform}`／`{backend,Go}`／`{Kubernetes,reliability}`，page=1）→ 5 個唯一 `GET /jobs/1000..1004`。跨 query 重複項不重抓。

## 4. 正向案例 V（mock，原子案例）

`mise run e2e-mock` 依序執行 V1／V2／V4／V5／V6 與 V7 各步，共用同一 artifact，分別使用一般 mock、Profile mock 與 worker mock 的隔離 SQLite、Profile 與答案卷；任一步失敗即停止並保留隔離目錄。每步一個原子觀察，標準答案為字面值（詳細測資見 §3，機器斷言見 `assert-positive.mjs`）。

| 步 | 動作 | 標準答案（字面預期） | 案例·需求 | 狀態 |
|---|---|---|---|---|
| S1 | 物化隔離 artifact | binary_sha256 == manifest；extension、rendered API/run/timer unit 存在 | V1·R8 | ✅ |
| S2 | 驗證隔離權限 | 驗收 root=0700；Profile／denylist／config=0600 | V1·R1 | ✅ |
| S3 | 驗證匿名 Profile 與 schema | Profile 摘要＝§3.4；schema_version=8、WAL、foreign_keys、10 張表（含 `filter_results` 與 `settings`）；雙 revision 欄位與 `scores` 四維欄位可用 | V1·R1 | ✅ |
| S4 | 啟動 Yourator fixture | `GET /healthz`→200 由本 harness 綁定；external_id 1000–1004 均合成 | V2·R2 | ✅ |
| S5 | 抓取後手動 filter/score（letter 零取件） | `run`＝`fetched:5/new:5`；`--stage filter`＝`filtered_out:1／queued:4`；`--stage score`＝`scored:4`；letter 未要求不取件、不生成 | V2·R5/R7 | ✅ |
| S6 | 驗證來源搜尋請求 | request journal＝§3.4（robots→3 query→5 detail），共 9 筆 | V2·R2 | ✅ |
| S7 | 驗證來源欄位正規化 | 5 筆逐欄＝§3.2 共同欄位＋各自 title/company/salary/remote/content_hash | V2·R2 | ✅ |
| S8 | 驗證硬規則彙總分流與逐條判定 | `#1000` filter_hits=`[exclude_title_keywords, salary_floor]`→`filtered_out`（結構化即淘汰、該筆零 Agent 呼叫）；`#1004` 的 `salary_floor` 與一條必要條件皆判 `unknown`，但 JD 完整故彙總為 `pass`→`queued`，`filter_hits` 為空；其餘 3 筆全 `pass` 進評分；通過結構化條件的 4 筆各恰一次 `role=filter` 呼叫，`filter_results` 逐條含必備／加分標記且未滿足的加分條件不影響彙總 | V2·R3 | ✅ |
| S9 | 驗證評分四維與分流，且 letter 零 Agent | `#1002`=90×4、`#1001`=80、`#1004`=70×4、`#1003`=60×4，reason 各＝§3.2；`#1002/#1001` shortlisted、`#1003/#1004` scored；letter=null、drafter/reviewer 呼叫=0；filter=4、scorer=4 | V2·R4 | ✅ |
| S10 | 要求生成後驗證信件與 Agent 稽核 | 對 2 筆 shortlisted `letter request`→`requested:<id>`；`--stage letter`＝`lettered:2`；`#1002` approved/rounds=1/apply=pending、`#1001` 首輪耗盡全部 reviewer runner，letter 為 null；轉換含 `shortlisted→letter_requested`；calls filter=4/scorer=4/drafter=2/reviewer=4（3 次 ok=false）；runner=checked-in fake | V2·R5 | ✅ |
| S11 | 重跑抓取與階段冪等 | 重跑 `fetched:5/new:0`；`--stage filter`＝`filtered_out:0／queued:0`、`--stage score`＝`scored:0`；Job=5、Score=4、Letter=2、Agent calls=16 均未增 | V2·R7 | ✅ |
| S12 | 安全 SQLite snapshot 與 Run stats | snapshot 只含契約欄位與 hash（JD 只留 `description_sha256`／長度，JD 與 Agent payload 的 PII 只留計數）；首次 `run` 的 `runs.Stats`＝`{fetched:5,new:5,queries:3,errors:0}`、冪等重跑那筆 `new:0`，皆無 filter/score/letter 統計 | V1/V2·R8 | ✅ |
| S54 | JD 與稽核 payload 的 PII 遮罩 | `jobs.description` 全表 `pii_matches=0` 且恰 1 筆（`#1004`）帶 `[EMAIL]`／`[PHONE]` 佔位；`agent_calls` 全表 `pii_matches=0`，其 filter 與 scorer 共 2 筆 payload 帶佔位且 token 用量完整保留（`masked==masked_with_usage`），呼叫未被整筆作廢 | V2·R8 | ✅ |
| S13 | 驗證 rendered systemd units | API/run ExecStart、PATH、DB 路徑正確；timer `OnCalendar=*-*-* 08:30:00 Asia/Taipei`；通過 `systemd-analyze` | V4·R7 | ✅ |
| S14 | transient one-shot service | `systemd-run --user --wait --pipe` 於 rendered PATH 執行 binary，完成一個無外部成本 stage | V4·R7 | ✅ |
| S15 | transient API service（含常駐 worker） | user manager 啟動同一 binary/SQLite 的 API service；loopback health 可達、is-active=active | V4·R6/R7 | ✅ |
| S16 | transient timer（fetch-only） | timer 以 `trigger=timer` 執行；service Result=success、ExecMainStatus=0；snapshot 出現 `Trigger:timer` | V4·R7 | ✅ |
| S17 | localhost API 正向認證 | 精確 extension Origin＋token=200；無 Origin MV3＋token=200；preflight=204 | V4·R6 | ✅ |
| S18 | API 清單、篩選、判定與對照 | 5 筆 verdict/letter_state＝§3.2；source/process/apply/verdict filter 精確；待看 queue 於清單擷取前為空；`#1002/#1001` 詳情四維、`filter_result` 逐條、letter、狀態事件精確 | V2/V4·R6 | ✅ |
| S46 | Agent 用量稽核與每日彙總 | 每筆 `agent_calls` 附實際 model 與該次 token／費用；`GET /status` 的 `agent_usage_daily` 依台北日界 × runner × model 彙總，且各欄加總＝該組呼叫數 × 單次用量；不自報費用的 runner 其 `cost_usd` 為 0 | V1/V2·R8 | ✅ |
| S19 | 載入固定 ID extension 模擬環境 | 固定 unpacked ID `oddnhajj…`；安全摘要 Origin=absent、Authorization present=true、token 已清空 | V4·R6 | ✅ |
| S20 | Side Panel 判定篩選、copy、apply | 四頁籤與 light／dark theme 正常；filters_verified、detail_verified、clipboard==核准合成信、`#1002` apply pending→applied 並寫回 SQLite | V4·R6 | ✅ |
| S21 | Side Panel 求職信生成入口 | 對 `#1001`（letter_failed）再次產生→受理轉 `letter_requested`；狀態事件永久記錄第 2 次 `letter_requested`，worker 隨後取件 | V4·R6 | ✅ |
| S22 | Side Panel 手動抓取與 Run history | manual fetch 完成；API `runs` 出現 `trigger=manual-extension`；批次歷程以中文呈現觸發方式、執行狀態、耗時、fetch stats 與 verdict 分布；抓取期間「進行中」顯示已收筆數與已耗時；存 screenshot | V4·R7 | ✅ |
| S23 | extension mock browser L1 | 專案鎖定 Playwright 19 tests 全 pass：Side Panel（含單筆重新處理與系統頁處理進度）／Profile editor／Options／service worker 的 mock Chrome API 互動，及 content script 於 104 search／notification／detail fixture 上的標記與 active-tab context（搜尋頁 `.jobfinder-mark` 依 verdict 標記、跳過 hotjob 廣告、title／data-gtm 地區薪資照 live selector 讀取；通知頁無 data-gtm 依位置與格式讀取；內頁由 JobPosting JSON-LD 擷取且不注入完整評分 overlay） | V4·R6／R9 | ✅ |
| S24 | 104 清單就地判定且列表路徑零 Agent | v5intern→`unfit/filtered_out`、v5senior→`discovered/pending_detail`；列表路徑只跑結構化硬規則，Agent 呼叫數不變 | V5·R2/R3/R9 | ✅ |
| S25 | 104 既有職缺回判定、內頁 capture 非同步 | 重複 list 對既有職缺 `created=false` 回現行判定；v5senior 進待看 queue；`capture/job`→`new/pending_score`、score=null，語意篩選與評分由 worker 消化 | V5·R9 | ✅ |

### V7 — Profile editor、revision 與重新處理

| 步 | 動作 | 標準答案（字面預期） | 案例·需求 | 狀態 |
|---|---|---|---|---|
| S30 | 以不存在的 Profile 啟動 serve | health、Profile、Job／Run 讀取可用；Profile status=`missing`；worker 與處理型 route 暫停／409 | V7·R1 | ✅ |
| S31 | 從 extension 建立合法合成 Profile | migration 升至含 revision 欄位的 schema；產生 `0600` YAML；provider 不重啟即 ready；回 ETag 與 revision，不自動重處理既有 Job | V7·R1/R6 | ✅ |
| S32 | 建立各處理／letter／apply 狀態後修改 `qualifications`，再手動重處理 | PUT 回 `status=ready`／`score_changed=true` 且雙 revision 皆為新值，儲存當下 snapshot 與存檔前逐位元組相同（既有 Job 保留原 revision）；POST reprocess 回 `status=queued` 且 `activation.refiltered>0`、`activation.protected>0`；reprocess 後 eligible Job 帶新 `filter_revision`，letter／apply 歷史不變 | V7·R1/R7 | ✅ |
| S33 | 重送語意相同 Profile | revision 不變、`semantic_changed=false`；狀態事件、Score 與 Agent call 數不增加 | V7·R1/R7 | ✅ |
| S34 | 外部修改 YAML 後以舊 ETag 儲存 | 回 412；磁碟與 active snapshot 不被舊資料覆蓋；editor 保留草稿 | V7·R1/R6 | ✅ |
| S35 | 送入 unknown field 與合成 PII | 回 422 safe issues；檔案與 snapshot 不變；log／evidence 不含 payload 或 denylist 值 | V7·R1/R8 | ✅ |
| S36 | score worker 執行中更新 Profile 並手動 reprocess | 舊 call 保留實際 revision；activation 切換 Job revision 後，舊結果 CAS 失敗，不成為現行 Score | V7·R1/R7 | ✅ |
| S37 | Chrome 人工 Profile gate | 系統頁各群組、Options 入口、批次時間、手動 reprocess、整數評分與 revision 燈號、全頁表單新增定位、衝突、離頁提醒及 light／dark 可用 | V7·R6 | 👤 |
| S38 | 單筆重新處理與處理進度 | 對已評分職缺按「重新處理」後只該筆回 `new`、worker 重篩並重評附加新 Score，其他職缺 Agent 呼叫數不變；對判不適合的職缺按同一入口後該筆重新進入篩選；系統頁處理進度與 Agent 呼叫紀錄反映該次執行 | V7·R6/R7 | 👤 |

| S39 | 自動處理開關與單筆插隊處理 | 關閉自動處理後，`new`／`queued` 職缺於 worker 掃描間隔內狀態不變且無新 Agent 呼叫；批次正在消化時關閉，最多再完成當下這一筆即停止並記一行 Info；對其中一筆送 `POST /jobs/{id}/process` 回 202，該筆完成篩選與評分且只增加該筆的 Agent 呼叫；每日評分額度已用盡時同一入口仍完成該筆；重新開啟後其餘職缺恢復消化 | V7·R6/R7 | ✅ |

| S39C | 消化順序 | 佇列同時有 55 筆 `queued` 與 1 筆 `new` 時，log 顯示先以單次取件上限 50 取件、再取件消化剩餘 5 筆（未設每日上限時單次取件上限即為批次界限），才篩選該筆 `new` 且它於同一輪接著評分抵達最終判定 | V7·R7 | ✅ |

| S39B | Profile 變更後的等待中職缺 | 儲存改動硬規則的 Profile 且不按「更新過時判定職缺」，開啟自動處理後 `new` 職缺仍被消化並以新 revision 完成篩選與評分；篩選判定已過時的 `queued` 職缺留在原狀態，log 記一行待重新處理筆數；對該筆送 `POST /jobs/{id}/process` 則完成重篩與重評 | V7·R6/R7 | ✅ |

### V6 — Cake 半被動擷取與跨來源合併

本組的列表 capture 排在 V5 的 104 列表判定之後、104 內頁補全文之前執行：自動合併只在雙方皆無產出時成立（§3.3.1）。

| 步 | 動作 | 標準答案（字面預期） | 案例·需求 | 狀態 |
|---|---|---|---|---|
| S40 | Cake 列表 capture（`source=cake`，`__NEXT_DATA__` 素材） | v6intern→`unfit/filtered_out`、v6dup 與 v6nometa→`discovered/pending_detail`；列表路徑 Agent 呼叫數不變 | V6·R2/R3/R9 | ✅ |
| S41 | Cake 內頁 capture 補全文（`cake_dom` 素材） | 全文含職缺描述與職務需求兩段且各自保留區塊標題、`external_id` 與列表項目一致；地點、月薪與遠端形式由 metadata 行辨識，辨識不到時為 `unknown`／null | V6·R2/R9 | ✅ |
| S41b | 同一來源的兩筆相似職缺 capture（DOM 收割素材） | 不自動合併也不出現於 `GET /duplicates`；兩筆各自保有獨立群組與 `discovered` 判定 | V6·R2.8 | ✅ |
| S42 | v6dup 與既有 104 `v5senior` 分群 | 兩筆歸入同一 group；canonical 依來源優先序為 104 那筆；alias `process_state=merged`，狀態事件記錄合併前狀態與 canonical id | V6·R2.8 | ✅ |
| S43 | 合併後的判定與清單 | Cake capture 回 canonical 的 job id 與既有 verdict；`GET /jobs`、`GET /queue` 不含 `merged`；Job 詳情的 `group.members` 含兩個來源連結；Agent 呼叫數不變 | V6·R2.8/R9.7 | ✅ |
| S44 | 灰帶候選與人工裁決 | `v6grey`／`v6ignore` 兩對皆不自動合併，並列於 `GET /duplicates`（`reason=title_similar`、兩側來源分屬 104 與 Cake）；`v6grey` 那對 `merge` 後合併成立、候選轉已裁決；`v6ignore` 那對 `ignore` 後不再出現且兩筆各自獨立 | V6·R2.8/R6.12 | ✅ |
| S45 | 取消合併 | alias 還原為合併前狀態與獨立 group，重新出現於清單；既有 Score 與 Letter 未被刪除 | V6·R2.8 | ✅ |

### V8 — 兩關判定的 Profile 連動（S8／S9 之外的部分）

篩選彙總分流與四維評分本身由主線的 S8、S9 驗證；本組只驗它們與 Profile、狀態機、migration 的連動；硬規則變更後的整批重篩由 V7 S32 驗證。

| 步 | 動作 | 標準答案（字面預期） | 案例·需求 | 狀態 |
|---|---|---|---|---|
| S51 | 摘要職缺不進篩選階段 | 只有摘要的職缺留在 `discovered`，filter 與 score 階段皆不取件；補全文後才進 `new` | V8·R3/R9 | L1 |
| S52 | 只改軟規則的重跑範圍 | 只修改 `intents` 後 PUT 回 `score_changed=true`／`filter_changed=false`；reprocess 後 `filtered_out` 職缺的狀態與逐條判定不變、`role=filter` 呼叫數不增 | V8·R1/R3 | ✅ |
| S53 | v6 migration 既有資料重置 | 升級後既有 `filtered_out`／`queued`／`scored`／`shortlisted` 一律為 `new` 且雙 revision 為 NULL；求職信階段職缺、Letter 與 apply 歷史逐筆未變 | V8·R1 | L1 |

## 5. 負向案例 N（骨架，待正向穩定後累加）

與 V 相同的原子表；每列預期 `ENVIRONMENT_BLOCKED`／`FAIL`／產品拒絕。目前保留骨架與意圖，尚未實作（⏳）；各模組 L1 錯誤案例仍由 `docs/tests/test-<module>.md` 維護。

| 群 | 意圖 | 標準答案（字面預期） | 需求 | 狀態 |
|---|---|---|---|---|
| N-SRC | 來源不可達 vs 零筆／格式錯 | 來源不可達→`ENVIRONMENT_BLOCKED`(2)；可連但零筆或欄位缺失→`FAIL`(1) | R2 | ⏳ |
| N-FLT | denylist／排除關鍵字邊界 | 命中 denylist 的 Profile 輸入被拒且不落庫；排除命中一律 `filtered_out` | R3 | ⏳ |
| N-LTR | 未要求即生成、輪數上限 | 未 `letter request` 前驅動 letter 階段→零取件、零 Agent；超出 review 上限→`failed` 終態 | R5 | ⏳ |
| N-P | PII 防線 | 求職信含實體姓名/聯絡方式而非 placeholder→`FAIL`；evidence 誤含禁記欄位→`FAIL` | R1/R8 | ⏳ |
| N-PRF | Profile 前置條件與條件式儲存 | missing／invalid 的處理型 route→409；缺 If-Match→428；衝突→412；非法／PII→422；均不得改檔或 snapshot | R1/R6/R8 | ⏳ |

## 6. Live 驗收：V3（真來源與已授權 CLI Agent）

跑在**測試環境**的實際安裝上。驗收內建於 binary 的隱藏子命令，Linux 與 Windows 共用同一份實作，兩個平台同一條指令，在任何目錄執行皆可：

```bash
jobfinder verify live                     # 完整模式
jobfinder verify live --recheck-letter    # 求職信補測模式
```

只給測試環境使用：正式環境不跑它，已安裝 binary 的版本不是 `dev (<commit>)` 時步驟 01 即 `FAIL`。

**執行前提**

| 項目 | 平台 | 說明 |
|---|---|---|
| 已安裝的 `jobfinder` | 兩者 | 依 [測試環境指南](guides/test-environment.md) 部署 dev 部署包；設定、Profile、SQLite 與排程的位置一律取自 `jobfinder paths` |
| 已授權的 `claude`、`codex` CLI | 兩者 | 設定路由到的真 CLI；解析到 fake Agent 時為 `ENVIRONMENT_BLOCKED` |
| 連得到正式 Yourator | 兩者 | 真來源抓取 |
| user systemd | Linux | 操作已安裝的 unit |

不需要 git clone、mise 或 `node`，不物化 artifact、不渲染 unit，也不讀 `harness/`、loopback `base_url`、fake Agent result 或 mock SQLite。

### 情境安排與復原

測試環境是長期使用的真實安裝，常駐 worker 在跑、開關與排程由使用者調整。每趟因此分三段：**情境安排** → **驗證** → **復原**。

| 項目 | 驗證需要的狀態 | 讀原值 | 安排 | 復原 |
|---|---|---|---|---|
| API 服務 | 執行中 | Linux `systemctl --user is-active jobfinder-api.service`；Windows api 工作的 `State` | 未執行則啟動並等 API 回應 | 原本未執行則停止，Windows 等 process 消失 |
| `worker.paused` | `false`（單筆入口在未帶常駐 worker 時回 `409 worker_not_resident`） | `GET /api/v1/settings` 的 `resident_worker` | `resident_worker` 為 `false` 時把 `config.yaml` 原檔備份到同目錄的 `config.yaml.verify-live.bak`，只改該鍵為 `false`，重啟 API 服務 | 以備份原檔覆蓋回去並比對 SHA-256，重啟 API 服務，刪除備份 |
| 自動處理開關 | 關閉 | `GET /api/v1/settings` 的 `auto_processing` | `PUT /api/v1/settings` 設 `false` | `PUT` 原值 |
| 每日抓取工作 | 可被手動觸發 | Windows run 工作的 `State` | Windows 為 `Disabled` 時 `Enable-ScheduledTask`；Linux 停用 timer 不影響手動啟動 one-shot，不安排 | Windows 原為 `Disabled` 則 `Disable-ScheduledTask`；Linux 無 |

- **順序**：安排依表列順序，復原反序。`worker.paused` 排在自動處理開關之前：重啟後的服務才帶常駐 worker，開關要在這之後關。
- **復原紀錄**：每項安排前先把原值寫進 `verify/restore.json`。復原在正常結束、驗證失敗、Ctrl+C 時都執行，完成後刪除該檔。上一趟被強制終止而留下該檔時，下一趟（含補測模式）開跑前先依它復原。
- **復原範圍是設定面**：資料面的增量（新抓的職缺、該趟的判定、Agent 呼叫、求職信產製）保留，那是本趟的證據。
- **Windows 改 `config.yaml`** 一律寫無 BOM 的 UTF-8，復原以複製檔案還原位元組。

安排完成後等 `GET /api/v1/status` 的 `in_flight` 清空，記下當下最新一筆 Agent 呼叫的 `id` 作為本趟基準，並記下此刻處於 `letter_requested` 的職缺。

### 受驗職缺的挑選

篩選與評分落在同一筆職缺上，求職信盡量也是同一筆。候選依序：

| 順位 | 來源狀態 | 送進驗證的方式 |
|---|---|---|
| 1 | `shortlisted` | `POST .../reprocess` 送回 `new`，再 `POST .../process` |
| 2 | `scored` | 同上 |
| 3 | `new`（含本趟新抓的） | 直接 `POST .../process` |

```
filter_agent_jobs = 0
for candidate in 候選清單:
    reprocess 回 409（有求職信歷史、merged）或送回 discovered → 下一筆
    process 並輪詢到該筆離開 new／queued 且 in_flight 無該筆
    本筆無 Filter Agent 呼叫（結構化條件 fail，零成本）→ 下一筆，此類跳過至多 10 筆
    filter_agent_jobs += 1
    本筆為評分終態（scored／shortlisted）→ 篩選與評分皆驗到，結束
    filter_agent_jobs == 2 → 評分驗證 ENVIRONMENT_BLOCKED，結束
求職信職缺 = 上面那筆若為 shortlisted，否則庫內任一 shortlisted／letter_failed／letter_ready
    都沒有 → 求職信驗證 ENVIRONMENT_BLOCKED
```

篩選或評分呼叫失敗而該筆留在 `new`／`queued` 時，依 `agent_calls` 的 `failure_kind` 判定：`runner_error`（額度、認證、逾時）為 `ENVIRONMENT_BLOCKED`，其餘為 `FAIL`。

### 求職信額度不足與補測模式

求職信照一般使用者的方式打 `POST /api/v1/jobs/{id}/letter`，受 `llm.max_letter_per_day` 限制。API 讀不到求職信的剩餘額度，額度不足只能從結果辨認：

| 要求後的觀察 | 判定 | 後續 |
|---|---|---|
| 轉為 `letter_ready`／`letter_failed` | 依步驟 07 的判準判定 | 無 |
| 等待三個 `worker.scan_interval` 加一個 `llm.min_interval` 後仍停在 `letter_requested`，且 `in_flight` 無 letter 工作 | `ENVIRONMENT_BLOCKED`，原因記為「求職信當日額度不足，未判定」 | 把職缺 ID、要求時間與要求前最新一次產製的 ID 寫入 `verify/letter-pending.json` |

該筆要求不會消失：跨台北日界後 worker 自行取件產製。

- **補測模式**只讀 `letter-pending.json` 指向的那一筆：已產製者以要求之後的那次產製依步驟 07 判定並刪除紀錄；仍停在 `letter_requested` 者維持 `ENVIRONMENT_BLOCKED`。補測模式不做情境安排、不送任何要求、不花額度，需要 API 服務已在執行。
- **完整模式**開跑時若 `letter-pending.json` 仍在，步驟 07 改為判定該筆，不另送新要求。

### 步驟與判準

| # | 步驟 | 動作 | 判準 |
|---|---|---|---|
| 00 | 環境預檢 | 檢查執行前提；有殘留復原紀錄時先復原 | 前提缺項為 `ENVIRONMENT_BLOCKED` |
| 01 | 安裝身分與設定契約 | `jobfinder version`、`jobfinder paths`；讀已安裝的 `config.yaml` | 版本為 `dev (<commit>)`；`api.addr` 為 loopback、有 token；`db.path` 指向回報的資料庫；Yourator `base_url` 缺省或為官方主機；四個 role 的 primary／fallback 共八個 endpoint 都明確指定 agent 與 model |
| 02 | Profile、權限與 schema | Profile lint、讀已安裝 SQLite 的 verification snapshot | lint 通過；Linux 上設定、Profile、denylist 為 `0600`（Windows 靠 `%LocalAppData%` 的 ACL，不驗權限位元）；schema 通過 oracle |
| 03 | 情境安排 | 見「情境安排與復原」 | 每項安排後重讀一次確認生效 |
| 04 | 真來源抓取 | 經已安裝排程觸發一次抓取並等結束（Linux `systemctl --user start jobfinder-run.service`、Windows `Start-ScheduledTask`） | 該趟 `runs` 列 `trigger` 為 `timer`、`state` 為 `done`、`errors` 為 0、`fetched` ≥ 1；Yourator 職缺通過 oracle 的 source 斷言 |
| 05 | 抓取冪等 | 記指紋，再觸發一次抓取 | 該趟 `new` 為 0；每筆職缺的 `content_hash` 與 `process_state` 前後逐筆相同 |
| 06 | 單筆篩選與評分 | 見「受驗職缺的挑選」 | 受驗職缺的逐條判定、彙總、四維分數、加權總分、reason 通過 oracle；Filter Agent 呼叫的職缺 ≤ 2 筆、Scorer 恰 1 筆 |
| 07 | 單筆求職信 | `POST .../letter` 並輪詢到 `letter_ready`／`letter_failed`；額度不足時見「求職信額度不足與補測模式」 | 要求之後的那次產製有成功的 drafter 呼叫；`approved` 者有成功的 reviewer 呼叫；非失敗者信件含兩個落款佔位；產製失敗且沒有任何成功的 drafter 呼叫時依 `failure_kind` 判定 |
| 08 | 已安裝排程定義 | Linux `systemd-analyze --user verify` 三個 unit；Windows 讀 `\jobfinder\` 的 api 與 run 工作 | Linux 三個 unit 通過驗證並指向安裝的 binary 與設定、timer 為每日 08:30 台北時間；Windows 兩個工作存在且 action 指向安裝的 `jobfinderw.exe` |
| 09 | 執行中的 loopback API | 帶與不帶 token 讀 `/jobs`、`/runs`；讀執行中 API process 的執行檔 | 帶 token 回 200 且含 `items`；未帶回 401；執行檔是安裝放置的 binary（Windows 為 `jobfinderw.exe`） |
| 10 | 復原 | 「情境安排與復原」反序 | 每項重讀後等於原值；`config.yaml` SHA-256 等於原檔；刪除復原紀錄 |
| 11 | evidence 安全性 | 掃描報告 | 不含 token、JD 內文、信件內容與落款佔位字面值 |

**成本上限以職缺計**：Filter Agent 至多兩筆職缺、Scorer 一筆、求職信產製一次（輪數依設定的 `llm.max_letter_rounds`）。步驟 06、07 各自收尾時，以 `GET /api/v1/status` 的 `agent_calls`（含 `id`、`job_id`、`role`）核對本趟基準之後的每一筆呼叫：篩選與評分只落在受驗職缺，drafter／reviewer 只落在求職信職缺、`letter-pending.json` 指向的職缺與基準當下已在 `letter_requested` 的職缺，其他職缺被呼叫即 `FAIL`。本趟呼叫多於該欄位的 20 筆視窗時同樣 `FAIL`。verification snapshot 的 `agent_calls` 是依角色彙總的計數，無法歸屬到單筆，只用於格式與指紋。

**判準以單筆與增量表達**，不對全庫的 score 或 letter 筆數下斷言。機器真相源是 `internal/liveverify` 的判準函式，直接讀 `store.VerificationSnapshot`：

| 判準 | 斷言 |
|---|---|
| schema | schema 版本、journal mode、foreign keys、必要資料表 |
| source | Yourator 職缺的 external ID 不重複、canonical HTTPS URL 在 `www.yourator.co`、標題／公司／地點非空、JD 長度 > 0、hash 格式、`remote_type` 列舉、薪資兩端同為 NULL 或 min ≤ max |
| fingerprint | 輸出 `筆數:sha256`，以 source、external ID、`content_hash`、`process_state` 組成 |
| screened | 該筆 `filter_outcome` 與逐條 `verdict` 在列舉內；評分終態者四維在合法區間、總分與 `reason_sha256` 存在 |
| lettered | 該次產製狀態為 `approved`／`finalized`／`failed` 之一；非失敗者該筆 letter 狀態與之相同且兩個落款佔位皆為真 |

### 判定與報告

報告、復原紀錄與求職信待補測紀錄落在安裝資料目錄下的 `verify/`（Linux `~/.local/share/jobfinder/verify/`、Windows `%LocalAppData%\jobfinder\data\verify\`），報告檔名 `<timestamp>-live.md`（補測模式 `<timestamp>-live-recheck.md`）。位置與執行時所在目錄無關，下一趟必定找得到上一趟的復原紀錄。真實職缺、Agent 原文與信件只留在測試環境的 SQLite，不進 evidence。

| exit code | 結果 |
|---|---|
| 0 | `PASS`：所有步驟通過且復原完成 |
| 1 | `FAIL`：產品行為不符判準 |
| 2 | `ENVIRONMENT_BLOCKED`：外部依賴不可用，或評分／求職信無從判定 |
| 3 | 復原未完成：壓過驗證結果，報告列出未復原的項目與手動復原指令 |

評分或求職信為 `ENVIRONMENT_BLOCKED` 時其餘步驟照常進行，全部通過者整趟結果為 `ENVIRONMENT_BLOCKED`。

部署包來自打包當下的 working tree，未提交的變更同樣驗得到。追溯以 `jobfinder version` 印出的 commit 與部署包的 `SHA256SUMS` 為準。

**驗收期間不要操作該測試後端**：在 Side Panel 切換自動處理或要求求職信，會被判為本趟以外的呼叫，或在復原時被覆寫回原值。強制終止（`kill -9`、關機、關閉主控台視窗）時復原不會執行，環境停在安排後的狀態，直到下一趟開跑時依復原紀錄還原。

Side Panel 的實機讀寫不在本節，屬 §6.1 人工組的 D4。

## 6.1 部署驗收

逐步操作見 [上手指南](guides/getting-started.md)；本節只定義判準。

D 系列分自動與人工兩組，分屬兩種環境：**自動組是開發階段驗收**，跑在隔離沙盒內；**人工組是測試環境的實機驗收**，安排見 [測試環境指南](guides/test-environment.md)。動到 `internal/paths`、`internal/install`、排程模板或 bootstrap 腳本時，自動組每次重跑，人工組重跑受影響的平台。

**自動組**由 `mise run e2e-deploy` 執行，落點全部在 `.local-dev/dev-verify/deploy/` 內。`internal/paths` 是路徑的唯一決策點，Linux 側的位置由 `$HOME` 與 `XDG_*` 推導、Windows 側由 `%LOCALAPPDATA%` 推導，把這些變數指向隔離根即得到一個無既有安裝的環境，而安裝走的仍是與真實安裝完全相同的程式碼路徑。

| 步驟 | 動作 | 標準答案（字面預期） |
|---|---|---|
| D1 全新安裝 | 在無既有安裝的環境執行 bootstrap 腳本（不帶旗標，即只裝後端；或解壓工件後跑 `jobfinder install`） | 印出的路徑與 `jobfinder paths` 一致；設定含隨機 token 且無 `CHANGE_ME`、無未替換的範例路徑佔位，且每個路徑都落在該次安裝的根之下；Linux 上設定為 `0600`；Windows 上 `bin\` 同時有 `jobfinder.exe` 與 `jobfinderw.exe`；生效面驗證全過 |
| D2 既有設定不覆寫 | 改動 `api.extension_origin` 後重跑安裝 | 設定內容逐字不變，安裝仍成功 |
| D3 排程實際觸發 | 手動觸發抓取工作（Linux `systemctl --user start jobfinder-run.service`；Windows `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'`） | 抓取實際執行並寫入 `runs`，該筆 `trigger` 為 `timer`；Linux 於 journald、Windows 於 `log.file` 看得到該趟記錄；Windows 上全程不出現主控台視窗 |
| D5 更新確實生效 | 對新版工件執行 `jobfinder update`，或在已有安裝的環境重跑 bootstrap 腳本（後端模式應自行改走 `update`） | 執行中 process 的執行檔為新 binary 且啟動時間晚於替換點；`jobfinder version` 為新版號；Windows 上兩支執行檔皆為新版 |
| D5A 更新不依賴服務當下是否在跑 | 停止 API 後對新版工件執行 `jobfinder update` | 服務被重新啟動並通過生效面驗證；同一份工件再跑一次 `update` 時 `jobfinder.prev` 仍為前一版 |
| D6 回滾 | `jobfinder rollback` | 執行中 process 為前一版；資料庫未被更動；`.bad` 保留了被回滾掉的版本；Windows 上兩支一起回到前一版，不出現版本不一致 |
| D6A 回滾不依賴服務當下是否在跑 | 停止 API 後執行 `jobfinder rollback` | 服務被啟動並通過生效面驗證，執行中 process 為前一版 |
| D6B 回滾只退一版 | 回滾後再執行一次 `jobfinder rollback` | 第二次被拒絕且不動任何檔案；`.bad` 仍是第一次回滾撤下來的版本 |
| D10 bootstrap 的 extension 模式（⏳ 未併入自動組） | 以 `--extension`／`-Extension` 執行 bootstrap 腳本，來源為 release 工件或 dev 部署包 | extension 解壓於 `<資料目錄>/jobfinder/extension/<版本字串>` 且含 `manifest.json`；Windows 上解出的檔案無 Mark of the Web；不建立任何服務、不寫入設定檔；印出的目錄可直接被 Chrome 載入。release 工件的 `manifest.json` `version` 與 `jobfinder version` 對得上且腳本印得出固定 ID；dev 部署包的 `key` 已移除，腳本改印出載入目錄 |

自動組以 `--skip-verify` 安裝，隨後自行啟動 `serve` 補上 API 生效面檢查（帶 token 回 200、未帶回 401），涵蓋 `internal/install/smoke.go` 中不依賴服務管理器的那一半。服務層改以 transient 單元驗證，不寫入正式 unit 目錄、不註冊正式排程工作。安裝器對 `systemctl` 的呼叫由 PATH 最前的 stub 吸收並記錄，D1、D5、D5A、D6 各自核對該趟有 `enable` 與 `restart` `jobfinder-run.timer`，且沒有對 `jobfinder-run.service` 下 `show` 以外的指令。每趟結束清除自己建立的 transient 單元與隔離根，中途失敗亦然。

D10 尚未併入自動組，以人工執行 bootstrap 腳本驗證：release 工件那條需要匿名下載 GitHub 工件，dev 部署包那條在測試環境以本地來源模式驗。

**人工組**需要真 Chrome 或真實作業系統環境，在測試環境進行，每個受支援平台各一輪。

| 步驟 | 動作 | 標準答案（字面預期） |
|---|---|---|
| D4 Side Panel 直連 | extension Options 填該測試後端的 loopback 位址與其設定中的 token，開啟 Side Panel | 後端與瀏覽器同機時無任何通道即可讀寫；未帶 token 的請求回 401 |
| D7 PATH 與診斷（Windows） | 開新終端執行 `jobfinder paths` | 不需完整路徑即可執行；印出 `%LocalAppData%\jobfinder\` 下的位置，含 `jobfinderw.exe` 那列 |
| D8 Agent CLI 可執行（Windows） | 讓一筆職缺實際走到評分 | 整段過程不彈出主控台視窗。npm 安裝的 `claude`／`codex` 能被服務叫起，由 Windows 的 live 驗收（§6 步驟 06、07）覆蓋，人工只觀察視窗 |
| D9 服務重啟不卡死（Windows） | 停止 api 工作，等 process 消失，再啟動 | 工作回到 `Running` 且 API 有回應。停止是直接終止行程，殘留的 worker 鎖檔不得阻擋下一次啟動 |
| D11 重新部署重新武裝排程且不觸發抓取 | 停用每日抓取（Linux `systemctl --user disable --now jobfinder-run.timer`；Windows `Disable-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'`）後重跑 bootstrap 腳本（走 `update`）；再停用一次後執行 `jobfinder rollback` | 兩次都印出 `fetch is armed` 並通過完整生效面驗證；Linux timer 為 `enabled` 且 `active`、有下一次觸發時間，Windows 工作為 `Ready` 且有 `NextRunTime`；前後 `runs` 筆數不變，Linux `jobfinder-run.service` 的 `ExecMainStartTimestamp` 不變，Windows 抓取工作的 `LastRunTime` 不變 |

人工組不得使用 `--skip-verify`：未經完整生效面驗證的安裝不算通過。

自動組不涵蓋兩件事，兩者都由單元測試補上。systemd unit 寫進 manager 搜尋路徑再 `enable --now` 這一串，受限於 manager 的搜尋路徑在啟動時就固定，同一個登入 session 內無法改指向隔離根；Windows 的 Task Scheduler 工作資料夾 `\jobfinder\` 是常數，不隨 `%LOCALAPPDATA%` 移動。掛載邏輯由 `internal/install/sequence_test.go` 的可注入 scheduler 守；各平台發出的排程指令由 `internal/install/scheduler_commands_test.go` 攔截比對，守住三條路徑都重新武裝排程、且沒有任何一條發出啟動抓取的指令。真實掛載留在人工組 D11。

## 7. 答案卷：報告如何對答案

`mise run e2e-mock` 每趟逐案例即時 append 到 `evidence/<timestamp>-mock.md`（`tail -f` 友善、中途崩潰留部分結果）；V3 live 另出 live 報告，落在測試環境安裝資料目錄的 `verify/<timestamp>-live.md`。答案卷每條 4 欄：**案例 ID（對齊 §4）｜驗證項目｜實下的指令/動作｜判定＋觀察到的字面值｜變更/印記**，收尾補 **PASS/FAIL/SKIP 計數＋需求覆蓋 tally＋使用者故事重建**。

**人工驗收步驟**：
1. **題目完整嗎**：§2 覆蓋度地圖的每項需求都有對應可執行案例，答案卷收尾 tally 顯示這趟驗到的需求集合＝預期。
2. **標準答案對嗎**：§3 的測資與 §4 的字面預期是否符合需求與你預期的成效（例如「intern 就該被篩掉」「未要求不生成信」）。
3. **實際作答＝標準答案嗎**：逐條把答案卷觀察值對 §4 該案例的字面預期（案例 ID 對齊）；答案卷不重嵌標準答案，對照以本檔為準（避免第三份真相源）。

## 8. Evidence、資料保護與判定

| 項目 | 規則 |
|---|---|
| evidence | 每案例在同一份答案卷記錄 artifact revision/checksum、觀察值、狀態轉換、Run 摘要及 browser trace／截圖索引。 |
| 禁止記錄 | Profile request／response body與 YAML、職缺全文、求職信、token、Agent 原始輸入輸出、日常 SQLite 資料。 |
| mock fixture | 合成 Profile／Job／Agent 回覆；不將真實職缺或個資加入 repo、fixture 或 evidence。 |
| live 資料 | 真職缺只留測試環境的 SQLite；evidence 僅存筆數、hash、狀態與格式摘要。 |
| `PASS` | 案例所有標準答案成立且答案卷可重建該判定。 |
| `ENVIRONMENT_BLOCKED` | 外部來源、已授權 CLI Agent、browser 相依或 systemd user 環境不可用，未判定產品行為。 |
| `FAIL` | artifact、流程、狀態、資料保護或可觀察結果不符標準答案。 |

## 9. 後續累加順序

1. Linux 與 Windows 測試環境各跑通一次 **V3**：真來源至少一筆、真 Agent 的篩選評分與求職信、復原完成與安全格式 evidence 缺一不可。
2. 依 §5 骨架累加負向案例 N，沿用相同需求對照與 evidence 格式。

**人工 gate 是常態流程，不是待辦**：凡動到 extension、Side Panel、Profile editor 或任一 content script 的改動，交付前由驗收者把同一份 artifact 載入實機 Chrome 走一次（步驟見 `docs/guides/getting-started.md` §5），涵蓋 §4 標為 👤 的步驟與該次改動觸及的頁面。自動隔離 Chromium 不得替代人工結論；結論當場即知，不回寫本檔。
