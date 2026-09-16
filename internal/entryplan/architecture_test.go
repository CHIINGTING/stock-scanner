package entryplan_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The direction of the wiring, enforced rather than asserted.
//
//	scanner output / evidence  →  entryplan (post-pass)  →  report
//
// entryplan answers "at what price", AFTER something else has already answered "is this worth
// owning". If the scanner could reach a price plan while deciding, the plan would be an input
// to the very verdict it is downstream of — and a scoring layer would start to depend on
// whether a tidy entry level happened to exist, which is a property of the chart, not of the
// company.
//
// # Why internal/report is ALLOWED here and FORBIDDEN in R15
//
// internal/derivatives/architecture_test.go lists ./internal/report among the packages that
// must never reach it. Same repo, opposite answer, and the difference is not taste:
//
//   - R15 is a DEFAULT-OFF RESEARCH LAYER whose isolation property is that no consumer can
//     reach `derivatives/acquire`, its ONLY networked package. Report generation must answer
//     from what is already archived, so a report that could reach R15 could open a socket
//     during rendering. The forbidden set there is "every R15 package, without exception",
//     precisely because the argument "this one opens no sockets" had already let `provider`
//     off the list once, and `acquire` turned out to be reachable through it.
//   - entryplan is a PURE DOMAIN PACKAGE with no network, no database, no clock and no
//     filesystem — asserted by TestTheEntryPlanLayerStaysPure below and by its own dependency
//     list. Rendering an entry plan is exactly what it is for. Forbidding report here would
//     forbid the feature.
//
// The line drawn here is therefore not "who may render it" but "who may DECIDE with it", and
// it falls between the decision packages and the presentation ones.
//
// R15 already had to make the ./internal/scanner vs ./cmd/scanner version of this call
// (architecture_test.go:29-32: cmd/scanner → internal/report → internal/derivatives is the
// INTENDED wiring, so asserting it at cmd/scanner would fail by design). Same reasoning,
// one package further along: cmd/scanner → internal/report → internal/entryplan is intended.
//
// EP-6A changed the second half of that sentence, which used to read "and internal/scanner →
// internal/entryplan is not". It now IS intended — the plan is attached to a WatchlistEntry
// field by a post-pass — and the rule this file exists for survives only as a rule about
// SYMBOLS rather than packages. See the note on `consumers` for exactly what stopped being
// enforced and which item owns the replacement.

// consumers are the DECISION packages. None of them may reach entryplan.
//
// Deliberately NOT including ./internal/report or ./cmd/scanner — see above. Those are the
// intended consumers, and listing them would assert against the design.
//
// The internal/market entries are EP-2's addition, and they are the direction half of the
// rule: market/model → entryplan, never the reverse. entryplan reuses model.Regime and
// model.InstitutionalPosture, and it must stay the one that reaches — a regime engine that
// could see an entry policy would be deciding the market's state partly by whether a tidy
// entry happened to be permitted in it. internal/market/analyzer and service are the regime
// and REPLAY implementations named in EP-2's own architecture rule.
//
// # EP-6A REMOVED ./internal/scanner AND ./internal/candidate FROM THIS LIST
//
// This is a WEAKENING and it is recorded as one. EP-6 attaches the plan to
// scanner.WatchlistEntry — a dedicated field outside ShadowSignals, written by the
// AttachEntryPlan post-pass after the watchlist sort is already final — and a struct field
// of type *entryplan.Plan is a package-level import no matter how shadow-only the field is.
// So this test's MECHANISM (`go list -deps`) can no longer express its RULE ("the decision
// path must not consult an entry plan"): the dependency is now a fact of the design, and the
// question has become which SYMBOLS inside internal/scanner may read the field.
//
// ./internal/candidate is here for a second-order reason and not because anything about it
// changed: it imports internal/scanner, so it reaches entryplan transitively through exactly
// the edge above. It has no direct import of its own, and acquiring one would be a genuine
// violation that nothing currently checks.
//
// WHAT REPLACED IT, and how much weaker it is. EP-6A added a file-level AST scan
// (internal/scanner/entryplan_isolation_test.go: only entryplan_attach.go may contain a
// `.EntryPlan` selector). EP-6D added a FUNCTION-level, repo-wide one
// (internal/scanner/entryplan_symbol_guard_test.go: across every non-test .go file under cmd/
// and internal/, the only functions containing a `.EntryPlan` selector are AttachEntryPlan and,
// since EP-7, the write-only research projection buildEntryPlanEvidence). Both
// are STRUCTURAL: they see a selector named EntryPlan in source, not a read through reflection,
// encoding/json, or a helper handed a whole WatchlistEntry. EP-6B's OFF-vs-ON shadow diff
// (cmd/scanner) remains the behavioural evidence that nothing on the watchlist path reads it.
//
// The four packages left below still carry the rule where it can still be stated
// structurally: none of them may reach entryplan at all, and none of them depends on
// internal/scanner, so removing the two entries above did not quietly retire them too.
var consumers = []string{
	"./internal/ai",
	"./internal/market/model",
	"./internal/market/analyzer",
	"./internal/market/service",
	"./internal/market/provider",
}

