package entryplan

// ── EP-5: the DECISION ────────────────────────────────────────────────────────────────
//
// This file publishes the first EntryStatus this package has ever produced. It computes NO
// PRICE, and that is not a slogan — it is the shape of the file. Every number it touches was
// computed by EP-3b or EP-4 and is READ HERE ONLY BY COMPARISON:
//
//	zone.Low <= current <= zone.High      is the market inside the band we may buy in
//	current  >  maxChase                  has the market run past what we may ever pay
//	current  <  zone.Low                  has the breakout level not been reached yet
//
// There is no support selection, no ATR, no half width, no pivot, no round-to-tick, no limit
// band, no 2R, no 3.5R and no valuation clamp anywhere below. A decision layer that recomputed
// any of them would be a SECOND opinion about a number the plan already published, and the two
// would disagree on some input — at which point the report shows one and the verdict was made
// on the other.
//
// # BUY is not BUY_NOW
//
// The scanner's ActPullbackBuy / ActBreakoutBuy already said this stock is worth owning. That
// is an input here (EntrySemantic), not a synonym for the answer. BUY_NOW is the conjunction of
// SIX conditions and each one can fail on a stock the scanner likes:
//
//	1. the entry SHAPE is resolved                     (EntrySemantic.Resolved)
//	2. the regime's policy permits that shape          (PolicyResult.Allowance)
//	3. an ideal entry zone exists                      (EP-3b)
//	4. the current price is INSIDE it, bounds included — or the plan explicitly authorises
//	   paying above it (ChasePolicy.Immediate) and the ceiling has not been crossed
//	5. the policy permits EXECUTING AT THE MARKET here, including every Requirement it
//	   attached to that permission
//	6. (EP-6D) the scanner's primary Action was classified and does NOT contradict the
//	   thesis (DecisionInput.Thesis == NOT_CONTRADICTED). An unclassified one is never BUY_NOW
//	   (S11/S15); a CONTRADICTED one (SELL / REDUCE / TAKE PROFIT / STOP LOSS) is never
//	   anything but NO_VALID_ENTRY once a shape is resolved (S1), and publishes no price
//
// # INSUFFICIENT_DATA is a state and NO_VALID_ENTRY is a verdict, and this is where it is decided
//
// The line is inherited three layers deep — model.Regime's UNKNOWN, analyzer.DecideRegime's R0,
// EP-2's AllowanceUnresolved — and it lands here as ONE criterion applied to every absence:
//
//	the rule RAN TO COMPLETION on evidence that was present  →  a VERDICT     NO_VALID_ENTRY
//	an input was missing, unusable, or undecided             →  a STATE       INSUFFICIENT_DATA
//
// So BEAR is NO_VALID_ENTRY (the regime was known, the table was consulted, the answer is no)
// and UNKNOWN is INSUFFICIENT_DATA (no answer was reached), and neither may borrow the other's
// output. See ReadAbsence for the same criterion applied to a missing zone, a missing ceiling
// and a missing risk boundary.
//
// # What is deliberately NOT a status gate
//
//   - THE RISK/REWARD RATIO. There is no `RiskReward.Ratio >= 2 → else NO_VALID_ENTRY` here and
//     v1 must not grow one. A ratio threshold is a claim about outcomes, and this repo has no
//     backtest behind one for THIS construction (internal/r6backtest measures a different stop,
//     taken from a FILL — see invalidationATRMultiple). A number invented here would silently
//     delete trades, and the deletion would be invisible because the plan would simply not
//     appear. If a later item wants it, it arrives with a study attached.
//   - TARGET2, THE VALUATION CEILING AND THE STRUCTURAL RESISTANCE. All optional, all absent on
//     perfectly ordinary plans (no resistance above the entry, no suitable P/E model), and none
//     of them is a statement about whether this entry may be taken.
//   - THE ADJUSTMENT AGE. A caveat, never a gate — the residue is ((N-1)/N)^age and 63 bars is
//     where a report stops mentioning it, not the day anything becomes clean (see
//     adjustmentAgeCaveatBars). Suppressing a plan over a residue of a fraction of a percent
//     would delete a usable entry.
//   - CONFIDENCE. BUY_NOW with LOW confidence is a real row: "how much evidence stands behind
//     the plan" and "what does the plan say" are different axes, and collapsing them would make
//     a thin-evidence plan indistinguishable from one whose entry has not arrived yet. There is
//     no Confidence field on DecisionInput, so the coupling cannot be written by accident.
//
// # And OVERHEATED is NOT aliased to TOO_EXTENDED
//
// The scanner's 「太熱不要追」 caution is a different model of the same idea, and TOO_EXTENDED is
// DERIVED here — setup semantic, plus a zone, plus a ceiling, plus current > ceiling. Wiring
// OVERHEATED straight through would make the two agree by construction and destroy the only
// useful thing about having two models: the ability to notice that they DISAGREE. entryplan
// cannot import internal/scanner at all (the architecture test enforces it), so the alias is
// unwritable here; this paragraph is the constraint on the ADAPTER.

// DecisionRule identifies which arm of the decision table produced a status.
//
// The precedent is internal/market/analyzer.DecideRegime, whose rule ids are persisted with the
// verdict for exactly this reason: "why was 2026-08-07 DISTRIBUTION" is answered by "R3", not by
// re-reading the code as it is today. Same here — an archived plan that says WAIT_PULLBACK/S17
// can be re-derived; one that says only WAIT_PULLBACK cannot.
//
// The ids are POSITIONAL — S0 is the first arm evaluated, S19 the last — so inserting an arm
// renumbers the ones below it. That is affordable exactly once, before any row is archived, and
// it is the price of the id meaning "the nth question the table asks" rather than "the nth arm
// anyone happened to write". EP-5 is the first version to persist one at all.
type DecisionRule string

