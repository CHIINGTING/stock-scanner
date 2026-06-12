package etfflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func saveSnap(t *testing.T, dir, etf, asOf string) {
	t.Helper()
	s := &Snapshot{
		ETFCode:   etf,
		ETFType:   TypeActive,
		AsOfDate:  asOf,
		Holdings:  []Holding{{StockCode: "2330", HoldingShares: 1000, HoldingRawUnit: unitShare}},
		FetchedAt: time.Now(),
	}
	if err := Save(dir, s); err != nil {
		t.Fatalf("Save %s: %v", asOf, err)
	}
}

func TestLoadHistoryOrdersAndLimits(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"2026-06-06", "2026-06-10", "2026-06-08", "2026-06-09"} {
		saveSnap(t, dir, "00981A", d)
	}
	got, err := LoadHistory(dir, "00981A", 3)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (maxDays)", len(got))
	}
	// Newest 3, ascending: 08, 09, 10.
	if got[0].AsOfDate != "2026-06-08" || got[2].AsOfDate != "2026-06-10" {
		t.Errorf("order/window wrong: %s..%s", got[0].AsOfDate, got[2].AsOfDate)
	}
}

func TestLoadHistoryMaxDaysZeroReturnsAll(t *testing.T) {
	dir := t.TempDir()
	saveSnap(t, dir, "00919", "2026-06-09")
	saveSnap(t, dir, "00919", "2026-06-10")
	got, _ := LoadHistory(dir, "00919", 0)
	if len(got) != 2 {
		t.Errorf("maxDays=0 should return all, got %d", len(got))
	}
}

func TestLoadHistoryMissingDirIsQuiet(t *testing.T) {
	got, err := LoadHistory(filepath.Join(t.TempDir(), "nope"), "00919", 10)
	if err != nil {
		t.Errorf("missing dir should be (nil,nil), got err %v", err)
	}
	if got != nil {
		t.Errorf("missing dir should yield nil, got %v", got)
	}
}

func TestLoadHistoryFiltersByETFAndSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	saveSnap(t, dir, "00981A", "2026-06-10")
	saveSnap(t, dir, "00919", "2026-06-10") // different ETF, must be excluded
	// A malformed file with this ETF's prefix must be skipped, not fatal.
	if err := os.WriteFile(filepath.Join(dir, "00981A_2026-06-11.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A foreign file that doesn't match the prefix must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadHistory(dir, "00981A", 10)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(got) != 1 || got[0].AsOfDate != "2026-06-10" {
		t.Errorf("expected only the one valid 00981A snapshot, got %+v", got)
	}
}
