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
	// 5d return = close[151+5]/entry - 1.
	want5 := (s.Close[156]/wantEntry - 1) * 100
	if math.Abs(tr.Exit5dReturn-want5) > 1e-6 {
		t.Errorf("5d return: got %v want %v", tr.Exit5dReturn, want5)
	}
	want20 := (s.Close[171]/wantEntry - 1) * 100
	if math.Abs(tr.Exit20dReturn-want20) > 1e-6 {
		t.Errorf("20d return: got %v want %v", tr.Exit20dReturn, want20)
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
	if !math.IsNaN(trades[0].Exit60dReturn) {
		t.Errorf("60d should be NaN when unavailable, got %v", trades[0].Exit60dReturn)
	}
	if math.IsNaN(trades[0].Exit5dReturn) {
		t.Errorf("5d should be available")
	}
}

// 3. stop hit: -10% rule fires and is reported.
func TestStopHit(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100
	}
	// entry bar (151) stays at 100; crash 15% strictly AFTER entry.
	for i := 152; i < 200; i++ {
		closes[i] = 85
	}
	s := mkStock("T3", closes)
	p := DefaultParams()
	p.Warmup = 100
	p.StopRules = []string{"PCT_-10"}
	trades := RunSetup(&Universe{Stocks: []*Stock{s}}, emptyPanel(), fixedSetup{at: map[int]bool{150: true}}, p)
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	if !trades[0].HitStop || trades[0].StopReason != "PCT_-10" {
		t.Errorf("expected PCT_-10 stop, got hit=%v reason=%q", trades[0].HitStop, trades[0].StopReason)
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
	if a[0].Exit5dReturn != b[0].Exit5dReturn {
		t.Errorf("5d return changed by future-only mutation: %v vs %v", a[0].Exit5dReturn, b[0].Exit5dReturn)
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
