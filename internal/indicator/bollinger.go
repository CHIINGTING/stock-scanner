package indicator

import "math"

// BollingerResult holds the Bollinger Band time series.
type BollingerResult struct {
	Middle []float64 // SMA(period)
	Upper  []float64 // middle + stdDev * σ
	Lower  []float64 // middle - stdDev * σ
	Width  []float64 // (upper - lower) / middle  — relative band width
}

// Bollinger computes Bollinger Bands over the closing prices.
func Bollinger(closes []float64, period int, stdDevMult float64) BollingerResult {
	n := len(closes)
	middle := SMA(closes, period)
	upper := make([]float64, n)
	lower := make([]float64, n)
	width := make([]float64, n)

	for i := period - 1; i < n; i++ {
		var variance float64
		for j := i - period + 1; j <= i; j++ {
			diff := closes[j] - middle[i]
			variance += diff * diff
		}
		sigma := math.Sqrt(variance / float64(period))
		u := middle[i] + stdDevMult*sigma
		l := middle[i] - stdDevMult*sigma
		upper[i] = u
		lower[i] = l
		if middle[i] > 0 {
			width[i] = (u - l) / middle[i]
		}
	}
	return BollingerResult{Middle: middle, Upper: upper, Lower: lower, Width: width}
}

// BBExpansion returns true if the latest band width expanded significantly vs yesterday.
// ratio = 1.2 means the band is at least 20% wider than yesterday.
func BBExpansion(bb BollingerResult, ratio float64) bool {
	n := len(bb.Width)
	if n < 2 {
		return false
	}
	prev := bb.Width[n-2]
	curr := bb.Width[n-1]
	return prev > 0 && curr >= prev*ratio
}

// BBSqueeze returns true if the band width on the last bar is below squeezePct
// (e.g. 0.05 means width < 5% of price), indicating volatility compression.
func BBSqueeze(bb BollingerResult, squeezePct float64) bool {
	n := len(bb.Width)
	if n == 0 {
		return false
	}
	return bb.Width[n-1] > 0 && bb.Width[n-1] < squeezePct
}

// VolatilityBreakout returns true if:
//  1. The BB width was contracting for squeezeDays
//  2. The last close broke above the upper band (bullish)
func VolatilityBreakout(closes []float64, bb BollingerResult, squeezeDays int, squeezePct float64) bool {
	n := len(bb.Width)
	if n < squeezeDays+1 {
		return false
	}
	// check squeeze: width was below squeezePct for squeezeDays before the last bar
	for i := n - 1 - squeezeDays; i < n-1; i++ {
		if bb.Width[i] >= squeezePct {
			return false
		}
	}
	// last close breaks above upper band
	return closes[n-1] > bb.Upper[n-1] && bb.Upper[n-1] > 0
}
