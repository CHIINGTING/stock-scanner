package healthcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The contract this file exists to hold:
//
//	health-dashboard MUST NOT create, modify, touch, rename or remove scanner cache files.
//
// It is written as a permanent regression rather than a one-off check because the violation
// it guards against was invisible from inside the dashboard: internal/fetcher writes what it
// fetches, so simply POINTING the health service at the scan's cache_dir was enough to put an
// intraday, half-formed daily bar into the series the next scan reads. Nothing in the
// dashboard's own behaviour looked wrong at the time.
//
// These tests use a REAL fetcher against a pre-seeded cache, so the fetcher's own read and
// write paths run. They make no network call: an entry inside the TTL is served from cache,
// which is exactly the path a warm cache takes in production.

// seedCache writes a fetcher cache record the way internal/fetcher would.
func seedCache(t *testing.T, dir, ticker string, fetchedAt time.Time, candles []fetcher.Candle) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"fetched_at": fetchedAt,
		"data": fetcher.StockData{
			Symbol: "2330", Name: "台積電", Market: "TW", Candles: candles,
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ticker+".json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type fileState struct {
	sum   string
	mtime time.Time
	size  int64
}

func stateOf(t *testing.T, path string) fileState {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return fileState{sum: hex.EncodeToString(sum[:]), mtime: st.ModTime(), size: st.Size()}
}

// realFetcherService builds a service whose prices come from a REAL fetcher pointed at
// cacheDir, so the fetcher's cache read and write paths both run.
func realFetcherService(t *testing.T, cacheDir, today string) *Service {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		Scanner: scanner.Config{Institution: scanner.InstitutionConfig{
			SnapshotDir: filepath.Join(root, "institution")}},
		FundamentalDir:    filepath.Join(root, "fundamental"),
		ValuationDir:      filepath.Join(root, "valuation"),
		MacroDir:          filepath.Join(root, "macro"),
		MarketSnapshotDir: filepath.Join(root, "market"),
		NewsSnapshotDir:   filepath.Join(root, "reports"),
	}
	f := fetcher.New(fetcher.Config{
		CacheDir:     cacheDir,
		CacheTTLMin:  600, // long, so the seeded entry is served without any network call
		Concurrency:  1,
		TimeoutSec:   5,
		HistoryRange: "2y",
	})
	return &Service{
		Catalog: testCatalog(t),
		Prices:  FetcherPrices{F: f},
		Scanner: scanner.New(cfg.Scanner),
		Cfg:     cfg.Defaulted(),
		Now:     func() time.Time { return day(today).Add(11 * time.Hour) },
	}
}

// ── gate 2: the scanner's cache files are untouched, byte for byte ────────────────────

// A health request must leave every scanner cache file byte-identical and with an unchanged
// mtime, and must produce its own artifact in its own directory.
func TestScannerCacheFilesAreNeverTouched(t *testing.T) {
	root := t.TempDir()
	scanCache := filepath.Join(root, ".cache")
	healthCache := filepath.Join(root, ".cache-health-dashboard")
	end := day("2026-09-04")

	// The scan's own cache: a complete series ending on the previous session.
	scanFile := seedCache(t, scanCache, "2330_TW", time.Now(),
		series(end.AddDate(0, 0, -1), 60, 700))
	// The dashboard's cache: its own copy, which it is allowed to write.
	seedCache(t, healthCache, "2330_TW", time.Now(), series(end, 60, 700))

	before := stateOf(t, scanFile)
	// Every file in the scan's cache, not only the one this check reads.
	dirBefore := snapshotDir(t, scanCache)

	svc := realFetcherService(t, healthCache, "2026-09-04")
	if _, err := svc.Check(context.Background(), Request{Symbol: "2330", AsOf: "2026-09-04"}); err != nil {
		t.Fatalf("check: %v", err)
	}
	// internal/fetcher writes its cache in a goroutine, so a write racing this assertion
	// would otherwise be missed entirely.
	time.Sleep(250 * time.Millisecond)

	after := stateOf(t, scanFile)
	if after.sum != before.sum {
		t.Errorf("the scanner's cache file CHANGED: sha256 %s → %s", before.sum, after.sum)
	}
	if !after.mtime.Equal(before.mtime) {
		t.Errorf("the scanner's cache file was touched: mtime %s → %s", before.mtime, after.mtime)
	}
	if after.size != before.size {
		t.Errorf("size %d → %d", before.size, after.size)
	}
	if diff := diffDir(t, scanCache, dirBefore); diff != "" {
		t.Errorf("the scanner's cache directory changed: %s", diff)
	}

	// And the dashboard did use a cache of its own.
	if _, err := os.Stat(filepath.Join(healthCache, "2330_TW.json")); err != nil {
		t.Errorf("the dashboard produced no artifact of its own: %v", err)
	}
}

