package candidate

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func fundView(status fundamental.Availability) *fundamental.View {
	eps := 49.33
	pub := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	v := &fundamental.View{
		Symbol: "2330.TW", Status: status, Source: fundamental.SourceName,
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

// The view projects; it must not recompute. Every number here was derived by the fundamental
// service, and a second computation in the presentation layer would be a fork that agrees
// until it does not.
func TestFundamentalViewProjectsOnly(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Fundamental = fundView(fundamental.Available)
	v := BuildViews([]Evidence{e})[0].Fundamental

	if v.Status != Availability(fundamental.Available) {
		t.Errorf("status = %q", v.Status)
	}
	// 467,580,548 vs 323,165,707 → +44.7%. The percentage is a display conversion of the
	// ratio the service already computed, not a fresh division of the raw revenues.
	if v.RevenueYoY != "+44.7%" {
		t.Errorf("revenue YoY = %q, want +44.7%%", v.RevenueYoY)
	}
	if v.GrossMargin != "+67.0%" {
		t.Errorf("gross margin = %q", v.GrossMargin)
	}
	if v.CumulativeEPS != "49.33" {
		t.Errorf("EPS = %q", v.CumulativeEPS)
	}

	// THE label guard. This source reports year-to-date; a period rendered as a bare "Q2"
	// would be read as three months of activity when it is six.
	if !strings.Contains(v.Period, "累計") {
		t.Errorf("period = %q — the cumulative marker is missing and a reader would halve "+
			"every conclusion", v.Period)
	}
	// Publication date is carried through: it is what makes point-in-time auditable.
	if v.PublishedAt != "2026-08-17" {
		t.Errorf("published = %q", v.PublishedAt)
	}
}

// Missing must never render as a number. "0.00%" is indistinguishable from a real zero.
func TestFundamentalMissingIsNotZero(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Fundamental = &fundamental.View{
		Symbol: "2330.TW", Status: fundamental.Unavailable,
		Reason:           "no fundamental snapshot archived on or before 2026-01-01",
		RevenueMetrics:   fundamental.DeriveRevenue(nil),
		FinancialMetrics: fundamental.DeriveFinancials(nil),
	}
	v := BuildViews([]Evidence{e})[0].Fundamental

	for name, got := range map[string]string{
		"revenue": v.Revenue, "yoy": v.RevenueYoY, "mom": v.RevenueMoM,
		"eps": v.CumulativeEPS, "gross margin": v.GrossMargin, "op margin": v.OperatingMargin,
	} {
		if got != "UNAVAILABLE" {
			t.Errorf("%s rendered as %q with no data; want UNAVAILABLE", name, got)
		}
		for _, bad := range []string{"0", "0.0", "0.00%", "+0.0%", "-"} {
			if got == bad {
				t.Errorf("%s rendered as %q — that reads as a real value", name, bad)
			}
		}
	}

	// A negative-base growth must say so rather than print a misleading percentage.
	nm := &fundamental.View{Status: fundamental.Available}
	nm.RevenueMetrics = fundamental.DeriveRevenue(&fundamental.MonthlyRevenue{
		Revenue: 100, PriorYearMonth: -50})
	e.Fundamental = nm
	got := BuildViews([]Evidence{e})[0].Fundamental.RevenueYoY
	if got != "NOT_MEANINGFUL" {
		t.Errorf("a negative comparison base rendered as %q", got)
	}
}

// DISABLED and UNAVAILABLE are different answers: a decision versus a fact about the data.
func TestFundamentalDisabledIsNotUnavailable(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	e.Fundamental = nil // the layer was switched off
	off := BuildViews([]Evidence{e})[0]
	if off.Fundamental.Status != Disabled {
		t.Errorf("layer off → %q, want DISABLED", off.Fundamental.Status)
	}

	e.Fundamental = &fundamental.View{Status: fundamental.Unavailable}
	on := BuildViews([]Evidence{e})[0]
	if on.Fundamental.Status != Availability(fundamental.Unavailable) {
		t.Errorf("enabled with no data → %q", on.Fundamental.Status)
	}
	if off.Fundamental.Status == on.Fundamental.Status {
		t.Fatal("a switched-off feature and a missing feed produced the same status")
	}

	// The block list must carry the same distinction.
	find := func(v View) Availability {
		for _, b := range v.Blocks {
			if b.Name == "Fundamental" {
				return b.Status
			}
		}
		t.Fatal("no Fundamental block")
		return ""
	}
	if find(off) == find(on) {
		t.Error("the blocks collapsed DISABLED into UNAVAILABLE")
	}
}

// One half present is PARTIAL, not unavailable: revenue and statements come from different
// pipelines on different schedules.
func TestFundamentalPartial(t *testing.T) {
	e := okEvidence("2330.TW", "2330", "", 50, scanner.ActWait)
	v := fundView(fundamental.Partial)
	v.Financials = nil
	v.FinancialStatus = fundamental.Unavailable
	v.FinancialMetrics = fundamental.DeriveFinancials(nil)
	e.Fundamental = v

	got := BuildViews([]Evidence{e})[0].Fundamental
	if got.Status != Availability(fundamental.Partial) {
		t.Errorf("status = %q, want PARTIAL", got.Status)
	}
	// The half that IS present survives.
	if got.RevenueYoY == "UNAVAILABLE" {
		t.Error("revenue was discarded because the statements were missing")
	}
	// The half that is not says so.
	if got.GrossMargin != "UNAVAILABLE" {
		t.Errorf("gross margin = %q with no statement", got.GrossMargin)
	}
}

// Mixed availability across candidates must not remove anyone, or reorder them.
func TestMixedFundamentalAvailabilityKeepsEveryCandidate(t *testing.T) {
	a := okEvidence("2327.TW", "2327", "", 40, scanner.ActWait)
	a.Fundamental = fundView(fundamental.Available)
	b := okEvidence("6488.TWO", "6488", "", 50, scanner.ActWait)
	b.Fundamental = &fundamental.View{Status: fundamental.Unavailable, Reason: "not archived"}
	c := okEvidence("2330.TW", "2330", "", 60, scanner.ActWait)
	c.Fundamental = fundView(fundamental.Available)

	var buf bytes.Buffer
	views := BuildViews([]Evidence{a, b, c})
	RenderText(&buf, views)
	out := buf.String()

	want := []string{"2327.TW", "6488.TWO", "2330.TW"}
	for i, w := range want {
		if views[i].Symbol != w {
			t.Fatalf("position %d = %s, want %s — fundamentals reordered the universe",
				i, views[i].Symbol, w)
		}
		if !strings.Contains(out, w) {
			t.Errorf("%s missing from the output", w)
		}
	}
	if !strings.Contains(out, "Fundamentals (Actual)") {
		t.Error("the section title does not mark the data as actual")
	}
	// No forecast vocabulary may appear — this layer has no forecast source.
	for _, banned := range []string{"Estimate", "Consensus", "Forecast", "Target Price",
		"Fair Value", "2027E"} {
		if strings.Contains(out, banned) {
			t.Errorf("the output contains %q, but no forecast data exists", banned)
		}
	}
}

// THE scanner invariance guard: fundamentals are evidence, never a decision input.
func TestFundamentalsDoNotChangeScannerFields(t *testing.T) {
	base := okEvidence("2330.TW", "2330", "", 71, scanner.ActPrepare)
	snapshot := func(e Evidence) [4]string {
		v := BuildViews([]Evidence{e})[0]
		return [4]string{v.Action, v.Stage, v.Price, v.ExplosionProb}
	}

	none := base
	none.Fundamental = nil
	strong := base
	strong.Fundamental = fundView(fundamental.Available)
	weak := base
	w := fundView(fundamental.Available)
	w.Financials.OperatingIncome = -1e12 // a heavy loss
	w.FinancialMetrics = fundamental.DeriveFinancials(w.Financials)
	weak.Fundamental = w

	a, b, c := snapshot(none), snapshot(strong), snapshot(weak)
	if a != b || b != c {
		t.Errorf("fundamentals changed the scanner fields:\n none=%v\n strong=%v\n weak=%v", a, b, c)
	}
	// RocketScore too.
	if BuildViews([]Evidence{none})[0].RocketScore != BuildViews([]Evidence{weak})[0].RocketScore {
		t.Error("fundamentals changed RocketScore")
	}
}
