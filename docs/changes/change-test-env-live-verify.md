# change — 測試環境的實機自動驗收

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

`scripts/verify/verify-live.sh` 是從隔離沙盒版的 live 驗收改寫而來，目標換成已安裝的測試環境，但驗證手段沒有跟著換，實機上跑不通：

**手動批次與常駐 worker 互斥。** 腳本以 `jobfinder run --stage` 的 filter／score／letter 驅動 Agent。那是除錯用的第二 process 入口，與常駐 worker 以 flock 互斥（`docs/designs/design-pipeline.md` §2.2）。測試環境的 API 服務帶著常駐 worker，第 01 步的 `run --stage filter --limit 1` 即被拒。

**篩選以每日額度為上限。** 第 04 步 `run --stage filter --limit 1000` 對整個 `new` 佇列跑篩選，受 `llm.max_filter_per_day`（預設 60）封頂。沙盒裡 `new` 只有合成的幾筆，實機上一趟就是數十次 Filter Agent 呼叫。

**判準仍假設空資料庫。** 第 02 步借用的 oracle `schema` mode 斷言資料庫沒有任何職缺，`live-snapshot complete` 斷言全庫恰有一筆 score 與一筆 letter，兩者都與 `docs/verify.md` §6 的增量判準矛盾，資料庫只要已有資料就必然 FAIL。

**只有 Linux 跑得動。** 腳本 source `lib.sh`、呼叫 checkout 內的 oracle 與 `mise exec -- node`，需要 git clone 與 mise。Windows 測試環境兩者皆無，實機驗收只能人工。

腳本的前提是「環境剛好處在可驗證的狀態」。測試環境是長期使用的真實安裝，worker 常駐、開關與排程由使用者依需要調整，這個前提不成立。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | Agent 驗證改走常駐 worker 的單筆入口：`POST /api/v1/jobs/{id}/process`（篩選續評分）與 `POST /api/v1/jobs/{id}/letter`（求職信）。不再呼叫 `run --stage`，worker 鎖的衝突因此不存在。 |
| D2 | 每趟分三段：**情境安排** → **驗證** → **復原**。安排前先把原值寫進復原紀錄檔；復原在正常結束、驗證失敗、中斷訊號時都執行；上一趟被強制終止而留下復原紀錄時，下一趟開跑前先復原。 |
| D3 | 復原的範圍是設定面：自動處理開關、`config.yaml`、API 服務與每日抓取工作的啟用／執行狀態。資料面的增量（新抓的職缺、該趟的判定、Agent 呼叫、求職信產製）保留，那是本趟的證據，也符合 `docs/verify.md` §6 的增量判準。 |
| D4 | 成本上限以職缺計：Filter Agent 至多兩筆職缺、Scorer 一筆、求職信產製一筆（輪數依設定的 `llm.max_letter_rounds`）。腳本斷言本趟所有 Agent 呼叫的 `job_id` 都落在它挑中的職缺內，任何其他職缺被呼叫即 FAIL。 |
| D5 | 抓取經已安裝的排程觸發（Linux `systemctl --user start jobfinder-run.service`、Windows `Start-ScheduledTask`），跑兩次：第一次驗真來源與資料格式，第二次驗冪等。兩次抓取都在自動處理關閉之後進行，新進職缺停在 `new`，不會被 worker 整批篩選。 |
| D6 | 同一份情境設計產出兩支腳本：`verify-live.sh`（Linux，bash）與 `verify-live.ps1`（Windows，PowerShell 5.1）。兩支共用同一份 oracle `assert-live.mjs`，斷言只寫一次。 |
| D7 | 腳本自足、隨 dev 部署包發送：原始碼放 `scripts/verify/live/`，`pack.sh` 在版本字串為 `dev` 時把三個檔案複製到工件目錄，與 `install.sh`／`install.ps1` 並列。執行不需要 git clone 與 mise，腳本不 source `lib.sh`。release 工件不帶。 |
| D8 | 刪除 `mise run verify-live`。入口是工件目錄內的腳本，開發機與 Windows 用同一種方式執行。 |
| D9 | 執行前提縮為四項：已安裝的 `jobfinder`、`node`、已授權的 `claude`／`codex` CLI、連得到正式 Yourator。`node` 在兩個平台都由 npm 安裝的 Agent CLI 帶進來。Linux 另需 `curl`。 |
| D10 | 判定維持三態，另加復原結果：驗證結果為 `PASS`（exit 0）／`FAIL`（exit 1）／`ENVIRONMENT_BLOCKED`（exit 2）；復原未完成時 exit 3 並壓過驗證結果，報告列出未復原的項目與手動復原指令。 |
| D11 | 報告與復原紀錄落在工件目錄的 `evidence/`。重新打包只覆寫工件，不清除該目錄。 |
| D12 | 測試環境的常態是每日抓取停用、自動處理關閉：每次部署或回滾後兩者都關回去，只有測試特定情境時才打開，測完關回。腳本不假設常態，一律讀原值、照原值復原。 |
| D13 | 求職信照一般使用者的方式打 `POST /api/v1/jobs/{id}/letter`，受 `llm.max_letter_per_day` 限制。額度足夠就依結果判定；額度不足時該項為無結果（`ENVIRONMENT_BLOCKED`），留下待補測紀錄，隔日以補測模式 `--recheck-letter`／`-RecheckLetter` 判定同一筆的產製結果，不再送新的要求。 |

