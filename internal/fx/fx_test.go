package fx

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// mkSeries builds a series whose bars are consecutive calendar days from 2026-01-01.
func mkSeries(closes ...float64) *Series {
	s := &Series{Symbol: "USDTWD"}
	for i, c := range closes {
		s.Bars = append(s.Bars, Bar{Date: fmt.Sprintf("2026-%02d-%02d", 1+i/28, 1+i%28), Close: c})
	}
	return s
}

// drift builds n bars compounding at r per bar, so the percentage change per bar is constant.
func drift(n int, start, r float64) *Series {
	cs := make([]float64, n)
	p := start
	for i := range cs {
		cs[i] = p
		p *= 1 + r
	}
	return mkSeries(cs...)
}

// after returns a Taiwan date strictly greater than every bar the series holds, so MetricsAsOf
// can use the whole thing.
func after(s *Series) string { return "2099-01-01" }

// THE test this package exists to not fail. USDTWD is 1 USD = X TWD, so the quote going UP
// means the local currency got WEAKER. Inverting this would flip every conclusion the
// research draws while still looking entirely plausible in a report.
func TestDirectionIsNotInverted(t *testing.T) {
	if got := Direction(+1.0); got != "TWD_WEAKER" {
		t.Errorf("USDTWD +1%% = %s, want TWD_WEAKER — the quote rose, so more TWD buys one USD", got)
	}
	if got := Direction(-1.0); got != "TWD_STRONGER" {
		t.Errorf("USDTWD -1%% = %s, want TWD_STRONGER", got)
	}
	if got := Direction(0); got != "TWD_FLAT" {
		t.Errorf("USDTWD 0%% = %s, want TWD_FLAT", got)
	}

	// And end to end. The fixture is a quiet series that ends with a decisive move, because
	// a CONSTANT drift is degenerate for any dispersion-scaled measure: with every window
	// identical there is no typical move to compare the latest one against. Real FX always
	// varies; a synthetic constant does not, and testing against it would be testing an
	// input that cannot occur.
	up := wiggleThenMove(400, 30, +0.02).MetricsAsOf(after(nil), DefaultConfig())
	if up.Status != StatusOK {
		t.Fatalf("status = %s", up.Status)
	}
	if c, _ := up.Change(20); c <= 0 {
		t.Fatalf("a rising quote gave a %v%% 20d change", c)
	}
	if up.Context != ContextStrongDepreciation && up.Context != ContextDepreciation {
		t.Errorf("a RISING USDTWD classified as %s — the sign is inverted", up.Context)
	}
	if up.ContextZ == nil || *up.ContextZ <= 0 {
		t.Errorf("a rising quote gave z = %v, want positive", up.ContextZ)
	}

	down := wiggleThenMove(400, 30, -0.02).MetricsAsOf(after(nil), DefaultConfig())
	if down.Context != ContextStrongAppreciation && down.Context != ContextAppreciation {
		t.Errorf("a FALLING USDTWD classified as %s — the sign is inverted", down.Context)
	}
	if down.ContextZ == nil || *down.ContextZ >= 0 {
		t.Errorf("a falling quote gave z = %v, want negative", down.ContextZ)
	}
}

// wiggleThenMove is a series with realistic small variation that ends with one decisive
// move of total size `finalMove` spread over the last 20 bars.
func wiggleThenMove(n int, start, finalMove float64) *Series {
	cs := make([]float64, n)
	p := start
	for i := 0; i < n; i++ {
		// Deterministic alternating jitter so the sample has a real dispersion.
		j := 0.0015
		if i%2 == 0 {
			j = -0.0015
		}
		if i%7 == 0 {
			j *= 2.5
		}
		p *= 1 + j
		if i >= n-20 {
			p *= 1 + finalMove/20
		}
		cs[i] = p
	}
	return mkSeries(cs...)
}

