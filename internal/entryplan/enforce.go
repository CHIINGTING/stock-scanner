package entryplan

import "strings"

// ── EP-3b: the production invariant gate ──────────────────────────────────────────────
//
// EP-3a shipped the checker (invariants.go) and deliberately did not wire it in, on the rule
// that "the item that writes a guard must not also be the item that decides what happens when
// it fires". EP-3b is the item that can produce a violation, so this file makes that decision.
//
// THE DECISION: withdraw the offending prices, keep the plan, and say so on the record.
//
// The three candidates and why the other two lose:
//
//   - Return the plan with the illegal price and a violation attached. Refused: the price is
//     the part a reader acts on, and an entry zone with Low > High reaching a report as
//     "here is the zone, also there is a problem" is a number that gets used.
//   - Refuse to return at all (panic, or an error return). Refused: ComputePlan is TOTAL —
//     defined for every input including the zero Snapshot — and a report that cannot render
//     because one stock's arithmetic broke is a worse outcome than a report that says this
//     stock has no price today.
//   - Withdraw the prices and keep the evidence, the trace and the reasons. Taken: the plan
//     still explains itself, the failure is greppable (ReasonPlanInvariantViolated), and the
//     trace still holds the arithmetic that produced the illegal number, which is the only
//     thing anyone could debug it from.
//
// # EP-4 narrowed WHICH prices, and the narrowing is a DEPENDENCY argument
//
// EP-3b withdrew all six fields for any violation, on the argument that "a bug does not
// respect field boundaries: the invariant that fired says which comparison failed, not which
// number was wrong". That argument was RIGHT FOR EP-3b and is wrong now, and the reason is that
// EP-3b had two prices with one dependency between them while EP-4 has six with a graph:
//
//	IdealEntry ─┬─→ MaxChasePrice
//	            └─→ Invalidation ─→ Target1 ─┬─→ Target2
//	                                         └─→ RiskReward
//
// Under the old rule a Target2 pulled below Target1 by a valuation ceiling would delete the
// entry zone, the chase ceiling, the stop and the risk/reward — five correct, independently
// derived prices — because a sixth, OPTIONAL one was inconsistent. That is not caution: the
// zone is what a reader acts on first, and deleting it hides a usable entry behind a fault in
// a sanity bound.
//
// So each violation is attributed to the field it is ABOUT, and that field is withdrawn
// together with everything computed FROM it — never less, never more. See withdrawalFor for
// the table and for the one case that still withdraws everything: a non-finite float whose
// path names no executable field, which is the situation EP-3b's argument actually described.
//
// # No production input reaches it, and that is asserted rather than hoped
//
// ReasonPlanInvariantViolated is a RESERVED reason (see ReservedReasons): reaching it from
// ComputePlan would mean this package computed an illegal price. What the tests do instead is
// drive this function DIRECTLY on hand-built broken Plans — the same inversion EP-3a used for
// the checker itself, and the same one the purity sweep uses with its planted os.WriteFile
// line. And because ComputePlan records the OUTCOME on the trace (InvariantCheckClean), the
// call cannot be deleted from ComputePlan without a test going red: "the gate ran and found
// nothing" and "the gate is gone" are different outputs, not the same silence.

