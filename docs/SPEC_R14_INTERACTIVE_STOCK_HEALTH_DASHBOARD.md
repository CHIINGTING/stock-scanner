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

### Availability vs quality (FU-9)

These are **two axes, and the second never overrides the first.**

```
AVAILABLE + HIGH        AVAILABLE + MEDIUM        AVAILABLE + LOW        INSUFFICIENT_DATA
```

Availability answers one deterministic question — are there at least `MinHistoricalPESamples`
usable observations — and it alone gates the target price. Quality answers a second one: how
much history is behind those observations. It is reported beside the statistics, and **nothing
in the quality layer can make a computable distribution uncomputable.** A `LOW` grade is a
caveat printed next to a number, never the removal of the number.

The floor stays at 20. FU-9 did not move it and must not be used as an argument to.

**Coverage semantics — `N/A` is not a missing session.**

```
coverage_ratio = valid_samples ÷ window_sessions
```

`window_sessions` is every session ARCHIVED in the window, whether or not the exchange
published a P/E for it. A session with no ratio is an **acquired** session: the company had no
positive earnings, which is a fact about the company and not a gap in this repo's data. The
denominator is never `valid_samples`, which would make coverage 100% for every stock by
construction — including one with four usable observations.

```
2330 台積電   225 valid / 225 archived   coverage 100%   valid span 339 days   HIGH
2337 旺宏      26 valid / 225 archived   coverage  12%   valid span  36 days   LOW
3055 蔚華科     0 valid / 225 archived   coverage   0%                          INSUFFICIENT
```

Both of the first two are `AVAILABLE` and both produce a target price. The third has no
distribution at all, and is `INSUFFICIENT` rather than `LOW` — `LOW` says "here is a number,
trust it less", and there is no number.

**Span, because a count cannot see concentration.** 26 samples spread across a year and 26
clustered in one recent month are the same count and different evidence. Reported per stock:
`valid_samples`, `window_sessions`, `coverage_ratio`, `oldest_valid_session`,
`newest_valid_session`, `sample_span_sessions`, `sample_span_days`.

**Rule version.** Every grade carries `valuation_quality_rule_version` (currently `FU9-v1`) and
is persisted with it. Thresholds will change; a stored `LOW` with no version cannot be read
afterwards, because nobody can tell whether the stock changed or the rule did.

**Thresholds are HEURISTIC and NOT BACKTEST-FITTED.** They are derived from the arithmetic of
the trading calendar — ~21 sessions a month, so a quarter ≈ 60 sessions ≈ 90 days and half a
year ≈ 120 sessions ≈ 180 days — and from the fact that a trailing P/E's denominator only moves
when earnings are reported, i.e. quarterly. A distribution spanning less than one reporting
cycle has seen the denominator change zero times. Nothing here was tuned against outcomes,
because no outcome data exists that could tune it.

### Data quality is not model suitability

**FU-9 does not determine whether P/E is the appropriate valuation model for a company.**

3037 欣興 has 225 of 225 sessions and a median around 120x. The evidence could not be better,
and the multiple may still be the wrong lens for a cyclical substrate maker at the top of a
cycle. That is a question about the MODEL, and answering it needs sector knowledge this layer
does not have.

For the same reason **dispersion is reported but does not feed the grade.** `IQR` and
`RelativeIQR` are shown — 6274 台燿's relative IQR is 91% — but a wide range usually means the
market re-rated the company, a real event correctly recorded, not that the observations are
poor. Letting width lower a data-quality grade would file "this stock re-rated" and "our
archive is thin" under the same warning.

### Model suitability (FU-11) — the fourth layer

FU-9 ended by naming this as a separate question. It now has an answer, and the four layers
are permanent and must not collapse into one another:

```
Availability       can the historical P/E distribution be computed?
Data Quality       is the evidence behind it sufficient?              (FU-9)
Dispersion         how wide is the distribution?                      (FU-9, descriptive)
Model Suitability  is P/E the right lens for this company at all?      (FU-11)
```

