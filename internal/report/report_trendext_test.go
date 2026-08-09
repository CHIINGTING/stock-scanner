package report

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func f(v float64) *float64 { return &v }

func teEntry(v *scanner.TrendExtensionView) scanner.WatchlistEntry {
	e := sampleEntry(false)
	e.TrendExt = v
	return e
}

func okHeat() *scanner.SectorHeatView {
	return &scanner.SectorHeatView{
		Status: scanner.SectorHeatOK, Heat: 78,
		Breadth: 72, RelativeMomentum: 83, Participation: 75, VolumeConfirmation: 81,
		MemberCount: 14, ValidMemberCount: 12,
		Codes: []string{scanner.CodeSectorHeatStrong, scanner.CodeSectorVolumeConfirm},
	}
}

func confirmedView() *scanner.TrendExtensionView {
	return &scanner.TrendExtensionView{
		State:       scanner.TrendConfirmed,
		MA20Slope5D: f(0.42), MA60Slope10D: f(0.18),
		Bias20: f(5.31), Bias60: f(9.02), Bias20Percentile: f(64),
		Sector: "半導體", Heat: okHeat(),
		// Production merges the sector's own codes into the view (see trendCodes), so the
		// fixture carries them too — otherwise this would test a shape that never occurs.
		Codes: []string{
			scanner.CodeMA20SlopeUp, scanner.CodeMA60SlopeUp, scanner.CodeSlopeAligned,
			scanner.CodeSectorHeatStrong, scanner.CodeSectorVolumeConfirm,
		},
	}
}

func TestTrendExtSectionRenders(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{teEntry(confirmedView())},
		GuardrailViewOptions{ShowTrendExtension: true})

	for _, want := range []string{
		"⑮ 趨勢／乖離／族群熱度", "TREND_CONFIRMED",
		// html/template escapes '+' as &#43;, so the sign is asserted in that form.
		"&#43;0.420% / 日", "&#43;0.180% / 日", // slope, % per day
		"&#43;5.31%", "&#43;9.02%", "64 分位", // BIAS and its own-history rank
		"78 / 100", "72", "83", "75", "81", // heat and its four components
		"12 / 14",                                                 // member disclosure
		"MA20_SLOPE_UP", "SLOPE_ALIGNED_UP", "SECTOR_HEAT_STRONG", // evidence codes
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered report is missing %q", want)
		}
	}
}

// EXTENDED must read as a warning, never as a stronger buy. The wording and the colour band
// both carry that, and both come from the analyzer's state — the renderer decides neither.
func TestTrendExtExtendedRendersAsWarning(t *testing.T) {
	v := confirmedView()
	v.State = scanner.TrendExtended
	v.Bias20, v.Bias20Percentile = f(19.4), f(97)

	html := genHTML(t, []scanner.WatchlistEntry{teEntry(v)},
		GuardrailViewOptions{ShowTrendExtension: true})

	if !strings.Contains(html, "EXTENDED") {
		t.Error("the state code must be visible")
	}
	if !strings.Contains(html, "不要追") || !strings.Contains(html, "Do not chase") {
		t.Error("EXTENDED must be worded as a do-not-chase warning")
	}
	// The distribution is asymmetric — poor median and deep drawdown, but a positive right
	// tail. So it is an entry-risk warning, never a sell or a bearish call.
	if !strings.Contains(html, "非賣出或看空訊號") {
		t.Error("EXTENDED must state that it is not a sell/bearish signal")
	}
	for _, forbidden := range []string{"必跌", "預期下跌", "空頭反轉", "賣出訊號"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("EXTENDED must not be worded as %q", forbidden)
		}
	}
	if !strings.Contains(html, "te-state te-warn") {
		t.Error("EXTENDED must use the warning colour band, not the confirmation one")
	}
	if strings.Contains(html, "te-state te-good") {
		t.Error("EXTENDED must never render with the good band")
	}
}

// Missing sector data must render as INSUFFICIENT_DATA with its denominators, never as 0.
func TestTrendExtInsufficientSectorNotZero(t *testing.T) {
	v := confirmedView()
	v.Heat = &scanner.SectorHeatView{
		Status: scanner.SectorHeatInsufficientData, MemberCount: 3, ValidMemberCount: 2,
	}
	html := genHTML(t, []scanner.WatchlistEntry{teEntry(v)},
		GuardrailViewOptions{ShowTrendExtension: true})

	if !strings.Contains(html, "INSUFFICIENT_DATA") {
		t.Error("a thin sector must say INSUFFICIENT_DATA")
	}
	if !strings.Contains(html, "2 / 3") {
		t.Error("the member denominators must still be shown")
	}
	if strings.Contains(html, "0 / 100") {
		t.Error("missing heat must never render as 0/100")
	}
	if strings.Contains(html, "族群熱度</span> <b>") {
		t.Error("no heat value may be rendered when the sector is unmeasurable")
	}
}

