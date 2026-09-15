package entryplan_test

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── EP-4's input fixtures ─────────────────────────────────────────────────────────────
//
// zoneCases() in zonecases_test.go is EP-3b's table: every distinct outcome of the ZONE and
// CHASE rules, each case built to reach one of them and declaring the exact prices it must
// produce. It stays exactly as EP-3b left it — EP-4 changes no zone and no ceiling — and every
// one of its cases now ALSO produces a stop, targets and a risk/reward, which is why
// TestEP3bPublishesTheZoneAndTheCeilingAndNothingElse was replaced rather than edited.
//
// This file is the same shape for the RISK side. Not a cross product: the invalidation and
// target rules have two dozen distinct outcomes and each needs specific arithmetic to reach, so
// a product of level values would be enormous and would still miss the interesting cells. Each
// case below is built to reach ONE outcome, DECLARES the exact stop, targets and ratio it must
// produce, and declares which reasons must and must not appear.
//
// The declared numbers are the anti-vacuity mechanism, and for EP-4 they are also the MUTATION
// WITNESSES: three cases separate max() from min() on the invalidation, one separates min() from
// max() on Target1, one separates 3.5R from 2R, and four separate "the valuation may lower
// Target2" from "the valuation is a dependency of Target2".

// riskBase is a complete pullback snapshot with a CONSOLIDATION base low and a projected
// valuation, on top of zoneBase's numbers: 2330 at 100, MA20 94, MA60 90, base low 92, pivot
// 105, ATR(14) 4.0 raw, previous close 98.
//
// The two EP-4 fields are what zoneBase does not carry:
//
//	BaseLowKind  CONSOLIDATION_BASE   so the base low is a legal stop candidate at all
//	Valuation    200, SUITABLE        deliberately FAR above every 3.5R target below, so the
//	                                  ceiling is NOT_BINDING in the base case and every case
//	                                  where it bites says so by setting the number itself
//
// Derived, by hand, for the base case — every number below is checked by the table:
//
//	zone            centre = MA20 = 94 (highest support below 100), half = max(0.5×4, 2×0.1) = 2
//	                → 92 → 96
//	stop candidates MA60 90 (below 92 ✓), base low 92 (NOT below 92 ✗), buffer 92 − 2 = 90 ✓
//	invalidation    max(structural) = MA60 = 90;  the buffer is FALLBACK_NOT_NEEDED
//	risk            96 − 90 = 6            ← from zone.High, never the 94 midpoint
//	2R target       96 + 2.0 × 6 = 108
//	resistance      pivot 105, above 96 ✓  → Target1 = min(108, 105) = 105   (RESISTANCE)
//	reward          105 − 96 = 9           → R:R = 9 / 6 = 1.50
//	3.5R target     96 + 3.5 × 6 = 117     → ceiling 200 does not bind → Target2 = 117
func riskBase() entryplan.Snapshot {
	in := zoneBase()
	in.BaseLowKind = entryplan.BaseLowConsolidationBase
	in.Valuation = entryplan.ValuationEvidence{
		Status: entryplan.Available, BaseTargetPrice: f(200),
		Suitability: entryplan.SuitabilitySuitable,
	}
	return in
}

// valuation is an AVAILABLE base-scenario target with a stated suitability and no basis of its
// own — it inherits the snapshot's, which is what a caller reading one series produces.
func valuation(target float64, s entryplan.ValuationSuitability) entryplan.ValuationEvidence {
	return entryplan.ValuationEvidence{Status: entryplan.Available, BaseTargetPrice: f(target),
		Suitability: s}
}

