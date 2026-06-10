# R6-2d Setup D — Crash-Regime Survivor Case Study

> **Setup D 是殺盤事件研究，不是高信心策略回測。**
> 本次 event_count=3，事件數仍極少，且主要集中在 2025 春季與 2026-03 邊際事件。
> 結果僅供 regime case study，不可外推；confidence 永遠 LOW。

- universe: 1967 stocks (cache, read-only)
- coverage: 2024-06-11 → 2026-06-08
- regime: 0050 20d return ≤ -8% ; warmup 120 ; horizons [5 10 20 60] (60d auxiliary)
- relative_return_vs_market_20d ≥ +5pp ; breadth_below_ma20 = context only (not a hard gate)
- regime events detected in full series: 4 (pre-warmup events yield no entries)
- event_count: **3**　regime_date_range: 2025-03-13~2026-03-31　proxy_symbol: 0050
- confidence: **LOW**　avg market_proxy_return_20d: -9.4%　avg relative_return_vs_market_20d: 14.0%

## D_CRASH_SURVIVOR（RS≥70 + 相對抗跌 ≥5pp）

- sample_count: 537　confidence: LOW　stop_hit_rate: 91.8%

| horizon | win | avg | median | hold_avg | stop_delta | rdd_avg | rdd_p90 |
|---|---|---|---|---|---|---|---|
| 5d | 46.7% | 0.9% | 0.0% | 1.3% | -0.4% | -5.6% | -12.5% |
| 10d | 38.7% | 0.6% | -1.2% | 0.4% | 0.2% | -5.6% | -12.5% |
| 20d | 33.3% | 1.1% | -2.3% | 0.8% | 0.3% | -5.6% | -12.5% |
| 60d | 23.9% | -2.4% | -3.3% | -1.8% | -0.5% | -5.6% | -12.5% |

## Cohort：HIGH_RS vs LOW_RS（同 regime、僅 near-MA20 候選，依 RS 切分）

回答「殺盤時 RS 高是否較抗跌」。

| cohort | n | win_20d | avg_20d | hold_20d | stop_hit | rdd_avg | rdd_p90 | avg_rel_ret_20d |
|---|---|---|---|---|---|---|---|---|
| HIGH_RS | 1624 | 41.6% | 0.7% | 2.3% | 94.2% | -4.2% | -11.2% | 6.9% |
| LOW_RS | 3519 | 49.1% | 0.1% | 1.1% | 98.8% | -2.1% | -6.5% | -0.4% |

