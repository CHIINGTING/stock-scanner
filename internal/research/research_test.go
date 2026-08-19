package research

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

func f64(v float64) *float64 { return &v }

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Enabled: true,
		Store:   store.Config{Path: filepath.Join(t.TempDir(), "research", "r13.db")},
	}
}

func openRecorder(t *testing.T) (*Recorder, func() error) {
	t.Helper()
	rec, closeStore, err := Open(testConfig(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = closeStore() })
	return rec, closeStore
}

// bareEntry is a watchlist entry with EVERY optional layer off — the default configuration.
// Most assertions about absence are made against this.
func bareEntry(symbol string) scanner.WatchlistEntry {
	return scanner.WatchlistEntry{
		A: scanner.StockAnalysis{
			Symbol: symbol, Name: "測試", Source: "watchlist",
			Close: 100, Volume: 12000, AvgVolume20: 9000, VolumeRatio: 1.33,
			MA20: 96.5, MA20Trend: "↑↑", RSI: 58.2, ATR: 3.1,
			Score: 71, Action: scanner.ActionBuy, BFPPoints: 4,
			PriceVolumeSignal: "價漲量增",
		},
		RocketScore: 68, RocketStage: scanner.StageMainRun,
		WatchAction: scanner.ActWatchClose, ExplosionProb: "中",
		RiskLabel: "中",
	}
}

// evidenceMap reads a snapshot's evidence back as key → row for assertions.
func evidenceMap(t *testing.T, st *store.Store, snapshotID int64) map[string]store.Evidence {
	t.Helper()
	rows, err := st.Evidence().BySnapshot(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	out := make(map[string]store.Evidence, len(rows))
	for _, e := range rows {
		out[e.Key] = e
	}
	return out
}

// ── the off switch ───────────────────────────────────────────────────────────────────

// Default OFF has to mean OFF: no database file, no directory, no writes.
func TestDisabledOpensNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "research", "r13.db")

	rec, closeStore, err := Open(Config{Enabled: false, Store: store.Config{Path: path}})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if rec != nil {
		t.Error("a disabled recorder should be nil")
	}
	if err := closeStore(); err != nil {
		t.Errorf("close: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("a disabled recorder must not even create its directory")
	}

	// A nil recorder is a safe no-op, so callers need no branch.
	res, err := rec.RecordScan(context.Background(), RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
	})
	if err != nil || res.Snapshots != 0 {
		t.Errorf("nil recorder = %+v, %v; want a zero result and no error", res, err)
	}
	if rec.Store() != nil {
		t.Error("a nil recorder has no store")
	}
}

func TestConfigDefaulted(t *testing.T) {
	got := Config{}.Defaulted()
	if got.Store.Path != store.DefaultPath {
		t.Errorf("store path = %q, want %q", got.Store.Path, store.DefaultPath)
	}
	if got.MarketSnapshotDir != "data/market" {
		t.Errorf("market dir = %q, want data/market", got.MarketSnapshotDir)
	}
	if got.Enabled {
		t.Error("Defaulted must not turn the feature on")
	}
	set := Config{MarketSnapshotDir: "x"}.Defaulted()
	if set.MarketSnapshotDir != "x" {
		t.Error("Defaulted overwrote an explicit market dir")
	}
}

// ── the run ──────────────────────────────────────────────────────────────────────────

