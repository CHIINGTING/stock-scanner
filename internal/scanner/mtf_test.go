package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func tv(state, momo string, valid, partial bool) TimeframeView {
	return TimeframeView{Valid: valid, Partial: partial, TrendState: state, MomentumState: momo, TrendScore: 80}
}

// tvs is like tv but with an explicit TrendScore (for STRONG-threshold tests).
func tvs(state, momo string, score float64, valid, partial bool) TimeframeView {
	return TimeframeView{Valid: valid, Partial: partial, TrendState: state, MomentumState: momo, TrendScore: score}
}

// 1. longTermFilter: >MA200 BULLISH, <MA200 BEARISH, <200 bars UNKNOWN (never bearish).
func TestMTFLongTermFilter(t *testing.T) {
	up := make([]float64, 250)
	down := make([]float64, 250)
	for i := range up {
		up[i] = 50 + float64(i)*0.5    // rising → last > MA200
		down[i] = 200 - float64(i)*0.5 // falling → last < MA200
	}
	if got := longTermFilter(up); got != "BULLISH" {
		t.Errorf("rising → BULLISH, got %s", got)
	}
	if got := longTermFilter(down); got != "BEARISH" {
		t.Errorf("falling → BEARISH, got %s", got)
	}
	if got := longTermFilter(make([]float64, 150)); got != "UNKNOWN" {
		t.Errorf("<200 bars → UNKNOWN (never bearish), got %s", got)
	}
}

