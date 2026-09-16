package entryplan

// RuleVersion identifies the rule-set a Plan was produced under.
//
// HEURISTIC — NOT BACKTEST-FITTED. Same discipline as valuation.QualityRuleVersion ("FU9-v1"),
// valuation.SuitabilityRuleVersion ("FU11-v1") and derivatives.FeatureVersion ("R15-v1"): a
// stored verdict with no version attached becomes uninterpretable the moment the rules move,
// because a reader cannot tell whether the stock changed or the rule did.
//
// EP1-v1 contained no thresholds at all — there was nothing yet that could have been fitted.
// EP2-v1 adds the regime → EntryPolicy table (policy.go) and the two chase MULTIPLIERS it
// carries, which are heuristics with no outcome study behind them and say so at the constants.
// Nothing in EP2-v1 computes a price from either, so an archived EP2-v1 row still contains no
// number a trader could act on — which is itself a fact worth being able to read off the row.
//
// EP-3a did NOT bump it. The invariant checker (invariants.go) added no rule, no reason code,
// no policy mapping and no status meaning: nothing about an already-archived EP2-v1 row meant
// anything different because a guard now existed.
//
// # EP3-v1 was the first bump, and it was a bump in the OUTPUT SEMANTICS
//
// EP-3b computes IdealEntry and MaxChasePrice, so an EP3-v1 row carries prices a reader can
// place an order at while an EP2-v1 row carries none. That is the difference that matters, and
// it is not "the work item number went up": the reason to bump is that the two rows answer
// DIFFERENT QUESTIONS, and a stored row with no stamp cannot say which one it answered.
//
// What else changed under EP3-v1, all of it reason-visible: twenty-three new Reason codes (the
// zone and chase steps), the six EP-3b evidence keys, the narrowed meaning of
// ENTRY_EVALUATION_NOT_IMPLEMENTED (see reasons.go — the code now means "no rule turns these
// prices into a STATUS"), and the production invariant gate that can withdraw a price.
//
// # EP4-v1 is the second bump, and it is the same KIND of change
//
// The reason is NOT that the work item number went up. It is that EP-4 adds THREE MORE
// ACTIONABLE NUMERIC SEMANTICS to a persisted row — a thesis-invalidation price, two target
// prices and a risk/reward ratio, all of them tick-aligned executable levels — so an EP3-v1
// row and an EP4-v1 row answer different questions:
//
//	EP3-v1 row   "where may this be bought, and how far may it be chased"
//	EP4-v1 row   ... "and where is the idea wrong, what is it worth, and at what ratio"
//
// A reader who compared the two without the stamp would read an EP3-v1 row's ABSENT stop as a
// market condition — "no stop could be established for this stock" — when the truth is that no
// rule existed to establish one. That is exactly the misreading the stamp prevents.
//
// WHAT AN ALREADY-ARCHIVED EP3-v1 ROW NOW MEANS: precisely what it meant when it was written —
// an entry zone, a chase ceiling, and NO risk side at all. Its missing Invalidation, Target1,
// Target2 and RiskReward are absences of a RULE, not absences of evidence, and they must not be
// compared with an EP4-v1 row's absences, which ARE evidence statements (no structural level
// qualified, no resistance above the entry, the valuation ceiling sat below the entry).
//
// What else changed under EP4-v1, all of it reason-visible: six new Reason codes plus one
// reserved (the invalidation and target steps), three new evidence keys (BASE_LOW_KIND,
// VALUATION_BASE_TARGET, VALUATION_SUITABILITY), four new plan invariants, and a NARROWED
// production withdrawal — the invariant gate now removes the violated field and its dependents
// instead of every price (see enforce.go). What did NOT change: EP-3's IdealEntryZone and
// MaxChasePrice semantics, which are byte-identical for every input; the confidence census
// (still four required and two corroborating items — see ComputePlan); and Status, which is
// still INSUFFICIENT_DATA for every input because EP-4 computes no verdict.
//
// # EP5-v1 is the third bump, and it is the FIRST ONE THAT CHANGES WHAT Status MEANS
//
// Again NOT because the work item number went up. Every EP1-, EP2-, EP3- and EP4-v1 row ever
// written carries Status = INSUFFICIENT_DATA, for every input, because no rule turned the
// evidence into a verdict. EP-5 adds that rule, so the SAME FIELD now answers a different
// question:
//
//	EP4-v1: numeric execution plan, no final EntryStatus semantics
//	EP5-v1: adds deterministic final EntryStatus decision semantics
//
// WHAT AN ALREADY-ARCHIVED EP4-v1 ROW NOW MEANS: its INSUFFICIENT_DATA is "no decision rule
// existed", not "the evidence did not support a call". The two are the same string and opposite
// statements, and the stamp is the only thing that separates them — which is exactly the
// misreading this constant exists to prevent. A reader comparing an EP4-v1 row with an EP5-v1
// row on Status alone is comparing a missing RULE with a missing FACT.
//
// What else changed under EP5-v1, all of it reason-visible: nineteen new Reason codes (the
// decision arms), ONE REMOVED (ENTRY_EVALUATION_NOT_IMPLEMENTED, whose own doc said the item
// that publishes a verdict deletes it), the SPLIT of NO_VALID_INVALIDATION into three codes so
// that "no candidate was comparable" and "every candidate was compared and none qualifies" stop
// arriving as one string, a new EP-2 policy field (ChasePolicy.Immediate — a ceiling is not a
// permission to pay it), seven new plan invariants covering the status, and the decision trace.
// What did NOT change: every price. IdealEntry, MaxChasePrice, Invalidation, Target1, Target2
// and RiskReward are byte-identical to EP4-v1 for every input, because EP-5 classifies evidence
// and computes none of it.
//
// The VALUE is pinned as a literal by golden_test.go. Until EP-3a the suite only ever compared
// this constant to itself, so a change to the value that took every reference with it — the
// one change that breaks a reader of an archived row — passed silently.
//
// # EP6D-v1 is the fourth bump, and it changes WHEN AN ENTRY MAY BE PUBLISHED
//
// EP-6D adds one decision input, DecisionInput.Thesis (from Snapshot.Thesis). A CONTRADICTED
// standing (the scanner's primary Action is SELL, REDUCE, TAKE PROFIT or STOP LOSS) is answered
// by a new arm S1 — NO_VALID_ENTRY with no executable price — whenever an entry shape is
// resolved; an UNCLASSIFIED standing turns what would have been BUY_NOW into INSUFFICIENT_DATA.
// That is a change to what a status MEANS — an EP5-v1 plan never consulted the scanner's Action
// at all — so the stamp moves, by the rule in the line below.
//
// WHAT AN ALREADY-ARCHIVED EP5-v1 ROW NOW MEANS: exactly what it meant when written. Its BUY_NOW
// is a statement about price, zone and regime policy only, and says nothing about whether the
// scanner simultaneously recommended selling. (No persistence layer wrote plans while EP6D-v1 and
// earlier were current — EP-7's evidence projection arrived under EP6G-v1 — so no such row is
// known to exist; the paragraph is here for the one that might.)
//
// What else changed under EP6D-v1: two Reason codes (STATUS_THESIS_CONTRADICTED,
// STATUS_THESIS_UNCLASSIFIED), two plan invariants (BUY_NOW_THESIS_NOT_CONTRADICTED,
// THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY), one Snapshot field, one DecisionInput field, one
// DecisionTrace field, and a RENUMBERING of every decision arm from S1 on (S1 is new). What did
// NOT change: the confidence census, and — for an input whose standing is NOT_CONTRADICTED —
// every price and every status.
//
// NOT BUMPED AGAIN when the rule was changed three more times inside the same unreleased version
// at the EP-6D review: the user moved TAKE PROFIT and STOP LOSS into CONTRADICTED, widened
// CONTRADICTED from "never BUY_NOW" to "NO_VALID_ENTRY with no prices, on every arm", and then
// required that an S1 plan compute no entry step at all, so EntryTrace carries no zone, chase,
// stop or target number either. All three change
// EP6D-v1's semantics, but no EntryPlan row had been archived at the time (EP-7, the persistence
// item, came later), so no EP6D-v1 row exists whose meaning could differ. It stays EP6D-v1
// deliberately.
//
// Bump this whenever any rule, reason code, policy mapping or status meaning changes.
//
// EP6G-v1 makes EP-4's already-defined optional BASE valuation ceiling reachable from the
// production scanner bridge. Status policy and R:R arithmetic are unchanged, but serialized
// Target2 and its evidence/trace may now differ, so this is an observable semantic bump.
//
// NOT BUMPED AGAIN at the EP-6G review fix: an S1 plan's VALUATION_BASE_TARGET evidence row now
// carries no value (UNAVAILABLE, NOT_SCREENED) instead of the projected target. That changes
// EP6G-v1's serialized S1 output, but EP6G-v1 is unreleased and no EntryPlan row had been
// archived at the time (EP-7 was not yet implemented), so it stays EP6G-v1. Non-S1 plans are
// unchanged.
//
// EP-7 (reviewed) persists plans produced under this version as ep_* evidence rows
// (internal/research/entryplan_evidence.go), copying ep_rule_version from Plan.RuleVersion. A
// stored EP6G-v1 row is therefore possible from EP-7 on, and any later change to a rule, reason
// code, policy mapping or status meaning must bump this constant.
const RuleVersion = "EP6G-v1"

