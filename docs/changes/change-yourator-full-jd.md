# Yourator 完整 JD 擷取

## 背景／動機

Yourator 職缺頁的工作內容、條件要求與加分條件位於同一個外層 `section.job-description`，其中各內容區段另有巢狀 `section`。只擷取第一個結束標籤會遺失條件要求與加分條件，使本地條件篩選與 Agent 評分缺少關鍵技能要求。

## 決策摘要

- `RawJob.description` 保存 Yourator 外層 `section.job-description` 的完整純文字內容，包含工作內容、條件要求、遠端型態、加分條件與頁面提供的其他職缺資訊。
- 解析器以巢狀 HTML section 的平衡邊界尋找完整外層容器，不以第一個 `</section>` 作為結束位置。
- 純文字保留區段標題與換行，使本地規則與 Agent 能辨識各項要求的語意邊界。
- 不新增資料庫欄位；既有內容雜湊、條件篩選、Scorer 與求職信流程繼續消費同一個 `description`。

## 相對舊狀態的差異

| 主題 | 舊狀態 | 最新狀態 |
|---|---|---|
| HTML 邊界 | 在第一個巢狀 `</section>` 停止 | 取得完整外層 `section.job-description` |
| JD 內容 | 通常只有工作內容 | 包含條件要求與加分條件等完整區段 |
| 純文字結構 | 所有空白壓成單一空格 | 保留標題與段落換行 |
| 下游契約 | `description` 供規則與 Agent 使用 | 維持同一欄位，不需 schema migration |

## Canonical 落點

| 文件 | 最新狀態 |
|---|---|
| [Crawler 設計](../designs/design-crawler.md) | Yourator 完整 JD 的 HTML 邊界與純文字映射 |
| [Crawler 測試](../tests/test-crawler.md) | 巢狀 section 與多個 JD 區段的解析案例 |

## 交付狀態

| 項目 | 狀態 | 完成條件 |
|---|---|---|
| Yourator 完整 JD 解析 | 已完成 | 工作內容、條件要求與加分條件均進入 `description` |
| 單元測試 | 已完成 | 巢狀區段、標題、換行與缺少容器案例通過 |

## 已知殘留限制

- Yourator 若移除 `job-description` class 或改為非 HTML 職缺資料來源，該筆會保留為 partial，需依公開頁新契約更新解析器。
