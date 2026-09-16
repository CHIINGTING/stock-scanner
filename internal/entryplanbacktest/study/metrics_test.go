package study

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────────────────
// EP-9 §35 — METRICS, against SMALL HAND-COMPUTABLE FIXTURES WITH EXPLICIT EXPECTED NUMBERS.
//
// No test here compares a helper against itself or against a second implementation of the
// same formula. Every expected value below was computed by hand and is written as a literal,
// because a statistical helper checked only against its own arithmetic passes while computing
// the wrong statistic.
// ──────────────────────────────────────────────────────────────────────────────────────────

func TestSummariseOnAHandComputedOddSample(t *testing.T) {
	// x = 1, 2, 3, 4, 5  (N=5)
	//   mean   = 15/5 = 3
	//   median = the 3rd of 5 = 3
	//   p25    = nearest-rank ceil(0.25*5) = ceil(1.25) = 2 → the 2nd value = 2
	//   p75    = nearest-rank ceil(0.75*5) = ceil(3.75) = 4 → the 4th value = 4
	//   win    = 5 of 5 strictly > 0 = 100%
	d := Summarise([]float64{3, 1, 5, 2, 4}) // deliberately unsorted
	expect(t, "N", float64(d.N), 5)
	expect(t, "mean", d.Mean, 3)
	expect(t, "median", d.Median, 3)
	expect(t, "p25", d.P25, 2)
	expect(t, "p75", d.P75, 4)
	expect(t, "min", d.Min, 1)
	expect(t, "max", d.Max, 5)
	expect(t, "win rate", d.WinRate, 100)
}

func TestSummariseOnAHandComputedEvenSampleWithNegatives(t *testing.T) {
	// x = -4, -1, 2, 7  (N=4)
	//   mean   = 4/4 = 1
	//   median = (-1 + 2)/2 = 0.5
	//   p25    = ceil(0.25*4) = 1 → the 1st value = -4
	//   p75    = ceil(0.75*4) = 3 → the 3rd value = 2
	//   win    = 2 of 4 strictly > 0 = 50%
	d := Summarise([]float64{7, -4, 2, -1})
	expect(t, "N", float64(d.N), 4)
	expect(t, "mean", d.Mean, 1)
	expect(t, "median", d.Median, 0.5)
	expect(t, "p25", d.P25, -4)
	expect(t, "p75", d.P75, 2)
	expect(t, "win rate", d.WinRate, 50)
}

// TestZeroIsNotAWin pins the boundary: a flat trade is not a winner.
func TestZeroIsNotAWin(t *testing.T) {
	d := Summarise([]float64{0, 0, 1})
	expect(t, "win rate", d.WinRate, 100.0/3.0)
	if d.WinRate > 33.4 || d.WinRate < 33.3 {
		t.Fatalf("win rate %v — exactly 0 must not count as a win", d.WinRate)
	}
}

// TestTheMeanAloneCannotHideAnOutlier is the reason Dist has five fields: a sample whose mean
// is dragged by one value must show it in the median and the quartiles.
func TestTheMeanAloneCannotHideAnOutlier(t *testing.T) {
	// x = -1, -1, -1, -1, 1000 → mean 199.2, median -1, p25 -1, p75 -1, win 20%
	d := Summarise([]float64{-1, -1, -1, -1, 1000})
	expect(t, "mean", d.Mean, 199.2)
	expect(t, "median", d.Median, -1)
	expect(t, "p25", d.P25, -1)
	expect(t, "p75", d.P75, -1)
	expect(t, "win rate", d.WinRate, 20)
	if d.Mean <= 0 && d.Median <= 0 {
		return
	}
	if d.Mean > 0 && d.Median > 0 {
		t.Fatal("the fixture no longer distinguishes mean from median")
	}
}

func TestAnEmptySampleIsNotAZeroMean(t *testing.T) {
	d := Summarise(nil)
	if d.N != 0 {
		t.Fatalf("N %d", d.N)
	}
	// The zeroes are placeholders and the printers must key off N; this pins that N is the
	// thing that distinguishes them.
	if d.Mean != 0 || d.Median != 0 {
		t.Fatalf("an empty sample produced mean %v median %v", d.Mean, d.Median)
	}
}

