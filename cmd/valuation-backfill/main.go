// Command valuation-backfill fills the historical P/E archive from the exchanges' own
// per-date endpoints.
//
//	go run ./cmd/valuation-backfill                 # 12 months, candidate.yaml
//	go run ./cmd/valuation-backfill -months 36
//	go run ./cmd/valuation-backfill -dry-run
//
// # Why this exists
//
// The live valuation endpoints take no parameters and serve only the current session, so the
// archive could previously only grow one day at a time — 20 trading days before a P/E
// distribution became computable at all. Both exchanges publish per-date ratios; this uses
// them. The earlier limitation was which endpoint the provider called, not what the source
// offers.
//
// # It does not fake history
//
// Every record is stamped Backfilled with the real FetchedAt, and archived under the day the
// BACKFILL RAN — never under the session it describes. LoadHistory's point-in-time bound is
// therefore unchanged: a health check dated before this run still cannot see these rows.
// Making historical rows visible to historical queries is a different feature with different
// semantics, and is deliberately not attempted here.
//
// SHADOW-ONLY. It writes data/valuation/ and nothing else; BUY / WATCH / SELL, the rocket
// score and every ranking are untouched.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func main() {
	log.SetFlags(log.Ltime)

	candidatesPath := flag.String("candidates", "candidate.yaml", "universe to backfill")
	dir := flag.String("out", valuation.DefaultDir(), "snapshot archive directory")
	months := flag.Int("months", 12, "how many calendar months back to fetch")
	throttleMs := flag.Int("throttle-ms", 1500, "minimum delay between requests")
	retries := flag.Int("retries", 4, "retries per request, with exponential backoff")
	rateBackoff := flag.Int("rate-limit-backoff-sec", 30,
		"base wait after a rate-limit refusal (doubled per retry)")
	refetch := flag.Bool("refetch", false,
		"re-fetch months already present in today's archive instead of resuming")
	dryRun := flag.Bool("dry-run", false, "fetch and report, write nothing")
	flag.Parse()

	if *months <= 0 {
		log.Fatalf("-months must be positive, got %d", *months)
	}

	list, err := candidate.Load(*candidatesPath)
	if err != nil {
		log.Fatalf("candidate config: %v", err)
	}
	if len(list.Candidates) == 0 {
		log.Fatalf("no candidates configured in %s", *candidatesPath)
	}

	// One clock for the whole run, so every derived date is consistent even across a
	// midnight boundary, and a rerun on the same day lands in the same archive directory.
	now := time.Now()
	archiveDate := now.Format("2006-01-02")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("interrupted — finishing the current request, already-written archives stand")
		cancel()
	}()

	p := valuation.NewHistoricalProvider(func(f string, a ...any) { log.Printf(f, a...) })
	p.Throttle = time.Duration(*throttleMs) * time.Millisecond
	p.Retries = *retries
	p.RateLimitBackoff = time.Duration(*rateBackoff) * time.Second

	targets, err := parseTargets(list)
	if err != nil {
		log.Fatalf("%v", err)
	}
	twse, tpex := splitByMarket(targets)

	log.Printf("backfill %d months  universe=%s (%d 檔: %d 上市 / %d 上櫃)  archive=%s/%s",
		*months, *candidatesPath, len(targets), len(twse), len(tpex), *dir, archiveDate)
	if *dryRun {
		log.Printf("dry-run: nothing will be written")
	}

	results := map[string]*result{}
	for _, t := range targets {
		results[t.key()] = &result{target: t, months: *months}
	}

	monthList := valuation.MonthsBack(now, *months)

	// Resume: whatever a previous run already archived TODAY is loaded first, so an
	// interrupted or rate-limited run can be repeated without re-fetching what it already
	// has. TWSE's CDN refuses bursts, so the realistic way to finish a large backfill is
	// several passes — and a pass that started from zero every time would never get further
	// than the first one did.
	if !*refetch {
		resumed := 0
		for _, t := range targets {
			if ex := readExisting(*dir, archiveDate, t); ex != nil && len(ex.History) > 0 {
				results[t.key()].add(ex.History)
				results[t.key()].resumed = len(ex.History)
				resumed++
			}
		}
		if resumed > 0 {
			log.Printf("resume: %d 檔已有今日封存，只補缺少的月份（-refetch 可強制重抓）", resumed)
		}
	}

	fetchTWSE(ctx, p, twse, monthList, results)
	fetchTPEX(ctx, p, tpex, monthList, now, results)

	if !*dryRun {
		writeArchives(*dir, archiveDate, now, targets, results)
	}
	report(results, targets, *months)
}

// ── targets ───────────────────────────────────────────────────────────────────────────

type target struct{ code, market string }

func (t target) key() string    { return t.code + "." + t.market }
func (t target) symbol() string { return t.code + "." + t.market }

func parseTargets(list *candidate.List) ([]target, error) {
	out := make([]target, 0, len(list.Candidates))
	for _, c := range list.Candidates {
		code, market, err := fetcher.ParseYahooSymbol(c.CanonicalSymbol())
		if err != nil {
			return nil, fmt.Errorf("candidate %q: %w", c.CanonicalSymbol(), err)
		}
		out = append(out, target{code: code, market: market})
	}
	return out, nil
}

