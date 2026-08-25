package r6backtest

import (
	"math"
	"math/rand"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// ──────────────────────────────────────────────────────────────────────────────
// USD/TWD × foreign flow — a MARKET-level study, counted at market scale.
//
// Everything else in this package measures per-stock setups, and its statistics are per
// trade. That shape is wrong here and dangerously so: USD/TWD, foreign flow and the index
// are ONE observation per day, shared by every stock in the universe. Running them through
// the per-stock engine would report ~1,900 "samples" for a single day's currency move and
// make a coin-flip look overwhelming — 500 days of data dressed up as 750,000.
//
// So this file does not use the trade engine. The unit of observation is the TRADING DATE.
// Stock-level counts are still reported, but as context for how much the naive framing would
// have inflated N, never as the sample size.
//
// # Publication times drive an asymmetry
//
//	FX bar D       finishes 23:59 London = 06:59 Taipei on D+1 → NOT known at the D close
//	BFI82U for D   published ~15:00 Taipei on D                → known when the scanner runs
//
// A scanner running after the close on D therefore has foreign flow for D but only FX up to
// D-1. The study uses exactly that, so an effect it finds is one the scanner could have
// acted on. TestFXStudyIsCausal holds the FX half.
// ──────────────────────────────────────────────────────────────────────────────

// FXDay is one trading date's market-level state, as known to a scanner running that evening.
type FXDay struct {
	Date string

	// FX, as of this Taiwan session — derived from bars strictly before Date.
	FXOK       bool
	FXContext  string
	FXChange1  float64
	FXChange5  float64
	FXChange20 float64
	FXZ        float64

	// Broad dollar, same causal contract as the pair above.
	DXYOK       bool
	DXYContext  string
	DXYChange20 float64

	// The TWD-specific part of the move, after the broad dollar is taken out. Fitted only on
	// data strictly before this date, with the explained point excluded from its own fit.
	ResidualOK      bool
	Residual        float64
	ResidualZ       float64
	ResidualContext string

	// Foreign net for this date, in NTD, and its size measured in the market's own typical
	// day. Both false when the archive has no entry — a holiday or a gap, never a zero.
	ForeignOK  bool
	ForeignNet float64
	ForeignZ   float64

	// Forward market returns, in percent, measured as the MEDIAN across the universe: what a
	// randomly chosen stock did. The scanner picks individual stocks, so the median stock is
	// the honest counterfactual; an index would answer a question about weights instead.
	Fwd map[int]float64
	// Breadth is how many stocks contributed to Fwd, so a thin date cannot masquerade as a
	// full one.
	Breadth int

	// TrailingMarket is the median stock's return over the PRECEDING window — a crude
	// regime proxy, built from the universe itself because the market dashboard has one
	// archived snapshot and cannot supply a regime series. Causal: bars up to and including
	// this date only. TrailingOK is false during warm-up.
	TrailingMarket float64
	TrailingOK     bool
}

// FXPanel is the aligned per-date study input.
type FXPanel struct {
	Days []FXDay
	// StockObservations is what the per-stock framing WOULD have counted. Reported so the
	// difference between it and len(Days) is visible rather than implicit.
	StockObservations int
}

// fxForeignZWindow is the trailing sample used to scale foreign flow into "typical days".
// Same idea as the FX z-score: the absolute NTD figure means nothing without knowing what a
// normal day looks like, and what is normal drifts.
const fxForeignZWindow = 60

// fxTrailingWindow is the regime proxy's lookback, in trading days.
const fxTrailingWindow = 20

// BuildFXPanel aligns the currency archive, the foreign-flow archive and the universe onto
// the universe's trading dates.
//
// horizons are the forward windows to measure. A date whose forward window runs off the end
// of the data simply has no entry for that horizon — it is never padded.
func BuildFXPanel(u *Universe, series, dxySeries *fx.Series, flow *provider.ForeignFlowSeries,
	cfg fx.Config, horizons []int) *FXPanel {

	cfg = cfg.Defaulted()
	panel := &FXPanel{}
	if u == nil || len(u.Axis) == 0 {
		return panel
	}

	flowByDate := map[string]float64{}
	var flowDates []string
	if flow != nil {
		for _, p := range flow.Points {
			flowByDate[p.Date] = p.ForeignNet
			flowDates = append(flowDates, p.Date)
		}
		sort.Strings(flowDates)
	}
	flowIdx := map[string]int{}
	for i, d := range flowDates {
		flowIdx[d] = i
	}

	for di, date := range u.Axis {
		day := FXDay{Date: date, Fwd: map[int]float64{}}

		if series != nil {
			m := series.MetricsAsOf(date, cfg)
			if m.Status.OK() {
				day.FXOK = true
				day.FXContext = m.Context
				day.FXChange1, _ = m.Change(1)
				day.FXChange5, _ = m.Change(5)
				day.FXChange20, _ = m.Change(20)
				if m.ContextZ != nil {
					day.FXZ = *m.ContextZ
				}
			}
		}

		if dxySeries != nil {
			dm := dxySeries.MetricsAsOf(date, cfg)
			if dm.Status.OK() {
				day.DXYOK = true
				day.DXYContext = dm.Context
				day.DXYChange20, _ = dm.Change(20)
			}
			if dec := fx.DecomposeAsOf(series, dxySeries, date, cfg, 0); dec.Status.OK() {
				day.ResidualOK = true
				day.Residual = dec.Residual
				day.ResidualZ = dec.ResidualZ
				day.ResidualContext = fx.ResidualContext(dec.ResidualZ, cfg)
			}
		}

		if net, ok := flowByDate[date]; ok {
			day.ForeignOK = true
			day.ForeignNet = net
			// Scale against the PRECEDING window only — including today would let the day
			// being classified move its own yardstick.
			if i, ok := flowIdx[date]; ok && i >= fxForeignZWindow {
				sample := make([]float64, 0, fxForeignZWindow)
				for k := i - fxForeignZWindow; k < i; k++ {
					sample = append(sample, flowByDate[flowDates[k]])
				}
				if sd := stdevOf(sample); sd > 0 {
					day.ForeignZ = net / sd
				}
			}
		}

		day.Fwd, day.Breadth = marketForward(u, di, horizons)
		day.TrailingMarket, day.TrailingOK = marketTrailing(u, di, fxTrailingWindow)
		panel.Days = append(panel.Days, day)
		panel.StockObservations += day.Breadth
	}
	return panel
}

// marketForward is the median forward return across the universe for each horizon.
func marketForward(u *Universe, dateIdx int, horizons []int) (map[int]float64, int) {
	out := map[int]float64{}
	breadth := 0
	per := map[int][]float64{}

	date := u.Axis[dateIdx]
	for _, s := range u.Stocks {
		i, ok := s.idxOf[date]
		if !ok || i >= len(s.Close) || s.Close[i] <= 0 {
			continue
		}
		breadth++
		for _, h := range horizons {
			j := i + h
			if j >= len(s.Close) || s.Close[j] <= 0 {
				continue
			}
			r := (s.Close[j]/s.Close[i] - 1) * 100
			if !math.IsNaN(r) && !math.IsInf(r, 0) {
				per[h] = append(per[h], r)
			}
		}
	}
	for h, vs := range per {
		if len(vs) > 0 {
			out[h] = medianOf(vs)
		}
	}
	return out, breadth
}

// marketTrailing is the median stock's return over the preceding n bars, ending at dateIdx.
// Strictly backward-looking, so stratifying on it cannot import knowledge of the future.
func marketTrailing(u *Universe, dateIdx, n int) (float64, bool) {
	if dateIdx < n {
		return 0, false
	}
	date, prev := u.Axis[dateIdx], u.Axis[dateIdx-n]
	var rs []float64
	for _, s := range u.Stocks {
		i, ok1 := s.idxOf[date]
		j, ok2 := s.idxOf[prev]
		if !ok1 || !ok2 || i >= len(s.Close) || j >= len(s.Close) || s.Close[j] <= 0 {
			continue
		}
		r := (s.Close[i]/s.Close[j] - 1) * 100
		if !math.IsNaN(r) && !math.IsInf(r, 0) {
			rs = append(rs, r)
		}
	}
	if len(rs) == 0 {
		return 0, false
	}
	return medianOf(rs), true
}

// ── conditions ────────────────────────────────────────────────────────────────────────

// FXCondition is one market-level state to study.
type FXCondition struct {
	Name string
	// Detect reads one day. It returns false when the day cannot be classified, so an
	// unmeasurable day is EXCLUDED rather than silently counted as "condition absent".
	Detect func(d FXDay) bool
}

// Foreign-flow strength band, in typical days. Same shape as the FX bands and equally
// unvalidated.
const fxForeignStrongZ = 1.0

// FXConditions is the study grid.
//
// It is built to answer four separate questions, in this order: does FX alone carry anything;
// does foreign flow alone; do they carry more together; and do the two DISAGREEING carry
// something different from either. Without the first two the interaction rows would have
// nothing to be compared against, and a combination that merely inherits one leg's effect
// would look like a discovery.
func FXConditions() []FXCondition {
	fxOK := func(d FXDay) bool { return d.FXOK }
	fgOK := func(d FXDay) bool { return d.ForeignOK }

	appreciating := func(d FXDay) bool {
		return fxOK(d) && (d.FXContext == fx.ContextAppreciation || d.FXContext == fx.ContextStrongAppreciation)
	}
	depreciating := func(d FXDay) bool {
		return fxOK(d) && (d.FXContext == fx.ContextDepreciation || d.FXContext == fx.ContextStrongDepreciation)
	}
	foreignBuy := func(d FXDay) bool { return fgOK(d) && d.ForeignNet > 0 }
	foreignSell := func(d FXDay) bool { return fgOK(d) && d.ForeignNet < 0 }

	return []FXCondition{
		// The control. Every row below is read as a difference from this.
		{"FX_BASELINE_ALL_DATES", func(d FXDay) bool { return true }},

		// ── FX alone ────────────────────────────────────────────────────────────
		{"FX_TWD_STRONG_APPRECIATION", func(d FXDay) bool { return fxOK(d) && d.FXContext == fx.ContextStrongAppreciation }},
		{"FX_TWD_APPRECIATION", appreciating},
		{"FX_TWD_STABLE", func(d FXDay) bool { return fxOK(d) && d.FXContext == fx.ContextStable }},
		{"FX_TWD_DEPRECIATION", depreciating},
		{"FX_TWD_STRONG_DEPRECIATION", func(d FXDay) bool { return fxOK(d) && d.FXContext == fx.ContextStrongDepreciation }},

		// ── foreign flow alone ──────────────────────────────────────────────────
		{"FGN_BUY", foreignBuy},
		{"FGN_SELL", foreignSell},
		{"FGN_STRONG_BUY", func(d FXDay) bool { return fgOK(d) && d.ForeignZ >= fxForeignStrongZ }},
		{"FGN_STRONG_SELL", func(d FXDay) bool { return fgOK(d) && d.ForeignZ <= -fxForeignStrongZ }},

		// ── the four interactions ───────────────────────────────────────────────
		{"FX_APPR_X_FGN_BUY", func(d FXDay) bool { return appreciating(d) && foreignBuy(d) }},
		{"FX_APPR_X_FGN_SELL", func(d FXDay) bool { return appreciating(d) && foreignSell(d) }},
		{"FX_DEPR_X_FGN_BUY", func(d FXDay) bool { return depreciating(d) && foreignBuy(d) }},
		{"FX_DEPR_X_FGN_SELL", func(d FXDay) bool { return depreciating(d) && foreignSell(d) }},

		// ── the broad dollar, and what it leaves unexplained ────────────────────
		// These exist to answer the question USD/TWD alone cannot: was that a TWD move or a
		// dollar move? If DXY_UP carries what USDTWD_DEPR carries, the pair was telling us
		// about the dollar and Taiwan was incidental.
		{"DXY_USD_APPRECIATION", func(d FXDay) bool {
			return d.DXYOK && (d.DXYContext == fx.ContextUSDAppreciation ||
				d.DXYContext == fx.ContextUSDStrongAppreciation)
		}},
		{"DXY_USD_DEPRECIATION", func(d FXDay) bool {
			return d.DXYOK && (d.DXYContext == fx.ContextUSDDepreciation ||
				d.DXYContext == fx.ContextUSDStrongDepreciation)
		}},
		{"RESIDUAL_TWD_WEAK", func(d FXDay) bool {
			return d.ResidualOK && d.ResidualContext == fx.ResidualTWDWeak
		}},
		{"RESIDUAL_TWD_STRONG", func(d FXDay) bool {
			return d.ResidualOK && d.ResidualContext == fx.ResidualTWDStrong
		}},

		// The three-way comparison the whole M4 question rests on: the same foreign-sell
		// condition, paired with the pair, with the dollar, and with the residual.
		{"DXY_UP_X_FGN_SELL", func(d FXDay) bool {
			return d.DXYOK && foreignSell(d) &&
				(d.DXYContext == fx.ContextUSDAppreciation || d.DXYContext == fx.ContextUSDStrongAppreciation)
		}},
		{"RESIDUAL_TWD_WEAK_X_FGN_SELL", func(d FXDay) bool {
			return d.ResidualOK && foreignSell(d) && d.ResidualContext == fx.ResidualTWDWeak
		}},

		// ── confirmation vs divergence ──────────────────────────────────────────
		// Confirmation = the currency and the flow tell the same capital story: money coming
		// in strengthens TWD and buys stock, money leaving does the reverse.
		{"FX_FGN_CONFIRMATION", func(d FXDay) bool {
			return (appreciating(d) && foreignBuy(d)) || (depreciating(d) && foreignSell(d))
		}},
		// Divergence is NOT bad data. A dollar move that has nothing to do with Taiwan, a
		// hedged position, an allocation shift — all produce it, and whether it carries
		// information is precisely what has not been established.
		{"FX_FGN_DIVERGENCE", func(d FXDay) bool {
			return (appreciating(d) && foreignSell(d)) || (depreciating(d) && foreignBuy(d))
		}},
	}
}

// ── date-level statistics ─────────────────────────────────────────────────────────────

// FXStat is one condition's result.
//
// The headline numbers are computed on NON-OVERLAPPING observations. That is the whole point
// of this struct: a 20-day forward window measured on consecutive days is the same 20 days
// counted twenty times, and reporting 77 such rows as "n = 77" claims twenty times more
// evidence than exists. Raw* fields are kept beside them so the gap between the two framings
// is visible rather than hidden by whichever one was chosen.
type FXStat struct {
	Name string
	// RawDates is every date the condition selected. NOT the sample size.
	RawDates int
	// StockObservations is what a per-stock framing would have claimed instead.
	StockObservations int

	// EffectiveN is the count of non-overlapping observations, per horizon: selecting a date
	// consumes the next `horizon` trading days, so a longer horizon has fewer of them.
	EffectiveN map[int]int

	// Canonical statistics, on the non-overlapping sample.
	WinRate map[int]float64
	Mean    map[int]float64
	Median  map[int]float64

	// 95% bootstrap intervals for the canonical win rate and median.
	WinRateLo map[int]float64
	WinRateHi map[int]float64
	MedianLo  map[int]float64
	MedianHi  map[int]float64

	// Raw (overlapping) win rate, for comparison only.
	RawWinRate map[int]float64
}

// bootstrapDraws is fixed and the seed is fixed, so a reported interval is reproducible.
// A confidence interval that moves between runs is not a confidence interval.
const bootstrapDraws = 2000

// nonOverlapping selects observations whose forward windows do not intersect.
//
// Greedy from the earliest date: take one, then skip every date inside its forward window.
// Greedy is not the largest possible independent set, but it is deterministic, order-
// preserving and obvious — and an interval built on a subtly-chosen subset would be worth
// less than one built on a slightly smaller obvious subset.
func nonOverlapping(dateIdx []int, horizon int) []int {
	var out []int
	last := -1 << 30
	for _, i := range dateIdx {
		if i-last < horizon {
			continue
		}
		out = append(out, i)
		last = i
	}
	return out
}

// RunFXStudy evaluates every condition over the panel.
func RunFXStudy(panel *FXPanel, conds []FXCondition, horizons []int) []FXStat {
	// Position of each day, so overlap can be measured in trading days rather than in
	// calendar days (a weekend is not a gap in a forward window).
	idxOf := make(map[string]int, len(panel.Days))
	for i, d := range panel.Days {
		idxOf[d.Date] = i
	}

	var out []FXStat
	for _, c := range conds {
		st := FXStat{
			Name: c.Name, EffectiveN: map[int]int{},
			WinRate: map[int]float64{}, Mean: map[int]float64{}, Median: map[int]float64{},
			WinRateLo: map[int]float64{}, WinRateHi: map[int]float64{},
			MedianLo: map[int]float64{}, MedianHi: map[int]float64{},
			RawWinRate: map[int]float64{},
		}
		var selected []int
		for i, d := range panel.Days {
			if !c.Detect(d) {
				continue
			}
			st.RawDates++
			st.StockObservations += d.Breadth
			selected = append(selected, i)
		}

		for _, h := range horizons {
			// Raw, overlapping — reference only.
			var raw []float64
			for _, i := range selected {
				if v, ok := panel.Days[i].Fwd[h]; ok {
					raw = append(raw, v)
				}
			}
			if len(raw) > 0 {
				st.RawWinRate[h] = winRateOf(raw)
			}

			// Canonical: non-overlapping.
			var vals []float64
			for _, i := range nonOverlapping(selected, h) {
				if v, ok := panel.Days[i].Fwd[h]; ok {
					vals = append(vals, v)
				}
			}
			st.EffectiveN[h] = len(vals)
			if len(vals) == 0 {
				continue
			}
			st.WinRate[h] = winRateOf(vals)
			st.Median[h] = medianOf(vals)
			var sum float64
			for _, v := range vals {
				sum += v
			}
			st.Mean[h] = sum / float64(len(vals))

			// The observations are non-overlapping, so their forward windows share no days
			// and a plain resample is legitimate. This is what a block bootstrap is FOR —
			// the blocking has been done by construction, where it can be seen, instead of
			// inside a resampling routine where it cannot.
			wLo, wHi, mLo, mHi := bootstrapCI(vals, h)
			st.WinRateLo[h], st.WinRateHi[h] = wLo, wHi
			st.MedianLo[h], st.MedianHi[h] = mLo, mHi
		}
		out = append(out, st)
	}
	return out
}

func winRateOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	wins := 0
	for _, x := range v {
		if x > 0 {
			wins++
		}
	}
	return float64(wins) / float64(len(v)) * 100
}

