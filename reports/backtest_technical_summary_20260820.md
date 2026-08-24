# R14 Technical Indicator Condition Comparison

> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。

> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。
> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。
> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。

- universe: 1986 stocks (cache, read-only)
- coverage: 2024-06-11 → 2026-08-19 (534 trading days)
- warmup: 250 ; horizons: [5 10 20 60] ; entry: next_open ; stops: [BREAK_MA60 PCT_-10]
- parameters: read from technical.DefaultConfig() — the SHIPPED values, not a tuned copy
- READ AS DIFFERENCES FROM BASELINE_ALL_BARS: the universe is survivorship-biased (delisted names absent), so absolute returns are optimistic for every row alike
- the falsifiable claims: ADX_STRONG_BULLISH must beat ADX_WEAK; MACD_ABOVE_SIGNAL_ACCELERATING must beat MACD_ABOVE_SIGNAL; KELTNER_BREAKOUT_ADX_CONFIRMED must beat KELTNER_BREAKOUT_UNCONFIRMED; RSI_OVERBOUGHT decides continuation vs mean reversion on its own numbers
- nothing here may be used to tune a threshold: the parameters are fixed before the run, and a result that falsifies a hypothesis is the point of the exercise

## BASELINE_ALL_BARS

- sample_count: 61175　confidence: HIGH　stop_hit_rate: 92.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.6% | -0.0% | -0.5% | 42.7% | 0.1% | -0.1% |
| 10d | 37.6% | 0.2% | -0.8% | 42.9% | 0.6% | -0.4% |
| 20d | 34.7% | 0.7% | -1.1% | 43.3% | 1.5% | -0.9% |
| 60d | 29.8% | 1.5% | -1.5% | 46.7% | 7.5% | -6.0% |

- max_drawdown_avg: -12.9%　max_drawdown_p90: -28.1%
- best_cases: 8291(563.3%), 8291(563.0%), 8291(562.7%), 8291(444.2%), 8291(345.4%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-86.0%), 0052(-85.7%), 6610(-62.5%)

## ADX_STRONG_BULLISH

- sample_count: 19948　confidence: HIGH　stop_hit_rate: 87.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.7% | -0.1% | -1.2% | 41.3% | -0.0% | -0.0% |
| 10d | 37.5% | 0.3% | -2.0% | 41.6% | 0.7% | -0.3% |
| 20d | 33.8% | 1.3% | -3.5% | 43.0% | 2.3% | -0.9% |
| 60d | 25.2% | 3.1% | -5.6% | 48.5% | 10.8% | -7.7% |

- max_drawdown_avg: -16.2%　max_drawdown_p90: -34.3%
- best_cases: 5386(274.9%), 5386(240.4%), 5386(236.6%), 5386(208.0%), 6217(205.5%)
- worst_cases: 7780(-89.7%), 0052(-86.3%), 0052(-86.0%), 0052(-85.7%), 6610(-62.5%)

## ADX_WEAK

- sample_count: 24161　confidence: HIGH　stop_hit_rate: 94.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.0% | -0.1% | -0.5% | 42.5% | 0.0% | -0.1% |
| 10d | 35.8% | -0.1% | -0.7% | 42.7% | 0.3% | -0.4% |
| 20d | 33.5% | 0.2% | -0.9% | 44.3% | 1.3% | -1.1% |
| 60d | 30.3% | 0.7% | -1.1% | 48.5% | 7.2% | -6.4% |

- max_drawdown_avg: -11.1%　max_drawdown_p90: -23.7%
- best_cases: 2492(160.8%), 3581(150.0%), 2327(145.6%), 2466(133.3%), 3090(131.5%)
- worst_cases: 4195(-23.2%), 2540(-22.1%), 2402(-22.0%), 6613(-20.8%), 6596(-20.5%)

## ADX_STRONG_BEARISH

