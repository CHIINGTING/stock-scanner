# SPEC R10-1 — Institutional Flow & BIAS Risk (Shadow Layer)

Status: **DRAFT (design-only, pre-implementation)**
Date: 2026-07-15
Scope: Add two per-stock analysis layers — **三大法人籌碼 (Institutional Flow)** and **乖離率風險 (BIAS Risk)** — as **shadow layers**: computed, persisted, displayed, and historically validated, but **never** affecting existing `Score` / `Action` / `RocketScore` / `WatchAction` / sort / stop in P0.

This SPEC is written against a **live-verified** data contract (see §3/§4). It does not authorize coding; it is the contract the implementation must match.

---

## 1. Goals / Non-goals

### Goals (P0)
- Ingest official per-stock 三大法人買賣超 for **上市 (TWSE)** and **上櫃 (TPEx)** into a normalized daily snapshot.
- Per institution category (FOREIGN / INVESTMENT_TRUST / DEALER / TOTAL): today net; 3/5/10/20-day cumulative; consecutive buy/sell days; BUY_TO_SELL / SELL_TO_BUY transition; net-buy ratio (÷ volume).
- Compute BIAS5/10/20/60 and a centralized risk classification.
- Display both in the HTML report (sections ⑪ / ⑫) + summary chips for genuinely important events.
- Persist a **structured sidecar JSON** per scan date so historical/cohort validation never depends on HTML.
- Enforce **data completeness**: real (non-holiday) gaps break streaks and set `StreakComplete=false`; incomplete rows are excluded from validation.

### Non-goals (P0)
- No change to scoring / action / probability / sort / stop (shadow only).
- No per-institution weighting, no acceleration / STRONG_ACCUMULATION / divergence signals (P1).
- No regime/volatility-adjusted BIAS thresholds (P1).
- No 占流通股數 / 占市值 normalization (needs shares-outstanding data not currently fetched — P1).
- No new security-type classifier (see §24).

---

## 2. Existing repo integration points

| Concern | Reuse |
|---|---|
| Price history / **trading-day calendar** | `internal/fetcher` Yahoo OHLCV in `.cache/<code>_{TW,TWO}.json`; `Candle.Date` = trading days; `Candle.Volume` = shares |
| Moving averages (for BIAS) | `internal/indicator.SMA` |
| ATR (for P1 BIAS regime-adjust) | `internal/indicator` ATR |
| Shadow-attach pattern | `internal/scanner/etfflow_attach.go` `AttachETFFlow` (post-pass, never re-invokes `computeRocket`) |
| Provider isolation | `internal/market/provider` + `internal/news.NewsProvider` (`ErrUnavailable`, never panic, respect ctx) |
| Daily snapshot persistence | `internal/etfflow/snapshot.go` (atomic temp+rename Save), `loader.go` (LoadHistory, graceful degrade) |
| Centralized thresholds | `internal/pricingpower/thresholds.go`, `etfflow.StrengthThresholds` |
| **Authoritative universe** | runtime `fetcher.FetchAll` → `FetchStockList`/`FetchTPEXList` (already `isOrdinaryStockCode`-filtered) → `StockData{Symbol, Market}`; watchlist/market entries are the intersection point |
| Feature-flag discipline | `scanner.Config` `EnableX` (compute+attach) / `ShowX` (display), both default false |
| Historical validation | `internal/validator`: `PriceStore` (structured `.cache` JSON → forward T+n + MFE(`MaxRunup`) + MAE(`MaxDrawdown`)), `Evaluator`, CSV/HTML writers. Signal source today = HTML parse (`parser.go`) — this SPEC adds a structured source alongside it, not a replacement. |

New packages (mirroring `internal/etfflow`):
```
internal/indicator/bias.go               # PURE BIAS values
internal/institution/{model,provider,twse,tpex,snapshot,loader,chip,signal}.go
internal/scanner/institution_attach.go   # attach chip view (post-pass)
internal/scanner/bias_attach.go          # attach BIAS values + risk label
internal/scanner/signal_confluence.go    # Trend × Flow × BIAS combos (display only)
internal/analysishistory/writer.go       # sidecar JSON writer
internal/validator/sidecar.go            # sidecar reader (mirrors price.go)
internal/validator/cohort.go             # cohort evaluator
cmd/institution-fetch/main.go            # standalone daily snapshot writer
```

