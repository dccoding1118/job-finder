# 測試規格 — agents（`internal/agents`）

對應 [agents 模組設計](../designs/design-agents.md)、PRD R3.2、R4、R5、R8.2。B2 實作 Runner 與 Scorer；B3 擴充 Drafter、Reviewer 與求職信防線；B7 新增 Filter 並改為四維評分。本文件是 L1 模組測試規格：以 fake Runner、合成 Profile 與合成 JD 驗證結構化輸出、重試策略與防線，不呼叫實際 claude 或 codex CLI。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件已實作批次的 AT-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| Profile 與 Job | 使用合成角色、技能、經歷、量化成果與 JD；不含姓名、聯絡方式、學校、公司或真實職缺內容 |
| Runner | 角色邏輯由 fake Runner 注入；CLI subprocess 以測試用假 executable 驗證 argv、cwd、timeout 與輸出，不呼叫真 CLI |
| JSON | 各成功回覆只含設計定義的 JSON 物件；另以合成的前後說明、型別錯誤與缺欄位覆蓋解析失敗 |
| 時間與稽核 | 注入 clock；每次 Runner 嘗試均檢查送往 store 的稽核資料，但不以 SQLite 行為作為本模組測試目標 |
| PII | Email、電話與身分證字號 pattern 在測試執行時動態組成；不得寫入 fixture、log 或版控檔案 |

## 3. B2 單元測試案例：Runner 與 Scorer

### 3.1 Runner 與輸出契約

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-01 | ClaudeRunner 或 CodexRunner 的底層程序成功回傳模型文字 | `Invoke` 回傳模型文字；工作目錄、逾時與輸出取得方式依 Runner 設定處理 |
| AT-02 | 底層程序非零退出、逾時或無法啟動 | `Invoke` 回傳可辨識的錯誤並保留底層原因，不將失敗輸出當成成功回覆 |
| AT-03 | 回覆含一個合法 JSON 區塊與前後說明文字 | 取出最後一個括號平衡的頂層 JSON 物件並完成解析 |
| AT-04 | 回覆沒有 JSON、JSON 不完整，或含多個無法判定的物件 | 視為輸出驗證失敗，不產生結果 |
| AT-18 | prompt 經 stdin 傳入，stdout 為 JSON envelope | 取 envelope 的 `result` 欄位為回覆；`is_error` 為真或 `subtype` 非 `success` 時視為 Invoke 失敗 |
| AT-19 | CLI 將最終訊息寫入 `-o` 指定的暫存檔，stdout 另含 transcript | 以該檔內容為回覆，不受 stdout transcript 影響；暫存檔隨 invocation 暫存目錄清除 |
| AT-24 | envelope 帶 `usage` 與 `total_cost_usd`，或 JSONL 事件行帶 token 數而無費用 | 用量原樣讀出並隨該次呼叫寫入 `agent_calls`；未提供的欄位為 0，不以價目表換算；envelope 缺 `usage` 時全部用量為 0 但回覆仍照常解析 |
| AT-07 | CLI 在 transcript 中回音 prompt，並將同一答案輸出兩次 | 解析取最後一個完整物件，不把兩份答案之間的雜訊併入 |
| AT-05 | Scorer 回傳四維 0–100 整數與上限內理由 | 解析為合法 `ScoreResult`，四維與理由完整保留 |
| AT-06 | Scorer 缺少任一維度、分數超出範圍、分數非整數或理由過長 | 拒絕輸出，回傳契約錯誤 |

### 3.2 呼叫策略與稽核

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-10 | primary Runner 第一次失敗、第二次成功 | 使用同一 primary 重試一次後成功；不呼叫 fallback |
| AT-11 | primary Runner 兩次皆失敗，fallback 成功 | 依序呼叫 primary 兩次與 fallback 一次，回傳 fallback 的合法結果 |
| AT-12 | primary 與 fallback 均失敗，或均回傳非法 JSON | 回傳失敗；不回傳部分 `ScoreResult` |
| AT-13 | 成功、程序失敗與 JSON 驗證失敗的各次呼叫 | 每次均建立正確 role、runner、input、raw output、ok、duration 的稽核資料；輸入與輸出不含 PII |
| AT-14 | 以合成 Profile 與 Job 產生 Scorer prompt | prompt 含四個評分維度、基準分規則、`intents` 與資格清單、JD 與僅輸出 JSON 的約束；不含履歷敘事；不要求 Agent 自算總分 |
| AT-15 | 四個常駐角色各自指定合法的 primary 與 fallback endpoint | 載入 `llm.roles` 後，篩選、評分、信件起草與信件審查各使用自己的 agent/model；agent 只接受 `claude` 或 `codex` |
| AT-16 | 每個 role endpoint 指定 agent 與 model | subprocess argv 精確包含該角色對應的 `--model`；同一 agent 在不同角色可使用不同 model；缺漏、空白或未知設定在外部呼叫前失敗 |
| AT-17 | Runner 執行正常、非零或逾時 | 每次使用新的空暫存 cwd；正常回傳 stdout，非零與 timeout 回安全錯誤並清理 cwd |
| AT-08 | 任一角色缺少路由、runner 名稱不合法，或設定在兩輪執行間變更 | 拒絕不完整設定；下一輪建立的 Pipeline 使用新路由，既有執行不改變 |

