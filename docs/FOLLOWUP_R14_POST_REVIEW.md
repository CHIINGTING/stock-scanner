# Follow-up work items from the R14 review

R14 is COMPLETE and ACCEPTED. Nothing here is part of it, and none of it may be folded back
into R14 — these are separate work items, recorded so they are not lost.

R14 keeps **zero diff to `internal/research`**.

---

## FU-1 — `risk_label = "—"` in the research evidence table

**Severity:** data quality, affects any research built on `category='risk'`.
**Owner package:** `internal/research` (NOT `internal/healthcheck`).

### What is wrong

`internal/scanner`'s `rocketRisk()` returns the full-width dash `"—"` — not `""` — when a stock
has **no** risk label. `internal/research/evidence.go:189-190` persists it unconditionally:

```go
c.text(store.CategoryRisk, "risk_label",   e.RiskLabel,   srcRocket)
c.text(store.CategoryRisk, "risk_warning", e.RiskWarning, srcRocket)
```

Its guard is `v != ""`, which the sentinel passes.

### Current extent, measured

`data/research/r13.db`, `evidence` table, `key='risk_label'`:

| value | rows |
|---|---|
| `—` (no risk) | **7,415** |
| `上影/收弱` | 6,586 |
| `追高` | 1,820 |
| `量不足` | 331 |
| `族群轉弱` | 178 |
| `跌破支撐` | 51 |

The placeholder outnumbers every real label combined. `risk_warning` carries the matching
`依計畫設好停損即可` rows. Any distribution over this key is currently dominated by "no risk"
recorded as though it were a risk.

### Both halves are required

**A. Writer fix.** No-risk evidence must be **absent**, not persisted as a sentinel. `internal/healthcheck`
already has the shape to copy:

```go
func hasRisk(label string) bool { return label != "" && label != RiskNone }
```

Ideally `internal/scanner` exports the sentinel (it is currently a bare literal in the
unexported `rocketRisk`), so both consumers reference one constant and a rename becomes a
compile error. `internal/healthcheck` pins it today with a contract test
(`TestRiskNoneMatchesWhatTheScannerEmits`) precisely because it cannot import it.

**B. Historical cleanup.** The 7,415 existing rows need an explicit strategy — delete, or leave
and mandate a filter at every read site. Decide and record which.

> **Do not do B without A.** Cleaning the table while the writer still emits the sentinel just
> refills it on the next scan, and the next reader has no way to know the cleanup happened.

### Acceptance

- a regression test: a stock with no risk label persists **no** `risk_label` row
- a mutation restoring `!= ""` must be **KILLED** by that test
- the cleanup strategy is written down, including what happens to rows already read by anything

---

## FU-2 — Assessment rule-set version

**Severity:** comparability of stored verdicts over time.
**Owner package:** `internal/healthcheck`.

The three thresholds are persisted with every assessment (`assessment/threshold_*`), so a
threshold change is already detectable. What is **not** captured is the identity of the rule set
itself — the ordering, the conditions, the hazard membership. A change to rule order or to a
condition would leave historical verdicts silently incomparable.

The AI side already has `PromptSetVersion = "r14-judge-v1"`. The assessment has no equivalent.

### Required before the rules or thresholds ever change

- add `AssessmentVersion = "R14-v1"` and persist it as evidence alongside the verdict
- bump it for any change to rule order, a rule condition, the hazard/caution sets, or a threshold
- a test asserting the version is persisted

### Standing constraint on the thresholds

`MarginAttractive = 15`, `MarginAcceptable = 0`, `PercentileExpensive = 80` are a **heuristic
baseline and NOT BACKTEST-FITTED**. They must remain explicit, persisted, versioned, falsifiable
and documented as unfitted.

> Do **not** fit or "optimise" them against the outcome data R14 is now accumulating. Fitting a
> rule to the sample later used to judge it removes the only property that made measuring it
> worthwhile.

---

