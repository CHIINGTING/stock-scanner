package entryplan

import "github.com/deep-huang/stock-scanner/internal/market/model"

// ── EP-2: the market regime's ENTRY POLICY ────────────────────────────────────────────
//
// This file answers ONE question, in two halves:
//
//	regime            →  what kind of buying does the market permit at all   (EntryPolicy)
//	(regime, semantic) →  is THIS kind of entry permitted under that policy   (PolicyResult)
//
// It computes NO PRICE. Not a zone, not a chase limit, not an invalidation, not a target.
// EP-2 owns the permission; EP-3 owns the arithmetic; EP-5 owns the resulting EntryStatus.
// The separation is not tidiness: a policy invented in the same commit as the prices it
// governs gets chosen to make the prices come out, which is how a threshold ends up quoted as
// though it had been measured.

// EntrySemantic is WHICH KIND of entry is on the table — the shape of the trade, not the
// verdict on it.
//
// # It is a REUSED semantic, not a new one
//
// This repo already decides pullback-vs-breakout, in internal/scanner, and that decision took
// real work: WatchAction distinguishes ActPullbackBuy from ActBreakoutBuy, and RocketStage
// distinguishes StagePreBreakout from StageMainRun. EP-3 must not re-derive it from a chart
// it barely sees; it must be TOLD. This type is where it lands.
//
// # The scanner → semantic mapping EP-5 is expected to implement
//
// Written down here, NOT implemented here — entryplan may not import internal/scanner (the
// architecture test enforces it), and the adapter belongs on the scanner side of the wiring
// where the WatchAction is in scope:
//
//	scanner.ActPullbackBuy   (PULLBACK_BUY)           → PULLBACK
//	scanner.ActBreakoutBuy   (BREAKOUT_BUY)           → BREAKOUT
//	scanner.ActPrepare       (PREPARE_ENTRY)          → UNKNOWN   (see below)
//	scanner.ActWatchClose    (WATCH_CLOSELY)          → UNKNOWN
//	scanner.ActWait          (WAIT)                   → UNKNOWN
//	scanner.ActTakeProfit    (TAKE_PROFIT)            → no plan should be requested at all
//	scanner.ActRemove        (REMOVE_FROM_WATCHLIST)  → no plan should be requested at all
//
// PREPARE_ENTRY maps to UNKNOWN deliberately. It says a setup is forming, not which side of
// the market the entry sits on, and picking one here would be entryplan acquiring the second
// opinion doc.go forbids. If PREPARE_ENTRY ever carries a direction, the adapter maps it then.
//
// # Why there is no fourth "NOT_AN_ENTRY" value
//
// It was considered, for TAKE_PROFIT and REMOVE_FROM_WATCHLIST. It is not shipped, for the
// reason ReasonPriceBasisUnavailable folds two distinct caller bugs into one code: a fourth
// value would be a fourth branch for every consumer to switch on, with NO behavioural
// difference in this layer — a policy has nothing to say about an entry nobody proposed.
//
// It would also legitimise a wiring that must not exist. doc.go: entryplan runs "given that
// the scanner has already decided a stock is worth owning". TAKE_PROFIT and REMOVE are EXIT
// decisions; a plan requested for one of them is an EP-5 bug, not a market state, and the
// place to catch it is the adapter that would have to construct the value. If a future item
// genuinely needs to distinguish "the scanner proposed no entry" from "nobody said", it splits
// UNKNOWN then, with the rule that needs it.
type EntrySemantic string

const (
	// EntrySemanticPullback — the entry is a retracement into support. The scanner's
	// ActPullbackBuy and 「拉回 5/10 日線量縮承接」.
	EntrySemanticPullback EntrySemantic = "PULLBACK"
	// EntrySemanticBreakout — the entry is a move THROUGH resistance, on confirmation. The
	// scanner's ActBreakoutBuy.
	EntrySemanticBreakout EntrySemantic = "BREAKOUT"
	// EntrySemanticUnknown — no entry semantic is in hand. A STATE, exactly like
	// model.RegimeUnknown: it is not "no entry", and it is not a refusal. It routes to
	// INSUFFICIENT_DATA, never to NO_VALID_ENTRY.
	EntrySemanticUnknown EntrySemantic = "UNKNOWN"
)

