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
