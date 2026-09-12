# 部署 — job-finder

本文件定義開發驗收與 MVP 單機正式部署的邊界。開發驗收只寫入隔離目錄；正式部署只在明確執行安裝流程時寫入使用者環境。MVP 僅供單人使用，全部以使用者身分執行、不需系統管理員權限，資料與設定不進版控。

逐步操作見 [上手指南](guides/getting-started.md)；本文定義契約。

支援的部署平台為 Linux 與 Windows。兩者共用同一份 Go 安裝實作，差異只在排程機制（systemd user unit／Task Scheduler）與日誌出口。macOS 有 binary 工件但不是部署平台：工件不含排程模板與 bootstrap 腳本。

## 1. 三種環境

| 環境 | 範圍 | 素材 | 安裝入口 | `jobfinder version` |
|---|---|---|---|---|
| 開發階段驗收 | `.local-dev/dev-verify/` 隔離根 | 當下 working tree 的建置 | 驗收腳本自行物化，不裝進使用者環境 | `dev (<commit>)` |
| 測試環境 | 一台機器的完整使用者環境 | dev 部署包 | bootstrap 腳本的本地來源模式 | `dev (<commit>)` |
| 正式環境 | 一台機器的完整使用者環境 | release 工件 | bootstrap 腳本的下載模式 | tag |

**測試環境與正式環境只差素材一項**，安裝入口、路徑決策、排程掛載與生效面驗證逐字相同。測試環境因此驗得到正式環境實際會走的安裝流程。官方建議的測試環境安排見 [測試環境指南](guides/test-environment.md)。

開發階段驗收的邊界是**不碰使用者環境**：不安裝 systemd unit、不覆蓋日常使用資料，也不讀取日常使用目錄。它把 `deploy/production/systemd/` 的模板渲染到隔離目錄，並以 transient user unit 驗證 service、one-shot 與 timer，不複製 unit 到正式 user unit 目錄、不啟用正式 unit。物化產物的 manifest 記錄來源 revision、建置時間與 binary checksum，使驗收可證明執行的是物化的那份 binary。作法見 `AGENTS.md` §5，案例見 [verify](verify.md)。

## 2. 路徑決策與檔案配置

**路徑的唯一決策點是 `internal/paths`**。CLI 的旗標預設值、安裝流程與排程模板渲染全部向它取值，因此一個平台的配置只被描述一次。`jobfinder paths` 印出這台機器實際採用的全部位置，是排查「到底讀了哪份設定」的第一站。

