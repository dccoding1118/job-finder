# 變更 — 跨來源同一職缺的辨識與合併

## 1. 背景與動機

同一份職缺經常同時刊登於 104、Cake 與 Yourator。現行唯一鍵是 `(source, external_id)`，跨來源沒有任何關聯機制，因此同一個職缺會是三筆獨立 Job：各自評分（三次 LLM 呼叫）、各自可生成求職信（最貴的階段重複計費）、投遞狀態各自獨立（同一個職缺可能被標記為已投遞、又在另一筆顯示待處理）。

三個來源都通之後，這是日常使用的實際痛點，也直接違背「重質不重量」與成本控制。

## 2. 決策摘要

| 項目 | 決策 |
|---|---|
| 識別方式 | 純程式規則，不呼叫 LLM：公司名正規化分群 ＋ 職稱正規化比對 ＋ 地區相容性 |
| 合併積極度 | 高信心自動合併；灰帶只登記為候選，由使用者在 Side Panel 一鍵合併或忽略 |
| 承載方式 | 新增 group 層；處理與投遞仍掛在 group 指定的 **canonical Job**，alias Job 轉入新終態 `merged` |
| 已有產出的 Job | alias 若已有 score／letter／apply 歷史，一律不自動合併，降級為候選 |
| 可逆性 | 提供取消合併，還原 alias 至合併前狀態 |

選擇「canonical Job ＋ alias」而非「把狀態與產出搬到 group」，是為了不動既有狀態機與 revision CAS 契約：所有既有取件、轉換、稽核邏輯照舊作用在 Job 上，group 只多一層歸屬與呈現。

## 3. 識別規則

**比較範圍**：只比較**來源未重疊**的群組。去重要解決的是同一則職缺刊登在兩個平台（如 104 與 Cake 各有一筆）；同平台上的兩筆是兩個不同的職位開口——平台不會把同一則職缺刊兩次——比較它們只會產生使用者必然否決的裁決。群組已因合併涵蓋多個來源時，這些來源全部排除。

**分群鍵（blocking）**：正規化公司名。不同公司一律不互相比較。

正規化（全部為程式規則，可測試、無 LLM）：

| 對象 | 規則 |
|---|---|
| 公司名 | 全形轉半形 → 轉小寫 → 去空白與標點 → 去除法人尾綴與地區後綴（`股份有限公司`／`有限公司`／`公司`／`台灣分公司`／`inc`／`ltd`／`co`／`corp`／`limited`） |
| 職稱 | 全形轉半形 → 轉小寫 → 去括號補述 → 去資歷修飾（`senior`／`sr`／`junior`／`jr`／`資深`／`中高階`／`實習`除外，實習屬不同職缺不得去除） → 中英同義詞正規化（後端↔backend、前端↔frontend、工程師↔engineer↔developer 等對照表） → 去空白與標點 |
| 地區 | 取縣市層級（`台北市`／`新北市`…）；`remote` 或未知視為與任何地區相容 |

| 判定 | 條件 | 動作 |
|---|---|---|
| 高信心 | 來源未重疊 ＋ 正規化公司相等 ＋ 正規化職稱**完全相等** ＋ 地區相容 | 自動合併為同一 group |
| 灰帶 | 來源未重疊 ＋ 正規化公司相等，且（職稱 token Jaccard ≥ `dedupe.title_similarity_threshold`（預設 0.6）但不完全相等；或職稱相等但地區不相容） | 登記 `job_dupe_candidates`，等待使用者裁決 |
| 不同 | 其餘（含來源重疊） | 不建立關聯 |

`dedupe_key = sha256(正規化公司 + "|" + 正規化職稱 + "|" + 縣市)`，供高信心比對以索引完成，不需兩兩比較。

## 4. 資料模型

新增兩張表與 `jobs` 一個欄位；既有欄位與狀態機語意不變。

`job_groups`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `canonical_job_id` | INTEGER FK→jobs | 承載處理、評分、求職信與投遞的那一筆 |
| `dedupe_key` | TEXT NULL | 高信心比對鍵；人工合併的 group 為 NULL |
| `created_at` / `updated_at` | TEXT | RFC3339 |

`job_dupe_candidates`

| 欄位 | 型別 | 說明 |
|---|---|---|
| `id` | INTEGER PK | |
| `group_a_id` / `group_b_id` | INTEGER FK→job_groups | 以較小 id 為 a，`UNIQUE(group_a_id, group_b_id)` |
| `similarity` | REAL | 職稱 token Jaccard |
| `reason` | TEXT | `title_similar` / `location_mismatch` / `has_output` |
| `state` | TEXT | `pending` / `merged` / `ignored` |
| `created_at` | TEXT | RFC3339 |

