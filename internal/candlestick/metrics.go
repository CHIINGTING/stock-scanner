package candlestick

import (
	"errors"
	"math"
)

// Validation sentinels. All are non-fatal to the caller: the analyzer skips geometry for a
// bar that fails validation (SPEC §4) — it never panics and never fabricates a pattern.
var (
	// ErrNonFinite: an OHLC value is NaN or ±Inf (malformed data).
	ErrNonFinite = errors.New("candlestick: non-finite OHLC")
	// ErrInconsistentOHLC: High < Low, or a body endpoint escapes the High/Low range.
	ErrInconsistentOHLC = errors.New("candlestick: inconsistent OHLC bounds")
	// ErrZeroRange: High == Low (a degenerate/flat bar, e.g. a limit-locked day). Structurally
	// legitimate but carries no shape → no geometry, and ratios would divide by zero.
	ErrZeroRange = errors.New("candlestick: zero range (degenerate bar)")
)

// Metrics is the pure, derived geometry input for one bar. Ratios are fractions of Range;
// because Metrics is only produced for a validated bar (Range > 0), the ratios never divide
// by zero and are always finite.
type Metrics struct {
	Open        float64
	High        float64
	Low         float64
	Close       float64
	Body        float64 // |Close-Open|
	Range       float64 // High-Low (> 0)
	UpperShadow float64 // High - max(Open,Close)  (>= 0)
	LowerShadow float64 // min(Open,Close) - Low    (>= 0)
	Bullish     bool    // Close > Open
	Bearish     bool    // Close < Open
	BodyPct     float64 // Body/Range
	UpperPct    float64 // UpperShadow/Range
	LowerPct    float64 // LowerShadow/Range
}

// Validate checks structural correctness of a raw candle. Order matters: non-finite first
// (so later comparisons are meaningful), then High/Low containment, then zero-range last (a
// flat bar is otherwise consistent). Returns a distinct sentinel per failure so callers can
// log/branch; all failures mean "no geometry for this bar".
func Validate(c Candle) error {
	for _, v := range []float64{c.Open, c.High, c.Low, c.Close} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ErrNonFinite
		}
	}
	if c.High < c.Low {
		return ErrInconsistentOHLC
	}
	if c.High < math.Max(c.Open, c.Close) || c.Low > math.Min(c.Open, c.Close) {
		return ErrInconsistentOHLC
	}
	if c.High == c.Low {
		return ErrZeroRange
	}
	return nil
}

// ComputeMetrics validates then derives Metrics for one candle. On any validation failure it
// returns the zero Metrics and the sentinel error (no NaN/Inf can leak into Metrics, and
// Range is guaranteed > 0 on success so all ratios are finite).
func ComputeMetrics(c Candle) (Metrics, error) {
	if err := Validate(c); err != nil {
		return Metrics{}, err
	}
	body := math.Abs(c.Close - c.Open)
	rng := c.High - c.Low // > 0, guaranteed by Validate
	upper := c.High - math.Max(c.Open, c.Close)
	lower := math.Min(c.Open, c.Close) - c.Low
	return Metrics{
		Open:        c.Open,
		High:        c.High,
		Low:         c.Low,
		Close:       c.Close,
		Body:        body,
		Range:       rng,
		UpperShadow: upper,
		LowerShadow: lower,
		Bullish:     c.Close > c.Open,
		Bearish:     c.Close < c.Open,
		BodyPct:     body / rng,
		UpperPct:    upper / rng,
		LowerPct:    lower / rng,
	}, nil
}
