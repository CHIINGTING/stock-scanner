package news

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// polarity is bullish-ish (+1) vs bearish-ish (−1) grouping. It is used ONLY for
// divergence detection (hasDivergence) and cross-source conflict marking (markConflicts).
// It MUST NOT drive market-summary labels — that would mislabel BULLISH as ROTATION_IN.
func polarity(t SignalType) int {
	switch t {
	case SignalBullish, SignalRotationIn:
		return +1
	case SignalBearish, SignalRotationOut, SignalRiskWarning:
		return -1
	default:
		return 0
	}
}

// BuildViews groups signals into per-stock display views. A stock's StockItems are
// signals naming its code; its SectorItems are signals on the sectors it belongs to.
// Divergence is set when the stock-level and sector-level polarities conflict. Every
// returned view has Computed=true; codes with no signals get no entry (caller leaves
// WatchlistEntry.News nil so the card renders exactly as today).
func BuildViews(signals []NewsSignal, idx *ResolveIndex) map[string]*StockNewsView {
	views := map[string]*StockNewsView{}
	get := func(code string) *StockNewsView {
		v, ok := views[code]
		if !ok {
			v = &StockNewsView{Computed: true}
			views[code] = v
		}
		return v
	}

	// index sector → signals for sector-item lookup.
	sectorSignals := map[string][]NewsSignal{}
	for _, sig := range signals {
		for _, sec := range sig.Sectors {
			sectorSignals[sec] = append(sectorSignals[sec], sig)
		}
	}

	// stock-level items.
	for _, sig := range signals {
		for _, st := range sig.Stocks {
			if st.Code == "" {
				continue
			}
			get(st.Code).StockItems = append(get(st.Code).StockItems, sig)
		}
	}

	// sector-level items for each stock we already know about (from its sectors).
	for code, v := range views {
		seen := map[string]bool{}
		for _, sec := range idx.SectorsForCode(code) {
			for _, sig := range sectorSignals[sec] {
				k := sig.EventID + string(sig.Signal) + sec
				if seen[k] {
					continue
				}
				seen[k] = true
				v.SectorItems = append(v.SectorItems, sig)
			}
		}
		v.Divergence = hasDivergence(v.StockItems, v.SectorItems)
	}
	return views
}

func hasDivergence(stockItems, sectorItems []NewsSignal) bool {
	var sPos, sNeg, secPos, secNeg bool
	for _, s := range stockItems {
		switch polarity(s.Signal) {
		case +1:
			sPos = true
		case -1:
			sNeg = true
		}
	}
	for _, s := range sectorItems {
		switch polarity(s.Signal) {
		case +1:
			secPos = true
		case -1:
			secNeg = true
		}
	}
	return (sPos && secNeg) || (sNeg && secPos)
}

