# R6 Pullback Backtest

> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。

> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。
> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。
> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。

- universe: 1974 stocks (cache, read-only)
- coverage: 2024-06-03 → 2026-06-08 (488 trading days)
- RS panel dates: 362 (lookback 120d)
- warmup: 250 bars (52w) ; horizons: [5 10 20 60] ; entry: next_open
- R6-2b: Setup A (MA20/MA60) + Setup B (pullback sweep) wired. Setup C/D not yet.
- 60d-horizon samples are fewer than 5/10/20d (forward window limited by data end).

## A_MA20_PULLBACK

- sample_count: 2361　confidence: HIGH　stop_hit_rate: 81.1%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.5% | 0.8% | -0.4% | 46.5% | 0.9% | -0.1% |
| 10d | 42.6% | 1.8% | -0.9% | 49.1% | 2.4% | -0.6% |
| 20d | 35.9% | 3.0% | -2.2% | 48.2% | 4.5% | -1.5% |
| 60d | 24.8% | 5.0% | -3.3% | 50.6% | 13.2% | -8.3% |

- max_drawdown_avg: -10.9%　max_drawdown_p90: -22.9%
- best_cases: 8472(174.4%), 6265(165.7%), 8472(159.2%), 6945(158.2%), 3054(154.9%)
- worst_cases: 6610(-58.8%), 3219(-18.9%), 1591(-17.6%), 3609(-17.0%), 6126(-16.9%)

## A_MA60_PULLBACK

- sample_count: 791　confidence: HIGH　stop_hit_rate: 92.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.3% | 0.4% | -0.4% | 45.8% | 0.5% | -0.1% |
| 10d | 31.2% | 0.8% | -0.9% | 45.8% | 1.5% | -0.6% |
| 20d | 26.7% | 1.4% | -1.2% | 43.5% | 2.9% | -1.5% |
| 60d | 20.1% | 1.6% | -1.4% | 41.9% | 4.4% | -2.8% |

- max_drawdown_avg: -9.5%　max_drawdown_p90: -20.6%
- best_cases: 8472(174.4%), 6945(158.2%), 4768(81.0%), 2491(71.4%), 1595(71.3%)
- worst_cases: 5309(-13.4%), 3313(-13.2%), 2380(-10.1%), 3691(-9.3%), 4722(-9.2%)

## B_PULLBACK_5 — pullback 5%

- sample_count: 7271　confidence: HIGH　stop_hit_rate: 75.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.3% | 1.0% | -0.8% | 46.7% | 1.3% | -0.3% |
| 10d | 43.3% | 2.2% | -1.4% | 51.0% | 3.6% | -1.4% |
| 20d | 38.4% | 4.0% | -2.9% | 52.8% | 7.3% | -3.3% |
| 60d | 27.4% | 5.0% | -5.6% | 57.3% | 21.1% | -16.1% |

- max_drawdown_avg: -13.1%　max_drawdown_p90: -26.1%
- best_cases: 5386(240.4%), 3581(197.3%), 6861(180.9%), 6217(165.1%), 6945(159.7%)
- worst_cases: 7780(-89.7%), 6610(-59.7%), 6610(-57.7%), 2755(-24.5%), 6983(-22.6%)

## B_PULLBACK_8 — pullback 8%

- sample_count: 5293　confidence: HIGH　stop_hit_rate: 76.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.6% | 1.3% | -0.8% | 47.4% | 1.6% | -0.4% |
| 10d | 43.3% | 2.4% | -1.5% | 52.1% | 3.9% | -1.6% |
| 20d | 37.8% | 3.8% | -3.2% | 53.1% | 7.6% | -3.8% |
| 60d | 27.1% | 4.7% | -5.8% | 58.4% | 22.6% | -17.8% |

- max_drawdown_avg: -13.3%　max_drawdown_p90: -26.2%
- best_cases: 3581(197.3%), 6861(180.9%), 6265(155.6%), 7610(154.0%), 6173(150.3%)
- worst_cases: 6610(-59.7%), 6610(-57.7%), 6983(-22.6%), 1528(-21.5%), 3081(-21.0%)

## B_PULLBACK_10 — pullback 10%

- sample_count: 3996　confidence: HIGH　stop_hit_rate: 77.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 43.8% | 1.0% | -1.0% | 46.8% | 1.4% | -0.4% |
| 10d | 42.2% | 2.3% | -1.7% | 51.5% | 4.0% | -1.7% |
| 20d | 37.2% | 3.7% | -3.4% | 53.1% | 7.7% | -4.0% |
| 60d | 26.9% | 4.6% | -6.0% | 59.5% | 23.0% | -18.4% |

