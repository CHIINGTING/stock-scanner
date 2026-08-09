package candlestick

import (
	"math"
	"testing"
)

func hasReason(rs []ConfidenceReason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

func sumReasons(rs []ConfidenceReason) float64 {
	var s float64
	for _, r := range rs {
		s += r.Delta
	}
	return s
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// metrics fixtures reused across confidence tests.
func hammerStrong(t *testing.T) Metrics {
	return mustMetrics(t, Candle{Open: 7, High: 10, Low: 0, Close: 9})
} // lowerPct 0.7
func longLowerWeak(t *testing.T) Metrics {
	return mustMetrics(t, Candle{Open: 5.5, High: 10, Low: 0, Close: 8})
} // lowerPct 0.55
func longUpperStrong(t *testing.T) Metrics {
	return mustMetrics(t, Candle{Open: 3, High: 10, Low: 2.5, Close: 4})
} // upperPct high
func marubozuStrong(t *testing.T) Metrics {
	return mustMetrics(t, Candle{Open: 10, High: 20.2, Low: 9.8, Close: 20})
}

func TestConfidenceReconstructable(t *testing.T) {
	// Σ reasons == Confidence (no clamp in-band).
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if !approx(sumReasons(got.ConfidenceReasons), got.Confidence) {
		t.Fatalf("Σreasons %.4f != confidence %.4f", sumReasons(got.ConfidenceReasons), got.Confidence)
	}
	if !hasReason(got.ConfidenceReasons, ReasonBaseGeometry) {
		t.Errorf("must always include BASE_GEOMETRY")
	}
}

func TestConfidenceGeometryOnly(t *testing.T) {
	// generic fallback, weak geometry, known-neutral context → BASE + GENERIC_FALLBACK.
	sig := PatternSignal{Type: PatternLongLowerShadow, ContextMatched: false}
	got := ScoreSignal(sig, longLowerWeak(t), nil, MarketContext{HasTrend: true}, DefaultThresholds())
	if !approx(got.Confidence, 0.30) {
		t.Errorf("confidence = %.3f, want 0.30", got.Confidence)
	}
	if got.Strength != 1 {
		t.Errorf("strength = %d, want 1 (geometry-only, weak)", got.Strength)
	}
	if !hasReason(got.ConfidenceReasons, ReasonGenericFallback) || hasReason(got.ConfidenceReasons, ReasonMissingContext) {
		t.Errorf("known-neutral generic must be GENERIC_FALLBACK, not MISSING_CONTEXT: %+v", got.ConfidenceReasons)
	}
}

func TestConfidenceGeometryTrend(t *testing.T) {
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	// BASE .40 + CLEAR .10 + STRONG .10 + TREND .15 = 0.75
	if !approx(got.Confidence, 0.75) {
		t.Errorf("confidence = %.3f, want 0.75", got.Confidence)
	}
	if got.Strength != 3 {
		t.Errorf("strength = %d, want 3 (geometry+trend)", got.Strength)
	}
}

func TestConfidenceGeometryTrendNear(t *testing.T) {
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true, HasNearLow: true, Near20DLow: true}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if !approx(got.Confidence, 0.85) { // +NEAR .10
		t.Errorf("confidence = %.3f, want 0.85", got.Confidence)
	}
	if got.Strength != 4 {
		t.Errorf("strength = %d, want 4", got.Strength)
	}
}

func TestConfidenceGeometryTrendNearVolume(t *testing.T) {
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true, HasNearLow: true, Near20DLow: true, HasVolumeRatio: true, VolumeRatio: 2.0}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if !approx(got.Confidence, 0.95) { // +VOLUME .10
		t.Errorf("confidence = %.3f, want 0.95", got.Confidence)
	}
	if got.Strength != 5 {
		t.Errorf("strength = %d, want 5", got.Strength)
	}
	// Stage present but P0 has no reliable bullish/bearish stage mapping → NO STAGE_SUPPORT
	// credit; confidence/strength unchanged (not 1.00).
	ctx.Stage = "MAIN_RUN"
	got2 := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if !approx(got2.Confidence, 0.95) {
		t.Errorf("stage presence must NOT add STAGE_SUPPORT in P0: conf=%.3f, want 0.95", got2.Confidence)
	}
	if hasReason(got2.ConfidenceReasons, ReasonStageSupport) {
		t.Errorf("STAGE_SUPPORT must not be credited for a mere non-empty Stage")
	}
}

