package r6backtest

import (
	"fmt"
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// fxSeriesFor builds an FX archive covering the same dates a test universe uses.
func fxSeriesFor(dates []string, closes ...float64) *fx.Series {
	s := &fx.Series{Symbol: fx.Symbol}
	for i, d := range dates {
		c := 30.0
		if i < len(closes) {
			c = closes[i]
		}
		s.Bars = append(s.Bars, fx.Bar{Date: d, Close: c})
	}
	return s
}

// THE market-scale guard. The study must count trading dates, not stock-days: the currency,
// the flow and the index are one observation per day shared by every stock, and reporting the
// per-stock count as N would inflate the sample by three orders of magnitude.
func TestFXStudyCountsDatesNotStockDays(t *testing.T) {
	var stocks []*Stock
	for i := 0; i < 12; i++ {
		stocks = append(stocks, teStock(fmt.Sprintf("S%02d", i), "族群", 120, 100, 0.003, 1000))
	}
	u := teUniverse(stocks...)

	panel := BuildFXPanel(u, fxSeriesFor(u.Axis), nil, nil, fx.DefaultConfig(), []int{5, 20})
	if len(panel.Days) != len(u.Axis) {
		t.Fatalf("panel has %d days, want one per trading date (%d)", len(panel.Days), len(u.Axis))
	}
	// The inflation the framing avoids must be visible, and large.
	if panel.StockObservations <= len(panel.Days) {
		t.Fatalf("stock observations %d should far exceed %d dates",
			panel.StockObservations, len(panel.Days))
	}

	stats := RunFXStudy(panel, FXConditions(), []int{5, 20})
	var base FXStat
	for _, s := range stats {
		if s.Name == "FX_BASELINE_ALL_DATES" {
			base = s
		}
	}
	if base.RawDates != len(u.Axis) {
		t.Errorf("baseline counted %d, want %d trading dates", base.RawDates, len(u.Axis))
	}
	if base.StockObservations <= base.RawDates {
		t.Error("the per-stock count should be reported alongside, and be much larger")
	}
}

// The FX half of the causality contract: a date's reading may not use the FX bar carrying
// that same date, because that bar has not closed when Taiwan closes.
func TestFXStudyIsCausal(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 120, 100, 0.003, 1000))

	// A series that jumps hugely on one date. If the panel used the same-day bar, the jump
	// would show up in that date's reading; it must show up the day AFTER instead.
	closes := make([]float64, len(u.Axis))
	for i := range closes {
		closes[i] = 30.0
	}
	jump := 60
	for i := jump; i < len(closes); i++ {
		closes[i] = 33.0
	}
	panel := BuildFXPanel(u, fxSeriesFor(u.Axis, closes...), nil, nil, fx.DefaultConfig(), []int{5})

	onJump := panel.Days[jump]
	dayAfter := panel.Days[jump+1]
	if onJump.FXChange1 != 0 {
		t.Errorf("the jump date already saw a %v%% 1d change — it used its own bar", onJump.FXChange1)
	}
	if dayAfter.FXChange1 <= 0 {
		t.Errorf("the day after the jump saw %v%%, want the jump to appear there", dayAfter.FXChange1)
	}

	// And truncating the archive at the date must not change that date's reading.
	full := BuildFXPanel(u, fxSeriesFor(u.Axis, closes...), nil, nil, fx.DefaultConfig(), []int{5})
	short := fxSeriesFor(u.Axis[:jump], closes[:jump]...)
	trunc := BuildFXPanel(u, short, nil, nil, fx.DefaultConfig(), []int{5})
	if full.Days[jump].FXChange5 != trunc.Days[jump].FXChange5 {
		t.Errorf("removing later bars changed date %s: %v vs %v",
			u.Axis[jump], full.Days[jump].FXChange5, trunc.Days[jump].FXChange5)
	}
}

