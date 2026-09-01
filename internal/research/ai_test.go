package research

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// aiEntry is a watchlist entry carrying a finished AI reading.
func aiEntry(symbol string, a *ai.Analysis) scanner.WatchlistEntry {
	e := bareEntry(symbol)
	e.AI = a
	return e
}

func okAnalysis(symbol string) *ai.Analysis {
	return &ai.Analysis{
		Symbol: symbol, Status: ai.StatusOK,
		Summary:    "沿 20 日線整理，量能收斂。",
		BullCase:   []string{"守住 MA20", "族群資金流入"},
		BearCase:   []string{"乖離仍偏高"},
		RiskFlags:  []string{"成交量偏低"},
		Confidence: 0.62, Model: "gpt-5.6-luna", LatencyMs: 1234, Tokens: 890,
	}
}

// recordScanWith persists one scan and returns the recorder plus its snapshot map.
func recordScanWith(t *testing.T, entries []scanner.WatchlistEntry) (*Recorder, Result) {
	t.Helper()
	rec, _ := openRecorder(t)
	res, err := rec.RecordScan(context.Background(), RunMeta{TradingDate: "2026-08-31"},
		Input{Watchlist: entries})
	if err != nil {
		t.Fatalf("record scan: %v", err)
	}
	return rec, res
}

// THE point of M3: an AI reading must survive the run that produced it.
func TestAISuccessIsPersisted(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.WatchlistEntry{aiEntry("2330", okAnalysis("2330"))}
	rec, scan := recordScanWith(t, entries)

	got, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
	if err != nil {
		t.Fatalf("record ai: %v", err)
	}
	if got.Runs != 1 || got.Agents != 1 || got.Skipped != 0 {
		t.Fatalf("result = %+v, want 1 run / 1 agent / 0 skipped", got)
	}

	snapID := scan.WatchlistSnapshots["2330"]
	rows, err := rec.Store().Analyses().AgentAnalysesBySnapshot(ctx, snapID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("read back: %v (%d rows)", err, len(rows))
	}
	a := rows[0]

	if a.Agent != store.AgentOpenAIAnalyst {
		t.Errorf("agent = %q, want the shared constant %q", a.Agent, store.AgentOpenAIAnalyst)
	}
	if a.Status != string(ai.StatusOK) {
		t.Errorf("status = %q", a.Status)
	}
	if a.Reasoning != "沿 20 日線整理，量能收斂。" {
		t.Errorf("summary did not land in reasoning: %q", a.Reasoning)
	}
	if a.Model != "gpt-5.6-luna" {
		t.Errorf("model = %q", a.Model)
	}
	if a.Confidence == nil || *a.Confidence != 0.62 {
		t.Errorf("confidence = %v", a.Confidence)
	}
	if a.LatencyMs == nil || *a.LatencyMs != 1234 {
		t.Errorf("latency = %v", a.LatencyMs)
	}
	if a.Tokens == nil || *a.Tokens != 890 {
		t.Errorf("tokens = %v", a.Tokens)
	}

	// The three lists must be recoverable INDIVIDUALLY — that is what the structured column
	// is for, and packing them into prose would pass a weaker assertion than this one.
	var out aiStructuredOutput
	if err := json.Unmarshal([]byte(a.OutputJSON), &out); err != nil {
		t.Fatalf("output_json is not valid JSON (%q): %v", a.OutputJSON, err)
	}
	if len(out.BullCase) != 2 || out.BullCase[0] != "守住 MA20" {
		t.Errorf("bull case = %v", out.BullCase)
	}
	if len(out.BearCase) != 1 || len(out.RiskFlags) != 1 {
		t.Errorf("bear/risk = %v / %v", out.BearCase, out.RiskFlags)
	}

	// The analyst must NOT look like a decision maker in the record.
	if a.Verdict != "" {
		t.Errorf("verdict = %q; the R11 analyst describes, it does not decide", a.Verdict)
	}

	// And the run it belongs to must be closed, with the provenance a later reader needs.
	runs, err := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, snapID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v (%d)", err, len(runs))
	}
	r := runs[0]
	if r.Status != store.RunStatusComplete {
		t.Errorf("run status = %q, want COMPLETE", r.Status)
	}
	if r.Mode != store.ModeLive {
		t.Errorf("mode = %q", r.Mode)
	}
	if r.OrchestratorVersion != AIPersistVersion || r.PromptSetVersion != ai.PromptSetVersion {
		t.Errorf("provenance missing: orchestrator=%q prompt=%q",
			r.OrchestratorVersion, r.PromptSetVersion)
	}
	if r.FinishedAt == nil {
		t.Error("run was never stamped finished")
	}
}

