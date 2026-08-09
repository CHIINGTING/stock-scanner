package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Foreign-cash net thresholds (億 TWD). BASELINE, not calibrated.
const (
	cashStrongBuyNet  = 200
	cashBuyNet        = 50
	cashSellNet       = -50
	cashStrongSellNet = -200
)

// AnalyzeCash scores the Foreign Cash module from net buy/sell. Higher net buying = more
// bullish = higher score.
func AnalyzeCash(d model.ForeignCashData, weight float64) model.ModuleScore {
	net := d.Net()
	state, score := cashState(net)
	return model.ModuleScore{
		Name:      model.ModuleForeignCash,
		Score:     score,
		Weight:    weight,
		State:     state,
		Detail:    fmt.Sprintf("Net %+.0f億 (Buy %.0f / Sell %.0f)", net, d.Buy, d.Sell),
		Available: true,
	}
}

func cashState(net float64) (string, float64) {
	switch {
	case net >= cashStrongBuyNet:
		return CashStrongBuy, 80
	case net >= cashBuyNet:
		return CashBuy, 65
	case net > cashSellNet:
		return CashNeutral, 50
	case net > cashStrongSellNet:
		return CashSell, 35
	default:
		return CashStrongSell, 20
	}
}
