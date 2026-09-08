package institutional_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// The percentile contract: three distributions that never touch, a reference set bounded by the
// reading's own date, and an INSUFFICIENT_SAMPLE that is a status rather than a number.

// dates builds n consecutive weekday-ish dates ending at the given day. They are plain calendar
// days — the baseline tolerance is 10 days by default, so consecutive days never trip it.
func consecutiveDates(t *testing.T, start int, n int) []string {
	t.Helper()
	if start-n+1 < 1 {
		t.Fatalf("consecutiveDates: %d dates ending at day %d runs off the month", n, start)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("2026-06-%02d", start-n+1+i))
	}
	return out
}

// longSeries builds `n` AVAILABLE consecutive observations whose POSITION rises by 100 a day
// and whose FLOW is the NEGATIVE of it — so anything that ranks FLOW against POSITION's
// distribution lands at the opposite end.
func longSeries(t *testing.T, n int) []institutional.Observation {
	t.Helper()
	ds := consecutiveDates(t, n, n)
	positions := make([]float64, n)
	for i := range positions {
		positions[i] = float64((i + 1) * 100)
	}
	return series(t, ds, positions)
}

// split returns the newest observation and everything before it.
func split(obs []institutional.Observation) (institutional.Observation, []institutional.Observation) {
	return obs[len(obs)-1], obs[:len(obs)-1]
}

func mustPercentiles(t *testing.T, cur institutional.Observation,
	hist []institutional.Observation, p institutional.Policy) institutional.PercentileSet {
	t.Helper()
	set, err := institutional.Percentiles(cur, hist, p)
	if err != nil {
		t.Fatalf("Percentiles: %v", err)
	}
	return set
}

func ratioOrFail(t *testing.T, r institutional.PercentileRank, what string) float64 {
	t.Helper()
	v, ok := r.Value()
	if !ok {
		t.Fatalf("%s: expected a percentile, got status=%s reason=%q", what, r.Status, r.Reason)
	}
	return v
}

// TestTheThreeDistributionsAreNeverShared is §10.4's rule made arithmetic.
//
// "FLOW, POSITION and CHANGE each get their own reference distribution; sharing one would rank
// a traded-lot figure against held-lot figures." The fixture makes the consequence visible:
// POSITION rises by 100 a day and FLOW is its negative, so the newest POSITION is the LARGEST
// value in its own distribution and the newest FLOW is the SMALLEST in its own. A shared
// distribution would put one of them in completely the wrong tail.
func TestTheThreeDistributionsAreNeverShared(t *testing.T) {
	obs := longSeries(t, 25)
	cur, hist := split(obs)
	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 5})

	flow := ratioOrFail(t, set.Flow, "FLOW percentile")
	pos := ratioOrFail(t, set.Position, "POSITION percentile")
	if pos != 1 {
		t.Errorf("POSITION percentile = %v, want 1: the newest position is the largest in its "+
			"own distribution", pos)
	}
	if flow >= 1 {
		t.Errorf("FLOW percentile = %v; the newest FLOW is the SMALLEST in its own "+
			"distribution, so a value of 1 means it was ranked against POSITION's", flow)
	}
	if math.Abs(flow-1.0/25.0) > 1e-9 {
		t.Errorf("FLOW percentile = %v, want %v (1 of 25, ties at-or-below)", flow, 1.0/25.0)
	}

	fd, ok := set.Distribution(institutional.SemanticsFlow)
	if !ok {
		t.Fatal("no FLOW distribution")
	}
	pd, ok := set.Distribution(institutional.SemanticsPosition)
	if !ok {
		t.Fatal("no POSITION distribution")
	}
	cd, ok := set.Distribution(institutional.SemanticsChange)
	if !ok {
		t.Fatal("no CHANGE distribution")
	}
	if len(fd.Values) != len(pd.Values) {
		t.Fatalf("FLOW has %d values and POSITION %d; the fixture gives both the same dates",
			len(fd.Values), len(pd.Values))
	}
	for i := range fd.Values {
		if fd.Values[i] == pd.Values[i] {
			t.Fatalf("FLOW and POSITION reference values agree at index %d (%v) — the two "+
				"distributions are the same set", i, fd.Values[i])
		}
	}
	// CHANGE is a constant +100 a day, which is neither of the other two.
	for _, v := range cd.Values {
		if v != 100 {
			t.Fatalf("CHANGE reference value %v; the fixture moves POSITION by exactly 100 a "+
				"day, so any other value means CHANGE was taken from FLOW or differenced "+
				"across the wrong series", v)
		}
	}
	if len(cd.Values) == 0 {
		t.Fatal("the CHANGE distribution is empty")
	}
}

