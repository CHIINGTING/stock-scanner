package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openTest gives each test its own file-backed database. A file (not :memory:) is used on
// purpose: the DSN pragmas, WAL mode and the directory-creation path are all part of what
// this package promises, and an in-memory database would skip every one of them.
func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Config{Path: filepath.Join(t.TempDir(), "nested", "r13.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedSnapshot creates a run plus one snapshot, the anchor most tests need.
func seedSnapshot(t *testing.T, s *Store) (ScanRun, StockSnapshot) {
	t.Helper()
	ctx := context.Background()
	run, err := s.Scans().StartRun(ctx, ScanRun{
		RunUID:       "run-1",
		TradingDate:  "2026-08-11",
		MarketRegime: "BULL",
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	price := 123.5
	snap, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID:     run.ID,
		Symbol:        "2330",
		Name:          "台積電",
		TradingDate:   run.TradingDate,
		SourceTab:     TabWatchlist,
		Close:         &price,
		ScannerAction: "BUY",
	})
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	return run, snap
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "r13.db")

	s, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("schema version = %d, want %d", v, SchemaVersion)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Re-opening an already-migrated database must be a no-op, not a second application.
	s2, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()
	if v2, err := s2.SchemaVersion(ctx); err != nil || v2 != SchemaVersion {
		t.Errorf("reopen schema version = %d (%v), want %d", v2, err, SchemaVersion)
	}
}

// A database migrated past what this binary knows must be refused. An old build writing
// into a newer schema is how an audit store loses columns without anyone noticing.
func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "r13.db")

	s, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		SchemaVersion+5, "from_the_future", formatTime(nowUTC())); err != nil {
		t.Fatalf("seed future migration: %v", err)
	}
	_ = s.Close()

	if _, err := Open(Config{Path: path}); err == nil {
		t.Fatal("opening a newer-schema database should fail")
	}
}

// The ON DELETE CASCADE relationships are load-bearing, and SQLite ignores them unless
// foreign_keys is on for the connection. This asserts the DSN pragma actually took.
func TestForeignKeysAreEnforcedAndCascade(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	run, snap := seedSnapshot(t, s)

	if _, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{
		Num(CategoryTechnical, "ma20", 120.4, "TWD", "scanner"),
	}); err != nil {
		t.Fatalf("save evidence: %v", err)
	}
	ar := startRun(t, s, snap.ID, "ar-1", "r13-v1")
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: ar.ID, Agent: "technical", Status: "OK", Verdict: "BULLISH",
	}); err != nil {
		t.Fatalf("save analysis: %v", err)
	}

	// A snapshot pointing at a non-existent run must be rejected outright.
	if _, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: 99999, Symbol: "9999", TradingDate: "2026-08-11", SourceTab: TabMarket,
	}); err == nil {
		t.Error("snapshot with a dangling scan_run_id should be rejected")
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM scan_runs WHERE id = ?`, run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM stock_snapshots`,
		`SELECT COUNT(*) FROM evidence`,
		`SELECT COUNT(*) FROM analysis_runs`,
		`SELECT COUNT(*) FROM agent_analysis`,
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s = %d after deleting the run, want 0 (cascade did not fire)", q, n)
		}
	}
}

