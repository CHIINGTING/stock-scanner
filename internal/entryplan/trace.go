package entryplan

import "github.com/deep-huang/stock-scanner/internal/pricerule"

// ── EP-3b: the work record behind an actionable price ─────────────────────────────────
//
// EP-1 and EP-2 could explain themselves with the evidence census alone, because their output
// was a word. EP-3b's output is a NUMBER a reader can place an order at, and a number is not
// explained by naming the inputs it came from — it is explained by showing the arithmetic.
//
// So every published price carries the terms it was built from: which candidate levels
// existed, what happened to each one, which level won, the ATR, the tick, both terms of the
// max() that fixed the width, the raw bounds before alignment, the aligned bounds after it,
// the previous close, the legal ceiling, whether the clamp bit, and which way each price was
// rounded. Given the trace and this package's source, every number on the plan can be
// recomputed by hand.
//
// # Why a typed struct and not twenty more evidence rows
//
// The evidence census (see plan.go) answers "what were we HANDED". These fields answer "what
// did the rule DO with it", and they are mostly not observations at all — a tick floor width
// is not evidence about a company. Encoding them as Evidence rows would also mean encoding
// numbers as `*float64` behind a string key, which is exactly the shape in which a unit gets
// lost.
//
// # THE TRACE IS NOT AN ORDER TICKET
//
// It deliberately contains numbers that are NOT prices: a raw bound before tick alignment, a
// centre that is not on the grid, and — when the zone was refused for it — a raw low that is
// not positive. That is the point: the refused arithmetic is the part a reader needs in order
// to see WHY there is no zone. Only Plan.IdealEntry and Plan.MaxChasePrice are executable, and
// the invariant checker enforces the difference: the geometry invariants apply to PriceZone
// values, and no PriceZone is ever put in here.

// TickDirection is which way a price was moved onto the tick grid. A stable code, persisted.
//
// It exists because "104.5" does not say whether the rule rounded a 104.8 down or a 104.3 up,
// and for a CEILING those two are different rules — one of them can publish a legal price
// above the ceiling it was computing.
type TickDirection string

const (
	// TickDown — the result is never above the input. For a ceiling (MaxChasePrice) this is
	// the only defensible direction.
	TickDown TickDirection = "DOWN"
	// TickUp — the result is never below the input.
	TickUp TickDirection = "UP"
	// TickNearest — the closer grid point. Not used by any rule in this package; carried so
	// pricerule's three directions map onto three codes rather than two and a silence.
	TickNearest TickDirection = "NEAREST"
)

// AllTickDirections is the registry, checked from both ends by the tick tests.
var AllTickDirections = []TickDirection{TickDown, TickUp, TickNearest}

// tickDirection labels a pricerule.RoundDir.
//
// It maps the three declared constants and returns "" for anything else, so a direction this
// package cannot name is recorded as unnamed rather than mislabelled as NEAREST.
// TestTickDirectionLabelsMatchPriceRule pins each label against pricerule's own String().
func tickDirection(d pricerule.RoundDir) TickDirection {
	switch d {
	case pricerule.RoundDown:
		return TickDown
	case pricerule.RoundUp:
		return TickUp
	case pricerule.RoundNearest:
		return TickNearest
	}
	return ""
}

// TickDirections is the pair of directions a zone's two bounds were aligned with.
type TickDirections struct {
	Low  TickDirection `json:"low,omitempty"`
	High TickDirection `json:"high,omitempty"`
}

// ClampOutcome says whether the daily price limit bit.
type ClampOutcome string

const (
	// ClampNotApplied — the computed chase limit was already inside the legal band.
	ClampNotApplied ClampOutcome = "NOT_CLAMPED"
	// ClampToLimitUp — the computed chase limit was above the day's highest legal price and
	// was replaced by it.
	ClampToLimitUp ClampOutcome = "CLAMPED_TO_LIMIT_UP"
)

// AllClampOutcomes is the registry, checked from both ends by the chase tests.
var AllClampOutcomes = []ClampOutcome{ClampNotApplied, ClampToLimitUp}

