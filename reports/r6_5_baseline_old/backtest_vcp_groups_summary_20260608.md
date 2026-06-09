# R6-2c Setup C — VCP Grade / Quality Groups

> Setup C 分群（VCP grade / quality bucket）。回測結果，僅供候選 / 勝率 / 風險 / 參考進場區。
> base_low 為 **proxy（近 40 日低）**，非 ComputeVCP 內部 contraction trough。
> 主統計 stop-adjusted；hold 為對照；dd 為 stop-aware realized drawdown。

- universe: 1974 stocks (cache, read-only)
- coverage: 2024-06-03 → 2026-06-08
- base_low proxy: min Low over 40 bars (NOT a ComputeVCP contraction trough)

## C_VCP_MA20_RETEST

| group | n | conf | win_20d | avg_20d | avg_60d | hold_20d | delta_20d | stop_hit | rdd_avg | rdd_p90 |
|---|---|---|---|---|---|---|---|---|---|---|
| ALL | 2592 | HIGH | 34.0% | 2.4% | 3.2% | 4.4% | -2.0% | 85.7% | -5.0% | -11.2% |
| grade=EARLY_VCP | 437 | HIGH | 37.8% | 0.6% | 1.2% | 0.8% | -0.2% | 88.3% | -3.1% | -6.9% |
| grade=STANDARD_VCP | 301 | HIGH | 29.6% | -0.1% | -0.3% | -0.1% | -0.1% | 90.7% | -3.6% | -8.2% |
| grade=HIGH_QUALITY_VCP | 1854 | HIGH | 33.9% | 3.2% | 4.3% | 6.1% | -2.8% | 84.3% | -5.6% | -12.0% |
| quality=70-79 | 1831 | HIGH | 34.2% | 2.5% | 3.2% | 4.7% | -2.2% | 85.0% | -5.1% | -11.4% |
| quality=80-89 | 533 | HIGH | 34.7% | 2.9% | 4.0% | 5.0% | -2.0% | 84.6% | -5.0% | -11.0% |
| quality=90+ | 228 | HIGH | 30.8% | 0.2% | 1.5% | 0.5% | -0.3% | 94.3% | -3.8% | -9.2% |

## C_VCP_MA60_RETEST

| group | n | conf | win_20d | avg_20d | avg_60d | hold_20d | delta_20d | stop_hit | rdd_avg | rdd_p90 |
|---|---|---|---|---|---|---|---|---|---|---|
| ALL | 1401 | HIGH | 29.1% | 1.6% | 2.0% | 3.8% | -2.2% | 90.4% | -3.7% | -8.1% |
| grade=EARLY_VCP | 223 | HIGH | 32.7% | 0.3% | 0.1% | 0.6% | -0.4% | 93.3% | -2.3% | -5.4% |
| grade=STANDARD_VCP | 200 | HIGH | 28.5% | 0.8% | 1.2% | 0.8% | -0.0% | 95.0% | -2.8% | -7.1% |
| grade=HIGH_QUALITY_VCP | 978 | HIGH | 28.4% | 2.1% | 2.6% | 5.1% | -3.0% | 88.9% | -4.2% | -8.9% |
| quality=70-79 | 1007 | HIGH | 28.1% | 1.5% | 2.2% | 3.6% | -2.1% | 90.2% | -3.9% | -8.5% |
| quality=80-89 | 294 | HIGH | 30.7% | 2.7% | 2.1% | 5.7% | -3.0% | 88.8% | -3.7% | -8.0% |
| quality=90+ | 100 | HIGH | 34.0% | -0.1% | -0.4% | 0.3% | -0.3% | 98.0% | -2.2% | -5.4% |

## C_VCP_BASE_LOW_RETEST

| group | n | conf | win_20d | avg_20d | avg_60d | hold_20d | delta_20d | stop_hit | rdd_avg | rdd_p90 |
|---|---|---|---|---|---|---|---|---|---|---|
| ALL | 288 | HIGH | 34.7% | 1.0% | 1.0% | 2.1% | -1.1% | 96.2% | -2.0% | -5.0% |
| grade=EARLY_VCP | 78 | HIGH | 41.0% | 0.5% | 0.8% | 1.2% | -0.7% | 94.9% | -1.3% | -3.3% |
| grade=STANDARD_VCP | 52 | HIGH | 34.6% | 3.0% | 1.9% | 3.1% | -0.1% | 96.2% | -1.5% | -3.3% |
| grade=HIGH_QUALITY_VCP | 158 | HIGH | 31.6% | 0.6% | 0.8% | 2.2% | -1.6% | 96.8% | -2.5% | -5.8% |
| quality=70-79 | 196 | HIGH | 33.2% | 0.5% | 0.9% | 1.7% | -1.2% | 95.9% | -2.2% | -5.6% |
| quality=80-89 | 53 | HIGH | 37.7% | 0.4% | 0.1% | 1.5% | -1.1% | 98.1% | -1.6% | -3.4% |
| quality=90+ | 39 | HIGH | 38.5% | 4.3% | 2.6% | 4.7% | -0.4% | 94.9% | -1.4% | -4.0% |

