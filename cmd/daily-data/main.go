// Command daily-data runs the once-a-day acquisition every other layer depends on.
//
//	go run ./cmd/daily-data                    # the last closed session
//	go run ./cmd/daily-data -date 2026-09-04   # a specific session
//	go run ./cmd/daily-data -dry-run           # decide the session, fetch nothing
//	make daily-data
//
// # Why a scheduled job rather than a side effect
//
// Two sources here are EXACT_DATE: the market and news snapshots are named by the day they
// were observed and have no fallback to the day before. A day nobody ran anything is a
// permanent hole — a date that can never be answered, not merely stale. Valuation fails more
// quietly: every skipped session is one fewer P/E sample, forever.
//
// So acquisition must not depend on somebody opening a dashboard. This is that job.
//
// # Isolation
//
// It uses the RESEARCH price cache, never the scanner's, and refuses to start if pointed at
// it. internal/fetcher writes what it fetches, so a run during trading hours sharing that
// directory would put a half-formed daily bar into the series the next scan reads.
//
// SHADOW-ONLY: it writes data/ archives and the research database. BUY / WATCH / SELL, the
// rocket score and every ranking are untouched.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/healthcheck"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// defaultCacheDir is the RESEARCH price cache — deliberately not the scanner's.
const defaultCacheDir = ".cache-research"

// probeSymbol is the liquid, always-trading stock used to tell a holiday from a trading day.
const probeSymbol = "2330"

type config struct {
	Fetcher   fetcher.Config  `yaml:"fetcher"`
	Scanner   scanner.Config  `yaml:"scanner"`
	Dashboard dashboardConfig `yaml:"health_dashboard"`
	Report    struct {
		OutputDir string `yaml:"output_dir"`
	} `yaml:"report"`
}

