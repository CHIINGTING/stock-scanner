package fx

import (
	"math"
	"testing"
)

// pairSeries builds two aligned series from explicit closes.
func pairSeries(tw, dx []float64) (*Series, *Series) {
	a, b := mkSeries(tw...), mkSeries(dx...)
	a.Symbol, b.Symbol = Symbol, SymbolDXY
	return a, b
}

// synth builds n aligned bars where USDTWD moves as beta*DXY plus a deterministic
// TWD-specific wobble, so the recovered coefficient can be checked against a known truth.
func synth(n int, beta float64) (*Series, *Series) {
	tw := make([]float64, n)
	dx := make([]float64, n)
	pt, pd := 30.0, 100.0
	for i := 0; i < n; i++ {
		// The dollar path needs dispersion AT THE WINDOW SCALE, not just bar to bar: a
		// purely alternating series has a near-constant 20-day change, which is a degenerate
		// regressor and identifies nothing. A slow swing on top of the jitter gives the
		// windowed changes something to vary across.
		dRet := 0.002*math.Sin(float64(i)/17) + 0.0006
		if i%2 == 0 {
			dRet -= 0.0012
		}
		// The local wobble is orthogonal to the dollar path by construction.
		wob := 0.0005
		if i%3 == 0 {
			wob = -0.0005
		}
		pd *= 1 + dRet
		pt *= 1 + beta*dRet + wob
		tw[i], dx[i] = pt, pd
	}
	return pairSeries(tw, dx)
}

// THE look-ahead guard for the regression. The observation being explained must not help
// choose the coefficients that explain it — otherwise the residual shrinks for arithmetic
// reasons and the decomposition looks clean while being circular.
func TestDecompositionExcludesThePoint(t *testing.T) {
	tw, dx := synth(500, 0.4)
	cfg := DefaultConfig()
	cut := "2099-01-01"

	got := DecomposeAsOf(tw, dx, cut, cfg, 200)
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}

	// Perturbing ONLY the final bar must move the residual but must NOT move the fitted
	// coefficients — if beta shifts, the last point was inside its own fit.
	tw2 := &Series{Symbol: tw.Symbol, Bars: append([]Bar(nil), tw.Bars...)}
	tw2.Bars[len(tw2.Bars)-1].Close *= 1.05
	got2 := DecomposeAsOf(tw2, dx, cut, cfg, 200)
	if got2.Status != StatusOK {
		t.Fatalf("status = %s", got2.Status)
	}
	if math.Abs(got.Beta-got2.Beta) > 1e-12 || math.Abs(got.Alpha-got2.Alpha) > 1e-12 {
		t.Errorf("changing the explained point moved the fit: beta %v→%v alpha %v→%v",
			got.Beta, got2.Beta, got.Alpha, got2.Alpha)
	}
	if math.Abs(got.Residual-got2.Residual) < 1e-9 {
		t.Error("changing the explained point did not move the residual at all")
	}
}

// The whole decomposition must be causal: bars on or after the Taiwan date are invisible,
// and truncating the series there changes nothing.
func TestDecompositionIsCausal(t *testing.T) {
	tw, dx := synth(500, 0.4)
	cut := tw.Bars[400].Date
	cfg := DefaultConfig()

	full := DecomposeAsOf(tw, dx, cut, cfg, 200)
	if full.Status != StatusOK {
		t.Fatalf("status = %s", full.Status)
	}
	if full.BarDate >= cut {
		t.Errorf("used bar %s for Taiwan session %s — same-day or later is look-ahead",
			full.BarDate, cut)
	}

	twT := &Series{Symbol: tw.Symbol, Bars: append([]Bar(nil), tw.Bars[:400]...)}
	dxT := &Series{Symbol: dx.Symbol, Bars: append([]Bar(nil), dx.Bars[:400]...)}
	trunc := DecomposeAsOf(twT, dxT, cut, cfg, 200)

	if full.Beta != trunc.Beta || full.Residual != trunc.Residual || full.BarDate != trunc.BarDate {
		t.Errorf("future bars changed a past decomposition:\n full  %+v\n trunc %+v", full, trunc)
	}
}

// The fit must recover a known sensitivity, or the residual is not what it claims to be.
func TestBetaRecoversTheKnownSensitivity(t *testing.T) {
	for _, want := range []float64{0.0, 0.3, 0.8} {
		tw, dx := synth(600, want)
		got := DecomposeAsOf(tw, dx, "2099-01-01", DefaultConfig(), 300)
		if got.Status != StatusOK {
			t.Fatalf("beta %v: status = %s", want, got.Status)
		}
		if math.Abs(got.Beta-want) > 0.15 {
			t.Errorf("planted beta %v, recovered %v", want, got.Beta)
		}
	}
}