- max_drawdown_avg: -13.4%　max_drawdown_p90: -26.5%
- best_cases: 6861(180.9%), 6265(155.6%), 6173(150.3%), 6830(138.1%), 4764(137.8%)
- worst_cases: 6610(-57.3%), 1528(-21.5%), 3054(-20.8%), 6983(-19.9%), 6830(-18.8%)

## B_PULLBACK_15 — pullback 15%

- sample_count: 1783　confidence: HIGH　stop_hit_rate: 79.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 45.9% | 1.4% | -0.7% | 48.5% | 1.8% | -0.4% |
| 10d | 42.5% | 2.2% | -1.8% | 52.1% | 4.1% | -1.9% |
| 20d | 35.3% | 3.0% | -3.9% | 52.8% | 7.5% | -4.6% |
| 60d | 25.3% | 2.7% | -5.9% | 61.1% | 23.5% | -20.8% |

- max_drawdown_avg: -13.5%　max_drawdown_p90: -26.2%
- best_cases: 6861(219.1%), 6173(189.0%), 4764(137.8%), 5386(132.6%), 3026(127.4%)
- worst_cases: 6610(-56.9%), 6983(-26.8%), 8358(-19.4%), 6217(-19.0%), 4919(-18.5%)

## B_PULLBACK_20 — pullback 20%

- sample_count: 558　confidence: HIGH　stop_hit_rate: 82.6%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 45.5% | 1.6% | -0.5% | 51.8% | 2.4% | -0.8% |
| 10d | 41.3% | 1.9% | -1.6% | 54.7% | 4.5% | -2.6% |
| 20d | 35.2% | 3.3% | -3.7% | 57.8% | 9.3% | -5.9% |
| 60d | 25.6% | 1.4% | -5.1% | 61.8% | 24.5% | -23.1% |

- max_drawdown_avg: -13.3%　max_drawdown_p90: -25.7%
- best_cases: 6861(180.9%), 3026(162.1%), 4764(137.8%), 3555(105.0%), 6426(100.9%)
- worst_cases: 6610(-57.3%), 6983(-26.8%), 4195(-21.8%), 3054(-21.4%), 6217(-17.0%)

## C_VCP_MA20_RETEST

- sample_count: 2592　confidence: HIGH　stop_hit_rate: 85.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 41.2% | 0.3% | -0.6% | 45.6% | 0.5% | -0.3% |
| 10d | 38.1% | 0.9% | -1.1% | 45.3% | 1.5% | -0.7% |
| 20d | 34.0% | 2.4% | -1.9% | 48.8% | 4.4% | -2.0% |
| 60d | 26.6% | 3.2% | -2.5% | 49.4% | 11.7% | -8.6% |

- max_drawdown_avg: -10.6%　max_drawdown_p90: -22.4%
- best_cases: 6983(176.7%), 8472(174.4%), 8472(159.2%), 6945(158.2%), 8472(152.4%)
- worst_cases: 6610(-59.0%), 2485(-18.4%), 4971(-17.8%), 6584(-17.6%), 7795(-17.3%)

## C_VCP_MA60_RETEST

- sample_count: 1401　confidence: HIGH　stop_hit_rate: 90.4%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.3% | 0.3% | -0.8% | 46.4% | 0.5% | -0.3% |
| 10d | 32.9% | 0.5% | -1.2% | 44.8% | 1.1% | -0.6% |
| 20d | 29.1% | 1.6% | -1.7% | 47.7% | 3.8% | -2.2% |
| 60d | 23.3% | 2.0% | -2.0% | 45.6% | 8.3% | -6.3% |

- max_drawdown_avg: -10.2%　max_drawdown_p90: -21.1%
- best_cases: 8472(174.4%), 6945(159.7%), 8472(152.4%), 4764(137.8%), 2344(97.6%)
- worst_cases: 6442(-16.9%), 1713(-15.5%), 6706(-14.9%), 1713(-14.5%), 3138(-14.1%)

## C_VCP_BASE_LOW_RETEST

- sample_count: 288　confidence: HIGH　stop_hit_rate: 96.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 41.3% | 0.2% | -0.3% | 46.9% | 0.2% | 0.0% |
| 10d | 39.2% | 0.4% | -0.4% | 46.9% | 0.4% | -0.0% |
| 20d | 34.7% | 1.0% | -0.6% | 43.8% | 2.1% | -1.1% |
| 60d | 32.8% | 1.0% | -0.6% | 43.1% | 3.7% | -2.7% |

- max_drawdown_avg: -8.8%　max_drawdown_p90: -19.2%
- best_cases: 6945(158.2%), 1717(62.9%), 8103(50.7%), 8039(29.3%), 3163(28.5%)
- worst_cases: 5211(-12.2%), 5490(-7.7%), 3653(-6.8%), 3086(-6.8%), 6259(-6.8%)

