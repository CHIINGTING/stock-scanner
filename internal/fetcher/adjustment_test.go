package fetcher

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Tests for ScanAdjustments / LastAdjustmentAge.
//
// Every expected number here is derived BY HAND in the comment next to it and
// written as a literal. None of it was produced by running the code under test,
// which is the point: a fixture generated from the implementation would agree
// with any implementation, including a wrong one.

// day returns bar n's date, one calendar day per bar from a fixed Monday. Fixed
// so the tests carry no dependency on the clock.
func day(n int) time.Time {
	return time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// series builds candles from paired raw/adjusted closes. OHLV are left at zero:
// nothing in this file reads them, and filling them with plausible-looking
// numbers would only suggest they matter.
func series(t *testing.T, closes, adj []float64) []Candle {
	t.Helper()
	if len(closes) != len(adj) {
		t.Fatalf("fixture broken: %d closes vs %d adjusted", len(closes), len(adj))
	}
	out := make([]Candle, len(closes))
	for i := range closes {
		out[i] = Candle{Date: day(i), Close: closes[i], AdjClose: adj[i]}
	}
	return out
}

const eps = 1e-12

// TestScanAdjustmentsSingleDividend is the anchor case: one 5% ex-dividend gap,
// placed so the arithmetic can be checked without a calculator.
//
// Construction: the stock is economically FLAT across all six bars, then pays a
// dividend worth 5% of its price on bar 3.
//
//	raw Close  100 100 100 | 95 95 95     ← the quote drops by the dividend
//	AdjClose    95  95  95 | 95 95 95     ← the holder's value never changed
//
// Hand-derived expectations:
//
//	ratio r = Close/AdjClose : 100/95 = 1.0526315789473684 on bars 0..2, 1.0 on 3..5
//	the single step is at bar 3 (the ex-dividend bar itself)
//	GapPct  = (1.0/1.0526315789473684 - 1) * 100 = (0.95 - 1) * 100 = -5.0 exactly
//	          — the raw series fell 5% while the adjusted series fell 0%, so the
//	            entire move is mechanical
//	LastAdjustmentAge = 5 - 3 = 2 bars ago
func TestScanAdjustmentsSingleDividend(t *testing.T) {
	candles := series(t,
		[]float64{100, 100, 100, 95, 95, 95},
		[]float64{95, 95, 95, 95, 95, 95})

	got := ScanAdjustments(candles)

	if got.Status != AdjustmentOK {
		t.Fatalf("Status = %q, want %q", got.Status, AdjustmentOK)
	}
	if got.UsableBars != 6 || got.SkippedBars != 0 {
		t.Errorf("UsableBars/SkippedBars = %d/%d, want 6/0", got.UsableBars, got.SkippedBars)
	}
	if len(got.Events) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got.Events), got.Events)
	}
	ev := got.Events[0]
	if ev.Index != 3 {
		t.Errorf("Index = %d, want 3 (the ex-dividend bar)", ev.Index)
	}
	if !ev.Date.Equal(day(3)) {
		t.Errorf("Date = %s, want %s", ev.Date, day(3))
	}
	if math.Abs(ev.RatioBefore-1.0526315789473684) > eps {
		t.Errorf("RatioBefore = %.17g, want 100/95 = 1.0526315789473684", ev.RatioBefore)
	}
	if math.Abs(ev.RatioAfter-1) > eps {
		t.Errorf("RatioAfter = %.17g, want 1", ev.RatioAfter)
	}
	if math.Abs(ev.GapPct-(-5)) > 1e-9 {
		t.Errorf("GapPct = %.12f, want -5 (raw -5%%, adjusted 0%%)", ev.GapPct)
	}
	if ev.SpansSkippedBar {
		t.Error("SpansSkippedBar = true, want false (no bar was skipped)")
	}

	age, ok := LastAdjustmentAge(candles)
	if !ok || age != 2 {
		t.Errorf("LastAdjustmentAge = (%d, %v), want (2, true)", age, ok)
	}
}

// TestScanAdjustmentsEventPositions covers where an event can sit: immediately
// after the first bar, in the middle, and on the very last bar.
func TestScanAdjustmentsEventPositions(t *testing.T) {
	// Three dividends, each a clean factor, so every GapPct is exact by hand.
	//
	//	bar        0      1      2      3      4      5
	//	Close    110    100    100     98     98     49
	//	AdjClose  50     50     50     50     50     50
	//	ratio    2.2    2.0    2.0   1.96   1.96    0.98
	//
	// steps: bar 1 (2.0/2.2), bar 3 (1.96/2.0), bar 5 (0.98/1.96)
	//
	//	bar 1 GapPct = (2.0/2.2 - 1)*100  = (0.909090909090909 - 1)*100 = -9.09090909090909
	//	bar 3 GapPct = (1.96/2.0 - 1)*100 = (0.98 - 1)*100              = -2.0
	//	bar 5 GapPct = (0.98/1.96 - 1)*100= (0.5 - 1)*100               = -50.0  (a 2:1 split)
	candles := series(t,
		[]float64{110, 100, 100, 98, 98, 49},
		[]float64{50, 50, 50, 50, 50, 50})

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentOK {
		t.Fatalf("Status = %q, want %q", got.Status, AdjustmentOK)
	}

	wantEvents := []struct {
		index  int
		gapPct float64
	}{
		{1, -9.09090909090909},
		{3, -2.0},
		{5, -50.0},
	}
	if len(got.Events) != len(wantEvents) {
		t.Fatalf("got %d events, want %d: %+v", len(got.Events), len(wantEvents), got.Events)
	}
	for i, want := range wantEvents {
		ev := got.Events[i]
		if ev.Index != want.index {
			t.Errorf("event %d: Index = %d, want %d", i, ev.Index, want.index)
		}
		if !ev.Date.Equal(day(want.index)) {
			t.Errorf("event %d: Date = %s, want %s", i, ev.Date, day(want.index))
		}
		if math.Abs(ev.GapPct-want.gapPct) > 1e-9 {
			t.Errorf("event %d: GapPct = %.12f, want %.12f", i, ev.GapPct, want.gapPct)
		}
	}

	// The newest bar IS the adjustment → age 0. This is the case that must not be
	// confused with "unknown", which also carries barsAgo 0 but ok=false.
	age, ok := LastAdjustmentAge(candles)
	if !ok || age != 0 {
		t.Errorf("LastAdjustmentAge = (%d, %v), want (0, true) — newest bar is the event", age, ok)
	}
}

