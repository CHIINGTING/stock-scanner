package entryplan

import "github.com/deep-huang/stock-scanner/internal/pricerule"

// ── EP-3b: the highest price this plan may be paid ────────────────────────────────────
//
// MaxChasePrice is a CEILING, and that single fact decides most of this file:
//
//	rawMaxChase = IdealEntry.High + policy.MaxATR × ATR14
//	limitUp     = pricerule.LimitUpPrice(PreviousClose, rule)
//	clamped     = min(rawMaxChase, limitUp)
//	MaxChase    = pricerule.RoundToTick(clamped, RoundDown)
//
// CLAMP BEFORE ROUND, and round DOWN. Both are load-bearing:
//
//   - Rounding UP would publish a legal-looking price ABOVE the limit the rule just computed.
//     For a ceiling that is not a rounding error, it is a different ceiling: 104.8 rounded up
//     is 105.0, and the plan would then permit paying more than its own arithmetic allowed.
//   - Clamping BEFORE aligning is the order that does not depend on another package's
//     post-condition. Aligning first and clamping afterwards happens to give the same answer
//     TODAY — but only because limitUp comes back from pricerule already aligned DOWN onto the
//     grid, so min(align(raw), limitUp) and align(min(raw, limitUp)) coincide. That is a fact
//     about pricerule, and pricerule is free to change it; this order needs only that the
//     clamp compares two numbers.
//
// What IS pinned by test, rather than argued: MaxChase <= limitUp and MaxChase <= rawMaxChase
// for every case in a sweep over eight previous closes, six levels, five ATRs and two regimes
// (TestTheChaseCeilingRespectsBothLimits, with the ceiling recomputed from an INDEPENDENT
// integer ±10% in the test); the exact answer at the boundary where rawMaxChase == limitUp,
// which must be the ceiling itself and must NOT report a clamp
// (TestTheBoundaryWhereTheRawChaseEqualsTheLegalCeiling); and the exact answer for an off-grid
// raw ceiling, which is the case where DOWN and UP differ (TestTheChaseCeilingIsAlignedDown).
//
// Note what the daily band does NOT protect: since limitUp is on the grid, aligning UP after
// the clamp could never breach it. The band would have absorbed the mistake, and the plan would
// have published a ceiling above its own arithmetic with nothing to show it. That is why the
// direction is asserted separately from the clamp.
//
// # There is no fallback anywhere in here
//
// Not for the previous close (`previousClose = &currentPrice` would band today's price around
// itself), not for the ATR, not for the limit rule. Every missing input produces NO CEILING
// and a code that says which input was missing — because a chase limit is the number that
// authorises paying above the ideal entry, and a fabricated one authorises it for a reason
// nobody chose.

// ChaseResult is the chase step: the ceiling if there is one, and the record of how it was
// reached if there is not.
//
// MaxChasePrice == nil ⟺ (Trace.Rejection != "" OR there was no zone to chase into). The
// second disjunct is the one case with no code of its own; see ChaseTrace.Rejection.
type ChaseResult struct {
	// MaxChasePrice is the highest price the plan permits paying. Tick-aligned, positive,
	// never below the top of the ideal entry zone.
	MaxChasePrice *float64 `json:"max_chase_price,omitempty"`
	// Trace is the work record: every term, the legal ceiling, and whether it bit.
	Trace ChaseTrace `json:"trace"`
}

