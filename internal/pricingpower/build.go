package pricingpower

// BuildInput is the pure orchestration input. In Phase 1 callers supply price-derived
// metrics (PricedIn) and cycle inputs; the evidence/fundamental dimensions arrive later
// as ExtraDimensions (all NOT_AVAILABLE for now). No data is fetched here.
type BuildInput struct {
	Symbol string
	Sector string
	Hints  []string // theme hints (news themes / product keywords)

	PricedIn PricedInInput
	Cycle    CycleInput

	// ExtraDimensions are evidence/fundamental dimensions supplied by later phases. Phase 1
	// leaves them empty (or NOT_AVAILABLE), so the score rests on the two SUPPORTED dims.
	ExtraDimensions []DimensionScore

	IndependentSources    int
	HasOfficialDisclosure bool

	Reasons []string
	Risks   []string
}

// Build assembles a Signal purely from inputs: theme, priced-in state, cycle, and an
// availability-normalized score. The two always-SUPPORTED dimensions (not_priced_in,
// technical) are derived here from price data; all other dimensions default to
// NOT_AVAILABLE until their phases land, so they are excluded from the denominator rather
// than punished. Pure — changes no external state.
func Build(in BuildInput, th Thresholds) Signal {
	state, prReasons := ClassifyPricedIn(in.PricedIn, th)
	in.Cycle.PricingState = state
	cyc := ClassifyCycle(in.Cycle)

	dims := []DimensionScore{
		notPricedInDimension(state),
		technicalDimension(in.PricedIn.Stage),
	}
	// evidence / fundamentals (default NOT_AVAILABLE in Phase 1)
	dims = append(dims, defaultUnavailable(in.ExtraDimensions)...)

	res := Compute(ScoreInputs{
		Dimensions:            dims,
		IndependentSources:    in.IndependentSources,
		HasOfficialDisclosure: in.HasOfficialDisclosure,
	}, th)

	reasons := append(append([]string(nil), in.Reasons...), prReasons...)
	return Signal{
		Symbol:          in.Symbol,
		Sector:          in.Sector,
		Theme:           ThemeFor(in.Sector, in.Hints...),
		Score:           res.Score,
		Earned:          res.Earned,
		AvailableWeight: res.AvailableWeight,
		TotalWeight:     res.TotalWeight,
		Coverage:        res.Coverage,
		Confidence:      res.Confidence,
		Cycle:           cyc,
		PricingState:    state,
		ProfitLeverage:  in.Cycle.ProfitLeverage,
		Dimensions:      res.Dimensions,
		Reasons:         reasons,
		Risks:           in.Risks,
		Computed:        true,
	}
}

// notPricedInDimension: the more room the price still has, the more of the 10 points it
// earns. Always SUPPORTED (price data always exists).
func notPricedInDimension(state PricingState) DimensionScore {
	max := DimensionMax[DimNotPricedIn]
	var earned float64
	var reason string
	switch state {
	case NotPricedIn:
		earned, reason = max, "股價尚未反映（最大機會）"
	case PartiallyPricedIn:
		earned, reason = max*0.6, "部分反映"
	case PricedIn:
		earned, reason = max*0.2, "多已反映"
	default: // Overextended
		earned, reason = 0, "股價已超漲"
	}
	return DimensionScore{Name: DimNotPricedIn, Availability: Supported, Earned: earned, Max: max, Reason: reason}
}

// technicalDimension: small confirmation bonus for constructive stages. Always SUPPORTED.
func technicalDimension(stage string) DimensionScore {
	max := DimensionMax[DimTechnical]
	var earned float64
	reason := "技術面中性"
	switch stage {
	case "PRE_BREAKOUT", "BASE_BUILDING", "BREAKOUT_START":
		earned, reason = max, "整理/準備突破"
	case "MAIN_RUN":
		earned, reason = max*0.6, "主升段"
	case "OVERHEATED", "FAILED":
		earned, reason = 0, "過熱/失敗"
	}
	return DimensionScore{Name: DimTechnical, Availability: Supported, Earned: earned, Max: max, Reason: reason}
}

// defaultUnavailable ensures the six evidence/fundamental dimensions exist as explicit
// NOT_AVAILABLE entries (for report transparency) unless a caller already supplied them.
func defaultUnavailable(extra []DimensionScore) []DimensionScore {
	present := map[string]bool{}
	for _, d := range extra {
		present[d.Name] = true
	}
	out := append([]DimensionScore(nil), extra...)
	for _, name := range []string{DimPriceIncrease, DimShortage, DimUtilization, DimMargin, DimProfitLeverage, DimEPS} {
		if !present[name] {
			out = append(out, DimensionScore{Name: name, Availability: NotAvailable, Max: DimensionMax[name], Reason: "資料未接入（Phase 2/3）"})
		}
	}
	return out
}