func TestRecordScanWritesRunSnapshotsAndDecision(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)
	st := rec.Store()

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12", ScannerVersion: "abc123"}, Input{
		Watchlist:    []scanner.WatchlistEntry{bareEntry("2330"), bareEntry("2317")},
		UniverseSize: 1712,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Snapshots != 2 || res.Skipped != 0 {
		t.Errorf("result = %+v, want 2 snapshots and no skips", res)
	}
	if res.Evidence == 0 {
		t.Error("a scanned stock should produce evidence")
	}

	run, err := st.Scans().RunByUID(ctx, res.RunUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.TradingDate != "2026-08-12" || run.ScannerVersion != "abc123" || run.UniverseSize != 1712 {
		t.Errorf("run metadata lost: %+v", run)
	}
	if run.FinishedAt == nil {
		t.Error("a completed scan must stamp the run as finished")
	}

	snaps, err := st.Scans().SnapshotsByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snaps))
	}
	for _, s := range snaps {
		if s.SourceTab != store.TabWatchlist {
			t.Errorf("%s tab = %s", s.Symbol, s.SourceTab)
		}
		if s.Close == nil || *s.Close != 100 {
			t.Errorf("%s close = %v, want the scanner's 100", s.Symbol, s.Close)
		}
		if s.ScannerAction != string(scanner.ActionBuy) {
			t.Errorf("%s action = %q, want BUY verbatim", s.Symbol, s.ScannerAction)
		}
		if s.RocketScore == nil || *s.RocketScore != 68 {
			t.Errorf("%s rocket score = %v", s.Symbol, s.RocketScore)
		}

		// The scanner's verdict is mirrored as a decision, so accuracy work has one place
		// to read it from — recorded even though no AI ran.
		decisions, err := st.Analyses().DecisionsBySnapshot(ctx, s.ID)
		if err != nil {
			t.Fatalf("decisions: %v", err)
		}
		if len(decisions) != 1 {
			t.Fatalf("%s has %d decisions, want the scanner's alone", s.Symbol, len(decisions))
		}
		if decisions[0].Source != store.DecisionSourceScanner || decisions[0].Decision != "BUY" {
			t.Errorf("%s decision = %s/%s", s.Symbol, decisions[0].Source, decisions[0].Decision)
		}
		if decisions[0].AnalysisRunID != nil {
			t.Error("a scanner decision must not be tied to an analysis run")
		}
	}
}

// Every agent in a run must see ONE snapshot per stock. The returned map is that handle.
func TestWatchlistSnapshotIDsAreTheSharedHandle(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330"), bareEntry("2317")},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(res.WatchlistSnapshots) != 2 {
		t.Fatalf("snapshot map = %v, want one id per symbol", res.WatchlistSnapshots)
	}
	id, ok := res.WatchlistSnapshots["2330"]
	if !ok || id == 0 {
		t.Fatalf("2330 has no snapshot id: %v", res.WatchlistSnapshots)
	}
	if res.WatchlistSnapshots["2317"] == id {
		t.Error("two stocks must not share one snapshot id")
	}
	// And that id resolves to the right stock.
	snap, err := rec.Store().Scans().Snapshot(ctx, id)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Symbol != "2330" {
		t.Errorf("id %d resolved to %s", id, snap.Symbol)
	}
}

// The same stock legitimately appears in more than one tab of a run and keeps a separate
// snapshot for each, because the scanner's verdict differs between them.
func TestSameSymbolAcrossTabs(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	market := bareEntry("2330").A
	market.Action = scanner.ActionWatch
	portfolio := bareEntry("2330").A
	portfolio.Action = scanner.ActionHold

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
		Market:    []scanner.StockAnalysis{market},
		Portfolio: []scanner.StockAnalysis{portfolio},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if res.Snapshots != 3 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want 3 snapshots across the three tabs", res)
	}

	snaps, err := rec.Store().Scans().SnapshotsByRun(ctx, res.RunID)
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	byTab := map[string]string{}
	for _, s := range snaps {
		byTab[s.SourceTab] = s.ScannerAction
	}
	want := map[string]string{
		store.TabWatchlist: "BUY", store.TabMarket: "WATCH", store.TabPositions: "HOLD",
	}
	for tab, action := range want {
		if byTab[tab] != action {
			t.Errorf("%s tab action = %q, want %q", tab, byTab[tab], action)
		}
	}
}

// One unwritable stock must cost that stock, not the whole scan record.
func TestPerStockFailureIsSkippedNotFatal(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	// A duplicate (run, symbol, tab) is rejected by the store — the second one is skipped.
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330"), bareEntry("2330"), bareEntry("2317")},
	})
	if err != nil {
		t.Fatalf("a per-stock failure must not fail the run: %v", err)
	}
	if res.Snapshots != 2 || res.Skipped != 1 {
		t.Errorf("result = %+v, want 2 written and 1 skipped", res)
	}
	run, err := rec.Store().Scans().RunByUID(ctx, res.RunUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.FinishedAt == nil {
		t.Error("the run should still be stamped complete")
	}
}

// A stock with no usable price keeps a NIL basis, so no outcome can ever be measured
// against a fabricated zero.
func TestZeroCloseStaysNil(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("0000")
	e.A.Close = 0
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	snap, err := rec.Store().Scans().Snapshot(ctx, res.WatchlistSnapshots["0000"])
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Close != nil {
		t.Errorf("close = %v, want nil", *snap.Close)
	}
	ev := evidenceMap(t, rec.Store(), snap.ID)
	if _, ok := ev["close"]; ok {
		t.Error("a zero close must leave no evidence row either")
	}
}

