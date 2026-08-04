# 安全性政策

## 回報漏洞

**請勿開公開 issue。** 使用 GitHub 的私密回報：本 repo 的 **Security → Report a vulnerability**。

沒有 SLA——這是個人專案。收到後會盡快確認，修正時程視嚴重度而定。修正後會在 release notes 說明，並在你同意時具名致謝。

## 支援範圍

只維護最新 release。舊版本不回頭修補。

## 威脅模型

本專案的預設部署是**單機自部署**：API 只綁 loopback、以隨機 Bearer token 驗證，並限定 extension origin。以下屬於使用者的部署責任，不視為本專案的漏洞：

- 自行把服務綁到 `0.0.0.0` 或公開到網際網路後遭到存取。
- `~/.config/jobfinder/config.yaml`（內含 API token）的檔案權限被放寬。
- 使用者自行提供的 LLM CLI／API 金鑰外洩。

以下屬於本專案的責任，歡迎回報：

- 繞過 token 或 extension origin 驗證的路徑。
- **PII 洩漏**：任何使姓名、Email、電話、校名或公司名進入資料庫、log、Agent prompt 或求職信的路徑，包括入庫遮罩的繞過。這是本專案最重視的一類問題。
- extension 的 content script 把不該送的資料送上後端，或對第三方網站曝露能力。
- SQL injection、路徑穿越等一般性實作漏洞。
