package entryplan_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

// ── the input matrix ──────────────────────────────────────────────────────────────────
//
// Sampling a handful of inputs is what FU-11's D8 did, and it covered 11 of 14 codes while
// claiming all of them. So the properties below are checked over a CROSS PRODUCT of every
// decision-relevant input class instead: not literally every float64, but every branch each
// field can take, in every combination with every other.
//
// 2 x 3 x 4 x 3 x 9 x 6 x 6 x 3 x 4 = 279,936 plans, all pure, all fast — of which 46,656 are
// well-formed. This sentence used to say "~29k" against a 69,984-case product, which was wrong
// by 2.4x in the one place whose job is to state how strong the sweep is, so the arithmetic is
// asserted by TestTheInputMatrixIsTheSizeItClaims rather than counted by hand. EP-2 multiplied
// it by the 4-value entry-semantic axis and had to edit the claim, which is the point of
// asserting it.

type namedSnapshot struct {
	name string
	in   entryplan.Snapshot
}

func matrix() []namedSnapshot {
	symbols := []struct {
		name string
		v    string
	}{{"symbol", "2330"}, {"nosymbol", ""}}

	asOfs := []struct {
		name string
		v    string
	}{{"date", "2026-09-09"}, {"nodate", ""}, {"baddate", "9 September"}}

	bases := []struct {
		name string
		v    entryplan.PriceBasis
	}{
		{"raw", entryplan.PriceBasisRaw},
		{"adjusted", entryplan.PriceBasisAdjusted},
		{"nobasis", ""},
		{"bogusbasis", entryplan.PriceBasis("TOTAL_RETURN")},
	}

	obsBases := []struct {
		name string
		v    entryplan.PriceBasis
	}{
		{"inherit", ""},
		{"ownraw", entryplan.PriceBasisRaw},
		{"ownbogus", entryplan.PriceBasis("SPLIT_ONLY")},
	}

	prices := []struct {
		name string
		v    entryplan.PriceObservation
	}{
		{"price_ok", entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)}},
		{"price_nil", entryplan.PriceObservation{Status: entryplan.Available}},
		{"price_unavailable", entryplan.PriceObservation{Status: entryplan.Unavailable, Value: f(902.5)}},
		{"price_insufficient", entryplan.PriceObservation{Status: entryplan.InsufficientData}},
		{"price_nostatus", entryplan.PriceObservation{Value: f(902.5)}},
		// The three shapes a "0 means missing" convention would have let through.
		{"price_nan", entryplan.PriceObservation{Status: entryplan.Available, Value: f(math.NaN())}},
		{"price_inf", entryplan.PriceObservation{Status: entryplan.Available, Value: f(math.Inf(1))}},
		{"price_zero", entryplan.PriceObservation{Status: entryplan.Available, Value: f(0)}},
		{"price_negative", entryplan.PriceObservation{Status: entryplan.Available, Value: f(-12)}},
	}

	atrs := []struct {
		name string
		v    entryplan.ATREvidence
	}{
		{"atr_ok", entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw}},
		{"atr_nil", entryplan.ATREvidence{Status: entryplan.Available, Period: 14}},
		{"atr_unavailable", entryplan.ATREvidence{Status: entryplan.Unavailable}},
		{"atr_nan", entryplan.ATREvidence{Status: entryplan.Available, Value: f(math.NaN())}},
		{"atr_zero", entryplan.ATREvidence{Status: entryplan.Available, Value: f(0)}},
		{"atr_empty", entryplan.ATREvidence{}},
	}

	regimes := []struct {
		name string
		v    entryplan.RegimeEvidence
	}{
		{"regime_bull", entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull}},
		{"regime_bear", entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBear}},
		{"regime_unknown", entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeUnknown}},
		{"regime_unavailable", entryplan.RegimeEvidence{Status: entryplan.Unavailable, Regime: model.RegimeBull}},
		{"regime_garbage", entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.Regime("MELT_UP")}},
		{"regime_empty", entryplan.RegimeEvidence{}},
	}

	ages := []struct {
		name string
		v    entryplan.AdjustmentAge
	}{
		{"age_unknown", entryplan.AdjustmentAge{}},
		{"age_0", entryplan.AdjustmentAge{BarsAgo: i(0)}},
		{"age_63", entryplan.AdjustmentAge{BarsAgo: i(63)}},
	}

	// EP-2's addition, and the axis that finally makes the confidence census non-vacuous:
	// required item #4 can now be present OR absent, where under EP-1 it was structurally
	// always absent. All four branches ComputePlan can take on it are here.
	semantics := []struct {
		name string
		v    entryplan.EntrySemanticEvidence
	}{
		{"sem_pullback", entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback}},
		{"sem_breakout", entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticBreakout}},
		// Handed to us, and the upstream layer could not say which kind of entry it is.
		{"sem_unknown", entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticUnknown}},
		// Never handed to us at all — the zero value, which is the honest default.
		{"sem_absent", entryplan.EntrySemanticEvidence{}},
	}

	var out []namedSnapshot
	for _, sym := range symbols {
		for _, asOf := range asOfs {
			for _, b := range bases {
				for _, ob := range obsBases {
					for _, pr := range prices {
						for _, atr := range atrs {
							for _, rg := range regimes {
								for _, ag := range ages {
									for _, sem := range semantics {
										obs := pr.v
										obs.PriceBasis = ob.v
										out = append(out, namedSnapshot{
											name: fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s/%s/%s",
												sym.name, asOf.name, b.name, ob.name,
												pr.name, atr.name, rg.name, ag.name, sem.name),
											in: entryplan.Snapshot{
												Symbol:        sym.v,
												AsOf:          asOf.v,
												PriceBasis:    b.v,
												AdjustmentAge: ag.v,
												CurrentPrice:  obs,
												ATR:           atr.v,
												Regime:        rg.v,
												EntrySemantic: sem.v,
											},
										})
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

// ── EP-1's whole behavioural contract ─────────────────────────────────────────────────

// atrUsable mirrors ComputePlan's ATR gate. ATREvidence exports no Usable() — deliberately,
// since ATREvidence's doc says requiring ATR is a policy owned by the consumer — so the census
// arithmetic below has to state the gate itself.
func atrUsable(a entryplan.ATREvidence) bool {
	if a.Status != entryplan.Available || a.Value == nil {
		return false
	}
	v := *a.Value
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

// expectedConfidence is the evidence census computed INDEPENDENTLY of ComputePlan: four
// required items, two corroborating ones, and ClassifyConfidence's ordering.
//
// It exists because EP-2 made the confidence axis reachable. Under EP-1 required item #4 was
// counted and could never be present, so every plan graded INSUFFICIENT_DATA and census_test
// had to record that no non-vacuous assertion about the required/optional SPLIT was possible:
// "move ATR from OptionalTotal to RequiredTotal in plan.go and this package still passes. It
// cannot fail." It can now. Moving ATR to the required side makes every ATR-less snapshot fall
// below the floor, which this function does not predict, and the test below turns red.
func expectedConfidence(in entryplan.Snapshot) entryplan.Confidence {
	required := in.CurrentPrice.BasisOr(in.PriceBasis).Supported() &&
		in.CurrentPrice.Usable() &&
		in.Regime.Usable() &&
		in.EntrySemantic.Usable()
	if !required {
		return entryplan.ConfidenceInsufficientData
	}
	var optional int
	if atrUsable(in.ATR) {
		optional++
	}
	if in.AdjustmentAge.Known() {
		optional++
	}
	switch optional {
	case 2:
		return entryplan.ConfidenceHigh
	case 1:
		return entryplan.ConfidenceMedium
	default:
		return entryplan.ConfidenceLow
	}
}

// THE CONFIDENCE GRADE IS THE CENSUS, AND IT IS NOT THE VERDICT (spec §27).
//
// EP-3b's version of this test asserted INSUFFICIENT_DATA for every input, because no rule in
// the package produced a status. EP-5 produces one, and the assertion it replaces that with is
// the one that was always the point: the grade is computed from the EVIDENCE CENSUS and from
// nothing else, so it must not move when the verdict does and must not be readable off it.
//
// "BUY_NOW with LOW confidence" is a real row. Collapsing the two axes would make a
// thin-evidence plan indistinguishable from one whose entry has not arrived yet, and a reader
// would lose the ability to tell "we are sure there is nothing here" from "we could not see".
//
// The CONFIDENCE half is asserted against an INDEPENDENT census — four required items and two
// corroborating ones — which is also what pins EP-3b's decision not to re-scale the grade:
// expectedConfidence knows nothing about the MA20 or the previous close, so counting either of
// them in plan.go turns this red.
func TestTheConfidenceGradeIsTheCensusAndNotTheVerdict(t *testing.T) {
	grades := map[entryplan.Confidence]int{}
	// The cross-tabulation, and it is the anti-collapse ledger: at least one grade must appear
	// beside more than one status, or "the axes are independent" is a claim about a table with
	// one column.
	pairs := map[entryplan.Confidence]map[entryplan.EntryStatus]bool{}
	for _, c := range matrix() {
		p := entryplan.ComputePlan(c.in)
		if pairs[p.Confidence] == nil {
			pairs[p.Confidence] = map[entryplan.EntryStatus]bool{}
		}
		pairs[p.Confidence][p.Status] = true
		if c.in.WellFormed() {
			if want := expectedConfidence(c.in); p.Confidence != want {
				t.Fatalf("%s: confidence = %q, want %q — the plan's census and the census "+
					"computed from the snapshot disagree", c.name, p.Confidence, want)
			}
			grades[p.Confidence]++
		} else if p.Confidence != entryplan.ConfidenceInsufficientData {
			t.Fatalf("%s: malformed snapshot graded %q — there is nothing to be confident "+
				"about", c.name, p.Confidence)
		}
		if p.RuleVersion != entryplan.RuleVersion {
			t.Fatalf("%s: rule version = %q, want %q", c.name, p.RuleVersion, entryplan.RuleVersion)
		}
		if len(p.Reasons) == 0 {
			t.Fatalf("%s: no reasons — an unexplained INSUFFICIENT_DATA is a shrug", c.name)
		}
		if p.Symbol != c.in.Symbol || p.AsOf != c.in.AsOf {
			t.Fatalf("%s: identity rewritten: symbol %q→%q, as_of %q→%q",
				c.name, c.in.Symbol, p.Symbol, c.in.AsOf, p.AsOf)
		}
	}

	// ANTI-VACUITY. Every grade must have been produced by something in the matrix, or the
	// comparison above is a comparison of two constants. All four is the claim EP-2 makes: the
	// axis is live.
	for _, g := range []entryplan.Confidence{
		entryplan.ConfidenceInsufficientData,
		entryplan.ConfidenceLow,
		entryplan.ConfidenceMedium,
		entryplan.ConfidenceHigh,
	} {
		if grades[g] == 0 {
			t.Errorf("no well-formed snapshot in the matrix graded %q — the confidence "+
				"assertion never ran for it. Grades seen: %v", g, grades)
		}
	}

	// THE ANTI-COLLAPSE LEDGER. If confidence and status were the same axis in disguise, each
	// grade would appear beside exactly one status. At least one grade must be seen with two.
	//
	// matrix() carries no level evidence, so it cannot reach BUY_NOW — the pair that matters
	// most ("BUY_NOW with LOW confidence") is asserted over the priced sweep by
	// TestMissingEvidenceIsNotLowConfidence and by the status census in
	// TestEP5PublishesAVerdictAndEveryPriceEP4Computed. What this ledger holds is the weaker
	// and still load-bearing half: the two are not a function of one another here either.
	var spread int
	for _, byStatus := range pairs {
		if len(byStatus) > 1 {
			spread++
		}
	}
	if spread == 0 {
		t.Errorf("every confidence grade in the matrix appears beside exactly one status "+
			"(%v) — the two axes are indistinguishable on this sweep, so nothing here would "+
			"notice if one were computed from the other", pairs)
	}
}

// The four input classes EP-1 must name correctly, spelled out one by one so a reader can see
// which input produces which code without decoding the matrix.
func TestComputePlanNamesTheMissingEvidence(t *testing.T) {
	base := entryplan.Snapshot{
		Symbol:        "2330",
		AsOf:          "2026-09-09",
		PriceBasis:    entryplan.PriceBasisRaw,
		AdjustmentAge: entryplan.AdjustmentAge{BarsAgo: i(63)},
		CurrentPrice:  entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)},
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime: entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
	}

	cases := []struct {
		name string
		mut  func(s *entryplan.Snapshot)
		want []entryplan.Reason
		// wantStatus is EP-5's addition. Empty means INSUFFICIENT_DATA, which is what a
		// table about MISSING EVIDENCE mostly produces — and stating it per case is how the
		// two rows that DO reach a verdict stop being invisible.
		wantStatus entryplan.EntryStatus
	}{
		{
			// The base snapshot carries no LEVEL evidence, so under EP3-v1 a "complete"
			// EP-2 snapshot is no longer complete: the zone rule now has an input it can be
			// missing. The code says which one, and the EP-3b limit still travels with it.
			name: "every EP-2 input in hand, and no level to centre a zone on",
			mut:  func(s *entryplan.Snapshot) {},
			want: []entryplan.Reason{
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonStatusEntryZoneUnavailable,
			},
		},
		{
			// A genuinely complete snapshot under EP3-v1: prices, and nothing missing.
			name: "complete under EP3-v1 — the only gap left is the STATUS",
			mut: func(s *entryplan.Snapshot) {
				// The supports are far enough below the market that the zone (80 ± 9.125,
				// on an ATR of 18.25) does not reach it, so the overlap flag stays off and
				// the reason list is exactly the one remaining gap.
				s.MA20, s.MA60, s.BaseLow = obs(80), obs(75), obs(78)
				s.PreviousClose = obs(98)
				s.CurrentPrice = obs(100)
			},
			want:       []entryplan.Reason{entryplan.ReasonStatusPriceAboveEntryZone},
			wantStatus: entryplan.StatusWaitPullback,
		},
		{
			name: "current price unavailable",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice = entryplan.PriceObservation{Status: entryplan.Unavailable}
			},
			// The levels cannot be SCREENED without a quote, so the level rule reports the
			// stage it reached — "unavailable" — rather than a rejection it never made. The
			// missing quote is named by its own code, once.
			want: []entryplan.Reason{
				entryplan.ReasonCurrentPriceUnavailable,
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonStatusCurrentPriceUnavailable,
			},
		},
		{
			name: "current price present but not a price (0 is not a cautious default)",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice = entryplan.PriceObservation{Status: entryplan.Available, Value: f(0)}
			},
			want: []entryplan.Reason{
				entryplan.ReasonCurrentPriceUnavailable,
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonStatusCurrentPriceUnavailable,
			},
		},
		{
			name: "required base evidence unavailable: no regime call",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Unavailable}
			},
			// No regime → no policy → NO DECISION about either the zone or the chase. Two
			// codes, both UNRESOLVED, because they are two different fields: a consumer
			// showing only one of them would say nothing about the other.
			want: []entryplan.Reason{
				entryplan.ReasonMarketRegimeUnavailable,
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonChasePolicyUnresolved,
				entryplan.ReasonStatusPolicyUnresolved,
			},
		},
		{
			name: "regime computed and UNKNOWN — the honest answer is still not a regime",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeUnknown}
			},
			want: []entryplan.Reason{
				entryplan.ReasonMarketRegimeUnavailable,
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonChasePolicyUnresolved,
				entryplan.ReasonStatusPolicyUnresolved,
			},
		},
		{
			name: "no entry semantic was handed to us — nothing to translate into a price",
			mut: func(s *entryplan.Snapshot) {
				s.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable}
			},
			// The regime is BULL, so the CHASE policy is still ALLOWED — a chase tolerance
			// is a fact about the regime and does not need a semantic. What is unresolved is
			// the ZONE, because no shape was proposed for it to permit.
			want: []entryplan.Reason{
				entryplan.ReasonEntrySemanticUnavailable,
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonStatusSemanticUnresolved,
			},
		},
		{
			name: "entry semantic in hand and UNKNOWN — a state, and still not a semantic",
			mut: func(s *entryplan.Snapshot) {
				s.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Available,
					Semantic: entryplan.EntrySemanticUnknown}
			},
			want: []entryplan.Reason{
				entryplan.ReasonEntrySemanticUnavailable,
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonStatusSemanticUnresolved,
			},
		},
		{
			name: "a breakout is as complete an input as a pullback",
			mut: func(s *entryplan.Snapshot) {
				s.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Available,
					Semantic: entryplan.EntrySemanticBreakout}
				s.PivotHigh = obs(105)
				s.PreviousClose = obs(98)
				s.CurrentPrice = obs(100)
				// An ATR of 4 rather than the base fixture's 18.25, so the day's +10%
				// ceiling (107.5) still covers the top of the breakout zone. With 18.25 the
				// zone would reach 114.5 and the ceiling would be withdrawn — a legitimate
				// answer, and a different case, tested in zonecases_test.go.
				s.ATR = atr(4)
			},
			want:       []entryplan.Reason{entryplan.ReasonStatusPriceBelowEntryZone},
			wantStatus: entryplan.StatusWaitBreakout,
		},
		{
			name: "price basis absent — never defaulted to RAW",
			mut:  func(s *entryplan.Snapshot) { s.PriceBasis = "" },
			// Without a basis nothing can be compared with anything, so the levels are
			// NOT_SCREENED and the level rule reports the stage it reached.
			want: []entryplan.Reason{
				entryplan.ReasonPriceBasisUnavailable,
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonStatusEntryZoneUnavailable,
			},
		},
		{
			name: "price basis unsupported",
			mut:  func(s *entryplan.Snapshot) { s.PriceBasis = entryplan.PriceBasis("TOTAL_RETURN") },
			want: []entryplan.Reason{
				entryplan.ReasonPriceBasisUnavailable,
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonStatusEntryZoneUnavailable,
			},
		},
		{
			name: "malformed: no symbol",
			mut:  func(s *entryplan.Snapshot) { s.Symbol = "" },
			want: []entryplan.Reason{entryplan.ReasonSnapshotMalformed},
		},
		{
			name: "malformed: AsOf is not a session date",
			mut:  func(s *entryplan.Snapshot) { s.AsOf = "yesterday" },
			want: []entryplan.Reason{entryplan.ReasonSnapshotMalformed},
		},
		{
			name: "malformed input refuses BEFORE diagnosing evidence",
			mut: func(s *entryplan.Snapshot) {
				s.Symbol = ""
				s.CurrentPrice = entryplan.PriceObservation{}
				s.Regime = entryplan.RegimeEvidence{}
				s.EntrySemantic = entryplan.EntrySemanticEvidence{}
				s.PriceBasis = ""
			},
			want: []entryplan.Reason{entryplan.ReasonSnapshotMalformed},
		},
		{
			name: "the zero Snapshot",
			mut:  func(s *entryplan.Snapshot) { *s = entryplan.Snapshot{} },
			want: []entryplan.Reason{entryplan.ReasonSnapshotMalformed},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mut(&in)
			p := entryplan.ComputePlan(in)
			if !reflect.DeepEqual(p.Reasons, c.want) {
				t.Errorf("reasons = %v, want %v", p.Reasons, c.want)
			}
			// THE STATUS, per case. Until EP-5 this block asserted INSUFFICIENT_DATA for
			// every row and then asserted !IsVerdict() on top of it, which was EP-4's scope
			// boundary from the inside. EP-5 is the item that retires it, and what replaces
			// it is the stronger claim: each row states the status its input produces, so a
			// verdict appearing on a row about MISSING EVIDENCE turns this red.
			//
			// TWO rows reach a verdict-shaped status and both are rows whose evidence is
			// COMPLETE — a pullback whose support band sits below the market, and a breakout
			// whose pivot sits above it. Every other row here is missing something, and every
			// one of them is INSUFFICIENT_DATA, which is the §5 line asserted case by case
			// rather than swept.
			want := c.wantStatus
			if want == "" {
				want = entryplan.StatusInsufficientData
			}
			if p.Status != want {
				t.Errorf("status = %q, want %q", p.Status, want)
			}
			if want == entryplan.StatusInsufficientData && p.Status.IsVerdict() {
				t.Errorf("status = %q is a VERDICT on an input that is MISSING evidence — a "+
					"state was published as a conclusion", p.Status)
			}
		})
	}
}