// AllEntrySemantics is every semantic this file declares — the WHOLE set, and the registry
// Valid() is defined in terms of.
//
// Declared rather than derived, because Go cannot enumerate constants, and therefore checked
// from BOTH ends by the policy tests, exactly as AllPolicyNames and AllReasons are: every
// EntrySemantic constant declared in this file is a member, and every member is a declared
// constant. Both directions are read off THIS FILE'S SOURCE rather than off a list retyped in
// the test, because a retyped list pins the test and not the package — that is precisely how a
// fourth value slips past, and the reasoning above ("Why there is no fourth NOT_AN_ENTRY
// value") is only worth anything if adding one turns something red.
var AllEntrySemantics = []EntrySemantic{
	EntrySemanticPullback,
	EntrySemanticBreakout,
	EntrySemanticUnknown,
}

// KnownEntrySemantic reports whether a semantic is in the registry.
func KnownEntrySemantic(s EntrySemantic) bool {
	for _, k := range AllEntrySemantics {
		if k == s {
			return true
		}
	}
	return false
}

// Valid guards against a semantic string no producer can emit (e.g. decoded from an older
// archive). Mirrors model.Regime.Valid.
//
// The REGISTRY decides, not a hand-written disjunction. An `||` chain and an exported registry
// are two lists of the same set, and when they disagree the wrong one is the registry — the
// list consumers actually enumerate. With one list there is nothing to drift.
func (s EntrySemantic) Valid() bool { return KnownEntrySemantic(s) }

// Resolved reports whether the semantic names an entry style a policy can be consulted about.
//
// UNKNOWN does not, and neither does a string that is not a semantic — but for different
// reasons, which PolicyResult keeps apart.
func (s EntrySemantic) Resolved() bool {
	return s == EntrySemanticPullback || s == EntrySemanticBreakout
}

// Allowance is the ONLY kind of answer EP-2 gives. Three values, and no fourth.
//
// It is deliberately NOT an EntryStatus. BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT are
// execution readings that need a price to be true, and this file has no price: producing one
// here would be EP-5's decision made in the layer that cannot see what it decides on.
type Allowance string

const (
	// AllowanceAllowed — the policy does not forbid this kind of entry.
	//
	// A PERMISSION, NOT A GO. It says the regime allows the SHAPE of the trade; the
	// Requirements travelling with it are the further structural conditions that must hold,
	// and EP-2 evaluates NONE of them because every one needs a price.
	AllowanceAllowed Allowance = "ALLOWED"
	// AllowanceDisallowed — the policy forbids this kind of entry. A DECISION, made on
	// sufficient evidence: the regime was known, the table was consulted, the answer is no.
	//
	// This is what BEAR produces. It is the input to EP-5's NO_VALID_ENTRY, and it must
	// never be confused with UNRESOLVED below.
	AllowanceDisallowed Allowance = "DISALLOWED"
	// AllowanceUnresolved — NO decision was made. Not a refusal, not a bearish reading.
	//
	// Produced when the regime is model.RegimeUnknown, when the regime string is not a
	// regime at all, or when there is no entry semantic to judge. It is the input to EP-5's
	// INSUFFICIENT_DATA.
	AllowanceUnresolved Allowance = "UNRESOLVED"
)

// Valid rejects the zero value: "" is not an allowance, it is a field nobody filled in.
func (a Allowance) Valid() bool {
	return a == AllowanceAllowed || a == AllowanceDisallowed || a == AllowanceUnresolved
}

