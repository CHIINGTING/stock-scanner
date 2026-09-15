package entryplan_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── EP-6D: the thesis standing, through the decision AND the whole pipeline ───────────
//
// The rule, as the user decided it:
//
//	CONTRADICTED      (SELL / REDUCE / TAKE PROFIT / STOP LOSS) → once an entry shape is
//	                  resolved, NO_VALID_ENTRY at S1 on every input, and NO executable price.
//	"" / undeclared   → only BUY_NOW changes (to INSUFFICIENT_DATA); everything else identical.
//	NOT_CONTRADICTED  → no effect.
//
// WHAT "THESIS-BLIND" MEANS HERE. There is no second, thesis-free implementation to diff against.
// The NOT_CONTRADICTED answer of every pre-existing EP-5 row is asserted against its frozen
// expected value by TestTheDecisionMatrixIsTheSpecifiedTable (dec() supplies NOT_CONTRADICTED),
// and the rows below use that answer as the baseline the other standings are compared with.

// thesisBuyNowSnapshot is a snapshot ComputePlan publishes as BUY_NOW when the standing is
// NOT_CONTRADICTED: the market at 100 inside the [97, 101] pullback band under BULL.
func thesisBuyNowSnapshot() entryplan.Snapshot {
	in := zoneBase()
	in.CurrentPrice, in.MA20 = obs(100), obs(99)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	return in
}

func planHasNoExecutablePrice(p entryplan.Plan) bool {
	return p.IdealEntry == nil && p.MaxChasePrice == nil && p.Invalidation == nil &&
		p.Target1 == nil && p.Target2 == nil && p.RiskReward == nil
}

// executablePart is the part of a plan an unclassified standing must leave alone unless the
// baseline was BUY_NOW.
type executablePart struct {
	IdealEntry    *entryplan.PriceZone
	MaxChasePrice *float64
	Invalidation  *entryplan.Invalidation
	Target1       *entryplan.PriceLevel
	Target2       *entryplan.PriceLevel
	RiskReward    *entryplan.RiskReward
	Evidence      []entryplan.Evidence
	Confidence    entryplan.Confidence
}

func executableOf(p entryplan.Plan) executablePart {
	return executablePart{p.IdealEntry, p.MaxChasePrice, p.Invalidation, p.Target1, p.Target2,
		p.RiskReward, p.Evidence, p.Confidence}
}