// forbidden is the entryplan package as `go list -deps` names it.
const entryplanPkg = "github.com/deep-huang/stock-scanner/internal/entryplan"

// allowedConsumers are the packages that MAY depend on entryplan. Listed so the reasoning is
// in the file rather than only in this comment, and existence-checked below so a rename does
// not leave the argument pointing at nothing.
//
// ./internal/scanner is EP-6A's addition: it OWNS the projection (entryplan_attach.go) and
// the field the plan is attached to, so it must be able to name the type. ./internal/candidate
// follows it transitively and holds no import of its own — see the note on `consumers`.
var allowedConsumers = []string{
	"./internal/report",
	"./cmd/scanner",
	"./internal/scanner",
	"./internal/candidate",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func deps(t *testing.T, root, pkg string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			got[s] = true
		}
	}
	return got
}

// The decision path must not be able to consult an entry plan.
func TestTheDecisionPathCannotReachTheEntryPlanLayer(t *testing.T) {
	root := repoRoot(t)
	for _, consumer := range consumers {
		t.Run(strings.TrimPrefix(consumer, "./internal/"), func(t *testing.T) {
			if deps(t, root, consumer)[entryplanPkg] {
				t.Errorf("%s depends on %s — the entry PRICE has become an input to the "+
					"decision that there is a trade at all, and the wiring "+
					"scanner → entryplan → report now runs backwards", consumer, entryplanPkg)
			}
		})
	}
}

// The reverse guard. entryplan must not import the decision layer, the presentation layer,
// the AI layer, the persistence layer, or any binary.
//
// Without this, the test above could be satisfied from the wrong side: entryplan importing
// internal/scanner creates the same cycle of authority, merely pointing the other way, and
// `go list -deps ./internal/scanner` would still be clean.
var forbiddenImports = []string{
	"github.com/deep-huang/stock-scanner/internal/scanner",
	"github.com/deep-huang/stock-scanner/internal/candidate",
	"github.com/deep-huang/stock-scanner/internal/report",
	"github.com/deep-huang/stock-scanner/internal/ai",
	"github.com/deep-huang/stock-scanner/internal/store",
	"github.com/deep-huang/stock-scanner/cmd/scanner",
	// EP-6D, EP-6 brief §12/§22: the valuation IMPLEMENTATION. entryplan receives a
	// ValuationEvidence projection and must never reach the package that produces it — that
	// package depends on os and net/http (go list -deps), so importing it would make this
	// package's purity a claim about someone else's code.
	"github.com/deep-huang/stock-scanner/internal/valuation",
}

