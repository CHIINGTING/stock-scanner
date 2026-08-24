# Signal Validation Feedback — 為何 REDUCE/獲利了結訊號偏弱

- 來源：`data/signal_validation_20260706.csv` + `data/signal_snapshots_20260706.csv`
- 驗證窗口：2026-06-01 ~ 06-13 報告，benchmark 0050，horizons T+3/5/10/20
- 產出：2026-07-06

## TL;DR

- REDUCE_GROUP 的問題**幾乎全在 watchlist 的「太熱不要追」警告，不在真正的持倉出場**。
- 120 個 WRONG 裡 **94 個 (78%) 是 TAKE_PROFIT/獲利了結類**；其中 **204/227** 的 watchlist REDUCE_GROUP 訊號是 `OVERHEATED;追高`。
- 這些被判 WRONG 的股票後續 **median +12.8% (T+10)、max_runup median +26.4%**，最猛 **+50~77%**（宏旭-KY、百容、和桐、松川精密…小型/KY 動能股）。
- 反觀真正的持倉出場（positions tab）只有 95 個訊號、**命中率 64%**，是兩者中較好的。
- 也就是說：Scanner 不是「出場出錯」，而是**在強勢股主升段的起點把它們打成『過熱勿追』並下車**。

## 數據拆解

| 分組 | 樣本 | 命中率 | 說明 |
|------|-----:|------:|------|
| REDUCE_GROUP 全體 | 322 | 60% (177/297) | HTML 的「走弱率 48%」是更誠實的數字，見下 |
| — watchlist | 227 | 58% (125/216) | 其中 204 個是 `OVERHEATED;追高` |
| — positions | 95 | 64% (52/81) | 真正的持倉 TAKE PROFIT/REDUCE/SELL/STOP LOSS |

WRONG 的後續報酬（這些其實「不該下車」）：

| 指標 | median | mean | p90 | max |
|------|-------:|-----:|----:|----:|
| return_5d | +12.8% | +13.9% | +27.5% | +39.0% |
| return_10d | +12.8% | +15.8% | +40.5% | +54.4% |
| max_runup | +26.4% | +28.9% | +46.4% | +76.8% |

補充警訊：
- **序列性重複誤判** — 同一檔在趨勢未破前天天重觸（聯電 x5、大毅 x5、揚明光 x4、松川精密 x3…）。120 個 wrong 只分布在 68 檔（1.8 訊號/檔），一大塊是同一波趨勢被重覆計次。
- **CORRECT 也偏軟** — 177 個 CORRECT 裡有 36 個 T+10 只是 0~−3% 的「停滯」，屬寬鬆計對。所以真正有效的 REDUCE 比命中率看起來更少。

## 根因（對到程式碼）

1. **watchlist OVERHEATED 門檻對動能行情太低** — `internal/scanner/rocket.go:257`
   ```go
   extended := extFrom5 > 12 || ret5 > 25   // 離5日均>12% 或 5日漲>25% 就算 extended
   ...
   case extended || climax:
       out.Stage = StageOverheated           // → WatchAction=TAKE_PROFIT, tag "OVERHEATED;追高"
   ```
   5 日漲 >25% 在多數行情是「過熱」，但在動能/risk-on 盤是**主升段起點**。6 月那批 KY/小型股正是從這裡再噴 +40~76%。

2. **分類錯誤（驗證面）** — watchlist `WatchAction.TAKE_PROFIT (OVERHEATED;追高)` 是「進場風險警告 / 別追高」，**不是方向看空**。validator 把它併進 REDUCE_GROUP 並要求「後續走弱」才算對，本質是 category error：既冤枉了訊號，也把真正持倉出場（64%）的命中率往下稀釋。

3. **持倉出場的次要問題** — `internal/scanner/scorer.go:669-688` `positionAdvice()`
   - `pnlPct >= 30` → 純獲利門檻、無趨勢確認，+30% 全出，錯過後續中位數 +26% runup。
   - `pnlPct >= 15 && rsi > 72`、REDUCE `pnlPct >= 12 && rsi > 65` → 純超買、無趨勢破壞確認；強勢股 RSI 長期 >72 是常態，會天天觸發。
   - 有趨勢確認的 rule（`deathCross` / `ma20Down`）才是對的方向 → 這幾條保留。