// riskCase is one named input with its declared risk-side outcome.
type riskCase struct {
	name string
	mut  func(*entryplan.Snapshot)

	// wantReasons must ALL be present, notReasons must ALL be absent. A subset check rather
	// than an exact list, because the unconditional EP-4 limit travels with everything;
	// notReasons is where "the rule did not report the wrong failure" is asserted.
	wantReasons []entryplan.Reason
	notReasons  []entryplan.Reason

	// The four published numbers. Each must be declared as either an exact value or an
	// explicit absence — TestTheRiskCaseTableIsWellFormed refuses a case that says nothing,
	// because a case that asserts nothing about the output cannot fail.
	wantStop    *float64
	noStop      bool
	wantTarget1 *float64
	noTarget1   bool
	wantTarget2 *float64
	noTarget2   bool
	wantRatio   *float64
	noRatio     bool

	// The trace facts that separate two cases with the same numbers. Optional: "" means the
	// case is not about them.
	wantStopSource entryplan.StopSource
	wantBinding    entryplan.Target1Binding
	wantCeiling    entryplan.ValuationCeilingOutcome
}

func riskCases() []riskCase {
	return []riskCase{
		// ── the ordinary case, and the exact numbers derived at riskBase ──────────────
		{
			name:           "pullback/complete",
			mut:            func(s *entryplan.Snapshot) {},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceMA60,
			wantBinding:    entryplan.Target1FromResistance,
			wantCeiling:    entryplan.ValuationCeilingNotBinding,
			notReasons: []entryplan.Reason{
				entryplan.ReasonNoValidInvalidation,
				entryplan.ReasonTarget2BelowTarget1,
				entryplan.ReasonValuationCeilingBelowEntry,
			},
		},

		// ── the invalidation SELECTION RULE. THE max() WITNESS ────────────────────────
		{
			// TWO structural candidates strictly below the zone: base low 91 and MA60 90.
			// The answer must be 91 — the NEAREST one below the entry, the first sufficient
			// falsifier price meets — and min() would answer 90.
			//
			// The consequences of min() are both visible in this row: the stop would sit a
			// dollar lower, and the risk would be 6 instead of 5, so the SAME setup would be
			// published with a 20% larger risk and an R:R of 1.50 instead of 1.80.
			name: "pullback/two_structural_levels_the_nearest_wins",
			mut: func(s *entryplan.Snapshot) {
				s.MA60, s.BaseLow = obs(90), obs(91)
			},
			wantStop:       f(91),
			wantTarget1:    f(105),
			wantTarget2:    f(113.5),
			wantRatio:      f(1.8),
			wantStopSource: entryplan.StopSourceBaseLow,
			wantBinding:    entryplan.Target1FromResistance,
		},
		{
			// A TIE between the two structural candidates resolves to the earlier source in
			// AllStopSources, which is the MA60. Deterministic rather than dependent on
			// iteration order.
			name: "pullback/tie_between_the_ma60_and_the_base_low",
			mut: func(s *entryplan.Snapshot) {
				s.MA60, s.BaseLow = obs(91), obs(91)
			},
			wantStop:       f(91),
			wantTarget1:    f(105),
			wantTarget2:    f(113.5),
			wantRatio:      f(1.8),
			wantStopSource: entryplan.StopSourceMA60,
		},
		{
			// NO structural candidate: the fallback is used, and it is the SECOND tier rather
			// than a peer. Note that the number here is the same 90 the base case publishes
			// from the MA60 — which is exactly why the trace's selected source is asserted
			// too, and why the case below plants a structural level BELOW the buffer.
			name: "pullback/no_structural_level_falls_back_to_the_atr_buffer",
			mut: func(s *entryplan.Snapshot) {
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
			},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},
		{
			// THE TIER WITNESS. A structural level at 89 sits BELOW the buffer at 90, so a
			// single merged max() over all three candidates would publish 90 — the heuristic
			// — and leave the observed level unused. It must publish 89.
			//
			// The domain reason, on these numbers: at 89.5 price is still INSIDE the base
			// that defines the setup, so the thesis is intact; a stop at 90 would declare it
			// dead while the structure is untouched. That is a loss budget, not an
			// invalidation.
			name: "pullback/a_structural_level_below_the_buffer_still_wins",
			mut: func(s *entryplan.Snapshot) {
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, obs(89)
			},
			wantStop:       f(89),
			wantTarget1:    f(105),
			wantTarget2:    f(120.5),
			wantRatio:      f(1.2857142857142858),
			wantStopSource: entryplan.StopSourceBaseLow,
		},

		// ── tick alignment, DOWN, on a level that is NOT already on the grid ─────────
		{
			// THE DIRECTION WITNESS FOR THE STOP. An MA60 of 89.93 is a legal indicator
			// value and not a legal quote; the 0.10 grid applies at that price, so DOWN
			// answers 89.90 and UP would answer 90.00.
			//
			// Rounding UP would move the thesis-break level CLOSER TO THE ENTRY — declaring
			// the idea dead at a price the rule never called a failure — and it would shrink
			// the risk from 6.10 to 6.00, which RAISES the published R:R. Both directions of
			// that are the flattering one.
			name: "pullback/an_off_grid_invalidation_is_aligned_down",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = obs(89.93)
			},
			wantStop:       f(89.9),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.4754098360655752),
			wantStopSource: entryplan.StopSourceMA60,
			wantBinding:    entryplan.Target1FromResistance,
		},
		{
			// THE DIRECTION WITNESS FOR BOTH TARGETS. The same off-grid stop with no
			// resistance: 2R lands at 108.199… and 3.5R at 117.349…, neither on the 0.50
			// grid. DOWN answers 108.00 and 117.00; UP would answer 108.50 and 117.50 —
			// a take-profit above the level the arithmetic authorised, and a reward (and
			// therefore an R:R) overstated by the rounding.
			name: "pullback/off_grid_targets_are_aligned_down",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = obs(89.93)
				s.PivotHigh = entryplan.PriceObservation{}
			},
			wantStop:    f(89.9),
			wantTarget1: f(108),
			wantTarget2: f(117),
			wantRatio:   f(1.9672131147541),
			wantBinding: entryplan.Target1FromRMultiple,
		},

		// ── the BaseLowKind gate ──────────────────────────────────────────────────────
		{
			// THE (1) WITNESS. The base low is 91, strictly below the zone, on the right
			// basis — and it is TODAY'S SINGLE-BAR LOW, which internal/scanner puts in that
			// field whenever it finds NO_BASE. It must NOT be the invalidation: the stop
			// falls back to the buffer at 90 and the row says NOT_A_STRUCTURAL_LEVEL.
			//
			// Removing the kind gate publishes 91 here, which is the same field, the same
			// number and a different fact.
			name: "pullback/a_single_bar_low_is_not_a_structural_level",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = entryplan.PriceObservation{}
				s.BaseLow, s.BaseLowKind = obs(91), entryplan.BaseLowLatestBar
			},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},
		{
			// An UNSTATED kind is refused for the same reason PriceBasis refuses "": reading
			// "nobody said" as the good case asserts the very fact the field exists to
			// establish, on behalf of the caller who did not know there was a choice.
			name: "pullback/an_unstated_base_low_kind_is_refused_too",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = entryplan.PriceObservation{}
				s.BaseLow, s.BaseLowKind = obs(91), ""
			},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},
		{
			// UNKNOWN — the producer looked and could not say. Also refused, and recorded as
			// INSUFFICIENT_DATA on its evidence row rather than as UNAVAILABLE.
			name: "pullback/an_unknown_base_low_kind_is_refused_too",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = entryplan.PriceObservation{}
				s.BaseLow, s.BaseLowKind = obs(91), entryplan.BaseLowKindUnknown
			},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},
		{
			// A stop candidate on the ADJUSTED series is refused, never converted — and the
			// zone is unaffected, because the MA20 is still the centre.
			name: "pullback/the_ma60_on_the_adjusted_series_is_not_a_candidate",
			mut: func(s *entryplan.Snapshot) {
				s.MA60 = obsOn(90, entryplan.PriceBasisAdjusted)
			},
			wantStop:       f(90),
			wantTarget1:    f(105),
			wantTarget2:    f(117),
			wantRatio:      f(1.5),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},

		// ── Target1: the min(), and its two absences ──────────────────────────────────
		{
			// NO RESISTANCE above the entry: the pure 2R target is published and the row
			// SAYS SO. 108, not 105 — and this is the case that separates "resistance clamps
			// the target" from "resistance is required for a target to exist".
			name: "pullback/no_resistance_above_the_entry",
			mut: func(s *entryplan.Snapshot) {
				s.PivotHigh = entryplan.PriceObservation{}
			},
			wantStop:    f(90),
			wantTarget1: f(108),
			wantTarget2: f(117),
			wantRatio:   f(2),
			wantBinding: entryplan.Target1FromRMultiple,
		},
		{
			// A pivot BELOW the entry band is not something the trade has to get through. It
			// is screened out as NOT_ABOVE_ZONE_HIGH and the 2R target stands.
			name: "pullback/resistance_below_the_entry_is_not_a_target",
			mut: func(s *entryplan.Snapshot) {
				s.PivotHigh = obs(95)
			},
			wantStop:    f(90),
			wantTarget1: f(108),
			wantTarget2: f(117),
			wantRatio:   f(2),
			wantBinding: entryplan.Target1FromRMultiple,
		},
		{
			// A pivot on the ADJUSTED series: refused, never converted, 2R stands.
			name: "pullback/resistance_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				s.PivotHigh = obsOn(105, entryplan.PriceBasisAdjusted)
			},
			wantStop:    f(90),
			wantTarget1: f(108),
			wantTarget2: f(117),
			wantRatio:   f(2),
			wantBinding: entryplan.Target1FromRMultiple,
		},
		{
			// THE min() WITNESS, in the direction the legacy code gets wrong. The resistance
			// at 100 is NEARER than the 2R target at 108, so Target1 is 100 and the R:R is
			// 0.67 — a plan a reader should probably decline. `max` would publish 108 and an
			// R:R of 2.00, i.e. a reward the stock has to clear a level to reach.
			name: "pullback/resistance_nearer_than_2R_clamps_the_target_down",
			mut: func(s *entryplan.Snapshot) {
				s.PivotHigh = obs(100)
			},
			wantStop:    f(90),
			wantTarget1: f(100),
			wantTarget2: f(117),
			wantRatio:   f(0.6666666666666666),
			wantBinding: entryplan.Target1FromResistance,
		},
		{
			// A resistance LESS THAN ONE TICK above the entry aligns DOWN onto zone.High
			// itself, which is not a target. Refused rather than nudged up a tick — and NOT
			// silently replaced by the 2R target, which would be a second rule triggered by
			// the failure of the first.
			name: "no_target/resistance_within_one_tick_of_the_entry",
			mut: func(s *entryplan.Snapshot) {
				s.PivotHigh = obs(96.05)
			},
			wantStop:    f(90),
			noTarget1:   true,
			noTarget2:   true,
			noRatio:     true,
			wantReasons: []entryplan.Reason{entryplan.ReasonTarget1NotAboveEntry},
		},

		// ── Target2: the valuation ceiling, which is OPTIONAL SANITY and not a dependency
		{
			// THE CEILING BITES: 110 is below the 3.5R target of 117, so Target2 is 110 and
			// the row carries VALUATION_CEILING_APPLIED. Target1 and the R:R are untouched —
			// they are price-structure arithmetic and do not consult the valuation at all.
			name:        "pullback/the_valuation_ceiling_lowers_the_second_target",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(110, entryplan.SuitabilitySuitable) },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(110),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingApplied,
		},
		{
			// THE CEILING PULLS THE PAIR OUT OF ORDER: 100 is between the entry and Target1.
			// Target2 is WITHDRAWN — not swapped with Target1, not raised to meet it — and
			// the code says which.
			name:        "no_target2/the_ceiling_falls_below_the_first_target",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(100, entryplan.SuitabilitySuitable) },
			wantStop:    f(90),
			wantTarget1: f(105),
			noTarget2:   true,
			wantRatio:   f(1.5),
			wantReasons: []entryplan.Reason{entryplan.ReasonTarget2BelowTarget1},
			notReasons:  []entryplan.Reason{entryplan.ReasonValuationCeilingBelowEntry},
			wantCeiling: entryplan.ValuationCeilingApplied,
		},
		{
			// THE CEILING SITS AT OR BELOW THE ENTRY: the model says the stock is already at
			// or above its base-case fair value at the price this plan would pay. A finding,
			// with its own code, and it still costs only the second target.
			name:        "no_target2/the_ceiling_sits_below_the_entry",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(95, entryplan.SuitabilitySuitable) },
			wantStop:    f(90),
			wantTarget1: f(105),
			noTarget2:   true,
			wantRatio:   f(1.5),
			wantReasons: []entryplan.Reason{entryplan.ReasonValuationCeilingBelowEntry},
			notReasons:  []entryplan.Reason{entryplan.ReasonTarget2BelowTarget1},
			wantCeiling: entryplan.ValuationCeilingApplied,
		},
		{
			// AN UNSUITABLE MODEL NEVER CLAMPS. The number is 110 and would have bitten;
			// valuation's own doc is emphatic that UNSUITABLE means "P/E is the wrong
			// instrument for this company", not "overpriced", so it must not cap a target
			// derived from price structure. Target2 is the unclamped 117.
			name:        "pullback/an_unsuitable_valuation_never_clamps",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(110, entryplan.SuitabilityUnsuitable) },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(117),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingModelUnsuitable,
		},
		{
			// NO VALUATION AT ALL: Target2 still exists, unclamped, and the trace says why it
			// was not capped. This is the row that separates "optional sanity" from
			// "dependency" — under a dependency reading, every company with no filed
			// positive-earnings basis would lose its second target for an accounting reason.
			name:        "pullback/no_valuation_at_all_still_publishes_the_second_target",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = entryplan.ValuationEvidence{} },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(117),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingUnavailable,
		},
		{
			// A ceiling on the ADJUSTED series: refused, never converted, and Target2 stands
			// unclamped rather than being deleted.
			name: "pullback/a_valuation_on_the_adjusted_series_never_clamps",
			mut: func(s *entryplan.Snapshot) {
				s.Valuation = entryplan.ValuationEvidence{Status: entryplan.Available,
					BaseTargetPrice: f(110), Suitability: entryplan.SuitabilitySuitable,
					PriceBasis: entryplan.PriceBasisAdjusted}
			},
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(117),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingBasisMismatch,
		},
		{
			// CONDITIONAL and WEAK both PERMIT the ceiling: the gate is on the model's
			// APPLICABILITY, and both of those say it applies with reservations. Present so
			// the suitability vocabulary is exercised end to end rather than only at its two
			// extremes.
			name:        "pullback/a_conditional_valuation_still_caps",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(110, entryplan.SuitabilityConditional) },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(110),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingApplied,
		},
		{
			name:        "pullback/a_weak_valuation_still_caps",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(110, entryplan.SuitabilityWeak) },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(110),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingApplied,
		},
		{
			// INSUFFICIENT_DATA permits it too, and that is a DECISION rather than an
			// oversight: "we could not evaluate applicability" is not a finding that P/E is
			// the wrong instrument. What makes it safe is that a ceiling only ever LOWERS a
			// target and is always recorded as having been applied.
			name:        "pullback/an_unevaluated_valuation_still_caps",
			mut:         func(s *entryplan.Snapshot) { s.Valuation = valuation(110, entryplan.SuitabilityInsufficientData) },
			wantStop:    f(90),
			wantTarget1: f(105),
			wantTarget2: f(110),
			wantRatio:   f(1.5),
			wantCeiling: entryplan.ValuationCeilingApplied,
		},

		// ── the BREAKOUT thesis ───────────────────────────────────────────────────────
		{
			// The breakout zone is [pivot, pivot + halfWidth] = [105, 107], so:
			//
			//	stop candidates  MA60 NOT_A_CANDIDATE_FOR_SEMANTIC (see StopSource.Supports
			//	                 Semantic), base low 92 below 105 ✓, buffer 105 − 2 = 103 ✓
			//	invalidation     the base FLOOR at 92 — back at the bottom of the base, nothing
			//	                 was broken out of. The buffer at 103 is tier 2 and unused.
			//	risk             107 − 92 = 15
			//	resistance       the pivot is 105 <= 107, so NOT_ABOVE_ZONE_HIGH — a breakout
			//	                 plan can NEVER have a usable resistance here, which is the
			//	                 whole reason Target1 falls back to the R multiple instead of
			//	                 being withheld
			//	Target1          107 + 2.0 × 15 = 137,  R:R = 30 / 15 = 2.00
			//	Target2          107 + 3.5 × 15 = 159.5
			name:           "breakout/complete",
			mut:            breakout,
			wantStop:       f(92),
			wantTarget1:    f(137),
			wantTarget2:    f(159.5),
			wantRatio:      f(2),
			wantStopSource: entryplan.StopSourceBaseLow,
			wantBinding:    entryplan.Target1FromRMultiple,
		},
		{
			// A breakout with no base floor: the fallback at pivot − 0.5 × ATR = 103, which
			// is the "false breakout" reading — price back below the level it cleared by half
			// a day's range.
			name: "breakout/no_base_floor_falls_back_to_the_buffer",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.BaseLow = entryplan.PriceObservation{}
			},
			wantStop:       f(103),
			wantTarget1:    f(115),
			wantTarget2:    f(121),
			wantRatio:      f(2),
			wantStopSource: entryplan.StopSourceZoneATRBuffer,
		},

		// ── the refusals ──────────────────────────────────────────────────────────────
		{
			// NO ZONE, so nothing for a stop to be below. The stop step reports NO CODE OF ITS
			// OWN — ZoneTrace.Rejection already says why there is no entry — and every
			// candidate is NOT_SCREENED rather than rejected.
			name: "no_zone/no_level_to_centre_an_entry_on",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60, s.BaseLow = entryplan.PriceObservation{},
					entryplan.PriceObservation{}, entryplan.PriceObservation{}
			},
			noStop:      true,
			noTarget1:   true,
			noTarget2:   true,
			noRatio:     true,
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
			notReasons:  []entryplan.Reason{entryplan.ReasonNoValidInvalidation},
		},
		{
			// A ZONE AND NO INVALIDATION. A 4-dollar stock with an ATR of 4: the zone is
			// 2 → 6, and the fallback lands at 2 − 2 = 0, which is not a price. With no
			// structural level either, there is NO STOP — and therefore no target and no
			// R:R, because a trade whose failure point cannot be stated has no risk/reward.
			//
			// What must NOT happen: zone.Low × 0.99, zone.Low − one tick, or any percentage
			// of the entry. Each would be finite, positive, below the entry and on the grid.
			name: "no_stop/the_atr_fallback_is_not_a_price",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(5), obs(4)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.PreviousClose = obs(5)
			},
			noStop:      true,
			noTarget1:   true,
			noTarget2:   true,
			noRatio:     true,
			wantReasons: []entryplan.Reason{entryplan.ReasonNoValidInvalidation},
		},
		{
			// THE FIRST TARGET LEAVES pricerule's ALIGNABLE DOMAIN (1e6 元). Every input is a
			// legal quote and the 2R target is not: 999150 + 2 × 450 = 1,000,050. Refused,
			// with no clamp to the bound — a clamped target is a fabricated target.
			name: "no_target/the_first_target_leaves_the_alignable_domain",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(999500), obs(999000)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.PivotHigh = entryplan.PriceObservation{}
				s.ATR = atr(300)
				s.PreviousClose = obs(999000)
				// The base fixture's 200 元 fair value is not a statement about a 999,000 元
				// stock; carrying it here would make this case about the valuation ceiling
				// instead of about the alignable domain.
				s.Valuation = entryplan.ValuationEvidence{}
			},
			wantStop:    f(998700),
			noTarget1:   true,
			noTarget2:   true,
			noRatio:     true,
			wantReasons: []entryplan.Reason{entryplan.ReasonTarget1TickAlignmentUnavailable},
		},
		{
			// THE SECOND TARGET LEAVES THE DOMAIN AND THE FIRST DOES NOT. With an ATR of 200
			// the risk is 300, so 2R = 999,700 is alignable and 3.5R = 1,000,150 is not.
			// Target1 and the R:R survive: the cascade is no wider than the dependency.
			name: "no_target2/the_second_target_leaves_the_alignable_domain",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(999500), obs(999000)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.PivotHigh = entryplan.PriceObservation{}
				s.ATR = atr(200)
				s.PreviousClose = obs(999000)
				// Cleared for the reason the case above clears it — and here it MATTERS:
				// a 200 元 ceiling would sit below the entry and this case would reach
				// VALUATION_CEILING_BELOW_ENTRY instead of the alignment refusal.
				s.Valuation = entryplan.ValuationEvidence{}
			},
			wantStop:    f(998800),
			wantTarget1: f(999700),
			noTarget2:   true,
			wantRatio:   f(2),
			wantReasons: []entryplan.Reason{entryplan.ReasonTarget2TickAlignmentUnavailable},
		},
	}
}

