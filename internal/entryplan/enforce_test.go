package entryplan_test

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── the production invariant gate ─────────────────────────────────────────────────────
//
// EP-3a shipped the checker and left the DECISION to EP-3b. The decision is: withdraw the
// offending prices, keep the plan, and record what happened. EP-4 narrowed "offending" from
// every price to the violated field and its dependents — see
// TestTheGateWithdrawsExactlyTheAffectedFields. This file tests both halves: that the gate does
// that on a broken plan, and that it RUNS on every plan ComputePlan returns.
//
// The second half is the one that is easy to get wrong. No input reaches a violation, so a
// test that only fed the gate real plans would pass with the call deleted from ComputePlan.
// What makes the call observable is the STAMP: every well-formed plan carries
// InvariantCheck = CLEAN, so "the gate ran and found nothing" and "the gate is gone" are
// different outputs.

// THE STAMP. Over every input this package can be handed.
//
// This is the assertion that fails if the gate is removed from ComputePlan — the mutation that
// would otherwise be invisible, because nothing production can compute breaks an invariant.
func TestEveryPlanCarriesTheInvariantGatesStamp(t *testing.T) {
	var wellFormed, malformed int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)

		if !c.in.WellFormed() {
			malformed++
			if p.EntryTrace != nil {
				t.Fatalf("%s: a malformed snapshot produced a work record — there is nothing "+
					"for it to be a record OF", c.name)
			}
			continue
		}
		wellFormed++
		if p.EntryTrace == nil {
			t.Fatalf("%s: no trace, so the gate's outcome is unrecorded and removing the "+
				"gate would be invisible", c.name)
		}
		if p.EntryTrace.InvariantCheck != entryplan.InvariantCheckClean {
			t.Fatalf("%s: invariant check = %q, want CLEAN. If the gate really fired, this "+
				"package computed an illegal price: %v", c.name,
				p.EntryTrace.InvariantCheck, p.EntryTrace.InvariantViolations)
		}
		if len(p.EntryTrace.InvariantViolations) != 0 {
			t.Fatalf("%s: violations %v on a plan stamped CLEAN", c.name,
				p.EntryTrace.InvariantViolations)
		}
		// The independent check, which is what makes the stamp mean anything: the checker
		// itself, run again here, must agree.
		if v := entryplan.PlanInvariantViolations(p); len(v) != 0 {
			t.Fatalf("%s: the plan breaks its own invariants and was returned anyway:\n  %v",
				c.name, renderViolations(v))
		}
	}
	if wellFormed == 0 || malformed == 0 {
		t.Fatalf("the sweep saw %d well-formed and %d malformed snapshots — one branch never "+
			"ran", wellFormed, malformed)
	}
}

