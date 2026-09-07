# Runbook — 發版後的換版部署

每次 release 之後照這份跑。三條 lane 各自獨立、可分開執行，但**必須全部換到同一個 tag**：

| Lane | 對象 | 節 |
|---|---|---|
| Linux 後端 | 跑 `jobfinder-api.service` 的機器 | §2 |
| Windows 後端 | 跑 `\jobfinder\api` 排程工作的機器 | §3 |
| Windows Chrome extension | 載入未封裝 extension 的那台 Chrome | §4 |

換版的語意（`update` 保留什麼、`rollback` 退到哪、跨 schema 為什麼要還原資料庫）見 [上手指南](getting-started.md) §10；本手冊只給逐步指令與驗收判準。首次安裝走 [上手指南](getting-started.md) §3–§6，不是這裡。

## 1. 開始之前

- **後端與 extension 必須同版**。兩者由同一個 tag 一起發出，執行期沒有任何版本協商或相容性檢查：舊版 extension 連新版 API 一樣連得上、Side Panel 一樣打得開，只會在某個功能上安靜地行為不對。
- **先備份資料庫**。跨 schema 版本的更新沒有備份就沒有退路——舊 binary 遇到比自己新的 schema 會拒絕啟動，而 `rollback` 不動資料庫。是否跨 schema 由該版的 migration 決定，不確定就備份，成本只是一次複製。

  ```bash
  # Linux
  systemctl --user stop jobfinder-api.service
  cd ~/.local/share/jobfinder && cp jobs.db jobs.db-wal jobs.db-shm backups/
  ```

  ```powershell
  # Windows
  Stop-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'
  $d = Join-Path $env:LOCALAPPDATA 'jobfinder\data'
  'jobs.db','jobs.db-wal','jobs.db-shm' | ForEach-Object { Copy-Item (Join-Path $d $_) (Join-Path $d 'backups') -Force }
  ```

  停掉服務再複製；`update` 會把服務重新啟動起來，不需要自己起。服務乾淨停止後 `-wal` 與 `-shm` 可能已被收進主檔，這兩個檔案不存在屬正常，複製時報缺檔即可忽略。`jobs.db.worker.lock` 不必備份。
- 本手冊一律用 `gh` CLI 下載，取其自動帶認證與 `--clobber` 覆寫。匿名路徑（`curl` 打 `releases/download`、`raw.githubusercontent.com` 單行安裝）同樣可用，見 `docs/guides/getting-started.md`。

## 2. Linux 後端

```bash
REPO=dccoding1118/job-finder
VER=<tag>                     # 例：v0.3.3
cd "$(mktemp -d)"

gh release download "$VER" -R "$REPO" -p "jobfinder_${VER}_linux_amd64.tar.gz" -p SHA256SUMS --clobber
sha256sum -c --ignore-missing SHA256SUMS

tar -xzf "jobfinder_${VER}_linux_amd64.tar.gz"
cd "jobfinder_${VER}_linux_amd64"
./jobfinder version    # 必須印出 $VER；印出 dev 代表版號注入失效，別裝
./jobfinder update
```

`--ignore-missing` 是因為 `SHA256SUMS` 列出全部平台的工件，而你只下載了一個。

驗收看 `update` 的輸出：

| 輸出 | 意義 |
|---|---|
| `API service effective: pid … running …` | 讀 `/proc/<pid>/exe` 確認**執行中的 process** 就是剛換的 binary。只有「服務 active」不算 |
| `API smoke passed … (authenticated 200, unauthenticated 401)` | API 真的能用，且沒有 token 進不去 |

收尾：

```bash
jobfinder version                                  # $VER
systemctl --user is-active jobfinder-api.service   # active
```

## 3. Windows 後端

以**一般權限**的 PowerShell 執行，全程不需要系統管理員。

```powershell
$REPO = "dccoding1118/job-finder"
$VER  = "<tag>"
$name = "jobfinder_${VER}_windows_amd64"
Set-Location (New-Item -ItemType Directory -Path (Join-Path $env:TEMP "jf-$VER") -Force)

gh release download $VER -R $REPO -p "$name.zip" -p SHA256SUMS --clobber

$expected = (Select-String -Path SHA256SUMS -Pattern ([regex]::Escape("$name.zip"))).Line.Split()[0]
$actual   = (Get-FileHash -Algorithm SHA256 "$name.zip").Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" } else { "checksum OK" }

Expand-Archive "$name.zip" -DestinationPath . -Force
Set-Location $name
Get-ChildItem -Recurse | Unblock-File
.\jobfinder.exe version    # 必須印出 $VER
.\jobfinder.exe update
```

`Unblock-File` 不能省：從網路下載的檔案帶 Mark of the Web，未解除封鎖會被 SmartScreen 擋下。binary 未經程式碼簽署，仍跳出「Windows 已保護您的電腦」時點「其他資訊」→「仍要執行」。