func TestTheEntryPlanLayerImportsNoDecisionOrIOPackage(t *testing.T) {
	got := deps(t, repoRoot(t), "./internal/entryplan")
	for _, pkg := range forbiddenImports {
		if got[pkg] {
			t.Errorf("internal/entryplan depends on %s", pkg)
		}
	}
	// The whole cmd tree, by prefix, so a binary added later is covered without editing the
	// list above.
	for dep := range got {
		if strings.HasPrefix(dep, "github.com/deep-huang/stock-scanner/cmd/") {
			t.Errorf("internal/entryplan depends on the binary %s", dep)
		}
	}
	// And no I/O, network or database package from the standard library either. A pure
	// domain package that has grown a net/http dependency has stopped being one, whatever
	// its own source says.
	for _, std := range []string{"net/http", "database/sql", "os/exec", "math/rand"} {
		if got[std] {
			t.Errorf("internal/entryplan depends on %s — it is not a pure domain package", std)
		}
	}
}

// The direction that MUST hold, so the two prohibitions above cannot both be satisfied by an
// entryplan that depends on nothing at all.
//
// entryplan reuses internal/market/model deliberately — model.Regime's doc calls itself "the
// ONE regime vocabulary in the codebase", and EP-2's policy table is keyed on it. If this
// dependency disappeared, it would mean a parallel regime vocabulary had been declared here,
// which is the failure the reuse exists to prevent and which no forbidden-import test would
// notice.
func TestTheEntryPlanLayerDependsOnTheMarketModel(t *testing.T) {
	got := deps(t, repoRoot(t), "./internal/entryplan")
	const modelPkg = "github.com/deep-huang/stock-scanner/internal/market/model"
	if !got[modelPkg] {
		t.Errorf("internal/entryplan no longer depends on %s — either the regime vocabulary "+
			"was duplicated here, or the policy table stopped being keyed on model.Regime",
			modelPkg)
	}
}

// The converse, so neither test above can pass by naming packages that do not exist.
//
// R15 has this test for the reason it is worth repeating: an isolation check asserting against
// a typo, a renamed package or a path that was never created is GREEN and MEANINGLESS, and
// nothing else in the suite would notice.
func TestTheForbiddenPackagesExist(t *testing.T) {
	root := repoRoot(t)
	var all []string
	all = append(all, consumers...)
	all = append(all, allowedConsumers...)
	all = append(all, forbiddenImports...)
	all = append(all, entryplanPkg)

	for _, pkg := range all {
		cmd := exec.Command("go", "list", pkg)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Errorf("%s does not build — an isolation check above is asserting against a "+
				"name that no longer exists, and would pass for the wrong reason", pkg)
		}
	}
}

