package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

func baseOpts(input, dir string) ingestOpts {
	return ingestOpts{
		etfCode:     "00981A",
		etfName:     "統一台股增長主動式 ETF",
		etfType:     "active",
		date:        "2026-06-10",
		input:       input,
		snapshotDir: dir,
		sourceURL:   "manual",
	}
}

func writeInputCSV(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "00981A_20260610.csv")
	content := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000,張,12.5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunMissingFlags(t *testing.T) {
	// Every required flag empty → error names all of them.
	err := run(ingestOpts{})
	if err == nil || !strings.Contains(err.Error(), "missing required flag") {
		t.Fatalf("expected missing-flag error, got %v", err)
	}
	for _, f := range []string{"--etf-code", "--etf-name", "--etf-type", "--date", "--input"} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error should mention %s: %v", f, err)
		}
	}
}

func TestRunBadETFType(t *testing.T) {
	o := baseOpts(writeInputCSV(t), t.TempDir())
	o.etfType = "hybrid"
	if err := run(o); err == nil {
		t.Error("invalid --etf-type must error")
	}
}

func TestRunInvalidDate(t *testing.T) {
	o := baseOpts(writeInputCSV(t), t.TempDir())
	o.date = "2026/06/10"
	if err := run(o); err == nil {
		t.Error("invalid --date must error")
	}
}

func TestRunMissingInputFile(t *testing.T) {
	o := baseOpts(filepath.Join(t.TempDir(), "nope.csv"), t.TempDir())
	if err := run(o); err == nil {
		t.Error("missing --input file must error")
	}
}

// Happy path: run() writes a snapshot LoadHistory can read back (end-to-end via CLI).
func TestRunWritesReadableSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := run(baseOpts(writeInputCSV(t), dir)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "00981A_2026-06-10.json")); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	hist, err := etfflow.LoadHistory(dir, "00981A", 10)
	if err != nil || len(hist) != 1 {
		t.Fatalf("LoadHistory: hist=%d err=%v", len(hist), err)
	}
	if hist[0].Holdings[0].HoldingShares != 1_000_000 { // 1000 張 normalised
		t.Errorf("normalisation via CLI failed: %+v", hist[0].Holdings[0])
	}
}
