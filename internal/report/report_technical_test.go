package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/technical"
)

func techEntry(r *technical.Result) scanner.WatchlistEntry {
	e := sampleEntry(false)
	e.Technical = r
	return e
}

// fullTechResult is what the scanner attaches when every indicator published.
func fullTechResult() *technical.Result {
	r := &technical.Result{
		Config: technical.DefaultConfig(),
		ADX: &technical.ADXResult{
			Status: technical.StatusOK, Period: 14, ADX: 31.8, PlusDI: 30.2, MinusDI: 14.6,
			Direction: technical.DirBullish, Strength: technical.StrengthStrong,
		},
		RSI: &technical.RSIResult{
			Status: technical.StatusOK, Period: 14, Value: 61.3,
			Zone: technical.RSIBullish, Rising: true,
		},
		MACD: &technical.MACDResult{
			Status: technical.StatusOK, Fast: 12, Slow: 26, Signal: 9,
			MACD: 2.11, SignalVal: 1.74, Histogram: 0.37,
			Cross: technical.CrossNone, Momentum: technical.MomentumAccelerating,
		},
		Keltner: &technical.KeltnerResult{
			Status: technical.StatusOK, Middle: 1200, Upper: 1280, Lower: 1120, ATR: 40,
			Position: technical.PosAboveUpper, Channel: technical.ChannelBreakout, WidthPct: 13.3,
		},
		Pivot: &technical.PivotResult{
			Status: technical.StatusOK, BasisDate: "2026-08-19",
			Pivot: 1190, R1: 1210, R2: 1240, R3: 1275, S1: 1160, S2: 1140, S3: 1105,
			NearestLevel: "R1", DistancePct: 0.83, Location: technical.LocNearResistance,
		},
	}
	r.Context = technical.BuildContext(*r)
	return r
}

// Default off means the section, its styles and its helpers contribute nothing at all.
func TestTechnicalSectionAbsentByDefault(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{techEntry(fullTechResult())}, GuardrailViewOptions{})
	for _, marker := range []string{"⑯ 技術指標", "wl-tech", "tech-chip", "tech-raw"} {
		if strings.Contains(html, marker) {
			t.Errorf("%q rendered with ShowTechnicalIndicators off", marker)
		}
	}
}

func TestTechnicalSectionRenders(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{techEntry(fullTechResult())},
		GuardrailViewOptions{ShowTechnicalIndicators: true})

	// The summary comes first and must be present.
	for _, want := range []string{
		"⑯ 技術指標", "方向", "趨勢強度", "動能", "波動", "位置",
		technical.DirBullish, technical.StrengthStrong, technical.MomentumAccelerating,
		technical.VolBreakout, technical.LocNearResistance,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the summary is missing %q", want)
		}
	}
	// Raw values are secondary but must still be there.
	for _, want := range []string{"ADX(14)", "31.8", "30.2 / 14.6", "RSI(14)", "61.3",
		"2.11 / 1.74", "Keltner", "1280.00", "R1", "2026-08-19"} {
		if !strings.Contains(html, want) {
			t.Errorf("raw value %q is missing", want)
		}
	}
	// The section must say what it is NOT.
	if !strings.Contains(html, "指標是證據，不是決策") {
		t.Error("the section does not state that it is evidence rather than a decision")
	}
	// And it must never render an instruction.
	for _, forbidden := range []string{"BUY_NOW", "SELL_NOW", "STRONG_BUY", "STRONG_SELL"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("the technical section rendered the instruction %q", forbidden)
		}
	}
}

// A non-OK indicator must print its STATUS, never a row of zeros that reads as a measurement.
func TestTechnicalNonOKIndicatorShowsItsStatus(t *testing.T) {
	r := fullTechResult()
	r.MACD = &technical.MACDResult{Status: technical.StatusInsufficientData, Cross: technical.CrossNone}
	r.Pivot = &technical.PivotResult{Status: technical.StatusNoData}
	r.Context = technical.BuildContext(*r)

	html := genHTML(t, []scanner.WatchlistEntry{techEntry(r)},
		GuardrailViewOptions{ShowTechnicalIndicators: true})

	if !strings.Contains(html, "MACD — INSUFFICIENT_DATA") {
		t.Error("an unavailable MACD should print its status")
	}
	if !strings.Contains(html, "Pivot — NO_DATA") {
		t.Error("an unavailable pivot should print its status")
	}
	if strings.Contains(html, "MACD / Signal") {
		t.Error("an unavailable MACD rendered its value row")
	}
	// ADX still published, so it must still be shown.
	if !strings.Contains(html, "ADX(14)") {
		t.Error("one unavailable indicator must not suppress the others")
	}
}

// The R11 bug in this file was AI styles emitted inside an unrelated flag's block, so the
// section was styled only when something else happened to be on. This asserts R14's styles
// follow R14's flag and nothing else.
func TestTechnicalStylesFollowTheirOwnFlagOnly(t *testing.T) {
	entries := []scanner.WatchlistEntry{techEntry(fullTechResult())}

	on := genHTML(t, entries, GuardrailViewOptions{ShowTechnicalIndicators: true})
	if !strings.Contains(on, ".wl-tech .tech-chip") {
		t.Error("the R14 styles are missing when its own flag is on")
	}

	for name, gv := range map[string]GuardrailViewOptions{
		"trend extension": {ShowTrendExtension: true},
		"ai":              {ShowAI: true},
		"candlestick":     {ShowCandlestick: true},
		"backtest":        {ShowBacktestInsights: true},
	} {
		if html := genHTML(t, entries, gv); strings.Contains(html, ".wl-tech .tech-chip") {
			t.Errorf("R14 styles were emitted by the unrelated %s flag", name)
		}
	}
}

// Nothing non-finite may reach the HTML. The technical package guards its own arithmetic;
// this checks the rendered output as the last line of defence.
func TestTechnicalHTMLCarriesNoNaNOrInf(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{techEntry(fullTechResult())},
		GuardrailViewOptions{ShowTechnicalIndicators: true})
	for _, bad := range []string{"NaN", "+Inf", "-Inf"} {
		if strings.Contains(html, bad) {
			t.Errorf("the report contains %q", bad)
		}
	}
}

// A nil Technical (feature off at scan time) must not render the section even when the
// display flag is on — there is nothing to show, and an empty shell would imply otherwise.
func TestTechnicalNilResultRendersNothing(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{techEntry(nil)},
		GuardrailViewOptions{ShowTechnicalIndicators: true})
	if strings.Contains(html, "⑯ 技術指標") {
		t.Error("the section rendered for an entry with no technical result")
	}
}