func TestRateCarriesItsDenominatorAndRefusesToInventOne(t *testing.T) {
	r := NewRate(3, 12, "filled observations whose plan published an invalidation")
	expect(t, "pct", r.Pct, 25)
	if r.Denominator != 12 || r.Hits != 3 {
		t.Fatalf("rate %+v", r)
	}
	if r.Population == "" {
		t.Fatal("a rate with no named population is a rate nobody can audit")
	}
	empty := NewRate(0, 0, "nothing")
	if !math.IsNaN(empty.Pct) {
		t.Fatalf("an empty denominator gave %v, want NaN — 0.0%% would assert that nothing "+
			"was hit out of something", empty.Pct)
	}
}

// TestReturnsAreComputedFromTheFillPriceWithHandCheckedNumbers drives Measure end to end.
func TestReturnsAreComputedFromTheFillPriceWithHandCheckedNumbers(t *testing.T) {
	// 30 flat bars at 100, then set the three horizon bars to known closes.
	bars, _ := flatSeries(30, 100)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 80, SessionsWaited: 1}
	// Return@5 reads bar 1+5 = 6; @10 reads 11; @20 reads 21.
	bars[6].Close = 88   // 88/80 - 1 = +10%
	bars[11].Close = 72  // 72/80 - 1 = -10%
	bars[21].Close = 120 // 120/80 - 1 = +50%
	out := Measure(bars, f, nil, nil, nil)
	expect(t, "Return@5", out.Returns[5], 10)
	expect(t, "Return@10", out.Returns[10], -10)
	expect(t, "Return@20", out.Returns[20], 50)
}

// TestMFEAndMAEAreHandCheckedAndUnclipped.
func TestMFEAndMAEAreHandCheckedAndUnclipped(t *testing.T) {
	bars, _ := flatSeries(30, 100)
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100, SessionsWaited: 1}
	// Window is bars 2..21. Put the extremes inside it.
	bars[5].High = 130 // +30%
	bars[9].Low = 82   // -18%
	out := Measure(bars, f, nil, nil, nil)
	if out.MFE20 == nil || out.MAE20 == nil {
		t.Fatal("no excursion")
	}
	expect(t, "MFE20", *out.MFE20, 30)
	expect(t, "MAE20", *out.MAE20, -18)
	expect(t, "excursion bars", float64(out.ExcursionBars), 20)
}

// TestMFECanBeNegativeAndIsNotClippedAtZero is the divergence from internal/validator, stated
// as a number: a position that gapped down and never recovered has a NEGATIVE best case.
func TestMFECanBeNegativeAndIsNotClippedAtZero(t *testing.T) {
	bars, _ := flatSeries(30, 100)
	for i := 2; i <= 21; i++ {
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = 90, 92, 88, 90
	}
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100, SessionsWaited: 1}
	out := Measure(bars, f, nil, nil, nil)
	expect(t, "MFE20", *out.MFE20, -8)  // best High 92 → 92/100-1
	expect(t, "MAE20", *out.MAE20, -12) // worst Low 88 → 88/100-1
	if *out.MFE20 >= 0 {
		t.Fatal("MFE was clipped at 0, which would report a favourable excursion that never happened")
	}

	// And the mirror case, which is the one a zero-seeded MAE hides: a position that never
	// traded BELOW the fill has a POSITIVE worst case. Seeding mae at 0 reports -0.00 there,
	// i.e. an adverse excursion that never happened.
	up, _ := flatSeries(30, 100)
	for i := 2; i <= 21; i++ {
		up[i].Open, up[i].High, up[i].Low, up[i].Close = 110, 112, 108, 110
	}
	g := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100, SessionsWaited: 1}
	o2 := Measure(up, g, nil, nil, nil)
	expect(t, "MAE20 (never below the fill)", *o2.MAE20, 8)
	if *o2.MAE20 <= 0 {
		t.Fatalf("MAE20 = %v was clipped at 0; the position was never underwater and the "+
			"worst bar was +8%%", *o2.MAE20)
	}
	expect(t, "MFE20 (never below the fill)", *o2.MFE20, 12)
}

