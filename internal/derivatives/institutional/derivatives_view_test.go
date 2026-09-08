package institutional_test

import (
	"errors"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// The composite view: three investors, the aggregate, the evidence and the warnings (§10.4).

func seriesFor(t *testing.T, inst string, dates []string, positions []float64) institutional.InstitutionSeries {
	t.Helper()
	obs := make([]institutional.Observation, 0, len(dates))
	for i, d := range dates {
		obs = append(obs, observationFor(t, d, institutional.ScopeTX, inst,
			institutional.StatusAvailable, fptr(-positions[i]), fptr(positions[i])))
	}
	return institutional.InstitutionSeries{
		Current: obs[len(obs)-1],
		History: obs[:len(obs)-1],
	}
}

func threeSeries(t *testing.T) []institutional.InstitutionSeries {
	t.Helper()
	dates := []string{"2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04"}
	return []institutional.InstitutionSeries{
		seriesFor(t, institutional.InstitutionDealer, dates, []float64{100, 200, 300, 400}),
		seriesFor(t, institutional.InstitutionTrust, dates, []float64{10, 20, 30, 40}),
		seriesFor(t, institutional.InstitutionForeign, dates, []float64{-1000, -900, -800, -700}),
	}
}

func mustView(t *testing.T, asOf string, s []institutional.InstitutionSeries,
	p institutional.Policy) institutional.InstitutionalDerivativesView {
	t.Helper()
	v, err := institutional.BuildInstitutionalDerivativesView(asOf, s, p)
	if err != nil {
		t.Fatalf("BuildInstitutionalDerivativesView: %v", err)
	}
	return v
}

// TestTheViewAssemblesTheThreeInvestorsAndTheAggregate.
func TestTheViewAssemblesTheThreeInvestorsAndTheAggregate(t *testing.T) {
	v := mustView(t, "2026-09-04", threeSeries(t), institutional.Policy{MinPercentileSample: 2})

	if v.Status != institutional.StatusAvailable {
		t.Fatalf("status = %s, want AVAILABLE (reason %q)", v.Status, v.Reason)
	}
	if len(v.Institutions) != 3 {
		t.Fatalf("%d institutions, want 3", len(v.Institutions))
	}
	for i, name := range institutional.RequiredInstitutions {
		if v.Institutions[i].Institution != name {
			t.Errorf("institution %d = %s, want %s (the exchange's own order)",
				i, v.Institutions[i].Institution, name)
		}
	}
	if v.SessionPolicy != institutional.SessionPolicyFixedByContract {
		t.Errorf("session policy = %q; this feed has no session dimension", v.SessionPolicy)
	}
	// POSITION totals 400 + 40 − 700.
	pos, ok := v.Total.Position.Lots()
	if !ok || pos != -260 {
		t.Errorf("total POSITION = %v (ok=%v), want -260", pos, ok)
	}
	// CHANGE totals 100 + 10 + 100 over the common 09-03 baseline.
	chg, ok := v.Total.Change.Lots()
	if !ok || chg != 210 {
		t.Errorf("total CHANGE = %v (ok=%v), want 210", chg, ok)
	}
	if v.Total.ChangeBaselineDate != "2026-09-03" {
		t.Errorf("aggregate baseline = %s, want 2026-09-03", v.Total.ChangeBaselineDate)
	}
	// FLOW and POSITION are never the same number here, which is the whole point of the
	// fixture: FLOW is the negative of POSITION.
	flow, _ := v.Total.Flow.Lots()
	if flow == pos {
		t.Error("the FLOW and POSITION totals are identical; one has stood in for the other")
	}
	if v.RuleVersions.Percentile != institutional.PercentileRuleVersion ||
		v.RuleVersions.Alignment != institutional.AlignmentRuleVersion {
		t.Error("the view does not record the rule versions it was computed under")
	}
}

// TestEvidenceCarriesEverySemanticsWithItsOwnLabel.
//
// §3: nothing reaches a reader as an unqualified 淨額. Every evidence line names its semantics,
// and the CHANGE lines carry the baseline they were measured against.
func TestEvidenceCarriesEverySemanticsWithItsOwnLabel(t *testing.T) {
	v := mustView(t, "2026-09-04", threeSeries(t), institutional.Policy{MinPercentileSample: 2})
	if len(v.Evidence) == 0 {
		t.Fatal("no evidence")
	}

	seen := map[string]bool{}
	for _, e := range v.Evidence {
		seen[e.Institution+"|"+e.Label] = true
		if e.Semantics == "" {
			t.Errorf("%s %s has no semantics", e.Institution, e.Label)
		}
		if e.Display == "" {
			t.Errorf("%s %s has an empty display", e.Institution, e.Label)
		}
		if e.Semantics == institutional.SemanticsChange && e.Status == institutional.StatusAvailable {
			if e.BaselineDate == "" {
				t.Errorf("%s %s is a CHANGE with no baseline date", e.Institution, e.Label)
			}
			// The aggregate line carries the COMMON baseline date and no gap of its own:
			// three institutions can resolve to the same date over different windows, and
			// inventing one number for the three would be a claim nobody computed.
			if e.Institution != institutional.AggregateLabel && e.ObservationGap == 0 {
				t.Errorf("%s %s is a CHANGE with no observation gap", e.Institution, e.Label)
			}
		}
	}
	for _, want := range []string{
		institutional.InstitutionDealer + "|FLOW",
		institutional.InstitutionDealer + "|POSITION",
		institutional.InstitutionDealer + "|" + institutional.HorizonLabel(1),
		institutional.AggregateLabel + "|FLOW",
		institutional.AggregateLabel + "|POSITION",
	} {
		if !seen[want] {
			t.Errorf("no evidence line for %s", want)
		}
	}
}

// TestAMissingInvestorIsPartialWithTheValueAbsent is §10.4: "外資 being present is not grounds
// for calling the total available."
func TestAMissingInvestorIsPartialWithTheValueAbsent(t *testing.T) {
	all := threeSeries(t)
	v := mustView(t, "2026-09-04", all[:2], institutional.Policy{MinPercentileSample: 2})

	if v.Status != institutional.StatusPartial {
		t.Errorf("status = %s, want PARTIAL", v.Status)
	}
	if len(v.Missing) != 1 || v.Missing[0] != institutional.InstitutionForeign {
		t.Errorf("missing = %v, want [%s]", v.Missing, institutional.InstitutionForeign)
	}
	for _, m := range []institutional.ObservedMetric{
		v.Total.Flow.ObservedMetric, v.Total.Position.ObservedMetric,
		v.Total.Change.ObservedMetric,
	} {
		if m.Observed {
			t.Errorf("%s total was published with an institution missing: %v", m.Semantics, *m.Value)
		}
		if m.Display == "0" || m.Display == "0 lots" {
			t.Errorf("%s total renders as %q", m.Semantics, m.Display)
		}
	}
	if !v.HasWarning(institutional.WarnMissingInstitution) {
		t.Error("no MISSING_INSTITUTION warning")
	}
	if !v.HasWarning(institutional.WarnAggregatePartial) {
		t.Error("no AGGREGATE_PARTIAL warning")
	}
}

// TestAMixedIdentityIsAnErrorNotAStatus is §10.4's other half: "a mixed identity is a caller
// error, because no number would have been correct".
func TestAMixedIdentityIsAnErrorNotAStatus(t *testing.T) {
	base := threeSeries(t)

	t.Run("two trading dates", func(t *testing.T) {
		mixed := append([]institutional.InstitutionSeries{}, base...)
		mixed[1] = seriesFor(t, institutional.InstitutionTrust,
			[]string{"2026-09-01", "2026-09-02", "2026-09-03"}, []float64{10, 20, 30})
		_, err := institutional.BuildInstitutionalDerivativesView("2026-09-04", mixed,
			institutional.Policy{})
		if !errors.Is(err, institutional.ErrDateMismatch) {
			t.Errorf("err = %v, want ErrDateMismatch", err)
		}
	})

	t.Run("two scopes", func(t *testing.T) {
		mixed := append([]institutional.InstitutionSeries{}, base...)
		mtx := observationFor(t, "2026-09-04", institutional.ScopeMTX,
			institutional.InstitutionTrust, institutional.StatusAvailable, fptr(1), fptr(2))
		mixed[1] = institutional.InstitutionSeries{Current: mtx}
		_, err := institutional.BuildInstitutionalDerivativesView("2026-09-04", mixed,
			institutional.Policy{})
		if !errors.Is(err, institutional.ErrScopeMismatch) {
			t.Errorf("err = %v, want ErrScopeMismatch", err)
		}
	})

	t.Run("the same institution twice", func(t *testing.T) {
		dup := append([]institutional.InstitutionSeries{}, base...)
		dup = append(dup, base[0])
		_, err := institutional.BuildInstitutionalDerivativesView("2026-09-04", dup,
			institutional.Policy{})
		if !errors.Is(err, institutional.ErrDuplicateObservation) {
			t.Errorf("err = %v, want ErrDuplicateObservation", err)
		}
	})

	t.Run("a reading after the cutoff", func(t *testing.T) {
		_, err := institutional.BuildInstitutionalDerivativesView("2026-09-03", base,
			institutional.Policy{})
		if !errors.Is(err, institutional.ErrLookAhead) {
			t.Errorf("err = %v, want ErrLookAhead — a reading dated 09-04 cannot be part of a "+
				"view as of 09-03", err)
		}
	})
}

// TestTheViewRecordsItsCutoffSeparatelyFromItsTradingDate.
//
// Reading 09-04 as of 09-04 and reading it as of 09-06 are different questions with possibly
// different answers, and a view that recorded only one date could not say which it answered.
func TestTheViewRecordsItsCutoffSeparatelyFromItsTradingDate(t *testing.T) {
	v := mustView(t, "2026-09-06", threeSeries(t), institutional.Policy{MinPercentileSample: 2})
	if v.AsOf != "2026-09-06" {
		t.Errorf("as-of = %s, want 2026-09-06", v.AsOf)
	}
	if v.TradingDate != "2026-09-04" {
		t.Errorf("trading date = %s, want 2026-09-04", v.TradingDate)
	}
	if v.AsOf == v.TradingDate {
		t.Error("the cutoff and the trading date collapsed into one field")
	}
}

// TestAWeekendRaisesNoObservationGapWarning.
//
// The warning is about SESSIONS this repository does not have, not about calendar days. A
// Friday-to-Monday CHANGE spans three days and skips nothing, and warning on every Monday is how
// a warning stops being read.
func TestAWeekendRaisesNoObservationGapWarning(t *testing.T) {
	// 2026-09-04 is a Friday; 2026-09-07 the following Monday.
	dates := []string{"2026-09-03", "2026-09-04", "2026-09-07"}
	s := []institutional.InstitutionSeries{
		seriesFor(t, institutional.InstitutionDealer, dates, []float64{100, 200, 300}),
	}
	v := mustView(t, "2026-09-07", s, institutional.Policy{MinPercentileSample: 1})
	if v.HasWarning(institutional.WarnObservationGap) {
		t.Errorf("a Friday-to-Monday CHANGE raised an observation-gap warning: %+v",
			v.WarningsFor(institutional.WarnObservationGap))
	}

	// A NO_SESSION row for the weekend changes nothing: it is the calendar, not a gap.
	withWeekend := append([]institutional.Observation{}, s[0].History...)
	withWeekend = append(withWeekend, noSession(t, "2026-09-05"), noSession(t, "2026-09-06"))
	s[0].History = withWeekend
	v = mustView(t, "2026-09-07", s, institutional.Policy{MinPercentileSample: 1})
	if v.HasWarning(institutional.WarnObservationGap) {
		t.Errorf("recorded NO_SESSION days raised an observation-gap warning: %+v",
			v.WarningsFor(institutional.WarnObservationGap))
	}
}

// TestASkippedSessionRaisesAnObservationGapWarning is the complement: a day the exchange traded
// and this repository does not have IS a gap, and the warning names it.
func TestASkippedSessionRaisesAnObservationGapWarning(t *testing.T) {
	dates := []string{"2026-09-01", "2026-09-02", "2026-09-04"}
	s := []institutional.InstitutionSeries{
		seriesFor(t, institutional.InstitutionDealer, dates, []float64{100, 200, 400}),
	}
	s[0].History = append(s[0].History, notPublished(t, "2026-09-03"))

	v := mustView(t, "2026-09-04", s, institutional.Policy{MinPercentileSample: 1})
	ws := v.WarningsFor(institutional.WarnObservationGap)
	if len(ws) != 1 {
		t.Fatalf("observation-gap warnings = %+v, want exactly one", ws)
	}
	if ws[0].Institution != institutional.InstitutionDealer {
		t.Errorf("the warning names %q", ws[0].Institution)
	}
	if ws[0].Message == "" {
		t.Error("the warning has no message")
	}
}

// TestAnUnusableInvestorIsWarnedAboutAndPublishesNoNumbers.
func TestAnUnusableInvestorIsWarnedAboutAndPublishesNoNumbers(t *testing.T) {
	s := threeSeries(t)
	s[1] = institutional.InstitutionSeries{
		Current: observationFor(t, "2026-09-04", institutional.ScopeTX,
			institutional.InstitutionTrust, institutional.StatusNotPublished, nil, nil),
	}
	v := mustView(t, "2026-09-04", s, institutional.Policy{MinPercentileSample: 2})

	if v.Status != institutional.StatusPartial {
		t.Errorf("status = %s, want PARTIAL", v.Status)
	}
	if !v.HasWarning(institutional.WarnSourceNotUsable) {
		t.Errorf("no SOURCE_NOT_USABLE warning: %+v", v.Warnings)
	}
	iv, ok := v.Institution(institutional.InstitutionTrust)
	if !ok {
		t.Fatal("the unusable institution is not in the view at all")
	}
	if iv.Position.Observed || iv.Flow.Observed {
		t.Error("a NOT_PUBLISHED institution published a number")
	}
	if v.Total.Position.Observed {
		t.Error("the total was published with one institution unpublished")
	}
}

// TestThePercentilesOnTheViewAreThePureLayersOwn: every institution carries the three separate
// distributions, computed inside the view rather than left to a caller.
func TestThePercentilesOnTheViewAreThePureLayersOwn(t *testing.T) {
	v := mustView(t, "2026-09-04", threeSeries(t), institutional.Policy{MinPercentileSample: 2})
	for _, iv := range v.Institutions {
		if len(iv.Percentiles.Distributions) != 3 {
			t.Fatalf("%s carries %d distributions, want 3", iv.Institution,
				len(iv.Percentiles.Distributions))
		}
		want := []institutional.ValueSemantics{
			institutional.SemanticsFlow, institutional.SemanticsPosition,
			institutional.SemanticsChange,
		}
		for i, sem := range want {
			if iv.Percentiles.Distributions[i].Semantics != sem {
				t.Errorf("%s distribution %d is %s, want %s", iv.Institution, i,
					iv.Percentiles.Distributions[i].Semantics, sem)
			}
		}
		if iv.Percentiles.Position.Status != institutional.PercentileAvailable {
			t.Errorf("%s POSITION percentile = %s (%s)", iv.Institution,
				iv.Percentiles.Position.Status, iv.Percentiles.Position.Reason)
		}
	}
	// The dealer's POSITION rises monotonically, so the newest is the top of its own set.
	dealer, _ := v.Institution(institutional.InstitutionDealer)
	if r, ok := dealer.Percentiles.Position.Value(); !ok || r != 1 {
		t.Errorf("dealer POSITION percentile = %v (ok=%v), want 1", r, ok)
	}
}

// TestAnEmptyViewIsRefused: no institutions is not an empty view, it is a caller with nothing to
// show, and a zero-valued view reads as three institutions holding nothing.
func TestAnEmptyViewIsRefused(t *testing.T) {
	if _, err := institutional.BuildInstitutionalDerivativesView("2026-09-04", nil,
		institutional.Policy{}); !errors.Is(err, institutional.ErrNoSeries) {
		t.Errorf("err = %v, want ErrNoSeries", err)
	}
}
