package healthcheck

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
)

// ── fixtures ──────────────────────────────────────────────────────────────────────────

// fundOpts is one archived MOPS observation, spelled out field by field so a test can say
// exactly which period it is talking about. The default is the shape that matters most: a
// CUMULATIVE half-year filing.
type fundOpts struct {
	observed  string // archive date (the snapshot's ObservedAt)
	published string // the source's own 出表日期; defaults to the day before observed
	year      int
	quarter   int
	month     int // set instead of quarter for a monthly-revenue-only snapshot
	eps       *float64
	revenue   float64
	gross     float64
	operating float64
	net       float64
	// noFinancials writes a snapshot with a monthly revenue record and no statement.
	noFinancials bool
	// revenueToo adds a monthly revenue record ALONGSIDE the statement, which is the only
	// shape internal/fundamental calls a complete observation.
	revenueToo bool
}

func writeFund(t *testing.T, dir string, o fundOpts) {
	t.Helper()
	obs := day(o.observed)
	pub := obs.AddDate(0, 0, -1)
	if o.published != "" {
		pub = day(o.published)
	}
	if o.year == 0 {
		o.year = 2026
	}
	snap := &fundamental.Snapshot{
		SchemaVersion:   fundamental.SchemaVersion,
		Symbol:          "2330.TW",
		Source:          "test",
		ObservedAt:      obs,
		Status:          fundamental.Available,
		RevenueStatus:   fundamental.Unavailable,
		FinancialStatus: fundamental.Available,
	}
	if o.noFinancials {
		snap.FinancialStatus = fundamental.Unavailable
		snap.RevenueStatus = fundamental.Available
		snap.Revenue = &fundamental.MonthlyRevenue{
			Symbol:         "2330.TW",
			Period:         fundamental.Period{Year: o.year, Month: o.month},
			Revenue:        fundamental.Money(o.revenue),
			PriorMonth:     fundamental.Money(o.revenue * 0.9),
			PriorYearMonth: fundamental.Money(o.revenue * 0.8),
			YTD:            fundamental.Money(o.revenue * 6),
			PriorYearYTD:   fundamental.Money(o.revenue * 5),
			PublishedAt:    pub,
		}
	} else {
		if o.revenueToo {
			snap.RevenueStatus = fundamental.Available
			snap.Revenue = &fundamental.MonthlyRevenue{
				Symbol:         "2330.TW",
				Period:         fundamental.Period{Year: o.year, Month: 8},
				Revenue:        fundamental.Money(o.revenue),
				PriorMonth:     fundamental.Money(o.revenue * 0.9),
				PriorYearMonth: fundamental.Money(o.revenue * 0.8),
				YTD:            fundamental.Money(o.revenue * 6),
				PriorYearYTD:   fundamental.Money(o.revenue * 5),
				PublishedAt:    pub,
			}
		}
		snap.Financials = &fundamental.Financials{
			Symbol:          "2330.TW",
			Period:          fundamental.Period{Year: o.year, Quarter: o.quarter, Cumulative: true},
			Revenue:         fundamental.Money(o.revenue),
			GrossProfit:     fundamental.Money(o.gross),
			OperatingIncome: fundamental.Money(o.operating),
			NetIncome:       fundamental.Money(o.net),
			CumulativeEPS:   o.eps,
			PublishedAt:     pub,
		}
	}
	if _, err := fundamental.SaveSnapshot(dir, snap); err != nil {
		t.Fatalf("save fundamental %s: %v", o.observed, err)
	}
}

func f64(v float64) *float64 { return &v }

// fundService builds a service with a 60-bar price series, so every fundamental test runs
// through the same real evidence path a request does.
func fundService(t *testing.T, today string) (*Service, string) {
	t.Helper()
	end := day(today)
	return newService(t, stubPrices{data: map[string]fetcher.StockData{
		"2330": {Symbol: "2330", Market: "TW", Candles: series(end, 60, 700)},
	}}, today)
}

