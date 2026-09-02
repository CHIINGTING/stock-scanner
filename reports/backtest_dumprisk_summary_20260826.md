# Next-Day Dump Risk — T+1 / T+2 Downside

- universe: 1987 stocks (cache, read-only)
- coverage: 2024-06-11 → 2026-08-25 (538 trading days in the cache; 286 distinct SIGNAL dates)
- warmup: 250 bars
- reference price: the SIGNAL CLOSE on day T — not a next-open entry, because the overnight gap is the risk being measured
- horizon: T+1 and T+2 only. The 5/10/20/60d horizons elsewhere in this package answer a different question and are not used here
- bands: large gain and volume spike are scanner.DefaultPriceMoveThresholds() — the SHIPPED values, not a tuned copy
- cuts: close-strength, upper-shadow and day-trading cuts are TERCILES OF THE OBSERVED trigger population, computed at run time and printed above — not chosen in advance
- selloff: defined on the BASELINE distribution of T+1 MAE in ATR, so the label means 'worse than all but the stated percentile of ordinary days' rather than a fixed −3%
- READ AS DIFFERENCES FROM DUMP_BASELINE_ALL_BARS: the universe is survivorship-biased (delisted names absent), so absolute returns are optimistic for every row alike
- INDEPENDENCE: observations are per stock-bar and are NOT independent — on a strong day hundreds of stocks trigger together. Read unique_dates and max_date_share before believing any n
- NO SCORING CHANGE may be made from this run: it is a shadow-layer study

## Thresholds (derived from the observed distribution, not chosen)

| cut | value | source |
|---|---|---|
| large gain | 3.00% | scanner.DefaultPriceMoveThresholds — the SHIPPED band |
| volume spike | 1.20x | analyzeVolume's shipped expansion band |
| weak close (CLV <=) | 0.7308 | lower tercile of the trigger population |
| strong close (CLV >=) | 1.0000 | upper tercile of the trigger population |
| long upper shadow (>=) | 0.2500 | upper tercile of the trigger population |
| high day-trading | — | no 當沖 archive covering this period |
| selloff (T+1 MAE <=) | -1.428 ATR | the 5.0th percentile of the BASELINE distribution |

## Feature distributions

| feature | n | p10 | p25 | p33 | p50 | p67 | p75 | p90 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| all/close_location_value | 443898 | 0.000 | 0.154 | 0.246 | 0.424 | 0.625 | 0.750 | 1.000 |
| all/upper_shadow_pct | 443898 | 0.000 | 0.000 | 0.000 | 0.182 | 0.333 | 0.419 | 0.630 |
| all/price_change_pct | 443898 | -2.847 | -1.237 | -0.759 | 0.000 | 0.513 | 1.009 | 2.987 |
| all/volume_ratio | 443898 | 0.332 | 0.507 | 0.595 | 0.788 | 1.049 | 1.242 | 1.987 |
| all/t1_mae_atr | 443898 | -1.110 | -0.699 | -0.565 | -0.355 | -0.167 | -0.059 | 0.133 |
| trigger/close_location_value | 26008 | 0.475 | 0.663 | 0.731 | 0.853 | 1.000 | 1.000 | 1.000 |
| trigger/upper_shadow_pct | 26008 | 0.000 | 0.000 | 0.000 | 0.132 | 0.250 | 0.317 | 0.483 |

## T+1 / T+2 outcomes by condition