// The decision table, in order. FIRST MATCH WINS, and the order is part of the specification:
// reordering these arms changes the verdict for inputs that satisfy two of them at once, so the
// order is stated here, asserted by TestTheDecisionPrecedenceIsTheSpecifiedOrder, and each arm
// carries its id into the result.
//
// The orderings that are load-bearing, i.e. where a plausible reordering is WRONG:
//
//   - (EP-6D) S1 (the thesis is contradicted) SITS RIGHT AFTER S0 AND AHEAD OF EVERYTHING ELSE.
//     Ahead of every price and policy arm, because the user's rule is that a stock the scanner
//     wants out of shows NO entry at all — not "wait for the pullback at X", not "too extended
//     above Y" — and the answer does not depend on where the market is. BEHIND S0, because
//     the thesis IS the resolved entry shape (the user's decision: PULLBACK_BUY / BREAKOUT_BUY
//     is the BUY thesis); with no shape there is no thesis to contradict, and such a plan has
//     no zone to withhold in the first place. Putting it ahead of S0 would turn every
//     SELL / REDUCE / TAKE PROFIT / STOP LOSS stock whose WatchAction is WAIT / WATCH_CLOSELY /
//     an exit into a verdict about an
//     entry nobody proposed.
//   - S4 (policy refuses) SITS AHEAD OF S9 (price above the ceiling). A BEAR plan with a
//     mathematically valid zone must be NO_VALID_ENTRY and never TOO_EXTENDED, because
//     TOO_EXTENDED means "the setup is fine, the price is not" — it implies a lower price at
//     which this WOULD be bought, and in BEAR there is none.
//   - S7/S8 (no risk boundary) SIT AHEAD OF every price arm. A plan that cannot say where it
//     would be proved wrong is not an executable trade plan at any price, so where the market
//     happens to be standing does not make it one.
//   - S13 (the ceiling is unresolved) SITS AHEAD OF S17 (wait for the pullback). With price
//     above the band and no ceiling COMPUTED, "merely above" and "beyond anything this plan
//     would pay" are both consistent with the evidence, and picking one is a guess. It does NOT
//     sit ahead of anything when the ceiling is absent BY DECISION: "you may not chase" and
//     "you may not wait" are different permissions, so a DISALLOWED chase still yields
//     WAIT_PULLBACK.
const (
	// RuleSemanticUnresolved — S0. No entry shape to decide about.
	RuleSemanticUnresolved DecisionRule = "S0"
	// RuleThesisContradicted — S1 (EP-6D). A shape was proposed and the scanner's primary Action
	// contradicts it (SELL / REDUCE / TAKE PROFIT / STOP LOSS). NO_VALID_ENTRY, and ComputePlan
	// publishes NO executable price for a plan decided here (see ComputePlan). Inserted at
	// position 1, which renumbered every arm below it — affordable because no plan had been
	// archived when it was inserted (EP-7's persistence came later; it does not store the rule id).
	RuleThesisContradicted DecisionRule = "S1"
	// RuleCurrentPriceUnavailable — S2. No price to compare anything against.
	RuleCurrentPriceUnavailable DecisionRule = "S2"
	// RulePolicyUnresolved — S3. The regime reached no decision about this shape.
	RulePolicyUnresolved DecisionRule = "S3"
	// RulePolicyRefused — S4. The regime forbids this shape. THE BEAR ARM.
	RulePolicyRefused DecisionRule = "S4"
	// RuleZoneUnavailable — S5. No entry band could be computed.
	RuleZoneUnavailable DecisionRule = "S5"
	// RuleNoLegalZone — S6. The band rule ran and there is no legal band.
	RuleNoLegalZone DecisionRule = "S6"
	// RuleRiskBoundaryUnavailable — S7. Nothing was ever comparable as an invalidation.
	RuleRiskBoundaryUnavailable DecisionRule = "S7"
	// RuleNoRiskBoundary — S8. Every candidate was compared and none is a legal boundary.
	RuleNoRiskBoundary DecisionRule = "S8"
	// RuleAboveMaxChase — S9. THE TOO_EXTENDED ARM, and the only one.
	RuleAboveMaxChase DecisionRule = "S9"
	// RuleInsideZone — S10. THE BUY_NOW ARM: price is in the band and execution is permitted.
	RuleInsideZone DecisionRule = "S10"
	// RuleInsideZoneUndecided — S11. In the band, and the permission to execute is undecided.
	// EP-6D widened what "undecided" covers: an UNCLASSIFIED thesis standing lands here too,
	// with its own reason code (STATUS_THESIS_UNCLASSIFIED).
	RuleInsideZoneUndecided DecisionRule = "S11"
	// RuleInsideZoneRefused — S12. In the band, and executing at the market is forbidden.
	RuleInsideZoneRefused DecisionRule = "S12"
	// RuleChaseCeilingUnresolved — S13. Above the band, and no ceiling could be computed.
	RuleChaseCeilingUnresolved DecisionRule = "S13"
	// RuleImmediateChase — S14. THE SECOND BUY_NOW ARM: a breakout that ran, inside its
	// ceiling, under a policy that explicitly permits paying up.
	RuleImmediateChase DecisionRule = "S14"
	// RuleImmediateChaseUndecided — S15. Same shape, and the permission is undecided —
	// including, since EP-6D, an unclassified thesis standing.
	RuleImmediateChaseUndecided DecisionRule = "S15"
	// RuleImmediateChaseRefused — S16. Same shape, and the permission is REFUSED.
	//
	// Its own arm rather than a fall-through to S19, and the difference is the reason code
	// that survives: the refusal is a DECISION about paying up, and the fallback's code says
	// "the market stands in no relation to the band that any arm calls executable", which is
	// false here — the relation was recognised and the permission said no.
	RuleImmediateChaseRefused DecisionRule = "S16"
	// RuleWaitPullback — S17. The legal entry is below the market.
	RuleWaitPullback DecisionRule = "S17"
	// RuleWaitBreakout — S18. The breakout level is above the market and has not given way.
	RuleWaitBreakout DecisionRule = "S18"
	// RuleNoExecutableRelation — S19. THE FALLBACK, and it is a verdict.
	RuleNoExecutableRelation DecisionRule = "S19"
)

