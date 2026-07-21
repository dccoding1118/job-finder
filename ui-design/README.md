# jobfinder UI design

本目錄是 jobfinder Chrome extension 的 UI 設計交付物。設計以 Chrome 原生 Side Panel 為主要操作面，求職網站頁面只保留職缺擷取與清單就地標記。畫面使用合成資料，不連接 localhost API，也不讀取正式 extension storage。

## 預覽

直接以瀏覽器開啟 `side-panel.html` 即可操作。若瀏覽器限制本機檔案行為，可在專案根目錄啟動任意靜態檔案伺服器後開啟 `/ui-design/side-panel.html`。

Header 的月亮／太陽按鈕可切換淺色與深色主題；也可在網址加上 `?theme=dark` 直接開啟深色版本。

建議以可調整寬度的窄視窗預覽；設計需在 320px 以上維持可用，不假設 Side Panel 固定寬度或固定顯示於瀏覽器右側。

## 檔案分工

| 檔案 | 用途 |
|---|---|
| `DESIGN.md` | 視覺系統的權威來源；格式對齊 Google Labs DESIGN.md 規格，可供設計工具與 coding agent 使用 |
| `SCREENS.md` | Side Panel 的畫面、狀態、互動與內容優先序 |
| `side-panel.html` | 工具中立的靜態 prototype 入口 |
| `side-panel.css` | 依 `DESIGN.md` 實作的視覺樣式 |
| `side-panel.js` | 頁籤、狀態切換與 prototype 回饋 |
| `mock-data.js` | Zero-PII 合成職缺、分數、信件與執行資料 |
| `side-panel.png` | 目前職缺畫面的審核參考圖 |
| `side-panel-dark.png` | 深色主題目前職缺畫面的審核參考圖 |
| `side-panel-states.png` | 系統與 prototype 狀態畫面的審核參考圖 |

## 設計工具交付

外部工具不是權威來源。需要在 Google Stitch、Figma 或 Penpot 繼續探索時，使用 `DESIGN.md` 提供視覺規則，以 `side-panel.png` 提供構圖參考，並以 `SCREENS.md` 補充狀態與互動。選定結果須回填本目錄的設計規則、參考圖與 prototype。

## 邊界

- UI 使用 jobfinder 自有視覺，不引用 104 或其他求職網站的品牌色與元件。
- `source` 只作為資料來源標籤，不能影響主要元件樣式。
- 淺色與深色主題使用相同資訊架構、狀態語意與互動；主題切換不得改變內容優先序。
- 判定、處理狀態與投遞狀態不得只靠顏色表達。
- Side Panel 不直接解析求職網站 DOM；content script 正規化資料後才交給 extension。
- Prototype 不包含真實姓名、聯絡方式、公司名稱、JD 或 API token。
