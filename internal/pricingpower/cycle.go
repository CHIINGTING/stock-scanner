package pricingpower

// CycleInput holds the (future) evidence + fundamental + technical signals that drive the
// pricing-cycle FSM. In Phase 1 the caller has no real news/fundamentals, so these stay
// zero/false and the engine correctly degrades to NONE — it never fabricates a state.
type CycleInput struct {
	HasShortageEvidence  bool // 缺貨 / lead time / 急單 / 供不應求
	HasPriceHikeEvidence bool // 漲價 / 調漲 / 報價上調
	PriceHikeSources     int  // count of GENUINELY INDEPENDENT sources/episodes confirming the hike
	SectorFollowers      int  // sector peers also showing a hike (propagation), for confirmation

	EarningsAvailable bool     // fundamentals present (Phase 3)
	ProfitLeverage    Leverage // profit-vs-revenue grade (Phase 3)

	PricingState        PricingState
	HasReversalEvidence bool // 報價下跌 / 砍單 / 庫存增 / 稼動率下降
	MomentumShiftDown   bool // existing MomentumFlow SHIFT_DOWN (technical)
}

// ClassifyCycle returns the pricing-cycle stage using a deterministic, documented
// precedence (highest first):
//
//	REVERSAL_RISK > LATE_CYCLE > EARNINGS_EXPANSION > CONFIRMED > PRICE_HIKE > EARLY_SIGNAL > NONE
//
// Refinement over the naive order: momentum-down ALONE is not a pricing-cycle reversal —
// there must be a cycle to reverse (a prior hike or earnings context). Explicit pricing
// reversal evidence (報價下跌/砍單…) stands on its own. This prevents a random weak stock
// with no pricing story from being mislabeled REVERSAL_RISK.
func ClassifyCycle(in CycleInput) Cycle {
	hadCycle := in.HasPriceHikeEvidence || in.EarningsAvailable
	if in.HasReversalEvidence || (in.MomentumShiftDown && hadCycle) {
		return CycleReversalRisk
	}

	earningsExpanding := in.EarningsAvailable &&
		(in.ProfitLeverage == LeverageStrong || in.ProfitLeverage == LeverageModerate)

	// LATE_CYCLE is EARNINGS_EXPANSION that the price has already run ahead of.
	if earningsExpanding && (in.PricingState == PricedIn || in.PricingState == Overextended) {
		return CycleLateCycle
	}
	if earningsExpanding {
		return CycleEarningsExpansion
	}
	// CONFIRMED needs independent corroboration, not a single mention.
	if in.HasPriceHikeEvidence && (in.PriceHikeSources >= 2 || in.SectorFollowers >= 2) {
		return CycleConfirmed
	}
	if in.HasPriceHikeEvidence {
		return CyclePriceHike
	}
	if in.HasShortageEvidence {
		return CycleEarlySignal
	}
	return CycleNone
}
