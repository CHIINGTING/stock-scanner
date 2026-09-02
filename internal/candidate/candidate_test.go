package candidate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write drops a candidate file into a temp dir and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "candidate.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidList(t *testing.T) {
	got, err := Load(write(t, `
candidates:
  - symbol: "2330.TW"
    note: "AI semiconductor"
  - symbol: "6488.TWO"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got.Candidates))
	}

	// A TWSE symbol resolves to the canonical (code, market) pair every other package uses.
	a := got.Candidates[0]
	if a.Code != "2330" || a.Market != "TW" {
		t.Errorf("2330.TW → code=%q market=%q", a.Code, a.Market)
	}
	if a.Note != "AI semiconductor" {
		t.Errorf("note = %q", a.Note)
	}
	if a.CanonicalSymbol() != "2330.TW" {
		t.Errorf("canonical = %q", a.CanonicalSymbol())
	}

	// And a TPEx one keeps its market — collapsing TWO into TW would fetch the wrong stock.
	b := got.Candidates[1]
	if b.Code != "6488" || b.Market != "TWO" {
		t.Errorf("6488.TWO → code=%q market=%q", b.Code, b.Market)
	}
	if b.Note != "" {
		t.Errorf("an absent note became %q", b.Note)
	}
}

// A missing file is its own error, because the caller's response differs from a broken one.
func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("a missing file loaded successfully")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound = false for %v — callers cannot tell 'not configured' from 'broken'", err)
	}
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("the error does not name the path: %v", err)
	}

	// A malformed file must NOT look like a missing one.
	_, err = Load(write(t, "candidates:\n  - symbol: \"2330.TW\"\n   note: broken\n"))
	if err == nil {
		t.Fatal("malformed YAML loaded successfully")
	}
	if IsNotFound(err) {
		t.Error("a malformed file was reported as missing")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	for name, body := range map[string]string{
		"bad indent":  "candidates:\n  - symbol: \"2330.TW\"\n   note: broken\n",
		"not a list":  "candidates: 2330.TW\n",
		"unknown key": "candidates:\n  - sybmol: \"2330.TW\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Error("loaded successfully; want an error")
			}
		})
	}
}

// An empty list is a valid configuration, not an error — and it must NEVER fall back to the
// scanner's watchlist, which is a different universe with different semantics.
func TestLoadEmptyList(t *testing.T) {
	for _, body := range []string{"candidates: []\n", "candidates:\n"} {
		got, err := Load(write(t, body))
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if len(got.Candidates) != 0 {
			t.Errorf("%q produced %d candidates", body, len(got.Candidates))
		}
		if got.Candidates == nil {
			t.Errorf("%q returned a nil slice; want an empty one so callers can range freely", body)
		}
	}
}

func TestLoadEmptySymbol(t *testing.T) {
	_, err := Load(write(t, "candidates:\n  - symbol: \"\"\n    note: x\n"))
	if err == nil {
		t.Fatal("an empty symbol loaded successfully")
	}
	if !strings.Contains(err.Error(), "no symbol") {
		t.Errorf("error should say the symbol is missing: %v", err)
	}
}

// Symbols this repo cannot fetch must fail at the config boundary, not become a mysterious
// empty result later.
func TestLoadInvalidSymbol(t *testing.T) {
	for name, sym := range map[string]string{
		"no market suffix": "2330",
		"letters only":     "ABC",
		"unsupported US":   "2330.US",
		"lowercase market": "2330.us",
		"code too short":   "233.TW",
		"code too long":    "1234567.TW",
		"letter first":     "A330.TW",
		"letter mid":       "23A0.TW",
		"empty code":       ".TW",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "candidates:\n  - symbol: \""+sym+"\"\n"))
			if err == nil {
				t.Errorf("%q was accepted", sym)
			}
		})
	}

	// ETF-shaped codes ARE valid here: this is a research universe, not the scan universe.
	// Refusing 0050 would mean the benchmark itself could not be written down.
	for _, sym := range []string{"0050.TW", "00981A.TW", "00632R.TW", "5483.TWO"} {
		if _, err := Load(write(t, "candidates:\n  - symbol: \""+sym+"\"\n")); err != nil {
			t.Errorf("%q was rejected: %v", sym, err)
		}
	}
}

// A duplicate is a config mistake in a hand-maintained list. Silently keeping one copy would
// hide it forever.
func TestLoadDuplicate(t *testing.T) {
	_, err := Load(write(t, `
candidates:
  - symbol: "2330.TW"
  - symbol: "2454.TW"
  - symbol: "2330.TW"
`))
	if err == nil {
		t.Fatal("a duplicate loaded successfully")
	}
	if !strings.Contains(err.Error(), "2330.TW") {
		t.Errorf("the error does not name the duplicate: %v", err)
	}
	// It must point at BOTH positions, or the human has to hunt for the other one.
	if !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "3") {
		t.Errorf("the error does not name both positions: %v", err)
	}
}

// THE case a raw-string check would miss: two spellings of one stock.
func TestLoadNormalizedDuplicate(t *testing.T) {
	for _, pair := range [][2]string{
		{"2330.tw", "2330.TW"},
		{"2330.TW", "2330.tw"},
		{"6488.two", "6488.TWO"},
		{" 2330.TW ", "2330.TW"},
	} {
		body := "candidates:\n  - symbol: \"" + pair[0] + "\"\n  - symbol: \"" + pair[1] + "\"\n"
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%q and %q were accepted as two candidates; they are one stock",
				pair[0], pair[1])
		}
	}
}

// The file's order is a human's priority statement. Nothing may re-sort it.
func TestLoadPreservesOrder(t *testing.T) {
	// Deliberately NOT alphabetical and not score-ordered, so any sort would be visible.
	got, err := Load(write(t, `
candidates:
  - symbol: "6488.TWO"
  - symbol: "2330.TW"
  - symbol: "2327.TW"
  - symbol: "0050.TW"
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"6488.TWO", "2330.TW", "2327.TW", "0050.TW"}
	for i, w := range want {
		if got.Candidates[i].CanonicalSymbol() != w {
			t.Fatalf("position %d = %s, want %s (order was not preserved: %v)",
				i, got.Candidates[i].CanonicalSymbol(), w, symbols(got))
		}
	}
}