D1 的理由不只是繞開鎖。`run --stage` 是 worker 停止時的人工重跑入口，使用者日常走的是常駐 worker；打在單筆入口上驗到的就是實際產品路徑，包括服務環境下的 PATH 與 Agent CLI 解析。單筆入口也不受每日預算與自動處理開關限制（`design-pipeline.md` §2.4），測試環境當天額度已被用掉時仍驗得動篩選與評分。

D6 選 node 當 oracle 的執行環境，是因為兩支腳本若各自實作斷言，判準會漂移，而漂移的症狀是兩個平台的「PASS」不是同一件事。

### 2.1 情境安排與復原

| 項目 | 驗證需要的狀態 | 讀原值 | 安排 | 復原 |
|---|---|---|---|---|
| API 服務 | 執行中 | Linux `systemctl --user is-active jobfinder-api.service`；Windows api 工作的 `State` | 未執行則啟動並等 API 回應 | 原本未執行則停止，Windows 等 process 消失 |
| `worker.paused` | `false`（單筆入口在未帶常駐 worker 時回 `409 worker_not_resident`） | `GET /api/v1/settings` 的 `resident_worker` | `resident_worker` 為 `false` 時備份 `config.yaml` 原檔，只改該鍵為 `false`，重啟 API 服務 | 以備份原檔覆蓋回去並比對 SHA-256，重啟 API 服務 |
| 自動處理開關 | 關閉 | `GET /api/v1/settings` 的 `auto_processing` | `PUT /api/v1/settings` 設 `false` | `PUT` 原值 |
| 每日抓取工作 | 可被手動觸發 | Windows run 工作的 `State` | Windows 為 `Disabled` 時 `Enable-ScheduledTask`；Linux 停用 timer 不影響手動啟動 one-shot，不安排 | Windows 原為 `Disabled` 則 `Disable-ScheduledTask`；Linux 無 |

安排依表列順序，復原反序。`worker.paused` 的安排排在自動處理開關之前：重啟後的服務才帶常駐 worker，開關要在這之後關，否則重啟與關閉之間 worker 可能開始整批消化。

Windows 改 `config.yaml` 一律以 `[System.IO.File]::WriteAllText` 寫無 BOM 的 UTF-8，復原用 `Copy-Item` 還原位元組（見 `AGENTS.md` §6 已知雷）。

### 2.2 挑選受驗職缺

篩選與評分要落在同一筆職缺上，求職信盡量也是同一筆。候選依序：

| 順位 | 來源狀態 | 送進驗證的方式 | 理由 |
|---|---|---|---|
| 1 | `shortlisted` | `POST .../reprocess` 送回 `new`，再 `POST .../process` | 先前通過篩選且達閾值，重跑後最可能再次達閾值，求職信可接在同一筆 |
| 2 | `scored` | 同上 | 先前通過篩選 |
| 3 | `new`（含本趟新抓的） | 直接 `POST .../process` | 資料庫只有新職缺時的來源 |

