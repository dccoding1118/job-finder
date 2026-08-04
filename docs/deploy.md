# 部署 — job-finder

本文件定義開發驗收與 MVP 單機正式部署的邊界。開發驗收只寫入隔離目錄；正式部署只在明確執行安裝流程時寫入使用者環境。MVP 僅供單人使用，所有服務皆為 systemd user unit，資料與設定不進版控。

## 1. 開發階段驗收部署

開發 checkout 與測試部署區分離：`.local-dev/verify/` 是本機、gitignored 的受測安裝根目錄。開發完成一個批次後，先以 `scripts/verify/harness/deploy.sh` 物化目前 binary、驗收設定、匿名 Profile、資源與證據目錄，再從該目錄執行 [verify](verify.md) 指定的 runbook。browser E2E 程式與設定位於 `scripts/verify/browser/`。

此部署不安裝 systemd unit、不覆蓋日常使用資料，也不讀取日常使用目錄。部署產物 manifest 必須記錄來源 revision、建置時間及 binary checksum，使驗收可證明執行的是物化的 binary。驗收會將 `deploy/production/systemd/` 的模板渲染到隔離目錄，並以 transient user unit 驗證 service、one-shot 與 timer；不得複製 unit 到正式 user unit 目錄或啟用正式 unit。

## 2. MVP 執行環境與檔案配置

| 項目 | 位置／設定 | 規則 |
|---|---|---|
| binary | `~/.local/lib/jobfinder/jobfinder` | 由已驗證 build 複製而來；systemd 以絕對路徑執行 |
| 設定 | `~/.config/jobfinder/config.yaml` | 由 `configs/config.example.yaml` 建立；檔案權限為 owner-only，所有引用路徑均為絕對路徑 |
| Profile／denylist | `~/.config/jobfinder/profile.yaml`、`~/.config/jobfinder/pii-denylist.txt` | 不入版控；Profile 與 denylist 由設定檔引用 |
| SQLite 與 lock | `~/.local/share/jobfinder/jobs.db` 及同目錄 lock | 資料目錄僅使用者可讀寫；服務與手動 CLI 共用同一檔案 |
| 備份 | `~/.local/share/jobfinder/backups/` | 在 pipeline 完整結束後複製 SQLite；保留數量由設定的維運程序管理 |
| user units | `~/.config/systemd/user/jobfinder-api.service`、`jobfinder-run.service`、`jobfinder-run.timer` | unit 定義入版控的 `deploy/production/systemd/`，正式安裝時複製至 user unit 目錄 |

`config.yaml` 的 `db.path`、`profile.path`、`profile.denylist` 必須指向上表位置；`api.addr` 固定為 loopback 位址。設定檔不得記錄 CLI 憑證或任何 PII；`api.token` 與 `api.extension_origin` 只存於 owner-only 的實際設定檔，不進版控。

## 3. systemd user units

| unit | 類型與生命週期 | ExecStart／必要設定 |
|---|---|---|
| `jobfinder-api.service` | 長駐服務；`Restart=on-failure` | `<binary> serve --config <config>`；僅監聽 `api.addr`；`WorkingDirectory` 為資料根。**同時承載 pipeline 常駐 worker**（篩選／評分／求職信的唯一消化者），因此此服務停止時處理即停止，僅抓取仍會依 timer 進行 |
| `jobfinder-run.service` | `Type=oneshot` | `<binary> run --config <config>`；只執行抓取，抓完即退出，不等待 LLM 階段 |
| `jobfinder-run.timer` | 每日觸發 | `OnCalendar=*-*-* 08:30:00 Asia/Taipei`、`Persistent=false`、`Unit=jobfinder-run.service`；錯過的排程不補跑 |

所有 unit 的 `Environment=PATH=` 必須是完整白名單，至少包含 `~/.local/bin`、`~/.local/share/mise/shims`、`/usr/local/bin`、`/usr/bin`、`/bin`，使 headless `claude`／`codex` 與其相依可被執行。不得依賴 interactive shell 的 `mise activate` 或 `bash -lc`。

服務以登入使用者執行，須先啟用 linger，確保登出後 web service 與 timer 持續可用。服務 stdout／stderr 由 journald 收集；Agent 稽核資料保留在 SQLite 的 `agent_calls`。

