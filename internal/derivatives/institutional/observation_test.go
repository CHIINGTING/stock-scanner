package institutional_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// TestObservedZeroIsNotAbsence is the invariant the whole ObservedMetric type exists for.
//
// A dealer who ended the session exactly flat published a zero. A day whose OpenInterest(Net)
// column carried "-" published nothing. `value == 0` cannot tell them apart, so nothing in this
// package is allowed to need it.
func TestObservedZeroIsNotAbsence(t *testing.T) {
	zero := observation(t, "2026-09-04", fptr(0), fptr(0))
	absent := observation(t, "2026-09-04", nil, nil)

	for _, c := range []struct {
		name string
		m    institutional.ObservedMetric
	}{
		{"FLOW", zero.Flow().ObservedMetric},
		{"POSITION", zero.Position().ObservedMetric},
	} {
		if !c.m.Observed {
			t.Errorf("%s: an observed zero must be Observed", c.name)
		}
		v, ok := c.m.Lots()
		if !ok || v != 0 {
			t.Errorf("%s: Lots() = %v, %v; want 0, true", c.name, v, ok)
		}
		if !c.m.IsObservedZero() {
			t.Errorf("%s: IsObservedZero() = false for a published zero", c.name)
		}
		if c.m.Status != institutional.StatusAvailable {
			t.Errorf("%s: status = %s, want AVAILABLE", c.name, c.m.Status)
		}
		if c.m.Display != "0 lots" {
			t.Errorf("%s: display = %q, want %q", c.name, c.m.Display, "0 lots")
		}
	}

	for _, c := range []struct {
		name string
		m    institutional.ObservedMetric
	}{
		{"FLOW", absent.Flow().ObservedMetric},
		{"POSITION", absent.Position().ObservedMetric},
	} {
		mustAbsent(t, c.m, institutional.StatusPartial, c.name+" with no net column")
		if c.m.IsObservedZero() {
			t.Errorf("%s: an absent value claims to be an observed zero", c.name)
		}
		if !c.m.Absent() {
			t.Errorf("%s: Absent() = false for a value with no number", c.name)
		}
	}

	// And the pair is distinguishable without looking at a number at all.
	if zero.Flow().Observed == absent.Flow().Observed {
		t.Fatal("a published zero and an absent column are indistinguishable on Observed")
	}
}

// TestNonObservationStatusesTravelThrough — NO_SESSION, NOT_PUBLISHED, MISSING and ERROR each
// reach the metric unchanged. §6: collapsing NOT_PUBLISHED into MISSING would make a normal
// afternoon look broken, and neither is ever NEUTRAL or 0.
func TestNonObservationStatusesTravelThrough(t *testing.T) {
	for _, status := range []institutional.MetricStatus{
		institutional.StatusMissing,
		institutional.StatusStale,
		institutional.StatusError,
		institutional.StatusNotAvailable,
		institutional.StatusNoSession,
		institutional.StatusNotPublished,
	} {
		t.Run(string(status), func(t *testing.T) {
			o := observationFor(t, "2026-09-04", institutional.ScopeTX,
				institutional.InstitutionDealer, status, nil, nil)
			mustAbsent(t, o.Flow().ObservedMetric, status, "FLOW")
			mustAbsent(t, o.Position().ObservedMetric, status, "POSITION")
			if o.HasPosition() {
				t.Error("HasPosition() is true on a day with no observation")
			}
		})
	}
}

// TestAStatusSayingNothingWasObservedCannotCarryRows — a NO_SESSION snapshot with rows in it is
// a contradiction, and picking either half would produce a position on a day the exchange was
// shut.
func TestAStatusSayingNothingWasObservedCannotCarryRows(t *testing.T) {
	_, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusNoSession,
	}, netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer,
		fptr(-1856), fptr(-697)))
	if err == nil {
		t.Fatal("a NO_SESSION observation carrying rows was accepted")
	}
}

