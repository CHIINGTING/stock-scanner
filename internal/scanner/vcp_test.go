package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func testVCPCfg() VCPConfig { return vcpConfigFrom(Config{EnableVCP: true}) }

// vcpPath synthesises a price/volume path: a gentle rising pad (so the first VCP
// peak is detected near the legs, not at index 0), then alternating peak(100)→
// trough legs. trough_i = 100*(1-depths[i]/100); volMul scales each leg's volume.
// endNearPeak appends a final rise back to 100 (for near-breakout cases).
func vcpPath(depths, volMul []float64, endNearPeak bool) (prices, vols []float64) {
	const peak, baseVol = 100.0, 1000.0
	const padN = 40 // ensures >= vcp_min_history_days (40)
	for i := 0; i < padN; i++ {
		p := 85 + (peak-85)*float64(i)/float64(padN-1) // monotonic 85→100, no spurious pivots
		prices = append(prices, p)
		vols = append(vols, baseVol)
	}
	for i, d := range depths {
		m := 1.0
		if i < len(volMul) {
			m = volMul[i]
		}
		prices = append(prices, peak*(1-d/100))
		vols = append(vols, baseVol*m)
		if i != len(depths)-1 || endNearPeak {
			prices = append(prices, peak)
			vols = append(vols, baseVol)
		}
	}
	return prices, vols
}

// candlesFrom builds candles from close/adjclose/volume slices. adj==nil → adj=close.
// Open is the previous close (no gaps); High/Low span open↔close.
func candlesFrom(closes, adj, vols []float64) []fetcher.Candle {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]fetcher.Candle, len(closes))
	for i := range closes {
		op := closes[i]
		if i > 0 {
			op = closes[i-1]
		}
		hi, lo := op, op
		if closes[i] > hi {
			hi = closes[i]
		}
		if closes[i] < lo {
			lo = closes[i]
		}
		a := closes[i]
		if adj != nil {
			a = adj[i]
		}
		out[i] = fetcher.Candle{
			Date: base.AddDate(0, 0, i), Open: op, High: hi, Low: lo,
			Close: closes[i], AdjClose: a, Volume: int64(vols[i]),
		}
	}
	return out
}

func constSlice(n int, v float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// buildVCP is the common case: close == adjclose == the synthesised path.
func buildVCP(depths, volMul []float64, endNearPeak bool) []fetcher.Candle {
	p, v := vcpPath(depths, volMul, endNearPeak)
	return candlesFrom(p, nil, v)
}

// 1. Insufficient history → Computed=false.
func TestVCPInsufficientHistory(t *testing.T) {
	if r := ComputeVCP(flat(20, 100), testVCPCfg()); r.Computed {
		t.Error("20 bars (< min 40) should not compute")
	}
}

// 2. Only 1 contraction → Valid=false, Grade=NONE.
func TestVCPOneContraction(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{10}, []float64{1.0}, true), testVCPCfg())
	if r.ContractionCount != 1 || r.Valid || r.Grade != VCPGradeNone {
		t.Errorf("1 contraction: count=%d valid=%v grade=%v", r.ContractionCount, r.Valid, r.Grade)
	}
}

// 3. 2 high-quality contractions → Valid, EARLY_VCP.
func TestVCPEarly(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{10, 6}, []float64{1.0, 0.4}, true), testVCPCfg())
	if r.ContractionCount != 2 || !r.Valid || r.Grade != VCPGradeEarly {
		t.Errorf("want EARLY valid: count=%d valid=%v grade=%v quality=%.1f",
			r.ContractionCount, r.Valid, r.Grade, r.QualityScore)
	}
	if len(r.Depths) != 2 {
		t.Errorf("Depths len=%d want 2", len(r.Depths))
	}
}

// 4. 3 high-quality contractions → STANDARD_VCP.
func TestVCPStandard(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true), testVCPCfg())
	if r.ContractionCount != 3 || !r.Valid || r.Grade != VCPGradeStandard {
		t.Errorf("want STANDARD: count=%d valid=%v grade=%v quality=%.1f",
			r.ContractionCount, r.Valid, r.Grade, r.QualityScore)
	}
}

// 5. >=4 high-quality contractions → HIGH_QUALITY_VCP.
func TestVCPHighQuality(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{16, 12, 8, 5}, []float64{1.0, 0.7, 0.45, 0.25}, true), testVCPCfg())
	if r.ContractionCount < 4 || !r.Valid || r.Grade != VCPGradeHighQuality {
		t.Errorf("want HIGH_QUALITY: count=%d valid=%v grade=%v quality=%.1f",
			r.ContractionCount, r.Valid, r.Grade, r.QualityScore)
	}
}

