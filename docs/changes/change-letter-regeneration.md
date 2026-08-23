# change — 求職信重新產製、歷程的重新讀取與承諾性敘述禁令

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

`v0.3.2` 的實際使用暴露三件事。

- **產出過求職信的職缺沒有任何重新產製的入口**。`letter_ready` 在狀態機上是處理軸終態，action dock 也只剩「複製求職信」。但過審與否是 Reviewer 的判定，不是使用者的：同一份 Profile 對同一個 JD 也可能寫出方向不對的一封信，而使用者對此無計可施——只能接受那一封，或放棄這筆職缺。
- **產製歷程會顯示已經不成立的空狀態**。展開一次之後結果就留在 Side Panel 的記憶體裡，下次展開直接沿用。空陣列同樣被當成「讀過了」，所以在產製開始前展開過的職缺，即使後來跑完一次產製，再展開仍然顯示「這筆職缺還沒有跑過求職信產製」。
- **求職信裡出現承諾性敘述**。實例：「我於 2022 年取得 Google Cloud Professional Cloud Architect 證照，現正規劃重新認證。期待有機會面談，說明我如何協助貴公司完成 GKE 平台建置。」兩句都符合現況，但對讀信的人是兩個承諾——會去重考證照、面試時要講得出 GKE 平台怎麼建。收信方的期待因此被架高到信件本身無從保證的位置，面試時要求的表現也跟著超出實際。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | `letter_ready` 增加往 `letter_requested` 的轉換，成為停留狀態；求職信的 API 入口與 `RequestLetter` 因此同時承接「重試」與「重新產製」，不另立路由與動作。 |
| D2 | 已有求職信的職缺在 action dock 提供「重新產製」，與「複製求職信」並列。 |
| D3 | 產製歷程每次展開都重新讀取，上次的結果只用來讓區塊立即有內容；要求產製後該區收起並丟棄上次的結果。 |
| D4 | Drafter prompt 明令禁止承諾性敘述，Reviewer prompt 將其列為必挑項。 |

D1 選擇沿用同一個入口而不新增「重新產製」路由：對 store 而言這三種來源狀態做的是同一件事——使用者要求一次產製。既有 Letter 不因此被刪改，新的一次產製寫入新的一列，讀取面取最新一列，先前各版留在產製歷程裡。

D3 的替代作法是讓 Side Panel 為求職信輪詢，但那與 R6.8「不得為此輪詢高頻請求」相衝突。展開是使用者主動的動作，把重新讀取綁在展開上既不增加閒置請求，又保證讀到的是當下的狀態。

D4 只做在 prompt 與審查關，不進 `guard()`：承諾與陳述的差別是語意的，程式防線比對的是詞表與佔位符，無從判定「現正規劃重新認證」是不是承諾。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| `letter_ready` | 處理軸終態，無出口 | 停留狀態，使用者要求即回 `letter_requested` |
| 已有求職信的 action dock | 只有複製 | 重新產製＋複製 |
| 產製歷程的讀取 | 每個職缺讀一次，結果沿用到頁面關閉 | 每次展開重讀，先畫上次的結果 |
| 求職信的承諾性敘述 | 無規則，Drafter 可自由寫入 | Drafter 禁止寫入，Reviewer 必挑 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/PRD.md` | R5.0、R5.1、R5.2、R6.8 | 已有求職信者可要求重新產製；Drafter 的承諾性敘述禁令與理由；Reviewer 的必挑項；生成入口的重新產製 |
| `docs/designs/design-schema.md` | §3.1 | `letter_ready → letter_requested` 轉換；終態與停留狀態的敘述 |
| `docs/designs/design-pipeline.md` | §4 | `RequestLetter` 的來源狀態 |
| `docs/designs/design-api.md` | §4 | `POST /jobs/{id}/letter` 的來源狀態與既有 Letter 的處置 |
| `docs/designs/design-agents.md` | §4 | Drafter 與 Reviewer 的規則要點 |
| `docs/designs/design-extension.md` | 求職信生成入口、求職信產製歷程 | 重新產製入口；每次展開重新讀取 |
| `docs/tests/test-schema.md` | ST-20 | 合法轉換清單 |
| `docs/tests/test-api.md` | AT-15A、AT-17 | `letter_ready` 受理；非法來源狀態清單 |
| `docs/tests/test-agents.md` | AT-19A | 兩個 prompt 的承諾性敘述規則 |
| `docs/tests/test-extension.md` | ET-62、ET-63 | 重新產製入口；歷程的重新讀取 |

## 5. 待實作進度

- [x] `letter_ready → letter_requested` 轉換
- [x] 重新產製入口
- [x] 產製歷程每次展開重讀
- [x] 兩個 prompt 的承諾性敘述規則
- [x] canonical 文件更新

## 6. 已知殘留限制

- 重新產製與第一次產製共用 `max_letter_per_day` 的同一格額度，重跑幾次就吃掉幾格；沒有「重跑不計入」的例外。
- 承諾性敘述只靠 prompt 與審查關把關，最後一輪的定稿不經審查，因此仍可能帶著承諾出去；投遞前自行過目這一點不變。
- 舊的求職信不會被標記為「已被新版取代」，讀取面只是取最新一列；要看先前各版得展開產製歷程。
