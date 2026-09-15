package entryplan_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── EP-5: the decision layer ──────────────────────────────────────────────────────────
//
// decidecases_test.go holds the matrix; this file is what reads it back, plus the properties
// that are about the TABLE rather than about any one row: that the arms are ordered, that every
// arm is reachable, that every absence code has a reading, and that the four things spec §15/16
// and §26/27 say must NOT gate a status cannot gate one.

// THE MATRIX. Every row asserts the ARM, the STATUS and the EXACT reason list.
//
// The arm matters as much as the status: two arms publish BUY_NOW and four publish
// NO_VALID_ENTRY, so a table checking only the status would pass with the wrong arm firing —
// and the arm is the half an archived verdict is re-derivable from.
func TestTheDecisionMatrixIsTheSpecifiedTable(t *testing.T) {
	for _, c := range decisionCases() {
		got := entryplan.DecideStatus(c.in)
		if got.Rule != c.wantRule {
			t.Errorf("%s: arm %q, want %q (status was %q) — a different arm reaching the "+
				"same status is a different sentence, and the archived row records the arm",
				c.name, got.Rule, c.wantRule, got.Status)
		}
		if got.Status != c.wantStatus {
			t.Errorf("%s: status %q, want %q (arm %q)", c.name, got.Status, c.wantStatus,
				got.Rule)
		}
		if !reflect.DeepEqual(got.Reasons, c.wantReasons) {
			t.Errorf("%s: reasons %v, want exactly %v — the list is compared byte for byte in "+
				"the archive, so an extra code and a reordering are both changes",
				c.name, got.Reasons, c.wantReasons)
		}
		for _, r := range got.Reasons {
			if !entryplan.KnownReason(r) {
				t.Errorf("%s: emits %q, which is not in AllReasons", c.name, r)
			}
		}
		if !entryplan.KnownDecisionRule(got.Rule) {
			t.Errorf("%s: arm %q is not in AllDecisionRules", c.name, got.Rule)
		}
		if len(got.Reasons) == 0 {
			t.Errorf("%s: a status with no reason — the arm that produced it, and therefore "+
				"what it meant, cannot be recovered from an archived row", c.name)
		}
		if !got.Status.Valid() {
			t.Errorf("%s: status %q is not one of the six", c.name, got.Status)
		}
	}
}

