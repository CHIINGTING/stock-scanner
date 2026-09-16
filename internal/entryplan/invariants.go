package entryplan

import (
	"fmt"
	"math"
	"reflect"
	"sort"
)

// ── the plan invariant checker ─────────────────────────────────────────────────────────
//
// The invariants this package has stated in PROSE since EP-1 — a zone bound is a positive
// price, Low <= High, no non-finite value escapes, a chase limit is not below the zone it
// chases into — become a CHECKER here, and the checker is verified against deliberately
// broken Plans in invariants_test.go.
//
// # Why it ships BEFORE the first `return &PriceZone{...}`
//
// EP-1's defence of plain float64 on PriceZone.Low, PriceZone.High, Invalidation.Price,
// PriceLevel.Price and the three RiskReward fields is "the struct exists only when it has
// real values". Under EP-1 and EP-2 that argument cost nothing: ComputePlan never built one
// of those structs, so the suite could assert the stronger property (every price pointer is
// nil) and the plain floats were never exposed. EP-3b returned the first zone, that test
// retired, and the argument now has to be TRUE — which is why the guard had to exist before
// the first producer rather than after it.
//
// # Why a checker rather than a test
//
// WHEN THIS WAS WRITTEN, a test asserting "no bad zone comes out of ComputePlan" would have
// been VACUOUS, because no zone came out of ComputePlan at all. So the invariants were written
// as a function, and what the tests exercise is the FUNCTION, on hand-built bad Plans — the
// subject under test is the checker, which exists, rather than a production path, which did
// not. The package already had two guards built this way and both are live: the purity sweep's
// planted `os.WriteFile` line (architecture_test.go) and the walkFloats fixture that must find
// exactly 7 floats and 5 non-finite ones (plan_test.go).
//
// SINCE EP-3b BOTH DIRECTIONS ARE LIVE. ComputePlan does produce zones and chase limits — and
// from EP-4 it produces stops, targets and risk/rewards too — so
// TestEveryPlanComputePlanReturnsIsInvariantClean is no longer vacuous on any price invariant,
// and it carries a ledger that counts each kind of price it saw so it cannot go back to being
// vacuous quietly.
//
// # What it checks, and what it still does not
//
// EP-3a implemented SEVEN invariants and deliberately left four to EP-4, on the rule that a
// checker written by an item that has never produced a stop level is a checker written against
// an imagined producer. EP-4 produces them, so the registry is now ELEVEN: the four additions
// are INVALIDATION_BELOW_ZONE, TARGET1_ABOVE_ZONE, TARGET2_NOT_BELOW_TARGET1 and
// RR_FINITE_POSITIVE, and each is declared next to the argument for its boundary case.
//
// What is STILL not checked, on purpose: that RiskReward.Ratio equals RewardPerShare /
// RiskPerShare. That is an ARITHMETIC CONSISTENCY claim, not a geometry one, and re-dividing
// two numbers to check that somebody divided them is a test of the producer rather than an
// invariant of the value — the terms are all on TargetTrace, so a reader can do the division
// themselves. If a later item wants it, it is a new code with its own name.
//
// The finiteness sweep remains the deliberate EXCEPTION to the "geometry only" line. It is not
// a statement about what a stop or a target MEANS — it is a statement about what a float64 may
// contain before it is encoded, and it therefore applies to every float reachable from a Plan
// whether or not this work item understands the field. A NaN in a future field is caught here
// with no rule about that field existing at all.
//
// # Wired into ComputePlan BY EP-3b, not by EP-3a
//
// EP-3a deliberately called this from nothing, for the reason doc.go gives for the regime →
// policy table: the item that writes a guard must not also be the item that decides what
// happens when it fires. Whether the caller DROPS the offending prices, downgrades the plan to
// INSUFFICIENT_DATA or refuses to return at all is a behaviour decision, and it belonged with
// the code that can actually produce a violation.
//
// EP-3b made it, in enforce.go: EnforcePlanInvariants withdraws the offending prices, keeps the
// plan, its evidence and its trace, and records the outcome — and ComputePlan's last step is
// that call. The other two candidates are argued and rejected there. EP-4 narrowed WHICH prices
// are withdrawn from "all of them" to "the violated field and everything computed from it";
// see withdrawalFor.
//
// RuleVersion is NOT bumped by EP-3a: nothing here changes a rule, a reason code, a policy
// mapping or a status meaning, and no archived plan means anything different because this
// file exists.

// PlanInvariant is a stable code for ONE invariant a Plan must satisfy.
//
// SCREAMING_SNAKE and stable for the reason Reason and PolicyName are: a violation that gets
// logged, archived or handed to an agent must stay greppable across a wording change, and the
// English sentence in PlanViolation.Detail is presentation.
type PlanInvariant string

