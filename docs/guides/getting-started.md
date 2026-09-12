# 上手指南 — 從下載到開始使用

照著做一遍就能跑起完整閉環。安裝順序是**後端 → extension → Profile → 第一趟抓取**，Linux 與 Windows 各節步驟一一對應。

契約與規格見 [deploy](../deploy.md)（路徑、排程、安裝子命令、release 工件）；本文只講操作。

本文走的是 release 工件的兩條路徑——bootstrap 腳本的下載安裝與自行解壓工件的手動安裝。以 dev 部署包架設測試環境不在本文範圍，見 [測試環境指南](test-environment.md)。

---

## 1. 先決定形態

| 形態 | 適用 | 本文適用性 |
|---|---|---|
| **本機部署（預設）** | 後端與 Chrome 同一台機器 | 就是本文 |
| **後端在別台機器** | 後端與 Chrome 不同機 | 本文的 §2–§6 照跑，另需自行把後端那台的 loopback port 轉送到本機；轉送作法本文不涵蓋 |

extension 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，這是權限層的硬約束：Options 只收本機位址，填遠端 URL 不會生效，也不得把服務改綁 `0.0.0.0`。

## 2. 前置檢查

| 項目 | Linux | Windows |
|---|---|---|
| 作業系統 | 有 systemd user session 的發行版 | Windows 10 / 11 |
| Shell | bash，`curl`、`tar`、`unzip`、`sha256sum` | PowerShell 5.1 以上（系統內建） |
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

**Agent CLI 在安裝階段用不到**，篩選與評分才需要。裝在後端之後補上也可以，補完須重啟 API 服務讓它重新讀取 PATH（見 §9）。

---

## 3. Linux：安裝後端

### 3.1 下載並驗 checksum

第一次建議手動做一遍，看清楚每一步在做什麼。

```bash
REPO=dccoding1118/job-finder
VER=$(gh release view -R "$REPO" --json tagName -q .tagName)
cd "$(mktemp -d)"

gh release download "$VER" -R "$REPO" -p "jobfinder_${VER}_linux_amd64.tar.gz" -p SHA256SUMS --clobber
sha256sum -c --ignore-missing SHA256SUMS
```

`--ignore-missing` 是因為 `SHA256SUMS` 列出全部平台的工件，而你只下載了一個。

沒有 GitHub CLI 時走匿名下載，前提是 release 可公開取得：

```bash
VER=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
curl -fsSLO "https://github.com/$REPO/releases/download/$VER/jobfinder_${VER}_linux_amd64.tar.gz"
curl -fsSLO "https://github.com/$REPO/releases/download/$VER/SHA256SUMS"
sha256sum -c --ignore-missing SHA256SUMS
```

熟悉之後可用 bootstrap 腳本代勞——它做的就是上面這幾行加解壓與交棒，同樣走匿名下載路徑：

```bash
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash
```

腳本有三種模式，不帶旗標即只裝後端。`--extension` 只取 extension（見 §5.1），`--all` 兩者都裝；經管線執行時旗標要走 `bash -s --`：

```bash
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash -s -- --all
```

後端模式看常駐 binary 是否已存在：沒有就交棒 `jobfinder install`，有就交棒 `jobfinder update`（保留回滾點，見 §10.1）。

### 3.2 解壓並安裝

```bash
tar -xzf "jobfinder_${VER}_linux_amd64.tar.gz"
cd "jobfinder_${VER}_linux_amd64"
./jobfinder version    # 必須印出 tag；印出 dev 代表版號注入失效，別裝
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

## 4. Windows：安裝後端

以**一般權限**的 PowerShell 執行，全程不需要系統管理員。

### 4.1 下載並驗 checksum

```powershell
$REPO = "dccoding1118/job-finder"
$VER  = (gh release view -R $REPO --json tagName -q .tagName)
$name = "jobfinder_${VER}_windows_amd64"
Set-Location (New-Item -ItemType Directory -Path (Join-Path $env:TEMP "jf-$VER") -Force)

