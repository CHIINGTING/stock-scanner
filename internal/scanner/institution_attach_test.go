package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/institution"
)

func i64(v int64) *int64 { return &v }

func chip(code string, foreign, trust, dealer int64) institution.StockChip {
	off := foreign + trust + dealer
	return institution.StockChip{
		Code:         code,
		ForeignTotal: institution.Leg{Net: foreign},
		Trust:        institution.Leg{Net: trust},
		DealerTotal:  institution.Leg{Net: dealer},
		// keep sub-legs consistent so reconciliation passes
		ForeignExclDealer: institution.Leg{Net: foreign},
		DealerSelf:        institution.Leg{Net: dealer},
		OfficialTotalNet:  i64(off),
		Reconciled:        true,
	}
}

func saveSnap(t *testing.T, dir, date string, chips ...institution.StockChip) {
	t.Helper()
	s := &institution.Snapshot{
		SchemaVersion: institution.SchemaVersion,
		Market:        institution.MarketTWSE,
		AsOfDate:      date,
		Unit:          institution.UnitShares,
		Source:        institution.SourceTWSE,
		FetchedAt:     time.Now(),
		Stocks:        chips,
	}
	if err := institution.Save(dir, s); err != nil {
		t.Fatalf("save %s: %v", date, err)
	}
}

func candleAt(date string, vol int64) fetcher.Candle {
	d, _ := time.Parse("2006-01-02", date)
	return fetcher.Candle{Date: d, Close: 100, High: 100, Low: 100, Volume: vol}
}

func TestAttachInstitution(t *testing.T) {
	dir := t.TempDir()
	saveSnap(t, dir, "2026-07-13", chip("2330", 1000, 500, 0))
	saveSnap(t, dir, "2026-07-14", chip("2330", 2000, 800, 0))
	loaded, err := institution.LoadHistory(dir, "2026-07-14", 40)
	if err != nil {
		t.Fatal(err)
	}

	entries := []WatchlistEntry{{A: StockAnalysis{Symbol: "2330", Name: "台積電"}}}
	candles := map[string][]fetcher.Candle{
		"2330": {candleAt("2026-07-13", 10000), candleAt("2026-07-14", 20000)},
	}
	AttachInstitution(entries, loaded, candles, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), InstitutionConfig{})

	iv := entries[0].Institution
	if iv == nil {
		t.Fatal("Institution should be attached")
	}
	if iv.Foreign.TodayNet != 2000 {
		t.Errorf("foreign today = %d, want 2000", iv.Foreign.TodayNet)
	}
	if iv.Foreign.ConsecutiveBuyDays != 2 {
		t.Errorf("foreign consecutive buy = %d, want 2", iv.Foreign.ConsecutiveBuyDays)
	}
	// netBuyRatio: 2000 / 20000 * 100 = 10
	if iv.Foreign.NetBuyRatio == nil || *iv.Foreign.NetBuyRatio != 10 {
		t.Errorf("foreign ratio = %v, want 10", iv.Foreign.NetBuyRatio)
	}
	if !iv.Completeness.DataComplete {
		t.Errorf("data should be complete")
	}
}

func TestAttachInstitutionGap(t *testing.T) {
	dir := t.TempDir()
	// 07-13 has data, 07-14 (a trading day per candles) has NO institution record → gap.
	saveSnap(t, dir, "2026-07-13", chip("2330", 1000, 500, 0))
	loaded, _ := institution.LoadHistory(dir, "2026-07-15", 40)

	entries := []WatchlistEntry{{A: StockAnalysis{Symbol: "2330"}}}
	candles := map[string][]fetcher.Candle{
		"2330": {candleAt("2026-07-13", 10000), candleAt("2026-07-14", 20000)},
	}
	AttachInstitution(entries, loaded, candles, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), InstitutionConfig{})

	iv := entries[0].Institution
	if iv == nil {
		t.Fatal("Institution should attach (07-13 record exists)")
	}
	// today (07-14) missing → DataComplete false, streak incomplete, ratio nil.
	if iv.Completeness.DataComplete {
		t.Errorf("today missing → DataComplete must be false")
	}
	if iv.Foreign.StreakComplete {
		t.Errorf("today missing → StreakComplete must be false")
	}
	if iv.Foreign.NetBuyRatio != nil {
		t.Errorf("today missing → ratio must be nil")
	}
}

func TestAttachInstitutionNoData(t *testing.T) {
	dir := t.TempDir()
	saveSnap(t, dir, "2026-07-14", chip("2330", 1000, 500, 0))
	loaded, _ := institution.LoadHistory(dir, "2026-07-14", 40)

	// Entry for a code with no institution records → left nil (graceful).
	entries := []WatchlistEntry{{A: StockAnalysis{Symbol: "9999"}}}
	candles := map[string][]fetcher.Candle{"9999": {candleAt("2026-07-14", 5000)}}
	AttachInstitution(entries, loaded, candles, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), InstitutionConfig{})
	if entries[0].Institution != nil {
		t.Errorf("code with no records must stay nil")
	}
}

func TestAttachInstitutionNilLoaded(t *testing.T) {
	entries := []WatchlistEntry{{A: StockAnalysis{Symbol: "2330"}}}
	AttachInstitution(entries, nil, nil, time.Now(), InstitutionConfig{}) // must not panic
	if entries[0].Institution != nil {
		t.Errorf("nil loaded → nil institution")
	}
}
