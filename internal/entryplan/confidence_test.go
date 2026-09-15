package entryplan_test

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// THE mutation gate for this file.
//
// Delete the availability pre-check at the top of ClassifyConfidence and this test must fail.
// It is the FU-9 M11 mutation transplanted: there, a below-floor distribution was graded
// instead of refused; here, a plan with no required evidence would come back LOW or HIGH — a
// grade a reader takes as "weak but real", when there is nothing behind it at all.
//
// MISSING IS NOT LOW.
func TestMissingEvidenceIsNotLowConfidence(t *testing.T) {
	full := entryplan.ConfidenceMetrics{
		RequiredTotal: 3, RequiredPresent: 3,
		OptionalTotal: 2, OptionalPresent: 2,
	}
	cases := []struct {
		name      string
		m         entryplan.ConfidenceMetrics
		available bool
		why       string
	}{
		{
			name: "not available, and every count says otherwise",
			m:    full, available: false,
			why: "the counts are complete and the caller says the evidence is not usable; " +
				"without the pre-check the switch grades this HIGH",
		},
		{
			name: "a required item is missing",
			m: entryplan.ConfidenceMetrics{RequiredTotal: 3, RequiredPresent: 2,
				OptionalTotal: 2, OptionalPresent: 2},
			available: true,
			why:       "full corroboration cannot substitute for a missing requirement",
		},
		{
			name: "nothing at all",
			m:    entryplan.ConfidenceMetrics{}, available: true,
			why: "an empty census is not a plan resting on little; it is no plan",
		},
		{
			name: "nothing required and nothing available",
			m:    entryplan.ConfidenceMetrics{}, available: false,
		},
		{
			name: "required items present but the caller never declared any",
			m: entryplan.ConfidenceMetrics{RequiredTotal: 0, RequiredPresent: 4,
				OptionalTotal: 1, OptionalPresent: 1},
			available: true,
			why:       "presents with no requirements is a malformed census, not a strong one",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := entryplan.ClassifyConfidence(c.m, c.available)
			if got != entryplan.ConfidenceInsufficientData {
				t.Errorf("ClassifyConfidence(%+v, %v) = %q, want INSUFFICIENT_DATA — %s",
					c.m, c.available, got, c.why)
			}
			if got == entryplan.ConfidenceLow {
				t.Error("missing evidence was graded LOW: 'trust it less' and 'there is " +
					"nothing to trust' are different sentences")
			}
		})
	}
}

// Availability is checked BEFORE classification — stated as an ordering, not as an outcome.
//
// For every metrics value that grades to something, flipping available to false must return
// INSUFFICIENT_DATA. If the grading ran first and the availability check merely adjusted the
// result afterwards, some combination would survive; none may.
func TestAvailabilityIsCheckedBeforeClassification(t *testing.T) {
	for _, m := range []entryplan.ConfidenceMetrics{
		{RequiredTotal: 1, RequiredPresent: 1},
		{RequiredTotal: 1, RequiredPresent: 1, OptionalTotal: 2, OptionalPresent: 1},
		{RequiredTotal: 1, RequiredPresent: 1, OptionalTotal: 2, OptionalPresent: 2},
		{RequiredTotal: 4, RequiredPresent: 4, OptionalTotal: 9, OptionalPresent: 9},
	} {
		if g := entryplan.ClassifyConfidence(m, true); g == entryplan.ConfidenceInsufficientData {
			t.Fatalf("%+v is supposed to be gradable when available", m)
		}
		if g := entryplan.ClassifyConfidence(m, false); g != entryplan.ConfidenceInsufficientData {
			t.Errorf("ClassifyConfidence(%+v, false) = %q — the grade was computed and then "+
				"patched, instead of the availability being checked first", m, g)
		}
	}
}

// The grading itself, once availability is settled. Structural: every branch is an ordering
// or an emptiness, with no numeric threshold anywhere, because a cut-off invented before any
// outcome data exists would be quoted afterwards as though it had been measured.
func TestConfidenceGrading(t *testing.T) {
	cases := []struct {
		name string
		m    entryplan.ConfidenceMetrics
		want entryplan.Confidence
	}{
		{"everything, required and corroborating",
			entryplan.ConfidenceMetrics{RequiredTotal: 3, RequiredPresent: 3, OptionalTotal: 2, OptionalPresent: 2},
			entryplan.ConfidenceHigh},
		{"required complete, corroboration partial",
			entryplan.ConfidenceMetrics{RequiredTotal: 3, RequiredPresent: 3, OptionalTotal: 2, OptionalPresent: 1},
			entryplan.ConfidenceMedium},
		{"required complete, nothing corroborates it",
			entryplan.ConfidenceMetrics{RequiredTotal: 3, RequiredPresent: 3, OptionalTotal: 2, OptionalPresent: 0},
			entryplan.ConfidenceLow},
		{"required complete, no corroboration was even asked for",
			entryplan.ConfidenceMetrics{RequiredTotal: 3, RequiredPresent: 3},
			entryplan.ConfidenceLow},
		{"more corroboration than was asked for is not extra credit",
			entryplan.ConfidenceMetrics{RequiredTotal: 1, RequiredPresent: 1, OptionalTotal: 1, OptionalPresent: 5},
			entryplan.ConfidenceHigh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entryplan.ClassifyConfidence(c.m, true); got != c.want {
				t.Errorf("ClassifyConfidence(%+v, true) = %q, want %q", c.m, got, c.want)
			}
		})
	}
}