func TestWALAndPragmas(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	var journal string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestConfigDefaulted(t *testing.T) {
	got := Config{}.Defaulted()
	if got.Path != DefaultPath {
		t.Errorf("Path = %q, want %q", got.Path, DefaultPath)
	}
	if got.BusyTimeoutMs != 5000 {
		t.Errorf("BusyTimeoutMs = %d, want 5000", got.BusyTimeoutMs)
	}
	// An explicit value must survive defaulting.
	set := Config{Path: "x.db", BusyTimeoutMs: 250}.Defaulted()
	if set.Path != "x.db" || set.BusyTimeoutMs != 250 {
		t.Errorf("Defaulted overwrote explicit config: %+v", set)
	}
}

// ── scan runs ────────────────────────────────────────────────────────────────────────

func TestScanRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	started := time.Date(2026, 8, 11, 2, 30, 0, 0, time.UTC)

	score := 61.5
	run, err := s.Scans().StartRun(ctx, ScanRun{
		RunUID: "run-a", TradingDate: "2026-08-11", StartedAt: started,
		ScannerVersion: "abc1234", MarketRegime: "BULL_PULLBACK", MarketScore: &score,
		UniverseSize: 1712,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.ID == 0 {
		t.Error("StartRun must return the assigned id")
	}
	if run.FinishedAt != nil {
		t.Error("a run in flight must have no finished_at")
	}

	got, err := s.Scans().RunByUID(ctx, "run-a")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt round-trip = %v, want %v", got.StartedAt, started)
	}
	if got.MarketScore == nil || *got.MarketScore != score {
		t.Errorf("MarketScore = %v, want %v", got.MarketScore, score)
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt = %v, want nil while in flight", got.FinishedAt)
	}

	if err := s.Scans().FinishRun(ctx, run.ID, started.Add(90*time.Second)); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// A run finishes once. A second stamp would silently rewrite the duration.
	if err := s.Scans().FinishRun(ctx, run.ID, started.Add(200*time.Second)); err == nil {
		t.Error("finishing an already-finished run should fail")
	}

	done, err := s.Scans().RunByUID(ctx, "run-a")
	if err != nil {
		t.Fatalf("lookup after finish: %v", err)
	}
	if done.FinishedAt == nil || !done.FinishedAt.Equal(started.Add(90*time.Second)) {
		t.Errorf("FinishedAt = %v, want the first stamp", done.FinishedAt)
	}
}

func TestRunByUIDNotFound(t *testing.T) {
	s := openTest(t)
	_, err := s.Scans().RunByUID(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStartRunValidates(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, err := s.Scans().StartRun(ctx, ScanRun{TradingDate: "2026-08-11"}); err == nil {
		t.Error("a run without run_uid should be rejected")
	}
	if _, err := s.Scans().StartRun(ctx, ScanRun{RunUID: "x"}); err == nil {
		t.Error("a run without trading_date should be rejected")
	}
}

// The same symbol legitimately appears in more than one tab of a run, with different
// scanner decisions. Re-inserting the SAME tab is a caller bug and must fail loudly.
func TestSnapshotUniquenessIsPerTab(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	run, _ := seedSnapshot(t, s)

	if _, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: run.ID, Symbol: "2330", TradingDate: run.TradingDate,
		SourceTab: TabMarket, ScannerAction: "WATCH",
	}); err != nil {
		t.Fatalf("same symbol in another tab should be allowed: %v", err)
	}
	if _, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: run.ID, Symbol: "2330", TradingDate: run.TradingDate,
		SourceTab: TabWatchlist, ScannerAction: "BUY",
	}); err == nil {
		t.Error("re-inserting the same (run, symbol, tab) should fail")
	}

	got, err := s.Scans().SnapshotsByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(got))
	}
}

