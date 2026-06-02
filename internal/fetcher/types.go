package fetcher

import "time"

// StockInfo is a lightweight descriptor used when building fetch job lists.
type StockInfo struct {
	Symbol string
	Name   string
	// Market: "TW" (TWSE 上市) | "TWO" (TPEX 上櫃) | "" (auto-detect)
	Market string
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
	// Market: "TW" | "TWO" (resolved after fetch)
	Market string
	// Source: "market" | "portfolio" | "watchlist"
	Source    string
	CostBasis float64 // only set when Source == "portfolio"
	Shares    int     // only set when Source == "portfolio"
	Candles   []Candle
}