// bootstrapCI returns 95% percentile intervals for the win rate and the median.
//
// The seed is derived from the sample and the horizon, so the same input always yields the
// same interval — a confidence interval that changes between runs cannot be checked by
// anyone, including its author.
func bootstrapCI(vals []float64, horizon int) (winLo, winHi, medLo, medHi float64) {
	n := len(vals)
	if n < 3 {
		// Below this an interval would be arithmetic theatre: it would be reported to two
		// decimal places and mean nothing.
		return math.NaN(), math.NaN(), math.NaN(), math.NaN()
	}
	var seed uint64 = uint64(horizon)*1000003 + uint64(n)
	for _, v := range vals {
		seed = seed*1099511628211 ^ math.Float64bits(v)
	}
	rng := rand.New(rand.NewSource(int64(seed)))

	wins := make([]float64, bootstrapDraws)
	meds := make([]float64, bootstrapDraws)
	sample := make([]float64, n)
	for b := 0; b < bootstrapDraws; b++ {
		for i := 0; i < n; i++ {
			sample[i] = vals[rng.Intn(n)]
		}
		wins[b] = winRateOf(sample)
		meds[b] = medianOf(sample)
	}
	sort.Float64s(wins)
	sort.Float64s(meds)
	lo, hi := int(0.025*bootstrapDraws), int(0.975*bootstrapDraws)-1
	return wins[lo], wins[hi], meds[lo], meds[hi]
}

