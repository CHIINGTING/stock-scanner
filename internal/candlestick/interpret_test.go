package candlestick

import (
	"strings"
	"testing"
)

// single interprets a single-bar geometry set under a context.
func interpSingle(geo []Geometry, ctx MarketContext) (PatternSignal, bool) {
	return InterpretSingleBar(geo, ctx)
}

var (
	ctxDown    = MarketContext{HasTrend: true, Downtrend: true}
	ctxUp      = MarketContext{HasTrend: true, Uptrend: true}
	ctxNeutral = MarketContext{HasTrend: true} // known, but neither up nor down, no near flags
	ctxUnknown = MarketContext{}               // insufficient history
)

func wantSignal(t *testing.T, s PatternSignal, ok bool, typ PatternType, dir Direction, ctxMatched bool) {
	t.Helper()
	if !ok {
		t.Fatalf("expected a signal of type %s, got none", typ)
	}
	if s.Type != typ {
		t.Fatalf("Type = %s, want %s", s.Type, typ)
	}
	if s.Direction != dir {
		t.Errorf("Direction = %s, want %s", s.Direction, dir)
	}
	if s.ContextMatched != ctxMatched {
		t.Errorf("ContextMatched = %v, want %v", s.ContextMatched, ctxMatched)
	}
	if s.Confidence != 0 || s.SuggestedAdjustment != 0 || s.AppliedAdjustment != 0 {
		t.Errorf("Phase-4 placeholders must be 0: conf=%v sug=%d app=%d", s.Confidence, s.SuggestedAdjustment, s.AppliedAdjustment)
	}
	if s.Description == "" {
		t.Errorf("Description must be set")
	}
}

func TestInterpretHammer(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, ctxDown)
	wantSignal(t, s, ok, PatternHammer, DirectionBullish, true)
}

func TestInterpretHangingMan(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBearishBody, GeometryLongLowerShadow}, ctxUp)
	wantSignal(t, s, ok, PatternHangingMan, DirectionBearish, true)
}

func TestInterpretInvertedHammer(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongUpperShadow}, ctxDown)
	wantSignal(t, s, ok, PatternInvertedHammer, DirectionBullish, true)
}

func TestInterpretShootingStar(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBearishBody, GeometryLongUpperShadow}, ctxUp)
	wantSignal(t, s, ok, PatternShootingStar, DirectionBearish, true)
}

func TestInterpretGenericLongUpperFallback(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongUpperShadow}, ctxNeutral)
	wantSignal(t, s, ok, PatternLongUpperShadow, DirectionNeutral, false)
}

func TestInterpretGenericLongLowerFallback(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, ctxNeutral)
	wantSignal(t, s, ok, PatternLongLowerShadow, DirectionNeutral, false)
}

func TestInterpretDoji(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryDoji}, ctxUp)
	wantSignal(t, s, ok, PatternDoji, DirectionNeutral, false)
}

func TestInterpretDojiSuppressesShadow(t *testing.T) {
	// Dragonfly: Doji + LongLowerShadow in a downtrend → DOJI (not HAMMER) in P0.
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryDoji, GeometryLongLowerShadow}, ctxDown)
	wantSignal(t, s, ok, PatternDoji, DirectionNeutral, false)
}

func TestInterpretBullishMarubozuContext(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryMarubozu}, ctxUp)
	wantSignal(t, s, ok, PatternBullishMarubozu, DirectionBullish, true)
}

func TestInterpretBullishMarubozuNoContext(t *testing.T) {
	// Emitted even without trend agreement; ContextMatched=false + position-risk note.
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryMarubozu}, ctxNeutral)
	wantSignal(t, s, ok, PatternBullishMarubozu, DirectionBullish, false)
	if !strings.Contains(s.Description, "位置風險") {
		t.Errorf("no-context marubozu should note position risk: %q", s.Description)
	}
}

func TestInterpretBearishMarubozu(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBearishBody, GeometryMarubozu}, ctxDown)
	wantSignal(t, s, ok, PatternBearishMarubozu, DirectionBearish, true)
}

func TestInterpretGenericBodyIsNotASignal(t *testing.T) {
	// Only a plain body color → NO single-bar signal.
	if _, ok := interpSingle([]Geometry{GeometryBullishBody}, ctxUp); ok {
		t.Errorf("plain BullishBody must not produce a signal")
	}
}

// ── two-bar context gate ─────────────────────────────────────────────────────

