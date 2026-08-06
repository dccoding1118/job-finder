# 變更 — 標準釋出流程：Linux／Windows 雙平台本機部署

## 1. 背景與動機

原本的部署形態只有一種：Linux ＋ systemd user unit，由 `scripts/deploy/` 三支 bash 腳本從開發 checkout 重新建置並安裝。這帶來三個彼此相關的問題。

**路徑沒有決策點**。`--config` 的預設值是相對路徑 `config.yaml`，真實路徑寫死在 `scripts/deploy/lib.sh` 的 XDG 變數裡，systemd unit 模板裡又各寫一次。同一件事被描述三遍，而 Windows 沒有 XDG，這三處沒有一處能回答「Windows 該用哪裡」。

**安裝語意寫在 shell 裡**。token 生成、設定渲染、既有設定不覆寫、生效面驗證都是 bash，只能在 Linux 執行。Windows 要跑起同一套語意，等於把整份邏輯用 PowerShell 再寫一次，兩份必然分岔。

**只有開發 checkout 一條安裝路徑**。使用者要安裝就得 clone、裝 mise、跑完整 build gate。release 工件已在產出（含 `windows/amd64`），卻沒有任何流程使用它。

## 2. 決策摘要

| 項目 | 決策 |
|---|---|
| 路徑決策點 | 集中於 `internal/paths`：CLI 旗標預設、安裝流程、模板渲染全部向它取值；`jobfinder paths` 對外印出 |
| 安裝語意 | 集中於 binary 的 `install`／`update`／`rollback` 子命令（`internal/install`），Linux 與 Windows 共用同一份 Go 實作 |
| 平台差異 | 只剩排程掛載（systemd user unit ／ Task Scheduler）與日誌出口（journald ／ 檔案輪替） |
| 安裝腳本 | `install.sh`／`install.ps1` 降為薄 bootstrap：解析版本 → 下載 → 驗 `SHA256SUMS` → 解壓 → 交棒給 `jobfinder install` |
| 開發 checkout | `scripts/deploy/*.sh` 降為 wrapper：跑 `fmt`／`lint`／`test`／`build`，再把剛建置的 binary 交給同一組子命令；`mise run deploy-*` 維持可用 |
| Windows 權限 | 明載退讓：無 `chmod` 等價物，保護來自 `%LocalAppData%` 繼承的 ACL |
| 支援平台 | Linux 與 Windows。macOS 有 binary 工件但不是部署平台，工件不含排程模板與 bootstrap |

## 3. 安裝媒介與安裝本身

解壓出的執行檔是**安裝媒介**：它把自己複製到常駐位置，排程執行的是那份副本，安裝完下載目錄即可刪除。安裝流程會拒絕「拿常駐副本安裝到自己身上」。

三條取得路徑因此得到完全相同的結果，使用者不必信任腳本才能安裝：

| 路徑 | 誰做下載與驗證 | 誰做安裝 |
|---|---|---|
| bootstrap 腳本 | 腳本 | `jobfinder install` |
| 手動下載工件 | 使用者自行比對 `SHA256SUMS` | `jobfinder install` |
| 開發 checkout | 不下載，就地建置 | `jobfinder install` |

此形態與 Elastic Agent、k0s、Telegraf、GitLab Runner 一致。

## 4. 生效面驗證的跨平台對應

「檔案複製了」與「服務 active」可能同時為真，而執行中的仍是舊 process。驗證因此一律打在執行中的 process 上。

| 驗證 | Linux | Windows |
|---|---|---|
| 執行中的就是剛裝的 binary | `/proc/<MainPID>/exe` | `Win32_Process.ExecutablePath` |
| 確實重啟過 | `ExecMainStartTimestamp` 晚於替換點 | process `CreationDate` 晚於替換點 |
| 排程已武裝 | timer active 且有 `NextElapseUSecRealtime` | 抓取工作有 `NextRunTime` |
| API 只在 loopback、且真的能用 | `api.addr` 為 loopback；帶 token 200、未帶 401 | 同左 |

`Start-ScheduledTask` 對已在執行的工作是 no-op，與 systemd `enable --now` 是同一個陷阱：更新一律先停、等 process 真的消失、再啟動。

排程狀態一律以 PowerShell 的 ScheduledTasks cmdlet 取物件屬性，不解析 `schtasks` 的文字輸出——後者以安裝語系呈現，會讓檢查隨使用者語言而通過或失敗。

## 5. Windows 的四項具體落差與處置

| 落差 | 處置 |
|---|---|
| 無 journald | Task Scheduler 丟棄工作輸出，故安裝時填入 `log.file`；記錄依大小輪替、保留固定份數。Linux 維持 stderr → journald，可自行啟用同一檔案輸出 |
| 無 `chmod` | 明載退讓：全部落在 `%LocalAppData%`，保護來自該目錄繼承的 ACL。不做無效的 `chmod` 假動作 |
| binary 不在 PATH | 安裝時把 `%LocalAppData%\jobfinder\bin` 寫入使用者 PATH（登入時已開的終端須重開） |
| Agent CLI 是 `.cmd` shim | `internal/agents` 在 Windows 先做 PATH 解析，遇 `.cmd`／`.bat` 改經 `%COMSPEC% /c`；另開放 `llm.roles.<role>.<primary\|fallback>.command` 直接指定完整執行檔路徑 |

## 6. 防止靜默失效的兩處硬性檢查

- **設定渲染的每個佔位替換必須恰好命中一次**，否則安裝直接失敗。靜默未命中會讓安裝指向相對的開發路徑，然後在無關的地方才炸。
- **CI 新增 `windows-latest` job**（build／test／排程模板 XML 可解析／`install.ps1` 語法）。Windows 成為受支援平台後，路徑類錯誤不得只在 release 才暴露。`internal/paths` 的單元測試亦可從 Linux 斷言 Windows 版面。

## 7. 影響範圍

| 面向 | 內容 |
|---|---|
| 新增 | `internal/paths`、`internal/install`、`internal/logging`；`jobfinder install`／`update`／`rollback`／`paths`；`deploy/production/windows/` 兩份 Task Scheduler 模板；`scripts/bootstrap/install.sh`／`install.ps1`；`.gitattributes`；CI 的 `windows` job |
| 變更 | `--config`／`--profile`／`--denylist` 預設值改由 `internal/paths` 決定；`config.example.yaml` 增 `log` 區段；`llm.roles` 端點增 `command`；`scripts/deploy/*.sh` 降為 wrapper；release 工件增 `configs/`、bootstrap 腳本與 Windows 排程模板 |
| 移除 | `scripts/deploy/lib.sh`（安裝語意已移入 binary） |
| 不變 | 資料模型、判定語意、API 契約、extension 行為 |

`docs/guides/runbook-extension.md` 由主線降為選配：本機部署落地後通道不再是預設路徑。通道的常駐目錄一併改為 `%LOCALAPPDATA%\jobfinder-tunnel\`，與本機安裝根目錄分開。
