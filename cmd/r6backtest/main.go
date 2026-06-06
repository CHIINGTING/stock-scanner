// Command r6backtest runs the R6 pullback / crash-low entry backtest off the
// existing .cache and writes a per-trade CSV + markdown summary into reports/.
// Decision-support only: no live scanner/report/scoring is touched, no orders.
//
// R6-2a: framework skeleton — universe + RS panel + engine + output schema wired
// end-to-end. No real setups (A/B/C/D) are registered yet; those arrive in
// R6-2b…d. Running now produces a header-only CSV and a framework summary so the
// schema and runtime are inspectable.
package main

import (
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/deep-huang/stock-scanner/internal/r6backtest"
)

func main() {
	cacheDir := flag.String("cache", ".cache", "OHLCV cache directory (read-only)")
	outDir := flag.String("out", "reports", "output directory (gitignored)")
	minBars := flag.Int("min-bars", 130, "drop symbols with fewer bars")
	stopBench := flag.Bool("stopbench", false, "R6-3: run stop-policy benchmark instead of the default backtest")
	flag.Parse()

	t0 := time.Now()
	u, err := r6backtest.LoadUniverse(*cacheDir, *minBars, nil, nil)
	if err != nil {
		log.Fatalf("load universe: %v", err)
	}
	if len(u.Stocks) == 0 {
		log.Fatalf("no cached stocks found in %s — populate .cache first", *cacheDir)
	}
	fmt.Printf("universe: %d stocks, axis %s → %s (%d days), loaded in %v\n",
		len(u.Stocks), u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis), time.Since(t0))

	p := r6backtest.DefaultParams()
	tRS := time.Now()
	rs := r6backtest.BuildRSPanel(u, p)
	fmt.Printf("RS panel: %d dates in %v\n", rs.Dates(), time.Since(tRS))

	// R6-2b: Setup A (MA20/MA60) + Setup B (pullback-depth sweep). C/D arrive later.
	var setups []r6backtest.Setup
	setups = append(setups, r6backtest.SetupAVariants()...)
	setups = append(setups, r6backtest.SetupBBuckets()...)

	stamp := time.Now().Format("20060102")

	// ── R6-3 stop-policy benchmark (separate output; baseline unchanged) ──
	if *stopBench {
		policies := r6backtest.BenchmarkStopPolicies()
		tB := time.Now()
		benchStats := r6backtest.RunStopBenchmark(u, rs, setups, policies, p)
		csv := filepath.Join(*outDir, "backtest_stop_benchmark_"+stamp+".csv")
		md := filepath.Join(*outDir, "backtest_stop_benchmark_summary_"+stamp+".md")
		if err := r6backtest.WriteBenchmarkCSV(csv, benchStats); err != nil {
			log.Fatalf("write benchmark csv: %v", err)
		}
		meta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
			fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
			fmt.Sprintf("warmup: %d ; horizons: %v ; entry: %s", p.Warmup, p.Horizons, p.EntryMode),
			fmt.Sprintf("policies compared: %d ; setups: %d", len(policies), len(setups)),
		}
		if err := r6backtest.WriteBenchmarkMarkdown(md, "R6-3 Stop Policy Benchmark", meta, benchStats); err != nil {
			log.Fatalf("write benchmark md: %v", err)
		}
		fmt.Printf("stop benchmark: %d rows (%d setups × %d policies) in %v\nwrote %s and %s\n",
			len(benchStats), len(setups), len(policies), time.Since(tB), csv, md)
		return
	}

	var allTrades []r6backtest.Trade
	var stats []r6backtest.SetupStat
	for _, s := range setups {
		tRun := time.Now()
		trades := r6backtest.RunSetup(u, rs, s, p)
		bucket := 0
		if len(trades) > 0 {
			bucket = trades[0].Bucket
		}
		allTrades = append(allTrades, trades...)
		stats = append(stats, r6backtest.ComputeStats(s.Name(), bucket, trades, p.Horizons, p))
		fmt.Printf("  %-18s %5d trades  (%v)\n", s.Name(), len(trades), time.Since(tRun))
	}

	csvPath := filepath.Join(*outDir, "backtest_pullback_"+stamp+".csv")
	mdPath := filepath.Join(*outDir, "backtest_pullback_summary_"+stamp+".md")

	if err := r6backtest.WriteCSV(csvPath, allTrades); err != nil {
		log.Fatalf("write csv: %v", err)
	}
	meta := []string{
		fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
		fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
		fmt.Sprintf("RS panel dates: %d (lookback %dd)", rs.Dates(), p.RSLookbackDays),
		fmt.Sprintf("warmup: %d bars (52w) ; horizons: %v ; entry: %s", p.Warmup, p.Horizons, p.EntryMode),
		"R6-2b: Setup A (MA20/MA60) + Setup B (pullback sweep) wired. Setup C/D not yet.",
		"60d-horizon samples are fewer than 5/10/20d (forward window limited by data end).",
	}
	if err := r6backtest.WriteMarkdown(mdPath, "R6 Pullback Backtest", meta, stats, p.Horizons); err != nil {
		log.Fatalf("write md: %v", err)
	}
	fmt.Printf("wrote %s (%d trades) and %s\n", csvPath, len(allTrades), mdPath)
}