// A missing archive must leave a day unmeasurable, never zero — otherwise "the currency did
// not move" and "we have no currency data" become the same row.
func TestFXPanelMissingDataIsNotZero(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 60, 100, 0.003, 1000))

	noFX := BuildFXPanel(u, nil, nil, nil, fx.DefaultConfig(), []int{5})
	for _, d := range noFX.Days {
		if d.FXOK {
			t.Fatal("a day claimed an FX reading with no archive")
		}
		if d.ForeignOK {
			t.Fatal("a day claimed foreign flow with no archive")
		}
	}
	// Every FX and interaction condition must therefore select nothing.
	for _, st := range RunFXStudy(noFX, FXConditions(), []int{5}) {
		if st.Name == "FX_BASELINE_ALL_DATES" {
			continue
		}
		if st.RawDates != 0 {
			t.Errorf("%s selected %d dates with no data at all", st.Name, st.RawDates)
		}
	}
}

// Foreign flow of exactly zero is a reading; a missing date is not. The two must not merge.
func TestForeignFlowZeroIsNotMissing(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 60, 100, 0.003, 1000))
	flow := &provider.ForeignFlowSeries{Source: "test"}
	for i, d := range u.Axis {
		if i%2 == 0 {
			flow.Points = append(flow.Points, provider.ForeignFlowPoint{Date: d, ForeignNet: 0})
		}
	}
	panel := BuildFXPanel(u, nil, nil, flow, fx.DefaultConfig(), []int{5})

	var present, absent int
	for _, d := range panel.Days {
		if d.ForeignOK {
			present++
			if d.ForeignNet != 0 {
				t.Errorf("%s: net = %v, want the stored 0", d.Date, d.ForeignNet)
			}
		} else {
			absent++
		}
	}
	if present == 0 || absent == 0 {
		t.Fatalf("fixture did not exercise both cases: present=%d absent=%d", present, absent)
	}
	// A stored zero is neither a buy nor a sell.
	for _, st := range RunFXStudy(panel, FXConditions(), []int{5}) {
		if st.Name == "FGN_BUY" || st.Name == "FGN_SELL" {
			if st.RawDates != 0 {
				t.Errorf("%s selected %d dates from an all-zero flow archive", st.Name, st.RawDates)
			}
		}
	}
}

// Confirmation and divergence must partition the days where both legs are directional: a day
// cannot be both, or comparing their forward returns would be meaningless.
func TestConfirmationAndDivergenceAreDisjoint(t *testing.T) {
	byName := map[string]FXCondition{}
	for _, c := range FXConditions() {
		byName[c.Name] = c
	}
	conf, div := byName["FX_FGN_CONFIRMATION"], byName["FX_FGN_DIVERGENCE"]

	for _, ctx := range []string{
		fx.ContextStrongAppreciation, fx.ContextAppreciation, fx.ContextStable,
		fx.ContextDepreciation, fx.ContextStrongDepreciation, fx.ContextUnknown,
	} {
		for _, net := range []float64{-1e9, 0, 1e9} {
			for _, fxOK := range []bool{true, false} {
				for _, fgOK := range []bool{true, false} {
					d := FXDay{FXOK: fxOK, FXContext: ctx, ForeignOK: fgOK, ForeignNet: net}
					if conf.Detect(d) && div.Detect(d) {
						t.Fatalf("day %+v is both confirmation and divergence", d)
					}
				}
			}
		}
	}
}

// The four interaction cells must be mutually exclusive too.
func TestInteractionCellsAreDisjoint(t *testing.T) {
	names := []string{"FX_APPR_X_FGN_BUY", "FX_APPR_X_FGN_SELL", "FX_DEPR_X_FGN_BUY", "FX_DEPR_X_FGN_SELL"}
	byName := map[string]FXCondition{}
	for _, c := range FXConditions() {
		byName[c.Name] = c
	}
	for _, ctx := range []string{fx.ContextAppreciation, fx.ContextStrongAppreciation,
		fx.ContextDepreciation, fx.ContextStrongDepreciation, fx.ContextStable} {
		for _, net := range []float64{-5, 0, 5} {
			d := FXDay{FXOK: true, FXContext: ctx, ForeignOK: true, ForeignNet: net}
			hits := 0
			for _, n := range names {
				if byName[n].Detect(d) {
					hits++
				}
			}
			if hits > 1 {
				t.Fatalf("ctx=%s net=%v matched %d cells", ctx, net, hits)
			}
		}
	}
}

