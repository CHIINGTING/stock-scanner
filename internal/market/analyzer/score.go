package analyzer

import "github.com/deep-huang/stock-scanner/internal/market/model"

// Aggregate combines the module scores into the 0..100 Market Score. It is a weighted
// average over AVAILABLE modules only: when a source is missing, its weight drops out and
// the remaining weights renormalize (dividing by the available weight sum), so a partial
// data day degrades gracefully instead of being dragged toward zero.
func Aggregate(mods []model.ModuleScore) model.MarketScore {
	var sumW, sumWS float64
	var avail int
	for _, m := range mods {
		if !m.Available {
			continue
		}
		sumW += m.Weight
		sumWS += m.Weight * m.Score
		avail++
	}
	total := 0.0
	if sumW > 0 {
		total = sumWS / sumW
	}
	return model.MarketScore{Total: total, AvailableCount: avail, Modules: mods}
}
