# 模組設計 — profile（匿名 Profile）

對應需求：R1。Profile 是評分與求職信生成的唯一個人資料來源。

## 1. 職責邊界

- 定義 JSON／YAML 共用的 Profile schema；strict decode、結構驗證、PII 檢核與安全欄位 issue。
- 產生 canonical YAML、檔案 ETag 與內容定址的 `profile_revision`，並以 owner-only 權限原子寫入。
- 提供同步化 runtime provider；以 immutable snapshot 向 filter、score、letter 與 API 提供 Profile、canonical YAML、ETag 與 revision。
- 提供 Agent prompt 所需的 canonical YAML，不直接重用未驗證的檔案 bytes。
- B6：反向校準建議產生（只產建議，不自動改寫）。
- 不負責：替使用者決定或自動改寫 Profile 內容、保存歷史版本、Job 重新處理與狀態轉換。

## 2. 檔案位置與敏感性

| 檔案 | 位置 | 版控 |
|---|---|---|
| `profile.yaml` | `.local-dev/profile.yaml`（或設定檔指定路徑） | **否**（含期望薪資等敏感值） |
| `profile.example.yaml` | repo `configs/` | 是（去敏感示例，欄位齊全） |
| `pii-denylist.txt` | `.local-dev/`（每行一個禁詞：姓名、Email、電話、校名、公司名…） | **否** |

`profile.path` 不存在是合法的 setup 狀態。新檔、同目錄暫存檔與替換後檔案皆使用 `0600`；模組不得擴大父目錄權限。

## 3. `profile.yaml` 格式

| 區段 | 欄位 | 型別 | 說明 |
|---|---|---|---|
| `summary` | — | string | 2–3 句去識別化定位敘述（如「十年資雲端架構與後端經驗…」） |
| `years_of_experience` | — | int | 總年資 |
| `education` | `degree` | enum：`bachelor`/`master`/`phd` | 無校名 |
| | `field` | string | 領域（如資訊工程） |
| `experiences[]` | `role` | string | 職稱/角色 |
| | `org_type` | string | 組織類型描述（「雲端代理商」「國際商業銀行」），**禁公司名** |
| | `years` | number | 該段年資 |
| | `summary` | string | 職責摘要 |
| | `achievements[]` | string | 量化成就條列 |
| | `skills[]` | string | 該段用到的技術 |
| `skills` | `expert[]` / `proficient[]` / `familiar[]` | string[] | 三級技能清單（防幻覺白名單的來源） |
| `certifications[]` | `name` / `status` | string | 證照與現況（如已過期） |
| `preferences` | `salary_min` / `salary_target` | int | 月薪下限 / 目標（敏感） |
| | `locations[]` | string[] | 可接受地點 |
| | `remote` | enum：`required`/`preferred`/`ok` | 遠端意願 |
| | `directions[]` | `{key, title, keywords[]}` | P1/P2/P3 方向與關鍵字 |
| | `industry_avoid[]` | string[] | 排除產業/型態 |
| | `screening.exclude_title_keywords[]` | string[] | 職稱含任一字串即排除 |
| | `screening.exclude_description_keywords[]` | string[] | 工作內容含任一字串即排除 |
| | `screening.require_any_keywords[]` | string[] | 職稱與工作內容都未命中任一字串即排除；空陣列時不額外排除 |
| | `screening.exclude_companies[]` | string[] | 公司名稱含任一字串即排除 |
| `honesty_bounds[]` | — | string[] | 誠實邊界條列（如「k8s 為規劃配置層級、無平台維運」），Drafter/Reviewer prompt 皆引用 |

## 4. 驗證與 PII 檢核

- **結構驗證**：JSON 與 YAML 使用同一 schema；拒絕 unknown field，並檢查必填欄位、enum 合法值、`skills` 三級不重複，以及條件篩選字串不得為空白。
- **PII 檢核**（`jobfinder profile lint`）：
  1. denylist 逐詞掃描 profile 全文（不分大小寫）。
  2. 內建 pattern：Email regex、台灣手機/市話 regex、身分證字號 regex。
  3. 任一命中 ⇒ 非零退出；API 僅回安全錯誤代碼、欄位路徑與訊息，不回 denylist 值或完整輸入。
- 同一檢核函式供 letter 管線重用（R5.4：求職信過審前也跑一次）。

## 5. 序列化、ETag 與 revision

| 項目 | 契約 |
|---|---|
| canonical YAML | 已知欄位完整輸出、陣列順序保留；註解、空白與人工欄位排序不屬契約。 |
| ETag | 精確檔案 bytes 的識別；檔案不存在時為 `"missing"`，供 `If-Match` 防止覆蓋外部修改。 |
| `profile_revision` | `sha256:<hex>`；輸入為固定 schema 版本標記與 canonical JSON，物件鍵與數值格式固定、陣列順序保留。 |
| 冪等 | 相同結構化內容得到相同 revision；只變更 YAML 註解或排版可改變 ETag，但不得改變工作 revision。 |

儲存流程依序比對 ETag、驗證 JSON／PII、產生 canonical bytes、寫入同目錄 `0600` 暫存檔、`fsync`、rename 原子替換，成功後切換 active snapshot。任一步失敗不得改變 active snapshot；儲存不修改既有 Job revision，也不觸發重新處理。手動 activation 見 [design-pipeline](design-pipeline.md)。

