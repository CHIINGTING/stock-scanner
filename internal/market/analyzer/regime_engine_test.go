package analyzer

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// view builds a StructureView directly so the decision table can be exercised on exact
// inputs instead of on a price series reverse-engineered to produce them.
func view(s model.PrimaryStructure, b model.BreadthQuality, p model.InstitutionalPosture) model.StructureView {
	return model.StructureView{
		Benchmark: model.Benchmark0050, Structure: s, Breadth: b, Posture: p,
		Metrics: map[string]float64{model.MetricDDFromHigh60: 1, model.MetricBreadthMA20: 60},
	}
}

var allPostures = []model.InstitutionalPosture{
	model.PostureSupportive, model.PostureNeutral, model.PostureDeteriorating, model.PostureUnknown,
}

var allBreadths = []model.BreadthQuality{
	model.BreadthHealthy, model.BreadthCooling, model.BreadthNarrowing, model.BreadthCollapsing,
}

// ── INVARIANT 1: a broken structure is BEAR, and nothing can talk it out of that ──────
//
// The whole point of putting R1 ahead of the DISTRIBUTION rules. Institutional evidence may
// refine a bullish or ranging tape into DISTRIBUTION; it must never soften a downtrend.
func TestInvariantDowntrendIsAlwaysBear(t *testing.T) {
	for _, b := range allBreadths {
		for _, p := range allPostures {
			for _, resilient := range []bool{true, false} {
				for _, intact := range []bool{true, false} {
					for _, orderly := range []bool{true, false} {
						v := view(model.StructureDowntrend, b, p)
						v.PriceResilient, v.TrendIntact, v.OrderlyCorrection = resilient, intact, orderly
						v.FailedHighs = 5
						d := DecideRegime(v, th)
						if d.Regime != model.RegimeBear {
							t.Fatalf("DOWNTREND/%s/%s (res=%v intact=%v orderly=%v) → %s, want BEAR (rule %s)",
								b, p, resilient, intact, orderly, d.Regime, d.RuleID)
						}
						if d.RuleID != model.RuleBearDowntrend {
							t.Fatalf("DOWNTREND must fire R1, got %s", d.RuleID)
						}
					}
				}
			}
		}
	}
}

// ── INVARIANT 2: the regime engine cannot see the Market Score ────────────────────────
//
// Enforced by the signature — DecideRegime takes a StructureView and thresholds, and there
// is no score anywhere in either. This test documents the property and guards the view: no
// score-like metric may be smuggled in through Metrics and change the verdict.
func TestInvariantScoreCannotAffectRegime(t *testing.T) {
	base := view(model.StructureUptrend, model.BreadthHealthy, model.PostureNeutral)
	want := DecideRegime(base, th)

	for score := 0.0; score <= 100; score += 5 {
		v := view(model.StructureUptrend, model.BreadthHealthy, model.PostureNeutral)
		// Every plausible spelling a future contributor might reach for.
		for _, k := range []string{"score", "market_score", "total", "Score"} {
			v.Metrics[k] = score
		}
		got := DecideRegime(v, th)
		if got.Regime != want.Regime || got.RuleID != want.RuleID {
			t.Fatalf("score %.0f changed the verdict: %s/%s → %s/%s",
				score, want.Regime, want.RuleID, got.Regime, got.RuleID)
		}
	}
}

// ── INVARIANT 3: missing structural data yields UNKNOWN, never a verdict ──────────────
func TestInvariantMissingDataIsUnknown(t *testing.T) {
	cases := []struct {
		name string
		v    model.StructureView
	}{
		{"both layers unknown", view(model.StructureUnknown, model.BreadthUnknown, model.PostureUnknown)},
		{"structure unknown", view(model.StructureUnknown, model.BreadthHealthy, model.PostureSupportive)},
		{"breadth unknown", view(model.StructureStrongUptrend, model.BreadthUnknown, model.PostureSupportive)},
		{"zero value", model.StructureView{}},
	}
	for _, c := range cases {
		d := DecideRegime(c.v, th)
		if d.Regime != model.RegimeUnknown {
			t.Errorf("%s → %s, want UNKNOWN", c.name, d.Regime)
		}
		if d.Regime == model.RegimeSideways {
			t.Errorf("%s: missing data was reported as a measured SIDEWAYS", c.name)
		}
		if d.RuleID != model.RuleUnknown {
			t.Errorf("%s: rule = %s, want R0", c.name, d.RuleID)
		}
		if len(d.Reasons) == 0 {
			t.Errorf("%s: UNKNOWN must say which input was unavailable", c.name)
		}
	}
	// An unknown POSTURE is not missing structural data — price and breadth alone describe
	// the market, and M2 ships with no posture producer at all.
	d := DecideRegime(view(model.StructureStrongUptrend, model.BreadthHealthy, model.PostureUnknown), th)
	if d.Regime == model.RegimeUnknown {
		t.Error("an unknown posture must not force the regime to UNKNOWN")
	}
}

