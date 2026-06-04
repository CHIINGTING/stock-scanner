package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

func testMFCfg() MomentumConfig { return momentumConfigFrom(Config{EnableMomentumFlow: true}) }

func mfLinear(n int, a, b float64) []float64 {
	s := make([]float64, n)
	if n == 1 {
		s[0] = b
		return s
	}
	for i := range s {
		s[i] = a + (b-a)*float64(i)/float64(n-1)
	}
	return s
}

func mfConcat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// mfCandles builds candles (adj == close) from closes with constant volume.
func mfCandles(closes []float64) []fetcher.Candle {
	return candlesFrom(closes, nil, constSlice(len(closes), 1000))
}

// 1. Momentum building: flat base then accelerating rise, RSI rising mid-low, up-volume.
func TestMomentumBuilding(t *testing.T) {
	closes := mfConcat(constSlice(44, 100), []float64{100.4, 101.0, 101.9, 103.1, 104.6, 106.4})
	rsi := constSlice(len(closes), 50)
	tail := []float64{50, 51, 52, 54, 56, 58}
	copy(rsi[len(rsi)-6:], tail)
	r := ComputeMomentum(mfCandles(closes), rsi, 1.0, testMFCfg())
	if r.Flow != MomentumBuilding {
		t.Errorf("want BUILDING, got %s (accel=%.5f score=%.1f)", r.Flow, r.SlopeAccel, r.Score)
	}
}

// 2. Momentum continuation: steady linear uptrend, near-zero acceleration.
func TestMomentumContinuation(t *testing.T) {
	closes := mfLinear(60, 100, 130)
	rsi := constSlice(len(closes), 58)
	r := ComputeMomentum(mfCandles(closes), rsi, 1.0, testMFCfg())
	if r.Flow != MomentumContinuation {
		t.Errorf("want CONTINUATION, got %s (accel=%.5f)", r.Flow, r.SlopeAccel)
	}
}

// 3. Momentum fading: uptrend then stalls/ticks down while still elevated.
func TestMomentumFading(t *testing.T) {
	closes := mfConcat(mfLinear(44, 100, 135), []float64{134, 133, 132.5, 132, 131.5, 131})
	rsi := constSlice(len(closes), 68)
	r := ComputeMomentum(mfCandles(closes), rsi, 1.0, testMFCfg())
	if r.Flow != MomentumFading {
		t.Errorf("want FADING, got %s (accel=%.5f div=%v)", r.Flow, r.SlopeAccel, r.Divergence)
	}
}

// 4. Structural shift up: price below key MA then reclaims it.
func TestMomentumShiftUp(t *testing.T) {
	closes := mfConcat(constSlice(5, 100), mfLinear(20, 100, 85), mfLinear(5, 85, 100))
	rsi := constSlice(len(closes), 48)
	r := ComputeMomentum(mfCandles(closes), rsi, 1.0, testMFCfg())
	if r.Flow != StructuralShiftUp {
		t.Errorf("want SHIFT_UP, got %s", r.Flow)
	}
}

// 5. Structural shift down: uptrend then loses key MA with negative 5-day return.
func TestMomentumShiftDown(t *testing.T) {
	closes := mfConcat(mfLinear(40, 100, 120), mfLinear(6, 118, 104))
	rsi := constSlice(len(closes), 45)
	r := ComputeMomentum(mfCandles(closes), rsi, 1.0, testMFCfg())
	if r.Flow != StructuralShiftDown {
		t.Errorf("want SHIFT_DOWN, got %s (ret5 path)", r.Flow)
	}
}

// 6. Limit-up lock guard: a higher-high low-volume final bar would be FADING via
// volume divergence, but a locked limit-up must NOT be classified FADING.
func TestMomentumLimitLockGuard(t *testing.T) {
	// rise (vol 2000) → pullback → final bar +9.5% (locked) with low volume.
	closes := mfConcat(mfLinear(30, 100, 120), mfLinear(4, 120, 116), []float64{127})
	vols := constSlice(len(closes), 2000)
	vols[len(vols)-1] = 400 // limit-up locked on shrinking volume
	candles := candlesFrom(closes, nil, vols)
	rsi := constSlice(len(closes), 60)

	locked := ComputeMomentum(candles, rsi, 0.5, testMFCfg()) // volRatio<1 → 漲停鎖量
	if locked.Flow == MomentumFading {
		t.Errorf("locked limit-up must not be FADING (量縮≠轉弱), got %s div=%v", locked.Flow, locked.Divergence)
	}
	notLocked := ComputeMomentum(candles, rsi, 2.0, testMFCfg()) // volRatio>=1 → not locked
	if !notLocked.Divergence {
		t.Errorf("expected volume divergence in this setup")
	}
	if notLocked.Flow != MomentumFading {
		t.Errorf("without the lock, divergence should make it FADING, got %s", notLocked.Flow)
	}
}

