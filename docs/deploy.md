# 部署 — job-finder

本文件定義開發驗收與 MVP 單機正式部署的邊界。開發驗收只寫入隔離目錄；正式部署只在明確執行安裝流程時寫入使用者環境。MVP 僅供單人使用，全部以使用者身分執行、不需系統管理員權限，資料與設定不進版控。

逐步操作見 [上手指南](guides/getting-started.md)；本文定義契約。

支援的部署平台為 Linux 與 Windows。兩者共用同一份 Go 安裝實作，差異只在排程機制（systemd user unit／Task Scheduler）與日誌出口。macOS 有 binary 工件但不是部署平台：工件不含排程模板與 bootstrap 腳本。

## 1. 開發階段驗收部署

開發 checkout 與測試部署區分離：`.local-dev/verify/` 是本機、gitignored 的受測安裝根目錄。開發完成一個批次後，先以 `scripts/verify/harness/deploy.sh` 物化目前 binary、驗收設定、匿名 Profile、資源與證據目錄，再從該目錄執行 [verify](verify.md) 指定的 runbook。browser E2E 程式與設定位於 `scripts/verify/browser/`。

此部署不安裝 systemd unit、不覆蓋日常使用資料，也不讀取日常使用目錄。部署產物 manifest 必須記錄來源 revision、建置時間及 binary checksum，使驗收可證明執行的是物化的 binary。驗收會將 `deploy/production/systemd/` 的模板渲染到隔離目錄，並以 transient user unit 驗證 service、one-shot 與 timer；不得複製 unit 到正式 user unit 目錄或啟用正式 unit。

## 2. 路徑決策與檔案配置

**路徑的唯一決策點是 `internal/paths`**。CLI 的旗標預設值、安裝流程與排程模板渲染全部向它取值，因此一個平台的配置只被描述一次。`jobfinder paths` 印出這台機器實際採用的全部位置，是排查「到底讀了哪份設定」的第一站。