// TestScanAdjustmentsSmallGaps pins the SMALL end of the detection range, which
// the round-number fixtures above do not exercise. Real Taiwan dividends are
// often well under 1%: over .cache the 10th percentile of |GapPct| is 0.79% and
// the smallest real event is 0.0075% (symbol 8932, 2024-10-28) — while 0050's own
// July 2026 distribution is only -0.60%. Since the measured noise band ends at
// 2.9e-07 (0.000029%), every one of these is far above noise and must be found.
//
// Each case is economically flat with a dividend worth d of the price, so
// GapPct = -d exactly:
//
//	Close 100 → 99.5   AdjClose 99.5 throughout   GapPct = (0.995 - 1)*100 = -0.5%
//	Close 100 → 99.95  AdjClose 99.95 throughout  GapPct = (0.9995 - 1)*100 = -0.05%
func TestScanAdjustmentsSmallGaps(t *testing.T) {
	tests := []struct {
		name       string
		before     float64
		after      float64
		wantGapPct float64
	}{
		{name: "0.5% dividend", before: 100, after: 99.5, wantGapPct: -0.5},
		{name: "0.05% dividend", before: 100, after: 99.95, wantGapPct: -0.05},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candles := series(t,
				[]float64{tc.before, tc.before, tc.after, tc.after},
				[]float64{tc.after, tc.after, tc.after, tc.after})
			got := ScanAdjustments(candles)
			if got.Status != AdjustmentOK {
				t.Fatalf("Status = %q, want %q", got.Status, AdjustmentOK)
			}
			if len(got.Events) != 1 {
				t.Fatalf("got %d events, want 1 — a sub-1%% dividend is still an adjustment: %+v",
					len(got.Events), got.Events)
			}
			ev := got.Events[0]
			if ev.Index != 2 {
				t.Errorf("Index = %d, want 2", ev.Index)
			}
			if math.Abs(ev.GapPct-tc.wantGapPct) > 1e-9 {
				t.Errorf("GapPct = %.12f, want %.12f", ev.GapPct, tc.wantGapPct)
			}
			if age, ok := LastAdjustmentAge(candles); !ok || age != 1 {
				t.Errorf("LastAdjustmentAge = (%d, %v), want (1, true)", age, ok)
			}
		})
	}
}

// TestScanAdjustmentsNoAdjustedSeries is the semantic core of this work item:
// ratio flat at 1.0 must NOT read as "this stock never paid a dividend", because
// yahoo.go fills AdjClose from Close when the source ships no adjusted array, and
// the two cases are then indistinguishable.
func TestScanAdjustmentsNoAdjustedSeries(t *testing.T) {
	// Prices move; AdjClose is an exact copy of Close on every bar, which is what
	// the yahoo.go fallback produces.
	closes := []float64{31, 32.5, 30, 30, 33.25, 40}
	candles := series(t, closes, closes)

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentNoAdjustedSeries {
		t.Fatalf("Status = %q, want %q — a flat 1.0 ratio is UNKNOWN, not OK/no-events",
			got.Status, AdjustmentNoAdjustedSeries)
	}
	if len(got.Events) != 0 {
		t.Errorf("Events = %+v, want empty", got.Events)
	}
	if got.UsableBars != 6 || got.SkippedBars != 0 {
		t.Errorf("UsableBars/SkippedBars = %d/%d, want 6/0", got.UsableBars, got.SkippedBars)
	}

	// A caller must be forced to branch here; returning (0, true) would let it
	// read "an adjustment on the newest bar", and returning any age at all would
	// let it read "no adjustment", which we cannot support.
	age, ok := LastAdjustmentAge(candles)
	if ok {
		t.Errorf("LastAdjustmentAge = (%d, true), want ok=false — undetectable, not absent", age)
	}
	if age != 0 {
		t.Errorf("barsAgo = %d, want the zero value when ok=false", age)
	}
}

// TestScanAdjustmentsOKWithNoEvents is the other half of the distinction: a real
// adjusted series (ratio away from 1.0) that simply did not adjust inside the
// window. Status is OK and an empty Events list is then a genuine answer — but
// LastAdjustmentAge still has nothing to report.
func TestScanAdjustmentsOKWithNoEvents(t *testing.T) {
	// Ratio is a constant 1.05 (a dividend that happened BEFORE this window), so
	// AdjClose is demonstrably not a copy of Close.
	candles := series(t,
		[]float64{105, 105, 105, 105},
		[]float64{100, 100, 100, 100})

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentOK {
		t.Fatalf("Status = %q, want %q — ratio != 1.0 proves an adjusted series exists",
			got.Status, AdjustmentOK)
	}
	if len(got.Events) != 0 {
		t.Errorf("Events = %+v, want empty", got.Events)
	}
	if age, ok := LastAdjustmentAge(candles); ok {
		t.Errorf("LastAdjustmentAge = (%d, true), want ok=false — no event to date", age)
	}
}

// TestScanAdjustmentsInsufficientBars checks the arithmetic floor: one ratio
// needs two usable bars.
func TestScanAdjustmentsInsufficientBars(t *testing.T) {
	tests := []struct {
		name        string
		candles     []Candle
		wantUsable  int
		wantSkipped int
	}{
		{name: "nil slice", candles: nil},
		{name: "empty slice", candles: []Candle{}},
		{
			name:       "one bar",
			candles:    series(t, []float64{95}, []float64{90}),
			wantUsable: 1,
		},
		{
			// Two bars, but one has no usable price → one ratio only, nothing to
			// compare it against. Must not silently report "no adjustment".
			name:        "two bars, one unusable",
			candles:     series(t, []float64{95, 0}, []float64{90, 90}),
			wantUsable:  1,
			wantSkipped: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanAdjustments(tc.candles)
			if got.Status != AdjustmentInsufficientBars {
				t.Errorf("Status = %q, want %q", got.Status, AdjustmentInsufficientBars)
			}
			if len(got.Events) != 0 {
				t.Errorf("Events = %+v, want empty", got.Events)
			}
			if got.UsableBars != tc.wantUsable || got.SkippedBars != tc.wantSkipped {
				t.Errorf("UsableBars/SkippedBars = %d/%d, want %d/%d",
					got.UsableBars, got.SkippedBars, tc.wantUsable, tc.wantSkipped)
			}
			if age, ok := LastAdjustmentAge(tc.candles); ok {
				t.Errorf("LastAdjustmentAge = (%d, true), want ok=false", age)
			}
		})
	}
}