// BuildSummary aggregates non-expired sector signals into one row per sector, shown across
// several day-windows (default 1/3/7) so fast rotation is visible (方案 A + multi-window).
// Each window cell folds signals into 🟢/🟡/🔴 with a net conclusion; the widest window
// also gates which sectors appear and drives the sort. Fresh/Active/Expired count all
// signals; expired signals are tallied but never drive a row.
func BuildSummary(signals []NewsSignal, now time.Time, cfg NewsConfig) *MarketNewsSummary {
	cfg = cfg.Defaulted()
	windows := append([]int(nil), cfg.BannerWindows...)
	sort.Ints(windows)
	maxW := windows[len(windows)-1]

	sum := &MarketNewsSummary{AsOf: now, Windows: windows}

	type wcell struct{ pos, risk, neg int }
	type sacc struct {
		cells  map[int]*wcell // window days → counts
		latest NewsSignal     // most recent within maxW
		has    bool
	}
	byS := map[string]*sacc{}
	var order []string

	for _, sig := range signals {
		switch BucketAt(sig.PublishedAt, now, cfg) {
		case AgeFresh:
			sum.Fresh++
		case AgeActive, AgeDecaying:
			sum.Active++
		case AgeExpired:
			sum.Expired++
			continue
		}
		age := AgeInDays(sig.PublishedAt, now)
		if age > maxW {
			continue // within max-age but outside the widest banner window
		}
		p := polarity(sig.Signal)
		for _, sec := range sig.Sectors {
			a, ok := byS[sec]
			if !ok {
				a = &sacc{cells: map[int]*wcell{}}
				byS[sec] = a
				order = append(order, sec)
			}
			for _, w := range windows {
				if age > w {
					continue
				}
				c := a.cells[w]
				if c == nil {
					c = &wcell{}
					a.cells[w] = c
				}
				switch {
				case p > 0:
					c.pos++
				case sig.Signal == SignalRiskWarning:
					c.risk++
				case p < 0:
					c.neg++
				}
			}
			if !a.has || sig.PublishedAt.After(a.latest.PublishedAt) {
				a.latest = sig
				a.has = true
			}
		}
	}

	rows := make([]SectorNewsRow, 0, len(order))
	for _, sec := range order {
		a := byS[sec]
		row := SectorNewsRow{Name: sec, LatestType: a.latest.Signal, LatestEP: epLabel(a.latest.EventID)}
		for _, w := range windows {
			c := a.cells[w]
			cell := SectorWindowCell{Days: w}
			if c != nil {
				cell.Pos, cell.Risk, cell.Neg = c.pos, c.risk, c.neg
				cell.Net = netConclusion(c.pos, c.risk, c.neg, latestBucket(a.latest.Signal))
			}
			cell.Display = cellDisplay(cell, w == maxW)
			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}
	// sort by the widest window's lean (pos-neg) desc, then more-discussed first.
	widest := func(r SectorNewsRow) SectorWindowCell { return r.Cells[len(r.Cells)-1] }
	sort.SliceStable(rows, func(i, j int) bool {
		ci, cj := widest(rows[i]), widest(rows[j])
		li, lj := ci.Pos-ci.Neg, cj.Pos-cj.Neg
		if li != lj {
			return li > lj
		}
		return (ci.Pos + ci.Risk + ci.Neg) > (cj.Pos + cj.Risk + cj.Neg)
	})
	sum.Sectors = rows
	return sum
}

// cellDisplay renders a compact window cell: a counts tally for the widest window
// (🟢5 🟡1 🔴3), otherwise a net-lean chip (🟢偏多 / 🟢🔴分歧). Empty → "－".
func cellDisplay(c SectorWindowCell, widest bool) string {
	if c.Pos == 0 && c.Risk == 0 && c.Neg == 0 {
		return "－"
	}
	if widest {
		var parts []string
		if c.Pos > 0 {
			parts = append(parts, fmt.Sprintf("🟢×%d", c.Pos))
		}
		if c.Risk > 0 {
			parts = append(parts, fmt.Sprintf("🟡×%d", c.Risk))
		}
		if c.Neg > 0 {
			parts = append(parts, fmt.Sprintf("🔴×%d", c.Neg))
		}
		return strings.Join(parts, " ")
	}
	return netDot(c) + c.Net
}

// netDot picks the leading dot(s) for a net-lean chip.
func netDot(c SectorWindowCell) string {
	switch c.Net {
	case "偏多":
		return "🟢"
	case "偏空":
		return "🔴"
	case "風險":
		return "🟡"
	}
	// 分歧 / X轉Y → show every polarity present.
	s := ""
	if c.Pos > 0 {
		s += "🟢"
	}
	if c.Risk > 0 {
		s += "🟡"
	}
	if c.Neg > 0 {
		s += "🔴"
	}
	return s
}

// latestBucket folds a SignalType into pos/risk/neg for the net conclusion.
func latestBucket(t SignalType) string {
	switch {
	case polarity(t) > 0:
		return "偏多"
	case t == SignalRiskWarning:
		return "風險"
	case polarity(t) < 0:
		return "偏空"
	default:
		return ""
	}
}

// netConclusion derives a one-phrase net lean from the tally + the latest signal's bucket.
// Rules (raw counts): mixed pos&neg of similar size → 分歧; otherwise the dominant bucket,
// annotated "X轉Y" when the latest signal flipped away from the dominant one.
func netConclusion(pos, risk, neg int, latest string) string {
	if pos == 0 && risk == 0 && neg == 0 {
		return "—"
	}
	if pos > 0 && neg > 0 {
		lo, hi := neg, pos
		if pos < neg {
			lo, hi = pos, neg
		}
		if lo*2 >= hi { // comparable both-way → genuine split
			return "分歧"
		}
	}
	top := "偏多"
	topN := pos
	if risk > topN {
		top, topN = "風險", risk
	}
	if neg > topN {
		top, topN = "偏空", neg
	}
	if latest != "" && latest != top {
		return top + "轉" + latest
	}
	return top
}

// epLabel renders an episode label from an event id ("gooaye-ep-677" → "EP677").
func epLabel(eventID string) string {
	if n := EpisodeNumber(eventID); n > 0 {
		return fmt.Sprintf("EP%d", n)
	}
	return ""
}