// PURITY, as a source fact rather than a promise.
//
// The dependency test above catches an imported clock or socket. This one catches the calls,
// including through a package that is legitimately imported for something else: "time" is
// here for time.Parse — a FORMAT check on AsOf — and time.Now would turn ComputePlan from a
// function of its snapshot into a function of when it ran, which no test on its return value
// would reveal.
func TestTheEntryPlanLayerStaysPure(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		body := string(src)
		for n, line := range strings.Split(body, "\n") {
			if f, ok := impurity(stripProse(line)); ok {
				t.Errorf("%s:%d contains %q: %s", name, n+1, f, strings.TrimSpace(line))
			}
		}
	}
	// The floor tracks the package: EP-1 shipped 5 production files, EP-2 added policy.go and
	// series.go, EP-3a added invariants.go, and EP-3b added levels.go, zone.go, chase.go,
	// trace.go, evidence.go and enforce.go. A sweep that silently stopped seeing half the
	// package would still pass a "> 0" check.
	if scanned < 15 {
		t.Fatalf("scanned only %d source files — the sweep is not seeing the package", scanned)
	}

	// THE SWEEP MUST CATCH WHAT IT CLAIMS TO CATCH.
	//
	// The first line below is not hypothetical: with the old enumerated list, a
	// `_ = os.WriteFile("/tmp/ep1_leak.json", nil, 0o644)` inside ComputePlan built cleanly
	// and the whole package tested green, while doc.go said "no filesystem ... a source scan
	// asserts it". Every entry here is a shape that must fail, checked against the same
	// matcher production is checked with.
	for _, planted := range []string{
		"\t_ = os.WriteFile(\"/tmp/ep1_leak.json\", nil, 0o644)",
		"\tfh, _ := os.Create(path)",
		"\tos.Remove(path)",
		"\tos.MkdirAll(dir, 0o755)",
		"\tos.Mkdir(dir, 0o755)",
		"\thome := os.Getenv(\"HOME\")",
		"\tfh, _ := os.Open(path)",
		"\tb, _ := ioutil.ReadFile(p)",
		"\tctx := context.Background()",
		"\tconn, _ := net.Dial(\"tcp\", addr)",
		"\tresp, _ := http.Get(u)",
		"\trow := sql.QueryRow(q)",
		"\tout, _ := exec.Command(\"git\").Output()",
		"\tnow := time.Now()",
		"\tage := time.Since(t0)",
		"\ttime.Sleep(time.Second)",
		"\tn := rand.Intn(10)",
		"\tc := fetcher.Candles(sym)",
		"\tv := scanner.Score(sym)",
	} {
		if f, ok := impurity(stripProse(planted)); !ok {
			t.Errorf("the purity sweep would not catch %q — it is the gap that let a file "+
				"write into a pure function", planted)
		} else if testing.Verbose() {
			t.Logf("planted %q caught by %q", planted, f)
		}
	}

	// And it must NOT trip on prose, on source labels, or on an identifier that merely ends
	// in the letters of a forbidden package. A guard that cries wolf gets weakened by the
	// next person in a hurry, which is how the list got enumerated in the first place.
	for _, legitimate := range []string{
		"// SOURCE: internal/fetcher.LastAdjustmentAge, carried verbatim.",
		"\tsource = \"fetcher.LastAdjustmentAge\"",
		"\t// EP2-v1, so it travels with a plan quoted out of context. It is also what stops a",
		"\tif _, err := time.Parse(asOfLayout, s.AsOf); err != nil {",
		"\treturn !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0",
		"\tfor _, info := range infos.All() {",
		"\tp.Reasons = reasons",
		"\tvideos.Close()",
	} {
		if f, ok := impurity(stripProse(legitimate)); ok {
			t.Errorf("the purity sweep trips on legitimate source, matching %q in: %s", f, legitimate)
		}
	}
}

// forbiddenSymbols are the calls no PRODUCTION file in this package may contain.
//
// `os.` IS BANNED WHOLESALE, and that is the fix for a real escape. The list used to name
// os.Open / os.Read / os.Getenv one by one, so os.WriteFile — and os.Create, os.Remove,
// os.MkdirAll, every other way to touch a disk — matched nothing. A prefix has no such gap:
// enumerating an API leaves the guard covering the calls someone thought of, and the leaks are
// always the ones added afterwards.
//
// NO EXCEPTION IS NEEDED for this file's own os.ReadDir/os.ReadFile scanner: the sweep skips
// *_test.go, and the split is the point. A test may read the source tree — that is how it
// checks the source tree. ComputePlan may not read anything.
//
// `context.` is here because a context is a cancellation and a deadline: a function taking one
// is a function whose answer can depend on when it was called and on who gave up first, which
// is the clock property under another name.
var forbiddenSymbols = []string{
	"os.",        // filesystem, environment, process — the whole package, not a sample of it
	"ioutil.",    // the deprecated door to the same place
	"net.",       // network
	"http.",      //
	"url.",       //
	"sql.",       // database
	"exec.",      // anything
	"syscall.",   //
	"rand.",      // randomness makes the result depend on nothing at all
	"context.",   // cancellation and deadlines are the clock wearing a different hat
	"time.Now",   // a clock makes the result depend on when it was computed
	"time.Since", //
	"time.Until", //
	"time.Sleep", //
	"time.After", //
	"time.Tick",  //
	"fetcher.",   // and no series: see the Snapshot doc on why the input is point values
	"scanner.",   //
}