// EntryStatus is the execution-side reading of an entry semantic the scanner already produced.
//
// It is NOT a second "should I buy" taxonomy. ActPullbackBuy / ActBreakoutBuy / the OVERHEATED
// entry caution already answer that; these values answer what a trader does with the order
// book today given that answer.
type EntryStatus string

const (
	// StatusBuyNow — buy at the market, now.
	//
	// EP-1 wrote down THREE conditions here so no later work item could quietly weaken them to
	// two. EP-5 implements them, and implementing them turned three into FIVE — the extra two
	// are the ones that were hiding inside "the entry policy permits executing right now":
	//
	//	1. the entry SHAPE is resolved — the scanner said pullback or breakout;
	//	2. the regime's policy permits that shape;
	//	3. a legal entry zone exists;
	//	4. the current price is INSIDE it (bounds inclusive) — or the policy EXPLICITLY
	//	   authorises paying above it and the chase ceiling has not been crossed;
	//	5. the policy permits executing AT THE MARKET here, including every Requirement it
	//	   attached to that permission.
	//
	// Any one of the five failing means this is not BUY_NOW. See DecideStatus: arms S10 and S14
	// are the only two that produce it, and a scanner BUY reaching this value without passing
	// through them would be the second opinion doc.go forbids.
	StatusBuyNow EntryStatus = "BUY_NOW"

	// StatusWaitPullback — the thesis holds and the legal entry sits BELOW the market. The
	// execution-side reading of scanner.ActPullbackBuy and of 「拉回 5/10 日線量縮承接」.
	StatusWaitPullback EntryStatus = "WAIT_PULLBACK"

	// StatusWaitBreakout — the thesis holds and the legal entry sits ABOVE the market, on
	// confirmation. The execution-side reading of scanner.ActBreakoutBuy.
	StatusWaitBreakout EntryStatus = "WAIT_BREAKOUT"

	// StatusTooExtended — the setup and the thesis are still VALID; price has simply run too
	// far from the legal entry zone to be bought here. The execution-side reading of the
	// OVERHEATED "太熱不要追" caution.
	//
	// It is not a rejection of the stock, and a TOO_EXTENDED plan MUST still carry its
	// IdealEntry: the whole content of the status is "here, no — there, yes", and a reader
	// shown only the word has been told the useless half of it. EP-5's arm S9 withdraws
	// nothing — the zone, the ceiling, the invalidation and both targets survive it — and
	// InvariantTooExtendedKeepsZone is where that is enforced rather than promised.
	//
	// ITS CONDITION IS NUMERIC AND IT IS THE ONLY ONE: a chase ceiling exists and the market is
	// STRICTLY above it. It is NOT an alias for the scanner's OVERHEATED caution; see
	// decide.go on why aliasing the two would destroy the ability to notice they disagree.
	StatusTooExtended EntryStatus = "TOO_EXTENDED"

	// StatusNoValidEntry — a VERDICT. The evidence was sufficient, the policy was applied,
	// and it found no legal price to buy at.
	//
	// Distinct from INSUFFICIENT_DATA in exactly the way model.RegimeSideways is distinct
	// from model.RegimeUnknown: this one says "we looked, there is no edge".
	StatusNoValidEntry EntryStatus = "NO_VALID_ENTRY"

	// StatusInsufficientData — a STATE, not a verdict, and not a bearish reading.
	//
	// The evidence does not support a judgement either way. It must never fall through to
	// NO_VALID_ENTRY, for the reason analyzer.DecideRegime's R0 gives for never letting
	// UNKNOWN fall through to SIDEWAYS: one of them is an absence of observation and the
	// other is a conclusion, and a reader cannot recover the difference afterwards.
	StatusInsufficientData EntryStatus = "INSUFFICIENT_DATA"
)

