# SPEC R10-2 — Candlestick Pattern Analyzer (Shadow Layer)

Status: **DRAFT (design-only, pre-implementation)**
Date: 2026-07-20
Scope: Add a per-stock **K 線型態 (Candlestick Pattern)** analysis module as a **shadow
layer** — computed, attached, displayed, persisted, and historically validatable, but with
**zero effect** on existing Score / Action / sort / Stage / Rocket / Momentum / VCP / BUY-
WATCH-REDUCE / default HTML / default JSON in P0.

Follows the same discipline as R10-1 (docs/SPEC_R10_1_INSTITUTION_BIAS_SHADOW.md): shadow-
first, default-off, calc ⊥ interpretation ⊥ rendering, thresholds centralized, deterministic.

---

## 1. Goals / Non-goals

### Goals (P0)
- Detect **13 P0 patterns** from daily OHLC via a strictly layered, deterministic pipeline.
- Attach results to the watchlist entry, render behind a display flag, persist to the
  structured sidecar for cohort validation.
- Emit, per signal: `Type`, `Direction`, `Confidence`, `Description` (中文), and a
  `SuggestedAdjustment` (a **proposed** score delta that is **NOT applied** in P0).

### Non-goals (P0)
- No change to scoring / action / sort / Stage / Rocket / Momentum / VCP.
- No 3-bar patterns. **Morning Star / Evening Star → P1. Harami → P1.**
- No Dragonfly/Gravestone Doji, Spinning Top, Tweezer, Three Soldiers/Crows (P1 — the
  layering makes these additive without touching Geometry).
- `SuggestedAdjustment` is recorded/validated only; never fed into any score.

---

## 2. The 13 P0 patterns

Each is an independent `PatternType` (see §7 — no Direction-multiplexing).

**Single-candle (9)**
1. `DOJI`
2. `LONG_UPPER_SHADOW`
3. `LONG_LOWER_SHADOW`
4. `HAMMER`
5. `HANGING_MAN`
6. `INVERTED_HAMMER`
7. `SHOOTING_STAR`
8. `BULLISH_MARUBOZU`
9. `BEARISH_MARUBOZU`

**Two-candle (4)**
10. `BULLISH_ENGULFING`
11. `BEARISH_ENGULFING`
12. `PIERCING_LINE`
13. `DARK_CLOUD_COVER`

`BULLISH_MARUBOZU` and `BEARISH_MARUBOZU` are **two independent PatternTypes**, NOT one
`MARUBOZU` + Direction. Rationale: consistency with every other type; HTML needs no second
mapping; sidecar `group by pattern` is direct; the validator computes each one's win rate
without re-splitting by Direction. (Direction / Description / Confidence /
SuggestedAdjustment / historical stats all differ.)

---

## 3. Pipeline architecture

```
Raw OHLC
  ↓
Validation           — reject degenerate bars (Range<=0, High<Low, …); never panic
  ↓
Metrics              — body, range, upper/lower shadow, ratios  (PURE)
  ↓
Geometry Detection   — SHAPE ONLY; deterministic; MULTI-OUTPUT   (PURE, context-blind)
  ↓
Context Builder      — Stage, trend, near-high/low, returns, volume → MarketContext (reusable)
  ↓
Interpretation       — Geometry + MarketContext → formal PatternType + Direction
  ↓
Confidence           — deterministic score from geometry extremity + context alignment
  ↓
PatternSignal        — one interpreted signal (Type, Direction, Confidence, Desc, Adj)
  ↓
Analyzer Result      — []PatternSignal (+ the MarketContext used), sorted by confidence
```

**Hard layering rules:**
- **Geometry Detection is a pure function of Metrics only.** It MUST NOT know Stage, MA,
  NearHigh, NearLow, Return, Volume, or Trend. It **outputs a set** of geometries (a bar can
  be simultaneously e.g. `GeometryBearishBody` + `GeometryLongUpperShadow`), never a single
  verdict.
- **Context Builder computes trend/context ONCE** into a reusable `MarketContext`.
  Interpretation **consumes** it and **must never recompute trend** itself.
- Only **Interpretation / Confidence** may read Stage / Trend / Context / Volume.
- Adding `DRAGONFLY_DOJI` / `GRAVESTONE_DOJI` / `SPINNING_TOP` later must require ~no change
  to Geometry — only new Interpretation rules over existing geometries.

---

## 4. Metrics + Validation

