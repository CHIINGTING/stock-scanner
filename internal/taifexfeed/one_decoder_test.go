package taifexfeed_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// §10.2's reviewer check, as a test rather than as a grep somebody has to remember to run.
//
// The spec says: "There must be exactly ONE code path that turns a TAIFEX institutional-futures
// response into a domain value. A reviewer check on M3: grep for a second decoder of
// OpenInterest(Net) should find nothing."
//
// That check was performed by eye and passed while a second decoder existed, because the two
// spellings lived in files nobody diffed against each other — internal/market/provider's
// taifexRow struct tags and internal/derivatives/provider's column constants. So the check is
// mechanised here, and it is mechanised on STRING LITERALS rather than on file text: the column
// names are discussed in half a dozen comments, and a grep over raw bytes would either drown in
// them or be narrowed until it stopped catching anything.
//
// A string literal naming one of these columns outside this package is, by construction, code
// that reaches into the response itself — a struct tag, a map key, a comparison — which is the
// only way a second decoder can be written.
var feedColumns = []string{
	"OpenInterest(", "TradingVolume(", "TradingValue(", "ContractValueofOpenInterest(",
}

func TestOnlyThisPackageSpellsTheInstitutionalFuturesColumns(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(root, "internal", "taifexfeed")

	fset := token.NewFileSet()
	var offenders []string

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip what is not this repository's own Go source. testdata is skipped because
			// a fixture that spells a column name is the POINT of a fixture.
			if name == "vendor" || name == "testdata" || name == "node_modules" ||
				(strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Tests may name the columns freely: a test that builds a response has to spell it,
		// and a test cannot be the production decode path.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(path, owner+string(filepath.Separator)) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0) // no comments
		if perr != nil {
			return nil // not our business here; the build catches it
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				s = lit.Value // a raw string with an odd escape; match on it anyway
			}
			for _, col := range feedColumns {
				if strings.Contains(s, col) {
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders, rel+":"+
						strconv.Itoa(fset.Position(lit.Pos()).Line)+": "+lit.Value)
					return false
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("the institutional-futures columns are spelled outside internal/taifexfeed:\n  %s\n\n"+
			"That is how the FU-10 failure was written the first time: two decoders of the same\n"+
			"bytes, disagreeing about them (parseAmount(\"-\") == 0 vs decodeNum(\"-\") == ABSENT),\n"+
			"so the same day reported differently depending on which entry point ran. §10.2 allows\n"+
			"exactly one decode; a reader that needs these columns imports the constants.",
			strings.Join(offenders, "\n  "))
	}
}

// The converse, so the test above cannot pass because the names it looks for are wrong: this
// package really does spell every one of them.
func TestThisPackageSpellsThemAll(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "taifexfeed.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, col := range feedColumns {
			if strings.Contains(s, col) {
				found[col] = true
			}
		}
		return true
	})
	for _, col := range feedColumns {
		if !found[col] {
			t.Errorf("internal/taifexfeed does not spell %q — the isolation check above is "+
				"asserting against a column name that no longer exists and would pass for the "+
				"wrong reason", col)
		}
	}
}