---

## 3. TWSE (上市) verified contract

Verified live 2026-07-15 (`stat="OK"`, 14,333 rows, reconciliation exact).

| Item | Value |
|---|---|
| URL | `https://www.twse.com.tw/rwd/zh/fund/T86` |
| Method | GET |
| Params | `date=YYYYMMDD` · `selectType=ALL` · `response=json` |
| Date format | Western `YYYYMMDD` |
| Response | JSON `{stat, date, title, hints, fields[19], data[][], total}` |
| Field names | Chinese, fully qualified (unambiguous) |
| **Units** | **股 (shares)** — explicit `hints:"單位：股"` |
| Numbers | comma-separated strings; leading `-`; literal `"0"` = real zero |
| Encoding | UTF-8 |
| All-stocks one call | ✅ (~14k rows incl. warrants) |
| Holiday | HTTP 200, `stat="很抱歉，沒有符合條件的資料!"`, **no `data`/`fields` keys** |
| Invalid/out-of-range date | HTTP 200, `stat="查詢日期大於…"` / `"查詢日期小於101年05月02日…"` |
| **History floor** | **2012-05-02 (民國101/05/02)**, server-stated |
| Valid-data test | **`stat == "OK"`** |

**19 fields (index → meaning):**
```
0 證券代號  1 證券名稱
2/3/4   外陸資(不含外資自營) 買/賣/淨      → FOREIGN_EXCL_DEALER
5/6/7   外資自營商 買/賣/淨                → FOREIGN_DEALER
8/9/10  投信 買/賣/淨                      → TRUST
11      自營商買賣超股數 (NET only, 合計)  → DEALER_TOTAL
12/13/14 自營商(自行買賣) 買/賣/淨          → DEALER_SELF
15/16/17 自營商(避險) 買/賣/淨            → DEALER_HEDGE
18      三大法人買賣超股數                 → OFFICIAL_TOTAL
```
TWSE has **no explicit 外資合計 column** (compute col4+col7) and **no buy/sell for DEALER_TOTAL** (net only).
Reconciliation verified (2330, 2026-07-15): −4,007,871 computed = official ✓.

---

## 4. TPEx (上櫃) verified contract

Use the **dated** endpoint (the openapi `tpex_3insti_daily_trading` is latest-only and has a mangled dealer split with inconsistent-whitespace JSON keys — **rejected for ingestion**).

Verified live 2026-07-15 (`stat="ok"`, 919 rows, reconciliation exact).

| Item | Value |
|---|---|
| URL | `https://www.tpex.org.tw/www/zh-tw/insti/dailyTrade` |
| Method | GET |
| Params | `type=Daily` · `sect=EW` · `date=YYYY/MM/DD` · `response=json` |
| Date format | request Western `YYYY/MM/DD`; body echoes ROC `115/07/15` |
| Response | `{columnNum, stat, tables:[{fields[24], data[][], subtitle, totalCount}]}` |
| Field names | **generic & duplicated** (`買進/賣出/買賣超股數` ×7) → **positional mapping only** |
| **Units** | **股 (shares)** (fields = `…股數`) |
| Numbers | as TWSE |
| All-stocks one call | ✅ (~919 rows) |
| Holiday | HTTP 200, **`stat="ok"` but `data=[]`, `totalCount=0`** (differs from TWSE) |
| Valid-data test | **`len(data) > 0`** (stat is `"ok"` even on holidays) |
| History floor | ≥ 2020 confirmed (2015 empty) |
| Caveat | subtitle: 投信量含鉅額/零股/綜合帳戶 → basis not identical to TWSE |
| Note | `columnNum:25` ≠ 24 actual fields → trust `len(fields)` |

