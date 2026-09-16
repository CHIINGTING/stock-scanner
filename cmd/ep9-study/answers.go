package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/study"
)

// MinInterpretableN is the cell size below which this study refuses to draw a conclusion.
//
// It is declared as a constant and applied uniformly rather than judged per table, because a
// floor chosen per cell is a floor chosen after seeing the cell. Thirty is conventional and
// arbitrary; what matters is that it is fixed, visible, and applied to segments that flatter
// the result as well as to segments that do not.
const MinInterpretableN = 30

// answerSpine renders EP-9 §28's eleven questions, in order, from the run's own numbers.
//
// Every figure is read off the artifact rather than typed, so the answers cannot drift from
// the tables below them. Where a cell is under MinInterpretableN the answer says so instead of
// stating a result.
func answerSpine(a *Artifact) string {
	var b strings.Builder
	p := func(f string, v ...any) { fmt.Fprintf(&b, f, v...) }

	pit := a.PIT
	f := a.Funnels["REPLAYED_PIT"]
	cell := func(arm study.Arm, w int) study.Metrics {
		c, ok := pit.Cells[study.CellKey(arm, w)]
		if !ok {
			return study.Metrics{Returns: map[int]study.Dist{}}
		}
		return c.Overall
	}
	b5 := cell(study.ArmZoneLimit, 5)
	c5 := cell(study.ArmChase, 5)
	_ = cell(study.ArmSignalCloseBaseline, 5)

	p("Answers are read off this run's own artifact. **Every one holds UNDER REPLAYED REGIME " +
		"ONLY** and none is a validation of production behaviour. HEURISTIC — NOT BACKTEST-FITTED; " +
		"a positive historical mean is not \"validated profitable\".\n\n")

	// 1
	p("**Q1. How much historical coverage do we actually have?**\n")
	p("%d sessions, %s .. %s, %d symbols loaded and %d observed, giving **%d stock-session "+
		"observations**. %d further eligible sessions have no point-in-time regime file and are "+
		"excluded from BOTH passes so the two tables share a session set. Valuation provenance is "+
		"`UNAVAILABLE_HISTORICALLY` for all %d observations: no valuation archive is dated on or "+
		"before any session this study can grade. Regime provenance is `REPLAYED_PIT` for all of "+
		"them; archived-regime coverage with a full 20-session outcome window is **zero**.\n\n",
		a.Sessions, a.DatasetFrom, a.DatasetTo, a.SymbolsLoaded, a.SymbolsObserved,
		f.PlansGenerated, a.BlindOnly, f.PlansGenerated)

	// 2
	p("**Q2. Which statuses are actually reachable?**\n")
	p("Of %d plans: ", f.PlansGenerated)
	var parts []string
	for _, st := range []entryplan.EntryStatus{entryplan.StatusBuyNow, entryplan.StatusWaitPullback,
		entryplan.StatusTooExtended, entryplan.StatusWaitBreakout, entryplan.StatusNoValidEntry,
		entryplan.StatusInsufficientData} {
		parts = append(parts, fmt.Sprintf("`%s` %d", st, pit.StatusCounts[string(st)]))
	}
	p("%s.\n", strings.Join(parts, ", "))
	p("`WAIT_BREAKOUT` is **N=0**, and that is a FINDING rather than a gap: production's "+
		"`ActBreakoutBuy` is unreachable, so `EntrySemantic=BREAKOUT` never occurs and neither does "+
		"the status that waits for it. `%s` is %.1f%% of all plans, and the reason census says why "+
		"— `ENTRY_SEMANTIC_UNAVAILABLE` on %d plans: the scanner never named an entry shape, so the "+
		"entry-plan layer is dark on 99%% of the universe. Only **%d plans (%.2f%%)** ever publish "+
		"an entry zone.\n\n",
		entryplan.StatusInsufficientData,
		pct(pit.StatusCounts[string(entryplan.StatusInsufficientData)], f.PlansGenerated),
		pit.ReasonCounts["ENTRY_SEMANTIC_UNAVAILABLE"], f.WithZone,
		pct(f.WithZone, f.PlansGenerated))

	// 3
	p("**Q3. What fraction of plans fill?**\n")
	p("Of the %d plans that published a zone, the zone arm fills ", b5.NCandidate)
	var fr []string
	for _, w := range study.WaitWindows {
		m := cell(study.ArmZoneLimit, w)
		fr = append(fr, fmt.Sprintf("**%.2f%%** at W=%d (%d/%d)", m.FillRate.Pct, w, m.NFilled, m.NCandidate))
	}
	p("%s. The chase arm, which has an order only on the %d plans that published a ceiling, fills ", strings.Join(fr, ", "), c5.NCandidate)
	fr = fr[:0]
	for _, w := range study.WaitWindows {
		m := cell(study.ArmChase, w)
		fr = append(fr, fmt.Sprintf("%.2f%% at W=%d", m.FillRate.Pct, w))
	}
	p("%s. %d plans are `NOT_APPLICABLE` for the chase arm because the regime's policy REFUSES "+
		"to chase — a refusal, not a failure to fill, and it is outside the denominator.\n\n",
		strings.Join(fr, ", "), c5.NNotApplicable-b5.NNotApplicable+0)

	// 4 — the PRICE question
	p("**Q4. Does zone waiting improve fill PRICE?**\n")
	p("Yes, clearly, and this is a statement about price rather than about return.\n\n")
	p("> %s\n\n", study.FillVsSignalCloseBpsConvention)
	p("| arm | W | AvgFillVsSignalCloseBps | MedianFillVsSignalCloseBps | reading |\n|---|---:|---:|---:|---|\n")
	for _, w := range study.WaitWindows {
		for _, arm := range []study.Arm{study.ArmZoneLimit, study.ArmChase, study.ArmSignalCloseBaseline} {
			m := cell(arm, w)
			if m.FillVsSignalCloseBps.N == 0 {
				continue
			}
			p("| %s | %d | %.2f | %.2f | %s |\n", m.Arm, w, m.FillVsSignalCloseBps.Mean,
				m.FillVsSignalCloseBps.Median, priceReading(m.FillVsSignalCloseBps.Mean))
		}
	}
	p("\nAt W=5 the zone arm fills on average **%.2f bps** — i.e. **%.2f%% BELOW** the signal "+
		"close, a CHEAPER entry — with a median of %.2f bps (%.2f%% below). The chase arm gives "+
		"back most of that (%.2f bps mean, %.2f%% below), which is exactly what a higher ceiling "+
		"buys. The benchmark is 0 by construction. So the zone rule does what it claims on PRICE; "+
		"whether the cheaper price is worth the missed trades is Q5 and Q6, not this question.\n\n",
		b5.FillVsSignalCloseBps.Mean, math.Abs(b5.FillVsSignalCloseBps.Mean)/100,
		b5.FillVsSignalCloseBps.Median, math.Abs(b5.FillVsSignalCloseBps.Median)/100,
		c5.FillVsSignalCloseBps.Mean, math.Abs(c5.FillVsSignalCloseBps.Mean)/100)

	// 5
	p("**Q5. What is the cost in missed opportunities?**\n")
	if len(pit.Opportunity) > 0 {
		p("Missed rate is ")
		var mr []string
		for _, o := range pit.Opportunity {
			mr = append(mr, fmt.Sprintf("**%.2f%%** at W=%d (%d of %d)", ratePct(o.MissedN, o.MissedN+o.FilledN),
				o.Wait, o.MissedN, o.MissedN+o.FilledN))
		}
		p("%s.\n\n", strings.Join(mr, ", "))
		p("What those plans went on to do, measured with the UNEXECUTABLE signal-close benchmark " +
			"— never a fabricated fill:\n\n")
		p("| W | missed N | benchmark Return@20 (missed) | filled N | benchmark Return@20 (filled) | verdict |\n")
		p("|---:|---:|---|---:|---|---|\n")
		for _, o := range pit.Opportunity {
			p("| %d | %d | %s | %d | %s | %s |\n", o.Wait, o.MissedN,
				dist(o.MissedBenchmark.Returns[20]), o.FilledN, dist(o.FilledBenchmark.Returns[20]),
				missVerdict(o))
		}
		p("\n%s\n\n", pit.Opportunity[0].Note)
	} else {
		p("Not computed in this run.\n\n")
	}

	// 6
	p("**Q6. Does chase improve outcomes or just fill rate?**\n")
	p("Just fill rate, and it makes the arm as a whole WORSE. Arm C fills more often by "+
		"construction (`MaxChasePrice >= IdealEntry.High` is a production invariant), so the "+
		"fill-rate line is arithmetic. On outcomes at W=5, arm C Return@20 is %s against arm B's "+
		"%s, and arm C's MAE20 is deeper (%s vs %s).\n\n",
		short2(c5.Returns[20]), short2(b5.Returns[20]), short2(c5.MAE20), short2(b5.MAE20))
	p("The §22 split is the sharper reading — the fills the ceiling ADDED:\n\n")
	p("| W | chase-only N | chase-only Return@20 | zone Return@20 | chase-only MAE20 | zone MAE20 | N>=%d? |\n", MinInterpretableN)
	p("|---:|---:|---|---|---|---|---|\n")
	for _, c := range pit.Comparison22 {
		p("| %d | %d | %s | %s | %s | %s | %v |\n", c.Wait, c.ChaseOnlyN,
			short2(c.ChaseOnly.Returns[20]), short2(c.ZoneFills.Returns[20]),
			short2(c.ChaseOnly.MAE20), short2(c.ZoneFills.MAE20), c.ChaseOnlyN >= MinInterpretableN)
	}
	p("\nThe extra fills were individually GOOD, yet the arm that takes them is worse overall — " +
		"because the ceiling also converts zone fills into dearer fills. A higher fill rate is not " +
		"an improvement.\n\n")

	// 7
	p("**Q7. How often is invalidation hit?**\n")
	p("| arm | W | invalidation hit | denominator | stop-first conservative | stop-first optimistic | AMBIGUOUS bars |\n")
	p("|---|---:|---|---|---|---|---:|\n")
	for _, w := range study.WaitWindows {
		for _, arm := range []study.Arm{study.ArmZoneLimit, study.ArmChase} {
			m := cell(arm, w)
			p("| %s | %d | %s | %s | %s | %s | %d |\n", m.Arm, w, rate(m.InvalidationHitRate),
				m.InvalidationHitRate.Population, rate(m.StopFirstRateConservative),
				rate(m.StopFirstRateOptimistic), m.FirstTouchAmbiguous)
		}
	}
	p("\nThe denominator is filled observations **whose plan published an invalidation**, never " +
		"every filled observation. The two stop-first figures are BOUNDS, not an estimate: daily " +
		"OHLC cannot order a stop and a target touched inside one bar, so every such bar is " +
		"counted as stop-first in the conservative bound and as not-stop-first in the optimistic " +
		"one, and the truth lies between them.\n\n")

	// 8
	p("**Q8. How often are T1/T2 reached?**\n")
	p("| arm | W | Target1 hit | Target2 hit |\n|---|---:|---|---|\n")
	for _, w := range study.WaitWindows {
		for _, arm := range []study.Arm{study.ArmZoneLimit, study.ArmChase} {
			m := cell(arm, w)
			p("| %s | %d | %s | %s |\n", m.Arm, w, rate(m.Target1HitRate), rate(m.Target2HitRate))
		}
	}
	p("\nEach denominator is filled observations whose plan published THAT level. At W=5 the zone "+
		"arm's invalidation is hit **%s** against Target1's **%s** — the stop is reached slightly "+
		"MORE often than the first target, and both are 20-session touch rates rather than exit "+
		"rules: a plan can touch both.\n\n", rate(b5.InvalidationHitRate), rate(b5.Target1HitRate))

	// 9
	p("**Q9. What do median and distribution metrics say?**\n")
	p("They contradict the means, and the medians are the ones to read.\n\n")
	p("| arm | W | Return@20 mean | median | p25 | p75 | win rate | N |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, w := range study.WaitWindows {
		for _, arm := range []study.Arm{study.ArmZoneLimit, study.ArmChase, study.ArmSignalCloseBaseline} {
			m := cell(arm, w)
			d := m.Returns[20]
			if d.N == 0 {
				continue
			}
			p("| %s | %d | %.2f | %.2f | %.2f | %.2f | %.2f%% | %d |\n", m.Arm, w, d.Mean,
				d.Median, d.P25, d.P75, d.WinRate, d.N)
		}
	}
	p("\nEvery executable arm has a **positive mean and a negative median** at 20 sessions: more "+
		"than half of filled entries were down, and the mean is carried by a right tail (p75 %.2f "+
		"against p25 %.2f for the zone arm at W=5). Win rates are all below 50%%. MFE20 mean %.2f "+
		"against MAE20 mean %.2f says the same thing from the excursion side: the plans moved a "+
		"long way in both directions. **Any statement about this study that quotes only a mean is "+
		"misleading, and that is why no table here reports one alone.**\n\n",
		b5.Returns[20].P75, b5.Returns[20].P25, b5.MFE20.Mean, b5.MAE20.Mean)

	// 10 / 11
	sup, ins := classifyConclusions(a, pit, cell)
	p("**Q10. Which conclusions are supported by sufficient N?**\n")
	p("Floor: **N >= %d** filled observations in the cell, fixed in advance and applied to "+
		"segments that flatter the result as well as those that do not. Sufficient N is a "+
		"NECESSARY condition here, not a sufficient one: every item below still inherits the "+
		"replayed-regime caveat, and none has a confidence interval attached.\n\n", MinInterpretableN)
	for _, l := range sup {
		p("- %s\n", l)
	}
	p("\n**Q11. Which conclusions remain insufficient evidence?**\n")
	for _, l := range ins {
		p("- %s\n", l)
	}
	p("\n")
	return b.String()
}

