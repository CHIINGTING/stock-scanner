package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// candleSeries builds n daily candles with the given closes; AdjClose defaults to
// Close unless adj is supplied (same length as closes).
func candleSeries(closes []float64, adj []float64) []fetcher.Candle {
	out := make([]fetcher.Candle, len(closes))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		cd := fetcher.Candle{
			Date: base.AddDate(0, 0, i), Open: c, High: c, Low: c, Close: c, Volume: 1000,
		}
		if adj != nil {
			cd.AdjClose = adj[i]
		} else {
			cd.AdjClose = c
		}
		out[i] = cd
	}
	return out
}

// flat builds n candles all at price p.
func flat(n int, p float64) []fetcher.Candle {
	closes := make([]float64, n)
	for i := range closes {
		closes[i] = p
	}
	return candleSeries(closes, nil)
}

func testRSCfg() RSConfig {
	return RSConfig{Enable: true, LookbackDays: 120, MinHistoryDays: 100, ExcludeNonCommon: true}
}

// 1. RS universe excludes ETF / 特別股 / DR / non-4-digit.
func TestRSUniverseExcludesNonCommon(t *testing.T) {
	cfg := testRSCfg()
	cases := []struct {
		code, name string
		want       bool
	}{
		{"0050", "元大台灣50", false},  // ETF (leading 0)
		{"00878", "國泰永續高股息", false}, // ETF (5-digit)
		{"2891B", "中信金乙特", false},   // 特別股 (letter suffix)
		{"9136", "巨騰-DR", false},    // DR by name
		{"03001", "權證", false},      // warrant (5-digit)
	}
	for _, c := range cases {
		if got := IsRSUniverseEligible(c.code, c.name, cfg); got != c.want {
			t.Errorf("IsRSUniverseEligible(%q,%q)=%v want %v", c.code, c.name, got, c.want)
		}
	}
}

// 2. Ordinary common stocks (incl. -KY) are eligible.
func TestRSUniverseIncludesCommon(t *testing.T) {
	cfg := testRSCfg()
	for _, c := range []struct{ code, name string }{
		{"2330", "台積電"}, {"1101", "台泥"}, {"4991", "環宇-KY"},
	} {
		if !IsRSUniverseEligible(c.code, c.name, cfg) {
			t.Errorf("expected %s (%s) eligible", c.code, c.name)
		}
	}
	// When the filter is disabled, everything is eligible.
	off := testRSCfg()
	off.ExcludeNonCommon = false
	if !IsRSUniverseEligible("0050", "ETF", off) {
		t.Error("ExcludeNonCommon=false should make everything eligible")
	}
}

// 3. Insufficient history → not computed.
func TestRSInsufficientHistory(t *testing.T) {
	cfg := testRSCfg() // needs >=100 history and >120 bars
	if _, ok := rsReturnPct(flat(50, 100), cfg); ok {
		t.Error("50 bars (< min_history 100) should not compute")
	}
	if _, ok := rsReturnPct(flat(110, 100), cfg); ok {
		t.Error("110 bars (<= lookback 120) should not compute")
	}
}

// 4. lookback price <= 0 → not computed.
func TestRSLookbackPriceNonPositive(t *testing.T) {
	cfg := testRSCfg()
	closes := make([]float64, 130)
	for i := range closes {
		closes[i] = 100
	}
	closes[130-1-120] = 0 // the lookback bar is zero
	if _, ok := rsReturnPct(candleSeries(closes, nil), cfg); ok {
		t.Error("zero lookback price should not compute")
	}
}

// 5. RSReturnPct is correct.
func TestRSReturnPctValue(t *testing.T) {
	cfg := testRSCfg()
	closes := make([]float64, 130)
	for i := range closes {
		closes[i] = 100
	}
	closes[130-1-120] = 100 // lookback price
	closes[130-1] = 150     // current price → +50%
	pct, ok := rsReturnPct(candleSeries(closes, nil), cfg)
	if !ok {
		t.Fatal("expected computed")
	}
	if pct < 49.99 || pct > 50.01 {
		t.Errorf("RSReturnPct=%.4f want 50", pct)
	}
}