// A dollar move that fully explains the pair must leave ~no residual; a purely local move
// must leave ~all of it. This is the property the whole M4 question rests on.
func TestResidualSeparatesLocalFromDollar(t *testing.T) {
	cfg := DefaultConfig()

	// Pair follows the dollar exactly (beta 1, no local wobble).
	n := 500
	tw := make([]float64, n)
	dx := make([]float64, n)
	pt, pd := 30.0, 100.0
	for i := 0; i < n; i++ {
		r := 0.002*math.Sin(float64(i)/17) + 0.0006
		if i%2 == 0 {
			r -= 0.0012
		}
		pd *= 1 + r
		pt *= 1 + r
		tw[i], dx[i] = pt, pd
	}
	a, b := pairSeries(tw, dx)
	pure := DecomposeAsOf(a, b, "2099-01-01", cfg, 300)
	if pure.Status != StatusOK {
		t.Fatalf("status = %s", pure.Status)
	}
	if math.Abs(pure.Residual) > 0.2 {
		t.Errorf("a move that IS the dollar left residual %v — the split is not separating", pure.Residual)
	}

	// Now add a large local-only move on the last window: the residual must pick it up.
	a2 := &Series{Symbol: a.Symbol, Bars: append([]Bar(nil), a.Bars...)}
	for i := len(a2.Bars) - 20; i < len(a2.Bars); i++ {
		a2.Bars[i].Close *= 1.0 + 0.003*float64(i-(len(a2.Bars)-21))
	}
	local := DecomposeAsOf(a2, b, "2099-01-01", cfg, 300)
	if local.Status != StatusOK {
		t.Fatalf("status = %s", local.Status)
	}
	if local.Residual <= pure.Residual {
		t.Errorf("a TWD-specific weakening did not raise the residual: %v vs %v",
			local.Residual, pure.Residual)
	}
	if ResidualContext(local.ResidualZ, cfg) != ResidualTWDWeak {
		t.Errorf("a TWD-specific weakening classified as %s", ResidualContext(local.ResidualZ, cfg))
	}
}

// The residual's sign must name the LOCAL currency, independent of what the dollar did.
func TestResidualContextDirection(t *testing.T) {
	cfg := DefaultConfig()
	if ResidualContext(+2, cfg) != ResidualTWDWeak {
		t.Error("a positive residual means USDTWD rose more than the dollar explains = TWD weaker")
	}
	if ResidualContext(-2, cfg) != ResidualTWDStrong {
		t.Error("a negative residual means TWD held up better than the dollar explains")
	}
	if ResidualContext(0, cfg) != ResidualTWDNeutral {
		t.Error("a zero residual is neutral")
	}
}

// Missing or unusable inputs must not produce a confident zero decomposition.
func TestDecompositionMissingInputs(t *testing.T) {
	tw, dx := synth(500, 0.4)
	cfg := DefaultConfig()

	for name, got := range map[string]Decomposition{
		"no usdtwd":  DecomposeAsOf(nil, dx, "2099-01-01", cfg, 200),
		"no dxy":     DecomposeAsOf(tw, nil, "2099-01-01", cfg, 200),
		"no date":    DecomposeAsOf(tw, dx, "", cfg, 200),
		"date first": DecomposeAsOf(tw, dx, tw.Bars[0].Date, cfg, 200),
	} {
		if got.Status.OK() {
			t.Errorf("%s: produced an OK decomposition", name)
		}
		if got.Residual != 0 || got.Beta != 0 {
			t.Errorf("%s: published values for an unusable input: %+v", name, got)
		}
	}

	// Too little history to fit is INSUFFICIENT, not a zero beta.
	short := &Series{Symbol: tw.Symbol, Bars: tw.Bars[:60]}
	shortD := &Series{Symbol: dx.Symbol, Bars: dx.Bars[:60]}
	if got := DecomposeAsOf(short, shortD, "2099-01-01", cfg, 200); got.Status.OK() {
		t.Error("60 bars should not be enough to fit")
	}

	// A dollar index that never moved carries no information about the pair.
	flatD := mkSeries(make([]float64, 500)...)
	for i := range flatD.Bars {
		flatD.Bars[i].Close = 100
	}
	flatD.Symbol = SymbolDXY
	if got := DecomposeAsOf(tw, flatD, "2099-01-01", cfg, 200); got.Status.OK() {
		t.Error("a constant dollar index should not yield a fit")
	}
}