// ATR IS THE WIDTH RULE'S INPUT NOW, AND ITS ABSENCE DECIDES EXACTLY ONE THING.
//
// ATREvidence's doc has always said the hard requirement "belongs to the work item that
// multiplies by it, which is the only one able to say what it does when the multiplier is
// missing". EP-3b is that work item, so the previous version of this test — "an absent ATR
// must not change the verdict, the reasons, or anything except the ATR evidence row" — is no
// longer the contract, and keeping it would have been a test that passed only because the base
// fixture had no levels either.
//
// What replaces it is the precise version of the same discipline:
//
//	the ZONE is withdrawn                 because the width is 0.5 × ATR and there is no ATR
//	the reason names the ATR              not the levels, which were fine
//	the CONFIDENCE grade moves one step   HIGH → MEDIUM, and never to INSUFFICIENT_DATA
//	the STATUS becomes a STATE            not a verdict: a missing multiplier is a missing
//	                                      DEPENDENCY, and doc.go's line is that it must never
//	                                      read as "there is nowhere legal to buy"
//	NO substitute width is invented       asserted separately, and at length, in zone_test.go
//
// The status line is EP-5's edit. EP-3b's version said "the status does not change", which was
// true only because nothing computed one; now the status DOES change — the zone it was built on
// is gone — and the assertion that matters is WHICH WAY it changes. INSUFFICIENT_DATA is right
// and NO_VALID_ENTRY would be the exact §5 error: the regime permitted the entry, the supports
// were all in hand, and the only thing missing was a number nobody supplied.
func TestATRIsRequiredByTheWidthRuleAndByNothingElse(t *testing.T) {
	withATR := entryplan.ComputePlan(zoneBase())
	if withATR.IdealEntry == nil {
		t.Fatalf("the fixture produced no zone even WITH an ATR: %v", withATR.Reasons)
	}

	in := zoneBase()
	in.ATR = entryplan.ATREvidence{Status: entryplan.Unavailable}
	withoutATR := entryplan.ComputePlan(in)

	if withoutATR.IdealEntry != nil {
		t.Errorf("a zone of %v → %v was published with no ATR", withoutATR.IdealEntry.Low,
			withoutATR.IdealEntry.High)
	}
	if !hasReason(withoutATR, entryplan.ReasonZoneATRUnavailable) {
		t.Errorf("reasons = %v, want ZONE_ATR_UNAVAILABLE", withoutATR.Reasons)
	}
	if hasReason(withoutATR, entryplan.ReasonPullbackLevelsUnavailable) {
		t.Errorf("the missing ATR was reported as a missing LEVEL: %v", withoutATR.Reasons)
	}
	if withATR.Status == entryplan.StatusInsufficientData {
		t.Fatalf("the fixture reaches no verdict even WITH an ATR, so the status assertion "+
			"below would compare INSUFFICIENT_DATA with itself: %v", withATR.Reasons)
	}
	if withoutATR.Status != entryplan.StatusInsufficientData {
		t.Errorf("removing the ATR produced %q, want INSUFFICIENT_DATA — an absent multiplier "+
			"is a missing DEPENDENCY, and any verdict here would say the market offers no "+
			"entry when what happened is that nobody supplied a width", withoutATR.Status)
	}
	if withoutATR.Status.IsVerdict() {
		t.Errorf("removing the ATR produced the VERDICT %q — this is the §5 error exactly: "+
			"the regime permitted the entry and every support was in hand", withoutATR.Status)
	}
	if !hasReason(withoutATR, entryplan.ReasonStatusEntryZoneUnavailable) {
		t.Errorf("the status does not name the missing band: %v", withoutATR.Reasons)
	}
	// The GRADE is the part that must not move: the census is EP-2's, and ATR is
	// corroborating evidence on it. Removing it drops HIGH to MEDIUM (one of two optional
	// items), which is a change in DEGREE — it must not cross to INSUFFICIENT_DATA, which
	// would say the required floor was not cleared.
	if withATR.Confidence != entryplan.ConfidenceHigh {
		t.Errorf("with every input in hand the grade is %q, want HIGH", withATR.Confidence)
	}
	if withoutATR.Confidence != entryplan.ConfidenceMedium {
		t.Errorf("without the ATR the grade is %q, want MEDIUM — ATR is corroborating "+
			"evidence, and INSUFFICIENT_DATA would say the required floor was not cleared",
			withoutATR.Confidence)
	}

	// The ATR row must still BE THERE, saying it is unavailable. Without the found flag this
	// loop was vacuous in exactly the case it exists to inspect: deleting the append from
	// ComputePlan left nothing to iterate and the test stayed green (review mutation M4). The
	// whole census is pinned in census_test.go; the flag is kept here so this test cannot go
	// hollow again on its own.
	var found bool
	for _, e := range withoutATR.Evidence {
		if e.Key == entryplan.EvidenceATR {
			found = true
			if e.Status == entryplan.Available || e.Value != nil {
				t.Errorf("ATR evidence still reads as present: %+v", e)
			}
		}
	}
	if !found {
		t.Errorf("the ATR evidence row disappeared when ATR was unavailable — recording "+
			"UNAVAILABLE is the row's job, and a dropped row reads downstream as a rule "+
			"that never looked. Census: %v", evidenceKeysOf(withoutATR))
	}
}

