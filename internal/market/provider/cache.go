package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// CacheFeed reads the scanner's existing price cache from disk.
//
// It is NOT a Provider: Provider fetches from TWSE/TAIFEX over the network (M4/M5). This is
// the local feed that supplies the two dimensions the market layer can compute with no
// network at all — the benchmark's own price history and the breadth universe. Both come
// from .cache, which already holds ~1,900 symbols × ~490 sessions, so every threshold in
// the structural engine can be back-tested from day one.
//
// Freshness is inherited: these prices are exactly as current as the last scanner run.
//
// (The repo has other .cache readers — internal/segbacktest and internal/r6backtest each
// carry one shaped for their own needs. A fourth small reader here is the lesser evil
// versus importing a backtest package into the market layer; if a fifth ever appears, that
// is the moment to extract a shared one.)
type CacheFeed struct {
	// Dir is the cache directory. Empty → ".cache".
	Dir string
	// MinBars drops symbols too short to contribute a moving average. Zero → 120, matching
	// the longest MA the breadth panel computes.
	MinBars int
	// MinCoverage drops axis dates where fewer than this FRACTION of the loaded symbols
	// have a bar. Zero → 0.5.
	//
	// This is a data-quality gate, not a tuning knob. The cache legitimately contains dates
	// that are not universe trading days: the oldest weeks belong to a handful of symbols
	// fetched with a longer window, and the odd mid-series date carries a dozen instruments
	// (a partial session, or a fetch that straddled one). Left on the axis, such a date is
	// a zero for every other symbol — and since a moving-average window containing a gap is
	// unmeasurable, ONE sparse date silently evicts the whole universe from the MA60
	// population for 60 sessions and from MA120 for 120. Observed in this repo's own cache:
	// 2025-08-01 carries 11 of 1993 symbols and would corrupt breadth into 2026-01.
	MinCoverage float64
}

// UniverseData is the aligned universe plus what had to be discarded to align it. The
// dropped dates are returned rather than silently swallowed: a caller replaying history
// needs to know that its axis is not simply "every date in the cache".
type UniverseData struct {
	Dates   []string
	Closes  [][]float64 // Closes[symbol][dateIdx]; 0 = no bar that day
	Symbols int
	// DroppedDates are axis candidates removed for insufficient coverage, ascending.
	DroppedDates []string
}

// cacheFile mirrors the on-disk shape written by internal/fetcher's dataCache. Only the
// fields the market layer needs are decoded.
type cacheFile struct {
	Data struct {
		Symbol  string `json:"Symbol"`
		Name    string `json:"Name"`
		Candles []struct {
			Date  time.Time `json:"Date"`
			Close float64   `json:"Close"`
		} `json:"Candles"`
	} `json:"data"`
}

func (f CacheFeed) dir() string {
	if f.Dir == "" {
		return ".cache"
	}
	return f.Dir
}

func (f CacheFeed) minBars() int {
	if f.MinBars <= 0 {
		return 120
	}
	return f.MinBars
}

func (f CacheFeed) minCoverage() float64 {
	if f.MinCoverage <= 0 {
		return 0.5
	}
	return f.MinCoverage
}

// Benchmark returns one symbol's bars, oldest-first.
//
// asOf trims the series to that date inclusive (empty = no trimming). This is the
// look-ahead guard: replaying 2026-03-01 must not see 2026-03-02's close, and doing the
// trim here means every caller gets it for free.
func (f CacheFeed) Benchmark(symbol, asOf string) ([]model.Bar, error) {
	path, err := f.findSymbol(symbol)
	if err != nil {
		return nil, err
	}
	bars, err := readBars(path)
	if err != nil {
		return nil, err
	}
	if asOf != "" {
		bars = trimTo(bars, asOf)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("market: %s has no bars up to %s", symbol, asOf)
	}
	return bars, nil
}

// Universe returns every cached symbol projected onto one shared date axis.
//
// Closes[i][d] is symbol i's close on Dates[d], with 0 meaning "no bar that day". The zero
// is load-bearing: the breadth panel treats it as "not measurable", which is what keeps a
// suspension from being counted as a stock that fell below its own moving average.
//
// Dates that fewer than MinCoverage of the symbols traded on are NOT universe trading days
// and are removed from the axis entirely (see MinCoverage for why this matters far more
// than it looks). They are reported back in DroppedDates.
func (f CacheFeed) Universe(asOf string) (*UniverseData, error) {
	paths, err := filepath.Glob(filepath.Join(f.dir(), "*.json"))
	if err != nil {
		return nil, fmt.Errorf("market: scan %s: %w", f.dir(), err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("market: no price cache in %s — run the scanner first", f.dir())
	}
	sort.Strings(paths)

	var rows []map[string]float64
	coverage := map[string]int{}

	for _, p := range paths {
		bars, err := readBars(p)
		if err != nil {
			continue // a half-written cache entry must not sink the whole universe
		}
		if asOf != "" {
			bars = trimTo(bars, asOf)
		}
		if len(bars) < f.minBars() {
			continue
		}
		m := make(map[string]float64, len(bars))
		for _, b := range bars {
			m[b.Date] = b.Close
			coverage[b.Date]++
		}
		rows = append(rows, m)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("market: no symbol in %s has %d+ bars", f.dir(), f.minBars())
	}

	need := int(float64(len(rows)) * f.minCoverage())
	out := &UniverseData{Symbols: len(rows)}
	for d, n := range coverage {
		if n >= need {
			out.Dates = append(out.Dates, d)
		} else {
			out.DroppedDates = append(out.DroppedDates, d)
		}
	}
	sort.Strings(out.Dates)
	sort.Strings(out.DroppedDates)
	if len(out.Dates) == 0 {
		return nil, fmt.Errorf("market: no date in %s reached %.0f%% coverage of %d symbols",
			f.dir(), f.minCoverage()*100, len(rows))
	}

	out.Closes = make([][]float64, len(rows))
	for i, r := range rows {
		s := make([]float64, len(out.Dates))
		for d, date := range out.Dates {
			s[d] = r[date] // absent → 0 → "not measurable"
		}
		out.Closes[i] = s
	}
	return out, nil
}

// findSymbol locates <dir>/<CODE>_<MARKET>.json without knowing the market suffix.
func (f CacheFeed) findSymbol(symbol string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(f.dir(), symbol+"_*.json"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("market: %s not found in %s", symbol, f.dir())
	}
	sort.Strings(matches)
	return matches[0], nil
}

func readBars(path string) ([]model.Bar, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	out := make([]model.Bar, 0, len(cf.Data.Candles))
	for _, c := range cf.Data.Candles {
		if c.Close <= 0 || c.Date.IsZero() {
			continue
		}
		out = append(out, model.Bar{Date: c.Date.Format("2006-01-02"), Close: c.Close})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// trimTo drops every bar dated after asOf. Dates are YYYY-MM-DD so string order is
// chronological order.
func trimTo(bars []model.Bar, asOf string) []model.Bar {
	for i := len(bars) - 1; i >= 0; i-- {
		if strings.Compare(bars[i].Date, asOf) <= 0 {
			return bars[:i+1]
		}
	}
	return nil
}
