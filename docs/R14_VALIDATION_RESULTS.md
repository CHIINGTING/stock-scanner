# R14 Technical Indicator — Outcome Validation Results

執行日期：2026-08-20　·　來源：`bin/r6backtest -technical`
輸出：`reports/backtest_technical_20260820.csv` / `reports/backtest_technical_summary_20260820.md`

---

## ⚠️ Statistical confidence: NOT ESTABLISHED

在讀任何一個數字之前，先讀這一節。

1. **觀察值高度重疊。** 每個條件在每一根 bar 上都可能觸發，同一檔股票連續數十根 bar 的
   forward return 幾乎是同一段行情。`sample_count` 6.1 萬筆的有效樣本數遠低於 6.1 萬，
   但本次未計算 effective N，因此**任何條件間的差異都沒有做顯著性檢定**。
2. **摘要裡的 `confidence: HIGH` 不是統計信心。** 那是既有引擎依 sample_count 給的標籤，
   完全沒有考慮重疊。不得引用為「統計上顯著」。
3. **單一 regime。** 涵蓋 2024-06-11 → 2026-08-19，warmup 250 根後實際訊號期約 14 個月，
   未跨越多空循環。cross-regime 驗證**未執行**。
4. **倖存者偏誤。** universe 來自 `.cache`，下市股票不在其中，所有列的絕對報酬一律偏樂觀。
   **只能讀「與 BASELINE_ALL_BARS 的差」，不能讀絕對值。**
5. **極端值集中。** best_cases 反覆由 5386、8291、3581 等少數標的佔據；worst_cases 由
   0052（ETF）、7780、6610 主導。多數條件的平均報酬由個位數標的決定。

**因此：本文件的結論一律是「未能支持」或「被否定」，沒有任何一項是「已證實有效」。**

---

## 執行參數

```
universe   : 1986 stocks（.cache，唯讀）
coverage   : 2024-06-11 → 2026-08-19（534 個交易日）
warmup     : 250　horizons: 5 / 10 / 20 / 60d
entry      : next_open（訊號在 bar i，進場在 i+1）
stops      : BREAK_MA60, PCT_-10
parameters : technical.DefaultConfig() —— 出貨值，非調校版本
conditions : 18　trades: 372,470
```

主要統計為 **stop-adjusted return**；`hold_*` 欄為忽略停損的對照。

---

## ‼️ 方法論缺陷：停損規則對空方／超賣條件具結構性偏誤

這是本次最重要的發現，必須寫在結論之前。

| 條件 | stop_hit_rate |
|---|---|
| RSI_OVERSOLD | **99.9%** |
| ADX_STRONG_BEARISH | **98.4%** |
| BASELINE_ALL_BARS | 92.7% |
| KELTNER_BREAKOUT | 85.2% |

引擎的停損是 `BREAK_MA60` 與 `PCT_-10`，兩者都是**做多**停損。超賣或空頭趨勢中的標的
本來就在 MA60 之下，進場當下即觸發停損 —— 報酬被截斷在接近損益兩平處。

證據：`RSI_OVERSOLD` 在 5d/10d/20d/60d 的勝率全部是 46.0~46.1%、平均報酬 +0.2%、
**中位數精確為 0.0%**。四個 horizon 完全不隨時間變化，這不是市場行為，是「進場即出場」
的機械結果。

**結論：`RSI_OVERSOLD` 與 `ADX_STRONG_BEARISH` 兩列的 stop-adjusted 數字不可解讀，**
**也不可與做多條件並列比較。** 這兩列的 `hold_*` 欄仍可粗略參考（見下）。

R14 不修改共用的 r6backtest 停損引擎（那是既有元件，且屬 R6 範圍）。若要正確測量空方／
超賣條件，需要一組對稱的停損規則，那是獨立工作項。

---

## 1. ADX ——「強趨勢 = 較好」未獲支持

20d，stop-adjusted：

| 條件 | n | 勝率 | 平均 | 中位數 | MDD avg |
|---|---|---|---|---|---|
| BASELINE_ALL_BARS | 61,175 | 34.7% | +0.7% | −1.1% | −12.9% |
| ADX_STRONG_BULLISH | 19,948 | **33.8%** | +1.3% | **−3.5%** | **−16.2%** |
| ADX_WEAK | 24,161 | 33.5% | +0.2% | −0.9% | −11.1% |

**規格書的可證偽宣稱是「ADX ≥ 25 且 +DI > −DI 應優於 ADX < 20」。就勝率而言：未成立。**
33.8% vs 33.5%，差距 0.3 個百分點，且**兩者都低於 baseline 的 34.7%**。

平均報酬確實較高（+1.3% vs +0.2%），但同時中位數大幅惡化（−3.5% vs −0.9%）、最大回撤
惡化（−16.2% vs −11.1%）。這是右偏分布的特徵：少數暴衝標的（5386 +274.9%）撐起平均，
而**典型的一筆交易虧得更多**。

> **ADX_STRONG_BULLISH 比較像是波動度／偏態的選擇器，不像是勝率上的優勢。**
> 這與 R12 對 EXTENDED 的發現是同一個型態。