// The table's own hygiene: no unnamed rows, no duplicate names, no row that asserts nothing.
func TestTheDecisionCaseTableIsWellFormed(t *testing.T) {
	cases := decisionCases()
	if len(cases) < 50 {
		t.Errorf("only %d rows — the matrix is two shapes by six regimes by nine price "+
			"relations plus the absence readings, and a shrinking table is how an arm loses "+
			"its only witness", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.name == "" {
			t.Error("an unnamed row: every failure message would be anonymous")
		}
		if seen[c.name] {
			t.Errorf("duplicate row name %q — one of the two is invisible in every failure",
				c.name)
		}
		seen[c.name] = true
		if c.wantRule == "" || c.wantStatus == "" || len(c.wantReasons) == 0 {
			t.Errorf("%s: a row that does not state an arm, a status AND a reason list is a "+
				"row that cannot fail", c.name)
		}
	}
}

// ── the canonical boundaries (spec §24) ───────────────────────────────────────────────

// THE FIVE PLACES WHERE `<` AND `<=` DIFFER.
//
// Each of these is one character away from a plausible wrong implementation, and each of them
// changes a verdict a reader acts on. This test does not check what the answers ARE — the
// matrix does that — it checks that the matrix CONTAINS them, so that deleting the rows makes
// a test fail rather than making the coverage quietly smaller.
func TestTheDecisionMatrixCoversTheCanonicalBoundaries(t *testing.T) {
	tick := decTick()
	boundaries := []struct {
		name  string
		price float64
	}{
		{"Current == Zone.Low", decZoneLow},
		{"Current == Zone.High", decZoneHigh},
		{"Current == MaxChase", decCeiling},
		{"Current == MaxChase + one tick", decCeiling + tick},
		{"Current == Zone.Low − one tick", decZoneLow - tick},
	}
	// A boundary is COVERED only if a row stands at that exact price with the whole rest of
	// the evidence present — a row that is refused by the policy before the price is read
	// covers nothing about the price.
	covered := map[string]int{}
	for _, c := range decisionCases() {
		if c.in.CurrentPrice == nil || c.in.Zone == nil || c.in.Invalidation == nil {
			continue
		}
		for _, b := range boundaries {
			if *c.in.CurrentPrice == b.price {
				covered[b.name]++
			}
		}
	}
	for _, b := range boundaries {
		if covered[b.name] == 0 {
			t.Errorf("no fully-evidenced row stands at %s (%v) — that boundary is where the "+
				"inclusive/exclusive mistake lives, and nothing in this suite would catch it",
				b.name, b.price)
		}
	}
	// And the two "one tick" rows must be built from a REAL tick, not from a typed-in 0.5.
	if tick <= 0 {
		t.Fatalf("tick size at %v is %v", decZoneHigh, tick)
	}
}

// ── the arm registry, from both ends ──────────────────────────────────────────────────

// Every arm DecideStatus returns is in AllDecisionRules, and every member is returned for at
// least one row of the matrix.
//
// The backward half is the one that matters, for the reason the Reason registry's backward half
// matters: an arm nobody can reach is a branch that has never run, and its id is a value a
// consumer writes a case for and never sees.
func TestEveryDecisionRuleIsReachableAndRegistered(t *testing.T) {
	seen := map[entryplan.DecisionRule]string{}
	for _, c := range decisionCases() {
		r := entryplan.DecideStatus(c.in).Rule
		if _, ok := seen[r]; !ok {
			seen[r] = c.name
		}
		if !entryplan.KnownDecisionRule(r) {
			t.Errorf("%s: arm %q is not in the registry", c.name, r)
		}
	}
	for _, want := range entryplan.AllDecisionRules {
		if _, ok := seen[want]; !ok {
			t.Errorf("no row in the matrix reaches arm %q — the arm is in the registry and "+
				"no test has ever seen it fire, so it could be deleted with this suite still "+
				"green", want)
		}
	}
	if len(seen) != len(entryplan.AllDecisionRules) {
		t.Errorf("the matrix reached %d arms and the registry has %d", len(seen),
			len(entryplan.AllDecisionRules))
	}
	if entryplan.KnownDecisionRule("S99") {
		t.Error("KnownDecisionRule accepts an id that is not in the registry")
	}
	// The registry itself: no duplicates, no empty ids.
	dup := map[entryplan.DecisionRule]bool{}
	for _, r := range entryplan.AllDecisionRules {
		if r == "" {
			t.Error("an empty id in AllDecisionRules")
		}
		if dup[r] {
			t.Errorf("%q listed twice", r)
		}
		dup[r] = true
	}
}

// ── the ORDER, which is part of the specification ─────────────────────────────────────

// FIRST MATCH WINS, and these are the inputs that satisfy TWO arms at once.
//
// This is the only kind of test that can pin an ordering. Each row below is an input for which
// two arms' conditions are BOTH true, and the assertion is that the EARLIER one answers — plus
// the independent check that "earlier" means earlier in AllDecisionRules, so the table and the
// registry cannot drift into disagreeing about what the order even is.
func TestTheDecisionPrecedenceIsTheSpecifiedOrder(t *testing.T) {
	index := map[entryplan.DecisionRule]int{}
	for n, r := range entryplan.AllDecisionRules {
		index[r] = n
	}

	cases := []struct {
		name          string
		in            entryplan.DecisionInput
		earlier, also entryplan.DecisionRule
		why           string
	}{
		{
			name:    "no shape AND a refusing regime",
			in:      dec(model.RegimeBear, entryplan.EntrySemanticUnknown, 850),
			earlier: entryplan.RuleSemanticUnresolved, also: entryplan.RulePolicyRefused,
			why: "'BEAR forbids this shape' is a sentence about nothing when there is no shape",
		},
		{
			name: "no price AND a refusing regime",
			in: dec(model.RegimeBear, entryplan.EntrySemanticPullback, 850,
				func(in *entryplan.DecisionInput) { in.CurrentPrice = nil }),
			earlier: entryplan.RuleCurrentPriceUnavailable, also: entryplan.RulePolicyRefused,
			why: "S2 is above S4 so that every arm below it may assume a comparable market",
		},
		{
			name:    "a refusing regime AND a price above the ceiling",
			in:      dec(model.RegimeBear, entryplan.EntrySemanticPullback, 900),
			earlier: entryplan.RulePolicyRefused, also: entryplan.RuleAboveMaxChase,
			why: "TOO_EXTENDED implies a LOWER price at which this would be bought, and in " +
				"BEAR there is none — spec §13",
		},
		{
			name: "no band AND no risk boundary",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, 850,
				noZone(entryplan.ReasonZoneATRUnavailable),
				noStop(entryplan.ReasonNoValidInvalidation)),
			earlier: entryplan.RuleZoneUnavailable, also: entryplan.RuleNoRiskBoundary,
			why: "the invalidation is defined RELATIVE to the band, so with no band there is " +
				"nothing for 'no legal boundary below it' to be about",
		},
		{
			name: "no risk boundary AND a price above the ceiling",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, 900,
				noStop(entryplan.ReasonNoValidInvalidation)),
			earlier: entryplan.RuleNoRiskBoundary, also: entryplan.RuleAboveMaxChase,
			why: "a plan that cannot say where it would be proved wrong is not executable at " +
				"ANY price, so where the market stands does not make it one",
		},
		{
			name: "no risk boundary AND a price inside the band",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, 850,
				noStop(entryplan.ReasonInvalidationEvidenceUnavailable)),
			earlier: entryplan.RuleRiskBoundaryUnavailable, also: entryplan.RuleInsideZone,
			why: "BUY_NOW with no invalidation is an instruction with no exit",
		},
		{
			name:    "a price above the ceiling AND a permitted breakout chase below it",
			in:      dec(model.RegimeBull, entryplan.EntrySemanticBreakout, 900),
			earlier: entryplan.RuleAboveMaxChase, also: entryplan.RuleImmediateChase,
			why: "the ceiling bounds the chase; past it the chase arm must not fire",
		},
		{
			name: "no ceiling by EVIDENCE GAP AND a pullback above the band",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, 866,
				noChase(entryplan.ReasonPreviousCloseUnavailable)),
			earlier: entryplan.RuleChaseCeilingUnresolved, also: entryplan.RuleWaitPullback,
			why: "'merely above the band' and 'past anything this plan would pay' are both " +
				"consistent with what is known, and picking one is a guess",
		},
		{
			// THE ROW THAT MAKES S17's REDUNDANT SEMANTIC TEST A CHECKED PROPERTY. A breakout
			// above its band matches the chase block AND (ignoring its shape) the wait arm's
			// `above` condition. S14 must answer. Hoisting the wait arm above the chase block
			// — which is what makes the deleted-gate mutation load-bearing rather than
			// equivalent — turns this red.
			name:    "a breakout above its band AND the position the wait arm sits below",
			in:      dec(model.RegimeBull, entryplan.EntrySemanticBreakout, 866),
			earlier: entryplan.RuleImmediateChase, also: entryplan.RuleWaitPullback,
			why: "a breakout that has run is not waiting for a retracement no thesis " +
				"predicts, and the arm order is what makes the shape test at S17 unnecessary",
		},
		{
			name:    "inside the band AND a shape whose wait arm would also match",
			in:      dec(model.RegimeBull, entryplan.EntrySemanticBreakout, 850),
			earlier: entryplan.RuleInsideZone, also: entryplan.RuleWaitBreakout,
			why: "a breakout standing inside its own band has triggered; there is nothing " +
				"left to wait for",
		},
	}

	for _, c := range cases {
		got := entryplan.DecideStatus(c.in).Rule
		if got != c.earlier {
			t.Errorf("%s: arm %q, want the EARLIER arm %q. %s", c.name, got, c.earlier, c.why)
		}
		ie, ok1 := index[c.earlier]
		il, ok2 := index[c.also]
		if !ok1 || !ok2 {
			t.Fatalf("%s: one of %q/%q is not in AllDecisionRules", c.name, c.earlier, c.also)
		}
		if ie >= il {
			t.Errorf("%s: this row calls %q the earlier arm, and AllDecisionRules puts it at "+
				"%d against %q at %d — the table and the registry disagree about what the "+
				"order IS", c.name, c.earlier, ie, c.also, il)
		}
	}
}

