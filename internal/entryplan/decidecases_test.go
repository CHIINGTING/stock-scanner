package entryplan_test

import (
	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/pricerule"
)

// ── EP-5: THE DECISION MATRIX ─────────────────────────────────────────────────────────
//
// The table this file exists to be. DecideStatus's arms are ORDERED and first-match-wins, which
// means the answer for an input satisfying two of them is decided by the ORDER — and an order
// is a specification only if something reads it back. A handful of example tests cannot: they
// pin the arms anyone remembered, and the arms nobody remembered are exactly the ones a later
// edit reorders.
//
// So the cases below are a MATRIX over the three axes the decision actually has:
//
//	SHAPE     PULLBACK, BREAKOUT                                    (EntrySemantic)
//	REGIME    BULL, BULL_PULLBACK, SIDEWAYS, DISTRIBUTION, BEAR, UNKNOWN
//	PRICE     below the band, one tick below its floor, AT the floor, inside, AT the ceiling
//	          of the band, above it, AT the chase ceiling, one tick above it, far above it
//
// Not the full Cartesian product — twelve of the twenty-four (regime × shape) pairs are refused
// or undecided by the POLICY before any price is read, and repeating nine price relations under
// each would assert the same arm nine times. What IS required, and what this table guarantees
// through TestTheDecisionMatrixCoversTheCanonicalBoundaries, is that every CANONICAL BOUNDARY
// appears at least once:
//
//	Current == Zone.Low     the cheapest price the band names, and it is BUYABLE
//	Current == Zone.High    the dearest price the band names, and it is BUYABLE
//	Current == MaxChase     the ceiling doing its job. STILL LEGAL — not TOO_EXTENDED
//	Current == MaxChase + 1 tick   the first price that IS TOO_EXTENDED
//	Current == Zone.Low − 1 tick   the last price that is not in the band
//
// Every one of those five is a place where `<` and `<=` differ, i.e. where the plausible wrong
// implementation is one character away from the right one.
//
// # The rows carry the ARM, not only the status
//
// Two arms produce BUY_NOW (inside the band, and a permitted chase above it) and four produce
// NO_VALID_ENTRY. A table that asserted only the status would pass with the wrong arm firing,
// and the arm is the half an archived row is re-derivable from — see DecisionRule.

// The fixture geometry. ONE band and ONE ceiling for the whole table, so that a row's name
// ("at the top of the band") and its number are the same fact, and so that a price relation can
// be read off the case name without opening the builder.
//
//	zone         840 ─────── 860
//	ceiling                       872
//
// The numbers are real TWSE quotes at a real tick tier (0.5 元 above 500 元), which matters for
// the two "one tick" rows: an off-grid price is not a price any exchange quoted, so a boundary
// case built from one would be testing a number that cannot occur.
const (
	decZoneLow  = 840.0
	decZoneHigh = 860.0
	decCeiling  = 872.0
	decStop     = 830.0
)

// decTick is the exchange tick at the top of the band, taken from pricerule rather than typed
// in — the tier boundaries are pricerule's to change, and a transcribed 0.5 would go on passing
// after they did.
func decTick() float64 {
	t, ok := pricerule.TickSize(decZoneHigh)
	if !ok {
		panic("no tick size at the fixture's own price")
	}
	return t
}

func decZone() *entryplan.PriceZone {
	w := 0.62
	return &entryplan.PriceZone{
		Low: decZoneLow, High: decZoneHigh, WidthATR: &w,
		PriceBasis: entryplan.PriceBasisRaw, Basis: []string{"MA20"},
	}
}

func decInvalidation() *entryplan.Invalidation {
	return &entryplan.Invalidation{Price: decStop, PriceBasis: entryplan.PriceBasisRaw,
		Basis: []string{"MA60"}}
}

// dec builds the COMPLETE-EVIDENCE input: a real policy from the real table, a band, a ceiling
// and a risk boundary, with only the price varying. Mutators strip pieces away.
//
// The policy comes from PolicyFor — the exported EP-2 rule — and never from a literal, so a row
// that says "SIDEWAYS refuses to conclude" is a statement about the SHIPPED table and not about
// a policy this test invented to agree with it.
func dec(regime model.Regime, sem entryplan.EntrySemantic, cur float64,
	muts ...func(*entryplan.DecisionInput)) entryplan.DecisionInput {
	in := entryplan.DecisionInput{
		Semantic:      sem,
		Policy:        entryplan.PolicyFor(regime, sem),
		CurrentPrice:  f(cur),
		Zone:          decZone(),
		MaxChasePrice: f(decCeiling),
		Invalidation:  decInvalidation(),
		// EP-6D: the complete-evidence input includes a CLASSIFIED, non-contradicting thesis
		// standing. Without it neither BUY_NOW arm can fire — "" is unclassified, and
		// unclassified is never permission. The thesis rows below strip or flip it.
		Thesis: entryplan.ThesisNotContradicted,
	}
	for _, m := range muts {
		m(&in)
	}
	return in
}