## 4. 安裝、更新與回滾

三個入口腳本位於 `scripts/deploy/`（共用 `lib.sh`），是正式部署的唯一入口；它們與 `scripts/verify/` 的驗收 harness 分離，**不由任何 `e2e-*` 任務呼叫**，也不寫入 `.local-dev/`。所有驗證打在**生效面**（執行中的 process），而非安裝面：`/proc/<pid>/exe` 必須指向剛安裝的 binary、更新後啟動時間須晚於替換點，光看 `is-active` 或 unit 檔內容不算通過。

| 腳本 | 動作 | 生效面驗證 |
|---|---|---|
| `install.sh` | preflight（`fmt`／`lint`／`test`＋`build`）→ 建立 §2 目錄與 owner-only 設定／Profile／denylist（既有者不覆寫）→ `profile lint` 閘門 → 安裝 binary 與渲染後 unit → `daemon-reload`、enable 並啟動 API service 與 timer | API service `MainPID` 的 `/proc/<pid>/exe` 指向安裝的 binary；只有 loopback listener；帶 token 的 Job API 回 200、未帶回 401；`jobfinder-run.service` 可載入且 timer active 並有下一次觸發時間 |
| `update.sh` | 重跑 preflight 與 build → 保留現行 binary 至 `jobfinder.prev`、替換 binary 與 unit → `daemon-reload` 後對 API service `try-restart` | 執行中 process 為新 binary 且啟動時間晚於替換點；loopback-only；API smoke 通過 |
| `rollback.sh` | 由 `jobfinder.prev` 與 `units.prev/` 還原前一版 binary 與 unit → `daemon-reload` 後 `try-restart`；SQLite **不自動更動**，僅在確認毀損時由 `~/.local/share/jobfinder/backups/` 手動還原 | 執行中 process 為還原版；loopback-only；API smoke 通過 |

第一次安裝時若無設定檔，`install.sh` 由 `configs/config.example.yaml` 渲染出絕對路徑與隨機 token 的 `config.yaml`；`api.extension_origin` 仍為佔位，須在載入 extension 前替換為實際 `chrome-extension://` id。既有設定檔一律不覆寫；unit 為渲染後的靜態副本，安裝前會比對並將差異吵出（template 改版或人工修改都不靜默吞掉）。

部署腳本一律不觸發抓取：`install.sh` 只確認 one-shot unit 可載入且 timer 已排定下一次觸發，`update.sh` 與 `rollback.sh` 完全不碰抓取路徑；抓取只由每日 timer 或操作者手動 `systemctl --user start jobfinder-run.service`（或 Side Panel 的重新整理動作）啟動。

`systemctl --user enable --now` 不會重啟已在執行的舊 process；更新與回滾一律使用 `try-restart`。不得將驗收部署（`.local-dev/verify/`）的 binary、設定或 state 直接覆蓋日常使用目錄。

## 5. 遠端存取與維運

API 不公開網路埠。Windows 工作站以背景常駐的 SSH local forward，將專用的本機 `127.0.0.1:18686` 轉送到 VM 的 `127.0.0.1:8686`，extension Options 使用該本機 endpoint；不得將 service 改綁 `0.0.0.0` 作為替代。通道由 Windows Task Scheduler 於登入時啟動，IAP 先處理底層重連，常駐 wrapper 在 SSH process 退出後重建完整 session；完整設定、驗證與排障步驟見 [Windows extension 與 GCP API 常駐通道](guides/runbook-extension.md)。

日常診斷使用 `journalctl --user -u jobfinder-api.service`、`journalctl --user -u jobfinder-run.service` 與 Side Panel 的 Run 歷史。驗證 systemd 環境時，以 `systemd-run --user --wait --pipe` 執行相同 binary／設定組合，API 使用 transient service，timer 使用 transient timer 實際觸發 one-shot；互動 shell 成功不構成 service 環境成功的證據。user bus 不可用時，開發驗收回 `ENVIRONMENT_BLOCKED`，不誤判為產品失敗。

## 6. 只收集不判定的暫停模式

