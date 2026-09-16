package pricerule

import (
	"math"
	"testing"
)

func TestClassifyLimitRule(t *testing.T) {
	cases := []struct {
		name string
		code string
		want LimitRule
		why  string
	}{
		{"TSMC", "2330", LimitRule10Pct, "4 digits, ordinary common share"},
		{"cement", "1101", LimitRule10Pct, "4 digits"},
		{"TPEX 4-digit", "6505", LimitRule10Pct, "TPEX shares follow the same ±10%"},
		{"high 4-digit", "9958", LimitRule10Pct, "4 digits"},
		{"leading 1", "1234", LimitRule10Pct, "only a leading \"00\" is excluded"},

		// The "00" ETF block. 0050 tracks domestic constituents and DOES have ±10%; 00646
		// tracks foreign constituents and has NO daily limit. The code cannot tell them apart,
		// and stockcatalog.Stock records no instrument class, so both are UNKNOWN.
		{"domestic ETF", "0050", LimitRuleUnknown, "\"00\" block: cannot tell domestic from foreign"},
		{"dividend ETF", "0056", LimitRuleUnknown, "\"00\" block"},
		{"foreign-constituent ETF", "00646", LimitRuleUnknown, "\"00\" block, and no daily limit at all"},
		{"inverse ETF", "00632R", LimitRuleUnknown, "\"00\" block with a letter suffix"},
		{"futures ETF", "00400A", LimitRuleUnknown, "\"00\" block with a letter suffix"},

		{"warrant", "031234", LimitRuleUnknown, "6 digits: warrant, its own limit mechanics"},
		{"6 digits", "123456", LimitRuleUnknown, "wrong length"},
		{"empty", "", LimitRuleUnknown, "no code at all"},
		{"letters in a 4-char code", "23A0", LimitRuleUnknown, "not 4 digits"},
		{"preferred-share style", "2882A", LimitRuleUnknown, "5 chars, not 4 digits"},
		{"3 digits", "233", LimitRuleUnknown, "wrong length"},
		{"5 digits", "23300", LimitRuleUnknown, "wrong length"},
		{"full-width digits", "２３３０", LimitRuleUnknown, "multi-byte runes are not ASCII digits"},
		{"whitespace only", "   ", LimitRuleUnknown, "trims to empty"},
		// Surrounding whitespace is trimmed: catalogue and CSV inputs carry it routinely.
		{"padded code", " 2330 ", LimitRule10Pct, "surrounding whitespace is trimmed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyLimitRule(c.code); got != c.want {
				t.Fatalf("ClassifyLimitRule(%q) = %q, want %q (%s)", c.code, got, c.want, c.why)
			}
		})
	}
}

