// Command daytrading-fetch is the standalone prefetch/backfill for per-stock 當日沖銷
// (day-trading) volume. It fetches TWSE (上市) + TPEx (上櫃) for one date or an inclusive
// date range and writes normalized snapshots to data/daytrading/{twse,tpex}_<date>.json,
// so a research study can compute per-stock DayTradingRatio entirely offline.
//
// Both sources publish after the close, so a date's figures are available the same evening.
//
// Failure isolation: one market being down never blocks the other, and one bad DATE never
// aborts a backfill — every failure is logged and the walk continues, because a range of
// several hundred trading days that dies on day 3 is worse than useless. Non-trading days
// are normal, not errors: the source answers with a well-formed empty payload, which is
// logged and skipped without writing a file.
//
// Examples:
//
//	go run ./cmd/daytrading-fetch --date 2026-08-25
//	go run ./cmd/daytrading-fetch --from 2024-01-01 --to 2026-08-25
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"time"

	"github.com/deep-huang/stock-scanner/internal/daytrading"
)

const dateLayout = "2006-01-02"

func main() {
	log.SetFlags(log.Ltime)
	date := flag.String("date", "", "single trading date (YYYY-MM-DD); default = today when no --from/--to")
	from := flag.String("from", "", "backfill start date (YYYY-MM-DD, inclusive)")
	to := flag.String("to", "", "backfill end date (YYYY-MM-DD, inclusive)")
	dir := flag.String("dir", "data/daytrading", "snapshot output directory")
	delay := flag.Duration("delay", 1500*time.Millisecond, "pause between requests (politeness; both sources throttle)")
	skipExisting := flag.Bool("skip-existing", true, "never re-fetch a (market, date) already on disk")
	timeoutSec := flag.Int("timeout", 30, "per-request timeout (seconds)")
	flag.Parse()

	dates, err := resolveDates(*date, *from, *to)
	if err != nil {
		log.Fatalf("%v", err)
	}

	providers := []daytrading.Provider{
		daytrading.NewTWSEProvider(time.Duration(*timeoutSec) * time.Second),
		daytrading.NewTPEXProvider(time.Duration(*timeoutSec) * time.Second),
	}

	log.Printf("daytrading-fetch: %d date(s) %s → %s, %d markets, dir=%s",
		len(dates), dates[0], dates[len(dates)-1], len(providers), *dir)

	var saved, skipped, noData, failed int
	first := true
	for i, d := range dates {
		for _, p := range providers {
			if *skipExisting && daytrading.Exists(*dir, p.Market(), d) {
				skipped++
				continue
			}
			if !first {
				time.Sleep(*delay)
			}
			first = false

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			snap, err := p.Fetch(ctx, d)
			cancel()
			if err != nil {
				// Isolated: log and move on. A single unreachable date must not cost the
				// rest of a multi-hundred-day walk.
				failed++
				log.Printf("%s %s: fetch error (isolated): %v", p.Market(), d, err)
				continue
			}
			if !snap.HasData() {
				noData++
				log.Printf("%s %s: no trading data (holiday / not yet published) — skip", p.Market(), d)
				continue
			}
			if err := daytrading.Save(*dir, snap); err != nil {
				if errors.Is(err, daytrading.ErrWouldDowngrade) {
					log.Printf("%s %s: %v — keeping existing snapshot", p.Market(), d, err)
				} else {
					failed++
					log.Printf("%s %s: save error: %v", p.Market(), d, err)
				}
				continue
			}
			saved++
			log.Printf("%s %s: saved %d stocks (date %d/%d)", p.Market(), d, len(snap.Stocks),
				i+1, len(dates))
		}
	}
	log.Printf("daytrading-fetch done: %d saved, %d already on disk, %d no-data, %d failed",
		saved, skipped, noData, failed)
}

// resolveDates turns the flags into the ascending list of dates to walk. --date and
// --from/--to are mutually exclusive; with neither, today is used (the after-close daily
// run).
func resolveDates(date, from, to string) ([]string, error) {
	switch {
	case date != "" && (from != "" || to != ""):
		return nil, errors.New("use either --date or --from/--to, not both")

	case date != "":
		if _, err := time.Parse(dateLayout, date); err != nil {
			return nil, err
		}
		return []string{date}, nil

	case from != "" || to != "":
		if from == "" || to == "" {
			return nil, errors.New("--from and --to must be given together")
		}
		start, err := time.Parse(dateLayout, from)
		if err != nil {
			return nil, err
		}
		end, err := time.Parse(dateLayout, to)
		if err != nil {
			return nil, err
		}
		if end.Before(start) {
			return nil, errors.New("--to is before --from")
		}
		var out []string
		for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
			// Weekends are skipped up front: neither exchange ever publishes on one, and
			// omitting them removes ~30% of the requests from a long backfill.
			if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
				continue
			}
			out = append(out, d.Format(dateLayout))
		}
		if len(out) == 0 {
			return nil, errors.New("range contains no weekdays")
		}
		return out, nil

	default:
		return []string{time.Now().Format(dateLayout)}, nil
	}
}
