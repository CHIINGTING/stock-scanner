package indicator

// SMA computes simple moving averages of length period over src.
// Returns same-length slice; elements before index period-1 are 0.
func SMA(src []float64, period int) []float64 {
	out := make([]float64, len(src))
	if period <= 0 || len(src) < period {
		return out
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += src[i]
	}
	out[period-1] = sum / float64(period)
	for i := period; i < len(src); i++ {
		sum += src[i] - src[i-period]
		out[i] = sum / float64(period)
	}
	return out
}

// MASlopePerDay returns the slope of a moving average itself, in PERCENT PER TRADING DAY:
//
//	((ma[t] / ma[t-lookback]) - 1) / lookback * 100
//
// Percent rather than absolute difference is the whole point: ma[t] - ma[t-5] is measured in
// dollars, so a NT$500 stock and a NT$20 stock cannot be compared. Dividing by the lookback
// normalises windows against each other, so an MA20 slope over 5 days and an MA60 slope over
// 10 days are the same unit and can sit in one table.
//
// ok=false when there is not enough history or either endpoint is non-positive — a missing
// slope must never arrive as a confident 0, which would read as "flat".
func MASlopePerDay(ma []float64, lookback int) (float64, bool) {
	n := len(ma)
	if lookback <= 0 || n < lookback+1 {
		return 0, false
	}
	cur, prev := ma[n-1], ma[n-1-lookback]
	if cur <= 0 || prev <= 0 {
		return 0, false
	}
	return (cur/prev - 1) / float64(lookback) * 100, true
}

// MA20ConsecutiveRising returns how many consecutive days MA20 has been rising (0 if flat/falling).
func MA20ConsecutiveRising(ma20 []float64) int {
	n := len(ma20)
	count := 0
	for i := n - 1; i > 0; i-- {
		if ma20[i] > ma20[i-1] {
			count++
		} else {
			break
		}
	}
	return count
}

// MA20ConsecutiveFalling returns how many consecutive days MA20 has been falling (0 if flat/rising).
func MA20ConsecutiveFalling(ma20 []float64) int {
	n := len(ma20)
	count := 0
	for i := n - 1; i > 0; i-- {
		if ma20[i] < ma20[i-1] {
			count++
		} else {
			break
		}
	}
	return count
}

// MA20Uptrend returns true if MA20 has been rising for at least confirmDays consecutive days.
func MA20Uptrend(ma20 []float64, confirmDays int) bool {
	return MA20ConsecutiveRising(ma20) >= confirmDays
}

// MA20TrendLabel returns a visual label for the current MA20 trend direction.
func MA20TrendLabel(ma20 []float64) string {
	r := MA20ConsecutiveRising(ma20)
	f := MA20ConsecutiveFalling(ma20)
	switch {
	case r >= 3:
		return "↑↑↑"
	case r == 2:
		return "↑↑"
	case r == 1:
		return "↑"
	case f >= 3:
		return "↓↓↓"
	case f == 2:
		return "↓↓"
	case f == 1:
		return "↓"
	default:
		return "→"
	}
}
