# jobfinder

[![CI](https://github.com/dccoding1118/job-finder/actions/workflows/ci.yml/badge.svg)](https://github.com/dccoding1118/job-finder/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dccoding1118/job-finder?sort=semver)](https://github.com/dccoding1118/job-finder/releases/latest)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)

匿名的 AI 求職媒合工具。持續從求職平台收集職缺，以你的去識別化 Profile 做硬條件篩選與四維評分，只把值得投的少數職缺送到你面前；你按下要求後才生成經審查的客製化求職信。投遞永遠由你手動完成。

**Chrome extension 是操作入口，也是 104 與 Cake 的資料採集器**；後端是跑在你自己機器上的 Go 服務，資料留在本機 SQLite。

## 為什麼不是平台的職缺通知

求職平台的訂閱通知本質是關鍵字、地區與薪資的結構化匹配——條件放寬就淹沒，收緊就漏掉。jobfinder 讓 LLM 讀 JD 全文對照你的 Profile 全文，逐條判定硬條件、再從內容契合、福利、加分條件與產業四個面向評分，並要求給出理由。目標是**精準少投**，不是多看多投。

## 運作方式

```
收集 ──► 硬條件篩選 ──► 四維評分 ──► 你要求 ──► 生成求職信 ──► 審查 ──► 你手動投遞
 │           │              │                        │            │
 │      結構化規則      Scorer Agent            Drafter Agent  Reviewer Agent
 │      （零 token）
 │
 ├─ Yourator：後端背景抓取（該平台搜尋路徑開放）
 └─ 104 / Cake：由 extension 擷取你瀏覽器中已載入的頁面
```

- **硬條件不花 token**：地區、年資、遠端等結構化條件先在本地判掉，全數通過的才付一次 Filter Agent。
- **缺資訊絕不判不適合**：JD 沒提到的條件記為未決，留在待看清單，不會靜默淘汰。
- **求職信按需生成**：不會為每筆推薦職缺預先燒 token。

## 三個原則

| 原則 | 意義 |
|---|---|
| **Zero-PII** | 資料庫、log、prompt 與求職信都不含姓名、Email、電話、校名或公司名。JD 夾帶的聯絡資訊在入庫前就遮罩，因此不會被送往任何外部模型。求職信落款固定為 `[你的姓名]`、`[你的聯絡方式]` |
| **Human-in-the-Loop** | 系統只產生建議與素材。不代投、不自動改寫你的 Profile、不改寫投遞歷史 |
| **來源合規** | 只處理免登入公開頁、遵守 robots.txt、不繞過任何防護。搜尋路徑受人機驗證保護的平台一律改由你自己的瀏覽器擷取，不做背景抓取 |

## 目前狀態

自部署形態可用：Linux 單機、SQLite、只綁 loopback 的 API、Chrome 原生 Side Panel。來源為 Yourator（背景抓取）與 104／Cake（extension 擷取）。

規劃中：LLM 直串 API（目前需要 headless `claude`／`codex` CLI）、Windows 部署、Chrome Web Store 上架、代管雲端版。階段規劃見 [docs/roadmap.md](docs/roadmap.md)。

## 快速開始

**前置**：Linux（systemd user session）、[mise](https://mise.jdx.dev/)、可用的 headless `claude` 或 `codex` CLI、Chrome 114+。

```bash
git clone https://github.com/dccoding1118/job-finder.git
cd job-finder
mise run deploy-install
```

`deploy-install` 會跑一次 `fmt`／`lint`／`test`／`build`，把 binary 與 systemd user unit 安裝到 XDG 位置，並從範本產生帶隨機 token 的 `~/.config/jobfinder/config.yaml`。既有設定不會被覆寫。

接著：

1. 編輯 `~/.config/jobfinder/profile.yaml` 填入你的去識別化 Profile（也可安裝後改用 extension 的全頁編輯器），以 `jobfinder profile lint` 檢查。
2. Chrome 開 `chrome://extensions` → 開發人員模式 → 載入未封裝項目 → 選 `extension/`，記下 extension ID。
3. 把 `~/.config/jobfinder/config.yaml` 的 `api.extension_origin` 換成 `chrome-extension://<你的 ID>`，然後 `systemctl --user restart jobfinder-api.service`。
4. extension 的 Options 頁填入 endpoint（預設 `http://127.0.0.1:8686`）與 config 中的 `api.token`。
5. 開啟 Side Panel 即可使用；抓取由每日 timer 觸發，也可手動 `systemctl --user start jobfinder-run.service`。

後端跑在遠端機器（例如雲端 VM）時，需自行把遠端的 loopback port 轉送到本機——不要把服務改綁 `0.0.0.0`。以 GCP IAP 為例的設定見 [docs/guides/runbook-extension.md](docs/guides/runbook-extension.md)。

更新與回滾：`mise run deploy-update`、`mise run deploy-rollback`。

## 文件

| 文件 | 內容 |
|---|---|
| [docs/PRD.md](docs/PRD.md) | 需求與範圍 |
| [docs/design.md](docs/design.md) | 系統架構與模組依賴 |
| [docs/designs/](docs/designs/) | 各模組契約 |
| [docs/deploy.md](docs/deploy.md) | 部署、release 工件與 CI |
| [docs/verify.md](docs/verify.md) | 整合與驗收案例 |
| [AGENTS.md](AGENTS.md) | 開發指南與專案現況 |

## 授權與貢獻

Copyright (c) 2026 Dennis Chan。本專案以 [AGPL-3.0](LICENSE) 授權：你可以自由使用、修改與自行部署；若你改造後拿它對外提供網路服務，必須一併公開你的修改。

**本專案目前不接受外部 Pull Request。** Issue 開放 bug 回報與功能建議，但不保證處理。詳見 [CONTRIBUTING.md](CONTRIBUTING.md)。