// ── BEAR ≠ UNKNOWN, on EVERY execution path (spec §6, Q4) ─────────────────────────────

// The paired test, swept rather than sampled.
//
// The two regimes are run through the SAME inputs — every shape, every price relation, every
// way a number can be missing — and the requirement is absolute: BEAR never answers
// INSUFFICIENT_DATA where UNKNOWN would, UNKNOWN never answers NO_VALID_ENTRY, and the two
// never agree on a status at all. The line is inherited from model.Regime and
// analyzer.DecideRegime's R0, and this is the last layer that can lose it.
func TestBearAndUnknownStayApartOnEveryExecutionPath(t *testing.T) {
	prices := []float64{800, decZoneLow, 850, decZoneHigh, 866, decCeiling, 900}
	mutations := []struct {
		name string
		mut  func(*entryplan.DecisionInput)
	}{
		{"complete evidence", func(*entryplan.DecisionInput) {}},
		{"no band, gap", noZone(entryplan.ReasonZoneATRUnavailable)},
		{"no band, searched", noZone(entryplan.ReasonPullbackLevelNotBelowCurrentPrice)},
		{"no stop, gap", noStop(entryplan.ReasonInvalidationEvidenceUnavailable)},
		{"no stop, searched", noStop(entryplan.ReasonNoValidInvalidation)},
		{"no ceiling, gap", noChase(entryplan.ReasonPreviousCloseUnavailable)},
		{"no ceiling, by decision", noChase(entryplan.ReasonChaseNotAllowed)},
	}

	var checked int
	for _, sem := range []entryplan.EntrySemantic{
		entryplan.EntrySemanticPullback, entryplan.EntrySemanticBreakout,
	} {
		for _, price := range prices {
			for _, m := range mutations {
				bear := entryplan.DecideStatus(dec(model.RegimeBear, sem, price, m.mut))
				unknown := entryplan.DecideStatus(dec(model.RegimeUnknown, sem, price, m.mut))
				checked++

				if bear.Status != entryplan.StatusNoValidEntry {
					t.Errorf("BEAR/%s/%v/%s answered %q — BEAR is a DECISION on sufficient "+
						"evidence and must never borrow the state's output", sem, price,
						m.name, bear.Status)
				}
				if unknown.Status != entryplan.StatusInsufficientData {
					t.Errorf("UNKNOWN/%s/%v/%s answered %q — UNKNOWN is the ABSENCE of a "+
						"decision and must never be published as a verdict", sem, price,
						m.name, unknown.Status)
				}
				if bear.Status == unknown.Status {
					t.Errorf("BEAR and UNKNOWN agree (%q) at %s/%v/%s — the whole point of "+
						"EP-2's three-valued Allowance was that they cannot", bear.Status,
						sem, price, m.name)
				}
				if bear.Rule != entryplan.RulePolicyRefused ||
					unknown.Rule != entryplan.RulePolicyUnresolved {
					t.Errorf("at %s/%v/%s the arms were %q and %q, want S4 and S3 — the "+
						"policy arms must sit ahead of every price arm", sem, price, m.name,
						bear.Rule, unknown.Rule)
				}
			}
		}
	}
	if checked != len(prices)*len(mutations)*2 {
		t.Fatalf("the sweep ran %d comparisons, want %d", checked,
			len(prices)*len(mutations)*2)
	}
}

// ── every absence code has a READING (decide.go's ReadAbsence) ────────────────────────