type dashboardConfig struct {
	History healthcheck.HistoryConfig `yaml:"history"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "config file path")
	candidatesPath := flag.String("candidates", "candidate.yaml", "acquisition universe")
	cacheDir := flag.String("cache-dir", defaultCacheDir,
		"price cache directory; MUST NOT be the scanner's")
	dateStr := flag.String("date", "", "trading session to acquire (YYYY-MM-DD); default: the last closed one")
	reportPath := flag.String("report", "", "write the completeness report as JSON to this exact path")
	reportDirFlag := flag.String("report-dir", "",
		"write the report into this directory, named for the SESSION acquired")
	dryRun := flag.Bool("dry-run", false, "resolve the session and exit without fetching")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// The one decision that keeps this job a sidecar.
	fetcherCfg, choice, err := buildFetcherConfig(cfg, *cacheDir)
	if err != nil {
		log.Fatalf("%v", err)
	}

	list, err := candidate.Load(*candidatesPath)
	if err != nil {
		log.Fatalf("candidate config: %v", err)
	}
	symbols := make([]string, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		symbols = append(symbols, c.CanonicalSymbol())
	}
	if len(symbols) == 0 {
		log.Fatalf("no candidates configured in %s", *candidatesPath)
	}

	// One clock reading for the whole run, so every derived date is consistent even across a
	// midnight boundary.
	now := time.Now()

	// A dry run returns BEFORE a Fetcher exists.
	//
	// Suppressing the probe inside resolveSession is not enough on its own: nothing observable
	// from outside distinguishes "probed" from "did not", because internal/fetcher writes its
	// cache in a goroutine and a process that exits promptly leaves no file either way. Not
	// constructing the Fetcher makes the difference concrete — fetcher.New MkdirAll's the
	// cache directory, so a dry run that has one is a dry run that did too much, and a test
	// can simply look.
	if *dryRun {
		state := resolveSession(*dateStr, now, nil, true)
		log.Printf("session %s  %s%s", state.Date, state.Session, reasonSuffix(state.Reason))
		log.Printf("universe %s (%d 檔)  price cache %s (未建立)",
			*candidatesPath, len(symbols), choice.Dir)
		log.Printf("dry-run：不抓取任何資料（未向價格來源確認是否為交易日）")
		return
	}

	f := fetcher.New(fetcherCfg)
	prices := healthcheck.FetcherPrices{F: f}

	state := resolveSession(*dateStr, now, prices, false)
	log.Printf("session %s  %s%s", state.Date, state.Session, reasonSuffix(state.Reason))
	log.Printf("universe %s (%d 檔)  price cache %s (掃描器的 %s 不會被寫入)",
		*candidatesPath, len(symbols), choice.Dir, choice.Reserved)

	instCfg := cfg.Scanner.Institution.Defaulted()
	deps := dailydata.Deps{
		Symbols:            symbols,
		Prices:             prices,
		ValuationDir:       valuation.DefaultDir(),
		FundamentalDir:     fundamental.DefaultDir(),
		InstitutionDir:     instCfg.SnapshotDir,
		InstitutionTimeout: 30 * time.Second,
		Logf:               log.Printf,
	}
	if err := dailydata.EnsureDirs(deps.ValuationDir, deps.FundamentalDir,
		deps.InstitutionDir); err != nil {
		log.Fatalf("%v", err)
	}

	// Outcome grading needs the history database. Absent, the step reports SKIPPED rather
	// than failing the run: a day's data is worth acquiring whether or not anything is
	// being graded against it.
	if hist := openHistory(cfg); hist != nil {
		defer func() { _ = hist.Close() }()
		deps.Outcomes = &healthcheck.Outcomes{
			History: hist, Prices: prices, Catalog: loadCatalog(),
			Logf: log.Printf,
		}
	} else {
		log.Printf("history: 未啟用，outcome 評分將跳過")
	}

	reportDir := cfg.Report.OutputDir
	if reportDir == "" {
		reportDir = "reports"
	}

	p := &dailydata.Pipeline{
		Logf: log.Printf,
		Steps: []dailydata.Step{
			dailydata.PriceStep(deps),
			dailydata.ValuationStep(deps),
			dailydata.FundamentalStep(deps),
			dailydata.InstitutionStep(deps),
			dailydata.NewsStep(reportDir),
			dailydata.OutcomeStep(deps),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("中斷 — 已寫入的封存保留，重跑會補齊其餘來源")
		cancel()
	}()

	log.Printf("── 取得 %s 的資料 ──", state.Date)
	// Day.Symbols is the single universe. Deps carries its own copy for the steps that
	// close over it, and they are the same slice — set once, here, so the two cannot drift
	// into disagreeing about what was expected.
	rep := p.Run(ctx, dailydata.Day{
		Date: state.Date, Now: now, Session: state.Session, Symbols: deps.Symbols,
	})

	printReport(rep)
	// Named for the SESSION, not the host's calendar day. A repair run at 01:00 for the
	// previous session would otherwise overwrite that morning's report with a different
	// day's results, under a filename that named neither of them correctly.
	path := *reportPath
	if path == "" && *reportDirFlag != "" {
		path = filepath.Join(*reportDirFlag,
			"daily_data_"+strings.ReplaceAll(rep.Date, "-", "")+".json")
	}
	if path != "" {
		if err := writeReport(path, rep); err != nil {
			log.Printf("report: %v", err)
		} else {
			log.Printf("report: %s", path)
		}
	}

	// The exit code reflects the verdict, but it is NOT the completeness signal — the report
	// is. A scheduler that only checks the exit code would treat PARTIAL as fine, which is
	// exactly the assumption this job replaces.
	switch rep.Status {
	case dailydata.StatusFailed:
		os.Exit(1)
	case dailydata.StatusPartial:
		os.Exit(2)
	}
}

// buildFetcherConfig resolves the price cache and returns the config the fetcher is built
// from.
//
// It returns the CONFIG rather than mutating one, so main cannot construct a Fetcher without
// having gone through the guard: the only fetcher.Config in this binary comes out of here.
// That matters more than it looks — the previous shape put the guard in main and the tests on
// the helper, so deleting main's call left the whole suite green. Testing the helper is not
// testing the wiring.
func buildFetcherConfig(cfg config, requested string) (fetcher.Config, fetcher.CacheChoice, error) {
	choice, err := fetcher.ResolveCacheDir(requested, cfg.Fetcher.CacheDir, defaultCacheDir)
	if err != nil {
		return fetcher.Config{}, choice, fmt.Errorf(
			"%w\n(that directory is the scanner's price cache — use a different -cache-dir)", err)
	}
	out := cfg.Fetcher
	out.CacheDir = choice.Dir
	return out, choice, nil
}

// resolveSession picks the session, honouring an explicit -date.
//
// dryRun suppresses the price probe. ConfirmSession fetches a real series to tell a holiday
// from a trading day, so without this a "dry run" would do network I/O and write the cache —
// the one thing the flag promises it will not do.
//
// The suppression lives HERE rather than at the call site because a caller can forget to
// apply it and nothing fails: the fetcher writes its cache in a goroutine, so a process that
// exits promptly leaves no file behind, and a subprocess test looking for one sees nothing
// either way. Put the decision where it can be tested with a counting stub instead.
func resolveSession(explicit string, now time.Time, prices dailydata.PriceSource,
	dryRun bool) dailydata.SessionState {
	if dryRun {
		prices = nil
	}
	probe := fetcher.StockInfo{Symbol: probeSymbol, Market: "TW"}
	if explicit != "" {
		if _, err := time.ParseInLocation("2006-01-02", explicit, dailydata.Taipei); err != nil {
			log.Fatalf("bad -date %q: %v", explicit, err)
		}
		// An explicit date is still confirmed against the data: asking for a holiday should
		// be a no-op, not a day of failures.
		return dailydata.ConfirmSession(
			dailydata.SessionState{Date: explicit, Session: dailydata.SessionClosed},
			prices, probe)
	}
	return dailydata.ConfirmSession(dailydata.Resolve(now), prices, probe)
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return "  — " + r
}

func openHistory(cfg config) *healthcheck.History {
	hc := cfg.Dashboard.History
	if !hc.Enabled {
		return nil
	}
	if hc.Path == "" {
		log.Printf("history: enabled 但未設定 path，跳過（不會落到正式 R13 資料庫）")
		return nil
	}
	h, err := healthcheck.OpenHistory(hc)
	if err != nil {
		log.Printf("history: %v — outcome 評分將跳過", err)
		return nil
	}
	return h
}

func loadCatalog() *stockcatalog.Catalog {
	c, err := stockcatalog.Load(stockcatalog.Sources{OverlayFile: stockcatalog.DefaultOverlayFile})
	if err != nil {
		log.Printf("catalog: %v — 市場別將由 fetcher 自行偵測", err)
		return nil
	}
	return c
}

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

// printReport writes the human-readable summary.
func printReport(rep *dailydata.Report) {
	fmt.Printf("\n%s  %s  session=%s\n", rep.Date, rep.Status, rep.MarketSession)
	fmt.Println(strings.Repeat("─", 92))
	fmt.Printf("%-13s %-11s %9s %9s  %s\n", "source", "status", "expected", "available", "detail")
	for _, s := range rep.Sources {
		exp, avail := "—", "—"
		if s.Expected > 0 {
			exp = fmt.Sprintf("%d", s.Expected)
			avail = fmt.Sprintf("%d", s.Available)
		}
		detail := s.Note
		if s.Reason != "" {
			if detail != "" {
				detail += "　"
			}
			detail += s.Reason
		}
		if len(detail) > 46 {
			detail = detail[:46] + "…"
		}
		fmt.Printf("%-13s %-11s %9s %9s  %s\n", s.Name, s.Status, exp, avail, detail)
		if len(s.Missing) > 0 {
			show := s.Missing
			if len(show) > 6 {
				show = show[:6]
			}
			fmt.Printf("%-13s   缺: %s%s\n", "", strings.Join(show, " "),
				more(len(s.Missing), len(show)))
		}
	}
	fmt.Println()
	switch rep.Status {
	case dailydata.StatusComplete:
		fmt.Println("COMPLETE —— 這一天的資料齊了。")
	case dailydata.StatusPartial:
		fmt.Println("PARTIAL —— 部分來源尚未取得。稍後重跑會補齊，已成功的不會重做。")
	case dailydata.StatusFailed:
		fmt.Println("FAILED —— 沒有任何來源成功。先確認網路與來源狀態再重跑。")
	case dailydata.StatusNoSession:
		fmt.Println("NO_SESSION —— 非交易日，沒有資料可取。這是正常的 no-op。")
	}
}

func more(total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf(" …+%d", total-shown)
}

func writeReport(path string, rep *dailydata.Report) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