// TestPercentileExcludesFutureObservations is the leak §10.4 names: "the distribution for a
// reading dated A contains only observations at or before A".
//
// A history entry dated after the reading is not filtered out quietly — it is ErrLookAhead,
// because a silently ignored row is how a look-ahead survives a code review.
func TestPercentileExcludesFutureObservations(t *testing.T) {
	obs := longSeries(t, 25)
	cur, hist := split(obs)

	future := observation(t, "2026-06-26", fptr(-9999), fptr(9999))
	if _, err := institutional.Percentiles(cur, append(hist, future), institutional.Policy{}); err == nil {
		t.Fatal("a history entry dated after the reading was accepted; the whole as-of contract " +
			"is that a later observation cannot reach an earlier reading")
	}

	// And the same day is not "at or before" either: two readings for one date are ambiguous.
	same := observation(t, cur.TradingDate, fptr(1), fptr(1))
	if _, err := institutional.Percentiles(cur, append(hist, same), institutional.Policy{}); err == nil {
		t.Fatal("a history entry dated ON the reading's date was accepted")
	}
}

// TestPercentileTiesCountAtOrBelow pins decision 4 against the repo's existing convention.
//
// internal/indicator.PercentileRank and internal/valuation.percentileOf are the same rule at two
// scales, and this asserts R15 is not a third: the ratio equals indicator's figure ÷ 100 on the
// same values, ties included. A test may import what the pure layer may not.
func TestPercentileTiesCountAtOrBelow(t *testing.T) {
	// Five observations all holding the same POSITION. A reading exactly at its own
	// historical median must not report below 50%.
	ds := consecutiveDates(t, 6, 6)
	positions := []float64{500, 500, 500, 500, 500, 500}
	obs := series(t, ds, positions)
	cur, hist := split(obs)

	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 3})
	got := ratioOrFail(t, set.Position, "POSITION percentile")
	if got != 1 {
		t.Errorf("POSITION percentile over six identical readings = %v, want 1 — with `<` "+
			"instead of `<=` a reading at its own median reports below 50%%", got)
	}

	dist, _ := set.Distribution(institutional.SemanticsPosition)
	want, ok := indicator.PercentileRank(dist.Values, 500, 1)
	if !ok {
		t.Fatal("indicator.PercentileRank refused the sample")
	}
	if math.Abs(got-want/100) > 1e-9 {
		t.Errorf("R15 says %v, internal/indicator says %v (÷100 = %v) — two conventions for "+
			"one percentile", got, want, want/100)
	}

	// A mixed sample, checked value by value against the existing implementation.
	mixed := longSeries(t, 20)
	mcur, mhist := split(mixed)
	mset := mustPercentiles(t, mcur, mhist, institutional.Policy{MinPercentileSample: 5})
	md, _ := mset.Distribution(institutional.SemanticsPosition)
	for _, v := range md.Values {
		gotRatio, ok := md.Ratio(v)
		if !ok {
			t.Fatalf("Ratio(%v) refused", v)
		}
		wantPct, ok := indicator.PercentileRank(md.Values, v, 1)
		if !ok {
			t.Fatalf("indicator.PercentileRank(%v) refused", v)
		}
		if math.Abs(gotRatio-wantPct/100) > 1e-9 {
			t.Errorf("Ratio(%v) = %v, indicator says %v", v, gotRatio, wantPct/100)
		}
	}
}