// EnforcePlanInvariants is the gate every Plan this package returns passes through.
//
// It returns the plan to publish and the violations it found. A clean plan comes back
// unchanged except for the outcome stamped on its trace; a broken one comes back with the
// AFFECTED executable prices removed, ReasonPlanInvariantViolated appended, and both the
// violated codes and the withdrawn field names on the trace.
//
// PURE. It never writes through the argument's pointers — the trace is COPIED before the
// outcome is stamped on it — so calling it on a plan a caller still holds cannot change the
// caller's copy. TestTheInvariantGateDoesNotMutateItsArgument is the guard.
func EnforcePlanInvariants(p Plan) (Plan, []PlanViolation) {
	violations := PlanInvariantViolations(p)

	outcome := InvariantCheckClean
	var withdrawn []PlanPriceField
	if len(violations) != 0 {
		outcome = InvariantCheckPricesWithdrawn

		// The union of every violation's withdrawal set, then the fields themselves. The
		// assignment is enumerated rather than reflected on purpose: this is the one place in
		// the package where a FIELD-BY-FIELD list is the right shape, because what it encodes
		// is "which fields may a reader place an order from", and that is a judgement each
		// future field's own work item must make explicitly. A reflective sweep would silently
		// withdraw a field somebody added for another purpose, and silently keep one it did
		// not recognise.
		//
		// TestTheGateWithdrawsExactlyTheAffectedFields holds the table against Plan's type,
		// so a new price-bearing field fails a test instead of surviving the gate.
		drop := map[PlanPriceField]bool{}
		for _, v := range violations {
			for f := range withdrawalFor(v) {
				drop[f] = true
			}
		}
		for _, f := range AllPlanPriceFields {
			if !drop[f] {
				continue
			}
			withdrawn = append(withdrawn, f)
			switch f {
			case FieldIdealEntry:
				p.IdealEntry = nil
			case FieldMaxChasePrice:
				p.MaxChasePrice = nil
			case FieldInvalidation:
				p.Invalidation = nil
			case FieldTarget1:
				p.Target1 = nil
			case FieldTarget2:
				p.Target2 = nil
			case FieldRiskReward:
				p.RiskReward = nil
			}
		}

		p.Reasons = appendReason(p.Reasons, ReasonPlanInvariantViolated)
		if len(withdrawn) != 0 {
			p.Caveats = append(p.Caveats, "此計畫違反自身不變式,已撤回受影響的可下單價位("+
				renderFields(withdrawn)+");這是本套件的程式錯誤,不是市場狀態")
		} else {
			// A STATUS-ONLY violation, so the outcome stamp must not say PRICES_WITHDRAWN: no
			// price was withdrawn, and a reader following that stamp would go looking for a
			// number that is still there.
			outcome = InvariantCheckStatusWithdrawn
		}

		// ── EP-5: A CONTRADICTED VERDICT IS WITHDRAWN THE WAY A CONTRADICTED PRICE IS ────
		//
		// The status invariants (EP-5's seven and EP-6D's BUY_NOW_THESIS_NOT_CONTRADICTED; EP-6D's
		// THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY is the one that also withdraws prices) have no price to withdraw — "BUY_NOW with no zone" is
		// not repaired by deleting a number — so the same decision is applied to the field
		// that IS wrong: the status goes back to INSUFFICIENT_DATA.
		//
		// Why INSUFFICIENT_DATA and not "leave the verdict and warn": every other value is a
		// VERDICT (EntryStatus.IsVerdict), and a plan whose verdict contradicts its own
		// numbers has not reached one. INSUFFICIENT_DATA is the only status that claims
		// nothing about the trade, so it is the honest residue — and it is a STATE, which is
		// exactly what "this package contradicted itself" is from a reader's point of view.
		//
		// Why not also delete every price: the numbers are not implicated. A verdict that
		// disagrees with a correct zone is a bug in the DECISION, and taking the zone with it
		// would hide a usable entry behind a fault in the layer above it — the same narrowing
		// argument EP-4 made about the second target.
		//
		// The ARM that fired is still on EntryTrace.Decision, so "the gate withdrew the
		// verdict" and "the decision never ran" stay different outputs.
		// F1 (delivered and reviewed as part of EP-7; not EP-6G): THE MIXED REPAIR HAS ITS OWN STAMP.
		//
		// A plan that broke a PRICE invariant and a STATUS invariant in the same pass withdraws
		// both — the prices above, the verdict here — and is stamped PRICES_AND_STATUS_WITHDRAWN
		// by the assignment just below. `outcome` arrives here as PRICES_WITHDRAWN when the
		// withdrawal set is non-empty and as STATUS_WITHDRAWN when it is empty; only the first is
		// upgraded, so a status-only repair keeps STATUS_WITHDRAWN. Without the upgrade the stamp
		// would say PRICES_WITHDRAWN and a reader grouping repairs by InvariantCheck would never
		// learn that the verdict was withdrawn too.
		//
		// ComputePlan cannot currently produce this combination, but callers can hand the gate a
		// constructed Plan; TestTheGateRecordsMixedPriceAndStatusWithdrawal plants one. EP-7
		// persists no InvariantCheck value (see internal/research/entryplan_evidence.go), so the
		// stamp is carried on EntryTrace only.
		if statusViolated(violations) {
			if len(withdrawn) != 0 {
				outcome = InvariantCheckPricesAndStatusWithdrawn
			}
			p.Status = StatusInsufficientData
			p.Caveats = append(p.Caveats, "此計畫的進場狀態與自身價位互相矛盾,已退回 "+
				"INSUFFICIENT_DATA;原本的判定與觸發的規則仍記錄在 EntryTrace.Decision 上")
		}
	}

	if p.EntryTrace != nil {
		// COPIED, then stamped. Mutating *p.EntryTrace would reach into the caller's own
		// struct, and a gate that edits its input is a gate that cannot be run twice.
		tr := *p.EntryTrace
		// EP-6D: an S1 plan whose step traces carry anything is stripped of them — the user's
		// rule is that no entry-derived number survives anywhere in such a plan.
		if tr.Decision.Rule == RuleThesisContradicted && violatedOne(violations,
			InvariantThesisContradictedPublishesNoEntry) {
			tr.Zone, tr.Chase, tr.Stop, tr.Targets = ZoneTrace{}, ChaseTrace{}, StopTrace{}, TargetTrace{}
		}
		tr.InvariantCheck = outcome
		tr.InvariantViolations = violatedInvariants(violations)
		tr.WithdrawnFields = withdrawn
		p.EntryTrace = &tr
	}
	return p, violations
}