gh release download $VER -R $REPO -p "$name.zip" -p SHA256SUMS --clobber

$expected = (Select-String -Path SHA256SUMS -Pattern ([regex]::Escape("$name.zip"))).Line.Split()[0]
$actual   = (Get-FileHash -Algorithm SHA256 "$name.zip").Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" } else { "checksum OK" }
```

沒有 GitHub CLI 時，登入 GitHub 後從 [Releases](https://github.com/dccoding1118/job-finder/releases/latest) 頁面下載 zip 與 `SHA256SUMS`，搬到同一個目錄，再跑上面的 checksum 三行。

bootstrap 版本（走匿名下載，前提是 release 可公開取得）：

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 | iex
```

腳本有三種模式，不帶旗標即只裝後端。要改模式就先存檔再執行，`iex` 收不了參數：

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -All   # -Extension 只裝 extension，-All 兩者都裝
```

後端模式看常駐 binary 是否已存在：沒有就交棒 `jobfinder install`，有就交棒 `jobfinder update`（保留回滾點，見 §10.1）。解壓出來的檔案由腳本解除 Mark of the Web。

### 4.2 解壓並安裝

```powershell
Expand-Archive "$name.zip" -DestinationPath . -Force
Set-Location $name
# 從網路下載的檔案帶有 Mark of the Web，未解除封鎖會被 SmartScreen 擋下
Get-ChildItem -Recurse | Unblock-File
.\jobfinder.exe version    # 必須印出 tag；印出 dev 代表版號注入失效，別裝
.\jobfinder.exe install
```

解壓出來應有 `jobfinder.exe`、`jobfinderw.exe`、`configs\`、`windows\`、`install.ps1`。**`jobfinderw.exe` 缺席時安裝直接失敗**，不會裝出一個排程指向不存在檔案的組合。

binary 未經程式碼簽署，SmartScreen 可能跳出「Windows 已保護您的電腦」。點「其他資訊」→「仍要執行」；或先以 `Get-FileHash` 比對 `SHA256SUMS` 確認來源無誤再執行。

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

## 5. 安裝 extension 並連上後端

extension 是使用系統的唯一介面，也是 Profile 編輯器與 104／Cake 就地擷取的載體，所以裝在後端之後、設定 Profile 之前。

Chrome 載入未封裝項目沒有 CLI 入口，**這是整套流程中唯一必須人工完成的部分**。

### 5.1 取得 extension 工件

extension 是**獨立工件** `jobfinder-extension_<tag>.zip`，平台 zip／tar 內不含它，安裝流程也不會取得它。

**extension 與後端必須同版**。兩者由同一個 tag 一起發出，但執行期沒有任何版本協商或相容性檢查：舊版 extension 連新版 API 一樣連得上、Side Panel 一樣打得開，只會在某個功能上安靜地行為不對。

解壓到一個**常駐目錄**，未封裝的 extension 目錄不能刪除或搬移——Chrome 每次啟動都要從那裡讀檔。

壓縮檔本身落在暫存目錄，與平台工件同一套路（§3.1、§4.1）：常駐目錄只放解壓出來的檔案，Chrome 讀的那一層不混入下載物。

bootstrap 腳本的 extension 模式做的就是下面這幾行——下載、驗 checksum、解壓到版本目錄（Windows 另解除 Mark of the Web），最後印出目錄位置與 Chrome 步驟：

```bash
# Linux
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash -s -- --extension
```

```powershell
# Windows
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Extension
```

跑 Chrome 的機器不必有後端：extension 模式不安裝任何服務，也不寫設定檔。手動路徑如下。

```bash
# Linux
TMP=$(mktemp -d)
DEST=~/.local/share/jobfinder/extension/$VER
mkdir -p "$DEST"
gh release download "$VER" -R "$REPO" -p "jobfinder-extension_${VER}.zip" -D "$TMP" --clobber
unzip -o "$TMP/jobfinder-extension_${VER}.zip" -d "$DEST"
grep '"version"' "$DEST/manifest.json"
```

```powershell
# Windows
$tmp  = (New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP "jf-$VER")).FullName
$dest = (New-Item -ItemType Directory -Force -Path (Join-Path $env:LOCALAPPDATA "jobfinder\extension\$VER")).FullName
gh release download $VER -R $REPO -p "jobfinder-extension_$VER.zip" -D $tmp --clobber
Expand-Archive (Join-Path $tmp "jobfinder-extension_$VER.zip") -DestinationPath $dest -Force
Get-ChildItem -Recurse $dest | Unblock-File
(Get-Content (Join-Path $dest "manifest.json") -Encoding UTF8 | ConvertFrom-Json).version
```

印出的 `version` 是 tag 去掉 `v` 的語意版號，要與 `jobfinder version` 對得上。

開發 checkout 可直接用 repo 的 `extension/` 目錄，該處 `manifest.json` 的 `version` 是佔位，打包時才由 tag 改寫。

### 5.2 載入 Chrome

1. Chrome 開 `chrome://extensions`
2. 右上「開發人員模式」打開
3. 「載入未封裝項目」→ 選 §5.1 的目錄（含 `manifest.json` 的那一層）

