package r6backtest

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/technical"
)

// The backtest must measure the SHIPPED parameters. If this drifts, the validation is
// reporting on a hypothesis nobody runs.
func TestBacktestUsesShippedTechnicalConfig(t *testing.T) {
	if techCfg != technical.DefaultConfig() {
		t.Errorf("backtest config diverged from the shipped default:\n%+v\n%+v",
			techCfg, technical.DefaultConfig())
	}
	if err := techCfg.Validate(); err != nil {
		t.Errorf("the config under validation does not validate: %v", err)
	}
}

// No condition may read a bar after i. Truncating the series at i must not change any
// verdict — the look-ahead guard, asserted rather than asserted-in-a-comment.
func TestTechnicalConditionsAreCausal(t *testing.T) {
	full := teStock("AAA", "半導體", 400, 100, 0.004, 1000)
	i := 350
	truncated := teStock("AAA", "半導體", i+1, 100, 0.004, 1000)

	for _, c := range TechnicalConditions() {
		a := c.Detect(technical.Analyze(barsUpTo(full, i), techCfg))
		b := c.Detect(technical.Analyze(barsUpTo(truncated, i), techCfg))
		if a != b {
			t.Errorf("%s: verdict changed when future bars were removed (%v → %v) — it reads ahead",
				c.Name, a, b)
		}
	}
}

// barsUpTo must hand over exactly bars 0..i, never one more.
func TestBarsUpToIsInclusiveAndBounded(t *testing.T) {
	s := teStock("AAA", "半導體", 100, 50, 0.003, 1000)

	got := barsUpTo(s, 40)
	if len(got) != 41 {
		t.Errorf("barsUpTo(40) returned %d bars, want 41", len(got))
	}
	if !got[40].Date.Equal(s.Candles[40].Date) {
		t.Error("the last bar is not bar i")
	}
	if barsUpTo(s, -1) != nil || barsUpTo(s, len(s.Candles)) != nil {
		t.Error("out-of-range indices must yield nothing rather than panic")
	}
}

func TestTechnicalConditionsCoverTheComparison(t *testing.T) {
	names := map[string]bool{}
	for _, c := range TechnicalConditions() {
		if c.Name == "" || c.Detect == nil {
			t.Errorf("malformed condition %q", c.Name)
		}
		if names[c.Name] {
			t.Errorf("duplicate condition %s", c.Name)
		}
		names[c.Name] = true
	}
	// The comparison the R14 specification requires: a baseline, each indicator alone, and
	// the confirmed/unconfirmed pairs that make a combination's contribution measurable.
	for _, required := range []string{
		"BASELINE_ALL_BARS",
		"ADX_STRONG_BULLISH", "ADX_WEAK", "ADX_STRONG_BEARISH",
		"MACD_ABOVE_SIGNAL", "MACD_ABOVE_SIGNAL_ACCELERATING",
		"RSI_OVERBOUGHT", "RSI_OVERBOUGHT_IN_STRONG_TREND",
		"KELTNER_BREAKOUT", "KELTNER_BREAKOUT_ADX_CONFIRMED", "KELTNER_BREAKOUT_UNCONFIRMED",
		"PIVOT_NEAR_RESISTANCE", "PIVOT_NEAR_SUPPORT",
	} {
		if !names[required] {
			t.Errorf("the comparison is missing %s", required)
		}
	}
}

// A confirmed breakout and an unconfirmed one must be mutually exclusive. If the same bar
// could satisfy both, comparing their forward returns would be meaningless.
func TestConfirmedAndUnconfirmedBreakoutsAreDisjoint(t *testing.T) {
	var confirmed, unconfirmed TechnicalCondition
	for _, c := range TechnicalConditions() {
		switch c.Name {
		case "KELTNER_BREAKOUT_ADX_CONFIRMED":
			confirmed = c
		case "KELTNER_BREAKOUT_UNCONFIRMED":
			unconfirmed = c
		}
	}
	for _, drift := range []float64{-0.01, -0.002, 0, 0.002, 0.01, 0.03} {
		s := teStock("AAA", "族群", 300, 100, drift, 1000)
		for i := 250; i < len(s.Candles); i++ {
			r := technical.Analyze(barsUpTo(s, i), techCfg)
			if confirmed.Detect(r) && unconfirmed.Detect(r) {
				t.Fatalf("drift %v bar %d satisfies both breakout conditions", drift, i)
			}
		}
	}
}

// The baseline must actually fire on ordinary bars — a control that never triggers gives
// every other condition nothing to be compared against.
func TestBaselineFiresOnOrdinaryBars(t *testing.T) {
	var baseline TechnicalCondition
	for _, c := range TechnicalConditions() {
		if c.Name == "BASELINE_ALL_BARS" {
			baseline = c
		}
	}
	s := teStock("AAA", "族群", 200, 100, 0.003, 1000)
	var fired int
	for i := 100; i < len(s.Candles); i++ {
		if baseline.Detect(technical.Analyze(barsUpTo(s, i), techCfg)) {
			fired++
		}
	}
	if fired == 0 {
		t.Fatal("the baseline never fired")
	}
}

