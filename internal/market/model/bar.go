package model

// Bar is one session of a price series, oldest-first when in a slice.
//
// Deliberately close-only. Every rule in the structural decision table is expressed on
// closing prices (moving averages, 20/60-day returns, drawdown from the 60-day CLOSING
// high), so carrying OHLC here would be four fields nothing reads. The one place intraday
// range would matter is the ATR fallback for HighVolatility, and that flag is computed
// from a realized-volatility percentile instead — see StructureThresholds.VolPercentile.
type Bar struct {
	Date  string  `json:"date"` // YYYY-MM-DD
	Close float64 `json:"close"`
}

// Closes extracts the close series in order. Returns a fresh slice so callers cannot
// mutate the input bars through it.
func Closes(bars []Bar) []float64 {
	out := make([]float64, len(bars))
	for i, b := range bars {
		out[i] = b.Close
	}
	return out
}
