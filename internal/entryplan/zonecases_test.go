package entryplan_test

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── EP-3b's input fixtures ────────────────────────────────────────────────────────────
//
// matrix() in plan_test.go is the EVIDENCE sweep: nine current prices, six ATRs, six regimes
// and four semantics crossed against each other, with no level evidence at all. It stays
// exactly as EP-2 left it, and it is still the right shape for what it tests — every branch
// each BASE evidence field can take. What it cannot reach is any input that produces a PRICE,
// because it carries no MA20, no pivot and no previous close.
//
// So EP-3b adds a second, NAMED set. Not a cross product: the zone and chase rules have
// twenty-odd distinct outcomes and each one needs specific arithmetic to reach, so a product
// of level values would be enormous and would still miss the interesting cells. Each case
// below is built to reach ONE outcome, DECLARES which reasons it must produce, and — where the
// number is the point — declares the exact prices too.
//
// The declared expectations are the anti-vacuity mechanism: a sweep over unlabelled fixtures
// tells you the code did not crash, and this table tells you it produced the right answer for
// the stated reason. TestTheZoneCasesProduceWhatTheyWereBuiltFor checks every field, and
// TestEveryEP3ReasonIsReachedByANamedCase checks that between them the cases reach every EP-3b
// reason code the registry declares.

// zoneBase is a complete, ordinary pullback snapshot: 2330 at 100, MA20 at 94 with the MA60
// and the base low below it, ATR(14) of 4.0 on the raw close, previous close 98.
//
// Every case is a MUTATION of this one, so a failure names the single field that differs.
func zoneBase() entryplan.Snapshot {
	return entryplan.Snapshot{
		Symbol:        "2330",
		AsOf:          "2026-09-09",
		PriceBasis:    entryplan.PriceBasisRaw,
		AdjustmentAge: entryplan.AdjustmentAge{BarsAgo: i(63)},
		CurrentPrice:  obs(100),
		MA20:          obs(94),
		MA60:          obs(90),
		BaseLow:       obs(92),
		PivotHigh:     obs(105),
		PreviousClose: obs(98),
		AvgVolume20:   f(1_500_000),
		ATR: entryplan.ATREvidence{Status: entryplan.Available, Value: f(4), Period: 14,
			PriceBasis: entryplan.PriceBasisRaw},
		Regime: entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBull},
		EntrySemantic: entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback},
		// EP-6D: an ordinary snapshot's scanner Action does not contradict the entry. Every
		// case that is not ABOUT the thesis inherits this, so a zone or risk case that reaches
		// BUY_NOW keeps reaching it for the reason it was written.
		Thesis: entryplan.ThesisNotContradicted,
	}
}

// obs is an AVAILABLE price observation with no basis of its own — it inherits the snapshot's,
// which is what a caller reading every price off one series produces.
func obs(v float64) entryplan.PriceObservation {
	return entryplan.PriceObservation{Status: entryplan.Available, Value: f(v)}
}

// obsOn is an AVAILABLE price observation that declares its OWN basis. Used for the mismatch
// cases, which are the whole point of PriceBasis being per-item.
func obsOn(v float64, b entryplan.PriceBasis) entryplan.PriceObservation {
	return entryplan.PriceObservation{Status: entryplan.Available, Value: f(v), PriceBasis: b}
}

// atr is an AVAILABLE ATR(14) on the raw close.
func atr(v float64) entryplan.ATREvidence {
	return entryplan.ATREvidence{Status: entryplan.Available, Value: f(v), Period: 14,
		PriceBasis: entryplan.PriceBasisRaw}
}

func breakout(s *entryplan.Snapshot) {
	s.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Available,
		Semantic: entryplan.EntrySemanticBreakout}
}

// zoneCase is one named input with its declared outcome.
type zoneCase struct {
	name string
	mut  func(*entryplan.Snapshot)
	// wantReasons must ALL be present on the plan. A subset check rather than an exact list,
	// because the unconditional EP-3b limit and the caveat codes travel with everything and
	// restating them 30 times would hide the one code each case is about. The EXACT list is
	// asserted separately, for a handful of cases, in plan_test.go.
	wantReasons []entryplan.Reason
	// notReasons must NOT be present. This is where "the rule did not fall back" is
	// asserted: a case built to have no ATR must not report a level problem.
	notReasons []entryplan.Reason
	// wantZone, when set, is the exact published zone. nil means "no zone", which is checked
	// as strictly as a price is.
	wantZone *[2]float64
	// wantChase, when set, is the exact published chase ceiling. Use noChase for "none".
	wantChase *float64
	noZone    bool
	noChase   bool
}