- sample_count: 7619　confidence: HIGH　stop_hit_rate: 98.4%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 43.1% | 0.1% | -0.2% | 48.2% | 0.6% | -0.5% |
| 10d | 41.4% | 0.1% | -0.3% | 47.2% | 0.9% | -0.9% |
| 20d | 39.8% | -0.0% | -0.3% | 44.4% | 0.8% | -0.8% |
| 60d | 38.9% | 0.1% | -0.4% | 45.4% | 3.7% | -3.5% |

- max_drawdown_avg: -10.3%　max_drawdown_p90: -23.0%
- best_cases: 2486(112.5%), 6588(99.9%), 1435(97.7%), 7795(87.9%), 6155(75.2%)
- worst_cases: 6908(-27.5%), 5481(-26.0%), 6727(-18.3%), 2303(-18.0%), 3090(-17.4%)

## MACD_ABOVE_SIGNAL

- sample_count: 40210　confidence: HIGH　stop_hit_rate: 90.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.0% | -0.1% | -0.8% | 41.2% | -0.1% | -0.1% |
| 10d | 36.3% | 0.1% | -1.0% | 41.8% | 0.4% | -0.4% |
| 20d | 33.6% | 0.8% | -1.5% | 44.0% | 1.9% | -1.1% |
| 60d | 27.4% | 2.0% | -2.2% | 47.7% | 8.5% | -6.5% |

- max_drawdown_avg: -13.3%　max_drawdown_p90: -29.0%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-85.7%), 6610(-62.5%), 6908(-35.6%)

## MACD_ABOVE_SIGNAL_ACCELERATING

- sample_count: 33129　confidence: HIGH　stop_hit_rate: 90.6%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 37.8% | -0.2% | -0.9% | 40.7% | -0.1% | -0.0% |
| 10d | 35.9% | 0.0% | -1.1% | 41.3% | 0.4% | -0.3% |
| 20d | 33.2% | 0.8% | -1.6% | 43.7% | 1.8% | -1.0% |
| 60d | 27.0% | 2.1% | -2.3% | 48.2% | 8.8% | -6.7% |

