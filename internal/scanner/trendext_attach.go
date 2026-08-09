package scanner

import "github.com/deep-huang/stock-scanner/internal/fetcher"

// R12 Trend / Extension attach.
//
// Computed in EnrichWatchlist AFTER computeRocket and the decision, following the same
// convention as R7-1 HoldingHorizon, R6-7 HorizonHint and R10-1 BIAS. By the time this runs
// every score, action, probability and sort position is already final; nothing here writes to
// any of them. The only field touched is WatchlistEntry.TrendExt, a dedicated field OUTSIDE
// ShadowSignals — the C6b guardrail scoring reads ShadowSignals and nothing else, so this
// data cannot reach the scoring path even by accident.
//
// The sector-heat side is read-only: it consults the SectorRotation entry the rotation pass
// already produced, using the SAME configs/sectors.yaml taxonomy. No second sector mapping
// and no second aggregation exist.

// attachTrendExtension builds one entry's R12 reading.
//
// heat is nil when the stock has no sector, when rotation did not run, or when the sector was
// too thin to measure — all three stay distinguishable downstream (nil vs INSUFFICIENT_DATA).
func attachTrendExtension(e *WatchlistEntry, candles []fetcher.Candle, rot map[string]*SectorRotation) {
	var heat *SectorHeatView
	if e.HasSector && rot != nil {
		if sr := rot[e.Sector]; sr != nil {
			heat = sr.Heat
		}
	}
	// The BIAS risk label is the percentile FALLBACK only. When R10-1 did not run the label
	// is empty, which simply means the fallback is unavailable and extension rests on the
	// percentile — never on a guessed default.
	var biasRisk string
	if e.Bias != nil {
		biasRisk = e.Bias.Risk
	}
	v := computeTrendExtension(buildTrendExtInput(candles, biasRisk, e.Sector, heat))
	e.TrendExt = &v
}
