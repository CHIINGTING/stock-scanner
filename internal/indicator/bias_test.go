package indicator

import (
	"math"
	"testing"
)

func TestBIASBasic(t *testing.T) {
	// A 20-length series whose SMA20 == 100 and last close == 120 → BIAS20 = 20%.
	closes := make([]float64, 20)
	for i := range closes {
		closes[i] = 100
	}
	closes[19] = 120
	// SMA20 = (19*100 + 120)/20 = 101 → BIAS = (120-101)/101*100.
	got, ok := BIAS(closes, 20)
	if !ok {
		t.Fatal("expected ok")
	}
	want := (120.0 - 101.0) / 101.0 * 100
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("BIAS = %v, want %v", got, want)
	}
}

func TestBIASExactExample(t *testing.T) {
	// SPEC §13 example: price 120, MA20 100 → 20%. Construct so SMA20 == 100 exactly.
	closes := make([]float64, 20)
	// first 19 sum to 1880, last = 120 → total 2000 → SMA20 = 100.
	for i := 0; i < 19; i++ {
		closes[i] = 1880.0 / 19.0
	}
	closes[19] = 120
	got, ok := BIAS(closes, 20)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(got-20.0) > 1e-9 {
		t.Errorf("BIAS = %v, want 20", got)
	}
}

func TestBIASInsufficientHistory(t *testing.T) {
	if _, ok := BIAS([]float64{1, 2, 3}, 20); ok {
		t.Error("expected ok=false for <period history")
	}
	if _, ok := BIAS(nil, 5); ok {
		t.Error("expected ok=false for empty series")
	}
}

func TestBIASZeroMA(t *testing.T) {
	// SMA == 0 → undefined, must return ok=false (no NaN/divide-by-zero).
	if _, ok := BIAS([]float64{0, 0, 0, 0, 0}, 5); ok {
		t.Error("expected ok=false when SMA==0")
	}
}
