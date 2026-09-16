package entryplan_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── literal goldens ───────────────────────────────────────────────────────────────────
//
// This file pins the LITERAL VALUES of strings that leave the process. It exists because of a
// specific finding from the EP-1 review (I-3): every version assertion in the suite was of the
// form
//
//	if p.RuleVersion != entryplan.RuleVersion { ... }        // plan_test.go
//
// which compares a constant to itself. It cannot fail. Renaming the constant, or changing its
// VALUE while every reference follows along, leaves the whole package green — and the value is
// exactly the part that has already left: it is stamped on every Plan, and doc.go's own
// archive TODO calls these strings "a wire format" once a row is on disk.
//
// The point of a literal golden is that the test does NOT follow along. Changing the value
// requires editing this file, and editing this file is the moment somebody has to answer "what
// reads the old value, and does it still mean the same thing".
//
// SCOPE. EP-3a pinned RuleVersion only, and left the rest to "the first work item that
// ARCHIVES or PERSISTS a Plan". EP-3b closes that follow-up early, because it is the item that
// makes the strings matter: a plan now carries a price, so a consumer will store one, and the
// EP-1 review measured what the suite does about it — "renaming an entire set of reason codes
// consistently would pass silently".
//
// So everything below compares a PERSISTED MACHINE VALUE against a literal typed out in this
// file. Never a Go constant against itself, and never a Go identifier: renaming
// ReasonZoneATRUnavailable is a refactor, and changing its value to "ATR_MISSING" is a wire
// format change, and only one of those may pass.

// ── RuleVersion ───────────────────────────────────────────────────────────────────────

// THE EP-4 BUMP. This line was "EP2-v1" until EP-3b and "EP3-v1" until EP-4, and BOTH failing
// tests were the intended alarm, not a nuisance.
//
// The bump is because the PERSISTED ACTIONABLE NUMERIC SEMANTICS changed, not because the work
// item number went up. EP-4 adds a thesis-invalidation price, two target prices and a
// risk/reward ratio — all tick-aligned executable levels — so an EP3-v1 row and an EP4-v1 row
// answer different questions, and a reader of an archived row must be able to tell which one it
// answered from the stamp alone.
//
// What an already-archived EP3-v1 row now means: exactly what it meant when it was written — an
// entry zone, a chase ceiling, and NO RISK SIDE AT ALL. Its missing Invalidation, Target1,
// Target2 and RiskReward are the absence of a RULE, not the absence of evidence, and they must
// not be compared with an EP4-v1 row's absences, which ARE evidence statements ("no structural
// level qualified below the zone", "no resistance above the entry"). The same sentence applied
// one version earlier to an EP2-v1 row, which carries no price at all.
//
// EP6D-v1: the fourth bump (its semantics were widened twice inside the unreleased version at
// the EP-6D review — see types.go — without a further bump, because nothing is archived). EP-6D
// gives the decision one more input — whether the
// scanner's primary Action contradicts the thesis — so an EP5-v1 BUY_NOW and an EP6D-v1 BUY_NOW
// are not the same claim: only the second says the scanner was not simultaneously recommending
// SELL, REDUCE, TAKE PROFIT or STOP LOSS. An archived EP5-v1 row keeps meaning exactly what it said (price, zone and
// regime policy only). See types.go.
const goldenRuleVersion = "EP6G-v1"

