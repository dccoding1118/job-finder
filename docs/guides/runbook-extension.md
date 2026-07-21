# Runbook — Extension 部署與 Chrome compatibility gate

供人工執行的操作手冊：如何把 jobfinder 的 Chrome MV3 extension 接上執行中的後端 API，並據以完成 `docs/verify.md` §4 的 **Chrome compatibility gate**（V4 人工驗收）。日常使用者要在瀏覽器操作 extension 走的是同一套設定，因此本檔同時是 extension 的部署／操作手冊。規格背景見 [design-extension](designs/design-extension.md)、部署基線見 [deploy.md](deploy.md)。

## 1. 拓撲與環境限制

實際佈署為跨機：**Chrome＋extension 在 Windows 桌機**，**批次（timer/one-shot）與 API 在 GCP VM**。兩端不能直接互連，原因是設計上的雙重 loopback 限制：

| 限制 | 出處 | 效果 |
|---|---|---|
| API 只綁 VM 的 `127.0.0.1:8686` | `deploy.md` §2/§4，`api.addr` 固定 loopback、不得改綁 `0.0.0.0` | API 不對外開埠，VM 外部無法直接連 |
| extension 只准連 loopback | `manifest.json` `host_permissions`＝`http://127.0.0.1/*`、`http://[::1]/*`；Options 亦只接受 `127.0.0.1`／`::1`／`localhost` 的 `http:` endpoint | extension 無法填入 VM 的公網位址或私網 IP |

因此兩端唯一合法橋接是 **SSH local port forward**：把 Windows 端的 `127.0.0.1:8686` 轉送到 VM 的 `127.0.0.1:8686`。extension 仍然只看到 loopback，API 仍然只綁 loopback，跨機流量全走 SSH 加密通道。**不以改綁 `0.0.0.0`、開防火牆埠或反向代理替代。**

關鍵常數（跨機一致）：

| 項目 | 值 |
|---|---|
| 固定 unpacked extension ID | `oddnhajjhmgogefocnljofeahniodiei`（由 `manifest.json` 的固定 `key` 決定，與載入路徑、機器無關） |
| `api.extension_origin` | `chrome-extension://oddnhajjhmgogefocnljofeahniodiei` |
| extension endpoint（Options 填） | `http://127.0.0.1:8686` |
| 轉送埠 | Windows `127.0.0.1:8686` → VM `127.0.0.1:8686` |
| VM 設定檔 | `~/.config/jobfinder/config.yaml`（owner-only；`api.token`、`api.extension_origin` 只存此處） |

## 2. VM 端前置（一次性）

1. **部署後端**：在 VM 執行 `mise run deploy-install`，完成安裝並啟動 `jobfinder-api.service` 與 `jobfinder-run.timer`（細節見 `deploy.md` §3–§4）。
2. **設定 `extension_origin`**：編輯 `~/.config/jobfinder/config.yaml`，將 `api.extension_origin` 由佔位符改為 `chrome-extension://oddnhajjhmgogefocnljofeahniodiei`，然後 `systemctl --user restart jobfinder-api.service`。
3. **取得 token**：從同一 `config.yaml` 讀出 `api.token`（安裝時隨機產生），稍後填入 extension。此值等同存取憑證，勿外流、勿進版控。
4. **本機自檢（VM 上）**：確認只有 loopback listener、且 token 閘門正常。

   | 檢查 | 指令 | 預期 |
   |---|---|---|
   | 帶 token | `curl -so /dev/null -w '%{http_code}' -H "Authorization: Bearer <token>" http://127.0.0.1:8686/api/v1/jobs` | `200` |
   | 未帶 token | `curl -so /dev/null -w '%{http_code}' http://127.0.0.1:8686/api/v1/jobs` | `401` |

## 3. 建立 SSH 通道（Windows 端）

在 Windows 開一個保持連線的通道，gate 期間全程開著。二選一：

- **gcloud（推薦，VM 無公網 IP 時走 IAP）**
  ```
  gcloud compute ssh <VM_NAME> --zone <ZONE> --tunnel-through-iap -- -N -L 127.0.0.1:8686:127.0.0.1:8686
  ```
- **原生 OpenSSH（VM 可直接 SSH 時）**
  ```
  ssh -N -L 127.0.0.1:8686:127.0.0.1:8686 <USER>@<VM_HOST>
  ```

`-N` 只建通道不開 shell；`-L` 前段的 `127.0.0.1:8686` 是 Windows 監聽埠，後段是 VM 上的目標。

**驗證通道**（另開 Windows 終端或瀏覽器）：`curl.exe -H "Authorization: Bearer <token>" http://127.0.0.1:8686/api/v1/jobs` 應回 `200`。連不到就先修通道，不要往下走（見 §7）。

