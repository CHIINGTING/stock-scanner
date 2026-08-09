package candlestick

// confidence.go — Layer 7: Confidence / Strength / SuggestedAdjustment. All deterministic:
// no weight search, no heuristic tuning, no probability model, no randomness.
//
//   - Confidence uses the LOCKED additive-evidence model (SPEC §9.2). Every contribution is
//     recorded as a ConfidenceReason so Confidence == Σ Delta is fully reconstructable from
//     (Geometry, Context, ConfidenceReasons) with NO hidden weights.
//   - Strength is derived from evidence completeness + geometry quality — NOT round(conf*5).
//   - SuggestedAdjustment is the fixed §9.1 baseline, gated by ContextMatched (generic shadow
//     fallbacks keep ±2; DOJI is always 0). It is DECOUPLED from Confidence/Strength — a
//     HAMMER is always +4 regardless of how confident/strong the read is.

// Confidence evidence deltas (LOCKED §9.2).
const (
	confBaseGeometry     = 0.40
	confClearGeometry    = 0.10
	confStrongGeometry   = 0.10
	confTrendMatch       = 0.15
	confNearExtreme      = 0.10
	confStageSupport     = 0.05
	confVolumeConfirm    = 0.10
	confWeakVolume       = -0.10
	confInsufficientHist = -0.10 // MISSING_CONTEXT (unknown trend history)
	confGenericFallback  = -0.10 // GENERIC_FALLBACK (context known but unsupportive)
)

// ConfidenceReason codes (LOCKED — tests key on codes, never on human-readable sentences).
const (
	ReasonBaseGeometry    = "BASE_GEOMETRY"
	ReasonClearBodyRatio  = "CLEAR_BODY_RATIO"
	ReasonStrongGeometry  = "STRONG_GEOMETRY"
	ReasonTrendMatch      = "TREND_MATCH"
	ReasonNearExtreme     = "NEAR_EXTREME"
	ReasonStageSupport    = "STAGE_SUPPORT"
	ReasonVolumeConfirm   = "VOLUME_CONFIRM"
	ReasonWeakVolume      = "WEAK_VOLUME"
	ReasonMissingContext  = "MISSING_CONTEXT"  // §6: unknown ≠ false (HasTrend == false)
	ReasonGenericFallback = "GENERIC_FALLBACK" // generic shadow name despite known context
)

// Geometry-quality margins (BASELINE — not calibrated).
const (
	qualClearShadowMargin = 0.15 // long-shadow pct must beat LongShadowMin by this → CLEAR
	qualStrongShadowPct   = 0.70 // long-shadow pct >= this → STRONG
	qualClearMarubozuAdd  = 0.05 // BodyPct >= MarubozuBodyMin + this → CLEAR
	qualStrongMarubozuSh  = 0.04 // both shadows <= this → STRONG
	qualClearDojiFactor   = 0.50 // BodyPct <= DojiBodyMax * this → CLEAR
	qualStrongDojiFactor  = 0.20 // BodyPct <= DojiBodyMax * this → STRONG
	qualClearEngulf       = 1.30 // cur.Body / prev.Body >= this → CLEAR
	qualStrongEngulf      = 2.00 // >= this → STRONG
	qualClearPenetration  = 0.60 // piercing/dark-cloud penetration >= this → CLEAR
	qualStrongPenetration = 0.75 // >= this → STRONG
)

// baseAdjustment is the fixed §9.1 SuggestedAdjustment baseline (shadow research only).
var baseAdjustment = map[PatternType]int{
	PatternDoji:             0,
	PatternLongUpperShadow:  -2,
	PatternLongLowerShadow:  2,
	PatternHammer:           4,
	PatternHangingMan:       -3,
	PatternInvertedHammer:   3,
	PatternShootingStar:     -5,
	PatternBullishMarubozu:  3,
	PatternBearishMarubozu:  -3,
	PatternBullishEngulfing: 6,
	PatternBearishEngulfing: -6,
	PatternPiercingLine:     4,
	PatternDarkCloudCover:   -4,
}

