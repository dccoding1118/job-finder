# 上手指南 — 從下載到開始使用

照著做一遍就能跑起完整閉環。Linux 與 Windows 各一節，步驟一一對應。

契約與規格見 [deploy](../deploy.md)（路徑、排程、安裝子命令、release 工件）；本文只講操作。

---

## 1. 先決定形態

| 形態 | 適用 | 本文適用性 |
|---|---|---|
| **本機部署（預設）** | 後端與 Chrome 同一台機器 | 就是本文 |
| **遠端後端（選配）** | 後端在另一台機器（例如雲端 VM） | 先讀本文的 §2–§6 裝好後端，再依 [遠端後端通道](runbook-extension.md) 設定通道 |

extension 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，這是權限層的硬約束：遠端後端一定得靠通道偽裝成本機 loopback，不能靠「設定檔填一個遠端 URL」解決，也不得把服務改綁 `0.0.0.0`。

## 2. 前置檢查

| 項目 | Linux | Windows |
|---|---|---|
| 作業系統 | 有 systemd user session 的發行版 | Windows 10 / 11 |
| Shell | bash，`curl`、`tar`、`sha256sum` | PowerShell 5.1 以上（系統內建） |
| Agent CLI | `claude` 或 `codex` 可執行 | 同左 |
| 瀏覽器 | Chrome 114+ | 同左 |
| 服務常駐 | 需啟用 linger | 登入時自動啟動，無對應設定 |

```bash
# Linux
systemctl --user show-environment >/dev/null && echo "user bus ok"
sudo loginctl enable-linger "$USER"
loginctl show-user "$USER" -p Linger --value   # 要是 yes
which claude codex
```

```powershell
# Windows
Get-Command claude, codex
```

**linger 是 Linux 最容易漏掉的一項**：沒開的話，你登出後每日抓取就靜默停止。安裝流程只會提醒，不會代為啟用（那需要 root）。

Windows 反過來：排程工作在**登入時**觸發，沒登入的日子就不會抓——這是形態差異，不是缺陷。

---

## 3. Linux：安裝

### 3.1 下載並驗 checksum

第一次建議手動做一遍，看清楚每一步在做什麼。

```bash
VER=v0.1.0
REPO=dccoding1118/job-finder
cd "$(mktemp -d)"

curl -fsSLO "https://github.com/$REPO/releases/download/$VER/jobfinder_${VER}_linux_amd64.tar.gz"
curl -fsSLO "https://github.com/$REPO/releases/download/$VER/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
```

`--ignore-missing` 是因為 `SHA256SUMS` 列出全部平台的工件，而你只下載了一個。

熟悉之後可用 bootstrap 腳本代勞——它做的就是上面這幾行加解壓與交棒：

```bash
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash
```

### 3.2 解壓並安裝

```bash
tar -xzf "jobfinder_${VER}_linux_amd64.tar.gz"
cd "jobfinder_${VER}_linux_amd64"
./jobfinder install
```

解壓出的執行檔是**安裝媒介**：它把自己複製到 `~/.local/bin/jobfinder`，systemd 執行的是那份副本，安裝完這個目錄可以刪掉。拿常駐副本對自己跑 `install` 會被明確拒絕。

### 3.3 讀懂輸出

輸出的最後幾行不是裝飾，是驗收條件：

| 輸出 | 意義 |
|---|---|
| `API service effective: pid … running …` | 讀 `/proc/<pid>/exe` 確認**執行中的 process** 就是剛裝的 binary。只有「服務 active」不算——舊 process 抓著已被替換的 inode 一樣是 active |
| `API smoke passed … (authenticated 200, unauthenticated 401)` | API 真的能用，且沒有 token 進不去 |
| `fetch is armed: … (next …)` | 排程已排定下一次觸發。**安裝流程一律不觸發抓取**，這是刻意的 |
| `linger is not enabled; run: …` | 出現這行就照做，否則登出後排程停擺 |
| `… is not on PATH` | 把 `~/.local/bin` 加進 shell profile，重開終端 |

### 3.4 確認落點

```bash
jobfinder paths
```

印出這台機器實際使用的全部位置。這是排查「到底讀了哪份設定」的第一站。順手驗一下：

```bash
grep -c CHANGE_ME  ~/.config/jobfinder/config.yaml    # 0
grep -c local-dev  ~/.config/jobfinder/config.yaml    # 0
stat -c '%a'       ~/.config/jobfinder/config.yaml    # 600
```

---

## 4. Windows：安裝

### 4.1 下載並驗 checksum

