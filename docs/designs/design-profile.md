# 模組設計 — profile（匿名 Profile）

對應需求：R1。Profile 是評分與求職信生成的唯一個人資料來源。

## 1. 職責邊界

- 定義 JSON／YAML 共用的 Profile schema；strict decode、結構驗證、PII 檢核與安全欄位 issue。
- 產生 canonical YAML、檔案 ETag、內容定址的 `filter_revision` 與 `score_revision`，並以 owner-only 權限原子寫入。
- 從 `experiences[]` 計算 `derived` 加總（總年資、管理年資、各產業年資），供篩選關做數值比較。
- 提供同步化 runtime provider；以 immutable snapshot 向 filter、score、letter 與 API 提供 Profile、canonical YAML、ETag 與兩個 revision。
- 提供各關所需的 Profile 子集：篩選關取 `requirements`／`qualifications`／`experiences` 的硬規則欄位與 `derived`；評分關取 `intents` 與 `qualifications` 的三個清單，**不含**履歷敘事；letter 取 `experiences` 與 `honesty_bounds`。
- 提供 Agent prompt 所需的 canonical YAML，不直接重用未驗證的檔案 bytes。
- 不負責：替使用者決定或自動改寫 Profile 內容、保存歷史版本、Job 重新處理與狀態轉換。

## 2. 檔案位置與敏感性

| 檔案 | 位置 | 版控 |
|---|---|---|
| `profile.yaml` | `.local-dev/profile.yaml`（或設定檔指定路徑） | **否**（含期望薪資等敏感值） |
| `profile.example.yaml` | repo `configs/` | 是（去敏感示例，欄位齊全） |
| `pii-denylist.txt` | `.local-dev/`（每行一個禁詞：姓名、Email、電話、校名、公司名…） | **否** |

`profile.path` 不存在是合法的 setup 狀態。新檔、同目錄暫存檔與替換後檔案皆使用 `0600`；模組不得擴大父目錄權限。

## 3. `profile.yaml` 格式（schema v6）

區段依用途劃分，表單與 YAML 的順序為 `search` → `requirements` → `intents` → `experiences` → `qualifications` → `honesty_bounds`，另有程式物化的 `derived`。

### 3.1 `search`（只用於搜尋）

| 欄位 | 型別 | 說明 |
|---|---|---|
| `directions[]` | `{key, title, keywords[]}` | P1/P2/P3 方向與搜尋關鍵字 |

`search` 不參與篩選、不參與評分、不進任何 revision。地區不在此區段：地區只有 `requirements.locations[]` 一份，見 §3.2。

### 3.2 `requirements`（硬規則）

| 欄位 | 型別 | 用途 |
|---|---|---|
| `salary_min` | int | JD 薪資上限低於此 ⇒ 不適合；JD 未揭露 ⇒ 資訊不足（敏感值） |
| `locations[]` | string[]，受控列舉 | 可接受的地區，空清單代表不限；同時決定要找哪裡的職缺 |
| `remote` | enum：`required`/`preferred`/`acceptable`/`rejected` | 遠端意願 |
| `employment_types[]` | string[]，受控列舉 | 可接受的工作型態，空清單代表不限 |
| `industry_avoid[]` | string[] | 排除產業 |
| `exclude_title_keywords[]` | string[] | 職稱含任一字串即排除 |
| `exclude_description_keywords[]` | string[] | 工作內容含任一字串即排除 |
| `exclude_companies[]` | string[] | 公司名稱含任一字串即排除 |

`remote` 同時是篩選條件與 `benefit_fit` 的加分依據，故兩組 revision 都納入。

`locations[]` 是唯一的一份地區設定。曾另有一份「搜尋地區」試圖搜得更廣再篩掉，但半被動來源的列表頁只有前幾頁，多搜出來的職缺一律被本欄硬規則淘汰，只會把可通勤地區的職缺擠出視野；兩份設定的交集才是實際結果，所以只留這一份。

地區與工作型態同為受控列舉：存的是地區鍵，每個鍵帶一組別名（簡體、繁體、英文），篩選以別名對 JD 所述地點做子字串比對，故選「台北市」也能命中寫「臺北市」或 `Taipei` 的 JD。**每個鍵只命中它自己所指的地點，沒有任何鍵代表其他鍵**：選「台北市」不會命中只寫「台灣」的 JD。鍵涵蓋台灣直轄市與各縣市，另有兩個非縣市的鍵：

| 鍵 | 顯示 | 含意 |
|---|---|---|
| `taiwan` | 台灣 | 只陳述國別、未指出縣市的地點；JD 若已寫出任一縣市即不由本鍵命中 |
| `overseas` | 海外 | 台灣以外，不分國家 |