// TestLimitPricesKnownValues. Every expectation is hand-computed: prevClose × 1.10 or × 0.90,
// then aligned to the tick of the RESULT's band — down for the ceiling, up for the floor. The
// code under test was not used to produce any of these numbers.
func TestLimitPricesKnownValues(t *testing.T) {
	cases := []struct {
		name      string
		prevClose float64
		wantUp    float64
		wantDown  float64
		note      string
	}{
		// 1245 → 1369.5 / 1120.5, both in the 5.00 band: down to 1365, up to 1125.
		{"1245", 1245, 1365, 1125, "the round1() bug case: 1369.5 is not a tradable price"},
		// 601 → 661.1 / 540.9, both in the 1.00 band.
		{"601", 601, 661, 541, ""},
		// 990 → 1089.0 (crosses into the 5.00 band, floors to 1085) / 891.0 (1.00 band).
		{"990 crosses the 1000 boundary", 990, 1085, 891, "the limit tick differs from prevClose's tick"},
		// 500 → 550.0 (1.00 band, already on grid) / 450.0 (0.5 band, already on grid).
		{"500", 500, 550, 450, ""},
		// 47.5 → 52.25 (0.1 band, floors to 52.2) / 42.75 (0.05 band, already on grid).
		{"47.5", 47.5, 52.2, 42.75, ""},
		// 33.5 → 36.85 / 30.15, both exact multiples of 0.05 that float64 cannot hold exactly.
		{"33.5", 33.5, 36.85, 30.15, "float noise must not cost a whole tick"},
		// 9.87 → 10.857 (crosses into the 0.05 band, floors to 10.85) / 8.883 (0.01 band, ceils to 8.89).
		{"9.87 crosses the 10 boundary", 9.87, 10.85, 8.89, "sub-cent input must not be truncated before rounding"},
		// 100 → 110.0 (0.5 band) / 90.0 (0.1 band), both already on grid.
		{"100", 100, 110, 90, ""},
		// 9 → 9.9 / 8.1, both in the 0.01 band and both exact multiples.
		{"9", 9, 9.9, 8.1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up, ok := LimitUpPrice(c.prevClose, LimitRule10Pct)
			if !ok {
				t.Fatalf("LimitUpPrice(%v, PCT_10) ok=false, want true", c.prevClose)
			}
			if math.Abs(up-c.wantUp) > eps {
				t.Fatalf("LimitUpPrice(%v) = %.17g, want %v  %s", c.prevClose, up, c.wantUp, c.note)
			}
			down, ok := LimitDownPrice(c.prevClose, LimitRule10Pct)
			if !ok {
				t.Fatalf("LimitDownPrice(%v, PCT_10) ok=false, want true", c.prevClose)
			}
			if math.Abs(down-c.wantDown) > eps {
				t.Fatalf("LimitDownPrice(%v) = %.17g, want %v  %s", c.prevClose, down, c.wantDown, c.note)
			}
		})
	}
}

// TestLimitPricesRoundIntoTheBand is the directionality contract that makes these safe to clamp
// with: the ceiling is never ABOVE +10% and the floor is never BELOW -10%. A clamp built on
// these can only ever be equal to or less extreme than the regulation allows, never more.
//
// The allowance is snapPriceTol — a fixed 0.0001 元, the documented width of the representation
// snap — and NOT a tolerance derived from the price. A price-scaled allowance is what previously
// hid a threshold that grew until it swallowed a cent; see the note where directionTol used to
// be in tick_test.go. The exact, tolerance-free version of this contract is
// TestLimitPricesNeverExceedTheRegulation below.
//
// Every prevClose here is >= 5, so the strict floor < ceiling assertion holds. It does not hold
// universally — see TestLimitPricesDegenerateWhenTheBandIsThinnerThanATick.
func TestLimitPricesRoundIntoTheBand(t *testing.T) {
	prevCloses := []float64{
		5, 9.87, 9.99, 10, 10.05, 33.5, 45.5, 47.5, 49.95, 50, 88.8, 99.9,
		100, 100.5, 250.25, 454.5, 499.5, 500, 601, 750.5, 909, 990, 999,
		1000, 1245, 2500, 4995,
	}
	for _, pc := range prevCloses {
		rawUp, rawDown := pc*1.10, pc*0.90

		up, ok := LimitUpPrice(pc, LimitRule10Pct)
		if !ok {
			t.Fatalf("LimitUpPrice(%v) ok=false, want true", pc)
		}
		if up > rawUp+snapPriceTol {
			t.Fatalf("LimitUpPrice(%v) = %v exceeds +10%% (%v) by %g", pc, up, rawUp, up-rawUp)
		}

		down, ok := LimitDownPrice(pc, LimitRule10Pct)
		if !ok {
			t.Fatalf("LimitDownPrice(%v) ok=false, want true", pc)
		}
		if down < rawDown-snapPriceTol {
			t.Fatalf("LimitDownPrice(%v) = %v undercuts -10%% (%v) by %g", pc, down, rawDown, rawDown-down)
		}

		if down >= up {
			t.Fatalf("prevClose %v: floor %v is not below ceiling %v", pc, down, up)
		}
		// Both ends must be whole cents, and be bit-exactly the float64 nearest that cent
		// count, like every other price this package emits.
		for label, v := range map[string]float64{"up": up, "down": down} {
			cents := v * 100
			if drift := math.Abs(cents - math.Round(cents)); drift > 1e-9 {
				t.Fatalf("prevClose %v: %s limit %.17g is not a whole number of cents (drift %g)", pc, label, v, drift)
			}
			if want := math.Round(cents) / 100; v != want {
				t.Fatalf("prevClose %v: %s limit = %.17g, want the exact float for %v cents, %.17g",
					pc, label, v, math.Round(cents), want)
			}
		}
	}
}