## 4. B3 單元測試案例：Drafter、Reviewer 與防線

### 4.1 Draft 與 review 契約

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-19A | 以合成 Profile 與 Job 產生 Drafter 與 Reviewer prompt | Drafter prompt 含承諾性敘述的禁令與其例示（面試時說明什麼、到職後完成哪個專案、將取得或更新哪張證照）與「以既成事實與現況陳述」的要求；Reviewer prompt 要求把承諾性敘述列為必挑項 |
| AT-20 | Drafter 回傳含兩個指定落款佔位符的合法 `letter` | 解析成功；信件文字可送入防線與 Reviewer |
| AT-21 | Drafter 缺少 `letter`、`letter` 非字串或回傳無法解析 JSON | 拒絕輸出，依呼叫策略重試或 fallback |
| AT-22 | Reviewer 回傳 `approve`，未提供 `edited_letter` | 採用原草稿為最終稿 |
| AT-23 | Reviewer 回傳 `approve` 與合法 `edited_letter` | 採用編輯後版本為最終稿，並重新通過全部防線 |
| AT-24 | Reviewer 回傳 `revise` 與具體 `issues` | 該版草稿與其 issues 併入歷程，放入下一次 Drafter prompt，產生新草稿後重新審查 |
| AT-25 | Reviewer 的 verdict 非法、`revise` 未附 issues，或 `edited_letter` 型別錯誤 | 拒絕輸出，依呼叫策略重試或 fallback |
| AT-26 | Reviewer 以 `revise` 回覆非空 `edited_letter`，或 `issues` 含空白項目 | 拒絕不符合契約的回覆；不得把未核准版本當成下一輪草稿或最終稿 |
| AT-27 | Reviewer 的 `issues` 元素為物件而非字串 | 取其文字欄位攤平為一句後照常解析；不因此判定整份回覆不合法 |
| AT-28 | Reviewer 的 `issues` 元素為字串 | 維持原行為，逐則原樣保留 |

### 4.2 生成迴圈與防幻覺防線

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-30 | 初稿通過防線且 Reviewer 首輪 `approve` | 產生 approved 結果，記錄一輪審查、draft/review runner 與可供 store 保存的 review log |
| AT-31 | 輪數上限 3，初稿與第二版皆被 `revise`，第三版不再送審 | 回傳 `finalized` 結果與第三版信件；第三輪不呼叫 Reviewer；review log 完整保留前兩輪意見 |
| AT-32 | 輪數上限 3，第二輪 Reviewer 回 `approve` | 立即回傳 approved 結果，不跑第三輪，`rounds` 記 2 |
| AT-33 | 信件缺少任一指定佔位符，或含額外未解析的 `[…]` 佔位符 | 程式防線拒絕信件，不送出或不接受 Reviewer 的核准結果 |
| AT-34 | 信件含 Profile 技能集與 JD 皆未出現的技術詞 | 程式防線以幻覺技術詞拒絕信件 |
| AT-35 | 信件含 denylist 禁詞、內建 PII pattern，或超過設定的字數上限 | 程式防線拒絕信件，錯誤指出觸發的規則，不輸出完整敏感內容 |
| AT-36 | 信件僅使用 Profile 或 JD 可支持的技術詞，含正確佔位符，且長度與 PII 檢核均合法 | 程式防線通過 |
| AT-37 | 以合成 Profile、Job 與 Reviewer issues 產生 Drafter／Reviewer prompt | Drafter prompt 限制可用事實、語言、字數與佔位符；Reviewer prompt 要求檢查幻覺、誇大與空泛詞 |
| AT-38 | 中間輪次的草稿或 Reviewer `edited_letter` 未通過防線 | 不呼叫 Reviewer，或不接受其 `approve`；以具體防線問題作為該版意見併入歷程要求重寫，並計入輪數上限 |
| AT-39 | Drafter 或 Reviewer 的 primary、重試與 fallback 呼叫交錯發生 | 每次嘗試都以正確 role 和 runner 寫稽核資料；成功結果只採用通過契約驗證者 |
| AT-40 | 分類被拒回應：CLI 自報錯誤（含 rate limit）、空輸出、無 JSON、JSON 無法解析、`reason` 超過上限、四維超出範圍、Filter 條件欄位不合法、其他內容不合法 | 各回對應失敗類別（含 `invalid_condition`）；CLI 自報錯誤優先於內容驗證 |
| AT-41 | 四維皆為低分但格式合法的評分回應 | 通過驗證並視為成功呼叫；低分不得被判定為失敗 |
| AT-42 | `reason` 恰為 100 字與 101 字 | 前者通過驗證；後者被拒 |
| AT-43 | 以字數規則計算 `reason` 長度：純中文、單一英文詞、含 `Node.js`／`C++`／`Go/Rust` 的混排 | 中文逐字計數，連續英數整段計一字，連字與 `.`／`/`／`+`／`#` 不切斷該詞 |
| AT-44 | 中英混排、runes 超過 100 但依字數規則未超過上限的 `reason` | 通過驗證，不觸發重跑 |
| AT-58 | 最後一輪的草稿未通過防線 | 回傳 failed 結果、不帶信件內容，供 pipeline 轉為 `letter_failed` |
| AT-59 | 任一輪的 Drafter 或 Reviewer 三個 runner 全數失敗 | 立即終止生成並回錯誤，不重試該輪、不跑後續輪次；已完成輪次的稽核資料照常寫入 |
| AT-60 | 第 k 輪的 Drafter prompt | 含第 1 至 k-1 版草稿原文與各版對應意見，依輪次順序排列 |
| AT-61 | 輪數上限取自設定而非常數 | 以不同上限值驅動，Drafter 與 Reviewer 的呼叫次數隨之改變 |
| AT-62 | 輪數上限設為 1 | 只跑一次 Drafter、不呼叫 Reviewer，回 `finalized`；`runner_review` 為空 |

