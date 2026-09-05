# R14 — Interactive Stock Health Dashboard

A standalone dashboard: search a stock by code, name or alias, pick it, run an on-demand
health check, and get an answer built entirely from evidence this repo already holds.

It is a **sidecar**. Nothing it produces reaches BUY / WATCH / SELL, the rocket score, the
ranking, the candidate filter or the market regime. The dependency arrow points one way and
is enforced structurally, not by convention.

---

## 1. Architecture

```
stockcatalog ─┐
              ├─► healthcheck.Service ──► Health ──► httpapi ──► dashboard.html
fetcher ──────┤        │
scanner ──────┤        ├─► valuation.ComputeTargetPrice   (pure)
fundamental ──┤        ├─► assess()                       (pure, deterministic)
valuation ────┤        ├─► Judge  ──► OpenAI              (prose only, fail-open)
institution ──┤        └─► History ──► internal/store     (SQLite, optional)
news ─────────┤
market/macro ─┘
```

| Package | Role | New? |
|---|---|---|
| `internal/stockcatalog` | canonical stock identity + search | new |
| `internal/healthcheck` | evidence, blocks, assessment, judge, history, outcomes | new |
| `internal/httpapi` | HTTP surface + the embedded page | new |
| `internal/valuation/target_price.go` | deterministic target price engine | new file, existing package |
| `cmd/health-dashboard` | the binary | new |

Changes to **shared** packages, in full:

- `internal/ai/client.go` — `Complete` split into a thin wrapper over a new `CompleteSchema`,
  so a second consumer can supply its own strict schema without a second HTTP client (and
  therefore without a second place that reads the key or sets the Authorization header).
  Signature and behaviour of `Complete` unchanged.
- `internal/fetcher/fetcher.go` — added exported `DefaultCacheDir`, used by the fetcher's own
  default and by the dashboard's cache guard. Purely additive.

`internal/scanner`, `internal/report`, `internal/store`, `internal/research`, `internal/fundamental`
and `cmd/scanner` are **untouched**.

---

## 2. Data flow

`fetch/snapshot → analysis → render` stays separated. The health endpoint fetches prices and
reads archives; the *page* is a static embedded asset that renders JSON. No HTTP fetch lives
in a rendering function, and `internal/report` is not involved at any point.

Within one request:

1. resolve identity from the catalog
2. build evidence (7 stages, ctx-checked between each)
3. project blocks — price, technical, fundamental, valuation, target, institution, news
4. data-quality panel
5. **assessment** (deterministic)
6. **judge** (prose only) — last, on a Health that is already final
7. **history** (optional) — last, and never fails the request

---

## 3. Stock catalog contract

There is no second stock list. The catalog is *composed* from files that already exist:

```
data/stock_names_zh.yaml   1,981 official names   (generated; the base)
configs/sectors.yaml       sector membership
configs/news_aliases.yaml  aliases already used by the news layer
configs/stock_catalog.yaml a thin overlay — 8 entries, NOT a stock list
```

`stocks.yaml` is deliberately **not** the source: it is gitignored, and `make merge-watchlist`
rebuilds its watchlist wholesale.

**Identity is always decided by catalog resolution.** `Resolve()` accepts a code, a canonical
symbol or an alias — **never a name**. Validation refuses an alias that is claimed by two
stocks, that equals another stock's code, or that equals another stock's official name. That
last rule exists because of a real defect found in review: `聯發` as an alias of 2454 shadowed
1459's actual name.

`Market` may be empty with `MarketSource = UNKNOWN`; the fetcher's `.TW → .TWO` detection
supplies it later and the result is labelled `RESOLVED`.

---

## 4. Point-in-time contract

`asOf` is threaded everywhere. `LoadLatest()` is never called.

| Source | Mode | Meaning |
|---|---|---|
| price / technical | `TRUNCATED` | the cached series is cut at asOf **in this layer** before any analyzer runs |
| fundamental | `AS_OF` | latest archive ≤ asOf, then each record filtered by its own `PublishedAt` |
| valuation | `AS_OF` | same |
| institution | `AS_OF` | `LoadHistory` bounded by asOf |
| news | `EXACT_DATE` | snapshots are named by observation date; no fallback to an earlier day |
| market / macro | `AS_OF` | latest archive ≤ asOf |

A future `asOf` is refused outright. `ObservationDate`, `ArchiveDate` and `PublishedAt` are
three distinct facts and are kept distinct.

---

## 5. AI contract

**The model produces no financial fact.** That is structural, not a matter of prompt wording:

