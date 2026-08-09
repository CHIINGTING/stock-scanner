package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/indicator"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// BreadthAnalyzer — layer 2a of the structural regime engine.
//
// Breadth is measured against the scanner's OWN universe (the ~1,900 symbols already in
// .cache), not against the exchange's advance/decline counts. Two reasons, both practical:
//
//   - It has two years of history from day one, so every threshold in here can be
//     back-tested immediately. The official counts only exist from the day we start
//     fetching them.
//   - "percent of stocks above their own MA20" and "how many rose today" answer different
//     questions. The former is a participation measure that survives a flat session; the
//     latter is a one-day snapshot. The regime table wants participation.
//
// The official counts remain useful as an independent cross-check (BreadthData in the raw
// snapshot), which is why they are recorded but not used here.

// BreadthPanel is the universe's breadth computed for EVERY date on a shared axis.
//
// Whole-axis rather than a single date because Drop20 — the fall from the 20-session peak
// of participation — needs breadth's own recent history. Computing the panel once and
// indexing into it also keeps the as-of discipline honest: date i only ever sees columns
// 0..i of each stock.
type BreadthPanel struct {
	Dates      []string
	AboveMA20  []float64 // percent of valid stocks above their own MA20
	AboveMA60  []float64
	AboveMA120 []float64
	Advancing  []float64 // percent of valid stocks that closed up vs the prior session
	ValidCount []int     // = Base20; the population the sufficiency gate is measured against

	// Each measure's own population. Recorded, and persisted as metrics, because a
	// percentage without its denominator cannot be audited after the fact: 75% of 8 stocks
	// and 75% of 1,900 look identical in a stored snapshot.
	Base20  []int
	Base60  []int
	Base120 []int
	BaseAdv []int
}

// BreadthView is one date's breadth, ready for classification.
type BreadthView struct {
	Date       string
	AboveMA20  float64
	AboveMA60  float64
	AboveMA120 float64
	// Drop20 is how many percentage POINTS below its own 20-session high AboveMA20 sits.
	// It is what separates "cooling" from "narrowing" when the absolute level is still
	// respectable: 58% is healthy on its own, but 58% after 80% is a warning.
	Drop20         float64
	AdvancingRatio float64
	ValidCount     int

	// Populations behind AboveMA60 / AboveMA120, and whether each cleared its own floor.
	// A measure that did not is left OUT of the metrics rather than persisted as a number
	// derived from a handful of names — the same discipline as MissingEvidence, applied at
	// the level of an individual metric.
	Base60, Base120 int
	Above60Known    bool
	Above120Known   bool

	// Sufficient is false when fewer than MinBreadthUniverse stocks could be measured.
	// Breadth is then UNKNOWN — not zero, and not neutral.
	Sufficient bool
}

// BuildBreadthPanel computes breadth across the whole axis.
//
// closes[stock][dateIdx] holds each symbol's closes aligned to dates, with 0 meaning "no
// bar that day" (suspension, or listed later). A stock is counted on a given date only when
// it has enough consecutive history for the moving average in question, so a newly listed
// symbol dilutes nothing.
func BuildBreadthPanel(dates []string, closes [][]float64) *BreadthPanel {
	p := &BreadthPanel{
		Dates:      dates,
		AboveMA20:  make([]float64, len(dates)),
		AboveMA60:  make([]float64, len(dates)),
		AboveMA120: make([]float64, len(dates)),
		Advancing:  make([]float64, len(dates)),
		ValidCount: make([]int, len(dates)),
		Base20:     make([]int, len(dates)),
		Base60:     make([]int, len(dates)),
		Base120:    make([]int, len(dates)),
		BaseAdv:    make([]int, len(dates)),
	}
	if len(dates) == 0 {
		return p
	}

	// Per-stock moving averages over the whole axis, computed once.
	type maSet struct{ ma20, ma60, ma120 []float64 }
	sets := make([]maSet, len(closes))
	for i, series := range closes {
		sets[i] = maSet{
			ma20:  gappedSMA(series, 20),
			ma60:  gappedSMA(series, 60),
			ma120: gappedSMA(series, 120),
		}
	}

	for d := range dates {
		// EACH measure carries its own denominator. A stock listed 60 sessions ago has an
		// MA20 but no MA120; counting it in the MA120 denominator would put it permanently
		// in the "not above" bucket and understate long-term participation — a bias that
		// would be invisible today (no rule reads AboveMA120) but would quietly corrupt the
		// stored metrics that later validation runs depend on.
		var base20, base60, base120, advBase int
		var up20, up60, up120, adv int

		for i, series := range closes {
			if d >= len(series) || series[d] <= 0 {
				continue
			}
			c := series[d]
			if sets[i].ma20[d] > 0 {
				base20++
				if c > sets[i].ma20[d] {
					up20++
				}
			}
			if sets[i].ma60[d] > 0 {
				base60++
				if c > sets[i].ma60[d] {
					up60++
				}
			}
			if sets[i].ma120[d] > 0 {
				base120++
				if c > sets[i].ma120[d] {
					up120++
				}
			}
			if d > 0 && series[d-1] > 0 {
				advBase++
				if c > series[d-1] {
					adv++
				}
			}
		}
		// ValidCount is the MA20 population: it is what the sufficiency gate and the
		// headline AboveMA20 are measured against.
		p.ValidCount[d] = base20
		p.Base20[d], p.Base60[d], p.Base120[d], p.BaseAdv[d] = base20, base60, base120, advBase
		p.AboveMA20[d] = ratio(up20, base20)
		p.AboveMA60[d] = ratio(up60, base60)
		p.AboveMA120[d] = ratio(up120, base120)
		p.Advancing[d] = ratio(adv, advBase)
	}
	return p
}