```
filter_agent_jobs = 0
for candidate in 候選清單:
    reprocess 回 409（有求職信歷史、merged）→ 下一筆
    process 並輪詢到該筆離開 new／queued 且 in_flight 無該筆
    本筆無 Filter Agent 呼叫（結構化條件 fail，零成本）→ 下一筆，此類跳過至多 10 筆
    filter_agent_jobs += 1
    本筆為 queued 之後的評分終態（scored／shortlisted）→ 篩選與評分皆驗到，結束
    filter_agent_jobs == 2 → 評分驗證 ENVIRONMENT_BLOCKED，結束
求職信職缺 = 上面那筆若為 shortlisted，否則庫內任一 shortlisted／letter_failed／letter_ready
    都沒有 → 求職信驗證 ENVIRONMENT_BLOCKED
```

求職信經 worker 取件，受 `llm.max_letter_per_day` 限制。API 讀不到求職信的剩餘額度，額度不足只能從結果辨認：

| 要求後的觀察 | 判定 | 後續 |
|---|---|---|
| 轉為 `letter_ready`／`letter_failed` | 依步驟 07 的判準判定 | 無 |
| 等待至少三個 `worker.scan_interval` 後仍停在 `letter_requested`，且 `in_flight` 無 letter 工作 | 無結果（`ENVIRONMENT_BLOCKED`），原因記為「求職信當日額度不足，未判定」 | 把職缺 ID 與要求時間寫入 `evidence/letter-pending.json` |

該筆要求不會消失：跨台北日界後 worker 自行取件產製。補測模式只讀 `letter-pending.json` 指向的那一筆——已產製者以要求時間之後的那次產製依步驟 07 判定並刪除紀錄；仍停在 `letter_requested` 者維持無結果。補測模式不做情境安排、不送任何要求、不花額度。

完整模式開跑時若 `letter-pending.json` 仍在，步驟 07 改為判定該筆，不另送新要求，避免同時掛著兩筆未處理的要求。

### 2.3 驗證步驟

