package entryplan

import "github.com/deep-huang/stock-scanner/internal/pricerule"

// ── EP-4: the price at which the idea was wrong ────────────────────────────────────────
//
// This file computes ONE number and it is not a stop loss:
//
//	Invalidation  the price below which the SUPPORT / BREAKOUT THESIS no longer holds
//
// A stop loss is a position-sizing decision — how much of an account may be lost on one idea —
// and it belongs to the position layer. This is the STRUCTURAL statement, and the difference is
// the whole reason Invalidation is not called Stop (see the type).
//
// # The three shapes this file refuses, all of them inherited
//
// internal/scanner/scorer.go:723 (priceTargets) computes, for every stock:
//
//	stop = entry - 2*atr        a fixed volatility budget, not a level
//	stop = bbLower * 0.99       a band edge, shaved by an arbitrary 1%
//	stop = entry * 0.93         a fixed 7% loss
//
// None of the three is an invalidation. Each answers "how much am I willing to lose", which is
// a different question with a different owner, and each produces a number that is finite,
// positive, below the entry and on the tick grid — so nothing downstream can tell it apart from
// a level. A plan that publishes one of them through this field says "the setup has failed"
// when what happened is "the loss budget was reached", and the two call for opposite actions:
// one means abandon the idea, the other means the position was too big.
//
// So there is NO percentage anywhere in this file, no multiple of the CURRENT price, and no
// fabricated floor when nothing qualifies. A missing invalidation is spelled nil, with
// ReasonNoValidInvalidation, and it costs the plan its targets and its risk/reward — which is
// correct: a trade whose failure point cannot be stated has no risk/reward.

// invalidationATRMultiple is how far BELOW the bottom of the ideal entry zone the volatility
// fallback sits, in ATR(14) units.
//
// HEURISTIC — NOT BACKTEST-FITTED, and 0.5 IS NOT CLAIMED TO BE OPTIMAL. There is no outcome
// study behind it in this repo; internal/r6backtest has stop-profile results, they are for a
// different construction (a trailing/initial stop measured from an entry FILL), and borrowing a
// number across that gap would be a fitted claim with no fit behind it. What IS deliberate is
// the SHAPE, and it is what makes the number meaningful rather than arbitrary:
//
//	pullback:  zone.Low − 0.5×ATR  =  (centre − 0.5×ATR) − 0.5×ATR  =  centre − ONE FULL ATR
//	breakout:  zone.Low − 0.5×ATR  =  pivot − 0.5×ATR
//
// because zone.Low is centre − zoneATRHalfMultiple×ATR for a pullback and the pivot itself for
// a breakout (see ComputeIdealEntryZone). So the fallback is not a loss budget dressed up as a
// level: for a pullback it says "the support this entry was priced from has failed by more than
// one full day's range", and for a breakout it says "price has fallen back below the level it
// broke out of by half a day's range" — a false breakout. Both are statements about the THESIS.
//
// It is still the SECOND tier. See SelectInvalidation: an observed structural level always wins.
const invalidationATRMultiple = 0.5

// StopSource names WHERE an invalidation candidate came from.
//
// A registry rather than a free string, for the reason LevelSource is one, and a SEPARATE
// vocabulary from LevelSource because one of its members is not an observed level at all: the
// volatility fallback is DERIVED from the published zone. Filing it under LevelSource would
// make "the plan is built on levels" false while continuing to print it.
type StopSource string

const (
	// StopSourceMA60 — the 60-session moving average. The slower trend support, and the
	// level whose loss means the 1–4 week trend context the entry assumed is gone.
	StopSourceMA60 StopSource = "MA60"

	// StopSourceBaseLow — the low of the consolidation base the move started from. The
	// STRONGEST structural statement available here: buyers defended it for at least three
	// sessions, so a break says the supply/demand balance the setup rests on has changed.
	//
	// USABLE ONLY WHEN Snapshot.BaseLowKind IS CONSOLIDATION_BASE. The same field carries
	// today's single-bar low when the scanner found no base; see BaseLowKind.
	StopSourceBaseLow StopSource = "BASE_LOW"

	// StopSourceZoneATRBuffer — zone.Low − invalidationATRMultiple × ATR(14). The
	// volatility FALLBACK, and the only member that is not an observed level.
	StopSourceZoneATRBuffer StopSource = "ZONE_LOW_MINUS_ATR"
)

// AllStopSources is every invalidation source, in the order candidates are reported.
//
// The order is load-bearing in exactly one place — it breaks a tie between two structural
// candidates holding the same price (see SelectInvalidation) — and is otherwise the stable
// order an archived trace is diffed in. The fallback is LAST because it is the last resort,
// which makes the trace read in the order the rule decides in.
var AllStopSources = []StopSource{
	StopSourceMA60,
	StopSourceBaseLow,
	StopSourceZoneATRBuffer,
}

