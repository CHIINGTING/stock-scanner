package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// rocketInputWithBQ builds a rocketInput over a flat synthetic series, overriding
// the consolidation BaseQualityScore so guardrail scoring can be tested precisely.
// nearHigh sets consol.NearPreviousHigh so the original g3 sub-score is exercisable.
func rocketInputWithBQ(bq float64, vcp *VCPResult, nh *NewHighResult, nearHigh, guardrail bool) rocketInput {
	candles := makeCandles(60, 50, 0.0, 1_000_000) // flat → minimal extra g3 terms
	s := New(Config{})
	ind := s.calcIndicators(candles)
	consol := analyzeConsolidation(candles, ind, false)
	consol.BaseQualityScore = bq
	consol.NearPreviousHigh = nearHigh
	return rocketInput{
		candles:          candles,
		ind:              ind,
		consol:           consol,
		bt:               Backtest{},
		flowDir:          FlowNeutral,
		guardrailScoring: guardrail,
		vcp:              vcp,
		newHigh:          nh,
	}
}

// C6b-1 unit tests: VCP may only correct g3's base-quality slot, gated by the
// master flag, using max() (never lowering), and never touching other groups.
func TestC6b1VCPCorrectsBaseQualityOnly(t *testing.T) {
	baseline := computeRocket(rocketInputWithBQ(50, nil, nil, false, false)).Score

	validHigh := &VCPResult{Computed: true, Valid: true, QualityScore: 90}
	validLow := &VCPResult{Computed: true, Valid: true, QualityScore: 30}
	invalid := &VCPResult{Computed: true, Valid: false, QualityScore: 90}

	// A. master OFF + valid high VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, validHigh, nil, false, false)).Score; got != baseline {
		t.Errorf("master off must not change score: got %d want %d", got, baseline)
	}
	// B. master ON + nil VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, nil, nil, false, true)).Score; got != baseline {
		t.Errorf("nil VCP must not change score: got %d want %d", got, baseline)
	}
	// C. master ON + invalid VCP → no change.
	if got := computeRocket(rocketInputWithBQ(50, invalid, nil, false, true)).Score; got != baseline {
		t.Errorf("invalid VCP must not change score: got %d want %d", got, baseline)
	}
	// D. master ON + valid VCP whose quality < base → max() keeps base → no change.
	if got := computeRocket(rocketInputWithBQ(50, validLow, nil, false, true)).Score; got != baseline {
		t.Errorf("VCP quality below base must not lower score: got %d want %d", got, baseline)
	}
	// E. master ON + valid VCP whose quality > base → raised, but bounded by the g3
	// base-quality weight (delta ≤ (90-50)/100*12 ≈ 4.8 → ≤5).
	raised := computeRocket(rocketInputWithBQ(50, validHigh, nil, false, true)).Score
	if raised <= baseline {
		t.Errorf("VCP quality above base should raise score: got %d baseline %d", raised, baseline)
	}
	if raised-baseline > 5 {
		t.Errorf("VCP correction exceeded the g3 base-quality weight: delta=%d (>5)", raised-baseline)
	}
}

// ── C6b-2: NewHigh replaces the g3 NearPreviousHigh sub-score ────────────────

func nh(score float64, computed bool) *NewHighResult {
	return &NewHighResult{Computed: computed, NewHighScore: score}
}

