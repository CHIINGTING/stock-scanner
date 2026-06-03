package scanner

import "time"

// Action is the trading recommendation.
type Action string

const (
	ActionStrongBuy  Action = "STRONG BUY"
	ActionBuy        Action = "BUY"
	ActionWatch      Action = "WATCH"
	ActionHold       Action = "HOLD"
	ActionReduce     Action = "REDUCE"
	ActionTakeProfit Action = "TAKE PROFIT"
	ActionStopLoss   Action = "STOP LOSS"
	ActionSell       Action = "SELL"
)

// ActionCSS maps each action to its CSS class for HTML rendering.
var ActionCSS = map[Action]string{
	ActionStrongBuy:  "action-strong-buy",
	ActionBuy:        "action-buy",
	ActionWatch:      "action-watch",
	ActionHold:       "action-hold",
	ActionReduce:     "action-reduce",
	ActionTakeProfit: "action-take-profit",
	ActionStopLoss:   "action-stop-loss",
	ActionSell:       "action-sell",
}

// LimitStatus classifies limit-up (漲停) chip dynamics inferred from daily OHLCV.
//
// 量縮本身不是問題，問題是「量縮時價格有沒有失守」：
//   - 漲停鎖住後量縮 → 賣壓惜售、籌碼鎖定，視為中性偏多（不扣分）。
//   - 漲停打開後放量下殺 / 漲停後放量下跌 → 出貨，才是負面訊號。
const (
	LimitLockedLowVol = "LOCKED_LIMIT_UP_LOW_VOLUME"  // 漲停鎖住後量縮（中性偏多）
	LimitUpFailed     = "LIMIT_UP_FAILED"             // 漲停打開後放量下殺
	LimitDistribution = "DISTRIBUTION_AFTER_LIMIT_UP" // 前日漲停後今日放量下跌（出貨）
)

// BFPCheckpoint holds the result of one BestFourPoint check.
type BFPCheckpoint struct {
	Name   string // e.g. "趨勢"
	Pass   bool
	Reason string
}

// StockAnalysis is the full analysis result for a single stock.
type StockAnalysis struct {
	Symbol string
	Name   string
	// Source: "market" | "portfolio" | "watchlist"
	Source string
	Date   time.Time

	// ── Current market data ──────────────────────────────────────────────────
	Close  float64
	Volume int64

	// ── Position context (Source == "portfolio") ─────────────────────────────
	CostBasis float64 // entry price
	Shares    int
	PnLPct    float64 // (Close - CostBasis) / CostBasis * 100
	PnLValue  float64 // (Close - CostBasis) * Shares

	// ── Trading advice ───────────────────────────────────────────────────────
	Score   int      // 0–100 composite score
	Action  Action   // the primary trading recommendation
	Reasons []string // human-readable reasons (Traditional Chinese)

	// BestFourPoint-style checkpoints
	BFPPoints int             // 0–5 how many checkpoints passed
	BFP       []BFPCheckpoint // individual checkpoint results

	// ── Price targets ────────────────────────────────────────────────────────
	EntryPrice float64
	StopLoss   float64
	Target1    float64
	Target2    float64

	// ── Indicators ───────────────────────────────────────────────────────────
	RSI         float64
	MA20        float64
	MA20Trend   string  // ↑↑↑ / ↑↑ / ↑ / → / ↓ / ↓↓ / ↓↓↓
	KDJK        float64
	KDJD        float64
	KDJJ        float64
	BBWidth     float64
	BBUpper     float64
	BBLower     float64
	VolumeRatio float64
	ATR         float64

	// ── Volume analysis ──────────────────────────────────────────────────────
	VolumeScore      int     // 0–25
	AvgVolume20      int64   // 20-day average volume
	PriceVolumeSignal string  // "價漲量增" | "價漲量縮" | "價跌量增" | "價跌量縮" | "漲停鎖量" | "漲停失敗"
	BuySellRatio     float64 // approximated buying pressure ratio (> 1 = bullish)
	IsLargeOrder     bool    // volume > 3× MA20

	// ── Limit-up (漲停) chip dynamics ────────────────────────────────────────
	LimitStatus string // "" | LOCKED_LIMIT_UP_LOW_VOLUME | LIMIT_UP_FAILED | DISTRIBUTION_AFTER_LIMIT_UP
	LimitNote   string // human-readable interpretation (Traditional Chinese)
}

// PortfolioValue returns current market value of the position.
func (a StockAnalysis) PortfolioValue() float64 {
	return a.Close * float64(a.Shares)
}

// PortfolioCost returns total cost of the position.
func (a StockAnalysis) PortfolioCost() float64 {
	return a.CostBasis * float64(a.Shares)
}
