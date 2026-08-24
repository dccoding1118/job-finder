# STATUS — job-finder（MVP 開發）

> 最後更新：2026-08-23。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- private repo 下 `scripts/bootstrap/install.sh`／`install.ps1` 無法驗證：兩者以匿名 `curl`／`Invoke-WebRequest` 打 `api.github.com/releases/latest` 與 `releases/download`，private repo 一律 404；`getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝同理。這是設計取捨（bootstrap 服務的是公開使用者），不是缺陷，但實測只能排在轉 public 之後。

- 目前只有正式環境，沒有測試環境：實機驗收只能把 checkout 建置或 release 工件裝進正式安裝位置（`mise run deploy-install`／`deploy-update`），代價是正式服務短暫停機、版號變成 `dev (<commit>)`、且與正式資料庫共用同一份資料。過渡期照這個方式跑，額外守則兩條——動到 schema 的改動實測前先備份 `~/.local/share/jobfinder/jobs.db`（連同 `-wal`、`-shm`）；驗完以 `mise run deploy-rollback` 或 release 工件的 `update` 回到正式版。測試環境建立之前不改這個作法。

- 測試 extension 不必經 GitHub 或發版：`release.yml` 的打包步驟就是「複製 `extension/`、改寫 `manifest.json` 的 `version`、壓成 zip」，本機以 `python3` 的 `zipfile` 即可重現（這台沒有 `zip` 指令）。要讓測試版與正式版**同時**存在於同一個 Chrome，關鍵是 `manifest.json` 內的固定 `key` 必須移除或改掉——ID 由它決定，兩個同 ID 的未封裝 extension 無法並存。移除 `key` 後 ID 改由載入目錄路徑決定，固定目錄即得到固定的測試 ID，該 ID 要填進測試後端設定的 `api.extension_origin`。

- 隔離的測試後端不需要動 `internal/paths`：`serve`、`run` 都收 `--config`，而 `db.path`、`profile.path`、`log.file`、`api.addr`、`api.token`、`api.extension_origin` 全在設定檔內，因此第二份設定檔就足以撐起一個獨立實例。服務掛載走 `systemd-run --user --unit=<name>`（`scripts/verify/run-live.sh` 已用這個方式跑 transient 單元），不寫進 `~/.config/systemd/user/`，正式的 `jobfinder-api.service` 不受影響。

## §2 未完成任務

**公開前置（依序完成後才轉 public）**

- [ ] bootstrap 腳本路徑的實測：`install.sh` 與 `install.ps1` 三種模式的匿名下載路徑，以及 `getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝。須待轉 public（見 §1）。`docs/verify.md` §6.1 的 D1–D9 與 D5A／D6A／D6B 已於兩平台全數通過，只剩這一項。

- [ ] `install.sh` 與 `install.ps1` 改為三種模式：**只裝後端**（現行行為：下載平台工件、驗 `SHA256SUMS`、解壓、交棒 `jobfinder install`／`update`）、**只裝 extension**、**兩者都裝**。轉 public 只解決匿名下載 404，不會補上 extension 這條 lane——兩支腳本從頭到尾只下載該平台的工件，extension zip 沒有任何腳本會去拿。實作要點：
  - extension 模式：下載 `jobfinder-extension_<tag>.zip`、驗 checksum、解壓到 `getting-started.md` §5.1 已定的版本目錄（Linux `~/.local/share/jobfinder/extension/<tag>`、Windows `%LOCALAPPDATA%\jobfinder\extension\<tag>`）、Windows 另跑 `Unblock-File`，然後印出 Chrome 的四個手動步驟與 extension ID。extension 沒有任何安裝語意（無設定渲染、無 token、無排程、無生效面驗證），所以整條路留在腳本內，不進 `jobfinder install`、不併進平台工件。
  - 預設維持只裝後端：`getting-started.md` §3.1 的單行安裝目前承諾的就是裝後端，改預設會讓已寫進文件的那一行行為變樣。另外兩種走明確旗標。
  - 遠端拓撲靠 extension 模式成立：那台 Windows 沒有後端也不該被裝出一個後端服務。
  - 同批修掉 `install.ps1` 解壓後未 `Unblock-File` 的缺口（MOTW 會傳給解出來的 exe），並讓後端模式依現場有無既有安裝自動選 `install` 或 `update`。
  - **不補 `gh` 下載路徑**：公開後匿名路徑就通了，補了只為了在 private 下先測一次，之後即是死碼。因此這項是「先寫、轉 public 當天隨即實測」。
  - Chrome 的「載入未封裝項目」沒有 CLI 入口（`docs/verify.md` 明列為整套流程中唯一必須人工完成的部分），腳本的終點是把目錄準備好並印出後續步驟；移除舊卡片、載入新目錄、重填 Options 這四步永遠是手動。

