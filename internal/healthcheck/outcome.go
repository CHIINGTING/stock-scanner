package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// Grading a health check after the fact.
//
// This is the first consumer of R13's outcomes table: the storage has existed since R13 and
// nothing ever filled it. Everything here measures forward returns from a snapshot's own
// close, using the existing repository — there is no second definition of "return" and no
// second table.
//
// # Three things it has to get right, and each was a real trap
//
//	only its own rows   OutcomeRepo.PendingHorizon does NOT filter on source_tab. Calling it
//	                    naively returns the scanner's snapshots too, and filling those in
//	                    would be this sidecar writing outcomes for judgements it did not make.
//	                    Rows are filtered to source_tab = TabHealthCheck before anything is
//	                    written.
//
//	one call, one vote  A person pressing the button three times on one stock leaves three
//	                    snapshots with the same symbol and trading date, because each check
//	                    opens its own scan_run. Grading all three counts one opinion three
//	                    times in every aggregate. Only the LAST check of a symbol-day is
//	                    graded; the earlier ones keep their rows and stay ungraded, which is
//	                    the honest record of "you looked, then looked again".
//
//	no future returns   A horizon is filled only when the trading days to support it actually
//	                    exist in the series. "The 20-day return is nil because the future has
//	                    not happened" and "…because the data is missing" are different facts,
//	                    and BarsObserved is what keeps them apart.
//
// # It never overwrites
//
// store.OutcomeRepo enforces write-once per horizon: a rerun over a date whose returns are
// already known is a normal event and leaves the first value in place. That is what stops a
// later, better-informed run quietly rewriting history and flattering the numbers.

// progressEvery is how often a long pass reports where it has got to.
const progressEvery = 25

// OutcomeConfig bounds one grading pass.
type OutcomeConfig struct {
	// AsOf is the date up to which returns may be computed. Empty → today in Taipei.
	AsOf string
	// Limit caps how many snapshots PendingHorizon returns per horizon. Zero → no cap.
	//
	// It is safe to truncate: which snapshot of a symbol-day is gradeable is decided from
	// the stored table, not from this page, so a page that happens to omit the canonical row
	// simply leaves it for the next pass rather than promoting a duplicate.
	Limit int
}

// OutcomeReport is what one pass did, for a caller that has to explain itself.
type OutcomeReport struct {
	AsOf string
	// Examined is how many health-check snapshots were considered after filtering.
	Examined int
	// Skipped counts snapshots deliberately not graded, by reason.
	SkippedDuplicate int
	SkippedNoBase    int
	SkippedNoPrice   int
	SkippedTooEarly  int
	// Filled counts horizons written, by horizon.
	Filled map[int]int
	// AlreadyKnown counts horizons that were already recorded and left alone.
	AlreadyKnown int
	Errors       []string
}

// Outcomes grades stored health checks.
type Outcomes struct {
	History *History
	Prices  PriceSource
	// Catalog resolves a symbol's exchange so the fetcher does not have to probe for it.
	// Optional: nil falls back to the fetcher's own .TW → .TWO detection.
	Catalog *stockcatalog.Catalog
	Logf    func(string, ...any)
	Now     func() time.Time
}

func (o *Outcomes) now() time.Time {
	if o != nil && o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Outcomes) logf(format string, args ...any) {
	if o == nil || o.Logf == nil {
		return
	}
	o.Logf(format, args...)
}

