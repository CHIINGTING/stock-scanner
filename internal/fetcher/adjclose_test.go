package fetcher

import "testing"

// TestPriceForCalc covers the C1 fallback contract: AdjClose is used only when
// the flag is on AND the value is valid (>0); otherwise we always fall back to
// the raw Close, so a missing/zero AdjClose can never produce a misleading price.
func TestPriceForCalc(t *testing.T) {
	tests := []struct {
		name        string
		candle      Candle
		useAdjusted bool
		want        float64
	}{
		{
			name:        "adj present & flag on → AdjClose",
			candle:      Candle{Close: 100, AdjClose: 90},
			useAdjusted: true,
			want:        90,
		},
		{
			name:        "adj present but flag off → Close",
			candle:      Candle{Close: 100, AdjClose: 90},
			useAdjusted: false,
			want:        100,
		},
		{
			name:        "adj missing (zero) & flag on → fallback Close",
			candle:      Candle{Close: 100, AdjClose: 0},
			useAdjusted: true,
			want:        100,
		},
		{
			name:        "adj invalid (negative) & flag on → fallback Close",
			candle:      Candle{Close: 100, AdjClose: -5},
			useAdjusted: true,
			want:        100,
		},
		{
			name:        "adj missing & flag off → Close",
			candle:      Candle{Close: 100},
			useAdjusted: false,
			want:        100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PriceForCalc(tt.candle, tt.useAdjusted); got != tt.want {
				t.Errorf("PriceForCalc() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPriceForCalcFlagOffIgnoresAdjClose asserts the golden-regression guarantee
// at the helper level: with the flag off, the result equals raw Close regardless
// of AdjClose — so enabling the field cannot change existing calculations.
func TestPriceForCalcFlagOffIgnoresAdjClose(t *testing.T) {
	for _, adj := range []float64{0, -1, 50, 100, 9999} {
		c := Candle{Close: 123.4, AdjClose: adj}
		if got := PriceForCalc(c, false); got != c.Close {
			t.Errorf("flag off with AdjClose=%v: got %v, want Close=%v", adj, got, c.Close)
		}
	}
}
