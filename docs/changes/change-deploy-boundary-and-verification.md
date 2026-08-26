# change — 部署形態的產品邊界與部署驗收自動化

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

兩個問題指向同一條界線：本 repo 承諾支援的部署形態到底是什麼。

**產品邊界被一份運維手冊撐大了。** `extension/manifest.json` 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，`internal/install/smoke.go` 的 `assertLoopback` 又在安裝時拒絕非 loopback 的 `api.addr`。程式碼層面，這個 repo 只支援後端與瀏覽器同機。但 `docs/guides/runbook-extension.md` 是一份 610 行、放在正式指南目錄下的 GCP IAP ＋ PuTTY 通道手冊，README、`deploy.md`、`getting-started.md`、`roadmap.md` 都指向它。公開後，讀者會把這份特定雲端環境的個人運維作法讀成官方推薦的遠端部署路徑。

**部署驗收 D1 卡在「需要一台乾淨機器」。** `verify.md` §6.1 把 D 系列全部列為人工 gate，理由寫在該節開頭：「安裝流程寫入的是真實使用者環境，自動化 harness 一律不碰」。D1 要求「在無既有安裝的環境執行」，於是每次動到 `internal/install`、`internal/paths` 或排程模板都得準備一台全新機器。這個前置擋在公開前置清單上，而準備成本讓它遲遲無法執行。

前提其實不成立：`internal/paths` 是路徑的唯一決策點，Linux 側全部位置由 `$HOME` 與 `XDG_*` 推導（`paths.go:66`、`:89`、`:93`），Windows 側由 `%LOCALAPPDATA%` 推導（`paths.go:113`）。把這些變數指向 `.local-dev/` 之下的目錄，安裝走的是與真實安裝完全相同的程式碼路徑，落點卻完全在隔離根內——正好符合「只寫 `.local-dev/`」這條既有規則。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 官方支援的部署形態只有一種：後端與瀏覽器同機，extension 直連 loopback。遠端後端是使用者自行負責的環境作法，本 repo 不提供實作路徑。 |
| D2 | `docs/guides/runbook-extension.md` 移出版控，改置於 `.local-dev/`。其中 extension 載入 Chrome 的步驟與 `getting-started.md` §5 重複，官方落點以後者為準。 |
| D3 | 部署驗收 D 系列拆成自動與人工兩組。自動組新增 `mise run e2e-deploy`，人工組維持每平台一輪。 |
| D4 | 自動組以 `HOME`（Linux）／`LOCALAPPDATA`（Windows）重導把安裝落點導進 `.local-dev/verify/deploy/`，不碰真實使用者環境。 |
| D5 | 自動組的服務層驗證走 transient 單元，不寫入正式 unit 目錄、不註冊正式排程工作。 |
| D6 | 每趟 `e2e-deploy` 結束一律清除自己建立的全部資源：transient 單元、重導根目錄。中途失敗也清。 |
| D7 | `--skip-verify` 的禁令改為分層陳述：自動組用重導安裝並自行啟動 `serve` 補上 API 生效面檢查，人工組維持完整的生效面驗證。 |

D3 不併進 `e2e-mock`：後者驗的是應用層行為（fetch／filter／score／letter，走 fake agent），D 系列驗的是安裝與排程層，前置條件與失敗模式不同。併在一起會讓每趟應用層驗收都得動 systemd。

## 3. 相對舊狀態的差異

| 主題 | 舊狀態 | 新狀態 |
|---|---|---|
| 遠端後端 | `docs/guides/` 下有完整的 GCP IAP 通道手冊，六份 canonical 文件指向它 | 官方文件只說明「遠端需自行把遠端 loopback 轉送到本機」這條約束與其原因，不提供作法 |
| extension 載入步驟 | 散在 `runbook-extension.md` §7 與 `getting-started.md` §5 兩處 | 只在 `getting-started.md` §5 |
| D1 全新安裝 | 人工，需要無既有安裝的機器 | 自動，`mise run e2e-deploy` 在隔離根內生成乾淨環境 |
| D2／D3／D5／D5A／D6／D6A／D6B／D10 | 人工 | 自動 |
| D4／D7／D8／D9 | 人工 | 人工（需真 Chrome 或真 Windows 環境） |
| `--skip-verify` | 一律不得用於部署 gate | 自動組可用，但須自行啟動 `serve` 補上 API 生效面檢查 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 寫入內容 |
|---|---|---|
| `README.md` | Deployment | 移除遠端 runbook 連結，改述唯一支援形態 |
| `AGENTS.md` | §3 頂層文件、§4 執行模型 | 移除 runbook 索引；執行模型只保留 loopback 約束 |
| `docs/deploy.md` | §5 | 改寫為「後端位置的約束」，移除 GCP 範例與 runbook 連結 |
| `docs/verify.md` | §1、§6.1、§9 | 新增 `e2e-deploy` 跑法；§6.1 拆自動／人工兩表；§9 人工 gate 改指 `getting-started.md` §5 |
| `docs/roadmap.md` | §2 | 移除 runbook 連結 |
| `docs/PRD.md` | R6.6 | 移除 SSH local forward 的實作提及 |
| `docs/guides/getting-started.md` | §1 形態表 | 遠端列改述為使用者自行負責 |
| `docs/guides/runbook-upgrade.md` | §Options | 移除 runbook 連結 |
| `mise.toml` | tasks | 新增 `e2e-deploy` |

## 5. 待實作進度

| 項目 | 狀態 |
|---|---|
| `scripts/verify/run-deploy.sh`（Linux 自動組） | 已實作並實測，D1／D2／D3／D5／D5A／D6／D6B 全數 PASS |
| Windows 自動組 | 未實作 |
| canonical 文件更新 | 已完成 |
| `runbook-extension.md` 移出版控 | 已完成 |

Windows 自動組的做法與 Linux 側對稱，兩處不同：以 `$env:LOCALAPPDATA` 指向隔離根（`internal/paths` 的 `windowsLayout` 讀這個變數，未設才退回 `%USERPROFILE%\AppData\Local`），並在 PATH 最前放一個 `powershell.cmd` 攔截排程呼叫——`internal/install/taskscheduler.go` 經 `powershell` 執行 `Register-ScheduledTask`，`.cmd` 在 `PATHEXT` 內，Go 的 `exec.LookPath` 會先找到它。此路徑尚未在 Windows 實機驗證過。

## 6. 已知殘留限制

- **systemd unit 的實際掛載無法自動化**。`internal/install/systemd.go` 的 `mount` 把 unit 寫進 manager 搜尋路徑再 `enable --now`，而 manager 的搜尋路徑在它啟動時就固定，同一個登入 session 內無法改指向重導後的目錄。自動組因此以 transient 單元驗服務層，掛載邏輯由 `internal/install/sequence_test.go` 的可注入 scheduler 守；真實掛載留在人工組。
- **Task Scheduler 的實際註冊同理**。`internal/install/taskscheduler.go` 的工作資料夾 `\jobfinder\` 是常數，重導 `%LOCALAPPDATA%` 不會改變它。Windows 自動組因此跳過排程註冊。
- **bootstrap 腳本的匿名下載路徑仍需 public repo**。`install.sh`／`install.ps1` 以匿名請求打 `api.github.com`，private repo 一律 404，這條只能在轉 public 後驗。