// KnownStopSource reports whether a source is in the registry.
func KnownStopSource(s StopSource) bool {
	for _, k := range AllStopSources {
		if k == s {
			return true
		}
	}
	return false
}

// Valid guards against a source string no producer can emit (e.g. decoded from an older
// archive). The REGISTRY decides — one list, nothing to drift.
func (s StopSource) Valid() bool { return KnownStopSource(s) }

// Structural reports whether a source is an OBSERVED LEVEL rather than the derived fallback.
//
// This is the tier boundary and it is stated once. See SelectInvalidation for why the tiers
// are not merged into one max().
func (s StopSource) Structural() bool {
	return s == StopSourceMA60 || s == StopSourceBaseLow
}

// SupportsSemantic reports whether a source is a candidate for one entry semantic.
//
// # PULLBACK: all three
//
// The entry is a retracement into support, so the thesis is "this support holds". Both the base
// low and the MA60 are levels whose loss falsifies it, and the fallback says the entry's own
// centre failed by more than a day's range.
//
// # BREAKOUT: the base low and the fallback, and NOT the MA60
//
// A breakout thesis is "the base was cleared and holds". The base's OWN floor falsifies it
// directly — back at the bottom of the base, nothing was broken out of, and BaseLow is that
// floor by construction: internal/scanner/consolidation.go derives PivotHigh and BaseLow from
// the SAME base window (`pivotHigh, baseLow := windowHighLow(candles, baseStart, n-1)`), so the
// pivot the breakout zone is built on and this floor are the two ends of one structure.
//
// The MA60 is excluded because it is not part of that structure. A breakout can be perfectly
// alive far above the MA60 and dead the moment it falls back into its base, so an MA60-based
// invalidation for a breakout would be a level that answers a different question — and, sitting
// much lower, it would silently multiply the risk denominator and flatter every risk/reward.
//
// An unresolved semantic supports nothing: there is no thesis to be falsified.
func (s StopSource) SupportsSemantic(sem EntrySemantic) bool {
	switch sem {
	case EntrySemanticPullback:
		return s == StopSourceMA60 || s == StopSourceBaseLow || s == StopSourceZoneATRBuffer
	case EntrySemanticBreakout:
		return s == StopSourceBaseLow || s == StopSourceZoneATRBuffer
	}
	return false
}

// RiskLevelDisposition is what happened to one level on the RISK SIDE of the plan — an
// invalidation candidate, or the resistance a target is measured against.
//
// ONE vocabulary for both, because both answer the same shape of question ("was this level
// usable here, and if not which condition failed") and two near-identical registries would
// drift. Every candidate always carries one: "the invalidation is the MA60" does not say
// whether the base low was missing, was on the wrong price basis, was today's single-bar low,
// or was simply not below the zone — and those four call for four different actions.
type RiskLevelDisposition string