// ComputeMaxChase computes the chase ceiling for one snapshot, one zone and one policy.
//
// PURE and TOTAL, and it never retains a pointer from its arguments.
//
// # The three nils EP-2 refused to merge, honoured here
//
// ChasePolicy splits the DECISION (Allowance) from the NUMBER (MaxATR) precisely so that this
// function can tell these apart, and they get three different codes:
//
//	DISALLOWED   → CHASE_NOT_ALLOWED          a decision, on sufficient evidence (BEAR)
//	UNRESOLVED   → CHASE_POLICY_UNRESOLVED    no decision was made (UNKNOWN regime)
//	no prevClose → PREVIOUS_CLOSE_UNAVAILABLE a missing input, under a policy that allowed it
//
// Merging any two of them would make "the market is bearish" and "we could not read the
// market" the same row in a watchlist, which is the failure model.Regime's own doc is written
// against and which no reader of an archived plan could undo.
//
// # The zone and the ceiling are independently available
//
// A missing previous close removes the ceiling and leaves the zone. That is deliberate: the
// zone is arithmetic on levels and volatility, and the ceiling is arithmetic on a regulatory
// band. They have no input in common except the ATR, and coupling them would delete a good
// entry zone because a different field was missing.
func ComputeMaxChase(in Snapshot, zone *PriceZone, policy PolicyResult) ChaseResult {
	var tr ChaseTrace

	chase := ChasePolicy{Allowance: AllowanceUnresolved}
	if policy.Policy != nil {
		chase = policy.Policy.Chase
	}
	if chase.MaxATR != nil {
		tr.MaxATR = copyFloat(*chase.MaxATR)
	}

	// 1. THE POLICY, reported whether or not a zone exists. Whether this regime tolerates
	// chasing is a fact about the regime, and withholding it until a zone happens to exist
	// would make the answer depend on an unrelated input.
	switch chase.Allowance {
	case AllowanceAllowed:
		if chase.MaxATR == nil {
			// ChasePolicy's invariant is MaxATR != nil ⟺ ALLOWED, so this is unreachable
			// through PolicyFor. It is handled rather than dereferenced, and it maps to
			// UNRESOLVED because a policy that permits chasing without naming a tolerance
			// has RESOLVED NO TOLERANCE — the honest reading, not "chasing is forbidden".
			// TestAnAllowedChaseWithNoMultiplierIsUnresolved plants exactly that policy.
			tr.Rejection = ReasonChasePolicyUnresolved
			return ChaseResult{Trace: tr}
		}
	case AllowanceDisallowed:
		tr.Rejection = ReasonChaseNotAllowed
		return ChaseResult{Trace: tr}
	default:
		tr.Rejection = ReasonChasePolicyUnresolved
		return ChaseResult{Trace: tr}
	}

	// 2. THE ZONE. Nothing to chase INTO, so there is nothing to bound — and no code of our
	// own: ZoneTrace.Rejection already says why there is no zone, and a second code here
	// would read as a second, independent failure.
	if zone == nil {
		return ChaseResult{Trace: tr}
	}
	tr.ZoneHigh = copyFloat(zone.High)

	// 3. THE ATR. Available by construction whenever a zone exists — the zone's own width
	// needed it — so its absence has no code of its own here either. Re-checked rather than
	// assumed, because "the zone exists therefore the ATR exists" is a property of another
	// function.
	if !(in.ATR.Status.OK() && in.ATR.Value != nil && finitePositive(*in.ATR.Value)) {
		tr.Rejection = ReasonZoneATRUnavailable
		return ChaseResult{Trace: tr}
	}
	atr := *in.ATR.Value
	tr.ATR = copyFloat(atr)

	basis := in.CurrentPrice.BasisOr(in.PriceBasis)

	// 4. THE PREVIOUS CLOSE. The one input with no substitute in this file.
	if !in.PreviousClose.Usable() {
		tr.Rejection = ReasonPreviousCloseUnavailable
		return ChaseResult{Trace: tr}
	}
	if in.PreviousClose.BasisOr(in.PriceBasis) != basis {
		// A previous close on a different series from the zone. Never converted.
		tr.Rejection = ReasonPreviousCloseBasisMismatch
		return ChaseResult{Trace: tr}
	}
	prevClose := *in.PreviousClose.Value
	tr.PreviousClose = copyFloat(prevClose)

	// 5. THE BASIS THE BAND IS DEFINED ON. A daily price limit is ±10% of the exchange's own
	// reference price, which is a RAW quote. A back-adjusted previous close is a synthetic
	// number that no band was ever computed from, so ±10% of it is not a ceiling — it is a
	// plausible number with no regulation behind it. Refused rather than published.
	//
	// The ZONE survives on an adjusted basis (it is internally consistent: adjusted levels,
	// adjusted volatility, and PriceZone.PriceBasis says so). The CEILING does not, because
	// its authority comes from a rule about raw prices.
	if basis != PriceBasisRaw {
		tr.Rejection = ReasonChaseLimitRequiresRawBasis
		return ChaseResult{Trace: tr}
	}

	// 6. THE LIMIT REGIME, from the SHAPE OF THE CODE — the only thing pricerule can read it
	// from. UNKNOWN is a real and honest answer, not a failure: the "00" block is ETFs, an
	// ETF on foreign constituents has NO daily limit while one on domestic constituents has
	// ±10%, and nothing in this repo records which is which (stockcatalog carries no
	// instrument class). Measured on the repo's own cache: 12 of 1,995 cached codes classify
	// UNKNOWN.
	//
	// So there is no ceiling THIS PACKAGE KNOWS OF, and the chase is refused. Publishing an
	// unclamped ceiling instead would be the alternative, and it is worse: for a domestic-
	// constituent ETF it authorises paying above the legal band, i.e. an order the exchange
	// rejects, which is precisely the "actually placeable price" property EP-3b exists to
	// deliver.
	rule := pricerule.ClassifyLimitRule(in.Symbol)
	tr.LimitRule = rule
	if rule != pricerule.LimitRule10Pct {
		tr.Rejection = ReasonChaseLimitRuleUnavailable
		return ChaseResult{Trace: tr}
	}

	limitUp, ok := pricerule.LimitUpPrice(prevClose, rule)
	if !ok {
		// pricerule refused: the previous close is outside the domain of things that are
		// prices, or one end of the band does not align. No default ceiling — MISSING ≠
		// ZERO, and a fabricated ceiling is the one number in this plan that would look
		// most authoritative.
		tr.Rejection = ReasonChaseLimitPriceUnavailable
		return ChaseResult{Trace: tr}
	}
	tr.LimitUp = copyFloat(limitUp)
	tr.LimitAssumption = limitAssumption

	// 7. THE ARITHMETIC.
	raw := zone.High + *chase.MaxATR*atr
	if !finitePositive(raw) {
		tr.Rejection = ReasonChaseLimitPriceUnavailable
		return ChaseResult{Trace: tr}
	}
	tr.Raw = copyFloat(raw)

	clamped, clamp := raw, ClampNotApplied
	if raw > limitUp {
		clamped, clamp = limitUp, ClampToLimitUp
	}
	tr.Clamp = clamp

	tr.TickDirection = tickDirection(pricerule.RoundDown)
	final, ok := pricerule.RoundToTick(clamped, pricerule.RoundDown)
	if !ok {
		// Unreachable through this function: clamped <= limitUp, and limitUp came back from
		// RoundToTick, so it is inside the alignable domain and strictly positive. The
		// branch exists because that argument rests on pricerule's domain bound, which is
		// pricerule's to change, and because a `v, _ :=` here is exactly the shape the EP-0
		// review warned about. ReasonChaseTickAlignmentUnavailable is a RESERVED reason for
		// that reason — see ReservedReasons.
		tr.Rejection = ReasonChaseTickAlignmentUnavailable
		return ChaseResult{Trace: tr}
	}

	// 8. THE CEILING MUST COVER THE ZONE IT BOUNDS. When the day's legal ceiling sits BELOW
	// the top of the ideal entry, there is no chase headroom at all: the plan would otherwise
	// carry a limit that forbids paying what it simultaneously calls an ideal entry, which is
	// InvariantMaxChaseCoversZone — the plan invariant EP-3a shipped for exactly this.
	//
	// It is refused as a CHASE outcome rather than by widening the invariant, because the
	// zone is still the right zone: the level and the volatility have not changed, only
	// today's band. The reason code says which of the two it was.
	if final < zone.High {
		tr.Rejection = ReasonChaseCeilingBelowZone
		return ChaseResult{Trace: tr}
	}
	tr.Final = copyFloat(final)
	return ChaseResult{MaxChasePrice: copyFloat(final), Trace: tr}
}