// The causality contract: a reading for a Taiwan session may not use the FX bar labelled
// with that same date, because that bar does not close until the next morning in Taipei.
func TestMetricsAsOfIsCausal(t *testing.T) {
	s := drift(400, 30, 0.0005)
	cut := s.Bars[300].Date

	got := s.MetricsAsOf(cut, DefaultConfig())
	if got.Status != StatusOK {
		t.Fatalf("status = %s", got.Status)
	}
	// The bar used must be STRICTLY before the Taiwan date.
	if got.FXDate >= cut {
		t.Errorf("reading for Taiwan %s used FX bar %s — same-day or later is look-ahead",
			cut, got.FXDate)
	}
	if got.FXDate != s.Bars[299].Date {
		t.Errorf("used %s, want the last bar before the session (%s)", got.FXDate, s.Bars[299].Date)
	}

	// Truncating everything from the cut onward must not change the reading at all.
	trunc := &Series{Symbol: s.Symbol, Bars: append([]Bar(nil), s.Bars[:300]...)}
	tg := trunc.MetricsAsOf(cut, DefaultConfig())
	if tg.Close != got.Close || tg.Context != got.Context || tg.FXDate != got.FXDate {
		t.Errorf("future bars changed a past reading:\n full  %+v\n trunc %+v", got, tg)
	}
	for _, w := range DefaultConfig().ChangeWindows {
		a, okA := got.Change(w)
		b, okB := tg.Change(w)
		if okA != okB || a != b {
			t.Errorf("%dd change changed when future bars were removed: %v/%v vs %v/%v", w, a, okA, b, okB)
		}
	}
}

func TestChangeWindows(t *testing.T) {
	// 100 → 110 over one bar is +10%; over five bars from 100 it is also +10%.
	s := mkSeries(100, 100, 100, 100, 100, 110)
	m := s.MetricsAsOf(after(s), Config{ChangeWindows: []int{1, 5}, ContextWindow: 5})
	if v, ok := m.Change(1); !ok || math.Abs(v-10) > 1e-9 {
		t.Errorf("1d change = %v (ok=%v), want 10", v, ok)
	}
	if v, ok := m.Change(5); !ok || math.Abs(v-10) > 1e-9 {
		t.Errorf("5d change = %v (ok=%v), want 10", v, ok)
	}
	// A window longer than the history is UNMEASURABLE, not zero.
	if _, ok := m.Change(20); ok {
		t.Error("a 20d change was reported from 6 bars")
	}
}

// MISSING ≠ ZERO, everywhere.
func TestMissingDataNeverBecomesZero(t *testing.T) {
	empty := (&Series{}).MetricsAsOf("2026-08-25", DefaultConfig())
	if empty.Status != StatusNoData {
		t.Errorf("status = %s, want NO_DATA", empty.Status)
	}
	if empty.Context != ContextUnknown || empty.MA != nil || empty.ContextZ != nil {
		t.Errorf("an empty series published values: %+v", empty)
	}
	if len(empty.ChangePct) != 0 {
		t.Errorf("an empty series published changes: %v", empty.ChangePct)
	}

	// Bars exist but all lie on or after the Taiwan date → nothing is usable yet.
	s := mkSeries(30, 31)
	if got := s.MetricsAsOf(s.Bars[0].Date, DefaultConfig()); got.Status != StatusNoData {
		t.Errorf("status = %s, want NO_DATA when no bar precedes the session", got.Status)
	}

	// Too short for the context window → classification UNKNOWN, but the short changes that
	// ARE measurable still come through.
	short := wiggleThenMove(10, 30, 0.01).MetricsAsOf(after(nil), DefaultConfig())
	if short.Context != ContextUnknown || short.ContextZ != nil {
		t.Errorf("a 10-bar series classified as %s", short.Context)
	}
	if _, ok := short.Change(1); !ok {
		t.Error("a 1d change should still be measurable from 10 bars")
	}
	if nilM := (*Metrics)(nil); func() bool { _, ok := nilM.Change(1); return ok }() {
		t.Error("a nil Metrics reported a change")
	}
}

// Non-positive or non-finite quotes are gaps in the feed, not prices, and must not become
// division artefacts.
func TestInvalidQuotesAreDropped(t *testing.T) {
	s := mkSeries(30, 0, -1, math.NaN(), math.Inf(1), 30.3, 30.6)
	m := s.MetricsAsOf(after(s), Config{ChangeWindows: []int{1}, ContextWindow: 1})
	if m.Status != StatusOK {
		t.Fatalf("status = %s — the valid bars should still be usable", m.Status)
	}
	if !finite(m.Close) || m.Close != 30.6 {
		t.Errorf("close = %v, want the last valid quote 30.6", m.Close)
	}
	v, ok := m.Change(1)
	if !ok || !finite(v) {
		t.Errorf("1d change = %v (ok=%v) — invalid quotes leaked", v, ok)
	}
}

