package pricingpower

import "testing"

func dim(name string, avail Availability, earned float64) DimensionScore {
	return DimensionScore{Name: name, Availability: avail, Earned: earned, Max: DimensionMax[name]}
}

func TestComputeNormalization(t *testing.T) {
	th := DefaultThresholds()

	// available weight = margin(15)+profit(15)+not_priced_in(10) = 40; earned 32 → 80.
	res := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimMargin, Supported, 12),
		dim(DimProfitLeverage, Supported, 12),
		dim(DimNotPricedIn, Supported, 8),
		dim(DimPriceIncrease, NotAvailable, 0), // excluded from denominator
		dim(DimShortage, NotAvailable, 0),
	}}, th)
	if res.AvailableWeight != 40 || res.TotalWeight != 100 {
		t.Fatalf("avail=%.0f total=%.0f want 40/100", res.AvailableWeight, res.TotalWeight)
	}
	if res.Score != 80 {
		t.Errorf("score=%d want 80 (32/40)", res.Score)
	}

	// available-but-earning-zero stays in denominator: avail = not_priced_in(10)+technical(5)=15,
	// earned 10 → 67 (10/15), NOT 100.
	res2 := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimNotPricedIn, Supported, 10),
		dim(DimTechnical, Supported, 0), // available, earned 0 — must count in denominator
	}}, th)
	if res2.AvailableWeight != 15 || res2.Score != 67 {
		t.Errorf("avail=%.0f score=%d want 15/67 (zero-earning dim still counts)", res2.AvailableWeight, res2.Score)
	}

	// all unavailable → 0 score, 0 denominator, no divide-by-zero.
	res3 := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimMargin, NotAvailable, 0), dim(DimEPS, NotAvailable, 0),
	}}, th)
	if res3.AvailableWeight != 0 || res3.Score != 0 {
		t.Errorf("all-unavailable = %.0f/%d want 0/0", res3.AvailableWeight, res3.Score)
	}

	// rounding: earned 7 / avail 10 = 70.0; earned 1/3 dims → check round-half.
	res4 := Compute(ScoreInputs{Dimensions: []DimensionScore{dim(DimNotPricedIn, Supported, 3.5)}}, th)
	if res4.Score != 35 {
		t.Errorf("rounding: 3.5/10 → %d want 35", res4.Score)
	}
}

func TestConfidenceGuardrails(t *testing.T) {
	th := DefaultThresholds()

	// Score 100 / avail 15 (coverage .15): data cap LOW → LOW confidence, never HIGH.
	low := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimNotPricedIn, Supported, 10), dim(DimTechnical, Supported, 5),
	}}, th)
	if low.Score != 100 {
		t.Fatalf("setup: score=%d want 100", low.Score)
	}
	if low.Confidence != ConfLow {
		t.Errorf("100 score / 15 avail → %s want LOW", low.Confidence)
	}

	// Score ~90 / avail 90 (coverage .9) WITH fundamentals present → MEDIUM (not LOW, not HIGH:
	// no independent official sources, but hard numbers raise the floor to MEDIUM).
	full := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimPriceIncrease, Supported, 18), dim(DimShortage, Supported, 13),
		dim(DimMargin, Supported, 14), dim(DimProfitLeverage, Supported, 14),
		dim(DimEPS, Supported, 9), dim(DimNotPricedIn, Supported, 9),
		dim(DimTechnical, Supported, 4), dim(DimUtilization, NotAvailable, 0),
	}}, th)
	if full.Coverage < 0.85 {
		t.Fatalf("setup coverage=%.2f want ~0.9", full.Coverage)
	}
	if full.Confidence != ConfMedium {
		t.Errorf("90/90 with fundamentals → %s want MEDIUM", full.Confidence)
	}

	// High coverage + official + 2 independent sources → HIGH.
	high := Compute(ScoreInputs{
		Dimensions: []DimensionScore{
			dim(DimMargin, Supported, 14), dim(DimProfitLeverage, Supported, 14),
			dim(DimEPS, Supported, 9), dim(DimPriceIncrease, Supported, 18),
			dim(DimShortage, Supported, 13), dim(DimNotPricedIn, Supported, 9),
		},
		IndependentSources: 2, HasOfficialDisclosure: true,
	}, th)
	if high.Confidence != ConfHigh {
		t.Errorf("high coverage + official + 2 sources → %s want HIGH", high.Confidence)
	}

	// Single podcast (0 independent, no official) even at decent coverage without
	// fundamentals stays LOW.
	podcast := Compute(ScoreInputs{Dimensions: []DimensionScore{
		dim(DimPriceIncrease, Supported, 18), dim(DimShortage, Supported, 13),
		dim(DimNotPricedIn, Supported, 9), // coverage = 45/100 = .45
	}}, th)
	if podcast.Confidence != ConfLow {
		t.Errorf("single-podcast news-only (no fundamentals/independence) → %s want LOW", podcast.Confidence)
	}
}
