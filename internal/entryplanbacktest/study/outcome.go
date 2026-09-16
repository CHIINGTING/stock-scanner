package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Horizons are the forward return horizons, in sessions after the fill session.
// Fixed in Phase 1; see entryplanbacktest.HorizonCloseIndex.
var Horizons = []int{5, 10, 20}

// Outcome is everything measured after a fill.
type Outcome struct {
	// Coverage says how much of the 20-session window the series holds. TRUNCATED is PENDING,
	// not a loss and not an exclusion.
	Coverage entryplanbacktest.WindowCoverage `json:"coverage"`

	// Returns are percent, keyed by horizon, PRESENT ONLY when the series reaches them. A
	// missing horizon is missing, never 0.
	Returns map[int]float64 `json:"returns,omitempty"`

	// MFE20 / MAE20 are percent excursions relative to the fill price over [F+1, F+20],
	// UNCLIPPED (see entryplanbacktest.ExcursionWindow: clipping at 0 would report a
	// favourable excursion that never happened). Nil when the window held no usable bar.
	MFE20 *float64 `json:"mfe_20,omitempty"`
	MAE20 *float64 `json:"mae_20,omitempty"`
	// ExcursionBars is how many usable bars the excursion was measured over — the honest
	// denominator for a TRUNCATED window.
	ExcursionBars int `json:"excursion_bars"`

	// InvalidationHit / Target1Hit / Target2Hit are "the level was touched at some point in
	// [F+1, F+20]". They are INDEPENDENT of each other and of ordering: both a stop and a
	// target can be touched in one window and both flags are then true. Ordering is FirstTouch's
	// question, and it is a separate field because the two are separate facts.
	//
	// Each is a POINTER: nil means the plan published no such level, so the observation is not
	// in that rate's DENOMINATOR at all. A false here would claim "the level existed and was
	// not reached", which is a different sentence.
	InvalidationHit *bool `json:"invalidation_hit,omitempty"`
	Target1Hit      *bool `json:"target_1_hit,omitempty"`
	Target2Hit      *bool `json:"target_2_hit,omitempty"`

	// FirstTouch is which of the invalidation and Target1 was reached FIRST, session by
	// session. AMBIGUOUS_STOP_TARGET_ORDER when the first session that touched either touched
	// BOTH — daily OHLC cannot order them, and this package refuses to.
	FirstTouch entryplanbacktest.TouchClass `json:"first_touch,omitempty"`
	// FirstTouchBar is the bar FirstTouch happened on, -1 when neither was touched.
	FirstTouchBar int `json:"first_touch_bar"`
}

// Measure computes the outcome of one fill.
//
// bars is the symbol's FULL series (not truncated at T): the forward window is exactly the
// part of it the plan could not see, and cutting it off would make every outcome PENDING.
// Every read is at an index strictly greater than the fill bar, except the fill price itself,
// which the caller already has.
func Measure(bars []fetcher.Candle, fill Fill, invalidation, target1, target2 *float64) Outcome {
	out := Outcome{Coverage: entryplanbacktest.WindowUnavailable, FirstTouchBar: -1}
	if !fill.Filled() {
		return out
	}
	n := len(bars)
	lo, hi, cov := entryplanbacktest.ExcursionWindow(fill.BarIndex, n)
	out.Coverage = cov
	if cov == entryplanbacktest.WindowUnavailable {
		return out
	}

	// Returns, per horizon, omitted when the series does not reach them.
	out.Returns = map[int]float64{}
	for _, h := range Horizons {
		i, ok := entryplanbacktest.HorizonCloseIndex(fill.BarIndex, h, n)
		if !ok || !(bars[i].Close > 0) {
			continue
		}
		out.Returns[h] = (bars[i].Close/fill.Price - 1) * 100
	}

	// Excursions, over the usable bars of [F+1, F+20].
	var mfe, mae float64
	var seen int
	for i := lo; i <= hi; i++ {
		b := bars[i]
		if !(b.High > 0) || !(b.Low > 0) || b.High < b.Low {
			continue // unusable bar: skipped and counted out, never read as 0
		}
		up := (b.High/fill.Price - 1) * 100
		dn := (b.Low/fill.Price - 1) * 100
		if seen == 0 || up > mfe {
			mfe = up
		}
		if seen == 0 || dn < mae {
			mae = dn
		}
		seen++
	}
	out.ExcursionBars = seen
	if seen > 0 {
		m, a := mfe, mae
		out.MFE20, out.MAE20 = &m, &a
	}

	// Level hits, each only when the plan published the level.
	if invalidation != nil {
		out.InvalidationHit = boolPtr(touchedBelow(bars, lo, hi, *invalidation))
	}
	if target1 != nil {
		out.Target1Hit = boolPtr(touchedAbove(bars, lo, hi, *target1))
	}
	if target2 != nil {
		out.Target2Hit = boolPtr(touchedAbove(bars, lo, hi, *target2))
	}

	// Ordering, but only where both levels exist: "which came first" is not a question about
	// a plan that published one of them.
	if invalidation != nil && target1 != nil {
		for i := lo; i <= hi; i++ {
			c := entryplanbacktest.ClassifyTouch(bars[i], *invalidation, *target1)
			if c == entryplanbacktest.TouchNone {
				continue
			}
			out.FirstTouch, out.FirstTouchBar = c, i
			break
		}
		if out.FirstTouch == "" {
			out.FirstTouch = entryplanbacktest.TouchNone
		}
	}
	return out
}

func touchedBelow(bars []fetcher.Candle, lo, hi int, level float64) bool {
	for i := lo; i <= hi; i++ {
		if bars[i].Low > 0 && bars[i].Low <= level {
			return true
		}
	}
	return false
}

func touchedAbove(bars []fetcher.Candle, lo, hi int, level float64) bool {
	for i := lo; i <= hi; i++ {
		if bars[i].High > 0 && bars[i].High >= level {
			return true
		}
	}
	return false
}

func boolPtr(b bool) *bool { return &b }
