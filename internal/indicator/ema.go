package indicator

// EMA computes exponential moving averages of length period over src, seeded with the SMA
// of the first `period` values.
//
// Returns a same-length slice; elements before index period-1 are 0, matching the warm-up
// convention SMA and ATR already use in this package. Callers must treat that leading zero
// as "not computed" rather than as a value — every reader in this repo already does.
//
// The SMA seed (rather than starting from src[0]) is the standard construction: it makes the
// first published value an honest average of a full window instead of a number dominated by
// one arbitrary starting price, and it is what MACD and Keltner definitions assume.
func EMA(src []float64, period int) []float64 {
	out := make([]float64, len(src))
	if period <= 0 || len(src) < period {
		return out
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += src[i]
	}
	out[period-1] = sum / float64(period)

	k := 2.0 / float64(period+1)
	for i := period; i < len(src); i++ {
		out[i] = (src[i]-out[i-1])*k + out[i-1]
	}
	return out
}
