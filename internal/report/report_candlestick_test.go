package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func candleEntry(res *candlestick.Result) scanner.WatchlistEntry {
	return scanner.WatchlistEntry{
		A:             scanner.StockAnalysis{Symbol: "1111", Name: "Test", Close: 100},
		RocketScore:   60,
		RocketStage:   scanner.StagePreBreakout,
		WatchAction:   scanner.ActPrepare,
		ExplosionProb: "MEDIUM",
		DaysToWatch:   "1~3 天",
		Candlestick:   res,
	}
}

func hammerSig(conf float64) candlestick.PatternSignal {
	return candlestick.PatternSignal{
		Type: candlestick.PatternHammer, Direction: candlestick.DirectionBullish,
		Confidence: conf, Strength: 3, ContextMatched: true,
		Description: "下跌／低檔出現長下影線，符合 Hammer 型態。", SuggestedAdjustment: 4,
	}
}

func genericSig() candlestick.PatternSignal {
	return candlestick.PatternSignal{
		Type: candlestick.PatternLongLowerShadow, Direction: candlestick.DirectionNeutral,
		Confidence: 0.40, Strength: 1, ContextMatched: false,
		Description: "長下影線，但缺乏足夠反轉背景。", SuggestedAdjustment: 2,
	}
}

func engulfSig() candlestick.PatternSignal {
	return candlestick.PatternSignal{
		Type: candlestick.PatternBullishEngulfing, Direction: candlestick.DirectionBullish,
		Confidence: 0.90, Strength: 5, ContextMatched: true,
		Description: "下跌／低檔出現多頭吞噬，符合 Bullish Engulfing 反轉型態。", SuggestedAdjustment: 6,
	}
}

// sigWithReasons builds an OK signal whose Evidence row is driven entirely by these codes.
func sigWithReasons(codes ...string) candlestick.PatternSignal {
	s := hammerSig(0.7)
	for _, c := range codes {
		s.ConfidenceReasons = append(s.ConfidenceReasons, candlestick.ConfidenceReason{Code: c})
	}
	return s
}

func showCandle() GuardrailViewOptions { return GuardrailViewOptions{ShowCandlestick: true} }

func TestCandleHiddenWhenShowFalse(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.82)}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, GuardrailViewOptions{ShowCandlestick: false})
	for _, marker := range []string{"⑬", "K 線型態", "class=\"candle-card\"", "Shadow Research Only", "🟢 Bullish"} {
		if strings.Contains(html, marker) {
			t.Errorf("show=false must omit %q", marker)
		}
	}
}

func TestCandleByteIdenticalWhenDisabled(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{engulfSig(), hammerSig(0.82)}}
	withData := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, GuardrailViewOptions{ShowCandlestick: false})
	nilData := genHTML(t, []scanner.WatchlistEntry{candleEntry(nil)}, GuardrailViewOptions{ShowCandlestick: false})
	if withData != nilData {
		t.Errorf("disabled output must be byte-identical whether or not Candlestick data is present")
	}
}

func TestCandleShownWhenEnabled(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.82)}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	for _, marker := range []string{"⑬", "K 線型態", "class=\"candle-card\"", "HAMMER", "下跌／低檔出現長下影線"} {
		if !strings.Contains(html, marker) {
			t.Errorf("expected %q in ⑬ section", marker)
		}
	}
}

func TestCandleNoPattern(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusNoPattern}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "No candlestick pattern detected.") {
		t.Errorf("NO_PATTERN status message missing")
	}
	if strings.Contains(html, "class=\"candle-card\"") {
		t.Errorf("no cards should render for NO_PATTERN")
	}
}

func TestCandleInvalidBar(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusInvalidBar}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "Latest candle invalid.") {
		t.Errorf("INVALID_BAR status message missing")
	}
}

func TestCandleNoData(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusNoData}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "Insufficient candle data.") {
		t.Errorf("NO_DATA status message missing")
	}
}

func TestCandleTwoCards(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{engulfSig(), hammerSig(0.75)}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if n := strings.Count(html, "class=\"candle-card\""); n != 2 {
		t.Errorf("expected 2 cards, got %d", n)
	}
}

func TestCandleOneCard(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.75)}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if n := strings.Count(html, "class=\"candle-card\""); n != 1 {
		t.Errorf("expected 1 card, got %d", n)
	}
}

func TestCandleDirectionBadge(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{genericSig()}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "⚪ Neutral") {
		t.Errorf("neutral badge missing")
	}
	res2 := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.7)}}
	html2 := genHTML(t, []scanner.WatchlistEntry{candleEntry(res2)}, showCandle())
	if !strings.Contains(html2, "🟢 Bullish") {
		t.Errorf("bullish badge missing")
	}
}

