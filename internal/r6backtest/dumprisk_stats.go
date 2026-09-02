package r6backtest

import (
	"fmt"
	"math"
	"sort"
)

// ──────────────────────────────────────────────────────────────────────────────
// Buckets and their statistics.
//
// Every bucket is read against DUMP_BASELINE_ALL_BARS. Absolute numbers here carry the
// package-wide survivorship bias — the universe comes from .cache, which contains only
// symbols still listed — so a bucket's own mean return says little and its DIFFERENCE from
// the baseline says what there is to say.
// ──────────────────────────────────────────────────────────────────────────────

// DumpCondition is one named subset of the observations.
type DumpCondition struct {
	Name   string
	Note   string
	Detect func(o DumpObs, th DumpThresholds) bool
}

// DumpConditions builds the comparison grid.
//
// The grid is deliberately a FACTORIAL over the three OHLCV dimensions (gain × volume ×
// close strength) rather than a list of interesting-looking combinations. A factorial makes
// the marginal contribution of each dimension readable: if weak-close rows differ from
// strong-close rows at BOTH volume levels, close strength carries information; if the
// difference only appears in one cell, it is more likely noise than a mechanism.
func DumpConditions() []DumpCondition {
	weak := func(o DumpObs, th DumpThresholds) bool {
		return !math.IsNaN(th.WeakCloseCLV) && o.Feat.CloseLocationValue <= th.WeakCloseCLV
	}
	strong := func(o DumpObs, th DumpThresholds) bool {
		return !math.IsNaN(th.StrongCloseCLV) && o.Feat.CloseLocationValue >= th.StrongCloseCLV
	}
	longUpper := func(o DumpObs, th DumpThresholds) bool {
		return !math.IsNaN(th.LongUpperShadowPct) && o.Feat.UpperShadowPct >= th.LongUpperShadowPct
	}
	shortUpper := func(o DumpObs, th DumpThresholds) bool {
		return !math.IsNaN(th.LongUpperShadowPct) && o.Feat.UpperShadowPct < th.LongUpperShadowPct
	}
	largeGain := func(o DumpObs, th DumpThresholds) bool {
		return o.Feat.ChangeOK && o.Feat.PriceChangePct >= th.LargeGainPct
	}
	spike := func(o DumpObs, th DumpThresholds) bool {
		return o.Feat.VolumeOK && o.Feat.VolumeRatio >= th.VolSpikeRatio
	}
	normalVol := func(o DumpObs, th DumpThresholds) bool {
		return o.Feat.VolumeOK && o.Feat.VolumeRatio < th.VolSpikeRatio
	}
	highDT := func(o DumpObs, th DumpThresholds) bool {
		return th.DayTradingAvailable && o.Feat.DayTradingOK &&
			o.Feat.DayTradingRatio >= th.HighDayTradingRatio
	}
	lowDT := func(o DumpObs, th DumpThresholds) bool {
		return th.DayTradingAvailable && o.Feat.DayTradingOK &&
			o.Feat.DayTradingRatio < th.HighDayTradingRatio
	}
	notLimit := func(o DumpObs, _ DumpThresholds) bool { return !o.Feat.NearLimitUp }
	and := func(fs ...func(DumpObs, DumpThresholds) bool) func(DumpObs, DumpThresholds) bool {
		return func(o DumpObs, th DumpThresholds) bool {
			for _, f := range fs {
				if !f(o, th) {
					return false
				}
			}
			return true
		}
	}

	return []DumpCondition{
		{"DUMP_BASELINE_ALL_BARS", "every valid bar — the control", func(DumpObs, DumpThresholds) bool { return true }},

		// ── Single dimensions, to see what each is worth alone ───────────────
		{"DUMP_LARGE_GAIN", "gain >= LargeGainPct, any volume", largeGain},
		{"DUMP_VOLUME_SPIKE", "volume >= VolSpikeRatio, any move", spike},
		{"DUMP_WEAK_CLOSE", "CLV in the bottom tercile, any move", weak},
		{"DUMP_LONG_UPPER_SHADOW", "upper shadow in the top tercile, any move", longUpper},

		// ── The 2x2 the question is really about ─────────────────────────────
		{"DUMP_GAIN_SPIKE_STRONG_CLOSE", "large gain + volume spike + closed near the high", and(largeGain, spike, strong)},
		{"DUMP_GAIN_SPIKE_WEAK_CLOSE", "large gain + volume spike + closed near the low", and(largeGain, spike, weak)},
		{"DUMP_GAIN_NORMALVOL_STRONG_CLOSE", "large gain + ordinary volume + closed near the high", and(largeGain, normalVol, strong)},
		{"DUMP_GAIN_NORMALVOL_WEAK_CLOSE", "large gain + ordinary volume + closed near the low", and(largeGain, normalVol, weak)},

		// ── Upper-shadow interaction, holding gain and volume fixed ──────────
		{"DUMP_GAIN_SPIKE_LONG_UPPER", "large gain + volume spike + long upper shadow", and(largeGain, spike, longUpper)},
		{"DUMP_GAIN_SPIKE_SHORT_UPPER", "large gain + volume spike + short upper shadow", and(largeGain, spike, shortUpper)},

		// ── The limit-up cases, held OUT of the ordinary large-gain rows ─────
		{"DUMP_NEAR_LIMIT_UP", "closed at or above +9.5% — a queue, not a free move",
			func(o DumpObs, _ DumpThresholds) bool { return o.Feat.NearLimitUp }},
		{"DUMP_GAIN_SPIKE_EXCL_LIMIT", "large gain + volume spike, limit-up days removed",
			and(largeGain, spike, func(o DumpObs, _ DumpThresholds) bool { return !o.Feat.NearLimitUp })},

		// ── Day-trading interaction (rows stay empty when the archive is absent) ──
		{"DUMP_GAIN_SPIKE_HIGH_DAYTRADE", "large gain + volume spike + day-trading in the top tercile", and(largeGain, spike, highDT)},
		{"DUMP_GAIN_SPIKE_LOW_DAYTRADE", "large gain + volume spike + day-trading below the top tercile", and(largeGain, spike, lowDT)},
		{"DUMP_HIGH_DAYTRADE_ANY", "day-trading in the top tercile, any move", highDT},

		// ── The 2x2 again with limit-up days REMOVED ────────────────────────
		// The trigger population's upper CLV tercile lands at exactly 1.0 — more than a third
		// of large-gain, high-volume sessions close precisely on the high, and a limit-up
		// close is one of them by construction. So "strong close" and "limit-up" are heavily
		// confounded, and the strong-close rows must be re-read with limit-ups removed before
		// close strength can be credited with anything.
		{"DUMP_GAIN_SPIKE_STRONG_CLOSE_EXCL_LIMIT", "large gain + spike + strong close, limit-up days removed",
			and(largeGain, spike, strong, notLimit)},
		{"DUMP_GAIN_SPIKE_WEAK_CLOSE_EXCL_LIMIT", "large gain + spike + weak close, limit-up days removed",
			and(largeGain, spike, weak, notLimit)},
		{"DUMP_GAIN_SPIKE_LONG_UPPER_EXCL_LIMIT", "large gain + spike + long upper shadow, limit-up days removed",
			and(largeGain, spike, longUpper, notLimit)},
		{"DUMP_GAIN_SPIKE_SHORT_UPPER_EXCL_LIMIT", "large gain + spike + short upper shadow, limit-up days removed",
			and(largeGain, spike, shortUpper, notLimit)},

		// ── The worst-case stack the hypothesis predicts ─────────────────────
		{"DUMP_GAIN_SPIKE_WEAK_LONGUPPER", "large gain + spike + weak close + long upper shadow", and(largeGain, spike, weak, longUpper)},
	}
}

