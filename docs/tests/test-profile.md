# 測試規格 — profile（`internal/profile`）

對應 [profile 模組設計](../designs/design-profile.md) 與 PRD R1。測試使用暫存目錄中的合成 `profile.yaml` 與 `pii-denylist.txt`；不得讀取或輸出使用者的實際 Profile。

## 1. 程式面閘門

| 閘門 | 指令 | 通過條件 |
|---|---|---|
| 格式化 | `mise run fmt` | gofumpt 無待格式化檔案 |
| 靜態檢查 | `mise run lint` | golangci-lint 無 error |
| 單元測試 | `mise run test` | 本文件的所有 PT-* 案例通過 |

## 2. 測試資料與共通條件

| 項目 | 規格 |
|---|---|
| Profile fixture | 每個案例在獨立暫存目錄建立 YAML；內容為合成角色、組織類型、技能與成就，不含真實履歷或可識別資料 |
| 合法基準 | 使用六個區段完整、enum 合法、技能不重複且不含 PII 的 Profile 作為基準 fixture |
| denylist fixture | 每個禁詞均為合成字串；逐詞案例另覆蓋大小寫差異 |
| PII 偵測輸入 | Email、電話與身分證字號 pattern 於測試執行時動態組成；不保存為 fixture、log 或版控檔案，且不得使用真實個資 |
| 錯誤驗證 | 失敗時斷言回傳錯誤包含欄位或命中位置，且不回傳可供後續 Agent 使用的 Profile |
| JSON／檔案 fixture | 合法與 unknown-field JSON、語意相同但排版不同的 YAML、暫存檔權限與可注入檔案失敗；不得讀取日常 `profile.yaml` |

## 3. 單元測試案例

### 3.1 載入與結構驗證

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-01 | 載入六個區段皆完整的合法 Profile | 成功取得完整 Profile；各區段、方向與誠實邊界可供後續 prompt 使用 |
| PT-02 | YAML 語法錯誤，或缺少必填區段與欄位（含 `experiences` 為空） | 拒絕載入；錯誤指出 YAML 或缺少的欄位 |
| PT-03 | `qualifications.education[].level`、`qualifications.skills[].level` 或 `requirements.remote` 使用未定義 enum | 拒絕載入；錯誤指出非法欄位值（`remote` 四值以外一律拒絕） |
| PT-19 | 受控列舉欄位（`requirements.employment_types[]`、`education[].status`、`certifications[].status`、`languages[].level`）填入鍵、別名（如「正職」「畢業」「中等」）與列舉外的值 | 鍵與別名皆正規化為鍵，`employment_types` 另去重；列舉外的值拒絕載入且錯誤列出合法鍵 |
| PT-34 | `requirements.locations[]` 填入鍵、簡繁英別名（`臺北`／`Taipei`／`全台`）、`taiwan`／`overseas` 與列舉外的地名 | 鍵與別名皆正規化為鍵並去重（`全台` 正規化為 `taiwan`）；列舉外的地名拒絕載入且錯誤列出合法鍵 |
| PT-36 | `requirements.locations[]` 仍帶 v5 的 `nationwide` 的舊檔 | 載入時展開為 `overseas` 以外的全部地區鍵（含 `taiwan`）並去重，原有其他項與 `overseas` 保留；不含 `nationwide` 的檔案不被改寫 |
| PT-37 | `LocationTerms` 與 `TaiwanLocationKeys` 的涵蓋範圍 | 每個鍵只展開自己的別名，沒有任一鍵展開為其他鍵；`TaiwanLocationKeys` 為 `overseas` 以外的全部鍵且含 `taiwan` |
| PT-35 | 仍帶 `search.locations` 的舊檔 | 併入 `requirements.locations[]`（原有項在前、去重）後照常載入；`search` 不再有地區欄位 |
| PT-04 | 年資或薪資欄位不是數值，或年資為負值 | 拒絕載入；錯誤指出數值欄位 |
| PT-05 | `search.directions` 缺少 `key`、`title` 或 `keywords` | 拒絕載入；錯誤指出不完整的方向項目 |
| PT-06 | `qualifications.skills[]` 出現同名技能兩筆 | 拒絕載入；錯誤列出重複技能 |
| PT-07 | `requirements` 的任一排除關鍵字或公司排除項為空白 | 拒絕載入；錯誤指出無效的求職條件項目 |
| PT-08 | 載入含硬性條件、軟性偏好、資格與經歷的 Profile | 成功取得可供搜尋、篩選與評分使用的各區段子集；Profile 是唯一來源 |
| PT-09 | 輸入含 `derived` 區段 | 拒絕載入；`derived` 由程式物化，不接受使用者輸入 |

### 3.1.1 `derived` 加總

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-15 | 多筆經歷，部分 `exclude_from_totals` 為真 | `total_years` 只加總未排除者 |
| PT-16 | 部分經歷 `is_management` 為真且未排除 | `management_years` 只加總符合兩條件者，不由 `role` 文字推測 |
| PT-17 | 同一 `industry` 分散在多筆經歷 | `industry_years` 依 key 分組加總；被排除的經歷不計入 |
| PT-18 | 儲存後讀回 Profile | `derived` 與來源欄位一致；修改任一 `years` 後重新儲存即重算 |