// The rejection codes, ENUMERATED INDEPENDENTLY of ReadAbsence's switch, each with the reading
// it must get.
//
// Typed out rather than derived, because a list derived from the switch would agree with the
// switch by construction. The two entries worth reading twice are the ones where the criterion
// decides AGAINST the obvious reading, and they are marked.
var rejectionReadings = map[entryplan.Reason]entryplan.AbsenceReading{
	// ── the zone step
	entryplan.ReasonZoneSemanticNotPermitted:    entryplan.AbsenceSearched,
	entryplan.ReasonZonePolicyUnresolved:        entryplan.AbsenceEvidenceGap,
	entryplan.ReasonEntrySemanticUnavailable:    entryplan.AbsenceEvidenceGap,
	entryplan.ReasonPullbackLevelsUnavailable:   entryplan.AbsenceEvidenceGap,
	entryplan.ReasonPullbackLevelsBasisMismatch: entryplan.AbsenceEvidenceGap,
	// A VERDICT, against the obvious reading: every support was in hand and every one was
	// compared with the market. Nothing is missing; there is no band below the market.
	entryplan.ReasonPullbackLevelNotBelowCurrentPrice: entryplan.AbsenceSearched,
	entryplan.ReasonBreakoutLevelUnavailable:          entryplan.AbsenceEvidenceGap,
	entryplan.ReasonBreakoutLevelBasisMismatch:        entryplan.AbsenceEvidenceGap,
	// A GAP, against the obvious reading: the pivot handed to this package is not a price any
	// exchange quoted, which is a defect in the INPUT and not a fact about the stock.
	entryplan.ReasonBreakoutLevelNotAQuote:       entryplan.AbsenceEvidenceGap,
	entryplan.ReasonZoneATRUnavailable:           entryplan.AbsenceEvidenceGap,
	entryplan.ReasonZoneATRBasisMismatch:         entryplan.AbsenceEvidenceGap,
	entryplan.ReasonZoneATRPeriodMismatch:        entryplan.AbsenceEvidenceGap,
	entryplan.ReasonZoneTickAlignmentUnavailable: entryplan.AbsenceEvidenceGap,
	entryplan.ReasonZoneLowNotAPrice:             entryplan.AbsenceSearched,

	// ── the chase step. The two DECIDED absences are the whole content of "you may not
	// chase is not you may not wait".
	entryplan.ReasonChaseNotAllowed:               entryplan.AbsenceByDecision,
	entryplan.ReasonChaseCeilingBelowZone:         entryplan.AbsenceByDecision,
	entryplan.ReasonChasePolicyUnresolved:         entryplan.AbsenceEvidenceGap,
	entryplan.ReasonPreviousCloseUnavailable:      entryplan.AbsenceEvidenceGap,
	entryplan.ReasonPreviousCloseBasisMismatch:    entryplan.AbsenceEvidenceGap,
	entryplan.ReasonChaseLimitRequiresRawBasis:    entryplan.AbsenceEvidenceGap,
	entryplan.ReasonChaseLimitRuleUnavailable:     entryplan.AbsenceEvidenceGap,
	entryplan.ReasonChaseLimitPriceUnavailable:    entryplan.AbsenceEvidenceGap,
	entryplan.ReasonChaseTickAlignmentUnavailable: entryplan.AbsenceEvidenceGap,

	// ── the invalidation step, and this is the split EP-5 could not have guessed.
	entryplan.ReasonNoValidInvalidation:               entryplan.AbsenceSearched,
	entryplan.ReasonInvalidationEvidenceUnavailable:   entryplan.AbsenceEvidenceGap,
	entryplan.ReasonInvalidationSelectionInconsistent: entryplan.AbsenceEvidenceGap,
}

func TestEveryRejectionCodeIsClassified(t *testing.T) {
	for code, want := range rejectionReadings {
		got := entryplan.ReadAbsence(code)
		if got == entryplan.AbsenceUnclassified {
			t.Errorf("%q has no reading — an unclassified absence is treated as an evidence "+
				"gap by every caller, so a new rejection code would silently become "+
				"INSUFFICIENT_DATA whatever it actually meant", code)
			continue
		}
		if got != want {
			t.Errorf("%q reads as %q, want %q", code, got, want)
		}
		if !entryplan.KnownReason(code) {
			t.Errorf("%q is not in AllReasons", code)
		}
	}

	// A code that is NOT a rejection must NOT be classified: the table is a statement about
	// absences, and one that answered for every reason code would be answering for reasons
	// that are not about a missing number at all.
	for _, notARejection := range []entryplan.Reason{
		entryplan.ReasonSnapshotMalformed,
		entryplan.ReasonRecentPriceAdjustment,
		entryplan.ReasonStatusPriceInsideZone,
		entryplan.ReasonPlanInvariantViolated,
		entryplan.Reason("NOT_A_REASON"),
	} {
		if r := entryplan.ReadAbsence(notARejection); r != entryplan.AbsenceUnclassified {
			t.Errorf("%q reads as %q — it is not a rejection code and classifying it invites "+
				"a caller to route a status on it", notARejection, r)
		}
	}

	// ANTI-VACUITY, and it is what stops this table from lagging production: every rejection
	// code the real pipeline actually writes onto a trace must be IN the table above. A code
	// added upstream fails here rather than defaulting into "insufficient data".
	var traces int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.EntryTrace == nil {
			continue
		}
		traces++
		for _, seen := range []entryplan.Reason{
			p.EntryTrace.Zone.Rejection,
			p.EntryTrace.Chase.Rejection,
			p.EntryTrace.Stop.Rejection,
		} {
			if seen == "" {
				continue
			}
			if _, ok := rejectionReadings[seen]; !ok {
				t.Errorf("%s: production wrote rejection %q onto a trace and this table does "+
					"not list it — ReadAbsence would answer UNCLASSIFIED and the decision "+
					"would call it a gap without anybody deciding that", c.name, seen)
			}
		}
	}
	if traces == 0 {
		t.Fatal("the sweep produced no traces — the anti-vacuity half never ran")
	}
	// And the readings registry itself is pinned from the production side.
	for _, r := range entryplan.AllAbsenceReadings {
		if r == "" {
			t.Error("an empty member of AllAbsenceReadings")
		}
	}
}

