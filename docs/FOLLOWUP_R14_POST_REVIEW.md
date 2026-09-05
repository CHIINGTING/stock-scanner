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

## FU-5 — Valuation backfill is incomplete (6 of 14 stocks)

**Severity:** the target price is unavailable for those stocks until it finishes.
**State as of 2026-09-05.**

`cmd/valuation-backfill -months 12` completed for 8 of 14, and those have working targets:

| stock | sessions | result |
|---|---|---|
| 2330 · 2327 · 6488 · 3026 · 6274 · 1815 · 6789 · 1560 | 225 | distribution ready |
| 3094 | 24 | usable, but thin — re-run to fill |
| 2337 · 1503 · 2449 · 3055 | 0–4 | **TWSE rate limit** |
| 8037 | 0 | **not in the TPEx ratio dataset at all** |

### The rate limit

TWSE's CDN (HiNetCDN) answers a burst with **HTTP 307** and the block persists for a
cooldown measured in tens of minutes. It began at roughly the 66th request; the send order
matches the failure order exactly.

Already fixed in `internal/valuation/historical.go` and `cmd/valuation-backfill`:

- 307 / 429 / 503 recognised as a rate limit (`IsRateLimited`), with a **30s base backoff**
  doubling per retry rather than the 1s used for ordinary errors
- default throttle 900ms → **1500ms**, retries 3 → 4
- **resume**: a re-run loads today's archive first and skips months already covered, so the
  realistic several-pass completion of a large backfill actually makes progress
  (`-refetch` forces a full re-fetch)

**To finish:** wait for the cooldown, then just re-run. It picks up where it stopped.

```bash
go run ./cmd/valuation-backfill -months 12
```

### 8037 欣興

Absent from **both** the MOPS financial datasets and the TPEx ratio dataset — `fundamental-fetch`
reported `8037 appears in neither dataset` and `valuation-fetch` reported `not in the TWO ratio
dataset`. That is a data-source fact, not a fetch failure. **Worth confirming the code and its
market** before assuming the exchanges are wrong.

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

## FU-8 — No daily fetch routine

**Severity:** the dashboard degrades quietly without one.

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

A post-close daily job covering all five is the obvious fix. Until then, historical health
checks will have permanent holes on the days nothing ran.

---

## FU-9 — A wide P/E distribution is not flagged

**Severity:** presentation; risks over-trusting a number.

The targets are P25 / median / P75 of the stock's own distribution, and the page shows all
three — but says nothing about how **dispersed** they are. 6274 台燿 spans 41.04 → 87.07 over
twelve months, more than 2×. A median drawn from a distribution that wide is not a stable
anchor, and a "margin of safety" derived from it deserves far less weight than the same
number computed from a tight distribution.

Nothing on the page distinguishes the two cases. Options: show the P75/P25 ratio, mark a
distribution above some dispersion threshold, or downgrade the target's own status to
`PARTIAL` when the spread is extreme. The third changes a deterministic status and would need
its own rule and threshold — so it is a design decision, not a tweak.

