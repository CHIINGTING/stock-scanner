package entryplan_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── the two EP-4 steps, driven DIRECTLY ───────────────────────────────────────────────
//
// ComputeInvalidation and ComputeTargets are exported and take the published zone (and, for the
// targets, the published invalidation) as ARGUMENTS. That is what makes each step testable on
// its own rule instead of only through a whole Snapshot — the split levels.go argues for at
// length — and it is also what lets these tests plant inputs ComputePlan cannot produce.
//
// Everything here is a claim about ONE step. The end-to-end numbers are riskCases()'.

// rawZone is a published-shaped zone: two positive tick-aligned bounds on the raw series.
func rawZone(low, high float64) *entryplan.PriceZone {
	return &entryplan.PriceZone{Low: low, High: high, PriceBasis: entryplan.PriceBasisRaw,
		Basis: []string{"MA20"}}
}

// stopSnapshot is a minimal snapshot for the invalidation step: the two structural levels, the
// base low's kind, and an ATR for the fallback.
func stopSnapshot(ma60, baseLow *float64, kind entryplan.BaseLowKind,
	atrValue *float64) entryplan.Snapshot {
	in := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
		BaseLowKind: kind,
	}
	if ma60 != nil {
		in.MA60 = obs(*ma60)
	}
	if baseLow != nil {
		in.BaseLow = obs(*baseLow)
	}
	if atrValue != nil {
		in.ATR = atr(*atrValue)
	}
	return in
}

// dispositionOf finds one stop source's row in a candidate list.
func dispositionOf(cands []entryplan.StopCandidate,
	src entryplan.StopSource) entryplan.RiskLevelDisposition {
	for _, c := range cands {
		if c.Source == src {
			return c.Disposition
		}
	}
	return ""
}

// ── the selection rule, on the exact numbers §3 of the review was argued with ─────────

// THE RULE: the HIGHEST eligible STRUCTURAL level below the zone, and the derivation is NOT
// SelectCenter's.
//
// SelectCenter takes the max because it is answering "which support will price be filled at
// first". This step takes the max because every structural candidate is a SUFFICIENT FALSIFIER
// — losing the base low says the supply/demand balance the setup rests on has changed, losing
// the MA60 says the trend context the entry assumed is gone — so
//
//	thesis_holds(p) ⟹ p > c₁ ∧ … ∧ p > cₙ ⟹ p > max(cᵢ)
//
// and the boundary of that conjunction is the maximum. The two questions are different and the
// answers only happen to coincide in direction; this test separates the rule from all three
// plausible wrong ones on one fixture, in the shape
// TestThePullbackCentreIsTheNearestSupportBelowTheMarket uses for the entry.
func TestTheInvalidationIsTheNearestStructuralLevelBelowTheZone(t *testing.T) {
	zone := rawZone(92, 96)
	in := stopSnapshot(f(88), f(91), entryplan.BaseLowConsolidationBase, f(4))

	got := entryplan.ComputeInvalidation(in, zone)
	if got.Invalidation == nil {
		t.Fatalf("no invalidation: %q", got.Trace.Rejection)
	}
	// 91 is the answer. min() gives 88, the mean gives 89.5, and the median of the three
	// eligible candidates (88, 90 buffer, 91) gives 90 — so this single case separates the
	// required rule from all three.
	if got.Invalidation.Price != 91 {
		t.Errorf("invalidation = %v, want 91 — the NEAREST structural level below the zone. "+
			"min() would answer 88, which keeps a thesis alive past the point the base low "+
			"already falsified it AND inflates the risk denominator from 5 to 8, publishing "+
			"the same setup with a 60%% larger risk at a flattering ratio", got.Invalidation.Price)
	}
	if got.Trace.SelectedSource != entryplan.StopSourceBaseLow {
		t.Errorf("selected %q, want BASE_LOW", got.Trace.SelectedSource)
	}
	// The loser is ELIGIBLE_NOT_SELECTED, not rejected: a reader must be able to see that a
	// deeper structural level existed and was passed over deliberately.
	if d := dispositionOf(got.Trace.Candidates, entryplan.StopSourceMA60); d != entryplan.RiskLevelEligibleNotSelected {
		t.Errorf("the MA60 at 88 is %q, want ELIGIBLE_NOT_SELECTED", d)
	}
}

// THE TIER, and it is the decision a single merged max() would silently reverse.
//
// The fallback sits just below zone.Low by construction, so a merged max() over all three
// candidates selects it in almost every plan that has a structural level at all. On these
// numbers: zone 92 → 96 with an ATR of 4 puts the fallback at 90, and a base low at 89 would
// then never be used — yet at 89.5 price is still INSIDE the base that defines the setup, so
// the thesis is intact and a stop at 90 would declare it dead while the structure is untouched.
// That is a loss budget, not an invalidation.
func TestTheVolatilityFallbackNeverPreEmptsAStructuralLevel(t *testing.T) {
	zone := rawZone(92, 96)

	// A structural level BELOW the buffer. The structure must win.
	deep := entryplan.ComputeInvalidation(
		stopSnapshot(nil, f(89), entryplan.BaseLowConsolidationBase, f(4)), zone)
	if deep.Invalidation == nil || deep.Invalidation.Price != 89 {
		t.Fatalf("invalidation = %v, want 89 — a merged max() over the three candidates would "+
			"answer 90 (the buffer) and leave the observed level unused", deep.Invalidation)
	}
	if d := dispositionOf(deep.Trace.Candidates, entryplan.StopSourceZoneATRBuffer); d != entryplan.RiskLevelFallbackNotNeeded {
		t.Errorf("the buffer is %q, want FALLBACK_NOT_NEEDED — the tier decision has to be "+
			"visible on the row, not only in the source", d)
	}

	// With no structural level, the SAME buffer is used, and it says so.
	only := entryplan.ComputeInvalidation(
		stopSnapshot(nil, nil, entryplan.BaseLowConsolidationBase, f(4)), zone)
	if only.Invalidation == nil || only.Invalidation.Price != 90 {
		t.Fatalf("invalidation = %v, want the fallback at 90", only.Invalidation)
	}
	if only.Trace.SelectedSource != entryplan.StopSourceZoneATRBuffer {
		t.Errorf("selected %q, want ZONE_LOW_MINUS_ATR", only.Trace.SelectedSource)
	}
	// The heuristic is LABELLED on the published level. The two answers above are 89 and 90 —
	// one dollar apart and not the same kind of claim.
	if !reflect.DeepEqual(only.Invalidation.Basis,
		[]string{"ZONE_LOW_MINUS_ATR", "HEURISTIC_VOLATILITY_BUFFER"}) {
		t.Errorf("basis = %v, want the source AND the heuristic marker", only.Invalidation.Basis)
	}
	if !reflect.DeepEqual(deep.Invalidation.Basis, []string{"BASE_LOW"}) {
		t.Errorf("an observed level carries basis %v — the heuristic marker must not travel "+
			"with it", deep.Invalidation.Basis)
	}
}