| condition | n | dates | stocks | max date % | T+1 open | T+1 close | T+1 low | T+2 close | gap-down % | neg-close % | selloff % | T+1 MAE (ATR) | T+2 MAE (ATR) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| DUMP_BASELINE_ALL_BARS | 443898 | 286 | 1975 | 0.4 | 0.284 | 0.065 | -1.437 | 0.143 | 29.22 | 48.36 | 5.00 | -0.423 | -0.659 |
| DUMP_LARGE_GAIN | 44174 | 239 | 1903 | 2.0 | 0.891 | 0.528 | -2.047 | 0.669 | 30.15 | 49.83 | 8.88 | -0.467 | -0.753 |
| DUMP_VOLUME_SPIKE | 117865 | 265 | 1975 | 1.1 | 0.392 | 0.116 | -1.666 | 0.227 | 29.21 | 49.08 | 8.15 | -0.505 | -0.767 |
| DUMP_WEAK_CLOSE | 329264 | 283 | 1975 | 0.5 | 0.292 | 0.050 | -1.411 | 0.146 | 27.20 | 47.70 | 4.31 | -0.397 | -0.622 |
| DUMP_LONG_UPPER_SHADOW | 190470 | 272 | 1975 | 0.7 | 0.338 | 0.099 | -1.394 | 0.173 | 26.64 | 47.66 | 4.21 | -0.393 | -0.621 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE | 8845 | 235 | 1684 | 1.1 | 2.043 | 1.470 | -1.767 | 1.655 | 23.49 | 43.67 | 15.35 | -0.430 | -0.785 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE | 8662 | 237 | 1701 | 1.0 | 0.583 | 0.043 | -2.308 | 0.138 | 31.23 | 55.00 | 9.42 | -0.544 | -0.812 |
| DUMP_GAIN_NORMALVOL_STRONG_CLOSE | 5410 | 231 | 1359 | 3.6 | 1.309 | 1.171 | -1.572 | 1.457 | 29.72 | 44.64 | 6.75 | -0.343 | -0.627 |
| DUMP_GAIN_NORMALVOL_WEAK_CLOSE | 5704 | 224 | 1289 | 5.9 | 0.544 | 0.429 | -1.933 | 0.578 | 30.65 | 47.60 | 3.49 | -0.356 | -0.607 |
| DUMP_GAIN_SPIKE_LONG_UPPER | 8814 | 237 | 1687 | 1.0 | 0.616 | 0.095 | -2.249 | 0.166 | 30.33 | 54.59 | 8.69 | -0.531 | -0.798 |
| DUMP_GAIN_SPIKE_SHORT_UPPER | 17194 | 235 | 1865 | 1.3 | 1.227 | 0.719 | -2.138 | 0.847 | 28.40 | 49.45 | 13.76 | -0.534 | -0.858 |
| DUMP_NEAR_LIMIT_UP | 9862 | 235 | 1447 | 1.7 | 2.514 | 2.056 | -1.530 | 2.433 | 20.41 | 39.62 | 12.81 | -0.315 | -0.652 |
| DUMP_GAIN_SPIKE_EXCL_LIMIT | 18703 | 237 | 1878 | 1.2 | 0.463 | -0.004 | -2.348 | 0.071 | 32.27 | 54.93 | 10.72 | -0.596 | -0.878 |
| DUMP_GAIN_SPIKE_HIGH_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_LOW_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_HIGH_DAYTRADE_ANY | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT | 1789 | 231 | 1030 | 1.8 | 0.120 | -0.101 | -2.182 | -0.055 | 37.00 | 53.44 | 15.99 | -0.717 | -1.044 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT | 8650 | 237 | 1701 | 1.0 | 0.583 | 0.044 | -2.305 | 0.141 | 31.24 | 54.98 | 9.40 | -0.544 | -0.812 |
| DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT | 8801 | 237 | 1687 | 1.0 | 0.615 | 0.096 | -2.246 | 0.168 | 30.34 | 54.57 | 8.68 | -0.530 | -0.797 |
| DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT | 9902 | 233 | 1831 | 1.3 | 0.327 | -0.093 | -2.439 | -0.016 | 33.99 | 55.24 | 12.53 | -0.655 | -0.950 |
| DUMP_GAIN_SPIKE_WEAK_LONGUPPER | 8080 | 237 | 1668 | 1.0 | 0.624 | 0.098 | -2.244 | 0.193 | 30.27 | 54.73 | 8.68 | -0.528 | -0.792 |

## Confounder profile — what each bucket is made of

A downside edge means nothing until this table shows the bucket is not simply a
selection of cheaper, thinner or more volatile names than the baseline.

