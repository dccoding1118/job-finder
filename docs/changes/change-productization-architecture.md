# 變更 — 產品化定位改為 extension-first 的 open-core 架構

## 1. 背景與動機

原 roadmap S2 規劃「將本機 extension 的 Profile 表單與求職流程**遷入**具登入的 Web 介面」，隱含假設是 extension 只是自用階段的過渡 UI，產品化後由 Web 取代。

此假設不成立。104 與 Cake 的半被動擷取只能在使用者已登入的瀏覽器中發生，而半被動是新增來源的預設路線（AGENTS.md §4 規則 5）。**extension 同時是唯一 UI 與唯一的受保護平台資料採集器**——移除它等於一併移除「使用者自帶資料」這條合規供給路線，直接把整個資料供給壓回商業化爬取，那正是本專案列為最高風險的一項。

因此產品化的形狀改為：extension 是產品本體且永遠免費，後端是可替換的媒合引擎，同一份程式碼支撐「使用者自部署」與「代管雲端」兩種部署。

## 2. 決策摘要

| 項目 | 決策 |
|---|---|
| 產品本體 | Chrome extension：唯一 UI ＋ 受保護平台的唯一採集器；不做取代它的 Web UI |
| 後端形態 | 同一份開源程式碼，兩種部署：使用者自部署（免費）與代管雲端（付費） |
| extension 與後端的關係 | 後端無關：extension 只認 endpoint ＋ auth，兩種部署共用同一顆已上架的 extension |
| 來源型態 | 依平台合規性逐一評估：搜尋路徑開放者可做背景批次抓取（`auto`），其餘走 extension 半被動（`semi_passive`）；半被動仍是新增來源的預設 |
| 來源開關 | 雙層：部署層決定該來源在此部署是否開放，使用者層決定要不要啟用；同一份設定 UI 於自部署與雲端共用 |
| 雲端成本控管 | 用量計量（credit）控管 LLM 支出；帳號、計費與計量實作不在本 repo |
| 授權 | AGPL-3.0 |
| 貢獻政策 | 不接受外部 PR；issue 開放 feature request 與 bug report，不保證處理 |
| repo 切分 | 現階段單一公開 repo；雲端層（帳號／計費／多租戶／資料服務）於另一私有 repo，待實際開工時再切出核心介面 |

## 3. 三種部署形態

| | 自部署（免費） | 代管雲端（付費） |
|---|---|---|
| 後端落點 | 使用者自己的機器：release binary 或容器 | 代管服務 |
| extension endpoint | `http://127.0.0.1:<port>` | `https://<代管網域>` |
| auth | 安裝時產生的隨機 Bearer token，使用者於 Options 貼上 | Google OAuth → session token |
| LLM | headless CLI Runner（訂閱額度）或使用者自帶 API Key | 平台代管，依用量計量 |
| 抓取責任歸屬 | 使用者自行啟用，責任在使用者 | 服務提供者代抓，責任在服務提供者 |
| 資料庫 | SQLite | PostgreSQL（多租戶） |

自部署的預設是後端跑在使用者本機、extension 直連 loopback，不需要任何通道設定。`docs/guides/runbook-extension.md` 的 SSH／IAP 常駐通道是「後端在遠端 VM」這個特定環境的作法，不是產品的預設路徑。

## 4. 來源能力矩陣與雙層開關

來源的可用性由三個維度決定，取代目前「一份 adapter 清單」的隱含模型：

| 維度 | 值 | 決定者 | 存放 |
|---|---|---|---|
| `mode` | `auto`（背景批次抓取）／`semi_passive`（extension 擷取） | 平台性質 | 程式碼內建 |
| 部署層可用性 | 此部署是否開放該來源 | 部署者（自部署＝使用者本人；雲端＝服務提供者） | 設定檔 |
| 使用者啟用 | 使用者要不要啟用該來源 | 使用者 | store `settings` 表，UI 開關 |

中間那層是法律責任的分界線：同一份程式碼，自部署版由使用者自行抓取，雲端版由服務提供者代抓。因此「此來源在雲端是否開放集中抓取」必須是**部署層**而非使用者層的旗標——雲端版把未開放的來源顯示為此部署未提供且不可開啟，自部署版則全部可開。設定 UI 因此能在兩種部署共用同一份實作。

