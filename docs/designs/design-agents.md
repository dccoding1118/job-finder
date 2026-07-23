# 模組設計 — agents（智能層：Runner 與三 Agent）

對應需求：R4、R5、R8.2。所有 LLM 互動的唯一入口。

## 1. 職責邊界

- **Runner 抽象**：以 subprocess 呼叫 headless CLI（claude 為主、codex 為輔），統一「prompt 進、結構化 JSON 出」。
- **三個 Agent 角色**：Scorer（評分）、Drafter（起草）、Reviewer（審查），各自的 prompt 模板與輸出契約。
- 輸出驗證、重試、runner fallback、防幻覺程式防線、`agent_calls` 稽核寫入；Profile 相關呼叫記錄工作開始時 snapshot 的 `profile_revision`。
- 不負責：取件與狀態推進（pipeline）、權重計算後的分流（pipeline 依 store 轉換）。

## 2. Runner 抽象

| 型別 | 定義 |
|---|---|
| `Runner`（介面） | `Name() string`；`Invoke(ctx, prompt string) (raw string, error)` |
| `ClaudeRunner` | 呼叫 `claude` CLI 非互動模式，明確傳入設定的 model；工作目錄設為空的暫存目錄 |
| `CodexRunner` | 呼叫 `codex` CLI 非互動模式，明確傳入設定的 model；使用 ephemeral、read-only 與非 git 目錄允許旗標 |

兩個 Runner 都以 CLI 的結構化輸出取回結果，不從自由文字刮取；prompt 的傳入方式依各 CLI 介面而定。

| Runner | argv | prompt 傳入 | 回應取得 |
|---|---|---|---|
| ClaudeRunner | `claude -p --model <model> --output-format json` | stdin | stdout 的 JSON envelope，取 `result` 欄位；`is_error` 為真或 `subtype` 非 `success` 即失敗 |
| CodexRunner | `codex exec --model <model> --ephemeral --skip-git-repo-check --sandbox read-only --color never -o <暫存檔> <prompt>` | 最後一個位置參數 | `-o` 指定的暫存檔，其內容為 agent 的最終訊息，不含 transcript |

- `-p` 使 claude 進入非互動模式；codex 不保留 session，且 subprocess 僅有唯讀 sandbox。暫存檔位於該次 invocation 的暫存目錄內，隨目錄一併清除。
- 回應解析取**最後一個括號平衡的頂層 JSON 物件**，並忽略字串內的大括號，使 CLI 重複輸出答案或夾帶說明文字時仍能正確取值。
- 錯誤面：非零退出、逾時（pipeline 設定）、envelope 回報失敗、輸出非預期格式，皆視為 Invoke 失敗。三次嘗試（primary 兩次、fallback 一次）全失敗時，回傳的錯誤保留最後一次的底層原因。

### 呼叫策略（設定檔 `llm.roles`）

| 角色 | primary | fallback | 說明 |
|---|---|---|---|
| scorer | `{agent: claude, model: claude-sonnet-5}` | `{agent: codex, model: gpt-5.6-terra}` | primary 失敗（Invoke 失敗或 JSON 驗證失敗）重試 1 次，再失敗換 fallback 一次 |
| drafter | `{agent: claude, model: claude-sonnet-5}` | `{agent: codex, model: gpt-5.6-terra}` | 同上；可獨立選擇適合寫作的 model |
| reviewer | `{agent: codex, model: gpt-5.6-terra}` | `{agent: claude, model: claude-sonnet-5}` | 預設跨 agent 審查；可獨立選擇審查強度，不受其他角色設定限制 |

每個 `primary`／`fallback` endpoint 都必須同時指定 `agent` 與 `model`。`agent` 只能是 `claude` 或 `codex`；model 直接傳入該 CLI 的 `--model`，因此同一 agent 可在不同角色使用不同 model。設定以 strict YAML 解析；未知欄位、空 model 或未知 agent 均在任何外部呼叫前失敗。

每次 invocation 使用新建的空暫存目錄，並受 `llm.timeout` 限制。`agent_calls` 保存實際 runner；固定 model 由該次物化 config 與 Runner argv 驗證，不把 model 誤寫成 runner 名稱。

每次呼叫（含失敗）寫入 `agent_calls`（role、runner、input、output、ok、duration）。

`primary.agent` 與 `fallback.agent` 可相同，model 可不同。每次 `jobfinder run` 在啟動時讀取設定；修改 agent 或 model 後的下一輪執行立即生效，不需重新建置。稽核保存實際 runner，不保存 CLI 憑證。

## 3. 輸出契約（Agent 回覆須為單一 JSON 物件）

