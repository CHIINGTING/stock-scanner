// Command market-fetch builds one day of the Market Dashboard and writes the snapshot.
//
//	go run ./cmd/market-fetch                    # today
//	go run ./cmd/market-fetch -date 2026-08-07   # a specific trading day
//	go run ./cmd/market-fetch -offline           # local price cache only, no exchange calls
//	go run ./cmd/market-fetch -replay 2024-01-01 # rebuild every day from that date forward
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

func main() {
	log.SetFlags(0)

	date := flag.String("date", time.Now().Format("2006-01-02"), "trading date (YYYY-MM-DD)")
	replay := flag.String("replay", "", "rebuild every cached trading day from this date forward")
	cacheDir := flag.String("cache", ".cache", "price cache directory")
	outDir := flag.String("out", "", "snapshot directory (default data/market)")
	offline := flag.Bool("offline", false, "skip the exchange calls; price/breadth only")
	timeout := flag.Duration("timeout", 25*time.Second, "per-request HTTP timeout")
	quiet := flag.Bool("quiet", false, "only print the verdict line")
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

	snap, path, err := p.RunAndSave(context.Background(), *date)
	if err != nil {
		log.Fatalf("market-fetch: %v", err)
	}
	printVerdict(snap, *quiet)
	log.Printf("wrote %s", path)
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

func printVerdict(s *model.Snapshot, quiet bool) {
	fmt.Printf("%s  regime %s (%s)  score %.1f  confidence %.2f\n",
		s.Date, s.Regime, s.RuleID, s.Score, s.Confidence)
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
