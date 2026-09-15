package entryplan_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── EP-2: the regime → policy table ───────────────────────────────────────────────────
//
// The highest-priority property in this file is ONE ROW: model.RegimeUnknown must map to
// PolicyUnresolved and to nothing else. Everything below is arranged so that changing that row
// — to SIDEWAYS's policy, to BEAR's, to anything — fails loudly and in several places.
//
// The precedent is quoted rather than paraphrased, because it is the same argument one layer
// up. internal/market/analyzer/regime_engine.go's R0 rule: UNKNOWN "is a state, not a failure,
// and it must never fall through to SIDEWAYS ('we looked, no edge') which is a verdict".

// The canonical regimes and the policy each one must map to. DECLARED, so a reader sees the
// whole table at once — and cross-checked against internal/market/model's own source below, so
// this list cannot silently fall behind the vocabulary it claims to cover.
var regimePolicyTable = []struct {
	regime model.Regime
	policy entryplan.PolicyName
	// buyNow / pullback / breakout are the allowances the EP-2 table specifies.
	buyNow, pullback, breakout entryplan.Allowance
	// chase is the chase allowance; hasChaseATR says whether a multiplier accompanies it.
	chase       entryplan.Allowance
	hasChaseATR bool
	// immediate is ChasePolicy.Immediate — permission to BUY AT THE MARKET above the band,
	// which is a strictly stronger thing than the chase tolerance beside it. It lives in the
	// table rather than in hand-written per-regime cases because the cases only ever named the
	// three rows someone thought of: flipping SIDEWAYS or DISTRIBUTION from DISALLOWED to
	// UNRESOLVED left the whole suite green while turning a DECISION into a STATE, which is
	// the BEAR/UNKNOWN collapse EP-2 exists to prevent. Measured, not argued.
	immediate entryplan.Allowance
}{
	{model.RegimeBull, entryplan.PolicyNormal,
		entryplan.AllowanceAllowed, entryplan.AllowanceAllowed, entryplan.AllowanceAllowed,
		entryplan.AllowanceAllowed, true, entryplan.AllowanceAllowed},
	{model.RegimeBullPullback, entryplan.PolicyPullbackOnly,
		entryplan.AllowanceAllowed, entryplan.AllowanceAllowed, entryplan.AllowanceDisallowed,
		entryplan.AllowanceAllowed, true, entryplan.AllowanceDisallowed},
	{model.RegimeSideways, entryplan.PolicyNearSupportOnly,
		entryplan.AllowanceAllowed, entryplan.AllowanceAllowed, entryplan.AllowanceDisallowed,
		entryplan.AllowanceDisallowed, false, entryplan.AllowanceDisallowed},
	{model.RegimeDistribution, entryplan.PolicyElevatedBar,
		entryplan.AllowanceAllowed, entryplan.AllowanceAllowed, entryplan.AllowanceDisallowed,
		entryplan.AllowanceDisallowed, false, entryplan.AllowanceDisallowed},
	{model.RegimeBear, entryplan.PolicyDefensive,
		entryplan.AllowanceDisallowed, entryplan.AllowanceDisallowed, entryplan.AllowanceDisallowed,
		entryplan.AllowanceDisallowed, false, entryplan.AllowanceDisallowed},
	{model.RegimeUnknown, entryplan.PolicyUnresolved,
		entryplan.AllowanceUnresolved, entryplan.AllowanceUnresolved, entryplan.AllowanceUnresolved,
		entryplan.AllowanceUnresolved, false, entryplan.AllowanceUnresolved},
}

// TestTheRegimePolicyTableIsExactlyWhatTheSpecSays walks every row.
func TestTheRegimePolicyTableIsExactlyWhatTheSpecSays(t *testing.T) {
	for _, c := range regimePolicyTable {
		t.Run(string(c.regime), func(t *testing.T) {
			p, ok := entryplan.PolicyForRegime(c.regime)
			if !ok {
				t.Fatalf("%s has NO policy — every canonical regime must map to one, or a "+
					"plan made in that regime is made under no policy at all", c.regime)
			}
			if p.Name != c.policy {
				t.Errorf("%s → %q, want %q", c.regime, p.Name, c.policy)
			}
			if p.Regime != c.regime {
				t.Errorf("%s's policy says it is for %q — a policy quoted out of context "+
					"would name the wrong market", c.regime, p.Regime)
			}
			if p.BuyNow.Allowance != c.buyNow {
				t.Errorf("%s buy-now = %q, want %q", c.regime, p.BuyNow.Allowance, c.buyNow)
			}
			if p.Pullback.Allowance != c.pullback {
				t.Errorf("%s pullback = %q, want %q", c.regime, p.Pullback.Allowance, c.pullback)
			}
			if p.Breakout.Allowance != c.breakout {
				t.Errorf("%s breakout = %q, want %q", c.regime, p.Breakout.Allowance, c.breakout)
			}
			if p.Chase.Allowance != c.chase {
				t.Errorf("%s chase = %q, want %q", c.regime, p.Chase.Allowance, c.chase)
			}
			if got := p.Chase.MaxATR != nil; got != c.hasChaseATR {
				t.Errorf("%s carries a chase multiplier = %v, want %v", c.regime, got, c.hasChaseATR)
			}
			// THE IMMEDIATE PERMISSION, row by row. Asserted here and not only in the
			// per-regime special cases below it, because those named BULL_PULLBACK, BEAR and
			// UNKNOWN — and a DISALLOWED→UNRESOLVED flip on SIDEWAYS or DISTRIBUTION passed
			// the entire suite. Both are DECISIONS ("with no directional edge a price above
			// the band has nothing behind it"), and UNRESOLVED would republish them as "we
			// could not read the market", which EP-5 turns into INSUFFICIENT_DATA where
			// WAIT_PULLBACK is the truth.
			if p.Chase.Immediate.Allowance != c.immediate {
				t.Errorf("%s immediate chase permission = %q, want %q — paying at the market "+
					"above the band is a strictly stronger authorisation than tolerating "+
					"slippage up to a ceiling, and the two are separate columns for that reason",
					c.regime, p.Chase.Immediate.Allowance, c.immediate)
			}
		})
	}
}

// ── the two rows that must never be the same ──────────────────────────────────────────