// The engine adapter must produce a trigger exactly when its condition holds, and nothing
// when it does not.
func TestTechnicalSetupsWrapEveryCondition(t *testing.T) {
	setups := TechnicalSetups(nil)
	if len(setups) != len(TechnicalConditions()) {
		t.Fatalf("wrapped %d of %d conditions", len(setups), len(TechnicalConditions()))
	}
	s := teStock("AAA", "族群", 300, 100, 0.006, 1000)
	i := len(s.Candles) - 1

	for _, st := range setups {
		trig := st.Detect(nil, nil, s, i, Params{})
		var cond TechnicalCondition
		for _, c := range TechnicalConditions() {
			if c.Name == st.Name() {
				cond = c
			}
		}
		want := cond.Detect(technical.Analyze(barsUpTo(s, i), techCfg))
		if want != (trig != nil) {
			t.Errorf("%s: setup fired=%v but the condition says %v", st.Name(), trig != nil, want)
		}
	}
}

// A condition must be silent while ITS indicator is still in warm-up — and only while.
//
// The windows differ by a lot, and conflating them would be wrong in both directions. A
// pivot needs only the previous session, so at three bars it legitimately has an answer; ADX
// needs 28 and MACD 36, so at three bars they must say nothing rather than produce a verdict
// from a half-warm series. This asserts each one against its own requirement.
func TestTechnicalSetupsRespectEachIndicatorsWindow(t *testing.T) {
	s := teStock("AAA", "族群", 3, 100, 0.01, 1000)
	i := len(s.Candles) - 1

	// At three bars only the pivot (and the baseline, which asks nothing of any window) can
	// have anything to say.
	mayFire := map[string]bool{
		"BASELINE_ALL_BARS":     true,
		"PIVOT_NEAR_RESISTANCE": true,
		"PIVOT_NEAR_SUPPORT":    true,
	}
	for _, st := range TechnicalSetups(nil) {
		fired := st.Detect(nil, nil, s, i, Params{}) != nil
		if fired && !mayFire[st.Name()] {
			t.Errorf("%s fired on 3 bars — its indicator is still in warm-up", st.Name())
		}
	}

	// And the underlying statuses say why, rather than reporting a zero reading.
	r := technical.Analyze(barsUpTo(s, i), techCfg)
	for name, st := range map[string]technical.Status{
		"adx": r.ADX.Status, "rsi": r.RSI.Status,
		"macd": r.MACD.Status, "keltner": r.Keltner.Status,
	} {
		if st != technical.StatusInsufficientData {
			t.Errorf("%s = %s on 3 bars, want INSUFFICIENT_DATA", name, st)
		}
	}
	if r.Pivot.Status != technical.StatusOK {
		t.Errorf("pivot = %s on 3 bars, want OK — it needs only the previous session", r.Pivot.Status)
	}
}

// The panel path and the direct path must reach identical verdicts. If they can differ, the
// backtest is measuring something the scanner does not do.
func TestPanelMatchesDirectAnalysis(t *testing.T) {
	s := teStock("AAA", "族群", 200, 100, 0.006, 1000)
	u := teUniverse(s)
	panel := BuildTechnicalPanel(u)

	direct := TechnicalSetups(nil)
	cached := TechnicalSetups(panel)

	for i := 0; i < len(s.Candles); i++ {
		for k := range direct {
			a := direct[k].Detect(u, nil, s, i, Params{}) != nil
			b := cached[k].Detect(u, nil, s, i, Params{}) != nil
			if a != b {
				t.Fatalf("%s at bar %d: direct=%v panel=%v", direct[k].Name(), i, a, b)
			}
		}
	}
}

// The panel must not consult future bars either: a stock's reading for bar i is the same
// whether or not later bars exist in the universe.
func TestTechnicalPanelIsCausal(t *testing.T) {
	long := BuildTechnicalPanel(teUniverse(teStock("AAA", "族群", 200, 100, 0.006, 1000)))
	short := BuildTechnicalPanel(teUniverse(teStock("AAA", "族群", 150, 100, 0.006, 1000)))

	for i := 0; i < 150; i++ {
		a, b := long.at("AAA", i), short.at("AAA", i)
		if a.adxOK != b.adxOK || a.adx != b.adx || a.rsi != b.rsi ||
			a.macdCross != b.macdCross || a.keltnerChannel != b.keltnerChannel {
			t.Fatalf("bar %d differs between a long and a short universe — the panel reads ahead", i)
		}
	}
}

// A symbol the panel never saw yields an empty reading rather than a panic or a fabricated one.
func TestTechnicalPanelUnknownSymbol(t *testing.T) {
	panel := BuildTechnicalPanel(teUniverse(teStock("AAA", "族群", 60, 100, 0.005, 1000)))
	if got := panel.at("ZZZ", 10); got.ok || got.adxOK {
		t.Errorf("unknown symbol gave %+v, want an empty reading", got)
	}
	if got := panel.at("AAA", 9999); got.ok {
		t.Error("an out-of-range bar gave a reading")
	}
	var nilPanel *TechnicalPanel
	if got := nilPanel.at("AAA", 0); got.ok {
		t.Error("a nil panel gave a reading")
	}
}