// THE THESIS MATRIX, at the DECISION: every row of the decision matrix, re-run under each standing.
func TestTheThesisStandingMatrix(t *testing.T) {
	unclassified := []entryplan.ThesisStanding{"", entryplan.ThesisStanding("SELL")}

	var resolvedRows, buyNowRows, otherRows, unresolvedRows int
	for _, c := range decisionCases() {
		base := c.in
		base.Thesis = entryplan.ThesisNotContradicted
		blind := entryplan.DecideStatus(base)

		// CONTRADICTED.
		in := c.in
		in.Thesis = entryplan.ThesisContradicted
		got := entryplan.DecideStatus(in)
		if !c.in.Semantic.Resolved() {
			unresolvedRows++
			// No shape, no thesis: S0 answers exactly as before.
			if !reflect.DeepEqual(got, blind) {
				t.Errorf("%s: with no resolved shape a CONTRADICTED standing changed %+v into "+
					"%+v — S0 sits ahead of S1 because there is no thesis to contradict",
					c.name, blind, got)
			}
		} else {
			resolvedRows++
			want := entryplan.DecisionResult{Status: entryplan.StatusNoValidEntry,
				Rule:    entryplan.RuleThesisContradicted,
				Reasons: []entryplan.Reason{entryplan.ReasonStatusThesisContradicted}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: CONTRADICTED gave %+v, want %+v — a stock the scanner wants out "+
					"of shows no entry, whatever the price, policy or band", c.name, got, want)
			}
		}

		// UNCLASSIFIED: only BUY_NOW moves.
		if blind.Status == entryplan.StatusBuyNow {
			buyNowRows++
		} else {
			otherRows++
		}
		for _, u := range unclassified {
			in := c.in
			in.Thesis = u
			got := entryplan.DecideStatus(in)
			if blind.Status != entryplan.StatusBuyNow {
				if !reflect.DeepEqual(got, blind) {
					t.Errorf("%s: unclassified standing %q changed a NON-BUY_NOW answer %+v "+
						"into %+v", c.name, u, blind, got)
				}
				continue
			}
			if got.Status != entryplan.StatusInsufficientData || len(got.Reasons) == 0 ||
				got.Reasons[len(got.Reasons)-1] != entryplan.ReasonStatusThesisUnclassified {
				t.Errorf("%s: unclassified standing %q on a BUY_NOW row gave %+v, want "+
					"INSUFFICIENT_DATA ending in STATUS_THESIS_UNCLASSIFIED", c.name, u, got)
			}
		}
	}
	// ANTI-VACUITY: every half of the rule must have been exercised on real rows.
	if resolvedRows < 60 || unresolvedRows < 3 || buyNowRows < 10 || otherRows < 40 {
		t.Errorf("matrix too thin: resolved %d (want ≥60), unresolved %d (≥3), BUY_NOW %d (≥10), "+
			"other %d (≥40)", resolvedRows, unresolvedRows, buyNowRows, otherRows)
	}
	t.Logf("resolved %d, unresolved %d, BUY_NOW %d, other %d", resolvedRows, unresolvedRows,
		buyNowRows, otherRows)
}

// THE THESIS MATRIX, through ComputePlan: every zone and risk case (the priced half of the input
// space), re-run under each standing. This is where "no executable price" is checked, because
// the decision layer holds no price.
func TestTheThesisStandingMatrixThroughComputePlan(t *testing.T) {
	var cases []namedSnapshot
	cases = append(cases, zoneMatrix()...)
	cases = append(cases, riskMatrix()...)

	var contradictedPriced, contradictedChecked, buyNowSeen, unclassifiedChecked int
	for _, c := range cases {
		base := c.in
		base.Thesis = entryplan.ThesisNotContradicted
		blind := entryplan.ComputePlan(base)
		if blind.EntryTrace == nil {
			continue
		}
		resolved := c.in.EntrySemantic.Usable()

		in := c.in
		in.Thesis = entryplan.ThesisContradicted
		got := entryplan.ComputePlan(in)
		if resolved {
			contradictedChecked++
			if !planHasNoExecutablePrice(blind) {
				contradictedPriced++
			}
			if got.Status != entryplan.StatusNoValidEntry ||
				got.EntryTrace.Decision.Rule != entryplan.RuleThesisContradicted {
				t.Errorf("%s: CONTRADICTED → %q at %q, want NO_VALID_ENTRY at S1", c.name,
					got.Status, got.EntryTrace.Decision.Rule)
			}
			if !planHasNoExecutablePrice(got) {
				t.Errorf("%s: CONTRADICTED plan still publishes a price: zone %v chase %v stop %v "+
					"t1 %v t2 %v rr %v", c.name, got.IdealEntry, got.MaxChasePrice,
					got.Invalidation, got.Target1, got.Target2, got.RiskReward)
			}
			if got.EntryTrace.InvariantCheck != entryplan.InvariantCheckClean {
				t.Errorf("%s: the gate stamped %q — S1's output must already satisfy "+
					"THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY", c.name, got.EntryTrace.InvariantCheck)
			}
		}

		for _, u := range []entryplan.ThesisStanding{"", entryplan.ThesisStanding("HOLD")} {
			in := c.in
			in.Thesis = u
			got := entryplan.ComputePlan(in)
			unclassifiedChecked++
			if !reflect.DeepEqual(executableOf(got), executableOf(blind)) {
				t.Errorf("%s: unclassified standing %q moved a price, the evidence or the "+
					"confidence", c.name, u)
			}
			if blind.Status == entryplan.StatusBuyNow {
				buyNowSeen++
				if got.Status != entryplan.StatusInsufficientData {
					t.Errorf("%s: unclassified %q on BUY_NOW → %q, want INSUFFICIENT_DATA",
						c.name, u, got.Status)
				}
			} else if got.Status != blind.Status ||
				!reflect.DeepEqual(got.Reasons, blind.Reasons) {
				t.Errorf("%s: unclassified %q changed a non-BUY_NOW plan: %q %v vs %q %v",
					c.name, u, got.Status, got.Reasons, blind.Status, blind.Reasons)
			}
		}
	}
	// ANTI-VACUITY: the "no price" half must have run on plans that HAD prices to withhold.
	if contradictedPriced < 20 || contradictedChecked < 40 || unclassifiedChecked < 80 {
		t.Errorf("too thin: contradicted %d (priced baseline %d), unclassified %d",
			contradictedChecked, contradictedPriced, unclassifiedChecked)
	}
	t.Logf("contradicted %d (priced baseline %d), unclassified %d (BUY_NOW baseline %d)",
		contradictedChecked, contradictedPriced, unclassifiedChecked, buyNowSeen)
}