// IsVerdict reports whether a status is a CONCLUSION about the trade rather than a statement
// about the evidence.
//
// Every status except INSUFFICIENT_DATA is a verdict — including NO_VALID_ENTRY, which is the
// one most likely to be misfiled, and including TOO_EXTENDED, which concludes something about
// the price while leaving the thesis intact.
func (s EntryStatus) IsVerdict() bool {
	switch s {
	case StatusBuyNow, StatusWaitPullback, StatusWaitBreakout,
		StatusTooExtended, StatusNoValidEntry:
		return true
	default:
		return false
	}
}

// Valid guards against a status string no rule can produce (e.g. decoded from an older
// snapshot). Mirrors model.Regime.Valid.
func (s EntryStatus) Valid() bool {
	return s == StatusInsufficientData || s.IsVerdict()
}

// PriceZone is a band of prices, not a point.
//
// An entry is a zone because a support level is a zone: MA20 today is not MA20 tomorrow, and
// a limit order placed at a single decimal is a bet on an arithmetic coincidence.
//
// The struct exists only when it has real Low and High values — a caller reads the pointer,
// never a zero. That is why Low and High are plain float64 while WidthATR is not: the first
// two are what makes the zone a zone, the third is a MEASUREMENT of it that can be absent
// because ATR can be absent.
type PriceZone struct {
	// Low and High bound the zone, Low <= High. Both are required for the zone to exist.
	Low  float64 `json:"low"`
	High float64 `json:"high"`

	// WidthATR is (High - Low) / ATR — the zone's width in units of the stock's own daily
	// range, so a 3-point band on a 40-dollar stock and on a 900-dollar one are comparable.
	//
	// A POINTER, deviating from the EP-1 sketch's plain float64, because ATR is optional
	// evidence: 0.0 would say "an infinitely tight zone", which is the opposite of "we do
	// not know how wide this is".
	WidthATR *float64 `json:"width_atr,omitempty"`

	// PriceBasis is what price series Low and High were derived from. See PriceBasis: this
	// repo mixes RAW and ADJUSTED across indicators today, so a price evidence that cannot
	// name its basis cannot be combined with another one.
	PriceBasis PriceBasis `json:"price_basis"`

	// Basis names the levels the zone was built from, as stable codes ("MA20",
	// "PRIOR_SWING_LOW", "BREAKOUT_RETEST"). EP-1 defines no such codes; the work item that
	// computes zones does.
	Basis []string `json:"basis,omitempty"`
}