func TestSnapshotRoundTripKeepsNilDistinctFromZero(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	run, _ := seedSnapshot(t, s)

	// A stock with no usable price: Close must come back nil, never 0.
	saved, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: run.ID, Symbol: "0000", TradingDate: run.TradingDate, SourceTab: TabMarket,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Scans().Snapshot(ctx, saved.ID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Close != nil {
		t.Errorf("Close = %v, want nil (missing is not zero)", *got.Close)
	}
	if got.ScannerScore != nil || got.RocketScore != nil {
		t.Errorf("unset scores must stay nil, got %v / %v", got.ScannerScore, got.RocketScore)
	}
}

// ── evidence ─────────────────────────────────────────────────────────────────────────

func TestEvidenceDropsAbsentValues(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	n, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{
		Num(CategoryTechnical, "ma20_slope", 0.42, "%/day", "indicator.MASlopePerDay"),
		Text(CategoryTechnical, "stage", "MAIN_RUN", "scanner.RocketStage"),
		// The scanner could not compute this one. It must leave NO row — an agent that
		// reads the store has to see absence, not a confident zero.
		{Category: CategoryTechnical, Key: "bias_percentile", Source: "scanner.Bias"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d rows, want 2 (the valueless one must be dropped)", n)
	}

	got, err := s.Evidence().BySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d rows, want 2", len(got))
	}
	for _, e := range got {
		if e.Key == "bias_percentile" {
			t.Error("an unavailable fact must not be stored at all")
		}
	}
}

func TestEvidenceValueRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	if _, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{
		Num(CategoryTechnical, "bias20", -3.25, "%", "scanner.BiasView"),
		Text(CategoryMarket, "regime", "DISTRIBUTION", "market.model.Regime"),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Evidence().BySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	byKey := map[string]Evidence{}
	for _, e := range got {
		byKey[e.Key] = e
	}

	bias := byKey["bias20"]
	if bias.NumValue == nil || *bias.NumValue != -3.25 {
		t.Errorf("bias20 num = %v, want -3.25", bias.NumValue)
	}
	if bias.TextValue != nil {
		t.Errorf("a numeric fact must not also carry text, got %q", *bias.TextValue)
	}
	if bias.Unit != "%" {
		t.Errorf("unit = %q, want %%", bias.Unit)
	}

	regime := byKey["regime"]
	if regime.TextValue == nil || *regime.TextValue != "DISTRIBUTION" {
		t.Errorf("regime text = %v, want DISTRIBUTION", regime.TextValue)
	}
	if regime.NumValue != nil {
		t.Errorf("a labelled fact must not also carry a number, got %v", *regime.NumValue)
	}
}

// Each specialist agent is handed only its own domain. This is what makes "the technical
// agent does not analyse news" a property of the data flow rather than a prompt request.
func TestEvidenceByCategoryIsolatesDomains(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	if _, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{
		Num(CategoryTechnical, "ma20", 100, "TWD", "scanner"),
		Num(CategoryTechnical, "ma60", 95, "TWD", "scanner"),
		Text(CategoryNews, "sentiment", "POSITIVE", "news"),
		Num(CategoryInstitution, "foreign_streak", 4, "days", "institution"),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	tech, err := s.Evidence().ByCategory(ctx, snap.ID, CategoryTechnical)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(tech) != 2 {
		t.Fatalf("technical rows = %d, want 2", len(tech))
	}
	for _, e := range tech {
		if e.Category != CategoryTechnical {
			t.Errorf("leaked %s evidence into the technical slice", e.Category)
		}
	}
}

func TestEvidenceRejectsDuplicateKey(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	e := Num(CategoryTechnical, "ma20", 100, "TWD", "scanner")
	if _, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{e}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := s.Evidence().SaveMany(ctx, snap.ID, []Evidence{e}); err == nil {
		t.Error("re-stating the same fact for the same snapshot should fail")
	}
}

// ── analysis runs ────────────────────────────────────────────────────────────────────

// startRun opens a live analysis run over a snapshot, the setup most agent tests need.
func startRun(t *testing.T, s *Store, snapshotID int64, uid, promptSet string) AnalysisRun {
	t.Helper()
	run, err := s.Analyses().StartAnalysisRun(context.Background(), AnalysisRun{
		StockSnapshotID:     snapshotID,
		RunUID:              uid,
		Mode:                ModeLive,
		OrchestratorVersion: "r13-m3",
		PromptSetVersion:    promptSet,
	})
	if err != nil {
		t.Fatalf("start analysis run %s: %v", uid, err)
	}
	return run
}

func TestAnalysisRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")
	if run.ID == 0 {
		t.Error("StartAnalysisRun must return the assigned id")
	}
	if run.Status != RunStatusRunning || run.FinishedAt != nil {
		t.Errorf("a fresh run should be RUNNING and unfinished, got %s / %v", run.Status, run.FinishedAt)
	}

	if err := s.Analyses().FinishAnalysisRun(ctx, run.ID, RunStatusComplete, time.Time{}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// A run finishes once; a second stamp would rewrite the execution's own history.
	if err := s.Analyses().FinishAnalysisRun(ctx, run.ID, RunStatusFailed, time.Time{}); err == nil {
		t.Error("finishing an already-finished analysis run should fail")
	}
	// RUNNING is not a terminal status.
	if err := s.Analyses().FinishAnalysisRun(ctx, run.ID, RunStatusRunning, time.Time{}); err == nil {
		t.Error("RUNNING should be rejected as a terminal status")
	}

	got, err := s.Analyses().AnalysisRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Status != RunStatusComplete || got.FinishedAt == nil {
		t.Errorf("run = %s / %v, want COMPLETE and finished", got.Status, got.FinishedAt)
	}
	if got.OrchestratorVersion != "r13-m3" || got.PromptSetVersion != "r13-v1" {
		t.Errorf("version audit lost: orchestrator=%q prompt_set=%q",
			got.OrchestratorVersion, got.PromptSetVersion)
	}
}

func TestAnalysisRunValidates(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	runs := s.Analyses()

	if _, err := runs.StartAnalysisRun(ctx, AnalysisRun{RunUID: "x", Mode: ModeLive}); err == nil {
		t.Error("a run without a snapshot id should be rejected")
	}
	if _, err := runs.StartAnalysisRun(ctx, AnalysisRun{StockSnapshotID: snap.ID, Mode: ModeLive}); err == nil {
		t.Error("a run without a run_uid should be rejected")
	}
	if _, err := runs.StartAnalysisRun(ctx, AnalysisRun{
		StockSnapshotID: snap.ID, RunUID: "x", Mode: "WHATEVER",
	}); err == nil {
		t.Error("an unknown analysis mode should be rejected")
	}
	if _, err := runs.AnalysisRun(ctx, 4242); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing run err = %v, want ErrNotFound", err)
	}
}

// The invariant the review asked for: a snapshot's analysis is not a one-shot event. Replay,
// a new prompt set and a model A/B must all be able to run over history already recorded.
func TestSameSnapshotAllowsMultipleAnalysisRuns(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	live := startRun(t, s, snap.ID, "ar-live", "r13-v1")
	replay, err := s.Analyses().StartAnalysisRun(ctx, AnalysisRun{
		StockSnapshotID: snap.ID, RunUID: "ar-replay", Mode: ModeReplay,
		OrchestratorVersion: "r13-m7", PromptSetVersion: "r13-v2",
	})
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	if replay.ID == live.ID {
		t.Fatal("the replay must be its own run")
	}

	runs, err := s.Analyses().AnalysisRunsBySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d analysis runs, want 2", len(runs))
	}
	if runs[0].Mode != ModeLive || runs[1].Mode != ModeReplay {
		t.Errorf("modes = %s,%s, want LIVE,REPLAY in start order", runs[0].Mode, runs[1].Mode)
	}
	if runs[0].PromptSetVersion == runs[1].PromptSetVersion {
		t.Error("the two runs should be distinguishable by prompt set version")
	}

	// The same execution may not cover the same snapshot twice.
	if _, err := s.Analyses().StartAnalysisRun(ctx, AnalysisRun{
		StockSnapshotID: snap.ID, RunUID: "ar-live", Mode: ModeLive,
	}); err == nil {
		t.Error("re-using a run_uid for a snapshot it already covered should fail")
	}
}

