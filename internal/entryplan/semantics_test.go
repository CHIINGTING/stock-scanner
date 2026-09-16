package entryplan_test

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// The four distinctions EP-1 exists to fix, held by tests rather than by comments.
//
// The precedent is one level up, in the regime layer, and the argument transfers unchanged.
// internal/market/model's Regime doc: UNKNOWN "is not a sixth market condition: it is the
// honest answer when the data cannot support a call, and it exists so that insufficient
// history never silently becomes SIDEWAYS". internal/market/analyzer's R0 rule: UNKNOWN "is a
// state, not a failure, and it must never fall through to SIDEWAYS ('we looked, no edge')
// which is a verdict."
//
// Read those two sentences with INSUFFICIENT_DATA for UNKNOWN and NO_VALID_ENTRY for SIDEWAYS
// and they are this file's specification. The failure they describe is not hypothetical
// anywhere in this repo: a state that falls through to a verdict is unrecoverable afterwards,
// because the stored row no longer says which one happened.

// INSUFFICIENT_DATA is a STATE. NO_VALID_ENTRY is a VERDICT. They are not interchangeable and
// neither may stand in for the other.
func TestInsufficientDataIsNotAVerdict(t *testing.T) {
	if entryplan.StatusInsufficientData.IsVerdict() {
		t.Error("INSUFFICIENT_DATA classified as a verdict — it is the absence of one. " +
			"It is not bearish, and it is not NO_VALID_ENTRY.")
	}
	if !entryplan.StatusNoValidEntry.IsVerdict() {
		t.Error("NO_VALID_ENTRY classified as a state — it IS a verdict: the evidence was " +
			"sufficient, the policy ran, and it found no legal price to buy at.")
	}
	if entryplan.StatusInsufficientData == entryplan.StatusNoValidEntry {
		t.Fatal("the two collapsed into one value")
	}
}

// The classification is EXHAUSTIVE and table-driven, so adding a status forces a decision
// about which side of the line it falls on instead of inheriting the default.
func TestEveryStatusIsClassifiedAsStateOrVerdict(t *testing.T) {
	cases := []struct {
		status  entryplan.EntryStatus
		verdict bool
		why     string
	}{
		{entryplan.StatusBuyNow, true, "thesis + zone + policy all say execute now"},
		{entryplan.StatusWaitPullback, true, "the legal entry is below the market"},
		{entryplan.StatusWaitBreakout, true, "the legal entry is above the market, on confirmation"},
		{entryplan.StatusTooExtended, true, "the setup is valid and the PRICE is not — still a conclusion"},
		{entryplan.StatusNoValidEntry, true, "we looked; there is no legal entry"},
		{entryplan.StatusInsufficientData, false, "we could not look"},
	}
	seen := map[entryplan.EntryStatus]bool{}
	for _, c := range cases {
		if got := c.status.IsVerdict(); got != c.verdict {
			t.Errorf("%s.IsVerdict() = %v, want %v (%s)", c.status, got, c.verdict, c.why)
		}
		if !c.status.Valid() {
			t.Errorf("%s is not Valid()", c.status)
		}
		if seen[c.status] {
			t.Errorf("%s listed twice", c.status)
		}
		seen[c.status] = true
	}
	if len(seen) != 6 {
		t.Errorf("the table covers %d statuses, want 6 — a status was added without deciding "+
			"whether it is a state or a verdict", len(seen))
	}
	for _, s := range []entryplan.EntryStatus{"", "UNKNOWN", "SIDEWAYS", "buy_now", "HOLD"} {
		if s.Valid() {
			t.Errorf("%q accepted as a status", s)
		}
	}
}