func (c riskCase) snapshot() entryplan.Snapshot {
	in := riskBase()
	c.mut(&in)
	return in
}

// riskMatrix is the named EP-4 cases as plain snapshots, for the package-wide sweeps.
func riskMatrix() []namedSnapshot {
	cases := riskCases()
	out := make([]namedSnapshot, 0, len(cases))
	for _, c := range cases {
		out = append(out, namedSnapshot{name: "risk:" + c.name, in: c.snapshot()})
	}
	return out
}

// The case table is only as good as its coverage, and "I added a case" is not the same claim as
// "the case reaches the outcome". This is the ledger, in the shape
// TestTheZoneCaseTableIsWellFormed established.
func TestTheRiskCaseTableIsWellFormed(t *testing.T) {
	cases := riskCases()
	if len(cases) < 25 {
		t.Errorf("only %d named cases — EP-4 has more distinct outcomes than that, and a "+
			"shrinking table is how a rule loses its only witness", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.name == "" {
			t.Error("an unnamed case: every failure message would be anonymous")
		}
		if seen[c.name] {
			t.Errorf("duplicate case name %q — one of the two is invisible in every failure",
				c.name)
		}
		seen[c.name] = true
		if c.mut == nil {
			t.Errorf("%s: no mutation function", c.name)
		}
		for _, pair := range []struct {
			what string
			set  bool
			none bool
		}{
			{"stop", c.wantStop != nil, c.noStop},
			{"target 1", c.wantTarget1 != nil, c.noTarget1},
			{"target 2", c.wantTarget2 != nil, c.noTarget2},
			{"ratio", c.wantRatio != nil, c.noRatio},
		} {
			if pair.set && pair.none {
				t.Errorf("%s: declares both a %s and no %s", c.name, pair.what, pair.what)
			}
			if !pair.set && !pair.none {
				t.Errorf("%s: says nothing about the %s — a case that asserts nothing about "+
					"the output is a case that cannot fail", c.name, pair.what)
			}
		}
		if !c.snapshot().WellFormed() {
			t.Errorf("%s: the snapshot is malformed, so ComputePlan refuses before any EP-4 "+
				"rule runs and the case tests nothing", c.name)
		}
	}
}
