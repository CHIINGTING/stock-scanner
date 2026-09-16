package entryplan_test

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── the evidence census ───────────────────────────────────────────────────────────────
//
// plan.go states that "the evidence census, the reason codes and the confidence contract are
// the actual deliverable" of EP-1. The reason codes are pinned from both ends by
// TestReasonRegistryIsExactlyWhatProductionEmits and the confidence contract by
// TestMissingEvidenceIsNotLowConfidence. This file is the census's half of that sentence, and
// it did not exist until a review found that ANY required evidence row could be deleted from
// ComputePlan with the whole suite still green:
//
//	M4  ATR row not appended when unavailable            SURVIVED
//	M5  PRICE_BASIS row not appended when unavailable    SURVIVED
//	M7  MARKET_REGIME row never appended at all          SURVIVED  ← a REQUIRED item, deletable
//	b2  an UNAVAILABLE row carrying Value: &0.0          SURVIVED
//
// The old ATR check iterated the evidence looking for a key and asserted inside the `if`, so
// deleting the row made the loop empty and the assertion vacuous — green for the absence of
// the very thing it was there to inspect. Everything below is therefore written as an
// assertion about the WHOLE census (an exact key sequence) rather than about a row that has
// to be found first.

// The census EP-1 owes for every well-formed snapshot, in the order ComputePlan emits it.
//
// Split required/optional to match plan.go's own two sections, and ORDERED because the order
// is a claim the code makes: PRICE_BASIS comes first "because it decides what the other price
// evidence even means", and a basis row arriving after the price it labels would leave a
// reader of an archived plan interpreting the price before it knows the series.
//
// EXACT, not "at least". A census that may quietly grow is a census a later item can quietly
// shrink; when EP-3 adds a row it edits this list, which is the point at which someone decides
// whether the new row is required or corroborating.
var (
	requiredEvidenceKeys = []string{
		entryplan.EvidencePriceBasis,
		entryplan.EvidenceCurrentPrice,
		entryplan.EvidenceMarketRegime,
		// EP-2. Required item #4, which under EP-1 was counted and had no field to be
		// counted FROM — see plan.go's step 4.
		entryplan.EvidenceEntrySemantic,
	}
	optionalEvidenceKeys = []string{
		entryplan.EvidenceATR,
		entryplan.EvidenceAdjustmentAge,
	}
	// EP-3b's six rows: RECORDED, AND NOT COUNTED BY THE CONFIDENCE CENSUS.
	//
	// A THIRD CLASS, and the reason it is not simply appended to the optional list is that
	// the two lists above are what ClassifyConfidence counts. Adding six items to the
	// corroborating side would re-grade every stock that has no base low from HIGH to
	// MEDIUM, with nothing about the stock or the plan having changed — and a grade that
	// moves because a rule item was added is a grade nobody can compare across two dates.
	// Re-scaling confidence needs its own work item; plan.go says so and
	// TestTheConfidenceCensusIsStillTheOneEP2Shipped holds the counts.
	tracedEvidenceKeys = []string{
		entryplan.EvidenceLevelMA20,
		entryplan.EvidenceLevelMA60,
		entryplan.EvidenceLevelBaseLow,
		entryplan.EvidenceLevelPivotHigh,
		entryplan.EvidencePreviousClose,
		entryplan.EvidenceAvgVolume20,
		// EP-4's three, on the same side of the census and for the same reason. Note that
		// BASE_LOW_KIND is a row ABOUT another row: it says whether LEVEL_BASE_LOW holds a
		// consolidation base low or today's single-bar low, which is the fact that decides
		// whether the base low may be an invalidation. It is a separate key rather than more
		// text on LEVEL_BASE_LOW because that row's Text already carries the level's
		// disposition.
		entryplan.EvidenceBaseLowKind,
		entryplan.EvidenceValuationBaseTarget,
		entryplan.EvidenceValuationSuitability,
	}
)

