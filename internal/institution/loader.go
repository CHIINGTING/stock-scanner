package institution

import (
	"fmt"
	"os"
	"regexp"
	"sort"
)

// snapFileRe matches snapshot filenames written by Save: {twse|tpex}_YYYY-MM-DD.json.
var snapFileRe = regexp.MustCompile(`^(twse|tpex)_(\d{4}-\d{2}-\d{2})\.json$`)

// Loaded is a read-only in-memory index of institutional snapshots across dates and both
// markets (a code belongs to exactly one market, so no key collisions). It answers "do we
// have a record for (code, date)?" — the single source the completeness model consults.
type Loaded struct {
	byCode map[string]map[string]*StockChip // code → asOfDate → chip
	dates  []string                         // loaded snapshot dates, ascending
}

// Chip returns the record for (code, date), or ok=false when absent. Absent means we lack
// the data for that day — the completeness model treats it as a gap, never a zero (§9).
func (l *Loaded) Chip(code, date string) (*StockChip, bool) {
	if l == nil {
		return nil, false
	}
	byDate, ok := l.byCode[code]
	if !ok {
		return nil, false
	}
	c, ok := byDate[date]
	return c, ok
}

// Dates returns the loaded snapshot dates (ascending). Empty when nothing was loaded.
func (l *Loaded) Dates() []string { return l.dates }

// LoadHistory reads up to maxDays of snapshots on/before endDate (YYYY-MM-DD) from dir,
// across both markets. Graceful degradation (never interrupts a scan):
//   - dir missing            → empty Loaded, nil.
//   - unreadable for other reasons → empty Loaded, err (caller may log + proceed).
//   - a malformed/foreign file → skipped silently.
//
// maxDays <= 0 means all available on/before endDate.
func LoadHistory(dir, endDate string, maxDays int) (*Loaded, error) {
	l := &Loaded{byCode: map[string]map[string]*StockChip{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return l, fmt.Errorf("institution: read dir %s: %w", dir, err)
	}

	// Collect the distinct dates present (on/before endDate) with their markets.
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
		if endDate != "" && date > endDate {
			continue
		}
		dateSet[date] = true
	}

	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if maxDays > 0 && len(dates) > maxDays {
		dates = dates[len(dates)-maxDays:]
	}

	for _, date := range dates {
		for _, market := range []string{MarketTWSE, MarketTPEX} {
			snap, err := Load(dir, market, date)
			if err != nil {
				continue // missing market for this date, or malformed → skip
			}
			for i := range snap.Stocks {
				c := &snap.Stocks[i]
				byDate := l.byCode[c.Code]
				if byDate == nil {
					byDate = map[string]*StockChip{}
					l.byCode[c.Code] = byDate
				}
				byDate[date] = c
			}
		}
	}
	l.dates = dates
	return l, nil
}
