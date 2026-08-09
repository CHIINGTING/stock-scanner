package candlestick

import (
	"testing"
	"time"
)

// declining builds n plain declining bars from `start` stepping down by `step`.
func declining(start, step float64, n int) []Candle {
	out := make([]Candle, n)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		c := start - step*float64(i)
		out[i] = Candle{Date: base.AddDate(0, 0, i), Open: c + 0.3, High: c + 0.6, Low: c - 0.6, Close: c, Volume: 1000}
	}
	return out
}

func findSignal(res Result, typ PatternType) (PatternSignal, bool) {
	for _, s := range res.Signals {
		if s.Type == typ {
			return s, true
		}
	}
	return PatternSignal{}, false
}

func TestAnalyzeHammerInDowntrend(t *testing.T) {
	cs := declining(130, 2, 11) // 11 declining bars → strong downtrend
	last := cs[len(cs)-1]
	// Replace the latest bar with a hammer (small body near top, long lower shadow).
	// small-but-real body (bodyPct ~0.19, not doji) + long lower shadow
	hammer := Candle{Date: last.Date.AddDate(0, 0, 1), Open: 108, High: 109.2, Low: 104, Close: 109, Volume: 1000}
	cs = append(cs, hammer)

	res := Analyze(cs, "", DefaultThresholds())
	s, ok := findSignal(res, PatternHammer)
	if !ok {
		t.Fatalf("expected HAMMER; got %+v", res.Signals)
	}
	if s.Direction != DirectionBullish || !s.ContextMatched {
		t.Errorf("hammer should be bullish + context-matched: %+v", s)
	}
	if s.SuggestedAdjustment != 4 || s.AppliedAdjustment != 0 {
		t.Errorf("hammer adjustment = %d (applied %d), want 4 / 0", s.SuggestedAdjustment, s.AppliedAdjustment)
	}
	if s.Confidence <= 0 {
		t.Errorf("confidence should be > 0")
	}
	if res.AsOfDate != hammer.Date.Format("2006-01-02") {
		t.Errorf("AsOfDate = %q", res.AsOfDate)
	}
	if !res.Context.Downtrend {
		t.Errorf("context should be downtrend")
	}
}

func TestAnalyzeGenericFallbackShortHistory(t *testing.T) {
	// Only 3 bars → insufficient history → long-lower-shadow latest becomes generic
	// LONG_LOWER_SHADOW with MISSING_CONTEXT, not HAMMER.
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cs := []Candle{
		{Date: base, Open: 100, High: 101, Low: 99, Close: 100},
		{Date: base.AddDate(0, 0, 1), Open: 100, High: 101, Low: 99, Close: 100},
		{Date: base.AddDate(0, 0, 2), Open: 99.5, High: 100.5, Low: 96, Close: 100.3}, // long lower shadow (bodyPct ~0.18)
	}
	res := Analyze(cs, "", DefaultThresholds())
	s, ok := findSignal(res, PatternLongLowerShadow)
	if !ok {
		t.Fatalf("expected LONG_LOWER_SHADOW; got %+v", res.Signals)
	}
	if s.ContextMatched {
		t.Errorf("generic fallback must not be context-matched")
	}
	if !hasReason(s.ConfidenceReasons, ReasonMissingContext) {
		t.Errorf("short history must add MISSING_CONTEXT: %+v", s.ConfidenceReasons)
	}
	if _, isHammer := findSignal(res, PatternHammer); isHammer {
		t.Errorf("must NOT be HAMMER on unknown context")
	}
}

func TestAnalyzeDegenerateLatestBar(t *testing.T) {
	cs := declining(130, 2, 11)
	// latest is a flat/limit-locked bar (zero range) → no geometry, no signals, context kept.
	last := cs[len(cs)-1]
	cs = append(cs, Candle{Date: last.Date.AddDate(0, 0, 1), Open: 108, High: 108, Low: 108, Close: 108, Volume: 1000})
	res := Analyze(cs, "", DefaultThresholds())
	if len(res.Signals) != 0 {
		t.Errorf("degenerate latest bar must yield no signals, got %+v", res.Signals)
	}
	if !res.Context.HasTrend {
		t.Errorf("context should still be built")
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	res := Analyze(nil, "", DefaultThresholds()) // must not panic
	if len(res.Signals) != 0 || res.AsOfDate != "" {
		t.Errorf("empty input → empty result")
	}
}

func TestAnalyzeOrdinaryBarNoSignal(t *testing.T) {
	// A plain mid-body bar with no notable shape and neutral context → no signals.
	cs := declining(110, 0, 15) // flat → HasTrend true, neither up nor down
	res := Analyze(cs, "", DefaultThresholds())
	if len(res.Signals) != 0 {
		t.Errorf("ordinary flat bar should produce no pattern, got %+v", res.Signals)
	}
}

func TestAnalyzeSortedByConfidence(t *testing.T) {
	res := Analyze(hammerEngulfDowntrend(), "", DefaultThresholds())
	if len(res.Signals) < 1 {
		t.Fatalf("expected signals; got none")
	}
	for i := 1; i < len(res.Signals); i++ {
		if res.Signals[i-1].Confidence < res.Signals[i].Confidence {
			t.Errorf("signals not sorted by confidence desc: %+v", res.Signals)
		}
	}
}

func TestAnalyzeTwoBarAndSingleBarCoexist(t *testing.T) {
	res := Analyze(hammerEngulfDowntrend(), "", DefaultThresholds())
	_, hasEngulf := findSignal(res, PatternBullishEngulfing)
	_, hasHammer := findSignal(res, PatternHammer)
	if !hasEngulf || !hasHammer {
		t.Fatalf("expected BOTH BULLISH_ENGULFING and HAMMER; got %+v", res.Signals)
	}
	if len(res.Signals) != 2 {
		t.Errorf("expected exactly 2 signals, got %d", len(res.Signals))
	}
	// no duplicate types
	seen := map[PatternType]bool{}
	for _, s := range res.Signals {
		if seen[s.Type] {
			t.Errorf("duplicate type %s", s.Type)
		}
		seen[s.Type] = true
	}
}

// hammerEngulfDowntrend builds a downtrend whose last two bars form a bullish engulfing AND
// whose latest bar is a small-body long-lower-shadow (HAMMER). Prev has a tiny bearish body
// so the small hammer body can still engulf it.
func hammerEngulfDowntrend() []Candle {
	cs := declining(130, 2, 10) // strong downtrend
	base := cs[len(cs)-1].Date
	// prev: tiny bearish body near 109
	prev := Candle{Date: base.AddDate(0, 0, 1), Open: 109.1, High: 109.3, Low: 108.8, Close: 109.0, Volume: 1000}
	// cur: bullish body 108.6→109.46 (engulfs [109,109.1]); long lower shadow to 105.2; bodyPct ~0.2 (not doji)
	cur := Candle{Date: base.AddDate(0, 0, 2), Open: 108.6, High: 109.5, Low: 105.2, Close: 109.46, Volume: 1000}
	return append(cs, prev, cur)
}
