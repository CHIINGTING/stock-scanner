package technical

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// The value must be the SAME number the rest of the scanner already reports. A second RSI
// that quietly disagreed with the one on the report would be worse than no RSI at all.
func TestRSIReusesTheExistingWilderImplementation(t *testing.T) {
	bars := trendBars(60, 100, 0.01)
	got := ComputeRSI(bars, DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}

	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}
	want := indicator.RSI(closes, 14)[len(closes)-1]
	if got.Value != want {
		t.Errorf("RSI = %v, want indicator.RSI's %v — this layer must not recompute it", got.Value, want)
	}
}

func TestRSIAllGainsAndAllLosses(t *testing.T) {
	up := ComputeRSI(trendBars(60, 100, 0.02), DefaultConfig())
	if up.Status != StatusOK {
		t.Fatalf("status = %s", up.Status)
	}
	if up.Value != 100 {
		t.Errorf("an unbroken run of gains gives RSI %v, want 100", up.Value)
	}
	if up.Zone != RSIOverbought {
		t.Errorf("zone = %s, want OVERBOUGHT", up.Zone)
	}

	down := ComputeRSI(trendBars(60, 300, -0.02), DefaultConfig())
	if down.Value != 0 {
		t.Errorf("an unbroken run of losses gives RSI %v, want 0", down.Value)
	}
	if down.Zone != RSIOversold {
		t.Errorf("zone = %s, want OVERSOLD", down.Zone)
	}
	// OVERBOUGHT is not a sell and OVERSOLD is not a buy — the type carries no such field.
	if !up.Rising && !up.Falling && !down.Rising && !down.Falling {
		t.Log("flat trajectories are legitimate; nothing to assert here")
	}
}

// A flat market gives no gains and no losses. Wilder's formula divides by the average loss,
// so this is the case that must not produce a NaN.
func TestRSIFlatMarket(t *testing.T) {
	got := ComputeRSI(flatBars(60, 100), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if !finite(got.Value) {
		t.Fatalf("non-finite RSI on a flat tape: %v", got.Value)
	}
	// Both flags false: flat is flat, and the two are not complements.
	if got.Rising || got.Falling {
		t.Errorf("a flat RSI reported rising=%v falling=%v", got.Rising, got.Falling)
	}
}

func TestRSIZoneBoundaries(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, RSIOversold}, {29.99, RSIOversold},
		{30, RSIWeak}, {44.99, RSIWeak},
		{45, RSINeutral}, {54.99, RSINeutral},
		{55, RSIBullish}, {70, RSIBullish}, // 70 itself is still BULLISH, per the spec
		{70.01, RSIOverbought}, {100, RSIOverbought},
	} {
		if got := rsiZone(tc.v); got != tc.want {
			t.Errorf("rsiZone(%v) = %s, want %s", tc.v, got, tc.want)
		}
	}
}

func TestRSITrajectory(t *testing.T) {
	// A long decline followed by a sharp rally: the last step must read as rising.
	closes := make([]float64, 0, 60)
	p := 200.0
	for i := 0; i < 45; i++ {
		closes = append(closes, p)
		p *= 0.99
	}
	for i := 0; i < 15; i++ {
		p *= 1.02
		closes = append(closes, p)
	}
	got := ComputeRSI(mkBars(closes...), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if !got.Rising || got.Falling {
		t.Errorf("after a rally the RSI should be rising, got rising=%v falling=%v", got.Rising, got.Falling)
	}
}

func TestRSIInsufficientData(t *testing.T) {
	cfg := DefaultConfig()
	// period+1 bars would give a value but no previous value, so no trajectory.
	if got := ComputeRSI(trendBars(cfg.RSIPeriod+1, 100, 0.01), cfg); got.Status != StatusInsufficientData {
		t.Errorf("%d bars → %s, want INSUFFICIENT_DATA (no previous value to compare)",
			cfg.RSIPeriod+1, got.Status)
	}
	if got := ComputeRSI(trendBars(cfg.RSIPeriod+2, 100, 0.01), cfg); got.Status != StatusOK {
		t.Errorf("%d bars → %s, want OK", cfg.RSIPeriod+2, got.Status)
	}
	if got := ComputeRSI(nil, cfg); got.Status != StatusNoData || got.Value != 0 {
		t.Errorf("no bars → %+v, want NO_DATA with no value", got)
	}
}