// Confidence is a SECOND AXIS, not a restatement of Status. All four grades are reachable, so
// the vocabulary is not three dead constants and an INSUFFICIENT_DATA.
func TestEveryConfidenceGradeIsReachable(t *testing.T) {
	seen := map[entryplan.Confidence]bool{}
	for _, m := range []entryplan.ConfidenceMetrics{
		{RequiredTotal: 1, RequiredPresent: 1, OptionalTotal: 1, OptionalPresent: 1},
		{RequiredTotal: 1, RequiredPresent: 1, OptionalTotal: 2, OptionalPresent: 1},
		{RequiredTotal: 1, RequiredPresent: 1},
		{},
	} {
		seen[entryplan.ClassifyConfidence(m, true)] = true
	}
	for _, want := range []entryplan.Confidence{
		entryplan.ConfidenceHigh, entryplan.ConfidenceMedium,
		entryplan.ConfidenceLow, entryplan.ConfidenceInsufficientData,
	} {
		if !seen[want] {
			t.Errorf("%s is unreachable", want)
		}
	}
}

// The two axes on EP-2's OWN OUTPUT: a genuinely complete snapshot grades HIGH, and its Status
// is still INSUFFICIENT_DATA.
//
// This is the row confidence.go's pairing table now names explicitly, and it is the exact
// opposite of what this test used to assert. It was TestComputePlanIsNeverConfidentUnderEP1 and
// it required INSUFFICIENT_DATA confidence for a fixture that had quietly stopped being
// complete: EP-2 added required item #4, the entry semantic, and the fixture never grew the
// field. So it passed by being handed INCOMPLETE evidence while claiming to prove something
// about complete evidence — and its stated reason ("a rule that turns evidence into a price is
// itself a required input") contradicted plan.go's own rule, A MISSING RULE IS NOT MISSING
// EVIDENCE. The missing rule is reported as a REASON and held on the STATUS axis; it is not
// counted as absent evidence.
//
// Delete EntrySemantic from the fixture and this test must fail. That is the mutation it exists
// to kill, and it is the one the old version had already lost to.
func TestACompleteSnapshotGradesHighWhileTheStatusStaysInsufficient(t *testing.T) {
	complete := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice: entryplan.PriceObservation{Status: entryplan.Available, Value: f(902.5)},
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: f(18.25), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime: entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
		AdjustmentAge: entryplan.AdjustmentAge{BarsAgo: i(63)},
	}
	got := entryplan.ComputePlan(complete)

	if got.Confidence != entryplan.ConfidenceHigh {
		t.Errorf("a complete snapshot graded %q, want HIGH — every required item and every "+
			"corroborating item is in hand, so grading it lower would report the missing RULE "+
			"as missing EVIDENCE, which is the one thing the second axis exists to prevent",
			got.Confidence)
	}
	if got.Status != entryplan.StatusInsufficientData {
		t.Errorf("Status = %q over a plan with no prices in it — EP-2 computes no entry, and a "+
			"confident census must not promote itself into a verdict", got.Status)
	}

	// And the plan SAYS which axis is short, so a reader is not sent looking for data that is
	// not what is missing.
	// EP-5 REPLACED THE SENTENCE THIS CHECKED. Until EP-4 the reason was an unconditional
	// ENTRY_EVALUATION_NOT_IMPLEMENTED — "there is no rule yet". EP-5 wrote the rule, so that
	// reason is gone and the plan now names what is ACTUALLY short: this fixture carries no
	// MA20/MA60/BaseLow, so there is no support to centre a pullback zone on.
	//
	// The two-axis contract is unchanged and is still what this test defends: complete
	// EVIDENCE grades HIGH while the STATUS stays INSUFFICIENT_DATA. What moved is only the
	// reason's content — from "the rule is missing" to "the levels are missing".
	var named bool
	for _, r := range got.Reasons {
		if r == entryplan.ReasonStatusEntryZoneUnavailable {
			named = true
		}
		switch r {
		case entryplan.ReasonSnapshotMalformed,
			entryplan.ReasonPriceBasisUnavailable,
			entryplan.ReasonCurrentPriceUnavailable,
			entryplan.ReasonMarketRegimeUnavailable,
			entryplan.ReasonEntrySemanticUnavailable:
			t.Errorf("a complete snapshot came back reporting %q — the plan is blaming the "+
				"input for the absence of a rule", r)
		}
	}
	if !named {
		t.Errorf("reasons = %v and none of them is %q — an INSUFFICIENT_DATA status over "+
			"complete evidence with no reason attached is a shrug", got.Reasons,
			entryplan.ReasonStatusEntryZoneUnavailable)
	}
}
