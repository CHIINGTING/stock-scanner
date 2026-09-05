package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/healthcheck"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

// The dashboard must never share the scanner's price cache.
//
// internal/fetcher WRITES what it fetches. Sharing the directory would let a health check run
// during trading hours put that day's half-formed bar into the series the next scan reads —
// the one way this sidecar could reach back into BUY / WATCH / SELL.
// The collision matrix, stated as a table so a missing row is visible.
//
//	.cache          vs .cache                          reject
//	.cache          vs ././.cache                      reject
//	absolute        vs the equivalent relative         reject
//	symlink → .cache                                   reject
//	blank health cache resolving to the fetcher default reject
//	.cache          vs .cache-health-dashboard         ACCEPT
func TestCacheCollisionMatrix(t *testing.T) {
	abs, err := filepath.Abs(".cache")
	if err != nil {
		t.Fatal(err)
	}
	reject := []struct{ name, a, b string }{
		{"identical", ".cache", ".cache"},
		{"single dot", "./.cache", ".cache"},
		{"double dot prefix", "././.cache", ".cache"},
		{"trailing slash", ".cache/", ".cache"},
		{"absolute vs relative", abs, ".cache"},
		{"relative vs absolute", ".cache", abs},
		{"traversal", "foo/../.cache", ".cache"},
		// A blank health cache means the fetcher falls back to its own default, which IS
		// the scanner's directory.
		{"blank resolves to the fetcher default", ".cache", defaultScannerCacheDir},
	}
	for _, tc := range reject {
		if !sameDir(tc.a, tc.b) {
			t.Errorf("%s: sameDir(%q, %q) = false; the scanner's cache would be shared",
				tc.name, tc.a, tc.b)
		}
	}
	accept := []struct{ name, a, b string }{
		{"the dashboard's own default", defaultCacheDir, ".cache"},
		{"an explicit separate dir", "/tmp/dash-cache", ".cache"},
		{"a near-miss name", ".cache2", ".cache"},
		{"a sibling", ".cache-health-dashboard", ".cache"},
	}
	for _, tc := range accept {
		if sameDir(tc.a, tc.b) {
			t.Errorf("%s: sameDir(%q, %q) = true; a legitimate directory was refused",
				tc.name, tc.a, tc.b)
		}
	}
}

// The default must not be the scanner's, or the guard only protects people who pass a flag.
func TestDefaultCacheDirIsNotTheScanners(t *testing.T) {
	if sameDir(defaultCacheDir, defaultScannerCacheDir) {
		t.Fatalf("the default cache dir %q is the scanner's", defaultCacheDir)
	}
	if defaultCacheDir == "" {
		t.Fatal("an empty default would make the fetcher fall back to the scanner's .cache")
	}
}

// A blank cache_dir in the config file means the fetcher would default to the scanner's, so
// the collision check has to consider that case too.
func TestBlankConfigCacheDirStillCollides(t *testing.T) {
	scanCache := ""
	if scanCache == "" {
		scanCache = defaultScannerCacheDir
	}
	if !sameDir(".cache", scanCache) {
		t.Fatal("a blank cache_dir does not resolve to the scanner's default")
	}
}

// A hard stop with a reachable bypass is not a hard stop.
//
// On a case-insensitive filesystem — APFS by default, so every stock macOS — ".CACHE" opens
// the same directory as ".cache" while comparing unequal as text, and EvalSymlinks does not
// close it either: it succeeds for both spellings and returns each unchanged. os.SameFile
// compares device and inode, which is what actually decides whether a write lands in the
// scanner's cache.
func TestCaseVariantOfTheScannerCacheIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(dir, ".CACHE")
	if _, err := os.Stat(upper); err != nil {
		if runtime.GOOS == "linux" {
			t.Skip("case-sensitive filesystem: .CACHE is genuinely a different directory")
		}
		t.Skipf("cannot reach %s: %v", upper, err)
	}
	if !sameDir(upper, real) {
		t.Fatalf("sameDir(%q, %q) = false — a case variant would bypass the guard and "+
			"write into the scanner's cache", upper, real)
	}
}