// Unavailable numbers render as an em dash, not as a confident 0.00%.
func TestTrendExtMissingValuesRenderAsDash(t *testing.T) {
	v := &scanner.TrendExtensionView{
		State: scanner.TrendExtInsufficient, // no slopes, no BIAS, no heat
	}
	html := genHTML(t, []scanner.WatchlistEntry{teEntry(v)},
		GuardrailViewOptions{ShowTrendExtension: true})

	if !strings.Contains(html, "INSUFFICIENT_DATA") {
		t.Error("the state must be shown even when every metric is missing")
	}
	if strings.Contains(html, "0.000% / 日") || strings.Contains(html, "0.00%") {
		t.Error("a missing slope/BIAS must not render as zero")
	}
	if !strings.Contains(html, "—") {
		t.Error("missing values should render as an em dash")
	}
	if !strings.Contains(html, "</html>") {
		t.Error("a fully-empty reading must not break the page")
	}
}

// The styles must follow ShowTrendExtension and nothing else.
func TestTrendExtStylesFollowOwnFlag(t *testing.T) {
	entries := []scanner.WatchlistEntry{teEntry(confirmedView())}

	on := genHTML(t, entries, GuardrailViewOptions{ShowTrendExtension: true})
	if !strings.Contains(on, ".wl-te .te-state") {
		t.Error("ShowTrendExtension=true must emit the ⑮ styles")
	}
	off := genHTML(t, entries, GuardrailViewOptions{ShowTrendExtension: false, ShowBacktestInsights: true, ShowAI: true})
	if strings.Contains(off, ".wl-te") {
		t.Error("ShowTrendExtension=false must emit no ⑮ styles regardless of other flags")
	}
}

// PULLBACK_IN_UPTREND is descriptive only. Validation did not support it as a superior
// entry, so the report must not promote it.
func TestPullbackIsNotPromoted(t *testing.T) {
	v := confirmedView()
	v.State = scanner.PullbackInUptrend
	html := genHTML(t, []scanner.WatchlistEntry{teEntry(v)},
		GuardrailViewOptions{ShowTrendExtension: true})

	if !strings.Contains(html, "非較佳進場") {
		t.Error("the label must say it is not a better entry")
	}
	for _, forbidden := range []string{"較佳進場點", "加碼", "逢低買進", "高勝率", "最佳買點"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("PULLBACK_IN_UPTREND must not be worded as %q", forbidden)
		}
	}
}

func TestTrendExtHiddenWhenShowFalse(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{teEntry(confirmedView())},
		GuardrailViewOptions{ShowTrendExtension: false})

	for _, absent := range []string{"⑮ 趨勢", "TREND_CONFIRMED", "MA20_SLOPE_UP", "78 / 100"} {
		if strings.Contains(html, absent) {
			t.Errorf("R12 content leaked with the flag off: %q", absent)
		}
	}
}

// The default report — what an operator who never opted in sees — must be byte-identical.
func TestTrendExtDefaultOutputUnchanged(t *testing.T) {
	without := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{})
	with := genHTML(t, []scanner.WatchlistEntry{teEntry(confirmedView())}, GuardrailViewOptions{})

	if without != with {
		t.Error("attaching a TrendExt reading changed the default report; it must be inert until show_trend_extension is on")
	}
}

// Every state must render with a non-empty label and a colour band — a state the analyzer can
// emit but the report cannot describe would surface as a blank chip.
func TestEveryTrendStateRenders(t *testing.T) {
	states := []scanner.TrendExtState{
		scanner.TrendConfirmed, scanner.PullbackInUptrend, scanner.TrendExtended,
		scanner.SectorConfirmed, scanner.SectorDivergence, scanner.TrendWeakening,
		scanner.TrendExtNeutral, scanner.TrendExtInsufficient,
	}
	for _, s := range states {
		text := trendStateText(s)
		if text == "" || text == string(s) {
			t.Errorf("state %s has no display label", s)
		}
		if !strings.HasPrefix(text, string(s)) {
			t.Errorf("state %s label should lead with the code, got %q", s, text)
		}
		if css := trendStateCSS(s); css == "" {
			t.Errorf("state %s has no colour band", s)
		}
	}
	// PULLBACK_IN_UPTREND must NOT share the constructive band with TREND_CONFIRMED: the
	// validation put them 15 points apart on 20d win rate, so equal colouring would be a
	// high-probability-setup claim the data does not support.
	if got := trendStateCSS(scanner.PullbackInUptrend); got != "te-flat" {
		t.Errorf("PULLBACK_IN_UPTREND renders as %q, want the neutral band", got)
	}
	if trendStateCSS(scanner.TrendConfirmed) == trendStateCSS(scanner.PullbackInUptrend) {
		t.Error("TREND_CONFIRMED and PULLBACK_IN_UPTREND must not look equally good")
	}
	for _, warn := range []scanner.TrendExtState{
		scanner.TrendExtended, scanner.TrendWeakening, scanner.SectorDivergence,
	} {
		if trendStateCSS(warn) != "te-warn" {
			t.Errorf("%s should render in the warning band", warn)
		}
	}
}
