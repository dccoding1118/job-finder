# 測試規格 — extension（`extension/`）

對應 [extension 模組設計](../designs/design-extension.md)、PRD R1、R6、R9。Side Panel、Profile editor、Options 與 service worker 以 mock Chrome API 與 mock API response 測試；104／Cake content script 與 Profile editor 的最終驗收由 Chrome 實機 gate 完成。

## 1. 自動化案例

| 編號 | 批次 | 測試情境 | 預期結果 |
|---|---|---|---|
| ET-01 | B4 | Options 儲存 loopback endpoint 與 token | 僅接受 loopback HTTP URL；token 存入 `chrome.storage.local`，不回顯於 dashboard |
| ET-02 | B4 | service worker 收到 Side Panel 的 API 請求 | 加入 Bearer token，回傳已標準化成功或錯誤結果 |
| ET-03 | B4 | Side Panel 四頁籤、Job 清單、判定篩選、四維對照、篩選逐條結論、待看清單與 Run 歷史 | 正確轉譯 API response；判定直接取用 `verdict`，不自行從 `process_state` 推導；Run stats 顯示 `key=value`；空值顯示「—」，錯誤有可理解訊息 |
| ET-21 | B4 | 推薦清單回應含下一頁 cursor，並在清單與目前職缺間往返 | 顯示「載入更多」並以相同篩選取得下一頁；追加且不重複既有 Job、不改變捲動位置；末頁隱藏按鈕；開啟職缺後標示「已看」，返回推薦頁恢復原位置；變更篩選或重新整理從第一頁開始 |
| ET-22 | B4 | 待看清單回應含下一頁 cursor，並在頁籤間往返 | 顯示「載入更多」並帶回 cursor 取得下一頁；追加且不重複既有 Job、不改變捲動位置；末頁隱藏按鈕；離開待看頁再返回時恢復原捲動位置 |
| ET-04 | B4 | 複製求職信成功與失敗 | 成功有文字確認且 clipboard 等於核准信件；失敗時信件仍可選取並顯示說明 |
| ET-05 | B4 | 投遞狀態與手動 run | 對應正確 API 路由與 payload；`already_running` 不重複送出 |
| ET-06 | B4 | 對照區依 `letter_state` 呈現求職信入口 | `none` 顯示產生按鈕、`requested` 顯示處理中且按鈕停用、`ready` 顯示信件與複製、`failed` 顯示未過審與再次產生 |
| ET-07 | B4 | 按下「產生求職信」 | 送出 `POST /jobs/{id}/letter` 一次並立即轉為處理中；連點不重複送出；失敗顯示可理解錯誤與可重送，不背景輪詢 |
| ET-08 | B4 | manifest 與 toolbar action | 宣告原生 Side Panel；toolbar action 開啟 `dashboard/index.html`，不宣告 popup |
| ET-09 | B4 | 淺色／深色主題切換 | 套用各自語意 token、保存 theme；目前頁籤、選取 Job 與 busy 狀態不被重設；320px 仍維持單欄可用 |
| ET-10 | B5 | list／job content script 送訊息 | 僅把目前已載入頁面的擷取素材交給 service worker；不直接讀取 token |
| ET-11 | B5 | 列表回應的就地標記 | 依 `verdict` 掛上對應標記：`unfit`／`recommended`／`not_recommended`／`pending_detail`／`pending_screen`／`pending_score` 各有可區分的專屬底色與外框，`unfit` 另附未通過條件、`recommended`／`not_recommended` 顯示總分；標記不只以顏色表達 |
| ET-47 | B5／B6 | 列表 capture 回應未涵蓋某筆 external_id | 該筆不被視為已處理；後續收割再問（同一份結果最多 3 次）並在取得判定後就地標記，不需重新整理文件 |
| ET-49 | B5／B6 | 使用者點開某筆內頁改變其判定後回到清單分頁 | 分頁轉為可見時清除判定快取並重新收割，就地更新為最新判定與命中；不需重新整理文件 |
| ET-12 | B5 | 列表回應含既有 Job 與新職缺混合 | 兩者以同一組 verdict 標記呈現；插件不因既有 Job 而重送 capture 或發起額外請求 |
| ET-13 | B5 | 內頁 capture context 與 Side Panel 判定 | 內頁不注入完整評分 overlay；content script 回報 Job ID 與擷取狀態；Side Panel 依 API 呈現 `unfit`／快取評分／`pending_screen`，並在輪詢取得語意篩選結果後改為 `unfit`（逐條原因）或 `pending_score` |
| ET-14 | B5 | capture API 失敗或離線 | 顯示可理解錯誤與使用者觸發的重送；不自動高頻重試、不暫存 JD |
| ET-15 | B5 | 內頁 capture 回 `pending_score` | Side Panel 顯示評分中並以 3 秒間隔輪詢 `GET /jobs/{id}`；verdict 轉終態後停止輪詢並呈現結果 |
| ET-16 | B5 | `pending_score` 持續達 5 分鐘上限 | 停止輪詢並顯示「仍在處理，可稍後重新整理」；不無限輪詢 |
| ET-17 | B5 | 內頁 capture 回 `pending_score` 且 `budget_exhausted` | 顯示「已達今日評分上限」而非處理中；不進入輪詢 |
| ET-18 | B5 | 搜尋頁虛擬捲動回收與載入新項目 | MutationObserver 收割新出現的項目；已收割項目不重送 capture；捲離回收不影響已送出的標記狀態 |
| ET-19 | B5 | 搜尋頁第一筆廣告職缺（`jobsource` 前綴 `hotjob`） | 不收割、不送 capture、不標記 |
| ET-20 | B5 | 搜尋頁與通知頁的同一職缺 | 兩套 selector 正規化為同一組 capture 項目（external_id、url、職稱、公司、地區、薪資一致） |
| ET-40 | B6 | Cake 列表頁 `__NEXT_DATA__` 與目前 URL 條件一致 | 由 `__NEXT_DATA__` 取得項目並送出帶 `source=cake` 的 capture payload；欄位與 104 路徑正規化為同一結構 |
| ET-41 | B6 | Cake 列表頁 URL 條件與 `__NEXT_DATA__` 的 `ssr.search` 不一致（使用者頁內改條件或翻頁） | 退回 DOM 收割並以 MutationObserver 涵蓋新注入項目；不使用過期的內嵌資料 |
| ET-48 | B6 | Cake 頁面在結果清單外（推薦／最近瀏覽區塊）另有連向同一職缺的連結 | 該筆的每個出現處都掛上標記；送出的欄位取自結果項目（公司名、地區、待遇齊備），不因非結果連結而缺欄位 |
| ET-42 | B6 | Cake DOM 收割時 class name 的 hash 段改變 | 仍以 `a[href^="/companies/"][href*="/jobs/"]` 與 `[class*="JobSearchItem"]` 前綴定位成功；不依賴完整 class 常數 |
| ET-43 | B6 | Cake 內頁擷取 | 以渲染後 DOM 收割職稱、公司名、JD 區塊與 metadata 行，送出 `source=cake` 的 capture 且不帶 `next_data`；Side Panel 依回應呈現，與 104 路徑共用同一套判定呈現規則 |
| ET-46 | B6 | 由 Cake 列表頁 soft navigation 進入職缺內頁（不重新載入文件） | 路由模組停掉列表模式並啟動內頁模式；page context 由 `list` 轉為 `job`，內頁擷取以新 URL 送出 |
| ET-44 | B6 | 目前職缺的 Job 回應帶多成員 `group` | 列出其他來源的平台與連結；評分與求職信只呈現一份；提供取消合併 |
| ET-45 | B6 | 系統頁「疑似重複」 | 併排顯示雙方職稱、公司、地區、來源與相似度；按下合併或忽略各送出一次對應請求並自清單移除；不自動裁決 |
| ET-30 | Profile | 系統頁 Profile 卡的 missing／invalid／ready | 顯示正確摘要與開始設定／編輯動作；invalid 不顯示敏感內容 |
| ET-31 | Profile | 全頁表單與動態陣列 | 六個區段依序呈現且所有 schema 欄位可編輯；陣列可新增、刪除、排序；新增後聚焦同區塊的新欄位且不跳至其他同型清單；鍵盤與錯誤聚焦可用 |
| ET-49 | Profile | 經歷列表與技能載入 | 年資加總為唯讀並隨經歷即時重算；按「從經歷載入」才把經歷技能補進技能總表（預設 `expert`、標示來源），已在表上者不重複帶入且熟練度不被改寫；`remote` 提供四值 |
| ET-51 | Profile | 地區欄位 | 以下拉選單提供台灣各直轄市與縣市＋台灣＋海外，選定值為地區鍵；已選過的地區不再出現在其他列的選項中 |
| ET-57 | Profile | 「＋ 全台」快捷鈕 | 一次填入海外以外的全部地區鍵（含「台灣」），已選的地區保留不重複、空白列被取代；不加入「海外」；全部已在清單時不再變更也不標記為已修改 |
| ET-52 | Profile | 資格三清單的排版 | 技能、證照與語言各為一項一列（名稱＋單一受控欄位），便於一眼核對是否有遺漏 |
| ET-50 | Profile | 巢狀清單的刪除與排序（經歷內的成就／技能、方向內的關鍵字） | 只影響該巢狀清單，外層經歷或方向的筆數與內容不變 |
| ET-51 | Profile | 受控列舉欄位（工作型態、學歷狀態、證照狀態、語言程度、學位、熟練度） | 一律以選單或複選呈現，不接受自由輸入；載入時顯示現值，未選時儲存前即提示 |
| ET-32 | Profile | 未儲存草稿離頁、reload 與無 autosave | 離頁先確認；reload 後草稿消失；未按儲存不送 PUT |
| ET-33 | Profile | 儲存確認與成功回饋 | 使用者確認「新職缺立即使用、舊評分保留」後才送含 If-Match 的 PUT；顯示 revision 短碼，不自動 reprocess |
| ET-34 | Profile | 412 conflict／422 validation／離線 | 保留草稿、顯示安全問題與重新載入；不提供強制覆蓋 |
| ET-35 | Profile | storage、log 與 service worker 路由 | Profile／草稿／API body 不進 storage 或 log；content script 不可讀取，所有 request 經 service worker |
| ET-36 | Profile | Job stale 標示與主題切換 | 分數只顯示整數；篩選與評分 revision 各以綠色最新／黃色待重評辨識，Letter stale 另提示；不改 verdict/apply，切換主題不清除 editor 狀態 |
| ET-37 | 系統 | 群組、Options、排程與手動 reprocess | 依連線、Profile、批次、進度、歷程排序；可開 Options；顯示每日 08:30；只有按下更新才 POST reprocess |
| ET-38 | 目前職缺 | `scored` 職缺按下「重新處理」 | 送出一次 `POST /api/v1/jobs/{id}/reprocess`；該筆立即顯示為篩選中且按鈕消失；判不適合的職缺同樣顯示此按鈕，處理中與信件階段職缺不顯示 |
| ET-53 | 目前職缺 | `pending_screen`／`pending_score` 職缺按下「馬上處理」 | 送出一次 `POST /api/v1/jobs/{id}/process`；該筆立即顯示為正在篩選／正在評分並開始輪詢；連點不重複送出；此狀態不顯示「重新處理」 |
| ET-54 | 目前職缺 | 自動處理已關閉或當日額度已用盡的等待中職缺 | 說明文字說出等待原因；「馬上處理」仍可用；未按下時不輪詢，按下後照常輪詢至終態或逾時 |
| ET-55 | 目前職缺 | `resident_worker` 為偽 | 「馬上處理」停用並說明該筆等待手動批次；不送出請求 |
| ET-56 | 系統 | 自動篩選與評分開關 | 依 `GET /api/v1/status` 的 `settings` 顯示開啟／關閉與說明；按下送出一次 `PUT /api/v1/settings`；成功後就地反映新狀態；`resident_worker` 為偽時停用 |
| ET-39 | 系統 | 處理進度與 Agent 呼叫紀錄 | 顯示各待處理狀態筆數（含待篩選與待看）與當日篩選、評分額度餘額；呼叫紀錄逐筆顯示 runner（有 model 時併列）與該次輸入／輸出 token，費用為 0 時不顯示金額；未完成呼叫顯示角色、耗時與失敗類別說明；低分的成功呼叫顯示為呼叫成功且不顯示失敗字樣；不輪詢 |
| ET-59 | 系統 | 「進行中」區塊 | 有 `in_flight` 或執行中批次時逐列顯示作業名稱、對象與已耗時；皆無時顯示空狀態；只在系統頁開啟且有作業在跑時每 5 秒重取，其餘情況不發請求 |
| ET-60 | 系統 | 批次歷程的用語與狀態 | 觸發方式、統計鍵與判定鍵皆為中文（手動（側邊欄）、抓取、推薦…）；顯示執行狀態與耗時；`stalled` 另附程序可能已結束的說明；不出現原始英文鍵或 `[object Object]` |
| ET-62 | B4 | 已產出求職信的職缺 | action dock 同時有「重新產製」與「複製求職信」；按下重新產製送出一次 `POST /jobs/{id}/letter`，該筆立即轉為處理中；產製歷程收起，下次展開重讀 |
| ET-63 | B4 | 產製歷程的重新讀取 | 每次展開都送出一次 `letter-history`；在歷程為空時展開過，之後跑完一次產製再展開，該次產製出現在清單上 |
| ET-61 | B4 | 求職信產製歷程的每一次產製 | 展開後逐輪呈現：每一輪為輪次、該輪信件全文與該輪結果（修改建議逐條列出／未通過保護規則／呼叫失敗／審核通過。／輪數用完，直接定稿。）；信件是本文而非契約 JSON；逐輪之後以「這次產出的信件」收尾，該信件與最後一輪的草稿不同時另標示「審查時經過編輯」；已達輪數上限的產製不重複顯示該標籤；外層區塊與每一次產製的收合箭頭各自隨自己的展開狀態轉向 |
| ET-58 | 系統 | 每日 Token 用量 | 依 `agent_usage_daily` 逐日分卡（新到舊），卡首為日期與當日 token 合計、卡內每列為一組 runner／model 的呼叫次數與輸入／輸出 token；費用為 0 的列不顯示金額；回應為空陣列時顯示空狀態而非 0 |