// TestScanAdjustmentsUnusableBars pins what a missing price does. A bar with no
// price cannot be evidence of anything: it must neither invent an event (its zero
// would otherwise look like an enormous ratio step) nor be quietly counted as an
// unchanged bar.
func TestScanAdjustmentsUnusableBars(t *testing.T) {
	t.Run("zeros inside a flat series invent no event", func(t *testing.T) {
		// AdjClose copies Close, with a Close=0 hole at bar 2 and an AdjClose=0
		// hole at bar 4. A naive scan would see ratios 0 and +Inf and report up to
		// four "events".
		candles := []Candle{
			{Date: day(0), Close: 20, AdjClose: 20},
			{Date: day(1), Close: 21, AdjClose: 21},
			{Date: day(2), Close: 0, AdjClose: 21},
			{Date: day(3), Close: 22, AdjClose: 22},
			{Date: day(4), Close: 23, AdjClose: 0},
			{Date: day(5), Close: 24, AdjClose: 24},
		}
		got := ScanAdjustments(candles)
		if len(got.Events) != 0 {
			t.Errorf("Events = %+v, want empty — a missing price is not an adjustment", got.Events)
		}
		if got.Status != AdjustmentNoAdjustedSeries {
			t.Errorf("Status = %q, want %q", got.Status, AdjustmentNoAdjustedSeries)
		}
		if got.UsableBars != 4 || got.SkippedBars != 2 {
			t.Errorf("UsableBars/SkippedBars = %d/%d, want 4/2 — the skips must be reported, not absorbed",
				got.UsableBars, got.SkippedBars)
		}
		if got.UsableBars+got.SkippedBars != len(candles) {
			t.Errorf("UsableBars+SkippedBars = %d, want len(candles) = %d",
				got.UsableBars+got.SkippedBars, len(candles))
		}
	})

	t.Run("negative prices are unusable too", func(t *testing.T) {
		candles := []Candle{
			{Date: day(0), Close: 20, AdjClose: 20},
			{Date: day(1), Close: -1, AdjClose: 20},
			{Date: day(2), Close: 20, AdjClose: -1},
			{Date: day(3), Close: 20, AdjClose: 20},
		}
		got := ScanAdjustments(candles)
		if len(got.Events) != 0 {
			t.Errorf("Events = %+v, want empty", got.Events)
		}
		if got.UsableBars != 2 || got.SkippedBars != 2 {
			t.Errorf("UsableBars/SkippedBars = %d/%d, want 2/2", got.UsableBars, got.SkippedBars)
		}
	})

	t.Run("real event across a hole is flagged, not duplicated", func(t *testing.T) {
		// A 5% dividend as in the anchor test, with bar 3 (the ex-dividend bar)
		// unpriced. The step is only visible between bar 2 and bar 4, so it is
		// reported at bar 4 with SpansSkippedBar set: the adjustment is real, but
		// it may have taken effect on bar 3.
		//
		//	bar        0      1      2     3      4     5
		//	Close    100    100    100     0     95    95
		//	AdjClose  95     95     95     0     95    95
		//	ratio  1.0526 1.0526 1.0526   n/a   1.0   1.0
		//
		// GapPct = (1.0/1.0526315789473684 - 1)*100 = -5.0
		candles := []Candle{
			{Date: day(0), Close: 100, AdjClose: 95},
			{Date: day(1), Close: 100, AdjClose: 95},
			{Date: day(2), Close: 100, AdjClose: 95},
			{Date: day(3), Close: 0, AdjClose: 0},
			{Date: day(4), Close: 95, AdjClose: 95},
			{Date: day(5), Close: 95, AdjClose: 95},
		}
		got := ScanAdjustments(candles)
		if got.Status != AdjustmentOK {
			t.Fatalf("Status = %q, want %q", got.Status, AdjustmentOK)
		}
		if len(got.Events) != 1 {
			t.Fatalf("got %d events, want exactly 1: %+v", len(got.Events), got.Events)
		}
		ev := got.Events[0]
		if ev.Index != 4 {
			t.Errorf("Index = %d, want 4 (first bar on the new basis that has a price)", ev.Index)
		}
		if !ev.SpansSkippedBar {
			t.Error("SpansSkippedBar = false, want true — bar 3 was skipped, so the date is approximate")
		}
		if math.Abs(ev.GapPct-(-5)) > 1e-9 {
			t.Errorf("GapPct = %.12f, want -5", ev.GapPct)
		}
		// Age is counted in input-slice bars and therefore spans the hole: 5 - 4 = 1.
		if age, ok := LastAdjustmentAge(candles); !ok || age != 1 {
			t.Errorf("LastAdjustmentAge = (%d, %v), want (1, true)", age, ok)
		}
	})
}