- [ ] 公開 GitHub repo。多數資安與對外可見度設定被 private＋免費方案擋住，須依下列**硬順序**在轉 public 當天一次做完（Dependabot alerts 與 automated security fixes 已於 private 階段開啟）：
  1. 本地備妥 `.github/workflows/codeql.yml`（**先別推**——private repo 的 `analyze` job 會恆紅）。
  2. `gh repo edit dccoding1118/job-finder --visibility public`（直接生效，不需 `--accept-visibility-change-consequences`，該旗標在部分 gh 版本會報 unknown flag）。
  3. 開啟 secret scanning ＋ push protection、Private vulnerability reporting（`SECURITY.md` 指向後者）。
  4. 推 codeql 分支並開 PR，讓 CI ＋ codeql 在**已 public** 的 repo 上首跑；README 補上 CodeQL badge。
  5. 全綠合併 → 設 main 分支保護（required status checks 填 `check`、`windows`、`analyze`；solo dev 不設 required reviews，會卡死自己）。
  6. 轉 public 後補驗 bootstrap 腳本：`install.sh` 與 `install.ps1` 三種模式（只裝後端／只裝 extension／兩者）的匿名下載路徑，以及 `getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝（見 §1）。

  `v0.1.0` 至 `v0.3.4` 已於 private 階段發出（工件與 checksum 齊備、版號注入正常），轉 public 後不需重打。

**與公開無關（可獨立進行）**

- [ ] 生效面驗證失敗時附上服務輸出（`docs/changes/change-update-effect-surface.md` §2 D3）的實機驗證：以 `v0.2.0` 工件對 schema 10 的資料庫跑 `update`，錯誤訊息應在「服務不是 active」之後附上 `database schema version 10 is newer than supported version 9`。此情境不能用連續兩次 `rollback` 製造——回滾只退一版。

- [ ] 建立隔離的測試環境。目標是同一台 Linux 同時跑正式與測試兩套、Windows Chrome 同時掛正式與測試兩個 extension，兩邊互不影響。落點與範圍：
  - **測試後端**：獨立設定檔（自己的 `api.addr` 埠、`db.path`、`profile.path`、`log.file`、`api.token`），以 `systemd-run --user` 掛 transient 單元跑 `serve --config`，正式服務不停機也不改動。
  - **測試資料**：資料庫取正式庫的副本，不共用檔案；預設不掛抓取排程——抓取與 Agent 呼叫共用同一組外部額度，兩套同時自動跑會重複消耗。要跑抓取時以 `run --config` 手動觸發。
  - **測試 extension**：本機打包腳本（複製 `extension/`、改寫 `manifest.json` 的 `version` 與名稱、移除固定 `key`、輸出到固定目錄），產物可直接載入 Chrome，與正式 extension 並存；測試後端設定的 `api.extension_origin` 填該測試 ID。
  - **Windows 連線**：另開一條通往測試埠的通道，測試 extension 的 Options 指向它。
  - **binary 隔離（已定案）**：`jobfinder install` 不加 `--instance`。安裝流程服務的是正式環境，測試實例的執行檔放自己的目錄、以完整路徑執行、不進 PATH，也不經 `install`／`update`／`rollback`——否則測試用的建置會覆蓋掉正式環境的 binary，正是要避免的事。
  - **文件**：`docs/changes/change-test-environment.md` 記動機與決策，`docs/deploy.md`、`docs/verify.md`、`docs/guides/runbook-extension.md` 落最新狀態。

**Roadmap（暫不實作，規劃見 `docs/roadmap.md`）**

- [ ] S1：反向校準閉環（前端入口，不做 CLI 指令）、每日高分職缺推送、成效統計、深入評估、履歷匯入產生 Profile 草稿、多 Profile。
- [ ] `requirements` 增設語意硬排除欄位（如 `exclude_conditions[]`，自然語言、併入 Filter Agent 該次呼叫逐條判定，JD 未提及回 `unknown`）：「不接受海外出差／外派」「不接受輪班」這類需求既不是產業也難以用關鍵字精準命中，現有六個 requirements 欄位皆無法承接，目前只能填 `intents.content_dislikes` 走軟性扣分。
- [ ] S2 LLM 直串 API：此專案對 LLM 的使用沒有工具呼叫，最適合的是 LLM API 而非 Agent；須針對篩選、評分與求職信生成／審核增加 LLM API 機制，並將每日篩選、評分的硬上限改為軟上限（達門檻告警但可續行）。提供 LLM API 與 agent binary 兩種配置（使用 agent 時由使用者自行配置環境 path 與訂閱額度）。
- [ ] S2 extension 連線模式：Options 增設自部署 token／雲端帳號兩種模式，endpoint 改用 `optional_host_permissions` 並於使用者填入自有網域時 runtime 請求授權。
- [ ] S2 來源能力矩陣與雙層開關：來源標記 `mode`；「此部署是否開放該來源」為部署層設定、「使用者是否啟用」存 store 並由設定 UI 開關。
- [ ] S2 Chrome Web Store 上架：隱私政策頁、廣域 optional host permission 的用途說明；上架後 `api.extension_origin` 改為內建預設值。
- [ ] S2 集中式運作日誌：在 extension UI 增加更詳細的逐行運作日誌，分別查看 UI／API 運作、worker 批次、fetch 批次、求職信處理。執行中作業、批次執行狀態與中文用語已落地（見 `docs/changes/change-run-observability.md`）。
- [ ] S2 設定功能：將配置檔內其餘功能提供到 extension 上配置（每日上限、掃描間隔、去重門檻、LLM 路由等；自動篩選與評分開關已落地）。處理量告警待 LLM 直串 API 的軟上限一併處理。
- [ ] 工程面跨階段項：API 請求層 log、worker 心跳與卡住偵測、來源健康度（上次成功抓取時間與連續失敗次數）、薪資字串解析涵蓋度、Agent「輸出已過契約驗證但 Invoke 回錯」不重跑的重試契約。