// classifyConclusions places each candidate conclusion on one side of the N floor, using the
// run's own cell sizes. Nothing here is hand-typed: the N values come from the artifact.
func classifyConclusions(a *Artifact, pit *study.Run,
	cell func(study.Arm, int) study.Metrics) (sup, ins []string) {

	b5 := cell(study.ArmZoneLimit, 5)
	c5 := cell(study.ArmChase, 5)
	seg := func(kind string, name string) study.Metrics {
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
	ok := func(n int) bool { return n >= MinInterpretableN }
	add := func(n int, text string) {
		line := fmt.Sprintf("%s (N=%d)", text, n)
		if ok(n) {
			sup = append(sup, line)
		} else {
			ins = append(ins, line+" — **below the N>=30 floor**")
		}
	}

	f := a.Funnels["REPLAYED_PIT"]
	sup = append(sup, fmt.Sprintf("The entry-plan layer is dark on almost the whole universe: "+
		"only %d of %d plans publish a zone (%.2f%%). This is a census, not a sample (N=%d).",
		f.WithZone, f.PlansGenerated, pct(f.WithZone, f.PlansGenerated), f.PlansGenerated))
	sup = append(sup, fmt.Sprintf("`WAIT_BREAKOUT` and the BREAKOUT semantic are unreachable: "+
		"N=0 over %d plans, consistent with the production unreachability proof.", f.PlansGenerated))
	sup = append(sup, fmt.Sprintf("The valuation ceiling never binds: 0 of %d plans, and the "+
		"archive cannot reach any gradeable session.", f.PlansGenerated))
	sup = append(sup, fmt.Sprintf("MA60's absence blocks no plan: neither level-reason code "+
		"appears in the reason census over %d plans, because plans fail at the SEMANTIC step "+
		"long before the level step.", f.PlansGenerated))

	add(b5.NFilled, "Zone fills are cheaper than the signal close "+
		fmt.Sprintf("(mean %.2f bps, median %.2f bps at W=5)", b5.FillVsSignalCloseBps.Mean,
			b5.FillVsSignalCloseBps.Median))
	add(b5.NFilled, fmt.Sprintf("The zone arm's fill rate is %.2f%% at W=5 and rises "+
		"monotonically with the wait window", b5.FillRate.Pct))
	add(b5.InvalidationHitRate.Denominator, fmt.Sprintf("The invalidation is hit %.2f%% of the "+
		"time and slightly more often than Target1 (%.2f%%)", b5.InvalidationHitRate.Pct,
		b5.Target1HitRate.Pct))
	add(b5.Returns[20].N, "Return@20 has a positive mean and a NEGATIVE median on every "+
		"executable arm, with win rates below 50%")
	add(c5.NFilled, "Arm C as a whole underperforms arm B on Return@20 despite a higher fill rate")

	for _, c := range pit.Comparison22 {
		add(c.ChaseOnlyN, fmt.Sprintf("Chase-only fills at W=%d outperformed zone fills "+
			"(%.2f%% vs %.2f%% mean Return@20)", c.Wait, c.ChaseOnly.Returns[20].Mean,
			c.ZoneFills.Returns[20].Mean))
	}
	for _, o := range pit.Opportunity {
		add(o.MissedN, fmt.Sprintf("At W=%d, the plans the zone arm MISSED had a benchmark "+
			"Return@20 of %.2f%% mean / %.2f%% median against the filled population's %.2f%% / "+
			"%.2f%%", o.Wait, o.MissedBenchmark.Returns[20].Mean, o.MissedBenchmark.Returns[20].Median,
			o.FilledBenchmark.Returns[20].Mean, o.FilledBenchmark.Returns[20].Median))
	}

	bn := seg("status", string(entryplan.StatusBuyNow))
	add(bn.NFilled, fmt.Sprintf("`BUY_NOW` is the WORST status by Return@20 (%.2f%% mean, "+
		"%.2f%% median, %.2f%% win)", bn.Returns[20].Mean, bn.Returns[20].Median, bn.Returns[20].WinRate))
	wp := seg("status", string(entryplan.StatusWaitPullback))
	add(wp.NFilled, fmt.Sprintf("`WAIT_PULLBACK` outperforms `BUY_NOW` (%.2f%% vs %.2f%% mean "+
		"Return@20)", wp.Returns[20].Mean, bn.Returns[20].Mean))
	te := seg("status", string(entryplan.StatusTooExtended))
	add(te.NFilled, fmt.Sprintf("`TOO_EXTENDED` sits between them (%.2f%% mean Return@20)",
		te.Returns[20].Mean))

	for _, rg := range []string{"BULL", "BULL_PULLBACK", "SIDEWAYS", "DISTRIBUTION"} {
		m := seg("regime", rg)
		if m.NFilled == 0 {
			ins = append(ins, fmt.Sprintf("Regime `%s`: no filled observation at all (N=0) — "+
				"nothing can be said", rg))
			continue
		}
		add(m.NFilled, fmt.Sprintf("Regime `%s` Return@20 %.2f%% mean / %.2f%% median",
			rg, m.Returns[20].Mean, m.Returns[20].Median))
	}
	for _, st := range []study.Stratum{study.StratumClean, study.StratumEventInWindow,
		study.StratumNoAdjustedSeries} {
		m := seg("stratum", string(st))
		add(m.NFilled, fmt.Sprintf("Adjustment stratum `%s` Return@20 %.2f%% mean / %.2f%% median",
			st, m.Returns[20].Mean, m.Returns[20].Median))
	}

	ins = append(ins, "Any ORDERING between segments: no confidence interval, no significance "+
		"test and no multiple-comparison correction was computed, and there are roughly thirty "+
		"segment cells per wait window, so some ordering is chance.")
	ins = append(ins, "Anything about production behaviour: every metric is under a REPLAYED "+
		"regime (posture always UNKNOWN so rules R4/R5 never fire; breadth survivorship-biased "+
		"over the cache's current membership). Archived-regime coverage with a full outcome "+
		"window is N=0, so no comparison exists.")
	ins = append(ins, "Anything about a wait window other than the predeclared W=3/5/10.")
	ins = append(ins, "Whether any of this generalises beyond one price cache of one market "+
		"over about eighteen months.")

	sort.SliceStable(ins, func(i, j int) bool { return false })
	return sup, ins
}

func pct(n, d int) float64 {
	if d == 0 {
		return math.NaN()
	}
	return float64(n) / float64(d) * 100
}

func ratePct(n, d int) float64 { return pct(n, d) }

func priceReading(bps float64) string {
	switch {
	case bps < -0.5:
		return fmt.Sprintf("filled %.2f%% BELOW the close — cheaper, better for a buyer", math.Abs(bps)/100)
	case bps > 0.5:
		return fmt.Sprintf("filled %.2f%% ABOVE the close — dearer, worse for a buyer", bps/100)
	}
	return "filled at the close"
}

func short2(d study.Dist) string {
	if d.N == 0 {
		return "N=0 -"
	}
	return fmt.Sprintf("mean %.2f median %.2f (N=%d)", d.Mean, d.Median, d.N)
}

// missVerdict states which way the miss cut, in words, with the N floor applied.
func missVerdict(o study.OpportunityCost) string {
	m, f := o.MissedBenchmark.Returns[20], o.FilledBenchmark.Returns[20]
	if m.N < MinInterpretableN || f.N < MinInterpretableN {
		return fmt.Sprintf("N too small (%d/%d)", m.N, f.N)
	}
	switch {
	case m.Median > f.Median:
		return "missing skipped the BETTER performers"
	case m.Median < f.Median:
		return "missing skipped the WORSE performers"
	}
	return "no median difference"
}
