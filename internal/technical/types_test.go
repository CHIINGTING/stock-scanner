package technical

import (
	"math"
	"testing"
	"time"
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// mkBars builds a synthetic series from closing prices, deriving a plausible OHLC around
// each close. It is the fixture builder every indicator test uses, so a bar that violates
// the input contract can never enter a test by accident.
func mkBars(closes ...float64) []Bar {
	bars := make([]Bar, len(closes))
	for i, c := range closes {
		prev := c
		if i > 0 {
			prev = closes[i-1]
		}
		high := math.Max(c, prev) * 1.01
		low := math.Min(c, prev) * 0.99
		bars[i] = Bar{
			Date: testStart.AddDate(0, 0, i),
			Open: prev, High: high, Low: low, Close: c, Volume: 1000,
		}
	}
	return bars
}

// trendBars compounds a drift so the percentage move per bar stays constant.
func trendBars(n int, start, drift float64) []Bar {
	closes := make([]float64, n)
	p := start
	for i := range closes {
		closes[i] = p
		p *= 1 + drift
	}
	return mkBars(closes...)
}

// rep repeats a price, for building a flat prologue inside a longer path. Paths are always
// assembled as one closes slice and passed to mkBars ONCE — appending two mkBars results
// would restart the date sequence and be rejected by the input contract, correctly.
func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// flatBars is a market with literally no movement — the degenerate case every indicator has
// to survive without dividing by zero.
func flatBars(n int, price float64) []Bar {
	bars := make([]Bar, n)
	for i := range bars {
		bars[i] = Bar{
			Date: testStart.AddDate(0, 0, i),
			Open: price, High: price, Low: price, Close: price, Volume: 1000,
		}
	}
	return bars
}

func TestDefaultConfigIsTheTextbookBaseline(t *testing.T) {
	c := DefaultConfig()
	if c.ADXPeriod != 14 || c.RSIPeriod != 14 {
		t.Errorf("ADX/RSI periods = %d/%d, want 14/14", c.ADXPeriod, c.RSIPeriod)
	}
	if c.MACDFast != 12 || c.MACDSlow != 26 || c.MACDSignal != 9 {
		t.Errorf("MACD = %d/%d/%d, want 12/26/9", c.MACDFast, c.MACDSlow, c.MACDSignal)
	}
	if c.KeltnerEMAPeriod != 20 || c.KeltnerATRPeriod != 20 || c.KeltnerMultiplier != 2 {
		t.Errorf("Keltner = EMA%d/ATR%d/x%v", c.KeltnerEMAPeriod, c.KeltnerATRPeriod, c.KeltnerMultiplier)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the default config must be valid: %v", err)
	}
}

func TestConfigDefaultedFillsOnlyZeros(t *testing.T) {
	got := Config{ADXPeriod: 7, KeltnerMultiplier: 1.5}.Defaulted()
	if got.ADXPeriod != 7 || got.KeltnerMultiplier != 1.5 {
		t.Errorf("Defaulted overwrote explicit values: %+v", got)
	}
	if got.RSIPeriod != 14 || got.MACDSlow != 26 {
		t.Errorf("Defaulted did not fill the zeros: %+v", got)
	}
}

func TestConfigValidateRejectsIncoherentSettings(t *testing.T) {
	for name, c := range map[string]Config{
		"fast >= slow":       {MACDFast: 26, MACDSlow: 26},
		"fast beyond slow":   {MACDFast: 30, MACDSlow: 26},
		"negative multiple":  {KeltnerMultiplier: -1},
		"absurd tolerance":   {PivotNearPct: 80},
		"di neutral too big": {DINeutralPct: 100},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	// A wholly zero config is valid, because Defaulted fills it.
	if err := (Config{}).Validate(); err != nil {
		t.Errorf("an empty config must default to a valid one: %v", err)
	}
}

// The input contract is a REJECTION contract: bad bars are refused, never repaired, so the
// output always reproduces from the input.
func TestNewSeriesRejectsRatherThanRepairs(t *testing.T) {
	good := Bar{Date: testStart, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 100}

	bad := map[string][]Bar{
		"no bars":        {},
		"zero close":     {{Date: testStart, Open: 10, High: 11, Low: 0, Close: 0, Volume: 1}},
		"negative price": {{Date: testStart, Open: -1, High: 11, Low: 9, Close: 10, Volume: 1}},
		"NaN close": {{Date: testStart, Open: 10, High: 11, Low: 9,
			Close: math.NaN(), Volume: 1}},
		"Inf high":       {{Date: testStart, Open: 10, High: math.Inf(1), Low: 9, Close: 10, Volume: 1}},
		"high below low": {{Date: testStart, Open: 10, High: 9, Low: 11, Close: 10, Volume: 1}},
		"close outside range": {{Date: testStart, Open: 10, High: 11, Low: 9,
			Close: 12, Volume: 1}},
		"negative volume": {{Date: testStart, Open: 10, High: 11, Low: 9, Close: 10, Volume: -5}},
		"NaN volume": {{Date: testStart, Open: 10, High: 11, Low: 9, Close: 10,
			Volume: math.NaN()}},
		"duplicate dates": {good, good},
		"out of order": {
			{Date: testStart.AddDate(0, 0, 1), Open: 10, High: 11, Low: 9, Close: 10, Volume: 1},
			good,
		},
	}
	for name, bars := range bad {
		if _, err := newSeries(bars); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}

	s, err := newSeries(mkBars(10, 11, 12))
	if err != nil {
		t.Fatalf("a clean series should be accepted: %v", err)
	}
	if s.n != 3 || s.closes[2] != 12 {
		t.Errorf("series = %+v", s)
	}
}

func TestStatusForMapsRejectionReasons(t *testing.T) {
	if _, err := newSeries(nil); statusFor(err) != StatusNoData {
		t.Error("no bars should map to NO_DATA")
	}
	_, err := newSeries([]Bar{{Close: 0}})
	if statusFor(err) != StatusInvalidBar {
		t.Error("a bad bar should map to INVALID_BAR")
	}
	if !StatusOK.OK() || StatusInsufficientData.OK() {
		t.Error("Status.OK is wrong")
	}
}

// Nothing non-finite may leave this package. These guards are the choke point.
func TestNumericalGuards(t *testing.T) {
	if _, ok := pctDiff(100, 0); ok {
		t.Error("a zero denominator must not yield a percentage")
	}
	if _, ok := pctDiff(math.NaN(), 100); ok {
		t.Error("a NaN numerator must not yield a percentage")
	}
	if v, ok := pctDiff(101, 100); !ok || math.Abs(v-1) > 1e-9 {
		t.Errorf("pctDiff(101,100) = %v (ok=%v), want 1", v, ok)
	}
	if _, ok := safeDiv(1, 0); ok {
		t.Error("division by zero must be refused")
	}
	if _, ok := safeDiv(math.Inf(1), 2); ok {
		t.Error("an infinite numerator must be refused")
	}
	if !allFinite(1, 2, 3) || allFinite(1, math.Inf(-1)) || allFinite(math.NaN()) {
		t.Error("allFinite is wrong")
	}
	if finitePositive(0) || finitePositive(-1) || finitePositive(math.NaN()) {
		t.Error("finitePositive accepted a non-price")
	}
}
