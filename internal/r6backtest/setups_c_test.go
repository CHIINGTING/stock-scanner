package r6backtest

import (
	"testing"
)

// stubVCP overrides the VCP provider for deterministic Setup C tests.
func stubVCP(t *testing.T, valid bool, grade string, q float64) {
	old := vcpProvider
	vcpProvider = func(*Stock, int) (bool, string, float64) { return valid, grade, q }
	t.Cleanup(func() { vcpProvider = old })
}

// 1. C_VCP_MA20_RETEST fires on a VCP-valid MA20 retest.
func TestSetupC_MA20Retest(t *testing.T) {
	stubVCP(t, true, "STANDARD_VCP", 82)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CMA20", closes))
	s.Low[i] = s.MA20[i]
	s.Close[i] = s.MA20[i] * 1.005
	tr := detectAt(SetupC{Variant: "MA20"}, s, i, 85)
	if tr == nil {
		t.Fatalf("C MA20 retest should trigger")
	}
	if !tr.VCPValid || tr.VCPGrade != "STANDARD_VCP" || tr.VCPQualityScore != 82 {
		t.Errorf("VCP fields not propagated: %+v", tr)
	}
}

// 2. C_VCP_MA60_RETEST fires on a VCP-valid MA60 retest.
func TestSetupC_MA60Retest(t *testing.T) {
	stubVCP(t, true, "HIGH_QUALITY_VCP", 91)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CMA60", closes))
	s.Low[i] = s.MA60[i]
	s.Close[i] = s.MA60[i] * 1.005
	if tr := detectAt(SetupC{Variant: "MA60"}, s, i, 85); tr == nil {
		t.Fatalf("C MA60 retest should trigger")
	}
}

// 3. C_VCP_BASE_LOW_RETEST needs base-low touch + hold + turn-up.
func TestSetupC_BaseLowRetest(t *testing.T) {
	stubVCP(t, true, "EARLY_VCP", 74)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CBL", closes))
	bl := baseLow(s, i, DefaultParams().BaseLowLookback)
	// touch base low, hold above it, and turn up from prior close.
	s.Low[i] = bl
	s.Close[i-1] = bl * 1.001
	s.Close[i] = bl * 1.02 // > close[i-1] (turn up) and >= bl*0.99 (held)
	if tr := detectAt(SetupC{Variant: "BASE_LOW"}, s, i, 85); tr == nil {
		t.Fatalf("C base-low retest should trigger")
	}
	// breaking below base low (no hold) must NOT trigger.
	s.Close[i] = bl * 0.95
	if tr := detectAt(SetupC{Variant: "BASE_LOW"}, s, i, 85); tr != nil {
		t.Errorf("break below base low must not trigger")
	}
}

// 4. VCP invalid → no trigger for any variant.
func TestSetupC_VCPInvalid(t *testing.T) {
	stubVCP(t, false, "NONE", 0)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CINV", closes))
	s.Low[i] = s.MA20[i]
	s.Close[i] = s.MA20[i] * 1.005
	for _, v := range []string{"MA20", "MA60", "BASE_LOW"} {
		if tr := detectAt(SetupC{Variant: v}, s, i, 85); tr != nil {
			t.Errorf("variant %s must not trigger when VCP invalid", v)
		}
	}
}

// 4b. quality < 70 → no trigger even if Valid (defensive).
func TestSetupC_LowQuality(t *testing.T) {
	stubVCP(t, true, "EARLY_VCP", 65)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CLQ", closes))
	s.Low[i] = s.MA20[i]
	s.Close[i] = s.MA20[i] * 1.005
	if tr := detectAt(SetupC{Variant: "MA20"}, s, i, 85); tr != nil {
		t.Errorf("quality<70 must not trigger")
	}
}

// 5. RS < 70 → no trigger.
func TestSetupC_LowRS(t *testing.T) {
	stubVCP(t, true, "STANDARD_VCP", 85)
	closes := buildUptrend(300, 0.004)
	i := 299
	s := withRSI(mkStock("CRS", closes))
	s.Low[i] = s.MA20[i]
	s.Close[i] = s.MA20[i] * 1.005
	if tr := detectAt(SetupC{Variant: "MA20"}, s, i, 55); tr != nil {
		t.Errorf("RS<70 must not trigger")
	}
}

