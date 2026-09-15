package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// THE STOP-GAP THAT REPLACES A GUARD EP-6A HAD TO WEAKEN.
//
// Until EP-6A, internal/entryplan's architecture_test.go listed ./internal/scanner among the
// packages that must NOT depend on entryplan, and `go list -deps` enforced it. Putting
// WatchlistEntry.EntryPlan on the struct makes that dependency a fact — the field's type IS an
// entryplan type — so that arm could not survive, and EP-6A moved internal/scanner (and
// internal/candidate, which imports it) to the allowed side.
//
// WHAT WAS LOST, stated plainly rather than smoothed over: the question stopped being "may this
// package import entryplan" and became "which SYMBOLS in this package may read the field". A
// dependency test cannot express the second. Between EP-6A and the symbol-level allowlist that
// EP-6D owns, nothing structural stood between a scoring function and the plan — and reading
// EntryPlan from computeRocket, the scorer or a sort comparator is precisely the contamination
// the whole work item exists to prevent.
//
// This test is the cheap replacement for that window. It is an AST scan, not a grep: it looks
// for a SELECTOR whose name is EntryPlan (`e.EntryPlan`, `entries[i].EntryPlan`), so it cannot
// be fooled by the config flags EnableEntryPlan / ShowEntryPlan, which are different
// identifiers and are not selectors on a WatchlistEntry. The field's own declaration in
// watchlist.go is an *ast.Field, not a selector, so declaring it does not trip this.
//
// It is WEAKER than the function-level, repo-wide scan EP-6D added in
// entryplan_symbol_guard_test.go (this one says "which files in internal/scanner", not "which
// functions anywhere", so a read in another package is invisible to it). It is kept because it
// is independent of that scan's directory walk. It is not a substitute
// for the behavioural OFF-vs-ON diff — which, since EP-6B, exists in cmd/scanner
// (entryplan_pipeline_test.go + shadowdiff_test.go) and is what actually proves no decision
// moved. This scan stays because the two answer different questions: that one says the decision
// did not move for the fixture it ran, this one says no production file can even reach the
// field. It exists so the window is not empty.
//
// entryPlanFieldReaders is the allowlist, by file. Exactly one production file may touch the
// field: the bridge that writes it.
var entryPlanFieldReaders = map[string]string{
	"entryplan_attach.go": "the bridge itself — it is the only thing that may WRITE the field, " +
		"and it runs as a post-pass after every score, action and sort position is final",
}

func TestOnlyTheBridgeTouchesTheEntryPlanField(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/scanner: %v — point this scan at wherever the scanner's "+
			"production files moved to, rather than dropping the check", err)
	}

	var scanned int
	found := map[string][]string{} // file -> positions
	fset := token.NewFileSet()

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "EntryPlan" {
				return true
			}
			found[name] = append(found[name], fset.Position(sel.Pos()).String())
			return true
		})
	}

	// ANTI-VACUITY, both halves. A scan that saw no files, or that found the field nowhere at
	// all, would pass while asserting nothing — and the second half is the one that rots: if
	// the bridge is renamed or the field is read only through a helper, this test goes quiet
	// rather than red.
	if scanned < 25 {
		t.Fatalf("scanned only %d production files in internal/scanner — the sweep is not "+
			"seeing the package and would pass for the wrong reason", scanned)
	}
	if len(found["entryplan_attach.go"]) == 0 {
		t.Fatalf("no file reads WatchlistEntry.EntryPlan at all, not even the bridge. Either " +
			"the bridge was renamed (repoint entryPlanFieldReaders) or it stopped writing the " +
			"field through a selector — in which case this scan can no longer see the reads it " +
			"exists to police (see also entryplan_symbol_guard_test.go)")
	}

	var offenders []string
	for file := range found {
		if _, ok := entryPlanFieldReaders[file]; !ok {
			offenders = append(offenders, file)
		}
	}
	sort.Strings(offenders)
	for _, file := range offenders {
		t.Errorf("%s reads WatchlistEntry.EntryPlan at %v.\n"+
			"EntryPlan is SHADOW EVIDENCE: it is computed after the scan has already decided, "+
			"and nothing in this package may read it back. A read here is how the plan becomes "+
			"an input to the decision it was derived from — the scoring, staging, action and "+
			"sort paths all live in this package, and once the field is reachable from them the "+
			"OFF-vs-ON equality EP-6B asserts stops being structurally true.\n"+
			"If this file genuinely needs the plan, that is an architecture change: say so in "+
			"entryPlanFieldReaders WITH the sentence explaining why, and make EP-6B's shadow "+
			"diff prove the decision still does not move.", file, found[file])
	}
}
