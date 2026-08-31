package fetcher

import (
	"fmt"
	"strings"
)

// YahooSymbol returns the Yahoo Finance ticker for a Taiwan stock.
//
//	market "TW"  → TWSE 上市，e.g. "2330.TW"
//	market "TWO" → TPEX 上櫃，e.g. "5483.TWO"
//	market ""    → 由呼叫端自動偵測（fallback）
func YahooSymbol(code, market string) string {
	switch market {
	case "TWO":
		return code + ".TWO"
	default: // "TW" or ""
		return code + ".TW"
	}
}

// Markets this repo supports. TWSE listed and TPEx over-the-counter — the two the fetcher,
// the TWSE/TPEx providers and the price cache all understand.
const (
	MarketTWSE = "TW"
	MarketTPEX = "TWO"
)

// ParseYahooSymbol splits a ticker like "2330.TW" back into code and market.
//
// It is the exact INVERSE of YahooSymbol, and lives beside it so the two cannot drift into
// disagreeing about what a symbol is. Everything in this repo stores a Taiwan stock as
// (code, market); a combined ticker only exists at the edges — the Yahoo URL, and a config
// file a human types by hand — so this is where that form is converted, once.
//
// The market suffix is validated STRICTLY: "2330.US" is refused rather than silently
// treated as TWSE, because a symbol this repo cannot fetch must fail at the config boundary
// instead of becoming a mysterious empty result later.
func ParseYahooSymbol(symbol string) (code, market string, err error) {
	s := strings.TrimSpace(symbol)
	if s == "" {
		return "", "", fmt.Errorf("empty symbol")
	}
	dot := strings.LastIndex(s, ".")
	if dot < 0 {
		return "", "", fmt.Errorf("%q has no market suffix (want e.g. 2330.%s or 5483.%s)",
			symbol, MarketTWSE, MarketTPEX)
	}
	code = s[:dot]
	// The suffix is upper-cased so "2330.tw" and "2330.TW" reach the same canonical form;
	// duplicate detection then sees them as the one stock they are.
	market = strings.ToUpper(s[dot+1:])

	switch market {
	case MarketTWSE, MarketTPEX:
	default:
		return "", "", fmt.Errorf("%q: market %q is not supported (only %s and %s)",
			symbol, market, MarketTWSE, MarketTPEX)
	}
	if !isPlausibleStockCode(code) {
		return "", "", fmt.Errorf("%q: %q is not a Taiwan stock code", symbol, code)
	}
	return code, market, nil
}

// isPlausibleStockCode reports whether code looks like a Taiwan listing.
//
// Deliberately WIDER than isOrdinaryStockCode: that one decides what enters the market SCAN
// (and excludes ETFs on purpose), while this decides what a human may legitimately name in
// config. Refusing 0050 here would mean the benchmark itself could not be written down.
//
// The shape is 4–6 characters, digits with an optional single trailing uppercase letter —
// which is what the price cache actually holds: 1,985 four-digit codes, plus a handful like
// 00981A and 00632R.
func isPlausibleStockCode(code string) bool {
	if len(code) < 4 || len(code) > 6 {
		return false
	}
	for i, r := range code {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z' && i == len(code)-1:
		default:
			return false
		}
	}
	return code[0] >= '0' && code[0] <= '9'
}
