package candlestick

// context.go — Layer 5: Context Builder. Produces a reusable MarketContext ONCE from the
// candle history (+ an injected Stage). Interpretation (Phase 4) must CONSUME this and never
// recompute trend / return / volume / near-high-low (SPEC §6).
//
// No look-ahead (SPEC lookback contract): the "latest" bar is candles[len-1]; the Near-high,
// Near-low, and average-volume windows are built from history STRICTLY BEFORE the latest bar
// (they never include it). Returns compare the latest close against a past close.
//
// Unknown ≠ false: when history is too short, the relevant capability flag (HasTrend /
// HasNearHigh / HasNearLow / HasVolumeRatio) is false and the boolean stays false, so
// Confidence (Phase 5) can tell "missing data" apart from "proven absent".

// Lookback windows (bars). All exclude the latest bar per the no-look-ahead contract.
const (
	ctxNearLookback = 20
	ctxVolLookback  = 20
	ctxReturn5      = 5
	ctxReturn10     = 10
)

// Stage is the (optional) externally-injected pipeline stage label (e.g. the scanner's
// RocketStage). The candlestick package never computes it — it is the ONLY context field the
// package does not derive itself. "" means unknown.
type Stage string

// MarketContext is the reusable market environment for the latest bar-position (SPEC §6).
type MarketContext struct {
	Stage Stage

	Uptrend     bool
	Downtrend   bool
	Near20DHigh bool
	Near20DLow  bool
	Return5D    float64
	Return10D   float64
	VolumeRatio float64

	// Capability flags — distinguish "unknown / insufficient history" from "proven false".
	HasTrend       bool
	HasNearHigh    bool
	HasNearLow     bool
	HasVolumeRatio bool
}

// Sufficient reports whether there is enough history to trust the trend read (the primary
// gate Confidence uses for the "insufficient context history" penalty).
func (c MarketContext) Sufficient() bool { return c.HasTrend }

// BuildContext derives the MarketContext for the latest bar of a chronological (oldest-first)
// candle history. stage is injected (may be ""). It never looks ahead and never panics on
// short history — it simply leaves the corresponding capability flags false.
func BuildContext(candles []Candle, stage Stage, th Thresholds) MarketContext {
	th = th.Defaulted()
	ctx := MarketContext{Stage: stage}
	n := len(candles)
	if n == 0 {
		return ctx
	}
	latest := candles[n-1]

	// Return5D (informational; latest close vs close 5 bars earlier).
	if n >= ctxReturn5+1 {
		if base := candles[n-1-ctxReturn5].Close; base > 0 {
			ctx.Return5D = (latest.Close - base) / base * 100
		}
	}

	// Return10D + trend classification.
	if n >= ctxReturn10+1 {
		if base := candles[n-1-ctxReturn10].Close; base > 0 {
			ctx.Return10D = (latest.Close - base) / base * 100
			ctx.HasTrend = true
			switch {
			case ctx.Return10D >= th.TrendReturnMin:
				ctx.Uptrend = true
			case ctx.Return10D <= -th.TrendReturnMin:
				ctx.Downtrend = true
			}
		}
	}

	// Near 20D high / low over the window STRICTLY BEFORE the latest bar (no look-ahead).
	if n >= ctxNearLookback+1 {
		win := candles[n-1-ctxNearLookback : n-1] // exactly the 20 bars before latest
		hi, lo := win[0].High, win[0].Low
		for _, c := range win {
			if c.High > hi {
				hi = c.High
			}
			if c.Low < lo {
				lo = c.Low
			}
		}
		if hi > 0 {
			ctx.HasNearHigh = true
			ctx.Near20DHigh = latest.Close >= hi*(1-th.NearPct/100)
		}
		if lo > 0 {
			ctx.HasNearLow = true
			ctx.Near20DLow = latest.Close <= lo*(1+th.NearPct/100)
		}
	}

	// Volume ratio vs the average of the window STRICTLY BEFORE the latest bar.
	if n >= ctxVolLookback+1 {
		win := candles[n-1-ctxVolLookback : n-1]
		var sum int64
		for _, c := range win {
			sum += c.Volume
		}
		if avg := float64(sum) / float64(len(win)); avg > 0 {
			ctx.HasVolumeRatio = true
			ctx.VolumeRatio = float64(latest.Volume) / avg
		}
	}

	return ctx
}