// TestLevelHitsUseTheirOwnDenominators — the §24 denominator-inflation guard, with numbers.
func TestLevelHitsUseTheirOwnDenominators(t *testing.T) {
	bars, s := flatSeries(30, 100)
	// Three observations, all filled at 100. Only two publish an invalidation; only one of
	// those is hit.
	mk := func(inv *float64, lowAt float64) Row {
		b := append([]fetcher.Candle(nil), bars...)
		b[5].Low = lowAt
		o := obsFor(b, s, s.dates[0], ptr(100.0), nil, inv, nil, nil)
		return Evaluate(o, ArmZoneLimit, 5)
	}
	rows := []Row{
		mk(ptr(95.0), 90), // invalidation published and hit
		mk(ptr(95.0), 99), // invalidation published, not hit
		mk(nil, 90),       // NO invalidation published: not in the denominator at all
	}
	m := Aggregate(ArmZoneLimit, 5, "", rows)
	if m.NFilled != 3 {
		t.Fatalf("NFilled %d, want 3", m.NFilled)
	}
	if m.InvalidationHitRate.Denominator != 2 {
		t.Fatalf("invalidation denominator %d, want 2 — using NFilled (3) would deflate the "+
			"rate with a plan that published no level", m.InvalidationHitRate.Denominator)
	}
	if m.InvalidationHitRate.Hits != 1 {
		t.Fatalf("invalidation hits %d, want 1", m.InvalidationHitRate.Hits)
	}
	expect(t, "invalidation hit rate", m.InvalidationHitRate.Pct, 50)
}

// TestAmbiguityIsCountedAndBoundedRatherThanResolved, with hand-checked bounds.
func TestAmbiguityIsCountedAndBoundedRatherThanResolved(t *testing.T) {
	bars, s := flatSeries(30, 100)
	mk := func(low, high float64) Row {
		b := append([]fetcher.Candle(nil), bars...)
		b[5].Low, b[5].High = low, high
		o := obsFor(b, s, s.dates[0], ptr(100.0), nil, ptr(95.0), ptr(110.0), nil)
		return Evaluate(o, ArmZoneLimit, 5)
	}
	rows := []Row{
		mk(90, 101), // stop only
		mk(99, 115), // target only
		mk(90, 115), // BOTH in one bar → ambiguous
		mk(99, 101), // neither
	}
	m := Aggregate(ArmZoneLimit, 5, "", rows)
	if m.FirstTouchStop != 1 || m.FirstTouchTarget != 1 || m.FirstTouchAmbiguous != 1 || m.FirstTouchNeither != 1 {
		t.Fatalf("first-touch counts stop=%d target=%d ambiguous=%d neither=%d, want 1/1/1/1",
			m.FirstTouchStop, m.FirstTouchTarget, m.FirstTouchAmbiguous, m.FirstTouchNeither)
	}
	// Conservative counts the ambiguous bar as a stop-first: 2 of 4 = 50%.
	expect(t, "stop-first conservative", m.StopFirstRateConservative.Pct, 50)
	// Optimistic counts none of them: 1 of 4 = 25%.
	expect(t, "stop-first optimistic", m.StopFirstRateOptimistic.Pct, 25)
	if m.StopFirstRateConservative.Pct == m.StopFirstRateOptimistic.Pct {
		t.Fatal("the two bounds collapsed — the ambiguity was resolved somewhere")
	}
	if m.StopFirstRateConservative.Denominator != 4 {
		t.Fatalf("bound denominator %d, want 4 (filled observations with BOTH levels)",
			m.StopFirstRateConservative.Denominator)
	}
}

func TestTheAmbiguousClassSurvivesIntoTheOutcome(t *testing.T) {
	bars, _ := flatSeries(30, 100)
	bars[5].Low, bars[5].High = 90, 115
	f := Fill{Outcome: FillFilled, BarIndex: 1, Price: 100, SessionsWaited: 1}
	out := Measure(bars, f, ptr(95.0), ptr(110.0), nil)
	if out.FirstTouch != entryplanbacktest.TouchAmbiguous {
		t.Fatalf("first touch %q, want %q", out.FirstTouch, entryplanbacktest.TouchAmbiguous)
	}
	if out.FirstTouchBar != 5 {
		t.Fatalf("first touch bar %d, want 5", out.FirstTouchBar)
	}
	// And both independent hit flags are true: the two facts are separate.
	if out.InvalidationHit == nil || !*out.InvalidationHit {
		t.Fatal("the invalidation was not recorded as touched")
	}
	if out.Target1Hit == nil || !*out.Target1Hit {
		t.Fatal("Target1 was not recorded as touched")
	}
}

func expect(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