// Invalidation is the price at which the reason for the trade stops being true.
//
// Deliberately NOT called "stop loss". A stop is a position-sizing and risk-budget decision
// that belongs to the position layer; this is the structural statement — below here, the
// setup that justified the entry no longer exists — and the two are different numbers that a
// single field would blur.
//
// Present only when a level was actually established. Price is plain float64 for that reason:
// a zero-priced invalidation is not a cautious default, it is a level that never gets hit.
type Invalidation struct {
	Price      float64    `json:"price"`
	PriceBasis PriceBasis `json:"price_basis"`

	// Basis names what makes this the level, as stable codes. EP-1 defines none.
	Basis []string `json:"basis,omitempty"`

	// Note is free presentation text. Never parsed, never a decision input.
	Note string `json:"note,omitempty"`
}

// PriceLevel is a single named price — a target, a trigger, a reference.
type PriceLevel struct {
	Price      float64    `json:"price"`
	PriceBasis PriceBasis `json:"price_basis"`

	// RMultiple is how far this level sits from the entry in units of the initial risk
	// (entry - invalidation). Nil when no invalidation was established, because without a
	// risk denominator the multiple does not exist — and 0.0 would claim the level is AT
	// the entry.
	RMultiple *float64 `json:"r_multiple,omitempty"`

	Basis []string `json:"basis,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// RiskReward is the arithmetic of one plan.
//
// Constructed ONLY when entry, invalidation and target are all established and RiskPerShare
// is strictly positive; otherwise the whole struct is nil. That is why the three fields are
// plain float64: there is no partially-known risk/reward, and a Ratio of 0 would read as
// "this trade offers no reward" rather than "we could not compute it".
type RiskReward struct {
	// RiskPerShare is entry - invalidation, strictly positive.
	RiskPerShare float64 `json:"risk_per_share"`
	// RewardPerShare is target - entry.
	RewardPerShare float64 `json:"reward_per_share"`
	// Ratio is RewardPerShare / RiskPerShare.
	Ratio float64 `json:"ratio"`
	// Target names which target the reward was measured to ("TARGET_1" / "TARGET_2").
	Target string `json:"target,omitempty"`
}

// Evidence is one traceable input behind a Plan, carried so a plan can be explained by
// showing what it was computed from rather than by appealing to the rule that produced it —
// the same discipline as valuation.QualityMetrics.
//
// Value is a pointer because most evidence keys are either present with a number or absent;
// Text carries the non-numeric ones. Both may be empty for an evidence item that exists only
// to record that something was UNAVAILABLE, which is itself a fact worth persisting.
type Evidence struct {
	// Key is a stable SCREAMING_SNAKE identifier ("CURRENT_PRICE", "ATR", "MARKET_REGIME").
	Key string `json:"key"`
	// Status is this item's own availability. Readers must check it; a nil Value with an
	// AVAILABLE status is a bug, not a zero.
	Status Availability `json:"status"`

	Value *float64 `json:"value,omitempty"`
	Text  string   `json:"text,omitempty"`

	// PriceBasis is set only for price-derived evidence, empty otherwise.
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
	// Source names where the item came from ("snapshot", "fetcher.LastAdjustmentAge").
	Source string `json:"source,omitempty"`
}

// Plan is the whole entry answer for one stock at one AsOf.
//
// Every execution price is a pointer and nil means ABSENT. There is no in-band sentinel: the
// exchanges quote positive prices, so 0 is a value this type would have to be read as
// meaning something, and every one of the readings is wrong (a free entry, a stop that never
// triggers, a target already reached).
type Plan struct {
	Symbol string `json:"symbol"`
	// AsOf is the session the plan describes, YYYY-MM-DD, copied from the Snapshot. The
	// plan is a statement about that session and stays true afterwards; it is not "today".
	AsOf        string      `json:"as_of"`
	Status      EntryStatus `json:"status"`
	RuleVersion string      `json:"rule_version"`

	IdealEntry    *PriceZone    `json:"ideal_entry,omitempty"`
	MaxChasePrice *float64      `json:"max_chase_price,omitempty"`
	Invalidation  *Invalidation `json:"invalidation,omitempty"`
	Target1       *PriceLevel   `json:"target_1,omitempty"`
	Target2       *PriceLevel   `json:"target_2,omitempty"`
	RiskReward    *RiskReward   `json:"risk_reward,omitempty"`

	// EntryTrace is the ARITHMETIC behind IdealEntry and MaxChasePrice — every candidate
	// level and what happened to it, both terms of the zone width, the raw bounds before
	// tick alignment, the legal ceiling and whether it bit. See trace.go.
	//
	// EP-3b's addition, and it is not decoration: a price a reader can act on has to be
	// explainable by SHOWING THE COMPUTATION, not by naming the rule that produced it. It is
	// also the only place a REFUSAL is legible — "no zone" plus a candidate list that says
	// the MA20 was 102.4 and above the market is a sentence; "no zone" alone is a shrug.
	//
	// NOT AN ORDER TICKET. It deliberately holds numbers that are not prices (a raw bound
	// before alignment, a centre off the tick grid), which is why it never holds a PriceZone:
	// the invariant checker's geometry rules apply to PriceZone values, and the trace is
	// exactly the place where refused arithmetic is allowed to be visible.
	//
	// Present on every well-formed plan, including the ones that publish no price. Nil only
	// for a malformed snapshot, where there is nothing for a work record to be about.
	EntryTrace *EntryTrace `json:"entry_trace,omitempty"`

	// Confidence is how much evidence stands behind the plan. A SECOND AXIS, never a
	// restatement of Status: see confidence.go.
	Confidence Confidence `json:"confidence"`

	// Reasons are stable machine-readable codes, deviating from the EP-1 sketch's []string
	// for the reason valuation.SuitabilityVerdict.Reasons is []SuitabilityReason: the
	// registry in reasons.go is only enforceable if the compiler knows what a reason is.
	// The JSON encoding is unchanged.
	Reasons  []Reason   `json:"reasons,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
	// Caveats are free presentation text, in the caller's language. Never parsed.
	Caveats []string `json:"caveats,omitempty"`
}

// HasExecutablePrices reports whether the plan carries any price a reader could act on.
//
// Used by the tests that hold EP-1 to emitting none, and by future callers that must not
// render an order ticket from an empty plan.
func (p Plan) HasExecutablePrices() bool {
	return p.IdealEntry != nil || p.MaxChasePrice != nil || p.Invalidation != nil ||
		p.Target1 != nil || p.Target2 != nil || p.RiskReward != nil
}