> 穩定性：通道中斷 extension 即失聯。長時間使用可用 `autossh`，或依賴 gcloud IAP 自身的重連；gate 這種短時作業手動保持即可。

## 4. 載入 extension（Windows Chrome）

1. 把 repo 的 `extension/` 目錄放到 Windows 本機（`git clone` 後取該子目錄，或整包複製；Chrome 載入未封裝只能指向本機資料夾）。
2. Chrome → `chrome://extensions` → 右上開啟「開發人員模式」→「載入未封裝項目」→ 選 `extension/` 目錄。
3. 確認顯示的 ID＝`oddnhajjhmgogefocnljofeahniodiei`。**不符**代表載錯目錄或 `manifest.json` 的 `key` 被改動——修正後 VM 的 `extension_origin` 也要同步改，否則 API 會擋掉。

## 5. 設定 endpoint 與 token（extension Options）

1. 在 `chrome://extensions` 的 jobfinder 卡片點「詳細資料」→「擴充功能選項」，或直接開 `chrome-extension://oddnhajjhmgogefocnljofeahniodiei/options/index.html`。
2. **API endpoint** 填 `http://127.0.0.1:8686`；**API token** 填 §2 取得的 `api.token` → 儲存。
3. 儲存後 token 欄位清空、狀態顯示「已儲存」是預期行為（token 已寫入 extension storage）。endpoint 只接受 loopback，填其他位址會被 Options 擋下。

## 6. Chrome gate 人工驗收

點擊 extension toolbar action 開啟原生 Side Panel（除錯時亦可直接開啟 `chrome-extension://oddnhajjhmgogefocnljofeahniodiei/dashboard/index.html`），逐項操作並對照 `docs/verify.md` §4 對應步驟的**字面預期**。標準答案以 `verify.md` 為準，此處不重抄，只列要親手走到的觀察點：

| 對照案例 | 要操作／觀察的行為 |
|---|---|
| S17 | localhost API 正向認證：帶精確 extension Origin＋token 與無 Origin＋token 皆通、preflight 正常 |
| S18 | Side Panel 四頁籤與淺／深色切換；推薦清單的來源／流程／投遞／判定四種篩選精確；點入職缺詳情呈現五維、求職信、狀態事件 |
| S20 | 判定篩選、複製核准求職信到剪貼簿、對某筆 `apply pending → applied` 並回寫 SQLite |
| S21 | 對 `letter_failed` 職缺再次「生成求職信」，受理轉 `letter_requested`，worker 隨後取件 |
| S22 | 手動抓取完成，`runs` 出現 `trigger=manual-extension`；Run history 呈現抓取統計與判定分布 |

判定：每項行為符合 `verify.md` §4 的字面預期即 `PASS`；不符為 `FAIL`；若因通道／環境不可用而無法操作，記 `ENVIRONMENT_BLOCKED`，不誤判為產品失敗。

## 7. 驗收與記錄

Chrome gate 是**使用者本人自行核對驗收**——與自動 e2e（`mise run e2e-mock`／`e2e-live` 產出答案卷供你核對）性質相同，只是這裡由你在真 Chrome 上逐項對照 §6 與 `verify.md §4` 的字面預期，符合即 `PASS`。**不另建 evidence 記錄機制**：人工核對本身即驗收結論，記錄只聚焦自動化驗證的答案卷。

若自行留截圖或筆記供個人參考，仍守 Zero-PII——不外流 `token`、不把真職缺全文／求職信全文／日常 SQLite 資料進版控。

## 8. 疑難排解

| 症狀 | 可能原因 | 處置 |
|---|---|---|
| extension 全部請求失敗 / 連不到 | SSH 通道未開或已斷；Windows 端未監聽 `127.0.0.1:8686` | 重建 §3 通道，先用 `curl` 確認 `200` 再操作 extension |
| API 回 `401` | token 與 VM `config.yaml` 不一致；或帶了錯誤 Origin | 重新於 Options 貼上正確 `api.token`；確認 `extension_origin` 已設為固定 ID 並重啟過 API |
| Options 不接受 endpoint | 填了非 loopback 位址 | endpoint 必須是 `http://127.0.0.1:8686`（跨機由 SSH forward 承接），不是 VM 的公網／私網 IP |
| extension ID 不是固定值 | 載錯目錄，或 `manifest.json` 的 `key` 被更動 | 重新載入正確 `extension/`；若確實改了 `key`，同步更新 VM `extension_origin` 與 `verify.md`／verify 腳本內的固定 ID |
| 通道通但 dashboard 空白 | VM 端 API service 未啟動，或 SQLite 尚無資料 | 檢查 `systemctl --user status jobfinder-api.service`；必要時先手動抓取（S22）或跑一次 pipeline |
