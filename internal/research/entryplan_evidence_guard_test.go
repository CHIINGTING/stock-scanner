package research

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-7 STRUCTURAL GUARDS for the ep_* evidence keys.
//
// STRUCTURAL EARLY WARNING, not behaviour proof. They parse non-test .go files under cmd/ and
// internal/. They do NOT see reads assembled at run time (a key built by concatenation, SQL
// written outside Go source, reflection), code outside cmd/ and internal/, or _test.go files.
// The behavioural half is TestEntryPlanPersistenceDoesNotAlterScannerRecordsOrInputs.
// ──────────────────────────────────────────────────────────────────────────────

const epRegistryFile = "internal/research/entryplan_evidence.go"

func epRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// walkProductionGo parses every non-test .go file under root/dirs and hands it to visit.
func walkProductionGo(t *testing.T, root string, dirs []string, visit func(rel string, fset *token.FileSet, f *ast.File)) int {
	t.Helper()
	fset := token.NewFileSet()
	scanned := 0
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				if n := de.Name(); n == "testdata" || strings.HasPrefix(n, ".") {
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
			rel, _ := filepath.Rel(root, path)
			scanned++
			visit(filepath.ToSlash(rel), fset, f)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return scanned
}

func declName(decl ast.Decl) string {
	if fd, ok := decl.(*ast.FuncDecl); ok {
		return fd.Name.Name
	}
	return "<package-level>"
}

// ── key enumeration, mechanical ───────────────────────────────────────────────────────────

// existingEvidenceKeys enumerates, from source, every evidence key written as a string literal
// directly after a Category* argument in a call — the shape every evidence writer in this repo
// uses (research's collector, store.Num/Text, healthcheck's num/text). Keys built by
// concatenation are returned separately as their literal parts.
func existingEvidenceKeys(t *testing.T, root string, skip string) (keys map[string]string, parts map[string]string, categories map[string]string, scanned int) {
	keys, parts, categories = map[string]string{}, map[string]string{}, map[string]string{}
	scanned = walkProductionGo(t, root, []string{"cmd", "internal"}, func(rel string, fset *token.FileSet, f *ast.File) {
		if rel == skip {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ValueSpec:
				for i, name := range x.Names {
					if strings.HasPrefix(name.Name, "Category") && i < len(x.Values) {
						if lit, ok := x.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							v, _ := strconv.Unquote(lit.Value)
							categories[v] = rel + "#" + name.Name
						}
					}
				}
			case *ast.CallExpr:
				for i := 0; i+1 < len(x.Args); i++ {
					if !isCategoryRef(x.Args[i]) {
						continue
					}
					pos := fset.Position(x.Args[i+1].Pos()).String()
					switch k := x.Args[i+1].(type) {
					case *ast.BasicLit:
						if k.Kind == token.STRING {
							v, _ := strconv.Unquote(k.Value)
							keys[v] = pos
						}
					case *ast.BinaryExpr:
						ast.Inspect(k, func(m ast.Node) bool {
							if lit, ok := m.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								v, _ := strconv.Unquote(lit.Value)
								parts[v] = pos
							}
							return true
						})
					}
				}
			}
			return true
		})
	})
	return keys, parts, categories, scanned
}

func isCategoryRef(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return strings.HasPrefix(x.Name, "Category")
	case *ast.SelectorExpr:
		return strings.HasPrefix(x.Sel.Name, "Category")
	}
	return false
}

// ── 12. no collision with any existing evidence key ───────────────────────────────────────