func zone(low, high float64) *[2]float64 { return &[2]float64{low, high} }

func zoneCases() []zoneCase {
	return []zoneCase{
		// ── the ordinary case, and the exact numbers it must produce ──────────────────
		//
		// centre = MA20 = 94 (the HIGHEST support below 100), half width = max(0.5 × 4,
		// 2 × 0.1) = 2, so the raw zone is 92 → 96 and both bounds are already on the 0.10
		// grid. Chase: 96 + 1.0 × 4 = 100, well inside the +10% ceiling of 107.5.
		{
			name:       "pullback/complete",
			mut:        func(s *entryplan.Snapshot) {},
			wantZone:   zone(92, 96),
			wantChase:  f(100),
			notReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
		},
		{
			// The MA20 is ABOVE the market, so it is not a retracement level; the centre
			// must fall to the next support DOWN, which is the base low at 92 — not the
			// MA60 at 90, and not the MA20 at 102.
			name: "pullback/ma20_above_market",
			mut:  func(s *entryplan.Snapshot) { s.MA20 = obs(102) },
			// centre 92, half width max(2, 0.2) = 2 → 90 → 94.
			wantZone:  zone(90, 94),
			wantChase: f(98),
		},
		{
			// A flat 20/60 crossover: two eligible supports at the same price. The
			// tie-break is AllLevelSources order, so the MA20 wins and the zone is the
			// same either way — what is pinned is that the ANSWER is deterministic.
			name: "pullback/tie_between_ma20_and_ma60",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60 = obs(94), obs(94)
			},
			wantZone:  zone(92, 96),
			wantChase: f(100),
		},
		{
			// Every support above the market. THE DATA IS THERE and the market has not come
			// back — a different sentence from "there are no levels", and the more
			// informative one, which is why levelRejectionReason reports the furthest stage
			// reached.
			name: "pullback/every_support_above_market",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60, s.BaseLow = obs(102), obs(105), obs(110)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelNotBelowCurrentPrice},
			notReasons:  []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// EXACTLY AT the market. `>= CurrentPrice` and not `> CurrentPrice`: a support
			// at the last price is not a level price has retraced to, and a zone centred
			// there is an entry at the market wearing the word "pullback".
			name: "pullback/support_exactly_at_market",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60, s.BaseLow = obs(100), obs(100), obs(100)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelNotBelowCurrentPrice},
			noZone:      true,
			noChase:     true,
		},
		{
			// One support survives when the other two are gone. The rule does not need all
			// three, and it does not invent the missing ones.
			name: "pullback/only_the_base_low_survives",
			mut: func(s *entryplan.Snapshot) {
				s.MA20 = entryplan.PriceObservation{Status: entryplan.Unavailable}
				s.MA60 = entryplan.PriceObservation{}
			},
			// centre 92 → 90 → 94.
			wantZone:  zone(90, 94),
			wantChase: f(98),
		},
		{
			name: "pullback/no_level_evidence_at_all",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60, s.BaseLow = entryplan.PriceObservation{},
					entryplan.PriceObservation{}, entryplan.PriceObservation{}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// A 0 support and a NaN support. MISSING ≠ ZERO: neither is a price, and a
			// zone centred on 0 would be a level nothing trades at.
			name: "pullback/levels_present_but_not_prices",
			mut: func(s *entryplan.Snapshot) {
				s.MA20, s.MA60, s.BaseLow = obs(0), obs(-5), obs(nan())
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
			noZone:      true,
			noChase:     true,
		},

		// ── price basis ───────────────────────────────────────────────────────────────
		{
			// THE MEASURED REPO PROBLEM: the quote is raw and the moving averages came
			// through fetcher.PriceForCalc on the adjusted series. Refused, never
			// converted, and never silently used.
			name: "pullback/levels_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				s.MA20 = obsOn(94, entryplan.PriceBasisAdjusted)
				s.MA60 = obsOn(90, entryplan.PriceBasisAdjusted)
				s.BaseLow = obsOn(92, entryplan.PriceBasisAdjusted)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackLevelsBasisMismatch},
			notReasons:  []entryplan.Reason{entryplan.ReasonPullbackLevelsUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// One usable support and one on the wrong basis. The usable one wins and the
			// mismatch does not contaminate it — but the MA20 at 99, which is on the wrong
			// series, must NOT become the centre even though it is the highest number.
			name: "pullback/one_level_mismatched_one_usable",
			mut: func(s *entryplan.Snapshot) {
				s.MA20 = obsOn(99, entryplan.PriceBasisAdjusted)
				s.MA60 = obs(90)
				s.BaseLow = entryplan.PriceObservation{}
			},
			// centre 90 (the MA60), half max(2, 0.2) = 2 → 88 → 92.
			wantZone:  zone(88, 92),
			wantChase: f(96),
		},
		{
			// The whole snapshot on the adjusted series: internally consistent, so the ZONE
			// is published — and the ceiling is not, because ±10% is defined on the
			// exchange's raw reference price.
			name: "pullback/everything_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				s.PriceBasis = entryplan.PriceBasisAdjusted
				s.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(4),
					Period: 14, PriceBasis: entryplan.PriceBasisAdjusted}
			},
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseLimitRequiresRawBasis},
			noChase:     true,
		},

		// ── the width ─────────────────────────────────────────────────────────────────
		{
			// NO ATR, NO ZONE. Not a tick-only width, not 2.5% of the last price. The
			// levels were fine and are reported as fine.
			name:        "pullback/no_atr",
			mut:         func(s *entryplan.Snapshot) { s.ATR = entryplan.ATREvidence{Status: entryplan.Unavailable} },
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneATRUnavailable},
			notReasons: []entryplan.Reason{
				entryplan.ReasonPullbackLevelsUnavailable,
				entryplan.ReasonZoneTickAlignmentUnavailable,
			},
			noZone:  true,
			noChase: true,
		},
		{
			name:        "pullback/zero_atr",
			mut:         func(s *entryplan.Snapshot) { s.ATR = atr(0) },
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneATRUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			name: "pullback/atr_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				s.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(4),
					Period: 14, PriceBasis: entryplan.PriceBasisAdjusted}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneATRBasisMismatch},
			notReasons:  []entryplan.Reason{entryplan.ReasonZoneATRUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// ATR(20) where the rule is defined on ATR(14). A plausible number for a
			// different measurement.
			name: "pullback/atr_of_the_wrong_period",
			mut: func(s *entryplan.Snapshot) {
				s.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(4),
					Period: 20, PriceBasis: entryplan.PriceBasisRaw}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneATRPeriodMismatch},
			notReasons:  []entryplan.Reason{entryplan.ReasonZoneATRUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// The caller did not say which period. Refused for the same reason: a width
			// that cannot be reproduced from the archive.
			name: "pullback/atr_of_no_stated_period",
			mut: func(s *entryplan.Snapshot) {
				s.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(4),
					PriceBasis: entryplan.PriceBasisRaw}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneATRPeriodMismatch},
			noZone:      true,
			noChase:     true,
		},
		{
			// THE TICK FLOOR CASE. A 8-元 stock with an ATR of 0.01: half an ATR is 0.005,
			// which is smaller than the 0.01 grid it trades on, so the floor of two ticks
			// (0.02) takes over. The floor WIDENS a width that exists; it never replaces a
			// missing one.
			name: "pullback/atr_smaller_than_two_ticks",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(8), obs(7.5)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.ATR = atr(0.01)
				s.PreviousClose = obs(7.9)
			},
			wantZone:  zone(7.48, 7.52),
			wantChase: f(7.53),
		},
		{
			// The ATR is more than twice the level, so the raw low is negative. Refused,
			// not floored at a tick: `low = tickSize` would publish a bound the rule never
			// computed and quietly replace the zone's width.
			name: "pullback/atr_wider_than_the_level_itself",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(20), obs(10)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.ATR = atr(30)
				s.PreviousClose = obs(19)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneLowNotAPrice},
			noZone:      true,
			noChase:     true,
		},
		{
			// A "level" of two million. pricerule's domain stops at 1e6 元 because above it
			// the number is not a price — it is a turnover figure or a market cap — and
			// there is no clamping, because a clamped price is a fabricated price.
			name: "pullback/level_outside_the_alignable_domain",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice, s.MA20 = obs(3_000_000), obs(2_000_000)
				s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
				s.ATR = atr(1000)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneTickAlignmentUnavailable},
			noZone:      true,
			noChase:     true,
		},

		// ── the overlap flag, and the clamp that must NOT happen ──────────────────────
		{
			// The MA20 is 1 元 below the market and half an ATR is 2, so the zone's high
			// lands ABOVE the last price. THE ZONE IS PUBLISHED UNCHANGED, centred on 99,
			// and the overlap is reported.
			//
			// If this ever comes back as 97 → 100, the clamp has been reintroduced: the
			// band would no longer be the support ± half an ATR, and the sentence that
			// justifies the number would be false while still being printed.
			name:        "pullback/zone_high_above_the_market",
			mut:         func(s *entryplan.Snapshot) { s.MA20 = obs(99) },
			wantZone:    zone(97, 101),
			wantChase:   f(105),
			wantReasons: []entryplan.Reason{entryplan.ReasonPullbackZoneOverlapsCurrentPrice},
		},

		// ── breakout ──────────────────────────────────────────────────────────────────
		{
			// centre = pivot = 105, half width max(2, 0.5 × ... ) — the tick at 105 is 0.5
			// so the floor is 1.0 and the ATR half is 2.0. The zone sits ON the pivot:
			// 105 → 107.
			name:      "breakout/complete",
			mut:       breakout,
			wantZone:  zone(105, 107),
			wantChase: f(107.5),
		},
		{
			// The supports are all present and NONE of them may be used: a breakout is
			// centred on the pivot or on nothing.
			name: "breakout/no_pivot",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.PivotHigh = entryplan.PriceObservation{}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonBreakoutLevelUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			name: "breakout/pivot_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.PivotHigh = obsOn(105, entryplan.PriceBasisAdjusted)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonBreakoutLevelBasisMismatch},
			notReasons:  []entryplan.Reason{entryplan.ReasonBreakoutLevelUnavailable},
			noZone:      true,
			noChase:     true,
		},
		{
			// A pivot that is not a whole number of cents — not a price any exchange
			// quoted. pricerule's RoundUp lands a hair BELOW it (the snap tolerance), which
			// would put the bottom of a breakout zone below the level that defines the
			// breakout. Refused rather than nudged up a tick.
			name: "breakout/pivot_is_not_a_legal_quote",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.PivotHigh = obs(100.00005)
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonBreakoutLevelNotAQuote},
			noZone:      true,
			noChase:     true,
		},
		{
			// A pivot BELOW the market. Not screened out: "the breakout already happened"
			// is a real state, and whether it means buy the retest or TOO_EXTENDED is a
			// STATUS decision this work item does not make. The zone is published.
			name: "breakout/pivot_below_the_market",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.PivotHigh = obs(95)
			},
			wantZone:  zone(95, 97),
			wantChase: f(101),
		},
		{
			// BULL_PULLBACK disallows a breakout entry: the premise of the regime is that
			// price is coming back to you. A DECISION, and the zone is refused for it.
			name: "breakout/disallowed_by_the_regime",
			mut: func(s *entryplan.Snapshot) {
				breakout(s)
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeBullPullback}
			},
			wantReasons: []entryplan.Reason{entryplan.ReasonZoneSemanticNotPermitted},
			notReasons:  []entryplan.Reason{entryplan.ReasonZonePolicyUnresolved},
			noZone:      true,
			noChase:     true,
		},

		// ── the chase ceiling ─────────────────────────────────────────────────────────
		{
			// No previous close: NO CEILING, and THE ZONE SURVIVES. The two availabilities
			// are independent, and the current price is not a substitute.
			name:        "chase/no_previous_close",
			mut:         func(s *entryplan.Snapshot) { s.PreviousClose = entryplan.PriceObservation{} },
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonPreviousCloseUnavailable},
			noChase:     true,
		},
		{
			name: "chase/previous_close_on_the_adjusted_series",
			mut: func(s *entryplan.Snapshot) {
				s.PreviousClose = obsOn(98, entryplan.PriceBasisAdjusted)
			},
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonPreviousCloseBasisMismatch},
			notReasons:  []entryplan.Reason{entryplan.ReasonPreviousCloseUnavailable},
			noChase:     true,
		},
		{
			// THE CLAMP. Previous close 100 → the day's ceiling is 110.0. The raw chase is
			// 96 + 1.0 × 4 = 100, which is inside it, so this case is the CONTROL; the
			// clamped one is below.
			name:      "chase/inside_the_daily_band",
			mut:       func(s *entryplan.Snapshot) { s.PreviousClose = obs(100) },
			wantZone:  zone(92, 96),
			wantChase: f(100),
		},
		{
			// The raw chase (96 + 8.0 × 4 = 128) is far above the +10% ceiling of 110, so
			// the ceiling wins. 110 is exactly on the 0.50 grid, so the answer is 110.
			name: "chase/clamped_to_the_daily_limit",
			mut: func(s *entryplan.Snapshot) {
				s.PreviousClose = obs(100)
				s.ATR = atr(32) // 0.5 × 32 = 16 half width, 1.0 × 32 chase headroom
				s.MA20, s.MA60, s.BaseLow = obs(94), entryplan.PriceObservation{},
					entryplan.PriceObservation{}
			},
			// centre 94, half 16 → 78 → 110. Raw chase 110 + 32 = 142, clamped to 110.
			wantZone:  zone(78, 110),
			wantChase: f(110),
		},
		{
			// The legal ceiling sits BELOW the top of the ideal zone: no chase headroom at
			// all. The ceiling is withdrawn (publishing it would break
			// MAX_CHASE_NOT_BELOW_ZONE_HIGH) and the zone is kept, because the level and
			// the volatility have not changed — only today's band.
			name: "chase/ceiling_below_the_zone",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice = obs(120)
				s.MA20, s.MA60, s.BaseLow = obs(105), entryplan.PriceObservation{},
					entryplan.PriceObservation{}
				s.ATR = atr(20)
				s.PreviousClose = obs(100)
			},
			wantZone:    zone(95, 115),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseCeilingBelowZone},
			noChase:     true,
		},
		{
			// An ETF code. pricerule cannot tell a domestic-constituent ETF (±10%) from a
			// foreign-constituent one (no limit at all) from the code alone, so it says
			// UNKNOWN and there is no ceiling this package knows of. The chase is refused
			// rather than published unclamped.
			name:        "chase/etf_code_with_no_establishable_limit",
			mut:         func(s *entryplan.Snapshot) { s.Symbol = "0050" },
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseLimitRuleUnavailable},
			noChase:     true,
		},
		{
			// A six-digit warrant code: its limits are tied to the underlying, not to ±10%
			// of its own previous close.
			name:        "chase/warrant_code",
			mut:         func(s *entryplan.Snapshot) { s.Symbol = "072588" },
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseLimitRuleUnavailable},
			noChase:     true,
		},
		{
			// A previous close whose +10% leaves pricerule's alignable domain. No default
			// ceiling.
			name: "chase/previous_close_outside_the_alignable_domain",
			mut: func(s *entryplan.Snapshot) {
				s.CurrentPrice = obs(900_000)
				s.MA20, s.MA60, s.BaseLow = obs(899_000), entryplan.PriceObservation{},
					entryplan.PriceObservation{}
				s.ATR = atr(1000)
				s.PreviousClose = obs(999_999)
			},
			wantZone:    zone(898_500, 899_500),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseLimitPriceUnavailable},
			noChase:     true,
		},
		{
			// SIDEWAYS permits a pullback entry NEAR SUPPORT and permits no chase at all.
			// Two different fields of one policy, and the zone must survive the second.
			name: "chase/disallowed_while_the_zone_is_allowed",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeSideways}
			},
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseNotAllowed},
			notReasons: []entryplan.Reason{
				entryplan.ReasonChasePolicyUnresolved,
				entryplan.ReasonZoneSemanticNotPermitted,
			},
			noChase: true,
		},
		{
			// BEAR: no entry shape and no chase. Both refusals are DECISIONS.
			name: "chase/bear_refuses_both",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeBear}
			},
			wantReasons: []entryplan.Reason{
				entryplan.ReasonZoneSemanticNotPermitted,
				entryplan.ReasonChaseNotAllowed,
			},
			notReasons: []entryplan.Reason{
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonChasePolicyUnresolved,
			},
			noZone:  true,
			noChase: true,
		},
		{
			// UNKNOWN: no decision was made about either. NOT the same row as BEAR, and the
			// two codes are what keeps them apart.
			name: "chase/unknown_regime_resolves_nothing",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeUnknown}
			},
			wantReasons: []entryplan.Reason{
				entryplan.ReasonZonePolicyUnresolved,
				entryplan.ReasonChasePolicyUnresolved,
			},
			notReasons: []entryplan.Reason{
				entryplan.ReasonZoneSemanticNotPermitted,
				entryplan.ReasonChaseNotAllowed,
			},
			noZone:  true,
			noChase: true,
		},
		{
			// DISTRIBUTION: the pullback bar is RAISED, not removed, and chasing is not
			// permitted. The requirement travels on the trace unevaluated.
			name: "chase/distribution_raises_the_bar_and_forbids_chasing",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeDistribution}
			},
			wantZone:    zone(92, 96),
			wantReasons: []entryplan.Reason{entryplan.ReasonChaseNotAllowed},
			noChase:     true,
		},
		{
			// BULL_PULLBACK chases LESS than BULL: 0.4 × ATR instead of 1.0. 96 + 0.4 × 4
			// = 97.6, and the 0.10 grid at that price holds it exactly.
			name: "chase/bull_pullback_tolerates_less",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available,
					Regime: model.RegimeBullPullback}
			},
			wantZone:  zone(92, 96),
			wantChase: f(97.6),
		},

		// ── the adjustment age is a caveat, never a gate ──────────────────────────────
		{
			// The newest bar IS the adjustment — the most alarming answer there is. The
			// prices are computed anyway and the fact is reported.
			name:        "adjustment/the_newest_bar_is_the_event",
			mut:         func(s *entryplan.Snapshot) { s.AdjustmentAge = entryplan.AdjustmentAge{BarsAgo: i(0)} },
			wantZone:    zone(92, 96),
			wantChase:   f(100),
			wantReasons: []entryplan.Reason{entryplan.ReasonRecentPriceAdjustment},
		},
		{
			// 62 bars: the residue is 1.03% of the event. Reported.
			name:        "adjustment/62_bars_ago",
			mut:         func(s *entryplan.Snapshot) { s.AdjustmentAge = entryplan.AdjustmentAge{BarsAgo: i(62)} },
			wantZone:    zone(92, 96),
			wantChase:   f(100),
			wantReasons: []entryplan.Reason{entryplan.ReasonRecentPriceAdjustment},
		},
		{
			// 63 bars: under 1%. Not reported — and NOT because anything became clean, see
			// adjustmentAgeCaveatBars.
			name:       "adjustment/63_bars_ago",
			mut:        func(s *entryplan.Snapshot) { s.AdjustmentAge = entryplan.AdjustmentAge{BarsAgo: i(63)} },
			wantZone:   zone(92, 96),
			wantChase:  f(100),
			notReasons: []entryplan.Reason{entryplan.ReasonRecentPriceAdjustment},
		},
		{
			// LastAdjustmentAge had NO ANSWER. Not "recent" and not "clean": three
			// different situations produce it and only one of them means no adjustment.
			name:       "adjustment/no_answer",
			mut:        func(s *entryplan.Snapshot) { s.AdjustmentAge = entryplan.AdjustmentAge{} },
			wantZone:   zone(92, 96),
			wantChase:  f(100),
			notReasons: []entryplan.Reason{entryplan.ReasonRecentPriceAdjustment},
		},

		// ── liquidity is recorded and gates nothing ───────────────────────────────────
		{
			name:      "liquidity/absent",
			mut:       func(s *entryplan.Snapshot) { s.AvgVolume20 = nil },
			wantZone:  zone(92, 96),
			wantChase: f(100),
		},
		{
			// A stock that barely trades. It still gets its zone and its ceiling: there is
			// no backtested "minimum volume at which a chase fills" contract in this repo,
			// and a threshold invented here would be a fabricated tradeability claim.
			name:      "liquidity/almost_none",
			mut:       func(s *entryplan.Snapshot) { s.AvgVolume20 = f(120) },
			wantZone:  zone(92, 96),
			wantChase: f(100),
		},

		// ── every tick tier, so "it goes through pricerule" is a fact ─────────────────
		//
		// One case per band of pricerule's table, each with a raw bound that is NOT on the
		// grid, so the published numbers can only be right if the alignment ran. The
		// expected values are computed by hand from the table, never from the code.
		tickCase("tick/under_10", 9.5, 8.93, 0.37, 8.74, 9.12, 9.49),
		tickCase("tick/10_to_50", 30, 27.77, 1.11, 27.20, 28.35, 29.45),
		tickCase("tick/50_to_100", 75, 71.43, 3.33, 69.70, 73.10, 76.40),
		tickCase("tick/100_to_500", 300, 287.77, 11.11, 282.00, 293.50, 304.50),
		tickCase("tick/500_to_1000", 800, 761.11, 33.33, 744.00, 778.00, 811.00),
		tickCase("tick/at_1000_exactly", 1100, 1000, 40, 980, 1020, 1060),
		tickCase("tick/over_1000", 1300, 1245.3, 60, 1215, 1280, 1340),
	}
}

