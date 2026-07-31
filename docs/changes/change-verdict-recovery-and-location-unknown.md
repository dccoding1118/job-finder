# 單筆重新處理、篩選終局化與未知地區

## 背景／動機

Cake 的列表項目常不帶地點，解析端以字面值 `unknown` 補上必填的 `location`，但地區硬規則只把空字串視為未決，於是「來源沒說」被讀成「地點不符」，職缺當場判為不適合——與「缺資訊絕不判不適合」的判準相違。同一批職缺點開內頁時又因 `UpsertJob` 的重設條件繞過 `canReset` 而回到 `new`，清單摘要的篩選形同白做，並多付一次 Filter Agent 呼叫。此外，判定錯誤的救援只有整批 activation，`rescore` 只收 `scored`／`shortlisted`，判錯的 `filtered_out` 無路可回。

## 決策摘要

- `location` 未知以 `store.LocationUnknown` 表示，地區規則對它與空字串一律判 `unknown`。哨兵值集中於 store，crawler 與判定端共用同一個常數。
- `filtered_out` 對來源內容變更是終局的：列表摘要與 JD 全文適用同一組硬規則，摘要階段的 `fail` 是對已陳述事實的結論，補全文只更新內容與 `content_hash`，不重開判定、不再付 Filter Agent。
- 單筆重評擴為單筆重新處理（`POST /api/v1/jobs/{id}/reprocess`）：使用者不同意的判定既可能出在評分關也可能出在篩選關，因此重做整條判定鏈。職缺回到管線起點——有 JD 全文者 `new`、只有摘要者 `discovered`——並清除舊命中、舊篩選逐條結果與舊 `score_revision`。
- 求職信階段與 `merged` 的職缺不得重新處理，求職信、投遞歷史與合併裁決不因此改寫；scores 維持 append-only。
- 狀態重設（內容變更或重新處理）一律連同 `filter_hits` 與該 Job 的 `filter_results` 清除：它們描述的是已被撤回的判定。
- 清單標記的判定快取在分頁自隱藏轉為可見時失效並重新收割；既有職缺的列表擷取只讀取、不重新篩選，因此不產生 Agent 呼叫。
- Side Panel 對 `pending_screen` 與 `pending_score` 都輪詢：重新處理後的職缺先經篩選才到評分。

## 相對舊狀態的差異

| 主題 | 舊狀態 | 最新狀態 |
|---|---|---|
| 來源未陳述地點 | 存字面值 `unknown`，被地區規則判 `fail` ⇒ 不適合 | 存哨兵值並判 `unknown` ⇒ 不構成拒絕 |
| 擷取 `filtered_out` 職缺的內頁 | 重設回 `new`，重跑篩選並可能再付 Filter Agent | 內容更新、判定不動 |
| 單筆救援入口 | `rescore`：僅 `scored`／`shortlisted` 回 `queued` | `reprocess`：任何非求職信、非 `merged` 的職缺回管線起點 |
| 重設後的舊命中與舊篩選結果 | 留在 Job 上，與新狀態並存（如「篩選中｜命中 locations」） | 隨重設清除 |
| 清單標記 | 判定快取在文件生命週期內不失效，須 F5 | 分頁回前景即失效重問 |
| Side Panel 輪詢 | 只有 `pending_score` | `pending_screen` 與 `pending_score` |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [Crawler 設計](../designs/design-crawler.md) | 未知地區哨兵值與公司城市補位的理由 |
| [Pipeline 設計](../designs/design-pipeline.md) | 地區條件的 `unknown` 判準、`RequestReprocess` 入口與 log 事件 |
| [Schema 設計](../designs/design-schema.md) | `filtered_out` 的終局性、重新處理的狀態轉換與 `ReprocessJob` 介面 |
| [API 設計](../designs/design-api.md) | `reprocess` route 契約與錯誤碼 |
| [Extension 設計](../designs/design-extension.md) | 重新處理動作、判定快取失效與輪詢範圍 |
| [Crawler／Schema／Pipeline／API／Extension 測試](../tests/) | CT-60、ST-66／66B／67、PT-77／77B／77C、PT-84、AT-70／71／71B、ET-38／49 |
| [驗收](../verify.md) | V7 S38 與 S23 的 Playwright 鎖定案例數 |
| [開發指南](../../AGENTS.md) | 未知地區哨兵、`filtered_out` 終局性與單筆重新處理 |

## 交付狀態

| 項目 | 狀態 | 完成條件 |
|---|---|---|
| 未知地區哨兵與地區規則 | 已完成 | crawler 與 filter 的單元測試涵蓋空字串與哨兵值 |
| `filtered_out` 終局化 | 已完成 | 擷取內頁後狀態、命中與取件行為經 pipeline 測試涵蓋 |
| store 單筆重新處理 | 已完成 | 全文／摘要兩種去向、冪等、保護狀態與清除行為經單元測試涵蓋 |
| API reprocess route | 已完成 | 成功、`filtered_out` 來源、409 與 404 行為經測試涵蓋 |
| Side Panel 重新處理與輪詢 | 已完成 | Playwright 驗證按鈕、狀態轉換與篩選中呈現 |
| 清單標記快取失效 | 已完成 | Playwright 驗證回前景後重問並就地更新 |
| Chrome 人工 gate | 未完成 | 依 `docs/verify.md` V7 S38 於實際 Chrome 驗收 |

## 已知殘留限制

- 重新處理沿用當日篩選與評分預算；預算用盡時該筆停留於 `new`／`queued` 至隔日日界。
- 已因舊規則判為不適合的既有職缺不會自動翻案，需由使用者逐筆重新處理或改動 Profile 觸發整批 activation。
- 清單標記的失效以分頁可見性為觸發；同一分頁內未離開時仍以既有的收割與重試機制更新。