const (
	// RiskLevelSelected — this level is the published invalidation, or the resistance the
	// target was clamped to.
	RiskLevelSelected RiskLevelDisposition = "SELECTED"

	// RiskLevelEligibleNotSelected — a legal candidate that lost the selection rule. NOT a
	// rejection: it is what lets a reader see that a deeper structural level existed and was
	// passed over deliberately.
	RiskLevelEligibleNotSelected RiskLevelDisposition = "ELIGIBLE_NOT_SELECTED"

	// RiskLevelUnavailable — no usable number: not AVAILABLE, absent, non-finite or not
	// positive. MISSING ≠ ZERO.
	RiskLevelUnavailable RiskLevelDisposition = "LEVEL_UNAVAILABLE"

	// RiskLevelBasisMismatch — the level is on a different price series from the zone it is
	// being compared with. Never converted; see PriceBasis.
	RiskLevelBasisMismatch RiskLevelDisposition = "PRICE_BASIS_MISMATCH"

	// RiskLevelNotBelowZoneLow — an invalidation candidate at or above the bottom of the
	// ideal entry zone.
	//
	// EXCLUDED, NEVER SHAVED. `if c >= zone.Low { c = zone.Low * 0.99 }` would publish a
	// thesis-failure price the rule never computed, one that sits INSIDE the band the plan
	// simultaneously calls a good entry — so the plan would be buying and giving up in the
	// same 1% of price.
	RiskLevelNotBelowZoneLow RiskLevelDisposition = "NOT_BELOW_ZONE_LOW"

	// RiskLevelNotAboveZoneHigh — a resistance candidate at or below the top of the ideal
	// entry zone. It is not a level the trade has to get through; it is one it is already
	// through, and using it as a first target would publish a target below the entry.
	RiskLevelNotAboveZoneHigh RiskLevelDisposition = "NOT_ABOVE_ZONE_HIGH"

	// RiskLevelNotStructural — a base low that is not a consolidation base low: today's
	// single-bar low, or a kind the caller did not state. See BaseLowKind.
	RiskLevelNotStructural RiskLevelDisposition = "NOT_A_STRUCTURAL_LEVEL"

	// RiskLevelWrongSemantic — a real level that is not a candidate for THIS thesis (the
	// MA60 under a breakout). Reported rather than dropped, so the candidate list stays the
	// width of the registry and a missing row can never be read as "the rule did not look".
	RiskLevelWrongSemantic RiskLevelDisposition = "NOT_A_CANDIDATE_FOR_SEMANTIC"

	// RiskLevelFallbackNotNeeded — the volatility fallback was legal and was not used,
	// because an observed structural level was available. THE TIER BOUNDARY, visible on the
	// row: a reader can see that the heuristic was computed and deliberately passed over.
	RiskLevelFallbackNotNeeded RiskLevelDisposition = "FALLBACK_NOT_NEEDED"

	// RiskLevelNotAPrice — a number was computed and it is not a price: the ATR fallback
	// landed at or below zero (a stock whose ATR exceeds the level the zone is centred on),
	// or pricerule refused to put it on the tick grid.
	//
	// Distinct from LEVEL_UNAVAILABLE on purpose: "there is no MA60" and "the fallback came
	// out at −0.4" are different facts, and the second one is the number a reader needs.
	RiskLevelNotAPrice RiskLevelDisposition = "NOT_A_PRICE"

	// RiskLevelNotScreened — the screen never ran: there was no zone to compare against.
	//
	// A STATE, not a verdict — the same distinction EntryStatus draws between
	// INSUFFICIENT_DATA and NO_VALID_ENTRY.
	RiskLevelNotScreened RiskLevelDisposition = "NOT_SCREENED"
)

// AllRiskLevelDispositions is the registry, kept honest from both ends by the stop and target
// tests and pinned as literals by golden_test.go.
var AllRiskLevelDispositions = []RiskLevelDisposition{
	RiskLevelSelected,
	RiskLevelEligibleNotSelected,
	RiskLevelUnavailable,
	RiskLevelBasisMismatch,
	RiskLevelNotBelowZoneLow,
	RiskLevelNotAboveZoneHigh,
	RiskLevelNotStructural,
	RiskLevelWrongSemantic,
	RiskLevelFallbackNotNeeded,
	RiskLevelNotAPrice,
	RiskLevelNotScreened,
}

// KnownRiskLevelDisposition reports whether a disposition is in the registry.
func KnownRiskLevelDisposition(d RiskLevelDisposition) bool {
	for _, k := range AllRiskLevelDispositions {
		if k == d {
			return true
		}
	}
	return false
}

// Eligible reports whether a screened candidate is one the rule may use.
func (d RiskLevelDisposition) Eligible() bool {
	return d == RiskLevelSelected || d == RiskLevelEligibleNotSelected
}

// StopCandidate is one invalidation candidate, its value, and what happened to it.
type StopCandidate struct {
	Source StopSource `json:"source"`
	// Status is this candidate's own availability, downgraded to UNAVAILABLE when the caller
	// labelled it AVAILABLE and it is not a price. The label is not trusted.
	Status Availability `json:"status"`
	// Value is the observed level, or — for the fallback — the computed one. COPIED out of
	// the snapshot; nil unless Status is AVAILABLE.
	Value *float64 `json:"value,omitempty"`
	// PriceBasis is the basis the candidate is measured on, already resolved through
	// PriceObservation.BasisOr, so an empty basis here means the snapshot did not declare
	// one either — never "RAW".
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
	// Disposition is what happened to this candidate. Never empty after screening.
	Disposition RiskLevelDisposition `json:"disposition"`
}

// value reports the candidate's number, if it has one.
func (c StopCandidate) value() (float64, bool) {
	if c.Status.OK() && c.Value != nil && finitePositive(*c.Value) {
		return *c.Value, true
	}
	return 0, false
}