// Which rows carry their reading as a NUMBER and which as TEXT. The distinction is what makes
// types.go's "a nil Value with an AVAILABLE status is a bug, not a zero" checkable: it is a bug
// for CURRENT_PRICE and it is normal for MARKET_REGIME, whose reading is a word.
var (
	numericEvidenceKeys = map[string]bool{
		entryplan.EvidenceCurrentPrice:  true,
		entryplan.EvidenceATR:           true,
		entryplan.EvidenceAdjustmentAge: true,
		// EP-3b. The four level rows carry a NUMBER (the observed level) and, as Text, the
		// LevelDisposition that says what happened to it — the two together are what make
		// "MA20 was 102.4 and above the market" readable off an archived row. They are
		// numeric rows that also carry a label, which is why the text-row rules below (no
		// value) do not apply to them.
		entryplan.EvidenceLevelMA20:      true,
		entryplan.EvidenceLevelMA60:      true,
		entryplan.EvidenceLevelBaseLow:   true,
		entryplan.EvidenceLevelPivotHigh: true,
		entryplan.EvidencePreviousClose:  true,
		entryplan.EvidenceAvgVolume20:    true,
		// EP-4. The valuation TARGET is a price; the SUITABILITY and the base low's KIND are
		// categories, and a consumer that computed with either would be doing arithmetic on
		// a word.
		entryplan.EvidenceValuationBaseTarget: true,
	}
	textEvidenceKeys = map[string]bool{
		entryplan.EvidencePriceBasis:           true,
		entryplan.EvidenceMarketRegime:         true,
		entryplan.EvidenceEntrySemantic:        true,
		entryplan.EvidenceBaseLowKind:          true,
		entryplan.EvidenceValuationSuitability: true,
	}
)

func censusKeys() []string {
	out := make([]string, 0,
		len(requiredEvidenceKeys)+len(optionalEvidenceKeys)+len(tracedEvidenceKeys))
	out = append(out, requiredEvidenceKeys...)
	out = append(out, optionalEvidenceKeys...)
	out = append(out, tracedEvidenceKeys...)
	return out
}

// The three lists above and production's own AllEvidenceKeys registry must be THE SAME LIST,
// in the same order.
//
// Without this, the census test asserts against a list retyped in the test file, and a key
// renamed in both places at once passes — which is precisely the shape of the RuleVersion
// defect the EP-1 review found ("every version assertion compared the constant to itself").
// The literal VALUES are pinned separately, in golden_test.go.
func TestTheTestsCensusListIsProductionsRegistry(t *testing.T) {
	if !sameKeys(censusKeys(), entryplan.AllEvidenceKeys) {
		t.Fatalf("the census lists in this file are %v and AllEvidenceKeys is %v — one of "+
			"them has a key the other does not, or they are in a different order, and the "+
			"order is part of the contract (PRICE_BASIS first: it decides what the other "+
			"price evidence means)", censusKeys(), entryplan.AllEvidenceKeys)
	}
	for _, k := range censusKeys() {
		if !entryplan.KnownEvidenceKey(k) {
			t.Errorf("%q is not in the production registry", k)
		}
	}
	if entryplan.KnownEvidenceKey("NOT_AN_EVIDENCE_KEY") {
		t.Error("KnownEvidenceKey accepts a key that is not in the registry")
	}
}

