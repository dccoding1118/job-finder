# 變更 — B0–B4 live E2E

## 背景與動機

現行 `e2e-live` 先執行 mock，沿用 loopback source、fake Agent、mock SQLite 與固定合成斷言，因此無法證明真 Yourator、真 CLI Runner 或 live 資料流程。Agent model 也未進入設定契約，CLI 可能使用未明示的預設模型。

## 決策摘要

- mock 與 live 共用同一份物化 product artifact；只分開 config、SQLite 與外部依賴。
- 每個 Profile direction 的 keywords 組成一次來源搜尋；每個來源每輪最多三個 direction queries，所有 query 與頁面結果依 `(source, external_id)` 合併去重。
- mock 可使用合成 JD、loopback fixture 與固定 Agent result；live 不得讀取或執行 mock harness。
- 每個 Agent role 的 primary／fallback 各自指定 agent CLI 與 model；現行配置中 Claude endpoint 使用 `claude-sonnet-5`，Codex endpoint 使用 `gpt-5.6-terra`，model 在啟動 headless CLI 時明確傳入。
- 開發中的 dirty 或未提交工作樹可執行驗收；artifact checksum 是主要追溯依據，Git metadata 僅為輔助資訊。
- `ENVIRONMENT_BLOCKED` 只表示正式來源、CLI 授權、systemd user bus 或 browser runtime 不可用；零筆、格式錯誤、mock dependency 滲入或產品斷言失敗皆為 `FAIL`。

## 相對既有狀態的差異

| 面向 | 既有狀態 | 目標狀態 |
|---|---|---|
| artifact | mock deploy 同時物化 product 與 fake executable | product artifact 共用；fake executable 位於獨立 mock harness |
| runtime | 單一 config／SQLite | `config-mock.yaml`／`config-live.yaml` 與 `mock.db`／`live.db` |
| live 入口 | 先跑 mock，再以同一狀態跑四個 stage | 直接從 reset/deploy 後的 live config 與空 live DB 執行 |
| 搜尋 | 三個方向 keywords 混入同一 request | 每個 direction 一個 request，最多三組，跨組與跨頁去重 |
| Agent | runner 有路由、無 model | 每個 role 的 primary／fallback endpoint 同時定義 agent 與 model，CLI argv 明確帶入 |
| evidence | live 只檢查 stage summary | 驗證來源格式、Agent 上限、冪等、systemd、API、extension 模擬與安全摘要 |

## 落點

- `docs/designs/design-crawler.md`、`docs/tests/test-crawler.md`
- `docs/designs/design-agents.md`、`docs/tests/test-agents.md`
- `docs/designs/design-pipeline.md`、`docs/design.md`
- `docs/verify.md`
- `configs/`、`internal/crawler/`、`internal/agents/`、`cmd/jobfinder/cli/`
- `scripts/verify/`

## 實作範圍

- [x] canonical 文件同步。
- [x] direction query、合規抓取與 crawler L1。
- [x] strict config、model-aware Runner、timeout／乾淨 cwd 與 Agent L1。
- [x] 共用 artifact 與 mock/live runtime/harness。
- [x] V3 live assertions、systemd/API/extension 模擬與 evidence。
- [x] 無外部成本驗證。

## 已知殘留限制

- 真 Yourator 與真 CLI Runner 僅由 opt-in `e2e-live` 呼叫，不進一般測試或 mock E2E。
- 實際 Chrome 安裝、權限、origin 與相容性仍是人工 gate；隔離 Chromium 只屬自動模擬。