// Decided reports whether the allowance is a CONCLUSION rather than the absence of one.
//
// The same split EntryStatus.IsVerdict makes, one layer up, and the reason this package can
// state the BEAR/UNKNOWN difference in a single expression: DEFENSIVE's DISALLOWED is
// Decided, UNRESOLVED's is not.
func (a Allowance) Decided() bool {
	return a == AllowanceAllowed || a == AllowanceDisallowed
}

// Requirement is a further structural condition an ALLOWED entry must satisfy, as a stable
// code — never as prose, and never evaluated here.
//
// Requirements exist because the regime table is not expressible in booleans. BULL_PULLBACK
// permits buying at the market only INSIDE the pullback zone; SIDEWAYS only NEAR SUPPORT;
// DISTRIBUTION only for a setup that clears a raised bar. A bare AllowBuyNow bool would have
// to answer "true" to all three and lose the only part that matters, or "false" and forbid
// what the table permits.
//
// EP-2 CANNOT CHECK ANY OF THEM. Each one is a statement about a price relative to a level,
// and this layer has neither. They are carried so EP-3 inherits the condition instead of
// re-inventing it.
type Requirement string

const (
	// RequirementInsideZone — the entry must sit inside the established entry zone. EP-3
	// defines the zone; EP-2 only says the condition applies.
	RequirementInsideZone Requirement = "INSIDE_VALID_ZONE"
	// RequirementNearSupport — the entry must sit at a structural support level, not merely
	// below the last price.
	RequirementNearSupport Requirement = "NEAR_SUPPORT"
	// RequirementElevatedEvidence — the setup must clear a raised evidence bar. What
	// "raised" means numerically is owned by the item that grades setups, not by this one.
	RequirementElevatedEvidence Requirement = "ELEVATED_EVIDENCE"
)

// AllRequirements is every requirement code this file can attach. Kept honest from both ends
// by the policy tests, for the reason AllReasons is.
var AllRequirements = []Requirement{
	RequirementInsideZone,
	RequirementNearSupport,
	RequirementElevatedEvidence,
}

// PolicyName identifies the regime's entry policy. Stable SCREAMING_SNAKE, persisted verbatim,
// with the natural-language wording living at the presentation boundary — the same code/wording
// split reasons.go makes.
type PolicyName string

const (
	// PolicyNormal — BULL. Every entry shape is permitted and chasing is bounded.
	PolicyNormal PolicyName = "NORMAL"
	// PolicyPullbackOnly — BULL_PULLBACK. The trend is intact and the correction is orderly,
	// so a retracement entry is the trade and a breakout entry is not.
	PolicyPullbackOnly PolicyName = "PULLBACK_ONLY"
	// PolicyNearSupportOnly — SIDEWAYS. No directional edge, so the only defensible entry is
	// one whose risk is defined by a level that is right there.
	PolicyNearSupportOnly PolicyName = "NEAR_SUPPORT_ONLY"
	// PolicyElevatedBar — DISTRIBUTION. The index is resilient and the internals are not;
	// entries are not forbidden, they are RARE, and the bar is raised rather than removed.
	PolicyElevatedBar PolicyName = "ELEVATED_BAR"
	// PolicyDefensive — BEAR. A DECISION, on sufficient evidence: no entry shape is permitted.
	//
	// EP-2 stops here, at DISALLOWED. The NO_VALID_ENTRY verdict is EP-5's to publish, and
	// the reason for the split is that this layer never sees whether an entry existed — only
	// whether one was permitted.
	PolicyDefensive PolicyName = "DEFENSIVE"
	// PolicyUnresolved — UNKNOWN. NOT "buying is forbidden". No policy decision was made,
	// because the regime layer could not make one.
	//
	// The precedent is exact and is quoted rather than paraphrased.
	// internal/market/analyzer/regime_engine.go's R0 rule: UNKNOWN "is a state, not a
	// failure, and it must never fall through to SIDEWAYS ('we looked, no edge') which is a
	// verdict". Mapping UNKNOWN to NEAR_SUPPORT_ONLY would be exactly that fall-through, one
	// layer down, and mapping it to DEFENSIVE would be worse: it would turn "we could not
	// look" into "we looked and the market is bearish", which no reader of a stored plan can
	// undo afterwards.
	PolicyUnresolved PolicyName = "UNRESOLVED"
)

