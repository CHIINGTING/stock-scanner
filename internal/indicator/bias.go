package indicator

import "sort"

// BIAS returns the latest 乖離率 (bias) for the given period:
//
//	BIAS_N = (Close - SMA_N) / SMA_N * 100
//
// It is a pure derivation of SMA (reused, single source of truth) and the latest close.
// ok=false when there is insufficient history (len(closes) < period) or SMA_N == 0, so a
// missing/degenerate value is never a misleading 0 or NaN (SPEC §13). BIAS is risk/context
// — NOT a buy/sell signal (see internal/scanner/bias_attach.go for the risk labeling).
func BIAS(closes []float64, period int) (float64, bool) {
	if period <= 0 || len(closes) < period {
		return 0, false
	}
	ma := SMA(closes, period)
	m := ma[len(ma)-1]
	if m == 0 {
		return 0, false
	}
	c := closes[len(closes)-1]
	return (c - m) / m * 100, true
}

// BIASSeries returns the BIAS value at EVERY bar where the moving average exists, using the
// same formula as BIAS. Bars before the MA is defined are omitted rather than zero-filled, so
// the caller never mistakes "not computable yet" for "price sat exactly on the MA".
//
// It exists so BIAS can be ranked against a stock's OWN history. A fixed universal threshold
// ("above +10% is extended") treats a 60%-volatility small cap and a utility the same way;
// a percentile asks the only question that transfers across stocks — how unusual is this for
// THIS stock. SMA is reused, so there is exactly one BIAS formula in the codebase.
func BIASSeries(closes []float64, period int) []float64 {
	if period <= 0 || len(closes) < period {
		return nil
	}
	ma := SMA(closes, period)
	out := make([]float64, 0, len(closes)-period+1)
	for i := period - 1; i < len(closes); i++ {
		if ma[i] == 0 {
			continue
		}
		out = append(out, (closes[i]-ma[i])/ma[i]*100)
	}
	return out
}

// PercentileRank returns what percentage of `sample` is <= v, in [0,100].
//
// minSample guards against a "percentile" computed from a handful of observations, which
// looks authoritative and means nothing. ok=false when the sample is too thin — the caller
// must then fall back or report the value as unavailable, never treat it as mid-range.
func PercentileRank(sample []float64, v float64, minSample int) (float64, bool) {
	if minSample < 1 {
		minSample = 1
	}
	if len(sample) < minSample {
		return 0, false
	}
	sorted := append([]float64(nil), sample...)
	sort.Float64s(sorted)
	// Count of elements <= v; sort.SearchFloat64s finds the first index >= v, so walk past
	// any run of exact ties to include them (ties should not read as "below average").
	i := sort.SearchFloat64s(sorted, v)
	for i < len(sorted) && sorted[i] <= v {
		i++
	}
	return float64(i) / float64(len(sorted)) * 100, true
}

// BIASPercentile ranks the LATEST BIAS against the stock's own trailing history.
//
// window is how many recent BIAS observations to rank against (252 ≈ one trading year).
// ok=false when the history is too short — an unavailable percentile stays unavailable.
func BIASPercentile(closes []float64, period, window, minSample int) (float64, bool) {
	series := BIASSeries(closes, period)
	if len(series) == 0 {
		return 0, false
	}
	latest := series[len(series)-1]
	if window > 0 && len(series) > window {
		series = series[len(series)-window:]
	}
	return PercentileRank(series, latest, minSample)
}
