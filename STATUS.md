# STATUS — job-finder（MVP 開發）

> 最後更新：2026-08-19。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- private repo 下 `scripts/bootstrap/install.sh`／`install.ps1` 無法驗證：兩者以匿名 `curl`／`Invoke-WebRequest` 打 `api.github.com/releases/latest` 與 `releases/download`，private repo 一律 404；`getting-started.md` §3.1 的 `raw.githubusercontent.com` 單行安裝同理。這是設計取捨（bootstrap 服務的是公開使用者），不是缺陷，但實測只能排在轉 public 之後。

## §2 未完成任務

**公開前置（依序完成後才轉 public）**

- [ ] 部署人工 gate：`docs/verify.md` §6.1 的 D1–D9。

  | 平台 | 通過 | 剩餘 |
  |---|---|---|
  | Linux | D1／D2／D3／D5／D6（release v0.1.0 工件實測）、D4（`runbook-extension.md` §8 的 tunnel 形態，Windows Chrome 經 IAP 通道連本機 API） | 無 |
  | Windows | D1／D2／D3／D4／D7／D8（release v0.1.1 工件實測，含 extension v0.1.1 實裝與重裝） | D5／D6／D9 |

  Windows D5／D6 需要前後兩個都含 `jobfinderw.exe` 的版本，`v0.1.0` 的工件沒有那支執行檔，發出 `v0.1.2` 後才驗得動。D5／D6／D9 由使用者決定延後到有對應情境時再驗。

  bootstrap 腳本路徑仍待驗（見 §1，須待轉 public）。

- [ ] 這台 Linux 機器目前跑的是 dev 建置（`mise run deploy-update`），不是 release。以 `v0.1.1` 之後的 release 工件重裝，才回到「跑的是正式版」的狀態。

- [ ] 求職信修正上版後的現場收尾：使用者 Windows 機器的 `worker.paused` 改回 false 並重啟服務；job 154 仍停在 `letter_requested`，取件後會依新規則得到結果（過審、跑滿輪數的最終版，或呼叫失敗即 `letter_failed`）。

- [ ] 抓取階段沒有任何可觀測性：`internal/crawler` 與 `pipeline.Fetch` 全程不寫日誌，且整批爬完才一次寫入 SQLite，一趟十分鐘以上的作業對使用者只有「排程工作仍在執行中」一個訊號，分不出正常與卡死。至少要在每個查詢、每頁、每 N 筆內頁記一行。與下方「API 請求層 log」屬同一類但更具體。

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
