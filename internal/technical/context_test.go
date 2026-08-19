package technical

import (
	"strings"
	"testing"
)

// The single most important guard in R14: nothing this package emits may read as an
// instruction. If a verb ever appears in the signal vocabulary, the aggregator has quietly
// become a decision engine.
func TestSignalsNeverInstruct(t *testing.T) {
	forbidden := []string{
		"BUY", "SELL", "ADD", "REDUCE", "AVOID", "HOLD", "ENTER", "EXIT",
		"LONG", "SHORT", "TAKE_PROFIT", "STOP", "TARGET", "RECOMMEND",
	}
	all := []string{
		SigStrongBullishTrend, SigStrongBearishTrend, SigTrendForming, SigNoTrend,
		SigMomentumAccelerating, SigMomentumDecelerating, SigBearishAccelerating,
		SigMACDGoldenCross, SigMACDDeathCross,
		SigKeltnerUpperBreakout, SigKeltnerLowerBreakdown,
		SigTrendExpansion, SigUnconfirmedBreakout,
		SigOverboughtStrongTrend, SigOversoldStrongTrend,
		SigNearResistance, SigNearSupport, SigAboveAllResistance, SigBelowAllSupport,
	}
	for _, sig := range all {
		for _, word := range forbidden {
			// Word-boundary check on the underscore-separated tokens, so BREAKDOWN is not
			// flagged for containing "DOWN" and BULLISH is not flagged for "BUY".
			for _, token := range strings.Split(sig, "_") {
				if token == word {
					t.Errorf("signal %q contains the instruction %q — this is evidence, not a decision",
						sig, word)
				}
			}
		}
	}
}

// Every dimension falls back independently. An absent ADX must not make a stock look like it
// has a WEAK trend: "not measured" and "measured, and weak" are different facts.
func TestMissingIndicatorsStayUnknown(t *testing.T) {
	c := BuildContext(Result{})
	if c.Direction != DirUnknown || c.TrendStrength != StrengthUnknown ||
		c.Momentum != MomentumUnknown || c.Volatility != VolUnknown ||
		c.PriceLocation != LocationUnknown {
		t.Errorf("an empty result gave %+v, want UNKNOWN everywhere", c)
	}
	if len(c.Signals) != 0 {
		t.Errorf("an empty result produced signals: %v", c.Signals)
	}

	// A PRESENT but non-OK indicator is the same as an absent one for interpretation.
	c = BuildContext(Result{
		ADX: &ADXResult{Status: StatusInsufficientData, Strength: StrengthWeak, Direction: DirBullish},
	})
	if c.TrendStrength != StrengthUnknown || c.Direction != DirUnknown {
		t.Errorf("a non-OK ADX leaked into the context: %+v", c)
	}
}

func TestContextDirectionPrefersADXAndReportsDisagreement(t *testing.T) {
	bullADX := &ADXResult{Status: StatusOK, Direction: DirBullish, Strength: StrengthStrong}
	bearMACD := &MACDResult{Status: StatusOK, MACD: -1, SignalVal: 0}
	bullMACD := &MACDResult{Status: StatusOK, MACD: 1, SignalVal: 0}

	if got := contextDirection(bullADX, bullMACD, nil); got != DirBullish {
		t.Errorf("agreement = %s, want BULLISH", got)
	}
	// The two strongest sources disagreeing is worth saying so, not worth a casting vote.
	if got := contextDirection(bullADX, bearMACD, nil); got != DirMixed {
		t.Errorf("disagreement = %s, want MIXED", got)
	}
	// A neutral ADX defers to MACD rather than flattening the reading.
	neutralADX := &ADXResult{Status: StatusOK, Direction: DirNeutral, Strength: StrengthWeak}
	if got := contextDirection(neutralADX, bullMACD, nil); got != DirBullish {
		t.Errorf("neutral ADX + bullish MACD = %s, want BULLISH", got)
	}
	// With neither, RSI is the weakest fallback — but it is still better than UNKNOWN.
	if got := contextDirection(nil, nil, &RSIResult{Status: StatusOK, Zone: RSIBullish}); got != DirBullish {
		t.Errorf("RSI-only = %s, want BULLISH", got)
	}
	if got := contextDirection(nil, nil, nil); got != DirUnknown {
		t.Errorf("nothing at all = %s, want UNKNOWN", got)
	}
}