// AllPolicyNames is every policy name this file can produce.
//
// Declared rather than derived, because Go cannot enumerate constants, and therefore checked
// from BOTH ends by the policy tests: every name the table returns is a member, and every
// member is returned for at least one canonical regime unless it is reserved. A dead policy
// name is a branch a consumer writes and never reaches.
var AllPolicyNames = []PolicyName{
	PolicyNormal,
	PolicyPullbackOnly,
	PolicyNearSupportOnly,
	PolicyElevatedBar,
	PolicyDefensive,
	PolicyUnresolved,
}

// ReservedPolicyNames are names that exist without a regime using them yet. EMPTY under
// EP2-v1, and it must stay empty unless a work item can say what the reservation is for: an
// unreachable policy is indistinguishable from a mapping someone forgot to write.
var ReservedPolicyNames = map[PolicyName]bool{}

// KnownPolicyName reports whether a name is in the registry.
func KnownPolicyName(n PolicyName) bool {
	for _, k := range AllPolicyNames {
		if k == n {
			return true
		}
	}
	return false
}

// Permission is what one policy says about one kind of buying.
//
// Requirements are only ever attached to an ALLOWED permission. A condition hanging off a
// DISALLOWED one would read as "satisfy this and it is fine", which is the opposite of what
// the policy said; the policy tests refuse it.
type Permission struct {
	Allowance    Allowance     `json:"allowance"`
	Requirements []Requirement `json:"requirements,omitempty"`
}

// ChasePolicy is how far above the ideal entry this regime tolerates paying, in ATR units.
//
// # MaxATR IS A MULTIPLIER, NEVER A PRICE
//
// Nothing in EP-2 multiplies it by anything. It is carried so EP-3 inherits the regime's
// tolerance instead of choosing one, and so the choice is reviewable on its own rather than
// buried in the expression that consumes it.
//
// # Why the allowance is a separate field
//
// The EP-2 sketch had a bare `MaxChaseATR *float64` with nil meaning "no chasing". That
// encoding cannot tell BEAR from UNKNOWN — the one distinction this work item exists to
// protect — because both would be nil, and the two nils mean "refused" and "undecided". So
// the DECISION lives in Allowance and the NUMBER in MaxATR, with one invariant tying them:
//
//	MaxATR != nil  ⟺  Allowance == ALLOWED
//
// MaxATR stays a pointer for the reason every optional number in this package is one:
// MISSING ≠ ZERO. A 0.0 would say "chasing is permitted, by exactly nothing", which is a
// tolerance nobody set.
type ChasePolicy struct {
	Allowance Allowance `json:"allowance"`
	// MaxATR is the multiple of ATR(14) a chase may cross above the ideal entry. Nil unless
	// Allowance is ALLOWED.
	MaxATR *float64 `json:"max_atr,omitempty"`

	// Immediate is whether this regime permits BUYING AT THE MARKET at a price ABOVE the
	// ideal entry zone, today, without waiting.
	//
	// # A CEILING IS NOT A PERMISSION, and EP-5 is where the difference became load-bearing
	//
	// Allowance + MaxATR answer "how far above the entry may this plan END UP PAYING" — the
	// bound on a fill. They do NOT answer "may a market order be sent right now at a price
	// the zone does not contain", and until EP-5 nothing had to ask. EP-5 does: with price
	// above the zone and below the ceiling it must publish either BUY_NOW or something else,
	// and reading "MaxChasePrice exists" as "chasing is authorised" would be an EXECUTION
	// permission inferred from a PRICE BOUND — a decision nobody in this package made.
	//
	// BULL_PULLBACK is the row that proves the two are different. It tolerates a 0.4×ATR
	// overshoot on a fill and its BuyNow permission simultaneously carries
	// INSIDE_VALID_ZONE — "only inside the pullback zone". A single field cannot say both.
	//
	// # The invariant, and why it is one-directional
	//
	//	Immediate.Allowance == ALLOWED  ⟹  Allowance == ALLOWED
	//
	// The converse is FALSE and BULL_PULLBACK is the counterexample. A permission to pay
	// above the zone with no ceiling naming how far would be unbounded chasing, which no row
	// grants; a ceiling with no permission to use it immediately is exactly the conservative
	// row this repo already has.
	//
	// UNRESOLVED here is the UNKNOWN regime's answer and it is NOT "no". See PolicyUnresolved.
	Immediate Permission `json:"immediate"`
}

