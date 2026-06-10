package r6backtest

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// cacheFile mirrors the on-disk .cache record ({fetched_at, data:StockData}).
type cacheFile struct {
	Data fetcher.StockData `json:"data"`
}

func sma(x []float64, n int) []float64 {
	out := make([]float64, len(x))
	var sum float64
	for i := range x {
		sum += x[i]
		if i >= n {
			sum -= x[i-n]
		}
		if i >= n-1 {
			out[i] = sum / float64(n)
		}
	}
	return out
}

func dateKey(t time.Time) string { return t.Format("2006-01-02") }

// LoadUniverse reads every .cache/*.json into a Stock with precomputed series.
// Read-only: it never writes to the cache. watchlist/sector are optional tags.
func LoadUniverse(cacheDir string, minBars int, watchlist map[string]bool, sectorOf map[string]string) (*Universe, error) {
	files, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	if err != nil {
		return nil, err
	}
	u := &Universe{bySym: make(map[string]*Stock)}
	dateSet := make(map[string]struct{})
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var cf cacheFile
		if json.Unmarshal(b, &cf) != nil {
			continue
		}
		c := cf.Data.Candles
		if len(c) < minBars {
			continue
		}
		sort.Slice(c, func(i, j int) bool { return c[i].Date.Before(c[j].Date) })
		s := &Stock{
			Symbol:      cf.Data.Symbol,
			Name:        cf.Data.Name,
			Sector:      sectorOf[cf.Data.Symbol],
			IsWatchlist: watchlist[cf.Data.Symbol],
			Candles:     c,
			idxOf:       make(map[string]int, len(c)),
		}
		for i, k := range c {
			s.Close = append(s.Close, k.Close)
			s.High = append(s.High, k.High)
			s.Low = append(s.Low, k.Low)
			s.Vol = append(s.Vol, float64(k.Volume))
			dk := dateKey(k.Date)
			s.idxOf[dk] = i
			dateSet[dk] = struct{}{}
		}
		s.MA5 = sma(s.Close, 5)
		s.MA10 = sma(s.Close, 10)
		s.MA20 = sma(s.Close, 20)
		s.MA60 = sma(s.Close, 60)
		s.VolMA20 = sma(s.Vol, 20)
		s.RSI14 = indicator.RSI(s.Close, 14)
		s.ATR14 = indicator.ATR(s.High, s.Low, s.Close, 14)
		u.Stocks = append(u.Stocks, s)
		u.bySym[s.Symbol] = s
	}
	u.Axis = make([]string, 0, len(dateSet))
	for d := range dateSet {
		u.Axis = append(u.Axis, d)
	}
	sort.Strings(u.Axis)
	return u, nil
}

// newHighSince reports whether any bar in (from, to] printed a fresh 20-day high
// (close >= max high of the prior 20 bars) — i.e., a new pullback leg started.
func newHighSince(s *Stock, from, to int) bool {
	for k := from + 1; k <= to; k++ {
		if k >= 21 && s.Close[k] >= maxHigh(s, k-20, k-1) {
			return true
		}
	}
	return false
}

// MaxHigh returns the max High over [lo,hi] (clamped at 0).
func maxHigh(s *Stock, lo, hi int) float64 {
	if lo < 0 {
		lo = 0
	}
	m := 0.0
	for i := lo; i <= hi; i++ {
		if s.High[i] > m {
			m = s.High[i]
		}
	}
	return m
}

func minLow(s *Stock, lo, hi int) float64 {
	if lo < 0 {
		lo = 0
	}
	m := math.Inf(1)
	for i := lo; i <= hi; i++ {
		if s.Low[i] < m {
			m = s.Low[i]
		}
	}
	return m
}

// entryPoint is one accepted entry (stop-independent), reusable across stop
// policies in the R6-3 benchmark.
type entryPoint struct {
	s        *Stock
	i        int // detection bar
	entryIdx int // entry bar (i+1 in next_open mode)
	entry    float64
	trig     Trigger
}