// AllDecisionRules is every arm, IN THE ORDER DecideStatus evaluates them.
//
// Declared rather than derived, because Go cannot enumerate constants, and kept honest from both
// ends: every rule DecideStatus returns is a member, and every member is returned for at least
// one input in the decision matrix. An arm nobody can reach is a branch that has never run.
var AllDecisionRules = []DecisionRule{
	RuleSemanticUnresolved,
	RuleThesisContradicted,
	RuleCurrentPriceUnavailable,
	RulePolicyUnresolved,
	RulePolicyRefused,
	RuleZoneUnavailable,
	RuleNoLegalZone,
	RuleRiskBoundaryUnavailable,
	RuleNoRiskBoundary,
	RuleAboveMaxChase,
	RuleInsideZone,
	RuleInsideZoneUndecided,
	RuleInsideZoneRefused,
	RuleChaseCeilingUnresolved,
	RuleImmediateChase,
	RuleImmediateChaseUndecided,
	RuleImmediateChaseRefused,
	RuleWaitPullback,
	RuleWaitBreakout,
	RuleNoExecutableRelation,
}

// KnownDecisionRule reports whether an id is in the registry. Mirrors KnownReason.
func KnownDecisionRule(r DecisionRule) bool {
	for _, k := range AllDecisionRules {
		if k == r {
			return true
		}
	}
	return false
}

// AbsenceReading is how the decision layer READS a code that explains a missing price.
//
// It exists because "there is no X" is three different sentences and a status must be a
// different word for each. The classification is a property of the CODE, not of which field was
// empty, so one table serves the zone, the ceiling and the risk boundary alike.
type AbsenceReading string

const (
	// AbsenceUnclassified — this code is not one this layer knows how to read.
	//
	// The zero value on purpose, and it is treated as an EVIDENCE GAP wherever a decision
	// depends on it: an absence nobody has classified must not become a verdict about a stock.
	// A code reaching here is a registry gap, not a market state.
	AbsenceUnclassified AbsenceReading = "UNCLASSIFIED"

	// AbsenceEvidenceGap — an input was missing, unusable or on the wrong price basis, so the
	// rule never ran to a conclusion. → INSUFFICIENT_DATA.
	AbsenceEvidenceGap AbsenceReading = "EVIDENCE_GAP"

	// AbsenceSearched — the rule RAN TO COMPLETION on evidence that was present and its answer
	// is "there is none". → NO_VALID_ENTRY.
	AbsenceSearched AbsenceReading = "SEARCHED_AND_NONE"

	// AbsenceByDecision — the number is missing because a POLICY refused to produce it, and its
	// absence therefore says nothing about whether an entry exists.
	//
	// Only the chase ceiling reaches this reading, and it is the whole content of the three
	// nils ComputeMaxChase refuses to merge: a plan that may not chase can still wait.
	AbsenceByDecision AbsenceReading = "ABSENT_BY_DECISION"
)

// AllAbsenceReadings is the registry, pinned as literals by golden_test.go.
var AllAbsenceReadings = []AbsenceReading{
	AbsenceUnclassified,
	AbsenceEvidenceGap,
	AbsenceSearched,
	AbsenceByDecision,
}

// ReadAbsence classifies one rejection code — a ZoneTrace.Rejection, a ChaseTrace.Rejection or a
// StopTrace.Rejection — into what its absence MEANS for the decision.
//
// # The criterion, applied code by code
//
// Not "which field is empty" but "did a comparison actually run on evidence that was there".
// The two cases the criterion decides against the obvious reading are worth naming:
//
//	PULLBACK_LEVEL_NOT_BELOW_CURRENT_PRICE  is a VERDICT. Every support the plan knows about
//	    was in hand, every one of them was compared with the market, and all of them are at or
//	    above it. Nothing is missing; there is simply no band below the market to buy into.
//	BREAKOUT_LEVEL_NOT_A_QUOTE              is a GAP. The pivot handed to this package is not a
//	    price any exchange quoted, which is a defect in the INPUT, not a fact about the stock.
//
// A code this table does not list answers UNCLASSIFIED, which every caller treats as a gap. That
// is the conservative direction: a state can be corrected by better data, a verdict published in
// its place cannot be undone by a reader.
//
// TestEveryRejectionCodeIsClassified enumerates the rejection-capable codes independently and
// requires each to answer something other than UNCLASSIFIED, so a new rejection code added
// upstream cannot silently default into "insufficient data".
func ReadAbsence(r Reason) AbsenceReading {
	switch r {
	// ── the zone step ─────────────────────────────────────────────────────────────────
	case ReasonZoneSemanticNotPermitted:
		// The policy refused the SHAPE. A decision on sufficient evidence, and — unlike the
		// chase ceiling's refusal — it removes the entry itself, so it is a verdict about
		// whether an entry exists and not merely about a missing number.
		return AbsenceSearched
	case ReasonZonePolicyUnresolved:
		return AbsenceEvidenceGap
	case ReasonPullbackLevelsUnavailable, ReasonPullbackLevelsBasisMismatch,
		ReasonBreakoutLevelUnavailable, ReasonBreakoutLevelBasisMismatch,
		ReasonBreakoutLevelNotAQuote, ReasonZoneATRUnavailable, ReasonZoneATRBasisMismatch,
		ReasonZoneATRPeriodMismatch, ReasonZoneTickAlignmentUnavailable,
		ReasonEntrySemanticUnavailable:
		return AbsenceEvidenceGap
	case ReasonPullbackLevelNotBelowCurrentPrice:
		return AbsenceSearched
	case ReasonZoneLowNotAPrice:
		// centre − halfWidth came out at or below zero. Both terms were present and the
		// arithmetic completed: this stock's own volatility is wider than twice the level its
		// band would be centred on, which is a fact about the stock.
		return AbsenceSearched

	// ── the chase step ────────────────────────────────────────────────────────────────
	case ReasonChaseNotAllowed:
		return AbsenceByDecision
	case ReasonChaseCeilingBelowZone:
		// Today's legal ceiling sits below the top of the band. A completed comparison of two
		// present numbers, and it says nothing was missing — but it also does not say there is
		// no entry, only that there is no headroom above it. Read as a DECIDED absence for the
		// same reason CHASE_NOT_ALLOWED is: the ceiling is gone, the zone is not.
		return AbsenceByDecision
	case ReasonChasePolicyUnresolved, ReasonPreviousCloseUnavailable,
		ReasonPreviousCloseBasisMismatch, ReasonChaseLimitRequiresRawBasis,
		ReasonChaseLimitRuleUnavailable, ReasonChaseLimitPriceUnavailable,
		ReasonChaseTickAlignmentUnavailable:
		return AbsenceEvidenceGap

	// ── the invalidation step ─────────────────────────────────────────────────────────
	case ReasonNoValidInvalidation:
		return AbsenceSearched
	case ReasonInvalidationEvidenceUnavailable, ReasonInvalidationSelectionInconsistent:
		return AbsenceEvidenceGap
	}
	return AbsenceUnclassified
}

