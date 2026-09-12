# 測試環境指南

本文是**官方建議**的測試環境安排。拿到原始碼的人可以自行設計另一套，本文提供的是一條已經走通、與正式環境只差素材一項的作法。契約層面的定義見 [deploy](../deploy.md)：本文說「建議怎麼安排」，那份說「產品能做什麼」。

## 1. 為什麼需要一台測試環境

開發階段驗收（`mise run e2e-mock`、`mise run e2e-deploy`）跑在 `.local-dev/dev-verify/` 隔離根內，用的是合成來源、fake Agent 與 transient unit。它證明程式邏輯走對，證明不了三件事：

| 驗不到的東西 | 為什麼 |
|---|---|
| 真來源與真 Agent | mock 用合成頁與 fake CLI，格式與失敗模式都是自己造的 |
| 真 Chrome 的 extension | 隔離 Chromium 是模擬，不等於實際載入未封裝 extension 的 Chrome |
| 真排程與真服務管理器 | transient unit 不進正式 unit 目錄，Task Scheduler 的工作資料夾也不隨環境變數移動 |

測試環境就是一台機器的完整使用者環境，安裝流程、排程、資料庫與 extension 都是真的，只有素材是尚未發版的建置。

## 2. 挑一台機器

| 要驗的東西 | 需要的機器 |
|---|---|
| 後端邏輯、真來源、真 Agent、systemd 排程 | 一台 Linux |
| Task Scheduler、`jobfinderw.exe`、Agent CLI 的 `.cmd` shim、PATH 註冊 | 一台 Windows |
| Chrome extension 實機 | 任一台裝有 Chrome 的機器 |

兩個平台的排程機制與日誌出口不同，改動 `internal/install`、`internal/paths`、排程模板或 bootstrap 腳本時，受影響的平台各驗一輪。只改後端邏輯時驗一台即可。

開發環境與 Linux 測試環境可以是同一台機器：開發階段驗收落在 `.local-dev/dev-verify/`，測試環境落在使用者環境的標準位置（見 [deploy](../deploy.md) §2），兩者不重疊。

## 3. 打包

在開發環境的 checkout 內：

```bash
mise run pack
```

產出落在 `.local-dev/test-deploy/`：

| 檔案 | 給誰 |
|---|---|
| `jobfinder_dev_linux_amd64.tar.gz` | Linux 測試環境 |
| `jobfinder_dev_windows_amd64.zip` | Windows 測試環境 |
| `jobfinder-extension_dev.zip` | 有 Chrome 的那台 |
| `SHA256SUMS` | 上列工件的 checksum |
| `install.sh`、`install.ps1` | bootstrap 腳本，與工件並列以便整個目錄搬走即可部署 |

**只保留一版**。回滾點由 `jobfinder update` 保留的 `jobfinder.prev` 提供，切換受測版本的手段是換 branch 重新打包。

## 4. 部署到 Linux 測試環境

```bash
.local-dev/test-deploy/install.sh --from-dir .local-dev/test-deploy
```

常駐 binary 已存在時腳本自行改走 `update`，不必分辨首裝與換版。裝完確認：

```bash
jobfinder version          # 應印 dev (<commit>)
jobfinder paths            # 確認落點都在使用者環境的標準位置
```

**每日抓取的排程預設會被啟用。** 測試環境與正式環境若共用同一組 Agent 訂閱額度，測試這台要停用它，改為需要時手動觸發：

```bash
systemctl --user disable --now jobfinder-run.timer
systemctl --user start jobfinder-run.service    # 需要抓取時手動跑
```

每次重新部署都會再次武裝排程（生效面驗證的一部分），停用因此要重做一次。

## 5. 部署到 Windows 測試環境

把 `.local-dev/test-deploy/` 整個目錄傳到 Windows，在該目錄內：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -FromDirectory .
```

裝完確認 `jobfinder version` 與 `jobfinder paths`，並確認 `bin\` 下同時有 `jobfinder.exe` 與 `jobfinderw.exe`。停用每日抓取：

```powershell
Disable-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'
jobfinder run                                    # 需要抓取時手動跑
```

工作停用後連手動啟動一併關閉，所以 Windows 這端的手動抓取走 CLI。Linux 那端停用的是 timer，one-shot unit 本身仍可手動觸發。

## 6. dev extension 與正式 extension 並存

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -FromDirectory . -Extension
```

extension 解壓到 `<資料目錄>/jobfinder/extension/dev`，Chrome 以「載入未封裝項目」指向該目錄。

| 項目 | 正式 extension | dev extension |
|---|---|---|
| 清單上的名稱 | `jobfinder` | `jobfinder (dev)` |
| ID 從哪裡來 | `manifest.json` 的固定 `key`，每台機器相同 | 載入目錄的路徑 |
| 常駐目錄 | `<資料目錄>/jobfinder/extension/<tag>` | `<資料目錄>/jobfinder/extension/dev` |

**目錄搬移即換 ID**，所以那個目錄固定下來就別再動。ID 以 `chrome://extensions` 顯示的為準，取得後填進測試後端設定的 `api.extension_origin` 並重啟 API 服務——後端只接受這個來源的請求。

Options 的 endpoint 填測試後端的位址，token 取自該後端的 `config.yaml`。兩個 extension 各自存自己的設定，正式那張卡片不受影響。

## 7. 後端與 Chrome 不同機時

官方支援的形態是後端與瀏覽器同機（原因見 [deploy](../deploy.md) §5）。測試環境若把後端放在另一台，唯一可行的接法是把那台的 loopback port 轉送到本機 loopback，Options 填轉送後的本機位址。這條路由使用者自行實作與維護，本 repo 不提供作法。

同時連線多個測試後端時，每個後端各佔一個本機 port，彼此不得互借；Options 在這些位址之間切換，決定這一輪連的是哪一台。

## 8. 在測試環境跑哪些驗收

| 驗收 | 在哪跑 | 內容 |
|---|---|---|
| live 驗收 | Linux 測試環境（需與開發環境同機） | `mise run verify-live`：真來源抓取、真格式、真 Agent 的篩選評分與求職信、冪等與 Run 統計。判準見 [verify](../verify.md) §6 |
| 部署驗收人工組 | 各平台測試環境 | D4 Side Panel 直連、D7 PATH 與診斷、D8 Agent CLI 可執行、D9 服務重啟不卡死。判準見 [verify](../verify.md) §6.1 |
| bootstrap 的 extension 模式 | 有 Chrome 的測試環境 | D10：解壓目錄、`manifest.json` 的版本、Windows 無 Mark of the Web、不建立任何服務 |

`mise run verify-live` 需要 git clone，因此只有與開發環境同機的測試環境跑得動。其餘平台的驗收是人工的。

## 9. 換版與停用

換版重跑第 3 節打包與該平台的部署指令，腳本自行走 `update`。更新只替換執行檔與排程定義，資料庫原地保留；跨 schema 版本的回滾必須先還原升級前的備份，`update` 不代為備份。

停用測試環境時逐項移除，不要刪整棵安裝根——Chrome 讀的 extension 目錄就在底下。步驟見 [上手指南](getting-started.md) §10.4。