```go
type Metrics struct {
	Open, High, Low, Close float64
	Body        float64 // |Close-Open|
	Range       float64 // High-Low
	UpperShadow float64 // High - max(Open,Close)
	LowerShadow float64 // min(Open,Close) - Low
	Bullish     bool    // Close > Open
	Bearish     bool    // Close < Open
	BodyPct     float64 // Body/Range        (0 when Range==0)
	UpperPct    float64 // UpperShadow/Range
	LowerPct    float64 // LowerShadow/Range
}
```
- **Validation** (before Metrics ratios): `High >= Low`, `High >= max(Open,Close)`,
  `Low <= min(Open,Close)`, `Range > 0`. A bar failing validation yields **no geometry**
  (detection skipped for that bar) — never a divide-by-zero, never a panic. A limit-locked
  bar (High==Low) is a legitimate degenerate case → no pattern.
- Metrics are pure; ratios guard `Range==0`.

---

## 5. Geometry Detection (pure, deterministic, multi-output)

Geometry tags (extensible; single-bar unless noted):
```go
const (
	GeometryBullishBody     // Close > Open
	GeometryBearishBody     // Close < Open
	GeometryDoji            // BodyPct <= DojiBodyMax
	GeometryMarubozu        // BodyPct >= MarubozuBodyMin AND both shadows small
	GeometryLongUpperShadow // UpperPct >= LongShadowMin AND LowerPct <= ShortShadowMax AND small body
	GeometryLongLowerShadow // LowerPct >= LongShadowMin AND UpperPct <= ShortShadowMax AND small body
	// two-bar geometric relationships (pure shape; colors decide direction, NOT context):
	GeometryBullishEngulf   // prev bearish body, cur bullish body, cur body engulfs prev body
	GeometryBearishEngulf   // prev bullish body, cur bearish body, cur body engulfs prev body
	GeometryPiercing        // prev bearish, cur bullish opens below prev low/close, closes > prev body midpoint (< prev open)
	GeometryDarkCloud       // prev bullish, cur bearish opens above prev high/close, closes < prev body midpoint (> prev open)
)
```
- `DetectSingleBar(m Metrics, th Thresholds) []Geometry` and `DetectTwoBar(prev, cur Metrics)
  []Geometry` are both pure and return **ordered sets** (possibly empty, possibly several).
- All thresholds live in `Thresholds` (§8). Engulf/piercing/dark-cloud direction is decided
  by candle **colors + body relationship only** — never by market context (that only
  affects Confidence and, for shadows, the interpreted name).

### 5.1 Ordering contract (LOCKED)

Geometry output order is a **formal contract**, defined by the canonical `geometryOrder`
slice — NOT the order detection conditions happen to be written, and NEVER map-iteration
order. Emission walks `geometryOrder` and keeps the detected tags:
```
1. GeometryBullishBody
2. GeometryBearishBody
3. GeometryDoji
4. GeometryMarubozu
5. GeometryLongUpperShadow
6. GeometryLongLowerShadow
7. GeometryBullishEngulf
8. GeometryBearishEngulf
9. GeometryPiercing
10. GeometryDarkCloud
```
Any NEW geometry MUST be inserted into `geometryOrder` at its proper position; adding it only
at a detection site (relying on append order) is a contract violation. A test asserts every
emitted tag is registered in `geometryOrder` and that output is a subsequence of it.

### 5.2 Boundary policy (LOCKED)

All threshold comparisons are **INCLUSIVE**, uniformly (no per-pattern discretion):
- "long / big" bounds use `>=` (e.g. `UpperPct >= LongShadowMin`, `BodyPct >= MarubozuBodyMin`)
- "short / small" bounds use `<=` (e.g. `BodyPct <= DojiBodyMax`, `LowerPct <= ShortShadowMax`)

Therefore a ratio EXACTLY equal to its threshold SATISFIES it: `UpperPct == LongShadowMin` →
long-shadow fires; `BodyPct == DojiBodyMax` → Doji fires; `BodyPct == MarubozuBodyMin` →
Marubozu candidate. Tested at the exact boundary.

### 5.3 Overlap policy (LOCKED)

**May coexist** (multi-output is expected):
- `BullishBody` + `LongLowerShadow`
- `Doji` + `LongLowerShadow`
- `Doji` + `LongUpperShadow`