// TestCurrentObservationIsInsideItsOwnDistribution pins decision 3.
//
// It matches internal/indicator's BIASPercentile, which ranks the latest BIAS against a series
// containing it. The consequence worth asserting is that a ratio is never 0: the reading is
// always at or below itself.
func TestCurrentObservationIsInsideItsOwnDistribution(t *testing.T) {
	if !institutional.PercentileIncludesCurrentObservation {
		t.Fatal("PercentileIncludesCurrentObservation is false; decision 3 says it is true")
	}
	obs := longSeries(t, 12)
	cur, hist := split(obs)
	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 3})

	dist, _ := set.Distribution(institutional.SemanticsPosition)
	if len(dist.Values) != 12 {
		t.Errorf("POSITION distribution has %d values over 12 observations; the current one "+
			"must be in its own reference set", len(dist.Values))
	}
	if !dist.IncludesCurrent {
		t.Error("the distribution does not record that it includes the current observation")
	}
	got := ratioOrFail(t, set.Flow, "FLOW percentile")
	if got <= 0 {
		t.Errorf("FLOW percentile = %v; with the reading inside its own distribution the "+
			"ratio is in (0,1]", got)
	}
}

// TestInsufficientSampleIsAStatusNotANumber is §10.4's rule: "below the configured minimum the
// answer is INSUFFICIENT_SAMPLE with the actual and required N, not a percentile computed from
// four points".
func TestInsufficientSampleIsAStatusNotANumber(t *testing.T) {
	obs := longSeries(t, 4)
	cur, hist := split(obs)
	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 20})

	for _, tc := range []struct {
		name string
		rank institutional.PercentileRank
	}{
		{"FLOW", set.Flow},
		{"POSITION", set.Position},
		{"CHANGE", set.Change},
	} {
		if tc.rank.Status != institutional.PercentileInsufficientSample {
			t.Errorf("%s: status = %s, want INSUFFICIENT_SAMPLE", tc.name, tc.rank.Status)
		}
		if tc.rank.Ratio != nil {
			t.Errorf("%s: a ratio (%v) was published below the minimum sample", tc.name, *tc.rank.Ratio)
		}
		if _, ok := tc.rank.Value(); ok {
			t.Errorf("%s: Value() returned a number below the minimum sample", tc.name)
		}
		if tc.rank.RequiredSampleSize != 20 {
			t.Errorf("%s: required N = %d, want 20", tc.name, tc.rank.RequiredSampleSize)
		}
		if tc.rank.SampleSize >= 20 || tc.rank.SampleSize < 0 {
			t.Errorf("%s: actual N = %d, which is not below the requirement", tc.name, tc.rank.SampleSize)
		}
		switch tc.rank.Display {
		case "", "0", "0.0%", "-":
			t.Errorf("%s: an absent percentile rendered as %q — R14 spent a milestone on "+
				"exactly this", tc.name, tc.rank.Display)
		}
	}

	// One more observation than the minimum, and the same call publishes a number: the
	// status is about the sample, not about the code path.
	enough := longSeries(t, 21)
	ecur, ehist := split(enough)
	eset := mustPercentiles(t, ecur, ehist, institutional.Policy{MinPercentileSample: 20})
	if eset.Position.Status != institutional.PercentileAvailable {
		t.Errorf("with 21 observations POSITION is %s, want AVAILABLE (reason %q)",
			eset.Position.Status, eset.Position.Reason)
	}
}