**24 fields (positional groups):**
```
0 代號  1 名稱
2-4   外陸資(不含外資自營) 買/賣/淨   → FOREIGN_EXCL_DEALER
5-7   外資自營商 買/賣/淨            → FOREIGN_DEALER
8-10  外資合計 買/賣/淨 (explicit)    → FOREIGN_TOTAL
11-13 投信 買/賣/淨                  → TRUST
14-16 自營商(自行買賣) 買/賣/淨       → DEALER_SELF
17-19 自營商(避險) 買/賣/淨          → DEALER_HEDGE
20-22 自營商合計 買/賣/淨            → DEALER_TOTAL
23    三大法人買賣超股數合計          → OFFICIAL_TOTAL
```
TPEx provides buy/sell/net for **all seven** legs.
Reconciliation verified (3105, 2026-07-15): 642,994 computed = official ✓; self+hedge = dealer_total ✓.

---

## 5. Raw institutional snapshot schema v1

Per market per date: `data/institution/{twse,tpex}_YYYY-MM-DD.json`.

```go
type Leg struct {
    Net  int64  `json:"net"`            // 股; always present when a row exists
    Buy  *int64 `json:"buy,omitempty"`  // nil = source omits (≠ 0)
    Sell *int64 `json:"sell,omitempty"`
}

type StockChip struct {
    Code, Name        string
    ForeignExclDealer Leg
    ForeignDealer     Leg
    ForeignTotal      Leg    // TPEx explicit; TWSE computed (Buy/Sell nil)
    Trust             Leg
    DealerSelf        Leg
    DealerHedge       Leg
    DealerTotal       Leg
    OfficialTotalNet  *int64 // SourceTotal
    Reconciled        bool   // §7 checks all passed
}

type Snapshot struct {
    SchemaVersion int          `json:"schema_version"` // = 1
    Market        string       `json:"market"`         // "TWSE" | "TPEX"
    AsOfDate      string       `json:"as_of_date"`     // YYYY-MM-DD (ROC normalized)
    Unit          string       `json:"unit"`           // "shares"
    Source        string       `json:"source"`         // "twse_t86" | "tpex_dailytrade"
    SourceURL     string       `json:"source_url"`
    FetchedAt     time.Time    `json:"fetched_at"`
    Stocks        []StockChip  `json:"stocks"`
}
```
- `Net int64` is safe: **live verification found zero empty/null/dash cells** on both sources — a value is always a comma-number or literal `"0"`. Missing only ever occurs at **row** (stock absent) or **whole-day** (holiday/error sentinel) level.
- `Buy/Sell` are pointers to distinguish "source did not provide" (TWSE FOREIGN_TOTAL / DEALER_TOTAL) from a real zero.
- Rows stored = normalized rows passing **basic syntactic validation** only (see §24); membership classification happens at attach time.

Number parsing: strip `,`, parse int; empty string (never observed) → treat row as malformed and skip that row (log), never coerce to 0.

---

## 6. Normalized domain model

Canonical legs (source-agnostic):

| Leg | TWSE | TPEx |
|---|---|---|
| FOREIGN_EXCL_DEALER | col4 | 2-4 |
| FOREIGN_DEALER | col7 | 5-7 |
| **FOREIGN_TOTAL** | computed col4+col7 (buy/sell nil) | 8-10 (explicit) |
| INVESTMENT_TRUST | col10 | 11-13 |
| DEALER_SELF | col14 | 14-16 |
| DEALER_HEDGE | col17 | 17-19 |
| DEALER_TOTAL | col11 (buy/sell nil) | 20-22 |
| OFFICIAL_TOTAL | col18 | 23 |

P0 display categories: `FOREIGN = FOREIGN_TOTAL.Net`, `INVESTMENT_TRUST = Trust.Net`, `DEALER = DEALER_TOTAL.Net`, `TOTAL = ComputedTotal` (§7).

Per-stock computed view (interpreted, not stored raw):
```go
type LegStats struct {
    TodayNet            int64
    Sum3d, Sum5d, Sum10d, Sum20d int64
    ConsecutiveBuyDays  int
    ConsecutiveSellDays int
    Transition          string   // "BUY_TO_SELL" | "SELL_TO_BUY" | "NONE"
    NetBuyRatio         *float64 // % of volume; nil when volume unavailable
    StreakComplete      bool
    CumulativeComplete  map[int]bool // window → whether that cumulative had no gap
}
type StockChipView struct {
    Foreign, Trust, Dealer, Total LegStats
    Completeness  ChipCompleteness   // §8
    TotalMismatch bool               // §7
}
```

