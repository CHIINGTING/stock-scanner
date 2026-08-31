package daytrading

import (
	"os"
	"path/filepath"
	"testing"
)

func saveAt(t *testing.T, dir, market, date string, codes ...string) {
	t.Helper()
	if err := Save(dir, sampleSnapshot(market, date, codes...)); err != nil {
		t.Fatalf("save %s %s: %v", market, date, err)
	}
}

func TestLoadRangeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-13", "2330")
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330", "2317")
	saveAt(t, dir, MarketTPEX, "2026-07-14", "3105")
	saveAt(t, dir, MarketTWSE, "2026-07-15", "2330")

	l, err := LoadRange(dir, "2026-07-13", "2026-07-14")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := l.Dates(); len(got) != 2 || got[0] != "2026-07-13" || got[1] != "2026-07-14" {
		t.Fatalf("dates = %v, want the two in-range dates ascending", got)
	}
	r, ok := l.Record("2330", "2026-07-13")
	if !ok {
		t.Fatalf("2330 on 07-13 should exist")
	}
	if r.DayTradeShares != 1000 || r.BuyAmount != 100000 {
		t.Errorf("record not preserved through the loader: %+v", r)
	}
	if _, ok := l.Record("3105", "2026-07-14"); !ok {
		t.Errorf("3105 (TPEx) on 07-14 should exist — both markets are indexed together")
	}
	if _, ok := l.Record("2330", "2026-07-15"); ok {
		t.Errorf("07-15 is past the range end and must be excluded")
	}
	if _, ok := l.Record("2330", "2026-07-12"); ok {
		t.Errorf("no record expected for a date with no snapshot")
	}
	// A code absent on a date is MISSING, not a zero day-trading day.
	if _, ok := l.Record("2317", "2026-07-13"); ok {
		t.Errorf("2317 has no 07-13 snapshot row and must be absent")
	}
	if l.Codes() != 3 {
		t.Errorf("Codes() = %d, want 3", l.Codes())
	}
}

func TestLoadRangeOpenBounds(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-13", "2330")
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330")

	all, _ := LoadRange(dir, "", "")
	if len(all.Dates()) != 2 {
		t.Errorf("open bounds must load everything, got %v", all.Dates())
	}
	fromOnly, _ := LoadRange(dir, "2026-07-14", "")
	if len(fromOnly.Dates()) != 1 || fromOnly.Dates()[0] != "2026-07-14" {
		t.Errorf("open end bound: got %v", fromOnly.Dates())
	}
}

func TestLoadHistoryCapsToMaxDays(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"2026-07-13", "2026-07-14", "2026-07-15"} {
		saveAt(t, dir, MarketTWSE, d, "2330")
	}
	l, err := LoadHistory(dir, "2026-07-15", 2)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := l.Dates(); len(got) != 2 || got[0] != "2026-07-14" {
		t.Fatalf("dates = %v, want the 2 most recent", got)
	}
	// The index must agree with Dates(): a trimmed-away date must not still resolve.
	if _, ok := l.Record("2330", "2026-07-13"); ok {
		t.Errorf("2026-07-13 was trimmed by maxDays and must not be in the index")
	}
	if _, ok := l.Record("2330", "2026-07-15"); !ok {
		t.Errorf("2026-07-15 should be indexed")
	}
}

func TestLoadHistoryEndDateCap(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-13", "2330")
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330")
	l, _ := LoadHistory(dir, "2026-07-13", 10)
	if _, ok := l.Record("2330", "2026-07-14"); ok {
		t.Errorf("07-14 is after endDate and must be excluded")
	}
}

// A read-side loader must degrade, never abort a study.
func TestLoadRangeMissingDir(t *testing.T) {
	l, err := LoadRange(filepath.Join(t.TempDir(), "nope"), "", "")
	if err != nil {
		t.Fatalf("missing dir must degrade gracefully, got %v", err)
	}
	if len(l.Dates()) != 0 || l.Codes() != 0 {
		t.Errorf("expected an empty index")
	}
}

func TestLoadRangeIgnoresForeignAndMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tpex_2026-07-14.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := LoadRange(dir, "", "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(l.Dates()) != 1 {
		t.Fatalf("dates = %v", l.Dates())
	}
	if _, ok := l.Record("2330", "2026-07-14"); !ok {
		t.Errorf("the healthy market must still be indexed alongside a malformed one")
	}
}

// A nil receiver answers like an empty index instead of panicking.
func TestNilLoadedIsSafe(t *testing.T) {
	var l *Loaded
	if _, ok := l.Record("2330", "2026-07-14"); ok {
		t.Errorf("nil index must report absent")
	}
	if l.Codes() != 0 || l.Dates() != nil {
		t.Errorf("nil index must be empty")
	}
}
