package fetcher

import "time"

type StockInfo struct {
	Symbol string
	Name   string
}

// Candle represents a single daily OHLCV bar.
type Candle struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// StockData holds the full history for one stock, sorted oldest-first.
type StockData struct {
	Symbol string
	Name   string
	// Source identifies where this stock came from.
	// Values: "market" | "portfolio" | "watchlist"
	Source    string
	CostBasis float64 // only set when Source == "portfolio"
	Shares    int     // only set when Source == "portfolio"
	Candles   []Candle
}