// THE ADJUSTMENT AGE IS A CAVEAT AND NEVER A GATE.
//
// A recent ex-dividend leaves residue in the ATR, so the zone width computed from it carries
// some of that event. EP-3b reports the fact and COMPUTES THE PRICES ANYWAY, and the two
// halves of that sentence are asserted separately below: the prices must be IDENTICAL across
// every age, and the caveat must appear for the recent ones.
//
// The threshold is 63 and not 65 — AdjustmentAge's doc has the derivation — and it is a
// REPORTING threshold. Nothing becomes clean on day 63: the residue is ((N-1)/N)^age, which is
// 1.03% at 62 bars and 0.96% at 63, and reading a smooth curve as a cliff is how a caveat
// turns into a gate.
func TestTheAdjustmentAgeIsACaveatAndNeverAGate(t *testing.T) {
	ages := []struct {
		age        entryplan.AdjustmentAge
		wantCaveat bool
	}{
		// LastAdjustmentAge answered ok == false. NOT recent, and NOT "no adjustment":
		// three situations produce it and only one of them means the series is clean.
		{entryplan.AdjustmentAge{}, false},
		{entryplan.AdjustmentAge{BarsAgo: i(0)}, true}, // the newest bar IS the event
		{entryplan.AdjustmentAge{BarsAgo: i(1)}, true},
		{entryplan.AdjustmentAge{BarsAgo: i(62)}, true}, // residue 1.03%
		{entryplan.AdjustmentAge{BarsAgo: i(63)}, false},
		{entryplan.AdjustmentAge{BarsAgo: i(65)}, false}, // the RSI number, not the gate here
		{entryplan.AdjustmentAge{BarsAgo: i(500)}, false},
	}

	var recent, notRecent int
	var refZone [2]float64
	var refChase float64
	var refStatus entryplan.EntryStatus
	for n, c := range ages {
		in := zoneBase()
		in.AdjustmentAge = c.age
		p := entryplan.ComputePlan(in)

		// (1) THE PRICES ARE UNTOUCHED. This is the assertion that fails if the age ever
		// becomes a gate — the mutation "recent adjustment → the whole plan is unavailable"
		// cannot survive it.
		if p.IdealEntry == nil || p.MaxChasePrice == nil {
			t.Fatalf("age %v: the plan lost its prices (zone %v, chase %v); reasons %v — a "+
				"recent adjustment must not suppress a plan over a residue of a fraction of "+
				"a percent", c.age.BarsAgo, p.IdealEntry, p.MaxChasePrice, p.Reasons)
		}
		if n == 0 {
			refZone = [2]float64{p.IdealEntry.Low, p.IdealEntry.High}
			refChase = *p.MaxChasePrice
		} else if p.IdealEntry.Low != refZone[0] || p.IdealEntry.High != refZone[1] ||
			*p.MaxChasePrice != refChase {
			t.Errorf("age %v moved the prices: zone %v → %v/%v, chase %v → %v",
				c.age.BarsAgo, refZone, p.IdealEntry.Low, p.IdealEntry.High, refChase,
				*p.MaxChasePrice)
		}
		// (1b) THE STATUS IS UNTOUCHED TOO, and from EP-5 that is a real assertion rather
		// than a restatement of "no rule computes one". The base fixture publishes a verdict,
		// so this compares a REAL status across seven ages — the mutation "AdjustmentAge
		// recent → NO_VALID_ENTRY" cannot survive it, and neither can a softer one that only
		// downgrades a BUY_NOW to a WAIT.
		if n == 0 {
			refStatus = p.Status
			if refStatus == entryplan.StatusInsufficientData {
				t.Fatalf("the fixture reaches no verdict at all, so comparing the status "+
					"across ages compares INSUFFICIENT_DATA with itself: reasons %v", p.Reasons)
			}
		} else if p.Status != refStatus {
			t.Errorf("age %v changed the status: %q → %q — the adjustment age is a CAVEAT, "+
				"and the residue it is about is a fraction of a percent", c.age.BarsAgo,
				refStatus, p.Status)
		}

		// (2) THE CAVEAT. Machine-readable, plus prose, plus the trace flag.
		got := hasReason(p, entryplan.ReasonRecentPriceAdjustment)
		if got != c.wantCaveat {
			t.Errorf("age %v: RECENT_PRICE_ADJUSTMENT present = %v, want %v; reasons %v",
				c.age.BarsAgo, got, c.wantCaveat, p.Reasons)
		}
		if p.EntryTrace.RecentAdjustment != c.wantCaveat {
			t.Errorf("age %v: the trace flag is %v, want %v", c.age.BarsAgo,
				p.EntryTrace.RecentAdjustment, c.wantCaveat)
		}
		if c.wantCaveat {
			recent++
			var prose bool
			for _, cav := range p.Caveats {
				if contains(cav, "除權息") && contains(cav, "63") {
					prose = true
				}
			}
			if !prose {
				t.Errorf("age %v: no caveat says what the residue is: %v", c.age.BarsAgo,
					p.Caveats)
			}
		} else {
			notRecent++
		}

		// (3) The age itself is PUBLISHED, on the evidence row and on the trace, so a later
		// consumer can apply its own error budget.
		var found bool
		for _, e := range p.Evidence {
			if e.Key != entryplan.EvidenceAdjustmentAge {
				continue
			}
			found = true
			if c.age.Known() {
				if e.Value == nil || *e.Value != float64(*c.age.BarsAgo) {
					t.Errorf("age %v: evidence value = %v", c.age.BarsAgo, e.Value)
				}
			} else if e.Value != nil {
				t.Errorf("an age was published for an answer that does not exist: %v", *e.Value)
			}
		}
		if !found {
			t.Errorf("age %v: the adjustment-age row disappeared; census %v", c.age.BarsAgo,
				evidenceKeysOf(p))
		}
	}
	if recent == 0 || notRecent == 0 {
		t.Fatalf("the table covered %d recent and %d not-recent ages — one branch never ran",
			recent, notRecent)
	}
}

