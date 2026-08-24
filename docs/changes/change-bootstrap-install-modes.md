# change — bootstrap 腳本的三種安裝模式

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

`install.sh`／`install.ps1` 從頭到尾只處理該平台的後端工件，extension zip 沒有任何腳本會去拿。使用者要裝 extension，只能照 `getting-started.md` §5.1 手動下載、驗 checksum、解壓到版本目錄——每次換版重來一遍，Windows 上還多一步 `Unblock-File`。

遠端拓撲讓這條缺口變成實際障礙：Chrome 跑在另一台 Windows，那台不該被裝出一個後端服務，卻需要一份與後端同版的 extension。現行腳本給不出「只裝 extension」這條路。

同一批還有兩個既有缺口：

- `install.ps1` 解壓後未 `Unblock-File`。下載的 zip 帶 Mark of the Web，解出來的執行檔繼承它，SmartScreen 會擋。
- 兩支腳本一律呼叫 `jobfinder install`。已有安裝的機器要換版時，正確的子命令是 `update`（它保留回滾點），使用者得自己知道別跑腳本。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 兩支腳本各增一個模式選項（`--mode`／`-Mode`），值為 `backend`、`extension`、`both`，預設 `backend`。 |
| D2 | extension 的整條路留在腳本內：下載 `jobfinder-extension_<tag>.zip`、驗 `SHA256SUMS`、解壓到版本目錄、Windows 另跑 `Unblock-File`，然後印出 Chrome 的手動步驟與固定 extension ID。 |
| D3 | 後端模式依常駐 binary 是否存在自動選 `install` 或 `update`。 |
| D4 | `install.ps1` 對解壓出的後端工件跑 `Unblock-File`。 |

D1 的預設不動：`getting-started.md` §3.1 與 README 的單行安裝承諾的就是裝後端，改預設會讓已寫進文件的那一行行為變樣。

D2 不把 extension 併進 `jobfinder install`，也不併進平台工件：extension 沒有任何安裝語意——無設定渲染、無 token、無排程、無生效面驗證——併進去只會讓安裝子命令多背一條與它的契約無關的路徑。

D3 的判準是常駐 binary（Linux `~/.local/bin/jobfinder`、Windows `%LocalAppData%\jobfinder\bin\jobfinder.exe`）是否存在，也就是 `update` 自己用來拒絕的那個檔案。

腳本的終點是把目錄準備好並印出後續步驟。Chrome 的「載入未封裝項目」沒有 CLI 入口（`docs/verify.md` 明列為整套流程中唯一必須人工完成的部分），移除舊卡片、載入新目錄、重填 Options 永遠是手動。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| 腳本涵蓋範圍 | 只有後端工件 | 後端、extension，或兩者 |
| extension 取得 | 全手動（下載、驗 checksum、解壓、Windows 解封鎖） | `--mode extension`／`-Mode extension` 代勞至目錄就緒 |
| 已有安裝時重跑腳本 | 一律 `install` | 自動改走 `update`，保留回滾點 |
| Windows 解壓後的 MOTW | 未解除，執行檔可能被 SmartScreen 擋 | 解壓後遞迴 `Unblock-File` |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/deploy.md` | §4、§7 | bootstrap 腳本的職責改述為三種模式，含後端子命令的自動選擇與 extension 的解壓落點 |
| `docs/guides/getting-started.md` | §3.1、§4.1 | 單行安裝標註其為後端模式，並列出另外兩種模式的呼叫方式 |
| `docs/guides/getting-started.md` | §5.1、§10.2 | extension 取得與換版改列腳本路徑，手動步驟保留為對照 |
| `README.md` | Installation、Updating | 三種模式的旗標；換版時腳本自動走 `update` |
| `AGENTS.md` | §6 | bootstrap 腳本職責的一行描述 |
| `docs/guides/runbook-upgrade.md` | §4.1 | Windows 換版取得 extension 的腳本路徑 |
| `docs/verify.md` | §6.1 | D1 標明預設模式；D5 納入重跑腳本；新增 D10：extension 模式的目錄與版本一致性 |

## 5. 待實作進度

- [x] `install.sh` 三模式
- [x] `install.ps1` 三模式
- [x] 後端子命令自動選擇
- [x] `install.ps1` 解壓後解除 MOTW
- [x] canonical 文件更新
- [ ] 兩平台各跑一輪 D1／D10 人工 gate（匿名下載路徑須待 repo 轉 public）

## 6. 已知殘留限制

- 腳本走匿名 `curl`／`Invoke-WebRequest` 打 `api.github.com` 與 `releases/download`，private repo 一律 404，因此三種模式的實測只能排在轉 public 之後。不補 `gh` 下載路徑：公開後匿名路徑就通了，補了只為了先測一次，之後即是死碼。
- extension 模式只準備目錄。載入 Chrome、移除舊卡片、重填 Options 仍是人工。
- extension 模式印出的 ID 是 manifest 固定 `key` 推出的常數；自行重簽或改過 key 的建置以 `chrome://extensions` 顯示者為準。