// limitAssumption is the caller-contract disclosure that travels with every clamp.
//
// pricerule.LimitUpPrice CANNOT verify either half of its own contract — it is pure, with no
// calendar, no corporate-action feed and no listing history — and neither can this package.
// So the assumption is stated as a code on the trace and as prose in the caveats, because a
// ceiling computed on an ex-dividend day from a raw previous close is wrong by roughly the
// dividend and looks exactly like a right one.
const limitAssumption = "ASSUMES_ORDINARY_SESSION_AND_UNADJUSTED_REFERENCE_PRICE"

// InvariantCheckOutcome is what the production invariant gate did to this plan.
type InvariantCheckOutcome string

const (
	// InvariantCheckClean — the plan satisfied every invariant in AllPlanInvariants.
	InvariantCheckClean InvariantCheckOutcome = "CLEAN"
	// InvariantCheckPricesWithdrawn — the plan broke at least one invariant and its
	// executable prices were removed before it was returned. See EnforcePlanInvariants.
	InvariantCheckPricesWithdrawn InvariantCheckOutcome = "PRICES_WITHDRAWN"

	// InvariantCheckStatusWithdrawn — the gate fired, and what it took was the VERDICT rather
	// than a price: one of EP-5's status invariants was broken, so Plan.Status went back to
	// INSUFFICIENT_DATA and every number survived.
	//
	// A THIRD value rather than a reuse of PRICES_WITHDRAWN, because the two outcomes tell a
	// reader to look in different places — one says the arithmetic produced an illegal price,
	// the other says the decision contradicted a legal one — and a stamp that could mean either
	// is a stamp nobody can act on. WithdrawnFields is empty for this outcome, by construction.
	InvariantCheckStatusWithdrawn InvariantCheckOutcome = "STATUS_WITHDRAWN"

	// InvariantCheckPricesAndStatusWithdrawn — both an executable price and the verdict
	// contradicted the plan's invariants. A distinct persisted value prevents either half of
	// the repair from disappearing when historical plans are classified by this field.
	InvariantCheckPricesAndStatusWithdrawn InvariantCheckOutcome = "PRICES_AND_STATUS_WITHDRAWN"
)

// AllInvariantCheckOutcomes is the registry, checked from both ends by the enforcement tests.
var AllInvariantCheckOutcomes = []InvariantCheckOutcome{
	InvariantCheckClean,
	InvariantCheckPricesWithdrawn,
	InvariantCheckStatusWithdrawn,
	InvariantCheckPricesAndStatusWithdrawn,
}

// PolicyTrace is what the regime's entry policy said, copied onto the plan.
//
// Copied rather than referenced: PolicyResult carries a *EntryPolicy, and a plan holding that
// pointer would change if a caller wrote through it.
type PolicyTrace struct {
	// Name is the policy that applied, or "" when the regime is not a model.Regime value —
	// which is not the same as PolicyUnresolved, the policy the UNKNOWN regime does have.
	Name PolicyName `json:"name,omitempty"`
	// SemanticAllowance is what the policy said about THIS entry semantic.
	SemanticAllowance Allowance `json:"semantic_allowance,omitempty"`
	// Requirements are the further conditions an ALLOWED entry must satisfy. EP-3b EVALUATES
	// NONE OF THEM — each is a statement about a price relative to a level, and turning them
	// into a status is EP-5's step. They are carried so EP-5 inherits the condition instead
	// of re-deriving it.
	Requirements []Requirement `json:"requirements,omitempty"`
	// ChaseAllowance is the policy's separate answer about chasing, which is NOT the same
	// field as SemanticAllowance: SIDEWAYS permits a pullback entry and permits no chase.
	ChaseAllowance Allowance `json:"chase_allowance,omitempty"`
	// ChaseMaxATR is the regime's chase tolerance in ATR units. Nil unless ChaseAllowance is
	// ALLOWED — the invariant ChasePolicy states.
	ChaseMaxATR *float64 `json:"chase_max_atr,omitempty"`
}