// impurity reports the first forbidden symbol a line of CODE calls, having had its comments
// and string literals stripped.
//
// Matched with a boundary before the symbol, so an identifier that merely ENDS in one of them
// — infos.Len(), videos.Close(), a field called Reasons — is not read as a filesystem call.
// Without it a wholesale "os." ban would produce false positives, and a false positive is how
// a guard gets relaxed back into an enumeration.
func impurity(code string) (string, bool) {
	for _, f := range forbiddenSymbols {
		if callsSymbol(code, f) {
			return f, true
		}
	}
	return "", false
}

func callsSymbol(code, sym string) bool {
	for i := 0; i+len(sym) <= len(code); {
		j := strings.Index(code[i:], sym)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !identByte(code[at-1]) {
			return true
		}
		i = at + 1
	}
	return false
}

// identByte reports whether b could be part of the identifier PRECEDING a symbol. '.' counts:
// a selector on something else (x.os.Foo) is not a call to the os package.
func identByte(b byte) bool {
	return b == '_' || b == '.' ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// stripProse removes comments and string literals from one line, leaving the code.
//
// Both removals are needed and for different reasons. A comment MUST be able to say
// "fetcher.LastAdjustmentAge", because that is where AdjustmentAge comes from and the doc
// would be useless without naming it. A string literal must be able to say the same, because
// it is the evidence SOURCE LABEL that makes an archived plan traceable. Neither is a call.
func stripProse(line string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(line); i++ {
		switch {
		case inString:
			if line[i] == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if line[i] == '"' {
				inString = false
			}
		case line[i] == '"':
			inString = true
		case line[i] == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String()
		default:
			b.WriteByte(line[i])
		}
	}
	return b.String()
}

// ── EP-3b: every executable price goes through internal/pricerule ─────────────────────
//
// The tick grid and the daily price limit are STATED MARKET RULES and they live in one place.
// A local rounding — math.Round(v*10)/10, a %.1f in a format string, a hand-rolled round1() —
// produces a number that looks like a price and is not one: 1245.0000000000002, or 1245.3 on a
// stock whose tick is 5.00, which the exchange silently rejects.
//
// This is a SOURCE FACT, asserted the way the purity sweep is asserted, because the behavioural
// tests can only catch a wrong number where a fixture happens to expose one — and the whole
// point of a rounding bug is that it is invisible at most prices.
var forbiddenRounding = []string{
	"math.Round", // the classic: math.Round(v*10)/10
	"math.Floor", //
	"math.Ceil",  //
	"math.Trunc", //
	"round1",     // and any local helper that reimplements it
	"roundTo",    //
	"strconv.Fo", // FormatFloat with a precision is the same move, spelled longer
}

// forbiddenFormatVerbs are the OTHER way a price gets "rounded": formatted to one decimal and
// then read back as though the format had made it legal.
//
// They are matched against a line with its COMMENTS stripped and its STRING LITERALS KEPT,
// which is the opposite of the rounding scan above and is necessary in both directions: a
// format verb only ever appears inside a literal, while reasons.go and zone.go have to be able
// to say "no %.1f in this package" in prose without tripping the ban on it.
var forbiddenFormatVerbs = []string{"%.0f", "%.1f", "%.2f", "%.3f", "%.4f"}

// stripComments removes a // comment and keeps the string literals — the mirror image of
// stripProse. A "//" inside a literal (a URL) is not a comment.
func stripComments(line string) string {
	inString := false
	for i := 0; i < len(line); i++ {
		switch {
		case inString:
			if line[i] == '\\' && i+1 < len(line) {
				i++
				continue
			}
			if line[i] == '"' {
				inString = false
			}
		case line[i] == '"':
			inString = true
		case line[i] == '/' && i+1 < len(line) && line[i+1] == '/':
			return line[:i]
		}
	}
	return line
}

func TestNoPriceIsRoundedOutsidePriceRule(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for n, line := range strings.Split(string(src), "\n") {
			// stripProse removes comments and string literals, so reasons.go may EXPLAIN
			// that math.Round(v*10)/10 is forbidden without tripping the ban on it.
			for _, f := range roundingIn(line) {
				t.Errorf("%s:%d rounds a number locally (%q): %s\nThe tick grid is a "+
					"stated exchange rule and it lives in internal/pricerule; a local "+
					"rounding here produces a price the exchange rejects, with no error "+
					"anywhere", name, n+1, f, strings.TrimSpace(line))
			}
		}
	}
	if scanned < 15 {
		t.Fatalf("scanned only %d source files — the sweep is not seeing the package", scanned)
	}

	// THE SWEEP MUST CATCH WHAT IT CLAIMS TO CATCH. Every line here is a shape that has to
	// fail, checked against the same matcher production is checked with.
	for _, planted := range []string{
		"\tlow = math.Round(raw*10) / 10",
		"\thigh := math.Floor(raw/tick) * tick",
		"\tchase := math.Ceil(raw*100) / 100",
		"\tv := math.Trunc(price)",
		"\tlow = round1(center - halfWidth)",
		"\thigh = roundTo(center+halfWidth, tick)",
		"\tlabel := fmt.Sprintf(\"%.1f\", price)",
		"\ts := strconv.FormatFloat(price, 'f', 2, 64)",
	} {
		if len(roundingIn(planted)) == 0 {
			t.Errorf("the rounding sweep would not catch %q — it is the gap through which a "+
				"price the exchange rejects reaches a report", planted)
		}
	}
	// And it must NOT trip on the prose that EXPLAINS the ban, or on the finiteness guards
	// this package legitimately uses.
	for _, legitimate := range []string{
		"\t// No local rounding is substituted. math.Round(v*10)/10 produces 1245.0000000000002",
		"\treturn !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0",
		"\tlow, ok := pricerule.RoundToTick(rawLow, lowDir)",
		"\ttick, ok := pricerule.TickSize(center)",
		"\ta.add(InvariantZoneOrdered, z.Path, fmt.Sprintf(\"zone low = %v\", z.Zone.Low))",
	} {
		for _, f := range roundingIn(legitimate) {
			t.Errorf("the rounding sweep trips on legitimate source, matching %q in: %s",
				f, legitimate)
		}
	}
}

