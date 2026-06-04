package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// rocketInputWithBQ builds a rocketInput over a flat synthetic series, overriding
// the consolidation BaseQualityScore so VCP correction can be tested precisely.
func rocketInputWithBQ(bq float64, vcp *VCPResult, guardrail bool) rocketInput {
	candles := makeCandles(60, 50, 0.0, 1_000_000) // flat → minimal extra g3 terms
	s := New(Config{})
	ind := s.calcIndicators(candles)
	consol := analyzeConsolidation(candles, ind, false)
	consol.BaseQualityScore = bq
	return rocketInput{
		candles:          candles,
		ind:              ind,
		consol:           consol,
		bt:               Backtest{},
		flowDir:          FlowNeutral,
		guardrailScoring: guardrail,
		vcp:              vcp,
	}
}

// C6b-1 unit tests: VCP may only correct g3's base-quality slot, gated by the
// master flag, using max() (never lowering), and never touching other groups.
func TestC6b1VCPCorrectsBaseQualityOnly(t *testing.T) {
	baseline := computeRocket(rocketInputWithBQ(50, nil, false)).Score

	validHigh := &VCPResult{Computed: true, Valid: true, QualityScore: 90}
	validLow := &VCPResult{Computed: true, Valid: true, QualityScore: 30}
	invalid := &VCPResult{Computed: true, Valid: false, QualityScore: 90}

	// A. master OFF + valid high VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, validHigh, false)).Score; got != baseline {
		t.Errorf("master off must not change score: got %d want %d", got, baseline)
	}
	// B. master ON + nil VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, nil, true)).Score; got != baseline {
		t.Errorf("nil VCP must not change score: got %d want %d", got, baseline)
	}
	// C. master ON + invalid VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, invalid, true)).Score; got != baseline {
		t.Errorf("invalid VCP must not change score: got %d want %d", got, baseline)
	}
	// D. master ON + valid VCP whose quality < base → max() keeps base → no change.
	if got := computeRocket(rocketInputWithBQ(50, validLow, true)).Score; got != baseline {
		t.Errorf("VCP quality below base must not lower score: got %d want %d", got, baseline)
	}
	// E. master ON + valid VCP whose quality > base → raised, but bounded by the g3
	// base-quality weight (delta ≤ (90-50)/100*12 ≈ 4.8 → ≤5).
	raised := computeRocket(rocketInputWithBQ(50, validHigh, true)).Score
	if raised <= baseline {
		t.Errorf("VCP quality above base should raise score: got %d baseline %d", raised, baseline)
	}
	if raised-baseline > 5 {
		t.Errorf("VCP correction exceeded the g3 base-quality weight: delta=%d (>5)", raised-baseline)
	}
}

// TestC6b1MasterFlagOffGolden: with the master flag OFF, enabling VCP only attaches
// shadow (C6a) and must NOT change RocketScore / WatchAction / ExplosionProb / order.
func TestC6b1MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	// VCP shadow computed, but master flag OFF → scoring must be unchanged.
	got := New(Config{EnableVCP: true}).EnrichWatchlist(items, so, rt, mb, nil)

	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol ||
			got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction ||
			got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off VCP changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.VCP == nil {
			t.Errorf("%s: VCP shadow should still be attached", base[i].A.Symbol)
		}
	}
}