func TestConfidenceNoVolumeData(t *testing.T) {
	// HasVolumeRatio=false → neither VOLUME_CONFIRM nor WEAK_VOLUME (even if VolumeRatio field
	// happens to hold a value).
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true, HasVolumeRatio: false, VolumeRatio: 9.9}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if hasReason(got.ConfidenceReasons, ReasonVolumeConfirm) || hasReason(got.ConfidenceReasons, ReasonWeakVolume) {
		t.Errorf("no volume data must add neither volume reason: %+v", got.ConfidenceReasons)
	}
}

func TestConfidenceMissingTrendVsGeneric(t *testing.T) {
	// Unknown trend history (HasTrend false) → MISSING_CONTEXT (not GENERIC_FALLBACK). §6.
	sig := PatternSignal{Type: PatternLongLowerShadow, ContextMatched: false}
	got := ScoreSignal(sig, longLowerWeak(t), nil, MarketContext{}, DefaultThresholds())
	if !hasReason(got.ConfidenceReasons, ReasonMissingContext) {
		t.Errorf("HasTrend=false must yield MISSING_CONTEXT: %+v", got.ConfidenceReasons)
	}
	if hasReason(got.ConfidenceReasons, ReasonGenericFallback) {
		t.Errorf("MISSING_CONTEXT and GENERIC_FALLBACK are mutually exclusive")
	}
}

func TestConfidenceMissingNearHigh(t *testing.T) {
	// Bearish reversal in uptrend but no near-high data → TREND_MATCH yes, NEAR_EXTREME no.
	sig := PatternSignal{Type: PatternShootingStar, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Uptrend: true, HasNearHigh: false, Near20DHigh: false}
	got := ScoreSignal(sig, longUpperStrong(t), nil, ctx, DefaultThresholds())
	if !hasReason(got.ConfidenceReasons, ReasonTrendMatch) {
		t.Errorf("expected TREND_MATCH")
	}
	if hasReason(got.ConfidenceReasons, ReasonNearExtreme) {
		t.Errorf("missing near-high must NOT credit NEAR_EXTREME")
	}
}

func TestConfidenceWeakVolume(t *testing.T) {
	sig := PatternSignal{Type: PatternHammer, ContextMatched: true}
	ctx := MarketContext{HasTrend: true, Downtrend: true, HasVolumeRatio: true, VolumeRatio: 0.5}
	got := ScoreSignal(sig, hammerStrong(t), nil, ctx, DefaultThresholds())
	if !hasReason(got.ConfidenceReasons, ReasonWeakVolume) {
		t.Errorf("VolumeRatio<VolWeak must add WEAK_VOLUME: %+v", got.ConfidenceReasons)
	}
	if hasReason(got.ConfidenceReasons, ReasonVolumeConfirm) {
		t.Errorf("weak volume must not also confirm")
	}
}

