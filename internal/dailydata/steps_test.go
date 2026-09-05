package dailydata

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/institution"
)

func dayAt(date, now string) Day {
	return Day{Date: date, Now: at(now), Session: SessionClosed, Symbols: []string{"2330.TW"}}
}

// ── the sources that cannot target a past session ─────────────────────────────────────

// valuation and fundamental fetch "now" and archive under "now". Run for an older session
// they would collect TODAY's figures, file them under TODAY, and let the report claim the
// older date — a confident misattribution, produced by exactly the backfill run most likely
// to be attempted.
func TestBackdatedValuationAndFundamentalAreSkippedNotMisattributed(t *testing.T) {
	d := Deps{Symbols: []string{"2330.TW"}}
	past := dayAt("2026-09-04", "2026-09-06 15:00")

	for _, s := range []Step{ValuationStep(d), FundamentalStep(d)} {
		got := s.Run(context.Background(), past)
		if got.Status != StatusSkipped {
			t.Errorf("%s for a past session = %s, want SKIPPED", s.Name, got.Status)
		}
		if got.Available > 0 {
			t.Errorf("%s claimed to have acquired %d records for a past session",
				s.Name, got.Available)
		}
		if !strings.Contains(got.Reason, "2026-09-04") ||
			!strings.Contains(got.Reason, "2026-09-06") {
			t.Errorf("%s: the reason does not name both dates: %s", s.Name, got.Reason)
		}
		if !strings.Contains(got.Reason, "valuation-backfill") {
			t.Errorf("%s: the reason does not point at the tool that CAN do this", s.Name)
		}
	}
}

// For today they run normally — the skip must not disable the daily path it protects.
func TestTodayIsNotSkipped(t *testing.T) {
	today := at("2026-09-08 15:00").In(Taipei).Format(dateLayout)
	d := dayAt(today, "2026-09-08 15:00")
	if r, skip := refuseBackdated("valuation", d); skip {
		t.Fatalf("today was skipped: %+v", r)
	}
}

// ── the price step needs a bar FOR the session ────────────────────────────────────────

type seriesPrices struct{ dates []string }

func (p seriesPrices) Fetch(fetcher.StockInfo) (fetcher.StockData, error) {
	var cs []fetcher.Candle
	for _, x := range p.dates {
		t, _ := time.ParseInLocation(dateLayout, x, Taipei)
		cs = append(cs, fetcher.Candle{Date: t, Close: 100})
	}
	return fetcher.StockData{Candles: cs}, nil
}

// "The series is not stale" is not "that day traded". For any past date the first is
// trivially true, so the step had no discriminating power on exactly the runs that need it.
func TestPriceStepRequiresABarForTheSession(t *testing.T) {
	// The series runs past the target but has no bar on it.
	d := Deps{Symbols: []string{"2330.TW"},
		Prices: seriesPrices{dates: []string{"2026-10-08", "2026-10-12", "2026-10-13"}}}
	got := PriceStep(d).Run(context.Background(), dayAt("2026-10-09", "2026-10-14 15:00"))
	if got.Status == StatusComplete {
		t.Fatalf("a session with no bar was reported COMPLETE: %+v", got)
	}
	if got.Available != 0 {
		t.Fatalf("available = %d for a session with no bar", got.Available)
	}

	// A session the series covers is complete.
	ok := PriceStep(d).Run(context.Background(), dayAt("2026-10-12", "2026-10-14 15:00"))
	if ok.Status != StatusComplete {
		t.Fatalf("a covered session = %s (%s)", ok.Status, ok.Reason)
	}
}

// ── institution: "not published yet" is not a session fact ────────────────────────────