## FU-3 — Intraday MAE, if reliable OHLC ever exists

**Severity:** none today; a constraint on future work.
**Owner package:** `internal/healthcheck` + `internal/store`.

`max_drawdown_20d` is permanently defined as a **close-based adverse excursion**. It is a lower
bound on true intraday MAE, and **must not be used to derive a stop-loss hit rate** — any such
study will systematically understate how often a stop would have been triggered.

If reliable OHLC becomes available, add a **separate** column — `intraday_mae_20d` — and leave
`max_drawdown_20d` alone.

> Never redefine the existing column. `outcomes` is write-once, so a redefinition would mix two
> meanings in one column with no way to tell which rows carry which.

---

## FU-4 — Apply the production-wiring rule to existing guards

**Severity:** methodology.

> For every production safety guard or dependency wiring, removing or bypassing the **production
> wiring** must cause a behavioural test failure. Testing the underlying internal component alone
> is insufficient.

R14 hit this twice (cache isolation, and `Catalog` → market), both times with a fully green suite
and a correct, tested helper that production did not actually call. The rule and both worked
examples are in
[SPEC_R14_INTERACTIVE_STOCK_HEALTH_DASHBOARD.md](SPEC_R14_INTERACTIVE_STOCK_HEALTH_DASHBOARD.md)
§12, together with the mutation taxonomy — **KILLED / SURVIVED / INVALID MUTATION** — and why an
invalid mutation (one that fails to compile) proves nothing and is easy to misread as a kill.

Worth auditing other safety-critical wiring in this repo the same way, starting with anything
that decides a path, a directory or a data source at start-up.

---

## FU-5 — Historical valuation backfill ✅ DONE

**Severity:** was blocking the target price on 6 of 14 stocks.
**Closed 2026-09-06.** All 14 stocks now hold a full 12-month window.

`cmd/valuation-backfill -months 12` — every stock in the universe, 225 sessions
(2025-10-01 → 2026-09-04):

| stocks | sessions | valid P/E | result |
|---|---|---|---|
| 2330 · 2327 · 6488 · 3026 · 6274 · 1815 · 6789 · 1560 · 3094 · 1503 · 2449 · 3037 | 225 | 225 | distribution ready |
| 2337 旺宏 | 225 | 26 | `AVAILABLE` on the 20-sample rule, but see FU-9 |
| 3055 蔚華科 | 225 | 0 | `INSUFFICIENT_DATA` — **correct**, see below |

### 3055 蔚華科 — 225 sessions, zero P/E

Every one of the 225 sessions was acquired; the exchange published no P/E for any of them,
because the company was not profitable over the trailing window. That is a complete record of
the truth, and `INSUFFICIENT_DATA` is the honest answer.

`N/A ≠ 0`. There is no substitute multiple to fill it with — a P/B or an EV/EBITDA target
presented in a P/E field would be a different number wearing the same label. The block stays
unavailable until the company has trailing earnings.

### The rate limit

TWSE's CDN (HiNetCDN) answers a burst with **HTTP 307** and the block persists for a
cooldown measured in tens of minutes. It began at roughly the 66th request; the send order
matched the failure order exactly.

Fixed in `internal/valuation/historical.go` and `cmd/valuation-backfill`:

- 307 / 429 / 503 recognised as a rate limit (`IsRateLimited`), with a **30s base backoff**
  doubling per retry rather than the 1s used for ordinary errors
- default throttle 900ms → **1500ms**, retries 3 → 4
- **resume**: a re-run loads today's archive first and skips work already covered, so the
  realistic several-pass completion of a large backfill actually makes progress
  (`-refetch` forces a full re-fetch)

### Resume covers TPEx too

TWSE is fetched one stock × one month; TPEx is one request per **session**, returning the
whole market. The month-level resume therefore did nothing for TPEx — a re-run re-fetched all
243 dates even when every target already held every session.

