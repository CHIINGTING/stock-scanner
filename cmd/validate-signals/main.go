// Command validate-signals produces a Signal Validation Report from the daily
// HTML reports the scanner has already generated.
//
// It answers: did the stocks the scanner told us to BUY/ADD actually rise, and did
// the ones it told us to REDUCE/SELL actually fall or lag the market? It respects
// each report's original judgment (no look-ahead re-scoring) and reads forward
// prices from the project's existing .cache OHLCV store.
//
// Example:
//
//	go run ./cmd/validate-signals \
//	  --reports-dir reports \
//	  --from 2026-06-01 --to 2026-06-13 \
//	  --horizons 3,5,10,20 \
//	  --output reports/signal_validation_20260706.html \
//	  --csv-output data/signal_validation_20260706.csv \
//	  --snapshot-output data/signal_snapshots_20260706.csv \
//	  --benchmark 0050
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/validator"
)

func main() {
	var (
		reportsDir     = flag.String("reports-dir", "reports", "directory of report_YYYYMMDD.html files")
		fromStr        = flag.String("from", "", "start date YYYY-MM-DD (inclusive)")
		toStr          = flag.String("to", "", "end date YYYY-MM-DD (inclusive)")
		horizonsStr    = flag.String("horizons", "3,5,10,20", "forward trading-day horizons, comma-separated")
		output         = flag.String("output", "", "HTML report output path")
		csvOutput      = flag.String("csv-output", "", "validation detail CSV output path")
		snapshotOutput = flag.String("snapshot-output", "", "optional parsed-snapshot CSV output path")
		benchmark      = flag.String("benchmark", "0050", "benchmark code for excess return (e.g. 0050 / 006208); blank to disable")
		cacheDir       = flag.String("cache-dir", ".cache", "OHLCV cache directory (forward price source)")
		tabs           = flag.String("tabs", "watchlist,positions,market", "report tabs to validate, comma-separated (blank = all)")
		marketBuyOnly  = flag.Bool("market-buy-only", true, "from the market-scan tab, keep only BUY picks (skip universe-wide SELL/HOLD)")
	)
	flag.Parse()

	from, err := parseDate(*fromStr)
	if err != nil {
		log.Fatalf("--from: %v", err)
	}
	to, err := parseDate(*toStr)
	if err != nil {
		log.Fatalf("--to: %v", err)
	}
	if to.Before(from) {
		log.Fatalf("--to (%s) is before --from (%s)", *toStr, *fromStr)
	}
	horizons, err := parseHorizons(*horizonsStr)
	if err != nil {
		log.Fatalf("--horizons: %v", err)
	}
	if *output == "" && *csvOutput == "" && *snapshotOutput == "" {
		log.Fatal("nothing to do: provide at least one of --output, --csv-output, --snapshot-output")
	}

	// 1. Parse historical reports (respecting original judgments; no re-scoring).
	snaps, err := validator.ParseReportsDir(*reportsDir, from, to)
	if err != nil {
		log.Fatalf("parse reports: %v", err)
	}
	log.Printf("parsed %d raw signals from %s (%s → %s)", len(snaps), *reportsDir, *fromStr, *toStr)

	// 2. Filter to the operational judgments.
	filter := validator.FilterOptions{Tabs: validator.ParseTabs(*tabs), MarketBuyOnly: *marketBuyOnly}
	snaps = filter.Apply(snaps)
	log.Printf("validating %d signals after tab filter (tabs=%q, market-buy-only=%v)", len(snaps), *tabs, *marketBuyOnly)

	// 3. Evaluate forward returns from the existing price cache.
	store := validator.NewPriceStore(*cacheDir)
	bench := validator.NewBenchmark(store, *benchmark)
	eval := &validator.Evaluator{Store: store, Benchmark: bench, Horizons: horizons}
	results := eval.EvaluateAll(snaps)

	// Enrich snapshots with the signal-day reference price we resolved (for the
	// snapshot CSV), taken from the evaluated results.
	for i := range results {
		if i < len(snaps) {
			snaps[i].SignalPrice = results[i].Signal.SignalPrice
		}
	}

	// 4. Outputs.
	if *snapshotOutput != "" {
		if err := validator.WriteSnapshotsCSV(*snapshotOutput, snaps); err != nil {
			log.Fatalf("write snapshot csv: %v", err)
		}
		log.Printf("wrote %s", *snapshotOutput)
	}
	if *csvOutput != "" {
		if err := validator.WriteValidationCSV(*csvOutput, results, horizons); err != nil {
			log.Fatalf("write validation csv: %v", err)
		}
		log.Printf("wrote %s", *csvOutput)
	}
	if *output != "" {
		meta := validator.ReportMeta{
			From:             from,
			To:               to,
			Horizons:         horizons,
			Benchmark:        *benchmark,
			BenchmarkEnabled: bench.Enabled(),
			FilterDesc:       filterDesc(*tabs, *marketBuyOnly),
			GeneratedAt:      time.Now(),
		}
		if err := validator.WriteHTMLReport(*output, meta, results); err != nil {
			log.Fatalf("write html report: %v", err)
		}
		log.Printf("wrote %s", *output)
	}

	printConsoleSummary(results)
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("required (YYYY-MM-DD)")
	}
	return time.Parse("2006-01-02", s)
}

func parseHorizons(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid horizon %q", p)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no horizons given")
	}
	sort.Ints(out)
	return out, nil
}

func filterDesc(tabs string, marketBuyOnly bool) string {
	if tabs == "" {
		tabs = "全部 tab"
	}
	desc := "驗證範圍：" + tabs + " tab"
	if marketBuyOnly {
		desc += "（市場掃描 tab 僅取 BUY 買進候選，排除全市場 SELL/HOLD 掃描狀態）"
	}
	return desc
}

func printConsoleSummary(results []validator.ValidationResult) {
	counts := map[validator.Result]int{}
	for _, r := range results {
		counts[r.Result]++
	}
	fmt.Fprintf(os.Stderr, "── summary ──\n")
	for _, k := range []validator.Result{
		validator.ResultCorrect, validator.ResultWrong, validator.ResultNeutral,
		validator.ResultPending, validator.ResultNoPriceData,
	} {
		fmt.Fprintf(os.Stderr, "  %-14s %d\n", k, counts[k])
	}
}
