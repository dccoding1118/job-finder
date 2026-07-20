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
| 合法基準 | 使用欄位完整、enum 合法、三層技能互斥且不含 PII 的 Profile 作為基準 fixture |
| denylist fixture | 每個禁詞均為合成字串；逐詞案例另覆蓋大小寫差異 |
| PII 偵測輸入 | Email、電話與身分證字號 pattern 於測試執行時動態組成；不保存為 fixture、log 或版控檔案，且不得使用真實個資 |
| 錯誤驗證 | 失敗時斷言回傳錯誤包含欄位或命中位置，且不回傳可供後續 Agent 使用的 Profile |

## 3. 單元測試案例

### 3.1 載入與結構驗證

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-01 | 載入欄位完整的合法 Profile | 成功取得完整 Profile；所有區段、方向與誠實邊界可供後續 prompt 使用 |
| PT-02 | YAML 語法錯誤，或缺少必填區段與欄位 | 拒絕載入；錯誤指出 YAML 或缺少的欄位 |
| PT-03 | `education.degree` 或 `preferences.remote` 使用未定義 enum | 拒絕載入；錯誤指出非法欄位值 |
| PT-04 | 年資或薪資欄位不是數值，或年資為負值 | 拒絕載入；錯誤指出數值欄位 |
| PT-05 | `preferences.directions` 缺少 `key`、`title` 或 `keywords` | 拒絕載入；錯誤指出不完整的方向項目 |
| PT-06 | 同一技能出現在 `expert`、`proficient` 或 `familiar` 的多個層級 | 拒絕載入；錯誤列出重複技能 |
| PT-07 | `preferences.screening` 的任一關鍵字或公司排除項為空白 | 拒絕載入；錯誤指出無效的求職條件項目 |
| PT-08 | 載入含薪資、地點、遠端、方向與完整條件篩選的 Profile | 成功取得可供搜尋與條件篩選使用的求職條件；Profile 是唯一來源 |

### 3.2 PII 檢核

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-10 | 對合法基準 Profile 執行 denylist 與內建 pattern 檢核 | 通過，沒有命中項目 |
| PT-11 | Profile 含 denylist 禁詞，且字母大小寫與 denylist 不同 | 拒絕；回報命中的禁詞與位置，大小寫不影響偵測 |
| PT-12 | Profile 分別含 Email、台灣手機、台灣市話與身分證字號格式 | 每一種輸入均被拒絕；回報對應的 pattern 命中位置 |
| PT-13 | Profile 同時含多個 denylist 與 pattern 命中 | 一次回報全部命中，供使用者完整修正 |
| PT-14 | 對合成求職信文字呼叫共用 PII 檢核函式 | 行為與 Profile 檢核一致；乾淨文字通過，含禁詞或內建 pattern 的文字被拒絕 |

### 3.3 CLI 介面

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-20 | 以預設路徑或 `--profile`、`--denylist` 指定合法 Profile 與 denylist，執行 `jobfinder profile lint` | exit 0；stdout 明確表示結構與 PII 檢核通過 |
| PT-21 | 對結構非法或含 PII 的 Profile 執行 `jobfinder profile lint` | 非零 exit；stderr 顯示驗證錯誤或全部 PII 命中項目 |
| PT-22 | 以預設路徑或 `--profile` 指定合法 Profile，執行 `jobfinder profile show` | exit 0；輸出載入後的 Profile 摘要，包含年資、學位領域、技能分級與求職方向，足以確認讀取的檔案內容 |
| PT-23 | 設定檔指向不存在或無法解析的 Profile，執行 `profile lint` 或 `profile show` | 非零 exit；stderr 說明檔案讀取或解析失敗 |

### 3.4 B6 反向校準

| 編號 | 測試情境 | 預期結果 |
|---|---|---|
| PT-30 | `interview` 事件少於 `calibration.min_interviews` | `jobfinder calibrate` 拒絕執行並說明尚未達門檻；不呼叫 Agent、不改寫 Profile |
| PT-31 | 達門檻的合成 Job、Score 與 `interview` 事件 | Calibrator 收到去識別化資料；輸出格式正確的建議 diff；Profile 檔案未被系統改寫 |
| PT-32 | Calibrator 輸出非法格式或含 PII | 拒絕建議、回傳安全錯誤；不寫入 Profile 或建議檔 |

## 4. 模組驗收

- `mise run fmt`、`mise run lint` 與 `mise run test` 全數通過。
- 合法的匿名 Profile 可被載入並提供給後續 Agent prompt；結構不完整、非法 enum、技能分級重複與非法數值均在載入時被拒絕。
- denylist 與內建 Email、電話、身分證字號 pattern 能檢出 Profile 與任意待檢文字中的 PII，並回報命中位置。
- `jobfinder profile lint` 與 `jobfinder profile show` 的輸出可由 Cobra 測試擷取；錯誤輸出不含完整 Profile 內容。
