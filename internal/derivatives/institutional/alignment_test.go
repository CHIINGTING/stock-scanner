package institutional_test

import (
	"reflect"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// Alignment is evidence, not a score. These tests are mostly about what it REFUSES to do:
// collapse a short-covering shape, treat an observed zero as no data, or treat no data as
// neutral.

func viewOf(t *testing.T, current institutional.Observation, history []institutional.Observation,
	p institutional.Policy,
) institutional.InstitutionView {
	t.Helper()
	v, err := institutional.BuildInstitutionView(current, history, p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestShortCoveringStaysMixedAndKeepsItsComponents is the case §10.4 names outright:
//
//	"POSITION bearish, FLOW bullish, CHANGE improving — a short-covering shape. Compressing
//	that to bullish or bearish throws away the only thing the three semantics were separated
//	to show."
//
// 外資 on 2026-09-04 held −82,389 lots. Suppose the next session they buy 3,000 lots and the
// short position shrinks: the position is STILL net short, the session's trading was net long,
// and the exposure is moving towards flat. All three facts are true at once and no single
// verdict carries them.
func TestShortCoveringStaysMixedAndKeepsItsComponents(t *testing.T) {
	history := series(t, []string{"2026-09-03", "2026-09-04"}, []float64{-85389, -82389})
	current := observation(t, "2026-09-07", fptr(3000), fptr(-79389))

	v := viewOf(t, current, history, institutional.DefaultPolicy())
	a := v.Alignment

	if a.Verdict != institutional.AlignmentMixed {
		t.Fatalf("verdict = %s, want MIXED", a.Verdict)
	}
	if a.Shape != institutional.ShapeShortCovering {
		t.Errorf("shape = %s, want SHORT_COVERING", a.Shape)
	}
	want := map[string]institutional.Direction{
		institutional.ComponentPosition: institutional.DirectionBearish,
		institutional.ComponentFlow:     institutional.DirectionBullish,
		institutional.HorizonLabel(1):   institutional.DirectionBullish,
		institutional.HorizonLabel(3):   institutional.DirectionUnknown,
		institutional.HorizonLabel(5):   institutional.DirectionUnknown,
		institutional.HorizonLabel(10):  institutional.DirectionUnknown,
	}
	for name, dir := range want {
		got, included := a.Direction(name)
		if got != dir {
			t.Errorf("%s: direction = %s, want %s", name, got, dir)
		}
		if included != (dir != institutional.DirectionUnknown) {
			t.Errorf("%s: included = %v", name, included)
		}
	}
	// All three of the named facts survive as numbers.
	if got := lotsOrFail(t, v.Position.ObservedMetric, "POSITION"); got != -79389 {
		t.Errorf("POSITION = %v", got)
	}
	if got := lotsOrFail(t, v.Flow.ObservedMetric, "FLOW"); got != 3000 {
		t.Errorf("FLOW = %v", got)
	}
	if got := lotsOrFail(t, v.Change.ObservedMetric, "CHANGE"); got != 3000 {
		t.Errorf("CHANGE = %v, want +3000 — the short position shrank", got)
	}
	if a.Bullish != 2 || a.Bearish != 1 {
		t.Errorf("counts: bullish %d bearish %d neutral %d", a.Bullish, a.Bearish, a.Neutral)
	}
	// The excluded horizons are named with a reason, not just absent from the tally.
	if len(a.Excluded) != 3 {
		t.Fatalf("Excluded = %v, want the three horizons with no history", a.Excluded)
	}
	for _, c := range a.Components {
		if !c.Included && c.ExcludedReason == "" {
			t.Errorf("%s was excluded with no reason", c.Name)
		}
		if !c.Included && c.Value != nil {
			t.Errorf("%s is excluded but carries a value", c.Name)
		}
	}
}

// TestAlignedVerdictsAndDeterminism — the ordinary cases, plus the property that two runs over
// the same input produce identical output.
func TestAlignedVerdictsAndDeterminism(t *testing.T) {
	rising := series(t, []string{"2026-09-03", "2026-09-04"}, []float64{70000, 74000})
	bull := observation(t, "2026-09-07", fptr(1200), fptr(76174))
	a := viewOf(t, bull, rising, institutional.DefaultPolicy()).Alignment
	if a.Verdict != institutional.AlignedBullish {
		t.Errorf("verdict = %s, want ALIGNED_BULLISH", a.Verdict)
	}
	if a.Shape != institutional.ShapeNone {
		t.Errorf("shape = %s, want NONE", a.Shape)
	}

	falling := series(t, []string{"2026-09-03", "2026-09-04"}, []float64{-70000, -74000})
	bear := observation(t, "2026-09-07", fptr(-1200), fptr(-76174))
	b := viewOf(t, bear, falling, institutional.DefaultPolicy()).Alignment
	if b.Verdict != institutional.AlignedBearish {
		t.Errorf("verdict = %s, want ALIGNED_BEARISH", b.Verdict)
	}
	if b.Shape != institutional.ShapeLongLiquidation {
		// POSITION bearish here, so the long-liquidation label must NOT fire.
		if b.Shape != institutional.ShapeNone {
			t.Errorf("shape = %s", b.Shape)
		}
	}

	again := viewOf(t, bull, rising, institutional.DefaultPolicy()).Alignment
	if !reflect.DeepEqual(a, again) {
		t.Error("two runs over the same input produced different alignments")
	}
	var order []string
	for _, c := range a.Components {
		order = append(order, c.Name)
	}
	wantOrder := []string{
		institutional.ComponentFlow, institutional.ComponentPosition,
		institutional.HorizonLabel(1), institutional.HorizonLabel(3),
		institutional.HorizonLabel(5), institutional.HorizonLabel(10),
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("component order = %v, want %v", order, wantOrder)
	}
}

// TestAllZeroButObservedIsNeutralNotInsufficient.
//
// Every component published a zero. That is a reading — the institution was flat and stayed
// flat — and it is nothing like a day with no file. §6: MISSING is never NEUTRAL, and the
// converse matters just as much.
func TestAllZeroButObservedIsNeutralNotInsufficient(t *testing.T) {
	flat := []string{"2026-08-24", "2026-08-25", "2026-08-26", "2026-08-27", "2026-08-28",
		"2026-08-31", "2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04"}
	history := series(t, flat, make([]float64, len(flat)))
	current := observation(t, "2026-09-07", fptr(0), fptr(0))

	v := viewOf(t, current, history, institutional.DefaultPolicy())
	a := v.Alignment

	if a.Verdict != institutional.AlignmentNeutral {
		t.Fatalf("verdict = %s, want NEUTRAL", a.Verdict)
	}
	if len(a.Included) != 6 || len(a.Excluded) != 0 {
		t.Errorf("included %v, excluded %v — every component was observed", a.Included, a.Excluded)
	}
	if a.Neutral != 6 || a.Bullish != 0 || a.Bearish != 0 {
		t.Errorf("counts: %d neutral, %d bullish, %d bearish", a.Neutral, a.Bullish, a.Bearish)
	}
	for _, c := range a.Components {
		if c.Value == nil || *c.Value != 0 {
			t.Errorf("%s: value = %v, want an observed 0", c.Name, c.Value)
		}
		if c.BelowSignificance {
			t.Errorf("%s: an exact zero was flagged as below significance", c.Name)
		}
		if c.Display != "0 lots" {
			t.Errorf("%s: display = %q", c.Name, c.Display)
		}
	}
	if !v.Flow.IsObservedZero() || !v.Position.IsObservedZero() || !v.Change.IsObservedZero() {
		t.Error("a published zero is not being reported as an observed zero")
	}
}

// TestNoObservationsIsInsufficientDataNotNeutral — the other half of the same rule.
func TestNoObservationsIsInsufficientDataNotNeutral(t *testing.T) {
	current := observationFor(t, "2026-09-07", institutional.ScopeTX,
		institutional.InstitutionDealer, institutional.StatusNotPublished, nil, nil)
	a := viewOf(t, current, nil, institutional.DefaultPolicy()).Alignment

	if a.Verdict != institutional.AlignmentInsufficientData {
		t.Fatalf("verdict = %s, want INSUFFICIENT_DATA", a.Verdict)
	}
	if len(a.Included) != 0 {
		t.Errorf("Included = %v", a.Included)
	}
	for _, c := range a.Components {
		if c.ExcludedReason == "" {
			t.Errorf("%s excluded with no reason", c.Name)
		}
		if c.Direction != institutional.DirectionUnknown {
			t.Errorf("%s: an excluded component has direction %s", c.Name, c.Direction)
		}
	}

	// One observed component is still not agreement with anything.
	one := observation(t, "2026-09-07", fptr(1200), nil)
	b := viewOf(t, one, nil, institutional.DefaultPolicy()).Alignment
	if b.Verdict != institutional.AlignmentInsufficientData {
		t.Errorf("verdict with one component = %s, want INSUFFICIENT_DATA", b.Verdict)
	}
	if len(b.Included) != 1 {
		t.Errorf("Included = %v, want just FLOW", b.Included)
	}
}

// TestMinimumSignificanceIsConfigurableAndKeepsTheComponent — a component under the threshold is
// NEUTRAL and still participates. Dropping it would make a quiet session look like a session
// with no data.
func TestMinimumSignificanceIsConfigurableAndKeepsTheComponent(t *testing.T) {
	history := series(t, []string{"2026-09-03", "2026-09-04"}, []float64{75974, 76074})
	current := observation(t, "2026-09-07", fptr(100), fptr(76174))

	loose := viewOf(t, current, history, institutional.DefaultPolicy()).Alignment
	if loose.Verdict != institutional.AlignedBullish {
		t.Errorf("with no threshold: verdict = %s, want ALIGNED_BULLISH", loose.Verdict)
	}

	p := institutional.DefaultPolicy()
	p.MinimumSignificanceLots = 1000
	strict := viewOf(t, current, history, p).Alignment

	flowDir, included := strict.Direction(institutional.ComponentFlow)
	if !included {
		t.Fatal("a 100-lot FLOW under a 1,000-lot threshold was excluded rather than neutral")
	}
	if flowDir != institutional.DirectionNeutral {
		t.Errorf("FLOW direction = %s, want NEUTRAL under a 1,000-lot threshold", flowDir)
	}
	for _, c := range strict.Components {
		if c.Name == institutional.ComponentFlow && !c.BelowSignificance {
			t.Error("FLOW is under the threshold but not flagged as such")
		}
	}
	// POSITION is 76,174 — far over the threshold — so the verdict is still directional.
	if strict.Verdict != institutional.AlignedBullish {
		t.Errorf("verdict = %s, want ALIGNED_BULLISH", strict.Verdict)
	}
	if strict.MinimumSignificanceLots != 1000 {
		t.Errorf("the alignment does not record the threshold it was computed under")
	}
	if strict.RuleVersion != institutional.AlignmentRuleVersion {
		t.Errorf("RuleVersion = %q", strict.RuleVersion)
	}
}

// TestAnAbsentComponentIsExcludedWhileAZeroIsIncluded — the distinction, at the alignment layer.
func TestAnAbsentComponentIsExcludedWhileAZeroIsIncluded(t *testing.T) {
	history := series(t, []string{"2026-09-04"}, []float64{76174})
	zeroFlow := observation(t, "2026-09-07", fptr(0), fptr(76174))
	absentFlow := observation(t, "2026-09-07", nil, fptr(76174))

	withZero := viewOf(t, zeroFlow, history, institutional.DefaultPolicy()).Alignment
	withAbsent := viewOf(t, absentFlow, history, institutional.DefaultPolicy()).Alignment

	if dir, ok := withZero.Direction(institutional.ComponentFlow); !ok || dir != institutional.DirectionNeutral {
		t.Errorf("zero FLOW: direction %s, included %v; want NEUTRAL and included", dir, ok)
	}
	if dir, ok := withAbsent.Direction(institutional.ComponentFlow); ok || dir != institutional.DirectionUnknown {
		t.Errorf("absent FLOW: direction %s, included %v; want UNKNOWN and excluded", dir, ok)
	}
	if withZero.Neutral == withAbsent.Neutral {
		t.Error("an absent FLOW and a zero FLOW produced the same neutral count")
	}
}