// InvalidationCandidatesFor collects one row per StopSource for one entry semantic and one
// published zone.
//
// ALWAYS the same width, in AllStopSources order, whatever the semantic and whatever is
// missing — the argument CandidateLevelsFor makes: a variable-width list is how a reader loses
// the ability to tell "the rule considered the base low and rejected it" from "this version of
// the rule had never heard of the base low".
//
// It decides only MEMBERSHIP and the two facts that are properties of the EVIDENCE rather than
// of the zone: a level that is not a candidate for this thesis, and a base low that is not a
// structural one. Everything else is ScreenInvalidationCandidates'.
//
// The fallback row is COMPUTED here, from the zone and the ATR, because it has no observation
// to be collected from. Its arithmetic needs the zone, so with no zone it is UNAVAILABLE — and
// so is every other row's screening, one step later.
func InvalidationCandidatesFor(in Snapshot, semantic EntrySemantic, zone *PriceZone) []StopCandidate {
	out := make([]StopCandidate, 0, len(AllStopSources))
	for _, src := range AllStopSources {
		c := StopCandidate{Source: src}

		switch src {
		case StopSourceZoneATRBuffer:
			c = bufferCandidate(in, zone)
		default:
			obs := in.stopObservationFor(src)
			c.Status = obs.Status
			c.PriceBasis = obs.BasisOr(in.PriceBasis)
			if obs.Usable() {
				// COPIED, never aliased.
				v := *obs.Value
				c.Status = Available
				c.Value = &v
			} else if c.Status == Available || c.Status == "" {
				// Labelled available and not a price, or never labelled. The label is not
				// trusted and the value is dropped rather than published.
				c.Status = Unavailable
			}
		}

		// MEMBERSHIP FIRST: a level that is not a candidate for this thesis is reported as
		// that and not screened further.
		if !src.SupportsSemantic(semantic) {
			c.Disposition = RiskLevelWrongSemantic
		} else if src == StopSourceBaseLow && c.Status.OK() && !in.BaseLowKind.Structural() {
			// A PRESENT base low of the wrong KIND. Checked before the geometry screen
			// because it is the more fundamental fact: a single-bar low that happens to sit
			// below the zone is still not a level, and reporting NOT_BELOW_ZONE_LOW for one
			// that sits above would suggest that a lower reading would have qualified.
			c.Disposition = RiskLevelNotStructural
		}
		out = append(out, c)
	}
	return out
}

// bufferCandidate computes the volatility fallback row: zone.Low − k × ATR(14).
//
// It re-checks the ATR rather than assuming "a zone exists therefore the ATR does", which is a
// property of ComputeIdealEntryZone and not of this function. The basis is the ATR's OWN, so a
// mismatch with the zone is caught by the screen exactly as a level's would be, rather than
// being silently inherited.
//
// A raw fallback that is not a price — reachable: a stock whose ATR exceeds the level its zone
// is centred on — is recorded as NOT_A_PRICE with no value published. It is not repaired to a
// tick, to zero, or to a fraction of the zone: the whole content of the number is the distance,
// and a repaired one keeps the name while losing the meaning.
func bufferCandidate(in Snapshot, zone *PriceZone) StopCandidate {
	c := StopCandidate{Source: StopSourceZoneATRBuffer, Status: Unavailable,
		PriceBasis: in.ATR.PriceBasis}
	if zone == nil {
		return c
	}
	if !(in.ATR.Status.OK() && in.ATR.Value != nil && finitePositive(*in.ATR.Value)) {
		// NO SUBSTITUTE MULTIPLIER. Without an ATR there is no fallback — and, crucially,
		// the STRUCTURAL candidates are untouched by this: an absent ATR must not be allowed
		// to mean "no invalidation", only "no fallback". TestAnAbsentATRLeavesTheStructural
		// CandidatesAlone drives exactly that through the exported function.
		return c
	}
	if in.ATR.Period != atrPeriodForZoneWidth {
		// The same period gate the width rule applies, for the same reason: 0.5 × ATR(14)
		// and 0.5 × ATR(5) are different distances, and an unnamed period cannot be
		// reproduced from the archive.
		return c
	}
	raw := zone.Low - invalidationATRMultiple**in.ATR.Value
	if !finitePositive(raw) {
		c.Disposition = RiskLevelNotAPrice
		return c
	}
	c.Status = Available
	c.Value = copyFloat(raw)
	return c
}

// stopObservationFor maps a stop source to the snapshot field it reads.
//
// The ONE place the mapping lives, for the reason Snapshot.observationFor is: a second copy is
// a second chance to read the MA60 into the base low's row, and both are plausible prices so no
// assertion on the numbers would catch it. The fallback has no observation and is handled by
// bufferCandidate; a source this package does not know reads nothing.
func (s Snapshot) stopObservationFor(src StopSource) PriceObservation {
	switch src {
	case StopSourceMA60:
		return s.MA60
	case StopSourceBaseLow:
		return s.BaseLow
	}
	return PriceObservation{}
}