// A MISSING ATR REMOVES THE FALLBACK AND NOTHING ELSE.
//
// The review's §4 names this specifically: "do not let ATR missing → Invalidation necessarily
// unavailable, unless the only candidate is ATR-derived". Through ComputePlan the situation
// cannot arise — a zone exists only if the width rule had an ATR — so it is driven directly
// here, which is the whole reason the step takes its arguments rather than deriving them.
func TestAnAbsentATRLeavesTheStructuralCandidatesAlone(t *testing.T) {
	zone := rawZone(92, 96)

	// No ATR, one structural level: the level is still published.
	withLevel := entryplan.ComputeInvalidation(
		stopSnapshot(f(90), nil, entryplan.BaseLowConsolidationBase, nil), zone)
	if withLevel.Invalidation == nil {
		t.Fatalf("no invalidation with an absent ATR and a usable MA60: %q — an absent "+
			"multiplier must remove the FALLBACK, not the answer", withLevel.Trace.Rejection)
	}
	if withLevel.Invalidation.Price != 90 {
		t.Errorf("invalidation = %v, want the MA60 at 90", withLevel.Invalidation.Price)
	}
	if d := dispositionOf(withLevel.Trace.Candidates, entryplan.StopSourceZoneATRBuffer); d != entryplan.RiskLevelUnavailable {
		t.Errorf("the buffer is %q, want LEVEL_UNAVAILABLE — there is no multiplier, so there "+
			"is no fallback, and no substitute multiplier is invented", d)
	}

	// No ATR and no structural level: NOW there is no invalidation, because the only
	// candidate left was the ATR-derived one.
	withNothing := entryplan.ComputeInvalidation(
		stopSnapshot(nil, nil, entryplan.BaseLowConsolidationBase, nil), zone)
	if withNothing.Invalidation != nil {
		t.Errorf("invalidation = %v with no ATR and no level — every way to produce one is a "+
			"fabrication", withNothing.Invalidation.Price)
	}
	// AND THE CODE SAYS WHICH KIND OF "NO" THIS IS. Until EP-5 it was NO_VALID_INVALIDATION,
	// which was the only code the step had; EP-5 split that one into three on spec §17's
	// instruction, and this fixture is squarely in the EVIDENCE half — nothing was compared
	// with the zone, because there was nothing to compare. Publishing NO_VALID_INVALIDATION
	// here would tell the decision layer that the MARKET offers no boundary, and it would
	// answer NO_VALID_ENTRY where INSUFFICIENT_DATA is the truth.
	if withNothing.Trace.Rejection != entryplan.ReasonInvalidationEvidenceUnavailable {
		t.Errorf("rejection = %q, want INVALIDATION_EVIDENCE_UNAVAILABLE", withNothing.Trace.Rejection)
	}
	if r := entryplan.ReadAbsence(withNothing.Trace.Rejection); r != entryplan.AbsenceEvidenceGap {
		t.Errorf("the decision layer reads it as %q, want EVIDENCE_GAP", r)
	}
	// EVERY row must say it was never comparable, or the fixture is in the other half.
	for _, cand := range withNothing.Trace.Candidates {
		if cand.Disposition == entryplan.RiskLevelNotBelowZoneLow ||
			cand.Disposition == entryplan.RiskLevelNotAPrice {
			t.Errorf("%s was COMPARED (%s), so this case reaches the verdict branch",
				cand.Source, cand.Disposition)
		}
	}
}

// A CANDIDATE AT OR ABOVE THE ZONE IS EXCLUDED, NEVER SHAVED.
//
// The two repairs available are zone.Low × 0.99 and zone.Low − one tick, and both produce a
// number that is finite, positive, below the entry and on the grid — so nothing downstream can
// tell either apart from a level. This test plants candidates at, just above and far above the
// zone low and requires the answer to be an ABSENCE, not a nearby number.
func TestAStopCandidateAtOrAboveTheZoneIsExcluded(t *testing.T) {
	zone := rawZone(92, 96)
	for _, at := range []float64{92, 92.5, 100} {
		// No ATR, so the fallback cannot rescue the case and the answer is forced to be an
		// absence rather than a different number.
		got := entryplan.ComputeInvalidation(
			stopSnapshot(f(at), nil, entryplan.BaseLowConsolidationBase, nil), zone)
		if got.Invalidation != nil {
			t.Errorf("a candidate at %v produced an invalidation of %v — the shaved variants "+
				"(zone.Low × 0.99 = %v, zone.Low − one tick = %v) are both finite, positive, "+
				"below the entry and on the grid, which is exactly why neither is allowed",
				at, got.Invalidation.Price, zone.Low*0.99, zone.Low-0.1)
		}
		if got.Trace.Rejection != entryplan.ReasonNoValidInvalidation {
			t.Errorf("a candidate at %v: rejection = %q, want NO_VALID_INVALIDATION",
				at, got.Trace.Rejection)
		}
		if d := dispositionOf(got.Trace.Candidates, entryplan.StopSourceMA60); d != entryplan.RiskLevelNotBelowZoneLow {
			t.Errorf("a candidate at %v is %q, want NOT_BELOW_ZONE_LOW", at, d)
		}
	}
	// And one tick below IS accepted, so the screen is a boundary and not a blanket refusal.
	ok := entryplan.ComputeInvalidation(
		stopSnapshot(f(91.9), nil, entryplan.BaseLowConsolidationBase, nil), zone)
	if ok.Invalidation == nil || ok.Invalidation.Price != 91.9 {
		t.Errorf("a candidate one tick below the zone produced %v, want 91.90", ok.Invalidation)
	}
}

