# 變更 — 硬規則篩選與軟規則評分分離

## 1. 背景與動機

現行判定把所有依據壓成單一次 LLM 呼叫：整份 Profile YAML（含履歷敘事）加 JD 餵給 Scorer，回五維分數（`hard_skill`／`domain`／`seniority`／`condition`／`direction`），Go 加權後與門檻比較，過門檻即推薦。實際使用後有三個結構性問題：

- **JD 的條件結構被壓平**：JD 寫「A 或 B」時被當成「A 且 B」計分，必要條件與加分條件也不分，有機會的職缺被系統性低估。
- **履歷敘事污染適配判斷**：`experiences` 的成就敘事會被讀成「擅長 ⇒ 適配高」，使用者「做過但不想再做」的工作內容只會拉高分而不會扣分；Profile 完全沒有反感內容的欄位。
- **硬性條件被當成軟分**：`condition` 佔權重 0.20，地點或薪資不符只是扣分而非判定不適合，硬性不符的職缺混進推薦清單、稀釋分數的意義。
- **年資語意錯誤**：JD 的「N 年以上」是過濾無經驗者的門檻，現行評分卻把高年資者對低年資要求判為不適合。

## 2. 決策摘要

| 項目 | 決策 |
|---|---|
| 判定結構 | 拆成兩關：**篩選**（硬規則）決定適合與否、**評分**（軟規則）決定推不推薦；兩關分離呼叫，不合併 |
| 硬規則落點 | 結構化條件由程式比對；語意條件（學歷、必備技能、產業對應）由 LLM 篩選關處理 |
| 年資與加總 | 一律程式計算。LLM 只做語意對應（JD 要求對應到哪些 `industry`），數值加總與比較由 Go 做 |
| 資訊不足 | 缺資訊**絕不判不適合**。清單摘要缺欄位判 `unknown` 歸「待看」；全文 JD 未載明該事實則視為定論並放行，否則有全文的職缺會永遠卡在待看。`remote` 不適用此分野——未提及遠端即等於現場，是定論不是缺資訊 |
| 評分輸入 | 只餵 `intents` 與判定所需的 `qualifications` 子集，**不餵履歷敘事** |
| 評分計分 | 基準分制：以門檻分為起點加減，clamp 0–100；無資訊可判時回基準分（中性） |
| Revision | 拆成 `filter_revision` 與 `score_revision`，各自只 hash 自己涵蓋的欄位 |
| 既有資料 | 全部職缺重置回未處理，先重跑篩選、再排程評分通過者 |

兩關分離而非合併成一次呼叫，是為了讓不合格的職缺不進入完整評分（省 token），並使 Profile 的硬／軟設定在使用者心智上對應到兩個獨立結果。

## 3. Profile Schema

Profile schema 升至 v4。表單與 YAML 的 section 順序：`search` → `requirements` → `intents` → `experiences` → `qualifications` → `honesty_bounds`。

### 3.1 `search`（只用於搜尋）

| 欄位 | 說明 |
|---|---|
| `directions[]` | `key`／`title`／`keywords[]` |
| `locations[]` | 搜尋用地區參數 |

與 `requirements.locations` 刻意分開：搜尋撒廣、篩選收緊是兩件事。不參與篩選、不參與評分、不進任何 revision。

### 3.2 `requirements`（硬規則）

| 欄位 | 型別 | 用途 |
|---|---|---|
| `salary_min` | int | JD 薪資上限低於此 → 不適合；清單未揭露 → 待看；全文 JD 未揭露 → 通過 |
| `locations[]` | string[] | 通勤可接受的地區 |
| `remote` | enum | `required`／`preferred`／`acceptable`／`rejected` |
| `employment_types[]` | string[] | 全職／兼職／約聘 |
| `industry_avoid[]` | string[] | 排除產業 |
| `exclude_title_keywords[]` | string[] | 標題排除關鍵字 |
| `exclude_description_keywords[]` | string[] | 內文排除關鍵字 |
| `exclude_companies[]` | string[] | 排除公司 |

`remote` 同時是篩選條件與 `benefit_fit` 的加分依據，故兩組 revision 都納入。

### 3.3 `intents`（軟規則）

| 欄位 | 型別 | 用途 |
|---|---|---|
| `salary_target` | int | `benefit_fit`：JD 薪資高於此則加分 |
| `content_likes[]` | string[] | `content_fit` 加分依據，條列敘事 |
| `content_dislikes[]` | string[] | `content_fit` 扣分依據，條列敘事 |
| `industry_interests[]` | string[] | `industry_fit`：對未來發展的期許 |

`industry_interests` 與 `requirements.industry_avoid` 語意不同：後者是硬排除，前者是領域發展偏好，不符只是分數較低。

