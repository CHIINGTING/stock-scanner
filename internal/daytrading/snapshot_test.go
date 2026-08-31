package daytrading

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleSnapshot(market, date string, codes ...string) *Snapshot {
	total := int64(1_793_940_000)
	s := &Snapshot{
		SchemaVersion:     SchemaVersion,
		Market:            market,
		AsOfDate:          date,
		Unit:              UnitSharesNTD,
		Source:            SourceTWSE,
		SourceURL:         twseTWTB4UURL,
		FetchedAt:         time.Now().UTC().Truncate(time.Second),
		MarketTotalShares: &total,
	}
	for i, c := range codes {
		s.Stocks = append(s.Stocks, StockDayTrade{
			Code:           c,
			Name:           "N" + c,
			DayTradeShares: int64(1000 * (i + 1)),
			BuyAmount:      int64(100_000 * (i + 1)),
			SellAmount:     int64(100_100 * (i + 1)),
		})
	}
	return s
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := sampleSnapshot(MarketTWSE, "2026-07-14", "2330", "2317")
	if err := Save(dir, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	// The on-disk name must match the institution archive's convention exactly.
	if _, err := os.Stat(filepath.Join(dir, "twse_2026-07-14.json")); err != nil {
		t.Fatalf("expected twse_2026-07-14.json: %v", err)
	}

	out, err := Load(dir, MarketTWSE, "2026-07-14")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Stocks) != 2 || out.Stocks[0].Code != "2330" {
		t.Fatalf("round-trip mismatch: %+v", out.Stocks)
	}
	if out.Stocks[0] != in.Stocks[0] {
		t.Errorf("record not preserved: %+v vs %+v", out.Stocks[0], in.Stocks[0])
	}
	if out.MarketTotalShares == nil || *out.MarketTotalShares != *in.MarketTotalShares {
		t.Errorf("market total not preserved: %v", out.MarketTotalShares)
	}
	if out.SchemaVersion != SchemaVersion || out.Unit != UnitSharesNTD || out.Source != SourceTWSE {
		t.Errorf("provenance not preserved: %+v", out)
	}
}

// A nil market total must survive the round trip AS nil — reloading it as 0 would convert
// a MISSING into a real figure.
func TestSnapshotPreservesMissingMarketTotal(t *testing.T) {
	dir := t.TempDir()
	in := sampleSnapshot(MarketTPEX, "2026-07-14", "3105")
	in.MarketTotalShares = nil
	if err := Save(dir, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := Load(dir, MarketTPEX, "2026-07-14")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.MarketTotalShares != nil {
		t.Errorf("missing total came back as %d", *out.MarketTotalShares)
	}
}

func TestSaveRefusesDowngrade(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sampleSnapshot(MarketTWSE, "2026-07-14", "2330", "2317", "1101")); err != nil {
		t.Fatalf("save full: %v", err)
	}
	// A rerun that fetched fewer stocks (source still mid-publication) is rejected.
	err := Save(dir, sampleSnapshot(MarketTWSE, "2026-07-14", "2330"))
	if !errors.Is(err, ErrWouldDowngrade) {
		t.Fatalf("want ErrWouldDowngrade, got %v", err)
	}
	out, _ := Load(dir, MarketTWSE, "2026-07-14")
	if len(out.Stocks) != 3 {
		t.Fatalf("old snapshot must be preserved, got %d stocks", len(out.Stocks))
	}
	// An equal-or-better rerun overwrites.
	if err := Save(dir, sampleSnapshot(MarketTWSE, "2026-07-14", "2330", "2317", "1101", "2454")); err != nil {
		t.Fatalf("upgrade rerun must succeed: %v", err)
	}
	out, _ = Load(dir, MarketTWSE, "2026-07-14")
	if len(out.Stocks) != 4 {
		t.Fatalf("upgrade not written, got %d stocks", len(out.Stocks))
	}
}

func TestSaveRejectsInvalidSnapshot(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		snap *Snapshot
	}{
		{name: "nil", snap: nil},
		{name: "bad market", snap: &Snapshot{Market: "NYSE", AsOfDate: "2026-07-14"}},
		{name: "bad date", snap: &Snapshot{Market: MarketTWSE, AsOfDate: "20260714"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Save(dir, tc.snap); err == nil {
				t.Fatalf("want error")
			}
		})
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir, MarketTWSE, "2026-07-14") {
		t.Fatalf("nothing saved yet")
	}
	if err := Save(dir, sampleSnapshot(MarketTWSE, "2026-07-14", "2330")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !Exists(dir, MarketTWSE, "2026-07-14") {
		t.Errorf("saved snapshot must be reported as existing")
	}
	if Exists(dir, MarketTPEX, "2026-07-14") {
		t.Errorf("the other market was not saved")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(t.TempDir(), MarketTWSE, "2026-07-14"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}