## 4.4 B7 單元測試案例：Filter 與四維 Scorer

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-50 | fake Runner 回合法 `FilterResult` | 解析出 `conditions[]` 各欄位與列舉；每條的 `kind`、`group`、`category`、`verdict` 完整保留 |
| AT-51 | 缺欄位、列舉非法、`group` 非正整數或 `years_required` 為負 | 驗證失敗並歸類為 `invalid_condition`；不部分採用 |
| AT-52 | `conditions` 為空陣列（JD 未載明條件） | 視為合法結果，彙總為全 `pass` |
| AT-53 | 年資、管理年資與產業類條件 | Agent 回的 `verdict` 被忽略；程式以 `derived` 加總比較後決定，且只採用 `years_required`／`years_max`／`industry_keys` |
| AT-54 | 以合成 Profile 與 Job 產生 Filter prompt | prompt 含 `qualifications`、經歷的 `industry` key 清單與 JD；**不含** `intents` 與履歷敘事；明確要求判不出來回 `unknown`、不得猜測為 `fail` |
| AT-55 | Filter 的 primary 失敗後重試與 fallback | 與其他角色相同的三次嘗試策略；每次以 `role='filter'` 寫稽核 |
| AT-56 | JD 完全未揭露某評分維度所需資訊 | Scorer 該維回基準分並通過驗證；不因缺資訊被判失敗 |
| AT-57 | Scorer 的 `bonus_fit` 面對未滿足的加分條件 | 不低於基準分（只加不減） |

## 4.3 Calibrator（S1，尚未實作）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| AT-45 | fake Runner 回合法 `CalibrationResult` | 解析出 `summary` 與 `suggestions[]`，各欄位型別與列舉正確；寫入 `role='calibrator'`、`job_id` 為 NULL 的稽核 |
| AT-46 | `suggestions[].field` 指向 `experiences`、`qualifications`、`honesty_bounds` 或 `derived` | 拒絕**整份**建議並回安全錯誤；不部分採用白名單內的項目 |
| AT-47 | `suggestions` 為空陣列 | 視為合法結果（證據不足，不建議調整）；不報錯、不重試 |
| AT-48 | 輸出非法 JSON、缺欄位、`action`／`confidence` 非列舉值或 `value` 型別不相容 | 驗證失敗即結束，不以變體 prompt 重試；失敗仍寫入稽核 |

## 5. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- B2 能以合法結構化回覆取得各維分數與理由，並正確處理 Runner 重試、fallback 與稽核資料。
- B7 的 Filter 能拆解 JD 條件並逐條給 `pass`／`fail`／`unknown`；年資類判定由程式覆寫，加分條件不進篩選彙總；Scorer prompt 不含履歷敘事。
- B3 依 `llm.max_letter_rounds` 決定輪數，最後一輪不送審而直接產出 `finalized`；只有通過佔位符、技術詞、PII 與字數防線且 Reviewer 核准的信件才能成為 approved 結果，兩者都必須通過全部防線。
- B3 在任一輪 Agent 呼叫失敗時立即終止，不自行重試整輪。
- 真實 claude / codex CLI、真實職缺與可供使用者檢閱的求職信，僅依 [verify](../verify.md) 的 B2、B3 手動驗收案例檢查，不進 L1 或例行 CI。
