# 原生 Side Panel 與視覺系統

## 背景／動機

Chrome extension 的日常操作面需要在瀏覽職缺時持續可見，並以窄幅單欄呈現判定、五維評分、待看清單、推薦職缺與系統狀態。`ui-design/` 定義的視覺系統與互動模型為正式前端的依循體。

## 決策摘要

- Chrome 原生 Side Panel 是 extension 的唯一主要操作面；toolbar action 開啟 Side Panel，不使用 popup。
- 104 content script 只負責使用者已載入頁面的擷取與列表就地標記。職缺內頁不再注入完整評分 overlay；Side Panel 透過 service worker 取得目前分頁的擷取結果。
- Side Panel 固定提供「目前職缺、待看、推薦、系統」四個頁籤，並保留既有篩選、求職信、投遞狀態、手動抓取與 Run history 能力。
- 視覺使用 `ui-design/DESIGN.md` 的 light／dark token。主題選擇持久化，但不儲存 JD、求職信或 API token 的副本。
- API 離線時保留當次頁面記憶體中的內容並停用寫入操作；重新開啟 extension 不跨 session 快取 JD 或求職信。

## 相對舊狀態的差異

| 主題 | 舊狀態 | 最新狀態 |
|---|---|---|
| Chrome 入口 | toolbar popup | toolbar action 開啟原生 Side Panel |
| 104 內頁判定 | 頁面內 Shadow DOM sidebar | Side Panel「目前職缺」 |
| 主要資訊架構 | 單頁 dashboard 區塊 | 目前職缺／待看／推薦／系統四頁籤 |
| 樣式 | 無正式 stylesheet | light／dark token、窄幅單欄、sticky header/action dock |
| 目前分頁 | dashboard 不感知 | service worker 向 active tab content script 查詢擷取 context |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [PRD](../PRD.md) | R6、R9 的 Side Panel 操作模型與 104 內頁呈現 |
| [系統設計](../design.md) | extension 模組名稱、邊界與 verdict 呈現面 |
| [extension 設計](../designs/design-extension.md) | Side Panel 元件、目前分頁 context、主題與失敗處理 |
| [extension 測試](../tests/test-extension.md) | Side Panel、主題、目前分頁與既有日常操作案例 |
| [驗證](../verify.md) | browser E2E 與 Chrome 人工 gate 的入口與觀察面 |
| [操作手冊](../guides/runbook-extension.md) | 原生 Side Panel 的開啟與驗收步驟 |

## 交付狀態

| 項目 | 狀態 | 完成條件 |
|---|---|---|
| 原生 Side Panel manifest 與 action 行為 | 已完成 | toolbar action 開啟 `dashboard/index.html` Side Panel |
| 正式資料綁定 | 已完成 | 四頁籤可使用現有 Job／Queue／Run API，職缺內頁 context 可顯示於目前職缺 |
| light／dark 主題 | 已完成 | 切換後維持目前頁籤與職缺，重新開啟仍保留選擇 |
| 自動化驗收 | 已完成 | Playwright 6 tests 與 mock E2E 25 步通過 |
| Chrome 實機 gate | 已完成 | 依 `docs/guides/runbook-extension.md` 使用同一 artifact 完成人工核對 |

## 已知殘留限制

- API 尚未提供 worker health 與排程時間的查詢契約；系統頁只呈現可由現有 API 證明的連線狀態、手動抓取與 Run history，不臆測 worker 或 timer 狀態。
- 離線內容只保留於當次 Side Panel DOM 記憶體，不寫入 extension storage。