const (
	// InvariantZoneLowPositive — PriceZone.Low > 0.
	//
	// Not "non-negative". TWSE quotes are positive, so a Low of 0 is not a cautious default:
	// it is a real price at which nothing has ever traded, and it makes the zone's lower half
	// unfalsifiable — every price is "above the low".
	InvariantZoneLowPositive PlanInvariant = "ZONE_LOW_POSITIVE"

	// InvariantZoneHighPositive — PriceZone.High > 0. Same argument as the low bound.
	InvariantZoneHighPositive PlanInvariant = "ZONE_HIGH_POSITIVE"

	// InvariantZoneOrdered — PriceZone.Low <= PriceZone.High.
	//
	// An inverted zone contains no price at all, so every "is the price inside the zone"
	// question answers NO and BUY_NOW becomes unreachable — a wrong answer that looks exactly
	// like a cautious one. types.go states "Low <= High" as a comment; this is the check.
	InvariantZoneOrdered PlanInvariant = "ZONE_LOW_NOT_ABOVE_HIGH"

	// InvariantFloatNotNaN — no float reachable from a Plan is NaN.
	//
	// Kept separate from the Inf code because the two fail differently. NaN makes every
	// comparison FALSE, so a NaN price silently disables the guard that was supposed to trip
	// on it; ±Inf compares as expected and then cannot be encoded at all.
	InvariantFloatNotNaN PlanInvariant = "FLOAT_NOT_NAN"

	// InvariantFloatNotInf — no float reachable from a Plan is ±Inf. encoding/json refuses
	// it, so a single one turns a whole report into an error rather than a wrong number.
	InvariantFloatNotInf PlanInvariant = "FLOAT_NOT_INF"

	// InvariantMaxChasePositive — MaxChasePrice > 0 when it is present at all.
	//
	// doc.go's MISSING ≠ ZERO clause names this exact number: "a zero-valued MaxChasePrice
	// would read as 'never chase', which is a decision this layer did not make". Absence is
	// spelled nil, and the type already supports it.
	InvariantMaxChasePositive PlanInvariant = "MAX_CHASE_POSITIVE"

	// InvariantMaxChaseCoversZone — MaxChasePrice >= IdealEntry.High.
	//
	// A plan whose chase limit sits below the top of its own ideal entry zone forbids paying
	// what it simultaneously calls ideal: the upper part of the zone is unbuyable and the two
	// numbers contradict each other. EQUALITY IS LEGAL and is the tightest defensible plan —
	// "buy in the zone, pay not one tick above it".
	InvariantMaxChaseCoversZone PlanInvariant = "MAX_CHASE_NOT_BELOW_ZONE_HIGH"

	// ── EP-4: the four EP-3a deliberately left to the item that produces a stop ───────
	//
	// EP-3a's file comment said these are "EP-4's, they are stated in prose on the types
	// today, and a checker written by the item that has not yet produced a single stop level
	// would be a checker written against an imagined producer". EP-4 is that item, so the
	// prose becomes checks and TestTheStopAndTargetInvariantsAreDeliberatelyNotImplementedYet
	// is replaced by its opposite.

	// InvariantInvalidationBelowZone — Invalidation.Price < IdealEntry.Low.
	//
	// STRICTLY below. An invalidation at or above the bottom of the entry band would have the
	// plan buying and abandoning in the same tick of price — two contradictory instructions,
	// not a cautious one — and it makes `risk = zone.High − invalidation` smaller than the
	// zone's own width, so the published R:R would be measured against a distance the trade
	// cannot even be filled inside.
	//
	// Equality is NOT legal here, and that is the opposite call from MAX_CHASE_NOT_BELOW_ZONE
	// HIGH, deliberately: a ceiling equal to the top of the zone still permits every price in
	// the zone, while a stop equal to the bottom of the zone forbids the price it is standing
	// on.
	InvariantInvalidationBelowZone PlanInvariant = "INVALIDATION_BELOW_ZONE"

	// InvariantTarget1AboveZone — Target1.Price > IdealEntry.High.
	//
	// A first target at or below the worst acceptable entry is not a target: the reward is
	// zero or negative at the entry the plan tells a reader to assume, so the R:R computed
	// from it is zero or negative — and a negative R:R rendered as "0.4R" is the shape in
	// which a losing plan looks like a small win.
	InvariantTarget1AboveZone PlanInvariant = "TARGET1_ABOVE_ZONE"

	// InvariantTarget2NotBelowTarget1 — Target2.Price >= Target1.Price when both exist.
	//
	// EQUALITY IS LEGAL: a valuation ceiling landing exactly on the first target is a real
	// answer ("the model's base case is the first level in the way"), and refusing it would
	// force the producer to invent a gap it has no basis for. What is illegal is the INVERTED
	// pair, because "target 1" and "target 2" are read as an ordered plan — scale out at the
	// first, hold for the second — and an inverted pair silently reverses the instruction.
	InvariantTarget2NotBelowTarget1 PlanInvariant = "TARGET2_NOT_BELOW_TARGET1"

	// InvariantRiskRewardFinitePositive — every RiskReward field is finite and strictly
	// positive when the struct exists at all.
	//
	// # This one folds the finiteness in, and the zone checks deliberately do not
	//
	// A NaN zone bound trips FLOAT_NOT_NAN alone, because `NaN <= 0` is false and the
	// positivity check does not fire — that asymmetry is written down at InvariantFloatNotNaN
	// and it is why NaN has an invariant of its own. Here the invariant is NAMED for the
	// conjunction ("finite positive"), so a NaN ratio trips BOTH this and FLOAT_NOT_NAN, and
	// both sentences are true: the bits cannot be encoded AND the ratio is not a positive
	// number. That is the same double report a -Inf zone bound already produces.
	//
	// types.go promises this in prose — "constructed ONLY when entry, invalidation and target
	// are all established and RiskPerShare is strictly positive; otherwise the whole struct is
	// nil" — and until EP-4 nothing made it true. A Ratio of 0 is the specific failure it
	// guards: it reads as "this trade offers no reward", which is a CONCLUSION, where the
	// honest encoding of "we could not compute it" is a nil struct.
	InvariantRiskRewardFinitePositive PlanInvariant = "RR_FINITE_POSITIVE"

	// ── EP-5: the STATUS invariants ───────────────────────────────────────────────────
	//
	// Seven, and every one of them is a NECESSARY CONDITION rather than a re-derivation. The
	// checker deliberately does NOT re-run the decision table: a second implementation of
	// DecideStatus in this file would agree with the first by construction on the day it was
	// written and would be the thing that silently drifts afterwards, and a guard that is a
	// copy of what it guards catches nothing.
	//
	// What they DO catch is the class of failure the decision layer can actually produce: a
	// verdict that contradicts the numbers published beside it. BUY_NOW with no zone, a
	// TOO_EXTENDED with no ceiling to be extended past, a WAIT_PULLBACK on a breakout — each
	// is a sentence a reader would act on and none of them can be true.
	//
	// THEY READ EntryTrace.Decision, not the Snapshot. The current price is not a field of
	// Plan (a plan is a statement about levels, not a quote), so the comparison uses the price
	// the decision recorded — which is also the only price that could have produced the
	// verdict. A plan with no decision trace is not checked against the price-bearing three,
	// and that is stated at each one rather than left as a silent skip.

	// InvariantStatusCanonical — Plan.Status is one of the six declared statuses.
	//
	// A string that no rule can produce is not a cautious value, it is a field a consumer will
	// switch on and fall through. Mirrors model.Regime.Valid one layer up.
	InvariantStatusCanonical PlanInvariant = "STATUS_CANONICAL"

	// InvariantStatusHasReason — a Plan with a status carries at least one Reason.
	//
	// A verdict with no defence is unrecoverable from the archived row: the arm that produced
	// it, and therefore what it MEANT, cannot be reconstructed. Every EP-5 arm publishes at
	// least one code specifically so this can be required.
	InvariantStatusHasReason PlanInvariant = "STATUS_HAS_REASON"

	// InvariantBuyNowHasZone — BUY_NOW implies IdealEntry != nil.
	//
	// "Buy at the market now" with no band published is an instruction with no price attached,
	// and a reader given it has to invent one — which is the whole failure this package exists
	// to prevent.
	InvariantBuyNowHasZone PlanInvariant = "BUY_NOW_HAS_ZONE"

	// InvariantBuyNowPriceAuthorised — BUY_NOW implies the recorded price is INSIDE the zone
	// (bounds inclusive) or at-or-below a chase ceiling that exists.
	//
	// The DISJUNCTION is the contract, and it is written here rather than hidden in the
	// decision: EP-5 has two BUY_NOW arms, one for a market inside the band and one for a
	// breakout that ran and is still inside its ceiling. What the invariant forbids is the
	// third possibility — a BUY_NOW at a price that is neither in the band nor covered by a
	// ceiling — which is a plan telling a reader to pay a price it never authorised.
	InvariantBuyNowPriceAuthorised PlanInvariant = "BUY_NOW_PRICE_AUTHORISED"

	// InvariantTooExtendedAboveCeiling — TOO_EXTENDED implies MaxChasePrice != nil and the
	// recorded price is STRICTLY above it.
	//
	// The status is the execution-side reading of "too far to chase", and the ONLY number that
	// makes that statement true is the plan's own ceiling. Equality is not extension: the
	// highest price the plan permits paying is a price the plan permits paying.
	InvariantTooExtendedAboveCeiling PlanInvariant = "TOO_EXTENDED_ABOVE_CEILING"

	// InvariantTooExtendedKeepsZone — TOO_EXTENDED implies IdealEntry != nil.
	//
	// SEPARATE from the ceiling check on purpose. "Too expensive here" is only actionable
	// beside "and this is where it would not be", so a TOO_EXTENDED plan that lost its zone has
	// been reduced to the useless half of its own sentence. types.go promised this from EP-1;
	// this is where it is enforced.
	InvariantTooExtendedKeepsZone PlanInvariant = "TOO_EXTENDED_KEEPS_ZONE"

	// InvariantWaitMatchesSemantic — WAIT_PULLBACK implies the decision's semantic is PULLBACK,
	// and WAIT_BREAKOUT implies BREAKOUT.
	//
	// ONE code for both directions because it is one claim: a waiting status is the execution
	// reading of an entry SHAPE the scanner chose, so publishing WAIT_PULLBACK for a breakout
	// would be this package overriding that choice — the second opinion doc.go forbids —
	// while telling a reader to wait for a retracement that no thesis predicts.
	InvariantWaitMatchesSemantic PlanInvariant = "WAIT_MATCHES_SEMANTIC"

	// ── EP-6D ─────────────────────────────────────────────────────────────────────────

	// InvariantBuyNowThesisNotContradicted — BUY_NOW implies the decision trace records the
	// thesis standing NOT_CONTRADICTED.
	//
	// The plan-level form of the one protection the user asked for: a stock whose primary
	// Action is SELL, REDUCE, TAKE PROFIT or STOP LOSS is never BUY_NOW. It is written as "must be NOT_CONTRADICTED"
	// rather than "must not be CONTRADICTED" because an UNCLASSIFIED standing is not
	// permission either (MISSING ≠ ZERO), and a necessary condition that let "" through would
	// be weaker than the decision it guards.
	//
	// Like the price-bearing EP-5 checks, it needs the decision trace: a Plan with no
	// EntryTrace records no standing and is not checked, which is stated here rather than left
	// to be inferred from a skip.
	InvariantBuyNowThesisNotContradicted PlanInvariant = "BUY_NOW_THESIS_NOT_CONTRADICTED"

	// InvariantThesisContradictedPublishesNoEntry — a decision trace recording the thesis
	// standing CONTRADICTED implies the plan carries NO executable price (IdealEntry,
	// MaxChasePrice, Invalidation, Target1, Target2, RiskReward all nil) and its status is not
	// one that tells a reader to enter or wait (BUY_NOW, WAIT_PULLBACK, WAIT_BREAKOUT,
	// TOO_EXTENDED); and a trace recording the arm S1 additionally implies the four STEP traces
	// (EntryTrace.Zone, .Chase, .Stop, .Targets) are empty — the user's rule is that no
	// entry-derived number appears anywhere in such a plan, diagnostics included.
	//
	// The plan-level form of the user's widened rule: a stock the scanner wants out of shows no
	// entry. INSUFFICIENT_DATA is allowed alongside it, because a CONTRADICTED standing on a plan
	// with no resolved entry shape is answered by S0, not S1 — there is no thesis to contradict
	// and no price to withhold.
	//
	// BOTH a price and a status invariant, so the gate withdraws every executable field AND
	// sends the verdict back to INSUFFICIENT_DATA (see withdrawalFor and statusInvariants), and
	// on an S1 plan it also empties the four step traces (see EnforcePlanInvariants).
	// Needs the decision trace; a Plan without one is not checked.
	InvariantThesisContradictedPublishesNoEntry PlanInvariant = "THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY"
)

