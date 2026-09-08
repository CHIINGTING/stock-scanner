package fetcher_test

// EXTERNAL test package, and that is the entire point of this file.
//
// WindowIsAdjustmentClean's doc makes quantitative claims about indicators that
// live in ANOTHER package: the ((N-1)/N)^age residual table for the Wilder ATR,
// the same recursion inside RSI, and the K/D cascade inside KDJ. Those claims
// were previously checked against a COPY of the recursion kept in
// adjustment_test.go, on the stated grounds that internal/indicator imports
// internal/fetcher (calculator.go:3) and so fetcher could not import it back.
//
// That reasoning holds only for the INTERNAL test package. adjustment_test.go is
// `package fetcher`, compiled into fetcher itself, so importing internal/indicator
// there really is a cycle. THIS file declares `package fetcher_test`, which the
// go tool compiles as a separate package AFTER fetcher, and which may therefore
// import anything that imports fetcher. No cycle — it is the standard Go idiom
// for exactly this situation, and it is what closes the gap the copy left open:
// rewriting internal/indicator/atr.go into a finite N-bar window keeps every
// test in adjustment_test.go green, and fails the first test below.
//
// Everything here measures by FINITE DIFFERENCE on the real indicator: build a
// fixture, bump one input, and read how much of the bump survives into today's
// value. Nothing calls a formula and compares it to itself. The literals
// asserted are the ones printed in WindowIsAdjustmentClean's doc, so a doc table
// edited without re-measuring fails here.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ── ATR ──────────────────────────────────────────────────────────────────────

// atrResidual measures, on the REAL indicator.ATR, what fraction of one true
// range's ENTRY WEIGHT (1/period) is still inside atr[newest] `age` bars later.
//
// Fixture: every bar has High 100.5, Low 99.5, Close 100, so for every i >= 1
//
//	tr(i) = max(H-L, |H-C[i-1]|, |L-C[i-1]|) = max(1.0, 0.5, 0.5) = 1.0
//
// Bumping ONE bar's High by +0.5 and its Low by -0.5 raises that bar's true
// range to 2.0 and touches no other bar's: tr(i+1) reads Close[i], which is
// left alone. Wilder smoothing is linear in tr, so the difference is exact and
// no step size has to be chosen.
func atrResidual(t *testing.T, period, age int) float64 {
	t.Helper()
	const span = 400 // long enough that every age tested sits in the recursion
	build := func(bumpIdx int) ([]float64, []float64, []float64) {
		highs := make([]float64, span)
		lows := make([]float64, span)
		closes := make([]float64, span)
		for i := range closes {
			highs[i], lows[i], closes[i] = 100.5, 99.5, 100
		}
		if bumpIdx >= 0 {
			highs[bumpIdx] += 0.5
			lows[bumpIdx] -= 0.5
		}
		return highs, lows, closes
	}
	idx := span - 1 - age
	if idx <= period {
		t.Fatalf("fixture broken: age %d puts the bumped bar at index %d, inside period %d's seed",
			age, idx, period)
	}

	h, l, c := build(-1)
	base := indicator.ATR(h, l, c, period)
	if base[span-1] != 1 {
		t.Fatalf("fixture broken: base ATR(%d)[newest] = %v, want exactly 1 (every true range is 1.0)",
			period, base[span-1])
	}
	h, l, c = build(idx)
	got := indicator.ATR(h, l, c, period)
	// (bumped - base) is (1/period)·((period-1)/period)^age; scale by period to
	// express it as a fraction of the weight the true range entered with.
	return (got[span-1] - base[span-1]) * float64(period)
}

// eventSeries builds a series of `length` bars that is economically FLAT and
// pays a dividend worth 5% of the price on the bar whose AGE is wantAge (age 0 =
// newest bar). It is the same construction adjustment_test.go uses, restated
// here because an external test package cannot see the internal one's helpers:
//
//	Close    is 100 on every bar
//	AdjClose is 95 before the event bar and 100 from it onward
//
// so the ratio staircase is 100/95 = 1.0526315789473684 before the event bar and
// exactly 1.0 from it onward: one step, at index length-1-wantAge, GapPct -5%.
func eventSeries(t *testing.T, length, wantAge int) []fetcher.Candle {
	t.Helper()
	if wantAge < 0 || wantAge > length-2 {
		t.Fatalf("fixture broken: age %d needs the event at index %d, which must be >= 1 and <= %d",
			wantAge, length-1-wantAge, length-1)
	}
	evIdx := length - 1 - wantAge
	out := make([]fetcher.Candle, length)
	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	for i := range out {
		adj := 95.0
		if i >= evIdx {
			adj = 100.0
		}
		out[i] = fetcher.Candle{Date: base.AddDate(0, 0, i), Close: 100, AdjClose: adj}
	}
	return out
}