// ── MISSING ≠ ZERO ────────────────────────────────────────────────────────────────────

// priceBearingTypes are the Plan fields a reader could place an order from.
//
// Matched by TYPE rather than by name, so a field added later to any of these types is
// covered without anyone remembering to extend this list.
func isPriceBearing(t reflect.Type) bool {
	if t.Kind() != reflect.Ptr {
		return false
	}
	switch t.Elem() {
	case reflect.TypeOf(float64(0)),
		reflect.TypeOf(entryplan.PriceZone{}),
		reflect.TypeOf(entryplan.Invalidation{}),
		reflect.TypeOf(entryplan.PriceLevel{}),
		reflect.TypeOf(entryplan.RiskReward{}):
		return true
	}
	return false
}

// NO LEVEL EVIDENCE, NO PRICE — over the whole 280k-case evidence matrix, none of which
// carries an MA20, a pivot or a previous close.
//
// This test used to be TestEP1EmitsNoExecutionPrices and it asserted that NOTHING in this
// package ever returns a price. EP-3b retires that claim, exactly as doc.go said the item
// that returns the first zone would. What is left is the STRONGER half of it: for an input
// with no level to centre a zone on, every price pointer must still be nil — because the ways
// to produce one anyway are all fabrications (the last price, a percentage of it, a tick-only
// band), and the matrix is 46,656 well-formed chances to take one of them.
//
// A 0 would be a real price on a Taiwanese exchange, and every reading of it is wrong: a free
// entry, a stop that never triggers, a target already reached.
func TestNoLevelEvidenceMeansNoPriceAtAll(t *testing.T) {
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

	var checked int
	for _, c := range matrix() {
		// The matrix carries no MA20, MA60, base low, pivot or previous close: assert that,
		// so this test cannot be quietly turned vacuous from the other end by a matrix that
		// grew level evidence.
		if c.in.MA20.Value != nil || c.in.MA60.Value != nil || c.in.BaseLow.Value != nil ||
			c.in.PivotHigh.Value != nil || c.in.PreviousClose.Value != nil {
			t.Fatalf("%s: the evidence matrix has grown level evidence, so it is no longer "+
				"the no-levels sweep this test is about", c.name)
		}
		p := entryplan.ComputePlan(c.in)
		v := reflect.ValueOf(p)
		for n := 0; n < planType.NumField(); n++ {
			if !isPriceBearing(planType.Field(n).Type) {
				continue
			}
			if !v.Field(n).IsNil() {
				t.Fatalf("%s: Plan.%s = %v, want nil — there is no level in this snapshot to "+
					"centre a zone on, so any price here was invented",
					c.name, planType.Field(n).Name, v.Field(n).Interface())
			}
		}
		if p.HasExecutablePrices() {
			t.Fatalf("%s: HasExecutablePrices() = true", c.name)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("the matrix is empty")
	}
}

// THE SCOPE BOUNDARY, from the other side. This test replaced
// TestEP3bPublishesTheZoneAndTheCeilingAndNothingElse, which asserted that no invalidation, no
// target and no risk/reward was ever published — the claim EP-4 exists to retire.
//
// EP-4's version of this test asserted the OPPOSITE — "and still no status" — because BUY_NOW
// needs "the current price is inside the zone" and "the policy permits executing now", and no
// rule in the package computed either. EP-5 is the item that computes them, so the boundary is
// gone and what replaces it is the two-sided claim: the six prices are still all there, AND a
// verdict is now published for every input.
//
// TWO LEDGERS, because the test can go vacuous from either end. The price ledger requires the
// sweep to have seen every EP-4 field on a real plan; the status ledger requires it to have
// reached every one of the six statuses, or "a verdict is published" would be a statement about
// a population of INSUFFICIENT_DATA rows.
func TestEP5PublishesAVerdictAndEveryPriceEP4Computed(t *testing.T) {
	var withZone, stops, target1s, target2s, rrs int
	statuses := map[entryplan.EntryStatus]int{}
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)

		// EP-5's half. Every plan now carries one of the six, and no plan carries a string
		// that is not one of them — the failure a consumer's switch falls through.
		statuses[p.Status]++
		if !p.Status.Valid() {
			t.Errorf("%s: status = %q is not one of the six declared EntryStatus values",
				c.name, p.Status)
		}
		if p.EntryTrace != nil && len(p.Reasons) == 0 {
			t.Errorf("%s: status %q with no reason at all", c.name, p.Status)
		}
		if p.IdealEntry != nil {
			withZone++
		}
		if p.Invalidation != nil {
			stops++
		}
		if p.Target1 != nil {
			target1s++
		}
		if p.Target2 != nil {
			target2s++
		}
		if p.RiskReward != nil {
			rrs++
		}
	}
	if withZone == 0 || stops == 0 || target1s == 0 || target2s == 0 || rrs == 0 {
		t.Fatalf("the union produced %d zones, %d invalidations, %d first targets, %d second "+
			"targets and %d risk/rewards — a field with no observations means the boundary "+
			"above was never tested on a plan that carries it", withZone, stops, target1s,
			target2s, rrs)
	}
	// EP-5'S OWN LEDGER, and it is what stops the assertions above from being a claim about a
	// population of INSUFFICIENT_DATA plans. Every one of the six statuses must appear on a
	// real snapshot — including the two verdicts, which are the ones a reader ACTS on.
	for _, want := range []entryplan.EntryStatus{
		entryplan.StatusBuyNow, entryplan.StatusWaitPullback, entryplan.StatusWaitBreakout,
		entryplan.StatusTooExtended, entryplan.StatusNoValidEntry,
		entryplan.StatusInsufficientData,
	} {
		if statuses[want] == 0 {
			t.Errorf("no snapshot in the union produces %q — the status is in the vocabulary "+
				"and nothing in this suite has seen the pipeline reach it. Census: %v",
				want, statuses)
		}
	}
	if testing.Verbose() {
		t.Logf("%d zones, %d stops, %d/%d targets, %d R:Rs; statuses %v", withZone, stops,
			target1s, target2s, rrs, statuses)
	}
}

