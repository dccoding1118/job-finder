# 模組設計 — profile（匿名 Profile）

對應需求：R1。Profile 是評分與求職信生成的唯一個人資料來源。

## 1. 職責邊界

- 定義 `profile.yaml` 格式；載入、結構驗證、PII 檢核。
- 提供 Agent prompt 所需的 Profile 渲染（YAML 原文餵入即可，不另做壓縮）。
- B6：反向校準建議產生（只產建議，不自動改寫）。
- 不負責：Profile 內容的撰寫（由使用者依履歷主檔人工＋agent 協助萃取，一次性作業）。

## 2. 檔案位置與敏感性

| 檔案 | 位置 | 版控 |
|---|---|---|
| `profile.yaml` | `.local-dev/profile.yaml`（或設定檔指定路徑） | **否**（含期望薪資等敏感值） |
| `profile.example.yaml` | repo `configs/` | 是（去敏感示例，欄位齊全） |
| `pii-denylist.txt` | `.local-dev/`（每行一個禁詞：姓名、Email、電話、校名、公司名…） | **否** |

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

- **結構驗證**：載入時檢查必填欄位、enum 合法值、`skills` 三級不重複，以及條件篩選字串不得為空白。
- **PII 檢核**（`jobfinder profile lint`）：
  1. denylist 逐詞掃描 profile 全文（不分大小寫）。
  2. 內建 pattern：Email regex、台灣手機/市話 regex、身分證字號 regex。
  3. 任一命中 ⇒ 非零退出並列出命中位置。
- 同一檢核函式供 letter 管線重用（R5.4：求職信過審前也跑一次）。

## 5. 反向校準（B6，R1.3）

```
jobfinder calibrate
  前置：status_events 中 apply 軸 to_state='interview' 的 job 數
        ≥ 設定檔 calibration.min_interviews（預設 5）
  1. 取這些 job 的 JD 與評分
  2. Calibrator Agent 萃取共同特徵（技能組合、產業、規模、職稱模式、薪資帶）
  3. 輸出「preferences 調整建議」為 unified diff 風格文字 → stdout ＋ 存檔
  4. 使用者人工編輯 profile.yaml 套用（系統不寫入）
```

特徵維度與建議格式的細節於 B6 設計時定案（PRD §10）。

## 6. CLI 介面

| 命令 | 行為 |
|---|---|
| `jobfinder profile lint` | 結構驗證 ＋ PII 檢核 |
| `jobfinder profile show` | 輸出載入後的 Profile 摘要（確認系統實際讀到什麼） |
| `jobfinder calibrate` | §5（B6） |

`profile lint` 與 `profile show` 預設讀取 `.local-dev/profile.yaml` 與 `.local-dev/pii-denylist.txt`；可分別以 `--profile`、`--denylist` 指定本機路徑。

## 7. 交付物

- `internal/profile/`：型別、載入/驗證、PII 檢核、求職條件導出與單元測試（fixture 含乾淨與含 PII 的樣本）。
- `configs/profile.example.yaml`。

## 8. 待決

- 校準建議格式（PRD §10，B6 定案）。