// statusInvariants are the EP-5 codes that are about Plan.Status rather than about a price.
//
// A SET rather than a prefix match on the code string, for the reason AllPlanInvariants is
// declared rather than derived: a name-shaped rule ("everything starting with STATUS_") is a
// second, invisible registry that the next code either joins or silently escapes.
var statusInvariants = map[PlanInvariant]bool{
	InvariantStatusCanonical:         true,
	InvariantStatusHasReason:         true,
	InvariantBuyNowHasZone:           true,
	InvariantBuyNowPriceAuthorised:   true,
	InvariantTooExtendedAboveCeiling: true,
	InvariantTooExtendedKeepsZone:    true,
	InvariantWaitMatchesSemantic:     true,
	// EP-6D. A STATUS invariant: it withdraws no price, only the verdict.
	InvariantBuyNowThesisNotContradicted: true,
	// EP-6D. Status AND price: the verdict goes back to INSUFFICIENT_DATA here, and
	// withdrawalFor additionally withdraws every executable field.
	InvariantThesisContradictedPublishesNoEntry: true,
}

// statusViolated reports whether any violation is about the STATUS.
func statusViolated(vs []PlanViolation) bool {
	for _, v := range vs {
		if statusInvariants[v.Invariant] {
			return true
		}
	}
	return false
}

// PlanPriceField names one field of a Plan a reader could place an order from.
//
// A registry, and it is the WITHDRAWAL VOCABULARY: the gate records which of these it removed,
// so "the gate fired and took the second target" and "the gate fired and took everything" are
// different outputs rather than the same PRICES_WITHDRAWN stamp. Persisted, so the values are
// pinned as literals by golden_test.go.
type PlanPriceField string

const (
	FieldIdealEntry    PlanPriceField = "IdealEntry"
	FieldMaxChasePrice PlanPriceField = "MaxChasePrice"
	FieldInvalidation  PlanPriceField = "Invalidation"
	FieldTarget1       PlanPriceField = "Target1"
	FieldTarget2       PlanPriceField = "Target2"
	FieldRiskReward    PlanPriceField = "RiskReward"
)

// AllPlanPriceFields is every executable field, in DEPENDENCY ORDER — a field never appears
// before something it is computed from.
//
// The order is what makes the withdrawn list readable in a diff, and it is also the order the
// closure below is written in. The VALUES are the Go field names on purpose: a violation Path
// is "Plan.Target2*.Price", so the same string identifies the field in both directions and
// there is no second mapping to drift.
var AllPlanPriceFields = []PlanPriceField{
	FieldIdealEntry,
	FieldMaxChasePrice,
	FieldInvalidation,
	FieldTarget1,
	FieldTarget2,
	FieldRiskReward,
}

