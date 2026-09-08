package pricerule

import (
	"math"
	"testing"
)

// eps is the comparison tolerance for a price. Every price this package returns is an exact
// integer number of cents, so anything looser than this would be hiding a real error.
const eps = 1e-9

// There is deliberately no directionTol() helper here any more.
//
// There used to be one, and it returned tickSnapRelTol × max(1, price) — a tolerance that GREW
// with the price, mirroring the shape of the snap threshold it was measuring. That is precisely
// why it could not detect the defect it was covering: when the threshold started absorbing more
// than a cent at large prices, the tolerance grew in lockstep and the assertion stayed green.
// RoundDown(99999999.99) returned 100000000 — a cent ABOVE its own input — and no test failed.
//
// A test tolerance that is derived from the thing under test can only ever confirm it. The
// replacements are both fixed:
//
//   - whole-cent inputs, i.e. every actual price, are asserted BIT-EXACTLY (down <= p, up >= p)
//     with no tolerance at all;
//   - sub-cent inputs, where the snap is allowed to overshoot by construction, are asserted
//     against snapPriceTol, a constant in 元 that does not depend on the price.

func TestTickSizeBands(t *testing.T) {
	// One representative price strictly inside each of the six bands.
	cases := []struct {
		name  string
		price float64
		want  float64
	}{
		{"under 10", 8.88, 0.01},
		{"10 to 50", 33.33, 0.05},
		{"50 to 100", 88.8, 0.1},
		{"100 to 500", 250.25, 0.5},
		{"500 to 1000", 750.5, 1},
		{"1000 and up", 1245, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TickSize(c.price)
			if !ok {
				t.Fatalf("TickSize(%v) ok=false, want true", c.price)
			}
			if math.Abs(got-c.want) > eps {
				t.Fatalf("TickSize(%v) = %v, want %v", c.price, got, c.want)
			}
		})
	}
}

// TestTickSizeBandBoundaries pins the boundary values THEMSELVES.
//
// Each band is half-open [lo, hi), so the boundary price belongs to the band that STARTS
// there. These five cases are the whole reason the table is written as a chain of `price < hi`
// comparisons; an accidental `<=` anywhere would produce a tick one tier too small and a price
// the exchange rejects without explanation.
func TestTickSizeBandBoundaries(t *testing.T) {
	cases := []struct {
		price float64
		want  float64
	}{
		{10, 0.05}, // not 0.01
		{50, 0.1},  // not 0.05
		{100, 0.5}, // not 0.1
		{500, 1},   // not 0.5
		{1000, 5},  // not 1
	}
	for _, c := range cases {
		got, ok := TickSize(c.price)
		if !ok {
			t.Fatalf("TickSize(%v) ok=false, want true", c.price)
		}
		if math.Abs(got-c.want) > eps {
			t.Fatalf("TickSize(%v) = %v, want %v (bands are [lo, hi))", c.price, got, c.want)
		}
	}
}

// TestTickSizeRejectsNonPrices is the MISSING ≠ ZERO contract for TickSize: no default tick.
func TestTickSizeRejectsNonPrices(t *testing.T) {
	cases := []struct {
		name  string
		price float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"zero", 0},
		{"negative", -12.5},
		{"negative tiny", -1e-9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tick, ok := TickSize(c.price)
			if ok {
				t.Fatalf("TickSize(%v) ok=true (tick %v), want false", c.price, tick)
			}
			if tick != 0 {
				t.Fatalf("TickSize(%v) returned tick %v alongside ok=false, want 0", c.price, tick)
			}
		})
	}
}

// TestTickSizeMonotonic asserts the table never gets FINER as price rises.
//
// A non-monotonic table would break the limit-price direction guarantee: rounding a higher
// price into the band could then land further from it than rounding a lower one.
func TestTickSizeMonotonic(t *testing.T) {
	prices := []float64{
		0.01, 0.5, 1, 5, 9.99, 10, 10.05, 25, 49.95, 50, 50.1, 75, 99.9,
		100, 100.5, 250, 499.5, 500, 501, 750, 999, 1000, 1005, 2500, 1e6,
	}
	prev, ok := TickSize(prices[0])
	if !ok {
		t.Fatalf("TickSize(%v) ok=false, want true", prices[0])
	}
	for _, p := range prices[1:] {
		tick, ok := TickSize(p)
		if !ok {
			t.Fatalf("TickSize(%v) ok=false, want true", p)
		}
		if tick < prev-eps {
			t.Fatalf("tick decreased at price %v: %v after %v", p, tick, prev)
		}
		prev = tick
	}
}