`manifest.json` 內含固定 `key`，所以 extension ID 是常數，跨機器、跨版本一致：

```
oddnhajjhmgogefocnljofeahniodiei
```

自行重簽或改過 key 才會不同，以 `chrome://extensions` 上顯示的為準。

### 5.3 設定 extension origin

**不改這一項，帶 Origin 的請求會被 API 拒絕**——這是「Side Panel 一片空白」最常見的原因。ID 是常數，所以這一項只需設定一次，換版不必重來。

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
```

Windows 改完依 §9 重啟 API 工作。

**設定檔是 UTF-8，改它一定要指定編碼。** Windows PowerShell 5.1 的 `Set-Content` 預設寫系統 ANSI code page（正體中文機器是 CP950），`>` 與 `Out-File` 預設寫 UTF-16；`config.yaml` 的註解含非 ASCII 字元，走這些預設會把檔案寫成不合法的 UTF-8，之後 `serve` 在解析設定時就失敗——而那個階段日誌還沒掛上，Windows 上看不到任何錯誤，只會看到排程工作啟動後立刻回到「就緒」。上面的 .NET 寫法明確指定無 BOM 的 UTF-8。`Set-Content -Encoding UTF8` 在 5.1 會**加上 BOM**，一樣別用。手改就用記事本，存檔時編碼選 UTF-8。

同理，**讀**設定檔與日誌一律用 `Get-Content -Encoding UTF8`：5.1 的 `Get-Content` 預設以系統 ANSI code page 解碼，正確的 UTF-8 檔案會顯示成亂碼。

### 5.4 取得 token 並填入 Options

```bash
# Linux
awk '/^api:/{f=1} f&&/^  token:/{print $2; exit}' ~/.config/jobfinder/config.yaml
```

```powershell
# Windows
(Select-String -Path $cfg -Pattern '^\s{2}token:').Line.Split(':')[1].Trim()
```

在 `chrome://extensions` 的 jobfinder 卡片點「詳細資料」→「擴充功能選項」，填入：

| 欄位 | 值 |
|---|---|
| API endpoint | `http://127.0.0.1:8686` |
| API token | 上面取得的那串 hex |

按儲存，顯示「已儲存」，token 欄位隨即清空。Options 只接受 loopback HTTP endpoint，填其他位址會被擋——這對應 manifest 的 `host_permissions`。

### 5.5 確認連上

點工具列的 jobfinder 圖示開啟 Side Panel。應看到清單框架與系統頁，而非連線錯誤；系統頁的 Profile 卡片狀態為 `ready`。

---

## 6. 設定 Profile

安裝時種下的是匿名範例 Profile，與你本人無關，**整份都要換掉**。