## 具體建議（依 CP 值排序）

### A. 先修「分類」— 幾乎零風險，立即讓數字回到真相（改 validator，不動 scanner）✅ 已實作
- 在 `internal/validator` 把 `OVERHEATED;追高` 這類 watchlist WatchAction 從 REDUCE_GROUP 拆出，獨立成 `ENTRY_CAUTION_GROUP`（`signal.go GroupForSnapshot`/`isOverheatedCaution`；只認 OVERHEATED token，不認裸 追高 sub-tag）。
- 判準改為「後續是否出現可進場的回檔 / 是否避開了追高即套」，而不是「必須下跌」（`evaluator.go evalEntryCaution`）。
- **實測（2026-06 窗口）**：REDUCE_GROUP 322→118 檔、命中率 60%→**62%**（只剩真出場）；拆出的 ENTRY_CAUTION 204 檔、避追高命中率 **76%**（其中 100/140 CORRECT 是後續真的走弱，非雜訊）；剩 **45 檔直接噴出**＝下面 B 的證據。整體命中率 62%→**65%**。

### B. 修 scanner 的 OVERHEATED 行為（真正影響選股）✅ 已實作（核心）
- **降級動作**：OVERHEATED 但趨勢仍多（`trendIntact := bullAlign && aboveMA20`、無 climax）時，WatchAction 給 `PULLBACK_BUY`（等回檔進場）而非 TAKE_PROFIT；只有 `overheatExit := climax || !trendIntact` 時才喊 TAKE_PROFIT（`rocket.go` stage 決策 + `watchActionFor`/`jointWatchAction`）。
- **TAKE_PROFIT 綁 climax**：真正該喊獲利了結的是 `climaxDistribution` / 上影量比≥2 或趨勢結構已破，而非單純 extension。✅
- **實測（2026-07-06 watchlist 掃描 before/after，同資料）**：130 檔 OVERHEATED 由「全部 TAKE_PROFIT」→ **98 TAKE_PROFIT（真過熱/破線）＋ 32 PULLBACK_BUY（過熱但趨勢完好）**。
- **尚未做（可選後續）**：`extended` 門檻情境化——`ret5 > 25` 對小型動能股仍偏緊；可改用相對 ATR、或加 RS 條件（高 RS 領導股放寬）進一步縮小上面那 45 檔「直接噴出」的誤標。目前只改「動作」未改「門檻」。

### C. 持倉出場改趨勢跟蹤（positions tab，`scorer.go`）
- `pnlPct>=30` 全出 → 改移動停利：收破前波低 / 跌破 MA10 或 MA20 / ATR trailing 才出，讓獲利續跑。
- 純超買觸發（rsi>72 / rsi>65）加「趨勢已破」否決：站上 MA20 且 MA20 未下彎時不出場。
- 出場改分批（先出 1/3，其餘 trailing）。

### D. 抑制序列重複 & 綁 regime（R9）
- 同檔喊過 TAKE_PROFIT/REDUCE 後、趨勢未破前不重複發，改「移動停利觀察」狀態，只有真正破線再提醒。
- risk-on / 強趨勢 regime 放寬獲利了結門檻、優先 trailing；risk-off 才用較緊固定出場。6 月是明顯的動能盤，正好對照 R9 市場 regime 層。

## 如何驗證改動
- 改完 **A** → 重跑同窗口，看 REDUCE_GROUP 命中率是否回升、ENTRY_CAUTION 是否被單獨且合理評估。
- 改完 **B/C** → 看 WRONG 的 `max_runup` 中位數是否下降、以及是否還會天天重觸同一檔（聯電/大毅類）。

相關：`docs/SPEC_R9_1_MARKET_REGIME_LAYER.md`、`cmd/validate-signals/README.md`。
