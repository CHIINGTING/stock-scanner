package study

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ── Adjustment strata (user decision 3) ───────────────────────────────────────────────────

// adjSeries builds bars whose Close/AdjClose ratio is 1.0 everywhere (an adjusted series that
// has adjusted nothing) unless a step is planted at `eventAt`.
func adjSeries(n int, eventAt int) []fetcher.Candle {
	bars, _ := flatSeries(n, 100)
	for i := range bars {
		// Ratio 1.05 before the event, 1.00 from it on: that is what a real restatement looks
		// like in Close / AdjClose, which is the ONLY thing ScanAdjustments can see.
		if eventAt >= 0 && i < eventAt {
			bars[i].AdjClose = bars[i].Close / 1.05
		} else {
			bars[i].AdjClose = bars[i].Close / 1.00000001
		}
	}
	return bars
}

func TestStratumIsCleanWhenNoAdjustmentTouchesEitherWindow(t *testing.T) {
	// Event at bar 5; the signal is bar 90, so the event is ~85 bars behind the plan window
	// (61 bars) and nowhere near the outcome window.
	bars := adjSeries(140, 5)
	if got := ClassifyStratum(bars, 90); got != StratumClean {
		t.Fatalf("stratum %q, want CLEAN — the event at bar 5 is outside both windows", got)
	}
}

func TestAnEventInsideThePlanWindowIsNotClean(t *testing.T) {
	// Signal at bar 90; the plan window is the trailing 61 bars, i.e. bars 30..90.
	bars := adjSeries(140, 60)
	if got := ClassifyStratum(bars, 90); got != StratumEventInWindow {
		t.Fatalf("stratum %q, want EVENT_IN_WINDOW — an adjustment at bar 60 sits inside the "+
			"61-bar plan window and corrupts the levels the plan was drawn from", got)
	}
}

func TestAnEventInsideTheOutcomeWindowIsNotClean(t *testing.T) {
	// Signal at bar 90; the outcome window is bars 90..110.
	bars := adjSeries(140, 100)
	if got := ClassifyStratum(bars, 90); got != StratumEventInWindow {
		t.Fatalf("stratum %q, want EVENT_IN_WINDOW — an adjustment at bar 100 manufactures a "+
			"stop touch, an excursion or a return inside the outcome window", got)
	}
}

// TestNoAdjustedSeriesIsItsOwnStratumAndIsNeverRead AsClean is the MISSING != ZERO line for
// this axis: 430 of 1,987 cached symbols land here and calling them clean would assert "no
// ex-dividend gap" for every one we merely cannot see.
func TestNoAdjustedSeriesIsItsOwnStratumAndIsNeverReadAsClean(t *testing.T) {
	bars, _ := flatSeries(140, 100)
	for i := range bars {
		bars[i].AdjClose = bars[i].Close // exactly 1.0 everywhere
	}
	got := ClassifyStratum(bars, 90)
	if got != StratumNoAdjustedSeries {
		t.Fatalf("stratum %q, want NO_ADJUSTED_SERIES", got)
	}
	if got == StratumClean {
		t.Fatal("a symbol with no adjusted series was reported CLEAN")
	}
}

func TestAnUnformableWindowIsUnknownRatherThanClean(t *testing.T) {
	bars := adjSeries(5, -1)
	if got := ClassifyStratum(bars, 3); got == StratumClean {
		t.Fatal("a 5-bar series was vouched for as CLEAN over a 61-bar plan window")
	}
	if got := ClassifyStratum(bars, -1); got != StratumUnknown {
		t.Fatalf("an out-of-range signal index gave %q, want UNKNOWN", got)
	}
	if got := ClassifyStratum(nil, 0); got != StratumUnknown {
		t.Fatalf("an empty series gave %q, want UNKNOWN", got)
	}
}

func TestTheStrataArePartitioningAndCountsReconcile(t *testing.T) {
	// Every stratum is a defined value and no observation can be in two.
	seen := map[Stratum]bool{}
	for _, s := range AllStrata {
		if !s.Valid() || seen[s] {
			t.Fatalf("%q is not a distinct defined stratum", s)
		}
		seen[s] = true
	}
	if Stratum("").Valid() || Stratum("DIRTY").Valid() {
		t.Fatal("an undefined stratum passed Valid")
	}
	// And a classification always lands on one of them — nothing falls through.
	for _, bars := range [][]fetcher.Candle{adjSeries(140, 5), adjSeries(140, 100), adjSeries(5, -1)} {
		if got := ClassifyStratum(bars, 90); !got.Valid() {
			t.Fatalf("classification produced %q, which is not a stratum", got)
		}
	}
}

// ── MA60 counterfactual (§31) ─────────────────────────────────────────────────────────────

func planBlocked(reason entryplan.Reason, currentPrice float64) *entryplan.Plan {
	p := &entryplan.Plan{Symbol: "T", Status: entryplan.StatusInsufficientData,
		RuleVersion: entryplan.RuleVersion, Reasons: []entryplan.Reason{reason},
		EntryTrace: &entryplan.EntryTrace{}}
	cp := currentPrice
	p.EntryTrace.Decision.CurrentPrice = &cp
	return p
}

// risingSeries makes MA60 sit strictly below the last close, so MA60 is an eligible centre.
func risingSeries(n int) []fetcher.Candle {
	bars, _ := flatSeries(n, 100)
	for i := range bars {
		p := 50.0 + float64(i)
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close, bars[i].AdjClose = p, p, p, p, p
	}
	return bars
}