// NO ZONE, NO CODE OF OUR OWN.
//
// ZoneTrace.Rejection already says why there is no entry, so the stop step reports a candidate
// list of NOT_SCREENED rows and no reason — the same discipline ComputeMaxChase follows when
// there is no zone to chase into. A second code here would read as a second, independent
// failure and send a reader looking at the MA60.
func TestTheStopStepAddsNoCodeWhenThereIsNoZone(t *testing.T) {
	got := entryplan.ComputeInvalidation(
		stopSnapshot(f(90), f(91), entryplan.BaseLowConsolidationBase, f(4)), nil)
	if got.Invalidation != nil {
		t.Errorf("an invalidation of %v with no entry to be below", got.Invalidation.Price)
	}
	if got.Trace.Rejection != "" {
		t.Errorf("rejection = %q, want none — the zone step already said why there is no "+
			"entry", got.Trace.Rejection)
	}
	if len(got.Trace.Candidates) != len(entryplan.AllStopSources) {
		t.Fatalf("%d candidate rows, want the full width of the registry (%d) — a variable-"+
			"width list is how a reader loses the difference between \"considered and "+
			"rejected\" and \"this version of the rule never heard of it\"",
			len(got.Trace.Candidates), len(entryplan.AllStopSources))
	}
	for _, c := range got.Trace.Candidates {
		if c.Disposition != entryplan.RiskLevelNotScreened {
			t.Errorf("%q is %q, want NOT_SCREENED — the conditions were never evaluated, "+
				"which is a STATE and not a rejection", c.Source, c.Disposition)
		}
	}
}

// The MA60 is NOT a breakout candidate, and the base floor is.
//
// A breakout thesis is "the base was cleared and holds", and the base's own floor falsifies it
// directly — internal/scanner derives PivotHigh and BaseLow from the SAME window, so the pivot
// the zone is built on and this floor are the two ends of one structure. The MA60 is not part of
// it: a breakout can be alive far above the MA60 and dead the moment it falls back into its
// base, and an MA60 stop would also sit much lower and flatter every risk/reward.
func TestTheBreakoutThesisUsesTheBaseFloorAndNotTheMA60(t *testing.T) {
	zone := rawZone(105, 107)
	in := stopSnapshot(f(96), f(92), entryplan.BaseLowConsolidationBase, f(4))
	in.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Available,
		Semantic: entryplan.EntrySemanticBreakout}

	got := entryplan.ComputeInvalidation(in, zone)
	if got.Invalidation == nil || got.Invalidation.Price != 92 {
		t.Fatalf("invalidation = %v, want the base floor at 92", got.Invalidation)
	}
	if d := dispositionOf(got.Trace.Candidates, entryplan.StopSourceMA60); d != entryplan.RiskLevelWrongSemantic {
		t.Errorf("the MA60 is %q under a breakout, want NOT_A_CANDIDATE_FOR_SEMANTIC — and "+
			"note that it is REPORTED rather than dropped, so the candidate list stays the "+
			"width of the registry", d)
	}
	// The MA60 at 96 is nearer the zone than the base floor at 92, so if it were a candidate
	// the max() rule would have chosen it — which is what makes this assertion about
	// MEMBERSHIP rather than about the ordering.
	if got.Trace.SelectedSource != entryplan.StopSourceBaseLow {
		t.Errorf("selected %q, want BASE_LOW", got.Trace.SelectedSource)
	}
}

// The base low's KIND decides whether it is a candidate at all, and NOT its value.
//
// The three non-structural kinds are planted with the SAME number, so the only thing that
// changes between them is the fact the field records. Removing the gate publishes 91 in all
// four rows.
func TestOnlyAConsolidationBaseLowIsAStructuralStop(t *testing.T) {
	zone := rawZone(92, 96)
	for _, c := range []struct {
		kind      entryplan.BaseLowKind
		wantPrice float64
		wantDisp  entryplan.RiskLevelDisposition
	}{
		{entryplan.BaseLowConsolidationBase, 91, entryplan.RiskLevelSelected},
		{entryplan.BaseLowLatestBar, 90, entryplan.RiskLevelNotStructural},
		{entryplan.BaseLowKindUnknown, 90, entryplan.RiskLevelNotStructural},
		{"", 90, entryplan.RiskLevelNotStructural},
	} {
		t.Run(string(c.kind), func(t *testing.T) {
			got := entryplan.ComputeInvalidation(
				stopSnapshot(nil, f(91), c.kind, f(4)), zone)
			if got.Invalidation == nil {
				t.Fatalf("no invalidation: %q", got.Trace.Rejection)
			}
			if got.Invalidation.Price != c.wantPrice {
				t.Errorf("invalidation = %v, want %v — a single-session low is one session's "+
					"worst tick, it is redrawn every morning, and price trades through it "+
					"inside perfectly healthy pullbacks", got.Invalidation.Price, c.wantPrice)
			}
			if d := dispositionOf(got.Trace.Candidates, entryplan.StopSourceBaseLow); d != c.wantDisp {
				t.Errorf("the base low is %q, want %q", d, c.wantDisp)
			}
			if got.Trace.BaseLowKind != c.kind {
				t.Errorf("the trace records kind %q, want %q", got.Trace.BaseLowKind, c.kind)
			}
		})
	}
}

