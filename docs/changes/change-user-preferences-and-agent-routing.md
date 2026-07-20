# 求職條件與 Agent 路由設定

## 背景與動機

使用者調整求職條件或 Agent 可用性時，應能修改本機檔案並於下一輪執行生效，不應重新建置或部署。使用者介面與使用者文件採用求職條件、條件篩選、AI 評分與求職信審查等業務用語，不顯示內部流程分層名稱。

## 決策摘要

| 主題 | 最新狀態 |
|---|---|
| 求職條件 | `profile.yaml` 是薪資、地點、遠端、方向、產業避開項目與條件篩選規則的單一真相。 |
| 執行設定 | `config.yaml` 保存資料路徑、來源啟用、評分門檻、單輪上限、Agent 路由與 API 連線設定。 |
| Agent 路由 | `llm.roles` 為評分、信件起草與信件審查的 primary／fallback 分別指定 agent CLI 與 model。下一輪 `jobfinder run` 讀取最新設定。 |
| 使用者用語 | UI、API 顯示名稱與驗收案例使用「條件篩選」、「AI 評分」、「信件起草」、「信件審查」；內部程式狀態與欄位名稱維持既有契約。 |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [PRD](../PRD.md) | 求職條件、條件篩選與可調整 Agent 路由的需求與流程。 |
| [系統設計](../design.md) | Profile 與 config 的所有權和載入時機。 |
| [Profile 設計](../designs/design-profile.md) | 求職條件與條件篩選欄位。 |
| [Pipeline 設計](../designs/design-pipeline.md) | 從 Profile 導出條件篩選規則。 |
| [Agents 設計](../designs/design-agents.md) | `llm.roles` 契約與 fallback 行為。 |
| [測試與驗證](../tests/test-profile.md) | Profile 條件欄位的驗證案例。 |

## 待實作進度

| 項目 | 完成條件 |
|---|---|
| Profile 條件篩選 | 載入並驗證 `preferences.screening`，pipeline 僅使用 Profile 導出的篩選條件。 |
| Agent 路由 | CLI 載入、驗證並套用 `llm.roles`；各角色依 endpoint 的 agent/model 重試與 fallback，且稽核記錄實際 runner。 |
| 介面與驗收 | Profile 或日後的 Options 編輯求職條件後，下一輪執行採用新值；驗收顯示業務用語。 |