// ── the cumulative-vs-quarter contract ────────────────────────────────────────────────

// The half-year filing must never present itself as a single quarter.
//
// This is the most expensive mistake available in this repo: MOPS 季別=2 is Jan–Jun, so a
// reader who takes it for Q2 alone doubles the earnings they are paying for.
func TestCumulativeEPSIsNeverPresentedAsQuarterly(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1_000_000, gross: 500_000, operating: 400_000, net: 300_000,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	eps := h.Fundamental.EPS

	got, ok := eps.Cumulative.Float()
	if !ok || got != 22.5 {
		t.Fatalf("Cumulative = %+v, want the reported 22.5", eps.Cumulative)
	}
	if eps.Quarter.Status == StatusAvailable {
		t.Fatalf("Quarter EPS = %+v — a half-year cumulative was passed off as one quarter", eps.Quarter)
	}
	if eps.Quarter.Value != nil {
		t.Fatalf("Quarter EPS carries a number (%v) the source never published", *eps.Quarter.Value)
	}
	if eps.Status == StatusAvailable {
		t.Fatalf("EPS block = AVAILABLE with three of four kinds missing")
	}
	// The label has to carry the convention: a legend elsewhere on the page does not travel
	// with the number when someone screenshots one row.
	if !strings.Contains(eps.Period, "累計") || !strings.Contains(eps.Period, "H1") {
		t.Fatalf("Period = %q, want it to say both H1 and 累計", eps.Period)
	}
	if !strings.Contains(eps.Cumulative.AsOf, "累計") {
		t.Fatalf("Cumulative.AsOf = %q — the number travels without its window", eps.Cumulative.AsOf)
	}
	if eps.Note == "" {
		t.Fatalf("no note explaining the cumulative convention")
	}
}

// Q1 is the one period where the cumulative window IS the quarter, and it is an identity,
// not a derivation. It must produce the real number rather than a blanket refusal.
func TestQ1CumulativeIsAlsoTheQuarter(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-05-15", year: 2026, quarter: 1, eps: f64(9.75),
		revenue: 500_000, gross: 250_000, operating: 200_000, net: 150_000,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	eps := h.Fundamental.EPS

	q, ok := eps.Quarter.Float()
	if !ok {
		t.Fatalf("Quarter = %+v, want Q1's own figure — Jan–Mar cumulative IS Q1", eps.Quarter)
	}
	if q != 9.75 {
		t.Fatalf("Quarter = %v, want 9.75 unchanged — nothing should be computed here", q)
	}
	c, _ := eps.Cumulative.Float()
	if c != q {
		t.Fatalf("Q1 quarter (%v) and cumulative (%v) must be the same number", q, c)
	}
	// Provenance has to say it was not derived, or the next reader will assume it was.
	if !strings.Contains(eps.Quarter.Source, "Q1") {
		t.Fatalf("Quarter.Source = %q — it must say why a quarterly figure exists here", eps.Quarter.Source)
	}
	if eps.Status != StatusPartial {
		t.Fatalf("EPS status = %s, want PARTIAL — TTM and forward are still missing", eps.Status)
	}
}

// Q3 and Q4 are cumulative too, and neither may produce a standalone quarter.
func TestOnlyQ1YieldsAQuarterlyEPS(t *testing.T) {
	for _, q := range []int{2, 3, 4} {
		s, root := fundService(t, "2026-09-04")
		writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
			observed: "2026-08-20", year: 2026, quarter: q, eps: f64(30),
			revenue: 1_000_000, gross: 500_000, operating: 400_000, net: 300_000,
		})
		h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
		got := h.Fundamental.EPS.Quarter
		if got.Status != StatusInsufficientData {
			t.Errorf("Q%d: Quarter status = %s, want INSUFFICIENT_DATA", q, got.Status)
		}
		if got.Reason == "" {
			t.Errorf("Q%d: no reason given for the missing quarterly EPS", q)
		}
	}
}