// TestWilderATRCannotBeCertifiedByWindow makes the central caveat in
// WindowIsAdjustmentClean's doc executable AGAINST THE REAL indicator.ATR: a
// true verdict is a guarantee for a FINITE-WINDOW calculation and is not one for
// a Wilder recursion, which retains ((N-1)/N)^age of every true range for ever.
//
// Hand-derived expectations — residual = ((N-1)/N)^age:
//
//	age           ATR(14)  = (13/14)^age    ATR(20)  = (19/20)^age
//	N+1             0.329                     0.341
//	2N              0.126                     0.129
//	60              0.012                     0.046
//	first age <1%      63                        90
//
// The literals are the ones printed in the doc's table, so a table edited
// without recomputing fails here. The 63 matters specifically: (13/14)^62 =
// 0.01011, i.e. 62 bars is NOT enough for 1%, which is the rounding an earlier
// draft made.
//
// Because the measurement now runs on indicator.ATR itself, replacing that
// recursion with a finite N-bar window — the change that would silently
// invalidate the doc — fails this test instead of passing unnoticed.
func TestWilderATRCannotBeCertifiedByWindow(t *testing.T) {
	table := []struct {
		period, age int
		want        float64 // the literal printed in WindowIsAdjustmentClean's doc
	}{
		{14, 15, 0.329}, {20, 21, 0.341},
		{14, 28, 0.126}, {20, 40, 0.129},
		{14, 60, 0.012}, {20, 60, 0.046},
	}
	for _, tc := range table {
		got := atrResidual(t, tc.period, tc.age)
		if math.Abs(got-tc.want) > 5e-4 {
			t.Errorf("ATR(%d) residual at age %d = %.6f, want %.3f (the doc's table)",
				tc.period, tc.age, got, tc.want)
		}
		if exact := math.Pow(float64(tc.period-1)/float64(tc.period), float64(tc.age)); math.Abs(got-exact) > 1e-9 {
			t.Errorf("ATR(%d) residual at age %d = %.12f, closed form ((N-1)/N)^age = %.12f — "+
				"the real indicator.ATR and the documented formula have parted ways",
				tc.period, tc.age, got, exact)
		}
	}

	// Where the residual finally drops under 1%, and the fact that it never
	// reaches zero on the way. A finite-window ATR would hit exactly 0 here.
	for _, tc := range []struct{ period, wantAge int }{{14, 63}, {20, 90}} {
		age := 1
		for ; age < 300; age++ {
			r := atrResidual(t, tc.period, age)
			if r <= 0 {
				t.Fatalf("ATR(%d) residual at age %d = %g, want strictly positive — "+
					"a Wilder recursion has no cut-off, so this is a windowed ATR and "+
					"WindowIsAdjustmentClean's doc is out of date", tc.period, age, r)
			}
			if r < 0.01 {
				break
			}
		}
		if age != tc.wantAge {
			t.Errorf("ATR(%d) first age with residual below 1%% = %d, want %d", tc.period, age, tc.wantAge)
		}
	}
	if r := atrResidual(t, 14, 62); r < 0.01 {
		t.Errorf("ATR(14) residual at age 62 = %.5f, want >= 0.01 — 62 bars must NOT qualify as "+
			"the 1%% point (ln(100)/ln(14/13) = 62.14 rounds UP)", r)
	}

	// And the punchline: one identical "clean" verdict covers wildly different
	// amounts of surviving event. A 5% dividend at age 15 and one at age 63 are
	// BOTH certified clean for a 15-bar read (ATR(14)'s TR-formation window),
	// while the residual differs by more than 30x.
	near := eventSeries(t, 200, 15)
	far := eventSeries(t, 200, 63)
	if !fetcher.WindowIsAdjustmentClean(near, 15) || !fetcher.WindowIsAdjustmentClean(far, 15) {
		t.Fatalf("fixture broken: barsRead=15 verdicts are %v / %v at ages 15 and 63, want true / true",
			fetcher.WindowIsAdjustmentClean(near, 15), fetcher.WindowIsAdjustmentClean(far, 15))
	}
	rNear, rFar := atrResidual(t, 14, 15), atrResidual(t, 14, 63)
	if rNear/rFar < 30 {
		t.Errorf("residual at age 15 / at age 63 = %.4f / %.4f = %.1fx, want > 30x — the point of "+
			"this test is that one identical 'clean' answer spans both", rNear, rFar, rNear/rFar)
	}
	if rNear < 0.3 {
		t.Errorf("residual at age 15 = %.4f, want >= 0.3 — a third of the ex-day true range is still "+
			"inside an ATR(14) this function calls clean", rNear)
	}
}

