package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6D — WHICH FUNCTIONS MAY TOUCH WatchlistEntry.EntryPlan, REPO-WIDE
//
// EP-6A's stop-gap (entryplan_isolation_test.go) answers "which FILE in internal/scanner" and
// says so. This answers "which FUNCTION, anywhere in the repository": every non-test .go file
// under cmd/ and internal/ is parsed, every selector whose name is EntryPlan is attributed to
// its enclosing top-level declaration, and the set of those declarations must EQUAL the
// allowlist below.
//
// It is STRUCTURAL EARLY WARNING, not behaviour proof. What it sees: `x.EntryPlan` written in
// source, in any package under cmd/ or internal/ — a score function, a sort comparator in
// watchlist.go, a report helper, a ranking pass in cmd/scanner — attributed to the declaration
// it is written in. What it does NOT see:
//   - a read through reflection or encoding/json (WatchlistEntry.EntryPlan carries a json tag,
//     watchlist.go:172), which names no selector;
//   - code outside cmd/ and internal/, generated code that is not on disk, and _test.go files.
// It OVER-approximates in one direction: a selector named EntryPlan on any other type would be
// reported too (no other type has such a field today).
// The behavioural evidence that no decision moves is EP-6B's OFF-vs-ON shadow diff
// (cmd/scanner/entryplan_pipeline_test.go + shadowdiff_test.go).
// ──────────────────────────────────────────────────────────────────────────────

// entryPlanFieldFunctions is the EXACT set of declarations that may contain a `.EntryPlan`
// selector, keyed "repo-relative file#Func" (methods as "#Recv.Method").
var entryPlanFieldFunctions = map[string]string{
	"internal/scanner/entryplan_attach.go#AttachEntryPlan": "the bridge's post-pass: the only " +
		"WRITE, after every score, action and sort position is final (buildWatchlist calls it " +
		"last, cmd/scanner/main.go)",
	// EP-7. The research store's write-only projection: it reads the published plan after the
	// report and the analysis-history sidecar are written (recordResearch is called after both
	// in cmd/scanner/main.go) and hands it to projectEntryPlan, which emits ep_* evidence rows
	// and returns nothing the scanner reads. internal/research/entryplan_evidence_guard_test.go pins that no production
	// code reads those rows back.
	"internal/research/entryplan_evidence.go#buildEntryPlanEvidence": "EP-7 persistence: the " +
		"READ projecting the finished plan into ep_* evidence rows",
	// EP-8. The report's ⑲ view builder: reads the published plan to format it for the HTML
	// report after every score, action and sort position is final (r.Generate runs after
	// buildWatchlist in cmd/scanner/main.go). It returns display strings only and writes nothing
	// back; internal/report/report_entryplan_test.go's OFF-vs-ON test pins that enabling it
	// changes no HTML outside the ⑲ block and mutates no plan.
	"internal/report/report_entryplan.go#entryPlanView": "EP-8 report section ⑲: the only " +
		"report READ, formatting the published plan for display",
}

