package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// The layer-3 regime decision table (docs/SPEC_MARKET_DASHBOARD.md §5.3).
//
// ORDERED, first match wins. The order is part of the specification: R1 sits ahead of every
// DISTRIBUTION rule precisely so that institutional evidence can never soften a structurally
// bearish tape, and R3–R5 sit ahead of R8 so that a divergence is never re-read as a healthy
// pullback. Reordering these arms changes the verdict, so each carries its spec rule id and
// that id is persisted with the decision.
//
// The signature CANNOT see the Market Score. That is the architectural invariant of the
// whole feature — score describes directional strength, regime describes structure, and the
// two are allowed to disagree (score 67 while the structure is BULL_PULLBACK). There is no
// score parameter here, so the forbidden coupling cannot be written by accident.

// DecideRegime maps a structural view onto one of the five public regimes.
//
// It is a pure function of the view: everything it branches on was computed and persisted by
// BuildStructureView, so a snapshot from six months ago can be re-judged offline without
// re-deriving anything.
func DecideRegime(v model.StructureView, th model.StructureThresholds) model.RegimeDecision {
	th = th.Defaulted()

	d := model.RegimeDecision{View: v}
	dist := func(rule string, why ...string) model.RegimeDecision {
		d.Regime, d.RuleID = model.RegimeDistribution, rule
		d.Reasons = append(reasonsFor(v), why...)
		return d
	}

	// R0 — either structural layer is unmeasurable. UNKNOWN is a state, not a failure, and
	// it must never fall through to SIDEWAYS ("we looked, no edge") which is a verdict.
	if !v.Known() {
		d.Regime, d.RuleID = model.RegimeUnknown, model.RuleUnknown
		d.Reasons = []string{unknownReason(v)}
		d.Caveats = []string{"資料不足，本日不做 regime 判斷"}
		return d
	}

	// R1 — a broken structure is BEAR, full stop. Institutions are `any` here BY DESIGN:
	// deteriorating flows may refine a bullish or ranging tape into DISTRIBUTION, but they
	// must never talk a downtrend UP into one.
	if v.Structure == model.StructureDowntrend {
		d.Regime, d.RuleID = model.RegimeBear, model.RuleBearDowntrend
		d.Reasons = reasonsFor(v)
		return d
	}

	// R2 — the long-term floor is holding but participation has collapsed; the structure is
	// bear in all but name.
	if v.Structure == model.StructureWeakening && v.Breadth == model.BreadthCollapsing {
		d.Regime, d.RuleID = model.RegimeBear, model.RuleBearCollapse
		d.Reasons = append(reasonsFor(v), "長線支撐雖未破，但廣度已潰散")
		return d
	}

	// R3 — the core distribution signature: price is holding up while participation is
	// narrowing or collapsing underneath it.
	if inStructures(v.Structure, model.StructureStrongUptrend, model.StructureUptrend,
		model.StructureRange, model.StructureWeakening) &&
		v.Breadth.Deteriorating() && v.PriceResilient {
		return dist(model.RuleDistBreadth, "價格仍撐住，但廣度已惡化——內外背離")
	}

	// R4 — breadth is only cooling, but the institutions are leaving at the same time.
	if inStructures(v.Structure, model.StructureStrongUptrend, model.StructureUptrend, model.StructureRange) &&
		v.Breadth == model.BreadthCooling &&
		v.Posture == model.PostureDeteriorating && v.PriceResilient {
		return dist(model.RuleDistCoolingInst, "廣度降溫同時法人轉出")
	}

	// R5 — repeated failure at the highs with institutions deteriorating.
	if inStructures(v.Structure, model.StructureUptrend, model.StructureRange) &&
		v.Posture == model.PostureDeteriorating && v.FailedHighs >= th.FailedHighsMin {
		return dist(model.RuleDistFailedHighs,
			fmt.Sprintf("近期 %d 次觸高後失守 MA20，且法人轉出", v.FailedHighs))
	}

	// R6 — the healthy top structure.
	if v.Structure == model.StructureStrongUptrend &&
		(v.Breadth == model.BreadthHealthy || v.Breadth == model.BreadthCooling) &&
		v.Posture != model.PostureDeteriorating {
		d.Regime, d.RuleID = model.RegimeBull, model.RuleBullStrong
		d.Reasons = reasonsFor(v)
		return d
	}

	// R7 — a plain uptrend still at its high with broad participation.
	//
	// The posture gate is `!= DETERIORATING`, matching R6. It originally read
	// `== SUPPORTIVE || == NEUTRAL`, which silently excluded UNKNOWN — so an exchange outage
	// could block this BULL rule while leaving R6 untouched, even though both describe a
	// healthy tape. Two bull rules disagreeing about whether missing institutional data is
	// disqualifying is an inconsistency, not a policy. Corrected in M8 validation; it moved
	// exactly one day in the historical replay, but it is the difference between a rule that
	// degrades gracefully and one that vanishes whenever a source is down.
	if v.Structure == model.StructureUptrend && v.Breadth == model.BreadthHealthy &&
		v.Posture != model.PostureDeteriorating &&
		metric(v, model.MetricDDFromHigh60) < th.NearHighDDPct {
		d.Regime, d.RuleID = model.RegimeBull, model.RuleBullUptrend
		d.Reasons = reasonsFor(v)
		return d
	}

	// R8 — the orderly correction: price HAS actually pulled back, the primary trend is
	// intact, participation is cooling rather than breaking, institutions are not leaving.
	if inStructures(v.Structure, model.StructureUptrend, model.StructureWeakening) &&
		(v.Breadth == model.BreadthHealthy || v.Breadth == model.BreadthCooling) &&
		v.Posture != model.PostureDeteriorating &&
		v.TrendIntact && v.OrderlyCorrection {
		d.Regime, d.RuleID = model.RegimeBullPullback, model.RulePullbackOrderly
		d.Reasons = append(reasonsFor(v), "回檔幅度可控、主趨勢未破")
		return d
	}

	// R9 — a range with nothing deteriorating.
	if v.Structure == model.StructureRange &&
		(v.Breadth == model.BreadthHealthy || v.Breadth == model.BreadthCooling) &&
		v.Posture != model.PostureDeteriorating {
		d.Regime, d.RuleID = model.RegimeSideways, model.RuleSidewaysRange
		d.Reasons = reasonsFor(v)
		return d
	}

	// R10 — the residual for a weakening tape whose long-term trend still holds. Breadth and
	// posture are `any`: this rule is gated by trendIntact alone, so it can produce a
	// BULL_PULLBACK whose breadth is NARROWING. That combination is only unreachable because
	// R3 sits ahead of it — see §7.2/§7.3 for why that matters when writing invariants.
	if v.Structure == model.StructureWeakening && v.TrendIntact {
		d.Regime, d.RuleID = model.RegimeBullPullback, model.RulePullbackWeakening
		d.Reasons = append(reasonsFor(v), "中期轉弱但長線支撐未破")
		d.Caveats = append(d.Caveats, "此判定由殘差規則產生，非典型的可控回檔")
		return d
	}

	// R11 — nothing above matched.
	d.Regime, d.RuleID = model.RegimeSideways, model.RuleSidewaysFallback
	d.Reasons = reasonsFor(v)
	d.Caveats = append(d.Caveats, "未命中任何具名規則，歸為無方向")
	return d
}

func inStructures(s model.PrimaryStructure, list ...model.PrimaryStructure) bool {
	for _, x := range list {
		if s == x {
			return true
		}
	}
	return false
}

func metric(v model.StructureView, key string) float64 {
	x, _ := v.Metric(key)
	return x
}

// reasonsFor states the three layers in the order the table reads them, so a stored decision
// explains itself without the reader needing the code.
func reasonsFor(v model.StructureView) []string {
	return []string{
		fmt.Sprintf("價格結構 %s／廣度 %s／法人 %s", v.Structure, v.Breadth, v.Posture),
		fmt.Sprintf("距 60 日高 -%.1f%%，站上 MA20 家數 %.1f%%（近 20 日高點 -%.1fpp）",
			metric(v, model.MetricDDFromHigh60), metric(v, model.MetricBreadthMA20),
			metric(v, model.MetricBreadthDrop20)),
	}
}

func unknownReason(v model.StructureView) string {
	switch {
	case v.Structure == model.StructureUnknown && v.Breadth == model.BreadthUnknown:
		return "價格結構與廣度皆無法判定"
	case v.Structure == model.StructureUnknown:
		return "benchmark 歷史不足，價格結構無法判定"
	default:
		return "可計算家數不足，廣度無法判定"
	}
}