func TestMA60IsLabelledWouldUnblockOnlyWhenNoOtherCandidateWasEligible(t *testing.T) {
	bars := risingSeries(120)
	last := bars[100].Close
	p := planBlocked(entryplan.ReasonPullbackLevelNotBelowCurrentPrice, last)
	if got := ClassifyMA60Block(p, bars, 100); got != MA60WouldUnblockSolely {
		t.Fatalf("label %q, want %q: MA60 of a rising series sits below the last close and no "+
			"other candidate was eligible", got, MA60WouldUnblockSolely)
	}
}

func TestAnAlreadyEligibleCandidateMeansMA60IsNotTheBlocker(t *testing.T) {
	bars := risingSeries(120)
	p := planBlocked(entryplan.ReasonPullbackLevelsUnavailable, bars[100].Close)
	p.EntryTrace.Zone.Candidates = []entryplan.CandidateLevel{
		{Source: entryplan.LevelMA20, Disposition: entryplan.DispositionEligibleNotSelected},
	}
	if got := ClassifyMA60Block(p, bars, 100); got != MA60BlockedOtherCause {
		t.Fatalf("label %q, want %q", got, MA60BlockedOtherCause)
	}
}

func TestAPlanThatGotAZoneIsNeverLabelledBlocked(t *testing.T) {
	bars := risingSeries(120)
	p := planBlocked(entryplan.ReasonPullbackLevelsUnavailable, bars[100].Close)
	p.IdealEntry = &entryplan.PriceZone{Low: 99, High: 101}
	if got := ClassifyMA60Block(p, bars, 100); got != MA60NotBlocked {
		t.Fatalf("label %q, want %q — a plan with a zone was not blocked by anything", got, MA60NotBlocked)
	}
}

// TestOnlyTheTwoLevelReasonsCount pins the deliberate exclusion of the basis-mismatch code:
// supplying MA60 would not fix a price-basis mismatch, so counting it would inflate §31.
func TestOnlyTheTwoLevelReasonsCount(t *testing.T) {
	bars := risingSeries(120)
	for _, r := range []entryplan.Reason{
		entryplan.ReasonPullbackLevelsBasisMismatch,
		entryplan.ReasonZoneATRUnavailable,
		entryplan.ReasonZoneSemanticNotPermitted,
	} {
		p := planBlocked(r, bars[100].Close)
		if got := ClassifyMA60Block(p, bars, 100); got != MA60NotBlocked {
			t.Fatalf("reason %s was labelled %q; MA60 cannot fix it, so it must not be "+
				"counted as MA60-blocked", r, got)
		}
	}
}

func TestMA60IsUnknownWhenItCannotBeComputed(t *testing.T) {
	bars := risingSeries(30) // fewer than 60 bars
	p := planBlocked(entryplan.ReasonPullbackLevelsUnavailable, bars[29].Close)
	if got := ClassifyMA60Block(p, bars, 29); got != MA60Unknown {
		t.Fatalf("label %q, want %q — 'we could not ask' is not 'the answer is no'", got, MA60Unknown)
	}
	if got := ClassifyMA60Block(nil, bars, 29); got != MA60Unknown {
		t.Fatalf("a nil plan gave %q", got)
	}
}

// TestMA60ChangesNoBaselineNumber is the §31 hard gate: the counterfactual is a COLUMN.
func TestMA60ChangesNoBaselineNumber(t *testing.T) {
	bars := risingSeries(120)
	p := planBlocked(entryplan.ReasonPullbackLevelNotBelowCurrentPrice, bars[100].Close)
	before, _ := json.Marshal(p)
	label := ClassifyMA60Block(p, bars, 100)
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatalf("classifying the MA60 counterfactual MUTATED the plan:\n%s\n%s", before, after)
	}
	if label == "" {
		t.Fatal("the classifier returned no label; the test would be vacuous")
	}
}

// ── The NaN-at-the-output-boundary defect §24 found ───────────────────────────────────────

func TestAnEmptyRateSerializesAsNullAndNotAsZero(t *testing.T) {
	b, err := json.Marshal(NewRate(0, 0, "nothing"))
	if err != nil {
		t.Fatalf("an empty rate could not be serialized at all: %v", err)
	}
	if !strings.Contains(string(b), `"pct":null`) {
		t.Fatalf("empty rate serialized as %s, want a null pct — 0.0 would assert that "+
			"nothing was hit out of something", b)
	}
	full, err := json.Marshal(NewRate(1, 4, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), `"pct":25`) {
		t.Fatalf("a real rate serialized as %s", full)
	}
}

// TestAWholeMetricsBlockSerializes is the regression for the run file that would not write.
func TestAWholeMetricsBlockSerializes(t *testing.T) {
	m := Aggregate(ArmZoneLimit, 5, "empty segment", nil)
	if _, err := json.Marshal(m); err != nil {
		t.Fatalf("an all-empty metric block could not be serialized: %v — the machine-readable "+
			"run file is what makes the study checkable, and a study that cannot write its own "+
			"result is a study nobody can check", err)
	}
	if !math.IsNaN(m.FillRate.Pct) {
		t.Fatal("an empty fill rate is no longer NaN in memory; the null is now hiding a 0")
	}
}