// The gate, driven DIRECTLY on a plan production cannot build. The same inversion EP-3a used
// for the checker: the subject under test is the gate, and the planted plan is the fixture.
func TestTheInvariantGateWithdrawsPricesFromABrokenPlan(t *testing.T) {
	broken := plantedBrokenPlan()
	broken.EntryTrace = &entryplan.EntryTrace{RuleVersion: entryplan.RuleVersion}
	broken.Evidence = append(broken.Evidence, entryplan.Evidence{
		Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Available, Value: f(902.5)})

	got, violations := entryplan.EnforcePlanInvariants(broken)
	if len(violations) == 0 {
		t.Fatal("the gate found nothing wrong with the plan built to break all seven " +
			"invariants — the fixture and the checker have drifted apart")
	}

	if got.HasExecutablePrices() {
		t.Errorf("prices survived the gate: %+v", got)
	}
	if got.IdealEntry != nil || got.MaxChasePrice != nil {
		t.Errorf("the EP-3b prices survived: zone %+v, chase %v", got.IdealEntry, got.MaxChasePrice)
	}
	if !hasReason(got, entryplan.ReasonPlanInvariantViolated) {
		t.Errorf("reasons = %v, want PLAN_INVARIANT_VIOLATED — a withdrawal with no code is "+
			"indistinguishable from a plan that never had a price", got.Reasons)
	}
	if got.EntryTrace == nil {
		t.Fatal("the trace was dropped, so nothing records what the gate did")
	}
	if got.EntryTrace.InvariantCheck != entryplan.InvariantCheckPricesWithdrawn {
		t.Errorf("outcome = %q, want PRICES_WITHDRAWN", got.EntryTrace.InvariantCheck)
	}
	// The codes, in registry order, deduplicated: what a consumer counts on.
	//
	// EP-4's four are deliberately NOT in this list. plantedBrokenPlan's stop is -Inf (which
	// really is below a zone low of 0), its Target1 is 900 (above a zone high of -5), its
	// Target2 is NaN (and `NaN < 900` is false, exactly as `NaN <= 0` is false for a zone
	// bound) and its RiskReward is 10/20/2. So the fixture that breaks all of EP-3a's seven
	// satisfies all of EP-4's four, which is the reason invariantCases() plants four more.
	want := []entryplan.PlanInvariant{
		entryplan.InvariantFloatNotNaN,
		entryplan.InvariantFloatNotInf,
		entryplan.InvariantZoneLowPositive,
		entryplan.InvariantZoneHighPositive,
		entryplan.InvariantZoneOrdered,
		entryplan.InvariantMaxChasePositive,
		entryplan.InvariantMaxChaseCoversZone,
	}
	if !reflect.DeepEqual(got.EntryTrace.InvariantViolations, want) {
		t.Errorf("violated codes = %v, want %v (distinct, in AllPlanInvariants order)",
			got.EntryTrace.InvariantViolations, want)
	}
	// And the WITHDRAWN FIELDS are recorded. Everything went here, because the zone itself is
	// broken and every other price is computed from it.
	if !reflect.DeepEqual(got.EntryTrace.WithdrawnFields, entryplan.AllPlanPriceFields) {
		t.Errorf("withdrawn fields = %v, want all of %v — a plan whose ZONE is illegal has "+
			"nothing left that was computed from a legal entry", got.EntryTrace.WithdrawnFields,
			entryplan.AllPlanPriceFields)
	}
	// The EVIDENCE survives. The prices are what a reader acts on; the evidence is what
	// somebody debugs it from.
	if len(got.Evidence) != len(broken.Evidence) {
		t.Errorf("the gate dropped evidence rows: %d → %d", len(broken.Evidence),
			len(got.Evidence))
	}
	var explained bool
	for _, c := range got.Caveats {
		if contains(c, "不變式") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("no caveat explains the withdrawal: %v", got.Caveats)
	}
}

// fullyPricedPlan is a LEGAL plan carrying all six executable prices, and it is the fixture the
// dependency-aware withdrawal is measured against.
//
// The geometry is deliberately ordinary and mutually consistent, so that a single planted fault
// is the ONLY thing wrong with any derived case, and every relation the checker tests holds
// with room to spare:
//
//	invalidation 88  <  zone.Low 92  <=  zone.High 96  <  target1 110  <=  target2 120
//	chase 100 >= zone.High;  risk 96 − 88 = 8;  reward 110 − 96 = 14;  ratio 1.75
//
// EP-5 added the STATUS half of "legal", and it is the same idea one field over: WAIT_PULLBACK
// on a PULLBACK shape, defended by the code S17 actually publishes, with the quote 99 recorded
// on the decision trace — above the band, which is what makes WAIT_PULLBACK true rather than
// merely well typed. A fixture missing any of those three is not clean to begin with, and every
// case derived from it would then be measuring two faults.
func fullyPricedPlan() entryplan.Plan {
	return entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		Status:        entryplan.StatusWaitPullback,
		Reasons:       []entryplan.Reason{entryplan.ReasonStatusPriceAboveEntryZone},
		RuleVersion:   entryplan.RuleVersion,
		IdealEntry:    legalZone(92, 96),
		MaxChasePrice: f(100),
		Invalidation:  &entryplan.Invalidation{Price: 88, PriceBasis: entryplan.PriceBasisRaw},
		Target1:       &entryplan.PriceLevel{Price: 110, PriceBasis: entryplan.PriceBasisRaw},
		Target2:       &entryplan.PriceLevel{Price: 120, PriceBasis: entryplan.PriceBasisRaw},
		RiskReward: &entryplan.RiskReward{RiskPerShare: 8, RewardPerShare: 14, Ratio: 1.75,
			Target: "TARGET_1"},
		EntryTrace: &entryplan.EntryTrace{
			RuleVersion: entryplan.RuleVersion,
			Decision: entryplan.DecisionTrace{
				Semantic: entryplan.EntrySemanticPullback, CurrentPrice: f(99),
				Rule: entryplan.RuleWaitPullback, Status: entryplan.StatusWaitPullback,
				Reasons: []entryplan.Reason{entryplan.ReasonStatusPriceAboveEntryZone},
			},
		},
	}
}