// DecisionInput is everything DecideStatus is allowed to see, and nothing else.
//
// A PROJECTION, deliberately narrow. It is not a Snapshot and it is certainly not a scanner
// result: the fields below are the OUTPUTS of steps that already ran, so this layer cannot
// re-derive a level, a width, a ceiling or a target even by accident — there is nothing here to
// re-derive them from. It also carries no Confidence and no AdjustmentAge, which is how "LOW
// confidence must not become WAIT" and "a recent adjustment must not become a gate" are made
// unwritable rather than merely forbidden.
//
// Every pointer means ABSENT when nil, and every absence is paired with the CODE that explains
// it. Reading `Zone == nil` alone would collapse "the ATR was missing" into "there is nowhere to
// buy", which is the single most consequential merge this layer can make.
type DecisionInput struct {
	// Semantic is WHICH SHAPE of entry the scanner proposed. Consumed, never derived.
	Semantic EntrySemantic `json:"semantic"`

	// Policy is EP-2's answer for (regime, semantic), whole — the semantic's allowance and
	// its requirements, plus the regime's BuyNow and Chase permissions. Carried as the
	// PolicyResult rather than as three booleans so that the BEAR/UNKNOWN split arrives here
	// in the type that protects it.
	Policy PolicyResult `json:"policy"`

	// CurrentPrice is the market. Nil, non-finite or non-positive means UNAVAILABLE, and
	// every comparison below is against it.
	CurrentPrice *float64 `json:"current_price,omitempty"`

	// Zone is the published ideal entry band, and ZoneAbsence is the code that explains a nil
	// one. Both, never one: see the type doc.
	Zone        *PriceZone `json:"zone,omitempty"`
	ZoneAbsence Reason     `json:"zone_absence,omitempty"`

	// MaxChasePrice is the published ceiling, and ChaseAbsence explains a nil one. The three
	// nils ComputeMaxChase refuses to merge are read back apart here through ReadAbsence.
	MaxChasePrice *float64 `json:"max_chase_price,omitempty"`
	ChaseAbsence  Reason   `json:"chase_absence,omitempty"`

	// Invalidation is the published thesis-failure level, and StopAbsence explains a nil one.
	Invalidation *Invalidation `json:"invalidation,omitempty"`
	StopAbsence  Reason        `json:"stop_absence,omitempty"`

	// Thesis is whether the scanner's primary Action contradicts the entry thesis (EP-6D).
	// Read by S1 (CONTRADICTED → NO_VALID_ENTRY) and by the two arms that would publish BUY_NOW,
	// through foldThesis (unclassified → not BUY_NOW). A category,
	// not a price and not a grade; "" means unclassified and is never read as permission.
	Thesis ThesisStanding `json:"thesis,omitempty"`
}

// DecisionResult is ONE status, the arm that produced it, and why.
//
// ITS FIELD SET IS CLOSED (EP-6D): TestTheDecisionResultFieldSetIsExactlyTheAllowlist compares
// the exported fields against a literal allowlist in both directions, so a fourth field — a
// Confidence, a score, a size — cannot be added without that test being edited, which is where
// "is the decision layer's output growing a second channel" gets asked out loud.
//
// One status and never two: the arms are ordered and the first match returns, so a plan cannot
// be both TOO_EXTENDED and WAIT_PULLBACK even though a careless set of independent `if`s would
// let it be. Reasons may be several — a status usually has a price half and a permission half —
// and there is always at least one.
//
// It carries NO PRICE. Every number a reader needs is already on the Plan, and a decision layer
// that returned one would be publishing a second copy of a number somebody else computed.
type DecisionResult struct {
	Status  EntryStatus  `json:"status"`
	Rule    DecisionRule `json:"rule"`
	Reasons []Reason     `json:"reasons"`
}

// requirementVerdict is EP-5's reading of one Requirement.
type requirementVerdict string

const (
	// reqDischarged — the condition is TRUE here, and this layer can see that it is.
	reqDischarged requirementVerdict = "DISCHARGED"
	// reqViolated — the condition is FALSE here.
	reqViolated requirementVerdict = "VIOLATED"
	// reqNotEvaluated — this layer cannot tell. NOT "satisfied by default".
	reqNotEvaluated requirementVerdict = "NOT_EVALUATED"
)