`allHaveSession` now gates each TPEx date: if all still-needed targets already hold that
session, the request is skipped; if even one is missing, the whole-market page is fetched once
and only the needed symbols merged.

The gate tests **session existence, not P/E availability**, and that distinction is the whole
point. 3055 has 225 sessions and not one usable P/E; a P/E-availability gate would re-fetch
its entire window on every run, forever. An archived `N/A` row is an acquired row.

### Incident: the daily fetch was erasing the backfill

Found and fixed during this work, and worth recording because it coupled FU-5 to FU-8.

`FetchAndStore` builds a `Snapshot` from the one session it just observed, with `History` nil.
`SaveSnapshot` wrote that snapshot **wholesale** to the archive path — the same path the
backfill had just filled with hundreds of sessions. One `valuation-fetch` run destroyed roughly
3,000 backfilled sessions across all fourteen stocks, and because FU-8's `ValuationStep` calls
the same function, the daily pipeline would have done it again every day.

`SaveSnapshot` now reads what is already at the path and merges (`MergeHistory`) before
writing. Covered by `TestSaveSnapshotNeverDropsExistingHistory` and
`TestSaveSnapshotMergeIsIdempotent`.

The general shape is worth remembering: **two writers, one path, asymmetric knowledge.** The
writer that knows less must not be allowed to write the whole file.

