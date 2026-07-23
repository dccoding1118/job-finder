# Runbook — Windows extension 與 GCP API 常駐通道

本手冊說明如何讓 Windows Chrome extension 經由背景常駐的 SSH local forward，連到 GCP VM 上只監聽 loopback 的 job-finder API。通道由 Windows Task Scheduler 在使用者登入時啟動，斷線後自動重建，不需要保留 PowerShell、Command Prompt 或 SSH 視窗。

Extension 的載入與 Chrome compatibility gate 也使用同一套設定。設計契約見 [design-extension](../designs/design-extension.md) 與 [design-api](../designs/design-api.md)，正式部署基線見 [deploy](../deploy.md)。

## 1. 拓撲與安全邊界

正式執行拓撲如下：

```text
Windows Chrome extension
  → http://127.0.0.1:18686
  → Windows 背景 gcloud／SSH tunnel
  → GCP IAP → VM SSH
  → VM 127.0.0.1:8686 job-finder API
```

| 限制 | 規則 |
|---|---|
| VM API listener | `api.addr` 固定為 `127.0.0.1:8686`；不得改綁 `0.0.0.0`。 |
| Windows listener | 固定綁 `127.0.0.1:18686`；不得使用 `0.0.0.0:18686`。 |
| GCP ingress | 只需讓 IAP 的來源範圍存取 VM SSH port；不開放 API port `8686`。 |
| API 驗證 | 每個 request 仍須帶 `api.token`；帶 Origin 時必須符合固定 extension origin。 |
| Extension endpoint | Options 使用 `http://127.0.0.1:18686`，不填 VM IP。 |

Windows 使用專用 local port `18686`，避免與 VM API port `8686` 同號，也降低 VS Code Port Forward 先占用 `8686` 的機率。若 `18686` 已被其他程式使用，應先找出並停止占用者；不要任意改成一個未記錄的埠。

關鍵常數：

| 項目 | 值 |
|---|---|
| 固定 unpacked extension ID | `oddnhajjhmgogefocnljofeahniodiei` |
| `api.extension_origin` | `chrome-extension://oddnhajjhmgogefocnljofeahniodiei` |
| Extension endpoint | `http://127.0.0.1:18686` |
| Windows local forward | `127.0.0.1:18686` |
| VM API target | `127.0.0.1:8686` |
| Windows 常駐目錄 | `%LOCALAPPDATA%\jobfinder\` |
| Windows 排程工作名稱 | `Jobfinder-Api-Tunnel` |
| VM 設定檔 | `~/.config/jobfinder/config.yaml` |

## 2. 前置條件

開始前先準備以下值；本文以大寫佔位符表示，執行時必須替換：

| 佔位符 | 內容 | 查詢方式 |
|---|---|---|
| `PROJECT_ID` | GCP project ID | `gcloud projects list` |
| `ZONE` | VM zone，例如 `asia-east1-b` | `gcloud compute instances list` |
| `VM_NAME` | Compute Engine instance name | `gcloud compute instances list` |
| `<token>` | VM `config.yaml` 的 `api.token` | 僅在 VM 本機讀取，不寫入本手冊或版控 |

Windows 端需要：

- Windows 10 或 Windows 11。
- Google Cloud CLI；`gcloud version` 可成功執行。
- 可登入目標 GCP project 的 Google 帳號。
- Chrome 與本專案的 `extension/` artifact。

GCP 端需要：

- VM 已完成 [deploy](../deploy.md) 的正式安裝。
- VM 的 `jobfinder-api.service` 正在執行。
- 使用者具備 IAP tunnel 與 Compute Engine SSH 所需權限。
- VPC firewall 允許 IAP TCP forwarding 來源 `35.235.240.0/20` 存取 VM 的 TCP `22`。
- API port `8686` 不對 VPC 或 Internet 開放。

若目前已能從 Windows 執行 `gcloud compute ssh VM_NAME --zone ZONE --tunnel-through-iap`，代表 IAP、IAM、SSH key 與 firewall 的基本前置已完成。否則先依 [Google Cloud IAP TCP forwarding](https://cloud.google.com/iap/docs/using-tcp-forwarding) 設定；不要用開放 VM 公網 SSH 或 API port 取代 IAP。

## 3. VM 端設定與驗證

### 3.1 安裝並設定 API

在 VM 執行正式安裝：

```bash
mise run deploy-install
```

編輯 `~/.config/jobfinder/config.yaml`，確認以下設定：

| 欄位 | 值 |
|---|---|
| `api.addr` | `127.0.0.1:8686` |
| `api.extension_origin` | `chrome-extension://oddnhajjhmgogefocnljofeahniodiei` |
| `api.token` | 非空、隨機且未進版控的 token |