// UNKNOWN IS NOT A PROHIBITION. This is the test the mutation list puts first, and it is
// written so that mapping UNKNOWN to SIDEWAYS's policy, to BEAR's, or to any policy that makes
// a decision fails here.
func TestUnknownRegimeIsUnresolvedAndNeverAVerdict(t *testing.T) {
	p, ok := entryplan.PolicyForRegime(model.RegimeUnknown)
	if !ok {
		t.Fatal("UNKNOWN has no policy row at all — UNKNOWN is a REGIME (model.Regime's doc: " +
			"\"the honest answer when the data cannot support a call\"), so it must have its " +
			"own row rather than being treated as an undefined string")
	}
	if p.Name != entryplan.PolicyUnresolved {
		t.Fatalf("UNKNOWN → %q. regime_engine.go's R0: UNKNOWN \"is a state, not a failure, "+
			"and it must never fall through to SIDEWAYS ('we looked, no edge') which is a "+
			"verdict\". Any policy that decides something turns \"we could not look\" into a "+
			"conclusion, and no reader of a stored plan can undo that afterwards", p.Name)
	}

	// The other five policies, by name, are all wrong answers for UNKNOWN — including the two
	// the mutation list names explicitly.
	sideways, _ := entryplan.PolicyForRegime(model.RegimeSideways)
	bear, _ := entryplan.PolicyForRegime(model.RegimeBear)
	if p.Name == sideways.Name {
		t.Error("UNKNOWN and SIDEWAYS share a policy — \"we could not look\" has become " +
			"\"we looked, no edge\"")
	}
	if p.Name == bear.Name {
		t.Error("UNKNOWN and BEAR share a policy — \"we could not look\" has become " +
			"\"the market is bearish\"")
	}

	// And every permission it carries is UNDECIDED, not refused.
	for _, perm := range []struct {
		what string
		p    entryplan.Permission
	}{{"buy now", p.BuyNow}, {"pullback", p.Pullback}, {"breakout", p.Breakout}} {
		if perm.p.Allowance != entryplan.AllowanceUnresolved {
			t.Errorf("UNKNOWN's %s permission = %q, want UNRESOLVED", perm.what, perm.p.Allowance)
		}
		if perm.p.Allowance.Decided() {
			t.Errorf("UNKNOWN's %s permission reports Decided() — insufficient evidence has "+
				"produced a decision", perm.what)
		}
	}
	if p.Chase.Allowance != entryplan.AllowanceUnresolved {
		t.Errorf("UNKNOWN's chase allowance = %q, want UNRESOLVED", p.Chase.Allowance)
	}
	if p.Chase.MaxATR != nil {
		t.Errorf("UNKNOWN's policy carries a chase multiplier %v — a tolerance nobody set",
			*p.Chase.MaxATR)
	}
}

// BEAR IS A DECISION, and the whole content of EP-2's highest-priority gate is that the two
// rows above and below are not the same sentence.
func TestBearRegimeIsADefensiveDecisionNotAnAbsentOne(t *testing.T) {
	bear, ok := entryplan.PolicyForRegime(model.RegimeBear)
	if !ok {
		t.Fatal("BEAR has no policy")
	}
	if bear.Name != entryplan.PolicyDefensive {
		t.Fatalf("BEAR → %q, want DEFENSIVE", bear.Name)
	}
	for _, perm := range []struct {
		what string
		p    entryplan.Permission
	}{{"buy now", bear.BuyNow}, {"pullback", bear.Pullback}, {"breakout", bear.Breakout}} {
		if perm.p.Allowance != entryplan.AllowanceDisallowed {
			t.Errorf("BEAR's %s permission = %q, want DISALLOWED", perm.what, perm.p.Allowance)
		}
		if !perm.p.Allowance.Decided() {
			t.Errorf("BEAR's %s permission is not Decided() — the evidence WAS sufficient and "+
				"the table WAS consulted; the answer is no, not \"we do not know\"", perm.what)
		}
	}

	unknown, _ := entryplan.PolicyForRegime(model.RegimeUnknown)
	if bear.Name == unknown.Name {
		t.Fatal("DEFENSIVE and UNRESOLVED have collapsed into one policy. They are the two " +
			"answers this work item exists to keep apart: one routes to NO_VALID_ENTRY (a " +
			"verdict) and the other to INSUFFICIENT_DATA (a state)")
	}
	if bear.BuyNow.Allowance == unknown.BuyNow.Allowance {
		t.Fatal("BEAR and UNKNOWN produce the same allowance")
	}

	// EP-2 stops at the allowance. It must not have produced the verdict itself.
	if entryplan.EntryStatus(bear.Name).Valid() {
		t.Errorf("the DEFENSIVE policy name %q is also a valid EntryStatus — EP-2 has started "+
			"producing EP-5's verdicts", bear.Name)
	}
}

// ── completeness, from both ends ──────────────────────────────────────────────────────

// modelRegimeConst matches a regime constant declaration in internal/market/model.
var modelRegimeConst = regexp.MustCompile(`(?m)^\s*(\w+)\s+Regime\s*=\s*"([A-Z_]+)"`)

// TestEveryRegimeInTheModelHasAPolicy reads internal/market/model/regime.go and requires a
// policy for every regime DECLARED THERE.
//
// Source-scanned rather than listed, because a hand-written list is exactly what a new regime
// slips past: adding a seventh value to model.Regime would leave every declared test green
// while the switch in PolicyForRegime silently answered "not a regime" for it. That is the
// mutation this test exists to kill, and the sweep also fails if a regime is REMOVED from the
// model while a policy row for it remains.
func TestEveryRegimeInTheModelHasAPolicy(t *testing.T) {
	src, err := os.ReadFile("../market/model/regime.go")
	if err != nil {
		t.Fatalf("cannot read the regime vocabulary: %v", err)
	}
	matches := modelRegimeConst.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("no regime constants found in internal/market/model/regime.go — the scan is " +
			"not reading the vocabulary and would pass for the wrong reason")
	}

	declared := map[model.Regime]string{}
	for _, m := range matches {
		declared[model.Regime(m[2])] = m[1]
	}

	// forward: every regime the model declares has a policy
	for regime, goName := range declared {
		if _, ok := entryplan.PolicyForRegime(regime); !ok {
			t.Errorf("model.%s (%q) has NO entry policy. A regime with no policy row is a "+
				"market state in which entryplan silently answers \"that is not a regime\", "+
				"which is the fallback this table was written without", goName, regime)
		}
		if !regime.Valid() {
			t.Errorf("model.%s (%q) is declared and its own Valid() rejects it", goName, regime)
		}
	}

	// backward: every policy row corresponds to a declared regime
	for _, c := range regimePolicyTable {
		if _, ok := declared[c.regime]; !ok {
			t.Errorf("the policy table has a row for %q and the model no longer declares it — "+
				"a policy for a regime nobody can produce", c.regime)
		}
	}
	if len(declared) != len(regimePolicyTable) {
		var names []string
		for r := range declared {
			names = append(names, string(r))
		}
		t.Errorf("internal/market/model declares %d regimes %v and this file asserts %d rows — "+
			"a regime was added or removed without deciding what its entry policy is",
			len(declared), names, len(regimePolicyTable))
	}
}

