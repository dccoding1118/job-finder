# change — 測試環境獨立成第三套部署

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

正式後端搬到獨立的 VM 之後，開發機的 GCP VM 與本機 Windows 都成為測試環境。原本的文件與工具只認得兩種環境——`.local-dev/` 內的開發階段驗收，以及裝在使用者環境的正式部署——測試環境沒有自己的位置，只能借用其中一邊的作法，而兩邊借過來都不對。

**借正式那邊：測試環境沒有工件可裝。** 正式環境走 release 工件，測試環境要驗的是尚未發版的改動，沒有對應的 tag。

**借開發那邊：`mise run deploy-*` 驗不到安裝入口。** 這條路徑跑完 `fmt`／`lint`／`test`／`build`，再把剛建置的 binary 直接交給 `jobfinder install`。它要求目標機器有 git clone 與完整 toolchain，而 Windows 那台兩者皆無；更關鍵的是它繞過 bootstrap 腳本，於是測試環境驗不到正式環境真正會走的那個入口。安裝入口本身就是測試環境該驗的東西之一。

**live 驗收的隔離理由已經消失。** `mise run e2e-live` 在 `.local-dev/verify/` 內物化自己的 binary、config 與 transient unit，隔離是為了不汙染日常資料。正式資料搬走之後，這台機器的 `jobs.db` 本來就是測試資料，而合成 config 加 transient unit 與實機之間仍有距離——真來源與真 Agent 值得在實機上驗。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 環境分三種：開發階段驗收、測試環境、正式環境。三者各有自己的素材與安裝入口，測試環境與正式環境只差素材一項，安裝路徑逐字相同。 |
| D2 | 新增 dev 部署包，形狀與 release 工件相同：平台包、獨立的 extension zip、`SHA256SUMS`。 |
| D3 | 打包邏輯集中在 `scripts/release/pack.sh`，`release.yml` 與本機打包共用。版本字串是參數，本機打包預設 `dev`。 |
| D4 | 版本字串為 `dev` 時，打包移除 `manifest.json` 的固定 `key` 並把 `name` 改為 `jobfinder (dev)`。測試 extension 與正式 extension 能並存於同一個 Chrome，因此是打包的保證。 |
| D5 | bootstrap 腳本新增本地來源模式 `--from-dir`／`-FromDirectory`：跳過 GitHub API 與下載，改讀該目錄的 `SHA256SUMS` 驗檔後照原路徑交棒。checksum 比對與遠端模式共用同一條程式碼。 |
| D6 | 刪除 `scripts/deploy/*.sh` 與 `mise run deploy-*`。測試環境改走 D2 的部署包加 D5 的本地模式。 |
| D7 | live 驗收升級為測試環境實機驗收。`mise run e2e-live` 改名 `mise run verify-live`，目標從隔離沙盒換成本機已安裝的測試環境。 |
| D8 | `e2e-mock` 與 `e2e-deploy` 維持隔離沙盒，理由是可重複性：前者要固定輸入得固定輸出，後者每趟必須從無既有安裝開始。 |
| D9 | extension 的常駐目錄一律以版本字串命名，release 工件為 tag、部署包為 `dev`。 |
| D10 | 文件分工：產品能做什麼寫 `deploy.md`，官方建議怎麼安排寫 `docs/guides/test-environment.md`，開發者怎麼驗寫 `AGENTS.md`。 |
| D11 | `.local-dev/` 依環境分目錄：`dev-verify/` 承載開發階段驗收，`test-deploy/` 承載測試環境部署包。兩者都是可重建的產物。 |

D3 的共用是為了讓「部署包與 release 工件同形狀」有機械保證。兩份各自維護的打包邏輯會漂移，而漂移的症狀是測試環境驗過的安裝流程與正式環境實際走的不是同一條。

D7 之後 live 驗收需要 git clone 才跑得動，因此只適用與開發環境同機的 Linux 測試環境。Windows 測試環境的實機驗收維持人工。

## 3. 相對舊狀態的差異