// ScreenInvalidationCandidates decides which candidates may be published as an invalidation.
//
// The conditions are ALL of: available, finite, strictly positive, on the SAME price basis as
// the zone, and STRICTLY BELOW zone.Low. Every one of them is a separate disposition code, so a
// rejection says which condition failed.
//
// PURE: it returns a new slice and does not write through the input's pointers.
//
// A candidate that survives is marked ELIGIBLE_NOT_SELECTED, never SELECTED — selection is
// SelectInvalidation's job, and a screen that also selected would make the tier rule untestable
// without a whole snapshot.
//
// # Strictly below, and why nothing is repaired up or down
//
// zone.Low is the cheapest price this plan calls a good entry. A "the idea is wrong" level at or
// above it would have the plan buying and abandoning in the same tick of price, which is not a
// conservative reading of anything — it is two contradictory instructions. The candidate is
// therefore EXCLUDED, and the shaved variants (zone.Low − one tick, zone.Low × 0.99) are
// refused: both publish a number the rule never computed, and both survive every downstream
// check because they are finite, positive, below the entry and on the grid.
//
// # When the screen cannot run
//
// It needs a zone to compare against. Without one every candidate is NOT_SCREENED — not
// rejected — because the conditions were never evaluated. ZoneTrace.Rejection already says why
// there is no zone, and reporting it a second time here as a stop failure would send a reader
// looking at the MA60.
func ScreenInvalidationCandidates(cands []StopCandidate, zone *PriceZone) []StopCandidate {
	out := make([]StopCandidate, len(cands))
	copy(out, cands)

	screenable := zone != nil && zone.PriceBasis.Supported() && finitePositive(zone.Low)

	for n := range out {
		c := &out[n]
		if c.Disposition != "" {
			// Already decided by membership or by the base-low KIND. Those are properties of
			// the evidence rather than of the zone, and they are the more specific true
			// statement, so the geometry screen does not overwrite them.
			continue
		}
		if !screenable {
			c.Disposition = RiskLevelNotScreened
			continue
		}
		v, ok := c.value()
		if !ok {
			c.Disposition = RiskLevelUnavailable
			continue
		}
		if c.PriceBasis != zone.PriceBasis {
			// NEVER CONVERTED. See PriceBasis: converting needs the fetcher's adjustment
			// factors, and a plausible conversion here would be a fabricated price no
			// downstream check could distinguish from a measured one.
			c.Disposition = RiskLevelBasisMismatch
			continue
		}
		if v >= zone.Low {
			c.Disposition = RiskLevelNotBelowZoneLow
			continue
		}
		c.Disposition = RiskLevelEligibleNotSelected
	}
	return out
}

// SelectInvalidation picks the candidate the invalidation is published from, and marks it
// SELECTED.
//
// It returns a NEW slice — the screened rows with at most two dispositions changed — and the
// index of the winner, so a caller can report the whole list including the losers. ok == false
// means no candidate was eligible.
//
// # TIER 1: the HIGHEST eligible STRUCTURAL level, i.e. the NEAREST one below the zone
//
// The rule is max(), and the derivation is NOT the one SelectCenter uses for the entry. Those
// two answer different questions and only happen to agree on the direction:
//
//	SelectCenter    "which support is price most likely to be filled at"  → the nearest one
//	SelectInvalidation  "which price means the idea was wrong"            → see below
//
// Every structural candidate is a SUFFICIENT falsifier on its own. Losing the base low says the
// supply/demand balance the setup rests on has changed; losing the MA60 says the trend context
// the entry assumed is gone. So the thesis holds only while price is above ALL of them:
//
//	thesis_holds(p)  ⟹  p > c₁ ∧ p > c₂ ∧ … ∧ p > cₙ  ⟹  p > max(cᵢ)
//
// and the BOUNDARY of that conjunction is max(cᵢ). min() would assert the opposite — that the
// thesis survives until the DEEPEST candidate is broken — which is false by construction,
// because the shallower one was already breached and was already sufficient. Its two costs are
// both bad and only one of them is visible: the plan holds a thesis already known to be wrong,
// and the risk denominator (entry − invalidation) inflates, which is a LARGER risk carried at a
// FLATTERING risk/reward number.
//
// A TIE between two structural candidates at the same price resolves to the earlier source in
// AllStopSources. It is a real possibility — a base low sitting exactly on a round MA60 — and
// the tie-break is stated so the answer is deterministic rather than dependent on iteration
// order.
//
// # TIER 2: the volatility fallback, and ONLY when no structural level qualified
//
// The tiers are NOT merged into one max(), and this is the decision in this file most worth
// reading twice, because merging them is both shorter and wrong.
//
// The fallback sits just below zone.Low by construction, so a merged max() would select it in
// almost every plan that has a structural level at all — and the structural rows would become
// decoration. Concretely: zone 92→96 on an ATR of 4 gives a fallback of 90, and a base low at
// 89 would then never be used. But 89 is where supply dried up: at 89.5 price is still INSIDE
// the base and the setup is intact, so a merged max() would declare the thesis dead while the
// structure that defines it is untouched. That is a stop, not an invalidation.
//
// The general form: the fallback's claim ("the entry's own level failed by more than a day's
// range") is WEAKER than the structural claim ("the level buyers defended is gone"), and where
// a real structure exists below the zone it is the structure the thesis rests on. A heuristic
// buffer may fill a gap; it may not pre-empt evidence.
func SelectInvalidation(screened []StopCandidate) ([]StopCandidate, int, bool) {
	out := make([]StopCandidate, len(screened))
	copy(out, screened)

	best, fallback := -1, -1
	for n := range out {
		if !out[n].Disposition.Eligible() {
			continue
		}
		v, ok := out[n].value()
		if !ok {
			// An eligible row with no number is a screen bug, not an invalidation. Refused
			// rather than dereferenced: the alternative to this branch is a panic in a report.
			return out, -1, false
		}
		if !out[n].Source.Structural() {
			if fallback >= 0 {
				// Two fallbacks is not a tie, it is a contradiction: there is one zone and
				// one ATR, so there is one buffer.
				return out, -1, false
			}
			fallback = n
			continue
		}
		if best < 0 {
			best = n
			continue
		}
		bv, okBest := out[best].value()
		if !okBest {
			return out, -1, false
		}
		// STRICTLY greater, so an equal price leaves the earlier source in place — the
		// tie-break stated in the doc.
		if v > bv {
			best = n
		}
	}

	if best >= 0 {
		if fallback >= 0 {
			// Computed, legal, and passed over. Recorded as such so the tier decision is
			// visible on the row rather than only in this source file.
			out[fallback].Disposition = RiskLevelFallbackNotNeeded
		}
		out[best].Disposition = RiskLevelSelected
		return out, best, true
	}
	if fallback >= 0 {
		out[fallback].Disposition = RiskLevelSelected
		return out, fallback, true
	}
	return out, -1, false
}