`worker.paused: true` 讓 `serve` 只提供 API 而不啟動常駐 worker，也不持有 worker lock。抓取、清單擷取與內頁擷取照常寫入，職缺一律停在 `new`，不呼叫任何 Agent、不產生判定，因此 Profile 尚未定案時不會累積之後必須作廢的結論。此時：

| 動作 | 指令 |
|---|---|
| 手動跑一批篩選 | `jobfinder run --config <config> --stage filter --limit <n>` |
| 手動跑一批評分 | `jobfinder run --config <config> --stage score --limit <n>` |
| 恢復常駐消化 | 改回 `paused: false` 並 `systemctl --user restart jobfinder-api.service` |

`llm.max_*_per_day` 不是暫停開關：值為 0 或負數代表**不設上限**，不是不執行。

日常要暫停 token 消耗用的是 Side Panel 系統頁的**自動篩選與評分**開關（存於資料庫，重啟仍生效）：worker 保持常駐並持有鎖，只是不自動取件，使用者仍可對單筆按「馬上處理」。`worker.paused` 則是部署期把三個階段整個交給 CLI 批次的模式，此模式下該開關與「馬上處理」皆無作用。

結構化硬規則仍會在清單擷取時就地判定（不花 token），故暫停期間仍可能出現 `filtered_out`；改動 `requirements` 會改變 `filter_revision`，之後的重新處理會把這些結論一併重跑。

清空既有職缺重新開始時，停止 `jobfinder-api.service` 與 `jobfinder-run.timer` 後刪除 SQLite（連同 `-wal`、`-shm`），下次啟動即以最新 schema 重建空庫。Profile、denylist 與設定不受影響。

## 7. CI 與 release 工件

**版號的單一真相是 git tag `v<MAJOR>.<MINOR>.<PATCH>`**，Go binary 與 extension 的版號皆由 tag 推導，不在原始碼中另存一份。

| workflow | 觸發 | 動作 |
|---|---|---|
| `.github/workflows/ci.yml` | pull request、push 至 `main` | 以 `mise.toml` 鎖定的工具鏈執行 gofumpt 檢查（只檢查不改寫）、`lint`、`test`，並確認 extension manifest 可解析 |
| `.github/workflows/release.yml` | push tag `v*` | 驗證 tag 格式 → 重跑 lint／test → 建置多平台 binary → 打包 extension → 產生 checksum → 建立 GitHub Release |

release 工件：

| 工件 | 內容 |
|---|---|
| `jobfinder_<tag>_<os>_<arch>.tar.gz` | 靜態 binary（`CGO_ENABLED=0`、`-trimpath`，版號經 `-ldflags` 注入 `cli.version`）＋ `LICENSE`、`README.md`、`systemd/` unit 模板。平台為 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`darwin/amd64` |
| `jobfinder-extension_<tag>.zip` | extension 目錄，`manifest.json` 的 `version` 於打包時改寫為 tag 去掉 `v` 的語意版號 |
| `SHA256SUMS` | 上述所有工件的 SHA256 |

`jobfinder version` 印出注入的版號；未經 release 建置的 binary 回報 `dev`。

extension zip 需人工上傳至 Chrome Web Store 並送審——審查結果有變數，不納入自動發佈。

`scripts/deploy/` 的安裝與更新目前由開發 checkout 重新建置（見 §4）；改為下載 release 工件並驗證 checksum 屬待實作項。

## 8. 產品化雛型（S3 方向，暫不實作）

| 面向 | 目標狀態 |
|---|---|
| 打包 | 容器化單一 image；worker 與集中抓取池可獨立部署 |
| 運算 | 容器服務（API）＋容器 job（抓取）；多實例 |
| 排程 | 雲端排程服務 ＋任務佇列（per-tenant 派工） |
| 資料庫 | PostgreSQL 多租戶 schema |
| LLM | 直串 API；金鑰入 secret 管理服務；成本工程（批次、模型分級、用量計量） |
| 身分 | Google OAuth ＋計費身分 |
| CI/CD | GitHub Actions → container registry → 容器服務；環境分層（staging／prod） |
| 觀測 | 集中式 log ＋指標告警、per-tenant 用量儀表板 |

帳號、計費與多租戶的實作不在本 repo。
