# change — 求職信產製歷程

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

求職信由 drafter 與 reviewer 交替跑 N 輪產出，但使用者只看得到最後一版信件。三個問題無從回答：每一版 draft 長什麼樣、reviewer 提了什麼意見、最終版有沒有把那些意見改掉。呼叫全部成功時尤其無聲——系統頁的 Agent 呼叫紀錄只對失敗的呼叫附輸出，成功的呼叫連一個字都不顯示。

資料其實已經寫進資料庫，缺的是讀取路徑與歸組欄位：

- **每一版 draft 與每則 review 的原文都在 `agent_calls.output`**，但 `RecentAgentCalls` 只取最近 N 筆、不依職缺篩選、成功的呼叫不附輸出。
- **`letters` 每次產製各寫一列、不覆蓋**，重跑前的舊產製都在，但 `LetterDetail` 只讀最新一列。
- **失敗的產製不寫 `letters`**，`review_log` 裡的 guard 失敗原因隨之消失，只剩散落的 `agent_calls`。
- **`agent_calls` 只有 `job_id` 與 `created_at`**，同一職缺重跑多次後無法分辨某次呼叫屬於哪一次產製、第幾輪。輪次也無法由呼叫順序推導：runner 重試與 guard 失敗都會產生連續兩筆 drafter 呼叫，兩者的邊界意義不同。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 新增 `letter_attempts` 表：一次求職信產製一列，開始時寫入、結束時回填結果。失敗的產製同樣留列。 |
| D2 | `agent_calls` 增設 `attempt_id` 與 `round`，把每一次呼叫歸進某次產製的某一輪。 |
| D3 | `letters` 增設 `attempt_id`，並移除 `rounds`／`review_log`／`runner_draft`／`runner_review`——歷程面的真相移交 `letter_attempts`，同一事實不存兩處。 |
| D4 | `agents.Audit` 改為接受單一稽核記錄型別，`Round` 為其中一欄；screener 與 scorer 的 `Round` 為 0。 |
| D5 | 新增 `GET /api/v1/jobs/{id}/letter-history`：回該職缺的全部產製（新到舊），每次產製附逐輪呼叫。 |
| D6 | 該 route 不回傳 `agent_calls.input`。 |
| D7 | extension 於職缺詳情頁的求職信卡片下方新增「產製歷程」摺疊區，預設收合；第一層為每次產製，展開後為逐輪的 draft 全文與 review 意見。 |
| D8 | migration 為既有 `letters` 各造一列 `letter_attempts` 並搬移欄位；既有 `agent_calls` 的 `attempt_id` 與 `round` 取 NULL。 |

D6 的理由是問題本身落在輸出面：draft 與 review 都在 `output`，而 `input` 帶著整份 Profile view，讓它經 API 出去只會擴大 PII 的暴露面而不回答任何問題。

D8 不回填舊呼叫的歸組，是因為 runner 重試與 guard 失敗都會讓同一輪出現多筆 drafter 呼叫，時間戳推導出來的輪次會是錯的——空欄位讀得出「不知道」，猜出來的數字讀不出。

`letter_attempts.status` 只在產製結束時寫入終態，進行中的列以 `finished_at` 為 NULL 表示。程序中途死亡會留下永久未收尾的列，這與 `runs` 未收尾輪次的處置一致：由讀取端依職缺狀態判定，不另設心跳。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| 一次產製的紀錄 | 成功才寫 `letters`，失敗無列 | `letter_attempts` 恆有一列，含失敗與進行中 |
| 輪次歸屬 | 無 | `agent_calls.attempt_id` ＋ `round` |
| `letters` 欄位 | 含 `rounds`／`review_log`／`runner_draft`／`runner_review` | 移交 `letter_attempts`，改持 `attempt_id` |
| 逐輪 draft 與 review | 僅存在於 `agent_calls`，無讀取面 | `GET /api/v1/jobs/{id}/letter-history` |
| 重跑後的舊產製 | 存在於 `letters` 但讀不到 | 歷程逐次列出，新到舊 |
| `agents.Audit` 簽章 | 九個位置參數 | 單一稽核記錄型別 |
| 職缺詳情頁 | 只有最終信件 | 加一個預設收合的產製歷程區 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/designs/design-schema.md` | §2 | 新增 `letter_attempts`；`letters` 與 `agent_calls` 的欄位調整；schema 版本 10 |
| | §4 | migration 9 |
| | §5 | `StartLetterAttempt`／`FinishLetterAttempt`／`ListLetterAttempts` |
| `docs/designs/design-agents.md` | 稽核 | `Audit` 的記錄型別與 `Round` 語意 |
| `docs/designs/design-pipeline.md` | letter 階段 | 產製開始建立 attempt、結束回填，逐輪呼叫帶輪次 |
| `docs/designs/design-api.md` | route 表 | `GET /api/v1/jobs/{id}/letter-history` 的回應形狀與不含 `input` 的界線 |
| `docs/designs/design-extension.md` | 職缺詳情 | 產製歷程區的層級與呈現 |
| `docs/tests/test-schema.md` | letters | attempt 生命週期、migration 搬移與 legacy NULL |
| `docs/tests/test-api.md` | letter-history | 逐次逐輪排序、不含 `input` |

## 5. 待實作進度

- [x] schema v10 與 migration
- [x] store 的 attempt 生命週期與查詢
- [x] `agents.Audit` 記錄型別與輪次
- [x] pipeline 的 attempt 串接
- [x] `GET /api/v1/jobs/{id}/letter-history`
- [x] extension 產製歷程區
- [x] canonical 文件更新

## 6. 已知殘留限制

- 升級前的呼叫沒有 `attempt_id` 與 `round`，歷程只顯示該次產製的結果與審查摘要，展不開逐輪內容。
- reviewer 意見的品質本身仍無從評估：歷程讓意見看得見，但「這個意見方向對不對」需要使用者對單輪表態才能累積，屬 `docs/roadmap.md` 的反向校準閉環。
- 產製進行中的列在服務被強制終止後不會收尾，`finished_at` 永久為 NULL。