| 角色 | Linux／macOS（XDG） | Windows |
|---|---|---|
| 常駐 binary | `~/.local/bin/jobfinder` | `%LocalAppData%\jobfinder\bin\jobfinder.exe` |
| 排程執行的 binary | 同上 | `%LocalAppData%\jobfinder\bin\jobfinderw.exe` |
| 設定 | `~/.config/jobfinder/config.yaml` | `%LocalAppData%\jobfinder\config\config.yaml` |
| Profile／denylist | `~/.config/jobfinder/profile.yaml`、`pii-denylist.txt` | `%LocalAppData%\jobfinder\config\` 下同名檔案 |
| SQLite 與 lock | `~/.local/share/jobfinder/jobs.db` | `%LocalAppData%\jobfinder\data\jobs.db` |
| 備份 | `~/.local/share/jobfinder/backups/` | `%LocalAppData%\jobfinder\data\backups\` |
| 日誌檔 | `~/.local/share/jobfinder/logs/jobfinder.log`（預設不啟用） | `%LocalAppData%\jobfinder\data\logs\jobfinder.log` |
| 回滾工件與安裝 metadata | `~/.local/lib/jobfinder/` | `%LocalAppData%\jobfinder\lib\` |
| 排程定義 | `~/.config/systemd/user/` 的三個 unit | Task Scheduler 的 `\jobfinder\api`、`\jobfinder\run` |

XDG 側尊重 `XDG_CONFIG_HOME` 與 `XDG_DATA_HOME`；Windows 側以 `%LOCALAPPDATA%` 為根，未設時退回 `%USERPROFILE%\AppData\Local`。

**Windows 安裝兩支執行檔**：使用者輸入的 `jobfinder.exe` 是 console subsystem，輸出走終端機；排程執行的 `jobfinderw.exe` 是同一份原始碼的 GUI subsystem 建置。Task Scheduler 在使用者自己的 session 裡啟動 console 程式會配一個主控台視窗，常駐服務不能在桌面上留一個視窗，一次性抓取也不該每天閃一次。兩者必須同版：安裝流程從同一份工件取出，回滾也一起移動。其他平台沒有 subsystem 這回事，兩個角色是同一個檔案。診斷用途上這個差異有實際後果：**`jobfinderw.exe` 沒有 stderr 可寫，要在前景看錯誤訊息一律用 `jobfinder.exe serve`**。

`config.yaml` 的 `db.path`、`profile.path`、`profile.denylist` 必須指向上表位置，由安裝流程渲染為絕對路徑；`api.addr` 固定為 loopback 位址。設定檔不得記錄 CLI 憑證或任何 PII；`api.token` 與 `api.extension_origin` 只存於實際設定檔，不進版控。

**權限模型**：Linux 上設定、Profile、denylist 由安裝流程寫成 `0600`，設定、資料、備份、日誌與回滾目錄為 `0700`。**目錄才是權限邊界**：SQLite 及其 `-wal`／`-shm` 由 driver 於執行期建立，用的是行程 umask，程式在建立後補上 `0600` 作為縱深防禦，但這層不像目錄那樣有保證。Windows 無等價的 `chmod`：整棵樹位於使用者的 `%LocalAppData%`，保護來自該目錄繼承的 ACL（非系統管理員的其他使用者無法讀取），安裝流程不套用權限。這是明載的退讓，不是遺漏——在 Windows 上宣稱套了 `chmod 600` 才是錯的。

**日誌**：結構化記錄同時寫檔案與 stderr，**檔案優先**。Linux 由 journald 收集 stderr，`log.file` 預設留空；Windows Task Scheduler 會丟棄工作的輸出，且 `jobfinderw.exe` 根本沒有主控台可寫，安裝流程因此在該平台填入 `log.file`。順序不可對調：多重寫入遇到第一個失敗的 writer 就停止，把沒有主控台的 stderr 排在前面會讓每一筆記錄在進到唯一的檔案 sink 之前就被吞掉。檔案依大小輪替（`log.max_size_mb`，預設 8MB）並保留固定份數（`log.keep`，預設 4，含現行檔）。Linux 亦可自行填入 `log.file` 取得同樣的檔案輸出。

**啟動失敗的錯誤**另有一條路徑：命令失敗的訊息本身走 stderr，有人收集就夠了（journald、終端機都算）。Windows 兩者皆無，因此該類錯誤會額外寫進 `log.file`；設定檔還沒解析成功、日誌尚未掛上時，則直接補一行到該平台的預設日誌位置。

## 3. 常駐與排程

兩個平台承載相同的兩件事：一個長駐的 API（同時承載 pipeline 常駐 worker）與一個每日觸發、冪等的一次性抓取。

| 角色 | Linux（`deploy/production/systemd/`） | Windows（`deploy/production/windows/`） |
|---|---|---|
| 長駐 API | `jobfinder-api.service`：`Type=simple`、`Restart=on-failure`、`WantedBy=default.target` | 工作 `\jobfinder\api`：登入時觸發、`RestartOnFailure` 三次、`ExecutionTimeLimit=PT0S` |
| 每日抓取 | `jobfinder-run.service`（`Type=oneshot`、`TimeoutStartSec=1800`）＋ `jobfinder-run.timer`（`OnCalendar=*-*-* 08:30:00 Asia/Taipei`、`Persistent=false`） | 工作 `\jobfinder\run`：每日 08:30、`StartWhenAvailable=false`、`ExecutionTimeLimit=PT30M` |
| 啟動命令 | `<binary> serve --config <config>`／`<binary> run --config <config> --trigger timer` | 同左，binary 為 `jobfinderw.exe` |
| 錯過的排程 | 不補跑（`Persistent=false`） | 不補跑（`StartWhenAvailable=false`） |
| 手動觸發抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 日常診斷 | `journalctl --user -u jobfinder-api.service` | `log.file`（見 §2） |

API 服務停止時處理即停止，僅抓取仍會依排程進行。抓取只記錄事實、抓完即退出，不等待 LLM 階段。

**Linux 特有**：所有 unit 的 `Environment=PATH=` 必須是完整白名單，至少包含 `~/.local/bin`、`~/.local/share/mise/shims`、`/usr/local/bin`、`/usr/bin`、`/bin`，使 headless `claude`／`codex` 與其相依可被執行；不得依賴 interactive shell 的 `mise activate` 或 `bash -lc`。服務以登入使用者執行，須先啟用 linger，確保登出後服務與 timer 持續可用。

**Windows 特有**：安裝時把 `%LocalAppData%\jobfinder\bin` 寫入使用者 PATH（登入時已存在的終端須重開才生效）。Agent CLI 若以 npm 安裝，`claude` 實際是 `.cmd` shim，CreateProcess 無法直接執行；`internal/agents` 因此在 Windows 上先做 PATH 解析，遇 `.cmd`／`.bat` 改經 `%COMSPEC% /c` 執行。CLI 不在服務 PATH 上時，可在 `llm.roles.<role>.<primary|fallback>.command` 直接填完整執行檔路徑。

Agent 子行程一律帶 `CREATE_NO_WINDOW` 啟動。服務本身沒有主控台，Windows 會替每個 console 子行程另配一個並顯示出來——而 `.cmd` shim 經 `cmd.exe` 執行正是這種子行程，不壓下去的話每次篩選、評分與求職信生成都會彈一個視窗。

**兩平台共有的常駐互斥**：常駐 worker 與手動 `run --stage` 由同一把鎖分隔，鎖是**核心持有於開啟中的檔案 handle**，不是「鎖檔存在與否」。行程無論怎麼結束核心都會釋放，這對 Windows 是必要條件——Task Scheduler 停止工作與關機都是直接終止行程，靠程式自行清理的鎖會在每次停止後殘留，使服務再也無法啟動。殘留的鎖檔本身不主張任何東西。

Agent 稽核資料保留在 SQLite 的 `agent_calls`。

## 4. 安裝、更新與回滾

**安裝語意集中在 binary 的三個子命令**：`jobfinder install`／`update`／`rollback`。路徑決策、token 生成、設定渲染、既有設定不覆寫、排程掛載與生效面驗證都在同一份 Go 程式碼裡，Linux 與 Windows 共用，平台差異只剩排程掛載。因此以下三條路徑得到完全相同的結果：

| 路徑 | 適用環境 | 素材 | 動作 |
|---|---|---|---|
| 下載安裝：`install.sh`／`install.ps1` | 正式環境 | release 工件 | 解析版本 → 下載工件與 `SHA256SUMS` → 驗 checksum → 解壓到暫存 → 執行解壓出的 `jobfinder install`，常駐 binary 已存在時改執行 `update` |
| 本地安裝：`install.sh --from-dir <dir>`／`install.ps1 -FromDirectory <dir>` | 測試環境 | dev 部署包 | 讀該目錄的 `SHA256SUMS` → 驗 checksum → 解壓到暫存 → 之後與上一列逐字相同 |
| 手動安裝：自行解壓工件 | 兩者 | 任一種 | 自行比對 `SHA256SUMS`，解壓後直接執行 `jobfinder install` |

**三條路徑的分界是素材從哪裡來，安裝動作本身完全相同。** 下載模式取的是某個 tag 的 release 工件，`jobfinder version` 印得出該 tag，`SHA256SUMS` 隨 Release 公開，任何人都能核對；本地模式取的是開發環境打包當下 working tree 的建置，`jobfinder version` 印 `dev (<commit>)`，對應不到任何 release，完整性由包內的 `SHA256SUMS` 保證。

**正式環境一律走 release 工件。** dev 部署包沒有公開的下載來源，也對應不到可重現的版本座標，它存在的理由是讓尚未發版的改動能在測試環境的完整安裝流程上驗過。部署包的產出見 §7。

**bootstrap 腳本另有 extension 模式**：不帶旗標即只裝後端；`--extension`（`install.sh`）／`-Extension`（`install.ps1`）只裝 extension，`--all`／`-All` 兩者都裝。extension 模式取得 `jobfinder-extension_<版本字串>.zip`、驗 checksum、解壓到 `<資料目錄>/jobfinder/extension/<版本字串>`（Linux `~/.local/share/…`、Windows `%LocalAppData%\jobfinder\extension\…`），Windows 另解除 Mark of the Web，終點是目錄就緒與印出 Chrome 的手動步驟。常駐目錄名一律是該素材的版本字串——release 工件為 tag，dev 部署包為 `dev`。extension 沒有安裝語意——無設定渲染、無 token、無排程、無生效面驗證——因此這條路留在腳本內，不進 `jobfinder install`、不併進平台工件。跑 Chrome 的那台機器不需要、也不該被裝出一個後端服務。

extension 模式同樣支援本地來源：`--from-dir`／`-FromDirectory` 下改讀該目錄的 extension zip。**release 工件的 extension ID 是常數**，`manifest.json` 帶固定 `key`，Chrome 在每台機器上推導出同一個 ID，腳本因此印得出它；dev 部署包的 `key` 已於打包時移除，ID 由載入目錄的路徑決定，腳本改為印出該目錄並要求從 `chrome://extensions` 取得實際 ID。