// TestRoundToTickDirections is the arithmetic table. Every expectation was computed by hand
// from the tick table, never by running the code under test.
func TestRoundToTickDirections(t *testing.T) {
	cases := []struct {
		name     string
		price    float64
		tick     float64 // documentation: the band that applies
		down, up float64
		nearest  float64
	}{
		// 1245.3 is the motivating bug: round1() prints it as a price, and it is not one.
		{"1245.3 in the 5 band", 1245.3, 5, 1245, 1250, 1245},
		{"52.23 in the 0.1 band", 52.23, 0.1, 52.2, 52.3, 52.2},
		{"33.33 in the 0.05 band", 33.33, 0.05, 33.30, 33.35, 33.35},
		{"8.888 in the 0.01 band", 8.888, 0.01, 8.88, 8.89, 8.89},
		// 250.25 is an exact half-tick: the tie goes UP.
		{"250.25 ties on the 0.5 band", 250.25, 0.5, 250.0, 250.5, 250.5},
		// 750.5 is an exact half-tick in the 1 band: the tie goes UP.
		{"750.5 ties on the 1 band", 750.5, 1, 750, 751, 751},
		// Already on the grid: all three directions agree and nothing moves.
		{"1245 already on grid", 1245, 5, 1245, 1245, 1245},
		{"36.85 already on grid", 36.85, 0.05, 36.85, 36.85, 36.85},
		{"9.9 already on grid", 9.9, 0.01, 9.9, 9.9, 9.9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if tick, ok := TickSize(c.price); !ok || math.Abs(tick-c.tick) > eps {
				t.Fatalf("precondition: TickSize(%v) = (%v, %v), want (%v, true)", c.price, tick, ok, c.tick)
			}
			for _, d := range []struct {
				dir  RoundDir
				want float64
			}{
				{RoundDown, c.down},
				{RoundUp, c.up},
				{RoundNearest, c.nearest},
			} {
				got, ok := RoundToTick(c.price, d.dir)
				if !ok {
					t.Fatalf("RoundToTick(%v, %v) ok=false, want true", c.price, d.dir)
				}
				if math.Abs(got-d.want) > eps {
					t.Fatalf("RoundToTick(%v, %v) = %v, want %v", c.price, d.dir, got, d.want)
				}
			}
		})
	}
}

// TestRoundToTickUsesInputBand pins that the tick comes from the price being aligned, not from
// the answer. 999.20 pays the [500,1000) tick of 1, so rounding up crosses into the next band
// and lands on 1000.00 — it does not re-derive a tick of 5 from its own result.
func TestRoundToTickUsesInputBand(t *testing.T) {
	got, ok := RoundToTick(999.20, RoundUp)
	if !ok {
		t.Fatalf("RoundToTick(999.20, UP) ok=false, want true")
	}
	if math.Abs(got-1000) > eps {
		t.Fatalf("RoundToTick(999.20, UP) = %v, want 1000", got)
	}
}

