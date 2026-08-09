package indicator

import (
	"math"
	"testing"
)

func closeEnough(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// ── MASlopePerDay ────────────────────────────────────────────────────────────────────

func TestMASlopePerDay(t *testing.T) {
	// 100 → 110 over 5 bars: +10% total, +2% per day.
	ma := []float64{100, 102, 104, 106, 108, 110}
	got, ok := MASlopePerDay(ma, 5)
	if !ok {
		t.Fatal("expected a computable slope")
	}
	closeEnough(t, got, 2, "MA slope")

	// Falling MA yields a negative slope of the same magnitude convention.
	down := []float64{110, 108, 106, 104, 102, 99}
	got, _ = MASlopePerDay(down, 5)
	if got >= 0 {
		t.Errorf("a falling MA must have a negative slope, got %v", got)
	}

	// Flat MA is exactly zero, not a rounding artefact.
	flat := []float64{50, 50, 50, 50, 50, 50}
	got, _ = MASlopePerDay(flat, 5)
	closeEnough(t, got, 0, "flat slope")
}

// The unit must be scale-free: two stocks with the same PERCENTAGE trend must produce the
// same slope regardless of price level. This is the reason the function is not a subtraction.
func TestMASlopePerDayIsScaleFree(t *testing.T) {
	cheap := []float64{20, 20.4, 20.8, 21.2, 21.6, 22} // +10% over 5
	pricey := []float64{500, 510, 520, 530, 540, 550}  // +10% over 5
	a, _ := MASlopePerDay(cheap, 5)
	b, _ := MASlopePerDay(pricey, 5)
	closeEnough(t, a, b, "slope must not depend on price level")
}

// A slope that cannot be computed must say so — never return 0, which reads as "flat".
func TestMASlopePerDayMissingOrInvalid(t *testing.T) {
	cases := []struct {
		name     string
		ma       []float64
		lookback int
	}{
		{"too short", []float64{100, 101, 102}, 5},
		{"empty", nil, 5},
		{"zero lookback", []float64{1, 2, 3}, 0},
		{"negative lookback", []float64{1, 2, 3}, -5},
		// SMA zero-fills its warm-up region; ranking against that would invent a slope.
		{"unwarmed MA endpoint", []float64{0, 0, 0, 0, 0, 108}, 5},
		{"zero current", []float64{100, 101, 102, 103, 104, 0}, 5},
	}
	for _, tc := range cases {
		if v, ok := MASlopePerDay(tc.ma, tc.lookback); ok {
			t.Errorf("%s: expected unavailable, got %v", tc.name, v)
		}
	}
}

// The scanner's real windows: MA20 over 5 days and MA60 over 10 days must both come out in
// the same %/day unit so they are comparable in one decision table.
func TestMASlopeWindowsAreComparable(t *testing.T) {
	ma20 := make([]float64, 21)
	for i := range ma20 {
		ma20[i] = 100 * math.Pow(1.01, float64(i)) // +1% per bar
	}
	ma60 := make([]float64, 21)
	for i := range ma60 {
		ma60[i] = 300 * math.Pow(1.01, float64(i)) // same growth, different level
	}
	s20, ok20 := MASlopePerDay(ma20, 5)
	s60, ok60 := MASlopePerDay(ma60, 10)
	if !ok20 || !ok60 {
		t.Fatal("both slopes should compute")
	}
	// Same underlying growth rate → slopes within a hair of each other despite different
	// windows and price levels (compounding makes them not exactly equal).
	if math.Abs(s20-s60) > 0.05 {
		t.Errorf("windows are not comparable: MA20/5d=%v MA60/10d=%v", s20, s60)
	}
}

// ── BIAS series & percentile ─────────────────────────────────────────────────────────

func TestBIASSeriesMatchesBIAS(t *testing.T) {
	closes := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	series := BIASSeries(closes, 5)
	if len(series) != len(closes)-5+1 {
		t.Fatalf("series length = %d, want %d", len(series), len(closes)-5+1)
	}
	// The last element must equal the single-point BIAS — one formula, two entry points.
	want, ok := BIAS(closes, 5)
	if !ok {
		t.Fatal("BIAS should compute")
	}
	closeEnough(t, series[len(series)-1], want, "series tail vs BIAS")
}

func TestBIASSeriesInsufficientHistory(t *testing.T) {
	if s := BIASSeries([]float64{1, 2, 3}, 20); s != nil {
		t.Errorf("expected nil for short history, got %v", s)
	}
	if s := BIASSeries([]float64{1, 2, 3}, 0); s != nil {
		t.Errorf("expected nil for zero period, got %v", s)
	}
}

func TestPercentileRank(t *testing.T) {
	sample := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	p, ok := PercentileRank(sample, 10, 5)
	if !ok || p != 100 {
		t.Errorf("max value → %v (ok=%v), want 100", p, ok)
	}
	p, _ = PercentileRank(sample, 5, 5)
	if p != 50 {
		t.Errorf("median value → %v, want 50", p)
	}
	// A value below everything ranks at 0, not at some floor.
	p, _ = PercentileRank(sample, -99, 5)
	if p != 0 {
		t.Errorf("below-min → %v, want 0", p)
	}
	// Ties count as "at or below": three 5s means 5 is not below average.
	p, _ = PercentileRank([]float64{5, 5, 5, 9}, 5, 1)
	if p != 75 {
		t.Errorf("tie handling → %v, want 75", p)
	}
}

// A percentile from a handful of points looks authoritative and means nothing.
func TestPercentileRankRejectsThinSample(t *testing.T) {
	if _, ok := PercentileRank([]float64{1, 2, 3}, 2, 60); ok {
		t.Error("a 3-point sample must not yield a percentile when 60 are required")
	}
	if _, ok := PercentileRank(nil, 1, 1); ok {
		t.Error("an empty sample must never yield a percentile")
	}
}

func TestBIASPercentile(t *testing.T) {
	// A steadily rising series ends at its most extended point, so the latest BIAS should
	// rank at the very top of its own history.
	closes := make([]float64, 300)
	for i := range closes {
		closes[i] = 100 * math.Pow(1.005, float64(i))
	}
	p, ok := BIASPercentile(closes, 20, 252, 60)
	if !ok {
		t.Fatal("expected a computable percentile")
	}
	if p < 90 {
		t.Errorf("a relentless uptrend should sit near its BIAS high, got %v", p)
	}

	// Too little history → unavailable, never a default.
	if _, ok := BIASPercentile(closes[:40], 20, 252, 60); ok {
		t.Error("40 bars must not produce a 252-day percentile with a 60-sample floor")
	}
}

// The window must actually bound the sample: ranking is against the last `window`
// observations and nothing older. Asserted as a direct contract against the series rather
// than by reasoning about a synthetic price path, where several effects fight each other.
func TestBIASPercentileRespectsWindow(t *testing.T) {
	closes := make([]float64, 400)
	for i := range closes {
		closes[i] = 100 * (1 + 0.3*math.Sin(float64(i)/9)) // varied, non-degenerate BIAS
	}
	series := BIASSeries(closes, 20)
	latest := series[len(series)-1]

	for _, window := range []int{60, 100, 252} {
		got, ok := BIASPercentile(closes, 20, window, 60)
		if !ok {
			t.Fatalf("window %d: expected a percentile", window)
		}
		want, _ := PercentileRank(series[len(series)-window:], latest, 60)
		closeEnough(t, got, want, "window "+string(rune('0'+window/100))+" rank")
	}

	// window 0 means "rank against everything available".
	all, _ := BIASPercentile(closes, 20, 0, 60)
	want, _ := PercentileRank(series, latest, 60)
	closeEnough(t, all, want, "window 0 = whole history")

	// A window longer than the history is not an error — it just uses everything.
	long, ok := BIASPercentile(closes, 20, 100000, 60)
	if !ok {
		t.Fatal("an oversized window should still rank against what exists")
	}
	closeEnough(t, long, want, "oversized window = whole history")
}