// The standing travels VERBATIM onto the trace, and S1's plan is clean at the gate.
func TestTheThesisStandingReachesTheTraceVerbatim(t *testing.T) {
	var ran int
	for _, standing := range []entryplan.ThesisStanding{entryplan.ThesisNotContradicted,
		entryplan.ThesisContradicted, "", entryplan.ThesisStanding("BUY")} {
		in := thesisBuyNowSnapshot()
		in.Thesis = standing
		p := entryplan.ComputePlan(in)
		ran++
		if p.EntryTrace == nil || p.EntryTrace.Decision.Thesis != standing {
			t.Errorf("standing %q: trace %+v does not record it verbatim", standing, p.EntryTrace)
		}
	}
	if ran != 4 {
		t.Fatalf("ran %d", ran)
	}
	control := entryplan.ComputePlan(thesisBuyNowSnapshot())
	if control.Status != entryplan.StatusBuyNow {
		t.Fatalf("control = %q, want BUY_NOW", control.Status)
	}
}

// THE GATE: a planted CONTRADICTED plan that still carries prices loses ALL of them and its
// verdict; a planted UNCLASSIFIED BUY_NOW loses only the verdict.
func TestTheGateRepairsAPlantedThesisViolation(t *testing.T) {
	clean := entryplan.ComputePlan(thesisBuyNowSnapshot())
	if clean.Status != entryplan.StatusBuyNow || clean.EntryTrace == nil || planHasNoExecutablePrice(clean) {
		t.Fatalf("control plan = %q, want a priced BUY_NOW with a trace", clean.Status)
	}
	plant := func(st entryplan.ThesisStanding) entryplan.Plan {
		broken := clean
		tr := *clean.EntryTrace
		tr.Decision.Thesis = st
		broken.EntryTrace = &tr
		return broken
	}

	got, v := entryplan.EnforcePlanInvariants(plant(entryplan.ThesisContradicted))
	counts := invariantCounts(v)
	if counts[entryplan.InvariantThesisContradictedPublishesNoEntry] != 1 ||
		counts[entryplan.InvariantBuyNowThesisNotContradicted] != 1 || len(v) != 2 {
		t.Fatalf("contradicted: violations %v, want exactly THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY "+
			"and BUY_NOW_THESIS_NOT_CONTRADICTED", renderViolations(v))
	}
	if got.Status != entryplan.StatusInsufficientData || !planHasNoExecutablePrice(got) {
		t.Errorf("contradicted: gate published %q with prices present=%v, want "+
			"INSUFFICIENT_DATA and no price", got.Status, !planHasNoExecutablePrice(got))
	}
	if len(got.EntryTrace.WithdrawnFields) != len(entryplan.AllPlanPriceFields) {
		t.Errorf("contradicted: withdrew %v, want every executable field", got.EntryTrace.WithdrawnFields)
	}

	got, v = entryplan.EnforcePlanInvariants(plant(""))
	if len(v) != 1 || v[0].Invariant != entryplan.InvariantBuyNowThesisNotContradicted {
		t.Fatalf("unclassified: violations %v, want exactly BUY_NOW_THESIS_NOT_CONTRADICTED",
			renderViolations(v))
	}
	if got.Status != entryplan.StatusInsufficientData ||
		got.EntryTrace.InvariantCheck != entryplan.InvariantCheckStatusWithdrawn ||
		len(got.EntryTrace.WithdrawnFields) != 0 || planHasNoExecutablePrice(got) {
		t.Errorf("unclassified: gate published %q stamp %q withdrawn %v — want the verdict "+
			"withdrawn and every price kept", got.Status, got.EntryTrace.InvariantCheck,
			got.EntryTrace.WithdrawnFields)
	}
}