// EVERY price-bearing field, held against Plan's own type — AND the DEPENDENCY-AWARE withdrawal
// EP-4 replaced EP-3b's "withdraw everything" with.
//
// # Why the rule changed, stated as the test that would have caught the old one
//
// EP-3b withdrew all six fields for any violation, arguing that "a bug does not respect field
// boundaries". With two prices and one dependency that was right. With six prices and a real
// graph it deletes correct work: the first row below plants a bad OPTIONAL second target, and
// under the old rule the entry zone, the chase ceiling, the stop, the first target and the
// risk/reward — five independently derived, legal numbers — would all have gone with it. The
// zone is the field a reader acts on first, so that is not caution, it is hiding a usable entry
// behind a fault in a sanity bound.
//
// enforce.go still assigns the six fields BY NAME rather than reflectively, because "may a
// reader place an order from this field" is a judgement each field's own work item has to make.
// That argument is only safe if a field ADDED later fails a test, which is the second half of
// this one: the reflective sweep finds every price-bearing field on the type, and the ledger at
// the bottom requires every one of them to have been withdrawn by SOME case.
func TestTheGateWithdrawsExactlyTheAffectedFields(t *testing.T) {
	planType := reflect.TypeOf(entryplan.Plan{})
	var covered []string
	for n := 0; n < planType.NumField(); n++ {
		if isPriceBearing(planType.Field(n).Type) {
			covered = append(covered, planType.Field(n).Name)
		}
	}
	if len(covered) < 6 {
		t.Fatalf("only %d price-bearing fields found (%v) — the reflective sweep has stopped "+
			"matching the Plan type and would pass vacuously", len(covered), covered)
	}

	all := []string{"IdealEntry", "MaxChasePrice", "Invalidation", "Target1", "Target2",
		"RiskReward"}

	cases := []struct {
		name string
		// break_ plants exactly ONE fault on an otherwise legal, fully-priced plan.
		break_ func(*entryplan.Plan)
		// want is the EXACT set of fields that must be withdrawn — not "at least", because
		// "at least" is satisfied by the old withdraw-everything rule.
		want []string
	}{
		{
			// THE CASE THAT MOTIVATED THE CHANGE. An optional 3.5R target pulled below the
			// first by a valuation ceiling is the one violation production could plausibly
			// produce, and it must cost the plan NOTHING ELSE.
			name:   "an inverted second target costs only the second target",
			break_: func(p *entryplan.Plan) { p.Target2 = &entryplan.PriceLevel{Price: 100} },
			want:   []string{"Target2"},
		},
		{
			name:   "a bad risk/reward costs only the risk/reward",
			break_: func(p *entryplan.Plan) { p.RiskReward.Ratio = 0 },
			want:   []string{"RiskReward"},
		},
		{
			// The chase ceiling is a LEAF: nothing is computed from it, so a chase fault must
			// not reach the risk side. Under EP-3b's rule this deleted all six.
			name:   "a bad chase ceiling costs only the chase ceiling",
			break_: func(p *entryplan.Plan) { p.MaxChasePrice = f(-1) },
			want:   []string{"MaxChasePrice"},
		},
		{
			// A first target at or below the entry takes the second and the R:R with it,
			// because Target2's ORDERING is defined against Target1 and the R:R's reward is
			// measured TO Target1. It does NOT take the zone or the stop, which were computed
			// before it.
			name:   "a first target below the entry costs the targets and the R:R",
			break_: func(p *entryplan.Plan) { p.Target1 = &entryplan.PriceLevel{Price: 90} },
			want:   []string{"Target1", "Target2", "RiskReward"},
		},
		{
			// The stop is the risk DENOMINATOR, so everything measured from it goes — and the
			// zone and the chase ceiling, which are not, stay.
			name: "a stop inside the zone costs the stop, the targets and the R:R",
			break_: func(p *entryplan.Plan) {
				p.Invalidation = &entryplan.Invalidation{Price: 93}
			},
			want: []string{"Invalidation", "Target1", "Target2", "RiskReward"},
		},
		{
			// The zone is the ROOT: every other price is measured from one of its bounds.
			name:   "an inverted zone costs everything",
			break_: func(p *entryplan.Plan) { p.IdealEntry = legalZone(96, 92) },
			want:   all,
		},
		{
			// A NON-FINITE FLOAT WHOSE PATH NAMES AN EXECUTABLE FIELD is attributed to it.
			name: "a NaN on the second target's R multiple costs only the second target",
			break_: func(p *entryplan.Plan) {
				p.Target2.RMultiple = f(math.NaN())
			},
			want: []string{"Target2"},
		},
		{
			// AND ONE WHOSE PATH NAMES NOTHING EXECUTABLE COSTS EVERYTHING. This is the case
			// EP-3b's argument actually described: a non-finite number the checker cannot
			// attribute is an arithmetic bug that respects no field boundary. Keeping it as
			// the conservative branch is what makes the narrowing above defensible rather
			// than merely convenient.
			name: "a NaN in the evidence census costs everything",
			break_: func(p *entryplan.Plan) {
				p.Evidence = append(p.Evidence,
					entryplan.Evidence{Key: "X", Status: entryplan.Available, Value: f(math.NaN())})
			},
			want: all,
		},
	}

	ledger := map[string]int{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The fixture must be CLEAN before the fault is planted, or the case is measuring
			// two faults and this test cannot say which set belongs to which.
			base := fullyPricedPlan()
			if v := entryplan.PlanInvariantViolations(base); len(v) != 0 {
				t.Fatalf("the fully-priced fixture is not legal to begin with: %v",
					renderViolations(v))
			}
			broken := fullyPricedPlan()
			c.break_(&broken)

			got, violations := entryplan.EnforcePlanInvariants(broken)
			if len(violations) == 0 {
				t.Fatal("the planted fault broke no invariant, so nothing was withdrawn and " +
					"this case asserts nothing")
			}

			want := map[string]bool{}
			for _, name := range c.want {
				want[name] = true
				ledger[name]++
			}
			v := reflect.ValueOf(got)
			for n := 0; n < planType.NumField(); n++ {
				field := planType.Field(n)
				if !isPriceBearing(field.Type) {
					continue
				}
				withdrawn := v.Field(n).IsNil()
				if want[field.Name] && !withdrawn {
					t.Errorf("Plan.%s survived with value %v — it is the violated field or is "+
						"computed from it, so publishing it would publish arithmetic done on a "+
						"number the checker has just refused", field.Name,
						v.Field(n).Interface())
				}
				if !want[field.Name] && withdrawn {
					t.Errorf("Plan.%s was withdrawn and nothing about it is wrong — it does "+
						"not depend on the violated field, so deleting it removes correct work "+
						"and hides a usable number behind an unrelated fault", field.Name)
				}
			}
			// The machine-readable record of the same thing. Without it PRICES_WITHDRAWN
			// cannot say WHICH, and a consumer reading only the stamp would assume all six.
			if got.EntryTrace == nil {
				t.Fatal("no trace, so the withdrawal is unrecorded")
			}
			gotFields := map[string]bool{}
			for _, fl := range got.EntryTrace.WithdrawnFields {
				gotFields[string(fl)] = true
			}
			if !reflect.DeepEqual(gotFields, want) {
				t.Errorf("trace records %v withdrawn, want exactly %v",
					got.EntryTrace.WithdrawnFields, c.want)
			}
		})
	}

	// ANTI-VACUITY. Every price-bearing field on the type must have been withdrawn by some
	// case above, or the withdrawal of that field has never been observed and could be
	// deleted from enforce.go with this suite green.
	for _, name := range covered {
		if ledger[name] == 0 {
			t.Errorf("no case above withdraws Plan.%s — the assignment could be removed from "+
				"EnforcePlanInvariants and nothing would notice. Ledger: %v", name, ledger)
		}
	}
}

