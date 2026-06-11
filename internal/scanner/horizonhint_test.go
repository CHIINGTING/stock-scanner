package scanner

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// hintCloses builds a candle slice carrying only Close prices (computeHorizonHint
// reads Close for MA60 + pullback depth; MA20/price come from StockAnalysis).
func hintCloses(cs ...float64) []fetcher.Candle {
	out := make([]fetcher.Candle, len(cs))
	for i, c := range cs {
		out[i] = fetcher.Candle{Close: c}
	}
	return out
}

// flatCloses returns n bars at v.
func flatCloses(n int, v float64) []fetcher.Candle {
	cs := make([]float64, n)
	for i := range cs {
		cs[i] = v
	}
	return hintCloses(cs...)
}

// pullbackCloses returns 20 bars: high then a final bar dropped by depthPct.
func pullbackCloses(depthPct float64) []fetcher.Candle {
	cs := make([]float64, 20)
	for i := range cs {
		cs[i] = 100
	}
	cs[len(cs)-1] = 100 * (1 - depthPct/100)
	return hintCloses(cs...)
}

func cShadow(quality, rs float64, flow MomentumFlow) *ShadowSignals {
	return &ShadowSignals{
		VCP:      &VCPResult{Computed: true, Valid: true, QualityScore: quality},
		RS:       &RSResult{Computed: true, RSRankPercentile: rs},
		Momentum: &MomentumState{Computed: true, Flow: flow},
	}
}

func bShadow(dist, newHighScore, rs float64) *ShadowSignals {
	return &ShadowSignals{
		NewHigh: &NewHighResult{Computed: true, DistanceFrom52wHighPct: dist, NewHighScore: newHighScore},
		RS:      &RSResult{Computed: true, RSRankPercentile: rs},
	}
}

// 1. Thin data → Computed=false.
func TestHintThinDataNotComputed(t *testing.T) {
	h := computeHorizonHint(StockAnalysis{Close: 100, MA20: 100}, nil, flatCloses(10, 100))
	if h.Computed {
		t.Fatalf("len(closes)<20 must yield Computed=false")
	}
}

// 2. Primary horizon is always 20d with early 5/10 and reference 60.
func TestHintPrimaryHorizonConstant(t *testing.T) {
	h := computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, nil, flatCloses(30, 100))
	if h.PrimaryDays != 20 {
		t.Errorf("PrimaryDays = %d, want 20", h.PrimaryDays)
	}
	if len(h.EarlyDays) != 2 || h.EarlyDays[0] != 5 || h.EarlyDays[1] != 10 {
		t.Errorf("EarlyDays = %v, want [5 10]", h.EarlyDays)
	}
	if len(h.ReferenceDays) != 1 || h.ReferenceDays[0] != 60 {
		t.Errorf("ReferenceDays = %v, want [60]", h.ReferenceDays)
	}
}

// 3. C (VCP) matches and takes priority; quality / RS boundaries are inclusive at 70.
func TestHintSetupC(t *testing.T) {
	cs := flatCloses(30, 100)
	a := StockAnalysis{Close: 100, MA20: 100} // would also satisfy A; C must win

	if got := computeHorizonHint(a, cShadow(70, 70, MomentumContinuation), cs); got.MatchedSetup != "C_VCP_MA20_RETEST" {
		t.Errorf("quality=70,rs=70 → %s, want C_VCP_MA20_RETEST", got.MatchedSetup)
	}
	if got := computeHorizonHint(a, cShadow(69, 70, MomentumContinuation), cs); got.MatchedSetup == "C_VCP_MA20_RETEST" {
		t.Errorf("quality=69 must NOT match C")
	}
	if got := computeHorizonHint(a, cShadow(70, 69, MomentumContinuation), cs); got.MatchedSetup == "C_VCP_MA20_RETEST" {
		t.Errorf("rs=69 must NOT match C")
	}
	if got := computeHorizonHint(a, cShadow(70, 70, StructuralShiftDown), cs); got.MatchedSetup == "C_VCP_MA20_RETEST" {
		t.Errorf("Flow=STRUCTURAL_SHIFT_DOWN must NOT match C")
	}
}