// DumpStat is one bucket's summary. Counts come first because they decide how much of the
// rest may be believed.
type DumpStat struct {
	Name string
	Note string

	Count        int
	UniqueDates  int
	UniqueStocks int
	// MaxDateShare is the largest fraction of the bucket contributed by any single trading
	// date. A bucket that is 40% one date is one event wearing a sample size.
	MaxDateShare float64

	T1OpenMean, T1OpenMedian   float64
	T1CloseMean, T1CloseMedian float64
	T1LowMean, T1LowMedian     float64
	T2CloseMean, T2CloseMedian float64

	GapDownRate    float64 // T+1 open < signal close
	NegCloseRate   float64 // T+1 close < signal close
	SelloffRate    float64 // T+1 MAE reached the baseline's SelloffPctile tail
	T1MAEATRMean   float64
	T1MAEATRMedian float64
	T2MAEATRMean   float64
	T2MAEATRMedian float64
	// ── Confounder profile: the bucket's own composition ────────────────────
	// Published for EVERY bucket so a downside edge can be checked against the possibility
	// that the bucket simply selected cheaper, thinner or more volatile names.
	MedPrice     float64 // median signal close, NTD
	MedAvgVol20  float64 // median trailing 20-session volume, 股
	MedATRPct    float64 // median ATR(14) as % of close
	MedTurnover  float64 // median close x volume, NTD
	LimitUpShare float64 // % of the bucket that closed at or above +9.5%

	ATRCoverage      float64 // fraction of rows with a usable ATR
	T2Coverage       float64 // fraction of rows with a T+2 bar
	DayTradeCoverage float64 // fraction of rows with 當沖 data
}

