package candidate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// THE isolation guard, checked structurally rather than by comment: the scanner must not be
// able to depend on the candidate package even by accident.
//
// A dependency in this direction would mean candidate.yaml could influence the scan — a
// missing file could fail a run, or a candidate could enter the universe — and no amount of
// care inside the candidate package would prevent it.
func TestScannerDoesNotDependOnCandidate(t *testing.T) {
	root := repoRoot(t)
	for _, pkg := range []string{
		"./cmd/scanner", "./internal/scanner", "./internal/fetcher", "./internal/report",
	} {
		// Relative package paths resolve against the module root, not the test's directory.
		cmd := exec.Command("go", "list", "-deps", pkg)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		if strings.Contains(string(out), "stock-scanner/internal/candidate") {
			t.Errorf("%s depends on internal/candidate — the research universe has leaked "+
				"into the scan path", pkg)
		}
	}
}

// The scanner must never name candidate.yaml either. The dependency test above catches a
// call; this catches a path string reaching it some other way, such as a config default.
func TestScannerNeverNamesTheCandidateFile(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{
		filepath.Join(root, "internal", "scanner"),
		filepath.Join(root, "internal", "fetcher"),
		filepath.Join(root, "cmd", "scanner"),
	} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "candidate.yaml") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s names candidate.yaml; the scanner must not know it exists", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// The scanner's own universe must be identical whether or not a candidate file exists, and
// must never contain a candidate that is not also in stocks.yaml.
func TestCandidateFileDoesNotChangeTheScannerUniverse(t *testing.T) {
	dir := t.TempDir()
	stocks := filepath.Join(dir, "stocks.yaml")
	if err := os.WriteFile(stocks, []byte(`
positions:
  - code: "2317"
    name: "鴻海"
    market: "TW"
    entry: 200
    shares: 1000
watchlist:
  - code: "2454"
    name: "聯發科"
    market: "TW"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	universe := func() []string {
		t.Helper()
		sl, err := fetcher.LoadStockList(stocks)
		if err != nil {
			t.Fatalf("load stocks: %v", err)
		}
		var out []string
		for _, p := range sl.AllPositions() {
			out = append(out, p.Code)
		}
		for _, w := range sl.AllWatchlist() {
			out = append(out, w.Code)
		}
		return out
	}

	before := universe()

	// Now add a candidate file naming stocks that appear in NEITHER section.
	candPath := filepath.Join(dir, "candidate.yaml")
	if err := os.WriteFile(candPath, []byte(`
candidates:
  - symbol: "2330.TW"
    note: "not in stocks.yaml at all"
  - symbol: "6488.TWO"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := candidate.Load(candPath)
	if err != nil {
		t.Fatalf("load candidates: %v", err)
	}

	after := universe()
	if len(before) != len(after) {
		t.Fatalf("the scanner universe changed size: %v → %v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("the scanner universe changed: %v → %v", before, after)
		}
	}

	// And the two universes really are disjoint here, so the assertion above was not vacuous.
	inScanner := map[string]bool{}
	for _, c := range after {
		inScanner[c] = true
	}
	for _, c := range list.Candidates {
		if inScanner[c.Code] {
			t.Fatalf("fixture is wrong: %s is in both universes, so this test proves nothing",
				c.Code)
		}
	}
	if len(list.Candidates) != 2 {
		t.Errorf("candidate universe = %d, want 2 — it must not be filtered by stocks.yaml",
			len(list.Candidates))
	}
}
