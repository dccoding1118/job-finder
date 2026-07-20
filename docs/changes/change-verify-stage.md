# 開發階段測試部署區

## 背景與動機

開發 checkout 是原始碼與建置來源；驗收必須針對物化後的 binary、設定與資源，避免舊 binary、開發暫存檔或未部署資源造成誤判。

## 決策摘要

`.local-dev/verify/` 是開發階段測試部署根目錄。每個完成批次均部署目前建置的 binary 與明確指定的驗收資源到此目錄，並只從該目錄執行 L2/L3 驗收。測試資料、資料庫與證據與版控腳本分離；B4 使用隔離 Chromium profile 與 unpacked extension 執行自動模擬，日常 Chrome compatibility 是獨立人工 gate；真 104 頁面的實機觀察屬 B5。

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [verify](../verify.md) | 測試部署目錄結構、runbook 分層、B0–B4 mock 正向驗收與後續累加規則 |
| [deploy](../deploy.md) | 開發階段驗收部署與 MVP 營運部署的邊界 |
| [AGENTS](../../AGENTS.md) | 開發完成後的驗收入口、隔離 browser E2E 與實機 gate 要求 |
| [STATUS](../../STATUS.md) | 待逐批實作與擴充的驗收 runbook 工作 |

## 待實作進度

| 項目 | 時機 | 完成條件 |
|---|---|---|
| 建立 `scripts/verify/` runbook | B0–B4 | 已完成：deploy、reset、mock 以同一 artifact、SQLite 與 evidence 執行 V1、V2、V4 的 23 步正向驗收。 |
| pipeline、Agent 與來源案例 | B1–B3 | 已完成：production adapter＋合成來源、fake Agent executable、逐欄 snapshot 與精確 Run stats。 |
| API、extension page 與排程驗收 | B4 | 已完成：transient systemd lifecycle、localhost API、固定 ID extension 模擬、SQLite 回寫與 browser evidence。 |
| Chrome compatibility gate | B4 | 待完成：驗收者以日常 Chrome 附加同一 artifact 的人工相容性證據。 |
| live 來源與 CLI Runner | B4 | 待完成：V3 使用獨立 live config／SQLite，真 Yourator 至少一筆且逐欄格式正確，已授權 CLI Runner 完成 score／letter；不得沿用 mock source、fake PATH 或 mock DB。 |
| 104 實機驗收 | B5 | 待完成：合成 104 DOM fixture 執行 V5；真 104 頁面只收集使用者已載入內容的相容性證據。 |