One consequence to note before anyone parallelizes a fetch: `SaveSnapshot` is now
read-modify-write, and its temp file is a fixed `path + ".tmp"`. Both current callers
(`Service.FetchAndStore` and the backfill's `writeArchives`) are strictly sequential in a
single goroutine, so there is no live risk — but two concurrent writers to the same symbol
would lose an update and could collide on the temp name. Making the fetch concurrent means
giving the temp file a unique suffix and serializing per path.

### 8037 → 3037 欣興 — a catalog identity error, not a data-source gap

The earlier note here said 8037 was "absent from both the MOPS financial datasets and the TPEx
ratio dataset", and treated that as a fact about the exchanges. It was a fact about `stocks.yaml`.

欣興電子 is **3037, listed on TWSE**. 8037 is a different code on a different market. The
exchanges were right to have nothing; the repo was asking for a stock that does not exist there.

- `stocks.yaml:49` — `code: "8037"` → `"3037"` (the root cause; `configs/sectors.yaml`
  already had 3037 correct in all three places)
- `candidate.yaml` — `8037.TWO` → `3037.TW`
- `.cache/8037_TWO.json` (0 candles, an empty auto-detect placeholder) and the 8037 archive
  rows purged

After the fix 3037 backfills like any other TWSE stock: **225 sessions, 225 valid, 0 N/A**,
P25 96.53 / median 120.00 / P75 142.35, current percentile 2%, target `AVAILABLE`.

The lesson is that "the source does not have it" is a claim about the request as much as about
the source. A code that returns nothing from *every* independent dataset is more likely wrong
than universally unpublished.

---

## FU-6 — `ConfiguredPE` is not wired to configuration

**Severity:** an error message tells the user to do something they cannot do.

`internal/valuation/target_price.go` defines `PERange`, validates it (including the mandatory
non-empty `Source`), and `multipleRange` uses it as the fallback when the historical
distribution is unusable. **Nothing reads it from a config file.** `buildTargetPrice` never
sets it, so `RuleConfiguredRange` is unreachable in production.

The refusal message therefore reads:

> 歷史本益比樣本不足（0 筆，需 20），**且未設定人工本益比區間**

…pointing at an action with no available mechanism.

**Either** wire it — the shape would be:

```yaml
health_dashboard:
  pe_ranges:
    "2337.TW": { bear: 18, base: 25, bull: 32, source: "2026 記憶體產業區間，研究員 A" }
```

**or** reword the message until it is wired. Do not leave it as it stands.

> Wiring it is a real trade-off, not a pure win: a manual range means someone decides "the
> fair multiple for this stock", which is the subjective input the whole module is built to
> avoid. Its defensible use is the handful of stocks where a person genuinely has an industry
> view and will put their name on it — which is why `Source` is mandatory.

---

## FU-7 — Prices come from an unofficial source

**Severity:** low today; a known asymmetry worth recording.

| data | source | tier |
|---|---|---|
| OHLCV prices | `query1.finance.yahoo.com/v8/finance/chart` | **unofficial** |
| P/E · P/B · yield | TWSE, TPEx | official |
| revenue · financials · EPS | MOPS | official |
| institutional flows | TWSE, TPEx | official |

The target price depends on **both** tiers at once: the multiple is official, the EPS is
recovered by dividing an official P/E into a **Yahoo** close. A Yahoo close that disagreed
with the exchange's own would skew the implied EPS silently.

Both exchanges publish official daily closes (TWSE `STOCK_DAY`, TPEx equivalent). Options,
in increasing cost: cross-validate a sample, cross-validate the specific `EPSBaseDate` close,
or replace Yahoo as the price source. None attempted; recorded so the dependency is explicit.

---

## FU-8 — Daily fetch routine ✅ DONE

**Severity was:** the dashboard degrades quietly without one.

`data/institution/` **does not exist** — `cmd/institution-fetch` has never produced anything,
so the institutional block is `UNAVAILABLE` for every stock. The 2026-09-05 run correctly
skipped: it was a Saturday.

Two of the sources have **EXACT_DATE** semantics — no snapshot for that day means no data,
with no fallback to the previous day:

| command | semantics | consequence of skipping a day |
|---|---|---|
| `market-fetch` | EXACT_DATE | that day's market block is permanently UNAVAILABLE |
| news snapshot | EXACT_DATE | same |
| `valuation-fetch` | accrues | one fewer P/E sample, forever |
| `fundamental-fetch` | AS_OF | tolerable — the previous archive still resolves |
| `institution-fetch` | AS_OF | tolerable |

**Built:** `cmd/daily-data` / `make daily-data` orchestrates all of it — price session
readiness → valuation → fundamental → institution → news check → outcome grading →
completeness report. Session-ready at 14:00 Asia/Taipei; weekends and holidays are safe
no-ops; every step is idempotent and the run is designed to be repeated. See the operating
notes below.

---

## FU-8 operating notes (the pipeline is built; these belong in a runbook)

### Long holidays will alarm every day, and that is the intended trade

`ConfirmSession` refuses to call a day non-trading when the probe series has fallen more than
`maxProbeStaleDays` (4 calendar days) behind the target — because a series that stale says
more about our copy of it than about the market, and reading it as "holiday" would exit 0
through a whole week of failures.

Lunar New Year closes the exchange for roughly nine days. From the fifth day onward the probe
is stale by more than the threshold, so the pipeline will not declare a holiday and will
report FAILED or PARTIAL every day until trading resumes.

> **Do not loosen the threshold to stop the noise.** Raising it to cover a nine-day holiday
> re-opens the case it exists for: a stale cache silently reporting a run of holidays and
> exiting 0, which looks exactly like a quiet week. Either skip the scheduled run over the
> known holiday dates, or accept the alarms.

A real fix is a holiday calendar — but a guessed one eventually skips a genuine session, so
it would have to come from the exchange rather than a hard-coded list.

### The scheduler owns which day is acquired

A weekend run resolves to NON_TRADING for the weekend date and does **not** look back at
Friday. That is deliberate: making "which day am I acquiring" a stateful look-back would mean
one run mixing several days' results, and `Report.Date` would have to either lie or become an
array.

If Friday's run failed, Saturday's job should pass `-date` explicitly. That keeps the report
a statement about one session, and makes the repair visible in the scheduler rather than
implicit in the tool.

---

## FU-9 — Valuation evidence quality ✅ DONE

**Closed 2026-09-06.** Rule version `FU9-v1`.

The system could say whether a distribution was computable and nothing about how much was
behind it. 2330 台積電 and 2337 旺宏 were both `AVAILABLE`, both produced a target price, and
the page showed them identically — one is a year of a company's own trading history, the other
five weeks.

So quality is now a **second axis**, never a replacement for availability:

```
AVAILABLE + HIGH        AVAILABLE + MEDIUM        AVAILABLE + LOW        INSUFFICIENT_DATA
```

Nothing in it can make a computable distribution uncomputable. `MinHistoricalPESamples = 20`
is unchanged, the target price is unchanged, and the scanner is untouched.

The full contract — coverage semantics, the `N/A` ≠ missing-session rule, the rule version,
the not-backtest-fitted disclaimer, and the data-quality/model-suitability line — is in
§6 of `SPEC_R14_INTERACTIVE_STOCK_HEALTH_DASHBOARD.md`. What follows is what it looks like on
the real archive.

| stock | valid / archived | coverage | valid span | grade |
|---|---|---|---|---|
| 2330 台積電 | 225 / 225 | 100% | 339d | HIGH |
| 3037 欣興 | 225 / 225 | 100% | 339d | HIGH |
| 6274 台燿 | 225 / 225 | 100% | 339d | HIGH |
| 2337 旺宏 | 26 / 225 | 11.6% | 36d | **LOW** — target still AVAILABLE |
| 3055 蔚華科 | 0 / 225 | 0% | — | **INSUFFICIENT** — no distribution to grade |

### The two mistakes this was built to avoid

**Coverage measured against itself.** `valid ÷ valid` is 100% for every stock that has ever
existed, and it would have let 2337 present five weeks of samples as perfect coverage. The
denominator is every session ARCHIVED in the window — a session the exchange published no P/E
for is an acquired session, and that is the FU-5 contract restated one layer up.

**Grading an absence.** 3055 has 225 archived sessions and not one usable P/E. `LOW` would tell
a reader the number in front of them is weak; there is no number. It is `INSUFFICIENT`, and
`ClassifyQuality` checks that before anything else.

### What was deliberately left out

Dispersion is measured (`IQR`, `RelativeIQR`) and **does not feed the grade**. The original
note here proposed exactly that — flag a wide distribution, possibly downgrade its status —
and building it made the objection clear: a wide P/E range usually means the market re-rated
the company, which is a real event faithfully recorded, not a defect in the archive. 6274 台燿's
relative IQR is 91% on 225 of 225 sessions; its data is excellent. Letting width lower a
data-quality grade would file "this stock re-rated" and "our archive is thin" under one
warning, and would mark 3037 欣興 down for having captured a re-rating correctly.

Whether a re-rating makes the median a poor anchor is a **model** question. See below.

Also out of scope, deliberately: no scanner impact, no BUY/WATCH/SELL impact, and no
confidence channel into the entry assessment. R14's assessment has no clean place to take one,
and forcing it in would have made a data-quality grade into a trading rule.

---

## FU-10 — Converge `cmd/institution-fetch` onto `institution.Acquire`

**Severity:** low; a drift that has already happened once.

FU-8 extracted the provider loop into `internal/institution.Acquire` because it existed twice
and the copies had diverged — one counted `ErrWouldDowngrade` as a successful acquisition and
the other did not, so the same day could be reported differently depending on which entry
point ran.

`cmd/daily-data` uses `Acquire`. `cmd/institution-fetch` still has its own loop. Converging it
changes an existing command's reporting behaviour, so it was deliberately left out of FU-8 to
keep that diff reviewable — but leaving two implementations is how the first drift happened.

Do it as its own change, so the behavioural difference is reviewed on its own.

---

## FU-11 — Valuation model suitability ✅ DONE

**Closed 2026-09-06.** Rule version `FU11-v1`.

The fourth layer. FU-9 grades the EVIDENCE; this grades whether trailing P/E is the right lens
for the company at all. The contract — vocabulary, the two evidence inputs, the three things
that may never decide a verdict, target independence — is in §6 of
`SPEC_R14_INTERACTIVE_STOCK_HEALTH_DASHBOARD.md`. What follows is what it looks like on the
real archive and what building it revealed.

| stock | quality | P/E shape | filed earnings | suitability | reasons |
|---|---|---|---|---|---|
| 2330 台積電 | HIGH | PERSISTENT | POSITIVE | SUITABLE | positive-earnings eligibility persistent |
| 3037 欣興 | HIGH | PERSISTENT | POSITIVE (EPS 11.70) | **SUITABLE** | median ≈ 120x changes nothing |
| 6274 台燿 | HIGH | PERSISTENT | POSITIVE | **SUITABLE** | + `VALUATION_REGIME_WIDE` |
| 1815 富喬 | HIGH | PERSISTENT | POSITIVE | **SUITABLE** | + `VALUATION_REGIME_WIDE`, `TARGET_SENSITIVITY_REVIEW` (MoS 103.3%) |
| 2337 旺宏 | LOW | RECENT_ONLY | POSITIVE | **WEAK** | `PE_AVAILABLE_ONLY_RECENTLY`, `EARNINGS_SIGN_CHANGE` — target still AVAILABLE |
| 3055 蔚華科 | INSUFFICIENT | NEVER | NON_POSITIVE (EPS −0.03) | **UNSUITABLE** | `NO_POSITIVE_TRAILING_EARNINGS`, `PE_NEVER_PUBLISHED_IN_WINDOW` |

### The good idea this turned out to have — and exactly how far it goes

The P/E availability pattern FU-9 already computed is a **sign-state history**, and it was
sitting unused.

The fundamental archive holds exactly one filed period per stock — 2026Q2, for all fourteen —
so there is no way to watch reported earnings move over time. But the exchanges do not compute
a P/E when the reference EPS is zero or negative, which makes the nil/non-nil pattern across
225 archived sessions a readout of the denominator's SIGN over a year. It is the only earnings
history this repo has, and it came free.

It is not, however, an earnings-MAGNITUDE history, and my first draft overstated it — the
reason code was called `STABLE_POSITIVE_EARNINGS` and the docs said "stable earnings
denominator". Both were wrong in the same way:

```
TTM EPS   20 → 10 → 5 → 1     stays PERSISTENT throughout
```

A denominator collapsing by 95% never crosses zero, so this signal cannot see it. The code is
now `POSITIVE_EARNINGS_ELIGIBILITY_PERSISTENT`, SUITABLE is defined narrowly as "structurally
usable under the observed positive-earnings eligibility history", and every SUITABLE verdict
carries `EARNINGS_MAGNITUDE_NOT_ESTABLISHED` unconditionally — the limit travels with the
conclusion rather than living in a document nobody re-reads.

The general lesson is worth keeping: a proxy is named for what it measures, not for what it is
being used to argue. `STABLE_POSITIVE_EARNINGS` was a conclusion wearing the costume of an
observation, and once it is written on a persisted row, every later reader inherits it.

That is also why three of the four persistence cases need no threshold at all — `PERSISTENT`
is "no gaps", `NEVER` is "no observations", and `RECENT_ONLY` is an ORDERING: every gap
precedes every observation. Only the window-length check is numeric.

### What made 3055 the hard case

`UNSUITABLE` requires two independent sources agreeing: no P/E anywhere in the window AND a
filing showing the basis is not positive. The temptation is to skip the second, since a missing
P/E is such a strong hint — but silence is also consistent with a suspension, a listing change,
or an archive that never covered the stock, and concluding a loss from it would be inventing a
financial fact.

3055's 2026Q2 filing reports EPS −0.03 on net income −7,061,000. That is what licenses the
verdict. `TestNoPEWithoutAFilingIsInsufficientNotUnsuitable` holds the line permanently.

A related check that only appeared while writing it: the filing's EPS and net income must
AGREE on the sign. They come from the same statement and describe the same window, so a
disagreement means one was misparsed, and a sign read from a misparsed field is worse than no
sign at all.

### The observation floor, and why the filing does not bypass it

`UNSUITABLE` is the only verdict that asserts a company-level fact, so it is the only one that
needed a floor on how much observation stands behind it. Three archived sessions with no
published P/E now reaches `INSUFFICIENT_DATA`, not the same conclusion as 3055 蔚華科's 225.

The filed loss does **not** bypass it, and that was a deliberate choice against the tempting
one. Bypassing is defensible — the filing is genuinely independent evidence — but it would be
a rule that concludes from a STATEMENT rather than from a PATTERN, and quietly folding it into
`NEVER` semantics would leave the stored verdict unable to say which evidence produced it. If
that rule is ever wanted it gets its own name, its own reason code, and its own row.

### An acquisition gap is not a financial fact

The most dangerous confusion available to this layer: a session nobody fetched must never read
as a session on which the exchange declined to publish a ratio. The first is a hole in our
data; the second is a claim about the company.

It turns out to be safe by construction — a stock absent from a whole-market response is
archived with `Ratios` nil, so there is no row and the gap merely shortens the series, leaving
`P P [hole] P P` as PERSISTENT. But "safe by construction" is exactly the kind of safety that
quietly stops being true, so it now has a test at both levels.

The residue is honest and unfixable here: PERSISTENT means "no ARCHIVED session lacked a P/E",
not "no session lacked one". A loss stretch inside an unfetched gap is invisible. Same
archive-completeness caveat FU-9 carries; it needs a trading calendar, which belongs to FU-8's
pipeline rather than to a second one built here.

**And the version of this that WAS a live bug.** Review found the other half: an unresolvable
P/E column. `fieldIndex` returns −1 for an unmatched header, `pick` turns it into `""`, and
`ratio("")` returns nil — so a renamed header archived rows whose nil P/E now reads as
"no positive earnings". The `fieldIndex` comment was right when it was written; FU-11 silently
invalidated it by giving nil a meaning.

That is the sharpest form of the whole work item's hazard: **giving an existing value a new
meaning re-opens every path that can produce it.** A nil P/E was harmless for as long as it
only meant "no sample". The moment it meant "not profitable", every parser that could emit one
by accident became a fabricator of financial facts — and the live whole-market endpoint would
have done it for every listed company at once.

`TWSEMonth` and `TPEXSession` now fail the response outright when the P/E column cannot be
resolved; `parseRatioRows` skips rows whose P/E key is absent. A missing COLUMN is a parse
problem; a published dash is the fact.

Review then found the asymmetric twin: an unmatched `股票代號` header empties every code, skips
every row, and returns an empty map — which the backfill reads as a **non-trading day**. A
renamed column would have reported the entire window as holidays. Guarded too, which gives the
layer one rule: **a parse failure must never be reported in the vocabulary of an
observation** — not as "no earnings", not as "market closed".

### The guard that covered 11 of 14

The rendered-wording guard swept eight hand-written inputs while its own comment claimed to
cover "every verdict this layer can produce". It reached 11 of the 14 notes, and two of the
three it missed — `VALUATION_REGIME_WIDE` and `TARGET_SENSITIVITY_REVIEW` — are on 6274 台燿
and 1815 富喬 in production right now.

A sampling guard has holes exactly where the inputs were not imagined, which is where the bugs
are. It is worse than no guard, because the comment invites trust. Rewritten to run off the
declared reason table in both directions: every declared code must have wording that claims no
stability, and every code the classifier can produce must be declared — so a new reason cannot
be added without wording, and wording cannot be added without a check.

### Two guards the first review round found missing

Both were properties FU-11 states loudly, and both survived being broken — the review's exact
words were that the two loudest claims were the two nothing was holding.

**The AI could have been given a verdict field.** The schema guard forbade NUMERIC fields, and
every deterministic conclusion in this repo is now a word: assessment, quality, suitability. A
string field named `suitability` that overwrote the verdict *only when the model supplied one*
passed the whole suite. Replaced the denylist with an ALLOWLIST over both the schema and the
decoded struct, plus an end-to-end test where the model returns fabricated verdicts for
suitability, quality, assessment and target price, and none of them move.

**Nothing pinned the asOf bound on the persistence shape.** The code was right — it reads the
same bounded slice as everything else — but replacing it with an unbounded read of the full
history broke no test. That matters more than it did for FU-9's metrics, because persistence is
a decision input to a PERSISTED verdict: a leak would silently rewrite history, so a stock that
later turned profitable would read RECENT_ONLY in a check dated before it did.

Also fixed: a stock with ZERO archived sessions plus a filed loss reached UNSUITABLE carrying
`PE_NEVER_PUBLISHED_IN_WINDOW` — a claim about an exchange that was never queried. That is the
silence-is-not-a-loss error running backwards, so `PEUnknown` and `NO_ARCHIVED_SESSIONS` now
separate "we watched and saw nothing published" from "we never watched".

### An existing invariant caught a real collision

`TestMissingFiguresProduceNoEvidenceRow` forbids any evidence row from storing a status word
as its value — `valuation/trailing_pe = "INSUFFICIENT_DATA"` would be a missing number dressed
as a measurement. `model_suitability` legitimately takes the value `INSUFFICIENT_DATA`, so it
tripped the check.

The fix was to name the exception rather than loosen the rule: keys that name a
CLASSIFICATION (whose entire vocabulary is words) are listed explicitly, and every key that
names a FIGURE keeps the original protection. Widening the allowlist would have been the
cheaper move and would have quietly disarmed a guard that has real work to do.

### Data gap closed on the way

3037 欣興 had no fundamental archive at all — the identity fix (FU-5) landed after the last
`fundamental-fetch`, so the repo had only ever asked MOPS about 8037. Re-fetched; all 14 stocks
now have a filed period.

---

## FU-12 — Alternative valuation models / model selection

**Severity:** design. FU-11 says which lens is wrong; it does not hand over a different one.

`UNSUITABLE` and `WEAK` are now reachable verdicts, and today they end the conversation: the
page says P/E is not the right primary anchor and offers nothing in its place. 3055 蔚華科 is
the live example — no usable P/E, a filed loss, and no other multiple to fall back on.

What this needs, and why none of it is a tweak:

- **A per-sector notion of which multiple is primary** (P/E, P/B, EV/EBITDA, EV/Sales). This is
  a judgement, and the repo has no authorized source for it. Inventing a sector→multiple table
  here would be the same class of mistake as an AI-chosen P/E.
- **Inputs that do not exist.** Book value per share is derivable from the financial
  statements; EBITDA and enterprise value are not, and neither is a share count this repo
  currently reads. Each new multiple is a data-acquisition problem before it is a modelling one.
- **A rule for the fallback.** Silently reverting to P/E when a sector's primary multiple is
  unavailable would be worse than the current state, because the page would look identical
  while meaning something different.
- **A second target-price engine, or a generalised one.** `ComputeTargetPrice` is written
  around one multiple and one same-session EPS recovery. Generalising it touches the most
  carefully constrained code in the valuation layer.

Two constraints for whoever picks this up:

**Do not start by loosening FU-9 or FU-11.** A model-selection layer that expresses itself by
lowering a data-quality grade, or by making a suitability verdict mean "use P/B instead",
destroys the separation all three layers depend on. Selection is a fifth question, not a
rewording of the fourth.

**A new multiple needs its own suitability question.** P/B is inappropriate for an
asset-light company for reasons that have nothing to do with why P/E is inappropriate for a
loss-making one. Copying FU-11's rules onto a different denominator would produce verdicts
that look principled and mean nothing.