// The policy-name registry, both directions. A name no regime uses is a branch a consumer
// writes and never reaches; a name the table produces and the registry omits is a code an
// archived plan carries that no consumer can enumerate. Same discipline as AllReasons.
func TestPolicyNameRegistryIsExactlyWhatTheTableProduces(t *testing.T) {
	used := map[entryplan.PolicyName]model.Regime{}
	for _, c := range regimePolicyTable {
		p, ok := entryplan.PolicyForRegime(c.regime)
		if !ok {
			continue
		}
		used[p.Name] = c.regime
	}

	// forward
	for name, regime := range used {
		if !entryplan.KnownPolicyName(name) {
			t.Errorf("regime %s produces policy %q, which is not in AllPolicyNames", regime, name)
		}
	}
	// backward
	for _, name := range entryplan.AllPolicyNames {
		if _, ok := used[name]; ok {
			continue
		}
		if entryplan.ReservedPolicyNames[name] {
			continue
		}
		t.Errorf("AllPolicyNames lists %q and no regime maps to it — an unreachable policy is "+
			"indistinguishable from a mapping someone forgot to write. If it is deliberate, "+
			"add it to ReservedPolicyNames with the reason", name)
	}

	// well-formed: unique, non-empty, stable SCREAMING_SNAKE
	seen := map[entryplan.PolicyName]bool{}
	for _, name := range entryplan.AllPolicyNames {
		if name == "" {
			t.Error("empty policy name in AllPolicyNames")
			continue
		}
		if seen[name] {
			t.Errorf("%q listed twice", name)
		}
		seen[name] = true
		for _, ch := range name {
			if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
				t.Errorf("%q is not a stable SCREAMING_SNAKE code", name)
				break
			}
		}
	}
	if entryplan.KnownPolicyName("NOT_A_POLICY") {
		t.Error("KnownPolicyName accepts a name that is not in the registry")
	}
	if len(entryplan.ReservedPolicyNames) != 0 {
		t.Errorf("ReservedPolicyNames is not empty: %v. Under EP2-v1 every name is in "+
			"production use; a reservation needs a stated purpose", entryplan.ReservedPolicyNames)
	}
}

// A regime string that no rule in internal/market can produce gets NO POLICY — not a fallback
// one, and specifically not UNRESOLVED.
//
// This is the "no default branch" property. A `default:` returning any policy would make the
// UNKNOWN row above dead code and would absorb every future regime silently.
func TestAnUndefinedRegimeGetsNoPolicyRatherThanAFallback(t *testing.T) {
	unknown, _ := entryplan.PolicyForRegime(model.RegimeUnknown)

	for _, r := range []model.Regime{"", "MELT_UP", "SIDEWAYS ", "bull", "UNKNOWN_", "NEUTRAL"} {
		if r.Valid() {
			t.Fatalf("%q is a valid model.Regime — this case is testing the wrong thing", r)
		}
		p, ok := entryplan.PolicyForRegime(r)
		if ok {
			t.Errorf("regime %q got policy %q — a decoded typo is not a market state, and "+
				"planning against it is planning against nothing", r, p.Name)
		}
		if p.Name == unknown.Name {
			t.Errorf("regime %q was answered with the UNKNOWN row's policy — \"not a regime\" "+
				"and \"the regime is unknown\" are different facts, and only one of them "+
				"means the pipeline is working", r)
		}
		if p.Name != "" {
			t.Errorf("regime %q produced the named policy %q with ok == false", r, p.Name)
		}

		// Through PolicyFor, the same input must be UNRESOLVED with NO policy attached.
		res := entryplan.PolicyFor(r, entryplan.EntrySemanticPullback)
		if res.Policy != nil {
			t.Errorf("PolicyFor(%q, PULLBACK) attached policy %q", r, res.Policy.Name)
		}
		if res.Allowance != entryplan.AllowanceUnresolved {
			t.Errorf("PolicyFor(%q, PULLBACK) allowance = %q, want UNRESOLVED", r, res.Allowance)
		}
		if res.Decided() {
			t.Errorf("PolicyFor(%q, PULLBACK) reports a decision", r)
		}
	}
}

// ── (regime, semantic): the shape the EP-2 sketch's own examples require ──────────────

// The three examples from the specification, plus the rest of the grid. This is what settles
// the table-vs-API shape question: the pair-dependence lives entirely in WHICH permission is
// read, so one six-row table plus a pure predicate reproduces it exactly.
func TestPolicyForDependsOnBothRegimeAndSemantic(t *testing.T) {
	cases := []struct {
		regime   model.Regime
		semantic entryplan.EntrySemantic
		want     entryplan.Allowance
		why      string
	}{
		{model.RegimeBull, entryplan.EntrySemanticBreakout, entryplan.AllowanceAllowed,
			"a breakout in a healthy uptrend is the trade"},
		{model.RegimeBull, entryplan.EntrySemanticPullback, entryplan.AllowanceAllowed,
			"so is buying the dip"},
		{model.RegimeBullPullback, entryplan.EntrySemanticBreakout, entryplan.AllowanceDisallowed,
			"buying a breakout INTO a correction pays up for the move being given back"},
		{model.RegimeBullPullback, entryplan.EntrySemanticPullback, entryplan.AllowanceAllowed,
			"the retracement IS the setup"},
		{model.RegimeSideways, entryplan.EntrySemanticBreakout, entryplan.AllowanceDisallowed,
			"a range breakout without an edge is a coin flip with slippage"},
		{model.RegimeSideways, entryplan.EntrySemanticPullback, entryplan.AllowanceAllowed,
			"near support the risk is at least definable"},
		{model.RegimeDistribution, entryplan.EntrySemanticBreakout, entryplan.AllowanceDisallowed,
			"internals are deteriorating; the breakout is the exit liquidity"},
		{model.RegimeDistribution, entryplan.EntrySemanticPullback, entryplan.AllowanceAllowed,
			"rare, and conditional on a raised bar — not forbidden"},
		{model.RegimeBear, entryplan.EntrySemanticBreakout, entryplan.AllowanceDisallowed,
			"a decision"},
		{model.RegimeBear, entryplan.EntrySemanticPullback, entryplan.AllowanceDisallowed,
			"also a decision"},
		{model.RegimeUnknown, entryplan.EntrySemanticBreakout, entryplan.AllowanceUnresolved,
			"no decision was made"},
		{model.RegimeUnknown, entryplan.EntrySemanticPullback, entryplan.AllowanceUnresolved,
			"nor here"},
	}
	for _, c := range cases {
		res := entryplan.PolicyFor(c.regime, c.semantic)
		if res.Allowance != c.want {
			t.Errorf("PolicyFor(%s, %s) = %q, want %q (%s)",
				c.regime, c.semantic, res.Allowance, c.want, c.why)
		}
		if res.Regime != c.regime || res.Semantic != c.semantic {
			t.Errorf("PolicyFor(%s, %s) reports (%s, %s) — the question was rewritten",
				c.regime, c.semantic, res.Regime, res.Semantic)
		}
		if res.Policy == nil {
			t.Errorf("PolicyFor(%s, %s) attached no policy for a canonical regime",
				c.regime, c.semantic)
		}
	}

	// The grid must be COMPLETE over (canonical regime x resolved semantic), so no pair can
	// be quietly left unasserted.
	if want := len(regimePolicyTable) * 2; len(cases) != want {
		t.Errorf("the grid covers %d pairs, want %d — a (regime, semantic) pair is unasserted",
			len(cases), want)
	}
}

