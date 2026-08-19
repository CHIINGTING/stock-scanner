// Package technical is the R14 technical-indicator expansion: ADX/DI, RSI, MACD, Keltner
// channel, classic pivot points, and an aggregator that turns them into a readable technical
// context.
//
// The governing rule, from which everything else follows:
//
//	Indicators are evidence, not decisions.
//
// Nothing here emits BUY / SELL / WATCH, and nothing here may be wired into a score, an
// action, a stage or a sort order. The outputs are Direction / TrendStrength / Momentum /
// Volatility / PriceLocation — descriptions of what the price series is doing. What to DO
// about that belongs to the scanner and, in R13's shadow layer, to the judge.
//
// Three properties are load-bearing and are asserted by tests rather than promised here:
//
//   - PURE. No clock, no randomness, no network, no database, no AI. The same bars always
//     produce the same result, which is what makes the R14 outcome validation meaningful.
//   - MISSING ≠ ZERO. Every indicator carries its own Status. An indicator that could not be
//     computed says INSUFFICIENT_DATA and leaves its numbers at zero as a formality; readers
//     must check Status, and the persistence layer writes no evidence for a non-OK result.
//   - NO NaN / Inf ESCAPES. Bars are validated on the way in and every division is guarded,
//     so nothing non-finite can reach JSON, HTML, SQLite or an agent prompt.
//
// Existing primitives are REUSED, never reimplemented: internal/indicator already provides
// Wilder RSI, Wilder ATR, SMA and (added for R14) EMA. This package computes only what did
// not already exist — the DI/ADX construction, the MACD assembly, the Keltner envelope, the
// pivot levels, and the interpretation on top.
package technical

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Bar is the OHLCV input contract. It is a plain value type on purpose: this package must
// not depend on the fetcher, the scanner, or any presentation layer, so callers convert
// their own candles into these — exactly as internal/candlestick already does.
type Bar struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// Status records how one indicator ended. The vocabulary matches internal/candlestick's
// (OK / NO_DATA / INVALID_BAR) rather than inventing a second one, plus the
// INSUFFICIENT_DATA case that a windowed indicator needs and a pattern matcher does not.
type Status string

const (
	// StatusOK — the value is usable.
	StatusOK Status = "OK"
	// StatusNoData — there were no bars at all.
	StatusNoData Status = "NO_DATA"
	// StatusInsufficientData — bars were valid but too few for this indicator's window.
	StatusInsufficientData Status = "INSUFFICIENT_DATA"
	// StatusInvalidBar — the series contains a bar that cannot be trusted (High < Low, a
	// non-positive or non-finite price, or an out-of-order / duplicated date).
	StatusInvalidBar Status = "INVALID_BAR"
)

// OK reports whether a status carries a usable reading.
func (s Status) OK() bool { return s == StatusOK }

// ── shared vocabularies ───────────────────────────────────────────────────────────────
//
// These are deliberately descriptive. None of them is an instruction, and none of them may
// be mapped to one inside this package.

// Direction of the prevailing move.
const (
	DirBullish = "BULLISH"
	DirBearish = "BEARISH"
	DirMixed   = "MIXED"
	DirNeutral = "NEUTRAL"
	DirUnknown = "UNKNOWN"
)

// TrendStrength — how directional the move is, NOT which way. A VERY_STRONG trend can be a
// downtrend; see ADXResult for why that distinction is enforced rather than assumed.
const (
	StrengthWeak       = "WEAK"
	StrengthForming    = "FORMING"
	StrengthStrong     = "STRONG"
	StrengthVeryStrong = "VERY_STRONG"
	StrengthUnknown    = "UNKNOWN"
)

// Momentum of the move.
const (
	MomentumAccelerating = "ACCELERATING"
	MomentumPositive     = "POSITIVE"
	MomentumNeutral      = "NEUTRAL"
	MomentumDecelerating = "DECELERATING"
	MomentumNegative     = "NEGATIVE"
	MomentumUnknown      = "UNKNOWN"
)

// Volatility regime.
const (
	VolContracting = "CONTRACTING"
	VolNormal      = "NORMAL"
	VolExpanding   = "EXPANDING"
	VolBreakout    = "BREAKOUT"
	VolBreakdown   = "BREAKDOWN"
	VolUnknown     = "UNKNOWN"
)

// PriceLocation relative to the pivot levels.
const (
	LocNearSupport     = "NEAR_SUPPORT"
	LocNearResistance  = "NEAR_RESISTANCE"
	LocAboveResistance = "ABOVE_RESISTANCE"
	LocBelowSupport    = "BELOW_SUPPORT"
	LocMidRange        = "MID_RANGE"
	LocationUnknown    = "UNKNOWN"
)