func TestRuleVersionIsPinnedToItsLiteralValue(t *testing.T) {
	if entryplan.RuleVersion != goldenRuleVersion {
		t.Errorf("RuleVersion = %q, want the literal %q.\nIf the rule set really changed, "+
			"update this golden IN THE SAME COMMIT as the rule, and say in the message what "+
			"an already-archived %q row now means. If nothing changed, this is a rename that "+
			"altered a persisted value.",
			entryplan.RuleVersion, goldenRuleVersion, goldenRuleVersion)
	}

	// And the stamp must actually reach the Plan. Asserted against the LITERAL for the same
	// reason: `p.RuleVersion != entryplan.RuleVersion` is true of a plan that carries the
	// constant AND of one that carries nothing, if the constant were ever emptied.
	var checked int
	for _, c := range matrix() {
		p := entryplan.ComputePlan(c.in)
		if p.RuleVersion != goldenRuleVersion {
			t.Fatalf("%s: plan carries rule version %q, want the literal %q — an archived "+
				"verdict with no version on it is uninterpretable the moment the rules move",
				c.name, p.RuleVersion, goldenRuleVersion)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no plan was checked — the matrix is empty and the assertion above never ran")
	}
}

// The SHAPE is pinned separately from the value, so that the next bump has to keep the format
// a reader can sort and compare: "EP<item>-v<revision>", no spaces, no build date, nothing
// that changes without a decision.
func TestTheRuleVersionShapeIsStable(t *testing.T) {
	v := entryplan.RuleVersion
	if !strings.HasPrefix(v, "EP") {
		t.Errorf("RuleVersion %q does not start with EP — the stamp names the work item that "+
			"produced the rules", v)
	}
	if !strings.Contains(v, "-v") {
		t.Errorf("RuleVersion %q carries no -v revision — valuation's FU9-v1 / FU11-v1 and "+
			"derivatives' R15-v1 all do, and a stamp without a revision cannot record a "+
			"correction to the same item's rules", v)
	}
	if strings.ContainsAny(v, " \t\"") || strings.ToUpper(v[:2]) != v[:2] {
		t.Errorf("RuleVersion %q is not a bare stable token", v)
	}
}

// ── the reason codes, as persisted ────────────────────────────────────────────────────

// Every Reason value, typed out. The SET is exact and the ORDER is AllReasons' own, so both a
// renamed value and a new code that nobody decided on turn this red.
//
// The EP-1 review's finding, in full: "EP-1 reviewer 實測過:目前一致改名整套會靜默通過" — a
// consistent rename of the whole set passed the suite, because every assertion in it compared
// the constant to itself. This file is the answer to that, and it is only an answer while the
// strings are LITERALS here.
var goldenReasons = []string{
	"SNAPSHOT_MALFORMED",
	"CURRENT_PRICE_UNAVAILABLE",
	"PRICE_BASIS_UNAVAILABLE",
	"MARKET_REGIME_UNAVAILABLE",
	"ENTRY_SEMANTIC_UNAVAILABLE",
	// EP-3b, the zone step.
	"ZONE_SEMANTIC_NOT_PERMITTED",
	"ZONE_POLICY_UNRESOLVED",
	"PULLBACK_LEVELS_UNAVAILABLE",
	"PULLBACK_LEVELS_PRICE_BASIS_MISMATCH",
	"PULLBACK_LEVEL_NOT_BELOW_CURRENT_PRICE",
	"BREAKOUT_LEVEL_UNAVAILABLE",
	"BREAKOUT_LEVEL_PRICE_BASIS_MISMATCH",
	"BREAKOUT_LEVEL_NOT_A_QUOTE",
	"ZONE_ATR_UNAVAILABLE",
	"ZONE_ATR_PRICE_BASIS_MISMATCH",
	"ZONE_ATR_PERIOD_MISMATCH",
	"ZONE_TICK_ALIGNMENT_UNAVAILABLE",
	"ZONE_LOW_NOT_A_PRICE",
	"PULLBACK_ZONE_OVERLAPS_CURRENT_PRICE",
	"RECENT_PRICE_ADJUSTMENT",
	// EP-3b, the chase step.
	"CHASE_NOT_ALLOWED",
	"CHASE_POLICY_UNRESOLVED",
	"PREVIOUS_CLOSE_UNAVAILABLE",
	"PREVIOUS_CLOSE_PRICE_BASIS_MISMATCH",
	"CHASE_LIMIT_REQUIRES_RAW_BASIS",
	"CHASE_LIMIT_RULE_UNAVAILABLE",
	"CHASE_LIMIT_PRICE_UNAVAILABLE",
	"CHASE_CEILING_BELOW_ZONE",
	// EP-4, the invalidation step — SPLIT BY EP-5 into three, on spec §17's instruction.
	//
	// WHAT AN ALREADY-ARCHIVED ROW MEANS NOW: NO_VALID_INVALIDATION keeps its literal value
	// and NARROWS. Before EP-5 it covered all four ways ComputeInvalidation can decline; from
	// EP5-v1 it means ONLY "every candidate was compared with the zone and none of them is a
	// legal boundary below it". A row archived under EP4-v1 carrying this code may therefore
	// have meant any of the four, and the RuleVersion on the row is what says which reading
	// applies — that is the whole reason RuleVersion is stamped on the plan AND on the trace.
	// The value was not renamed, because the narrowed meaning is the one the NAME already said
	// and a rename would have broken every reader for a change of scope they can date.
	"NO_VALID_INVALIDATION",
	"INVALIDATION_EVIDENCE_UNAVAILABLE",
	"INVALIDATION_SELECTION_INCONSISTENT",
	// EP-4, the target step.
	"TARGET_1_TICK_ALIGNMENT_UNAVAILABLE",
	"TARGET_1_NOT_ABOVE_ENTRY",
	"TARGET_2_TICK_ALIGNMENT_UNAVAILABLE",
	"VALUATION_CEILING_BELOW_ENTRY",
	"TARGET_2_BELOW_TARGET_1",
	// ── EP-5, the DECISION step. THE FIRST CODES THAT EXPLAIN A STATUS RATHER THAN A
	// MISSING PRICE, so a reader can partition an archived plan's reason list into "why
	// these numbers" and "why this verdict" without a lookup table.
	//
	// WHAT AN ALREADY-ARCHIVED ROW MEANS NOW: nothing changes for one. Under EP4-v1 every
	// well-formed plan carried INSUFFICIENT_DATA and an unconditional
	// ENTRY_EVALUATION_NOT_IMPLEMENTED, which is GONE from this list — its own doc called it
	// "deliberately TEMPORARY: the work item that implements entry evaluation removes it",
	// and EP-5 is that work item. A row carrying it is an EP4-v1 row and reads exactly as it
	// always did: the layer had no entry evaluation. It cannot be produced again.
	"STATUS_ENTRY_SEMANTIC_UNRESOLVED",
	"STATUS_CURRENT_PRICE_UNAVAILABLE",
	"STATUS_POLICY_UNRESOLVED",
	"STATUS_POLICY_REFUSED",
	"STATUS_ENTRY_ZONE_UNAVAILABLE",
	"STATUS_NO_LEGAL_ENTRY_ZONE",
	"STATUS_RISK_BOUNDARY_UNAVAILABLE",
	"STATUS_NO_RISK_BOUNDARY",
	"STATUS_ABOVE_MAX_CHASE",
	"STATUS_PRICE_INSIDE_ZONE",
	"STATUS_PRICE_ABOVE_ENTRY_ZONE",
	"STATUS_PRICE_BELOW_ENTRY_ZONE",
	"STATUS_IMMEDIATE_ENTRY_REFUSED",
	"STATUS_IMMEDIATE_ENTRY_UNRESOLVED",
	"STATUS_REQUIREMENT_NOT_MET",
	"STATUS_REQUIREMENT_NOT_EVALUATED",
	"STATUS_IMMEDIATE_CHASE_PERMITTED",
	"STATUS_CHASE_CEILING_UNRESOLVED",
	"STATUS_NO_EXECUTABLE_PRICE_RELATION",
	// ── EP-6D, the thesis standing on the two BUY_NOW arms. WHAT AN ALREADY-ARCHIVED ROW
	// MEANS NOW: unchanged — no EP5-v1 row carries either code, and every EP5-v1 code keeps its
	// meaning. Rows newly carrying STATUS_THESIS_UNCLASSIFIED are rows EP5-v1 would have
	// published as BUY_NOW; rows carrying STATUS_THESIS_CONTRADICTED are any plan with a
	// resolved entry shape whose scanner Action contradicts it.
	"STATUS_THESIS_CONTRADICTED",
	"STATUS_THESIS_UNCLASSIFIED",
	// Reserved.
	"CHASE_TICK_ALIGNMENT_UNAVAILABLE",
	"RISK_NOT_ESTABLISHED",
	"PLAN_INVARIANT_VIOLATED",
}

func TestTheReasonCodesArePinnedToTheirLiteralValues(t *testing.T) {
	got := make([]string, 0, len(entryplan.AllReasons))
	for _, r := range entryplan.AllReasons {
		got = append(got, string(r))
	}
	assertLiteralSequence(t, "AllReasons", got, goldenReasons)

	// And the codes must actually be REACHED. Asserted against the literals, because
	// `r != entryplan.ReasonX` is true of a plan carrying the constant AND of one carrying
	// nothing, if the constant were ever emptied.
	//
	// The sweep is emittedReasons' — ComputePlan over every snapshot, PLUS DecideStatus over
	// the decision matrix — for the reason stated there: EP-5's decision layer is a separately
	// exported total rule, and four of its arms read a projection today's upstream steps do
	// not happen to produce together.
	seen := map[string]bool{}
	for r := range emittedReasons(t) {
		seen[string(r)] = true
	}
	var reached int
	for _, want := range goldenReasons {
		if seen[want] {
			reached++
		}
	}
	if reached < len(goldenReasons)-len(entryplan.ReservedReasons) {
		t.Errorf("only %d of the %d literal codes appeared on a real plan (%d are reserved) "+
			"— the values in this file are not the values production emits", reached,
			len(goldenReasons), len(entryplan.ReservedReasons))
	}
}

// ── the evidence keys, as persisted ───────────────────────────────────────────────────

// Every census key, in emission order. The ORDER is part of the contract: PRICE_BASIS first,
// because it decides what the other price evidence means.
var goldenEvidenceKeys = []string{
	"PRICE_BASIS",
	"CURRENT_PRICE",
	"MARKET_REGIME",
	"ENTRY_SEMANTIC",
	"ATR",
	"ADJUSTMENT_AGE",
	"LEVEL_MA20",
	"LEVEL_MA60",
	"LEVEL_BASE_LOW",
	"LEVEL_BREAKOUT_PIVOT",
	"PREVIOUS_CLOSE",
	"AVG_VOLUME_20",
	"BASE_LOW_KIND",
	"VALUATION_BASE_TARGET",
	"VALUATION_SUITABILITY",
}

func TestTheEvidenceKeysArePinnedToTheirLiteralValues(t *testing.T) {
	assertLiteralSequence(t, "AllEvidenceKeys", entryplan.AllEvidenceKeys, goldenEvidenceKeys)

	// The keys as they appear ON A PLAN, which is what a consumer joins against.
	in := zoneBase()
	p := entryplan.ComputePlan(in)
	assertLiteralSequence(t, "the census of a complete plan", evidenceKeysOf(p), goldenEvidenceKeys)

	// The evidence SOURCE labels are persisted too, and one of them names a function whose
	// answer is uninterpretable without it (see AdjustmentAge).
	want := map[string]string{
		"CURRENT_PRICE":  "snapshot",
		"ADJUSTMENT_AGE": "fetcher.LastAdjustmentAge",
		"LEVEL_MA20":     "snapshot",
		"PREVIOUS_CLOSE": "snapshot",
	}
	for _, e := range p.Evidence {
		if w, ok := want[e.Key]; ok {
			if e.Source != w {
				t.Errorf("evidence %q source = %q, want the literal %q", e.Key, e.Source, w)
			}
			delete(want, e.Key)
		}
	}
	if len(want) != 0 {
		t.Errorf("rows missing from the census: %v", want)
	}
}

// ── every other vocabulary that leaves the process ────────────────────────────────────

// The remaining persisted sets, each pinned as a literal sequence. This is the rest of doc.go's
// archive TODO: every PolicyName, every Requirement, every EntryStatus, every Availability,
// every PriceBasis, every EntrySemantic, every Allowance, every PlanInvariant, and EP-3b's
// seven new vocabularies.
func TestEveryPersistedVocabularyIsPinnedToItsLiteralValues(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"AllPolicyNames", renderStrings(entryplan.AllPolicyNames), []string{
			"NORMAL", "PULLBACK_ONLY", "NEAR_SUPPORT_ONLY", "ELEVATED_BAR", "DEFENSIVE",
			"UNRESOLVED",
		}},
		{"AllRequirements", renderStrings(entryplan.AllRequirements), []string{
			"INSIDE_VALID_ZONE", "NEAR_SUPPORT", "ELEVATED_EVIDENCE",
		}},
		{"AllEntrySemantics", renderStrings(entryplan.AllEntrySemantics), []string{
			"PULLBACK", "BREAKOUT", "UNKNOWN",
		}},
		{"AllPlanInvariants", renderStrings(entryplan.AllPlanInvariants), []string{
			"FLOAT_NOT_NAN", "FLOAT_NOT_INF", "ZONE_LOW_POSITIVE", "ZONE_HIGH_POSITIVE",
			"ZONE_LOW_NOT_ABOVE_HIGH", "MAX_CHASE_POSITIVE", "MAX_CHASE_NOT_BELOW_ZONE_HIGH",
			// EP-4's four, appended so the seven EP-3a codes keep their positions: the
			// ORDER of this registry is the order violations are reported and the order
			// EntryTrace.InvariantViolations is written in, and an archived row's list is
			// diffed byte for byte.
			"INVALIDATION_BELOW_ZONE", "TARGET1_ABOVE_ZONE", "TARGET2_NOT_BELOW_TARGET1",
			"RR_FINITE_POSITIVE",
			// EP-5's seven, appended for the same reason EP-4's four were: the order of this
			// registry is the order violations are reported and the order
			// EntryTrace.InvariantViolations is written in.
			//
			// WHAT AN ALREADY-ARCHIVED ROW MEANS NOW: every EP4-v1 row was stamped CLEAN with
			// an EMPTY violation list, because no plan production could build broke an
			// invariant. Adding seven codes cannot change what an empty list meant. What it
			// DOES change is that a plan whose STATUS contradicts its own numbers is now
			// caught — and EP4-v1 plans had no status worth contradicting, since every one of
			// them was INSUFFICIENT_DATA.
			"STATUS_CANONICAL", "STATUS_HAS_REASON", "BUY_NOW_HAS_ZONE",
			"BUY_NOW_PRICE_AUTHORISED", "TOO_EXTENDED_ABOVE_CEILING",
			"TOO_EXTENDED_KEEPS_ZONE", "WAIT_MATCHES_SEMANTIC",
			// EP-6D's one, appended. WHAT AN ALREADY-ARCHIVED ROW MEANS NOW: unchanged — an
			// EP5-v1 row's violation list could not contain it, and a CLEAN stamp on one said
			// nothing about the scanner's Action.
			"BUY_NOW_THESIS_NOT_CONTRADICTED",
			// EP-6D, widened by the user: a CONTRADICTED thesis publishes no entry at all.
			"THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY",
		}},
		// EP-6D: the thesis standing, persisted on EntryTrace.Decision.Thesis.
		{"AllThesisStandings", renderStrings(entryplan.AllThesisStandings), []string{
			"NOT_CONTRADICTED", "CONTRADICTED",
		}},
		// EP-5's two new registries: the decision table's arm ids and the readings the
		// decision gives a missing price. Both are PERSISTED — the arm on
		// EntryTrace.Decision.Rule, the readings beside each absence code — so both are wire
		// format from EP5-v1 on.
		{"AllDecisionRules", renderStrings(entryplan.AllDecisionRules), []string{
			"S0", "S1", "S2", "S3", "S4", "S5", "S6", "S7", "S8", "S9", "S10", "S11", "S12",
			"S13", "S14", "S15", "S16", "S17", "S18",
			// EP-6D inserted RuleThesisContradicted at S1 and renumbered S1..S18 to S2..S19.
			// WHAT AN ALREADY-ARCHIVED ROW MEANS NOW: no row existed — nothing persisted plans
			// at the time (EP-7 came later, and its ep_* rows do not store the rule id) — which is
			// the only condition under which a positional renumber is allowed at all (see
			// DecisionRule).
			"S19",
		}},
		{"AllAbsenceReadings", renderStrings(entryplan.AllAbsenceReadings), []string{
			"UNCLASSIFIED", "EVIDENCE_GAP", "SEARCHED_AND_NONE", "ABSENT_BY_DECISION",
		}},
		{"AllLevelSources", renderStrings(entryplan.AllLevelSources), []string{
			"MA20", "MA60", "BASE_LOW", "BREAKOUT_PIVOT",
		}},
		{"AllLevelDispositions", renderStrings(entryplan.AllLevelDispositions), []string{
			"SELECTED", "ELIGIBLE_NOT_SELECTED", "LEVEL_UNAVAILABLE", "PRICE_BASIS_MISMATCH",
			"NOT_BELOW_CURRENT_PRICE", "NOT_A_CANDIDATE_FOR_SEMANTIC", "NOT_SCREENED",
		}},
		{"AllHalfWidthBindings", renderStrings(entryplan.AllHalfWidthBindings), []string{
			"ATR_HALF", "TICK_FLOOR",
		}},
		{"AllWidthRejections", renderStrings(entryplan.AllWidthRejections), []string{
			"ATR_UNAVAILABLE", "ATR_PRICE_BASIS_MISMATCH", "ATR_PERIOD_MISMATCH",
			"TICK_SIZE_UNAVAILABLE",
		}},
		{"AllTickDirections", renderStrings(entryplan.AllTickDirections), []string{
			"DOWN", "UP", "NEAREST",
		}},
		{"AllClampOutcomes", renderStrings(entryplan.AllClampOutcomes), []string{
			"NOT_CLAMPED", "CLAMPED_TO_LIMIT_UP",
		}},
		// EP-5 adds STATUS_WITHDRAWN. WHAT AN ARCHIVED ROW MEANS NOW: unchanged — no EP4-v1
		// row carries it, and CLEAN and PRICES_WITHDRAWN mean exactly what they meant. The
		// new value exists because EP-5's seven invariants withdraw a VERDICT and no price,
		// and stamping that PRICES_WITHDRAWN would send a reader looking for a number that is
		// still there.
		// PRICES_AND_STATUS_WITHDRAWN is F1's value, delivered and reviewed as part of EP-7 —
		// not part of EP-6G. EP-7 does not persist InvariantCheck as an evidence row.
		{"AllInvariantCheckOutcomes", renderStrings(entryplan.AllInvariantCheckOutcomes),
			[]string{"CLEAN", "PRICES_WITHDRAWN", "STATUS_WITHDRAWN", "PRICES_AND_STATUS_WITHDRAWN"}},
		// ── EP-4's six new persisted vocabularies ────────────────────────────────────
		{"AllStopSources", renderStrings(entryplan.AllStopSources), []string{
			"MA60", "BASE_LOW", "ZONE_LOW_MINUS_ATR",
		}},
		{"AllRiskLevelDispositions", renderStrings(entryplan.AllRiskLevelDispositions),
			[]string{
				"SELECTED", "ELIGIBLE_NOT_SELECTED", "LEVEL_UNAVAILABLE",
				"PRICE_BASIS_MISMATCH", "NOT_BELOW_ZONE_LOW", "NOT_ABOVE_ZONE_HIGH",
				"NOT_A_STRUCTURAL_LEVEL", "NOT_A_CANDIDATE_FOR_SEMANTIC",
				"FALLBACK_NOT_NEEDED", "NOT_A_PRICE", "NOT_SCREENED",
			}},
		{"AllTarget1Bindings", renderStrings(entryplan.AllTarget1Bindings), []string{
			"R_MULTIPLE", "RESISTANCE",
		}},
		{"AllValuationCeilingOutcomes", renderStrings(entryplan.AllValuationCeilingOutcomes),
			[]string{
				"VALUATION_UNAVAILABLE", "MODEL_UNSUITABLE", "PRICE_BASIS_MISMATCH",
				"NOT_BINDING", "APPLIED",
			}},
		{"AllRiskRejections", renderStrings(entryplan.AllRiskRejections), []string{
			"RISK_NOT_POSITIVE", "INVALIDATION_PRICE_BASIS_MISMATCH",
		}},
		{"AllBaseLowKinds", renderStrings(entryplan.AllBaseLowKinds), []string{
			"CONSOLIDATION_BASE", "LATEST_BAR_LOW", "UNKNOWN",
		}},
		// The suitability projection's values are valuation.Suitability's, VERBATIM. Pinned
		// as literals here because that is the whole content of the projection: a rename on
		// either side turns a caller's mapping silently wrong, and this package cannot
		// import the other one to compare (it reaches os and net/http).
		{"AllValuationSuitabilities", renderStrings(entryplan.AllValuationSuitabilities),
			[]string{"SUITABLE", "CONDITIONAL", "WEAK", "UNSUITABLE", "INSUFFICIENT_DATA"}},
		// The withdrawal vocabulary, in DEPENDENCY order. The values are Plan's own Go field
		// names, which is what lets a violation Path ("Plan.Target2*.Price") and a withdrawn
		// field name be the same string with no second mapping to drift.
		{"AllPlanPriceFields", renderStrings(entryplan.AllPlanPriceFields), []string{
			"IdealEntry", "MaxChasePrice", "Invalidation", "Target1", "Target2", "RiskReward",
		}},
		// Sets with no registry variable: the values are pinned by naming the constants,
		// which is legitimate here because the COMPARISON is still against a literal.
		{"the entry statuses", []string{
			string(entryplan.StatusBuyNow), string(entryplan.StatusWaitPullback),
			string(entryplan.StatusWaitBreakout), string(entryplan.StatusTooExtended),
			string(entryplan.StatusNoValidEntry), string(entryplan.StatusInsufficientData),
		}, []string{
			"BUY_NOW", "WAIT_PULLBACK", "WAIT_BREAKOUT", "TOO_EXTENDED", "NO_VALID_ENTRY",
			"INSUFFICIENT_DATA",
		}},
		{"the availabilities", []string{
			string(entryplan.Available), string(entryplan.Unavailable),
			string(entryplan.InsufficientData),
		}, []string{"AVAILABLE", "UNAVAILABLE", "INSUFFICIENT_DATA"}},
		{"the price bases", []string{
			string(entryplan.PriceBasisRaw), string(entryplan.PriceBasisAdjusted),
		}, []string{"RAW", "ADJUSTED"}},
		{"the allowances", []string{
			string(entryplan.AllowanceAllowed), string(entryplan.AllowanceDisallowed),
			string(entryplan.AllowanceUnresolved),
		}, []string{"ALLOWED", "DISALLOWED", "UNRESOLVED"}},
		{"the confidence grades", []string{
			string(entryplan.ConfidenceHigh), string(entryplan.ConfidenceMedium),
			string(entryplan.ConfidenceLow), string(entryplan.ConfidenceInsufficientData),
		}, []string{"HIGH", "MEDIUM", "LOW", "INSUFFICIENT_DATA"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertLiteralSequence(t, c.name, c.got, c.want)
		})
	}
}

