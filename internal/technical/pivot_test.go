package technical

import (
	"math"
	"testing"
)

// A hand-checkable fixture: previous session H=110, L=90, C=100.
//
//	P  = (110+90+100)/3 = 100
//	R1 = 2*100 - 90     = 110      S1 = 2*100 - 110 = 90
//	R2 = 100 + 20       = 120      S2 = 100 - 20    = 80
//	R3 = 110 + 2*(100-90) = 130    S3 = 90 - 2*(110-100) = 70
func pivotFixture(todayClose float64) []Bar {
	return []Bar{
		{Date: testStart, Open: 100, High: 110, Low: 90, Close: 100, Volume: 1000},
		{Date: testStart.AddDate(0, 0, 1),
			Open: todayClose, High: todayClose, Low: todayClose, Close: todayClose, Volume: 1000},
	}
}

func TestPivotKnownFixture(t *testing.T) {
	got := ComputePivot(pivotFixture(100), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	for _, tc := range []struct {
		name string
		got  float64
		want float64
	}{
		{"P", got.Pivot, 100},
		{"R1", got.R1, 110}, {"R2", got.R2, 120}, {"R3", got.R3, 130},
		{"S1", got.S1, 90}, {"S2", got.S2, 80}, {"S3", got.S3, 70},
	} {
		if math.Abs(tc.got-tc.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if got.BasisDate != testStart.Format("2006-01-02") {
		t.Errorf("basis date = %q, want the PREVIOUS session's date", got.BasisDate)
	}
}

// The look-ahead guard: today's own high/low must not shape today's levels. If they did,
// every backtest of a pivot rule would be measuring levels nobody could have traded against.
func TestPivotUsesThePreviousSessionOnly(t *testing.T) {
	base := pivotFixture(100)
	first := ComputePivot(base, DefaultConfig())

	// Give today a wildly different range. The levels must not move.
	moved := pivotFixture(100)
	moved[1].High = 500
	moved[1].Low = 50
	moved[1].Open = 100
	second := ComputePivot(moved, DefaultConfig())

	if first.Pivot != second.Pivot || first.R1 != second.R1 || first.S3 != second.S3 {
		t.Errorf("today's own range changed today's levels:\n%+v\n%+v", first, second)
	}
}

// The spec's own example: price 100, R1 at 101 → nearest R1, distance +1%.
// Positive means the level sits ABOVE price.
func TestNearestLevelAndSignedDistance(t *testing.T) {
	// Previous session H=101, L=99, C=100 → P=100, R1=101, S1=99.
	bars := []Bar{
		{Date: testStart, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1000},
		{Date: testStart.AddDate(0, 0, 1), Open: 100.6, High: 100.6, Low: 100.6, Close: 100.6, Volume: 1000},
	}
	got := ComputePivot(bars, DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.NearestLevel != "R1" {
		t.Errorf("nearest = %s, want R1 (100.6 is closer to 101 than to 100)", got.NearestLevel)
	}
	wantPct := (101/100.6 - 1) * 100
	if math.Abs(got.DistancePct-wantPct) > 1e-9 {
		t.Errorf("distance = %v%%, want %v%% (the level relative to price)", got.DistancePct, wantPct)
	}
	if got.DistancePct <= 0 {
		t.Error("a level above price must give a positive distance")
	}

	// A level below price gives a negative distance.
	below := bars
	below[1] = Bar{Date: testStart.AddDate(0, 0, 1), Open: 99.4, High: 99.4, Low: 99.4, Close: 99.4, Volume: 1000}
	got = ComputePivot(below, DefaultConfig())
	if got.NearestLevel != "S1" {
		t.Errorf("nearest = %s, want S1", got.NearestLevel)
	}
	if got.DistancePct >= 0 {
		t.Errorf("distance = %v, want negative for a level below price", got.DistancePct)
	}
}

func TestPivotLocations(t *testing.T) {
	cfg := DefaultConfig() // PivotNearPct = 1.0
	for _, tc := range []struct {
		name  string
		close float64
		want  string
	}{
		// Levels: S3=70 S2=80 S1=90 P=100 R1=110 R2=120 R3=130.
		{"above every resistance", 140, LocAboveResistance},
		{"below every support", 60, LocBelowSupport},
		{"hugging R1", 109.5, LocNearResistance},
		{"hugging S1", 90.5, LocNearSupport},
		{"sitting on the axis", 100, LocNearSupport}, // at or above P, the axis is the floor
		{"between levels", 105, LocMidRange},
	} {
		got := ComputePivot(pivotFixture(tc.close), cfg)
		if got.Status != StatusOK {
			t.Fatalf("%s: status = %s", tc.name, got.Status)
		}
		if got.Location != tc.want {
			t.Errorf("%s (close %v, nearest %s, %.2f%%): location = %s, want %s",
				tc.name, tc.close, got.NearestLevel, got.DistancePct, got.Location, tc.want)
		}
	}
}

// The tolerance is configuration, so widening it must widen the "near" zone. This is what
// keeps it a tolerance rather than a magic number.
func TestPivotToleranceIsConfigurable(t *testing.T) {
	tight := ComputePivot(pivotFixture(107), Config{PivotNearPct: 1})
	if tight.Location != LocMidRange {
		t.Errorf("with a 1%% tolerance, 107 vs R1 110 = %s, want MID_RANGE", tight.Location)
	}
	loose := ComputePivot(pivotFixture(107), Config{PivotNearPct: 5})
	if loose.Location != LocNearResistance {
		t.Errorf("with a 5%% tolerance, 107 vs R1 110 = %s, want NEAR_RESISTANCE", loose.Location)
	}
}

func TestPivotInsufficientAndInvalid(t *testing.T) {
	cfg := DefaultConfig()
	// One bar means the only available basis is the bar being evaluated — the look-ahead
	// this construction exists to avoid.
	one := []Bar{{Date: testStart, Open: 100, High: 110, Low: 90, Close: 100, Volume: 1}}
	if got := ComputePivot(one, cfg); got.Status != StatusInsufficientData {
		t.Errorf("one bar → %s, want INSUFFICIENT_DATA", got.Status)
	}
	if got := ComputePivot(nil, cfg); got.Status != StatusNoData {
		t.Errorf("no bars → %s, want NO_DATA", got.Status)
	}
	got := ComputePivot(one, cfg)
	if got.Pivot != 0 || got.NearestLevel != "" || got.Location != LocationUnknown {
		t.Errorf("an INSUFFICIENT_DATA result carried values: %+v", got)
	}
}

// A previous session with no range at all (H=L=C) collapses every level onto one price.
// That must stay finite and must not be reported as a breakout.
func TestPivotZeroRangeSession(t *testing.T) {
	bars := []Bar{
		{Date: testStart, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000},
		{Date: testStart.AddDate(0, 0, 1), Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000},
	}
	got := ComputePivot(bars, DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if !allFinite(got.Pivot, got.R1, got.R2, got.R3, got.S1, got.S2, got.S3, got.DistancePct) {
		t.Fatalf("non-finite levels from a zero-range session: %+v", got)
	}
	if got.Location == LocAboveResistance || got.Location == LocBelowSupport {
		t.Errorf("a zero-range session reported %s", got.Location)
	}
}

func TestPivotIsDeterministic(t *testing.T) {
	bars := pivotFixture(105)
	if ComputePivot(bars, DefaultConfig()) != ComputePivot(bars, DefaultConfig()) {
		t.Error("same input gave different results")
	}
}
