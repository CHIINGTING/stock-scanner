package recon

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// TestFutureBarsCannotChangeTheSerializedPlanAtT drives the real Reconstruct twice over one
// session — once on a cache that ends at T, once on a cache that continues with arbitrary
// future bars — and compares the FULL SERIALIZED PLAN of every symbol, field for field.
//
// It runs BOTH arms: regime-blind and REPLAYED_PIT against a seeded replay file, so the
// regime/policy path is exercised and not only the price path.
//
// # WHAT IT COVERS, AND WHAT IT DOES NOT — stated because the difference is large
//
// COVERS: that appending bars after T changes nothing in the serialized plan for T — the
// status, every reason code, the evidence list, the confidence census, the policy row, the
// decision trace, and the level-candidate dispositions on the zone trace. If any production
// path ever reads past the truncation point, this fails.
//
// DOES NOT COVER: the arithmetic of a plan that PUBLISHES PRICES. The scanner emits a
// resolvable entry semantic on about 0.84% of real stock-sessions (5,883 zone-publishing
// plans out of 702,526 in the EP-9 study), so a small synthetic fixture almost never produces
// one: sixty seeded six-symbol universes were tried against a BULL replay and produced ZERO
// plans with an IdealEntry. The plans compared here therefore carry no zone, no chase
// ceiling, no invalidation and no targets, and the guarantee this test provides for those
// fields is that they are CONSISTENTLY ABSENT, not that their arithmetic is point-in-time.
//
// That gap is deliberate rather than unnoticed, and the alternative was rejected: a series
// hand-engineered to force analyzeConsolidation into a pullback would be testing my ability
// to reverse-engineer that function, not a property of the reconstruction, and it would go
// stale the moment the consolidation rule moved. The point-in-time guarantee for the priced
// fields rests instead on the slice bound itself (TestTruncateAtIsAPointInTimeCut) and on the
// mutation audit's M14/M18, which break BOTH this test and that one.
//
// The test asserts its own coverage — plan count, and that no compared plan publishes an
// entry price — so a future fixture that does reach the priced path FAILS here rather than
// silently widening what this test is read to prove.
func TestFutureBarsCannotChangeTheSerializedPlanAtT(t *testing.T) {
	const bars = 150
	const signalIdx = 119 // leaves 30 future bars in the "with future" cache

	syms := []string{"2330", "2317", "2454", "3037", "2603"}
	atT := t.TempDir()
	withFuture := t.TempDir()
	for i, s := range syms {
		series := pitSeries(bars, int64(i))
		writeSeries(t, atT, s+"_TW.json", s, series[:signalIdx+1])
		writeSeries(t, withFuture, s+"_TW.json", s, series)
	}
	signal := series0(bars)[signalIdx].Date.Format(DateLayout)

	// A real, valid replay file for the signal session, so the PIT arm exercises the
	// regime → policy path rather than the unresolved-policy shortcut the blind arm takes.
	replayDir := t.TempDir()
	rr := model.NewRegimeReplay(signal, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rr.Regime, rr.RuleID = model.RegimeBull, "R1"
	rr.Structure.Structure, rr.Structure.Breadth = model.StructureUptrend, model.BreadthHealthy
	rr.Structure.Benchmark = model.Benchmark0050
	rr.InputExtent.BenchmarkSymbol = string(model.Benchmark0050)
	rr.InputExtent.BenchmarkBars = 150
	rr.InputExtent.BreadthSymbolsLoaded = 5
	rr.InputExtent.BenchmarkFirst = "2025-01-02"
	rr.InputExtent.BenchmarkLast = signal
	rr.InputExtent.BreadthFirst = "2025-01-02"
	rr.InputExtent.BreadthLast = signal
	rr.InputExtent.BreadthIndex = signalIdx
	rr.InputExtent.BreadthUniverse = 5
	if _, err := marketservice.SaveReplay(replayDir, rr); err != nil {
		t.Fatalf("save replay: %v", err)
	}

	// Anti-vacuity: the two caches really do differ.
	if len(mustRead(t, filepath.Join(withFuture, "2330_TW.json"))) <=
		len(mustRead(t, filepath.Join(atT, "2330_TW.json"))) {
		t.Fatal("the two caches are the same size; the future bars were not written")
	}

	for _, arm := range []Arm{ArmRegimeBlind, ArmReplayedPIT} {
		t.Run(string(arm), func(t *testing.T) {
			planA, priced := reconstructPlans(t, atT, replayDir, signal, arm)
			planB, _ := reconstructPlans(t, withFuture, replayDir, signal, arm)

			if len(planA) == 0 {
				t.Fatal("the reconstruction produced no plans; the test would be vacuous")
			}
			if len(planA) != len(planB) {
				t.Fatalf("appending future bars changed the number of plans at %s: %d -> %d",
					signal, len(planA), len(planB))
			}
			t.Logf("arm %s: compared %d serialized plans, %d of which publish an entry price",
				arm, len(planA), priced)
			// The scope disclosure above this test says the plans it compares publish no
			// zone, chase, stop or target, so for those fields the guarantee is CONSISTENT
			// ABSENCE, not point-in-time arithmetic. Assert it rather than only logging it:
			// a fixture that does reach the priced path makes that paragraph stale, and the
			// scope must be re-reviewed before the stronger claim is read into this test.
			if priced != 0 {
				t.Fatalf("arm %s: %d of %d plans publish an entry price, want 0 — this "+
					"fixture now reaches the priced path, so the DOES NOT COVER paragraph "+
					"above is stale and the test's scope must be re-reviewed", arm, priced, len(planA))
			}
			for sym, a := range planA {
				b, ok := planB[sym]
				if !ok {
					t.Fatalf("%s has a plan at %s only when the cache stops at T", sym, signal)
				}
				if a != b {
					t.Fatalf("%s (%s arm): the SERIALIZED PLAN for session %s changed when bars "+
						"AFTER %s were appended. Something in the production path read past "+
						"the truncation point.\n at T:        %s\n with future: %s",
						sym, arm, signal, signal, a, b)
				}
			}
		})
	}
}

// reconstructPlans runs the real Reconstructor over one session and returns symbol → the
// plan's full JSON, plus how many of those plans publish an entry price.
func reconstructPlans(t *testing.T, cacheDir, replayDir, session string, arm Arm) (map[string]string, int) {
	t.Helper()
	c, err := LoadCache(cacheDir, EligibilityBars)
	if err != nil {
		t.Fatalf("load %s: %v", cacheDir, err)
	}
	arch, err := OpenReplayArchive(replayDir)
	if err != nil {
		t.Fatalf("open replay: %v", err)
	}
	r := &Reconstructor{
		Scanner: scanner.New(scanner.Config{
			MinPrice: 1, MinAvgVolume: 1, EnableEntryPlan: true,
			EnableRSRank: true, RSLookbackDays: 120, RSMinHistoryDays: 100,
		}),
		Cache:   c,
		Sectors: &SectorPanel{Order: []string{"Tech"}, Members: map[string][]string{"Tech": {"2330", "2317", "2454"}}},
		Replay:  arch,
		MinBars: EligibilityBars,
	}
	res, err := r.Reconstruct(session, arm, FullFidelity)
	if err != nil {
		t.Fatalf("reconstruct %s (%s): %v", session, arm, err)
	}
	out := map[string]string{}
	var priced int
	for i := range res.Entries {
		p := res.Entries[i].EntryPlan
		if p == nil {
			continue
		}
		if p.IdealEntry != nil {
			priced++
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out[res.Entries[i].A.Symbol] = string(b)
	}
	return out, priced
}

// pitSeries builds a deterministic pseudo-random walk. Deterministic because a flat series
// would make every plan identical and the comparison meaningless; seeded because the test must
// produce the same fixture on every run and on every machine.
func pitSeries(n int, seed int64) []fetcher.Candle {
	rng := rand.New(rand.NewSource(seed + 1))
	out := make([]fetcher.Candle, 0, n)
	d := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	price := 100.0
	for i := 0; i < n; i++ {
		price *= 1 + (rng.Float64()-0.48)*0.04
		if price < 5 {
			price = 5
		}
		hi := price * (1 + rng.Float64()*0.02)
		lo := price * (1 - rng.Float64()*0.02)
		out = append(out, fetcher.Candle{
			Date: d.AddDate(0, 0, i), Open: (hi + lo) / 2, High: hi, Low: lo,
			Close: price, AdjClose: price, Volume: int64(1_000_000 + rng.Intn(500_000)),
		})
	}
	return out
}

func series0(n int) []fetcher.Candle { return pitSeries(n, 0) }

func writeSeries(t *testing.T, dir, name, symbol string, bars []fetcher.Candle) {
	t.Helper()
	sd := fetcher.StockData{Symbol: symbol, Market: "TW", Candles: bars}
	b, err := json.Marshal(map[string]any{"data": sd})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var _ = entryplan.RuleVersion
