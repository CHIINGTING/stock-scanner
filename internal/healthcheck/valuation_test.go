package healthcheck

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// valService builds a service and returns its valuation archive directory.
func valService(t *testing.T, today string) (*Service, string) {
	t.Helper()
	s, root := fundService(t, today)
	return s, filepath.Join(root, "valuation")
}

// writeRatios writes one archived session with the ratios a test cares about. The archive
// date and the session date are the same, which is the simple case.
func writeRatios(t *testing.T, dir, date string, pe, pb, yield *float64) {
	t.Helper()
	writeLaggedRatios(t, dir, date, date, pe, pb, yield)
}

// writeLaggedRatios separates the two dates the way the exchanges actually publish: the
// archive is written on archiveDate, and the ratios inside it describe the earlier
// sessionDate. LoadHistory picks the newest ARCHIVE, so this is how a test controls which
// session the trailing P/E ends up belonging to.
func writeLaggedRatios(t *testing.T, dir, archiveDate, sessionDate string, pe, pb, yield *float64) {
	t.Helper()
	snap := &valuation.Snapshot{
		SchemaVersion: valuation.SchemaVersion,
		Symbol:        "2330.TW",
		Source:        "test",
		ObservedAt:    day(archiveDate),
		Status:        valuation.Available,
		Ratios: &valuation.Ratios{
			Symbol: "2330.TW", Date: sessionDate,
			PERatio: pe, PBRatio: pb, DividendYield: yield,
		},
	}
	if _, err := valuation.SaveSnapshot(dir, snap); err != nil {
		t.Fatalf("save valuation %s: %v", archiveDate, err)
	}
}

// writeSessions writes n consecutive daily sessions ending the day before endExclusive,
// with P/E rising by 1 from start. It is the only way to reach the sample floor.
func writeSessions(t *testing.T, dir string, n int, endExclusive string, start float64) {
	t.Helper()
	end := day(endExclusive)
	for i := n; i >= 1; i-- {
		d := end.AddDate(0, 0, -i).Format(dateLayout)
		pe := start + float64(n-i)
		writeRatios(t, dir, d, &pe, nil, nil)
	}
}

// ── the INSUFFICIENT_DATA contract (§12) ──────────────────────────────────────────────

