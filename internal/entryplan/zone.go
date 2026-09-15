package entryplan

import "github.com/deep-huang/stock-scanner/internal/pricerule"

// ── EP-3b: the ideal entry zone ────────────────────────────────────────────────────────
//
// The first actionable number this package has ever produced. Everything in here is written
// against one failure mode, which is not "the zone is slightly wrong" but "the zone is a
// fabrication that looks like a measurement":
//
//	if atr <= 0 { atr = currentPrice * 0.025 }      // the legacy priceTargets bug
//	if support <= 0 { support = currentPrice * 0.95 }
//	entry = currentPrice                            // the legacy EntryPrice
//
// None of those is a fallback. Each is a DIFFERENT RULE wearing the output field of the rule
// the reader thinks produced it, and a zone built that way survives every downstream check —
// it is finite, it is positive, it is ordered, it is on the tick grid, and it is not a level.
//
// So: when an input the rule needs is missing, the OUTPUT IS MISSING. Not a percentage, not a
// tick-only width, not the last price.

// The two width constants, named so they are reviewable without reading the arithmetic.
//
// HEURISTIC — NOT BACKTEST-FITTED, the same disclosure RuleVersion and the chase multipliers
// carry. There is no outcome study behind either number in this repo. What IS deliberate:
//
//   - zoneATRHalfMultiple is a HALF width, so the zone spans one full ATR from low to high.
//     A level is not a price and a day's range is the natural unit of "about here".
//   - zoneTickFloorTicks exists because a stock whose ATR is smaller than a couple of ticks
//     would otherwise get a zone narrower than the grid it trades on, i.e. a zone containing
//     at most one legal price, which is a limit order pretending to be a band.
//
// The tick floor is a FLOOR ON A WIDTH THAT ALREADY EXISTS. It is not a substitute for a
// missing ATR — see zoneHalfWidth.
const (
	zoneATRHalfMultiple = 0.5
	zoneTickFloorTicks  = 2
)

// atrPeriodForZoneWidth is the ATR period this rule is defined in terms of.
//
// FOURTEEN, checked rather than assumed. 0.5 × ATR(14) and 0.5 × ATR(5) are different widths,
// and an ATREvidence carrying Period 5 with a plausible value would silently produce the wrong
// zone — the field exists precisely so the consumer can refuse. ATREvidence's own doc says
// Period 0 means "the caller did not say", which is a refusal too: a width computed from an
// unnamed period cannot be reproduced from the archive.
const atrPeriodForZoneWidth = 14

// HalfWidthBinding says which term of the max() decided the half width. A stable code, kept on
// the trace, so a reader can tell a volatility-sized zone from a grid-sized one without
// recomputing anything.
type HalfWidthBinding string

const (
	// BindingATRHalf — 0.5 × ATR was the wider term. The normal case.
	BindingATRHalf HalfWidthBinding = "ATR_HALF"
	// BindingTickFloor — the tick floor was the wider term: this stock's ATR is smaller than
	// two ticks at this price.
	BindingTickFloor HalfWidthBinding = "TICK_FLOOR"
)

// AllHalfWidthBindings is the registry, checked from both ends by the zone tests.
var AllHalfWidthBindings = []HalfWidthBinding{BindingATRHalf, BindingTickFloor}

// WidthRejection is why no half width could be established. Persisted on the trace.
//
// Separate from Reason on purpose: these are the SUB-REASONS of one plan-level reason, and
// they are the part a reader needs in order to know what to fix. widthRejectionReason maps
// them onto the plan's vocabulary.
type WidthRejection string

const (
	// WidthRejectATRUnavailable — no usable ATR: not AVAILABLE, absent, non-finite, or not
	// positive. A zero ATR would claim a stock that does not move.
	WidthRejectATRUnavailable WidthRejection = "ATR_UNAVAILABLE"
	// WidthRejectATRBasisMismatch — the ATR was measured on a different price series from
	// the zone's own basis. Never converted; see PriceBasis.
	WidthRejectATRBasisMismatch WidthRejection = "ATR_PRICE_BASIS_MISMATCH"
	// WidthRejectATRPeriod — the ATR is not the period this rule is defined on. See
	// atrPeriodForZoneWidth.
	WidthRejectATRPeriod WidthRejection = "ATR_PERIOD_MISMATCH"
	// WidthRejectTickSize — pricerule.TickSize refused the centre, which it does for exactly
	// the values that are not prices (non-finite, non-positive). There is no default tick:
	// "probably 0.01" applied to an unknown price is how a bad number acquires the look of a
	// legal one.
	WidthRejectTickSize WidthRejection = "TICK_SIZE_UNAVAILABLE"
)

