package study

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ──────────────────────────────────────────────────────────────────────────────────────────
// Tests that drive Execute ITSELF.
//
// Why these exist: the accumulators were well covered and Execute was not, so three reviewer
// mutations inside run.go survived the whole suite — including one that INVERTS the Q5
// verdict, which is the most consequential number in the study, and one that re-introduced
// the "the MISSED row echoes arm B's returns" defect that had already been found and fixed
// once. Every test in this file asserts a property of the ASSEMBLED RUN, not of a helper
// wired up by hand in the test.
// ──────────────────────────────────────────────────────────────────────────────────────────

// execFixture builds observations with a KNOWN Q5 direction.
//
//	"MISS-<n>"  zone far below the market: never fills, and the stock then RISES hard.
//	"FILL-<n>"  zone at the market: fills on T+1, and the stock then FALLS.
//
// So the missed population's benchmark must beat the filled population's. If Execute ever
// swaps the two sides, the direction flips and the assertion below fails.
func execFixture(t *testing.T, misses, fills int) []Observation {
	t.Helper()
	var out []Observation
	mk := func(sym string, zoneHigh float64, drift float64) Observation {
		bars, s := flatSeries(40, 100)
		for i := 2; i < 40; i++ {
			p := 100.0 + drift*float64(i-1)
			bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = p, p, p, p
			bars[i].AdjClose = p
		}
		idx, _ := s.IndexOf(s.dates[0])
		return Observation{
			Symbol: sym, SignalAsOf: s.dates[0],
			Status: entryplan.StatusWaitPullback, Semantic: entryplan.EntrySemanticPullback,
			Policy: entryplan.PolicyNormal,
			Regime: model.RegimeBull, RegimeProvenance: entryplanbacktest.RegimeReplayedPIT,
			ValuationProvenance: entryplanbacktest.ValuationUnavailableHistorically,
			SignalClose:         bars[idx].Close,
			ZoneHigh:            &zoneHigh, ZoneLow: ptr(zoneHigh - 1),
			Stratum: StratumClean, MA60Block: MA60NotBlocked,
			bars: bars, sessions: s, sigIdx: idx,
		}
	}
	for i := 0; i < misses; i++ {
		out = append(out, mk(sym("MISS", i), 50, +2)) // unreachable zone, stock rises
	}
	for i := 0; i < fills; i++ {
		out = append(out, mk(sym("FILL", i), 100, -1)) // zone at the market, stock falls
	}
	return out
}

func sym(prefix string, i int) string {
	return prefix + string(rune('A'+i/26)) + string(rune('A'+i%26))
}

// TestExecuteProducesTheQ5SplitInTheRightDirection is the guard on the study's most
// consequential number. The fixture is built so the answer is known: the MISSED plans rose and
// the FILLED plans fell, so the missed side's benchmark MUST be the higher one.
func TestExecuteProducesTheQ5SplitInTheRightDirection(t *testing.T) {
	obs := execFixture(t, 40, 40)
	// Ten more executable plans that published NO zone. Arm B never had an order for them, so
	// they are NOT_APPLICABLE and must not enter the Q5 split on either side — a missed
	// opportunity is an order that did not fill, not an order that was never placed.
	noZone := execFixture(t, 0, 10)
	for i := range noZone {
		noZone[i].Symbol = "NZ" + noZone[i].Symbol
		noZone[i].ZoneHigh, noZone[i].ZoneLow = nil, nil
	}
	obs = append(obs, noZone...)
	run := Execute(obs, entryplanbacktest.RegimeReplayedPIT, nil)

	if len(run.Opportunity) != len(WaitWindows) {
		t.Fatalf("Execute produced %d opportunity blocks, want %d", len(run.Opportunity), len(WaitWindows))
	}
	for _, o := range run.Opportunity {
		if o.MissedN != 40 || o.FilledN != 40 {
			t.Fatalf("W=%d split is missed=%d filled=%d, want 40/40 — either the §18 gate or "+
				"the fill-outcome gate let the wrong observations in. Ten NOT_APPLICABLE "+
				"plans (no zone published) are in the input and belong on NEITHER side.",
				o.Wait, o.MissedN, o.FilledN)
		}
		m, f := o.MissedBenchmark.Returns[20], o.FilledBenchmark.Returns[20]
		if m.N == 0 || f.N == 0 {
			t.Fatalf("W=%d: missed N=%d filled N=%d — an empty side makes the test vacuous",
				o.Wait, m.N, f.N)
		}
		if !(m.Mean > 0) {
			t.Fatalf("W=%d: the MISSED side's benchmark mean is %.2f; the fixture's missed "+
				"stocks RISE, so a non-positive mean means Execute put the filled rows on the "+
				"missed side", o.Wait, m.Mean)
		}
		if !(f.Mean < 0) {
			t.Fatalf("W=%d: the FILLED side's benchmark mean is %.2f; the fixture's filled "+
				"stocks FALL", o.Wait, f.Mean)
		}
		if !(m.Mean > f.Mean) || !(m.Median > f.Median) {
			t.Fatalf("W=%d: missed %.2f/%.2f is not above filled %.2f/%.2f — the Q5 verdict "+
				"is inverted", o.Wait, m.Mean, m.Median, f.Mean, f.Median)
		}
	}
}