// Run grades every health-check snapshot that can be graded, and returns what it did.
func (o *Outcomes) Run(ctx context.Context, cfg OutcomeConfig) (OutcomeReport, error) {
	rep := OutcomeReport{Filled: map[int]int{}}
	if o == nil || o.History == nil || o.History.store == nil {
		return rep, fmt.Errorf("healthcheck: no history store to grade")
	}
	if o.Prices == nil {
		return rep, fmt.Errorf("healthcheck: no price source")
	}
	asOf := cfg.AsOf
	if asOf == "" {
		asOf = o.defaultAsOf()
	}
	if _, err := ParseDate(asOf); err != nil {
		return rep, fmt.Errorf("%w: %v", ErrBadDate, err)
	}
	rep.AsOf = asOf

	// Gather the candidates across every horizon, then dedupe ONCE — a snapshot pending
	// several horizons must be graded as one stock, not once per horizon.
	pending := map[int64]store.StockSnapshot{}
	for _, h := range store.Horizons {
		rows, err := o.History.store.Outcomes().PendingHorizon(ctx, h, asOf, cfg.Limit)
		if err != nil {
			return rep, fmt.Errorf("healthcheck: pending %dd: %w", h.Days(), err)
		}
		for _, s := range rows {
			// The filter PendingHorizon does not apply. Without it this would write
			// outcomes for the scanner's own snapshots.
			if s.SourceTab != TabHealthCheck {
				continue
			}
			pending[s.ID] = s
		}
	}

	canon, err := o.canonicalIDs(ctx, pending)
	if err != nil {
		return rep, err
	}
	graded, duplicates := gradeable(pending, canon)
	rep.Examined = len(graded)
	rep.SkippedDuplicate = duplicates

	// Deterministic order so a run is reproducible and a log is diffable.
	sort.Slice(graded, func(i, j int) bool {
		if graded[i].TradingDate != graded[j].TradingDate {
			return graded[i].TradingDate < graded[j].TradingDate
		}
		if graded[i].Symbol != graded[j].Symbol {
			return graded[i].Symbol < graded[j].Symbol
		}
		return graded[i].ID < graded[j].ID
	})

	o.logf("outcomes: grading %d health checks as of %s (%d duplicate rows set aside)",
		len(graded), asOf, duplicates)
	for i, snap := range graded {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		o.grade(ctx, snap, asOf, &rep)
		// A batch that fetches a price per stock can run for a while; silence for minutes
		// is indistinguishable from a hang.
		if progressEvery > 0 && (i+1)%progressEvery == 0 {
			o.logf("outcomes: %d/%d", i+1, len(graded))
		}
	}
	return rep, nil
}

// grade fills whatever horizons the price series can support for one snapshot.
func (o *Outcomes) grade(ctx context.Context, snap store.StockSnapshot, asOf string, rep *OutcomeReport) {
	if snap.Close == nil || *snap.Close <= 0 {
		// No basis price means no outcome can ever be measured for this row. Recorded as a
		// skip rather than as a zero return.
		rep.SkippedNoBase++
		return
	}
	base := *snap.Close

	bars, err := o.forwardBars(snap, asOf)
	if err != nil {
		rep.SkippedNoPrice++
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s %s: %v", snap.Symbol, snap.TradingDate, err))
		return
	}
	if len(bars) == 0 {
		rep.SkippedTooEarly++
		return
	}

	repo := o.History.store.Outcomes()
	row, err := repo.Ensure(ctx, snap.ID, base)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s: ensure: %v", snap.Symbol, err))
		return
	}
	// Measure against the base ON THE ROW, not the one just passed in. They are equal today
	// because a snapshot's close is immutable, but Ensure refuses to move an existing base —
	// so if they ever differed, the row's value is the one every stored return was computed
	// from, and using anything else would put two definitions in one table.
	base = row.BaseClose
	if err := repo.SetBarsObserved(ctx, snap.ID, len(bars)); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s: bars: %v", snap.Symbol, err))
	}

	for _, h := range store.Horizons {
		n := h.Days()
		if len(bars) < n {
			continue // the future has not happened yet — not an error, and not a zero
		}
		if bars[n-1].Close <= 0 {
			continue // that session has no usable close; leaving the horizon NULL is honest
		}
		pct := (bars[n-1].Close - base) / base * 100
		wrote, err := repo.SetReturn(ctx, snap.ID, h, pct)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s %dd: %v", snap.Symbol, n, err))
			continue
		}
		if wrote {
			rep.Filled[n]++
		} else {
			rep.AlreadyKnown++
		}
	}

	// Extremes over the 20-day window, from the bars that exist so far.
	if len(bars) >= store.H20D.Days() {
		window := bars[:store.H20D.Days()]
		maxGain, maxDD := extremes(base, window)
		if _, err := repo.SetExtremes(ctx, snap.ID, maxGain, maxDD); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: extremes: %v", snap.Symbol, err))
		}
	}
}