// An UNKNOWN or undefined SEMANTIC is UNRESOLVED — even in BEAR, where the policy itself is a
// decision. "There is no question" and "the answer is no" must not collapse.
func TestAnAbsentSemanticIsUnresolvedEvenUnderADecidedPolicy(t *testing.T) {
	for _, sem := range []entryplan.EntrySemantic{
		entryplan.EntrySemanticUnknown, "", "SCALE_IN", "pullback", "BUY_NOW",
	} {
		if sem.Resolved() {
			t.Fatalf("%q reports Resolved() — this case is testing the wrong thing", sem)
		}
		for _, c := range regimePolicyTable {
			res := entryplan.PolicyFor(c.regime, sem)
			if res.Allowance != entryplan.AllowanceUnresolved {
				t.Errorf("PolicyFor(%s, %q) = %q, want UNRESOLVED — there is no entry "+
					"semantic to judge, which is not the same as judging it", c.regime, sem,
					res.Allowance)
			}
			if len(res.Requirements) != 0 {
				t.Errorf("PolicyFor(%s, %q) attached requirements %v to a question that was "+
					"never asked", c.regime, sem, res.Requirements)
			}
			// The POLICY is still reported for a canonical regime: the regime was known even
			// though the semantic was not, and a consumer has to be able to say which of the
			// two was missing.
			if res.Policy == nil {
				t.Errorf("PolicyFor(%s, %q) dropped the policy — a consumer can no longer "+
					"tell an unknown SEMANTIC from an unknown REGIME", c.regime, sem)
			} else if res.Policy.Name != c.policy {
				t.Errorf("PolicyFor(%s, %q) reported policy %q", c.regime, sem, res.Policy.Name)
			}
		}
	}

	// PermissionFor says the same thing with its ok flag, which is what PolicyFor reads.
	p, _ := entryplan.PolicyForRegime(model.RegimeBull)
	if _, ok := p.PermissionFor(entryplan.EntrySemanticUnknown); ok {
		t.Error("PermissionFor(UNKNOWN) claims to have an answer")
	}
	// And there is no route from any semantic to the buy-now permission: BUY_NOW is a TIMING
	// question EP-5 asks once a shape has been permitted, not an entry shape.
	//
	// Checked on the two regimes whose buy-now permission is DISTINGUISHABLE from both entry
	// shapes — BULL_PULLBACK (buy-now carries INSIDE_VALID_ZONE, its pullback carries nothing,
	// its breakout is refused) and DISTRIBUTION (two requirements vs one vs refused). On BULL
	// all three permissions are byte-identical, so asserting there would prove nothing.
	for _, regime := range []model.Regime{model.RegimeBullPullback, model.RegimeDistribution} {
		pol, ok := entryplan.PolicyForRegime(regime)
		if !ok {
			t.Fatalf("%s has no policy", regime)
		}
		if reflect.DeepEqual(pol.BuyNow, pol.Pullback) || reflect.DeepEqual(pol.BuyNow, pol.Breakout) {
			t.Fatalf("%s's buy-now permission is identical to one of its entry shapes, so "+
				"this test cannot detect a mis-route", regime)
		}
		for _, sem := range []entryplan.EntrySemantic{
			entryplan.EntrySemanticPullback, entryplan.EntrySemanticBreakout,
			entryplan.EntrySemanticUnknown, "BUY_NOW", "",
		} {
			perm, ok := pol.PermissionFor(sem)
			if ok && reflect.DeepEqual(perm, pol.BuyNow) {
				t.Errorf("%s: semantic %q was routed to the buy-now permission — a shape has "+
					"been answered with a timing decision", regime, sem)
			}
			if !ok && !reflect.DeepEqual(perm, entryplan.Permission{}) {
				t.Errorf("%s: PermissionFor(%q) returned ok == false and the permission %+v — "+
					"a caller ignoring the flag would read a real answer", regime, sem, perm)
			}
		}
	}
}

// ── the three answers, and no fourth ──────────────────────────────────────────────────

// EP-2 answers ALLOWED / DISALLOWED / UNRESOLVED. It does not produce an EntryStatus, and no
// allowance may be spelled like one: BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT need a price
// relative to a level, and this layer is not given a price.
func TestTheOnlyAnswersAreAllowedDisallowedUnresolved(t *testing.T) {
	all := []entryplan.Allowance{
		entryplan.AllowanceAllowed, entryplan.AllowanceDisallowed, entryplan.AllowanceUnresolved,
	}
	for _, a := range all {
		if !a.Valid() {
			t.Errorf("%q is not Valid()", a)
		}
		if entryplan.EntryStatus(a).Valid() {
			t.Errorf("the allowance %q is also a valid EntryStatus — the permission layer has "+
				"started producing the decide layer's vocabulary", a)
		}
	}
	if entryplan.AllowanceAllowed.Decided() != true ||
		entryplan.AllowanceDisallowed.Decided() != true ||
		entryplan.AllowanceUnresolved.Decided() != false {
		t.Error("Decided() no longer separates the two conclusions from the absence of one")
	}
	for _, bad := range []entryplan.Allowance{
		"", "CONDITIONAL", "MAYBE", "allowed", "BUY_NOW", "WAIT_PULLBACK", "NO_VALID_ENTRY",
	} {
		if bad.Valid() {
			t.Errorf("%q accepted as an allowance", bad)
		}
	}

	// Over the WHOLE grid, including the invalid inputs, only those three ever come back.
	for _, r := range []model.Regime{
		model.RegimeBull, model.RegimeBullPullback, model.RegimeSideways,
		model.RegimeDistribution, model.RegimeBear, model.RegimeUnknown, "", "MELT_UP",
	} {
		for _, sem := range []entryplan.EntrySemantic{
			entryplan.EntrySemanticPullback, entryplan.EntrySemanticBreakout,
			entryplan.EntrySemanticUnknown, "", "SCALE_IN",
		} {
			got := entryplan.PolicyFor(r, sem).Allowance
			if !got.Valid() {
				t.Errorf("PolicyFor(%q, %q) answered %q", r, sem, got)
			}
		}
	}
}

// ── requirements ──────────────────────────────────────────────────────────────────────