| 主題 | 舊狀態 | 新狀態 |
|---|---|---|
| 環境種類 | 開發階段驗收、正式部署 | 開發階段驗收、測試環境、正式環境 |
| 測試環境的素材 | 無；開發機借 `mise run deploy-*`，其他機器只能裝 release 工件 | dev 部署包，由開發環境打包產出 |
| 測試環境的安裝入口 | `jobfinder install`（直接呼叫） | bootstrap 腳本的本地模式，與正式環境同一個入口 |
| 打包邏輯 | 寫在 `release.yml` 的步驟內，本機無法重現 | `scripts/release/pack.sh`，兩個呼叫者共用 |
| 測試 extension | 無 | 部署包的 extension zip，`key` 已移除、`name` 標記 `dev` |
| live 驗收 | `mise run e2e-live`，隔離沙盒內的合成 config 與 transient unit | `mise run verify-live`，打在已安裝的測試環境 |
| `.local-dev/` | `verify/` 一個沙盒承載全部驗收 | `dev-verify/` 與 `test-deploy/` 依環境分開 |
| 測試環境的說明 | 無 | `docs/guides/test-environment.md` |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/deploy.md` | §1 | 開發階段驗收收斂為一段定位，作法指向 `AGENTS.md` §5 |
| `docs/deploy.md` | §4 | 素材與安裝入口的對照換成三列：release 工件、dev 部署包、開發階段驗收（不裝進使用者環境）。`--from-dir`／`-FromDirectory` 的語意 |
| `docs/deploy.md` | §7 | dev 部署包的工件清單與 `pack.sh` 的兩個呼叫者；`dev` 版本字串對 extension `key` 與 `name` 的影響 |
| `docs/guides/test-environment.md` | 全檔（新增） | 官方建議的測試環境做法 |
| `AGENTS.md` | §5 | `.local-dev/dev-verify/` 沙盒定位、`verify-live` 的目標是測試環境 |
| `AGENTS.md` | §6 | 刪除 `scripts/deploy/*.sh` 的敘述，改指 `deploy.md` §4 與測試環境 guide |
| `docs/verify.md` | §1 | 沙盒路徑更名，`e2e-live` 換成 `verify-live` 並標明其目標 |
| `docs/verify.md` | §6 | live 案例改為測試環境實機，判準從「空的 live SQLite」改為增量比對 |
| `docs/verify.md` | §6.1 | D 系列的自動組與人工組各自標明所屬環境 |
| `README.md` | 文件索引 | 加入測試環境 guide |

## 5. 待實作進度

| # | 項目 | 狀態 |
|---|---|---|
| 1 | `scripts/release/pack.sh`：吃版本字串與目標平台，產出平台包、extension zip 與 `SHA256SUMS` | ✅ |
| 2 | `release.yml` 的建置與打包改為呼叫 `pack.sh` | ✅ |
| 3 | `scripts/bootstrap/install.sh` 與 `install.ps1` 的本地來源模式 | ✅ |
| 4 | 兩支 bootstrap 寫死的 extension ID 常數：本地模式改印出載入目錄 | ✅ |
| 5 | 刪除 `scripts/deploy/*.sh`、`scripts/verify/run-live.sh`、`configs/verify.live.yaml`、`lib.sh` 的 live 常數、`harness/deploy.sh` 的 live 渲染段 | ✅ |
| 6 | `mise.toml`：刪 `deploy-*` 與 `e2e-live`，新增 `pack` 與 `verify-live` | ✅ |
| 7 | `.local-dev/` 目錄更名，同步 `lib.sh` 的沙盒根 | ✅ |
| 8 | Linux 測試環境以 dev 部署包重裝，作為新安裝路徑的第一次實機驗證 | ⏳ |
| 9 | Windows 測試環境部署 dev 包、載入 dev extension、建立第二條通道 | ⏳ |
| 10 | 測試環境跑一次 live 驗收 | ⏳ |

## 6. 已知殘留限制

- **`verify-live` 需要 git clone**，只適用與開發環境同機的 Linux 測試環境。Windows 測試環境的 D7／D8／D9 維持人工。
- **dev 部署包沒有公開的下載來源**。它不進 GitHub Release，跨機部署靠檔案傳輸，完整性由包內的 `SHA256SUMS` 保證。
- **dev extension 的 ID 由載入目錄決定**。目錄搬移即換 ID，測試後端的 `api.extension_origin` 必須跟著改。固定目錄是取得固定 ID 的唯一手段。
- **部署包只保留一版**。回滾點由 `jobfinder update` 的 `jobfinder.prev` 提供，切換受測版本的手段是換 branch 重新打包。
- **`verify-live.sh` 尚未實跑**。它改寫成打在已安裝環境，真來源與真 Agent 的一趟完整執行要等 Linux 測試環境部署完成才驗得到。
- **`internal/install` 的 asset 解析仍支援 git checkout 佈局**（`deploy/production/<平台>/`、`bin/`）。刪掉 `scripts/deploy/*.sh` 之後這條分支沒有呼叫者，是否一併移除待決。
