# change — pipeline 常駐 worker 與非同步 capture

## 1. 背景／動機

B5 的 104 spike 取樣後，內頁 capture 的同步評分設計被證實不可行，且與既有執行模型互相矛盾：

- `IngestJob` 原設計為「解析全文 → upsert → 同步條件篩選 → 通過者**立即 Scorer 評分** → 回傳五維分數」。
- 但 pipeline 是 flock 單例鎖逐項跑，LLM 另有 `min_interval` 20s 序列化與 300s timeout。
- 兩者相加的結果是：使用者點開一個 104 內頁，sidebar 會阻塞在「鎖競爭 ＋ LLM 排隊」上數十秒至數分鐘；若每日排程 run 正在進行（最多 30 筆 × 20s ≈ 10 分鐘），該筆甚至要等到隔天的 run 才會被取件。

根因是驅動模型錯置：抓取（IO、可排程、成批）與處理（LLM、逐項、隨時有件）被綁在同一個 `run` 生命週期裡，而 extension 是使用者隨時觸發的第三個入口，無法對齊任何「輪」。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | 排程只驅動 **fetch**（全自動來源爬取寫 DB）；filter／score／letter 改由**常駐 worker** 持續掃 DB 待處理狀態消化。 |
| D2 | worker 跑在 API server process 內（已是常駐服務，同 binary、同 systemd service）。process 內 mutex 為主要序列化機制；flock 只保留給 `jobfinder run` 這類第二 process 的手動除錯入口。 |
| D3 | `IngestJob` 不再呼叫 Scorer，改為「解析全文 → upsert → 同步條件篩選 → `filtered_out` ∣ `queued`」後立即回應。評分由 worker 非同步完成。 |
| D4 | sidebar 對通過篩選的職缺低頻輪詢 `GET /api/v1/jobs/{id}` 至 verdict 出現；`unfit` 由 capture 同步回應直接得出，不輪詢。 |
| D5 | LLM 成本上限由「每輪」改為「**每日總量**」（`llm.max_score_per_day` / `max_letter_per_day`），以台北時間日界重置。`llm.min_interval` 序列化不變。 |
| D6 | `runs` 只記抓取事實；判定分布改為展開該輪時即時從 `jobs` 查詢，新增 `jobs.discovered_by_run_id` 關聯。extension 來源的職缺不屬於任何 run（該欄位為 NULL）。 |

### 2.1 決策理由（摘要）

- **D1／D2**：`IngestList` 的同步性是 UX 需求（使用者站在 104 清單頁上，判定必須當場出現），而條件篩選無 LLM、毫秒級，同步完全成立。真正不能同步的只有評分。把處理面改為常駐，三個入口（排程 fetch、CLI、extension capture）就共用同一條消化路徑，不再有「等下一輪」的洞。
- **D3／D4**：條件篩選無 LLM ⇒ **`unfit` 在 capture 同步回應中即可得出**，被排除的職缺 sidebar 是即時的，只有通過篩選、真的要送 LLM 的才需要等待。輪詢對象是 localhost API（非 104、非 LLM），成本趨近於零；且 sidebar 場景使用者正盯著該頁，與「插件不得為求職信輪詢」的 dashboard 場景性質不同。
- **D5**：無「輪」則 per-run 上限失效。extension 由使用者自行觸發，連開二十個內頁即二十次 LLM 呼叫，必須有時間窗預算才能封住成本。
- **D6**：worker 非同步 ⇒ fetch run 結束時該批職缺尚未評分完，任何寫入 run stats 的判定統計當場即過期。改為即時查詢後，展開三天前那輪看到的是那批職缺**現在**的狀態。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| `jobfinder run` | 一輪跑完 fetch→filter→score→letter 後退出 | 只跑 fetch 後退出 |
| filter／score／letter 驅動 | run 的階段 | API server 內常駐 worker |
| 單例鎖 | flock，涵蓋整個 run | worker 為 process 內 mutex；flock 僅供手動 CLI 第二 process |
| `IngestJob` | 同步評分，回五維分數 | 只到條件篩選，回受理或快取 |
| `RequestLetter` | 背景觸發 letter 階段，鎖被占用則留待下輪 | 只經 store 轉 `letter_requested`，worker 自然取件 |
| sidebar | 等待同步回應 | capture 同步得 `unfit`；否則低頻輪詢 |
| LLM 上限 | `max_score_per_run` 30／`max_letter_per_run` 10 | `max_score_per_day`／`max_letter_per_day` |
| `runs.stats` | fetched/new/filtered/scored/shortlisted/letters_ok/letters_failed/errors | fetched/new/errors（僅抓取事實）＋ `queries` |
| run↔job | 無關聯 | `jobs.discovered_by_run_id` |

## 4. 落點

| canonical 文件 | 更新範圍 |
|---|---|
| `docs/PRD.md` | R7.1／R7.2／R7.3（排程與執行模型）、R8.1（Run 紀錄）、R9.2（內頁非同步評估）、R6.5 |
| `docs/designs/design-pipeline.md` | §1 職責邊界、§2 執行模型（run＝fetch、worker、ingest 入口）、§4 每日上限、§6 設定檔、§7 測試 |
| `docs/designs/design-api.md` | §1、§4（`capture/job` 回應、`POST /api/v1/runs` 語意）、Run 歷史即時查詢 |
| `docs/designs/design-schema.md` | §2.1（`discovered_by_run_id`）、§2.5（`runs.stats`）、§5（`PickForStage` 語意） |
| `docs/designs/design-extension.md` | §4（內頁 capture 與 sidebar 輪詢）、§4.2 |
| `docs/tests/test-pipeline.md`、`test-api.md`、`test-schema.md`、`test-extension.md` | 對應案例 |
| `docs/deploy.md` | systemd：timer 只跑 fetch；worker 隨 API service 常駐 |

## 5. 待實作進度

- [ ] store：`jobs.discovered_by_run_id` 欄位與 migration；`runs.stats` 縮減
- [ ] pipeline：run 縮為 fetch-only；常駐 worker（掃描迴圈、process 內 mutex、每日預算）
- [ ] pipeline：`IngestJob` 移除 Scorer 呼叫
- [ ] api：`capture/job` 回應改為受理／快取；Run 歷史即時 join 判定分布
- [ ] extension：sidebar 輪詢狀態機
- [ ] deploy：systemd timer 與 service 調整

## 6. 已知殘留限制

- 每日上限用盡後，新職缺停留 `queued` 至隔日；sidebar 顯示「已達今日評分上限」，此為刻意的成本封頂而非錯誤。
- worker 與手動 `jobfinder run --job ID` 併行時，仍以 flock 互斥；手動入口在 worker 常駐的情況下取得鎖的機會有限，僅供除錯，不應作為常態路徑。
- 批次來源（Yourator／Cake）產生的 partial 職缺不套用 partial 條件篩選，會停留 `discovered` 進入待看清單——partial 篩選只在 104 清單 capture 路徑上同步執行。