func TestCandleSummaryChipThreshold(t *testing.T) {
	// >= 0.80 → chip present.
	hi := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.82)}}
	if !strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(hi)}, showCandle()), "class=\"candle-chip\"") {
		t.Errorf("confidence 0.82 should show summary chip")
	}
	// 0.79 → no chip.
	lo := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.79)}}
	if strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(lo)}, showCandle()), "class=\"candle-chip\"") {
		t.Errorf("confidence 0.79 must NOT show summary chip")
	}
}

func TestCandleGenericLabel(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{genericSig()}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "Generic Pattern") {
		t.Errorf("LONG_LOWER_SHADOW should be labelled Generic Pattern")
	}
	// a formal HAMMER must NOT be labelled generic
	res2 := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.7)}}
	if strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(res2)}, showCandle()), "Generic Pattern") {
		t.Errorf("HAMMER must not be labelled Generic Pattern")
	}
}

func TestCandleEvidenceRow(t *testing.T) {
	// All three positive → ✓ Trend / ✓ Near Extreme / ✓ Volume.
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{
		sigWithReasons(candlestick.ReasonTrendMatch, candlestick.ReasonNearExtreme, candlestick.ReasonVolumeConfirm),
	}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	for _, want := range []string{"Evidence", "✓ Trend", "✓ Near Extreme", "✓ Volume"} {
		if !strings.Contains(html, want) {
			t.Errorf("evidence row missing %q", want)
		}
	}
	// Direction-agnostic label: never the literal "Near Low"/"Near High".
	if strings.Contains(html, "Near Low") || strings.Contains(html, "Near High") {
		t.Errorf("evidence must use direction-agnostic 'Near Extreme', not Near Low/High")
	}
}

func TestCandleEvidenceAbsenceIsDash(t *testing.T) {
	// No reasons at all → Trend/Near are "—", never "✗" (absence ≠ opposite).
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons()}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	for _, want := range []string{"— Trend", "— Near Extreme", "— Volume"} {
		if !strings.Contains(html, want) {
			t.Errorf("evidence row missing %q", want)
		}
	}
	if strings.Contains(html, "✗ Trend") || strings.Contains(html, "✗ Near Extreme") {
		t.Errorf("Trend/Near must never render ✗ (absence of evidence ≠ opposite)")
	}
}

func TestCandleEvidenceVolumeTriState(t *testing.T) {
	weak := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons(candlestick.ReasonWeakVolume)}}
	if h := genHTML(t, []scanner.WatchlistEntry{candleEntry(weak)}, showCandle()); !strings.Contains(h, "✗ Volume") {
		t.Errorf("WEAK_VOLUME must render '✗ Volume'")
	}
	none := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons()}}
	h := genHTML(t, []scanner.WatchlistEntry{candleEntry(none)}, showCandle())
	if !strings.Contains(h, "— Volume") || strings.Contains(h, "✗ Volume") {
		t.Errorf("no volume evidence must render '— Volume', not '✗ Volume'")
	}
}

func TestCandleEvidenceStageOmittedInP0(t *testing.T) {
	// STAGE_SUPPORT never appears in P0, so the Stage cell must not render (no dead column).
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{
		sigWithReasons(candlestick.ReasonTrendMatch, candlestick.ReasonNearExtreme, candlestick.ReasonVolumeConfirm),
	}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if strings.Contains(html, "Stage") {
		t.Errorf("P0 must not render any Stage evidence cell")
	}
	// Forward-compat: once STAGE_SUPPORT is credited, '✓ Stage' lights up with no template change.
	fwd := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons(candlestick.ReasonStageSupport)}}
	if !strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(fwd)}, showCandle()), "✓ Stage") {
		t.Errorf("STAGE_SUPPORT should render '✓ Stage' when present")
	}
}

func TestCandleContextLimitedBadge(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons(candlestick.ReasonMissingContext)}}
	if !strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle()), "Context Limited") {
		t.Errorf("MISSING_CONTEXT must render the 'Context Limited' badge")
	}
	no := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{sigWithReasons(candlestick.ReasonTrendMatch)}}
	if strings.Contains(genHTML(t, []scanner.WatchlistEntry{candleEntry(no)}, showCandle()), "Context Limited") {
		t.Errorf("no MISSING_CONTEXT must NOT render 'Context Limited'")
	}
}

func TestCandleAdjustmentLabel(t *testing.T) {
	res := &candlestick.Result{Status: candlestick.StatusOK, Signals: []candlestick.PatternSignal{hammerSig(0.7)}}
	html := genHTML(t, []scanner.WatchlistEntry{candleEntry(res)}, showCandle())
	if !strings.Contains(html, "Suggested Adjustment +4") || !strings.Contains(html, "Not Applied") {
		t.Errorf("adjustment must be shown as '+4' and 'Not Applied'")
	}
	if !strings.Contains(html, "Confidence 70%") || !strings.Contains(html, "Strength 3/5") {
		t.Errorf("confidence %% and strength X/5 must render separately")
	}
}
