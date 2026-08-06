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

自部署形態可用：Linux 與 Windows 單機、SQLite、只綁 loopback 的 API、Chrome 原生 Side Panel。來源為 Yourator（背景抓取）與 104／Cake（extension 擷取）。

規劃中：LLM 直串 API（目前需要 headless `claude`／`codex` CLI）、Chrome Web Store 上架、代管雲端版。階段規劃見 [docs/roadmap.md](docs/roadmap.md)。

## 快速開始

**前置**：Linux（systemd user session）或 Windows 10/11、可用的 headless `claude` 或 `codex` CLI、Chrome 114+。

安裝腳本只做下載與 checksum 驗證，實際安裝由 binary 的 `jobfinder install` 完成：它決定路徑、產生 token、渲染設定、掛上排程，並在**執行中的 process** 上驗證結果。既有的設定、Profile、denylist 與資料庫一律不覆寫。

**Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.sh | bash
```

**Windows（PowerShell）**

```powershell
irm https://raw.githubusercontent.com/dccoding1118/job-finder/main/scripts/bootstrap/install.ps1 | iex
```

也可以到 [Releases](https://github.com/dccoding1118/job-finder/releases/latest) 自行下載工件、比對 `SHA256SUMS`，解壓後直接執行其中的 `jobfinder install`（Windows 為 `jobfinder.exe install`）——結果完全相同。安裝完成後解壓目錄即可刪除：常駐執行的是安裝到使用者目錄的另一份副本。

安裝後 `jobfinder paths` 會印出這台機器實際使用的所有位置。接著：

1. 編輯 `jobfinder paths` 列出的 `profile` 填入你的去識別化 Profile（也可改用 extension 的全頁編輯器），以 `jobfinder profile lint` 檢查。
2. Chrome 開 `chrome://extensions` → 開發人員模式 → 載入未封裝項目 → 選 `extension/`，記下 extension ID。
3. 把設定檔的 `api.extension_origin` 換成 `chrome-extension://<你的 ID>`，然後重啟 API：Linux `systemctl --user restart jobfinder-api.service`，Windows `Restart-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'api'`。
4. extension 的 Options 頁填入 endpoint（預設 `http://127.0.0.1:8686`）與設定檔中的 `api.token`。
5. 開啟 Side Panel 即可使用；抓取由每日排程觸發，也可手動觸發：Linux `systemctl --user start jobfinder-run.service`，Windows `Start-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'`。

更新與回滾：重跑安裝腳本（或對新版工件執行 `jobfinder update`），出問題則 `jobfinder rollback`。

**逐步走完整趟**（含手動驗 checksum、Profile 設定、extension 串接、日常操作對照與排障）見 **[上手指南](docs/guides/getting-started.md)**。

後端與瀏覽器不同機時，需自行把遠端的 loopback port 轉送到本機——不要把服務改綁 `0.0.0.0`。這是選配路徑，以 GCP IAP 為例的設定見 [遠端後端通道](docs/guides/runbook-extension.md)。

開發 checkout 另有 `mise run deploy-install`／`deploy-update`／`deploy-rollback`：先跑 `fmt`／`lint`／`test`／`build`，再把剛建置的 binary 交給同一組子命令。

## 文件

| 文件 | 內容 |
|---|---|
| [docs/guides/getting-started.md](docs/guides/getting-started.md) | 上手指南：安裝、設定、extension 串接、日常操作與排障 |
| [docs/PRD.md](docs/PRD.md) | 需求與範圍 |
| [docs/design.md](docs/design.md) | 系統架構與模組依賴 |
| [docs/designs/](docs/designs/) | 各模組契約 |
| [docs/deploy.md](docs/deploy.md) | 部署、release 工件與 CI |
| [docs/verify.md](docs/verify.md) | 整合與驗收案例 |
| [AGENTS.md](AGENTS.md) | 開發指南與專案現況 |

## 授權與貢獻

Copyright (c) 2026 Dennis Chan。本專案以 [AGPL-3.0](LICENSE) 授權：你可以自由使用、修改與自行部署；若你改造後拿它對外提供網路服務，必須一併公開你的修改。

**本專案目前不接受外部 Pull Request。** Issue 開放 bug 回報與功能建議，但不保證處理。詳見 [CONTRIBUTING.md](CONTRIBUTING.md)。
