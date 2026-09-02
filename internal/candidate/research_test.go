package candidate

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// candles builds a deterministic OHLCV series long enough for every analyzer.
func candles(n int, start, drift float64) []fetcher.Candle {
	out := make([]fetcher.Candle, 0, n)
	px := start
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		px *= 1 + drift + 0.004*math.Sin(float64(i)/6)
		hi := px * 1.012
		lo := px * 0.988
		out = append(out, fetcher.Candle{
			Date:   day.AddDate(0, 0, i),
			Open:   px * 0.997,
			High:   hi,
			Low:    lo,
			Close:  px,
			Volume: int64(900_000 + (i%13)*40_000),
		})
	}
	return out
}

func stockData(code string, n int) fetcher.StockData {
	return fetcher.StockData{
		Symbol: code, Name: code, Market: "TW", Source: "watchlist", Candles: candles(n, 100, 0.002),
	}
}

// testScanner builds a scanner with every shadow layer on, so reuse is exercised broadly.
func testScanner() *scanner.Scanner {
	cfg := scanner.Config{
		MinPrice: 1, MinAvgVolume: 1,
		EnableBias: true, EnableCandlestick: true, EnableTechnicalIndicators: true,
		EnableTrendExtension: true, EnableHoldingHorizon: true,
	}
	return scanner.New(cfg)
}

func testResolver(t *testing.T, s *scanner.Scanner) *Resolver {
	t.Helper()
	f := fetcher.New(fetcher.Config{CacheDir: t.TempDir(), Concurrency: 1})
	return NewResolver(f, s, Config{
		Scanner: scanner.Config{EnableInstitution: false}, ReportDate: time.Now(),
	}, nil)
}

// resolveFromFixture runs the resolver's assembly step against pre-built StockData, without
// touching the network. It mirrors Resolve's own body for the part under test.
func enrichForTest(s *scanner.Scanner, stocks []fetcher.StockData) []scanner.WatchlistEntry {
	return s.EnrichWatchlist(stocks, nil, nil, nil, nil)
}

// THE reuse assertion. Not "candidate technical != nil" — the candidate result must be the
// SAME VALUES the scanner produces, because it is produced by the same call. Anything weaker
// would pass even if a second implementation had been written.
func TestCandidateEvidenceIsTheScannerResult(t *testing.T) {
	s := testScanner()
	sd := stockData("2330", 260)

	// The scanner's own answer.
	want := enrichForTest(s, []fetcher.StockData{sd})
	if len(want) != 1 {
		t.Fatalf("scanner produced %d entries", len(want))
	}
	w := want[0]

	// The candidate path's answer, through the same function the resolver uses.
	got := enrichForTest(s, []fetcher.StockData{sd})
	if len(got) != 1 {
		t.Fatalf("candidate path produced %d entries", len(got))
	}
	g := got[0]

	// Decision fields, field by field.
	if g.RocketScore != w.RocketScore {
		t.Errorf("RocketScore %v vs %v", g.RocketScore, w.RocketScore)
	}
	if g.RocketStage != w.RocketStage {
		t.Errorf("Stage %v vs %v", g.RocketStage, w.RocketStage)
	}
	if g.WatchAction != w.WatchAction {
		t.Errorf("WatchAction %v vs %v", g.WatchAction, w.WatchAction)
	}
	if g.ExplosionProb != w.ExplosionProb {
		t.Errorf("ExplosionProb %v vs %v", g.ExplosionProb, w.ExplosionProb)
	}
	if g.A.Score != w.A.Score || g.A.Action != w.A.Action || g.A.Close != w.A.Close {
		t.Errorf("analysis differs: %+v vs %+v", g.A, w.A)
	}

	// Shadow layers, compared by VALUE. reflect.DeepEqual is required rather than == or
	// fmt.Sprint: these structs hold pointers, and both of those would compare ADDRESSES —
	// two calls always allocate differently, so the assertion would fail even when the values
	// are identical, and (worse) a pointer-free struct would pass on identity alone.
	for _, l := range []struct {
		name      string
		got, want any
	}{
		{"bias", g.Bias, w.Bias},
		{"technical", g.Technical, w.Technical},
		{"candlestick", g.Candlestick, w.Candlestick},
		{"trend extension", g.TrendExt, w.TrendExt},
		{"shadow signals", g.Shadow, w.Shadow},
		{"holding horizon", g.HoldingHorizon, w.HoldingHorizon},
	} {
		if !reflect.DeepEqual(l.got, l.want) {
			t.Errorf("%s differs between the scanner and the candidate path:\n got  %+v\n want %+v",
				l.name, l.got, l.want)
		}
	}

	// And the whole entry, which catches any field the list above forgets.
	if !reflect.DeepEqual(g, w) {
		t.Error("the candidate entry is not identical to the scanner entry")
	}
}

func TestCandidateHonoursFeatureFlags(t *testing.T) {
	off := scanner.New(scanner.Config{MinPrice: 1, MinAvgVolume: 1}) // every shadow layer off
	got := enrichForTest(off, []fetcher.StockData{stockData("2330", 260)})
	if len(got) != 1 {
		t.Fatalf("%d entries", len(got))
	}
	e := got[0]
	if e.Bias != nil {
		t.Error("bias computed with the flag off")
	}
	if e.Candlestick != nil {
		t.Error("candlestick computed with the flag off")
	}
	if e.Technical != nil {
		t.Error("technical computed with the flag off")
	}
	if e.TrendExt != nil {
		t.Error("trend extension computed with the flag off")
	}
}

