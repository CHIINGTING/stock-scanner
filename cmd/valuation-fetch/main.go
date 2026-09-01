// Command valuation-fetch archives the exchanges' published price ratios for the candidate
// universe.
//
// Separate from candidate-research for the same reason as fundamental-fetch: the endpoints
// serve only the latest session, so a P/E history exists only because this command runs
// regularly and keeps what it saw. Research reads that archive and never fetches — a live
// call during a historical run would return today's ratio under a past date.
//
// It computes no fair value and calls no AI. There is no authorized source of Taiwanese
// earnings forecasts, so forward-looking measures are reported as NOT_IMPLEMENTED rather
// than estimated.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

const exitConfigError = 2

func main() {
	log.SetFlags(log.Ltime)
	candidatesPath := flag.String("candidates", "candidate.yaml", "candidate research universe")
	dir := flag.String("out", valuation.DefaultDir(), "snapshot archive directory")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall fetch timeout")
	flag.Parse()

	list, err := candidate.Load(*candidatesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "candidate config: %v\n", err)
		os.Exit(exitConfigError)
	}
	if len(list.Candidates) == 0 {
		fmt.Println("No candidates configured.")
		return
	}
	symbols := make([]string, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		symbols = append(symbols, c.CanonicalSymbol())
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	snaps, err := valuation.NewService(nil, *dir, log.Printf).FetchAndStore(ctx, symbols)
	if err != nil {
		log.Printf("valuation: %v", err)
	}

	var ok, missing int
	fmt.Printf("\nValuation fetch  universe=%s (%d 檔)\n\n", *candidatesPath, len(symbols))
	for _, s := range snaps {
		line := fmt.Sprintf("  %-10s %-12s", s.Symbol, s.Status)
		if s.Ratios != nil {
			ok++
			pe := "no P/E published"
			if s.Ratios.PERatio != nil {
				pe = fmt.Sprintf("P/E %.2f", *s.Ratios.PERatio)
			}
			line += fmt.Sprintf("  %s  %s", s.Ratios.Date, pe)
		} else {
			missing++
			line += "  " + s.Reason
		}
		fmt.Println(line)
	}
	fmt.Printf("\nCandidates: %d   Archived: %d   Missing: %d\n", len(snaps), ok, missing)
	fmt.Printf("archive: %s/<observed-date>/\n", *dir)
}