func TestClamp01(t *testing.T) {
	cases := []struct{ in, want float64 }{{-0.5, 0}, {1.5, 1}, {0, 0}, {1, 1}, {0.42, 0.42}}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Errorf("clamp01(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAdjustmentIndependenceFromConfidence(t *testing.T) {
	// Two HAMMER reads with very different confidence → same SuggestedAdjustment +4.
	hi := ScoreSignal(PatternSignal{Type: PatternHammer, ContextMatched: true}, hammerStrong(t), nil,
		MarketContext{HasTrend: true, Downtrend: true, HasNearLow: true, Near20DLow: true, HasVolumeRatio: true, VolumeRatio: 2}, DefaultThresholds())
	lo := ScoreSignal(PatternSignal{Type: PatternHammer, ContextMatched: true}, hammerStrong(t), nil,
		MarketContext{HasTrend: true, Downtrend: true}, DefaultThresholds())
	if hi.Confidence == lo.Confidence {
		t.Fatalf("test setup: confidences should differ")
	}
	if hi.SuggestedAdjustment != 4 || lo.SuggestedAdjustment != 4 {
		t.Errorf("HAMMER adjustment must always be +4 regardless of confidence: hi=%d lo=%d", hi.SuggestedAdjustment, lo.SuggestedAdjustment)
	}
	if hi.AppliedAdjustment != 0 || lo.AppliedAdjustment != 0 {
		t.Errorf("AppliedAdjustment must always be 0")
	}
}

func TestAdjustmentGatingRules(t *testing.T) {
	th := DefaultThresholds()
	// generic fallback keeps ±2 despite ContextMatched=false
	g := ScoreSignal(PatternSignal{Type: PatternLongLowerShadow, ContextMatched: false}, longLowerWeak(t), nil, MarketContext{HasTrend: true}, th)
	if g.SuggestedAdjustment != 2 {
		t.Errorf("LONG_LOWER_SHADOW adjustment = %d, want +2 (exception)", g.SuggestedAdjustment)
	}
	// DOJI always 0
	d := ScoreSignal(PatternSignal{Type: PatternDoji, ContextMatched: false}, mustMetrics(t, Candle{Open: 10, High: 11, Low: 9, Close: 10.02}), nil, MarketContext{HasTrend: true}, th)
	if d.SuggestedAdjustment != 0 {
		t.Errorf("DOJI adjustment = %d, want 0", d.SuggestedAdjustment)
	}
	// marubozu with ContextMatched=false → 0; with true → +3
	m0 := ScoreSignal(PatternSignal{Type: PatternBullishMarubozu, ContextMatched: false}, marubozuStrong(t), nil, MarketContext{HasTrend: true}, th)
	if m0.SuggestedAdjustment != 0 {
		t.Errorf("bullish marubozu (no context) adjustment = %d, want 0", m0.SuggestedAdjustment)
	}
	m1 := ScoreSignal(PatternSignal{Type: PatternBullishMarubozu, ContextMatched: true}, marubozuStrong(t), nil, MarketContext{HasTrend: true, Uptrend: true}, th)
	if m1.SuggestedAdjustment != 3 {
		t.Errorf("bullish marubozu (context) adjustment = %d, want +3", m1.SuggestedAdjustment)
	}
}

func TestStrengthIndependentOfConfidence(t *testing.T) {
	// Confidence 0.75 → round(0.75*5)=4, but Strength is 3 (geometry+trend). Proves Strength
	// is NOT round(confidence*5).
	got := ScoreSignal(PatternSignal{Type: PatternHammer, ContextMatched: true}, hammerStrong(t), nil,
		MarketContext{HasTrend: true, Downtrend: true}, DefaultThresholds())
	if !approx(got.Confidence, 0.75) {
		t.Fatalf("setup: confidence %.3f", got.Confidence)
	}
	if r := int(math.Round(got.Confidence * 5)); got.Strength == r {
		t.Errorf("strength %d must not equal round(conf*5)=%d", got.Strength, r)
	}
	if got.Strength != 3 {
		t.Errorf("strength = %d, want 3", got.Strength)
	}
}

func TestEngulfQualityUsesPrev(t *testing.T) {
	prev := mustMetrics(t, Candle{Open: 12, High: 12.5, Low: 9.5, Close: 10}) // body 2
	cur := mustMetrics(t, Candle{Open: 9, High: 13.5, Low: 8.5, Close: 13})   // body 4 → ratio 2.0
	got := ScoreSignal(PatternSignal{Type: PatternBullishEngulfing, ContextMatched: true}, cur, &prev,
		MarketContext{HasTrend: true, Downtrend: true}, DefaultThresholds())
	if !hasReason(got.ConfidenceReasons, ReasonClearBodyRatio) || !hasReason(got.ConfidenceReasons, ReasonStrongGeometry) {
		t.Errorf("strong engulf (ratio 2.0) should be clear+strong: %+v", got.ConfidenceReasons)
	}
	if got.SuggestedAdjustment != 6 {
		t.Errorf("bullish engulfing adjustment = %d, want +6", got.SuggestedAdjustment)
	}
}