// TestLimitPricesRejectUnknownRule. An UNKNOWN regime yields NO price. The alternative — return
// the computed number and let the caller decide whether to trust it — is what this package is
// built to prevent: the number would be indistinguishable from a real limit at the call site.
func TestLimitPricesRejectUnknownRule(t *testing.T) {
	rules := []LimitRule{
		LimitRuleUnknown,
		LimitRule(""),
		LimitRule("PCT_7"),
		LimitRule("pct_10"), // case matters; this is not the constant
	}
	for _, r := range rules {
		if v, ok := LimitUpPrice(1245, r); ok || v != 0 {
			t.Fatalf("LimitUpPrice(1245, %q) = (%v, %v), want (0, false)", r, v, ok)
		}
		if v, ok := LimitDownPrice(1245, r); ok || v != 0 {
			t.Fatalf("LimitDownPrice(1245, %q) = (%v, %v), want (0, false)", r, v, ok)
		}
	}
}

// TestLimitPricesRejectInvalidPrevClose. No repaired inputs, no substituted defaults.
func TestLimitPricesRejectInvalidPrevClose(t *testing.T) {
	cases := []struct {
		name      string
		prevClose float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"zero", 0},
		{"negative", -1245},
		{"absurdly large", 1e300},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v, ok := LimitUpPrice(c.prevClose, LimitRule10Pct); ok || v != 0 {
				t.Fatalf("LimitUpPrice(%v, PCT_10) = (%v, %v), want (0, false)", c.prevClose, v, ok)
			}
			if v, ok := LimitDownPrice(c.prevClose, LimitRule10Pct); ok || v != 0 {
				t.Fatalf("LimitDownPrice(%v, PCT_10) = (%v, %v), want (0, false)", c.prevClose, v, ok)
			}
		})
	}
}

// TestLimitPricesThroughClassify wires the two halves together the way a caller would, and
// pins the consequence: an ETF code produces no limit price at all.
func TestLimitPricesThroughClassify(t *testing.T) {
	if _, ok := LimitUpPrice(1245, ClassifyLimitRule("2330")); !ok {
		t.Fatalf("a 4-digit share code should yield a limit-up price")
	}
	if v, ok := LimitUpPrice(150, ClassifyLimitRule("0050")); ok {
		t.Fatalf("an ETF code yielded limit-up %v; ETF limits are not knowable from the code", v)
	}
	if v, ok := LimitDownPrice(150, ClassifyLimitRule("0050")); ok {
		t.Fatalf("an ETF code yielded limit-down %v; ETF limits are not knowable from the code", v)
	}
}

// tickTenthsAt is the tick table transcribed into TENTHS OF A CENT, written out again here on
// purpose so the sweep below does not measure the code under test against itself.
//
// A tenth of a cent is the resolution the exercise needs: ±10% of a whole-cent price is exact in
// tenths of a cent (95909 cents × 11 / 10 = 105499.9 cents) and inexact in anything coarser, so
// the whole comparison is done in int64 tenths with no float64 anywhere.
func tickTenthsAt(priceTenths int64) int64 {
	switch {
	case priceTenths < 10000: // < 10 元
		return 10 // 0.01
	case priceTenths < 50000: // < 50 元
		return 50 // 0.05
	case priceTenths < 100000: // < 100 元
		return 100 // 0.1
	case priceTenths < 500000: // < 500 元
		return 500 // 0.5
	case priceTenths < 1000000: // < 1000 元
		return 1000 // 1
	default:
		return 5000 // 5
	}
}

