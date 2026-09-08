package pricerule

import "math"

// TickSize returns the minimum price increment that applies AT this price level.
//
// The table below is a STATED MARKET RULE, not data. It is transcribed from the TWSE trading
// rules (the 股價升降單位 tiers, which TPEX mirrors for equities); it did not come from any
// feed, any fetcher or any cached response, and it is NOT verified against an authorized
// source — no reconciliation gate covers it. It is a constant in this file because a human
// wrote it down, so if the exchange changes a tier this file is the only thing that changes,
// and nothing downstream needs to re-derive anything.
//
//	price       tick
//	[0,    10)  0.01
//	[10,   50)  0.05
//	[50,  100)  0.1
//	[100, 500)  0.5
//	[500, 1000) 1
//	[1000,  ∞)  5
//
// Every band is half-open, [lo, hi): a price of exactly 50 pays 0.1, not 0.05, and a price of
// exactly 1000 pays 5, not 1. The five boundary values are pinned by their own test cases
// because an off-by-one-band tick is the kind of error that produces a price the exchange
// silently rejects rather than an error anyone can see.
//
// ok=false when price is not finite (NaN, ±Inf) or is not positive. There is deliberately no
// default tick for that case: a tick of "probably 0.01" applied to an unknown price is how a
// bad number acquires the appearance of a legal one. MISSING ≠ ZERO.
func TickSize(price float64) (tick float64, ok bool) {
	if !isFinitePositive(price) {
		return 0, false
	}
	switch {
	case price < 10:
		return 0.01, true
	case price < 50:
		return 0.05, true
	case price < 100:
		return 0.1, true
	case price < 500:
		return 0.5, true
	case price < 1000:
		return 1, true
	default:
		return 5, true
	}
}

// RoundDir selects which way RoundToTick moves a price that is not already on the grid.
type RoundDir int

const (
	// RoundDown moves towards zero: the result is never above the input.
	RoundDown RoundDir = iota
	// RoundUp moves away from zero: the result is never below the input.
	RoundUp
	// RoundNearest moves to the closer grid point, with an exact tie going up.
	RoundNearest
)

// String makes a RoundDir readable in a test failure or a log line.
func (d RoundDir) String() string {
	switch d {
	case RoundDown:
		return "DOWN"
	case RoundUp:
		return "UP"
	case RoundNearest:
		return "NEAREST"
	default:
		return "INVALID"
	}
}

// maxAlignablePrice is the largest input RoundToTick will accept.
//
// It is a DOMAIN bound, deliberately drawn near the top of the real world rather than near the
// top of float64. Above it the answer is ok=false; it does not clamp, because a clamped price
// is a fabricated price.
//
// Where 1e6 comes from. The highest price in the repo's own .cache is 19,795 (5274), and the
// highest price ever printed on either Taiwan board is roughly 6,075 (3008, 大立光). 1e6 sits
// two orders of magnitude above that, so no price a caller could legitimately hold is refused,
// and it is eight orders below the 2^53 point where float64 stops holding integers exactly
// (price*100 at the bound is 1e8, whose ULP is 1.5e-8 cents — six orders below the snap
// threshold described on snapCentTol, so cent arithmetic is not merely exact here, it has room
// to spare).
//
// Why not a bound sized for the arithmetic instead (the previous value was 1e13). Two reasons,
// both of them failures observed in review rather than hypotheticals:
//
//   - A number that is not a price at all — a turnover figure, a market capitalisation, a share
//     count — comes back tick-aligned with ok=true from a wide bound, which is exactly the
//     "plausibly wrong" outcome this package exists to prevent. 1e6 refuses all three.
//   - The bound applies to the number being aligned, and the two limit prices are different
//     numbers (prevClose × 1.10 and × 0.90). A bound reachable by real arithmetic therefore
//     produced an internally inconsistent pair — a security with a limit-down price and no
//     limit-up price. limitPrices now computes both ends and fails as a pair, and this bound is
//     far enough out that the pair-failure branch is only ever reached by garbage.
const maxAlignablePrice = 1e6