| condition | n | median price | median 20d volume | median ATR% | median turnover (NTD) | limit-up share % |
|---|---:|---:|---:|---:|---:|---:|
| DUMP_BASELINE_ALL_BARS | 443898 | 43.30 | 386955 | 3.22 | 13714631 | 2.2 |
| DUMP_LARGE_GAIN | 44174 | 63.20 | 1305255 | 4.67 | 150035519 | 22.3 |
| DUMP_VOLUME_SPIKE | 117865 | 44.15 | 337974 | 3.08 | 29212204 | 6.2 |
| DUMP_WEAK_CLOSE | 329264 | 43.70 | 433535 | 3.30 | 15155357 | 0.0 |
| DUMP_LONG_UPPER_SHADOW | 190470 | 45.45 | 504017 | 3.33 | 18388970 | 0.0 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE | 8845 | 52.40 | 992441 | 4.37 | 176296352 | 79.8 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE | 8662 | 60.15 | 1067636 | 4.23 | 197453127 | 0.1 |
| DUMP_GAIN_NORMALVOL_STRONG_CLOSE | 5410 | 83.60 | 1703901 | 5.55 | 121391750 | 46.0 |
| DUMP_GAIN_NORMALVOL_WEAK_CLOSE | 5704 | 78.60 | 2010675 | 5.73 | 141401993 | 0.0 |
| DUMP_GAIN_SPIKE_LONG_UPPER | 8814 | 61.10 | 1124470 | 4.21 | 206211563 | 0.1 |
| DUMP_GAIN_SPIKE_SHORT_UPPER | 17194 | 54.90 | 931304 | 4.13 | 154387284 | 42.4 |
| DUMP_NEAR_LIMIT_UP | 9862 | 65.80 | 1923597 | 5.20 | 317666549 | 100.0 |
| DUMP_GAIN_SPIKE_EXCL_LIMIT | 18703 | 57.60 | 886068 | 3.96 | 143795080 | 0.0 |
| DUMP_GAIN_SPIKE_HIGH_DAYTRADE | 0 | 0.00 | 0 | 0.00 | 0 | 0.0 |
| DUMP_GAIN_SPIKE_LOW_DAYTRADE | 0 | 0.00 | 0 | 0.00 | 0 | 0.0 |
| DUMP_HIGH_DAYTRADE_ANY | 0 | 0.00 | 0 | 0.00 | 0 | 0.0 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT | 1789 | 43.45 | 232921 | 3.06 | 23256570 | 0.0 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT | 8650 | 60.10 | 1071342 | 4.23 | 197596583 | 0.0 |
| DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT | 8801 | 61.10 | 1128878 | 4.21 | 207145696 | 0.0 |
| DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT | 9902 | 54.90 | 706591 | 3.71 | 102013358 | 0.0 |
| DUMP_GAIN_SPIKE_WEAK_LONGUPPER | 8080 | 61.10 | 1123040 | 4.22 | 206142892 | 0.1 |

## By market regime


### BREADTH_STRONG