測資不得含真實 JD、Profile、token 或任何來源平台的真實頁面內容。

## 2. Chrome 實機 gate

- B4 由驗收者以與自動驗收相同的 unpacked extension artifact 載入實際 Chrome，確認 toolbar action 開啟原生 Side Panel，完成 Options 設定、淺深色切換、四頁籤、對照、產生求職信、copy、apply、manual run 與 Run history；自動隔離 Chromium 結果不得替代。
- 在使用者自行開啟的 104 搜尋頁與通知頁確認列表收割與就地標記：不適合者當場可辨識、既有職缺直接顯示既有判定；不由插件開分頁或觸發背景導覽。
- 在使用者自行開啟的 Cake 搜尋／職類頁與職缺內頁完成同一組確認；`__NEXT_DATA__` 與 DOM 兩條路徑都要實際走過（直接開啟帶條件 URL、以及在頁內改條件後）。
- 在使用者自行點開的 104 職缺頁確認內頁擷取，Side Panel「目前職缺」顯示判定／四維分數／篩選逐條結論／快取；104 頁面不出現第二套完整評分 overlay。
- 在 Side Panel 確認清單、判定、對照、求職信生成與複製、投遞狀態、手動 run、Run 歷史與待看清單。
- 將人工檢查結果與不含敏感內容的證據記入 `.local-dev/verify/`。
- 由系統頁進入全頁 Profile editor，核對窄幅入口、六區段完整表單、經歷列表與技能載入按鈕、唯讀年資加總、地區下拉選單、遠端四值、動態新增定位、鍵盤操作、離頁提醒、錯誤聚焦、ETag 衝突、手動更新過時評分與 light／dark 主題；不得載入實際使用者 Profile。
- 在 Side Panel 確認六類判定的呈現：不適合附未通過條件、待看附原始連結、推薦／不推薦附四維與總分、篩選中與評分中各自顯示處理中。