// The two chase tolerances, as named constants so they are reviewable without reading the
// table.
//
// HEURISTIC — NOT BACKTEST-FITTED, the same disclosure RuleVersion carries and for the same
// reason: there is no outcome study behind either number, and a value quoted from a table is
// assumed to have one. What IS deliberate is the ORDERING — a pullback regime tolerates less
// chasing than a trending one — and the ordering is what the tests pin, not the magnitudes.
const (
	// maxChaseATRBull — BULL: "limited". One day's range above the ideal entry.
	maxChaseATRBull = 1.0
	// maxChaseATRBullPullback — BULL_PULLBACK: "conservative", and strictly less than BULL.
	// The whole premise of the regime is that price is coming back to you.
	maxChaseATRBullPullback = 0.4
)

// EntryPolicy is the regime's entry permission set — a PURE FUNCTION OF THE REGIME.
//
// # Why the table is keyed on the regime ALONE
//
// The EP-2 sketch asked for both a per-regime EntryPolicy and a PolicyFor(regime, semantic)
// API, which are two different shapes. The conflict is resolved in favour of ONE table:
//
//	regime  →  EntryPolicy       a total, single-key mapping: exhaustive, mutation-testable,
//	                             and diffable — six rows a reviewer can read at once
//	(policy, semantic) → Permission   a pure PREDICATE over that table
//
// The alternative — keying the table on the pair — multiplies six rows by three semantics
// into eighteen, of which twelve say nothing new, and it makes "every regime has a policy"
// unstateable: completeness would become a property of a product rather than of a mapping.
// The sketch's own examples (BULL+BREAKOUT allowed, BULL_PULLBACK+BREAKOUT not) are satisfied
// exactly by the predicate, because the pair-dependence lives entirely in WHICH permission is
// read, not in which policy applies.
//
// # BuyNow is not an EntrySemantic
//
// PULLBACK and BREAKOUT are the SHAPES of an entry; buying at the market now is a question of
// TIMING that EP-5 asks after a shape has been permitted. It is carried on the policy because
// the regime is what makes it answerable — SIDEWAYS permits it only near support, BEAR not at
// all — and PolicyFor deliberately does not route any semantic to it.
type EntryPolicy struct {
	Name PolicyName `json:"name"`
	// Regime is the regime this policy IS the answer for, carried so a policy quoted out of
	// context still says what it applies to.
	Regime model.Regime `json:"regime"`

	// BuyNow is the permission to execute at the market. See the type doc: not a semantic.
	BuyNow Permission `json:"buy_now"`
	// Pullback and Breakout are the permissions for the two entry shapes.
	Pullback Permission `json:"pullback"`
	Breakout Permission `json:"breakout"`

	Chase ChasePolicy `json:"chase"`
}