// The confirmation the spec describes: ADX >= 25, +DI > -DI, MACD above signal, histogram
// rising, RSI above 55 → STRONG_BULLISH_TREND. And no score is touched by it.
func TestStrongBullishConfirmation(t *testing.T) {
	c := BuildContext(Result{
		ADX:  &ADXResult{Status: StatusOK, ADX: 31.8, PlusDI: 30, MinusDI: 15, Direction: DirBullish, Strength: StrengthStrong},
		RSI:  &RSIResult{Status: StatusOK, Value: 61.3, Zone: RSIBullish, Rising: true},
		MACD: &MACDResult{Status: StatusOK, MACD: 2.1, SignalVal: 1.7, Histogram: 0.4, Momentum: MomentumAccelerating},
	})
	if c.Direction != DirBullish || c.TrendStrength != StrengthStrong {
		t.Errorf("context = %+v", c)
	}
	if c.Momentum != MomentumAccelerating {
		t.Errorf("momentum = %s, want ACCELERATING", c.Momentum)
	}
	if !hasSignal(c, SigStrongBullishTrend) {
		t.Errorf("signals = %v, want STRONG_BULLISH_TREND", c.Signals)
	}
	if !hasSignal(c, SigMomentumAccelerating) {
		t.Errorf("signals = %v, want the accelerating-momentum signal", c.Signals)
	}
}

// A breakout WITH a strong trend behind it and the same breakout with ADX under 20 are
// different events. Naming both is what lets the R14 backtest ask which one actually pays.
func TestBreakoutConfirmationVersusUnconfirmed(t *testing.T) {
	breakout := &KeltnerResult{Status: StatusOK, Channel: ChannelBreakout, Position: PosAboveUpper}

	confirmed := BuildContext(Result{
		ADX:     &ADXResult{Status: StatusOK, ADX: 32, Direction: DirBullish, Strength: StrengthStrong},
		Keltner: breakout,
	})
	if !hasSignal(confirmed, SigTrendExpansion) {
		t.Errorf("signals = %v, want TREND_EXPANSION", confirmed.Signals)
	}
	if hasSignal(confirmed, SigUnconfirmedBreakout) {
		t.Errorf("a confirmed breakout was also called unconfirmed: %v", confirmed.Signals)
	}

	unconfirmed := BuildContext(Result{
		ADX:     &ADXResult{Status: StatusOK, ADX: 14, Direction: DirNeutral, Strength: StrengthWeak},
		Keltner: breakout,
	})
	if !hasSignal(unconfirmed, SigUnconfirmedBreakout) {
		t.Errorf("signals = %v, want UNCONFIRMED_BREAKOUT", unconfirmed.Signals)
	}
	if hasSignal(unconfirmed, SigTrendExpansion) {
		t.Errorf("a breakout with ADX 14 was called an expansion: %v", unconfirmed.Signals)
	}
	// Both must still report the breakout itself.
	for _, c := range []*Context{confirmed, unconfirmed} {
		if !hasSignal(c, SigKeltnerUpperBreakout) || c.Volatility != VolBreakout {
			t.Errorf("the breakout itself was lost: %+v", c)
		}
	}
}