- max_drawdown_avg: -13.4%　max_drawdown_p90: -29.2%
- best_cases: 5386(240.4%), 5386(208.0%), 5386(196.3%), 5386(192.1%), 8047(189.9%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 6908(-35.6%), 6908(-34.7%), 6908(-31.8%)

## MACD_GOLDEN_CROSS

- sample_count: 14874　confidence: HIGH　stop_hit_rate: 93.6%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 37.1% | -0.1% | -0.7% | 40.6% | -0.0% | -0.0% |
| 10d | 35.0% | -0.1% | -0.9% | 40.2% | 0.1% | -0.2% |
| 20d | 32.7% | 0.4% | -1.1% | 42.6% | 1.1% | -0.8% |
| 60d | 29.3% | 1.4% | -1.3% | 48.7% | 8.2% | -6.8% |

- max_drawdown_avg: -11.7%　max_drawdown_p90: -25.5%
- best_cases: 2492(176.5%), 6983(160.5%), 6861(154.3%), 3581(150.0%), 2434(135.5%)
- worst_cases: 0052(-86.3%), 0052(-86.1%), 6610(-62.5%), 6983(-22.6%), 6515(-21.2%)

## RSI_OVERBOUGHT

- sample_count: 11792　confidence: HIGH　stop_hit_rate: 85.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.6% | -0.3% | -1.9% | 40.7% | -0.3% | -0.0% |
| 10d | 38.9% | 0.3% | -2.9% | 42.9% | 0.9% | -0.5% |
| 20d | 35.4% | 1.5% | -6.4% | 44.1% | 2.8% | -1.3% |
| 60d | 25.6% | 3.6% | -10.0% | 49.5% | 12.2% | -8.7% |

- max_drawdown_avg: -18.0%　max_drawdown_p90: -36.5%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 5321(-26.0%)

## RSI_OVERBOUGHT_IN_STRONG_TREND

- sample_count: 9420　confidence: HIGH　stop_hit_rate: 86.0%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.8% | -0.3% | -2.0% | 41.1% | -0.2% | -0.1% |
| 10d | 38.9% | 0.4% | -3.0% | 43.2% | 1.0% | -0.6% |
| 20d | 35.6% | 1.7% | -7.2% | 44.7% | 3.0% | -1.3% |
| 60d | 25.9% | 4.0% | -10.2% | 49.9% | 12.8% | -8.9% |

- max_drawdown_avg: -18.8%　max_drawdown_p90: -37.9%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-89.7%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 5321(-26.0%)

## RSI_OVERSOLD

- sample_count: 4422　confidence: HIGH　stop_hit_rate: 99.9%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 46.1% | 0.2% | 0.0% | 50.5% | 1.1% | -0.9% |
| 10d | 46.0% | 0.2% | 0.0% | 49.3% | 1.5% | -1.3% |
| 20d | 46.0% | 0.2% | 0.0% | 44.9% | 0.8% | -0.6% |
| 60d | 46.0% | 0.1% | 0.0% | 45.3% | 1.9% | -1.8% |

- max_drawdown_avg: -9.7%　max_drawdown_p90: -21.1%
- best_cases: 1435(97.7%), 1435(62.4%), 6229(44.6%), 6610(42.3%), 8905(23.5%)
- worst_cases: 3167(-17.5%), 2233(-16.8%), 2327(-15.3%), 6785(-14.5%), 8162(-14.3%)

## KELTNER_BREAKOUT

- sample_count: 17440　confidence: HIGH　stop_hit_rate: 85.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.9% | -0.2% | -1.7% | 41.2% | -0.2% | -0.0% |
| 10d | 38.8% | 0.3% | -2.7% | 42.8% | 0.8% | -0.5% |
| 20d | 35.3% | 1.6% | -5.3% | 44.4% | 2.8% | -1.2% |
| 60d | 25.8% | 3.8% | -8.1% | 49.9% | 12.1% | -8.3% |

- max_drawdown_avg: -17.0%　max_drawdown_p90: -35.2%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 6908(-34.7%)

## KELTNER_BREAKOUT_ADX_CONFIRMED

- sample_count: 12146　confidence: HIGH　stop_hit_rate: 85.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.3% | -0.2% | -1.8% | 41.5% | -0.2% | -0.0% |
| 10d | 39.0% | 0.4% | -2.9% | 42.9% | 0.9% | -0.5% |
| 20d | 35.5% | 1.7% | -6.2% | 44.5% | 2.9% | -1.2% |
| 60d | 26.1% | 4.0% | -10.0% | 50.1% | 12.6% | -8.7% |

- max_drawdown_avg: -18.2%　max_drawdown_p90: -37.4%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-89.7%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 5321(-26.0%)

## KELTNER_BREAKOUT_UNCONFIRMED

- sample_count: 3098　confidence: HIGH　stop_hit_rate: 84.8%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.9% | -0.2% | -1.4% | 41.3% | -0.1% | -0.0% |
| 10d | 38.9% | 0.1% | -2.3% | 42.9% | 0.4% | -0.4% |
| 20d | 35.3% | 1.3% | -4.1% | 44.3% | 2.3% | -1.0% |
| 60d | 25.3% | 3.1% | -5.6% | 48.1% | 10.0% | -7.0% |

- max_drawdown_avg: -13.6%　max_drawdown_p90: -27.4%
- best_cases: 2492(160.8%), 3581(150.0%), 2327(145.6%), 6173(128.4%), 3581(128.3%)
- worst_cases: 2540(-22.1%), 2402(-22.0%), 6504(-20.1%), 3073(-20.0%), 8277(-19.1%)

## PIVOT_NEAR_RESISTANCE

- sample_count: 41499　confidence: HIGH　stop_hit_rate: 93.0%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.4% | -0.0% | -0.6% | 43.1% | 0.1% | -0.1% |
| 10d | 37.4% | 0.1% | -0.8% | 43.1% | 0.5% | -0.4% |
| 20d | 34.8% | 0.6% | -1.0% | 44.1% | 1.4% | -0.8% |
| 60d | 30.1% | 1.5% | -1.4% | 48.5% | 7.7% | -6.3% |

- max_drawdown_avg: -12.0%　max_drawdown_p90: -26.1%
- best_cases: 3581(249.2%), 5386(236.6%), 5386(236.1%), 6861(199.2%), 8047(189.9%)
- worst_cases: 7780(-89.8%), 0052(-86.4%), 0052(-85.7%), 6610(-62.5%), 6610(-60.9%)

## PIVOT_NEAR_SUPPORT

- sample_count: 39218　confidence: HIGH　stop_hit_rate: 94.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.2% | 0.0% | -0.4% | 43.6% | 0.3% | -0.2% |
| 10d | 38.0% | 0.1% | -0.6% | 43.4% | 0.5% | -0.4% |
| 20d | 35.3% | 0.4% | -0.9% | 44.2% | 1.4% | -1.0% |
| 60d | 31.4% | 1.1% | -1.1% | 48.0% | 7.3% | -6.2% |

- max_drawdown_avg: -11.7%　max_drawdown_p90: -25.2%
- best_cases: 3054(181.2%), 3026(178.9%), 6983(176.7%), 4764(169.3%), 8047(167.7%)
- worst_cases: 0052(-86.3%), 0052(-85.8%), 0052(-85.7%), 6610(-59.7%), 6610(-57.7%)

## CONTEXT_STRONG_BULLISH_TREND

- sample_count: 17075　confidence: HIGH　stop_hit_rate: 87.6%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.9% | -0.2% | -1.3% | 40.5% | -0.2% | -0.0% |
| 10d | 37.4% | 0.3% | -1.9% | 41.4% | 0.6% | -0.4% |
| 20d | 34.2% | 1.3% | -3.4% | 43.1% | 2.3% | -1.0% |
| 60d | 25.9% | 3.2% | -5.7% | 48.0% | 10.6% | -7.4% |

- max_drawdown_avg: -16.2%　max_drawdown_p90: -34.4%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-89.7%), 0052(-86.3%), 0052(-85.7%), 6610(-62.5%), 6908(-35.6%)

