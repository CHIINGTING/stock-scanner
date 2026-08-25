package research

import (
	"context"
	"fmt"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// fullyLoadedEntry has EVERY optional layer populated, which is the only configuration in
// which two projections can collide. The bug this file exists for — sector heat written by
// both the trend-extension path and the sector path — was invisible for weeks because the
// scans that exercised it ran with rotation disabled, leaving one of the two writers nil.
func fullyLoadedEntry(symbol, sector string) scanner.WatchlistEntry {
	e := bareEntry(symbol)
	e.Sector, e.HasSector = sector, true
	e.SectorFlowDir, e.SectorMidLabel = "INFLOW", "強"
	e.SectorStage = scanner.ConfirmedRotation

	e.Shadow = &scanner.ShadowSignals{
		RS:             &scanner.RSResult{Computed: true, RSRankPercentile: 88, RSReturnPct: 21},
		NewHigh:        &scanner.NewHighResult{Computed: true, H20Valid: true, H20: true, NewHighScore: 70},
		VCP:            &scanner.VCPResult{Computed: true, Valid: true, QualityScore: 81},
		Momentum:       &scanner.MomentumState{Computed: true, Flow: "BUILDING", Score: 66},
		MultiTimeframe: &scanner.MultiTimeframe{AlignmentScore: 74, AlignmentLabel: "FULL_BULL"},
	}
	e.Bias = &scanner.BiasView{Bias20: f64(4.1), Risk: "ELEVATED"}
	e.Institution = &institution.StockChipView{
		Code: symbol, AsOfDate: "2026-08-25",
		Foreign:      institution.LegStats{TodayNet: 1_000_000, ConsecutiveBuyDays: 3},
		Completeness: institution.ChipCompleteness{ExpectedDays: 20, ObservedDays: 20, DataComplete: true},
	}
	e.Candlestick = &candlestick.Result{
		Status:  candlestick.StatusOK,
		Signals: []candlestick.PatternSignal{{Type: "HAMMER", Direction: "BULLISH", Confidence: 0.7, Strength: 3}},
	}
	e.News = &news.StockNewsView{Computed: true,
		StockItems: []news.NewsSignal{{Signal: news.SignalBullish, Strength: 6, Confidence: 5}}}
	e.Technical = analyzeFixture(200, 100, 0.01)

	// The collision: TrendExt.Heat is the SAME pointer the rotation entry carries, because
	// attachTrendExtension copies it rather than recomputing.
	heat := &scanner.SectorHeatView{
		Status: scanner.SectorHeatOK, Heat: 72, Breadth: 83, Participation: 67,
		MemberCount: 6, ValidMemberCount: 6, Coverage: 100, MedianReturn20: 5.4,
	}
	e.TrendExt = &scanner.TrendExtensionView{
		State: scanner.TrendConfirmed, MA20Slope5D: f64(0.42), Sector: sector, Heat: heat,
	}
	e.A.PriceMove = scanner.MoveLargeUp
	e.A.PriceVolumeState = "LARGE_UP_VOLUME_EXPANSION"
	e.A.PriceChangePct = 4.2
	return e
}

// THE guard for this whole class of bug: no projection may emit the same (category, key)
// twice for one snapshot. The store enforces it with a UNIQUE constraint, but by then the
// failure is a runtime log line in the middle of a scan — this catches it at build time,
// with the layer that collided named in the failure.
func TestProjectionEmitsNoDuplicateKeys(t *testing.T) {
	heat := &scanner.SectorHeatView{
		Status: scanner.SectorHeatOK, Heat: 72, Breadth: 83,
		MemberCount: 6, ValidMemberCount: 6, Coverage: 100,
	}
	rot := &scanner.SectorRotation{Name: "半導體", Score: 78, OppScore: 81, Heat: heat}

	e := fullyLoadedEntry("2330", "半導體")
	e.TrendExt.Heat = heat // exactly what the scanner does: one object, two holders

	var c collector
	buildWatchlistEvidence(&c, e, rot, MarketContext{
		Regime: "BULL", Score: f64(61), Confidence: f64(0.8), AsOfDate: "2026-08-25",
	})

	seen := map[string]string{} // "category/key" → the source that wrote it first
	for _, item := range c.items {
		k := item.Category + "/" + item.Key
		if first, dup := seen[k]; dup {
			t.Errorf("%s written twice: first by %s, again by %s", k, first, item.Source)
			continue
		}
		seen[k] = item.Source
	}
	if len(c.items) == 0 {
		t.Fatal("the fixture produced no evidence at all")
	}
	// The collision case specifically: heat must be present exactly once, and it must come
	// from the sector layer rather than the technical one.
	if src, ok := seen[store.CategorySector+"/heat_status"]; !ok {
		t.Error("heat_status went missing entirely — the fix removed too much")
	} else if src != "scanner.SectorHeatView" {
		t.Errorf("heat_status written by %s, want the sector heat layer", src)
	}
}

// And end to end through the store, which is where the constraint actually lives: a fully
// loaded entry must persist with no error and no skipped rows.
func TestFullyLoadedEntryPersistsCleanly(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	heat := &scanner.SectorHeatView{
		Status: scanner.SectorHeatOK, Heat: 72, Breadth: 83,
		MemberCount: 6, ValidMemberCount: 6, Coverage: 100,
	}
	e := fullyLoadedEntry("2330", "半導體")
	e.TrendExt.Heat = heat

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-25"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
		Rotation:  []scanner.SectorRotation{{Name: "半導體", Score: 78, Heat: heat}},
		MarketCtx: MarketContext{Regime: "BULL"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Skipped != 0 {
		t.Errorf("%d snapshots were skipped", res.Skipped)
	}

	snapID := res.WatchlistSnapshots["2330"]
	rows, err := rec.Store().Evidence().BySnapshot(ctx, snapID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Every row the projection produced must have landed. A UNIQUE violation would have
	// aborted the batch transaction, leaving this far short.
	if res.Evidence != len(rows) {
		t.Errorf("reported %d evidence rows but stored %d — the batch was aborted mid-write",
			res.Evidence, len(rows))
	}

	var heatStatus int
	for _, r := range rows {
		if r.Key == "heat_status" {
			heatStatus++
		}
	}
	if heatStatus != 1 {
		t.Errorf("heat_status stored %d times, want exactly 1", heatStatus)
	}
}

// Several stocks in the same sector share ONE heat object. Each snapshot gets its own row —
// the uniqueness is per snapshot, not global — and none of them may collide with itself.
func TestSharedSectorHeatAcrossStocks(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	heat := &scanner.SectorHeatView{
		Status: scanner.SectorHeatOK, Heat: 72, MemberCount: 6, ValidMemberCount: 6,
	}
	var entries []scanner.WatchlistEntry
	for i := 0; i < 5; i++ {
		e := fullyLoadedEntry(fmt.Sprintf("233%d", i), "半導體")
		e.TrendExt.Heat = heat
		entries = append(entries, e)
	}

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-25"}, Input{
		Watchlist: entries,
		Rotation:  []scanner.SectorRotation{{Name: "半導體", Heat: heat}},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Snapshots != 5 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want 5 snapshots and no skips", res)
	}
	for sym, id := range res.WatchlistSnapshots {
		rows, err := rec.Store().Evidence().ByCategory(ctx, id, store.CategorySector)
		if err != nil {
			t.Fatalf("%s: %v", sym, err)
		}
		var n int
		for _, r := range rows {
			if r.Key == "heat_status" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: heat_status stored %d times", sym, n)
		}
	}
}
