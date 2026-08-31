package r6backtest

import "sort"

// ──────────────────────────────────────────────────────────────────────────────
// Regime labels for the dump-risk grid.
//
// R9's five-way regime layer was superseded by R10 and its market dashboard is still on a
// mock provider (docs/PROJECT_FEATURE_STATUS.md §2 row 13), so there is no persisted
// historical regime series to join against. Rather than invent one, this labels each trading
// date from data the universe already carries: the SAME breadth measure the crash-regime
// panel uses (breadthBelowMA20). The labels are therefore breadth terciles, not R9's regime
// names, and they are named to say so — a reader must not mistake DUMP_REGIME_WEAK for a
// validated "BEAR" classification.
//
// Causal by construction: the breadth of day T is computed from closes and MA20s on day T,
// both known at that close.
// ──────────────────────────────────────────────────────────────────────────────

// Breadth-tercile regime labels.
const (
	RegimeBreadthStrong = "BREADTH_STRONG" // fewest stocks below their MA20
	RegimeBreadthMid    = "BREADTH_MID"
	RegimeBreadthWeak   = "BREADTH_WEAK" // most stocks below their MA20
)

// BreadthRegimeLabeller precomputes a breadth reading for every date on the universe axis and
// returns a lookup that maps a date key to its tercile label.
//
// The terciles are cut on THIS period's own breadth distribution, so the labels mean
// "relatively strong/weak for this sample" — not an absolute market state. Two years of a
// mostly-rising tape will still produce a WEAK tercile, and that is the point: it is the
// least-strong third of the sample, which is the most this cache can support.
func BreadthRegimeLabeller(u *Universe) func(dateKey string) string {
	if u == nil || len(u.Axis) == 0 {
		return func(string) string { return "" }
	}

	breadth := make(map[string]float64, len(u.Axis))
	vals := make([]float64, 0, len(u.Axis))
	for _, d := range u.Axis {
		b := breadthBelowMA20(u, d)
		breadth[d] = b
		vals = append(vals, b)
	}
	sort.Float64s(vals)

	// breadthBelowMA20 is the % BELOW MA20, so a HIGH value is a WEAK market.
	lo := pctlOf(vals, 33.3)
	hi := pctlOf(vals, 66.7)

	return func(dateKey string) string {
		b, ok := breadth[dateKey]
		if !ok {
			return ""
		}
		switch {
		case b <= lo:
			return RegimeBreadthStrong
		case b >= hi:
			return RegimeBreadthWeak
		default:
			return RegimeBreadthMid
		}
	}
}