| # | 步驟 | 動作 | 判準 |
|---|---|---|---|
| 00 | 環境預檢 | 檢查執行前提；有殘留復原紀錄時先復原 | 前提缺項為 `ENVIRONMENT_BLOCKED` |
| 01 | 安裝身分與設定契約 | `jobfinder version`、`jobfinder paths`；讀已安裝的 `config.yaml` | 版本為 `dev (<commit>)`；執行中 API process 的執行檔是安裝放置的 binary；`api.addr` 為 loopback、有 token；`db.path` 指向回報的資料庫；Yourator `base_url` 缺省或為官方主機；四個 role 的 primary／fallback 共八個 endpoint 都明確指定 agent 與 model |
| 02 | Profile、權限與 schema | `jobfinder profile lint`、`jobfinder verify snapshot` | lint 通過；Linux 上設定、Profile、denylist 為 `0600`（Windows 靠 `%LocalAppData%` 的 ACL，不驗權限位元）；schema 通過 oracle |
| 03 | 情境安排 | §2.1 | 每項安排後重讀一次確認生效 |
| 04 | 真來源抓取 | 經排程觸發一次抓取並等結束 | 該趟 `runs` 列 `trigger` 為 `timer`、無 errors、`fetched` ≥ 1；資料格式通過 oracle 的 source 斷言 |
| 05 | 抓取冪等 | 記指紋，再觸發一次抓取 | 該趟 `new` 為 0；每筆職缺的 `content_hash` 與 `process_state` 前後逐筆相同 |
| 06 | 單筆篩選與評分 | §2.2 | 受驗職缺的逐條判定、彙總、四維分數、加權總分、reason 通過 oracle；Filter Agent 呼叫的職缺 ≤ 2 筆、Scorer 恰 1 筆 |
| 07 | 單筆求職信 | `POST .../letter` 並輪詢到 `letter_ready`／`letter_failed`；額度不足時見 §2.2 | `letter-history` 最新一次產製有 drafter 呼叫；`approved` 者有 reviewer 呼叫；信件含兩個落款佔位；本趟 drafter／reviewer 呼叫只落在該筆 |
| 08 | 已安裝排程定義 | Linux `systemd-analyze --user verify` 三個 unit；Windows 讀 `\jobfinder\` 的 api 與 run 工作 | Linux 三個 unit 通過驗證並指向安裝的 binary 與設定、timer 為每日 08:30 台北時間；Windows 兩個工作存在且 action 指向安裝的 `jobfinderw.exe` |
| 09 | 執行中的 loopback API | 帶與不帶 token 讀 `/jobs`、`/runs` | 帶 token 回 200 且含 `items`；未帶回 401 |
| 10 | 復原 | §2.1 反序 | 每項重讀後等於原值；`config.yaml` SHA-256 等於原檔；刪除復原紀錄 |
| 11 | evidence 安全性 | 掃描報告 | 不含 token、JD 內文、信件內容與落款佔位字面值 |

步驟 04 至 07 的 Agent 呼叫歸屬以 `GET /api/v1/status` 的 `agent_calls`（含 `job_id` 與 `created_at`）判定：取本趟開始時間之後的呼叫，逐筆核對職缺與角色。`jobfinder verify snapshot` 的 `agent_calls` 是依角色彙總的計數，無法歸屬到單筆，只用於格式與指紋。

### 2.4 oracle

`assert-live.mjs` 取代 `assert-positive.mjs` 的 `live-snapshot` 與 `live-fingerprint` 兩個 mode，後兩者一併刪除。

| mode | 輸入 | 斷言 |
|---|---|---|
| `schema` | snapshot | schema 版本、journal mode、foreign keys、必要資料表 |
| `source` | snapshot | Yourator 職缺的 external ID 不重複、canonical HTTPS URL 在 `www.yourator.co`、標題／公司／地點非空、JD 長度 > 0、hash 格式、`remote_type` 列舉、薪資兩端同為 NULL 或 min ≤ max |
| `fingerprint` | snapshot | 輸出 `筆數:sha256`，以 source、external ID、`content_hash`、`process_state` 組成 |
| `job <id> screened` | snapshot | 該筆 `filter_outcome` 與逐條 `verdict` 在列舉內；評分終態者四維在合法區間、總分與 `reason_sha256` 存在 |
| `job <id> lettered` | snapshot | 該筆 letter 狀態為 `approved`／`finalized`／`failed` 之一；非失敗者兩個落款佔位皆為真 |

判準全部以單筆或增量表達，不對全庫的 score 或 letter 筆數下斷言。

## 3. 相對舊狀態的差異

| 主題 | 舊狀態 | 新狀態 |
|---|---|---|
| Agent 驗證的入口 | `run --stage` 的 filter／score／letter，與常駐 worker 互斥 | 常駐 worker 的單筆入口 `process`／`letter` |
| 篩選成本 | 整個 `new` 佇列，受每日額度封頂 | 至多兩筆職缺，Scorer 一筆，求職信一次產製 |
| 環境前提 | 假設環境剛好可驗證 | 腳本自行安排情境並復原 |
| 判準 | oracle 斷言全庫恰一筆 score、一筆 letter | 單筆與增量 |
| 抓取觸發 | `run --stage fetch` 直接執行，排程觸發另一步驗 | 兩次抓取都經已安裝排程觸發 |
| 平台 | 只有 Linux，需要 git clone 與 mise | Linux 與 Windows 各一支，隨 dev 部署包發送 |
| 入口 | `mise run verify-live` | 工件目錄內的 `verify-live.sh`／`verify-live.ps1` |
| 報告位置 | checkout 內 `.local-dev/test-deploy/evidence/` | 工件目錄的 `evidence/` |
| 判定 | 三態 | 三態加復原結果（exit 3） |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/verify.md` | §1 | 腳本位置改為 `scripts/verify/live/`；「跑到哪停」的 V3 改指工件目錄內的腳本，兩個平台 |
| `docs/verify.md` | §6 | 全節改寫：執行前提、入口與兩個平台、情境安排與復原、受驗職缺的挑選、步驟與判準、成本上限、求職信額度不足時的無結果與補測模式、exit code 含復原結果 |
| `docs/verify.md` | §6.1 | 人工組 D8 註明「Agent CLI 可被服務叫起」已由 Windows 實機驗收覆蓋，人工只剩「不彈出主控台視窗」的觀察 |
| `docs/verify.md` | §7、§8、§9 | live 報告的位置；「live 資料」列改為測試環境的 SQLite；§9 第 1 項改為兩個平台各跑通一次 |
| `docs/guides/test-environment.md` | §3 | 工件目錄清單加入 `verify-live.sh`、`verify-live.ps1`、`assert-live.mjs` 與 `evidence/` |
| `docs/guides/test-environment.md` | §4、§5 | 測試環境的常態：部署或回滾後停用每日抓取並關閉自動處理；測試特定情境才打開，測完關回 |
| `docs/guides/test-environment.md` | §8 | live 驗收改為兩個平台都跑，各附完整模式與補測模式的執行指令；刪除「需與開發環境同機」「其餘平台人工」 |
| `docs/deploy.md` | §7 | dev 部署包的內容加入實機驗收腳本與 oracle，release 工件不帶 |
| `docs/design.md` | §8 | 測試策略的 Live 驗收列：兩個平台、單筆入口、跑完復原 |
| `AGENTS.md` | §5 | 分層表的 Live 驗收列改為工件目錄內的腳本；腳本位置段落改為 `scripts/verify/live/`，刪除 `verify-live.sh` 與 `lib.sh` 同層的敘述 |
| `docs/changes/change-test-environment.md` | §5 第 10 項、§6 | 第 10 項改指本文；刪除「`verify-live` 需要 git clone」與「`verify-live.sh` 在實機上跑不通」兩條殘留限制 |