// institution.ErrNoData means BOTH "holiday" and "not published yet", and TWSE T86 typically
// appears between 16:00 and 18:00 while this job's cut-off is 14:00.
//
// Reporting that as NO_SESSION made overall() return COMPLETE and the process exit 0 — so on
// an ordinary trading day, run at its own designed time, the scheduler saw success and never
// came back. That is the single failure this report exists to prevent, and it was on the main
// path rather than an edge.
func TestInstitutionNotPublishedYetIsNotASessionFact(t *testing.T) {
	d := Deps{
		InstitutionDir: t.TempDir(),
		InstitutionProviders: func(time.Duration) []institution.Provider {
			return []institution.Provider{
				noDataProvider{"TWSE"}, noDataProvider{"TPEX"},
			}
		},
	}
	got := InstitutionStep(d).Run(context.Background(), dayAt("2026-09-08", "2026-09-08 14:05"))

	if got.Status == StatusNoSession {
		t.Fatalf("a CLOSED session with no institutional data was reported NO_SESSION; " +
			"that makes the whole run COMPLETE and the exit code 0")
	}
	if got.Status == StatusComplete {
		t.Fatalf("no data was reported COMPLETE: %+v", got)
	}
	if got.Status != StatusFailed {
		t.Fatalf("status = %s, want FAILED", got.Status)
	}
	if !strings.Contains(got.Reason, "16:00") {
		t.Errorf("the reason does not say when the data appears: %s", got.Reason)
	}
	if got.Note != "尚未公布" {
		t.Errorf("note = %q", got.Note)
	}
}

// noDataProvider answers as the exchanges do before publication: a real reply carrying
// nothing, not a transport failure.
type noDataProvider struct{ market string }

func (p noDataProvider) Market() string { return p.market }
func (p noDataProvider) Fetch(context.Context, string) (*institution.Snapshot, error) {
	return nil, institution.ErrNoData
}

// A genuinely successful acquisition is still COMPLETE — the fix must not make the step
// 永遠 fail.
func TestInstitutionSuccessIsComplete(t *testing.T) {
	dir := t.TempDir()
	d := Deps{
		InstitutionDir: dir,
		InstitutionProviders: func(time.Duration) []institution.Provider {
			return []institution.Provider{
				okProvider{"TWSE", institution.MarketTWSE},
				okProvider{"TPEX", institution.MarketTPEX},
			}
		},
	}
	got := InstitutionStep(d).Run(context.Background(), dayAt("2026-09-08", "2026-09-08 14:05"))
	if got.Status != StatusComplete {
		t.Fatalf("status = %s (%s), want COMPLETE", got.Status, got.Reason)
	}
	if got.Available != 2 {
		t.Fatalf("available = %d, want 2", got.Available)
	}
}

type okProvider struct{ name, market string }

func (p okProvider) Market() string { return p.name }
func (p okProvider) Fetch(_ context.Context, date string) (*institution.Snapshot, error) {
	return &institution.Snapshot{
		SchemaVersion: institution.SchemaVersion, Market: p.market, AsOfDate: date,
		Unit: institution.UnitShares, Source: "test", FetchedAt: time.Now(),
		Stocks: []institution.StockChip{{Code: "2330", Name: "台積電", Reconciled: true}},
	}, nil
}

// The report must fold that into something an operator acts on, not success.
func TestInstitutionFailureKeepsTheRunFromClaimingComplete(t *testing.T) {
	p := &Pipeline{Steps: []Step{
		{Name: "price", Run: func(context.Context, Day) SourceReport { return ok(14) }},
		{Name: "valuation", Run: func(context.Context, Day) SourceReport { return ok(14) }},
		{Name: "institution", Run: func(context.Context, Day) SourceReport {
			return SourceReport{Status: StatusFailed, Note: "尚未公布",
				Reason: "TWSE T86 通常 16:00–18:00 才公布"}
		}},
	}}
	rep := p.Run(context.Background(), dayAt("2026-09-08", "2026-09-08 14:05"))
	if rep.Status == StatusComplete {
		t.Fatalf("the run claimed COMPLETE while institution never arrived")
	}
	if rep.Status != StatusPartial {
		t.Fatalf("status = %s, want PARTIAL — rerunning this evening fixes it", rep.Status)
	}
	inst, _ := rep.Source("institution")
	if !strings.Contains(inst.Reason, "16:00") {
		t.Errorf("the reason does not tell the operator when to come back: %s", inst.Reason)
	}
}