// A requirement may only hang off an ALLOWED permission. On a DISALLOWED one it would read as
// "satisfy this and it is fine", which is the opposite of what the policy said; on an
// UNRESOLVED one it would be a condition attached to a decision nobody made.
//
// The registry is also checked from both ends, and the table must actually USE requirements —
// otherwise the concept is decoration and BULL_PULLBACK's "only inside the zone" has been
// silently downgraded to a plain "allowed".
func TestRequirementsOnlyEverQualifyAnAllowedEntry(t *testing.T) {
	known := map[entryplan.Requirement]bool{}
	for _, r := range entryplan.AllRequirements {
		known[r] = true
	}
	used := map[entryplan.Requirement]bool{}
	var attached int

	for _, c := range regimePolicyTable {
		p, ok := entryplan.PolicyForRegime(c.regime)
		if !ok {
			continue
		}
		for _, perm := range []struct {
			what string
			p    entryplan.Permission
		}{{"buy now", p.BuyNow}, {"pullback", p.Pullback}, {"breakout", p.Breakout}} {
			if len(perm.p.Requirements) > 0 && perm.p.Allowance != entryplan.AllowanceAllowed {
				t.Errorf("%s's %s permission is %q and carries requirements %v — a condition "+
					"on a refused or undecided permission reads as \"do this and it is fine\"",
					c.regime, perm.what, perm.p.Allowance, perm.p.Requirements)
			}
			seen := map[entryplan.Requirement]bool{}
			for _, req := range perm.p.Requirements {
				attached++
				used[req] = true
				if !known[req] {
					t.Errorf("%s's %s permission carries %q, which is not in AllRequirements",
						c.regime, perm.what, req)
				}
				if seen[req] {
					t.Errorf("%s's %s permission repeats %q", c.regime, perm.what, req)
				}
				seen[req] = true
			}
		}
	}

	if attached == 0 {
		t.Fatal("no permission in the whole table carries a requirement — the table's " +
			"\"only inside a valid zone\" / \"only near support\" / \"rare\" columns have " +
			"been flattened into a plain ALLOWED, and this test is now vacuous")
	}
	for _, r := range entryplan.AllRequirements {
		if !used[r] {
			t.Errorf("AllRequirements lists %q and no policy attaches it — a condition EP-3 "+
				"would write a branch for and never reach", r)
		}
	}

	// The specific rows the EP-2 table calls out, so a flattened column is named rather than
	// merely counted.
	bp, _ := entryplan.PolicyForRegime(model.RegimeBullPullback)
	if !hasRequirement(bp.BuyNow.Requirements, entryplan.RequirementInsideZone) {
		t.Errorf("BULL_PULLBACK buy-now requirements = %v, want INSIDE_VALID_ZONE — the table "+
			"says buying at the market is permitted ONLY inside the zone", bp.BuyNow.Requirements)
	}
	sw, _ := entryplan.PolicyForRegime(model.RegimeSideways)
	if !hasRequirement(sw.BuyNow.Requirements, entryplan.RequirementNearSupport) ||
		!hasRequirement(sw.Pullback.Requirements, entryplan.RequirementNearSupport) {
		t.Error("SIDEWAYS is NEAR_SUPPORT_ONLY and one of its allowed entries does not " +
			"require support")
	}
	di, _ := entryplan.PolicyForRegime(model.RegimeDistribution)
	if !hasRequirement(di.Pullback.Requirements, entryplan.RequirementElevatedEvidence) {
		t.Error("DISTRIBUTION pullbacks are CONDITIONAL and carry no elevated-evidence bar")
	}
	if len(di.BuyNow.Requirements) <= len(di.Pullback.Requirements) {
		t.Errorf("DISTRIBUTION buy-now (%v) is not held to a higher bar than its pullback "+
			"(%v) — the table says RARE vs CONDITIONAL",
			di.BuyNow.Requirements, di.Pullback.Requirements)
	}
	// And a requirement reaches the RESULT, which is what EP-3 actually reads.
	res := entryplan.PolicyFor(model.RegimeDistribution, entryplan.EntrySemanticPullback)
	if !hasRequirement(res.Requirements, entryplan.RequirementElevatedEvidence) {
		t.Errorf("PolicyFor(DISTRIBUTION, PULLBACK).Requirements = %v — the condition did not "+
			"survive the trip to the consumer", res.Requirements)
	}
}

func hasRequirement(rs []entryplan.Requirement, want entryplan.Requirement) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

// ── the chase multiplier: MISSING ≠ ZERO, one more time ───────────────────────────────

// MaxATR != nil ⟺ Allowance == ALLOWED, and a refused chase is nil rather than 0.
//
// 0.0 would say "chasing is permitted, by exactly nothing", which is a tolerance nobody set
// and which an EP-3 expression would happily multiply by an ATR to produce the ideal entry as
// the maximum chase price — a plausible-looking number with no author.
func TestTheChaseMultiplierIsAbsentRatherThanZero(t *testing.T) {
	var allowed, refused int
	for _, c := range regimePolicyTable {
		p, ok := entryplan.PolicyForRegime(c.regime)
		if !ok {
			continue
		}
		switch p.Chase.Allowance {
		case entryplan.AllowanceAllowed:
			allowed++
			if p.Chase.MaxATR == nil {
				t.Errorf("%s allows chasing and names no multiplier", c.regime)
				continue
			}
			if v := *p.Chase.MaxATR; !(v > 0) {
				t.Errorf("%s chase multiplier = %v, want strictly positive — 0 is not a "+
					"tolerance, it is the absence of one", c.regime, v)
			}
		default:
			refused++
			if p.Chase.MaxATR != nil {
				t.Errorf("%s chase allowance is %q and it still carries the multiplier %v",
					c.regime, p.Chase.Allowance, *p.Chase.MaxATR)
			}
		}
	}
	if allowed == 0 || refused == 0 {
		t.Fatalf("the table has %d chase-allowing and %d chase-refusing rows — one half of "+
			"this test never ran", allowed, refused)
	}

	// The ORDERING is the deliberate part; the magnitudes are heuristics and are not pinned.
	bull, _ := entryplan.PolicyForRegime(model.RegimeBull)
	bp, _ := entryplan.PolicyForRegime(model.RegimeBullPullback)
	if bull.Chase.MaxATR == nil || bp.Chase.MaxATR == nil {
		t.Fatal("BULL and BULL_PULLBACK must both permit some chasing")
	}
	if !(*bp.Chase.MaxATR < *bull.Chase.MaxATR) {
		t.Errorf("BULL_PULLBACK tolerates chasing %v vs BULL's %v — the pullback regime's "+
			"whole premise is that price is coming back to you, so it must be the more "+
			"conservative of the two", *bp.Chase.MaxATR, *bull.Chase.MaxATR)
	}
}

// ── purity, and no shared state ───────────────────────────────────────────────────────