// marketCloseHour is when the Taiwan session ends (13:30). The cut-off below adds a margin,
// because the last print and the feed catching up are not the same instant.
const (
	marketCloseHour   = 13
	marketCloseMinute = 30
	feedSettleMinutes = 30
)

// defaultAsOf is the last session that has actually CLOSED.
//
// Not simply "today". internal/fetcher backfills the current session from the quote meta, so
// a bar dated today exists from the opening bell and carries a mid-session price. Grading at
// 10:00 would take that price as a completed session and write it into a WRITE-ONCE column:
// permanently wrong, with no field on the row to say it was an intraday value and no path to
// correct it. A day's wait costs nothing; a wrong number that cannot be revised costs the
// research it feeds.
func (o *Outcomes) defaultAsOf() string {
	now := o.now().In(Taipei)
	if sessionClosed(now) {
		return now.Format(dateLayout)
	}
	return now.AddDate(0, 0, -1).Format(dateLayout)
}

// sessionClosed reports whether the trading day of t has finished.
func sessionClosed(t time.Time) bool {
	cut := time.Date(t.Year(), t.Month(), t.Day(),
		marketCloseHour, marketCloseMinute+feedSettleMinutes, 0, 0, Taipei)
	return !t.Before(cut)
}

// forwardBars returns the sessions AFTER the snapshot's trading date, up to asOf.
//
// Strictly after: the snapshot's own session is the basis, and including it would make every
// 1-day return start from itself.
//
// The current session is excluded until it closes, whatever asOf says. An explicit
// -outcomes-as-of today at 10:00 would otherwise reach the same intraday bar that
// defaultAsOf exists to avoid, and the damage is identical because the column is write-once.
//
// A bar with a non-positive close is KEPT, not dropped: dropping it would silently shift
// every index after it, so the "20-day" return would quietly span 21 trading days. The
// horizon that lands on it is skipped instead — see grade.
func (o *Outcomes) forwardBars(snap store.StockSnapshot, asOf string) ([]fetcher.Candle, error) {
	sd, err := o.Prices.Fetch(fetcher.StockInfo{
		Symbol: snap.Symbol,
		Market: o.marketOf(snap.Symbol),
	})
	if err != nil {
		return nil, err
	}
	today := o.now().In(Taipei)
	todayStr := today.Format(dateLayout)
	closed := sessionClosed(today)

	var out []fetcher.Candle
	for _, c := range sd.Candles {
		d := c.Date.Format(dateLayout)
		if d <= snap.TradingDate || d > asOf {
			continue
		}
		if d == todayStr && !closed {
			continue // the session is still running; its close does not exist yet
		}
		out = append(out, c)
	}
	return out, nil
}

// marketOf resolves the exchange from the catalog when one is available.
//
// Without it the fetcher probes .TW and falls back to .TWO, which costs an extra request for
// every TPEx stock. The catalog already knows, so it is asked when present; an unknown symbol
// falls back to the probe rather than guessing.
func (o *Outcomes) marketOf(symbol string) string {
	if o == nil || o.Catalog == nil {
		return ""
	}
	if st, ok := o.Catalog.Lookup(symbol); ok {
		return st.Market
	}
	return ""
}

