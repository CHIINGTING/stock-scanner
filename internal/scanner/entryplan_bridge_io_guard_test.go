package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6D (D3) — THE BRIDGE DOES NO I/O, AS A SOURCE FACT ABOUT entryplan_attach.go
//
// EP-6C changed AttachEntryPlan's signature (candles + market) and argued, by reading the
// imports, that no I/O had moved into the bridge. This turns the argument into a check. The
// file is parsed and THREE exact sets are enforced:
//
//  1. its imports;
//  2. every selector on an imported package other than entryplan (fetcher, model, math) —
//     fetcher is a NETWORK package (Yahoo / TWSE fetchers live there), so importing it for
//     Candle must not become a door to fetcher.New(...).FetchAll();
//  3. every call: a function declared in this same file, a builtin/conversion from a fixed
//     set, an allowlisted package selector, or a METHOD whose name is in a fixed set.
//
// WHAT THIS DOES AND DOES NOT PROVE — it is STRUCTURAL EARLY WARNING, not behaviour proof:
//   - It proves the file cannot NAME os, net/http, database/sql, a fetcher client, a clock, or
//     a same-package helper declared in another file (every such call is refused by rule 3).
//   - It does NOT type-check. Rule 3's method names are matched by NAME (IsZero, Format, Equal,
//     Valid); a value of some I/O type with a method of one of those names would pass.
//   - It does NOT look inside the functions it allows. entryplan.* is allowed wholesale because
//     internal/entryplan's own guards (direct-import allowlist, purity sweep, clock scan) cover
//     it; fetcher.LastAdjustmentAge is allowed by name, and nothing here reads its body
//     (internal/fetcher/adjustment.go imports only math and time — a fact about today's file,
//     not something this test checks).
// ──────────────────────────────────────────────────────────────────────────────

const bridgeFile = "entryplan_attach.go"

var (
	bridgeImports = map[string]bool{
		"math": true,
		"github.com/deep-huang/stock-scanner/internal/entryplan":    true,
		"github.com/deep-huang/stock-scanner/internal/fetcher":      true,
		"github.com/deep-huang/stock-scanner/internal/market/model": true,
	}
	// Selectors allowed on each imported package, by import path. entryplan is "*" — see above.
	bridgePackageSelectors = map[string]map[string]bool{
		"math": {"IsNaN": true, "IsInf": true},
		"github.com/deep-huang/stock-scanner/internal/fetcher":      {"Candle": true, "LastAdjustmentAge": true},
		"github.com/deep-huang/stock-scanner/internal/market/model": {"Regime": true},
		"github.com/deep-huang/stock-scanner/internal/entryplan":    {"*": true},
	}
	bridgeBuiltinCalls = map[string]bool{"len": true, "float64": true}
	bridgeMethodCalls  = map[string]bool{"IsZero": true, "Format": true, "Equal": true, "Valid": true}
)

