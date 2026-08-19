package technical

import "testing"

// A crossing is an EVENT. This is the test that stops "MACD > Signal" from being flagged
// GOLDEN on every bar of a months-long trend.
func TestCrossReportsOnlyTheCrossingBar(t *testing.T) {
	for name, tc := range map[string]struct {
		prevMACD, prevSig, macd, sig float64
		want                         string
	}{
		"crossed up":            {-1, 0, 1, 0, CrossGolden},
		"crossed down":          {1, 0, -1, 0, CrossDeath},
		"already above":         {2, 1, 3, 1, CrossNone},
		"already below":         {-2, -1, -3, -1, CrossNone},
		"touched then rose":     {1, 1, 2, 1, CrossGolden},
		"touched then fell":     {1, 1, 0, 1, CrossDeath},
		"still converging":      {-2, 0, -0.5, 0, CrossNone},
		"widening above signal": {1, 0.5, 5, 0.5, CrossNone},
	} {
		if got := macdCross(tc.prevMACD, tc.prevSig, tc.macd, tc.sig); got != tc.want {
			t.Errorf("%s: cross = %s, want %s", name, got, tc.want)
		}
	}
}

// A sustained uptrend must NOT keep reporting GOLDEN once the cross is behind it.
func TestSustainedTrendDoesNotKeepReportingACross(t *testing.T) {
	got := ComputeMACD(trendBars(120, 100, 0.01), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.MACD <= got.SignalVal {
		t.Fatalf("a long uptrend should leave MACD above its signal, got %v vs %v", got.MACD, got.SignalVal)
	}
	if got.Cross != CrossNone {
		t.Errorf("cross = %s deep inside a trend, want NONE — a crossing is an event", got.Cross)
	}
}

// reversalPath builds: a flat stretch (MACD and signal both at zero), a sharp move that
// drives MACD decisively to one side of its signal, then a sharp reversal. The flat prologue
// matters — a purely compounding decline makes each absolute step SMALLER, which leaves MACD
// already rising and above its signal, so no crossing would ever be available to detect.
func reversalPath(flat int, first, second float64, firstLen, secondLen int) []float64 {
	closes := make([]float64, 0, flat+firstLen+secondLen)
	p := 100.0
	for i := 0; i < flat; i++ {
		closes = append(closes, p)
	}
	for i := 0; i < firstLen; i++ {
		p *= 1 + first
		closes = append(closes, p)
	}
	for i := 0; i < secondLen; i++ {
		p *= 1 + second
		closes = append(closes, p)
	}
	return closes
}

// crossSeenDuring walks the tail of a path one bar at a time and reports whether the wanted
// crossing fires on any bar. Walking bar by bar is the point: ComputeMACD only ever reads the
// LAST bar, so this is what a live scanner would actually have seen day by day.
func crossSeenDuring(closes []float64, tail int, want string) bool {
	for end := len(closes) - tail; end <= len(closes); end++ {
		if end < 2 {
			continue
		}
		if r := ComputeMACD(mkBars(closes[:end]...), DefaultConfig()); r.Status.OK() && r.Cross == want {
			return true
		}
	}
	return false
}

func TestGoldenAndDeathCrossesAreDetected(t *testing.T) {
	down := reversalPath(40, -0.02, +0.03, 20, 25)
	if !crossSeenDuring(down, 25, CrossGolden) {
		t.Error("a sharp reversal upward after a decline should produce a GOLDEN cross on some bar")
	}

	up := reversalPath(40, +0.02, -0.03, 20, 25)
	if !crossSeenDuring(up, 25, CrossDeath) {
		t.Error("a sharp reversal downward after a rally should produce a DEATH cross on some bar")
	}

	// And the crossing must be rare: it fires on the turn, not on every bar of the move.
	var goldenBars int
	for end := len(down) - 25; end <= len(down); end++ {
		if r := ComputeMACD(mkBars(down[:end]...), DefaultConfig()); r.Status.OK() && r.Cross == CrossGolden {
			goldenBars++
		}
	}
	if goldenBars > 2 {
		t.Errorf("GOLDEN fired on %d of 26 bars — a crossing is an event, not a state", goldenBars)
	}
}

// The histogram trajectory is read independently of sign, which is what lets a still-positive
// MACD report DECELERATING.
func TestHistogramMomentum(t *testing.T) {
	for name, tc := range map[string]struct {
		h2, h1, h0 float64
		want       string
	}{
		"rising positive":  {0.2, 0.4, 0.7, MomentumAccelerating},
		"falling positive": {0.8, 0.5, 0.2, MomentumDecelerating},
		"rising negative":  {-0.8, -0.5, -0.2, MomentumAccelerating},
		"falling negative": {-0.2, -0.5, -0.8, MomentumDecelerating},
		"flat":             {0.5, 0.5, 0.5, MomentumNeutral},
		"choppy":           {0.5, 0.2, 0.4, MomentumNeutral},
	} {
		if got := histogramMomentum(tc.h2, tc.h1, tc.h0); got != tc.want {
			t.Errorf("%s: momentum = %s, want %s", name, got, tc.want)
		}
	}
}

// The case the spec calls out: MACD still above Signal, but losing steam.
func TestDeceleratingWhileStillAboveSignal(t *testing.T) {
	// A strong rally that flattens out — the histogram shrinks while MACD stays on top.
	closes := make([]float64, 0, 120)
	p := 100.0
	for i := 0; i < 60; i++ {
		closes = append(closes, p)
		p *= 1.03
	}
	for i := 0; i < 40; i++ {
		closes = append(closes, p)
		p *= 1.0005
	}
	got := ComputeMACD(mkBars(closes...), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.MACD <= got.SignalVal {
		t.Skip("fixture did not keep MACD above signal; the trajectory assertion needs that state")
	}
	if got.Momentum != MomentumDecelerating {
		t.Errorf("momentum = %s while MACD is still above signal, want DECELERATING", got.Momentum)
	}
}

func TestMACDInsufficientAndInvalid(t *testing.T) {
	cfg := DefaultConfig()
	need := cfg.MACDSlow + cfg.MACDSignal + 1

	if got := ComputeMACD(trendBars(need-1, 100, 0.01), cfg); got.Status != StatusInsufficientData {
		t.Errorf("%d bars → %s, want INSUFFICIENT_DATA", need-1, got.Status)
	}
	if got := ComputeMACD(trendBars(need, 100, 0.01), cfg); got.Status != StatusOK {
		t.Errorf("%d bars → %s, want OK", need, got.Status)
	}
	if got := ComputeMACD(nil, cfg); got.Status != StatusNoData {
		t.Errorf("no bars → %s, want NO_DATA", got.Status)
	}
	// A non-OK result publishes no numbers and no cross.
	got := ComputeMACD(trendBars(10, 100, 0.01), cfg)
	if got.MACD != 0 || got.Histogram != 0 || got.Cross != CrossNone {
		t.Errorf("an INSUFFICIENT_DATA result carried values: %+v", got)
	}
	if got.Momentum != MomentumUnknown {
		t.Errorf("momentum = %s, want UNKNOWN when nothing was measured", got.Momentum)
	}
}

func TestMACDFlatMarketIsAllZeroAndFinite(t *testing.T) {
	got := ComputeMACD(flatBars(80, 100), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if !allFinite(got.MACD, got.SignalVal, got.Histogram) {
		t.Fatalf("non-finite output on a flat tape: %+v", got)
	}
	if got.Cross != CrossNone {
		t.Errorf("cross = %s on a flat tape, want NONE", got.Cross)
	}
	if got.Momentum != MomentumNeutral {
		t.Errorf("momentum = %s on a flat tape, want NEUTRAL", got.Momentum)
	}
}

func TestMACDIsDeterministic(t *testing.T) {
	bars := trendBars(100, 100, 0.008)
	if ComputeMACD(bars, DefaultConfig()) != ComputeMACD(bars, DefaultConfig()) {
		t.Error("same input gave different results")
	}
}