func evidenceKeysOf(p entryplan.Plan) []string {
	out := make([]string, 0, len(p.Evidence))
	for _, e := range p.Evidence {
		out = append(out, e.Key)
	}
	return out
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

// TestEvidenceCensusIsExactAndAbsenceIsRecorded is the census contract, over the full matrix.
//
// Three properties, none of which can be satisfied by a row that is missing:
//
//  1. EXACT SET. Every well-formed snapshot produces exactly the six rows above, in order —
//     no omission (M4/M5/M6/M7), no duplicate, no extra.
//  2. ABSENCE IS A FACT. A row whose evidence could not be obtained is still emitted, with a
//     non-AVAILABLE status. types.go says "an item recording UNAVAILABLE is itself a fact
//     worth persisting"; until now nothing made that true. It matters because the alternative
//     encoding — drop the row — is indistinguishable at the consumer from "this version of
//     the rules never looked", which is a different sentence.
//  3. MISSING ≠ ZERO, ONE LEVEL DOWN. Value != nil implies Status == AVAILABLE. The package's
//     signature property was enforced on Plan's top-level price fields only
//     (the price sweep reflects over Plan's fields and never descends into
//     Plan.Evidence), so a 0.0 attached to an UNAVAILABLE row passed every existing guard:
//     the reflective sweep saw a finite float and the aliasing check only rejects negatives
//     and NaN. A reader that trusts Value without reading Status then prices a stock at 0.
func TestEvidenceCensusIsExactAndAbsenceIsRecorded(t *testing.T) {
	want := censusKeys()

	var wellFormed, malformed, rows int
	// Per key, how many times it was seen in each state — the anti-vacuity ledger. A census
	// test that only ever ran against fully-available fixtures would assert nothing about
	// absence, which is the case that broke.
	available := map[string]int{}
	absent := map[string]int{}

	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)

		// A snapshot that is not a stock gets no per-item diagnosis at all: there is nothing
		// for the evidence to be ABOUT. Asserted here so "no evidence" stays a deliberate
		// answer for exactly one input class rather than a state any bug can reach.
		if !c.in.WellFormed() {
			malformed++
			if len(p.Evidence) != 0 {
				t.Fatalf("%s: malformed snapshot produced evidence %v — a caller bug was "+
					"dressed up as a market condition", c.name, evidenceKeysOf(p))
			}
			continue
		}
		wellFormed++

		if got := evidenceKeysOf(p); !sameKeys(got, want) {
			t.Fatalf("%s: evidence census = %v, want exactly %v (in order)\n"+
				"a required row that can be deleted from ComputePlan with the suite green is "+
				"not evidence, it is a comment", c.name, got, want)
		}

		for _, e := range p.Evidence {
			rows++

			switch e.Status {
			case entryplan.Available:
				available[e.Key]++
			case entryplan.Unavailable, entryplan.InsufficientData:
				absent[e.Key]++
			default:
				t.Fatalf("%s: evidence %q status = %q — the zero Availability is not a "+
					"status, it is a row nobody filled in", c.name, e.Key, e.Status)
			}

			if e.Source == "" {
				t.Fatalf("%s: evidence %q names no Source — an archived plan cannot be "+
					"joined back to what it was computed from", c.name, e.Key)
			}

			// (3) MISSING ≠ ZERO. The invariant types.go only stated in the forward
			// direction, now held in the reverse one too.
			if e.Value != nil && e.Status != entryplan.Available {
				t.Fatalf("%s: evidence %q has status %q and still carries Value = %v — a "+
					"number on a row that says it has none. MISSING ≠ ZERO applies to the "+
					"evidence rows, not only to Plan's top-level price fields",
					c.name, e.Key, e.Status, *e.Value)
			}

			if e.Status == entryplan.Available {
				if numericEvidenceKeys[e.Key] && e.Value == nil {
					t.Fatalf("%s: evidence %q is AVAILABLE with a nil Value — types.go: "+
						"that is a bug, not a zero", c.name, e.Key)
				}
				if textEvidenceKeys[e.Key] {
					if e.Text == "" {
						t.Fatalf("%s: evidence %q is AVAILABLE and says nothing", c.name, e.Key)
					}
					if e.Value != nil {
						t.Fatalf("%s: evidence %q is a text reading and carries the number "+
							"%v — a consumer would compute with a category", c.name, e.Key, *e.Value)
					}
				}
			}
		}
	}

	// The ledger. Every row must have been observed BOTH present and absent somewhere in the
	// matrix, or the corresponding half of this test never ran.
	if wellFormed == 0 || malformed == 0 {
		t.Fatalf("the matrix covered %d well-formed and %d malformed snapshots — one branch "+
			"of this test never executed", wellFormed, malformed)
	}
	if rows != wellFormed*len(want) {
		t.Fatalf("inspected %d evidence rows over %d plans, want %d", rows, wellFormed, wellFormed*len(want))
	}
	for _, k := range want {
		if available[k] == 0 {
			t.Errorf("evidence %q was never AVAILABLE anywhere in the matrix — the "+
				"present-value assertions above never ran for it", k)
		}
		if absent[k] == 0 {
			t.Errorf("evidence %q was never recorded as absent anywhere in the matrix — "+
				"'an item recording UNAVAILABLE is itself a fact worth persisting' is "+
				"untested for it", k)
		}
	}
}