// TestScopesDoNotMix — three ways, because there are three places a mixture could enter.
func TestScopesDoNotMix(t *testing.T) {
	rows := append(
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer, fptr(-1856), fptr(-697)),
		netRows("2026-09-04", institutional.ProductMTX, institutional.InstitutionDealer, fptr(184), fptr(-2959))...,
	)
	rows = append(rows,
		netRows("2026-09-04", institutional.ProductMicroTX, institutional.InstitutionDealer, fptr(9), fptr(-11))...)

	tx, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, tx.Position().ObservedMetric, "TX POSITION"); got != -697 {
		t.Errorf("TX POSITION = %v, want -697 (-697 + -2959 + -11 = %v would be the sum)",
			got, -697+-2959+-11)
	}
	if got := lotsOrFail(t, tx.Flow().ObservedMetric, "TX FLOW"); got != -1856 {
		t.Errorf("TX FLOW = %v, want -1856", got)
	}

	// The coverage record names what was left out, so "we only counted TX" is visible
	// rather than implied by the number being right.
	cov := tx.Coverage
	if cov.RowsIncluded != 6 || cov.RowsExcluded != 12 || cov.RowsSeen != 18 {
		t.Errorf("coverage seen/included/excluded = %d/%d/%d, want 18/6/12",
			cov.RowsSeen, cov.RowsIncluded, cov.RowsExcluded)
	}
	if len(cov.ProductsSeen) != 3 {
		t.Errorf("ProductsSeen = %v, want all three products", cov.ProductsSeen)
	}
	var excludedProducts []string
	for _, e := range cov.ExcludedContracts {
		if e.Reason != institutional.ExcludedOutOfScope {
			t.Errorf("exclusion %+v: unexpected reason", e)
		}
		excludedProducts = append(excludedProducts, e.Product)
	}
	if len(excludedProducts) != 2 {
		t.Errorf("excluded contracts = %v, want the two out-of-scope products", excludedProducts)
	}

	// 2. A history in another scope is an error, never a silently skipped row.
	txSeries := observation(t, "2026-09-03", fptr(1), fptr(100))
	mtx := observationFor(t, "2026-09-03", institutional.ScopeMTX,
		institutional.InstitutionDealer, institutional.StatusAvailable, fptr(1), fptr(100))
	if _, err := institutional.ResolveBaseline(tx, []institutional.Observation{mtx}, 1,
		institutional.DefaultPolicy()); !errors.Is(err, institutional.ErrScopeMismatch) {
		t.Errorf("ResolveBaseline over mixed scopes: err = %v, want ErrScopeMismatch", err)
	}
	if _, err := institutional.ResolveBaseline(tx, []institutional.Observation{txSeries}, 1,
		institutional.DefaultPolicy()); err != nil {
		t.Errorf("ResolveBaseline over one scope: %v", err)
	}
}

// TestInvestorsDoNotMix — the same three places, for the institution dimension. 自營商 and 投信
// held positions on 2026-09-04 that differ in sign as well as magnitude.
func TestInvestorsDoNotMix(t *testing.T) {
	rows := append(
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer, fptr(-1856), fptr(-697)),
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionTrust, fptr(1785), fptr(76174))...,
	)
	rows = append(rows,
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionForeign, fptr(-880), fptr(-82389))...)

	for _, w := range []struct {
		institution string
		position    float64
	}{
		{institutional.InstitutionDealer, -697},
		{institutional.InstitutionTrust, 76174},
		{institutional.InstitutionForeign, -82389},
	} {
		o, err := institutional.NewObservation(institutional.ObservationRequest{
			TradingDate:  "2026-09-04",
			Session:      institutional.SessionCombined,
			Dataset:      dataset,
			Institution:  w.institution,
			Scope:        institutional.ScopeTX,
			SourceStatus: institutional.StatusAvailable,
		}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if got := lotsOrFail(t, o.Position().ObservedMetric, w.institution); got != w.position {
			t.Errorf("%s POSITION = %v, want %v (the three-institution sum is %v)",
				w.institution, got, w.position, -697+76174.0-82389)
		}
		for _, e := range o.Coverage.ExcludedContracts {
			if e.Reason != institutional.ExcludedOtherInstitution {
				t.Errorf("%s: exclusion %+v, want OTHER_INSTITUTION", w.institution, e)
			}
		}
	}

	dealer := observation(t, "2026-09-04", fptr(-1856), fptr(-697))
	trust := observationFor(t, "2026-09-03", institutional.ScopeTX,
		institutional.InstitutionTrust, institutional.StatusAvailable, fptr(1), fptr(1))
	if _, err := institutional.ResolveBaseline(dealer, []institutional.Observation{trust}, 1,
		institutional.DefaultPolicy()); !errors.Is(err, institutional.ErrInstitutionMismatch) {
		t.Errorf("ResolveBaseline over two institutions: err = %v, want ErrInstitutionMismatch", err)
	}
}

