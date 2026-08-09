package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// USD/TWD daily-change thresholds (TWD terms). BASELINE, not calibrated. A weaker TWD
// (positive USDTWD change) leans risk-off for Taiwan equities.
const (
	twdRiskOnChg  = -0.10 // USDTWD falls this much or more → TWD strength → Risk On
	twdRiskOffChg = 0.10  // USDTWD rises this much or more → TWD weakness → Risk Off
)

// AnalyzeCurrency scores the Currency module from the USD/TWD move (primary) with the DXY as
// context. Risk On (TWD strengthening) scores high; Risk Off scores low.
func AnalyzeCurrency(d model.CurrencyData, weight float64) model.ModuleScore {
	state, score := currencyState(d.USDTWDChg)
	return model.ModuleScore{
		Name:      model.ModuleCurrency,
		Score:     score,
		Weight:    weight,
		State:     state,
		Detail:    fmt.Sprintf("USD/TWD %.3f (%+.3f)・DXY %.2f (%+.2f)", d.USDTWD, d.USDTWDChg, d.DXY, d.DXYChg),
		Available: true,
	}
}

func currencyState(usdtwdChg float64) (string, float64) {
	switch {
	case usdtwdChg <= twdRiskOnChg:
		return CurrencyRiskOn, 65
	case usdtwdChg >= twdRiskOffChg:
		return CurrencyRiskOff, 35
	default:
		return CurrencyNeutral, 50
	}
}