// priceFieldDependents is the DOWNSTREAM CLOSURE: every field whose value is computed from the
// key, including the key itself.
//
// Written out rather than derived from a graph walk, because each row is a claim about what the
// rules actually do and is checkable against them:
//
//	IdealEntry     everything. The zone is the root: the chase ceiling is measured from
//	               zone.High, the invalidation must sit below zone.Low, and both targets and
//	               the R:R are measured from zone.High.
//	MaxChasePrice  itself. Nothing is computed from the ceiling — it is a leaf, which is why
//	               a chase violation must not be allowed to delete the risk side.
//	Invalidation   the targets and the R:R, because risk = zone.High − invalidation is their
//	               shared denominator. NOT the zone: the zone was computed first and does not
//	               depend on the stop.
//	Target1        Target2 and the R:R. Target2's ORDERING is defined against Target1, and the
//	               R:R's reward is measured TO Target1. Not the zone, not the stop.
//	Target2        itself. A leaf, and the optional one; see EnforcePlanInvariants on why this
//	               row is the whole reason the narrowing exists.
//	RiskReward     itself. A leaf: it is derived from three fields and nothing derives from it.
var priceFieldDependents = map[PlanPriceField][]PlanPriceField{
	FieldIdealEntry: {FieldIdealEntry, FieldMaxChasePrice, FieldInvalidation, FieldTarget1,
		FieldTarget2, FieldRiskReward},
	FieldMaxChasePrice: {FieldMaxChasePrice},
	FieldInvalidation:  {FieldInvalidation, FieldTarget1, FieldTarget2, FieldRiskReward},
	FieldTarget1:       {FieldTarget1, FieldTarget2, FieldRiskReward},
	FieldTarget2:       {FieldTarget2},
	FieldRiskReward:    {FieldRiskReward},
}

// withdrawalFor is the set of executable fields one violation costs the plan.
//
// # The geometry invariants name their own field
//
// Each of the nine geometry codes is ABOUT one field — a zone bound, the chase ceiling, the
// stop, either target, the risk/reward — so the violation identifies the owner directly and
// the closure above does the rest.
//
// # The finiteness codes are attributed BY PATH, and fall back to everything
//
// FLOAT_NOT_NAN and FLOAT_NOT_INF are produced by a reflective sweep over every float
// reachable from the Plan, including floats in the evidence census and in the trace. When the
// path names an executable field ("Plan.Target1*.RMultiple*") the violation is that field's.
// When it does not ("Plan.Evidence[0].Value*", "Plan.EntryTrace*.Zone.Center*") there is
// nothing to attribute it to, and EP-3b's argument applies in full: a non-finite number
// somewhere unattributable in a plan is an arithmetic bug that respects no field boundary, so
// every price goes. That is the CONSERVATIVE branch, and it is reached only when the checker
// cannot say whose number it was.
func withdrawalFor(v PlanViolation) map[PlanPriceField]bool {
	out := map[PlanPriceField]bool{}
	if v.Invariant == InvariantThesisContradictedPublishesNoEntry {
		// EP-6D. The rule is "no executable price at all", so every field goes — checked
		// BEFORE the status early-return below, which would otherwise withdraw nothing.
		for _, f := range AllPlanPriceFields {
			out[f] = true
		}
		return out
	}
	if statusInvariants[v.Invariant] {
		// EP-5's status codes withdraw NO PRICE. The offending field is Plan.Status and it is
		// handled in EnforcePlanInvariants; falling through to the conservative branch below
		// would delete six correct numbers because the verdict computed from them was wrong,
		// which is the narrowing argument EP-4 made, inverted.
		return out
	}
	owner, ok := priceFieldOwner(v)
	if !ok {
		for _, f := range AllPlanPriceFields {
			out[f] = true
		}
		return out
	}
	for _, f := range priceFieldDependents[owner] {
		out[f] = true
	}
	return out
}