// TestUnusableAndNullReadingsAreExcludedNotZeroFilled pins decisions 5 and 6.
//
// A NOT_PUBLISHED day and a day whose net column was absent both contribute NOTHING. Filling
// either with 0 would drag every percentile towards the middle of a distribution that never
// happened.
func TestUnusableAndNullReadingsAreExcludedNotZeroFilled(t *testing.T) {
	obs := longSeries(t, 10)
	cur, hist := split(obs)

	// A NOT_PUBLISHED day and a day whose POSITION column was absent, both earlier than the
	// whole series so the numbering stays readable.
	hist = append(hist, notPublished(t, "2026-05-20"))
	hist = append(hist, observation(t, "2026-05-21", fptr(-50), nil))

	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 3})
	dist, _ := set.Distribution(institutional.SemanticsPosition)

	for _, v := range dist.Values {
		if v == 0 {
			t.Fatalf("a 0 reached the POSITION reference distribution; neither the "+
				"NOT_PUBLISHED day nor the absent column is a zero. Values: %v", dist.Values)
		}
	}
	if len(dist.Values) != 10 {
		t.Errorf("POSITION distribution has %d values; 10 observations carry a POSITION and "+
			"two do not", len(dist.Values))
	}

	reasons := map[string]string{}
	for _, e := range dist.Excluded {
		reasons[e.TradingDate] = e.Reason
	}
	if reasons["2026-05-20"] != institutional.ExcludedNotUsable {
		t.Errorf("the NOT_PUBLISHED day is excluded as %q, want %q",
			reasons["2026-05-20"], institutional.ExcludedNotUsable)
	}
	if reasons["2026-05-21"] != institutional.ExcludedValueAbsent {
		t.Errorf("the absent-column day is excluded as %q, want %q",
			reasons["2026-05-21"], institutional.ExcludedValueAbsent)
	}

	// The FLOW distribution is unaffected by POSITION's absent column: that day HAS a FLOW.
	fd, _ := set.Distribution(institutional.SemanticsFlow)
	if len(fd.Values) != 11 {
		t.Errorf("FLOW distribution has %d values; 11 observations carry a FLOW", len(fd.Values))
	}
}

// TestStaleIsNeverAReferenceObservation pins decision 6's non-configurable half.
//
// §10.4 rules STALE out of the CHANGE baseline "and that is not configurable: a stale reading is
// one whose freshness contract already failed, and using it as a baseline would launder that
// failure". The same argument applies to a reference distribution, and there is no policy knob
// that admits it.
func TestStaleIsNeverAReferenceObservation(t *testing.T) {
	obs := longSeries(t, 10)
	cur, hist := split(obs)
	stale := observationFor(t, "2026-05-19", institutional.ScopeTX,
		institutional.InstitutionDealer, institutional.StatusStale, nil, nil)
	hist = append(hist, stale)

	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 3})
	dist, _ := set.Distribution(institutional.SemanticsPosition)
	for _, d := range dist.Dates {
		if d == "2026-05-19" {
			t.Fatal("a STALE observation contributed to the reference distribution")
		}
	}
	found := false
	for _, e := range dist.Excluded {
		if e.TradingDate == "2026-05-19" && e.Status == institutional.StatusStale {
			found = true
		}
	}
	if !found {
		t.Errorf("the STALE day was dropped without being recorded; skipping is not silent "+
			"(§10.4). Excluded: %+v", dist.Excluded)
	}
}

// TestLookbackWindowIsCountedInObservations pins decision 1.
//
// With a lookback of 5 the distribution holds 5 values however long the series is — and the 5
// are the most RECENT, not the first five encountered in whatever order the caller supplied.
func TestLookbackWindowIsCountedInObservations(t *testing.T) {
	obs := longSeries(t, 30)
	cur, hist := split(obs)
	// Reversed on the way in: the window must be decided by trading date, never by the
	// order the caller happened to hold the slice in.
	reversed := make([]institutional.Observation, 0, len(hist))
	for i := len(hist) - 1; i >= 0; i-- {
		reversed = append(reversed, hist[i])
	}

	set := mustPercentiles(t, cur, reversed, institutional.Policy{
		PercentileLookbackObservations: 5,
		MinPercentileSample:            3,
	})
	dist, _ := set.Distribution(institutional.SemanticsPosition)
	if len(dist.Values) != 5 {
		t.Fatalf("distribution has %d values, want 5", len(dist.Values))
	}
	// POSITION on day n is n*100, so the newest five over 30 days are 2600..3000.
	want := []float64{2600, 2700, 2800, 2900, 3000}
	for i, v := range want {
		if dist.Values[i] != v {
			t.Errorf("value %d = %v, want %v (the window must be the most recent five)",
				i, dist.Values[i], v)
		}
	}
}

