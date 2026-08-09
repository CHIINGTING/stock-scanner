package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// AnalyzeFutures scores the Foreign Futures module. The Trend (from the daily change) drives
// the 0..100 score; the Extreme Indicator (from net position) is reported as Detail and, for
// heavy/extreme readings, surfaced later as a regime caveat rather than counted as bearish.
func AnalyzeFutures(d model.FuturesData, weight float64, th model.FuturesThresholds) model.ModuleScore {
	trend, score := futuresTrend(d.DailyChange, th)
	extreme := futuresExtreme(d.NetPosition, th)
	return model.ModuleScore{
		Name:      model.ModuleForeignFutures,
		Score:     score,
		Weight:    weight,
		State:     trend,
		Detail:    fmt.Sprintf("Net %.0f・%s・Δ%+.0f", d.NetPosition, extreme, d.DailyChange),
		Available: true,
	}
}

// futuresTrend maps the daily change to a Trend label and a bullishness score (0..100).
// First match wins, most-bearish first.
func futuresTrend(change float64, th model.FuturesThresholds) (string, float64) {
	switch {
	case change <= th.AggressiveShortChange:
		return FuturesAggressiveShort, 20
	case change <= th.MoreShortChange:
		return FuturesMoreShort, 40
	case change <= th.StableChange:
		return FuturesStable, 50
	case change <= th.CoverShortChange:
		return FuturesCoverShort, 65
	default:
		return FuturesAggressiveCover, 80
	}
}

// futuresExtreme maps the net position to an Extreme Indicator label, most-negative first.
func futuresExtreme(net float64, th model.FuturesThresholds) string {
	switch {
	case net < th.ExtremeShortNet:
		return ExtremeExtremeShort
	case net < th.HeavyShortNet:
		return ExtremeHeavyShort
	case net < th.ShortNet:
		return ExtremeShort
	case net <= th.LightShortNet:
		return ExtremeLightShort
	default:
		return ExtremeNeutral
	}
}

// FuturesExtreme exposes the Extreme Indicator for callers that want it standalone (e.g.
// regime caveats). It mirrors the internal bucketing.
func FuturesExtreme(net float64, th model.FuturesThresholds) string { return futuresExtreme(net, th) }
