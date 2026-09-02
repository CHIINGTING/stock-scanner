// Command fundamental-fetch downloads Taiwan ACTUAL financial data for the candidate research
// universe and archives it as dated snapshots.
//
// Separate from candidate-research on purpose. The MOPS endpoints serve only the CURRENT
// period, so history exists only because this command runs regularly and keeps what it saw.
// If research fetched instead of reading, a run dated in the past would download today's
// figures and label them with that past date — the look-ahead would be built in.
//
// It reaches no verdict, computes no valuation and calls no AI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
)

const exitConfigError = 2

func main() {
	log.SetFlags(log.Ltime)
	candidatesPath := flag.String("candidates", "candidate.yaml", "candidate research universe")
	dir := flag.String("out", fundamental.DefaultDir(), "snapshot archive directory")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall fetch timeout")
	flag.Parse()

	list, err := candidate.Load(*candidatesPath)
	if err != nil {
		// A broken universe file is a mistake to fix now; a stock that fails to fetch is not.
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

	svc := fundamental.NewService(nil, *dir, log.Printf)
	snaps, err := svc.FetchAndStore(ctx, symbols)
	if err != nil {
		log.Printf("fundamental: %v", err)
	}

	var available, partial, unavailable int
	fmt.Printf("\nFundamental fetch  universe=%s (%d 檔)\n\n", *candidatesPath, len(symbols))
	for _, s := range snaps {
		switch s.Status {
		case fundamental.Available:
			available++
		case fundamental.Partial:
			partial++
		default:
			unavailable++
		}
		line := fmt.Sprintf("  %-10s %-12s revenue=%s financials=%s",
			s.Symbol, s.Status, s.RevenueStatus, s.FinancialStatus)
		if s.Financials != nil {
			line += fmt.Sprintf("  %dQ%d", s.Financials.Period.Year, s.Financials.Period.Quarter)
			if s.Financials.Period.Cumulative {
				line += "(累計)"
			}
		}
		fmt.Println(line)
		if s.Reason != "" {
			fmt.Printf("             %s\n", s.Reason)
		}
	}
	fmt.Printf("\nCandidates: %d   Available: %d   Partial: %d   Unavailable: %d\n",
		len(snaps), available, partial, unavailable)
	fmt.Printf("archive: %s/<observed-date>/\n", *dir)
}