// TestRoundToTickDirectionGuarantee: DOWN never returns more than its input, UP never less.
// This is the property the limit-price clamp rests on.
//
// Whole-cent inputs — every price the exchange can quote — are checked with ==/<=/>= and NO
// tolerance. Sub-cent inputs are checked against the fixed snapPriceTol, because the snap is
// allowed to move a value that sits a hair inside a grid point, and that allowance is 0.0001 元
// at 8.88 and 0.0001 元 at 999995 alike.
func TestRoundToTickDirectionGuarantee(t *testing.T) {
	centPrices := []float64{
		0.01, 0.07, 1.23, 8.88, 9.99, 10, 10.03, 33.33, 49.99, 50, 52.23,
		88.87, 99.99, 100, 100.01, 250.25, 499.99, 500, 500.5, 750.5, 999.2,
		1000, 1245.3, 1367.7, 2500.5, 9999.9, 19795, 999995, 999999.99, 1e6,
	}
	for _, p := range centPrices {
		down, ok := RoundToTick(p, RoundDown)
		if !ok {
			t.Fatalf("RoundToTick(%v, DOWN) ok=false, want true", p)
		}
		if down > p {
			t.Fatalf("RoundDown(%.17g) = %.17g, which is ABOVE the input by %g", p, down, down-p)
		}

		up, ok := RoundToTick(p, RoundUp)
		if !ok {
			t.Fatalf("RoundToTick(%v, UP) ok=false, want true", p)
		}
		if up < p {
			t.Fatalf("RoundUp(%.17g) = %.17g, which is BELOW the input by %g", p, up, p-up)
		}

		if down > up {
			t.Fatalf("RoundDown(%v)=%v exceeds RoundUp(%v)=%v", p, down, p, up)
		}
	}

	// Sub-cent inputs: not prices, but callers compute them (a mean, an ATR multiple), so the
	// overshoot they can produce is bounded by a constant rather than left open.
	subCent := []float64{1.234, 8.888, 9.999, 33.333, 250.255, 999.995, 999999.999}
	for _, p := range subCent {
		down, ok := RoundToTick(p, RoundDown)
		if !ok {
			t.Fatalf("RoundToTick(%v, DOWN) ok=false, want true", p)
		}
		if down > p+snapPriceTol {
			t.Fatalf("RoundDown(%.17g) = %.17g, above its input by %g, more than snapPriceTol %g",
				p, down, down-p, snapPriceTol)
		}

		up, ok := RoundToTick(p, RoundUp)
		if !ok {
			t.Fatalf("RoundToTick(%v, UP) ok=false, want true", p)
		}
		if up < p-snapPriceTol {
			t.Fatalf("RoundUp(%.17g) = %.17g, below its input by %g, more than snapPriceTol %g",
				p, up, p-up, snapPriceTol)
		}

		if down > up {
			t.Fatalf("RoundDown(%v)=%v exceeds RoundUp(%v)=%v", p, down, p, up)
		}
	}
}

// TestRoundToTickIsCentExact is the floating-point contract: a returned price is always a whole
// number of cents. It covers 0.05 and 0.5 specifically, the two ticks with no exact binary
// representation, which are where math.Floor(price/tick)*tick produces 1245.0000000000002.
func TestRoundToTickIsCentExact(t *testing.T) {
	// [10,50) pays 0.05; [100,500) pays 0.5.
	prices := []float64{
		10.0, 10.01, 10.03, 12.37, 19.999, 23.33, 33.333, 41.07, 49.99, 49.999,
		100.0, 100.01, 100.37, 123.45, 250.25, 250.27, 333.33, 411.11, 499.99, 499.999,
	}
	for _, p := range prices {
		tick, ok := TickSize(p)
		if !ok {
			t.Fatalf("TickSize(%v) ok=false, want true", p)
		}
		if math.Abs(tick-0.05) > eps && math.Abs(tick-0.5) > eps {
			t.Fatalf("precondition: price %v has tick %v, want 0.05 or 0.5", p, tick)
		}
		for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
			got, ok := RoundToTick(p, dir)
			if !ok {
				t.Fatalf("RoundToTick(%v, %v) ok=false, want true", p, dir)
			}
			cents := got * 100
			if drift := math.Abs(cents - math.Round(cents)); drift > 1e-9 {
				t.Fatalf("RoundToTick(%v, %v) = %.17g; ×100 = %.17g is not a whole number of cents (drift %g)",
					p, dir, got, cents, drift)
			}
			// And BIT-EXACTLY the float64 nearest that cent count. This is the assertion that
			// the loose drift check above cannot make: math.Floor(price/tick)*tick returns
			// 33.300000000000004 for 33.333, whose drift from a whole cent is only 5e-13 yet
			// which prints, serialises and compares as a number that is not 33.30.
			if want := math.Round(cents) / 100; got != want {
				t.Fatalf("RoundToTick(%v, %v) = %.17g, want the exact float for %v cents, %.17g",
					p, dir, got, math.Round(cents), want)
			}
			// And the result must itself be a multiple of the tick, in cents.
			tickCents := math.Round(tick * 100)
			if rem := math.Mod(math.Round(cents), tickCents); rem != 0 {
				t.Fatalf("RoundToTick(%v, %v) = %v is not a multiple of tick %v (remainder %v cents)",
					p, dir, got, tick, rem)
			}
		}
	}
}

