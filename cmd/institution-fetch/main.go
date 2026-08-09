// Command institution-fetch is the standalone daily prefetch for per-stock 三大法人買賣超
// (R10-1). It fetches TWSE (上市) + TPEx (上櫃) for a date and writes normalized snapshots
// to data/institution/{twse,tpex}_<date>.json. The scanner runtime only LOADS these
// snapshots — it never fetches live — so a source outage never blocks a scan (SPEC §21).
//
// Per-source failures are isolated: one market down still lets the other write. A no-data
// day (holiday) is logged and skipped; a same-day rerun that would reduce completeness is
// refused (§19).
//
// Example:
//
//	go run ./cmd/institution-fetch --date 2026-07-15 --dir data/institution
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"time"

	"github.com/deep-huang/stock-scanner/internal/institution"
)

func main() {
	date := flag.String("date", time.Now().Format("2006-01-02"), "trading date (YYYY-MM-DD)")
	dir := flag.String("dir", "data/institution", "snapshot output directory")
	timeoutSec := flag.Int("timeout", 30, "per-request timeout (seconds)")
	flag.Parse()

	if _, err := time.Parse("2006-01-02", *date); err != nil {
		log.Fatalf("bad --date %q: %v", *date, err)
	}
	timeout := time.Duration(*timeoutSec) * time.Second
	ctx := context.Background()

	providers := []institution.Provider{
		institution.NewTWSEProvider(timeout),
		institution.NewTPEXProvider(timeout),
	}

	var saved int
	for _, p := range providers {
		snap, err := p.Fetch(ctx, *date)
		if err != nil {
			if errors.Is(err, institution.ErrNoData) {
				log.Printf("%s %s: no trading data (holiday/before floor) — skip", p.Market(), *date)
			} else {
				log.Printf("%s %s: fetch error (isolated): %v", p.Market(), *date, err)
			}
			continue
		}
		if err := institution.Save(*dir, snap); err != nil {
			if errors.Is(err, institution.ErrWouldDowngrade) {
				log.Printf("%s %s: %v — keeping existing snapshot", p.Market(), *date, err)
			} else {
				log.Printf("%s %s: save error: %v", p.Market(), *date, err)
			}
			continue
		}
		log.Printf("%s %s: saved %d stocks", p.Market(), *date, len(snap.Stocks))
		saved++
	}
	log.Printf("institution-fetch done: %d/%d markets saved for %s", saved, len(providers), *date)
}
