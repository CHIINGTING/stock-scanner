package etfflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseHoldingsCSV covers parse concerns directly (shared by both sources).
func TestParseHoldingsCSV(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000000,股,12.5\n" +
		"2303,聯電,500,張,6.0%\n" + // % suffix tolerated; 500 張
		"\n" + // blank row skipped
		"1101,台泥,\"1,200\",千股,1.1\n" // thousands separator tolerated; 1200 千股
	got, err := parseHoldingsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows: got %d want 3 (%+v)", len(got), got)
	}
	if got[1].StockCode != "2303" || got[1].Quantity != 500 || got[1].RawUnit != "張" || got[1].Weight != 6.0 {
		t.Errorf("row[1] (raw, un-normalised): %+v", got[1])
	}
	if got[2].Quantity != 1200 {
		t.Errorf("thousands separator not handled: %+v", got[2])
	}
}

func TestParseHoldingsCSVColumnOrderIndependent(t *testing.T) {
	csv := "holding_weight,holding_raw_unit,holding_shares,stock_name,stock_code\n" +
		"12.5,張,1200,台積電,2330\n"
	got, err := parseHoldingsCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got[0].StockCode != "2330" || got[0].Quantity != 1200 || got[0].RawUnit != "張" {
		t.Errorf("order-independent parse failed: %+v", got[0])
	}
}

func TestParseHoldingsCSVErrors(t *testing.T) {
	cases := map[string]string{
		"missing column": "stock_code,stock_name,holding_shares,holding_weight\n2330,台積電,100,12.5\n",
		"unknown unit":   "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n2330,台積電,100,box,12.5\n",
		"bad number":     "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n2330,台積電,N/A,股,12.5\n",
		"no rows":        "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n\n",
	}
	for name, csv := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHoldingsCSV(strings.NewReader(csv)); err == nil {
				t.Errorf("%s: expected error", name)
			}
		})
	}
}

// CSVFileSource reads an ARBITRARY path (not the ManualCSVSource <dir>/raw convention).
func TestCSVFileSourceReadsArbitraryPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whatever-name.csv") // deliberately not <etf>_<date>.csv
	content := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,500,張,12.5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CSVFileSource{Path: path}.Load("00981A", "2026-06-10")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Quantity != 500 || got[0].RawUnit != "張" {
		t.Errorf("CSVFileSource parse: %+v", got)
	}
}

func TestCSVFileSourceMissingFile(t *testing.T) {
	if _, err := (CSVFileSource{Path: filepath.Join(t.TempDir(), "nope.csv")}).Load("", ""); err == nil {
		t.Error("missing file should error")
	}
}
