package indicator

// KDJResult holds the K, D, J time series.
type KDJResult struct {
	K []float64
	D []float64
	J []float64
}

// KDJ computes the KDJ (stochastic oscillator variant) widely used in Taiwan/China markets.
//
//	RSV(n) = (Close - LowestLow(kPeriod)) / (HighestHigh(kPeriod) - LowestLow(kPeriod)) * 100
//	K(n)   = (dSmooth-1)/dSmooth * K(n-1) + (1/dSmooth) * RSV(n)   [initial K = 50]
//	D(n)   = (dSmooth-1)/dSmooth * D(n-1) + (1/dSmooth) * K(n)      [initial D = 50]
//	J(n)   = 3*K(n) - 2*D(n)
func KDJ(highs, lows, closes []float64, kPeriod, dSmooth, jSmooth int) KDJResult {
	n := len(closes)
	K := make([]float64, n)
	D := make([]float64, n)
	J := make([]float64, n)

	kWeight := 1.0 / float64(dSmooth)
	dWeight := 1.0 / float64(jSmooth)

	prevK, prevD := 50.0, 50.0
	for i := 0; i < n; i++ {
		start := i - kPeriod + 1
		if start < 0 {
			start = 0
		}
		lo, hi := lows[start], highs[start]
		for j := start + 1; j <= i; j++ {
			if lows[j] < lo {
				lo = lows[j]
			}
			if highs[j] > hi {
				hi = highs[j]
			}
		}

		var rsv float64
		if hi == lo {
			rsv = 50
		} else {
			rsv = (closes[i] - lo) / (hi - lo) * 100
		}

		k := (1-kWeight)*prevK + kWeight*rsv
		d := (1-dWeight)*prevD + dWeight*k
		j := 3*k - 2*d

		K[i] = k
		D[i] = d
		J[i] = j
		prevK = k
		prevD = d
	}
	return KDJResult{K: K, D: D, J: J}
}

// KDJGoldenCross returns true if K crossed above D on the last bar (K[n-2] < D[n-2] && K[n-1] >= D[n-1]).
func KDJGoldenCross(res KDJResult) bool {
	n := len(res.K)
	if n < 2 {
		return false
	}
	return res.K[n-2] < res.D[n-2] && res.K[n-1] >= res.D[n-1]
}