目前的來源狀態：Yourator 為 `auto`（搜尋路徑本身開放）；104 與 Cake 為 `semi_passive`。新增平台先評估其搜尋路徑是否開放，開放才進 `auto`，否則走半被動；不繞過任何防護。

## 5. extension 的後端無關化

| 縫 | 現況 | 目標 |
|---|---|---|
| endpoint | Options 設定 endpoint，`host_permissions` 靜態列出 loopback | 保留設定，改用 `optional_host_permissions`，使用者填入自己的後端網域時於 runtime 請求授權——自部署的網域無法事先列舉 |
| auth | 單一 Bearer token | 連線模式二選一：自部署 token 或雲端帳號（OAuth session） |
| extension 識別 | manifest 已釘 `key`，ID 穩定；安裝時仍須人工把 `api.extension_origin` 佔位換成實際 ID | 上架後 ID 為已知常數，`extension_origin` 內建預設值，自部署使用者只需貼 token |
| capture 路徑 | `POST /api/v1/capture/*` 至 loopback | 契約不變，僅 endpoint 不同；雲端版沿用同一組 API |

廣域 `optional_host_permissions` 於 Chrome Web Store 審查時須說明用途（連線至使用者自行部署的後端），此為上架說明的必要內容。

## 6. Zero-PII 邊界

雲端版新增帳號層，媒合層的 Zero-PII 政策不變：

| 層 | 內容 | 政策 |
|---|---|---|
| 帳號層 | 登入身分、通知信箱、計費資訊 | 必要 PII，僅此層持有，與媒合資料實體分離 |
| 媒合層 | Profile、JD、評分、求職信、狀態事件 | 維持 Zero-PII：`UpsertJob` 於入庫前遮罩 JD 夾帶的 Email／電話，`SaveAgentCall` 同樣遮罩後才寫入，因此雲端版的媒合資料與送往 LLM 的 prompt 一律不帶聯絡資訊 |

帳號層不在本 repo。

## 7. 對既有 roadmap 假設的差異

| 主題 | 舊 | 新 |
|---|---|---|
| extension 的角色 | 自用階段的過渡 UI，S2 由 Web UI 取代 | 產品本體，永遠存在且免費 |
| S2 的主要工作 | 建 Web UI 並遷移 Profile 表單 | 後端可代管化：extension 後端無關化、LLM 直串 API、帳號層 |
| 使用者取得方式 | 單一代管服務 | 自部署（免費）或代管雲端（付費）二選一 |
| 抓取供給 | S3 才決策合規路線 | 依平台逐一評估：合規可抓者做背景批次，其餘半被動；部署層旗標分離責任歸屬 |
| 部署工件 | 開發 checkout 直接 build＋install | CI 產出帶版號與 checksum 的 release 工件，安裝流程只從該工件部署 |
| 授權與貢獻 | 未定 | AGPL-3.0；不收外部 PR |

## 8. 落點

| canonical 文件 | 更新內容 |
|---|---|
| `docs/roadmap.md` | 重寫階段規劃與目標架構為 extension-first；商業章節移出本 repo |
| `docs/deploy.md` | §7 產品化雛型改寫；新增 release 工件與 CI 發佈流程章節 |
| `AGENTS.md` | §1 專案定位由「單人單機工具」擴為「自部署／代管雙形態的開源產品」 |
| `README.md` | 新增：對外定位、快速開始、授權與貢獻政策 |

## 9. 待實作

| 項目 | 依賴 |
|---|---|
| LLM 直串 API 與 Runner 雙實作 | 雲端版的硬前置；多用戶下 CLI 訂閱模式不成立 |
| 來源能力矩陣與雙層開關（含設定 UI） | 無 |
| extension 連線模式與 `optional_host_permissions` | 無 |
| Chrome Web Store 上架 | 隱私政策頁、上架說明；上架後 `extension_origin` 才能內建 |
| 帳號層與用量計量 | 私有 repo；依賴 LLM 直串 API |

## 10. 已知殘留限制

- 核心模組目前全在 `internal/` 下，外部 module 無法 import。雲端層要以獨立 repo 依賴本核心時，必須先將所需模組提升至可匯出路徑；此重構刻意延後到雲端層實際開工，以免現在就把模組邊界凍結成對外 API。
- AGPL-3.0 擋的是「取用本程式碼提供對外服務而不公開修改」，不限制任何人自行部署自用——後者本就是免費版的預期用法。