// evaluateRequirement reads one Requirement against the only fact this layer has about the
// entry: whether the market is currently inside the published band.
//
// # Two of the three are NOT EVALUATED, and that is the honest answer
//
// TODO(the item that grades setups / the item that classifies support levels): supply the two
// evaluations below. Recorded in code rather than in a review thread because a follow-up that
// lives only in a thread is a follow-up nobody is holding.
//
//	NEAR_SUPPORT       needs a judgement about WHICH level counts as support, and the zone's
//	                   centre is not one by construction: with no consolidation base the
//	                   scanner puts today's single-bar low in BaseLow (see BaseLowKind), and a
//	                   band centred on that is not "at a structural support level". Deciding it
//	                   here would be this package inventing a support taxonomy in the file
//	                   whose whole premise is that it invents nothing.
//	ELEVATED_EVIDENCE  is a setup SCORE. entryplan cannot see one — it may not import
//	                   internal/scanner (the architecture test enforces it) and no field on
//	                   DecisionInput carries a grade.
//
// The consequence is stated plainly at ReasonStatusRequirementNotEvaluated: SIDEWAYS and
// DISTRIBUTION cannot produce BUY_NOW under EP5-v1. That is a true statement about a missing
// evaluator, and it is confined to the at-the-market arms — WAIT_PULLBACK and WAIT_BREAKOUT do
// not claim the entry may be taken now, so nothing gates them on it.
//
// THERE IS NO default: CASE. An unrecognised requirement answers NOT_EVALUATED explicitly, so a
// Requirement added later is refused rather than silently satisfied — the failure direction that
// costs nothing to be wrong in.
func evaluateRequirement(r Requirement, insideZone bool) requirementVerdict {
	switch r {
	case RequirementInsideZone:
		// The ONE this layer can discharge, and it discharges it from the arm's own condition
		// rather than from a second computation: "the entry must sit inside the established
		// entry zone" is true exactly when the price we are deciding about is in the band.
		if insideZone {
			return reqDischarged
		}
		return reqViolated
	case RequirementNearSupport, RequirementElevatedEvidence:
		return reqNotEvaluated
	}
	return reqNotEvaluated
}

// resolveExecution folds a permission and its requirements into ONE allowance, plus the code
// that explains it.
//
// The fold is deliberately pessimistic in the two directions that matter: a DISALLOWED
// permission stays DISALLOWED whatever its requirements say (a condition on a refusal reads as
// "satisfy this and it is fine", which the policy tests already refuse to let EP-2 write), and a
// requirement this layer cannot evaluate turns an ALLOWED permission into UNRESOLVED rather than
// leaving it ALLOWED. The second is the whole point: an unchecked condition must produce a
// STATE, never a permission.
//
// perms are folded together — the permission for the entry SHAPE, the permission to EXECUTE at
// the market, and (above the band) the permission to PAY UP — because all of them must hold
// before anything is bought, and their requirement lists are separate halves of one sentence
// (SIDEWAYS: the shape must be near support AND buying at the market must be near support).
//
// insideZone is passed by the ARM, not recomputed here: it is the arm's own condition, and a
// second `zone.Low <= cur` inside this function would be a copy that can disagree with the one
// that chose the arm. S10 passes true, S14 passes false — and false is what makes
// RequirementInsideZone falsifiable at all.
func resolveExecution(insideZone bool, perms ...Permission) (Allowance, Reason) {
	out := AllowanceAllowed
	reason := Reason("")

	for _, p := range perms {
		switch p.Allowance {
		case AllowanceDisallowed:
			// A refusal ends it: nothing below can lift a DISALLOWED back up.
			return AllowanceDisallowed, ReasonStatusImmediateEntryRefused
		case AllowanceAllowed:
		default:
			if out == AllowanceAllowed {
				out, reason = AllowanceUnresolved, ReasonStatusImmediateEntryUnresolved
			}
		}
	}
	for _, p := range perms {
		for _, req := range p.Requirements {
			switch evaluateRequirement(req, insideZone) {
			case reqDischarged:
			case reqViolated:
				return AllowanceDisallowed, ReasonStatusRequirementNotMet
			default:
				if out == AllowanceAllowed {
					out, reason = AllowanceUnresolved, ReasonStatusRequirementNotEvaluated
				}
			}
		}
	}
	return out, reason
}

// foldThesis folds an UNCLASSIFIED thesis standing (EP-6D) into an at-the-market allowance.
//
// Called on EXACTLY the two arms whose ALLOWED outcome is BUY_NOW (S10 and S14), after the
// policy permissions have been folded, and it acts ONLY on an ALLOWED allowance — so for an
// unclassified standing the one thing it can change is a BUY_NOW that was about to be
// published:
//
//	not ALLOWED                       unchanged, with its own code.
//	ALLOWED + NOT_CONTRADICTED        unchanged → BUY_NOW.
//	ALLOWED + anything else           UNRESOLVED + STATUS_THESIS_UNCLASSIFIED → INSUFFICIENT_DATA.
//	                                  MISSING ≠ ZERO: nobody said the thesis stands.
//
// A CONTRADICTED standing never reaches this function: S1 answered it. It is deliberately NOT
// given its own case here — if S1 were ever moved below these arms, a contradicted standing
// would fall into "anything else" and still never be BUY_NOW, which is the safe direction, and
// TestTheThesisStandingMatrix would go red on the missing NO_VALID_ENTRY.
//
// THERE IS NO default: THAT PERMITS. The only standing that leaves ALLOWED intact is the one
// declared constant.
func foldThesis(a Allowance, why Reason, t ThesisStanding) (Allowance, Reason) {
	if a != AllowanceAllowed || t == ThesisNotContradicted {
		return a, why
	}
	return AllowanceUnresolved, ReasonStatusThesisUnclassified
}

