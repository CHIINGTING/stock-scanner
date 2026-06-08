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
	"strings"
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

	// R6-2b: Setup A/B ; R6-2c: Setup C (real VCP retest). D arrives later.
	var setups []r6backtest.Setup
	setups = append(setups, r6backtest.SetupAVariants()...)
	setups = append(setups, r6backtest.SetupBBuckets()...)
	setups = append(setups, r6backtest.SetupCVariants()...)

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
	var vcpGroups []r6backtest.VCPGroup
	for _, s := range setups {
		tRun := time.Now()
		trades := r6backtest.RunSetup(u, rs, s, p)
		bucket := 0
		if len(trades) > 0 {
			bucket = trades[0].Bucket
		}
		allTrades = append(allTrades, trades...)
		stats = append(stats, r6backtest.ComputeStats(s.Name(), bucket, trades, p.Horizons, p))
		if strings.HasPrefix(s.Name(), "C_VCP") { // R6-2c grade/quality grouping
			vcpGroups = append(vcpGroups, r6backtest.VCPGroupStats(s.Name(), trades, p.Horizons, p))
		}
		fmt.Printf("  %-22s %5d trades  (%v)\n", s.Name(), len(trades), time.Since(tRun))
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

	if len(vcpGroups) > 0 { // R6-2c: Setup C grade / quality grouping summary
		vcpMd := filepath.Join(*outDir, "backtest_vcp_groups_summary_"+stamp+".md")
		gmeta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
			fmt.Sprintf("coverage: %s → %s", u.Axis[0], u.Axis[len(u.Axis)-1]),
			fmt.Sprintf("base_low proxy: min Low over %d bars (NOT a ComputeVCP contraction trough)", p.BaseLowLookback),
		}
		if err := r6backtest.WriteVCPGroupMarkdown(vcpMd, "R6-2c Setup C — VCP Grade / Quality Groups", gmeta, vcpGroups, p.Horizons); err != nil {
			log.Fatalf("write vcp groups md: %v", err)
		}
		fmt.Printf("wrote %s\n", vcpMd)
	}

	// ── R6-2d Setup D: crash-regime survivor CASE STUDY (warmup 120, LOW conf) ──
	// Run separately from the generic loop: needs the regime panel, forces LOW
	// confidence, and writes its own outputs. Not part of A/B/C or the benchmark.
	regime := r6backtest.BuildRegimePanel(u, -8.0)
	if !regime.ProxyOK {
		fmt.Printf("Setup D skipped: market proxy %s not in cache\n", r6backtest.ProxySymbol)
		return
	}
	dp := p
	dp.Warmup = 120
	dp.ForceLowConfidence = true
	dMain := r6backtest.RunSetup(u, rs, r6backtest.SetupD{Regime: regime, RelThreshold: 5}, dp)
	dStat := r6backtest.ComputeStats("D_CRASH_SURVIVOR", 0, dMain, dp.Horizons, dp)
	// faithful event_count = distinct regime events that actually produced trades
	// (pre-warmup events that yielded no entries are excluded).
	evCount, evRange := r6backtest.DistinctCrashEvents(dMain)
	dStat.EventCount = evCount
	dStat.RegimeDateRange = evRange
	dStat.ProxySymbol = r6backtest.ProxySymbol
	high, low := r6backtest.RunCrashCohorts(u, rs, regime, dp)

	crashCSV := filepath.Join(*outDir, "backtest_crash_survivors_"+stamp+".csv")
	crashMd := filepath.Join(*outDir, "backtest_crash_survivors_summary_"+stamp+".md")
	if err := r6backtest.WriteCrashSurvivorsCSV(crashCSV, dMain); err != nil {
		log.Fatalf("write crash csv: %v", err)
	}
	dmeta := []string{
		fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
		fmt.Sprintf("coverage: %s → %s", u.Axis[0], u.Axis[len(u.Axis)-1]),
		fmt.Sprintf("regime: %s 20d return ≤ -8%% ; warmup %d ; horizons %v (60d auxiliary)", r6backtest.ProxySymbol, dp.Warmup, dp.Horizons),
		"relative_return_vs_market_20d ≥ +5pp ; breadth_below_ma20 = context only (not a hard gate)",
		fmt.Sprintf("regime events detected in full series: %d (pre-warmup events yield no entries)", regime.EventCount),
	}
	if err := r6backtest.WriteCrashSummary(crashMd, "R6-2d Setup D — Crash-Regime Survivor Case Study",
		dmeta, dStat, r6backtest.AvgRelativeReturn(dMain), r6backtest.AvgProxyReturn(dMain),
		high, low, evCount, evRange, dp.Horizons); err != nil {
		log.Fatalf("write crash summary: %v", err)
	}
	fmt.Printf("Setup D: event_count=%d range=%s ; D_CRASH_SURVIVOR=%d trades ; cohort HIGH=%d LOW=%d\n",
		evCount, evRange, len(dMain), high.Stat.SampleCount, low.Stat.SampleCount)
	fmt.Printf("wrote %s and %s\n", crashCSV, crashMd)
}