// TestExecuteLeavesTheMissedArmWithNoOutcome pins, on the ASSEMBLED RUN, the defect that was
// found once and fixed once: arm D must publish MissedRate and nothing else.
func TestExecuteLeavesTheMissedArmWithNoOutcome(t *testing.T) {
	run := Execute(execFixture(t, 40, 40), entryplanbacktest.RegimeReplayedPIT, nil)
	for _, w := range WaitWindows {
		c, ok := run.Cells[CellKey(ArmMissed, w)]
		if !ok {
			t.Fatalf("W=%d has no MISSED cell", w)
		}
		m := c.Overall
		// Anti-vacuity: arm B must have produced a real sample for the same rows.
		b := run.Cells[CellKey(ArmZoneLimit, w)].Overall
		if b.Returns[20].N == 0 {
			t.Fatalf("W=%d: arm B produced no Return@20 sample; the test would be vacuous", w)
		}
		for _, h := range Horizons {
			if m.Returns[h].N != 0 {
				t.Fatalf("W=%d: the MISSED cell reports %d Return@%d samples — a fabricated "+
					"outcome for trades that did not happen", w, m.Returns[h].N, h)
			}
		}
		if m.MFE20.N != 0 || m.MAE20.N != 0 || m.FillVsSignalCloseBps.N != 0 {
			t.Fatalf("W=%d: the MISSED cell reports excursions or fill quality (MFE N=%d, "+
				"MAE N=%d, bps N=%d)", w, m.MFE20.N, m.MAE20.N, m.FillVsSignalCloseBps.N)
		}
		if m.InvalidationHitRate.Denominator != 0 || m.Target1HitRate.Denominator != 0 {
			t.Fatalf("W=%d: the MISSED cell reports level hits", w)
		}
		if m.NMissed != 40 {
			t.Fatalf("W=%d: the MISSED cell counts %d misses, want 40", w, m.NMissed)
		}
	}
}

