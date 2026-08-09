package institution

import (
	"errors"
	"testing"
	"time"
)

func sampleSnapshot(market string, codes ...string) *Snapshot {
	s := &Snapshot{
		SchemaVersion: SchemaVersion,
		Market:        market,
		AsOfDate:      "2026-07-14",
		Unit:          UnitShares,
		Source:        SourceTWSE,
		FetchedAt:     time.Now(),
	}
	for _, c := range codes {
		s.Stocks = append(s.Stocks, StockChip{Code: c, ForeignTotal: Leg{Net: 1000}, Trust: Leg{Net: 500}, DealerTotal: Leg{Net: 0}, OfficialTotalNet: ptr(1500), Reconciled: true})
	}
	return s
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := sampleSnapshot(MarketTWSE, "2330", "2317")
	if err := Save(dir, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := Load(dir, MarketTWSE, "2026-07-14")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Stocks) != 2 || out.Stocks[0].Code != "2330" {
		t.Fatalf("round-trip mismatch: %+v", out.Stocks)
	}
	if out.Stocks[0].ForeignTotal.Net != 1000 {
		t.Errorf("net not preserved")
	}
}

func TestSaveNonDowngrade(t *testing.T) {
	dir := t.TempDir()
	full := sampleSnapshot(MarketTWSE, "2330", "2317", "1101")
	if err := Save(dir, full); err != nil {
		t.Fatalf("save full: %v", err)
	}
	// A rerun that fetched fewer stocks must be rejected, old file kept.
	partial := sampleSnapshot(MarketTWSE, "2330")
	err := Save(dir, partial)
	if !errors.Is(err, ErrWouldDowngrade) {
		t.Fatalf("want ErrWouldDowngrade, got %v", err)
	}
	out, _ := Load(dir, MarketTWSE, "2026-07-14")
	if len(out.Stocks) != 3 {
		t.Fatalf("old snapshot must be preserved, got %d stocks", len(out.Stocks))
	}
	// An equal-or-better rerun overwrites.
	better := sampleSnapshot(MarketTWSE, "2330", "2317", "1101", "2454")
	if err := Save(dir, better); err != nil {
		t.Fatalf("save better: %v", err)
	}
	out, _ = Load(dir, MarketTWSE, "2026-07-14")
	if len(out.Stocks) != 4 {
		t.Fatalf("better snapshot must overwrite, got %d", len(out.Stocks))
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir(), MarketTWSE, "2026-07-14")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
