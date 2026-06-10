package r6backtest

import (
	"os"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// TestMain stubs the signal providers to benign, deterministic values for the
// whole package test run, so trigger tests exercise the PRICE gates without
// depending on momentum/MTF internals. A dedicated smoke test still covers the
// real providers.
func TestMain(m *testing.M) {
	flowProvider = func(*Stock, int) string { return "MOMENTUM_BUILDING" }
	mtfProvider = func(*Stock, int) string { return "STRONG" }
	os.Exit(m.Run())
}

// stubPanel returns a panel that reports the given percentile for every date a
// stock has, so RS gating can be controlled in tests.
func stubPanel(s *Stock, pct float64) *RSPanel {
	m := map[string]map[string]float64{}
	for d := range s.idxOf {
		m[d] = map[string]float64{s.Symbol: pct}
	}
	return &RSPanel{byDate: m}
}

// buildUptrend constructs a long, gently rising series (so 5>10>20>60 stack and
// the 52-week high sits just above price) then dips the LAST bar to a target
// fraction of MA20/MA60 with low volume — a clean pullback-touch at bar n-1.
func buildUptrend(n int, slopePerBar float64) []float64 {
	closes := make([]float64, n)
	p := 100.0
	for i := 0; i < n; i++ {
		closes[i] = p
		p *= (1 + slopePerBar)
	}
	return closes
}

func withRSI(s *Stock) *Stock { s.RSI14 = indicator.RSI(s.Close, 14); return s }

// helper: run a setup on a single stock at a forced RS percentile.
func detectAt(setup Setup, s *Stock, i int, pct float64) *Trigger {
	return setup.Detect(&Universe{Stocks: []*Stock{s}}, stubPanel(s, pct), s, i, DefaultParams())
}

// 1. Setup A MA20 triggers on a bullish pullback that taps MA20 (above MA60).
func TestSetupA_MA20_Triggers(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("A20", closes))
	// force a pullback tap: set last low to MA20*1.00, close just above MA20, above MA60.
	s.Low[i] = s.MA20[i] * 1.00
	s.Close[i] = s.MA20[i] * 1.005
	s.High[i] = s.Close[i] * 1.001
	// low volume on last 3 bars
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	if tr := detectAt(SetupA{Variant: "MA20"}, s, i, 85); tr == nil {
		t.Fatalf("MA20 pullback should trigger")
	}
}

// 2. Setup A MA60 triggers on a tap of MA60.
func TestSetupA_MA60_Triggers(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("A60", closes))
	s.Low[i] = s.MA60[i] * 1.00
	s.Close[i] = s.MA60[i] * 1.005
	s.High[i] = s.Close[i] * 1.001
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	if tr := detectAt(SetupA{Variant: "MA60"}, s, i, 85); tr == nil {
		t.Fatalf("MA60 pullback should trigger")
	}
}

// 3. Setup A MA20 does NOT trigger when support is broken (close below MA60).
func TestSetupA_SupportBroken_NoTrigger(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("Abrk", closes))
	s.Low[i] = s.MA20[i] * 1.00
	s.Close[i] = s.MA60[i] * 0.97 // below MA60 → support broken
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	if tr := detectAt(SetupA{Variant: "MA20"}, s, i, 85); tr != nil {
		t.Fatalf("broken support must not trigger")
	}
}

// 3b. Setup A does NOT trigger when RS < 70.
func TestSetupA_LowRS_NoTrigger(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("Alow", closes))
	s.Low[i] = s.MA20[i] * 1.00
	s.Close[i] = s.MA20[i] * 1.005
	if tr := detectAt(SetupA{Variant: "MA20"}, s, i, 50); tr != nil {
		t.Fatalf("RS<70 must not trigger")
	}
}

// 4. Setup B classifies pullback depth into the right bucket (cumulative).
func TestSetupB_BucketClassification(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("B", closes))
	recentHigh := maxHigh(s, i-19, i)
	// engineer an exact 12% pullback at the last bar.
	s.Close[i] = recentHigh * 0.88
	s.Low[i] = s.Close[i] * 0.999
	cases := map[int]bool{5: true, 8: true, 10: true, 15: false, 20: false}
	for bucket, want := range cases {
		got := detectAt(SetupB{Bucket: bucket}, s, i, 85) != nil
		if got != want {
			t.Errorf("12%% pullback, bucket %d: triggered=%v want=%v", bucket, got, want)
		}
	}
}

// 5. Setup B does NOT trigger when pullback depth is below the bucket.
func TestSetupB_ShallowPullback_NoTrigger(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("Bshallow", closes))
	recentHigh := maxHigh(s, i-19, i)
	s.Close[i] = recentHigh * 0.97 // only 3% pullback
	s.Low[i] = s.Close[i] * 0.999
	if tr := detectAt(SetupB{Bucket: 10}, s, i, 85); tr != nil {
		t.Fatalf("3%% pullback must not trigger bucket 10")
	}
}