// No PlanPriceField name may be a PREFIX of another, because fieldForPath attributes a violation
// by matching the path segment after "Plan." against the registry.
//
// Not hypothetical arithmetic: a field called "Target10" would be attributed to "Target1", so a
// violation on it would withdraw Target1's dependents and leave the real field published.
func TestNoPriceFieldNameIsAPrefixOfAnother(t *testing.T) {
	fields := entryplan.AllPlanPriceFields
	if len(fields) < 6 {
		t.Fatalf("the registry holds %d fields — this check would pass vacuously", len(fields))
	}
	var pairs int
	for _, a := range fields {
		for _, b := range fields {
			if a == b {
				continue
			}
			pairs++
			if strings.HasPrefix(string(b), string(a)) {
				t.Errorf("%q is a prefix of %q — fieldForPath would attribute a violation on "+
					"the second to the first, and withdraw the wrong set of fields", a, b)
			}
		}
	}
	if pairs == 0 {
		t.Fatal("no pair was compared")
	}
	// And the registry must be the Plan's OWN field names, or the path matching is comparing
	// strings that only look alike.
	planType := reflect.TypeOf(entryplan.Plan{})
	for _, fl := range fields {
		if _, ok := planType.FieldByName(string(fl)); !ok {
			t.Errorf("AllPlanPriceFields lists %q and Plan has no such field — the withdrawal "+
				"vocabulary and the violation paths have drifted apart", fl)
		}
	}
}

