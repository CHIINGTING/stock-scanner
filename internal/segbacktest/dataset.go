// Package segbacktest builds the 區間策略回測面板 — a self-contained interactive
// HTML page that replays "what if I had bought here and sold there" across several
// capital-deployment strategies (單筆、定期定額、逢跌狙擊、停利接回、落袋為安).
//
// Split of responsibilities:
//
//   - dataset.go — reads the scanner's .cache/*.json price cache and turns it into a
//     compact JSON payload (shared date axis + per-stock close series) that is embedded
//     into the page. No network access; the panel is fully offline.
//   - engine.js  — the strategy engine. Pure functions, no DOM. Single source of truth,
//     embedded into the page AND exercised by engine_js_test.go through jsc.
//   - panel.go   — renders the page (layout, chart, tables) around the two above.
//
// The panel is decision-support only: it reports what a rule would have done on
// historical closes. It never places orders and never claims predictive power.
package segbacktest

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MinBars is the shortest FULL history a symbol must have to be worth embedding.
// Below this a backtest window cannot even cover the 60-day moving average lead-in.
const MinBars = 70

// MinWindowBars is the floor once the history is trimmed by LoadOptions.Days. A
// recently listed stock legitimately has only a handful of bars inside a short
// window — that is worth embedding (the panel shows its available range) as long as
// there are two closes to run a buy and a sell against.
const MinWindowBars = 2

// cacheRecord mirrors the on-disk shape written by internal/fetcher's dataCache.
// Only the fields the panel needs are decoded.
type cacheRecord struct {
	FetchedAt time.Time `json:"fetched_at"`
	Data      struct {
		Symbol  string `json:"Symbol"`
		Name    string `json:"Name"`
		Market  string `json:"Market"`
		Candles []struct {
			Date     time.Time `json:"Date"`
			Close    float64   `json:"Close"`
			AdjClose float64   `json:"AdjClose"`
		} `json:"Candles"`
	} `json:"data"`
}

// Price is a close serialised with the shortest exact decimal form (940, 106.5),
// which keeps the embedded payload small. A zero means "no bar on that axis date".
type Price float64

// MarshalJSON rounds to 2 decimals — Yahoo occasionally hands back float artifacts
// like 106.50000000000001 — then emits the shortest representation.
func (p Price) MarshalJSON() ([]byte, error) {
	v := math.Round(float64(p)*100) / 100
	if v <= 0 {
		return []byte("0"), nil
	}
	return []byte(strconv.FormatFloat(v, 'f', -1, 64)), nil
}

// Series is one symbol's close history, aligned to Dataset.Axis starting at Offset.
// Closes[i] belongs to Axis[Offset+i]; a 0 marks a date the symbol did not trade
// (suspension, or listed on a different calendar than the union axis).
type Series struct {
	Code   string  `json:"c"`
	Name   string  `json:"n"`
	Market string  `json:"m"`
	Offset int     `json:"o"`
	Closes []Price `json:"p"`
}

// Dataset is the payload embedded in the panel.
type Dataset struct {
	// Axis is the union of every trading date, ascending, as "2006-01-02".
	Axis []string `json:"axis"`
	// Stocks is sorted by Code so the payload is byte-stable across runs.
	Stocks []Series `json:"stocks"`
	// FetchedAt is the newest cache timestamp seen, i.e. how fresh the prices are.
	FetchedAt time.Time `json:"fetchedAt"`
	// Adjusted records whether closes are split/dividend-adjusted.
	Adjusted bool `json:"adjusted"`
}

// LoadOptions controls which slice of the cache gets embedded.
type LoadOptions struct {
	// Dir is the price cache directory (default ".cache").
	Dir string
	// Days keeps only the most recent N trading dates; 0 keeps the full history.
	Days int
	// UseAdjusted reads AdjClose (falling back to Close) instead of raw Close.
	UseAdjusted bool
	// Only, when non-empty, restricts the universe to these stock codes.
	Only []string
}

