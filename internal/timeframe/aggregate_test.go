package timeframe

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// mkDay builds a daily candle dated at Taipei midnight (matches ToWeekly's bucketing).
func mkDay(date string, o, h, l, c float64, v int64) fetcher.Candle {
	t, err := time.ParseInLocation("2006-01-02", date, taipei)
	if err != nil {
		panic(err)
	}
	return fetcher.Candle{Date: t, Open: o, High: h, Low: l, Close: c, Volume: v, AdjClose: c}
}

// 1. Two full weeks aggregate with correct OHLCV.
func TestToWeeklyBasic(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2026-01-05", 100, 105, 99, 101, 10), // Mon (ISO 2026-W2)
		mkDay("2026-01-06", 101, 110, 100, 108, 20),
		mkDay("2026-01-07", 108, 112, 104, 106, 15),
		mkDay("2026-01-08", 106, 107, 95, 100, 25),
		mkDay("2026-01-09", 100, 103, 98, 102, 30), // Fri
		mkDay("2026-01-12", 102, 104, 101, 103, 5), // next Mon (W3)
		mkDay("2026-01-13", 103, 109, 102, 108, 6),
		mkDay("2026-01-14", 108, 111, 107, 110, 7),
		mkDay("2026-01-15", 110, 113, 106, 109, 8),
		mkDay("2026-01-16", 109, 115, 108, 114, 9), // Fri
	}
	w := ToWeekly(daily)
	if len(w) != 2 {
		t.Fatalf("expected 2 weeks, got %d", len(w))
	}
	a := w[0]
	if a.Open != 100 || a.Close != 102 || a.High != 112 || a.Low != 95 || a.Volume != 100 || a.Days != 5 {
		t.Errorf("week A wrong: O=%.0f C=%.0f H=%.0f L=%.0f V=%d Days=%d", a.Open, a.Close, a.High, a.Low, a.Volume, a.Days)
	}
	if a.AdjClose != 102 {
		t.Errorf("week A AdjClose=%.0f want 102 (last day)", a.AdjClose)
	}
	if a.Partial {
		t.Error("interior/complete week A must not be Partial")
	}
	b := w[1]
	if b.Open != 102 || b.Close != 114 || b.High != 115 || b.Low != 101 || b.Volume != 35 || b.Days != 5 {
		t.Errorf("week B wrong: O=%.0f C=%.0f H=%.0f L=%.0f V=%d Days=%d", b.Open, b.Close, b.High, b.Low, b.Volume, b.Days)
	}
	if b.Partial { // ends Friday → complete
		t.Error("week B ends Friday → must not be Partial")
	}
	if a.ISOWeek == b.ISOWeek {
		t.Error("two distinct weeks should have distinct ISOWeek")
	}
	// oldest-first
	if !w[0].Date.Before(w[1].Date) {
		t.Error("output must be oldest-first")
	}
}

// 2. Friday → next Monday splits into two weeks.
func TestToWeeklyCrossWeekBoundary(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2026-01-09", 100, 100, 100, 100, 1), // Fri
		mkDay("2026-01-12", 101, 101, 101, 101, 1), // Mon
	}
	w := ToWeekly(daily)
	if len(w) != 2 {
		t.Fatalf("Fri→Mon should be 2 weeks, got %d", len(w))
	}
}

// 3. Cross-YEAR (the key case): 2025-12-29..2026-01-02 are all ISO 2026-W1 → one bar.
func TestToWeeklyCrossYearSameISOWeek(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2025-12-29", 50, 55, 49, 52, 1), // Mon
		mkDay("2025-12-30", 52, 53, 50, 51, 1),
		mkDay("2025-12-31", 51, 58, 51, 57, 1),
		mkDay("2026-01-01", 57, 60, 56, 59, 1), // Thu (holiday in reality, but tests ISO grouping)
		mkDay("2026-01-02", 59, 61, 58, 60, 1), // Fri
	}
	w := ToWeekly(daily)
	if len(w) != 1 {
		t.Fatalf("12/29..1/2 must aggregate into ONE ISO week, got %d", len(w))
	}
	if w[0].ISOYear != 2026 || w[0].ISOWeek != 1 {
		t.Errorf("expected ISO 2026-W1, got %d-W%d", w[0].ISOYear, w[0].ISOWeek)
	}
	if w[0].Days != 5 || w[0].Open != 50 || w[0].Close != 60 || w[0].High != 61 || w[0].Low != 49 {
		t.Errorf("aggregation wrong across year boundary: %+v", w[0].Candle)
	}
}