// The POSITIVE half: the two files that compute prices must actually REACH pricerule.
//
// Without this, the ban above is satisfied perfectly by a package that computes no price at
// all — and it would also be satisfied by one that had quietly stopped aligning, since
// "produces no forbidden call" and "produces a legal price" are different claims.
func TestTheZoneAndChaseRulesUsePriceRule(t *testing.T) {
	const pkg = "github.com/deep-huang/stock-scanner/internal/pricerule"
	if !deps(t, repoRoot(t), "./internal/entryplan")[pkg] {
		t.Fatalf("internal/entryplan no longer depends on %s — either the tick grid and the "+
			"daily price limit have been reimplemented here, or no price is being computed", pkg)
	}
	for _, name := range []string{"zone.go", "chase.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		for _, want := range []string{"pricerule.RoundToTick"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not call %s — a price that is not aligned is not a price a "+
					"caller can place an order at", name, want)
			}
		}
	}
	// And the limit rule is consulted where the ceiling is computed.
	src, err := os.ReadFile("chase.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pricerule.ClassifyLimitRule", "pricerule.LimitUpPrice"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("chase.go does not call %s — an unclamped ceiling can exceed the day's "+
				"legal price", want)
		}
	}
}

// roundingIn reports every forbidden rounding shape one line of source contains.
//
// Two matchers, on two different views of the line, for the reason given at
// forbiddenFormatVerbs: a call is code (comments and literals stripped) and a format verb is a
// literal (comments stripped only).
func roundingIn(line string) []string {
	var out []string
	code := stripProse(line)
	for _, f := range forbiddenRounding {
		if callsSymbol(code, f) {
			out = append(out, f)
		}
	}
	withLiterals := stripComments(line)
	for _, verb := range forbiddenFormatVerbs {
		if strings.Contains(withLiterals, verb) {
			out = append(out, verb)
		}
	}
	return out
}