## 6. Runtime provider 與狀態

| 狀態 | provider 行為 |
|---|---|
| `missing` | 無 snapshot；Profile API 可建立第一份 Profile，處理型入口與 worker 暫停。 |
| `invalid` | 檔案存在但無法解析或驗證；只提供安全 issues，不以預設值或舊 snapshot 繼續處理。 |
| `ready` | 提供 immutable snapshot；每個工作開始時取得一次，工作途中不得換版。 |
| `degraded` | runtime snapshot 無法安全提供；暫停 worker，等待人工修復。 |

API 讀取回傳 snapshot 的結構化副本，不暴露內部可變物件。語意相同的重複儲存不替換工作中的 snapshot，也不重新入隊。

## 7. 反向校準（B6，R1.3）

校準是**只讀 Profile、只產建議**的離線分析：由已取得面試的職缺反推「什麼樣的 JD 真的會回應我」，把結論表達為 `preferences` 的調整建議 diff。它不改寫任何檔案，也不影響任何 Job 的狀態。

### 7.1 觸發與樣本

| 項目 | 規則 |
|---|---|
| 入口 | `jobfinder calibrate`（CLI only；MVP 不開 API 與 UI 入口） |
| 前置 | `status_events` 中 `axis='apply' AND to_state='interview'` 的相異 job 數 ≥ `calibration.min_interviews`（預設 5）；未達門檻拒絕執行並列印目前筆數，不呼叫 Agent |
| 成功樣本 | 上述 job 的 JD 全文、職稱、公司產業摘要、地區、薪資區間與現行五維分數 |
| 對照樣本 | `applied` 之後轉入 `ghosted` 的 job，取樣上限與成功樣本同數；不足時可為空，Agent 需在無對照下仍只根據成功樣本作答 |

樣本一律取 canonical Job（alias 為同一職缺，重複計入會扭曲特徵權重）。JD 與公司名是公開資訊，不屬 PII；Profile 只送 `preferences` 區段，不送經歷、成就與 `summary`。

### 7.2 特徵萃取維度

Calibrator 的萃取維度與評分五維對齊，讓建議可直接對應到分數的落差：

| 維度 | 萃取內容 | 可影響的 `preferences` 欄位 |
|---|---|---|
| 技能組合 | 成功樣本共同出現、且 Profile 方向關鍵字未涵蓋的技術詞 | `directions[].keywords` |
| 領域與場景 | 產業別、系統型態、雲平台生態的集中傾向 | `directions[].title`、`industry_avoid` |
| 資歷與職級 | 職稱的層級用語模式（如偏 lead／偏 IC） | `screening.exclude_title_keywords`、`screening.require_any_keywords` |
| 工作條件 | 薪資帶、地區、遠端型態的實際分布 | `salary_min`、`salary_target`、`locations`、`remote` |
| 方向命中 | 哪個方向（P1／P2／P3）實際帶來面試、哪個沒有 | `directions[]` 的順序與關鍵字 |

### 7.3 輸出與防線

Agent 回 JSON 建議清單（契約見 [design-agents](design-agents.md) §4.4），程式端依序把關：

1. **JSON 契約驗證**：欄位、型別、`action` 列舉、`confidence` 列舉；不合法即拒絕整份建議。
2. **欄位白名單**：`field` 必須落在 `preferences.*`（上表所列欄位）；指向 `skills`、`experiences`、`summary`、`honesty_bounds` 等任何其他路徑一律拒絕——校準不得改寫事實性履歷內容，只能調整求職條件。
3. **套用於副本**：把建議套到 Profile 的記憶體副本，產生 canonical YAML。
4. **PII 檢核**：對套用後的副本跑與 §4 相同的檢核；命中即拒絕整份建議並回安全錯誤（不回命中值）。
5. **產出 diff**：原 canonical YAML 與副本的 unified diff → stdout，同時寫入 Profile 同目錄的 `profile.calibration-<RFC3339>.diff`（`0600`，版控外）。

磁碟上的 `profile.yaml` 與 active snapshot **在任何情況下都不被此流程修改**。使用者閱讀 diff 後，自行於 Profile editor 逐項套用並明確儲存——套用與否、套用哪幾條，都是使用者的決定（PRD 核心原則 Human-in-the-Loop）。

每次呼叫（含被拒絕者）寫入 `agent_calls`，`role='calibrator'`、`job_id` 為 NULL。

## 8. CLI 介面

| 命令 | 行為 |
|---|---|
| `jobfinder profile lint` | 結構驗證 ＋ PII 檢核 |
| `jobfinder profile show` | 輸出載入後的 Profile 摘要（確認系統實際讀到什麼） |
| `jobfinder calibrate` | §7；未達門檻或建議未通過防線時非零退出，不產生 diff 檔 |

`profile lint` 與 `profile show` 預設讀取 `.local-dev/profile.yaml` 與 `.local-dev/pii-denylist.txt`；可分別以 `--profile`、`--denylist` 指定本機路徑。

## 9. 交付物

- `internal/profile/`：型別、strict codec、驗證、PII 檢核、canonical serialization、ETag／revision、原子檔案寫入、provider、求職條件導出、校準建議套用與 diff 產生，以及單元測試。
- `configs/profile.example.yaml`。

## 10. 待決

（無。）