// bridgeIOProblems returns every rule the source breaks, plus the number of calls it classified.
func bridgeIOProblems(name string, src []byte) ([]string, int) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return []string{"parse: " + err.Error()}, 0
	}
	var problems []string
	pos := func(n ast.Node) string { return fset.Position(n.Pos()).String() }

	// 1. imports, and the local name of each.
	local := map[string]string{} // local name → import path
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !bridgeImports[path] {
			problems = append(problems, pos(imp)+": imports "+strconv.Quote(path)+
				", which is not in bridgeImports")
			continue
		}
		ln := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			if imp.Name.Name == "." || imp.Name.Name == "_" {
				problems = append(problems, pos(imp)+": imports "+strconv.Quote(path)+" as "+
					imp.Name.Name+" — its uses cannot be audited")
				continue
			}
			ln = imp.Name.Name
		}
		local[ln] = path
	}

	declared := map[string]bool{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			declared[fd.Name.Name] = true
		}
	}

	// 2. every package selector.
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, isPkg := local[id.Name]
		if !isPkg {
			return true
		}
		allowed := bridgePackageSelectors[path]
		if !allowed["*"] && !allowed[sel.Sel.Name] {
			problems = append(problems, pos(sel)+": uses "+id.Name+"."+sel.Sel.Name+
				", which is not in bridgePackageSelectors["+strconv.Quote(path)+"]")
		}
		return true
	})

	// 3. every call.
	var calls int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		calls++
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if !declared[fn.Name] && !bridgeBuiltinCalls[fn.Name] {
				problems = append(problems, pos(call)+": calls "+fn.Name+
					"(), which is neither declared in this file nor an allowed builtin — a "+
					"helper from elsewhere in the package can do anything")
			}
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok {
				if _, isPkg := local[id.Name]; isPkg {
					return true // rule 2 already judged it
				}
			}
			if !bridgeMethodCalls[fn.Sel.Name] {
				problems = append(problems, pos(call)+": calls method "+fn.Sel.Name+
					"(), which is not in bridgeMethodCalls")
			}
		default:
			problems = append(problems, pos(call)+": a call through an expression this "+
				"guard cannot name (a function value, a literal, an index) — refused")
		}
		return true
	})
	return problems, calls
}

func TestTheEntryPlanBridgeCanNameNoIO(t *testing.T) {
	src, err := os.ReadFile(bridgeFile)
	if err != nil {
		t.Fatalf("read %s: %v — repoint this guard at the bridge, do not drop it", bridgeFile, err)
	}
	problems, calls := bridgeIOProblems(bridgeFile, src)
	t.Logf("%s: %d calls classified", bridgeFile, calls)
	for _, p := range problems {
		t.Error(p)
	}
	// ANTI-VACUITY: the bridge makes 33 calls today (measured); a parse that saw none asserts nothing.
	if calls < 30 {
		t.Fatalf("classified only %d calls in %s — the scan is not seeing the file", calls, bridgeFile)
	}

	// THE GUARD MUST CATCH WHAT IT CLAIMS TO CATCH, through the same function.
	const head = "package scanner\nimport (\n\t\"math\"\n\t\"github.com/deep-huang/stock-scanner/internal/entryplan\"\n\t\"github.com/deep-huang/stock-scanner/internal/fetcher\"\n)\n"
	planted := map[string]string{
		"import os":       "package scanner\nimport \"os\"\nfunc f() { _, _ = os.ReadFile(\"x\") }\n",
		"import net/http": "package scanner\nimport \"net/http\"\nfunc f() { _, _ = http.Get(\"u\") }\n",
		"aliased os":      "package scanner\nimport fs \"os\"\nfunc f() { _ = fs.Getenv(\"H\") }\n",
		"fetcher client":  head + "func f() { c := fetcher.New(fetcher.Config{}); _ = c }\n",
		"package helper":  head + "func f() { loadValuationArchive() }\n",
		"method I/O":      head + "func f(c interface{ FetchAll() }) { c.FetchAll() }\n",
		"func value":      head + "var g = func() {}\nfunc f() { g() }\n",
		"dot import":      "package scanner\nimport . \"math\"\nfunc f() { _ = IsNaN(1) }\n",
	}
	names := make([]string, 0, len(planted))
	for k := range planted {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if p, _ := bridgeIOProblems("planted.go", []byte(planted[k])); len(p) == 0 {
			t.Errorf("the bridge I/O guard would not catch %q:\n%s", k, planted[k])
		}
	}
	// And it must pass a legitimate projection.
	legit := head + "func g(v float64) bool { return math.IsNaN(v) }\n" +
		"func f(c []fetcher.Candle) entryplan.Snapshot { _ = g(1); _ = len(c); _, _ = fetcher.LastAdjustmentAge(c); return entryplan.ComputePlanInput() }\n"
	if p, _ := bridgeIOProblems("legit.go", []byte(legit)); len(p) != 0 {
		t.Errorf("the guard refuses a legitimate projection: %v", p)
	}
}