// ── INVARIANT 4: BULL_PULLBACK and DISTRIBUTION are structurally distinguishable ───────
//
// The distinction that the whole feature exists to make:
//
//	BULL_PULLBACK — price HAS corrected, internals are cooling but not breaking
//	DISTRIBUTION  — price has NOT corrected, internals are deteriorating underneath it
//
// They are separated on the PRICE axis (orderlyCorrection vs priceResilient) and on the
// BREADTH axis (healthy/cooling vs narrowing/collapsing), so no input can satisfy both.
func TestInvariantPullbackAndDistributionAreDisjoint(t *testing.T) {
	for _, s := range []model.PrimaryStructure{
		model.StructureStrongUptrend, model.StructureUptrend, model.StructureRange, model.StructureWeakening,
	} {
		for _, b := range allBreadths {
			for _, p := range allPostures {
				for _, resilient := range []bool{true, false} {
					for _, orderly := range []bool{true, false} {
						v := view(s, b, p)
						v.PriceResilient, v.OrderlyCorrection, v.TrendIntact = resilient, orderly, true
						d := DecideRegime(v, th)

						switch d.Regime {
						case model.RegimeBullPullback:
							if d.RuleID == model.RulePullbackOrderly {
								// The named pullback rule requires an actual correction and
								// non-deteriorating internals.
								if !v.OrderlyCorrection {
									t.Fatalf("R8 fired without an orderly correction: %+v", v)
								}
								if v.Breadth.Deteriorating() {
									t.Fatalf("R8 fired with deteriorating breadth %s", v.Breadth)
								}
								if v.Posture == model.PostureDeteriorating {
									t.Fatalf("R8 fired with deteriorating institutions")
								}
							}
							// Both pullback rules require the primary trend to be intact.
							if !v.TrendIntact {
								t.Fatalf("%s fired without trendIntact", d.RuleID)
							}
						case model.RegimeDistribution:
							// Every distribution rule requires internals to be deteriorating
							// in some form — never a clean tape.
							clean := !v.Breadth.Deteriorating() && v.Posture != model.PostureDeteriorating
							if clean {
								t.Fatalf("%s produced DISTRIBUTION from a clean tape: %+v", d.RuleID, v)
							}
						}
					}
				}
			}
		}
	}
}

// The price axis is what makes the two mutually exclusive in practice: priceResilient means
// "has not fallen more than ResilientDDPct", orderlyCorrection means "has fallen at least
// CorrectionMinPct". They can only overlap inside [CorrectionMinPct, ResilientDDPct], and
// there the earlier rule wins deterministically.
func TestPullbackDistributionPriceAxisSeparation(t *testing.T) {
	if th.CorrectionMinPct > th.ResilientDDPct {
		return // no overlap band at all
	}
	// Below the correction floor: resilient only → cannot be a pullback.
	shallow := view(model.StructureUptrend, model.BreadthNarrowing, model.PostureNeutral)
	shallow.PriceResilient, shallow.TrendIntact = true, true
	shallow.OrderlyCorrection = false
	if got := DecideRegime(shallow, th); got.Regime != model.RegimeDistribution {
		t.Errorf("resilient price + narrowing breadth should be DISTRIBUTION, got %s", got.Regime)
	}

	// Past the resilient ceiling: corrected only → cannot be distribution by R3.
	deep := view(model.StructureUptrend, model.BreadthCooling, model.PostureNeutral)
	deep.PriceResilient, deep.OrderlyCorrection, deep.TrendIntact = false, true, true
	if got := DecideRegime(deep, th); got.Regime != model.RegimeBullPullback {
		t.Errorf("corrected price + cooling breadth should be BULL_PULLBACK, got %s", got.Regime)
	}
}

