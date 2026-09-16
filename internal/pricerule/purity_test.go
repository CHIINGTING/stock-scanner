package pricerule

import (
	"math"
	"testing"
)

// TestPackageIsPure writes the PURE contract down.
//
// There is no clock, no rand, no network, no database and no AI in this package, so the test
// cannot fail today by construction. That is the point: it is the assertion that fails on the
// day someone adds a "today's limit is looser because it is a first trading day" branch that
// reads time.Now(), or memoises a tick table that mutates. The results are compared with ==,
// bit for bit, not within a tolerance.
func TestPackageIsPure(t *testing.T) {
	prices := []float64{
		0.07, 8.888, 9.87, 10, 33.5, 47.5, 50, 88.87, 100, 250.25, 500,
		601, 750.5, 990, 1000, 1245.3, 2500, math.NaN(), math.Inf(1), 0, -5,
	}
	codes := []string{"2330", "0050", "00632R", "031234", "", "23A0", " 2330 "}

	for _, p := range prices {
		t1, ok1 := TickSize(p)
		t2, ok2 := TickSize(p)
		if t1 != t2 || ok1 != ok2 {
			t.Fatalf("TickSize(%v) not deterministic: (%v,%v) then (%v,%v)", p, t1, ok1, t2, ok2)
		}

		for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
			r1, rok1 := RoundToTick(p, dir)
			r2, rok2 := RoundToTick(p, dir)
			if r1 != r2 || rok1 != rok2 {
				t.Fatalf("RoundToTick(%v, %v) not deterministic: (%v,%v) then (%v,%v)", p, dir, r1, rok1, r2, rok2)
			}
		}

		for _, rule := range []LimitRule{LimitRule10Pct, LimitRuleUnknown} {
			u1, uok1 := LimitUpPrice(p, rule)
			u2, uok2 := LimitUpPrice(p, rule)
			if u1 != u2 || uok1 != uok2 {
				t.Fatalf("LimitUpPrice(%v, %q) not deterministic: (%v,%v) then (%v,%v)", p, rule, u1, uok1, u2, uok2)
			}
			d1, dok1 := LimitDownPrice(p, rule)
			d2, dok2 := LimitDownPrice(p, rule)
			if d1 != d2 || dok1 != dok2 {
				t.Fatalf("LimitDownPrice(%v, %q) not deterministic: (%v,%v) then (%v,%v)", p, rule, d1, dok1, d2, dok2)
			}
		}
	}

	for _, c := range codes {
		if ClassifyLimitRule(c) != ClassifyLimitRule(c) {
			t.Fatalf("ClassifyLimitRule(%q) not deterministic", c)
		}
	}
}

// TestNoNonFiniteEscapes: whatever goes in, nothing non-finite comes out with ok=true, and a
// rejected call always leaves the value at exactly zero.
func TestNoNonFiniteEscapes(t *testing.T) {
	inputs := []float64{
		math.NaN(), math.Inf(1), math.Inf(-1), 0, -0, -1e-300, -1245,
		1e-300, 1e-3, 1, 1e6, 1e6 + 0.01, 1e13, 1e13 + 1, 1e14,
		math.MaxFloat64, math.SmallestNonzeroFloat64,
	}
	for _, p := range inputs {
		for _, dir := range []RoundDir{RoundDown, RoundUp, RoundNearest} {
			v, ok := RoundToTick(p, dir)
			if ok && (math.IsNaN(v) || math.IsInf(v, 0)) {
				t.Fatalf("RoundToTick(%v, %v) returned non-finite %v with ok=true", p, dir, v)
			}
			if !ok && v != 0 {
				t.Fatalf("RoundToTick(%v, %v) returned %v with ok=false, want 0", p, dir, v)
			}
		}
		for _, fn := range []struct {
			name string
			f    func(float64, LimitRule) (float64, bool)
		}{{"LimitUpPrice", LimitUpPrice}, {"LimitDownPrice", LimitDownPrice}} {
			v, ok := fn.f(p, LimitRule10Pct)
			if ok && (math.IsNaN(v) || math.IsInf(v, 0)) {
				t.Fatalf("%s(%v) returned non-finite %v with ok=true", fn.name, p, v)
			}
			if !ok && v != 0 {
				t.Fatalf("%s(%v) returned %v with ok=false, want 0", fn.name, p, v)
			}
		}
	}
}
