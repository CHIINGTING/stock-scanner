package recon

import (
	"encoding/json"

	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// ──────────────────────────────────────────────────────────────────────────────────────────
// The two properties internal/scanner/entryplan_symbol_guard_test.go's EP-9 allowlist entries
// CLAIM. They are here rather than there because the claim is about this package, and a claim
// whose evidence lives only in the allowlist's comment is the defect this repo keeps catching.
// ──────────────────────────────────────────────────────────────────────────────────────────

// TestTheResearchReadersNeverMutateAPlan drives the real readers over real plans and compares
// the plans' serialized form before and after.
//
// JSON rather than reflect.DeepEqual because the plan graph is full of pointers: DeepEqual
// would compare what they point AT, which is what we want, but a marshalled copy also catches
// a reader that swapped a pointer for an identical value, and it is what any archive of these
// plans would actually store.
func TestTheResearchReadersNeverMutateAPlan(t *testing.T) {
	plans := []*entryplan.Plan{
		{Symbol: "2330", AsOf: "2026-09-10", Status: entryplan.StatusBuyNow,
			RuleVersion: entryplan.RuleVersion,
			IdealEntry:  &entryplan.PriceZone{Low: 99, High: 101},
			Target1:     &entryplan.PriceLevel{Price: 120},
			EntryTrace:  &entryplan.EntryTrace{RuleVersion: entryplan.RuleVersion}},
		{Symbol: "2317", AsOf: "2026-09-10", Status: entryplan.StatusInsufficientData,
			RuleVersion: entryplan.RuleVersion,
			EntryTrace:  &entryplan.EntryTrace{RuleVersion: entryplan.RuleVersion}},
		{Symbol: "2454", AsOf: "2026-09-10", Status: entryplan.StatusNoValidEntry,
			RuleVersion: entryplan.RuleVersion},
	}
	plans[0].EntryTrace.Decision.Semantic = entryplan.EntrySemanticPullback
	plans[0].EntryTrace.Targets.ValuationCeiling = entryplan.ValuationCeilingApplied

	before := make([]string, len(plans))
	for i, p := range plans {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		before[i] = string(b)
	}

	// Reader 1: the funnel's per-entry counter.
	f := NewFunnel(ArmReplayedPIT, FullFidelity)
	entries := make([]scanner.WatchlistEntry, len(plans))
	for i := range plans {
		entries[i].A.Symbol = plans[i].Symbol
		entries[i].EntryPlan = plans[i]
	}
	f.Add(SessionResult{Universe: len(entries), Entries: entries})
	if f.PlansGenerated != len(plans) {
		t.Fatalf("the funnel read %d plans of %d — the test would be vacuous",
			f.PlansGenerated, len(plans))
	}
	// Reader 2: the semantic projection.
	for _, p := range plans {
		_ = PlanSemantic(p)
	}

	for i, p := range plans {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(b) != before[i] {
			t.Fatalf("plan %s was MUTATED by a research reader:\n before %s\n after  %s",
				p.Symbol, before[i], b)
		}
	}
}

// TestTheMutationDetectorWouldSeeAChange is the anti-vacuity half: the comparison above must
// actually be able to fail.
func TestTheMutationDetectorWouldSeeAChange(t *testing.T) {
	p := &entryplan.Plan{Symbol: "2330", Status: entryplan.StatusBuyNow}
	b1, _ := json.Marshal(p)
	p.Status = entryplan.StatusNoValidEntry
	b2, _ := json.Marshal(p)
	if string(b1) == string(b2) {
		t.Fatal("the JSON comparison cannot see a changed status")
	}
}

// TestNoProductionPackageImportsTheResearchLayer enforces "downstream" rather than asserting
// it: if any package outside internal/entryplanbacktest/... and cmd/ep9-* ever imports the
// research layer, the research readers stop being out of band and the allowlist's reason
// stops being true.
func TestNoProductionPackageImportsTheResearchLayer(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const researchPrefix = "github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	fset := token.NewFileSet()
	var scanned int
	var offenders []string

	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, de fs.DirEntry, err error) error {
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
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			// The research layer and its own commands may of course import it.
			if strings.HasPrefix(rel, "internal/entryplanbacktest/") ||
				strings.HasPrefix(rel, "cmd/ep9-") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			scanned++
			for _, im := range f.Imports {
				p := strings.Trim(im.Path.Value, `"`)
				if strings.HasPrefix(p, researchPrefix) {
					offenders = append(offenders, rel+" imports "+p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 250 {
		t.Fatalf("scanned only %d production files — the walk is not seeing the repository", scanned)
	}
	for _, o := range offenders {
		t.Errorf("%s — EP-9's research layer must stay downstream of production. If a "+
			"production package needs it, that is an architecture change and the allowlist "+
			"entry in internal/scanner/entryplan_symbol_guard_test.go stops being true.", o)
	}

	// ANTI-VACUITY: the walk must catch a planted import through the same predicate.
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "report", "x.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package report\nimport _ \"" + researchPrefix + "/study\"\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	pf, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var sawPlanted bool
	for _, im := range pf.Imports {
		if strings.HasPrefix(strings.Trim(im.Path.Value, `"`), researchPrefix) {
			sawPlanted = true
		}
	}
	if !sawPlanted {
		t.Fatal("the import predicate does not recognise a planted research import")
	}

}