所有 prompt 皆要求「只輸出 JSON，不加說明文字」；解析時先截取首個 `{…}` 區塊再 unmarshal，缺欄位/型別錯誤＝驗證失敗。

### 3.1 ScoreResult（Scorer）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `hard_skill` / `domain` / `seniority` / `condition` / `direction` | int | 0–100 |
| `reason` | string | ≤50 中文字，說明推薦或不推薦 |

加權總分由 Go 依設定檔權重計算（PRD R4.2），Agent 不回總分。

### 3.2 DraftResult（Drafter）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `letter` | string | 求職信全文；結尾必含 `[你的姓名]` 與 `[你的聯絡方式]` 佔位符 |

### 3.3 ReviewResult（Reviewer）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `verdict` | enum | `approve` / `revise` |
| `issues[]` | string[] | `revise` 時必填：具體問題（幻覺技能、空泛詞、誇大） |
| `edited_letter` | string NULL | Reviewer 直接刪改後可過審的版本；有值且 `verdict=approve` 時以此為最終稿 |

## 4. Agent prompt 要點（模板放 `internal/agents/prompts/*.tmpl`）

Profile 輸入一律來自 provider snapshot 的 canonical YAML。Scorer、Drafter 與 Reviewer 單次工作途中不得重新載入 Profile；每次 `agent_calls` 與其 Score／Letter 產出保存相同的實際 revision。

| 角色 | 輸入 | 規則要點 |
|---|---|---|
| Scorer | Profile YAML 全文＋Job（title/company/JD/薪資/地點/remote） | 逐維給分；條件契合須對照 preferences；方向契合對照 directions 關鍵字；理由 ≤50 字 |
| Drafter | Profile＋Job＋（重寫輪）Reviewer issues | 只可使用 Profile 存在的技能與成就；引用量化數據；遵守 `honesty_bounds`；精煉（300–450 字）；佔位符落款；繁體中文（JD 為英文則英文） |
| Reviewer | Profile＋Job＋草稿 | 毒舌審查：任何 Profile 無根據的技能/經歷/數字＝幻覺必挑；空泛形容詞（「熱情」「抗壓」等無實據修飾）要求刪除；可直接給 `edited_letter`；檢查佔位符落款 |

## 5. 生成迴圈與防幻覺防線（R5）

letter 工作以開始時取得的 snapshot 完成。若 Profile 在工作途中更新，Letter 與所有 draft／review call 仍記錄原 revision；完成後可立即導出 `letter_stale=true`，但不得丟棄、覆蓋或自動重跑已完成的使用者要求。

```
draft = Drafter(profile, job)
for review_round in 1..3:  # 初稿後最多重寫兩次
    guard(draft)            # 程式防線，失敗=直接要求重寫（視同 revise）
    rv = Reviewer(profile, job, draft)
    if rv.verdict == approve:
        final = rv.edited_letter ?? draft
        guard(final); pii_lint(final)   # 最終稿再過一次防線
        return approved(final, rounds, review_log)
    if review_round == 3:
        return failed(review_log)   # → letter_failed
    draft = Drafter(profile, job, issues=rv.issues)
return failed(review_log)   # → letter_failed
```

`guard()` 程式防線（R5.4）：

| 檢查 | 規則 |
|---|---|
| 佔位符 | 必含 `[你的姓名]`、`[你的聯絡方式]`；不得出現其他 `[…]` 未解析佔位 |
| 技術詞白名單 | 從 letter 抽出技術詞，與白名單比對；出現白名單外的技術詞 ⇒ 失敗。白名單＝Profile 技能全集（`skills` 的 expert／proficient／familiar 加上各 `experiences[].skills`）＋JD 內文 |
| PII | 重用 profile 模組的 denylist＋pattern 檢核 |
| 長度 | 超出上限（設定，預設 600 字）⇒ 失敗 |

## 6. 測試

- Runner：以假可執行檔模擬正常、非零、逾時與 argv/cwd；精確驗證 model flag、空暫存目錄與 cleanup。真 CLI 呼叫只由 opt-in 的 `e2e-live` 驗收，不進 CI。
- 三 Agent：fake Runner 回罐頭 JSON，驗證解析、驗證失敗路徑、fallback 切換、迴圈輪次上限。
- guard：表驅動正反例（幻覺技能、缺佔位符、含 PII、超長）。

## 7. 交付物

- `internal/agents/`：runner（claude/codex/fake）、角色路由驗證、prompts、scorer/drafter/reviewer、guard、測試。

## 8. 待決

（無——CLI 參數細節屬 spike 回填項，非設計待決。）
