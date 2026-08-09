package fetcher

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PositionEntry is one holding in the positions/portfolio section of stocks.yaml.
type PositionEntry struct {
	Code string `yaml:"code"`
	Name string `yaml:"name"`
	// Market: "TW" (TWSE 上市) | "TWO" (TPEX 上櫃) | "" (auto-detect)
	Market string `yaml:"market"`
	// Entry is the entry price (preferred). Cost is the legacy alias.
	Entry  float64 `yaml:"entry"`
	Cost   float64 `yaml:"cost"` // backward compat: used when Entry == 0
	Shares int     `yaml:"shares"`
}

// EntryPrice returns the effective entry/cost price.
func (e PositionEntry) EntryPrice() float64 {
	if e.Entry > 0 {
		return e.Entry
	}
	return e.Cost
}

// WatchEntry is one item in the watchlist section.
type WatchEntry struct {
	Code   string `yaml:"code"`
	Name   string `yaml:"name"`
	Market string `yaml:"market"` // "TW" | "TWO" | ""
}

// StockList is the top-level structure of stocks.yaml.
//
// Ownership of each section is deliberate and asymmetric:
//
//	positions        HUMAN ONLY. Real money. No tool writes here, ever.
//	watchlist_pinned HUMAN ONLY. Candidates you added yourself and want kept.
//	watchlist        MACHINE. Rebuilt from each scan; anything not qualifying today is gone.
//
// The split exists because the machine watchlist is a SNAPSHOT of today's qualifying
// candidates, not an archive. Before this split it was append-only and had grown to 748
// entries against a 66-entry daily signal — 92% sediment, which is the same as having no
// shortlist at all.
type StockList struct {
	// Positions is the canonical key (new). Portfolio is the legacy alias.
	Positions []PositionEntry `yaml:"positions"`
	Portfolio []PositionEntry `yaml:"portfolio"` // backward compat
	Watchlist []WatchEntry    `yaml:"watchlist"`
	// WatchlistPinned survives every rebuild. Put your own ideas here — anything the daily
	// scan did not surface would otherwise vanish on the next run.
	WatchlistPinned []WatchEntry `yaml:"watchlist_pinned"`
}

// AllWatchlist merges the pinned and machine watchlists for analysis.
//
// Pinned entries come FIRST and win on duplicates: if you pinned a stock and the scan also
// surfaced it, the entry you wrote (with your own name/market annotations) is the one used.
// Codes already held in positions are excluded — a holding is tracked in the positions view,
// and duplicating it as a candidate would report the same stock twice with different advice.
func (sl *StockList) AllWatchlist() []WatchEntry {
	held := map[string]bool{}
	for _, p := range sl.AllPositions() {
		held[p.Code] = true
	}
	seen := map[string]bool{}
	var all []WatchEntry
	for _, group := range [][]WatchEntry{sl.WatchlistPinned, sl.Watchlist} {
		for _, e := range group {
			if e.Code == "" || seen[e.Code] || held[e.Code] {
				continue
			}
			seen[e.Code] = true
			all = append(all, e)
		}
	}
	return all
}

// AllPositions merges both positions: and portfolio: sections.
// Entries in positions: take precedence; duplicates (by code) are skipped.
func (sl *StockList) AllPositions() []PositionEntry {
	seen := make(map[string]bool)
	var all []PositionEntry
	for _, e := range sl.Positions {
		if !seen[e.Code] {
			seen[e.Code] = true
			all = append(all, e)
		}
	}
	for _, e := range sl.Portfolio {
		if !seen[e.Code] {
			seen[e.Code] = true
			all = append(all, e)
		}
	}
	return all
}

// LoadStockList reads and parses stocks.yaml.
func LoadStockList(path string) (*StockList, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var sl StockList
	if err := yaml.Unmarshal(data, &sl); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &sl, nil
}