func TestOnlyAttachEntryPlanTouchesTheEntryPlanFieldRepoWide(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found, scanned, err := entryPlanSelectorSites(root, []string{"cmd", "internal"})
	if err != nil {
		t.Fatal(err)
	}

	// ANTI-VACUITY: the repository has hundreds of production files under cmd/ and internal/.
	if scanned < 250 {
		t.Fatalf("scanned only %d production files — the walk is not seeing the repository", scanned)
	}
	if len(found["internal/scanner/entryplan_attach.go#AttachEntryPlan"]) == 0 {
		t.Fatalf("AttachEntryPlan no longer contains a .EntryPlan selector (sites: %v) — either "+
			"the bridge moved (repoint the allowlist) or it writes the field some other way, "+
			"in which case this scan can no longer see what it polices", found)
	}

	offenders := entryPlanFieldOffenders(found)
	for _, decl := range offenders {
		t.Errorf("%s touches .EntryPlan at %v.\nEntryPlan is SHADOW EVIDENCE computed after the "+
			"scan decided; a read anywhere else is how it becomes an input to a score, a stage, "+
			"an action, a sort or a report. If this declaration "+
			"genuinely needs the plan, that is an architecture change: add it to "+
			"entryPlanFieldFunctions WITH the reason, and make the OFF-vs-ON shadow diff prove "+
			"no decision moved.", decl, found[decl])
	}
	for decl := range entryPlanFieldFunctions {
		if len(found[decl]) == 0 {
			t.Errorf("allowlisted %s contains no .EntryPlan selector — the allowlist describes "+
				"code that no longer exists", decl)
		}
	}

	// THE SCAN MUST CATCH WHAT IT CLAIMS TO CATCH, on planted source through the same function.
	dir := t.TempDir()
	planted := map[string]string{
		"internal/scanner/watchlist.go": "package scanner\nfunc sortEntries(es []WatchlistEntry) bool {\n" +
			"\treturn es[0].EntryPlan != nil\n}\n",
		"internal/report/render.go": "package report\nfunc (r *R) row(e X) { _ = e.EntryPlan.Status }\n",
		"cmd/scanner/rank.go":       "package main\nvar pick = func(e X) any { return e.EntryPlan }\n",
		"internal/research/research.go": "package research\nfunc (r *Recorder) RecordScan(e X) bool {\n" +
			"\treturn e.EntryPlan != nil\n}\n",
	}
	for rel, src := range planted {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pf, _, err := entryPlanSelectorSites(dir, []string{"cmd", "internal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"internal/scanner/watchlist.go#sortEntries",
		"internal/report/render.go#R.row",
		"cmd/scanner/rank.go#<package-level>",
		"internal/research/research.go#Recorder.RecordScan",
	} {
		if len(pf[want]) == 0 {
			t.Errorf("the planted read %s was not attributed (got %v)", want, pf)
		}
	}
	// And the allowlist decision itself is exact: every planted reader — including one in the
	// same file-family as the EP-7 persistence entry — is an offender. A prefix or package-wide
	// exemption would drop one of these.
	if got, want := entryPlanFieldOffenders(pf), []string{
		"cmd/scanner/rank.go#<package-level>",
		"internal/report/render.go#R.row",
		"internal/research/research.go#Recorder.RecordScan",
		"internal/scanner/watchlist.go#sortEntries",
	}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("planted offenders = %v, want %v", got, want)
	}
	// And it must not trip on the config flags, which are different identifiers.
	legit := filepath.Join(dir, "internal", "x", "flags.go")
	_ = os.MkdirAll(filepath.Dir(legit), 0o755)
	_ = os.WriteFile(legit, []byte("package x\nfunc f(c C) bool { return c.EnableEntryPlan || c.ShowEntryPlan }\n"), 0o644)
	pf, _, _ = entryPlanSelectorSites(dir, []string{"internal/x"})
	if len(pf) != 0 {
		t.Errorf("the scan trips on the config flags: %v", pf)
	}
}

// entryPlanFieldOffenders returns, sorted, every declaration in found that is not an EXACT key
// of entryPlanFieldFunctions.
func entryPlanFieldOffenders(found map[string][]string) []string {
	var offenders []string
	for decl := range found {
		if _, ok := entryPlanFieldFunctions[decl]; !ok {
			offenders = append(offenders, decl)
		}
	}
	sort.Strings(offenders)
	return offenders
}

// entryPlanSelectorSites walks the given repo-relative directories and returns, per enclosing
// declaration, the positions of every `.EntryPlan` selector in non-test .go files.
func entryPlanSelectorSites(root string, dirs []string) (map[string][]string, int, error) {
	found := map[string][]string{}
	var scanned int
	fset := token.NewFileSet()
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				if name := de.Name(); name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			scanned++
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, decl := range f.Decls {
				name := "<package-level>"
				if fd, ok := decl.(*ast.FuncDecl); ok {
					name = fd.Name.Name
					if fd.Recv != nil && len(fd.Recv.List) == 1 {
						name = recvTypeName(fd.Recv.List[0].Type) + "." + name
					}
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if ok && sel.Sel != nil && sel.Sel.Name == "EntryPlan" {
						key := rel + "#" + name
						found[key] = append(found[key], fset.Position(sel.Pos()).String())
					}
					return true
				})
			}
			return nil
		})
		if err != nil {
			return nil, scanned, err
		}
	}
	return found, scanned, nil
}

func recvTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeName(x.X)
	}
	return "?"
}