// ── INVARIANT 5: the table is ordered and deterministic ───────────────────────────────
func TestInvariantOrderingIsDeterministic(t *testing.T) {
	// Same input, many times, same answer — no map iteration or clock leaking in.
	v := view(model.StructureUptrend, model.BreadthNarrowing, model.PostureDeteriorating)
	v.PriceResilient, v.TrendIntact, v.FailedHighs = true, true, 3
	first := DecideRegime(v, th)
	for i := 0; i < 50; i++ {
		if got := DecideRegime(v, th); got.Regime != first.Regime || got.RuleID != first.RuleID {
			t.Fatalf("non-deterministic: %s/%s vs %s/%s", first.Regime, first.RuleID, got.Regime, got.RuleID)
		}
	}
	// R3 must win over R5 for this input, proving earlier rules take precedence rather than
	// "the most specific" one.
	if first.RuleID != model.RuleDistBreadth {
		t.Errorf("expected the earlier rule R3 to win, got %s", first.RuleID)
	}

	// Exactly one regime per input, always — R11 is an unconditional fallback.
	for _, s := range []model.PrimaryStructure{
		model.StructureStrongUptrend, model.StructureUptrend, model.StructureRange,
		model.StructureWeakening, model.StructureDowntrend,
	} {
		for _, b := range allBreadths {
			for _, p := range allPostures {
				d := DecideRegime(view(s, b, p), th)
				if !d.Regime.Valid() || d.Regime == model.RegimeUnknown {
					t.Fatalf("%s/%s/%s produced %q", s, b, p, d.Regime)
				}
				if d.RuleID == "" {
					t.Fatalf("%s/%s/%s produced no rule id", s, b, p)
				}
			}
		}
	}
}

// Every named rule must be reachable. A rule that no input can trigger is dead code
// pretending to be policy.
func TestEveryRuleIsReachable(t *testing.T) {
	fired := map[string]bool{}
	record := func(v model.StructureView) { fired[DecideRegime(v, th).RuleID] = true }

	record(view(model.StructureUnknown, model.BreadthUnknown, model.PostureUnknown))         // R0
	record(view(model.StructureDowntrend, model.BreadthHealthy, model.PostureSupportive))    // R1
	record(view(model.StructureWeakening, model.BreadthCollapsing, model.PostureSupportive)) // R2

	r3 := view(model.StructureUptrend, model.BreadthNarrowing, model.PostureNeutral)
	r3.PriceResilient = true
	record(r3) // R3

	r4 := view(model.StructureUptrend, model.BreadthCooling, model.PostureDeteriorating)
	r4.PriceResilient = true
	record(r4) // R4

	r5 := view(model.StructureRange, model.BreadthHealthy, model.PostureDeteriorating)
	r5.FailedHighs = 3
	record(r5) // R5

	record(view(model.StructureStrongUptrend, model.BreadthHealthy, model.PostureSupportive)) // R6
	record(view(model.StructureUptrend, model.BreadthHealthy, model.PostureSupportive))       // R7

	r8 := view(model.StructureUptrend, model.BreadthCooling, model.PostureNeutral)
	r8.TrendIntact, r8.OrderlyCorrection = true, true
	record(r8) // R8

	record(view(model.StructureRange, model.BreadthHealthy, model.PostureNeutral)) // R9

	r10 := view(model.StructureWeakening, model.BreadthNarrowing, model.PostureDeteriorating)
	r10.TrendIntact = true
	record(r10) // R10

	record(view(model.StructureWeakening, model.BreadthCooling, model.PostureNeutral)) // R11

	for _, id := range []string{
		model.RuleUnknown, model.RuleBearDowntrend, model.RuleBearCollapse, model.RuleDistBreadth,
		model.RuleDistCoolingInst, model.RuleDistFailedHighs, model.RuleBullStrong,
		model.RuleBullUptrend, model.RulePullbackOrderly, model.RuleSidewaysRange,
		model.RulePullbackWeakening, model.RuleSidewaysFallback,
	} {
		if !fired[id] {
			t.Errorf("rule %s is unreachable", id)
		}
	}
}

// Institutional evidence may only ever make the verdict MORE cautious on a non-bearish
// structure — it must not turn a deteriorating tape bullish.
func TestPostureNeverUpgradesToBull(t *testing.T) {
	for _, s := range []model.PrimaryStructure{
		model.StructureStrongUptrend, model.StructureUptrend, model.StructureRange, model.StructureWeakening,
	} {
		for _, b := range allBreadths {
			base := DecideRegime(view(s, b, model.PostureNeutral), th)
			worse := DecideRegime(view(s, b, model.PostureDeteriorating), th)
			if base.Regime != model.RegimeBull && worse.Regime == model.RegimeBull {
				t.Errorf("%s/%s: deteriorating institutions upgraded %s → BULL", s, b, base.Regime)
			}
		}
	}
}