「不限台灣任何地點」因此是**一組鍵**而非一個鍵：`overseas` 以外的全部鍵。編輯器以「＋ 全台」快捷鈕一次填入這組鍵（見 [design-extension](design-extension.md) §3），設定檔則逐項列出。

「新竹」「嘉義」未帶市／縣時同為市與縣兩鍵的別名：用詞本身無從判斷，而含糊的用詞不得構成淘汰。

`employment_types[]` 存的是型態鍵，不是 JD 的字面用詞；每個鍵帶一組別名，篩選以別名比對 JD 全文，故選「全職」也能命中寫「正職」的 JD。編輯器只提供這四個選項，不接受自由輸入——自由輸入的用詞比對不到任何 JD，只會讓條件恆為「資訊不足」。

| 鍵 | 顯示 | 比對別名 |
|---|---|---|
| `full_time` | 全職 | 全職、正職、full-time、full time、fulltime |
| `part_time` | 兼職 | 兼職、工讀、part-time、part time、parttime |
| `contract` | 約聘 | 約聘、約僱、派遣、契約、contract |
| `internship` | 實習 | 實習、internship、intern |

「JD 有無揭露型態」以全部別名的聯集判斷：JD 完全未出現任一別名即 `unknown`，出現但不屬使用者所選型態即 `fail`。

### 3.3 `intents`（軟規則）

| 欄位 | 型別 | 用途 |
|---|---|---|
| `salary_target` | int | `benefit_fit`：JD 薪資高於此則加分（敏感值） |
| `content_likes[]` | string[] | `content_fit` 加分依據，條列敘事 |
| `content_dislikes[]` | string[] | `content_fit` 扣分依據，條列敘事 |
| `industry_interests[]` | string[] | `industry_fit`：對未來發展的期許 |

`industry_interests` 與 `requirements.industry_avoid` 語意不同：後者是硬排除，前者是領域發展偏好，不符只是分數較低。

### 3.4 `experiences[]`（一份工作一列）

| 欄位 | 型別 | 必填 | 用途 |
|---|---|:---:|---|
| `industry` | string | ✔ | 硬規則：產業經驗；建議取自受控清單，可自訂補充 |
| `years` | float | ✔ | 硬規則：年資加總 |
| `is_management` | bool | ✔ | 硬規則：管理年資加總。使用者勾選，不由 LLM 從 `role` 文字推測 |
| `exclude_from_totals` | bool | | 實習或非相關經歷不計入加總 |
| `skills[]` | string[] | | 硬規則：必備技能來源；guard 白名單來源。只填名稱，熟練度不在此標 |
| `org_type` | string | | Drafter 取材；組織類型描述（「雲端代理商」「國際商業銀行」），**禁公司名** |
| `role` | string | | Drafter 取材 |
| `achievements[]` | string[] | | Drafter 取材，需帶量化數據 |

一筆經歷同時服務篩選關與寫信。

### 3.5 `qualifications`

| 欄位 | 型別 | 用途 |
|---|---|---|
| `education[]` | `{ level, field, status }` | 硬規則：學歷比對（無校名）；`level` 為 `bachelor`／`master`／`phd`，`status` 為 `graduated`（畢業）／`attended`（肄業） |
| `skills[]` | `{ name, level }` | 硬規則：必備技能；軟規則：`bonus_fit`。`level` 為 `expert`／`proficient`／`familiar` |
| `certifications[]` | `{ name, status }` | 硬規則：必要證照；軟規則：`bonus_fit`。`status` 為 `active`（有效）／`expired`（過期）／`renewing`（過期重考中） |
| `languages[]` | `{ name, level }` | 硬規則：必要語言；軟規則：`bonus_fit`。`level` 為 `native`（母語）／`fluent`（流利）／`intermediate`（中等）／`basic`（基礎） |

受控列舉一律存鍵、顯示標籤：讀取時舊值（中文標籤或其他寫法）正規化為鍵，鍵以外的值拒絕載入；編輯器一律以下拉選單呈現，不接受自由輸入。`employment_types` 的別名同時作為 JD 比對用詞（§3.2），其餘列舉的別名只用於正規化舊值。

`skills[]` 是技能總表，熟練度只在此填一次；其初始內容為 `experiences[].skills[]` 的聯集加上工作外取得的技能（表單帶入行為見 [design-extension](design-extension.md) §3）。

### 3.6 `honesty_bounds[]`

條列字串（如「k8s 為規劃配置層級、無平台維運」），Drafter 與 Reviewer 的防幻覺依據，不進任何 revision。

### 3.7 `derived`（程式物化，不可編輯）