**移除沒有對應的子命令**：`install`／`update`／`rollback` 三者都不負責拆除。安裝根目錄底下除了安裝流程的產物，還有 bootstrap 的 extension 模式解壓出的 `extension/<tag>/`——Chrome 讀的就是那裡——所以移除是逐項進行，不是刪整棵樹。步驟見 [上手指南](guides/getting-started.md) §10.4。

解壓出的執行檔是**安裝媒介**，不是安裝本身：它把自己複製到 §2 的常駐位置，排程執行的是那份副本，安裝完下載目錄即可刪除。安裝流程會拒絕「拿常駐副本安裝到自己身上」。Windows 另從同一份工件取出 `jobfinderw.exe` 一併放置；工件缺少它時安裝直接失敗，不會裝出一個排程指向不存在檔案的組合。

| 子命令 | 動作 | 生效面驗證 |
|---|---|---|
| `install` | 建立 §2 目錄 → 渲染設定並生成隨機 token（既有者不覆寫）→ 種入範例 Profile 與空 denylist（既有者不覆寫）→ `profile lint` 閘門 → 放置 binary → 掛載排程 → 註冊 PATH → 啟動並驗證 | 見下段 |
| `update` | 保留現行 binary 至 `jobfinder.prev`（新舊為同一份建置時保留既有的 `.prev` 不覆蓋）→ 替換 binary 與排程定義 → 重啟 API → 驗證 | 同上，且啟動時間須晚於替換點 |
| `rollback` | 現行 binary 存為 `.bad`（保留前滾可能）→ 由 `.prev` 與 stash 的排程定義還原 → 重啟 API → 驗證；SQLite **不自動更動**，僅在確認毀損時由備份目錄手動還原 | 同上 |