// TestScanAdjustmentsFloatNoise is the threshold's regression test. The series is
// built the way real noise arises: values accumulated (a running mean, so the sum
// carries drift) and then rounded to float32, which is the precision Yahoo ships.
// The ratio Close/AdjClose is mathematically the constant 1.03 on every bar, so
// the CORRECT answer is zero events; the float representation jitters in the last
// bits and a threshold set too tight would report an event on nearly every bar.
func TestScanAdjustmentsFloatNoise(t *testing.T) {
	const bars = 400
	candles := make([]Candle, bars)
	sum := 0.0
	for i := 0; i < bars; i++ {
		// A drifting but bounded price path: accumulate, then average. The
		// accumulation is deliberate — it is what makes the value carry rounding
		// error rather than being a clean literal.
		sum += 0.1 * float64(i%7+1)
		price := sum / float64(i+1) * 100
		candles[i] = Candle{
			Date: day(i),
			// Both series independently rounded to float32, exactly as the upstream
			// JSON delivers them (cached values look like 45.474998474121094).
			Close:    float64(float32(price)),
			AdjClose: float64(float32(price / 1.03)),
		}
	}

	// Guard against a vacuous pass: confirm the fixture really does jitter, and
	// by a margin within an order of magnitude of the threshold. If this ever
	// drops to ~0 the test has stopped testing anything.
	maxDelta := 0.0
	for i := 1; i < bars; i++ {
		prev := candles[i-1].Close / candles[i-1].AdjClose
		cur := candles[i].Close / candles[i].AdjClose
		if d := math.Abs(cur/prev - 1); d > maxDelta {
			maxDelta = d
		}
	}
	if maxDelta < 1e-8 {
		t.Fatalf("fixture is too clean to be a noise test: max ratio jitter %.3e", maxDelta)
	}
	if maxDelta >= adjustmentEpsilon {
		// If you got here by LOWERING adjustmentEpsilon, read its measurement block
		// before touching this fixture. ~1.5e-07 of jitter is what four float32
		// roundings physically produce; it is a property of the data feed, not a
		// tunable, and a threshold under it declares float noise to be dividends.
		t.Fatalf("fixture jitter %.3e exceeds the threshold %.0e — if adjustmentEpsilon was just "+
			"tightened, the threshold is wrong, not the fixture", maxDelta, adjustmentEpsilon)
	}
	t.Logf("float32 fixture jitter: max |r[i]/r[i-1]-1| = %.3e (threshold %.0e)", maxDelta, adjustmentEpsilon)

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentOK {
		t.Errorf("Status = %q, want %q (ratio is 1.03, not 1.0)", got.Status, AdjustmentOK)
	}
	if len(got.Events) != 0 {
		t.Errorf("got %d events, want 0 — float noise is not an adjustment: %+v",
			len(got.Events), got.Events)
	}
	if age, ok := LastAdjustmentAge(candles); ok {
		t.Errorf("LastAdjustmentAge = (%d, true), want ok=false", age)
	}
}

// TestScanAdjustmentsPurity: no clock, no I/O, no state. Same input → same
// output, and the caller's slice comes back untouched.
func TestScanAdjustmentsPurity(t *testing.T) {
	candles := series(t,
		[]float64{110, 100, 100, 98, 98, 49},
		[]float64{50, 50, 50, 50, 50, 50})
	before := make([]Candle, len(candles))
	copy(before, candles)

	first := ScanAdjustments(candles)
	second := ScanAdjustments(candles)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two calls disagree:\n first  = %+v\n second = %+v", first, second)
	}
	if !reflect.DeepEqual(candles, before) {
		t.Error("ScanAdjustments mutated the caller's candles")
	}

	a1, ok1 := LastAdjustmentAge(candles)
	a2, ok2 := LastAdjustmentAge(candles)
	if a1 != a2 || ok1 != ok2 {
		t.Errorf("LastAdjustmentAge not deterministic: (%d,%v) then (%d,%v)", a1, ok1, a2, ok2)
	}
	if !reflect.DeepEqual(candles, before) {
		t.Error("LastAdjustmentAge mutated the caller's candles")
	}
}

// ── real-data smoke test ─────────────────────────────────────────────────────

const adjustmentCacheDir = "../../.cache"

// TestScanAdjustmentsRealCache0050 runs the detector against the cached 0050
// history and asserts the four ex-dividend dates it must find. 0050 distributes
// twice a year (January and July), and these four dates were verified
// independently of this code:
//
//	2025-01-17, 2025-07-21, 2026-01-22, 2026-07-21
//
// This is the test that would catch a threshold that has drifted into the noise
// (extra dates) or above the real events (missing dates). Skips when .cache is
// absent, so a fresh clone or CI can never go red on it.
func TestScanAdjustmentsRealCache0050(t *testing.T) {
	if _, err := os.Stat(adjustmentCacheDir); err != nil {
		t.Skipf("no .cache at %s — skipping real-data validation", adjustmentCacheDir)
	}
	path := filepath.Join(adjustmentCacheDir, "0050_TW.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("0050 not cached at %s: %v", path, err)
	}
	var rec cacheRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		t.Fatalf("cannot decode %s: %v", path, err)
	}
	candles := rec.Data.Candles
	if len(candles) < 120 {
		t.Skipf("0050 has only %d cached bars — too short to contain the expected dates", len(candles))
	}

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentOK {
		t.Fatalf("Status = %q, want %q — 0050 pays dividends and Yahoo adjusts for them",
			got.Status, AdjustmentOK)
	}

	first, last := candles[0].Date, candles[len(candles)-1].Date
	t.Logf("0050: %d bars %s → %s, %d events, %d skipped",
		len(candles), first.Format("2006-01-02"), last.Format("2006-01-02"),
		len(got.Events), got.SkippedBars)

	wantDates := []string{"2025-01-17", "2025-07-21", "2026-01-22", "2026-07-21"}
	gotDates := make([]string, 0, len(got.Events))
	for _, ev := range got.Events {
		gotDates = append(gotDates, ev.Date.Format("2006-01-02"))
		t.Logf("  event %s idx=%d gap=%+.4f%% ratio %.9f → %.9f",
			ev.Date.Format("2006-01-02"), ev.Index, ev.GapPct, ev.RatioBefore, ev.RatioAfter)
	}

	// Only assert over the dates the cached window actually covers, so a
	// re-fetched shorter cache reports a skip-shaped truth rather than a failure.
	var expected []string
	for _, d := range wantDates {
		day, err := time.Parse("2006-01-02", d)
		if err != nil {
			t.Fatalf("bad literal in test: %q", d)
		}
		if !day.Before(first) && !day.After(last) {
			expected = append(expected, d)
		}
	}
	if len(expected) == 0 {
		t.Skipf("cached window %s → %s contains none of the known 0050 ex-dates",
			first.Format("2006-01-02"), last.Format("2006-01-02"))
	}
	if len(expected) < len(wantDates) {
		t.Logf("cached window covers only %d of the %d known ex-dates", len(expected), len(wantDates))
	}

	for _, want := range expected {
		found := false
		for _, have := range gotDates {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("0050 ex-dividend date %s not detected; detected dates = %v", want, gotDates)
		}
	}

	// The converse matters just as much: a threshold sunk into the noise band
	// would add dates that are not corporate actions. Within the covered window
	// the detected set must be exactly the expected set.
	for _, have := range gotDates {
		day, err := time.Parse("2006-01-02", have)
		if err != nil {
			t.Fatalf("unparsable event date %q", have)
		}
		if day.Before(first) || day.After(last) {
			continue
		}
		known := false
		for _, want := range wantDates {
			if have == want {
				known = true
				break
			}
		}
		if !known {
			t.Errorf("detected %s, which is not a known 0050 ex-dividend date (known: %v) — "+
				"either the threshold is admitting noise or the known list needs updating",
				have, wantDates)
		}
	}
}

