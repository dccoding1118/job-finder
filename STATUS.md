# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-26。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- API service 沒有 HTTP 請求層 log，排查 capture／badge 問題時無從得知插件送了什麼、回了什麼 status，只能反推資料庫。落點應為 `docs/designs/design-api.md` 的觀測面（method、path、status、來源，不記 payload 內容）。
- Cake 的條件式搜尋路徑不得取樣（`design-crawler` §1），因此 `ssr.search.filters` 與 URL 參數的映射無法驗證；內嵌狀態只在 URL 條件僅 `query`／`page` 時採用，巡邏 URL 實務上一律走 DOM 收割。
- Linux 使用者層級部署維持 XDG 分類；jobfinder 的公開主程式是使用者與 systemd 直接啟動的命令，安裝位置應改為 `~/.local/bin/jobfinder`，`~/.local/lib/jobfinder/` 僅保留 rollback 工件與安裝 metadata。

## §2 未完成任務

- [ ] 本輪修正（列表逐項容錯、Cake 項目容器取最外層、`ssr.search` 物件形態、Cake 地點退回公司城市、`PickForStage` 帶 revision、Cake SPA 模式路由與內頁 DOM 收割、同來源不比對去重、upsert 保留判定所屬 revision＋v5 資料修復、Scorer reason 要求 40~60 字）需重新部署並把 `extension/` 重新載入 Windows Chrome 後實測；部署時 v5 migration 會就地修復正式庫 41 筆讀不出分數的職缺。
- [ ] 若 104 兩個清單頁重新部署後仍無 badge，需取得 Chrome DevTools console 與 Network（`/api/v1/capture/list` 的 status 與 response）才能再往下判斷；本輪只證實了「整批 400 使全頁失去標記」這條路徑。
- [ ] 依 §1 補上 API 的 HTTP 請求層 log。
- [ ] 評估「Invoke 回錯但輸出已通過契約驗證」的白付重跑：正式庫 236 次 scorer 呼叫中 39 次屬此類（claude 29／codex 10），約佔失敗的三分之一；另有 77 次是 CLI／API 自報錯誤（不可避），7 次是 reason 超出容忍上限。落點應為 `docs/designs/design-agents.md` §3.1 的重試契約。
- [ ] 實作 B6 剩下一塊，契約見 `docs/designs/design-profile.md` §7 與 `docs/designs/design-agents.md`：反向校準（Calibrator agent、`jobfinder calibrate` 的白名單與 PII 防線、只產 diff 不寫檔）。
- [ ] 補上 V6 的驗收 harness 步驟 S40–S45 與 S41b（Cake capture 測資見 `docs/verify.md` §3.3.1；S46–S47 待校準實作），並完成 V6 的實際 Chrome 人工 gate（Cake 列表與內頁兩條擷取路徑、跨來源合併呈現與疑似重複裁決）。
- [ ] 完成 V7 S38 實際 Chrome 人工 gate（單筆重新評分與系統頁處理進度）；契約見 `docs/changes/change-job-rescore-and-progress.md`。
- [ ] 完成 V7 S37 實際 Chrome 人工 gate，PR 合併後以 `mise run deploy-install` 重新部署（既有機器需重新部署才會套用 timer `Persistent=false`）；目前正式 `jobfinder-api.service` 與 `jobfinder-run.timer` 已停用，`deploy-update` 不會重新 enable 已停用的 units。契約與驗收見 `docs/changes/change-profile-editor.md` 及 `docs/verify.md`。

**Roadmap — Profile 能力（暫不實作）**

- [ ] 反向校準的前端入口：目前 `jobfinder calibrate` 是唯一入口（CLI only，無 API route、Side Panel 無入口），使用者須登入 VM 下指令並自行閱讀 diff 檔。合理的前端形態是系統頁顯示 `interview` 累計與門檻、可觸發校準、以逐條建議（附證據與信心度）呈現 diff 並勾選套用至 Profile editor 草稿。**不得**改為自動套用——套用仍須經使用者明確儲存（PRD R1.3 與 Human-in-the-Loop）。

- [ ] 匯入去識別化履歷並自動填寫 Profile；須保留人工檢查與確認後才寫入的關卡。
- [ ] 支援多 Profile（多履歷），包含建立、切換、個別編輯，以及各次媒合所使用 Profile 的明確歸屬。

**Roadmap — 部署標準化（暫不實作）**

- [ ] deploy 整合 GitHub release：建立「正式版工件」路徑——由 CI/release 產出帶版號與 checksum 的正式工件（binary），正式環境只從該工件部署，不再從開發 checkout 直接 build+install（目前 `scripts/deploy/install.sh` 走 preflight→build→install 的開發目錄直裝路徑）。一併規劃 Chrome extension 隨 release 的上版流程如何整合（打包、版號對齊、`extension_origin` 更新）。目標：收斂「開發目錄→正式環境」的直接安裝路徑。
- [ ] 將公開主程式由 `~/.local/lib/jobfinder/jobfinder` 搬到 `~/.local/bin/jobfinder`，同步更新 `docs/deploy.md`、部署與驗收腳本、systemd unit template、rollback／manifest 路徑契約及相關測試；XDG config／data 配置維持不變。
