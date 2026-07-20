# 驗收與部署目錄分層

## 背景與動機

開發驗收的腳本、browser E2E 程式與正式 systemd unit 模板需要以目錄邊界表達不同的執行權限與資料落點，避免將隔離驗收誤認為正式安裝。

## 決策摘要

- `scripts/verify/` 是開發驗收入口，包含物化、重置、mock/live runbook、fixture 與 browser E2E 設定及案例。
- `.local-dev/verify/` 是唯一的開發驗收資料根；所有 artifact、設定、SQLite、browser profile、結果與 evidence 都在此目錄，且不得覆蓋日常資料。
- `deploy/production/` 只保存正式單機部署資源；目前包含 systemd user unit 模板，不含會安裝、啟用或修改使用者環境的腳本。
- 未來正式安裝流程應置於 `scripts/deploy/`，僅在明確執行部署任務時使用；不得由 `e2e-*` 任務呼叫。

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [deploy](../deploy.md) | 開發驗收與正式部署的目錄、資料與執行邊界 |
| [verify](../verify.md) | 開發驗收入口與 browser E2E 的位置 |
| [AGENTS](../../AGENTS.md) | 專案目錄與驗收、部署導航 |

## 待實作進度

| 項目 | 完成條件 |
|---|---|
| 調整驗收與模板目錄 | 已將 browser E2E 及 Playwright 設定收納至 `scripts/verify/`，正式 unit 移至 `deploy/production/systemd/`。 |
| 正式安裝器 | B4 部署驗收完成後，在 `scripts/deploy/` 實作可明確呼叫的安裝、更新與回滾入口。 |