**Must never coexist** (guaranteed by construction; tested):
- `BullishBody` + `BearishBody` (a body is one color, or flat = neither)
- `BULLISH_MARUBOZU` + `BEARISH_MARUBOZU` (they derive from opposite body colors)
- `GeometryBullishEngulf` + `GeometryBearishEngulf` (opposite color pairs)
- `GeometryPiercing` + `GeometryDarkCloud` (opposite color pairs); also engulf ⟂ piercing
  (engulf closes beyond the prior body, piercing within it)

### 5.4 DetectTwoBar ordering contract (LOCKED)

`DetectTwoBar(prev, cur Metrics)`: **prev MUST be chronologically earlier than cur.** The
caller guarantees time order. The function never swaps and never sorts; passing them reversed
yields wrong (but deterministic) output. This removes any "which one is latest?" ambiguity.

---

## 6. Context Builder (reusable MarketContext)

```go
type Stage string // injected pipeline stage label (e.g. RocketStage); "" when unknown

type MarketContext struct {
	Stage       Stage
	Uptrend     bool
	Downtrend   bool
	Near20DHigh bool
	Near20DLow  bool
	Return5D    float64
	Return10D   float64
	VolumeRatio float64 // latest volume / 20D avg (window excludes latest)
	// Capability flags — "unknown / insufficient history" vs "proven false" (LOCKED):
	HasTrend       bool
	HasNearHigh    bool
	HasNearLow     bool
	HasVolumeRatio bool
}
func (c MarketContext) Sufficient() bool { return c.HasTrend }
```
- Built **once** from the candle history (+ injected Stage); Interpretation CONSUMES it and
  must never recompute trend / return / volume / near-high-low.
- **Capability flags (LOCKED):** when history is too short, the boolean stays false AND its
  capability flag is false, so Confidence tells "missing data" apart from "proven absent".
  Never conflate `Uptrend=false, Downtrend=false` (could be flat-but-known) with "unknown".
- **Trend (P0 baseline):** derived from `Return10D` — `Uptrend` if `Return10D >=
  TrendReturnMin`, `Downtrend` if `<= -TrendReturnMin`, else neither; `HasTrend` requires
  ≥ 11 bars. (MA-slope refinement is P1.)
- **Lookback / no-look-ahead contract (LOCKED):** latest bar = `candles[len-1]`. The
  Near-high, Near-low, and average-volume windows are the 20 bars **strictly before** the
  latest bar — they NEVER include it (so a bar cannot be "near its own high"). Returns compare
  the latest close to a past close.
- **The candlestick package computes trend/near/return/volume itself; `Stage` is the ONLY
  externally-injected field** (keeps the package reusable and pure w.r.t. scanner concepts).

---

## 7. Interpretation (Geometry + MarketContext → PatternType)

`PatternType` constants (independent Marubozu types as required):
```go
PatternNone, PatternDoji, PatternLongUpperShadow, PatternLongLowerShadow,
PatternHammer, PatternHangingMan, PatternInvertedHammer, PatternShootingStar,
PatternBullishMarubozu, PatternBearishMarubozu,
PatternBullishEngulfing, PatternBearishEngulfing, PatternPiercingLine, PatternDarkCloudCover
```

Mapping (context decides the *name* for shadow geometries; colors decide it for the rest):

| Geometry | Context | → PatternType | Direction |
|---|---|---|---|
| LongLowerShadow | Downtrend or Near20DLow | HAMMER | BULLISH |
| LongLowerShadow | Uptrend or Near20DHigh | HANGING_MAN | BEARISH |
| LongLowerShadow | neutral / insufficient | LONG_LOWER_SHADOW | NEUTRAL |
| LongUpperShadow | Downtrend or Near20DLow | INVERTED_HAMMER | BULLISH |
| LongUpperShadow | Uptrend or Near20DHigh | SHOOTING_STAR | BEARISH |
| LongUpperShadow | neutral / insufficient | LONG_UPPER_SHADOW | NEUTRAL |
| Doji | any | DOJI | NEUTRAL |
| Marubozu + BullishBody | any (ContextMatched only if trend agrees) | BULLISH_MARUBOZU | BULLISH |
| Marubozu + BearishBody | any (ContextMatched only if trend agrees) | BEARISH_MARUBOZU | BEARISH |
| BullishEngulf | Downtrend or Near20DLow | BULLISH_ENGULFING | BULLISH |
| BullishEngulf | **otherwise** | **no signal** (geometry evidence internal only) | — |
| BearishEngulf | Uptrend or Near20DHigh | BEARISH_ENGULFING | BEARISH |
| BearishEngulf | **otherwise** | **no signal** | — |
| Piercing | Downtrend or Near20DLow | PIERCING_LINE | BULLISH |
| Piercing | **otherwise** | **no signal** | — |
| DarkCloud | Uptrend or Near20DHigh | DARK_CLOUD_COVER | BEARISH |
| DarkCloud | **otherwise** | **no signal** | — |

