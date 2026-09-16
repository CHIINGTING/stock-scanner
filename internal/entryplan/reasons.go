package entryplan

// Reason is a stable, machine-readable code for why a Plan says what it says.
//
// SCREAMING_SNAKE and persisted verbatim, with the natural-language wording living at the
// presentation boundary — the same split valuation.SuitabilityReason makes, and for the same
// reason: a stored plan stays interpretable after a UI change, and a UI change cannot alter
// what was concluded.
type Reason string

const (
	// ReasonSnapshotMalformed — the input is not a stock. No symbol, or an AsOf that is not
	// a date. A CALLER BUG, reported rather than repaired: guessing a symbol or a session
	// would attach a plan to something nobody asked about.
	ReasonSnapshotMalformed Reason = "SNAPSHOT_MALFORMED"

	// ReasonCurrentPriceUnavailable — there is no usable current price: unavailable, absent,
	// non-finite, or not positive. Every output of this package is a price relative to it,
	// so nothing downstream can be computed without it.
	ReasonCurrentPriceUnavailable Reason = "CURRENT_PRICE_UNAVAILABLE"

	// ReasonPriceBasisUnavailable — the snapshot's PriceBasis is missing or is not a basis
	// this package understands.
	//
	// Absent and unsupported share ONE code deliberately. The distinction is real, but it is
	// a distinction between two kinds of caller bug with identical consequence — no price
	// evidence in this snapshot can be combined with any other — and a second code would be
	// a second thing for a consumer to switch on for no behavioural difference. If a future
	// item ever treats them differently, it splits the code then, with the rule that needs it.
	ReasonPriceBasisUnavailable Reason = "PRICE_BASIS_UNAVAILABLE"

	// ReasonMarketRegimeUnavailable — no regime call is in hand: either it was never
	// computed (Status) or it was computed and came back UNKNOWN (model.RegimeUnknown).
	//
	// Note what this reason does NOT say. It does not say the market is bad. An entry plan
	// made without knowing the regime is a plan made without knowing whether the same
	// pullback is a gift or a knife.
	ReasonMarketRegimeUnavailable Reason = "MARKET_REGIME_UNAVAILABLE"

	// ReasonEntrySemanticUnavailable — no entry semantic is in hand: either none was handed
	// to us (Status) or the upstream layer looked and could not say which kind of entry this
	// is (EntrySemanticUnknown).
	//
	// It is REQUIRED census evidence, not a refusal. entryplan translates an existing entry
	// semantic into a price (doc.go); without one there is nothing to translate, and this
	// package is forbidden from choosing between a pullback and a breakout on its own — that
	// would be the second opinion the architecture test exists to prevent.
	//
	// Absent and UNKNOWN share ONE code, for the reason ReasonPriceBasisUnavailable folds its
	// two cases: the distinction is real and it is preserved on the evidence ROW (UNAVAILABLE
	// vs INSUFFICIENT_DATA), but no rule here treats them differently, and a second code
	// would be a second branch for every consumer with no behavioural difference.
	ReasonEntrySemanticUnavailable Reason = "ENTRY_SEMANTIC_UNAVAILABLE"

	// THE CODE THAT USED TO SIT HERE — ENTRY_EVALUATION_NOT_IMPLEMENTED — IS GONE, AND ITS
	// OWN DOC SAID SO
	//
	// It read, verbatim: "STILL TEMPORARY BY CONSTRUCTION. The work item that publishes an
	// EntryStatus verdict deletes this constant, and the bidirectional registry test then
	// FAILS until it is removed from AllReasons too." EP-5 is that work item, so it is
	// deleted rather than retained-and-unused: under EP5-v1 the sentence it asserted — "no
	// rule turns these prices into a STATUS" — is FALSE for every input, and a code that
	// makes a false claim is worse than a missing one because a consumer branches on it.
	//
	// WHAT AN ARCHIVED ROW CARRYING IT STILL MEANS: exactly what it meant when it was
	// written. RuleVersion is how a reader tells the two apart — an EP4-v1 row's
	// INSUFFICIENT_DATA is "no decision rule existed", an EP5-v1 row's is "the rule ran and
	// the evidence did not support a call". Decoders of old rows must therefore NOT map this
	// string onto any code below; KnownReason answers false for it deliberately.

	// ── EP-3b: the zone step ──────────────────────────────────────────────────────────
	//
	// Every one of these is a reason there is NO IDEAL ENTRY ZONE — except the overlap flag,
	// which travels WITH a zone. None of them is a status: "there is no legal zone today" is
	// not NO_VALID_ENTRY, which is a verdict EP-5 publishes after weighing the zone, the
	// invalidation and the reward.

	// ReasonZoneSemanticNotPermitted — the regime's policy FORBIDS this kind of entry. A
	// DECISION, on sufficient evidence: BEAR disallows every shape, and BULL_PULLBACK,
	// SIDEWAYS and DISTRIBUTION disallow a breakout.
	//
	// Distinct from ReasonZonePolicyUnresolved below, and the distinction is the whole point
	// of EP-2's Allowance type: one of these is "we looked, and no", the other is "we could
	// not look". A consumer that merged them would report an unreadable market as a bearish
	// one.
	ReasonZoneSemanticNotPermitted Reason = "ZONE_SEMANTIC_NOT_PERMITTED"

	// ReasonZonePolicyUnresolved — NO POLICY DECISION WAS MADE about this kind of entry:
	// the regime is model.RegimeUnknown, or the regime string is not a regime, or there is
	// no entry semantic to judge. Never a prohibition. See PolicyUnresolved.
	ReasonZonePolicyUnresolved Reason = "ZONE_POLICY_UNRESOLVED"

	// ReasonPullbackLevelsUnavailable — no support level was usable at all: the MA20, the
	// MA60 and the base low were absent, non-finite or not positive.
	ReasonPullbackLevelsUnavailable Reason = "PULLBACK_LEVELS_UNAVAILABLE"

	// ReasonPullbackLevelsBasisMismatch — a support level exists and is on a DIFFERENT price
	// series from the quote (see PriceBasis). It is refused rather than converted: the
	// adjustment factors belong to the fetcher, and a conversion invented here would be a
	// fabricated price indistinguishable downstream from a measured one.
	ReasonPullbackLevelsBasisMismatch Reason = "PULLBACK_LEVELS_PRICE_BASIS_MISMATCH"

	// ReasonPullbackLevelNotBelowCurrentPrice — every usable support sits AT OR ABOVE the
	// last price, so there is no retracement level to buy.
	//
	// A statement about the levels, NOT about the trade. Whether that means "buy it here" or
	// "too extended" needs a status, and this layer does not compute one.
	ReasonPullbackLevelNotBelowCurrentPrice Reason = "PULLBACK_LEVEL_NOT_BELOW_CURRENT_PRICE"

	// ReasonBreakoutLevelUnavailable — there is no usable pivot high, so there is nothing to
	// break out OF.
	//
	// The zone is refused rather than centred on something else. The current price, the MA20
	// and the last close are all available and all wrong: each would produce a "breakout
	// entry" at a price nothing broke out of, and the chase ceiling would then be measured
	// from a level that does not exist.
	ReasonBreakoutLevelUnavailable Reason = "BREAKOUT_LEVEL_UNAVAILABLE"

	// ReasonBreakoutLevelBasisMismatch — the pivot high is on a different price series from
	// the quote. Refused, never converted; see ReasonPullbackLevelsBasisMismatch.
	ReasonBreakoutLevelBasisMismatch Reason = "BREAKOUT_LEVEL_PRICE_BASIS_MISMATCH"

	// ReasonBreakoutLevelNotAQuote — the pivot high is not a price the exchange could have
	// quoted, so aligning the zone's low onto the tick grid would put it BELOW the level the
	// breakout is defined by.
	//
	// It arises only for a level that is not a whole number of cents, which no real quote is:
	// pricerule's RoundUp can land within 0.0001 元 below such an input. Refused rather than
	// nudged up one tick, because inventing the next tick publishes a breakout price nobody
	// broke out of.
	ReasonBreakoutLevelNotAQuote Reason = "BREAKOUT_LEVEL_NOT_A_QUOTE"

	// ReasonZoneATRUnavailable — the zone's WIDTH needs ATR(14) and there is none: not
	// AVAILABLE, absent, non-finite, or not positive.
	//
	// # This is a MISSING DEPENDENCY, not the trigger of a second rule
	//
	// There is deliberately no fallback width. Not the tick floor alone, not a percentage of
	// the current price, not the 2.5% the legacy priceTargets code used. The two candidate
	// widths differ by an order of magnitude — at 1245 元 the tick floor is 10 元 while half a
	// realistic ATR is 30–60 元 — and publishing either through one field under one name
	// leaves a reader unable to tell which rule produced the number they are looking at. The
	// narrow one reads as a tightly-defined, high-conviction entry, which is the opposite of
	// what it would mean.
	ReasonZoneATRUnavailable Reason = "ZONE_ATR_UNAVAILABLE"

	// ReasonZoneATRBasisMismatch — the ATR was measured on a different price series from the
	// zone's own basis, so subtracting it from the level would be arithmetic across two
	// price universes. This repo really does mix them: internal/indicator computes ATR on the
	// raw close while other evidence may arrive through fetcher.PriceForCalc.
	ReasonZoneATRBasisMismatch Reason = "ZONE_ATR_PRICE_BASIS_MISMATCH"

	// ReasonZoneATRPeriodMismatch — the ATR in hand is not the period the width rule is
	// defined on. 0.5 × ATR(14) and 0.5 × ATR(5) are different widths, and ATREvidence
	// carries Period precisely so the consumer can refuse instead of silently using whatever
	// it was handed. Period 0 ("the caller did not say") is refused for the same reason: a
	// width computed from an unnamed period cannot be reproduced from the archive.
	ReasonZoneATRPeriodMismatch Reason = "ZONE_ATR_PERIOD_MISMATCH"

	// ReasonZoneTickAlignmentUnavailable — pricerule refused to put one of the zone's
	// numbers on the tick grid: the price is outside the domain of things that are prices
	// (non-finite, non-positive, or above pricerule's 1e6 元 bound).
	//
	// No local rounding is substituted. math.Round(v*10)/10 produces 1245.0000000000002 and
	// a price of 1245.3 on a stock whose tick is 5.00 — a number the exchange rejects,
	// arrived at without an error anywhere.
	ReasonZoneTickAlignmentUnavailable Reason = "ZONE_TICK_ALIGNMENT_UNAVAILABLE"

	// ReasonZoneLowNotAPrice — the zone's raw lower bound came out at or below zero, which
	// happens when the stock's ATR is more than twice the level the zone is centred on.
	//
	// Refused, not repaired. `if low <= 0 { low = tickSize }` would publish a bound the rule
	// never computed and silently replace the zone's width — the only property that made the
	// number meaningful — with whatever happened to be left above zero.
	ReasonZoneLowNotAPrice Reason = "ZONE_LOW_NOT_A_PRICE"

	// ReasonPullbackZoneOverlapsCurrentPrice — a FLAG THAT TRAVELS WITH A PUBLISHED ZONE, not
	// a refusal: the pullback zone's high sits at or above the last price.
	//
	// The zone is NOT clamped to the current price. Clamping would keep the low at
	// centre − halfWidth while cutting the high, leaving a band that is no longer centred on
	// the support — and "the zone is the support ± half an ATR", the only reason the number
	// is defensible, would become false while still being printed.
	//
	// What the overlap MEANS — the market is already inside the zone (BUY_NOW) or has run
	// through it (TOO_EXTENDED) — is a status, and EP-3b computes no status.
	ReasonPullbackZoneOverlapsCurrentPrice Reason = "PULLBACK_ZONE_OVERLAPS_CURRENT_PRICE"

	// ReasonRecentPriceAdjustment — a corporate-action adjustment landed recently enough that
	// the ATR-derived width still carries part of it. A CAVEAT THAT TRAVELS WITH THE PRICES.
	//
	// NOT A GATE. The zone and the chase limit are computed exactly as they would be without
	// it, and the fact is reported instead — because the contamination is a CONTINUOUS decay,
	// ((N-1)/N)^age for a Wilder ATR, not a state that ends. 63 bars is where the residue
	// falls below 1% for N=14; it is a reporting threshold, and nothing "becomes clean" on
	// day 63. Suppressing the plan instead would delete a usable entry over a residue of a
	// few tenths of a percent.
	ReasonRecentPriceAdjustment Reason = "RECENT_PRICE_ADJUSTMENT"

	// ── EP-3b: the chase step ─────────────────────────────────────────────────────────

	// ReasonChaseNotAllowed — the regime FORBIDS chasing. A DECISION: BEAR, SIDEWAYS and
	// DISTRIBUTION all say no, on evidence.
	//
	// It must never be merged with ReasonChasePolicyUnresolved. See ChasePolicy: EP-2 split
	// the decision from the number specifically so that these two nils could stay apart.
	ReasonChaseNotAllowed Reason = "CHASE_NOT_ALLOWED"

	// ReasonChasePolicyUnresolved — NO CHASE DECISION WAS MADE: the regime is UNKNOWN, is not
	// a regime at all, or resolved no tolerance. Not a prohibition, and not a bearish reading.
	ReasonChasePolicyUnresolved Reason = "CHASE_POLICY_UNRESOLVED"

	// ReasonPreviousCloseUnavailable — chasing was permitted and there is no previous close,
	// so the day's legal ceiling cannot be computed.
	//
	// THE CURRENT PRICE IS NOT A SUBSTITUTE. A daily price limit is a band around the
	// PREVIOUS session's reference price; computing it from today's price gives a ceiling
	// that moves with the very thing it is supposed to bound. The zone survives — the two
	// availabilities are independent.
	ReasonPreviousCloseUnavailable Reason = "PREVIOUS_CLOSE_UNAVAILABLE"

	// ReasonPreviousCloseBasisMismatch — the previous close is on a different price series
	// from the rest of the plan. Refused, never converted.
	ReasonPreviousCloseBasisMismatch Reason = "PREVIOUS_CLOSE_PRICE_BASIS_MISMATCH"

	// ReasonChaseLimitRequiresRawBasis — the plan's prices are on the ADJUSTED series and a
	// daily price limit is ±10% of the exchange's RAW reference price.
	//
	// A back-adjusted previous close is a synthetic number no band was ever computed from, so
	// ±10% of it is not a ceiling — it is a plausible number with no regulation behind it.
	// The ZONE survives (it is internally consistent and PriceZone.PriceBasis says which
	// series it is on); the ceiling does not, because its authority comes from a rule about
	// raw prices.
	ReasonChaseLimitRequiresRawBasis Reason = "CHASE_LIMIT_REQUIRES_RAW_BASIS"

	// ReasonChaseLimitRuleUnavailable — pricerule.ClassifyLimitRule could not establish that
	// ±10% even applies to this security, so this package knows of no ceiling.
	//
	// The honest answer for the "00" ETF block, and it is a gap in the REPO rather than in
	// the market: an ETF tracking foreign constituents has no daily limit at all while one
	// tracking domestic constituents has ±10%, both live under "00", and internal/
	// stockcatalog records no instrument class to tell them apart. Measured: 12 of the 1,995
	// codes in the repo's own cache classify UNKNOWN.
	//
	// The chase is refused rather than left unclamped, because an unclamped ceiling on a
	// domestic-constituent ETF authorises an order above the legal band — an order the
	// exchange rejects, which is exactly the "actually placeable" property this work item
	// exists to deliver.
	ReasonChaseLimitRuleUnavailable Reason = "CHASE_LIMIT_RULE_UNAVAILABLE"

	// ReasonChaseLimitPriceUnavailable — the limit regime applies and pricerule still could
	// not produce the band: the previous close is outside the domain of things that are
	// prices, or the chase arithmetic left it. No default ceiling; MISSING ≠ ZERO.
	ReasonChaseLimitPriceUnavailable Reason = "CHASE_LIMIT_PRICE_UNAVAILABLE"

	// ReasonChaseCeilingBelowZone — today's highest legal price sits BELOW the top of the
	// ideal entry zone, so there is no chase headroom at all.
	//
	// Publishing the ceiling anyway would break InvariantMaxChaseCoversZone: the plan would
	// forbid paying what it simultaneously calls an ideal entry. Publishing the zone high
	// instead would breach the daily limit. So the CEILING is withdrawn and the zone is kept
	// — the level and the volatility have not changed, only today's band — and this code says
	// which of the two it was.
	ReasonChaseCeilingBelowZone Reason = "CHASE_CEILING_BELOW_ZONE"

	// ── EP-4: the invalidation step ───────────────────────────────────────────────────

	// ReasonNoValidInvalidation — a zone was published and NO candidate qualified as the
	// price at which the thesis stops being true.
	//
	// # There is no fabricated stop behind this code, and that is the whole point
	//
	// The alternatives were all available and all refused, because each produces a number
	// that is finite, positive, below the entry, on the tick grid — and not a level:
	//
	//	zone.Low * 0.99        an arbitrary 1% below the entry band
	//	zone.Low - one tick    the smallest lie the grid allows
	//	entry - 2 * ATR        the legacy priceTargets stop: a LOSS BUDGET, not a thesis
	//	entry * 0.93           the legacy fixed 7%
	//
	// A trade whose failure point cannot be stated has no risk/reward, so this code takes
	// the targets and the R:R with it (see ComputeTargets) — and that cascade is correct
	// rather than unfortunate: the numbers it removes are exactly the ones that would have
	// been computed from the invented stop.
	//
	// What each candidate actually failed is on its own row in StopTrace.Candidates: absent,
	// on the wrong price basis, not a structural level (see BaseLowKind), not below the
	// bottom of the zone, or — for the volatility fallback — not a price at all.
	//
	// # NARROWED BY EP-5, and the VALUE did not change
	//
	// Until EP-5 this ONE code covered all four ways ComputeInvalidation can decline, and the
	// decision layer could not tell them apart — so "we never had a level to compare" and "we
	// compared every level and none of them is a legal boundary" arrived as the same string.
	// They are different answers: the first is INSUFFICIENT_DATA (a state) and the second is
	// NO_VALID_ENTRY (a verdict — the system knows where it might buy and cannot say where it
	// would be proved wrong, which is not an executable trade plan).
	//
	// This code now means ONLY the second: A COMPARISON RAN TO COMPLETION AND ITS ANSWER IS
	// "NOT A LEGAL BOUNDARY". The other two are ReasonInvalidationEvidenceUnavailable and
	// ReasonInvalidationSelectionInconsistent. The persisted value is unchanged because the
	// narrowed meaning is the one the name already said, and RuleVersion records the split.
	ReasonNoValidInvalidation Reason = "NO_VALID_INVALIDATION"

	// ReasonInvalidationEvidenceUnavailable — NO invalidation candidate was ever COMPARABLE
	// with the zone: every row was absent, on a different price basis, not a structural level
	// (see BaseLowKind), or not a candidate for this thesis at all.
	//
	// UPSTREAM DATA MISSING, not a market fact. Nothing was measured against the zone, so
	// there is no finding to report — which is why EP-5 reads this as INSUFFICIENT_DATA and
	// NOT as NO_VALID_ENTRY. The criterion is the one this package applies everywhere: the
	// rule RAN TO COMPLETION on evidence that was present → a verdict; an input was missing
	// or unusable → a state.
	//
	// RESERVED, on the SAME argument as ReasonRiskNotEstablished and measured the same way.
	// ComputeInvalidation is EXPORTED and takes the ZONE AS AN ARGUMENT, and its doc states the
	// precondition: the zone "must be the zone ComputeIdealEntryZone published". Honour that
	// precondition and this code is unreachable, because a published zone implies a usable
	// ATR(14) on the zone's own basis (zoneHalfWidth demands exactly that), which makes the
	// volatility fallback row comparable — so SOME candidate always reaches the geometry screen
	// and the answer is a verdict (NO_VALID_INVALIDATION) rather than a gap. BREAK the
	// precondition — hand it a zone on another price series — and every row is incomparable and
	// this is the honest answer. The branch is written rather than assumed away because the
	// argument rests on ANOTHER FUNCTION IN THIS PACKAGE, and because EP-5 reads this code to
	// choose a STATUS: if it silently became NO_VALID_INVALIDATION, a missing input would be
	// published as a verdict about the stock.
	//
	// TestTheStopEvidenceGapIsRefusedRatherThanCalledAVerdict drives it directly.
	ReasonInvalidationEvidenceUnavailable Reason = "INVALIDATION_EVIDENCE_UNAVAILABLE"

	// ReasonInvalidationSelectionInconsistent — the screen produced a usable candidate and
	// the selection step still came back with nothing, or came back with an index that has no
	// number behind it.
	//
	// AN INTERNAL CONTRADICTION, NOT A MARKET STATE. It is the defensive half of
	// SelectInvalidation (an eligible row without a value; two volatility fallbacks for one
	// zone) and the guard immediately after it, and it must not be published as a verdict
	// about the stock: EP-5 reads it as INSUFFICIENT_DATA, because a rule that contradicted
	// itself did not complete a judgement about anything.
	//
	// RESERVED, and on the strongest of the three reservation arguments: UNREACHABLE BY
	// CONSTRUCTION, through the exported API and through a broken precondition alike. Both
	// producing branches live inside ComputeInvalidation, between calls it makes ITSELF:
	//
	//	classifyStopUnavailability's first clause  an ELIGIBLE row survived
	//	                                          ScreenInvalidationCandidates and
	//	                                          SelectInvalidation still answered "none"
	//	the guard after SelectInvalidation        the selected INDEX has no number behind it
	//
	// Neither is a statement about a caller's input: they are statements about two functions in
	// this file disagreeing. No argument, no zone and no snapshot can make them true, which is
	// exactly why they are written — the alternative to the second one is a nil dereference in
	// a report, and the alternative to the first is publishing NO_VALID_INVALIDATION, which
	// would tell a reader that THE MARKET offers no boundary when what happened is a bug.
	//
	// TestTheReservedSelectionInconsistencyCodeIsUnreachable is the ledger: it drives the two
	// exported steps with the contradictory shapes and records that the pipeline still cannot
	// be made to emit it.
	//
	// # The unreachability proof and its PREMISES, each with the guard that holds it
	//
	// The proof is not self-evident from SelectInvalidation alone: one of its pillars is a fact
	// about the stop-source REGISTRY, and a registry is edited by a different work item than the
	// selector. So the premise is named here, with the machine guard that fails when it stops
	// being true — a natural-language proof with no guard behind it is a proof that quietly
	// expires.
	//
	//	Proof premise: AllStopSources contains exactly one non-structural (derived) source.
	//	               SelectInvalidation refuses a list holding TWO eligible derived rows
	//	               ("two fallbacks is not a tie, it is a contradiction"); with one derived
	//	               source no candidate list can hold two, so that branch cannot fire.
	//	Guard:         TestTheUnreachableSelectionInconsistencyProofStillHasExactlyOneDerivedStopSource
	//	               (steps_test.go) — reads AllStopSources and StopSource.Structural directly
	//	               and FAILS ON PURPOSE the day a second derived source lands, naming the
	//	               three ways out (re-prove, reclassify, or merge the new source).
	//
	// The other two pillars need no registry guard, because both are properties of code in
	// stop.go that the same reviewer reads: an eligible row always carries a value (the screen
	// requires one before it writes ELIGIBLE_NOT_SELECTED), and the index the selector returns
	// is an index it already read a value from.
	ReasonInvalidationSelectionInconsistent Reason = "INVALIDATION_SELECTION_INCONSISTENT"

	// ── EP-4: the target step ─────────────────────────────────────────────────────────

	// ReasonTarget1TickAlignmentUnavailable — pricerule refused to put the first target on
	// the tick grid.
	//
	// REACHABLE, unlike the chase step's equivalent: the 2R target is computed FROM the entry
	// (zone.High + 2 × risk) and can leave pricerule's alignable domain — 1e6 元 — while every
	// input to it is a legal quote. No local rounding is substituted, and no clamp to the
	// domain bound is invented: a clamped target is a fabricated target.
	ReasonTarget1TickAlignmentUnavailable Reason = "TARGET_1_TICK_ALIGNMENT_UNAVAILABLE"

	// ReasonTarget1NotAboveEntry — the first target came out at or below the top of the ideal
	// entry zone after tick alignment.
	//
	// Reachable through a resistance sitting less than one tick above zone.High, which aligns
	// DOWN onto zone.High itself. Refused rather than nudged up a tick: the next tick is a
	// price the rule never computed and it would be published as the level the trade aims at.
	// Note what does NOT happen — the rule does not fall back to the pure 2R target here.
	// That would be a SECOND rule triggered by the failure of the first, which is the shape
	// this package refuses everywhere (see ReasonZoneATRUnavailable).
	ReasonTarget1NotAboveEntry Reason = "TARGET_1_NOT_ABOVE_ENTRY"

	// ReasonTarget2TickAlignmentUnavailable — pricerule refused to align the second target.
	// Reachable for the reason the first one is, one R multiple further out.
	ReasonTarget2TickAlignmentUnavailable Reason = "TARGET_2_TICK_ALIGNMENT_UNAVAILABLE"

	// ReasonValuationCeilingBelowEntry — the valuation base-case target sits AT OR BELOW the
	// price this plan is willing to pay, so there is no second target above the entry.
	//
	// A FINDING, not an error: the model says the stock is already at or above its base-case
	// fair value at the entry. Target1 and the R:R survive — they are price-structure
	// arithmetic and do not depend on the valuation at all — and the second target is
	// withdrawn rather than raised back above the entry, which would publish a level the
	// ceiling had just refused.
	ReasonValuationCeilingBelowEntry Reason = "VALUATION_CEILING_BELOW_ENTRY"

	// ReasonTarget2BelowTarget1 — the valuation ceiling pulled the second target below the
	// first, so the two no longer form an ordered pair.
	//
	// The second target is WITHDRAWN. It is not swapped with the first (they mean different
	// things and both R multiples on the rows would become false), and the second is not
	// raised to meet the first (that discards the ceiling, which is the only independent
	// evidence in the step). Target1 and the R:R are untouched.
	ReasonTarget2BelowTarget1 Reason = "TARGET_2_BELOW_TARGET_1"

	// ── reserved: reachable in production, unreachable from ComputePlan ───────────────

	// ReasonChaseTickAlignmentUnavailable — pricerule refused to align the clamped chase
	// price.
	//
	// RESERVED. It cannot be produced through ComputePlan: the clamped value is at most
	// limitUp, and limitUp is itself a value pricerule returned from RoundToTick, so it is
	// inside the alignable domain and strictly positive. The branch exists anyway because
	// that argument rests on pricerule's own domain bound — which is pricerule's to change —
	// and because `v, _ := pricerule.RoundToTick(...)` is precisely the shape the EP-0 review
	// warned about: ok == false appears in 0.6% of real inputs, which is the frequency at
	// which a discarded error is never noticed.
	//
	// See ReservedReasons for how the registry test stays honest about it.
	ReasonChaseTickAlignmentUnavailable Reason = "CHASE_TICK_ALIGNMENT_UNAVAILABLE"

	// ReasonRiskNotEstablished — there is no risk denominator, so no target and no R:R can be
	// stated.
	//
	// RESERVED. Two sub-cases, both on the trace as a RiskRejection, and both unreachable
	// from ComputePlan:
	//
	//	RISK_NOT_POSITIVE                  zone.High − Invalidation <= 0. ComputeInvalidation
	//	                                   requires the level to be strictly BELOW zone.Low,
	//	                                   and zone.Low <= zone.High, so the difference is
	//	                                   strictly positive by construction.
	//	INVALIDATION_PRICE_BASIS_MISMATCH  the invalidation is on another price series.
	//	                                   ComputeInvalidation builds it on the zone's own.
	//
	// The guards are written because ComputeTargets is EXPORTED and takes the zone and the
	// invalidation as ARGUMENTS — that is what makes the step separately testable, and it is
	// also what makes a caller able to hand it an inconsistent pair. Both branches are driven
	// directly by TestTheRiskDenominatorIsRefusedRatherThanInvented; see ReservedReasons.
	ReasonRiskNotEstablished Reason = "RISK_NOT_ESTABLISHED"

	// ── EP-5: the DECISION step ───────────────────────────────────────────────────────
	//
	// Every code below is a reason a STATUS is what it is, and none of them is a reason a
	// PRICE is missing. They are emitted by DecideStatus (decide.go) and by nothing else, so
	// a reader can partition a plan's reason list into "why these numbers" and "why this
	// verdict" without a lookup table.
	//
	// EVERY STATUS CARRIES AT LEAST ONE. A status with no reason is a verdict with no defence,
	// and the arm that produced it becomes unrecoverable from the archived row — which is the
	// whole failure DecisionRule and this vocabulary exist to prevent.

	// ReasonStatusSemanticUnresolved — there is no entry SHAPE to decide about: the semantic
	// is UNKNOWN, or is not a semantic at all. A STATE. See EntrySemantic.Resolved.
	ReasonStatusSemanticUnresolved Reason = "STATUS_ENTRY_SEMANTIC_UNRESOLVED"

	// ReasonStatusCurrentPriceUnavailable — no usable current price, so no price RELATION can
	// be stated. Every EP-5 verdict except the two policy ones is a comparison against it.
	ReasonStatusCurrentPriceUnavailable Reason = "STATUS_CURRENT_PRICE_UNAVAILABLE"

	// ReasonStatusPolicyUnresolved — the regime's policy reached NO DECISION about this entry
	// shape (UNKNOWN regime, or a regime string that is not a regime).
	//
	// THE UNKNOWN HALF OF THE BEAR/UNKNOWN LINE, at the decision layer. It routes to
	// INSUFFICIENT_DATA and must never route to NO_VALID_ENTRY.
	ReasonStatusPolicyUnresolved Reason = "STATUS_POLICY_UNRESOLVED"

	// ReasonStatusPolicyRefused — the regime's policy FORBIDS this entry shape. BEAR's
	// DEFENSIVE row, and BULL_PULLBACK / SIDEWAYS / DISTRIBUTION for a BREAKOUT.
	//
	// THE BEAR HALF. A DECISION on sufficient evidence, so it routes to NO_VALID_ENTRY — and
	// it does so AHEAD of every price comparison, which is why a BEAR plan is never
	// TOO_EXTENDED: "too expensive to buy here" would imply there is a price at which the
	// policy would buy, and there is not.
	ReasonStatusPolicyRefused Reason = "STATUS_POLICY_REFUSED"

	// ReasonStatusEntryZoneUnavailable — the policy permits this entry and NO ideal entry zone
	// could be COMPUTED, because an input was missing or unusable (no level, no ATR, a basis
	// mismatch, an alignment refusal).
	//
	// A STATE. doc.go's line, one layer up: an unavailable ATR is a missing DEPENDENCY, and a
	// zone that could not be computed is not the same claim as "there is nowhere legal to buy".
	ReasonStatusEntryZoneUnavailable Reason = "STATUS_ENTRY_ZONE_UNAVAILABLE"

	// ReasonStatusNoLegalEntryZone — the zone rule RAN TO COMPLETION on evidence that was
	// present and produced no legal band: every support sits at or above the market, or the
	// band's own lower bound came out at or below zero.
	//
	// A VERDICT. The distinction from the code above is the one EP-5 applies everywhere:
	// completed-on-present-evidence → verdict, missing input → state.
	ReasonStatusNoLegalEntryZone Reason = "STATUS_NO_LEGAL_ENTRY_ZONE"

	// ReasonStatusRiskBoundaryUnavailable — no invalidation, because no candidate was ever
	// comparable with the zone. See ReasonInvalidationEvidenceUnavailable and
	// ReasonInvalidationSelectionInconsistent. A STATE.
	ReasonStatusRiskBoundaryUnavailable Reason = "STATUS_RISK_BOUNDARY_UNAVAILABLE"

	// ReasonStatusNoRiskBoundary — every invalidation candidate was compared with the zone and
	// none of them is a legal boundary below it. See ReasonNoValidInvalidation.
	//
	// A VERDICT, and the argument for it is worth stating because "no stop" sounds like a
	// missing detail: the system knows where it might buy and cannot say where it would be
	// proved wrong. That is not an executable trade plan, so it is NO_VALID_ENTRY and not a
	// BUY_NOW with a caveat.
	ReasonStatusNoRiskBoundary Reason = "STATUS_NO_RISK_BOUNDARY"

	// ReasonStatusAboveMaxChase — the current price is STRICTLY ABOVE the plan's own chase
	// ceiling. The whole numeric content of TOO_EXTENDED.
	//
	// STRICTLY. A price EQUAL to the ceiling is the ceiling doing its job — the highest price
	// the plan permits paying is a price the plan permits paying — so equality is not this
	// code. See DecideStatus's arm S9.
	ReasonStatusAboveMaxChase Reason = "STATUS_ABOVE_MAX_CHASE"

	// ReasonStatusPriceInsideZone — the current price is INSIDE the ideal entry zone, bounds
	// INCLUSIVE. The price half of BUY_NOW.
	ReasonStatusPriceInsideZone Reason = "STATUS_PRICE_INSIDE_ZONE"

	// ReasonStatusPriceAboveEntryZone — the current price sits ABOVE the top of the zone, so
	// the legal entry is below the market. The price half of WAIT_PULLBACK.
	ReasonStatusPriceAboveEntryZone Reason = "STATUS_PRICE_ABOVE_ENTRY_ZONE"

	// ReasonStatusPriceBelowEntryZone — the current price sits BELOW the bottom of the zone,
	// so the breakout level has not been reached. The price half of WAIT_BREAKOUT.
	ReasonStatusPriceBelowEntryZone Reason = "STATUS_PRICE_BELOW_ENTRY_ZONE"

	// ReasonStatusImmediateEntryRefused — the policy FORBIDS executing at the market here,
	// even though it permits the entry shape. A DECISION.
	ReasonStatusImmediateEntryRefused Reason = "STATUS_IMMEDIATE_ENTRY_REFUSED"

	// ReasonStatusImmediateEntryUnresolved — the policy reached NO DECISION about executing at
	// the market here. A STATE, kept apart from the code above for the reason every UNRESOLVED
	// in this package is kept apart from every DISALLOWED.
	ReasonStatusImmediateEntryUnresolved Reason = "STATUS_IMMEDIATE_ENTRY_UNRESOLVED"

	// ReasonStatusRequirementNotMet — a Requirement attached to the permission is FALSE here.
	//
	// Only INSIDE_VALID_ZONE can be false at this layer, and it is false exactly when the
	// current price is outside the zone — which is the case on the ONE at-the-market arm that
	// runs there: S16, a breakout that has left its band and is being considered for a chase.
	// S10 evaluates the same requirement with the price inside the band, where it is discharged.
	//
	// It is a DECISION, not a state: the condition was stated by EP-2, evaluated here, and found
	// false. That is why it folds to DISALLOWED and not to UNRESOLVED — the opposite of
	// ReasonStatusRequirementNotEvaluated, which is a condition nothing in this repo can read.
	ReasonStatusRequirementNotMet Reason = "STATUS_REQUIREMENT_NOT_MET"

	// ReasonStatusRequirementNotEvaluated — a Requirement attached to the permission is one
	// EP-5 CANNOT EVALUATE, so no decision about executing at the market can be reached.
	//
	// # This is a refusal to pretend, and it is why a whole regime can read INSUFFICIENT_DATA
	//
	// NEAR_SUPPORT and ELEVATED_EVIDENCE are conditions EP-2 attached and nothing in this repo
	// evaluates: the first needs a judgement about which level counts as support (the zone's
	// centre may be today's single-bar low — see BaseLowKind), the second is a setup SCORE this
	// package cannot see. Ignoring them would publish BUY_NOW while a stated condition went
	// unchecked, which is precisely the "a comment claimed a guard the code does not provide"
	// failure this package is written against.
	//
	// So SIDEWAYS and DISTRIBUTION cannot produce BUY_NOW today. That is a true statement about
	// a MISSING EVALUATOR, not a claim about the stock, and it is confined to the at-the-market
	// arms: WAIT_PULLBACK and WAIT_BREAKOUT do not assert that the entry may be taken now, so
	// they are not gated by it. See decide.go's evaluateRequirement and the TODO it carries.
	ReasonStatusRequirementNotEvaluated Reason = "STATUS_REQUIREMENT_NOT_EVALUATED"

	// ReasonStatusImmediateChasePermitted — price has left the entry zone UPWARD, it is still
	// at or below the chase ceiling, and the regime EXPLICITLY permits paying up at the market
	// (ChasePolicy.Immediate).
	//
	// It is emitted ONLY for a BREAKOUT. A breakout plan's instruction was "buy when the level
	// gives way"; the level gave way and the fill was missed by a little, so paying up is the
	// SAME instruction continued. A pullback plan's instruction is "wait for price to come back
	// to the level", and price above the band is that instruction NOT YET SATISFIED — reading
	// it as a chase would delete WAIT_PULLBACK for every plan whose market sits inside one
	// chase tolerance of its support.
	ReasonStatusImmediateChasePermitted Reason = "STATUS_IMMEDIATE_CHASE_PERMITTED"

	// ReasonStatusChaseCeilingUnresolved — price is above the zone, the answer depends on the
	// chase ceiling, and there IS no ceiling for a reason that is not a decision: the previous
	// close is missing, the basis is not RAW, the limit rule is unknown, or the chase policy
	// itself is unresolved.
	//
	// # The three nils, honoured one layer up
	//
	// ComputeMaxChase already refuses to merge them (CHASE_NOT_ALLOWED / CHASE_POLICY_UNRESOLVED
	// / PREVIOUS_CLOSE_UNAVAILABLE) and this is where that split pays: a ceiling absent BY
	// DECISION leaves "wait for the pullback" perfectly answerable — "you may not chase" and
	// "you may not wait" are different permissions — while a ceiling that could not be COMPUTED
	// leaves "is this merely above the band, or beyond what the plan would ever pay" with no
	// answer at all. Guessing either way would invent one.
	ReasonStatusChaseCeilingUnresolved Reason = "STATUS_CHASE_CEILING_UNRESOLVED"

	// ReasonStatusNoExecutablePriceRelation — the evidence is complete, the policy permits the
	// shape, a zone and a risk boundary both exist, and the current price stands in NO relation
	// to the zone that any arm of the decision table calls executable or waitable.
	//
	// THE FALLBACK ARM, and it is a VERDICT rather than a state on purpose: everything a
	// judgement needs was present and the judgement is "not here". ONE live shape reaches it —
	// a PULLBACK whose support band price has already fallen through, so there is no level left
	// to wait for. A breakout that ran above its band under a policy refusing to pay up is a
	// DIFFERENT sentence and gets ReasonStatusImmediateEntryRefused on its own arm (S16).
	ReasonStatusNoExecutablePriceRelation Reason = "STATUS_NO_EXECUTABLE_PRICE_RELATION"

	// ReasonStatusThesisContradicted — EP-6D. An entry shape was proposed and the scanner's
	// primary Action CONTRADICTS it (the adapter classified it CONTRADICTED — SELL, REDUCE,
	// TAKE PROFIT or STOP LOSS). A VERDICT: the evidence is present and it says no, so arm S1
	// answers NO_VALID_ENTRY whatever the price, policy or band, and ComputePlan publishes no
	// executable price beside it. See ThesisStanding.
	ReasonStatusThesisContradicted Reason = "STATUS_THESIS_CONTRADICTED"

	// ReasonStatusThesisUnclassified — EP-6D. Price and permissions would have published
	// BUY_NOW, and nobody classified whether the primary Action contradicts the thesis (the
	// standing is "" or not a declared value). A STATE, never read as "not contradicted":
	// the arm answers INSUFFICIENT_DATA on S11 or S15. MISSING ≠ ZERO.
	ReasonStatusThesisUnclassified Reason = "STATUS_THESIS_UNCLASSIFIED"

	// ReasonPlanInvariantViolated — the plan broke one of its own invariants
	// (AllPlanInvariants) and its executable prices were WITHDRAWN before it was returned.
	//
	// RESERVED, and it must stay unreachable from ComputePlan: a plan that reaches this code
	// is a bug in this package, not a market condition. EnforcePlanInvariants is the producer
	// and it is driven directly by TestTheInvariantGateWithdrawsPricesFromABrokenPlan, so the
	// code is verified rather than merely declared — see ReservedReasons.
	ReasonPlanInvariantViolated Reason = "PLAN_INVARIANT_VIOLATED"
)