// THE BASE LOW'S KIND MUST NOT MOVE THE ZONE. EP-3's IdealEntryZone semantics are frozen.
//
// The two questions are different: "where is price likely to be bought" tolerates a short-term
// low, because it is a real place trades happened, while "where is the idea wrong" does not.
// So SelectCenter still accepts a base low of any kind as a CENTRE, and this is the test that
// says the EP-4 field did not leak into the EP-3 rule.
func TestTheBaseLowKindDoesNotMoveTheZone(t *testing.T) {
	var ref *entryplan.PriceZone
	for n, kind := range []entryplan.BaseLowKind{
		"", entryplan.BaseLowConsolidationBase, entryplan.BaseLowLatestBar,
		entryplan.BaseLowKindUnknown,
	} {
		in := zoneBase()
		// The base low is the CENTRE here: the MA20 is above the market, so the highest
		// eligible support is the base low itself. If the kind reached the zone rule, this is
		// the fixture where it would show.
		in.MA20, in.MA60, in.BaseLow = obs(102), obs(90), obs(92)
		in.BaseLowKind = kind

		p := entryplan.ComputePlan(in)
		if p.IdealEntry == nil {
			t.Fatalf("kind %q: no zone — reasons %v", kind, p.Reasons)
		}
		if p.EntryTrace.Zone.CenterSource != entryplan.LevelBaseLow {
			t.Fatalf("kind %q: the zone is centred on %q, so this fixture is not testing what "+
				"it claims", kind, p.EntryTrace.Zone.CenterSource)
		}
		if n == 0 {
			ref = p.IdealEntry
			continue
		}
		if p.IdealEntry.Low != ref.Low || p.IdealEntry.High != ref.High {
			t.Errorf("kind %q moved the zone from %v → %v to %v → %v — EP-4's field has "+
				"leaked into EP-3's rule, whose semantics are frozen", kind, ref.Low,
				ref.High, p.IdealEntry.Low, p.IdealEntry.High)
		}
	}
}

// ── the target step ──────────────────────────────────────────────────────────────────

// THE RISK IS MEASURED FROM zone.High, AND THE MIDPOINT IS THE SPECIFIC ALTERNATIVE.
//
// On these numbers the difference is visible in both terms at once: from zone.High the risk is
// 8 and the reward 14, so the ratio is 1.75; from the midpoint (94) the risk would be 6 and the
// reward 16, so the ratio would be 2.67 — a 52% better-looking trade produced by nothing but
// the choice of entry. The bias has a direction, and it is the flattering one.
func TestTheRiskIsMeasuredFromTheTopOfTheZoneAndNotTheMidpoint(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 88, PriceBasis: entryplan.PriceBasisRaw}
	in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw, PivotHigh: obs(110)}

	got := entryplan.ComputeTargets(in, zone, inv)
	if got.RiskReward == nil {
		t.Fatalf("no risk/reward: %q / %q", got.Trace.Target1Rejection, got.Trace.RiskRejection)
	}
	if got.RiskReward.RiskPerShare != 8 {
		t.Errorf("risk = %v, want 96 − 88 = 8. The midpoint would give 94 − 88 = 6, which is "+
			"the number that makes every plan in the archive look better than it is",
			got.RiskReward.RiskPerShare)
	}
	if *got.Trace.EntryUsed != 96 {
		t.Errorf("the trace records entry %v, want zone.High = 96", *got.Trace.EntryUsed)
	}
	// Target1 = min(96 + 2 × 8, 110) = min(112, 110) = 110, reward 14, ratio 1.75.
	if got.Target1 == nil || got.Target1.Price != 110 {
		t.Fatalf("target 1 = %v, want the resistance at 110", got.Target1)
	}
	if got.RiskReward.RewardPerShare != 14 || got.RiskReward.Ratio != 1.75 {
		t.Errorf("reward %v, ratio %v — want 14 and 1.75. From the midpoint the same plan "+
			"would read 16 and 2.67", got.RiskReward.RewardPerShare, got.RiskReward.Ratio)
	}
	if got.RiskReward.Target != "TARGET_1" {
		t.Errorf("the ratio names %q, want the literal \"TARGET_1\" — an R:R measured to a "+
			"3.5R second target would be an unfalsifiable number on a plan most positions "+
			"never hold for", got.RiskReward.Target)
	}
}

// A NEARER RESISTANCE CLAMPS TARGET 1 DOWN, AND THE LEGACY DOES THE OPPOSITE.
//
// internal/scanner/scorer.go:747-750 is `if bbUpper > t1 { t1 = bbUpper }`, i.e. the target is
// pushed FURTHER AWAY by the level in the way. This test is the inversion, on numbers where the
// two answers differ by 12 元 and the ratio by 3x.
func TestTheNearerOfTwoRAndResistanceWins(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 90, PriceBasis: entryplan.PriceBasisRaw}
	base := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw}

	// risk = 6, so 2R = 108. A resistance at 100 is nearer and must win.
	near := base
	near.PivotHigh = obs(100)
	gotNear := entryplan.ComputeTargets(near, zone, inv)
	if gotNear.Target1 == nil || gotNear.Target1.Price != 100 {
		t.Fatalf("target 1 = %v, want the resistance at 100 — max() would answer 108, a "+
			"target beyond a level the stock has not cleared", gotNear.Target1)
	}
	if gotNear.Trace.Target1Binding != entryplan.Target1FromResistance {
		t.Errorf("binding = %q, want RESISTANCE", gotNear.Trace.Target1Binding)
	}
	if !closeEnough(gotNear.RiskReward.Ratio, 4.0/6.0) {
		t.Errorf("ratio = %v, want 4/6 = 0.67 — with max() it would read 2.00, and the "+
			"difference is whether a reader takes this trade", gotNear.RiskReward.Ratio)
	}

	// A resistance FURTHER than 2R leaves the R multiple in place.
	far := base
	far.PivotHigh = obs(200)
	gotFar := entryplan.ComputeTargets(far, zone, inv)
	if gotFar.Target1 == nil || gotFar.Target1.Price != 108 {
		t.Fatalf("target 1 = %v, want the 2R target at 108", gotFar.Target1)
	}
	if gotFar.Trace.Target1Binding != entryplan.Target1FromRMultiple {
		t.Errorf("binding = %q, want R_MULTIPLE", gotFar.Trace.Target1Binding)
	}
	// The R multiples on the two rows describe the PUBLISHED prices, not the raw ones.
	if gotNear.Target1.RMultiple == nil || !closeEnough(*gotNear.Target1.RMultiple, 4.0/6.0) {
		t.Errorf("the clamped target carries R multiple %v, want 0.67 — stamping 2.0 on it "+
			"would be the one field in the plan a reader cannot check",
			gotNear.Target1.RMultiple)
	}
	if gotFar.Target1.RMultiple == nil || *gotFar.Target1.RMultiple != 2 {
		t.Errorf("the unclamped target carries R multiple %v, want 2", gotFar.Target1.RMultiple)
	}
}