// ScoreSignal fills Confidence / ConfidenceReasons / Strength / SuggestedAdjustment /
// AppliedAdjustment for an interpreted signal. m is the CURRENT (latest) bar metrics; prev is
// the previous bar metrics (nil for single-bar signals). It never mutates its inputs beyond
// the returned copy.
func ScoreSignal(sig PatternSignal, m Metrics, prev *Metrics, ctx MarketContext, th Thresholds) PatternSignal {
	th = th.Defaulted()

	clear, strong := geometryQuality(sig.Type, m, prev, th)
	trendMatch := sig.ContextMatched && contextTrendMatch(sig.Type, ctx)
	nearMatch := sig.ContextMatched && contextNearMatch(sig.Type, ctx)
	stageSupport := sig.ContextMatched && stageSupportsPattern(sig.Type, ctx.Stage)
	// Volume evidence requires HasVolumeRatio: no data → neither confirm nor penalize.
	volConfirm := ctx.HasVolumeRatio && ctx.VolumeRatio >= th.VolConfirm
	weakVol := ctx.HasVolumeRatio && ctx.VolumeRatio < th.VolWeak

	var reasons []ConfidenceReason
	add := func(code string, delta float64) {
		reasons = append(reasons, ConfidenceReason{Code: code, Delta: delta})
	}

	add(ReasonBaseGeometry, confBaseGeometry)
	if clear {
		add(ReasonClearBodyRatio, confClearGeometry)
	}
	if strong {
		add(ReasonStrongGeometry, confStrongGeometry)
	}
	if trendMatch {
		add(ReasonTrendMatch, confTrendMatch)
	}
	if nearMatch {
		add(ReasonNearExtreme, confNearExtreme)
	}
	if stageSupport {
		add(ReasonStageSupport, confStageSupport)
	}
	if volConfirm {
		add(ReasonVolumeConfirm, confVolumeConfirm)
	} else if weakVol {
		add(ReasonWeakVolume, confWeakVolume)
	}
	// Missing-context vs generic-fallback are mutually exclusive (SPEC §6/§9.2): an unknown
	// trend history is MISSING_CONTEXT; a known-but-unsupportive context that yielded a
	// generic shadow name is GENERIC_FALLBACK.
	if !ctx.HasTrend {
		add(ReasonMissingContext, confInsufficientHist)
	} else if isGenericFallback(sig.Type) {
		add(ReasonGenericFallback, confGenericFallback)
	}

	var sum float64
	for _, r := range reasons {
		sum += r.Delta
	}
	sig.Confidence = clamp01(sum)
	sig.ConfidenceReasons = reasons
	sig.Strength = computeStrength(clear, strong, trendMatch, nearMatch, volConfirm, stageSupport)
	sig.SuggestedAdjustment = adjustmentFor(sig.Type, sig.ContextMatched)
	sig.AppliedAdjustment = 0
	return sig
}

// computeStrength derives 1..5 from evidence completeness + geometry quality (NOT from
// Confidence). Geometry-only → 1 (2 if the geometry is clear/strong); each context
// corroboration (+trend/+near/+volume/+stage) adds one, capped at 5.
func computeStrength(clear, strong, trendMatch, nearMatch, volConfirm, stageSupport bool) int {
	s := 1
	if clear || strong {
		s = 2
	}
	for _, ok := range []bool{trendMatch, nearMatch, volConfirm, stageSupport} {
		if ok {
			s++
		}
	}
	if s > 5 {
		s = 5
	}
	return s
}

// adjustmentFor returns the fixed §9.1 baseline, gated by ContextMatched. DOJI is always 0;
// generic shadow fallbacks keep ±2 despite ContextMatched=false (the documented exception);
// every other pattern yields 0 unless ContextMatched. Never scaled by Confidence/Strength.
func adjustmentFor(t PatternType, ctxMatched bool) int {
	switch {
	case t == PatternDoji:
		return 0
	case isGenericFallback(t):
		return baseAdjustment[t] // ±2 exception
	case !ctxMatched:
		return 0
	default:
		return baseAdjustment[t]
	}
}

func isGenericFallback(t PatternType) bool {
	return t == PatternLongUpperShadow || t == PatternLongLowerShadow
}

// IsGenericFallback reports whether a PatternType is a generic (context-less) shadow
// fallback (LONG_UPPER_SHADOW / LONG_LOWER_SHADOW). Exported purely so a renderer can label
// it "Generic Pattern" WITHOUT switching on individual type names (keeps presentation
// type-agnostic). Read-only query — no analyzer behavior.
func IsGenericFallback(t PatternType) bool { return isGenericFallback(t) }

