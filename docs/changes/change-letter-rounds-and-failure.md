# change — 求職信輪次語意、累積重寫與失敗終止

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

求職信階段在實機上一律失敗，且失敗的職缺無限重試，吃光每日額度。追查後發現四個彼此獨立的問題：

- **Reviewer 的輸出契約沒有傳達給模型**。`reviewPrompt` 只要求「回傳 verdict、issues」，未規定 `issues` 的元素型別；模型回物件陣列，Go 端 `[]string` 解析失敗。三個 runner 全敗，整筆作廢。三個 prompt 中只有它沒有輸出規格。
- **呼叫失敗的職缺卡在隊首**。pipeline 對 `GenerateLetter` 回錯的分支只記 log，不寫 `letters`、不轉狀態、不動 `updated_at`；`PickForStage` 以 `updated_at, id` 排序，該筆永遠最先被取到，其他 `letter_requested` 永遠輪不到。每次重試付掉一格每日額度，跨日重置後再付一輪。
- **多輪重寫沒有累積效果**。`draftPrompt` 只收上一輪的 `issues`，看不到被批評的那份草稿，且 `issues` 每輪整個覆蓋。drafter 等於每輪從零重寫，第一輪指出的問題到第三輪已無人記得。
- **輪數上限寫死**，且第三輪 review 若回 `revise`，該輪意見無人接收，三輪產出全部丟棄，改寫入一封只有落款佔位符的空信。

另有一個放大上述損耗的 prompt 缺陷：Reviewer 會把 `[你的姓名]`、`[你的聯絡方式]` 當成待填欄位而要求填入真實個資。那是刻意保留的成品形態，drafter 若照辦會被 guard 的佔位符檢查與 PII lint 擋下，整輪報銷。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 輪數上限改為可配置 `llm.max_letter_rounds`（預設 3）。語意為**最多產出第幾版**：前 N-1 輪各跑一次 draft ＋ 一次 review，第 N 輪只 draft，該版即最終稿。 |
| D2 | drafter 每輪收到**到目前為止的全部歷程**：各版草稿與其對應的 review 意見，依序排列。 |
| D3 | 任一輪的 draft 或 review 在 Primary、Primary、Fallback 三個 runner 後仍失敗，該筆立即 `letter_failed`，不重試、不跑後續輪次。 |
| D4 | `letters.status` 增設 `finalized`：跑滿輪數產出的最終版，未經最後一次審查。`process_state` 仍為 `letter_ready`。 |
| D5 | 失敗一律寫入 `letters` 列，`content` 為空字串，`review_log` 記各輪意見與失敗原因；不再以落款佔位符填充成一封假信。 |
| D6 | `reviewPrompt` 補上輸出規格（`issues` 為字串陣列）與佔位符語意（刻意保留的最終形態，要求填入真實個資屬錯誤意見）。 |
| D7 | `parseReview` 對 `issues` 放寬解析容忍度：元素為字串照收，為物件則取其文字欄位攤平為一句。契約不變，只是不因模型單一欄位失守而作廢整輪。 |

guard 失敗不屬 D3。guard 是呼叫成功後的程式檢查，失敗表示這一版不合格而非拿不到產出，因此維持既有規則：中間輪次視同 `revise`，錯誤內容當作 issues 進下一輪；最後一輪失敗則無下一輪可修，該筆 `letter_failed`。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| 輪數 | 程式寫死 3 | `llm.max_letter_rounds`，預設 3 |
| 第 N 輪 | draft ＋ review，`revise` 即整筆失敗 | 只 draft，產出即最終稿 |
| drafter 輸入 | Profile ＋ Job ＋ 上一輪 issues | 加上各版草稿與各版意見的完整歷程 |
| 呼叫失敗 | 停留 `letter_requested`，worker 下次重試 | 立即 `letter_failed`，等使用者再次要求 |
| `letters.status` | `approved` / `failed` | `approved` / `finalized` / `failed` |
| 失敗時的 `content` | `[你的姓名]\n[你的聯絡方式]` | 空字串 |
| 失敗時的 `letters` 列 | 呼叫失敗不寫、審核不過寫 | 一律寫 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/designs/design-agents.md` | §3.4 | `issues[]` 的解析容忍度說明 |
| | §4 | Drafter 輸入欄加入歷輪草稿與意見；Reviewer 規則寫明佔位符是最終形態 |
| | §5 | 生成迴圈偽碼改為 N 輪語意、最後一輪不審核、呼叫失敗即終止；輪數來自設定 |
| | §6 | 測試項補輪數語意、累積歷程、呼叫失敗終止、`issues` 物件形式解析 |
| `docs/designs/design-pipeline.md` | §5 | 設定表加入 `llm.max_letter_rounds` |
| | §6 | 錯誤處理表為 letter 階段的 Agent 呼叫失敗加例外列 |
| | §8 | 測試項補呼叫失敗即 `letter_failed` |
| `docs/designs/design-schema.md` | §2.3 | `letters.status` 列舉加 `finalized`；`content` 允許空 |
| | §3 | 狀態轉換表補「呼叫失敗」與「跑滿輪數」兩條轉入理由 |
| `docs/designs/design-api.md` | letter viewmodel | `finalized` 視同可讀取的信件 |
| `docs/designs/design-extension.md` | 求職信卡片 | `finalized` 顯示信件並標示未經最後審查；`letter_failed` 文案涵蓋呼叫失敗 |
| `docs/tests/test-agents.md` | 求職信 | 輪次矩陣、累積歷程、呼叫失敗終止、`issues` 型別容忍 |
| `docs/tests/test-pipeline.md` | letter 階段 | 呼叫失敗轉 `letter_failed` 且不阻塞其他職缺 |
| `configs/config.example.yaml` 等 | `llm` | `max_letter_rounds` |

## 5. 待實作進度

- [x] canonical 文件依 §4 就地更新
- [x] `internal/agents`：迴圈語意、累積歷程、prompt、`parseReview`
- [x] `internal/pipeline`：呼叫失敗終止、失敗寫入 `letters`
- [x] `internal/store`：`finalized` 與空 `content` 的驗證
- [x] `internal/api` 與 `extension`：`finalized` 呈現
- [x] 設定：`llm.max_letter_rounds` 解析與預設
- [x] 測試依 §4 的兩份 test 文件補齊，`docs/verify.md` §3.2／S10 的標準答案隨之更新

## 6. 已知殘留限制

- `max_letter_rounds` 設為 1 時只有一次 draft、沒有任何 review，`letters.runner_review` 為空。這是設定者自願放棄審查，不視為錯誤。
- letter 階段的呼叫失敗不再讓該階段回報錯誤：該筆已寫入結果並離開取件狀態，錯誤原因由 Error log、`letters` 列與 `agent_calls` 三處記錄。維持回報錯誤會讓 CLI 與 worker 把已處置的業務結果誤報為執行失敗。
- 呼叫失敗即終止的代價是暫時性故障（LLM 服務中斷、額度耗盡）也會判 `letter_failed`，需要使用者再按一次。取捨理由是 runner 層已有 Primary、Primary、Fallback 三次，再加自動重試只會在故障期間持續燒額度。