// AllWidthRejections is the registry, checked from both ends by the zone tests.
var AllWidthRejections = []WidthRejection{
	WidthRejectATRUnavailable,
	WidthRejectATRBasisMismatch,
	WidthRejectATRPeriod,
	WidthRejectTickSize,
}

// ZoneWidth is the half width and EVERY TERM IT WAS COMPUTED FROM.
//
// The terms are kept, not just the answer. "half width = 2.0" is unreadable six months later:
// it does not say whether the stock's ATR was 4.0 or whether its ATR was 0.02 and the tick
// floor took over, and those two zones mean opposite things about the stock.
type ZoneWidth struct {
	// ATR is the ATR(14) the width was computed from, in price units.
	ATR float64 `json:"atr"`
	// ATRHalfWidth is zoneATRHalfMultiple × ATR.
	ATRHalfWidth float64 `json:"atr_half_width"`
	// TickSize is pricerule.TickSize at the CENTRE — the tier that applies where the zone
	// sits, not where the last price sits.
	TickSize float64 `json:"tick_size"`
	// TickFloorWidth is zoneTickFloorTicks × TickSize.
	TickFloorWidth float64 `json:"tick_floor_width"`
	// HalfWidth is max(ATRHalfWidth, TickFloorWidth) — the one actually used.
	HalfWidth float64 `json:"half_width"`
	// Binding says which term won.
	Binding HalfWidthBinding `json:"binding"`
}

// zoneHalfWidth computes the half width for a zone centred at center.
//
//	halfWidth = max(0.5 × ATR14, 2 × TickSize(center))
//
// # An unavailable ATR means an unavailable ZONE. It does not mean a different rule
//
// This is the one decision in EP-3b most worth stating out loud, because the alternative is
// so easy to write: fall back to the tick floor alone, or to a percentage of the last price.
// Both are refused.
//
// A missing ATR is a MISSING DEPENDENCY, not the trigger condition of a second rule. And the
// two widths are not close: at 1245 the tick floor is 10 元 (two 5-元 ticks), while half of a
// realistic ATR there is 30–60 元. Publishing either through the same field, under the same
// name, leaves a reader unable to tell which rule produced the number in front of them — and
// the narrow one reads as a high-conviction, tightly-defined entry, which is the opposite of
// what it would mean.
//
// So the tick floor only ever WIDENS a width that already exists, and ok == false is returned
// with the precise reason otherwise. The caller publishes no zone.
func zoneHalfWidth(center float64, atr ATREvidence, zoneBasis PriceBasis) (ZoneWidth, WidthRejection, bool) {
	// 1. A usable ATR. Availability BEFORE basis and BEFORE period: a caller who sent no ATR
	// at all should be told that, not told its basis is wrong.
	if !(atr.Status.OK() && atr.Value != nil && finitePositive(*atr.Value)) {
		return ZoneWidth{}, WidthRejectATRUnavailable, false
	}
	// 2. Same price series as the zone. An ATR measured on the adjusted close is a span of a
	// different series; subtracting it from a raw level is arithmetic across two universes.
	if atr.PriceBasis != zoneBasis {
		return ZoneWidth{}, WidthRejectATRBasisMismatch, false
	}
	// 3. The period this rule is defined on.
	if atr.Period != atrPeriodForZoneWidth {
		return ZoneWidth{}, WidthRejectATRPeriod, false
	}

	// 4. The tick tier AT THE CENTRE. ok == false is handled explicitly — pricerule returns
	// (value, bool) on every function precisely so that a caller cannot write `v, _ :=` and
	// never find out.
	tick, ok := pricerule.TickSize(center)
	if !ok {
		return ZoneWidth{}, WidthRejectTickSize, false
	}

	w := ZoneWidth{
		ATR:            *atr.Value,
		ATRHalfWidth:   zoneATRHalfMultiple * *atr.Value,
		TickSize:       tick,
		TickFloorWidth: zoneTickFloorTicks * tick,
	}
	w.HalfWidth, w.Binding = w.ATRHalfWidth, BindingATRHalf
	if w.TickFloorWidth > w.HalfWidth {
		w.HalfWidth, w.Binding = w.TickFloorWidth, BindingTickFloor
	}
	// Non-finite cannot arise from a finite positive ATR and a table tick, and it is checked
	// anyway: every number this package publishes passes a finiteness gate, and a width is
	// about to become two prices.
	if !finite(w.HalfWidth) || w.HalfWidth <= 0 {
		return ZoneWidth{}, WidthRejectATRUnavailable, false
	}
	return w, "", true
}