func TestEntryPlanKeysCollideWithNoExistingEvidenceKey(t *testing.T) {
	root := epRepoRoot(t)
	keys, parts, categories, scanned := existingEvidenceKeys(t, root, epRegistryFile)

	// ANTI-VACUITY: the enumeration must see both known writers and the concatenated family.
	if scanned < 250 || len(keys) < 200 {
		t.Fatalf("scanned %d files, found %d literal keys — the enumeration is not seeing the writers", scanned, len(keys))
	}
	for _, known := range []string{"heat_status", "tech_adx", "target_2", "eps_ttm", "regime"} {
		if _, ok := keys[known]; !ok {
			t.Fatalf("enumeration missed known key %q", known)
		}
	}
	if _, ok := parts["today_net"]; !ok {
		t.Fatalf("enumeration missed the concatenated institution family (parts %v)", parts)
	}

	for k, pos := range keys {
		if strings.HasPrefix(k, "ep_") {
			t.Errorf("existing writer at %s already uses the ep_ prefix: %q", pos, k)
		}
	}
	for p, pos := range parts {
		if strings.HasPrefix(p, "ep_") {
			t.Errorf("concatenated key at %s starts with ep_: %q", pos, p)
		}
	}
	for _, k := range entryPlanEvidenceKeys {
		if pos, ok := keys[k]; ok {
			t.Errorf("registry key %q collides with an existing evidence key at %s", k, pos)
		}
	}
	if owner, ok := categories[store.CategoryEntryPlan]; !ok || owner != "internal/store/model.go#CategoryEntryPlan" {
		t.Errorf("category %q is declared by %q, want only store.CategoryEntryPlan", store.CategoryEntryPlan, owner)
	}
	n := 0
	for v := range categories {
		if v == store.CategoryEntryPlan {
			n++
		}
	}
	if n != 1 {
		t.Errorf("category value declared %d times", n)
	}

	// The dynamic half: a fully loaded watchlist projection (the keys concatenation produces
	// included) emits no ep_ key and no entry_plan category.
	var c collector
	e := fullyLoadedEntry("2330", "半導體")
	buildWatchlistEvidence(&c, e, &scanner.SectorRotation{Name: "半導體", Heat: e.TrendExt.Heat},
		MarketContext{Regime: "BULL", Score: f64(60)})
	if len(c.items) < 100 {
		t.Fatalf("anti-vacuity: fully loaded projection produced %d rows", len(c.items))
	}
	reg := map[string]bool{}
	for _, k := range entryPlanEvidenceKeys {
		reg[k] = true
	}
	for _, it := range c.items {
		if strings.HasPrefix(it.Key, "ep_") || it.Category == store.CategoryEntryPlan || reg[it.Key] {
			t.Errorf("non-entry-plan projection emitted %s/%s", it.Category, it.Key)
		}
	}
}

// ── 18 (structural half). nothing reads the ep_* rows back ────────────────────────────────

// epReferenceAllowlist is the EXACT set of declarations that may name an ep_* key literal, the
// entry_plan category literal, store.CategoryEntryPlan, or the registry identifiers.
var epReferenceAllowlist = map[string]string{
	epRegistryFile + "#<package-level>":       "the registry constants themselves",
	epRegistryFile + "#projectEntryPlan":      "the write-only projection",
	"internal/store/model.go#<package-level>": "the category constant's declaration",
	"internal/research/evidence.go#describeCounts": "the per-category row count in the " +
		"RecordScan log line; a tally of rows being written, not a read of their values",
}

// epReferenceSites returns decl → positions of every ep_ reference in dirs.
func epReferenceSites(t *testing.T, root string, dirs []string) (map[string][]string, int) {
	found := map[string][]string{}
	scanned := walkProductionGo(t, root, dirs, func(rel string, fset *token.FileSet, f *ast.File) {
		for _, decl := range f.Decls {
			key := rel + "#" + declName(decl)
			ast.Inspect(decl, func(n ast.Node) bool {
				hit := false
				switch x := n.(type) {
				case *ast.BasicLit:
					if x.Kind == token.STRING {
						v, _ := strconv.Unquote(x.Value)
						hit = strings.HasPrefix(v, "ep_") || v == "entry_plan"
					}
				case *ast.SelectorExpr:
					hit = x.Sel.Name == "CategoryEntryPlan"
				case *ast.Ident:
					hit = strings.HasPrefix(x.Name, "epKey") || x.Name == "entryPlanEvidenceKeys"
				}
				if hit {
					found[key] = append(found[key], fset.Position(n.Pos()).String())
				}
				return true
			})
		}
	})
	return found, scanned
}

