package institutional_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// The three-institution total: available only when all three are, PARTIAL with the value ABSENT
// otherwise, and an error when the caller mixed identities rather than lost one.

// liveView builds one institution's 2026-09-04 view against a 2026-09-03 baseline.
func liveView(t *testing.T, inst string, flow, position, prevPosition *float64) institutional.InstitutionView {
	t.Helper()
	var history []institutional.Observation
	if prevPosition != nil {
		history = append(history, observationFor(t, "2026-09-03", institutional.ScopeTX, inst,
			institutional.StatusAvailable, fptr(0), prevPosition))
	}
	current := observationFor(t, "2026-09-04", institutional.ScopeTX, inst,
		institutional.StatusAvailable, flow, position)
	v, err := institutional.BuildInstitutionView(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// the live 2026-09-04 session, §10.4's table.
func liveViews(t *testing.T) []institutional.InstitutionView {
	t.Helper()
	return []institutional.InstitutionView{
		liveView(t, institutional.InstitutionDealer, fptr(-1856), fptr(-697), fptr(-1697)),
		liveView(t, institutional.InstitutionTrust, fptr(1785), fptr(76174), fptr(75174)),
		liveView(t, institutional.InstitutionForeign, fptr(-880), fptr(-82389), fptr(-83389)),
	}
}

func TestThreeInstitutionTotalOnTheLiveSession(t *testing.T) {
	total, err := institutional.AggregateInstitutions(liveViews(t))
	if err != nil {
		t.Fatal(err)
	}
	if total.Status != institutional.StatusAvailable {
		t.Fatalf("status = %s (%s), want AVAILABLE", total.Status, total.Reason)
	}
	if got := lotsOrFail(t, total.Flow.ObservedMetric, "total FLOW"); got != -1856+1785-880 {
		t.Errorf("total FLOW = %v, want %v", got, -1856+1785-880)
	}
	if got := lotsOrFail(t, total.Position.ObservedMetric, "total POSITION"); got != -697+76174-82389 {
		t.Errorf("total POSITION = %v, want %v", got, -697+76174-82389)
	}
	if got := lotsOrFail(t, total.Change.ObservedMetric, "total CHANGE"); got != 3000 {
		t.Errorf("total CHANGE = %v, want 3000 (each institution moved +1,000)", got)
	}
	if total.ChangeBaselineDate != "2026-09-03" {
		t.Errorf("ChangeBaselineDate = %q", total.ChangeBaselineDate)
	}
	// FLOW and POSITION totals are still different numbers, for the same reason as the rows.
	fv, _ := total.Flow.Lots()
	pv, _ := total.Position.Lots()
	if fv == pv {
		t.Error("the FLOW and POSITION totals are the same number")
	}
	if len(total.Institutions) != 3 || len(total.Missing) != 0 {
		t.Errorf("institutions %v, missing %v", total.Institutions, total.Missing)
	}
}

// TestAMissingInstitutionMakesThePartialTotalValueless — §10.4: "外資 being present is not
// grounds for calling the total available."
func TestAMissingInstitutionMakesThePartialTotalValueless(t *testing.T) {
	views := liveViews(t)
	partial := []institutional.InstitutionView{views[0], views[2]} // no 投信

	total, err := institutional.AggregateInstitutions(partial)
	if err != nil {
		t.Fatal(err)
	}
	if total.Status != institutional.StatusPartial {
		t.Fatalf("status = %s, want PARTIAL", total.Status)
	}
	mustAbsent(t, total.Flow.ObservedMetric, institutional.StatusPartial, "total FLOW")
	mustAbsent(t, total.Position.ObservedMetric, institutional.StatusPartial, "total POSITION")
	mustAbsent(t, total.Change.ObservedMetric, institutional.StatusPartial, "total CHANGE")
	if len(total.Missing) != 1 || total.Missing[0] != institutional.InstitutionTrust {
		t.Errorf("Missing = %v, want 投信 named", total.Missing)
	}
	if !strings.Contains(total.Reason, institutional.InstitutionTrust) {
		t.Errorf("reason %q does not name the missing institution", total.Reason)
	}
	// −697 + −82,389 = −83,086 is the number a two-institution sum would have produced, and
	// it is not a market total.
	if v, ok := total.Position.Lots(); ok {
		t.Fatalf("a two-institution total published %v", v)
	}
}

// TestOneAbsentComponentMakesOnlyThatSemanticsPartial — the aggregate is PARTIAL overall, and
// the semantics that IS complete keeps its number, because §6's PARTIAL means "the present ones
// are usable".
func TestOneAbsentComponentMakesOnlyThatSemanticsPartial(t *testing.T) {
	views := liveViews(t)
	views[1] = liveView(t, institutional.InstitutionTrust, fptr(1785), nil, fptr(75174))

	total, err := institutional.AggregateInstitutions(views)
	if err != nil {
		t.Fatal(err)
	}
	if total.Status != institutional.StatusPartial {
		t.Fatalf("status = %s, want PARTIAL", total.Status)
	}
	if got := lotsOrFail(t, total.Flow.ObservedMetric, "total FLOW"); got != -1856+1785-880 {
		t.Errorf("total FLOW = %v", got)
	}
	mustAbsent(t, total.Position.ObservedMetric, institutional.StatusPartial, "total POSITION")
	mustAbsent(t, total.Change.ObservedMetric, institutional.StatusPartial, "total CHANGE")
	if !strings.Contains(total.Position.Reason, institutional.InstitutionTrust) {
		t.Errorf("reason %q does not name the institution with no POSITION", total.Position.Reason)
	}
}

// TestChangeTotalsRequireOneSharedBaseline — three differences taken over three different
// windows do not add up to a difference of the total, and nothing downstream could tell.
func TestChangeTotalsRequireOneSharedBaseline(t *testing.T) {
	views := liveViews(t)
	// 投信's previous valid observation is two days further back.
	trustHistory := []institutional.Observation{
		observationFor(t, "2026-09-01", institutional.ScopeTX, institutional.InstitutionTrust,
			institutional.StatusAvailable, fptr(0), fptr(75174)),
		observationFor(t, "2026-09-03", institutional.ScopeTX, institutional.InstitutionTrust,
			institutional.StatusNotPublished, nil, nil),
	}
	trustCurrent := observationFor(t, "2026-09-04", institutional.ScopeTX,
		institutional.InstitutionTrust, institutional.StatusAvailable, fptr(1785), fptr(76174))
	v, err := institutional.BuildInstitutionView(trustCurrent, trustHistory, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	views[1] = v

	total, err := institutional.AggregateInstitutions(views)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, total.Position.ObservedMetric, "total POSITION"); got != -697+76174-82389 {
		t.Errorf("total POSITION = %v — POSITION does not depend on a baseline", got)
	}
	mustAbsent(t, total.Change.ObservedMetric, institutional.StatusPartial, "total CHANGE")
	if !strings.Contains(total.Change.Reason, "2026-09-01") {
		t.Errorf("reason %q does not name the differing baselines", total.Change.Reason)
	}
	if total.ChangeBaselineDate != "" {
		t.Errorf("ChangeBaselineDate = %q with two different baselines", total.ChangeBaselineDate)
	}
}

// TestMixedIdentitiesAreErrorsNotCaveats — a caller that combined two dates, two scopes, two
// revisions or the same institution twice has no correct answer available, so nothing is
// returned. §10.4 requires the same trading date, session, dataset, instrument scope and
// revision view.
func TestMixedIdentitiesAreErrorsNotCaveats(t *testing.T) {
	base := liveViews(t)

	t.Run("scope", func(t *testing.T) {
		views := liveViews(t)
		mtxCurrent := observationFor(t, "2026-09-04", institutional.ScopeMTX,
			institutional.InstitutionForeign, institutional.StatusAvailable, fptr(3044), fptr(6255))
		v, err := institutional.BuildInstitutionView(mtxCurrent, nil, institutional.DefaultPolicy())
		if err != nil {
			t.Fatal(err)
		}
		views[2] = v
		if _, err := institutional.AggregateInstitutions(views); !errors.Is(err, institutional.ErrScopeMismatch) {
			t.Errorf("err = %v, want ErrScopeMismatch — TX lots and MTX lots do not add", err)
		}
	})

	t.Run("duplicate institution", func(t *testing.T) {
		views := []institutional.InstitutionView{base[0], base[0], base[1]}
		if _, err := institutional.AggregateInstitutions(views); !errors.Is(err, institutional.ErrDuplicateObservation) {
			t.Errorf("err = %v, want ErrDuplicateObservation", err)
		}
	})

	t.Run("trading date", func(t *testing.T) {
		views := liveViews(t)
		other := observationFor(t, "2026-09-03", institutional.ScopeTX,
			institutional.InstitutionForeign, institutional.StatusAvailable, fptr(-880), fptr(-82389))
		v, err := institutional.BuildInstitutionView(other, nil, institutional.DefaultPolicy())
		if err != nil {
			t.Fatal(err)
		}
		views[2] = v
		if _, err := institutional.AggregateInstitutions(views); !errors.Is(err, institutional.ErrDateMismatch) {
			t.Errorf("err = %v, want ErrDateMismatch", err)
		}
	})

	t.Run("revision", func(t *testing.T) {
		views := liveViews(t)
		views[2].Revision = 1
		if _, err := institutional.AggregateInstitutions(views); !errors.Is(err, institutional.ErrRevisionMismatch) {
			t.Errorf("err = %v, want ErrRevisionMismatch — two revisions are not one "+
				"point-in-time view", err)
		}
	})

	t.Run("unknown institution", func(t *testing.T) {
		views := liveViews(t)
		views[2].Institution = "散戶"
		if _, err := institutional.AggregateInstitutions(views); !errors.Is(err, institutional.ErrUnknownInstitution) {
			t.Errorf("err = %v, want ErrUnknownInstitution", err)
		}
	})

	t.Run("no views", func(t *testing.T) {
		if _, err := institutional.AggregateInstitutions(nil); err == nil {
			t.Error("an empty aggregate was accepted")
		}
	})
}