- Interpretation may emit **multiple** signals for one bar-position (e.g. a two-bar signal
  plus a single-bar signal). Deterministic precedence: two-bar patterns rank above single-
  bar; within a bar, a specific named pattern outranks a neutral geometry-only name. Results
  are sorted by Confidence desc.
- A bar that is both Doji-geometry and long-shadow-geometry (future Dragonfly/Gravestone)
  resolves to `DOJI` in P0; the extra classification is a P1 rule added here, not in Geometry.

### 7.2 Interpretation precedence & multi-signal policy (LOCKED)

Interpretation is a deterministic rule engine (`InterpretSingleBar` / `InterpretTwoBar` /
`Interpret`). It consumes Geometry + MarketContext only — it never re-analyses OHLC and never
recomputes trend/return/volume/near.

Single-bar precedence (first match wins; emits exactly ONE signal or none):
```
1. DOJI                                   (independent; wins over long-shadow — Dragonfly/Gravestone are P1)
2. BULLISH_MARUBOZU / BEARISH_MARUBOZU     (by body color)
3. HAMMER / HANGING_MAN / INVERTED_HAMMER / SHOOTING_STAR   (long-shadow + matching context)
4. LONG_UPPER_SHADOW / LONG_LOWER_SHADOW   (generic fallback: neutral / insufficient / conflict)
```
- Generic body color (`BullishBody`/`BearishBody`) is **evidence only** — never a signal.
- A formal single-bar pattern **covers (suppresses)** its generic shadow fallback (you never
  get HAMMER *and* LONG_LOWER_SHADOW).
- **Context conflict** (bullish- AND bearish-supporting both true, e.g. Downtrend+Near20DHigh)
  → generic fallback, never a hard pick.

Combined policy: at most ONE two-bar + ONE single-bar signal for the latest bar-position.
Two-bar is emitted first (prioritized over generic single-bar geometry). The two never share
a PatternType (disjoint families) → no duplicate types. HAMMER + BULLISH_ENGULFING may
coexist; HAMMER + LONG_LOWER_SHADOW may not.

### 7.1 Family output semantics (LOCKED)

| Family | Context mismatch behaviour | ContextMatched | Signal emitted? | SuggestedAdjustment |
|---|---|---|---|---|
| HAMMER / HANGING_MAN / INVERTED_HAMMER / SHOOTING_STAR | context DECIDES the name; no match → falls back to generic shadow | true (by construction) | yes | table value |
| LONG_UPPER_SHADOW / LONG_LOWER_SHADOW | neutral-context fallback | false | yes (generic, lower Confidence) | ±2 (exception) |
| BULLISH_MARUBOZU / BEARISH_MARUBOZU | still emitted; Description notes位置風險 | false when trend disagrees | yes | 0 when ContextMatched=false; table value when true |
| BULLISH_ENGULFING / BEARISH_ENGULFING / PIERCING_LINE / DARK_CLOUD_COVER | **NOT emitted**; geometry kept internal only | (n/a — no signal) | **no** | n/a |
| DOJI | always emitted, Direction NEUTRAL | false | yes | 0 (always) |

---

## 8. Thresholds (centralized, BASELINE — not calibrated)

```go
type Thresholds struct {
	DojiBodyMax     float64 // default 0.10  (BodyPct <=)
	MarubozuBodyMin float64 // default 0.90
	MarubozuShadowMax float64 // default 0.08 (each shadow pct)
	LongShadowMin   float64 // default 0.50  (long shadow pct >=)
	ShortShadowMax  float64 // default 0.20  (opposite shadow pct <=)
	ShadowBodyMax   float64 // default 0.35  (body pct for hammer/star family)
	NearPct         float64 // default 3.0   (% within 20D high/low → Near flags)
	TrendReturnMin  float64 // default 3.0   (% Return10D magnitude to confirm trend w/ MA)
	VolConfirm      float64 // default 1.5   (VolumeRatio for volume-confirmed confidence)
}
func DefaultThresholds() Thresholds { … }
```
No magic numbers outside this struct. P1 may make these regime/volatility-aware.

---

## 9. Confidence + SuggestedAdjustment (LOCKED)

