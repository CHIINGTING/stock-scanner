package dumprisk

import (
	"math"
	"testing"
)

// A textbook strong-close bar: opens low, runs, closes on the high.
func strongBar() Input {
	return Input{Open: 100, High: 110, Low: 99, Close: 110, Volume: 5_000_000,
		PrevClose: 100, VolMA20: 1_000_000, ATR: 4}
}

func TestComputeGeometry(t *testing.T) {
	cases := []struct {
		name                         string
		in                           Input
		wantCLV, wantUpper, wantGive float64
	}{
		{
			name: "closed on the high — no upper shadow, nothing given back",
			in:   Input{Open: 100, High: 110, Low: 100, Close: 110, PrevClose: 100},
			// range 10, close==high → CLV 1, upper 0, giveback 0
			wantCLV: 1, wantUpper: 0, wantGive: 0,
		},
		{
			name:    "closed on the low — everything given back",
			in:      Input{Open: 110, High: 110, Low: 100, Close: 100, PrevClose: 100},
			wantCLV: 0, wantUpper: 0, wantGive: 1,
		},
		{
			name: "ran to 110, closed at 105 — half the range surrendered",
			in:   Input{Open: 100, High: 110, Low: 100, Close: 105, PrevClose: 100},
			// upper shadow = 110 - max(100,105) = 5 over range 10
			wantCLV: 0.5, wantUpper: 0.5, wantGive: 0.5,
		},
		{
			name: "close below open: upper shadow measures to the body, giveback to the close",
			in:   Input{Open: 108, High: 110, Low: 100, Close: 104, PrevClose: 100},
			// upper = 110-108 = 2 → 0.2 ; giveback = 110-104 = 6 → 0.6
			wantCLV: 0.4, wantUpper: 0.2, wantGive: 0.6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Compute(tc.in)
			if !f.Valid {
				t.Fatalf("bar should be valid, got %v", f.InvalidBy)
			}
			assertNear(t, "CLV", f.CloseLocationValue, tc.wantCLV)
			assertNear(t, "UpperShadowPct", f.UpperShadowPct, tc.wantUpper)
			assertNear(t, "GiveBackPct", f.GiveBackPct, tc.wantGive)
		})
	}
}

// A degenerate or malformed bar must never produce a number that looks like a reading.
func TestComputeRejectsUnusableBars(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{"High == Low (limit-locked)", Input{Open: 50, High: 50, Low: 50, Close: 50, PrevClose: 49}},
		{"High < Low", Input{Open: 50, High: 40, Low: 60, Close: 50, PrevClose: 49}},
		{"close escapes the range", Input{Open: 50, High: 55, Low: 45, Close: 60, PrevClose: 49}},
		{"NaN high", Input{Open: 50, High: math.NaN(), Low: 45, Close: 50, PrevClose: 49}},
		{"Inf low", Input{Open: 50, High: 55, Low: math.Inf(-1), Close: 50, PrevClose: 49}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := Compute(tc.in)
			if f.Valid {
				t.Fatalf("bar should be rejected, got Valid=true")
			}
			if f.InvalidBy == nil {
				t.Error("InvalidBy must say why")
			}
			for name, v := range map[string]float64{
				"CLV": f.CloseLocationValue, "upper": f.UpperShadowPct,
				"lower": f.LowerShadowPct, "body": f.BodyPct, "giveback": f.GiveBackPct,
			} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Errorf("%s = %v; an invalid bar must yield zero, never NaN/Inf", name, v)
				}
				if v != 0 {
					t.Errorf("%s = %v; geometry must stay zero on an invalid bar", name, v)
				}
			}
		})
	}
}