// noZone / noStop / noChase remove one published number AND supply the code that explains it,
// which is the whole point of the pair: `Zone == nil` alone cannot distinguish "the ATR was
// missing" from "every support sits above the market".
func noZone(why entryplan.Reason) func(*entryplan.DecisionInput) {
	return func(in *entryplan.DecisionInput) { in.Zone, in.ZoneAbsence = nil, why }
}

func noStop(why entryplan.Reason) func(*entryplan.DecisionInput) {
	return func(in *entryplan.DecisionInput) { in.Invalidation, in.StopAbsence = nil, why }
}

func noChase(why entryplan.Reason) func(*entryplan.DecisionInput) {
	return func(in *entryplan.DecisionInput) { in.MaxChasePrice, in.ChaseAbsence = nil, why }
}

// thesis replaces the thesis standing (EP-6D). dec() supplies NOT_CONTRADICTED.
func thesis(t entryplan.ThesisStanding) func(*entryplan.DecisionInput) {
	return func(in *entryplan.DecisionInput) { in.Thesis = t }
}

// withPolicy replaces the whole PolicyResult.
//
// Used only by the rows that reach arms the SHIPPED six-regime table cannot produce — a
// permission to execute at the market that is refused or undecided while the entry SHAPE is
// permitted. Those arms are this package's half of a contract with EP-2, and the contract is
// PolicyResult, not "the six rows that exist in September 2026": DecideStatus is exported, it
// is documented TOTAL over every DecisionInput, and a policy row added later that pairs an
// allowed shape with a refused execution must not fall into a branch nobody has run.
//
// Each such row says WHICH FUTURE POLICY it stands for, so the fixture is a hypothesis about
// EP-2's table and not a shape invented to make a branch go green.
func withPolicy(p entryplan.PolicyResult) func(*entryplan.DecisionInput) {
	return func(in *entryplan.DecisionInput) { in.Policy = p }
}

// policyWith composes a PolicyResult out of the permission pieces EP-2 already ships.
//
// allowance/reqs are what the policy says about the SHAPE; buyNow and immediate are the two
// execution permissions. The regime is carried so a failure message names the row this is a
// hypothesis about.
func policyWith(regime model.Regime, sem entryplan.EntrySemantic, allowance entryplan.Allowance,
	reqs []entryplan.Requirement, buyNow, immediate entryplan.Permission) entryplan.PolicyResult {
	p := entryplan.EntryPolicy{
		Name:     entryplan.PolicyNormal,
		Regime:   regime,
		BuyNow:   buyNow,
		Pullback: entryplan.Permission{Allowance: allowance, Requirements: reqs},
		Breakout: entryplan.Permission{Allowance: allowance, Requirements: reqs},
		Chase: entryplan.ChasePolicy{
			Allowance: entryplan.AllowanceAllowed,
			Immediate: immediate,
		},
	}
	return entryplan.PolicyResult{
		Regime: regime, Semantic: sem, Policy: &p,
		Allowance: allowance, Requirements: reqs,
	}
}

func allowed() entryplan.Permission {
	return entryplan.Permission{Allowance: entryplan.AllowanceAllowed}
}

func refused() entryplan.Permission {
	return entryplan.Permission{Allowance: entryplan.AllowanceDisallowed}
}

func undecided() entryplan.Permission {
	return entryplan.Permission{Allowance: entryplan.AllowanceUnresolved}
}

// decisionCase is one row: an input, the arm that must fire, the status it must publish, and
// the EXACT reason list.
//
// Reasons are exact and ORDERED. "Contains" would pass with a code added that nobody decided
// on, and the order is what a stored row is diffed by.
type decisionCase struct {
	name        string
	in          entryplan.DecisionInput
	wantRule    entryplan.DecisionRule
	wantStatus  entryplan.EntryStatus
	wantReasons []entryplan.Reason
}

func rs(rr ...entryplan.Reason) []entryplan.Reason { return rr }

