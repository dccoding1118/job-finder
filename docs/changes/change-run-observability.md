# change — 執行中可觀測性與批次歷程用語

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

系統的每一個進度訊號都在一件工作**結束後**才寫入：職缺狀態在階段處理完才轉移，`agent_calls` 帶著 `duration_ms` 表示它是呼叫回來後才落帳，`runs` 只在 `FinishRun` 寫一次統計。單一 Agent 呼叫要跑數十秒到數分鐘，一趟抓取要跑十分鐘以上，在那段時間裡沒有任何一筆資料改變，因此正在工作的系統與已經死掉的程序在畫面上完全相同。

各階段的具體缺口：

- **抓取全程零紀錄**。`internal/crawler` 一行日誌都沒有，`pipeline.Fetch` 也是等來源回傳整份切片才開始 `UpsertJob`。中途被中止則整趟作廢。
- **執行中的批次無法辨識**。`runs` 只有 `started_at` 與 `finished_at`，執行中與「程序已死但沒收尾」都表現為 `finished_at` 為空、統計全零。
- **Agent 呼叫在進行時不存在**。稽核列只在呼叫結束後寫入，呼叫進行中沒有任何一筆可讀的紀錄。
- **批次歷程以英文鍵值呈現**。`trigger`、`stats` 的鍵、判定分布的鍵都直接印原始字串，與 UI 其他地方對同一概念的中文用語不一致。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | `runs` 增設 `heartbeat_at`：`StartRun` 寫入、每個批次落地時更新、`FinishRun` 一併寫入。它是「執行中」與「已中斷」的唯一判準。 |
| D2 | Source 契約改為串流：`Fetch(ctx, spec, emit func(Batch) error) error`。來源邊解析邊交付，呼叫端邊收邊寫。 |
| D3 | Yourator 逐筆職缺交付一個 `Batch`，每頁結束另交付一個不帶職缺的 `Batch`。空批次讓整頁都是重複刊登時仍能回報進度。 |
| D4 | `pipeline.Fetch` 新增 `Progress` callback，在每個批次落地後帶著累計數呼叫。抓取跑在自己的程序，回報只能經資料庫。 |
| D5 | Agent 支撐的工作以行程內的 `Activity` 記錄，`InFlight()` 經 `/api/v1/status` 揭露。不落 DB：進行中只對執行中的程序為真，重啟即失效。 |
| D6 | 執行狀態由 API 導出四態 `running`／`stalled`／`done`／`failed`，不存 DB 欄位。心跳沉默超過五分鐘判 `stalled`。 |
| D7 | 批次歷程的 `trigger`、統計鍵、判定鍵一律在 extension 端映射為中文；API 維持英文鍵。 |
| D8 | 系統頁新增「進行中」區塊；該頁開啟且確實有工作在跑時，每 5 秒更新 `/api/v1/status` 與 `/api/v1/runs`。 |

五分鐘門檻的依據：單次來源請求最差是設定延遲 ＋ 30 秒逾時 ＋ 兩次重試，約一分半，門檻須明顯高於它，否則健康的抓取會被判為中斷。

抓取與 Agent 兩者的回報管道刻意不同。抓取由排程或 API 觸發**另一個程序**執行，服務程序的記憶體看不到它，只能經 `runs` 的心跳；Agent 呼叫跑在服務程序自己的 worker 裡，落 DB 反而會留下重啟後永遠清不掉的假進行中。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| Source 契約 | 整批回傳 `([]RawJob, error)` | 串流交付 `emit(Batch)`，回傳 `error` |
| 抓取寫入時機 | 全部抓完才逐筆寫 | 每筆解析完即寫 |
| 抓取日誌 | 無 | 每個查詢、每頁各一行 Info；每筆職缺一行 Debug |
| `runs` 欄位 | `started_at`／`finished_at`／`trigger`／`stats`／`error` | 加 `heartbeat_at` |
| 執行中判定 | 無 | API 導出 `state` 四態 |
| 進行中的 Agent 工作 | 無從得知 | `/api/v1/status` 的 `in_flight`，含已耗時 |
| 批次歷程用語 | `manual-extension`、`fetched=4`、`recommended=2` | 手動（側邊欄）、抓取 4、推薦 2 |
| 系統頁更新 | 只在手動重新整理時 | 有工作在跑時每 5 秒 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/designs/design-schema.md` | §2 | `runs` 增設 `heartbeat_at`；schema 版本 9 |
| | §4 | migration 8 |
| `docs/designs/design-crawler.md` | §2 | Source 契約改為串流交付，`Batch` 的欄位與空批次語意 |
| | §3 | Yourator 的日誌顆粒度 |
| `docs/designs/design-pipeline.md` | §3 | `Fetch` 邊抓邊寫與 `Progress` 契約 |
| | §4 | `Activity` 的記錄範圍與不落 DB 的理由 |
| `docs/designs/design-api.md` | Run viewmodel | `heartbeat_at` 與導出的 `state` 四態 |
| | `/api/v1/status` | `in_flight` 欄位 |
| `docs/designs/design-extension.md` | 系統頁 | 進行中區塊、批次歷程的狀態與中文用語、活動輪詢 |
| `docs/tests/test-schema.md` | runs | 心跳寫入與已完成批次不被改寫 |
| `docs/tests/test-crawler.md` | Yourator | 串流交付與空批次 |
| `docs/tests/test-pipeline.md` | fetch | 邊抓邊寫、`Progress` 失敗即終止；`Activity` 的進出與 nil 安全 |
| `docs/tests/test-api.md` | runs／status | `state` 四態、`in_flight` 的已耗時 |
| `docs/tests/test-extension.md` | 系統頁 | 中文用語與進行中區塊 |

## 5. 待實作進度

- [x] `internal/store`：`heartbeat_at`、migration 8、`TouchRun`、`ListRuns`
- [x] `internal/crawler`：串流契約、Yourator 日誌
- [x] `internal/pipeline`：`Fetch` 邊抓邊寫、`Progress`、`Activity` 與三個階段的掛點
- [x] `internal/api`：`state` 導出、`in_flight`
- [x] `extension`：進行中區塊、中文用語、活動輪詢
- [x] 測試依 §4 的五份 test 文件補齊
- [x] canonical 文件依 §4 就地更新

## 6. 已知殘留限制

- 本次變更之前寫入的 `runs` 沒有心跳。未收尾的舊列一律呈現為已中斷，不會被誤判為執行中。
- `in_flight` 只涵蓋服務程序自己在跑的工作。`run --stage` 由另一個程序手動驅動的階段不在其中，它的進度只在日誌裡。
- 心跳的顆粒度是「一筆職缺」。單筆職缺的內頁請求若連續逾時重試，心跳最長可靜默約一分半，仍在五分鐘門檻內。
- 抓取的失敗仍然是整趟終止：串流只保住了失敗前已寫入的職缺，不改變錯誤處理語意。