// 7. Priority: a bullish-looking trend WITH bearish divergence → FADING (not CONTINUATION).
func TestMomentumPriorityFadingOverContinuation(t *testing.T) {
	// Strong uptrend (so the pullback stays above the key MA → no reclaim), then a
	// higher high (132) on weak volume vs the prior high (130) → bearish divergence.
	closes := mfConcat(mfLinear(40, 100, 130), mfLinear(4, 130, 126), mfLinear(5, 126, 132))
	vols := constSlice(len(closes), 2000)
	for i := len(vols) - 5; i < len(vols); i++ {
		vols[i] = 700 // the second push is on weak volume
	}
	candles := candlesFrom(closes, nil, vols)
	rsi := constSlice(len(closes), 62)
	r := ComputeMomentum(candles, rsi, 1.0, testMFCfg())
	if r.Flow == MomentumContinuation {
		t.Errorf("divergence should take priority → not CONTINUATION")
	}
	if r.Flow != MomentumFading {
		t.Errorf("want FADING by priority, got %s (div=%v)", r.Flow, r.Divergence)
	}
}

// 8. Insufficient history → Computed=false, NEUTRAL.
func TestMomentumInsufficientHistory(t *testing.T) {
	closes := constSlice(20, 100)
	r := ComputeMomentum(mfCandles(closes), constSlice(20, 50), 1.0, testMFCfg())
	if r.Computed || r.Flow != MomentumNeutral {
		t.Errorf("20 bars (< min 30) → expected not computed NEUTRAL, got computed=%v flow=%s", r.Computed, r.Flow)
	}
}

// 9. Score is bounded and directional (building > 50 > shift-down).
func TestMomentumScoreDirection(t *testing.T) {
	build := ComputeMomentum(mfCandles(mfConcat(constSlice(44, 100),
		[]float64{100.4, 101, 101.9, 103.1, 104.6, 106.4})),
		func() []float64 { r := constSlice(50, 50); copy(r[44:], []float64{50, 51, 52, 54, 56, 58}); return r }(),
		1.0, testMFCfg())
	down := ComputeMomentum(mfCandles(mfConcat(mfLinear(40, 100, 120), mfLinear(6, 118, 104))),
		constSlice(46, 45), 1.0, testMFCfg())
	if build.Score < 0 || build.Score > 100 || down.Score < 0 || down.Score > 100 {
		t.Fatalf("score out of bounds: build=%.1f down=%.1f", build.Score, down.Score)
	}
	if !(build.Score > 50 && down.Score < 50) {
		t.Errorf("expected build>50>down, got build=%.1f down=%.1f", build.Score, down.Score)
	}
}

// accelPath is an accelerating rise → clearly positive SlopeAccel (a steady linear
// rise has ~0 accel, so it cannot discriminate Close vs AdjClose; this can).
func accelPath() []float64 {
	return mfConcat(constSlice(40, 100), []float64{101, 102.5, 104.5, 107, 110})
}

// 10. adjusted-close off → uses Close (SlopeAccel reflects Close path).
func TestMomentumUsesCloseWhenAdjOff(t *testing.T) {
	closes := accelPath()                  // accelerating on Close
	adj := constSlice(len(closes), 100)    // flat adj must be ignored
	r := ComputeMomentum(candlesFrom(closes, adj, constSlice(len(closes), 1000)), constSlice(len(closes), 50), 1.0, testMFCfg())
	if r.SlopeAccel <= 0.005 {
		t.Errorf("flag off must use Close (accelerating) → SlopeAccel>0.005, got %.5f", r.SlopeAccel)
	}
}

// 11. adjusted-close on & valid → uses AdjClose.
func TestMomentumUsesAdjWhenOn(t *testing.T) {
	cfg := testMFCfg()
	cfg.UseAdjustedClose = true
	adj := accelPath()                       // accelerating AdjClose
	closes := constSlice(len(adj), 100)      // flat Close
	r := ComputeMomentum(candlesFrom(closes, adj, constSlice(len(adj), 1000)), constSlice(len(adj), 50), 1.0, cfg)
	if r.SlopeAccel <= 0.005 {
		t.Errorf("flag on with valid AdjClose (accelerating) → SlopeAccel>0.005, got %.5f", r.SlopeAccel)
	}
}

// 12. AdjClose invalid (<=0) with flag on → fallback Close.
func TestMomentumAdjInvalidFallbackClose(t *testing.T) {
	cfg := testMFCfg()
	cfg.UseAdjustedClose = true
	closes := accelPath()                // accelerating Close
	adj := constSlice(len(closes), 0)    // invalid → fallback Close
	r := ComputeMomentum(candlesFrom(closes, adj, constSlice(len(closes), 1000)), constSlice(len(closes), 50), 1.0, cfg)
	if r.SlopeAccel <= 0.005 {
		t.Errorf("invalid AdjClose should fall back to Close (accelerating) → SlopeAccel>0.005, got %.5f", r.SlopeAccel)
	}
}

// 13. Pure & deterministic.
func TestMomentumIsPure(t *testing.T) {
	closes := mfLinear(60, 100, 130)
	candles := mfCandles(closes)
	before := candles[0].Close
	r1 := ComputeMomentum(candles, constSlice(60, 58), 1.0, testMFCfg())
	r2 := ComputeMomentum(candles, constSlice(60, 58), 1.0, testMFCfg())
	if candles[0].Close != before {
		t.Error("ComputeMomentum mutated input candles")
	}
	if r1.Flow != r2.Flow || r1.Score != r2.Score {
		t.Error("ComputeMomentum not deterministic")
	}
}
