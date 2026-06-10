package r6backtest

import (
	"math"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// mkStock builds a Stock from explicit OHLC closes (open==close, low offset)
// starting at a fixed date, with precomputed series — mirrors LoadUniverse.
func mkStock(sym string, closes []float64) *Stock {
	s := &Stock{Symbol: sym, Name: sym, idxOf: map[string]int{}}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range closes {
		d := start.AddDate(0, 0, i)
		s.Candles = append(s.Candles, fetcher.Candle{
			Date: d, Open: c, High: c * 1.01, Low: c * 0.99, Close: c, Volume: 1000,
		})
		s.Close = append(s.Close, c)
		s.High = append(s.High, c*1.01)
		s.Low = append(s.Low, c*0.99)
		s.Vol = append(s.Vol, 1000)
		s.idxOf[dateKey(d)] = i
	}
	s.MA5 = sma(s.Close, 5)
	s.MA10 = sma(s.Close, 10)
	s.MA20 = sma(s.Close, 20)
	s.MA60 = sma(s.Close, 60)
	s.VolMA20 = sma(s.Vol, 20)
	return s
}

// fixedSetup triggers at exactly the bars in `at`.
type fixedSetup struct {
	at map[int]bool
}

func (fixedSetup) Name() string { return "FIXED" }
func (s fixedSetup) Detect(_ *Universe, _ *RSPanel, _ *Stock, i int, _ Params) *Trigger {
	if s.at[i] {
		return &Trigger{Bucket: 0, PullbackPct: 0}
	}
	return nil
}

func emptyPanel() *RSPanel { return &RSPanel{byDate: map[string]map[string]float64{}} }

// 1. entry at i+1 open, and forward returns at exact horizons.
func TestEntryAndForwardReturns(t *testing.T) {
	// 200 flat bars then a clean ramp so MA/warmup are defined; we trigger at i=150.
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100
	}
	// make entry bar (151) open = 100; then +1% per day after entry.
	for i := 151; i < 200; i++ {
		closes[i] = 100 * math.Pow(1.01, float64(i-150))
	}
	s := mkStock("T1", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = nil // isolate return math
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	tr := trades[0]
	// entry = open of bar 151. Open == close of that bar in mkStock.
	wantEntry := s.Candles[151].Open
	if math.Abs(tr.EntryPrice-wantEntry) > 1e-9 {
		t.Errorf("entry price: got %v want %v", tr.EntryPrice, wantEntry)
	}
	// 5d return = close[151+5]/entry - 1 (no stop → stop-adjusted == hold).
	want5 := (s.Close[156]/wantEntry - 1) * 100
	if math.Abs(tr.Return5d-want5) > 1e-6 {
		t.Errorf("5d return: got %v want %v", tr.Return5d, want5)
	}
	if math.Abs(tr.HoldReturn5d-want5) > 1e-6 {
		t.Errorf("5d hold return: got %v want %v", tr.HoldReturn5d, want5)
	}
	want20 := (s.Close[171]/wantEntry - 1) * 100
	if math.Abs(tr.Return20d-want20) > 1e-6 {
		t.Errorf("20d return: got %v want %v", tr.Return20d, want20)
	}
}

// 2. 60d horizon unavailable → NaN (empty), shorter horizons still recorded.
func TestHorizonUnavailable(t *testing.T) {
	closes := make([]float64, 130)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.1
	}
	s := mkStock("T2", closes)
	p := DefaultParams()
	p.Warmup = 100
	// trigger near the end: entry 126, +60 not available, +5/10 maybe not either.
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{123: true}}, p)
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	if !math.IsNaN(trades[0].Return60d) {
		t.Errorf("60d should be NaN when unavailable, got %v", trades[0].Return60d)
	}
	if math.IsNaN(trades[0].Return5d) {
		t.Errorf("5d should be available")
	}
}

// flat-then helper: 200 bars at 100, entry at bar 151 (==100), caller mutates tail.
func flatStock(sym string) []float64 {
	c := make([]float64, 200)
	for i := range c {
		c[i] = 100
	}
	return c
}

