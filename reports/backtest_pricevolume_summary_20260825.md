# Flat-Price × Volume Semantics

> 回測結果為決策支援，僅供候選 / 勝率 / 風險 / 參考進場區之用，非買賣指令。

> **主要統計採 stop-adjusted return**（horizon 前命中停損則以 stop price 計）。
> **hold-to-horizon return 僅作為對照**（忽略停損、單純持有到期）。
> stop_saved_or_hurt_delta = avg_stop_adjusted_return − avg_hold_return（正=停損保護，負=過早洗出）。

- universe: 1987 stocks (cache, read-only)
- coverage: 2024-06-11 → 2026-08-25 (538 trading days)
- warmup: 250 ; horizons: [5 10 20 60] ; entry: next_open ; stops: [BREAK_MA60 PCT_-10]
- FLAT means EXACTLY zero change — the same definition the shipped label correction uses
- volume bands 1.2 / 0.8 are the SHIPPED ones (analyzeVolume and its score table), not a private copy
- READ AS DIFFERENCES FROM PV_BASELINE_ALL_BARS: the universe is survivorship-biased, so absolute returns are optimistic for every row alike
- the question: does PV_FLAT_VOLUME_EXPANSION behave like PV_DOWN_VOLUME_EXPANSION (candidate A: keep scoring flat as down), like PV_UP_VOLUME_EXPANSION (candidate C), or like neither (candidate B: its own neutral bucket)?
- PV_NEAR_FLAT_* rows exist to test the band the correction deliberately did NOT widen: if a −0.3% session behaves like an exactly-flat one, a wider neutral band is worth proposing; if it behaves like the decline it is currently called, the strict definition was right
- no scoring change may be made from this run without the difference being larger than the noise these overlapping bar-level observations can support

## PV_BASELINE_ALL_BARS

- sample_count: 60931　confidence: HIGH　stop_hit_rate: 92.4%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.1% | -0.1% | -0.6% | 42.0% | 0.0% | -0.1% |
| 10d | 37.6% | 0.2% | -0.8% | 42.9% | 0.6% | -0.4% |
| 20d | 34.7% | 0.6% | -1.1% | 43.2% | 1.5% | -0.8% |
| 60d | 29.8% | 1.5% | -1.5% | 46.9% | 7.4% | -5.9% |

- max_drawdown_avg: -12.8%　max_drawdown_p90: -27.9%
- best_cases: 8291(563.3%), 8291(563.0%), 8291(444.2%), 8291(345.4%), 5386(274.9%)
- worst_cases: 7780(-90.4%), 0052(-86.3%), 0052(-86.0%), 0052(-85.7%), 6610(-62.5%)

## PV_FLAT_VOLUME_EXPANSION

- sample_count: 4603　confidence: HIGH　stop_hit_rate: 95.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 37.3% | -0.0% | -0.3% | 41.2% | 0.1% | -0.1% |
| 10d | 35.6% | -0.0% | -0.4% | 41.9% | 0.3% | -0.3% |
| 20d | 33.5% | 0.2% | -0.5% | 42.9% | 0.8% | -0.6% |
| 60d | 30.4% | 0.8% | -0.6% | 44.2% | 3.8% | -2.9% |

- max_drawdown_avg: -9.3%　max_drawdown_p90: -20.1%
- best_cases: 4304(233.3%), 7610(116.5%), 6182(105.4%), 3576(103.4%), 6624(98.2%)
- worst_cases: 0052(-86.3%), 1435(-18.2%), 3609(-17.8%), 2327(-17.4%), 3234(-16.9%)

## PV_DOWN_VOLUME_EXPANSION

- sample_count: 25519　confidence: HIGH　stop_hit_rate: 94.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.9% | -0.1% | -0.5% | 43.2% | 0.3% | -0.3% |
| 10d | 38.0% | 0.1% | -0.6% | 43.9% | 0.7% | -0.6% |
| 20d | 35.7% | 0.5% | -0.8% | 43.8% | 1.4% | -0.9% |
| 60d | 31.8% | 1.2% | -1.1% | 46.8% | 6.5% | -5.3% |

- max_drawdown_avg: -12.0%　max_drawdown_p90: -26.1%
- best_cases: 5386(240.4%), 3026(212.6%), 6861(201.3%), 2492(161.3%), 6983(161.1%)
- worst_cases: 0052(-86.4%), 0052(-85.8%), 6908(-34.6%), 6908(-29.1%), 6884(-23.5%)

## PV_UP_VOLUME_EXPANSION

- sample_count: 35632　confidence: HIGH　stop_hit_rate: 90.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 37.9% | -0.1% | -1.0% | 40.3% | -0.1% | -0.0% |
| 10d | 36.2% | 0.1% | -1.3% | 41.2% | 0.5% | -0.3% |
| 20d | 33.0% | 0.8% | -1.8% | 42.9% | 1.8% | -0.9% |
| 60d | 26.8% | 2.0% | -2.7% | 46.8% | 8.3% | -6.2% |

- max_drawdown_avg: -13.6%　max_drawdown_p90: -29.1%
- best_cases: 8291(563.3%), 8291(563.0%), 8291(345.4%), 8291(264.7%), 5386(240.4%)
- worst_cases: 0052(-86.3%), 0052(-85.7%), 6610(-62.5%), 6610(-61.8%), 6908(-35.6%)

## PV_FLAT_VOLUME_SHRINK

