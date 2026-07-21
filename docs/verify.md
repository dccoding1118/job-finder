# 驗收測試 — job-finder

> **單一累加式題目卷**：每個 V/N 案例＝**題目**（驅動動作）＋**標準答案**（字面可比對的預期）＋**具體測資**；模組完成即補上可執行案例，全案例最終串成使用者的完整日常求職迴圈。
> **題目卷 ↔ 答案卷**：本檔是題目卷；`mise run e2e-mock` 每趟產生 `evidence/<timestamp>-mock.md` 答案卷（逐案例的實際觀察值與判定，案例 ID 與本檔 §4 對齊）。**人工驗收＝拿答案卷逐案例對本檔標準答案**（§7）。
> **只驗真程式真的跑得出來的部分**。mock fixture（合成 Yourator／104 頁、fake CLI Agent、隔離 Chromium）**允許但明標「模擬，不等於真來源、真 CLI Agent 或實際 Chrome 已驗收」**；真依賴另走 V3 live（§6）與人工 Chrome gate（§9）。
> **三態判定**：`PASS` ／ `ENVIRONMENT_BLOCKED`（exit 2，外部依賴不可用，未判定產品）／ `FAIL`（exit 1，產品行為不符），不得以「安全完成」當 PASS。
> 標準答案的機器真相源是 `scripts/verify/oracle/assert-positive.mjs`；本檔為人重述並指向它，兩者不得分歧。對應 `docs/design.md` §8 測試策略。最後更新：2026-07-20。

負向案例（N）目前僅保留骨架（§5），待正向流程穩定後，以相同的需求對照與 evidence 格式累加；不阻礙目前 V 的交付。

## 1. 怎麼跑起來

在專案根目錄執行：

```bash
mise run e2e-mock   # 物化隔離 artifact → 依序跑 V1/V2/V4/V5 共 25 步 → 產生答案卷
```

- **沙盒**：`.local-dev/verify/`（gitignored 隔離根，0700，不碰日常 Profile/設定/SQLite）。
  `artifact/` 承載 mock/live 共用 product binary、extension 與 production unit templates；
  `harness/` 只承載 mock fixture/fake Agent；`runtime/` 產生 config、SQLite、browser profile 與 rendered units；
  `evidence/` 保留歷次答案卷。腳本位於 `scripts/verify/`（runbook 於根層、browser E2E 於 `browser/`、Playwright 設定 `playwright.config.js`）。
- **跑完要檢查哪些產物**（人工照著看一遍）：

  | 產物 | 位置 | 看什麼 |
  |---|---|---|
  | 答案卷 | `.local-dev/verify/evidence/<最新>-mock.md` | 逐案例觀察值＋PASS/FAIL；收尾 tally 與使用者故事重建 |
  | SQLite snapshot | `<binary> verify snapshot --db .local-dev/verify/runtime/mock.db` | 4 筆 Job 的終態、五維分數、letter 輪次、狀態事件 |
  | browser evidence | `evidence/extension-browser.json`、`evidence/extension-dashboard.png` | extension 模擬互動的安全摘要與截圖 |

- **跑到哪停**：mock 全 25 步現可跑（✅）。V3 live 需真 Yourator＋已授權 `claude`/`codex` CLI，另跑 `mise run e2e-live`（⏳，§6）。實際 Chrome 安裝與相容性一律人工 gate（§9）。

## 2. 覆蓋度地圖（需求 ←→ 案例）

| 需求 | 成品流程中的驗證 | 正向案例 | 負向案例 | 現況 |
|---|---|---|---|---|
| R1 匿名 Profile、PII 防線、僅產校準建議 | V1、V6 | S1–S3 | N-P | ◑ |
| R2 Yourator／Cake／104 進同一 Job 流程 | V2、V5、V6 | S4、S7、S24–S25 | N-SRC | ◑ |
| R3 Profile 條件篩選與可稽核狀態 | V2、V5 | S8、S24 | N-FLT | ◑ |
| R4 CLI Runner 評分、五維分流、設定路由 | V2、V3 | S9 | — | ◑ |
| R5 要求後才生成的 Drafter–Reviewer 與程式防線 | V2、V3 | S5、S10 | N-LTR | ◑ |
| R6 extension 清單/判定/對照/生成/複製/投遞/手動/Run | V3、V4 | S17–S23 | — | ◑ |
| R7 one-shot／timer／手動／冪等／執行上限 | V2、V3、V4 | S11、S14、S16 | — | ◑ |
| R8 Run 摘要、Agent 稽核、安全 evidence | V1–V6 | S12 | — | ◑ |
| R9 104 列表快速判定、內頁完整評估、待看清單 | V5 | S24–S25 | — | ◑ |

