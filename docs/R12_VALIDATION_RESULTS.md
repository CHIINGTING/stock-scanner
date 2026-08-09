# R12 Validation — Slope / BIAS / SectorHeat

> **R12 維持 shadow-only。本文件不授權任何 scoring promotion，也不因下列結果調整任何門檻。**
>
> 這份驗證的價值在於它**否證了原始假設的一部分**，而不是被拿來反覆調參直到數字好看。
> 依此原則，`internal/scanner/trendext.go` 的門檻**在看到下列結果之後一個字都沒有改**。

執行方式：`./bin/r6backtest -trendext`（另可 `-heatstep 1` 改為逐日重算族群熱度面板）。
原始引擎輸出見文末〈附錄：原始輸出〉，本文只在其上加結論與限制，不修改數字。

---

## ⚠️ Statistical confidence: **NOT ESTABLISHED**

**這是閱讀本文件所有數字的前提。**

下方每一列的 `sample_count` 是 **bar-level 重疊觀測數**，不是獨立樣本數：

- 觀測在時間上互相重疊。同一檔股票、同一段趨勢，連續多個交易日都各自成為一筆觀測。
- 同一個 symbol / state episode 內的重複觀測**高度自相關**——它們描述的是同一個事件，不是多個獨立事件。
- 因此 **raw N 會實質性高估 effective N**。`BASELINE` 的 61,339 筆觀測，其獨立事件數遠低於此，且本次未估計。

**通用回測引擎印出的 `confidence: HIGH` 標籤，對 R12 而言不具統計意義。**
那個標籤只反映 raw 觀測筆數門檻，是 r6backtest 為既有 setup 設計的啟發式，
**不得**被解讀為 R12 各 cohort 差異的統計信心。

> 依 review 決議：**不因 R12 修改共用的 r6backtest confidence 引擎**。
> 該引擎服務既有 R6 setup，在此改動會牽動無關模組。此限制以本節文件形式承載。

在 effective sample 被正確估計之前（見〈下一步：Event-level deduplication〉），
本文件所有差異僅能作為**方向性線索**，不足以作為 promotion 依據。

---

## 結論

### 1. SectorHeat 是目前唯一的 primary promotion candidate

驗證顯示，本次新增的三項資訊裡，**最有用的是族群熱度**：

| cohort | 20d win rate |
|---|---|
| BASELINE | 34.7% |
| SECTOR_ONLY_HEAT_STRONG | **43.4%** |
| STATE_TREND_CONFIRMED | **45.1%** |

**不得**把 TREND_CONFIRMED 的改善整體歸因於「MA slope + BIAS + SectorHeat 三者的組合」。
SECTOR_ONLY 單獨就已達 43.4%，而 TREND_CONFIRMED 為 45.1%——
**兩者只差 1.7 個百分點，其中大部分可能已經來自 SectorHeat 本身。**

> **MA slope / BIAS 在 conditioning on SectorHeat 之後的增量價值，目前仍未被證實（unproven）。**

這正是下一節 ablation 要回答的問題。在它被回答之前，**不做任何 scoring change**。

### 2. PULLBACK_IN_UPTREND：狀態保留，但**不是**較佳進場

| | BASELINE | PULLBACK_IN_UPTREND |
|---|---|---|
| 20d win rate | 34.7% | **29.9%** |
| 20d mean | +0.7% | +0.7% |
| 20d median | -1.1% | -2.2% |
| MAE (avg) | -13.1% | -12.3% |

解讀：**回撤略淺，但沒有報酬優勢，且勝率更低。**

因此 `PULLBACK_IN_UPTREND` 的語意僅限於：

```
上升趨勢中的技術性回檔（描述價格在趨勢中的位置）
```

**它不得被描述為**：較佳進場 / 加碼點 / buy-the-dip edge / 高勝率 setup。

狀態與實作**保持不變**，門檻**不因此結果調整**，也**不以本次回測對其最佳化**。

### 3. EXTENDED：追價風險警告，**不是**看空訊號

| | 值 |
|---|---|
| 20d win rate | 34.8% |
| 20d mean | **+1.6%** |
| 20d median | **-4.4%** |
| MAE (avg) | **-16.3%** |

分布是**不對稱**的：**典型結果差、下檔風險深，但右尾存在正向離群值**
（平均數高於中位數 6 個百分點，即少數大幅續漲樣本撐起了均值）。

因此唯一正確的解讀是：

```
不要追 / 進場風險升高
```