// ── the JSON wire format ──────────────────────────────────────────────────────────────

// The field NAMES an archived plan is stored under.
//
// doc.go: "Once a row is on disk those strings are a wire format, and a rename that only breaks
// readers nobody has written yet is indistinguishable from a rename that breaks the archive."
// A Go field rename is free; changing its json tag is not, and only this file can tell them
// apart.
func TestTheJSONWireFormatIsPinned(t *testing.T) {
	cases := []struct {
		value any
		want  map[string]string
	}{
		{entryplan.Plan{}, map[string]string{
			"Symbol": "symbol", "AsOf": "as_of", "Status": "status",
			"RuleVersion": "rule_version", "IdealEntry": "ideal_entry,omitempty",
			"MaxChasePrice": "max_chase_price,omitempty",
			"Invalidation":  "invalidation,omitempty", "Target1": "target_1,omitempty",
			"Target2": "target_2,omitempty", "RiskReward": "risk_reward,omitempty",
			"EntryTrace": "entry_trace,omitempty", "Confidence": "confidence",
			"Reasons": "reasons,omitempty", "Evidence": "evidence,omitempty",
			"Caveats": "caveats,omitempty",
		}},
		{entryplan.PriceZone{}, map[string]string{
			"Low": "low", "High": "high", "WidthATR": "width_atr,omitempty",
			"PriceBasis": "price_basis", "Basis": "basis,omitempty",
		}},
		{entryplan.EntryTrace{}, map[string]string{
			"RuleVersion": "rule_version", "AdjustmentAgeBars": "adjustment_age_bars,omitempty",
			"RecentAdjustment": "recent_adjustment,omitempty", "Policy": "policy",
			"Zone": "zone", "Chase": "chase", "Stop": "stop", "Targets": "targets",
			// EP-5's work record. Present on every well-formed plan, including the ones whose
			// status is INSUFFICIENT_DATA: "the decision ran and could not conclude" and "no
			// decision ran" are different outputs, and without this field they would be the
			// same one.
			"Decision":            "decision",
			"InvariantCheck":      "invariant_check",
			"InvariantViolations": "invariant_violations,omitempty",
			"WithdrawnFields":     "withdrawn_fields,omitempty",
		}},
		// ── EP-4's types. The three PRICE-BEARING ones (Invalidation, PriceLevel,
		// RiskReward) were declared by EP-1 and never built until now, so this is the first
		// version in which their json tags are a wire format rather than a plan.
		{entryplan.Invalidation{}, map[string]string{
			"Price": "price", "PriceBasis": "price_basis", "Basis": "basis,omitempty",
			"Note": "note,omitempty",
		}},
		{entryplan.PriceLevel{}, map[string]string{
			"Price": "price", "PriceBasis": "price_basis",
			"RMultiple": "r_multiple,omitempty", "Basis": "basis,omitempty",
			"Note": "note,omitempty",
		}},
		{entryplan.RiskReward{}, map[string]string{
			"RiskPerShare": "risk_per_share", "RewardPerShare": "reward_per_share",
			"Ratio": "ratio", "Target": "target,omitempty",
		}},
		// ── EP-5's trace. The arm id and the three absence READINGS are the fields an
		// archived verdict is re-derivable from: NO_VALID_INVALIDATION and
		// INVALIDATION_EVIDENCE_UNAVAILABLE leave the same nil pointer and produce different
		// statuses, so a row showing only the nil could explain neither.
		{entryplan.DecisionTrace{}, map[string]string{
			"Semantic": "semantic,omitempty", "CurrentPrice": "current_price,omitempty",
			"Rule": "rule,omitempty", "Status": "status,omitempty",
			"Reasons":       "reasons,omitempty",
			"ZoneAbsence":   "zone_absence,omitempty",
			"ZoneReading":   "zone_reading,omitempty",
			"ChaseAbsence":  "chase_absence,omitempty",
			"ChaseReading":  "chase_reading,omitempty",
			"StopAbsence":   "stop_absence,omitempty",
			"StopReading":   "stop_reading,omitempty",
			"PolicyDecided": "policy_decided,omitempty",
			// EP-6D. omitempty: "" (unclassified) is spelled by absence on the wire, which
			// is the same encoding every other optional reading on this trace uses.
			"Thesis": "thesis,omitempty",
		}},
		{entryplan.StopTrace{}, map[string]string{
			"Semantic": "semantic,omitempty", "PriceBasis": "price_basis,omitempty",
			"ZoneLow": "zone_low,omitempty", "Candidates": "candidates,omitempty",
			"BaseLowKind": "base_low_kind,omitempty", "ATR": "atr,omitempty",
			"ATRMultiple":    "atr_multiple,omitempty",
			"SelectedSource": "selected_source,omitempty", "RawPrice": "raw_price,omitempty",
			"TickDirection": "tick_direction,omitempty", "Price": "price,omitempty",
			"Rejection": "rejection,omitempty",
		}},
		{entryplan.StopCandidate{}, map[string]string{
			"Source": "source", "Status": "status", "Value": "value,omitempty",
			"PriceBasis": "price_basis,omitempty", "Disposition": "disposition",
		}},
		{entryplan.ResistanceCandidate{}, map[string]string{
			"Source": "source", "Status": "status", "Value": "value,omitempty",
			"PriceBasis": "price_basis,omitempty", "Disposition": "disposition",
		}},
		{entryplan.TargetTrace{}, map[string]string{
			"PriceBasis": "price_basis,omitempty", "EntryUsed": "entry_used,omitempty",
			"Invalidation": "invalidation,omitempty", "Risk": "risk,omitempty",
			"RiskRejection": "risk_rejection,omitempty", "Resistance": "resistance",
			"Target1RMultiple":     "target_1_r_multiple,omitempty",
			"Target1RTarget":       "target_1_r_target,omitempty",
			"Target1Raw":           "target_1_raw,omitempty",
			"Target1Binding":       "target_1_binding,omitempty",
			"Target1":              "target_1,omitempty",
			"Target1Rejection":     "target_1_rejection,omitempty",
			"Target2RMultiple":     "target_2_r_multiple,omitempty",
			"Target2Raw":           "target_2_raw,omitempty",
			"ValuationTarget":      "valuation_target,omitempty",
			"ValuationSuitability": "valuation_suitability,omitempty",
			"ValuationCeiling":     "valuation_ceiling,omitempty",
			"Target2Clamped":       "target_2_clamped,omitempty",
			"Target2":              "target_2,omitempty",
			"Target2Rejection":     "target_2_rejection,omitempty",
			"TickDirection":        "tick_direction,omitempty",
			"Reward":               "reward,omitempty", "Ratio": "ratio,omitempty",
		}},
		{entryplan.ValuationEvidence{}, map[string]string{
			"Status": "status", "BaseTargetPrice": "base_target_price,omitempty",
			"Suitability": "suitability,omitempty", "PriceBasis": "price_basis,omitempty",
		}},
		{entryplan.ZoneTrace{}, map[string]string{
			"Semantic": "semantic,omitempty", "PriceBasis": "price_basis,omitempty",
			"Candidates": "candidates,omitempty", "CenterSource": "center_source,omitempty",
			"Center": "center,omitempty", "ATR": "atr,omitempty",
			"ATRPeriod": "atr_period,omitempty", "ATRHalfWidth": "atr_half_width,omitempty",
			"TickSize": "tick_size,omitempty", "TickFloorWidth": "tick_floor_width,omitempty",
			"HalfWidth":        "half_width,omitempty",
			"HalfWidthBinding": "half_width_binding,omitempty",
			"WidthRejection":   "width_rejection,omitempty", "RawLow": "raw_low,omitempty",
			"RawHigh": "raw_high,omitempty", "TickDirections": "tick_directions,omitempty",
			"Low": "low,omitempty", "High": "high,omitempty",
			"OverlapsCurrentPrice": "overlaps_current_price,omitempty",
			"Rejection":            "rejection,omitempty",
		}},
		{entryplan.ChaseTrace{}, map[string]string{
			"ZoneHigh": "zone_high,omitempty", "MaxATR": "max_atr,omitempty",
			"ATR": "atr,omitempty", "Raw": "raw,omitempty",
			"PreviousClose": "previous_close,omitempty", "LimitRule": "limit_rule,omitempty",
			"LimitUp": "limit_up,omitempty", "Clamp": "clamp,omitempty",
			"LimitAssumption": "limit_assumption,omitempty",
			"TickDirection":   "tick_direction,omitempty", "Final": "final,omitempty",
			"Rejection": "rejection,omitempty",
		}},
		{entryplan.PolicyTrace{}, map[string]string{
			"Name": "name,omitempty", "SemanticAllowance": "semantic_allowance,omitempty",
			"Requirements":   "requirements,omitempty",
			"ChaseAllowance": "chase_allowance,omitempty",
			"ChaseMaxATR":    "chase_max_atr,omitempty",
		}},
		{entryplan.CandidateLevel{}, map[string]string{
			"Source": "source", "Status": "status", "Value": "value,omitempty",
			"PriceBasis": "price_basis,omitempty", "Disposition": "disposition",
		}},
		{entryplan.Evidence{}, map[string]string{
			"Key": "key", "Status": "status", "Value": "value,omitempty",
			"Text": "text,omitempty", "PriceBasis": "price_basis,omitempty",
			"Source": "source,omitempty",
		}},
		{entryplan.TickDirections{}, map[string]string{
			"Low": "low,omitempty", "High": "high,omitempty",
		}},
	}

	for _, c := range cases {
		rt := reflect.TypeOf(c.value)
		t.Run(rt.Name(), func(t *testing.T) {
			if rt.NumField() != len(c.want) {
				t.Errorf("%s has %d fields and this golden lists %d — a field was added or "+
					"removed, which changes what an archived row contains", rt.Name(),
					rt.NumField(), len(c.want))
			}
			for n := 0; n < rt.NumField(); n++ {
				field := rt.Field(n)
				want, ok := c.want[field.Name]
				if !ok {
					t.Errorf("%s.%s is not in the golden — a new field reaches the archive "+
						"under a name nobody decided on", rt.Name(), field.Name)
					continue
				}
				if got := field.Tag.Get("json"); got != want {
					t.Errorf("%s.%s json tag = %q, want the literal %q", rt.Name(),
						field.Name, got, want)
				}
			}
		})
	}
}

// assertLiteralSequence compares a production sequence with a literal one, element by element.
func assertLiteralSequence(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d members and the golden lists %d:\n got  %v\n want %v\n"+
			"If a value was added or removed, say in the commit what an already-archived row "+
			"means now, and update this file in the SAME change", what, len(got), len(want),
			got, want)
	}
	for n := range want {
		if got[n] != want[n] {
			t.Errorf("%s[%d] = %q, want the literal %q — this is a PERSISTED value, so the "+
				"question is what reads the old one", what, n, got[n], want[n])
		}
	}
}
