# change — update 的重啟與回滾點

> 非 canonical。本文只負責記錄本次變更的動機、決策與落點；規格的最新狀態以第 4 節列出的 canonical 文件為準。

## 1. 背景／動機

`jobfinder update` 的兩個環節各自在一種情境下靜默失效，皆由 v0.1.1 升級到 v0.2.0 的實測發現。

- **服務停止時不會被重新啟動**。Linux 的重啟走 `systemctl --user try-restart`，該指令對停止中的服務是 no-op。使用者為了備份資料庫先停掉 API，接著跑 `update`，於是 binary 替換完成、重啟沒有發生、生效面驗證因服務不是 active 而失敗，整個更新停在中途狀態。Windows 的重啟走 Stop → 等 process 消失 → Start，不受影響。
- **同版重跑會毀掉回滾點**。`placeBinary` 無條件把現行 binary 複製成 `jobfinder.prev`。第一次 `update` 之後現行已是新版，同一份工件再跑一次就用新版覆蓋掉 `.prev` 裡的前一版，`rollback` 之後回到的是它本來要撤銷的那一版。上一則情境會直接誘發這個：使用者看到更新沒收尾，起了服務再跑一次。

`v0.3.0` 升級後的回滾實測又暴露第三與第四個缺口：`rollback` 換回舊 binary 後服務起不來，訊息只有 `install: jobfinder-api.service is failed, want active`。真正的原因寫在 journal 裡——`database schema version 10 is newer than supported version 9`，也就是資料庫已升級而 `rollback` 不動資料庫。重啟本身成功，失敗的是服務啟動，而命令說不出這個差別。

第四個缺口出在同一次實測：`rollback` 只退一個版本，而連續執行第二次時它會把來源與現行同為一份建置的情況照做一遍——把 `.bad` 覆蓋成該版，銷毀第一次剛撤下來的版本，並回報 `rollback complete`。這與 `update` 的同版覆蓋是同一類問題，差別只在毀掉的是前滾的副本。

## 2. 決策摘要

| # | 決策 |
|---|---|
| D1 | Linux 的重啟改用 `systemctl --user restart`：無條件重啟，停止中的服務會被啟動。`update` 與 `rollback` 共用這條路徑，兩者同時修好。 |
| D2 | `placeBinary` 於來源與目標的 SHA256 相同時保留既有的 `.prev`，只替換 binary 本身並回報一行。 |
| D3 | 生效面驗證失敗時附上服務自己的輸出：Linux 取 journal，Windows 取 `log.file`，取不到則附工作的 `LastTaskResult`。 |
| D4 | 來源與現行為同一份建置時 `rollback` 拒絕執行，不動任何檔案。 |

`enable --now`、`try-restart` 與 `restart` 三者只有最後一個在兩種情境下都正確：前者對執行中的服務無作用，中者對停止中的服務無作用，而更新後必須確保記憶體裡跑的是新 binary，與它更新前處於哪個狀態無關。

D2 以內容摘要判斷而非檔案識別：來源與目標本來就是不同路徑的兩個檔案，`os.SameFile` 回答不了「這是不是同一份建置」。

## 3. 相對舊狀態的差異

| 面向 | 舊 | 新 |
|---|---|---|
| Linux 重啟指令 | `try-restart`，服務停止時無作用 | `restart`，無條件重啟 |
| 同版重跑 `update` | 以現行版覆蓋 `jobfinder.prev` | 保留既有 `.prev`，回報 binary 未變 |
| 驗證失敗的訊息 | 只有「服務不是 active」 | 另附服務自己印出的最後數行 |
| 連續第二次 `rollback` | 以現行版覆蓋 `.bad` 並回報成功 | 拒絕執行，`.bad` 保持第一次撤下來的版本 |

## 4. 落點（canonical 最新狀態）

| 文件 | 章節 | 內容 |
|---|---|---|
| `docs/deploy.md` | §4 | `update` 的 `.prev` 保留條件；重啟一律無條件的理由；驗證失敗附服務輸出；跨 schema 版本回滾須先還原資料庫 |
| `docs/guides/getting-started.md` | §10.1 | 跨 schema 版本的更新前備份與回滾順序；回滾只退一個版本 |
| `docs/verify.md` | §6.1 | D6A：服務停止時的回滾；D6B：連續第二次回滾被拒絕 |
| `docs/verify.md` | §6.1 | D5A：服務停止時的更新與同版重跑後的 `.prev` |

## 5. 待實作進度

- [x] Linux 重啟改為無條件
- [x] 同版重跑保留回滾點
- [x] 驗證失敗附上服務輸出
- [x] 同版回滾拒絕執行
- [x] canonical 文件更新

## 6. 已知殘留限制

- 已被覆蓋掉的 `.prev` 無從還原；升級前踩過這個情境的機器需自行下載前一版工件重裝。
- 生效面驗證仍不預先檢查「舊 binary 能不能開現行資料庫」——舊 binary 不提供它支援的 schema 版本，無從在回滾前判定。診斷改為事後附上服務的原因，操作順序（先還原資料庫再回滾）由文件承擔。
