package candlestick

import (
	"testing"
	"time"
)

// series builds candles from closes; each bar's High/Low straddle the close, Volume=vol.
func series(closes []float64, vol int64) []Candle {
	out := make([]Candle, len(closes))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = Candle{Date: base.AddDate(0, 0, i), Open: c, High: c + 1, Low: c - 1, Close: c, Volume: vol}
	}
	return out
}

func ramp(start, step float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + step*float64(i)
	}
	return out
}

func TestBuildContextUptrend(t *testing.T) {
	ctx := BuildContext(series(ramp(100, 2, 15), 1000), "", DefaultThresholds())
	if !ctx.HasTrend || !ctx.Uptrend || ctx.Downtrend {
		t.Fatalf("expected uptrend: %+v", ctx)
	}
	if ctx.Return10D <= 0 {
		t.Errorf("Return10D should be positive: %v", ctx.Return10D)
	}
}

func TestBuildContextDowntrend(t *testing.T) {
	ctx := BuildContext(series(ramp(140, -2, 15), 1000), "", DefaultThresholds())
	if !ctx.HasTrend || !ctx.Downtrend || ctx.Uptrend {
		t.Fatalf("expected downtrend: %+v", ctx)
	}
}

func TestBuildContextFlatIsNeitherButKnown(t *testing.T) {
	// Flat series → HasTrend true (we KNOW), but neither up nor down.
	ctx := BuildContext(series(ramp(100, 0, 15), 1000), "", DefaultThresholds())
	if !ctx.HasTrend {
		t.Fatalf("flat but sufficient history → HasTrend must be true")
	}
	if ctx.Uptrend || ctx.Downtrend {
		t.Errorf("flat → neither up nor down: %+v", ctx)
	}
}

func TestBuildContextInsufficientHistory(t *testing.T) {
	// < 11 bars → HasTrend false (unknown, not proven-false); < 21 → no near flags.
	ctx := BuildContext(series(ramp(100, 1, 8), 1000), "", DefaultThresholds())
	if ctx.HasTrend || ctx.Uptrend || ctx.Downtrend {
		t.Errorf("short history → HasTrend/Uptrend/Downtrend all false, got %+v", ctx)
	}
	if ctx.HasNearHigh || ctx.HasNearLow || ctx.HasVolumeRatio {
		t.Errorf("short history → capability flags false, got %+v", ctx)
	}
	if ctx.Sufficient() {
		t.Errorf("Sufficient() (== HasTrend) must be false on short history")
	}
}

func TestBuildContextNearHighNoLookAhead(t *testing.T) {
	// 21 bars: the 20 BEFORE latest all High≈100 except one at 120; latest.High is a huge
	// spike (200) that MUST be ignored (window excludes latest). latest.Close=118 is within
	// 3% of the window high 120 → Near20DHigh true; if it wrongly used latest.High=200 it'd be false.
	closes := ramp(100, 0, 20) // 20 prior bars at 100
	cs := series(closes, 1000)
	cs[10].High = 120 // a prior-window high
	// append the latest bar with a spike high but close near the prior window high
	latest := Candle{Date: cs[len(cs)-1].Date.AddDate(0, 0, 1), Open: 118, High: 200, Low: 117, Close: 118, Volume: 1000}
	cs = append(cs, latest)

	ctx := BuildContext(cs, "", DefaultThresholds())
	if !ctx.HasNearHigh {
		t.Fatalf("expected HasNearHigh")
	}
	if !ctx.Near20DHigh {
		t.Errorf("Near20DHigh must be true using the WINDOW high (120), not latest.High (200)")
	}
}

func TestBuildContextNearLow(t *testing.T) {
	closes := ramp(100, 0, 20)
	cs := series(closes, 1000)
	cs[5].Low = 80 // window low
	latest := Candle{Date: cs[len(cs)-1].Date.AddDate(0, 0, 1), Open: 81, High: 82, Low: 79.5, Close: 81, Volume: 1000}
	cs = append(cs, latest)
	ctx := BuildContext(cs, "", DefaultThresholds())
	if !ctx.HasNearLow || !ctx.Near20DLow {
		t.Errorf("expected Near20DLow from window low 80, got %+v", ctx)
	}
}

func TestBuildContextVolumeRatioExcludesLatest(t *testing.T) {
	cs := series(ramp(100, 0, 20), 1000) // 20 prior bars vol 1000
	latest := Candle{Date: cs[len(cs)-1].Date.AddDate(0, 0, 1), Open: 100, High: 101, Low: 99, Close: 100, Volume: 2500}
	cs = append(cs, latest)
	ctx := BuildContext(cs, "", DefaultThresholds())
	if !ctx.HasVolumeRatio {
		t.Fatalf("expected HasVolumeRatio")
	}
	if ctx.VolumeRatio != 2.5 { // 2500 / avg(1000)
		t.Errorf("VolumeRatio = %v, want 2.5 (avg excludes latest)", ctx.VolumeRatio)
	}
}

func TestBuildContextStageInjection(t *testing.T) {
	ctx := BuildContext(series(ramp(100, 1, 15), 1000), Stage("MAIN_RUN"), DefaultThresholds())
	if ctx.Stage != "MAIN_RUN" {
		t.Errorf("stage not injected: %q", ctx.Stage)
	}
}

func TestBuildContextEmpty(t *testing.T) {
	ctx := BuildContext(nil, "", DefaultThresholds()) // must not panic
	if ctx.HasTrend || ctx.HasNearHigh || ctx.HasVolumeRatio {
		t.Errorf("empty history → all capability flags false")
	}
}