驗收：

```powershell
jobfinder version                                              # $VER
Get-ScheduledTask -TaskPath '\jobfinder\' | Format-Table TaskName, State
```

`api` 應為 `Running`、`run` 應為 `Ready`。`update` 一併換掉 `jobfinder.exe` 與排程實際執行的 `jobfinderw.exe`，兩支永遠同版。

服務起不來要看原因時用 `jobfinder.exe serve --config <config>` 在前景跑——`jobfinderw.exe` 是 GUI subsystem 建置，沒有 stderr。

## 4. Windows Chrome extension

**4.1 下載並解壓到新的版本目錄**

```powershell
$VER = "<tag>"; $REPO = "dccoding1118/job-finder"
$tmp  = (New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP "jf-$VER")).FullName
$dest = (New-Item -ItemType Directory -Force -Path (Join-Path $env:LOCALAPPDATA "jobfinder\extension\$VER")).FullName
gh release download $VER -R $REPO -p "jobfinder-extension_$VER.zip" -D $tmp --clobber
Expand-Archive (Join-Path $tmp "jobfinder-extension_$VER.zip") -DestinationPath $dest -Force
Get-ChildItem -Recurse $dest | Unblock-File
(Get-Content (Join-Path $dest "manifest.json") -Encoding UTF8 | ConvertFrom-Json).version
```

印出的 `version` 是 tag 去掉 `v` 的語意版號，要與 `jobfinder version` 對得上。壓縮檔留在暫存目錄；版本目錄是**常駐**的，Chrome 每次啟動都要從那裡讀檔，不能刪也不能搬。

release 可公開取得時，這一步可交給 bootstrap 腳本的 extension 模式（走匿名下載，不需 `gh`）：

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Extension -Version $VER
```

它做的是同一件事：下載、驗 `SHA256SUMS`、解壓到同一個版本目錄、解除 Mark of the Web，最後印出目錄位置。4.2 起的步驟不變。

**4.2 換掉 Chrome 裡的卡片**

1. Chrome 開 `chrome://extensions`，右上「開發人員模式」保持開啟
2. 移除舊的 jobfinder 卡片
3. 「載入未封裝項目」→ 選 4.1 的版本目錄（含 `manifest.json` 的那一層）
4. 確認卡片版本是新版、ID 仍是 `oddnhajjhmgogefocnljofeahniodiei`

ID 由 `manifest.json` 內固定的 `key` 決定，換版不變，所以 `api.extension_origin` 不必動、API 不必重啟。

**4.3 重填 Options**

移除卡片會清掉 `chrome.storage.local`，endpoint 與 token 必須重填。先取 token：

```powershell
$cfg = Join-Path $env:LOCALAPPDATA 'jobfinder\config\config.yaml'
(Select-String -Path $cfg -Pattern '^\s{2}token:').Line.Split(':')[1].Trim()
```

後端在別台機器時，token 取自**那台**的設定檔，endpoint 填轉送後的本機 loopback。同機部署則填 `http://127.0.0.1:8686`。

在 jobfinder 卡片點「詳細資料」→「擴充功能選項」填入兩欄，按儲存；顯示「已儲存」且 token 欄位清空即成功。Options 只收 loopback HTTP endpoint。

**4.4 驗收**

點工具列的 jobfinder 圖示開 Side Panel：看到清單框架與系統頁，而非連線錯誤；系統頁的 Profile 卡片狀態為 `ready`。

**4.5 收尾**

新版確認正常之後才刪舊的版本目錄。它是 extension 的回滾路徑——後端 `rollback` 退回前一版時，extension 也要一起退回舊目錄才維持同版。

## 5. 出問題怎麼退

```bash
jobfinder rollback     # Linux 與 Windows 同一個子命令
jobfinder version      # 應為前一版
```

回滾**只退一個版本**，連跑兩次第二次會被拒絕；要退超過一版，下載該版工件跑 `update`。跨 schema 版本的回滾要先停服務、還原升級前的資料庫備份（連同 `-wal`、`-shm`），再跑 `rollback`。extension 同時退回前一個版本目錄。

## 6. 一次看完的驗收清單

| Lane | 判準 |
|---|---|
| Linux 後端 | `jobfinder version` 為新 tag；`update` 印出 `API service effective` 與 `API smoke passed`；`systemctl --user is-active jobfinder-api.service` 為 active |
| Windows 後端 | `jobfinder version` 為新 tag；排程 `api` 為 `Running`、`run` 為 `Ready`；全程未出現主控台視窗 |
| extension | 卡片版本＝tag 去掉 `v`；ID 未變；Side Panel 連得上且 Profile 為 `ready` |