// StopResult is the whole invalidation step: the level if there is one, and the record of how it
// was reached if there is not.
//
// Invalidation == nil ⟺ (Trace.Rejection != "" OR there was no zone). The second disjunct is
// the one case with no code of its own; see StopTrace.Rejection.
type StopResult struct {
	// Invalidation is the published thesis-failure level, or nil. Tick-aligned, positive,
	// strictly below the bottom of the ideal entry zone.
	Invalidation *Invalidation `json:"invalidation,omitempty"`
	// Trace is the work record: every candidate, the fallback's terms, the tick direction,
	// and the rejection if there was one.
	Trace StopTrace `json:"trace"`
}

// ComputeInvalidation computes the thesis-invalidation level for one snapshot and one zone.
//
// PURE and TOTAL: defined for every input including the zero Snapshot and a nil zone, no clock,
// no I/O, and it never retains a pointer from its arguments.
//
// # The zone is an ARGUMENT, not something this function decides
//
// It must be the zone ComputeIdealEntryZone published — ComputePlan passes exactly that. The
// invalidation is defined RELATIVE TO THE PUBLISHED ENTRY (strictly below zone.Low), so
// recomputing the zone here would be two chances for the entry the plan shows and the entry the
// stop was measured against to be edited apart.
//
// # Order of the gates, which is the order of the sentences a reader needs
//
//  1. Is there a published entry at all? (no zone → nothing to be below)
//  2. Which candidates exist, and which are legal? (collect → screen)
//  3. Which one is the answer? (tier 1 structural, then tier 2 fallback)
//  4. Is the answer a legal QUOTE? (tick alignment, DOWN)
//
// Nothing later substitutes for anything earlier, and step 4 never invents a price.
func ComputeInvalidation(in Snapshot, zone *PriceZone) StopResult {
	semantic := in.EntrySemantic.Semantic

	tr := StopTrace{
		Semantic:    semantic,
		BaseLowKind: in.BaseLowKind,
	}
	if in.ATR.Value != nil && finite(*in.ATR.Value) {
		// Recorded even when it is about to be refused: "the ATR was 0" and "there was no
		// ATR" are different rows. Non-finite is NOT recorded — nothing this package
		// publishes may carry a NaN, including a diagnostic.
		tr.ATR = copyFloat(*in.ATR.Value)
	}

	// 1. THE PUBLISHED ENTRY. The candidate list is recorded for this outcome too: a plan
	// that says "no invalidation" and shows no candidates is indistinguishable from one
	// produced by a rule that never looked at a level.
	cands := InvalidationCandidatesFor(in, semantic, zone)
	if zone == nil {
		// NO CODE OF OUR OWN, exactly as ComputeMaxChase reports nothing when there is no
		// zone to chase into: ZoneTrace.Rejection already says why there is no entry, and a
		// second code here would read as a second, independent failure.
		tr.Candidates = ScreenInvalidationCandidates(cands, zone)
		return StopResult{Trace: tr}
	}
	tr.PriceBasis = zone.PriceBasis
	tr.ZoneLow = copyFloat(zone.Low)
	tr.ATRMultiple = copyFloat(invalidationATRMultiple)

	// 2. THE CANDIDATES.
	screened := ScreenInvalidationCandidates(cands, zone)
	screened, best, ok := SelectInvalidation(screened)
	tr.Candidates = screened
	if !ok {
		// WHICH KIND of "no" this is has to be read off the candidate rows, because the three
		// kinds are indistinguishable at this call site: SelectInvalidation returns the same
		// false for "nothing was eligible" and for its own two self-contradiction guards. See
		// classifyStopUnavailability — and see ReasonNoValidInvalidation for why a single code
		// here was the gap EP-5 could not have closed by guessing.
		tr.Rejection = classifyStopUnavailability(screened)
		return StopResult{Trace: tr}
	}
	raw, ok := screened[best].value()
	if !ok {
		// SelectInvalidation never returns an index without a number. Refused rather than
		// dereferenced.
		//
		// AN INTERNAL CONTRADICTION, and it is reported as one: the screen said this row was
		// usable and the row has no value. Publishing it as NO_VALID_INVALIDATION would tell a
		// reader that the MARKET offers no boundary, when what happened is that two functions
		// in this file disagreed.
		tr.Rejection = ReasonInvalidationSelectionInconsistent
		return StopResult{Trace: tr}
	}
	tr.SelectedSource = screened[best].Source
	tr.RawPrice = copyFloat(raw)

	// 3. TICK ALIGNMENT, DOWN.
	//
	// DOWN is the only defensible direction and the argument is about ORDER SEMANTICS, not
	// about symmetry. Two things follow from rounding this level UP, and both are worse than
	// the one tick of extra loss that rounding DOWN can cost:
	//
	//   - A thesis-break level moved CLOSER TO THE ENTRY declares the idea dead at a price the
	//     rule never said was a failure. Downstream that is an exit from a setup that is still
	//     intact — a behavioural error with no bound on it, since the trade is simply gone.
	//   - risk = zone.High − invalidation SHRINKS, so the published risk/reward RISES. That is
	//     the systematically optimistic direction, on the one number a reader uses to decide
	//     whether the trade is worth taking, and it is exactly the bias ComputeTargets refuses
	//     when it measures risk from zone.High rather than from the midpoint.
	//
	// So every EP-4 price rounds in the direction that makes the plan look WORSE, never better:
	// the invalidation further away, the targets nearer. One tick of extra loss is bounded and
	// visible; an inflated R:R is neither.
	tr.TickDirection = tickDirection(pricerule.RoundDown)
	price, ok := pricerule.RoundToTick(raw, pricerule.RoundDown)
	if !ok {
		// Unreachable through this function: the screen already required a finite, positive
		// candidate strictly below zone.Low, and zone.Low is itself a value pricerule returned
		// from RoundToTick, so the input is inside the alignable domain. The branch is written
		// rather than assumed away because that argument rests on ANOTHER package's domain
		// bound, and because `v, _ := pricerule.RoundToTick(...)` is precisely the shape the
		// EP-0 review warned about. The candidate is marked and the level is refused; no
		// nearest tick is invented.
		screened[best].Disposition = RiskLevelNotAPrice
		tr.Candidates = screened
		// NO_VALID_INVALIDATION, in its narrowed EP-5 sense: the selection ran to completion on
		// a candidate that was present, and the number it produced is not a price any exchange
		// quotes. That is a finding about the LEVEL, not a missing input — nothing here was
		// absent — so it is a verdict and not a state.
		tr.Rejection = ReasonNoValidInvalidation
		return StopResult{Trace: tr}
	}

	// 4. THE GEOMETRY, AFTER ALIGNMENT — because alignment is arithmetic, and arithmetic is
	// where an invariant stops holding. Rounding DOWN cannot lift a price, so this cannot fire
	// for a screened candidate; it is checked because the invariant belongs to the PUBLISHED
	// number, not to the one that was screened.
	if !finitePositive(price) || price >= zone.Low {
		screened[best].Disposition = RiskLevelNotBelowZoneLow
		tr.Candidates = screened
		// THE CANONICAL NO_VALID_INVALIDATION: a full search finished and the winner is not a
		// legal boundary below the published entry. Nothing was missing, so EP-5 reads this as
		// NO_VALID_ENTRY — the plan knows where it might buy and cannot say where it would be
		// proved wrong.
		tr.Rejection = ReasonNoValidInvalidation
		return StopResult{Trace: tr}
	}
	tr.Price = copyFloat(price)

	return StopResult{
		Invalidation: &Invalidation{
			Price:      price,
			PriceBasis: zone.PriceBasis,
			Basis:      invalidationBasis(screened[best].Source),
		},
		Trace: tr,
	}
}

