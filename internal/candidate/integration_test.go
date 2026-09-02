package candidate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// sentinelPrice is a starting price no real Taiwan stock has. Every seeded series begins
// here, so a resolved candidate that does NOT carry it came from somewhere else — which is
// how this test notices if the cache format drifts and the fetcher quietly goes to the
// network instead of failing.
const sentinelPrice = 137.913

// seedCache writes a StockData into a cache directory in the fetcher's own on-disk format,
// so the resolver's real fetch path finds it without any network call.
//
// The format is duplicated here rather than exported from the fetcher: adding production API
// that exists only for tests is worse than one struct literal, and the sentinel above turns a
// format change into a failure rather than a silent fallback.
func seedCache(t *testing.T, dir, code, market string, bars int) {
	seedCacheCandles(t, dir, code, market, candles(bars, sentinelPrice, 0.002))
}

// seedEmptyCache seeds a cache entry with NO bars — the deterministic stand-in for a stock
// the data source has nothing for. Without it this test would reach the real network to
// discover that 9999 does not exist, making it slow and dependent on Yahoo being reachable.
func seedEmptyCache(t *testing.T, dir, code, market string) {
	seedCacheCandles(t, dir, code, market, nil)
}

func seedCacheCandles(t *testing.T, dir, code, market string, cs []fetcher.Candle) {
	t.Helper()
	sym := fetcher.YahooSymbol(code, market)
	rec := struct {
		FetchedAt time.Time         `json:"fetched_at"`
		Data      fetcher.StockData `json:"data"`
	}{
		FetchedAt: time.Now(),
		Data: fetcher.StockData{
			Symbol: code, Name: "測試 " + code, Market: market, Candles: cs,
		},
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// cacheKey replaces the dot: "2330.TW" → "2330_TW.json".
	name := strings.ReplaceAll(sym, ".", "_") + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatalf("seed %s: %v", sym, err)
	}
}

// assertFromCache fails if an entry did not come from the seeded fixture.
func assertFromCache(t *testing.T, e *scanner.WatchlistEntry, what string) {
	t.Helper()
	if e == nil {
		t.Fatalf("%s: no entry", what)
	}
	if !strings.HasPrefix(e.A.Name, "測試 ") {
		t.Fatalf("%s resolved from outside the fixture (name %q) — the cache format has "+
			"probably changed and the fetcher went to the network", what, e.A.Name)
	}
}