### 9.1 SuggestedAdjustment baseline (shadow research only — never enters scoring)

| PatternType | SuggestedAdjustment |
|---|---|
| DOJI | 0 |
| LONG_UPPER_SHADOW | −2 |
| LONG_LOWER_SHADOW | +2 |
| HAMMER | +4 |
| HANGING_MAN | −3 |
| INVERTED_HAMMER | +3 |
| SHOOTING_STAR | −5 |
| BULLISH_MARUBOZU | +3 |
| BEARISH_MARUBOZU | −3 |
| BULLISH_ENGULFING | +6 |
| BEARISH_ENGULFING | −6 |
| PIERCING_LINE | +4 |
| DARK_CLOUD_COVER | −4 |

Rules (invariants, enforced + tested):
- **`AppliedAdjustment` is ALWAYS 0** in P0 (the value that would touch a score). It is a
  separate field from `SuggestedAdjustment` precisely so the "never applied" invariant is
  explicit and greppable.
- **`SuggestedAdjustment` may be non-zero only when `ContextMatched == true`** — with one
  documented exception: the generic shadow fallbacks `LONG_UPPER_SHADOW` (−2) /
  `LONG_LOWER_SHADOW` (+2) keep their ±2 even though `ContextMatched == false` (they carry a
  mild geometric lean), **but their Confidence must be lower than any formal context
  pattern** (§9.2 ranges).
- **`DOJI` is always 0**, regardless of context.
- The four context-named single-bar patterns (HAMMER / HANGING_MAN / INVERTED_HAMMER /
  SHOOTING_STAR) exist only when context matched → `ContextMatched=true` by construction.
- **MARUBOZU** is a directional-body *shape* description, not inherently a reversal claim, so
  it is **still emitted** as `BULLISH_MARUBOZU` / `BEARISH_MARUBOZU` even when context does
  not align → then `ContextMatched=false`, `SuggestedAdjustment=0`, lower Confidence, and the
  Description notes the位置風險 (position risk).
- **ENGULFING / PIERCING / DARK_CLOUD are formal reversal patterns and are context-GATED.**
  When the geometry holds but prior trend / near-high / near-low does NOT support the
  reversal, the analyzer **must NOT emit a formal signal**: no `BULLISH_ENGULFING` /
  `BEARISH_ENGULFING` / `PIERCING_LINE` / `DARK_CLOUD_COVER` in `Result.Signals`, no
  classification, no `SuggestedAdjustment`. The raw `GeometryBullishEngulfing` /
  `GeometryBearishEngulfing` / `GeometryPiercing` / `GeometryDarkCloud` evidence is retained
  **internally only** (never surfaced as a reversal signal). Rationale: a bullish engulfing
  without a prior decline is not a bullish reversal under this SPEC.

### 9.2 Confidence (LOCKED — deterministic additive evidence, no weight model)

`Confidence` starts at the base and sums the applicable terms, then clamps to `[0.0, 1.0]`:

| Evidence term | Δ |
|---|---|
| Base geometry match | 0.40 |
| Geometry clearly exceeds threshold | +0.10 |
| Strong geometry / body relationship | +0.10 |
| Prior trend matched | +0.15 |
| Near recent high / low matched | +0.10 |
| Stage supports interpretation | +0.05 |
| Volume confirmed | +0.10 |
| Weak volume | −0.10 |
| Context missing for generic fallback | −0.10 |
| Insufficient context history | −0.10 |

`clamp(sum, 0.0, 1.0)`. No multiplicative weighting, no learned model — each term is a
documented, testable boolean contribution.

Expected resulting bands (asserted loosely in tests, not hard-coded):
- Generic shadow fallback (LONG_UPPER/LOWER_SHADOW): **~0.35–0.55**
- Formal single-bar context pattern (HAMMER, SHOOTING_STAR, …): **~0.55–0.80**
- Two-bar reversal + context + volume (ENGULFING/PIERCING/DARK_CLOUD): **~0.70–0.95**

Both `SuggestedAdjustment` and `Confidence` are recorded in the sidecar so the validator can
later measure whether applying the adjustment would have helped — the evidence gate before
any P1 promotion into scoring.

### 9.3 ConfidenceReason codes + reconstructability (LOCKED)

Every evidence contribution is recorded as a `ConfidenceReason{Code, Delta}`, so
`Confidence == Σ Delta` (the §9.2 deltas keep the raw sum within [0,1], so `clamp01` is a
safety net and the equality holds — a test asserts `Σreasons == Confidence`). Codes:

| Code | Δ | When |
|---|---|---|
| `BASE_GEOMETRY` | +0.40 | always (a signal was emitted) |
| `CLEAR_BODY_RATIO` | +0.10 | defining ratio clearly beyond threshold |
| `STRONG_GEOMETRY` | +0.10 | structurally strong geometry / body relationship |
| `TREND_MATCH` | +0.15 | ContextMatched and prior trend supports the pattern |
| `NEAR_EXTREME` | +0.10 | ContextMatched and near-high/low supports the pattern |
| `STAGE_SUPPORT` | +0.05 | ContextMatched **and Stage DIRECTION supports the pattern's bias** (see §9.5) — **not** merely "Stage present" |
| `VOLUME_CONFIRM` | +0.10 | `HasVolumeRatio && VolumeRatio >= VolConfirm` |
| `WEAK_VOLUME` | −0.10 | `HasVolumeRatio && VolumeRatio < VolWeak` |
| `MISSING_CONTEXT` | −0.10 | `HasTrend == false` (unknown ≠ false, §6) |
| `GENERIC_FALLBACK` | −0.10 | generic shadow name despite KNOWN context |

`MISSING_CONTEXT` and `GENERIC_FALLBACK` are **mutually exclusive** (unknown-history →
MISSING_CONTEXT; known-but-unsupportive → GENERIC_FALLBACK).

**Volume gate (LOCKED):** volume evidence requires `HasVolumeRatio == true`. No volume data →
neither `VOLUME_CONFIRM` nor `WEAK_VOLUME`. Only `≥ VolConfirm` (1.5) confirms, only
`< VolWeak` (0.7) penalizes; the band in between adds nothing.

### 9.5 STAGE_SUPPORT gate (LOCKED)

`STAGE_SUPPORT` requires the injected Stage's DIRECTION to support the pattern's bias —
bullish reversals (HAMMER / INVERTED_HAMMER / BULLISH_ENGULFING / PIERCING_LINE) only when the
stage is weak / basing / bullish-reversal-consistent; bearish reversals (HANGING_MAN /
SHOOTING_STAR / BEARISH_ENGULFING / DARK_CLOUD_COVER) only when the stage is strong / high /
bearish-reversal-consistent. **A non-empty Stage alone MUST NOT add +0.05.**

**P0 status:** the candlestick package treats `Stage` as an opaque label and has no reliable
bullish/bearish mapping, so `stageSupportsPattern` returns false and `STAGE_SUPPORT` is NEVER
credited in P0. The code + delta are reserved; a real stage→direction mapping belongs at the
scanner-attach layer (which knows the RocketStage taxonomy) or in P1.

### 9.4 Strength (LOCKED — NOT round(confidence×5))

Derived from evidence completeness + geometry quality:
```
start 1  →  2 if geometry is clear or strong
         +1 per context corroboration: TREND_MATCH, NEAR_EXTREME, VOLUME_CONFIRM, STAGE_SUPPORT
         clamp to 5
```
So geometry-only → 1–2; +trend → 3; +trend+near+volume+stage → 5. Independent of Confidence
(a Confidence-0.75 read has Strength 3, not `round(3.75)=4`).

---

## 10. Types (public API)

```go
type Direction string // "BULLISH" | "BEARISH" | "NEUTRAL"

type PatternSignal struct {
	Type                PatternType
	Direction           Direction
	Confidence          float64            // [0,1]  == Σ ConfidenceReasons.Delta
	ConfidenceReasons   []ConfidenceReason // additive evidence trail (§9.3) — makes Confidence reconstructable
	Strength            int                // 1..5 (§9.4); NOT round(confidence×5)
	ContextMatched      bool               // §9.1 gate: SuggestedAdjustment non-zero only if true
	Geometry            []Geometry         // which geometries triggered it (provenance/debug)
	Description         string             // 中文 explanation
	SuggestedAdjustment int                // proposed delta (shadow research); gated by ContextMatched
	AppliedAdjustment   int                // ALWAYS 0 in P0 — the "never touches score" invariant
	BarOffset           int                // 0 = latest bar
}

type ConfidenceReason struct {
	Code  string  // §9.3 code
	Delta float64
}

type Result struct {
	AsOfDate string
	Context  MarketContext
	Signals  []PatternSignal // sorted by Confidence desc; empty when nothing fires
}

// Analyze runs the whole pipeline for a candle history (+ injected stage) and returns the
// Result for the latest bar-position. Pure, deterministic, never panics.
func Analyze(candles []Candle, stage string, th Thresholds) Result
```
`Candle` here is the minimal OHLCV the package needs; the scanner adapts `fetcher.Candle`.