`update` 與 `rollback` 對該平台的**全部**執行檔一起動作。Windows 只還原其中一支會讓使用者輸入的 binary 與實際在跑的服務落在不同版本，比原本要回滾的狀態更糟，因此回滾前先確認每一支都有對應的 `.prev`，缺一即拒絕。

**所有驗證打在生效面（執行中的 process），而非安裝面**：「檔案複製了」與「服務 active」都可能同時為真而執行中的仍是舊 process。

驗證失敗時附上服務自己的輸出（Linux 取 journal，Windows 取 `log.file`，取不到則附工作的 `LastTaskResult`）。「服務不是 active」只說得出症狀，而原因通常是服務啟動時已經印出來的一行——最典型的是回滾到跨 schema 版本的舊 binary，它拒絕開啟已升級的資料庫。

**`rollback` 只退一個版本**：rollback 目錄保留的前一版恰好一份，回滾不會把它往前推。因此連續執行兩次 rollback 不會退到再前一版——第二次的來源與現行是同一份建置，一律拒絕執行，否則它會把保留前滾可能的 `.bad` 覆蓋成同一版，銷毀第一次剛撤下來的那一版。要退超過一版只能取得該版工件跑 `update`。

**`rollback` 不動資料庫，因此跨 schema 版本的回滾必須先還原升級前的資料庫備份**，否則舊 binary 會因 `database schema version N is newer than supported version M` 而拒絕啟動。`update` 不代為備份。

