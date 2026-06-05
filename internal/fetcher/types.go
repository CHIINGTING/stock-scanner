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
	// AdjClose is the split/dividend-adjusted close. When the data source does not
	// provide it (or it is invalid), it falls back to Close at parse time, so it is
	// never a misleading zero. Read it via PriceForCalc, never decide on AdjClose
	// being zero by itself.
	AdjClose float64
}

// PriceForCalc returns the price a calculation should use for a candle.
//
//   - useAdjusted == false            → always Close (preserves existing behaviour).
//   - useAdjusted == true, AdjClose>0 → AdjClose.
//   - useAdjusted == true, AdjClose<=0 (missing/invalid) → fallback to Close.
//
// This is the single entry point every adjusted-price-aware calculation (RS, new
// high, VCP, backtest …) should call, so the fallback rule lives in exactly one place.
func PriceForCalc(c Candle, useAdjusted bool) float64 {
	if useAdjusted && c.AdjClose > 0 {
		return c.AdjClose
	}
	return c.Close
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
