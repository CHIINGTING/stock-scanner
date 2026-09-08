package institutional_test

import (
	"errors"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// The baseline rules, which are where a plausible shortcut does the most damage: D − 1 is
// almost always right and catastrophically wrong across a weekend, and a shortened horizon
// wearing a longer label is undetectable downstream.

// tradingDates is a run of real Taipei trading days ending on Friday 2026-09-04.
var tradingDates = []string{
	"2026-08-24", "2026-08-25", "2026-08-26", "2026-08-27", "2026-08-28",
	"2026-08-31", "2026-09-01", "2026-09-02", "2026-09-03", "2026-09-04",
}

// TestChangeIsPositionMinusPositionNeverFlow.
//
// The series moves POSITION up by 1,000 a day while FLOW moves DOWN by the same amount, so
// every wrong pairing produces a distinctive wrong answer:
//
//	POSITION(D) − POSITION(prev) = +1,000   ← the only correct one
//	FLOW(D) − FLOW(prev)         = −1,000
//	POSITION(D) − FLOW(prev)     = +2,000 + ...
func TestChangeIsPositionMinusPositionNeverFlow(t *testing.T) {
	history := series(t, []string{"2026-09-03"}, []float64{75174})
	current := observation(t, "2026-09-04", fptr(-1785), fptr(76174))

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got := lotsOrFail(t, change.ObservedMetric, "CHANGE")
	if got != 1000 {
		t.Fatalf("CHANGE = %v, want 1000 (POSITION 76174 − 75174). FLOW-differenced would be %v",
			got, -1785-(-75174))
	}
	if change.Semantics != institutional.SemanticsChange {
		t.Errorf("CHANGE carries semantics %q", change.Semantics)
	}
	if res.PreviousTradingDate != "2026-09-03" || res.ObservationGap != 1 || res.CalendarGapDays != 1 {
		t.Errorf("resolution = %+v, want previous 2026-09-03, gap 1 observation / 1 day", res)
	}

	// The FLOW half of the same observation is untouched and still says something different.
	if f := lotsOrFail(t, current.Flow().ObservedMetric, "FLOW"); f == got {
		t.Error("FLOW and CHANGE are the same number")
	}
}

// TestChangeFallsBackToNothingWhenPositionIsAbsent — never to FLOW, and never to 0. §10.4 names
// both substitutions; each produces a number on a day when there is none.
func TestChangeFallsBackToNothingWhenPositionIsAbsent(t *testing.T) {
	history := series(t, []string{"2026-09-03"}, []float64{75174})
	current := observation(t, "2026-09-04", fptr(1785), nil)

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	mustAbsent(t, change.ObservedMetric, institutional.StatusPartial, "CHANGE with no current POSITION")
	if res.Status != institutional.StatusPartial {
		t.Errorf("resolution status = %s, want PARTIAL", res.Status)
	}
	if v, ok := change.Lots(); ok {
		t.Fatalf("CHANGE = %v; FLOW-differenced would be %v and 0 would be the other wrong answer",
			v, 1785-(-75174))
	}
}

// TestPreviousValidObservationCrossesAWeekend — Monday's baseline is Friday, the observation
// gap is 1 and the calendar gap is 3, and BOTH travel with the value. A difference labelled
// CHANGE_1D over three calendar days is §10.4's named defect.
func TestPreviousValidObservationCrossesAWeekend(t *testing.T) {
	// The weekend is simply absent from the series, which is how a real archive looks.
	history := series(t, []string{"2026-09-03", "2026-09-04"}, []float64{75000, 76174})
	monday := observation(t, "2026-09-07", fptr(-500), fptr(77174))

	change, res, err := institutional.Change(monday, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 1000 {
		t.Errorf("CHANGE = %v, want 1000", got)
	}
	if res.PreviousTradingDate != "2026-09-04" {
		t.Errorf("PreviousTradingDate = %q, want 2026-09-04 — D−1 would be Sunday 2026-09-06",
			res.PreviousTradingDate)
	}
	if res.ObservationGap != 1 {
		t.Errorf("ObservationGap = %d, want 1", res.ObservationGap)
	}
	if res.CalendarGapDays != 3 {
		t.Errorf("CalendarGapDays = %d, want 3 — the calendar gap is what makes the weekend "+
			"visible", res.CalendarGapDays)
	}
	if res.Status != institutional.StatusAvailable {
		t.Errorf("status = %s, want AVAILABLE (3 days is inside the tolerance)", res.Status)
	}
}

// TestPreviousValidObservationSkipsNotPublishedAndKeepsTheGap.
//
// A 15:00 run legitimately sees NOT_PUBLISHED for the institutional file while the by-strike
// report is already out (§6). That day is not a baseline, it is not a hole in the record, and
// skipping it must not make the resulting difference look like a one-day one.
func TestPreviousValidObservationSkipsNotPublishedAndKeepsTheGap(t *testing.T) {
	history := []institutional.Observation{
		observation(t, "2026-09-03", fptr(-1), fptr(70000)),
		notPublished(t, "2026-09-04"),
		noSession(t, "2026-09-05"), // Saturday, recorded as a status rather than as zeros
		noSession(t, "2026-09-06"), // Sunday
	}
	current := observation(t, "2026-09-07", fptr(-2), fptr(76174))

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 6174 {
		t.Errorf("CHANGE = %v, want 6174 (76174 − 70000)", got)
	}
	if res.PreviousTradingDate != "2026-09-03" {
		t.Errorf("PreviousTradingDate = %q, want 2026-09-03", res.PreviousTradingDate)
	}
	if res.ObservationGap != 1 {
		t.Errorf("ObservationGap = %d, want 1 valid observation", res.ObservationGap)
	}
	if res.CalendarGapDays != 4 {
		t.Errorf("CalendarGapDays = %d, want 4 — the unpublished Friday and the weekend are "+
			"still days", res.CalendarGapDays)
	}
	if len(res.SkippedDates) != 3 {
		t.Fatalf("SkippedDates = %+v, want the weekend and the unpublished Friday", res.SkippedDates)
	}
	wantSkips := map[string]string{
		"2026-09-06": institutional.SkipNoSession,
		"2026-09-05": institutional.SkipNoSession,
		"2026-09-04": institutional.SkipNotPublished,
	}
	for _, sk := range res.SkippedDates {
		if want, ok := wantSkips[sk.TradingDate]; !ok || sk.Reason != want {
			t.Errorf("skipped %s for %q, want %q", sk.TradingDate, sk.Reason, want)
		}
	}
	// Newest first, so a reader walks the same path the resolver did.
	if got := res.SkippedDateList(); got[0] != "2026-09-06" || got[2] != "2026-09-04" {
		t.Errorf("SkippedDateList() = %v, want [2026-09-06 2026-09-05 2026-09-04]", got)
	}
	// NOT_PUBLISHED is not MISSING and the skip records which one it was: the two imply
	// opposite actions (§6), and a resolver that flattened them would make an afternoon run
	// look like a hole in the archive.
	for _, sk := range res.SkippedDates {
		if sk.TradingDate == "2026-09-04" && sk.Status != institutional.StatusNotPublished {
			t.Errorf("2026-09-04 was skipped with status %s", sk.Status)
		}
	}
}

// TestAPartialBaselineWithoutPositionIsSkipped — it may carry a perfectly good FLOW, which is
// exactly why it cannot anchor a POSITION difference.
func TestAPartialBaselineWithoutPositionIsSkipped(t *testing.T) {
	partialNoPosition := observationFor(t, "2026-09-03", institutional.ScopeTX,
		institutional.InstitutionDealer, institutional.StatusPartial, fptr(5000), nil)
	history := []institutional.Observation{
		observation(t, "2026-09-02", fptr(-1), fptr(70000)),
		partialNoPosition,
	}
	current := observation(t, "2026-09-04", fptr(-2), fptr(76174))

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 6174 {
		t.Errorf("CHANGE = %v, want 6174; 76174 − 5000 = %v would mean the PARTIAL day's FLOW "+
			"was used as a position", got, 76174-5000)
	}
	if res.PreviousTradingDate != "2026-09-02" {
		t.Errorf("PreviousTradingDate = %q, want 2026-09-02", res.PreviousTradingDate)
	}
	if len(res.SkippedDates) != 1 || res.SkippedDates[0].Reason != institutional.SkipPartialWithoutPosition {
		t.Errorf("SkippedDates = %+v, want one PARTIAL_WITHOUT_POSITION", res.SkippedDates)
	}
}

// TestAPartialBaselineWithPositionIsUsedUnderTheDefaultPolicy — and the policy can turn it off.
// §10.4 skips "a PARTIAL snapshot LACKING the POSITION columns", which is not the same as
// skipping every PARTIAL; the resolution records which one it used.
func TestAPartialBaselineWithPositionIsUsedUnderTheDefaultPolicy(t *testing.T) {
	history := []institutional.Observation{
		observation(t, "2026-09-02", fptr(-1), fptr(60000)),
		observationFor(t, "2026-09-03", institutional.ScopeTX, institutional.InstitutionDealer,
			institutional.StatusPartial, fptr(5000), fptr(70000)),
	}
	current := observation(t, "2026-09-04", fptr(-2), fptr(76174))

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 6174 {
		t.Errorf("CHANGE = %v, want 6174", got)
	}
	if res.BaselineStatus != institutional.StatusPartial {
		t.Errorf("BaselineStatus = %s, want PARTIAL recorded on the resolution", res.BaselineStatus)
	}

	strict := institutional.DefaultPolicy()
	strict.AcceptPartialWithPosition = false
	change, res, err = institutional.Change(current, history, strict)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 16174 {
		t.Errorf("strict CHANGE = %v, want 16174 (against 2026-09-02)", got)
	}
	if res.BaselineStatus != institutional.StatusAvailable {
		t.Errorf("strict BaselineStatus = %s, want AVAILABLE", res.BaselineStatus)
	}
}

// TestStaleBaselineIsNotANumber — a baseline beyond the tolerance produces STALE_BASELINE with
// the dates and gaps intact, not a difference across a three-week hole.
func TestStaleBaselineIsNotANumber(t *testing.T) {
	history := series(t, []string{"2026-08-14"}, []float64{70000})
	current := observation(t, "2026-09-07", fptr(-2), fptr(76174))

	change, res, err := institutional.Change(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	mustAbsent(t, change.ObservedMetric, institutional.StatusStaleBaseline, "CHANGE")
	if res.Status != institutional.StatusStaleBaseline {
		t.Errorf("resolution status = %s, want STALE_BASELINE", res.Status)
	}
	if res.PreviousTradingDate != "2026-08-14" || res.CalendarGapDays != 24 {
		t.Errorf("resolution = %+v, want the baseline date and 24-day gap kept", res)
	}
	if res.ToleranceDays != 10 {
		t.Errorf("ToleranceDays = %d, want 10", res.ToleranceDays)
	}

	// The same data inside a wider tolerance is a number again — the state is about the
	// policy, not about the data being unusable in principle.
	loose := institutional.DefaultPolicy()
	loose.MaxBaselineCalendarGapDays = 30
	change, _, err = institutional.Change(current, history, loose)
	if err != nil {
		t.Fatal(err)
	}
	if got := lotsOrFail(t, change.ObservedMetric, "CHANGE"); got != 6174 {
		t.Errorf("CHANGE = %v, want 6174 under a 30-day tolerance", got)
	}
}

// TestHorizonsCountObservationsNotCalendarDays — Change3/5/10 over ten real trading days.
func TestHorizonsCountObservationsNotCalendarDays(t *testing.T) {
	positions := []float64{
		70000, 70500, 71000, 71500, 72000, 72500, 73000, 73500, 74000, 75174,
	}
	history := series(t, tradingDates, positions)
	current := observation(t, "2026-09-07", fptr(-2), fptr(76174))

	horizons, err := institutional.ChangeHorizons(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		n        int
		baseline string
		change   float64
		calendar int
	}{
		{1, "2026-09-04", 76174 - 75174, 3},
		{3, "2026-09-02", 76174 - 73500, 5},
		{5, "2026-08-31", 76174 - 72500, 7},
		{10, "2026-08-24", 76174 - 70000, 14},
	}
	if len(horizons) != len(want) {
		t.Fatalf("got %d horizons, want %d", len(horizons), len(want))
	}
	for i, w := range want {
		h := horizons[i]
		if h.Horizon != w.n {
			t.Fatalf("horizon %d has N = %d", i, h.Horizon)
		}
		if h.Label != institutional.HorizonLabel(w.n) {
			t.Errorf("%d: label = %q", w.n, h.Label)
		}
		// The label says OBS, never D: the 10-observation window spans 14 calendar days.
		if got := lotsOrFail(t, h.Change.ObservedMetric, h.Label); got != w.change {
			t.Errorf("%s = %v, want %v", h.Label, got, w.change)
		}
		if h.PreviousTradingDate != w.baseline {
			t.Errorf("%s baseline = %q, want %q", h.Label, h.PreviousTradingDate, w.baseline)
		}
		if h.EffectiveObservations != w.n {
			t.Errorf("%s effective observations = %d, want %d", h.Label, h.EffectiveObservations, w.n)
		}
		if h.CalendarGapDays != w.calendar {
			t.Errorf("%s calendar gap = %d, want %d", h.Label, h.CalendarGapDays, w.calendar)
		}
		if h.CurrentTradingDate != "2026-09-07" {
			t.Errorf("%s current date = %q", h.Label, h.CurrentTradingDate)
		}
		if h.Status != institutional.StatusAvailable {
			t.Errorf("%s status = %s", h.Label, h.Status)
		}
	}
}

// TestInsufficientHistoryNeverShortensTheHorizon — the failure this test exists for is a
// Change10 computed over four observations and labelled Change10, which nothing downstream
// could detect.
func TestInsufficientHistoryNeverShortensTheHorizon(t *testing.T) {
	history := series(t, tradingDates[6:], []float64{70000, 71000, 72000, 75174}) // four observations
	current := observation(t, "2026-09-07", fptr(-2), fptr(76174))

	horizons, err := institutional.ChangeHorizons(current, history, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	byN := map[int]institutional.HorizonChange{}
	for _, h := range horizons {
		byN[h.Horizon] = h
	}

	if got := lotsOrFail(t, byN[3].Change.ObservedMetric, "CHANGE_3OBS"); got != 76174-71000 {
		t.Errorf("CHANGE_3OBS = %v, want %v", got, 76174-71000)
	}
	for _, n := range []int{5, 10} {
		h := byN[n]
		mustAbsent(t, h.Change.ObservedMetric, institutional.StatusInsufficientHistory, h.Label)
		if h.PreviousTradingDate != "" {
			t.Errorf("%s has baseline %q; there is no %d-observation baseline in a "+
				"4-observation series", h.Label, h.PreviousTradingDate, n)
		}
		if h.EffectiveObservations != 4 {
			t.Errorf("%s effective observations = %d, want 4 — the count is reported, the "+
				"horizon is not shortened to it", h.Label, h.EffectiveObservations)
		}
		if h.Horizon != n {
			t.Errorf("%s: horizon silently became %d", h.Label, h.Horizon)
		}
		if v, ok := h.Change.Lots(); ok {
			// The shortened answer would be 76174 − 70000 = 6174 for both 5 and 10.
			t.Errorf("%s produced %v; the shortest available span would give %v",
				h.Label, v, 76174-70000)
		}
	}
}

// TestHistoryIsRejectedRatherThanFilteredWhenItLooksAhead — §5's as-of bound is the caller's
// job, and a resolver that quietly ignores a future row is where a look-ahead hides.
func TestHistoryIsRejectedRatherThanFilteredWhenItLooksAhead(t *testing.T) {
	current := observation(t, "2026-09-04", fptr(-2), fptr(76174))
	for _, d := range []string{"2026-09-04", "2026-09-07"} {
		history := series(t, []string{d}, []float64{1})
		_, err := institutional.ResolveBaseline(current, history, 1, institutional.DefaultPolicy())
		if !errors.Is(err, institutional.ErrLookAhead) {
			t.Errorf("history dated %s: err = %v, want ErrLookAhead", d, err)
		}
	}
	dup := []institutional.Observation{
		observation(t, "2026-09-03", fptr(1), fptr(1)),
		observation(t, "2026-09-03", fptr(2), fptr(2)),
	}
	_, err := institutional.ResolveBaseline(current, dup, 1, institutional.DefaultPolicy())
	if !errors.Is(err, institutional.ErrDuplicateObservation) {
		t.Errorf("duplicate dates: err = %v, want ErrDuplicateObservation", err)
	}
}

// TestHistoryOrderDoesNotMatter — the resolver sorts; a caller handing back rows in query order
// gets the same answer as one handing back a reversed slice.
func TestHistoryOrderDoesNotMatter(t *testing.T) {
	positions := []float64{70000, 70500, 71000, 71500, 72000, 72500, 73000, 73500, 74000, 75174}
	ascending := series(t, tradingDates, positions)
	descending := make([]institutional.Observation, len(ascending))
	for i, o := range ascending {
		descending[len(ascending)-1-i] = o
	}
	current := observation(t, "2026-09-07", fptr(-2), fptr(76174))

	a, err := institutional.ResolveBaseline(current, ascending, 5, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	b, err := institutional.ResolveBaseline(current, descending, 5, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if a.PreviousTradingDate != b.PreviousTradingDate || a.CalendarGapDays != b.CalendarGapDays {
		t.Errorf("order changed the answer: %+v vs %+v", a, b)
	}
}

// TestNoHistoryAtAllIsInsufficientHistory — the first day of an archive. Not zero, not an error.
func TestNoHistoryAtAllIsInsufficientHistory(t *testing.T) {
	current := observation(t, "2026-09-04", fptr(-1856), fptr(-697))
	change, res, err := institutional.Change(current, nil, institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	mustAbsent(t, change.ObservedMetric, institutional.StatusInsufficientHistory, "CHANGE")
	if res.EffectiveObservations != 0 || res.PreviousTradingDate != "" {
		t.Errorf("resolution = %+v", res)
	}
}