// THE GATE ON THE STATUS HALF: a CONTRADICTED plan that publishes no price but still carries an
// entering or waiting status. BUY_NOW is deliberately NOT used here — there
// BUY_NOW_THESIS_NOT_CONTRADICTED withdraws the verdict too and would mask a missing
// status-set membership for THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY.
func TestTheGateWithdrawsAContradictedWaitingOrExtendedStatus(t *testing.T) {
	cases := []struct {
		name   string
		status entryplan.EntryStatus
		why    entryplan.Reason
	}{
		{"WAIT_PULLBACK", entryplan.StatusWaitPullback, entryplan.ReasonStatusPriceAboveEntryZone},
		{"TOO_EXTENDED", entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase},
	}
	var ran int
	for _, c := range cases {
		planted := withThesis(decided(statused(pricedPlan(nil, nil), c.status, c.why),
			entryplan.EntrySemanticPullback, 900), entryplan.ThesisContradicted)
		got, v := entryplan.EnforcePlanInvariants(planted)
		ran++
		if invariantCounts(v)[entryplan.InvariantThesisContradictedPublishesNoEntry] != 1 {
			t.Fatalf("%s: violations %v, want THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY", c.name,
				renderViolations(v))
		}
		if got.Status != entryplan.StatusInsufficientData {
			t.Errorf("%s: the gate kept status %q on a contradicted plan, want INSUFFICIENT_DATA — "+
				"the invariant is a STATUS invariant as well as a price one", c.name, got.Status)
		}
		if !planHasNoExecutablePrice(got) {
			t.Errorf("%s: the gate left a price on a contradicted plan", c.name)
		}
	}
	if ran != len(cases) {
		t.Fatalf("ran %d of %d", ran, len(cases))
	}
}

// THE GATE ON THE TRACE HALF: an S1 plan with a price planted into each step trace comes out with
// all four step traces empty, and its JSON no longer contains the planted numbers.
func TestTheGateStripsEntryNumbersFromAnS1Trace(t *testing.T) {
	planted := s1Plan()
	planted.EntryTrace.Zone.Low, planted.EntryTrace.Zone.High = f(97.3), f(101.7)
	planted.EntryTrace.Chase.Final = f(105.9)
	planted.EntryTrace.Stop.Price = f(95.1)
	planted.EntryTrace.Targets.Target1, planted.EntryTrace.Targets.Target2 = f(111.3), f(122.7)
	marks := []float64{97.3, 101.7, 105.9, 95.1, 111.3, 122.7}

	before := jsonNumbers(t, planted)
	for _, m := range marks {
		if !before[m] {
			t.Fatalf("anti-vacuity: the planted plan's JSON does not contain %v", m)
		}
	}
	got, v := entryplan.EnforcePlanInvariants(planted)
	if invariantCounts(v)[entryplan.InvariantThesisContradictedPublishesNoEntry] != 1 {
		t.Fatalf("violations %v, want THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY", renderViolations(v))
	}
	after := jsonNumbers(t, got)
	for _, m := range marks {
		if after[m] {
			t.Errorf("the gate left the planted entry number %v in the plan's JSON", m)
		}
	}
	if !reflect.DeepEqual(got.EntryTrace.Zone, entryplan.ZoneTrace{}) ||
		!reflect.DeepEqual(got.EntryTrace.Chase, entryplan.ChaseTrace{}) ||
		!reflect.DeepEqual(got.EntryTrace.Stop, entryplan.StopTrace{}) ||
		!reflect.DeepEqual(got.EntryTrace.Targets, entryplan.TargetTrace{}) {
		t.Errorf("the gate did not empty every step trace: %+v", got.EntryTrace)
	}
}

