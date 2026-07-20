# change · B4 mock E2E、MV3 API 認證與 systemd lifecycle

> **變更紀錄檔**（非 canonical）。此處記錄變更動機與差異；正式規格以「落點」列出的 canonical 文件為準。
> 類型：update。日期：2026-07-17（Asia/Taipei）。狀態：實作與驗收完成。

## 背景／動機

B4 mock E2E 已能物化 binary、合成來源、SQLite、API 與 extension；隔離 Chromium 中的 MV3 service worker 呼叫 loopback API 時，部分 request 不會送出 `Origin`，與 API「所有請求同時要求精確 Origin 與 token」的契約衝突。systemd 驗收也只檢查 unit 文字並在 shell 直接執行 binary，未經 user systemd manager；evidence 則在斷言前標示 PASS，失敗報告無法可靠重建判定。

## 決策摘要

- API 維持 loopback-only 與可撤換 Bearer token。請求若帶 `Origin`，必須精確等於設定的 extension origin；MV3 privileged fetch 未帶 `Origin` 時，以有效 Bearer token 驗證。任何錯誤 Origin 即使 token 正確仍拒絕，缺失或錯誤 token 一律拒絕。
- 固定 manifest key 產生穩定 unpacked extension ID。隔離 Chromium 模擬記錄 request 的 method、path、Origin 是否存在、preflight/actual 與 Authorization 是否存在，不記錄 token、body、JD 或 Profile；此結果不取代實際 Chrome 人工 gate。
- `e2e-mock` 以同一物化 artifact、SQLite 與 evidence 完成 23 個步驟。每步只在產品斷言完成後寫 PASS；環境能力不足回 `ENVIRONMENT_BLOCKED`／exit 2，產品不符合契約回 `FAIL`／exit 1。
- systemd 驗收不安裝正式 unit。先驗證由 production template 渲染的 unit，再以 `systemd-run --user` 建立 transient API service、one-shot service 與 timer，證明 user manager 下的 PATH、artifact、設定與 SQLite 一致。
- Playwright 只使用 `mise` 管理的 Node 與專案 lockfile 安裝版本；安裝入口固定執行 `npm ci`。
- mock 使用 loopback Yourator-compatible 合成來源、驗收 artifact 內的 fake `claude`／`codex` executable 與隔離 Chromium extension 模擬；crawler adapter、pipeline、SQLite、API 與 systemd 執行 production 實作。真 Yourator 與已授權 CLI Runner 移至 live，實際 Chrome 移至人工 gate。
- 合成來源使用四筆可識別情境，分別驗證條件篩除、低分保留、高分核准與 Reviewer 固定退回。來源 request journal 與安全 SQLite snapshot 必須精確證明搜尋參數、detail request、欄位正規化、filter hits、五維分數、信件終態、狀態事件、Run stats 與 Agent 呼叫摘要。
- 本階段的 E2E 僅納入正向流程。認證拒絕、PII 拒絕、單例鎖衝突及外部服務錯誤等負向情境由 L1 規格與後續 E2E 累加處理。

## 相對既有狀態的差異

| 面向 | 既有狀態 | 最新狀態 |
|---|---|---|
| MV3 身分驗證 | 每個 request 必須同時帶精確 Origin 與 token | 帶 Origin 時精確比對；無 Origin 的 privileged MV3 request 由可撤換 token 驗證 |
| systemd | grep unit 並以 `env -i` 直接執行 | production template 靜態驗證＋transient user service/oneshot/timer 實跑 |
| evidence | 動作前先寫 PASS，步數 16／18 混用 | 23 步一致，斷言後才寫 PASS，終態唯一 |
| browser | extension 自動操作缺少可追溯結果 | 隔離 Chromium 模擬保存安全 request 摘要、固定 ID、SQLite 回寫與 screenshot；不宣稱實際 Chrome 通過 |
| Node toolchain | lockfile 缺失時退回 `npm install` | lockfile 必要，固定 `npm ci` 與專案 Playwright |
| pipeline evidence | 只比對筆數與終態字串 | 逐欄安全 snapshot、request journal 與精確 Run／Agent 呼叫計數 |
| browser evidence | copy 點擊後立即記為成功，Job ID 寫死 | 等待 clipboard 完成、比對合成信件、由畫面資料取得 Job ID，並驗證五維分數與格式化 Run stats |

## 落點（canonical 最新狀態）

- `docs/designs/design-api.md`：MV3 無 Origin request 的 token 契約、錯誤 Origin 拒絕規則與安全觀測摘要。
- `docs/designs/design-extension.md`：固定 unpacked ID、service worker credential、隔離 Chromium 模擬與實際 Chrome gate 邊界。
- `docs/tests/test-api.md`：有／無 Origin、token 與 preflight 的安全案例。
- `docs/verify.md`：23 步 mock E2E、systemd transient lifecycle、extension 模擬、live 真來源／真 Agent 與 evidence 判定。
- `docs/deploy.md`：開發驗收的 transient user unit 邊界。

## 待實作進度

- [x] API 認證與 L1 測試。
- [x] systemd transient lifecycle、evidence 終態與固定 Node／Playwright 入口。
- [x] 四情境來源 fixture、request journal、安全 SQLite snapshot 與精確正向斷言。
- [x] 隔離 Chromium extension 模擬的五維分數、clipboard、篩選、Run history 與 SQLite 回寫證據。
- [x] canonical 文件同步與完整驗收。

## 已知殘留限制

- 日常 Chrome compatibility gate 仍需驗收者以實際 Chrome 附加至同一 artifact；所有自動 browser E2E 均視為模擬。
- 真 Yourator 與已授權 CLI Runner 屬 `e2e-live`，須使用獨立 live config／SQLite，至少抓回一筆真資料並驗證格式；不由 mock 結果替代。