- the reply schema (`strict: true`, `additionalProperties: false`) has **no numeric field at
  all** — `confidence` is an enum, not a score, so there is no slot a figure could arrive in;
- the AI block is merged **after** every number is final;
- no code path reads AI output back into a value.

On top of that, a **prose scrubber** rejects any reply containing a digit (including full-width
０-９), because a model can always write 「目標價 1,100 元」 inside a sentence. The instruction
tells it to write 月線 / 季線 rather than 20 日 so it can comply.

**Known limit, stated plainly:** Chinese numerals pass the scrubber. 「一千一百元」 contains no
digit. The scrubber is defence in depth, *not* the guarantee — the guarantee is the three
structural properties above, and prose that says 一千一百 is still prose: wrong, possibly
embarrassing, but not a number on this page.

The key comes from `OPENAI_API_KEY` only, is read inside the client at request time, and is
never stored on a struct. It is not a flag (flags reach shell history and `ps`), not in config,
and not in the page. The browser never calls a model; a `default-src 'none'` CSP with a
SHA-256 `script-src` states that to the browser too.

**Failure is never fatal.** Disabled, no key, timeout, 429, 5xx, 401, garbage body, valid
envelope with broken payload, truncated reply, dirty prose, empty prose — each returns a status
and a reason, and the rest of the page is served unchanged.

---

## 6. Valuation contract

Trailing P/E, P/B and dividend yield are the **exchange's own published figures**, carried
through unchanged.

The historical P/E distribution needs `MinHistoricalPESamples = 20` sessions. Below that it is
`INSUFFICIENT_DATA` **with the sample count** — `INSUFFICIENT_DATA（1 筆，需 20）` — never `0`,
`0x` or `0 percentile`. A loss-making company's absent P/E is a fact about the company, not a
zero, and sessions with no published P/E are dropped from the distribution rather than counted.

### Where the history comes from

**Historical P/E IS available from official TWSE/TPEx historical endpoints.** The earlier
limitation was PROVIDER IMPLEMENTATION COVERAGE, not source availability — an earlier version
of this document claimed the source did not exist, which was wrong.

|  | endpoint | shape |
|---|---|---|
| live | `openapi.twse.com.tw/v1/exchangeReport/BWIBBU_ALL`<br>`tpex.org.tw/openapi/v1/tpex_mainboard_peratio_analysis` | whole market, **current session only**, no parameters |
| historical | `twse.com.tw/rwd/zh/afterTrading/BWIBBU?date=&stockNo=` | **one stock, one month** of sessions |
| historical | `tpex.org.tw/.../peratio_analysis/pera_result.php?d=` | **one session, whole market** |

Both historical endpoints return JSON from the exchange, with no access control to work
around. `cmd/valuation-backfill` uses them; the default window is **12 months** (~240
sessions) — enough to span a full reporting cycle and several valuation expansions, where 20
sessions is only the minimum at which the statistics become computable at all. The 5-year
`DefaultWindowDays` remains a research window and is deliberately not the R14 default, so
different industry regimes are not blended into one distribution.

`MinHistoricalPESamples = 20` is unchanged by the backfill.

### Backfill provenance — three dates, never conflated

A backfilled record is not pretended to be a contemporaneous observation:

```
Ratios.Date        2026-08-03   the session the EXCHANGE computed the ratio for
Ratios.FetchedAt   2026-09-05   when THIS REPOSITORY actually retrieved it
Ratios.Backfilled  true         retrieved from a historical endpoint, not observed live
Ratios.Source      TWSE_HISTORICAL | TPEX_HISTORICAL
archive directory  2026-09-05   the day the BACKFILL RAN
```

Writing `ObservedAt: 2026-08-03` would claim this repo knew something it did not, and every
look-ahead guard downstream trusts that claim.

**The point-in-time contract is unchanged.** Backfilled sessions live in `Snapshot.History`
inside the archive for the day the backfill ran, so `LoadHistory`'s archive-date bound still
applies: a check dated before the backfill cannot see them. Making official historical data
visible to historical queries is a different feature with different semantics
(`OFFICIAL_HISTORICAL_REPLAY`), and `LoadView(asOf)` / `LoadValuation(asOf)` were deliberately
not relaxed here.

`CurrentPercentile` is a ratio in `[0,1]` in the domain; the ×100 happens **once**, at the
presentation boundary.

Forward P/E, PEG and fair value are `NOT_IMPLEMENTED`: no authorized source for Taiwanese
estimates exists.

---

## 7. Target price contract

```
Target Price = EPS × PE Multiple
```

