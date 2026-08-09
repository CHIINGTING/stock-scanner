package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// writeBench writes a benchmark cache file whose last bar is `last`.
func writeBench(t *testing.T, dir, code string, start time.Time, days int) {
	t.Helper()
	type candle struct {
		Date  time.Time
		Close float64
	}
	var cs []candle
	for i := 0; i < days; i++ {
		cs = append(cs, candle{Date: start.AddDate(0, 0, i), Close: 100 + float64(i)})
	}
	rec := map[string]any{"fetched_at": time.Now(),
		"data": map[string]any{"Symbol": code, "Name": code, "Market": "TW", "Candles": cs}}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, code+"_TW.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The trading-calendar guard. A scheduled weekday job fires on holidays too, and the one
// thing it must never do is invent a trading day that did not happen.
func TestBenchmarkTradedGuard(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	writeBench(t, dir, "0050", start, 10) // 2026-08-01 .. 2026-08-10
	feed := provider.CacheFeed{Dir: dir}

	// A date the benchmark actually printed a close on.
	traded, last, err := benchmarkTraded(feed, model.Benchmark0050, "2026-08-10")
	if err != nil {
		t.Fatalf("benchmarkTraded: %v", err)
	}
	if !traded {
		t.Errorf("2026-08-10 has a bar; want traded=true (last=%s)", last)
	}
	if last != "2026-08-10" {
		t.Errorf("last bar = %s", last)
	}

	// A date AFTER the last bar: either a holiday or the cache has not caught up. Both must
	// refuse, and both must report the last bar so the log says which day it really has.
	traded, last, err = benchmarkTraded(feed, model.Benchmark0050, "2026-08-11")
	if err != nil {
		t.Fatalf("benchmarkTraded: %v", err)
	}
	if traded {
		t.Error("a date with no bar must not count as a trading day")
	}
	if last != "2026-08-10" {
		t.Errorf("the guard must report the real last bar, got %s", last)
	}

	// A date before the series starts is an error, not a silent false.
	if _, _, err := benchmarkTraded(feed, model.Benchmark0050, "2020-01-01"); err == nil {
		t.Error("a date before the series should error rather than pass the guard")
	}
}

// A weekend is the common case the guard exists for: the benchmark simply has no bar, so the
// last bar is Friday's and the requested Saturday is refused.
func TestGuardRefusesWeekend(t *testing.T) {
	dir := t.TempDir()
	// 2026-08-03 is a Monday; ten consecutive calendar days would include a weekend, so write
	// weekdays only to mimic a real exchange series.
	type candle struct {
		Date  time.Time
		Close float64
	}
	var cs []candle
	d := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	for len(cs) < 10 {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			cs = append(cs, candle{Date: d, Close: 100})
		}
		d = d.AddDate(0, 0, 1)
	}
	rec := map[string]any{"fetched_at": time.Now(),
		"data": map[string]any{"Symbol": "0050", "Market": "TW", "Candles": cs}}
	b, _ := json.Marshal(rec)
	os.WriteFile(filepath.Join(dir, "0050_TW.json"), b, 0o644)

	feed := provider.CacheFeed{Dir: dir}
	lastWeekday := cs[len(cs)-1].Date.Format("2006-01-02")

	// The Saturday following the last bar.
	sat := cs[len(cs)-1].Date.AddDate(0, 0, 1)
	for sat.Weekday() != time.Saturday {
		sat = sat.AddDate(0, 0, 1)
	}
	traded, last, err := benchmarkTraded(feed, model.Benchmark0050, sat.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("benchmarkTraded: %v", err)
	}
	if traded {
		t.Errorf("%s is a Saturday and must not be a trading day", sat.Format("2006-01-02"))
	}
	if last != lastWeekday {
		t.Errorf("last bar = %s, want %s", last, lastWeekday)
	}
}
