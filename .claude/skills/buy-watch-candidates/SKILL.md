---
name: buy-watch-candidates
description: Build today's BUY & WATCH candidate list from the daily report's 市場掃描 (market-scan) tab. Use when the user asks to extract / 建立 today's report's BUY & WATCH stocks into the candidates_YYYYMMDD.yaml format (code + name).
---

# BUY & WATCH 候選清單

從當天報告 `reports/report_YYYYMMDD.html` 的**市場掃描分頁** (`<div id="tab-market">`)
抽出 **BUY 與 WATCH** 的個股，輸出成 `reports/candidates_YYYYMMDD.yaml`。

市場掃描分頁會列出每一檔掃描到的股票及其交易建議（STRONG BUY / BUY / WATCH /
HOLD / REDUCE / SELL）。本 skill 只保留可操作的訊號：

- `STRONG BUY` 與 `BUY` → `buy:` 群組
- `WATCH` → `watch:` 群組
- 其餘（HOLD / REDUCE / SELL）忽略

只看市場掃描分頁，不含持倉、飆股候選、輪動分頁。

## 執行

報告通常上千列、檔案可達數 MB，**不要**用肉眼讀 HTML — 一律跑解析腳本：

```bash
python3 .claude/skills/buy-watch-candidates/extract.py [日期或報告路徑]
```

- 不帶參數 → 用今天的 `report_YYYYMMDD.html`，若無則用最新的 `report_*.html`
- 帶 `20260609` → 用 `reports/report_20260609.html`
- 帶完整路徑 → 用該檔

腳本會寫出 `reports/candidates_YYYYMMDD.yaml` 並印出 BUY / WATCH 檔數。

跑完後，把來源報告日期與 BUY / WATCH 檔數回報給使用者；若 BUY 為 0 或檔數異常，
提醒可能報告是空的（例如重跑時市場掃描無資料）。

## 中文股名補正

報告（及資料快取）對部分個股存的是英文名（例：3290 → "DONPON PRECISION INC"）。
`extract.py` 會載入 `data/stock_names_zh.yaml`（code → 官方中文名對照），在**報告股名
不含中文時**改用中文名；已是中文的不動，查不到的維持原樣。對照檔缺檔則略過、不影響執行。

對照檔由官方 TWSE 上市 + TPEX 上櫃 ISIN 清單產生，要刷新時重跑：

```bash
python3 .claude/skills/buy-watch-candidates/build_name_map.py
```

會連外網抓清單、寫出 `data/stock_names_zh.yaml`（約 1980 檔，只收 4 碼，排除權證）。

## 輸出格式

```yaml
# 飆股候選清單 — 市場掃描 BUY & WATCH
# 來源：report_20260609.html（市場掃描分頁）
# 產生日期：2026-06-09
meta:
  report_date: 2026-06-09
  source: report_20260609.html
  section: 市場掃描 (tab-market)
  total_candidates: 62
  buy_count: 6
  watch_count: 56

# === BUY：訊號成立，可考慮進場 ===
buy:
  - code: "3022"
    name: "威強電"

# === WATCH：觀察中，等待進場訊號確認 ===
watch:
  - code: "2547"
    name: "日勝生"
```