// TTM and forward EPS are never invented — not annualised from a half year, and never
// filled from a model's idea of analyst consensus.
func TestTTMAndForwardEPSAreNeverFabricated(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1_000_000, gross: 500_000, operating: 400_000, net: 300_000,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	eps := h.Fundamental.EPS

	// The two statuses are deliberately different. INSUFFICIENT_DATA means the capability
	// exists and the evidence has not accrued; NOT_IMPLEMENTED means there is no authorized
	// source at all and waiting will never help.
	for name, tc := range map[string]struct {
		v    Value
		want Status
	}{
		"TTM":     {eps.TTM, StatusInsufficientData},
		"Forward": {eps.Forward, StatusNotImplemented},
	} {
		v := tc.v
		if v.Status != tc.want {
			t.Errorf("%s status = %s, want %s", name, v.Status, tc.want)
		}
		if v.Value != nil {
			t.Errorf("%s carries a number (%v) with no source behind it", name, *v.Value)
		}
		if v.Display != string(tc.want) {
			t.Errorf("%s.Display = %q — it must not read as a figure", name, v.Display)
		}
		if v.Reason == "" {
			t.Errorf("%s gives no reason", name)
		}
	}
	// Annualising 22.5 would give 45 — the specific wrong answer this refuses to produce.
	if v, ok := eps.TTM.Float(); ok {
		t.Fatalf("TTM = %v, but no four consecutive quarters were ever loaded", v)
	}
	if !strings.Contains(eps.Forward.Reason, "AI") {
		t.Errorf("Forward.Reason = %q — it should say the model may not supply one either",
			eps.Forward.Reason)
	}
}

// ── margins and growth ────────────────────────────────────────────────────────────────

// Margins are fractions in the domain layer and percentages exactly once, here.
func TestMarginsAreConvertedToPercentOnce(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	b := h.Fundamental

	for _, tc := range []struct {
		name string
		got  Value
		want float64
	}{
		{"gross", b.GrossMargin, 50},
		{"operating", b.OperatingMargin, 40},
		{"net", b.NetMargin, 30},
	} {
		v, ok := tc.got.Float()
		if !ok {
			t.Fatalf("%s margin = %+v, want a figure", tc.name, tc.got)
		}
		if v != tc.want {
			t.Fatalf("%s margin = %v, want %v — a fraction leaked through, or was scaled twice",
				tc.name, v, tc.want)
		}
		if tc.got.Unit != "%" {
			t.Fatalf("%s margin unit = %q, want %%", tc.name, tc.got.Unit)
		}
		if !strings.HasSuffix(tc.got.Display, "%") {
			t.Fatalf("%s margin Display = %q, want a percent suffix", tc.name, tc.got.Display)
		}
	}
}

// A statement with no EPS line is UNAVAILABLE, not zero — and the margins beside it, which
// do not depend on EPS, still report.
func TestMissingEPSDoesNotZeroTheBlock(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: nil,
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	eps := h.Fundamental.EPS.Cumulative
	if eps.Status == StatusAvailable || eps.Value != nil {
		t.Fatalf("Cumulative EPS = %+v with no EPS line in the filing", eps)
	}
	if eps.Display == "0" || eps.Display == "0.00" || eps.Display == "-" {
		t.Fatalf("Cumulative EPS Display = %q — reads as a company that earned nothing", eps.Display)
	}
	if _, ok := h.Fundamental.GrossMargin.Float(); !ok {
		t.Fatalf("the margins were dropped along with the EPS: %+v", h.Fundamental.GrossMargin)
	}
}