func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func stdevOf(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := x - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)))
}

// ── regime stratification ─────────────────────────────────────────────────────────────

// FXStratum is one condition measured inside one market-trend regime.
//
// A market-level effect that only exists in one regime is not a rule, it is a description of
// that regime — and with a single 26-month sample containing one of each, that is the most
// likely thing to find. Splitting is how the study can say so instead of reporting an
// average that belongs to neither half.
type FXStratum struct {
	Name   string
	Regime string
	// Dates is the raw count; EffectiveN is the sample the statistics rest on. Stratifying
	// halves an already-thin sample, so the second number is the one that decides whether a
	// regime split says anything at all.
	Dates      int
	EffectiveN map[int]int
	WinRate    map[int]float64
	Median     map[int]float64
}

// FXRegimeLabels are the two halves of the crude trend proxy.
const (
	RegimeMarketUp   = "TRAILING_UP"
	RegimeMarketDown = "TRAILING_DOWN"
)

// RunFXStrata repeats a study inside each trailing-trend half.
func RunFXStrata(panel *FXPanel, conds []FXCondition, horizons []int) []FXStratum {
	var out []FXStratum
	for _, regime := range []string{RegimeMarketUp, RegimeMarketDown} {
		sub := &FXPanel{}
		for _, d := range panel.Days {
			if !d.TrailingOK {
				continue
			}
			up := d.TrailingMarket > 0
			if (regime == RegimeMarketUp) != up {
				continue
			}
			sub.Days = append(sub.Days, d)
		}
		for _, st := range RunFXStudy(sub, conds, horizons) {
			out = append(out, FXStratum{
				Name: st.Name, Regime: regime, Dates: st.RawDates,
				EffectiveN: st.EffectiveN, WinRate: st.WinRate, Median: st.Median,
			})
		}
	}
	return out
}