// ZoneTrace is the arithmetic behind IdealEntry, or behind its absence.
type ZoneTrace struct {
	// Semantic and PriceBasis are the two facts every other number here is relative to.
	Semantic   EntrySemantic `json:"semantic,omitempty"`
	PriceBasis PriceBasis    `json:"price_basis,omitempty"`

	// Candidates is every level source, in AllLevelSources order, with its value and its
	// disposition. ALWAYS the full width, so a missing row cannot be read as "the rule did
	// not consider it".
	Candidates []CandidateLevel `json:"candidates,omitempty"`

	// CenterSource and Center are the level the zone was centred on. Center is NOT
	// necessarily on the tick grid: it is an observed indicator value, not a quote.
	CenterSource LevelSource `json:"center_source,omitempty"`
	Center       *float64    `json:"center,omitempty"`

	// The width terms. ATR and ATRPeriod are recorded even when the width was refused for
	// them — "the ATR was 0" and "there was no ATR" are different rows.
	ATR              *float64         `json:"atr,omitempty"`
	ATRPeriod        int              `json:"atr_period,omitempty"`
	ATRHalfWidth     *float64         `json:"atr_half_width,omitempty"`
	TickSize         *float64         `json:"tick_size,omitempty"`
	TickFloorWidth   *float64         `json:"tick_floor_width,omitempty"`
	HalfWidth        *float64         `json:"half_width,omitempty"`
	HalfWidthBinding HalfWidthBinding `json:"half_width_binding,omitempty"`
	WidthRejection   WidthRejection   `json:"width_rejection,omitempty"`

	// RawLow and RawHigh are the bounds BEFORE tick alignment. Recorded for every zone,
	// including a refused one — see the file comment: a raw low that is not positive is
	// exactly the number a reader needs in order to understand the refusal.
	RawLow  *float64 `json:"raw_low,omitempty"`
	RawHigh *float64 `json:"raw_high,omitempty"`

	// TickDirections is which way each bound was aligned.
	TickDirections TickDirections `json:"tick_directions,omitempty"`

	// Low and High are the ALIGNED bounds — the same numbers as IdealEntry.Low/High, present
	// only when a zone was published.
	Low  *float64 `json:"low,omitempty"`
	High *float64 `json:"high,omitempty"`

	// OverlapsCurrentPrice reports that a PULLBACK zone's high sits at or above the last
	// price. A FLAG, not a decision: the zone is published unchanged (see ComputeIdealEntryZone) and
	// whether it means BUY_NOW or TOO_EXTENDED is EP-5's to say.
	OverlapsCurrentPrice bool `json:"overlaps_current_price,omitempty"`

	// Rejection is why there is no zone. Non-empty exactly when ZoneResult.Zone is nil.
	Rejection Reason `json:"rejection,omitempty"`
}

// ChaseTrace is the arithmetic behind MaxChasePrice, or behind its absence.
type ChaseTrace struct {
	// ZoneHigh, MaxATR and ATR are the three terms of the raw limit.
	ZoneHigh *float64 `json:"zone_high,omitempty"`
	MaxATR   *float64 `json:"max_atr,omitempty"`
	ATR      *float64 `json:"atr,omitempty"`
	// Raw is ZoneHigh + MaxATR × ATR, before any clamp and before alignment.
	Raw *float64 `json:"raw,omitempty"`

	// PreviousClose is what the legal ceiling was computed FROM. Never the current price:
	// see Snapshot.PreviousClose.
	PreviousClose *float64 `json:"previous_close,omitempty"`
	// LimitRule is pricerule.ClassifyLimitRule's verdict on the SHAPE OF THE CODE. UNKNOWN
	// is a real answer for the "00" ETF block and it refuses the chase; see ComputeMaxChase.
	LimitRule pricerule.LimitRule `json:"limit_rule,omitempty"`
	// LimitUp is the day's highest legal price, tick-aligned by pricerule.
	LimitUp *float64 `json:"limit_up,omitempty"`
	// Clamp says whether the ceiling bit.
	Clamp ClampOutcome `json:"clamp,omitempty"`
	// LimitAssumption is the caller-contract disclosure that travels with any clamp. See
	// limitAssumption.
	LimitAssumption string `json:"limit_assumption,omitempty"`

	// TickDirection is which way the final price was aligned. DOWN, always, for a ceiling.
	TickDirection TickDirection `json:"tick_direction,omitempty"`
	// Final is the published MaxChasePrice.
	Final *float64 `json:"final,omitempty"`

	// Rejection is why there is no chase limit.
	//
	// Empty in exactly ONE non-published case: chasing was permitted and there was no zone
	// to chase into, in which case ZoneTrace.Rejection is the answer and repeating it here
	// would invent a second cause. Every other absence has its own code, and the three EP-2
	// kept apart stay apart — CHASE_NOT_ALLOWED (a decision), CHASE_POLICY_UNRESOLVED (no
	// decision), PREVIOUS_CLOSE_UNAVAILABLE (a missing input).
	Rejection Reason `json:"rejection,omitempty"`
}