設定修改後重啟 API：

```bash
systemctl --user restart jobfinder-api.service
systemctl --user status jobfinder-api.service
```

### 3.2 確認 listener 與 token

先在 VM 上確認帶 token 回 `200`、未帶 token 回 `401`：

```bash
curl -so /dev/null -w '%{http_code}\n' \
  -H 'Authorization: Bearer <token>' \
  http://127.0.0.1:8686/api/v1/jobs

curl -so /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:8686/api/v1/jobs
```

確認 API 只監聽 loopback：

```bash
ss -lntp | grep ':8686'
```

預期 listener 是 `127.0.0.1:8686` 或等價 loopback，不得是 `0.0.0.0:8686`、VM 私網 IP 或公網 IP。

## 4. Windows 一次性初始化

以下步驟使用一般 Windows PowerShell，不需要系統管理員權限。

### 4.1 驗證工具

```powershell
gcloud version
```

若 `gcloud` 不存在，先安裝並重新開啟 PowerShell：[Install the Google Cloud CLI](https://cloud.google.com/sdk/docs/install-sdk#windows)。Windows 版 Google Cloud CLI 內附 PuTTY，`gcloud compute ssh` 預設呼叫該 PuTTY 執行檔；本手冊不假設它使用 Windows OpenSSH。

### 4.2 登入並設定 project

```powershell
gcloud auth login
gcloud config set project PROJECT_ID
gcloud auth list --filter=status:ACTIVE
```

最後一個指令應顯示預定用來建立 tunnel 的帳號。背景工作會沿用此 Windows 使用者的 gcloud credential，因此排程工作必須由同一使用者執行。

### 4.3 先完成一次互動式 SSH

```powershell
gcloud compute ssh VM_NAME `
  --project PROJECT_ID `
  --zone ZONE `
  --tunnel-through-iap
```

第一次執行可能建立 SSH key、要求確認 host key，或要求重新登入。完成後輸入 `exit` 回到 Windows。這些互動動作必須先在前景完成，不能留給背景排程處理。

背景連線不能處理互動 prompt，因此 PuTTY private key 必須能由此 Windows 使用者非互動地使用。Windows gcloud 預設 key 位於 `%USERPROFILE%\.ssh\google_compute_engine.ppk`；若 key 需要 passphrase，應先以 Pageant 載入並確認重新登入後仍可取得 key，否則背景連線會失敗。無 passphrase 的專用 key 必須依賴 Windows 帳號權限與 BitLocker 保護。

### 4.4 確認 local port 未被占用

```powershell
Get-NetTCPConnection `
  -LocalAddress 127.0.0.1 `
  -LocalPort 18686 `
  -State Listen `
  -ErrorAction SilentlyContinue
```

無輸出代表可使用。若有輸出，查出占用程序：

```powershell
$Listener = Get-NetTCPConnection `
  -LocalPort 18686 `
  -State Listen `
  -ErrorAction Stop

Get-Process -Id $Listener.OwningProcess
```

若占用者是 VS Code，先在 VS Code 的 Ports 面板停止該 forward，再重新檢查。不要直接終止不明程序。

### 4.5 前景測試完整通道

先在 PowerShell 前景建立 tunnel：

```powershell
gcloud compute ssh VM_NAME `
  --project PROJECT_ID `
  --zone ZONE `
  --tunnel-through-iap `
  --ssh-flag="-N" `
  --ssh-flag="-L 127.0.0.1:18686:127.0.0.1:8686"
```

Windows PowerShell 一律使用重複的 `--ssh-flag="..."` 把參數交給底層 PuTTY。不要改用獨立的 `--` 後接參數；部分 Windows gcloud 執行路徑會讓後續參數仍落入 gcloud parser，並回報 `unrecognized arguments`。也不要加入 OpenSSH 專用的 `-o ExitOnForwardFailure=...`、`-o ServerAliveInterval=...`、`-o ServerAliveCountMax=...` 或 `-o ConnectTimeout=...`；Windows gcloud 顯示執行檔為 `sdk\putty.exe` 時，這些選項會讓 PuTTY 以 return code `1` 結束。`-N` 與 `-L` 是 PuTTY 支援的通道選項。

保持該 PowerShell 開啟，另開一個 PowerShell 驗證 API：

```powershell
$JobfinderToken = Read-Host 'API token'

curl.exe `
  --connect-timeout 5 `
  --max-time 15 `
  --output NUL `
  --silent `
  --show-error `
  --write-out "%{http_code}`n" `
  --header "Authorization: Bearer $JobfinderToken" `
  http://127.0.0.1:18686/api/v1/jobs

Remove-Variable JobfinderToken
```

預期回 `200`。完成後回到 tunnel 視窗按 `Ctrl+C`；再次執行同一個 `curl.exe` 應連線失敗，證明流量確實經過 tunnel，而非其他 listener。

## 5. 建立背景自動重連 wrapper

### 5.1 建立 Windows 本機目錄

```powershell
New-Item `
  -ItemType Directory `
  -Force `
  -Path "$env:LOCALAPPDATA\jobfinder" | Out-Null

notepad "$env:LOCALAPPDATA\jobfinder\jobfinder-tunnel.ps1"
```

將以下內容貼入檔案，替換前三個佔位值後儲存。此檔不得包含 `api.token`、Profile、JD 或其他 PII。

```powershell
$ProjectId = 'PROJECT_ID'
$Zone = 'ZONE'
$VmName = 'VM_NAME'
$LocalPort = 18686
$RemotePort = 8686
$RetrySeconds = 10

$ErrorActionPreference = 'Continue'
$RuntimeDir = Join-Path $env:LOCALAPPDATA 'jobfinder'
$LogPath = Join-Path $RuntimeDir 'tunnel.log'
$Gcloud = (Get-Command gcloud.cmd -ErrorAction Stop).Source

New-Item -ItemType Directory -Force -Path $RuntimeDir | Out-Null

while ($true) {
    $StartedAt = Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'
    "[$StartedAt] starting tunnel" | Add-Content -Path $LogPath

    & $Gcloud compute ssh $VmName `
        --project $ProjectId `
        --zone $Zone `
        --tunnel-through-iap `
        --quiet `
        --ssh-flag="-N" `
        --ssh-flag="-L 127.0.0.1:${LocalPort}:127.0.0.1:${RemotePort}" *>> $LogPath

    $ExitCode = $LASTEXITCODE
    $StoppedAt = Get-Date -Format 'yyyy-MM-ddTHH:mm:ssK'
    "[$StoppedAt] tunnel exited code=$ExitCode; retrying in ${RetrySeconds}s" |
        Add-Content -Path $LogPath

    Start-Sleep -Seconds $RetrySeconds
}
```

這個 wrapper 的存活模型如下：

| 情況 | 行為 |
|---|---|
| 網路短暫中斷、Windows 換網路 | IAP tunnel 先嘗試重連；PuTTY／gcloud process 若退出，wrapper 10 秒後重建完整 session。 |
| VM 重啟 | 既有 process 結束後，wrapper 持續重試，直到 VM SSH 恢復。 |
| Local port 已占用 | PuTTY 無法建立 forward 並退出；wrapper 持續重試並在 log 留下原因。 |
| Credential 失效或需互動 | `--quiet` 禁止 gcloud prompt；需人工重新登入或處理 PuTTY key／host key。 |
| Wrapper 本身意外終止 | Task Scheduler 依 §6 設定重新啟動。 |

### 5.2 手動測試 wrapper

```powershell
powershell.exe `
  -NoLogo `
  -NoProfile `
  -ExecutionPolicy Bypass `
  -File "$env:LOCALAPPDATA\jobfinder\jobfinder-tunnel.ps1"
```

另開 PowerShell，以 §4.5 的 `curl.exe` 驗證 `200`。然後在 wrapper 視窗按 `Ctrl+C`，確認 local listener 消失。

檢查 log：

```powershell
Get-Content `
  "$env:LOCALAPPDATA\jobfinder\tunnel.log" `
  -Tail 50
```

`ExecutionPolicy Bypass` 只套用到這個 PowerShell process，不修改全機 execution policy。Wrapper 檔案位於目前使用者的 `%LOCALAPPDATA%`，不得放到多人可寫目錄。

## 6. 設定 Windows Task Scheduler

使用 Windows「工作排程器」建立工作；選擇「建立工作」，不要使用精簡的「建立基本工作」。

### 6.1 一般

| 欄位 | 設定 |
|---|---|
| 名稱 | `Jobfinder-Api-Tunnel` |
| 描述 | `Windows loopback to GCP VM job-finder API through SSH/IAP` |
| 執行身分 | 目前已完成 `gcloud auth login` 的 Windows 使用者 |
| 執行模式 | `只有使用者登入時才執行` |
| 使用最高權限執行 | 不勾選 |
| 隱藏 | 勾選 |

只有使用者登入時執行，可確保排程使用同一份使用者 profile、gcloud credential 與 SSH key。Chrome 也只有登入後才會使用 extension，因此不需要在登出狀態維持 tunnel。

### 6.2 觸發程序

新增觸發程序：

| 欄位 | 設定 |
|---|---|
| 開始工作 | `登入時` |
| 使用者 | 指定目前 Windows 使用者 |
| 延遲工作 | `1 分鐘` |
| 已啟用 | 勾選 |

延遲 30 秒可讓 Windows 網路與使用者 profile 先完成初始化；即使網路更晚才可用，wrapper 仍會每 10 秒重試。

### 6.3 動作

新增「啟動程式」動作：

| 欄位 | 設定 |
|---|---|
| 程式或指令碼 | `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` |
| 新增引數 | 見下方；將 `<WINDOWS_USER>` 換成實際使用者目錄名稱 |
| 開始位置 | `C:\Users\<WINDOWS_USER>\AppData\Local\jobfinder` |

新增引數填入單一行：

```text
-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "C:\Users\<WINDOWS_USER>\AppData\Local\jobfinder\jobfinder-tunnel.ps1"
```

使用絕對路徑，不使用 `%LOCALAPPDATA%`、`~` 或相對路徑，避免 Task Scheduler 的環境展開差異。

### 6.4 條件

| 欄位 | 設定 |
|---|---|
| 只有在電腦使用 AC 電源時才啟動 | 不勾選 |
| 若電腦切換成電池電源則停止 | 不勾選 |
| 只有在網路連線可用時才啟動 | 不勾選 |
| 喚醒電腦執行 | 不需要 |

不使用「只有在網路連線可用時」條件，避免 Task Scheduler 選錯 network profile 或漏掉登入時機；wrapper 自己負責等待網路恢復。

### 6.5 設定

| 欄位 | 設定 |
|---|---|
| 允許依需求執行工作 | 勾選 |
| 錯過排定開始時間後儘快執行 | 勾選 |
| 工作失敗時重新啟動 | 每 `1 分鐘`，嘗試 `999` 次 |
| 工作執行超過指定時間則停止 | 不勾選 |
| 工作要求停止時強制停止 | 勾選 |
| 工作已在執行時 | `不要啟動新執行個體` |

Wrapper 正常情況會長期執行；不得保留 Task Scheduler 預設的三天 execution time limit，否則 tunnel 會定期被停止。

### 6.6 啟動並驗證排程

儲存工作後，在 PowerShell 執行：

```powershell
Start-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Start-Sleep -Seconds 5
Get-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Get-ScheduledTaskInfo -TaskName 'Jobfinder-Api-Tunnel'
```

預期工作狀態為 `Running`。確認 listener：

```powershell
Get-NetTCPConnection `
  -LocalAddress 127.0.0.1 `
  -LocalPort 18686 `
  -State Listen
```

再以 §4.5 的 `curl.exe` 驗證回 `200`。驗證完成後關閉所有 PowerShell 視窗；listener 與 API 存取仍應保持正常，證明 tunnel 已在背景執行。

最後登出並重新登入 Windows，等待約 30 秒後重做 listener 與 `curl.exe` 檢查，驗證登入自啟。

## 7. 設定 Chrome extension

### 7.1 載入 extension

1. 將同一正式 release 的 `extension/` artifact 放到 Windows 本機固定目錄。
2. 開啟 Chrome `chrome://extensions`。
3. 開啟「開發人員模式」。
4. 選擇「載入未封裝項目」，指向 `extension/` 目錄。
5. 確認 ID 是 `oddnhajjhmgogefocnljofeahniodiei`。

若 ID 不符，先確認載入的 artifact 與 `manifest.json`；不要直接放寬 VM 的 `extension_origin`。若 release 確實變更固定 key，必須同步更新正式設計、VM 設定與驗收契約。

### 7.2 設定 endpoint 與 token

1. 在 `chrome://extensions` 的 job-finder 卡片開啟「詳細資料」。
2. 開啟「擴充功能選項」。
3. API endpoint 填入 `http://127.0.0.1:18686`。
4. API token 填入 VM `config.yaml` 的 `api.token`。
5. 儲存。

Token 欄位儲存後清空是預期行為；token 已存入 extension 的 local storage，不應顯示在 Side Panel 或 log。

## 8. 完整驗收

### 8.1 通道驗收

| 驗收項 | 操作 | 預期 |
|---|---|---|
| 背景執行 | 關閉所有 PowerShell／CMD 視窗後呼叫 API | 回 `200` |
| 登入自啟 | 登出再登入，等待 30–60 秒 | Task 為 `Running`，port `18686` 有 listener |
| 自動重連 | VM 執行 `sudo reboot`，或短暫中斷 Windows 網路後恢復 | extension 暫時離線；VM／網路恢復後 tunnel 自動重建 |
| 單一 instance | 工作正在執行時再次 `Start-ScheduledTask` | 不產生第二個 listener 或第二個 wrapper |
| Loopback-only | 檢查 Windows listener | 只有 `127.0.0.1:18686`，不是 `0.0.0.0:18686` |
| Token gate | 分別以有效 token 與無 token 呼叫 | `200`／`401` |

自動重連驗收應允許 VM 開機與 API service 啟動所需時間。Windows wrapper 會持續重試，不需要人工重開 tunnel；若五分鐘後仍未恢復，再進入 §10 排障。

### 8.2 Chrome compatibility gate

點擊 extension toolbar action 開啟 Side Panel，依 [verify](../verify.md) §4 的 V4 人工步驟逐項核對。自動 Chromium E2E 不替代真 Windows Chrome、背景 tunnel 與正式 API 的人工結論。

人工驗收不另建含敏感內容的 evidence。自行保留截圖或筆記時，不得包含 token、真實 JD 全文、求職信全文或日常 SQLite 資料。

## 9. 日常操作

### 9.1 查詢狀態

```powershell
Get-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Get-ScheduledTaskInfo -TaskName 'Jobfinder-Api-Tunnel'

Get-NetTCPConnection `
  -LocalAddress 127.0.0.1 `
  -LocalPort 18686 `
  -State Listen `
  -ErrorAction SilentlyContinue

Get-Content `
  "$env:LOCALAPPDATA\jobfinder\tunnel.log" `
  -Tail 50
```

### 9.2 手動重啟

```powershell
Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Start-Sleep -Seconds 3
Start-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
```

重啟後先確認 listener，再用帶 connect／total timeout 的 `curl.exe` 驗證；不要讓診斷 request 無限等待。

### 9.3 更新 VM、project 或 zone

1. `Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'`。
2. 編輯 `%LOCALAPPDATA%\jobfinder\jobfinder-tunnel.ps1` 頂端常數。
3. 在前景重新執行 §4.3 與 §4.5。
4. 啟動排程並完成 §6.6 驗證。

### 9.4 Log 維護

Tunnel log 只應記時間、gcloud／SSH 診斷與 exit code，不得寫入 API token 或 request body。需要清空時先停止工作：

```powershell
Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Clear-Content "$env:LOCALAPPDATA\jobfinder\tunnel.log"
Start-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
```

## 10. 疑難排解

依序檢查，不要一開始就重裝 extension 或開放 VM API port。

### 10.1 Extension 顯示 API 離線

1. 檢查排程狀態。
2. 檢查 `127.0.0.1:18686` listener。
3. 查看 `tunnel.log` 最後 50 行。
4. 手動重啟排程。
5. 用 §4.5 的 `curl.exe` 驗證。

| 觀察 | 可能原因 | 處置 |
|---|---|---|
| Task 不是 `Running` | Wrapper 未啟動、路徑錯誤或 PowerShell 啟動失敗 | 檢查 Task 的 action、絕對路徑與 `LastTaskResult`；以前景方式執行 wrapper。 |
| Task 是 `Running`，沒有 listener | gcloud／SSH 正在重試；credential、IAP、IAM、VM 或 port 可能異常 | 讀 `tunnel.log`；以前景 `gcloud compute ssh ... --troubleshoot --tunnel-through-iap` 診斷。 |
| Listener 存在，`curl` timeout | SSH session 僵死或 VM API 未回應 | 重啟 Task；在 VM 檢查 `jobfinder-api.service`。 |
| `curl` 回 `401` | Token 不一致 | 從 VM owner-only 設定重新取得 token 並更新 Options。 |
| `curl` 回 `200`，extension 仍失敗 | Endpoint、extension token、Origin 或 extension artifact 不一致 | 確認 Options endpoint、固定 ID 與 VM `api.extension_origin`。 |

### 10.2 Local port 被占用

Log 常見訊息為 `Address already in use` 或 `cannot listen to port`。執行：

```powershell
$Listener = Get-NetTCPConnection `
  -LocalPort 18686 `
  -State Listen `
  -ErrorAction Stop

Get-Process -Id $Listener.OwningProcess
```

若占用者不是目前的 `ssh`／`gcloud` 子程序，先確認程式用途再停止。VS Code Port Forward 是已知占用來源；應從 Ports 面板移除該 forward。若占用者是殘留的 job-finder tunnel，先停止排程，確認所有相關子程序已結束，再啟動排程。

### 10.3 Gcloud credential 失效

Log 若出現 login、credential、reauthentication 或 `Invalid Credentials`：

```powershell
Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
gcloud auth login
gcloud auth list --filter=status:ACTIVE
gcloud compute ssh VM_NAME --project PROJECT_ID --zone ZONE --tunnel-through-iap
Start-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
```

登入完成後必須先成功進入一次互動式 SSH，再恢復背景工作。

### 10.4 IAP、IAM、firewall 或 VM 異常

```powershell
gcloud compute ssh VM_NAME `
  --project PROJECT_ID `
  --zone ZONE `
  --tunnel-through-iap `
  --troubleshoot
```

檢查項目：

- VM 是否為 `RUNNING`。
- 帳號是否仍有 `iap.tunnelInstances.accessViaIAP` 與 SSH 所需權限。
- IAP firewall 是否只允許 `35.235.240.0/20` 到 TCP `22`。
- VM 的 SSH service 是否正常。
- 公司網路或代理是否阻擋 IAP TCP domain。

### 10.5 VM API service 異常

在 VM 執行：

```bash
systemctl --user status jobfinder-api.service
journalctl --user -u jobfinder-api.service -n 100 --no-pager
ss -lntp | grep ':8686'
```

API 修復後不需重開 Chrome；Windows tunnel 會持續存在或自動重建，extension 再次請求即可恢復。

## 11. 停用與移除

### 11.1 暫時停用

```powershell
Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Disable-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
```

### 11.2 重新啟用

```powershell
Enable-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
Start-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel'
```

### 11.3 完整移除

先停止並移除 Task：

```powershell
Stop-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel' -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName 'Jobfinder-Api-Tunnel' -Confirm:$false
```

確認 `127.0.0.1:18686` 已無 listener，再由使用者自行刪除 `%LOCALAPPDATA%\jobfinder\`。移除 Windows tunnel 不會刪除 VM 上的 job-finder API、SQLite、設定或 systemd units。

## 12. 官方參考

- [Google Cloud：Use IAP for TCP forwarding](https://cloud.google.com/iap/docs/using-tcp-forwarding)
- [Google Cloud CLI：gcloud compute ssh](https://cloud.google.com/sdk/gcloud/reference/compute/ssh)
- [Microsoft：ScheduledTasks PowerShell module](https://learn.microsoft.com/powershell/module/scheduledtasks/)
- [Microsoft：New-ScheduledTaskSettingsSet](https://learn.microsoft.com/powershell/module/scheduledtasks/new-scheduledtasksettingsset)