```powershell
$VER  = "v0.1.0"
$REPO = "dccoding1118/job-finder"
$name = "jobfinder_${VER}_windows_amd64"
Set-Location (New-Item -ItemType Directory -Path (Join-Path $env:TEMP "jf-$VER"))

Invoke-WebRequest "https://github.com/$REPO/releases/download/$VER/$name.zip" -OutFile "$name.zip"
Invoke-WebRequest "https://github.com/$REPO/releases/download/$VER/SHA256SUMS"  -OutFile "SHA256SUMS"

$expected = (Select-String -Path SHA256SUMS -Pattern ([regex]::Escape("$name.zip"))).Line.Split()[0]
$actual   = (Get-FileHash -Algorithm SHA256 "$name.zip").Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" } else { "checksum OK" }
```

bootstrap 版本：

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 | iex
```

### 4.2 解壓並安裝

```powershell
Expand-Archive "$name.zip" -DestinationPath . -Force
Set-Location $name
# 從網路下載的檔案帶有 Mark of the Web，未解除封鎖會被 SmartScreen 擋下
Get-ChildItem -Recurse | Unblock-File
.\jobfinder.exe install
```

binary 未經程式碼簽署，SmartScreen 仍可能跳出「Windows 已保護您的電腦」。點「其他資訊」→「仍要執行」；或先以 `Get-FileHash` 比對 `SHA256SUMS` 確認來源無誤再執行。

### 4.3 讀懂輸出

除了與 Linux 對應的三項驗證（`API task effective`、`API smoke passed`、`fetch is armed`），Windows 多一行：

```
added C:\Users\you\AppData\Local\jobfinder\bin to the user PATH; open a new terminal for it to take effect
```

**照做**：安裝把 bin 目錄寫進使用者 PATH，但已經開著的終端不會知道。開一個新的 PowerShell 視窗再繼續。

`API task effective` 是讀 `Win32_Process` 的 `ExecutablePath` 與 `CreationDate` 得到的，等價於 Linux 讀 `/proc/<pid>/exe`。

**Windows 裝的是兩支執行檔**：你輸入的 `jobfinder.exe`，以及排程實際執行的 `jobfinderw.exe`。後者是同一份程式的 GUI subsystem 建置，所以常駐服務不會在桌面上留一個主控台視窗、每日抓取也不會閃一次。兩支永遠同版，`update` 與 `rollback` 一起換。

代價是 `jobfinderw.exe` 沒有 stderr：**要在前景看服務起不來的原因，用 `jobfinder.exe serve --config <config>`**，不要用 `jobfinderw.exe`。

### 4.4 確認落點

```powershell
jobfinder paths
Get-ScheduledTask -TaskPath '\jobfinder\' | Format-Table TaskName, State
Get-ScheduledTaskInfo -TaskPath '\jobfinder\' -TaskName 'run' | Select-Object NextRunTime
```

`api` 應為 `Running`、`run` 應為 `Ready` 且有 `NextRunTime`。圖形介面在 `taskschd.msc` 的「工作排程器程式庫 → jobfinder」。

`jobfinder paths` 不會列出 systemd unit 目錄——Windows 的排程不在檔案樹裡。

**權限**：Windows 沒有 `chmod` 等價物。帶 token 的設定檔靠 `%LocalAppData%` 繼承的 ACL 保護（非系統管理員的其他使用者讀不到），安裝流程不會、也不能套 `0600`。這是明載的退讓。

---

## 5. 設定 Profile

Profile 還沒定案就開始評分，只會累積之後必須作廢的結論。先把常駐 worker 停下來：

| 步驟 | Linux | Windows |
|---|---|---|
| 編輯設定，把 `worker.paused` 改為 `true` | `$EDITOR ~/.config/jobfinder/config.yaml` | `notepad (Join-Path $env:LOCALAPPDATA 'jobfinder\config\config.yaml')` |
| 重啟 API | `systemctl --user restart jobfinder-api.service` | `Restart-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'` |

此模式下抓取、清單擷取與內頁擷取照常寫入，職缺一律停在待篩選，不呼叫任何 Agent。

接著編輯 Profile 與 denylist（路徑見 `jobfinder paths`），然後：

```bash
jobfinder profile lint
# profile lint passed
```

不需要帶 `--profile`／`--denylist`，預設值就是 `jobfinder paths` 印的位置。

也可以先跳過這步，等 extension 裝好後改用它的全頁編輯器——那個介面比手改 YAML 好用，且儲存時同樣過 PII 檢核。

---

## 6. 安裝 extension 並串起來

這是唯一必須人工完成的部分：Chrome 載入 unpacked extension 沒有 CLI 入口。

### 6.1 載入

1. Chrome 開 `chrome://extensions`
2. 右上「開發人員模式」打開
3. 「載入未封裝項目」→ 選 repo 的 `extension/` 目錄（或 release 的 `jobfinder-extension_<tag>.zip` 解壓後的目錄）

