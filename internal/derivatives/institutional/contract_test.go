package institutional_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// The contract with the rest of R15: the status vocabulary, the semantics names, the leaf
// property, and the live numbers §10.4 is written around.

// TestSourceStatusVocabularyMatchesTheAcquisitionLayer pins the duplication.
//
// internal/derivatives/institutional deliberately does not import internal/derivatives — that
// package owns persistence, and importing it would give a pure domain layer a transitive
// dependency on database/sql. The price is that §6's eight statuses are spelled in two places,
// and this test is what stops the two spellings drifting: a status added to either side without
// the other fails here rather than at whatever a switch's default happens to be.
func TestSourceStatusVocabularyMatchesTheAcquisitionLayer(t *testing.T) {
	if len(derivatives.Statuses) != len(institutional.SourceStatuses) {
		t.Fatalf("derivatives has %d statuses, institutional has %d source statuses",
			len(derivatives.Statuses), len(institutional.SourceStatuses))
	}
	for i, want := range derivatives.Statuses {
		got := institutional.SourceStatuses[i]
		if string(got) != string(want) {
			t.Errorf("status %d: institutional says %q, derivatives says %q", i, got, want)
		}
		converted, err := institutional.SourceStatus(string(want))
		if err != nil {
			t.Errorf("SourceStatus(%q): %v", want, err)
			continue
		}
		if converted != got {
			t.Errorf("SourceStatus(%q) = %q, want %q", want, converted, got)
		}
	}
	// And the converse: a derived status is not a fetch outcome and may not be written back
	// to a data-health record (§6.1 — merging the two vocabularies lets a caller treat a
	// risk state as a fetch failure).
	for _, d := range institutional.DerivedStatuses {
		if _, err := institutional.SourceStatus(string(d)); err == nil {
			t.Errorf("SourceStatus(%q) succeeded; a derived status is not a fetch outcome", d)
		}
		if d.IsSourceStatus() {
			t.Errorf("%q claims to be a source status", d)
		}
		if !d.IsDerived() {
			t.Errorf("%q does not claim to be derived", d)
		}
	}
}

// TestSemanticsAndSessionMatchTheDecoder pins the other shared spellings: M3 writes FLOW /
// POSITION into the rows this package reads, and derivatives.SessionCombined is what the
// snapshot table stores.
func TestSemanticsAndSessionMatchTheDecoder(t *testing.T) {
	if string(institutional.SemanticsFlow) != provider.SemanticsFlow {
		t.Errorf("FLOW: %q vs decoder's %q", institutional.SemanticsFlow, provider.SemanticsFlow)
	}
	if string(institutional.SemanticsPosition) != provider.SemanticsPosition {
		t.Errorf("POSITION: %q vs decoder's %q", institutional.SemanticsPosition, provider.SemanticsPosition)
	}
	if institutional.SessionCombined != derivatives.SessionCombined {
		t.Errorf("session: %q vs snapshot's %q", institutional.SessionCombined, derivatives.SessionCombined)
	}
	for i, want := range []string{
		provider.InstitutionDealer, provider.InstitutionTrust, provider.InstitutionForeign,
	} {
		if institutional.RequiredInstitutions[i] != want {
			t.Errorf("institution %d: %q, want %q", i, institutional.RequiredInstitutions[i], want)
		}
	}
}

// TestThePureLayerStaysPure is the reason the duplication above is acceptable.
//
// M4 is the domain layer: the persistence-backed history and the percentile distributions are a
// separate work item, and this package must remain computable from a slice in memory. `go list
// -deps` is what enforces that, rather than the package comment — internal/derivatives pulls in
// modernc.org/sqlite through internal/store, and one convenience import would put a SQL driver
// behind every pure function here.
func TestThePureLayerStaysPure(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "./internal/derivatives/institutional")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	forbidden := []string{
		"database/sql",
		"net/http",
		"github.com/deep-huang/stock-scanner/internal/store",
		"github.com/deep-huang/stock-scanner/internal/derivatives",
		"github.com/deep-huang/stock-scanner/internal/derivatives/acquire",
	}
	deps := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}
	for _, pkg := range forbidden {
		if deps[pkg] {
			t.Errorf("internal/derivatives/institutional depends on %s — M4 is a pure domain "+
				"layer and this is how it stops being one", pkg)
		}
	}
}