// StopTrace is the arithmetic behind Invalidation, or behind its absence.
type StopTrace struct {
	// Semantic and PriceBasis are the two facts every other number here is relative to. The
	// basis is the ZONE's, because the invalidation is defined relative to the published
	// entry and is refused when a candidate is not on the same series.
	Semantic   EntrySemantic `json:"semantic,omitempty"`
	PriceBasis PriceBasis    `json:"price_basis,omitempty"`

	// ZoneLow is the bound every candidate had to sit strictly below.
	ZoneLow *float64 `json:"zone_low,omitempty"`

	// Candidates is every stop source, in AllStopSources order, with its value and its
	// disposition. ALWAYS the full width, so a missing row cannot be read as "the rule did
	// not consider it".
	Candidates []StopCandidate `json:"candidates,omitempty"`

	// BaseLowKind is what kind of level Snapshot.BaseLow was. Recorded on the trace as well
	// as on its evidence row, because it is the fact that decides whether the base low was a
	// candidate at all and a reader of the candidate list needs it in the same place.
	BaseLowKind BaseLowKind `json:"base_low_kind,omitempty"`

	// The fallback's two terms. ATR is recorded even when the fallback was refused for it —
	// "the ATR was 0" and "there was no ATR" are different rows — and ATRMultiple is
	// invalidationATRMultiple, carried so an archived plan does not depend on this source
	// file to be re-derived.
	ATR         *float64 `json:"atr,omitempty"`
	ATRMultiple *float64 `json:"atr_multiple,omitempty"`

	// SelectedSource and RawPrice are the candidate that won and its value BEFORE tick
	// alignment. RawPrice is not necessarily on the grid: an MA60 is an indicator value, not
	// a quote.
	SelectedSource StopSource `json:"selected_source,omitempty"`
	RawPrice       *float64   `json:"raw_price,omitempty"`

	// TickDirection is which way the level was aligned. DOWN, always; see ComputeInvalidation
	// for the order-semantics argument.
	TickDirection TickDirection `json:"tick_direction,omitempty"`
	// Price is the published Invalidation.Price.
	Price *float64 `json:"price,omitempty"`

	// Rejection is why there is no invalidation.
	//
	// Empty in exactly ONE non-published case: there was no zone for a level to be below, in
	// which case ZoneTrace.Rejection is the answer and repeating it here would invent a
	// second cause. Every other absence is NO_VALID_INVALIDATION with the per-candidate
	// dispositions above saying which condition each one failed.
	Rejection Reason `json:"rejection,omitempty"`
}