// One RunUID spans a whole batch, so the panel produced by a single execution is
// recoverable across stocks.
func TestAnalysisRunUIDSpansTheBatch(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	run, snapA := seedSnapshot(t, s)

	snapB, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: run.ID, Symbol: "2317", TradingDate: run.TradingDate, SourceTab: TabWatchlist,
	})
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	startRun(t, s, snapA.ID, "batch-1", "r13-v1")
	startRun(t, s, snapB.ID, "batch-1", "r13-v1")

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM analysis_runs WHERE run_uid = 'batch-1'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("run_uid batch-1 covers %d snapshots, want 2", n)
	}
}

// ── agent analysis ───────────────────────────────────────────────────────────────────

// The structural ban on agent debate, at the level it actually belongs: WITHIN one
// execution an agent gets one verdict and cannot talk itself into a peer's answer.
func TestAgentCannotReviseWithinAnalysisRun(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")

	first := AgentAnalysis{
		AnalysisRunID: run.ID, Agent: "technical", Verdict: "BULLISH",
		Status: "OK", Model: "gpt-5.6-luna",
	}
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, first); err != nil {
		t.Fatalf("first verdict: %v", err)
	}

	revised := first
	revised.Verdict = "NEUTRAL" // "you convinced me" — must be impossible to store
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, revised); err == nil {
		t.Fatal("an agent must not record a second verdict within the same analysis run")
	}

	got, err := s.Analyses().AgentAnalysesByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].Verdict != "BULLISH" {
		t.Errorf("verdicts = %+v, want exactly the original BULLISH", got)
	}
}

// The other half: the same agent MAY reach a different conclusion in a new run, and both
// answers survive as separate immutable history.
func TestAgentCanReplayInNewAnalysisRun(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	v1 := startRun(t, s, snap.ID, "ar-v1", "r13-v1")
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: v1.ID, Agent: "technical", Verdict: "BULLISH", Status: "OK",
		Model: "gpt-5.6-luna",
	}); err != nil {
		t.Fatalf("v1 verdict: %v", err)
	}

	v2, err := s.Analyses().StartAnalysisRun(ctx, AnalysisRun{
		StockSnapshotID: snap.ID, RunUID: "ar-v2", Mode: ModeReplay, PromptSetVersion: "r13-v2",
	})
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: v2.ID, Agent: "technical", Verdict: "WATCH", Status: "OK",
		Model: "gpt-5.6-luna",
	}); err != nil {
		t.Fatalf("replay verdict must be allowed: %v", err)
	}

	// Each run sees only its own answer.
	one, err := s.Analyses().AgentAnalysesByRun(ctx, v1.ID)
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	if len(one) != 1 || one[0].Verdict != "BULLISH" {
		t.Errorf("run v1 = %+v, want the original BULLISH untouched by the replay", one)
	}

	// The attribution view sees both, oldest run first.
	all, err := s.Analyses().AgentAnalysesBySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d verdicts across runs, want 2", len(all))
	}
	if all[0].Verdict != "BULLISH" || all[1].Verdict != "WATCH" {
		t.Errorf("history = %s,%s, want BULLISH,WATCH in run order", all[0].Verdict, all[1].Verdict)
	}
	if all[0].AnalysisRunID == all[1].AnalysisRunID {
		t.Error("the two verdicts must be attributable to different runs")
	}
}

func TestAgentAnalysisRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")

	score, conf := 82.0, 0.71
	latency, tokens := int64(940), 1330
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Agent: "sector", Verdict: "LEADING",
		Score: &score, Confidence: &conf, Reasoning: "heat 88, breadth 100",
		Model: "gpt-5.6-luna", Status: "OK", LatencyMs: &latency, Tokens: &tokens,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A failed agent is recorded as a failure, not as a neutral opinion.
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Agent: "news", Status: "NO_API_KEY",
	}); err != nil {
		t.Fatalf("save failed agent: %v", err)
	}

	got, err := s.Analyses().AgentAnalysesByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d analyses, want 2", len(got))
	}
	// Ordered by agent: news, sector.
	if got[0].Agent != "news" || got[1].Agent != "sector" {
		t.Errorf("order = %s,%s, want news,sector", got[0].Agent, got[1].Agent)
	}
	if got[0].Score != nil || got[0].Confidence != nil {
		t.Error("an agent that never ran must have no score and no confidence")
	}
	if got[1].Score == nil || *got[1].Score != score {
		t.Errorf("score = %v, want %v", got[1].Score, score)
	}
	if got[1].LatencyMs == nil || *got[1].LatencyMs != latency {
		t.Errorf("latency = %v, want %v", got[1].LatencyMs, latency)
	}
	if got[1].Tokens == nil || *got[1].Tokens != tokens {
		t.Errorf("tokens = %v, want %v", got[1].Tokens, tokens)
	}
}

func TestAgentAnalysisValidates(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")

	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Verdict: "BULLISH", Status: "OK",
	}); err == nil {
		t.Error("an analysis without an agent name should be rejected")
	}
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Agent: "technical",
	}); err == nil {
		t.Error("an analysis without a status should be rejected")
	}
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		Agent: "technical", Status: "OK",
	}); err == nil {
		t.Error("an analysis without an analysis_run_id should be rejected")
	}
	// A dangling run id must be refused by the foreign key, not silently orphaned.
	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: 999999, Agent: "technical", Status: "OK",
	}); err == nil {
		t.Error("an analysis pointing at a non-existent run should be rejected")
	}
}

// ── decisions ────────────────────────────────────────────────────────────────────────

// The scanner's verdict is snapshot-level and written once; each analysis run carries its
// own judge verdict. All of them coexist.
func TestJudgeDecisionBelongsToAnalysisRun(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	an := s.Analyses()

	if _, err := an.SaveScannerDecision(ctx, snap.ID, "BUY"); err != nil {
		t.Fatalf("scanner decision: %v", err)
	}

	r1 := startRun(t, s, snap.ID, "ar-1", "r13-v1")
	r2 := startRun(t, s, snap.ID, "ar-2", "r13-v2")

	conf := 0.55
	if _, err := an.SaveJudgeDecision(ctx, r1.ID, Decision{
		Decision: string(VerdictWatch), Confidence: &conf,
		Reasoning:  "trend intact, wait for pullback",
		Supporting: `["ma20 trend intact"]`, Opposing: `["bias percentile extreme"]`,
		Model: "gpt-5.6-luna",
	}); err != nil {
		t.Fatalf("judge r1: %v", err)
	}
	if _, err := an.SaveJudgeDecision(ctx, r2.ID, Decision{
		Decision: string(VerdictBuy), Model: "gpt-5.6-luna",
	}); err != nil {
		t.Fatalf("judge r2 must be allowed to disagree with r1: %v", err)
	}

	got, err := an.DecisionsBySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d decisions, want 3 (scanner + two runs)", len(got))
	}
	if got[0].Source != DecisionSourceScanner || got[0].Decision != "BUY" {
		t.Errorf("first decision = %s/%s, want the scanner's BUY", got[0].Source, got[0].Decision)
	}
	if got[0].AnalysisRunID != nil {
		t.Error("a scanner decision must not belong to an analysis run")
	}
	if got[1].Decision != string(VerdictWatch) || got[2].Decision != string(VerdictBuy) {
		t.Errorf("judge verdicts = %s,%s, want WATCH,BUY in run order", got[1].Decision, got[2].Decision)
	}
	if got[1].AnalysisRunID == nil || *got[1].AnalysisRunID != r1.ID {
		t.Errorf("judge verdict is not attributable to its run: %v", got[1].AnalysisRunID)
	}
	if got[1].Opposing != `["bias percentile extreme"]` {
		t.Errorf("opposing evidence lost: %q", got[1].Opposing)
	}

	// One judge verdict per run, one scanner decision per snapshot.
	if _, err := an.SaveJudgeDecision(ctx, r1.ID, Decision{Decision: string(VerdictAvoid)}); err == nil {
		t.Error("a second judge decision within the same analysis run should fail")
	}
	if _, err := an.SaveScannerDecision(ctx, snap.ID, "SELL"); err == nil {
		t.Error("a second scanner decision for the same snapshot should fail")
	}
}

