package main

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/study"
)

// followup is one CANDIDATE_FOLLOWUP: a thing the results SUGGEST, with the number that
// suggested it, and nothing else.
//
// EP-9 §29-§32: anything the results suggest becomes a candidate with evidence attached, never
// an EP-9 change. These are written into the run's own SUMMARY.md so the list is an ARTIFACT
// rather than a sentence in a review thread that nobody is holding — the same reason
// internal/entryplan/doc.go keeps its TODOs in code.
type followup struct{ what, evidence string }

// candidateFollowups builds the list from the run's own numbers. Nothing is typed: if a figure
// moves on the next run the list moves with it, which is the point.
func candidateFollowups(a *Artifact) []followup {
	pit := a.PIT
	f := a.Funnels["REPLAYED_PIT"]
	cell := func(arm study.Arm, w int) study.Metrics {
		c, ok := pit.Cells[study.CellKey(arm, w)]
		if !ok {
			return study.Metrics{Returns: map[int]study.Dist{}}
		}
		return c.Overall
	}
	seg := func(kind, name string) study.Metrics {
		c, ok := pit.Cells[study.CellKey(study.ArmZoneLimit, 5)]
		if !ok {
			return study.Metrics{Returns: map[int]study.Dist{}}
		}
		switch kind {
		case "status":
			return c.ByStatus[name]
		case "regime":
			return c.ByRegime[name]
		case "stratum":
			return c.ByStratum[name]
		}
		return study.Metrics{Returns: map[int]study.Dist{}}
	}
	b5 := cell(study.ArmZoneLimit, 5)
	c5 := cell(study.ArmChase, 5)
	ne5 := pit.NonExecutable[study.CellKey(study.ArmZoneLimit, 5)]

	var out []followup

	// The §18 item the user asked for by name.
	nonExN := f.WithZone - headlineN(a)
	sidew, distr := pit.NonExecutableStatusesByRegime["SIDEWAYS"], pit.NonExecutableStatusesByRegime["DISTRIBUTION"]
	out = append(out, followup{
		// EP-10 LABELLING FIX. This item used to read "would-be `BUY_NOW`", which asserts the
		// outcome of an evaluation that never ran. UNKNOWN IS NOT PASS: nothing in this repo
		// evaluated NEAR_SUPPORT or ELEVATED_EVIDENCE for these plans, so they are
		// EVALUATOR-BLOCKED ENTRY CANDIDATES — not would-be, not near, and not rejected
		// BUY_NOW. Their counterfactual outcomes below are POST_HOC_DISCOVERY.
		"**EVALUATOR-BLOCKED ENTRY CANDIDATES: zone-publishing plans held by an unimplemented " +
			"requirement evaluator.** " +
			nfmt(pit.NonExecutableReasons[string(entryplan.ReasonStatusRequirementNotEvaluated)]) +
			" plans published a zone and were held at `INSUFFICIENT_DATA` because `NEAR_SUPPORT` / " +
			"`ELEVATED_EVIDENCE` are conditions nothing in this repo evaluates " +
			"(decide.go:484-490, reasons.go:569-585). **Nothing here says what the evaluator " +
			"would have decided; UNKNOWN is not PASS, and these are not would-be, near or " +
			"rejected `BUY_NOW` plans.** Candidate: define the canonical evaluator, its " +
			"point-in-time reconstruction, its PASS/FAIL behaviour and the status transitions " +
			"it causes — or state in the layer's own docs that two regimes cannot reach BUY_NOW.",
		fmt.Sprintf("%d excluded plans; at W=5 they filled %s and returned %s — %s. "+
			"Concentrated in SIDEWAYS (%d) and DISTRIBUTION (%d). "+
			"POST_HOC_DISCOVERY: these are counterfactual outcomes for orders the layer never "+
			"published, computed after the fact, in NO headline metric, and they are not "+
			"evidence about what the missing evaluator would have decided.",
			nonExN, rate(ne5.FillRate), dist(ne5.Returns[20]), medianVerdict(ne5, b5), sidew, distr),
	})

	bn := seg("status", string(entryplan.StatusBuyNow))
	wp := seg("status", string(entryplan.StatusWaitPullback))
	out = append(out, followup{
		"**`BUY_NOW` is the worst-performing status.** Candidate: investigate decide.go's S10 " +
			"and S14 arms — the two that produce it — against the statuses that merely wait.",
		fmt.Sprintf("W=5 Return@20: BUY_NOW %s vs WAIT_PULLBACK %s.",
			short2(bn.Returns[20]), short2(wp.Returns[20])),
	})

	var chase string
	for _, c := range pit.Comparison22 {
		chase += fmt.Sprintf("W=%d chase-only %s vs zone %s; ", c.Wait,
			short2(c.ChaseOnly.Returns[20]), short2(c.ZoneFills.Returns[20]))
	}
	out = append(out, followup{
		"**The chase ceiling degrades arm C overall while the fills it ADDS are good.** " +
			"Candidate: the ceiling's effect on the PRICE of fills that the zone would have " +
			"taken anyway, separately from the fills it adds.",
		fmt.Sprintf("Arm C %s vs arm B %s at W=5. %s", short2(c5.Returns[20]),
			short2(b5.Returns[20]), chase),
	})

	bp := seg("regime", "BULL_PULLBACK")
	out = append(out, followup{
		"**`BULL_PULLBACK` is the only negative regime segment.** Candidate: re-measure with " +
			"more data before drawing any conclusion; it is also the smallest regime cell.",
		fmt.Sprintf("W=5 Return@20 %s against BULL %s and SIDEWAYS %s.", short2(bp.Returns[20]),
			short2(seg("regime", "BULL").Returns[20]), short2(seg("regime", "SIDEWAYS").Returns[20])),
	})

	out = append(out, followup{
		"**The invalidation is hit more often than Target1.** Candidate: whether the 0.5-ATR " +
			"zone half-width places the stop inside normal noise. **This is not a tuning " +
			"proposal and no threshold was changed.**",
		fmt.Sprintf("W=5: invalidation %s, Target1 %s; stop-first %s..%s.",
			rate(b5.InvalidationHitRate), rate(b5.Target1HitRate),
			rate(b5.StopFirstRateOptimistic), rate(b5.StopFirstRateConservative)),
	})

	out = append(out, followup{
		"**Almost no plan ever resolves an entry semantic, so the layer is dark on the whole " +
			"universe.** Candidate: the scanner's `ActPullbackBuy` emission rate — this is " +
			"upstream of entryplan entirely.",
		fmt.Sprintf("`ENTRY_SEMANTIC_UNAVAILABLE` on %d of %d plans (%.2f%%); only %d plans "+
			"(%.2f%%) publish a zone.", pit.ReasonCounts["ENTRY_SEMANTIC_UNAVAILABLE"],
			f.PlansGenerated, pct(pit.ReasonCounts["ENTRY_SEMANTIC_UNAVAILABLE"], f.PlansGenerated),
			f.WithZone, pct(f.WithZone, f.PlansGenerated)),
	})

	cl := seg("stratum", string(study.StratumClean))
	ev := seg("stratum", string(study.StratumEventInWindow))
	na := seg("stratum", string(study.StratumNoAdjustedSeries))
	out = append(out, followup{
		"**`CLEAN` is the only adjustment stratum with a win rate above 50%.** Candidate: " +
			"whether adjustment-contaminated observations should be a reported stratum in " +
			"production analytics too, not only in research.",
		fmt.Sprintf("W=5 Return@20: CLEAN %s (win %.2f%%), EVENT_IN_WINDOW %s (win %.2f%%), "+
			"NO_ADJUSTED_SERIES %s (win %.2f%%).", short2(cl.Returns[20]), cl.Returns[20].WinRate,
			short2(ev.Returns[20]), ev.Returns[20].WinRate, short2(na.Returns[20]), na.Returns[20].WinRate),
	})

	out = append(out, followup{
		"**MA60 would have changed nothing in this dataset.** If MA60 is ever implemented it " +
			"must be justified on grounds other than unblocking plans. **Not implemented here.**",
		fmt.Sprintf("Neither `PULLBACK_LEVELS_UNAVAILABLE` nor "+
			"`PULLBACK_LEVEL_NOT_BELOW_CURRENT_PRICE` appears in the reason census over %d "+
			"plans; the MA60 counterfactual column is NOT_BLOCKED for all %d.",
			f.PlansGenerated, pit.MA60Counts["NOT_BLOCKED"]),
	})

	var q5 string
	for _, o := range pit.Opportunity {
		q5 += fmt.Sprintf("W=%d missed %s vs filled %s; ", o.Wait,
			short2(o.MissedBenchmark.Returns[20]), short2(o.FilledBenchmark.Returns[20]))
	}
	out = append(out, followup{
		"**Waiting for the zone systematically selects the stocks that then underperform.** " +
			"Candidate: the selection cost of the pullback rule, measured against its price " +
			"advantage. This is the largest effect in the study.",
		fmt.Sprintf("Benchmark Return@20 split by whether the zone filled: %s Price advantage "+
			"is only %.2f bps (%.2f%%) at W=5.", q5, b5.FillVsSignalCloseBps.Mean,
			b5.FillVsSignalCloseBps.Mean/100),
	})

	return out
}

func nfmt(n int) string { return fmt.Sprintf("%d", n) }

func medianVerdict(a, b study.Metrics) string {
	am, bm := a.Returns[20], b.Returns[20]
	if am.N < MinInterpretableN || bm.N < MinInterpretableN {
		return fmt.Sprintf("N too small to compare (%d/%d)", am.N, bm.N)
	}
	if am.Median > 0 && bm.Median <= 0 {
		return "the ONLY group in the study with a positive median"
	}
	if am.Median > bm.Median {
		return "a higher median than the executable population"
	}
	return "a lower median than the executable population"
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedByCount(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// headlineN is the §18 executable population size, read off the run rather than recomputed.
func headlineN(a *Artifact) int {
	if a.PIT == nil {
		return 0
	}
	c, ok := a.PIT.Cells[study.CellKey(study.ArmZoneLimit, study.WaitWindows[0])]
	if !ok {
		return 0
	}
	return c.Overall.NCandidate + c.Overall.NNotApplicable
}