---

## 11. Shadow-only integration

- New dedicated field on `WatchlistEntry`, **outside `ShadowSignals`**:
  ```go
  Candlestick *candlestick.Result `json:"candlestick,omitempty"` // enable_candlestick
  ```
- Computed in `EnrichWatchlist` behind `EnableCandlestick`, **AFTER `computeRocket`** (so the
  RocketStage can be injected as `MarketContext.Stage`) and **never fed back into it** —
  identical placement to BIAS (R10-1). Thin scanner glue in `internal/scanner/candlestick_attach.go`
  adapts `fetcher.Candle` → `candlestick.Candle` and passes `rk.Stage`.
- Config flags, default **false**: `enable_candlestick`, `show_candlestick`.
- **Guarantee**: no scoring/action/sort/Stage/Rocket/Momentum/VCP path reads
  `WatchlistEntry.Candlestick`. Provable structurally (post-rocket, dedicated field) and by
  grep, exactly as R10-1.

---

## 12. HTML section ⑬

Watchlist card, gated by `show_candlestick`, styled like ⑪/⑫:
- ⑬ K 線型態: list the latest signals — pattern (中文名 + Type), Direction, Confidence,
  Description, and a "建議調整（未套用）" note showing `SuggestedAdjustment` explicitly as
  non-applied.
- Summary chip when a high-confidence reversal fires (e.g. `🕯️ 吞噬（看多）信心 0.8`).
- Default output byte-identical when `show_candlestick=false`.

---

## 13. Structured sidecar (additive)

Extend `analysishistory` **additively** (SchemaVersion stays compatible; §20 of R10-1):
```go
type StockData struct {
	// … existing …
	Candlestick *CandlestickPayload `json:"candlestick,omitempty"`
}
type CandlestickPayload struct {
	Signals []CandlestickSignalPayload `json:"signals"`
	Context CandlestickContextPayload  `json:"context"` // trend/near/return/volume/stage/sufficient
}
type CandlestickSignalPayload struct {
	Type, Direction     string
	Confidence          float64
	ContextMatched      bool
	SuggestedAdjustment int // int (§1 correction) — no float serialization drift
	AppliedAdjustment   int // always 0
	// (raw enough for cohort group-by-pattern and adjustment back-testing)
}
```
Written from `cmd/scanner/main.go` when `EnableCandlestick` is on. Older sidecars lack the
field → readers see nil (never a zero pattern).

---

## 14. Cohort validation