- sample_count: 15846　confidence: HIGH　stop_hit_rate: 96.2%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 35.0% | -0.6% | -0.5% | 38.1% | -0.8% | 0.2% |
| 10d | 33.7% | -0.6% | -0.6% | 39.0% | -0.6% | 0.0% |
| 20d | 31.2% | -0.6% | -0.8% | 39.7% | -0.3% | -0.3% |
| 60d | 28.5% | -0.4% | -0.9% | 43.2% | 3.3% | -3.7% |

- max_drawdown_avg: -10.8%　max_drawdown_p90: -23.5%
- best_cases: 4304(196.7%), 6265(165.7%), 3481(132.3%), 2243(132.2%), 2454(124.9%)
- worst_cases: 6236(-25.8%), 6236(-25.8%), 6236(-25.8%), 4195(-25.0%), 3026(-20.5%)

## PV_DOWN_VOLUME_SHRINK

- sample_count: 33650　confidence: HIGH　stop_hit_rate: 95.1%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.3% | -0.1% | -0.5% | 42.4% | 0.0% | -0.1% |
| 10d | 37.4% | -0.0% | -0.6% | 42.8% | 0.3% | -0.4% |
| 20d | 34.5% | 0.1% | -0.8% | 42.8% | 1.0% | -0.8% |
| 60d | 31.2% | 0.8% | -1.0% | 46.6% | 6.8% | -6.0% |

- max_drawdown_avg: -11.9%　max_drawdown_p90: -25.5%
- best_cases: 6658(158.3%), 8047(154.4%), 6182(152.9%), 6861(149.4%), 3026(141.4%)
- worst_cases: 7780(-89.7%), 0052(-86.0%), 6610(-61.3%), 6610(-56.9%), 6908(-30.8%)

## PV_UP_VOLUME_SHRINK

- sample_count: 32881　confidence: HIGH　stop_hit_rate: 94.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.6% | -0.0% | -0.5% | 43.5% | 0.2% | -0.2% |
| 10d | 36.9% | 0.1% | -0.7% | 43.4% | 0.6% | -0.5% |
| 20d | 33.7% | 0.4% | -0.9% | 42.9% | 1.1% | -0.7% |
| 60d | 30.2% | 0.9% | -1.1% | 46.7% | 7.0% | -6.0% |

- max_drawdown_avg: -12.0%　max_drawdown_p90: -26.2%
- best_cases: 8291(444.2%), 6217(205.5%), 5386(202.0%), 6861(199.2%), 8047(189.9%)
- worst_cases: 7780(-90.4%), 0052(-85.9%), 6610(-60.1%), 6225(-25.3%), 2072(-25.1%)

## PV_FLAT_ANY_VOLUME

- sample_count: 20553　confidence: HIGH　stop_hit_rate: 95.7%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 36.2% | -0.4% | -0.4% | 39.8% | -0.6% | 0.1% |
| 10d | 34.6% | -0.4% | -0.6% | 40.1% | -0.4% | -0.1% |
| 20d | 32.2% | -0.3% | -0.7% | 41.1% | 0.1% | -0.4% |
| 60d | 29.1% | 0.0% | -0.9% | 44.2% | 3.9% | -3.9% |

- max_drawdown_avg: -10.6%　max_drawdown_p90: -23.2%
- best_cases: 4304(233.3%), 6265(165.7%), 5464(160.6%), 2483(136.4%), 6715(135.3%)
- worst_cases: 0052(-86.3%), 6236(-25.8%), 6236(-25.8%), 6236(-25.8%), 4195(-25.0%)

## PV_NEAR_FLAT_DOWN_0_TO_05

- sample_count: 22971　confidence: HIGH　stop_hit_rate: 95.3%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 38.8% | -0.1% | -0.4% | 41.4% | -0.1% | -0.0% |
| 10d | 36.8% | -0.0% | -0.5% | 42.1% | 0.2% | -0.2% |
| 20d | 34.3% | 0.1% | -0.7% | 42.5% | 0.7% | -0.6% |
| 60d | 31.2% | 0.6% | -0.8% | 44.8% | 4.5% | -3.9% |

- max_drawdown_avg: -10.4%　max_drawdown_p90: -22.1%
- best_cases: 6983(176.7%), 8042(165.9%), 8047(165.0%), 5386(151.2%), 4908(139.3%)
- worst_cases: 0052(-85.7%), 6610(-55.9%), 7818(-35.1%), 6908(-34.6%), 6908(-28.7%)

## PV_NEAR_FLAT_UP_0_TO_05

- sample_count: 22952　confidence: HIGH　stop_hit_rate: 95.0%

| horizon | win_rate | avg_return | median_return | hold_win | hold_avg | stop_delta |
|---|---|---|---|---|---|---|
| 5d | 39.1% | -0.1% | -0.4% | 42.3% | -0.0% | -0.0% |
| 10d | 37.0% | 0.0% | -0.5% | 42.2% | 0.2% | -0.2% |
| 20d | 34.5% | 0.2% | -0.7% | 42.6% | 0.7% | -0.6% |
| 60d | 31.1% | 0.7% | -0.9% | 45.2% | 4.7% | -4.0% |

- max_drawdown_avg: -10.4%　max_drawdown_p90: -22.4%
- best_cases: 4304(208.9%), 8047(167.7%), 4741(139.1%), 6217(134.0%), 2434(127.9%)
- worst_cases: 0052(-86.4%), 0052(-85.8%), 6610(-59.2%), 6610(-57.3%), 6908(-27.5%)

