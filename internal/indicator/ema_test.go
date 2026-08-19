package indicator

import (
	"math"
	"testing"
)

func TestEMASeedsWithSMA(t *testing.T) {
	src := []float64{1, 2, 3, 4, 5, 6}
	got := EMA(src, 3)

	for i := 0; i < 2; i++ {
		if got[i] != 0 {
			t.Errorf("warm-up index %d = %v, want the package's 0 convention", i, got[i])
		}
	}
	// The first published value is the SMA of the first window: (1+2+3)/3.
	if math.Abs(got[2]-2) > 1e-12 {
		t.Errorf("seed = %v, want the SMA 2", got[2])
	}
	// k = 2/(3+1) = 0.5 → (4-2)*0.5 + 2 = 3.
	if math.Abs(got[3]-3) > 1e-12 {
		t.Errorf("EMA[3] = %v, want 3", got[3])
	}
	if math.Abs(got[4]-4) > 1e-12 {
		t.Errorf("EMA[4] = %v, want 4", got[4])
	}
}

// A constant series must give a constant EMA equal to that constant — the classic check
// that the smoothing weights sum correctly.
func TestEMAOfAConstantIsTheConstant(t *testing.T) {
	src := make([]float64, 50)
	for i := range src {
		src[i] = 42
	}
	got := EMA(src, 12)
	for i := 11; i < len(got); i++ {
		if math.Abs(got[i]-42) > 1e-9 {
			t.Fatalf("EMA[%d] = %v, want 42", i, got[i])
		}
	}
}

func TestEMAInsufficientOrInvalidInput(t *testing.T) {
	if got := EMA([]float64{1, 2}, 5); len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Errorf("too-short input = %v, want all zeros", got)
	}
	if got := EMA([]float64{1, 2, 3}, 0); len(got) != 3 || got[2] != 0 {
		t.Errorf("period 0 = %v, want all zeros", got)
	}
	if got := EMA(nil, 3); len(got) != 0 {
		t.Errorf("nil input = %v, want empty", got)
	}
}

// EMA must react faster than SMA to a step change — the property MACD depends on.
func TestEMALeadsSMA(t *testing.T) {
	src := make([]float64, 40)
	for i := range src {
		src[i] = 100
	}
	for i := 20; i < len(src); i++ {
		src[i] = 120
	}
	ema := EMA(src, 10)
	sma := SMA(src, 10)
	// Two bars after the step, the EMA has travelled further than the SMA.
	if ema[22] <= sma[22] {
		t.Errorf("EMA %v should lead SMA %v after a step up", ema[22], sma[22])
	}
}
