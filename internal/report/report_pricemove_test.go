package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// genMarketHTML renders through the MARKET tab, which is where the price-volume cell lives
// (the watchlist card summarises differently).
func genMarketHTML(t *testing.T, rows []scanner.StockAnalysis) string {
	t.Helper()
	dir := t.TempDir()
	r := New(Config{OutputDir: dir})
	date := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	if err := r.Generate(rows, nil, nil, nil, "-", date, GuardrailViewOptions{}, nil, nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report_20260605.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(b)
}

func pmRow(sig, move string, chg, atr float64) scanner.StockAnalysis {
	return scanner.StockAnalysis{
		Symbol: "2330", Name: "測試", Source: "market", Close: 100,
		PriceVolumeSignal: sig, PriceMove: move,
		PriceChangePct: chg, PriceChangeATR: atr,
	}
}

// magnitudeSpan extracts the rendered magnitude span, so assertions cannot be satisfied by
// the stylesheet merely DEFINING a class.
func magnitudeSpan(html string) string {
	i := strings.Index(html, `<span class="pv-mag `)
	if i < 0 {
		return ""
	}
	j := strings.Index(html[i:], "</span>")
	if j < 0 {
		return ""
	}
	return html[i : i+j]
}

// The whole point, at the surface a human reads: a −0.35% drift and a −6.2% break must no
// longer look identical.
func TestReportShowsMagnitudeBesideTheSignal(t *testing.T) {
	small := magnitudeSpan(genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價跌量縮", scanner.MoveFlat, -0.35, -0.2)}))
	large := magnitudeSpan(genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價跌量縮", scanner.MoveLargeDown, -6.2, -2.4)}))

	if !strings.Contains(small, "-0.35%") {
		t.Errorf("the small move's percentage is missing: %q", small)
	}
	if !strings.Contains(large, "-6.20%") {
		t.Errorf("the large move's percentage is missing: %q", large)
	}
	// They are visually distinguishable.
	if !strings.Contains(large, "pv-mag-large") {
		t.Errorf("a large move is not marked as large: %q", large)
	}
	if strings.Contains(small, "pv-mag-large") {
		t.Errorf("a flat move was marked as large: %q", small)
	}
	// The ATR context travels with it.
	if !strings.Contains(large, "-2.4 ATR") {
		t.Errorf("the ATR multiple is missing: %q", large)
	}
	// And the legacy label was NOT replaced.
	full := genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價跌量縮", scanner.MoveLargeDown, -6.2, -2.4)})
	if !strings.Contains(full, "價跌量縮") {
		t.Error("the legacy signal was dropped from the report")
	}
}

// An unmeasurable session must render NOTHING rather than a confident 0.00%.
func TestReportOmitsMagnitudeWhenUnknown(t *testing.T) {
	html := genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價跌量縮", scanner.MoveUnknown, 0, 0)})
	if span := magnitudeSpan(html); span != "" {
		t.Errorf("a session with no previous close rendered %q", span)
	}
	if !strings.Contains(html, "價跌量縮") {
		t.Error("the legacy signal should still render")
	}
}

// A real 0.0% session IS measurable and must show — distinguishing it from "we could not look".
func TestReportShowsGenuineFlatSession(t *testing.T) {
	span := magnitudeSpan(genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價跌量縮", scanner.MoveFlat, 0, 0)}))
	if !strings.Contains(span, "0.00%") {
		t.Errorf("a genuine unchanged session should render its 0.00%%: %q", span)
	}
	// …and WITHOUT a sign. An exactly unchanged session is neither a gain nor a loss, and
	// "+0.00%" beside the legacy "價跌量縮" was a line that contradicted itself twice over.
	// ("&#43;" is how html/template escapes a literal "+".)
	if strings.Contains(span, "&#43;") || strings.Contains(span, "-0.00") {
		t.Errorf("a zero change was rendered with a sign: %q", span)
	}
	if !strings.Contains(span, "pv-mag-flat") {
		t.Errorf("a flat session should be marked flat: %q", span)
	}
	// No ATR context when the ATR was unavailable — not "0.0 ATR".
	if strings.Contains(span, "ATR") {
		t.Errorf("an unavailable ATR was rendered: %q", span)
	}
}

// Nothing non-finite may reach the HTML.
func TestReportMagnitudeCarriesNoNaN(t *testing.T) {
	html := genMarketHTML(t, []scanner.StockAnalysis{
		pmRow("價漲量增", scanner.MoveLargeUp, 7.5, 3.1)})
	for _, bad := range []string{"NaN", "+Inf", "-Inf"} {
		if strings.Contains(html, bad) {
			t.Errorf("the report contains %q", bad)
		}
	}
}

// The watchlist card is the tab with the most stocks in it. Before this, the magnitude
// existed in the store for every one of them and was visible for none — the price-volume
// cell only lives in the portfolio and market tables.
func TestWatchlistCardShowsTodaysMove(t *testing.T) {
	e := sampleEntry(false)
	e.A.Close = 605
	e.A.PriceVolumeSignal = "價跌量縮"
	e.A.PriceMove = scanner.MoveLargeDown
	e.A.PriceChangePct = -5.91
	e.A.PriceChangeATR = -0.59
	e.A.VolumeRatio = 0.62

	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{})
	span := magnitudeSpan(html)
	if !strings.Contains(span, "-5.91%") {
		t.Errorf("the day's move is missing from the watchlist card: %q", span)
	}
	if !strings.Contains(span, "-0.6 ATR") {
		t.Errorf("the ATR context is missing: %q", span)
	}
	if !strings.Contains(span, "pv-mag-large") {
		t.Errorf("a large move is not marked large: %q", span)
	}
	// The volume side of the pairing travels with it — a magnitude without its volume is
	// only half the signal this whole change is about.
	if !strings.Contains(html, "量比") || !strings.Contains(html, "價跌量縮") {
		t.Error("the volume ratio / legacy signal are missing from the card")
	}
}

// An unmeasurable session shows the price and nothing else — never a fabricated 0.00%.
func TestWatchlistCardOmitsUnknownMove(t *testing.T) {
	e := sampleEntry(false)
	e.A.PriceMove = scanner.MoveUnknown
	e.A.PriceVolumeSignal = ""
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{})
	if span := magnitudeSpan(html); span != "" {
		t.Errorf("rendered %q for a session with no previous close", span)
	}
	if !strings.Contains(html, "現價") {
		t.Error("the price line itself went missing")
	}
}