Profile 儲存時由 Go 從 `experiences[]` 計算並寫入，供篩選關做數值比較：

| 欄位 | 計算 |
|---|---|
| `total_years` | `exclude_from_totals` 為否的 `years` 加總 |
| `management_years` | 同上且 `is_management` 為真的 `years` 加總 |
| `industry_years` | 以 `industry` 分組的 `years` 加總 |

年資只由經歷列表導出，不另設使用者填寫的總年資欄位，避免兩處不一致。

## 4. 驗證與 PII 檢核

- **結構驗證**：JSON 與 YAML 使用同一 schema；拒絕 unknown field 與使用者送入的 `derived`，並檢查必填欄位、enum 合法值、`qualifications.skills[]` 的 `name` 不重複，以及條件字串不得為空白。`years` 不得為負；`experiences[]` 至少一筆。
- **PII 檢核**（`jobfinder profile lint`）：
  1. denylist 逐詞掃描 profile 全文（不分大小寫）。
  2. 內建 pattern：Email regex、台灣手機/市話 regex、身分證字號 regex。
  3. 任一命中 ⇒ 非零退出；API 僅回安全錯誤代碼、欄位路徑與訊息，不回 denylist 值或完整輸入。
- 同一檢核函式供 letter 管線重用（R5.4：求職信過審前也跑一次）。

## 5. 序列化、ETag 與雙 revision

| 項目 | 契約 |
|---|---|
| canonical YAML | 已知欄位完整輸出、陣列順序保留；註解、空白與人工欄位排序不屬契約。 |
| ETag | 精確檔案 bytes 的識別；檔案不存在時為 `"missing"`，供 `If-Match` 防止覆蓋外部修改。 |
| `filter_revision`／`score_revision` | 各為 `sha256:<hex>`；輸入為固定 schema 版本標記與該關涵蓋欄位的 canonical JSON，物件鍵與數值格式固定、陣列順序保留。 |
| 冪等 | 相同結構化內容得到相同 revision；只變更 YAML 註解、排版，或只改不進 hash 的欄位，可改變 ETag 但不得改變任一 revision。 |

各 revision 涵蓋的欄位與變更後的重跑範圍：

| revision | 納入 hash 的欄位 | 變更後的重跑範圍 |
|---|---|---|
| `filter_revision` | `requirements` 全部；`qualifications` 全部；`experiences[]` 的 `industry`／`years`／`is_management`／`exclude_from_totals`／`skills[]` | 全部重篩，通過者再重評 |
| `score_revision` | `intents` 全部；`requirements.remote`／`requirements.locations[]`；`qualifications` 的 `skills[]`／`certifications[]`／`languages[]` | 只重評，不重篩 |

不進任何 hash：`search` 全部、`experiences[]` 的 `org_type`／`role`／`achievements[]`、`honesty_bounds`、`derived`（由來源欄位決定）。

`requirements.remote`、`requirements.locations[]` 與 `qualifications` 的三個清單同時進兩組 hash——它們既是篩選條件也是加分依據，改動時兩關都要重跑；這是欄位跨關共用的必然代價。ETag 仍以整份 canonical YAML 計算，與 revision 無關。

儲存流程依序比對 ETag、驗證 JSON／PII、產生 canonical bytes、寫入同目錄 `0600` 暫存檔、`fsync`、rename 原子替換，成功後切換 active snapshot。任一步失敗不得改變 active snapshot；儲存不修改既有 Job revision，也不觸發重新處理。手動 activation 見 [design-pipeline](design-pipeline.md)。

## 6. Runtime provider 與狀態

| 狀態 | provider 行為 |
|---|---|
| `missing` | 無 snapshot；Profile API 可建立第一份 Profile，處理型入口與 worker 暫停。 |
| `invalid` | 檔案存在但無法解析或驗證；只提供安全 issues，不以預設值或舊 snapshot 繼續處理。 |
| `ready` | 提供 immutable snapshot；每個工作開始時取得一次，工作途中不得換版。 |
| `degraded` | runtime snapshot 無法安全提供；暫停 worker，等待人工修復。 |

API 讀取回傳 snapshot 的結構化副本，不暴露內部可變物件。語意相同的重複儲存不替換工作中的 snapshot，也不重新入隊。

## 7. 反向校準（S1，R1.3；尚未實作）

校準是**只讀 Profile、只產建議**的離線分析：由已取得面試的職缺反推「什麼樣的 JD 真的會回應我」，把結論表達為 `search`／`requirements`／`intents` 的調整建議 diff。它不改寫任何檔案，也不影響任何 Job 的狀態。

### 7.1 觸發與樣本