- **✅ 可執行**：已有驗收入口且本次 artifact 可驗證完整案例。**◑ 部分可驗**：已有可執行子流程，未覆蓋需求完整結果。**⏳ 待實作**：規格已保留、產品或 verifier 尚未交付。
- V1、V2、V4、V5 共用同一份 mock 答案卷（§4）；V3 走 live（§6）；V6 待 Cake adapter 與校準交付。

## 3. 測資與標準答案（fixture answer key）

mock 一趟用固定合成測資；下表即「標準答案」，逐值由 `assert-positive.mjs` 機器斷言。V 案例（§4）的每一步都對照這裡的具體值。

### 3.1 使用者故事（mock 一趟走完的劇本）

> 載入 **4 筆 Yourator 職缺** → `#1000`「intern」命中排除關鍵字，**當場篩掉**（unfit / filtered_out）→ 其餘 3 筆評分：`#1003` 得 **60**（not_recommended）、`#1001` 得 **80**、`#1002` 得 **90**（後兩者達閾值停在 shortlisted，**未經要求不生成求職信**）→ 使用者對 2 筆 shortlisted **要求生成求職信** → `#1002` 首輪即**核准**（approved / round 1）、`#1001` 三輪仍**退回**（failed / round 3）→ 使用者在 dashboard **複製** `#1002` 的信、標記**已投遞**（applied）→ 另在 **104 搜尋頁**載入 2 筆：intern 當場篩掉、senior 列入**待看清單** → 點開 senior **內頁**補全文、過篩、排進評分**佇列**（queued，待常駐 worker 評分）。

### 3.2 Yourator 四筆測資（標準答案）

| ext_id | 標題 | 公司 | 薪資 | remote | 篩選 | 五維／total | reason | process 終態 | letter | API verdict／letter_state |
|---|---|---|---|---|---|---|---|---|---|---|
| 1000 | Verification intern platform engineer | Example Learning | 60000–70000 | onsite | 命中 `exclude_title_keywords` | —（null） | — | `filtered_out` | null | `unfit`／— |
| 1001 | Verification failure remote platform engineer | Example Platform | 100000–120000 | remote | pass | 80 | 合成重試情境 | `letter_failed` | failed／rounds=3 | `recommended`／`failed` |
| 1002 | Verification ready hybrid backend engineer | Example Services | 110000–130000 | hybrid | pass | 90/90/90/90/90 → 90 | 合成核准情境 | `letter_ready` | approved／rounds=1／apply pending→applied | `recommended`／`ready` |
| 1003 | Verification low score cloud engineer | Example Operations | 90000–100000 | onsite | pass | 60/60/60/60/60 → 60 | 合成低分情境 | `scored` | null | `not_recommended`／— |

- 共同欄位：`source=yourator`、`url=http://127.0.0.1:18787/jobs/<id>`、`location=Taipei`、`company_info=public listing`、`content_hash=sha256(標題\n描述\n薪資min\n薪資max\nTaipei\nremote)`。
- **Agent 呼叫累計**：評分階段 `scorer=3`；要求生成後 `drafter=4`、`reviewer=4`（信件 runner：draft=`claude`、review=`codex`；scorer runner=`claude`）。冪等重跑與列表路徑**零新增**。
- **letter placeholder**：核准信含 `[你的姓名]`、`[你的聯絡方式]` 兩個 placeholder（程式防線，不外洩 PII）。

### 3.3 104 兩筆測資（V5）

| item | 標題 | 列表判定 | 內頁 capture 後 |
|---|---|---|---|
| v5intern | backend intern engineer | 命中 exclude → `unfit`／`filtered_out` | — |
| v5senior | Senior backend engineer | `discovered`／`pending_detail`，進待看清單 | 過篩 → `queued`／`pending_score`（worker 另外評分） |

### 3.4 Profile 與來源請求測資

- Profile（匿名）：`years_of_experience=8`、`education=master (computer science)`、`expert=[Java]`、`proficient=[Go]`、`directions=[P1:cloud architecture, P2:backend engineering, P3:platform reliability]`。
- 來源請求序：先 `GET /robots.txt` → 3 個方向各一次 `GET /api/v4/jobs`（`term[]`＝`{cloud,platform}`／`{backend,Go}`／`{Kubernetes,reliability}`，page=1）→ 4 個唯一 `GET /jobs/1000..1003`。跨 query 重複項不重抓。

