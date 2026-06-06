package r6backtest

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 6. CSV schema is fixed and matches the documented field order.
func TestCSVSchemaFixed(t *testing.T) {
	want := []string{
		"setup_name", "stock_code", "stock_name", "is_watchlist_member",
		"entry_date", "entry_price",
		"exit_5d_return", "exit_10d_return", "exit_20d_return", "exit_60d_return",
		"max_drawdown_after_entry", "hit_stop", "stop_reason",
		"rs_rank_at_entry", "distance_from_52w_high", "pullback_pct_from_recent_high",
		"ma20_distance_pct", "ma60_distance_pct",
		"vcp_valid", "momentum_flow", "mtf_signal", "sector", "bucket",
	}
	got := CSVHeader()
	if len(got) != len(want) {
		t.Fatalf("header length: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("header[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// 7. WriteCSV always writes the header (even with zero trades), and a row count
//    matches the trades.
func TestWriteCSVHeaderAndRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.csv")
	if err := WriteCSV(p, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("empty trades → header only, got %d lines", len(lines))
	}
	tr := Trade{SetupName: "FIXED", StockCode: "2330", EntryDate: time.Now(),
		EntryPrice: 100, Exit5dReturn: 1.2, Exit60dReturn: math.NaN()}
	if err := WriteCSV(p, []Trade{tr}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("1 trade → header + 1 row, got %d lines", len(lines))
	}
	// NaN 60d return must serialize as empty field.
	if !strings.Contains(lines[1], ",100.00,") {
		t.Errorf("entry price not serialized: %q", lines[1])
	}
}

// 8. ForceLowConfidence pins confidence to LOW regardless of sample size.
func TestForceLowConfidence(t *testing.T) {
	var trades []Trade
	for i := 0; i < 100; i++ {
		trades = append(trades, Trade{Exit5dReturn: 1, Exit20dReturn: 1})
	}
	p := DefaultParams()
	p.ForceLowConfidence = true
	st := ComputeStats("D", 0, trades, []int{5, 20}, p)
	if st.Confidence != "LOW" {
		t.Errorf("forced LOW expected, got %s (n=%d)", st.Confidence, st.SampleCount)
	}
	// without the flag, 100 samples → HIGH.
	p.ForceLowConfidence = false
	if st2 := ComputeStats("A", 0, trades, []int{5, 20}, p); st2.Confidence != "HIGH" {
		t.Errorf("100 samples → HIGH expected, got %s", st2.Confidence)
	}
}

// 9. vocabulary guard: no forbidden order/trade tokens in any rendered output.
func TestNoForbiddenTokens(t *testing.T) {
	dir := t.TempDir()
	tr := Trade{SetupName: "PULLBACK_MA20", StockCode: "2330", StockName: "台積電",
		EntryDate: time.Now(), EntryPrice: 100, StopReason: "BREAK_MA60",
		MomentumFlow: "MOMENTUM_BUILDING", MTFSignal: "STRONG"}
	csv := filepath.Join(dir, "t.csv")
	md := filepath.Join(dir, "t.md")
	if err := WriteCSV(csv, []Trade{tr}); err != nil {
		t.Fatal(err)
	}
	st := ComputeStats("PULLBACK_MA20", 0, []Trade{tr}, []int{5, 20}, DefaultParams())
	if err := WriteMarkdown(md, "R6 Pullback Backtest", []string{"universe: 1"}, []SetupStat{st}, []int{5, 20}); err != nil {
		t.Fatal(err)
	}
	for _, fp := range []string{csv, md} {
		b, _ := os.ReadFile(fp)
		up := strings.ToUpper(string(b))
		for _, tok := range forbiddenTokens {
			if strings.Contains(up, tok) {
				t.Errorf("%s contains forbidden token %q", filepath.Base(fp), tok)
			}
		}
	}
}