**不是**：必跌 / 賣出 / 空頭反轉 / 預期下跌。

`EXTENDED` **不得**被轉換為 SELL、bearish reversal 或 expected decline。
報表措辭與本文件均須維持此區分。

### 4. SECTOR_DIVERGENCE 方向與假設一致

20d mean +2.0% vs TREND_CONFIRMED +6.1%——個股獨強而族群未共振者表現較弱，
與「降低 trend confidence」的設計方向一致。惟樣本僅 943 筆且受上述 effective-N 問題影響。

---

## 下一步：Ablation matrix（promotion 的前置條件）

**尚未實作。** 本次不建置完整 ablation 引擎；此處僅記錄為下一個驗證步驟。

| # | cohort |
|---|---|
| 1 | BASELINE |
| 2 | SECTOR_ONLY |
| 3 | SLOPE_ONLY |
| 4 | BIAS_ONLY |
| 5 | SECTOR + SLOPE |
| 6 | SECTOR + BIAS |
| 7 | SLOPE + BIAS |
| 8 | SECTOR + SLOPE + BIAS |

**主要問題：**

```
在 SectorHeat 已知的條件下，
MA slope 與 BIAS 是否提供增量預測資訊？
```

**此問題必須先被回答，才能討論任何 R12 scoring promotion。**

現況：cohort 1–4 已存在於本次輸出（`BASELINE_ALL_BARS` / `SECTOR_ONLY_HEAT_STRONG` /
`SLOPE_ONLY_MA20_UP` / `BIAS_ONLY_*`），5–8 尚未建立。

---

## 下一步：Event-level deduplication（robustness）

**尚未實作。** 僅在能與 production code 乾淨隔離時才實作。

至少需比較兩種 cohort 定義：

| cohort 定義 | 說明 |
|---|---|
| bar-level（現況） | 每個滿足條件的交易日各算一筆 |
| **state-entry** | 只計入 symbol **首次進入**該 TrendExtension 狀態的那一天 |

state-entry 規則：

```
NEUTRAL          → TREND_CONFIRMED    計入
TREND_CONFIRMED  → TREND_CONFIRMED    不計入
TREND_CONFIRMED  → NEUTRAL → TREND_CONFIRMED   再次計入
```

可選的第二種方法：**每 symbol / state 20 個交易日 cooldown**。

兩者的目的相同：估計 effective N，讓上方〈Statistical confidence〉一節能從
NOT ESTABLISHED 前進。

---

## 下一步：Regime robustness

本次涵蓋期 **2024-06 → 2026-08**，且該期間幾乎全為多頭，
**不足以支撐 cross-regime promotion**。

未來至少需按下列期間拆解：

- 2024 H2
- 2025
- 2026 YTD

並在 MarketRegime 可用後，優先改以 MarketRegime 分層。

**Promotion 條件：效果需在多個期間 / regime 間方向一致**，而非僅憑 pooled 表現。

---

## 限制：Survivorship

`.cache` 只包含**目前仍存續**的標的，已下市 / 已消失的標的不在母體內。

因此**絕對報酬與勝率數字可能偏樂觀**，且此偏誤同時作用於每一個 cohort（含 BASELINE）。

R12 結果的正確用法：

```
相對於同一 baseline 的 cohort 差異
```

**不是**對實盤預期報酬的估計。

---

## 技術債 / 後續事項（**R12 不處理**）

Phase 0 audit 找到下列既有問題。依 review 決議，**不在 R12 內重構**，僅記錄：

| 項目 | 說明 | 風險 |
|---|---|---|
| `SectorRotation.MA60Slope` 誤名 | 實際內容是「MA60 上揚的成員比例」，是 breadth 不是 slope | **不得在本 feature commit 內改名**——會製造與 R12 無關的相容性風險 |
| `internal/pricingpower` 孤兒套件 | 完整實作 + 測試，但除自身外無任何檔案 import | 未接線，且含第二套 sector→theme 對應 |
| 第二套 sector→theme mapping | 位於 `internal/pricingpower/theme.go`，與 `configs/sectors.yaml` 並存 | 與「唯一 taxonomy」原則衝突 |
| 「EXTENDED」有四套定義 | `rocket.go` 的 `extFrom5>12`（影響 Stage 與計分）、`BiasView.Risk`、`pricingpower.OVEREXTENDED`、R12 的 `BIAS20 percentile` | R12 刻意不新增第五套：分位不可得時 fallback 到既有 R10-1 標籤 |

