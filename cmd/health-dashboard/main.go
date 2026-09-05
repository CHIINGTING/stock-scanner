// Command health-dashboard serves the interactive stock health check.
//
//	go run ./cmd/health-dashboard                  # http://localhost:8080
//	go run ./cmd/health-dashboard -addr :9000
//	go run ./cmd/health-dashboard -no-ai           # deterministic evidence only
//
// # It is a SIDECAR, and the price cache is where that had to be enforced
//
// This binary reads the archives the fetch commands already write, and nothing it produces
// reaches BUY / WATCH / SELL, the rocket score, the ranking, the candidate filter or the
// market regime.
//
// Reading is not the whole story, though, and the price path is the exception that had to be
// closed. internal/fetcher WRITES what it fetches: FetchSectorStocks → fetchYahoo →
// dataCache.Set → os.WriteFile. Pointed at the scan's own cache_dir, a health check run at
// 11:00 would write that morning's HALF-FORMED daily bar into .cache/2330_TW.json with a
// fresh timestamp, and a scan started inside cache_ttl_min would read it back as the last
// completed session. Every moving average, RSI and ATR computed from it would be wrong, and
// the wrongness would arrive in a BUY.
//
// So this binary keeps its OWN cache directory (defaultCacheDir), and refuses to start if it
// is ever pointed at the scanner's. Two caches cost a re-fetch; one shared cache costs the
// guarantee that running a dashboard cannot change tomorrow's report.
//
// # The API key
//
// OPENAI_API_KEY comes from the environment and nowhere else. It is not a flag — flags land
// in shell history and in `ps` output — it is not in the config file, and it is never held on
// a struct. Without it the dashboard runs and every section but the AI reading is produced.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/healthcheck"
	"github.com/deep-huang/stock-scanner/internal/httpapi"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

// config is the subset of configs/config.yaml this binary needs.
//
// It reuses the scanner's and fetcher's own config types so the dashboard computes indicators
// with exactly the settings the daily scan uses. A dashboard whose RSI period differed from
// the report's would be a second opinion wearing the same words.
type config struct {
	Fetcher fetcher.Config `yaml:"fetcher"`
	Scanner scanner.Config `yaml:"scanner"`
	// Dashboard is this binary's own block. It is absent from configs/config.yaml today, so
	// every field falls back to a default and the scan's config file needs no edit.
	Dashboard dashboardConfig `yaml:"health_dashboard"`
}