// FXCoverage reports how much of the study window each archive actually covered, and whether
// the covered subset is a fair sample of FX conditions.
//
// This exists because the foreign-flow archive has systematic holes: whole months are absent,
// and an interaction measured only where BOTH archives happen to overlap is measured on a
// subsample that was not chosen at random. Comparing the FX-context mix inside and outside
// the covered dates is the cheapest available check on whether that matters.
type FXCoverage struct {
	TotalDates     int
	FXDates        int
	ForeignDates   int
	BothDates      int
	ContextWithin  map[string]int // FX context distribution on dates WITH foreign data
	ContextOutside map[string]int // …and on dates without
}

// Coverage summarises the panel's completeness.
func Coverage(panel *FXPanel) FXCoverage {
	c := FXCoverage{
		TotalDates:     len(panel.Days),
		ContextWithin:  map[string]int{},
		ContextOutside: map[string]int{},
	}
	for _, d := range panel.Days {
		if d.FXOK {
			c.FXDates++
		}
		if d.ForeignOK {
			c.ForeignDates++
		}
		if d.FXOK && d.ForeignOK {
			c.BothDates++
		}
		if !d.FXOK {
			continue
		}
		if d.ForeignOK {
			c.ContextWithin[d.FXContext]++
		} else {
			c.ContextOutside[d.FXContext]++
		}
	}
	return c
}

