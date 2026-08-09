package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCache writes a file in the exact on-disk shape internal/fetcher's dataCache uses, so
// these tests break if that format ever changes.
func writeCache(t *testing.T, dir, code, market string, start string, closes []float64) {
	t.Helper()
	type candle struct {
		Date   time.Time
		Open   float64
		High   float64
		Low    float64
		Close  float64
		Volume int64
	}
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	var cs []candle
	for i, c := range closes {
		cs = append(cs, candle{Date: d.AddDate(0, 0, i), Close: c, Open: c, High: c, Low: c, Volume: 1})
	}
	rec := map[string]any{
		"fetched_at": time.Now(),
		"data":       map[string]any{"Symbol": code, "Name": code, "Market": market, "Candles": cs},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, code+"_"+market+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ramp(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 100 + 0.5*float64(i)
	}
	return out
}

func TestBenchmarkLoadsAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "0050", "TW", "2026-01-01", ramp(200))

	f := CacheFeed{Dir: dir}
	bars, err := f.Benchmark("0050", "")
	if err != nil {
		t.Fatalf("Benchmark: %v", err)
	}
	if len(bars) != 200 {
		t.Fatalf("got %d bars, want 200", len(bars))
	}
	if bars[0].Date != "2026-01-01" {
		t.Errorf("first bar %s, want 2026-01-01", bars[0].Date)
	}
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Date >= bars[i].Date {
			t.Fatalf("bars not ascending at %d: %s >= %s", i, bars[i-1].Date, bars[i].Date)
		}
	}
	if _, err := f.Benchmark("9999", ""); err == nil {
		t.Error("an unknown symbol should error")
	}
}

// The look-ahead guard: replaying a past date must not see later bars.
func TestBenchmarkAsOfTrims(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "0050", "TW", "2026-01-01", ramp(200))
	f := CacheFeed{Dir: dir}

	bars, err := f.Benchmark("0050", "2026-01-10")
	if err != nil {
		t.Fatal(err)
	}
	if got := bars[len(bars)-1].Date; got != "2026-01-10" {
		t.Errorf("last bar %s, want 2026-01-10", got)
	}
	if len(bars) != 10 {
		t.Errorf("got %d bars, want 10", len(bars))
	}
	// A non-trading asOf falls back to the most recent earlier bar, never forward.
	bars, err = f.Benchmark("0050", "2026-01-10T23")
	if err != nil {
		t.Fatal(err)
	}
	if got := bars[len(bars)-1].Date; got > "2026-01-10T23" {
		t.Errorf("trim went past asOf: %s", got)
	}
	if _, err := f.Benchmark("0050", "2020-01-01"); err == nil {
		t.Error("an asOf before the series starts should error, not return everything")
	}
}

func TestUniverseAlignsOntoSharedAxis(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "1111", "TW", "2026-01-01", ramp(200)) // full history
	writeCache(t, dir, "2222", "TW", "2026-02-01", ramp(160)) // listed later
	writeCache(t, dir, "3333", "TW", "2026-01-01", ramp(50))  // too short, dropped

	u, err := CacheFeed{Dir: dir, MinBars: 120}.Universe("")
	if err != nil {
		t.Fatalf("Universe: %v", err)
	}
	dates, closes := u.Dates, u.Closes
	if len(closes) != 2 {
		t.Fatalf("got %d symbols, want 2 (the 50-bar one must be dropped)", len(closes))
	}
	for i, s := range closes {
		if len(s) != len(dates) {
			t.Fatalf("symbol %d has %d closes for %d dates", i, len(s), len(dates))
		}
	}
	// The late-listing symbol must carry zeros before it listed — "no bar", not a price.
	var late []float64
	for _, s := range closes {
		if s[0] == 0 {
			late = s
		}
	}
	if late == nil {
		t.Fatal("expected one symbol with no bar on the first axis date")
	}
	firstReal := -1
	for d, v := range late {
		if v > 0 {
			firstReal = d
			break
		}
	}
	if firstReal <= 0 {
		t.Errorf("late listing should start after the axis begins, got index %d", firstReal)
	}
	for d := 0; d < firstReal; d++ {
		if late[d] != 0 {
			t.Errorf("pre-listing date %d should be 0, got %v", d, late[d])
		}
	}
}

func TestUniverseSkipsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	writeCache(t, dir, "1111", "TW", "2026-01-01", ramp(200))
	if err := os.WriteFile(filepath.Join(dir, "8888_TW.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := CacheFeed{Dir: dir, MinBars: 120}.Universe("")
	if err != nil {
		t.Fatalf("one corrupt file must not sink the universe: %v", err)
	}
	if u.Symbols != 1 {
		t.Errorf("got %d symbols, want 1", u.Symbols)
	}
}

func TestUniverseErrors(t *testing.T) {
	if _, err := (CacheFeed{Dir: t.TempDir()}).Universe(""); err == nil {
		t.Error("an empty cache dir should error")
	}
	dir := t.TempDir()
	writeCache(t, dir, "1111", "TW", "2026-01-01", ramp(30))
	if _, err := (CacheFeed{Dir: dir, MinBars: 120}).Universe(""); err == nil {
		t.Error("a universe where nothing is long enough should error")
	}
}

// One sparse date on the axis is not a harmless extra column: every other symbol gets a 0
// there, and since a moving-average window containing a gap is unmeasurable, that single
// date evicts the whole universe from the MA120 population for 120 sessions. Observed for
// real in this repo's cache (2025-08-01 carries 11 of 1993 symbols).
func TestUniverseDropsLowCoverageDates(t *testing.T) {
	dir := t.TempDir()
	// 20 symbols trade every day for 200 days...
	for i := 0; i < 20; i++ {
		writeCache(t, dir, fmt.Sprintf("10%02d", i), "TW", "2026-01-01", ramp(200))
	}
	// ...and one straggler additionally carries a date nobody else has.
	writeStraggler(t, dir, "9001", "TW", "2026-01-01", ramp(200), "2025-08-01")

	u, err := CacheFeed{Dir: dir, MinBars: 120}.Universe("")
	if err != nil {
		t.Fatalf("Universe: %v", err)
	}
	for _, d := range u.Dates {
		if d == "2025-08-01" {
			t.Fatal("a date carried by 1 of 21 symbols must not become a universe trading day")
		}
	}
	found := false
	for _, d := range u.DroppedDates {
		if d == "2025-08-01" {
			found = true
		}
	}
	if !found {
		t.Errorf("the dropped date must be reported, got %v", u.DroppedDates)
	}
	if u.Symbols != 21 {
		t.Errorf("dropping a date must not drop symbols: got %d, want 21", u.Symbols)
	}
	if len(u.Dates) != 200 {
		t.Errorf("axis = %d dates, want the 200 real ones", len(u.Dates))
	}
}

// writeStraggler writes a symbol whose series carries one extra, otherwise-unused date.
func writeStraggler(t *testing.T, dir, code, market, start string, closes []float64, extra string) {
	t.Helper()
	type candle struct {
		Date  time.Time
		Close float64
	}
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := time.Parse("2006-01-02", extra)
	if err != nil {
		t.Fatal(err)
	}
	cs := []candle{{Date: ex, Close: 99}}
	for i, c := range closes {
		cs = append(cs, candle{Date: d.AddDate(0, 0, i), Close: c})
	}
	rec := map[string]any{
		"fetched_at": time.Now(),
		"data":       map[string]any{"Symbol": code, "Name": code, "Market": market, "Candles": cs},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, code+"_"+market+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
