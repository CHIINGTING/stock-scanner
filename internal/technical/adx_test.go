package technical

import (
	"math"
	"testing"
)

func TestADXInsufficientAndInvalidInput(t *testing.T) {
	cfg := DefaultConfig()

	if got := ComputeADX(nil, cfg); got.Status != StatusNoData {
		t.Errorf("no bars → %s, want NO_DATA", got.Status)
	}
	// 2*period is the true minimum: one window to smooth, one to average into the first ADX.
	if got := ComputeADX(trendBars(2*cfg.ADXPeriod-1, 100, 0.01), cfg); got.Status != StatusInsufficientData {
		t.Errorf("%d bars → %s, want INSUFFICIENT_DATA", 2*cfg.ADXPeriod-1, got.Status)
	}
	if got := ComputeADX(trendBars(2*cfg.ADXPeriod, 100, 0.01), cfg); got.Status != StatusOK {
		t.Errorf("%d bars → %s, want OK", 2*cfg.ADXPeriod, got.Status)
	}
	bad := trendBars(40, 100, 0.01)
	bad[10].High = bad[10].Low - 1
	if got := ComputeADX(bad, cfg); got.Status != StatusInvalidBar {
		t.Errorf("a bad bar → %s, want INVALID_BAR", got.Status)
	}
	// A non-OK result must not publish numbers that look like readings.
	got := ComputeADX(nil, cfg)
	if got.ADX != 0 || got.PlusDI != 0 || got.MinusDI != 0 {
		t.Errorf("a NO_DATA result carried values: %+v", got)
	}
	if got.Strength != StrengthUnknown {
		t.Errorf("strength = %s, want UNKNOWN when nothing was measured", got.Strength)
	}
}

func TestStrongUptrendIsStrongAndBullish(t *testing.T) {
	got := ComputeADX(trendBars(80, 100, 0.02), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Direction != DirBullish {
		t.Errorf("direction = %s, want BULLISH", got.Direction)
	}
	if got.PlusDI <= got.MinusDI {
		t.Errorf("+DI %v should exceed -DI %v in a relentless uptrend", got.PlusDI, got.MinusDI)
	}
	if got.Strength != StrengthStrong && got.Strength != StrengthVeryStrong {
		t.Errorf("strength = %s (ADX %v), want STRONG or better", got.Strength, got.ADX)
	}
}

// The reading the R14 spec singles out: a high ADX with -DI on top is a VERY STRONG
// DOWNTREND. Strength must never be mistaken for bullishness.
func TestStrongDowntrendIsVeryStrongAndBearish(t *testing.T) {
	got := ComputeADX(trendBars(80, 200, -0.02), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Direction != DirBearish {
		t.Errorf("direction = %s, want BEARISH", got.Direction)
	}
	if got.MinusDI <= got.PlusDI {
		t.Errorf("-DI %v should exceed +DI %v in a relentless downtrend", got.MinusDI, got.PlusDI)
	}
	if got.Strength != StrengthStrong && got.Strength != StrengthVeryStrong {
		t.Errorf("strength = %s (ADX %v), want STRONG or better", got.Strength, got.ADX)
	}
	// The whole point: a strong bearish trend is STRONG, not bullish.
	if got.Direction == DirBullish {
		t.Fatal("a downtrend was reported bullish")
	}
}

func TestSidewaysMarketIsWeak(t *testing.T) {
	// An oscillation with no net direction.
	closes := make([]float64, 90)
	for i := range closes {
		closes[i] = 100 + 2*math.Sin(float64(i)/2)
	}
	got := ComputeADX(mkBars(closes...), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.Strength != StrengthWeak && got.Strength != StrengthForming {
		t.Errorf("strength = %s (ADX %v), want WEAK or FORMING in a sideways tape", got.Strength, got.ADX)
	}
}

// A perfectly flat tape has no true range to divide by. It must report a reading, not a NaN.
func TestZeroVolatilityDoesNotDivideByZero(t *testing.T) {
	got := ComputeADX(flatBars(60, 100), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if !allFinite(got.ADX, got.PlusDI, got.MinusDI) {
		t.Fatalf("non-finite output on a flat tape: %+v", got)
	}
	if got.PlusDI != 0 || got.MinusDI != 0 {
		t.Errorf("a flat tape has no directional movement, got +DI %v / -DI %v", got.PlusDI, got.MinusDI)
	}
	if got.Direction != DirNeutral {
		t.Errorf("direction = %s, want NEUTRAL", got.Direction)
	}
	if got.Strength != StrengthWeak {
		t.Errorf("strength = %s, want WEAK", got.Strength)
	}
}

func TestADXStrengthBands(t *testing.T) {
	for _, tc := range []struct {
		adx  float64
		want string
	}{
		{0, StrengthWeak}, {19.99, StrengthWeak},
		{20, StrengthForming}, {24.99, StrengthForming},
		{25, StrengthStrong}, {39.99, StrengthStrong},
		{40, StrengthVeryStrong}, {100, StrengthVeryStrong},
	} {
		if got := adxStrength(tc.adx); got != tc.want {
			t.Errorf("adxStrength(%v) = %s, want %s", tc.adx, got, tc.want)
		}
	}
}

// The neutral band is proportional, so a near-tie is NEUTRAL at any DI magnitude rather than
// being decided by a rounding difference.
func TestDIDirectionNeutralBandIsProportional(t *testing.T) {
	// A 1-point gap is decisive at DI 8, noise at DI 45.
	if got := diDirection(8.5, 7.5, 5); got != DirBullish {
		t.Errorf("small DIs with a clear relative gap = %s, want BULLISH", got)
	}
	if got := diDirection(45.5, 44.5, 5); got != DirNeutral {
		t.Errorf("large DIs with the same absolute gap = %s, want NEUTRAL", got)
	}
	if got := diDirection(0, 0, 5); got != DirNeutral {
		t.Errorf("no directional movement = %s, want NEUTRAL", got)
	}
	if got := diDirection(10, 30, 5); got != DirBearish {
		t.Errorf("-DI dominant = %s, want BEARISH", got)
	}
}

func TestADXIsDeterministic(t *testing.T) {
	bars := trendBars(80, 100, 0.01)
	a := ComputeADX(bars, DefaultConfig())
	b := ComputeADX(bars, DefaultConfig())
	if a != b {
		t.Errorf("same input gave different results:\n%+v\n%+v", a, b)
	}
}