// splitByMarket separates the two fetch strategies. TWSE is queried per stock; TPEx returns
// the whole market per session, so its stocks are fanned out from one shared response.
func splitByMarket(ts []target) (twse, tpex []target) {
	for _, t := range ts {
		if t.market == "TWO" {
			tpex = append(tpex, t)
		} else {
			twse = append(twse, t)
		}
	}
	return twse, tpex
}

// ── results ───────────────────────────────────────────────────────────────────────────

type result struct {
	target  target
	months  int
	rows    []valuation.Ratios
	errs    []string
	skipped int
	// resumed is how many sessions were already in today's archive before this run.
	resumed int
	// limited records that the exchange refused the request RATE, which is a "come back
	// later" rather than "this data does not exist" — a distinction the report has to keep,
	// because the two call for completely different responses from the operator.
	limited bool
}

// has reports whether the month is already covered, so a resumed run skips it.
//
// "Covered" is at least one session in that calendar month. A month with a genuine handful of
// sessions and a month cut short by a rate limit are indistinguishable from the archive
// alone, so this errs toward not re-fetching; -refetch forces the issue.
func (r *result) has(month time.Time) bool {
	prefix := month.Format("2006-01")
	for _, x := range r.rows {
		if strings.HasPrefix(x.Date, prefix) {
			return true
		}
	}
	return false
}

func (r *result) add(rows []valuation.Ratios) { r.rows = append(r.rows, rows...) }

// ── fetching ──────────────────────────────────────────────────────────────────────────

func fetchTWSE(ctx context.Context, p *valuation.HistoricalProvider, ts []target,
	months []time.Time, results map[string]*result) {
	if len(ts) == 0 {
		return
	}
	log.Printf("TWSE: %d 檔 × %d 個月 = %d 次請求", len(ts), len(months), len(ts)*len(months))
	for _, t := range ts {
		res := results[t.key()]
		for _, m := range months {
			if ctx.Err() != nil {
				return
			}
			if res.has(m) {
				continue // already archived by an earlier pass
			}
			rows, err := p.TWSEMonth(ctx, t.code, m)
			if err != nil {
				// A failed month is recorded and the rest continue: partial data is worth
				// far more than an all-or-nothing run that loses eleven good months to one
				// bad request.
				res.errs = append(res.errs, fmt.Sprintf("%s: %v", m.Format("2006-01"), err))
				if valuation.IsRateLimited(err) {
					res.limited = true
				}
				continue
			}
			res.add(rows)
		}
		log.Printf("  %s  %d sessions%s", t.symbol(), len(res.rows), errSuffix(res))
	}
}

func fetchTPEX(ctx context.Context, p *valuation.HistoricalProvider, ts []target,
	months []time.Time, now time.Time, results map[string]*result) {
	if len(ts) == 0 {
		return
	}
	days := sessionDays(months, now)
	log.Printf("TPEx: %d 個日期 × 1 次請求（每次回傳全市場，再取本 universe 的 %d 檔）",
		len(days), len(ts))

	var fetched, empty int
	for i, d := range days {
		if ctx.Err() != nil {
			break
		}
		byCode, err := p.TPEXSession(ctx, d)
		if err != nil {
			for _, t := range ts {
				results[t.key()].errs = append(results[t.key()].errs,
					fmt.Sprintf("%s: %v", d.Format("2006-01-02"), err))
			}
			continue
		}
		if len(byCode) == 0 {
			empty++ // weekend or holiday
			continue
		}
		fetched++
		for _, t := range ts {
			if r, ok := byCode[t.code]; ok {
				results[t.key()].add([]valuation.Ratios{r})
			} else {
				results[t.key()].skipped++
			}
		}
		if (i+1)%20 == 0 {
			log.Printf("  ... %d/%d 個日期（%d 個交易日）", i+1, len(days), fetched)
		}
	}
	log.Printf("  TPEx 完成：%d 個交易日，%d 個非交易日", fetched, empty)
}

// sessionDays lists every weekday in the covered months, up to and including now.
//
// Weekends are dropped before any request is made — they are guaranteed non-trading days and
// asking for them would be a third of the requests wasted. Holidays cannot be known in
// advance and come back empty, which is not an error.
func sessionDays(months []time.Time, now time.Time) []time.Time {
	var out []time.Time
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, m := range months {
		end := m.AddDate(0, 1, 0)
		for d := m; d.Before(end); d = d.AddDate(0, 0, 1) {
			if d.After(today) {
				break
			}
			switch d.Weekday() {
			case time.Saturday, time.Sunday:
				continue
			}
			out = append(out, d)
		}
	}
	return out
}

func errSuffix(r *result) string {
	if len(r.errs) == 0 {
		return ""
	}
	return fmt.Sprintf("  (%d 個月失敗)", len(r.errs))
}

// ── archiving ─────────────────────────────────────────────────────────────────────────