// ── MISSING ≠ NEUTRAL ────────────────────────────────────────────────────────────────

// With every optional layer off, the store must contain NO row for any of them. A zero
// would tell an agent "BIAS is 0%" when the truth is "BIAS was never computed".
func TestDisabledLayersLeaveNoRows(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	for _, key := range []string{
		"bias5", "bias10", "bias20", "bias60", "bias_risk", // R10-1 off
		"ma20_slope_5d", "ma60_slope_10d", "bias20_percentile", "trend_ext_state", // R12 off
		"heat", "heat_status", "heat_breadth", // R12 sector heat off
		"candlestick_status", "candlestick_pattern", // R10-2 off
		"foreign_today_net", "data_complete", // institution off
		"top_signal", "computed", // news off
		"regime", "score", // market dashboard never ran
		"rs_rank_percentile", "vcp_valid", "momentum_flow", "mtf_alignment", // shadow off
	} {
		if e, ok := ev[key]; ok {
			t.Errorf("%q was written as %v with its layer disabled — missing must not become a value",
				key, valueOf(e))
		}
	}

	// The base technical facts the scanner always produces must still be there.
	for _, key := range []string{"close", "ma20", "rsi", "technical_score", "rocket_stage"} {
		if _, ok := ev[key]; !ok {
			t.Errorf("%q is missing — the base scan always produces it", key)
		}
	}
}

// The one rule that cannot be relaxed: this layer records, it never derives. MA60 and MA120
// do not exist per stock anywhere in this repo, so they must not appear here — not even when
// the R12 layer supplies MA60 SLOPES, which is a different measurement.
func TestNoSecondIndicatorSetIsInvented(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.TrendExt = &scanner.TrendExtensionView{
		State:        scanner.TrendConfirmed,
		MA20Slope5D:  f64(0.42),
		MA60Slope10D: f64(0.18),
	}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	for _, forbidden := range []string{"ma60", "ma120", "ma5", "ma10", "ma200"} {
		if _, ok := ev[forbidden]; ok {
			t.Errorf("%q was written — this layer must never derive an indicator the scanner lacks", forbidden)
		}
	}
	// The slopes, which DO exist, are recorded.
	if got := ev["ma20_slope_5d"]; got.NumValue == nil || *got.NumValue != 0.42 {
		t.Errorf("ma20_slope_5d = %v, want 0.42", got.NumValue)
	}
	if ev["ma20_slope_5d"].Unit != "%/day" {
		t.Errorf("slope unit = %q, want %%/day", ev["ma20_slope_5d"].Unit)
	}
	// A percentile the R12 layer could not compute stays absent.
	if _, ok := ev["bias20_percentile"]; ok {
		t.Error("a nil percentile must leave no row")
	}
}

// ── per-layer projection ─────────────────────────────────────────────────────────────

func TestBiasProjectionKeepsNilWindowsAbsent(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Bias = &scanner.BiasView{Bias5: f64(2.5), Bias20: f64(-3.25), Risk: "ELEVATED"}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	if got := ev["bias20"]; got.NumValue == nil || *got.NumValue != -3.25 {
		t.Errorf("bias20 = %v, want -3.25 (a negative reading must survive)", got.NumValue)
	}
	for _, absent := range []string{"bias10", "bias60"} {
		if _, ok := ev[absent]; ok {
			t.Errorf("%q had insufficient history and must leave no row", absent)
		}
	}
	// BIAS risk is chase/oversold RISK, so it belongs to the risk agent, not the technical one.
	if ev["bias_risk"].Category != store.CategoryRisk {
		t.Errorf("bias_risk category = %q, want %q", ev["bias_risk"].Category, store.CategoryRisk)
	}
}

