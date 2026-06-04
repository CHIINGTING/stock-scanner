package fetcher

import (
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }
func ip(v int64) *int64      { return &v }

func TestBackfillLatestFromMeta(t *testing.T) {
	// Existing candles end 2026-06-02 (Taiwan). meta carries the 2026-06-03 session
	// (which Yahoo returned with null OHLCV in the quote arrays).
	jun2 := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC) // 09:00 Taipei
	candles := []Candle{
		{Date: jun2.AddDate(0, 0, -1), Close: 1060},
		{Date: jun2, Close: 988, High: 995, Low: 980, Volume: 4_431_988},
	}
	// meta: 2026-06-03 13:30 Taipei = 05:30 UTC
	jun3meta := time.Date(2026, 6, 3, 5, 30, 0, 0, time.UTC).Unix()

	got := backfillLatestFromMeta(candles, fp(1085), jun3meta,
		fp(1075), fp(1085), fp(1065), ip(1_536_851))

	if len(got) != 3 {
		t.Fatalf("expected 3 candles after backfill, got %d", len(got))
	}
	last := got[len(got)-1]
	if last.Close != 1085 {
		t.Errorf("last close = %v, want 1085", last.Close)
	}
	if tradingDay(last.Date) != 20260603 {
		t.Errorf("last bar trading day = %d, want 20260603", tradingDay(last.Date))
	}
	if last.Volume != 1_536_851 {
		t.Errorf("last volume = %d, want 1536851", last.Volume)
	}
}

func TestBackfillNoOpWhenMetaNotNewer(t *testing.T) {
	jun3 := time.Date(2026, 6, 3, 1, 0, 0, 0, time.UTC)
	candles := []Candle{{Date: jun3, Close: 1085}}
	// meta same trading day as last candle → must not duplicate.
	metaTime := time.Date(2026, 6, 3, 5, 30, 0, 0, time.UTC).Unix()
	got := backfillLatestFromMeta(candles, fp(1085), metaTime, nil, nil, nil, nil)
	if len(got) != 1 {
		t.Errorf("expected no append for same trading day, got %d candles", len(got))
	}
}

func TestBackfillNoOpWhenMetaMissing(t *testing.T) {
	candles := []Candle{{Date: time.Now(), Close: 100}}
	if got := backfillLatestFromMeta(candles, nil, 0, nil, nil, nil, nil); len(got) != 1 {
		t.Errorf("nil meta price should be a no-op")
	}
}