### 3.4 `experiences`

| 欄位 | 型別 | 必填 | 用途 |
|---|---|:---:|---|
| `industry` | string | ✔ | 硬規則：產業經驗；建議取自受控清單，可自訂補充 |
| `years` | float | ✔ | 硬規則：年資加總 |
| `is_management` | bool | ✔ | 硬規則：管理年資加總。使用者勾選，不由 LLM 從 `role` 文字推測 |
| `exclude_from_totals` | bool | | 實習或非相關經歷不計入加總 |
| `skills[]` | string[] | | 硬規則：必備技能來源；guard 白名單來源。只填名稱，熟練度不在此標 |
| `org_type` | string | | Drafter 取材 |
| `role` | string | | Drafter 取材 |
| `achievements[]` | string[] | | Drafter 取材，需帶量化數據 |

一筆經歷同時服務篩選關與寫信，填寫邏輯即「一份工作一列」。

### 3.5 `qualifications`

| 欄位 | 型別 | 用途 |
|---|---|---|
| `education[]` | `{ level, field, status }` | 硬規則：學歷比對 |
| `skills[]` | `{ name, level }` | 硬規則：必備技能；軟規則：`bonus_fit` |
| `certifications[]` | `{ name, status }` | 硬規則：必要證照；軟規則：`bonus_fit` |
| `languages[]` | `{ name, level }` | 硬規則：必要語言；軟規則：`bonus_fit` |

`skills[]` 是技能總表，`level` 為 `expert`／`proficient`／`familiar`。編輯時由 `experiences[].skills[]` 聯集自動帶入（預設 `proficient`），使用者只需調整熟練度並補上工作外取得的技能；帶入項在 UI 標示來源，刪除經歷時提示對應技能是否保留。熟練度只在此填一次。

### 3.6 `honesty_bounds`

條列字串，Drafter 與 Reviewer 的防幻覺依據，不進任何 revision。

### 3.7 `derived`（程式物化，不可編輯）

Profile 儲存時由 Go 從 `experiences[]` 計算並寫入，供篩選關做數值比較：

| 欄位 | 計算 |
|---|---|
| `total_years` | `exclude_from_totals` 為否的 `years` 加總 |
| `management_years` | 同上且 `is_management` 為真的 `years` 加總 |
| `industry_years` | 以 `industry` 分組的 `years` 加總 |

使用者不再填 `years_of_experience`，避免與經歷列表兩處不一致。

### 3.8 移除的欄位

`summary`、`years_of_experience`、`preferences.directions`（移入 `search`）、`preferences.screening.require_any_keywords`、獨立的 `industries[]`（併入 `experiences[]`）。

`summary` 移除的理由：Drafter 本就讀 `experiences` 細節，`summary` 是同一批事實的壓縮版，只增加填寫負擔與兩處不一致風險；求職動機面由 `content_likes` 與 `industry_interests` 承載且更精準。`require_any_keywords` 的功能由 `search.directions` 與軟規則評分取代，留著只會誤殺。

## 4. Revision 模型

`profile_revision` 由單一值拆成兩個，各自只 hash 涵蓋的欄位。

| revision | 納入 hash 的欄位 | 變更後的重跑範圍 |
|---|---|---|
| `filter_revision` | `requirements` 全部；`qualifications` 全部；`experiences[]` 的 `industry`／`years`／`is_management`／`exclude_from_totals`／`skills[]` | 全部重篩，通過者再重評 |
| `score_revision` | `intents` 全部；`requirements.remote`／`requirements.locations[]`；`qualifications.skills[]`／`certifications[]`／`languages[]` | 只重評，不重篩 |

不進任何 hash：`search` 全部、`experiences[]` 的 `org_type`／`role`／`achievements[]`、`honesty_bounds`、`derived`（由來源欄位決定）。

`requirements.remote`、`requirements.locations[]` 與 `qualifications` 的三個清單同時進兩組 hash——它們既是篩選條件也是加分依據，改動時兩關都要重跑。這是欄位跨關共用的必然代價。

檔案 ETag 仍以整份 canonical YAML 計算，與 revision 無關；`search` 或 `achievements` 的修改會改變 ETag 但不觸發任何重跑。

## 5. 第一關：篩選（硬規則）

逐條比對，每條結果為 `pass`／`fail`／`unknown`。

| 條件 | 判定方式 |
|---|---|
| 地點、`remote`、`employment_types`、`salary_min`、`industry_avoid`、`exclude_*` | 程式比對 `requirements` |
| 學歷／科系、必備技能、必要證照、必要語言 | LLM 比對 `qualifications` |
| 年資、管理年資、必要產業經驗 | LLM 只做語意對應，Go 用 `derived` 做加總與比較 |

