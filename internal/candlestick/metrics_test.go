package candlestick

import (
	"errors"
	"math"
	"testing"
)

func TestValidateOK(t *testing.T) {
	c := Candle{Open: 10, High: 12, Low: 8, Close: 11}
	if err := Validate(c); err != nil {
		t.Fatalf("valid candle rejected: %v", err)
	}
}

func TestValidateNonFinite(t *testing.T) {
	cases := []Candle{
		{Open: math.NaN(), High: 12, Low: 8, Close: 11},
		{Open: 10, High: math.Inf(1), Low: 8, Close: 11},
		{Open: 10, High: 12, Low: math.Inf(-1), Close: 11},
		{Open: 10, High: 12, Low: 8, Close: math.NaN()},
	}
	for i, c := range cases {
		if err := Validate(c); !errors.Is(err, ErrNonFinite) {
			t.Errorf("case %d: want ErrNonFinite, got %v", i, err)
		}
	}
}

func TestValidateInconsistent(t *testing.T) {
	// High < Low
	if err := Validate(Candle{Open: 10, High: 5, Low: 9, Close: 8}); !errors.Is(err, ErrInconsistentOHLC) {
		t.Errorf("High<Low: want ErrInconsistentOHLC, got %v", err)
	}
	// body escapes High
	if err := Validate(Candle{Open: 20, High: 12, Low: 8, Close: 11}); !errors.Is(err, ErrInconsistentOHLC) {
		t.Errorf("open>high: want ErrInconsistentOHLC, got %v", err)
	}
	// body escapes Low
	if err := Validate(Candle{Open: 10, High: 12, Low: 8, Close: 7}); !errors.Is(err, ErrInconsistentOHLC) {
		t.Errorf("close<low: want ErrInconsistentOHLC, got %v", err)
	}
}

func TestValidateZeroRange(t *testing.T) {
	// flat/limit-locked bar: High == Low == Open == Close
	if err := Validate(Candle{Open: 10, High: 10, Low: 10, Close: 10}); !errors.Is(err, ErrZeroRange) {
		t.Errorf("zero-range: want ErrZeroRange, got %v", err)
	}
}

func TestComputeMetricsFormulas(t *testing.T) {
	// O=10 H=12 L=8 C=11 → body 1, range 4, upper 1 (12-11), lower 2 (10-8), bullish.
	m, err := ComputeMetrics(Candle{Open: 10, High: 12, Low: 8, Close: 11})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertF(t, "body", m.Body, 1)
	assertF(t, "range", m.Range, 4)
	assertF(t, "upper", m.UpperShadow, 1)
	assertF(t, "lower", m.LowerShadow, 2)
	assertF(t, "bodyPct", m.BodyPct, 0.25)
	assertF(t, "upperPct", m.UpperPct, 0.25)
	assertF(t, "lowerPct", m.LowerPct, 0.5)
	if !m.Bullish || m.Bearish {
		t.Errorf("expected bullish body")
	}
}

func TestComputeMetricsDojiBody(t *testing.T) {
	// Close == Open → body 0, neither bullish nor bearish.
	m, err := ComputeMetrics(Candle{Open: 10, High: 11, Low: 9, Close: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m.Body != 0 || m.Bullish || m.Bearish {
		t.Errorf("flat body: body=%v bull=%v bear=%v", m.Body, m.Bullish, m.Bearish)
	}
	assertF(t, "bodyPct", m.BodyPct, 0)
}

func TestComputeMetricsRejectsInvalid(t *testing.T) {
	// zero-range → error, zero Metrics (no NaN leaks).
	m, err := ComputeMetrics(Candle{Open: 10, High: 10, Low: 10, Close: 10})
	if !errors.Is(err, ErrZeroRange) {
		t.Fatalf("want ErrZeroRange, got %v", err)
	}
	if m != (Metrics{}) {
		t.Errorf("failed metrics must be zero value, got %+v", m)
	}
	// non-finite → error.
	if _, err := ComputeMetrics(Candle{Open: math.NaN(), High: 1, Low: 0, Close: 1}); !errors.Is(err, ErrNonFinite) {
		t.Errorf("want ErrNonFinite, got %v", err)
	}
}

func TestComputeMetricsRatiosFinite(t *testing.T) {
	// Hammer-shaped: tiny body at top, long lower shadow — all ratios finite, shadows >= 0.
	m, err := ComputeMetrics(Candle{Open: 11, High: 12, Low: 8, Close: 11.5})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{m.BodyPct, m.UpperPct, m.LowerPct} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("ratio not finite: %v", v)
		}
	}
	if m.UpperShadow < 0 || m.LowerShadow < 0 {
		t.Errorf("shadows must be >= 0: upper=%v lower=%v", m.UpperShadow, m.LowerShadow)
	}
	if m.LowerPct <= m.UpperPct {
		t.Errorf("expected dominant lower shadow")
	}
}

func assertF(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
