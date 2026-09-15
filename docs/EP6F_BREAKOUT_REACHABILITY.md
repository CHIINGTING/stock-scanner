# EP-6F — BREAKOUT_BUY reachability and semantic validation

## Decision

- Recommendation: `KEEP_DEAD`
- Confidence: `INSUFFICIENT_EVIDENCE`
- Production semantic gate: `NO PRODUCTION STRATEGY CHANGE`

This means preserve the current behavior for now, not that a permanently dead action is desirable.
The tested `PREVIOUS_WINDOW_HIGH` candidate has positive mean forward returns, but it also emits
8,854 signals, has a 53.25% research-defined five-session false-breakout rate, lacks point-in-time
market-regime coverage, and was not compared with a canonical base-breakout definition. Signal
existence alone is not enough to authorize a strategy change.

## Complete production path and proof

`Scanner.EnrichWatchlist` calls `analyzeConsolidation`, then passes the result to `computeRocket`.
`computeRocket` can assign `StageBreakoutStart` only through `justBroke`; `watchActionFor` maps that
stage to `ActBreakoutBuy`. With the optional momentum guardrail enabled, `jointWatchAction` runs
afterward, so it is also an action gate.

The current `justBroke` predicates are all required:

1. at least 30 candles (otherwise `computeRocket` returns `WAIT`);
2. `PivotHigh > 0`;
3. current `Close > PivotHigh` (strict; equality does not pass);
4. prior `Close <= PivotHigh * 1.001`;
5. current volume ratio `Volume / VolumeMA20 >= 1.3`.

Stage precedence adds earlier gates. `FAILED` wins when the platform broke, price/MA20 and return
conditions fail, or outflow/MA10 conditions fail. `OVERHEATED` wins for extension (`MA5` distance
over 12% or 5-day return over 25%) or climax/distribution. Only then is `justBroke` considered.
The resulting action may subsequently be changed by the optional momentum joint-action rule.
Sector, score, base quality, setup bucket, and explosion probability do not gate the ordinary
`StageBreakoutStart -> BREAKOUT_BUY` mapping. Volume confirmation does gate it as stated above.

`analyzeConsolidation` constructs `Consol.PivotHigh` as the maximum High in a window ending at
`n-1`, in both the `days < 3` and ordinary branches. Thus the signal bar participates. The
ordinary branch's effective start is `n-1-min(60,n-1)`, so the value is a recent high reference,
not the local consolidation-box high calculated earlier in that function. No tick rounding occurs
before `justBroke`: raw floats are compared. `round1` is applied only to the displayed breakout
price after stage/action selection.

For a legal OHLC signal bar:

- `PivotHigh >= current High` because the reference includes the signal bar;
- `current Close <= current High` by OHLC validity;
- therefore `current Close <= PivotHigh`;
- but `justBroke` requires `current Close > PivotHigh`.

The conjunction is impossible. `TestEP6FCurrentBreakoutIsUnreachable` machine-pins current-bar
inclusion, legal OHLC ordering, strict/equality behavior, and the real production decision path.
It is a conditional unreachability result: malformed OHLC can violate the second premise. In the
raw cache, two apparent current breakouts disappeared after the explicit OHLC-validity screen;
213 malformed signal bars were excluded.

## Candidate and point-in-time boundary

`PREVIOUS_WINDOW_HIGH` uses the same 60-bar scanner lookback, but only completed bars:

```text
reference[T] = max(High[T-60:T-1])
```

All other `computeRocket` predicates and precedence remain unchanged in the observational replay.
The candidate is test-only and is not production behavior. The append-future-bars test pins that
the reference and decision at T are unchanged when bars after T are appended. Forward returns and
excursions alone read T+1 onward.

## Repository-cache observational study

Source: 1,997 existing `.cache/*.json` files; no network fetch. Each symbol was replayed by
historical prefix. Effective opportunity-set N was 797,445 valid point-in-time evaluations after
excluding 213 invalid signal bars. Candidate outcome N was 8,854 signals with 20 forward sessions.