func TestFXConditionsCoverTheQuestion(t *testing.T) {
	names := map[string]bool{}
	for _, c := range FXConditions() {
		if c.Name == "" || c.Detect == nil {
			t.Errorf("malformed condition %q", c.Name)
		}
		if names[c.Name] {
			t.Errorf("duplicate %s", c.Name)
		}
		names[c.Name] = true
	}
	// FX alone, flow alone, the interactions, and confirmation/divergence — without the
	// first two the interaction rows have nothing to be compared against.
	for _, want := range []string{
		"FX_BASELINE_ALL_DATES",
		"FX_TWD_APPRECIATION", "FX_TWD_DEPRECIATION", "FX_TWD_STABLE",
		"FGN_BUY", "FGN_SELL",
		"FX_APPR_X_FGN_BUY", "FX_APPR_X_FGN_SELL", "FX_DEPR_X_FGN_BUY", "FX_DEPR_X_FGN_SELL",
		"FX_FGN_CONFIRMATION", "FX_FGN_DIVERGENCE",
	} {
		if !names[want] {
			t.Errorf("the study is missing %s", want)
		}
	}
}

// Forward returns must not read past the end of the data, and a horizon that runs off the
// end simply has no entry.
func TestForwardReturnsDoNotRunOffTheEnd(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 60, 100, 0.003, 1000))
	panel := BuildFXPanel(u, nil, nil, nil, fx.DefaultConfig(), []int{5, 20})
	last := panel.Days[len(panel.Days)-1]
	if _, ok := last.Fwd[5]; ok {
		t.Error("the final date reported a 5d forward return")
	}
	if _, ok := panel.Days[0].Fwd[5]; !ok {
		t.Error("an early date should have a measurable 5d forward return")
	}
}

// ── effective sample size ─────────────────────────────────────────────────────────────

// THE overlap guard. Consecutive dates share almost all of a 20-day forward window; counting
// them as independent claims twenty times more evidence than exists.
func TestNonOverlappingRespectsTheForwardWindow(t *testing.T) {
	// Twenty consecutive dates, 20-day horizon → exactly one independent observation.
	consecutive := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
	if got := nonOverlapping(consecutive, 20); len(got) != 1 {
		t.Errorf("20 consecutive dates at horizon 20 → %d observations, want 1: %v", len(got), got)
	}
	// Spread far apart → all independent.
	spread := []int{0, 25, 50, 75}
	if got := nonOverlapping(spread, 20); len(got) != 4 {
		t.Errorf("dates 25 apart at horizon 20 → %d, want 4", len(got))
	}
	// Exactly one horizon apart is far enough; one day less is not.
	if got := nonOverlapping([]int{0, 20}, 20); len(got) != 2 {
		t.Errorf("exactly one horizon apart should be independent, got %d", len(got))
	}
	if got := nonOverlapping([]int{0, 19}, 20); len(got) != 1 {
		t.Errorf("one day short of a horizon should NOT be independent, got %d", len(got))
	}
	// A shorter horizon admits more observations from the same dates.
	if a, b := len(nonOverlapping(consecutive, 5)), len(nonOverlapping(consecutive, 20)); a <= b {
		t.Errorf("horizon 5 gave %d and horizon 20 gave %d — shorter must admit more", a, b)
	}
	// Order is preserved and nothing is invented.
	got := nonOverlapping(consecutive, 5)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("selection is not ordered: %v", got)
		}
	}
}

