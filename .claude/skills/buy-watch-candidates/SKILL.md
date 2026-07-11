---
name: buy-watch-candidates
description: Merge today's BUY & WATCH stocks from the daily report's 市場掃描 (market-scan) tab into stocks.yaml's watchlist. Use when the user asks to extract / 建立 / 更新 today's report's BUY & WATCH stocks into stocks.yaml.
---

# BUY & WATCH → stocks.yaml watchlist

從當天報告 `reports/report_YYYYMMDD.html` 的**市場掃描分頁** (`<div id="tab-market">`)
抽出 **BUY 與 WATCH** 的個股，直接**併入 `stocks.yaml` 的 `watchlist:`**。

市場掃描分頁會列出每一檔掃描到的股票及其交易建議（STRONG BUY / BUY / WATCH /
HOLD / REDUCE / SELL）。本 skill 只保留可操作的訊號：

- `STRONG BUY` / `BUY` / `WATCH` → 全部併入 `stocks.yaml` 的 `watchlist:`
- 其餘（HOLD / REDUCE / SELL）忽略

`stocks.yaml` 沒有 `buy:` / `watch:` 層級，只有 `positions:` 與 `watchlist:`，
所以 BUY 與 WATCH 一律進 `watchlist:`（BUY 排在 WATCH 前面）。

只看市場掃描分頁，不含持倉、飆股候選、輪動分頁。

## 併入規則（去重、冪等）

- 已在 `positions:`（持股）的 code → **跳過**，不重複放進觀察清單
- 已在 `watchlist:` 的 code → **跳過**
- 其餘新 code → **附加到 watchlist 尾端**
- `positions:`、註解、既有 watchlist 順序全部保留；重複執行同一份報告不會改動檔案

## 執行

報告通常上千列、檔案可達數 MB，**不要**用肉眼讀 HTML — 一律跑解析腳本：

```bash
python3 .claude/skills/buy-watch-candidates/extract.py [日期或報告路徑]
```

- 不帶參數 → 用今天的 `report_YYYYMMDD.html`，若無則用最新的 `report_*.html`
- 帶 `20260609` → 用 `reports/report_20260609.html`
- 帶完整路徑 → 用該檔

腳本會就地更新 `reports/../stocks.yaml`（專案根目錄）並印出：來源報告、掃描到的
BUY / WATCH 檔數、實際新增與跳過的檔數，以及新增清單。

跑完後，把來源報告日期、新增檔數、跳過檔數回報給使用者；若 BUY+WATCH 掃描結果為 0，
提醒可能報告是空的（例如重跑時市場掃描無資料）。新增 0 檔通常代表今天的候選都已在
清單或持股中，屬正常。

## 中文股名補正

報告（及資料快取）對部分個股存的是英文名（例：3290 → "DONPON PRECISION INC"）。
`extract.py` 會載入 `data/stock_names_zh.yaml`（code → 官方中文名對照），在**報告股名
不含中文時**改用中文名；已是中文的不動，查不到的維持原樣。對照檔缺檔則略過、不影響執行。

對照檔由官方 TWSE 上市 + TPEX 上櫃 ISIN 清單產生，要刷新時重跑：

```bash
python3 .claude/skills/buy-watch-candidates/build_name_map.py
```

會連外網抓清單、寫出 `data/stock_names_zh.yaml`（約 1980 檔，只收 4 碼，排除權證）。

## 併入後的 stocks.yaml（示意）

```yaml
watchlist:
  - code: "3176"
    name: "基亞"
  # …既有項目保留…
  - code: "3022"      # ← 今天報告新增的 BUY/WATCH
    name: "威強電"
```