// 2. mtfAlignment label mapping + UNKNOWN propagation.
func TestMTFAlignment(t *testing.T) {
	cases := []struct {
		d, w TimeframeView
		want string
	}{
		{tv("UPTREND", "STEADY", true, false), tv("UPTREND", "STEADY", true, false), "FULL_BULL"},
		{tv("DOWNTREND", "NEGATIVE", true, false), tv("DOWNTREND", "NEGATIVE", true, false), "FULL_BEAR"},
		{tv("UPTREND", "STEADY", true, false), tv("DOWNTREND", "NEGATIVE", true, false), "CONFLICT"},
		{tv("UPTREND", "STEADY", true, false), tv("RANGE", "STEADY", true, false), "DAILY_LEADS"},
		{tv("UPTREND", "STEADY", false, false), tv("UPTREND", "STEADY", true, false), "UNKNOWN"}, // daily invalid
	}
	for i, c := range cases {
		if _, got := mtfAlignment(c.d, c.w); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}

// 3. R4-2b mtfSignalStrength: STRONG requires FULL_BULL + both scores >= threshold +
// both momentum ACCELERATING + weekly not partial; otherwise FULL_BULL → MODERATE.
func TestMTFSignalStrength(t *testing.T) {
	cfg := MTFConfig{StrongDailyScore: 85, StrongWeeklyScore: 85}
	accel := mtfMomoAccelerating

	// score 100/100 + both ACCELERATING + not partial → STRONG
	if got := mtfSignalStrength(tvs("UPTREND", accel, 100, true, false), tvs("UPTREND", accel, 100, true, false), "FULL_BULL", cfg); got != "STRONG" {
		t.Errorf("full-aligned accelerating → STRONG, got %s", got)
	}
	// score 100/100 but only STEADY → MODERATE (動能普通)
	if got := mtfSignalStrength(tvs("UPTREND", accel, 100, true, false), tvs("UPTREND", mtfMomoSteady, 100, true, false), "FULL_BULL", cfg); got != "MODERATE" {
		t.Errorf("steady momentum → MODERATE, got %s", got)
	}
	// score 75/75 (below threshold) → MODERATE
	if got := mtfSignalStrength(tvs("UPTREND", accel, 75, true, false), tvs("UPTREND", accel, 75, true, false), "FULL_BULL", cfg); got != "MODERATE" {
		t.Errorf("score 75 below threshold → MODERATE, got %s", got)
	}
	// weekly partial (even 100/100/accel) → NOT STRONG
	if got := mtfSignalStrength(tvs("UPTREND", accel, 100, true, false), tvs("UPTREND", accel, 100, true, true), "FULL_BULL", cfg); got == "STRONG" {
		t.Errorf("partial weekly must not be STRONG, got %s", got)
	}
	// DAILY_LEADS → MODERATE
	if got := mtfSignalStrength(tvs("UPTREND", accel, 100, true, false), tvs("RANGE", mtfMomoSteady, 50, true, false), "DAILY_LEADS", cfg); got != "MODERATE" {
		t.Errorf("DAILY_LEADS → MODERATE, got %s", got)
	}
	// CONFLICT → CONFLICTED
	if got := mtfSignalStrength(tvs("UPTREND", accel, 100, true, false), tvs("DOWNTREND", mtfMomoNegative, 0, true, false), "CONFLICT", cfg); got != "CONFLICTED" {
		t.Errorf("CONFLICT → CONFLICTED, got %s", got)
	}
	// FULL_BEAR → WEAK
	if got := mtfSignalStrength(tvs("DOWNTREND", mtfMomoNegative, 0, true, false), tvs("DOWNTREND", mtfMomoNegative, 0, true, false), "FULL_BEAR", cfg); got != "WEAK" {
		t.Errorf("FULL_BEAR → WEAK, got %s", got)
	}
	// invalid view → UNKNOWN
	if got := mtfSignalStrength(tvs("UNKNOWN", mtfMomoUnknown, 0, false, false), tvs("UPTREND", accel, 100, true, false), "UNKNOWN", cfg); got != "UNKNOWN" {
		t.Errorf("invalid → UNKNOWN, got %s", got)
	}
}

// 4. computeTimeframeView with insufficient data → Valid=false, UNKNOWN (not bearish).
func TestMTFTimeframeViewInsufficient(t *testing.T) {
	v := computeTimeframeView("DAILY", make([]float64, 20), mtfDailyShortMA, mtfDailyMidMA, mtfDailyLongMA, false)
	if v.Valid || v.TrendState != "UNKNOWN" || v.MomentumState != "UNKNOWN" {
		t.Errorf("insufficient data must be Valid=false/UNKNOWN, got valid=%v trend=%s momo=%s", v.Valid, v.TrendState, v.MomentumState)
	}
}

// 5. Integration: uptrend → Daily UPTREND + BULLISH long-term filter.
func TestMTFComputeUptrend(t *testing.T) {
	r := ComputeMultiTimeframe(makeCandles(220, 50, 0.4, 1_000_000), MTFConfig{Enable: true})
	if !r.Daily.Valid || r.Daily.TrendState != "UPTREND" {
		t.Errorf("uptrend daily should be UPTREND, got valid=%v state=%s", r.Daily.Valid, r.Daily.TrendState)
	}
	if r.LongTermFilter != "BULLISH" {
		t.Errorf("rising 220 bars → LongTermFilter BULLISH, got %s", r.LongTermFilter)
	}
}

// 6. Integration: insufficient history → everything UNKNOWN, nothing bearish.
func TestMTFComputeInsufficient(t *testing.T) {
	r := ComputeMultiTimeframe(makeCandles(40, 50, 0.3, 1_000_000), MTFConfig{Enable: true})
	if r.Daily.Valid || r.Daily.TrendState != "UNKNOWN" {
		t.Errorf("40 bars daily should be UNKNOWN, got valid=%v state=%s", r.Daily.Valid, r.Daily.TrendState)
	}
	if r.AlignmentLabel != "UNKNOWN" || r.SignalStrength != "UNKNOWN" || r.LongTermFilter != "UNKNOWN" {
		t.Errorf("insufficient → all UNKNOWN, got align=%s sig=%s ltf=%s", r.AlignmentLabel, r.SignalStrength, r.LongTermFilter)
	}
	if r.AlignmentLabel == "FULL_BEAR" || r.LongTermFilter == "BEARISH" {
		t.Error("insufficient data must never be bearish")
	}
}

// 7. Pure function: ComputeMultiTimeframe does not mutate input.
func TestMTFPure(t *testing.T) {
	candles := makeCandles(220, 50, 0.4, 1_000_000)
	before := candles[0].Close
	_ = ComputeMultiTimeframe(candles, MTFConfig{Enable: true})
	if candles[0].Close != before {
		t.Error("ComputeMultiTimeframe mutated input")
	}
}

// 8/9. flag off golden + flag on shadow-only (score/action/prob/order unchanged).
func TestR42MTFShadowDoesNotAffectScoring(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	off := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	on := New(Config{EnableMultiTimeframe: true}).EnrichWatchlist(items, so, rt, mb, nil)

	if len(off) != len(on) {
		t.Fatalf("length differs")
	}
	for i := range off {
		if on[i].A.Symbol != off[i].A.Symbol || on[i].RocketScore != off[i].RocketScore ||
			on[i].WatchAction != off[i].WatchAction || on[i].ExplosionProb != off[i].ExplosionProb {
			t.Errorf("%s: MTF flag changed scoring/order", off[i].A.Symbol)
		}
		if off[i].Shadow != nil {
			t.Errorf("%s: flags off → Shadow must be nil", off[i].A.Symbol)
		}
		if on[i].Shadow == nil || on[i].Shadow.MultiTimeframe == nil {
			t.Errorf("%s: MTF flag on → Shadow.MultiTimeframe must be attached", on[i].A.Symbol)
		}
	}
}