// The required/optional split is not decoration: it is what the confidence census counts, and
// counting an item on the wrong side changes the grade. ATR sits on the optional side because
// ATREvidence says requiring it is a policy owned by the item that multiplies by it.
//
// # This test could not fail under EP-1, and now it can
//
// The previous version of this comment recorded exactly why. EP-1's fourth required item — "an
// entry evaluation" — was counted and could never be present, so EVERY plan graded
// INSUFFICIENT_DATA and "moving ATR to the required side would turn the grade INSUFFICIENT"
// was false: nothing downstream could tell the two censuses apart, and writing the assertion
// anyway would have been a test that passes because it can only pass.
//
// EP-2 filled that slot with the entry SEMANTIC, which a caller can actually supply. A
// complete snapshot now grades LOW with no corroboration and HIGH with all of it, so the two
// sides of the split have observably different effects and the assertion below is real:
//
//	ATR on the OPTIONAL side  →  no-optional = LOW,               all-optional = HIGH
//	ATR on the REQUIRED side  →  no-optional = INSUFFICIENT_DATA  (the floor is not cleared)
//
// and it is checked in both directions — the optional rows must move the CONFIDENCE, and they
// must not move the STATUS, the REASONS or the census keys.
func TestOptionalEvidenceIsCountedAsOptional(t *testing.T) {
	if len(requiredEvidenceKeys) == 0 || len(optionalEvidenceKeys) == 0 {
		t.Fatal("the census has collapsed to one class")
	}
	for _, k := range optionalEvidenceKeys {
		if numericEvidenceKeys[k] == textEvidenceKeys[k] {
			t.Errorf("evidence %q is classified as neither numeric nor text, or as both", k)
		}
	}

	// A snapshot with every REQUIRED item present and nothing corroborating it.
	base := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice: entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)},
		Regime:       entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
	}
	withNoOptional := entryplan.ComputePlan(base)
	base.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
		PriceBasis: entryplan.PriceBasisRaw}
	base.AdjustmentAge = entryplan.AdjustmentAge{BarsAgo: i(63)}
	withAllOptional := entryplan.ComputePlan(base)

	// The required floor IS cleared without any optional evidence. This is the assertion that
	// fails if ATR or the adjustment age is moved to the required side.
	if withNoOptional.Confidence != entryplan.ConfidenceLow {
		t.Errorf("a snapshot holding every required item and nothing else graded %q, want LOW "+
			"— an OPTIONAL row is being counted as required, so a plan that rests on the "+
			"minimum is being reported as having no evidence at all", withNoOptional.Confidence)
	}
	if withAllOptional.Confidence != entryplan.ConfidenceHigh {
		t.Errorf("a fully corroborated snapshot graded %q, want HIGH", withAllOptional.Confidence)
	}
	// And the optional rows must not decide anything ELSE.
	if withNoOptional.Status != withAllOptional.Status {
		t.Errorf("optional evidence changed the status: %q → %q",
			withNoOptional.Status, withAllOptional.Status)
	}
	// NOTE WHAT IS NOT ASSERTED HERE ANY MORE. Until EP-3b this test also required the
	// REASONS to be identical with and without the corroborating evidence. That claim is
	// false now and it would have been a test passing for an accident: ATR is the zone
	// width's input, so on a snapshot that HAS levels, removing it changes the reason list
	// from "no zone, and here is why" to "no zone for a different reason". The base fixture
	// here carries no levels, so the reason list happens not to move — which is exactly the
	// kind of green nobody should trust.
	//
	// The real contract is asserted where it belongs, in
	// TestATRIsRequiredByTheWidthRuleAndByNothingElse: the missing ATR withdraws the ZONE and
	// names itself, and it does not move the GRADE across the availability floor.
	if withNoOptional.Status != withAllOptional.Status {
		t.Errorf("optional evidence changed the status: %q → %q", withNoOptional.Status,
			withAllOptional.Status)
	}
	if !sameKeys(evidenceKeysOf(withNoOptional), evidenceKeysOf(withAllOptional)) {
		t.Errorf("the census itself changed with the optional evidence: %v vs %v",
			evidenceKeysOf(withNoOptional), evidenceKeysOf(withAllOptional))
	}
}

// ── the sweep's own size ──────────────────────────────────────────────────────────────