func symbols(l *List) []string {
	out := make([]string, 0, len(l.Candidates))
	for _, c := range l.Candidates {
		out = append(out, c.CanonicalSymbol())
	}
	return out
}

// The loader must not touch the file. A tool that quietly rewrote a config a human maintains
// would make every later diff untrustworthy.
func TestLoadIsReadOnly(t *testing.T) {
	body := "candidates:\n  - symbol: \"2330.tw\"\n    note: \"lower case on purpose\"\n"
	p := write(t, body)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// Normalisation happened in memory…
	if got.Candidates[0].CanonicalSymbol() != "2330.TW" {
		t.Errorf("not normalised in memory: %q", got.Candidates[0].CanonicalSymbol())
	}
	// …and the raw symbol the human typed is still available for error messages.
	if got.Candidates[0].Symbol != "2330.tw" {
		t.Errorf("the typed symbol was overwritten: %q", got.Candidates[0].Symbol)
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the file was modified:\nbefore: %q\nafter:  %q", before, after)
	}
}

// The shipped candidate.yaml must itself be valid, or the first real run fails.
func TestShippedCandidateFileIsValid(t *testing.T) {
	got, err := Load("../../candidate.yaml")
	if err != nil {
		t.Fatalf("the repo's own candidate.yaml does not load: %v", err)
	}
	if len(got.Candidates) == 0 {
		t.Skip("shipped file is empty; nothing to check")
	}
	for _, c := range got.Candidates {
		if c.Code == "" || c.Market == "" {
			t.Errorf("%s did not resolve: %+v", c.Symbol, c)
		}
	}
}
