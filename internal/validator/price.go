package validator

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// PriceStore reads the project's existing on-disk OHLCV cache (.cache/*.json) and
// serves daily candle series to the validator. It reuses fetcher.StockData, whose
// exported fields match the cache JSON, so no separate price source is introduced.
//
// The store is read-only and offline: it never triggers a network fetch. Forward
// returns can therefore only be computed as far as the cache already extends; the
// evaluator marks anything beyond that PENDING.
type PriceStore struct {
	dir    string
	loaded map[string]*PriceSeries // code → series (nil = confirmed missing)
}

// cacheFile mirrors the two-level shape the fetcher writes: {fetched_at, data:{...}}.
type cacheFile struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Data      fetcher.StockData `json:"data"`
}

// PriceSeries is a stock's daily candles, oldest-first, with a date→index map.
type PriceSeries struct {
	Code    string
	Candles []fetcher.Candle
	byDate  map[int]int // yyyymmdd → candle index
}

// ForwardOutcome is the measured behaviour of a stock after a signal.
type ForwardOutcome struct {
	SignalPrice float64 // EOD close on the signal day (reference price)
	EntryDate   time.Time
	EntryPrice  float64 // next trading day's open (fallback close)

	Returns     map[int]float64 // horizon → return % vs entry price (only present horizons)
	MaxHorizon  int             // largest horizon with price data available
	MaxDrawdown float64         // % (<=0)
	MaxRunup    float64         // % (>=0)
}

// NewPriceStore builds a store over the given cache directory (default ".cache").
func NewPriceStore(dir string) *PriceStore {
	if dir == "" {
		dir = ".cache"
	}
	return &PriceStore{dir: dir, loaded: make(map[string]*PriceSeries)}
}

// Series returns the cached series for a code, or nil if no cache file exists.
// TW (上市) and TWO (上櫃) filename variants are both tried.
func (s *PriceStore) Series(code string) *PriceSeries {
	if ser, ok := s.loaded[code]; ok {
		return ser
	}
	var ser *PriceSeries
	for _, suffix := range []string{"_TW", "_TWO"} {
		path := filepath.Join(s.dir, code+suffix+".json")
		if series, err := loadSeries(code, path); err == nil && series != nil && len(series.Candles) > 0 {
			ser = series
			break
		}
	}
	s.loaded[code] = ser
	return ser
}

func loadSeries(code, path string) (*PriceSeries, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf cacheFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	candles := cf.Data.Candles
	sort.Slice(candles, func(i, j int) bool { return candles[i].Date.Before(candles[j].Date) })

	byDate := make(map[int]int, len(candles))
	for i, c := range candles {
		byDate[dateKey(c.Date)] = i
	}
	return &PriceSeries{Code: code, Candles: candles, byDate: byDate}, nil
}

// dateKey collapses a timestamp to a calendar-date integer (yyyymmdd) in UTC.
// Cache candle dates are stored as the TW trading day at 01:00Z, so the UTC
// calendar date equals the trading date.
func dateKey(t time.Time) int {
	y, m, d := t.UTC().Date()
	return y*10000 + int(m)*100 + d
}

// signalIndex returns the index of the candle on the signal date, or the first
// trading day on/after it when the exact date is absent (holiday alignment).
func (ps *PriceSeries) signalIndex(signalDate time.Time) (int, bool) {
	if i, ok := ps.byDate[dateKey(signalDate)]; ok {
		return i, true
	}
	target := dateKey(signalDate)
	for i, c := range ps.Candles {
		if dateKey(c.Date) >= target {
			return i, true
		}
	}
	return 0, false
}

// Outcome measures forward returns for a signal on signalDate over the given
// horizons (in trading days, counted from the entry day). It returns ok=false
// only when the signal day itself cannot be located or there is no next trading
// day to enter on. Individual horizons beyond the available data are simply
// omitted from Returns, and the evaluator treats those as PENDING.
func (ps *PriceSeries) Outcome(signalDate time.Time, horizons []int) (ForwardOutcome, bool) {
	var out ForwardOutcome
	out.Returns = make(map[int]float64)

	sigIdx, ok := ps.signalIndex(signalDate)
	if !ok {
		return out, false
	}
	out.SignalPrice = ps.Candles[sigIdx].Close

	entryIdx := sigIdx + 1
	if entryIdx >= len(ps.Candles) {
		// Signal is the most recent bar; no next-day entry exists yet.
		return out, false
	}
	entry := ps.Candles[entryIdx]
	entryPrice := entry.Open
	if entryPrice <= 0 {
		entryPrice = entry.Close
	}
	out.EntryDate = entry.Date
	out.EntryPrice = entryPrice
	if entryPrice <= 0 {
		return out, false
	}

	maxH := 0
	for _, h := range horizons {
		ti := entryIdx + h
		if ti >= len(ps.Candles) {
			continue
		}
		out.Returns[h] = pct(ps.Candles[ti].Close, entryPrice)
		if h > maxH {
			maxH = h
		}
	}
	out.MaxHorizon = maxH

	// Drawdown / runup over the realised window (entry+1 .. entry+maxH), using
	// intraday extremes so a spike that never closed is still captured.
	last := entryIdx + maxH
	if last >= len(ps.Candles) {
		last = len(ps.Candles) - 1
	}
	dd, ru := 0.0, 0.0
	for i := entryIdx + 1; i <= last; i++ {
		if lo := pct(ps.Candles[i].Low, entryPrice); lo < dd {
			dd = lo
		}
		if hi := pct(ps.Candles[i].High, entryPrice); hi > ru {
			ru = hi
		}
	}
	out.MaxDrawdown = dd
	out.MaxRunup = ru
	return out, true
}

// pct returns (v/base - 1) * 100, guarding against a zero base.
func pct(v, base float64) float64 {
	if base == 0 {
		return math.NaN()
	}
	return (v/base - 1) * 100
}
