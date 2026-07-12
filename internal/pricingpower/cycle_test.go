package pricingpower

import "testing"

func TestClassifyCycle(t *testing.T) {
	cases := []struct {
		name string
		in   CycleInput
		want Cycle
	}{
		{"empty → NONE (degradation)", CycleInput{}, CycleNone},
		{"shortage only → EARLY_SIGNAL", CycleInput{HasShortageEvidence: true}, CycleEarlySignal},
		{"hike single source → PRICE_HIKE", CycleInput{HasPriceHikeEvidence: true, PriceHikeSources: 1}, CyclePriceHike},
		{"hike + 2 sources → CONFIRMED", CycleInput{HasPriceHikeEvidence: true, PriceHikeSources: 2}, CycleConfirmed},
		{"hike + 2 followers → CONFIRMED", CycleInput{HasPriceHikeEvidence: true, SectorFollowers: 2}, CycleConfirmed},
		{"earnings expansion", CycleInput{EarningsAvailable: true, ProfitLeverage: LeverageStrong, PricingState: NotPricedIn}, CycleEarningsExpansion},
		{"earnings + priced-in → LATE_CYCLE", CycleInput{EarningsAvailable: true, ProfitLeverage: LeverageStrong, PricingState: PricedIn}, CycleLateCycle},
		{"reversal evidence → REVERSAL_RISK", CycleInput{HasReversalEvidence: true}, CycleReversalRisk},
		{"reversal overrides earnings", CycleInput{EarningsAvailable: true, ProfitLeverage: LeverageStrong, PricingState: PricedIn, HasReversalEvidence: true}, CycleReversalRisk},
	}
	for _, c := range cases {
		if got := ClassifyCycle(c.in); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

// Precedence + the momentum-down refinement.
func TestCyclePrecedence(t *testing.T) {
	// momentum-down ALONE (no pricing/earnings context) must NOT be a pricing reversal.
	if got := ClassifyCycle(CycleInput{MomentumShiftDown: true}); got != CycleNone {
		t.Errorf("momentum-down alone = %s want NONE (not a cycle to reverse)", got)
	}
	// momentum-down WITH a prior hike → REVERSAL_RISK.
	if got := ClassifyCycle(CycleInput{HasPriceHikeEvidence: true, PriceHikeSources: 2, MomentumShiftDown: true}); got != CycleReversalRisk {
		t.Errorf("momentum-down + hike context = %s want REVERSAL_RISK", got)
	}
	// LATE_CYCLE takes precedence over EARNINGS_EXPANSION when priced-in.
	if got := ClassifyCycle(CycleInput{EarningsAvailable: true, ProfitLeverage: LeverageModerate, PricingState: Overextended}); got != CycleLateCycle {
		t.Errorf("earnings + overextended = %s want LATE_CYCLE", got)
	}
	// EARNINGS_EXPANSION takes precedence over CONFIRMED (evidence still present).
	if got := ClassifyCycle(CycleInput{HasPriceHikeEvidence: true, PriceHikeSources: 3, EarningsAvailable: true, ProfitLeverage: LeverageStrong, PricingState: NotPricedIn}); got != CycleEarningsExpansion {
		t.Errorf("earnings + confirmed-evidence = %s want EARNINGS_EXPANSION", got)
	}
}