// An overbought RSI inside a strong trend is a DIFFERENT event from one in a directionless
// tape. Collapsing them into "OVERBOUGHT" would make the continuation-vs-reversion question
// R14 exists to answer unanswerable.
func TestOverboughtIsReportedWithItsTrendContext(t *testing.T) {
	strong := BuildContext(Result{
		ADX: &ADXResult{Status: StatusOK, ADX: 45, Direction: DirBullish, Strength: StrengthVeryStrong},
		RSI: &RSIResult{Status: StatusOK, Value: 78, Zone: RSIOverbought},
	})
	if !hasSignal(strong, SigOverboughtStrongTrend) {
		t.Errorf("signals = %v, want OVERBOUGHT_IN_STRONG_TREND", strong.Signals)
	}

	weak := BuildContext(Result{
		ADX: &ADXResult{Status: StatusOK, ADX: 12, Direction: DirNeutral, Strength: StrengthWeak},
		RSI: &RSIResult{Status: StatusOK, Value: 78, Zone: RSIOverbought},
	})
	if hasSignal(weak, SigOverboughtStrongTrend) {
		t.Errorf("an overbought reading in a WEAK tape was labelled as in a strong trend: %v", weak.Signals)
	}
}

// A still-positive MACD that is losing steam must report DECELERATING — the reading a
// level-only view cannot produce.
func TestDeceleratingBullishMomentum(t *testing.T) {
	c := BuildContext(Result{
		ADX:  &ADXResult{Status: StatusOK, ADX: 28, Direction: DirBullish, Strength: StrengthStrong},
		MACD: &MACDResult{Status: StatusOK, MACD: 2.0, SignalVal: 1.8, Histogram: 0.2, Momentum: MomentumDecelerating},
	})
	if c.Momentum != MomentumDecelerating {
		t.Errorf("momentum = %s, want DECELERATING", c.Momentum)
	}
	if !hasSignal(c, SigMomentumDecelerating) {
		t.Errorf("signals = %v, want the decelerating signal", c.Signals)
	}
	// The trend itself is still strong and bullish; only the momentum is fading.
	if !hasSignal(c, SigStrongBullishTrend) {
		t.Errorf("signals = %v, the trend reading should survive a momentum slowdown", c.Signals)
	}
}

// EXPANDING / CONTRACTING require a width history this single-bar view does not have.
// Guessing them would be exactly the fabrication R14 forbids.
func TestVolatilityNeverGuessesATrajectory(t *testing.T) {
	inside := BuildContext(Result{
		Keltner: &KeltnerResult{Status: StatusOK, Channel: ChannelInside, Position: PosUpperHalf},
	})
	if inside.Volatility != VolNormal {
		t.Errorf("volatility = %s, want NORMAL", inside.Volatility)
	}
	if inside.Volatility == VolExpanding || inside.Volatility == VolContracting {
		t.Error("a single-bar view claimed to know the width trajectory")
	}
	none := BuildContext(Result{})
	if none.Volatility != VolUnknown {
		t.Errorf("volatility with no Keltner = %s, want UNKNOWN", none.Volatility)
	}
}

func TestSignalOrderIsStructuralFirst(t *testing.T) {
	c := BuildContext(Result{
		ADX:     &ADXResult{Status: StatusOK, ADX: 32, Direction: DirBullish, Strength: StrengthStrong},
		MACD:    &MACDResult{Status: StatusOK, MACD: 2, SignalVal: 1, Histogram: 1, Momentum: MomentumAccelerating},
		Keltner: &KeltnerResult{Status: StatusOK, Channel: ChannelBreakout, Position: PosAboveUpper},
		Pivot:   &PivotResult{Status: StatusOK, Location: LocNearResistance, NearestLevel: "R1"},
	})
	if len(c.Signals) == 0 {
		t.Fatal("no signals")
	}
	if c.Signals[0] != SigStrongBullishTrend {
		t.Errorf("first signal = %s, want the structural trend reading first", c.Signals[0])
	}
	if c.Signals[len(c.Signals)-1] != SigNearResistance {
		t.Errorf("last signal = %s, want the local price location last", c.Signals[len(c.Signals)-1])
	}
}

