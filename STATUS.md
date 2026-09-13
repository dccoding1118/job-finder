# STATUS — job-finder（MVP 開發）

> 最後更新：2026-09-13。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- 正式後端不放兩份：兩台各跑一份就是兩份各自獨立的 `jobs.db`，職缺狀態、去重分群與求職信歷程各記各的；且兩邊都掛每日抓取時，同一組 Agent 額度會被消耗兩次。Windows 那台的價值在於它是 D7／D8／D9 唯一能跑的地方，不在於當正式的第二個家。

- 正式後端選 Linux 而非 Windows 桌機：每日抓取的排程契約是錯過不補跑（Linux `Persistent=false`、Windows `StartWhenAvailable=false`），Windows 桌機關機一晚就漏一天；Linux 側有 linger，登出關終端都不影響常駐。

- 本台 GCP VM 同時是開發環境與 Linux 測試環境：開發階段驗收落在 `.local-dev/dev-verify/`，測試環境落在使用者環境的標準位置，兩者不重疊。Windows 是另一台測試環境，也是 D7／D8／D9 唯一能跑的地方。

- 來源端點維持現狀：抓取批次目前只為 Yourator 而寫，`sources.yourator.base_url` 選填、缺省走程式內的 `https://www.yourator.co`，安裝渲染的設定不帶這一行。**接入第二個抓取來源時**改為每個來源的端點一律由設定檔明確提供、缺少即報錯，屆時不得保留程式內預設值，`docs/designs/design-pipeline.md` 的 `sources.<name>` 列與設定範本一併改寫。

- 測試環境的實機自動驗收要重新設計，現行 `scripts/verify/verify-live.sh` 不可用。它是從隔離沙盒版改寫而來，直接對已安裝環境跑 `run --stage`，但常駐 worker 持有 worker 鎖，第 01 步即被拒；且第 04 步 filter 以每日額度為上限，實機上會一次呼叫 60 次。重新設計的要求：
  - **定位**：測試環境的實機驗收，不是開發階段驗收，不必由 `mise` 觸發。同一份情境設計產出 Linux 與 Windows 兩支腳本。
  - **流程**：先把測試環境暫時安排成可自動驗證的情境（例如 worker 鎖、`auto_processing`、`worker.paused`），跑一輪、產出報告，最後把環境復原成跑之前的設定；中途失敗也要復原。
  - **成本**：每種驗證只跑一筆——一個職缺依序跑一次篩選、評分、寫信、批改信，不得出現批次篩選。
  - **情境**：實機不一定每一步都有現成情境，缺的情境由腳本自行安排，不要求使用者事先把環境調成特定狀態。
  - 開工時另立 `docs/changes/` change 文件，改寫 `docs/verify.md` §6 與測試環境指南 §8。

## §2 未完成任務

**測試環境獨立成第三套部署**

- [ ] 重新設計測試環境的實機自動驗收（要求見 §1）。

**與公開無關（可獨立進行）**

- [ ] 生效面驗證失敗時附上服務輸出（`docs/changes/change-update-effect-surface.md` §2 D3）的實機驗證。情境仍成立：現行 `schemaVersion` 為 10（`internal/store/store.go:23`），`v0.2.0` 為 9，以該工件對 schema 10 的資料庫跑 `update`，錯誤訊息應在「服務不是 active」之後附上 `database schema version 10 is newer than supported version 9`。此情境不能用連續兩次 `rollback` 製造——回滾只退一版。

  待驗的範圍已收窄到一件事：**診斷文字真的從 journald 或 `log.file` 取得**。錯誤訊息的組裝邏輯由 `internal/install/sequence_test.go` 的 `TestVerifyEffectCarriesTheServiceReasonIntoTheError` 守著，但該測試的 diagnosis 是注入的字串，不會真的呼叫 `journalctl`（`internal/install/systemd.go:151`）或讀 Windows 的 `LastTaskResult`。排在測試環境就緒之後進行。

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