// ── RSI ──────────────────────────────────────────────────────────────────────

// rsiSensitivity is d RSI[newest] / d close[newest-age] on the REAL
// indicator.RSI, by central difference.
//
// Fixture: closes alternate 100 / 101, so every bar is a gain or a loss and
// BOTH Wilder averages stay strictly positive (a monotone fixture would pin
// avgLoss at 0 and RSI at the constant 100, measuring nothing).
//
// The bumped bar must be a PEAK (close 101, odd index): bumping a peak up makes
// that bar's gain larger and the next bar's loss larger, and both sign patterns
// survive a small bump. Bumping a TROUGH instead shrinks a loss and a gain by
// the same amount and the two contributions cancel to within float noise
// (measured: 7e-10, i.e. nothing) — a degenerate direction, not a memory
// measurement, so only peaks are used and only even ages are reachable.
//
// RSI is NONLINEAR in the two averages, so this is a derivative at one point,
// not a residual weight. What is claimed of it is only the SHAPE: shifting the
// bumped bar one bar further back multiplies the derivative by exactly
// (N-1)/N, because that factor multiplies the perturbation of avgGain and of
// avgLoss alike, leaving the (fixed) partial derivatives untouched.
func rsiSensitivity(t *testing.T, period, age int) float64 {
	t.Helper()
	const (
		span = 400
		step = 1e-5
	)
	idx := span - 1 - age
	if idx%2 != 1 {
		t.Fatalf("fixture broken: age %d bumps index %d, which is a trough — only peaks carry signal", age, idx)
	}
	if idx <= period {
		t.Fatalf("fixture broken: age %d puts the bumped bar at index %d, inside period %d's seed", age, idx, period)
	}
	at := func(bump float64) float64 {
		closes := make([]float64, span)
		for i := range closes {
			closes[i] = 100 + float64(i%2)
		}
		closes[idx] += bump
		return indicator.RSI(closes, period)[span-1]
	}
	return (at(step) - at(-step)) / (2 * step)
}

// TestRSIWilderMemoryOnRealIndicator pins the doc's claim that rsi.go:38-39 is
// the IDENTICAL Wilder recursion, on the real indicator.RSI.
//
// Hand-derived expectation: the sensitivity of today's RSI to a close `age` bars
// back is proportional to (13/14)^age at period 14, so
//
//	sensitivity(age) / sensitivity(0) = (13/14)^age exactly.
//
// Only even ages are measurable (see rsiSensitivity), which brackets rather than
// lands on the 1% point: (13/14)^62 = 0.010533 is still above 1% and
// (13/14)^64 = 0.009083 is below, so with the exact shape asserted the crossing
// is at 63 — the same 63 the doc quotes for ATR(14).
func TestRSIWilderMemoryOnRealIndicator(t *testing.T) {
	const period = 14
	base := rsiSensitivity(t, period, 0)
	if base == 0 {
		t.Fatalf("fixture broken: RSI is insensitive to the newest close")
	}
	for _, age := range []int{2, 4, 8, 20, 40, 62, 64} {
		got := rsiSensitivity(t, period, age) / base
		want := math.Pow(13.0/14, float64(age))
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("RSI(14) sensitivity ratio at age %d = %.9f, want (13/14)^%d = %.9f — "+
				"avgGain/avgLoss are no longer the Wilder recursion the doc cites",
				age, got, age, want)
		}
		if got <= 0 {
			t.Errorf("RSI(14) sensitivity ratio at age %d = %g, want strictly positive — "+
				"a Wilder recursion has no cut-off", age, got)
		}
	}
	if r := rsiSensitivity(t, period, 62) / base; r < 0.01 {
		t.Errorf("RSI(14) memory at age 62 = %.6f, want >= 0.01 — 62 bars is not yet the 1%% point", r)
	}
	if r := rsiSensitivity(t, period, 64) / base; r >= 0.01 {
		t.Errorf("RSI(14) memory at age 64 = %.6f, want < 0.01 — the 1%% point must be at or before 64", r)
	}
}