### 3.2 PII 檢核

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-10 | 對合法基準 Profile 執行 denylist 與內建 pattern 檢核 | 通過，沒有命中項目 |
| PT-11 | Profile 含 denylist 禁詞，且字母大小寫與 denylist 不同 | 拒絕；安全 issue 指向欄位位置但不回禁詞本身，大小寫不影響偵測 |
| PT-12 | Profile 分別含 Email、台灣手機、台灣市話與身分證字號格式 | 每一種輸入均被拒絕；回報對應的 pattern 命中位置 |
| PT-13 | Profile 同時含多個 denylist 與 pattern 命中 | 一次回報全部安全 issue，供使用者完整修正；不回完整輸入或 denylist 值 |
| PT-14 | 對合成求職信文字呼叫共用 PII 檢核函式 | 行為與 Profile 檢核一致；乾淨文字通過，含禁詞或內建 pattern 的文字被拒絕 |

### 3.3 CLI 介面

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-20 | 以預設路徑或 `--profile`、`--denylist` 指定合法 Profile 與 denylist，執行 `jobfinder profile lint` | exit 0；stdout 明確表示結構與 PII 檢核通過 |
| PT-21 | 對結構非法或含 PII 的 Profile 執行 `jobfinder profile lint` | 非零 exit；stderr 顯示驗證錯誤或全部 PII 命中項目 |
| PT-22 | 以預設路徑或 `--profile` 指定合法 Profile，執行 `jobfinder profile show` | exit 0；輸出載入後的 Profile 摘要，包含物化年資、學位領域、技能熟練度、硬性條件與求職方向，足以確認讀取的檔案內容 |
| PT-23 | 設定檔指向不存在或無法解析的 Profile，執行 `profile lint` 或 `profile show` | 非零 exit；stderr 說明檔案讀取或解析失敗 |

### 3.4 反向校準（S1，尚未實作）

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-30 | `interview` 事件少於 `calibration.min_interviews` | 拒絕執行並回報目前筆數與門檻；不呼叫 Agent、不改寫 Profile |
| PT-31 | 達門檻的合成 Job、Score 與 `interview` 事件 | Calibrator 輸入只含 `search`／`requirements`／`intents` 與樣本，不含經歷、成就與資格；輸出建議套用於副本後產生 unified diff；`profile.yaml` 與 active snapshot 未變 |
| PT-32 | 成功樣本中含同一群組的多筆 alias | 只取 canonical 一筆計入樣本，重複刊登不重複加權 |
| PT-33 | 對照樣本（`ghosted`）不存在 | 仍可執行，僅以成功樣本作答；不因缺對照而失敗 |
| PT-34 | Calibrator 建議指向 `search`／`requirements`／`intents` 以外的欄位 | 拒絕整份建議並回安全錯誤；不產生 diff、不寫入 Profile |
| PT-35 | 套用建議後的副本命中 PII 檢核 | 拒絕整份建議並回安全錯誤（不回命中值）；磁碟檔與 snapshot 不變 |
| PT-36 | Calibrator 回空 `suggestions` | 正常結束並說明無足夠證據建議調整；不產生空 diff |

### 3.5 Canonical serialization、檔案與 provider

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-40 | JSON／YAML round-trip 與 unknown field | 已知欄位、enum、陣列順序完整；unknown field 拒絕且不無聲遺失 |
| PT-41 | 相同結構的不同 YAML 註解／排版 | 兩個 revision 皆相同、精確 bytes ETag 不同 |
| PT-42 | 重送相同內容 | 兩個 revision 均不變且回語意 no-op |
| PT-46 | 只修改 `intents` 任一欄位 | 只有 `score_revision` 改變，`filter_revision` 不變 |
| PT-47 | 只修改 `requirements` 的排除關鍵字、`salary_min`、`employment_types` 或 `industry_avoid` | 只有 `filter_revision` 改變 |
| PT-48 | 修改 `requirements.remote`、`requirements.locations[]` 或 `qualifications` 的 `skills`／`certifications`／`languages` | 兩個 revision 皆改變（跨關共用欄位） |
| PT-49 | 只修改 `search`、`experiences[].achievements`／`role`／`org_type` 或 `honesty_bounds` | 兩個 revision 皆不變；ETag 改變 |
| PT-43 | 新建與替換 Profile | 檔案與暫存檔為 `0600`，同目錄原子 rename；不擴大父目錄權限 |
| PT-44 | PII、schema、fsync／rename 失敗 | 不改磁碟檔或 active snapshot；issue 僅含安全 code、path、message |
| PT-45 | provider 的 missing／invalid／ready | 狀態正確；ready snapshot immutable；單一工作取得後不因後續替換而變動 |

## 4. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- 合法的匿名 Profile 可被載入並提供給後續 Agent prompt；結構不完整、非法 enum、技能重複與非法數值均在載入時被拒絕。
- `derived` 加總與雙 revision 的涵蓋範圍正確：改軟規則只動 `score_revision`、改搜尋或成就兩者皆不動。
- denylist 與內建 Email、電話、身分證字號 pattern 能檢出 Profile 與任意待檢文字中的 PII，並回報命中位置。
- `jobfinder profile lint` 與 `jobfinder profile show` 的輸出可由 Cobra 測試擷取；錯誤輸出不含完整 Profile 內容。