func TestNoProductionCodeReadsEntryPlanEvidenceBack(t *testing.T) {
	root := epRepoRoot(t)
	found, scanned := epReferenceSites(t, root, []string{"cmd", "internal"})
	if scanned < 250 {
		t.Fatalf("scanned only %d production files", scanned)
	}
	for _, must := range []string{epRegistryFile + "#projectEntryPlan", epRegistryFile + "#<package-level>"} {
		if len(found[must]) == 0 {
			t.Fatalf("%s holds no ep_ reference — the scan is not seeing the registry (found %v)", must, found)
		}
	}
	var offenders []string
	for decl := range found {
		if _, ok := epReferenceAllowlist[decl]; !ok {
			offenders = append(offenders, decl)
		}
	}
	for decl := range epReferenceAllowlist {
		if len(found[decl]) == 0 {
			t.Errorf("allowlisted %s holds no ep_ reference — the allowlist describes code that no longer exists", decl)
		}
	}
	sort.Strings(offenders)
	for _, d := range offenders {
		t.Errorf("%s references ep_* evidence at %v. EntryPlan persistence is WRITE-ONLY: a read of "+
			"these rows is how a stored plan becomes an input to a score, action, ranking or price.", d, found[d])
	}

	// THE SCAN MUST CATCH WHAT IT CLAIMS TO: planted readers, through the same function.
	dir := t.TempDir()
	planted := map[string]string{
		"internal/scanner/watchlist.go": "package scanner\nfunc boost(m map[string]float64) float64 { return m[\"ep_rr_ratio\"] }\n",
		"cmd/scanner/rank.go":           "package main\nfunc pick(st S) { st.ByCategory(1, store.CategoryEntryPlan) }\n",
		"internal/research/ai.go":       "package research\nfunc leak() []string { return entryPlanEvidenceKeys }\n",
		"internal/report/x.go":          "package report\nvar cat = \"entry_plan\"\n",
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
	pf, _ := epReferenceSites(t, dir, []string{"cmd", "internal"})
	for _, want := range []string{"internal/scanner/watchlist.go#boost", "cmd/scanner/rank.go#pick",
		"internal/research/ai.go#leak", "internal/report/x.go#<package-level>"} {
		if len(pf[want]) == 0 {
			t.Errorf("planted reader %s was not caught (got %v)", want, pf)
		}
	}
}

// The decision packages cannot reach the store or the research layer at all, so they have no
// API through which to read an evidence row; and research does not reach valuation or
// healthcheck, so persistence cannot recompute a target (B6).
func TestEntryPlanPersistenceDependencyDirection(t *testing.T) {
	root := epRepoRoot(t)
	deps := func(pkg string) map[string]bool {
		cmd := exec.Command("go", "list", "-deps", pkg)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		got := map[string]bool{}
		for _, l := range strings.Split(string(out), "\n") {
			got[strings.TrimSpace(l)] = true
		}
		if len(got) < 5 {
			t.Fatalf("go list -deps %s returned %d packages", pkg, len(got))
		}
		return got
	}
	const mod = "github.com/deep-huang/stock-scanner/"
	for _, pkg := range []string{"./internal/scanner", "./internal/entryplan", "./internal/candidate", "./internal/report"} {
		d := deps(pkg)
		for _, forbidden := range []string{"internal/store", "internal/research"} {
			if d[mod+forbidden] {
				t.Errorf("%s depends on %s — the decision path can now read stored evidence", pkg, forbidden)
			}
		}
	}
	d := deps("./internal/research")
	if !d[mod+"internal/entryplan"] || !d[mod+"internal/store"] {
		t.Fatal("anti-vacuity: research no longer depends on entryplan and store")
	}
	for _, forbidden := range []string{"internal/valuation", "internal/healthcheck"} {
		if d[mod+forbidden] {
			t.Errorf("internal/research depends on %s — persistence must not load or recompute valuation", forbidden)
		}
	}
}

// ── decision 2 (structural half). persistence reads no clock and no session helper ───────
//
// STRUCTURAL CHECK, not behaviour proof. It parses two scopes and flags identifiers:
//   - all of internal/research/entryplan_evidence.go (which must also not import "time"), and
//   - the body of RecordScan's `for _, e := range in.Watchlist` loop in research.go, the loop
//     that calls buildEntryPlanEvidence. The rest of RecordScan is NOT scanned: it stamps
//     StartedAt / FinishRun with time.Now, which is run metadata, not a gate.
//
// It does not follow calls out of those scopes (collector, snapshotFrom, the store), does not
// see a clock reached through an interface or a func value with an innocuous name, and matches
// session helpers by NAME only. The behavioural half is
// TestEntryPlanSameIdentityPlanIsPersistedWithoutSessionGating.

const epResearchFile = "internal/research/research.go"

// epClockIdents are identifiers that read or interpret wall-clock time.
var epClockIdents = map[string]bool{"Now": true, "Since": true, "Until": true, "LoadLocation": true}

// epSessionIdent matches market-session helper names in this repo (sessionClosed, SameDay,
// tradingDay, ConfirmSession, hasSession, ...) and the obvious spellings of a close/EOD gate.
var epSessionIdent = regexp.MustCompile(`(?i)session|sameday|tradingday|market_?(open|close)|tradinghour|clock|^eod|eod$`)

// epClockRefs returns a position for every clock or session reference inside scope. Comments and
// string literals are not identifiers and are never flagged. Any selector on the file's "time"
// import (whatever its local name) is flagged, as is a dot import of "time".
func epClockRefs(fset *token.FileSet, file *ast.File, scope ast.Node) []string {
	timeNames := map[string]bool{}
	var out []string
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path != "time" {
			continue
		}
		switch {
		case imp.Name == nil:
			timeNames["time"] = true
		case imp.Name.Name == ".":
			out = append(out, fset.Position(imp.Pos()).String()+" dot import of time")
		default:
			timeNames[imp.Name.Name] = true
		}
	}
	ast.Inspect(scope, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && timeNames[id.Name] {
				out = append(out, fmt.Sprintf("%s %s.%s", fset.Position(x.Pos()), id.Name, x.Sel.Name))
			}
		case *ast.Ident:
			if epClockIdents[x.Name] || epSessionIdent.MatchString(x.Name) {
				out = append(out, fmt.Sprintf("%s %s", fset.Position(x.Pos()), x.Name))
			}
		}
		return true
	})
	return out
}