// TARGET 2 IS 3.5R FROM THE ORIGINAL RISK, AND NOT A DISTANCE FROM TARGET 1.
//
// The fixture is a plan whose Target1 was CLAMPED to a resistance at 100, so the two rules give
// different answers: 3.5R from the entry is 96 + 21 = 117, while "Target1 plus something" would
// inherit the clamp and be wrong by the size of it — and the R multiple stamped on the row
// would stop being true.
func TestTheSecondTargetIsMeasuredFromTheOriginalRisk(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 90, PriceBasis: entryplan.PriceBasisRaw}
	in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw, PivotHigh: obs(100)}

	got := entryplan.ComputeTargets(in, zone, inv)
	if got.Target1 == nil || got.Target1.Price != 100 {
		t.Fatalf("target 1 = %v, want the clamped 100", got.Target1)
	}
	if got.Target2 == nil || got.Target2.Price != 117 {
		t.Fatalf("target 2 = %v, want 96 + 3.5 × 6 = 117 — deriving it from the CLAMPED "+
			"target 1 would compound the clamp", got.Target2)
	}
	if got.Target2.RMultiple == nil || *got.Target2.RMultiple != 3.5 {
		t.Errorf("target 2 carries R multiple %v, want 3.5", got.Target2.RMultiple)
	}
	// And the two multiples are NOT the same rule: 3.5 is not 2 × 1.75, and a second target at
	// exactly twice the first would read as "double it" rather than as a level.
	if got.Target1.RMultiple != nil && *got.Target2.RMultiple == 2**got.Target1.RMultiple {
		t.Errorf("the second target is exactly twice the first (%v, %v) — on this fixture "+
			"that can only happen if it was derived from Target1", *got.Target1.RMultiple,
			*got.Target2.RMultiple)
	}
}

// THE RISK DENOMINATOR IS REFUSED RATHER THAN INVENTED — the RESERVED code's producer.
//
// Both branches are unreachable from ComputePlan, because ComputeInvalidation publishes a level
// strictly below zone.Low on the zone's own basis. They exist because this function is EXPORTED
// and takes the pair as ARGUMENTS, so a caller can hand it an inconsistent one — and because
// the alternative to the guard is a negative risk, a negative reward and a positive-looking
// ratio computed from two sign errors.
func TestTheRiskDenominatorIsRefusedRatherThanInvented(t *testing.T) {
	zone := rawZone(92, 96)
	in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw, PivotHigh: obs(110)}

	for _, c := range []struct {
		name string
		inv  *entryplan.Invalidation
		want entryplan.RiskRejection
	}{
		{
			name: "a stop above the entry gives a non-positive risk",
			inv:  &entryplan.Invalidation{Price: 96, PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.RiskRejectNotPositive,
		},
		{
			name: "a stop far above the entry",
			inv:  &entryplan.Invalidation{Price: 200, PriceBasis: entryplan.PriceBasisRaw},
			want: entryplan.RiskRejectNotPositive,
		},
		{
			name: "a stop on another price series cannot be subtracted",
			inv:  &entryplan.Invalidation{Price: 88, PriceBasis: entryplan.PriceBasisAdjusted},
			want: entryplan.RiskRejectBasisMismatch,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := entryplan.ComputeTargets(in, zone, c.inv)
			if got.Target1 != nil || got.Target2 != nil || got.RiskReward != nil {
				t.Errorf("targets %v / %v and R:R %v were published from an unusable risk",
					got.Target1, got.Target2, got.RiskReward)
			}
			if got.Trace.RiskRejection != c.want {
				t.Errorf("risk rejection = %q, want %q", got.Trace.RiskRejection, c.want)
			}
			if got.Trace.Target1Rejection != entryplan.ReasonRiskNotEstablished {
				t.Errorf("target rejection = %q, want RISK_NOT_ESTABLISHED",
					got.Trace.Target1Rejection)
			}
			if got.Trace.Risk != nil {
				t.Errorf("a risk of %v was recorded for a pair that has none", *got.Trace.Risk)
			}
		})
	}
	// Both members of the sub-reason registry must have been reached, or one of them is a
	// dead code with an excuse.
	if len(entryplan.AllRiskRejections) != 2 {
		t.Errorf("%d risk rejections in the registry and this test drives 2",
			len(entryplan.AllRiskRejections))
	}
}

// NO ZONE OR NO STOP → NO TARGET, and NO CODE OF THE TARGET STEP'S OWN.
//
// The zone trace and the stop trace already say why each is missing; a second code here would
// read as a second, independent failure.
func TestTheTargetStepAddsNoCodeWhenItsInputsAreMissing(t *testing.T) {
	in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw, PivotHigh: obs(110)}
	inv := &entryplan.Invalidation{Price: 88, PriceBasis: entryplan.PriceBasisRaw}

	for _, c := range []struct {
		name string
		zone *entryplan.PriceZone
		inv  *entryplan.Invalidation
	}{
		{"no zone", nil, inv},
		{"no invalidation", rawZone(92, 96), nil},
		{"neither", nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := entryplan.ComputeTargets(in, c.zone, c.inv)
			if got.Target1 != nil || got.Target2 != nil || got.RiskReward != nil {
				t.Errorf("something was published: %v / %v / %v", got.Target1, got.Target2,
					got.RiskReward)
			}
			if got.Trace.Target1Rejection != "" || got.Trace.Target2Rejection != "" ||
				got.Trace.RiskRejection != "" {
				t.Errorf("codes %q / %q / %q — the zone and stop traces already say why",
					got.Trace.Target1Rejection, got.Trace.Target2Rejection,
					got.Trace.RiskRejection)
			}
			// The resistance row is still recorded, at the stage it reached.
			if got.Trace.Resistance.Disposition == "" {
				t.Error("the resistance candidate carries no disposition — a blank row is one " +
					"nobody filled in")
			}
		})
	}
}

