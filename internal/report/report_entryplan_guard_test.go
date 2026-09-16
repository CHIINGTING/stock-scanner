package report

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-8 REPORT PURITY — STRUCTURAL GUARD, NOT BEHAVIOUR PROOF
//
// What it checks, by parsing source (go/parser), and nothing more:
//
//  1. No non-test .go file in internal/report calls any of the entryplan functions that compute a
//     plan, a policy, a status or a price step (epForbiddenEntryPlanCalls), written as
//     `entryplan.<Name>(…)` under whatever local name the file imports internal/entryplan as.
//  2. report_entryplan.go — the file that holds every ⑲ helper — does not import
//     internal/valuation, internal/healthcheck, internal/pricerule, internal/indicator or
//     internal/technical.
//  3. In report_entryplan.go the only field read off a scanner.WatchlistEntry value is EntryPlan
//     (checked on the parameter of entryPlanView, the one function that receives an entry).
//
// What it does NOT see: a call made through a function value or reflection, code in the
// template string (html/template actions are not Go syntax), and any file outside internal/report.
// The behaviour proof is the output tests in report_entryplan_test.go.
// ──────────────────────────────────────────────────────────────────────────────

var epForbiddenEntryPlanCalls = []string{
	"ComputePlan", "DecideStatus", "PolicyFor", "PolicyForRegime",
	"ComputeIdealEntryZone", "ComputeMaxChase", "ComputeInvalidation", "ComputeTargets",
	"CandidateLevelsFor", "ScreenCandidateLevels", "SelectCenter",
	"InvalidationCandidatesFor", "ScreenInvalidationCandidates", "SelectInvalidation",
	"ClassifyConfidence", "EnforcePlanInvariants", "ReadAbsence",
}

var epForbiddenImports = []string{
	"github.com/deep-huang/stock-scanner/internal/valuation",
	"github.com/deep-huang/stock-scanner/internal/healthcheck",
	"github.com/deep-huang/stock-scanner/internal/pricerule",
	"github.com/deep-huang/stock-scanner/internal/indicator",
	"github.com/deep-huang/stock-scanner/internal/technical",
}

const epEntryPlanImport = "github.com/deep-huang/stock-scanner/internal/entryplan"

// epForbiddenCallSites returns "file:line entryplan.Name" for every forbidden call in src.
func epForbiddenCallSites(fset *token.FileSet, f *ast.File) []string {
	local := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == epEntryPlanImport {
			local = "entryplan"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if local == "" {
		return nil
	}
	forbidden := map[string]bool{}
	for _, n := range epForbiddenEntryPlanCalls {
		forbidden[n] = true
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && forbidden[sel.Sel.Name] {
			out = append(out, fset.Position(call.Pos()).String()+" entryplan."+sel.Sel.Name)
		}
		return true
	})
	return out
}

func epImports(f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		out = append(out, p)
	}
	return out
}

// epEntryFieldReads returns every selector read off the named parameter of fn.
func epEntryFieldReads(f *ast.File, fn, param string) (fields []string, found bool) {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn || fd.Body == nil {
			continue
		}
		found = true
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == param {
				fields = append(fields, sel.Sel.Name)
			}
			return true
		})
	}
	return fields, found
}

func TestEntryPlanReportCodeComputesNothing(t *testing.T) {
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	importsEntryPlan := 0
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		full, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, p := range epImports(full) {
			if p == epEntryPlanImport {
				importsEntryPlan++
			}
		}
		for _, site := range epForbiddenCallSites(fset, full) {
			t.Errorf("report code calls a plan-computing function: %s", site)
		}
	}
	// Anti-vacuity: the package has report.go and report_entryplan.go, and the latter imports
	// entryplan, so the call scan had something to look at.
	if scanned < 2 || importsEntryPlan < 1 {
		t.Fatalf("scanned %d files, %d import entryplan — the guard is not seeing the ⑲ code", scanned, importsEntryPlan)
	}

	ep, err := parser.ParseFile(fset, "report_entryplan.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range epImports(ep) {
		for _, bad := range epForbiddenImports {
			if p == bad {
				t.Errorf("report_entryplan.go imports %s", p)
			}
		}
	}
	fields, found := epEntryFieldReads(ep, "entryPlanView", "e")
	if !found {
		t.Fatal("entryPlanView not found in report_entryplan.go — repoint this guard")
	}
	sort.Strings(fields)
	if strings.Join(fields, ",") != "EntryPlan" {
		t.Errorf("entryPlanView reads %v off the watchlist entry; only EntryPlan is allowed", fields)
	}
}

// The guard must catch what it claims to catch, on planted source through the same functions.
func TestEntryPlanReportPurityGuardCatchesPlantedViolations(t *testing.T) {
	dir := t.TempDir()
	src := `package report

import (
	ep "github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func entryPlanView(e X) any {
	_ = valuation.Available
	_ = e.EntryZone
	p := ep.ComputePlan(ep.Snapshot{})
	_ = ep.DecideStatus(ep.DecisionInput{})
	_ = ep.KnownReason("X")
	return e.EntryPlan
}
`
	path := filepath.Join(dir, "planted.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	sites := epForbiddenCallSites(fset, f)
	if len(sites) != 2 || !strings.HasSuffix(sites[0], "entryplan.ComputePlan") ||
		!strings.HasSuffix(sites[1], "entryplan.DecideStatus") {
		t.Errorf("planted forbidden calls = %v, want ComputePlan and DecideStatus only (KnownReason is allowed)", sites)
	}
	badImport := false
	for _, p := range epImports(f) {
		for _, bad := range epForbiddenImports {
			badImport = badImport || p == bad
		}
	}
	if !badImport {
		t.Error("planted valuation import was not detected")
	}
	fields, found := epEntryFieldReads(f, "entryPlanView", "e")
	sort.Strings(fields)
	if !found || strings.Join(fields, ",") != "EntryPlan,EntryZone" {
		t.Errorf("planted entry reads = %v (found=%v), want EntryPlan,EntryZone", fields, found)
	}
}
