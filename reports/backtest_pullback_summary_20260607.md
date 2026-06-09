# R6 Pullback Backtest

> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。

> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。
> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。
> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。

- universe: 1973 stocks (cache, read-only)
- coverage: 2024-06-03 → 2026-06-05 (487 trading days)
- RS panel dates: 364 (lookback 120d)
- warmup: 250 bars (52w) ; horizons: [5 10 20 60] ; entry: next_open
- R6-2b: Setup A (MA20/MA60) + Setup B (pullback sweep) wired. Setup C/D not yet.
- 60d-horizon samples are fewer than 5/10/20d (forward window limited by data end).

## A_MA20_PULLBACK

- sample_count: 2384　confidence: HIGH　stop_hit_rate: 79.5%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.9% | 0.8% | -0.4% | 47.0% | 0.9% | -0.1% |
| 10d | 42.8% | 1.8% | -0.9% | 49.3% | 2.4% | -0.6% |
| 20d | 35.9% | 3.0% | -2.2% | 48.2% | 4.4% | -1.4% |
| 60d | 24.7% | 5.0% | -3.3% | 50.4% | 13.1% | -8.0% |

- max_drawdown_avg: -10.6%　max_drawdown_p90: -22.6%
- best_cases: 8472(174.4%), 6265(165.7%), 8472(159.2%), 6945(158.2%), 3054(154.9%)
- worst_cases: 6610(-58.8%), 3219(-18.9%), 1591(-17.6%), 3609(-17.0%), 6126(-16.9%)

## A_MA60_PULLBACK

- sample_count: 804　confidence: HIGH　stop_hit_rate: 92.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.4% | 0.4% | -0.4% | 46.0% | 0.5% | -0.1% |
| 10d | 31.0% | 0.8% | -0.9% | 46.2% | 1.4% | -0.6% |
| 20d | 26.2% | 1.3% | -1.2% | 43.4% | 2.8% | -1.5% |
| 60d | 19.5% | 1.5% | -1.4% | 41.2% | 4.2% | -2.7% |

- max_drawdown_avg: -9.4%　max_drawdown_p90: -20.5%
- best_cases: 8472(174.4%), 6945(158.2%), 4768(81.0%), 2491(71.4%), 1595(71.3%)
- worst_cases: 5309(-13.4%), 3313(-13.2%), 2380(-10.1%), 3691(-9.3%), 4722(-9.2%)

## B_PULLBACK_5 — pullback 5%

- sample_count: 7266　confidence: HIGH　stop_hit_rate: 72.8%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.4% | 1.1% | -0.8% | 47.0% | 1.4% | -0.3% |
| 10d | 43.9% | 2.4% | -1.3% | 51.4% | 3.6% | -1.2% |
| 20d | 38.8% | 4.2% | -2.7% | 52.7% | 7.3% | -3.1% |
| 60d | 27.4% | 5.3% | -5.3% | 57.3% | 21.0% | -15.7% |

- max_drawdown_avg: -12.6%　max_drawdown_p90: -25.6%
- best_cases: 5386(240.4%), 3581(197.3%), 6861(180.9%), 6217(165.1%), 6945(159.7%)
- worst_cases: 7780(-89.7%), 6610(-59.7%), 6610(-57.7%), 2755(-24.5%), 6983(-22.6%)

## B_PULLBACK_8 — pullback 8%

- sample_count: 5282　confidence: HIGH　stop_hit_rate: 72.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 44.7% | 1.3% | -0.8% | 47.7% | 1.7% | -0.4% |
| 10d | 44.0% | 2.6% | -1.3% | 52.5% | 4.0% | -1.4% |
| 20d | 38.3% | 4.1% | -2.9% | 53.1% | 7.6% | -3.5% |
| 60d | 27.0% | 5.0% | -5.5% | 58.4% | 22.5% | -17.5% |

- max_drawdown_avg: -12.7%　max_drawdown_p90: -25.7%
- best_cases: 3581(197.3%), 6861(180.9%), 6265(155.6%), 7610(154.0%), 6173(150.3%)
- worst_cases: 6610(-59.7%), 6610(-57.7%), 6983(-22.6%), 1528(-21.5%), 3081(-21.0%)

## B_PULLBACK_10 — pullback 10%

- sample_count: 3996　confidence: HIGH　stop_hit_rate: 74.0%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 43.9% | 1.1% | -0.9% | 47.0% | 1.5% | -0.4% |
| 10d | 42.7% | 2.5% | -1.6% | 51.7% | 4.1% | -1.6% |
| 20d | 37.5% | 3.9% | -3.3% | 53.1% | 7.7% | -3.8% |
| 60d | 26.8% | 4.9% | -5.8% | 59.4% | 22.9% | -18.0% |

- max_drawdown_avg: -12.8%　max_drawdown_p90: -26.2%
- best_cases: 6861(180.9%), 6265(155.6%), 6173(150.3%), 6830(138.1%), 4764(137.8%)
- worst_cases: 6610(-57.3%), 1528(-21.5%), 3054(-20.8%), 6983(-19.9%), 6830(-18.8%)

## B_PULLBACK_15 — pullback 15%

- sample_count: 1782　confidence: HIGH　stop_hit_rate: 75.1%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 46.1% | 1.5% | -0.7% | 48.7% | 1.9% | -0.4% |
| 10d | 43.1% | 2.4% | -1.6% | 52.2% | 4.1% | -1.7% |
| 20d | 35.9% | 3.2% | -3.6% | 52.6% | 7.5% | -4.3% |
| 60d | 25.2% | 2.7% | -5.7% | 60.8% | 23.1% | -20.4% |

- max_drawdown_avg: -13.0%　max_drawdown_p90: -25.7%
- best_cases: 6861(219.1%), 6173(189.0%), 4764(137.8%), 5386(132.6%), 3026(127.4%)
- worst_cases: 6610(-56.9%), 6983(-26.8%), 8358(-19.4%), 6217(-19.0%), 3491(-18.4%)

## B_PULLBACK_20 — pullback 20%

- sample_count: 557　confidence: HIGH　stop_hit_rate: 77.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 45.8% | 1.6% | -0.4% | 52.1% | 2.5% | -0.8% |
| 10d | 41.9% | 2.1% | -1.5% | 54.8% | 4.6% | -2.5% |
| 20d | 36.1% | 3.6% | -3.4% | 57.7% | 9.2% | -5.6% |
| 60d | 24.8% | 1.4% | -5.2% | 61.4% | 24.3% | -22.9% |

- max_drawdown_avg: -12.8%　max_drawdown_p90: -25.2%
- best_cases: 6861(180.9%), 3026(162.1%), 4764(137.8%), 3555(105.0%), 6426(100.9%)
- worst_cases: 6610(-57.3%), 6983(-26.8%), 4195(-21.8%), 3054(-21.4%), 6217(-17.0%)