// The R0 property, at the level of ComputePlan: MISSING EVIDENCE must never fall through to
// the verdict.
//
// Until EP-5 this could be written as "NO_VALID_ENTRY is never produced", because no rule in
// the package produced any verdict at all. EP-5 produces them, so the claim has to be written
// the way it was always meant: the verdict is reachable only from a DECISION, and never from an
// absence. The matrix is the right sweep for it because it contains every way each input can be
// missing, in every combination.
//
// The regime axis is what carries the line, and it carries it in BOTH directions:
//
//	BEAR                                   the table was consulted and said no   → a VERDICT
//	UNKNOWN / unavailable / not a regime    no decision was reached at all        → a STATE
//
// A row that is not decided must never be NO_VALID_ENTRY, whatever else is missing — and a
// BEAR row must never be INSUFFICIENT_DATA once it has a shape and a price, because that would
// be a decision reported as an absence, which is the same error in the other direction.
func TestMissingEvidenceNeverFallsThroughToNoValidEntry(t *testing.T) {
	// The reason codes a NO_VALID_ENTRY may rest on. Each is a COMPLETED rule: a policy that
	// refused, a search that ran, or a price relation that was evaluated. None of them is an
	// absence.
	verdictReasons := map[entryplan.Reason]bool{
		entryplan.ReasonStatusPolicyRefused:             true,
		entryplan.ReasonStatusNoLegalEntryZone:          true,
		entryplan.ReasonStatusNoRiskBoundary:            true,
		entryplan.ReasonStatusNoExecutablePriceRelation: true,
		entryplan.ReasonStatusImmediateEntryRefused:     true,
		entryplan.ReasonStatusRequirementNotMet:         true,
		entryplan.ReasonPlanInvariantViolated:           true,
	}

	var verdicts, states, undecidedRegimes int
	for _, c := range matrix() {
		p := entryplan.ComputePlan(c.in)
		decided := c.in.Regime.Status.OK() && c.in.Regime.Regime.Valid() &&
			c.in.Regime.Regime != model.RegimeUnknown
		if !decided {
			undecidedRegimes++
		}

		switch p.Status {
		case entryplan.StatusNoValidEntry:
			verdicts++
			if !decided {
				t.Fatalf("%s: an UNDECIDED regime produced NO_VALID_ENTRY — a state was "+
					"reported as a conclusion, and nothing downstream can tell the difference "+
					"afterwards. Reasons: %v", c.name, p.Reasons)
			}
			var defended bool
			for _, r := range p.Reasons {
				if verdictReasons[r] {
					defended = true
				}
			}
			if !defended {
				t.Fatalf("%s: NO_VALID_ENTRY with no completed rule behind it: %v",
					c.name, p.Reasons)
			}
		case entryplan.StatusInsufficientData:
			states++
			if decided && c.in.Regime.Regime == model.RegimeBear && c.in.WellFormed() &&
				c.in.EntrySemantic.Semantic.Resolved() && usableQuote(c.in.CurrentPrice) {
				t.Fatalf("%s: BEAR with a shape and a quote answered INSUFFICIENT_DATA — a "+
					"DECISION on sufficient evidence was reported as an absence. Reasons: %v",
					c.name, p.Reasons)
			}
		}
	}
	// ANTI-VACUITY, both halves. Without this the sweep passes when it produces only states
	// (the assertion about verdicts never runs) or only verdicts.
	if verdicts == 0 || states == 0 || undecidedRegimes == 0 {
		t.Fatalf("the matrix produced %d verdicts, %d states and %d undecided regimes — one "+
			"of the branches above never ran", verdicts, states, undecidedRegimes)
	}
}

// usableQuote is the test's own reading of "there is a price", written here rather than
// borrowed so it cannot drift into being the same expression production uses.
func usableQuote(o entryplan.PriceObservation) bool {
	return o.Status.OK() && o.Value != nil && *o.Value > 0 && !math.IsNaN(*o.Value) &&
		!math.IsInf(*o.Value, 0)
}

