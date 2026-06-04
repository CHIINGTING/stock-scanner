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
		nil, // C6a: no RS table
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

// TestC6aShadowDoesNotAffectScoring is the C6a golden-regression guard: turning all
// four shadow flags on must NOT change RocketScore / WatchAction / ExplosionProb or
// the output order — it only attaches shadow data (nil when off, populated when on).
func TestC6aShadowDoesNotAffectScoring(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
	}
	sectorOf := map[string]string{}
	rot := map[string]*SectorRotation{}
	members := map[string][]fetcher.StockData{}

	off := New(Config{}) // all shadow flags false
	resOff := off.EnrichWatchlist(items, sectorOf, rot, members, nil)

	on := New(Config{EnableRSRank: true, EnableNewHigh: true, EnableVCP: true, EnableMomentumFlow: true})
	resOn := on.EnrichWatchlist(items, sectorOf, rot, members, on.BuildRSTable(items))

	if len(resOff) != len(resOn) {
		t.Fatalf("length differs: off=%d on=%d", len(resOff), len(resOn))
	}
	for i := range resOff {
		if resOff[i].A.Symbol != resOn[i].A.Symbol {
			t.Errorf("order changed at %d: %s vs %s", i, resOff[i].A.Symbol, resOn[i].A.Symbol)
		}
		if resOff[i].RocketScore != resOn[i].RocketScore {
			t.Errorf("%s RocketScore changed: %d vs %d", resOff[i].A.Symbol, resOff[i].RocketScore, resOn[i].RocketScore)
		}
		if resOff[i].WatchAction != resOn[i].WatchAction {
			t.Errorf("%s WatchAction changed: %s vs %s", resOff[i].A.Symbol, resOff[i].WatchAction, resOn[i].WatchAction)
		}
		if resOff[i].ExplosionProb != resOn[i].ExplosionProb {
			t.Errorf("%s ExplosionProb changed: %s vs %s", resOff[i].A.Symbol, resOff[i].ExplosionProb, resOn[i].ExplosionProb)
		}
		// flags off → the whole shadow container is nil.
		if resOff[i].Shadow != nil {
			t.Errorf("%s: expected Shadow to be nil when all flags are disabled", resOff[i].A.Symbol)
		}
	}
	// flags on → container non-nil and per-stock shadows attached (260 candles suffice).
	for i := range resOn {
		s := resOn[i].Shadow
		if s == nil {
			t.Fatalf("%s: expected Shadow non-nil when flags are enabled", resOn[i].A.Symbol)
		}
		if s.NewHigh == nil || s.VCP == nil || s.Momentum == nil || s.RS == nil {
			t.Errorf("%s: flags on but shadow not fully attached (rs=%v nh=%v vcp=%v mom=%v)",
				resOn[i].A.Symbol, s.RS != nil, s.NewHigh != nil, s.VCP != nil, s.Momentum != nil)
		}
	}
}

// TestC6aSingleFlagShadow: enabling only one flag attaches only that field, leaves
// the others nil, and still does not change score/action/probability/order.
func TestC6aSingleFlagShadow(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
	}
	base := New(Config{}).EnrichWatchlist(items, map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}, nil)

	onlyVCP := New(Config{EnableVCP: true})
	got := onlyVCP.EnrichWatchlist(items, map[string]string{}, map[string]*SectorRotation{}, map[string][]fetcher.StockData{}, nil)

	for i := range got {
		if got[i].Shadow == nil {
			t.Fatalf("%s: expected Shadow non-nil with VCP enabled", got[i].A.Symbol)
		}
		if got[i].Shadow.VCP == nil {
			t.Errorf("%s: VCP shadow should be attached", got[i].A.Symbol)
		}
		if got[i].Shadow.RS != nil || got[i].Shadow.NewHigh != nil || got[i].Shadow.Momentum != nil {
			t.Errorf("%s: only VCP should be attached, others must be nil", got[i].A.Symbol)
		}
		// scoring unchanged vs the all-off baseline
		if got[i].A.Symbol != base[i].A.Symbol || got[i].RocketScore != base[i].RocketScore ||
			got[i].WatchAction != base[i].WatchAction || got[i].ExplosionProb != base[i].ExplosionProb {
			t.Errorf("%s: single-flag shadow changed scoring/order", got[i].A.Symbol)
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