// A symlink pointing at the scanner's cache is refused too.
func TestSymlinkToTheScannerCacheIsRefused(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked-cache")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if !sameDir(link, real) {
		t.Fatalf("a symlink to the scanner's cache was accepted")
	}
}

// Two genuinely different directories must still be allowed, or the guard blocks everything.
func TestDistinctDirectoriesAreAllowed(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, ".cache")
	b := filepath.Join(dir, ".cache-health-dashboard")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if sameDir(a, b) {
		t.Fatalf("two distinct directories were reported as the same")
	}
	if !strings.HasSuffix(b, defaultCacheDir) {
		t.Fatalf("fixture drifted from the real default %q", defaultCacheDir)
	}
}

// ── the production wiring guard ───────────────────────────────────────────────────────

// resolveCacheDir is the function main() actually uses, so these cases test the production
// resolution semantics rather than a property of internal/fetcher.
//
// The whole matrix runs through resolveCacheDir, not through sameDir: the previous version of
// these tests exercised the comparison helper while leaving the CALLER untested, and deleting
// the rejection from main() left the suite green.
func TestResolveCacheDirRejectsTheScannerCache(t *testing.T) {
	abs, err := filepath.Abs(".cache")
	if err != nil {
		t.Fatal(err)
	}
	reject := []struct{ name, requested, scanCache string }{
		{"identical", ".cache", ".cache"},
		{"single dot", "./.cache", ".cache"},
		{"double dot prefix", "././.cache", ".cache"},
		{"trailing slash", ".cache/", ".cache"},
		{"relative vs equivalent absolute", ".cache", abs},
		{"absolute vs equivalent relative", abs, ".cache"},
		{"traversal", "foo/../.cache", ".cache"},
		// A blank scan cache is NOT "no collision possible": the fetcher defaults it to
		// .cache, so a config that omits the key still collides.
		{"blank scan cache resolves to the fetcher default", ".cache", ""},
		{"blank scan cache vs an equivalent spelling", "././.cache", ""},
	}
	for _, tc := range reject {
		got, err := resolveCacheDir(tc.requested, tc.scanCache)
		if err == nil {
			t.Errorf("%s: resolveCacheDir(%q, %q) = %+v, want a rejection — the dashboard "+
				"would write into the scanner's cache", tc.name, tc.requested, tc.scanCache, got)
			continue
		}
		if !strings.Contains(err.Error(), "scanner's price cache") {
			t.Errorf("%s: rejection does not say why: %v", tc.name, err)
		}
	}

	accept := []struct{ name, requested, scanCache string }{
		{"the dashboard's own default", defaultCacheDir, ".cache"},
		{"a blank request takes the dashboard default", "", ".cache"},
		{"a blank request against a blank scan cache", "", ""},
		{"an explicit separate directory", "/tmp/dash-cache", ".cache"},
		{"a near-miss name", ".cache2", ".cache"},
		{"a sibling", ".cache-health-dashboard", ".cache"},
	}
	for _, tc := range accept {
		got, err := resolveCacheDir(tc.requested, tc.scanCache)
		if err != nil {
			t.Errorf("%s: resolveCacheDir(%q, %q) refused a legitimate directory: %v",
				tc.name, tc.requested, tc.scanCache, err)
			continue
		}
		if got.Dir == "" {
			t.Errorf("%s: no directory resolved", tc.name)
		}
		if sameDir(got.Dir, got.Reserved) {
			t.Errorf("%s: resolved %q which IS the scanner's %q", tc.name, got.Dir, got.Reserved)
		}
	}
}