// THE VALUATION IS OPTIONAL SANITY, IN BOTH DIRECTIONS.
//
// It may LOWER Target2 and it may never remove it, and an UNSUITABLE model may do neither. The
// four rows here are the same 110 元 number under four different availabilities, so the only
// thing that changes is the fact the fields record.
func TestTheValuationCeilingLowersTargetTwoAndNeverRemovesIt(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 90, PriceBasis: entryplan.PriceBasisRaw}
	// risk 6, so 3.5R = 117 and a ceiling of 110 binds.
	for _, c := range []struct {
		name    string
		val     entryplan.ValuationEvidence
		want    float64
		outcome entryplan.ValuationCeilingOutcome
	}{
		{"suitable and binding", valuation(110, entryplan.SuitabilitySuitable), 110,
			entryplan.ValuationCeilingApplied},
		{"unsuitable never clamps", valuation(110, entryplan.SuitabilityUnsuitable), 117,
			entryplan.ValuationCeilingModelUnsuitable},
		{"an unreadable verdict never clamps", valuation(110, "SOMETHING_NEW"), 117,
			entryplan.ValuationCeilingModelUnsuitable},
		{"absent", entryplan.ValuationEvidence{}, 117,
			entryplan.ValuationCeilingUnavailable},
		{"present but not a price", entryplan.ValuationEvidence{Status: entryplan.Available,
			Suitability: entryplan.SuitabilitySuitable}, 117,
			entryplan.ValuationCeilingUnavailable},
		{"above the 3.5R target", valuation(200, entryplan.SuitabilitySuitable), 117,
			entryplan.ValuationCeilingNotBinding},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
				PriceBasis: entryplan.PriceBasisRaw, Valuation: c.val}
			got := entryplan.ComputeTargets(in, zone, inv)
			if got.Target2 == nil {
				t.Fatalf("no second target — the valuation is a SANITY BOUND and not a "+
					"dependency: %q", got.Trace.Target2Rejection)
			}
			if got.Target2.Price != c.want {
				t.Errorf("target 2 = %v, want %v", got.Target2.Price, c.want)
			}
			if got.Trace.ValuationCeiling != c.outcome {
				t.Errorf("ceiling outcome = %q, want %q", got.Trace.ValuationCeiling, c.outcome)
			}
			// The APPLIED case, and only it, is marked on the published row.
			var marked bool
			for _, b := range got.Target2.Basis {
				if b == "VALUATION_CEILING_APPLIED" {
					marked = true
				}
			}
			if marked != (c.outcome == entryplan.ValuationCeilingApplied) {
				t.Errorf("target 2 basis = %v and the outcome is %q — the marker must travel "+
					"with a clamped number and only with it", got.Target2.Basis, c.outcome)
			}
			// Target1 and the R:R are untouched in every row: they are price-structure
			// arithmetic and do not consult the valuation at all.
			if got.Target1 == nil || got.Target1.Price != 108 || got.RiskReward == nil {
				t.Errorf("the valuation moved the first target (%v) or the R:R (%v)",
					got.Target1, got.RiskReward)
			}
		})
	}
}

// AN INVERTED TARGET PAIR IS WITHDRAWN, NOT SWAPPED AND NOT RAISED.
func TestAnInvertedTargetPairIsWithdrawnRatherThanReordered(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 90, PriceBasis: entryplan.PriceBasisRaw}
	in := entryplan.Snapshot{Symbol: "2330", AsOf: "2026-09-09",
		PriceBasis: entryplan.PriceBasisRaw,
		PivotHigh:  obs(105), // clamps target 1 to 105
		Valuation:  valuation(100, entryplan.SuitabilitySuitable),
	}
	got := entryplan.ComputeTargets(in, zone, inv)
	if got.Target1 == nil || got.Target1.Price != 105 {
		t.Fatalf("target 1 = %v, want 105", got.Target1)
	}
	if got.Target2 != nil {
		t.Errorf("target 2 = %v — a ceiling at 100 is below the first target, so the pair is "+
			"not a pair. SWAPPING them would publish the ceiling as the nearer target and "+
			"make both R multiples false; RAISING the second to meet the first would discard "+
			"the only independent evidence in the step", got.Target2.Price)
	}
	if got.Trace.Target2Rejection != entryplan.ReasonTarget2BelowTarget1 {
		t.Errorf("rejection = %q, want TARGET_2_BELOW_TARGET_1", got.Trace.Target2Rejection)
	}
	if got.RiskReward == nil || got.RiskReward.Ratio != 1.5 {
		t.Errorf("the R:R was disturbed by the second target: %v", got.RiskReward)
	}

	// A ceiling at or below the ENTRY is a different sentence with its own code.
	in.Valuation = valuation(95, entryplan.SuitabilitySuitable)
	below := entryplan.ComputeTargets(in, zone, inv)
	if below.Target2 != nil {
		t.Errorf("target 2 = %v with a fair value below the entry", below.Target2.Price)
	}
	if below.Trace.Target2Rejection != entryplan.ReasonValuationCeilingBelowEntry {
		t.Errorf("rejection = %q, want VALUATION_CEILING_BELOW_ENTRY",
			below.Trace.Target2Rejection)
	}
}

// The two steps are PURE and do not alias their arguments.
//
// TestComputePlanIsPureAndDoesNotAliasItsInput covers ComputePlan; these are the same claim for
// the exported steps, which take POINTERS to a zone and an invalidation the caller still holds.
func TestTheRiskStepsDoNotAliasTheirArguments(t *testing.T) {
	zone := rawZone(92, 96)
	inv := &entryplan.Invalidation{Price: 88, PriceBasis: entryplan.PriceBasisRaw,
		Basis: []string{"MA60"}}
	in := stopSnapshot(f(90), f(91), entryplan.BaseLowConsolidationBase, f(4))
	in.PivotHigh = obs(110)
	in.Valuation = valuation(200, entryplan.SuitabilitySuitable)

	stop := entryplan.ComputeInvalidation(in, zone)
	targets := entryplan.ComputeTargets(in, zone, inv)

	// Mutating the caller's inputs afterwards must not change an already-returned answer.
	zone.Low, zone.High = 1, 2
	inv.Price = 1
	*in.MA60.Value = 1
	*in.Valuation.BaseTargetPrice = 1

	if stop.Invalidation.Price != 91 {
		t.Errorf("the returned invalidation changed to %v when the caller moved its own "+
			"numbers", stop.Invalidation.Price)
	}
	if targets.Target1 == nil || targets.Target1.Price != 110 ||
		targets.RiskReward.RiskPerShare != 8 {
		t.Errorf("the returned targets changed: %v / %v", targets.Target1, targets.RiskReward)
	}
	// And no published float is non-finite, which is the property the invariant gate backs up.
	for _, v := range []float64{stop.Invalidation.Price, targets.Target1.Price,
		targets.RiskReward.Ratio} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("a non-finite number escaped: %v", v)
		}
	}
}