func maxHorizon(p Params) int {
	maxH := 0
	for _, h := range p.Horizons {
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

// entryOf resolves the entry bar/price for detection bar i.
func entryOf(s *Stock, i int, p Params) (int, float64, bool) {
	n := len(s.Candles)
	entryIdx := i + 1
	if p.EntryMode == "signal_close" {
		entryIdx = i
	}
	if entryIdx >= n {
		return 0, 0, false
	}
	entry := s.Candles[entryIdx].Open
	if p.EntryMode == "signal_close" {
		entry = s.Close[entryIdx]
	}
	if entry <= 0 {
		return 0, 0, false
	}
	return entryIdx, entry, true
}

// collectEntries runs detection + cooldown de-dup once and returns the entries
// (independent of any stop policy). Look-ahead safety: detection reads only bars
// <= i; cooldown resets on a fresh 20-day high (new pullback leg).
func collectEntries(u *Universe, rs *RSPanel, setup Setup, p Params) []entryPoint {
	var eps []entryPoint
	for _, s := range u.Stocks {
		n := len(s.Candles)
		cdUntil := map[int]int{}
		lastEntry := map[int]int{}
		hasEntry := map[int]bool{}
		for i := p.Warmup; i+1+p.MinForward <= n-1; i++ {
			trig := setup.Detect(u, rs, s, i, p)
			if trig == nil {
				continue
			}
			b := trig.Bucket
			if hasEntry[b] && i < cdUntil[b] && !newHighSince(s, lastEntry[b], i) {
				continue
			}
			entryIdx, entry, ok := entryOf(s, i, p)
			if !ok {
				continue
			}
			cdUntil[b] = i + p.Cooldown
			lastEntry[b] = i
			hasEntry[b] = true
			eps = append(eps, entryPoint{s: s, i: i, entryIdx: entryIdx, entry: entry, trig: *trig})
		}
	}
	return eps
}

// RunSetup backtests one setup over the universe using the baseline stop derived
// from p.StopRules (identical to R6-2b). Entry executes at i+1 open; outcomes are
// measured strictly after the entry bar.
func RunSetup(u *Universe, rs *RSPanel, setup Setup, p Params) []Trade {
	maxH := maxHorizon(p)
	policy := rulesStop{rules: p.StopRules}
	eps := collectEntries(u, rs, setup, p)
	trades := make([]Trade, 0, len(eps))
	for _, ep := range eps {
		end := ep.entryIdx + maxH
		if end > len(ep.s.Candles)-1 {
			end = len(ep.s.Candles) - 1
		}
		sr := policy.Eval(ep.s, ep.entryIdx, ep.entry, p, end)
		if t := buildTrade(setup.Name(), rs, ep, sr, maxH); t != nil {
			trades = append(trades, *t)
		}
	}
	return trades
}

// buildTrade computes forward returns / drawdown for an entry under a given stop
// result. Stop-adjusted returns use the stop exit price when the stop fell on or
// before the horizon; hold returns ignore the stop entirely.
func buildTrade(name string, rs *RSPanel, ep entryPoint, sr StopResult, maxH int) *Trade {
	s, i, entryIdx, entry, trig := ep.s, ep.i, ep.entryIdx, ep.entry, ep.trig
	n := len(s.Candles)

	d := dateKey(s.Candles[i].Date)
	rsPct, _ := rs.At(s.Symbol, d)
	hi52 := maxHigh(s, i-249, i)
	dist52 := math.NaN()
	if hi52 > 0 {
		dist52 = (hi52 - s.Close[i]) / hi52 * 100
	}
	ma20d, ma60d := math.NaN(), math.NaN()
	if s.MA20[i] > 0 {
		ma20d = (s.Close[i] - s.MA20[i]) / s.MA20[i] * 100
	}
	if s.MA60[i] > 0 {
		ma60d = (s.Close[i] - s.MA60[i]) / s.MA60[i] * 100
	}

	end := entryIdx + maxH
	if end > n-1 {
		end = n - 1
	}
	dd := 0.0
	for j := entryIdx + 1; j <= end; j++ {
		x := (s.Low[j]/entry - 1) * 100
		if x < dd {
			dd = x
		}
	}
	// stop-aware realized drawdown: only up to the exit (stop bar) — what the
	// trade actually experienced before being stopped out.
	lastBar := end
	if sr.HitStop && sr.StopBar < lastBar {
		lastBar = sr.StopBar
	}
	rdd := 0.0
	for j := entryIdx + 1; j <= lastBar; j++ {
		x := (s.Low[j]/entry - 1) * 100
		if x < rdd {
			rdd = x
		}
	}

	stopRet := math.NaN()
	if sr.HitStop {
		stopRet = (sr.StopPrice/entry - 1) * 100
	}
	hold := func(h int) float64 {
		j := entryIdx + h
		if j > n-1 {
			return math.NaN()
		}
		return (s.Close[j]/entry - 1) * 100
	}
	stopAdj := func(h int) float64 {
		if sr.HitStop && sr.StopBar <= entryIdx+h {
			return stopRet
		}
		return hold(h)
	}

	return &Trade{
		SetupName:             name,
		StockCode:             s.Symbol,
		StockName:             s.Name,
		IsWatchlistMember:     s.IsWatchlist,
		EntryDate:             s.Candles[entryIdx].Date,
		EntryPrice:            entry,
		SignalDate:            s.Candles[i].Date,
		SignalClose:           s.Close[i],
		Return5d:              stopAdj(5),
		Return10d:             stopAdj(10),
		Return20d:             stopAdj(20),
		Return60d:             stopAdj(60),
		HoldReturn5d:          hold(5),
		HoldReturn10d:         hold(10),
		HoldReturn20d:         hold(20),
		HoldReturn60d:         hold(60),
		MaxDrawdownAfterEntry: dd,
		RealizedDrawdown:      rdd,
		HitStop:               sr.HitStop,
		StopReason:            sr.Reason,
		StopDate:              sr.StopDate,
		StopPrice:             sr.StopPrice,
		RSRankAtEntry:         rsPct,
		DistanceFrom52wHigh:   dist52,
		PullbackPctFromHigh:   trig.PullbackPct,
		MA20DistancePct:       ma20d,
		MA60DistancePct:       ma60d,
		VCPValid:              trig.VCPValid,
		VCPGrade:              trig.VCPGrade,
		VCPQualityScore:       trig.VCPQualityScore,
		MomentumFlow:          trig.MomentumFlow,
		MTFSignal:             trig.MTFSignal,
		Sector:                s.Sector,
		Bucket:                trig.Bucket,
		Crash:                 trig.Crash,
	}
}