// MISSING ≠ ZERO: an absent input must set its OK flag false, not report a plausible zero.
func TestComputeMissingInputsAreNotZeros(t *testing.T) {
	t.Run("no previous close", func(t *testing.T) {
		in := strongBar()
		in.PrevClose = 0
		f := Compute(in)
		if f.ChangeOK {
			t.Error("ChangeOK must be false without a previous close")
		}
		if f.PriceChangePct != 0 {
			t.Errorf("PriceChangePct = %v; want the zero value guarded by ChangeOK", f.PriceChangePct)
		}
		if f.NearLimitUp {
			t.Error("NearLimitUp cannot be true when the move is unmeasurable")
		}
	})

	t.Run("no volume average", func(t *testing.T) {
		in := strongBar()
		in.VolMA20 = 0
		if f := Compute(in); f.VolumeOK || f.VolumeRatio != 0 {
			t.Errorf("VolumeOK=%v ratio=%v; want false/0", f.VolumeOK, f.VolumeRatio)
		}
	})

	t.Run("ATR warm-up", func(t *testing.T) {
		in := strongBar()
		in.ATR = 0
		f := Compute(in)
		if f.ATROK || f.PriceChangeATR != 0 {
			t.Errorf("ATROK=%v atr=%v; want false/0", f.ATROK, f.PriceChangeATR)
		}
		if !f.ChangeOK {
			t.Error("a missing ATR must not invalidate the percentage move")
		}
	})

	t.Run("no day-trading snapshot", func(t *testing.T) {
		in := strongBar() // HasDayTrading defaults false
		if f := Compute(in); f.DayTradingOK || f.DayTradingRatio != 0 {
			t.Errorf("DayTradingOK=%v ratio=%v; an absent archive must not read as 0%% day-trading",
				f.DayTradingOK, f.DayTradingRatio)
		}
	})
}

func TestDayTradingRatio(t *testing.T) {
	cases := []struct {
		name      string
		dayTrade  float64
		total     float64
		wantOK    bool
		wantRatio float64
	}{
		{"half the volume was day-traded", 500, 1000, true, 0.5},
		{"none", 0, 1000, true, 0},
		{"all of it", 1000, 1000, true, 1},
		{"zero total volume — undefined, not 0%", 0, 0, false, 0},
		{"day-trade exceeds total — a data fault, rejected not clamped", 1500, 1000, false, 0},
		{"negative shares — rejected", -5, 1000, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := strongBar()
			in.HasDayTrading = true
			in.DayTradeShares = tc.dayTrade
			in.TotalShares = tc.total
			f := Compute(in)
			if f.DayTradingOK != tc.wantOK {
				t.Fatalf("DayTradingOK = %v, want %v", f.DayTradingOK, tc.wantOK)
			}
			if f.DayTradingRatio != tc.wantRatio {
				t.Errorf("ratio = %v, want %v", f.DayTradingRatio, tc.wantRatio)
			}
		})
	}
}

func TestNearLimitUp(t *testing.T) {
	cases := []struct {
		close float64
		want  bool
	}{
		{109.97, true},  // +9.97% — a queue at the cap
		{109.50, true},  // exactly the cut, inclusive
		{109.49, false}, // just under
		{105.00, false},
	}
	for _, tc := range cases {
		in := Input{Open: 100, High: 110, Low: 100, Close: tc.close, PrevClose: 100}
		if got := Compute(in).NearLimitUp; got != tc.want {
			t.Errorf("close %.2f: NearLimitUp = %v, want %v", tc.close, got, tc.want)
		}
	}
}

func TestPriceChangeATR(t *testing.T) {
	// +4 points of move on an ATR of 4 is exactly one ATR, regardless of the percentage.
	in := Input{Open: 100, High: 105, Low: 99, Close: 104, PrevClose: 100, ATR: 4, VolMA20: 1}
	f := Compute(in)
	if !f.ATROK {
		t.Fatal("ATROK should be true")
	}
	assertNear(t, "PriceChangeATR", f.PriceChangeATR, 1.0)
	assertNear(t, "PriceChangePct", f.PriceChangePct, 4.0)
}

func assertNear(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
