package technical

import "github.com/deep-huang/stock-scanner/internal/indicator"

// MACDResult is the moving-average convergence/divergence reading.
//
// Two things here are easy to get wrong and are deliberately not:
//
//   - Cross reports a CROSSING, not a relationship. "MACD is above Signal" is a state that
//     can persist for months; marking every one of those bars GOLDEN would make the field
//     useless and would quietly turn a rare event into a permanent flag.
//   - Momentum reads the HISTOGRAM's trajectory, so a trend that is still positive but
//     losing steam reports DECELERATING even while MACD stays above Signal. That is the
//     information the level alone cannot give.
type MACDResult struct {
	Status Status `json:"status"`

	Fast   int `json:"fast"`
	Slow   int `json:"slow"`
	Signal int `json:"signal_period"`

	MACD      float64 `json:"macd"`
	SignalVal float64 `json:"signal"`
	Histogram float64 `json:"histogram"`

	Cross    string `json:"cross"`    // CrossGolden | CrossDeath | CrossNone
	Momentum string `json:"momentum"` // MomentumAccelerating | Decelerating | Neutral
}

// Cross vocabulary.
const (
	CrossGolden = "GOLDEN"
	CrossDeath  = "DEATH"
	CrossNone   = "NONE"
)

// ComputeMACD builds the MACD line, its signal and the histogram.
//
// The signal EMA is seeded on the MACD series ONLY from where that series becomes valid.
// Running it over the whole slice would fold the warm-up zeros into the seed and drag the
// first real signal values toward zero — a subtle error that makes early crossings fire in
// the wrong place.
func ComputeMACD(bars []Bar, cfg Config) MACDResult {
	cfg = cfg.Defaulted()
	out := MACDResult{
		Fast: cfg.MACDFast, Slow: cfg.MACDSlow, Signal: cfg.MACDSignal,
		Cross: CrossNone, Momentum: MomentumUnknown,
	}

	s, err := newSeries(bars)
	if err != nil {
		out.Status = statusFor(err)
		return out
	}
	// The histogram trajectory needs three consecutive published values, and the crossing
	// test needs the previous bar. The first valid histogram sits at slow+signal-2, so the
	// series must reach slow+signal to have that plus two more.
	need := cfg.MACDSlow + cfg.MACDSignal + 1
	if s.n < need {
		out.Status = StatusInsufficientData
		return out
	}

	fast := indicator.EMA(s.closes, cfg.MACDFast)
	slow := indicator.EMA(s.closes, cfg.MACDSlow)

	// MACD is defined only where the SLOW EMA is (the fast one is always ready sooner).
	start := cfg.MACDSlow - 1
	line := make([]float64, s.n-start)
	for i := range line {
		line[i] = fast[start+i] - slow[start+i]
	}

	sig := indicator.EMA(line, cfg.MACDSignal)
	sigStart := cfg.MACDSignal - 1 // index within `line`
	if len(line) < sigStart+3 {
		out.Status = StatusInsufficientData
		return out
	}

	hist := make([]float64, len(line))
	for i := sigStart; i < len(line); i++ {
		hist[i] = line[i] - sig[i]
	}

	last := len(line) - 1
	if !allFinite(line[last], sig[last], hist[last], line[last-1], sig[last-1]) {
		out.Status = StatusInvalidBar
		return out
	}

	out.Status = StatusOK
	out.MACD, out.SignalVal, out.Histogram = line[last], sig[last], hist[last]
	out.Cross = macdCross(line[last-1], sig[last-1], line[last], sig[last])
	out.Momentum = histogramMomentum(hist[last-2], hist[last-1], hist[last])
	return out
}

// macdCross reports a crossing on the latest bar only.
//
// Equality on the previous bar counts as "not yet above": a series that touches the signal
// line and then separates has crossed, and requiring a strict prior inequality would miss it.
func macdCross(prevMACD, prevSignal, macd, signal float64) string {
	switch {
	case prevMACD <= prevSignal && macd > signal:
		return CrossGolden
	case prevMACD >= prevSignal && macd < signal:
		return CrossDeath
	default:
		return CrossNone
	}
}

// histogramMomentum reads the last three histogram values.
//
// It measures the trajectory's DIRECTION, independent of sign: a histogram going
// -0.8 → -0.5 → -0.2 is decelerating bearish pressure and reports ACCELERATING (the move
// upward is gaining), while 0.8 → 0.5 → 0.2 reports DECELERATING even though MACD is still
// above its signal. Whether that is bullish or bearish is the Context's job to combine with
// direction — this function only describes the slope.
func histogramMomentum(h2, h1, h0 float64) string {
	if !allFinite(h2, h1, h0) {
		return MomentumUnknown
	}
	switch {
	case h0 > h1 && h1 > h2:
		return MomentumAccelerating
	case h0 < h1 && h1 < h2:
		return MomentumDecelerating
	default:
		return MomentumNeutral
	}
}
