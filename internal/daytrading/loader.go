package daytrading

import (
	"fmt"
	"os"
	"regexp"
	"sort"
)

// snapFileRe matches snapshot filenames written by Save: {twse|tpex}_YYYY-MM-DD.json.
var snapFileRe = regexp.MustCompile(`^(twse|tpex)_(\d{4}-\d{2}-\d{2})\.json$`)

// Loaded is a read-only in-memory index of 當日沖銷 snapshots across dates and both
// markets (a code belongs to exactly one market, so no key collisions). It answers "do we
// have a 當沖 record for (code, date)?" — and an absent answer means we LACK the data for
// that day, which a study must treat as a gap, never as a zero day-trading ratio.
type Loaded struct {
	byCode map[string]map[string]*StockDayTrade // code → asOfDate → record
	dates  []string                             // loaded snapshot dates, ascending
}

// Record returns the record for (code, date), or ok=false when absent.
func (l *Loaded) Record(code, date string) (*StockDayTrade, bool) {
	if l == nil {
		return nil, false
	}
	byDate, ok := l.byCode[code]
	if !ok {
		return nil, false
	}
	r, ok := byDate[date]
	return r, ok
}

// Dates returns the loaded snapshot dates (ascending). Empty when nothing was loaded.
func (l *Loaded) Dates() []string {
	if l == nil {
		return nil
	}
	return l.dates
}

// Codes returns how many distinct codes the index holds (a cheap coverage sanity check).
func (l *Loaded) Codes() int {
	if l == nil {
		return 0
	}
	return len(l.byCode)
}

// LoadRange reads every snapshot in dir whose date falls in [from, to] (inclusive,
// YYYY-MM-DD; an empty bound means unbounded on that side), across both markets.
// Graceful degradation (a read-side loader must never abort a study):
//   - dir missing                  → empty Loaded, nil.
//   - unreadable for other reasons → empty Loaded, err (caller may log + proceed).
//   - a malformed/foreign file     → skipped silently.
func LoadRange(dir, from, to string) (*Loaded, error) {
	l := &Loaded{byCode: map[string]map[string]*StockDayTrade{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return l, fmt.Errorf("daytrading: read dir %s: %w", dir, err)
	}

	dateSet := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := snapFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		date := m[2]
		if from != "" && date < from {
			continue
		}
		if to != "" && date > to {
			continue
		}
		dateSet[date] = true
	}

	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	l.index(dir, dates)
	return l, nil
}

// LoadHistory reads up to maxDays of snapshots on/before endDate (YYYY-MM-DD), mirroring
// internal/institution.LoadHistory. maxDays <= 0 means all available on/before endDate.
func LoadHistory(dir, endDate string, maxDays int) (*Loaded, error) {
	l, err := LoadRange(dir, "", endDate)
	if err != nil || maxDays <= 0 || len(l.dates) <= maxDays {
		return l, err
	}
	// Re-index against only the most recent maxDays so the index and Dates() agree.
	kept := l.dates[len(l.dates)-maxDays:]
	trimmed := &Loaded{byCode: map[string]map[string]*StockDayTrade{}}
	trimmed.index(dir, kept)
	return trimmed, nil
}

// index loads the given dates (both markets) into the code→date map.
func (l *Loaded) index(dir string, dates []string) {
	for _, date := range dates {
		for _, market := range []string{MarketTWSE, MarketTPEX} {
			snap, err := Load(dir, market, date)
			if err != nil {
				continue // missing market for this date, or malformed → skip
			}
			for i := range snap.Stocks {
				r := &snap.Stocks[i]
				byDate := l.byCode[r.Code]
				if byDate == nil {
					byDate = map[string]*StockDayTrade{}
					l.byCode[r.Code] = byDate
				}
				byDate[date] = r
			}
		}
	}
	l.dates = dates
}