// TestLimitPricesNeverExceedTheRegulation is the clamp contract with no tolerance at all, swept
// over EVERY whole-cent prevClose from 0.01 to 20000.00.
//
// The upper bound is 20,000 元 rather than a round 2,000 because the highest close in this
// repo's own .cache is 19,795 (5274). A sweep whose ceiling sits BELOW the data the caller
// will actually hand it is a sweep that has not been run on the real range.
//
// The expectation is computed in int64 tenths of a cent and never touches this package: the
// ceiling is 11×cents floored onto the tick grid, the floor is 9×cents ceiled onto it. The
// assertion is equality, so the test fails both when the answer is illegal (outside the band)
// and when it is needlessly conservative (a tick further inside than it had to be).
//
// This is the test that rejects an over-wide snap threshold on evidence rather than on argument.
// Measured with a 0.1-cent threshold — sub-cent, and therefore passing every "a cent is never
// absorbed" check — this sweep reports 316 illegal ceilings and 215 illegal floors, the first
// realistic one being prevClose 959.09, whose +10% is exactly 1054.999: the legal ceiling is
// 1050.00 and a 0.1-cent threshold answers 1055.00. At 0.01 cents there are none.
func TestLimitPricesNeverExceedTheRegulation(t *testing.T) {
	for cents := int64(1); cents <= 2000000; cents++ {
		prevClose := float64(cents) / 100

		up, ok := LimitUpPrice(prevClose, LimitRule10Pct)
		if !ok {
			t.Fatalf("LimitUpPrice(%.17g, PCT_10) ok=false, want true", prevClose)
		}
		down, ok := LimitDownPrice(prevClose, LimitRule10Pct)
		if !ok {
			t.Fatalf("LimitDownPrice(%.17g, PCT_10) ok=false, want true", prevClose)
		}

		// Exact band ends, in tenths of a cent.
		upTenths := 11 * cents
		downTenths := 9 * cents
		upTick, downTick := tickTenthsAt(upTenths), tickTenthsAt(downTenths)
		wantUp := (upTenths / upTick) * upTick                          // floor into the band
		wantDown := ((downTenths + downTick - 1) / downTick) * downTick // ceil into the band

		gotUp := int64(math.Round(up * 1000))
		gotDown := int64(math.Round(down * 1000))

		if gotUp != wantUp {
			verdict := "is needlessly far inside the band"
			if gotUp > upTenths {
				verdict = "is ABOVE the +10% the regulation permits"
			}
			t.Fatalf("prevClose %.17g: limit up %v (%v tenths) %s; exact +10%% is %v tenths and the legal ceiling is %v tenths",
				prevClose, up, gotUp, verdict, upTenths, wantUp)
		}
		if gotDown != wantDown {
			verdict := "is needlessly far inside the band"
			if gotDown < downTenths {
				verdict = "is BELOW the -10% the regulation permits"
			}
			t.Fatalf("prevClose %.17g: limit down %v (%v tenths) %s; exact -10%% is %v tenths and the legal floor is %v tenths",
				prevClose, down, gotDown, verdict, downTenths, wantDown)
		}
	}
}

// TestLimitPricesSucceedAndFailAsAPair pins that the two ends are never available one without
// the other.
//
// The bound in RoundToTick applies to the number being aligned, and the two ends are different
// numbers, so a naive implementation checks the bound per call and produces a security that has
// a limit-down price and no limit-up price. That state does not exist in the market. It was
// reachable at both ends of the domain: at the top, where × 1.10 crosses maxAlignablePrice while
// × 0.90 does not, and at the bottom, where a sub-cent prevClose rounds its ceiling off the
// grid while its floor survives.
func TestLimitPricesSucceedAndFailAsAPair(t *testing.T) {
	prevCloses := []float64{
		// Sub-cent: the ceiling used to fall off the bottom of the grid on its own.
		1e-6, 0.001, 0.004, 0.005, 0.009,
		// Real prices.
		0.01, 0.09, 0.1, 1, 9.87, 100, 1245, 19795,
		// The top of the domain: × 1.10 leaves it before × 0.90 does.
		900000, 909090, 909090.91, 909091, 999999.99, 1e6, 1000000.01, 1e7,
		// Not prices at all.
		1.23e8, 1e13, 1e300,
	}
	for _, pc := range prevCloses {
		up, upOK := LimitUpPrice(pc, LimitRule10Pct)
		down, downOK := LimitDownPrice(pc, LimitRule10Pct)
		if upOK != downOK {
			t.Fatalf("prevClose %.17g: LimitUpPrice ok=%v (%v) but LimitDownPrice ok=%v (%v) — a security cannot have one end of the band and not the other",
				pc, upOK, up, downOK, down)
		}
		if !upOK && (up != 0 || down != 0) {
			t.Fatalf("prevClose %.17g: ok=false but values are (%v, %v), want (0, 0)", pc, up, down)
		}
		if upOK && down > up {
			t.Fatalf("prevClose %.17g: floor %v is above ceiling %v", pc, down, up)
		}
	}
}