## 5. 待實作進度

| # | 項目 | 狀態 |
|---|---|---|
| 1 | 第 4 節落點的 canonical 文件就地更新 | ⏳ |
| 2 | `scripts/verify/live/assert-live.mjs`；刪除 `assert-positive.mjs` 的 `live-snapshot`、`live-fingerprint` | ⏳ |
| 3 | `scripts/verify/live/verify-live.sh`：§2.1 至 §2.3，自足不 source `lib.sh`；刪除 `scripts/verify/verify-live.sh` 與 `lib.sh` 內指向它的註解 | ⏳ |
| 4 | `scripts/verify/live/verify-live.ps1`：與第 3 項同一份步驟與判準 | ⏳ |
| 4A | 兩支腳本的補測模式與 `letter-pending.json` | ⏳ |
| 5 | `scripts/release/pack.sh`：`dev` 版本複製三個檔案到工件目錄 | ⏳ |
| 6 | `mise.toml`：刪除 `verify-live` 任務 | ⏳ |
| 7 | Linux 測試環境實跑一次，含一趟中途 `kill -9` 後由下一趟復原 | ⏳ |
| 8 | Windows 測試環境實跑一次，含一趟 `worker.paused: true` 起始的情境 | ⏳ |

## 6. 已知殘留限制

- **求職信受每日額度限制，且事先查不到。** 單筆插隊只涵蓋篩選與評分，求職信經 worker 取件；`GET /api/v1/status` 沒有求職信的剩餘額度，要求端點在額度用盡時仍受理。額度不足只能等待後從結果辨認，該項隔日補測。產品日後補上額度讀取面與要求時的擋下，本流程只需把「等待後辨認」換成預檢；另一個待決的是求職信驗證是否比照篩選與評分改走不受每日上限的入口。
- **求職信的呼叫數不是固定值。** 一次產製的輪數取決於 Reviewer 是否核准，上限由 `llm.max_letter_rounds` 決定；腳本限制的是產製次數，不改寫該設定。
- **受驗職缺的狀態會被改寫。** 順位 1、2 的候選經 `reprocess` 重跑，新 score 成為現行分數，舊 score 保留為歷史。這是測試資料，且與 D3 的增量判準一致。
- **資料庫從未通過篩選時可能驗不到評分。** 候選只剩 `new` 且前兩筆進入 Filter Agent 的都被判不適合，評分驗證為 `ENVIRONMENT_BLOCKED`。
- **驗收期間不要操作該測試後端。** 在 Side Panel 切換自動處理或要求求職信，會被斷言視為本趟以外的呼叫，或在復原時被覆寫回原值。
- **強制終止只能延後復原。** `kill -9`、關機或關閉 PowerShell 視窗時復原不會執行，環境停在安排後的狀態，直到下一趟開跑時依復原紀錄還原。