// ── WindowIsAdjustmentClean ──────────────────────────────────────────────────

// eventSeries builds a series of `length` bars that is economically FLAT and
// pays a dividend worth 5% of the price on the bar whose AGE is wantAge (age 0
// = newest bar). Construction is the anchor test's:
//
//	raw Close  100 ... 100 | 95 ... 95     ← quote drops by the dividend
//	AdjClose    95 ...  95 |  95 ... 95    ← holder's value never changed
//
// so the ratio staircase is 100/95 = 1.0526315789473684 before the event bar and
// exactly 1.0 from it onward: one step, at index length-1-wantAge, GapPct -5%.
// The event index must be >= 1 for the step to be visible at all, so
// wantAge <= length-2.
func eventSeries(t *testing.T, length, wantAge int) []Candle {
	t.Helper()
	idx := length - 1 - wantAge
	if idx < 1 {
		t.Fatalf("fixture broken: event index %d in a %d-bar series leaves no earlier bar to step from", idx, length)
	}
	closes := make([]float64, length)
	adj := make([]float64, length)
	for i := range closes {
		if i < idx {
			closes[i] = 100
		} else {
			closes[i] = 95
		}
		adj[i] = 95
	}
	return series(t, closes, adj)
}

// TestWindowIsAdjustmentCleanFalseBranches walks every path that must answer
// false without looking at any event, because each is a case we cannot vouch
// for. "Unknown" and "out of range" must never come back as "clean".
//
// Hand-derived expectations, one row per branch:
//
//	fixture                        len  barsRead  scan status          want
//	AdjClose copies Close           30    20      NO_ADJUSTED_SERIES   false  (undetectable, not absent)
//	one bar only                     1     1      INSUFFICIENT_BARS    false  (no ratio pair exists)
//	5% dividend at age 2             6     0      OK, 1 event          false  (a zero-bar read is not a clean read)
//	5% dividend at age 2             6    -1      OK, 1 event          false  (negative is nonsense, not "clean")
//	ratio constant 1.05              4     5      OK, 0 events         false  (window longer than the evidence)
//	5% dividend at age 2             6     7      OK, 1 event          false  (same, with an event present)
//
// The `ratio constant 1.05 / barsRead 5` row is the one that pins the
// barsRead > len(candles) guard on its own: it is the only shape where dropping
// the guard would answer true (status OK with no events short-circuits to clean),
// since with an event present age <= len-1 < barsRead already forces false.
func TestWindowIsAdjustmentCleanFalseBranches(t *testing.T) {
	flat := make([]float64, 30)
	for i := range flat {
		flat[i] = 40 + float64(i%3)
	}

	tests := []struct {
		name     string
		candles  []Candle
		barsRead int
		why      string
	}{
		{
			name:     "NO_ADJUSTED_SERIES",
			candles:  series(t, flat, flat),
			barsRead: 20,
			why:      "ratio flat at 1.0 is UNKNOWN — an adjustment may have happened invisibly",
		},
		{
			name:     "INSUFFICIENT_BARS",
			candles:  series(t, []float64{95}, []float64{90}),
			barsRead: 1,
			why:      "one bar yields no ratio pair, so nothing can be shown",
		},
		{
			name:     "barsRead zero",
			candles:  eventSeries(t, 6, 2),
			barsRead: 0,
			why:      "a zero-bar read has nothing to certify; answering true would let a caller default to clean",
		},
		{
			name:     "barsRead negative",
			candles:  eventSeries(t, 6, 2),
			barsRead: -1,
			why:      "nonsense input must not read as clean",
		},
		{
			name:     "barsRead beyond series, OK with no events",
			candles:  series(t, []float64{105, 105, 105, 105}, []float64{100, 100, 100, 100}),
			barsRead: 5,
			why:      "we hold 4 bars and were asked about 5 — the 5th is unseen, not clean",
		},
		{
			name:     "barsRead beyond series, with an event",
			candles:  eventSeries(t, 6, 2),
			barsRead: 7,
			why:      "same guard, event present",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if WindowIsAdjustmentClean(tc.candles, tc.barsRead) {
				t.Errorf("WindowIsAdjustmentClean(len=%d, barsRead=%d) = true, want false — %s",
					len(tc.candles), tc.barsRead, tc.why)
			}
		})
	}
}

// TestWindowIsAdjustmentCleanOKNoEvents is the only shape that answers true with
// no event evidence at all: an adjusted series demonstrably exists (ratio 1.05,
// so AdjClose is not a copy of Close) and nothing in it ever adjusted, so no
// trailing window of it can straddle a basis change.
//
//	Close    105 105 105 105     ratio 1.05 on every bar → OK, 0 events
//	AdjClose 100 100 100 100     every barsRead in 1..4 is clean
func TestWindowIsAdjustmentCleanOKNoEvents(t *testing.T) {
	candles := series(t,
		[]float64{105, 105, 105, 105},
		[]float64{100, 100, 100, 100})
	for barsRead := 1; barsRead <= 4; barsRead++ {
		if !WindowIsAdjustmentClean(candles, barsRead) {
			t.Errorf("WindowIsAdjustmentClean(barsRead=%d) = false, want true — "+
				"an adjusted series exists and never adjusted", barsRead)
		}
	}
}