// TestRoundToTickRejects covers every ok=false path.
func TestRoundToTickRejects(t *testing.T) {
	cases := []struct {
		name  string
		price float64
		dir   RoundDir
		why   string
	}{
		{"NaN", math.NaN(), RoundDown, "TickSize rejects it"},
		{"+Inf", math.Inf(1), RoundDown, "TickSize rejects it"},
		{"-Inf", math.Inf(-1), RoundUp, "TickSize rejects it"},
		{"zero", 0, RoundNearest, "TickSize rejects it"},
		{"negative", -100, RoundDown, "TickSize rejects it"},
		{"just above the price domain", 1000000.01, RoundDown, "1e6 is the top of the domain"},
		{"far above the price domain", 1e14, RoundDown, "not a price by any reading"},
		// A sub-tick price rounded DOWN falls off the bottom of the grid. 0.00 is the correct
		// multiple and is not a price, so the answer is ok=false rather than (0, true).
		{"sub-tick rounded down", 0.004, RoundDown, "would return 0.00 as a price"},
		{"undeclared direction", 100, RoundDir(42), "not one of the three constants"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RoundToTick(c.price, c.dir)
			if ok {
				t.Fatalf("RoundToTick(%v, %v) ok=true (got %v), want false because %s", c.price, c.dir, got, c.why)
			}
			if got != 0 {
				t.Fatalf("RoundToTick(%v, %v) returned %v alongside ok=false, want 0", c.price, c.dir, got)
			}
		})
	}
}

// TestRoundDirString keeps the debug labels honest, including the undeclared case.
func TestRoundDirString(t *testing.T) {
	cases := map[RoundDir]string{
		RoundDown:    "DOWN",
		RoundUp:      "UP",
		RoundNearest: "NEAREST",
		RoundDir(42): "INVALID",
	}
	for dir, want := range cases {
		if got := dir.String(); got != want {
			t.Fatalf("RoundDir(%d).String() = %q, want %q", int(dir), got, want)
		}
	}
}

// meanOf returns the arithmetic mean of n copies of v, accumulated the way an SMA is.
//
// It exists to manufacture a value that IS v mathematically but is a lossy low-side float64
// image of it — the input shape that makes snapCentTol necessary. Nothing in the package
// under test does this; the test does it so the package does not have to trust that callers
// hand it pristine literals.
func meanOf(v float64, n int) float64 {
	s := 0.0
	for i := 0; i < n; i++ {
		s += v
	}
	return s / float64(n)
}

