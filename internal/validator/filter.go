package validator

import "strings"

// FilterOptions selects which parsed signals are actually validated.
//
// Rationale: the daily report's 市場掃描 (market) tab scans a ~1000-stock universe
// and labels most of it SELL/HOLD. Those are screening states, not the scanner's
// actionable calls, and including them all would drown the hit rate in market
// breadth. The operational judgments live in the watchlist and positions tabs, so
// by default the market tab contributes only its BUY picks.
type FilterOptions struct {
	Tabs          map[string]bool // whitelist of source tabs; empty = allow all
	MarketBuyOnly bool            // when true, keep only BUY_GROUP rows from the market tab
}

// DefaultFilter is the recommended default described above.
func DefaultFilter() FilterOptions {
	return FilterOptions{
		Tabs:          map[string]bool{"watchlist": true, "positions": true, "market": true},
		MarketBuyOnly: true,
	}
}

// ParseTabs builds a tab whitelist from a comma-separated flag value.
// An empty string means "all tabs".
func ParseTabs(csv string) map[string]bool {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	m := map[string]bool{}
	for _, t := range strings.Split(csv, ",") {
		if t = strings.TrimSpace(t); t != "" {
			m[t] = true
		}
	}
	return m
}

// Apply returns the subset of snaps that pass the filter.
func (o FilterOptions) Apply(snaps []SignalSnapshot) []SignalSnapshot {
	out := make([]SignalSnapshot, 0, len(snaps))
	for _, s := range snaps {
		if len(o.Tabs) > 0 && !o.Tabs[s.SourceTab] {
			continue
		}
		if o.MarketBuyOnly && s.SourceTab == "market" && s.ActionGroup != BuyGroup {
			continue
		}
		out = append(out, s)
	}
	return out
}