// ── EP-6D: the direct-import allowlist and the clock ───────────────────────────────────
//
// forbiddenImports above is a BLACKLIST of transitive dependencies: it catches the packages
// somebody thought of. entryPlanDirectImports is the EXACT set of packages the production files
// of internal/entryplan import directly (`go list -f '{{.Imports}}'`), compared in both
// directions — so a new direct import of anything at all, including a stdlib package no list
// names yet (io, bufio, log, crypto/rand ...), is a failing test and a decision.
//
// Structural: it proves what the package IMPORTS, not what any function does with it.
var entryPlanDirectImports = map[string]string{
	"fmt":     "violation messages and paths (invariants.go) and validation errors (series.go)",
	"math":    "finiteness checks — IsNaN / IsInf (input.go, invariants.go)",
	"reflect": "the invariant checker's reflective float and zone walk (invariants.go)",
	"sort":    "sorting map keys in the reflective invariant walk so output is byte-stable (invariants.go)",
	"strings": "reading violation paths and joining withdrawn field names (enforce.go)",
	"time":    "time.Parse as a FORMAT check on AsOf / regime dates, and time.Time for ordering them — never a clock; see TestTheEntryPlanLayerNeverReadsAClock",
	"github.com/deep-huang/stock-scanner/internal/market/model": "the ONE regime vocabulary; see TestTheEntryPlanLayerDependsOnTheMarketModel",
	"github.com/deep-huang/stock-scanner/internal/pricerule":    "the tick grid and the daily price limit; see TestTheZoneAndChaseRulesUsePriceRule",
}

func TestTheEntryPlanLayerDirectImportsAreExactlyTheAllowlist(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./internal/entryplan")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -f .Imports ./internal/entryplan: %v", err)
	}
	got := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			got[s] = true
		}
	}
	// ANTI-VACUITY: a go list that printed nothing would satisfy the "unexpected" direction.
	if len(got) < 5 {
		t.Fatalf("go list reported only %d direct imports (%v) — the listing is not seeing "+
			"the package", len(got), got)
	}
	var matched int
	for pkg := range got {
		if _, ok := entryPlanDirectImports[pkg]; !ok {
			t.Errorf("internal/entryplan now imports %q directly, which is not in "+
				"entryPlanDirectImports. This package is a pure domain layer: no network, no "+
				"filesystem, no database, no clock, no randomness. If the import is genuinely "+
				"pure, add it WITH the sentence saying what it is for; if it is not, remove it", pkg)
			continue
		}
		matched++
	}
	for pkg, why := range entryPlanDirectImports {
		if !got[pkg] {
			t.Errorf("internal/entryplan no longer imports %q (%s) — update the allowlist so it "+
				"describes the package as it is", pkg, why)
		}
	}
	if matched != len(entryPlanDirectImports) {
		t.Errorf("matched %d of %d allowlisted imports", matched, len(entryPlanDirectImports))
	}
}

// entryPlanTimeFiles is the EXACT set of production files that may import "time", and
// entryPlanTimeSelectors the EXACT set of `time.X` selectors any of them may use.
//
// EP-6 brief §22: "entryplan must not import time for decision semantics". decide.go and plan.go
// are the decision; neither may import it. The two files that do use it only to PARSE a date
// string and to hold the parsed value.
var entryPlanTimeFiles = map[string]bool{"input.go": true, "series.go": true}

var entryPlanTimeSelectors = map[string]bool{"Parse": true, "Time": true}