Profile 還沒定案就開始評分，只會累積之後必須作廢的結論。同一天內設定完就直接往下做；要跨到隔天，先把常駐 worker 停下來：

| 步驟 | Linux | Windows |
|---|---|---|
| 編輯設定，把 `worker.paused` 改為 `true` | `$EDITOR ~/.config/jobfinder/config.yaml` | 見 §5.3 的編碼規則 |
| 重啟 API | `systemctl --user restart jobfinder-api.service` | 見 §9 |

此模式下抓取、清單擷取與內頁擷取照常寫入，職缺一律停在待篩選，不呼叫任何 Agent。

### 6.1 先填 PII denylist

denylist 是你自己的禁字表，一行一個，以 `#` 開頭的行與空行忽略，比對不分大小寫且子字串命中即算。路徑見 `jobfinder paths`。

先填它再編 Profile：編輯器儲存時就會用這份表擋下命中的內容。

### 6.2 用全頁編輯器填寫

Side Panel 的系統頁 → Profile 卡片 → 「編輯履歷與求職條件」，Chrome 會開一個新分頁載入全頁編輯器。

編輯器透過 API 寫檔，避開手改 YAML 的編碼問題，且儲存時走的是與 `jobfinder profile lint` 同一套結構驗證與 PII 檢核。

各區塊決定什麼：

| 區塊 | 決定什麼 | 必填 |
|---|---|---|
| `search` 搜尋方向 | 系統用什麼關鍵字去各來源找職缺 | 至少一個方向，含 key、title、keywords |
| `requirements` 硬條件 | 職缺過不過得了門檻。不符直接篩掉，不花 token | `remote` |
| `intents` 軟性偏好 | 評分的加減分，不會把職缺篩掉 | 無 |
| `experiences` 經歷 | 年資與領域，供評分判斷勝任度 | 至少一筆，`industry` 必填、`years` 不可為負 |
| `qualifications` 學經歷證照語言 | 同上 | 至少一項 skill |

受控詞彙的合法值：

| 欄位 | 合法值 |
|---|---|
| `requirements.remote` | `required`、`preferred`、`acceptable`、`rejected` |
| `requirements.locations` | 22 個縣市代碼，加上 `taiwan`（JD 只寫台灣、未寫縣市）與 `overseas` |
| `qualifications.skills[].level` | `expert`、`proficient`、`familiar`，技能名稱不可重複 |
| `qualifications.education[].level` | `bachelor`、`master`、`phd` |

`taiwan` 是一個獨立地區，不是「全部縣市」的簡寫：涵蓋全國要列出除 `overseas` 以外的每一個代碼。`derived` 區塊在儲存時由系統算出，不要手填。

`honesty_bounds` 是你對自己能力的誠實界線，求職信生成受它約束。

### 6.3 從檔案端獨立驗一次

```bash
jobfinder profile lint
# profile lint passed
jobfinder profile show
jobfinder queries show
```

不需要帶 `--profile`／`--denylist`，預設值就是 `jobfinder paths` 印的位置。

`profile show` 的內容應為你本人的條件；`queries show` 印出每個搜尋方向展開後的實際查詢字串，那是抓取真正會用的東西，不合理現在改還來得及。

---

## 7. 跑第一趟

| 動作 | Linux | Windows |
|---|---|---|
| 手動觸發抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 看日誌 | `journalctl --user -u jobfinder-run.service -f` | `Get-Content …\data\logs\jobfinder.log -Encoding UTF8 -Wait -Tail 50` |

**一趟完整抓取要跑十分鐘以上，期間沒有任何進度訊號。** 每個職缺各打一次內頁，之間隨機等 1.5–3.5 秒；整批爬完才一次寫進資料庫。抓取路徑不產生日誌記錄，篩選與評分階段才有。

判斷它是在跑還是卡住，看三處：排程工作仍為執行中、行程存在、`runs` 已有一筆該趟紀錄（該筆在爬取開始前就寫入，代表設定、資料庫與 Profile 全部載入成功）。Windows 上真要看錯誤訊息，停掉工作後改用 `jobfinder run --config <config>` 在前景重跑。

