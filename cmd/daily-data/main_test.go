package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// The production wiring guard.
//
// Removing or bypassing it must fail a test, not merely be covered by internal/fetcher's own
// unit tests: the hazard is which directory this binary CHOOSES, and a correct helper that
// production never calls is exactly the failure R14 shipped once already.

func TestCacheDirIsNotTheScanners(t *testing.T) {
	if fetcher.SameDir(defaultCacheDir, fetcher.DefaultCacheDir) {
		t.Fatalf("the default cache dir %q is the scanner's", defaultCacheDir)
	}
	if defaultCacheDir == "" {
		t.Fatal("an empty default makes the fetcher fall back to the scanner's cache")
	}
}

// The full collision matrix, through the function main() actually calls.
func TestResolveCacheDirMatrix(t *testing.T) {
	abs, err := filepath.Abs(".cache")
	if err != nil {
		t.Fatal(err)
	}
	reject := []struct{ name, requested, reserved string }{
		{"identical", ".cache", ".cache"},
		{"single dot", "./.cache", ".cache"},
		{"double dot prefix", "././.cache", ".cache"},
		{"trailing slash", ".cache/", ".cache"},
		{"relative vs absolute", ".cache", abs},
		{"absolute vs relative", abs, ".cache"},
		{"traversal", "foo/../.cache", ".cache"},
		// configs/config.yaml has no cache_dir key at all, so a blank reserved value is the
		// LIVE path — the fetcher defaults it to .cache.
		{"blank reserved resolves to the fetcher default", ".cache", ""},
	}
	for _, tc := range reject {
		if _, err := fetcher.ResolveCacheDir(tc.requested, tc.reserved, defaultCacheDir); err == nil {
			t.Errorf("%s: ResolveCacheDir(%q, %q) was accepted — this job would write the "+
				"scanner's cache", tc.name, tc.requested, tc.reserved)
		}
	}
	accept := []struct{ name, requested, reserved string }{
		{"this job's default", defaultCacheDir, ".cache"},
		{"blank request takes the default", "", ".cache"},
		{"blank request, blank reserved", "", ""},
		{"the dashboard's cache", ".cache-health-dashboard", ".cache"},
		{"an explicit directory", "/tmp/research-cache", ".cache"},
		{"a near-miss name", ".cache2", ".cache"},
	}
	for _, tc := range accept {
		got, err := fetcher.ResolveCacheDir(tc.requested, tc.reserved, defaultCacheDir)
		if err != nil {
			t.Errorf("%s: refused a legitimate directory: %v", tc.name, err)
			continue
		}
		if fetcher.SameDir(got.Dir, got.Reserved) {
			t.Errorf("%s: resolved %q which IS the reserved %q", tc.name, got.Dir, got.Reserved)
		}
	}
}

// A blank request must never resolve to the fetcher's own default — the case a caller is
// most likely to hit by omission.
func TestBlankRequestNeverResolvesToTheScannerCache(t *testing.T) {
	got, err := fetcher.ResolveCacheDir("", "", defaultCacheDir)
	if err != nil {
		t.Fatalf("a fully-default configuration was refused: %v", err)
	}
	if fetcher.SameDir(got.Dir, fetcher.DefaultCacheDir) {
		t.Fatalf("a blank request resolved to the scanner's cache %q", got.Dir)
	}
	if got.Dir != defaultCacheDir {
		t.Fatalf("resolved %q, want %q", got.Dir, defaultCacheDir)
	}
}

// A symlink and a case variant both have to be refused through the production path.
func TestResolveRefusesSymlinkAndCaseVariant(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".cache")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(real, link); err == nil {
		if _, err := fetcher.ResolveCacheDir(link, real, defaultCacheDir); err == nil {
			t.Error("a symlink to the reserved cache was accepted")
		}
	}
	upper := filepath.Join(dir, ".CACHE")
	if _, err := os.Stat(upper); err == nil {
		if _, err := fetcher.ResolveCacheDir(upper, real, defaultCacheDir); err == nil {
			t.Error("a case variant of the reserved cache was accepted")
		}
	}
}

// ── the wiring itself ─────────────────────────────────────────────────────────────────