// With no archive at all, every figure must say so — and none may render as a number.
func TestNoFundamentalArchiveRendersNoNumbers(t *testing.T) {
	s, _ := fundService(t, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	b := h.Fundamental
	if b.Status == StatusAvailable {
		t.Fatalf("status = AVAILABLE with no archive written")
	}
	if b.Reason == "" {
		t.Fatalf("no reason given")
	}
	for name, v := range map[string]Value{
		"revenue": b.Revenue, "yoy": b.RevenueYoY, "mom": b.RevenueMoM,
		"period_revenue": b.Revenue4Period, "gross": b.GrossMargin,
		"operating": b.OperatingMargin, "net": b.NetMargin,
		"eps_cumulative": b.EPS.Cumulative, "eps_quarter": b.EPS.Quarter,
		"eps_ttm": b.EPS.TTM, "eps_forward": b.EPS.Forward,
	} {
		if v.Value != nil {
			t.Errorf("%s carries a number with no archive: %v", name, *v.Value)
		}
		if v.Status == StatusAvailable {
			t.Errorf("%s = AVAILABLE with no archive", name)
		}
		if isNumericLooking(v.Display) {
			t.Errorf("%s Display = %q — a reader would take that for a figure", name, v.Display)
		}
	}
}

// isNumericLooking reports whether a display string could be mistaken for a financial
// figure. A status word cannot; "0", "0.00", "-" and "0%" all can.
func isNumericLooking(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "—" {
		return true
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// ── point in time ─────────────────────────────────────────────────────────────────────

// A filing PUBLISHED after the as-of date is invisible even when it sits in an archive
// observed on or before it. Observation date and publication date are different facts, and
// only the second one governs what a reader could have known.
func TestFilingPublishedAfterAsOfIsInvisible(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	// Observed on the 1st, but the source stamped it as published on the 3rd.
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-09-01", published: "2026-09-03",
		year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})

	early := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-02"})
	if _, ok := early.Fundamental.EPS.Cumulative.Float(); ok {
		t.Fatalf("a filing published 2026-09-03 was visible to a check dated 2026-09-02")
	}

	later := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if v, ok := later.Fundamental.EPS.Cumulative.Float(); !ok || v != 22.5 {
		t.Fatalf("EPS = %+v on 2026-09-04, want 22.5 — it was public by then",
			later.Fundamental.EPS.Cumulative)
	}
}

// The check must read the archive for its own date even when a NEWER one exists on disk.
// This is the look-ahead the whole archive layout exists to prevent, so it is asserted on
// the rendered block and not only on the raw evidence.
func TestNewerArchiveDoesNotLeakIntoAHistoricalCheck(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	fdir := filepath.Join(root, "fundamental")

	writeFund(t, fdir, fundOpts{observed: "2026-05-15", year: 2026, quarter: 1, eps: f64(9.75),
		revenue: 500, gross: 250, operating: 200, net: 150})
	writeFund(t, fdir, fundOpts{observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-06-30"})
	got, ok := h.Fundamental.EPS.Cumulative.Float()
	if !ok {
		t.Fatalf("no EPS for 2026-06-30: %+v", h.Fundamental.EPS.Cumulative)
	}
	if got != 9.75 {
		t.Fatalf("EPS = %v on 2026-06-30, want the Q1 filing's 9.75 — the August archive leaked back", got)
	}
	if !strings.Contains(h.Fundamental.EPS.Period, "Q1") {
		t.Fatalf("period = %q, want the Q1 window", h.Fundamental.EPS.Period)
	}
	// And on that date Q1 is a real quarter, so the identity has to survive the trip too.
	if v, ok := h.Fundamental.EPS.Quarter.Float(); !ok || v != 9.75 {
		t.Fatalf("Q1 quarter EPS = %+v on the historical date", h.Fundamental.EPS.Quarter)
	}
}

// ── plumbing ──────────────────────────────────────────────────────────────────────────

// The block reaches Health and the data-quality panel, so a missing archive shows up as a
// named gap rather than a silently absent section.
func TestFundamentalIsReportedInDataQuality(t *testing.T) {
	s, root := fundService(t, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	var found *BlockStatus
	for i := range h.DataQuality.Blocks {
		if h.DataQuality.Blocks[i].Name == "fundamental" {
			found = &h.DataQuality.Blocks[i]
		}
	}
	if found == nil {
		t.Fatalf("no fundamental row in the data-quality panel: %+v", h.DataQuality.Blocks)
	}
	if found.Status == StatusAvailable {
		t.Fatalf("fundamental reported AVAILABLE with no archive")
	}
	if h.DataQuality.Level == QualityComplete {
		t.Fatalf("quality = COMPLETE while the fundamental block is %s", found.Status)
	}

	// A statement with no monthly revenue beside it is half the picture, and the layer says
	// so — PARTIAL is the honest answer, not AVAILABLE.
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})
	half := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if half.Fundamental.Status != StatusPartial {
		t.Fatalf("statement-only archive = %s, want PARTIAL", half.Fundamental.Status)
	}

	// Both halves present → AVAILABLE, and the panel follows the block.
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-21", year: 2026, quarter: 2, eps: f64(22.5), revenueToo: true,
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})
	full := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if full.Fundamental.Status != StatusAvailable {
		t.Fatalf("full archive = %s (%s), want AVAILABLE",
			full.Fundamental.Status, full.Fundamental.Reason)
	}
	for _, b := range full.DataQuality.Blocks {
		if b.Name == "fundamental" && b.Status != StatusAvailable {
			t.Fatalf("panel says %s while the block says AVAILABLE", b.Status)
		}
	}
}

