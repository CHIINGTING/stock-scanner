package institutional_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Shared fixtures. The live 2026-09-04 session is the reference throughout: it is the day §10.4
// is written around, and its three institutions differ between FLOW and POSITION by a factor of
// 43, which no invented fixture would have thought to do.

const dataset = provider.SourceInstitutionalFutures

func fptr(v float64) *float64 { return &v }

// netRows builds the six rows M3 emits for one (product, institution) feed row. Only the two
// NET rows carry the values under test; LONG and SHORT are present and DELIBERATELY
// inconsistent with NET, so any implementation that derives NET from long − short produces a
// different number and fails.
func netRows(date, product, institution string, flow, position *float64) []provider.InstitutionalRow {
	base := provider.InstitutionalRow{TradingDate: date, Product: product, Institution: institution}
	mk := func(sem, side string, lots *float64) provider.InstitutionalRow {
		r := base
		r.Semantics, r.Side, r.Lots = sem, side, lots
		return r
	}
	return []provider.InstitutionalRow{
		mk(provider.SemanticsFlow, provider.SideLong, fptr(11111)),
		mk(provider.SemanticsFlow, provider.SideShort, fptr(22222)),
		mk(provider.SemanticsFlow, provider.SideNet, flow),
		mk(provider.SemanticsPosition, provider.SideLong, fptr(33333)),
		mk(provider.SemanticsPosition, provider.SideShort, fptr(44444)),
		mk(provider.SemanticsPosition, provider.SideNet, position),
	}
}

// observation builds one AVAILABLE TX observation for a dealer, through the real constructor.
func observation(t *testing.T, date string, flow, position *float64) institutional.Observation {
	t.Helper()
	return observationFor(t, date, institutional.ScopeTX, institutional.InstitutionDealer,
		institutional.StatusAvailable, flow, position)
}

func observationFor(t *testing.T, date string, scope institutional.InstrumentScope,
	inst string, status institutional.MetricStatus, flow, position *float64,
) institutional.Observation {
	t.Helper()
	var rows []provider.InstitutionalRow
	if status.Usable() {
		rows = netRows(date, scope.Product, inst, flow, position)
	}
	o, err := institutional.NewObservation(institutional.ObservationRequest{
		TradingDate:  date,
		Session:      institutional.SessionCombined,
		Dataset:      dataset,
		Institution:  inst,
		Scope:        scope,
		SourceStatus: status,
		SnapshotID:   1,
		Revision:     0,
		FetchedAt:    time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC),
	}, rows)
	if err != nil {
		t.Fatalf("NewObservation(%s, %s, %s): %v", date, inst, scope, err)
	}
	return o
}

// noSession is a day the exchange did not trade: a status, not an empty snapshot and not zeros.
func noSession(t *testing.T, date string) institutional.Observation {
	t.Helper()
	return observationFor(t, date, institutional.ScopeTX, institutional.InstitutionDealer,
		institutional.StatusNoSession, nil, nil)
}

func notPublished(t *testing.T, date string) institutional.Observation {
	t.Helper()
	return observationFor(t, date, institutional.ScopeTX, institutional.InstitutionDealer,
		institutional.StatusNotPublished, nil, nil)
}

// series builds consecutive AVAILABLE observations, one per supplied date, with the given
// POSITION values and a FLOW that deliberately moves the OTHER way — so anything that
// differences FLOW instead of POSITION gets a wrong sign as well as a wrong number.
func series(t *testing.T, dates []string, positions []float64) []institutional.Observation {
	t.Helper()
	if len(dates) != len(positions) {
		t.Fatalf("series: %d dates and %d positions", len(dates), len(positions))
	}
	out := make([]institutional.Observation, 0, len(dates))
	for i, d := range dates {
		out = append(out, observation(t, d, fptr(-positions[i]), fptr(positions[i])))
	}
	return out
}

// loadFixture decodes the archived live response.
func loadFixture(t *testing.T) *provider.Institutional {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "testdata", "institutional_futures_20260904.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := provider.ParseInstitutionalFutures(body)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return got
}

func lotsOrFail(t *testing.T, m institutional.ObservedMetric, what string) float64 {
	t.Helper()
	v, ok := m.Lots()
	if !ok {
		t.Fatalf("%s: expected an observed value, got status=%s reason=%q display=%q",
			what, m.Status, m.Reason, m.Display)
	}
	return v
}

func mustAbsent(t *testing.T, m institutional.ObservedMetric, want institutional.MetricStatus, what string) {
	t.Helper()
	if m.Observed {
		t.Fatalf("%s: expected no value, got %v (status %s)", what, *m.Value, m.Status)
	}
	if m.Value != nil {
		t.Fatalf("%s: Observed is false but Value is non-nil (%v)", what, *m.Value)
	}
	if m.Status != want {
		t.Fatalf("%s: status = %s, want %s (reason %q)", what, m.Status, want, m.Reason)
	}
	if m.Display == "0" || m.Display == "0 lots" || m.Display == "-" || m.Display == "" {
		t.Fatalf("%s: an absent value rendered as %q — R14 spent a milestone on exactly this",
			what, m.Display)
	}
}