// TestLiveSessionFlowAndPositionAreDifferentNumbers is §10.4's table, computed rather than
// quoted.
//
// | institution | FLOW net | POSITION net |
// | 自營商      |   −1,856 |         −697 |
// | 投信        |   +1,785 |      +76,174 |
// | 外資及陸資  |     −880 |      −82,389 |
//
// 投信 traded +1,785 lots while holding +76,174 — a factor of 43. Any implementation that lets
// one stand in for the other fails on the second column of the second row.
func TestLiveSessionFlowAndPositionAreDifferentNumbers(t *testing.T) {
	decoded := loadFixture(t)
	if decoded.TradingDate != "2026-09-04" {
		t.Fatalf("fixture trading date = %q", decoded.TradingDate)
	}

	want := []struct {
		institution        string
		flow, position     float64
		mtxPositionForTest float64
	}{
		{institutional.InstitutionDealer, -1856, -697, -2959},
		{institutional.InstitutionTrust, 1785, 76174, -50},
		{institutional.InstitutionForeign, -880, -82389, 6255},
	}
	for _, w := range want {
		t.Run(w.institution, func(t *testing.T) {
			obs, err := institutional.NewObservation(institutional.ObservationRequest{
				TradingDate:  decoded.TradingDate,
				Session:      institutional.SessionCombined,
				Dataset:      decoded.Source,
				Institution:  w.institution,
				Scope:        institutional.ScopeTX,
				SourceStatus: institutional.StatusAvailable,
			}, decoded.Rows)
			if err != nil {
				t.Fatal(err)
			}
			flow := lotsOrFail(t, obs.Flow().ObservedMetric, "FLOW")
			position := lotsOrFail(t, obs.Position().ObservedMetric, "POSITION")
			if flow != w.flow {
				t.Errorf("FLOW = %v, want %v (TradingVolume(Net))", flow, w.flow)
			}
			if position != w.position {
				t.Errorf("POSITION = %v, want %v (OpenInterest(Net))", position, w.position)
			}
			if flow == position {
				t.Errorf("FLOW and POSITION are the same number (%v); one is standing in "+
					"for the other", flow)
			}
			if obs.Flow().Semantics != institutional.SemanticsFlow {
				t.Errorf("FLOW carries semantics %q", obs.Flow().Semantics)
			}
			if obs.Position().Semantics != institutional.SemanticsPosition {
				t.Errorf("POSITION carries semantics %q", obs.Position().Semantics)
			}

			// The same response, same institution, different instrument. 小型臺指期貨 is
			// a quarter-sized contract; its lots are not TX lots and the two must never
			// end up in one number.
			mtx, err := institutional.NewObservation(institutional.ObservationRequest{
				TradingDate:  decoded.TradingDate,
				Session:      institutional.SessionCombined,
				Dataset:      decoded.Source,
				Institution:  w.institution,
				Scope:        institutional.ScopeMTX,
				SourceStatus: institutional.StatusAvailable,
			}, decoded.Rows)
			if err != nil {
				t.Fatal(err)
			}
			gotMTX := lotsOrFail(t, mtx.Position().ObservedMetric, "MTX POSITION")
			if gotMTX != w.mtxPositionForTest {
				t.Errorf("MTX POSITION = %v, want %v", gotMTX, w.mtxPositionForTest)
			}
			if position == w.position+w.mtxPositionForTest {
				t.Errorf("TX POSITION equals TX+MTX (%v); the scopes were summed", position)
			}
		})
	}
}

// TestNetIsTakenFromTheFeedsOwnColumn — never derived from long − short.
//
// The fixture's LONG and SHORT rows are real; the check is that removing the NET row produces
// an ABSENT value rather than long − short, which would be a plausible number computed from a
// column layout nobody verified (§3.1, and internal/market/provider's existing rule).
func TestNetIsTakenFromTheFeedsOwnColumn(t *testing.T) {
	decoded := loadFixture(t)
	var kept []provider.InstitutionalRow
	for _, r := range decoded.Rows {
		if r.Product == institutional.ProductTX &&
			r.Institution == institutional.InstitutionForeign &&
			r.Semantics == provider.SemanticsPosition && r.Side == provider.SideNet {
			continue
		}
		kept = append(kept, r)
	}
	obs, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  decoded.TradingDate,
		Session:      institutional.SessionCombined,
		Dataset:      decoded.Source,
		Institution:  institutional.InstitutionForeign,
		Scope:        institutional.ScopeTX,
		SourceStatus: institutional.StatusAvailable,
	}, kept)
	if err != nil {
		t.Fatal(err)
	}
	mustAbsent(t, obs.Position().ObservedMetric, institutional.StatusPartial, "POSITION with no NET row")
	// 8,153 − 90,542 = −82,389 is the right answer and the wrong method: it would agree with
	// the feed today and disagree silently the day the columns move.
	if v, ok := obs.Position().Lots(); ok {
		t.Fatalf("POSITION was derived as %v with no NET column present", v)
	}
	// FLOW is untouched and still there — the absence is per-column, not per-row.
	if got := lotsOrFail(t, obs.Flow().ObservedMetric, "FLOW"); got != -880 {
		t.Errorf("FLOW = %v, want -880", got)
	}
}