Reuse `internal/validator` (SidecarStore + PriceStore.Outcome forward returns / MFE / MAE).
Add candlestick cohorts, e.g.:
- `BULLISH_ENGULFING` (Confidence ≥ t, Downtrend context) → measure T+1/3/5/10 win rate.
- `SHOOTING_STAR` near-high → measure whether a pullback followed (reduce-warning value).
Group by `PatternType` directly (that's why Marubozu is split). Also back-test
`SuggestedAdjustment` (did applying it improve outcomes?) — the evidence gate before any P1
scoring promotion.

---

## 15. P0 / P1 scope

**P0**: the 13 patterns; the full Metrics→Geometry→Context→Interpretation→Confidence
pipeline; multi-output geometry; attach + gated HTML + additive sidecar + cohort-ready data;
`SuggestedAdjustment` recorded but **not applied**; zero scoring impact.

**P1**: Morning/Evening Star, Harami, Dragonfly/Gravestone Doji, Spinning Top, Tweezer,
Three Soldiers/Crows; regime/volatility-aware thresholds; promotion of `SuggestedAdjustment`
into scoring behind a master guardrail flag (evidence-gated by §14).

---

## 16. Test matrix

Pure, deterministic, table-driven:
- **Geometry** (context-blind): synthetic bars → exact geometry set. e.g. small body/long
  lower shadow → {BearishBody|BullishBody, LongLowerShadow}; full body → {…, Marubozu};
  engulf/piercing/dark-cloud two-bar fixtures. **Assert multi-output** (a bar yields >1 tag).
- **Validation**: Range==0 (High==Low) → no geometry, no panic; High<Low guarded.
- **Interpretation**: same LongLowerShadow geometry → HAMMER (downtrend), HANGING_MAN
  (uptrend), LONG_LOWER_SHADOW (insufficient). Marubozu → BULLISH/BEARISH by body color
  (emitted even without context; then ContextMatched=false, SuggestedAdjustment=0).
- **Two-bar context gating (REQUIRED negative tests)** — geometry present but context absent
  must produce NO formal signal:
  - bullish-engulfing geometry + no downtrend/near-low → **no `BULLISH_ENGULFING` signal**
  - bearish-engulfing geometry + no uptrend/near-high → **no `BEARISH_ENGULFING` signal**
  - piercing geometry + neutral context → **no `PIERCING_LINE` signal**
  - dark-cloud geometry + neutral context → **no `DARK_CLOUD_COVER` signal**
  (each also asserts the raw two-bar geometry evidence is still detected internally).
- **Adjustment invariants**: `AppliedAdjustment == 0` for every emitted signal; non-zero
  `SuggestedAdjustment` ⇒ `ContextMatched == true` OR type ∈ {LONG_UPPER_SHADOW,
  LONG_LOWER_SHADOW}; `DOJI` adjustment always 0. Fields are `int`.
- **Context**: insufficient history → Sufficient=false → neutral names, capped confidence.
- **Determinism**: same input → identical output (no map-order flakiness in signal order).
- **Confidence** monotonicity (stronger geometry / aligned context ⇒ ≥ confidence).
- **Shadow guarantee**: with flags off, scanner output + sidecar byte-identical; grep proof
  that no scoring path reads `.Candlestick`.
- Sidecar round-trip; validator cohort group-by-pattern.

---

## 17. Files (planned)

```
internal/candlestick/{types,metrics,geometry,context,interpret,confidence,thresholds,analyzer}.go (+ _test.go each)
internal/scanner/candlestick_attach.go (+ _test)
internal/scanner/watchlist.go      # dedicated field + EnrichWatchlist hook
internal/scanner/scanner.go        # enable_candlestick / show_candlestick
internal/report/report.go          # section ⑬ + helpers (behind ShowCandlestick)
internal/analysishistory/model.go  # additive CandlestickPayload
internal/validator/cohort.go       # candlestick cohorts
cmd/scanner/main.go                # sidecar payload wiring
configs/config.yaml                # flags default false
```

---

## 19. Implementation phases (LOCKED — dependency order; stop after each)

Geometry's input is **always validated `Metrics`** — Geometry never touches raw OHLC or
validation. Therefore Types/Validation/Metrics come first, Geometry second.

```
Phase 1  Types + Thresholds + Validation + Metrics
          (candle validation, zero-range handling, metrics formulas, NaN/Inf behaviour, metrics unit tests)
Phase 2  Geometry Detection (multi-output, pure over validated Metrics)
Phase 3  Context Builder (MarketContext)
Phase 4  Interpretation (family semantics §7/§7.1, two-bar context gating)
Phase 5  Confidence / Strength / SuggestedAdjustment (§9)
Phase 6  Analyzer orchestration (Result)
Phase 7  Scanner attach + flags (enable/show_candlestick, dedicated field, post-rocket)
Phase 8  HTML gated output (⑬)
Phase 9  analysishistory sidecar (additive payload)
Phase 10 Validator cohort-ready extension
Phase 11 Wiring / config / docs
Phase 12 Full validation + static isolation proof (gofmt/vet/test + no-scoring grep)
```

**Per-phase protocol (MANDATORY):** after completing each phase I will (1) run that phase's
tests, (2) report **Added / Modified / Behavior / Tests / Compatibility / Open questions**,
(3) **STOP and wait for approval**, and (4) never enter the next phase on my own. Per-phase
git commits are NOT required; completing all phases before review is NOT allowed.

## 18. Locked decisions (no runtime discretion)
1. **SuggestedAdjustment magnitudes** — LOCKED in §9.1 table.
2. **Confidence formula** — LOCKED as the deterministic additive-evidence model in §9.2.
3. **Scan scope** — P0 analyzes the **latest bar-position only**; the sidecar records that
   one Result per stock per scan date. (Multi-position history is P1.)
4. **DOJI** — stays flat (single `DOJI` type, adjustment 0, Direction NEUTRAL) in P0; context
   sub-types (Dragonfly/Gravestone) are P1 and added at the Interpretation layer only.
5. **Invariant** — `AppliedAdjustment == 0` for every signal in P0; `SuggestedAdjustment`
   non-zero only when `ContextMatched == true`, except the generic shadow fallbacks (±2 with
   lower Confidence). DOJI always 0.
