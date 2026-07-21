# STATUS — job-finder（MVP 開發）

> 最後更新：2026-07-22。規劃文件見 `docs/PRD.md`、`docs/design.md`、`docs/roadmap.md`、`docs/deploy.md`、`docs/designs/`。

## §1 未歸檔結論

（無。）

## §2 未完成任務

**Roadmap — 部署標準化（暫不實作）**

- [ ] deploy 整合 GitHub release：建立「正式版工件」路徑——由 CI/release 產出帶版號與 checksum 的正式工件（binary），正式環境只從該工件部署，不再從開發 checkout 直接 build+install（目前 `scripts/deploy/install.sh` 走 preflight→build→install 的開發目錄直裝路徑）。一併規劃 Chrome extension 隨 release 的上版流程如何整合（打包、版號對齊、`extension_origin` 更新）。目標：收斂「開發目錄→正式環境」的直接安裝路徑。
- [ ] 檢視並訂定檔案部署標準：目前 runtime 檔案／配置走 XDG 三分而散落三處（設定 `~/.config/jobfinder/`、資料 `~/.local/share/jobfinder/`、binary `~/.local/lib/jobfinder/`）；對照 agent-manager、gogents 改用單一 `~/.xxx/` 專用目錄。釐清兩種做法的規劃取捨（XDG 慣例 vs. 單一目錄好搬移／好備份／好清除），選定一套可跨專案共用的部署標準。**此項會重開 §1 已定案的「維持 XDG 三分、不改單一目錄」結論（現已載於 `docs/deploy.md` §2），若改標準需同步更新該文件與 install 腳本。**