The first three describe the DATA. The fourth describes the COMPANY, and the two can point in
opposite directions. Every combination below is reachable, and tests prove it:

```
Quality HIGH         + SUITABLE
Quality HIGH         + WEAK               ← the one that proves the layers are separate
Quality LOW          + CONDITIONAL
Quality INSUFFICIENT + UNSUITABLE
Quality INSUFFICIENT + INSUFFICIENT_DATA
```

**Suitability consumes FU-9 metrics as evidence and is never derived from the FU-9 grade.**
There is no mapping table, in either direction.

**Vocabulary:** `SUITABLE` / `CONDITIONAL` / `WEAK` / `UNSUITABLE` / `INSUFFICIENT_DATA`,
rule version `FU11-v1`, persisted with every verdict.

```
UNSUITABLE  ≠  bad company        UNSUITABLE  ≠  overpriced
UNSUITABLE  ≠  HIGH_RISK          UNSUITABLE  ≠  SELL
```

It says one thing: under current evidence, a P/E multiple is not the right primary anchor.

#### Earnings denominator semantics

`PE = Price / Earnings`, so the question is whether the DENOMINATOR has enough economic
stability to anchor anything. Two deterministic inputs, both from timestamped archives:

| input | source | values |
|---|---|---|
| filed earnings sign | MOPS 綜合損益表 `CumulativeEPS` + `NetIncome`, which must AGREE | POSITIVE / NON_POSITIVE / UNKNOWN |
| P/E availability shape | the nil/non-nil pattern across archived sessions | PERSISTENT / RECENT_ONLY / INTERMITTENT / NEVER / UNKNOWN |

The second input is the important one and it is **structural, not thresholded**.
`RECENT_ONLY` is an ORDERING, not a recency window: every session WITHOUT a P/E precedes every
session with one, i.e. a single loss-to-profit transition. `N N N P P P` is RECENT_ONLY;
`N N P P N P` is INTERMITTENT, and so is `P P P N N N`.

##### What that signal is, precisely

The exchanges do not compute a P/E when the reference EPS is zero or negative. So the pattern
is a **positive-earnings eligibility history** — a trailing-earnings **sign-state proxy**.

It is **NOT an earnings-magnitude history**, and a PERSISTENT pattern does **not** show that
earnings are stable:

```
TTM EPS   20 → 10 → 5 → 1     stays PERSISTENT throughout
```

A denominator collapsing by 95% is invisible here, because it never crossed zero. Nothing in
this layer may be described as showing stable, healthy or improving earnings. The phrase
"stable earnings denominator" must not appear.

##### An acquisition gap is not a financial fact

The pattern may use a P/E absence only where the session observation actually exists. A stock
absent from a whole-market response is archived with `Ratios` **nil** — no row — so an
unfetched stretch SHORTENS the series rather than punching a hole in it:

```
P P [archive hole] P P   →   PERSISTENT, never INTERMITTENT
```

Missing acquisition data is a data-completeness problem, not evidence about earnings. The cost
is that PERSISTENT means "no ARCHIVED session lacked a P/E", not "no session lacked one" — the
same archive-completeness caveat FU-9 carries, and it cannot be closed without a trading
calendar.

**The corollary, which is where the real bug was.** If an absent P/E is a financial fact, then
every path that can produce one must be able to justify it. An unresolvable P/E COLUMN cannot:
`fieldIndex` returns −1 for an unmatched header, `pick` turns that into `""`, and `ratio("")`
returns nil — so a renamed header used to archive "this company had no positive earnings" for
every row it touched. One bad backfill month inside a good series read as
`P P [renamed month] P P` → INTERMITTENT → "the denominator crossed zero", and on the live
whole-market endpoint it would have done so for the entire market at once.

`TWSEMonth` and `TPEXSession` now FAIL the response when the P/E column cannot be resolved, and
`parseRatioRows` skips a row whose P/E KEY is absent. The distinction is:

| | meaning | handling |
|---|---|---|
| the P/E **key/column** is missing | we cannot see the column — a parse problem | refuse / skip |
| the P/E **value** is `-` or empty | the exchange published no ratio — the financial fact | archive as nil |