// THE OTHER HALF OF THE DEPENDENCY CONTRACT: the four EP-4 fields must be ABSENT exactly when
// their dependency is, and no wider.
//
// Written as a sweep over every input because the failure mode is a cascade that is too wide —
// "no valuation, therefore no second target", "no resistance, therefore no first target" — and
// each of those reads downstream as a market condition rather than as a rule gap.
func TestTheRiskSideCascadesNoWiderThanItsDependencies(t *testing.T) {
	var noZone, noStop, withStopNoT2 int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)

		if p.IdealEntry == nil {
			noZone++
			// No published entry: nothing to be below and nothing to measure from.
			if p.Invalidation != nil || p.Target1 != nil || p.Target2 != nil || p.RiskReward != nil {
				t.Errorf("%s: no zone, and yet a stop (%v) / targets (%v, %v) / R:R (%v) — "+
					"every EP-4 number is measured from a bound of the published entry",
					c.name, p.Invalidation, p.Target1, p.Target2, p.RiskReward)
			}
			continue
		}
		if p.Invalidation == nil {
			noStop++
			// No risk denominator: no target and no ratio. The ZONE and the chase ceiling
			// survive, and that is asserted here rather than assumed.
			if p.Target1 != nil || p.Target2 != nil || p.RiskReward != nil {
				t.Errorf("%s: no invalidation, and yet targets (%v, %v) or an R:R (%v) — the "+
					"risk denominator is their shared input", c.name, p.Target1, p.Target2,
					p.RiskReward)
			}
			continue
		}
		// A stop exists, so a first target and an R:R must too — unless the target step
		// refused for a reason of its own, which every case in the union that does so names.
		if p.Target1 == nil {
			if !hasReason(p, entryplan.ReasonTarget1TickAlignmentUnavailable) &&
				!hasReason(p, entryplan.ReasonTarget1NotAboveEntry) {
				t.Errorf("%s: a stop with no first target and no code saying why: %v",
					c.name, p.Reasons)
			}
			continue
		}
		if p.RiskReward == nil {
			t.Errorf("%s: a zone, a stop and a first target, and no risk/reward — the ratio "+
				"has every input it needs", c.name)
		}
		if p.Target2 == nil {
			withStopNoT2++
			// Target2 is the OPTIONAL one, and its absence must not have taken anything with
			// it. This is the case enforce.go's narrowed withdrawal exists for.
			if p.Target1 == nil || p.RiskReward == nil || p.IdealEntry == nil {
				t.Errorf("%s: a missing second target removed something else", c.name)
			}
			if !hasReason(p, entryplan.ReasonTarget2BelowTarget1) &&
				!hasReason(p, entryplan.ReasonValuationCeilingBelowEntry) &&
				!hasReason(p, entryplan.ReasonTarget2TickAlignmentUnavailable) {
				t.Errorf("%s: no second target and no code saying why: %v", c.name, p.Reasons)
			}
		}
	}
	if noZone == 0 || noStop == 0 || withStopNoT2 == 0 {
		t.Fatalf("the union covered %d plans with no zone, %d with a zone and no stop, and "+
			"%d with a stop and no second target — a branch with no case in it is an "+
			"assertion that never ran", noZone, noStop, withStopNoT2)
	}
}

