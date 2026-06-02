package fetcher

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