// ── KDJ ──────────────────────────────────────────────────────────────────────

// kdjResidual measures, on the REAL indicator.KDJ, what fraction of one rsv's
// SAME-BAR influence is still in today's K, D and J `age` bars later.
//
// Fixture: High 100 and Low 0 on every bar, so every kPeriod window has hi = 100
// and lo = 0 and kdj.go's rsv reduces to (close - 0)/(100 - 0)*100 == close.
// Bumping one close therefore bumps exactly one rsv and nothing else. K and D
// are linear in rsv, so a bump of 1.0 gives the exact derivative.
//
// NORMALISATION, which the number is meaningless without: each residual is
// divided by the sensitivity of the SAME OUTPUT to TODAY's rsv (age 0). It reads
// "what fraction of the influence this input had on the day it entered is still
// in today's value" — the same convention as the ATR table, where that entry
// weight is 1/N. It also returns the RAW (unnormalised) derivatives, because the
// J assertions compare the 3K and 2D terms against each other.
func kdjResidual(t *testing.T, age, kPeriod, dSmooth, jSmooth int) (kRes, dRes, rawK, rawD, rawJ float64) {
	t.Helper()
	const span = 400
	run := func(bumpIdx int) indicator.KDJResult {
		highs := make([]float64, span)
		lows := make([]float64, span)
		closes := make([]float64, span)
		for i := range closes {
			highs[i], lows[i], closes[i] = 100, 0, 50
		}
		if bumpIdx >= 0 {
			closes[bumpIdx] += 1
		}
		return indicator.KDJ(highs, lows, closes, kPeriod, dSmooth, jSmooth)
	}
	n := span - 1
	base := run(-1)
	if base.K[n] != 50 || base.D[n] != 50 {
		t.Fatalf("fixture broken: base K/D = %v/%v, want 50/50 (rsv is a constant 50)", base.K[n], base.D[n])
	}
	at := func(a int) (float64, float64, float64) {
		g := run(n - a)
		return g.K[n] - base.K[n], g.D[n] - base.D[n], g.J[n] - base.J[n]
	}
	k0, d0, _ := at(0)
	if k0 == 0 || d0 == 0 {
		t.Fatalf("fixture broken: same-bar sensitivity is zero (K %v, D %v)", k0, d0)
	}
	dk, dd, dj := at(age)
	return dk / k0, dd / d0, dk, dd, dj
}

