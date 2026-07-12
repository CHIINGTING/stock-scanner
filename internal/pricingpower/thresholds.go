package pricingpower

// Thresholds centralizes every tunable number used by the engine so none are scattered
// as magic constants. Pass DefaultThresholds() unless a caller overrides. (YAML wiring
// is a later phase; keeping them here already satisfies "centralized + configurable".)
type Thresholds struct {
	// ── Already-priced-in (pricedin.go) ──────────────────────────────────────
	Ret60Partial float64 // ret60 ≥ this → +1 "warm"
	Ret60Priced  float64 // ret60 ≥ this → +2 "hot"
	Ret60Extreme float64 // ret60 ≥ this → OVEREXTENDED on its own (extreme single metric)
	Near52wHigh  float64 // within this % of the 52w high → +1
	MA20DevHot   float64 // Close this % above MA20 → +1
	MA20DevExtr  float64 // Close this % above MA20 → OVEREXTENDED on its own (extreme)
	MA60DevHot   float64 // Close this % above MA60 → +1
	RSHigh       float64 // RS percentile ≥ this → +1
	PtsPricedIn  int     // total points ≥ this → PRICED_IN
	PtsOverext   int     // total points ≥ this → OVEREXTENDED (combined, non-extreme)

	// ── Confidence (score.go) ────────────────────────────────────────────────
	CoverageMedium float64 // coverage ≥ this → data allows up to MEDIUM
	CoverageHigh   float64 // coverage ≥ this → data allows up to HIGH
	FundCoverage   float64 // fundamentals present AND coverage ≥ this → ceiling floor MEDIUM
}

// DefaultThresholds are the Phase-1 baseline (not calibrated; tune later).
func DefaultThresholds() Thresholds {
	return Thresholds{
		Ret60Partial: 15,
		Ret60Priced:  40,
		Ret60Extreme: 120,
		Near52wHigh:  8,
		MA20DevHot:   12,
		MA20DevExtr:  35,
		MA60DevHot:   20,
		RSHigh:       80,
		PtsPricedIn:  3,
		PtsOverext:   5,

		CoverageMedium: 0.40,
		CoverageHigh:   0.70,
		FundCoverage:   0.60,
	}
}
