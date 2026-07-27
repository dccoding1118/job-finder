# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-27。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- 實機 Chrome 人工 gate 的結果不回寫 `docs/verify.md`（不附截圖或證據檔）：每次改動皆由使用者實際部署後在 Windows Chrome 操作驗證，驗收結論當場即知，回寫只是重複記錄。verify 的 ⏳ 僅代表自動化 harness 步驟尚未實作。

## §2 未完成任務

- [ ] 補上 V6 的驗收 harness 步驟 S40–S45 與 S41b（Cake capture 測資見 `docs/verify.md` §3.3.1）。

**Roadmap（暫不實作，規劃見 `docs/roadmap.md`）**

- [ ] S1：反向校準閉環（前端入口，不做 CLI 指令）、每日高分職缺推送、成效統計、深入評估、履歷匯入產生 Profile 草稿、多 Profile。
- [ ] 工程面：部署改走 CI/release 正式版工件、主程式搬至 `~/.local/bin/jobfinder`、API 請求層 log、Agent「輸出已過契約驗證但 Invoke 回錯」不重跑的重試契約。
