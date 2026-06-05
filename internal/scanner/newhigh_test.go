package scanner

import "testing"

func testNHCfg() NewHighConfig {
	return newHighConfigFrom(Config{
		EnableNewHigh: true,
		// leave the rest zero → defaults: [20,60,120,250], minHist 60, within 25,
		// strong 10, far 50, near 3, buffer 1, volConfirm 1.5, overext 75.
	})
}

// risingTo builds n candles: flat at base for the first n-1 bars, then `last`
// on the final bar (so prior-window highs equal base; today decides new highs).
func risingTo(n int, base, last float64) []float64 {
	c := make([]float64, n)
	for i := range c {
		c[i] = base
	}
	c[n-1] = last
	return c
}

// 1. Multi-period new-high flags at the boundary lengths.
func TestNewHighFlags(t *testing.T) {
	cfg := testNHCfg()
	// 300 bars, today clearly above everything → all four new highs.
	r := computeNewHigh(candleSeries(risingTo(300, 100, 200), nil), 1.0, 50, cfg)
	if !r.Computed || !(r.H20 && r.H60 && r.H120 && r.H250) {
		t.Fatalf("expected all new highs, got computed=%v H20=%v H60=%v H120=%v H250=%v",
			r.Computed, r.H20, r.H60, r.H120, r.H250)
	}
	if !(r.H20Valid && r.H60Valid && r.H120Valid && r.H250Valid) {
		t.Error("all validity flags should be true with 300 bars")
	}
	// 80 bars: H20/H60 valid; H120/H250 invalid (insufficient lookback).
	r2 := computeNewHigh(candleSeries(risingTo(80, 100, 200), nil), 1.0, 50, cfg)
	if !(r2.H20Valid && r2.H60Valid) || r2.H120Valid || r2.H250Valid {
		t.Errorf("80 bars validity wrong: H20=%v H60=%v H120=%v H250=%v",
			r2.H20Valid, r2.H60Valid, r2.H120Valid, r2.H250Valid)
	}
}

// 2. Insufficient history → not computed.
func TestNewHighInsufficientHistory(t *testing.T) {
	cfg := testNHCfg() // min history 60
	if r := computeNewHigh(flat(40, 100), 1.0, 50, cfg); r.Computed {
		t.Error("40 bars (< min 60) should not compute")
	}
}

// 3. Distance from 52w high: at high → 0; pulled back 7% → -7.
func TestDistanceFrom52wHigh(t *testing.T) {
	cfg := testNHCfg()
	atHigh := computeNewHigh(candleSeries(risingTo(300, 100, 200), nil), 1.0, 50, cfg)
	if atHigh.DistanceFrom52wHighPct < -0.01 || atHigh.DistanceFrom52wHighPct > 0.01 {
		t.Errorf("at high want 0, got %.4f", atHigh.DistanceFrom52wHighPct)
	}
	// High of 200 reached earlier, today 186 → -7%.
	closes := risingTo(300, 100, 0)
	closes[200] = 200 // 52w high
	closes[299] = 186 // today
	r := computeNewHigh(candleSeries(closes, nil), 1.0, 50, cfg)
	if r.DistanceFrom52wHighPct < -7.01 || r.DistanceFrom52wHighPct > -6.99 {
		t.Errorf("want -7, got %.4f", r.DistanceFrom52wHighPct)
	}
}

// 4. Leadership eligibility (within 25%) vs not.
func TestNewHighLeadership(t *testing.T) {
	cfg := testNHCfg()
	mk := func(today float64) NewHighResult {
		closes := risingTo(300, 100, 0)
		closes[150] = 200 // 52w high
		closes[299] = today
		return computeNewHigh(candleSeries(closes, nil), 1.0, 50, cfg)
	}
	if r := mk(160); !r.NewHighLeadershipEligible { // -20% → eligible
		t.Errorf("-20%% should be leadership-eligible, dist=%.1f", r.DistanceFrom52wHighPct)
	}
	if r := mk(120); r.NewHighLeadershipEligible { // -40% → not eligible
		t.Errorf("-40%% should NOT be eligible, dist=%.1f", r.DistanceFrom52wHighPct)
	}
}

// 5. Distance-to-52w-high bands (C3a semantics): leadership 25% ⊇ near 15% ⊇ breakout 5%.
func TestNewHighDistanceBands(t *testing.T) {
	cfg := testNHCfg() // within 25, near 15, breakout 5
	mk := func(distPct float64) NewHighResult {
		closes := risingTo(300, 100, 0)
		closes[150] = 200                     // 52-week high
		closes[299] = 200 * (1 - distPct/100) // today distPct% below the high
		return computeNewHigh(candleSeries(closes, nil), 1.0, 50, cfg)
	}
	cases := []struct {
		dist                   float64
		leader, near, breakout bool
	}{
		{24, true, false, false},
		{14, true, true, false},
		{4, true, true, true},
		{30, false, false, false},
	}
	for _, c := range cases {
		r := mk(c.dist)
		if r.NewHighLeadershipEligible != c.leader || r.Near52wHigh != c.near || r.BreakoutWatch != c.breakout {
			t.Errorf("dist=%g%% (computed %.2f): leader=%v near=%v breakout=%v; want %v/%v/%v",
				c.dist, r.DistanceFrom52wHighPct,
				r.NewHighLeadershipEligible, r.Near52wHigh, r.BreakoutWatch,
				c.leader, c.near, c.breakout)
		}
	}
}

