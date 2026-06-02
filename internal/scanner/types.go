package scanner

import "time"

// Action is the trading recommendation.
type Action string

const (
	ActionStrongBuy Action = "STRONG BUY"
	ActionBuy       Action = "BUY"
	ActionWatch     Action = "WATCH"
	ActionHold      Action = "HOLD"
	ActionReduce    Action = "REDUCE"
	ActionSell      Action = "SELL"
)

// ActionCSS maps each action to its CSS class for HTML rendering.
var ActionCSS = map[Action]string{
	ActionStrongBuy: "action-strong-buy",
	ActionBuy:       "action-buy",
	ActionWatch:     "action-watch",
	ActionHold:      "action-hold",
	ActionReduce:    "action-reduce",
	ActionSell:      "action-sell",
}

// StockAnalysis is the full analysis result for a single stock.
type StockAnalysis struct {
	Symbol string
	Name   string
	// Source: "market" | "portfolio" | "watchlist"
	Source string
	Date   time.Time

	// Current price & volume
	Close  float64
	Volume int64

	// Portfolio context (Source == "portfolio")
	CostBasis float64
	Shares    int
	PnLPct    float64 // (Close - CostBasis) / CostBasis * 100
	PnLValue  float64 // (Close - CostBasis) * Shares

	// Scoring & recommendation
	Score   int    // 0–100
	Action  Action
	Reasons []string // Chinese explanations, one per factor

	// Price targets
	EntryPrice float64
	StopLoss   float64
	Target1    float64
	Target2    float64

	// Indicators (latest values)
	RSI         float64
	MA20        float64
	MA20Trend   string // ↑↑↑ / ↑↑ / ↑ / → / ↓ / ↓↓ / ↓↓↓
	KDJK        float64
	KDJD        float64
	KDJJ        float64
	BBWidth     float64
	BBUpper     float64
	BBLower     float64
	VolumeRatio float64
	ATR         float64
}

// PortfolioValue returns market value of the position.
func (a StockAnalysis) PortfolioValue() float64 {
	return a.Close * float64(a.Shares)
}

// PortfolioCost returns total cost of the position.
func (a StockAnalysis) PortfolioCost() float64 {
	return a.CostBasis * float64(a.Shares)
}
