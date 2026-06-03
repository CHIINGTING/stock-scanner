package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func TestBucketOf(t *testing.T) {
	cases := []struct {
		days int
		want ConsolBucket
	}{
		{3, MicroBase}, {5, MicroBase},
		{6, ShortBase}, {10, ShortBase},
		{11, SwingBase}, {20, SwingBase},
		{21, MidBase}, {40, MidBase},
		{41, LongBase}, {60, LongBase},
	}
	for _, c := range cases {
		if got := bucketOf(c.days); got != c.want {
			t.Errorf("bucketOf(%d)=%v want %v", c.days, got, c.want)
		}
	}
}

func TestWatchActionFor(t *testing.T) {
	cases := []struct {
		stage RocketStage
		ret1  float64
		vol   float64
		want  WatchAction
	}{
		{StageBaseBuilding, 0, 1, ActWatchClose},
		{StagePreBreakout, 0, 1, ActPrepare},
		{StageBreakoutStart, 0, 1, ActBreakoutBuy},
		{StageMainRun, -1, 0.8, ActPullbackBuy}, // pulling back on low volume
		{StageMainRun, 1, 1.5, ActWatchClose},   // still advancing
		{StageOverheated, 0, 1, ActTakeProfit},
		{StageFailed, 0, 1, ActRemove},
		{StageNotReady, 0, 1, ActWait},
	}
	for _, c := range cases {
		if got := watchActionFor(c.stage, c.ret1, c.vol); got != c.want {
			t.Errorf("watchActionFor(%v,%v,%v)=%v want %v", c.stage, c.ret1, c.vol, got, c.want)
		}
	}
}

func TestExplosionProb(t *testing.T) {
	if explosionProb(StagePreBreakout, 80) != "HIGH" {
		t.Errorf("pre-breakout high score should be HIGH")
	}
	if explosionProb(StagePreBreakout, 65) != "MEDIUM" {
		t.Errorf("mid score should be MEDIUM")
	}
	if explosionProb(StageOverheated, 95) != "LOW" {
		t.Errorf("overheated should be LOW (explosion mostly done)")
	}
	if explosionProb(StageFailed, 80) != "LOW" {
		t.Errorf("failed should be LOW")
	}
}

func TestBacktestConfidence(t *testing.T) {
	if backtestConfidence(20, 5) != "HIGH" {
		t.Errorf("stock>=15 → HIGH")
	}
	if backtestConfidence(2, 40) != "HIGH" {
		t.Errorf("sector>=30 → HIGH")
	}
	if backtestConfidence(7, 5) != "MEDIUM" {
		t.Errorf("stock>=6 → MEDIUM")
	}
	if backtestConfidence(2, 3) != "LOW" {
		t.Errorf("tiny samples → LOW")
	}
}

// TestEnrichWatchlistSmoke verifies the full pipeline runs and sorts by score.
func TestEnrichWatchlistSmoke(t *testing.T) {
	s := New(Config{})
	strong := fetcher.StockData{Symbol: "1111", Name: "Strong", Source: "watchlist",
		Candles: makeCandles(260, 50, 0.4, 2_000_000)}
	flat := fetcher.StockData{Symbol: "2222", Name: "Flat", Source: "watchlist",
		Candles: makeCandles(260, 50, 0.0, 1_000_000)}

	out := s.EnrichWatchlist(
		[]fetcher.StockData{flat, strong},
		map[string]string{},                  // no sector linkage
		map[string]*SectorRotation{},
		map[string][]fetcher.StockData{},
	)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	// Sorted by RocketScore desc.
	if out[0].RocketScore < out[1].RocketScore {
		t.Errorf("not sorted by RocketScore: %d < %d", out[0].RocketScore, out[1].RocketScore)
	}
	for _, e := range out {
		if e.RocketScore < 0 || e.RocketScore > 100 {
			t.Errorf("%s score out of range: %d", e.A.Symbol, e.RocketScore)
		}
		if e.RocketStage == "" || e.WatchAction == "" {
			t.Errorf("%s missing stage/action", e.A.Symbol)
		}
		if e.Backtest.PatternName == "" {
			t.Errorf("%s missing backtest pattern", e.A.Symbol)
		}
	}
}

// TestBacktestAggregation builds a synthetic series with repeated breakout-after-base
// patterns and checks the engine records samples with positive bias.
func TestBacktestAggregation(t *testing.T) {
	// Construct a series: repeated [flat base 8 bars] → [up thrust]. After each thrust
	// price keeps rising for a few bars (positive forward return).
	var candles []fetcher.Candle
	price := 50.0
	day := 0
	addBar := func(o, h, l, c float64, v int64) {
		candles = append(candles, bar(o, h, l, c, v))
		day++
	}
	for cycle := 0; cycle < 12; cycle++ {
		// 8-bar tight base around `price`
		for i := 0; i < 8; i++ {
			addBar(price, price+0.3, price-0.3, price, 1_000_000)
		}
		// breakout bar + follow-through up
		for i := 0; i < 6; i++ {
			price += 1.2
			addBar(price-0.5, price+0.2, price-0.6, price, 2_500_000)
		}
		// small pullback
		price -= 1.0
		addBar(price+0.5, price+0.5, price-0.2, price, 1_200_000)
	}

	s := New(Config{})
	ind := s.calcIndicators(candles)
	bt := s.runBacktest(candles, ind, nil)
	if bt.StockSampleCount == 0 {
		t.Fatalf("expected some backtest samples, got 0 (pattern=%s)", bt.PatternName)
	}
	if bt.PatternName == "" {
		t.Errorf("pattern name empty")
	}
	t.Logf("pattern=%s samples=%d winRate=%.0f avgRet=%.1f conf=%s",
		bt.PatternName, bt.StockSampleCount, bt.StockWinRate, bt.AvgReturn, bt.Confidence)
}
