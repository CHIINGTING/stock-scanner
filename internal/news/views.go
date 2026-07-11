package news

import (
	"sort"
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

// BuildSummary aggregates sector-level signals into the market banner, grouping sectors
// by their actual SignalType (NOT by polarity), so a BULLISH sector is labeled 偏多 and
// only a genuine ROTATION_IN sector is labeled 資金輪動進場. A sector may appear in more
// than one group when signals conflict. Fresh/Active/Expired count all signals; expired
// signals are tallied but excluded from the labels.
func BuildSummary(signals []NewsSignal, now time.Time, cfg NewsConfig) *MarketNewsSummary {
	sum := &MarketNewsSummary{AsOf: now}
	byType := map[SignalType]map[string]bool{}
	addSector := func(t SignalType, sec string) {
		if byType[t] == nil {
			byType[t] = map[string]bool{}
		}
		byType[t][sec] = true
	}
	for _, sig := range signals {
		switch BucketAt(sig.PublishedAt, now, cfg) {
		case AgeFresh:
			sum.Fresh++
		case AgeActive, AgeDecaying:
			sum.Active++
		case AgeExpired:
			sum.Expired++
			continue // tally only; expired signals do not drive labels
		}
		for _, sec := range sig.Sectors {
			addSector(sig.Signal, sec)
		}
	}
	sum.RotationIn = sortedKeys(byType[SignalRotationIn])
	sum.RotationOut = sortedKeys(byType[SignalRotationOut])
	sum.Bullish = sortedKeys(byType[SignalBullish])
	sum.Bearish = sortedKeys(byType[SignalBearish])
	sum.RiskWarning = sortedKeys(byType[SignalRiskWarning])
	return sum
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