---

## 7. ComputedTotal / SourceTotal reconciliation

```
ComputedTotal = ForeignTotal.Net + Trust.Net + DealerTotal.Net
SourceTotal   = OfficialTotalNet
```
Three parse-time checks (integer-exact on live data):
1. `ForeignTotal.Net == ForeignExclDealer.Net + ForeignDealer.Net`
2. `DealerTotal.Net == DealerSelf.Net + DealerHedge.Net`
3. `ComputedTotal == SourceTotal`

Rules:
- All pass → `Reconciled = true`.
- Any fail beyond tolerance `0` → `Reconciled = false`, `TotalMismatch = true`, **log warning, never panic, never drop the row**.
- Display uses `ComputedTotal`; `SourceTotal` retained for the check.
- On TPEx, checks 1–2 also validate that the **positional mapping** is still correct (field names cannot).

---

## 8. Completeness model

Trading-day calendar = the stock's price candle dates (`.cache`). Institution data presence = per-date snapshot records.

```go
type ChipCompleteness struct {
    ExpectedDays int      // trading days (candles) in the lookback window
    ObservedDays int      // expected days with an institution record for this stock
    MissingDates []string // YYYY-MM-DD, expected-but-missing
    DataComplete bool     // today's own record present
}
```
- **Expected trading day** = date with a candle for this stock.
- **Observed** = expected day that also has an institution record.
- **Missing** = expected − observed (a real gap; never a holiday).
- A non-trading day (no candle) is **skipped**, never counted as a gap.
- Validation (§18) excludes rows with `DataComplete == false` or the relevant `StreakComplete == false` / `CumulativeComplete[w] == false`.

---

## 9. Trading-day / missing-data rules

- **Holiday / market closed**: no candle → skip; never a gap. (Also: TWSE `stat!="OK"` and TPEx `len(data)==0` confirm no trading data for that date at the source.)
- **Expected trading day but institution record missing** (candle exists, no snapshot record for the stock, OR the whole-day snapshot absent): **real gap**. It breaks streaks and taints cumulatives that span it.
- Data ordering: loader sorts ascending by `AsOfDate`; reversed input normalized; duplicate dates de-duplicated (latest `FetchedAt` wins, subject to §19).
- Gaps are **never zero-filled** — a phantom zero would fabricate a transition or reset a streak.

---

## 10. Streak rules

Given a per-stock net series over consecutive **expected trading days** (most recent last):
- `ConsecutiveBuyDays` = count back from today while `net > 0` (strictly). Stops at the first `net ≤ 0`.
- `ConsecutiveSellDays` = count back while `net < 0`. Stops at first `net ≥ 0`.
- **Zero resets both** (a flat day is neither accumulation nor distribution).
- **Gap handling**: if walking back hits an **expected trading day with missing data before the streak naturally ends**, stop counting there and set `StreakComplete = false`. The reported streak covers only the proven contiguous run.

Example (`7/10 +1000`, `7/11 missing`, `7/12 +2000`): 7/11 is an expected trading day (candle exists) with no record → streak stops at the gap → `ConsecutiveBuyDays` counts only 7/12, `StreakComplete=false`. Never "連買 2 日".

---

## 11. Transition rules

Compare today vs the **previous expected trading day with data**:
- `BUY_TO_SELL`: `prev > 0 AND today < 0`
- `SELL_TO_BUY`: `prev < 0 AND today > 0`
- Zero on either side → `NONE` (a zero neither triggers nor bridges a transition).
- If the previous expected trading day has **missing data**, transition = `NONE` and the row is flagged `DataComplete`-dependent (do not infer through a gap).

Example (`+1000, 0, -800`): Day3 transition = `NONE` (prev=0). A "look-through-zero to last non-zero" variant is **P1**, off by default.

---

## 12. Net-buy ratio