// TestWindowIsAdjustmentCleanBoundary is the off-by-one test, and the reason the
// function exists rather than leaving `age <= N` to callers. The criterion is
// age >= barsRead: the window is dirty exactly when the event bar falls inside
// the last barsRead bars.
//
// All rows use eventSeries, so the single event sits at index length-1-age and
// the age is fixed by construction, not read back from the code under test:
//
//	length  age  barsRead  event idx  event inside last barsRead bars?  want
//	   30    20     20          9      age 20 > 19 = oldest read → no    true
//	   30    19     20         10      age 19 <= 19               → yes  false
//	   30    60     20        -31      (invalid fixture, not tested)
//	    6     2      2          3      age 2 > 1                  → yes* true
//	    6     1      2          4      age 1 <= 1                 → yes  false
//	   30     3      3         26      age 3 > 2                  → no   true
//	   30     2      3         27      age 2 <= 2                 → yes  false
//
// (*) age 2 with barsRead 2 reads ages 0..1, which stops one bar short of the
// event bar — clean.
//
// The age == barsRead-1 rows are the ones that a `age >= barsRead-1` criterion
// would wrongly pass, and the ones a caller writing `age <= barsRead-2` (correct
// only for a plain average) would wrongly pass for ATR.
func TestWindowIsAdjustmentCleanBoundary(t *testing.T) {
	tests := []struct {
		length   int
		age      int
		barsRead int
		want     bool
	}{
		{length: 30, age: 20, barsRead: 20, want: true},
		{length: 30, age: 19, barsRead: 20, want: false},
		{length: 6, age: 2, barsRead: 2, want: true},
		{length: 6, age: 1, barsRead: 2, want: false},
		{length: 30, age: 3, barsRead: 3, want: true},
		{length: 30, age: 2, barsRead: 3, want: false},
	}
	for _, tc := range tests {
		name := "age" + itoa(tc.age) + "_barsRead" + itoa(tc.barsRead)
		t.Run(name, func(t *testing.T) {
			candles := eventSeries(t, tc.length, tc.age)

			// Guard against a fixture that does not carry the age it claims: the
			// expectations above are only meaningful if the event really is where
			// eventSeries put it.
			scan := ScanAdjustments(candles)
			if scan.Status != AdjustmentOK || len(scan.Events) != 1 {
				t.Fatalf("fixture broken: status %q with %d events, want OK with 1",
					scan.Status, len(scan.Events))
			}
			if idx := scan.Events[0].Index; idx != tc.length-1-tc.age {
				t.Fatalf("fixture broken: event at index %d, want %d (age %d in %d bars)",
					idx, tc.length-1-tc.age, tc.age, tc.length)
			}

			if got := WindowIsAdjustmentClean(candles, tc.barsRead); got != tc.want {
				t.Errorf("WindowIsAdjustmentClean(age=%d, barsRead=%d) = %v, want %v — "+
					"dirty iff the event bar is among the last barsRead bars (age <= barsRead-1)",
					tc.age, tc.barsRead, got, tc.want)
			}
		})
	}
}

// TestWindowIsAdjustmentCleanBarsReadSemantics proves barsRead is really the
// number of bars READ and not the indicator period — i.e. that the parameter is
// implemented rather than ignored.
//
// One series, one age, two callers:
//
//	40 bars, 5% dividend at age 20 (index 19)
//	a 20-bar read (MA20)    covers ages 0..19 → stops one bar short → clean
//	a 21-bar read (20 TRs)  covers ages 0..20 → reaches the event bar → NOT clean
//
// age == 20 is the ONLY age at which these two disagree, which is why it is the
// case worth pinning: if this test ever passes with both verdicts equal,
// barsRead has stopped being read.
//
// Precisely what the extra bar buys, so this is not oversold: a read of B bars
// truly straddles the basis change when age <= B-2, and the function suppresses
// at age <= B-1. Passing 21 therefore leaves one bar of margin; passing 20 lands
// exactly on a 21-bar read's true contamination boundary (age <= 19) with none.
// Both are "correct" at age 19 — the difference shows up only here, at age 20,
// and the repo's preference is the suppressing side.
//
// WHAT THIS TEST DOES NOT SAY. The 21-bar row was previously labelled "ATR20",
// as though passing N+1 certified a Wilder ATR. It does not, and the label was
// removed: 21 bars is the window in which 20 TRUE RANGES are FORMED, nothing
// more. This repo's ATR is a recursion over the whole series and retains
// ((N-1)/N)^age of every true range for ever — see
// TestWilderATRCannotBeCertifiedByWindow in adjustment_indicator_test.go, which
// measures against the REAL indicator.ATR exactly how much of the event a
// "clean" verdict here still leaves inside the ATR.
func TestWindowIsAdjustmentCleanBarsReadSemantics(t *testing.T) {
	const (
		length = 40
		age    = 20
	)
	candles := eventSeries(t, length, age)

	scan := ScanAdjustments(candles)
	if scan.Status != AdjustmentOK || len(scan.Events) != 1 || scan.Events[0].Index != length-1-age {
		t.Fatalf("fixture broken: status %q, %d events, first index %d — want OK, 1, %d",
			scan.Status, len(scan.Events), scan.Events[0].Index, length-1-age)
	}

	ma20 := WindowIsAdjustmentClean(candles, 20) // MA20 reads 20 bars
	tr20 := WindowIsAdjustmentClean(candles, 21) // 20 true ranges are formed from 21 bars
	if !ma20 {
		t.Errorf("barsRead=20 = false, want true — its 20 bars stop at age 19, the event is at age 20")
	}
	if tr20 {
		t.Errorf("barsRead=21 = true, want false — its 21st bar IS the event bar at age 20")
	}
	if ma20 == tr20 {
		t.Errorf("barsRead 20 and 21 agree (%v) on the same series — barsRead is being ignored, "+
			"which is precisely the off-by-one this parameter's name exists to prevent", ma20)
	}
}