// TestKDJCascadeResidualOnRealIndicator pins the KDJ paragraph of
// WindowIsAdjustmentClean's doc, and specifically the two errors an earlier
// version of that paragraph made: it named dSmooth as D's decay parameter, and
// it modelled D as a single geometric decay (2/3)^age.
//
// From kdj.go:22-23, D's weight is 1/jSmooth, and from kdj.go:48-49 D is an EMA
// OF K — a cascade. Hand-derived, with a = 1-1/dSmooth and b = 1-1/jSmooth:
//
//	K residual = a^age
//	D residual = sum(m = 0..age) b^m · a^(age-m)
//	           = (a^(age+1) - b^(age+1)) / (a - b),  or (age+1)·a^age when a == b
//
// so at the defaults (dSmooth = jSmooth = 3) D is (age+1)·(2/3)^age. The doc's
// literals, reproduced here:
//
//	age      K       D
//	  1   0.6667  1.3333     ← D is ABOVE its entry-day influence: the cascade
//	  2   0.4444  1.3333       is still filling, so D is not monotone
//	  5   0.1317  0.7901
//	 12   0.0077  0.1002     ← K's 1% point; D still holds 10%
//	 19   0.0005  0.0090     ← D's 1% point
//
// The separation of dSmooth from jSmooth is pinned by a second configuration:
// d_smooth 3 / j_smooth 9 leaves every K value identical and moves D's 1% point
// from age 19 to age 51. A model that used dSmooth for D would predict no change
// at all.
func TestKDJCascadeResidualOnRealIndicator(t *testing.T) {
	const (
		kPeriod = 9
		dflt    = 3
	)

	// 1. The doc's table, against the real indicator.
	for _, tc := range []struct{ age int; wantK, wantD float64 }{
		{1, 0.6667, 1.3333},
		{2, 0.4444, 1.3333},
		{5, 0.1317, 0.7901},
		{12, 0.0077, 0.1002},
		{19, 0.0005, 0.0090},
	} {
		k, d, _, _, _ := kdjResidual(t, tc.age, kPeriod, dflt, dflt)
		if math.Abs(k-tc.wantK) > 5e-5 {
			t.Errorf("K residual at age %d = %.6f, want %.4f (the doc's table)", tc.age, k, tc.wantK)
		}
		if math.Abs(d-tc.wantD) > 5e-5 {
			t.Errorf("D residual at age %d = %.6f, want %.4f (the doc's table)", tc.age, d, tc.wantD)
		}
	}

	// 2. The closed forms, on every configuration including a == b and a != b.
	closedD := func(age, dSmooth, jSmooth int) float64 {
		a := 1 - 1/float64(dSmooth)
		b := 1 - 1/float64(jSmooth)
		if a == b {
			return float64(age+1) * math.Pow(a, float64(age))
		}
		return (math.Pow(a, float64(age+1)) - math.Pow(b, float64(age+1))) / (a - b)
	}
	for _, cfg := range [][2]int{{3, 3}, {3, 9}, {5, 3}} {
		for _, age := range []int{1, 3, 7, 12, 19, 30} {
			k, d, _, _, _ := kdjResidual(t, age, kPeriod, cfg[0], cfg[1])
			wantK := math.Pow(1-1/float64(cfg[0]), float64(age))
			if math.Abs(k-wantK) > 1e-9 {
				t.Errorf("d_smooth %d / j_smooth %d: K residual at age %d = %.12f, closed form a^age = %.12f",
					cfg[0], cfg[1], age, k, wantK)
			}
			if wantD := closedD(age, cfg[0], cfg[1]); math.Abs(d-wantD) > 1e-9 {
				t.Errorf("d_smooth %d / j_smooth %d: D residual at age %d = %.12f, cascade closed form = %.12f — "+
					"D is an EMA of K, not a single geometric decay",
					cfg[0], cfg[1], age, d, wantD)
			}
		}
	}

	// 3. D is NOT (2/3)^age at the defaults. Stated as its own assertion because
	//    that is the specific claim the doc used to make.
	if _, d, _, _, _ := kdjResidual(t, 12, kPeriod, dflt, dflt); math.Abs(d-math.Pow(2.0/3, 12)) < 0.05 {
		t.Errorf("D residual at age 12 = %.6f is within 0.05 of (2/3)^12 = %.6f — "+
			"the cascade must make D's tail visibly longer than K's", d, math.Pow(2.0/3, 12))
	}

	// 4. First age under 1%, and D's non-monotone start.
	firstUnder1 := func(dSmooth, jSmooth int, pickD bool) int {
		for age := 1; age < 300; age++ {
			k, d, _, _, _ := kdjResidual(t, age, kPeriod, dSmooth, jSmooth)
			v := k
			if pickD {
				v = d
			}
			if v <= 0 {
				t.Fatalf("d_smooth %d / j_smooth %d: residual at age %d = %g, want strictly positive — "+
					"these are EMAs from index 0 and have no cut-off", dSmooth, jSmooth, age, v)
			}
			if v < 0.01 {
				return age
			}
		}
		t.Fatalf("d_smooth %d / j_smooth %d: residual never fell below 1%%", dSmooth, jSmooth)
		return 0
	}
	if got := firstUnder1(dflt, dflt, false); got != 12 {
		t.Errorf("K first age below 1%% = %d, want 12 (the doc's number)", got)
	}
	if got := firstUnder1(dflt, dflt, true); got != 19 {
		t.Errorf("D first age below 1%% = %d, want 19 (the doc's number) — using dSmooth for D "+
			"would predict 12", got)
	}
	for _, age := range []int{1, 2} {
		if _, d, _, _, _ := kdjResidual(t, age, kPeriod, dflt, dflt); d <= 1 {
			t.Errorf("D residual at age %d = %.4f, want > 1 — the cascade is still filling, so "+
				"'residual' must not be read as monotone decay", age, d)
		}
	}

	// 5. jSmooth, not dSmooth, is D's decay parameter.
	for _, age := range []int{1, 5, 12, 19} {
		kDefault, _, _, _, _ := kdjResidual(t, age, kPeriod, dflt, dflt)
		kWide, _, _, _, _ := kdjResidual(t, age, kPeriod, dflt, 9)
		if kDefault != kWide {
			t.Errorf("changing j_smooth 3 → 9 moved K at age %d (%.12f → %.12f); K depends on dSmooth alone",
				age, kDefault, kWide)
		}
	}
	if got := firstUnder1(dflt, 9, true); got != 51 {
		t.Errorf("d_smooth 3 / j_smooth 9: D first age below 1%% = %d, want 51 — D's decay is "+
			"governed by jSmooth, so widening it must lengthen D's memory and nothing else", got)
	}

	// 6. J = 3K - 2D inherits D's tail: the 2D term overtakes the 3K term between
	//    ages 3 and 4 (J's response to a shock changes SIGN there) and dominates
	//    afterwards.
	for _, tc := range []struct{ age int; wantPositive bool }{{3, true}, {4, false}} {
		_, _, _, _, rawJ := kdjResidual(t, tc.age, kPeriod, dflt, dflt)
		if (rawJ > 0) != tc.wantPositive {
			t.Errorf("J response at age %d = %+.9e, want positive == %v — the 3K/2D crossover "+
				"must sit between ages 3 and 4", tc.age, rawJ, tc.wantPositive)
		}
	}
	_, _, rawK, rawD, rawJ := kdjResidual(t, 19, kPeriod, dflt, dflt)
	if math.Abs(rawJ-(3*rawK-2*rawD)) > 1e-12 {
		t.Errorf("dJ = %.12e but 3·dK - 2·dD = %.12e — J is defined as 3K-2D (kdj.go:50)",
			rawJ, 3*rawK-2*rawD)
	}
	if r := math.Abs(rawJ) / math.Abs(rawK); math.Abs(r-10.3333) > 1e-3 {
		t.Errorf("|dJ|/|dK| at age 19 = %.4f, want 10.3333 (the doc's number) — J's long tail is D's", r)
	}
}