// PermissionFor returns what this policy says about one entry semantic.
//
// ok == false means THE QUESTION DOES NOT ARISE: the semantic is UNKNOWN, or it is not a
// semantic at all. It is never "the answer is no" — a missing question and a negative answer
// are the two things this whole work item is about keeping apart.
//
// There is no route from any semantic to BuyNow, by construction.
func (p EntryPolicy) PermissionFor(s EntrySemantic) (Permission, bool) {
	switch s {
	case EntrySemanticPullback:
		return p.Pullback, true
	case EntrySemanticBreakout:
		return p.Breakout, true
	}
	return Permission{}, false
}

// PolicyForRegime is the whole regime → policy table.
//
// TOTAL over the canonical regimes and DETERMINISTIC: same regime, same policy, every time,
// with no clock, no I/O and no shared state. Every returned slice and pointer is freshly
// built, so a caller that mutates one policy cannot reach into the next one's answer.
//
// # There is no default branch, and that is the point
//
// A `default: return <some policy>` would make the ONE mapping that matters — UNKNOWN — the
// one nobody had to write down, and it would silently absorb any regime added later. So an
// unrecognised regime string returns NO POLICY (ok == false) rather than a fallback one:
// a value that no rule in internal/market can produce is not a market state, it is a decoded
// typo, and planning against it would be planning against nothing.
//
// UNKNOWN is NOT that case. UNKNOWN is a REGIME — "the honest answer when the data cannot
// support a call" (model.Regime's doc) — so it has its own row, returning PolicyUnresolved
// with every permission UNRESOLVED.
func PolicyForRegime(r model.Regime) (EntryPolicy, bool) {
	switch r {
	case model.RegimeBull:
		// Healthy uptrend, broad participation. Every shape is on the table and the only
		// limit is how far up you may pay for it.
		return EntryPolicy{
			Name:     PolicyNormal,
			Regime:   r,
			BuyNow:   Permission{Allowance: AllowanceAllowed},
			Pullback: Permission{Allowance: AllowanceAllowed},
			Breakout: Permission{Allowance: AllowanceAllowed},
			Chase: ChasePolicy{
				Allowance: AllowanceAllowed,
				MaxATR:    chaseATR(maxChaseATRBull),
				// THE ONE ROW THAT PERMITS PAYING UP AT THE MARKET. The trend is intact and
				// participation is broad, so the move that left the entry band behind is the
				// move the plan was betting on; the ceiling bounds what may be paid for it
				// rather than forbidding it. Its BuyNow permission carries no requirement
				// either — this is the only regime where both halves are unconditional.
				Immediate: Permission{Allowance: AllowanceAllowed},
			},
		}, true

	case model.RegimeBullPullback:
		// Primary uptrend intact, orderly correction. Buying the retracement is the trade;
		// buying a breakout INTO a correction is paying up for the move that is currently
		// being given back, so it is refused rather than merely conditioned.
		return EntryPolicy{
			Name:   PolicyPullbackOnly,
			Regime: r,
			BuyNow: Permission{
				Allowance:    AllowanceAllowed,
				Requirements: []Requirement{RequirementInsideZone},
			},
			Pullback: Permission{Allowance: AllowanceAllowed},
			Breakout: Permission{Allowance: AllowanceDisallowed},
			Chase: ChasePolicy{
				Allowance: AllowanceAllowed,
				MaxATR:    chaseATR(maxChaseATRBullPullback),
				// A CEILING WITHOUT A PERMISSION, and the row that makes the two fields
				// necessary. 0.4×ATR bounds a fill that slips above the band; it does not
				// authorise sending a market order above it, because the row's own BuyNow
				// permission says INSIDE_VALID_ZONE and the regime's whole premise is that
				// price is coming back to you. DISALLOWED is a DECISION on sufficient
				// evidence — the regime was known and the table was consulted — not an
				// absence, so it never routes to INSUFFICIENT_DATA.
				Immediate: Permission{Allowance: AllowanceDisallowed},
			},
		}, true

	case model.RegimeSideways:
		// No directional edge. An entry can still be defensible, but only where the risk is
		// defined by a level that is right there — and chasing has nothing to chase.
		return EntryPolicy{
			Name:   PolicyNearSupportOnly,
			Regime: r,
			BuyNow: Permission{
				Allowance:    AllowanceAllowed,
				Requirements: []Requirement{RequirementNearSupport},
			},
			Pullback: Permission{
				Allowance:    AllowanceAllowed,
				Requirements: []Requirement{RequirementNearSupport},
			},
			Breakout: Permission{Allowance: AllowanceDisallowed},
			// No ceiling and no permission. Stated as its own DISALLOWED rather than
			// inherited from the chase allowance above it: with no directional edge, a price
			// above the entry band has nothing behind it, and this row's BuyNow permission
			// says NEAR_SUPPORT — which a price above the band is, by construction, not.
			Chase: ChasePolicy{
				Allowance: AllowanceDisallowed,
				Immediate: Permission{Allowance: AllowanceDisallowed},
			},
		}, true

	case model.RegimeDistribution:
		// Index resilient, internals deteriorating. Entries are RARE, not forbidden: the bar
		// is raised, and buying at the market additionally has to be at a level.
		return EntryPolicy{
			Name:   PolicyElevatedBar,
			Regime: r,
			BuyNow: Permission{
				Allowance: AllowanceAllowed,
				Requirements: []Requirement{
					RequirementElevatedEvidence,
					RequirementNearSupport,
				},
			},
			Pullback: Permission{
				Allowance:    AllowanceAllowed,
				Requirements: []Requirement{RequirementElevatedEvidence},
			},
			Breakout: Permission{Allowance: AllowanceDisallowed},
			// Entries are rare and CONDITIONED here; paying above the band is the one thing
			// a deteriorating tape punishes, and it is also the only kind of entry whose
			// risk is not defined by a level that is right there. DISALLOWED, and again as
			// this row's own decision rather than as a consequence of the ceiling's absence.
			Chase: ChasePolicy{
				Allowance: AllowanceDisallowed,
				Immediate: Permission{Allowance: AllowanceDisallowed},
			},
		}, true

	case model.RegimeBear:
		// Trend and structure both bearish. A DECISION on sufficient evidence, and every
		// permission is DISALLOWED — which is Decided(), unlike UNKNOWN's below.
		//
		// EP-2 says no more than this. NO_VALID_ENTRY is EP-5's verdict to publish.
		return EntryPolicy{
			Name:     PolicyDefensive,
			Regime:   r,
			BuyNow:   Permission{Allowance: AllowanceDisallowed},
			Pullback: Permission{Allowance: AllowanceDisallowed},
			Breakout: Permission{Allowance: AllowanceDisallowed},
			Chase: ChasePolicy{
				Allowance: AllowanceDisallowed,
				// DISALLOWED, which is Decided(). BEAR refuses every shape of buying, and
				// buying at the market above the entry band is the shape it refuses hardest.
				Immediate: Permission{Allowance: AllowanceDisallowed},
			},
		}, true

	case model.RegimeUnknown:
		// Insufficient / missing data. NOT a prohibition — no decision at all.
		//
		// regime_engine.go R0: UNKNOWN "is a state, not a failure, and it must never fall
		// through to SIDEWAYS ('we looked, no edge') which is a verdict". Every permission
		// here is UNRESOLVED, which is !Decided(), and the whole row exists so that
		// insufficient evidence routes to INSUFFICIENT_DATA rather than to NO_VALID_ENTRY.
		return EntryPolicy{
			Name:     PolicyUnresolved,
			Regime:   r,
			BuyNow:   Permission{Allowance: AllowanceUnresolved},
			Pullback: Permission{Allowance: AllowanceUnresolved},
			Breakout: Permission{Allowance: AllowanceUnresolved},
			Chase: ChasePolicy{
				Allowance: AllowanceUnresolved,
				// UNRESOLVED, NOT DISALLOWED — the one value in this column that is not a
				// decision. Making it DISALLOWED would turn "we could not read the market"
				// into "the market forbids paying up", and EP-5 would publish NO_VALID_ENTRY
				// where INSUFFICIENT_DATA is the truth. This is the same line PolicyUnresolved
				// draws, in the field EP-5 reads to decide between the two.
				Immediate: Permission{Allowance: AllowanceUnresolved},
			},
		}, true
	}

	// Not a regime. See the doc above: no fallback policy, because there is no market state
	// to have a policy about.
	return EntryPolicy{}, false
}