// TestLimitPricesDegenerateWhenTheBandIsThinnerThanATick pins the range where floor == ceiling.
//
// For prevClose in [0.01, 0.09] the whole ±10% band is at most ±0.9 cents wide, the tick there
// is 1 cent, and both ends round onto prevClose itself. That is arithmetically correct and not a
// fabrication — there is no other legal price inside such a band — but it means "floor <
// ceiling" is NOT a universal invariant, and the comment on LimitDownPrice says so rather than
// claiming otherwise. Strictness begins at 0.10.
//
// No Taiwan board quotes anything in this range; it is pinned because the contract has to be
// true where it is stated, not merely where the data lives.
func TestLimitPricesDegenerateWhenTheBandIsThinnerThanATick(t *testing.T) {
	for cents := int64(1); cents <= 9; cents++ {
		prevClose := float64(cents) / 100
		up, upOK := LimitUpPrice(prevClose, LimitRule10Pct)
		down, downOK := LimitDownPrice(prevClose, LimitRule10Pct)
		if !upOK || !downOK {
			t.Fatalf("prevClose %.17g: want both ends available, got up=(%v,%v) down=(%v,%v)", prevClose, up, upOK, down, downOK)
		}
		if up != prevClose || down != prevClose {
			t.Fatalf("prevClose %.17g: want floor == ceiling == prevClose (the ±10%% band is thinner than the 0.01 tick), got up=%v down=%v",
				prevClose, up, down)
		}
	}

	// 0.10 is the first prevClose whose band is wide enough to hold a different legal price.
	up, upOK := LimitUpPrice(0.10, LimitRule10Pct)
	down, downOK := LimitDownPrice(0.10, LimitRule10Pct)
	if !upOK || !downOK {
		t.Fatalf("prevClose 0.10: want both ends available, got up=(%v,%v) down=(%v,%v)", up, upOK, down, downOK)
	}
	if math.Abs(up-0.11) > eps || math.Abs(down-0.09) > eps {
		t.Fatalf("prevClose 0.10: want (0.11, 0.09), got (%v, %v)", up, down)
	}
	if !(down < up) {
		t.Fatalf("prevClose 0.10: floor %v is not strictly below ceiling %v; strict ordering is supposed to start here", down, up)
	}
}

// TestLimitPricesRejectNonPriceMagnitudes: a turnover, a market capitalisation or a share count
// passed in where a prevClose was expected must produce no limit prices at all.
//
// Before the domain bound was narrowed to 1e6 these all returned a tick-aligned number with
// ok=true — a plausible-looking limit price derived from a quantity that was never a price.
func TestLimitPricesRejectNonPriceMagnitudes(t *testing.T) {
	cases := []struct {
		name      string
		prevClose float64
	}{
		{"a share count", 5000000},
		{"a day's turnover in 元", 1.23e8},
		{"a market capitalisation in 元", 1.5e13},
		{"a volume in shares", 1234567},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if v, ok := LimitUpPrice(c.prevClose, LimitRule10Pct); ok || v != 0 {
				t.Fatalf("LimitUpPrice(%.17g, PCT_10) = (%v, %v), want (0, false)", c.prevClose, v, ok)
			}
			if v, ok := LimitDownPrice(c.prevClose, LimitRule10Pct); ok || v != 0 {
				t.Fatalf("LimitDownPrice(%.17g, PCT_10) = (%v, %v), want (0, false)", c.prevClose, v, ok)
			}
		})
	}
}