Losing a month to an error a human will see is recoverable. Writing a false earnings history
into the archive is not, because nothing downstream can afterwards tell which nils were real.

The TPEx code column is guarded the same way, against a different false fact: an unmatched
`股票代號` header empties every row's code, every row is skipped, the caller receives an empty
map — and reads it as a **non-trading day**. A renamed column would report the whole backfill
window as holidays, with no error and no rows.

The general rule this layer now follows: **a parse failure must never be reported in the
vocabulary of an observation.** Not as "no earnings", not as "market closed".

The wording guard on rendered reasons is enforced the same way — driven by the reason-code
table (`AllSuitabilityReasons`) rather than by sampled inputs, in both directions: every
declared code must have wording that claims no stability, and every code the classifier can
actually produce must be declared. A sampled version of that test covered 11 of 14 notes, and
two of the three it missed are live on real stocks.

**Known limit of that guard.** Its coverage over CODES is now exhaustive; its coverage over
VOCABULARY is not, and cannot be — `stabilityClaims` is a hand-written denylist, so a phrasing
outside it (「盈餘表現穩定」 rather than 「盈餘穩定」) passes. The guard catches the specific
regression it was built for and raises the cost of the general one; it is not a proof. Anyone
rewording a note is still responsible for the claim it makes.

Guarded columns are likewise `code` and `pe` only. `pb` and `yield` still fall through to nil
on a renamed header, which is correct under the rule above: a nil P/B is displayed as an honest
absent value and feeds no judgement, whereas a nil P/E and an empty row set are both
INTERPRETED. If a future layer ever gives P/B a meaning, its column needs the same guard on the
same day.

##### The observation floor

`MinSuitabilityWindowSessions = 60` — approximately one reporting cycle (~21 sessions a month).

It proves only that the window is **long enough to potentially span** one reporting cycle. It
does **not** prove an earnings update occurred inside it; the filing dates that would establish
that are not part of this input. The weaker claim is what the rules rely on.

It applies to WINDOW SESSIONS, a different quantity from FU-9's `QualityMediumMinSamples`,
which counts VALID SAMPLES.

It also gates the `NEVER` conclusion: fewer than 60 archived sessions with no published P/E is
`INSUFFICIENT_DATA` with `INSUFFICIENT_OBSERVATION_WINDOW`, never UNSUITABLE. Three sessions of
silence is not a year of it. **The filed earnings evidence does not bypass this floor** — that
would be a different rule, concluding from a statement rather than from a pattern, and folding
it into `NEVER` would leave the stored verdict unable to say which evidence produced it. If it
is ever wanted, it gets its own name and its own reason code.

**HEURISTIC / NOT BACKTEST-FITTED.** There is very little here that could be fitted.

##### What SUITABLE actually claims

Narrowly: **P/E is structurally usable under the observed positive-earnings eligibility
history.** Nothing more.

Every SUITABLE verdict carries `EARNINGS_MAGNITUDE_NOT_ESTABLISHED`, unconditionally, so the
limit travels with the conclusion instead of living in a document. FU11-v1 does **not**
establish earnings-magnitude stability, and cannot: that needs multi-period earnings, which
needs a fundamental archive holding more than one filed period per stock. Future work, not a
threshold change.

#### Three things that must never decide a verdict

| | real case | contract |
|---|---|---|
| a high P/E | 3037 欣興, median ≈ 120x on 225/225 | a level is a property of the NUMERATOR. `SuitabilityInput` carries no P/E level at all. |
| a wide distribution | 6274 台燿, relative IQR ≈ 0.91 | a re-rating the archive captured correctly. Attaches `VALUATION_REGIME_WIDE`, nothing more. |
| an extreme target upside | 1815 富喬, base case > +100% | what a low current multiple against the stock's own distribution arithmetically produces. Attaches `TARGET_SENSITIVITY_REVIEW`. |

The last two are CAUTION reasons, appended after the class is decided and structurally
incapable of changing it. Reasoning from a result back to the validity of the model that
produced it is reasoning backwards, and it would contradict FU-9, which kept dispersion out
of the quality grade for the same reason.