func TestSectorAndHeatProjection(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Sector, e.HasSector = "半導體", true
	e.SectorFlowDir, e.SectorMidLabel = "INFLOW", "強"

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
		Rotation: []scanner.SectorRotation{{
			Name: "半導體", Score: 78.5, OppScore: 81, AvgReturn20: 6.2, AboveMA60Ratio: 83.3,
			Heat: &scanner.SectorHeatView{
				Status: scanner.SectorHeatOK, Heat: 72, Breadth: 83.3, Participation: 66.7,
				MemberCount: 6, ValidMemberCount: 6, Coverage: 100, MedianReturn20: 5.4,
			},
		}},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	if ev["sector"].TextValue == nil || *ev["sector"].TextValue != "半導體" {
		t.Errorf("sector = %v", ev["sector"].TextValue)
	}
	if got := ev["sector_score"]; got.NumValue == nil || *got.NumValue != 78.5 {
		t.Errorf("sector_score = %v, want 78.5", got.NumValue)
	}
	if got := ev["heat"]; got.NumValue == nil || *got.NumValue != 72 {
		t.Errorf("heat = %v, want 72", got.NumValue)
	}
	// Sector material must land in the sector category so the sector agent gets it and the
	// technical agent does not.
	for _, key := range []string{"sector_score", "heat", "heat_breadth", "sector_above_ma60_ratio"} {
		if ev[key].Category != store.CategorySector {
			t.Errorf("%s category = %q, want %q", key, ev[key].Category, store.CategorySector)
		}
	}
}

// A sector below the member floor publishes its STATUS and its denominators but no score —
// mirroring ComputeSectorHeat, which refuses to produce a precise-looking number from a
// sample too small to support one.
func TestInsufficientHeatPublishesDenominatorsNotAScore(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("1504")
	e.Sector, e.HasSector = "電機機械", true
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
		Rotation: []scanner.SectorRotation{{
			Name: "電機機械",
			Heat: &scanner.SectorHeatView{
				Status: scanner.SectorHeatInsufficientData, MemberCount: 2, ValidMemberCount: 2,
			},
		}},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["1504"])

	if ev["heat_status"].TextValue == nil || *ev["heat_status"].TextValue != scanner.SectorHeatInsufficientData {
		t.Errorf("heat_status = %v, want INSUFFICIENT_DATA", ev["heat_status"].TextValue)
	}
	if got := ev["heat_valid_member_count"]; got.NumValue == nil || *got.NumValue != 2 {
		t.Errorf("valid member count = %v, want 2 — the denominator must be disclosed", got.NumValue)
	}
	for _, absent := range []string{"heat", "heat_breadth", "heat_participation", "heat_coverage"} {
		if _, ok := ev[absent]; ok {
			t.Errorf("%q was published below the member floor", absent)
		}
	}
}

func TestInstitutionProjection(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Institution = &institution.StockChipView{
		Code: "2330", AsOfDate: "2026-08-11",
		Foreign: institution.LegStats{
			TodayNet: 1_200_000, Sum5d: 4_800_000, ConsecutiveBuyDays: 4,
			Transition: "SELL_TO_BUY", NetBuyRatio: f64(3.4), StreakComplete: true,
		},
		// The trust leg had no same-day volume, so its ratio is unavailable.
		Trust:        institution.LegStats{TodayNet: -50_000, ConsecutiveSellDays: 2},
		Completeness: institution.ChipCompleteness{ExpectedDays: 20, ObservedDays: 20, DataComplete: true},
	}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	if got := ev["foreign_today_net"]; got.NumValue == nil || *got.NumValue != 1_200_000 {
		t.Errorf("foreign today net = %v", got.NumValue)
	}
	if got := ev["foreign_consecutive_buy_days"]; got.NumValue == nil || *got.NumValue != 4 {
		t.Errorf("foreign streak = %v, want 4", got.NumValue)
	}
	if got := ev["foreign_net_buy_ratio"]; got.NumValue == nil || *got.NumValue != 3.4 {
		t.Errorf("foreign net buy ratio = %v", got.NumValue)
	}
	// Unavailable ratio → no row, not 0%.
	if _, ok := ev["trust_net_buy_ratio"]; ok {
		t.Error("an unavailable net-buy ratio must leave no row")
	}
	// Zero IS a reading for a streak count, and a leg with no activity is still a fact.
	if got := ev["dealer_today_net"]; got.NumValue == nil || *got.NumValue != 0 {
		t.Errorf("dealer today net = %v, want a real 0", got.NumValue)
	}
	if ev["foreign_today_net"].Category != store.CategoryInstitution {
		t.Errorf("institution material landed in %q", ev["foreign_today_net"].Category)
	}
}