```
NetBuyRatio_leg = leg.Net (股) / Volume (股) * 100
```
- `Volume` = same-date `Candle.Volume` (Yahoo, shares). Both operands are shares → no unit conversion.
- Computed for FOREIGN / INVESTMENT_TRUST / DEALER / TOTAL.
- `Volume <= 0` or candle missing for the date → `NetBuyRatio = nil` (undefined), **never 0** (distinguish missing from zero).
- Caveat: Yahoo total volume ≠ exchange official 成交股數 exactly; acceptable for a ratio. Switching to TWSE `STOCK_DAY` volume for exactness is P1.

---

## 13. BIAS5 / 10 / 20 / 60

Pure function in `internal/indicator/bias.go`:
```
BIAS_N = (Close - SMA_N) / SMA_N * 100
```
- Reuse `indicator.SMA`.
- Insufficient history (`len(candles) < N`) → BIAS_N unavailable (nil), not 0/NaN.
- `SMA_N == 0` → unavailable (guard divide-by-zero), not NaN.
- Uses raw `Close` by default (consistent with existing indicators); adjusted-close variant deferred.

---

## 14. BIAS risk classification

Centralized thresholds (no magic numbers scattered), **BASELINE — not calibrated**:
```go
type BiasThresholds struct {
    AnchorWindow int // default 20
    // positive (追價 / overextension)
    Elevated, High, Extreme float64 // default 10, 15, 20
    // negative (超跌)
    Oversold, DeeplyOversold float64 // default -10, -18
}
```
Risk label derived from the **anchor window** (default BIAS20; documented), other windows shown raw:
```
BIAS_anchor >= Extreme          → EXTREME
>= High                          → HIGH
>= Elevated                      → ELEVATED
in (Oversold, Elevated)          → NORMAL
<= DeeplyOversold                → DEEPLY_OVERSOLD
<= Oversold                      → OVERSOLD
```
Semantics documented explicitly: **BIAS is risk/context, not a buy/sell signal.** `+18%` → 短線追價風險偏高 (not "will fall"); `-15%` → 明顯低於中期均線、超跌區 (not "buy now"). P1 = regime/ATR-adjusted thresholds.

---

## 15. Shadow-only integration

- New `WatchlistEntry` fields, **dedicated, NOT inside `ShadowSignals`** (so the C6b guardrail can never read them):
  ```go
  Institution *institution.StockChipView `json:"institution,omitempty"` // enable_institution
  Bias        *scanner.BiasView          `json:"bias,omitempty"`        // enable_bias
  Confluence  *scanner.ConfluenceView    `json:"confluence,omitempty"`  // display combos
  ```
- BIAS computed in `EnrichWatchlist` behind `EnableBias` (needs only candles) — like `HoldingHorizon`, AFTER `computeRocket`, never fed in.
- Institution attached by **`AttachInstitution` post-pass** AFTER enrichment (like `AttachETFFlow`) — `computeRocket` already ran, never re-invoked.
- `signal_confluence.go` reads already-attached Institution + BIAS + existing trend fields → `HEALTHY_STRENGTH` / `DISTRIBUTION_RISK` / `OVERSOLD_CHIP_IMPROVEMENT`, **text only**.
- Config flags (default false): `enable_institution` / `show_institution` / `enable_bias` / `show_bias`.
- **Guarantee**: none of these fields are read by any scoring/action/probability/sort/stop path. Trivially provable (post-pass, dedicated fields).

---

## 16. HTML sections ⑪ / ⑫

Watchlist card, gated by `show_institution` / `show_bias`, styled like existing ⑨/⑩:
- **⑪ 三大法人籌碼**: per FOREIGN / INVESTMENT_TRUST / DEALER / TOTAL — today, 5d, 10d, 連續狀態 (連買/連賣 N 日 + `⚠ 資料不完整` when `StreakComplete=false`), 轉折 (BUY_TO_SELL / SELL_TO_BUY), netBuyRatio.
- **⑫ 乖離率**: BIAS5/10/20/60 raw + Risk label.
- **Summary chips** (events only, not raw dumps): e.g. `🟢 投信連買 7 日`, `🔄 外資由賣轉買`, `🔴 BIAS20 +17.8% 追價風險偏高`. Incomplete-data streaks are labeled, not promoted to chips.
- Report renders "資料未更新/缺漏" when the attach found no data for a stock (graceful).
- HTML carries **no** structured data-attributes for validation (presentation only — §17).