// tickCase builds one tick-tier case: a pullback whose only support is `level`, an ATR of
// `atrValue`, and the three published numbers stated by hand.
//
// The previous close is 10% below the current price so the +10% ceiling never binds — these
// cases are about the GRID, and a clamp firing inside one would test something else.
func tickCase(name string, current, level, atrValue, wantLow, wantHigh, wantChase float64) zoneCase {
	return zoneCase{
		name: name,
		mut: func(s *entryplan.Snapshot) {
			s.CurrentPrice = obs(current)
			s.MA20 = obs(level)
			s.MA60, s.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
			s.ATR = atr(atrValue)
			s.PreviousClose = obs(current)
		},
		wantZone:  zone(wantLow, wantHigh),
		wantChase: f(wantChase),
	}
}

func nan() float64 {
	var zero float64
	return zero / zero
}

// snapshotFor applies one case's mutation to the base snapshot.
func (c zoneCase) snapshot() entryplan.Snapshot {
	in := zoneBase()
	c.mut(&in)
	return in
}

// zoneMatrix is the named cases as plain snapshots, for the sweeps that iterate every input
// this package can be handed (the census, the invariant gate, the finiteness walk).
func zoneMatrix() []namedSnapshot {
	cases := zoneCases()
	out := make([]namedSnapshot, 0, len(cases))
	for _, c := range cases {
		out = append(out, namedSnapshot{name: "zone:" + c.name, in: c.snapshot()})
	}
	return out
}