// TestTheEntryPlanLayerNeverReadsAClock is an AST scan: it resolves the local name each file
// gives the "time" import (including an alias or a dot/blank import, which are refused
// outright), then inspects every selector on that name.
//
// It is STRUCTURAL EARLY WARNING. It catches `time.Now()` in source; it does not catch a clock
// reached through another imported package, which is what the direct-import allowlist and
// TestTheEntryPlanLayerStaysPure are for.
func TestTheEntryPlanLayerNeverReadsAClock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var scanned, parseSeen int
	importers := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		used, problems := timeSelectorsIn(name, src)
		for _, p := range problems {
			t.Error(p)
		}
		if used == nil {
			continue
		}
		importers[name] = true
		if !entryPlanTimeFiles[name] {
			t.Errorf("%s imports \"time\". Only %v may: the decision (decide.go, plan.go) and "+
				"every rule must be a function of its snapshot, never of when it ran", name,
				entryPlanTimeFiles)
		}
		for sel, n := range used {
			if !entryPlanTimeSelectors[sel] {
				t.Errorf("%s uses time.%s (%d×) — only %v are allowed: a format check and the "+
					"parsed value, never a clock, a timer or a sleep", name, sel, n,
					entryPlanTimeSelectors)
			}
			if sel == "Parse" {
				parseSeen += n
			}
		}
	}
	// ANTI-VACUITY.
	if scanned < 15 {
		t.Fatalf("scanned only %d production files", scanned)
	}
	if !reflect.DeepEqual(importers, entryPlanTimeFiles) {
		t.Errorf("files importing time = %v, want exactly %v", importers, entryPlanTimeFiles)
	}
	if parseSeen < 3 {
		t.Errorf("saw time.Parse %d times, want the 3 known call sites — the scan is not "+
			"seeing the selectors it polices", parseSeen)
	}

	// THE SCAN MUST CATCH WHAT IT CLAIMS TO CATCH, checked with the same function.
	for _, planted := range []string{
		"package entryplan\nimport \"time\"\nfunc f() { _ = time.Now() }\n",
		"package entryplan\nimport clk \"time\"\nfunc f() { _ = clk.Now() }\n",
		"package entryplan\nimport \"time\"\nfunc f() { time.Sleep(1) }\n",
		"package entryplan\nimport . \"time\"\nfunc f() { _ = Now() }\n",
		"package entryplan\nimport \"time\"\nvar d = time.Since\n",
	} {
		used, problems := timeSelectorsIn("planted.go", []byte(planted))
		var bad bool
		for sel := range used {
			if !entryPlanTimeSelectors[sel] {
				bad = true
			}
		}
		if !bad && len(problems) == 0 {
			t.Errorf("the clock scan would not catch:\n%s", planted)
		}
	}
	if used, problems := timeSelectorsIn("ok.go",
		[]byte("package entryplan\nimport \"time\"\nfunc f(s string) { _, _ = time.Parse(\"2006-01-02\", s); var x time.Time; _ = x }\n")); len(problems) != 0 || len(used) != 2 {
		t.Errorf("the clock scan misreads a legitimate parse: used=%v problems=%v", used, problems)
	}
}

// timeSelectorsIn returns, for one source file, the count of each `time.X` selector (nil when
// the file does not import time) and any structural problem (an unparsable file, a dot or blank
// import of time).
func timeSelectorsIn(name string, src []byte) (map[string]int, []string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, []string{name + ": parse: " + err.Error()}
	}
	local := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != "\"time\"" {
			continue
		}
		local = "time"
		if imp.Name != nil {
			switch imp.Name.Name {
			case ".", "_":
				return map[string]int{}, []string{name + ": imports \"time\" as " +
					imp.Name.Name + " — its selectors cannot be audited"}
			default:
				local = imp.Name.Name
			}
		}
	}
	if local == "" {
		return nil, nil
	}
	used := map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
			used[sel.Sel.Name]++
		}
		return true
	})
	return used, nil
}