func TestCandlestickProjectionKeepsOnlyTheTopSignal(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Candlestick = &candlestick.Result{
		Status: candlestick.StatusOK,
		Signals: []candlestick.PatternSignal{
			{Type: "BULLISH_ENGULFING", Direction: "BULLISH", Confidence: 0.86, Strength: 4, ContextMatched: true},
			{Type: "HAMMER", Direction: "BULLISH", Confidence: 0.51, Strength: 2},
		},
	}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	if ev["candlestick_pattern"].TextValue == nil || *ev["candlestick_pattern"].TextValue != "BULLISH_ENGULFING" {
		t.Errorf("pattern = %v, want the analyzer's top signal", ev["candlestick_pattern"].TextValue)
	}
	if got := ev["candlestick_confidence"]; got.NumValue == nil || *got.NumValue != 0.86 {
		t.Errorf("confidence = %v, want 0.86", got.NumValue)
	}
}

// NO_PATTERN is a real observation and must be recorded as such — distinct from the layer
// having been off, which produces no status row at all.
func TestCandlestickNoPatternIsRecorded(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Candlestick = &candlestick.Result{Status: candlestick.StatusNoPattern}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])
	if ev["candlestick_status"].TextValue == nil || *ev["candlestick_status"].TextValue != candlestick.StatusNoPattern {
		t.Errorf("status = %v, want NO_PATTERN recorded", ev["candlestick_status"].TextValue)
	}
	if _, ok := ev["candlestick_pattern"]; ok {
		t.Error("no pattern means no pattern row")
	}
}

func TestNewsProjectionPicksTheStrongestSignal(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	e := bareEntry("2330")
	e.News = &news.StockNewsView{
		Computed: true,
		StockItems: []news.NewsSignal{
			{Signal: news.SignalNeutral, Strength: 3, Confidence: 9, PublishedAt: base},
			{Signal: news.SignalBullish, Strength: 8, Confidence: 6, PublishedAt: base.AddDate(0, 0, 1)},
			{Signal: news.SignalRiskWarning, Strength: 8, Confidence: 4, PublishedAt: base},
		},
		SectorItems: []news.NewsSignal{{Signal: news.SignalRotationIn, Strength: 5}},
		Divergence:  true,
	}
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	// Highest Strength, tie-broken by Confidence → the BULLISH 8/6, not the RISK_WARNING 8/4.
	if ev["top_signal"].TextValue == nil || *ev["top_signal"].TextValue != string(news.SignalBullish) {
		t.Errorf("top signal = %v, want BULLISH (strength tie broken by confidence)", ev["top_signal"].TextValue)
	}
	if got := ev["stock_item_count"]; got.NumValue == nil || *got.NumValue != 3 {
		t.Errorf("stock item count = %v, want 3", got.NumValue)
	}
	if ev["divergence"].TextValue == nil || *ev["divergence"].TextValue != "TRUE" {
		t.Errorf("divergence = %v", ev["divergence"].TextValue)
	}
	if ev["top_signal"].Category != store.CategoryNews {
		t.Errorf("news material landed in %q", ev["top_signal"].Category)
	}
}

func TestStrongestSignalIsDeterministic(t *testing.T) {
	base := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	items := []news.NewsSignal{
		{Signal: "A", Strength: 5, Confidence: 5, PublishedAt: base.AddDate(0, 0, 2)},
		{Signal: "B", Strength: 5, Confidence: 5, PublishedAt: base},
		{Signal: "C", Strength: 5, Confidence: 5, PublishedAt: base.AddDate(0, 0, 1)},
	}
	got, ok := strongestSignal(items)
	if !ok || got.Signal != "B" {
		t.Errorf("strongest = %v (ok=%v), want the earliest of the tied signals", got.Signal, ok)
	}
	if _, ok := strongestSignal(nil); ok {
		t.Error("an empty list has no strongest signal")
	}
}