// ComputeDumpStats aggregates one bucket.
func ComputeDumpStats(name, note string, obs []DumpObs, th DumpThresholds) DumpStat {
	st := DumpStat{Name: name, Note: note, Count: len(obs)}
	if len(obs) == 0 {
		return st
	}

	dates := map[string]int{}
	stocks := map[string]struct{}{}
	var t1o, t1c, t1l, t2c, mae1, mae2 []float64
	var prices, vols, atrPcts, turnovers []float64
	var gapDown, negClose, selloff, atrOK, t2OK, dtOK, limitUp int

	for _, o := range obs {
		dates[o.Date.Format("2006-01-02")]++
		stocks[o.Symbol] = struct{}{}

		if !math.IsNaN(o.T1OpenRet) {
			t1o = append(t1o, o.T1OpenRet)
			if o.T1OpenRet < 0 {
				gapDown++
			}
		}
		if !math.IsNaN(o.T1CloseRet) {
			t1c = append(t1c, o.T1CloseRet)
			if o.T1CloseRet < 0 {
				negClose++
			}
		}
		if !math.IsNaN(o.T1LowRet) {
			t1l = append(t1l, o.T1LowRet)
		}
		if o.HasT2 && !math.IsNaN(o.T2CloseRet) {
			t2c = append(t2c, o.T2CloseRet)
			t2OK++
		}
		if o.ATROK && !math.IsNaN(o.T1MAEATR) {
			atrOK++
			mae1 = append(mae1, o.T1MAEATR)
			// The selloff label is defined on the BASELINE distribution, so the same bar is
			// "a selloff" regardless of which bucket it lands in.
			if !math.IsNaN(th.SelloffMAEATR) && o.T1MAEATR <= th.SelloffMAEATR {
				selloff++
			}
			if o.HasT2 && !math.IsNaN(o.T2MAEATR) {
				mae2 = append(mae2, o.T2MAEATR)
			}
		}
		if o.Feat.DayTradingOK {
			dtOK++
		}
		if o.Feat.NearLimitUp {
			limitUp++
		}
		if o.Price > 0 {
			prices = append(prices, o.Price)
		}
		if o.AvgVol20 > 0 {
			vols = append(vols, o.AvgVol20)
		}
		if !math.IsNaN(o.ATRPct) {
			atrPcts = append(atrPcts, o.ATRPct)
		}
		if o.TurnoverNTD > 0 {
			turnovers = append(turnovers, o.TurnoverNTD)
		}
	}

	st.UniqueDates = len(dates)
	st.UniqueStocks = len(stocks)
	maxDate := 0
	for _, c := range dates {
		if c > maxDate {
			maxDate = c
		}
	}
	st.MaxDateShare = float64(maxDate) / float64(len(obs))

	st.T1OpenMean, st.T1OpenMedian = mean(t1o), median(t1o)
	st.T1CloseMean, st.T1CloseMedian = mean(t1c), median(t1c)
	st.T1LowMean, st.T1LowMedian = mean(t1l), median(t1l)
	st.T2CloseMean, st.T2CloseMedian = mean(t2c), median(t2c)
	st.T1MAEATRMean, st.T1MAEATRMedian = mean(mae1), median(mae1)
	st.T2MAEATRMean, st.T2MAEATRMedian = mean(mae2), median(mae2)

	st.MedPrice = median(prices)
	st.MedAvgVol20 = median(vols)
	st.MedATRPct = median(atrPcts)
	st.MedTurnover = median(turnovers)
	st.LimitUpShare = rate(limitUp, len(obs))

	st.GapDownRate = rate(gapDown, len(t1o))
	st.NegCloseRate = rate(negClose, len(t1c))
	st.SelloffRate = rate(selloff, atrOK)
	st.ATRCoverage = rate(atrOK, len(obs))
	st.T2Coverage = rate(t2OK, len(obs))
	st.DayTradeCoverage = rate(dtOK, len(obs))

	return st
}

func rate(hit, total int) float64 {
	if total == 0 {
		return math.NaN()
	}
	return float64(hit) / float64(total) * 100
}