// ── .cache canary for the de-gapping measurement ─────────────────────────────

const adjustmentIndicatorCacheDir = "../../.cache"

// cachedStock mirrors internal/fetcher's unexported cacheRecord JSON shape. Only
// the payload is read; FetchedAt is irrelevant here and the TTL is not applied,
// because this is a measurement over whatever history is on disk, not a fetch.
type cachedStock struct {
	Data fetcher.StockData `json:"data"`
}

// degapErrors returns, per cached symbol with a detectable last adjustment, the
// age of that adjustment and the ATR(14) error the raw series carries because of
// it — the exact counterfactual quoted in WindowIsAdjustmentClean's doc.
//
// The counterfactual restates the close on the bar BEFORE the event onto the new
// basis (Close[k-1] × RatioAfter/RatioBefore). That close is read by exactly one
// true range — tr(k) uses Close[k-1]; tr(k-1) uses Close[k-2] — so feeding the
// restated series to the real indicator.ATR de-gaps ONE true range and leaves
// every other input identical. Error is |raw/cf - 1| in percent, an ABSOLUTE
// value (signed, 7 / 136 / 67 of the doc's three rows are negative).
func degapErrors(t *testing.T) (byAge map[string]float64, ages []int, errs []float64) {
	t.Helper()
	entries, err := os.ReadDir(adjustmentIndicatorCacheDir)
	if err != nil {
		t.Skipf("no .cache at %s — skipping real-data validation", adjustmentIndicatorCacheDir)
	}
	byAge = map[string]float64{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(adjustmentIndicatorCacheDir, e.Name()))
		if err != nil {
			continue
		}
		var rec cachedStock
		if json.Unmarshal(blob, &rec) != nil {
			continue
		}
		candles := rec.Data.Candles
		if len(candles) < 120 { // the sampling floor the doc's study used
			continue
		}
		age, ok := fetcher.LastAdjustmentAge(candles)
		if !ok || age < 15 { // age < 15 is not even TR-formation clean for ATR(14)
			continue
		}
		scan := fetcher.ScanAdjustments(candles)
		ev := scan.Events[len(scan.Events)-1]
		if ev.Index < 1 || ev.RatioBefore <= 0 {
			continue
		}
		n := len(candles)
		highs := make([]float64, n)
		lows := make([]float64, n)
		closes := make([]float64, n)
		for i, c := range candles {
			highs[i], lows[i], closes[i] = c.High, c.Low, c.Close
		}
		raw := indicator.ATR(highs, lows, closes, 14)[n-1]
		degapped := make([]float64, n)
		copy(degapped, closes)
		degapped[ev.Index-1] = closes[ev.Index-1] * (ev.RatioAfter / ev.RatioBefore)
		cf := indicator.ATR(highs, lows, degapped, 14)[n-1]
		if cf <= 0 || raw <= 0 {
			continue
		}
		ages = append(ages, age)
		errs = append(errs, math.Abs(raw/cf-1)*100)
	}
	return byAge, ages, errs
}

