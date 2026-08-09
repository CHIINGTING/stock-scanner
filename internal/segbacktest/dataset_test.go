package segbacktest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCache writes a cache file in the exact shape internal/fetcher's dataCache uses,
// so these tests break if that on-disk format ever changes.
type bar struct {
	date  string
	close float64
	adj   float64
}

func fakeCache(t *testing.T, dir, code, market, name string, bars []bar, fetched time.Time) {
	t.Helper()
	type candle struct {
		Date     time.Time
		Open     float64
		High     float64
		Low      float64
		Close    float64
		Volume   int64
		AdjClose float64
	}
	var candles []candle
	for _, b := range bars {
		d, err := time.Parse("2006-01-02", b.date)
		if err != nil {
			t.Fatalf("bad date %q: %v", b.date, err)
		}
		candles = append(candles, candle{
			Date: d, Open: b.close, High: b.close, Low: b.close,
			Close: b.close, Volume: 1000, AdjClose: b.adj,
		})
	}
	rec := map[string]any{
		"fetched_at": fetched,
		"data": map[string]any{
			"Symbol": code, "Name": name, "Market": market, "Source": "market",
			"Candles": candles,
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, code+"_"+market+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// series of n consecutive weekdays starting at start, each close = base+i.
func rampBars(t *testing.T, start string, n int, base float64) []bar {
	t.Helper()
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	var out []bar
	for i := 0; len(out) < n; i++ {
		day := d.AddDate(0, 0, i)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		px := base + float64(len(out))
		out = append(out, bar{date: day.Format("2006-01-02"), close: px, adj: px})
	}
	return out
}

func TestLoadDatasetBasics(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	fakeCache(t, dir, "2330", "TW", "台積電", rampBars(t, "2026-01-01", 100, 900), now)
	fakeCache(t, dir, "5483", "TWO", "中美晶", rampBars(t, "2026-01-01", 100, 160), now.Add(-time.Hour))
	// Too short to embed.
	fakeCache(t, dir, "9999", "TW", "短命股", rampBars(t, "2026-01-01", 10, 50), now)
	// Corrupt file must be skipped, not fatal.
	if err := os.WriteFile(filepath.Join(dir, "8888_TW.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	ds, err := LoadDataset(LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(ds.Stocks) != 2 {
		t.Fatalf("want 2 stocks (short + corrupt skipped), got %d", len(ds.Stocks))
	}
	if ds.Stocks[0].Code != "2330" || ds.Stocks[1].Code != "5483" {
		t.Errorf("stocks not sorted by code: %s, %s", ds.Stocks[0].Code, ds.Stocks[1].Code)
	}
	if ds.Stocks[0].Name != "台積電" || ds.Stocks[1].Market != "TWO" {
		t.Errorf("metadata lost: %+v %+v", ds.Stocks[0], ds.Stocks[1])
	}
	if len(ds.Axis) != 100 {
		t.Errorf("axis len = %d, want 100", len(ds.Axis))
	}
	for i := 1; i < len(ds.Axis); i++ {
		if ds.Axis[i-1] >= ds.Axis[i] {
			t.Fatalf("axis not strictly ascending at %d: %s >= %s", i, ds.Axis[i-1], ds.Axis[i])
		}
	}
	if !ds.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want the newest cache stamp %v", ds.FetchedAt, now)
	}
	if !ds.Has("2330") || ds.Has("9999") {
		t.Errorf("Has() disagrees with the embedded set")
	}
	if got := ds.Bars(); got != 200 {
		t.Errorf("Bars() = %d, want 200", got)
	}
	if ds.Start() != ds.Axis[0] || ds.End() != ds.Axis[len(ds.Axis)-1] {
		t.Errorf("Start/End mismatch")
	}
}

func TestLoadDatasetAlignsDifferentCalendars(t *testing.T) {
	// 2330 trades every weekday; 1234 starts a month later and is missing one day in
	// the middle. Both must land on the shared axis at the right positions.
	dir := t.TempDir()
	now := time.Now()
	long := rampBars(t, "2026-01-01", 90, 100)
	fakeCache(t, dir, "2330", "TW", "長", long, now)

	short := rampBars(t, "2026-02-02", 80, 50)
	gapDate := short[10].date
	withGap := append([]bar{}, short[:10]...)
	withGap = append(withGap, short[11:]...) // drop one bar
	fakeCache(t, dir, "1234", "TW", "缺", withGap, now)

	ds, err := LoadDataset(LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	var s Series
	for _, x := range ds.Stocks {
		if x.Code == "1234" {
			s = x
		}
	}
	if s.Code == "" {
		t.Fatal("1234 missing")
	}
	if ds.Axis[s.Offset] != short[0].date {
		t.Errorf("offset points at %s, want first bar %s", ds.Axis[s.Offset], short[0].date)
	}
	// Every embedded close must match the axis date it sits on.
	for i, p := range s.Closes {
		date := ds.Axis[s.Offset+i]
		if date == gapDate {
			if p != 0 {
				t.Errorf("gap date %s should be 0, got %v", date, p)
			}
			continue
		}
		var want float64
		for _, b := range withGap {
			if b.date == date {
				want = b.close
			}
		}
		if want == 0 {
			// axis date on which this symbol simply did not trade
			if p != 0 {
				t.Errorf("%s: expected no bar, got %v", date, p)
			}
			continue
		}
		if float64(p) != want {
			t.Errorf("%s: close = %v, want %v", date, p, want)
		}
	}
	// The short series must not claim bars before it listed.
	if s.Offset == 0 {
		t.Errorf("late-listing symbol should have a non-zero offset")
	}
}

func TestLoadDatasetDaysTrim(t *testing.T) {
	dir := t.TempDir()
	full := rampBars(t, "2026-01-01", 120, 100)
	fakeCache(t, dir, "2330", "TW", "台積電", full, time.Now())

	ds, err := LoadDataset(LoadOptions{Dir: dir, Days: 30})
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(ds.Axis) != 30 {
		t.Fatalf("axis = %d, want 30", len(ds.Axis))
	}
	// Trimming keeps the MOST RECENT window.
	if ds.End() != full[len(full)-1].date {
		t.Errorf("End = %s, want newest bar %s", ds.End(), full[len(full)-1].date)
	}
	if ds.Start() != full[len(full)-30].date {
		t.Errorf("Start = %s, want %s", ds.Start(), full[len(full)-30].date)
	}
	if len(ds.Stocks[0].Closes) != 30 {
		t.Errorf("closes = %d, want 30", len(ds.Stocks[0].Closes))
	}
	// Days larger than the history is a no-op, not an error.
	ds2, err := LoadDataset(LoadOptions{Dir: dir, Days: 9999})
	if err != nil {
		t.Fatalf("LoadDataset wide: %v", err)
	}
	if len(ds2.Axis) != 120 {
		t.Errorf("axis = %d, want 120", len(ds2.Axis))
	}
}

func TestLoadDatasetAdjustedAndOnly(t *testing.T) {
	dir := t.TempDir()
	bars := rampBars(t, "2026-01-01", 80, 100)
	for i := range bars {
		bars[i].adj = bars[i].close / 2 // pretend a 1:2 split adjustment
	}
	fakeCache(t, dir, "2330", "TW", "台積電", bars, time.Now())
	fakeCache(t, dir, "2317", "TW", "鴻海", rampBars(t, "2026-01-01", 80, 200), time.Now())

	raw, err := LoadDataset(LoadOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	adj, err := LoadDataset(LoadOptions{Dir: dir, UseAdjusted: true})
	if err != nil {
		t.Fatal(err)
	}
	if raw.Adjusted || !adj.Adjusted {
		t.Errorf("Adjusted flag not propagated: raw=%v adj=%v", raw.Adjusted, adj.Adjusted)
	}
	find := func(ds *Dataset, code string) Series {
		for _, s := range ds.Stocks {
			if s.Code == code {
				return s
			}
		}
		t.Fatalf("%s missing", code)
		return Series{}
	}
	if got, want := float64(find(adj, "2330").Closes[0]), bars[0].close/2; got != want {
		t.Errorf("adjusted close = %v, want %v", got, want)
	}
	if got, want := float64(find(raw, "2330").Closes[0]), bars[0].close; got != want {
		t.Errorf("raw close = %v, want %v", got, want)
	}

	only, err := LoadDataset(LoadOptions{Dir: dir, Only: []string{" 2317 "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Stocks) != 1 || only.Stocks[0].Code != "2317" {
		t.Errorf("Only filter failed: %+v", only.Stocks)
	}
}

func TestLoadDatasetErrors(t *testing.T) {
	if _, err := LoadDataset(LoadOptions{Dir: t.TempDir()}); err == nil {
		t.Error("empty dir should error")
	}
	dir := t.TempDir()
	fakeCache(t, dir, "9999", "TW", "短", rampBars(t, "2026-01-01", 5, 10), time.Now())
	if _, err := LoadDataset(LoadOptions{Dir: dir}); err == nil {
		t.Error("all-too-short cache should error")
	}
}

func TestPriceMarshalsCompactly(t *testing.T) {
	cases := []struct {
		in   Price
		want string
	}{
		{940, "940"},
		{106.5, "106.5"},
		{106.50000000000001, "106.5"}, // Yahoo float artifact
		{1.235, "1.24"},               // 2dp rounding
		{0, "0"},
		{-3, "0"}, // negatives are "no data", never emitted as prices
	}
	for _, c := range cases {
		b, err := json.Marshal(c.in)
		if err != nil {
			t.Fatalf("marshal %v: %v", float64(c.in), err)
		}
		if string(b) != c.want {
			t.Errorf("Price(%v) → %s, want %s", float64(c.in), b, c.want)
		}
	}
}

func TestCodeFromFilename(t *testing.T) {
	cases := map[string]string{
		".cache/2330_TW.json":  "2330",
		".cache/5483_TWO.json": "5483",
		"/x/00981A_TW.json":    "00981A",
		"weird.json":           "weird",
	}
	for in, want := range cases {
		if got := codeFromFilename(in); got != want {
			t.Errorf("codeFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
