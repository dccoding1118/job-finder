# 模組設計 — agents（智能層：Runner 與五 Agent）

對應需求：R3.2、R4、R5、R8.2。所有 LLM 互動的唯一入口。

## 1. 職責邊界

- **Runner 抽象**：以 subprocess 呼叫 headless CLI（claude 為主、codex 為輔），統一「prompt 進、結構化 JSON 出」。
- **五個 Agent 角色**：Filter（JD 條件拆解與語意硬條件比對）、Scorer（四維評分）、Drafter（起草）、Reviewer（審查）、Calibrator（反向校準建議，S1 範圍尚未實作），各自的 prompt 模板與輸出契約。Calibrator 只在使用者主動觸發校準時呼叫，不參與任何常駐階段。
- 輸出驗證、重試、runner fallback、防幻覺程式防線、`agent_calls` 稽核寫入；Profile 相關呼叫記錄工作開始時 snapshot 的對應 revision（filter 記 `filter_revision`、score 記 `score_revision`、letter 記兩者）。
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
| filter | `{agent: claude, model: claude-sonnet-5}` | `{agent: codex, model: gpt-5.6-terra}` | 同下；篩選是逐條事實比對，可獨立選擇較便宜的 model |
| scorer | `{agent: claude, model: claude-sonnet-5}` | `{agent: codex, model: gpt-5.6-terra}` | primary 失敗（Invoke 失敗或 JSON 驗證失敗）重試 1 次，再失敗換 fallback 一次 |
| drafter | `{agent: claude, model: claude-sonnet-5}` | `{agent: codex, model: gpt-5.6-terra}` | 同上；可獨立選擇適合寫作的 model |
| reviewer | `{agent: codex, model: gpt-5.6-terra}` | `{agent: claude, model: claude-sonnet-5}` | 預設跨 agent 審查；可獨立選擇審查強度，不受其他角色設定限制 |

每個 `primary`／`fallback` endpoint 都必須同時指定 `agent` 與 `model`。`agent` 只能是 `claude` 或 `codex`；model 直接傳入該 CLI 的 `--model`，因此同一 agent 可在不同角色使用不同 model。設定以 strict YAML 解析；未知欄位、空 model 或未知 agent 均在任何外部呼叫前失敗。

每次 invocation 使用新建的空暫存目錄，並受 `llm.timeout` 限制。`agent_calls` 保存實際 runner；固定 model 由該次物化 config 與 Runner argv 驗證，不把 model 誤寫成 runner 名稱。

每次呼叫（含失敗）寫入 `agent_calls`（role、runner、input、output、ok、duration）。

`primary.agent` 與 `fallback.agent` 可相同，model 可不同。每次 `jobfinder run` 在啟動時讀取設定；修改 agent 或 model 後的下一輪執行立即生效，不需重新建置。稽核保存實際 runner，不保存 CLI 憑證。

## 3. 輸出契約（Agent 回覆須為單一 JSON 物件）

所有 prompt 皆要求「只輸出 JSON，不加說明文字」；解析時先截取首個 `{…}` 區塊再 unmarshal，缺欄位/型別錯誤＝驗證失敗。

### 3.1 FilterResult（Filter）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `conditions[]` | object[] | JD 拆解出的條件，逐條含判定；可為空陣列（JD 未載明任何條件） |
| `conditions[].text` | string | 條件敘述，≤40 中文字 |
| `conditions[].kind` | enum | `required`／`bonus` |
| `conditions[].group` | int | 選言分組編號；同組任一 `pass` 即該組 `pass`，獨立條件各自一組 |
| `conditions[].category` | enum | `education`／`skill`／`certification`／`language`／`experience_years`／`management_years`／`industry`／`other` |
| `conditions[].verdict` | enum | `pass`／`fail`／`unknown` |
| `conditions[].years_required` | number NULL | 年資類條件的要求下限 |
| `conditions[].years_max` | number NULL | JD 明確設定的年資上限，無則 NULL |
| `conditions[].industry_keys[]` | string[] | `industry` 類別：JD 要求對應到 profile 的哪些 `industry` key |

**年資與產業年資的 `verdict` 一律由程式覆寫**：Agent 只負責讀出 `years_required`／`years_max`／`industry_keys`，比較由 Go 以 `derived` 加總執行（見 [design-pipeline](design-pipeline.md) §3.2）。Agent 對這幾類回的 verdict 不採用，避免 LLM 算術錯誤成為判定依據。