## CONTEXT_TREND_EXPANSION

- sample_count: 12146　confidence: HIGH　stop_hit_rate: 85.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 40.3% | -0.2% | -1.8% | 41.5% | -0.2% | -0.0% |
| 10d | 39.0% | 0.4% | -2.9% | 42.9% | 0.9% | -0.5% |
| 20d | 35.5% | 1.7% | -6.2% | 44.5% | 2.9% | -1.2% |
| 60d | 26.1% | 4.0% | -10.0% | 50.1% | 12.6% | -8.7% |

- max_drawdown_avg: -18.2%　max_drawdown_p90: -37.4%
- best_cases: 5386(240.4%), 5386(236.6%), 5386(208.0%), 5386(196.3%), 5386(192.1%)
- worst_cases: 7780(-89.7%), 0052(-86.3%), 0052(-85.7%), 6908(-35.6%), 5321(-26.0%)

## CONTEXT_UNCONFIRMED_BREAKOUT

- sample_count: 3098　confidence: HIGH　stop_hit_rate: 84.8%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.9% | -0.2% | -1.4% | 41.3% | -0.1% | -0.0% |
| 10d | 38.9% | 0.1% | -2.3% | 42.9% | 0.4% | -0.4% |
| 20d | 35.3% | 1.3% | -4.1% | 44.3% | 2.3% | -1.0% |
| 60d | 25.3% | 3.1% | -5.6% | 48.1% | 10.0% | -7.0% |

- max_drawdown_avg: -13.6%　max_drawdown_p90: -27.4%
- best_cases: 2492(160.8%), 3581(150.0%), 2327(145.6%), 6173(128.4%), 3581(128.3%)
- worst_cases: 2540(-22.1%), 2402(-22.0%), 6504(-20.1%), 3073(-20.0%), 8277(-19.1%)