// A failed analysis must be recorded AS a failure, not dropped and not smoothed into a
// neutral opinion — otherwise the store's success rate is a lie by omission.
func TestAIFailureIsPersistedAsFailure(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		status ai.Status
		reason string
	}{
		{"no key", ai.StatusNoKey, "未設定 OPENAI_API_KEY"},
		{"transport", ai.StatusError, "timeout"},
		{"bad output", ai.StatusBadOutput, "模型回應不是合法 JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []scanner.WatchlistEntry{
				aiEntry("2330", &ai.Analysis{Symbol: "2330", Status: tc.status, Reason: tc.reason}),
			}
			rec, scan := recordScanWith(t, entries)
			got, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
			if err != nil {
				t.Fatalf("record: %v", err)
			}
			if got.Agents != 1 {
				t.Fatalf("a failed analysis was not recorded: %+v", got)
			}
			if got.ByStatus[string(tc.status)] != 1 {
				t.Errorf("status counters = %v", got.ByStatus)
			}

			rows, _ := rec.Store().Analyses().AgentAnalysesBySnapshot(ctx, scan.WatchlistSnapshots["2330"])
			if len(rows) != 1 {
				t.Fatalf("got %d rows", len(rows))
			}
			if rows[0].Status != string(tc.status) {
				t.Errorf("status = %q, want %q", rows[0].Status, tc.status)
			}
			// A failed analysis has no summary, so the one-line reason takes the prose
			// column — the failure stays legible without re-running anything.
			if rows[0].Reasoning != tc.reason {
				t.Errorf("reason not preserved: %q", rows[0].Reasoning)
			}
			// A failure has no confidence. Storing 0.0 would read as "certain of nothing"
			// rather than "never asked".
			if rows[0].Confidence != nil {
				t.Errorf("a failed analysis carries confidence %v", *rows[0].Confidence)
			}
			// The RUN must be marked failed too, or a later query for successful runs
			// would count this one.
			runs, _ := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, scan.WatchlistSnapshots["2330"])
			if len(runs) != 1 || runs[0].Status != store.RunStatusFailed {
				t.Errorf("run status = %+v, want FAILED", runs)
			}
		})
	}
}

// AI off must leave the AI tables untouched. A row saying "we did not ask" is still a row,
// and it would inflate every later count.
func TestAIDisabledWritesNothing(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.WatchlistEntry{bareEntry("2330"), bareEntry("2454")} // AI field nil
	rec, scan := recordScanWith(t, entries)

	got, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.Runs != 0 || got.Agents != 0 {
		t.Fatalf("wrote %+v for a scan with no AI at all", got)
	}
	for sym, id := range scan.WatchlistSnapshots {
		runs, _ := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, id)
		if len(runs) != 0 {
			t.Errorf("%s: %d analysis runs with AI disabled", sym, len(runs))
		}
	}
	// The scanner's own decisions must still be there — this feature is additive.
	dec, err := rec.Store().Analyses().DecisionsBySnapshot(ctx, scan.WatchlistSnapshots["2330"])
	if err != nil || len(dec) != 1 || dec[0].Source != store.DecisionSourceScanner {
		t.Errorf("scanner decision disturbed: %v / %+v", err, dec)
	}
}