// The table is a FUNCTION, not a shared variable. Two calls must be equal and must not share
// the pointer or the slice, or a consumer that adjusts its own copy would rewrite the policy
// for every later caller in the process.
func TestThePolicyTableIsPureAndHandsOutNoSharedState(t *testing.T) {
	for _, c := range regimePolicyTable {
		a, _ := entryplan.PolicyForRegime(c.regime)
		b, _ := entryplan.PolicyForRegime(c.regime)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two calls disagree:\n%+v\n%+v", c.regime, a, b)
		}
		if a.Chase.MaxATR != nil {
			if a.Chase.MaxATR == b.Chase.MaxATR {
				t.Errorf("%s: both calls returned the SAME chase pointer — a caller writing "+
					"through it would rewrite the table", c.regime)
			}
			*a.Chase.MaxATR = 99
			if b2, _ := entryplan.PolicyForRegime(c.regime); *b2.Chase.MaxATR == 99 {
				t.Errorf("%s: mutating a returned chase multiplier changed the table", c.regime)
			}
		}
		if len(a.BuyNow.Requirements) > 0 {
			a.BuyNow.Requirements[0] = "MUTATED"
			if b2, _ := entryplan.PolicyForRegime(c.regime); b2.BuyNow.Requirements[0] == "MUTATED" {
				t.Errorf("%s: the requirements slice is shared with the table", c.regime)
			}
		}
	}

	// PolicyFor is deterministic over the whole grid, invalid inputs included.
	for _, r := range []model.Regime{
		model.RegimeBull, model.RegimeBear, model.RegimeUnknown, "", "MELT_UP",
	} {
		for _, sem := range []entryplan.EntrySemantic{
			entryplan.EntrySemanticPullback, entryplan.EntrySemanticBreakout,
			entryplan.EntrySemanticUnknown, "",
		} {
			if !reflect.DeepEqual(entryplan.PolicyFor(r, sem), entryplan.PolicyFor(r, sem)) {
				t.Errorf("PolicyFor(%q, %q) is not deterministic", r, sem)
			}
		}
	}
}

// ── the policy layer cannot see a price ───────────────────────────────────────────────

// Structural, by reflection over the SIGNATURE and the RESULT type.
//
// EP-2's scope is "no entry price is computed", and the cheapest way to make that true is for
// the layer to be incapable of holding one: no PriceZone, no Invalidation, no PriceLevel, no
// RiskReward, no PriceBasis anywhere in PolicyResult, and no price of any kind among
// PolicyFor's inputs. A future item that starts computing zones here has to change this test
// first, which is the point at which someone notices.
func TestThePolicyLayerCannotSeeAPrice(t *testing.T) {
	priceTypes := map[reflect.Type]string{
		reflect.TypeOf(entryplan.PriceZone{}):        "PriceZone",
		reflect.TypeOf(entryplan.Invalidation{}):     "Invalidation",
		reflect.TypeOf(entryplan.PriceLevel{}):       "PriceLevel",
		reflect.TypeOf(entryplan.RiskReward{}):       "RiskReward",
		reflect.TypeOf(entryplan.PriceObservation{}): "PriceObservation",
		reflect.TypeOf(entryplan.Snapshot{}):         "Snapshot",
		reflect.TypeOf(entryplan.PriceBasis("")):     "PriceBasis",
	}

	fn := reflect.TypeOf(entryplan.PolicyFor)
	if fn.NumIn() != 2 {
		t.Fatalf("PolicyFor takes %d arguments — it is a function of (regime, semantic) and "+
			"nothing else", fn.NumIn())
	}
	if fn.In(0) != reflect.TypeOf(model.Regime("")) || fn.In(1) != reflect.TypeOf(entryplan.EntrySemantic("")) {
		t.Errorf("PolicyFor's signature is (%s, %s), want (model.Regime, EntrySemantic)",
			fn.In(0), fn.In(1))
	}
	for n := 0; n < fn.NumIn(); n++ {
		if name, bad := priceTypes[fn.In(n)]; bad {
			t.Errorf("PolicyFor takes a %s — the permission layer can see a price, so it can "+
				"decide with one", name)
		}
		if fn.In(n).Kind() == reflect.Float64 || fn.In(n).Kind() == reflect.Float32 {
			t.Errorf("PolicyFor takes a bare %s", fn.In(n).Kind())
		}
	}

	var found int
	walkTypes(reflect.TypeOf(entryplan.PolicyResult{}), map[reflect.Type]bool{}, func(ty reflect.Type) {
		found++
		if name, bad := priceTypes[ty]; bad {
			t.Errorf("PolicyResult reaches a %s — EP-2 must not be able to hold an entry "+
				"price, let alone compute one", name)
		}
	})
	if found < 8 {
		t.Errorf("the type walk reached only %d types from PolicyResult — it is not "+
			"descending and would pass vacuously", found)
	}
	// Non-vacuity: the same walk MUST flag a type that does carry prices.
	var flagged bool
	walkTypes(reflect.TypeOf(entryplan.Plan{}), map[reflect.Type]bool{}, func(ty reflect.Type) {
		if _, bad := priceTypes[ty]; bad {
			flagged = true
		}
	})
	if !flagged {
		t.Error("the walk does not find a price-bearing type inside Plan, so finding none " +
			"inside PolicyResult means nothing")
	}
}

// walkTypes visits every type reachable from ty, through pointers, slices, arrays, maps and
// struct fields.
func walkTypes(ty reflect.Type, seen map[reflect.Type]bool, visit func(reflect.Type)) {
	if ty == nil || seen[ty] {
		return
	}
	seen[ty] = true
	visit(ty)
	switch ty.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		walkTypes(ty.Elem(), seen, visit)
	case reflect.Map:
		walkTypes(ty.Key(), seen, visit)
		walkTypes(ty.Elem(), seen, visit)
	case reflect.Struct:
		for n := 0; n < ty.NumField(); n++ {
			walkTypes(ty.Field(n).Type, seen, visit)
		}
	}
}

// ── the entry semantic vocabulary ─────────────────────────────────────────────────────

// entrySemanticConst matches an EntrySemantic constant declaration in policy.go.
var entrySemanticConst = regexp.MustCompile(`(?m)^\s*(\w+)\s+EntrySemantic\s*=\s*"([A-Z_]+)"`)