// A clean plan passes through UNCHANGED apart from the stamp. A gate that edited good plans
// would be switched off by the next person in a hurry.
func TestACleanPlanPassesTheGateUnchanged(t *testing.T) {
	clean := entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		Status:        entryplan.StatusInsufficientData,
		RuleVersion:   entryplan.RuleVersion,
		IdealEntry:    legalZone(92, 96),
		MaxChasePrice: f(100),
		Reasons:       []entryplan.Reason{entryplan.ReasonStatusEntryZoneUnavailable},
		EntryTrace:    &entryplan.EntryTrace{RuleVersion: entryplan.RuleVersion},
	}
	got, violations := entryplan.EnforcePlanInvariants(clean)
	if len(violations) != 0 {
		t.Fatalf("a legal plan was reported as violating %v", renderViolations(violations))
	}
	if got.IdealEntry == nil || got.MaxChasePrice == nil || *got.MaxChasePrice != 100 {
		t.Errorf("the gate withdrew prices from a clean plan: %+v / %v", got.IdealEntry,
			got.MaxChasePrice)
	}
	if !reflect.DeepEqual(got.Reasons, clean.Reasons) {
		t.Errorf("reasons changed: %v → %v", clean.Reasons, got.Reasons)
	}
	if len(got.Caveats) != 0 {
		t.Errorf("a caveat was added to a clean plan: %v", got.Caveats)
	}
	if got.EntryTrace.InvariantCheck != entryplan.InvariantCheckClean {
		t.Errorf("outcome = %q, want CLEAN", got.EntryTrace.InvariantCheck)
	}
}