// snapshotDir records every file in a directory, so a created or removed file is caught too.
func snapshotDir(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out[e.Name()] = stateOf(t, filepath.Join(dir, e.Name()))
	}
	return out
}

func diffDir(t *testing.T, dir string, before map[string]fileState) string {
	t.Helper()
	now := snapshotDir(t, dir)
	for name, was := range before {
		is, ok := now[name]
		if !ok {
			return "file removed: " + name
		}
		if is.sum != was.sum {
			return "file modified: " + name
		}
		if !is.mtime.Equal(was.mtime) {
			return "file touched (mtime): " + name
		}
	}
	for name := range now {
		if _, ok := before[name]; !ok {
			return "file created: " + name
		}
	}
	return ""
}

// ── gate 4: the intraday incident, as a regression ────────────────────────────────────

// The exact shape of the incident this guard exists for.
//
// The scan's cache holds a complete series whose last COMPLETED session is D-1. At 11:00 on
// day D the dashboard fetches, and what it gets includes a half-formed bar for D — the
// session has not closed, so its high, low, close and volume are all provisional.
//
// After the health check, the scanner's own technical path must still see D-1 as its last
// bar. If the two caches are ever shared again, the scan reads D's provisional bar as a
// completed session and every moving average, RSI and ATR built on it is wrong — in a BUY.
func TestIntradayHealthCheckDoesNotMoveTheScannersLastBar(t *testing.T) {
	root := t.TempDir()
	scanCache := filepath.Join(root, ".cache")
	healthCache := filepath.Join(root, ".cache-health-dashboard")

	d := day("2026-09-04") // today, mid-session
	dMinus1 := d.AddDate(0, 0, -1)

	// The scan's view: complete, ending D-1.
	completed := series(dMinus1, 60, 700)
	seedCache(t, scanCache, "2330_TW", time.Now(), completed)

	// The dashboard's view: the same history PLUS a provisional bar for D. Its close is
	// deliberately far from the trend so a contaminated scan would be obvious.
	partial := append(append([]fetcher.Candle(nil), completed...), fetcher.Candle{
		Date: d, Open: 760, High: 999, Low: 700, Close: 985, AdjClose: 985, Volume: 120,
	})
	seedCache(t, healthCache, "2330_TW", time.Now(), partial)

	scanBefore := stateOf(t, filepath.Join(scanCache, "2330_TW.json"))

	svc := realFetcherService(t, healthCache, "2026-09-04")
	h, err := svc.Check(context.Background(), Request{Symbol: "2330", AsOf: "2026-09-04"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	time.Sleep(250 * time.Millisecond)

	// The dashboard legitimately sees the provisional bar — that is its own business.
	if h.Price.BarDate != "2026-09-04" {
		t.Fatalf("the dashboard did not read its own cache: bar_date = %q", h.Price.BarDate)
	}

	// The scanner's cache must be untouched…
	scanAfter := stateOf(t, filepath.Join(scanCache, "2330_TW.json"))
	if scanAfter.sum != scanBefore.sum {
		t.Fatalf("the provisional bar reached the scanner's cache file")
	}

	// …and reading it through the REAL fetcher must still give D-1 as the last bar.
	scanFetcher := fetcher.New(fetcher.Config{
		CacheDir: scanCache, CacheTTLMin: 600, Concurrency: 1, TimeoutSec: 5,
	})
	got, err := scanFetcher.FetchSectorStocks([]fetcher.StockInfo{{Symbol: "2330", Market: "TW"}})
	if err != nil || len(got) == 0 {
		t.Fatalf("re-read scanner cache: %v", err)
	}
	bars := got[0].Candles
	if len(bars) == 0 {
		t.Fatal("no bars in the scanner's cache")
	}
	last := bars[len(bars)-1].Date.Format(dateLayout)
	if last != dMinus1.Format(dateLayout) {
		t.Fatalf("the scanner's last completed bar moved to %s; it must still be %s",
			last, dMinus1.Format(dateLayout))
	}
	for _, c := range bars {
		if c.Date.Format(dateLayout) == "2026-09-04" {
			t.Fatalf("the provisional D bar is in the scanner's series: %+v", c)
		}
		if c.Close == 985 {
			t.Fatalf("the provisional close leaked into the scanner's series")
		}
	}
	if len(bars) != len(completed) {
		t.Fatalf("the scanner's series length changed: %d → %d", len(completed), len(bars))
	}
}