// The semantic set, and the scanner vocabulary it is a reading of. The MAPPING is EP-5's work
// item — entryplan may not import internal/scanner — so what is pinned here is that the target
// values exist, are distinct, and route absence to a STATE rather than to a refusal.
//
// The SET ITSELF is pinned from three sides: policy.go's own source, the exported registry, and
// the three names EP-2 decided on. That is a repair, not belt-and-braces. This test used to
// build a three-element local slice and then assert len() over it, so `len(seen) != 3` was a
// statement about the test's own literal and could not fail: adding a fourth constant and
// teaching Valid() to accept it left the entire suite green, which made policy.go's "Why there
// is no fourth NOT_AN_ENTRY value" section an unenforced opinion. The registry and the source
// scan are the same discipline TestEveryRegimeInTheModelHasAPolicy and
// TestPolicyNameRegistryIsExactlyWhatTheTableProduces already apply; EntrySemantic was the one
// vocabulary in this file that had neither.
func TestTheEntrySemanticSetIsExactlyPullbackBreakoutUnknown(t *testing.T) {
	// The set as PRODUCTION declares it, read off policy.go's source rather than retyped here.
	src, err := os.ReadFile("policy.go")
	if err != nil {
		t.Fatalf("cannot read the semantic vocabulary: %v", err)
	}
	matches := entrySemanticConst.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 3 {
		t.Fatalf("found %d EntrySemantic constants in policy.go — the scan is not reading the "+
			"vocabulary and would pass for the wrong reason", len(matches))
	}
	declared := map[entryplan.EntrySemantic]string{}
	for _, m := range matches {
		declared[entryplan.EntrySemantic(m[2])] = m[1]
	}

	// The registry, well-formed: unique, non-empty, stable SCREAMING_SNAKE — the same three
	// checks AllPolicyNames and AllReasons get.
	seen := map[entryplan.EntrySemantic]bool{}
	for _, s := range entryplan.AllEntrySemantics {
		if s == "" {
			t.Error("empty semantic in AllEntrySemantics")
			continue
		}
		if seen[s] {
			t.Errorf("%q listed twice", s)
		}
		seen[s] = true
		for _, ch := range s {
			if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
				t.Errorf("%q is not a stable SCREAMING_SNAKE code", s)
				break
			}
		}
	}

	// forward: every constant production declares is in the registry, and is Valid()
	for sem, goName := range declared {
		if !seen[sem] {
			t.Errorf("policy.go declares %s (%q) and AllEntrySemantics does not list it — a "+
				"semantic a producer can emit and no consumer can enumerate", goName, sem)
		}
		if !sem.Valid() {
			t.Errorf("%s (%q) is declared and EntrySemantic.Valid() rejects it", goName, sem)
		}
		if !entryplan.KnownEntrySemantic(sem) {
			t.Errorf("%s (%q) is declared and KnownEntrySemantic rejects it — Valid() and the "+
				"registry have stopped being one list, which is what defining Valid() in terms of "+
				"the registry was for", goName, sem)
		}
	}
	// backward: no dead registry entry
	for _, sem := range entryplan.AllEntrySemantics {
		if _, ok := declared[sem]; !ok {
			t.Errorf("AllEntrySemantics lists %q and policy.go declares no constant for it — a "+
				"branch a consumer writes and never reaches", sem)
		}
	}
	if len(declared) != len(entryplan.AllEntrySemantics) {
		t.Errorf("policy.go declares %d semantics %v and AllEntrySemantics has %d members %v",
			len(declared), declared, len(entryplan.AllEntrySemantics), entryplan.AllEntrySemantics)
	}

	// And the set is EXACTLY these three. THE decision this test's name makes, so a fourth
	// value fails here even if it is added consistently in every place above.
	want := []entryplan.EntrySemantic{
		entryplan.EntrySemanticPullback,
		entryplan.EntrySemanticBreakout,
		entryplan.EntrySemanticUnknown,
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("%q is no longer in the semantic set", w)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("the semantic set is %v (%d members), want exactly %v — a value was added "+
			"without deciding whether it is an entry SHAPE or the absence of one. policy.go "+
			"argues at length that there is no fourth value; if that changed, change the "+
			"argument and this list together",
			entryplan.AllEntrySemantics, len(seen), want)
	}
	if !entryplan.EntrySemanticPullback.Resolved() || !entryplan.EntrySemanticBreakout.Resolved() {
		t.Error("an entry shape does not report Resolved()")
	}
	if entryplan.EntrySemanticUnknown.Resolved() {
		t.Error("UNKNOWN reports Resolved() — it is the honest \"we cannot say which\", and " +
			"a policy consulted about it would be answering an unasked question")
	}
	for _, bad := range []entryplan.EntrySemantic{
		"", "pullback", "BUY_NOW", "TAKE_PROFIT", "NOT_AN_ENTRY", "PREPARE_ENTRY",
	} {
		if bad.Valid() {
			t.Errorf("%q accepted as an entry semantic", bad)
		}
	}

	// The evidence wrapper keeps the two absences apart, exactly as RegimeEvidence does.
	cases := []struct {
		in     entryplan.EntrySemanticEvidence
		usable bool
	}{
		{entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticPullback}, true},
		{entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticBreakout}, true},
		// computed, and could not say
		{entryplan.EntrySemanticEvidence{Status: entryplan.Available,
			Semantic: entryplan.EntrySemanticUnknown}, false},
		// never handed to us
		{entryplan.EntrySemanticEvidence{Status: entryplan.Unavailable,
			Semantic: entryplan.EntrySemanticPullback}, false},
		{entryplan.EntrySemanticEvidence{}, false},
		// a label from an older vocabulary
		{entryplan.EntrySemanticEvidence{Status: entryplan.Available, Semantic: "SCALE_IN"}, false},
	}
	for _, c := range cases {
		if got := c.in.Usable(); got != c.usable {
			t.Errorf("EntrySemanticEvidence%+v.Usable() = %v, want %v", c.in, got, c.usable)
		}
	}
}