| 驗證 | Linux | Windows |
|---|---|---|
| 執行中的就是剛裝的 binary | API service `MainPID` 的 `/proc/<pid>/exe` | `jobfinderw.exe` 的 `Win32_Process` `ExecutablePath` |
| 確實重啟過 | `ExecMainStartTimestamp` 晚於替換點 | process `CreationDate` 晚於替換點 |
| 排程已武裝 | one-shot unit 可載入、timer active 且有 `NextElapseUSecRealtime` | 抓取工作有 `NextRunTime` |
| API 只在 loopback | `api.addr` 必須是 loopback 位址（服務就綁這個位址，沒有例外） | 同左 |
| API 真的能用 | 帶 token 的 `GET /api/v1/jobs` 回 200、未帶回 401 | 同左 |

`--skip-verify` 可略過生效面檢查，僅供沒有可用使用者 session 的環境（容器映像建置、無人值守佈建）；未經生效面驗證的安裝不算已證明可用。

第一次安裝時若無設定檔，由 `configs/config.example.yaml` 渲染出絕對路徑與隨機 token 的 `config.yaml`。**每個佔位替換都要求恰好命中一次**，否則安裝直接失敗——靜默未命中會讓安裝指向相對的開發路徑，然後在無關的地方才炸。`api.extension_origin` 仍為佔位，須在載入 extension 前替換為實際 `chrome-extension://` id。Linux 的 unit 為渲染後的靜態副本，安裝前會比對並將差異吵出（template 改版或人工修改都不靜默吞掉）。

安裝流程一律不觸發抓取：只確認排程已排定下一次觸發。抓取只由每日排程、操作者手動觸發，或 Side Panel 的重新整理動作啟動。

重啟一律是**無條件重啟**，不是「有在跑才重啟」也不是「啟用」：`systemctl --user enable --now` 對執行中的服務無作用，`try-restart` 對停止中的服務無作用，兩者各自會在一半的情境下讓新 binary 躺在磁碟上而記憶體裡沒有它。同版重跑 `update` 不覆蓋 `jobfinder.prev`：回滾點必須指向前一個版本，被現行版蓋掉等於回滾指向它自己要撤銷的那一版。

Windows 的 `Start-ScheduledTask` 對已在執行的工作是 no-op，和 systemd 的 `enable --now` 是同一個陷阱：更新一律先停、等 process 真的消失、再啟動。ScheduledTasks 模組沒有單一 cmdlet 能完成重啟，`Stop-ScheduledTask` 之後必須等到工作離開 `Running` 才呼叫 `Start-ScheduledTask`；服務行程在啟動時取得環境變數，改動 PATH 或補裝 Agent CLI 後同樣要走這一步。不得將開發階段驗收（`.local-dev/dev-verify/`）的 binary、設定或 state 直接覆蓋日常使用目錄。