// TestRoundToTickAbsorbsRepresentationNoise covers the snap documented on snapCentTol.
//
// Every input here is mathematically an exact tick multiple, so all three directions must
// return it unchanged. Without the snap, RoundDown loses a whole tick and returns a value that
// is not even a whole cent — the naiveWithoutSnap column records what the unsnapped code
// actually produced, measured, so this test fails loudly if the snap is ever removed.
func TestRoundToTickAbsorbsRepresentationNoise(t *testing.T) {
	cases := []struct {
		name             string
		price            float64
		want             float64
		naiveWithoutSnap float64
	}{
		{"one ULP below 36.85 (tick 0.05)", math.Nextafter(36.85, 0), 36.85, 36.799999999999997},
		{"one ULP below 47.75 (tick 0.05)", math.Nextafter(47.75, 0), 47.75, 47.700000000000003},
		{"one ULP below 250.5 (tick 0.5)", math.Nextafter(250.5, 0), 250.5, 250},
		{"one ULP below 1365 (tick 5)", math.Nextafter(1365, 0), 1365, 1360},
		{"one ULP below 661 (tick 1)", math.Nextafter(661, 0), 661, 660},
		{"one ULP below 88.7 (tick 0.1)", math.Nextafter(88.7, 0), 88.7, 88.599999999999994},
		{"one ULP below 8.88 (tick 0.01)", math.Nextafter(8.88, 0), 8.88, 8.8699999999999992},
		// The realistic origin: an averaged level, not a hand-written literal.
		{"60-term mean of 36.85", meanOf(36.85, 60), 36.85, 36.799999999999997},
		{"250-term mean of 12.35", meanOf(12.35, 250), 12.35, 12.300000000000001},
		{"1000-term mean of 88.7", meanOf(88.7, 1000), 88.7, 88.599999999999994},
		// And the mirror: a high-side image must not be ceiled up a whole tick either.
		{"one ULP above 36.85", math.Nextafter(36.85, 100), 36.85, 36.85},
		{"one ULP above 1365", math.Nextafter(1365, 1e6), 1365, 1365},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Precondition: the input really is an off-grid float64 image of c.want.
			if c.price == c.want {
				t.Fatalf("precondition: input %.17g is bit-identical to %v, so it tests nothing", c.price, c.want)
			}
			for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
				got, ok := RoundToTick(c.price, dir)
				if !ok {
					t.Fatalf("RoundToTick(%.17g, %v) ok=false, want true", c.price, dir)
				}
				if got != c.want {
					t.Fatalf("RoundToTick(%.17g, %v) = %.17g, want exactly %v (unsnapped code gives %.17g)",
						c.price, dir, got, c.want, c.naiveWithoutSnap)
				}
			}
		})
	}
}

// TestRoundToTickDoesNotAbsorbRealDifferences is the other half of the snap contract: the
// tolerance is for float noise only and must never swallow a difference a trader could see. One
// cent is 0.002 tick counts at the coarsest tick (5.00) and the threshold there is 0.00002 —
// 100× smaller, the same ratio as at every other tier — so it survives.
func TestRoundToTickDoesNotAbsorbRealDifferences(t *testing.T) {
	cases := []struct {
		price float64
		dir   RoundDir
		want  float64
		why   string
	}{
		{1364.99, RoundDown, 1360, "one cent below a 5.00 grid point is still below it"},
		{1360.01, RoundUp, 1365, "one cent above a 5.00 grid point is still above it"},
		{36.84, RoundDown, 36.80, "one cent below a 0.05 grid point"},
		{36.81, RoundUp, 36.85, "one cent above a 0.05 grid point"},
		{250.49, RoundDown, 250.00, "one cent below a 0.5 grid point"},
		{88.69, RoundDown, 88.60, "one cent below a 0.1 grid point"},
	}
	for _, c := range cases {
		got, ok := RoundToTick(c.price, c.dir)
		if !ok {
			t.Fatalf("RoundToTick(%v, %v) ok=false, want true", c.price, c.dir)
		}
		if math.Abs(got-c.want) > eps {
			t.Fatalf("RoundToTick(%v, %v) = %v, want %v — %s", c.price, c.dir, got, c.want, c.why)
		}
	}
}

// TestAlignToTickCentsRejectsNonPositiveTick reaches the guard that RoundToTick itself cannot,
// because every tick in the TickSize table is a positive whole number of cents. It is here so
// that a future sub-cent band degrades to ok=false rather than dividing by zero and converting
// an infinity to int64.
func TestAlignToTickCentsRejectsNonPositiveTick(t *testing.T) {
	for _, tickCents := range []int64{0, -1, -500} {
		for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
			if v, ok := alignToTickCents(100, tickCents, dir); ok || v != 0 {
				t.Fatalf("alignToTickCents(100, %d, %v) = (%v, %v), want (0, false)", tickCents, dir, v, ok)
			}
		}
	}
}

