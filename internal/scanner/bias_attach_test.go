package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func candlesFromCloses(closes []float64) []fetcher.Candle {
	out := make([]fetcher.Candle, len(closes))
	base := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	for i, c := range closes {
		out[i] = fetcher.Candle{Date: base.AddDate(0, 0, i), Close: c, High: c, Low: c, Volume: 1000}
	}
	return out
}

func TestComputeBiasRiskLabels(t *testing.T) {
	th := DefaultBiasThresholds()
	// Build 60 closes all 100 then set last to hit a target BIAS20.
	mk := func(last float64) []fetcher.Candle {
		cl := make([]float64, 60)
		for i := range cl {
			cl[i] = 100
		}
		cl[59] = last
		return candlesFromCloses(cl)
	}
	// With 19 of the last-20 at 100 and last=X, SMA20=(1900+X)/20.
	cases := []struct {
		last float64
		want string
	}{
		{100, BiasRiskNormal},
		{130, BiasRiskExtreme}, // ~+28%
		{85, BiasRiskOversold}, // ~-14%? check band
	}
	for _, c := range cases {
		bv := computeBias(mk(c.last), th)
		if bv.Bias20 == nil {
			t.Fatalf("bias20 nil for last=%v", c.last)
		}
		if bv.Risk != c.want {
			t.Errorf("last=%v BIAS20=%.2f risk=%q want %q", c.last, *bv.Bias20, bv.Risk, c.want)
		}
	}
}

func TestComputeBiasInsufficientHistory(t *testing.T) {
	bv := computeBias(candlesFromCloses([]float64{10, 11, 12}), DefaultBiasThresholds())
	if bv.Bias20 != nil || bv.Bias60 != nil {
		t.Errorf("bias20/60 should be nil with 3 candles")
	}
	if bv.Risk != BiasRiskNA {
		t.Errorf("risk should be N/A when anchor unavailable, got %q", bv.Risk)
	}
}