// The whole point of the shadow layer: no amount of AI activity may alter what the scanner
// decided, including replays that reach the opposite conclusion.
func TestScannerDecisionUnaffectedByAIReplay(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	an := s.Analyses()

	if _, err := an.SaveScannerDecision(ctx, snap.ID, "BUY"); err != nil {
		t.Fatalf("scanner decision: %v", err)
	}

	for i, verdict := range []JudgeVerdict{VerdictAvoid, VerdictReduce, VerdictAdd} {
		run := startRun(t, s, snap.ID, fmt.Sprintf("ar-%d", i), "r13-v1")
		if _, err := an.SaveJudgeDecision(ctx, run.ID, Decision{Decision: string(verdict)}); err != nil {
			t.Fatalf("judge %s: %v", verdict, err)
		}
		if _, err := an.SaveAgentAnalysis(ctx, AgentAnalysis{
			AnalysisRunID: run.ID, Agent: "risk", Verdict: "HIGH", Status: "OK",
		}); err != nil {
			t.Fatalf("agent: %v", err)
		}
	}

	got, err := an.DecisionsBySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	scanner := got[0]
	if scanner.Source != DecisionSourceScanner || scanner.Decision != "BUY" {
		t.Fatalf("the scanner decision changed to %s/%s after three AI replays",
			scanner.Source, scanner.Decision)
	}
	if scanner.CreatedAt.IsZero() {
		t.Error("the scanner decision lost its timestamp")
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decisions WHERE stock_snapshot_id = ? AND source = ?`,
		snap.ID, DecisionSourceScanner).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("scanner decision rows = %d, want exactly 1", n)
	}
}

// A judge verdict must belong to the snapshot its RUN analysed — the caller does not get to
// name a different one.
func TestJudgeDecisionDerivesItsSnapshotFromTheRun(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	scanRun, snapA := seedSnapshot(t, s)

	snapB, err := s.Scans().SaveSnapshot(ctx, StockSnapshot{
		ScanRunID: scanRun.ID, Symbol: "2317", TradingDate: scanRun.TradingDate, SourceTab: TabWatchlist,
	})
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	run := startRun(t, s, snapA.ID, "ar-1", "r13-v1")

	// The caller tries to file it against the wrong stock; the run wins.
	saved, err := s.Analyses().SaveJudgeDecision(ctx, run.ID, Decision{
		StockSnapshotID: snapB.ID, Decision: string(VerdictHold),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.StockSnapshotID != snapA.ID {
		t.Errorf("decision filed against snapshot %d, want %d (the run's own)",
			saved.StockSnapshotID, snapA.ID)
	}
	if wrong, err := s.Analyses().DecisionsBySnapshot(ctx, snapB.ID); err != nil || len(wrong) != 0 {
		t.Errorf("snapshot B picked up %d decisions it never had a run for", len(wrong))
	}
}

func TestJudgeVerdictVocabularyIsEnforced(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")

	// The six defined verdicts are exactly the valid ones.
	for _, v := range JudgeVerdicts {
		if !v.Valid() {
			t.Errorf("%s should be a valid verdict", v)
		}
	}
	for _, v := range []JudgeVerdict{"", "STRONG BUY", "SELL", "buy", "TAKE PROFIT"} {
		if v.Valid() {
			t.Errorf("%q must not be a valid JudgeVerdict", v)
		}
	}
	if len(JudgeVerdicts) != 6 {
		t.Errorf("JudgeVerdicts has %d entries, want the 6 defined verdicts", len(JudgeVerdicts))
	}

	// A scanner action is NOT a judge verdict: the two vocabularies stay separate.
	if _, err := s.Analyses().SaveJudgeDecision(ctx, run.ID, Decision{Decision: "STRONG BUY"}); err == nil {
		t.Error("a scanner.Action must not be storable as a judge verdict")
	}
	if _, err := s.Analyses().SaveJudgeDecision(ctx, 999999, Decision{
		Decision: string(VerdictBuy),
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("judge decision on a missing run = %v, want ErrNotFound", err)
	}
	if _, err := s.Analyses().SaveScannerDecision(ctx, snap.ID, ""); err == nil {
		t.Error("an empty scanner action should be rejected")
	}
}

// The decisions CHECK constraint and its two partial indexes hard-code the source literals.
// If the Go constants are renamed without touching the SQL, every insert starts failing at
// runtime instead of here.
func TestDecisionSourceConstantsMatchSchema(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	var ddl string
	if err := s.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'decisions'`).Scan(&ddl); err != nil {
		t.Fatalf("read ddl: %v", err)
	}
	for _, want := range []string{
		"source = '" + DecisionSourceScanner + "'",
		"source = '" + DecisionSourceJudge + "'",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("the decisions CHECK does not mention %s — Go and SQL have drifted", want)
		}
	}

	var indexes int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name IN ('idx_decisions_scanner_once', 'idx_decisions_judge_once')`).
		Scan(&indexes); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	if indexes != 2 {
		t.Errorf("found %d of the 2 partial unique indexes that separate the two sources", indexes)
	}
}

// A mislevelled decision must be impossible even when the repository is bypassed: the CHECK
// constraint, not the Go wrapper, is what makes the two levels real.
func TestDecisionLevelsAreEnforcedBySchema(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")
	now := formatTime(nowUTC())

	// A scanner decision that claims an analysis run.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, ?, ?, 'BUY', ?)`, snap.ID, run.ID, DecisionSourceScanner, now); err == nil {
		t.Error("a SCANNER decision with an analysis_run_id should violate the CHECK")
	}
	// A judge decision floating free of any run.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, NULL, ?, 'WATCH', ?)`, snap.ID, DecisionSourceJudge, now); err == nil {
		t.Error("an AI_JUDGE decision without an analysis_run_id should violate the CHECK")
	}

	// Positive control: the same statement shape with the levels the RIGHT way round must
	// succeed. Without this, the two assertions above would also pass if the SQL were simply
	// malformed, and the test would be proving nothing.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, NULL, ?, 'BUY', ?)`, snap.ID, DecisionSourceScanner, now); err != nil {
		t.Fatalf("a correctly levelled SCANNER decision should insert: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, ?, ?, 'WATCH', ?)`, snap.ID, run.ID, DecisionSourceJudge, now); err != nil {
		t.Fatalf("a correctly levelled AI_JUDGE decision should insert: %v", err)
	}
	// And the partial unique indexes still bite on a second row of each kind.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, NULL, ?, 'SELL', ?)`, snap.ID, DecisionSourceScanner, now); err == nil {
		t.Error("a second SCANNER decision should violate idx_decisions_scanner_once")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (stock_snapshot_id, analysis_run_id, source, decision, created_at)
		VALUES (?, ?, ?, 'AVOID', ?)`, snap.ID, run.ID, DecisionSourceJudge, now); err == nil {
		t.Error("a second AI_JUDGE decision for the same run should violate idx_decisions_judge_once")
	}
}

// Deleting a scan run must take the analysis runs, agent verdicts and decisions with it.
func TestCascadeReachesAnalysisRows(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	scanRun, snap := seedSnapshot(t, s)
	run := startRun(t, s, snap.ID, "ar-1", "r13-v1")

	if _, err := s.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Agent: "technical", Status: "OK", Verdict: "BULLISH",
	}); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if _, err := s.Analyses().SaveScannerDecision(ctx, snap.ID, "BUY"); err != nil {
		t.Fatalf("scanner decision: %v", err)
	}
	if _, err := s.Analyses().SaveJudgeDecision(ctx, run.ID, Decision{Decision: string(VerdictWatch)}); err != nil {
		t.Fatalf("judge decision: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM scan_runs WHERE id = ?`, scanRun.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, table := range []string{"analysis_runs", "agent_analysis", "decisions"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s kept %d rows after the scan run was deleted", table, n)
		}
	}
}

