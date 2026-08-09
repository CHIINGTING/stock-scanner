package indicator

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