// ── NO NaN / Inf ESCAPES ──────────────────────────────────────────────────────────────

// walkFloats visits every float reachable from v, DEREFERENCING pointers and descending into
// structs, slices, arrays, maps and interfaces.
//
// Reflective rather than field-by-field on purpose: a hand-written check covers the fields
// that existed when it was written, and the ones that leak are the ones added afterwards.
func walkFloats(path string, v reflect.Value, visit func(path string, got float64)) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		visit(path, v.Float())
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return
		}
		walkFloats(path+"*", v.Elem(), visit)
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			walkFloats(path+"."+v.Type().Field(n).Name, v.Field(n), visit)
		}
	case reflect.Slice, reflect.Array:
		for n := 0; n < v.Len(); n++ {
			walkFloats(fmt.Sprintf("%s[%d]", path, n), v.Index(n), visit)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkFloats(fmt.Sprintf("%s[%v]", path, k.Interface()), v.MapIndex(k), visit)
		}
	}
}

// Not one non-finite value may reach JSON, HTML, SQLite or an agent prompt — including the
// ones handed IN. NaN and +Inf are in the matrix as current prices and as ATRs precisely so
// that echoing an input into the evidence rows cannot launder them.
func TestNoNonFiniteFloatEscapes(t *testing.T) {
	// The walker itself must be able to find a planted value, or every assertion below is
	// vacuous.
	planted := entryplan.Plan{
		MaxChasePrice: f(math.NaN()),
		IdealEntry:    &entryplan.PriceZone{Low: 1, High: math.Inf(1), WidthATR: f(math.Inf(-1))},
		Evidence:      []entryplan.Evidence{{Key: "X", Value: f(math.NaN())}},
		Target1:       &entryplan.PriceLevel{Price: 3, RMultiple: f(math.NaN())},
	}
	var seen, bad int
	walkFloats("Plan", reflect.ValueOf(planted), func(_ string, got float64) {
		seen++
		if math.IsNaN(got) || math.IsInf(got, 0) {
			bad++
		}
	})
	if seen != 7 || bad != 5 {
		t.Fatalf("walker found %d floats and %d non-finite in a fixture built to contain "+
			"7 and 5 — it is not reaching through pointers or slices", seen, bad)
	}

	for _, c := range matrix() {
		p := entryplan.ComputePlan(c.in)
		walkFloats("Plan", reflect.ValueOf(p), func(path string, got float64) {
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("%s: %s = %v — a non-finite value escaped into a Plan", c.name, path, got)
			}
		})
	}
}