60d 更清楚：勝率 25.2%（baseline 29.8%），平均 +3.1%，中位數 −5.6%。

---

## 2. MACD —— histogram 加速度**沒有**增加任何東西

20d，stop-adjusted：

| 條件 | n | 勝率 | 平均 | 中位數 |
|---|---|---|---|---|
| MACD_ABOVE_SIGNAL | 40,210 | 33.6% | +0.8% | −1.5% |
| MACD_ABOVE_SIGNAL_ACCELERATING | 33,129 | **33.2%** | +0.8% | −1.6% |
| MACD_GOLDEN_CROSS | 14,874 | 32.7% | +0.4% | −1.1% |

**規格書的宣稱是「MACD bullish + histogram accelerating 應優於單純 MACD > Signal」。**
**結果：被否定。** 加上加速度條件後勝率下降 0.4pp、平均報酬相同、中位數略差。
樣本從 40,210 縮到 33,129（篩掉 18%），卻沒有換到任何改善。

`MACD_GOLDEN_CROSS`（真正的穿越事件）表現比持續在 signal 之上更差：勝率 32.7%，
是全部做多條件中最低的之一。

三者皆低於 baseline 34.7%。

---

## 3. RSI —— 既非 continuation 也非 mean reversion，是偏態放大器

| 條件 | horizon | 勝率 | 平均 | 中位數 | MDD avg |
|---|---|---|---|---|---|
| RSI_OVERBOUGHT | 20d | 35.4% | +1.5% | **−6.4%** | −18.0% |
| RSI_OVERBOUGHT | 60d | **25.6%** | +3.6% | **−10.0%** | — |
| RSI_OVERBOUGHT_IN_STRONG_TREND | 20d | 35.6% | +1.7% | −7.2% | −18.8% |
| BASELINE | 20d | 34.7% | +0.7% | −1.1% | −12.9% |

**規格書刻意不預設答案。資料給的答案是：兩者都不是。**

- 勝率略高於 baseline（35.4% vs 34.7%）→ 不支持「超買必然反轉」。
- 中位數 −6.4%（60d −10.0%）遠差於 baseline −1.1% → 不支持「超買代表延續」。
- 平均 +1.5% 高於 baseline，但最大回撤 −18.0% 是所有條件中最差的一組。

**正確的敘述是：RSI > 70 之後的分布被拉寬了。** 少數標的續飆（5386 系列包辦全部
best_cases），多數標的的典型結果明顯較差。持有期越長越極端（60d 勝率掉到 25.6%）。

加上「在強趨勢中」幾乎沒有改變任何數字（35.6% vs 35.4%），代表 ADX 的條件在此**沒有
提供額外資訊**。

---

## 4. Keltner —— ADX 確認**沒有**改善突破

20d，stop-adjusted：

| 條件 | n | 勝率 | 平均 | 中位數 | MDD avg |
|---|---|---|---|---|---|
| KELTNER_BREAKOUT | 17,440 | 35.3% | +1.6% | −5.3% | −17.0% |
| …_ADX_CONFIRMED | 12,146 | 35.5% | +1.7% | **−6.2%** | **−18.2%** |
| …_UNCONFIRMED | 3,098 | 35.3% | +1.3% | **−4.1%** | **−13.6%** |

**規格書的宣稱是「Close > Upper + ADX ≥ 25 + 量能擴張應優於單純 Close > Upper」。**
**結果：未獲支持。**

confirmed 對 unconfirmed 只多 0.2pp 勝率、+0.4pp 平均報酬，但**中位數更差**
（−6.2% vs −4.1%）、**回撤更大**（−18.2% vs −13.6%）。

換句話說，「用 ADX 確認突破」買到的不是更高的成功率，而是更大的波動。
如果目標是勝率或風險調整後報酬，這個確認條件目前**沒有價值**。

（註：本次未納入量能擴張條件 —— `technical` package 不處理量能，那屬於既有 scanner 的
`VolumeRatio`。完整驗證規格書的三條件版本需要跨層條件，列為後續工作。）

---

## 5. Pivot —— 與 baseline 無法區分

20d，stop-adjusted：

| 條件 | n | 勝率 | 平均 | 中位數 |
|---|---|---|---|---|
| BASELINE_ALL_BARS | 61,175 | 34.7% | +0.7% | −1.1% |
| PIVOT_NEAR_RESISTANCE | 41,499 | 34.8% | +0.6% | −1.0% |
| PIVOT_NEAR_SUPPORT | 39,218 | 35.3% | +0.4% | −0.9% |

**規格書的問題：「接近 R1/R2 是否降低短期風險報酬比？接近 S1/S2 是否提高反彈機率？」**

**答案：在 1% 容差與這些 horizon 下，兩者都測不到。** 三列的勝率落在 34.7~35.3%、
平均報酬 0.4~0.7%、中位數 −0.9~−1.1%，差異小於任何合理的雜訊門檻。

值得注意的是兩個條件各自涵蓋了 baseline 的 2/3（41,499 與 39,218 對 61,175）——
以 1% 容差而言，「接近某個 pivot」幾乎是常態而非事件。若要讓這個維度有鑑別力，
`pivot_near_pct` 需要更嚴格；**但本次不調整**，調參來讓結果好看正是 R14 禁止的事。