// The gate is PURE: it does not write through the trace pointer its argument carries, so
// calling it cannot change a plan the caller still holds — and it can be called twice.
func TestTheInvariantGateDoesNotMutateItsArgument(t *testing.T) {
	trace := &entryplan.EntryTrace{RuleVersion: entryplan.RuleVersion}
	arg := entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		IdealEntry: legalZone(0, -5), MaxChasePrice: f(-6),
		EntryTrace: trace,
	}
	got, _ := entryplan.EnforcePlanInvariants(arg)

	if trace.InvariantCheck != "" {
		t.Errorf("the caller's own trace was stamped %q — the gate reached through the "+
			"pointer into a struct it does not own", trace.InvariantCheck)
	}
	if arg.IdealEntry == nil || arg.MaxChasePrice == nil {
		t.Error("the caller's own plan lost its prices")
	}
	if got.EntryTrace == trace {
		t.Error("the returned plan shares the caller's trace pointer, so the next writer of " +
			"either one changes both")
	}
	// Running it AGAIN on its own output is safe, and the second pass finds nothing — because
	// withdrawing the prices is what fixed the plan. The outcome stamp therefore flips to
	// CLEAN on the second pass, which is not a contradiction: it is the honest answer to "does
	// THIS plan break an invariant", and the withdrawal reason is what preserves the history.
	again, moreViolations := entryplan.EnforcePlanInvariants(got)
	if len(moreViolations) != 0 {
		t.Errorf("the withdrawal did not make the plan clean: %v", renderViolations(moreViolations))
	}
	if again.HasExecutablePrices() {
		t.Error("the second pass restored a price")
	}
	if !hasReason(again, entryplan.ReasonPlanInvariantViolated) {
		t.Errorf("the second pass lost the withdrawal code: %v", again.Reasons)
	}
	if len(again.Reasons) != len(got.Reasons) || len(again.Caveats) != len(got.Caveats) {
		t.Errorf("the second pass duplicated the explanation: reasons %v, caveats %v",
			again.Reasons, again.Caveats)
	}
}

// A plan with no trace at all is still gated. It is the shape a hand-built plan has, and the
// gate must not depend on a field ComputePlan happens to fill.
func TestTheGateWorksOnAPlanWithNoTrace(t *testing.T) {
	got, violations := entryplan.EnforcePlanInvariants(entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09", MaxChasePrice: f(math.Inf(1)),
	})
	if len(violations) == 0 {
		t.Fatal("an infinite chase price passed the gate")
	}
	if got.MaxChasePrice != nil {
		t.Errorf("the price survived: %v", *got.MaxChasePrice)
	}
	if got.EntryTrace != nil {
		t.Errorf("the gate invented a work record for a plan it did not compute: %+v",
			got.EntryTrace)
	}
	if !hasReason(got, entryplan.ReasonPlanInvariantViolated) {
		t.Errorf("reasons = %v, want the withdrawal code", got.Reasons)
	}
}

// The reason is RESERVED, and this is the test the reservation points at: production emits it,
// from EnforcePlanInvariants, and no ComputePlan input does.
func TestTheWithdrawalReasonIsReservedAndProduced(t *testing.T) {
	if !entryplan.ReservedReasons[entryplan.ReasonPlanInvariantViolated] {
		t.Error("PLAN_INVARIANT_VIOLATED is not marked reserved, so the registry test would " +
			"demand a ComputePlan input that produces it — and reaching it from ComputePlan " +
			"would mean this package computed an illegal price")
	}
	got, _ := entryplan.EnforcePlanInvariants(entryplan.Plan{MaxChasePrice: f(0)})
	if !hasReason(got, entryplan.ReasonPlanInvariantViolated) {
		t.Error("the reserved code has no producer at all, which makes it dead rather than " +
			"reserved")
	}
	// And it never appears on a real plan.
	for _, c := range allSnapshots() {
		if hasReason(entryplan.ComputePlan(c.in), entryplan.ReasonPlanInvariantViolated) {
			t.Fatalf("%s: ComputePlan withdrew a price — that is a bug in this package, not "+
				"an input class", c.name)
		}
	}
}

// The withdrawal code is not repeated when the gate runs on a plan that already carries it.
func TestTheWithdrawalCodeIsNotRepeated(t *testing.T) {
	p := entryplan.Plan{MaxChasePrice: f(-1),
		Reasons: []entryplan.Reason{entryplan.ReasonPlanInvariantViolated}}
	got, _ := entryplan.EnforcePlanInvariants(p)
	var n int
	for _, r := range got.Reasons {
		if r == entryplan.ReasonPlanInvariantViolated {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the code appears %d times — a repeated reason reads as two independent "+
			"findings in an archived list", n)
	}
}

