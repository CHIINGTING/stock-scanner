package r6backtest

import "time"

// ProxySymbol is the market proxy used for Setup D crash-regime detection.
const ProxySymbol = "0050"

// regimeMergeGapBars: consecutive regime days within this gap belong to the same
// crash event; a larger gap starts a new event.
const regimeMergeGapBars = 5

// RegimeDay is the per-date crash-regime context.
type RegimeDay struct {
	InRegime      bool
	ProxyReturn20 float64 // 0050 20-day return %
	BreadthBelow  float64 // % of universe below MA20 that day
	EventID       int     // 0 when not in regime
	EventStart    time.Time
	EventEnd      time.Time
}

// RegimePanel maps date → RegimeDay, plus event bookkeeping.
type RegimePanel struct {
	byDate     map[string]RegimeDay
	EventCount int
	FirstDate  string
	LastDate   string
	ProxyOK    bool
}

// At returns the regime context for a date.
func (r *RegimePanel) At(dateKey string) (RegimeDay, bool) {
	if r == nil {
		return RegimeDay{}, false
	}
	rd, ok := r.byDate[dateKey]
	return rd, ok
}

// BuildRegimePanel computes crash-regime days from the 0050 proxy (20d return ≤
// -8%), segments consecutive regime days into events (merge gap ≤ 5 bars), and
// records breadth-below-MA20 on each regime day. No look-ahead: each day uses
// only bars up to that day. Returns a panel with ProxyOK=false if 0050 is absent.
func BuildRegimePanel(u *Universe, threshold float64) *RegimePanel {
	rp := &RegimePanel{byDate: map[string]RegimeDay{}}
	proxy, ok := u.Get(ProxySymbol)
	if !ok {
		return rp
	}
	rp.ProxyOK = true

	// 1. mark regime days + proxy 20d return, in proxy bar order.
	type mark struct {
		idx  int
		date string
		ret  float64
	}
	var marks []mark
	for i := 20; i < len(proxy.Candles); i++ {
		if proxy.Close[i-20] <= 0 {
			continue
		}
		ret := (proxy.Close[i]/proxy.Close[i-20] - 1) * 100
		dk := dateKey(proxy.Candles[i].Date)
		if ret <= threshold {
			marks = append(marks, mark{i, dk, ret})
		}
		// store proxy return for every day (used for relative-return lookups).
		rp.byDate[dk] = RegimeDay{ProxyReturn20: ret, InRegime: ret <= threshold}
	}
	if len(marks) == 0 {
		return rp
	}

	// 2. segment into events by proxy-bar-index gap.
	eventID := 0
	var evStart, evEnd time.Time
	var members [][]mark
	var cur []mark
	prevIdx := -1000
	for _, m := range marks {
		if m.idx-prevIdx > regimeMergeGapBars && len(cur) > 0 {
			members = append(members, cur)
			cur = nil
		}
		cur = append(cur, m)
		prevIdx = m.idx
	}
	if len(cur) > 0 {
		members = append(members, cur)
	}
	rp.EventCount = len(members)

	// 3. assign event id + start/end to each regime day; compute breadth.
	for _, ev := range members {
		eventID++
		evStart = proxy.Candles[ev[0].idx].Date
		evEnd = proxy.Candles[ev[len(ev)-1].idx].Date
		for _, m := range ev {
			rd := rp.byDate[m.date]
			rd.InRegime = true
			rd.EventID = eventID
			rd.EventStart = evStart
			rd.EventEnd = evEnd
			rd.BreadthBelow = breadthBelowMA20(u, m.date)
			rp.byDate[m.date] = rd
		}
	}
	rp.FirstDate = dateKey(proxy.Candles[marks[0].idx].Date)
	rp.LastDate = dateKey(proxy.Candles[marks[len(marks)-1].idx].Date)
	return rp
}

// breadthBelowMA20 returns the % of universe stocks (with a valid MA20 that day)
// whose close is below their MA20 — a market-breadth context measure.
func breadthBelowMA20(u *Universe, dateKey string) float64 {
	below, total := 0, 0
	for _, s := range u.Stocks {
		i, ok := s.IndexOf(dateKey)
		if !ok || s.MA20[i] <= 0 {
			continue
		}
		total++
		if s.Close[i] < s.MA20[i] {
			below++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(below) / float64(total) * 100
}
