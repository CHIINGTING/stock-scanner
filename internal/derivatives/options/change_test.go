package options_test

import (
	"errors"
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// policy is M4's, with the percentile minimum lowered so a small synthetic series can produce
// one at all. Everything else — the horizons, both tolerances, the skip rules — is the
// shipped configuration, because M5 reuses it rather than having its own.
func policy() institutional.Policy {
	p := institutional.DefaultPolicy()
	p.MinPercentileSample = 3
	return p
}

// TestAChangeIsTheDifferenceOfTWORATIOS, over the same contract.
func TestAChangeIsTheDifferenceOfTWORATIOS(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)}, // 1.00
		{date: "2026-09-02", expiry: "202609", callOI: fptr(1000), putOI: fptr(1200)}, // 1.20
		{date: "2026-09-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1500)}, // 1.50
	})
	current, history := split(obs)

	hc, err := options.ChangeOver(options.MetricOIPCR, current, history, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := hc.Change.Value()
	if !ok {
		t.Fatalf("no change: %s (%s)", hc.Status, hc.Change.Reason)
	}
	if math.Abs(v-0.3) > 1e-12 {
		t.Errorf("CHANGE_1OBS = %v, want 0.30 (1.50 − 1.20)", v)
	}
	if hc.PreviousTradingDate != "2026-09-02" {
		t.Errorf("baseline %q", hc.PreviousTradingDate)
	}
	if hc.Rollover.Crossed {
		t.Error("a same-contract change reported a rollover")
	}
	if hc.Label != "PCR_CHANGE_1OBS" {
		t.Errorf("label %q — the suffix is OBS because a multi-day difference labelled 1D is "+
			"the defect the baseline rule exists to prevent", hc.Label)
	}

	// Two hops back, and it is NOT the sum of the numerators or anything else derived:
	// 1.50 − 1.00.
	hc3, err := options.ChangeOver(options.MetricOIPCR, current, history, 2, policy())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := hc3.Change.Value(); math.Abs(v-0.5) > 1e-12 {
		t.Errorf("CHANGE_2OBS = %v, want 0.50", v)
	}
}

// TestARolloverIsNotAChange, and the expiry-zeroing case §10.8 names explicitly.
func TestARolloverIsNotAChange(t *testing.T) {
	// The front monthly settles: 202609 leaves and 202610 arrives. The put/call ratio of
	// the new contract is not the old one's, and their difference is a number about nothing.
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-16", expiry: "202609", callOI: fptr(1000), putOI: fptr(2000)}, // 2.00
		{date: "2026-09-17", expiry: "202610", callOI: fptr(1000), putOI: fptr(1000)}, // 1.00
	})
	current, history := split(obs)

	hc, err := options.ChangeOver(options.MetricOIPCR, current, history, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	if hc.Change.Observed {
		v, _ := hc.Change.Value()
		t.Fatalf("a change of %v was published across a contract-identity change; the two "+
			"ratios describe different contracts", v)
	}
	if hc.Status != options.RatioRolloverBoundary {
		t.Errorf("status = %s, want ROLLOVER_BOUNDARY", hc.Status)
	}
	if !hc.Rollover.Crossed {
		t.Error("the rollover was not recorded")
	}
	if !contains(hc.Rollover.Removed, "202609") || !contains(hc.Rollover.Added, "202610") {
		t.Errorf("the rollover names added %v / removed %v, which does not say what happened",
			hc.Rollover.Added, hc.Rollover.Removed)
	}
	if !hasWarning(hc.Warnings, options.WarnRolloverBoundary) {
		t.Errorf("no %s warning: %+v", options.WarnRolloverBoundary, hc.Warnings)
	}

	// The baseline was still RESOLVED — the answer is "not a change", not "no history".
	if hc.Resolution.PreviousTradingDate != "2026-09-16" {
		t.Errorf("baseline %q", hc.Resolution.PreviousTradingDate)
	}
}