// AllPlanInvariants is every invariant the checker enforces, in the order it reports them.
//
// Declared rather than derived, and kept honest from the side that CAN be kept honest today:
// the forward half of the Reason registry test ("everything production emits is a member") has
// no analogue here, because production emits no violation at all and must not. What replaces
// it is the anti-vacuity LEDGER in invariants_test.go — every member of this list must be
// tripped by at least one planted Plan, or the test fails and names the member. A code in this
// list that no fixture can trip is a check nobody has ever seen run.
var AllPlanInvariants = []PlanInvariant{
	InvariantFloatNotNaN,
	InvariantFloatNotInf,
	InvariantZoneLowPositive,
	InvariantZoneHighPositive,
	InvariantZoneOrdered,
	InvariantMaxChasePositive,
	InvariantMaxChaseCoversZone,
	InvariantInvalidationBelowZone,
	InvariantTarget1AboveZone,
	InvariantTarget2NotBelowTarget1,
	InvariantRiskRewardFinitePositive,
	InvariantStatusCanonical,
	InvariantStatusHasReason,
	InvariantBuyNowHasZone,
	InvariantBuyNowPriceAuthorised,
	InvariantTooExtendedAboveCeiling,
	InvariantTooExtendedKeepsZone,
	InvariantWaitMatchesSemantic,
	InvariantBuyNowThesisNotContradicted,
	InvariantThesisContradictedPublishesNoEntry,
}