// TargetTrace is the arithmetic behind Target1, Target2 and the risk/reward, or behind their
// absence.
//
// It is the widest trace in the package on purpose: EP-4's outputs are the ones a reader is
// asked to ACT on a ratio from, and §18 of its own review requires that the entry used, the
// invalidation, the risk, both R multiples, the resistance, the valuation ceiling and the
// reward can all be recomputed BY HAND from the plan. A machine tag saying RESISTANCE_CLAMPED
// is not that; the number that clamped it is.
type TargetTrace struct {
	PriceBasis PriceBasis `json:"price_basis,omitempty"`

	// EntryUsed is IdealEntry.High — the WORST fill inside the zone, and the entry every
	// number below is measured from. NEVER the midpoint; see ComputeTargets.
	EntryUsed *float64 `json:"entry_used,omitempty"`
	// Invalidation is the published stop level the risk was measured to.
	Invalidation *float64 `json:"invalidation,omitempty"`
	// Risk is EntryUsed − Invalidation, strictly positive when present.
	Risk *float64 `json:"risk,omitempty"`
	// RiskRejection is why there is no risk denominator. Both members are unreachable from
	// ComputePlan; see RiskRejection.
	RiskRejection RiskRejection `json:"risk_rejection,omitempty"`

	// Resistance is the one structural resistance candidate and its disposition. SELECTED
	// means it clamped Target1.
	Resistance ResistanceCandidate `json:"resistance"`

	// The first target's terms: the multiple, the pure R target, the min() of that and the
	// resistance, which term won, and the aligned answer.
	Target1RMultiple *float64       `json:"target_1_r_multiple,omitempty"`
	Target1RTarget   *float64       `json:"target_1_r_target,omitempty"`
	Target1Raw       *float64       `json:"target_1_raw,omitempty"`
	Target1Binding   Target1Binding `json:"target_1_binding,omitempty"`
	Target1          *float64       `json:"target_1,omitempty"`
	Target1Rejection Reason         `json:"target_1_rejection,omitempty"`

	// The second target's terms: the multiple, the raw 3.5R level computed from the ORIGINAL
	// risk, the valuation ceiling and its outcome, the clamped value before alignment, and
	// the aligned answer.
	Target2RMultiple     *float64                `json:"target_2_r_multiple,omitempty"`
	Target2Raw           *float64                `json:"target_2_raw,omitempty"`
	ValuationTarget      *float64                `json:"valuation_target,omitempty"`
	ValuationSuitability ValuationSuitability    `json:"valuation_suitability,omitempty"`
	ValuationCeiling     ValuationCeilingOutcome `json:"valuation_ceiling,omitempty"`
	Target2Clamped       *float64                `json:"target_2_clamped,omitempty"`
	Target2              *float64                `json:"target_2,omitempty"`
	Target2Rejection     Reason                  `json:"target_2_rejection,omitempty"`

	// TickDirection is which way both targets were aligned. DOWN, always; see ComputeTargets.
	TickDirection TickDirection `json:"tick_direction,omitempty"`

	// Reward and Ratio are the risk/reward, restated here so the whole computation reads in
	// one place: Reward = Target1 − EntryUsed, Ratio = Reward / Risk.
	Reward *float64 `json:"reward,omitempty"`
	Ratio  *float64 `json:"ratio,omitempty"`
}

// DecisionTrace is the EP-5 decision's work record: what it was handed, which arm fired, and
// what it answered.
//
// It exists for the reason every other trace in this file does — a verdict a reader can act on
// cannot be explained by naming the layer that produced it — and for one more that is specific
// to a STATUS: the arms are ORDERED, so "why WAIT_PULLBACK and not TOO_EXTENDED" is answered by
// the rule id and by nothing else in the plan.
//
// Status here is what DecideStatus SAID. Plan.Status is what was PUBLISHED, and the two differ
// in exactly one case: the invariant gate found the verdict contradicting the plan's own numbers
// and withdrew it (see EnforcePlanInvariants). Keeping both is what makes that case legible
// instead of silent.
type DecisionTrace struct {
	// Semantic is the entry shape the decision was made about, copied so the trace reads
	// without the zone trace next to it.
	Semantic EntrySemantic `json:"semantic,omitempty"`
	// CurrentPrice is the market the comparisons were made against. Nil when there was none,
	// which is itself the answer for the S2 arm.
	CurrentPrice *float64 `json:"current_price,omitempty"`

	// Rule is which arm of the decision table fired. Persisted, in the shape
	// model.RegimeDecision.RuleID is: an archived verdict is only re-derivable with it.
	Rule DecisionRule `json:"rule,omitempty"`
	// Status is the arm's answer, before the invariant gate.
	Status EntryStatus `json:"status,omitempty"`
	// Reasons are the codes the arm published. Always at least one for a decided plan.
	Reasons []Reason `json:"reasons,omitempty"`

	// ZoneAbsence, ChaseAbsence and StopAbsence are the codes the decision READ to tell a
	// missing input from a completed search, and the readings it got. Recorded together
	// because the reading is the load-bearing half: NO_VALID_INVALIDATION and
	// INVALIDATION_EVIDENCE_UNAVAILABLE leave the same nil pointer and produce different
	// statuses, and a trace showing only the nil could not explain either.
	ZoneAbsence   Reason         `json:"zone_absence,omitempty"`
	ZoneReading   AbsenceReading `json:"zone_reading,omitempty"`
	ChaseAbsence  Reason         `json:"chase_absence,omitempty"`
	ChaseReading  AbsenceReading `json:"chase_reading,omitempty"`
	StopAbsence   Reason         `json:"stop_absence,omitempty"`
	StopReading   AbsenceReading `json:"stop_reading,omitempty"`
	PolicyDecided Allowance      `json:"policy_decided,omitempty"`

	// Thesis is the thesis standing the decision was handed (EP-6D), verbatim — "" when the
	// caller classified nothing. Recorded because it can decide S1, S11 and S15, and because
	// InvariantBuyNowThesisNotContradicted and InvariantThesisContradictedPublishesNoEntry read
	// it here: a plan is not a Snapshot, and this is
	// the only place on it that says what the decision was told.
	Thesis ThesisStanding `json:"thesis,omitempty"`
}