// Config holds every period and threshold R14 uses. They live in one struct so a parameter
// is never a magic number buried in a rule — the R14 validation work needs to be able to
// state exactly which parameters a result was produced under.
type Config struct {
	ADXPeriod int `yaml:"adx_period"`

	RSIPeriod int `yaml:"rsi_period"`

	MACDFast   int `yaml:"macd_fast"`
	MACDSlow   int `yaml:"macd_slow"`
	MACDSignal int `yaml:"macd_signal"`

	KeltnerEMAPeriod  int     `yaml:"keltner_ema_period"`
	KeltnerATRPeriod  int     `yaml:"keltner_atr_period"`
	KeltnerMultiplier float64 `yaml:"keltner_multiplier"`

	// PivotNearPct is how close (in percent) price must be to a pivot level to count as
	// "near" it. A tolerance, not a signal threshold.
	PivotNearPct float64 `yaml:"pivot_near_pct"`

	// DINeutralPct is how close +DI and -DI must be, as a percentage of their sum, before
	// the direction is called NEUTRAL rather than picked by a rounding error.
	DINeutralPct float64 `yaml:"di_neutral_pct"`
}

// DefaultConfig is the textbook baseline. Every one of these is a STARTING HYPOTHESIS to be
// tested by R14's outcome validation, not a calibrated value — the whole point of R14 is to
// find out which of them actually carry predictive weight on this market.
func DefaultConfig() Config {
	return Config{
		ADXPeriod:         14,
		RSIPeriod:         14,
		MACDFast:          12,
		MACDSlow:          26,
		MACDSignal:        9,
		KeltnerEMAPeriod:  20,
		KeltnerATRPeriod:  20,
		KeltnerMultiplier: 2.0,
		PivotNearPct:      1.0,
		DINeutralPct:      5.0,
	}
}

// Defaulted fills zero values from DefaultConfig, matching the convention the ai, market,
// institution and research configs already use.
func (c Config) Defaulted() Config {
	d := DefaultConfig()
	if c.ADXPeriod <= 0 {
		c.ADXPeriod = d.ADXPeriod
	}
	if c.RSIPeriod <= 0 {
		c.RSIPeriod = d.RSIPeriod
	}
	if c.MACDFast <= 0 {
		c.MACDFast = d.MACDFast
	}
	if c.MACDSlow <= 0 {
		c.MACDSlow = d.MACDSlow
	}
	if c.MACDSignal <= 0 {
		c.MACDSignal = d.MACDSignal
	}
	if c.KeltnerEMAPeriod <= 0 {
		c.KeltnerEMAPeriod = d.KeltnerEMAPeriod
	}
	if c.KeltnerATRPeriod <= 0 {
		c.KeltnerATRPeriod = d.KeltnerATRPeriod
	}
	if c.KeltnerMultiplier <= 0 {
		c.KeltnerMultiplier = d.KeltnerMultiplier
	}
	if c.PivotNearPct <= 0 {
		c.PivotNearPct = d.PivotNearPct
	}
	if c.DINeutralPct <= 0 {
		c.DINeutralPct = d.DINeutralPct
	}
	return c
}

// Validate reports the first configuration error.
//
// It checks in two passes, and the split matters. Actively-WRONG values are checked on the
// RAW config, because Defaulted() treats a non-positive number as "unset" and would quietly
// replace it — so a typo'd -1 multiplier would otherwise become a silent 2.0 rather than the
// error it is. Only after that does it check COHERENCE on the defaulted config, where an
// omitted field legitimately means "use the default".
func (c Config) Validate() error {
	for _, f := range []struct {
		name string
		v    int
	}{
		{"adx_period", c.ADXPeriod},
		{"rsi_period", c.RSIPeriod},
		{"macd_fast", c.MACDFast},
		{"macd_slow", c.MACDSlow},
		{"macd_signal", c.MACDSignal},
		{"keltner_ema_period", c.KeltnerEMAPeriod},
		{"keltner_atr_period", c.KeltnerATRPeriod},
	} {
		if f.v < 0 {
			return fmt.Errorf("technical: %s must not be negative, got %d", f.name, f.v)
		}
	}
	if c.KeltnerMultiplier < 0 || math.IsInf(c.KeltnerMultiplier, 0) || math.IsNaN(c.KeltnerMultiplier) {
		return fmt.Errorf("technical: keltner_multiplier must be a non-negative finite number, got %v",
			c.KeltnerMultiplier)
	}
	if c.PivotNearPct < 0 || c.PivotNearPct > 50 {
		return fmt.Errorf("technical: pivot_near_pct must be in [0, 50], got %v", c.PivotNearPct)
	}
	if c.DINeutralPct < 0 || c.DINeutralPct >= 100 {
		return fmt.Errorf("technical: di_neutral_pct must be in [0, 100), got %v", c.DINeutralPct)
	}

	d := c.Defaulted()
	if d.MACDFast >= d.MACDSlow {
		return fmt.Errorf("technical: macd_fast (%d) must be shorter than macd_slow (%d)",
			d.MACDFast, d.MACDSlow)
	}
	return nil
}

