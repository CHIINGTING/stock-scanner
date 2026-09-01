package report

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func fvP(v float64) *float64 { return &v }

// fundSection extracts the ⑰ block by BALANCED div counting.
//
// Scoping matters: the page has other sections that legitimately print "+0.0%" or a zero, and
// an assertion scanning the whole document would fail on one of those rather than on anything
// this section did.
func fundSection(html string) string {
	const open = `<div class="wl-sec wl-fund">`
	i := strings.Index(html, open)
	if i < 0 {
		return ""
	}
	depth, j := 0, i
	for j < len(html) {
		if strings.HasPrefix(html[j:], "<div") {
			depth++
			j += 4
			continue
		}
		if strings.HasPrefix(html[j:], "</div>") {
			depth--
			j += 6
			if depth == 0 {
				return html[i:j]
			}
			continue
		}
		j++
	}
	return html[i:]
}

// fundamentalView mirrors the shape internal/fundamental produces for a real stock.
func fundamentalView() *fundamental.View {
	eps := 49.33
	pub := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	v := &fundamental.View{
		Symbol: "2330.TW", Status: fundamental.Available, Source: fundamental.SourceName,
		RevenueStatus: fundamental.Available, FinancialStatus: fundamental.Available,
		Revenue: &fundamental.MonthlyRevenue{
			Period:  fundamental.Period{Year: 2026, Month: 7},
			Revenue: 467580548000, PriorMonth: 442679969000, PriorYearMonth: 323165707000,
			PublishedAt: pub,
		},
		Financials: &fundamental.Financials{
			Period:  fundamental.Period{Year: 2026, Quarter: 2, Cumulative: true},
			Revenue: 2404483690000, GrossProfit: 1611606116000,
			OperatingIncome: 1425568793000, CumulativeEPS: &eps, PublishedAt: pub,
		},
	}
	v.RevenueMetrics = fundamental.DeriveRevenue(v.Revenue)
	v.FinancialMetrics = fundamental.DeriveFinancials(v.Financials)
	return v
}

func valuationView() *valuation.Valuation {
	return &valuation.Valuation{
		Status: valuation.Partial, Symbol: "2330.TW", Source: valuation.SourceName,
		TrailingPE: fvP(27.88), TrailingDate: "2026-08-31",
		TrailingStatus: valuation.Available,
		PBRatio:        fvP(9.70), DividendYield: fvP(0.91),
		HistoricalPE: valuation.HistoricalPEStats{
			Status: valuation.InsufficientData, WindowDays: 1825, SampleCount: 1,
		},
		ForwardPEStatus: valuation.NotImplemented,
		PEGStatus:       valuation.NotImplemented,
		FairValueStatus: valuation.NotImplemented,
	}
}

func withResearch(f map[string]*fundamental.View, v map[string]*valuation.Valuation) GuardrailViewOptions {
	return GuardrailViewOptions{Fundamental: f, Valuation: v}
}

// The section renders the archived values, and labels the period as CUMULATIVE.
func TestFundamentalSectionRenders(t *testing.T) {
	e := sampleEntry(false)
	html := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(map[string]*fundamental.View{e.A.Symbol: fundamentalView()}, nil))

	if !strings.Contains(html, "⑰ 基本面／估值") {
		t.Fatal("the section did not render")
	}
	for _, want := range []string{"4676 億", "&#43;44.7%", "&#43;5.6%", "49.33",
		"&#43;67.0%", "&#43;59.3%", "2026-08-17"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// THE label guard. This source reports year-to-date; rendering a bare "Q2" would be read
	// as three months of activity when it is six, halving every conclusion drawn from it.
	if !strings.Contains(html, "累計") {
		t.Error("the cumulative marker is missing from the period label")
	}
	// The section states it changes nothing.
	if !strings.Contains(html, "不影響 BUY／WATCH／SELL") {
		t.Error("the section does not say it is research-only")
	}
}

func TestValuationSectionRenders(t *testing.T) {
	e := sampleEntry(false)
	html := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(nil, map[string]*valuation.Valuation{e.A.Symbol: valuationView()}))

	for _, want := range []string{"27.88", "9.70", "0.91%", "2026-08-31"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A thin archive says how thin, and how far it has to go — the exchanges publish no
	// historical P/E, so this fills one trading day at a time.
	if !strings.Contains(html, "INSUFFICIENT_DATA（1 筆，需 20）") {
		t.Errorf("the historical P/E status is not shown with its progress")
	}
}

// Missing must never render as a number. A P/E of zero would put a loss-making stock at the
// cheap end of any comparison; a revenue of zero reads as a collapse.
func TestMissingRendersStatusNotZero(t *testing.T) {
	e := sampleEntry(false)
	// A valuation where the exchange published no P/E at all.
	empty := &valuation.Valuation{
		Status: valuation.Partial, TrailingStatus: valuation.Unavailable,
		HistoricalPE:    valuation.HistoricalPEStats{Status: valuation.InsufficientData},
		ForwardPEStatus: valuation.NotImplemented, PEGStatus: valuation.NotImplemented,
		FairValueStatus: valuation.NotImplemented,
	}
	// A fundamental view with no records behind it.
	bare := &fundamental.View{Status: fundamental.Partial}
	bare.RevenueMetrics = fundamental.DeriveRevenue(nil)
	bare.FinancialMetrics = fundamental.DeriveFinancials(nil)

	html := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(map[string]*fundamental.View{e.A.Symbol: bare},
			map[string]*valuation.Valuation{e.A.Symbol: empty}))

	if !strings.Contains(html, "⑰ 基本面／估值") {
		t.Fatal("the section vanished instead of reporting what is missing")
	}
	sec := fundSection(html)
	if n := strings.Count(sec, "UNAVAILABLE"); n < 4 {
		t.Errorf("only %d UNAVAILABLE markers in the section: %s", n, sec)
	}
	// The specific renderings that would be read as data — checked inside the section only.
	for _, bad := range []string{">0.00<", "0 億", "&#43;0.0%", "0.00%"} {
		if strings.Contains(sec, bad) {
			t.Errorf("a missing value rendered as %q in: %s", bad, sec)
		}
	}
}