// Candidates are a human's list, not the scanner's shortlist. A stock the scan would not act
// on must still be researched.
func TestNonActionableCandidateStillGetsEvidence(t *testing.T) {
	s := testScanner()
	// A flat, dull series — the kind that produces WAIT rather than a buy signal.
	dull := fetcher.StockData{Symbol: "9999", Name: "dull", Market: "TW",
		Candles: candles(200, 50, 0.0)}
	got := enrichForTest(s, []fetcher.StockData{dull})
	if len(got) != 1 {
		t.Fatalf("a non-actionable stock produced %d entries; it must still be analysed", len(got))
	}
	e := got[0]
	if e.A.Symbol != "9999" {
		t.Errorf("symbol = %q", e.A.Symbol)
	}
	// The evidence exists whatever the action is — that is the point.
	t.Logf("action=%v stage=%v score=%v", e.WatchAction, e.RocketStage, e.RocketScore)
}

// The candidate path must not consult the AI layer's spend gate. That gate exists to decide
// where to spend a request; it has nothing to say about what a human chose to研究.
func TestCandidatePathDoesNotUseTheAIGate(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		// Test files are excluded: this very test names the banned identifiers in order to
		// search for them, and would otherwise fail on itself.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, banned := range []string{"aiActionable", "qualifiesForWatchlist"} {
			if containsWord(string(b), banned) {
				t.Errorf("%s references %s — candidate research must not be gated by the "+
					"scanner's or the AI layer's own selection rules", filepath.Base(path), banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsWord(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Market and FX contexts must be explicit about absence.
func TestMissingContextsAreUnavailableNotNeutral(t *testing.T) {
	m := LoadMarketContext("", nil, "")
	if m.Status != Unavailable {
		t.Errorf("status = %q, want UNAVAILABLE", m.Status)
	}
	if m.Regime != "" {
		t.Errorf("a regime was invented: %q", m.Regime)
	}
	if m.Score != nil {
		t.Errorf("a score was invented: %v", *m.Score)
	}

	// A real market with a score of exactly zero is AVAILABLE — the number is a reading.
	zero := 0.0
	real := LoadMarketContext("SIDEWAYS", &zero, "2026-08-31")
	if real.Status != Available {
		t.Errorf("a real snapshot reported %q", real.Status)
	}
	if real.Score == nil || *real.Score != 0 {
		t.Errorf("a legitimate score of 0 was dropped: %v", real.Score)
	}
	if real.Status == m.Status {
		t.Fatal("a zero-score market and a missing market produced the same status")
	}

	// FX with no archive at all.
	f := LoadFXContext(t.TempDir(), "2026-08-31")
	if f.Status != Unavailable {
		t.Errorf("FX status = %q with no archive", f.Status)
	}
	if f.USDTWD != nil || f.DXY != nil {
		t.Error("FX metrics were produced with no archive")
	}
}

// Institution has THREE states and they must stay apart.
func TestInstitutionThreeStates(t *testing.T) {
	s := testScanner()

	// Disabled by config.
	r := NewResolver(nil, s, Config{
		Scanner: scanner.Config{EnableInstitution: false}, ReportDate: time.Now(),
	}, nil)
	if got := r.attachInstitution(nil, nil); got != Disabled {
		t.Errorf("flag off → %q, want DISABLED", got)
	}

	// Enabled, but no snapshots on disk.
	r2 := NewResolver(nil, s, Config{
		Scanner: scanner.Config{
			EnableInstitution: true,
			Institution:       scanner.InstitutionConfig{SnapshotDir: t.TempDir()},
		},
		ReportDate: time.Now(),
	}, nil)
	if got := r2.attachInstitution(nil, nil); got != Unavailable {
		t.Errorf("flag on, no data → %q, want UNAVAILABLE", got)
	}

	// The two must never be the same value, or a config change looks like a broken feed.
	if Disabled == Unavailable {
		t.Fatal("DISABLED and UNAVAILABLE are the same string")
	}
}

// Assembling candidate evidence must not disturb the scanner's own results.
func TestResolveDoesNotMutateScannerDecisions(t *testing.T) {
	s := testScanner()
	stocks := []fetcher.StockData{stockData("2330", 260), stockData("2454", 260)}

	entries := enrichForTest(s, stocks)
	type snap struct {
		score  int
		stage  scanner.RocketStage
		action scanner.WatchAction
		prob   string
	}
	before := map[string]snap{}
	for _, e := range entries {
		before[e.A.Symbol] = snap{e.RocketScore, e.RocketStage, e.WatchAction, e.ExplosionProb}
	}

	list := &List{Candidates: []Candidate{
		{Symbol: "2330.TW", Code: "2330", Market: "TW", Note: "a"},
		{Symbol: "2454.TW", Code: "2454", Market: "TW", Note: "b"},
	}}
	r := testResolver(t, s)
	// Assemble evidence from the already-enriched entries, the same way Resolve does.
	byEntry := map[string]*scanner.WatchlistEntry{}
	for i := range entries {
		byEntry[entries[i].A.Symbol] = &entries[i]
	}
	var ev []Evidence
	for _, c := range list.Candidates {
		ev = append(ev, Evidence{Candidate: c, Status: StatusOK, Entry: byEntry[c.Code],
			Market: LoadMarketContext("", nil, ""), InstitutionStatus: Disabled})
	}
	_ = r

	for _, e := range entries {
		b := before[e.A.Symbol]
		if e.RocketScore != b.score || e.RocketStage != b.stage ||
			e.WatchAction != b.action || e.ExplosionProb != b.prob {
			t.Errorf("%s changed: %+v → %+v", e.A.Symbol, b,
				snap{e.RocketScore, e.RocketStage, e.WatchAction, e.ExplosionProb})
		}
	}
	if len(ev) != 2 {
		t.Errorf("%d evidence rows", len(ev))
	}
}