// RunDumpStudy assigns every observation to every matching bucket and returns the stats in
// grid order. Buckets deliberately OVERLAP (a bar can be both DUMP_LARGE_GAIN and
// DUMP_GAIN_SPIKE_WEAK_CLOSE); the mutually-exclusive claim applies only within a partition,
// which the 2x2 rows and the day-trading pair satisfy and a test asserts.
func RunDumpStudy(obs []DumpObs, th DumpThresholds) []DumpStat {
	conds := DumpConditions()
	out := make([]DumpStat, 0, len(conds))
	for _, c := range conds {
		var bucket []DumpObs
		for _, o := range obs {
			if c.Detect(o, th) {
				bucket = append(bucket, o)
			}
		}
		out = append(out, ComputeDumpStats(c.Name, c.Note, bucket, th))
	}
	return out
}

// DumpRegimeSplit groups observations by a caller-supplied regime label for the signal date,
// so the same grid can be read per market environment. Dates with no label land in "UNKNOWN"
// rather than being dropped, so the parts still sum to the whole.
func DumpRegimeSplit(obs []DumpObs, regimeOf func(date string) string) map[string][]DumpObs {
	out := map[string][]DumpObs{}
	for _, o := range obs {
		label := "UNKNOWN"
		if regimeOf != nil {
			if r := regimeOf(o.Date.Format("2006-01-02")); r != "" {
				label = r
			}
		}
		out[label] = append(out[label], o)
	}
	return out
}

// DumpQuantileReport describes one feature's observed distribution, so the chosen cut points
// can be checked against the shape they came from instead of being taken on trust.
type DumpQuantileReport struct {
	Feature string
	N       int
	P10     float64
	P25     float64
	P33     float64
	P50     float64
	P67     float64
	P75     float64
	P90     float64
}

func quantiles(name string, xs []float64) DumpQuantileReport {
	return DumpQuantileReport{
		Feature: name, N: len(xs),
		P10: pctlOf(xs, 10), P25: pctlOf(xs, 25), P33: pctlOf(xs, 33.3),
		P50: pctlOf(xs, 50), P67: pctlOf(xs, 66.7), P75: pctlOf(xs, 75), P90: pctlOf(xs, 90),
	}
}

// DumpDistributions reports the feature distributions for the baseline and, separately, for
// the trigger population the buckets split.
func DumpDistributions(obs []DumpObs, th DumpThresholds) []DumpQuantileReport {
	var allCLV, allUpper, allChg, allVol, allMAE []float64
	var trigCLV, trigUpper, trigDT []float64

	for _, o := range obs {
		allCLV = append(allCLV, o.Feat.CloseLocationValue)
		allUpper = append(allUpper, o.Feat.UpperShadowPct)
		if o.Feat.ChangeOK {
			allChg = append(allChg, o.Feat.PriceChangePct)
		}
		if o.Feat.VolumeOK {
			allVol = append(allVol, o.Feat.VolumeRatio)
		}
		if o.ATROK && !math.IsNaN(o.T1MAEATR) {
			allMAE = append(allMAE, o.T1MAEATR)
		}
		if isTrigger(o, th.LargeGainPct, th.VolSpikeRatio) {
			trigCLV = append(trigCLV, o.Feat.CloseLocationValue)
			trigUpper = append(trigUpper, o.Feat.UpperShadowPct)
			if o.Feat.DayTradingOK {
				trigDT = append(trigDT, o.Feat.DayTradingRatio)
			}
		}
	}

	reports := []DumpQuantileReport{
		quantiles("all/close_location_value", allCLV),
		quantiles("all/upper_shadow_pct", allUpper),
		quantiles("all/price_change_pct", allChg),
		quantiles("all/volume_ratio", allVol),
		quantiles("all/t1_mae_atr", allMAE),
		quantiles("trigger/close_location_value", trigCLV),
		quantiles("trigger/upper_shadow_pct", trigUpper),
	}
	if len(trigDT) > 0 {
		reports = append(reports, quantiles("trigger/day_trading_ratio", trigDT))
	}
	return reports
}

// DumpObsDateIndex returns the sorted unique signal dates present in obs — used to report
// coverage and to size the independence caveat.
func DumpObsDateIndex(obs []DumpObs) []string {
	seen := map[string]struct{}{}
	for _, o := range obs {
		seen[o.Date.Format("2006-01-02")] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// String renders a stat compactly for the terminal.
func (s DumpStat) String() string {
	return fmt.Sprintf("%-34s n=%-7d dates=%-4d stocks=%-5d T1open=%+.2f%% T1close=%+.2f%% gap=%.1f%% neg=%.1f%% MAE=%.2fATR",
		s.Name, s.Count, s.UniqueDates, s.UniqueStocks,
		s.T1OpenMean, s.T1CloseMean, s.GapDownRate, s.NegCloseRate, s.T1MAEATRMean)
}