// ── outcomes ─────────────────────────────────────────────────────────────────────────

func TestOutcomeEnsureIsIdempotentAndKeepsItsBase(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	first, err := s.Outcomes().Ensure(ctx, snap.ID, 100)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if first.BaseClose != 100 || first.BarsObserved != 0 {
		t.Errorf("fresh outcome = %+v", first)
	}
	if first.Return1D != nil || first.Return3D != nil || first.Return5D != nil ||
		first.Return10D != nil || first.Return20D != nil || first.MaxGain20D != nil {
		t.Error("a fresh outcome must have no measurements yet")
	}

	// A second Ensure with a DIFFERENT base must not move the basis: every return already
	// stored was measured against the original.
	again, err := s.Outcomes().Ensure(ctx, snap.ID, 250)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("Ensure created a second row (%d vs %d)", again.ID, first.ID)
	}
	if again.BaseClose != 100 {
		t.Errorf("base close moved to %v — the basis of a measurement must be fixed", again.BaseClose)
	}
}

func TestOutcomeReturnsAreWriteOnce(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	if _, err := s.Outcomes().Ensure(ctx, snap.ID, 100); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	ok, err := s.Outcomes().SetReturn(ctx, snap.ID, H5D, 4.2)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !ok {
		t.Fatal("the first write of a horizon must land")
	}

	// Hindsight tries again with a better number. It must be refused, quietly and without
	// error — a rerun over a settled date is normal, overwriting history is not.
	ok, err = s.Outcomes().SetReturn(ctx, snap.ID, H5D, 99.9)
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if ok {
		t.Error("a horizon that already has a value must not be rewritten")
	}

	got, err := s.Outcomes().BySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Return5D == nil || *got.Return5D != 4.2 {
		t.Errorf("return_5d = %v, want the original 4.2", got.Return5D)
	}
	// The other horizons must be untouched by all of that.
	if got.Return1D != nil || got.Return20D != nil {
		t.Error("writing one horizon must not populate the others")
	}
}