// TestATRDeGappingErrorRealCache is the canary for the de-gapping measurement in
// WindowIsAdjustmentClean's doc, which is otherwise the only large empirical
// claim in that file with no test behind it (the residual table has literals
// pinned above; adjustmentEpsilon has TestScanAdjustmentsRealCache0050).
//
// It is deliberately COARSE. Pinning every percentile would break on any cache
// refresh and teach the next reader to update the numbers without thinking. What
// it asserts is the SHAPE the doc's argument rests on:
//
//	the age >= 63 band (1% residual) is genuinely quiet — worst case under 1%; and
//	the younger band is an order of magnitude worse, which is why the doc says a
//	"clean" verdict from WindowIsAdjustmentClean does not certify an ATR.
//
// If either stops holding, the tables in the doc must be re-measured; this test
// cannot do that for you. Skips when .cache is absent or too small to say
// anything.
func TestATRDeGappingErrorRealCache(t *testing.T) {
	_, ages, errs := degapErrors(t)

	band := func(lo, hi int) []float64 {
		var out []float64
		for i, a := range ages {
			if a >= lo && a <= hi {
				out = append(out, errs[i])
			}
		}
		sort.Float64s(out)
		return out
	}
	stat := func(v []float64, p float64) float64 {
		if len(v) == 0 {
			return math.NaN()
		}
		idx := p / 100 * float64(len(v)-1)
		lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
		return v[lo]*(1-(idx-float64(lo))) + v[hi]*(idx-float64(lo))
	}

	// Log the disjoint bands so re-measuring the doc's table is one -v run away.
	for _, b := range [][2]int{{15, 19}, {20, 24}, {25, 29}, {30, 39}, {40, 62}, {63, 1 << 30}} {
		v := band(b[0], b[1])
		if len(v) == 0 {
			continue
		}
		t.Logf("age %2d..%-10d n=%4d median=%.2f%% p90=%.2f%% p99=%.2f%% max=%.2f%%",
			b[0], b[1], len(v), stat(v, 50), stat(v, 90), stat(v, 99), stat(v, 100))
	}

	old := band(63, 1<<30)
	young := band(15, 62)
	if len(old) < 50 || len(young) < 50 {
		t.Skipf("cache too small for this canary: %d symbols at age >= 63, %d at age 15..62 "+
			"(the doc's measurement had 386 and 992)", len(old), len(young))
	}
	t.Logf("doc's numbers: age>=63 n=386 median 0.00%% p90 0.03%% max 0.59%%; "+
		"age 15..62 n=992 median 0.13%% p90 1.37%% max 12.96%%")

	if m := stat(old, 100); m >= 1.0 {
		t.Errorf("age >= 63 band: worst ATR(14) de-gapping error = %.4f%%, want < 1%% "+
			"(the doc measured 0.59%%). The doc's claim that the error collapses past the 1%% "+
			"residual point no longer holds for this cache — re-measure the tables in "+
			"WindowIsAdjustmentClean's doc", m)
	}
	if m := stat(old, 50); m >= 0.1 {
		t.Errorf("age >= 63 band: median error = %.4f%%, want < 0.1%% (the doc measured 0.00%%)", m)
	}
	if oldMax, youngMax := stat(old, 100), stat(young, 100); youngMax < 5*oldMax || youngMax < 2.0 {
		t.Errorf("age 15..62 worst error %.4f%% vs age >= 63 worst %.4f%% — want the young band at "+
			"least 5x worse AND at least 2%%. The doc measured 12.96%% vs 0.59%% (22x); if the gap "+
			"has closed, the argument that a 'clean' window verdict does not certify an ATR needs "+
			"re-measuring", youngMax, oldMax)
	}
}
