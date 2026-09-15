package entryplan_test

import (
	"reflect"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── EP-6E: the five EP-5 status invariants, through the GATE ──────────────────────────
//
// The checker tests (invariants_test.go) prove each of these five FIRES. They do not prove what
// the gate DOES when it fires, and the EP-6D review measured that deleting any of
//
//	STATUS_HAS_REASON, BUY_NOW_HAS_ZONE, BUY_NOW_PRICE_AUTHORISED,
//	TOO_EXTENDED_ABOVE_CEILING, TOO_EXTENDED_KEEPS_ZONE
//
// from enforce.go's statusInvariants left the suite green. Out of the set, such a violation takes
// withdrawalFor's conservative branch (no owning price field → every field withdrawn) and
// statusViolated no longer fires, so the plan KEEPS its verdict — a BUY_NOW with every price
// deleted. In the set (the current, documented behaviour — enforce.go EnforcePlanInvariants and
// withdrawalFor), the verdict goes back to INSUFFICIENT_DATA, no price is withdrawn, the stamp is
// STATUS_WITHDRAWN and PLAN_INVARIANT_VIOLATED is appended.
//
// Each row below plants EXACTLY ONE violation (asserted before the gate runs, so no other
// invariant can mask the one under test — the defect EP-6D's first BUY_NOW plant had), asserts
// the WHOLE gate output against an expected plan built by hand, and runs its LEGAL TWIN through
// the same gate to show it comes back untouched except for the CLEAN stamp. Several rows exist
// only to kill a PREDICATE weakening inside the checker (a boundary, a dropped status, a skip
// that needs a quote); each names the weakening it is there for.

// sgBase is a legal, fully priced plan with a decision trace, as in fullyPricedPlan:
//
//	invalidation 88 < zone [92, 96] < target1 110 <= target2 120; chase 100; rr 8/14/1.75
//
// with the given status, reason, arm, entry shape and quote, and a NOT_CONTRADICTED standing (so
// a BUY_NOW twin does not trip BUY_NOW_THESIS_NOT_CONTRADICTED).
func sgBase(st entryplan.EntryStatus, why entryplan.Reason, rule entryplan.DecisionRule,
	sem entryplan.EntrySemantic, cur float64) entryplan.Plan {
	p := fullyPricedPlan()
	p.Status, p.Reasons = st, []entryplan.Reason{why}
	p.EntryTrace.Decision = entryplan.DecisionTrace{
		Semantic: sem, CurrentPrice: f(cur), Rule: rule, Status: st,
		Reasons: []entryplan.Reason{why},
		Thesis:  entryplan.ThesisNotContradicted,
	}
	return p
}

// sgExpectVerdictWithdrawn is the gate output the status set implies for a planted status-only
// violation: the planted plan with status INSUFFICIENT_DATA, PLAN_INVARIANT_VIOLATED appended,
// one caveat appended, and (if traced) a COPIED trace stamped STATUS_WITHDRAWN naming exactly
// the one invariant and no withdrawn field. Every price, the decision arm and the evidence are
// exactly the planted ones.
func sgExpectVerdictWithdrawn(t *testing.T, name string, planted, got entryplan.Plan,
	inv entryplan.PlanInvariant) {
	t.Helper()
	want := planted
	want.Status = entryplan.StatusInsufficientData
	want.Reasons = append(append([]entryplan.Reason(nil), planted.Reasons...),
		entryplan.ReasonPlanInvariantViolated)
	if len(got.Caveats) != len(planted.Caveats)+1 ||
		!contains(got.Caveats[len(got.Caveats)-1], "INSUFFICIENT_DATA") {
		t.Errorf("%s: caveats = %v, want exactly one added caveat naming INSUFFICIENT_DATA",
			name, got.Caveats)
	} else {
		for _, c := range got.Caveats {
			if contains(c, "可下單價位") {
				t.Errorf("%s: a PRICE-withdrawal caveat was added for a status-only violation: %q",
					name, c)
			}
		}
		want.Caveats = got.Caveats
	}
	if planted.EntryTrace != nil {
		tr := *planted.EntryTrace
		tr.InvariantCheck = entryplan.InvariantCheckStatusWithdrawn
		tr.InvariantViolations = []entryplan.PlanInvariant{inv}
		tr.WithdrawnFields = nil
		want.EntryTrace = &tr
		if got.EntryTrace == nil {
			t.Fatalf("%s: the gate dropped the trace", name)
		}
		if got.EntryTrace.InvariantCheck != entryplan.InvariantCheckStatusWithdrawn {
			t.Errorf("%s: stamp = %q, want STATUS_WITHDRAWN", name, got.EntryTrace.InvariantCheck)
		}
		if len(got.EntryTrace.WithdrawnFields) != 0 {
			t.Errorf("%s: withdrew %v for a violation about the STATUS", name,
				got.EntryTrace.WithdrawnFields)
		}
	}
	if got.Status != entryplan.StatusInsufficientData {
		t.Errorf("%s: the gate kept status %q, want INSUFFICIENT_DATA", name, got.Status)
	}
	if !reflect.DeepEqual(executableOf(got), executableOf(planted)) {
		t.Errorf("%s: the gate changed an executable price for a status-only violation:\n got %+v\nwant %+v",
			name, executableOf(got), executableOf(planted))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: gate output differs from the documented withdrawal:\n got %+v\nwant %+v",
			name, got, want)
	}
	// The repaired plan is clean: a second pass finds nothing.
	if _, more := entryplan.EnforcePlanInvariants(got); len(more) != 0 {
		t.Errorf("%s: the repaired plan still violates %v", name, renderViolations(more))
	}
}