// One scan is ONE AI run. Ten stocks share a single run_uid, so "what did the panel think
// that evening" is a query, not a reconstruction.
func TestManyStocksShareOneRunUID(t *testing.T) {
	ctx := context.Background()
	var entries []scanner.WatchlistEntry
	for _, sym := range []string{"2330", "2454", "2317", "3231"} {
		entries = append(entries, aiEntry(sym, okAnalysis(sym)))
	}
	rec, scan := recordScanWith(t, entries)

	got, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.Runs != 4 || got.Agents != 4 {
		t.Fatalf("result = %+v, want 4/4", got)
	}

	seen := map[string]bool{}
	for _, id := range scan.WatchlistSnapshots {
		runs, err := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, id)
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs for snapshot %d: %v (%d)", id, err, len(runs))
		}
		seen[runs[0].RunUID] = true
	}
	if len(seen) != 1 {
		t.Errorf("4 stocks produced %d distinct run uids: %v — one scan must be one AI run",
			len(seen), seen)
	}
	if got.RunUID == "" || !seen[got.RunUID] {
		t.Errorf("reported run uid %q is not the one stored", got.RunUID)
	}
}

// Re-running must not silently duplicate, and must not overwrite. A second execution is a
// REPLAY: it gets its own run and its own immutable rows beside the first.
func TestRerunProducesASeparateRunNotADuplicate(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.WatchlistEntry{aiEntry("2330", okAnalysis("2330"))}
	rec, scan := recordScanWith(t, entries)

	first, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// A second reading of the SAME snapshots — a replay against the same scan.
	second := []scanner.WatchlistEntry{aiEntry("2330", &ai.Analysis{
		Symbol: "2330", Status: ai.StatusOK, Summary: "第二次解讀", Confidence: 0.4,
	})}
	secondRes, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, second)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.RunUID == secondRes.RunUID {
		t.Fatal("a rerun reused the first run's uid — the two readings are now indistinguishable")
	}

	snapID := scan.WatchlistSnapshots["2330"]
	runs, _ := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, snapID)
	if len(runs) != 2 {
		t.Fatalf("got %d analysis runs after a rerun, want 2 (replay, not overwrite)", len(runs))
	}
	rows, _ := rec.Store().Analyses().AgentAnalysesBySnapshot(ctx, snapID)
	if len(rows) != 2 {
		t.Fatalf("got %d agent rows, want 2", len(rows))
	}
	// The first reading must be unchanged — this is an append-only audit store.
	if rows[0].Reasoning != "沿 20 日線整理，量能收斂。" {
		t.Errorf("the original reading was altered: %q", rows[0].Reasoning)
	}
	if rows[1].Reasoning != "第二次解讀" {
		t.Errorf("the replay did not land: %q", rows[1].Reasoning)
	}

	// And within ONE run, one agent still cannot write twice — the structural ban holds.
	if _, err := rec.Store().Analyses().SaveAgentAnalysis(ctx, store.AgentAnalysis{
		AnalysisRunID: runs[0].ID, Agent: store.AgentOpenAIAnalyst, Status: "OK",
	}); err == nil {
		t.Error("a second verdict from the same agent in the same run was accepted")
	}
}

// A stock with a reading but no snapshot id (not on the watchlist tab) is skipped rather
// than attached to the wrong row.
func TestAIWithoutSnapshotIsSkipped(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.WatchlistEntry{aiEntry("2330", okAnalysis("2330"))}
	rec, scan := recordScanWith(t, entries)

	// A map that does not contain the symbol.
	got, err := rec.RecordAI(ctx, scan.RunUID, map[string]int64{"9999": 12345}, entries)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.Runs != 0 {
		t.Errorf("wrote %d runs for a symbol with no snapshot", got.Runs)
	}
}

// An analysis with no lists must store "" — distinguishable from "{}", which would mean the
// model returned an empty structure.
func TestNoStructuredOutputStoresEmptyNotBraces(t *testing.T) {
	ctx := context.Background()
	entries := []scanner.WatchlistEntry{aiEntry("2330", &ai.Analysis{
		Symbol: "2330", Status: ai.StatusOK, Summary: "只有摘要", Confidence: 0.5,
	})}
	rec, scan := recordScanWith(t, entries)
	if _, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries); err != nil {
		t.Fatal(err)
	}
	rows, _ := rec.Store().Analyses().AgentAnalysesBySnapshot(ctx, scan.WatchlistSnapshots["2330"])
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	if rows[0].OutputJSON != "" {
		t.Errorf("OutputJSON = %q, want empty (no structure produced, not an empty one)",
			rows[0].OutputJSON)
	}
}