// TestChangeDistributionIsRecomputedPerDate pins decision 8.
//
// Each historical date's CHANGE is POSITION(D) − POSITION(the previous valid observation before
// D), recomputed — never differenced from FLOW, and never taken across a gap without saying so.
// The fixture puts a NOT_PUBLISHED day in the middle: the date after it has a CHANGE of 200,
// because its previous VALID observation is two calendar days back.
func TestChangeDistributionIsRecomputedPerDate(t *testing.T) {
	dates := []string{"2026-06-01", "2026-06-02", "2026-06-04", "2026-06-05", "2026-06-06"}
	positions := []float64{100, 200, 400, 500, 600}
	obs := series(t, dates, positions)
	obs = append(obs, notPublished(t, "2026-06-03"))
	cur, hist := split(obs[:5])
	hist = append(hist, obs[5])

	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 2})
	dist, _ := set.Distribution(institutional.SemanticsChange)
	// 06-02:+100, 06-04:+200 (across the NOT_PUBLISHED day), 06-05:+100, 06-06:+100.
	// 06-01 has no earlier observation and contributes nothing.
	want := []float64{100, 100, 100, 200}
	if len(dist.Values) != len(want) {
		t.Fatalf("CHANGE distribution = %v, want %v", dist.Values, want)
	}
	for i := range want {
		if dist.Values[i] != want[i] {
			t.Fatalf("CHANGE distribution = %v, want %v — the +200 is the day after the "+
				"NOT_PUBLISHED one, and a distribution of all +100s means the gap was ignored",
				dist.Values, want)
		}
	}
	if len(dist.Excluded) == 0 {
		t.Error("the oldest date and the NOT_PUBLISHED day contributed nothing and neither was recorded")
	}
}

// TestPercentileValueAbsentIsNotInsufficientSample keeps two different "no percentile" apart.
//
// A reading with no number and a history too short to rank it are different facts, and §6's
// "MISSING is never NEUTRAL" is the same rule one level up: the panel must be able to say which.
func TestPercentileValueAbsentIsNotInsufficientSample(t *testing.T) {
	obs := longSeries(t, 25)
	_, hist := split(obs)
	// A current reading whose POSITION column was absent, on the day after the series.
	cur := observation(t, "2026-06-26", fptr(-2600), nil)

	set := mustPercentiles(t, cur, hist, institutional.Policy{MinPercentileSample: 5})
	if set.Position.Status != institutional.PercentileValueAbsent {
		t.Errorf("POSITION percentile status = %s, want VALUE_ABSENT", set.Position.Status)
	}
	if set.Position.SampleSize < 5 {
		t.Errorf("the sample is %d — the distribution is fine, it is the reading that is "+
			"absent, and the two must not both read as a short history", set.Position.SampleSize)
	}
	if set.Flow.Status != institutional.PercentileAvailable {
		t.Errorf("FLOW percentile status = %s, want AVAILABLE: only POSITION was absent",
			set.Flow.Status)
	}
}

// TestPolicyRejectsAnUnsatisfiableMinimum: a minimum above the window can never be met, so
// every percentile would be INSUFFICIENT_SAMPLE forever and the panel would show a permanent
// data gap that is really a typo.
func TestPolicyRejectsAnUnsatisfiableMinimum(t *testing.T) {
	_, err := institutional.Policy{
		PercentileLookbackObservations: 10,
		MinPercentileSample:            11,
	}.Normalized()
	if err == nil {
		t.Fatal("a minimum sample above the lookback window was accepted")
	}
}

// TestDefaultPercentilePolicyIsTheDocumentedOne pins the two numbers a caller inherits.
func TestDefaultPercentilePolicyIsTheDocumentedOne(t *testing.T) {
	p, err := institutional.Policy{}.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if p.PercentileLookbackObservations != institutional.DefaultPercentileLookbackObservations {
		t.Errorf("lookback = %d, want %d", p.PercentileLookbackObservations,
			institutional.DefaultPercentileLookbackObservations)
	}
	if p.MinPercentileSample != institutional.DefaultMinPercentileSample {
		t.Errorf("minimum sample = %d, want %d", p.MinPercentileSample,
			institutional.DefaultMinPercentileSample)
	}
	if institutional.PercentileTieRule != "AT_OR_BELOW" {
		t.Errorf("tie rule = %q", institutional.PercentileTieRule)
	}
	if institutional.PercentileScale != "RATIO_0_1" {
		t.Errorf("scale = %q", institutional.PercentileScale)
	}
}
