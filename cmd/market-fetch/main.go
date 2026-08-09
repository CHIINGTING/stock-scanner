// Command market-fetch builds one day of the Market Dashboard and writes the snapshot.
//
//	go run ./cmd/market-fetch                     # today
//	go run ./cmd/market-fetch -date 2026-08-07    # a specific trading day
//	go run ./cmd/market-fetch -offline            # local price cache only, no exchange calls
//	go run ./cmd/market-fetch -replay 2024-01-01  # rebuild every cached day from that date
//
// SHADOW-ONLY. It reads the scanner's price cache and writes data/market/; it does not touch
// stock scoring, BUY/WATCH/SELL, stage analysis, candlestick, or any ranking.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
	"github.com/deep-huang/stock-scanner/internal/market/service"
)

// exitNotTradingDay is returned when the requested date is not a trading day (or the local
// price cache has not caught up yet). It is deliberately 0: for a scheduled weekday job,
// "today was a public holiday" is a normal outcome, not a failure to alert on.
const exitNotTradingDay = 0

func main() {
	log.SetFlags(0)

	date := flag.String("date", time.Now().Format("2006-01-02"), "trading date (YYYY-MM-DD)")
	replay := flag.String("replay", "", "rebuild every cached trading day from this date forward")
	cacheDir := flag.String("cache", ".cache", "price cache directory")
	outDir := flag.String("out", "", "snapshot directory (default data/market)")
	offline := flag.Bool("offline", false, "skip the exchange calls; price/breadth only")
	timeout := flag.Duration("timeout", 25*time.Second, "per-request HTTP timeout")
	quiet := flag.Bool("quiet", false, "only print the verdict line")
	requireTradingDay := flag.Bool("require-trading-day", false,
		"refuse to write a snapshot unless the benchmark actually traded on -date")
	flag.Parse()

	cfg := model.MarketConfig{StorageDir: *outDir}.Defaulted()
	feed := provider.CacheFeed{Dir: *cacheDir}

	var net []service.NetProvider
	if !*offline {
		net = append(net, provider.NewTWSEProvider(*timeout), provider.NewTAIFEXProvider(*timeout))
	}
	p := service.NewPipeline(cfg, feed, net...)
	if !*quiet {
		p = p.WithLogger(log.Printf)
	}

	if *replay != "" {
		if err := runReplay(p, feed, *replay, *quiet); err != nil {
			log.Fatalf("market-fetch: %v", err)
		}
		return
	}

	// A scheduled weekday job will fire on holidays and on days the exchange was closed.
	// Writing a snapshot then would invent a trading day that never happened, so the
	// benchmark's own last bar is used as the authority on whether `date` traded at all.
	if *requireTradingDay {
		traded, last, err := benchmarkTraded(feed, cfg.Benchmark, *date)
		if err != nil {
			log.Fatalf("market-fetch: cannot read benchmark %s: %v", cfg.Benchmark, err)
		}
		if !traded {
			log.Printf("requested_date=%s benchmark_last_bar=%s", *date, last)
			log.Printf("market-fetch: %s is not a trading day for %s (or the price cache has "+
				"not caught up). No snapshot written.", *date, cfg.Benchmark)
			os.Exit(exitNotTradingDay)
		}
	}

	snap, path, err := p.RunAndSave(context.Background(), *date)
	if err != nil {
		log.Fatalf("market-fetch: %v", err)
	}
	report(snap, path, *quiet)
}

// benchmarkTraded reports whether the benchmark has a bar dated exactly `date`.
//
// This is the trading-calendar check, and it deliberately uses the price series rather than a
// holiday table: a hard-coded calendar goes stale every year and cannot know about an
// unscheduled closure, whereas "did this instrument print a close that day" is true by
// construction. It also catches the other case that must not produce a snapshot — the local
// cache simply not being refreshed yet.
func benchmarkTraded(feed provider.CacheFeed, benchmark model.Benchmark, date string) (bool, string, error) {
	bars, err := feed.Benchmark(string(benchmark), date)
	if err != nil {
		return false, "", err
	}
	last := bars[len(bars)-1].Date
	return last == date, last, nil
}

// runReplay rebuilds history from the local cache. Exchange data is not available for past
// dates (the TAIFEX daily feed has no date parameter and TWSE would need one request per
// day), so a replay is price/breadth only — which is exactly the dimension that HAS history.
func runReplay(p *service.Pipeline, feed provider.CacheFeed, from string, quiet bool) error {
	uni, err := feed.Universe("")
	if err != nil {
		return err
	}
	p.Net = nil // replay is offline by construction
	var built, failed int
	for _, d := range uni.Dates {
		if d < from {
			continue
		}
		snap, _, err := p.RunAndSave(context.Background(), d)
		if err != nil {
			failed++
			continue
		}
		built++
		if !quiet {
			fmt.Printf("%s  %-14s score %5.1f  conf %.2f  (%s)\n",
				snap.Date, snap.Regime, snap.Score, snap.Confidence, snap.RuleID)
		}
	}
	log.Printf("replay: %d snapshots written, %d dates skipped", built, failed)
	if built == 0 {
		os.Exit(1)
	}
	return nil
}

// report prints the run in a shape a CI log can be read at a glance: one machine-greppable
// key=value block, then the human detail. Everything a failed run needs to be diagnosed from
// the log alone is in the first block.
func report(s *model.Snapshot, path string, quiet bool) {
	fmt.Printf("requested_date=%s regime=%s rule=%s score=%.1f confidence=%.2f snapshot=%s\n",
		s.Date, s.Regime, s.RuleID, s.Score, s.Confidence, path)

	for _, src := range s.Raw.Sources {
		asOf := src.AsOfDate
		if asOf == "" {
			asOf = "-"
		}
		line := fmt.Sprintf("source=%s status=%s source_date=%s", src.Name, src.Status, asOf)
		if src.AsOfDate != "" && src.AsOfDate != s.Date {
			line += " ⚠ SOURCE_DATE_MISMATCH"
		}
		if src.Err != "" {
			line += " err=" + src.Err
		}
		fmt.Println(line)
	}

	var missing []string
	for _, e := range s.Evidence {
		if !e.Usable() {
			missing = append(missing, e.Source)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("missing_evidence=%v\n", missing)
	} else {
		fmt.Println("missing_evidence=none")
	}

	if quiet {
		return
	}
	for _, r := range s.Reasons {
		fmt.Println("  ", r)
	}
	for _, c := range s.Caveats {
		fmt.Println("  ⚠", c)
	}
}