---

## 附錄：原始輸出

以下為 `./bin/r6backtest -trendext` 的未修改輸出。
**閱讀時請套用本文件開頭的 statistical-confidence 限制**，尤其是 `confidence: HIGH` 標籤不具統計意義。

# R12 Trend / BIAS / Sector-Heat Condition Comparison

> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。

> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。
> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。
> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。

- universe: 1983 stocks (cache, read-only) ; sectors: 1040 symbols mapped
- coverage: 2024-06-11 → 2026-08-07 (526 trading days)
- warmup: 250 ; horizons: [5 10 20 60] ; entry: next_open ; stops: [BREAK_MA60 PCT_-10]
- heat panel step: 5 trading days
- thresholds: read from scanner.DefaultTrendExtThresholds() — the SHIPPED values, not a tuned copy
- READ AS DIFFERENCES FROM BASELINE_ALL_BARS: the universe is survivorship-biased (delisted names absent), so absolute returns are optimistic for every row alike
- the falsifiable claim: STATE_PULLBACK_IN_UPTREND and STATE_EXTENDED must differ in forward return AND downside — if they do not, the state machine is not describing a real distinction

## BASELINE_ALL_BARS

- sample_count: 61339　confidence: HIGH　stop_hit_rate: 92.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.7% | -0.0% | -0.5% | 42.5% | 0.1% | -0.1% |
| 10d | 37.7% | 0.2% | -0.8% | 42.4% | 0.5% | -0.3% |
| 20d | 34.7% | 0.7% | -1.1% | 43.4% | 1.6% | -0.9% |
| 60d | 29.6% | 1.5% | -1.6% | 46.5% | 7.5% | -6.0% |

- max_drawdown_avg: -13.1%　max_drawdown_p90: -28.3%
- best_cases: 8291(563.3%), 8291(563.0%), 8291(562.7%), 8291(444.2%), 8291(345.4%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-86.0%), 0052(-85.7%), 6610(-62.5%)

## SLOPE_ONLY_MA20_UP

- sample_count: 35257　confidence: HIGH　stop_hit_rate: 89.0%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.2% | 0.1% | -0.9% | 42.7% | 0.2% | -0.1% |
| 10d | 37.6% | 0.5% | -1.5% | 43.0% | 0.9% | -0.4% |
| 20d | 33.4% | 1.3% | -2.4% | 44.4% | 2.5% | -1.2% |
| 60d | 25.2% | 2.6% | -3.8% | 47.9% | 9.8% | -7.2% |

- max_drawdown_avg: -14.7%　max_drawdown_p90: -31.4%
- best_cases: 8291(563.3%), 8291(444.2%), 8291(345.4%), 5386(274.9%), 8291(264.7%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-86.0%), 0052(-85.7%), 6610(-62.5%)

## BIAS_ONLY_NEAR_MA20

- sample_count: 37143　confidence: HIGH　stop_hit_rate: 95.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.4% | 0.0% | -0.4% | 42.2% | 0.1% | -0.1% |
| 10d | 35.7% | 0.0% | -0.6% | 41.9% | 0.3% | -0.3% |
| 20d | 32.5% | 0.2% | -0.8% | 42.6% | 1.0% | -0.9% |
| 60d | 28.9% | 0.6% | -1.0% | 45.0% | 5.7% | -5.0% |

- max_drawdown_avg: -11.0%　max_drawdown_p90: -23.3%
- best_cases: 8291(562.5%), 5386(236.1%), 3026(212.6%), 6861(199.0%), 6983(176.7%)
- worst_cases: 0052(-85.9%), 6610(-59.0%), 5481(-26.0%), 6236(-25.8%), 6236(-25.8%)

## BIAS_ONLY_EXTENDED_P90

- sample_count: 22568　confidence: HIGH　stop_hit_rate: 87.4%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.0% | -0.1% | -1.5% | 40.7% | -0.1% | -0.0% |
| 10d | 37.6% | 0.4% | -2.2% | 41.9% | 0.8% | -0.4% |
| 20d | 33.6% | 1.4% | -3.9% | 43.1% | 2.4% | -1.1% |
| 60d | 24.5% | 2.6% | -6.0% | 47.4% | 10.1% | -7.5% |

