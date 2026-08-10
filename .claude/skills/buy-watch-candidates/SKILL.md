---
name: buy-watch-candidates
description: Rebuild stocks.yaml's watchlist from today's BUY / WATCH / HOLD stocks in the daily report's 市場掃描 (market-scan) tab. Replaces the machine watchlist wholesale; never touches positions: or watchlist_pinned:. Use when the user asks to extract / 建立 / 更新 / 重建 today's report's BUY & WATCH stocks into stocks.yaml.
---

# BUY / WATCH / HOLD → stocks.yaml watchlist

從當天報告 `reports/report_YYYYMMDD.html` 的**市場掃描分頁** (`<div id="tab-market">`)
抽出 **BUY、WATCH 與 HOLD** 的個股，**重建**（不是附加）`stocks.yaml` 的 `watchlist:`。

## 重建，不是附加

`watchlist:` 每次都被今日的候選整批取代。原本的 append-only 讓它變成單向棘輪——
累積到 **748 檔**，而單日訊號只有 **66 檔**，等於 92% 是早已不合格的歷史沉積。
翻不完的候選清單等於沒有清單。

## 三個區塊的所有權不對稱

| 區塊 | 誰擁有 | 本 skill 的行為 |
| --- | --- | --- |
| `positions:` | **只有你** | 永不寫入；其代號也不會進 `watchlist:`（同一檔不會被列兩次） |
| `watchlist_pinned:` | **只有你** | 永不寫入；每次重建都完整保留 |
| `watchlist:` | 機器 | 整批取代為今日 BUY + WATCH + HOLD |

自己從股癌、研究或直覺想追蹤的標的，放 `watchlist_pinned:`——那是唯一不會被重建洗掉的地方。
scanner 會把它與 `watchlist:` 合併後一起抓取分析，釘選的標的照樣出現在飆股候選分頁。

## 安全規則

- **候選為 0 時不動檔案**。掃描沒產出候選與掃描失敗在訊號上分不出來，在有歧義時清空
  清單是破壞性的。
- **同報告重跑不產生 diff**，內容相同就不寫入。
- 檔頭註解與 key 順序保留。

市場掃描分頁會列出每一檔掃描到的股票及其交易建議（STRONG BUY / BUY / WATCH /
HOLD / REDUCE / SELL）。本 skill 保留值得追蹤的訊號：

- `STRONG BUY` / `BUY` / `WATCH` / `HOLD` → 全部併入 `stocks.yaml` 的 `watchlist:`
- 其餘（REDUCE / SELL）忽略

HOLD 是保留的最弱一層——它既不是買也不是賣，屬於「還在看」而不是「已淘汰」，
所以進觀察清單而非丟掉。代價是清單規模大約翻倍，因此 HOLD 排在最後。

`stocks.yaml` 沒有 `buy:` / `watch:` / `hold:` 層級，只有 `positions:` 與 `watchlist:`，
所以三層一律進 `watchlist:`，依把握度排序寫入（BUY → WATCH → HOLD）。

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
BUY / WATCH / HOLD 檔數、實際新增與跳過的檔數，以及新增清單。

跑完後，把來源報告日期、新增檔數、跳過檔數回報給使用者；若 BUY+WATCH+HOLD 掃描結果為 0，
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
  - code: "3022"      # ← 今天報告新增的 BUY / WATCH / HOLD
    name: "威強電"
```
