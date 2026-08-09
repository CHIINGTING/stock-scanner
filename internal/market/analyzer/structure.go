package analyzer

import "github.com/deep-huang/stock-scanner/internal/market/model"

// The layer-1 and layer-2 classifiers of the structural regime engine
// (docs/SPEC_MARKET_DASHBOARD.md §3.2 / §3.3).
//
// Both are ordered decision tables: conditions are evaluated top-down and the FIRST match
// wins. Order is part of the specification, not an implementation detail — reordering these
// switch arms changes the classification, so each arm is numbered to match the spec table.
//
// Neither function can see the Market Score. That is enforced structurally: the signatures
// take a PriceView / BreadthView and thresholds, and there is no score in either. It is not
// possible to write "if score >= 70 then bull" here even by accident.

// ClassifyStructure is layer 1 (§3.2). It reads ONLY the benchmark's price.
//
// The ordering matters most between DOWNTREND and WEAKENING: both are below MA20 and MA60
// with a negative 20-day return, and the ONLY thing separating them is whether the
// long-term floor (MA120) still holds. Testing DOWNTREND first means a broken long-term
// floor always wins — an orderly pullback can never be mistaken for a bear market, and a
// bear market can never be softened into a pullback.
func ClassifyStructure(v PriceView, th model.StructureThresholds) model.PrimaryStructure {
	th = th.Defaulted()

	// ① insufficient history — never guess
	if !v.Sufficient || v.Close <= 0 || v.MA20 == 0 || v.MA60 == 0 || v.MA120 == 0 {
		return model.StructureUnknown
	}

	below20, below60 := v.Close < v.MA20, v.Close < v.MA60
	below120 := v.Close < v.MA120

	// ② DOWNTREND — long-term floor broken, medium-term falling
	if below60 && below120 && !v.MA60Rising && v.Ret20 < 0 {
		return model.StructureDowntrend
	}

	// ③ WEAKENING — medium-term structure broken, long-term floor intact
	if below20 && below60 && v.Ret20 < 0 && !below120 {
		return model.StructureWeakening
	}

	// ④ STRONG_UPTREND — full bullish stack, rising MA60, near the 60-day high
	if v.Close > v.MA20 && v.MA20 > v.MA60 && v.MA60 > v.MA120 &&
		v.MA60Rising && v.Ret60 > 0 && v.DDHigh <= th.NearHighDDPct {
		return model.StructureStrongUptrend
	}

	// ⑤ UPTREND — above a rising MA60 with a positive 60-day return
	if !below60 && v.MA60Rising && v.Ret60 > 0 {
		return model.StructureUptrend
	}

	// ⑥ RANGE — everything else
	return model.StructureRange
}

// ClassifyBreadth is layer 2a (§3.3).
//
// nearHigh comes from the PRICE side (DDHigh <= NearHighDDPct) and is what makes NARROWING
// a divergence test rather than a lone threshold: "fewer than 45% of stocks are above their
// own MA20 WHILE the index sits at its high" is a statement about price and internals
// disagreeing. Without nearHigh it would just be "breadth is lowish", which is exactly the
// reading that fails to distinguish a healthy pullback from distribution.
func ClassifyBreadth(v BreadthView, nearHigh bool, th model.StructureThresholds) model.BreadthQuality {
	th = th.Defaulted()

	// ① too few measurable stocks — never guess. The population is re-checked here rather
	// than trusting the flag alone, mirroring ClassifyStructure's own MA self-check: a view
	// hand-built by a caller must not be able to assert sufficiency it does not have.
	if !v.Sufficient || v.ValidCount < th.MinBreadthUniverse {
		return model.BreadthUnknown
	}
	// ② COLLAPSING
	if v.AboveMA20 < th.BreadthCollapsePct {
		return model.BreadthCollapsing
	}
	// ③ NARROWING — leadership narrowing WHILE price holds up. Both branches require
	// nearHigh, because narrowing is a DIVERGENCE between price and internals; without the
	// price half it is just "breadth is lower than it was", which is what a broad correction
	// looks like and belongs in COOLING.
	//
	// Corrected in M3 on historical evidence: the drop-only branch used to fire without
	// nearHigh and produced 51 of 130 NARROWING days, of which 45 were days when the
	// benchmark itself had already fallen >=3% from its high — price and breadth receding
	// together, the opposite of a divergence. See SPEC §16.1 Q1.
	if nearHigh && (v.AboveMA20 < th.BreadthNarrowPct || v.Drop20 >= th.BreadthDropNarrowPP) {
		return model.BreadthNarrowing
	}
	// ④ HEALTHY
	if v.AboveMA20 >= th.BreadthHealthyPct && v.Drop20 < th.BreadthDropCoolPP {
		return model.BreadthHealthy
	}
	// ⑤ COOLING — everything else
	return model.BreadthCooling
}