// 6. NewHighScore: all-green → 100; far from high → capped 35; H250 overext → ×0.6.
func TestNewHighScore(t *testing.T) {
	cfg := testNHCfg()
	// All-green: new highs across the board, volume-confirmed, at 52w high.
	full := computeNewHigh(candleSeries(risingTo(300, 100, 200), nil), 1.6, 50, cfg)
	if full.NewHighScore != 100 {
		t.Errorf("all-green want 100, got %.1f", full.NewHighScore)
	}
	// Far from high: build a far-from-52w case (not a leader) → score capped ≤ 35.
	closes := risingTo(300, 100, 0)
	closes[150] = 300 // very high earlier
	closes[299] = 120 // today far below (−60%), but still a 20-day new high vs recent flat 100
	for i := 250; i < 299; i++ {
		closes[i] = 100
	}
	far := computeNewHigh(candleSeries(closes, nil), 1.6, 50, cfg)
	if far.NewHighScore > 35 {
		t.Errorf("far-from-high should be capped ≤35, got %.1f (dist=%.1f)",
			far.NewHighScore, far.DistanceFrom52wHighPct)
	}
	// Overextension dampener: all-green but RSI high and H250 true → ×0.6.
	hot := computeNewHigh(candleSeries(risingTo(300, 100, 200), nil), 1.6, 80, cfg)
	if hot.NewHighScore >= full.NewHighScore {
		t.Errorf("overextended score should be dampened below %.1f, got %.1f",
			full.NewHighScore, hot.NewHighScore)
	}
}

// 7. enable=false golden regression is structural (NewHighResult is isolated,
// pipeline never calls computeNewHigh). Here we assert purity: inputs unchanged.
func TestNewHighIsPure(t *testing.T) {
	cfg := testNHCfg()
	candles := candleSeries(risingTo(300, 100, 200), nil)
	before := candles[0].Close
	_ = computeNewHigh(candles, 1.0, 50, cfg)
	if candles[0].Close != before {
		t.Error("computeNewHigh mutated input candles")
	}
}

// 8. adjusted-close off → uses Close.
func TestNewHighUsesCloseWhenAdjOff(t *testing.T) {
	cfg := testNHCfg()                // UseAdjustedClose false by default
	closes := risingTo(300, 100, 99)  // today below prior high → NOT a new high
	adj := risingTo(300, 100, 999)    // adj would be a huge new high, must be ignored
	r := computeNewHigh(candleSeries(closes, adj), 1.0, 50, cfg)
	if r.H60 {
		t.Error("flag off must use Close (99 < prior 100) → no 60-day new high")
	}
}

// 9. nh adjusted-close on & valid → uses AdjClose.
func TestNewHighUsesAdjWhenOn(t *testing.T) {
	cfg := testNHCfg()
	cfg.UseAdjustedClose = true
	closes := risingTo(300, 100, 99) // today below prior high on Close → no new high
	adj := risingTo(300, 100, 200)   // adj breaks out
	r := computeNewHigh(candleSeries(closes, adj), 1.0, 50, cfg)
	if !r.H60 {
		t.Error("flag on with valid AdjClose should see the breakout")
	}
}

// 10. AdjClose invalid (<=0) with flag on → fallback Close.
func TestNewHighAdjInvalidFallbackClose(t *testing.T) {
	cfg := testNHCfg()
	cfg.UseAdjustedClose = true
	closes := risingTo(300, 100, 200) // close breaks out
	adj := risingTo(300, 0, 0)        // all invalid → fallback to Close
	r := computeNewHigh(candleSeries(closes, adj), 1.0, 50, cfg)
	if !r.H60 {
		t.Error("invalid AdjClose should fall back to Close and see the breakout")
	}
}

// 11. BreakoutWatch is purely distance-based (<=5% from 52w high), volume-independent.
func TestBreakoutWatchIsDistanceBased(t *testing.T) {
	cfg := testNHCfg()
	closes := risingTo(300, 100, 0)
	closes[150] = 200
	closes[299] = 198 // -1% → inside the 5% breakout band
	hiVol := computeNewHigh(candleSeries(closes, nil), 3.0, 50, cfg)
	loVol := computeNewHigh(candleSeries(closes, nil), 0.1, 50, cfg)
	if !hiVol.BreakoutWatch || !loVol.BreakoutWatch {
		t.Error("breakout_watch must be distance-only (<=5%), independent of volume")
	}
	closes[299] = 180 // -10% → outside the 5% band
	if r := computeNewHigh(candleSeries(closes, nil), 3.0, 50, cfg); r.BreakoutWatch {
		t.Error("-10% should be outside breakout band even with high volume")
	}
}