| Measure | Result |
|---|---:|
| Current `BREAKOUT_BUY` | 0 |
| Candidate `BREAKOUT_BUY` | 8,854 |
| Unique symbols | 1,787 |
| Unique signal dates | 407 |
| Signals per symbol | min 1, max 27 |
| Volume confirmed (`>=1.3`) | 8,854 |
| Mean return, 5 sessions | +0.9780% (N=8,854) |
| Mean return, 10 sessions | +1.7331% (N=8,854) |
| Mean return, 20 sessions | +2.7641% (N=8,854) |
| Mean 20-session MFE | +15.6978% (N=8,854) |
| Mean 20-session MAE | -9.2000% (N=8,854) |
| False breakout | 4,715 / 8,854 (53.25%) |

False breakout is an **EP-6F research definition**, not established strategy semantics: any close
below the candidate reference within the next five sessions.

Distance over reference: 2,590 at `[0%,1%)`, 3,292 at `[1%,3%)`, and 2,972 at `>=3%` (N shown by
each count). All candidate signals necessarily have `StageBreakoutStart` and volume confirmation,
so those strata do not vary. The repository cache has no trustworthy point-in-time market-regime
series covering these 407 dates; regime results are therefore explicitly unavailable, not filled
from a future/current snapshot. No inference is made from missing regime strata.

Overlap with the existing primary scanner `Action`: WATCH 5,166; HOLD 2,612; BUY 825; STRONG BUY
1; REDUCE 194; SELL 56. Thus sell-type overlap is 250 (REDUCE + SELL); TAKE_PROFIT and STOP_LOSS
were 0 in this market-source replay. Existing `WatchAction` overlap: WATCH_CLOSELY 5,654; WAIT
2,576; PREPARE_ENTRY 624; PULLBACK_BUY 0; existing BREAKOUT_BUY 0.

## EntryPlan impact

The existing projection maps `BREAKOUT_BUY` to `EntrySemantic=BREAKOUT`; no EntryPlan rule was
changed. Replaying the candidate through `AttachEntryPlan` without inventing historical regimes
produced:

| Status | N |
|---|---:|
| BUY_NOW | 0 |
| WAIT_BREAKOUT | 0 |
| TOO_EXTENDED | 0 |
| NO_VALID_ENTRY | 250 |
| INSUFFICIENT_DATA | 8,604 |

Executable-price gains: 0. The 250 sell-side contradictions remain `NO_VALID_ENTRY` with no
executable entry price; the rule was neither weakened nor bypassed. The other plans cannot resolve
without point-in-time regime evidence. This is an honest limitation of this study, not evidence
that reachable breakouts would never gain prices in a fully covered production scan.

## Mutation evidence and regression boundary

- Mutating strict `Close > PivotHigh` to `>=` made the reachability guard fail with a real
  `BREAKOUT_BUY` at equality.
- Mutating both PivotHigh constructions to end at `n-2` made the inclusion premise guard fail.
- Both mutations changed behavior (neither was `NO_CHANGE` or comment-only), were restored, and
  SHA-256 matched baseline afterward: `rocket.go` `ed741bac...d64ac5`, `consolidation.go`
  `804b7a50...cfdf2`.

Tradeoff: enabling the tested candidate would convert 8,854 historical evaluations from WAIT,
WATCH_CLOSELY, or PREPARE_ENTRY into BREAKOUT_BUY and would expose the existing BREAKOUT EntryPlan
mapping. Keeping the current rule preserves all scanner scores, stages/actions outside this dead
branch, EntryPlan semantics, RuleVersion, valuation, persistence, and reports while the missing
semantic and regime evidence is resolved.

README: `NO_CHANGE` — EP-6F validated a known defect and evaluated a test-only candidate; no rule
was implemented, integrated, or enabled, so documenting the candidate as scanner behavior would
be false. This report maintains `validated != implemented != integrated != enabled`.

Regression: `go test ./...` passed all 65 packages (`packages OK: 65`, `packages FAIL: 0`). The
final unrestricted run was mixed cached/non-cached: 43 cached packages, 10 non-cached tested
packages, and 12 packages with no test files. The initial sandboxed run failed only where
`httptest` was forbidden to bind localhost; rerunning with localhost permission passed those
packages. Scope formatting/checks were limited to the new EP-6F test and this document; no claim
is made about repository-wide gofmt cleanliness or the pre-existing `internal/r6backtest` files.
