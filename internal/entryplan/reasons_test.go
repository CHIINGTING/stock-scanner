package entryplan_test

import (
	"sort"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// emittedReasons is the set of codes this package's DECISION-BEARING rules actually produce,
// across every input each of them can be handed. It is the ground truth both halves of the
// registry test are compared against.
//
// TWO SWEEPS, and the union is the point:
//
//	ComputePlan   over allSnapshots() — the 280k-case evidence matrix, the named EP-3b price
//	              cases and EP-4's risk cases
//	DecideStatus  over decisionCases() — EP-5's decision matrix
//
// The union has mattered at every step since EP-3b, always for the same reason. matrix() carries
// no MA20, no pivot and no previous close, so on its own it cannot reach a single one of the
// twenty-odd codes the zone and chase rules can produce — a registry test run over it alone
// would have reported nineteen dead codes, and the obvious "fix" would have been to delete the
// assertion.
//
// EP-5 adds the second sweep because DecideStatus is a SEPARATELY EXPORTED, DOCUMENTED-TOTAL
// rule whose argument is a PROJECTION. Four of its arms read a combination of fields that
// today's upstream steps happen not to produce together — a stop absence classified as an
// evidence gap, and a policy row pairing a permitted SHAPE with a refused or undecided
// EXECUTION. Those arms are not dead code: they are this package's half of the contract with
// EP-2 and with the stop step, and the matrix drives them through the exported entry point with
// inputs that satisfy that function's own stated contract ("defined for every input including
// the zero DecisionInput").
//
// Where a code can be reached ONLY by BREAKING a documented precondition — handing an exported
// step an argument its own doc says it will not be handed — the package does NOT stretch this
// sweep to cover it. That is what ReservedReasons is for, and EP-4 set the precedent with
// RISK_NOT_ESTABLISHED. The line between the two is stated there.
func emittedReasons(t *testing.T) map[entryplan.Reason]string {
	t.Helper()
	seen := map[entryplan.Reason]string{}
	note := func(r entryplan.Reason, where string) {
		if _, ok := seen[r]; !ok {
			seen[r] = where
		}
	}
	for _, c := range allSnapshots() {
		for _, r := range entryplan.ComputePlan(c.in).Reasons {
			note(r, "ComputePlan/"+c.name)
		}
	}
	for _, c := range decisionCases() {
		for _, r := range entryplan.DecideStatus(c.in).Reasons {
			note(r, "DecideStatus/"+c.name)
		}
	}
	return seen
}

// Both sweeps must actually contribute, or the union above is a union of one.
//
// Without this, deleting either half leaves the registry test green for as long as the other
// half happens to cover everything — which is exactly the failure mode the union exists to
// prevent, one level up.
func TestBothReasonSweepsContribute(t *testing.T) {
	fromPlan := map[entryplan.Reason]bool{}
	for _, c := range allSnapshots() {
		for _, r := range entryplan.ComputePlan(c.in).Reasons {
			fromPlan[r] = true
		}
	}
	fromDecision := map[entryplan.Reason]bool{}
	for _, c := range decisionCases() {
		for _, r := range entryplan.DecideStatus(c.in).Reasons {
			fromDecision[r] = true
		}
	}
	var onlyPlan, onlyDecision int
	for r := range fromPlan {
		if !fromDecision[r] {
			onlyPlan++
		}
	}
	for r := range fromDecision {
		if !fromPlan[r] {
			onlyDecision++
		}
	}
	if onlyPlan == 0 {
		t.Error("every code the snapshot sweep produces is also produced by the decision " +
			"matrix — the first sweep is contributing nothing and could be deleted")
	}
	if onlyDecision == 0 {
		t.Error("the decision matrix produces no code the snapshot sweep does not — it is " +
			"contributing nothing to the registry census, which means the four contract arms " +
			"it exists to reach are not being reached")
	}
}

// The registry is checked in BOTH directions, and the second direction is the one that is
// usually skipped.
//
//	forward   every reason production emits is in AllReasons
//	backward  every member of AllReasons is emitted by production
//
// FU-11's D8 is the reason the backward half exists. It shipped a wording guard that covered
// 11 of 14 codes while its own comment claimed it covered all of them, and two of the three
// gaps were live on real stocks — a one-directional check reports green while the registry and
// the code drift apart, in either direction. A dead code is not harmless: a consumer writes a
// branch for it, and the branch never runs.
//
// The comparison is against an EXHAUSTIVE sweep rather than a sample, for the same reason.
func TestReasonRegistryIsExactlyWhatProductionEmits(t *testing.T) {
	emitted := emittedReasons(t)

	// forward
	for r, where := range emitted {
		if !entryplan.KnownReason(r) {
			t.Errorf("production emits %q (first at %s) and it is not in AllReasons — a "+
				"stored plan would carry a code no consumer can enumerate", r, where)
		}
	}

	// backward, with the reserved codes exempted BY NAME and nothing else.
	//
	// FIVE codes are unreachable from the sweeps by design, and every one of them is listed in
	// ReservedReasons with a producer and an argument. Everything else must be produced by
	// some input, or it is a branch a consumer writes and never reaches.
	for _, r := range entryplan.AllReasons {
		if _, ok := emitted[r]; ok {
			if entryplan.ReservedReasons[r] {
				t.Errorf("%q is marked RESERVED and an input produces it (%s) — the "+
					"reservation is now a lie, and for PLAN_INVARIANT_VIOLATED it would mean "+
					"this package computed an illegal price", r, emitted[r])
			}
			continue
		}
		if entryplan.ReservedReasons[r] {
			continue
		}
		t.Errorf("AllReasons lists %q and no input produces it — a dead reason is a "+
			"branch a consumer will write and never reach", r)
	}

	if len(emitted)+len(entryplan.ReservedReasons) != len(entryplan.AllReasons) {
		var got, want []string
		for r := range emitted {
			got = append(got, string(r))
		}
		for _, r := range entryplan.AllReasons {
			want = append(want, string(r))
		}
		sort.Strings(got)
		sort.Strings(want)
		t.Errorf("emitted %v (%d) plus %d reserved, registry %v (%d)", got, len(emitted),
			len(entryplan.ReservedReasons), want, len(entryplan.AllReasons))
	}
}

// The reservation itself must stay SMALL and JUSTIFIED, and every reserved code must be a real
// registry member.
//
// The precedent and the limit are policy.go's ReservedPolicyNames, which is EMPTY and says it
// "must stay empty unless a work item can say what the reservation is for: an unreachable
// policy is indistinguishable from a mapping someone forgot to write". EP-3b claimed two and
// EP-4 claims a third; the tests that hold them are named at ReservedReasons:
// TestTheReservedChaseAlignmentCodeIsUnreachable,
// TestTheRiskDenominatorIsRefusedRatherThanInvented and
// TestTheWithdrawalReasonIsReservedAndProduced.
//
// EP-4's addition is RISK_NOT_ESTABLISHED, and the reason it is a reservation rather than a
// dead code is that ComputeTargets is EXPORTED and takes the zone and the invalidation as
// ARGUMENTS. That is what makes the step separately testable, and it is also what lets a caller
// hand it an inconsistent pair — so the two guards are written, and the test drives them
// directly with exactly such a pair. Reaching either from ComputePlan would mean
// ComputeInvalidation published a level that is not strictly below zone.Low, or published it on
// another price series.
//
// EP-5 adds two, both in the invalidation step, and NEITHER of them is EP-5 hiding an arm it
// could not reach. EP-5 is the item that SPLIT one reason code into three, on spec §17's
// instruction, because a decision layer reading a single NO_VALID_INVALIDATION had to call every
// missing stop either a state or a verdict and both are wrong for half the inputs. A split
// makes distinctions the coupled pipeline does not currently exercise: with a zone published,
// zoneHalfWidth has already demanded a usable ATR(14) on the zone's own basis, which is exactly
// what makes the volatility fallback row comparable — so ComputePlan always reaches the geometry
// screen and always gets a verdict. The alternative to reserving them is leaving the three
// merged, which is the thing the split exists to prevent.
func TestTheReservedReasonSetIsExactlyTheFiveClaimedOnes(t *testing.T) {
	want := map[entryplan.Reason]bool{
		entryplan.ReasonChaseTickAlignmentUnavailable: true,
		entryplan.ReasonRiskNotEstablished:            true,
		entryplan.ReasonPlanInvariantViolated:         true,
		// EP-5's two, both in the INVALIDATION step and both for a stated reason.
		//
		// INVALIDATION_EVIDENCE_UNAVAILABLE is RISK_NOT_ESTABLISHED's argument exactly:
		// ComputeInvalidation is exported, takes the ZONE as an argument, and its doc states
		// the precondition ("it must be the zone ComputeIdealEntryZone published") that makes
		// the branch unreachable. Driven directly by
		// TestTheStopEvidenceGapIsRefusedRatherThanCalledAVerdict.
		//
		// INVALIDATION_SELECTION_INCONSISTENT is the STRONGER case, and the one CHASE_TICK_
		// ALIGNMENT_UNAVAILABLE set the precedent for: it fires only if the screen and the
		// selector inside this package disagree, so no argument reaches it and the ledger
		// test asserts that rather than pretending to produce it.
		entryplan.ReasonInvalidationEvidenceUnavailable:   true,
		entryplan.ReasonInvalidationSelectionInconsistent: true,
	}
	for r := range entryplan.ReservedReasons {
		if !want[r] {
			t.Errorf("%q is reserved and this test does not know why — a reservation with no "+
				"stated producer is a dead code with an excuse", r)
		}
		if !entryplan.KnownReason(r) {
			t.Errorf("%q is reserved and is not in AllReasons at all", r)
		}
	}
	for r := range want {
		if !entryplan.ReservedReasons[r] {
			t.Errorf("%q is no longer reserved — if an input now produces it, delete it from "+
				"this list; if not, the registry test will call it dead", r)
		}
	}
	if len(entryplan.ReservedReasons) != len(want) {
		t.Errorf("%d reserved reasons, want %d", len(entryplan.ReservedReasons), len(want))
	}
}

// The registry itself must be well formed: no duplicates, no empty codes, and the stable
// SCREAMING_SNAKE shape the rest of the repo persists.
func TestReasonRegistryIsWellFormed(t *testing.T) {
	seen := map[entryplan.Reason]bool{}
	for _, r := range entryplan.AllReasons {
		if r == "" {
			t.Error("empty reason code in AllReasons")
			continue
		}
		if seen[r] {
			t.Errorf("%q listed twice", r)
		}
		seen[r] = true
		for _, ch := range r {
			if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
				t.Errorf("%q is not a stable SCREAMING_SNAKE code", r)
				break
			}
		}
	}
	if len(entryplan.AllReasons) == 0 {
		t.Fatal("the registry is empty")
	}
	if entryplan.KnownReason("NOT_A_REASON") {
		t.Error("KnownReason accepts a code that is not in the registry")
	}
}