// The percentile bands must bucket by DISTRIBUTION position, and the two ends must not be
// swapped: percentile 0 is the appreciation end.
func TestContextBands(t *testing.T) {
	cfg := DefaultConfig() // mild 0.5, strong 1.5
	for _, tc := range []struct {
		z    float64
		want string
	}{
		{-3.0, ContextStrongAppreciation},
		{-1.5, ContextStrongAppreciation},
		{-1.49, ContextAppreciation},
		{-0.5, ContextAppreciation},
		{-0.49, ContextStable},
		{0, ContextStable},
		{0.49, ContextStable},
		{0.5, ContextDepreciation},
		{1.49, ContextDepreciation},
		{1.5, ContextStrongDepreciation},
		{3.0, ContextStrongDepreciation},
	} {
		if got := contextFor(tc.z, cfg, Symbol); got != tc.want {
			t.Errorf("z %v = %s, want %s", tc.z, got, tc.want)
		}
	}
	// The sign must map to the right currency. Negative z = USDTWD fell = TWD stronger.
	if contextFor(-2, cfg, Symbol) != ContextStrongAppreciation {
		t.Error("negative z must mean TWD appreciation")
	}
}

// The classification must not fire off a sample too thin to rank against.
func TestClassificationRespectsMinSample(t *testing.T) {
	cfg := DefaultConfig()
	// 130 bars gives ~110 overlapping 20d windows — below the 120 floor.
	thin := wiggleThenMove(130, 30, 0.01).MetricsAsOf(after(nil), cfg)
	if thin.Context != ContextUnknown {
		t.Errorf("a thin sample classified as %s, want UNKNOWN", thin.Context)
	}
	// Well past the floor it classifies.
	fat := wiggleThenMove(400, 30, 0.01).MetricsAsOf(after(nil), cfg)
	if fat.Context == ContextUnknown {
		t.Error("400 bars should be enough to classify")
	}
}

func TestConfigDefaulted(t *testing.T) {
	got := Config{ContextWindow: 5}.Defaulted()
	if got.ContextWindow != 5 {
		t.Error("Defaulted overwrote an explicit value")
	}
	if got.MAPeriod != 20 || got.PercentileWindow != 252 || got.StrongZ != 1.5 {
		t.Errorf("Defaulted did not fill the zeros: %+v", got)
	}
	if len(Config{}.Defaulted().ChangeWindows) != 3 {
		t.Error("the default change windows went missing")
	}
}

func TestMetricsAreDeterministic(t *testing.T) {
	s := drift(400, 30, 0.0007)
	a := s.MetricsAsOf("2099-01-01", DefaultConfig())
	b := s.MetricsAsOf("2099-01-01", DefaultConfig())
	if a.Close != b.Close || a.Context != b.Context || a.FXDate != b.FXDate {
		t.Error("same input gave different results")
	}
}

// The dollar index must be described in DOLLAR terms. Reusing the TWD vocabulary for it
// would state something false — DXY falling says nothing about the local currency — and a
// label that is wrong but plausible cannot be detected by anything downstream.
func TestDXYContextIsAboutTheDollar(t *testing.T) {
	cfg := DefaultConfig()

	// Index falling = dollar weaker.
	if got := contextFor(-2, cfg, SymbolDXY); got != ContextUSDStrongDepreciation {
		t.Errorf("DXY z=-2 → %s, want USD_STRONG_DEPRECIATION", got)
	}
	// Index rising = dollar stronger.
	if got := contextFor(+2, cfg, SymbolDXY); got != ContextUSDStrongAppreciation {
		t.Errorf("DXY z=+2 → %s, want USD_STRONG_APPRECIATION", got)
	}
	if got := contextFor(0, cfg, SymbolDXY); got != ContextUSDStable {
		t.Errorf("DXY z=0 → %s, want USD_STABLE", got)
	}
	// And no DXY reading may ever carry a TWD label.
	for _, z := range []float64{-3, -1, 0, 1, 3} {
		got := contextFor(z, cfg, SymbolDXY)
		if strings.HasPrefix(got, "TWD_") {
			t.Errorf("DXY z=%v was labelled %q — that says something about the wrong currency", z, got)
		}
	}
	// The same z on USDTWD keeps the TWD naming, and the two disagree by construction.
	if contextFor(-2, cfg, Symbol) == contextFor(-2, cfg, SymbolDXY) {
		t.Error("USDTWD and DXY produced the same label for the same z")
	}

	// End to end through a series, so the symbol really is what drives the naming.
	s := wiggleThenMove(400, 100, +0.03)
	s.Symbol = SymbolDXY
	m := s.MetricsAsOf(after(nil), cfg)
	if !strings.HasPrefix(m.Context, "USD_") {
		t.Errorf("a DXY series produced %q", m.Context)
	}
}