---

## 17. Structured historical sidecar JSON

`data/analysis_history/YYYY-MM-DD.json` (under `data/`, not `reports/`, keeping reports/ presentation-only and outside the validator's `report_*.html` regex).
```jsonc
{
  "schema_version": 1,
  "date": "2026-07-15",
  "stocks": [{
    "code": "2330", "action": "BUY", "score": 82,
    "institution": {
      "foreign": { "today":.., "sum3d":.., "sum5d":.., "sum10d":.., "sum20d":..,
                   "consecutiveBuyDays":.., "consecutiveSellDays":.., "transition":"..",
                   "netBuyRatio":.., "streakComplete":true, "dataComplete":true },
      "investmentTrust": { ... }, "dealer": { ... }, "total": { ... }
    },
    "bias": { "bias5":.., "bias10":.., "bias20":.., "bias60":.., "risk":"HIGH" }
  }]
}
```
- Written by `internal/analysishistory` from `cmd/scanner/main.go` after results are assembled (pure serialization, no new analysis).
- `netBuyRatio` / bias fields are nullable (absent ≠ 0).
- One file per scan date; covers watchlist entries (extendable to market/positions).

---

## 18. Cohort validation design

Reuse `internal/validator` machinery; add a structured signal source **alongside** the HTML parser (do not replace it):
- `internal/validator/sidecar.go` — `SidecarStore` **mirroring `price.go` `PriceStore`**: read-only, offline, `Load(date) → map[code]Record`.
- `internal/validator/cohort.go` — joins `SidecarStore` records (by code+date) with `PriceStore.Outcome` (existing T+1/3/5/10 + MFE + MAE), buckets by condition, reports per-cohort hit-rate / avg return / MFE / MAE.
- Example cohorts: `投信連買 ∧ 外資由賣轉買 ∧ BIAS20 未過熱`; `高正BIAS ∧ 由買轉賣`.
- **Exclusion**: any row with `dataComplete=false`, or `streakComplete=false` for a streak-based cohort, is excluded so incomplete data never pollutes results.
- `PriceStore`, `Evaluator`, `parser.go` internals unchanged. New code = one reader + one cohort aggregator + one writer.

---

## 19. Snapshot overwrite / non-downgrade rules

- Write atomic (temp + rename), like `etfflow.Save`.
- **Historical dates**: effectively immutable (T86 / dailyTrade final after close).
- **Same-day rerun**: overwrite **only if the new snapshot is at least as complete** as the existing one (≥ record count and no loss of populated legs). A rerun that fetched **fewer** stocks (e.g. mid-publication) is **rejected + logged**, not written — never downgrade completeness.
- `FetchedAt` stored for provenance.
- Net effect: idempotent, monotonically improving, partial refetch cannot corrupt a good snapshot.

---

## 20. SchemaVersion compatibility

- `SchemaVersion` on every snapshot (v1) and on the sidecar (v1).
- Evolution is **additive**: new institutional sub-legs added as **optional / pointer** fields; old snapshots unmarshal with them absent (nil ≠ 0); readers branch on presence + version.
- All seven legs + official total are stored from **v1** (see §22 G) so future P1 needs no re-fetch.
- A loader meeting an unknown higher version reads the fields it understands, ignores the rest, never panics.

---

## 21. Graceful degradation

- **Standalone prefetch** `cmd/institution-fetch` writes snapshots; scanner runtime **only loads**.
- Missing snapshot / missing stock record → field stays nil; report shows "資料未更新/缺漏"; **scan never blocks**.
- Provider errors isolated per source (`ErrUnavailable`, never panic, respect ctx); one market down still lets the other proceed.
- Malformed row → skipped + logged, not fatal (mirrors `etfflow.LoadHistory`).
- Missing candle/volume → ratio nil, BIAS windows that lack history nil.

---

## 22. P0 / P1 scope

**P0**
- TWSE + TPEx ingestion → normalized snapshot v1 (all 7 legs stored).
- Per category: today, 3/5/10/20d cumulative, consecutive buy/sell, BUY_TO_SELL/SELL_TO_BUY, netBuyRatio.
- Completeness + StreakComplete.
- BIAS5/10/20/60 + fixed centralized risk classification.
- HTML ⑪/⑫ + summary chips.
- Sidecar JSON + cohort validation.
- Zero scoring impact.

**P1**
- Buy/sell acceleration, STRONG_ACCUMULATION/DISTRIBUTION, 法人×price divergence.
- DEALER_SELF vs DEALER_HEDGE isolation; FOREIGN_EXCL vs FOREIGN_DEALER divergence.
- Regime/ATR-adjusted BIAS thresholds.
- 占流通股數 / 占市值 normalization (new data need).
- Per-institution weighting + promotion into scoring behind a new master guardrail flag (evidence-gated by §18).
- Look-through-zero transition variant.

---

## 23. Test matrix

Pure-function table tests (deterministic):
- Streak buy `[+100,+200,+300] → 3`; sell `[-100,-200,-300] → 3`.
- Transition `[+100,-200] → BUY_TO_SELL`; `[-100,+200] → SELL_TO_BUY`.
- Zero: `[+1000,0,-800] → transition NONE`, streaks reset by the zero.
- **Gap**: `[+1000, <missing>, +2000] → ConsecutiveBuyDays covers only latest, StreakComplete=false`.
- BIAS `price120 / MA100 → BIAS20 = 20`.
- Edge: `<5`/`<20` days; `SMA=0`; missing institution day; `net=0`; `volume=0` (ratio nil, no divide-by-zero); non-continuous dates; reversed input.
- Reconciliation: FOREIGN_TOTAL / DEALER_TOTAL / OFFICIAL mismatch → `Reconciled=false`, no panic.
- Parser: TWSE holiday (`stat!="OK"`) and TPEx holiday (`stat="ok"` + empty) both → no-data, not error.
- TPEx positional mapping validated by reconciliation on a fixture.
- Snapshot round-trip; same-day non-downgrade (fewer rows rejected).
- Attach intersection: snapshot code absent from universe → not attached; universe code absent from snapshot → nil, graceful.

---

## 24. Security-type filtering caveat (IMPORTANT)

**A 4-digit numeric code is NOT an ordinary-stock classification.**
- `0050` = ETF (excluded by leading `0`), but `9103` = TDR passes `^\d{4}$` — a regex cannot classify security type.
- Even the repo's existing `isOrdinaryStockCode` (len==4 && !prefix "0") admits TDRs (e.g. 9103); it is a **syntactic** filter, not a classifier.

Design (no new classifier):
```
Raw provider response
  → normalize
  → basic syntactic validation (^\d{4}$ prefilter only, to keep files lean)
  → snapshot (retains all syntactically-valid rows; NOT a security-type decision)
  → AttachInstitution: intersect with the scanner's authoritative universe
    (the actual WatchlistEntry / market codes, which came from FetchAll's
     ordinary-stock membership) — authoritative membership happens HERE
```
- **Authoritative universe** = runtime `fetcher.FetchAll` output (already `isOrdinaryStockCode`-filtered) and the watchlist/market entries derived from it. `AttachInstitution` only attaches to codes that exist as entries → intersection is automatic, no separate classifier.
- The `^\d{4}$` prefilter in `cmd/institution-fetch` is documented in-code as **basic syntax prefilter only**, explicitly not ordinary-stock classification. Whole-universe fidelity is preserved because membership is decided at attach time against the existing universe provider.

---

## Open questions (to resolve before/during implementation)

1. Sidecar coverage: watchlist-only (P0) vs also market/positions?
2. BIAS anchor: single BIAS20 anchor vs multi-window composite risk label?
3. `institution-fetch` prefilter: keep `^\d{4}$` lean-storage prefilter vs store fully raw and intersect only at attach (both satisfy §24; choose per storage-size tolerance)?
4. Exact TPEx history floor (2016–2019 boundary) if deep backfill is ever wanted.
