package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/macro"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// macF is a local pointer helper; the package already has one named f.
func macF(v float64) *float64 { return &v }

func macroResearch() *macro.Research {
	return &macro.Research{
		Status: macro.Available, ArchiveDate: "2026-09-01",
		Fed: macro.FedView{Status: macro.Available, Date: "2026-08-28", Freshness: macro.Fresh,
			EFFR: macF(3.63), TargetLower: macF(3.50), TargetUpper: macF(3.75),
			Direction: macro.FedEasing, LastChangeBps: f(-25)},
		Treasury: macro.TreasuryView{Status: macro.Available, Date: "2026-08-31",
			Freshness: macro.Fresh, Yield2Y: macF(4.34), Yield10Y: macF(4.75), Spread: macF(0.41),
			Curve: macro.CurveNormal},
		Inflation: macro.InflationView{Status: macro.Available, Date: "2026-07",
			Freshness: macro.Fresh, CPIYoY: macF(0.034), CoreCPIYoY: macF(0.025),
			Trend: macro.InflationDecelerating},
		Labor: macro.LaborView{Status: macro.Available, Date: "2026-07", Freshness: macro.Fresh,
			Unemployment: macF(4.1), PayrollChange: f(-23), State: macro.LaborMixed},
		Volatility: macro.VolatilityView{Status: macro.Available, Date: "2026-08-31",
			Freshness: macro.Fresh, VIX: macF(14.92), State: macro.VIXLow},
		PCEStatus: macro.NotImplemented, FOMCCalendarStatus: macro.NotImplemented,
	}
}

// The macro bar is market-wide: it renders once, in the header, never per stock.
func TestMacroBarRendersOnce(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)},
		GuardrailViewOptions{Macro: macroResearch()})

	if n := strings.Count(html, `<div class="macro-bar">`); n != 1 {
		t.Errorf("the macro bar rendered %d times, want 1", n)
	}
	for _, want := range []string{"3.50–3.75%", "EASING", "-25 bps", "4.34%", "4.75%",
		"NORMAL", "DECELERATING", "MIXED", "14.92", "LOW"} {
		if !strings.Contains(html, want) {
			t.Errorf("the macro bar is missing %q", want)
		}
	}
	// Each series shows its OWN observation date; a monthly figure must not appear to be a
	// September one just because the report is.
	if !strings.Contains(html, "2026-07") || !strings.Contains(html, "2026-08-31") {
		t.Error("observation dates are missing; the reader cannot see a monthly lag")
	}
	// The unusable sources are declared rather than omitted.
	if !strings.Contains(html, "NOT_IMPLEMENTED") {
		t.Error("PCE / FOMC are not marked NOT_IMPLEMENTED")
	}
	// And the page says the layer changes nothing.
	if !strings.Contains(html, "不影響任何評分") {
		t.Error("the macro bar does not say it is research-only")
	}
}

// Missing values must never render as a number.
func TestMacroMissingIsNotZero(t *testing.T) {
	r := &macro.Research{
		Status:     macro.Partial,
		Fed:        macro.FedView{Status: macro.Unavailable, Direction: macro.FedUnknown},
		Treasury:   macro.TreasuryView{Status: macro.Unavailable, Curve: macro.CurveUnknown},
		Inflation:  macro.InflationView{Status: macro.Unavailable, Trend: macro.InflationUnknown},
		Labor:      macro.LaborView{Status: macro.Unavailable, State: macro.LaborUnknown},
		Volatility: macro.VolatilityView{Status: macro.Unavailable, State: macro.VIXUnknown},
		PCEStatus:  macro.NotImplemented, FOMCCalendarStatus: macro.NotImplemented,
	}
	html := genHTML(t, nil, GuardrailViewOptions{Macro: r})

	if !strings.Contains(html, `<div class="macro-bar">`) {
		t.Fatal("a partial macro view removed the whole section")
	}
	if n := strings.Count(html, "UNAVAILABLE"); n < 5 {
		t.Errorf("only %d UNAVAILABLE markers; every missing value must say so", n)
	}
	// A zero Fed funds rate is a policy statement; a zero VIX is not a thing.
	for _, bad := range []string{"0.00%", "0.00 pp", "+0K", ">0.00<"} {
		if strings.Contains(html, bad) {
			t.Errorf("a missing value rendered as %q", bad)
		}
	}
}

// A stale reading keeps its value and gains a marker — hiding it would lose information.
func TestMacroStaleShowsValueAndMarker(t *testing.T) {
	r := macroResearch()
	r.Treasury.Freshness = macro.Stale
	r.Treasury.Date = "2026-08-01"
	html := genHTML(t, nil, GuardrailViewOptions{Macro: r})

	if !strings.Contains(html, "STALE") {
		t.Error("a stale reading is not marked")
	}
	if !strings.Contains(html, "4.75%") {
		t.Error("a stale reading's value was hidden; the reader needs both value and age")
	}
}

// Risk flags come from the domain. The template must not invent one.
func TestMacroRiskFlagsAreRenderedNotDerived(t *testing.T) {
	r := macroResearch()
	r.RiskFlags = []macro.RiskFlag{macro.FlagYieldCurveInverted, macro.FlagVIXElevated}
	html := genHTML(t, nil, GuardrailViewOptions{Macro: r})

	for _, want := range []string{"YIELD_CURVE_INVERTED", "VIX_ELEVATED"} {
		if !strings.Contains(html, want) {
			t.Errorf("flag %q missing", want)
		}
	}
	// A view with no flags must produce none — the template does not add its own.
	clean := genHTML(t, nil, GuardrailViewOptions{Macro: macroResearch()})
	for _, unwanted := range []string{"YIELD_CURVE_INVERTED", "RECESSION", "CRASH"} {
		if strings.Contains(clean, unwanted) {
			t.Errorf("the template produced %q from a clean view", unwanted)
		}
	}
}

// No forecast or recommendation vocabulary may reach the page.
func TestMacroPageMakesNoPrediction(t *testing.T) {
	html := genHTML(t, nil, GuardrailViewOptions{Macro: macroResearch()})
	for _, banned := range []string{"will cut", "will hike", "expected to", "probability",
		"recession", "Macro Score", "risk-on", "risk-off"} {
		if strings.Contains(html, banned) {
			t.Errorf("the page contains %q; this layer has no basis for it", banned)
		}
	}
}

// THE isolation guard: macro may add a header line and change nothing else.
func TestMacroDoesNotChangeTheRestOfTheReport(t *testing.T) {
	entries := []scanner.WatchlistEntry{sampleEntry(true), sampleEntry(false)}
	market := []scanner.StockAnalysis{
		{Symbol: "2330", Name: "台積電", Source: "market", Close: 1000, Score: 77,
			Action: scanner.ActionBuy, PriceVolumeSignal: "價漲量增"},
	}
	without := genFull(t, market, entries, GuardrailViewOptions{})
	with := genFull(t, market, entries, GuardrailViewOptions{Macro: macroResearch()})

	// Everything from the tab strip onward — where every stock row, score and ordering
	// lives. The header and stylesheet are the two places macro is allowed to differ.
	tabs := func(h string) string {
		i := strings.Index(h, `<div class="tabs">`)
		if i < 0 {
			t.Fatal("no tab strip")
		}
		return h[i:]
	}
	if tabs(without) != tabs(with) {
		t.Error("adding the macro context changed a stock row — it may only add a header line")
	}
	if !strings.Contains(with, `<div class="macro-bar">`) {
		t.Fatal("the macro bar did not render, so this test proved nothing")
	}
}