抓完後 Side Panel 應出現職缺。worker 在數秒內開始消化篩選與評分；`worker.paused` 為 `true` 時則全部停在待篩選。

104 與 Cake 的職缺不從這裡來：用 Chrome 開該平台的搜尋頁，content script 會就地擷取你已載入的內容。半被動來源的搜尋條件由你在該平台自行設定，系統不生成搜尋 URL。

---

## 8. 日常操作對照

| 事情 | Linux | Windows |
|---|---|---|
| 重啟 API | `systemctl --user restart jobfinder-api.service` | 見 §9 |
| 手動抓取 | `systemctl --user start jobfinder-run.service` | `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'` |
| 看日誌 | `journalctl --user -u jobfinder-api.service -f` | `Get-Content …\logs\jobfinder.log -Encoding UTF8 -Wait` |
| 排程狀態 | `systemctl --user list-timers jobfinder-run.timer` | `Get-ScheduledTaskInfo -TaskPath '\jobfinder\' -TaskName 'run'` |
| 版本 | `jobfinder version` | 同左 |
| 路徑排障 | `jobfinder paths` | 同左 |

**暫停 token 消耗**用 Side Panel 系統頁的「自動篩選與評分」開關：存在資料庫、重啟仍生效，worker 保持常駐，你仍可對單筆按「馬上處理」，求職信也照常產生。`worker.paused` 是另一回事——那是部署期把三個階段整個交給 CLI 批次的模式，此模式下該開關與「馬上處理」皆無作用。

`llm.max_*_per_day` 不是暫停開關：值為 0 或負數代表**不設上限**，不是不執行。

---

## 9. 重啟 Windows 的 API 工作

PowerShell 的 ScheduledTasks 模組沒有 `Restart-ScheduledTask`，重啟是停止、等行程消失、再啟動三步：

```powershell
Stop-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'
while ((Get-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api').State -eq 'Running') { Start-Sleep -Milliseconds 500 }
Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'
```

中間的等待不可省略。`Start-ScheduledTask` 對仍在執行的工作是 no-op，與 systemd 的 `enable --now` 是同一個陷阱：磁碟上換了、記憶體裡跑的還是舊的。

服務行程在啟動時取得環境變數，**改過 PATH 或補裝 Agent CLI 之後必須走這一步**，否則服務手上的仍是舊 PATH。

---

## 10. 更新與回滾

### 10.1 後端

逐步指令（下載、驗 checksum、解壓、換版、驗收）見 [換版部署 runbook](runbook-upgrade.md)；本節說明這些動作的語意與規則。

**跨 schema 版本的更新之前先備份資料庫**（見下方回滾段落）。換版本身是一個子命令：

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

回滾**不動資料庫**。被回滾掉的版本保留在 rollback 目錄的 `.bad` 檔，所以前滾還有得救。

**回滾只退一個版本**：保留的前一版恰好一份，回滾不會把它往前推。連跑兩次 rollback 不會退到再前一版，第二次會被拒絕。要退超過一版，下載該版工件跑 `update`。

**跨 schema 版本回滾要連資料庫一起還原**：資料庫在升級時會自動升到新版 schema，而舊 binary 遇到比自己新的 schema 會拒絕啟動（`database schema version N is newer than supported version M`）。這種情況下 `rollback` 會換回舊 binary、重啟服務，然後服務起不來、驗證失敗——訊息會附上服務自己印的那一行原因。正確順序是先停服務、還原升級前的資料庫備份（連同 `-wal`、`-shm`），再跑 `rollback`。

沒有升級前備份就沒有退路：舊 schema 無從由新資料庫重建。因此**跨版本更新之前先備份資料庫**，`update` 本身不會代為備份。

### 10.2 extension

