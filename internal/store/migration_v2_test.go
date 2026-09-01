package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// openAt opens a store at an explicit path so a migration can be observed across two opens.
func openAt(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// A v1 database must be upgradable in place, and everything already in it must survive.
//
// This is the property that makes the migration safe to ship: the R13 store is an audit
// store, so a migration that silently dropped recorded evidence would be worse than no
// migration at all.
func TestMigrationV2IsBackwardCompatible(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v1.db")

	// Build a database at version 1 exactly as the shipped v1 binary would have, then write
	// a row through the v1 column set.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version != 1 {
			continue
		}
		for _, stmt := range m.stmts {
			if _, err := raw.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("v1 ddl: %v", err)
			}
		}
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (1,'r13_foundation','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// A snapshot, a run and an agent row, all inserted WITHOUT naming output_json — the
	// shape a v1 binary produced.
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO scan_runs (run_uid, trading_date, started_at, scanner_version, market_regime, universe_size, note)
	          VALUES ('legacy','2026-01-02','2026-01-02T00:00:00Z','v1','',0,'')`)
	mustExec(`INSERT INTO stock_snapshots (scan_run_id, symbol, name, trading_date, source_tab, scanner_action, created_at)
	          VALUES (1,'2330','台積電','2026-01-02','watchlist','BUY','2026-01-02T00:00:00Z')`)
	mustExec(`INSERT INTO analysis_runs (stock_snapshot_id, run_uid, mode, status, started_at, created_at)
	          VALUES (1,'legacy-ai','LIVE','COMPLETE','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z')`)
	mustExec(`INSERT INTO agent_analysis (analysis_run_id, agent, verdict, reasoning, model, status, created_at)
	          VALUES (1,'LEGACY_AGENT','','舊資料','old-model','OK','2026-01-02T00:00:00Z')`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	// Now open with the current binary: it must migrate to v2 rather than refuse.
	st := openAt(t, path)
	if got := SchemaVersion; got != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", got)
	}

	rows, err := st.Analyses().AgentAnalysesByRun(ctx, 1)
	if err != nil {
		t.Fatalf("read legacy row after migration: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d legacy rows, want 1 — the migration lost recorded evidence", len(rows))
	}
	got := rows[0]
	if got.Agent != "LEGACY_AGENT" || got.Reasoning != "舊資料" || got.Model != "old-model" {
		t.Errorf("legacy row was altered: %+v", got)
	}
	// The new column must read back as "no structured output", not as invalid JSON.
	if got.OutputJSON != "" {
		t.Errorf("legacy row gained OutputJSON %q; want empty", got.OutputJSON)
	}

	// And a v1-shaped INSERT (not naming the column) must still work after the migration,
	// which is what NOT NULL DEFAULT '' buys.
	if _, err := st.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: 1, Agent: "SECOND_AGENT", Status: "OK", Reasoning: "no structure",
	}); err != nil {
		t.Fatalf("insert without structured output failed after migration: %v", err)
	}
}

// The new column must round-trip, and must stay distinguishable from "the agent produced {}".
func TestOutputJSONRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)
	_, snap := seedSnapshot(t, st)
	snapID := snap.ID

	run, err := st.Analyses().StartAnalysisRun(ctx, AnalysisRun{
		StockSnapshotID: snapID, RunUID: "ai-1", Mode: ModeLive, Status: RunStatusRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	const payload = `{"bull_case":["量增"],"bear_case":["乖離大"],"risk_flags":["流動性"]}`
	if _, err := st.Analyses().SaveAgentAnalysis(ctx, AgentAnalysis{
		AnalysisRunID: run.ID, Agent: AgentOpenAIAnalyst, Status: "OK",
		Reasoning: "整理末端", OutputJSON: payload,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.Analyses().AgentAnalysesByRun(ctx, run.ID)
	if err != nil || len(got) != 1 {
		t.Fatalf("read back: %v (%d rows)", err, len(got))
	}
	if got[0].OutputJSON != payload {
		t.Errorf("OutputJSON = %q, want %q", got[0].OutputJSON, payload)
	}
	if got[0].Reasoning != "整理末端" {
		t.Errorf("prose column was disturbed: %q", got[0].Reasoning)
	}
	// Reading by snapshot must project the column too — the second query is easy to forget.
	bySnap, err := st.Analyses().AgentAnalysesBySnapshot(ctx, snapID)
	if err != nil || len(bySnap) != 1 {
		t.Fatalf("by snapshot: %v (%d rows)", err, len(bySnap))
	}
	if bySnap[0].OutputJSON != payload {
		t.Errorf("by-snapshot query dropped OutputJSON: %q", bySnap[0].OutputJSON)
	}
}
