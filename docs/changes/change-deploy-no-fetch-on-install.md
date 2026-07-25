# 部署不觸發抓取

## 背景／動機

安裝腳本在完成安裝後會實際啟動一次 `jobfinder-run.service` 抓取，`jobfinder-run.timer` 又以 `Persistent=true` 補跑錯過的排程，使每次重新部署都對外站台發出一輪完整抓取。抓取路徑本身已由 `e2e-live` 驗收覆蓋，部署階段不需要再以真實抓取證明。

## 決策摘要

- 部署腳本一律不觸發抓取；`install.sh`、`update.sh`、`rollback.sh` 皆不啟動 `jobfinder-run.service`。
- `install.sh` 改以「抓取已就緒」為驗證條件：one-shot unit 可載入、timer active 且已排定下一次觸發時間。
- `jobfinder-run.timer` 使用 `Persistent=false`；錯過的排程不補跑，開機或重新部署不會立即抓取。
- 抓取只有兩個來源：每日 `OnCalendar` 排程，或操作者手動觸發（`systemctl --user start jobfinder-run.service`，或 Side Panel 的執行動作）。

## 相對舊狀態的差異

| 主題 | 舊狀態 | 最新狀態 |
|---|---|---|
| 安裝後行為 | 啟動一次真實抓取作為 smoke | 只確認 one-shot unit 與 timer 就緒 |
| 錯過的排程 | `Persistent=true` 補跑 | `Persistent=false` 不補跑 |
| 抓取觸發來源 | 排程、手動、部署 | 排程、手動 |
| `install.sh` 生效面驗證 | `run` one-shot `Result=success` | unit `LoadState=loaded`、timer `ActiveState=active` 且有下一次觸發時間 |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [部署](../deploy.md) | timer 參數、部署腳本的抓取觸發規則與生效面驗證條件 |

## 交付狀態

| 項目 | 狀態 | 完成條件 |
|---|---|---|
| timer 不補跑 | 已完成 | unit template 為 `Persistent=false` |
| 安裝不抓取 | 已完成 | `install.sh` 以 `assert_run_armed` 取代真實抓取 smoke |

## 已知殘留限制

- 安裝驗證不再涵蓋抓取實際能否成功；抓取路徑的證據來自 `e2e-live` 與首次排程／手動觸發後的 Run 歷史。
- 既有機器上已安裝的 timer 需重新部署（`mise run deploy-install` 或 `deploy-update`）才會換成 `Persistent=false`。