// sgExpectClean is the control: a legal plan reports nothing and comes back identical except for
// the CLEAN stamp on a copied trace.
func sgExpectClean(t *testing.T, name string, twin entryplan.Plan) {
	t.Helper()
	if v := entryplan.PlanInvariantViolations(twin); len(v) != 0 {
		t.Fatalf("%s: the legal twin is not legal: %v", name, renderViolations(v))
	}
	got, v := entryplan.EnforcePlanInvariants(twin)
	if len(v) != 0 {
		t.Fatalf("%s: the gate reported %v on the legal twin", name, renderViolations(v))
	}
	want := twin
	if twin.EntryTrace != nil {
		tr := *twin.EntryTrace
		tr.InvariantCheck = entryplan.InvariantCheckClean
		want.EntryTrace = &tr
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the gate changed a legal twin:\n got %+v\nwant %+v", name, got, want)
	}
}

type statusGateCase struct {
	name string
	inv  entryplan.PlanInvariant
	// twin is legal; plant is twin with exactly one thing broken.
	twin  entryplan.Plan
	plant entryplan.Plan
}

func statusGateCases() []statusGateCase {
	var cs []statusGateCase
	add := func(name string, inv entryplan.PlanInvariant, twin entryplan.Plan,
		breakIt func(entryplan.Plan) entryplan.Plan) {
		// breakIt receives a STRUCT COPY of the twin, which still shares the twin's pointers, so
		// it must REPLACE a pointer (nil it, or point it at a copy) rather than write through it.
		// The twin is re-checked after the gate ran on the plant, which catches a violation.
		cs = append(cs, statusGateCase{name: name, inv: inv, twin: twin, plant: breakIt(twin)})
	}
	pull, brk := entryplan.EntrySemanticPullback, entryplan.EntrySemanticBreakout

	// Legal twins, one per status. Quotes: BUY_NOW 95 inside [92, 96]; TOO_EXTENDED 100.01 above
	// the ceiling 100; the rest carry no price check.
	buyNow := func() entryplan.Plan {
		return sgBase(entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone,
			entryplan.RuleInsideZone, pull, 95)
	}
	tooExt := func(cur float64) entryplan.Plan {
		return sgBase(entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase,
			entryplan.RuleAboveMaxChase, pull, cur)
	}
	byStatus := []entryplan.Plan{
		buyNow(),
		sgBase(entryplan.StatusWaitPullback, entryplan.ReasonStatusPriceAboveEntryZone,
			entryplan.RuleWaitPullback, pull, 99),
		sgBase(entryplan.StatusWaitBreakout, entryplan.ReasonStatusPriceBelowEntryZone,
			entryplan.RuleWaitBreakout, brk, 90),
		tooExt(100.01),
		sgBase(entryplan.StatusNoValidEntry, entryplan.ReasonStatusNoExecutablePriceRelation,
			entryplan.RuleNoExecutableRelation, pull, 99),
		sgBase(entryplan.StatusInsufficientData, entryplan.ReasonStatusEntryZoneUnavailable,
			entryplan.RuleZoneUnavailable, pull, 99),
	}

	// ── STATUS_HAS_REASON, at EVERY status: kills a weakening that exempts any one status
	// (e.g. `p.Status.IsVerdict()` in place of `p.Status != ""`, or a dropped BUY_NOW).
	for _, twin := range byStatus {
		st := twin.Status
		add("no reason at "+string(st), entryplan.InvariantStatusHasReason, twin,
			func(p entryplan.Plan) entryplan.Plan { p.Reasons = nil; return p })
	}

	// ── BUY_NOW_HAS_ZONE
	// Traced, with a chase ceiling present: kills a weakening that exempts a plan with a
	// ceiling or requires a quote to be absent.
	add("BUY_NOW, no zone, traced, ceiling present", entryplan.InvariantBuyNowHasZone, buyNow(),
		func(p entryplan.Plan) entryplan.Plan { p.IdealEntry = nil; return p })
	// No trace, no ceiling: kills a weakening that requires a recorded quote before checking.
	add("BUY_NOW, no zone, no trace, no ceiling", entryplan.InvariantBuyNowHasZone,
		statused(pricedPlan(legalZone(92, 96), nil), entryplan.StatusBuyNow,
			entryplan.ReasonStatusPriceInsideZone),
		func(p entryplan.Plan) entryplan.Plan { p.IdealEntry = nil; return p })

	// ── BUY_NOW_PRICE_AUTHORISED — the disjunction "inside [Low, High] OR <= an existing ceiling".
	// Above the ceiling. Twin AT the ceiling (second half of the disjunction, boundary inclusive).
	atCeiling := buyNow()
	atCeiling.EntryTrace.Decision.CurrentPrice = f(100)
	atCeiling.EntryTrace.Decision.Rule = entryplan.RuleImmediateChase
	atCeiling.Reasons = []entryplan.Reason{entryplan.ReasonStatusImmediateChasePermitted}
	add("BUY_NOW above its ceiling", entryplan.InvariantBuyNowPriceAuthorised, atCeiling,
		func(p entryplan.Plan) entryplan.Plan {
			tr := *p.EntryTrace
			tr.Decision.CurrentPrice = f(100.01)
			p.EntryTrace = &tr
			return p
		})
	// No ceiling; twin exactly at zone.Low. Plant one cent below: kills dropping the lower bound
	// and `Low <= cur` → `Low < cur` (the twin then fails as a control).
	noCeilingAt := func(cur float64) entryplan.Plan {
		p := buyNow()
		p.MaxChasePrice = nil
		p.EntryTrace.Decision.CurrentPrice = f(cur)
		return p
	}
	moveQuote := func(cur float64) func(entryplan.Plan) entryplan.Plan {
		return func(p entryplan.Plan) entryplan.Plan {
			tr := *p.EntryTrace
			tr.Decision.CurrentPrice = f(cur)
			p.EntryTrace = &tr
			return p
		}
	}
	add("BUY_NOW below its zone, no ceiling", entryplan.InvariantBuyNowPriceAuthorised,
		noCeilingAt(92), moveQuote(91.99))
	// No ceiling; twin exactly at zone.High. Plant one cent above: kills dropping the upper bound
	// and `cur <= High` → `cur < High`.
	add("BUY_NOW above its zone, no ceiling", entryplan.InvariantBuyNowPriceAuthorised,
		noCeilingAt(96), moveQuote(96.01))

	// ── TOO_EXTENDED_ABOVE_CEILING
	// AT the ceiling (equality is not extension) and BELOW it; twin one cent above.
	add("TOO_EXTENDED at its ceiling", entryplan.InvariantTooExtendedAboveCeiling, tooExt(100.01),
		moveQuote(100))
	add("TOO_EXTENDED below its ceiling", entryplan.InvariantTooExtendedAboveCeiling,
		tooExt(100.01), moveQuote(99))
	// No ceiling, traced.
	add("TOO_EXTENDED, no ceiling, traced", entryplan.InvariantTooExtendedAboveCeiling,
		tooExt(105), func(p entryplan.Plan) entryplan.Plan { p.MaxChasePrice = nil; return p })
	// No ceiling, NO trace: the absence of the ceiling is checked without a quote (invariants.go
	// auditStatus: the nil-ceiling add precedes the `cur == nil` skip). Kills a weakening that
	// moves the skip above it.
	add("TOO_EXTENDED, no ceiling, no trace", entryplan.InvariantTooExtendedAboveCeiling,
		statused(pricedPlan(legalZone(92, 96), f(100)), entryplan.StatusTooExtended,
			entryplan.ReasonStatusAboveMaxChase),
		func(p entryplan.Plan) entryplan.Plan { p.MaxChasePrice = nil; return p })

	// ── TOO_EXTENDED_KEEPS_ZONE
	// Traced, ceiling present and exceeded: kills a weakening that exempts a plan with a ceiling.
	add("TOO_EXTENDED, no zone, traced", entryplan.InvariantTooExtendedKeepsZone, tooExt(105),
		func(p entryplan.Plan) entryplan.Plan { p.IdealEntry = nil; return p })
	// No trace: kills a weakening that requires a recorded quote.
	add("TOO_EXTENDED, no zone, no trace", entryplan.InvariantTooExtendedKeepsZone,
		statused(pricedPlan(legalZone(92, 96), f(100)), entryplan.StatusTooExtended,
			entryplan.ReasonStatusAboveMaxChase),
		func(p entryplan.Plan) entryplan.Plan { p.IdealEntry = nil; return p })

	return cs
}

