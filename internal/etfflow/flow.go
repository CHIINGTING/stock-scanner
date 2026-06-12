package etfflow

import (
	"math"
	"sort"
)

// ──────────────────────────────────────────────────────────────────────────────
// Flow calculator (R8-2).
//
// Given each watched ETF's ordered snapshot history, derive per-stock holdings
// flow: day-over-day delta, consecutive buy/sell day streaks, active/passive
// aggregates, and sector-level deltas. Everything here is a PURE function over
// snapshots — it reads no forward data, mutates nothing, and has no dependency on
// the scanner package. See SPEC §7.
//
// flow_pressure_ratio = |delta_shares| / stock_avg_volume_20d needs the stock's
// average volume, which lives in the scanner. To keep this package free of any
// reverse dependency, the calculator emits delta_shares only; the ratio is a
// separate pure helper (PressureRatio) the caller invokes at attach time with the
// avg volume it already has. Signal classification + data_lag are R8-3, not here.
// ──────────────────────────────────────────────────────────────────────────────

// History is one ETF's snapshots. Order does not matter on input — CalculateFlows
// sorts a defensive copy ascending by AsOfDate before computing.
type History struct {
	ETFCode   string
	ETFType   string // TypeActive | TypePassive
	Snapshots []*Snapshot
}

// StockETFLeg is one ETF's flow for one stock: the latest day-over-day delta plus
// the consecutive buy/sell streaks measured over the whole snapshot history.
type StockETFLeg struct {
	ETFCode      string
	ETFType      string
	AsOfDate     string // latest ("today") snapshot's as-of date
	PrevAsOfDate string // previous snapshot's as-of date ("" when only one snapshot)

	TodayShares int64
	PrevShares  int64
	DeltaShares int64   // TodayShares - PrevShares
	DeltaWeight float64 // today weight - prev weight

	ConsecutiveBuyDays  int // most-recent run of positive daily deltas (0 if newest day isn't a buy)
	ConsecutiveSellDays int // most-recent run of negative daily deltas (0 if newest day isn't a sell)
}

// StockFlow aggregates every ETF leg touching one stock, split active vs passive.
type StockFlow struct {
	StockCode string
	StockName string
	Legs      []StockETFLeg

	TotalActiveDeltaShares  int64
	TotalPassiveDeltaShares int64
	TotalDeltaShares        int64
}

// SectorFlow is the active/passive ETF delta summed across a sector's stocks
// (context only — never fed into rotation scoring; SPEC §1 decision 3).
type SectorFlow struct {
	Sector             string
	ActiveDeltaShares  int64
	PassiveDeltaShares int64
}

// CalculateFlows builds a per-stock flow map keyed by stock code from the watched
// ETFs' histories. A stock gets a leg from an ETF only when it is currently held
// (TodayShares != 0) or its latest delta is non-zero (e.g. fully sold out today);
// stocks long gone from an ETF add no noise. Stocks with no surviving leg are
// omitted from the result.
func CalculateFlows(histories []History) map[string]*StockFlow {
	out := make(map[string]*StockFlow)

	for _, h := range histories {
		snaps := sortedSnapshots(h.Snapshots)
		n := len(snaps)
		if n == 0 {
			continue
		}
		latest := snaps[n-1]
		asOf := latest.AsOfDate
		prevAsOf := ""
		if n >= 2 {
			prevAsOf = snaps[n-2].AsOfDate
		}

		// Per-snapshot lookup of (shares, weight, name) for every stock this ETF
		// has ever held, so a stock missing from a given day reads as 0 shares.
		seriesShares, latestName := sharesSeries(snaps)

		for code, shares := range seriesShares {
			today := shares[n-1]
			// With only one snapshot there is no prior day to diff against, so the
			// delta is 0 (undefined), NOT "the whole position is a fresh buy". The
			// multi-snapshot new-add case legitimately reads prev=0 because the
			// previous snapshot genuinely lacked the stock.
			var prev, delta int64
			if n >= 2 {
				prev = shares[n-2]
				delta = today - prev
			}
			if today == 0 && delta == 0 {
				continue // never held now and no recent move → no leg
			}
			buy, sell := consecutiveDays(shares)

			leg := StockETFLeg{
				ETFCode:             h.ETFCode,
				ETFType:             h.ETFType,
				AsOfDate:            asOf,
				PrevAsOfDate:        prevAsOf,
				TodayShares:         today,
				PrevShares:          prev,
				DeltaShares:         delta,
				DeltaWeight:         weightDelta(snaps, code),
				ConsecutiveBuyDays:  buy,
				ConsecutiveSellDays: sell,
			}

			sf := out[code]
			if sf == nil {
				sf = &StockFlow{StockCode: code, StockName: latestName[code]}
				out[code] = sf
			}
			if sf.StockName == "" {
				sf.StockName = latestName[code]
			}
			sf.Legs = append(sf.Legs, leg)
			sf.TotalDeltaShares += delta
			if h.ETFType == TypeActive {
				sf.TotalActiveDeltaShares += delta
			} else {
				sf.TotalPassiveDeltaShares += delta
			}
		}
	}

	// Stable leg order (by ETF code) so output is deterministic for tests/reports.
	for _, sf := range out {
		sort.SliceStable(sf.Legs, func(i, j int) bool {
			return sf.Legs[i].ETFCode < sf.Legs[j].ETFCode
		})
	}
	return out
}