// KnownPlanInvariant reports whether a code is in the registry. Mirrors KnownReason and
// KnownPolicyName so a code decoded from a log or an archive can be validated.
func KnownPlanInvariant(i PlanInvariant) bool {
	for _, k := range AllPlanInvariants {
		if k == i {
			return true
		}
	}
	return false
}

// PlanViolation is one broken invariant, at one place in one Plan.
//
// Three fields rather than a formatted string, so a consumer can COUNT by invariant without
// parsing English — which is what the checker's own anti-vacuity ledger does. Detail still
// carries the whole sentence, because this repo's rule is that a reader must not have to open
// the source to understand a failure.
type PlanViolation struct {
	// Invariant is which rule was broken. Always a member of AllPlanInvariants.
	Invariant PlanInvariant `json:"invariant"`
	// Path is where in the Plan, in the walker's notation: a field is ".Name", a pointer
	// dereference is "*", a slice element is "[n]". "Plan.IdealEntry*.Low" is the Low field
	// of the zone IdealEntry points at.
	Path string `json:"path"`
	// Detail is the whole human sentence: the value seen, and what the invariant is for.
	Detail string `json:"detail"`
}

// String is the one-line form used in test failures and logs.
func (v PlanViolation) String() string {
	return fmt.Sprintf("entryplan: %s: %s [%s]", v.Path, v.Detail, v.Invariant)
}