// extremes returns the best and worst CLOSE in the window, as percentages of base.
//
// Closes, not highs and lows, and that choice is permanent because the columns are
// write-once: revisiting it later would mean deleting rows. It is deliberate — a close is
// reproducible from any archive and is not sensitive to a single bad tick — but it has a
// systematic direction worth stating, because someone will one day use these numbers for
// stop-loss research:
//
//	max_drawdown_20d is a LOWER BOUND on the true maximum adverse excursion.
//
// An intraday spike through a stop that recovers by the close is invisible here, so a study
// built on this column will UNDERSTATE how often a stop would have been hit.
func extremes(base float64, bars []fetcher.Candle) (maxGain, maxDrawdown float64) {
	for _, c := range bars {
		if c.Close <= 0 {
			continue
		}
		pct := (c.Close - base) / base * 100
		if pct > maxGain {
			maxGain = pct
		}
		if pct < maxDrawdown {
			maxDrawdown = pct
		}
	}
	return maxGain, maxDrawdown
}

// canonicalIDs finds the one gradeable snapshot per (symbol, trading date), across the WHOLE
// table rather than within this pass's pending set.
//
// Deciding it from the pending set alone is wrong in a way that only appears on the SECOND
// run, which is exactly how this job is meant to be used:
//
//	pass 1  both duplicates are pending; the later one wins and fills every horizon
//	pass 2  the winner has no NULL horizon left, so PendingHorizon does not return it — and
//	        the earlier duplicate is now the only candidate, so it becomes "the latest" and
//	        gets graded too
//
// Both rows then carry the same returns, and one opinion counts twice in every aggregate.
// SetReturn is write-once, so nothing can undo it afterwards short of deleting rows. Every
// duplicated symbol-day reaches that state once its horizons fill.
//
// So the winner is looked up from what is STORED, independent of what is pending.
func (o *Outcomes) canonicalIDs(ctx context.Context, pending map[int64]store.StockSnapshot) (map[string]int64, error) {
	dates := map[string]bool{}
	for _, s := range pending {
		dates[s.TradingDate] = true
	}
	canon := map[string]int64{}
	for date := range dates {
		runs, err := o.History.store.Scans().RunsByDate(ctx, date)
		if err != nil {
			return nil, fmt.Errorf("healthcheck: runs on %s: %w", date, err)
		}
		for _, run := range runs {
			snaps, err := o.History.store.Scans().SnapshotsByRun(ctx, run.ID)
			if err != nil {
				return nil, fmt.Errorf("healthcheck: snapshots of run %d: %w", run.ID, err)
			}
			for _, s := range snaps {
				if s.SourceTab != TabHealthCheck {
					continue
				}
				// Only a check that CAN be graded may be canonical.
				//
				// A check whose price fetch failed is still saved — that is the honest
				// record of an attempt — but it carries no basis close, so no return can
				// ever be measured from it. Letting it win on ID alone would silence the
				// day entirely: the one gradeable check of that symbol-day would be set
				// aside as a duplicate of a row that can never be graded, and the outcome
				// would simply never appear.
				//
				// So "the last check of the day" means the last one that recorded a price.
				if s.Close == nil || *s.Close <= 0 {
					continue
				}
				key := s.Symbol + "|" + s.TradingDate
				if cur, ok := canon[key]; !ok || s.ID > cur {
					canon[key] = s.ID
				}
			}
		}
	}
	return canon, nil
}

// gradeable keeps only the canonical snapshot of each symbol-day and reports how many were
// set aside.
//
// The earlier duplicates keep their rows — they are a real record of what was judged and
// when — they are simply never graded, in this pass or any later one.
func gradeable(in map[int64]store.StockSnapshot, canon map[string]int64) ([]store.StockSnapshot, int) {
	out := make([]store.StockSnapshot, 0, len(in))
	skipped := 0
	for _, s := range in {
		if canon[s.Symbol+"|"+s.TradingDate] != s.ID {
			skipped++
			continue
		}
		out = append(out, s)
	}
	return out, skipped
}