// The study must report the effective sample, and it must be far smaller than the raw count
// for a condition that fires on consecutive days.
func TestStudyReportsEffectiveNotRawN(t *testing.T) {
	var stocks []*Stock
	for i := 0; i < 8; i++ {
		stocks = append(stocks, teStock(fmt.Sprintf("S%d", i), "族群", 200, 100, 0.003, 1000))
	}
	u := teUniverse(stocks...)
	panel := BuildFXPanel(u, nil, nil, nil, fx.DefaultConfig(), []int{5, 20})

	var base FXStat
	for _, st := range RunFXStudy(panel, FXConditions(), []int{5, 20}) {
		if st.Name == "FX_BASELINE_ALL_DATES" {
			base = st
		}
	}
	if base.RawDates == 0 {
		t.Fatal("the baseline selected nothing")
	}
	if base.EffectiveN[20] >= base.RawDates {
		t.Errorf("effective N %d is not smaller than raw %d — the overlap was not removed",
			base.EffectiveN[20], base.RawDates)
	}
	// Roughly raw/horizon, since the baseline fires every day.
	if want := base.RawDates / 20; base.EffectiveN[20] > want+2 {
		t.Errorf("effective N %d for %d consecutive dates at horizon 20, want about %d",
			base.EffectiveN[20], base.RawDates, want)
	}
	if base.EffectiveN[5] <= base.EffectiveN[20] {
		t.Error("a shorter horizon must yield more independent observations")
	}
}

// A confidence interval that moves between runs cannot be checked by anyone.
func TestBootstrapIsReproducible(t *testing.T) {
	vals := []float64{-3, -1, 0.5, 2, 4, -2, 1.5, 0.2, -0.7, 3.3}
	a1, a2, a3, a4 := bootstrapCI(vals, 20)
	b1, b2, b3, b4 := bootstrapCI(vals, 20)
	if a1 != b1 || a2 != b2 || a3 != b3 || a4 != b4 {
		t.Error("the same sample produced different intervals on two calls")
	}
	// A different horizon is a different question and may legitimately differ.
	if c1, _, _, _ := bootstrapCI(vals, 5); c1 == a1 && len(vals) > 3 {
		t.Log("horizons happened to agree; not an error")
	}
	// The interval must bracket the point estimate.
	w := winRateOf(vals)
	if w < a1-1e-9 || w > a2+1e-9 {
		t.Errorf("win rate %.1f outside its own interval [%.1f, %.1f]", w, a1, a2)
	}
}

// Too few observations must yield NO interval rather than a precise-looking fiction.
func TestBootstrapRefusesThinSamples(t *testing.T) {
	for _, vals := range [][]float64{nil, {1}, {1, 2}} {
		lo, hi, mlo, mhi := bootstrapCI(vals, 20)
		if !math.IsNaN(lo) || !math.IsNaN(hi) || !math.IsNaN(mlo) || !math.IsNaN(mhi) {
			t.Errorf("n=%d produced an interval [%v, %v]", len(vals), lo, hi)
		}
	}
}

// ── DXY / residual causality ──────────────────────────────────────────────────────────