// PlanFloat is one float64 the checker found, and where.
type PlanFloat struct {
	Path  string  `json:"path"`
	Value float64 `json:"value"`
}

// PlanZone is one PriceZone the checker found, and where.
type PlanZone struct {
	Path string    `json:"path"`
	Zone PriceZone `json:"zone"`
}

// PlanInvariantAudit is the checker's WORK RECORD, not just its verdict.
//
// It exists because "the checker found no violation" and "the checker never looked" are the
// same output otherwise — the failure mode this whole package is written against, one level
// up. FloatsSeen and ZonesSeen let a test assert the checker VISITED the expected number of
// fields, so a Plan field added later that the reflective walk cannot reach makes a test go
// red instead of quietly widening the blind spot.
type PlanInvariantAudit struct {
	// FloatsSeen is every float64 reachable from the Plan, in walk order, including the ones
	// that are perfectly fine. A shrinking count is the signal.
	FloatsSeen []PlanFloat `json:"floats_seen"`
	// ZonesSeen is every PriceZone reachable from the Plan, in walk order. Collected by TYPE
	// rather than by field name, so a second zone-valued field added later is checked without
	// this file being edited.
	ZonesSeen []PlanZone `json:"zones_seen"`
	// Violations is the verdict. Deterministic order, grouped: the finiteness sweep in walk
	// order first, then the geometry of each zone in walk order, then the chase limit.
	Violations []PlanViolation `json:"violations,omitempty"`
}

// PlanInvariantViolations reports every invariant a Plan breaks, or nil for a clean one.
//
// A SLICE and not an error: every violation is reported, not just the first. A Plan whose
// zone is inverted AND whose chase limit is below it is broken in two independent ways, and
// an error-returning check would hand back one of them and leave the second to be discovered
// on the next run — the same reason RegimeSession.Validate's single-error shape is right for
// a decode gate and wrong for a checker.
//
// Nil for a clean Plan, so `len(...) == 0` and `... == nil` agree.
func PlanInvariantViolations(p Plan) []PlanViolation {
	return AuditPlanInvariants(p).Violations
}

