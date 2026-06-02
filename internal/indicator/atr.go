package indicator

import "math"

// ATR computes Average True Range using Wilder's smoothing (period=14 standard).
// Returns same-length slice; entries before index period are 0.
func ATR(highs, lows, closes []float64, period int) []float64 {
	n := len(closes)
	atr := make([]float64, n)
	if n < period+1 || period <= 0 {
		return atr
	}

	tr := func(i int) float64 {
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		return math.Max(hl, math.Max(hc, lc))
	}

	var sum float64
	for i := 1; i <= period; i++ {
		sum += tr(i)
	}
	atr[period] = sum / float64(period)

	for i := period + 1; i < n; i++ {
		atr[i] = (atr[i-1]*float64(period-1) + tr(i)) / float64(period)
	}
	return atr
}