// The dollar index is subject to the same contract as the pair: a Taiwan session may not
// read the bar carrying its own date.
func TestDXYAndResidualAreCausal(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 400, 100, 0.003, 1000))

	// Both series wiggle, then the dollar jumps on one date.
	tw := make([]float64, len(u.Axis))
	dx := make([]float64, len(u.Axis))
	pt, pd := 30.0, 100.0
	for i := range tw {
		j := 0.0015
		if i%2 == 0 {
			j = -0.0015
		}
		pt *= 1 + j*0.4
		pd *= 1 + j
		tw[i], dx[i] = pt, pd
	}
	jump := 300
	for i := jump; i < len(dx); i++ {
		dx[i] *= 1.05
	}
	twS, dxS := fxSeriesFor(u.Axis, tw...), fxSeriesFor(u.Axis, dx...)
	dxS.Symbol = fx.SymbolDXY

	panel := BuildFXPanel(u, twS, dxS, nil, fx.DefaultConfig(), []int{5})
	if !panel.Days[jump+1].DXYOK {
		t.Skip("fixture did not produce a usable DXY reading; nothing to assert")
	}
	// The jump must not be visible on its own date.
	onJump, after := panel.Days[jump], panel.Days[jump+1]
	if onJump.DXYOK && after.DXYOK && onJump.DXYChange20 == after.DXYChange20 {
		t.Error("the DXY reading did not advance across the jump date")
	}

	// Truncating both archives at the date must leave that date's reading untouched.
	twT := &fx.Series{Symbol: twS.Symbol, Bars: append([]fx.Bar(nil), twS.Bars[:jump]...)}
	dxT := &fx.Series{Symbol: dxS.Symbol, Bars: append([]fx.Bar(nil), dxS.Bars[:jump]...)}
	trunc := BuildFXPanel(u, twT, dxT, nil, fx.DefaultConfig(), []int{5})

	a, b := panel.Days[jump], trunc.Days[jump]
	if a.DXYChange20 != b.DXYChange20 {
		t.Errorf("future DXY bars changed a past reading: %v vs %v", a.DXYChange20, b.DXYChange20)
	}
	if a.ResidualOK != b.ResidualOK || a.Residual != b.Residual {
		t.Errorf("future bars changed a past residual: %v/%v vs %v/%v",
			a.ResidualOK, a.Residual, b.ResidualOK, b.Residual)
	}
}

// With no dollar archive, the dollar and residual conditions must select nothing — never
// fall back to treating the pair as if it were the dollar.
func TestNoDXYArchiveLeavesDollarRowsEmpty(t *testing.T) {
	u := teUniverse(teStock("AAA", "族群", 200, 100, 0.003, 1000))
	panel := BuildFXPanel(u, fxSeriesFor(u.Axis), nil, nil, fx.DefaultConfig(), []int{5})

	for _, d := range panel.Days {
		if d.DXYOK || d.ResidualOK {
			t.Fatal("a day claimed a dollar or residual reading with no DXY archive")
		}
	}
	for _, st := range RunFXStudy(panel, FXConditions(), []int{5}) {
		switch st.Name {
		case "DXY_USD_APPRECIATION", "DXY_USD_DEPRECIATION",
			"RESIDUAL_TWD_WEAK", "RESIDUAL_TWD_STRONG",
			"DXY_UP_X_FGN_SELL", "RESIDUAL_TWD_WEAK_X_FGN_SELL":
			if st.RawDates != 0 {
				t.Errorf("%s selected %d dates with no dollar data", st.Name, st.RawDates)
			}
		}
	}
}

// An interaction is only interesting relative to the leg it contains. This checks the
// comparison is wired to the right leg and reports how much of it survived.
func TestIncrementsCompareAgainstTheLeg(t *testing.T) {
	stats := []FXStat{
		{Name: "FGN_SELL", RawDates: 100,
			EffectiveN: map[int]int{20: 10}, WinRate: map[int]float64{20: 40}},
		{Name: "FX_DEPR_X_FGN_SELL", RawDates: 40,
			EffectiveN: map[int]int{20: 5}, WinRate: map[int]float64{20: 48}},
	}
	got := Increments(stats, 20)
	if len(got) != 1 {
		t.Fatalf("got %d increments, want 1", len(got))
	}
	inc := got[0]
	if inc.Leg != "FGN_SELL" {
		t.Errorf("compared against %q", inc.Leg)
	}
	if inc.Delta != 8 {
		t.Errorf("delta = %v, want 48-40 = 8", inc.Delta)
	}
	if inc.RetainedPct != 40 {
		t.Errorf("retained = %v%%, want 40%%", inc.RetainedPct)
	}
	// A leg with no effective observations yields no comparison rather than a fake one.
	if len(Increments([]FXStat{
		{Name: "FGN_SELL", EffectiveN: map[int]int{20: 0}},
		{Name: "FX_DEPR_X_FGN_SELL", EffectiveN: map[int]int{20: 5}, WinRate: map[int]float64{20: 50}},
	}, 20)) != 0 {
		t.Error("an increment was reported against an empty leg")
	}
}