func TestInterpretBullishEngulfingGatedPositive(t *testing.T) {
	s, ok := InterpretTwoBar([]Geometry{GeometryBullishEngulf}, ctxDown)
	wantSignal(t, s, ok, PatternBullishEngulfing, DirectionBullish, true)
}

func TestInterpretBullishEngulfingGatedNegative(t *testing.T) {
	if _, ok := InterpretTwoBar([]Geometry{GeometryBullishEngulf}, ctxNeutral); ok {
		t.Errorf("bullish engulf without downtrend/near-low must NOT emit a signal")
	}
	if _, ok := InterpretTwoBar([]Geometry{GeometryBullishEngulf}, ctxUnknown); ok {
		t.Errorf("bullish engulf with unknown context must NOT emit a signal")
	}
}

func TestInterpretBearishEngulfingGatedNegative(t *testing.T) {
	if _, ok := InterpretTwoBar([]Geometry{GeometryBearishEngulf}, ctxNeutral); ok {
		t.Errorf("bearish engulf without uptrend/near-high must NOT emit a signal")
	}
}

func TestInterpretPiercingGated(t *testing.T) {
	if _, ok := InterpretTwoBar([]Geometry{GeometryPiercing}, ctxNeutral); ok {
		t.Errorf("piercing without downtrend must NOT emit a signal")
	}
	s, ok := InterpretTwoBar([]Geometry{GeometryPiercing}, ctxDown)
	wantSignal(t, s, ok, PatternPiercingLine, DirectionBullish, true)
}

func TestInterpretDarkCloudGated(t *testing.T) {
	if _, ok := InterpretTwoBar([]Geometry{GeometryDarkCloud}, ctxNeutral); ok {
		t.Errorf("dark cloud without uptrend must NOT emit a signal")
	}
	s, ok := InterpretTwoBar([]Geometry{GeometryDarkCloud}, ctxUp)
	wantSignal(t, s, ok, PatternDarkCloudCover, DirectionBearish, true)
}

// ── context conflict + unknown fallback ──────────────────────────────────────

func TestInterpretContextConflictFallback(t *testing.T) {
	// Downtrend AND Near20DHigh (contradiction) → generic fallback, NOT HAMMER.
	conflict := MarketContext{HasTrend: true, Downtrend: true, HasNearHigh: true, Near20DHigh: true}
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, conflict)
	wantSignal(t, s, ok, PatternLongLowerShadow, DirectionNeutral, false)
}

func TestInterpretUnknownContextFallback(t *testing.T) {
	s, ok := interpSingle([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, ctxUnknown)
	wantSignal(t, s, ok, PatternLongLowerShadow, DirectionNeutral, false)
}

// ── multi-signal policy ──────────────────────────────────────────────────────

func TestInterpretMaxTwoSignals(t *testing.T) {
	// Latest bar is a long-lower-shadow in a downtrend (→ HAMMER) AND the two bars form a
	// bullish engulfing (→ BULLISH_ENGULFING). Both allowed; two-bar first; ≤ 2; no dup type.
	out := Interpret(
		[]Geometry{GeometryBullishBody, GeometryLongLowerShadow},
		[]Geometry{GeometryBullishEngulf},
		ctxDown,
	)
	if len(out) != 2 {
		t.Fatalf("want 2 signals, got %d: %+v", len(out), out)
	}
	if out[0].Type != PatternBullishEngulfing {
		t.Errorf("two-bar must come first, got %s", out[0].Type)
	}
	if out[1].Type != PatternHammer {
		t.Errorf("single-bar second, got %s", out[1].Type)
	}
	seen := map[PatternType]bool{}
	for _, s := range out {
		if seen[s.Type] {
			t.Errorf("duplicate PatternType %s", s.Type)
		}
		seen[s.Type] = true
	}
}

func TestInterpretFormalCoversGenericFallback(t *testing.T) {
	// A HAMMER context must NOT also yield LONG_LOWER_SHADOW (single-bar emits exactly one).
	out := Interpret([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, nil, ctxDown)
	if len(out) != 1 || out[0].Type != PatternHammer {
		t.Fatalf("want exactly [HAMMER], got %+v", out)
	}
}

func TestInterpretNoTwoBarWhenGatedOut(t *testing.T) {
	// Gated-out two-bar + a generic single-bar → only the generic single-bar signal.
	out := Interpret([]Geometry{GeometryBullishBody, GeometryLongLowerShadow}, []Geometry{GeometryBullishEngulf}, ctxNeutral)
	if len(out) != 1 || out[0].Type != PatternLongLowerShadow {
		t.Fatalf("want exactly [LONG_LOWER_SHADOW], got %+v", out)
	}
}