// widthRejectionReason maps a width sub-reason onto the plan's reason vocabulary.
//
// Every member of AllWidthRejections is handled explicitly, so the trailing return is
// unreachable for any code in the registry — it exists because Go needs one, and because a
// rejection added later should land on the ATR reason rather than on "".
//
// TestEveryWidthRejectionMapsToAReason is what holds it: the registry's size is pinned, three
// of the four codes are produced by named cases and each is asserted to reach its OWN distinct
// plan reason, and the fourth (TICK_SIZE_UNAVAILABLE) is asserted to be UNREACHABLE through
// ComputePlan — the centre comes from a screened candidate, so it is finite and positive,
// which is exactly when pricerule.TickSize succeeds.
func widthRejectionReason(r WidthRejection) Reason {
	switch r {
	case WidthRejectATRBasisMismatch:
		return ReasonZoneATRBasisMismatch
	case WidthRejectATRPeriod:
		return ReasonZoneATRPeriodMismatch
	case WidthRejectTickSize:
		return ReasonZoneTickAlignmentUnavailable
	case WidthRejectATRUnavailable:
		return ReasonZoneATRUnavailable
	}
	return ReasonZoneATRUnavailable
}

// ZoneResult is the whole zone step: the zone if there is one, and the record of how it was
// reached if there is not.
//
// Zone == nil ⟺ Trace.Rejection != "". Both directions are asserted, because a nil zone with
// no reason is a shrug and a zone with a rejection code attached is two answers.
type ZoneResult struct {
	// Zone is the published ideal entry zone, or nil. Tick-aligned, ordered, positive.
	Zone *PriceZone `json:"zone,omitempty"`
	// Trace is the work record — every candidate, every term of the width, the raw bounds
	// before alignment, and the rejection if there was one.
	Trace ZoneTrace `json:"trace"`
}

