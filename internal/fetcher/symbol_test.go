package fetcher

import "testing"

// ParseYahooSymbol must be the exact inverse of YahooSymbol. If the two ever disagree, a
// config file and a fetch URL would be describing different stocks.
func TestParseIsTheInverseOfCompose(t *testing.T) {
	for _, tc := range []struct{ code, market string }{
		{"2330", MarketTWSE}, {"6488", MarketTPEX}, {"0050", MarketTWSE},
		{"00981A", MarketTWSE}, {"5483", MarketTPEX},
	} {
		sym := YahooSymbol(tc.code, tc.market)
		gotCode, gotMarket, err := ParseYahooSymbol(sym)
		if err != nil {
			t.Errorf("%s: %v", sym, err)
			continue
		}
		if gotCode != tc.code || gotMarket != tc.market {
			t.Errorf("%s round-tripped to (%q, %q), want (%q, %q)",
				sym, gotCode, gotMarket, tc.code, tc.market)
		}
	}
}

// The market suffix is case-insensitive on input and canonical on output, so two spellings
// of one stock cannot survive as two.
func TestParseNormalisesTheMarketSuffix(t *testing.T) {
	for _, in := range []string{"2330.tw", "2330.Tw", "2330.TW", " 2330.TW "} {
		code, market, err := ParseYahooSymbol(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if code != "2330" || market != MarketTWSE {
			t.Errorf("%q → (%q, %q)", in, code, market)
		}
	}
}

// A symbol this repo cannot fetch must be refused here rather than becoming an empty result
// somewhere downstream.
func TestParseRejectsUnsupported(t *testing.T) {
	for name, sym := range map[string]string{
		"no suffix":     "2330",
		"US market":     "2330.US",
		"HK market":     "0700.HK",
		"empty":         "",
		"suffix only":   ".TW",
		"letters":       "ABC.TW",
		"leading alpha": "A330.TW",
		"too short":     "233.TW",
		"too long":      "1234567.TW",
		"two letters":   "2330AB.TW",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseYahooSymbol(sym); err == nil {
				t.Errorf("%q was accepted", sym)
			}
		})
	}
}

// The config-level check is deliberately WIDER than the scan-universe filter: a human must
// be able to name an ETF (including the benchmark) in a research list, even though the market
// scan excludes them.
func TestPlausibleCodeIsWiderThanTheScanUniverse(t *testing.T) {
	for _, code := range []string{"0050", "00981A", "00632R"} {
		if isOrdinaryStockCode(code) {
			t.Fatalf("fixture is wrong: %s is in the scan universe, so this proves nothing", code)
		}
		if !isPlausibleStockCode(code) {
			t.Errorf("%s cannot be named in config, so the benchmark could not be researched", code)
		}
	}
	// And an ordinary stock is valid under both.
	if !isOrdinaryStockCode("2330") || !isPlausibleStockCode("2330") {
		t.Error("2330 should satisfy both")
	}
}