// epWatchlistLoop returns RecordScan's `range in.Watchlist` loop, and how many such loops exist.
func epWatchlistLoop(file *ast.File) (*ast.RangeStmt, int) {
	var loop *ast.RangeStmt
	n := 0
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "RecordScan" || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(node ast.Node) bool {
			rs, ok := node.(*ast.RangeStmt)
			if !ok {
				return true
			}
			if sel, ok := rs.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Watchlist" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "in" {
					loop, n = rs, n+1
				}
			}
			return true
		})
	}
	return loop, n
}

func epCalls(scope ast.Node, name string) bool {
	found := false
	ast.Inspect(scope, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

func epImportsTime(file *ast.File) bool {
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "time" {
			return true
		}
	}
	return false
}

// epClockOffenders applies the guard to the two scopes of a tree rooted at root.
func epClockOffenders(t *testing.T, root string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var out []string

	ev, err := parser.ParseFile(fset, filepath.Join(root, epRegistryFile), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !epCalls(ev, "projectEntryPlan") {
		t.Fatalf("anti-vacuity: %s no longer calls projectEntryPlan", epRegistryFile)
	}
	if epImportsTime(ev) {
		out = append(out, epRegistryFile+" imports time")
	}
	out = append(out, epClockRefs(fset, ev, ev)...)

	rs, err := parser.ParseFile(fset, filepath.Join(root, epResearchFile), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	loop, n := epWatchlistLoop(rs)
	if n != 1 {
		t.Fatalf("anti-vacuity: found %d `range in.Watchlist` loops in RecordScan, want 1", n)
	}
	if !epCalls(loop.Body, "buildEntryPlanEvidence") {
		t.Fatal("anti-vacuity: the watchlist loop no longer calls buildEntryPlanEvidence")
	}
	return append(out, epClockRefs(fset, rs, loop.Body)...)
}

func TestEntryPlanPersistenceHasNoClockOrSessionReference(t *testing.T) {
	if got := epClockOffenders(t, epRepoRoot(t)); len(got) != 0 {
		t.Errorf("EntryPlan persistence references a clock or market-session helper: %v. "+
			"Persistence is a recorder, not a session-policy engine (user decision 2): a same-symbol, "+
			"same-AsOf plan is recorded whatever the time.", got)
	}

	// THE GUARD MUST CATCH WHAT IT CLAIMS TO: planted trees through the same function.
	const cleanEvidence = "package research\nfunc projectEntryPlan() {}\nfunc buildEntryPlanEvidence() { projectEntryPlan() }\n"
	const cleanResearch = "package research\nimport \"time\"\n" +
		"func (r *Recorder) RecordScan(in Input) {\n" +
		"\tstart := time.Now() // run metadata, outside the loop: not scanned\n" +
		"\tfor _, e := range in.Watchlist {\n\t\t// time.Now() in a comment\n\t\t_ = \"time.Now\"\n\t\tbuildEntryPlanEvidence(e)\n\t}\n" +
		"\t_ = start\n}\n"
	loopWith := func(stmt string) string {
		return strings.Replace(cleanResearch, "\t\tbuildEntryPlanEvidence(e)\n", "\t\t"+stmt+"\n\t\tbuildEntryPlanEvidence(e)\n", 1)
	}
	evidenceWith := func(imports, body string) string {
		return "package research\n" + imports + "func projectEntryPlan() {\n" + body + "\n}\n" +
			"func buildEntryPlanEvidence() { projectEntryPlan() }\n"
	}
	cases := []struct {
		name, evidence, research string
		wantFlag                 bool
	}{
		{"clean trees, clock in comment / string / outside the loop", cleanEvidence, cleanResearch, false},
		{"time.Now in projection", evidenceWith("import \"time\"\n", "_ = time.Now()"), cleanResearch, true},
		{"aliased time.Since in projection", evidenceWith("import tm \"time\"\n", "_ = tm.Since(tm.Time{})"), cleanResearch, true},
		{"dot-imported Until in projection", evidenceWith("import . \"time\"\n", "_ = Until(Time{})"), cleanResearch, true},
		{"time imported for a non-clock type in projection", evidenceWith("import \"time\"\n", "var _ time.Duration"), cleanResearch, true},
		{"injected clk.Now in projection", evidenceWith("", "_ = clk.Now()"), cleanResearch, true},
		{"session helper in projection", evidenceWith("", "if !sessionClosed(x) { return }"), cleanResearch, true},
		{"time.LoadLocation in watchlist loop", cleanEvidence, loopWith("_, _ = time.LoadLocation(\"Asia/Taipei\")"), true},
		{"date-vs-today gate in watchlist loop", cleanEvidence, loopWith("if e.D >= time.Now().Format(\"2006-01-02\") { continue }"), true},
		{"news.SameDay in watchlist loop", cleanEvidence, loopWith("if news.SameDay(a, b) { continue }"), true},
		{"EOD flag in watchlist loop", cleanEvidence, loopWith("if !isEOD { continue }"), true},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		for rel, src := range map[string]string{epRegistryFile: tc.evidence, epResearchFile: tc.research} {
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got := epClockOffenders(t, dir)
		if (len(got) != 0) != tc.wantFlag {
			t.Errorf("planted %q: offenders %v, want flagged=%v", tc.name, got, tc.wantFlag)
		}
		t.Logf("planted %q: offenders %v", tc.name, got)
	}
}