| 項目 | 規則 |
|---|---|
| 入口 | Side Panel 系統頁：顯示 `interview` 累計與門檻、觸發校準、以逐條建議呈現 diff 供勾選套用至 Profile 編輯器草稿；不提供 CLI 指令 |
| 前置 | `status_events` 中 `axis='apply' AND to_state='interview'` 的相異 job 數 ≥ `calibration.min_interviews`（預設 5）；未達門檻拒絕執行並列印目前筆數，不呼叫 Agent |
| 成功樣本 | 上述 job 的 JD 全文、職稱、公司產業摘要、地區、薪資區間與現行四維分數 |
| 對照樣本 | `applied` 之後轉入 `ghosted` 的 job，取樣上限與成功樣本同數；不足時可為空，Agent 需在無對照下仍只根據成功樣本作答 |

樣本一律取 canonical Job（alias 為同一職缺，重複計入會扭曲特徵權重）。JD 與公司名是公開資訊，不屬 PII；Profile 只送 `search`、`requirements` 與 `intents`，不送 `experiences`、`qualifications` 與 `honesty_bounds`。

### 7.2 特徵萃取維度

Calibrator 的萃取維度與求職條件的三個區段對齊，讓建議可直接對應到分數的落差：

| 維度 | 萃取內容 | 可影響的欄位 |
|---|---|---|
| 技能組合 | 成功樣本共同出現、且 Profile 方向關鍵字未涵蓋的技術詞 | `search.directions[].keywords` |
| 領域與場景 | 產業別、系統型態、雲平台生態的集中傾向 | `search.directions[].title`、`intents.industry_interests`、`requirements.industry_avoid` |
| 工作內容 | 成功樣本共同的工作內容特徵，與被 ghosted 樣本的差異 | `intents.content_likes`、`intents.content_dislikes` |
| 資歷與職級 | 職稱的層級用語模式（如偏 lead／偏 IC） | `requirements.exclude_title_keywords` |
| 工作條件 | 薪資帶、地區、遠端型態的實際分布 | `requirements.salary_min`／`locations`／`remote`、`intents.salary_target` |
| 方向命中 | 哪個方向（P1／P2／P3）實際帶來面試、哪個沒有 | `search.directions[]` 的順序與關鍵字 |

### 7.3 輸出與防線

Agent 回 JSON 建議清單（契約見 [design-agents](design-agents.md) §3.5），程式端依序把關：

1. **JSON 契約驗證**：欄位、型別、`action` 列舉、`confidence` 列舉；不合法即拒絕整份建議。
2. **欄位白名單**：`field` 必須落在 `search.*`、`requirements.*` 或 `intents.*`（上表所列欄位）；指向 `experiences`、`qualifications`、`honesty_bounds` 或 `derived` 一律拒絕——校準不得改寫事實性履歷內容，只能調整求職條件。
3. **套用於副本**：把建議套到 Profile 的記憶體副本，產生 canonical YAML。
4. **PII 檢核**：對套用後的副本跑與 §4 相同的檢核；命中即拒絕整份建議並回安全錯誤（不回命中值）。
5. **產出 diff**：原 canonical YAML 與副本的 unified diff，逐條建議附證據與信心度回給前端；不寫入任何檔案。

磁碟上的 `profile.yaml` 與 active snapshot **在任何情況下都不被此流程修改**。使用者閱讀 diff 後，於 Profile editor 逐項套用並明確儲存——套用與否、套用哪幾條，都是使用者的決定（PRD 核心原則 Human-in-the-Loop）。

每次呼叫（含被拒絕者）寫入 `agent_calls`，`role='calibrator'`、`job_id` 為 NULL。

## 8. CLI 介面

| 命令 | 行為 |
|---|---|
| `jobfinder profile lint` | 結構驗證 ＋ PII 檢核 |
| `jobfinder profile show` | 輸出載入後的 Profile 摘要（確認系統實際讀到什麼） |

`profile lint` 與 `profile show` 預設讀取 `.local-dev/profile.yaml` 與 `.local-dev/pii-denylist.txt`；可分別以 `--profile`、`--denylist` 指定本機路徑。

## 9. 交付物

- `internal/profile/`：型別、strict codec、驗證、PII 檢核、canonical serialization、ETag／雙 revision、`derived` 計算、schema migration（v5 的 `nationwide` 展開為 `overseas` 以外的全部地區鍵；v4 的 `search.locations` 併入 `requirements.locations`；pre-v4 整份對映）、原子檔案寫入、provider、各關 Profile 子集導出、校準建議套用與 diff 產生，以及單元測試。
- `configs/profile.example.yaml`。

## 10. 待決

（無。）