// AuditPlanInvariants is PlanInvariantViolations plus the record of what was inspected.
//
// Deterministic and total: same Plan in, same audit out, no panic on any shape — a checker
// that can panic on a malformed Plan cannot be used to guard against malformed Plans.
func AuditPlanInvariants(p Plan) PlanInvariantAudit {
	var a PlanInvariantAudit
	walkPlanValue("Plan", reflect.ValueOf(p), &a)

	// 1. FINITENESS, over every float the walk reached — including fields whose MEANING this
	// work item does not implement. See the file comment: this is a claim about the bits,
	// not about the stop level.
	for _, f := range a.FloatsSeen {
		switch {
		case math.IsNaN(f.Value):
			a.add(InvariantFloatNotNaN, f.Path, "value is NaN — NaN makes every comparison "+
				"false, so a NaN price does not trip the guard that was meant to catch it, it "+
				"DISABLES it; and encoding/json cannot represent it at all. An absent price is "+
				"spelled nil")
		case math.IsInf(f.Value, 0):
			a.add(InvariantFloatNotInf, f.Path, fmt.Sprintf("value is %v — an infinite price "+
				"cannot be encoded as JSON, so one of them turns a whole report into an error "+
				"rather than into a wrong number. An absent price is spelled nil", f.Value))
		}
	}

	// 2. ZONE GEOMETRY, per zone found by the walk. Reflective collection rather than
	// `p.IdealEntry` on purpose: the enumerated version would check the zone fields that
	// existed when this was written, which is the failure mode the finiteness sweep is
	// reflective to avoid.
	//
	// A non-finite bound is NOT skipped here. -Inf really is <= 0, so it trips both its
	// finiteness invariant and this one, and both sentences are true.
	for _, z := range a.ZonesSeen {
		if z.Zone.Low <= 0 {
			a.add(InvariantZoneLowPositive, z.Path+".Low", fmt.Sprintf("zone low = %v — a "+
				"zone bound must be a strictly positive price. The exchange quotes positive "+
				"prices, so 0 is not a cautious default: it is a level nothing ever trades at, "+
				"and it makes the lower bound unfalsifiable", z.Zone.Low))
		}
		if z.Zone.High <= 0 {
			a.add(InvariantZoneHighPositive, z.Path+".High", fmt.Sprintf("zone high = %v — a "+
				"zone bound must be a strictly positive price, for the reason the low bound "+
				"must: 0 is a price, not an absence, and absence is spelled by a nil zone",
				z.Zone.High))
		}
		if z.Zone.Low > z.Zone.High {
			a.add(InvariantZoneOrdered, z.Path, fmt.Sprintf("zone low = %v is ABOVE high = %v "+
				"— an inverted zone contains no price, so every \"is the price inside the "+
				"zone\" question answers no and BUY_NOW becomes unreachable. types.go states "+
				"Low <= High; this is where it is enforced", z.Zone.Low, z.Zone.High))
		}
	}

	// 3. THE CHASE LIMIT against the zone it chases into. A cross-field relation between two
	// NAMED fields, so it is written as one — reflection could find the pair only by
	// guessing which zone a chase limit belongs to.
	if p.MaxChasePrice != nil {
		chase := *p.MaxChasePrice
		if chase <= 0 {
			a.add(InvariantMaxChasePositive, "Plan.MaxChasePrice*", fmt.Sprintf(
				"max chase price = %v — doc.go's MISSING ≠ ZERO clause names this number "+
					"specifically: a zero or negative chase limit reads as \"never chase\", "+
					"which is a decision this layer did not make. Absence is spelled nil",
				chase))
		}
		if p.IdealEntry != nil && chase < p.IdealEntry.High {
			a.add(InvariantMaxChaseCoversZone, "Plan.MaxChasePrice*", fmt.Sprintf(
				"max chase price = %v is BELOW Plan.IdealEntry*.High = %v — the plan forbids "+
					"paying what it simultaneously calls an ideal entry, so the top of its own "+
					"zone is unbuyable. Equality is legal and is the tightest honest plan: buy "+
					"in the zone, pay nothing above it", chase, p.IdealEntry.High))
		}
	}

	// 4. THE RISK SIDE, against the zone it is measured from. EP-4's four, and each is a
	// cross-field relation between NAMED fields for the reason the chase check is: reflection
	// could find the pair only by guessing which zone a stop belongs to.
	//
	// EVERY ONE IS GUARDED ON ITS DEPENDENCY BEING PRESENT, and that is not defensive coding
	// — it is the missing-semantics contract. A plan with a legal zone and no invalidation is
	// a NORMAL plan (no candidate qualified), and a checker that reported it as broken would
	// make "there is no stop today" indistinguishable from "this package computed an illegal
	// stop".
	if p.Invalidation != nil && p.IdealEntry != nil && p.Invalidation.Price >= p.IdealEntry.Low {
		a.add(InvariantInvalidationBelowZone, "Plan.Invalidation*.Price", fmt.Sprintf(
			"invalidation = %v is NOT BELOW Plan.IdealEntry*.Low = %v — the plan would buy "+
				"and abandon the idea in the same tick of price, and the risk it publishes "+
				"would be smaller than the width of the band the fill happens in. Unlike the "+
				"chase ceiling, equality is NOT legal: a ceiling at the top of the zone still "+
				"permits every price in it, a stop at the bottom forbids the one it stands on",
			p.Invalidation.Price, p.IdealEntry.Low))
	}
	if p.Target1 != nil && p.IdealEntry != nil && p.Target1.Price <= p.IdealEntry.High {
		a.add(InvariantTarget1AboveZone, "Plan.Target1*.Price", fmt.Sprintf(
			"target 1 = %v is NOT ABOVE Plan.IdealEntry*.High = %v — zone.High is the worst "+
				"fill this plan calls acceptable, so the reward at the entry a reader must "+
				"assume is zero or negative, and the risk/reward computed from it is a loss "+
				"rendered as a ratio", p.Target1.Price, p.IdealEntry.High))
	}
	if p.Target1 != nil && p.Target2 != nil && p.Target2.Price < p.Target1.Price {
		a.add(InvariantTarget2NotBelowTarget1, "Plan.Target2*.Price", fmt.Sprintf(
			"target 2 = %v is BELOW Plan.Target1*.Price = %v — the two are read as an ordered "+
				"plan (scale out at the first, hold for the second), so an inverted pair "+
				"reverses the instruction in silence. Equality is legal: a valuation ceiling "+
				"landing on the first target is a real answer",
			p.Target2.Price, p.Target1.Price))
	}
	if rr := p.RiskReward; rr != nil {
		for _, part := range []struct {
			name  string
			value float64
			why   string
		}{
			{"RiskPerShare", rr.RiskPerShare, "the risk denominator. A zero or negative risk " +
				"makes the ratio meaningless or sign-flipped, and types.go's defence of a " +
				"plain float64 here is that the struct exists ONLY when the risk is strictly " +
				"positive"},
			{"RewardPerShare", rr.RewardPerShare, "the reward numerator, measured to the " +
				"target named in RiskReward.Target"},
			{"Ratio", rr.Ratio, "the published risk/reward. 0 reads as \"this trade offers no " +
				"reward\", which is a CONCLUSION; \"we could not compute it\" is spelled by a " +
				"nil RiskReward"},
		} {
			if !finitePositive(part.value) {
				a.add(InvariantRiskRewardFinitePositive, "Plan.RiskReward*."+part.name,
					fmt.Sprintf("%s = %v is not a finite positive number — %s", part.name,
						part.value, part.why))
			}
		}
	}

	// 5. THE STATUS, against the numbers published beside it. EP-5's seven, and every one is a
	// NECESSARY condition — see the registry block on why this is not a second copy of
	// DecideStatus.
	auditStatus(p, &a)
	return a
}

