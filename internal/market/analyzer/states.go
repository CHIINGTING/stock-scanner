// Package analyzer holds the PURE scoring functions of the Market Dashboard: each module's
// raw data → a 0..100 ModuleScore, the weighted aggregate, and the regime classifier. No
// I/O, no network, no globals — everything is a deterministic function of its inputs, so it
// is fully table-testable.
//
// Threshold policy: the two spec-mandated tables (Foreign-Futures Trend/Extreme and the
// regime score cutoffs) live in model.MarketConfig. The softer per-module score buckets
// below are documented BASELINE constants — uncalibrated, tuned later against real data.
package analyzer

// Foreign-Futures Trend labels (by daily change).
const (
	FuturesAggressiveShort = "Aggressive Short"
	FuturesMoreShort       = "More Short"
	FuturesStable          = "Stable"
	FuturesCoverShort      = "Cover Short"
	FuturesAggressiveCover = "Aggressive Cover"
)

// Foreign-Futures Extreme Indicator labels (by net position). Extreme Short is deliberately
// NOT treated as directly bearish — it may be hedging — so it surfaces as a caveat.
const (
	ExtremeNeutral      = "Neutral"
	ExtremeLightShort   = "Light Short"
	ExtremeShort        = "Short"
	ExtremeHeavyShort   = "Heavy Short"
	ExtremeExtremeShort = "Extreme Short"
)

// Foreign-Cash labels (by net buy/sell, 億 TWD).
const (
	CashStrongBuy  = "Strong Buy"
	CashBuy        = "Buy"
	CashNeutral    = "Neutral"
	CashSell       = "Sell"
	CashStrongSell = "Strong Sell"
)

// Margin labels (retail chasing behaviour).
const (
	MarginBullish = "Bullish"
	MarginNeutral = "Neutral"
	MarginBearish = "Bearish"
)

// Currency labels.
const (
	CurrencyRiskOn  = "Risk On"
	CurrencyNeutral = "Neutral"
	CurrencyRiskOff = "Risk Off"
)

// VIX labels.
const (
	VIXNormal      = "Normal"
	VIXElevated    = "Elevated"
	VIXFear        = "Fear"
	VIXExtremeFear = "Extreme Fear"
)

// Options (put/call ratio) labels.
const (
	OptionsLowPC      = "Low P/C"
	OptionsNeutralPC  = "Neutral P/C"
	OptionsElevatedPC = "Elevated P/C"
	OptionsHighPC     = "High P/C"
)

// clamp bounds v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
