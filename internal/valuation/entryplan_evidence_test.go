package valuation

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fundamental"
)

func TestBuildEntryPlanEvidenceProjectsOnlyPITBaseScenario(t *testing.T) {
	asOf := "2026-09-10"
	med, p25, p75, pe := 20.0, 15.0, 25.0, 10.0
	v := &Valuation{Status: Available, TrailingPE: &pe, TrailingDate: "2026-09-08",
		HistoricalPE: HistoricalPEStats{Status: Available, SampleCount: 60, Median: &med, P25: &p25, P75: &p75,
			Persistence: PEPersistent, Quality: QualityBlock{QualityMetrics: QualityMetrics{WindowSessions: 60}}}}
	eps := 5.0
	f := &fundamental.View{Source: "MOPS", ObservedAt: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
		Financials: &fundamental.Financials{Period: fundamental.Period{Year: 2026, Quarter: 2}, CumulativeEPS: &eps,
			NetIncome: 1, PublishedAt: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}}
	bars := []fetcher.Candle{{Date: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), Close: 100}}
	got := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, f)
	if got.Status != Available || got.BaseTarget == nil || *got.BaseTarget != 200 || got.Model != RuleHistoricalPercentile || got.Suitability != SuitabilitySuitable {
		t.Fatalf("projection = %+v, want available BASE=200 historical model suitable", got)
	}
}

// pitValuation is a valuation that, given a close on its TrailingDate, projects an AVAILABLE
// BASE target (20x the implied EPS) with a PERSISTENT 60-session P/E history.
func pitValuation(trailingDate string) *Valuation {
	pe, med, p25, p75 := 10.0, 20.0, 15.0, 25.0
	return &Valuation{Status: Available, TrailingPE: &pe, TrailingDate: trailingDate,
		HistoricalPE: HistoricalPEStats{Status: Available, SampleCount: 60, Median: &med, P25: &p25, P75: &p75,
			Persistence: PEPersistent, Quality: QualityBlock{QualityMetrics: QualityMetrics{WindowSessions: 60}}}}
}

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

// pitFiling is a positive-earnings filing observed on observed and published on published.
func pitFiling(observed, published time.Time) *fundamental.View {
	eps := 5.0
	return &fundamental.View{Source: "MOPS", ObservedAt: observed,
		Financials: &fundamental.Financials{Period: fundamental.Period{Year: 2026, Quarter: 2}, CumulativeEPS: &eps,
			NetIncome: 1, PublishedAt: published}}
}

// R8a. A valuation whose TrailingDate is after the analysis date is refused outright. The
// candles include a close ON that future TrailingDate (and on the analysis date), so a missing
// series cannot be what produces the refusal. The whole projection must equal the builder's
// initial "nothing projected" value: UNAVAILABLE, no BASE target, no model, suitability
// INSUFFICIENT_DATA — even though the filing and P/E history would otherwise classify.
//
// What this does NOT prove on its own: that the TrailingDate guard is the only thing keeping
// the future close out. The close loop's own `d <= asOf` check also refuses the 09-11 bar, so
// with the TrailingDate guard removed the target is STILL nil; the guard's removal is caught by
// Status / Model / Suitability, not by the target.
func TestBuildEntryPlanEvidenceRefusesAFutureTrailingDate(t *testing.T) {
	asOf := "2026-09-10"
	bars := []fetcher.Candle{{Date: day(8), Close: 100}, {Date: day(10), Close: 105}, {Date: day(11), Close: 110}}
	got := BuildEntryPlanEvidence(asOf, "2330", 105, bars, pitValuation("2026-09-11"), pitFiling(day(9), day(1)))
	want := EntryPlanEvidence{Status: Unavailable, AsOf: asOf, Suitability: SuitabilityInsufficientData}
	if got.BaseTarget != nil {
		t.Fatalf("future TrailingDate produced a BASE target %v: %+v", *got.BaseTarget, got)
	}
	if got != want {
		t.Fatalf("future TrailingDate was projected: got %+v, want the unprojected %+v", got, want)
	}
}

