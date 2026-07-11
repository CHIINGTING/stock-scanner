package validator

import "time"

// Benchmark computes excess return versus a reference instrument (default 0050,
// the Yuanta Taiwan-50 ETF, which the cache already carries). If the benchmark
// series is unavailable the evaluator transparently falls back to absolute-return
// rules, so benchmarking stays optional.
type Benchmark struct {
	Code   string
	series *PriceSeries
}

// NewBenchmark loads the benchmark series from the price store. A blank or
// index-style code (TWSE/TPEX) that is not present in the cache yields a disabled
// benchmark (Enabled() == false) rather than an error.
func NewBenchmark(store *PriceStore, code string) *Benchmark {
	b := &Benchmark{Code: code}
	if code == "" {
		return b
	}
	b.series = store.Series(code)
	return b
}

// Enabled reports whether excess-return calculation is available.
func (b *Benchmark) Enabled() bool { return b != nil && b.series != nil }

// Returns gives the benchmark's own forward returns for the same signal date and
// horizons, so the caller can subtract them from a stock's returns.
func (b *Benchmark) Returns(signalDate time.Time, horizons []int) map[int]float64 {
	if !b.Enabled() {
		return nil
	}
	out, ok := b.series.Outcome(signalDate, horizons)
	if !ok {
		return nil
	}
	return out.Returns
}
