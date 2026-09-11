# STATUS — job-finder（MVP 開發）

> 最後更新：2026-09-11。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- 環境分工已定案：正式後端只有一份，放一台新的 GCP VM；本台 GCP VM 與本機 Windows 都是測試部署；改動先在兩個測試後端驗過，再跑 release 發布到正式 VM。正式搬離本台之後，這台的 `mise run deploy-install` 不再覆蓋任何正式資產，測試安裝就是這台機器上的一般安裝，不需要獨立設定檔與 transient 單元那套。

- 正式後端不放兩份：兩台各跑一份就是兩份各自獨立的 `jobs.db`，職缺狀態、去重分群與求職信歷程各記各的；且兩邊都掛每日抓取時，同一組 Agent 額度會被消耗兩次。Windows 那台的價值在於它是 D7／D8／D9 唯一能跑的地方，不在於當正式的第二個家。

- 正式後端選 Linux 而非 Windows 桌機：每日抓取的排程契約是錯過不補跑（Linux `Persistent=false`、Windows `StartWhenAvailable=false`），Windows 桌機關機一晚就漏一天；Linux 側有 linger，登出關終端都不影響常駐。

- 官方支援的部署形態只有一種：後端與瀏覽器同機。`extension/manifest.json` 的 `host_permissions` 只有 `http://127.0.0.1/*` 與 `http://[::1]/*`，`internal/install/smoke.go` 的 `assertLoopback` 又會在安裝時拒絕非 loopback 的 `api.addr`——這是程式碼強制的邊界。遠端後端由使用者自理，本 repo 不提供作法；個人的 GCP IAP 通道手冊已移出版控到 `.local-dev/personal-ops/runbook-extension.md`。讓 Options 的位址欄位能真正填遠端主機，需要 extension 改用 `optional_host_permissions`，屬 roadmap S2。

- 測試 extension 不必經 GitHub 或發版：`release.yml` 的打包步驟就是「複製 `extension/`、改寫 `manifest.json` 的 `version`、壓成 zip」，本機以 `python3` 的 `zipfile` 即可重現（這台沒有 `zip` 指令）。要讓測試版與正式版**同時**存在於同一個 Chrome，關鍵是 `manifest.json` 內的固定 `key` 必須移除或改掉——ID 由它決定，兩個同 ID 的未封裝 extension 無法並存。移除 `key` 後 ID 改由載入目錄路徑決定，固定目錄即得到固定的測試 ID，該 ID 要填進測試後端設定的 `api.extension_origin`。

## §2 未完成任務

**公開後的部署收尾**

- [ ] **步驟四**：把本台 GCP VM 與本機 Windows 轉為測試環境。
  - **本台**：停止並移除正式的 systemd unit，改以 `mise run deploy-install` 當測試安裝（開發機走 working tree 建置，版號為 `dev`）。每日抓取的 timer 已停用，要抓取時手動 `jobfinder run`——Agent 額度只有一組。
  - **Windows**：既有安裝改為測試用途，`api.extension_origin` 改填測試 extension 的 ID。
  - **Windows 通道**：新增第二條指向本台 VM 的通道，與正式通道並存——排程工作 `Jobfinder-Api-Tunnel-Test`、local port `28686`、常駐目錄 `%LOCALAPPDATA%\jobfinder-tunnel-test\`、wrapper `jobfinder-tunnel-test.ps1`。作法見 `.local-dev/personal-ops/runbook-extension.md` §9。
  - **測試 extension**：本機打包（複製 `extension/`、改寫 `manifest.json` 的 `version` 與名稱、移除固定 `key`、輸出到固定目錄），與正式 extension 並存；Options 在 `http://127.0.0.1:8686`（Windows 本機測試後端）與 `http://127.0.0.1:28686`（本台測試後端）之間切換。
  - **文件**：`docs/changes/change-test-environment.md` 記動機與決策，`docs/deploy.md` 與 `docs/verify.md` 落最新狀態。

**與公開無關（可獨立進行）**

- [ ] 生效面驗證失敗時附上服務輸出（`docs/changes/change-update-effect-surface.md` §2 D3）的實機驗證。情境仍成立：現行 `schemaVersion` 為 10（`internal/store/store.go:23`），`v0.2.0` 為 9，以該工件對 schema 10 的資料庫跑 `update`，錯誤訊息應在「服務不是 active」之後附上 `database schema version 10 is newer than supported version 9`。此情境不能用連續兩次 `rollback` 製造——回滾只退一版。

  待驗的範圍已收窄到一件事：**診斷文字真的從 journald 或 `log.file` 取得**。錯誤訊息的組裝邏輯由 `internal/install/sequence_test.go` 的 `TestVerifyEffectCarriesTheServiceReasonIntoTheError` 守著，但該測試的 diagnosis 是注入的字串，不會真的呼叫 `journalctl`（`internal/install/systemd.go:151`）或讀 Windows 的 `LastTaskResult`。排在步驟四之後、於測試環境進行。

- [ ] Windows 側的部署驗收自動組：Linux 側已由 `mise run e2e-deploy` 落地（D1／D2／D3／D5／D5A／D6／D6B）。Windows 對稱做法是以 `$env:LOCALAPPDATA` 指向隔離根，並在 PATH 最前放一個 `powershell.cmd` 攔截 `Register-ScheduledTask`（`.cmd` 在 `PATHEXT` 內，Go 的 `exec.LookPath` 會先找到它）；此路徑尚未在 Windows 實機驗證過。

- [ ] 讓 Windows 端的 local ssh forward 不再有 PuTTY 視窗。**排在步驟三與步驟四完成之後**才評估。背景與方案：
  - **現況**：正式後端在遠端 Linux，而官方形態只支援 loopback（見 §1），所以 Windows 這端必須自行把遠端的 `8686` 轉送到本機 `18686`。作法留在 `.local-dev/personal-ops/runbook-extension.md`。
  - **視窗的根因**：Windows 版 gcloud SDK 內附 `putty.exe`，`gcloud compute ssh` 預設呼叫它，而它是 GUI subsystem 程式，會自己建視窗——與工作是否背景執行無關。與 `deploy.md` §2 講 `jobfinderw.exe` 存在的理由是同一件事，差別在 PuTTY 沒有無視窗版本可換。
  - **待評估方案一**：Task Scheduler 的工作改勾「不論使用者是否登入均執行」。工作在非互動 session 跑，視窗不畫到桌面，工作管理員仍看得到行程。代價是要儲存 Windows 帳號密碼；且該模式沒有桌面可彈對話框，PuTTY 一旦需要互動（host key 確認、key passphrase）就會卡住，前置必須先在前景做完。
  - **待評估方案二**：改用 Windows 10 內建的 OpenSSH `ssh.exe`（console subsystem），以 `wscript.exe` 跑 `WScript.Shell.Run(cmd, 0, False)` 隱藏啟動，走 IAP 時用 `gcloud compute start-iap-tunnel --listen-on-stdin` 當 ProxyCommand。不必存密碼，零件較多。
  - **已知限制**：換網路（有線換手機熱點）一定會斷，這是 TCP 的必然而非 SSH 的缺陷；要撐過換網路只能改用 UDP 且無連線狀態的 VPN，那需要在 VM 開 inbound port，本輪未採用。

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
