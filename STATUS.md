# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-23。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

- Linux 使用者層級部署維持 XDG 分類；jobfinder 的公開主程式是使用者與 systemd 直接啟動的命令，安裝位置應改為 `~/.local/bin/jobfinder`，`~/.local/lib/jobfinder/` 僅保留 rollback 工件與安裝 metadata。

## §2 未完成任務

- [ ] 完成 V7 S37 實際 Chrome 人工 gate，PR 合併後以 `mise run deploy-install` 重新部署；目前正式 `jobfinder-api.service` 與 `jobfinder-run.timer` 已停用，`deploy-update` 不會重新 enable 已停用的 units。契約與驗收見 `docs/changes/change-profile-editor.md` 及 `docs/verify.md`。

**Roadmap — Profile 能力（暫不實作）**

- [ ] 匯入去識別化履歷並自動填寫 Profile；須保留人工檢查與確認後才寫入的關卡。
- [ ] 支援多 Profile（多履歷），包含建立、切換、個別編輯，以及各次媒合所使用 Profile 的明確歸屬。

**Roadmap — 部署標準化（暫不實作）**

- [ ] deploy 整合 GitHub release：建立「正式版工件」路徑——由 CI/release 產出帶版號與 checksum 的正式工件（binary），正式環境只從該工件部署，不再從開發 checkout 直接 build+install（目前 `scripts/deploy/install.sh` 走 preflight→build→install 的開發目錄直裝路徑）。一併規劃 Chrome extension 隨 release 的上版流程如何整合（打包、版號對齊、`extension_origin` 更新）。目標：收斂「開發目錄→正式環境」的直接安裝路徑。
- [ ] 將公開主程式由 `~/.local/lib/jobfinder/jobfinder` 搬到 `~/.local/bin/jobfinder`，同步更新 `docs/deploy.md`、部署與驗收腳本、systemd unit template、rollback／manifest 路徑契約及相關測試；XDG config／data 配置維持不變。