// TestWindowIsAdjustmentCleanUnpricedWindow pins the guard that a window made of
// bars WITH NO PRICE is not "clean". It is MISSING ≠ ZERO applied to the window
// itself rather than to the ratio, and before the guard existed the function
// answered true on a window in which not one bar was usable.
//
// The minimal reproduction, and why it slipped through: the ratio staircase is
// built only from usable bars, so the ten priced bars at the FRONT prove an
// adjusted series exists (ratio 1.05, not a flat 1.0) and contain no step. The
// scan therefore returns OK with zero events — the one shape that short-circuits
// to true — while the trailing twenty bars it is being asked about are empty.
//
//	bar        0..9        10..29
//	Close       105             0
//	AdjClose    100             0
//	ratio      1.05           n/a     → OK, 0 events, usable 10, skipped 20
//
// The subtests also fix the guard's SCOPE, which is the part a careless
// implementation gets wrong: it must re-derive usability over the trailing
// barsRead bars, NOT read AdjustmentScan.SkippedBars, which counts the WHOLE
// series. A hole outside the window disqualifies nothing.
func TestWindowIsAdjustmentCleanUnpricedWindow(t *testing.T) {
	// priced builds a 30-bar series at a constant ratio of 1.05 (so the scan is
	// OK with no events) and blanks the listed bars.
	priced := func(blank ...int) []Candle {
		closes := make([]float64, 30)
		adj := make([]float64, 30)
		for i := range closes {
			closes[i], adj[i] = 105, 100
		}
		for _, i := range blank {
			closes[i], adj[i] = 0, 0
		}
		return series(t, closes, adj)
	}

	t.Run("whole window unpriced", func(t *testing.T) {
		blank := make([]int, 0, 20)
		for i := 10; i < 30; i++ {
			blank = append(blank, i)
		}
		candles := priced(blank...)

		// The premise: the scan really does report the "clean" shape here.
		scan := ScanAdjustments(candles)
		if scan.Status != AdjustmentOK || len(scan.Events) != 0 {
			t.Fatalf("fixture broken: status %q with %d events, want OK with 0", scan.Status, len(scan.Events))
		}
		if scan.UsableBars != 10 || scan.SkippedBars != 20 {
			t.Fatalf("fixture broken: usable/skipped = %d/%d, want 10/20", scan.UsableBars, scan.SkippedBars)
		}

		// Every trailing window ends on bar 29, which has no price, so no
		// barsRead at all can be certified.
		for _, barsRead := range []int{1, 2, 10, 20, 21, 30} {
			if WindowIsAdjustmentClean(candles, barsRead) {
				t.Errorf("WindowIsAdjustmentClean(barsRead=%d) = true, want false — "+
					"the last %d bars carry no price at all, so nothing about them is shown",
					barsRead, barsRead)
			}
		}
	})

	t.Run("one hole inside the window", func(t *testing.T) {
		candles := priced(25) // window of 20 = bars 10..29, hole at 25
		if WindowIsAdjustmentClean(candles, 20) {
			t.Error("WindowIsAdjustmentClean(barsRead=20) = true, want false — bar 25 is inside " +
				"the window and unpriced; an adjustment landing on it is invisible to the scan")
		}
		// Control: the identical series WITH bar 25 priced must be clean, so the
		// false above is attributable to the hole and to nothing else.
		if !WindowIsAdjustmentClean(priced(), 20) {
			t.Error("WindowIsAdjustmentClean(barsRead=20) on the hole-free control = false, want true")
		}
	})

	t.Run("the same hole outside the window", func(t *testing.T) {
		candles := priced(5) // window of 20 = bars 10..29; the hole is at bar 5
		if scan := ScanAdjustments(candles); scan.SkippedBars != 1 {
			t.Fatalf("fixture broken: SkippedBars = %d, want 1", scan.SkippedBars)
		}
		if !WindowIsAdjustmentClean(candles, 20) {
			t.Error("WindowIsAdjustmentClean(barsRead=20) = false, want true — the only hole is at " +
				"bar 5, outside the last 20 bars; gating on the whole-series SkippedBars would " +
				"reject a window that is genuinely complete")
		}
		// Widen the window until it swallows bar 5 and the verdict must flip.
		if WindowIsAdjustmentClean(candles, 25) {
			t.Error("WindowIsAdjustmentClean(barsRead=25) = true, want false — bars 5..29 include the hole")
		}
	})
}

// WHY THE WILDER-ATR MEASUREMENT IS NOT IN THIS FILE, AND MUST NOT COME BACK.
//
// An earlier version kept a COPY of internal/indicator/atr.go's recursion here
// (wilderATRLast) and measured the residual against the copy, justified as "this
// package cannot import internal/indicator without an import cycle". That reason
// was only half true, and the half that is false is the load-bearing one:
//
//   - THIS file is `package fetcher`, an INTERNAL test package. It is compiled
//     INTO package fetcher, so importing internal/indicator — which imports
//     internal/fetcher (calculator.go:3) — really would be a cycle. True.
//   - A file in the SAME DIRECTORY declaring `package fetcher_test` is a
//     separate EXTERNAL test package that the go tool compiles after fetcher,
//     and it may import anything that imports fetcher. No cycle. This is the
//     standard Go idiom for exactly this situation.
//
// So the copy bought nothing and cost the only thing that mattered: it could not
// notice atr.go changing. Rewriting atr.go as a finite N-bar window left this
// whole file GREEN. The measurement now lives in adjustment_indicator_test.go
// (package fetcher_test) and runs against the real indicator.ATR, indicator.RSI
// and indicator.KDJ, so the doc's residual tables and the indicators cannot
// drift apart unnoticed.

