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

```
jobfinder calibrate
  前置：status_events 中 apply 軸 to_state='interview' 的 job 數
        ≥ 設定檔 calibration.min_interviews（預設 5）
  1. 取這些 job 的 JD 與評分
  2. Calibrator Agent 萃取共同特徵（技能組合、產業、規模、職稱模式、薪資帶）
  3. 輸出「preferences 調整建議」為 unified diff 風格文字 → stdout ＋ 存檔
  4. 使用者在 Profile editor 檢查並明確儲存；校準流程本身不寫入
```

特徵維度與建議格式的細節於 B6 設計時定案（PRD §10）。

## 8. CLI 介面

| 命令 | 行為 |
|---|---|
| `jobfinder profile lint` | 結構驗證 ＋ PII 檢核 |
| `jobfinder profile show` | 輸出載入後的 Profile 摘要（確認系統實際讀到什麼） |
| `jobfinder calibrate` | §7（B6） |

`profile lint` 與 `profile show` 預設讀取 `.local-dev/profile.yaml` 與 `.local-dev/pii-denylist.txt`；可分別以 `--profile`、`--denylist` 指定本機路徑。

## 9. 交付物

- `internal/profile/`：型別、strict codec、驗證、PII 檢核、canonical serialization、ETag／revision、原子檔案寫入、provider、求職條件導出與單元測試。
- `configs/profile.example.yaml`。

## 10. 待決

- 校準建議格式（PRD §10，B6 定案）。