// ComputeIdealEntryZone computes the ideal entry zone for one snapshot under one policy.
//
// PURE and TOTAL: defined for every input including the zero Snapshot, no clock, no I/O, and
// it never retains a pointer from its argument.
//
// # The policy is an ARGUMENT, not something this function decides
//
// It must be PolicyFor(in.Regime.Regime, in.EntrySemantic.Semantic) — ComputePlan passes
// exactly that. It is threaded through rather than recomputed here so the policy row on the
// plan's evidence census and the policy the zone was actually built under CANNOT DISAGREE: two
// calls are two chances for one of them to be edited.
//
// # The order of the gates is the order of the sentences a reader needs
//
//  1. Is this KIND of entry permitted at all? (policy) — a BEAR pullback zone is not a
//     narrower zone, it is not a zone.
//  2. Is there a LEVEL to centre it on? (candidates → screen → select)
//  3. Is there a WIDTH? (ATR)
//  4. Are the resulting bounds PRICES? (positive, tick-aligned)
//
// Nothing later substitutes for anything earlier.
func ComputeIdealEntryZone(in Snapshot, policy PolicyResult) ZoneResult {
	semantic := in.EntrySemantic.Semantic
	basis := in.CurrentPrice.BasisOr(in.PriceBasis)

	tr := ZoneTrace{
		Semantic:   semantic,
		PriceBasis: basis,
	}

	// The candidate list is recorded for EVERY outcome, including the ones that refuse
	// before screening. A plan that says "no zone" and shows no candidates is
	// indistinguishable from one produced by a rule that never looked at a level.
	cands := CandidateLevelsFor(in, semantic)

	// 1. POLICY. Three answers and they are not merged: see Allowance.
	switch policy.Allowance {
	case AllowanceAllowed:
		// Permitted. Not a go — see Allowance's own doc — and the Requirements travelling
		// with it are EP-5's to evaluate.
	case AllowanceDisallowed:
		tr.Candidates = markNotScreened(cands)
		tr.Rejection = ReasonZoneSemanticNotPermitted
		return ZoneResult{Trace: tr}
	default:
		// UNRESOLVED, or a policy field nobody filled in. NO DECISION WAS MADE, which is a
		// different fact from "this entry is forbidden" — the distinction EP-2 exists to
		// protect, carried here rather than collapsed.
		tr.Candidates = markNotScreened(cands)
		tr.Rejection = ReasonZonePolicyUnresolved
		return ZoneResult{Trace: tr}
	}

	// 2. THE CENTRE.
	screened := ScreenCandidateLevels(cands, in.CurrentPrice, in.PriceBasis)
	screened, best, ok := SelectCenter(screened, semantic)
	tr.Candidates = screened
	if !ok {
		tr.Rejection = levelRejectionReason(screened, semantic)
		return ZoneResult{Trace: tr}
	}
	center, ok := screened[best].value()
	if !ok {
		// SelectCenter never returns an index without a number. Refused rather than
		// dereferenced, because the alternative to this branch is a panic in a report.
		tr.Rejection = levelRejectionReason(screened, semantic)
		return ZoneResult{Trace: tr}
	}
	tr.CenterSource = screened[best].Source
	tr.Center = copyFloat(center)

	// 3. THE WIDTH.
	tr.ATRPeriod = in.ATR.Period
	if in.ATR.Value != nil && finite(*in.ATR.Value) {
		// Recorded even when it is about to be refused: "the ATR was 0" and "there was no
		// ATR" are different rows. Non-finite is NOT recorded — nothing this package
		// publishes may carry a NaN, including a diagnostic.
		tr.ATR = copyFloat(*in.ATR.Value)
	}
	width, rej, ok := zoneHalfWidth(center, in.ATR, basis)
	if !ok {
		tr.WidthRejection = rej
		tr.Rejection = widthRejectionReason(rej)
		return ZoneResult{Trace: tr}
	}
	tr.ATRHalfWidth = copyFloat(width.ATRHalfWidth)
	tr.TickSize = copyFloat(width.TickSize)
	tr.TickFloorWidth = copyFloat(width.TickFloorWidth)
	tr.HalfWidth = copyFloat(width.HalfWidth)
	tr.HalfWidthBinding = width.Binding

	// 4. THE BOUNDS. The two semantics build them differently, and the difference is the
	// whole geometry of the trade: a pullback zone SURROUNDS its level, a breakout zone sits
	// ON TOP of it.
	var rawLow, rawHigh float64
	var lowDir, highDir pricerule.RoundDir
	switch semantic {
	case EntrySemanticPullback:
		// Centred on the support: center ± halfWidth.
		//
		// NOT CLAMPED TO THE CURRENT PRICE. If the upper bound lands at or above the last
		// price, the zone stays as computed and the fact is flagged (see
		// ReasonPullbackZoneOverlapsCurrentPrice). Setting High = CurrentPrice would keep
		// the low at center − halfWidth while cutting the high, leaving an ASYMMETRIC band
		// that is no longer centred on anything — and the sentence "the zone is the support
		// ± half an ATR", which is the only reason the number is defensible, would become
		// false while still being printed.
		rawLow, rawHigh = center-width.HalfWidth, center+width.HalfWidth
		// DOWN / UP: the zone is the band of prices this entry ACCEPTS, so alignment must
		// COVER the computed band rather than shave it. Rounding the low up would refuse
		// fills the rule allows; rounding the high down would do the same at the top. Both
		// directions therefore move outward, and the tick grid is the reason the raw bounds
		// are not orderable prices in the first place.
		lowDir, highDir = pricerule.RoundDown, pricerule.RoundUp
	case EntrySemanticBreakout:
		// Sits ON the pivot: [pivot, pivot + halfWidth].
		//
		// THE LOW DOES NOT EXTEND DOWN. A breakout entry below the pivot is not an entry on
		// a breakout — it is a bet the level will be cleared, which is the trade the
		// PULLBACK semantic describes and a different thesis with a different invalidation.
		rawLow, rawHigh = center, center+width.HalfWidth
		// UP / UP. Up at the low because the low IS the breakout level: aligning it down
		// would place the bottom of a "breakout zone" BELOW the price that defines the
		// breakout, i.e. it would buy the level before it broke. Up at the high for the
		// same covering argument as the pullback case.
		lowDir, highDir = pricerule.RoundUp, pricerule.RoundUp
	default:
		// Unreachable: SelectCenter refuses an unresolved semantic. Stated as a refusal
		// rather than a default so that a semantic added later cannot inherit the pullback
		// geometry by accident.
		tr.Rejection = ReasonEntrySemanticUnavailable
		return ZoneResult{Trace: tr}
	}
	tr.RawLow, tr.RawHigh = copyFloat(rawLow), copyFloat(rawHigh)
	tr.TickDirections = TickDirections{Low: tickDirection(lowDir), High: tickDirection(highDir)}

	// A raw low that is not a price. Reachable: a stock whose ATR is more than twice its own
	// support level (a wide-ranging penny stock) produces center − halfWidth <= 0.
	//
	// REFUSED, not repaired. `if low <= 0 { low = tick }` would publish a bound the rule
	// never computed, and the zone's width — the only thing that made it meaningful — would
	// silently become whatever was left above zero.
	if !finitePositive(rawLow) {
		tr.Rejection = ReasonZoneLowNotAPrice
		return ZoneResult{Trace: tr}
	}

	// 5. TICK ALIGNMENT. Every published price goes through pricerule; there is no
	// math.Round(v*10)/10 and no %.1f in this package, and a source scan asserts it.
	low, ok := pricerule.RoundToTick(rawLow, lowDir)
	if !ok {
		tr.Rejection = ReasonZoneTickAlignmentUnavailable
		return ZoneResult{Trace: tr}
	}
	high, ok := pricerule.RoundToTick(rawHigh, highDir)
	if !ok {
		tr.Rejection = ReasonZoneTickAlignmentUnavailable
		return ZoneResult{Trace: tr}
	}

	// 6. THE GEOMETRY, AFTER ALIGNMENT — because alignment is arithmetic and arithmetic is
	// where an invariant stops holding.
	if low > high || !finitePositive(low) || !finitePositive(high) {
		tr.Rejection = ReasonZoneTickAlignmentUnavailable
		return ZoneResult{Trace: tr}
	}
	if semantic == EntrySemanticBreakout && low < center {
		// The breakout low must still be AT OR ABOVE the pivot after alignment. RoundUp can
		// land a hair below its input when the input sits within pricerule's snap tolerance
		// of a grid point (0.0001 元) — which only happens for an input that is not a whole
		// number of cents, i.e. not a price any exchange quoted. Refused rather than nudged
		// up by a tick: a level that is not a legal quote is not a level, and inventing the
		// next tick would publish a breakout price nobody broke out of.
		tr.Rejection = ReasonBreakoutLevelNotAQuote
		return ZoneResult{Trace: tr}
	}

	tr.Low, tr.High = copyFloat(low), copyFloat(high)

	zone := &PriceZone{
		Low:        low,
		High:       high,
		PriceBasis: basis,
		Basis:      []string{string(tr.CenterSource)},
	}
	// WidthATR measures the PUBLISHED zone, not the raw one: it is what a reader compares
	// across stocks, and the raw band is not what anyone can buy.
	wATR := (high - low) / width.ATR
	if finite(wATR) {
		zone.WidthATR = &wATR
	}

	// The pullback overlap FLAG. Recorded, and it decides nothing here: whether an overlap
	// makes the plan BUY_NOW (the market is already in the zone) or TOO_EXTENDED is a STATUS
	// decision, and EP-3b does not compute a status.
	if semantic == EntrySemanticPullback && in.CurrentPrice.Usable() && high >= *in.CurrentPrice.Value {
		tr.OverlapsCurrentPrice = true
	}

	return ZoneResult{Zone: zone, Trace: tr}
}

// markNotScreened stamps a candidate list that never reached the screen.
//
// It exists so a policy refusal does not publish a list of blank dispositions: NOT_SCREENED is
// a fact ("the conditions were never evaluated"), and an empty string is a field nobody filled
// in. Rows already marked NOT_A_CANDIDATE_FOR_SEMANTIC keep that, because it is the more
// specific true statement.
func markNotScreened(cands []CandidateLevel) []CandidateLevel {
	out := make([]CandidateLevel, len(cands))
	copy(out, cands)
	for n := range out {
		if out[n].Disposition == "" {
			out[n].Disposition = DispositionNotScreened
		}
	}
	return out
}

// copyFloat puts a copy of v on the heap.
//
// A copy, always: every number this package publishes is detached from the caller's storage,
// so a caller mutating its own float after ComputePlan returns cannot change an
// already-returned plan. TestComputePlanIsPureAndDoesNotAliasItsInput is the guard.
func copyFloat(v float64) *float64 { return &v }