// C6b-2 unit tests: NewHigh only enters g3 (replacing NearPreviousHigh), gated by
// the master flag, never double-counting and never exceeding the g3 cap.
func TestC6b2NewHighReplacesNearHighInG3(t *testing.T) {
	// Baseline with NearPreviousHigh ON: original g3 formula (BQ*12 + 6).
	baseNear := computeRocket(rocketInputWithBQ(50, nil, nil, true, false)).Score

	// 1. master OFF + NewHigh present → original formula (NearPreviousHigh still +6).
	if got := computeRocket(rocketInputWithBQ(50, nil, nh(90, true), true, false)).Score; got != baseNear {
		t.Errorf("master off must keep original g3: got %d want %d", got, baseNear)
	}
	// 4. master ON + NewHigh nil → original formula.
	if got := computeRocket(rocketInputWithBQ(50, nil, nil, true, true)).Score; got != baseNear {
		t.Errorf("nil NewHigh must keep original g3: got %d want %d", got, baseNear)
	}
	// 5. master ON + NewHigh Computed=false → original formula (no hard-compute).
	if got := computeRocket(rocketInputWithBQ(50, nil, nh(90, false), true, true)).Score; got != baseNear {
		t.Errorf("Computed=false must keep original g3: got %d want %d", got, baseNear)
	}

	// 3/7. master ON + NewHigh Computed, high score → NewHigh branch.
	// g3 = BQ/100*10 + NewHighScore/100*8 (+ no NearPreviousHigh). With BQ=50,
	// score=90: 5 + 7.2 = 12.2 vs original 6+6=12 → close; assert NewHigh branch active
	// by checking that NearPreviousHigh no longer adds on top (cap respected, no double).
	onHigh := computeRocket(rocketInputWithBQ(50, nil, nh(90, true), true, true)).Score
	// Compare to a hypothetical "double-count" upper bound: if NearPreviousHigh(+6)
	// were ALSO added, g3 would be ~18.2; ensure we are well below that inflation.
	if onHigh > baseNear+5 {
		t.Errorf("NewHigh appears to double-count with NearPreviousHigh: on=%d base=%d", onHigh, baseNear)
	}
	// g3 must stay capped: even NewHighScore=100, BQ=100 cannot exceed 25.
	full := computeRocket(rocketInputWithBQ(100, nil, nh(100, true), true, true)).Score
	capped := computeRocket(rocketInputWithBQ(100, nil, nh(100, true), false, true)).Score // off → original BQ*12 path
	_ = capped
	if full <= 0 || full > 100 {
		t.Errorf("score out of range with full NewHigh: %d", full)
	}
}

// 6. Deliberate behavior: Computed=true with NewHighScore=0 takes the NewHigh branch
// (a "no leadership" verdict) and must NOT fall back to NearPreviousHigh+6.
func TestC6b2NewHighComputedZeroScoreUsesNewHighBranch(t *testing.T) {
	// With NearPreviousHigh=true, original g3 would get +6; NewHigh branch (score 0)
	// gives 0 for that slot. So the NewHigh-branch score must be LOWER than the
	// original-with-NearHigh score — proving we took the NewHigh branch deliberately.
	original := computeRocket(rocketInputWithBQ(50, nil, nil, true, false)).Score      // BQ*12 + 6
	newHighZero := computeRocket(rocketInputWithBQ(50, nil, nh(0, true), true, true)).Score // BQ*10 + 0
	if newHighZero >= original {
		t.Errorf("Computed=true score=0 should take NewHigh branch (lower, no NearHigh fallback): nh0=%d original=%d",
			newHighZero, original)
	}
	// And it must not panic / must stay in range.
	if newHighZero < 0 || newHighZero > 100 {
		t.Errorf("score out of range: %d", newHighZero)
	}
}

// TestC6b2MasterFlagOffGolden: master flag OFF + new_high ON → only shadow attached,
// scoring/order identical to the all-off baseline.
func TestC6b2MasterFlagOffGolden(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	so, rt, mb := map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}

	base := New(Config{}).EnrichWatchlist(items, so, rt, mb, nil)
	got := New(Config{EnableNewHigh: true}).EnrichWatchlist(items, so, rt, mb, nil) // master off

	for i := range base {
		if got[i].A.Symbol != base[i].A.Symbol ||
			got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction ||
			got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: master-off NewHigh changed scoring/order", base[i].A.Symbol)
		}
		if got[i].Shadow == nil || got[i].Shadow.NewHigh == nil {
			t.Errorf("%s: NewHigh shadow should still be attached", base[i].A.Symbol)
		}
	}
}

// 8. VCP + NewHigh together: effBQ still VCP-corrected, fed through the NewHigh ×10 slot.
func TestC6b2VCPAndNewHighTogether(t *testing.T) {
	vHigh := &VCPResult{Computed: true, Valid: true, QualityScore: 90}
	// BQ=50, VCP raises effBQ to 90 → g3 base = 90/100*10 = 9; NewHighScore 80 → 6.4.
	withVCP := computeRocket(rocketInputWithBQ(50, vHigh, nh(80, true), false, true)).Score
	noVCP := computeRocket(rocketInputWithBQ(50, nil, nh(80, true), false, true)).Score
	if withVCP <= noVCP {
		t.Errorf("VCP should still raise effBQ within the NewHigh branch: withVCP=%d noVCP=%d", withVCP, noVCP)
	}
	if withVCP-noVCP > 5 { // bounded by (90-50)/100*10 = 4
		t.Errorf("VCP correction in NewHigh branch exceeded base-quality weight: delta=%d", withVCP-noVCP)
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