// ratio is a percentage that stays 0 when nothing was measurable, rather than dividing by
// zero or inventing a value.
func ratio(n, base int) float64 {
	if base <= 0 {
		return 0
	}
	return float64(n) / float64(base) * 100
}

// AnalyzeBreadth turns one date of the panel into Evidence plus the view the classifier
// needs. nearHigh comes from the price side and only affects the NARROWING test.
func AnalyzeBreadth(p *BreadthPanel, at int, nearHigh bool, th model.StructureThresholds) (model.Evidence, BreadthView) {
	th = th.Defaulted()

	var v BreadthView
	if p == nil || at < 0 || at >= len(p.Dates) {
		return model.MissingEvidence(model.EvidenceBreadth, "breadth panel 無此日期"), v
	}
	v.Date = p.Dates[at]
	v.ValidCount = p.ValidCount[at]

	if v.ValidCount < th.MinBreadthUniverse {
		return model.MissingEvidence(model.EvidenceBreadth,
			fmt.Sprintf("可計算家數 %d，未達 %d 門檻", v.ValidCount, th.MinBreadthUniverse)), v
	}
	v.Sufficient = true
	v.AboveMA20 = p.AboveMA20[at]
	v.AboveMA60 = p.AboveMA60[at]
	v.AboveMA120 = p.AboveMA120[at]
	v.AdvancingRatio = p.Advancing[at]
	v.Drop20 = drop20(p.AboveMA20, at)
	v.Base60, v.Base120 = p.Base60[at], p.Base120[at]
	v.Above60Known = v.Base60 >= th.MinBreadthUniverse
	v.Above120Known = v.Base120 >= th.MinBreadthUniverse

	quality := ClassifyBreadth(v, nearHigh, th)
	dir, strength := breadthDirection(quality)

	return model.Evidence{
		Source:     model.EvidenceBreadth,
		Direction:  dir,
		Strength:   strength,
		Confidence: breadthConfidence(v, th),
		Summary: fmt.Sprintf("%s：站上 MA20 %.1f%%（近 20 日高點 -%.1fpp）、MA60 %.1f%%，%d 檔可計算",
			breadthLabel(quality), v.AboveMA20, v.Drop20, v.AboveMA60, v.ValidCount),
		Metrics:   breadthMetrics(v),
		Available: true,
	}, v
}

// drop20 is the fall in percentage points from the highest AboveMA20 of the trailing 20
// sessions (including today). Positive means participation has receded from its peak.
func drop20(series []float64, at int) float64 {
	start := at - 19
	if start < 0 {
		start = 0
	}
	peak := 0.0
	for _, x := range series[start : at+1] {
		if x > peak {
			peak = x
		}
	}
	d := peak - series[at]
	if d < 0 {
		d = 0
	}
	return d
}

func breadthDirection(q model.BreadthQuality) (model.Direction, model.Strength) {
	switch q {
	case model.BreadthHealthy:
		return model.DirBullish, model.StrengthStrong
	case model.BreadthCooling:
		return model.DirNeutral, model.StrengthWeak
	case model.BreadthNarrowing:
		return model.DirBearish, model.StrengthModerate
	case model.BreadthCollapsing:
		return model.DirBearish, model.StrengthExtreme
	}
	return model.DirUnknown, model.StrengthUnknown
}

// breadthConfidence falls as the measurable universe thins: a breadth number from 520
// stocks deserves less weight than the same number from 1,900.
func breadthConfidence(v BreadthView, th model.StructureThresholds) float64 {
	c := 1.0
	if v.ValidCount < th.MinBreadthUniverse*2 {
		c -= 0.2
	}
	if v.ValidCount < th.MinBreadthUniverse*3/2 {
		c -= 0.1
	}
	if c < 0 {
		c = 0
	}
	return round2(c)
}

func breadthLabel(q model.BreadthQuality) string {
	switch q {
	case model.BreadthHealthy:
		return "廣度健康"
	case model.BreadthCooling:
		return "廣度降溫"
	case model.BreadthNarrowing:
		return "廣度收斂"
	case model.BreadthCollapsing:
		return "廣度潰散"
	}
	return "廣度不可測"
}

// gappedSMA is an n-period SMA that refuses to average across missing bars.
//
// indicator.SMA assumes a dense series; a suspended stock has 0s in the middle, and feeding
// those straight in would silently drag its average toward zero and make it look like it
// had collapsed below its own MA. Here a window containing any gap yields 0 ("not
// measurable that day") and the stock simply is not counted.
func gappedSMA(series []float64, n int) []float64 {
	out := make([]float64, len(series))
	if len(series) < n {
		return out
	}
	dense := make([]float64, len(series))
	copy(dense, series)
	ma := indicator.SMA(dense, n)
	for i := n - 1; i < len(series); i++ {
		ok := true
		for k := i - n + 1; k <= i; k++ {
			if series[k] <= 0 {
				ok = false
				break
			}
		}
		if ok && i < len(ma) {
			out[i] = ma[i]
		}
	}
	return out
}
