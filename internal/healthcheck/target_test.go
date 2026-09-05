package healthcheck

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// ── the EPS base session ──────────────────────────────────────────────────────────────

// The EPS is recovered from the close of the session the exchange's P/E belongs to, which is
// days older than the analysis date because that is how the exchanges publish.
func TestTargetEPSUsesThePESessionsClose(t *testing.T) {
	end := day("2026-09-04")
	s, root := valService(t, "2026-09-04")
	vdir := root

	// A P/E of 20 published for the 2026-09-01 session. The series rises 1 TWD a day from
	// 700 over 60 sessions ending 2026-09-04, so 2026-09-01 closed at 756 and 2026-09-04 at
	// 759 — the exact lag that makes the two prices different.
	writeSessions(t, vdir, valuation.MinHistoricalPESamples-1, "2026-09-01", 20)
	pe := 20.0
	writeRatios(t, vdir, "2026-09-01", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	tgt := h.Target
	if tgt.Status != StatusAvailable {
		t.Fatalf("status = %s (%s)", tgt.Status, tgt.Reason)
	}
	if tgt.EPSBaseDate != "2026-09-01" {
		t.Fatalf("EPSBaseDate = %q, want the P/E's own session", tgt.EPSBaseDate)
	}
	basePrice, ok := tgt.EPSBasePrice.Float()
	if !ok {
		t.Fatalf("no EPS base price: %+v", tgt.EPSBasePrice)
	}
	current, _ := tgt.CurrentPrice.Float()
	if basePrice == current {
		t.Fatalf("base price and current price are both %v — the lag was collapsed", basePrice)
	}
	eps, ok := tgt.EPS.Float()
	if !ok {
		t.Fatalf("no EPS: %+v", tgt.EPS)
	}
	if want := basePrice / 20; eps != want {
		t.Fatalf("EPS = %v, want %v (= %v / 20)", eps, want, basePrice)
	}
	if wrong := current / 20; eps == wrong {
		t.Fatalf("EPS was recovered from the analysis-date close (%v)", current)
	}
	_ = end
}

// With no close for the P/E's own session, no target is produced. The engine must not reach
// for the nearest price it happens to have.
func TestTargetRefusesWhenThePESessionHasNoClose(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	writeSessions(t, vdir, valuation.MinHistoricalPESamples, "2026-09-04", 20)
	// The newest ARCHIVE describes a session far older than the 60-bar price series reaches,
	// so there is no close to divide the P/E out of.
	pe := 20.0
	writeLaggedRatios(t, vdir, "2026-09-04", "2020-01-02", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	tgt := h.Target
	if tgt.Status == StatusAvailable {
		t.Fatalf("a target was produced with no close for the P/E session: %+v", tgt)
	}
	if tgt.EPS.Value != nil {
		t.Fatalf("an EPS was produced: %v", *tgt.EPS.Value)
	}
	for _, sc := range tgt.Scenarios {
		if sc.TargetPrice.Value != nil {
			t.Fatalf("%s produced a target price", sc.Name)
		}
	}
}

// ── no fabricated inputs ──────────────────────────────────────────────────────────────

// Below the sample floor there is no rule, no multiple and no target — and the block says
// how far short it is rather than drawing three rows of zeros.
func TestTargetIsInsufficientWithoutEnoughSamples(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	pe := 20.0
	writeRatios(t, vdir, "2026-09-03", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	tgt := h.Target
	if tgt.Status != StatusInsufficientData {
		t.Fatalf("status = %s, want INSUFFICIENT_DATA", tgt.Status)
	}
	if tgt.Rule != valuation.RuleNone {
		t.Fatalf("rule = %q, want NONE — no rule may be claimed when none applied", tgt.Rule)
	}
	if tgt.SampleCount != 1 || tgt.RequiredSamples != valuation.MinHistoricalPESamples {
		t.Fatalf("samples = %d/%d", tgt.SampleCount, tgt.RequiredSamples)
	}
	if len(tgt.Scenarios) != 3 {
		t.Fatalf("want three labelled rows even on refusal, got %d", len(tgt.Scenarios))
	}
	for _, sc := range tgt.Scenarios {
		for name, v := range map[string]Value{
			"pe": sc.PEMultiple, "target": sc.TargetPrice, "upside": sc.UpsidePct, "eps": sc.EPS,
		} {
			if v.Value != nil {
				t.Errorf("%s %s carries a number: %v", sc.Name, name, *v.Value)
			}
			if isNumericLooking(v.Display) {
				t.Errorf("%s %s Display = %q — reads as a figure", sc.Name, name, v.Display)
			}
		}
		if sc.Reason == "" {
			t.Errorf("%s gives no reason", sc.Name)
		}
	}
	if tgt.MarginOfSafety.Value != nil {
		t.Fatalf("a margin of safety was produced with no target")
	}
	if isNumericLooking(tgt.MarginOfSafety.Display) {
		t.Fatalf("MarginOfSafety Display = %q", tgt.MarginOfSafety.Display)
	}
}

// A loss-making company has no published P/E, so there is no earnings base and no target —
// and the refusal must say why the reported cumulative EPS is not a substitute.
func TestTargetRefusesWithoutAPublishedPE(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	writeSessions(t, vdir, valuation.MinHistoricalPESamples, "2026-09-04", 20)
	// The most recent session has no P/E at all.
	writeRatios(t, vdir, "2026-09-04", nil, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	if h.Target.Status == StatusAvailable {
		t.Fatalf("a target was produced with no published P/E")
	}
	if h.Target.EPS.Value != nil {
		t.Fatalf("an EPS was produced: %v", *h.Target.EPS.Value)
	}
}

// ── traceability ──────────────────────────────────────────────────────────────────────

// Every scenario row answers on its own: EPS, its source and kind, the multiple, the rule
// behind it, the sample count and the as-of date.
func TestEveryTargetRowIsSelfContained(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	n := valuation.MinHistoricalPESamples
	// A real spread so P25 / median / P75 differ.
	writeSessions(t, vdir, n, "2026-09-04", 10)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	tgt := h.Target
	if tgt.Status != StatusAvailable {
		t.Fatalf("status = %s (%s)", tgt.Status, tgt.Reason)
	}
	if len(tgt.Scenarios) != 3 {
		t.Fatalf("scenarios = %d", len(tgt.Scenarios))
	}
	names := []string{valuation.ScenarioBear, valuation.ScenarioBase, valuation.ScenarioBull}
	for i, sc := range tgt.Scenarios {
		if sc.Name != names[i] {
			t.Fatalf("row %d = %s, want %s — the order is fixed", i, sc.Name, names[i])
		}
		if _, ok := sc.EPS.Float(); !ok {
			t.Errorf("%s: no EPS on the row", sc.Name)
		}
		if sc.EPSBasis != valuation.EPSTrailingImplied {
			t.Errorf("%s: EPSBasis = %q", sc.Name, sc.EPSBasis)
		}
		if sc.EPSSource == "" {
			t.Errorf("%s: no EPS source", sc.Name)
		}
		if _, ok := sc.PEMultiple.Float(); !ok {
			t.Errorf("%s: no PE multiple", sc.Name)
		}
		if sc.Rule != valuation.RuleHistoricalPercentile {
			t.Errorf("%s: Rule = %q", sc.Name, sc.Rule)
		}
		if sc.RuleDetail == "" {
			t.Errorf("%s: the multiple has no justification on the row", sc.Name)
		}
		if sc.SampleCount != n {
			t.Errorf("%s: SampleCount = %d, want %d", sc.Name, sc.SampleCount, n)
		}
		if sc.AsOf != "2026-09-04" {
			t.Errorf("%s: AsOf = %q", sc.Name, sc.AsOf)
		}
		if !strings.Contains(sc.PEMultiple.Source, "本股") &&
			!strings.Contains(sc.PEMultiple.Source, "人工") {
			t.Errorf("%s: multiple source = %q, want it to name the evidence", sc.Name, sc.PEMultiple.Source)
		}
	}

	// Bear < Base < Bull, because they are P25 < median < P75 of one distribution.
	bear, _ := tgt.Scenarios[0].PEMultiple.Float()
	base, _ := tgt.Scenarios[1].PEMultiple.Float()
	bull, _ := tgt.Scenarios[2].PEMultiple.Float()
	if !(bear <= base && base <= bull) {
		t.Fatalf("multiples not ordered: %v / %v / %v", bear, base, bull)
	}

	// The headline margin of safety is the BASE row's upside, not a fourth number.
	ms, ok := tgt.MarginOfSafety.Float()
	if !ok {
		t.Fatalf("no margin of safety: %+v", tgt.MarginOfSafety)
	}
	baseUp, _ := tgt.Scenarios[1].UpsidePct.Float()
	if ms != baseUp {
		t.Fatalf("margin of safety %v != base upside %v", ms, baseUp)
	}
}

// The block is reported in the data-quality panel, so a refusal is a named gap.
func TestTargetIsReportedInDataQuality(t *testing.T) {
	s, _ := valService(t, "2026-09-04")
	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	var seen bool
	for _, b := range h.DataQuality.Blocks {
		if b.Name == "target_price" {
			seen = true
			if b.Status == StatusAvailable {
				t.Fatalf("target AVAILABLE with no valuation archive")
			}
		}
	}
	if !seen {
		t.Fatalf("no target_price row in the panel: %+v", h.DataQuality.Blocks)
	}
}

// Both prices on the block name their own session. The upside's denominator is a price from
// a specific day too, and on a holiday or behind a stale cache that day is not asOf.
func TestBothPricesNameTheirSession(t *testing.T) {
	s, vdir := valService(t, "2026-09-04")
	writeSessions(t, vdir, valuation.MinHistoricalPESamples-1, "2026-09-01", 20)
	pe := 20.0
	writeRatios(t, vdir, "2026-09-01", &pe, nil, nil)

	h := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	tgt := h.Target
	if tgt.Status != StatusAvailable {
		t.Fatalf("status = %s (%s)", tgt.Status, tgt.Reason)
	}
	if tgt.CurrentDate == "" {
		t.Fatalf("CurrentDate is empty — the upside denominator has no session")
	}
	if tgt.CurrentDate != h.Price.BarDate {
		t.Fatalf("CurrentDate = %q, want the price block's bar date %q",
			tgt.CurrentDate, h.Price.BarDate)
	}
	if tgt.CurrentPrice.AsOf != tgt.CurrentDate {
		t.Fatalf("CurrentPrice.AsOf = %q, want %q", tgt.CurrentPrice.AsOf, tgt.CurrentDate)
	}
	if tgt.EPSBasePrice.AsOf != tgt.EPSBaseDate {
		t.Fatalf("EPSBasePrice.AsOf = %q, want %q", tgt.EPSBasePrice.AsOf, tgt.EPSBaseDate)
	}
	if tgt.CurrentDate == tgt.EPSBaseDate {
		t.Fatalf("both dates are %s — this fixture exists to keep them apart", tgt.CurrentDate)
	}
	// The margin of safety must say which quantity it is, or "−56%" reads as a contradiction.
	if tgt.MarginOfSafety.Status == StatusAvailable && tgt.MarginOfSafetyBasis == "" {
		t.Fatalf("a margin of safety was reported with no statement of what it measures")
	}
}