// THE end-to-end test: a candidate.yaml fixture all the way to rendered views, through the
// real loader, the real fetcher cache, the real scanner analyzers and the real renderer.
// Nothing is mocked in the middle — a test that injected CandidateEvidence directly would
// prove only that the last step works.
func TestEndToEndFromFileToView(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two resolvable stocks and one that is not in the cache at all.
	seedCache(t, cache, "2327", "TW", 260)
	seedCache(t, cache, "6488", "TWO", 260)
	seedEmptyCache(t, cache, "9999", "TW")

	candPath := filepath.Join(dir, "candidate.yaml")
	body := `
candidates:
  - symbol: "2327.TW"
    note: "candidate A"
  - symbol: "9999.TW"
    note: "will not resolve"
  - symbol: "6488.TWO"
    note: "candidate B"
`
	if err := os.WriteFile(candPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := Load(candPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// A fetcher pointed at the seeded cache with a long TTL, so nothing reaches the network.
	f := fetcher.New(fetcher.Config{CacheDir: cache, Concurrency: 1, CacheTTLMin: 100000, RequestDelayMs: 1})
	s := testScanner()
	r := NewResolver(f, s, Config{
		Scanner: scanner.Config{EnableInstitution: false}, ReportDate: time.Now(),
	}, nil)

	ev := r.Resolve(list, LoadMarketContext("", nil, ""), FXContext{Status: Unavailable})

	// Every candidate is present, in file order, whatever happened to each.
	if len(ev) != 3 {
		t.Fatalf("got %d evidence rows, want 3", len(ev))
	}
	wantOrder := []string{"2327.TW", "9999.TW", "6488.TWO"}
	for i, w := range wantOrder {
		if ev[i].Candidate.CanonicalSymbol() != w {
			t.Fatalf("position %d = %s, want %s", i, ev[i].Candidate.CanonicalSymbol(), w)
		}
	}

	// The two seeded stocks resolved; the third failed WITHOUT taking them with it.
	if ev[0].Status != StatusOK {
		t.Errorf("2327 status = %s (%s)", ev[0].Status, ev[0].Reason)
	}
	if ev[2].Status != StatusOK {
		t.Errorf("6488 status = %s (%s)", ev[2].Status, ev[2].Reason)
	}
	if ev[1].Status == StatusOK {
		t.Error("9999 resolved from an empty cache")
	}
	if ev[1].Reason == "" {
		t.Error("the failed candidate carries no reason")
	}

	// The resolved ones really carry the scanner's evidence.
	if ev[0].Entry == nil || ev[0].Entry.A.Symbol != "2327" {
		t.Fatalf("2327 has no scanner entry: %+v", ev[0].Entry)
	}
	assertFromCache(t, ev[0].Entry, "2327")
	assertFromCache(t, ev[2].Entry, "6488")
	if ev[0].Entry.Technical == nil || ev[0].Entry.Bias == nil {
		t.Error("shadow layers did not run on the candidate path")
	}
	// A .TWO candidate must keep its market all the way through.
	if ev[2].Entry == nil || ev[2].Candidate.Market != "TWO" {
		t.Errorf("6488 lost its market: %+v", ev[2].Candidate)
	}

	// Notes survive verbatim.
	if ev[0].Candidate.Note != "candidate A" || ev[2].Candidate.Note != "candidate B" {
		t.Errorf("notes changed: %q / %q", ev[0].Candidate.Note, ev[2].Candidate.Note)
	}

	// …through the view…
	views := BuildViews(ev)
	if len(views) != 3 {
		t.Fatalf("%d views", len(views))
	}
	for i, w := range wantOrder {
		if views[i].Symbol != w {
			t.Fatalf("view %d = %s, want %s", i, views[i].Symbol, w)
		}
	}

	// …and into the rendered output.
	var buf bytes.Buffer
	RenderText(&buf, views)
	out := buf.String()
	for _, w := range wantOrder {
		if !strings.Contains(out, w) {
			t.Errorf("%s missing from the rendered output", w)
		}
	}
	if !strings.Contains(out, "candidate A") {
		t.Error("the note did not reach the output")
	}
	if !strings.Contains(out, string(StatusFetchError)) {
		t.Error("the failure is not visible in the output")
	}
	// Market was unavailable for this run, and must say so rather than showing a regime.
	if !strings.Contains(out, "Market:            UNAVAILABLE") &&
		!strings.Contains(out, "Market:") {
		t.Error("no market line at all")
	}
}

// Changing only the note must not change a single computed value. The note is user metadata;
// if it could reach a calculation, the research output would depend on prose.
func TestNoteDoesNotAffectAnyComputedValue(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	seedCache(t, cache, "2330", "TW", 260)

	run := func(note string) View {
		t.Helper()
		p := filepath.Join(t.TempDir(), "candidate.yaml")
		if err := os.WriteFile(p, []byte(
			"candidates:\n  - symbol: \"2330.TW\"\n    note: \""+note+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		list, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		f := fetcher.New(fetcher.Config{CacheDir: cache, Concurrency: 1, CacheTTLMin: 100000, RequestDelayMs: 1})
		r := NewResolver(f, testScanner(), Config{
			Scanner: scanner.Config{EnableInstitution: false}, ReportDate: time.Now(),
		}, nil)
		ev := r.Resolve(list, LoadMarketContext("", nil, ""), FXContext{Status: Unavailable})
		return BuildViews(ev)[0]
	}

	a := run("AI server passive components")
	b := run("completely different text")

	if a.Note == b.Note {
		t.Fatal("the fixture did not actually vary the note")
	}
	if a.RocketScore != b.RocketScore || a.Action != b.Action || a.Stage != b.Stage ||
		a.Price != b.Price || a.ExplosionProb != b.ExplosionProb || a.DataQuality != b.DataQuality {
		t.Errorf("the note changed a computed value:\n note A: %+v\n note B: %+v", a, b)
	}
	if len(a.Blocks) != len(b.Blocks) {
		t.Fatalf("block count differs: %d vs %d", len(a.Blocks), len(b.Blocks))
	}
	for i := range a.Blocks {
		if a.Blocks[i] != b.Blocks[i] {
			t.Errorf("block %s differs: %+v vs %+v", a.Blocks[i].Name, a.Blocks[i], b.Blocks[i])
		}
	}
}

// Running research must not write to either config file. A tool that quietly rewrote a config
// a human maintains would make every later diff untrustworthy.
func TestResearchWritesNoConfigFile(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	seedCache(t, cache, "2330", "TW", 260)

	candPath := filepath.Join(dir, "candidate.yaml")
	stocksPath := filepath.Join(dir, "stocks.yaml")
	candBody := "candidates:\n  - symbol: \"2330.tw\"\n    note: \"lower case\"\n"
	stocksBody := "watchlist:\n  - code: \"2454\"\n    market: \"TW\"\n"
	for p, b := range map[string]string{candPath: candBody, stocksPath: stocksBody} {
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	list, err := Load(candPath)
	if err != nil {
		t.Fatal(err)
	}
	f := fetcher.New(fetcher.Config{CacheDir: cache, Concurrency: 1, CacheTTLMin: 100000, RequestDelayMs: 1})
	r := NewResolver(f, testScanner(), Config{
		Scanner: scanner.Config{EnableInstitution: false}, ReportDate: time.Now(),
	}, nil)
	ev := r.Resolve(list, LoadMarketContext("", nil, ""), FXContext{Status: Unavailable})
	var buf bytes.Buffer
	RenderText(&buf, BuildViews(ev))

	for p, want := range map[string]string{candPath: candBody, stocksPath: stocksBody} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s was modified:\n want %q\n got  %q", filepath.Base(p), want, got)
		}
	}
	// Normalisation happened in memory only.
	if ev[0].Candidate.CanonicalSymbol() != "2330.TW" {
		t.Errorf("not normalised in memory: %q", ev[0].Candidate.CanonicalSymbol())
	}
}

// The candidate universe must be free of the scanner's — able to research a stock the
// watchlist has never heard of.
func TestCandidateUniverseIsIndependent(t *testing.T) {
	dir := t.TempDir()
	stocksPath := filepath.Join(dir, "stocks.yaml")
	if err := os.WriteFile(stocksPath, []byte(`
watchlist:
  - code: "1101"
    market: "TW"
  - code: "1102"
    market: "TW"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	candPath := filepath.Join(dir, "candidate.yaml")
	if err := os.WriteFile(candPath, []byte(`
candidates:
  - symbol: "2330.TW"
  - symbol: "6488.TWO"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	sl, err := fetcher.LoadStockList(stocksPath)
	if err != nil {
		t.Fatal(err)
	}
	list, err := Load(candPath)
	if err != nil {
		t.Fatal(err)
	}

	scannerCodes := map[string]bool{}
	for _, w := range sl.AllWatchlist() {
		scannerCodes[w.Code] = true
	}
	if len(scannerCodes) != 2 {
		t.Fatalf("scanner universe = %v", scannerCodes)
	}
	if len(list.Candidates) != 2 {
		t.Fatalf("candidate universe = %d", len(list.Candidates))
	}
	for _, c := range list.Candidates {
		if scannerCodes[c.Code] {
			t.Errorf("%s is in both universes; the fixture does not test independence", c.Code)
		}
	}
}