## 5. 後端位置的約束與維運

**支援的形態是後端與瀏覽器同機**，extension 直連 loopback。

後端位置不是「設定檔填一個 URL」可決定的：extension 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，`api.addr` 也只決定綁哪個 loopback port，且 `internal/install` 的生效面驗證會拒絕非 loopback 的位址。後端放在別台機器時，唯一可行的接法是把那台的 loopback port 轉送到本機 loopback，extension Options 填該本機 endpoint；這條路由使用者自行實作與維護，本 repo 不提供作法。把 service 改綁 `0.0.0.0` 不是替代方案——它讓 API 暴露在網路上，而驗證只有一個 Bearer token。

讓 Options 的位址欄位真正可以填遠端主機，需要 extension 改用 `optional_host_permissions` 於 runtime 請求授權，屬 `docs/roadmap.md` 的 S2 項目。

日常診斷見 §3 的診斷欄與 Side Panel 的 Run 歷史。驗證 systemd 環境時，以 `systemd-run --user --wait --pipe` 執行相同 binary／設定組合，API 使用 transient service，timer 使用 transient timer 實際觸發 one-shot；互動 shell 成功不構成 service 環境成功的證據。user bus 不可用時，開發驗收回 `ENVIRONMENT_BLOCKED`，不誤判為產品失敗。

## 6. 只收集不判定的暫停模式

`worker.paused: true` 讓 `serve` 只提供 API 而不啟動常駐 worker，也不持有 worker lock。抓取、清單擷取與內頁擷取照常寫入，職缺一律停在 `new`，不呼叫任何 Agent、不產生判定，因此 Profile 尚未定案時不會累積之後必須作廢的結論。此時：

| 動作 | 指令 |
|---|---|
| 手動跑一批篩選 | `jobfinder run --stage filter --limit <n>` |
| 手動跑一批評分 | `jobfinder run --stage score --limit <n>` |
| 恢復常駐消化 | 改回 `paused: false` 後重啟 API（Linux `systemctl --user restart jobfinder-api.service`；Windows 停止 api 工作、等行程消失、再啟動） |

`llm.max_*_per_day` 不是暫停開關：值為 0 或負數代表**不設上限**，不是不執行。

日常要暫停 token 消耗用的是 Side Panel 系統頁的**自動篩選與評分**開關（存於資料庫，重啟仍生效）：worker 保持常駐並持有鎖，只是不自動取件，使用者仍可對單筆按「馬上處理」。`worker.paused` 則是部署期把三個階段整個交給 CLI 批次的模式，此模式下該開關與「馬上處理」皆無作用。

結構化硬規則仍會在清單擷取時就地判定（不花 token），故暫停期間仍可能出現 `filtered_out`；改動 `requirements` 會改變 `filter_revision`，之後的重新處理會把這些結論一併重跑。

清空既有職缺重新開始時，停止 `jobfinder-api.service` 與 `jobfinder-run.timer` 後刪除 SQLite（連同 `-wal`、`-shm`），下次啟動即以最新 schema 重建空庫。Profile、denylist 與設定不受影響。

## 7. CI 與 release 工件

**版號的單一真相是 git tag `v<MAJOR>.<MINOR>.<PATCH>`**，Go binary 與 extension 的版號皆由 tag 推導，不在原始碼中另存一份。

