package r6backtest

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// sameF compares two returns treating NaN==NaN (unavailable horizon) as equal.
func sameF(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }

// withATR adds the ATR series mkStock omits, for stop tests.
func withATR(s *Stock) *Stock {
	s.ATR14 = indicator.ATR(s.High, s.Low, s.Close, 14)
	return s
}

// ramp builds a flat 200-bar series at 100 with entry bar 151 == 100; the caller
// mutates the tail. Returns the stock (RSI/ATR populated) and standard params.
func stopFixture(tailFrom int, tailVal float64) (*Stock, Params) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100
	}
	for i := tailFrom; i < 200; i++ {
		closes[i] = tailVal
	}
	s := withATR(withRSI(mkStock("STP", closes)))
	p := DefaultParams()
	p.Warmup = 100
	return s, p
}

// evalOnly runs a single entry at detection bar 150 through one policy and returns
// the trade (entry bar = 151).
func tradeWith(s *Stock, p Params, policy StopPolicy) Trade {
	eps := collectEntries(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)
	ep := eps[0]
	maxH := maxHorizon(p)
	end := ep.entryIdx + maxH
	if end > len(s.Candles)-1 {
		end = len(s.Candles) - 1
	}
	sr := policy.Eval(ep.s, ep.entryIdx, ep.entry, p, end)
	return *buildTrade("FIXED", emptyPanel(), ep, sr, maxH)
}

// 1. PCT policies fire at their exact thresholds.
func TestPctPolicies(t *testing.T) {
	s, p := stopFixture(152, 86) // -14% after entry
	if tr := tradeWith(s, p, pctStop{"PCT_ONLY_10", -10}); !tr.HitStop || tr.StopReason != "PCT_ONLY_10" {
		t.Errorf("PCT_ONLY_10 should fire on -14%%")
	}
	if tr := tradeWith(s, p, pctStop{"PCT_ONLY_15", -15}); tr.HitStop {
		t.Errorf("PCT_ONLY_15 should NOT fire on -14%%")
	}
	s2, p2 := stopFixture(152, 84) // -16%
	if tr := tradeWith(s2, p2, pctStop{"PCT_ONLY_15", -15}); !tr.HitStop {
		t.Errorf("PCT_ONLY_15 should fire on -16%%")
	}
}

// 2. ATR stop uses an entry-fixed level (ATR at the signal bar).
func TestATRStop(t *testing.T) {
	s, p := stopFixture(152, 90)
	sig := 150 // entryIdx-1
	atr := s.ATR14[sig]
	if atr <= 0 {
		t.Skip("flat series → ATR 0; skip")
	}
	// choose a drop just beyond entry-2*ATR.
	level := 100 - 2*atr
	for i := 152; i < 200; i++ {
		s.Close[i] = level - 0.5
		s.Low[i] = s.Close[i]
	}
	if tr := tradeWith(s, p, atrStop{"ATR_2", 2}); !tr.HitStop || tr.StopReason != "ATR_2" {
		t.Errorf("ATR_2 should fire below entry-2ATR (level=%.2f)", level)
	}
}

// 3. MA60_CONFIRM needs TWO consecutive closes below MA60 (single dip recovers).
func TestMA60Confirm(t *testing.T) {
	closes := make([]float64, 220)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.1 // rising → MA60 below price
	}
	s := withATR(withRSI(mkStock("MC", closes)))
	p := DefaultParams()
	p.Warmup = 100
	entryIdx := 151
	// single dip at 160 then recover → no stop.
	s.Close[160] = s.MA60[160] * 0.95
	s.Close[161] = s.MA60[161] * 1.05
	end := entryIdx + maxHorizon(p)
	if end > len(s.Candles)-1 {
		end = len(s.Candles) - 1
	}
	if sr := (ma60ConfirmStop{}).Eval(s, entryIdx, s.Candles[entryIdx].Open, p, end); sr.HitStop {
		t.Errorf("single dip below MA60 must not confirm-stop")
	}
	// two consecutive dips at 170,171 → stop on 171.
	s.Close[170] = s.MA60[170] * 0.95
	s.Close[171] = s.MA60[171] * 0.95
	if sr := (ma60ConfirmStop{}).Eval(s, entryIdx, s.Candles[entryIdx].Open, p, end); !sr.HitStop || sr.StopBar != 171 {
		t.Errorf("two consecutive closes below MA60 must stop on the 2nd (got hit=%v bar=%d)", sr.HitStop, sr.StopBar)
	}
}

// 4. NO_STOP never stops; stop-adjusted return == hold return.
func TestNoStopControl(t *testing.T) {
	s, p := stopFixture(152, 70) // big crash, but NO_STOP ignores it
	tr := tradeWith(s, p, noStop{})
	if tr.HitStop {
		t.Fatalf("NO_STOP must never stop")
	}
	if !sameF(tr.Return5d, tr.HoldReturn5d) || !sameF(tr.Return20d, tr.HoldReturn20d) || !sameF(tr.Return60d, tr.HoldReturn60d) {
		t.Errorf("NO_STOP stop-adjusted must equal hold: %v/%v/%v vs %v/%v/%v",
			tr.Return5d, tr.Return20d, tr.Return60d, tr.HoldReturn5d, tr.HoldReturn20d, tr.HoldReturn60d)
	}
}

// 5. SWING_LOW fires when close breaks the pre-entry swing low.
func TestSwingLowStop(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100
	}
	closes[145] = 95 // establishes a swing low ~95 within SwingLowback before entry
	for i := 152; i < 200; i++ {
		closes[i] = 90 // below the swing low
	}
	s := withATR(withRSI(mkStock("SL", closes)))
	p := DefaultParams()
	p.Warmup = 100
	tr := tradeWith(s, p, rulesStop{name: "SWING_LOW", rules: []string{"BREAK_SWING_LOW"}})
	if !tr.HitStop || tr.StopReason != "BREAK_SWING_LOW" {
		t.Errorf("SWING_LOW should fire when close breaks the pre-entry low")
	}
}

// 6. ATR stop is look-ahead safe: mutating bars AFTER the stop does not change it.
func TestATRStopNoLookAhead(t *testing.T) {
	s, p := stopFixture(152, 90)
	sig := 150
	atr := s.ATR14[sig]
	if atr <= 0 {
		t.Skip("ATR 0")
	}
	level := 100 - 2*atr
	for i := 152; i < 200; i++ {
		s.Close[i] = level - 0.5
		s.Low[i] = s.Close[i]
	}
	a := tradeWith(s, p, atrStop{"ATR_2", 2})
	// mutate far-future bars beyond the stop.
	for i := 170; i < 200; i++ {
		s.Close[i] = 9999
	}
	b := tradeWith(s, p, atrStop{"ATR_2", 2})
	if a.StopDate != b.StopDate || a.StopPrice != b.StopPrice {
		t.Errorf("ATR stop changed by post-stop future bars: %v/%v vs %v/%v",
			a.StopDate, a.StopPrice, b.StopDate, b.StopPrice)
	}
}

// 7. baseline policy set is correct and ordered (baseline first, NO_STOP last).
func TestBenchmarkPolicySet(t *testing.T) {
	ps := BenchmarkStopPolicies()
	if len(ps) != 9 {
		t.Fatalf("want 9 policies, got %d", len(ps))
	}
	if ps[0].Name() != "BASELINE" || ps[len(ps)-1].Name() != "NO_STOP" {
		t.Errorf("expected BASELINE first and NO_STOP last, got %s..%s", ps[0].Name(), ps[len(ps)-1].Name())
	}
}