// EntryTrace is the whole work record for one plan.
//
// Present on every well-formed plan, including the ones that publish no price: "there is no
// zone and here is the candidate list that produced none" is the sentence a reader needs, and
// a nil trace would be indistinguishable from a rule that never ran.
type EntryTrace struct {
	// RuleVersion is stamped here as well as on the Plan, so a trace read out of a log or a
	// prompt in isolation still says which rule set produced it.
	RuleVersion string `json:"rule_version"`

	// AdjustmentAgeBars is fetcher.LastAdjustmentAge's answer, carried verbatim. Nil means
	// THERE IS NO ANSWER, which is not "no recent adjustment" — see AdjustmentAge.
	AdjustmentAgeBars *int `json:"adjustment_age_bars,omitempty"`
	// RecentAdjustment reports that a corporate-action adjustment is recent enough that the
	// ATR-derived width still carries some of it. NOT A GATE: see recentAdjustment.
	RecentAdjustment bool `json:"recent_adjustment,omitempty"`

	Policy PolicyTrace `json:"policy"`
	Zone   ZoneTrace   `json:"zone"`
	Chase  ChaseTrace  `json:"chase"`
	// Stop and Targets are EP-4's two steps. Present on every well-formed plan, including the
	// ones that publish no stop and no target: "there is no invalidation and here is the
	// candidate list that produced none" is the sentence a reader needs.
	Stop    StopTrace   `json:"stop"`
	Targets TargetTrace `json:"targets"`

	// Decision is EP-5's work record. Present on every well-formed plan, including the ones
	// whose status is INSUFFICIENT_DATA — "the decision ran and could not conclude" and "no
	// decision ran" are different outputs, and without this field they would be the same one.
	Decision DecisionTrace `json:"decision"`

	// InvariantCheck is what the production gate did with this plan. See
	// EnforcePlanInvariants: it is on the trace so that "the gate ran and found nothing" and
	// "the gate was removed" are different outputs.
	InvariantCheck InvariantCheckOutcome `json:"invariant_check"`
	// InvariantViolations are the distinct codes that fired, in AllPlanInvariants order.
	// Empty for every plan ComputePlan can produce; non-empty means this package computed an
	// illegal price and withdrew it.
	InvariantViolations []PlanInvariant `json:"invariant_violations,omitempty"`
	// WithdrawnFields are the executable fields the gate removed, in AllPlanPriceFields
	// (dependency) order.
	//
	// EP-4's addition, and it is what keeps the narrowed withdrawal observable: since the gate
	// no longer takes every price, PRICES_WITHDRAWN alone would not say whether a reader lost
	// an optional second target or the entry zone itself. Empty for every plan ComputePlan can
	// produce.
	WithdrawnFields []PlanPriceField `json:"withdrawn_fields,omitempty"`
}