// auditStatus checks the seven EP-5 invariants and EP-6D's two: the status is a status, it is
// defended, a BUY_NOW's thesis standing is NOT_CONTRADICTED, a CONTRADICTED standing publishes
// no entry, and it
// does not contradict the prices or the entry shape it was published with.
//
// SPLIT OUT of AuditPlanInvariants rather than inlined because it is the one group whose inputs
// are not all on Plan itself — the price the verdict was made against lives on the decision
// trace — and mixing that lookup into the geometry block would make it read as though Plan
// carried a quote.
func auditStatus(p Plan, a *PlanInvariantAudit) {
	if !p.Status.Valid() {
		a.add(InvariantStatusCanonical, "Plan.Status", fmt.Sprintf("status = %q is not one of "+
			"the six declared EntryStatus values — a string no rule can produce is not a "+
			"cautious value, it is a case every consumer's switch falls through", p.Status))
	}
	if p.Status != "" && len(p.Reasons) == 0 {
		a.add(InvariantStatusHasReason, "Plan.Reasons", fmt.Sprintf("status = %q carries no "+
			"reason code — the arm that produced it, and therefore what it meant, cannot be "+
			"recovered from an archived row", p.Status))
	}

	// The price the verdict was made against. NOT a field of Plan: a plan is a statement about
	// levels, not a quote. Its absence disables the three price-bearing checks below, which is
	// stated at each one rather than left to be inferred from a nil.
	var cur *float64
	var semantic EntrySemantic
	if p.EntryTrace != nil {
		cur = p.EntryTrace.Decision.CurrentPrice
		semantic = p.EntryTrace.Decision.Semantic
	}

	// EP-6D: a CONTRADICTED thesis publishes no entry — no price, no entering or waiting status.
	if p.EntryTrace != nil && p.EntryTrace.Decision.Thesis == ThesisContradicted {
		priced := p.IdealEntry != nil || p.MaxChasePrice != nil || p.Invalidation != nil ||
			p.Target1 != nil || p.Target2 != nil || p.RiskReward != nil
		entering := p.Status == StatusBuyNow || p.Status == StatusWaitPullback ||
			p.Status == StatusWaitBreakout || p.Status == StatusTooExtended
		traced := p.EntryTrace.Decision.Rule == RuleThesisContradicted && !stepTracesEmpty(p.EntryTrace)
		if priced || entering || traced {
			a.add(InvariantThesisContradictedPublishesNoEntry, "Plan.EntryTrace*.Decision.Thesis",
				fmt.Sprintf("the thesis standing is CONTRADICTED (the scanner's primary Action is "+
					"SELL / REDUCE / TAKE PROFIT / STOP LOSS) and the plan still publishes an "+
					"entry: status %q, executable prices present = %v, step traces non-empty on "+
					"an S1 plan = %v. A stock the scanner wants out of must show no zone, ceiling, "+
					"stop or target — not even inside the trace — and no entering or waiting "+
					"status", p.Status, priced, traced))
		}
	}

	switch p.Status {
	case StatusBuyNow:
		// EP-6D. Checked BEFORE the zone checks because those `break` out of this case, and a
		// BUY_NOW with no zone AND a contradicted thesis is broken in two independent ways.
		if p.EntryTrace != nil && p.EntryTrace.Decision.Thesis != ThesisNotContradicted {
			a.add(InvariantBuyNowThesisNotContradicted, "Plan.EntryTrace*.Decision.Thesis",
				fmt.Sprintf("status is BUY_NOW and the thesis standing is %q — a stock whose "+
					"primary Action contradicts the entry (SELL / REDUCE / TAKE PROFIT / "+
					"STOP LOSS), or whose Action "+
					"nobody classified, is never told to buy at the market now",
					p.EntryTrace.Decision.Thesis))
		}
		if p.IdealEntry == nil {
			a.add(InvariantBuyNowHasZone, "Plan.IdealEntry", "status is BUY_NOW and there is "+
				"no ideal entry zone — \"buy at the market now\" with no band published is an "+
				"instruction whose price the reader has to invent")
			break
		}
		// NO CHECK WITHOUT THE PRICE. A plan with no decision trace records no quote, and
		// asserting the disjunction against a price that was never recorded would fail every
		// hand-built Plan for a reason that has nothing to do with the invariant.
		if cur == nil {
			break
		}
		inside := p.IdealEntry.Low <= *cur && *cur <= p.IdealEntry.High
		chased := p.MaxChasePrice != nil && *cur <= *p.MaxChasePrice
		if !inside && !chased {
			a.add(InvariantBuyNowPriceAuthorised, "Plan.Status", fmt.Sprintf(
				"status is BUY_NOW at %v, which is outside Plan.IdealEntry* [%v, %v] and is "+
					"not covered by a chase ceiling — the plan is telling a reader to pay a "+
					"price it never authorised", *cur, p.IdealEntry.Low, p.IdealEntry.High))
		}
	case StatusTooExtended:
		if p.IdealEntry == nil {
			a.add(InvariantTooExtendedKeepsZone, "Plan.IdealEntry", "status is TOO_EXTENDED "+
				"and the ideal entry zone is gone — \"too expensive here\" is only actionable "+
				"beside \"and this is where it would not be\", so the plan has kept the "+
				"useless half of its own sentence")
		}
		if p.MaxChasePrice == nil {
			a.add(InvariantTooExtendedAboveCeiling, "Plan.MaxChasePrice", "status is "+
				"TOO_EXTENDED and there is no chase ceiling — the ceiling is the only number "+
				"that makes \"past what this plan would pay\" a true statement")
			break
		}
		if cur == nil {
			break
		}
		if !(*cur > *p.MaxChasePrice) {
			a.add(InvariantTooExtendedAboveCeiling, "Plan.Status", fmt.Sprintf(
				"status is TOO_EXTENDED at %v with a ceiling of %v — the price is not above "+
					"the ceiling, and equality is not extension: the highest price the plan "+
					"permits paying is a price the plan permits paying", *cur, *p.MaxChasePrice))
		}
	case StatusWaitPullback:
		if semantic != "" && semantic != EntrySemanticPullback {
			a.add(InvariantWaitMatchesSemantic, "Plan.Status", fmt.Sprintf("status is "+
				"WAIT_PULLBACK and the entry shape is %q — a waiting status is the execution "+
				"reading of the shape the scanner chose, so this plan is overriding that "+
				"choice and telling a reader to wait for a move no thesis predicts", semantic))
		}
	case StatusWaitBreakout:
		if semantic != "" && semantic != EntrySemanticBreakout {
			a.add(InvariantWaitMatchesSemantic, "Plan.Status", fmt.Sprintf("status is "+
				"WAIT_BREAKOUT and the entry shape is %q — see WAIT_PULLBACK: the two waiting "+
				"statuses are readings of a shape, not opinions about one", semantic))
		}
	}
}