// 6. base_low proxy is the min Low over BaseLowLookback bars.
func TestBaseLowProxy(t *testing.T) {
	closes := buildUptrend(300, 0.004)
	s := withRSI(mkStock("BL", closes))
	i := 299
	want := minLow(s, i-39, i) // 40-bar lookback
	if got := baseLow(s, i, 40); got != want {
		t.Errorf("baseLow proxy: got %v want %v", got, want)
	}
}

// 7. names are correct.
func TestSetupCNames(t *testing.T) {
	if (SetupC{Variant: "MA20"}).Name() != "C_VCP_MA20_RETEST" {
		t.Error("MA20 name")
	}
	if (SetupC{Variant: "MA60"}).Name() != "C_VCP_MA60_RETEST" {
		t.Error("MA60 name")
	}
	if (SetupC{Variant: "BASE_LOW"}).Name() != "C_VCP_BASE_LOW_RETEST" {
		t.Error("BASE_LOW name")
	}
	if len(SetupCVariants()) != 3 {
		t.Error("variant count")
	}
}

// 8. VCPGroupStats splits by grade and quality bucket.
func TestVCPGroupStats(t *testing.T) {
	mk := func(grade string, q, ret float64) Trade {
		return Trade{SetupName: "C_VCP_MA20_RETEST", VCPGrade: grade, VCPQualityScore: q, Return20d: ret, HoldReturn20d: ret}
	}
	trades := []Trade{
		mk("EARLY_VCP", 74, 1), mk("STANDARD_VCP", 82, 2), mk("STANDARD_VCP", 88, 3), mk("HIGH_QUALITY_VCP", 92, 4),
	}
	g := VCPGroupStats("C_VCP_MA20_RETEST", trades, []int{20}, DefaultParams())
	// overall + 3 grades + 3 quality buckets = 7 rows.
	if len(g.Stats) != 7 {
		t.Fatalf("want 7 stat rows, got %d", len(g.Stats))
	}
	byLabel := map[string]int{}
	for _, s := range g.Stats {
		byLabel[s.Subgroup] = s.SampleCount
	}
	if byLabel["grade=STANDARD_VCP"] != 2 {
		t.Errorf("STANDARD_VCP count: got %d want 2", byLabel["grade=STANDARD_VCP"])
	}
	if byLabel["quality=90+"] != 1 || byLabel["quality=80-89"] != 2 || byLabel["quality=70-79"] != 1 {
		t.Errorf("quality buckets wrong: %v", byLabel)
	}
}

// 9. no look-ahead: stubbed gates fixed, mutating future bars doesn't change trigger.
func TestSetupC_NoLookAhead(t *testing.T) {
	stubVCP(t, true, "STANDARD_VCP", 85)
	closes := buildUptrend(320, 0.004)
	i := 280
	s1 := withRSI(mkStock("CLA", closes))
	s1.Low[i] = s1.MA20[i]
	s1.Close[i] = s1.MA20[i] * 1.005
	c2 := append([]float64(nil), s1.Close...)
	for j := i + 1; j < len(c2); j++ {
		c2[j] = 9999
	}
	s2 := withRSI(mkStock("CLA", c2))
	s2.Low[i] = s1.Low[i]
	s2.Close[i] = s1.Close[i]
	a := detectAt(SetupC{Variant: "MA20"}, s1, i, 85)
	b := detectAt(SetupC{Variant: "MA20"}, s2, i, 85)
	if (a == nil) != (b == nil) {
		t.Errorf("future-only mutation changed C detection")
	}
}

// 10. real VCP provider smoke (un-stubbed path) does not panic.
func TestRealVCPProviderSmoke(t *testing.T) {
	s := withRSI(mkStock("VS", buildUptrend(300, 0.004)))
	_, _, _ = asofVCP(s, 299)
}