// 4. B (52w-high pullback) matches, maps depth to nearest R6 bucket, respects gates.
func TestHintSetupB(t *testing.T) {
	a := StockAnalysis{Close: 90, MA20: 50} // far from MA20 so A can't pre-empt; C off (no VCP)

	got := computeHorizonHint(a, bShadow(-10, 70, 70), pullbackCloses(10))
	if got.MatchedSetup != "B_PULLBACK_10" {
		t.Errorf("depth 10%% → %s, want B_PULLBACK_10", got.MatchedSetup)
	}
	if got := computeHorizonHint(a, bShadow(-10, 70, 70), pullbackCloses(16)); got.MatchedSetup != "B_PULLBACK_15" {
		t.Errorf("depth 16%% → %s, want nearest bucket B_PULLBACK_15", got.MatchedSetup)
	}
	// 52w-high gate: dist beyond -25 must not match B.
	if got := computeHorizonHint(a, bShadow(-30, 70, 70), pullbackCloses(10)); strings.HasPrefix(got.MatchedSetup, "B_PULLBACK") {
		t.Errorf("dist=-30 must NOT match B, got %s", got.MatchedSetup)
	}
	// NewHighScore gate.
	if got := computeHorizonHint(a, bShadow(-10, 59, 70), pullbackCloses(10)); strings.HasPrefix(got.MatchedSetup, "B_PULLBACK") {
		t.Errorf("NewHighScore=59 must NOT match B")
	}
	// Pullback out of [5,20] must not match B.
	if got := computeHorizonHint(a, bShadow(-10, 70, 70), pullbackCloses(2)); strings.HasPrefix(got.MatchedSetup, "B_PULLBACK") {
		t.Errorf("depth 2%% must NOT match B")
	}
}

// 5. A (MA pullback): near MA20 → MEDIUM; else near MA60 → LOW.
func TestHintSetupA(t *testing.T) {
	if got := computeHorizonHint(StockAnalysis{Close: 101, MA20: 100}, nil, flatCloses(30, 999)); got.MatchedSetup != "A_MA20_PULLBACK" || got.Confidence != "MEDIUM" {
		t.Errorf("near MA20 → %s/%s, want A_MA20_PULLBACK/MEDIUM", got.MatchedSetup, got.Confidence)
	}
	// Far from MA20 but near MA60 (flat series → MA60==price).
	if got := computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, nil, flatCloses(60, 100)); got.MatchedSetup != "A_MA60_PULLBACK" || got.Confidence != "LOW" {
		t.Errorf("near MA60 → %s/%s, want A_MA60_PULLBACK/LOW", got.MatchedSetup, got.Confidence)
	}
}

// 6. Default: nil shadow + far from MAs + <60 bars → DEFAULT, never panics, still 20d.
func TestHintDefaultAndNilSafe(t *testing.T) {
	got := computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, nil, flatCloses(30, 100))
	if got.MatchedSetup != "DEFAULT" || got.Confidence != "LOW" {
		t.Errorf("→ %s/%s, want DEFAULT/LOW", got.MatchedSetup, got.Confidence)
	}
	if !got.Computed || got.PrimaryDays != 20 {
		t.Errorf("DEFAULT must still be Computed with 20d primary")
	}
	// Partial shadow (only RS) must not panic and must not match C/B.
	partial := &ShadowSignals{RS: &RSResult{Computed: true, RSRankPercentile: 99}}
	if g := computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, partial, flatCloses(30, 100)); g.MatchedSetup != "DEFAULT" {
		t.Errorf("partial shadow → %s, want DEFAULT (safe degrade)", g.MatchedSetup)
	}
}

// 7. Report red line: no copy may read as a trade order. Scan every produced string.
func TestHintNoForbiddenTokens(t *testing.T) {
	forbidden := []string{"建議買入", "持有到第", "到期賣出", "自動停損", "BUY", "PLACE_ORDER", "AUTO_BUY"}
	hints := []HoldingHorizonHint{
		computeHorizonHint(StockAnalysis{Close: 100, MA20: 100}, cShadow(80, 80, MomentumContinuation), flatCloses(30, 100)),
		computeHorizonHint(StockAnalysis{Close: 90, MA20: 50}, bShadow(-10, 70, 70), pullbackCloses(10)),
		computeHorizonHint(StockAnalysis{Close: 101, MA20: 100}, nil, flatCloses(30, 999)),
		computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, nil, flatCloses(60, 100)),
		computeHorizonHint(StockAnalysis{Close: 100, MA20: 50}, nil, flatCloses(30, 100)),
	}
	for _, h := range hints {
		strs := append([]string{h.MatchedSetup}, h.Reason...)
		strs = append(strs, h.Caveat...)
		for _, s := range strs {
			for _, f := range forbidden {
				if strings.Contains(s, f) {
					t.Errorf("forbidden token %q in hint copy: %q (setup=%s)", f, s, h.MatchedSetup)
				}
			}
		}
	}
}
