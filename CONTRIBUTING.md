# 貢獻說明

先講結論：**本專案不接受外部 Pull Request。**

程式碼公開的目的是讓你能夠檢視、自行部署與自行修改（AGPL-3.0 保障這些權利），不是徵求共同維護。開發方向由維護者單獨決定，外部 PR 一律會被關閉，恕不逐一討論。

未來是否開放共建再議；在本檔改口之前，請以此為準。

## 你可以做什麼

| 你想做的事 | 怎麼做 |
|---|---|
| 回報 bug | 開 issue（Bug 回報模板）。附上重現步驟、預期與實際行為、`journalctl --user -u jobfinder-api.service` 的相關片段 |
| 建議功能 | 開 issue（功能建議模板）。說明你的使用情境與現在被什麼卡住 |
| 自己改來用 | Fork 隨意改，不必徵詢。AGPL 只在你「拿改造版對外提供網路服務」時要求你公開修改 |
| 問使用問題 | 開 issue 前先看 [README](README.md)、[docs/deploy.md](docs/deploy.md) 與 [docs/guides/](docs/guides/) |

**issue 不保證回覆或處理。** 這是個人專案，維護時間有限；沒有回應不代表建議不好。

## 回報時請勿附上個人資料

本專案的核心原則之一是 Zero-PII。回報問題時請把姓名、Email、電話、學校與公司名從 log、Profile 片段與截圖中移除——包括你自己的與招募方的。含個資的 issue 會被直接關閉。

## 安全性問題

不要開公開 issue，見 [SECURITY.md](SECURITY.md)。
