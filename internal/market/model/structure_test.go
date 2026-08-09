package model

import (
	"encoding/json"
	"testing"
)

// UNKNOWN must be reachable and distinct at every layer. This is the type-level half of the
// "missing data never becomes SIDEWAYS" guarantee; the decision table enforces the other
// half in M3.
func TestUnknownExistsAtEveryLayer(t *testing.T) {
	if !StructureUnknown.Valid() || !BreadthUnknown.Valid() || !PostureUnknown.Valid() || !RegimeUnknown.Valid() {
		t.Fatal("UNKNOWN must be a valid state at every layer")
	}
	// UNKNOWN is not a synonym for the calm/neutral states.
	if StructureUnknown == StructureRange {
		t.Error("UNKNOWN structure must not equal RANGE")
	}
	if BreadthUnknown == BreadthHealthy || BreadthUnknown == BreadthCooling {
		t.Error("UNKNOWN breadth must not equal a measured state")
	}
	if PostureUnknown == PostureNeutral {
		t.Error("UNKNOWN posture must not equal NEUTRAL")
	}
	if RegimeUnknown == RegimeSideways {
		t.Error("UNKNOWN regime must not equal SIDEWAYS")
	}
}

func TestEnumValidityRejectsUndefined(t *testing.T) {
	for _, p := range []PrimaryStructure{
		StructureStrongUptrend, StructureUptrend, StructureRange,
		StructureWeakening, StructureDowntrend, StructureUnknown,
	} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	if PrimaryStructure("BULL").Valid() {
		t.Error("a regime value must not pass as a structure")
	}
	if BreadthQuality("NARROW").Valid() || InstitutionalPosture("BEARISH").Valid() {
		t.Error("undefined layer-2 values accepted")
	}
	if Regime("Bull Market").Valid() {
		t.Error("the pre-rename regime string must not validate")
	}
}

func TestStructureSemanticHelpers(t *testing.T) {
	if !StructureStrongUptrend.Bullish() || !StructureUptrend.Bullish() {
		t.Error("uptrend family should report Bullish")
	}
	for _, p := range []PrimaryStructure{StructureRange, StructureWeakening, StructureDowntrend, StructureUnknown} {
		if p.Bullish() {
			t.Errorf("%q must not report Bullish", p)
		}
	}
	if !BreadthNarrowing.Deteriorating() || !BreadthCollapsing.Deteriorating() {
		t.Error("NARROWING/COLLAPSING are the deteriorating states")
	}
	for _, b := range []BreadthQuality{BreadthHealthy, BreadthCooling, BreadthUnknown} {
		if b.Deteriorating() {
			t.Errorf("%q must not report Deteriorating", b)
		}
	}
}

// Known() is what the M3 table will consult before it is allowed to name a regime.
func TestStructureViewKnown(t *testing.T) {
	full := StructureView{Structure: StructureUptrend, Breadth: BreadthHealthy, Posture: PostureNeutral}
	if !full.Known() {
		t.Error("a fully classified view should be Known")
	}
	// Posture may be UNKNOWN (two of three institutional sources can be down) without
	// invalidating the structural read — price and breadth alone still describe the market.
	noPosture := StructureView{Structure: StructureUptrend, Breadth: BreadthHealthy, Posture: PostureUnknown}
	if !noPosture.Known() {
		t.Error("unknown posture must not invalidate a known price/breadth structure")
	}
	for _, v := range []StructureView{
		{Structure: StructureUnknown, Breadth: BreadthHealthy},
		{Structure: StructureUptrend, Breadth: BreadthUnknown},
		{},
	} {
		if v.Known() {
			t.Errorf("view %+v must not be Known", v)
		}
	}
}

func TestBenchmarkIsExplicit(t *testing.T) {
	if !Benchmark0050.Valid() || !BenchmarkTAIEX.Valid() {
		t.Error("both benchmarks must validate")
	}
	if Benchmark("^TWII").Valid() {
		t.Error("undefined benchmark accepted")
	}
	// The MVP proxy must never serialize as the index it stands in for.
	if string(Benchmark0050) == string(BenchmarkTAIEX) {
		t.Fatal("0050 must remain distinguishable from TAIEX")
	}
}

func TestStructureViewMetrics(t *testing.T) {
	v := StructureView{Metrics: map[string]float64{MetricDDFromHigh60: 0}}
	if x, ok := v.Metric(MetricDDFromHigh60); !ok || x != 0 {
		t.Error("a recorded zero drawdown must survive")
	}
	if _, ok := v.Metric(MetricMA200); ok {
		t.Error("absent metric reported present")
	}
	var empty StructureView
	if _, ok := empty.Metric(MetricClose); ok {
		t.Error("nil metrics map reported a value")
	}
}

func TestRegimeDecisionRoundTripKeepsRuleID(t *testing.T) {
	in := RegimeDecision{
		Regime: RegimeDistribution,
		RuleID: RuleDistBreadth,
		View: StructureView{
			Benchmark: Benchmark0050, Structure: StructureUptrend,
			Breadth: BreadthNarrowing, Posture: PostureDeteriorating,
			PriceResilient: true,
			Metrics:        map[string]float64{MetricBreadthMA20: 41.5},
		},
		Reasons: []string{"指數距 60 日高 1.2%，但站上 MA20 家數僅 41.5%"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out RegimeDecision
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	// The rule id is what makes a past call auditable; losing it makes the archive opaque.
	if out.RuleID != RuleDistBreadth {
		t.Errorf("RuleID = %q, want %q", out.RuleID, RuleDistBreadth)
	}
	if out.Regime != RegimeDistribution || out.View.Breadth != BreadthNarrowing {
		t.Errorf("decision changed in round trip: %+v", out)
	}
	if !out.View.PriceResilient {
		t.Error("stored predicate lost — the decision would not be reproducible")
	}
}

// Every rule in the approved decision table needs a distinct id, or two different reasons
// for a verdict would be indistinguishable in the archive.
func TestRuleIDsAreUnique(t *testing.T) {
	ids := []string{
		RuleUnknown, RuleBearDowntrend, RuleBearCollapse, RuleDistBreadth,
		RuleDistCoolingInst, RuleDistFailedHighs, RuleBullStrong, RuleBullUptrend,
		RulePullbackOrderly, RuleSidewaysRange, RulePullbackWeakening, RuleSidewaysFallback,
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty rule id")
		}
		if seen[id] {
			t.Errorf("duplicate rule id %q", id)
		}
		seen[id] = true
	}
	if len(seen) != 12 {
		t.Errorf("got %d rule ids, want 12 (R0..R11)", len(seen))
	}
}