## 4. 正向案例 V（mock，25 步原子案例）

`mise run e2e-mock` 依序執行下列 25 步，共用同一 artifact、SQLite 與答案卷；任一步失敗即停止並保留隔離目錄。每步一個原子觀察，標準答案為字面值（詳細測資見 §3，機器斷言見 `assert-positive.mjs`）。

| 步 | 動作 | 標準答案（字面預期） | 案例·需求 | 狀態 |
|---|---|---|---|---|
| S1 | 物化隔離 artifact | binary_sha256 == manifest；extension、rendered API/run/timer unit 存在 | V1·R8 | ✅ |
| S2 | 驗證隔離權限 | 驗收 root=0700；Profile／denylist／config=0600 | V1·R1 | ✅ |
| S3 | 驗證匿名 Profile 與 schema | Profile summary＝§3.4；schema_version=2、WAL、foreign_keys、6 張表 | V1·R1 | ✅ |
| S4 | 啟動 Yourator fixture | `GET /healthz`→200 由本 harness 綁定；external_id 1000–1003 均合成 | V2·R2 | ✅ |
| S5 | 抓取後手動 filter/score（letter 零取件） | `run`＝`fetched:4/new:4`；`--stage filter`＝`filtered:1`；`--stage score`＝`scored:3`；letter 未要求不取件、不生成 | V2·R5/R7 | ✅ |
| S6 | 驗證來源搜尋請求 | request journal＝§3.4（robots→3 query→4 detail），共 8 筆 | V2·R2 | ✅ |
| S7 | 驗證來源欄位正規化 | 4 筆逐欄＝§3.2 共同欄位＋各自 title/company/salary/remote/content_hash | V2·R2 | ✅ |
| S8 | 驗證條件篩選 | `#1000` filter_hits=`[exclude_title_keywords]`→`filtered_out`；其餘進評分 | V2·R3 | ✅ |
| S9 | 驗證評分五維與分流，且 letter 零 Agent | `#1002`=90×5、`#1001`=80、`#1003`=60×5，reason 各＝§3.2；`#1002/#1001` shortlisted、`#1003` scored；letter=null、drafter/reviewer 呼叫=0；scorer=3 | V2·R4 | ✅ |
| S10 | 要求生成後驗證信件與 Agent 稽核 | 對 2 筆 shortlisted `letter request`→`requested:<id>`；`--stage letter`＝`lettered:2`；`#1002` approved/rounds=1/apply=pending、`#1001` failed/rounds=3；轉換含 `shortlisted→letter_requested`；calls scorer=3/drafter=4/reviewer=4；runner=checked-in fake | V2·R5 | ✅ |
| S11 | 重跑抓取與階段冪等 | 重跑 `fetched:4/new:0`；filter=0、score=0；Job=4、Score=3、Letter=2、Agent calls=11 均未增 | V2·R7 | ✅ |
| S12 | 安全 SQLite snapshot 與 Run stats | snapshot 只含契約欄位與 hash；`runs.Stats`＝`{fetched:4,new:4,queries:3,errors:0}`，無 filter/score/letter 統計 | V1/V2·R8 | ✅ |
| S13 | 驗證 rendered systemd units | API/run ExecStart、PATH、DB 路徑正確；timer `OnCalendar=*-*-* 08:30:00 Asia/Taipei`；通過 `systemd-analyze` | V4·R7 | ✅ |
| S14 | transient one-shot service | `systemd-run --user --wait --pipe` 於 rendered PATH 執行 binary，完成一個無外部成本 stage | V4·R7 | ✅ |
| S15 | transient API service（含常駐 worker） | user manager 啟動同一 binary/SQLite 的 API service；loopback health 可達、is-active=active | V4·R6/R7 | ✅ |
| S16 | transient timer（fetch-only） | timer 以 `trigger=timer` 執行；service Result=success、ExecMainStatus=0；snapshot 出現 `Trigger:timer` | V4·R7 | ✅ |
| S17 | localhost API 正向認證 | 精確 extension Origin＋token=200；無 Origin MV3＋token=200；preflight=204 | V4·R6 | ✅ |
| S18 | API 清單、篩選、判定與對照 | 4 筆 verdict/letter_state＝§3.2；source/process/apply/verdict filter 精確；queue 空；`#1002/#1001` 詳情五維、letter、狀態事件精確 | V2/V4·R6 | ✅ |
| S19 | 載入固定 ID extension 模擬環境 | 固定 unpacked ID `oddnhajj…`；安全摘要 Origin=absent、Authorization present=true、token 已清空 | V4·R6 | ✅ |
| S20 | dashboard 判定篩選、copy、apply | filters_verified、detail_verified、clipboard==核准合成信、`#1002` apply pending→applied 並寫回 SQLite | V4·R6 | ✅ |
| S21 | dashboard 求職信生成入口 | 對 `#1001`（letter_failed）再次產生→受理轉 `letter_requested`；狀態事件永久記錄第 2 次 `letter_requested`，worker 隨後取件 | V4·R6 | ✅ |
| S22 | dashboard 手動抓取與 Run history | manual fetch 完成；API `runs` 出現 `trigger=manual-extension`；Run history 呈現 fetch stats 與 verdict 分布；存 screenshot | V4·R7 | ✅ |
| S23 | extension mock browser L1 | 專案鎖定 Playwright 6 tests 全 pass：dashboard／Options／service worker 的 mock Chrome API 互動，及 content script 於 104 search／notification／detail fixture 上的標記與 sidebar（搜尋頁 `.jobfinder-mark` 依 verdict 標記、跳過 hotjob 廣告、title／data-gtm 地區薪資照 live selector 讀取；通知頁無 data-gtm 依位置與格式讀取；內頁 sidebar 由 JobPosting JSON-LD 顯示 verdict 與五維） | V4·R6／R9 | ✅ |
| S24 | 104 清單就地判定且列表路徑零 Agent | v5intern→`unfit/filtered_out`、v5senior→`discovered/pending_detail`；列表路徑 Agent 呼叫數不變 | V5·R2/R3/R9 | ✅ |
| S25 | 104 既有職缺回判定、內頁 capture 非同步 | 重複 list 對既有職缺 `created=false` 回現行判定；v5senior 進待看 queue；`capture/job`→`queued/pending_score`、score=null | V5·R9 | ✅ |