// The published date is surfaced, not the fetch date — a page that showed only ObservedAt
// would invite a reader to date the filing by when this repo happened to download it.
func TestPublishedAndObservedDatesAreBothCarried(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-08-20", published: "2026-08-14",
		year: 2026, quarter: 2, eps: f64(22.5),
		revenue: 1000, gross: 500, operating: 400, net: 300,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	b := h.Fundamental
	if b.FinancialPublishedAt != "2026-08-14" {
		t.Fatalf("FinancialPublishedAt = %q, want the source's 2026-08-14", b.FinancialPublishedAt)
	}
	if b.ObservedAt != "2026-08-20" {
		t.Fatalf("ObservedAt = %q, want the fetch date 2026-08-20", b.ObservedAt)
	}
	if b.FinancialPublishedAt == b.ObservedAt {
		t.Fatalf("the two dates collapsed onto each other")
	}
}

// Monthly revenue growth reports as a percentage with its own period, independently of the
// statement — the two halves of the block arrive on different schedules.
func TestMonthlyRevenueReportsWithoutAStatement(t *testing.T) {
	s, root := fundService(t, "2026-09-04")
	writeFund(t, filepath.Join(root, "fundamental"), fundOpts{
		observed: "2026-09-01", noFinancials: true, year: 2026, month: 8, revenue: 1000,
	})

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	b := h.Fundamental
	if b.RevenueStatus != StatusAvailable {
		t.Fatalf("RevenueStatus = %s (%s)", b.RevenueStatus, b.Reason)
	}
	if v, ok := b.Revenue.Float(); !ok || v != 1000 {
		t.Fatalf("Revenue = %+v, want 1000", b.Revenue)
	}
	if b.RevenuePeriod != "2026-08" {
		t.Fatalf("RevenuePeriod = %q, want 2026-08", b.RevenuePeriod)
	}
	// 1000 vs a prior-year month of 800 is +25%.
	if v, ok := b.RevenueYoY.Float(); !ok || v < 24.99 || v > 25.01 {
		t.Fatalf("RevenueYoY = %+v, want +25%%", b.RevenueYoY)
	}
	// No statement in this snapshot, so the EPS side must stay empty rather than zero.
	if b.EPS.Cumulative.Value != nil {
		t.Fatalf("EPS appeared from a revenue-only snapshot: %+v", b.EPS.Cumulative)
	}
	if b.FinancialStatus == StatusAvailable {
		t.Fatalf("FinancialStatus = AVAILABLE with no statement in the snapshot")
	}
}

var _ = time.Time{}