// THE BEHAVIOUR TEST ON THE WIRE: a real snapshot, marshalled. Thesis-blind, its JSON contains
// the zone bounds, the chase ceiling, the stop and both targets; CONTRADICTED, it contains none
// of them. The fixture is chosen so that none of those numbers equals a market observation the
// plan legitimately keeps (checked, not assumed) — otherwise "absent" could not be asserted.
func TestAContradictedPlansJSONCarriesNoEntryNumber(t *testing.T) {
	in := thesisBuyNowSnapshot()
	in.PivotHigh = obs(130) // keep the first target off the pivot observation

	blind := entryplan.ComputePlan(in)
	if blind.IdealEntry == nil || blind.MaxChasePrice == nil || blind.Invalidation == nil ||
		blind.Target1 == nil || blind.Target2 == nil {
		t.Fatalf("thesis-blind fixture is missing a price: %+v", blind)
	}
	entry := map[string]float64{
		"zone.low": blind.IdealEntry.Low, "zone.high": blind.IdealEntry.High,
		"max_chase": *blind.MaxChasePrice, "invalidation": blind.Invalidation.Price,
		"target_1": blind.Target1.Price, "target_2": blind.Target2.Price,
	}
	observations := map[float64]bool{}
	for _, o := range []entryplan.PriceObservation{in.CurrentPrice, in.MA20, in.MA60, in.BaseLow,
		in.PivotHigh, in.PreviousClose} {
		if o.Value != nil {
			observations[*o.Value] = true
		}
	}
	observations[*in.ATR.Value] = true
	for name, v := range entry {
		if observations[v] {
			t.Fatalf("fixture collision: %s = %v equals a kept market observation, so its absence "+
				"cannot be asserted — change the fixture", name, v)
		}
	}
	blindNums := jsonNumbers(t, blind)
	for name, v := range entry {
		if !blindNums[v] {
			t.Fatalf("anti-vacuity: the thesis-blind JSON does not contain %s = %v", name, v)
		}
	}

	in.Thesis = entryplan.ThesisContradicted
	p := entryplan.ComputePlan(in)
	if p.Status != entryplan.StatusNoValidEntry || p.EntryTrace.Decision.Rule != entryplan.RuleThesisContradicted {
		t.Fatalf("contradicted plan = %q at %q, want NO_VALID_ENTRY at S1", p.Status,
			p.EntryTrace.Decision.Rule)
	}
	nums := jsonNumbers(t, p)
	for name, v := range entry {
		if nums[v] {
			t.Errorf("the contradicted plan's JSON still contains %s = %v", name, v)
		}
	}
	// The observations are KEPT, by rule: the evidence census still carries the market.
	for _, v := range []float64{*in.CurrentPrice.Value, *in.MA20.Value, *in.PreviousClose.Value} {
		if !nums[v] {
			t.Errorf("the contradicted plan dropped the market observation %v from its evidence", v)
		}
	}
}

