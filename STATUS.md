# STATUS — job-finder（MVP 開發）

> 最後更新：2026-08-03。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

（無）

## §2 未完成任務

- [ ] `agent_calls` 的 PII 守衛會讓已付費的呼叫整筆作廢：`SaveAgentCall` 在 prompt 或回應命中 email／09 開頭手機號時回 `store: invalid agent call`，該錯誤向上冒泡成整筆篩選失敗（實機 job_id=21 已發生），LLM 已經呼叫過、結果卻被丟棄，且該筆留在 `new` 每輪重試、每輪重付。決定作法：稽核寫入前遮蔽 PII 再存（保留用量與判定），而非讓守衛否決整筆工作。
- [ ] PR #19 的抓取層修正（JD 擷取丟棄 `script`／`style` 內容）之後尚未重跑完整 `mise run e2e-live`：step 01–10 與新的 step 07 指紋斷言已分別驗過，但 step 11–13（真 MV3 extension 唯讀連線、mock/live 邊界、安全 evidence）在修正後未實跑。等有真實 Agent 額度時跑一次確認。
- [ ] `docs/verify.md` §4 的 S11、S12、S18、S32 標為 ⏳，但三支 mock runbook 實際都會跑且通過；其標準答案的字面值也與 harness 現行斷言不符（如 S11 寫 Score=3／Agent calls=15，實際為 4／16）。需逐條校對後把標準答案改成實際契約、狀態改為 ✅。

**Roadmap（暫不實作，規劃見 `docs/roadmap.md`）**

- [ ] S1：反向校準閉環（前端入口，不做 CLI 指令）、每日高分職缺推送、成效統計、深入評估、履歷匯入產生 Profile 草稿、多 Profile。
- [ ] `requirements` 增設語意硬排除欄位（如 `exclude_conditions[]`，自然語言、併入 Filter Agent 該次呼叫逐條判定，JD 未提及回 `unknown`）：「不接受海外出差／外派」「不接受輪班」這類需求既不是產業也難以用關鍵字精準命中，現有六個 requirements 欄位皆無法承接，目前只能填 `intents.content_dislikes` 走軟性扣分。
- [ ] 產品化部署架構：以 chrome extension 為主的 AI 輔助求職媒合產品，規劃用戶適合的後端部署架構，與可能的收費模式(ex: 開源、自部署後端免費，提供雲端部署收費版本，但似乎與 chrome extension 本身的付費版無關，可能是另外的 credit 模式)。
- [ ] 產品化上版/釋出流程：部署改走 CI/release 正式版工件、主程式搬至 `~/.local/bin/jobfinder`、API 請求層 log、Agent「輸出已過契約驗證但 Invoke 回錯」不重跑的重試契約。
- [ ] 產品化 LLM 配置：此專案對 LLM 的使用情境，LLM 其實沒有用到工具的部分，所以最適合的是使用 LLM 而不是 Agent，須針對篩選、評分與求職信生成/審核增加 LLM API 機制，並將原每日篩選、評分硬上限改成軟上限，當處理量到達指定門檻，觸發告警顯示今日以篩選/評分 XX 筆職缺，但還可以繼續處理。提供 LLM API、agent binary 整合配置，設定使用某個 agent (用戶須自己配置好環境 path，使用訂閱額度) 或 LLM API / API Key。
- [ ] 產品化監控功能：在 extension UI 增加集中式、更詳細的運作流程日誌與監控細節，分別查看 UI/API 運作、worker 批次、fetch 批次、求職信處理的日誌。fetch 批次歷程中，那些英文改成中文說明呈現。
- [ ] 產品化設定功能：將配置檔內其餘功能提供到 extension 上配置（每日上限、掃描間隔、去重門檻、LLM 路由等；自動篩選與評分開關已落地）。處理量告警待「產品化 LLM 配置」的軟上限一併處理。