// DecideStatus is EP-5: one Plan's evidence in, ONE EntryStatus out.
//
// PURE and TOTAL. Defined for every input including the zero DecisionInput, no clock, no I/O, no
// randomness, and it never retains or writes through a pointer from its argument. Same input,
// same status, every time — which is what makes an archived verdict re-derivable.
//
// ORDERED, FIRST MATCH WINS. The arms below are the specification, not an implementation detail
// of it: see the DecisionRule block for the three orderings that are load-bearing and for what
// each plausible reordering would get wrong.
func DecideStatus(in DecisionInput) DecisionResult {
	answer := func(r DecisionRule, s EntryStatus, reasons ...Reason) DecisionResult {
		return DecisionResult{Status: s, Rule: r, Reasons: reasons}
	}

	// ── S0. IS THERE AN ENTRY SHAPE TO DECIDE ABOUT ───────────────────────────────────
	//
	// FIRST, because every arm below is a statement about a PULLBACK or a BREAKOUT and neither
	// is on the table until the scanner said which. UNKNOWN is a STATE — it must never fall
	// through to a verdict, for the reason DecideRegime's R0 gives about SIDEWAYS.
	if !in.Semantic.Resolved() {
		return answer(RuleSemanticUnresolved, StatusInsufficientData,
			ReasonStatusSemanticUnresolved)
	}

	// ── S1. DOES THE SCANNER CONTRADICT THE THESIS (EP-6D) ────────────────────────────
	//
	// The user's rule: a stock whose primary Action is SELL, REDUCE, TAKE PROFIT or STOP LOSS
	// shows no entry — not BUY_NOW, not a wait, not "too extended". A VERDICT: the evidence is
	// present and says no, whatever the price, the policy or the band. ComputePlan withholds
	// every executable price for a plan decided here. See the DecisionRule block for why this
	// sits after S0 and ahead of everything else. An UNCLASSIFIED standing is NOT handled here —
	// it only ever removes BUY_NOW (foldThesis).
	if in.Thesis == ThesisContradicted {
		return answer(RuleThesisContradicted, StatusNoValidEntry, ReasonStatusThesisContradicted)
	}

	// ── S2. IS THERE A MARKET TO COMPARE AGAINST ──────────────────────────────────────
	//
	// The current price has NO SUBSTITUTE here, exactly as it has none in ComputePlan. Every
	// remaining arm except the two policy ones is a comparison against it.
	cur, ok := usablePrice(in.CurrentPrice)
	if !ok {
		return answer(RuleCurrentPriceUnavailable, StatusInsufficientData,
			ReasonStatusCurrentPriceUnavailable)
	}

	// ── S3 / S4. THE POLICY, AND THE LINE BETWEEN THE TWO WAYS OF SAYING NO ───────────
	//
	// AHEAD OF EVERY PRICE ARM. See the DecisionRule block: BEAR with a mathematically valid
	// zone is NO_VALID_ENTRY and never TOO_EXTENDED, because TOO_EXTENDED implies a lower
	// price at which this would be bought, and there is none.
	switch in.Policy.Allowance {
	case AllowanceAllowed:
	case AllowanceDisallowed:
		return answer(RulePolicyRefused, StatusNoValidEntry, ReasonStatusPolicyRefused)
	default:
		// UNRESOLVED, or a field nobody filled in. NO DECISION WAS MADE — the UNKNOWN regime,
		// or a regime string that is not a regime. A STATE.
		return answer(RulePolicyUnresolved, StatusInsufficientData, ReasonStatusPolicyUnresolved)
	}

	// ── S5 / S6. THE ENTRY BAND ───────────────────────────────────────────────────────
	//
	// NEVER `zone == nil → NO_VALID_ENTRY`. The code that explains the nil is what decides,
	// through ReadAbsence: a missing ATR is a missing dependency and a support that sits above
	// the market is a finding. Both leave the same nil pointer behind.
	if in.Zone == nil {
		if ReadAbsence(in.ZoneAbsence) == AbsenceSearched {
			return answer(RuleNoLegalZone, StatusNoValidEntry, ReasonStatusNoLegalEntryZone)
		}
		return answer(RuleZoneUnavailable, StatusInsufficientData,
			ReasonStatusEntryZoneUnavailable)
	}
	zone := *in.Zone

	// ── S7 / S8. THE RISK BOUNDARY ────────────────────────────────────────────────────
	//
	// AHEAD OF EVERY PRICE ARM, and this is the arm most likely to be argued down to a caveat.
	// It should not be: the plan knows where it might buy and cannot say where it would be
	// proved wrong, and where the market happens to be standing does not repair that. It is
	// also why the two codes had to be split upstream — see classifyStopUnavailability.
	if in.Invalidation == nil {
		if ReadAbsence(in.StopAbsence) == AbsenceSearched {
			return answer(RuleNoRiskBoundary, StatusNoValidEntry, ReasonStatusNoRiskBoundary)
		}
		return answer(RuleRiskBoundaryUnavailable, StatusInsufficientData,
			ReasonStatusRiskBoundaryUnavailable)
	}

	// ── S9. TOO_EXTENDED ──────────────────────────────────────────────────────────────
	//
	// The ONLY arm that produces it, and its condition is entirely numeric: a ceiling exists
	// and the market is STRICTLY above it. Equality is the ceiling doing its job, not a
	// breach — "the highest price the plan permits paying" is a price the plan permits paying.
	//
	// THE PLAN KEEPS EVERYTHING. Nothing here withdraws the zone, the ceiling, the stop or the
	// targets: the whole content of TOO_EXTENDED is "here, no — there, yes", and a reader shown
	// only the word has been told the useless half of it.
	//
	// hasCeiling is read ONCE, here, and every arm below uses that one reading. usablePrice
	// rather than `!= nil`, because a ceiling that is NaN or non-positive is not a ceiling: NaN
	// would make `cur > ceiling` false and so DISABLE this guard rather than trip it, and the
	// arms below would then treat a garbage number as an authorisation to pay.
	ceiling, hasCeiling := usablePrice(in.MaxChasePrice)
	if hasCeiling && cur > ceiling {
		return answer(RuleAboveMaxChase, StatusTooExtended, ReasonStatusAboveMaxChase)
	}

	// ── S10 / S11 / S12. THE MARKET IS INSIDE THE BAND ─────────────────────────────────
	//
	// BOUNDS INCLUSIVE. zone.Low and zone.High are the cheapest and the dearest price this plan
	// calls an acceptable fill, and a limit order at either is a legal order; making the band
	// exclusive would refuse the exact prices the band was published to name.
	if zone.Low <= cur && cur <= zone.High {
		allowance, why := resolveExecution(true, buyNowPermission(in.Policy), semanticPermission(in.Policy))
		allowance, why = foldThesis(allowance, why, in.Thesis)
		switch allowance {
		case AllowanceAllowed:
			return answer(RuleInsideZone, StatusBuyNow, ReasonStatusPriceInsideZone)
		case AllowanceDisallowed:
			// The shape is permitted and executing at the market here is not. There is nothing
			// left to wait FOR — the market is already at the entry — so this is a verdict.
			return answer(RuleInsideZoneRefused, StatusNoValidEntry, ReasonStatusPriceInsideZone, why)
		default:
			return answer(RuleInsideZoneUndecided, StatusInsufficientData,
				ReasonStatusPriceInsideZone, why)
		}
	}

	above := cur > zone.High

	// ── S13. ABOVE THE BAND WITH NO CEILING, AND THE CEILING IS WHAT DECIDES ──────────
	//
	// The three-way read of a missing MaxChasePrice, and the reason `if MaxChasePrice == nil`
	// alone is not allowed to be written anywhere in this file:
	//
	//	ABSENT BY DECISION   the policy refused to chase, or today's band leaves no headroom.
	//	                     "You may not chase" is not "you may not wait": fall through. A
	//	                     PULLBACK reaches S17 and answers WAIT_PULLBACK; a BREAKOUT whose
	//	                     level has given way reaches S16 and answers NO_VALID_ENTRY, because
	//	                     there is nothing left to wait for and no authorised price above the
	//	                     band. (Before EP-6D's renumber this comment said "S15 answers
	//	                     WAIT_PULLBACK" — the ids are positional and it did not survive the
	//	                     renumbering S15 introduced.)
	//	EVIDENCE GAP         the previous close is missing, the basis is not RAW, the limit rule
	//	                     is unknown, or the chase policy itself is unresolved. Then "merely
	//	                     above the band" and "past anything this plan would pay" are both
	//	                     consistent with what is known, and choosing between them would be a
	//	                     guess wearing a verdict's output field.
	//
	// It applies ONLY above the band. Inside it, zone membership is the whole answer and a
	// missing ceiling changes nothing — see S10, which sits above this arm for that reason.
	if above && !hasCeiling && ReadAbsence(in.ChaseAbsence) != AbsenceByDecision {
		return answer(RuleChaseCeilingUnresolved, StatusInsufficientData,
			ReasonStatusPriceAboveEntryZone, ReasonStatusChaseCeilingUnresolved)
	}

	// ── S14 / S15 / S16. THE BREAKOUT THAT RAN, AND THE PERMISSION TO PAY UP ──────────
	//
	// Reached only when the market has not crossed the ceiling (S9 sent that away) and the
	// ceiling's absence, if it is absent, is a DECISION (S13 sent every other reading away). So
	// what is left is either the band between zone.High and the ceiling — the chase range — or a
	// plan that has been told it may not pay above its band at all.
	//
	// BREAKOUT ONLY, and the asymmetry is the point. A breakout plan's instruction was "buy
	// when the level gives way"; it gave way, and the fill was missed by less than one chase
	// tolerance, so paying up CONTINUES that instruction. A pullback plan's instruction is
	// "wait for price to come back to the level", and a market above the band is that
	// instruction not yet satisfied — reading it as a chase would delete WAIT_PULLBACK for
	// every plan whose market sits within one chase tolerance of its own support, which is
	// most of them.
	//
	// The permission is ChasePolicy.Immediate and NOT "MaxChasePrice exists". A ceiling bounds
	// what may be paid; it does not authorise paying it today. BULL_PULLBACK is the row that
	// proves they are different — a 0.4×ATR ceiling and DISALLOWED immediate entry.
	//
	// ALL THREE PERMISSIONS ARE FOLDED, not just Immediate, and `insideZone` is FALSE — which is
	// the fact this arm exists because of. This arm publishes BUY_NOW, so the six conditions at
	// the top of this file apply to it in full, and condition 5 is "the policy permits EXECUTING
	// AT THE MARKET here, INCLUDING EVERY REQUIREMENT it attached to that permission". Immediate
	// alone answers only the "above the band" half; BuyNow is the permission that governs
	// executing at the market at all, and the entry SHAPE's permission carries the requirements
	// EP-2 attached to the shape. A policy whose BuyNow says INSIDE_VALID_ZONE — BULL_PULLBACK's
	// row says exactly that — is refused HERE, by the requirement it wrote down, rather than by
	// a special case somebody would have to remember to add.
	if above && in.Semantic == EntrySemanticBreakout {
		if !hasCeiling {
			// NO CEILING AT ALL, and S13 has already sent away every reading of that absence
			// except ABSENT BY DECISION — so what is left is a plan that MAY NOT pay above its
			// band ("you may not chase") or one whose legal ceiling does not reach the top of
			// its own band ("there is no headroom"). Either way there is no authorised price
			// above the zone, and BUY_NOW here would be the fourth condition at the top of this
			// file — "the ceiling has not been crossed" — asserted about a ceiling that does not
			// exist. It is also the exact shape InvariantBuyNowPriceAuthorised was written to
			// catch, and a decision layer whose output the gate has to repair is a decision
			// layer that decided wrong.
			return answer(RuleImmediateChaseRefused, StatusNoValidEntry,
				ReasonStatusPriceAboveEntryZone, ReasonStatusImmediateEntryRefused)
		}
		allowance, why := resolveExecution(false, buyNowPermission(in.Policy),
			semanticPermission(in.Policy), immediatePermission(in.Policy))
		allowance, why = foldThesis(allowance, why, in.Thesis)
		switch allowance {
		case AllowanceAllowed:
			return answer(RuleImmediateChase, StatusBuyNow, ReasonStatusImmediateChasePermitted)
		case AllowanceUnresolved:
			return answer(RuleImmediateChaseUndecided, StatusInsufficientData,
				ReasonStatusPriceAboveEntryZone, why)
		}
		// S16. REFUSED, and it is a VERDICT with the refusal's own code on it.
		//
		// It is NOT WAIT_BREAKOUT — the level has already given way, so there is nothing left
		// to wait for — and it is NOT TOO_EXTENDED either, because the market is inside the
		// ceiling and TOO_EXTENDED's whole content is that it is not.
		//
		// It is also not left to fall through to S19. `why` says which permission refused and
		// S19's code says the market stands in no relation any arm calls executable, which is
		// FALSE here: the relation was recognised, priced and refused. Dropping `why` on the
		// floor would have made STATUS_IMMEDIATE_ENTRY_REFUSED and STATUS_REQUIREMENT_NOT_MET
		// codes this package computes and never publishes.
		return answer(RuleImmediateChaseRefused, StatusNoValidEntry,
			ReasonStatusPriceAboveEntryZone, why)
	}

	// ── S17. WAIT_PULLBACK ────────────────────────────────────────────────────────────
	//
	// The legal entry sits BELOW the market. The execution-side reading of
	// scanner.ActPullbackBuy and of 「拉回 5/10 日線量縮承接」.
	//
	// NOT GATED ON THE CEILING. A missing MaxChasePrice under a policy that refuses to chase
	// leaves this answer completely intact, and merging the two permissions would be the
	// package's own three-nils argument thrown away at the last step.
	//
	// THE SEMANTIC TEST IS REDUNDANT AT THIS POSITION, AND IT STAYS. S14/S15/S16 return for
	// EVERY `above && BREAKOUT`, so by the time control reaches here `above` already implies
	// PULLBACK and `&& in.Semantic == EntrySemanticPullback` cannot change an answer — a
	// mutation that deletes it survives the suite, and it survives it because it is EQUIVALENT,
	// not because nothing is watching.
	//
	// It is written anyway for the reason every redundant guard in this package is: the
	// redundancy is a property of the ORDER, and the order is the one thing about this table
	// that a later edit changes. Hoist this arm one block up and the gate becomes load-bearing
	// immediately — every breakout that has run would be published as "wait for the pullback".
	// TestTheDecisionPrecedenceIsTheSpecifiedOrder pins that dependency directly, with a row
	// that hands both arms an input they both match.
	if above && in.Semantic == EntrySemanticPullback {
		return answer(RuleWaitPullback, StatusWaitPullback, ReasonStatusPriceAboveEntryZone)
	}

	// ── S18. WAIT_BREAKOUT ────────────────────────────────────────────────────────────
	//
	// The legal entry sits ABOVE the market, on confirmation: the breakout level has not been
	// reached. The execution-side reading of scanner.ActBreakoutBuy.
	//
	// "The price is not in the zone" is NOT a reason to refuse a plan — it is the NORMAL state
	// of a breakout that has not triggered, and answering NO_VALID_ENTRY here would delete
	// every pending breakout in the watchlist.
	if !above && in.Semantic == EntrySemanticBreakout {
		return answer(RuleWaitBreakout, StatusWaitBreakout, ReasonStatusPriceBelowEntryZone)
	}

	// ── S19. THE FALLBACK, AND IT IS A VERDICT ────────────────────────────────────────
	//
	// Everything a judgement needs was present — a shape, a permitted policy, a band, a risk
	// boundary and a market price — and the market stands in no relation to the band that any
	// arm calls executable or waitable.
	//
	// ONE live shape reaches it, and naming it is what keeps the arm from becoming scenery: a
	// PULLBACK whose support band the market has ALREADY FALLEN THROUGH. There is no "wait" for
	// a level price is below, and there is nothing to buy at a band the market left downward.
	// (The other shape that used to arrive here — a refused breakout chase — has its own arm at
	// S16 now, because the refusal's cause is a fact worth publishing.)
	//
	// The arm is still written as an unconditional fallback rather than as `if !above &&
	// semantic == PULLBACK`, because DecideStatus is TOTAL: an EntrySemantic that passes
	// Resolved() today is one of two values, and an arm that assumes that would be a silent
	// fall-off-the-end the day a third is added.
	return answer(RuleNoExecutableRelation, StatusNoValidEntry,
		ReasonStatusNoExecutablePriceRelation)
}