// R8b. The EPS base is the close ON the exchange's TrailingDate, exactly. The canonical rule is
// healthcheck.closeOn (internal/healthcheck/target.go:116-133), which matches `d == date` only
// and returns "no close" otherwise; ComputeTargetPrice then answers INSUFFICIENT_DATA with no
// scenario (internal/valuation/target_price.go:289-298) rather than substituting another day's
// close. Here a close exists BEFORE TrailingDate (09-05) and after it (09-09, 09-10) but not on
// it, so the projection must carry no target and INSUFFICIENT_DATA status.
//
// Control: the same fixture plus the 09-08 close does project a target, so the absence below is
// caused by the missing exact-date close and nothing else.
func TestBuildEntryPlanEvidenceRequiresTheCloseOnTheTrailingDate(t *testing.T) {
	asOf := "2026-09-10"
	v := pitValuation("2026-09-08")
	bars := []fetcher.Candle{{Date: day(5), Close: 90}, {Date: day(9), Close: 105}, {Date: day(10), Close: 110}}
	withExact := []fetcher.Candle{{Date: day(5), Close: 90}, {Date: day(8), Close: 100}, {Date: day(9), Close: 105}, {Date: day(10), Close: 110}}

	control := BuildEntryPlanEvidence(asOf, "2330", 110, withExact, v, nil)
	if control.Status != Available || control.BaseTarget == nil || *control.BaseTarget != 200 {
		t.Fatalf("anti-vacuity: the exact-date close did not project BASE=200: %+v", control)
	}

	got := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, nil)
	if got.BaseTarget != nil {
		t.Fatalf("no close on TrailingDate 2026-09-08, yet a BASE target %v was projected "+
			"(an earlier session's close was used as the EPS base): %+v", *got.BaseTarget, got)
	}
	if got.Status != InsufficientData {
		t.Fatalf("status = %q, want INSUFFICIENT_DATA (no close for the ratio's own session)", got.Status)
	}
}

// R8e. A filing that was OBSERVED by the analysis date but PUBLISHED after it must be treated as
// absent. Only PublishedAt is in the future here (ObservedAt 09-09 <= asOf 09-10), and the
// fixture is one where the filing MATTERS: with the same filing published in the past the
// verdict is SUITABLE, and without any filing it is not. The future-published result must
// equal the no-filing result exactly.
func TestBuildEntryPlanEvidenceTreatsAFuturePublishedFilingAsAbsent(t *testing.T) {
	asOf := "2026-09-10"
	v := pitValuation("2026-09-08")
	bars := []fetcher.Candle{{Date: day(8), Close: 100}}

	absent := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, nil)
	past := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, pitFiling(day(9), day(1)))
	if past.Suitability != SuitabilitySuitable || absent.Suitability == past.Suitability {
		t.Fatalf("anti-vacuity: the filing does not change the verdict (past-published %q, absent %q)",
			past.Suitability, absent.Suitability)
	}

	future := pitFiling(day(9), day(12))
	if f := future.ObservedAt.Format("2006-01-02"); f > asOf {
		t.Fatalf("fixture: ObservedAt %s must not be after asOf, or this is not a PublishedAt test", f)
	}
	got := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, future)
	if got.Suitability != absent.Suitability || got.Status != absent.Status || got.Model != absent.Model ||
		(got.BaseTarget == nil) != (absent.BaseTarget == nil) ||
		(got.BaseTarget != nil && *got.BaseTarget != *absent.BaseTarget) {
		t.Fatalf("a filing published 2026-09-12 was used on 2026-09-10: got %+v, want as if absent %+v", got, absent)
	}
}

// R8e2. The mirror of R8e: a filing PUBLISHED before the analysis date but OBSERVED (fetched into
// the archive) after it is also treated as absent, as the builder's doc comment promises for
// future-dated records. Only ObservedAt is in the future here (PublishedAt 09-01 <= asOf 09-10,
// ObservedAt 09-11). Control: the same filing observed on 09-09 changes the verdict to SUITABLE,
// while no filing gives a different verdict — so equality with the no-filing result is not vacuous.
func TestBuildEntryPlanEvidenceTreatsAFutureObservedFilingAsAbsent(t *testing.T) {
	asOf := "2026-09-10"
	v := pitValuation("2026-09-08")
	bars := []fetcher.Candle{{Date: day(8), Close: 100}}

	absent := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, nil)
	observedInTime := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, pitFiling(day(9), day(1)))
	if observedInTime.Suitability != SuitabilitySuitable || absent.Suitability == observedInTime.Suitability {
		t.Fatalf("anti-vacuity: the filing does not change the verdict (observed-in-time %q, absent %q)",
			observedInTime.Suitability, absent.Suitability)
	}

	future := pitFiling(day(11), day(1))
	if p := future.Financials.PublishedAt.Format("2006-01-02"); p > asOf {
		t.Fatalf("fixture: PublishedAt %s must not be after asOf, or this is not an ObservedAt test", p)
	}
	got := BuildEntryPlanEvidence(asOf, "2330", 110, bars, v, future)
	if got.Suitability != absent.Suitability || got.Status != absent.Status || got.Model != absent.Model ||
		(got.BaseTarget == nil) != (absent.BaseTarget == nil) ||
		(got.BaseTarget != nil && *got.BaseTarget != *absent.BaseTarget) {
		t.Fatalf("a filing observed 2026-09-11 was used on 2026-09-10: got %+v, want as if absent %+v", got, absent)
	}
}