// snapCentTol is how close to an exact tick boundary counts as being ON that boundary,
// expressed in CENTS. snapPriceTol is the same quantity in 元.
//
// This is a representation correction, not a rounding policy, and it is load-bearing rather
// than defensive. A price that IS an exact multiple of 0.05 can reach this function as a
// float64 sitting a hair BELOW that multiple, and floor() then drops a whole tick:
//
//	math.Nextafter(36.85, 0)  ->  36.849999999999994  ->  *100 = 3684.9999999999995
//	                          ->  736.99999999999989 ticks  ->  floor 736  ->  36.80
//
// One ULP of input error, 0.05 of output error, and the result is not even a whole cent
// (36.799999999999997). One ULP is nothing: a 60-term mean of 36.85 lands at
// 36.849999999999959, and any SMA/VWAP/ATR-derived level a caller passes in carries error of
// that order or worse. Note that a SINGLE multiply does not trigger it — 33.5*1.1 is
// 36.850000000000001, on the high side, and *100 rounds it back to exactly 3685 — so the
// failure mode only appears once the input has been through a longer computation, which is
// exactly when nobody is looking for it.
//
// The threshold is ABSOLUTE in price space, and that is the whole point of its shape. It was
// relative (1e-9 of the tick count) and that was a real defect: a relative threshold grows with
// the price while a genuine difference does not, so past a crossover it starts absorbing
// differences a trader can see. Measured, before the fix: RoundToTick(99999999.99, RoundDown)
// returned 100000000, i.e. a cent ABOVE its own input, and RoundToTick(999999999999, RoundDown)
// returned a value a whole unit above its input. Narrowing the domain hides that; it does not
// answer it. An absolute threshold answers it at every price, and the proof below needs no
// case analysis on the price at all.
//
// Why 0.01 cents, and what the margin is at each tier. Convert everything into tick counts,
// which is the space the comparison happens in. A difference of d cents is d/tickCents tick
// counts, and the threshold is snapCentTol/tickCents tick counts, so the RATIO of the two is
// snapCentTol/d — the tickCents cancels, and the margin is therefore IDENTICAL at every tier:
//
//	tick  tickCents  threshold    one tenth-cent  one cent   margin vs 0.1c  margin vs 1c
//	0.01    1        0.01         0.1             1          10×             100×
//	0.05    5        0.002        0.02            0.2        10×             100×
//	0.1    10        0.001        0.01            0.1        10×             100×
//	0.5    50        0.0002       0.002           0.02       10×             100×
//	1     100        0.0001       0.001           0.01       10×             100×
//	5     500        0.00002      0.0002          0.002      10×             100×
//
// (Columns after tickCents are in tick counts.) One cent — the smallest difference between two
// prices the exchange can quote — is 100× the threshold at every tier and is never absorbed;
// that is tested. And no threshold can pull a value across a real tick boundary in any case:
// the nearest boundary is 0.5 tick counts away, which is 50× the threshold even at the finest
// tier, where the threshold is at its largest.
//
// The 10× column is the binding one, and it is why the threshold is not the more obvious 0.1
// cents. One cent is NOT the finest genuine quantity in this package: ±10% of a whole-cent
// price lands on a TENTH-of-a-cent grid (95909 cents × 1.1 = 105499.9 cents exactly), so a
// threshold of 0.1 cents absorbs a real difference there. Measured with a 0.1-cent threshold:
// prevClose 959.09 → limit up 1055.00, when the exact +10% is 1054.999 and the legal ceiling is
// 1050.00 — a fabricated price a full tick above what the regulation permits, reachable from an
// ordinary four-digit share price. A sweep of every whole-cent prevClose from 0.01 to 2000
// produced 316 such illegal ceilings and 215 illegal floors at 0.1 cents, and none at 0.01
// cents. That sweep is now a test.
//
// The other end: the threshold has to stay far above the float64 noise it exists to absorb. The
// largest tick count anywhere in the domain is 2e5 (1e6 元 at a tick of 5; every finer tier caps
// out near 1e3), and a few ULP of 2e5 is about 1e-10 tick counts — five orders of magnitude
// below 0.00002, the smallest threshold in the table. A 1000-term mean carries error of the same
// order. There is no price in the domain where the threshold is tight.
//
// The price of the snap, stated plainly: RoundDown can return a value up to snapPriceTol (0.0001
// 元) ABOVE its input, and RoundUp the same distance below, when the input sits a hair inside a
// grid point. That is the fixed, price-independent cost of not losing a whole tick to one ULP.
// For any input that is a whole number of cents — that is, for every actual price — the
// direction guarantee holds bit-exactly, because the nearest wrong answer is 100× away. Both
// halves are tested: the bit-exact one on cent-valued inputs across the whole domain, and the
// bounded-overshoot one with the fixed constant, never a price-scaled tolerance.
const (
	snapCentTol  = 0.01
	snapPriceTol = snapCentTol / 100
)