## 5. 負向案例 N（骨架，待正向穩定後累加）

與 V 相同的原子表；每列預期 `ENVIRONMENT_BLOCKED`／`FAIL`／產品拒絕。目前保留骨架與意圖，尚未實作（⏳）；各模組 L1 錯誤案例仍由 `docs/tests/test-<module>.md` 維護。

| 群 | 意圖 | 標準答案（字面預期） | 需求 | 狀態 |
|---|---|---|---|---|
| N-SRC | 來源不可達 vs 零筆／格式錯 | 來源不可達→`ENVIRONMENT_BLOCKED`(2)；可連但零筆或欄位缺失→`FAIL`(1) | R2 | ⏳ |
| N-FLT | denylist／排除關鍵字邊界 | 命中 denylist 的 Profile 輸入被拒且不落庫；排除命中一律 `filtered_out` | R3 | ⏳ |
| N-LTR | 未要求即生成、輪數上限 | 未 `letter request` 前驅動 letter 階段→零取件、零 Agent；超出 review 上限→`failed` 終態 | R5 | ⏳ |
| N-P | PII 防線 | 求職信含實體姓名/聯絡方式而非 placeholder→`FAIL`；evidence 誤含禁記欄位→`FAIL` | R1/R8 | ⏳ |

## 6. 現行 live 案例：V3（真來源與已授權 CLI Agent）

在已安裝且授權 `claude`、`codex` CLI，並允許連線正式 Yourator 的環境執行：

```bash
mise run e2e-live
```

直接 reset/deploy 共用 artifact，從空的 live SQLite 執行下列階段，不會先跑 mock，也不讀 `harness/`、loopback `base_url`、fake Agent result 或 mock SQLite。Agent 成本上限為 score 一筆、letter 一筆；真實職缺、Agent 原文與信件只留 gitignored live SQLite，不進 evidence。