// ── purity and totality ───────────────────────────────────────────────────────────────

// DecideStatus is PURE and TOTAL: same input, same answer, and it never writes through a
// pointer it was handed.
//
// The archived verdict is only re-derivable if this holds, and the pointer half matters because
// DecisionInput carries FOUR pointers straight off the Plan — a decision that wrote through one
// of them would edit the very numbers the report is about to show.
func TestDecideStatusIsPureAndLeavesItsArgumentAlone(t *testing.T) {
	for _, c := range decisionCases() {
		before := snapshotOfInput(c.in)
		first := entryplan.DecideStatus(c.in)
		// Mutating the ANSWER must not reach the next call's answer.
		for n := range first.Reasons {
			first.Reasons[n] = "MUTATED"
		}
		second := entryplan.DecideStatus(c.in)
		for _, r := range second.Reasons {
			if r == "MUTATED" {
				t.Fatalf("%s: the second call returned a reason the caller wrote into the "+
					"first — the two results share backing memory", c.name)
			}
		}
		if second.Rule != first.Rule && second.Rule == "" {
			t.Fatalf("%s: the second call reached a different arm", c.name)
		}
		if after := snapshotOfInput(c.in); after != before {
			t.Errorf("%s: the input changed across the call:\n  before %+v\n  after  %+v",
				c.name, before, after)
		}
	}
}

// inputFingerprint is the values behind DecisionInput's pointers, flattened so a change written
// THROUGH a pointer is visible to a comparison. Comparing the struct itself would compare the
// pointers, which are equal whatever they point at.
type inputFingerprint struct {
	cur, curOK       float64
	zoneLow, zoneHi  float64
	hasZone, hasStop bool
	chase            float64
	hasChase         bool
	stop             float64
}

func snapshotOfInput(in entryplan.DecisionInput) inputFingerprint {
	out := inputFingerprint{}
	if in.CurrentPrice != nil {
		out.cur, out.curOK = *in.CurrentPrice, 1
	}
	if in.Zone != nil {
		out.hasZone = true
		out.zoneLow, out.zoneHi = in.Zone.Low, in.Zone.High
	}
	if in.MaxChasePrice != nil {
		out.hasChase, out.chase = true, *in.MaxChasePrice
	}
	if in.Invalidation != nil {
		out.hasStop, out.stop = true, in.Invalidation.Price
	}
	// NaN never equals itself, which would make every comparison of this struct fail on a row
	// that plants one — turning a purity assertion into a guaranteed failure that says nothing
	// about purity. The planted NaNs are normalised to a sentinel, so "the value was a NaN
	// before and after" compares equal while any OTHER change still shows.
	for _, fl := range []*float64{&out.cur, &out.chase, &out.stop, &out.zoneLow, &out.zoneHi} {
		if *fl != *fl {
			*fl = -1
		}
	}
	return out
}

// ── the four things that must NOT gate a status (spec §15, §16, §26, §27) ─────────────