- max_drawdown_avg: -15.8%　max_drawdown_p90: -32.8%
- best_cases: 8291(563.3%), 8291(563.0%), 8291(562.7%), 8291(444.2%), 8291(345.4%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 6908(-34.7%)

## SECTOR_ONLY_HEAT_STRONG

- sample_count: 3016　confidence: HIGH　stop_hit_rate: 81.6%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 45.9% | 0.9% | -0.7% | 49.2% | 1.2% | -0.3% |
| 10d | 45.2% | 2.5% | -1.1% | 53.2% | 3.9% | -1.4% |
| 20d | 43.4% | 6.0% | -2.1% | 57.1% | 8.7% | -2.7% |
| 60d | 35.9% | 10.6% | -4.7% | 66.6% | 27.4% | -16.8% |

- max_drawdown_avg: -15.6%　max_drawdown_p90: -35.3%
- best_cases: 2492(192.9%), 2492(160.8%), 6173(154.4%), 2492(151.4%), 2327(145.6%)
- worst_cases: 6173(-20.8%), 3260(-20.2%), 6488(-18.9%), 2327(-18.8%), 2009(-18.7%)

## STATE_PULLBACK_IN_UPTREND

- sample_count: 12706　confidence: HIGH　stop_hit_rate: 90.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.4% | 0.2% | -0.7% | 43.9% | 0.3% | -0.1% |
| 10d | 36.1% | 0.4% | -1.3% | 43.3% | 0.8% | -0.4% |
| 20d | 29.9% | 0.7% | -2.2% | 43.3% | 1.9% | -1.2% |
| 60d | 22.2% | 1.6% | -2.8% | 48.4% | 9.3% | -7.6% |

- max_drawdown_avg: -12.3%　max_drawdown_p90: -26.4%
- best_cases: 5386(236.1%), 3581(233.1%), 3026(212.6%), 6983(176.7%), 6451(147.4%)
- worst_cases: 0052(-85.9%), 6236(-25.8%), 6236(-25.8%), 6236(-25.8%), 6236(-25.8%)

## STATE_EXTENDED

- sample_count: 19523　confidence: HIGH　stop_hit_rate: 86.5%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.1% | 0.0% | -1.4% | 41.6% | 0.1% | -0.1% |
| 10d | 38.8% | 0.6% | -2.2% | 43.0% | 1.1% | -0.4% |
| 20d | 34.8% | 1.6% | -4.4% | 44.0% | 2.8% | -1.2% |
| 60d | 25.1% | 3.0% | -6.8% | 48.3% | 10.9% | -7.9% |

- max_drawdown_avg: -16.3%　max_drawdown_p90: -33.7%
- best_cases: 8291(563.3%), 8291(444.2%), 8291(345.4%), 8291(264.7%), 5386(240.4%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 6908(-34.7%)

## STATE_TREND_CONFIRMED

- sample_count: 1387　confidence: HIGH　stop_hit_rate: 80.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 48.9% | 1.8% | 0.0% | 50.9% | 2.1% | -0.3% |
| 10d | 47.4% | 3.6% | -0.7% | 53.8% | 4.6% | -1.0% |
| 20d | 45.1% | 6.1% | -1.9% | 58.4% | 8.4% | -2.2% |
| 60d | 36.4% | 11.5% | -4.1% | 68.7% | 31.0% | -19.5% |

- max_drawdown_avg: -14.9%　max_drawdown_p90: -34.8%
- best_cases: 6173(189.0%), 3026(162.1%), 2327(145.6%), 2327(127.8%), 8261(122.1%)
- worst_cases: 6173(-20.8%), 6488(-18.9%), 3665(-18.5%), 5425(-18.4%), 3491(-18.4%)

## STATE_SECTOR_DIVERGENCE

- sample_count: 943　confidence: HIGH　stop_hit_rate: 86.4%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 41.9% | -0.0% | -0.9% | 46.2% | 0.3% | -0.3% |
| 10d | 36.0% | 0.3% | -2.1% | 44.0% | 0.8% | -0.5% |
| 20d | 33.5% | 2.0% | -2.6% | 50.8% | 4.8% | -2.8% |
| 60d | 29.7% | 8.7% | -3.4% | 64.2% | 27.7% | -19.0% |

- max_drawdown_avg: -13.7%　max_drawdown_p90: -27.3%
- best_cases: 6182(113.2%), 3026(106.5%), 3026(103.7%), 6147(98.5%), 6147(96.4%)
- worst_cases: 6610(-62.5%), 6610(-60.9%), 6610(-57.7%), 4991(-18.5%), 4991(-17.1%)