// TestExecuteAppliesTheSection18PopulationFilter pins the user's §18 ruling on the assembled
// run: a zone-publishing plan whose STATUS is not executable is out of every headline metric
// and present, in full, in the named block.
func TestExecuteAppliesTheSection18PopulationFilter(t *testing.T) {
	obs := execFixture(t, 0, 40)
	// Ten more that publish a zone but whose verdict was never reached.
	extra := execFixture(t, 0, 10)
	for i := range extra {
		extra[i].Symbol = "NX" + extra[i].Symbol
		extra[i].Status = entryplan.StatusInsufficientData
		extra[i].Reasons = []entryplan.Reason{entryplan.ReasonStatusRequirementNotEvaluated}
	}
	// CHASE-ONLY observations, in both populations: the zone sits far below the market so arm
	// B misses, while the chase ceiling sits at the market so arm C fills. Without these the
	// §22 chase-only counter is 0 on every path and an unfiltered counter is indistinguishable
	// from a filtered one — which is exactly how a leak survived an earlier audit.
	chaseExec := execFixture(t, 3, 0)
	for i := range chaseExec {
		chaseExec[i].Symbol = "CE" + chaseExec[i].Symbol
		chaseExec[i].MaxChase = ptr(100.0)
	}
	chaseNonExec := execFixture(t, 3, 0)
	for i := range chaseNonExec {
		chaseNonExec[i].Symbol = "CN" + chaseNonExec[i].Symbol
		chaseNonExec[i].MaxChase = ptr(100.0)
		chaseNonExec[i].Status = entryplan.StatusInsufficientData
		chaseNonExec[i].Reasons = []entryplan.Reason{entryplan.ReasonStatusRequirementNotEvaluated}
	}
	all := append(append(append(obs, extra...), chaseExec...), chaseNonExec...)
	run := Execute(all, entryplanbacktest.RegimeReplayedPIT, nil)

	for _, w := range WaitWindows {
		head := run.Cells[CellKey(ArmZoneLimit, w)].Overall
		if head.NCandidate != 43 {
			t.Fatalf("W=%d headline NCandidate=%d, want 43 (40 + 3 chase-only executable) — "+
				"the 13 non-executable-status plans are inside a headline metric",
				w, head.NCandidate)
		}
		if head.NExcludedByStatus != 13 {
			t.Fatalf("W=%d headline NExcludedByStatus=%d, want 13 — the excluded population "+
				"must be visible, not merely absent", w, head.NExcludedByStatus)
		}
		ne, ok := run.NonExecutable[CellKey(ArmZoneLimit, w)]
		if !ok {
			t.Fatalf("W=%d has no non-executable block", w)
		}
		if ne.NCandidate != 13 {
			t.Fatalf("W=%d excluded block NCandidate=%d, want 13 — nothing may be dropped by "+
				"the pair", w, ne.NCandidate)
		}
		if ne.NFilled == 0 {
			t.Fatalf("W=%d excluded block has no fills; the block would be empty and the test "+
				"vacuous", w)
		}
		// The two populations must partition the input exactly.
		if got := head.NCandidate + head.NNotApplicable + ne.NCandidate + ne.NNotApplicable; got != 56 {
			t.Fatalf("W=%d: the two populations cover %d of 56 observations", w, got)
		}
		if head.Population == "" || ne.Population == "" {
			t.Fatal("a metric block does not name its population")
		}
	}
	// THE §22 COUNTERS OBEY THE SAME FILTER. This is the assertion the earlier version of
	// this test lacked, which is how an unfiltered parallel counter survived a 64/64 mutation
	// audit: ZoneN was incremented beside acc.Add() and therefore never saw the filter, so the
	// §22 table printed a zone-fills N that disagreed with the distribution on the same row by
	// exactly the excluded population.
	for _, c := range run.Comparison22 {
		if c.ZoneN != c.ZoneFills.NFilled {
			t.Fatalf("W=%d: §22 reports ZoneN=%d beside a distribution of N=%d. A count that "+
				"disagrees with the sample it labels is worse than no count.",
				c.Wait, c.ZoneN, c.ZoneFills.NFilled)
		}
		if c.ChaseOnlyN != c.ChaseOnly.NFilled {
			t.Fatalf("W=%d: §22 reports ChaseOnlyN=%d beside a distribution of N=%d.",
				c.Wait, c.ChaseOnlyN, c.ChaseOnly.NFilled)
		}
		if c.ZoneFills.NExcludedByStatus == 0 {
			t.Fatalf("W=%d: the §22 zone accumulator excluded nothing, so the assertion above "+
				"cannot distinguish a filtered counter from an unfiltered one", c.Wait)
		}
		// The chase-only side needs the same anti-vacuity guard, and it needs a NON-ZERO
		// chase-only population: with none, a filtered and an unfiltered counter both read 0.
		if c.ChaseOnlyN == 0 {
			t.Fatalf("W=%d: no chase-only fills in the fixture, so the ChaseOnlyN assertion "+
				"above cannot detect an unfiltered counter", c.Wait)
		}
		if c.ChaseOnly.NExcludedByStatus == 0 {
			t.Fatalf("W=%d: the §22 chase-only accumulator excluded nothing, so the assertion "+
				"above cannot distinguish a filtered counter from an unfiltered one", c.Wait)
		}
		if c.ChaseOnlyN != 3 {
			t.Fatalf("W=%d: ChaseOnlyN=%d, want 3 — three executable chase-only fills are in "+
				"the fixture and three non-executable ones must be excluded", c.Wait, c.ChaseOnlyN)
		}
	}

	// Q5 obeys the same filter.
	for _, o := range run.Opportunity {
		if o.MissedN+o.FilledN != 43 {
			t.Fatalf("W=%d Q5 covers %d observations, want the 43 executable ones only",
				o.Wait, o.MissedN+o.FilledN)
		}
	}
	// And the excluded block carries the evidence for why it exists.
	if run.NonExecutableReasons[string(entryplan.ReasonStatusRequirementNotEvaluated)] != 13 {
		t.Fatalf("the excluded block's reason census is %v; it must record WHY those plans "+
			"were excluded", run.NonExecutableReasons)
	}
	if run.NonExecutableStatuses[string(entryplan.StatusInsufficientData)] != 13 {
		t.Fatalf("the excluded block's status census is %v", run.NonExecutableStatuses)
	}
}