// ── EP-5: the two RESERVED codes the invalidation split created ───────────────────────

// THE EVIDENCE GAP IS REFUSED RATHER THAN CALLED A VERDICT — the reserved code's producer.
//
// spec §17 required EP-5 to distinguish "upstream data missing" (a STATE) from "the search ran
// and there is no legal boundary" (a VERDICT), and that meant splitting one reason code into
// three. INVALIDATION_EVIDENCE_UNAVAILABLE is the state, and it is unreachable from ComputePlan
// for a reason worth knowing rather than worth glossing:
//
//	a PUBLISHED zone implies a usable ATR(14) ON THE ZONE'S OWN BASIS — zoneHalfWidth refuses
//	the zone otherwise — and that is exactly what bufferCandidate needs. So the volatility
//	fallback row always reaches the geometry screen, and the answer is always a FINDING.
//
// The branch exists because ComputeInvalidation is EXPORTED and takes the ZONE AS AN ARGUMENT,
// which is what makes the step separately testable and also what lets a caller hand it a zone
// from another price series. This test is that caller. It matters more than a normal reserved
// code because EP-5 READS this reason to choose a STATUS: if the branch silently answered
// NO_VALID_INVALIDATION instead, a missing input would be published as a verdict about a stock.
func TestTheStopEvidenceGapIsRefusedRatherThanCalledAVerdict(t *testing.T) {
	in := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice:  obs(95),
		ATR:           entryplan.ATREvidence{Status: entryplan.Available, Value: f(4), Period: 14, PriceBasis: entryplan.PriceBasisRaw},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available, Semantic: entryplan.EntrySemanticPullback},
	}

	// A zone on the ADJUSTED series with every candidate on the RAW one. NOTHING is comparable
	// with it — not the MA60, not the base low, and not the fallback, whose basis is the ATR's.
	zone := &entryplan.PriceZone{Low: 92, High: 96, PriceBasis: entryplan.PriceBasisAdjusted}
	got := entryplan.ComputeInvalidation(in, zone)

	if got.Invalidation != nil {
		t.Fatalf("an invalidation of %v was published from candidates none of which could be "+
			"compared with the zone", got.Invalidation)
	}
	if got.Trace.Rejection != entryplan.ReasonInvalidationEvidenceUnavailable {
		t.Fatalf("rejection = %q, want INVALIDATION_EVIDENCE_UNAVAILABLE — reporting "+
			"NO_VALID_INVALIDATION here would tell a reader that the MARKET offers no boundary "+
			"when what happened is that nothing was ever measured against the zone",
			got.Trace.Rejection)
	}
	// EVERY row must say it was not comparable, and NONE may say it failed the geometry: the
	// difference between the two codes IS this distinction, so a fixture where some row was
	// compared would be testing the other branch.
	var rows int
	for _, c := range got.Trace.Candidates {
		rows++
		if c.Disposition == entryplan.RiskLevelNotBelowZoneLow ||
			c.Disposition == entryplan.RiskLevelNotAPrice {
			t.Errorf("%s was actually COMPARED (%s), so this fixture reaches the verdict "+
				"branch and not the gap branch", c.Source, c.Disposition)
		}
	}
	if rows == 0 {
		t.Fatal("no candidate rows recorded — the loop above asserted nothing")
	}

	// And the decision layer must READ it as a state. This is the half spec §17 is about: the
	// code exists so that DecideStatus can answer INSUFFICIENT_DATA rather than NO_VALID_ENTRY.
	if r := entryplan.ReadAbsence(got.Trace.Rejection); r != entryplan.AbsenceEvidenceGap {
		t.Errorf("the decision layer reads %q as %q, want EVIDENCE_GAP", got.Trace.Rejection, r)
	}
	if !entryplan.ReservedReasons[entryplan.ReasonInvalidationEvidenceUnavailable] {
		t.Error("the code is not marked reserved, so the registry test would now demand a " +
			"SNAPSHOT that produces it — and no snapshot can, because a published zone " +
			"guarantees a comparable fallback row")
	}
}

// THE SELF-CONTRADICTION CODE IS UNREACHABLE, and this is the ledger that says so.
//
// INVALIDATION_SELECTION_INCONSISTENT is the strongest of the reservations: unlike the evidence
// gap above, NO argument reaches it, because both producing branches sit BETWEEN two calls
// ComputeInvalidation makes to functions in this same file. It fires only if
// ScreenInvalidationCandidates and SelectInvalidation disagree about the same slice.
//
// So this test claims the OPPOSITE of a producer test — it drives the two exported steps with
// the shapes that would have to disagree, and records that they do not — and it is here for the
// reason TestTheReservedChaseAlignmentCodeIsUnreachable is: the day the code DOES appear,
// somebody has to explain why.
func TestTheReservedSelectionInconsistencyCodeIsUnreachable(t *testing.T) {
	// 1. Not from the pipeline.
	var checked int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		checked++
		if hasReason(p, entryplan.ReasonInvalidationSelectionInconsistent) {
			t.Fatalf("%s produced INVALIDATION_SELECTION_INCONSISTENT — the screen and the "+
				"selector in stop.go disagreed about the same candidate list, which is a bug "+
				"in this package and not a market state. Reasons: %v", c.name, p.Reasons)
		}
	}
	if checked == 0 {
		t.Fatal("no plans checked")
	}

	// 2. Not from the exported step, on any zone a caller can invent. The three shapes below
	// are the ones that would have to produce the contradiction: a zone nothing is below, a
	// zone nothing is comparable with, and a zone everything is below.
	in := entryplan.Snapshot{
		Symbol: "2330", AsOf: "2026-09-09", PriceBasis: entryplan.PriceBasisRaw,
		CurrentPrice:  obs(95),
		MA60:          obs(90),
		BaseLow:       obs(88),
		BaseLowKind:   entryplan.BaseLowConsolidationBase,
		ATR:           entryplan.ATREvidence{Status: entryplan.Available, Value: f(4), Period: 14, PriceBasis: entryplan.PriceBasisRaw},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available, Semantic: entryplan.EntrySemanticPullback},
	}
	for _, zone := range []*entryplan.PriceZone{
		rawZone(92, 96),
		rawZone(1, 2),
		rawZone(9000, 9600),
		{Low: 92, High: 96, PriceBasis: entryplan.PriceBasisAdjusted},
		nil,
	} {
		got := entryplan.ComputeInvalidation(in, zone)
		if got.Trace.Rejection == entryplan.ReasonInvalidationSelectionInconsistent {
			t.Errorf("zone %v drove the exported step to contradict itself", zone)
		}
	}

	// 3. THE ANTI-VACUITY HALF, and without it this test passes by asserting an absence.
	// ScreenInvalidationCandidates and SelectInvalidation must actually AGREE — an eligible
	// row must exist AND be selected — or "the contradiction never happened" would be true
	// because neither function ever ran.
	cands := entryplan.InvalidationCandidatesFor(in, entryplan.EntrySemanticPullback, rawZone(92, 96))
	screened := entryplan.ScreenInvalidationCandidates(cands, rawZone(92, 96))
	var eligible int
	for _, c := range screened {
		if c.Disposition.Eligible() {
			eligible++
		}
	}
	if eligible == 0 {
		t.Fatal("no candidate survived the screen on the fixture, so the selector was never " +
			"asked anything and the contradiction it guards against could not have arisen")
	}
	if _, _, ok := entryplan.SelectInvalidation(screened); !ok {
		t.Fatalf("the screen produced %d eligible rows and the selector returned nothing — "+
			"that IS the contradiction, and it is now reachable", eligible)
	}
	if !entryplan.ReservedReasons[entryplan.ReasonInvalidationSelectionInconsistent] {
		t.Error("the code is not marked reserved, so the registry test would now demand an " +
			"input that produces it — and no input can")
	}
}

