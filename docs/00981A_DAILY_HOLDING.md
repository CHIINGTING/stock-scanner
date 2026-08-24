# 00981A 每日官方持股抓取

抓取 **00981A 主動統一台股增長 ETF**（fundCode `49YTW`）的**每日官方持股 / PCF（投資組合明細）**，
轉成 CSV，供後續 `cmd/etfflow-ingest` 匯入成 R8 ETF Flow snapshot。

> 本功能只做 **00981A**，不擴充其他 ETF。

---

## 1. 為什麼不用 WantGoo 當每日來源

WantGoo 的 ETF 成分股是**季度（季配/季更）資料**，更新頻率不足以反映「每日」持股變化。R8 ETF Flow
需要每日 delta（連續加碼 / 減碼、賣壓比），季度資料無法支撐，故**不採用 WantGoo 作為每日來源**。

## 2. 為什麼不用 MoneyDJ 當完整每日來源

MoneyDJ 對 00981A 目前只抓得到**部分**持股（覆蓋不完整），主動式 ETF 的成分股若缺漏，會使
delta / 權重計算失真。故**不以 MoneyDJ 作為完整每日來源**（最多只能當交叉比對）。

## 3. 正式資料來源：統一投信官方 ezMoney

主動式 ETF 依規定每日揭露 PCF（申購買回清單）/ 投資組合明細，由發行商官方公布：

```
https://www.ezmoney.com.tw/ETF/Transaction/PCF?fundCode=49YTW
```

這是**每日、官方、完整**的資料，作為唯一主來源。

> 揭露延遲：官方持股通常於交易日當天 16:30 後才更新（T 日資料 T 日傍晚或 T+1 公布）。
> 這也正是 R8 report ⑨「延遲揭露資料，不代表盤中即時買賣」的由來。

---

## 4. 安裝

```bash
# Node + Playwright（瀏覽器自動化下載官方檔案）
npm install playwright
npx playwright install chromium

# Python 端 XLSX -> CSV
python3 -m pip install openpyxl
```

> macOS 可直接執行；首次 `npx playwright install chromium` 會下載 Chromium。

---

## 5. 手動執行

```bash
# 抓最新可下載資料 + 轉 CSV
bash scripts/fetch_00981a_daily.sh
```

下載的官方檔存到 `data/official/`，檔名格式：

```
00981A_official_YYYYMMDD_<原始檔名>
```

轉出的 CSV 存到 `data/official/csv/`（UTF-8 with BOM，每個工作表一個 CSV）。
執行 log 寫到 `logs/fetch_00981a_daily.log`。

### 指定日期

```bash
# 只跑 Playwright 下載（指定日期）
node scripts/fetch_00981a_daily_official.mjs 2026/06/12

# 或整合腳本帶日期
bash scripts/fetch_00981a_daily.sh 2026/06/12
```

### 單獨轉換某個 XLSX

```bash
python3 scripts/xlsx_to_csv.py data/official/00981A_official_20260612_<原始檔名>.xlsx
```

---

## 6. 找不到下載按鈕時（debug）

官方頁面為 JS 動態渲染，下載元素的選擇器可能隨改版變動。若 `fetch_00981a_daily_official.mjs`
找不到下載按鈕 / 檔案連結，它會：

1. 在 stdout 印出頁面所有 `button` / `input` / `a` 的文字、value、href、id、name。
2. 存全頁截圖到 `data/official/00981A_debug.png`。
3. 以 exit code `2` 結束。

把上述控制項傾印與截圖回報，即可據此調整 `DOWNLOAD_TEXT_RE` / `findDownloadTrigger` 的選擇器。

---

## 7. cron 範例

官方每日資料通常在 **16:30 後**更新，建議 16:40 抓（週一至週五）：

```cron
40 16 * * 1-5 cd /Users/deep.huang/stock-scanner && bash scripts/fetch_00981a_daily.sh >> logs/fetch_00981a_daily.log 2>&1
```

---

## 8. 後續：轉成 R8 snapshot

CSV 取得後，**對應欄位**（`stock_code, stock_name, holding_shares, holding_raw_unit, holding_weight`）
再用 manual ingest CLI 匯入成 R8 snapshot：

```bash
go run ./cmd/etfflow-ingest \
  --etf-code 00981A --etf-name "主動統一台股增長 ETF" --etf-type active \
  --date 2026-06-12 --input data/official/csv/<對應好的>.csv \
  --snapshot-dir data/etf_holdings --source-url "ezMoney official PCF"
```

> 官方 XLSX 的欄位/表頭與我們的 canonical CSV 未必一致，通常需要一個對應步驟（挑出代號/股數/權重欄、
> 補上 `holding_raw_unit`）。對應規則待看到實際官方檔內容後再定。

---

## 9. 產物不進版控

`data/official/`（官方 XLSX/XLS、轉出的 CSV、debug 截圖）與 `logs/` 屬資料/執行產物，**不要 commit**。
已於 `.gitignore` 忽略。