// AggregateSectors sums each stock's active/passive delta into its sector, using
// the caller-supplied code→sector map. Stocks with no sector mapping are skipped.
// Context only; this never influences rotation scoring.
func AggregateSectors(flows map[string]*StockFlow, sectorOf map[string]string) map[string]*SectorFlow {
	out := make(map[string]*SectorFlow)
	for code, sf := range flows {
		sector := sectorOf[code]
		if sector == "" {
			continue
		}
		s := out[sector]
		if s == nil {
			s = &SectorFlow{Sector: sector}
			out[sector] = s
		}
		s.ActiveDeltaShares += sf.TotalActiveDeltaShares
		s.PassiveDeltaShares += sf.TotalPassiveDeltaShares
	}
	return out
}

// PressureRatio = |deltaShares| / avgVolume20. Returns 0 when avgVolume20 <= 0 so a
// missing average volume degrades safely instead of producing Inf/NaN. This is the
// single home of the ratio formula; the caller supplies avg volume at attach time.
func PressureRatio(deltaShares int64, avgVolume20 float64) float64 {
	if avgVolume20 <= 0 {
		return 0
	}
	return math.Abs(float64(deltaShares)) / avgVolume20
}

// ── helpers ───────────────────────────────────────────────────────────────────

// sortedSnapshots returns a copy of snaps ordered ascending by AsOfDate. AsOfDate is
// a zero-padded YYYY-MM-DD string, so lexical order equals chronological order.
func sortedSnapshots(snaps []*Snapshot) []*Snapshot {
	out := make([]*Snapshot, 0, len(snaps))
	for _, s := range snaps {
		if s != nil {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AsOfDate < out[j].AsOfDate })
	return out
}

// sharesSeries returns, for every stock the ETF has ever held, its share count per
// snapshot (aligned to snaps order; missing day = 0), plus the latest-known name of
// each stock for display.
func sharesSeries(snaps []*Snapshot) (series map[string][]int64, latestName map[string]string) {
	n := len(snaps)
	series = make(map[string][]int64)
	latestName = make(map[string]string)

	ensure := func(code string) []int64 {
		s, ok := series[code]
		if !ok {
			s = make([]int64, n)
			series[code] = s
		}
		return s
	}
	for i, snap := range snaps {
		for _, hld := range snap.Holdings {
			s := ensure(hld.StockCode)
			s[i] = hld.HoldingShares
			if hld.StockName != "" {
				latestName[hld.StockCode] = hld.StockName // last write wins → newest snapshot
			}
		}
	}
	return series, latestName
}

// consecutiveDays returns the most-recent run of positive (buy) or negative (sell)
// daily deltas over a stock's share series. At most one of the two is non-zero: the
// streak is anchored to the newest day's direction and breaks on the first day that
// reverses or is flat. A flat newest day yields (0, 0).
func consecutiveDays(shares []int64) (buy, sell int) {
	n := len(shares)
	if n < 2 {
		return 0, 0
	}
	last := shares[n-1] - shares[n-2]
	switch {
	case last > 0:
		for k := n - 1; k >= 1; k-- {
			if shares[k]-shares[k-1] > 0 {
				buy++
			} else {
				break
			}
		}
		return buy, 0
	case last < 0:
		for k := n - 1; k >= 1; k-- {
			if shares[k]-shares[k-1] < 0 {
				sell++
			} else {
				break
			}
		}
		return 0, sell
	default:
		return 0, 0
	}
}

// weightDelta returns the latest day-over-day weight change for a stock (today − prev),
// reading 0 for a snapshot where the stock is absent. 0 when there is only one snapshot.
func weightDelta(snaps []*Snapshot, code string) float64 {
	n := len(snaps)
	if n < 2 {
		return 0
	}
	return weightOf(snaps[n-1], code) - weightOf(snaps[n-2], code)
}

// weightOf returns a stock's holding weight in one snapshot, or 0 if absent.
func weightOf(snap *Snapshot, code string) float64 {
	for _, h := range snap.Holdings {
		if h.StockCode == code {
			return h.HoldingWeight
		}
	}
	return 0
}