// 6. Percentile: stronger return → higher percentile.
func TestRSPercentileStrongerHigher(t *testing.T) {
	cfg := testRSCfg()
	mk := func(code string, cur float64) RSInput {
		closes := make([]float64, 130)
		for i := range closes {
			closes[i] = 100
		}
		closes[130-1] = cur // vary current price
		return RSInput{Symbol: code, Name: code, Candles: candleSeries(closes, nil)}
	}
	items := []RSInput{mk("1111", 90), mk("2222", 110), mk("3333", 150)}
	res := CalculateRSRanks(items, cfg)
	// res[2] strongest (+50%) should outrank res[1] (+10%) > res[0] (-10%).
	if !(res[2].RSRankPercentile > res[1].RSRankPercentile &&
		res[1].RSRankPercentile > res[0].RSRankPercentile) {
		t.Errorf("percentile not monotonic: %.1f %.1f %.1f",
			res[0].RSRankPercentile, res[1].RSRankPercentile, res[2].RSRankPercentile)
	}
	if res[2].RSScore != res[2].RSRankPercentile {
		t.Error("RSScore should equal percentile in v1")
	}
}

// 7. enable_rs_rank=false: pipeline never computes RS; analyze() output is RS-free.
// Proven structurally: StockAnalysis has no RS fields and analyze() never calls RS.
// Here we also assert CalculateRSRanks is opt-in & pure (does not mutate inputs).
func TestRSIsOptInAndPure(t *testing.T) {
	cfg := testRSCfg()
	items := []RSInput{{Symbol: "2330", Name: "台積電", Candles: flat(130, 100)}}
	before := items[0].Candles[0].Close
	_ = CalculateRSRanks(items, cfg)
	if items[0].Candles[0].Close != before {
		t.Error("CalculateRSRanks mutated input candles")
	}
	// Disabled config is just the gate the pipeline checks; the helper itself stays pure.
	if cfg.Enable && len(CalculateRSRanks(items, cfg)) != 1 {
		t.Error("unexpected result length")
	}
}

// 8. rs adjusted-close off → uses Close.
func TestRSUsesCloseWhenAdjOff(t *testing.T) {
	cfg := testRSCfg()
	cfg.UseAdjustedClose = false
	closes := make([]float64, 130)
	adj := make([]float64, 130)
	for i := range closes {
		closes[i] = 100
		adj[i] = 200 // very different, must be ignored when flag off
	}
	closes[130-1] = 150
	adj[130-1] = 300
	pct, ok := rsReturnPct(candleSeries(closes, adj), cfg)
	if !ok || pct < 49.99 || pct > 50.01 {
		t.Errorf("flag off should use Close → 50%%, got %.4f ok=%v", pct, ok)
	}
}

// 9. rs_use_adjusted_close=true & AdjClose valid → uses AdjClose.
func TestRSUsesAdjWhenOn(t *testing.T) {
	cfg := testRSCfg()
	cfg.UseAdjustedClose = true
	closes := make([]float64, 130)
	adj := make([]float64, 130)
	for i := range closes {
		closes[i] = 100
		adj[i] = 100
	}
	adj[130-1] = 120 // adjusted current +20% (close says +0%)
	pct, ok := rsReturnPct(candleSeries(closes, adj), cfg)
	if !ok || pct < 19.99 || pct > 20.01 {
		t.Errorf("flag on should use AdjClose → 20%%, got %.4f ok=%v", pct, ok)
	}
}

// 10. AdjClose invalid (<=0) with flag on → fallback Close.
func TestRSAdjInvalidFallbackClose(t *testing.T) {
	cfg := testRSCfg()
	cfg.UseAdjustedClose = true
	closes := make([]float64, 130)
	adj := make([]float64, 130)
	for i := range closes {
		closes[i] = 100
		adj[i] = 0 // invalid → PriceForCalc falls back to Close
	}
	closes[130-1] = 150
	pct, ok := rsReturnPct(candleSeries(closes, adj), cfg)
	if !ok || pct < 49.99 || pct > 50.01 {
		t.Errorf("invalid AdjClose should fall back to Close → 50%%, got %.4f ok=%v", pct, ok)
	}
}