// usablePrice unwraps an optional price and refuses the values that are not prices.
//
// A NaN would make every comparison below FALSE — so a NaN current price would not trip the
// guards, it would DISABLE them — and a zero or negative one is not a cautious default but a
// price nothing trades at. MISSING ≠ ZERO, one more time.
func usablePrice(p *float64) (float64, bool) {
	if p == nil || !finitePositive(*p) {
		return 0, false
	}
	return *p, true
}

// buyNowPermission is the regime's permission to EXECUTE AT THE MARKET, or an UNRESOLVED one
// when there is no policy at all.
//
// A nil Policy means the regime string was not a regime (see PolicyResult's three-way table).
// UNRESOLVED and not DISALLOWED: nobody decided.
func buyNowPermission(r PolicyResult) Permission {
	if r.Policy == nil {
		return Permission{Allowance: AllowanceUnresolved}
	}
	return r.Policy.BuyNow
}

// immediatePermission is the regime's permission to pay ABOVE the entry band at the market.
//
// EP-2's ChasePolicy.Immediate, read straight. It is NOT inferred from MaxATR or from the
// existence of a ceiling: see ChasePolicy on why the price bound and the execution permission
// are two fields.
func immediatePermission(r PolicyResult) Permission {
	if r.Policy == nil {
		return Permission{Allowance: AllowanceUnresolved}
	}
	return r.Policy.Chase.Immediate
}

// semanticPermission is what the policy said about the entry SHAPE, rebuilt from the
// PolicyResult the caller was handed.
//
// It carries the requirements EP-2 attached to that shape, which is why it is folded into the
// at-the-market arms alongside BuyNow: SIDEWAYS conditions BOTH on NEAR_SUPPORT, and dropping
// either half would publish BUY_NOW with a stated condition unchecked.
func semanticPermission(r PolicyResult) Permission {
	return Permission{Allowance: r.Allowance, Requirements: r.Requirements}
}