// Result is the whole R14 reading for one stock.
//
// Each field is nil when that indicator was not produced at all. A field that IS present but
// could not be computed carries its own non-OK Status — those are different facts, and the
// distinction is what stops "we did not look" from being read as "there was nothing there".
type Result struct {
	Config  Config         `json:"config"`
	ADX     *ADXResult     `json:"adx,omitempty"`
	RSI     *RSIResult     `json:"rsi,omitempty"`
	MACD    *MACDResult    `json:"macd,omitempty"`
	Keltner *KeltnerResult `json:"keltner,omitempty"`
	Pivot   *PivotResult   `json:"pivot,omitempty"`
	Context *Context       `json:"context,omitempty"`
}

// ── input validation ──────────────────────────────────────────────────────────────────

// errNoBars and errInvalidBars distinguish the two rejection reasons internally.
var (
	errNoBars      = errors.New("technical: no bars")
	errInvalidBars = errors.New("technical: invalid bars")
)

// series is the validated, column-wise view every indicator reads. Building it once per
// Analyze keeps the per-indicator code free of re-validation and guarantees they all agree
// about what the input was.
type series struct {
	opens  []float64
	highs  []float64
	lows   []float64
	closes []float64
	vols   []float64
	n      int
}

// newSeries validates bars and splits them into columns.
//
// It rejects, rather than repairs:
//
//   - no bars at all;
//   - any non-finite or non-positive price (a zero close is not a price, it is a gap in the
//     data, and silently keeping it would poison every ratio downstream);
//   - High < Low, or a High/Low that does not contain the Open/Close — a bar that cannot
//     have happened;
//   - dates that are duplicated or out of order, because every indicator here assumes the
//     series moves forward in time and none of them can detect that it does not.
//
// Repairing bad input would make the output non-reproducible from the input, which is
// exactly what R14's validation work cannot tolerate.
func newSeries(bars []Bar) (series, error) {
	if len(bars) == 0 {
		return series{}, errNoBars
	}
	s := series{
		opens:  make([]float64, len(bars)),
		highs:  make([]float64, len(bars)),
		lows:   make([]float64, len(bars)),
		closes: make([]float64, len(bars)),
		vols:   make([]float64, len(bars)),
		n:      len(bars),
	}
	var prevDate time.Time
	for i, b := range bars {
		if !finitePositive(b.Open) || !finitePositive(b.High) ||
			!finitePositive(b.Low) || !finitePositive(b.Close) {
			return series{}, errInvalidBars
		}
		if b.High < b.Low || b.High < b.Open || b.High < b.Close ||
			b.Low > b.Open || b.Low > b.Close {
			return series{}, errInvalidBars
		}
		if math.IsNaN(b.Volume) || math.IsInf(b.Volume, 0) || b.Volume < 0 {
			return series{}, errInvalidBars
		}
		if !b.Date.IsZero() {
			if !prevDate.IsZero() && !b.Date.After(prevDate) {
				return series{}, errInvalidBars // duplicate or out of order
			}
			prevDate = b.Date
		}
		s.opens[i], s.highs[i], s.lows[i], s.closes[i], s.vols[i] =
			b.Open, b.High, b.Low, b.Close, b.Volume
	}
	return s, nil
}

// statusFor maps a newSeries error onto the status an indicator should report.
func statusFor(err error) Status {
	switch {
	case errors.Is(err, errNoBars):
		return StatusNoData
	case errors.Is(err, errInvalidBars):
		return StatusInvalidBar
	default:
		return StatusInvalidBar
	}
}

// finitePositive is the price guard: NaN, ±Inf and non-positive values are all "not a price".
func finitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

// finite reports whether a computed value may be published. Every value this package emits
// passes through here, so a non-finite intermediate becomes a non-OK status rather than an
// Inf in a JSON payload or an agent prompt.
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// allFinite is the multi-value form, for results that publish several numbers together.
func allFinite(vs ...float64) bool {
	for _, v := range vs {
		if !finite(v) {
			return false
		}
	}
	return true
}

// pctDiff returns (a/b - 1) * 100, guarded. ok is false when b is not a usable denominator,
// so a division by zero can never become an Inf or a fabricated 0%.
func pctDiff(a, b float64) (float64, bool) {
	if !finite(a) || !finitePositive(b) {
		return 0, false
	}
	v := (a/b - 1) * 100
	if !finite(v) {
		return 0, false
	}
	return v, true
}

// safeDiv returns a/b guarded against a zero or non-finite denominator.
func safeDiv(a, b float64) (float64, bool) {
	if !finite(a) || !finite(b) || b == 0 {
		return 0, false
	}
	v := a / b
	if !finite(v) {
		return 0, false
	}
	return v, true
}
