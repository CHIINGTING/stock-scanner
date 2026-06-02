package indicator

import "math"

// RSI computes the Relative Strength Index using Wilder's smoothed method (period=14 standard).
// Returns a slice of same length as closes; entries before index period are 0.
func RSI(closes []float64, period int) []float64 {
	n := len(closes)
	rsi := make([]float64, n)
	if n < period+1 || period <= 0 {
		return rsi
	}

	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	wilder := func(ag, al float64) float64 {
		if al == 0 {
			return 100
		}
		return 100 - 100/(1+ag/al)
	}
	rsi[period] = wilder(avgGain, avgLoss)

	for i := period + 1; i < n; i++ {
		d := closes[i] - closes[i-1]
		gain := math.Max(d, 0)
		loss := math.Max(-d, 0)
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		rsi[i] = wilder(avgGain, avgLoss)
	}
	return rsi
}