`jobs` 新增 `group_id INTEGER NULL FK→job_groups`：每筆 Job 入庫即歸屬一個 group（初始為單成員、canonical 為自己）。

**canonical 選擇順序**：有 JD 全文者優先 → `description` 較長者 → 設定檔 `dedupe.source_priority`（預設 `104` > `cake` > `yourator`）→ `first_seen_at` 較早者。

**alias 的處理狀態**：新增終態 `merged`。合併時 alias 由當時狀態轉入 `merged` 並寫入 `status_events`（note 記錄合併前狀態與 canonical job id），此後不被任何階段取件、不耗 LLM、不出現在任何清單。取消合併時依該事件還原。

## 5. 流程落點

| 時機 | 行為 |
|---|---|
| upsert（fetch 與 capture 共用） | 寫入 Job 後計算 `dedupe_key`；命中既有 group 且雙方皆無 score／letter／apply 產出 ⇒ 自動合併；有產出 ⇒ 登記候選 |
| 合併 | 單一交易：選定 canonical、alias 轉 `merged`、`jobs.group_id` 收斂、候選標記 `merged` |
| capture 命中 alias | 回傳 **canonical** 的 job id 與 verdict——使用者在 104 頁面看到的職缺若已由 Cake 評過分，就地標記直接顯示既有判定，不重跑也不重複計費 |
| 取消合併 | alias 還原為合併前狀態與獨立 group；不刪除既有 score／letter |

## 6. API 與 UI

| 介面 | 內容 |
|---|---|
| Job viewmodel | 新增 `group`：`{ group_id, canonical_job_id, members: [{ job_id, source, url, external_id }], duplicate_candidate_count }` |
| `GET /api/v1/jobs`、`GET /api/v1/queue` | 只回 canonical；`merged` 不出現在任何清單，也不導出 verdict |
| `GET /api/v1/duplicates` | `pending` 候選清單（雙方的職稱、公司、地區、來源、相似度、原因） |
| `POST /api/v1/duplicates/{id}/merge`、`/ignore` | 使用者裁決 |
| `POST /api/v1/jobs/{id}/unmerge` | 取消合併 |
| Side Panel | 目前職缺詳情列出「其他來源連結」，供使用者選擇投遞平台；系統頁新增「疑似重複」區塊，逐筆併排比較並提供合併／忽略 |

## 7. 落點

| 文件 | 更新內容 |
|---|---|
| `docs/PRD.md` | R2.3 擴充、新增 R2.8（跨來源合併）、R6.12（重複職缺呈現）、§3.1 `merged` 狀態、§7 B6 |
| `docs/design.md` | §5 關鍵技術決策、§6 狀態機摘要、§7 開發順序 |
| `docs/designs/design-schema.md` | §2 新表與 `group_id`、§3.1 `merged`、§4 去重規則、§5 store 介面 |
| `docs/designs/design-pipeline.md` | ingest／upsert 後的分群與合併時機、取件排除 `merged` |
| `docs/designs/design-api.md` | group viewmodel、duplicates route、unmerge |
| `docs/designs/design-extension.md` | 其他來源連結、疑似重複區塊 |
| `docs/tests/test-schema.md`、`test-pipeline.md`、`test-api.md`、`test-extension.md` | 正規化、合併、候選、取消合併、清單排除案例 |
| `docs/verify.md` | 跨來源合併的 e2e 案例 |

## 8. 待實作進度

- [x] store：schema v4 migration、正規化與 `dedupe_key`、合併／取消合併／候選裁決交易
- [x] pipeline：upsert 後的分群鉤點、判定回傳 canonical、取件排除 `merged`
- [x] api：group viewmodel、duplicates 與 unmerge route
- [x] extension：其他來源連結、疑似重複區塊
- [ ] V6 的驗收 harness 步驟（S42–S45）與實際 Chrome 人工 gate

## 9. 已知殘留限制

- 同公司同職稱但實際為不同團隊的職缺會被自動合併；此類在台灣平台上多為同一缺重複刊登，誤合併時由取消合併還原。
- 職稱同義詞對照表是人工維護的有限清單，只覆蓋常見中英對照；覆蓋不到者落入灰帶由使用者裁決，不會誤合併。
- 不比對 JD 內文相似度——那需要向量或 LLM，成本與誤判都高於收益。
- 同一平台上同公司的相似職缺（例如後端工程師與後端技術主管）不會被提出裁決。這是刻意的：同平台的兩筆本就是兩個開口，提出來只會製造必然否決的待審項。