`kind` 為 `bonus` 的條件不進篩選彙總，只保存供評分關的 `bonus_fit` 重用。輸出缺欄位、列舉非法、`group` 非正整數或 `years_required` 為負，皆為驗證失敗。

### 3.2 ScoreResult（Scorer）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `content_fit` / `benefit_fit` / `bonus_fit` / `industry_fit` | int | 0–100；各維以門檻分為基準加減後 clamp，無資訊可判時回基準分 |
| `reason` | string | 說明推薦或不推薦；prompt 要求 40~60 字，驗證容忍上限 100 字，兩者皆以下述字數規則計算 |

**字數規則**：中文（與其他非拉丁字元）一字算一字，連續的英文詞或數字整段只算一字——`Kubernetes` 與 `Node.js` 各算一字。理由是雙語敘述的長度取決於它讀起來多長，不是英文詞拼出幾個字母；以字母計數會讓一段簡短、切題的理由僅因引用數個英文技術詞就超限。此規則同時寫進 prompt（讓 LLM 得以自我檢查）與驗證。

**要求字數與容忍上限刻意分離**：兩者相等時，LLM 只要略微超出就整筆作廢並重跑一次，而重跑是實打實的 token 成本，且重跑的回應因脈絡疊加常不如首次準確。因此 prompt 要求的是實際想要的長度（40~60 字），驗證的上限放寬到 100 字，只擋「完全無視要求」的回應。放寬容忍上限**不得**回頭調高 prompt 要求的字數。

每次嘗試的稽核結果只有兩種：runner 有回應且回應通過上述契約（成功），或未通過（失敗）。分數高低不影響此判定。失敗者由 `ClassifyFailure(role, output)` 分為 `runner_error`（CLI 自報錯誤，優先於內容驗證）、`empty_output`、`no_json`、`invalid_json`、`reason_too_long`、`score_out_of_range`、`invalid_condition`（Filter 的條件列舉、分組或年資欄位不合法）、`invalid_content`，供 API 的處理進度呈現失敗原因而不外洩原始輸出。此分類對五個角色共用。

加權總分由 Go 依設定檔權重計算（PRD R4.2），Agent 不回總分。

### 3.3 DraftResult（Drafter）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `letter` | string | 求職信全文；結尾必含 `[你的姓名]` 與 `[你的聯絡方式]` 佔位符 |

### 3.4 ReviewResult（Reviewer）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `verdict` | enum | `approve` / `revise` |
| `issues[]` | string[] | `revise` 時必填：具體問題（幻覺技能、空泛詞、誇大） |
| `edited_letter` | string NULL | Reviewer 直接刪改後可過審的版本；有值且 `verdict=approve` 時以此為最終稿 |

### 3.5 CalibrationResult（Calibrator）

| 欄位 | 型別 | 約束 |
|---|---|---|
| `summary` | string | ≤200 中文字，說明成功樣本的共同特徵 |
| `suggestions[]` | object[] | 可為空陣列（代表無足夠證據建議調整） |
| `suggestions[].field` | string | `search.*`／`requirements.*`／`intents.*` 的欄位路徑（如 `search.directions[0].keywords`）；白名單外一律拒絕整份建議 |
| `suggestions[].action` | enum | `add` / `remove` / `replace` |
| `suggestions[].value` | string ∣ number ∣ string[] | 與目標欄位型別相容 |
| `suggestions[].evidence` | string | ≤100 字，指出此建議來自哪些樣本的共同特徵 |
| `suggestions[].confidence` | enum | `high` / `medium` / `low` |

驗證失敗（含白名單外欄位、型別不相容）不重試變體 prompt，直接以失敗結束並記入 `agent_calls`——校準是使用者主動觸發的一次性分析，反覆重試只是浪費額度。套用與 diff 產生屬 profile 模組（見 [design-profile](design-profile.md) §7.3）。

## 4. Agent prompt 要點（模板放 `internal/agents/prompts/*.tmpl`）

Profile 輸入一律來自 provider snapshot，且**各角色只取自己該看的子集**（見 [design-profile](design-profile.md) §1）。單次工作途中不得重新載入 Profile；每次 `agent_calls` 與其產出保存相同的實際 revision。