// PolicyResult is the answer to (regime, semantic): the policy that applied, and what it said
// about that one semantic.
//
// # Three different ways to be UNRESOLVED, all readable off the struct
//
//	Policy == nil                    the regime string is not a regime — a caller bug or an
//	                                 archive decoded from an older vocabulary
//	Policy.Name == PolicyUnresolved  the regime IS model.RegimeUnknown — evidence insufficient
//	!Semantic.Resolved()             there is no entry semantic to judge
//
// All three produce Allowance == UNRESOLVED, because in all three no decision was made. They
// are kept distinguishable because the consumer that has to EXPLAIN the answer needs to say
// which one happened, and a single flag cannot.
//
// It carries no price, no zone and no basis, and TestThePolicyLayerCannotSeeAPrice asserts it:
// a permission layer that could read a price would be a decision layer.
type PolicyResult struct {
	Regime   model.Regime  `json:"regime"`
	Semantic EntrySemantic `json:"semantic"`

	// Policy is the regime's policy, or nil when the regime is not a model.Regime value.
	// A POINTER because absent must be absent — a zero EntryPolicy would carry Name "",
	// which is not a policy, and a reader would have to know that to avoid trusting it.
	Policy *EntryPolicy `json:"policy,omitempty"`

	// Allowance is the policy's answer for THIS semantic. Never "", never an EntryStatus.
	Allowance Allowance `json:"allowance"`
	// Requirements are the further conditions an ALLOWED entry must satisfy. EP-2 evaluates
	// none of them; each one needs a price.
	Requirements []Requirement `json:"requirements,omitempty"`
}