// buildFetcherConfig is the ONLY route to a fetcher.Config in this binary, so the guard
// cannot be bypassed by forgetting to call it — there would be no config to build one from.
//
// The previous shape put the resolve in main() and these tests on internal/fetcher's helper.
// Deleting main's call to the guard then left the entire suite green, which is the same
// defect R14 shipped once: a correct, tested helper that production does not call.
func TestBuildFetcherConfigEnforcesTheGuard(t *testing.T) {
	var cfg config
	cfg.Fetcher.CacheDir = ".cache" // the scanner's, as configs/config.yaml resolves to

	if _, _, err := buildFetcherConfig(cfg, ".cache"); err == nil {
		t.Fatal("the scanner's cache was accepted")
	}
	if _, _, err := buildFetcherConfig(cfg, "././.cache"); err == nil {
		t.Fatal("a spelling of the scanner's cache was accepted")
	}

	got, choice, err := buildFetcherConfig(cfg, "")
	if err != nil {
		t.Fatalf("the default configuration was refused: %v", err)
	}
	if got.CacheDir != defaultCacheDir {
		t.Fatalf("the fetcher would be built with %q, want %q", got.CacheDir, defaultCacheDir)
	}
	if fetcher.SameDir(got.CacheDir, ".cache") {
		t.Fatalf("the resolved cache IS the scanner's: %q", got.CacheDir)
	}
	if choice.Reserved != ".cache" {
		t.Fatalf("the reserved directory was not carried through: %q", choice.Reserved)
	}

	// A blank configured cache_dir — which is what configs/config.yaml actually has — must
	// still resolve to the scanner's for the purpose of refusing it.
	var blank config
	if _, _, err := buildFetcherConfig(blank, ".cache"); err == nil {
		t.Fatal("a blank configured cache_dir let the scanner's cache through")
	}
}

// The binary itself refuses to start. This is the backstop for the one thing a unit test
// cannot reach: that main() actually runs the guarded path before doing any work.
func TestBinaryRefusesTheScannerCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	bin := filepath.Join(t.TempDir(), "daily-data")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// -dry-run so nothing is fetched even if the guard were broken.
	cmd := exec.Command(bin, "-cache-dir", ".cache", "-dry-run")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the binary started with the scanner's cache:\n%s", out)
	}
	if !strings.Contains(string(out), "scanner's price cache") {
		t.Fatalf("it failed for some other reason:\n%s", out)
	}
}

// repoRoot finds the module root, so the subprocess runs where configs/ and candidate.yaml are.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("cannot find the module root")
	return ""
}

// -dry-run must not touch the network.
//
// Counted, not inferred from side effects: the fetcher writes its cache in a goroutine, so a
// process that exits promptly leaves no file behind whether or not it fetched. A test that
// looked for cache files would pass for both behaviours — which it did, and the mutation
// survived it.
func TestDryRunDoesNotProbeThePriceSource(t *testing.T) {
	var calls int
	counting := countingPrices{n: &calls}

	// A weekday with an explicit date, so the probe WOULD run.
	st := resolveSession("2026-09-04", at("2026-09-04 15:00"), counting, true)
	if calls != 0 {
		t.Fatalf("-dry-run made %d price requests; it must make none", calls)
	}
	if st.Date != "2026-09-04" {
		t.Fatalf("the session was still resolved wrongly: %+v", st)
	}

	// Without the flag it does probe — the suppression must not disable the check itself.
	calls = 0
	if got := resolveSession("2026-09-04", at("2026-09-04 15:00"), counting, false); calls == 0 {
		t.Fatalf("a normal run made no price request (%+v)", got)
	}

	// And the default (no -date) path is suppressed too.
	calls = 0
	resolveSession("", at("2026-09-08 15:00"), counting, true)
	if calls != 0 {
		t.Fatalf("-dry-run probed on the default path: %d requests", calls)
	}
}

type countingPrices struct{ n *int }

func (c countingPrices) Fetch(fetcher.StockInfo) (fetcher.StockData, error) {
	*c.n++
	d, _ := time.ParseInLocation("2006-01-02", "2026-09-04", dailydata.Taipei)
	return fetcher.StockData{Candles: []fetcher.Candle{{Date: d, Close: 100}}}, nil
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, dailydata.Taipei)
	if err != nil {
		panic(err)
	}
	return t
}

// A dry run must not even create the cache directory.
//
// This is the observable the previous test lacked. fetcher.New MkdirAll's its cache at
// construction, so "the directory exists" is a deterministic, race-free proof that a Fetcher
// was built — where "a cache file exists" is not, since the write happens in a goroutine the
// process may outrun.
func TestDryRunBuildsNoFetcher(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	bin := filepath.Join(t.TempDir(), "daily-data")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cache := filepath.Join(t.TempDir(), "never-created")

	cmd := exec.Command(bin, "-dry-run", "-cache-dir", cache, "-date", "2026-09-04")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatalf("-dry-run created the cache directory %s, so it built a Fetcher:\n%s",
			cache, out)
	}
	if !strings.Contains(string(out), "dry-run") {
		t.Fatalf("the run did not report itself as a dry run:\n%s", out)
	}
}