func (a *PlanInvariantAudit) add(inv PlanInvariant, path, detail string) {
	a.Violations = append(a.Violations, PlanViolation{Invariant: inv, Path: path, Detail: detail})
}

// priceZoneType is the type the walk collects zones by. A named variable so the walk cannot
// silently stop matching after a rename: renaming PriceZone updates this, deleting it does
// not compile.
var priceZoneType = reflect.TypeOf(PriceZone{})

// walkPlanValue visits every float and every PriceZone reachable from v, DEREFERENCING
// pointers and interfaces and descending into structs, slices, arrays and maps.
//
// Reflective for the same reason the purity sweep bans the whole `os.` prefix instead of
// listing its functions: an enumeration covers the fields the author thought of, and the ones
// that leak are always the ones added afterwards. A field-by-field finiteness check would have
// to be edited by every future work item, and the item that forgets is exactly the item that
// introduces the leak.
//
// This is a SECOND implementation of plan_test.go's walkFloats, on purpose and not by
// accident. That one is the independent witness for "ComputePlan emits no non-finite float";
// if it were replaced by a call into this file, one bug in this walk would blind both the
// checker and the test that is supposed to notice. TestTheTwoFloatWalkersAgree cross-checks
// them on the same fixture, which is the property that actually matters.
//
// TERMINATION. The walk has no cycle detection because the Plan type graph is acyclic — no
// type reachable from Plan contains a pointer back to a type already on the path — and
// TestThePlanTypeGraphIsWalkable asserts exactly that, so a future self-referential field
// fails a test instead of hanging a report.
func walkPlanValue(path string, v reflect.Value, a *PlanInvariantAudit) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		a.FloatsSeen = append(a.FloatsSeen, PlanFloat{Path: path, Value: v.Float()})
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		walkPlanValue(path+"*", v.Elem(), a)
	case reflect.Struct:
		if v.Type() == priceZoneType && v.CanInterface() {
			// CanInterface is false only for a value reached through an UNEXPORTED field.
			// The Plan graph has none — TestThePlanTypeGraphIsWalkable holds that line, and
			// it is the test that must fail if one is ever added, rather than this branch
			// quietly skipping the zone.
			zone, _ := v.Interface().(PriceZone)
			a.ZonesSeen = append(a.ZonesSeen, PlanZone{Path: path, Zone: zone})
		}
		for n := 0; n < v.NumField(); n++ {
			walkPlanValue(path+"."+v.Type().Field(n).Name, v.Field(n), a)
		}
	case reflect.Slice, reflect.Array:
		for n := 0; n < v.Len(); n++ {
			walkPlanValue(fmt.Sprintf("%s[%d]", path, n), v.Index(n), a)
		}
	case reflect.Map:
		// Sorted by rendered key, because a map iteration order is random and this package's
		// outputs must be byte-stable: an unstable violation order would look like a changed
		// verdict in a diff. Plan has no map field today; the branch is here because the
		// walk's job is to survive the field that gets added later.
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return renderKey(keys[i]) < renderKey(keys[j]) })
		for _, k := range keys {
			walkPlanValue(fmt.Sprintf("%s[%v]", path, renderKey(k)), v.MapIndex(k), a)
		}
	}
}

// renderKey formats a map key for the path, without assuming it is interface-able.
func renderKey(k reflect.Value) string {
	if k.CanInterface() {
		return fmt.Sprintf("%v", k.Interface())
	}
	return k.String()
}

// stepTracesEmpty reports whether the four entry STEP traces carry nothing at all. Compared
// against the zero value of each trace type, so any field a step fills — a price, a candidate
// list, a rejection code — counts.
func stepTracesEmpty(tr *EntryTrace) bool {
	return reflect.DeepEqual(tr.Zone, ZoneTrace{}) && reflect.DeepEqual(tr.Chase, ChaseTrace{}) &&
		reflect.DeepEqual(tr.Stop, StopTrace{}) && reflect.DeepEqual(tr.Targets, TargetTrace{})
}