**The EPS.** MOPS reports *cumulative* earnings, so this repo has no TTM EPS and annualising a
half-year figure is the guess a valuation layer must not make. The EPS used is the one implied
by the exchange's own trailing P/E:

```
impliedEPS = close(EPSBaseDate) / trailingPE
```

`EPSBaseDate` is the session the exchange **computed that P/E for**, which is several days
older than the analysis date because that is the publication lag. Dividing the *analysis-date*
close by an older P/E does not recover any earnings figure that ever existed — it returns the
true EPS scaled by whatever the price did in between. (This was a real defect, caught in
review: with it restored, a 2020 P/E paired with a 2026 close produced a **+47.5% margin of
safety** on a stock that should have had no target at all.) The current price still appears —
in the upside, where it belongs.

Two properties of that division, both easy to "tidy up" wrongly: the published P/E has two
decimals, so the recovered EPS carries roughly ±0.02% quantisation error; and the division uses
the **raw** close, not an adjusted one, because that is what the exchange used.

> **Same-session invariant.** `ImpliedTTMEPS = Close ÷ PE` is permitted **only** when
> `Close.Date == PE.Date`. A newer close divided by an older P/E is forbidden, and so is the
> reverse. `closeOn` matches the session exactly and never falls back to the nearest earlier
> bar; with no matching session there is no target. Backfill makes this easy to reintroduce —
> the archive now holds hundreds of P/E rows whose sessions have no bar in a truncated
> series — so all four cases are pinned in `samesession_test.go`, including that backfilled
> history may populate the DISTRIBUTION without fabricating a current EPS.

**The multiple.** Two rules, in order:

1. `HISTORICAL_PERCENTILE` — this stock's own P25 / median / P75, needing ≥ 20 samples
2. `CONFIGURED_RANGE` — an explicit human-supplied range that **must name its source**
3. otherwise `NONE`, and no target

> **Deviation from the original §13 priority list, recorded deliberately.** The spec listed
> "historical PE percentile → historical median PE → current valuation regime → configured
> range". The middle two are both derived from the *same* distribution, so they cannot be a
> fallback for that distribution being absent — that is arithmetic, not style. Supplying a
> market-average multiple there would be 「AI 覺得 25x 合理」 with a rule name attached. Do not
> "fix" the code to match the original list.

Every scenario row carries its own provenance — EPS, EPS basis, EPS source, multiple, rule,
rule detail, sample count, asOf — because a row is what a person screenshots, and a multiple
whose justification lives one level up travels as a bare number.

`MarginOfSafety` is the BASE case's `(target − price) / price`, **not** the textbook
`(fair − price) / fair`. It goes negative above the base case. `MarginOfSafetyBasis` says so on
the block.

---

## 8. Entry assessment semantics

Vocabulary: `ATTRACTIVE / ACCEPTABLE / WAIT / OVERVALUED / HIGH_RISK / INSUFFICIENT_DATA`.
Deliberately **not** BUY/WATCH/SELL: the scanner answers "what does this chart score out to";
this answers "is this a place to put money today". Sharing a vocabulary would invite a reader
to treat one as the other.

Not a scoring system — an ordered list of named rules, first match wins, no weights:

| # | Rule | Verdict |
|---|---|---|
| 1 | `NO_PRICE` | INSUFFICIENT_DATA |
| 2 | `HAZARD` — a named severe label, or limit-up distribution | HIGH_RISK |
| 3 | `EXPENSIVE` — percentile ≥ 80 **and** base upside < 0 | OVERVALUED |
| 4 | `NO_MARGIN` — no target price | WAIT |
| 5 | `VALUE_AND_SETUP` — upside ≥ 15%, scanner BUY, no caution | ATTRACTIVE |
| 6 | `VALUE_NO_SETUP` | WAIT |
| 7 | `NO_UPSIDE` | WAIT |
| 8 | `DEFAULT` | ACCEPTABLE |

Thresholds: `MarginAttractive = 15`, `MarginAcceptable = 0`, `PercentileExpensive = 80`.

> **NOT BACKTEST-FITTED.** These are a heuristic baseline: round numbers chosen by hand, never
> fitted to outcome data. They must stay **explicit, persisted, versioned, falsifiable and
> documented as unfitted**. Do not "optimise" them against the outcome data this feature is
> now accumulating — fitting a rule to the sample that will later be used to judge it destroys
> the only property that made it worth measuring.

They are persisted with every assessment so a later reader can tell whether a call aged badly
because the evidence moved or because the rule did.

