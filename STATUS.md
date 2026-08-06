# STATUS — job-finder（MVP 開發）

> 最後更新：2026-08-06。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- SQLite 檔案實際為 `0644`，`docs/deploy.md` §2 宣稱資料庫檔案為 `0600`。資料庫由 `internal/store.Open` 於執行期建立（`sql.Open("sqlite", path)`），不經 `internal/install` 的 chmod 路徑，因此永遠是 driver 的 `0666 & ~umask`。資料目錄為 `0700`，實際暴露為零；要收斂的是文件與實作的落差，二選一：`store.Open` 建檔後 chmod 為 `0600`（連同 `-wal`／`-shm`），或修正文件只宣稱目錄層 owner-only。

- Linux 的 `deploy/production/systemd/jobfinder-run.service` 缺 `--trigger timer`，Windows 的 `jobfinder-run.xml` 有。因此 Linux 上每日排程抓取全被記為 `manual-cli`，Side Panel 的 Run 歷程無法區分排程與人工觸發（已由 `runs` 表 id 7–9 證實）。修正處為該 unit 模板的 `ExecStart`。

- private repo 下 `scripts/bootstrap/install.sh`／`install.ps1` 無法驗證：兩者以匿名 `curl`／`Invoke-WebRequest` 打 `api.github.com/releases/latest` 與 `releases/download`，private repo 一律 404；`getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝同理。這是設計取捨（bootstrap 服務的是公開使用者），不是缺陷，但實測只能排在轉 public 之後。

## §2 未完成任務

**公開前置（依序完成後才轉 public）**

- [ ] 部署人工 gate：`docs/verify.md` §6.1 的 D1–D8。Linux 的 D1、D2、D3、D5、D6 已以 release v0.1.0 工件在本機通過（checksum 相符、`jobfinder version` 印出 tag、生效面 pid 與啟動時間、`runs` 有排程觸發那趟、`jobfinder.prev` 與 release binary 逐位元相同、回滾後 DB 不變）。**剩餘**：Linux D4（Side Panel 直連，本機無 Chrome，以 `runbook-extension.md` §8 的 tunnel 形態代替）、bootstrap 腳本路徑（見 §1，須待轉 public）；Windows D1–D8 全部。Windows 端可直接取 release 的 `jobfinder_v0.1.0_windows_amd64.zip` 實測，不需另行交叉編譯。

- [ ] 公開 GitHub repo。多數資安與對外可見度設定被 private＋免費方案擋住，須依下列**硬順序**在轉 public 當天一次做完（Dependabot alerts 與 automated security fixes 已於 private 階段開啟）：
  1. 本地備妥 `.github/workflows/codeql.yml`（**先別推**——private repo 的 `analyze` job 會恆紅）。
  2. `gh repo edit dccoding1118/job-finder --visibility public`（直接生效，不需 `--accept-visibility-change-consequences`，該旗標在部分 gh 版本會報 unknown flag）。
  3. 開啟 secret scanning ＋ push protection、Private vulnerability reporting（`SECURITY.md` 指向後者）。
  4. 推 codeql 分支並開 PR，讓 CI ＋ codeql 在**已 public** 的 repo 上首跑；README 補上 CodeQL badge。
  5. 全綠合併 → 設 main 分支保護（required status checks 填 `check`、`windows`、`analyze`；solo dev 不設 required reviews，會卡死自己）。
  6. 轉 public 後補驗 bootstrap 腳本：`install.sh` 與 `install.ps1` 的匿名下載路徑，以及 `getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝（見 §1）。

  首版 `v0.1.0` 已於 private 階段發出（工件與 checksum 齊備、版號注入正常），轉 public 後不需重打。

**Roadmap（暫不實作，規劃見 `docs/roadmap.md`）**

- [ ] S1：反向校準閉環（前端入口，不做 CLI 指令）、每日高分職缺推送、成效統計、深入評估、履歷匯入產生 Profile 草稿、多 Profile。
- [ ] `requirements` 增設語意硬排除欄位（如 `exclude_conditions[]`，自然語言、併入 Filter Agent 該次呼叫逐條判定，JD 未提及回 `unknown`）：「不接受海外出差／外派」「不接受輪班」這類需求既不是產業也難以用關鍵字精準命中，現有六個 requirements 欄位皆無法承接，目前只能填 `intents.content_dislikes` 走軟性扣分。
- [ ] S2 LLM 直串 API：此專案對 LLM 的使用沒有工具呼叫，最適合的是 LLM API 而非 Agent；須針對篩選、評分與求職信生成／審核增加 LLM API 機制，並將每日篩選、評分的硬上限改為軟上限（達門檻告警但可續行）。提供 LLM API 與 agent binary 兩種配置（使用 agent 時由使用者自行配置環境 path 與訂閱額度）。
- [ ] S2 extension 連線模式：Options 增設自部署 token／雲端帳號兩種模式，endpoint 改用 `optional_host_permissions` 並於使用者填入自有網域時 runtime 請求授權。
- [ ] S2 來源能力矩陣與雙層開關：來源標記 `mode`；「此部署是否開放該來源」為部署層設定、「使用者是否啟用」存 store 並由設定 UI 開關。
- [ ] S2 Chrome Web Store 上架：隱私政策頁、廣域 optional host permission 的用途說明；上架後 `api.extension_origin` 改為內建預設值。
- [ ] S2 監控功能：在 extension UI 增加集中式、更詳細的運作流程日誌與監控細節，分別查看 UI／API 運作、worker 批次、fetch 批次、求職信處理的日誌；fetch 批次歷程中的英文改成中文呈現。
- [ ] S2 設定功能：將配置檔內其餘功能提供到 extension 上配置（每日上限、掃描間隔、去重門檻、LLM 路由等；自動篩選與評分開關已落地）。處理量告警待 LLM 直串 API 的軟上限一併處理。
- [ ] 工程面跨階段項：API 請求層 log、薪資字串解析涵蓋度、Agent「輸出已過契約驗證但 Invoke 回錯」不重跑的重試契約。
