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
	recentBull := flag.Bool("recentbull", false, "R6-6: recent bull regime validation (20d primary, signal-date 2m/4m/6m windows)")
	trendExt := flag.Bool("trendext", false, "R12: slope / BIAS / sector-heat condition comparison (baseline vs each leg vs combined states)")
	priceVol := flag.Bool("pricevolume", false, "Flat-price × volume: measures whether an exactly-unchanged session behaves like DOWN, like UP, or like neither, before scorer.go decides")
	technical := flag.Bool("technical", false, "R14: ADX / RSI / MACD / Keltner / pivot condition comparison (baseline vs each indicator vs confirmed combinations)")
	sectors := flag.String("sectors", "configs/sectors.yaml", "R12: sector taxonomy for the heat panel (same file the scanner uses)")
	heatStep := flag.Int("heatstep", 5, "R12: recompute the sector heat panel every N trading days (heat moves slowly; 1 = every day)")
	flag.Parse()

	t0 := time.Now()
	// R12 needs the sector taxonomy to build the heat panel. It is loaded from the SAME
	// configs/sectors.yaml the scanner uses — there is no second sector mapping. Other modes
	// pass nil exactly as before, so their universes are unchanged.
	var sectorOf map[string]string
	if *trendExt {
		m, err := r6backtest.LoadSectorMap(*sectors)
		if err != nil {
			log.Fatalf("load sectors: %v", err)
		}
		sectorOf = m
		fmt.Printf("sector map: %d symbols from %s\n", len(sectorOf), *sectors)
	}
	u, err := r6backtest.LoadUniverse(*cacheDir, *minBars, nil, sectorOf)
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

	// ── R12 trend/extension condition comparison (separate output; baseline unchanged) ──
	if *trendExt {
		tH := time.Now()
		panel := r6backtest.BuildHeatPanel(u, *heatStep)
		fmt.Printf("sector heat panel: %d dates (step %d) in %v\n", len(u.Axis), *heatStep, time.Since(tH))

		teSetups := r6backtest.TrendExtSetups(u, panel)
		var teStats []r6backtest.SetupStat
		var teTrades []r6backtest.Trade
		for _, st := range teSetups {
			tRun := time.Now()
			trades := r6backtest.RunSetup(u, rs, st, p)
			teTrades = append(teTrades, trades...)
			teStats = append(teStats, r6backtest.ComputeStats(st.Name(), 0, trades, p.Horizons, p))
			fmt.Printf("  %-28s %6d trades  (%v)\n", st.Name(), len(trades), time.Since(tRun))
		}
		csv := filepath.Join(*outDir, "backtest_trendext_"+stamp+".csv")
		md := filepath.Join(*outDir, "backtest_trendext_summary_"+stamp+".md")
		if err := r6backtest.WriteCSV(csv, teTrades); err != nil {
			log.Fatalf("write trendext csv: %v", err)
		}
		meta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only) ; sectors: %d symbols mapped", len(u.Stocks), len(sectorOf)),
			fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
			fmt.Sprintf("warmup: %d ; horizons: %v ; entry: %s ; stops: %v", p.Warmup, p.Horizons, p.EntryMode, p.StopRules),
			fmt.Sprintf("heat panel step: %d trading days", *heatStep),
			"thresholds: read from scanner.DefaultTrendExtThresholds() — the SHIPPED values, not a tuned copy",
			"READ AS DIFFERENCES FROM BASELINE_ALL_BARS: the universe is survivorship-biased (delisted names absent), so absolute returns are optimistic for every row alike",
			"the falsifiable claim: STATE_PULLBACK_IN_UPTREND and STATE_EXTENDED must differ in forward return AND downside — if they do not, the state machine is not describing a real distinction",
		}
		if err := r6backtest.WriteMarkdown(md, "R12 Trend / BIAS / Sector-Heat Condition Comparison", meta, teStats, p.Horizons); err != nil {
			log.Fatalf("write trendext md: %v", err)
		}
		fmt.Printf("trendext: %d conditions, %d trades\nwrote %s and %s\n", len(teStats), len(teTrades), csv, md)
		return
	}

	// ── Flat-price × volume semantics (separate output; baseline unchanged) ──
	//
	// The label correction (a 0.00% close is not a decline) shipped on its own because it
	// fixed a false statement. What a flat session should be WORTH is measured here first.
	if *priceVol {
		var pvStats []r6backtest.SetupStat
		var pvTrades []r6backtest.Trade
		for _, st := range r6backtest.PVSetups() {
			tRun := time.Now()
			trades := r6backtest.RunSetup(u, rs, st, p)
			pvTrades = append(pvTrades, trades...)
			pvStats = append(pvStats, r6backtest.ComputeStats(st.Name(), 0, trades, p.Horizons, p))
			fmt.Printf("  %-30s %6d trades  (%v)\n", st.Name(), len(trades), time.Since(tRun))
		}
		csv := filepath.Join(*outDir, "backtest_pricevolume_"+stamp+".csv")
		md := filepath.Join(*outDir, "backtest_pricevolume_summary_"+stamp+".md")
		if err := r6backtest.WriteCSV(csv, pvTrades); err != nil {
			log.Fatalf("write pricevolume csv: %v", err)
		}
		meta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
			fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
			fmt.Sprintf("warmup: %d ; horizons: %v ; entry: %s ; stops: %v", p.Warmup, p.Horizons, p.EntryMode, p.StopRules),
			"FLAT means EXACTLY zero change — the same definition the shipped label correction uses",
			"volume bands 1.2 / 0.8 are the SHIPPED ones (analyzeVolume and its score table), not a private copy",
			"READ AS DIFFERENCES FROM PV_BASELINE_ALL_BARS: the universe is survivorship-biased, so absolute returns are optimistic for every row alike",
			"the question: does PV_FLAT_VOLUME_EXPANSION behave like PV_DOWN_VOLUME_EXPANSION (candidate A: keep scoring flat as down), like PV_UP_VOLUME_EXPANSION (candidate C), or like neither (candidate B: its own neutral bucket)?",
			"PV_NEAR_FLAT_* rows exist to test the band the correction deliberately did NOT widen: if a −0.3% session behaves like an exactly-flat one, a wider neutral band is worth proposing; if it behaves like the decline it is currently called, the strict definition was right",
			"no scoring change may be made from this run without the difference being larger than the noise these overlapping bar-level observations can support",
		}
		if err := r6backtest.WriteMarkdown(md, "Flat-Price × Volume Semantics", meta, pvStats, p.Horizons); err != nil {
			log.Fatalf("write pricevolume md: %v", err)
		}
		fmt.Printf("pricevolume: %d conditions, %d trades\nwrote %s and %s\n", len(pvStats), len(pvTrades), csv, md)
		return
	}

	// ── R14 technical indicator validation (separate output; baseline unchanged) ──
	//
	// The premise is that every one of these is a HYPOTHESIS until this repo's own history
	// says otherwise. The baseline and the single-indicator legs are in the comparison so a
	// combination cannot claim credit that belongs to one of its parts.
	if *technical {
		tP := time.Now()
		panel := r6backtest.BuildTechnicalPanel(u)
		fmt.Printf("technical panel: %d stocks x %d dates in %v\n", len(u.Stocks), len(u.Axis), time.Since(tP))

		var tStats []r6backtest.SetupStat
		var tTrades []r6backtest.Trade
		for _, st := range r6backtest.TechnicalSetups(panel) {
			tRun := time.Now()
			trades := r6backtest.RunSetup(u, rs, st, p)
			tTrades = append(tTrades, trades...)
			tStats = append(tStats, r6backtest.ComputeStats(st.Name(), 0, trades, p.Horizons, p))
			fmt.Printf("  %-34s %6d trades  (%v)\n", st.Name(), len(trades), time.Since(tRun))
		}
		csv := filepath.Join(*outDir, "backtest_technical_"+stamp+".csv")
		md := filepath.Join(*outDir, "backtest_technical_summary_"+stamp+".md")
		if err := r6backtest.WriteCSV(csv, tTrades); err != nil {
			log.Fatalf("write technical csv: %v", err)
		}
		meta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
			fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
			fmt.Sprintf("warmup: %d ; horizons: %v ; entry: %s ; stops: %v", p.Warmup, p.Horizons, p.EntryMode, p.StopRules),
			"parameters: read from technical.DefaultConfig() — the SHIPPED values, not a tuned copy",
			"READ AS DIFFERENCES FROM BASELINE_ALL_BARS: the universe is survivorship-biased (delisted names absent), so absolute returns are optimistic for every row alike",
			"the falsifiable claims: ADX_STRONG_BULLISH must beat ADX_WEAK; MACD_ABOVE_SIGNAL_ACCELERATING must beat MACD_ABOVE_SIGNAL; KELTNER_BREAKOUT_ADX_CONFIRMED must beat KELTNER_BREAKOUT_UNCONFIRMED; RSI_OVERBOUGHT decides continuation vs mean reversion on its own numbers",
			"nothing here may be used to tune a threshold: the parameters are fixed before the run, and a result that falsifies a hypothesis is the point of the exercise",
		}
		if err := r6backtest.WriteMarkdown(md, "R14 Technical Indicator Condition Comparison", meta, tStats, p.Horizons); err != nil {
			log.Fatalf("write technical md: %v", err)
		}
		fmt.Printf("technical: %d conditions, %d trades\nwrote %s and %s\n", len(tStats), len(tTrades), csv, md)
		return
	}

	// ── R6-6 recent bull regime validation (separate output; 20d primary) ──
	// NOT cross-regime. Slices the same A/B/C entries into recent signal-date
	// windows and judges them on the 20d horizon with 20d-maturity accounting.
	// Setup D is excluded (no crash regime in a bull window).
	if *recentBull {
		windows := r6backtest.DefaultRecentBullWindows(u.Axis)
		policies := r6backtest.BenchmarkStopPolicies()
		tB := time.Now()
		cells := r6backtest.RunRecentBull(u, rs, setups, policies, p, windows)
		csv := filepath.Join(*outDir, "backtest_recent_bull_"+stamp+".csv")
		md := filepath.Join(*outDir, "backtest_recent_bull_summary_"+stamp+".md")
		if err := r6backtest.WriteRecentBullCSV(csv, cells); err != nil {
			log.Fatalf("write recent-bull csv: %v", err)
		}
		cutoff := ""
		if len(u.Axis) > r6backtest.PrimaryHorizon {
			cutoff = u.Axis[len(u.Axis)-1-r6backtest.PrimaryHorizon]
		}
		meta := []string{
			fmt.Sprintf("universe: %d stocks (cache, read-only)", len(u.Stocks)),
			fmt.Sprintf("coverage: %s → %s (%d trading days)", u.Axis[0], u.Axis[len(u.Axis)-1], len(u.Axis)),
			fmt.Sprintf("last_available_date: %s ; 20d_maturity_cutoff: %s (signals after this = UNMATURED_20D)", u.Axis[len(u.Axis)-1], cutoff),
			fmt.Sprintf("warmup: %d ; entry: %s ; setups: %d (A/B/C; Setup D excluded — no crash regime) ; policies: %d", p.Warmup, p.EntryMode, len(setups), len(policies)),
			"primary_horizon: 20d ; 5d/10d = early reaction ; 60d = optional reference",
		}
		anchors := []string{"A_MA20_PULLBACK", "B_PULLBACK_10", "C_VCP_MA20_RETEST"}
		if err := r6backtest.WriteRecentBullMarkdown(md, "R6-6 Recent Bull Regime Validation", meta, cells, windows, "BASELINE", anchors); err != nil {
			log.Fatalf("write recent-bull md: %v", err)
		}
		fmt.Printf("recent bull: %d cells (%d setups × %d policies × %d windows) in %v\nwrote %s and %s\n",
			len(cells), len(setups), len(policies), len(windows), time.Since(tB), csv, md)
		return
	}

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
