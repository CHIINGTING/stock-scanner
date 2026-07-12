package pricingpower

import "testing"

func TestClassifyPricedIn(t *testing.T) {
	th := DefaultThresholds()
	cases := []struct {
		name string
		in   PricedInInput
		want PricingState
	}{
		{"quiet base → not priced in", PricedInInput{Ret60: 8, DistFrom52wHighPct: -25, RSPercentile: 40}, NotPricedIn},
		{"ret60 alone (warm) → partially", PricedInInput{Ret60: 20, DistFrom52wHighPct: -20}, PartiallyPricedIn},
		{"ret60 hot alone (+2) → partially, NOT overextended", PricedInInput{Ret60: 55, DistFrom52wHighPct: -20}, PartiallyPricedIn},
		{"combined 3 → priced in", PricedInInput{Ret60: 55, DistFrom52wHighPct: -5, MA20DevPct: 14}, PricedIn},
		{"combined 5 → overextended", PricedInInput{Ret60: 55, DistFrom52wHighPct: -3, MA20DevPct: 14, MA60DevPct: 25, RSPercentile: 85}, Overextended},
		{"extreme ret60 alone → overextended", PricedInInput{Ret60: 130, DistFrom52wHighPct: -30}, Overextended},
		{"extreme MA20 dev alone → overextended", PricedInInput{Ret60: 10, MA20DevPct: 40}, Overextended},
	}
	for _, c := range cases {
		if got, _ := ClassifyPricedIn(c.in, th); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

// Boundary values around the priced-in point thresholds.
func TestClassifyPricedInBoundaries(t *testing.T) {
	th := DefaultThresholds()
	// exactly at Ret60Priced(40) → +2, plus nearHigh(+1) = 3 → PRICED_IN.
	if got, _ := ClassifyPricedIn(PricedInInput{Ret60: 40, DistFrom52wHighPct: -8}, th); got != PricedIn {
		t.Errorf("boundary ret60=40 + near-high = %s want PRICED_IN", got)
	}
	// just below near-high band: -8.1% is outside 8% → only +2 → PARTIALLY.
	if got, _ := ClassifyPricedIn(PricedInInput{Ret60: 40, DistFrom52wHighPct: -8.1}, th); got != PartiallyPricedIn {
		t.Errorf("boundary just-outside near-high = %s want PARTIALLY", got)
	}
	// single-metric false-positive guard: strong RS only (+1) → PARTIALLY, never higher.
	if got, _ := ClassifyPricedIn(PricedInInput{RSPercentile: 95}, th); got != PartiallyPricedIn {
		t.Errorf("RS-only = %s want PARTIALLY (single-metric guard)", got)
	}
}
