package technical

import "github.com/deep-huang/stock-scanner/internal/indicator"

// KeltnerResult is the EMA-centred, ATR-scaled envelope.
//
// Position says where price sits; Channel says whether it has left the envelope. They are
// separate because "above the upper band" is a location, while "this is a breakout" is a
// claim about what that location means — and neither is a trade instruction. Whether a
// Keltner breakout is worth anything on its own, or only when ADX confirms it, is one of the
// questions R14's outcome validation is built to answer.
type KeltnerResult struct {
	Status Status `json:"status"`

	EMAPeriod  int     `json:"ema_period"`
	ATRPeriod  int     `json:"atr_period"`
	Multiplier float64 `json:"multiplier"`

	Middle float64 `json:"middle"`
	Upper  float64 `json:"upper"`
	Lower  float64 `json:"lower"`
	ATR    float64 `json:"atr"`

	Position string `json:"position"` // PosAboveUpper … PosBelowLower
	Channel  string `json:"channel"`  // ChannelBreakout | Breakdown | Inside

	// WidthPct is the full channel width as a percentage of the middle line — the
	// scale-free form, so a 30 TWD channel on a 1000 TWD stock is not mistaken for a wide
	// one.
	WidthPct float64 `json:"width_pct"`
}

// Position vocabulary.
const (
	PosAboveUpper = "ABOVE_UPPER"
	PosUpperHalf  = "UPPER_HALF"
	PosMiddle     = "MIDDLE"
	PosLowerHalf  = "LOWER_HALF"
	PosBelowLower = "BELOW_LOWER"
)

// Channel vocabulary.
const (
	ChannelBreakout  = "BREAKOUT"
	ChannelBreakdown = "BREAKDOWN"
	ChannelInside    = "INSIDE_CHANNEL"
)

// keltnerMiddleBand is how close to the centre line price must be to read as MIDDLE,
// expressed as a fraction of the channel's HALF width.
//
// A band is needed at all because an exact equality with the EMA essentially never happens,
// and an enum value that can never occur is worse than no enum value. A fraction of the
// half-width rather than a fixed percentage keeps it meaningful across volatility regimes:
// in a wide channel the "middle" is genuinely a wider zone.
const keltnerMiddleBand = 0.10

// ComputeKeltner builds the channel from the existing EMA and Wilder ATR primitives.
//
// ATR is NOT recomputed here: internal/indicator.ATR is already Wilder's, and it is what the
// rest of the scanner reads. A second ATR could disagree with the one used for stops.
func ComputeKeltner(bars []Bar, cfg Config) KeltnerResult {
	cfg = cfg.Defaulted()
	out := KeltnerResult{
		EMAPeriod: cfg.KeltnerEMAPeriod, ATRPeriod: cfg.KeltnerATRPeriod,
		Multiplier: cfg.KeltnerMultiplier,
		Position:   LocationUnknown, Channel: LocationUnknown,
	}

	s, err := newSeries(bars)
	if err != nil {
		out.Status = statusFor(err)
		return out
	}
	// ATR is defined from index atrPeriod (it needs one prior close), the EMA from
	// emaPeriod-1. Both must be published on the last bar.
	need := cfg.KeltnerATRPeriod + 1
	if cfg.KeltnerEMAPeriod > need {
		need = cfg.KeltnerEMAPeriod
	}
	if s.n < need {
		out.Status = StatusInsufficientData
		return out
	}

	ema := indicator.EMA(s.closes, cfg.KeltnerEMAPeriod)
	atr := indicator.ATR(s.highs, s.lows, s.closes, cfg.KeltnerATRPeriod)

	i := s.n - 1
	middle, band := ema[i], atr[i]*cfg.KeltnerMultiplier
	upper, lower := middle+band, middle-band
	closePx := s.closes[i]

	if !allFinite(middle, band, upper, lower) || middle <= 0 {
		out.Status = StatusInvalidBar
		return out
	}

	out.Status = StatusOK
	out.Middle, out.Upper, out.Lower, out.ATR = middle, upper, lower, atr[i]
	if w, ok := safeDiv(100*(upper-lower), middle); ok {
		out.WidthPct = w
	}

	// A zero-ATR (perfectly flat) tape collapses the channel onto the EMA. There is no
	// breakout to speak of, and calling one would be an artefact of a degenerate divisor.
	if band == 0 {
		out.Position = PosMiddle
		out.Channel = ChannelInside
		return out
	}

	switch {
	case closePx > upper:
		out.Position, out.Channel = PosAboveUpper, ChannelBreakout
	case closePx < lower:
		out.Position, out.Channel = PosBelowLower, ChannelBreakdown
	default:
		out.Channel = ChannelInside
		switch dev := closePx - middle; {
		case dev > keltnerMiddleBand*band:
			out.Position = PosUpperHalf
		case dev < -keltnerMiddleBand*band:
			out.Position = PosLowerHalf
		default:
			out.Position = PosMiddle
		}
	}
	return out
}