// AllReasons is every reason code this package can emit.
//
// Declared rather than derived, because Go cannot enumerate constants — and therefore kept
// honest FROM BOTH ENDS by TestReasonRegistryIsExactlyWhatProductionEmits:
//
//	forward   every reason production actually emits is a member;
//	backward  every member is actually emitted by production.
//
// "Production" is the union of the exported rules that write onto Plan.Reasons: ComputePlan
// over every snapshot, and (from EP-5) DecideStatus over the decision matrix. The exception is
// ReservedReasons, whose members can be reached only by breaking an exported step's stated
// precondition or not at all; each names its producer there.
//
// The backward half is the one that is usually skipped, and it is the one that matters here.
// FU-11's D8 shipped a guard covering 11 of 14 codes while its comment claimed all 14, and
// two of the three gaps were live on real stocks — a one-directional check reports green
// while the registry and the code drift apart. A dead code in this list is not harmless
// either: it is a code a consumer will write a branch for, and the branch will never run.
var AllReasons = []Reason{
	ReasonSnapshotMalformed,
	ReasonCurrentPriceUnavailable,
	ReasonPriceBasisUnavailable,
	ReasonMarketRegimeUnavailable,
	ReasonEntrySemanticUnavailable,

	// EP-3b, the zone step.
	ReasonZoneSemanticNotPermitted,
	ReasonZonePolicyUnresolved,
	ReasonPullbackLevelsUnavailable,
	ReasonPullbackLevelsBasisMismatch,
	ReasonPullbackLevelNotBelowCurrentPrice,
	ReasonBreakoutLevelUnavailable,
	ReasonBreakoutLevelBasisMismatch,
	ReasonBreakoutLevelNotAQuote,
	ReasonZoneATRUnavailable,
	ReasonZoneATRBasisMismatch,
	ReasonZoneATRPeriodMismatch,
	ReasonZoneTickAlignmentUnavailable,
	ReasonZoneLowNotAPrice,
	ReasonPullbackZoneOverlapsCurrentPrice,
	ReasonRecentPriceAdjustment,

	// EP-3b, the chase step.
	ReasonChaseNotAllowed,
	ReasonChasePolicyUnresolved,
	ReasonPreviousCloseUnavailable,
	ReasonPreviousCloseBasisMismatch,
	ReasonChaseLimitRequiresRawBasis,
	ReasonChaseLimitRuleUnavailable,
	ReasonChaseLimitPriceUnavailable,
	ReasonChaseCeilingBelowZone,

	// EP-4, the invalidation step.
	ReasonNoValidInvalidation,
	ReasonInvalidationEvidenceUnavailable,
	ReasonInvalidationSelectionInconsistent,

	// EP-4, the target step.
	ReasonTarget1TickAlignmentUnavailable,
	ReasonTarget1NotAboveEntry,
	ReasonTarget2TickAlignmentUnavailable,
	ReasonValuationCeilingBelowEntry,
	ReasonTarget2BelowTarget1,

	// EP-5, the decision step.
	ReasonStatusSemanticUnresolved,
	ReasonStatusCurrentPriceUnavailable,
	ReasonStatusPolicyUnresolved,
	ReasonStatusPolicyRefused,
	ReasonStatusEntryZoneUnavailable,
	ReasonStatusNoLegalEntryZone,
	ReasonStatusRiskBoundaryUnavailable,
	ReasonStatusNoRiskBoundary,
	ReasonStatusAboveMaxChase,
	ReasonStatusPriceInsideZone,
	ReasonStatusPriceAboveEntryZone,
	ReasonStatusPriceBelowEntryZone,
	ReasonStatusImmediateEntryRefused,
	ReasonStatusImmediateEntryUnresolved,
	ReasonStatusRequirementNotMet,
	ReasonStatusRequirementNotEvaluated,
	ReasonStatusImmediateChasePermitted,
	ReasonStatusChaseCeilingUnresolved,
	ReasonStatusNoExecutablePriceRelation,

	// EP-6D, the thesis standing on the two BUY_NOW arms.
	ReasonStatusThesisContradicted,
	ReasonStatusThesisUnclassified,

	// Reserved. See ReservedReasons.
	ReasonChaseTickAlignmentUnavailable,
	ReasonRiskNotEstablished,
	ReasonPlanInvariantViolated,
}