// decisionInputAllowlist is the EXACT exported field set DecideStatus is allowed to see, with
// ONE SENTENCE PER FIELD saying why the decision layer needs it.
//
// # An ALLOWLIST, not a blacklist, and that changed after EP-5
//
// This test used to name the eight fields that must never appear, which caught exactly the eight
// mistakes somebody had already thought of. It did not catch `ATR14`, `Tick`, `PreviousClose`,
// `MA20`, `MA60`, `BaseLow` or `PivotHigh` — every one of which is a price-CONSTRUCTION input
// that would let this layer re-derive a level it is supposed to consume, and every one of which
// could have landed silently. The set is now pinned from BOTH ends: a missing field fails, and
// an UNEXPECTED field fails even when production does not read it yet. Adding one costs an edit
// to this list, which is where the "does the decision layer actually depend on this?" question
// gets asked out loud.
//
// # The criterion is DOMAIN DEPENDENCY, not smallness
//
// The three *Absence codes are the clearest case. They are not price data and they are not
// decoration: EP-5's whole job is to tell "we could not look" (INSUFFICIENT_DATA, a state) from
// "we looked and there is no legal price" (NO_VALID_ENTRY, a verdict), and a nil pointer alone
// cannot carry that difference. Availability/absence metadata is therefore WELCOME here. What is
// refused is the material a price could be REBUILT from, and the numbers that grade a plan after
// it has been built.
var decisionInputAllowlist = map[string]string{
	"Semantic": "WHICH SHAPE of entry is proposed. The arms are semantic-specific — WAIT_PULLBACK " +
		"and WAIT_BREAKOUT are different verdicts about the same price — so without it there is " +
		"no way to say what the plan is waiting FOR.",
	"Policy": "EP-2's answer for (regime, semantic): the allowance, the requirements, and the " +
		"BuyNow/Chase permissions. It is the PERMISSION half of every status; re-deriving it " +
		"here would be a second regime rule.",
	"CurrentPrice": "the market. Every arm is a comparison against it (inside the band, above " +
		"the ceiling, below the band), so it is the one price this layer genuinely needs.",
	"Zone": "the PUBLISHED entry band. Consumed, never recomputed — it is the thing BUY_NOW and " +
		"WAIT are statements about.",
	"ZoneAbsence": "WHY there is no band. Distinguishes 'the ATR was missing' (a state) from " +
		"'no legal entry level exists' (a verdict); reading Zone == nil alone collapses them.",
	"MaxChasePrice": "the PUBLISHED ceiling. TOO_EXTENDED is defined as price above it, and the " +
		"ceiling is the only number that makes that sentence true rather than an opinion.",
	"ChaseAbsence": "WHY there is no ceiling. ComputeMaxChase refuses to merge three different " +
		"nils and this is where they are read back apart, so a missing previous close does not " +
		"become 'chasing is forbidden'.",
	"Invalidation": "the PUBLISHED thesis-failure level. Its presence is what makes a plan " +
		"executable — a status that says 'buy now' beside no statable failure point is not a " +
		"trade plan — and the layer reads its existence, not its arithmetic.",
	"StopAbsence": "WHY there is no invalidation. The EP-5 split (evidence gap → INSUFFICIENT_DATA, " +
		"completed search → NO_VALID_ENTRY) is decided from this code and from nothing else.",
	"Thesis": "EP-6D, the user's one protection: whether the scanner's primary Action " +
		"CONTRADICTS the entry thesis (SELL / REDUCE / TAKE PROFIT / STOP LOSS). Read by S1 (CONTRADICTED → " +
		"NO_VALID_ENTRY) and by the two BUY_NOW arms (unclassified → not BUY_NOW). A category the adapter already classified — " +
		"no price can be rebuilt from it and it grades nothing.",
}

// decisionInputForbidden is the OLD blacklist, KEPT rather than deleted.
//
// The allowlist above already rejects every one of these names — an unexpected field is a
// failure whatever it is called — so this map adds no coverage. What it adds is the REASON:
// each entry cites the spec section that argued the field out, and a maintainer who hits the
// generic "unexpected field" message on `RiskReward` deserves to be told that §15 decided this,
// not to go looking for the discussion. It is checked FIRST so the specific message wins.
var decisionInputForbidden = map[string]string{
	"RiskReward":    "spec §15: a ratio threshold is a claim about outcomes and this repo has no backtest behind one for THIS construction",
	"Ratio":         "spec §15",
	"Target1":       "spec §16: an absent target is normal and says nothing about whether the entry may be taken",
	"Target2":       "spec §16",
	"AdjustmentAge": "spec §26: a caveat, never a gate",
	"Confidence":    "spec §27: 'how much evidence' and 'what does the plan say' are different axes",
	"Valuation":     "spec §16: the valuation ceiling is optional and is not a permission",
	"Score":         "the scanner's grade is an input to WHETHER to own, not to WHERE to buy",
}

// The strongest form of the claim: THE FIELD SET IS EXACTLY THIS, IN BOTH DIRECTIONS.
//
// A test that asserted "BUY_NOW survives a bad R:R" would pass while the coupling was written
// somewhere else. The blacklist version of this test said the coupling is UNWRITABLE for eight
// named fields; this one says the INPUT IS CLOSED — DecisionInput carries these ten fields and
// nothing else, so a tenth cannot arrive without a maintainer answering, in decisionInputAllowlist,
// why the decision layer depends on it.
func TestTheDecisionCannotSeeWhatMustNotGateIt(t *testing.T) {
	ty := reflect.TypeOf(entryplan.DecisionInput{})

	// ANTI-VACUITY, before anything else: a struct with no fields would make both directions
	// of the comparison below trivially satisfiable in one direction and silently wrong.
	if ty.NumField() == 0 {
		t.Fatal("DecisionInput has no fields at all — the comparison below asserted nothing")
	}

	got := map[string]bool{}
	var inOrder []string
	for n := 0; n < ty.NumField(); n++ {
		fl := ty.Field(n)
		if fl.PkgPath != "" {
			// Unexported: not part of the contract a caller can populate, and not something a
			// review of this package's API would see. DecisionInput has none today.
			continue
		}
		got[fl.Name] = true
		inOrder = append(inOrder, fl.Name)
	}

	// 1. UNEXPECTED fields, walked in DECLARATION order rather than over the map, so two new
	// fields produce the same two messages in the same order on every run. The named ones are
	// reported first, because their message carries the argument.
	for _, name := range inOrder {
		if why, bad := decisionInputForbidden[name]; bad {
			t.Errorf("DecisionInput carries %q — %s", name, why)
			continue
		}
		if _, ok := decisionInputAllowlist[name]; !ok {
			t.Errorf("unexpected DecisionInput field %q. This layer's input is a CLOSED "+
				"projection: it may hold the OUTPUTS of steps that already ran plus the codes "+
				"that explain an absent one, and nothing a price could be rebuilt from or that "+
				"grades a plan after the fact. Decide which one this is:\n"+
				"  (A) the decision layer really does depend on %q — then add it to "+
				"decisionInputAllowlist WITH the sentence saying why, and update the "+
				"architecture contract in decide.go's DecisionInput doc; or\n"+
				"  (B) it does not — then remove the field. A price-construction input (an ATR, "+
				"a tick, a moving average, a pivot, a previous close) is case (B) by "+
				"construction: this layer consumes levels, it does not build them.\n"+
				"It may NOT arrive silently, and 'production does not read it yet' is not an "+
				"exemption — the field is what makes the coupling writable.", name, name)
		}
	}

	// 2. MISSING fields. The other direction, so nobody can satisfy this test by deleting a
	// field the decision genuinely needs — dropping StopAbsence, say, would make the
	// state/verdict split undecidable while every arm still compiled. SORTED for the same
	// reason the walk above is ordered.
	want := make([]string, 0, len(decisionInputAllowlist))
	for name := range decisionInputAllowlist {
		want = append(want, name)
	}
	sort.Strings(want)
	for _, name := range want {
		if !got[name] {
			t.Errorf("DecisionInput no longer carries %q, which the contract says this layer "+
				"needs: %s", name, decisionInputAllowlist[name])
		}
	}

	// 3. The ANSWER's closure used to live here as a SHAPE check (no float64, no *float64).
	// That had a blind spot the EP-6 brief named: `Confidence Confidence` — a string-typed
	// field — sailed through it. It is now an exact allowlist of its own, below.
}