// A stock with nothing archived gets no section — that is different from an archived record
// whose fields are missing, which does render.
func TestNoArchiveNoSection(t *testing.T) {
	e := sampleEntry(false)
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{})
	if strings.Contains(html, "⑰ 基本面／估值") {
		t.Error("the section rendered for a stock with nothing archived")
	}
}

// Growth colour follows the sign, and an unavailable metric gets no colour at all — tinting
// it would imply a direction that was never measured.
func TestGrowthColourOnlyWhenMeasured(t *testing.T) {
	e := sampleEntry(false)
	down := fundamentalView()
	down.Revenue.PriorYearMonth = 900000000000 // a decline
	down.RevenueMetrics = fundamental.DeriveRevenue(down.Revenue)
	html := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(map[string]*fundamental.View{e.A.Symbol: down}, nil))
	if !strings.Contains(html, "fv-down") {
		t.Error("a revenue decline was not marked")
	}

	// A metric with no comparison base gets neither colour.
	none := fundamentalView()
	none.Revenue.PriorYearMonth = 0
	none.RevenueMetrics = fundamental.DeriveRevenue(none.Revenue)
	html2 := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(map[string]*fundamental.View{e.A.Symbol: none}, nil))
	i := strings.Index(html2, "YoY")
	if i < 0 {
		t.Fatal("no YoY line")
	}
	line := html2[i:]
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	if strings.Contains(line, "fv-up") || strings.Contains(line, "fv-down") {
		t.Errorf("an unmeasured growth figure was coloured: %s", line)
	}
}

// The page must never imply a forecast this repo has no source for.
func TestNoFabricatedForecast(t *testing.T) {
	e := sampleEntry(false)
	html := genHTML(t, []scanner.WatchlistEntry{e},
		withResearch(map[string]*fundamental.View{e.A.Symbol: fundamentalView()},
			map[string]*valuation.Valuation{e.A.Symbol: valuationView()}))

	// The unsourced measures are named with their status, so a reader learns there is
	// nothing to wait for rather than assuming a computation failed.
	if !strings.Contains(html, "NOT_IMPLEMENTED") {
		t.Error("the unsourced valuation measures are not marked")
	}
	for _, bad := range []string{"Forward EPS: 0", "Forward P/E: 0", "Fair Value: 0",
		"Target Price", "Analyst Consensus", "2027E", "合理價 0"} {
		if strings.Contains(html, bad) {
			t.Errorf("the page contains %q, which nothing can populate", bad)
		}
	}
}

// THE isolation guard: this section may be added and nothing else may move.
func TestResearchSectionDoesNotChangeTheRest(t *testing.T) {
	entries := []scanner.WatchlistEntry{sampleEntry(true), sampleEntry(false)}
	market := []scanner.StockAnalysis{
		{Symbol: "2330", Name: "台積電", Source: "market", Close: 1000, Score: 77,
			Action: scanner.ActionBuy, PriceVolumeSignal: "價漲量增"},
	}
	without := genFull(t, market, entries, GuardrailViewOptions{})
	with := genFull(t, market, entries, withResearch(
		map[string]*fundamental.View{entries[0].A.Symbol: fundamentalView()},
		map[string]*valuation.Valuation{entries[0].A.Symbol: valuationView()}))

	if !strings.Contains(with, "⑰ 基本面／估值") {
		t.Fatal("the section did not render, so this test proved nothing")
	}
	// Strip the added section; the rest of the page must be byte-identical.
	strip := func(h string) string {
		for {
			sec := fundSection(h)
			if sec == "" {
				return h
			}
			h = strings.Replace(h, sec, "", 1)
		}
	}
	// Compare from the tab strip onward: every stock row, score, action and ordering.
	tabs := func(h string) string {
		i := strings.Index(h, `<div class="tabs">`)
		if i < 0 {
			t.Fatal("no tab strip")
		}
		return h[i:]
	}
	// Whitespace-only lines are dropped from both before comparing: removing a block from
	// the template leaves its indentation behind, and this test is about whether any stock
	// row, score, action or ordering moved — not about blank lines. Every line with content
	// still has to match exactly.
	meaningful := func(h string) []string {
		var out []string
		for _, ln := range strings.Split(h, "\n") {
			if strings.TrimSpace(ln) != "" {
				out = append(out, ln)
			}
		}
		return out
	}
	a, b := meaningful(tabs(strip(without))), meaningful(tabs(strip(with)))
	if len(a) != len(b) {
		t.Fatalf("the page gained or lost %d content lines outside the section", len(b)-len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("line %d changed outside the section:\n without: %.120q\n with:    %.120q",
				i, a[i], b[i])
		}
	}
}