// 3 (stop semantics #1). Stop hit BEFORE 5d → return_5d uses the stop price,
// and stop_date/stop_price are recorded; hold_return_5d ignores the stop.
func TestStopBefore5d_UsesStopPrice(t *testing.T) {
	closes := flatStock("S1")
	// entry bar 151 = 100; bar 153 crashes to 85 (≤ -10% at bar 153, before 5d=156).
	for i := 152; i < 200; i++ {
		closes[i] = 85
	}
	closes[151] = 100 // ensure entry open = 100
	s := mkStock("S1", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = []string{"PCT_-10"}
	tr := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)[0]
	if !tr.HitStop || tr.StopReason != "PCT_-10" {
		t.Fatalf("expected PCT_-10 stop, got hit=%v reason=%q", tr.HitStop, tr.StopReason)
	}
	if tr.StopPrice != 85 || tr.StopDate.IsZero() {
		t.Errorf("stop_price/stop_date: got %v / %v", tr.StopPrice, tr.StopDate)
	}
	wantStopRet := (85.0/tr.EntryPrice - 1) * 100
	if math.Abs(tr.Return5d-wantStopRet) > 1e-9 {
		t.Errorf("return_5d should use stop price: got %v want %v", tr.Return5d, wantStopRet)
	}
	// hold ignores the stop → 5d close is also 85 here, but assert it equals hold math.
	wantHold := (s.Close[156]/tr.EntryPrice - 1) * 100
	if math.Abs(tr.HoldReturn5d-wantHold) > 1e-9 {
		t.Errorf("hold_return_5d should ignore stop: got %v want %v", tr.HoldReturn5d, wantHold)
	}
}

// stop semantics #2. Stop hit AFTER 5d but before 10d → return_5d uses the 5d
// close (no stop yet), return_10d uses the stop price.
func TestStopBetween5dAnd10d(t *testing.T) {
	closes := flatStock("S2")
	// hold flat (100) through bar 156 (5d), then crash at bar 158 (within 10d=161).
	for i := 158; i < 200; i++ {
		closes[i] = 80
	}
	s := mkStock("S2", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = []string{"PCT_-10"}
	tr := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)[0]
	// 5d (bar 156) is still 100 → ~0% (no stop yet).
	if math.Abs(tr.Return5d-0) > 1e-9 {
		t.Errorf("return_5d should be the 5d close (no stop yet): got %v", tr.Return5d)
	}
	// 10d uses stop price (80).
	wantStopRet := (80.0/tr.EntryPrice - 1) * 100
	if math.Abs(tr.Return10d-wantStopRet) > 1e-9 {
		t.Errorf("return_10d should use stop price: got %v want %v", tr.Return10d, wantStopRet)
	}
}

// stop semantics #3 & #4. No stop → return_Xd == hold close; hold always ignores stop.
func TestNoStop_ReturnEqualsHold(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100 + float64(i)*0.2 // steady rise, never triggers a -10% stop
	}
	s := mkStock("S3", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = []string{"PCT_-10"}
	tr := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)[0]
	if tr.HitStop {
		t.Fatalf("no stop expected")
	}
	for h, got := range map[int]float64{5: tr.Return5d, 20: tr.Return20d} {
		hold := (s.Close[151+h]/tr.EntryPrice - 1) * 100
		if math.Abs(got-hold) > 1e-9 {
			t.Errorf("h=%d no-stop return should equal hold: got %v want %v", h, got, hold)
		}
	}
}

// 4. no look-ahead: mutating bars AFTER the entry window does not change the
//    detection result or entry price (detector only reads <= i).
func TestNoLookAhead(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100 + float64(i%7)
	}
	s1 := mkStock("LA", closes)
	// s2 identical up to entry+1, then wildly different future.
	c2 := append([]float64(nil), closes...)
	for i := 160; i < 200; i++ {
		c2[i] = 999
	}
	s2 := mkStock("LA", c2)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = nil
	setup := fixedSetup{at: map[int]bool{150: true}}
	a := RunSetup(&Universe{Stocks: []*Stock{s1}}, emptyPanel(), setup, p)
	b := RunSetup(&Universe{Stocks: []*Stock{s2}}, emptyPanel(), setup, p)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("want 1 trade each, got %d/%d", len(a), len(b))
	}
	if a[0].EntryPrice != b[0].EntryPrice {
		t.Errorf("entry price must not depend on future bars: %v vs %v", a[0].EntryPrice, b[0].EntryPrice)
	}
	// 5d return uses bars within [151,156] (< 160), so must be identical.
	if a[0].Return5d != b[0].Return5d {
		t.Errorf("5d return changed by future-only mutation: %v vs %v", a[0].Return5d, b[0].Return5d)
	}
}

// 5. cooldown: triggering every bar yields entries spaced >= cooldown
//    (unless a fresh 20-day high resets the leg).
func TestCooldownDedup(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100 // perfectly flat → never a new 20-day high → cooldown always applies
	}
	s := mkStock("CD", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.Cooldown = 10
	p.StopRules = nil
	all := map[int]bool{}
	for i := 100; i < 190; i++ {
		all[i] = true
	}
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: all}, p)
	if len(trades) < 2 {
		t.Fatalf("expected several trades, got %d", len(trades))
	}
	// entries must be >= cooldown apart by entry date index.
	prev := -1000
	for _, tr := range trades {
		idx := s.idxOf[tr.EntryDate.Format("2006-01-02")] - 1 // detection bar = entryIdx-1
		if prev >= 0 && idx-prev < p.Cooldown {
			t.Errorf("entries closer than cooldown: %d then %d", prev, idx)
		}
		prev = idx
	}
}
