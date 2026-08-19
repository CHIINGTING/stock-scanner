package technical

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// The ATR must be the SAME Wilder ATR the scanner already uses for stops. A second one could
// disagree with the stop distance shown on the report.
func TestKeltnerReusesTheExistingATR(t *testing.T) {
	bars := trendBars(60, 100, 0.01)
	got := ComputeKeltner(bars, DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}

	highs := make([]float64, len(bars))
	lows := make([]float64, len(bars))
	closes := make([]float64, len(bars))
	for i, b := range bars {
		highs[i], lows[i], closes[i] = b.High, b.Low, b.Close
	}
	want := indicator.ATR(highs, lows, closes, 20)[len(bars)-1]
	if got.ATR != want {
		t.Errorf("ATR = %v, want indicator.ATR's %v — this layer must not recompute it", got.ATR, want)
	}
	// And the envelope must be exactly middle ± multiplier·ATR.
	if math.Abs((got.Upper-got.Middle)-2*want) > 1e-9 {
		t.Errorf("upper band is %v above the middle, want 2 x ATR = %v", got.Upper-got.Middle, 2*want)
	}
	if math.Abs((got.Middle-got.Lower)-2*want) > 1e-9 {
		t.Errorf("lower band is %v below the middle, want 2 x ATR = %v", got.Middle-got.Lower, 2*want)
	}
}

func TestKeltnerPositions(t *testing.T) {
	cfg := DefaultConfig()

	// A vertical move ends outside the upper band.
	up := ComputeKeltner(mkBars(append(rep(100, 40), 130, 160, 200)...), cfg)
	if up.Status != StatusOK {
		t.Fatalf("status = %s", up.Status)
	}
	if up.Position != PosAboveUpper || up.Channel != ChannelBreakout {
		t.Errorf("a vertical rally = %s / %s, want ABOVE_UPPER / BREAKOUT", up.Position, up.Channel)
	}

	// And the mirror image.
	down := ComputeKeltner(mkBars(append(rep(200, 40), 160, 130, 100)...), cfg)
	if down.Status != StatusOK {
		t.Fatalf("status = %s", down.Status)
	}
	if down.Position != PosBelowLower || down.Channel != ChannelBreakdown {
		t.Errorf("a vertical drop = %s / %s, want BELOW_LOWER / BREAKDOWN", down.Position, down.Channel)
	}

	// A quiet drift stays inside.
	inside := ComputeKeltner(trendBars(60, 100, 0.001), cfg)
	if inside.Channel != ChannelInside {
		t.Errorf("a quiet drift = %s, want INSIDE_CHANNEL", inside.Channel)
	}
	if inside.Position == PosAboveUpper || inside.Position == PosBelowLower {
		t.Errorf("an inside-channel reading cannot be %s", inside.Position)
	}
}

// A perfectly flat tape has zero ATR, which collapses the channel onto the EMA. Calling that
// a breakout would be an artefact of a degenerate divisor, not an observation.
func TestKeltnerZeroATR(t *testing.T) {
	got := ComputeKeltner(flatBars(60, 100), DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	if got.ATR != 0 {
		t.Fatalf("ATR = %v on a flat tape, want 0", got.ATR)
	}
	if !allFinite(got.Middle, got.Upper, got.Lower, got.WidthPct) {
		t.Fatalf("non-finite output with a zero ATR: %+v", got)
	}
	if got.Channel != ChannelInside || got.Position != PosMiddle {
		t.Errorf("zero ATR = %s / %s, want INSIDE_CHANNEL / MIDDLE", got.Channel, got.Position)
	}
	if got.WidthPct != 0 {
		t.Errorf("width = %v, want 0", got.WidthPct)
	}
}

// Width is expressed against the middle line so it is comparable across price levels.
func TestKeltnerWidthIsScaleFree(t *testing.T) {
	cheap := ComputeKeltner(trendBars(60, 20, 0.01), DefaultConfig())
	dear := ComputeKeltner(trendBars(60, 2000, 0.01), DefaultConfig())
	if cheap.Status != StatusOK || dear.Status != StatusOK {
		t.Fatalf("status = %s / %s", cheap.Status, dear.Status)
	}
	if math.Abs(cheap.WidthPct-dear.WidthPct) > 0.5 {
		t.Errorf("the same percentage path gave widths %v vs %v — width is not scale-free",
			cheap.WidthPct, dear.WidthPct)
	}
}

func TestKeltnerInsufficientAndInvalid(t *testing.T) {
	cfg := DefaultConfig()
	need := cfg.KeltnerATRPeriod + 1

	if got := ComputeKeltner(trendBars(need-1, 100, 0.01), cfg); got.Status != StatusInsufficientData {
		t.Errorf("%d bars → %s, want INSUFFICIENT_DATA", need-1, got.Status)
	}
	if got := ComputeKeltner(trendBars(need, 100, 0.01), cfg); got.Status != StatusOK {
		t.Errorf("%d bars → %s, want OK", need, got.Status)
	}
	if got := ComputeKeltner(nil, cfg); got.Status != StatusNoData {
		t.Errorf("no bars → %s, want NO_DATA", got.Status)
	}
	got := ComputeKeltner(trendBars(5, 100, 0.01), cfg)
	if got.Middle != 0 || got.Upper != 0 || got.Position != LocationUnknown {
		t.Errorf("an INSUFFICIENT_DATA result carried values: %+v", got)
	}
}

func TestKeltnerIsDeterministic(t *testing.T) {
	bars := trendBars(60, 100, 0.01)
	if ComputeKeltner(bars, DefaultConfig()) != ComputeKeltner(bars, DefaultConfig()) {
		t.Error("same input gave different results")
	}
}