// A plan never repeats a reason, and the order is deterministic — a stored reason list is
// compared byte-for-byte in the archive, so an unstable order would look like a changed
// verdict.
func TestPlanReasonsAreDeduplicatedAndOrdered(t *testing.T) {
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		seen := map[entryplan.Reason]bool{}
		for _, r := range p.Reasons {
			if seen[r] {
				t.Fatalf("%s: reason %q repeated in %v", c.name, r, p.Reasons)
			}
			seen[r] = true
		}
		again := entryplan.ComputePlan(c.in)
		if len(again.Reasons) != len(p.Reasons) {
			t.Fatalf("%s: reason count is not stable", c.name)
		}
		for n := range p.Reasons {
			if again.Reasons[n] != p.Reasons[n] {
				t.Fatalf("%s: reason order is not stable at %d", c.name, n)
			}
		}
	}
}

// THE EP-1 LIMIT IS GONE, AND ITS OWN DOC SAID THIS WOULD HAPPEN.
//
// Until EP-4 every well-formed plan carried an unconditional ENTRY_EVALUATION_NOT_IMPLEMENTED
// — the same discipline as valuation's EARNINGS_MAGNITUDE_NOT_ESTABLISHED, so the limit
// travelled with the conclusion instead of living only in a comment. That reason's doc called
// itself "deliberately TEMPORARY: the work item that implements entry evaluation removes it".
// EP-5 is that work item. DecideStatus answers every well-formed input, so the sentence the
// reason asserted is false for all of them and the constant is gone rather than left to rot.
//
// What survives from that test, because it was never about the limit: a MALFORMED snapshot
// reports SNAPSHOT_MALFORMED and NOTHING ELSE. A caller who was handed something that is not
// a stock must not also be handed a list of market-shaped observations about it, which would
// suggest the two are comparable failures.
func TestAMalformedSnapshotReportsThatAndNothingElse(t *testing.T) {
	var malformed int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		var hasMalformed bool
		for _, r := range p.Reasons {
			if r == entryplan.ReasonSnapshotMalformed {
				hasMalformed = true
			}
		}
		if !hasMalformed {
			continue
		}
		malformed++
		if len(p.Reasons) != 1 {
			t.Fatalf("%s: malformed input produced %v, want SNAPSHOT_MALFORMED alone",
				c.name, p.Reasons)
		}
	}
	// ANTI-VACUITY. If the sweep stops producing malformed cases, the loop above passes by
	// never running and this test becomes a comment.
	if malformed == 0 {
		t.Fatal("no snapshot in the sweep was malformed — this test never executed its assertion")
	}
}
