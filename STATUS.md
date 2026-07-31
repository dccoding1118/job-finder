# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-31。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- 實機 Chrome 人工 gate 的結果不回寫 `docs/verify.md`（不附截圖或證據檔）：每次改動皆由使用者實際部署後在 Windows Chrome 操作驗證，驗收結論當場即知，回寫只是重複記錄。verify 的 ⏳ 僅代表自動化 harness 步驟尚未實作。

## §2 未完成任務

- [ ] 補上 token 用量追蹤的 canonical 文件：schema v7（`agent_calls` 的 `model` 與六個用量欄位、`agent_calls_runner_model_created_idx`）尚未寫進 `docs/designs/design-schema.md`，`GET /api/v1/status` 的 `agent_usage_daily`／`filter_budget` 未寫進 `docs/designs/design-api.md`，系統頁每日用量呈現未寫進 `docs/designs/design-extension.md`，對應測試規格（`docs/tests/`）與驗收案例（`docs/verify.md`）亦缺。
- [ ] 清掉 `pipeline.Filter.Match`：清單標記改由 `Evaluate` ＋ `failedTexts` 決定後，`Match` 只剩 `pipeline_test.go` 呼叫，註解描述的用途也已不成立。
- [ ] 決定「只陳述國別的地點」怎麼判：Cake 有些職缺地點只寫「台灣」，而「Taiwan／台灣」只是 `nationwide` 的別名，使用者若選的是縣市鍵就會判 `fail` → 不適合。目前維持原判準未動；要改的話應視為未決（`unknown`）而非不符。
- [ ] 實機 Chrome 人工 gate：Profile editor 六區段新表單（含地區下拉、技能「從經歷載入」、資格三清單一項一列）、六類 verdict 與篩選逐條結論的 Side Panel 呈現、待看清單補全文、系統頁每次呼叫的 token／費用與每日用量卡片排版。
- [ ] 上版後首次啟動：雙 revision 的 schema 標記已升至 v5，既有職缺全部視為過時，需在系統頁手動 reprocess 一次。本機 `profile.yaml` 的地區值（台北／臺北／新北／台中／Taipei）皆在地區列舉內，載入時自動併入 `requirements.locations` 並正規化為地區鍵，無需手改。
- [ ] 實機部署測試：清空既有 JD 資料後重新建立，先以 `worker.paused: true` 只收集不判定，確認 Profile 欄位與內容、104／Cake 擷取正常後，改回 `paused: false` 並以 `run --stage filter/score` 批次消化（步驟見 `docs/deploy.md` §6）。
- [ ] 補上 V6 的驗收 harness 步驟 S40–S45 與 S41b（Cake capture 測資見 `docs/verify.md` §3.3.1）。

**Roadmap（暫不實作，規劃見 `docs/roadmap.md`）**

- [ ] S1：反向校準閉環（前端入口，不做 CLI 指令）、每日高分職缺推送、成效統計、深入評估、履歷匯入產生 Profile 草稿、多 Profile。
- [ ] `requirements` 增設語意硬排除欄位（如 `exclude_conditions[]`，自然語言、併入 Filter Agent 該次呼叫逐條判定，JD 未提及回 `unknown`）：「不接受海外出差／外派」「不接受輪班」這類需求既不是產業也難以用關鍵字精準命中，現有六個 requirements 欄位皆無法承接，目前只能填 `intents.content_dislikes` 走軟性扣分。
- [ ] 產品化部署架構：以 chrome extension 為主的 AI 輔助求職媒合產品，規劃用戶適合的後端部署架構，與可能的收費模式(ex: 開源、自部署後端免費，提供雲端部署收費版本，但似乎與 chrome extension 本身的付費版無關，可能是另外的 credit 模式)。
- [ ] 產品化上版/釋出流程：部署改走 CI/release 正式版工件、主程式搬至 `~/.local/bin/jobfinder`、API 請求層 log、Agent「輸出已過契約驗證但 Invoke 回錯」不重跑的重試契約。
- [ ] 產品化 LLM 配置：此專案對 LLM 的使用情境，LLM 其實沒有用到工具的部分，所以最適合的是使用 LLM 而不是 Agent，須針對篩選、評分與求職信生成/審核增加 LLM API 機制，並將原每日篩選、評分硬上限改成軟上限，當處理量到達指定門檻，觸發告警顯示今日以篩選/評分 XX 筆職缺，但還可以繼續處理。提供 LLM API、agent binary 整合配置，設定使用某個 agent (用戶須自己配置好環境 path，使用訂閱額度) 或 LLM API / API Key。
- [ ] 產品化監控功能：在 extension UI 增加集中式、更詳細的運作流程日誌與監控細節，分別查看 UI/API 運作、worker 批次、fetch 批次、求職信處理的日誌。fetch 批次歷程中，那些英文改成中文說明呈現。
- [ ] 產品化設定功能：將配置檔內功能提供到 extension 上配置，包含暫停自動篩選、評分功能(停止 token 消耗，對應處理量告警)。