// 4. Late-Dec vs early-Jan that fall in different ISO weeks split into two bars.
func TestToWeeklyCrossYearDifferentISOWeek(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2025-12-22", 10, 10, 10, 10, 1), // Mon (ISO 2025-W52)
		mkDay("2025-12-26", 11, 11, 11, 11, 1), // Fri
		mkDay("2025-12-29", 12, 12, 12, 12, 1), // Mon (ISO 2026-W1)
		mkDay("2026-01-02", 13, 13, 13, 13, 1), // Fri
	}
	w := ToWeekly(daily)
	if len(w) != 2 {
		t.Fatalf("expected 2 distinct ISO weeks, got %d", len(w))
	}
	if w[0].ISOYear == w[1].ISOYear && w[0].ISOWeek == w[1].ISOWeek {
		t.Error("the two weeks must have different (ISOYear, ISOWeek) keys")
	}
}

// 5. Last week ending mid-week is Partial; the preceding full week is not.
func TestToWeeklyPartialLastWeek(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2026-01-05", 100, 100, 100, 100, 1), // Mon
		mkDay("2026-01-06", 100, 100, 100, 100, 1),
		mkDay("2026-01-07", 100, 100, 100, 100, 1),
		mkDay("2026-01-08", 100, 100, 100, 100, 1),
		mkDay("2026-01-09", 100, 100, 100, 100, 1), // Fri → complete
		mkDay("2026-01-12", 100, 100, 100, 100, 1), // Mon
		mkDay("2026-01-13", 100, 100, 100, 100, 1),
		mkDay("2026-01-14", 100, 100, 100, 100, 1), // Wed → last day, incomplete
	}
	w := ToWeekly(daily)
	if len(w) != 2 {
		t.Fatalf("expected 2 weeks, got %d", len(w))
	}
	if w[0].Partial {
		t.Error("first (complete) week must not be Partial")
	}
	if !w[1].Partial || w[1].Days != 3 {
		t.Errorf("last week (ends Wed) should be Partial with Days=3, got Partial=%v Days=%d", w[1].Partial, w[1].Days)
	}
}

// 6. A holiday-short INTERIOR week (4 days, not last) is NOT Partial — Days conveys it.
func TestToWeeklyInteriorShortWeekNotPartial(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2026-01-05", 100, 100, 100, 100, 1), // Mon
		mkDay("2026-01-06", 100, 100, 100, 100, 1),
		mkDay("2026-01-07", 100, 100, 100, 100, 1),
		mkDay("2026-01-08", 100, 100, 100, 100, 1), // Thu (Fri 01-09 "holiday" → only 4 days)
		mkDay("2026-01-12", 100, 100, 100, 100, 1), // Mon (next week makes prior non-last)
		mkDay("2026-01-16", 100, 100, 100, 100, 1), // Fri
	}
	w := ToWeekly(daily)
	if len(w) != 2 {
		t.Fatalf("expected 2 weeks, got %d", len(w))
	}
	if w[0].Partial {
		t.Error("interior short week must NOT be Partial (use Days)")
	}
	if w[0].Days != 4 {
		t.Errorf("interior short week Days=%d want 4", w[0].Days)
	}
}

// 7. Empty and single-day inputs.
func TestToWeeklyEdgeCases(t *testing.T) {
	if w := ToWeekly(nil); len(w) != 0 {
		t.Errorf("empty input → empty output, got %d", len(w))
	}
	// single Monday bar → one Partial week (ends Mon < Fri).
	w := ToWeekly([]fetcher.Candle{mkDay("2026-01-05", 100, 100, 100, 100, 1)})
	if len(w) != 1 || !w[0].Partial || w[0].Days != 1 {
		t.Errorf("single Mon bar: want 1 partial week Days=1, got len=%d partial=%v days=%d", len(w), w[0].Partial, w[0].Days)
	}
}

// 10. WeeklyCandles strips back to plain candles with matching OHLCV.
func TestWeeklyCandles(t *testing.T) {
	daily := []fetcher.Candle{
		mkDay("2026-01-05", 100, 105, 99, 101, 10),
		mkDay("2026-01-09", 101, 110, 98, 108, 20), // same week (Mon..Fri)
	}
	w := ToWeekly(daily)
	cs := WeeklyCandles(w)
	if len(cs) != len(w) {
		t.Fatalf("WeeklyCandles len %d != %d", len(cs), len(w))
	}
	if cs[0].Open != w[0].Open || cs[0].Close != w[0].Close || cs[0].High != w[0].High ||
		cs[0].Low != w[0].Low || cs[0].Volume != w[0].Volume || cs[0].AdjClose != w[0].AdjClose {
		t.Error("WeeklyCandles OHLCV must match the weekly bar")
	}
}