彙總：任一 `fail` → **不適合**；無 `fail` 但有 `unknown` → **待看**；全 `pass` → **待評分**。

### 5.1 條件拆解

篩選關先產出 JD 的條件拆解，每條標記為**必備**或**加分**，並標示選言關係。此拆解隨判定結果一併保存，後續由評分關的 `bonus_fit` 重用，兩關不各自重解一次。

- 選言（「A 或 B」）滿足任一即 `pass`。
- 標為加分的條件不列入篩選，只進 `bonus_fit`。

### 5.2 年資

JD 的「N 年以上」是**下限**：`derived.total_years >= N` 即 `pass`，年資超出**不扣分、不判不適合**。只有 JD 明確設上限（如「限 3 年以下」）才可能 `fail`。管理經驗同理，比對 `derived.management_years`。

產業經驗由 LLM 回答「JD 要求對應到 profile 的哪些 `industry` key、要求幾年」，Go 取 `derived.industry_years` 相加後比較。

### 5.3 學歷

JD 的要求必須被**同一筆學歷同時滿足**：存在某筆 `level >= 要求級別` 且 `field` 與要求科系相容，才 `pass`。

例：profile 為 `[碩士·機械, 學士·資工]`——JD 要求「大學資工」→ `pass`（學士·資工 同時滿足）；JD 要求「碩士資工」→ `fail`（無單筆同時滿足）；JD 要求「碩士不限科系」→ `pass`。

科系相容性需語意判斷，故整條歸 LLM。

### 5.4 遠端

| `remote` | 篩選 | `benefit_fit` |
|---|---|---|
| `required` | 只有 JD 明寫全遠端才通過；`hybrid`／`onsite`／未提及 → 不適合 | `full` 加分 |
| `preferred` | 全部通過 | `full` 加較多、`hybrid` 加較少、`onsite` 不加 |
| `acceptable` | 全部通過 | 不加不減 |
| `rejected` | JD 提及全遠端或部分遠端 → 不適合；`onsite` 與未提及皆通過 | 不加不減 |

JD 未提及遠端即等於現場：`required` 與 `rejected` 都據此定案，此條永遠不會是 `unknown`。

## 6. 第二關：評分（軟規則）

只有「待評分」的職缺進入。基準分制：各維度以門檻分為起點加減，clamp 0–100；無資訊可判時回基準分。

| 維度 | 權重 | 對照 JD | 參考 Profile |
|---|---|---|---|
| `content_fit` | 0.50 | 工作內容描述 | `content_likes`（加）／`content_dislikes`（減） |
| `benefit_fit` | 0.20 | 薪資、休假、獎金、遠端、工時制度 | `salary_target`、`remote`；優於勞基法的休假、不打卡／彈性工時、額外獎金皆加分 |
| `bonus_fit` | 0.15 | 加分條件（來自 §5.1 拆解） | `qualifications` 的 `skills`／`certifications`／`languages`、`experiences[].industry` |
| `industry_fit` | 0.15 | 公司產品／服務所屬領域 | `industry_interests` |

`bonus_fit` **只加不減**：JD 的加分條件未滿足不應懲罰，否則列越多加分項的 JD 分數越低，方向就反了。

加權總分過門檻 → **推薦**，否則 **不推薦**。

評分關的 prompt **不餵** `experiences[].achievements`／`org_type`／`role` 與 `honesty_bounds`——切斷履歷敘事對適配判斷的污染是本次變更的核心動作。

### 6.1 不做的維度

`culture_fit`（公司制度、管理方式、工作環境）本次不實作，`company_likes`／`company_dislikes` 欄位也不加。JD 幾乎不揭露這類資訊，實作後只會恆回基準分，效果是稀釋其他維度的區辨力並讓使用者多填無作用的欄位。待有公司情報來源時再一併加入。

## 7. 資料模型與狀態

### 7.1 `scores`

五個維度欄位換成四個：`dim_hard_skill`／`dim_domain`／`dim_seniority`／`dim_condition`／`dim_direction` → `dim_content`／`dim_benefit`／`dim_bonus`／`dim_industry`。`total`、`reason`、`runner` 不變；`profile_revision` 換成 `score_revision`。

### 7.2 篩選結果

新增保存篩選關的逐條判定與 JD 條件拆解（含每條的 `pass`／`fail`／`unknown` 與必備／加分標記），供 UI 呈現「為什麼判不適合」與評分關重用，並記錄 `filter_revision`。

### 7.3 狀態機

彙總不新增狀態，只補一條轉換：篩選時發現該筆其實沒有 JD 全文者退回 `discovered`。