// ── purity ────────────────────────────────────────────────────────────────────────────

// Deterministic, and it does not retain or mutate the caller's data. The second half matters
// as much as the first: a Plan holding a pointer INTO the Snapshot would change value after
// it was returned, which no amount of determinism testing on the return value would show.
func TestComputePlanIsPureAndDoesNotAliasItsInput(t *testing.T) {
	price, atr := 902.5, 18.25
	in := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice: entryplan.PriceObservation{Status: entryplan.Available, Value: &price},
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: &atr, Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime:        entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		AdjustmentAge: entryplan.AdjustmentAge{BarsAgo: i(63)},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
	}
	before := entryplan.ComputePlan(in)
	again := entryplan.ComputePlan(in)
	if !reflect.DeepEqual(before, again) {
		t.Fatalf("ComputePlan is not deterministic:\n%+v\n%+v", before, again)
	}
	if !reflect.DeepEqual(in.CurrentPrice.Value, &price) || price != 902.5 || atr != 18.25 {
		t.Fatal("ComputePlan mutated its input")
	}

	// Move the caller's numbers underneath the returned plan.
	price, atr = -1, math.NaN()
	for _, e := range before.Evidence {
		if e.Value == nil {
			continue
		}
		if math.IsNaN(*e.Value) || *e.Value < 0 {
			t.Fatalf("evidence %q aliases the caller's float: %v", e.Key, *e.Value)
		}
	}
	if !reflect.DeepEqual(before, entryplan.ComputePlan(entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice: entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)},
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime:        entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		AdjustmentAge: entryplan.AdjustmentAge{BarsAgo: i(63)},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
	})) {
		t.Fatal("an already-returned plan changed when the caller's floats moved")
	}
}