// priceFieldOwner is which executable field a violation is about.
//
// ok == false means "no executable field", which is a real answer for a non-finite float in the
// evidence census or the trace — see withdrawalFor.
func priceFieldOwner(v PlanViolation) (PlanPriceField, bool) {
	switch v.Invariant {
	case InvariantZoneLowPositive, InvariantZoneHighPositive, InvariantZoneOrdered:
		// The zone invariants are collected reflectively over every PriceZone the walk finds,
		// and Plan has exactly one zone-valued field today. The PATH is still what decides, so
		// a second zone-valued field added later is attributed to itself rather than to
		// IdealEntry by assumption.
		return fieldForPath(v.Path)
	case InvariantMaxChasePositive, InvariantMaxChaseCoversZone:
		return FieldMaxChasePrice, true
	case InvariantInvalidationBelowZone:
		return FieldInvalidation, true
	case InvariantTarget1AboveZone:
		return FieldTarget1, true
	case InvariantTarget2NotBelowTarget1:
		// TARGET 2, not Target1. The pair is inconsistent and the checker cannot say which of
		// the two is wrong — but the ORDER of the plan is "first target, then second", so the
		// second is the one that has to justify itself against the first. Withdrawing Target1
		// instead would take the R:R with it (see the closure) and delete the number a reader
		// actually decides on because an optional 3.5R level disagreed with it.
		return FieldTarget2, true
	case InvariantRiskRewardFinitePositive:
		return FieldRiskReward, true
	case InvariantFloatNotNaN, InvariantFloatNotInf:
		return fieldForPath(v.Path)
	}
	// A code outside the registry. Attributed to nothing, so withdrawalFor takes everything:
	// an invariant this function has never heard of is exactly the case where "which half is
	// good" is unknown.
	return "", false
}

// fieldForPath reads the executable field out of a violation Path.
//
// The notation is walkPlanValue's: "Plan" then ".Field", with "*" for a pointer dereference and
// "[n]" for a slice index. So the field is the segment after "Plan." up to the first "." or "*",
// and a path that does not start there — or names something that is not an executable field —
// answers false rather than guessing.
//
// It relies on no PlanPriceField being a PREFIX of another, which is true of the six today and
// is asserted by TestNoPriceFieldNameIsAPrefixOfAnother rather than assumed, because a field
// called "Target10" would otherwise be attributed to "Target1".
func fieldForPath(path string) (PlanPriceField, bool) {
	const prefix = "Plan."
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := path[len(prefix):]
	for _, f := range AllPlanPriceFields {
		name := string(f)
		if rest == name {
			return f, true
		}
		if strings.HasPrefix(rest, name) && (rest[len(name)] == '*' || rest[len(name)] == '.') {
			return f, true
		}
	}
	return "", false
}

// renderFields joins withdrawn field names for the caveat prose. Presentation only; the
// machine-readable list is EntryTrace.WithdrawnFields.
func renderFields(fs []PlanPriceField) string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f))
	}
	return strings.Join(out, ", ")
}

// violatedInvariants reduces the violation list to its distinct codes, in AllPlanInvariants
// order.
//
// Codes and not sentences, and in registry order rather than in report order, because this is
// the field a consumer COUNTS on — PlanViolation.Detail already carries the English.
func violatedInvariants(vs []PlanViolation) []PlanInvariant {
	if len(vs) == 0 {
		return nil
	}
	seen := map[PlanInvariant]bool{}
	for _, v := range vs {
		seen[v.Invariant] = true
	}
	var out []PlanInvariant
	for _, inv := range AllPlanInvariants {
		if seen[inv] {
			out = append(out, inv)
			delete(seen, inv)
		}
	}
	// A code outside the registry cannot be produced by the checker (its own registry test
	// holds that), and it is not dropped here either: a violation nobody can enumerate is
	// still a violation, and silently losing it would be the one bug this file exists to
	// surface.
	for _, v := range vs {
		if seen[v.Invariant] {
			out = append(out, v.Invariant)
			delete(seen, v.Invariant)
		}
	}
	return out
}

// appendReason appends r unless it is already there.
//
// The reason list is compared byte for byte in the archive and a repeated code would read as
// two independent findings. TestPlanReasonsAreDeduplicatedAndOrdered is the guard, and it is
// why the two independent steps (zone, chase) can both report the same underlying cause
// without the plan saying it twice.
func appendReason(list []Reason, r Reason) []Reason {
	if r == "" {
		return list
	}
	for _, existing := range list {
		if existing == r {
			return list
		}
	}
	return append(list, r)
}

// violatedOne reports whether a specific invariant is among the violations.
func violatedOne(vs []PlanViolation, inv PlanInvariant) bool {
	for _, v := range vs {
		if v.Invariant == inv {
			return true
		}
	}
	return false
}
