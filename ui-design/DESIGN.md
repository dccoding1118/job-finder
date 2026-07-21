---
version: "alpha"
name: "jobfinder"
description: "A calm, decisive side-panel workspace for reviewing job opportunities while browsing supported job sites."
colors:
  canvas: "#F4F5F2"
  surface: "#FFFFFF"
  surface-muted: "#ECEFEA"
  ink: "#17223B"
  ink-muted: "#687083"
  border: "#DDE1DA"
  primary: "#2756D8"
  primary-hover: "#1E46B8"
  primary-soft: "#E8EEFF"
  positive: "#147A63"
  positive-soft: "#E1F3EC"
  negative: "#BA3F4B"
  negative-soft: "#FBE8EA"
  warning: "#A76308"
  warning-soft: "#FFF0D5"
  neutral-soft: "#E9ECF0"
  dark-canvas: "#101521"
  dark-surface: "#171E2C"
  dark-surface-muted: "#222B3B"
  dark-ink: "#F3F6FA"
  dark-ink-muted: "#9BA7BB"
  dark-border: "#2B3547"
  dark-primary: "#6C8FFF"
  dark-primary-soft: "#202E55"
  dark-positive: "#5ED0AF"
  dark-positive-soft: "#163B34"
  dark-negative: "#FF8994"
  dark-negative-soft: "#47232D"
  dark-warning: "#F5BC66"
  dark-warning-soft: "#44321A"
typography:
  body:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.55
    letterSpacing: "0em"
  label:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "12px"
    fontWeight: 650
    lineHeight: 1.4
    letterSpacing: "0.01em"
  title:
    fontFamily: "ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "20px"
    fontWeight: 750
    lineHeight: 1.25
    letterSpacing: "-0.02em"
rounded:
  sm: "8px"
  md: "12px"
  lg: "18px"
  full: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  xxl: "32px"
components:
  primary-button:
    height: "42px"
    background: "{colors.primary}"
    foreground: "{colors.surface}"
    radius: "{rounded.md}"
  card:
    background: "{colors.surface}"
    border: "{colors.border}"
    radius: "{rounded.lg}"
  status-chip:
    radius: "{rounded.full}"
    font: "{typography.label}"
---

# jobfinder visual system

## Overview

jobfinder 是一個安靜但有判斷力的求職決策工作台。介面應讓使用者在瀏覽職缺時快速得到可信判定、理解理由並採取下一步，而不是模仿求職平台或呈現大量行銷內容。

整體感受為專業、克制、清醒與可靠。資訊密度適中，重要結論明顯，細節按需展開。視覺以溫暖中性色降低長時間閱讀負擔，以深墨藍建立信任，以單一鈷藍承載主要操作。來源平台只能是次要標籤。

Side Panel 是窄幅、可調整寬度的工作面。任何畫面都須先在 320px 寬度成立，再利用較寬空間改善排版。不得依賴固定右側位置，因瀏覽器可由使用者決定 Side Panel 位於左側或右側。

## Colors

| 角色 | Token | 用途 |
|---|---|---|
| 畫布 | `canvas` | Panel 主背景，降低白色眩光 |
| 表面 | `surface` | 卡片、選單、浮層 |
| 主要文字 | `ink` | 標題、分數、主要內容 |
| 次要文字 | `ink-muted` | 輔助資訊、時間、說明 |
| 主操作 | `primary` | 主要 CTA、選中頁籤、焦點 |
| 推薦 | `positive` | 推薦、完成、連線正常 |
| 排除 | `negative` | 不適合、錯誤、危險結果 |
| 等待 | `warning` | 待補全文、待評分、處理中 |

狀態色必須搭配文字與圖示。柔和背景色只用於 badge、callout 與小範圍狀態提示，不以整張卡片的大面積飽和色表達判定。

深色主題使用帶藍的近黑畫布與深墨藍表面，不使用純黑 `#000000`。主要文字為低眩光的冷白色，狀態色提高明度而降低面積。深色與淺色主題維持相同語意映射：藍色仍代表主要操作，綠色仍代表推薦與正常，紅色仍代表排除與錯誤，琥珀色仍代表等待與額度限制。

## Typography

使用作業系統內建 sans-serif 字型，不載入遠端字型。品牌、標題與資料均使用同一家族，靠字重、尺寸與留白建立層級。