// Decided reports whether a policy DECISION was reached for this semantic.
func (r PolicyResult) Decided() bool { return r.Allowance.Decided() }

// PolicyFor answers whether one entry semantic is permitted in one regime.
//
// PURE and TOTAL: defined for every (regime, semantic) pair including the zero values, with no
// clock, no I/O and no shared state. It reads no price — it is not given one — so it cannot
// have an opinion about whether a particular entry is available, only about whether that KIND
// of entry is permitted at all.
//
// The three answers are ALLOWED / DISALLOWED / UNRESOLVED. It does not produce BUY_NOW,
// WAIT_PULLBACK or WAIT_BREAKOUT: those need a price relative to a zone, and turning a
// permission into one of them is EP-5's decide step.
func PolicyFor(regime model.Regime, semantic EntrySemantic) PolicyResult {
	out := PolicyResult{
		Regime:   regime,
		Semantic: semantic,
		// The honest default, asserted rather than assumed: until a row of the table has
		// been read AND a semantic has been matched, no decision exists.
		Allowance: AllowanceUnresolved,
	}

	policy, ok := PolicyForRegime(regime)
	if !ok {
		// Not a regime at all. Policy stays nil; see the type doc's three-way table.
		return out
	}
	out.Policy = &policy

	perm, ok := policy.PermissionFor(semantic)
	if !ok {
		// A policy applies, but there is no entry semantic for it to apply TO.
		return out
	}
	out.Allowance = perm.Allowance
	out.Requirements = perm.Requirements
	return out
}

// chaseATR copies a chase multiplier onto the heap.
//
// A FUNCTION, not a package-level pointer variable, so every policy handed out owns its own
// number: a caller that writes through the pointer changes its own copy and nothing else.
// The same reason ComputePlan copies every float out of its Snapshot.
func chaseATR(v float64) *float64 { return &v }