| 篩選彙總 | 只有摘要 | 有 JD 全文 |
|---|---|---|
| 任一 `fail` | `filtered_out`（不適合） | `filtered_out`（不適合） |
| 無 `fail`、有 `unknown` | 留 `discovered`（待看，等補全文） | `queued`（待評分） |
| 全 `pass` | 留 `discovered` | `queued`（待評分） |

判定由五類擴為六類，因為 `new` 與 `queued` 對使用者是兩件事：前者篩選未完成、後者已通過篩選正等待評分。判定名稱說的是系統正在對這筆職缺做什麼。

| verdict | process_state | 顯示 |
|---|---|---|
| `unfit` | `filtered_out` | 不適合 |
| `pending_detail` | `discovered` | 待看 |
| `pending_screen` | `new` | 篩選中 |
| `pending_score` | `queued` | 評分中 |
| `not_recommended` | `scored` | 不推薦 |
| `recommended` | `shortlisted`／`letter_*` | 推薦 |

`new → discovered` 需加入 `validProcessTransition`：篩選時若發現該筆只有摘要，退回待看清單補全文，而不是把摘要送進評分。

`internal/api/viewmodel.go` 新增 `pending_screen` 常數並改寫 `verdictByState`：`new` 與 `queued` 各自對應一類，前端徽章、判定下拉與 content script 標記一併改用「篩選中／評分中」的動作式用語。

### 7.4 既有資料

Profile schema v4 migration 後，**全部既有職缺重置回未處理**（`filtered_out`／`scored`／`shortlisted` 等一律回到篩選前狀態），既有 `scores` 資料失效。之後由篩選關重跑一次，通過者再由評分關排程消化。已有求職信與投遞歷史不得因此改寫或刪除（沿用 `docs/designs/design-profile.md` 的既有約束）。

## 8. 影響範圍

| 模組 | 變更 |
|---|---|
| `internal/profile/` | schema v4 與 migration、section 重組、`derived` 計算、雙 revision hash、canonical serialization、PII 檢核範圍調整 |
| `internal/pipeline/` | `Filter` 由關鍵字擴充為完整硬規則比對；新增 LLM 篩選階段與新狀態的取件；評分階段權重改四維；雙 revision 的 CAS 與重跑範圍 |
| `internal/agents/` | 新增 Filter agent（條件拆解＋語意比對）與其輸出契約；Scorer prompt 全面改寫、`ScoreResult` 換四維；`ClassifyFailure` 補新失敗型別；Drafter／Reviewer 的 Profile 輸入改取 `experiences`＋`honesty_bounds` 子集；guard 白名單來源改為 `qualifications.skills` ∪ `experiences[].skills` |
| `internal/store/` | schema v6：`scores` 維度欄位、篩選結果保存、雙 revision 欄位、新 process_state 與轉換表、既有職缺重置 migration |
| `internal/api/` | Profile GET／PUT 的新結構與雙 ETag／revision 語意；`verdictByState` 擴充；Job viewmodel 帶出篩選逐條結果；`/api/v1/status` 補篩選階段筆數 |
| `extension/` | Profile editor 表單依新 section 與順序全面改寫（技能帶入、經歷列表、遠端四值）；清單顯示篩選未通過原因 |
| `configs/` | `scoring.*` 權重鍵換成四維並加基準分設定 |
| `docs/` | `PRD.md` R4；`design.md`；`designs/` 的 profile／pipeline／agents／schema／api／extension；`tests/` 對應測試規格；`verify.md` 新案例 |

## 9. 測試與驗收

- **L1**：`derived` 加總（含 `exclude_from_totals`、管理年資）、學歷同筆比對的正反例、遠端四值 × JD 三態的矩陣、選言條件、`bonus_fit` 只加不減、雙 revision 的 hash 涵蓋範圍（改 `search`／`achievements` 不變、改 `intents` 只變 `score_revision`）。
- **L2／e2e-mock**：篩選三分流與新狀態轉換、評分只取「待評分」、Profile 改 `intents` 後只重評不重篩、既有職缺重置後的完整重跑。
- **人工 gate**：Profile editor 新表單的實際 Chrome 操作；六類 verdict 在 Side Panel 的呈現與篩選未通過原因。

## 10. 待實作進度

canonical 文件已更新（`PRD.md` 新增 B7 與改寫 R1／R3／R4、`design.md`、`designs/` 六份模組設計、`tests/` 五份測試規格、`verify.md` 新增 V8、`AGENTS.md`）。程式與驗收 harness 未開始。

## 11. 已知殘留限制

- `culture_fit` 與公司情報未納入（§6.1）。
- `experiences[].industry` 的受控清單初版以常見產業為主，罕見產業需使用者自訂，語意對應品質取決於 LLM。
- 既有職缺全部重評有一次性 token 成本，與每日評分預算共用額度，消化需數日。
