package etfflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleSnapshot() *Snapshot {
	return &Snapshot{
		ETFCode:  "00981A",
		ETFName:  "主動式測試 ETF",
		ETFType:  TypeActive,
		AsOfDate: "2026-06-10",
		Holdings: []Holding{
			{StockCode: "2330", StockName: "台積電", HoldingShares: 1_000_000, HoldingRawUnit: unitShare, HoldingWeight: 12.5},
			{StockCode: "2303", StockName: "聯電", HoldingShares: 500_000, HoldingRawUnit: unitLot, HoldingWeight: 6.0},
		},
		SourceURL: "manual: exported by hand",
		FetchedAt: time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC),
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := sampleSnapshot()
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File lands at the deterministic <etf>_<asOfDate>.json path.
	if _, err := os.Stat(filepath.Join(dir, "00981A_2026-06-10.json")); err != nil {
		t.Fatalf("expected snapshot file: %v", err)
	}

	got, err := Load(dir, "00981A", "2026-06-10")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ETFCode != want.ETFCode || got.ETFType != want.ETFType || got.AsOfDate != want.AsOfDate {
		t.Errorf("header mismatch: got %+v", got)
	}
	if len(got.Holdings) != len(want.Holdings) {
		t.Fatalf("holdings len: got %d want %d", len(got.Holdings), len(want.Holdings))
	}
	if got.Holdings[1].HoldingShares != 500_000 || got.Holdings[1].HoldingRawUnit != unitLot {
		t.Errorf("holding[1] round-trip mismatch: %+v", got.Holdings[1])
	}
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt: got %v want %v", got.FetchedAt, want.FetchedAt)
	}
}

func TestLoadMissingFileIsNotExist(t *testing.T) {
	_, err := Load(t.TempDir(), "00919", "2026-06-10")
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want errors.Is os.ErrNotExist, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Snapshot)
		wantErr bool
	}{
		{"ok", func(*Snapshot) {}, false},
		{"missing code", func(s *Snapshot) { s.ETFCode = "" }, true},
		{"bad type", func(s *Snapshot) { s.ETFType = "hybrid" }, true},
		{"bad date", func(s *Snapshot) { s.AsOfDate = "2026/06/10" }, true},
		{"empty date", func(s *Snapshot) { s.AsOfDate = "" }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := sampleSnapshot()
			c.mutate(s)
			err := s.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("Validate() err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestValidateNil(t *testing.T) {
	var s *Snapshot
	if err := s.Validate(); err == nil {
		t.Error("nil snapshot should fail Validate")
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	bad := sampleSnapshot()
	bad.ETFType = "hybrid"
	if err := Save(dir, bad); err == nil {
		t.Fatal("Save should reject an invalid snapshot")
	}
	// Nothing (not even a .tmp) should be left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("invalid Save left files behind: %v", entries)
	}
}