// S1 IS ASKED BEFORE THE STEPS RUN, and the early answer must be the full decision's answer.
//
// For every plan: decided at S1 ⇔ no step ran (all four step traces empty). The cases are chosen
// to cover the BOUNDARIES of that early question, not just the priced half:
//
//	resolved     every zone and risk case (semantic AVAILABLE + PULLBACK / BREAKOUT)
//	unresolved   a subset of matrix() whose semantic is UNKNOWN or absent, plus zoneBase with the
//	             semantic set UNAVAILABLE / AVAILABLE+UNKNOWN / "" — never S1, so the steps
//	             MUST run even under a CONTRADICTED standing (S0 comes first)
//
// each × {CONTRADICTED, NOT_CONTRADICTED, ""}. A plan decided at S1 must also carry no absence
// code and no reading on its decision trace, since no step produced one.
func TestThePreDecisionAgreesWithTheFullDecision(t *testing.T) {
	type bucketed struct {
		bucket string
		c      namedSnapshot
	}
	var cases []bucketed
	for _, c := range append(zoneMatrix(), riskMatrix()...) {
		cases = append(cases, bucketed{"resolved", c})
	}
	// matrix() is 280k rows; a representative subset: well-formed rows (symbol + parsable date)
	// whose semantic is UNKNOWN or absent, capped per kind.
	perKind := map[string]int{}
	for _, c := range matrix() {
		if !c.in.WellFormed() {
			continue
		}
		kind := ""
		switch {
		case strings.HasSuffix(c.name, "sem_unknown"):
			kind = "matrix:sem_unknown"
		case strings.HasSuffix(c.name, "sem_absent"):
			kind = "matrix:sem_absent"
		default:
			continue
		}
		if perKind[kind] >= 300 {
			continue
		}
		perKind[kind]++
		cases = append(cases, bucketed{kind, c})
	}
	for _, sem := range []struct {
		name string
		ev   entryplan.EntrySemanticEvidence
	}{
		{"base:UNAVAILABLE", entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable}},
		{"base:AVAILABLE+UNKNOWN", entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticUnknown}},
		{"base:no status", entryplan.EntrySemanticEvidence{}},
	} {
		in := zoneBase()
		in.EntrySemantic = sem.ev
		cases = append(cases, bucketed{sem.name, namedSnapshot{name: sem.name, in: in}})
	}

	counts := map[string]int{}
	var s1 int
	for _, bc := range cases {
		for _, st := range []entryplan.ThesisStanding{entryplan.ThesisContradicted,
			entryplan.ThesisNotContradicted, ""} {
			in := bc.c.in
			in.Thesis = st
			p := entryplan.ComputePlan(in)
			if p.EntryTrace == nil {
				t.Fatalf("%s/%q: no trace on a well-formed snapshot", bc.c.name, st)
			}
			counts[bc.bucket]++
			empty := reflect.DeepEqual(p.EntryTrace.Zone, entryplan.ZoneTrace{}) &&
				reflect.DeepEqual(p.EntryTrace.Chase, entryplan.ChaseTrace{}) &&
				reflect.DeepEqual(p.EntryTrace.Stop, entryplan.StopTrace{}) &&
				reflect.DeepEqual(p.EntryTrace.Targets, entryplan.TargetTrace{})
			isS1 := p.EntryTrace.Decision.Rule == entryplan.RuleThesisContradicted
			if bc.bucket != "resolved" && isS1 {
				t.Errorf("%s/%q: an unresolved shape was decided at S1 — S0 comes first", bc.c.name, st)
			}
			if isS1 {
				s1++
				if !empty {
					t.Errorf("%s/%q: decided at S1 but the steps ran", bc.c.name, st)
				}
				d := p.EntryTrace.Decision
				if d.ZoneAbsence != "" || d.ZoneReading != "" || d.ChaseAbsence != "" ||
					d.ChaseReading != "" || d.StopAbsence != "" || d.StopReading != "" {
					t.Errorf("%s/%q: an S1 decision trace carries absence codes or readings "+
						"for steps that never ran: %+v", bc.c.name, st, d)
				}
			} else if empty {
				t.Errorf("%s/%q: decided at %s but no step ran", bc.c.name, st,
					p.EntryTrace.Decision.Rule)
			}
		}
	}
	// ANTI-VACUITY, per bucket.
	floors := map[string]int{"resolved": 240, "matrix:sem_unknown": 900, "matrix:sem_absent": 900,
		"base:UNAVAILABLE": 3, "base:AVAILABLE+UNKNOWN": 3, "base:no status": 3}
	for bucket, floor := range floors {
		if counts[bucket] < floor {
			t.Errorf("bucket %s ran %d plans, want ≥ %d", bucket, counts[bucket], floor)
		}
	}
	if s1 < 40 {
		t.Errorf("only %d S1 plans", s1)
	}
	t.Logf("plans per bucket %v, S1 %d", counts, s1)
}