// TestExpiryZeroingIsNotACollapse is the same rule seen from M6's side.
//
// An expiry settles and its open interest goes to zero. Read as a difference that is a total
// collapse of put open interest, and it would happen on every settlement day — which, per
// §3.1, is not a monthly event: the F-series weeklies settle far more often and carry the
// larger share of open interest.
func TestExpiryZeroingIsNotACollapse(t *testing.T) {
	obs := buildSeries(t, options.FamilyWeekly, []day{
		{date: "2026-09-03", expiry: "202609F1", callOI: fptr(6000), putOI: fptr(11000)},
		// The F1 settles and its lots leave; F2 is what is left, with almost nothing in it.
		{date: "2026-09-04", expiry: "202609F2", callOI: fptr(10), putOI: fptr(1)},
	})
	current, history := split(obs)

	hc, err := options.ChangeOver(options.MetricOIPCR, current, history, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	if hc.Change.Observed {
		v, _ := hc.Change.Value()
		t.Fatalf("the settlement produced a change of %v — a wall that vanishes because its "+
			"contract expired is not a collapsing wall", v)
	}
	if hc.Status != options.RatioRolloverBoundary {
		t.Errorf("status = %s, want ROLLOVER_BOUNDARY", hc.Status)
	}
	// The metadata M6 will read has to be there, not merely the refusal.
	if len(hc.Rollover.Removed) == 0 || hc.Rollover.Rule != options.RolloverRule {
		t.Errorf("rollover metadata is incomplete: %+v", hc.Rollover)
	}
}

// TestTheHorizonIsNeverShortenedToFit.
func TestTheHorizonIsNeverShortenedToFit(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)},
		{date: "2026-09-02", expiry: "202609", callOI: fptr(1000), putOI: fptr(1100)},
		{date: "2026-09-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1200)},
	})
	current, history := split(obs)

	hcs, err := options.ChangeHorizons(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if len(hcs) != len(institutional.DefaultHorizons) {
		t.Fatalf("%d horizons, want %v", len(hcs), institutional.DefaultHorizons)
	}
	for _, hc := range hcs {
		switch hc.Horizon {
		case 1, 2:
			if !hc.Change.Observed {
				t.Errorf("%s is absent with two observations behind it", hc.Label)
			}
		default:
			if hc.Change.Observed {
				v, _ := hc.Change.Value()
				t.Errorf("%s produced %v from %d valid observations — a shorter span wearing "+
					"a longer label is the defect this state prevents", hc.Label, v,
					hc.EffectiveObservations)
			}
			if hc.Status != options.RatioInsufficientHistory {
				t.Errorf("%s status = %s, want INSUFFICIENT_HISTORY", hc.Label, hc.Status)
			}
			if hc.EffectiveObservations >= hc.Horizon {
				t.Errorf("%s claims %d effective observations", hc.Label, hc.EffectiveObservations)
			}
		}
	}
}

// TestAnAbsentRatioCannotAnchorAChange: a day whose ratio is absent is SKIPPED with a reason,
// and the numerators never stand in for it.
func TestAnAbsentRatioCannotAnchorAChange(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)},
		// The call side is absent: rows are there, the column is not.
		{date: "2026-09-02", expiry: "202609", callOI: nil, putOI: fptr(1100)},
		{date: "2026-09-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1400)},
	})
	current, history := split(obs)

	hc, err := options.ChangeOver(options.MetricOIPCR, current, history, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	if hc.PreviousTradingDate != "2026-09-01" {
		t.Fatalf("baseline %q, want 2026-09-01 — 09-02 has no ratio to anchor on",
			hc.PreviousTradingDate)
	}
	if v, _ := hc.Change.Value(); math.Abs(v-0.4) > 1e-12 {
		t.Errorf("change = %v, want 0.40", v)
	}
	skipped := hc.Resolution.SkippedDates
	if len(skipped) != 1 || skipped[0].Reason != options.SkipRatioAbsent {
		t.Errorf("skipped %+v, want 09-02 with reason %s — skipping is not silent",
			skipped, options.SkipRatioAbsent)
	}
	if hc.CalendarGapDays != 2 {
		t.Errorf("calendar gap = %d, want 2; a one-observation difference that spans two "+
			"days must say so", hc.CalendarGapDays)
	}
}

// TestNonUsableDaysAreSkippedWithMFoursReasons — the vocabulary is M4's, not a second one.
func TestNonUsableDaysAreSkippedWithMFoursReasons(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)},
		{date: "2026-09-02", status: institutional.StatusNotPublished},
		{date: "2026-09-03", status: institutional.StatusNoSession},
		{date: "2026-09-04", expiry: "202609", callOI: fptr(1000), putOI: fptr(1300)},
	})
	current, history := split(obs)

	res, err := options.ResolveBaseline(options.MetricOIPCR, current, history, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	if res.PreviousTradingDate != "2026-09-01" {
		t.Fatalf("baseline %q", res.PreviousTradingDate)
	}
	want := map[string]string{
		"2026-09-02": institutional.SkipNotPublished,
		"2026-09-03": institutional.SkipNoSession,
	}
	if len(res.SkippedDates) != 2 {
		t.Fatalf("skipped %+v", res.SkippedDates)
	}
	for _, s := range res.SkippedDates {
		if want[s.TradingDate] != s.Reason {
			t.Errorf("%s skipped for %q, want %q", s.TradingDate, s.Reason, want[s.TradingDate])
		}
	}
}