// decisionResultAllowlist is the EXACT field set of DecisionResult, with the reason each field
// is part of the decision layer's OUTPUT contract. EP-6D.
//
// # Why an allowlist and not the shape check it replaces
//
// The previous guard refused float-shaped fields, which catches "the decision publishes a
// price" and nothing else. A `Confidence Confidence`, a `Score int`, a `PositionSize string` or
// a `Rank uint8` would all have passed it, and every one of them is a second channel out of a
// layer whose whole contract is ONE status, ONE arm, and the codes that defend them. A consumer
// that finds a Confidence on the decision will gate on it, and then the thing EP-5's doc calls
// "different axes" has been collapsed by whoever reads the struct next.
//
// # What is compared
//
// EVERY field, exported and unexported — stricter than decisionInputAllowlist, which skips
// unexported ones. An unexported field cannot be set by a caller, but it can be written by
// DecideStatus and read by ComputePlan in this same package, which is exactly the channel this
// guard exists to close.
var decisionResultAllowlist = map[string]string{
	"Status": "the verdict — the one thing this layer exists to publish",
	"Rule": "the arm that produced it; persisted so an archived verdict is re-derivable " +
		"(see DecisionRule)",
	"Reasons": "the codes that defend the verdict; InvariantStatusHasReason requires at least one",
}

// THE DECISION'S OUTPUT IS CLOSED, IN BOTH DIRECTIONS. EP-6D, spec item "DecisionResult shape
// hardening".
//
// Structural: this proves the TYPE carries exactly these fields. It does not prove how any of
// them is computed — the decision matrix does that.
func TestTheDecisionResultFieldSetIsExactlyTheAllowlist(t *testing.T) {
	rt := reflect.TypeOf(entryplan.DecisionResult{})

	// ANTI-VACUITY: a zero-field type, or an emptied allowlist, would make one direction of
	// the comparison trivially true.
	if rt.NumField() == 0 {
		t.Fatal("DecisionResult has no fields — the comparison below asserts nothing")
	}
	if len(decisionResultAllowlist) != 3 {
		t.Fatalf("decisionResultAllowlist has %d entries, want the 3 this test was written "+
			"against — editing the allowlist is the architecture change, so its size is pinned "+
			"too", len(decisionResultAllowlist))
	}

	got := map[string]bool{}
	for n := 0; n < rt.NumField(); n++ {
		fl := rt.Field(n)
		got[fl.Name] = true
		if _, ok := decisionResultAllowlist[fl.Name]; !ok {
			t.Errorf("unexpected DecisionResult field %q (type %s). The decision layer "+
				"publishes ONE status, the arm that produced it and the codes that defend it. "+
				"A new field is a new OUTPUT CHANNEL — a confidence, a score, a size, a rank — "+
				"and the first consumer to read it will gate on it, collapsing the axes EP-5 "+
				"keeps apart. If the decision layer genuinely must publish it, add it to "+
				"decisionResultAllowlist WITH the sentence saying why and update DecisionResult's "+
				"doc; otherwise remove the field.", fl.Name, fl.Type)
		}
		if fl.Type.Kind() == reflect.Float64 ||
			(fl.Type.Kind() == reflect.Ptr && fl.Type.Elem().Kind() == reflect.Float64) {
			t.Errorf("DecisionResult carries a price-shaped field %q — the decision layer "+
				"publishes a verdict, not a number", fl.Name)
		}
	}

	want := make([]string, 0, len(decisionResultAllowlist))
	for name := range decisionResultAllowlist {
		want = append(want, name)
	}
	sort.Strings(want)
	var matched int
	for _, name := range want {
		if !got[name] {
			t.Errorf("DecisionResult no longer carries %q, which the contract says it "+
				"publishes: %s", name, decisionResultAllowlist[name])
			continue
		}
		matched++
	}
	if matched != rt.NumField() {
		t.Errorf("matched %d allowlisted fields against %d declared ones", matched, rt.NumField())
	}
}