`manifest.json` 內含固定 `key`，所以 extension ID 是常數，跨機器一致：

```
oddnhajjhmgogefocnljofeahniodiei
```

自行重簽或改過 key 才會不同，以 `chrome://extensions` 上顯示的為準。

### 6.2 設定 extension origin

**不改這一項，帶 Origin 的請求會被 API 拒絕**——這是「Side Panel 一片空白」最常見的原因。

```bash
# Linux
sed -i 's|^  extension_origin: .*|  extension_origin: chrome-extension://oddnhajjhmgogefocnljofeahniodiei|' \
  ~/.config/jobfinder/config.yaml
systemctl --user restart jobfinder-api.service
```

```powershell
# Windows
$cfg  = Join-Path $env:LOCALAPPDATA 'jobfinder\config\config.yaml'
$text = [System.IO.File]::ReadAllText($cfg, [System.Text.Encoding]::UTF8) -replace `
  '(?m)^  extension_origin: .*', '  extension_origin: chrome-extension://oddnhajjhmgogefocnljofeahniodiei'
[System.IO.File]::WriteAllText($cfg, $text, (New-Object System.Text.UTF8Encoding($false)))
Restart-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'
```

**設定檔是 UTF-8，改它一定要指定編碼。** Windows PowerShell 5.1 的 `Set-Content` 預設寫系統 ANSI code page（正體中文機器是 CP950），`>` 與 `Out-File` 預設寫 UTF-16；`config.yaml` 的註解含非 ASCII 字元，走這些預設會把檔案寫成不合法的 UTF-8，之後 `serve` 在解析設定時就失敗——而那個階段日誌還沒掛上，Windows 上看不到任何錯誤，只會看到排程工作啟動後立刻回到「就緒」。上面的 .NET 寫法明確指定無 BOM 的 UTF-8。`Set-Content -Encoding UTF8` 在 5.1 會**加上 BOM**，一樣別用。手改就用記事本（現行 Windows 的記事本預設存無 BOM 的 UTF-8）。

### 6.3 取得 token 並填入 Options

```bash
# Linux
awk '/^api:/{f=1} f&&/^  token:/{print $2; exit}' ~/.config/jobfinder/config.yaml
```

```powershell
# Windows
Select-String -Path $cfg -Pattern '^\s{2}token:' | ForEach-Object { $_.Line.Split(':')[1].Trim() }
```

在 `chrome://extensions` 的 jobfinder 卡片點「詳細資料」→「擴充功能選項」，填入：

| 欄位 | 值 |
|---|---|
| API endpoint | `http://127.0.0.1:8686` |
| API token | 上面取得的那串 hex |

按儲存，顯示「已儲存」。Options 只接受 loopback HTTP endpoint，填其他位址會被擋——這對應 manifest 的 `host_permissions`。

### 6.4 開啟 Side Panel

點工具列的 jobfinder 圖示。應看到清單框架與系統頁，而非連線錯誤。

---

## 7. 跑第一趟

| 動作 | Linux | Windows |
|---|---|---|
| 手動觸發抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 看日誌 | `journalctl --user -u jobfinder-run.service -f` | `Get-Content …\data\logs\jobfinder.log -Wait -Tail 50` |

抓完後 Side Panel 應出現職缺，全部停在待篩選（因為 §5 停了 worker）。確認 Profile 沒問題後把 `worker.paused` 改回 `false` 並重啟 API，worker 就開始消化篩選與評分。

104 與 Cake 的職缺不從這裡來：用 Chrome 開該平台的搜尋頁，content script 會就地擷取你已載入的內容。半被動來源的搜尋條件由你在該平台自行設定，系統不生成搜尋 URL。

---

## 8. 日常操作對照

| 事情 | Linux | Windows |
|---|---|---|
| 重啟 API | `systemctl --user restart jobfinder-api.service` | `Restart-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'` |
| 手動抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 看日誌 | `journalctl --user -u jobfinder-api.service -f` | `Get-Content …\logs\jobfinder.log -Wait` |
| 排程狀態 | `systemctl --user list-timers jobfinder-run.timer` | `Get-ScheduledTaskInfo -TaskPath '\jobfinder\' -TaskName 'run'` |
| 版本 | `jobfinder version` | 同左 |
| 路徑排障 | `jobfinder paths` | 同左 |

**暫停 token 消耗**用 Side Panel 系統頁的「自動篩選與評分」開關：存在資料庫、重啟仍生效，worker 保持常駐，你仍可對單筆按「馬上處理」。`worker.paused` 是另一回事——那是部署期把三個階段整個交給 CLI 批次的模式，此模式下該開關與「馬上處理」皆無作用。

`llm.max_*_per_day` 不是暫停開關：值為 0 或負數代表**不設上限**，不是不執行。

---

