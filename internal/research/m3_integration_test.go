package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"database/sql"
	"github.com/deep-huang/stock-scanner/internal/ai"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
	_ "modernc.org/sqlite"
)

// A fake Responses API. No key, no network, no cost — and it exercises the REAL client,
// parser and persistence path rather than a stub of them.
func fakeOpenAI(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"output": []any{map[string]any{
				"type": "message",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": `{"summary":"沿 20 日線整理，量能收斂。","bull_case":["守住 MA20","族群資金流入"],"bear_case":["乖離仍偏高"],"risk_flags":["成交量偏低"],"confidence":0.62}`,
				}},
			}},
			"usage": map[string]any{"total_tokens": 812},
			"model": "gpt-5.6-luna",
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestM3EndToEndPersistence(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "verify.db")
	t.Setenv("OPENAI_API_KEY", "test-key-not-a-real-secret")

	// 1. A watchlist the scanner has already finished scoring.
	var entries []scanner.WatchlistEntry
	for i, sym := range []string{"2330", "2454", "2317"} {
		e := scanner.WatchlistEntry{
			RocketScore: 80 - i, RocketStage: scanner.StagePreBreakout,
			WatchAction: scanner.ActPrepare, Sector: "半導體",
		}
		e.A = scanner.StockAnalysis{
			Symbol: sym, Name: sym, Close: 100 + float64(i), MA20: 95,
			Score: 70 + i, Action: scanner.ActionBuy,
		}
		entries = append(entries, e)
	}
	// One candidate the AI must not spend a request on.
	skip := entries[0]
	skip.A.Symbol = "9999"
	skip.WatchAction = scanner.ActWait
	entries = append(entries, skip)

	before := append([]scanner.WatchlistEntry(nil), entries...)

	// 2. AI post-pass against the fake endpoint, WITH a market context.
	scanner.AttachAI(ctx, entries, ai.Config{BaseURL: fakeOpenAI(t), Model: "gpt-5.6-luna"},
		true, scanner.AIMarketContext{Available: true, Regime: "BULL_PULLBACK", Score: 61.4}, nil)

	// 3. Shadow check BEFORE persistence: nothing the scanner decided may have moved.
	for i := range entries {
		if entries[i].RocketScore != before[i].RocketScore ||
			entries[i].WatchAction != before[i].WatchAction ||
			entries[i].A.Action != before[i].A.Action ||
			entries[i].A.Score != before[i].A.Score {
			t.Fatalf("%s changed across AttachAI", entries[i].A.Symbol)
		}
	}

	// 4. Persist the scan, then the AI.
	rec, closeStore, err := Open(Config{
		Enabled: true, Store: store.Config{Path: dbPath},
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer closeStore()

	score, conf := 61.4, 0.42
	scanRes, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-08-31"},
		Input{
			Watchlist: entries,
			MarketCtx: MarketContext{
				Regime: "BULL_PULLBACK", Score: &score, Confidence: &conf, AsOfDate: "2026-08-31",
			},
		})
	if err != nil {
		t.Fatalf("record scan: %v", err)
	}
	aiRes, err := rec.RecordAI(ctx, scanRes.RunUID, scanRes.WatchlistSnapshots, entries)
	if err != nil {
		t.Fatalf("record ai: %v", err)
	}
	t.Logf("scan: %d snapshots / %d evidence | ai: %d runs / %d agents",
		scanRes.Snapshots, scanRes.Evidence, aiRes.Runs, aiRes.Agents)

	// 5. SQL-level verification, on the real file.
	closeStore()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	q1 := func(q string) string {
		var v sql.NullString
		if err := db.QueryRow(q).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v.String
	}
	dump := func(title, q string) {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		defer rows.Close()
		fmt.Printf("\n%s\n", title)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			line := "  "
			for i, c := range cols {
				line += fmt.Sprintf("%s=%v  ", c, sqlVal(vals[i]))
			}
			fmt.Println(line)
		}
	}

	dump("SELECT COUNT(*) FROM analysis_runs;", "SELECT COUNT(*) AS n FROM analysis_runs")
	dump("SELECT agent, COUNT(*) FROM agent_analysis GROUP BY agent;",
		"SELECT agent, COUNT(*) AS n FROM agent_analysis GROUP BY agent")
	dump("SELECT status, COUNT(*) FROM agent_analysis GROUP BY status;",
		"SELECT status, COUNT(*) AS n FROM agent_analysis GROUP BY status")
	dump("SELECT market_regime, COUNT(*) FROM scan_runs GROUP BY market_regime;",
		"SELECT market_regime, COUNT(*) AS n FROM scan_runs GROUP BY market_regime")
	dump("SELECT category, COUNT(*) FROM evidence GROUP BY category ORDER BY category;",
		"SELECT category, COUNT(*) AS n FROM evidence GROUP BY category ORDER BY category")
	dump("SELECT source, COUNT(*) FROM decisions GROUP BY source;",
		"SELECT source, COUNT(*) AS n FROM decisions GROUP BY source")
	dump("distinct AI run_uid (one scan = one AI run)",
		"SELECT COUNT(DISTINCT run_uid) AS distinct_run_uids FROM analysis_runs")
	dump("output_json sample", "SELECT substr(output_json,1,80) AS output_json FROM agent_analysis LIMIT 1")
	dump("verdict must be empty (analyst, not judge)",
		"SELECT COALESCE(NULLIF(verdict,''),'(empty)') AS verdict, COUNT(*) AS n FROM agent_analysis GROUP BY 1")

	// Assertions the print alone would not enforce.
	if got := q1("SELECT COUNT(*) FROM analysis_runs"); got != "3" {
		t.Errorf("analysis_runs = %s, want 3 (the non-actionable candidate must not get one)", got)
	}
	if got := q1("SELECT COUNT(DISTINCT run_uid) FROM analysis_runs"); got != "1" {
		t.Errorf("distinct AI run_uids = %s, want 1", got)
	}
	if got := q1("SELECT market_regime FROM scan_runs LIMIT 1"); got != "BULL_PULLBACK" {
		t.Errorf("scan_runs.market_regime = %q", got)
	}
	if got := q1("SELECT COUNT(*) FROM agent_analysis WHERE verdict <> ''"); got != "0" {
		t.Errorf("%s agent rows carry a verdict; the analyst must not decide", got)
	}
	if got := q1("SELECT COUNT(*) FROM decisions WHERE source='AI_JUDGE'"); got != "0" {
		t.Errorf("AI_JUDGE rows exist: %s — M3 must not open that path", got)
	}
	if got := q1("SELECT COUNT(*) FROM outcomes"); got != "0" {
		t.Errorf("outcomes = %s, want 0 in M3", got)
	}
	// No secret may have reached the file.
	raw, _ := os.ReadFile(dbPath)
	for _, bad := range []string{"test-key-not-a-real-secret", "OPENAI_API_KEY", "Authorization", "Bearer "} {
		if len(raw) > 0 && containsBytes(raw, bad) {
			t.Errorf("the database contains %q", bad)
		}
	}
}

func sqlVal(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func containsBytes(hay []byte, needle string) bool {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(hay); i++ {
		if string(hay[i:i+len(n)]) == needle {
			return true
		}
	}
	_ = n
	return false
}