// stageSupportsPattern reports whether the injected Stage DIRECTION supports the pattern's
// bullish/bearish bias (bullish reversals need a weak/basing stage; bearish reversals a
// strong/high stage). In P0 the candlestick package treats Stage as an OPAQUE label and has
// NO reliable bullish/bearish mapping, so this always returns false — STAGE_SUPPORT is never
// credited merely because a Stage is present. A real stage→direction mapping belongs at the
// scanner-attach layer (which knows the RocketStage taxonomy) or in P1; the STAGE_SUPPORT
// code + delta are reserved for then.
func stageSupportsPattern(_ PatternType, _ Stage) bool { return false }

// contextTrendMatch reports whether the prior trend supports this pattern (reversals want the
// OPPOSITE trend they reverse; marubozu continuations want the SAME direction).
func contextTrendMatch(t PatternType, ctx MarketContext) bool {
	switch t {
	case PatternHammer, PatternInvertedHammer, PatternBullishEngulfing, PatternPiercingLine:
		return ctx.Downtrend
	case PatternHangingMan, PatternShootingStar, PatternBearishEngulfing, PatternDarkCloudCover:
		return ctx.Uptrend
	case PatternBullishMarubozu:
		return ctx.Uptrend
	case PatternBearishMarubozu:
		return ctx.Downtrend
	default:
		return false
	}
}

// contextNearMatch reports whether a near-extreme supports this pattern.
func contextNearMatch(t PatternType, ctx MarketContext) bool {
	switch t {
	case PatternHammer, PatternInvertedHammer, PatternBullishEngulfing, PatternPiercingLine:
		return ctx.Near20DLow
	case PatternHangingMan, PatternShootingStar, PatternBearishEngulfing, PatternDarkCloudCover:
		return ctx.Near20DHigh
	case PatternBullishMarubozu:
		return ctx.Near20DHigh
	case PatternBearishMarubozu:
		return ctx.Near20DLow
	default:
		return false
	}
}

// geometryQuality reports whether the pattern's defining geometry clearly exceeds its
// threshold (clear) and/or is structurally strong (strong). Deterministic, per family.
func geometryQuality(t PatternType, m Metrics, prev *Metrics, th Thresholds) (clear, strong bool) {
	switch t {
	case PatternHammer, PatternHangingMan, PatternLongLowerShadow:
		clear = m.LowerPct >= th.LongShadowMin+qualClearShadowMargin
		strong = m.LowerPct >= qualStrongShadowPct
	case PatternInvertedHammer, PatternShootingStar, PatternLongUpperShadow:
		clear = m.UpperPct >= th.LongShadowMin+qualClearShadowMargin
		strong = m.UpperPct >= qualStrongShadowPct
	case PatternBullishMarubozu, PatternBearishMarubozu:
		clear = m.BodyPct >= th.MarubozuBodyMin+qualClearMarubozuAdd
		strong = m.UpperPct <= qualStrongMarubozuSh && m.LowerPct <= qualStrongMarubozuSh
	case PatternDoji:
		clear = m.BodyPct <= th.DojiBodyMax*qualClearDojiFactor
		strong = m.BodyPct <= th.DojiBodyMax*qualStrongDojiFactor
	case PatternBullishEngulfing, PatternBearishEngulfing:
		if prev != nil && prev.Body > 0 {
			r := m.Body / prev.Body
			clear = r >= qualClearEngulf
			strong = r >= qualStrongEngulf
		}
	case PatternPiercingLine, PatternDarkCloudCover:
		if prev != nil {
			p := penetration(t, m, *prev)
			clear = p >= qualClearPenetration
			strong = p >= qualStrongPenetration
		}
	}
	return
}

// penetration is how deep the current close reaches into the prior body (0..1+), for
// piercing (up into a prior down body) / dark cloud (down into a prior up body).
func penetration(t PatternType, m Metrics, prev Metrics) float64 {
	switch t {
	case PatternPiercingLine:
		denom := prev.Open - prev.Close // prior bearish body height
		if denom <= 0 {
			return 0
		}
		return (m.Close - prev.Close) / denom
	case PatternDarkCloudCover:
		denom := prev.Close - prev.Open // prior bullish body height
		if denom <= 0 {
			return 0
		}
		return (prev.Close - m.Close) / denom
	}
	return 0
}

// clamp01 constrains x to [0,1]. The §9.2 deltas keep the raw sum within [0,1] in practice,
// so this is a safety net (and the Σreasons==Confidence invariant holds because no clamp
// fires); it is unit-tested directly for <0 / >1 / exact 0 / exact 1.
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