// TestWindowIsAdjustmentCleanRealCache0050 is the real-data case. The age it
// checks against is derived from 0050's known ex-dividend DATE (2026-07-21,
// the same literal the scan test asserts), not read back from the detector, so
// the boundary is pinned by an external fact.
//
//	age = (index of the newest bar) - (index of the 2026-07-21 bar)
//	WindowIsAdjustmentClean(candles, age)   must be true  (window stops one bar short)
//	WindowIsAdjustmentClean(candles, age+1) must be false (window reaches the event bar)
//
// It also pins the pre-slicing pitfall documented on ScanAdjustments: 0050's
// ratio staircase reached exactly 1.0 at that dividend, so its last 20 bars in
// ISOLATION report NO_ADJUSTED_SERIES ("unknown") even though the full series
// scans OK. Skips when .cache is absent.
func TestWindowIsAdjustmentCleanRealCache0050(t *testing.T) {
	if _, err := os.Stat(adjustmentCacheDir); err != nil {
		t.Skipf("no .cache at %s — skipping real-data validation", adjustmentCacheDir)
	}
	path := filepath.Join(adjustmentCacheDir, "0050_TW.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("0050 not cached at %s: %v", path, err)
	}
	var rec cacheRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		t.Fatalf("cannot decode %s: %v", path, err)
	}
	candles := rec.Data.Candles
	if len(candles) < 120 {
		t.Skipf("0050 has only %d cached bars", len(candles))
	}

	// 0050 distributes in January and July. This literal is the LAST known
	// ex-date; if the cache ever runs far enough past it for the next one to
	// exist, the literal is no longer "last" and the boundary below would be
	// measured against the wrong bar, so skip rather than assert nonsense.
	const knownLastExDate = "2026-07-21"
	staleAfter := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	if !candles[len(candles)-1].Date.Before(staleAfter) {
		t.Skipf("cache runs to %s, past %s — %q may no longer be 0050's last ex-date",
			candles[len(candles)-1].Date.Format("2006-01-02"), staleAfter.Format("2006-01-02"), knownLastExDate)
	}

	exIdx := -1
	for i, c := range candles {
		if c.Date.Format("2006-01-02") == knownLastExDate {
			exIdx = i
			break
		}
	}
	if exIdx < 1 {
		t.Skipf("cached window %s → %s does not contain %s",
			candles[0].Date.Format("2006-01-02"),
			candles[len(candles)-1].Date.Format("2006-01-02"), knownLastExDate)
	}
	age := len(candles) - 1 - exIdx
	t.Logf("0050: %d bars, last known ex-date %s at index %d → age %d",
		len(candles), knownLastExDate, exIdx, age)

	if !WindowIsAdjustmentClean(candles, age) {
		t.Errorf("WindowIsAdjustmentClean(barsRead=%d) = false, want true — "+
			"%d bars stop one bar short of the %s event bar", age, age, knownLastExDate)
	}
	if WindowIsAdjustmentClean(candles, age+1) {
		t.Errorf("WindowIsAdjustmentClean(barsRead=%d) = true, want false — "+
			"the %dth trailing bar IS the %s event bar", age+1, age+1, knownLastExDate)
	}

	// The documented pre-slicing trap, on real data.
	if age >= 20 {
		if full := ScanAdjustments(candles); full.Status != AdjustmentOK {
			t.Errorf("full series Status = %q, want %q", full.Status, AdjustmentOK)
		}
		sliced := ScanAdjustments(candles[len(candles)-20:])
		if sliced.Status != AdjustmentNoAdjustedSeries {
			t.Errorf("last 20 bars alone: Status = %q, want %q — the staircase has unwound to 1.0, "+
				"so a pre-sliced window is indistinguishable from having no adjusted series",
				sliced.Status, AdjustmentNoAdjustedSeries)
		}
		if !WindowIsAdjustmentClean(candles, 20) {
			t.Error("WindowIsAdjustmentClean(full series, 20) = false, want true — " +
				"passing the full series is what recovers the answer the slicing destroys")
		}
	}
}

// TestScanAdjustmentsPerBarAdjustedHole pins the second documented mechanism for
// a POSITIVE GapPct (the first, a reverse split / capital reduction, has no
// example in the cache either). yahoo.go's AdjClose = Close fallback is per-bar,
// so one missing adjusted close drops that bar's ratio to exactly 1.0 and the
// staircase steps down onto it and back off again — two events, the second
// positive:
//
//	bar        0        1        2        3
//	Close    105      105      105      105
//	AdjClose 100      105      100      100      ← bar 1's adjusted close missing
//	ratio   1.05      1.0     1.05     1.05
//
//	bar 1 GapPct = (1.0/1.05 - 1)*100 = -4.761904761904762
//	bar 2 GapPct = (1.05/1.0 - 1)*100 = +5.0 exactly
//
// Neither is a corporate action, which is why the doc calls this out: an
// upstream hole fabricates events, and any caller comparing GapPct < -x instead
// of math.Abs(GapPct) > x would silently ignore the positive half.
func TestScanAdjustmentsPerBarAdjustedHole(t *testing.T) {
	candles := series(t,
		[]float64{105, 105, 105, 105},
		[]float64{100, 105, 100, 100})

	got := ScanAdjustments(candles)
	if got.Status != AdjustmentOK {
		t.Fatalf("Status = %q, want %q", got.Status, AdjustmentOK)
	}
	if len(got.Events) != 2 {
		t.Fatalf("got %d events, want 2 (down onto 1.0, then back off it): %+v", len(got.Events), got.Events)
	}
	if got.Events[0].Index != 1 || math.Abs(got.Events[0].GapPct-(-4.761904761904762)) > 1e-9 {
		t.Errorf("event 0 = idx %d gap %.12f, want idx 1 gap -4.761904761905",
			got.Events[0].Index, got.Events[0].GapPct)
	}
	if got.Events[1].Index != 2 || math.Abs(got.Events[1].GapPct-5) > 1e-9 {
		t.Errorf("event 1 = idx %d gap %.12f, want idx 2 gap +5 exactly",
			got.Events[1].Index, got.Events[1].GapPct)
	}
	if got.Events[1].GapPct <= 0 {
		t.Errorf("event 1 GapPct = %.12f, want positive — the sign is the point: "+
			"RatioAfter > RatioBefore is possible and callers must use math.Abs", got.Events[1].GapPct)
	}
	// The fabricated pair also means the window looks dirty right up to the hole.
	if WindowIsAdjustmentClean(candles, 2) {
		t.Error("WindowIsAdjustmentClean(barsRead=2) = true, want false — the fabricated event sits at age 1")
	}
}

// itoa keeps subtest names readable without pulling strconv in for one call.
func itoa(n int) string {
	if n < 0 {
		return "neg" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + itoa(n%10)
}