## 9. 更新與回滾

下載新版工件、驗 checksum、解壓（同 §3.1 或 §4.1），然後：

```bash
./jobfinder update       # Linux
.\jobfinder.exe update   # Windows
```

`update` 會保留現行 binary 供回滾，替換 binary 與排程定義，重啟 API，並在**執行中的 process** 上驗證新版且啟動時間晚於替換點。

> 為什麼要「重啟」而不是「啟用」：systemd 的 `enable --now` 與 Windows 的 `Start-ScheduledTask` 對已在執行的服務都是 no-op。新 binary 躺在磁碟上、記憶體裡跑的還是舊的——這是更新最典型的假成功。

出問題就回滾：

```bash
jobfinder rollback
jobfinder version    # 應為前一版
```

回滾**不動資料庫**。被回滾掉的版本保留在 rollback 目錄的 `.bad` 檔，所以前滾還有得救。資料庫確認毀損才從備份目錄手動還原。

清空既有職缺重新開始：停掉 API 與抓取排程後刪除 SQLite（連同 `-wal`、`-shm`），下次啟動即以最新 schema 重建空庫。Profile、denylist 與設定不受影響。

---

## 10. 常見卡點

| 症狀 | 原因與處置 |
|---|---|
| Side Panel 空白或連不上 | `api.extension_origin` 還是佔位，或改了沒重啟 API |
| Options 存不進去 | endpoint 必須是 loopback HTTP；`https://` 或網域會被擋 |
| Linux 隔天沒抓 | linger 沒開，登出後 timer 停了 |
| Windows 排程跑了但沒動靜 | 去看 `log.file`。Task Scheduler 丟棄工作的 stdout／stderr，這是 Windows 唯一的日誌出口 |
| 評分一直失敗 | Agent CLI 不在服務的 PATH 上。Linux 檢查 unit 的 `Environment=PATH=` 是否含 mise shims；Windows 用 `llm.roles.<role>.<primary\|fallback>.command` 填完整執行檔路徑 |
| Windows 上 CLI 回「不是有效的應用程式」 | npm 裝的 `claude` 是 `.cmd` shim。程式已自動改經 `%COMSPEC% /c`；仍失敗就用上一列的 `command` 指定完整路徑 |
| `jobfinder: command not found` | bin 目錄不在 PATH。Linux 加進 shell profile；Windows 開新終端 |
| Windows 排程「啟動」後立刻回到「就緒」 | 服務起來就退了。依序查：`(Get-ScheduledTaskInfo -TaskPath '\jobfinder\' -TaskName 'api').LastTaskResult`；`log.file` 有沒有這次的紀錄；然後用 `jobfinder.exe serve --config <config>` 在前景跑，錯誤會直接印出來。常見原因是 API port 被別的程式占用（VS Code Port Forward 是慣犯），或設定檔被非 UTF-8 的寫入弄壞（見 §6.2） |
| API port 被占用 | `Get-NetTCPConnection -LocalAddress 127.0.0.1 -LocalPort 8686 -State Listen \| ForEach-Object { Get-Process -Id $_.OwningProcess }`，找出占用者再處置；不要改用別的埠繞過 |
| Windows 上執行檔被擋下 | 下載的檔案帶 Mark of the Web。`Get-ChildItem -Recurse \| Unblock-File`；binary 未簽署，SmartScreen 另需點「其他資訊」→「仍要執行」 |
| 不確定讀了哪份設定 | `jobfinder paths` |

---

## 11. 發版（維護者）

使用者不需要這一節。工件內容與 workflow 契約見 [deploy §7](../deploy.md#7-ci-與-release-工件)。

| 步驟 | 動作 |
|---|---|
| 1. 合併後確認 CI | `ci.yml` 的 `check`（ubuntu）與 `windows` 兩個 job 皆綠 |
| 2. 演練 | `gh workflow run release.yml` → `gh run download --name dry-run-artifacts`，檢查工件內容（Linux 應含 `configs/`、`systemd/`、`install.sh`；Windows 應含 `configs/`、`windows/`、`install.ps1`） |
| 3. 發版 | `git tag v<MAJOR>.<MINOR>.<PATCH>` → `git push origin <tag>` |
| 4. 驗版號 | 下載工件執行 `jobfinder version`，**必須印出 tag**。印出 `dev` 代表 ldflags 注入失效，這版不能發 |
| 5. 驗安裝 | 依 [verify §6.1](../verify.md) 的 D1–D9，Linux 與 Windows 各跑一輪 |

發錯了：`gh release delete <tag> --cleanup-tag`，修好後重推同一個 tag。已被下載過的 tag 不要重用，直接跳下一個 patch 版號。

extension zip 需人工上傳至 Chrome Web Store 送審，不納入自動發佈。