// TestSessionPolicyIsFixedByTheDataContract — COMBINED, because the feed has no session
// dimension. Never read from a clock, and never defaulted from a caller's wrong value.
func TestSessionPolicyIsFixedByTheDataContract(t *testing.T) {
	for _, session := range []string{"DAY", "AFTER_HOURS", "", "combined"} {
		_, err := institutional.NewObservation(institutional.ObservationRequest{
			TradingDate:  "2026-09-04",
			Session:      session,
			Dataset:      dataset,
			Institution:  institutional.InstitutionDealer,
			Scope:        institutional.ScopeTX,
			SourceStatus: institutional.StatusAvailable,
		}, nil)
		if !errors.Is(err, institutional.ErrSessionMismatch) {
			t.Errorf("session %q: err = %v, want ErrSessionMismatch", session, err)
		}
	}
	v, err := institutional.BuildInstitutionView(
		observation(t, "2026-09-04", fptr(-1856), fptr(-697)), nil, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if v.SessionPolicy != institutional.SessionPolicyFixedByContract {
		t.Errorf("SessionPolicy = %q", v.SessionPolicy)
	}
}

// TestDuplicateNetRowsAreAnError — two NET rows for one key have no correct resolution: adding
// them double-counts and choosing one is arbitrary.
func TestDuplicateNetRowsAreAnError(t *testing.T) {
	rows := append(
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer, fptr(-1856), fptr(-697)),
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer, fptr(-1), fptr(-2))...,
	)
	_, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, rows)
	if !errors.Is(err, institutional.ErrDuplicateObservation) {
		t.Fatalf("err = %v, want ErrDuplicateObservation", err)
	}
}

// TestRowsFromAnotherDateAreExcluded — a response carrying a stray date never contributes to
// today's observation. That is how a CHANGE acquires a baseline it did not measure.
func TestRowsFromAnotherDateAreExcluded(t *testing.T) {
	rows := append(
		netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer, fptr(-1856), fptr(-697)),
		netRows("2026-09-03", institutional.ProductTX, institutional.InstitutionDealer, fptr(99), fptr(99))...,
	)
	o, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, o.Position().ObservedMetric, "POSITION"); got != -697 {
		t.Errorf("POSITION = %v, want -697", got)
	}
	found := false
	for _, e := range o.Coverage.ExcludedContracts {
		if e.Reason == institutional.ExcludedOtherTradingDate && e.TradingDate == "2026-09-03" {
			found = true
		}
	}
	if !found {
		t.Errorf("the 2026-09-03 rows were dropped without being recorded: %+v",
			o.Coverage.ExcludedContracts)
	}
}

// TestAMissingContractIsMissingNotPartial — the fetch succeeded and this product was not in the
// response. "PARTIAL: the column was absent" would claim a row that never existed.
func TestAMissingContractIsMissingNotPartial(t *testing.T) {
	rows := netRows("2026-09-04", institutional.ProductTE, institutional.InstitutionDealer, fptr(-18), fptr(-3))
	o, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	mustAbsent(t, o.Position().ObservedMetric, institutional.StatusMissing, "POSITION")
	if !strings.Contains(o.Position().Reason, institutional.ProductTX) {
		t.Errorf("reason %q does not name the missing product", o.Position().Reason)
	}
	if o.Coverage.FoundScopeProduct() {
		t.Error("FoundScopeProduct() is true for a product that was not in the response")
	}
}

// TestTypedSemanticsCannotBeSwapped — the compiler does most of this; Valid() covers the value that
// arrived by deserialisation, where the type is gone and only the field survives.
func TestTypedSemanticsCannotBeSwapped(t *testing.T) {
	o := observation(t, "2026-09-04", fptr(-1856), fptr(-697))
	if err := o.Flow().Valid(); err != nil {
		t.Error(err)
	}
	if err := o.Position().Valid(); err != nil {
		t.Error(err)
	}
	// A POSITION metric stuffed into a FLOW container is caught.
	bad := institutional.TradingFlowContracts{ObservedMetric: o.Position().ObservedMetric}
	if err := bad.Valid(); err == nil {
		t.Error("a POSITION value inside a TradingFlowContracts passed Valid()")
	}
}

// TestSideRowsOtherThanNetAreIgnored — LONG and SHORT are present in every response and are
// never added into the value. The helper's LONG/SHORT are deliberately inconsistent with NET.
func TestSideRowsOtherThanNetAreIgnored(t *testing.T) {
	rows := netRows("2026-09-04", institutional.ProductTX, institutional.InstitutionDealer,
		fptr(-1856), fptr(-697))
	var sides []string
	for _, r := range rows {
		sides = append(sides, r.Side)
	}
	if len(sides) != 6 {
		t.Fatalf("expected six rows per feed row, got %d", len(sides))
	}
	o, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  "2026-09-04",
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  institutional.InstitutionDealer,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, o.Position().ObservedMetric, "POSITION"); got != -697 {
		t.Errorf("POSITION = %v; 33333 - 44444 = %v would mean long − short was used",
			got, 33333-44444)
	}
	_ = provider.SideNet
}
