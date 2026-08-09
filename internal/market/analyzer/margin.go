package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// AnalyzeMargin scores the Margin module by whether retail is chasing price. The healthy
// (bullish) case is price up while margin (融資) falls — institutions lead, retail hasn't
// piled in. The bearish case is price down while margin rises — retail catching a falling
// knife. Signals are read from the sign of price change vs margin-balance change.
func AnalyzeMargin(d model.MarginData, weight float64) model.ModuleScore {
	state, score := marginState(d.PriceChangePct, d.MarginBalanceChg)
	return model.ModuleScore{
		Name:      model.ModuleMargin,
		Score:     score,
		Weight:    weight,
		State:     state,
		Detail:    fmt.Sprintf("Price %+.2f%%・融資 %+.0f億", d.PriceChangePct, d.MarginBalanceChg),
		Available: true,
	}
}

func marginState(priceChg, marginChg float64) (string, float64) {
	switch {
	case priceChg > 0 && marginChg < 0:
		// price up, margin down — strongest healthy tape.
		return MarginBullish, 65
	case priceChg < 0 && marginChg > 0:
		// price down, margin up — retail chasing losers.
		return MarginBearish, 30
	case priceChg > 0 && marginChg > 0:
		// price up but retail piling in — caution.
		return MarginNeutral, 45
	default:
		// price down + deleveraging, or flat — neutral.
		return MarginNeutral, 50
	}
}
