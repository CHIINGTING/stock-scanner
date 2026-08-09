package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// BIAS Risk shadow layer (R10-1). Display/context only — BIAS is追價/超跌 RISK, never a
// buy/sell signal (SPEC §13/§14). Computed in EnrichWatchlist behind EnableBias, AFTER
// computeRocket and never fed into it: WatchlistEntry.Bias never affects Score / Action /
// RocketScore / WatchAction / sort / stop.
// ──────────────────────────────────────────────────────────────────────────────

// BIAS risk labels (§14).
const (
	BiasRiskNA             = "N/A" // anchor window unavailable (insufficient history)
	BiasRiskNormal         = "NORMAL"
	BiasRiskElevated       = "ELEVATED"
	BiasRiskHigh           = "HIGH"
	BiasRiskExtreme        = "EXTREME"
	BiasRiskOversold       = "OVERSOLD"
	BiasRiskDeeplyOversold = "DEEPLY_OVERSOLD"
)

// BiasView holds the four BIAS windows (nil = insufficient history) + the risk label.
type BiasView struct {
	Bias5  *float64
	Bias10 *float64
	Bias20 *float64
	Bias60 *float64
	Risk   string
}

// BiasThresholds centralizes every BIAS number (§14). BASELINE — not calibrated. P1 makes
// these regime/volatility-adjusted; P0 keeps them fixed and in exactly one place.
type BiasThresholds struct {
	AnchorWindow   int     // the window the risk label is derived from (default 20)
	Elevated       float64 // >= → ELEVATED (default 10)
	High           float64 // >= → HIGH (default 15)
	Extreme        float64 // >= → EXTREME (default 20)
	Oversold       float64 // <= → OVERSOLD (default -10)
	DeeplyOversold float64 // <= → DEEPLY_OVERSOLD (default -18)
}

// DefaultBiasThresholds is the P0 baseline.
func DefaultBiasThresholds() BiasThresholds {
	return BiasThresholds{AnchorWindow: 20, Elevated: 10, High: 15, Extreme: 20, Oversold: -10, DeeplyOversold: -18}
}

// computeBias builds the BiasView from a candle history. Uses raw Close (consistent with
// existing indicators). Each window is nil when history is insufficient / SMA==0.
func computeBias(candles []fetcher.Candle, th BiasThresholds) BiasView {
	closes := closeSlice(candles)
	bv := BiasView{
		Bias5:  biasPtr(closes, 5),
		Bias10: biasPtr(closes, 10),
		Bias20: biasPtr(closes, 20),
		Bias60: biasPtr(closes, 60),
	}
	bv.Risk = classifyBiasRisk(anchorBias(bv, th.AnchorWindow), th)
	return bv
}

func biasPtr(closes []float64, period int) *float64 {
	if v, ok := indicator.BIAS(closes, period); ok {
		return &v
	}
	return nil
}

// anchorBias returns the BIAS value of the anchor window (default 20).
func anchorBias(bv BiasView, window int) *float64 {
	switch window {
	case 5:
		return bv.Bias5
	case 10:
		return bv.Bias10
	case 60:
		return bv.Bias60
	default:
		return bv.Bias20
	}
}

// classifyBiasRisk maps the anchor BIAS to a risk label. nil anchor → N/A (never guess).
func classifyBiasRisk(anchor *float64, th BiasThresholds) string {
	if anchor == nil {
		return BiasRiskNA
	}
	b := *anchor
	switch {
	case b >= th.Extreme:
		return BiasRiskExtreme
	case b >= th.High:
		return BiasRiskHigh
	case b >= th.Elevated:
		return BiasRiskElevated
	case b <= th.DeeplyOversold:
		return BiasRiskDeeplyOversold
	case b <= th.Oversold:
		return BiasRiskOversold
	default:
		return BiasRiskNormal
	}
}
