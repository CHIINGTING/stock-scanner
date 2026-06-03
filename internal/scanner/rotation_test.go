package scanner

import (
	"math"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func TestClassifyStage(t *testing.T) {
	tests := []struct {
		name                                            string
		avgReturn20, avgRSI, newHigh, breakout, volExp float64
		want                                            RotationStage
	}{
		{"late: overextended", 30, 75, 80, 60, 50, LateRotation},
		{"hot: many new highs + high rsi", 18, 66, 60, 30, 40, HotRotation},
		{"confirmed: many breakouts", 8, 55, 20, 45, 35, ConfirmedRotation},
		{"early: volume up, money flowing", 4, 52, 10, 20, 50, EarlyRotation},
		{"early: dormant fallback", -2, 45, 0, 10, 10, EarlyRotation},
		// big return but RSI not yet overbought → not LATE; few new highs → not HOT
		{"confirmed not late when rsi low", 30, 60, 20, 45, 30, ConfirmedRotation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyStage(tt.avgReturn20, tt.avgRSI, tt.newHigh, tt.breakout, tt.volExp)
			if got != tt.want {
				t.Errorf("classifyStage(%v,%v,%v,%v,%v) = %q, want %q",
					tt.avgReturn20, tt.avgRSI, tt.newHigh, tt.breakout, tt.volExp, got, tt.want)
			}
		})
	}
}

func TestStageWeightOrdering(t *testing.T) {
	// EARLY/CONFIRMED must be boosted above neutral; HOT/LATE discounted below.
	if !(stageWeight(EarlyRotation) > 1 && stageWeight(ConfirmedRotation) > 1) {
		t.Errorf("EARLY/CONFIRMED should be boosted: early=%v confirmed=%v",
			stageWeight(EarlyRotation), stageWeight(ConfirmedRotation))
	}
	if !(stageWeight(HotRotation) < 1 && stageWeight(LateRotation) < 1) {
		t.Errorf("HOT/LATE should be discounted: hot=%v late=%v",
			stageWeight(HotRotation), stageWeight(LateRotation))
	}
	if !(stageWeight(EarlyRotation) > stageWeight(ConfirmedRotation)) {
		t.Errorf("EARLY should rank above CONFIRMED")
	}
	if !(stageWeight(HotRotation) > stageWeight(LateRotation)) {
		t.Errorf("HOT should rank above LATE")
	}
}

func TestNormalizeRelStrength(t *testing.T) {
	rs := []SectorRotation{
		{AvgReturn20: 10},
		{AvgReturn20: 0},
		{AvgReturn20: 5},
	}
	normalizeRelStrength(rs)
	if rs[0].RelStrength != 100 {
		t.Errorf("max return should map to 100, got %v", rs[0].RelStrength)
	}
	if rs[1].RelStrength != 0 {
		t.Errorf("min return should map to 0, got %v", rs[1].RelStrength)
	}
	if math.Abs(rs[2].RelStrength-50) > 0.01 {
		t.Errorf("mid return should map to ~50, got %v", rs[2].RelStrength)
	}

	// All equal → everyone gets 50.
	eq := []SectorRotation{{AvgReturn20: 3}, {AvgReturn20: 3}}
	normalizeRelStrength(eq)
	for i, r := range eq {
		if r.RelStrength != 50 {
			t.Errorf("equal momentum sector %d should be 50, got %v", i, r.RelStrength)
		}
	}
}

// makeCandles builds a synthetic price/volume series of length n.
// step is the daily close increment; vol is the constant daily volume.
func makeCandles(n int, start, step float64, vol int64) []fetcher.Candle {
	out := make([]fetcher.Candle, n)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c := start
	for i := 0; i < n; i++ {
		out[i] = fetcher.Candle{
			Date:   base.AddDate(0, 0, i),
			Open:   c,
			High:   c + 0.5,
			Low:    c - 0.5,
			Close:  c,
			Volume: vol,
		}
		c += step
	}
	return out
}

func TestScanRotationOrdersByOpportunity(t *testing.T) {
	s := New(Config{})

	// Strong steady uptrend (should score & rank well).
	strong := fetcher.StockData{Symbol: "AAA", Name: "Strong", Candles: makeCandles(70, 50, 0.8, 2_000_000)}
	// Flat / weak series (low score).
	weak := fetcher.StockData{Symbol: "BBB", Name: "Weak", Candles: makeCandles(70, 50, 0.0, 1_000_000)}

	order := []string{"上升族群", "盤整族群"}
	grouped := map[string][]fetcher.StockData{
		"上升族群": {strong},
		"盤整族群": {weak},
	}

	res := s.ScanRotation(order, grouped)
	if len(res) != 2 {
		t.Fatalf("expected 2 sectors, got %d", len(res))
	}
	// Results must be sorted by OppScore descending.
	if res[0].OppScore < res[1].OppScore {
		t.Errorf("results not sorted by OppScore: %v < %v", res[0].OppScore, res[1].OppScore)
	}
	// Score must be within 0–100 and stage assigned.
	for _, r := range res {
		if r.Score < 0 || r.Score > 100 {
			t.Errorf("%s score out of range: %v", r.Name, r.Score)
		}
		if r.Stage == "" {
			t.Errorf("%s missing stage", r.Name)
		}
		if len(r.Stocks) == 0 {
			t.Errorf("%s missing member stocks", r.Name)
		}
	}
}

func TestScanRotationSkipsEmptySectors(t *testing.T) {
	s := New(Config{})
	// Too few candles → member skipped → sector dropped.
	tiny := fetcher.StockData{Symbol: "CCC", Candles: makeCandles(10, 50, 1, 1_000_000)}
	res := s.ScanRotation([]string{"短資料族群"}, map[string][]fetcher.StockData{
		"短資料族群": {tiny},
	})
	if len(res) != 0 {
		t.Errorf("sector with no usable members should be dropped, got %d", len(res))
	}
}
