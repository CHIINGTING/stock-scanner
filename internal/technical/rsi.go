package technical

import "github.com/deep-huang/stock-scanner/internal/indicator"

// RSIResult is the interpreted view of Wilder's RSI.
//
// The number itself is NOT computed here: internal/indicator.RSI already implements Wilder
// smoothing and is what the rest of the scanner reads. Reimplementing it would create a
// second RSI that could silently disagree with the one on the report — the exact failure R14
// is meant to avoid. This file adds only the reading: which zone, and which way it is going.
//
// OVERBOUGHT is not SELL and OVERSOLD is not BUY. Whether a Taiwanese stock above 70
// continues or mean-reverts is an open question R14's outcome validation exists to answer,
// so this layer states the zone and stops there.
type RSIResult struct {
	Status Status `json:"status"`

	Period int     `json:"period"`
	Value  float64 `json:"value"`
	Zone   string  `json:"zone"`

	// Rising / Falling describe the last completed step. Both false means flat — the two
	// flags are not complements, and a flat RSI is a real observation.
	Rising  bool `json:"rising"`
	Falling bool `json:"falling"`
}

// RSI zone boundaries. Textbook, therefore hypotheses (see the R14 non-negotiable: nothing
// may be scored on these until the historical validation says they carry weight).
const (
	RSIOversold   = "OVERSOLD"   // < 30
	RSIWeak       = "WEAK"       // [30, 45)
	RSINeutral    = "NEUTRAL"    // [45, 55)
	RSIBullish    = "BULLISH"    // [55, 70]
	RSIOverbought = "OVERBOUGHT" // > 70
)

const (
	rsiOversoldBelow   = 30.0
	rsiWeakBelow       = 45.0
	rsiNeutralBelow    = 55.0
	rsiOverboughtAbove = 70.0
)

// ComputeRSI reads the latest Wilder RSI and classifies it.
//
// It needs period+2 bars, not period+1: the extra bar is what makes Rising/Falling a
// measurement rather than a guess. Without a previous RSI value there is no trajectory, and
// reporting "flat" in that case would be a fabrication.
func ComputeRSI(bars []Bar, cfg Config) RSIResult {
	cfg = cfg.Defaulted()
	out := RSIResult{Period: cfg.RSIPeriod, Zone: DirUnknown}

	s, err := newSeries(bars)
	if err != nil {
		out.Status = statusFor(err)
		return out
	}
	p := cfg.RSIPeriod
	if s.n < p+2 {
		out.Status = StatusInsufficientData
		return out
	}

	vals := indicator.RSI(s.closes, p)
	last := vals[s.n-1]
	prev := vals[s.n-2]
	if !allFinite(last, prev) {
		out.Status = StatusInvalidBar
		return out
	}

	out.Status = StatusOK
	out.Value = last
	out.Zone = rsiZone(last)
	out.Rising = last > prev
	out.Falling = last < prev
	return out
}

// rsiZone buckets the value. Boundaries follow the R14 specification exactly: the BULLISH
// band is inclusive at 70, so only a value strictly above 70 is OVERBOUGHT.
func rsiZone(v float64) string {
	switch {
	case v < rsiOversoldBelow:
		return RSIOversold
	case v < rsiWeakBelow:
		return RSIWeak
	case v < rsiNeutralBelow:
		return RSINeutral
	case v <= rsiOverboughtAbove:
		return RSIBullish
	default:
		return RSIOverbought
	}
}