// NO LEVEL EVIDENCE, NO EXECUTION STATUS — and from EP-5 that is a claim about the DECISION
// rather than about the package's scope.
//
// Until EP-5 this test was called TestBuyNowIsUnreachableUnderEP1 and said "this work item
// implements none of BUY_NOW's conditions". That sentence is now false: EP-5 implements all of
// them, and BUY_NOW is reachable — TestEP5PublishesAVerdictAndEveryPriceEP4Computed requires
// it. What survives, and is worth keeping, is the property matrix() actually exercises: it
// carries NO MA20, NO pivot and NO base low, so no zone can exist for any of its 280k inputs,
// and every one of the four execution statuses needs a zone to be a statement about a price.
//
// So this is the decision layer's S5 arm, swept: a missing DEPENDENCY produces a STATE, never
// an execution reading.
func TestNoLevelEvidenceMeansNoExecutionStatus(t *testing.T) {
	for _, c := range matrix() {
		switch entryplan.ComputePlan(c.in).Status {
		case entryplan.StatusBuyNow, entryplan.StatusWaitPullback,
			entryplan.StatusWaitBreakout, entryplan.StatusTooExtended:
			t.Fatalf("%s: EP-1 reached an execution status with no entry rule implemented", c.name)
		}
	}
}

// TOO_EXTENDED is "here, no — there, yes". The status is only half the information; the other
// half is the zone the price ran away from, and a reader shown only the word has been given
// the useless half.
//
// EP-1 cannot compute the zone, so what is asserted is that the TYPE supports carrying it —
// the shape that the work item computing zones has to fill.
func TestTooExtendedCanCarryAnIdealEntryZone(t *testing.T) {
	width := 0.8
	p := entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		Status:      entryplan.StatusTooExtended,
		RuleVersion: entryplan.RuleVersion,
		IdealEntry: &entryplan.PriceZone{
			Low: 845, High: 862, WidthATR: &width,
			PriceBasis: entryplan.PriceBasisRaw,
			Basis:      []string{"MA20"},
		},
		Confidence: entryplan.ConfidenceMedium,
	}
	if !p.HasExecutablePrices() {
		t.Fatal("a TOO_EXTENDED plan carrying a zone reports no executable prices")
	}
	if p.IdealEntry.Low >= p.IdealEntry.High {
		t.Fatal("zone bounds are not ordered")
	}
	if p.IdealEntry.PriceBasis != entryplan.PriceBasisRaw {
		t.Fatal("the zone cannot state the basis it was measured on")
	}
	if !p.Status.IsVerdict() {
		t.Fatal("TOO_EXTENDED must be a verdict: the thesis is intact and the price is not")
	}
}

// WAIT_PULLBACK / WAIT_BREAKOUT are the EXECUTION-SIDE readings of entry semantics the
// scanner already produces. They are not a second opinion about whether to buy, and this
// package is not allowed to acquire one — see the architecture test for the enforcement.
//
// Pinned here as a value mapping so that if EP-5 ever wires it the other way round, the
// intent is written down as a test rather than as prose.
func TestStatusesAreExecutionReadingsOfExistingEntrySemantics(t *testing.T) {
	cases := []struct {
		existing string // the vocabulary this repo already ships
		status   entryplan.EntryStatus
	}{
		{"PULLBACK_BUY (scanner.ActPullbackBuy) / 拉回 5/10 日線量縮承接", entryplan.StatusWaitPullback},
		{"BREAKOUT_BUY (scanner.ActBreakoutBuy)", entryplan.StatusWaitBreakout},
		{"OVERHEATED entry caution / 太熱不要追", entryplan.StatusTooExtended},
	}
	for _, c := range cases {
		if !c.status.IsVerdict() {
			t.Errorf("%s maps to %s, which is not a verdict — an existing entry semantic "+
				"cannot translate into 'we could not look'", c.existing, c.status)
		}
		if c.status == entryplan.StatusInsufficientData {
			t.Errorf("%s collapsed into INSUFFICIENT_DATA", c.existing)
		}
	}
	// The mapping itself is EP-5's work item. What EP-1 owns is that the target vocabulary
	// exists and is distinct, so the translation has somewhere to land.
	if entryplan.StatusWaitPullback == entryplan.StatusWaitBreakout {
		t.Fatal("pullback and breakout entries are not the same order")
	}
}
