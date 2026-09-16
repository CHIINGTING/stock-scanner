package options_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// ramp builds n days whose put open interest rises by 100 a day against a fixed call side, so
// the ratio is strictly increasing and every rank is predictable.
func ramp(t *testing.T, n int) []options.Observation {
	t.Helper()
	days := make([]day, 0, n)
	for i := 1; i <= n; i++ {
		days = append(days, day{
			date: fmt.Sprintf("2026-06-%02d", i), expiry: "202609",
			callOI: fptr(1000), putOI: fptr(float64(1000 + i*100)),
		})
	}
	return buildSeries(t, options.FamilyMonthly, days)
}

// TestTheReadingIsRankedAgainstItsOwnHistory, with M4's window, minimum, tie rule and scale.
func TestTheReadingIsRankedAgainstItsOwnHistory(t *testing.T) {
	obs := ramp(t, 10)
	current, history := split(obs)

	rank, dist, err := options.Percentile(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if rank.Status != options.PercentileAvailable {
		t.Fatalf("status = %s (%s)", rank.Status, rank.Reason)
	}
	if dist.Size() != 10 {
		t.Errorf("the distribution holds %d values, want 10 including the reading itself",
			dist.Size())
	}
	v, _ := rank.Value()
	if v != 1 {
		t.Errorf("the highest reading ranks %v, want 1.0", v)
	}
	if rank.Scale != institutional.PercentileScale || rank.TieRule != institutional.PercentileTieRule {
		t.Errorf("scale %q / tie rule %q are not M4's", rank.Scale, rank.TieRule)
	}
	if !rank.IncludesCurrent {
		t.Error("the reading is not in its own distribution, so the two percentile " +
			"implementations in this repository disagree about the same number")
	}
	if rank.LookbackObservations != institutional.DefaultPercentileLookbackObservations {
		t.Errorf("lookback = %d, want M4's %d", rank.LookbackObservations,
			institutional.DefaultPercentileLookbackObservations)
	}
}

// TestTiesCountAtOrBelow — the rule is M4's function, and this pins the consequence: a reading
// exactly at its own historical median reports at or above 50%, not below.
func TestTiesCountAtOrBelow(t *testing.T) {
	// Five days, three of them identical at 1.00 and the reading one of them.
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-06-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(500)},  // 0.5
		{date: "2026-06-02", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)}, // 1.0
		{date: "2026-06-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)}, // 1.0
		{date: "2026-06-04", expiry: "202609", callOI: fptr(1000), putOI: fptr(2000)}, // 2.0
		{date: "2026-06-05", expiry: "202609", callOI: fptr(1000), putOI: fptr(1000)}, // 1.0
	})
	current, history := split(obs)
	rank, _, err := options.Percentile(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := rank.Value()
	if !ok {
		t.Fatalf("no percentile: %s", rank.Reason)
	}
	// Four of the five values are at or below 1.00.
	if v != 0.8 {
		t.Errorf("percentile = %v, want 0.8 — with `<` instead of `<=` this is 0.2 and a "+
			"reading at its own median reports as unusually low", v)
	}
	// And the same numbers through M4's own function, so the two can never drift.
	if got, ok := institutional.PercentileRatioAtOrBelow([]float64{0.5, 1, 1, 1, 2}, 1); !ok || got != v {
		t.Errorf("M4's rule gives %v for the same inputs", got)
	}
}

// TestBelowTheMinimumThereIsNoPercentile.
func TestBelowTheMinimumThereIsNoPercentile(t *testing.T) {
	obs := ramp(t, 3)
	current, history := split(obs)
	p := policy()
	p.MinPercentileSample = 20

	rank, _, err := options.Percentile(options.MetricOIPCR, current, history, p)
	if err != nil {
		t.Fatal(err)
	}
	if rank.Ratio != nil {
		t.Fatalf("a percentile of %v was computed from %d points", *rank.Ratio, rank.SampleSize)
	}
	if rank.Status != options.PercentileInsufficientSample {
		t.Errorf("status = %s, want INSUFFICIENT_SAMPLE", rank.Status)
	}
	if rank.SampleSize != 3 || rank.RequiredSampleSize != 20 {
		t.Errorf("sample %d of %d — §10.4 requires BOTH to travel with the answer",
			rank.SampleSize, rank.RequiredSampleSize)
	}
	if rank.Display == "0" || rank.Display == "" {
		t.Errorf("display %q", rank.Display)
	}
}