// ── incremental value ─────────────────────────────────────────────────────────────────

// Increment is one interaction measured against the leg it is built on.
//
// An interaction row being positive proves nothing: "TWD depreciation AND foreign selling"
// contains "foreign selling", so it inherits whatever that leg carries. The only question
// worth asking is whether ADDING the currency condition changed anything, and that is a
// comparison against the leg, not against the unconditional baseline.
type Increment struct {
	Interaction string
	Leg         string
	Horizon     int

	LegWin       float64
	LegEffN      int
	InteractWin  float64
	InteractEffN int
	// Delta is the interaction's win rate minus the leg's — the incremental effect.
	Delta float64
	// Overlap is how much of the leg the interaction kept. A condition that retains almost
	// all of its leg has not really added a condition.
	RetainedPct float64
}

// Increments compares each interaction with the foreign-flow leg it is built on.
func Increments(stats []FXStat, horizon int) []Increment {
	by := map[string]FXStat{}
	for _, s := range stats {
		by[s.Name] = s
	}
	pairs := []struct{ interaction, leg string }{
		{"FX_DEPR_X_FGN_SELL", "FGN_SELL"},
		{"FX_APPR_X_FGN_BUY", "FGN_BUY"},
		{"DXY_UP_X_FGN_SELL", "FGN_SELL"},
		{"RESIDUAL_TWD_WEAK_X_FGN_SELL", "FGN_SELL"},
		{"FX_FGN_CONFIRMATION", "FGN_SELL"},
	}
	var out []Increment
	for _, p := range pairs {
		i, ok1 := by[p.interaction]
		l, ok2 := by[p.leg]
		if !ok1 || !ok2 || i.EffectiveN[horizon] == 0 || l.EffectiveN[horizon] == 0 {
			continue
		}
		inc := Increment{
			Interaction: p.interaction, Leg: p.leg, Horizon: horizon,
			LegWin: l.WinRate[horizon], LegEffN: l.EffectiveN[horizon],
			InteractWin: i.WinRate[horizon], InteractEffN: i.EffectiveN[horizon],
			Delta: i.WinRate[horizon] - l.WinRate[horizon],
		}
		if l.RawDates > 0 {
			inc.RetainedPct = float64(i.RawDates) / float64(l.RawDates) * 100
		}
		out = append(out, inc)
	}
	return out
}