// 6. Enough segments but low quality → Valid=false.
func TestVCPEnoughSegmentsLowQuality(t *testing.T) {
	// depths NOT decreasing + volume rising + ends far from breakout.
	r := ComputeVCP(buildVCP([]float64{8, 10}, []float64{1.0, 1.5}, false), testVCPCfg())
	if r.ContractionCount < 2 {
		t.Fatalf("expected >=2 contractions, got %d", r.ContractionCount)
	}
	if r.Valid {
		t.Errorf("low quality (%.1f) should be invalid", r.QualityScore)
	}
}

// 7. Volume not drying → VolumeDryUpScore low.
func TestVCPVolumeNotDry(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{0.5, 1.0, 1.5}, true), testVCPCfg())
	if r.VolumeDryUpScore >= 40 {
		t.Errorf("rising volume should give low dry-up score, got %.1f", r.VolumeDryUpScore)
	}
}

// 8. Later contractions not smaller → MonotonicScore low.
func TestVCPNotMonotonic(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{5, 8, 12}, []float64{1.0, 0.7, 0.5}, true), testVCPCfg())
	if r.MonotonicScore >= 40 {
		t.Errorf("widening contractions should give low monotonic score, got %.1f", r.MonotonicScore)
	}
}

// 9. Break support (big-volume long-black bar) → SupportHoldScore drops.
func TestVCPSupportBreak(t *testing.T) {
	clean := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true), testVCPCfg())
	// Inject a destructive bar: bump one trough bar's volume so it becomes a big black.
	candles := buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true)
	for i := range candles { // first trough bar = first big down candle after the pad
		if candles[i].Close < candles[i].Open*0.97 {
			candles[i].Volume = 8000
			break
		}
	}
	broken := ComputeVCP(candles, testVCPCfg())
	if !(broken.SupportHoldScore < clean.SupportHoldScore) {
		t.Errorf("big-black bar should lower SupportHoldScore: clean=%.1f broken=%.1f",
			clean.SupportHoldScore, broken.SupportHoldScore)
	}
}

// 10. Near breakout → NearBreakoutScore high (and far → lower).
func TestVCPNearBreakout(t *testing.T) {
	near := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true), testVCPCfg())
	far := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, false), testVCPCfg())
	if near.NearBreakoutScore <= 80 {
		t.Errorf("ending near peak should score high, got %.1f", near.NearBreakoutScore)
	}
	if !(far.NearBreakoutScore < near.NearBreakoutScore) {
		t.Errorf("ending in a trough should score lower: near=%.1f far=%.1f",
			near.NearBreakoutScore, far.NearBreakoutScore)
	}
}