| workflow | job | 觸發 | 動作 |
|---|---|---|---|
| `.github/workflows/ci.yml` | `check`（ubuntu） | pull request、push 至 `main` | 以 `mise.toml` 鎖定的工具鏈執行 gofumpt 檢查（只檢查不改寫）、`lint`、`test`，並確認 extension manifest 可解析 |
| `.github/workflows/ci.yml` | `windows`（windows-latest） | 同上 | `go build`／`go test`、Task Scheduler 模板可被 XML 解析、`install.ps1` 語法檢查與其 `SHA256SUMS` 比對的實際呼叫。Windows 是受支援平台，路徑類錯誤不得只在 release 才暴露 |
| `.github/workflows/release.yml` | `release` | push tag `v*` | 驗證 tag 格式 → 重跑 lint／test → 呼叫 `scripts/release/pack.sh` 產出工件與 checksum → 建立 GitHub Release |

release 工件：

| 工件 | 內容 |
|---|---|
| `jobfinder_<tag>_<os>_<arch>.tar.gz` | 靜態 binary（`CGO_ENABLED=0`、`-trimpath`，版號經 `-ldflags` 注入 `internal/version.tag`）＋ `LICENSE`、`README.md`、`configs/`。Linux 另附 `systemd/` unit 模板與 `install.sh`。平台為 `linux/amd64`、`linux/arm64`、`darwin/arm64`、`darwin/amd64`；darwin 只有 binary 與 `configs/`（非部署平台） |
| `jobfinder_<tag>_windows_amd64.zip` | 同上，`jobfinder.exe` 與排程執行用的 `jobfinderw.exe`（同源、`-H=windowsgui`），另附 `windows/` Task Scheduler 模板與 `install.ps1` |
| `jobfinder-extension_<tag>.zip` | extension 目錄，`manifest.json` 的 `version` 於打包時改寫為 tag 去掉 `v` 的語意版號 |
| `SHA256SUMS` | 上述所有工件的 SHA256 |

**打包邏輯集中在 `scripts/release/pack.sh`**，吃版本字串與目標平台清單，產出上表的工件形狀。它有兩個呼叫者：`release.yml` 帶 tag 與全部平台，開發環境帶 `dev` 與受測平台。兩者共用同一支腳本，因此「dev 部署包與 release 工件同形狀」是機械保證，不靠人維持。壓縮不使用 `zip` 指令，開發機不一定有它。

**dev 部署包**是測試環境的素材，工件形狀與上表逐項相同，差別只在版本字串為 `dev` 而非 tag，以及它落在開發環境的本機目錄、不進 GitHub Release。版本字串為 `dev` 時打包另做兩件事：移除 `manifest.json` 的固定 `key`、把 `name` 改為 `jobfinder (dev)`。前者讓 Chrome 改以載入目錄推導 ID，後者讓兩張卡片在清單上分得開——dev extension 與正式 extension 因此能並存於同一個 Chrome，而這是打包的保證，不是操作步驟。

版號的唯一決策點是 `internal/version`：逐欄位取 ldflags 注入值 → `debug.ReadBuildInfo()` → 寫死的 fallback。`jobfinder version` 因此在任何建置路徑都印得出可辨識的身分——release 建置印 tag，本機建置印 `dev (<commit>) (dirty)`，不偽造版本號。

`-X` 的符號路徑 `github.com/dccoding1118/job-finder/internal/version.tag` 是字串綁定：package 搬家或變數改名會使注入**靜默失效**，版號悄悄退回 `dev`。改動時必須同步 `release.yml`。

工件內容即安裝流程所需的全部素材：`configs/` 供設定渲染、平台目錄供排程掛載、bootstrap 腳本供安裝入口使用。安裝流程預設從執行檔所在目錄取素材，`--assets` 可另行指定；release 工件與 dev 部署包的佈局相同，因此兩種素材共用同一份實作。

**extension 與後端必須同版**。兩者由同一個 tag 一起發出，但執行期沒有版本協商或相容性檢查：舊版 extension 連新版 API 一樣通過認證、Side Panel 一樣載入，只在個別功能上安靜地行為不對。extension 不由 `jobfinder install` 取得——它是獨立工件，平台工件內不含它；bootstrap 腳本的 extension 模式負責下載與解壓，載入 Chrome 仍是人工步驟，後端換版時一併更換。

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