// A blank request must never resolve to the fetcher's own default, which is the scanner's
// directory. This is the case a caller is most likely to hit by omission.
func TestBlankRequestNeverResolvesToTheScannerCache(t *testing.T) {
	got, err := resolveCacheDir("", "")
	if err != nil {
		t.Fatalf("a fully-default configuration was refused: %v", err)
	}
	if sameDir(got.Dir, defaultScannerCacheDir) {
		t.Fatalf("a blank request resolved to the scanner's cache %q", got.Dir)
	}
	if got.Dir != defaultCacheDir {
		t.Fatalf("resolved %q, want the dashboard default %q", got.Dir, defaultCacheDir)
	}
	if got.Reserved != defaultScannerCacheDir {
		t.Fatalf("the scanner default was not applied: %q", got.Reserved)
	}
}

// A symlink pointing at the scanner's cache must be refused THROUGH resolveCacheDir, not
// only through sameDir.
func TestResolveCacheDirRefusesASymlinkToTheScannerCache(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked-cache")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if _, err := resolveCacheDir(link, real); err == nil {
		t.Fatal("a symlink to the scanner's cache was accepted")
	}
}

// The APFS case-variant bypass, through the production function.
func TestResolveCacheDirRefusesACaseVariant(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(dir, ".CACHE")
	if _, err := os.Stat(upper); err != nil {
		if runtime.GOOS == "linux" {
			t.Skip("case-sensitive filesystem: .CACHE is genuinely a different directory")
		}
		t.Skipf("cannot reach %s: %v", upper, err)
	}
	if _, err := resolveCacheDir(upper, real); err == nil {
		t.Fatal("a case variant of the scanner's cache was accepted")
	}
}

// The guard must compare against the fetcher's ACTUAL default, not a copy of it.
//
// configs/config.yaml has no cache_dir key at all, so "blank scan cache" is the live path
// rather than a defensive branch. Two independent string literals would let this hard stop
// fail silently the day the fetcher's default changed — precisely on that path.
func TestScannerDefaultTracksTheFetcher(t *testing.T) {
	if defaultScannerCacheDir != fetcher.DefaultCacheDir {
		t.Fatalf("the guard compares against %q but the fetcher defaults to %q",
			defaultScannerCacheDir, fetcher.DefaultCacheDir)
	}
	// No fetcher is constructed here. fetcher.New MkdirAll's its cache directory as a side
	// effect, so a blank CacheDir would create ".cache/" inside this package on every test
	// run — a test that litters the source tree. The line that used to be here also asserted
	// nothing beyond "the constructor returned non-nil", while its comment claimed to verify
	// where the cache resolved.
	if _, err := resolveCacheDir("", fetcher.DefaultCacheDir); err != nil {
		t.Fatalf("the dashboard default collided with the fetcher default: %v", err)
	}
	if _, err := resolveCacheDir(fetcher.DefaultCacheDir, ""); err == nil {
		t.Fatal("asking for the fetcher's default cache was accepted")
	}
}

// Every dependency the grader needs must actually be wired.
//
// An unset field compiles and runs, and is silently inert: Catalog was left out, so the
// market lookup returned "" forever and every TPEx stock paid an extra Yahoo probe that
// nothing would have flagged.
func TestGraderIsFullyWired(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(p, []byte("stocks:\n  - symbol: \"2330.TW\"\n    name: \"台積電\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := stockcatalog.Load(stockcatalog.Sources{
		NamesFile: "-", SectorsFile: "-", AliasesFile: "-", OverlayFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	hist, err := healthcheck.OpenHistory(healthcheck.HistoryConfig{Enabled: true, Path: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hist.Close() }()

	svc := healthcheck.NewService(cat, fetcher.New(fetcher.Config{CacheDir: dir}), nil,
		healthcheck.Config{}, nil)
	svc.History = hist

	g := newGrader(svc, cat)
	if g.History == nil {
		t.Error("History not wired; the grader has nothing to read")
	}
	if g.Prices == nil {
		t.Error("Prices not wired; no forward bars can be fetched")
	}
	if g.Catalog == nil {
		t.Error("Catalog not wired; every TPEx stock pays an extra probe")
	}
	if g.Logf == nil {
		t.Error("Logf not wired; a long batch runs silently")
	}
}
