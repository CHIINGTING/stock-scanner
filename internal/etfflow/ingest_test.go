package etfflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeToShares(t *testing.T) {
	cases := []struct {
		qty     float64
		unit    string
		want    int64
		wantErr bool
	}{
		{1000, unitShare, 1000, false},
		{500, unitLot, 500_000, false},      // 500 張 = 500,000 股
		{500, unitThousand, 500_000, false}, // 500 千股 = 500,000 股
		{0, unitShare, 0, false},
		{12, " 張 ", 12_000, false}, // surrounding spaces tolerated
		{100, "", 0, true},         // empty unit rejected
		{100, "lots", 0, true},     // unknown unit rejected
	}
	for _, c := range cases {
		got, err := NormalizeToShares(c.qty, c.unit)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeToShares(%v,%q) err=%v wantErr=%v", c.qty, c.unit, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("NormalizeToShares(%v,%q)=%d want %d", c.qty, c.unit, got, c.want)
		}
	}
}

// writeRawCSV creates <dir>/raw/<etf>_<asOfDate>.csv with the given content and
// returns the snapshot root dir. No fixture is committed — it lives in t.TempDir().
func writeRawCSV(t *testing.T, etf, asOf, content string) string {
	t.Helper()
	dir := t.TempDir()
	rawDir := filepath.Join(dir, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := etf + "_" + asOf + ".csv"
	if err := os.WriteFile(filepath.Join(rawDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return dir
}

func TestManualCSVSourceLoad(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000000,股,12.5\n" +
		"2303,聯電,500,張,6.0%\n" + // weight may carry a % suffix
		"\n" + // blank trailing row tolerated
		"1101,台泥,\"1,200\",千股,1.1\n" // thousands separator tolerated
	dir := writeRawCSV(t, "00981A", "2026-06-10", csv)

	got, err := ManualCSVSource{Dir: dir}.Load("00981A", "2026-06-10")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows: got %d want 3 (%+v)", len(got), got)
	}
	if got[1].StockCode != "2303" || got[1].Quantity != 500 || got[1].RawUnit != "張" || got[1].Weight != 6.0 {
		t.Errorf("row[1] mismatch: %+v", got[1])
	}
	if got[2].Quantity != 1200 {
		t.Errorf("thousands separator not handled: %+v", got[2])
	}
}

func TestManualCSVSourceColumnOrderIndependent(t *testing.T) {
	csv := "holding_weight,holding_raw_unit,holding_shares,stock_name,stock_code\n" +
		"12.5,股,1000000,台積電,2330\n"
	dir := writeRawCSV(t, "00919", "2026-06-10", csv)
	got, err := ManualCSVSource{Dir: dir}.Load("00919", "2026-06-10")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got[0].StockCode != "2330" || got[0].Quantity != 1_000_000 {
		t.Errorf("order-independent parse failed: %+v", got[0])
	}
}

func TestManualCSVSourceErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := (ManualCSVSource{Dir: t.TempDir()}).Load("00919", "2026-06-10"); err == nil {
			t.Error("expected error for missing csv")
		}
	})
	t.Run("missing required column", func(t *testing.T) {
		dir := writeRawCSV(t, "00919", "2026-06-10",
			"stock_code,stock_name,holding_shares,holding_weight\n2330,台積電,100,12.5\n")
		if _, err := (ManualCSVSource{Dir: dir}).Load("00919", "2026-06-10"); err == nil {
			t.Error("expected error for missing holding_raw_unit column")
		}
	})
	t.Run("bad unit", func(t *testing.T) {
		dir := writeRawCSV(t, "00919", "2026-06-10",
			"stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n2330,台積電,100,box,12.5\n")
		if _, err := (ManualCSVSource{Dir: dir}).Load("00919", "2026-06-10"); err == nil {
			t.Error("expected error for unknown unit")
		}
	})
	t.Run("bad quantity", func(t *testing.T) {
		dir := writeRawCSV(t, "00919", "2026-06-10",
			"stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n2330,台積電,N/A,股,12.5\n")
		if _, err := (ManualCSVSource{Dir: dir}).Load("00919", "2026-06-10"); err == nil {
			t.Error("expected error for non-numeric holding_shares")
		}
	})
	t.Run("no rows", func(t *testing.T) {
		dir := writeRawCSV(t, "00919", "2026-06-10",
			"stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n\n")
		if _, err := (ManualCSVSource{Dir: dir}).Load("00919", "2026-06-10"); err == nil {
			t.Error("expected error for header-only csv")
		}
	})
}

func TestIngestNormalisesAndStamps(t *testing.T) {
	csv := "stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n" +
		"2330,台積電,1000000,股,12.5\n" +
		"2303,聯電,500,張,6.0\n"
	dir := writeRawCSV(t, "00981A", "2026-06-10", csv)
	now := time.Date(2026, 6, 11, 8, 30, 0, 0, time.UTC)

	snap, err := Ingest(ManualCSVSource{Dir: dir}, SnapshotMeta{
		ETFCode:   "00981A",
		ETFName:   "主動式測試 ETF",
		ETFType:   TypeActive,
		AsOfDate:  "2026-06-10",
		SourceURL: "manual",
	}, now)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if snap.ETFType != TypeActive || snap.AsOfDate != "2026-06-10" {
		t.Errorf("meta not stamped: %+v", snap)
	}
	if !snap.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt: got %v want %v", snap.FetchedAt, now)
	}
	// 500 張 must be normalised to 500,000 股 while keeping the raw unit.
	if snap.Holdings[1].HoldingShares != 500_000 || snap.Holdings[1].HoldingRawUnit != "張" {
		t.Errorf("normalisation failed: %+v", snap.Holdings[1])
	}
}

func TestIngestRejectsBadType(t *testing.T) {
	dir := writeRawCSV(t, "00919", "2026-06-10",
		"stock_code,stock_name,holding_shares,holding_raw_unit,holding_weight\n2330,台積電,100,股,1\n")
	_, err := Ingest(ManualCSVSource{Dir: dir}, SnapshotMeta{
		ETFCode:  "00919",
		ETFType:  "hybrid",
		AsOfDate: "2026-06-10",
	}, time.Now())
	if err == nil {
		t.Error("Ingest should reject invalid etf_type before loading")
	}
}
