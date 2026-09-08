# R15 TAIFEX fixtures — provenance

**Tests in this package never download.** Every `ParseX` runs off the bytes below. If a test
ever needs a new characteristic, capture it, trim it, and record it here — do not add a
network call.

## Where these came from

| what | value |
|---|---|
| host | `https://openapi.taifex.com.tw/v1/<endpoint>` |
| trading session described | **2026-09-04** (Friday) |
| captured | **2026-09-06** |
| capture method | plain `GET`, no date parameter — these endpoints accept none and answer with the latest session |

The five large captures were taken during the R15-M1 audit; the four smaller ones
(`…CallsAndPuts…`, margin, final settlement, settled positions) were captured on 2026-09-06
for M3 and describe the same 2026-09-04 session, which their own date fields confirm.

## Files

| file | endpoint | rows here | rows in the live response | how it was trimmed |
|---|---|---|---|---|
| `institutional_futures_20260904.json` | `MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate` | 9 | 66 | kept 臺股期貨 / 小型臺指期貨 / 電子期貨 × the three institutions |
| `institutional_callsputs_20260904.json` | `MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate` | 30 | 30 | **untrimmed** — the whole response is 17 KB |
| `put_call_ratio_20260904.json` | `PutCallRatio` | 23 | 23 | **untrimmed** — this is the one endpoint that carries history rather than a single session |
| `options_by_strike_20260904.json` | `DailyMarketReportOpt` | 277 (270 TXO) | 12,012 | TXO rows at 8 strikes common to every expiry (41200, 43000, 45000, 46000, 46400, 46600, 48000, 50400), plus 7 TEO/CAO rows carrying real volume and OI so the TXO-only contract rule has something to exclude |
| `put_call_ratio_trimmed_reconcile_20260904.json` | — | 1 | — | **DERIVED, not captured.** See "the reconciliation fixture" below |
| `futures_daily_20260904.json` | `DailyMarketReportFut` | 47 | 2,250 | hand-picked to carry every characteristic listed below |
| `options_delta_20260904.json` | `DailyOptionsDelta` | 70 | 8,530 | TXO rows at 4 of the same strikes, plus 6 TEO rows |
| `margin_20260904.json` | `IndexFuturesAndOptionsMargining` | 31 | 31 | **untrimmed** |
| `final_settlement_20260904.json` | `FinalSettlementPriceIndexOptions` | 1 | 1 | **untrimmed** — the feed returns exactly one row, the expiry that settled |
| `settled_positions_20260904.json` | `SettledPositionsIndexOptions` | 2 | 2 | **untrimmed** |

Row values are byte-identical to the live response; trimming only ever removed whole rows.

## Characteristics preserved on purpose

Each of these is here because a plausible parser gets it wrong and the spec records the
counterexample.

- Chinese column VALUES: `買權` / `賣權`, `一般` / `盤後`, `臺股期貨` / `臺指選擇權`,
  `外資及陸資` / `投信` / `自營商`, `臺指選擇權F1`
- ASCII `CALL` / `PUT` in the institutional calls-and-puts feed, which disagrees with every
  other options feed's spelling (§7.2)
- the `-` sentinel: 126 rows of `options_by_strike`, and `%` / `Change` / `Open` in
  `futures_daily`
- `"0"` real zeros: 41 rows of `options_by_strike` — a strike nobody holds, which is a
  different fact from `-`
- `"NULL"` strings: 19 `SettlementPrice` values in `futures_daily`
- empty strings: `TradingHalt` everywhere,
  `Volume(ExecutionsAmongSpreadOrderAndSingleOrderOnly)` in `futures_daily`
- `/` combination codes: `202609/202610`, `202609/202611`, `202609W2/202609`
- every TXO expiry-code shape live on the session: `202609`, `202609W2`, `202609F1`,
  `202609F2`, `202609F3`, `202610`, `202611`, `202612`, `202703`
- both sessions in both daily reports
- real percentages next to `-`: `9.98%`, `0.00%`, `-1.20%`, `-`
- `202609F1` present in `options_by_strike` and **absent from `options_delta`** — the feed
  omits the expiry settling that day, and that absence must mean nothing

## The reconciliation fixture

`put_call_ratio_20260904.json` is the real official ratio and reconciles against the FULL
capture, verified on 2026-09-06:

| rule | PutOI | CallOI | PutVol | CallVol |
|---|---|---|---|---|
| official `PutCallRatio` | **48,162** | **48,258** | **356,219** | **361,817** |
| all TXO rows, both sessions, all expiries | 109,555 | 113,459 | 356,219 ✅ | 361,817 ✅ |
| all TXO rows minus the expiry settling that day (`202609F1`) | 48,162 ✅ | 48,258 ✅ | 28,802 | 29,661 |
| all TXO rows minus every `F*` (the rejected draft rule) | 46,852 ✗ | 46,675 ✗ | 27,506 ✗ | 28,028 ✗ |
| `一般` session only | 109,555 | 113,459 | 214,161 ✗ | 248,856 ✗ |

Those numbers cannot survive trimming: dropping 95% of the rows drops 95% of the lots. So the
committed by-strike fixture ships with a **derived** companion,
`put_call_ratio_trimmed_reconcile_20260904.json`, whose figures are the same two rules applied
to the trimmed rows by the Python trim script — volume over every row, OI over every row
except `202609F1`:

```
PutVolume 63014   CallVolume 66916   PutOI 5651   CallOI 4718
```

It is labelled DERIVED because it is: it does not prove the rule against the exchange (the
table above does that, at capture time, against all 12,012 rows). What it does is pin the rule
as a regression, and — because the three WRONG rules produce visibly different numbers on the
trimmed rows too — let the test assert that each of them fails against it:

```
all rows including 202609F1   PutOI 17,148  CallOI 11,380   (2.75x too high)
excluding every F*            PutOI  5,348  CallOI  4,293   (too low)
一般 session only             PutVol 41,529 CallVol 45,870  (too low)
```

## `malformed/`

Hand-written, not captured. Each file isolates one failure mode from §6.2.

| file | failure | expected |
|---|---|---|
| `html_error_page.html` | an HTML error page where JSON was expected | whole-response rejection |
| `empty.json` | zero bytes | whole-response rejection |
| `no_rows.json` | `[]` where the exchange should have returned rows | whole-response rejection |
| `undecodable.json` | truncated JSON | whole-response rejection |
| `missing_openinterest_column.json` | a required column renamed away | whole-response rejection |
| `options_by_strike_mixed_dates.json` | two trading dates in a single-session feed | whole-response rejection |
| `futures_daily_numeric_json.json` | `Last` sent as a JSON number, not a string | whole-response rejection |
| `options_by_strike_bad_rows.json` | one good row plus four bad ones: a non-numeric `StrikePrice` (a NOT NULL key), an unknown `Volume` token, an unrecognised `CallPut`, an unrecognised `TradingSession` | PARTIAL — rejected rows counted, the good row and the `-`/`NULL`/`""` absences stored |