// snapTiers is one exact grid point near the TOP of each tick tier's reachable range, plus the
// tier's tick. The top of the range is the interesting end: a threshold that scales with the
// price is at its widest there, which is exactly where the old relative threshold went wrong.
// The last row sits at the top of the whole domain (maxAlignablePrice is 1e6).
var snapTiers = []struct {
	name string
	tick float64
	grid float64
}{
	{"tick 0.01, top of [0,10)", 0.01, 9.99},
	{"tick 0.05, top of [10,50)", 0.05, 49.95},
	{"tick 0.1, top of [50,100)", 0.1, 99.9},
	{"tick 0.5, top of [100,500)", 0.5, 499.5},
	{"tick 1, top of [500,1000)", 1, 999},
	{"tick 5, top of the domain", 5, 999995},
}

// measuredSnapWidth finds the largest deviation from `grid` that RoundToTick still treats as
// being ON the grid, by bisecting on OBSERVED BEHAVIOUR rather than by reading the constant.
//
// Measuring behaviour is the point. An assertion about snapCentTol would keep passing if the
// comparison in alignToTickCents were changed to widen the threshold some other way — back to a
// price-proportional form, or to a max() of the two. This returns the width the code actually
// has, in 元, whatever shape the code is written in.
func measuredSnapWidth(t *testing.T, grid, tick float64, dir RoundDir) float64 {
	t.Helper()

	onGrid := func(delta float64) bool {
		p := grid - delta
		if dir == RoundUp {
			p = grid + delta
		}
		v, ok := RoundToTick(p, dir)
		if !ok {
			t.Fatalf("RoundToTick(%.17g, %v) ok=false, want true", p, dir)
		}
		return v == grid
	}

	// delta 0 is on the grid trivially. Half a tick is half a tick count from the nearest
	// integer, the furthest any value can be, so it must not snap under any sane threshold.
	lo, hi := 0.0, tick/2
	if onGrid(hi) {
		t.Fatalf("grid %v (tick %v), %v: a deviation of half a tick (%g) still lands on the grid; the snap threshold is not sub-tick at all",
			grid, tick, dir, hi)
	}
	for i := 0; i < 200; i++ {
		mid := lo + (hi-lo)/2
		if mid == lo || mid == hi {
			break
		}
		if onGrid(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// TestSnapThresholdIsSubCentAtEveryTier is the assertion the old relative threshold could not
// survive, stated over the WHOLE accepted domain rather than over the prices that happen to
// occur in practice.
//
// The threshold is measured at every tick tier, at a price near the top of that tier and — for
// the coarsest tier — near the top of the domain, then converted back into 元 and compared with
// the two smallest genuine quantities in this package:
//
//	one cent (0.01)        the smallest difference between two prices the exchange can quote;
//	one tenth-cent (0.001) the grid that ±10% of a whole-cent price lands on, since
//	                       95909 cents × 1.1 = 105499.9 cents exactly.
//
// The tenth-cent bound is the binding one and it is not theoretical: with a threshold of 0.1
// cents, prevClose 959.09 yields a limit up of 1055.00 when the regulation permits 1054.999 and
// the legal ceiling is 1050.00.
//
// What this test would have caught. Before the fix the threshold was tickSnapRelTol × max(1,
// ticks), which at the top of the domain measures 0.001 元 — exactly the tenth-cent bound, no
// margin at all — and beyond the old 1e13 domain measured more than a whole unit.
func TestSnapThresholdIsSubCentAtEveryTier(t *testing.T) {
	const (
		oneCent     = 0.01
		oneTenthCen = 0.001
	)
	for _, tier := range snapTiers {
		t.Run(tier.name, func(t *testing.T) {
			for _, dir := range []RoundDir{RoundDown, RoundUp} {
				width := measuredSnapWidth(t, tier.grid, tier.tick, dir)

				if width <= 0 {
					t.Fatalf("%v: measured snap width is %g — the snap is gone, and one ULP of input error now costs a whole tick", dir, width)
				}
				if width >= oneTenthCen {
					t.Fatalf("%v at grid %v: measured snap width %g 元 reaches a tenth of a cent (%g); ±10%% of a whole-cent price lands on that grid, so a real difference is being absorbed",
						dir, tier.grid, width, oneTenthCen)
				}
				if width >= oneCent {
					t.Fatalf("%v at grid %v: measured snap width %g 元 reaches one cent (%g); two prices the exchange can tell apart are being merged",
						dir, tier.grid, width, oneCent)
				}
				// And with stated margin, not merely "less than".
				if got := oneTenthCen / width; got < 5 {
					t.Fatalf("%v at grid %v: snap width %g 元 leaves only %.2f× margin under a tenth of a cent, want >= 5×",
						dir, tier.grid, width, got)
				}
				if got := oneCent / width; got < 50 {
					t.Fatalf("%v at grid %v: snap width %g 元 leaves only %.2f× margin under one cent, want >= 50×",
						dir, tier.grid, width, got)
				}
				// The width is the SAME at every tier, because both the threshold and a
				// difference in cents divide by tickCents. That tier-independence is the
				// property the absolute form has and the relative form did not.
				if math.Abs(width-snapPriceTol) > snapPriceTol*0.05 {
					t.Fatalf("%v at grid %v: measured snap width %g 元, want snapPriceTol %g 元 (±5%%) at EVERY tier",
						dir, tier.grid, width, snapPriceTol)
				}
			}
		})
	}
}

// TestSnapThresholdBrackets is the same contract as explicit numbers, one tier per row, for a
// reader who would rather see values than a bisection.
//
// Inside the threshold: a hundredth of a cent below a grid point is representation noise and
// snaps back. Outside: a tenth of a cent, and a whole cent, are real and must cost a tick.
func TestSnapThresholdBrackets(t *testing.T) {
	for _, tier := range snapTiers {
		t.Run(tier.name, func(t *testing.T) {
			cases := []struct {
				delta float64
				want  float64
				why   string
			}{
				{1e-5, tier.grid, "a hundredth of a cent below the grid is noise: snaps back"},
				{1e-3, tier.grid - tier.tick, "a tenth of a cent below the grid is real: loses a tick"},
				{1e-2, tier.grid - tier.tick, "a whole cent below the grid is real: loses a tick"},
			}
			for _, c := range cases {
				p := tier.grid - c.delta
				got, ok := RoundToTick(p, RoundDown)
				if !ok {
					t.Fatalf("RoundToTick(%.17g, DOWN) ok=false, want true", p)
				}
				if math.Abs(got-c.want) > eps {
					t.Fatalf("RoundToTick(%.17g, DOWN) = %.17g, want %v — %s", p, got, c.want, c.why)
				}
			}
		})
	}
}

// TestRoundToTickDirectionIsBitExactOnCentPrices sweeps whole-cent inputs across the ENTIRE
// accepted domain and asserts the direction guarantee with ==, no tolerance of any kind.
//
// Every price the exchange can quote is a whole number of cents, so this is the direction
// guarantee on the real input set rather than on a hand-picked list. The windows are dense
// around every band edge and at the top of the domain — where a price-scaled threshold is at its
// widest — and a coarse stride covers everything between.
func TestRoundToTickDirectionIsBitExactOnCentPrices(t *testing.T) {
	check := func(t *testing.T, cents int64) {
		t.Helper()
		p := float64(cents) / 100
		if p > maxAlignablePrice {
			t.Fatalf("test bug: %v cents is outside the domain", cents)
		}
		tick, ok := TickSize(p)
		if !ok {
			t.Fatalf("TickSize(%v) ok=false, want true", p)
		}
		tickCents := int64(math.Round(tick * 100))

		down, ok := RoundToTick(p, RoundDown)
		if !ok {
			t.Fatalf("RoundToTick(%.17g, DOWN) ok=false, want true", p)
		}
		if down > p {
			t.Fatalf("RoundDown(%.17g) = %.17g, ABOVE its input by %g — the limit-price clamp rests on this never happening",
				p, down, down-p)
		}
		up, ok := RoundToTick(p, RoundUp)
		if !ok {
			t.Fatalf("RoundToTick(%.17g, UP) ok=false, want true", p)
		}
		if up < p {
			t.Fatalf("RoundUp(%.17g) = %.17g, BELOW its input by %g", p, up, p-up)
		}
		if down > up {
			t.Fatalf("price %.17g: RoundDown %v exceeds RoundUp %v", p, down, up)
		}
		// An input already on the grid must not move at all, in either direction.
		if cents%tickCents == 0 {
			if down != p || up != p {
				t.Fatalf("price %.17g is an exact multiple of tick %v, but DOWN=%.17g UP=%.17g", p, tick, down, up)
			}
		}
	}

	windows := []struct {
		name     string
		from, to int64 // inclusive, in cents
	}{
		{"the bottom of the grid", 1, 2000},
		{"the 10.00 band edge", 99000, 101000},
		{"the 50.00 band edge", 499000, 501000},
		{"the 100.00 band edge", 999000, 1001000},
		{"the 500.00 band edge", 4999000, 5001000},
		{"the 1000.00 band edge", 9999000, 10001000},
		{"the top of the domain", 99998000, 100000000},
	}
	for _, w := range windows {
		t.Run(w.name, func(t *testing.T) {
			for c := w.from; c <= w.to; c++ {
				check(t, c)
			}
		})
	}
	t.Run("a coarse stride over the whole domain", func(t *testing.T) {
		// 99991 is prime-ish and not a multiple of any tickCents, so the stride does not land
		// only on grid points.
		for c := int64(1); c <= 100000000; c += 99991 {
			check(t, c)
		}
	})
}

// TestRoundToTickRejectsNonPriceMagnitudes pins the domain bound at 1e6 by the failures it
// exists to prevent.
//
// The first three rows are the measured defect that this bound and the absolute snap threshold
// were introduced for. With the old 1e13 bound and the old price-proportional threshold,
// RoundToTick answered ok=true with a value ABOVE its own input:
//
//	RoundDown(999999999999)  = 1000000000000   (+1)
//	RoundDown(9999999999.99) = 10000000000     (+0.01)
//	RoundDown(99999999.99)   = 100000000       (+0.01)
//
// The rest are the other way the old bound failed: a number that is not a price at all came back
// tick-aligned and ok=true, which is indistinguishable from an answer at the call site.
func TestRoundToTickRejectsNonPriceMagnitudes(t *testing.T) {
	cases := []struct {
		name  string
		price float64
		why   string
	}{
		{"the +1 violation", 999999999999, "returned 1000000000000, one whole unit above its input"},
		{"the +0.01 violation at 1e10", 9999999999.99, "returned 10000000000, a cent above its input"},
		{"the +0.01 violation at 1e8", 99999999.99, "returned 100000000, a cent above its input"},
		{"a share count", 5000000, "shares, not 元"},
		{"a day's turnover", 1.23e8, "元 of turnover, not a price"},
		{"a market capitalisation", 1.5e13, "元 of market cap, not a price"},
		{"the old domain bound", 1e13, "no security has ever traded near here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
				got, ok := RoundToTick(c.price, dir)
				if ok {
					t.Fatalf("RoundToTick(%.17g, %v) = (%v, true), want ok=false — %s", c.price, dir, got, c.why)
				}
				if got != 0 {
					t.Fatalf("RoundToTick(%.17g, %v) returned %v alongside ok=false, want 0", c.price, dir, got)
				}
			}
		})
	}

	// And the bound itself is inclusive: 1e6 is accepted, one cent more is not. The highest
	// price in the repo's .cache is 19,795 and the highest ever printed on either board is about
	// 6,075, so nothing real is anywhere near this edge.
	if v, ok := RoundToTick(maxAlignablePrice, RoundDown); !ok || v != maxAlignablePrice {
		t.Fatalf("RoundToTick(%v, DOWN) = (%v, %v), want (%v, true)", maxAlignablePrice, v, ok, maxAlignablePrice)
	}
	if v, ok := RoundToTick(maxAlignablePrice+0.01, RoundDown); ok || v != 0 {
		t.Fatalf("RoundToTick(%v, DOWN) = (%v, %v), want (0, false)", maxAlignablePrice+0.01, v, ok)
	}
}