// scannerWatchActionConst matches a WatchAction constant declaration in internal/scanner.
//
// Same shape as modelRegimeConst above, and the same reason: the constant's VALUE is what ends
// up in an archived row, and `= "..."` is what distinguishes a declaration from the struct
// field of the same type (rocket.go has one).
//
// READING THE FILE IS NOT IMPORTING IT. This test opens ../scanner/rocket.go as BYTES; the
// entryplan package and its test binary still have no edge to internal/scanner, so
// TestTheEntryPlanLayerImportsNoDecisionOrIOPackage (which asks `go list -deps`) is unaffected,
// and TestTheEntryPlanLayerStaysPure skips *_test.go by design — "A test may read the source
// tree — that is how it checks the source tree. ComputePlan may not read anything." Importing
// the scanner would give this layer the second opinion doc.go forbids; reading its source is
// how a claim ABOUT its source gets checked, which is exactly what
// TestEveryRegimeInTheModelHasAPolicy does to ../market/model/regime.go.
// The optional `const ` prefix is load-bearing: without it the pattern matches only
// declarations inside a `const ( ... )` block, and a single-line `const ActX WatchAction =
// "X"` slips past while the floor below is still met by rocket.go's seven. Measured — that
// exact shape, in another file of internal/scanner, was green before this alternation.
var scannerWatchActionConst = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s+WatchAction\s*=\s*"([A-Z_]+)"`)

// The scanner → semantic mapping is DOCUMENTED in policy.go and implemented nowhere, and this
// test holds both halves: the note names every WatchAction the scanner can emit, and this
// package still does not import the scanner.
//
// It is checked against the SCANNER'S SOURCE rather than against a list retyped here, because
// the value of the note is that EP-3 does not have to re-derive pullback-vs-breakout from a
// chart — and a note that has silently fallen behind the scanner's vocabulary is worse than
// none. The retyped list was the bug: this test used to iterate seven string literals of its
// own and only ever proved that policy.go still mentioned those seven, so an EIGHTH
// WatchAction added to rocket.go was unmapped, unmentioned, and green everywhere.
func TestTheScannerToSemanticMappingIsWrittenDownButNotImplemented(t *testing.T) {
	src, err := os.ReadFile("policy.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// Every WatchAction the scanner DECLARES must appear in the note, so a new one cannot be
	// left unmapped without this test noticing.
	// The WHOLE package directory, not just rocket.go. All seven constants live in rocket.go
	// today, and reading only that file would have found all seven — passing the floor below —
	// while missing an eighth declared in any other file of internal/scanner. A guard whose
	// claim is "every WatchAction the scanner declares" has to look everywhere the scanner
	// could declare one, or the claim is the narrower thing it actually checks.
	entries, err := os.ReadDir("../scanner")
	if err != nil {
		t.Fatalf("cannot read internal/scanner: %v — point this sweep at wherever the "+
			"WatchAction constants live now, rather than dropping the check", err)
	}
	var declared [][]string
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join("../scanner", name))
		if err != nil {
			t.Fatalf("read ../scanner/%s: %v", name, err)
		}
		scanned++
		declared = append(declared, scannerWatchActionConst.FindAllStringSubmatch(string(src), -1)...)
	}
	if scanned < 10 {
		t.Fatalf("scanned only %d production files in internal/scanner — the sweep is not "+
			"seeing the package and would pass for the wrong reason", scanned)
	}
	if len(declared) < 7 {
		t.Fatalf("found %d WatchAction constants across internal/scanner — the sweep is "+
			"not seeing the vocabulary (constants moved? declaration reshaped? value no longer "+
			"SCREAMING_SNAKE?) and would pass for the wrong reason. Fix the scan; do not "+
			"delete it", len(declared))
	}
	for _, m := range declared {
		goName, wire := m[1], m[2]
		if !strings.Contains(body, goName) {
			t.Errorf("policy.go's scanner → semantic note does not mention scanner.%s (%q) — "+
				"EP-5 would have to guess what that action means for an entry plan", goName, wire)
		}
		if !strings.Contains(body, wire) {
			t.Errorf("policy.go's note does not quote scanner.%s's value %q — the note reads as "+
				"a mapping of wire codes and one of them is now stale", goName, wire)
		}
	}
	// And the note is a note: no import, no adapter. (The dependency itself is enforced by
	// architecture_test.go; this catches the import being added to THIS file.)
	if strings.Contains(body, "internal/scanner\"") {
		t.Error("policy.go imports internal/scanner — the wiring now runs backwards")
	}
}

// TestEveryRegimeStatesItsImmediateChasePermission closes the gap the EP-5 implementer
// disclosed rather than hid: the Immediate column was added to ChasePolicy, six rows were
// filled in, and NOTHING asserted that a seventh row would have to fill it too.
//
// WHY THE ZERO VALUE IS THE DANGEROUS CASE, and why this is not merely tidiness. Allowance is
// a string, so an omitted Immediate is "" — and "" is neither ALLOWED nor DISALLOWED nor
// UNRESOLVED. EP-5's decide layer reads a non-ALLOWED immediate permission that is also not
// DISALLOWED as undecided, so an omitted field arrives at the caller as INSUFFICIENT_DATA:
// "we could not read whether this market permits paying up". For BULL_PULLBACK the truth is
// the opposite and it is a DECISION — that row's whole point is that a chase CEILING is not a
// chase PERMISSION. Turning that refusal into a shrug is exactly the collapse EP-2 exists to
// prevent, and it is the collapse a missing line produces.
//
// MEASURED, not argued: deleting BULL_PULLBACK's Immediate line leaves the entire package
// green without this test. With it, the same deletion names the row and says what "" means.
func TestEveryRegimeStatesItsImmediateChasePermission(t *testing.T) {
	// Driven off regimePolicyTable — the same declaration the spec table test reads — rather
	// than a second list of regimes, which would be one more thing to forget to update.
	var checked int
	for _, c := range regimePolicyTable {
		r := c.regime
		p, ok := entryplan.PolicyForRegime(r)
		if !ok {
			continue // TestEveryRegimeInTheModelHasAPolicy owns that failure
		}
		checked++
		got := p.Chase.Immediate.Allowance
		if !got.Valid() {
			t.Errorf("%s: Chase.Immediate.Allowance = %q, which is not one of the three "+
				"answers. An omitted field reads as undecided, so this regime would tell a "+
				"caller \"we could not read whether paying up is permitted\" when the table "+
				"was simply never asked", r, got)
			continue
		}
		// The one-directional invariant the field's own doc states. Checked here rather than
		// trusted, because it is the half that cannot be recovered from the ceiling: a
		// permission to pay at the market is strictly stronger than a tolerance for slippage.
		if got == entryplan.AllowanceAllowed && p.Chase.Allowance != entryplan.AllowanceAllowed {
			t.Errorf("%s: immediate chasing is ALLOWED while the chase tolerance is %q — a "+
				"licence to buy above the band with no ceiling bounding how far above",
				r, p.Chase.Allowance)
		}
		// And the converse must NOT be assumed. BULL_PULLBACK is the live counterexample; if
		// it ever stops being one, the doc on ChasePolicy.Immediate is no longer true.
		if r == model.RegimeBullPullback {
			if p.Chase.Allowance != entryplan.AllowanceAllowed {
				t.Errorf("BULL_PULLBACK no longer tolerates chasing (%q), so it is no longer "+
					"the counterexample ChasePolicy.Immediate's doc cites", p.Chase.Allowance)
			}
			if got != entryplan.AllowanceDisallowed {
				t.Errorf("BULL_PULLBACK's immediate permission = %q, want DISALLOWED — the "+
					"row exists to show a ceiling is not a permission, and a REFUSAL is not "+
					"an ABSENCE: a shrug here would route to INSUFFICIENT_DATA", got)
			}
		}
		// UNKNOWN is the only row allowed to be undecided, and it must BE undecided.
		if r == model.RegimeUnknown && got != entryplan.AllowanceUnresolved {
			t.Errorf("UNKNOWN's immediate permission = %q, want UNRESOLVED — DISALLOWED here "+
				"would turn \"we could not read the market\" into \"the market forbids paying "+
				"up\", and EP-5 would publish NO_VALID_ENTRY where INSUFFICIENT_DATA is true",
				got)
		}
		// BEAR refuses every shape; an undecided immediate permission would leak a state into
		// the one regime that is entirely verdicts.
		if r == model.RegimeBear && got != entryplan.AllowanceDisallowed {
			t.Errorf("BEAR's immediate permission = %q, want DISALLOWED — BEAR is a decided "+
				"refusal on every execution shape", got)
		}
	}
	if checked < 6 {
		t.Fatalf("only %d regimes were examined — the sweep is not seeing the vocabulary and "+
			"would pass for the wrong reason", checked)
	}
}