| 層級 | 建議 |
|---|---|
| 品牌 | 16px、750，緊縮字距 |
| 畫面標題 | 20px、750 |
| 卡片標題 | 15–16px、700 |
| 內文 | 14px、400，行高 1.55 |
| 標籤 | 12px、650 |
| 輔助資料 | 12–13px、400–550 |
| 總分 | 30–34px、780，使用 tabular number |

長 JD 與求職信維持可選取的純文字，不使用全大寫標題。中文標點與英文識別字之間保留自然間距。

## Layout

- 基礎空間單位為 4px；常用間距為 8、12、16、24px。
- Panel 內容水平留白為 14–16px；主要內容下方保留 sticky action bar 所需空間。
- 頂部品牌列、頁面脈絡與頁籤形成一個 sticky header。
- Header 提供主題切換；使用者切換主題時不得清除目前頁籤、職缺或操作狀態。
- 畫面以單欄為主；只有短小的統計與動作可並排。
- 卡片內部不以多層邊框分割，優先使用留白、淡色區塊與細分隔線。
- 觸控目標高度至少 40px；圖示按鈕至少 36×36px。
- 清單列的主要文字最多兩行，狀態與總分固定在視覺尾端，避免不同來源造成版面跳動。

## Elevation & Depth

Panel 內只使用兩層深度：畫布與卡片。卡片以 1px 淺邊框為主，陰影極淡。Toast 與臨時浮層可使用較明顯陰影，但不得讓介面呈現多層漂浮卡片。

Side Panel 本身由瀏覽器提供邊界，不在正式 extension 內額外模擬裝置框或外部陰影。寬螢幕上的 prototype 可以使用外框協助審稿，但不屬於產品 UI。

## Shapes

- 主要卡片使用 18px 圓角。
- 按鈕與輸入元件使用 12px 圓角。
- Badge 與狀態 chip 使用 pill 形。
- 品牌標記以圓角方形與「掃描／定位」幾何構成，不使用公事包、放大鏡或特定求職網站標誌。
- 圖示採 1.8–2px 線條、圓角端點，視覺尺寸以 16、18、20px 為主。

## Components

### Header

品牌列包含 jobfinder 標記、產品名稱、連線狀態與重新整理。其下的 page context 顯示目前來源、職稱摘要與擷取狀態。來源標籤保持中性，不使用來源品牌色。

### Tabs

主要頁籤固定為「目前職缺、待看、推薦、系統」。選中狀態以文字、底線與色彩共同表達。數量使用小型 counter，不讓 counter 成為主要視覺。

### Verdict

判定由 icon、文字與柔和背景共同呈現。推薦顯示總分；不適合顯示命中條件；待評分顯示處理狀態；待補全文顯示需要使用者點開職缺頁。

### Score bars

五維評分使用水平 bar，不使用雷達圖。每列包含維度名稱、數字與 bar；bar 是輔助視覺，數字才是可讀真相。總分與理由置於 bar 之前。

### Job rows

清單列顯示職稱、匿名公司類型、來源、地點、總分或狀態。整列可點擊，hover 與 focus 狀態一致。來源變多時只增加 label，不改變元件結構。

### Buttons

同一畫面只有一個 primary action。次要動作用白底邊框按鈕；低優先動作用文字按鈕。非同步操作在送出後立即呈現處理中並停用重複送出。

### Status feedback

短暫成功訊息使用底部 toast；需要處置的錯誤使用畫面內 callout。API 離線時保留可瀏覽的快取內容，並清楚標記動作暫不可用。

## Do's and Don'ts

### Do

- 先呈現判定與理由，再呈現完整資料。
- 讓使用者在目前職缺、待看與推薦之間快速切換。
- 使用匿名、合成且不含 PII 的預覽內容。
- 在所有元件維持鍵盤焦點、可辨識名稱與足夠對比。
- 將來源平台視為 adapter context，而非 UI 品牌。
- 讓較長內容可以展開、選取與捲動。

### Don't

- 不使用 104、1111、Yourator 或其他來源的品牌色、Logo、字型與元件風格。
- 不將內部 `process_state` 識別字當作主要使用者文案。
- 不以單一顏色表達推薦、排除或等待。
- 不在窄 Side Panel 塞入桌面 dashboard 表格、雷達圖或多欄看板。
- 不使用遠端 CDN、外部字型或運行期載入的圖示庫。
- 不把深色主題做成單純反相；卡片、邊框、狀態柔色與 focus ring 都要有獨立深色 token。
- 不在 Side Panel 自動投遞、背景開啟求職頁或繞過網站防護。
