package pricingpower

import "testing"

// End-to-end pure Build with Phase-1-realistic inputs (no evidence/fundamentals):
// score rests on the two SUPPORTED dims, confidence is LOW, cycle degrades to NONE, and
// the six unfetched dimensions are present as explicit NOT_AVAILABLE for transparency.
func TestBuildPhase1(t *testing.T) {
	th := DefaultThresholds()
	sig := Build(BuildInput{
		Symbol: "2327", Sector: "被動元件",
		PricedIn: PricedInInput{Ret60: 8, DistFrom52wHighPct: -22, RSPercentile: 45, Stage: "BASE_BUILDING"},
	}, th)

	if !sig.Computed {
		t.Fatal("Computed should be true")
	}
	if sig.Theme != ThemePassive {
		t.Errorf("theme=%s want PASSIVE_COMPONENT", sig.Theme)
	}
	if sig.PricingState != NotPricedIn {
		t.Errorf("state=%s want NOT_PRICED_IN", sig.PricingState)
	}
	if sig.Cycle != CycleNone {
		t.Errorf("cycle=%s want NONE (no evidence/fundamentals in Phase 1)", sig.Cycle)
	}
	if sig.Confidence != ConfLow {
		t.Errorf("confidence=%s want LOW (coverage tiny)", sig.Confidence)
	}
	// only not_priced_in(10) + technical(5) available → denominator 15.
	if sig.AvailableWeight != 15 || sig.TotalWeight != 100 {
		t.Errorf("avail=%.0f total=%.0f want 15/100", sig.AvailableWeight, sig.TotalWeight)
	}
	// NOT_PRICED_IN → full 10, BASE_BUILDING → full 5 → 15/15 → score 100 (but LOW conf).
	if sig.Score != 100 {
		t.Errorf("score=%d want 100 (both supported dims maxed)", sig.Score)
	}
	// transparency: the six unfetched dims must be present as NOT_AVAILABLE.
	na := 0
	for _, d := range sig.Dimensions {
		if d.Availability == NotAvailable {
			na++
		}
	}
	if na != 6 {
		t.Errorf("expected 6 NOT_AVAILABLE dimensions for explainability, got %d", na)
	}
}