#### An absent P/E is not proof of a loss

`UNSUITABLE` with `NO_POSITIVE_TRAILING_EARNINGS` requires **two independent sources agreeing**:
no P/E anywhere in the window AND a filing showing the earnings basis is not positive.

Silence alone is consistent with a suspension, a listing change, or a dataset gap. 3055 蔚華科
qualifies because its 2026Q2 filing reports EPS −0.03 on net income −7,061,000; the same
archive with no filing is `INSUFFICIENT_DATA`.

`NEVER` additionally requires at least `MinSuitabilityWindowSessions` archived sessions — see
the observation floor above.

**And a non-observation is not silence.** `NEVER` means this repo watched N sessions and the
exchange published no ratio on any of them. An EMPTY archive is `UNKNOWN`, classified
`INSUFFICIENT_DATA` with `NO_ARCHIVED_SESSIONS`, and may never carry
`PE_NEVER_PUBLISHED_IN_WINDOW` — that reason asserts something about an exchange, and an
exchange that was never queried has not said anything. This is the same error as reading
silence as a loss, arrived at from the other side, and it is reachable: 3037 欣興 held a
valuation archive and no fundamental one for the whole of FU-11.

#### The AI decides no verdict, structurally

Every deterministic conclusion this repo computes is now a WORD rather than a number — entry
assessment, data quality, model suitability — so "the reply schema has no numeric field" has
stopped being the same claim as "the model decides nothing". A string field named
`suitability` is neither numeric nor on any denylist.

The reply's field set is therefore an ALLOWLIST — **default-deny** — enforced over both the
schema and the decoded struct, plus an end-to-end test in which the model actively attempts to
set every verdict and changes none of them. Any future field, of any type, fails until it is
explicitly added to the interpretation-only set. Adding one is a decision someone has to make
deliberately, which is the moment to ask whether the model should be able to send it at all.

These classes may never become AI-authoritative: assessment, verdict, quality, suitability, any
deterministic status, EPS, P/E, target price, margin of safety, or any deterministic rule
output. **Do not revert to a denylist of dangerous field names** — that is the design that
failed, because it could only enumerate the fields someone had already thought of.

The evidence table's exception for classification keys is justified the same way: each such key
declares its CLOSED VOCABULARY in the test, and the stored value must be a member of it. A
figure key has no vocabulary to declare and so cannot be admitted; numeric evidence keys keep
their protection against storing `INSUFFICIENT_DATA` as though it were a measurement.

#### Target price independence, and same-session

`git diff internal/valuation/target_price.go` is empty and `SuitabilityInput` carries nothing
the target engine reads. `Target Price: AVAILABLE` beside `Suitability: WEAK` is the expected
pairing, not a contradiction — the system can compute it, and it should not be treated as a
high-confidence long-run anchor.

The same-session invariant is untouched: `ImpliedTTMEPS = close(EPSBaseDate) / trailingPE`
only where `Close.Date == PE.Date`, `closeOn` still an exact match with no nearest-earlier
fallback.

#### Non-goals

No P/B target, EV/EBITDA, DCF, normalized EPS, analyst consensus, sector-specific model
selection, or automatic model switching. FU-11 says which lens is appropriate; it does not
provide an alternative one. The AI may explain a verdict and may not change it, recompute it,
propose a threshold, or suggest another valuation model.

`FU-12 — Alternative Valuation Models / Model Selection` is the follow-up.

#### Coverage is not archive completeness

FU-9's coverage is `valid P/E sessions ÷ ARCHIVED sessions`. It is **not**
`archived sessions ÷ expected market sessions`. A stock with 100 archived sessions, all
carrying a P/E, reports 100% coverage even if the window should have held 225 — the missing
125 were never fetched, and nothing in FU-9 or FU-11 can see that.

FU-11 does not implement a trading calendar. FU-8's daily pipeline is the place a real
market-session expectation would come from; integrating it is future work, and a second
calendar must not be built for this.

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