// THE BOUNDARY S1 IS DEFINED ON: the entry SHAPE, not its availability label. A semantic of
// PULLBACK or BREAKOUT handed over with a non-OK status still resolves (S0 reads
// EntrySemantic.Resolved), so a CONTRADICTED standing is answered at S1 — and the pre-decision
// must agree, or the steps would run for a plan the decision says has none.
func TestAContradictedShapeWithANonOKStatusIsStillS1WithNoSteps(t *testing.T) {
	var ran int
	for _, sem := range []entryplan.EntrySemantic{entryplan.EntrySemanticPullback,
		entryplan.EntrySemanticBreakout} {
		for _, status := range []entryplan.Availability{entryplan.Unavailable,
			entryplan.InsufficientData, ""} {
			in := zoneBase()
			in.EntrySemantic = entryplan.EntrySemanticEvidence{Status: status, Semantic: sem}
			in.Thesis = entryplan.ThesisContradicted
			p := entryplan.ComputePlan(in)
			ran++
			if p.EntryTrace == nil || p.EntryTrace.Decision.Rule != entryplan.RuleThesisContradicted ||
				p.Status != entryplan.StatusNoValidEntry {
				t.Fatalf("%s/%q: plan %q, want NO_VALID_ENTRY at S1", sem, status, p.Status)
			}
			if !reflect.DeepEqual(p.EntryTrace.Zone, entryplan.ZoneTrace{}) ||
				!reflect.DeepEqual(p.EntryTrace.Chase, entryplan.ChaseTrace{}) ||
				!reflect.DeepEqual(p.EntryTrace.Stop, entryplan.StopTrace{}) ||
				!reflect.DeepEqual(p.EntryTrace.Targets, entryplan.TargetTrace{}) {
				t.Errorf("%s/%q: decided at S1 but a step ran", sem, status)
			}
			if p.EntryTrace.InvariantCheck != entryplan.InvariantCheckClean {
				t.Errorf("%s/%q: gate stamp %q, want CLEAN — the gate had to repair what the "+
					"pre-decision should have prevented", sem, status, p.EntryTrace.InvariantCheck)
			}
			if !planHasNoExecutablePrice(p) {
				t.Errorf("%s/%q: S1 plan carries a price", sem, status)
			}
		}
	}
	if ran != 6 {
		t.Fatalf("ran %d of 6", ran)
	}
}

// jsonNumbers marshals v and returns every number that appears anywhere in the JSON.
func jsonNumbers(t *testing.T, v any) map[float64]bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := map[float64]bool{}
	var walk func(any)
	walk = func(x any) {
		switch y := x.(type) {
		case float64:
			out[y] = true
		case []any:
			for _, e := range y {
				walk(e)
			}
		case map[string]any:
			for _, e := range y {
				walk(e)
			}
		}
	}
	walk(tree)
	return out
}