後端換版時 extension 一起換，兩者同版是使用前提（見 §5.1）。逐步指令見 [換版部署 runbook](runbook-upgrade.md) §4：解壓到新的版本目錄、移除舊卡片、載入新目錄、重填 Options。前一步（取得新版並解壓到新的版本目錄）可交給 bootstrap 腳本的 extension 模式；後三步在 Chrome 內完成，沒有命令列入口。

移除 extension 會清掉它的 `chrome.storage.local`，endpoint 與 token 必須重填。ID 由固定 `key` 決定，換版不變，所以 `api.extension_origin` 不必動。舊的版本目錄確認新版正常後才刪。

### 10.3 清空既有職缺重新開始

停掉 API 與抓取排程後刪除 SQLite（連同 `-wal`、`-shm`），下次啟動即以最新 schema 重建空庫。Profile、denylist 與設定不受影響。

### 10.4 移除安裝

**安裝根目錄底下不只有安裝流程的產物。** 整棵刪掉會一併帶走 Chrome 正在讀的 extension 目錄，以及任何自行放在該路徑下的檔案。逐項處理，不要一次刪整棵樹。

| 子目錄 | 內容 | 移除後端時 |
|---|---|---|
| `bin/`、`lib/` | 執行檔、回滾工件與安裝 metadata | 刪 |
| `config/` | 設定、Profile、denylist | 先備份 Profile 再刪 |
| `data/` | SQLite、worker 鎖檔、備份、日誌 | 要保留投遞歷程就先備份 |
| `extension/<tag>/` | Chrome 載入未封裝項目時讀取的常駐目錄 | **保留**，除非同時要移除 extension |

**先備份**：Profile 是唯一無法從工件重建的東西，資料庫承載全部職缺狀態與求職信歷程。

```bash
# Linux
cp ~/.config/jobfinder/profile.yaml ~/profile-backup.yaml
cp ~/.local/share/jobfinder/jobs.db ~/jobs-backup.db
```

```powershell
# Windows
Copy-Item "$env:LOCALAPPDATA\jobfinder\config\profile.yaml" "$env:USERPROFILE\profile-backup.yaml"
Copy-Item "$env:LOCALAPPDATA\jobfinder\data\jobs.db" "$env:USERPROFILE\jobs-backup.db"
```

**Linux**：

```bash
systemctl --user disable --now jobfinder-api.service jobfinder-run.timer
systemctl --user stop jobfinder-run.service
rm -f ~/.config/systemd/user/jobfinder-api.service \
      ~/.config/systemd/user/jobfinder-run.service \
      ~/.config/systemd/user/jobfinder-run.timer
systemctl --user daemon-reload

rm -f ~/.local/bin/jobfinder
rm -rf ~/.local/lib/jobfinder
rm -rf ~/.config/jobfinder
rm -rf ~/.local/share/jobfinder/{jobs.db,jobs.db-wal,jobs.db-shm,jobs.db.worker.lock,backups,logs}
```

最後一行刻意逐項列出，`~/.local/share/jobfinder/extension/` 因此留著。`jobs.db.worker.lock` 是常駐 worker 與手動批次的互斥鎖，落點是資料庫路徑加上該後綴，與 SQLite 同層，一併刪除。連 extension 一起移除才加 `rm -rf ~/.local/share/jobfinder`。

**Windows**：

```powershell
Stop-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api' -ErrorAction SilentlyContinue
while ((Get-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api' -ErrorAction SilentlyContinue).State -eq 'Running') {
  Start-Sleep -Milliseconds 500
}
Unregister-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api' -Confirm:$false -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run' -Confirm:$false -ErrorAction SilentlyContinue

$root = Join-Path $env:LOCALAPPDATA 'jobfinder'
Remove-Item (Join-Path $root 'bin')    -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $root 'lib')    -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $root 'config') -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $root 'data')   -Recurse -Force -ErrorAction SilentlyContinue

$bin = Join-Path $root 'bin'
$path = [Environment]::GetEnvironmentVariable('PATH', 'User')
[Environment]::SetEnvironmentVariable(
  'PATH', (($path -split ';' | Where-Object { $_ -ne $bin }) -join ';'), 'User')
```

