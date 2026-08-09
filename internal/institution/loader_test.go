package institution

import "testing"

func saveAt(t *testing.T, dir, market, date string, codes ...string) {
	t.Helper()
	s := sampleSnapshot(market, codes...)
	s.Market = market
	s.AsOfDate = date
	if err := Save(dir, s); err != nil {
		t.Fatalf("save %s %s: %v", market, date, err)
	}
}

func TestLoadHistoryIndex(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-13", "2330")
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330")
	saveAt(t, dir, MarketTPEX, "2026-07-14", "3105")

	l, err := LoadHistory(dir, "2026-07-14", 10)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(l.Dates()) != 2 {
		t.Fatalf("dates = %v, want 2", l.Dates())
	}
	if _, ok := l.Chip("2330", "2026-07-13"); !ok {
		t.Errorf("2330 on 07-13 should exist")
	}
	if _, ok := l.Chip("3105", "2026-07-14"); !ok {
		t.Errorf("3105 (TPEx) on 07-14 should exist")
	}
	if _, ok := l.Chip("2330", "2026-07-12"); ok {
		t.Errorf("no record expected for a non-loaded date")
	}
}

func TestLoadHistoryEndDateCap(t *testing.T) {
	dir := t.TempDir()
	saveAt(t, dir, MarketTWSE, "2026-07-13", "2330")
	saveAt(t, dir, MarketTWSE, "2026-07-14", "2330")
	l, _ := LoadHistory(dir, "2026-07-13", 10) // endDate excludes 07-14
	if _, ok := l.Chip("2330", "2026-07-14"); ok {
		t.Errorf("07-14 is after endDate and must be excluded")
	}
}

func TestLoadHistoryMissingDir(t *testing.T) {
	l, err := LoadHistory("/no/such/dir/xyz", "2026-07-14", 10)
	if err != nil {
		t.Fatalf("missing dir must degrade gracefully, got %v", err)
	}
	if len(l.Dates()) != 0 {
		t.Errorf("expected empty index")
	}
}