// ReservedReasons are codes that PRODUCTION CAN EMIT but that NO INPUT TO ComputePlan can
// produce.
//
// The pattern, and the limit on it, are policy.go's ReservedPolicyNames: a reservation is
// only allowed when a work item can say what it is for, because "unreachable" and "a mapping
// somebody forgot to write" look identical from the registry. All three entries here name a
// producer and a test that drives it:
//
//	CHASE_TICK_ALIGNMENT_UNAVAILABLE  ComputeMaxChase's `ok == false` branch on
//	                                  pricerule.RoundToTick. Unreachable because the clamp
//	                                  bounds its input inside pricerule's alignable domain —
//	                                  an argument about ANOTHER package's constant, which is
//	                                  why the branch is written rather than assumed away.
//	RISK_NOT_ESTABLISHED              ComputeTargets' two guards on the (zone, invalidation)
//	                                  pair it is HANDED. Unreachable from ComputePlan because
//	                                  ComputeInvalidation publishes a level strictly below
//	                                  zone.Low on the zone's own basis — an argument about
//	                                  ANOTHER FUNCTION IN THIS PACKAGE, which is why the
//	                                  branch is written rather than assumed away, and why the
//	                                  test drives ComputeTargets directly with a planted pair.
//	PLAN_INVARIANT_VIOLATED           EnforcePlanInvariants, driven directly on a hand-built
//	                                  broken Plan. Reaching it from ComputePlan would mean
//	                                  this package computed an illegal price, which is a bug
//	                                  and not an input class.
//	INVALIDATION_EVIDENCE_UNAVAILABLE ComputeInvalidation's "no row was ever comparable"
//	                                  branch. EP-5's addition, and it is RISK_NOT_ESTABLISHED's
//	                                  argument exactly: the step is EXPORTED, takes the ZONE as
//	                                  an ARGUMENT, and its doc states the precondition that
//	                                  makes the branch unreachable. Break the precondition and
//	                                  it is the only honest answer, which is why the branch is
//	                                  written. TestTheStopEvidenceGapIsRefusedRatherThanCalled
//	                                  AVerdict drives it.
//	INVALIDATION_SELECTION_INCONSISTENT
//	                                  ComputeInvalidation's two self-contradiction guards.
//	                                  The ONE reservation here that no caller can reach at all,
//	                                  by any argument: it fires only if the screen and the
//	                                  selector in this package disagree. That is why its entry
//	                                  in this list carries a LEDGER TEST rather than a producer
//	                                  test — see the code's own doc, and see
//	                                  ReasonChaseTickAlignmentUnavailable for the precedent.
//
// The backward half of TestReasonRegistryIsExactlyWhatProductionEmits exempts exactly these
// five and NOTHING ELSE, and a separate test pins the list — so a reserved code is still a code
// with a stated producer and a stated argument, not a dead branch with an excuse.
//
// The two EP-5 additions are both in the INVALIDATION step, and that is not a coincidence: EP-5
// is the item that SPLIT one reason code into three (see ReasonNoValidInvalidation), and a split
// makes distinctions that the coupled pipeline does not currently exercise. The alternative was
// to leave the three merged, which is the thing spec §17 exists to forbid: the decision layer
// would have had to call every missing stop either a state or a verdict, and both are wrong for
// half the inputs.
var ReservedReasons = map[Reason]bool{
	ReasonChaseTickAlignmentUnavailable:     true,
	ReasonRiskNotEstablished:                true,
	ReasonPlanInvariantViolated:             true,
	ReasonInvalidationEvidenceUnavailable:   true,
	ReasonInvalidationSelectionInconsistent: true,
}

// KnownReason reports whether a code is in the registry.
func KnownReason(r Reason) bool {
	for _, k := range AllReasons {
		if k == r {
			return true
		}
	}
	return false
}
