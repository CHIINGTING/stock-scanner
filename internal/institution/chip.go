package institution

// chip.go holds the PURE, deterministic per-stock institutional statistics (§8–§12). It
// depends on nothing but this package's own types — no BIAS, no price trend, no scanner.
//
// Input model:
//   - `expected` is the trading calendar (price-candle dates, ascending, the LAST element
//     being the report/as-of date). It is the authoritative set of "expected trading days"
//     (§9): a date absent here is a non-trading day and never a gap.
//   - `records` maps the dates we HAVE institution data for → the four category nets.
//   - A date in `expected` but missing from `records` is a REAL GAP (breaks streaks).

// dayNet is one aligned trading day for one category.
type dayNet struct {
	present bool
	net     int64
}

// Compute builds the interpreted view for one stock. todayVolume is same-day volume in 股
// (<=0 → net-buy ratio nil). todayReconciled is the §7 result on today's raw record
// (meaningful only when today is present).
func Compute(code, name, asOfDate string, expected []string, records map[string]CategoryNets, todayVolume int64, todayReconciled bool) StockChipView {
	view := StockChipView{Code: code, Name: name, AsOfDate: asOfDate}
	view.Foreign = legStats(alignSeries(expected, records, func(n CategoryNets) int64 { return n.Foreign }), todayVolume)
	view.Trust = legStats(alignSeries(expected, records, func(n CategoryNets) int64 { return n.Trust }), todayVolume)
	view.Dealer = legStats(alignSeries(expected, records, func(n CategoryNets) int64 { return n.Dealer }), todayVolume)
	view.Total = legStats(alignSeries(expected, records, func(n CategoryNets) int64 { return n.Total }), todayVolume)
	view.Completeness = completeness(expected, records, asOfDate)
	view.TotalMismatch = view.Completeness.DataComplete && !todayReconciled
	return view
}

// alignSeries projects records onto the expected calendar for one category (index-aligned
// with expected; missing dates stay {present:false}).
func alignSeries(expected []string, records map[string]CategoryNets, sel func(CategoryNets) int64) []dayNet {
	out := make([]dayNet, len(expected))
	for i, d := range expected {
		if r, ok := records[d]; ok {
			out[i] = dayNet{present: true, net: sel(r)}
		}
	}
	return out
}

func legStats(s []dayNet, vol int64) LegStats {
	st := LegStats{Transition: transition(s), CumulativeComplete: map[int]bool{}}
	n := len(s)
	todayPresent := n > 0 && s[n-1].present
	if todayPresent {
		st.TodayNet = s[n-1].net
	}
	st.Sum3d, st.CumulativeComplete[3] = sumWindow(s, 3)
	st.Sum5d, st.CumulativeComplete[5] = sumWindow(s, 5)
	st.Sum10d, st.CumulativeComplete[10] = sumWindow(s, 10)
	st.Sum20d, st.CumulativeComplete[20] = sumWindow(s, 20)

	buy, buyComplete := consecutive(s, true)
	sell, sellComplete := consecutive(s, false)
	st.ConsecutiveBuyDays = buy
	st.ConsecutiveSellDays = sell
	switch {
	case !todayPresent:
		st.StreakComplete = false
	case buy > 0:
		st.StreakComplete = buyComplete
	case sell > 0:
		st.StreakComplete = sellComplete
	default:
		st.StreakComplete = true // today is flat (net 0) — determinate
	}
	if todayPresent {
		st.NetBuyRatio = ratio(st.TodayNet, vol)
	}
	return st
}

// sumWindow sums the last n aligned days. complete=false when the window is shorter than n
// OR any day in it is missing (§8 — a cumulative that spans a gap is tainted).
func sumWindow(s []dayNet, n int) (int64, bool) {
	start := len(s) - n
	complete := true
	if start < 0 {
		start = 0
		complete = false
	}
	var sum int64
	for i := start; i < len(s); i++ {
		if s[i].present {
			sum += s[i].net
		} else {
			complete = false
		}
	}
	return sum, complete
}

// consecutive counts the streak of same-sign days back from today (§10). A zero ends the
// streak (returns count so far, complete=true — natural end). A missing (gap) day ends it
// with complete=false. Reaching the window start while still matching also yields
// complete=false (earlier history unknown — never bridge or assume).
func consecutive(s []dayNet, positive bool) (int, bool) {
	n := len(s)
	if n == 0 || !s[n-1].present {
		return 0, false // today missing → streak undetermined
	}
	count := 0
	for i := n - 1; i >= 0; i-- {
		if !s[i].present {
			return count, false // real gap before a natural end
		}
		net := s[i].net
		match := (positive && net > 0) || (!positive && net < 0)
		if !match {
			return count, true // opposite sign or zero → natural end
		}
		count++
	}
	return count, false // consumed the whole window still matching → can't confirm
}

// transition compares today vs the previous trading day WITH DATA (§11). Zero on either
// side, a missing previous day, or no previous day → NONE (never infer through a gap/zero).
func transition(s []dayNet) string {
	n := len(s)
	if n < 2 {
		return TransitionNone
	}
	today, prev := s[n-1], s[n-2]
	if !today.present || !prev.present {
		return TransitionNone
	}
	switch {
	case prev.net > 0 && today.net < 0:
		return TransitionBuyToSell
	case prev.net < 0 && today.net > 0:
		return TransitionSellToBuy
	default:
		return TransitionNone
	}
}

// ratio is net-buy % of same-day volume (§12). vol<=0 → nil (undefined, never 0).
func ratio(net, vol int64) *float64 {
	if vol <= 0 {
		return nil
	}
	r := float64(net) / float64(vol) * 100
	return &r
}

// completeness summarises the window (§8). today = last expected date (the as-of date).
func completeness(expected []string, records map[string]CategoryNets, asOfDate string) ChipCompleteness {
	c := ChipCompleteness{ExpectedDays: len(expected)}
	for _, d := range expected {
		if _, ok := records[d]; ok {
			c.ObservedDays++
		} else {
			c.MissingDates = append(c.MissingDates, d)
		}
	}
	_, c.DataComplete = records[asOfDate]
	return c
}