| 角色 | 輸入 | 規則要點 |
|---|---|---|
| Filter | `qualifications`（學歷、技能、證照、語言）＋`experiences[]` 的 `industry` key 清單＋Job（title/company/JD/薪資/地點/remote） | 先把 JD 拆成逐條條件並標記必備／加分與選言分組；再逐條比對 Profile 給 `pass`／`fail`／`unknown`；**判不出來一律 `unknown`，不得猜測為 `fail`**；學歷須同一筆同時滿足級別與科系；年資與產業年資只回要求數值與對應的 `industry` key，不自行比較；不給分數 |
| Scorer | `intents`＋`qualifications` 的 `skills`／`certifications`／`languages`＋`requirements.remote`／`locations`＋篩選關保存的加分條件＋Job（title/company/JD/薪資/地點/remote/福利與工時敘述） | 四維以門檻分為基準加減；`content_fit` 對照 `content_likes`／`content_dislikes`；`benefit_fit` 對照 `salary_target` 與優於勞基法的休假、彈性工時、額外獎金，遠端形式的加分級距見 [design-pipeline](design-pipeline.md) §3.3；`bonus_fit` **只加不減**；`industry_fit` 對照 `industry_interests`；無資訊可判時回基準分；理由 40~60 字（字數規則見 §3.2）。**輸入不含 `experiences` 的 `role`／`org_type`／`achievements` 與 `honesty_bounds`** |
| Drafter | `experiences`＋`qualifications`＋`honesty_bounds`＋Job＋（重寫輪）Reviewer issues | 只可使用 Profile 存在的技能與成就；引用量化數據；遵守 `honesty_bounds`；精煉（300–450 字）；佔位符落款；繁體中文（JD 為英文則英文） |
| Reviewer | 同 Drafter 的子集＋Job＋草稿 | 毒舌審查：任何 Profile 無根據的技能/經歷/數字＝幻覺必挑；空泛形容詞（「熱情」「抗壓」等無實據修飾）要求刪除；可直接給 `edited_letter`；檢查佔位符落款 |
| Calibrator | Profile 的 `search`／`requirements`／`intents`＋成功樣本（JD、職稱、產業、地區、薪資、四維分數）＋對照樣本 | 只比較兩組樣本的共同與差異特徵，依 [design-profile](design-profile.md) §7.2 的維度作答；只得建議 `search`／`requirements`／`intents` 欄位；證據不足時回空 `suggestions`，不得臆測；不得輸出任何履歷事實的修改建議 |

Scorer 的輸入排除履歷敘事：成就敘事會被讀成「擅長 ⇒ 適配高」，使「做過但不想再做」的內容只加不減，適配判斷因此失真。

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
| 技術詞白名單 | 從 letter 抽出技術詞，與白名單比對；出現白名單外的技術詞 ⇒ 失敗。白名單＝`qualifications.skills[].name` ∪ 各 `experiences[].skills` ∪ JD 內文 |
| PII | 重用 profile 模組的 denylist＋pattern 檢核 |
| 長度 | 超出上限（設定，預設 600 字）⇒ 失敗 |

## 6. 測試

- Runner：以假可執行檔模擬正常、非零、逾時與 argv/cwd；精確驗證 model flag、空暫存目錄與 cleanup。真 CLI 呼叫只由 opt-in 的 `e2e-live` 驗收，不進 CI。
- 五 Agent：fake Runner 回罐頭 JSON，驗證解析、驗證失敗路徑、fallback 切換、迴圈輪次上限；Filter 另驗條件拆解欄位驗證、年資類 verdict 由程式覆寫、加分條件不進篩選彙總；Calibrator 另驗欄位白名單拒絕與空建議路徑。
- prompt 子集：Scorer prompt 不含 `achievements`／`role`／`org_type`／`honesty_bounds`；Filter prompt 不含 `intents`。
- guard：表驅動正反例（幻覺技能、缺佔位符、含 PII、超長）。

## 7. 交付物

- `internal/agents/`：runner（claude/codex/fake）、角色路由驗證、prompts、filter/scorer/drafter/reviewer/calibrator、guard、測試。

## 8. 待決

（無——CLI 參數細節屬 spike 回填項，非設計待決。）