// THE GATE WITHDRAWS THE VERDICT for each of the five EP-5 status invariants EP-6D found
// unguarded, keeps every price, and leaves the legal twin alone.
func TestTheGateWithdrawsTheVerdictForEachEP5StatusInvariant(t *testing.T) {
	floor := map[entryplan.PlanInvariant]int{
		entryplan.InvariantStatusHasReason:         6,
		entryplan.InvariantBuyNowHasZone:           2,
		entryplan.InvariantBuyNowPriceAuthorised:   3,
		entryplan.InvariantTooExtendedAboveCeiling: 4,
		entryplan.InvariantTooExtendedKeepsZone:    2,
	}
	ran := map[entryplan.PlanInvariant]int{}
	for _, c := range statusGateCases() {
		if _, ok := floor[c.inv]; !ok {
			t.Fatalf("%s: case targets %q, which is not one of the five", c.name, c.inv)
		}
		sgExpectClean(t, c.name+" [twin]", c.twin)

		before := entryplan.PlanInvariantViolations(c.plant)
		if len(before) != 1 || before[0].Invariant != c.inv {
			t.Fatalf("%s: the plant reports %v before the gate, want exactly one %s — any other "+
				"violation could mask the one under test", c.name, renderViolations(before), c.inv)
		}
		got, v := entryplan.EnforcePlanInvariants(c.plant)
		if !reflect.DeepEqual(v, before) {
			t.Errorf("%s: gate violations %v differ from the checker's %v", c.name,
				renderViolations(v), renderViolations(before))
		}
		sgExpectVerdictWithdrawn(t, c.name, c.plant, got, c.inv)
		// Re-run the twin AFTER the plant went through the gate: the gate must not have
		// reached back into it.
		sgExpectClean(t, c.name+" [twin after gate]", c.twin)
		ran[c.inv]++
	}
	for inv, n := range floor {
		if ran[inv] < n {
			t.Errorf("%s: ran %d plant(s), want at least %d", inv, ran[inv], n)
		}
	}
}