| condition | n | dates | stocks | max date % | T+1 open | T+1 close | T+1 low | T+2 close | gap-down % | neg-close % | selloff % | T+1 MAE (ATR) | T+2 MAE (ATR) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| DUMP_BASELINE_ALL_BARS | 135850 | 93 | 1974 | 1.4 | 0.263 | -0.031 | -1.530 | -0.167 | 28.21 | 51.88 | 5.19 | -0.458 | -0.729 |
| DUMP_LARGE_GAIN | 15716 | 74 | 1799 | 3.0 | 1.065 | 0.411 | -2.181 | 0.359 | 26.45 | 52.06 | 9.60 | -0.497 | -0.808 |
| DUMP_VOLUME_SPIKE | 42202 | 89 | 1973 | 3.1 | 0.418 | 0.095 | -1.721 | 0.000 | 27.55 | 51.04 | 8.05 | -0.522 | -0.818 |
| DUMP_WEAK_CLOSE | 98389 | 93 | 1974 | 1.8 | 0.244 | -0.078 | -1.509 | -0.235 | 26.05 | 51.75 | 4.43 | -0.434 | -0.699 |
| DUMP_LONG_UPPER_SHADOW | 59027 | 91 | 1974 | 1.8 | 0.300 | -0.035 | -1.508 | -0.165 | 25.35 | 51.33 | 4.27 | -0.429 | -0.689 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE | 3617 | 71 | 1330 | 2.6 | 2.249 | 1.638 | -1.689 | 1.626 | 21.15 | 42.36 | 14.82 | -0.409 | -0.780 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE | 3409 | 74 | 1345 | 2.6 | 0.551 | -0.235 | -2.518 | -0.329 | 27.84 | 57.73 | 9.68 | -0.593 | -0.885 |
| DUMP_GAIN_NORMALVOL_STRONG_CLOSE | 1632 | 71 | 787 | 5.9 | 1.726 | 1.008 | -1.766 | 1.010 | 25.98 | 47.00 | 8.03 | -0.358 | -0.693 |
| DUMP_GAIN_NORMALVOL_WEAK_CLOSE | 1599 | 70 | 822 | 9.6 | 0.800 | 0.048 | -2.151 | 0.223 | 23.76 | 53.41 | 4.44 | -0.391 | -0.637 |
| DUMP_GAIN_SPIKE_LONG_UPPER | 3462 | 74 | 1340 | 2.7 | 0.591 | -0.140 | -2.432 | -0.270 | 26.57 | 56.85 | 8.90 | -0.574 | -0.866 |
| DUMP_GAIN_SPIKE_SHORT_UPPER | 7066 | 71 | 1680 | 3.0 | 1.372 | 0.799 | -2.116 | 0.755 | 25.84 | 49.45 | 13.05 | -0.527 | -0.864 |
| DUMP_NEAR_LIMIT_UP | 3888 | 71 | 1151 | 2.9 | 2.787 | 2.003 | -1.581 | 2.002 | 17.98 | 40.56 | 13.79 | -0.330 | -0.703 |
| DUMP_GAIN_SPIKE_EXCL_LIMIT | 7489 | 74 | 1727 | 3.0 | 0.488 | -0.106 | -2.448 | -0.176 | 29.18 | 56.38 | 10.19 | -0.616 | -0.914 |
| DUMP_GAIN_SPIKE_HIGH_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_LOW_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_HIGH_DAYTRADE_ANY | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT | 680 | 71 | 536 | 4.7 | 0.141 | 0.091 | -2.144 | 0.240 | 35.74 | 50.88 | 12.94 | -0.682 | -0.983 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT | 3405 | 74 | 1345 | 2.6 | 0.552 | -0.228 | -2.511 | -0.317 | 27.81 | 57.68 | 9.63 | -0.592 | -0.884 |
| DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT | 3459 | 74 | 1340 | 2.7 | 0.590 | -0.135 | -2.427 | -0.262 | 26.57 | 56.81 | 8.88 | -0.573 | -0.866 |
| DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT | 4030 | 71 | 1557 | 3.3 | 0.400 | -0.080 | -2.466 | -0.103 | 31.41 | 56.00 | 11.32 | -0.653 | -0.955 |
| DUMP_GAIN_SPIKE_WEAK_LONGUPPER | 3176 | 74 | 1299 | 2.6 | 0.593 | -0.174 | -2.449 | -0.263 | 26.73 | 57.24 | 8.94 | -0.576 | -0.866 |

### BREADTH_MID

