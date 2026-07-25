# 單筆重新評分與處理進度可觀測性

## 背景／動機

評分結果偶爾會因 JD 擷取不完整而錯誤，過去只能整批 reprocess，成本是每筆一次 Agent 呼叫。同時，處理過程只有 worker 的單行錯誤 log 與無對外出口的 `agent_calls` 稽核列，使用者無從判斷某筆為何遲遲沒有評分結果。

## 決策摘要

- 單筆重評是使用者明確動作：`scored` 與 `shortlisted` 可經 `POST /api/v1/jobs/{id}/rescore` 回到 `queued`，由常駐 worker 以 active revision 重評；成本為一次 Agent 呼叫。
- 已進入求職信階段的職缺不得重評，求職信與投遞歷史不因重評改寫；scores 維持 append-only，新分數寫入後才成為現行分數。
- 重評與整批 activation 重新處理並存，互不取代。
- pipeline 各階段以 `log/slog` 輸出結構化執行記錄至 journald，只記識別子、狀態與計量，不含 JD、Profile 或 Agent 原始輸出。
- `GET /api/v1/status` 是處理進度的唯一讀取面：各處理狀態筆數、當日評分預算餘額、最近 20 筆 Agent 呼叫。
- Agent 呼叫的 `ok` 只表示「runner 有回應且回應通過契約驗證」，與評分高低無關；未通過者由 agents 模組分類為 `failure_kind` 並附截斷回應，成功呼叫不附任何輸出。
- Scorer 的 `reason` 上限為 100 中文字：實測退回的理由平均 63 字、最長 92 字，50 字上限使格式正確的評分被整筆作廢並重試，白耗每日額度。
- Side Panel 在職缺詳情提供「重新評分」次要動作，在系統頁提供「處理進度」與「Agent 呼叫紀錄」；進度只在載入與重新整理時取得，不輪詢。

## 相對舊狀態的差異

| 主題 | 舊狀態 | 最新狀態 |
|---|---|---|
| 修正單筆錯誤評分 | 只能整批 reprocess 或不處理 | 單筆重評，成本一次 Agent 呼叫 |
| `scored` 狀態 | 處理軸終態 | 可由使用者要求的重評離開 |
| 執行記錄 | 僅 worker 單行錯誤 log | 各階段取件、開始、完成、失敗與丟棄皆有結構化記錄 |
| Agent 呼叫稽核 | 只有 `agent_calls` 資料列，無讀取面 | `GET /api/v1/status` 呈現最近 20 筆與分類後的失敗原因 |
| 待處理量 | 無查詢方式 | 各處理狀態筆數與評分預算餘額可查 |
| Scorer `reason` 上限 | 50 字，超過即整筆退回重試 | 100 字 |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [API 設計](../designs/design-api.md) | rescore 與 status route 契約、錯誤碼與安全邊界 |
| [Pipeline 設計](../designs/design-pipeline.md) | `RequestRescore` 入口與各階段 log 事件表 |
| [Schema 設計](../designs/design-schema.md) | `scored`／`shortlisted` → `queued` 轉換、`RequeueScore` 與進度查詢介面 |
| [Extension 設計](../designs/design-extension.md) | 重新評分動作與系統頁處理進度呈現 |
| [Agents 設計](../designs/design-agents.md) | 呼叫成功與失敗的判準、失敗分類 |
| [Schema／Pipeline／API／Extension／Agents 測試](../tests/) | ST-66–68、PT-77、AT-70–72、ET-38–39、agents AT-40–41 |
| [驗收](../verify.md) | V7 S38 與 S23 的 Playwright 鎖定案例數 |
| [開發指南](../../AGENTS.md) | 單筆重評與處理進度已具備 |

## 交付狀態

| 項目 | 狀態 | 完成條件 |
|---|---|---|
| store 單筆 requeue 與進度查詢 | 已完成 | 狀態轉換、事件、保護規則與截斷行為經單元測試涵蓋 |
| pipeline 重評入口與結構化 log | 已完成 | 重評以 active revision 入隊；各階段事件依設計輸出 |
| API rescore 與 status route | 已完成 | 成功、409、404 與 405 行為經測試涵蓋 |
| Agent 呼叫失敗分類 | 已完成 | 七種失敗類別經單元測試涵蓋；低分仍判定為成功呼叫 |
| Scorer reason 上限放寬 | 已完成 | prompt 與驗證同步為 100 字；上限邊界經單元測試涵蓋 |
| Side Panel 重新評分與處理進度 | 已完成 | Playwright 驗證按鈕、狀態轉換與進度呈現 |
| Chrome 人工 gate | 未完成 | 依 `docs/verify.md` V7 S38 於實際 Chrome 驗收 |

## 已知殘留限制

- 處理進度只在載入與重新整理時取得，不即時推播；正在評分中的單筆進度仍以既有 3 秒輪詢的職缺詳情呈現。
- `GET /api/v1/status` 的 Agent 呼叫固定回最近 20 筆，不支援分頁或依職缺篩選。
- 重評沿用當日評分預算；預算用盡時該筆停留 `queued` 至隔日日界。