// TestThePercentileCannotSeeTheFuture.
func TestThePercentileCannotSeeTheFuture(t *testing.T) {
	obs := ramp(t, 10)
	// Rank day 5 against a history that contains days 6-10.
	if _, _, err := options.Percentile(options.MetricOIPCR, obs[4], obs[5:], policy()); !errors.Is(err, options.ErrLookAhead) {
		t.Fatalf("a future observation in the reference set returned %v, want ErrLookAhead", err)
	}

	// And the ordinary case: ranked against days 1-5 only, the reading is the highest of
	// five and nothing from days 6-10 is in the distribution.
	rank, dist, err := options.Percentile(options.MetricOIPCR, obs[4], obs[:4], policy())
	if err != nil {
		t.Fatal(err)
	}
	if dist.Size() != 5 {
		t.Fatalf("the distribution holds %d values, want 5", dist.Size())
	}
	for _, v := range dist.Values {
		if v > 1.5 {
			t.Errorf("%v is in a distribution for a reading dated day 5; it belongs to a "+
				"session that had not happened", v)
		}
	}
	if v, _ := rank.Value(); v != 1 {
		t.Errorf("percentile = %v, want 1.0", v)
	}
}

// TestEveryCombinationGetsItsOwnDistribution is §10.8's four separations, each asserted the
// only way that survives a widened partition: mixing is an ERROR, not a bigger sample.
func TestEveryCombinationGetsItsOwnDistribution(t *testing.T) {
	monthly := ramp(t, 5)
	current, history := split(monthly)

	weekly := buildSeries(t, options.FamilyWeekly, []day{
		{date: "2026-06-01", expiry: "202609W2", callOI: fptr(1000), putOI: fptr(9000)},
	})
	night := []options.Observation{mustObserve(t, func() options.ObservationRequest {
		r := req(options.FamilyMonthly, provider.SessionAfterHours)
		r.TradingDate = "2026-06-01"
		return r
	}(), nil)}
	otherProduct := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-06-01", expiry: "202609", callOI: fptr(1), putOI: fptr(9)},
	})
	otherProduct[0].Key.Product = "TEO"

	for _, c := range []struct {
		name    string
		history []options.Observation
	}{
		{"weekly against monthly", append(append([]options.Observation{}, history...), weekly...)},
		{"night against day", append(append([]options.Observation{}, history...), night...)},
		{"another product", append(append([]options.Observation{}, history...), otherProduct...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := options.Percentile(options.MetricOIPCR, current, c.history, policy()); !errors.Is(err, options.ErrKeyMismatch) {
				t.Fatalf("mixing returned %v, want ErrKeyMismatch — a percentile over a "+
					"population the reading does not belong to reads as a conclusion", err)
			}
		})
	}

	// The fourth separation, metric against metric, cannot be expressed as a mixed slice:
	// the metric is chosen when the percentile is asked for, and each metric reads its own
	// ratio off the same observations. So it is asserted as two different answers over one
	// history, which is what the separation IS.
	volRank, volDist, err := options.Percentile(options.MetricVolumePCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	oiRank, oiDist, err := options.Percentile(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if volDist.Key.Same(oiDist.Key) {
		t.Fatal("the volume and open-interest distributions share a key, so one population " +
			"holds traded lots and held lots at once")
	}
	if len(volDist.Values) > 0 && len(oiDist.Values) > 0 && volDist.Values[0] == oiDist.Values[0] {
		t.Error("the two distributions hold the same values")
	}
	if volRank.Key.Metric == oiRank.Key.Metric {
		t.Error("the two ranks carry the same metric")
	}
	// The synthetic series carries a constant volume ratio, so the two answers differ.
	if v, ok := volRank.Value(); ok {
		if o, ok2 := oiRank.Value(); ok2 && v == o && volDist.Size() == oiDist.Size() {
			t.Log("both ranks are 1.0 here, which is legitimate; the KEY separation above is " +
				"what the rule is about")
		}
	}
}

// TestNonUsableDaysDoNotConsumeAWindowSlotAndAreNeverZeroFilled.
func TestNonUsableDaysDoNotConsumeAWindowSlotAndAreNeverZeroFilled(t *testing.T) {
	obs := buildSeries(t, options.FamilyMonthly, []day{
		{date: "2026-06-01", expiry: "202609", callOI: fptr(1000), putOI: fptr(1100)},
		{date: "2026-06-02", status: institutional.StatusNotPublished},
		{date: "2026-06-03", expiry: "202609", callOI: fptr(1000), putOI: fptr(1200)},
		// Usable, and this metric has no ratio on it: absent, never 0.
		{date: "2026-06-04", expiry: "202609", callOI: nil, putOI: fptr(1300)},
		{date: "2026-06-05", expiry: "202609", callOI: fptr(1000), putOI: fptr(1400)},
	})
	current, history := split(obs)
	rank, dist, err := options.Percentile(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if dist.Size() != 3 {
		t.Fatalf("the distribution holds %d values, want 3: 06-02 was not observed and 06-04 "+
			"has no ratio", dist.Size())
	}
	for _, v := range dist.Values {
		if v == 0 {
			t.Error("a 0 is in the distribution — an absent ratio was zero-filled, which " +
				"drags every percentile towards the middle of a population that never happened")
		}
	}
	reasons := map[string]string{}
	for _, e := range dist.Excluded {
		reasons[e.TradingDate] = e.Reason
	}
	if reasons["2026-06-02"] != options.ExcludedNotUsable {
		t.Errorf("06-02 excluded as %q, want %s", reasons["2026-06-02"], options.ExcludedNotUsable)
	}
	if reasons["2026-06-04"] != options.ExcludedRatioAbsent {
		t.Errorf("06-04 excluded as %q, want %s", reasons["2026-06-04"], options.ExcludedRatioAbsent)
	}
	if rank.ExcludedCount != 2 {
		t.Errorf("excluded count = %d, want 2 — a thin sample over a long window must be "+
			"visible as such", rank.ExcludedCount)
	}
}

// TestARankOfAnAbsentReadingIsNotAThinSample: two different facts, two different states.
func TestARankOfAnAbsentReadingIsNotAThinSample(t *testing.T) {
	obs := ramp(t, 6)
	current, history := split(obs)
	// The reading itself loses its ratio.
	blank := buildSeries(t, options.FamilyMonthly, []day{
		{date: current.TradingDate, expiry: "202609", callOI: nil, putOI: fptr(1000)},
	})[0]

	rank, _, err := options.Percentile(options.MetricOIPCR, blank, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if rank.Status != options.PercentileValueAbsent {
		t.Errorf("status = %s, want VALUE_ABSENT — a data gap today is not a history gap",
			rank.Status)
	}
	if rank.SampleSize < 5 {
		t.Errorf("sample size %d; the history is intact and should say so", rank.SampleSize)
	}
}

// TestTheMetricViewCarriesTheWholeAnswer.
func TestTheMetricViewCarriesTheWholeAnswer(t *testing.T) {
	obs := ramp(t, 8)
	current, history := split(obs)
	v, err := options.BuildMetricView(options.MetricOIPCR, current, history, policy())
	if err != nil {
		t.Fatal(err)
	}
	if !v.PCR.Ratio.Observed {
		t.Fatalf("no ratio on the view: %s", v.PCR.Reason)
	}
	if len(v.Changes) != len(institutional.DefaultHorizons) {
		t.Errorf("%d horizons on the view", len(v.Changes))
	}
	if v.Percentile.Status != options.PercentileAvailable {
		t.Errorf("percentile %s", v.Percentile.Status)
	}
	if v.Distribution.Size() == 0 {
		t.Error("the reference distribution was not carried, so the percentile cannot be " +
			"re-derived or disbelieved")
	}
	if _, ok := v.Change(1); !ok {
		t.Error("CHANGE_1OBS is missing from the view")
	}
}