**Rule-set versioning — open, and required before the thresholds ever change.** The AI side has
`PromptSetVersion = "r14-judge-v1"`; the assessment has no equivalent. Assessments written today
therefore carry their thresholds but not a rule-set identity, so a future change to the rule
ORDER or to a condition (as opposed to a threshold value) would leave historical verdicts
silently incomparable. Adding `AssessmentVersion = "R14-v1"` and persisting it is tracked in
[FOLLOWUP_R14_POST_REVIEW.md](FOLLOWUP_R14_POST_REVIEW.md); it is deliberately **not** part of
R14 itself.

`Rules[]` is the ordered **evaluation trace** up to the terminal decision: rules evaluated and
not fired are present with `Fired=false`; rules after the terminal one were never reached.

**Hazards are named, never inferred from "non-empty".** `internal/scanner` writes `"—"` for
*no* risk, and `LOCKED_LIMIT_UP_LOW_VOLUME` is documented there as 中性偏多 and excluded from
its own negative set. Reading either as a hazard is not a reading of the evidence — it
contradicts it. The remaining labels (`族群轉弱 / 假突破 / 量不足 / 上影/收弱`) are **cautions**:
they block ATTRACTIVE without pretending the stock is untouchable.

---

## 9. Failure semantics

`MISSING ≠ ZERO`, enforced by the `Value` type: every metric carries a status and a
**pre-rendered `Display`** that is never `0` or `-` for a missing figure. A renderer that wants
to print must go through `Display`.

| Status | Meaning |
|---|---|
| `AVAILABLE` | present and usable |
| `PARTIAL` | a real number that is tainted — e.g. a 5-day institutional sum spanning a missing session. Shown **with** the figure and a caveat marker; `Float()` refuses it so no rule treats it as clean |
| `INSUFFICIENT_DATA` | computable in principle; the evidence has not accrued. **Wait** |
| `UNAVAILABLE` | the source was consulted and had nothing |
| `DISABLED` | switched off — a decision, not a data fact |
| `NOT_APPLICABLE` | cannot apply to this security |
| `NOT_IMPLEMENTED` | no authorized source exists at all. **Waiting will never help** |

`INSUFFICIENT_DATA` and `NOT_IMPLEMENTED` are not interchangeable: TTM EPS is the first,
forward EPS is the second.

**Presentation sentinels must not leak into data, prompts or decisions.** The scanner's `"—"`
was mistaken for content by four independent `!= ""` tests — the assessment, the evidence
table, the model's prompt, and the page (where JS truthiness put 「風險：—」 in front of the
user). One `hasRisk()` decides it, and `technicalBlock` drops the sentinel at the API boundary
so it never leaves the package.

---

## 10. SQLite integration

Reuses the R13 chain with **no new tables and no migration**:

```
scan_runs → stock_snapshots → evidence
                            → analysis_runs → agent_analysis
                            → outcomes
```

Marks: `source_tab = "health_check"`, `scanner_version = "r14-health-dashboard"`, plus a
descriptive `note`. Disabled by default; `-history` without an explicit path **refuses to
start** rather than defaulting into the live R13 database.

**No `decisions` row is ever written.** That table is the population R13-M7 scores SCANNER
against AI_JUDGE, and a health check is neither. The assessment is stored as evidence instead.

### Constraints this imposes on neighbours

- **`scan_runs` has no `source_tab` column.** Every check opens its own run, so
  `RunsByDate` returns them alongside real scans and cannot be filtered the way snapshots can.
  Filter on `scanner_version` or `note`.
- **`OutcomeRepo.PendingHorizon` does not filter `source_tab`.** Correct for outcomes; wrong
  for any scanner-accuracy query, which must exclude `source_tab='health_check'`.
- **One symbol-day can hold several snapshots.** Three checks are three judgements and three
  rows. Any aggregate must dedupe.

### ⚠ Pre-existing contamination, not introduced here

`internal/research/evidence.go` writes the same `"—"` sentinel unconditionally. The live
`data/research/r13.db` already holds **7,415** rows of `risk_label = "—"` — more than every
real label combined (`上影/收弱` 6,586 · `追高` 1,820 · `跌破支撐` 51). Any aggregate over
`category='risk'` is already dominated by a placeholder. R14 does not write these and does not
fix them; a decision is owed before that column is used for research.

---

## 11. Outcome validation

First consumer of the `outcomes` table — R13 built it and nothing ever wrote to it.

- The outcome row is opened at **save** time with the check's own close as the basis, because
  the basis is a property of the judgement (it is what the reader saw), and because
  `PendingHorizon` joins `outcomes` so a snapshot without a row is invisible forever.
