package candidate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func f64(v float64) *float64 { return &v }

func valuationFixture(status valuation.Availability) *valuation.Valuation {
	return &valuation.Valuation{
		Status: status, Symbol: "2330.TW", Source: valuation.SourceName,
		TrailingPE: f64(28.05), TrailingDate: "2026-08-28", TrailingStatus: valuation.Available,
		PBRatio: f64(9.76), DividendYield: f64(0.91),
		HistoricalPE: valuation.HistoricalPEStats{
			Status: valuation.Available, WindowDays: 1825, SampleCount: 240,
			Median: f64(22.5), P25: f64(18.0), P75: f64(27.0), CurrentPercentile: f64(0.82),
		},
		ForwardPEStatus: valuation.NotImplemented,
		PEGStatus:       valuation.NotImplemented,
		FairValueStatus: valuation.NotImplemented,
	}
}

// The view projects. Every number arrives computed; the presentation layer only formats.
func TestValuationViewProjectsOnly(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Valuation = valuationFixture(valuation.Available)
	v := BuildViews([]Evidence{e})[0].Valuation

	if v.TrailingPE != "28.05" || v.TrailingDate != "2026-08-28" {
		t.Errorf("trailing = %q @ %q", v.TrailingPE, v.TrailingDate)
	}
	if v.MedianPE != "22.50" || v.P25PE != "18.00" || v.P75PE != "27.00" {
		t.Errorf("distribution = %q / %q / %q", v.MedianPE, v.P25PE, v.P75PE)
	}
	// The domain stores a ratio; display converts to a percentage. Both conventions must not
	// coexist in one place.
	if v.Percentile != "82%" {
		t.Errorf("percentile = %q, want 82%%", v.Percentile)
	}
	// Sample count and window travel with the statistics: a median over 4 points and one over
	// 400 are different claims, and a reader who cannot see which has no way to tell.
	if v.SampleCount != 240 || v.WindowDays != 1825 {
		t.Errorf("provenance lost: %d samples / %dd", v.SampleCount, v.WindowDays)
	}
}

// THE scope guard. Metrics with no source must render their STATUS, so a reader learns there
// is nothing to wait for rather than assuming the computation failed.
func TestUnsourcedMetricsRenderAsNotImplemented(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Valuation = valuationFixture(valuation.Available)
	v := BuildViews([]Evidence{e})[0].Valuation

	for name, got := range map[string]string{
		"forward PE": v.ForwardPE, "PEG": v.PEG, "fair value": v.FairValue,
	} {
		if got != "NOT_IMPLEMENTED" {
			t.Errorf("%s = %q, want NOT_IMPLEMENTED", name, got)
		}
		// Above all, never a number.
		for _, bad := range []string{"0", "0.00", "-", "N/A", ""} {
			if got == bad {
				t.Errorf("%s rendered as %q — that reads as a computed value", name, bad)
			}
		}
	}
}

// A thin archive says so, and the trailing figure survives alongside it.
func TestInsufficientHistoryDoesNotHideTrailingPE(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	val := valuationFixture(valuation.Partial)
	val.HistoricalPE = valuation.HistoricalPEStats{
		Status: valuation.InsufficientData, WindowDays: 1825, SampleCount: 3,
	}
	e.Valuation = val
	v := BuildViews([]Evidence{e})[0].Valuation

	if v.HistoryStatus != Availability(valuation.InsufficientData) {
		t.Errorf("history status = %q", v.HistoryStatus)
	}
	if v.MedianPE != "UNAVAILABLE" || v.Percentile != "UNAVAILABLE" {
		t.Errorf("statistics were produced from 3 samples: %q / %q", v.MedianPE, v.Percentile)
	}
	// INSUFFICIENT_DATA is "wait"; UNAVAILABLE is "there is nothing to wait for". They must
	// stay distinct.
	if valuation.InsufficientData == valuation.Unavailable {
		t.Fatal("the two states collide")
	}
	// The trailing figure is unaffected.
	if v.TrailingPE != "28.05" {
		t.Errorf("trailing PE was lost: %q", v.TrailingPE)
	}
}

func TestValuationDisabledIsNotUnavailable(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Valuation = nil
	off := BuildViews([]Evidence{e})[0].Valuation
	if off.Status != Disabled {
		t.Errorf("layer off → %q", off.Status)
	}
	e.Valuation = &valuation.Valuation{Status: valuation.Unavailable}
	on := BuildViews([]Evidence{e})[0].Valuation
	if off.Status == on.Status {
		t.Fatal("a switched-off layer and a missing archive produced the same status")
	}
}

// The rendered output must never imply a forecast this repo does not have.
func TestRenderedValuationClaimsNoForecast(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Valuation = valuationFixture(valuation.Available)
	var buf bytes.Buffer
	RenderText(&buf, BuildViews([]Evidence{e}))
	out := buf.String()

	if !strings.Contains(out, "Valuation") || !strings.Contains(out, "Trailing P/E:") {
		t.Fatal("the valuation section did not render")
	}
	if !strings.Contains(out, "NOT_IMPLEMENTED") {
		t.Error("the unsourced metrics are not marked")
	}
	// No forecast or recommendation vocabulary anywhere.
	for _, banned := range []string{"Forward EPS", "Consensus", "Target Price", "2027E",
		"Estimate", "undervalued", "overvalued", "CHEAP", "EXPENSIVE"} {
		if strings.Contains(out, banned) {
			t.Errorf("the output contains %q, which this layer has no basis for", banned)
		}
	}
}

// THE scanner invariance guard: valuation is evidence, never a decision input.
func TestValuationDoesNotChangeScannerFields(t *testing.T) {
	base := okEvidence("2330.TW", "2330", "", 71, scanner.ActPrepare)
	snap := func(e Evidence) [5]string {
		v := BuildViews([]Evidence{e})[0]
		return [5]string{v.Action, v.Stage, v.Price, v.ExplosionProb, string(rune(v.RocketScore))}
	}

	none := base
	none.Valuation = nil
	cheap := base
	c := valuationFixture(valuation.Available)
	c.TrailingPE = f64(5)
	c.HistoricalPE.CurrentPercentile = f64(0.02)
	cheap.Valuation = c
	rich := base
	r := valuationFixture(valuation.Available)
	r.TrailingPE = f64(200)
	r.HistoricalPE.CurrentPercentile = f64(0.99)
	rich.Valuation = r

	a, b, d := snap(none), snap(cheap), snap(rich)
	if a != b || b != d {
		t.Errorf("valuation changed the scanner fields:\n none=%v\n cheap=%v\n rich=%v", a, b, d)
	}
}

// Valuation availability must not reorder the universe.
func TestValuationDoesNotReorderCandidates(t *testing.T) {
	a := okEvidence("2327.TW", "2327", "", 40, scanner.ActWait)
	a.Valuation = nil
	b := okEvidence("2330.TW", "2330", "", 90, scanner.ActWait)
	b.Valuation = valuationFixture(valuation.Available)
	c := okEvidence("6488.TWO", "6488", "", 50, scanner.ActWait)
	c.Valuation = &valuation.Valuation{Status: valuation.Unavailable}

	views := BuildViews([]Evidence{a, b, c})
	for i, w := range []string{"2327.TW", "2330.TW", "6488.TWO"} {
		if views[i].Symbol != w {
			t.Fatalf("position %d = %s, want %s — valuation reordered the universe",
				i, views[i].Symbol, w)
		}
	}
}