// 6 & 7. Cooldown de-dups within a flat leg; a fresh 20-day high resets it.
func TestSetupB_CooldownAndReset(t *testing.T) {
	// 260 bars rising to bar 250, then a flat plateau ~10% below the high so SetupB(10)
	// would trigger every bar; cooldown must space the entries.
	n := 320
	closes := make([]float64, n)
	p := 100.0
	for i := 0; i < 250; i++ {
		closes[i] = p
		p *= 1.005
	}
	high := closes[249]
	for i := 250; i < n; i++ {
		closes[i] = high * 0.89 // ~11% below the recent high → bucket 10 triggers
	}
	s := withRSI(mkStock("Bcd", closes))
	p2 := DefaultParams()
	p2.Warmup = 251
	p2.Cooldown = 10
	p2.StopRules = nil
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, stubPanel(s, 85), SetupB{Bucket: 10}, p2)
	if len(trades) < 2 {
		t.Fatalf("expected multiple cooldown-spaced entries, got %d", len(trades))
	}
	prev := -1000
	for _, tr := range trades {
		idx := s.idxOf[tr.SignalDate.Format("2006-01-02")]
		if prev >= 0 && idx-prev < p2.Cooldown {
			t.Errorf("entries closer than cooldown: %d then %d", prev, idx)
		}
		prev = idx
	}
	// reset: inject a fresh 20-day high mid-plateau; that bar should be allowed
	// even if within cooldown of the prior entry.
	resetIdx := 265
	s2 := withRSI(mkStock("Brst", closes))
	s2.Close[resetIdx] = high * 1.2 // new 20-day high
	s2.High[resetIdx] = s2.Close[resetIdx] * 1.001
	// next bar pulls back >10% from that new high
	s2.Close[resetIdx+1] = s2.Close[resetIdx] * 0.88
	got := SetupB{Bucket: 10}.Detect(&Universe{Stocks: []*Stock{s2}}, stubPanel(s2, 85), s2, resetIdx+1, p2)
	if got == nil {
		t.Errorf("pullback from a fresh 20-day high should be detectable (leg reset)")
	}
}

// 7b. Engine-level reset: a fresh 20-day high between entries lets a second
//     entry through even inside the cooldown window.
func TestEngineCooldownReset(t *testing.T) {
	n := 320
	closes := make([]float64, n)
	p := 100.0
	for i := 0; i < 250; i++ {
		closes[i] = p
		p *= 1.005
	}
	h1 := closes[249]
	for i := 250; i <= 254; i++ { // plateau 1: ~11% below recent high → triggers bucket 10
		closes[i] = h1 * 0.89
	}
	closes[255] = h1 * 1.30 // fresh 20-day high (new leg)
	for i := 256; i < n; i++ { // plateau 2: ~11% below the NEW high
		closes[i] = closes[255] * 0.89
	}
	s := withRSI(mkStock("RST", closes))
	p2 := DefaultParams()
	p2.Warmup = 251
	p2.Cooldown = 10
	p2.StopRules = nil
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, stubPanel(s, 85), SetupB{Bucket: 10}, p2)
	// expect an entry in plateau 1 AND another in plateau 2 within 10 bars of it,
	// which is only possible because the new high reset the cooldown.
	var p1, p2e bool
	for _, tr := range trades {
		idx := s.idxOf[tr.SignalDate.Format("2006-01-02")]
		if idx >= 251 && idx <= 254 {
			p1 = true
		}
		if idx >= 256 && idx <= 260 {
			p2e = true
		}
	}
	if !p1 || !p2e {
		t.Errorf("expected entries in both legs (reset), got plateau1=%v plateau2=%v (n=%d)", p1, p2e, len(trades))
	}
}

// 10. Real signal providers run without panic on a synthetic series (integration
//     smoke for the un-stubbed path).
func TestRealSignalProvidersSmoke(t *testing.T) {
	s := withRSI(mkStock("SMK", buildUptrend(300, 0.004)))
	_ = asofMomentumFlow(s, 299)
	_ = asofMTFSignal(s, 299)
}

// 7c. The momentum gate rejects STRUCTURAL_SHIFT_DOWN even when price gates pass.
func TestSetup_ShiftDownRejected(t *testing.T) {
	old := flowProvider
	flowProvider = func(*Stock, int) string { return "STRUCTURAL_SHIFT_DOWN" }
	defer func() { flowProvider = old }()
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("SD", closes))
	s.Low[i] = s.MA20[i] * 1.00
	s.Close[i] = s.MA20[i] * 1.005
	s.High[i] = s.Close[i] * 1.001
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	if tr := detectAt(SetupA{Variant: "MA20"}, s, i, 85); tr != nil {
		t.Fatalf("STRUCTURAL_SHIFT_DOWN must block the trigger")
	}
}

// 8. No look-ahead: a setup's detection at bar i is unchanged when bars > i are
//    mutated (detectors only read candles[:i+1]).
func TestSetups_NoLookAhead(t *testing.T) {
	closes := buildUptrend(320, 0.004)
	i := 280
	s1 := withRSI(mkStock("LA1", closes))
	s1.Close[i] = maxHigh(s1, i-19, i) * 0.88
	s1.Low[i] = s1.Close[i] * 0.999
	c2 := append([]float64(nil), s1.Close...)
	for j := i + 1; j < len(c2); j++ {
		c2[j] = 9999
	}
	s2 := withRSI(mkStock("LA1", c2))
	s2.Low[i] = s1.Low[i]
	a := SetupB{Bucket: 10}.Detect(&Universe{Stocks: []*Stock{s1}}, stubPanel(s1, 85), s1, i, DefaultParams())
	b := SetupB{Bucket: 10}.Detect(&Universe{Stocks: []*Stock{s2}}, stubPanel(s2, 85), s2, i, DefaultParams())
	if (a == nil) != (b == nil) {
		t.Fatalf("future-only mutation changed detection: %v vs %v", a != nil, b != nil)
	}
}

// 9. Setup names are stable and distinct (schema/grouping depends on them).
func TestSetupNames(t *testing.T) {
	if (SetupA{Variant: "MA20"}).Name() != "A_MA20_PULLBACK" {
		t.Error("A MA20 name")
	}
	if (SetupA{Variant: "MA60"}).Name() != "A_MA60_PULLBACK" {
		t.Error("A MA60 name")
	}
	if (SetupB{Bucket: 15}).Name() != "B_PULLBACK_15" {
		t.Error("B name")
	}
	if len(SetupBBuckets()) != 5 || len(SetupAVariants()) != 2 {
		t.Error("variant/bucket counts")
	}
}
