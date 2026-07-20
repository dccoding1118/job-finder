# Chrome extension 前端與 localhost API

## 背景與動機

MVP 的日常求職操作與 104 瀏覽流程統一在 Chrome extension。Go 服務維持本機資料、pipeline 與 API 邊界，不承擔 HTML 渲染。

## 決策摘要

- extension page 是職缺清單、對照、複製、投遞狀態、手動 run、Run 歷史與待看清單的唯一入口。
- `jobfinder serve` 是只監聽 loopback 的 JSON API。所有 API request 由 extension service worker 發出，使用 token 與精確 extension origin 驗證。
- 104 content script／sidebar 只處理使用者已載入頁面，經 API capture endpoint 進入既有 crawler、pipeline 與 store 契約。
- 交付順序為 B4 API＋extension page＋timer、B5 104 半被動擷取、B6 Cake adapter＋Profile 校準。

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [PRD](../PRD.md) | R6 extension page 儀表板，B4–B6 分批與流程 |
| [系統設計](../design.md) | API、extension 模組邊界與依賴方向 |
| [API 設計](../designs/design-api.md) | API 路由、認證、CORS、資料模型與測試 |
| [extension 設計](../designs/design-extension.md) | extension page、Options、service worker、104 script 與 sidebar |
| [驗證](../verify.md) | B4 API／extension page 與 B5 Chrome E2E gate |
| [部署](../deploy.md) | API service、timer 與 SSH local forward |

## 待實作進度

| 項目 | 批次 | 完成條件 |
|---|---|---|
| API、extension page 與 timer | B4 | API 與 extension 自動化案例、systemd lifecycle 驗收完成 |
| 104 半被動擷取 | B5 | 104 spike 回填，Chrome E2E gate 通過 |
| Cake 與反向校準 | B6 | Cake adapter 與 Profile 建議 diff 驗收完成 |