| 角色 | Linux／macOS（XDG） | Windows |
|---|---|---|
| 常駐 binary | `~/.local/bin/jobfinder` | `%LocalAppData%\jobfinder\bin\jobfinder.exe` |
| 設定 | `~/.config/jobfinder/config.yaml` | `%LocalAppData%\jobfinder\config\config.yaml` |
| Profile／denylist | `~/.config/jobfinder/profile.yaml`、`pii-denylist.txt` | `%LocalAppData%\jobfinder\config\` 下同名檔案 |
| SQLite 與 lock | `~/.local/share/jobfinder/jobs.db` | `%LocalAppData%\jobfinder\data\jobs.db` |
| 備份 | `~/.local/share/jobfinder/backups/` | `%LocalAppData%\jobfinder\data\backups\` |
| 日誌檔 | `~/.local/share/jobfinder/logs/jobfinder.log`（預設不啟用） | `%LocalAppData%\jobfinder\data\logs\jobfinder.log` |
| 回滾工件與安裝 metadata | `~/.local/lib/jobfinder/` | `%LocalAppData%\jobfinder\lib\` |
| 排程定義 | `~/.config/systemd/user/` 的三個 unit | Task Scheduler 的 `\jobfinder\api`、`\jobfinder\run` |

XDG 側尊重 `XDG_CONFIG_HOME` 與 `XDG_DATA_HOME`；Windows 側以 `%LOCALAPPDATA%` 為根，未設時退回 `%USERPROFILE%\AppData\Local`。

`config.yaml` 的 `db.path`、`profile.path`、`profile.denylist` 必須指向上表位置，由安裝流程渲染為絕對路徑；`api.addr` 固定為 loopback 位址。設定檔不得記錄 CLI 憑證或任何 PII；`api.token` 與 `api.extension_origin` 只存於實際設定檔，不進版控。

**權限模型**：Linux 上設定、Profile、denylist、資料庫與備份目錄皆為 owner-only（檔案 `0600`、目錄 `0700`）。Windows 無等價的 `chmod`：整棵樹位於使用者的 `%LocalAppData%`，保護來自該目錄繼承的 ACL（非系統管理員的其他使用者無法讀取），安裝流程不再額外套用權限。這是明載的退讓，不是遺漏——在 Windows 上宣稱套了 `chmod 600` 才是錯的。

**日誌**：結構化記錄一律寫 stderr。Linux 由 journald 收集，`log.file` 預設留空；Windows Task Scheduler 會丟棄工作的輸出，安裝流程因此在該平台填入 `log.file`，否則失敗的排程抓取不會留下任何痕跡。檔案依大小輪替（`log.max_size_mb`，預設 8MB）並保留固定份數（`log.keep`，預設 4，含現行檔）。Linux 亦可自行填入 `log.file` 取得同樣的檔案輸出。

## 3. 常駐與排程

兩個平台承載相同的兩件事：一個長駐的 API（同時承載 pipeline 常駐 worker）與一個每日觸發、冪等的一次性抓取。

| 角色 | Linux（`deploy/production/systemd/`） | Windows（`deploy/production/windows/`） |
|---|---|---|
| 長駐 API | `jobfinder-api.service`：`Type=simple`、`Restart=on-failure`、`WantedBy=default.target` | 工作 `\jobfinder\api`：登入時觸發、`RestartOnFailure` 三次、`ExecutionTimeLimit=PT0S` |
| 每日抓取 | `jobfinder-run.service`（`Type=oneshot`、`TimeoutStartSec=1800`）＋ `jobfinder-run.timer`（`OnCalendar=*-*-* 08:30:00 Asia/Taipei`、`Persistent=false`） | 工作 `\jobfinder\run`：每日 08:30、`StartWhenAvailable=false`、`ExecutionTimeLimit=PT30M` |
| 啟動命令 | `<binary> serve --config <config>`／`<binary> run --config <config>` | 同左（`--trigger timer`） |
| 錯過的排程 | 不補跑（`Persistent=false`） | 不補跑（`StartWhenAvailable=false`） |
| 手動觸發抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 日常診斷 | `journalctl --user -u jobfinder-api.service` | `log.file`（見 §2） |

API 服務停止時處理即停止，僅抓取仍會依排程進行。抓取只記錄事實、抓完即退出，不等待 LLM 階段。

**Linux 特有**：所有 unit 的 `Environment=PATH=` 必須是完整白名單，至少包含 `~/.local/bin`、`~/.local/share/mise/shims`、`/usr/local/bin`、`/usr/bin`、`/bin`，使 headless `claude`／`codex` 與其相依可被執行；不得依賴 interactive shell 的 `mise activate` 或 `bash -lc`。服務以登入使用者執行，須先啟用 linger，確保登出後服務與 timer 持續可用。

**Windows 特有**：安裝時把 `%LocalAppData%\jobfinder\bin` 寫入使用者 PATH（登入時已存在的終端須重開才生效）。Agent CLI 若以 npm 安裝，`claude` 實際是 `.cmd` shim，CreateProcess 無法直接執行；`internal/agents` 因此在 Windows 上先做 PATH 解析，遇 `.cmd`／`.bat` 改經 `%COMSPEC% /c` 執行。CLI 不在服務 PATH 上時，可在 `llm.roles.<role>.<primary|fallback>.command` 直接填完整執行檔路徑。

Agent 稽核資料保留在 SQLite 的 `agent_calls`。

## 4. 安裝、更新與回滾

**安裝語意集中在 binary 的三個子命令**：`jobfinder install`／`update`／`rollback`。路徑決策、token 生成、設定渲染、既有設定不覆寫、排程掛載與生效面驗證都在同一份 Go 程式碼裡，Linux 與 Windows 共用，平台差異只剩排程掛載。因此以下三條路徑得到完全相同的結果：

| 取得方式 | 動作 |
|---|---|
| `install.sh`／`install.ps1` | 解析版本 → 下載工件與 `SHA256SUMS` → 驗 checksum → 解壓到暫存 → 執行解壓出的 `jobfinder install` |
| 手動下載工件 | 自行比對 `SHA256SUMS`，解壓後直接執行 `jobfinder install` |
| 開發 checkout | `scripts/deploy/*.sh`（`mise run deploy-*`）：跑 `fmt`／`lint`／`test`／`build`，再把剛建置的 binary 交給同一組子命令，並以 checkout 為 `--assets` |

解壓出的執行檔是**安裝媒介**，不是安裝本身：它把自己複製到 §2 的常駐位置，排程執行的是那份副本，安裝完下載目錄即可刪除。安裝流程會拒絕「拿常駐副本安裝到自己身上」。

| 子命令 | 動作 | 生效面驗證 |
|---|---|---|
| `install` | 建立 §2 目錄 → 渲染設定並生成隨機 token（既有者不覆寫）→ 種入範例 Profile 與空 denylist（既有者不覆寫）→ `profile lint` 閘門 → 放置 binary → 掛載排程 → 註冊 PATH → 啟動並驗證 | 見下段 |
| `update` | 保留現行 binary 至 `jobfinder.prev` → 替換 binary 與排程定義 → 重啟 API → 驗證 | 同上，且啟動時間須晚於替換點 |
| `rollback` | 現行 binary 存為 `.bad`（保留前滾可能）→ 由 `jobfinder.prev` 與 stash 的排程定義還原 → 重啟 API → 驗證；SQLite **不自動更動**，僅在確認毀損時由備份目錄手動還原 | 同上 |

**所有驗證打在生效面（執行中的 process），而非安裝面**：「檔案複製了」與「服務 active」都可能同時為真而執行中的仍是舊 process。

| 驗證 | Linux | Windows |
|---|---|---|
| 執行中的就是剛裝的 binary | API service `MainPID` 的 `/proc/<pid>/exe` | `Win32_Process` 的 `ExecutablePath` |
| 確實重啟過 | `ExecMainStartTimestamp` 晚於替換點 | process `CreationDate` 晚於替換點 |
| 排程已武裝 | one-shot unit 可載入、timer active 且有 `NextElapseUSecRealtime` | 抓取工作有 `NextRunTime` |
| API 只在 loopback | `api.addr` 必須是 loopback 位址（服務就綁這個位址，沒有例外） | 同左 |
| API 真的能用 | 帶 token 的 `GET /api/v1/jobs` 回 200、未帶回 401 | 同左 |

`--skip-verify` 可略過生效面檢查，僅供沒有可用使用者 session 的環境（容器映像建置、無人值守佈建）；未經生效面驗證的安裝不算已證明可用。

第一次安裝時若無設定檔，由 `configs/config.example.yaml` 渲染出絕對路徑與隨機 token 的 `config.yaml`。**每個佔位替換都要求恰好命中一次**，否則安裝直接失敗——靜默未命中會讓安裝指向相對的開發路徑，然後在無關的地方才炸。`api.extension_origin` 仍為佔位，須在載入 extension 前替換為實際 `chrome-extension://` id。Linux 的 unit 為渲染後的靜態副本，安裝前會比對並將差異吵出（template 改版或人工修改都不靜默吞掉）。

安裝流程一律不觸發抓取：只確認排程已排定下一次觸發。抓取只由每日排程、操作者手動觸發，或 Side Panel 的重新整理動作啟動。

Windows 的 `Start-ScheduledTask` 對已在執行的工作是 no-op，和 systemd 的 `enable --now` 是同一個陷阱：更新一律先停、等 process 真的消失、再啟動。不得將驗收部署（`.local-dev/verify/`）的 binary、設定或 state 直接覆蓋日常使用目錄。

## 5. 遠端後端（選配）與維運

**預設形態是後端與瀏覽器同機**：extension 直連 loopback，不需要本節。

extension 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，後端位置不是「設定檔填一個 URL」可決定的——`api.addr` 只決定綁哪個 loopback port。因此後端跑在遠端機器時，必須自行把遠端的 loopback port 轉送到本機 loopback，extension Options 使用該本機 endpoint；不得將 service 改綁 `0.0.0.0` 作為替代。以 GCP IAP 為例的完整設定、自動重連、驗證與排障見 [遠端後端：Windows extension 與 GCP API 常駐通道](guides/runbook-extension.md)。真正的遠端／雲端 endpoint 需要 `optional_host_permissions` 的 runtime 授權，屬 `docs/roadmap.md` 的 S2 項目。

日常診斷見 §3 的診斷欄與 Side Panel 的 Run 歷史。驗證 systemd 環境時，以 `systemd-run --user --wait --pipe` 執行相同 binary／設定組合，API 使用 transient service，timer 使用 transient timer 實際觸發 one-shot；互動 shell 成功不構成 service 環境成功的證據。user bus 不可用時，開發驗收回 `ENVIRONMENT_BLOCKED`，不誤判為產品失敗。

## 6. 只收集不判定的暫停模式

`worker.paused: true` 讓 `serve` 只提供 API 而不啟動常駐 worker，也不持有 worker lock。抓取、清單擷取與內頁擷取照常寫入，職缺一律停在 `new`，不呼叫任何 Agent、不產生判定，因此 Profile 尚未定案時不會累積之後必須作廢的結論。此時：

| 動作 | 指令 |
|---|---|
| 手動跑一批篩選 | `jobfinder run --stage filter --limit <n>` |
| 手動跑一批評分 | `jobfinder run --stage score --limit <n>` |
| 恢復常駐消化 | 改回 `paused: false` 後重啟 API（Linux `systemctl --user restart jobfinder-api.service`；Windows `Restart-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'`） |

`llm.max_*_per_day` 不是暫停開關：值為 0 或負數代表**不設上限**，不是不執行。

日常要暫停 token 消耗用的是 Side Panel 系統頁的**自動篩選與評分**開關（存於資料庫，重啟仍生效）：worker 保持常駐並持有鎖，只是不自動取件，使用者仍可對單筆按「馬上處理」。`worker.paused` 則是部署期把三個階段整個交給 CLI 批次的模式，此模式下該開關與「馬上處理」皆無作用。

結構化硬規則仍會在清單擷取時就地判定（不花 token），故暫停期間仍可能出現 `filtered_out`；改動 `requirements` 會改變 `filter_revision`，之後的重新處理會把這些結論一併重跑。

清空既有職缺重新開始時，停止 `jobfinder-api.service` 與 `jobfinder-run.timer` 後刪除 SQLite（連同 `-wal`、`-shm`），下次啟動即以最新 schema 重建空庫。Profile、denylist 與設定不受影響。

## 7. CI 與 release 工件

**版號的單一真相是 git tag `v<MAJOR>.<MINOR>.<PATCH>`**，Go binary 與 extension 的版號皆由 tag 推導，不在原始碼中另存一份。

| workflow | job | 觸發 | 動作 |
|---|---|---|---|
| `.github/workflows/ci.yml` | `check`（ubuntu） | pull request、push 至 `main` | 以 `mise.toml` 鎖定的工具鏈執行 gofumpt 檢查（只檢查不改寫）、`lint`、`test`，並確認 extension manifest 可解析 |
| `.github/workflows/ci.yml` | `windows`（windows-latest） | 同上 | `go build`／`go test`、Task Scheduler 模板可被 XML 解析、`install.ps1` 語法檢查。Windows 是受支援平台，路徑類錯誤不得只在 release 才暴露 |
| `.github/workflows/release.yml` | `release` | push tag `v*` | 驗證 tag 格式 → 重跑 lint／test → 建置多平台 binary → 打包 extension → 產生 checksum → 建立 GitHub Release |

release 工件：

| 工件 | 內容 |
|---|---|
| `jobfinder_<tag>_<os>_<arch>.tar.gz` | 靜態 binary（`CGO_ENABLED=0`、`-trimpath`，版號經 `-ldflags` 注入 `internal/version.tag`）＋ `LICENSE`、`README.md`、`configs/`。Linux 另附 `systemd/` unit 模板與 `install.sh`。平台為 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`darwin/amd64`；darwin 只有 binary 與 `configs/`（非部署平台） |
| `jobfinder_<tag>_windows_amd64.zip` | 同上，`jobfinder.exe`，另附 `windows/` Task Scheduler 模板與 `install.ps1` |
| `jobfinder-extension_<tag>.zip` | extension 目錄，`manifest.json` 的 `version` 於打包時改寫為 tag 去掉 `v` 的語意版號 |
| `SHA256SUMS` | 上述所有工件的 SHA256 |

版號的唯一決策點是 `internal/version`：逐欄位取 ldflags 注入值 → `debug.ReadBuildInfo()` → 寫死的 fallback。`jobfinder version` 因此在任何建置路徑都印得出可辨識的身分——release 建置印 tag，本機建置印 `dev (<commit>) (dirty)`，不偽造版本號。

`-X` 的符號路徑 `github.com/dccoding1118/job-finder/internal/version.tag` 是字串綁定：package 搬家或變數改名會使注入**靜默失效**，版號悄悄退回 `dev`。改動時必須同步 `release.yml`。

工件內容即安裝流程所需的全部素材：`configs/` 供設定渲染、平台目錄供排程掛載、bootstrap 腳本供下載路徑使用。安裝流程在開發 checkout 下改讀 `deploy/production/<平台>/`，因此兩種來源共用同一份實作。

extension zip 需人工上傳至 Chrome Web Store 並送審——審查結果有變數，不納入自動發佈。

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