// TOO_EXTENDED KEEPS THE WHOLE PLAN (spec §12, Q5).
//
// "Too expensive here" is only actionable beside "and this is where it would not be". Asserted
// over the real pipeline rather than over the decision alone, because the field that could be
// emptied is on the Plan and the emptying would happen in ComputePlan or in the gate.
func TestTooExtendedKeepsEveryPriceThePlanComputed(t *testing.T) {
	var seen int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.Status != entryplan.StatusTooExtended {
			continue
		}
		seen++
		if p.IdealEntry == nil {
			t.Errorf("%s: TOO_EXTENDED with no entry band — the reader has been told the "+
				"useless half of the sentence", c.name)
		}
		if p.MaxChasePrice == nil {
			t.Errorf("%s: TOO_EXTENDED with no ceiling — the ceiling is the only number that "+
				"makes 'past what this plan would pay' a true statement", c.name)
		}
		if p.Invalidation == nil {
			t.Errorf("%s: TOO_EXTENDED dropped the invalidation", c.name)
		}
		if p.Target1 == nil {
			t.Errorf("%s: TOO_EXTENDED dropped the first target", c.name)
		}
	}
	if seen == 0 {
		t.Fatal("no snapshot in the sweep produced TOO_EXTENDED — this test never ran its " +
			"assertion")
	}
}

// EVERY status carries at least one reason, over the WHOLE pipeline.
//
// The invariant checker enforces this on a Plan; this is the same claim one layer earlier, on
// the decision itself, so "the arm published nothing and the gate repaired it" and "the arm
// published a code" stay different outcomes.
func TestEveryDecidedPlanCarriesTheArmAndItsReasons(t *testing.T) {
	statuses := map[entryplan.EntryStatus]int{}
	arms := map[entryplan.DecisionRule]int{}
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.EntryTrace == nil {
			// Malformed: ComputePlan refuses before any rule runs.
			continue
		}
		d := p.EntryTrace.Decision
		statuses[d.Status]++
		arms[d.Rule]++
		if len(d.Reasons) == 0 {
			t.Fatalf("%s: the decision trace records arm %q with no reason", c.name, d.Rule)
		}
		if !entryplan.KnownDecisionRule(d.Rule) {
			t.Fatalf("%s: the decision trace records arm %q, which is not in the registry",
				c.name, d.Rule)
		}
		if !d.Status.Valid() {
			t.Fatalf("%s: the decision trace records status %q", c.name, d.Status)
		}
		for _, r := range d.Reasons {
			if !hasReason(p, r) {
				t.Fatalf("%s: the decision published %q and the plan does not carry it — the "+
					"trace and the row a reader sees disagree", c.name, r)
			}
		}
	}
	// ANTI-VACUITY, and a census worth reading: the sweep must reach more than one status and
	// more than a couple of arms, or the assertions above are about one input class.
	if len(statuses) < 4 {
		t.Errorf("the sweep produced only %d distinct statuses (%v) — the properties above "+
			"are being asserted over one kind of plan", len(statuses), statuses)
	}
	if len(arms) < 8 {
		t.Errorf("the sweep reached only %d distinct arms (%v)", len(arms), arms)
	}
}

// THE PUBLISHED STATUS IS THE DECISION'S, AND THE ONE EXCEPTION IS RECORDED.
//
// Plan.Status is what a reader acts on; EntryTrace.Decision.Status is what the rule said. They
// may differ in EXACTLY ONE case — the invariant gate found the verdict contradicting the plan's
// own numbers and sent it back to INSUFFICIENT_DATA — and that case is stamped STATUS_WITHDRAWN.
//
// Without this, ComputePlan could re-decide the status AFTER the decision layer ran, on evidence
// the decision layer is not allowed to see. That is not a hypothetical shape: every gate spec
// §15/§16/§26 forbids (an R:R threshold, a missing second target, a recent adjustment, an
// upstream OVERHEATED flag aliased straight through) is written most naturally exactly there —
// one line below `p.Status = decision.Status`, where all four of those values ARE in scope. The
// DecisionInput projection makes them unwritable inside decide.go; this test is what makes them
// unwritable outside it.
func TestThePublishedStatusIsTheDecisionsUnlessTheGateWithdrewIt(t *testing.T) {
	var agreed, withdrawn int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		if p.EntryTrace == nil {
			continue
		}
		decided := p.EntryTrace.Decision.Status
		switch p.EntryTrace.InvariantCheck {
		case entryplan.InvariantCheckStatusWithdrawn:
			withdrawn++
			if p.Status != entryplan.StatusInsufficientData {
				t.Fatalf("%s: the gate withdrew the verdict and published %q", c.name, p.Status)
			}
		default:
			agreed++
			if p.Status != decided {
				t.Fatalf("%s: the plan publishes %q and the decision said %q, with the gate "+
					"stamped %q — something re-decided the status after the decision layer "+
					"ran, on evidence that layer is not allowed to see. Reasons: %v",
					c.name, p.Status, decided, p.EntryTrace.InvariantCheck, p.Reasons)
			}
		}
	}
	if agreed == 0 {
		t.Fatal("no well-formed plan reached the comparison — this test never ran")
	}
	// The withdrawal branch is EXPECTED to be empty over production inputs: reaching it would
	// mean this package contradicted itself. It is exercised directly, on a planted plan, by
	// TestTheGateWithdrawsAContradictedVerdictAndKeepsThePrices.
	if withdrawn != 0 {
		t.Errorf("%d plans had their verdict withdrawn by the gate — every one of them is a "+
			"bug in this package, not a market state", withdrawn)
	}
}