// THE PROOF ABOVE HAS A PREMISE, AND THIS IS THE MACHINE GUARD ON IT.
//
// TestTheReservedSelectionInconsistencyCodeIsUnreachable claims that
// INVALIDATION_SELECTION_INCONSISTENT cannot be emitted. That claim is not self-evident from the
// code: one of its three pillars is a fact about the stop-source REGISTRY, not about
// SelectInvalidation's own logic.
//
//	AllStopSources contains EXACTLY ONE non-structural (derived) source.
//
// SelectInvalidation has a branch that reads "two fallbacks is not a tie, it is a contradiction"
// and returns "none" for a candidate list carrying two eligible derived rows. Today that branch
// cannot fire, because there is one derived source (the volatility buffer), so a screened list
// can hold at most one derived eligible row. Add a SECOND derived source and the branch becomes
// reachable from a legal snapshot — at which point the reservation silently becomes false, and
// nothing else in this suite would notice.
//
// So this test is NOT the claim "there may only ever be one derived source". It is a TRIPWIRE:
// the day somebody lands a second one, this fails on purpose and tells them which proof they
// have just invalidated.
//
// It reads the PRODUCTION registry (entryplan.AllStopSources) and the PRODUCTION classifier
// (StopSource.Structural) rather than restating the member list, because a second hand-written
// copy of the source list is a second list to drift — and a drifted copy would make this guard
// pass while the premise it guards was already broken.
func TestTheUnreachableSelectionInconsistencyProofStillHasExactlyOneDerivedStopSource(t *testing.T) {
	var structural, derived []entryplan.StopSource
	for _, src := range entryplan.AllStopSources {
		if src.Structural() {
			structural = append(structural, src)
			continue
		}
		derived = append(derived, src)
	}

	// ANTI-VACUITY: an empty registry would make the count assertion below meaningless.
	if len(entryplan.AllStopSources) == 0 {
		t.Fatal("AllStopSources is empty — this guard inspected nothing, and the unreachability " +
			"proof for INVALIDATION_SELECTION_INCONSISTENT is no longer being checked at all")
	}

	if len(derived) != 1 {
		t.Errorf("AllStopSources now has %d non-structural (derived) stop sources, %v, and the "+
			"proof that INVALIDATION_SELECTION_INCONSISTENT is UNREACHABLE assumes EXACTLY ONE "+
			"(today: %s). SelectInvalidation refuses a candidate list holding two eligible "+
			"derived rows — 'two fallbacks is not a tie, it is a contradiction' — and that "+
			"branch is unreachable ONLY because one derived source cannot produce two rows. "+
			"With %d of them a legal snapshot can reach it, so the RESERVED marking in "+
			"reasons.go and TestTheReservedSelectionInconsistencyCodeIsUnreachable are now "+
			"claiming something false.\n"+
			"WHAT TO DO — pick one, do not delete this test:\n"+
			"  (a) RE-PROVE unreachability for the new registry: show that no snapshot can put "+
			"two derived rows in the eligible state at once, and record the new argument beside "+
			"the reason code; or\n"+
			"  (b) RECLASSIFY the reason: drop it from ReservedReasons, give it a producing "+
			"snapshot, and decide what EP-5 must publish when it fires; or\n"+
			"  (c) merge the new source into the existing fallback so the registry keeps one "+
			"derived member.\n"+
			"Structural sources are unaffected — they are selected by max() and a tie is "+
			"resolved, not refused. Structural set: %v",
			len(derived), derived, entryplan.StopSourceZoneATRBuffer, len(derived), structural)
	}

	// The derived one must still be the SOURCE THE PROOF NAMES. A registry that swapped the
	// volatility buffer for some other single derived source would keep the count at one while
	// invalidating every sentence the proof writes about bufferCandidate's arithmetic.
	if len(derived) == 1 && derived[0] != entryplan.StopSourceZoneATRBuffer {
		t.Errorf("the single non-structural stop source is now %q and the unreachability proof "+
			"is written about %q (zone.Low − k×ATR, one zone and one ATR therefore one row). "+
			"The count still holds; the ARGUMENT does not. Re-derive it for %q before relying "+
			"on the RESERVED marking.", derived[0], entryplan.StopSourceZoneATRBuffer, derived[0])
	}

	// And the other end of the same premise: the structural set must be non-empty, or "tier 1
	// wins" would be a rule about an empty set and the fallback would be the only answer any
	// plan can ever reach.
	if len(structural) == 0 {
		t.Error("no stop source is structural any more, so every invalidation is the derived " +
			"fallback — the tier rule in SelectInvalidation now has nothing to select from and " +
			"the FALLBACK_NOT_NEEDED disposition is dead code")
	}
}