// BuildStructureView assembles the persisted layer-1 + layer-2 record, including the four
// predicates the regime decision table branches on (M3).
//
// The predicates are computed HERE and stored, rather than recomputed inside the decision
// table, for one reason: a snapshot from six months ago must be re-judgeable without
// re-deriving anything. If the table recomputed them, changing a threshold would silently
// change the meaning of old records.
//
// posture is supplied by the caller. In M2 nothing produces it yet, so callers pass
// PostureUnknown — and per §5.3 that does NOT make the regime UNKNOWN: Known() checks the
// price and breadth layers only, because those two alone describe the market.
func BuildStructureView(
	pv PriceView, bv BreadthView, posture model.InstitutionalPosture, th model.StructureThresholds,
) model.StructureView {
	th = th.Defaulted()

	structure := ClassifyStructure(pv, th)
	nearHigh := pv.Sufficient && pv.DDHigh <= th.NearHighDDPct
	breadth := ClassifyBreadth(bv, nearHigh, th)

	if posture == "" {
		posture = model.PostureUnknown
	}

	view := model.StructureView{
		Benchmark: pv.Benchmark,
		Structure: structure,
		Breadth:   breadth,
		Posture:   posture,
	}

	if pv.Sufficient {
		view.TrendIntact = pv.Close > pv.MA120 && pv.MA120 > 0 && pv.MA60Rising
		// priceResilient means "price has NOT meaningfully corrected" — it is the price half
		// of the distribution signature (resilient tape, deteriorating internals) and the
		// complement of orderlyCorrection.
		//
		// Corrected in M3: this used to be `dd <= 5% OR close > MA60`. The second clause is a
		// TREND statement, not a "hasn't corrected" statement — a benchmark 12% off its high
		// but still above its 60-day average has plainly corrected. Historically it made
		// priceResilient true on 83% of days (53 of them purely via the MA60 clause while
		// already >5% down) and overlap with orderlyCorrection on 61 days, all of which R3
		// claimed before R8 could see them. See SPEC §16.1 Q2.
		view.PriceResilient = pv.DDHigh <= th.ResilientDDPct
		view.OrderlyCorrection = pv.DDHigh >= th.CorrectionMinPct &&
			pv.DDHigh <= th.CorrectionMaxPct &&
			pv.WorstDay20 > -th.CapitulationDayPct
		view.FailedHighs = pv.FailedHighs
	}

	view.Metrics = mergeMetrics(priceMetrics(pv), breadthMetrics(bv))
	return view
}

func priceMetrics(v PriceView) map[string]float64 {
	if !v.Sufficient {
		return nil
	}
	return map[string]float64{
		model.MetricClose:         round2(v.Close),
		model.MetricMA20:          round2(v.MA20),
		model.MetricMA60:          round2(v.MA60),
		model.MetricMA120:         round2(v.MA120),
		model.MetricMA200:         round2(v.MA200),
		model.MetricRet20:         round2(v.Ret20),
		model.MetricRet60:         round2(v.Ret60),
		model.MetricDDFromHigh60:  round2(v.DDHigh),
		model.MetricRealizedVol20: round2(v.RealizedVol20),
		model.MetricVolPercentile: round2(v.VolPercentile),
	}
}

func breadthMetrics(v BreadthView) map[string]float64 {
	if !v.Sufficient {
		return nil
	}
	m := map[string]float64{
		model.MetricBreadthMA20:   round2(v.AboveMA20),
		model.MetricBreadthDrop20: round2(v.Drop20),
		model.MetricBreadthValid:  float64(v.ValidCount),
		model.MetricAdvRatio:      round2(v.AdvancingRatio),
	}
	// A measure whose population fell below the floor is OMITTED, not written as a number
	// nobody can later tell was derived from a dozen stocks. Its denominator is recorded
	// either way so the omission is explainable.
	m[model.MetricBreadthBase60] = float64(v.Base60)
	m[model.MetricBreadthBase120] = float64(v.Base120)
	if v.Above60Known {
		m[model.MetricBreadthMA60] = round2(v.AboveMA60)
	}
	if v.Above120Known {
		m[model.MetricBreadthMA120] = round2(v.AboveMA120)
	}
	return m
}

func mergeMetrics(ms ...map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Flags computes the orthogonal event/volatility flags (§9 of the spec). They are separate
// from the regime on purpose: "crash" and "recovery" are events, not structures, and giving
// them regime slots would make them compete with DISTRIBUTION for the same day.
//
// A missing volatility percentile leaves HighVolatility false rather than guessing — the
// flag degrades on its own without touching the regime.
//
// RecoveryEvent is deliberately NOT computed here. It is defined as "reclaimed MA20 shortly
// after a crash", which needs to know whether earlier sessions were crash days — i.e. it
// reads stored history, not today's view. It lands with the daily pipeline (M8), which is
// the first thing that has snapshots to look back at.
func Flags(pv PriceView, bv BreadthView, th model.StructureThresholds) model.Flags {
	th = th.Defaulted()
	var f model.Flags
	if !pv.Sufficient {
		return f
	}
	if pv.VolPctKnown && pv.VolPercentile >= th.VolPercentile {
		f.HighVolatility = true
	}
	if bv.Sufficient && pv.Ret20 <= th.CrashRet20Pct && bv.AboveMA20 < th.BreadthCollapsePct {
		f.CrashEvent = true
	}
	return f
}