// RoundToTick aligns price onto the tick grid in the requested direction.
//
// The tick comes from TickSize(price) — the price BEING aligned, not the result. That matters
// at a band edge: RoundUp(999.20) uses the [500,1000) tick of 1 and answers 1000.00, and does
// not re-derive a tick of 5 from its own answer. ok=false whenever TickSize says ok=false.
//
// The arithmetic is integer on purpose and this is the one thing in the package worth reading
// twice. Every Taiwan tick is a whole number of cents, so the price is expressed as a count of
// ticks, the count is floored/ceiled/rounded as a whole number, and the answer is rebuilt as
// (count × tickCents) cents. The result is therefore an exact integer number of cents by
// construction. The obvious alternative, math.Floor(price/tick)*tick, is what produces
// 1245.0000000000002, and it is not used here or anywhere else in this package.
//
// ok=false, with no value, in four cases:
//
//   - TickSize rejected the price (non-finite, non-positive);
//   - the price is above maxAlignablePrice, i.e. outside the domain of things that are prices;
//   - dir is not one of the three declared constants;
//   - the aligned result would be zero or negative, i.e. a sub-tick price rounded DOWN off the
//     bottom of the grid. 0.00 is arithmetically the correct multiple there, but it is not a
//     price, and handing a caller 0.00 with ok=true is precisely the failure this package
//     exists to prevent. Note the asymmetry this creates on purpose: TickSize(0.004) succeeds
//     (0.004 is in the [0,10) band) while RoundToTick(0.004, RoundDown) does not.
func RoundToTick(price float64, dir RoundDir) (float64, bool) {
	tick, ok := TickSize(price)
	if !ok {
		return 0, false
	}
	if price > maxAlignablePrice {
		return 0, false
	}

	// tickCents is exact for every entry in the TickSize table: 1, 5, 10, 50, 100, 500.
	return alignToTickCents(price, int64(math.Round(tick*100)), dir)
}

// alignToTickCents is the integer core of RoundToTick, split out so the tickCents <= 0 guard is
// reachable from a test. Via RoundToTick it is not: every tick in the table is a positive whole
// number of cents. The guard exists so that adding a sub-cent band later fails as ok=false
// instead of dividing by zero and handing int64() an infinity.
//
// price is assumed already validated (finite, positive, within maxAlignablePrice).
func alignToTickCents(price float64, tickCents int64, dir RoundDir) (float64, bool) {
	if tickCents <= 0 {
		return 0, false
	}

	ticks := price * 100 / float64(tickCents)

	// snapCentTol cents, converted into this tier's tick counts. Dividing by tickCents is what
	// makes the threshold mean the same distance in 元 at every tier — 0.0001 元 — instead of a
	// distance that grows with the tick or with the price. See snapCentTol.
	tol := snapCentTol / float64(tickCents)
	if nearest := math.Round(ticks); math.Abs(ticks-nearest) <= tol {
		ticks = nearest
	}

	var count float64
	switch dir {
	case RoundDown:
		count = math.Floor(ticks)
	case RoundUp:
		count = math.Ceil(ticks)
	case RoundNearest:
		// Half-up. price is positive here, so "up" and "away from zero" coincide.
		count = math.Floor(ticks + 0.5)
	default:
		return 0, false
	}
	if count <= 0 {
		return 0, false
	}

	cents := int64(count) * tickCents
	return float64(cents) / 100, true
}

// isFinitePositive is the input gate shared by every exported function here.
//
// It rejects NaN, ±Inf and anything <= 0, and it does not repair them. There is no
// `if price <= 0 { price = something }` in this package.
func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}