| 階段 | 動作 | 標準答案（字面預期） | 狀態 |
|---|---|---|---|
| 共用 artifact 與 live runtime | 物化一次 binary/extension/units，產生 live config、SQLite、evidence | mock/live 同一 binary checksum；`base_url` 為 `https://www.yourator.co`；live SQLite 與 mock/日常分離；PATH 不含 harness | ⏳ |
| 外部能力 preflight | 檢查來源工具、user systemd、browser runtime、設定路由 CLI；資料 request 前先查 robots.txt | 未授權／網路／來源不可達→`ENVIRONMENT_BLOCKED`；不得退回 fixture 或 fake Runner | ⏳ |
| 真來源 fetch | 每方向 keywords 組一個正式 query（每來源最多三組），結果進同一池 | 至少一筆真 Job；跨 query/page 依 external ID 去重；**零筆＝FAIL**；evidence 只記來源、筆數、external ID hash 與 request/format 摘要 | ⏳ |
| 真資料格式 | 讀 live SQLite 安全 snapshot | external ID、canonical HTTPS URL、標題、公司、非空 JD、地點、remote enum、content hash 正確；salary 可 NULL，非 NULL 時 min≤max | ⏳ |
| 真 Agent score／letter | 對一筆過 filter 的 Job 評分；再對一筆推薦職缺**明確要求後**生成 | 五維、加權總分、reason、runner audit、信件終態合法；要求前零 Drafter/Reviewer 呼叫；score/letter 各最多一筆；executable 非 repo 內 fake | ⏳ |
| 冪等與 Run | 再執行相同 live query | 同 source/external ID 不新增重複 Job；Run stats、Agent 上限、錯誤摘要正確 | ⏳ |
| systemd／API／extension 模擬 | live config 啟 transient systemd 與 localhost API，隔離 Chromium 操作 extension | 讀同一 live SQLite；browser verifier 依實際 Job 狀態選資料、不依賴 mock 合成標題或固定 ID；仍屬自動模擬，非實際 Chrome gate | ⏳ |

開發中未提交變更可直接驗收。artifact manifest 以 binary/extension/config/unit checksum 為主要追溯；Git revision 與 dirty 狀態只作輔助，不構成執行閘門。

## 7. 答案卷：報告如何對答案

`mise run e2e-mock` 每趟逐案例即時 append 到 `evidence/<timestamp>-mock.md`（`tail -f` 友善、中途崩潰留部分結果）；V3 live 另出 live 報告。答案卷每條 4 欄：**案例 ID（對齊 §4）｜驗證項目｜實下的指令/動作｜判定＋觀察到的字面值｜變更/印記**，收尾補 **PASS/FAIL/SKIP 計數＋需求覆蓋 tally＋使用者故事重建**。

**人工驗收步驟**：
1. **題目完整嗎**：§2 覆蓋度地圖的每項需求都有對應可執行案例，答案卷收尾 tally 顯示這趟驗到的需求集合＝預期。
2. **標準答案對嗎**：§3 的測資與 §4 的字面預期是否符合需求與你預期的成效（例如「intern 就該被篩掉」「未要求不生成信」）。
3. **實際作答＝標準答案嗎**：逐條把答案卷觀察值對 §4 該案例的字面預期（案例 ID 對齊）；答案卷不重嵌標準答案，對照以本檔為準（避免第三份真相源）。

## 8. Evidence、資料保護與判定

| 項目 | 規則 |
|---|---|
| evidence | 每案例在同一份答案卷記錄 artifact revision/checksum、觀察值、狀態轉換、Run 摘要及 browser trace／截圖索引。 |
| 禁止記錄 | Profile、職缺全文、求職信、token、Agent 原始輸入輸出、日常 SQLite 資料。 |
| mock fixture | 合成 Profile／Job／Agent 回覆；不將真實職缺或個資加入 repo、fixture 或 evidence。 |
| live 資料 | 真職缺只留 gitignored live SQLite；evidence 僅存筆數、hash、狀態與格式摘要。 |
| `PASS` | 案例所有標準答案成立且答案卷可重建該判定。 |
| `ENVIRONMENT_BLOCKED` | 外部來源、已授權 CLI Agent、browser 相依或 systemd user 環境不可用，未判定產品行為。 |
| `FAIL` | artifact、流程、狀態、資料保護或可觀察結果不符標準答案。 |

## 9. 後續累加順序

1. 在具備正式來源連線與已授權 CLI 的環境跑通 **V3**：真來源至少一筆、真 Agent score／letter 與安全格式 evidence 缺一不可。
2. 完成實際 **Chrome compatibility gate**；自動隔離 Chromium 不得替代人工結論。人工操作步驟見 `docs/guides/runbook-extension.md`。
3. 完成 **V5** 的真 104 頁人工 Chrome gate；真頁面只驗證使用者已載入的內容，確認清單就地標記與既有職缺判定一致。
4. 完成 **V6** 的 Cake adapter 與校準 diff，再將 V1–V6 彙整為完整日常求職迴圈。
5. 依 §5 骨架累加負向案例 N，沿用相同需求對照與 evidence 格式。