| condition | n | dates | stocks | max date % | T+1 open | T+1 close | T+1 low | T+2 close | gap-down % | neg-close % | selloff % | T+1 MAE (ATR) | T+2 MAE (ATR) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| DUMP_BASELINE_ALL_BARS | 182809 | 119 | 1974 | 1.1 | 0.193 | 0.031 | -1.440 | 0.134 | 30.70 | 48.94 | 5.07 | -0.426 | -0.653 |
| DUMP_LARGE_GAIN | 18033 | 98 | 1822 | 3.6 | 0.834 | 0.480 | -2.057 | 0.723 | 30.74 | 51.05 | 8.68 | -0.464 | -0.740 |
| DUMP_VOLUME_SPIKE | 47026 | 108 | 1973 | 2.2 | 0.321 | 0.098 | -1.675 | 0.246 | 31.00 | 49.38 | 8.19 | -0.506 | -0.757 |
| DUMP_WEAK_CLOSE | 134491 | 117 | 1974 | 1.3 | 0.189 | 0.011 | -1.411 | 0.147 | 28.82 | 48.17 | 4.41 | -0.402 | -0.615 |
| DUMP_LONG_UPPER_SHADOW | 79248 | 110 | 1973 | 1.6 | 0.238 | 0.062 | -1.405 | 0.190 | 27.94 | 47.97 | 4.48 | -0.396 | -0.612 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE | 3664 | 98 | 1315 | 2.1 | 1.956 | 1.515 | -1.719 | 1.790 | 24.65 | 43.83 | 14.57 | -0.408 | -0.755 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE | 3525 | 96 | 1389 | 2.0 | 0.664 | 0.232 | -2.128 | 0.448 | 31.63 | 54.33 | 8.62 | -0.498 | -0.756 |
| DUMP_GAIN_NORMALVOL_STRONG_CLOSE | 2253 | 95 | 984 | 4.9 | 1.105 | 0.925 | -1.752 | 1.214 | 30.27 | 47.98 | 6.52 | -0.374 | -0.656 |
| DUMP_GAIN_NORMALVOL_WEAK_CLOSE | 1998 | 95 | 914 | 5.8 | 0.531 | 0.347 | -1.987 | 0.667 | 28.68 | 49.05 | 3.70 | -0.369 | -0.603 |
| DUMP_GAIN_SPIKE_LONG_UPPER | 3652 | 96 | 1396 | 1.9 | 0.677 | 0.222 | -2.124 | 0.414 | 31.22 | 54.33 | 8.24 | -0.496 | -0.753 |
| DUMP_GAIN_SPIKE_SHORT_UPPER | 7090 | 98 | 1688 | 2.1 | 1.141 | 0.741 | -2.091 | 0.977 | 29.63 | 49.27 | 13.33 | -0.517 | -0.829 |
| DUMP_NEAR_LIMIT_UP | 4063 | 98 | 1120 | 2.6 | 2.349 | 2.035 | -1.539 | 2.514 | 21.66 | 40.04 | 12.08 | -0.305 | -0.633 |
| DUMP_GAIN_SPIKE_EXCL_LIMIT | 7700 | 96 | 1745 | 2.2 | 0.461 | 0.064 | -2.258 | 0.237 | 33.23 | 54.57 | 10.43 | -0.572 | -0.844 |
| DUMP_GAIN_SPIKE_HIGH_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_LOW_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_HIGH_DAYTRADE_ANY | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT | 726 | 96 | 548 | 3.2 | 0.175 | -0.057 | -2.059 | -0.106 | 36.91 | 53.86 | 15.84 | -0.692 | -1.043 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT | 3520 | 96 | 1388 | 2.0 | 0.664 | 0.233 | -2.125 | 0.445 | 31.65 | 54.32 | 8.61 | -0.497 | -0.756 |
| DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT | 3645 | 96 | 1394 | 1.9 | 0.677 | 0.224 | -2.120 | 0.415 | 31.22 | 54.29 | 8.23 | -0.496 | -0.753 |
| DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT | 4055 | 96 | 1536 | 2.5 | 0.268 | -0.079 | -2.382 | 0.078 | 35.04 | 54.82 | 12.40 | -0.640 | -0.926 |
| DUMP_GAIN_SPIKE_WEAK_LONGUPPER | 3321 | 96 | 1347 | 2.0 | 0.701 | 0.263 | -2.086 | 0.469 | 30.98 | 54.32 | 8.10 | -0.486 | -0.740 |

### BREADTH_WEAK