type dashboardConfig struct {
	AI      healthcheck.JudgeConfig   `yaml:"ai"`
	History healthcheck.HistoryConfig `yaml:"history"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "config file path")
	cacheDir := flag.String("cache-dir", defaultCacheDir,
		"price cache directory; MUST NOT be the scanner's")
	addr := flag.String("addr", ":8080", "listen address")
	noAI := flag.Bool("no-ai", false, "serve deterministic evidence only, never call a model")
	history := flag.Bool("history", false, "record every check into the R13 SQLite database")
	gradeOutcomes := flag.Bool("outcomes", false,
		"grade stored health checks against what happened next, then exit (no server)")
	outcomesAsOf := flag.String("outcomes-as-of", "",
		"grade returns up to this date (YYYY-MM-DD); default today")
	overlay := flag.String("catalog", stockcatalog.DefaultOverlayFile,
		"stock catalog overlay YAML (\"-\" to skip)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		// A missing or malformed config is fatal rather than defaulted: serving a health
		// check computed with silently different indicator settings than the scan would be
		// a second opinion nobody asked for.
		log.Fatalf("config: %v", err)
	}

	// The catalog decides stock identity for the whole service. A partial one would resolve
	// some symbols and 404 others, so a load failure stops startup rather than producing a
	// server that is wrong about which company it is looking at.
	catalog, err := stockcatalog.Load(stockcatalog.Sources{OverlayFile: *overlay})
	if err != nil {
		log.Fatalf("stock catalog: %v", err)
	}
	if catalog.Len() == 0 {
		log.Fatalf("stock catalog: loaded 0 stocks; nothing could be searched or checked")
	}
	log.Printf("catalog: %d stocks", catalog.Len())

	// The one decision that keeps this binary a sidecar, made in a function so it can be
	// tested. See resolveCacheDir.
	choice, err := resolveCacheDir(*cacheDir, cfg.Fetcher.CacheDir)
	if err != nil {
		log.Fatalf("%v", err)
	}
	cfg.Fetcher.CacheDir = choice.Dir
	log.Printf("price cache: %s (the scanner's %s is never written)", choice.Dir, choice.ScanDir)

	aiCfg := cfg.Dashboard.AI
	if *noAI {
		aiCfg.Enabled = false
	}

	svcCfg := healthcheck.Config{Scanner: cfg.Scanner, AI: aiCfg}
	svc := healthcheck.NewService(
		catalog,
		fetcher.New(cfg.Fetcher),
		scanner.New(cfg.Scanner),
		svcCfg,
		func(format string, args ...any) { log.Printf(format, args...) },
	)

	switch {
	case !aiCfg.Enabled:
		log.Printf("ai: disabled — every section but the AI reading is still produced")
	case !hasAPIKey():
		log.Printf("ai: OPENAI_API_KEY is not set — the AI section will report UNAVAILABLE")
	default:
		log.Printf("ai: enabled (model %s)", aiCfg.Defaulted().Model)
	}

	if *history || cfg.Dashboard.History.Enabled {
		hc := cfg.Dashboard.History
		if hc.Path == "" {
			// Defaulting would land on store.DefaultPath — the live R13 audit database —
			// from a single flag, and every button press writes a scan_run into it. An
			// explicit path is cheap; discovering later that a dashboard has been writing
			// into the research database is not.
			log.Fatalf("history is enabled but no path is set. " +
				"Set health_dashboard.history.path in the config — defaulting here would " +
				"write into the live R13 research database.")
		}
		hist, err := healthcheck.OpenHistory(hc)
		if err != nil {
			// Startup fails rather than silently serving without the audit trail the operator
			// asked for. A dashboard that quietly stopped recording would be discovered at
			// the moment somebody tried to grade a call and found nothing.
			log.Fatalf("history: %v", err)
		}
		defer func() { _ = hist.Close() }()
		svc.History = hist
		log.Printf("history: recording to %s (source_tab=%s, scanner_version=%s)",
			hc.Path, healthcheck.TabHealthCheck, healthcheck.HistoryVersion)
	} else {
		log.Printf("history: disabled — checks are answered but not recorded")
	}

	// Grading is a BATCH job, not a request path, so it runs and exits rather than being
	// exposed as an endpoint: it walks every stored check, and a stray HTTP call must not be
	// able to start that. It shares this binary because it needs the same config, the same
	// catalog and — critically — the same cache guard.
	if *gradeOutcomes {
		if svc.History == nil {
			log.Fatalf("-outcomes needs history enabled; there is nothing stored to grade")
		}
		grader := newGrader(svc, catalog)
		rep, err := grader.Run(context.Background(),
			healthcheck.OutcomeConfig{AsOf: *outcomesAsOf})
		if err != nil {
			log.Fatalf("outcomes: %v", err)
		}
		reportOutcomes(rep)
		return
	}

	api := &httpapi.Server{Catalog: catalog, Health: svc}
	srv := &http.Server{
		Addr:    *addr,
		Handler: api.Handler(),
		// ReadHeaderTimeout bounds how long a client may take to send its headers. Without
		// it a single idle connection holds a goroutine open indefinitely — the Slowloris
		// shape — and it is the one timeout net/http does not default.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// A health check fetches prices and reads several archives, and may wait on a model.
		// WriteTimeout has to exceed the handler's own 60s bound or the response is cut off
		// mid-body and the browser shows a truncated page rather than an error.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("health dashboard on http://localhost%s", normaliseAddr(*addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Shut down on the first signal so an in-flight health check finishes rather than being
	// cut off half-rendered.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// cacheChoice is the resolved price-cache decision: where this binary will write, and the
// scanner directory it was checked against.
type cacheChoice struct {
	Dir     string
	ScanDir string
}

// resolveCacheDir decides where the dashboard's price cache goes, and REFUSES the scanner's.
//
// This is the production invariant, extracted into a function precisely so it can be killed
// by a test:
//
//	health-dashboard wiring MUST NOT resolve to the scanner cache directory.
//
// It was previously inline in main(), where deleting the whole check left the suite green —
// the isolation tests in internal/healthcheck prove that internal/fetcher does not write
// across two directories a test hands it, which is a different statement entirely. Nothing
// tested the step that CHOOSES those directories.
//
// Both defaults matter and both are applied here rather than by the caller:
//
//	requested == ""   this binary's own directory, never the fetcher's default
//	scanCache == ""   the FETCHER's default, because that is what a blank cache_dir in the
//	                  scan's config actually resolves to — treating blank as "no collision
//	                  possible" would reopen the hole for every config that omits the key
//
// A collision is an error rather than a warning. internal/fetcher writes what it fetches, so
// sharing the directory lets an intraday check put a half-formed daily bar into the series
// the next scan reads; a warning in a log nobody reads is not a guarantee.
func resolveCacheDir(requested, scanCache string) (cacheChoice, error) {
	if requested == "" {
		requested = defaultCacheDir
	}
	if scanCache == "" {
		scanCache = defaultScannerCacheDir
	}
	if sameDir(requested, scanCache) {
		return cacheChoice{}, fmt.Errorf(
			"cache-dir %q is the scanner's price cache (%q).\n"+
				"A health check fetches live prices and internal/fetcher writes what it "+
				"fetches, so sharing this directory would let an intraday check put a "+
				"half-formed daily bar into the series the next scan reads. "+
				"Use a different -cache-dir.", requested, scanCache)
	}
	return cacheChoice{Dir: requested, ScanDir: scanCache}, nil
}

// defaultCacheDir is this binary's own price cache, deliberately NOT the scanner's.
const defaultCacheDir = ".cache-health-dashboard"

// defaultScannerCacheDir is internal/fetcher's OWN default, imported rather than copied.
//
// It is used when the config file leaves cache_dir blank — which configs/config.yaml
// currently does, so this is the live path, not a defensive branch. A second copy of the
// literal would make this hard stop fail silently the day the fetcher's default changed;
// referencing the constant makes that a compile-time concern instead.
const defaultScannerCacheDir = fetcher.DefaultCacheDir

// sameDir reports whether two paths name the same directory.
//
// Three tests, widest last:
//
//	text    cleaned, then made absolute — catches ".cache", "./.cache", "foo/../.cache"
//	link    EvalSymlinks — catches a symlink pointing at the other directory
//	identity os.SameFile on the stat results — catches everything the string comparison
//	         cannot see
//
// The identity check is the one that matters, and string comparison alone is a bypass rather
// than a hypothetical: APFS is case-insensitive by default, so on macOS "-cache-dir .CACHE"
// resolves to the scanner's cache while comparing unequal as text. EvalSymlinks does not
// help — it succeeds for both and returns each spelling unchanged. os.SameFile compares
// device and inode, so it closes that along with hard links and bind mounts in one move.
//
// A path that does not exist yet cannot be stat'd, which is normal on a first run; the string
// tests still apply, and a directory that does not exist cannot be the one being written to
// anyway.
func sameDir(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	aa, errA := filepath.Abs(ca)
	ab, errB := filepath.Abs(cb)
	if errA == nil && errB == nil {
		if aa == ab {
			return true
		}
		if ra, err1 := filepath.EvalSymlinks(aa); err1 == nil {
			if rb, err2 := filepath.EvalSymlinks(ab); err2 == nil && ra == rb {
				return true
			}
		}
	}
	sa, err1 := os.Stat(ca)
	sb, err2 := os.Stat(cb)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// newGrader wires the batch grader.
//
// It is a function, not an inline literal, for the reason resolveCacheDir is: a field left
// unset in main() compiles, runs, and is silently inert — Catalog was exactly that, and the
// only symptom was a redundant Yahoo probe per TPEx stock, which nothing would ever notice.
// Wiring that has to be right is wiring that has to be testable.
func newGrader(svc *healthcheck.Service, catalog *stockcatalog.Catalog) *healthcheck.Outcomes {
	return &healthcheck.Outcomes{
		History: svc.History,
		Prices:  svc.Prices,
		Catalog: catalog,
		Logf:    func(f string, a ...any) { log.Printf(f, a...) },
	}
}

// reportOutcomes prints what a grading pass did, including what it deliberately did NOT do.
//
// The skips are printed because they are the interesting part: a pass that graded nothing
// because every check is too recent looks identical, in a bare success line, to one that
// graded nothing because the filter is broken.
func reportOutcomes(rep healthcheck.OutcomeReport) {
	log.Printf("outcomes as of %s: examined %d health checks", rep.AsOf, rep.Examined)
	for _, h := range []int{1, 3, 5, 10, 20} {
		if n := rep.Filled[h]; n > 0 {
			log.Printf("  %2dd: filled %d", h, n)
		}
	}
	log.Printf("  already known: %d", rep.AlreadyKnown)
	log.Printf("  skipped: %d duplicate (same symbol+day), %d no basis price, "+
		"%d no series, %d too recent",
		rep.SkippedDuplicate, rep.SkippedNoBase, rep.SkippedNoPrice, rep.SkippedTooEarly)
	for _, e := range rep.Errors {
		log.Printf("  error: %s", e)
	}
}

// hasAPIKey reports whether the environment carries a key, WITHOUT reading its value.
func hasAPIKey() bool { return os.Getenv("OPENAI_API_KEY") != "" }

func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// normaliseAddr makes ":8080" printable as a URL.
func normaliseAddr(addr string) string {
	if addr == "" {
		return ":8080"
	}
	return addr
}