func TestOutcomeExtremesAndBarsObserved(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)
	if _, err := s.Outcomes().Ensure(ctx, snap.ID, 100); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if ok, err := s.Outcomes().SetExtremes(ctx, snap.ID, 12.5, -6.1); err != nil || !ok {
		t.Fatalf("set extremes: ok=%v err=%v", ok, err)
	}
	if ok, err := s.Outcomes().SetExtremes(ctx, snap.ID, 90, -1); err != nil || ok {
		t.Errorf("extremes must be write-once: ok=%v err=%v", ok, err)
	}

	if err := s.Outcomes().SetBarsObserved(ctx, snap.ID, 12); err != nil {
		t.Fatalf("bars: %v", err)
	}
	// A partial re-scan must not make a measured snapshot look unmeasured.
	if err := s.Outcomes().SetBarsObserved(ctx, snap.ID, 3); err != nil {
		t.Fatalf("bars backwards: %v", err)
	}

	got, err := s.Outcomes().BySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.MaxGain20D == nil || *got.MaxGain20D != 12.5 {
		t.Errorf("max gain = %v, want 12.5", got.MaxGain20D)
	}
	if got.MaxDrawdown20D == nil || *got.MaxDrawdown20D != -6.1 {
		t.Errorf("max drawdown = %v, want -6.1", got.MaxDrawdown20D)
	}
	if got.BarsObserved != 12 {
		t.Errorf("bars observed = %d, want 12 (must only move forward)", got.BarsObserved)
	}
}

func TestOutcomeErrorsOnMissingRow(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	if _, err := s.Outcomes().BySnapshot(ctx, snap.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("BySnapshot err = %v, want ErrNotFound", err)
	}
	// Setting a return before Ensure is a caller bug, not a silent no-op.
	if _, err := s.Outcomes().SetReturn(ctx, snap.ID, H1D, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetReturn err = %v, want ErrNotFound", err)
	}
	if _, err := s.Outcomes().SetReturn(ctx, snap.ID, Horizon(7), 1); err == nil {
		t.Error("an untracked horizon should be rejected")
	}
	if _, err := s.Outcomes().Ensure(ctx, snap.ID, 0); err == nil {
		t.Error("a non-positive base close should be rejected")
	}
}

func TestPendingHorizonIsTheWorkQueue(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	scans := s.Scans()

	mk := func(uid, date, symbol string) StockSnapshot {
		run, err := scans.StartRun(ctx, ScanRun{RunUID: uid, TradingDate: date})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		px := 50.0
		snap, err := scans.SaveSnapshot(ctx, StockSnapshot{
			ScanRunID: run.ID, Symbol: symbol, TradingDate: date,
			SourceTab: TabWatchlist, Close: &px,
		})
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if _, err := s.Outcomes().Ensure(ctx, snap.ID, px); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		return snap
	}

	old := mk("r1", "2026-07-01", "1111")
	mid := mk("r2", "2026-07-15", "2222")
	// Dated after the cutoff: its future has not happened yet, so it must never appear.
	mk("r3", "2026-09-01", "3333")

	// Settle the older one; it should drop out of the queue.
	if _, err := s.Outcomes().SetReturn(ctx, old.ID, H5D, 1.1); err != nil {
		t.Fatalf("set: %v", err)
	}

	pending, err := s.Outcomes().PendingHorizon(ctx, H5D, "2026-08-11", 0)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d rows %+v, want 1", len(pending), pending)
	}
	if pending[0].ID != mid.ID {
		t.Errorf("pending row = %s, want %s", pending[0].Symbol, mid.Symbol)
	}

	// A different horizon is still outstanding for both dated on or before the cutoff.
	pending20, err := s.Outcomes().PendingHorizon(ctx, H20D, "2026-08-11", 0)
	if err != nil {
		t.Fatalf("pending 20d: %v", err)
	}
	if len(pending20) != 2 {
		t.Errorf("pending 20d = %d, want 2 (horizons are independent)", len(pending20))
	}

	if limited, err := s.Outcomes().PendingHorizon(ctx, H20D, "2026-08-11", 1); err != nil || len(limited) != 1 {
		t.Errorf("limit ignored: %d rows, err=%v", len(limited), err)
	}
}

// ── transactions ─────────────────────────────────────────────────────────────────────

func TestWithTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	_, snap := seedSnapshot(t, s)

	wantErr := errors.New("boom")
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO evidence (stock_snapshot_id, category, key, num_value, created_at)
			VALUES (?, 'technical', 'ma20', 1, ?)`, snap.ID, formatTime(nowUTC())); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the callback's error", err)
	}

	got, err := s.Evidence().BySnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rolled-back rows survived: %+v", got)
	}
}

func TestCloseIsSafeOnNilStore(t *testing.T) {
	var s *Store
	if err := s.Close(); err != nil {
		t.Errorf("Close on a nil store = %v, want nil", err)
	}
	if s.Path() != "" {
		t.Error("Path on a nil store should be empty")
	}
}