// Every price evidence row can name the basis it was measured on. Not decoration: this repo
// currently computes MA20/ATR/RSI/BB on the RAW close (internal/indicator/calculator.go:33)
// while relstrength.go:124 and vcp.go:236 go through fetcher.PriceForCalc, so two numbers in
// one snapshot may not be on the same series. An unlabelled price cannot be combined safely
// with another one.
func TestEveryPriceEvidenceCarriesItsBasis(t *testing.T) {
	in := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisAdjusted,
		CurrentPrice: entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)},
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime: entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticBreakout},
	}
	want := map[string]entryplan.PriceBasis{
		entryplan.EvidenceCurrentPrice: entryplan.PriceBasisAdjusted, // inherited from the snapshot
		entryplan.EvidenceATR:          entryplan.PriceBasisRaw,      // its own, and different
	}
	for _, e := range entryplan.ComputePlan(in).Evidence {
		if w, ok := want[e.Key]; ok {
			if e.PriceBasis != w {
				t.Errorf("evidence %q basis = %q, want %q", e.Key, e.PriceBasis, w)
			}
			delete(want, e.Key)
		}
	}
	if len(want) != 0 {
		t.Errorf("price evidence missing entirely: %v", want)
	}

	// And the package never converts between them. An ADJUSTED snapshot stays ADJUSTED.
	in.PriceBasis = entryplan.PriceBasisAdjusted
	for _, e := range entryplan.ComputePlan(in).Evidence {
		if e.Key == entryplan.EvidencePriceBasis && e.Text != string(entryplan.PriceBasisAdjusted) {
			t.Errorf("price basis was rewritten to %q", e.Text)
		}
	}
}
