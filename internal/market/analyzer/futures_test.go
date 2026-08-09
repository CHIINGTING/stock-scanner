package analyzer

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func TestFuturesTrend(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Futures
	cases := []struct {
		change float64
		want   string
	}{
		{-5000, FuturesAggressiveShort},
		{-3000, FuturesAggressiveShort}, // boundary: <=
		{-2999, FuturesMoreShort},
		{-500, FuturesMoreShort}, // boundary: <=
		{-499, FuturesStable},
		{0, FuturesStable},
		{500, FuturesStable}, // boundary: <=
		{501, FuturesCoverShort},
		{3000, FuturesCoverShort}, // boundary: <=
		{3001, FuturesAggressiveCover},
		{9000, FuturesAggressiveCover},
	}
	for _, c := range cases {
		if got, _ := futuresTrend(c.change, th); got != c.want {
			t.Errorf("futuresTrend(%.0f) = %q, want %q", c.change, got, c.want)
		}
	}
}

func TestFuturesExtreme(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Futures
	cases := []struct {
		net  float64
		want string
	}{
		{-80000, ExtremeExtremeShort},
		{-70001, ExtremeExtremeShort},
		{-70000, ExtremeHeavyShort}, // boundary: not < -70000
		{-50001, ExtremeHeavyShort},
		{-50000, ExtremeShort}, // boundary
		{-30001, ExtremeShort},
		{-30000, ExtremeLightShort}, // boundary: <= -10000 reached later; -30000 not < -30000
		{-10000, ExtremeLightShort}, // boundary: <=
		{-9999, ExtremeNeutral},
		{0, ExtremeNeutral},
		{50000, ExtremeNeutral},
	}
	for _, c := range cases {
		if got := futuresExtreme(c.net, th); got != c.want {
			t.Errorf("futuresExtreme(%.0f) = %q, want %q", c.net, got, c.want)
		}
	}
}

// The spec's worked example: Net -83390, Δ-2324 → More Short trend + Extreme Short indicator.
func TestAnalyzeFuturesSpecExample(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Futures
	m := AnalyzeFutures(model.FuturesData{NetPosition: -83390, DailyChange: -2324}, 20, th)
	if m.State != FuturesMoreShort {
		t.Errorf("trend = %q, want %q", m.State, FuturesMoreShort)
	}
	if !m.Available {
		t.Error("module should be available")
	}
	if got := FuturesExtreme(-83390, th); got != ExtremeExtremeShort {
		t.Errorf("extreme = %q, want %q", got, ExtremeExtremeShort)
	}
}