| condition | n | dates | stocks | max date % | T+1 open | T+1 close | T+1 low | T+2 close | gap-down % | neg-close % | selloff % | T+1 MAE (ATR) | T+2 MAE (ATR) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| DUMP_BASELINE_ALL_BARS | 125239 | 74 | 1975 | 1.5 | 0.437 | 0.220 | -1.331 | 0.486 | 28.16 | 43.68 | 4.70 | -0.382 | -0.594 |
| DUMP_LARGE_GAIN | 10425 | 67 | 1723 | 8.4 | 0.729 | 0.787 | -1.827 | 1.039 | 34.71 | 44.36 | 8.13 | -0.426 | -0.693 |
| DUMP_VOLUME_SPIKE | 28637 | 68 | 1974 | 3.9 | 0.471 | 0.179 | -1.571 | 0.528 | 28.73 | 45.72 | 8.23 | -0.480 | -0.710 |
| DUMP_WEAK_CLOSE | 96384 | 73 | 1975 | 1.8 | 0.486 | 0.234 | -1.310 | 0.528 | 26.13 | 42.92 | 4.06 | -0.354 | -0.554 |
| DUMP_LONG_UPPER_SHADOW | 52195 | 71 | 1973 | 2.2 | 0.531 | 0.309 | -1.248 | 0.523 | 26.12 | 43.04 | 3.76 | -0.346 | -0.559 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE | 1564 | 66 | 907 | 3.2 | 1.771 | 0.973 | -2.059 | 1.404 | 26.21 | 46.36 | 18.41 | -0.529 | -0.867 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE | 1728 | 67 | 1017 | 5.0 | 0.480 | 0.206 | -2.263 | 0.420 | 37.09 | 50.98 | 10.53 | -0.543 | -0.782 |
| DUMP_GAIN_NORMALVOL_STRONG_CLOSE | 1525 | 65 | 846 | 12.9 | 1.163 | 1.709 | -1.097 | 2.296 | 32.92 | 37.18 | 5.70 | -0.280 | -0.514 |
| DUMP_GAIN_NORMALVOL_WEAK_CLOSE | 2107 | 59 | 938 | 15.9 | 0.362 | 0.798 | -1.717 | 0.760 | 37.73 | 41.81 | 2.56 | -0.318 | -0.587 |
| DUMP_GAIN_SPIKE_LONG_UPPER | 1700 | 67 | 1006 | 4.6 | 0.534 | 0.302 | -2.149 | 0.512 | 36.06 | 50.59 | 9.24 | -0.517 | -0.753 |
| DUMP_GAIN_SPIKE_SHORT_UPPER | 3038 | 66 | 1355 | 3.2 | 1.089 | 0.482 | -2.298 | 0.760 | 31.47 | 49.90 | 16.43 | -0.591 | -0.909 |
| DUMP_NEAR_LIMIT_UP | 1911 | 66 | 810 | 8.9 | 2.312 | 2.210 | -1.409 | 3.133 | 22.71 | 36.79 | 12.35 | -0.307 | -0.587 |
| DUMP_GAIN_SPIKE_EXCL_LIMIT | 3514 | 67 | 1442 | 3.5 | 0.412 | 0.061 | -2.334 | 0.228 | 36.77 | 52.62 | 12.49 | -0.607 | -0.877 |
| DUMP_GAIN_SPIKE_HIGH_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_LOW_DAYTRADE | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_HIGH_DAYTRADE_ANY | 0 | 0 | 0 | 0.0 | 0.000 | 0.000 | 0.000 | 0.000 | 0.00 | 0.00 | 0.00 | 0.000 | 0.000 |
| DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT | 383 | 64 | 320 | 6.3 | -0.023 | -0.528 | -2.482 | -0.478 | 39.43 | 57.18 | 21.67 | -0.829 | -1.154 |
| DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT | 1725 | 67 | 1017 | 5.0 | 0.479 | 0.194 | -2.264 | 0.417 | 37.16 | 51.01 | 10.55 | -0.544 | -0.783 |
| DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT | 1697 | 67 | 1006 | 4.6 | 0.532 | 0.290 | -2.150 | 0.509 | 36.12 | 50.62 | 9.25 | -0.518 | -0.754 |
| DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT | 1817 | 66 | 1084 | 4.0 | 0.299 | -0.154 | -2.506 | -0.034 | 37.37 | 54.49 | 15.52 | -0.690 | -0.992 |
| DUMP_GAIN_SPIKE_WEAK_LONGUPPER | 1583 | 67 | 957 | 4.7 | 0.524 | 0.295 | -2.165 | 0.521 | 35.88 | 50.54 | 9.35 | -0.518 | -0.752 |