// TestTheToleranceIsMFoursAndScalesWithTheHorizon.
func TestTheToleranceIsMFoursAndScalesWithTheHorizon(t *testing.T) {
	p := policy()
	for n := 1; n <= 10; n++ {
		if got, want := p.ToleranceDays(n), p.MaxBaselineCalendarGapDays+(n-1)*p.CalendarDaysPerObservation; got != want {
			t.Fatalf("ToleranceDays(%d) = %d, want %d", n, got, want)
		}
	}

	// A baseline beyond the tolerance is STALE_BASELINE, not a number.
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-06-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)},
		{date: "2026-09-04", expiry: "202609", callOI: fptr(1000), putOI: fptr(1300)},
	})
	current, history := split(obs)
	hc, err := options.ChangeOver(options.MetricOIPCR, current, history, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Change.Observed {
		t.Fatal("a change was published across a 95-day gap")
	}
	if hc.Status != options.RatioStaleBaseline {
		t.Errorf("status = %s, want STALE_BASELINE", hc.Status)
	}
	if hc.Resolution.PreviousTradingDate == "" || hc.Resolution.CalendarGapDays == 0 {
		t.Error("the resolution dropped the baseline it found; \"we looked and the last " +
			"usable session was months ago\" is the useful part")
	}
}

// TestAChangeRefusesADifferentMeasurement — differencing a weekly against a monthly, or a
// DAY figure against an AFTER_HOURS one, is a caller error and not a status on a value.
func TestAChangeRefusesADifferentMeasurement(t *testing.T) {
	weekly := buildSeries(t, options.FamilyWeekly, []day{
		{date: "2026-09-03", expiry: "202609W2", callOI: fptr(100), putOI: fptr(100)},
	})
	monthly := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-04", expiry: "202609", callOI: fptr(100), putOI: fptr(150)},
	})
	if _, err := options.ChangeOver(options.MetricOIPCR, monthly[0], weekly, 1, policy()); !errors.Is(err, options.ErrKeyMismatch) {
		t.Fatalf("differencing a monthly against a weekly returned %v, want ErrKeyMismatch", err)
	}
}

// TestAChangeRefusesTheFuture: §5's bound, re-checked over the slice this package is handed.
func TestAChangeRefusesTheFuture(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-09-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)},
		{date: "2026-09-04", expiry: "202609", callOI: fptr(1000), putOI: fptr(1200)},
	})
	// The reading is the OLDER day and the "history" contains the newer one.
	if _, err := options.ChangeOver(options.MetricOIPCR, obs[0], obs[1:], 1, policy()); !errors.Is(err, options.ErrLookAhead) {
		t.Fatalf("a future observation in the history returned %v, want ErrLookAhead", err)
	}
}

// TestAChangeOverTheFixtureUsesRatiosNotNumerators is the volume side of the same rule, with
// the live figures underneath it: the numerators move by tens of thousands of lots while the
// ratio moves by hundredths, so a difference of numerators is unmistakable.
func TestAChangeOverTheFixtureUsesRatiosNotNumerators(t *testing.T) {
	rows := fixtureRows(t)
	todayReq := req(options.FamilyAll, provider.SessionCombined)
	today := mustObserve(t, todayReq, rows)

	yesterdayReq := todayReq
	yesterdayReq.TradingDate = "2026-09-03"
	scaled := make([]provider.OptionStrikeRow, len(rows))
	copy(scaled, rows)
	for i := range scaled {
		scaled[i].TradingDate = "2026-09-03"
		if scaled[i].Volume != nil {
			v := *scaled[i].Volume * 2
			scaled[i].Volume = &v
		}
	}
	yesterday := mustObserve(t, yesterdayReq, scaled)

	hc, err := options.ChangeOver(options.MetricVolumePCR, today,
		[]options.Observation{yesterday}, 1, policy())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := hc.Change.Value()
	if !ok {
		t.Fatalf("no change: %s", hc.Change.Reason)
	}
	// Doubling every volume leaves the RATIO untouched, and a difference of numerators
	// would be −63,014.
	if v != 0 {
		t.Errorf("change = %v, want 0: both sides doubled, so the ratio did not move", v)
	}
	if v == -allBothSessionsPutVol {
		t.Error("the change is a difference of NUMERATORS")
	}
}