// ── EP-5: the gate withdraws a CONTRADICTED VERDICT the way it withdraws a bad price ──

// A STATUS-ONLY VIOLATION TAKES THE STATUS AND NOTHING ELSE.
//
// The seven EP-5 invariants have no price to withdraw — "BUY_NOW with no zone" is not repaired
// by deleting a number — so the gate sends the status back to INSUFFICIENT_DATA and stamps
// STATUS_WITHDRAWN. Three things have to be true at once for that to be honest:
//
//	the status is INSUFFICIENT_DATA   the only value that claims nothing about the trade, and
//	                                  a STATE, which is what "this package contradicted itself"
//	                                  is from a reader's point of view
//	every PRICE survives              the numbers are not implicated; taking them would hide a
//	                                  usable entry behind a fault in the layer above them
//	the ARM is still on the trace     so "the gate withdrew the verdict" and "no decision ran"
//	                                  stay different outputs
func TestTheGateWithdrawsAContradictedVerdictAndKeepsThePrices(t *testing.T) {
	broken := fullyPricedPlan()
	// A verdict that contradicts the plan's own shape: WAIT_PULLBACK on a breakout. Every
	// price stays legal, so this is the ONLY thing wrong.
	broken.EntryTrace.Decision.Semantic = entryplan.EntrySemanticBreakout
	broken.EntryTrace.Decision.Rule = entryplan.RuleWaitPullback

	got, violations := entryplan.EnforcePlanInvariants(broken)
	if len(violations) != 1 ||
		violations[0].Invariant != entryplan.InvariantWaitMatchesSemantic {
		t.Fatalf("violations = %v, want exactly WAIT_MATCHES_SEMANTIC — this fixture is built "+
			"so the verdict is the only thing wrong", renderViolations(violations))
	}
	if got.Status != entryplan.StatusInsufficientData {
		t.Errorf("status = %q, want INSUFFICIENT_DATA — every other value is a VERDICT, and a "+
			"plan whose verdict contradicts its own numbers has not reached one", got.Status)
	}
	if got.EntryTrace == nil {
		t.Fatal("the trace was dropped, so nothing records what the gate did")
	}
	if got.EntryTrace.InvariantCheck != entryplan.InvariantCheckStatusWithdrawn {
		t.Errorf("outcome = %q, want STATUS_WITHDRAWN — PRICES_WITHDRAWN would send a reader "+
			"looking for a number that is still there", got.EntryTrace.InvariantCheck)
	}
	if len(got.EntryTrace.WithdrawnFields) != 0 {
		t.Errorf("fields %v were withdrawn for a violation about the STATUS",
			got.EntryTrace.WithdrawnFields)
	}
	// EVERY PRICE SURVIVES. Asserted field by field rather than through HasExecutablePrices,
	// which is satisfied by one survivor.
	if got.IdealEntry == nil || got.MaxChasePrice == nil || got.Invalidation == nil ||
		got.Target1 == nil || got.Target2 == nil || got.RiskReward == nil {
		t.Errorf("a price was withdrawn for a fault in the DECISION: zone %v, chase %v, "+
			"stop %v, t1 %v, t2 %v, rr %v", got.IdealEntry, got.MaxChasePrice,
			got.Invalidation, got.Target1, got.Target2, got.RiskReward)
	}
	// THE ARM SURVIVES, and so does what it originally said.
	if got.EntryTrace.Decision.Rule != entryplan.RuleWaitPullback ||
		got.EntryTrace.Decision.Status != entryplan.StatusWaitPullback {
		t.Errorf("the decision trace no longer records what was withdrawn: %+v",
			got.EntryTrace.Decision)
	}
	if !hasReason(got, entryplan.ReasonPlanInvariantViolated) {
		t.Errorf("reasons = %v, want PLAN_INVARIANT_VIOLATED", got.Reasons)
	}
	var explained bool
	for _, c := range got.Caveats {
		if contains(c, "INSUFFICIENT_DATA") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("no caveat explains the withdrawn verdict: %v", got.Caveats)
	}
	// AND THE GATE IS IDEMPOTENT: the repaired plan is clean, so a second pass changes
	// nothing. A gate whose own output fails its own check is a gate that cannot be re-run.
	again, more := entryplan.EnforcePlanInvariants(got)
	if len(more) != 0 {
		t.Errorf("the repaired plan still violates %v", renderViolations(more))
	}
	if again.Status != entryplan.StatusInsufficientData {
		t.Errorf("the second pass changed the status to %q", again.Status)
	}
}