func TestMarketContextProjection(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
		MarketCtx: MarketContext{
			Regime: "BULL_PULLBACK", Score: f64(61.5), Confidence: f64(0.82), AsOfDate: "2026-08-12",
		},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	ev := evidenceMap(t, rec.Store(), res.WatchlistSnapshots["2330"])

	if ev["regime"].TextValue == nil || *ev["regime"].TextValue != "BULL_PULLBACK" {
		t.Errorf("regime = %v", ev["regime"].TextValue)
	}
	if ev["regime"].Category != store.CategoryMarket {
		t.Errorf("regime category = %q", ev["regime"].Category)
	}
	// The regime is also on the run itself, so R13-M7 can slice by it without a join.
	run, err := rec.Store().Scans().RunByUID(ctx, res.RunUID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.MarketRegime != "BULL_PULLBACK" || run.MarketScore == nil || *run.MarketScore != 61.5 {
		t.Errorf("run market read = %q / %v", run.MarketRegime, run.MarketScore)
	}
}

// ── domain isolation ─────────────────────────────────────────────────────────────────

// Each specialist agent is handed only its own category. This asserts the projection puts
// material in the right lane, which is what makes that scoping real.
func TestEvidenceLandsInTheRightDomain(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	e := bareEntry("2330")
	e.Sector, e.HasSector = "半導體", true
	e.Bias = &scanner.BiasView{Bias20: f64(4.1), Risk: "ELEVATED"}
	e.Institution = &institution.StockChipView{Foreign: institution.LegStats{TodayNet: 100}}
	e.News = &news.StockNewsView{Computed: true}
	e.Shadow = &scanner.ShadowSignals{RS: &scanner.RSResult{Computed: true, RSRankPercentile: 88}}

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, Input{
		Watchlist: []scanner.WatchlistEntry{e},
		Rotation:  []scanner.SectorRotation{{Name: "半導體", Score: 78}},
		MarketCtx: MarketContext{Regime: "BULL"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	st := rec.Store()
	snapID := res.WatchlistSnapshots["2330"]

	for _, tc := range []struct {
		category string
		mustHave []string
		mustNot  []string
	}{
		{store.CategoryTechnical, []string{"ma20", "rsi", "bias20"}, []string{"regime", "foreign_today_net", "sector_score"}},
		{store.CategoryInstitution, []string{"foreign_today_net"}, []string{"ma20", "regime"}},
		{store.CategorySector, []string{"sector", "sector_score", "rs_rank_percentile"}, []string{"ma20", "foreign_today_net"}},
		{store.CategoryNews, []string{"computed"}, []string{"ma20", "sector_score"}},
		{store.CategoryMarket, []string{"regime"}, []string{"ma20", "sector_score"}},
		{store.CategoryRisk, []string{"risk_label", "bias_risk"}, []string{"ma20", "regime"}},
	} {
		rows, err := st.Evidence().ByCategory(ctx, snapID, tc.category)
		if err != nil {
			t.Fatalf("%s: %v", tc.category, err)
		}
		got := map[string]bool{}
		for _, r := range rows {
			if r.Category != tc.category {
				t.Errorf("%s slice leaked a %s row", tc.category, r.Category)
			}
			got[r.Key] = true
		}
		for _, k := range tc.mustHave {
			if !got[k] {
				t.Errorf("%s is missing %q", tc.category, k)
			}
		}
		for _, k := range tc.mustNot {
			if got[k] {
				t.Errorf("%s must not contain %q", tc.category, k)
			}
		}
	}

	// RS is the sector agent's material, deliberately not the technical agent's.
	sector, _ := st.Evidence().ByCategory(ctx, snapID, store.CategorySector)
	var foundRS bool
	for _, r := range sector {
		if r.Key == "rs_rank_percentile" && r.NumValue != nil && *r.NumValue == 88 {
			foundRS = true
		}
	}
	if !foundRS {
		t.Error("relative strength should reach the sector agent")
	}
}

// ── misc ─────────────────────────────────────────────────────────────────────────────

func TestRecordScanValidatesTradingDate(t *testing.T) {
	rec, _ := openRecorder(t)
	if _, err := rec.RecordScan(context.Background(), RunMeta{}, Input{}); err == nil {
		t.Error("a scan without a trading date should be rejected")
	}
}

// Two scans of the same date are separate runs, not a replacement: which one was in force is
// a question the data should be able to answer.
func TestRerunCreatesASecondRun(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	in := Input{Watchlist: []scanner.WatchlistEntry{bareEntry("2330")}}
	first, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-12"}, in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.RunID == second.RunID || first.RunUID == second.RunUID {
		t.Fatal("a rerun must be its own run")
	}
	runs, err := rec.Store().Scans().RunsByDate(ctx, "2026-08-12")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("got %d runs for the date, want 2", len(runs))
	}
}

func TestBuildVersionDoesNotGuess(t *testing.T) {
	// Whatever the test binary was stamped with, the result must be either empty (unknown
	// build recorded as unknown) or a short revision — never a fabricated placeholder.
	v := BuildVersion()
	if len(v) > 20 {
		t.Errorf("BuildVersion() = %q, want a short revision or empty", v)
	}
}

// valueOf renders an evidence row for failure messages.
func valueOf(e store.Evidence) any {
	if e.NumValue != nil {
		return *e.NumValue
	}
	if e.TextValue != nil {
		return *e.TextValue
	}
	return nil
}