// End-to-end through Analyze: a real price path, not hand-built structs.
func TestAnalyzeProducesAContextFromRealBars(t *testing.T) {
	r := Analyze(trendBars(120, 100, 0.015), DefaultConfig())
	if r == nil || r.Context == nil {
		t.Fatal("Analyze must always return a result with a context")
	}
	for name, st := range map[string]Status{
		"adx": r.ADX.Status, "rsi": r.RSI.Status, "macd": r.MACD.Status,
		"keltner": r.Keltner.Status, "pivot": r.Pivot.Status,
	} {
		if st != StatusOK {
			t.Errorf("%s = %s on 120 clean bars, want OK", name, st)
		}
	}
	if r.Context.Direction != DirBullish {
		t.Errorf("direction = %s on a relentless uptrend, want BULLISH", r.Context.Direction)
	}
	if r.Config.ADXPeriod != 14 {
		t.Errorf("the result must record the parameters it was produced under, got %+v", r.Config)
	}
}

// Analyze NEVER returns nil, and a thin history yields a result whose sub-statuses explain
// themselves rather than a nil the caller cannot distinguish from "feature off".
func TestAnalyzeNeverReturnsNil(t *testing.T) {
	r := Analyze(nil, DefaultConfig())
	if r == nil || r.Context == nil {
		t.Fatal("Analyze(nil) must still return a result")
	}
	if r.ADX.Status != StatusNoData || r.Pivot.Status != StatusNoData {
		t.Errorf("statuses = %s / %s, want NO_DATA", r.ADX.Status, r.Pivot.Status)
	}
	if r.Context.Direction != DirUnknown {
		t.Errorf("direction = %s, want UNKNOWN", r.Context.Direction)
	}

	thin := Analyze(trendBars(25, 100, 0.01), DefaultConfig())
	if thin.MACD.Status != StatusInsufficientData {
		t.Errorf("MACD on 25 bars = %s, want INSUFFICIENT_DATA", thin.MACD.Status)
	}
}

func TestMinBarsMatchesTheSlowestIndicator(t *testing.T) {
	cfg := DefaultConfig()
	need := MinBars(cfg)
	// MACD is the slowest at the defaults: 26 + 9 + 1 = 36, versus ADX's 28.
	if need != cfg.MACDSlow+cfg.MACDSignal+1 {
		t.Errorf("MinBars = %d, want the MACD requirement %d", need, cfg.MACDSlow+cfg.MACDSignal+1)
	}
	// At exactly MinBars every indicator must publish.
	r := Analyze(trendBars(need, 100, 0.01), cfg)
	for name, st := range map[string]Status{
		"adx": r.ADX.Status, "rsi": r.RSI.Status, "macd": r.MACD.Status,
		"keltner": r.Keltner.Status, "pivot": r.Pivot.Status,
	} {
		if st != StatusOK {
			t.Errorf("%s = %s at MinBars(%d), want OK", name, st, need)
		}
	}
	// One bar short, at least one must not.
	short := Analyze(trendBars(need-1, 100, 0.01), cfg)
	if short.MACD.Status == StatusOK && short.ADX.Status == StatusOK &&
		short.Keltner.Status == StatusOK {
		t.Error("everything published one bar below MinBars — the bound is wrong")
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	bars := trendBars(120, 100, 0.01)
	a := Analyze(bars, DefaultConfig())
	b := Analyze(bars, DefaultConfig())
	if *a.ADX != *b.ADX || *a.RSI != *b.RSI || *a.MACD != *b.MACD ||
		*a.Keltner != *b.Keltner || *a.Pivot != *b.Pivot {
		t.Error("same input gave different indicator results")
	}
	if a.Context.Direction != b.Context.Direction ||
		strings.Join(a.Context.Signals, ",") != strings.Join(b.Context.Signals, ",") {
		t.Error("same input gave a different context")
	}
}

func hasSignal(c *Context, want string) bool {
	for _, s := range c.Signals {
		if s == want {
			return true
		}
	}
	return false
}