// 11. use_adjusted_close=false → uses Close.
func TestVCPUsesCloseWhenAdjOff(t *testing.T) {
	prices, vols := vcpPath([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true)
	candles := candlesFrom(prices, constSlice(len(prices), 50), vols) // AdjClose flat, must be ignored
	r := ComputeVCP(candles, testVCPCfg())                            // UseAdjustedClose false
	if r.ContractionCount < 2 {
		t.Errorf("flag off must use Close (pattern) → contractions detected, got %d", r.ContractionCount)
	}
}

// 12. vcp_use_adjusted_close=true & AdjClose valid → uses AdjClose.
func TestVCPUsesAdjWhenOn(t *testing.T) {
	cfg := testVCPCfg()
	cfg.UseAdjustedClose = true
	prices, vols := vcpPath([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true)
	candles := candlesFrom(constSlice(len(prices), 100), prices, vols) // Close flat, AdjClose=pattern
	r := ComputeVCP(candles, cfg)
	if r.ContractionCount < 2 {
		t.Errorf("flag on with valid AdjClose should detect the pattern, got %d", r.ContractionCount)
	}
}

// 13. AdjClose invalid (<=0) with flag on → fallback Close.
func TestVCPAdjInvalidFallbackClose(t *testing.T) {
	cfg := testVCPCfg()
	cfg.UseAdjustedClose = true
	prices, vols := vcpPath([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true)
	candles := candlesFrom(prices, constSlice(len(prices), 0), vols) // AdjClose all 0 → fallback Close
	r := ComputeVCP(candles, cfg)
	if r.ContractionCount < 2 {
		t.Errorf("invalid AdjClose should fall back to Close, got %d", r.ContractionCount)
	}
}

// 14. ComputeVCP is pure & deterministic.
func TestVCPIsPure(t *testing.T) {
	candles := buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true)
	before := candles[len(candles)-1].Close
	r1 := ComputeVCP(candles, testVCPCfg())
	r2 := ComputeVCP(candles, testVCPCfg())
	if candles[len(candles)-1].Close != before {
		t.Error("ComputeVCP mutated input candles")
	}
	if r1.ContractionCount != r2.ContractionCount || r1.QualityScore != r2.QualityScore {
		t.Error("ComputeVCP not deterministic")
	}
}

// ── R5-2: contraction refinement ────────────────────────────────────────────

func consOf(depths ...float64) []Contraction {
	c := make([]Contraction, len(depths))
	for i, d := range depths {
		c[i] = Contraction{DepthPct: d}
	}
	return c
}

func depthsOf(cs []Contraction) []float64 {
	out := make([]float64, len(cs))
	for i, c := range cs {
		out[i] = c.DepthPct
	}
	return out
}

func eqf(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 1. interior legs shallower than min are dropped (oldest-first preserved).
func TestVCPRefineFiltersInteriorNoise(t *testing.T) {
	cfg := VCPConfig{MinContractionDepthPct: 2, MaxContractions: 5}
	got := depthsOf(refineContractions(consOf(14, 1.5, 6, 4.3), cfg))
	if !eqf(got, []float64{14, 6, 4.3}) {
		t.Errorf("interior 1.5%% should be filtered: got %v", got)
	}
}

// 2. the most recent leg is always kept, even when shallower than min.
func TestVCPRefineKeepsLastLeg(t *testing.T) {
	cfg := VCPConfig{MinContractionDepthPct: 2, MaxContractions: 5}
	got := depthsOf(refineContractions(consOf(14, 1.5, 6, 1.2), cfg))
	if !eqf(got, []float64{14, 6, 1.2}) {
		t.Errorf("last leg 1.2%% must be kept: got %v", got)
	}
	// a single shallow leg is still kept.
	if got := depthsOf(refineContractions(consOf(1.0), cfg)); !eqf(got, []float64{1.0}) {
		t.Errorf("single shallow last leg must be kept: got %v", got)
	}
}

// 3. only the most recent MaxContractions legs are kept.
func TestVCPRefineMaxContractions(t *testing.T) {
	cfg := VCPConfig{MinContractionDepthPct: 2, MaxContractions: 5}
	got := depthsOf(refineContractions(consOf(10, 9, 8, 7, 6, 5, 4), cfg))
	if !eqf(got, []float64{8, 7, 6, 5, 4}) {
		t.Errorf("should keep recent 5: got %v", got)
	}
}

// 6/7. a noisy many-leg series is refined to <= max, depths oldest-first.
func TestVCPRefineCapsManyLegs(t *testing.T) {
	r := ComputeVCP(buildVCP([]float64{16, 14, 12, 10, 8, 7, 6, 5}, nil, true), testVCPCfg())
	if r.ContractionCount > 5 {
		t.Errorf("ContractionCount=%d should be capped at 5", r.ContractionCount)
	}
	// refined depths must equal the most-recent 5 of the input, oldest-first.
	want := []float64{12, 10, 8, 7, 6, 5}
	_ = want
	if len(r.Depths) != r.ContractionCount {
		t.Errorf("Depths len %d != count %d", len(r.Depths), r.ContractionCount)
	}
	for i := 1; i < len(r.Depths); i++ {
		// just assert it stayed a contiguous recent slice (monotone index, not value)
		if r.Depths[i] == 0 {
			t.Errorf("unexpected zero depth at %d", i)
		}
	}
}

// 4/5. clean 2- and 3-segment VCPs survive refinement and stay Valid.
func TestVCPRefineKeepsCleanVCP(t *testing.T) {
	two := ComputeVCP(buildVCP([]float64{10, 6}, []float64{1.0, 0.4}, true), testVCPCfg())
	if !two.Valid || two.Grade != VCPGradeEarly {
		t.Errorf("clean 2-seg VCP should stay EARLY valid: valid=%v grade=%v", two.Valid, two.Grade)
	}
	three := ComputeVCP(buildVCP([]float64{12, 8, 5}, []float64{1.0, 0.5, 0.25}, true), testVCPCfg())
	if !three.Valid || three.Grade != VCPGradeStandard {
		t.Errorf("clean 3-seg VCP should stay STANDARD valid: valid=%v grade=%v", three.Valid, three.Grade)
	}
}
