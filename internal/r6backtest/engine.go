package r6backtest

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
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

// RunSetup backtests one setup over the whole universe and returns the trades.
// Look-ahead safety: detection reads only bars <= i (setup's responsibility),
// entry executes at bar i+1 open, and all outcomes are measured strictly after
// the entry bar. Cooldown de-dups overlapping triggers on the same stock+bucket.
func RunSetup(u *Universe, rs *RSPanel, setup Setup, p Params) []Trade {
	var trades []Trade
	maxH := 0
	for _, h := range p.Horizons {
		if h > maxH {
			maxH = h
		}
	}
	for _, s := range u.Stocks {
		n := len(s.Candles)
		cdUntil := map[int]int{} // bucket → first re-entry-allowed index
		for i := p.Warmup; i+1+p.MinForward <= n-1; i++ {
			trig := setup.Detect(u, rs, s, i, p)
			if trig == nil {
				continue
			}
			// Cooldown: suppress unless past cooldown OR a fresh 20-day high
			// since detection started a new pullback leg.
			newLeg := s.Close[i] >= maxHigh(s, i-19, i-1)
			if i < cdUntil[trig.Bucket] && !newLeg {
				continue
			}
			cdUntil[trig.Bucket] = i + p.Cooldown

			t := buildTrade(setup.Name(), rs, s, i, trig, p, maxH)
			if t != nil {
				trades = append(trades, *t)
			}
		}
	}
	return trades
}

// buildTrade simulates entry at i+1 open and computes forward returns, drawdown,
// and the first stop hit. Returns nil if the entry bar does not exist.
func buildTrade(name string, rs *RSPanel, s *Stock, i int, trig *Trigger, p Params, maxH int) *Trade {
	n := len(s.Candles)
	entryIdx := i + 1
	if p.EntryMode == "signal_close" {
		entryIdx = i
	}
	if entryIdx >= n {
		return nil
	}
	entry := s.Candles[entryIdx].Open
	if p.EntryMode == "signal_close" {
		entry = s.Close[entryIdx]
	}
	if entry <= 0 {
		return nil
	}

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

	ret := func(h int) float64 {
		j := entryIdx + h
		if j > n-1 {
			return math.NaN()
		}
		return (s.Close[j]/entry - 1) * 100
	}

	// max drawdown over the longest available horizon window after entry.
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

	// first stop hit (scanning forward, bar by bar).
	swingLow := minLow(s, i-p.SwingLowback+1, i)
	hitStop, reason := false, ""
	for j := entryIdx + 1; j <= end && !hitStop; j++ {
		for _, rule := range p.StopRules {
			switch rule {
			case "BREAK_MA60":
				if s.MA60[j] > 0 && s.Close[j] < s.MA60[j] {
					hitStop, reason = true, "BREAK_MA60"
				}
			case "BREAK_SWING_LOW":
				if swingLow > 0 && s.Close[j] < swingLow {
					hitStop, reason = true, "BREAK_SWING_LOW"
				}
			case "PCT_-8":
				if (s.Close[j]/entry-1)*100 <= -8 {
					hitStop, reason = true, "PCT_-8"
				}
			case "PCT_-10":
				if (s.Close[j]/entry-1)*100 <= -10 {
					hitStop, reason = true, "PCT_-10"
				}
			}
			if hitStop {
				break
			}
		}
	}

	return &Trade{
		SetupName:             name,
		StockCode:             s.Symbol,
		StockName:             s.Name,
		IsWatchlistMember:     s.IsWatchlist,
		EntryDate:             s.Candles[entryIdx].Date,
		EntryPrice:            entry,
		Exit5dReturn:          ret(5),
		Exit10dReturn:         ret(10),
		Exit20dReturn:         ret(20),
		Exit60dReturn:         ret(60),
		MaxDrawdownAfterEntry: dd,
		HitStop:               hitStop,
		StopReason:            reason,
		RSRankAtEntry:         rsPct,
		DistanceFrom52wHigh:   dist52,
		PullbackPctFromHigh:   trig.PullbackPct,
		MA20DistancePct:       ma20d,
		MA60DistancePct:       ma60d,
		VCPValid:              trig.VCPValid,
		MomentumFlow:          trig.MomentumFlow,
		MTFSignal:             trig.MTFSignal,
		Sector:                s.Sector,
		Bucket:                trig.Bucket,
	}
}