//nolint:funlen // the table IS the specification; splitting it hides the ordering it encodes.
func decisionCases() []decisionCase {
	tick := decTick()

	const (
		belowBand  = 800.0
		insideBand = 850.0
		aboveBand  = 866.0
		farAbove   = 900.0
	)
	var (
		justBelowBand = decZoneLow - tick
		justOverCeil  = decCeiling + tick
	)

	return []decisionCase{
		// ── S0. NO SHAPE ──────────────────────────────────────────────────────────
		//
		// FIRST, and ahead of the price and the policy alike: every arm below is a sentence
		// about a PULLBACK or a BREAKOUT, and until the scanner says which there is nothing
		// to be right or wrong about. UNKNOWN is a STATE (regime_engine.go's R0, one layer
		// down), so it must never fall through to a verdict.
		{
			name:        "an unknown entry shape is a state, whatever the regime says",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticUnknown, insideBand),
			wantRule:    entryplan.RuleSemanticUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusSemanticUnresolved),
		},
		{
			// AHEAD OF BEAR. The shape is missing, so "BEAR forbids this shape" is a
			// sentence about nothing — there is no shape for it to forbid.
			name:        "an unknown entry shape outranks even a refusing regime",
			in:          dec(model.RegimeBear, entryplan.EntrySemanticUnknown, insideBand),
			wantRule:    entryplan.RuleSemanticUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusSemanticUnresolved),
		},
		{
			name: "a semantic that is not a semantic at all is the same state",
			in: dec(model.RegimeBull, entryplan.EntrySemantic("MOMENTUM_BUY"), insideBand,
				withPolicy(entryplan.PolicyFor(model.RegimeBull,
					entryplan.EntrySemantic("MOMENTUM_BUY")))),
			wantRule:    entryplan.RuleSemanticUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusSemanticUnresolved),
		},

		// ── S2. NO MARKET ─────────────────────────────────────────────────────────
		{
			name: "no current price at all",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				func(in *entryplan.DecisionInput) { in.CurrentPrice = nil }),
			wantRule:    entryplan.RuleCurrentPriceUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusCurrentPriceUnavailable),
		},
		{
			// MISSING ≠ ZERO, in the field every comparison below is made against.
			name:        "a current price of zero is an absence, not a cheap stock",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, 0),
			wantRule:    entryplan.RuleCurrentPriceUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusCurrentPriceUnavailable),
		},
		{
			// A NaN makes EVERY comparison below false, so it does not TRIP the guards, it
			// DISABLES them. Refused here rather than compared.
			name: "a NaN current price is refused before any comparison",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				func(in *entryplan.DecisionInput) { in.CurrentPrice = f(nan()) }),
			wantRule:    entryplan.RuleCurrentPriceUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusCurrentPriceUnavailable),
		},

		// ── S3 / S4. THE POLICY, AND THE PAIRED TEST spec §6 REQUIRES ─────────────
		//
		// BEAR → NO_VALID_ENTRY and UNKNOWN → INSUFFICIENT_DATA, side by side, on inputs
		// identical in every other respect. Both regimes publish NO plan; only these two
		// rows say that the two silences are different sentences.
		{
			name:        "BEAR is a VERDICT: the regime was known and the table said no",
			in:          dec(model.RegimeBear, entryplan.EntrySemanticPullback, insideBand),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},
		{
			name:        "UNKNOWN is a STATE: no decision was reached at all",
			in:          dec(model.RegimeUnknown, entryplan.EntrySemanticPullback, insideBand),
			wantRule:    entryplan.RulePolicyUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPolicyUnresolved),
		},
		{
			name:        "BEAR refuses the breakout shape too",
			in:          dec(model.RegimeBear, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},
		{
			name:        "UNKNOWN reaches no decision about a breakout either",
			in:          dec(model.RegimeUnknown, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RulePolicyUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPolicyUnresolved),
		},
		{
			// THE §13 ORDERING, and the row that makes it observable. Everything a
			// TOO_EXTENDED verdict needs is present — a band, a ceiling, and a price far
			// above it — and the answer is still NO_VALID_ENTRY, because TOO_EXTENDED
			// implies a LOWER price at which this would be bought and in BEAR there is none.
			name:        "BEAR far above its own chase ceiling is NO_VALID_ENTRY, not TOO_EXTENDED",
			in:          dec(model.RegimeBear, entryplan.EntrySemanticPullback, farAbove),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},
		{
			// The same ordering from the other side: UNKNOWN with the price above the
			// ceiling is still a STATE. A regime nobody could read does not acquire an
			// opinion about price by the price moving.
			name:        "UNKNOWN far above the ceiling is still INSUFFICIENT_DATA",
			in:          dec(model.RegimeUnknown, entryplan.EntrySemanticPullback, farAbove),
			wantRule:    entryplan.RulePolicyUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPolicyUnresolved),
		},
		{
			// A regime string that is not a regime: PolicyResult.Policy is nil. UNRESOLVED
			// and NOT DISALLOWED — a decoded typo is not a market state, and nobody decided.
			name:        "a regime that is not a regime reaches no decision",
			in:          dec(model.Regime("MELT_UP"), entryplan.EntrySemanticPullback, insideBand),
			wantRule:    entryplan.RulePolicyUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPolicyUnresolved),
		},
		{
			// The three regimes that permit the PULLBACK shape and refuse the BREAKOUT one.
			// A refusal of the SHAPE is a verdict, exactly as BEAR's is, and it arrives
			// before any price is read.
			name:        "BULL_PULLBACK refuses a breakout: buying into a correction",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},
		{
			name:        "SIDEWAYS refuses a breakout: no directional edge to break out into",
			in:          dec(model.RegimeSideways, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},
		{
			name:        "DISTRIBUTION refuses a breakout: a deteriorating tape punishes paying up",
			in:          dec(model.RegimeDistribution, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RulePolicyRefused,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPolicyRefused),
		},

		// ── S5 / S6. THE BAND, AND THE TWO WAYS IT CAN BE MISSING ─────────────────
		//
		// The pair spec §5 is about. Same nil pointer, two different verdicts, and the CODE
		// that travels with the nil is the only thing that can tell them apart.
		{
			name: "no band because the ATR was missing — a DEPENDENCY, so a state",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noZone(entryplan.ReasonZoneATRUnavailable)),
			wantRule:    entryplan.RuleZoneUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusEntryZoneUnavailable),
		},
		{
			name: "no band because every support sits at or above the market — a FINDING",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noZone(entryplan.ReasonPullbackLevelNotBelowCurrentPrice)),
			wantRule:    entryplan.RuleNoLegalZone,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoLegalEntryZone),
		},
		{
			// The band's own lower bound came out at or below zero: this stock's volatility
			// is wider than twice the level the band is centred on. Arithmetic that
			// COMPLETED on present terms, so a finding about the stock.
			name: "no band because the band's floor is not a price — also a FINDING",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noZone(entryplan.ReasonZoneLowNotAPrice)),
			wantRule:    entryplan.RuleNoLegalZone,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoLegalEntryZone),
		},
		{
			// A code this layer does not recognise reads as a GAP, never as a verdict. The
			// conservative direction, stated at AbsenceUnclassified: a state can be
			// corrected by better data; a verdict published in its place cannot be undone.
			name: "no band and no code at all is read as a gap, not as a verdict",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noZone("")),
			wantRule:    entryplan.RuleZoneUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusEntryZoneUnavailable),
		},

		// ── S7 / S8. THE RISK BOUNDARY, AHEAD OF EVERY PRICE ARM ──────────────────
		//
		// spec §17's split, read back. A plan that knows where it might buy and cannot say
		// where it would be proved wrong is not an executable trade plan AT ANY PRICE, so
		// these sit above the price arms — and both rows below put the market INSIDE the
		// band, where BUY_NOW would otherwise fire, to make that ordering visible.
		{
			name: "no risk boundary because nothing was ever comparable — a STATE",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noStop(entryplan.ReasonInvalidationEvidenceUnavailable)),
			wantRule:    entryplan.RuleRiskBoundaryUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusRiskBoundaryUnavailable),
		},
		{
			name: "no risk boundary because the rule contradicted itself — also a STATE",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noStop(entryplan.ReasonInvalidationSelectionInconsistent)),
			wantRule:    entryplan.RuleRiskBoundaryUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusRiskBoundaryUnavailable),
		},
		{
			name: "no risk boundary after every candidate was compared — a VERDICT",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noStop(entryplan.ReasonNoValidInvalidation)),
			wantRule:    entryplan.RuleNoRiskBoundary,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoRiskBoundary),
		},
		{
			name: "an unclassified stop code is a gap, and the market's position does not repair it",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				noStop("")),
			wantRule:    entryplan.RuleRiskBoundaryUnavailable,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusRiskBoundaryUnavailable),
		},

		// ── BULL × PULLBACK, ALL NINE PRICE RELATIONS ─────────────────────────────
		//
		// The unconditional row: every permission ALLOWED, no requirement attached. So this
		// is the axis read in isolation — whatever these nine rows say is the PRICE rule and
		// nothing else.
		{
			name:        "BULL pullback below the whole band: the level it was waiting for is gone",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, belowBand),
			wantRule:    entryplan.RuleNoExecutableRelation,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoExecutablePriceRelation),
		},
		{
			// CANONICAL BOUNDARY: one tick UNDER the floor is still outside the band.
			name:        "BULL pullback one tick below the band's floor is outside it",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, justBelowBand),
			wantRule:    entryplan.RuleNoExecutableRelation,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoExecutablePriceRelation),
		},
		{
			// CANONICAL BOUNDARY: the floor itself is BUYABLE. Bounds inclusive — a limit
			// order at zone.Low is a legal order, and an exclusive band would refuse the
			// exact price it was published to name.
			name:        "BULL pullback AT the band's floor is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, decZoneLow),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			name:        "BULL pullback inside the band is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			// CANONICAL BOUNDARY: the ceiling of the BAND is buyable too.
			name:        "BULL pullback AT the top of the band is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, decZoneHigh),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			// ABOVE the band and inside the ceiling — and it is WAIT_PULLBACK and NOT a
			// chase, because a pullback plan's instruction is "wait for price to come back",
			// and a market above the band is that instruction NOT YET SATISFIED.
			name:        "BULL pullback above the band waits, it does not chase",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			// CANONICAL BOUNDARY: AT the chase ceiling. The highest price the plan permits
			// paying is a price the plan permits paying, so this is NOT TOO_EXTENDED.
			name:        "BULL pullback exactly AT the chase ceiling is not extended",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, decCeiling),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			// CANONICAL BOUNDARY: one tick over, and the verdict changes. This row and the
			// one above it are the whole content of "STRICTLY above".
			name:        "BULL pullback ONE TICK above the ceiling IS TOO_EXTENDED",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, justOverCeil),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},
		{
			name:        "BULL pullback far above the ceiling is TOO_EXTENDED",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticPullback, farAbove),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},

		// ── BULL × BREAKOUT, ALL NINE ─────────────────────────────────────────────
		//
		// The SAME nine prices under the same regime, and four of the nine differ. That
		// difference is the entire content of "the shape is an input, not a synonym for the
		// answer".
		{
			name:        "BULL breakout below the band waits for the level to give way",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, belowBand),
			wantRule:    entryplan.RuleWaitBreakout,
			wantStatus:  entryplan.StatusWaitBreakout,
			wantReasons: rs(entryplan.ReasonStatusPriceBelowEntryZone),
		},
		{
			// CANONICAL BOUNDARY, and the row that would break if membership went exclusive
			// in the other direction: one tick below the pivot is still WAITING.
			name:        "BULL breakout one tick below the pivot is still waiting",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, justBelowBand),
			wantRule:    entryplan.RuleWaitBreakout,
			wantStatus:  entryplan.StatusWaitBreakout,
			wantReasons: rs(entryplan.ReasonStatusPriceBelowEntryZone),
		},
		{
			// CANONICAL BOUNDARY: AT the pivot the breakout has triggered, and the band's
			// floor is buyable. Exclusive membership would publish WAIT_BREAKOUT for a level
			// price is standing on.
			name:        "BULL breakout AT the pivot is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, decZoneLow),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			name:        "BULL breakout inside the band is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, insideBand),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			name:        "BULL breakout AT the top of the band is BUY_NOW",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, decZoneHigh),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			// THE CHASE. The level gave way, the fill was missed by less than one chase
			// tolerance, and BULL is the one shipped row whose Immediate permission is
			// ALLOWED — so paying up CONTINUES the instruction the plan was written with.
			name:        "BULL breakout above the band, inside the ceiling, is a permitted chase",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand),
			wantRule:    entryplan.RuleImmediateChase,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusImmediateChasePermitted),
		},
		{
			// CANONICAL BOUNDARY, on the arm where it costs the most: paying EXACTLY the
			// ceiling is still permitted.
			name:        "BULL breakout AT the ceiling is still a permitted chase",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, decCeiling),
			wantRule:    entryplan.RuleImmediateChase,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusImmediateChasePermitted),
		},
		{
			name:        "BULL breakout ONE TICK above the ceiling IS TOO_EXTENDED",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, justOverCeil),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},
		{
			name:        "BULL breakout far above the ceiling is TOO_EXTENDED",
			in:          dec(model.RegimeBull, entryplan.EntrySemanticBreakout, farAbove),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},

		// ── BULL_PULLBACK × PULLBACK ──────────────────────────────────────────────
		//
		// The row that proves a CEILING IS NOT A PERMISSION: 0.4×ATR of chase tolerance and
		// an Immediate permission that is DISALLOWED. Its BuyNow additionally carries
		// INSIDE_VALID_ZONE, which is the ONE requirement this layer can discharge — and it
		// discharges it, so the band is buyable.
		{
			name:        "BULL_PULLBACK inside the band discharges INSIDE_VALID_ZONE and buys",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticPullback, insideBand),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			name:        "BULL_PULLBACK AT the floor of the band buys",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticPullback, decZoneLow),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			name:        "BULL_PULLBACK above the band waits",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticPullback, aboveBand),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			name:        "BULL_PULLBACK past the ceiling is TOO_EXTENDED",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticPullback, justOverCeil),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},
		{
			name:        "BULL_PULLBACK below its own band has nothing left to wait for",
			in:          dec(model.RegimeBullPullback, entryplan.EntrySemanticPullback, belowBand),
			wantRule:    entryplan.RuleNoExecutableRelation,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusNoExecutablePriceRelation),
		},

		// ── SIDEWAYS × PULLBACK, and DISTRIBUTION × PULLBACK ──────────────────────
		//
		// The two regimes whose permissions carry a requirement NOTHING IN THIS REPO CAN
		// EVALUATE (NEAR_SUPPORT needs a support taxonomy; ELEVATED_EVIDENCE is a setup
		// score entryplan may not see). An unchecked condition must produce a STATE, never a
		// permission — so at-the-market entry is INSUFFICIENT_DATA and WAITING is untouched.
		{
			name:       "SIDEWAYS inside the band cannot conclude: NEAR_SUPPORT is unevaluated",
			in:         dec(model.RegimeSideways, entryplan.EntrySemanticPullback, insideBand),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusRequirementNotEvaluated),
		},
		{
			// AND YET IT CAN STILL WAIT. The requirement gates EXECUTION, and waiting does
			// not claim the entry may be taken now.
			name:        "SIDEWAYS above the band still waits — the requirement gates buying, not waiting",
			in:          dec(model.RegimeSideways, entryplan.EntrySemanticPullback, aboveBand),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			// spec §14 AND mutation 10, in one row. SIDEWAYS refuses to chase, so
			// ComputeMaxChase publishes NO ceiling — and "you may not chase" is NOT "you may
			// not wait". CHASE_NOT_ALLOWED reads as ABSENT BY DECISION, which is why S13
			// does not fire and this is WAIT_PULLBACK rather than INSUFFICIENT_DATA.
			name: "SIDEWAYS with chasing REFUSED still waits for the pullback",
			in: dec(model.RegimeSideways, entryplan.EntrySemanticPullback, aboveBand,
				noChase(entryplan.ReasonChaseNotAllowed)),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			name:       "DISTRIBUTION inside the band cannot conclude either",
			in:         dec(model.RegimeDistribution, entryplan.EntrySemanticPullback, insideBand),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusRequirementNotEvaluated),
		},
		{
			name:        "DISTRIBUTION above the band waits",
			in:          dec(model.RegimeDistribution, entryplan.EntrySemanticPullback, aboveBand),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			name:        "DISTRIBUTION past the ceiling is TOO_EXTENDED, requirement or not",
			in:          dec(model.RegimeDistribution, entryplan.EntrySemanticPullback, justOverCeil),
			wantRule:    entryplan.RuleAboveMaxChase,
			wantStatus:  entryplan.StatusTooExtended,
			wantReasons: rs(entryplan.ReasonStatusAboveMaxChase),
		},

		// ── S13. THE THREE READINGS OF A MISSING CEILING (spec §14) ───────────────
		{
			// AN EVIDENCE GAP. With price above the band and no ceiling COMPUTED, "merely
			// above" and "past anything this plan would pay" are both consistent with what
			// is known, and choosing between them would be a guess wearing a verdict's
			// output field.
			name: "above the band with the previous close missing: the ceiling cannot be computed",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				noChase(entryplan.ReasonPreviousCloseUnavailable)),
			wantRule:   entryplan.RuleChaseCeilingUnresolved,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusChaseCeilingUnresolved),
		},
		{
			name: "above the band with the chase policy itself unresolved",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				noChase(entryplan.ReasonChasePolicyUnresolved)),
			wantRule:   entryplan.RuleChaseCeilingUnresolved,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusChaseCeilingUnresolved),
		},
		{
			// ABSENT BY DECISION, second flavour: today's legal ceiling does not reach the
			// top of the band. No headroom, and the band is untouched — so a pullback still
			// waits.
			name: "above the band with no headroom over it still waits",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				noChase(entryplan.ReasonChaseCeilingBelowZone)),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},
		{
			// THE ARM'S SCOPE. INSIDE the band a missing ceiling changes nothing: zone
			// membership is the whole answer, and S10 sits above S13 for that reason.
			name: "INSIDE the band a missing ceiling is irrelevant",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				noChase(entryplan.ReasonPreviousCloseUnavailable)),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			// BELOW the band it is irrelevant too: a breakout that has not triggered is not
			// waiting on a ceiling.
			name: "BELOW the band a missing ceiling is irrelevant",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, belowBand,
				noChase(entryplan.ReasonPreviousCloseUnavailable)),
			wantRule:    entryplan.RuleWaitBreakout,
			wantStatus:  entryplan.StatusWaitBreakout,
			wantReasons: rs(entryplan.ReasonStatusPriceBelowEntryZone),
		},
		{
			// A CEILING THAT IS NOT A PRICE. Not nil, so `MaxChasePrice == nil` would miss
			// it; NaN, so `cur > ceiling` is FALSE and the TOO_EXTENDED guard would be
			// DISABLED rather than tripped. Read as no ceiling, and therefore as a gap.
			name: "a NaN ceiling is no ceiling, and it is a gap rather than a licence",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				func(in *entryplan.DecisionInput) { in.MaxChasePrice = f(nan()) }),
			wantRule:   entryplan.RuleChaseCeilingUnresolved,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusChaseCeilingUnresolved),
		},

		// ── S16. A BREAKOUT THAT RAN AND MAY NOT BE PAID UP FOR ───────────────────
		{
			// THE CEILING-LESS CHASE, refused. The ceiling is absent BY DECISION, so there
			// is no authorised price above the band at all — and BUY_NOW here would assert
			// "the ceiling has not been crossed" about a ceiling that does not exist.
			name: "a breakout above its band with chasing refused is a verdict, not a chase",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				noChase(entryplan.ReasonChaseNotAllowed)),
			wantRule:   entryplan.RuleImmediateChaseRefused,
			wantStatus: entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusImmediateEntryRefused),
		},
		{
			// THE PERMISSION REFUSED WITH A CEILING PRESENT. Stands for a future EP-2 row
			// pairing BULL_PULLBACK's DISALLOWED Immediate with a permitted breakout — the
			// exact shape ChasePolicy.Immediate was added to be able to express.
			name: "a breakout chase refused by the Immediate permission",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticBreakout,
					entryplan.AllowanceAllowed, nil, allowed(), refused()))),
			wantRule:   entryplan.RuleImmediateChaseRefused,
			wantStatus: entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusImmediateEntryRefused),
		},
		{
			// THE REQUIREMENT REFUSED. Stands for a future row that permits paying up while
			// its BuyNow permission still says INSIDE_VALID_ZONE — which BULL_PULLBACK's
			// shipped row says today. The requirement is FALSE here, and this is the arm
			// where it can be false: price is above the band by construction.
			name: "a breakout chase refused by INSIDE_VALID_ZONE, which is false above the band",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticBreakout,
					entryplan.AllowanceAllowed, nil,
					entryplan.Permission{Allowance: entryplan.AllowanceAllowed,
						Requirements: []entryplan.Requirement{entryplan.RequirementInsideZone}},
					allowed()))),
			wantRule:   entryplan.RuleImmediateChaseRefused,
			wantStatus: entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusRequirementNotMet),
		},
		{
			// A REFUSAL OUTRANKS AN UNEVALUATED CONDITION. resolveExecution folds the
			// allowances before the requirements precisely so that "no" cannot be downgraded
			// to "we could not tell" by a condition hanging off some other permission.
			name: "a refusal outranks an unevaluable requirement on the same chase",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticBreakout,
					entryplan.AllowanceAllowed,
					[]entryplan.Requirement{entryplan.RequirementNearSupport},
					allowed(), refused()))),
			wantRule:   entryplan.RuleImmediateChaseRefused,
			wantStatus: entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusImmediateEntryRefused),
		},

		// ── S15. THE CHASE PERMISSION UNDECIDED ───────────────────────────────────
		{
			// UNRESOLVED ≠ DISALLOWED, on the execution permission, above the band. A future
			// regime row that has not made up its mind about paying up must not be read as
			// having said no — that is the BEAR/UNKNOWN line at the last field it survives to.
			name: "a breakout chase whose permission is undecided is a STATE",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticBreakout,
					entryplan.AllowanceAllowed, nil, allowed(), undecided()))),
			wantRule:   entryplan.RuleImmediateChaseUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusImmediateEntryUnresolved),
		},
		{
			// The same state reached through an UNEVALUABLE requirement rather than an
			// undecided allowance: a condition this layer cannot read turns an ALLOWED
			// permission into UNRESOLVED, it does not leave it ALLOWED.
			name: "a breakout chase gated on a requirement nothing can evaluate is a STATE",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticBreakout,
					entryplan.AllowanceAllowed,
					[]entryplan.Requirement{entryplan.RequirementElevatedEvidence},
					allowed(), allowed()))),
			wantRule:   entryplan.RuleImmediateChaseUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusRequirementNotEvaluated),
		},

		// ── S11 / S12. INSIDE THE BAND, AND EXECUTION IS NOT PERMITTED ────────────
		{
			// REFUSED AT THE MARKET while the SHAPE is permitted. There is nothing left to
			// wait FOR — the market is already at the entry — so this is a verdict, and the
			// verdict carries the refusal's own code beside the price fact.
			name: "inside the band with buying at the market refused is a VERDICT",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticPullback,
					entryplan.AllowanceAllowed, nil, refused(), allowed()))),
			wantRule:   entryplan.RuleInsideZoneRefused,
			wantStatus: entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusImmediateEntryRefused),
		},
		{
			name: "inside the band with buying at the market UNDECIDED is a STATE",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticPullback,
					entryplan.AllowanceAllowed, nil, undecided(), allowed()))),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusImmediateEntryUnresolved),
		},
		{
			// A POLICY WITH NO POLICY BEHIND IT. PolicyResult.Policy is nil (a regime string
			// that is not a regime) but the Allowance field says ALLOWED — an inconsistent
			// projection a caller can build. buyNowPermission answers UNRESOLVED rather than
			// DISALLOWED: nobody decided.
			name: "an allowance with no policy behind it reaches no decision about executing",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				withPolicy(entryplan.PolicyResult{Regime: model.Regime("MELT_UP"),
					Semantic:  entryplan.EntrySemanticPullback,
					Allowance: entryplan.AllowanceAllowed})),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusImmediateEntryUnresolved),
		},
		{
			// INSIDE_VALID_ZONE DISCHARGED, and it is discharged from the ARM'S OWN
			// condition rather than from a second comparison — so the requirement cannot
			// disagree with the arm that chose it.
			name: "inside the band, INSIDE_VALID_ZONE is discharged and BUY_NOW stands",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, decZoneHigh,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticPullback,
					entryplan.AllowanceAllowed, nil,
					entryplan.Permission{Allowance: entryplan.AllowanceAllowed,
						Requirements: []entryplan.Requirement{entryplan.RequirementInsideZone}},
					allowed()))),
			wantRule:    entryplan.RuleInsideZone,
			wantStatus:  entryplan.StatusBuyNow,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone),
		},
		{
			// A REQUIREMENT NOBODY DECLARED. There is no default: case in
			// evaluateRequirement, so a Requirement added later is REFUSED (NOT_EVALUATED)
			// rather than silently satisfied — the failure direction that costs nothing.
			name: "a requirement this layer has never heard of is not satisfied by default",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticPullback,
					entryplan.AllowanceAllowed,
					[]entryplan.Requirement{entryplan.Requirement("EARNINGS_CLEARED")},
					allowed(), allowed()))),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusRequirementNotEvaluated),
		},

		// ── EP-6D: THE THESIS STANDING ─────────────────────────────────────────────
		//
		// The user's decisions (2026-09-14): WatchAction PULLBACK_BUY / BREAKOUT_BUY is the
		// existing BUY thesis; a stock whose primary Action is SELL, REDUCE, TAKE PROFIT or STOP
		// LOSS (CONTRADICTED) shows NO entry at all — NO_VALID_ENTRY at S1, whatever the price,
		// policy or band; an UNCLASSIFIED standing only ever loses BUY_NOW. These rows pin both.
		{
			name: "inside the band, a CONTRADICTED thesis is S1, not a buy",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			name: "a permitted breakout chase with a CONTRADICTED thesis is S1",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			// WIDENED: the answer that would have been WAIT_PULLBACK is not a wait either.
			name: "a CONTRADICTED thesis above a pullback band is S1, not a wait",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			name: "a CONTRADICTED thesis past the ceiling is S1, not TOO_EXTENDED",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, farAbove,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			name: "a CONTRADICTED thesis below a breakout level is S1, not WAIT_BREAKOUT",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, belowBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			// AHEAD OF THE PRICE ARM: the answer does not depend on where the market is.
			name: "a CONTRADICTED thesis with no current price is still S1",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				func(in *entryplan.DecisionInput) { in.CurrentPrice = nil },
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			// AHEAD OF THE POLICY ARMS: BEAR would have said the same status for a different
			// reason; the arm that answers is the thesis.
			name: "a CONTRADICTED thesis under BEAR is answered by S1",
			in: dec(model.RegimeBear, entryplan.EntrySemanticPullback, insideBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleThesisContradicted,
			wantStatus:  entryplan.StatusNoValidEntry,
			wantReasons: rs(entryplan.ReasonStatusThesisContradicted),
		},
		{
			// BEHIND S0: no resolved shape means no thesis to contradict — a state, as before.
			name: "a CONTRADICTED standing with no entry shape stays S0",
			in: dec(model.RegimeBull, entryplan.EntrySemanticUnknown, insideBand,
				thesis(entryplan.ThesisContradicted)),
			wantRule:    entryplan.RuleSemanticUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusSemanticUnresolved),
		},
		{
			// MISSING ≠ ZERO: an unclassified standing is not "not contradicted".
			name: "inside the band, an UNCLASSIFIED thesis is a state, not a buy",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				thesis("")),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusThesisUnclassified),
		},
		{
			name: "inside the band, an UNDECLARED thesis standing is the same state",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				thesis(entryplan.ThesisStanding("HOLD"))),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusThesisUnclassified),
		},
		{
			name: "a permitted breakout chase with an UNCLASSIFIED thesis is a state",
			in: dec(model.RegimeBull, entryplan.EntrySemanticBreakout, aboveBand,
				thesis("")),
			wantRule:   entryplan.RuleImmediateChaseUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone,
				entryplan.ReasonStatusThesisUnclassified),
		},
		{
			// An undecided permission keeps ITS code when the thesis is merely unclassified.
			name: "an undecided execution keeps its own code when the thesis is unclassified",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, insideBand,
				withPolicy(policyWith(model.RegimeBull, entryplan.EntrySemanticPullback,
					entryplan.AllowanceAllowed, nil, undecided(), allowed())),
				thesis("")),
			wantRule:   entryplan.RuleInsideZoneUndecided,
			wantStatus: entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusPriceInsideZone,
				entryplan.ReasonStatusImmediateEntryUnresolved),
		},
		{
			// NOT WIDENED for unclassified: a wait stays a wait.
			name: "an UNCLASSIFIED thesis does not touch WAIT_PULLBACK",
			in: dec(model.RegimeBull, entryplan.EntrySemanticPullback, aboveBand,
				thesis("")),
			wantRule:    entryplan.RuleWaitPullback,
			wantStatus:  entryplan.StatusWaitPullback,
			wantReasons: rs(entryplan.ReasonStatusPriceAboveEntryZone),
		},

		// ── THE ZERO INPUT ────────────────────────────────────────────────────────
		{
			// DecideStatus is TOTAL. The zero DecisionInput has no shape, so S0 answers it —
			// and it answers with a STATE, which is the only honest reading of a struct
			// nobody filled in.
			name:        "the zero input is answered, and it is answered with a state",
			in:          entryplan.DecisionInput{},
			wantRule:    entryplan.RuleSemanticUnresolved,
			wantStatus:  entryplan.StatusInsufficientData,
			wantReasons: rs(entryplan.ReasonStatusSemanticUnresolved),
		},
	}
}
