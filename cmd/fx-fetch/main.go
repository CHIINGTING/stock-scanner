// Command fx-fetch downloads the daily USD/TWD series into the local archive.
//
// Separate binary, matching cmd/market-fetch and cmd/institution-fetch: acquisition runs on
// its own schedule and its own failure budget, and the scanner reads whatever the archive
// holds. A currency feed being down must never be able to stop a scan.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fx"
)

func main() {
	log.SetFlags(log.Ltime)
	dir := flag.String("out", fx.DefaultStoreDir(), "archive directory")
	which := flag.String("series", "all", "usdtwd | dxy | all")
	years := flag.Int("years", 5, "how much history to request")
	timeout := flag.Duration("timeout", 30*time.Second, "HTTP timeout")
	flag.Parse()

	var specs []fx.SourceSpec
	switch *which {
	case "usdtwd":
		specs = []fx.SourceSpec{fx.SpecUSDTWD}
	case "dxy":
		specs = []fx.SourceSpec{fx.SpecDXY}
	default:
		specs = []fx.SourceSpec{fx.SpecUSDTWD, fx.SpecDXY}
	}

	today := time.Now().Format("2006-01-02")
	for _, spec := range specs {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		s, err := fx.FetchSeries(ctx, nil, spec, *years)
		cancel()
		if err != nil {
			log.Fatalf("%v", err)
		}
		path, err := fx.SaveSeries(*dir, spec, s)
		if err != nil {
			log.Fatalf("%v", err)
		}

		first, last := s.Bars[0], s.Bars[len(s.Bars)-1]
		fmt.Printf("%-7s %d bars, %s → %s (last %.4f)  → %s\n",
			spec.Symbol, len(s.Bars), first.Date, last.Date, last.Close, path)

		// The reading a Taiwan session TODAY would legitimately see — printed at acquisition
		// time so the causality gap (bar date strictly before the session) is visible.
		m := s.MetricsAsOf(today, fx.DefaultConfig())
		if m.Status.OK() {
			d1, _ := m.Change(1)
			d5, _ := m.Change(5)
			d20, _ := m.Change(20)
			fmt.Printf("        as of Taiwan %s uses bar %s: 1D %+.2f%% | 5D %+.2f%% | 20D %+.2f%% | %s\n",
				today, m.FXDate, d1, d5, d20, m.Context)
		} else {
			fmt.Printf("        as of Taiwan %s: %s\n", today, m.Status)
		}
	}
	_ = os.Stdout
}
