package analyzer

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

var th = model.StructureThresholds{}.Defaulted()

// pv builds a PriceView directly so the classifier can be tested on exact inputs rather
// than on a price series reverse-engineered to produce them.
func pv(close, ma20, ma60, ma120 float64, ma60Rising bool, ret20, ret60, dd float64) PriceView {
	return PriceView{
		Benchmark: model.Benchmark0050, Bars: 500, Sufficient: true,
		Close: close, MA20: ma20, MA60: ma60, MA120: ma120,
		MA60Rising: ma60Rising, Ret20: ret20, Ret60: ret60, DDHigh: dd,
	}
}

func TestClassifyStructureTable(t *testing.T) {
	cases := []struct {
		name string
		in   PriceView
		want model.PrimaryStructure
	}{
		{"strong uptrend: full stack, at the high",
			pv(110, 105, 100, 95, true, 6, 20, 1), model.StructureStrongUptrend},
		{"uptrend: above rising MA60 but pulled back from the high",
			pv(104, 103, 100, 95, true, -1, 12, 8), model.StructureUptrend},
		{"uptrend: stack is right but too far off the high for STRONG",
			pv(110, 105, 100, 95, true, 6, 20, 9), model.StructureUptrend},
		{"weakening: lost MA20 and MA60, MA120 still holds",
			pv(97, 103, 100, 95, true, -5, 8, 12), model.StructureWeakening},
		{"downtrend: below MA60 and MA120, MA60 falling",
			pv(90, 95, 100, 105, false, -7, -15, 20), model.StructureDowntrend},
		{"range: choppy, no qualifying condition",
			pv(101, 100, 100, 99, false, 1, 0.5, 4), model.StructureRange},
		{"range: above MA60 but MA60 not rising",
			pv(105, 103, 100, 95, false, 2, 5, 4), model.StructureRange},
		{"range: above rising MA60 but 60d return negative",
			pv(105, 103, 100, 95, true, 2, -3, 4), model.StructureRange},
	}
	for _, c := range cases {
		if got := ClassifyStructure(c.in, th); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// The single most important ordering in layer 1: a broken long-term floor must always read
// as DOWNTREND. If WEAKENING were tested first, a bear market would be softened into a
// pullback and R1 (DOWNTREND ⇒ BEAR) would never fire.
func TestDowntrendBeatsWeakening(t *testing.T) {
	// Identical except for where price sits relative to MA120.
	broken := pv(90, 95, 100, 95, false, -7, -12, 20)  // close < MA120
	intact := pv(97, 103, 100, 95, false, -7, -12, 20) // close > MA120

	if got := ClassifyStructure(broken, th); got != model.StructureDowntrend {
		t.Errorf("long-term floor broken should be DOWNTREND, got %s", got)
	}
	if got := ClassifyStructure(intact, th); got != model.StructureWeakening {
		t.Errorf("long-term floor intact should be WEAKENING, got %s", got)
	}
}

// Insufficient data must reach UNKNOWN, never fall through to RANGE. RANGE is a measured
// verdict ("we looked, there is no edge"); UNKNOWN is the absence of a verdict.
func TestStructureUnknownNeverFallsThroughToRange(t *testing.T) {
	cases := []struct {
		name string
		in   PriceView
	}{
		{"not sufficient", PriceView{Sufficient: false, Close: 100, MA20: 99, MA60: 98, MA120: 97}},
		{"no close", PriceView{Sufficient: true, MA20: 99, MA60: 98, MA120: 97}},
		{"no MA120 (history < 120)", PriceView{Sufficient: true, Close: 100, MA20: 99, MA60: 98}},
		{"zero value", PriceView{}},
	}
	for _, c := range cases {
		got := ClassifyStructure(c.in, th)
		if got != model.StructureUnknown {
			t.Errorf("%s: got %s, want UNKNOWN", c.name, got)
		}
		if got == model.StructureRange {
			t.Errorf("%s: missing data was reported as a measured RANGE", c.name)
		}
	}
}

func bv(above20, drop20 float64, valid int) BreadthView {
	return BreadthView{
		AboveMA20: above20, AboveMA60: above20, Drop20: drop20,
		ValidCount: valid, Sufficient: true,
	}
}

func TestClassifyBreadthTable(t *testing.T) {
	cases := []struct {
		name     string
		in       BreadthView
		nearHigh bool
		want     model.BreadthQuality
	}{
		{"healthy", bv(62, 4, 1900), false, model.BreadthHealthy},
		{"collapsing beats everything", bv(22, 40, 1900), true, model.BreadthCollapsing},
		{"narrowing: divergence at the high", bv(41, 5, 1900), true, model.BreadthNarrowing},
		{"narrowing: steep fall from the peak", bv(58, 24, 1900), false, model.BreadthNarrowing},
		{"cooling: mid level", bv(48, 12, 1900), false, model.BreadthCooling},
		{"cooling: high level but receded", bv(60, 14, 1900), false, model.BreadthCooling},
	}
	for _, c := range cases {
		if got := ClassifyBreadth(c.in, c.nearHigh, th); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// NARROWING is a DIVERGENCE test, not a level test: the same 41% breadth means something
// different depending on whether the index is at its high.
func TestNarrowingRequiresDivergence(t *testing.T) {
	weak := bv(41, 5, 1900)

	atHigh := ClassifyBreadth(weak, true, th)
	pulledBack := ClassifyBreadth(weak, false, th)

	if atHigh != model.BreadthNarrowing {
		t.Errorf("41%% breadth at the high should be NARROWING, got %s", atHigh)
	}
	if pulledBack == model.BreadthNarrowing {
		t.Error("41% breadth after a pullback is cooling, not narrowing — the divergence is gone")
	}
	if pulledBack != model.BreadthCooling {
		t.Errorf("expected COOLING when not near the high, got %s", pulledBack)
	}
}

func TestBreadthUnknownWhenUniverseTooThin(t *testing.T) {
	thin := BreadthView{AboveMA20: 62, ValidCount: 100, Sufficient: false}
	got := ClassifyBreadth(thin, false, th)
	if got != model.BreadthUnknown {
		t.Errorf("got %s, want UNKNOWN", got)
	}
	if got == model.BreadthCooling || got == model.BreadthHealthy {
		t.Error("an unmeasurable universe was reported as a measured state")
	}
}

func TestBuildStructureViewPredicates(t *testing.T) {
	cases := []struct {
		name                            string
		price                           PriceView
		trendIntact, resilient, orderly bool
	}{
		{
			name:        "at the high: intact, resilient, no correction",
			price:       pv(110, 105, 100, 95, true, 6, 20, 1),
			trendIntact: true, resilient: true, orderly: false,
		},
		{
			name:        "orderly 8% pullback, still above MA120",
			price:       pv(101, 103, 100, 95, true, -4, 10, 8),
			trendIntact: true, resilient: true, orderly: true, // above MA60 → resilient
		},
		{
			name:        "20% drawdown is beyond orderly",
			price:       pv(85, 95, 100, 90, false, -18, -10, 20),
			trendIntact: false, resilient: false, orderly: false,
		},
	}
	for _, c := range cases {
		v := BuildStructureView(c.price, bv(60, 5, 1900), model.PostureUnknown, th)
		if v.TrendIntact != c.trendIntact {
			t.Errorf("%s: TrendIntact = %v, want %v", c.name, v.TrendIntact, c.trendIntact)
		}
		if v.PriceResilient != c.resilient {
			t.Errorf("%s: PriceResilient = %v, want %v", c.name, v.PriceResilient, c.resilient)
		}
		if v.OrderlyCorrection != c.orderly {
			t.Errorf("%s: OrderlyCorrection = %v, want %v", c.name, v.OrderlyCorrection, c.orderly)
		}
	}
}

// A single capitulation session disqualifies a correction from being orderly even when the
// total drawdown sits inside the band — that is the whole point of the predicate.
func TestCapitulationDayBreaksOrderly(t *testing.T) {
	base := pv(101, 103, 100, 95, true, -4, 10, 8)
	base.WorstDay20 = -2.0
	if !BuildStructureView(base, bv(60, 5, 1900), model.PostureUnknown, th).OrderlyCorrection {
		t.Error("a -2% worst day inside an 8% drawdown should still be orderly")
	}
	base.WorstDay20 = -6.5
	if BuildStructureView(base, bv(60, 5, 1900), model.PostureUnknown, th).OrderlyCorrection {
		t.Error("a -6.5% session should disqualify the correction from being orderly")
	}
}

// Layer 2b has no producer until M6. An unknown posture must NOT poison the price/breadth
// read — Known() checks only the first two layers.
func TestUnknownPostureDoesNotInvalidateStructure(t *testing.T) {
	v := BuildStructureView(pv(110, 105, 100, 95, true, 6, 20, 1), bv(62, 4, 1900), "", th)
	if v.Posture != model.PostureUnknown {
		t.Errorf("empty posture should normalize to UNKNOWN, got %q", v.Posture)
	}
	if !v.Known() {
		t.Error("price+breadth are both known; Known() must be true despite an unknown posture")
	}
}

func TestStructureViewUnknownWhenLayersMissing(t *testing.T) {
	v := BuildStructureView(PriceView{}, BreadthView{}, model.PostureUnknown, th)
	if v.Structure != model.StructureUnknown || v.Breadth != model.BreadthUnknown {
		t.Errorf("expected both layers UNKNOWN, got %s / %s", v.Structure, v.Breadth)
	}
	if v.Known() {
		t.Error("Known() must be false so the M3 table returns UNKNOWN rather than SIDEWAYS")
	}
	if v.TrendIntact || v.PriceResilient || v.OrderlyCorrection {
		t.Error("predicates must stay false when there is no price data to derive them from")
	}
}

func TestFlags(t *testing.T) {
	calm := pv(110, 105, 100, 95, true, 6, 20, 1)
	calm.VolPctKnown, calm.VolPercentile = true, 45
	if f := Flags(calm, bv(62, 4, 1900), th); f.HighVolatility || f.CrashEvent {
		t.Errorf("calm tape should raise no flags: %+v", f)
	}

	wild := calm
	wild.VolPercentile = 88
	if !Flags(wild, bv(62, 4, 1900), th).HighVolatility {
		t.Error("vol percentile 88 >= 80 should raise HighVolatility")
	}

	// Crash needs BOTH the price drop and collapsed breadth.
	crash := pv(80, 95, 100, 105, false, -12, -20, 25)
	crash.VolPctKnown = true
	if !Flags(crash, bv(22, 40, 1900), th).CrashEvent {
		t.Error("-12% with 22% breadth should raise CrashEvent")
	}
	if Flags(crash, bv(55, 10, 1900), th).CrashEvent {
		t.Error("a price drop without collapsed breadth is not a crash event")
	}

	// An unknown volatility distribution must leave the flag off, not guess.
	noDist := calm
	noDist.VolPctKnown, noDist.VolPercentile = false, 99
	if Flags(noDist, bv(62, 4, 1900), th).HighVolatility {
		t.Error("an unbuildable percentile must not set HighVolatility")
	}
}