// writeArchives merges each stock's sessions into the archive for TODAY.
//
// Merging rather than replacing is what makes a rerun idempotent: an existing file's live
// Ratios and any previously backfilled sessions are preserved, and the same input produces
// the same output. A stock whose fetch produced nothing is left completely alone — an empty
// result must never overwrite good data.
func writeArchives(dir, archiveDate string, now time.Time, ts []target, results map[string]*result) {
	var written, skipped int
	for _, t := range ts {
		res := results[t.key()]
		if len(res.rows) == 0 {
			skipped++
			continue
		}
		existing := readExisting(dir, archiveDate, t)

		snap := &valuation.Snapshot{
			SchemaVersion: valuation.SchemaVersion,
			Symbol:        t.symbol(),
			Source:        valuation.SourceName,
			ObservedAt:    now,
			Status:        valuation.Available,
		}
		if existing != nil {
			// Keep whatever the live fetch put here. The backfill adds history; it does not
			// get to decide what today's reading was.
			snap.Source = existing.Source
			snap.ObservedAt = existing.ObservedAt
			snap.Status = existing.Status
			snap.Reason = existing.Reason
			snap.Ratios = existing.Ratios
			snap.History = existing.History
		}
		stamped := make([]valuation.Ratios, 0, len(res.rows))
		for _, r := range res.rows {
			r.FetchedAt = now
			r.Backfilled = true
			stamped = append(stamped, r)
		}
		snap.History = valuation.MergeHistory(snap.History, stamped)

		if _, err := valuation.SaveSnapshot(dir, snap); err != nil {
			log.Printf("  ! %s: save: %v", t.symbol(), err)
			continue
		}
		written++
	}
	log.Printf("archive: %s/%s — %d 檔寫入, %d 檔無資料未動", dir, archiveDate, written, skipped)
}

// readExisting loads today's archive for one stock, if the live fetch already wrote one.
//
// Read directly rather than through the package: internal/valuation exposes LoadHistory for
// the point-in-time path and no single-file loader, and adding one to a shared package for a
// backfill's convenience is more change than this needs. A missing or unreadable file simply
// means "nothing to merge with" — never a reason to lose the fetched history.
func readExisting(dir, archiveDate string, t target) *valuation.Snapshot {
	b, err := os.ReadFile(valuation.SnapshotPath(dir, archiveDate, t.code, t.market))
	if err != nil {
		return nil
	}
	var s valuation.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		log.Printf("  ! %s: existing archive unreadable, not merging: %v", t.symbol(), err)
		return nil
	}
	return &s
}

// ── report ────────────────────────────────────────────────────────────────────────────

func report(results map[string]*result, ts []target, months int) {
	fmt.Printf("\n%-12s %-6s %-8s %-8s %-6s %-6s %-12s %-12s %8s %8s %8s %10s  %s\n",
		"symbol", "market", "period", "sessions", "valid", "n/a", "oldest", "newest",
		"P25", "median", "P75", "percentile", "target")
	fmt.Println(strings.Repeat("─", 132))

	for _, t := range ts {
		res := results[t.key()]
		rows := res.rows
		sort.Slice(rows, func(i, j int) bool { return rows[i].Date < rows[j].Date })

		var valid, na int
		for _, r := range rows {
			if r.PERatio != nil {
				valid++
			} else {
				na++
			}
		}
		oldest, newest := "—", "—"
		if len(rows) > 0 {
			oldest, newest = rows[0].Date, rows[len(rows)-1].Date
		}

		// Run the REAL calculator over what was fetched, so the report shows what the
		// dashboard will show rather than a second statistic computed here.
		v := valuation.Calculate(valuation.Input{
			Symbol: t.symbol(), History: rows, Current: currentOf(rows),
			AsOfDate: newest,
		})
		h := v.HistoricalPE
		mkt := "TWSE"
		if t.market == "TWO" {
			mkt = "TPEx"
		}
		tp := "INSUFFICIENT_DATA"
		if h.Status == valuation.Available {
			tp = "distribution ready"
		}
		if res.limited && h.Status != valuation.Available {
			// The difference an operator needs: this one is incomplete because the exchange
			// throttled us, not because the data is missing. Re-running finishes it.
			tp = "RATE LIMITED — 重跑即可續抓"
		}
		fmt.Printf("%-12s %-6s %-8s %8d %6d %6d %-12s %-12s %8s %8s %8s %10s  %s\n",
			t.symbol(), mkt, fmt.Sprintf("%dM", months), len(rows), valid, na,
			oldest, newest, f(h.P25), f(h.Median), f(h.P75), pct(h.CurrentPercentile), tp)
		for _, e := range res.errs[:min(len(res.errs), 2)] {
			fmt.Printf("%-12s   ! %s\n", "", e)
		}
	}
	fmt.Printf("\nminimum samples required: %d (unchanged)\n", valuation.MinHistoricalPESamples)
}

// currentOf returns the newest row carrying a P/E, which is what the distribution's
// percentile is measured against.
func currentOf(rows []valuation.Ratios) *valuation.Ratios {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].PERatio != nil {
			r := rows[i]
			return &r
		}
	}
	return nil
}

func f(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *p)
}

func pct(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *p*100)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