- **Only `health_check` rows are graded.** Filling the scanner's would be this sidecar writing
  outcomes for judgements it did not make.
- **One call, one vote.** The canonical row per symbol-day is the last check *that recorded a
  price*, decided from the **stored table** rather than from the pending page — deciding it
  from the pending set is right on the first pass and wrong on the second, once the winner has
  filled every horizon and stops being pending.
- **No unclosed session.** `defaultAsOf` is the last closed session (13:30 + 30min), and
  `forwardBars` excludes today's bar while the session runs whatever `as_of` says. The columns
  are write-once, so an intraday price would be permanently wrong with nothing to mark it.
- **`max_drawdown_20d = close-based adverse excursion.** This is the column's permanent
  definition, not an implementation detail. It is a **lower bound** on true intraday MAE: an
  intraday spike through a stop that recovers by the close is invisible to it.

  > **It must not be used to derive a stop-loss hit rate.** Any such study built on this column
  > will systematically understate how often a stop would have been triggered.

  If reliable OHLC becomes available, add a **separate** metric — `intraday_mae_20d` — rather
  than redefining this one. The columns are write-once, so overwriting is not merely
  discouraged: it would silently mix two definitions in one column with no way to tell which
  rows carry which.

---

## 12. Testing strategy

### The production-wiring rule

> **For every production safety guard or dependency wiring, removing or bypassing the
> PRODUCTION wiring must cause a behavioural test failure. Testing the underlying internal
> component alone is insufficient.**

This was learned twice in this feature, both times from a passing test suite:

1. **Cache isolation guard.** `cacheisolation_test.go` proved that `internal/fetcher` does not
   write across two directories *a test hands it*. Deleting the entire `sameDir` hard stop from
   `main()` left the whole suite green — the step that CHOOSES those directories had no test at
   all. Fixed by extracting `resolveCacheDir`, which is what `main()` calls; removing its
   rejection branch now fails.
2. **`Catalog` → market wiring.** `marketOf()` was correct and tested. The grader was
   constructed in `main()` without `Catalog`, so it returned `""` forever and every TPEx stock
   paid a redundant probe — compiling, running, silently inert, and reported by me as working.
   Fixed by extracting `newGrader`; omitting the field now fails.

The shape is the same both times: a correct helper, a tested helper, and nothing testing
whether production actually calls it.

### Mutation verification

Every guard here was verified by breaking it. A mutation result is one of three things, and
they must not be confused:

| Result | Meaning |
|---|---|
| **KILLED** | a named test failed. The guard is real. |
| **SURVIVED** | the suite stayed green. The guard is **not tested** — this is a finding, not a pass. |
| **INVALID MUTATION** | the edit did not compile (or did not apply). It proves *nothing* and must be redone. |

The third is easy to misread as the first. Deleting the running-session guard left two variables
unused, so the run failed to build — and `FAIL … [build failed]` looked like a killed mutation
through a narrow grep, while a differently-filtered view would have looked like SURVIVED.
Keep the mutation compiling (e.g. `&& false` rather than deleting the block) and check the build
explicitly before reading the test result.

- **Mutation-driven.** Every guard in this feature was verified by breaking it and confirming a
  named test dies.
- **Production shapes, not convenient ones.** The `"—"` bug survived a full suite because every
  fixture used `""`, a value production never emits.
- **Wiring is tested, not just helpers.** `resolveCacheDir` and `newGrader` exist as functions
  because a field left unset in `main()` compiles, runs and is silently inert.
- **The page is executed, not grepped.** `dashboard_script_test.go` runs the real script in
  `jsc`/`node` against a DOM shim and asserts on the tree. Grepping the source proved only that
  a branch existed; executing it found a PARTIAL value rendered as absent and an `undefined`
  reaching the page.
- **No test calls OpenAI or Yahoo.** The AI tests use `httptest` and a dummy key; the cache
  tests seed archives and use a long TTL so the fetcher serves from disk.

## 13. Non-goals

- Changing anything a scan decides. R14 never writes a scanner artefact and is never read by one.
- A second stock list, a second analysis database, or a second definition of "return".
- Forward earnings, analyst consensus, PEG or a scenario fair value — no authorized source.
- Standalone quarterly EPS for Q2–Q4 — recoverable only by subtracting a prior filing, which
  this build does not read. (Q1 is reported because its cumulative window *is* the quarter — an
  identity, not a derivation.)
- Intraday. Every archive here is end-of-day.