// A nil recorder is a no-op, so the call site needs no branch.
func TestRecordAIOnNilRecorder(t *testing.T) {
	var rec *Recorder
	got, err := rec.RecordAI(context.Background(), "x", map[string]int64{"2330": 1},
		[]scanner.WatchlistEntry{aiEntry("2330", okAnalysis("2330"))})
	if err != nil || got.Runs != 0 {
		t.Errorf("nil recorder: %+v / %v", got, err)
	}
}

// ── market context → scan_runs ────────────────────────────────────────────────────────

// Audit finding: 40 scan_runs, market_regime empty on every one. The engine existed and its
// output never reached the record. This asserts the wiring, not the engine.
func TestMarketRegimeReachesTheScanRun(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	score, conf := 61.4, 0.42
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-31"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
		MarketCtx: MarketContext{
			Regime: "BULL_PULLBACK", Score: &score, Confidence: &conf, AsOfDate: "2026-08-31",
		},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	run, err := rec.Store().Scans().RunByUID(ctx, res.RunUID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.MarketRegime != "BULL_PULLBACK" {
		t.Errorf("scan_runs.market_regime = %q, want BULL_PULLBACK", run.MarketRegime)
	}
	if run.MarketScore == nil || *run.MarketScore != 61.4 {
		t.Errorf("scan_runs.market_score = %v", run.MarketScore)
	}
}

// With no snapshot the regime must stay EMPTY — never a guessed default. An empty string
// here reads as "unknown"; a "SIDEWAYS" written by us would read as a measurement.
func TestMissingMarketLeavesTheRegimeEmpty(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)

	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-31"}, Input{
		Watchlist: []scanner.WatchlistEntry{bareEntry("2330")},
		MarketCtx: MarketContext{}, // no snapshot for this date
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	run, err := rec.Store().Scans().RunByUID(ctx, res.RunUID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if run.MarketRegime != "" {
		t.Errorf("market_regime = %q with no snapshot; want empty, never a default", run.MarketRegime)
	}
	if run.MarketScore != nil {
		t.Errorf("market_score = %v with no snapshot; want NULL", *run.MarketScore)
	}
}

// A candidate the AI deliberately passed over must leave NO row. SKIPPED is not a failure —
// it is the absence of an attempt, and a row for it would say the opposite.
func TestSkippedAndDisabledLeaveNoRow(t *testing.T) {
	ctx := context.Background()
	for _, st := range []ai.Status{ai.StatusSkipped, ai.StatusDisabled} {
		t.Run(string(st), func(t *testing.T) {
			entries := []scanner.WatchlistEntry{
				aiEntry("2330", &ai.Analysis{Symbol: "2330", Status: st}),
				aiEntry("2454", okAnalysis("2454")), // one real analysis beside it
			}
			rec, scan := recordScanWith(t, entries)
			got, err := rec.RecordAI(ctx, scan.RunUID, scan.WatchlistSnapshots, entries)
			if err != nil {
				t.Fatal(err)
			}
			if got.Runs != 1 || got.Agents != 1 {
				t.Fatalf("result = %+v, want only the analysed stock recorded", got)
			}
			runs, _ := rec.Store().Analyses().AnalysisRunsBySnapshot(ctx, scan.WatchlistSnapshots["2330"])
			if len(runs) != 0 {
				t.Errorf("%s produced %d analysis runs", st, len(runs))
			}
			// And the one that WAS analysed is still there.
			ok, _ := rec.Store().Analyses().AgentAnalysesBySnapshot(ctx, scan.WatchlistSnapshots["2454"])
			if len(ok) != 1 {
				t.Errorf("the analysed stock lost its row: %d", len(ok))
			}
		})
	}
}