// allSnapshots is matrix() ∪ zoneMatrix() ∪ riskMatrix(): the EVIDENCE sweep, the ZONE cases
// and the RISK cases.
//
// Every package-wide property — the exact evidence census, the invariant gate, "no non-finite
// float escapes", the reason registry — is asserted over this union rather than over matrix()
// alone. Under EP-2 matrix() was the whole input space that mattered because no price could be
// produced; from EP-3b it covers only the half with no levels in it, and a sweep that missed
// the priced half would report the strongest properties in this package as holding over
// exactly the inputs that cannot exercise them.
//
// EP-4 adds riskMatrix() for the same reason one step further out: zoneMatrix() carries no
// BaseLowKind and no projected valuation, so on its own it cannot reach a single one of the
// valuation-ceiling outcomes, cannot distinguish a consolidation base low from a single-bar
// one, and never publishes a clamped second target. A registry sweep over the first two sets
// alone would have reported EP-4's codes as dead.
func allSnapshots() []namedSnapshot {
	out := matrix()
	out = append(out, zoneMatrix()...)
	return append(out, riskMatrix()...)
}

// The case table is only as good as its coverage, and "I added a case" is not the same claim
// as "the case reaches the outcome". This test is the ledger.
func TestTheZoneCaseTableIsWellFormed(t *testing.T) {
	cases := zoneCases()
	if len(cases) < 40 {
		t.Errorf("only %d named cases — EP-3b has more distinct outcomes than that, and a "+
			"shrinking table is how a rule loses its only witness", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.name == "" {
			t.Error("an unnamed case: every failure message would be anonymous")
		}
		if seen[c.name] {
			t.Errorf("duplicate case name %q — one of the two is invisible in every failure", c.name)
		}
		seen[c.name] = true
		if c.mut == nil {
			t.Errorf("%s: no mutation function", c.name)
		}
		if c.wantZone != nil && c.noZone {
			t.Errorf("%s: declares both a zone and no zone", c.name)
		}
		if c.wantChase != nil && c.noChase {
			t.Errorf("%s: declares both a chase price and no chase price", c.name)
		}
		if c.wantZone == nil && !c.noZone {
			t.Errorf("%s: says nothing about the zone — a case that asserts nothing about "+
				"the output is a case that cannot fail", c.name)
		}
		if c.wantChase == nil && !c.noChase {
			t.Errorf("%s: says nothing about the chase ceiling", c.name)
		}
		if !c.snapshot().WellFormed() {
			t.Errorf("%s: the snapshot is malformed, so ComputePlan refuses before any "+
				"EP-3b rule runs and the case tests nothing", c.name)
		}
	}
}