`$root` 本身不刪，`extension\` 留在原地。PATH 改動要開新終端才生效。

**確認移除乾淨**：

```bash
# Linux
systemctl --user list-units 'jobfinder*' --all
ls ~/.local/bin/jobfinder ~/.config/jobfinder 2>&1
ls ~/.local/share/jobfinder
```

```powershell
# Windows
Get-ScheduledTask -TaskPath '\jobfinder\' -ErrorAction SilentlyContinue
Get-Process jobfinder, jobfinderw -ErrorAction SilentlyContinue
Get-ChildItem (Join-Path $env:LOCALAPPDATA 'jobfinder')
```

前兩項應無輸出，最後一項應只剩 `extension`；該機器沒有載入未封裝項目時，最後一項為空。

**移除 extension**：在 `chrome://extensions` 移除卡片，再刪版本目錄。移除卡片會清掉 `chrome.storage.local`，重裝後 endpoint 與 token 要重填。

---

---

## 11. 常見卡點

| 症狀 | 原因與處置 |
|---|---|
| Side Panel 空白或連不上 | `api.extension_origin` 還是佔位，或改了沒重啟 API |
| Options 存不進去 | endpoint 必須是 loopback HTTP；`https://` 或網域會被擋 |
| extension 行為與文件不符 | 版本與後端不一致。比對 `chrome://extensions` 的版本與 `jobfinder version`，執行期不會有任何警告 |
| `Restart-ScheduledTask` 找不到指令 | 該 cmdlet 不存在，用 §9 的三步 |
| Linux 隔天沒抓 | linger 沒開，登出後 timer 停了 |
| Windows 排程跑了但沒動靜 | 去看 `log.file`。Task Scheduler 丟棄工作的 stdout／stderr，這是 Windows 唯一的日誌出口 |
| 抓取工作長時間停在執行中 | 十分鐘以上屬正常，判斷方式見 §7 |
| 評分一直失敗 | Agent CLI 不在服務的 PATH 上。Linux 檢查 unit 的 `Environment=PATH=` 是否含 mise shims；Windows 先依 §9 重啟再試，仍失敗則用 `llm.roles.<role>.<primary\|fallback>.command` 填完整執行檔路徑 |
| Windows 上 CLI 回「不是有效的應用程式」 | npm 裝的 `claude` 是 `.cmd` shim。程式已自動改經 `%COMSPEC% /c`；仍失敗就用上一列的 `command` 指定完整路徑 |
| `jobfinder: command not found` | bin 目錄不在 PATH。Linux 加進 shell profile；Windows 開新終端 |
| Windows 排程「啟動」後立刻回到「就緒」 | 服務起來就退了。依序查：`(Get-ScheduledTaskInfo -TaskPath '\jobfinder\' -TaskName 'api').LastTaskResult`；`log.file` 有沒有這次的紀錄；然後用 `jobfinder.exe serve --config <config>` 在前景跑，錯誤會直接印出來。常見原因是 API port 被別的程式占用（VS Code Port Forward 是慣犯），或設定檔被非 UTF-8 的寫入弄壞（見 §5.3） |
| 設定檔或日誌顯示為亂碼 | `Get-Content` 少了 `-Encoding UTF8`（見 §5.3） |
| API port 被占用 | `Get-NetTCPConnection -LocalAddress 127.0.0.1 -LocalPort 8686 -State Listen \| ForEach-Object { Get-Process -Id $_.OwningProcess }`，找出占用者再處置；不要改用別的埠繞過 |
| Windows 上執行檔被擋下 | 下載的檔案帶 Mark of the Web。`Get-ChildItem -Recurse \| Unblock-File`；binary 未簽署，SmartScreen 另需點「其他資訊」→「仍要執行」 |
| 不確定讀了哪份設定 | `jobfinder paths` |

---

## 12. 發版（維護者）

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
