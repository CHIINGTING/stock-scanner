# R15 — TAIFEX Derivatives Risk Layer

**Status:** SPEC / IN PROGRESS
**Branch:** `feat/r15-taifex-derivatives-risk` (from `main` @ `6948e4a`, which contains R14 + FU-5/8/9/11)
**Mode:** default-off · shadow-only · research

---

## 0. What this is, and what it is not

R15 turns TAIFEX futures and options data into a **market-level risk layer** — evidence about
the next one to four weeks of holding risk. It is not a futures trading system, it produces no
orders, and it changes no stock decision.

The distinction matters because everything below follows from it. A layer that only *informs*
can be wrong in public, can say INSUFFICIENT_DATA for months, and can be deleted without
anything else moving. A layer that *decides* cannot. R15 is the first kind, permanently for
v1, and the tests enforce that rather than the prose.

### Non-goals (v1)

Not implemented, and not to be added without a new milestone:

- any change to BUY / WATCH / SELL, `RocketScore`, `WatchAction`, sorting, stops, candidate
  filtering, or target price
- futures or options entry/exit signals of any kind
- Max Pain as a magnet price
- OI walls asserted as support or resistance
- calling the non-institutional residual "散戶"
- mixing traded volume with open interest in one number
- letting the AI promote weak evidence into a recommendation

---

## 1. Repository audit — what already exists

Established by reading the code on 2026-09-06, not from memory.

### 1.1 There is already a TAIFEX provider

`internal/market/provider/taifex.go` (198 lines) fetches **foreign institutional futures open
interest** for 臺股期貨 from the OpenAPI JSON feed, and `taifex_backfill.go` recovers history
from the Big5 CSV download. `model.FuturesOIData` carries `LongOI / ShortOI / NetOI` plus
`OtherItems` for 投信 / 自營商, with a comment saying those are "for later phases".

**R15 extends this; it does not replace it.** FU-10 in the R14 follow-up log records what
happens when one acquisition path is copied instead of shared — two implementations of the
institution loop diverged on whether a downgrade counted as success, so the same day reported
differently depending on the entry point. That mistake is not to be repeated here.

Two facts about the existing parser, both load-bearing for R15:

- It reads only the `OpenInterest(*)` columns. The same response also carries
  `TradingVolume(*)`. So the FLOW/POSITION distinction §3 demands is **already available in
  data the repo fetches today and currently discards**.
- `NetOI` is taken from the official net column rather than derived as long − short, so a
  layout change surfaces as a mismatch instead of being papered over. R15 keeps that rule.

### 1.2 The shadow-layer pattern is settled and consistent

Every optional layer since R8 uses the same two-flag shape in `configs/config.yaml`:

```yaml
enable_X: false   # 總開關，預設 false — 完全不計算
show_X:   false   # 顯示開關，預設 false — 只控制 report
```

with the documented invariant "不影響 Score / Action / RocketScore / WatchAction / 排序 /
停損".

The `show=true` + `enable=false` guard is a real mechanism but a **narrower** one than a first
reading suggests: `scanner.Config.Validate()` (`internal/scanner/candlestick_attach.go:53-61`)
enforces exactly two pairs today — candlestick and technical indicators. Nothing stops the
combination elsewhere, and `configs/config.yaml:273-274` currently ships `enable_ai: false`
with `show_ai: true` unchallenged. R15 must therefore **add its own check**; inheriting one
does not happen automatically.

(The comment at `candlestick_attach.go:51` cites "SPEC R10-2 §9" for this rule. That section is
actually "Confidence + SuggestedAdjustment"; the flag contract is at
`docs/SPEC_R10_2_CANDLESTICK_SHADOW.md:506,520`. The mis-citation is pre-existing and is noted
here only so this spec does not propagate it.)

### 1.3 Persistence is versioned and append-only

`internal/store/schema.go`: `SchemaVersion = 2`, migrations declared as an append-only
`[]migration` with `{version, name, stmts}`, each applied in its own transaction together with
its `schema_migrations` row, so a half-applied migration is impossible. Forward-only by
design: "the answer to a bad migration is a new forward migration, not an automated rollback
that destroys recorded evidence."

Existing tables: `scan_runs`, `stock_snapshots`, `evidence`, `analysis_runs`, `agent_analysis`,
`decisions`, `outcomes`.

R15 reuses this machinery but **not this database's version counter** — see §7.1 for why the
obvious choice (migration 3 on the shared list) would break rollback.

### 1.4 Reusable pieces

| need | existing | location |
|---|---|---|
| trading session / Taipei clock | `Taipei`, `Resolve`, `Closed`, `ConfirmSession` | `internal/dailydata/session.go` (FU-8) |
| daily orchestration | `Pipeline`, `Step`, `SourceReport`, COMPLETE/PARTIAL/FAILED/NO_SESSION | `internal/dailydata/` |
| availability vocabulary | AVAILABLE / PARTIAL / INSUFFICIENT_DATA / UNAVAILABLE / DISABLED / NOT_IMPLEMENTED | `internal/healthcheck/types.go`, `internal/valuation/model.go` |
| point-in-time archive shape | `data/<layer>/<observed-date>/<file>` + as-of load | `internal/valuation/provider.go` |
| report section gating | `GV.ShowX` in Go templates | `internal/report/report.go` |
| rule versioning + not-fitted disclaimer | `QualityRuleVersion`, `SuitabilityRuleVersion` | `internal/valuation/{quality,suitability}.go` |

### 1.5 Why R15 cannot break R9–R14

Three structural reasons, each testable rather than asserted:

1. **The decision path does not import R15.** `go list -deps` on `./internal/scanner`,
   `./internal/report`, `./internal/candidate` and `./internal/ai` must contain none of
   `internal/derivatives`, `internal/derivatives/provider`, `internal/derivatives/acquire`.

   All four consumers, and all three packages. An earlier version checked one consumer against
   two packages, and the omitted one was `acquire` — R15's only networked package — so
   `internal/report` importing it compiled and left the whole suite green while `http.go`'s
   comment claimed a test enforced the opposite. See `internal/derivatives/architecture_test.go`.

   **"R15 is a leaf" is true only in that direction.** `internal/derivatives` already reaches
   `internal/market/{model,provider,analyzer,service}` transitively through `internal/dailydata`
   (§1.4's intended reuse of the session logic), and that predates R15. What must stay clean is
   the PARSE path: `internal/derivatives/provider` reaches no `internal/market` package, which is
   where a market token policy could do damage. `internal/taifexfeed/direction_test.go` pins
   both halves of that. The technique is proven in this repo
   (`internal/candidate/isolation_test.go:29-46`) and verified to work here: the scanner's
   dependency set is ten repo packages and does not include `internal/healthcheck`.

   The check is scoped to `./internal/scanner` **only**. It must NOT be copied onto
   `./cmd/scanner`, because `cmd/scanner → internal/report → internal/derivatives` is the
   intended wiring for the report section and would fail by design.

2. **Default off.** With both flags absent or false, nothing is fetched, computed, persisted or
   rendered, and the scanner's output is byte-identical. A golden test asserts the
   byte-identity rather than trusting the reasoning.

3. **R15 does not touch the shared database's schema version.** See §7. Adding a migration to
   `internal/store`'s list would raise `SchemaVersion` to 3, and `schema.go:244-246` then makes
   any binary built without R15 **refuse to open `r13.db` at all** ("refusing to write with an
   older binary"). That would make a default-off research layer able to break the existing
   research store on rollback, which contradicts §14. R15 therefore gets its own database file
   and its own migration list, reusing the same machinery.

## 2. Source contract

### 2.1 The catalogue, read rather than guessed

`https://openapi.taifex.com.tw/swagger.json` lists **135 endpoints**. An earlier draft of this
spec probed a dozen invented names, collected HTTP 302s, and concluded that margin and IV had
no official source. Two of those conclusions were false negatives, and one of them
(margin) would have led M7 to build a hand-maintained configuration table for data the exchange
publishes daily.

The method is therefore part of the contract: **read the catalogue; never conclude "no source"
from a guessed URL.**

Endpoints R15 uses, all verified live on 2026-09-06 (session 2026-09-04):

| purpose | endpoint | rows | notes |
|---|---|---|---|
| institutional futures | `MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate` | 66 | FLOW **and** POSITION per institution |
| institutional options, call/put split | `MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate` | 30 | preferred over the 15-row combined version for M5 |
| put/call ratio (official) | `PutCallRatio` | 23 | **23 sessions of history** — every other endpoint returns one |
| options by strike | `DailyMarketReportOpt` | 12,012 | `StrikePrice`, `CallPut`, `ContractMonth(Week)`, `OpenInterest`, `Volume`, `SettlementPrice`, `TradingSession` |
| futures daily | `DailyMarketReportFut` | 2,250 | per-contract OHLC / volume / OI |
| **margin** | `IndexFuturesAndOptionsMargining` | 31 | `ClearingMargin`, `MaintenanceMargin`, `InitialMargin`, `Date` — M7's risk-capital weight |
| settlement price / date | `FinalSettlementPriceIndexOptions` | — | `ContractDeliveryMonth`, `TheFinalSettlementDay` — resolves §3.1 |
| settled positions | `SettledPositionsIndexOptions` | — | OI removed by settlement |
| official OI change | `va01` (每日股價指數類選擇權未平倉量增減) | — | `Change` + `PreviousDay`; CHANGE need not be derived |
| whole-market OI | `OpenInterestOfLargeTradersFutures` / `…Options` | — | carries `OIOfMarket`. **Not fetched in v1** — §11.1's net residual needs no total; documented in §11.3 for later |
| per-strike Delta | `DailyOptionsDelta` | — | carries `ContractSettlementDay` |

### 2.2 Date parameters, and the two shapes

The `…BytheDate` and daily-report endpoints accept no date and return the latest session. The
existing provider already handles this and R15 inherits the rule:

> The requested date is validated against the date the feed actually returned. A mismatch is
> `ErrDateMismatch`, never today's data wearing yesterday's label.

The parallel `…BytheWeek` series returns a `FromDate`/`ToDate` range instead. R15 v1 uses the
daily series only; the weekly series is recorded here so nobody assumes every endpoint has the
same shape.

History accrues from archived snapshots, as valuation history does. Where a backfill is needed
it follows `taifex_backfill.go`: Big5 decoding contained in one file (enforced by
`internal/market/provider/containment_test.go`), output identical to the JSON path.

### 2.3 Margin is available; IV is not

**Margin** comes from `IndexFuturesAndOptionsMargining`, with the exchange's own effective
date. It is fetched, archived and point-in-time bounded like every other dataset. There is no
hand-maintained margin table and no `margin.contracts` configuration block — an earlier draft
proposed one, which would have been staler than the feed, unauditable, and (as reviewed) had
no staleness rule at all, so a value entered in January would still have been presented as
current in September.

If the margin fetch fails or is absent for a session, M7 degrades to OI-weighted zones with
`margin_source: NONE` and a status saying the risk-capital weighting is not applied. It never
substitutes 1, and never carries a previous day's margin forward silently.

**IV** has no source: the 135-endpoint catalogue contains no implied-volatility or VIX dataset.
This conclusion now rests on reading the catalogue, not on failed guesses. M8 therefore ships
its interface, status vocabulary, storage and fixture tests, reports `NOT_AVAILABLE` with that
reason, and blocks nothing. A future provider adapter slots in behind the interface without
touching the domain model.

Yesterday's value is never presented as today's. A stale observation is `STALE` with its real
`as_of` — a different thing from `MISSING`, and a very different thing from `NEUTRAL`.

### 2.4 No live HTTP after fetch

```
fetch → validate → normalize → persist immutable snapshot → compute → attach shadow view → report → outcome backfill
```

Only the first stage may touch the network. Report generation, scanner decisions and the AI
judge must not.

Enforced by installing an `http.RoundTripper` that fails the test on any request, then running
report generation end to end. This is a **new** technique in this repo — R14's cache-isolation
proof was filesystem-based (`internal/healthcheck/cacheisolation_test.go`), and no fail-on-request
transport exists yet. It is named as new so nobody looks for a precedent that is not there.

## 3. The three semantics that must never merge

```
FLOW      what was traded during the session       (TradingVolume(Net))
POSITION  open exposure at the close               (OpenInterest(Net))
CHANGE    POSITION minus POSITION on the previous valid trading day
```

A single `net` field carrying whichever the caller assumed is the defect this section exists to
prevent, and it is a realistic one: 外資 can sell 3,000 lots on a day when their net long
position *rises*, because the sale closed shorts. Reporting either number as "外資淨額" makes
the two indistinguishable.

`CHANGE` is derived and therefore never stored on a raw observation — the same rule
`FuturesOIData` already states for daily change. It is computed against the previous valid
trading day, which is not "yesterday": across a long weekend or a typhoon closure the previous
valid session may be four days back, and the gap is recorded so a reader can see it.

Every value carries:

```
as_of             the trading session it describes
trading_session   DAY | AFTER_HOURS | COMBINED
contract          e.g. TX, TXO
expiry            e.g. 202609, 202609W2
source            endpoint identifier
fetched_at        when THIS repository retrieved it
status            AVAILABLE | PARTIAL | MISSING | STALE | ERROR | NOT_AVAILABLE | NO_SESSION
value_semantics   FLOW | POSITION | CHANGE
schema_version
```

`value_semantics` is a stored field, not a naming convention, so a row that reaches a reader
through any path still says what it is.

### 3.1 Sessions, dates and expiries

All time in `Asia/Taipei`, via `internal/dailydata/session.go`. Process `time.Local` is never
read. Day and night rows are grouped by the feed's own `TradingSession` field (`一般` / `盤後`),
never by the clock at fetch time.

That is the whole rule, and it is enough — R15 never has to reason about which calendar day a
night session spans. Which is fortunate, because the exchange's convention is the opposite of
the intuitive one: the `盤後` rows printed in day *D*'s report were traded on the **evening
before** *D*. An earlier draft stated the reverse. Nothing behaved differently, because the
grouping never consulted the clock — but a sentence rewritten for factual accuracy had a fact
wrong in it.

A non-trading day yields `NO_SESSION` — not an empty snapshot, and not zeros.

#### The aggregation rule, derived by reconciliation against the official figure

An earlier draft classified any `ContractMonth(Week)` code that was neither `202609` nor
`202609W2` — in live data, `202609F1`, `F2`, `F3` — as `UNCLASSIFIED` and excluded it. That
rule is **wrong**, and wrong in the most dangerous available way: it produces a number, and the
status stays AVAILABLE.

`F1/F2/F3` are not noise. They are TXO expiry series, they are the bulk of the market, and the
exchange identifies them explicitly — `FinalSettlementPriceIndexOptions` returns
`{"Contract":"TXO","ContractDeliveryMonth":"202609F1","TheFinalSettlementDay":"20260904"}`.

Reconciling the by-strike report against the official `PutCallRatio` for 2026-09-04 settles the
rule empirically:

| rule | PutOI | CallOI | PutVol | CallVol |
|---|---|---|---|---|
| **official `PutCallRatio`** | **48,162** | **48,258** | **356,219** | **361,817** |
| all rows (both sessions, all expiries) | 109,555 | 113,459 | 356,219 ✅ | 361,817 ✅ |
| exclude only the expiry settling that day | 48,162 ✅ | 48,258 ✅ | 28,802 | 29,661 |
| exclude all `F*` (the rejected draft rule) | 46,852 ✗ | 46,675 ✗ | 27,506 ✗ | 28,028 ✗ |

So the rule is asymmetric, and each half matches the exchange exactly:

- **Volume** — sum every row: both trading sessions, every expiry. (`一般` alone gives 214,161
  vs the official 356,219, so the night session is not optional either.)
- **Open interest** — sum every row EXCEPT the expiry whose final settlement day is this
  trading day. Its OI is still printed in that day's report (126,594 lots for `202609F1`) but
  is no longer live exposure. The settling expiry is read from
  `FinalSettlementPriceIndexOptions`, never inferred from the code's shape.
- Summing OI across `一般` and `盤後` does not double-count, but not for the reason an earlier
  draft gave. The night rows do not carry OI `0` — they carry the string `"-"`. Verified: of
  5,988 TXO rows, the 2,868 `盤後` rows all have `OpenInterest: "-"` and no `一般` row does.

#### Sentinels: any non-numeric token is ABSENT, and `"0"` is not one

Three drafts of this section enumerated the sentinels they had seen and were wrong each time,
so the rule is now stated the other way round.

> **Normalise first, then apply the rule.** Normalisation is: trim whitespace, remove thousands
> separators (`,`), and for a percentage field strip a trailing `%`. Then: **any token that
> still does not parse as a number decodes to ABSENT** — a nil, not a zero. It contributes
> nothing to a sum, it is not an error, and an *unrecognised* token is additionally recorded in
> data health so a new one becomes visible instead of silent. `"0"` parses, and is an observed
> zero.

The normalisation step is not decoration; without it the rule destroys real data twice over:

- **`%`** — `DailyMarketReportFut`'s `%` field carries 727 genuine values (`"9.98%"`, `"1.15%"`)
  alongside 1,467 `"-"` and 56 `"0.00%"`. Un-normalised, every real value is ABSENT *and* logged
  as an unrecognised token: 783 lines of daily noise while 727 observations are silently dropped.
- **Thousands separators** — the JSON feeds carry none (checked across every field), but the
  Big5 CSV backfill path does, which is exactly why `internal/market/provider/taifex.go:192`
  already strips commas. Un-normalised, `"1,234"` → ABSENT while `"999"` parses: **only numbers
  ≥ 1000 disappear**, silently, on the historical path only.

**One normaliser, two policies — and the policy is the interesting half.**

The JSON path M3 widens uses `parseAmount` (`internal/market/provider/taifex.go:159,167,168` →
`twse.go:401`), **not** `parseOI` — `parseOI` serves only the Big5 backfill. An earlier draft
named the wrong function, which matters because `parseAmount` does the exact thing §3.1 forbids:

```go
func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" || s == "-" {
		return 0, nil          // ← MISSING mapped to ZERO
	}
	return strconv.ParseFloat(s, 64)
}
```

and it is **correct there**: on the TWSE cash rows it was written for, an empty cell genuinely
is zero, the comment says so, and `twse_test.go:194` pins it. Invariant 1 forbids changing it.

So the split is:

- **shared normaliser** — trim, remove thousands separators, strip a trailing `%` on a
  percentage field. Nothing else. It makes no decision about meaning.
- **call-site policy** — `parseAmount` keeps `""`/`"-"` → `0` and unparseable → error. R15's
  decoder applies the opposite policy to the same normalised token: `""`/`"-"` → **ABSENT**,
  unrecognised → ABSENT plus a data-health record.

Putting `""`/`"-"` → 0 inside the normaliser would make R15 inherit MISSING = ZERO and collapse
this entire section; leaving it at the call site keeps both behaviours correct for their own
data. That is the only workable arrangement, and it is stated because both readings are
available from the text otherwise.

One scope note for invariant 1's verification: the normaliser is shared with the **TWSE**
reader, so extracting it touches `twse.go`, not only the TAIFEX files.

The distinction being protected is the repo-wide MISSING ≠ ZERO contract arriving in a new feed:
`OpenInterest: "-"` (not carried) and `OpenInterest: "0"` (a strike nobody holds, 1,295 TXO
rows) are different facts. Both plausible shortcuts fail in opposite directions — treating `"-"`
as a parse error flips the OI aggregate to MISSING and fails the M5 gate; treating it as 0
passes the gate by luck and erases the distinction everywhere else.

An earlier draft also attached a causal story — "the night rows carry no OI" — which does not
hold: `DailyMarketReportFut` has **252 一般 rows** with `OpenInterest: "-"`. The rule survives
without the story, and the story is removed rather than repaired.

**Field names differ per feed** and must be read from each feed rather than assumed:
`DailyMarketReportOpt` has `Close`; `DailyMarketReportFut` has **`Last`** and no `Close` at all,
plus `Change` and `%`.

**Which field, on which feed.** The rule reads `ContractMonth(Week)` on `DailyMarketReportOpt`,
`DailyMarketReportFut` and `DailyOptionsDelta`. Nothing else is a classifier input, and one feed
in particular must never be used as one:

```json
SettledPositionsIndexOptions:
{"Contract":"TXU","ContractDeliveryMonth":"202609","ContractName":"臺指選擇權F1", ...}
```

That is the **same F1 weekly**, with the suffix moved into `ContractName` and a bare `202609` in
the expiry field. Applied there, the rule would call it MONTHLY while `DailyMarketReportOpt`
calls the same series WEEKLY — one contract, two kinds. `SettledPositionsIndexOptions` is a
settlement-event feed whose `ContractDeliveryMonth` is a delivery month, not a series code; it
joins to a series through `derivative_contracts` on `(Contract, ContractDeliveryMonth,
ContractName)`, which is where §7.2's TXO/TXU trap is reconciled.

**The large-trader feeds are not classifier inputs either, and their expiry field is not an
expiry.** `OpenInterestOfLargeTradersFutures` / `…Options` carry `SettlementMonth`, whose live
values include two sentinels — and both are bare six digits, so the rule above would call them
MONTHLY:

```
TXO 買權, TypeOfTraders 0:
  SettlementMonth 202609   OIOfMarket 26,164
  SettlementMonth 666666   OIOfMarket 16,419
  SettlementMonth 999912   OIOfMarket 48,258   ← equals the official CallOI: ALL contracts
```

`999912` is the all-contracts total. Summing rows would count the market roughly three times —
the failure COMBINATION exists to prevent, in a feed §3.1 previously did not govern at all.

So: `SettlementMonth` is **never** an `expiry_kind` input.

**And v1 does not read these feeds at all.** §11.1 shows the net residual needs no whole-market
total, which removes their only v1 consumer. They stay documented in §11.3 for the day a total
is needed, with the row-selection rule (`999912`, `TypeOfTraders = 0`, never both types, never
summed with `666666`) written down while it is fresh — but they are **out of M3's fetch scope**,
absent from M10's data-health panel, and the `OpenInterestOfLargeTradersFutures` BOM (§11.3) is
therefore not a v1 problem.

An earlier draft said the residual denominator reads one row per `(Contract, CallPut)` and
reports `MISSING` when it cannot. Both halves were wrong: v1's residual is a futures figure, so
the options-shaped key never applied, and forcing `MISSING` would suppress a number that is
fully computable without any total.

**Combination contracts.** A code containing `/` is a spread, not a series:

```
CAF 202609/202610   CCF 202609/202610   MTX 202609W2/202609   TX 202609/202612
```

271 such rows live in `DailyMarketReportFut`, and `202609/202610` is `YYYYMM`-prefixed, so the
rule above would happily call it WEEKLY. It is neither: it is one order across two expiries. Combination rows
carry `OpenInterest: "-"`, so excluding them from OI is already a no-op — the rule matters for
volume, and for stopping a `/` code from being classified as a series at all. `expiry_kind = COMBINATION`, excluded from
every per-series aggregate, and its volume reported separately if at all. (Verified: TXO carries
no combination codes in the by-strike report, so the M5 reconciliation is unaffected — the rule
exists for the futures path and for the next feed that grows one.)

`expiry_kind = UNRESOLVED` now means one thing only: **the code does not parse as
`YYYYMM`-prefixed and is not a combination.** It is counted in every total and reported in data
health.

#### Absence from `DailyOptionsDelta` means nothing

Verified live: `DailyOptionsDelta` **omits the expiry that settles today**. `202609F1` is absent
from it and present in the by-strike report carrying 126,594 lots.

This matters because draft 2 combined two rules that then contradicted each other on the single
largest row in the file. The settling expiry had no delta row → `UNRESOLVED` → "counted in every
total" → included in OI:

```
all TXO expiries              223,014
minus 202609F1 (126,594)       96,420
official PutOI + CallOI        96,420   ✅ the exclusion rule is right
223,014 / 96,420 = 2.31x                ✗ what the UNRESOLVED clause would have produced
```

So, stated as precedence rather than left to an implementer:

1. `expiry_kind` comes from the code (above). Missing delta data never makes a contract
   `UNRESOLVED`; it only means no Delta is available for it.
2. OI excludes the expiry named by `FinalSettlementPriceIndexOptions`, and that exclusion
   **outranks** any "counted in every total" rule.
3. The two never disagree, because they answer different questions: what kind of series this is,
   and whether it is still live.

#### Contract selection rules

All aggregation is over **TXO rows only** — `DailyMarketReportOpt` carries 12,012 rows across
TXO / TEO / TGO / IJO and others, and "sum every row" above means every TXO row.

| module | product | expiries | sessions |
|---|---|---|---|
| M4 institutional futures | 臺股期貨 | **no expiry or session breakdown at source** — the feed carries `Date, ContractCode, Item`, the `TradingVolume(*)` / `OpenInterest(*)` counts and their `(Thousands)` value columns, but nothing identifying an expiry or a session | n/a |
| M5 PCR | TXO | volume: all · OI: all minus the expiry settling on the session being aggregated (§3.1) | both |
| M6 OI trend | TXO | WEEKLY and MONTHLY separately (rule above); the expiry settling on that session excluded from OI | both |
| M7 defense zones | TXO | WEEKLY = tactical, MONTHLY = structural | both |

#### When the settlement feed itself is missing

If `FinalSettlementPriceIndexOptions` is unavailable on a day that turns out to be a settlement
day, OI would silently include a settled expiry — a 2.3× error on 2026-09-04, with an AVAILABLE
status. So it is not treated as an optional input:

- OI aggregates require a successful settlement-feed read. Without it the OI half is `MISSING`
  (the volume half is unaffected and stays AVAILABLE — they have different dependencies).
- The reconciliation gate is the backstop: if computed PCR does not match the official
  `PutCallRatio`, the aggregate is `ERROR` with the discrepancy recorded, never a number.
- If `PutCallRatio` is itself missing, the backstop is gone and the OI aggregate is `MISSING`
  rather than unverified-but-published. An unchecked number that happens to be right is
  indistinguishable from one that happens to be wrong.
- A non-settlement day and a settlement feed that has not updated yet look identical from one
  response. They are separated by the exchange's own answer, compared against **the trading date
  being aggregated** — never against "today":

  > the aggregate for session *D* excludes the expiry named by the settlement feed **iff** that
  > feed's `TheFinalSettlementDay` equals *D*.

  Anchoring to the clock was a live bug in an earlier draft. So is comparing against the
  **live** feed, which is the same bug from the other side: `FinalSettlementPriceIndexOptions`
  carries no date of its own (keys are `TheFinalSettlementDay, Contract, ContractName,
  ContractDeliveryMonth, TheFinalSettlementPrice`) and returns only the most recent settlement
  event. TXO's next settlement is `202609W2` on 2026-09-09, so recomputing session 2026-09-04
  on or after that date gets `20260909 ≠ 20260904`, does not exclude `202609F1`, and lands on
  223,014 vs 96,420 — the same 2.31× error, reached by rerunning a day that was correct when it
  was first fetched.

  > The comparison reads the settlement snapshot **archived for D**, never the live feed.

  A failed read is `MISSING`. The feed's date is never assumed to be the session's.

#### Dateless feeds and `trading_date`

`FinalSettlementPriceIndexOptions`, `DailyOptionsDelta` and `SettledPositionsIndexOptions`
supply **no trading date**. §7.3's key `(source, trading_date, trading_session, revision_no)`
cannot be populated from their content, and §2.2's `ErrDateMismatch` check has nothing to
validate against.

The rule, which also makes the settlement comparison above well-defined:

- a fetch is always **for** a target session *D*, decided by `internal/dailydata/session.go`
  before any request is made
- a feed that carries an **observation date** — the trading day the data describes — is
  validated against *D* (`ErrDateMismatch` unchanged)
- a feed that carries none is archived under *D* with `date_source: ASSIGNED` recorded on the
  snapshot, so a reader can always tell an observed date from an assigned one

The qualifier "observation date" is load-bearing. All three "dateless" feeds do carry a date
field; none of them is a trading day, and validating against them would break the layer:

| feed | field | what it is |
|---|---|---|
| `SettledPositionsIndexOptions` | `TheFinalSettlementDay` | a settlement date — equals *D* only on settlement days |
| `DailyOptionsDelta` | `ContractSettlementDay` | a **future** date, per row |
| `IndexFuturesAndOptionsMargining` | `Date` | see below |

Margin's `Date` is the exchange's **effective date**, not a publication date: it changes only
when margins change. Validating it against *D* would raise `ErrDateMismatch` on every day
without a margin revision, leaving margin permanently MISSING and M7 permanently at
`margin_source: NONE`. It is therefore `date_source: ASSIGNED` with the effective date stored
separately, and M7 uses the most recent effective date ≤ *D* — a carry-forward, legitimate
precisely because the exchange states the value applies until it changes. That is the one place
§2.3's "yesterday's value is never presented as today's" does not apply, and it is called out
here rather than left to look like an exception nobody noticed.

**The carry-forward is bounded to one snapshot.** It walks `effective_date` **inside the margin
snapshot archived for *D***; it never walks `trading_date` across snapshots. Without that bound
the two rules contradict each other on the day a fetch fails: §2.3 says `margin_source: NONE`,
while §7.3's `WHERE trading_date <= :A` would happily return the previous day's snapshot and
carry a still-effective value forward. A failed fetch for *D* means no margin snapshot for *D*,
which means `NONE` — the exchange's "applies until changed" licenses reusing a value we
**observed for D**, not reusing an observation we never made.

(Presentation note for M10: that panel row's `as_of` is *D*, not the effective date. Showing the
effective date would light up §6's STALE rule on a margin that is correct and simply has not
changed — invariant 4 in reverse.)
- every point-in-time read of these feeds resolves through §7.3 against the snapshot archived
  for *D*. The live response is never consulted for a historical session

`date_source` is a stored field for the same reason `value_semantics` is: the distinction has to
survive reaching a reader by any path, and "we assumed this belonged to D" is exactly the kind
of claim that becomes invisible once it is only a convention.

## 4. Feature switches

R15 is a top-level block, following `research:` (R13) and `health_dashboard:` (R14) rather than
the flat `scanner.enable_X` flags: like those two, R15 does not live under `internal/scanner`,
and the config shape is where that fact is visible first.

```yaml
# ── TAIFEX 衍生品風險層 (R15) — SHADOW ONLY ──────────────────────────────
derivatives:
  enabled: false                       # 總開關，預設 false — 完全不 fetch / 不算 / 不寫入
  show: false                          # 顯示開關，預設 false — 只控制 report ⑱
  data_dir: "data/derivatives"
  store:
    path: "data/research/r15_derivatives.db"
  iv:
    provider: ""                       # 空 = NOT_AVAILABLE（§2.3），不是 0
```

`show: true` with `enabled: false` is a **startup fatal**. It is NOT inherited:
`scanner.Config.Validate()` covers only its own two pairs (§1.2), and this block is not part of
`scanner.Config`. R15 adds its own validation at config load, and a test asserts the
combination is rejected — otherwise the report would render a section for data nobody
computed, which reads as "there was nothing to show" rather than "nobody looked".

Disabled means absent: no section, no zeros, no `NEUTRAL` placeholder, no empty shell.

## 5. Point-in-time contract

Identical in shape to the valuation archive, because that contract has already survived a
review round that caught a live look-ahead:

- raw snapshots are written to `data/derivatives/<observed-date>/` and are **immutable**; a
  re-run merges rather than overwrites
- every query is bounded by `as_of` on **both** the session date and the archive date, so a
  correction archived later cannot reach an earlier reading
- an official revision creates a NEW snapshot; the original is never edited. Which revision a
  reading dated *A* sees is defined in §7.3 — the greatest `fetched_at ≤ A`, so a correction
  fetched later cannot improve a past evaluation
- computed features record the `feature_version` and the snapshot ids they were derived from

The failure this prevents is specific: a strategy evaluated on revised data it could not have
had at signal time will look better than it was, and nothing downstream can detect it
afterwards.

---

## 6. Status vocabulary

```
AVAILABLE      observed, fresh, complete
PARTIAL        observed, some components absent — the present ones are usable
MISSING        not observed
STALE          observed, but older than the expected freshness for this dataset
ERROR          the fetch or parse failed; the reason is recorded
NOT_AVAILABLE  no authorized source exists (IV today)
NO_SESSION     the exchange did not trade
NOT_PUBLISHED  the session happened; the exchange has not released this dataset yet
```

`NOT_PUBLISHED` is separated from `MISSING` because the two imply opposite actions. `MISSING`
means the observation was not made and will not appear by waiting; `NOT_PUBLISHED` means come
back later — TAIFEX releases institutional positions well after the by-strike report, so a
15:00 run legitimately sees one and not the other. Collapsing them would either make a normal
afternoon look broken or make a genuine gap look like patience.

**`MISSING` is never `NEUTRAL`.** A neutral reading is a conclusion drawn from data; a missing
one is the absence of data. R14 spent a milestone on the equivalent confusion
(`INSUFFICIENT_DATA` rendered as `0`), and the rule that came out of it applies unchanged: a
value with nothing behind it must never look like a number.

### 6.1 Relationship to the existing vocabulary

The repo already has one (`internal/healthcheck/types.go:43-56`,
`internal/valuation/model.go`). R15's is a **superset with a different domain**, not a rival, and
the mapping is fixed here so nothing has to guess at a boundary:

| R15 | existing equivalent | why R15 needs its own |
|---|---|---|
| `AVAILABLE` | `AVAILABLE` | same |
| `PARTIAL` | `PARTIAL` | same |
| `MISSING` | `UNAVAILABLE` | R15 distinguishes "not observed" from "no source exists"; the existing enum does not |
| `NOT_AVAILABLE` | `NOT_IMPLEMENTED` | same meaning: no authorized source, waiting never helps |
| `STALE` | — | new: observed but too old. Has no existing equivalent, and collapsing it into either neighbour is the invariant-4 failure |
| `ERROR` | — | new: the attempt failed, as distinct from the data not existing |
| `NO_SESSION` | `NO_SESSION` (`internal/dailydata`) | same, reused |

Two existing statuses have **no R15 equivalent, deliberately**: `NOT_APPLICABLE` (the metric
does not apply to this subject) and `INSUFFICIENT_DATA` (computable in principle, not enough
observations yet). Neither describes a fetch outcome, which is what the R15 enum is for. R15
expresses "not enough observations" as `INSUFFICIENT_SAMPLE` at the evaluation layer (§9) and
as a `RiskLevel` (§10.1), and never as a source status.

Those two vocabularies are **different questions and must not be merged with the above**: `INSUFFICIENT_SAMPLE` (§9) is about evaluation sample size, and `RiskLevel`'s
`INSUFFICIENT_DATA` (§10.1) is a risk state. A single enum carrying all three would let a
caller write a switch that silently treats a risk state as a fetch failure.

### 6.2 Atomicity when only some rows are bad

Undefined behaviour here is how a partial payload becomes an AVAILABLE number, so the split is
fixed rather than left to whoever writes the parser:

**Whole-response rejection** — nothing is stored, status `ERROR`, when the failure is
STRUCTURAL and the response cannot be trusted as a whole:

- the body is not the expected content type, or is an HTML error page
- the envelope does not decode
- a REQUIRED column is absent (§3.1: an unresolvable 本益比 or 股票代號 column already fails
  this way)
- the payload is empty where the exchange should have returned rows
- the response's own date contradicts the requested session (`ErrDateMismatch`)

**PARTIAL** — valid rows are stored, rejected rows are COUNTED, status `PARTIAL`, when the
failure is per-ROW and the row's identity is intact: a malformed number in an otherwise
well-formed row.

The consequence that makes this worth fixing in the spec: **an aggregate whose correctness
depends on completeness may not be published from a PARTIAL snapshot.** A PCR computed over
"most of the rows" is not a PCR, and it would reconcile against nothing. §3.1's rule applies —
`ERROR` with the discrepancy recorded, never a number.

A row rejected for a malformed value is not the same as a row carrying `"-"`. The second is an
observation of absence (§3.1) and is stored normally; only the first counts as rejected.

**Which malformed value rejects the row — the column's nullability decides, not the token.**
§3.1 says an unparsable token is ABSENT and explicitly not an error; this section says a
malformed number in a well-formed row is a rejected row. Read as rules about tokens the two
contradict each other. Read against M2's schema they do not, and the schema is the arbiter:

| the malformed value is in | example | outcome |
|---|---|---|
| a NULLABLE value column | `oi`, `volume`, `settlement`, `lots`, the margins | **ABSENT** — the column exists to express exactly this |
| a NOT NULL key column | `options_oi_by_strike.strike`, `call_put`, `trading_session` | **rejected row**, counted, PARTIAL |

A row whose strike will not parse has no identity: it cannot be keyed, stored, or read back, so
there is nothing to call absent. A row whose open interest will not parse is a real strike about
which one number is unknown. That is the difference, and it is a property of the schema rather
than a judgement about the token.

An unrecognised `CallPut` or `TradingSession` rejects the row for the same reason — the first
decides a side, the second decides which snapshot the row belongs to (§10.2b), and both are in
the key.

### 6.3 Revision timestamps must not go backwards

`PutSnapshot` rejects a revision whose `fetched_at` precedes the latest revision's, with a typed
error. The readers order on `fetched_at` before `revision_no`, so an out-of-order revision would
be permanently unreachable: an as-of read would keep returning revision 0 while a newer
correction sat in the table.

This is a natural invariant rather than a constraint fought against, because `fetched_at` means
**when this repository retrieved the bytes** — not when the exchange published them. A backfill
that recovers a 2025 session today has `fetched_at` = today, which is monotonic by construction.
The rejection therefore fires only on a caller trying to assert a retrieval that did not happen
in that order, which is exactly what should fail.

Nothing silently adjusts a timestamp. If a source's own publication time is ever needed it gets
its own column with its own provenance; overwriting `fetched_at` to make an import fit would
destroy the only ordering the point-in-time reader has.

## 7. Persistence

### 7.1 Its own database file, the same machinery

R15 writes `data/research/r15_derivatives.db`, with its own migration list starting at version 1
and its own `schema_migrations` table.

**Why not migration 3 on the shared list.** `internal/store/schema.go:244-246` refuses to open a
database whose recorded version exceeds the binary's `SchemaVersion`. Adding migration 3 to the
shared list therefore makes any binary built without R15 refuse `r13.db` entirely. A default-off
research layer that can lock the existing research store on rollback is not default-off in any
meaningful sense.

**What this actually costs in `internal/store`.** An earlier draft called this "additive" and
"the migration list becomes a parameter". That was wrong on both counts and M2 must not be
started from it:

- `migration` and its fields, the `migrations` var and `migrate` are all **unexported**
  (`schema.go:17-27,231`), so no other package can build a list today.
- `migrate` takes its ceiling from the package const `SchemaVersion`, **not** from the list
  (`schema.go:244-246`). Parameterising only the list would judge R15's database against ceiling
  2, and the day R15's own list reached version 3 the R15 code would refuse the R15 file.
- `store.Config` (`store.go:34`) has no field for either, and `Open`/`OpenContext`
  (`store.go:72,77`) cannot be told which schema to apply.

So M2's first task is a small, explicit **API change** to `internal/store`:

```go
// Schema is one database's migration set. The R13 schema is the zero value's default, so
// every existing caller is unchanged.
type Schema struct {
    Name       string       // for error messages: "r13", "r15_derivatives"
    Migrations []Migration  // append-only, ordered
}
func (s Schema) Version() int   // = the highest declared migration version, NOT a const

type Migration struct {  // exported; was `migration`
    Version int
    Name    string
    Stmts   []string
}
```

`Config` gains `Schema Schema` with a **`yaml:"-"` tag** (zero value → the R13 schema). The tag
is not cosmetic: `research.Config` embeds `store.Config` with a yaml tag
(`internal/research/research.go:19`), so an untagged field containing `[]Migration{Stmts []string}`
would make arbitrary DDL reachable from a config file.

`migrate` takes the ceiling from `Schema.Version()` rather than the const, which is what makes
two databases with different version sequences possible in one binary. `SchemaVersion` becomes
`= R13Schema.Version()` rather than a literal, so the two can no longer drift — today nothing in
production reads the const, and `internal/store/store_test.go:67` is the only thing keeping it
honest.

One access path to state, because M2 will hit it immediately: `*store.Store` exposes no generic
query method. `WithTx` (`internal/store/store.go:163`) is the only exported way for
`internal/derivatives` to reach its own tables, **including for reads**. That is workable and
requires no new API, but it is not obvious from the outside.

**Existing behaviour must not change**: `SchemaVersion` still resolves to 2 for R13 (now a `var` derived from `R13Schema.Version()` rather than a literal — nothing in production reads it, only tests), every
current call site compiles untouched, and a test asserts an existing `r13.db` migrates to the
same end state before and after. This is reuse of one mechanism, not a second persistence
system — but it is an API change, and calling it additive was a mis-description worth
correcting here rather than discovering in M2.

### 7.2 Tables

```
derivative_snapshots      append-only raw observations, keyed (source, trading_date,
                          trading_session, revision_no) — §7.3
derivative_contracts      code, kind, expiry, expiry_kind, settlement_date
institutional_derivatives FLOW + POSITION, keyed
                          (snapshot, institution, contract, call_put, value_semantics, side)
                          — value_semantics is IN the key: one row cannot be both FLOW and
                          POSITION (§3), and leaving it out makes the two overwrite each other
                          call_put carries the options dimension; without it the 30-row
                          CallsAndPuts feed (§2.1) could only be stored by collapsing it back
                          into the 15-row combined feed it was chosen over
options_aggregate         put/call volume + OI totals, weekly/monthly split
options_oi_by_strike      (snapshot, contract, expiry, strike, call_put) -> oi, volume, settlement
margin_rates              (contract, effective_date) -> clearing/maintenance/initial
iv_observations           (snapshot, contract, expiry, atm_strike) -> iv, source, status
derivative_features       computed values + feature_version + input snapshot ids
derivative_data_health    per-source status, as_of, fetched_at, expected_freshness, reason
strategy_evaluations      §8
derivative_outcomes       §8
```

Every table carries `schema_version`. Tables holding **observations** also carry `source` and
`fetched_at` — where the bytes came from and when this repo retrieved them.

Three tables hold **derived** values instead and carry their own provenance:
`derivative_features` (`feature_version`, `input_snapshot_ids`, `computed_at`),
`strategy_evaluations` (`feature_version`, `created_at`) and `derivative_outcomes`
(`first_computed_at`, `last_updated_at`). A `source` column on a computed row would name an
endpoint that did not produce it, and a `fetched_at` would be a retrieval that never happened —
the input snapshot ids are what makes such a row re-derivable, which is the property `source`
provides for an observation.

**No key column is ever NULL.** SQLite treats every NULL as distinct in a UNIQUE index, so a
nullable key column silently disables the constraint for exactly the rows it covers — and
invariant 9 (a rerun inserts nothing new) would then hold for options and quietly fail for
futures. Sentinels, not NULLs:

| column | sentinel | when |
|---|---|---|
| `call_put` | `''` | futures rows, which have no call/put dimension |
| `trading_session` | `COMBINED` | feeds with no session dimension: `PutCallRatio`, margin, settlement, delta, settled positions |
| `side` | — | `LONG` / `SHORT` / `NET`. All three are stored, and `NET` is taken from the feed's own net column rather than derived from long − short — the rule `FuturesOIData` already states, so that a column-layout change surfaces as a mismatch instead of being papered over by arithmetic |

**`call_put` is normalised on the way in, and the feeds disagree about how to spell it:**

| feed | key | values |
|---|---|---|
| `DailyMarketReportOpt` | `CallPut` | `買權` / `賣權` |
| `SettledPositionsIndexOptions` | `Call/Put` | `買權` / `賣權` |
| `OpenInterestOfLargeTradersOptions` | `CallPut` | `買權` / `賣權` |
| `MarketDataOfMajorInstitutionalTradersDetailsOfCallsAndPutsBytheDate` | `CallPut` | **`CALL` / `PUT`** |

An earlier draft asserted the values are "never `Call`/`Put`", which is false for the last row —
the very feed §2.1 selects for M5. Normalisation is to `CALL` / `PUT` at the parser boundary,
and an unrecognised value is a parse **error**, not a silent third category: a mis-mapped
call/put inverts a PCR without changing its magnitude, which no reconciliation gate can see.

**One cross-endpoint trap for `derivative_contracts`**: the same series is named differently by
different feeds. `DailyMarketReportOpt` and `FinalSettlementPriceIndexOptions` say
`TXO` / `202609F1`; `SettledPositionsIndexOptions` says `Contract: TXU`,
`ContractDeliveryMonth: 202609`, `ContractName: 臺指選擇權F1` for the same contract. A join keyed
on `(contract, expiry)` will silently match nothing. `derivative_contracts` is the place that
reconciliation lives, and a test must pin at least this one known pair.

### 7.3 Identity, idempotency, and which snapshot a reader gets

`derivative_snapshots` is **append-only. Nothing in R15 ever UPDATEs a snapshot row.**

```sql
UNIQUE(source, trading_date, trading_session, revision_no)
```

`revision_no` starts at 0. `content_hash` is a plain column, never part of a key — an earlier
draft put it in the unique index, which would have inserted a row whenever the response bytes
differed in any way, including key order, directly contradicting invariant 9.

Two rules cover every re-fetch:

- **same content hash** → no insert. Row counts are unchanged; a test asserts that across a
  second identical run.
- **different content hash** → insert `revision_no + 1`. The earlier revision is left exactly as
  written.

There is no separate revisions table and no `superseded_at`. The previous draft had both, and
also said "the primary row's `content_hash` and `superseded_at` are updated. The original is
never edited" — which is a contradiction in one sentence, and left a real leak: any reader going
through the primary row would have seen revised bytes under the original key. Append-only
removes the leak and the contradiction together, and needs no field that nothing reads.

**Which revision the hash is compared against**: the **latest** revision for that key, not any
revision. Under "any", an exchange that reverts a correction would find the original hash
already present, insert nothing, and leave every reader permanently on the superseded content.

**Reader selection**, stated to the boundary because this is the section that exists to prevent
look-ahead and a vague bound is exactly how look-ahead returns:

```sql
WHERE trading_date <= :A
  AND fetched_at   <= :A_end     -- 23:59:59.999 on A, Asia/Taipei, converted to UTC
ORDER BY fetched_at DESC, revision_no DESC
LIMIT 1                          -- per (source, trading_date, trading_session)
```

`fetched_at` is stored UTC at second granularity (`internal/store/store.go:18`) while §3.1
mandates Asia/Taipei for every trading-day decision, so the two must be reconciled explicitly
rather than compared as strings: *A* is a Taipei trading date, and the bound is the **end** of
that day in Taipei, expressed in UTC. `revision_no DESC` is the tie-break when two revisions
share a second — without it the query is non-deterministic, and a non-deterministic
point-in-time read is a look-ahead bug that only appears under load. Because revision 0 is itself a row in the same table, a reading dated before
any correction resolves to the original observation rather than to nothing — the case the
previous two-table design silently failed.

A revision fetched after *A* is invisible to a reading dated *A*. That is what stops a later
correction from improving a past evaluation, and it mirrors the archive-date bound in
`internal/valuation` — which a review round caught being absent from the persistence shape once
already.

## 8. Strategy lifecycle

```
EXPERIMENTAL → SHADOW → VALIDATED → ACTIVE
                 ↓          ↓          ↓
              RETIRED   DEGRADED   DEGRADED
```

Every R15 strategy starts and stays at `EXPERIMENTAL` or `SHADOW` for v1. `ACTIVE` is not
reachable in this milestone, and no code path may set it.

Stored per signal: `strategy_id`, `strategy_version`, `signal_time`, `as_of`,
`feature_version`, `sample_size`, `expected_horizon`, `entry_reference`, `prediction`,
`confidence`, `MFE`, `MAE`, `outcome_{1,3,5,10,20}d`, `validation_status`,
`retirement_reason`.

Retiring a strategy sets a status and a reason. **Failed results are never deleted or hidden** —
a research store that quietly loses its failures reports only survivors, which is the
mechanism that makes every strategy look good.

### 8.1 Promotion gates

Minimum before a strategy may leave `SHADOW`:

- `effective_N ≥ 60` non-neutral, non-missing signals
- out-of-sample only: the evaluation window must not overlap any window used to choose a
  parameter
- lift over the stated baseline positive at the low end of a bootstrap interval
- coverage ≥ 0.80 over the evaluation window
- at least two distinct market regimes represented

These thresholds are **HEURISTIC and NOT BACKTEST-FITTED**, versioned as `R15-v1`, and are not
to be adjusted to make a candidate pass. Four or eight good days is not evidence; the gate
exists precisely because it will feel too strict at the moment someone wants to cross it.

---

## 9. Out-of-sample validation

Every reported evaluation carries: `effective_N`, coverage, hit rate, baseline, lift, median
and mean return, MFE, MAE, a bootstrap interval, missing count, neutral count, invalid count,
regime segmentation, and — if trades are simulated — the transaction-cost assumption.

Below the sample floor the answer is `INSUFFICIENT_SAMPLE`, with the counts shown. Excluding
neutral samples to raise a hit rate, or reporting a hit rate without the return distribution,
is prohibited: both make a coin flip look like a signal.

---

## 10. Work items

| id | title | depends on |
|---|---|---|
| M1 | Repository audit & canonical spec | — |
| M2 | Persistence & snapshot foundation (own DB, own migration list — §7.1) | M1 |
| M3 | TAIFEX fetch, normalize & cache — **extending `internal/market/provider`, see §10.2** | M2 |
| M4 | Institutional derivatives FLOW / POSITION / CHANGE | M3 |
| M5 | PCR & options structure | M3 |
| M6 | Layered OI trend | M3 |
| M7 | Defense profile | M6 |
| M8 | IV risk state (interface + NOT_AVAILABLE) | M2 |
| M9 | Composite `DerivativesRiskView` | M4–M8 |
| M10 | Report, data health & validation | M9 |

### 10.1 Rules that apply to several work items

**PCR reconciliation (M5)** — computed Volume PCR and OI PCR must equal the official
`PutCallRatio` for the same session (§3.1). This is an acceptance gate, not a sanity check: it
is the only thing standing between the aggregation rules and a plausible-looking wrong number.

It is **M5's gate, against the full archived response**, and cannot be met by a committed
fixture: the official totals hold only over all 12,012 rows, and a TXO-only fixture is ~1.9 MB.
M3's fixture proves the RULE instead — the same three aggregations over the trimmed rows, with
each rejected rule asserted to produce a visibly different number (including the settling expiry
inflates OI, excluding all `F*` undershoots, `一般`-only undershoots volume). The full-capture
verification of the real totals is recorded in `internal/derivatives/testdata/README.md`.

The distinction is what keeps the M3 test honest. A fixture carrying its own computed expectations
would agree with any implementation; asserting that the WRONG rules fail does not.

**Emerging and collapsing OI walls (M6)** must not be decided on absolute lot counts alone.
A wall that vanishes because its contract expired is not a collapsing wall, and treating it as
one would generate a bearish signal on every settlement day — which, per §3.1, is neither
reliably the third Wednesday nor a monthly event: the F-series weeklies settle far more often
and now carry the larger share of open interest. Required inputs:
absolute change, relative change, market-wide OI change, persistence across sessions, distance
to expiry, and an explicit roll adjustment. A dedicated test asserts that a full expiry roll
produces **no** collapse signal.

**Defense zones (M7)** are zones, not points. A single strike printed to the lot is false
precision. Deterministic method, tested on unimodal and multimodal and small-sample inputs.

**Direction and risk are separate axes (M9)**:

```
Direction = BULLISH | NEUTRAL | BEARISH | MIXED
RiskLevel = LOW | NORMAL | ELEVATED | HIGH | INSUFFICIENT_DATA | STALE
```

Strongly bullish and HIGH risk is a coherent, common state. Collapsing them into one score
destroys the only thing this layer adds.

If a 0–100 composite is added at all, components stay individually visible, a missing component
is not 0, effective weights are renormalised over what is present, coverage is displayed, and
low coverage may not report high confidence. Weights live in one tested place.

---

### 10.2 How M3 relates to the existing provider (FU-10)

§1.1 says R15 extends `internal/market/provider/taifex.go` rather than replacing it. Concretely:

- The institutional futures fetch stays **one function**. M3 widens the existing `taifexRow` to
  decode `TradingVolume(*)` alongside `OpenInterest(*)`, and widens `FuturesOIData` (or returns
  a superset type) so both semantics reach callers. `internal/market` keeps consuming exactly
  what it consumes today.
- Options, PCR, margin and settlement datasets are **new endpoints with no existing reader**,
  so their fetchers are new code in `internal/derivatives/provider`, Big5 nowhere. The
  large-trader feeds are **not** in M3's scope (§2.1, §3.1) — v1 has no consumer for them.
- Date handling splits per §3.1, and this paragraph previously said "date validated against the
  response" for all of them, which would have been actively harmful: margin's `Date` is an
  effective date, so validating it would raise `ErrDateMismatch` on every day without a margin
  revision and leave M7 permanently at `margin_source: NONE`. Feeds carrying an **observation
  date** are validated against *D*; the rest are archived under *D* with
  `date_source: ASSIGNED`.
- There must be exactly **one** code path that turns a TAIFEX institutional-futures response
  into a domain value. A reviewer check on M3: `grep` for a second decoder of
  `OpenInterest(Net)` should find nothing. FU-10 records what a second copy costs — two
  implementations of the institution loop disagreed about whether a downgrade counted as
  success, so the same day reported differently depending on which entry point ran.

### 10.2b Constraints M2 established that M3 must honour

Recorded here because they are consequences of the schema rather than choices M3 gets to make:

- **`DailyMarketReportOpt` must be stored as TWO snapshots**, `DAY` and `AFTER_HOURS`.
  `options_oi_by_strike` is keyed `(snapshot_id, product, expiry_code, strike, call_put)` with
  no session column, so storing the whole response under one `COMBINED` snapshot makes the
  2,868 `盤後` rows collide with the `一般` rows on the UNIQUE index — while §3.1's volume rule
  needs both sessions. The session lives on the snapshot, not on the strike row.
- **`trading_date` is written as `YYYY-MM-DD`, never the feed's native `20260904`.**
  `PutSnapshot` now rejects the latter, because `trading_date` is compared as a bare string
  against an as-of date and the two formats do not order together.
- **`trading_session` is one of `DAY` / `AFTER_HOURS` / `COMBINED`**, validated on write. A
  typo would open a parallel key space in which every re-fetch inserts and no as-of read ever
  finds the row.
- **A backfill must not write a revision with an earlier `fetched_at` than the revision it
  follows.** The readers order on `fetched_at` before `revision_no`, so a backfill replaying
  archived responses out of order would make an as-of read return revision 0 while a later
  revision exists. Nothing in M2 can produce that; M3's backfill can.
- **`derivative_outcomes` records no price series.** `first_computed_at` does not identify what
  produced `outcome_1d…20d`. Adding that column is M9's problem and needs no R13 change.
- **The TXO/TXU reconciliation test §7.2 requires does not exist yet.** `derivative_contracts`
  has the `alt_product` / `alt_expiry_code` / `contract_name` columns for it; populating and
  testing them belongs to M3/M4 and must not be treated as delivered with M2.

### 10.3 Report layout (M10)

The scanner report numbers its sections; ⑨–⑯ are per-stock-card blocks gated by `GV.ShowX`
(`internal/report/report.go:827-891`) and ⑰ is fundamental/valuation. **R15 is ⑱.**

R15 is not the first market-level content here — `GV.FX` and `GV.Macro` already render once in
the header, documented in `report.go` as "Market-level, so it renders ONCE in the header — never
per stock", and the 輪動 / 市場掃描 tabs are market-level too. R15 follows that established
placement rather than inventing one. (An earlier draft claimed to be first, which is the same
kind of error §2.4 avoids in the other direction: asserting a precedent's absence without
looking.)

What *is* a decision: ⑨–⑰ all sit inside `{{ range $i, $e := .Watchlist }}` (`report.go:2327`),
so a market risk layer placed among them would repeat identically on every card and read as if
it described that stock.

- **Placement**: one section in the **header region, immediately after the FX/Macro block and
  before the tab strip** (`report.go:2243-2249`), so it is visible from every tab rather than
  belonging to one of the three card lists (持倉 `:2253`, 飆股候選 `:2310`, 市場掃描 `:2673`).
  Rendered once, outside every card loop, participating in no sort.
- **Gate**: `GV.ShowDerivatives`, from `derivatives.show`. Absent when false — no heading, no
  empty shell.
- **Content** (each row carries its own status; a MISSING row renders as a status label, never
  as a number or a dash):
  1. summary — `Direction`, `RiskLevel`, `Confidence`, `SampleSize`, `as_of`
  2. institutional positions — FLOW / POSITION / CHANGE per institution, each **labelled with
     its semantics**, with 1/3/5/10-day changes and the percentile
  3. PCR — Volume PCR and OI PCR separately, weekly and monthly, with change, percentile,
     sample size, and the official reconciliation status (§3.1)
  4. OI structure — weighted centre, 3/5/10-day migration, emerging and collapsing walls,
     weekly∩monthly overlap, distance to price in points and in ATR
  5. defense zones — S1–S3 / R1–R3 as **zones with width**, strength, distance, and the
     parameters used
  6. IV — `NOT_AVAILABLE` with its reason until a source exists
  7. warnings and missing reasons, verbatim
  8. calculation parameters and `feature_version`
- **Data health sub-panel** (⑱-b), one row per source — TAIFEX futures, institutional futures,
  options aggregate, options OI by strike, margin, IV provider, derivative computation, outcome
  backfill — each with status, as-of, fetched-at, expected freshness, source and reason. §14
  makes a week of AVAILABLE here the precondition for turning `show` on, so this panel is
  required by M10, not optional.
- **Test**: rendering is asserted by executing the template, not by grepping the source — the
  technique R14 adopted after grep-only page tests passed while the page showed
  "觀察 undefined / 20".

### 10.4 M4 — the institutional position contract

The three semantics of §3, made arithmetic:

```
FLOW     = TradingVolume(Long) − TradingVolume(Short)      what was traded this session
POSITION = OpenInterest(Long)  − OpenInterest(Short)       exposure held at the close
CHANGE   = POSITION(D) − POSITION(previous valid observation)
```

They are never substituted for one another, and the live data shows why in one line
(2026-09-04, 臺股期貨):

| institution | FLOW net | POSITION net |
|---|---|---|
| 自營商 | −1,856 | −697 |
| 投信 | +1,785 | +76,174 |
| 外資及陸資 | −880 | −82,389 |

投信 traded +1,785 lots while holding +76,174 — a factor of 43. A view that showed either
number under the label "淨額" would be defensible prose and useless evidence.

Forbidden, each because it silently produces a plausible number: FLOW standing in for POSITION;
CHANGE derived by differencing FLOW; falling back to FLOW when POSITION is absent; filling an
absent CHANGE with 0; adding traded lots to open lots; mixing lots with contract value.

**On the word `net`**: what is forbidden is an AMBIGUOUS one — a field called `net` that a reader
cannot resolve to a semantics. `FlowNetLots` and `PositionNetLots` on the raw observation are
fine and in fact required: they name the exchange's own net COLUMN, which §3 insists NET be
taken from rather than derived as long − short. The rule is that nothing reaches a reader as an
unqualified "淨額" — every exported quantity carries its semantics in its type.

#### Previous valid observation

CHANGE's baseline is **not** `D − 1`. It is the most recent EARLIER trading date that has, for
the same dataset, investor, instrument scope, session policy and value semantics, an observation
that **carries a usable POSITION** and is visible at the current as-of cutoff.

An earlier draft said "AVAILABLE" in one sentence and "a PARTIAL snapshot LACKING THE POSITION
columns is skipped" in the next, which are different rules — the second admits a PARTIAL that
has POSITION. **The second is correct**, and the reason is what PARTIAL means at the snapshot
level (§6.2): some rows were rejected, the rest are real observations. A snapshot whose 臺股期貨
POSITION row parsed cleanly is a usable baseline whatever happened to an unrelated product's
row, and discarding it would push the baseline further back for no gain in truth.

So the skip list is: weekends, holidays, `NO_SESSION`, `NOT_PUBLISHED`, `ERROR`, `STALE`, and
any snapshot — `AVAILABLE` or `PARTIAL` — in which THIS investor/scope's POSITION is absent.
The status that matters is the metric's, not the snapshot's. `BaselineStatus` records which it
was, so a baseline taken from a PARTIAL snapshot is visible rather than inferred.

`STALE` is skipped and that is not configurable: a stale reading is one whose freshness contract
already failed, and using it as a baseline would launder that failure into a CHANGE.

Skipping is not silent: `PreviousTradingDate`, `ObservationGap` (in valid observations) and
`SkippedDates` travel with the value. A multi-day difference labelled `CHANGE_1D` is the defect
this exists to prevent.

If the baseline is further back than the configured tolerance the result is `STALE_BASELINE`,
not a number.

**The tolerance scales with the horizon.** A single-hop tolerance applied flat would mark every
`Change10` stale, because ten valid observations legitimately span about two calendar weeks. It
is `MaxBaselineCalendarGapDays + (N−1) × CalendarDaysPerObservation` — defaults 10 and 3, where
10 is sized for the Lunar New Year closure and 3 leaves room for a long weekend at every hop.

Both numbers are HEURISTIC and NOT BACKTEST-FITTED: they come from the exchange calendar, not
from any outcome.

#### Horizons

`ChangeN` = POSITION(D) − POSITION(N valid observations earlier). **N counts observations, not
calendar days.** With fewer than N available the answer is `INSUFFICIENT_HISTORY` with the value
absent — never a shorter horizon wearing the longer label, and never 0. Each horizon carries its
current date, baseline date, effective observation count, calendar gap and status.

#### Instrument scope and session policy

Scope is explicit and never inferred. The feed carries 22 products; 臺股期貨 (TX), 小型臺指期貨
(MTX) and 微型臺指期貨 are different instruments and their lots are not comparable, let alone
addable. Every view records `InstrumentScope`, the contract-selection policy, included and
excluded contracts, and coverage.

The institutional futures feed has **no session dimension** — it carries `Date, ContractCode,
Item` and the two semantics' columns, nothing else — so its session policy is `COMBINED`, fixed
by the data contract. It is never chosen by reading the clock.

#### Percentiles

FLOW, POSITION and CHANGE each get their own reference distribution; sharing one would rank a
traded-lot figure against held-lot figures. The distribution for a reading dated *A* contains
only observations at or before *A* that were **visible at A** — the §7.3 rule, applied to
history rather than to a single row. Below the configured minimum the answer is
`INSUFFICIENT_SAMPLE` with the actual and required N, not a percentile computed from four points.

#### Aggregation across institutions

A three-institution total requires all three observed, on the same trading date, session,
dataset, instrument scope, revision view and value semantics. Any missing component makes the
aggregate `PARTIAL` with the **value absent**. 外資 being present is not grounds for calling the
total available.

**A missing institution and a mixed identity are different failures.** A missing one is a fact
about the data: `PARTIAL`, value absent, name the absentee. A mixed identity — two trading
dates, two scopes, two revisions, or the same institution twice — is a caller error, because no
number would have been correct, and it is an ERROR rather than a status on a value.

**The aggregate CHANGE additionally requires a common baseline.** Three institutions can resolve
to three different previous-valid dates: one had a `NOT_PUBLISHED` day the others did not. The
sum of those three differences is not the difference of the sums, so an aggregate CHANGE over
differing baselines is `PARTIAL` with the differing dates named. FLOW and POSITION totals are
unaffected — they are same-session readings, with no baseline to disagree about.

#### Alignment

Direction agreement across FLOW / POSITION / CHANGE / Change3 / Change5 / Change10 is reported
as evidence, not collapsed into a score: `ALIGNED_BULLISH`, `ALIGNED_BEARISH`, `MIXED`,
`NEUTRAL`, `INSUFFICIENT_DATA`, with the participating and excluded components named.

The state worth preserving is the one a single verdict destroys: **POSITION bearish, FLOW
bullish, CHANGE improving** — a short-covering shape. Compressing that to "bullish" or
"bearish" throws away the only thing the three semantics were separated to show.

### 10.5 Milestone status

Recorded here rather than in a report, because the distinction between "a reviewer approved
this" and "I checked it myself" is exactly the kind that decays into the stronger claim once it
is only in prose.

| milestone | implementation | automated reviewer | mutations |
|---|---|---|---|
| M1 spec | COMPLETE | APPROVED (round 8) | — |
| M2 persistence | COMPLETE | APPROVED (round 2) | 6/6 KILLED |
| M3 acquisition | COMPLETE | APPROVED after 3 blocking fixes | 14/14 KILLED |
| M4 institutional | COMPLETE | **APPROVED** (bounded final review, third attempt) | 10/10 + 3 independent, all BUILD OK + KILLED |
| M5 options structure | COMPLETE | **APPROVED** | 13 + 3 independent, all BUILD OK + KILLED |

**M4's review took three attempts.** The first two agents hit session limits before producing
any finding; the third ran a bounded scope and approved, having independently re-verified the
blocking gates from the code and re-run three mutations (the `candidateDates` SQL ordering,
absent POSITION → 0, and the percentile look-ahead guard). It explicitly recorded what it did
NOT check, which is why the approval is worth having.

Two things from that round are worth keeping:

- Before the bounded review, ten of the required mutations had been run manually. **Five of the
  first attempts were INVALID rather than the KILLED/SURVIVED nearly reported** — a comment, a
  constant name that did not exist, an accessor rather than the construction path. §10.6 exists
  because of that.
- Re-running the `candidateDates` mutation, the reviewer noticed that changing the two health
  queries' `ORDER BY` to `fetched_at` literally — as the instruction said — would have produced
  a **runtime SQL error**, because `derivative_data_health` has both a `fetched_at` and a
  `recorded_at` column and those queries bound on `recorded_at`. It used `recorded_at DESC`
  instead, on the grounds that a mutation which cannot execute is INVALID rather than KILLED.
  That is the procedure working: the instruction was followed in substance rather than in
  letter, and the difference was reported.

**A refinement from M5, worth stating as part of the procedure.** A mutation whose only build
error is a now-unused import is NOT `INVALID` — deleting the dead import is a compile
consequence, not a second semantic change, and the pair is still one production-path defect.

The case was `math.Abs(c) + math.Abs(p)` → `c + p` in the call/put contract-count summary
(§10.8 forbids a signed sum, which can report 0 for an institution holding 100 net calls and
100 net puts). Removing `math.Abs` orphans the `"math"` import. Rewriting it as `math.Abs(c+p)`
to keep the import compiling tests the CANCELLATION half but leaves the sign half untested;
deleting the import and using the true `c+p` tests both, and is what M5's review ran. Both kill,
so nothing was let through — but the weaker substitute was chosen for the wrong reason.

So: fix the mechanical consequence, keep the semantic mutation. Reach for a different mutation
only when the SEMANTICS cannot be expressed in compiling code.

### 10.6 Mutation procedure (from M5 onward)

M3 and M4 both produced mutations that were nearly misreported. The cause was always the same:
a blind string replacement whose exit code was read as a verdict. The procedure is therefore
fixed:

```
1  state the mutation ID and the production invariant it should break
2  apply it
3  confirm `git diff` is non-empty, and READ the diff
4  confirm the change is on the production decision path
5  reject comment-only, test-only, or unmatched replacements
6  go build ./...
7  build fails            → INVALID_MUTATION, design another
8  build OK + tests pass  → SURVIVED
9  build OK + tests fail  → KILLED
10 revert, and confirm nothing remains
```

`KILLED` requires all three of: the production path actually changed, the build succeeded, and a
relevant test failed. Anything else — an unmatched string, a comment, a test-only edit, a
nonexistent symbol, an accessor with no effect on the data flow, a syntax or type error — is
`INVALID_MUTATION`, and the answer is a different mutation representing the same defect, not a
verdict.

### 10.7 The two-layer ordering contract (established in M4)

`history.candidateDates` orders twice, and the layers are not redundant:

```
SQL ORDER BY   decides WHICH dates survive the LIMIT
Go sort.Reverse decides the final arrangement of what came back
```

Neither may be removed. **A Go re-sort cannot recover a date the SQL LIMIT already truncated
away**, so a mutation of the SQL ordering alone produces a correctly-sorted list of the WRONG
days — and is invisible whenever `MaxScanDates` exceeds the available history, which is the
shape of every small fixture.

A bounded-history test must therefore make the history LONGER than the scan limit.
`TestTheScanLimitKeepsTheMostRecentDates` is that test and must be preserved or replaced by an
equivalent.

### 10.8 M5 — the options structure contract

```
VolumePCR = Σ put TradingVolume / Σ call TradingVolume
OIPCR     = Σ put OpenInterest  / Σ call OpenInterest
```

Both are RATIOS in the domain — `1.05`, never `105`. A percentage is a presentation choice and
the ×100 happens once, at the boundary, exactly as `CurrentPercentile` already does.

Neither is ever called just "PCR". The type is part of the name: `VOLUME_PCR` and `OI_PCR` are
different measurements of different things — one is trading activity, the other is held
exposure — and §10.4's institutional table already shows how far apart those two can be.

Stored per result: numerator, denominator, ratio, observed state, session, contract family,
included and excluded contracts, trading date, as-of, snapshot id, revision, fetched-at,
coverage, status. **Never the ratio alone** — a ratio with no numerator cannot be checked, and a
1.05 built from 21/20 is not the same evidence as one built from 42,000/40,000.

#### The night session carries no open interest at all

Measured on the committed fixture (TXO, 2026-09-04):

| session | family | put vol | call vol | put OI | call OI | OI rows | OI absent |
|---|---|---|---|---|---|---|---|
| 一般 | MONTHLY | 829 | 609 | 3,141 | 3,380 | 80 | 0 |
| 一般 | WEEKLY | 40,700 | 45,261 | 14,007 | 8,000 | 64 | 0 |
| 盤後 | MONTHLY | 591 | 737 | — | — | 0 | 80 |
| 盤後 | WEEKLY | 20,894 | 20,309 | — | — | 0 | 46 |

Every 盤後 row carries `OpenInterest: "-"`. Not zero — **absent** (§3.1). So:

- **an AFTER_HOURS OI PCR does not exist.** Numerator and denominator are both absent, and the
  answer is `INSUFFICIENT_DATA`. Computing it yields 0/0; zero-filling yields a fabricated ratio
  from a session that reports no open interest. This is Gate 2 meeting Gate 6 on real data.
- **volume PCR needs both sessions.** 一般 alone gives 41,529 put against the 63,014 both
  sessions produce — a third of the activity missing.

The session policy is therefore per METRIC, fixed by the data contract and never by a clock:

```
OI PCR      DAY only          (AFTER_HOURS reports no open interest)
Volume PCR  DAY, AFTER_HOURS, and their sum
```

The combined volume figure is admissible because non-overlap is structural rather than assumed:
every row carries exactly one `TradingSession`, and M3 partitions on that field, so the two sets
are disjoint by construction. It is also the figure that reconciles with the official
`PutCallRatio` (§3.1) — the reconciliation is only possible over both sessions.

#### Zero, absent, and the denominator

```
call denominator observed > 0   → ratio available
call denominator observed = 0   → ZERO_DENOMINATOR, ratio absent
call denominator absent         → INSUFFICIENT_DATA, ratio absent
put numerator observed = 0, call > 0 → ratio is an OBSERVED ZERO
put numerator absent            → ratio absent
```

No `Inf`, no `NaN`, anywhere — not in the domain, not in JSON, not in SQLite. A ratio that
cannot be computed is absent with a status, never 0 and never neutral. Only one side present is
`PARTIAL` with the ratio absent; the missing side is never inferred from the present one.

#### PCR is a structure, not a direction

Open interest says how many calls and puts are open. It does **not** say who is long or short —
the same OI is consistent with buyers or writers dominating, and the feed carries no long/short
identity for it. So M5 describes:

```
PUT_OI_DOMINANT · BALANCED · CALL_OI_DOMINANT
```

and never `BULLISH` / `BEARISH`. "High put OI means put-selling support" and "high call OI means
call-writing resistance" are folk readings with a plausible opposite; if either is carried at
all it is `InterpretationType = HEURISTIC`, `ValidationStatus = SHADOW`, with the contrary
reading stated beside it. M5 produces no validated direction.

#### Contract families and rollover

WEEKLY / MONTHLY come from M3's `ClassifyExpiry` — the code-shape rule, already reviewed. **No
second classifier.** Every result records the family, the expiries and contract ids included,
days to expiry, and the selection policy.

A PCR change across a contract-identity change is not a change. Where the identity differs the
answer is absent with `ROLLOVER_BOUNDARY`, not a difference between two different things. OI
falling to zero at settlement is the contract ending, not the market collapsing — and M6's
emerging/collapsing walls will read this metadata, so getting it right here is a precondition
rather than a nicety.

#### Changes and percentiles

`PCRChangeN = CurrentPCR − BaselinePCR`, over N valid OBSERVATIONS (§10.4's rule, reused).
Differencing the numerators instead is not a PCR change. Absent when the sample is short, when a
denominator is zero, when a rollover intervenes, or when the scopes differ.

Percentiles reuse §10.4's policy verbatim — 60 observations, minimum 20, ratio in [0,1], ties
at-or-below — and every combination gets its OWN distribution: metric type × contract family ×
session × selection policy. Mixing volume with OI, weekly with monthly, or day with night makes
a percentile that ranks a figure against a population it does not belong to.

#### Institutional call/put

M4's definitions carry over unchanged. What must not appear is `call net + put net` presented as
directional exposure: calls and puts have different deltas, different strikes and different
sides, so their lot counts do not add into a market view. A combined figure may only be labelled
a **contract-count summary**. Delta-weighted exposure needs the delta feed and its own work item.

## 11. Non-institutional residual

Never "散戶". The name is `NON_INSTITUTIONAL_NET_POSITION`, and every rendering states that it
is a **residual**: it may contain other juridical persons, hedgers, market makers and
unclassified participants. Research proxy, shadow-only, no decision weight.

### 11.1 v1 computes the NET residual, and the denominator cancels

An earlier draft said the total came from `OpenInterestOfLargeTradersFutures` and that a missing
total made the residual MISSING. Both statements were wrong, and the algebra shows why:

```
residualLong  = T − Σ instLong
residualShort = T − Σ instShort
residualNet   = residualLong − residualShort = Σ instShort − Σ instLong
```

**T cancels.** Verified on 2026-09-04: institutional 臺股期貨 OI Long 90,765 / Short 97,677 →
residual net **6,912**, identical under either candidate total. So v1 needs no whole-market
figure at all, and the "MISSING if the total is missing" rule would have suppressed a number
that is fully computable.

### 11.2 Why a level or a share is NOT in v1

The moment the residual is expressed as a long/short **level** or a market **share**, T stops
cancelling — and T does not mean what it appears to:

```
OpenInterestOfLargeTradersFutures, TX, SettlementMonth 999912, TypeOfTraders 0
  OIOfMarket 117,552   ContractName "臺股期貨(TX+MTX/4)"     ← a COMPOSITE
DailyMarketReportFut, TX only                107,437
Institutional feed, ContractCode 臺股期貨    pure TX (小型/微型 are separate rows)
```

Mixing calibres gives `117,552 − 90,765 = 26,787` against the correct-calibre
`107,437 − 90,765 = 16,672` — a **61% error**, on an AVAILABLE status, with no official
cross-check on the futures side to catch it (there is no `PutCallRatio` equivalent). And the
composite cannot be inverted: TX + MTX/4 = 117,828.75 ≠ 117,552, so pure TX is not recoverable
from it.

So: **v1 reports the NET residual only.** A level or share requires an explicit calibre
decision (report everything as TX-equivalent, including the rounding rule for MTX/4) and is out
of scope until someone makes it.

### 11.3 If a total is ever needed

- **Options** are already calibre-aligned and verified: `SettlementMonth 999912,
  TypeOfTraders 0` gives TXO CALL 48,258 / PUT 48,162, exactly the official `PutCallRatio`, and
  the institutional options feed is the same TXO calibre.
- `TypeOfTraders` 0 and 1 carry the **same** `OIOfMarket`; taking both double-counts.
- `SettlementMonth 666666` is **not** a second sentinel for "near month" — it is the
  **weeklies aggregate**. Verified: TXO CALL 16,419 = W2 14,836 + F2 1,493 + F3 90 exactly, and
  PUT 14,547 = 13,237 + 1,135 + 175. That makes it a free cross-check on M6/M7's weekly split,
  and it must never be summed alongside `999912`.
- `OpenInterestOfLargeTradersFutures` serves its JSON with a **UTF-8 BOM** — the only one of the
  R15 endpoints that does. Go's `json.Unmarshal` rejects it outright
  (`invalid character 'ï' looking for beginning of value`), so this feed needs defensive
  decoding before it is usable at all. One more reason v1's residual does not depend on it.
- The feed also uses a **third contract namespace**: 681 of its 704 codes do not match
  `DailyMarketReportFut` (`CA`↔`CAF`, `CC`↔`CCF`, `CJ`↔`CJF`+`CJ1`). `derivative_contracts`
  must reconcile three namespaces — large-trader, daily-report, and the institutional feed's
  Chinese product names — not just the TXO/TXU pair §7.2 pins.

## 12. AI limits

The AI may organise structured evidence, explain conflicts between signals, describe what is
missing, and summarise risk scenarios.

It may not recompute an official figure, invent a missing one, describe FLOW as POSITION,
assert an OI zone as support, produce entry or exit points, change any stock decision, or claim
a strategy works without outcome evidence.

Enforcement is structural, following FU-11: the reply schema and the decoded struct share a
**default-deny allowlist**, so a new field of any type fails the tests until someone adds it
deliberately. A denylist of dangerous names is not acceptable — it can only enumerate what
someone already thought of, and every deterministic conclusion here is a word rather than a
number.

---

## 13. Validation gates (run at the end of every work item)

```bash
git diff --check
gofmt -l <R15 files>
go vet ./...
go test ./...
```

Plus the twelve invariants:

1. R15 disabled → existing output and decisions byte-identical
2. absent TAIFEX data → no panic
3. `MISSING` never renders as `NEUTRAL`
4. `STALE` never presented as current
5. no live HTTP during report generation
6. AI judge cannot consume R15 evidence to change a decision
7. BUY / WATCH / SELL unchanged
8. sorting unchanged
9. re-running produces no duplicate rows
10. point-in-time queries never read a future snapshot
11. a contract roll is not reported as an OI collapse
12. FLOW / POSITION / CHANGE labelled consistently everywhere

Each is a test, not a checklist item.

---

## 14. Rollout and rollback

**Rollout.** Ship disabled. Enable `enabled: true` with `show: false` first, so snapshots
accrue while nothing renders — the point-in-time archive needs history before any percentile
means anything, and `PutCallRatio`'s 23 sessions are the only head start available. Turn on
`show` once the data-health panel reports AVAILABLE for a full week.

**Rollback.** Set `enabled: false`. Nothing else is required, and that is now literally true
rather than approximately: R15 owns its own database file and its own migration sequence, so
`internal/store.SchemaVersion` never moves and a binary built without R15 opens `r13.db`
exactly as before (§7.1). No existing code path reads an R15 table.

Deleting `r15_derivatives.db` is optional and destroys research history; the archived snapshots
under `data/derivatives/` are kept regardless. They are observations, and deleting them to tidy
up would destroy the only record of what was knowable on those days.