// One observation is not a distribution, and it must not render as one.
//
// The specific failures being blocked: a median of "0", a "0x", and a percentile of 0 —
// each of which would place a stock at the cheapest point of its own history on the
// strength of no history at all.
func TestOneSampleIsInsufficientAndNeverZero(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	pe := 18.0
	writeRatios(t, vdir, "2026-09-03", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	hist := h.Valuation.Historical

	if hist.Status != StatusInsufficientData {
		t.Fatalf("status = %s, want INSUFFICIENT_DATA", hist.Status)
	}
	if hist.SampleCount != 1 {
		t.Fatalf("SampleCount = %d, want 1 — the count must be reported, not hidden", hist.SampleCount)
	}
	if hist.RequiredSamples != valuation.MinHistoricalPESamples {
		t.Fatalf("RequiredSamples = %d, want %d", hist.RequiredSamples, valuation.MinHistoricalPESamples)
	}
	// The spec names this string shape: samples and requirement both visible.
	if !strings.Contains(hist.Summary, "1") || !strings.Contains(hist.Summary, "20") {
		t.Fatalf("Summary = %q, want it to name both the 1 sample and the 20 required", hist.Summary)
	}
	if !strings.Contains(hist.Summary, string(StatusInsufficientData)) {
		t.Fatalf("Summary = %q, want it to lead with INSUFFICIENT_DATA", hist.Summary)
	}

	for name, v := range map[string]Value{
		"median": hist.Median, "p25": hist.P25, "p75": hist.P75,
		"min": hist.Min, "max": hist.Max, "percentile": hist.CurrentPercentile,
	} {
		if v.Status != StatusInsufficientData {
			t.Errorf("%s status = %s, want INSUFFICIENT_DATA", name, v.Status)
		}
		if v.Value != nil {
			t.Errorf("%s carries a number (%v) computed from one observation", name, *v.Value)
		}
		if isNumericLooking(v.Display) {
			t.Errorf("%s Display = %q — a reader would take that for a statistic", name, v.Display)
		}
		if v.Display == "0" || v.Display == "0x" || v.Display == "0%" {
			t.Errorf("%s rendered as the literal zero this contract exists to prevent", name)
		}
	}

	// The trailing figure itself is a published fact and survives independently.
	if got, ok := h.Valuation.TrailingPE.Float(); !ok || got != 18 {
		t.Fatalf("TrailingPE = %+v, want the published 18", h.Valuation.TrailingPE)
	}
}

// Every count below the floor reports its own count, so a filling archive is visible.
func TestSampleCountIsReportedBelowTheFloor(t *testing.T) {
	for _, n := range []int{0, 1, 5, valuation.MinHistoricalPESamples - 1} {
		s, vdir := valService(t, "2026-09-04")
		if n > 0 {
			writeSessions(t, vdir, n, "2026-09-04", 15)
		}
		h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
		hist := h.Valuation.Historical
		if hist.Status != StatusInsufficientData {
			t.Errorf("n=%d: status = %s, want INSUFFICIENT_DATA", n, hist.Status)
		}
		if hist.SampleCount != n {
			t.Errorf("n=%d: SampleCount = %d", n, hist.SampleCount)
		}
		want := fmt.Sprintf("%d", n)
		if !strings.Contains(hist.Summary, want) {
			t.Errorf("n=%d: Summary = %q, want it to name the count", n, hist.Summary)
		}
	}
}

// At the floor the distribution becomes real, and the percentile is a PERCENT — the domain
// carries a ratio in [0,1], and the ×100 must happen exactly once.
func TestAtTheSampleFloorTheDistributionIsUsable(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	n := valuation.MinHistoricalPESamples
	// Sessions with P/E 10, 11, … and the newest one the current reading.
	writeSessions(t, vdir, n, "2026-09-04", 10)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	hist := h.Valuation.Historical

	if hist.Status != StatusAvailable {
		t.Fatalf("status = %s at %d samples, want AVAILABLE", hist.Status, hist.SampleCount)
	}
	if hist.SampleCount != n {
		t.Fatalf("SampleCount = %d, want %d", hist.SampleCount, n)
	}
	med, ok := hist.Median.Float()
	if !ok {
		t.Fatalf("median = %+v", hist.Median)
	}
	// 10..29 → median 19.5.
	if med != 19.5 {
		t.Fatalf("median = %v, want 19.5", med)
	}
	if hist.Median.Unit != "x" || !strings.HasSuffix(hist.Median.Display, "x") {
		t.Fatalf("median rendered as %q, want a multiple", hist.Median.Display)
	}
	lo, _ := hist.Min.Float()
	hi, _ := hist.Max.Float()
	if lo != 10 || hi != 29 {
		t.Fatalf("min/max = %v/%v, want 10/29", lo, hi)
	}

	pct, ok := hist.CurrentPercentile.Float()
	if !ok {
		t.Fatalf("percentile = %+v", hist.CurrentPercentile)
	}
	// The newest session's P/E is the highest in the window, so it sits at the top.
	if pct <= 1 {
		t.Fatalf("percentile = %v — a [0,1] ratio leaked through without being scaled", pct)
	}
	if pct > 100 {
		t.Fatalf("percentile = %v — scaled twice", pct)
	}
	if pct != 100 {
		t.Fatalf("percentile = %v, want 100 for the highest P/E in the window", pct)
	}
	if hist.CurrentPercentile.Unit != "%" {
		t.Fatalf("percentile unit = %q, want %%", hist.CurrentPercentile.Unit)
	}
	if hist.Summary == "" || strings.Contains(hist.Summary, string(StatusInsufficientData)) {
		t.Fatalf("Summary = %q, want a real summary at full samples", hist.Summary)
	}
}

// ── a loss-making company ─────────────────────────────────────────────────────────────

// The exchange publishes no P/E for non-positive earnings. That absence is a fact about the
// company and must never become a P/E of 0 — which would rank it as the cheapest stock on
// the board.
func TestNoPublishedPEIsNotAZeroPE(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	pb := 1.8
	yield := 2.5
	writeRatios(t, vdir, "2026-09-03", nil, &pb, &yield)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	v := h.Valuation

	if v.TrailingPE.Status == StatusAvailable || v.TrailingPE.Value != nil {
		t.Fatalf("TrailingPE = %+v with nothing published", v.TrailingPE)
	}
	if isNumericLooking(v.TrailingPE.Display) {
		t.Fatalf("TrailingPE Display = %q — reads as a real multiple", v.TrailingPE.Display)
	}
	if v.TrailingPE.Reason == "" {
		t.Fatalf("no reason given for the absent P/E")
	}
	// The ratios that do not depend on earnings still report.
	if got, ok := v.PBRatio.Float(); !ok || got != 1.8 {
		t.Fatalf("PBRatio = %+v, want 1.8", v.PBRatio)
	}
	if got, ok := v.DividendYield.Float(); !ok || got != 2.5 {
		t.Fatalf("DividendYield = %+v, want 2.5", v.DividendYield)
	}
}

// A stretch with no published P/E must not drag the distribution down: those sessions are
// dropped, not counted as zero.
func TestLossMakingSessionsAreDroppedNotZeroed(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	n := valuation.MinHistoricalPESamples
	writeSessions(t, vdir, n, "2026-09-04", 10)
	// Ten further sessions with no P/E at all, interleaved before the window's start.
	end := day("2026-09-04")
	for i := 1; i <= 10; i++ {
		d := end.AddDate(0, 0, -(n + i)).Format(dateLayout)
		writeRatios(t, vdir, d, nil, nil, nil)
	}

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	hist := h.Valuation.Historical
	if hist.SampleCount != n {
		t.Fatalf("SampleCount = %d, want %d — sessions with no P/E were counted", hist.SampleCount, n)
	}
	if lo, _ := hist.Min.Float(); lo != 10 {
		t.Fatalf("min = %v, want 10 — a zero leaked into the distribution", lo)
	}
}

// ── no source at all ──────────────────────────────────────────────────────────────────

// Forward P/E, PEG and a fair value all need an earnings forecast this repo has no
// authorized source for. They report NOT_IMPLEMENTED — "there is nothing to wait for" —
// rather than INSUFFICIENT_DATA, and never a number.
func TestForecastRatiosHaveNoSourceAndSaySo(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	pe := 18.0
	writeRatios(t, vdir, "2026-09-03", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	for name, v := range map[string]Value{
		"forward_pe": h.Valuation.ForwardPE,
		"peg":        h.Valuation.PEG,
		"fair_value": h.Valuation.FairValue,
	} {
		if v.Status != StatusNotImplemented {
			t.Errorf("%s status = %s, want NOT_IMPLEMENTED", name, v.Status)
		}
		if v.Value != nil {
			t.Errorf("%s carries a number: %v", name, *v.Value)
		}
		if v.Reason == "" {
			t.Errorf("%s gives no reason", name)
		}
		if isNumericLooking(v.Display) {
			t.Errorf("%s Display = %q", name, v.Display)
		}
	}
}

// With no archive, every valuation figure says so and none renders as a number.
func TestNoValuationArchiveRendersNoNumbers(t *testing.T) {
	s, _ := valService(t, "2026-09-04")

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	v := h.Valuation
	if v.Status == StatusAvailable {
		t.Fatalf("status = AVAILABLE with no archive")
	}
	for name, val := range map[string]Value{
		"trailing_pe": v.TrailingPE, "pb": v.PBRatio, "yield": v.DividendYield,
		"median": v.Historical.Median, "percentile": v.Historical.CurrentPercentile,
	} {
		if val.Value != nil {
			t.Errorf("%s carries a number with no archive: %v", name, *val.Value)
		}
		if isNumericLooking(val.Display) {
			t.Errorf("%s Display = %q", name, val.Display)
		}
	}
	if v.Historical.Summary == "" {
		t.Fatalf("no summary for an empty distribution")
	}
	if !strings.Contains(v.Historical.Summary, string(StatusInsufficientData)) {
		t.Fatalf("Summary = %q, want INSUFFICIENT_DATA", v.Historical.Summary)
	}
}

// ── point in time ─────────────────────────────────────────────────────────────────────

// The distribution must be built only from sessions on or before the as-of date, even when
// later archives exist. A percentile that saw the future is the exact defect the archive
// layout exists to prevent.
func TestHistoricalPEDoesNotSeeTheFuture(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	n := valuation.MinHistoricalPESamples

	// 20 sessions of P/E 10..29 ending before 2026-07-01 …
	writeSessions(t, vdir, n, "2026-07-01", 10)
	// … and a much more expensive stretch after it, which a check dated 2026-06-30 must
	// not be able to see.
	writeSessions(t, vdir, n, "2026-09-04", 90)

	past := check(t, s, Request{Symbol: "2330", AsOf: "2026-06-30"})
	hist := past.Valuation.Historical
	if hist.Status != StatusAvailable {
		t.Fatalf("status = %s (%d samples), want AVAILABLE from the earlier stretch",
			hist.Status, hist.SampleCount)
	}
	if hi, _ := hist.Max.Float(); hi > 29 {
		t.Fatalf("max = %v on 2026-06-30 — the later, pricier sessions leaked backwards", hi)
	}
	if hist.SampleCount > n {
		t.Fatalf("SampleCount = %d on 2026-06-30, want at most the %d earlier sessions",
			hist.SampleCount, n)
	}

	// And the present-day check does see them, so the test above is not passing by accident.
	now := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if hi, _ := now.Valuation.Historical.Max.Float(); hi <= 29 {
		t.Fatalf("max = %v today, want the later stretch to be visible now", hi)
	}
}

// The valuation block reaches the data-quality panel.
func TestValuationIsReportedInDataQuality(t *testing.T) {
	s, _ := valService(t, "2026-09-04")
	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	var seen bool
	for _, b := range h.DataQuality.Blocks {
		if b.Name == "valuation" {
			seen = true
			if b.Status == StatusAvailable {
				t.Fatalf("valuation AVAILABLE with no archive")
			}
		}
	}
	if !seen {
		t.Fatalf("no valuation row in the panel: %+v", h.DataQuality.Blocks)
	}
}