// F1 (delivered and reviewed as part of EP-7). One planted price fault (zone low 0) and one planted
// status fault (WAIT_PULLBACK on a BREAKOUT shape), alone and together. The two single-fault
// controls pin that each fault on its own still gets its own stamp, so the mixed stamp cannot
// be satisfied by a gate that stamps PRICES_AND_STATUS_WITHDRAWN for everything.
func TestTheGateRecordsMixedPriceAndStatusWithdrawal(t *testing.T) {
	priceFault := func(p *entryplan.Plan) { p.IdealEntry.Low = 0 }
	statusFault := func(p *entryplan.Plan) {
		p.EntryTrace.Decision.Semantic = entryplan.EntrySemanticBreakout
		p.EntryTrace.Decision.Rule = entryplan.RuleWaitPullback
	}
	allFields := []entryplan.PlanPriceField{entryplan.FieldIdealEntry, entryplan.FieldMaxChasePrice,
		entryplan.FieldInvalidation, entryplan.FieldTarget1, entryplan.FieldTarget2, entryplan.FieldRiskReward}

	cases := []struct {
		name       string
		faults     []func(*entryplan.Plan)
		wantCheck  entryplan.InvariantCheckOutcome
		wantStatus entryplan.EntryStatus
		wantInv    []entryplan.PlanInvariant
		wantFields []entryplan.PlanPriceField
	}{
		{"price only", []func(*entryplan.Plan){priceFault},
			entryplan.InvariantCheckPricesWithdrawn, entryplan.StatusWaitPullback,
			[]entryplan.PlanInvariant{"ZONE_LOW_POSITIVE", "INVALIDATION_BELOW_ZONE"}, allFields},
		{"status only", []func(*entryplan.Plan){statusFault},
			entryplan.InvariantCheckStatusWithdrawn, entryplan.StatusInsufficientData,
			[]entryplan.PlanInvariant{"WAIT_MATCHES_SEMANTIC"}, nil},
		{"mixed", []func(*entryplan.Plan){priceFault, statusFault},
			entryplan.InvariantCheckPricesAndStatusWithdrawn, entryplan.StatusInsufficientData,
			[]entryplan.PlanInvariant{"ZONE_LOW_POSITIVE", "INVALIDATION_BELOW_ZONE", "WAIT_MATCHES_SEMANTIC"},
			allFields},
	}
	ran := 0
	for _, c := range cases {
		broken := fullyPricedPlan()
		for _, f := range c.faults {
			f(&broken)
		}
		got, _ := entryplan.EnforcePlanInvariants(broken)
		ran++
		if got.EntryTrace == nil {
			t.Fatalf("%s: gate dropped the trace", c.name)
		}
		if got.EntryTrace.InvariantCheck != c.wantCheck {
			t.Errorf("%s: InvariantCheck = %q, want %q", c.name, got.EntryTrace.InvariantCheck, c.wantCheck)
		}
		if got.Status != c.wantStatus {
			t.Errorf("%s: status = %q, want %q", c.name, got.Status, c.wantStatus)
		}
		if !reflect.DeepEqual(got.EntryTrace.InvariantViolations, c.wantInv) {
			t.Errorf("%s: violations = %v, want %v", c.name, got.EntryTrace.InvariantViolations, c.wantInv)
		}
		if !reflect.DeepEqual(got.EntryTrace.WithdrawnFields, c.wantFields) {
			t.Errorf("%s: withdrawn = %v, want %v", c.name, got.EntryTrace.WithdrawnFields, c.wantFields)
		}
	}
	if ran != 3 {
		t.Fatalf("ran %d of 3 cases", ran)
	}
}