---

## 6. Technical Context —— 聚合層目前沒有增加資訊

| 條件 | n | 20d 勝率 | 20d 平均 |
|---|---|---|---|
| CONTEXT_STRONG_BULLISH_TREND | 17,075 | 34.2% | +1.3% |
| ADX_STRONG_BULLISH | 19,948 | 33.8% | +1.3% |
| CONTEXT_TREND_EXPANSION | 12,146 | 35.5% | +1.7% |
| KELTNER_BREAKOUT_ADX_CONFIRMED | 12,146 | 35.5% | +1.7% |
| CONTEXT_UNCONFIRMED_BREAKOUT | 3,098 | 35.3% | +1.3% |
| KELTNER_BREAKOUT_UNCONFIRMED | 3,098 | 35.3% | +1.3% |

`CONTEXT_TREND_EXPANSION` 與 `KELTNER_BREAKOUT_ADX_CONFIRMED` **樣本數與每一個統計量
完全相同**；`CONTEXT_UNCONFIRMED_BREAKOUT` 與 `KELTNER_BREAKOUT_UNCONFIRMED` 亦然。
這是定義上的必然（聚合器的規則就是這兩個條件的合取），但它證實了一件事：

> **目前的 Context 聚合沒有產生任何單一指標之外的資訊。**

它的價值目前純粹在**可讀性**（把五個維度整理成人與 agent 可讀的敘述），而不在預測力。
這一點必須誠實記錄，否則「有一個聚合層」很容易被誤讀成「聚合有效」。

---

## 7. 唯一表現高於 baseline 的做多條件（且差距極小）

以 20d 勝率排序，所有**可解讀**（非停損偏誤）的條件：

```
PIVOT_NEAR_SUPPORT             35.3%
RSI_OVERBOUGHT_IN_STRONG_TREND 35.6%
KELTNER_BREAKOUT_ADX_CONFIRMED 35.5%
KELTNER_BREAKOUT               35.3%
CONTEXT_UNCONFIRMED_BREAKOUT   35.3%
PIVOT_NEAR_RESISTANCE          34.8%
BASELINE_ALL_BARS              34.7%   ← 基準
CONTEXT_STRONG_BULLISH_TREND   34.2%
ADX_STRONG_BULLISH             33.8%
MACD_ABOVE_SIGNAL              33.6%
ADX_WEAK                       33.5%
MACD_ABOVE_SIGNAL_ACCELERATING 33.2%
MACD_GOLDEN_CROSS              32.7%
```

最好的條件比 baseline 高 0.9 個百分點，最差的低 2.0 個百分點。
**在有效樣本數未知、未做顯著性檢定的前提下，這個範圍不足以支持任何一個條件進入計分。**

---

## 結論

**R14 維持 shadow-only。沒有任何指標取得進入 scoring 的資格。**

| 規格書的可證偽宣稱 | 結果 |
|---|---|
| ADX 強趨勢優於弱趨勢 | **未獲支持**（勝率 33.8% vs 33.5%，兩者皆低於 baseline） |
| MACD histogram 加速優於單純 MACD > Signal | **被否定**（33.2% vs 33.6%） |
| Keltner 突破經 ADX 確認後更好 | **未獲支持**（勝率 +0.2pp，但中位數與回撤皆惡化） |
| RSI > 70 是 continuation 或 mean reversion | **兩者皆非**（偏態放大，中位數大幅惡化） |
| Pivot 接近壓力／支撐具鑑別力 | **測不到**（與 baseline 無法區分） |
| Context 聚合優於單一指標 | **未獲支持**（與其成分完全相同） |

沒有任何 threshold 因為這份結果被調整過。`technical.DefaultConfig()` 在跑之前就固定，
跑完之後也沒有動。

---

## 未執行 / 後續工作

1. **對稱停損規則** —— 現行 BREAK_MA60 / PCT_-10 使空方與超賣條件不可測（stop_hit_rate
   98~100%）。這是 r6backtest 共用元件，R14 不修改。
2. **Effective N 與顯著性檢定** —— 需處理重疊觀察值（事件級去重或 block bootstrap）。
3. **Cross-regime** —— 目前僅單一 14 個月區間；需接上 `market/model.Regime` 分層。
4. **量能條件** —— 規格書的 Keltner 三條件版本需要 scanner 的 `VolumeRatio`，屬跨層條件。
5. **R13-M6 outcome tracking** —— 本次走 r6backtest（歷史回測）。R13 的 `outcomes` 表
   是**前瞻**追蹤（記錄當下判斷、日後補實際結果），兩者互補，尚未串接。
6. **參數敏感度** —— `pivot_near_pct = 1.0` 使「接近 pivot」涵蓋 2/3 的 bar，鑑別力低。
   在有顯著性檢定之前不調整。

---

## 附錄：原始輸出

完整逐條件統計見 `reports/backtest_technical_summary_20260820.md`，
逐筆交易見 `reports/backtest_technical_20260820.csv`（372,470 筆）。
