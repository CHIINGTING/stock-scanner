package etfflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCSV drops a CSV at an arbitrary path under t.TempDir() (no repo fixture).
func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

// Full R8-5a pipeline for one ETF: CSV → CSVFileSource → Ingest → Save → LoadHistory.
// Verifies units are normalised to 股 and the snapshot round-trips off disk.
func ingestRoundTrip(t *testing.T, etf, etfType, csv string) *Snapshot {
	t.Helper()
	csvPath := writeCSV(t, csv)
	snapDir := t.TempDir()
	now := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	snap, err := Ingest(CSVFileSource{Path: csvPath}, SnapshotMeta{
		ETFCode:   etf,
		ETFName:   etf + " name",
		ETFType:   etfType,
		AsOfDate:  "2026-06-10",
		SourceURL: "manual",
	}, now)
	if err != nil {
		t.Fatalf("Ingest %s: %v", etf, err)
	}
	if err := Save(snapDir, snap); err != nil {
		t.Fatalf("Save %s: %v", etf, err)
	}

	hist, err := LoadHistory(snapDir, etf, 10)
	if err != nil {
		t.Fatalf("LoadHistory %s: %v", etf, err)
	}
	if len(hist) != 1 {
		t.Fatalf("LoadHistory %s: got %d snapshots want 1", etf, len(hist))
	}
	return hist[0]
}

func TestE2EActiveETF(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000,張,12.5\n" + // 1000 張 → 1,000,000 股
		"2303,聯電,800000,股,6.0\n"
	got := ingestRoundTrip(t, "00981A", TypeActive, csv)

	if got.ETFType != TypeActive || got.AsOfDate != "2026-06-10" {
		t.Errorf("snapshot meta: %+v", got)
	}
	if len(got.Holdings) != 2 {
		t.Fatalf("holdings: got %d want 2", len(got.Holdings))
	}
	if got.Holdings[0].HoldingShares != 1_000_000 || got.Holdings[0].HoldingRawUnit != "張" {
		t.Errorf("張 normalisation: %+v", got.Holdings[0])
	}
	if got.Holdings[1].HoldingShares != 800_000 {
		t.Errorf("股 passthrough: %+v", got.Holdings[1])
	}
}

func TestE2EPassiveETF(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2412,中華電,500,千股,8.0\n" // 500 千股 → 500,000 股
	got := ingestRoundTrip(t, "00919", TypePassive, csv)

	if got.ETFType != TypePassive {
		t.Errorf("etf_type: got %q want passive", got.ETFType)
	}
	if got.Holdings[0].HoldingShares != 500_000 {
		t.Errorf("千股 normalisation: %+v", got.Holdings[0])
	}
}

func TestE2EUnknownUnitRejected(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000,口,12.5\n" // 口 is not a supported unit
	csvPath := writeCSV(t, csv)
	_, err := Ingest(CSVFileSource{Path: csvPath}, SnapshotMeta{
		ETFCode: "00981A", ETFName: "x", ETFType: TypeActive, AsOfDate: "2026-06-10",
	}, time.Now())
	if err == nil {
		t.Error("unknown unit must be rejected, not guessed")
	}
}

func TestE2EInvalidDateRejected(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000,張,12.5\n"
	csvPath := writeCSV(t, csv)
	_, err := Ingest(CSVFileSource{Path: csvPath}, SnapshotMeta{
		ETFCode: "00981A", ETFName: "x", ETFType: TypeActive, AsOfDate: "2026/06/10", // wrong format
	}, time.Now())
	if err == nil {
		t.Error("invalid as_of_date must be rejected")
	}
}