// LoadDataset reads every cache file under opts.Dir and builds the embeddable payload.
// Unreadable or too-short files are skipped, not fatal — a panel over 1900 symbols
// should not fail because one cache entry is half-written.
func LoadDataset(opts LoadOptions) (*Dataset, error) {
	dir := opts.Dir
	if dir == "" {
		dir = ".cache"
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("scan cache dir: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no price cache found in %s — run the scanner first", dir)
	}
	sort.Strings(paths)

	var keep map[string]bool
	if len(opts.Only) > 0 {
		keep = make(map[string]bool, len(opts.Only))
		for _, c := range opts.Only {
			keep[strings.TrimSpace(c)] = true
		}
	}

	// Pass 1 — decode, collect per-symbol date→close maps and the union date axis.
	type parsed struct {
		code, name, market string
		byDate             map[string]float64
	}
	var (
		rows     []parsed
		dateSet  = map[string]bool{}
		newest   time.Time
		seen     = map[string]bool{}
		skipped  int
		duplicat int
	)
	for _, p := range paths {
		rec, err := readCacheFile(p)
		if err != nil {
			skipped++
			continue
		}
		code := strings.TrimSpace(rec.Data.Symbol)
		if code == "" {
			code = codeFromFilename(p)
		}
		if code == "" || (keep != nil && !keep[code]) {
			continue
		}
		if seen[code] {
			duplicat++
			continue
		}
		byDate := make(map[string]float64, len(rec.Data.Candles))
		for _, c := range rec.Data.Candles {
			px := c.Close
			if opts.UseAdjusted && c.AdjClose > 0 {
				px = c.AdjClose
			}
			if px <= 0 || c.Date.IsZero() {
				continue
			}
			byDate[c.Date.Format("2006-01-02")] = px
		}
		if len(byDate) < MinBars {
			skipped++
			continue
		}
		seen[code] = true
		rows = append(rows, parsed{code: code, name: strings.TrimSpace(rec.Data.Name),
			market: rec.Data.Market, byDate: byDate})
		for d := range byDate {
			dateSet[d] = true
		}
		if rec.FetchedAt.After(newest) {
			newest = rec.FetchedAt
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no usable price series in %s (skipped %d files)", dir, skipped)
	}

	axis := make([]string, 0, len(dateSet))
	for d := range dateSet {
		axis = append(axis, d)
	}
	sort.Strings(axis)
	if opts.Days > 0 && len(axis) > opts.Days {
		axis = axis[len(axis)-opts.Days:]
	}
	axisIdx := make(map[string]int, len(axis))
	for i, d := range axis {
		axisIdx[d] = i
	}

	// Pass 2 — project every symbol onto the axis, trimmed to its own first/last bar.
	ds := &Dataset{Axis: axis, FetchedAt: newest, Adjusted: opts.UseAdjusted}
	for _, r := range rows {
		s, ok := project(r.code, r.name, r.market, r.byDate, axisIdx, len(axis))
		if !ok {
			continue
		}
		ds.Stocks = append(ds.Stocks, s)
	}
	if len(ds.Stocks) == 0 {
		return nil, fmt.Errorf("every series fell outside the %d-date window", len(axis))
	}
	sort.Slice(ds.Stocks, func(i, j int) bool { return ds.Stocks[i].Code < ds.Stocks[j].Code })
	return ds, nil
}

// project aligns one symbol's date→close map onto the shared axis. It returns false
// when the symbol has fewer than MinWindowBars bars inside the window.
func project(code, name, market string, byDate map[string]float64,
	axisIdx map[string]int, axisLen int) (Series, bool) {

	first, last, bars := axisLen, -1, 0
	for d := range byDate {
		i, ok := axisIdx[d]
		if !ok {
			continue // outside the (possibly trimmed) window
		}
		bars++
		if i < first {
			first = i
		}
		if i > last {
			last = i
		}
	}
	if bars < MinWindowBars || last < first {
		return Series{}, false
	}
	closes := make([]Price, last-first+1)
	for d, px := range byDate {
		if i, ok := axisIdx[d]; ok {
			closes[i-first] = Price(px)
		}
	}
	return Series{Code: code, Name: name, Market: market, Offset: first, Closes: closes}, true
}

func readCacheFile(path string) (cacheRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheRecord{}, err
	}
	var rec cacheRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return cacheRecord{}, err
	}
	return rec, nil
}

// codeFromFilename recovers "2330" from ".cache/2330_TW.json" for cache entries whose
// Symbol field is empty.
func codeFromFilename(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	if i := strings.LastIndex(stem, "_"); i > 0 {
		return stem[:i]
	}
	return stem
}

// Bars returns the total number of embedded close values, for logging.
func (d *Dataset) Bars() int {
	n := 0
	for _, s := range d.Stocks {
		n += len(s.Closes)
	}
	return n
}

// Start and End are the first and last axis dates ("" when empty).
func (d *Dataset) Start() string {
	if len(d.Axis) == 0 {
		return ""
	}
	return d.Axis[0]
}

func (d *Dataset) End() string {
	if len(d.Axis) == 0 {
		return ""
	}
	return d.Axis[len(d.Axis)-1]
}

// Has reports whether the dataset contains a symbol.
func (d *Dataset) Has(code string) bool {
	for _, s := range d.Stocks {
		if s.Code == code {
			return true
		}
	}
	return false
}