// The matrix comment quantifies the sweep, and a quantity in a comment is a claim. It said
// "~29k plans" while the cross product is 69,984 — off by 2.4x, in the one sentence whose job
// is to say how strong the sweep is. Asserted here so the number cannot drift again, and so
// that adding an input class forces the author to look at what it multiplies out to.
func TestTheInputMatrixIsTheSizeItClaims(t *testing.T) {
	const (
		// 2 symbols x 3 as-of x 4 snapshot bases x 3 observation bases x 9 prices
		// x 6 ATRs x 6 regimes x 3 adjustment ages x 4 entry semantics.
		wantTotal = 2 * 3 * 4 * 3 * 9 * 6 * 6 * 3 * 4
		// The well-formed subset: one symbol and one date of those, everything else free.
		wantWellFormed = 1 * 1 * 4 * 3 * 9 * 6 * 6 * 3 * 4
	)
	all := matrix()
	if len(all) != wantTotal {
		t.Errorf("matrix() produced %d snapshots, want %d — the comment above it quantifies "+
			"this sweep, so the number is part of the claim", len(all), wantTotal)
	}

	var wellFormed int
	names := make(map[string]bool, len(all))
	for _, c := range all {
		if c.in.WellFormed() {
			wellFormed++
		}
		if names[c.name] {
			t.Fatalf("duplicate matrix case %q — two input classes share a name and one of "+
				"them is invisible in every failure message", c.name)
		}
		names[c.name] = true
	}
	if wellFormed != wantWellFormed {
		t.Errorf("matrix() produced %d well-formed snapshots, want %d", wellFormed, wantWellFormed)
	}

	// And the size assertion must be reading the real thing: a name should still describe the
	// case it belongs to.
	if !strings.Contains(all[0].name, "symbol") {
		t.Errorf("first matrix case is named %q — the naming scheme changed and the failure "+
			"messages above no longer identify an input", all[0].name)
	}
	if len(censusKeys()) != 15 {
		t.Errorf("the census is %d rows wide; if that was deliberate, update this test and "+
			"the split between required, corroborating and traced evidence — and say which "+
			"side of the CONFIDENCE census the new row falls on", len(censusKeys()))
	}
}

// ── the confidence census EP-3b deliberately did not touch ────────────────────────────

// FOUR REQUIRED ITEMS AND TWO CORROBORATING ONES, counted straight off ConfidenceMetrics.
//
// Six new evidence rows arrive with EP-3b and none of them is counted. That is a decision, not
// an omission — plan.go argues it at length — and this is the test that makes it a decision:
// counting the MA20 or the previous close on either side of the census changes these numbers
// and turns this red, so a future item has to state the case in the same commit.
//
// It reads the counts through the exported grader rather than through a plan, because
// ConfidenceMetrics is where the census actually lives. The plan-level consequence is asserted
// by TestComputePlanStillReachesNoVerdictUnderEP3 against expectedConfidence, which knows
// nothing about EP-3b's fields.
func TestTheConfidenceCensusIsStillTheOneEP2Shipped(t *testing.T) {
	// A fully-populated EP-3b snapshot: every EP-2 item, every EP-3b level, the previous
	// close and the liquidity row.
	full := entryplan.ComputePlan(zoneBase())
	if full.Confidence != entryplan.ConfidenceHigh {
		t.Errorf("a snapshot holding EVERYTHING grades %q, want HIGH", full.Confidence)
	}

	// Strip every EP-3b input and NOTHING else. The grade must not move: the level evidence,
	// the previous close and the liquidity row are not on the census.
	in := zoneBase()
	in.MA20, in.MA60, in.BaseLow, in.PivotHigh, in.PreviousClose =
		entryplan.PriceObservation{}, entryplan.PriceObservation{}, entryplan.PriceObservation{},
		entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.AvgVolume20 = nil
	stripped := entryplan.ComputePlan(in)

	if stripped.Confidence != full.Confidence {
		t.Errorf("removing every EP-3b input moved the grade %q → %q. If the census really "+
			"should count them, that is a confidence-rule change: it re-grades every "+
			"archived comparison, and it needs its own work item and its own argument",
			full.Confidence, stripped.Confidence)
	}
	// And it DID change the output, so the assertion above is not comparing two identical
	// plans.
	if full.IdealEntry == nil || stripped.IdealEntry != nil {
		t.Fatalf("the fixtures are not what this test needs: full zone = %v, stripped zone "+
			"= %v", full.IdealEntry, stripped.IdealEntry)
	}

	// The census ARITHMETIC, stated as literals. This is the pair of numbers a future item
	// would have to change.
	if got := entryplan.ClassifyConfidence(entryplan.ConfidenceMetrics{
		RequiredTotal: 4, RequiredPresent: 4, OptionalTotal: 2, OptionalPresent: 2,
	}, true); got != entryplan.ConfidenceHigh {
		t.Errorf("4-of-4 required and 2-of-2 corroborating grades %q, want HIGH", got)
	}
	if got := entryplan.ClassifyConfidence(entryplan.ConfidenceMetrics{
		RequiredTotal: 4, RequiredPresent: 4, OptionalTotal: 8, OptionalPresent: 2,
	}, true); got == entryplan.ConfidenceHigh {
		t.Error("with EP-3b's six rows added to the corroborating side, a plan holding only " +
			"the ATR and the adjustment age would still grade HIGH — which is the sign the " +
			"census was widened without the grade scale being rethought")
	}
}