// classifyStopUnavailability says WHY SelectInvalidation came back with nothing, by reading the
// screened rows rather than by asking the selector — which cannot tell the three apart.
//
// # The split exists because EP-5 has to make a different kind of statement for each
//
// One reason code covered all of this until EP-5, and a decision layer reading it could only
// choose between calling every case INSUFFICIENT_DATA (so "every level is above the entry band"
// would read as "we could not look") or calling every case NO_VALID_ENTRY (so "the MA60 was
// absent" would read as a verdict about the stock). Both are wrong for half the inputs.
//
// The criterion is the one this package applies everywhere, and it is about whether A COMPARISON
// ACTUALLY RAN, not about which field was empty:
//
//	a row reached the geometry screen and failed it   →  a verdict   NO_VALID_INVALIDATION
//	no row was ever comparable with the zone          →  a state     ..._EVIDENCE_UNAVAILABLE
//	a row WAS usable and selection produced nothing   →  a bug       ..._SELECTION_INCONSISTENT
//
// ORDERED, first match wins, in the shape internal/market/analyzer.DecideRegime uses and for the
// same reason: the order is part of the specification. The inconsistency check is FIRST because
// it is the only one whose truth does not depend on the others — if a usable row survived the
// screen, no statement about the market explains the empty result, whatever the other rows say.
//
// # Where each disposition lands, and why NOT_BELOW_ZONE_LOW is the verdict case
//
//	NOT_BELOW_ZONE_LOW  the level was present, positive, on the zone's own basis, and the
//	                    comparison with zone.Low was PERFORMED. Its answer is "this is not a
//	                    boundary below the entry" — a fact about the chart.
//	NOT_A_PRICE         a number was COMPUTED (the volatility fallback) and it is at or below
//	                    zero, which is a fact about this stock's volatility against its own
//	                    level. Also a completed computation, so also a verdict.
//	LEVEL_UNAVAILABLE   there was no number. Nothing ran.
//	PRICE_BASIS_MISMATCH  a number existed and could not be compared with the zone at all: this
//	                    repo mixes RAW and ADJUSTED, and refusing to convert (see PriceBasis) is
//	                    a statement about the DATA, not about the stock.
//	NOT_A_STRUCTURAL_LEVEL  the base low in hand is today's single bar, or its kind was never
//	                    stated. The structural level is NOT IN HAND — an evidence gap — and
//	                    reporting it as "no legal boundary exists" would blame the market for a
//	                    field the scanner did not fill.
//	NOT_A_CANDIDATE_FOR_SEMANTIC / NOT_SCREENED  the screen was never applied to this row.
func classifyStopUnavailability(screened []StopCandidate) Reason {
	// 1. A usable row survived the screen and selection still produced nothing. Whatever the
	// other rows say, no fact about the market explains that.
	for _, c := range screened {
		if c.Disposition.Eligible() {
			return ReasonInvalidationSelectionInconsistent
		}
	}
	// 2. At least one candidate was compared and the comparison rejected it. The search ran.
	for _, c := range screened {
		if c.Disposition == RiskLevelNotBelowZoneLow || c.Disposition == RiskLevelNotAPrice {
			return ReasonNoValidInvalidation
		}
	}
	// 3. Nothing was ever comparable. An evidence gap, and NOT a verdict.
	//
	// This is also the answer for an EMPTY list, which no caller in this package produces —
	// InvalidationCandidatesFor is always the width of AllStopSources. Returning the evidence
	// code for it is the conservative direction: a state can be corrected by better data, a
	// verdict published in its place cannot be undone by a reader.
	return ReasonInvalidationEvidenceUnavailable
}

// invalidationBasis names what makes this the level, as stable codes.
//
// The fallback carries a SECOND code saying it is a heuristic. That is not decoration: the two
// rows are read the same way otherwise, and "the thesis fails at 90 because buyers defended 90"
// and "the thesis fails at 90 because 90 is a bit more than a day's range below the entry" are
// different claims with different strengths. A reader who cannot tell them apart cannot weigh
// the plan.
func invalidationBasis(src StopSource) []string {
	if src == StopSourceZoneATRBuffer {
		return []string{string(src), basisHeuristicBuffer}
	}
	return []string{string(src)}
}

// basisHeuristicBuffer marks an invalidation that is DERIVED from the entry band and the ATR
// rather than observed as a level. See invalidationATRMultiple: the multiple is a heuristic and
// is not claimed to be optimal.
const basisHeuristicBuffer = "HEURISTIC_VOLATILITY_BUFFER"
