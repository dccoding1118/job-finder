# change — 重新部署時重新武裝排程

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

測試環境指南要求與正式環境共用 Agent 額度的測試機停用每日抓取排程。Linux 測試環境以 dev 部署包重裝時，常駐 binary 已存在，bootstrap 交棒 `update`：binary 替換、排程定義替換、API 重啟與 API 的生效面驗證都通過，最後停在 `install: jobfinder-run.timer ActiveState=inactive, want active`，安裝紀錄沒有寫入。

缺口是「建立排程」的動作只存在於 `install`，而「排程已武裝」的驗證三條路徑共用：

| 環節 | `install` | `update` | `rollback` |
|---|---|---|---|
| 掛載或還原排程定義 | 寫入 unit，`enable` timer | 寫入 unit，`enable` timer | 還原 stash 的 unit，不 `enable` |
| 啟動或重啟 | 重啟 API，`start` timer | 只重啟 API | 只重啟 API |
| 生效面驗證 | 要求 timer active | 要求 timer active | 要求 timer active |

`enable` 只決定 manager 下次啟動時要不要帶起 timer，不改變它當下的 `ActiveState`。排程在執行 `update` 或 `rollback` 之前已停止時，這兩條路徑必定失敗；排程原本就在跑的機器才會通過，驗證項因此證明不了任何事。

Windows 的 `update` 以 `Register-ScheduledTask -Force` 覆寫工作定義，範本的 `Enabled=true` 會一併寫回，停用過的工作恢復啟用。`rollback` 還原的是 `update` 當下匯出的定義，匯出時工作若已停用，還原出來的也是停用狀態，驗證同樣因沒有 `NextRunTime` 而失敗。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | `install`、`update`、`rollback` 對 API 服務與每日抓取排程一律建立或重建：API 無條件重啟，排程無條件重新武裝，與執行前處於運行、停止或停用無關。 |
| D2 | 重新武裝只排定下一次觸發，**絕不執行抓取**。抓取只由排程到點、操作者手動觸發，或 Side Panel 的重新整理動作啟動。 |
| D3 | Linux 以 `systemctl --user enable jobfinder-run.timer` 加 `systemctl --user restart jobfinder-run.timer` 重新武裝：前者讓重開機後仍會帶起 timer，後者讓它當下就是 active。三條路徑都不對 `jobfinder-run.service` 下任何啟動指令。 |
| D4 | Windows 以 `Enable-ScheduledTask` 重新武裝抓取工作。三條路徑都不對抓取工作呼叫 `Start-ScheduledTask`。 |
| D5 | 測試環境停用排程的步驟保留，於每次重新部署後重做。 |

D3 取 `restart` 的理由與 API 的無條件重啟相同：它對停止中與執行中的 timer 都重新計算下一次觸發，替換後的 `OnCalendar` 立即生效。timer 為 `Persistent=false`，啟動與重啟都不補跑錯過的觸發。以與 `jobfinder-run.timer` 相同 `OnCalendar`／`Persistent=false` 的探針 unit 在 systemd 255 上實測：timer 為 inactive、active、停用後重新 `enable` 三種狀態下 `restart`，對應的 service 都未被執行，改動 `OnCalendar` 後下一次觸發時間隨之更新。

D4 同理：抓取工作的觸發只有 `CalendarTrigger`，且 `StartWhenAvailable=false`，啟用不會補跑錯過的觸發。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| Linux `update`／`rollback` 的排程 | 不處理，timer 停止時驗證失敗；`rollback` 還原的 unit 不 `enable` | `enable` 加 `restart` timer，通過驗證 |
| Linux `install` 的排程 | `start` timer | `enable` 加 `restart` timer，與另兩條路徑共用 |
| Windows `rollback` 還原出停用的工作 | 保持停用，驗證失敗 | `Enable-ScheduledTask` 後通過驗證 |
| 保證不觸發抓取 | 文件聲明 | 另由指令層單元測試與自動組的 `systemctl` 呼叫紀錄守住 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/deploy.md` | §4 | 子命令表的動作欄加入重新武裝排程；三條路徑建立或重建服務與排程、重新武裝不觸發抓取、兩平台的武裝指令 |
| `docs/verify.md` | §6.1 | 人工組新增 D11：停用排程後重新部署與回滾；自動組核對 D1、D5、D5A、D6 的 `systemctl` 呼叫；自動組不涵蓋範圍補上排程武裝的指令層單元測試 |
| `docs/guides/test-environment.md` | §4、§5、§8 | 重新部署會重新武裝排程但不觸發抓取、停用於每次部署後重做；人工組列入 D11 |
| `docs/guides/getting-started.md` | §10.1 | `update` 與 `rollback` 重新武裝排程且不觸發抓取 |
| `AGENTS.md` | §6 已知雷 | `enable` 不改變 timer 當下狀態；重新武裝不得啟動抓取 |

## 5. 待實作進度

- [x] canonical 文件更新
- [x] Linux：`install`／`update`／`rollback` 共用 `enable` 加 `restart` timer
- [x] Windows：`install`／`update`／`rollback` 共用 `Enable-ScheduledTask`
- [x] 指令層單元測試：兩平台三條路徑都重新武裝排程，且不發出任何啟動抓取的指令
- [x] 自動組核對 D1、D5、D5A、D6 的 `systemctl` 呼叫
- [x] Linux 測試環境實機 D11
- [ ] Windows 測試環境實機 D11

## 6. 已知殘留限制

- 自動組的 `systemctl` 由 stub 吸收，核對得到安裝器送出哪些指令，核對不到 systemd 收到後的實際狀態；後者只由人工組 D11 守著。
- Windows 端的指令序列只有單元測試，實機結果要等 Windows 測試環境跑完 D11。
