package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func fxMetrics(closePx float64, ctx string, d1, d5, d20 float64) *fx.Metrics {
	return &fx.Metrics{
		Status: fx.StatusOK, AsOfTaiwanDate: "2026-06-05", FXDate: "2026-06-04",
		Close: closePx, Context: ctx,
		ChangePct: map[int]float64{1: d1, 5: d5, 20: d20},
	}
}

// The currency line must appear once, in the header — it is market-level, and repeating it
// per stock would be 100+ copies of one fact.
func TestFXBarRendersOnceInTheHeader(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)},
		GuardrailViewOptions{FX: fxMetrics(31.854, fx.ContextDepreciation, 0.12, 1.10, 2.40)})

	if n := strings.Count(html, `class="fx-bar"`); n != 1 {
		t.Errorf("the FX bar rendered %d times, want exactly 1", n)
	}
	// html/template escapes "+" as "&#43;" — the rendering is right, a naive assertion is not.
	for _, want := range []string{"USD/TWD", "31.854", "1D", "5D", "20D", "&#43;1.10%", "台幣貶值"} {
		if !strings.Contains(html, want) {
			t.Errorf("the FX bar is missing %q", want)
		}
	}
	// The causality gap must be stated where a reader will see it.
	if !strings.Contains(html, "2026-06-04") {
		t.Error("the FX bar does not say which session it used")
	}
	if !strings.Contains(html, "不影響任何評分") {
		t.Error("the FX bar does not say it is research-only")
	}
}

// THE direction guard, at the surface a human reads. USDTWD rising means TWD WEAKENED; a
// report that says the opposite would invert every conclusion while looking perfectly normal.
func TestFXBarDirectionIsNotInverted(t *testing.T) {
	weaker := genHTML(t, nil, GuardrailViewOptions{
		FX: fxMetrics(32.5, fx.ContextStrongDepreciation, 0.3, 1.5, 3.0)})
	if !strings.Contains(weaker, "台幣明顯貶值") {
		t.Error("a RISING USDTWD should read as TWD depreciation")
	}
	if strings.Contains(weaker, "台幣明顯升值") || strings.Contains(weaker, "台幣升值") {
		t.Error("a rising USDTWD was labelled appreciation — the sign is inverted")
	}

	stronger := genHTML(t, nil, GuardrailViewOptions{
		FX: fxMetrics(30.1, fx.ContextStrongAppreciation, -0.3, -1.5, -3.0)})
	if !strings.Contains(stronger, "台幣明顯升值") {
		t.Error("a FALLING USDTWD should read as TWD appreciation")
	}

	// And the colour follows the currency: a rising quote is the weak-TWD class.
	if !strings.Contains(weaker, "fx-chg fx-weak") {
		t.Error("a positive change should carry the weak-TWD class")
	}
	if !strings.Contains(stronger, "fx-chg fx-strong") {
		t.Error("a negative change should carry the strong-TWD class")
	}
}

// No archive, or an unusable one, must render nothing at all — never a zero rate.
func TestFXBarAbsentWithoutData(t *testing.T) {
	// Assert on the rendered ELEMENT, not the class name: the stylesheet also contains
	// ".fx-bar", and matching that would pass whether or not anything was drawn.
	const el = `<div class="fx-bar">`

	none := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{})
	if strings.Contains(none, el) || strings.Contains(none, "USD/TWD") {
		t.Error("the FX bar rendered with no metrics")
	}

	unusable := genHTML(t, nil, GuardrailViewOptions{
		FX: &fx.Metrics{Status: fx.StatusInsufficientData, Context: fx.ContextUnknown}})
	if strings.Contains(unusable, el) {
		t.Error("the FX bar rendered for an unusable reading")
	}
}

// A window the history could not cover prints an em dash, not a fabricated 0.00%.
func TestFXBarUnmeasurableWindow(t *testing.T) {
	m := fxMetrics(31.5, fx.ContextStable, 0.1, 0.2, 0)
	delete(m.ChangePct, 20) // 20d not measurable
	html := genHTML(t, nil, GuardrailViewOptions{FX: m})
	if !strings.Contains(html, "20D —") {
		t.Error("an unmeasurable window should print a dash, not a number")
	}
}

// THE regression guard. FX is market context: it may add a header line and change nothing
// else. Every stock row must be byte-identical with and without it.
func TestFXDoesNotChangeTheRestOfTheReport(t *testing.T) {
	entries := []scanner.WatchlistEntry{sampleEntry(true), sampleEntry(false)}
	market := []scanner.StockAnalysis{
		{Symbol: "2330", Name: "台積電", Source: "market", Close: 1000, Score: 77,
			Action: scanner.ActionBuy, PriceVolumeSignal: "價漲量增"},
	}

	withoutFX := genFull(t, market, entries, GuardrailViewOptions{})
	withFX := genFull(t, market, entries, GuardrailViewOptions{
		FX: fxMetrics(31.854, fx.ContextDepreciation, 0.12, 1.10, 2.40)})

	// Compare everything from the tab strip onward — that is where every stock row, score,
	// action and ordering lives. The header (one added line) and the stylesheet (the FX rules
	// are only emitted when the bar renders) are the two places the feature is ALLOWED to
	// differ, and both sit above this point.
	tabs := func(h string) string {
		i := strings.Index(h, `<div class="tabs">`)
		if i < 0 {
			t.Fatal("no tab strip in the rendered report")
		}
		return h[i:]
	}
	if tabs(withoutFX) != tabs(withFX) {
		t.Error("enabling the FX context changed a stock row — it may only add a header line")
	}
	// And the bar really was added, so the comparison above was not vacuous.
	if !strings.Contains(withFX, `<div class="fx-bar">`) {
		t.Fatal("the FX bar did not render, so this test proved nothing")
	}
}