// TestExecuteComputesNoMetricOnANonPITPass pins the user's B9 ruling on the assembled run.
func TestExecuteComputesNoMetricOnANonPITPass(t *testing.T) {
	obs := execFixture(t, 10, 10)
	for i := range obs {
		obs[i].RegimeProvenance = entryplanbacktest.RegimeUnavailable
	}
	run := Execute(obs, entryplanbacktest.RegimeUnavailable, nil)
	if run.MetricsHeld {
		t.Fatal("a regime-blind pass reports MetricsHeld")
	}
	if len(run.Cells) != 0 || len(run.Opportunity) != 0 || len(run.Comparison22) != 0 {
		t.Fatalf("a regime-blind pass produced %d cells, %d opportunity blocks and %d §22 "+
			"comparisons — it is a COVERAGE STATEMENT and must carry no metric",
			len(run.Cells), len(run.Opportunity), len(run.Comparison22))
	}
	if total := len(run.StatusCounts); total == 0 {
		t.Fatal("a regime-blind pass produced no coverage counts either; it must still count")
	}
	// The PIT pass over the same fixture DOES produce cells, so the check above is not
	// passing because Execute produces nothing at all.
	pit := Execute(execFixture(t, 10, 10), entryplanbacktest.RegimeReplayedPIT, nil)
	if len(pit.Cells) == 0 {
		t.Fatal("the PIT pass produced no cells; the comparison is vacuous")
	}
}

var _ = fetcher.Candle{}

// TestTheExecutablePopulationIsTheFourEntryStatuses pins the EP-9 §18 membership itself.
//
// The set is the user's ruling, transcribed, and it is exactly the four statuses that assert
// an entry may be taken — now (BUY_NOW), on a pullback, on a breakout, or later once price
// comes back (TOO_EXTENDED, which keeps its zone and its targets). Adding a status widens
// every headline metric; dropping one silently shrinks the population a table describes.
func TestTheExecutablePopulationIsTheFourEntryStatuses(t *testing.T) {
	want := []entryplan.EntryStatus{
		entryplan.StatusBuyNow,
		entryplan.StatusWaitPullback,
		entryplan.StatusWaitBreakout,
		entryplan.StatusTooExtended,
	}
	if len(ExecutableStatuses) != len(want) {
		t.Fatalf("the executable population has %d statuses, want %d: %v",
			len(ExecutableStatuses), len(want), ExecutableStatuses)
	}
	for _, s := range want {
		if !ExecutableStatuses[s] {
			t.Fatalf("%q is not in the executable population — a headline metric just lost "+
				"a whole class of published entries", s)
		}
		o := &Observation{Status: s}
		if !o.ExecutableStatus() {
			t.Fatalf("Observation with status %q reports itself non-executable", s)
		}
	}
	// The two that must stay OUT, and the zero value.
	for _, s := range []entryplan.EntryStatus{
		entryplan.StatusInsufficientData, entryplan.StatusNoValidEntry, "",
	} {
		if ExecutableStatuses[s] {
			t.Fatalf("%q is in the executable population — a plan the layer never published "+
				"as an entry is inside every headline metric", s)
		}
		o := &Observation{Status: s}
		if o.ExecutableStatus() {
			t.Fatalf("Observation with status %q reports itself executable", s)
		}
	}
}
