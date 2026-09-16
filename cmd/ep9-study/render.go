package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/study"
)

// num renders a float the way every table in this study must: "-" when there is nothing to
// show, never 0.00. A zero printed for an empty sample is the single most common way a
// backtest table lies.
func num(v float64, n int) string {
	if n == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func rate(r study.Rate) string {
	if r.Denominator == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f%% (%d/%d)", r.Pct, r.Hits, r.Denominator)
}

func dist(d study.Dist) string {
	if d.N == 0 {
		return "N=0  -"
	}
	return fmt.Sprintf("N=%d mean %s median %s p25 %s p75 %s win %s%%",
		d.N, num(d.Mean, d.N), num(d.Median, d.N), num(d.P25, d.N), num(d.P75, d.N),
		num(d.WinRate, d.N))
}

// CSVProvenancePrefix is the comment block every metrics CSV must open with.
//
// A CSV is the most DETACHABLE artifact this study produces: it will be opened in a
// spreadsheet, pasted into a message and quoted in isolation long after SUMMARY.md is out of
// sight. A metrics table with no "under replayed regime only" statement on its own face is
// therefore the exact failure the user's B9 ruling targets, and a column alone is not enough
// — a reader who filters the sheet keeps the column and loses the caveats.
//
// So BOTH: a leading comment block carrying conclusions_hold_under and both replay caveats,
// AND a per-row regime_provenance column, so neither a truncated head nor a filtered column
// can strip the provenance entirely.
const csvCommentPrefix = "# "

func csvProvenanceBlock(r *study.Run) []string {
	out := []string{
		csvCommentPrefix + "EP-9 metrics. HEURISTIC — NOT BACKTEST-FITTED. A positive historical mean is NOT 'validated profitable'.",
		csvCommentPrefix + "regime_provenance: " + string(r.Provenance),
		csvCommentPrefix + "conclusions_hold_under: " + collapse(r.Holds),
	}
	for i, c := range r.Caveats {
		out = append(out, fmt.Sprintf("%scaveat_%d: %s", csvCommentPrefix, i+1, collapse(c)))
	}
	out = append(out,
		csvCommentPrefix+"population: rows are EP-9 §18 EXECUTABLE (BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT / TOO_EXTENDED) unless the segment says otherwise.",
		csvCommentPrefix+"population_class: EXECUTABLE rows and NON_EXECUTABLE_COUNTERFACTUAL rows are distinguished by the `population` column; a NON_EXECUTABLE_COUNTERFACTUAL row is POST_HOC_DISCOVERY and was in no headline metric.",
		csvCommentPrefix+study.FillVsSignalCloseBpsConvention,
	)
	// EP-10: the same facts the SUMMARY carries, as NAMED KEYS on the CSV's own face.
	//
	// The block above already SAYS these things in prose, and prose is what a reader skims
	// past. A key=value line is what a reader greps for and what a script can assert, and the
	// four keys below are the ones a detached metrics table is most likely to be read against:
	// nobody who finds this file later can assume it describes production execution.
	out = append(out,
		csvCommentPrefix+"methodology_version: "+nonEmpty(r.Meta.MethodologyVersion),
		csvCommentPrefix+"entryplan_rule_version: "+nonEmpty(r.Meta.RuleVersion),
		csvCommentPrefix+"regime_study_arm: REPLAYED_PIT (metrics exist for this arm only; the regime-blind pass is a coverage statement)",
		csvCommentPrefix+"execution_arm: see the `arm` column; wait_window: see the `wait` column; horizon: 5 / 10 / 20 sessions AFTER the fill session",
		csvCommentPrefix+"adjustment_cohort: see the segment rows CLEAN / EVENT_IN_WINDOW / NO_ADJUSTED_SERIES",
		csvCommentPrefix+"production_historical_execution: NOT_EVALUABLE",
		csvCommentPrefix+"replay_not_production_wired: true",
		csvCommentPrefix+"archived_regime_n: 0",
		csvCommentPrefix+"posture: UNKNOWN",
		csvCommentPrefix+"posture_dependent_rules: NOT_EVALUABLE",
		csvCommentPrefix+"breadth_survivorship_bias: true",
	)
	return out
}

func nonEmpty(s string) string {
	if s == "" {
		return "(absent)"
	}
	return s
}

// collapse flattens a sentence onto one line so it cannot break the comment block.
func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func writeCSV(path string, r *study.Run) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("csv: %v\n", err)
		return
	}
	defer f.Close()
	for _, line := range csvProvenanceBlock(r) {
		if _, err := fmt.Fprintln(f, line); err != nil {
			fmt.Printf("csv provenance: %v\n", err)
			return
		}
	}
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"regime_provenance", "population", "arm", "executable", "wait", "segment", "n_candidate", "n_filled",
		"n_missed", "n_not_applicable", "fill_rate_pct", "missed_rate_pct",
		"ret5_n", "ret5_mean", "ret5_median", "ret5_p25", "ret5_p75", "ret5_win",
		"ret10_n", "ret10_mean", "ret10_median", "ret10_p25", "ret10_p75", "ret10_win",
		"ret20_n", "ret20_mean", "ret20_median", "ret20_p25", "ret20_p75", "ret20_win",
		"mfe20_n", "mfe20_mean", "mfe20_median", "mae20_n", "mae20_mean", "mae20_median",
		"invalidation_hit_pct", "invalidation_den", "t1_hit_pct", "t1_den",
		"t2_hit_pct", "t2_den", "stopfirst_conservative_pct", "stopfirst_optimistic_pct",
		"ambiguous_n", "fill_vs_close_bps_mean", "fill_vs_close_bps_median", "pending_truncated"})

	emit := func(m study.Metrics) {
		g := func(h int) study.Dist { return m.Returns[h] }
		pop := m.Population
		if pop == "" {
			pop = "EXECUTABLE (EP-9 §18 headline)"
		}
		row := []string{string(r.Provenance), pop, string(m.Arm), strconv.FormatBool(m.Executable()),
			strconv.Itoa(m.Wait), m.Segment,
			strconv.Itoa(m.NCandidate), strconv.Itoa(m.NFilled), strconv.Itoa(m.NMissed),
			strconv.Itoa(m.NNotApplicable), num(m.FillRate.Pct, m.FillRate.Denominator),
			num(m.MissedRate.Pct, m.MissedRate.Denominator)}
		for _, h := range study.Horizons {
			d := g(h)
			row = append(row, strconv.Itoa(d.N), num(d.Mean, d.N), num(d.Median, d.N),
				num(d.P25, d.N), num(d.P75, d.N), num(d.WinRate, d.N))
		}
		row = append(row, strconv.Itoa(m.MFE20.N), num(m.MFE20.Mean, m.MFE20.N), num(m.MFE20.Median, m.MFE20.N),
			strconv.Itoa(m.MAE20.N), num(m.MAE20.Mean, m.MAE20.N), num(m.MAE20.Median, m.MAE20.N),
			num(m.InvalidationHitRate.Pct, m.InvalidationHitRate.Denominator),
			strconv.Itoa(m.InvalidationHitRate.Denominator),
			num(m.Target1HitRate.Pct, m.Target1HitRate.Denominator), strconv.Itoa(m.Target1HitRate.Denominator),
			num(m.Target2HitRate.Pct, m.Target2HitRate.Denominator), strconv.Itoa(m.Target2HitRate.Denominator),
			num(m.StopFirstRateConservative.Pct, m.StopFirstRateConservative.Denominator),
			num(m.StopFirstRateOptimistic.Pct, m.StopFirstRateOptimistic.Denominator),
			strconv.Itoa(m.FirstTouchAmbiguous),
			num(m.FillVsSignalCloseBps.Mean, m.FillVsSignalCloseBps.N),
			num(m.FillVsSignalCloseBps.Median, m.FillVsSignalCloseBps.N),
			strconv.Itoa(m.PendingTruncated))
		_ = w.Write(row)
	}

	keys := make([]string, 0, len(r.Cells))
	for k := range r.Cells {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ne, ok := r.NonExecutable[k]; ok {
			emit(ne)
		}
		c := r.Cells[k]
		emit(c.Overall)
		for _, seg := range []map[string]study.Metrics{c.ByStatus, c.BySeman, c.ByRegime, c.ByPolicy, c.ByStratum} {
			sk := make([]string, 0, len(seg))
			for s := range seg {
				sk = append(sk, s)
			}
			sort.Strings(sk)
			for _, s := range sk {
				emit(seg[s])
			}
		}
	}
}

func renderSummary(a *Artifact) string {
	var b strings.Builder
	p := func(f string, v ...any) { fmt.Fprintf(&b, f, v...) }

	p("# EP-9 Phase 2b — EntryPlan outcome study\n\n")
	p("**%s**\n\n", a.NotBacktestFitted)
	p("**Conclusions hold under:** %s\n\n", a.ConclusionsHoldUnder)
	for _, c := range a.Caveats {
		p("- %s\n", c)
	}
	p("\nBaseline commit `%s` · rule version `%s` · methodology `%s`\n", a.BaselineCommit, a.RuleVersion, a.MethodologyVersion)
	p("Dataset %s .. %s, %d sessions, %d symbols loaded / %d observed.\n", a.DatasetFrom, a.DatasetTo,
		a.Sessions, a.SymbolsLoaded, a.SymbolsObserved)
	p("Replay digest `%s`, cache digest `%s`. Digests cover: %s\n\n",
		short(a.ReplayDigest), short(a.CacheDigest), a.DigestCovers)

	p("## Dataset drift — read this before comparing anything across runs\n\n")
	p("```\ncache newest session   %s\nsession axis           %d\neligible sessions      %d\n"+
		"covered (this study)   %d\ndataset range          %s .. %s\nprice cache digest     %s\n"+
		"replay archive digest  %s\n```\n\n",
		a.Drift.CacheNewestSession, a.Drift.AxisSessions, a.Drift.EligibleSessions,
		a.Drift.CoveredSessions, a.DatasetFrom, a.DatasetTo, a.CacheDigest, a.ReplayDigest)
	p("%s\n\n", a.Drift.Note)

	p("## Population: what the headline numbers describe (EP-9 §18)\n\n")
	p("**Every headline metric below covers EXECUTABLE plans only** — those whose published\n")
	p("`EntryStatus` is `BUY_NOW`, `WAIT_PULLBACK`, `WAIT_BREAKOUT` or `TOO_EXTENDED`. A plan\n")
	p("that published an entry zone but whose STATUS is `INSUFFICIENT_DATA` or `NO_VALID_ENTRY`\n")
	p("was never published as an entry, so averaging it into an execution arm would answer a\n")
	p("different question from the one these tables claim to answer.\n\n")
	if a.PIT != nil {
		f := a.Funnels["REPLAYED_PIT"]
		ex := headlineN(a)
		nonEx := f.WithZone - ex
		p("```\nplans publishing an entry zone (funnel)      %6d\n", f.WithZone)
		p("  less NON-EXECUTABLE status (§18 excluded)  %6d\n", nonEx)
		p("  = EXECUTABLE headline population           %6d\n```\n\n", ex)
		p("The %d excluded plans are **not deleted**: they have their own section, ", nonEx)
		p("[Non-executable block](#non-executable-block-18-excluded), reporting their counts,\n")
		p("fill rate and return distribution in full. They are in **no** headline metric, **no**\n")
		p("hit-rate denominator and **not** in the Q5 split. The funnel above them keeps counting\n")
		p("all %d zone-publishing plans, which is why the two numbers differ and why the\n", f.WithZone)
		p("subtraction is printed rather than implied.\n\n")
	}

	p("## §28 — the eleven questions\n\n")
	p("%s\n\n", answerSpine(a))

	p("## Data-integrity events worth knowing about\n\n")
	p("These were found by this study's own §24 integrity pass and fixed in METHODOLOGY, not by\n")
	p("cleaning rows. They are here rather than only in the integrity section because each one\n")
	p("silently corrupted a number before it was caught.\n\n")
	p("- **One stock was counted twice in every metric until de-duplication was added.** The price\n")
	p("  cache holds both `5236_TW.json` and `5236_TWO.json` — the same listing before and after a\n")
	p("  transfer between the exchanges — so 5236 entered every session as two observations and\n")
	p("  carried double weight in every mean, median, rate and count. It surfaced as 324\n")
	p("  `DUPLICATE_IDENTITY` defects over a 60-session slice. The fix is a deterministic\n")
	p("  de-duplication (newest last bar wins, then the longer series, then the filename) and the\n")
	p("  dropped file is RECORDED rather than discarded. Duplicates dropped in this run: %d.\n", a.DroppedDuplicates)
	p("- **The machine-readable run file did not write at all on the first full-scale run.**\n")
	p("  `encoding/json` refuses NaN, and an empty rate's percentage is NaN by design (0.0%% would\n")
	p("  assert that nothing was hit out of something). Fixed by serializing an empty rate as\n")
	p("  `null`. A study that cannot write its own result is a study nobody can check.\n")
	p("- **The MISSED arm briefly reported the zone arm's FILLED returns.** Arm D was built by\n")
	p("  relabelling arm B's rows, so a table of filled-trade outcomes carried the label MISSED.\n")
	p("  Arm D now publishes only MissedRate and every distribution in it is N=0.\n\n")

	p("## Coverage funnel\n\n")
	for _, name := range []string{"REPLAYED_PIT", "REGIME_BLIND"} {
		f := a.Funnels[name]
		if f == nil {
			continue
		}
		p("### %s (%d sessions)\n\n```\n", name, f.Sessions)
		p("candidate stock-session observations  %8d\n", f.Candidates)
		p("scanner observations available        %8d\n", f.ScannerObservations)
		p("  EntrySemantic resolved              %8d\n", f.SemanticResolved)
		p("  EntrySemantic unresolved            %8d\n", f.SemanticUnresolved)
		p("plans generated                       %8d\n", f.PlansGenerated)
		for _, l := range f.StatusLines() {
			p("    %s\n", l)
		}
		p("plans with IdealEntry zone            %8d\n", f.WithZone)
		p("plans with MaxChasePrice              %8d\n", f.WithMaxChase)
		p("plans with Invalidation               %8d\n", f.WithInvalidation)
		p("plans with Target1                    %8d\n", f.WithTarget1)
		p("plans with Target2                    %8d\n", f.WithTarget2)
		p("plans with suitable valuation ceiling %8d\n", f.WithSuitableValuationCeiling)
		if err := f.Reconcile(); err != nil {
			p("RECONCILE FAILED: %v\n", err)
		} else {
			p("reconciles: OK\n")
		}
		p("```\n\n")
	}

	if a.PIT == nil {
		return b.String()
	}

	p("## Strata (adjustment), MA60 counterfactual, statuses\n\n```\n")
	for _, s := range study.AllStrata {
		p("%-20s %8d\n", s, a.PIT.StratumCounts[s])
	}
	p("\n")
	for _, m := range study.AllMA60Labels {
		p("%-28s %8d\n", m, a.PIT.MA60Counts[m])
	}
	p("```\n\n")

	p("### Plan reason census (why the MA60 answer is what it is)\n\n```\n")
	rk := make([]string, 0, len(a.PIT.ReasonCounts))
	for k := range a.PIT.ReasonCounts {
		rk = append(rk, k)
	}
	sort.Slice(rk, func(i, j int) bool { return a.PIT.ReasonCounts[rk[i]] > a.PIT.ReasonCounts[rk[j]] })
	for i, k := range rk {
		if i >= 20 {
			p("... and %d more codes\n", len(rk)-20)
			break
		}
		p("%-46s %9d\n", k, a.PIT.ReasonCounts[k])
	}
	p("```\n\n")

	p("## Metrics per arm x wait window (REPLAYED_PIT only)\n\n")
	for _, w := range study.WaitWindows {
		p("### Wait window W=%d\n\n", w)
		p("| arm | executable | N cand | N filled | N missed | N n/a | fill rate | Return@5 | Return@10 | Return@20 |\n")
		p("|---|---|---:|---:|---:|---:|---|---|---|---|\n")
		for _, arm := range study.AllArms {
			c, ok := a.PIT.Cells[study.CellKey(arm, w)]
			if !ok {
				continue
			}
			m := c.Overall
			if m.Arm == study.ArmMissed {
				// Arm D fabricates no fill, so it publishes no outcome. Only MissedRate can
				// be read off it and the outcome columns say so rather than showing a dash a
				// reader might mistake for "measured, and it was zero".
				p("| %s | %v | %d | — | %d | %d | missed %s | (no fill, no outcome) | (no fill, no outcome) | (no fill, no outcome) |\n",
					m.Arm, m.Executable(), m.NCandidate, m.NMissed, m.NNotApplicable, rate(m.MissedRate))
				continue
			}
			p("| %s | %v | %d | %d | %d | %d | %s | %s | %s | %s |\n", m.Arm, m.Executable(),
				m.NCandidate, m.NFilled, m.NMissed, m.NNotApplicable, rate(m.FillRate),
				dist(m.Returns[5]), dist(m.Returns[10]), dist(m.Returns[20]))
		}
		p("\n| arm | MFE20 | MAE20 | invalidation hit | T1 hit | T2 hit | stop-first (cons/opt) | ambiguous | fill vs close bps |\n")
		p("|---|---|---|---|---|---|---|---:|---|\n")
		for _, arm := range study.AllArms {
			c, ok := a.PIT.Cells[study.CellKey(arm, w)]
			if !ok || arm == study.ArmMissed {
				continue
			}
			m := c.Overall
			p("| %s | %s | %s | %s | %s | %s | %s / %s | %d | mean %s median %s |\n", m.Arm,
				dist(m.MFE20), dist(m.MAE20), rate(m.InvalidationHitRate),
				rate(m.Target1HitRate), rate(m.Target2HitRate),
				rate(m.StopFirstRateConservative), rate(m.StopFirstRateOptimistic),
				m.FirstTouchAmbiguous,
				num(m.FillVsSignalCloseBps.Mean, m.FillVsSignalCloseBps.N),
				num(m.FillVsSignalCloseBps.Median, m.FillVsSignalCloseBps.N))
		}
		p("\n")
	}

	p("**Fill price sign convention.** %s\n\n", study.FillVsSignalCloseBpsConvention)

	p("## §28 Q5 — opportunity cost of a missed entry\n\n")
	p("| W | zone MISSED N | benchmark Return@20 (missed) | zone FILLED N | benchmark Return@20 (filled) |\n")
	p("|---:|---:|---|---:|---|\n")
	for _, o := range a.PIT.Opportunity {
		p("| %d | %d | %s | %d | %s |\n", o.Wait, o.MissedN,
			dist(o.MissedBenchmark.Returns[20]), o.FilledN, dist(o.FilledBenchmark.Returns[20]))
	}
	p("\n| W | benchmark Return@5 (missed) | Return@5 (filled) | Return@10 (missed) | Return@10 (filled) |\n")
	p("|---:|---|---|---|---|\n")
	for _, o := range a.PIT.Opportunity {
		p("| %d | %s | %s | %s | %s |\n", o.Wait,
			dist(o.MissedBenchmark.Returns[5]), dist(o.FilledBenchmark.Returns[5]),
			dist(o.MissedBenchmark.Returns[10]), dist(o.FilledBenchmark.Returns[10]))
	}
	if len(a.PIT.Opportunity) > 0 {
		p("\n%s\n\n", a.PIT.Opportunity[0].Note)
	}

	p("## Segments (arm B, W=5)\n\n")
	if c, ok := a.PIT.Cells[study.CellKey(study.ArmZoneLimit, 5)]; ok {
		segTable(&b, "By EntryStatus", c.ByStatus)
		segTable(&b, "By EntrySemantic", c.BySeman)
		segTable(&b, "By regime (REPLAYED_PIT)", c.ByRegime)
		segTable(&b, "By published Plan policy", c.ByPolicy)
		segTable(&b, "By adjustment stratum", c.ByStratum)
	}

	p("## §22 — zone fills vs chase-only fills\n\n")
	p("| W | zone fills N | chase-only N | zone Return@20 | chase-only Return@20 | zone MAE20 | chase-only MAE20 | zone inval hit | chase-only inval hit | interpretable |\n")
	p("|---:|---:|---:|---|---|---|---|---|---|---|\n")
	for _, c := range a.PIT.Comparison22 {
		p("| %d | %d | %d | %s | %s | %s | %s | %s | %s | %v |\n", c.Wait, c.ZoneN, c.ChaseOnlyN,
			dist(c.ZoneFills.Returns[20]), dist(c.ChaseOnly.Returns[20]),
			dist(c.ZoneFills.MAE20), dist(c.ChaseOnly.MAE20),
			rate(c.ZoneFills.InvalidationHitRate), rate(c.ChaseOnly.InvalidationHitRate),
			c.Interpretable)
	}
	if len(a.PIT.Comparison22) > 0 {
		p("\n%s\n\n", a.PIT.Comparison22[0].Note)
	}

	p("## §23 — executable arms vs the SIGNAL_CLOSE benchmark\n\n")
	p("| arm | W | comparison | arm Return@20 | benchmark Return@20 |\n|---|---:|---|---|---|\n")
	for _, c := range a.PIT.Comparison23 {
		p("| %s | %d | unconditional | %s | %s |\n", c.Arm, c.Wait,
			dist(c.ArmUnconditional.Returns[20]), dist(c.BaselineUnconditional.Returns[20]))
		p("| %s | %d | matched | %s | %s |\n", c.Arm, c.Wait,
			dist(c.ArmMatched.Returns[20]), dist(c.BaselineMatched.Returns[20]))
	}
	if len(a.PIT.Comparison23) > 0 {
		p("\n%s\n\n", a.PIT.Comparison23[0].SelectionEffectNote)
	}

	p("## §24 integrity\n\n```\nrows checked %d, defects %d\n", a.PIT.Integrity.Checked, a.PIT.Integrity.Total())
	codes := make([]string, 0, len(a.PIT.Integrity.ByCode))
	for c := range a.PIT.Integrity.ByCode {
		codes = append(codes, string(c))
	}
	sort.Strings(codes)
	for _, c := range codes {
		p("%-32s %6d\n", c, a.PIT.Integrity.ByCode[study.IntegrityCode(c)])
	}
	p("```\n\n")

	p("## Non-executable block (§18 excluded)\n\n")
	p("**NOT PART OF ANY HEADLINE METRIC.** These plans published an entry zone — the zone STEP\n")
	p("succeeded — but the VERDICT was never reached, so the layer never published them as\n")
	p("entries. They are reported here in full because the reason they exist is a missing\n")
	p("evaluator rather than a fact about the stocks.\n\n")
	p("`entryplan`'s own `ReasonStatusRequirementNotEvaluated` (internal/entryplan/reasons.go:569-585)\n")
	p("states it: `NEAR_SUPPORT` and `ELEVATED_EVIDENCE` are \"conditions nothing in this repo\n")
	p("evaluates\", so `SIDEWAYS` and `DISTRIBUTION` cannot produce `BUY_NOW` today\n")
	p("(internal/entryplan/decide.go:484-490). That is a statement about a MISSING EVALUATOR.\n\n")
	p("Status census of the excluded block:\n\n```\n")
	for _, k := range sortedKeys(a.PIT.NonExecutableStatuses) {
		p("%-24s %8d\n", k, a.PIT.NonExecutableStatuses[k])
	}
	p("```\n\nReason census of the excluded block (top codes):\n\n```\n")
	neReasons := sortedByCount(a.PIT.NonExecutableReasons)
	for i, k := range neReasons {
		if i >= 10 {
			break
		}
		p("%-46s %8d\n", k, a.PIT.NonExecutableReasons[k])
	}
	p("```\n\n| W | N cand | N filled | fill rate | Return@20 | MFE20 | MAE20 |\n")
	p("|---:|---:|---:|---|---|---|---|\n")
	for _, w := range study.WaitWindows {
		m, ok := a.PIT.NonExecutable[study.CellKey(study.ArmZoneLimit, w)]
		if !ok {
			continue
		}
		p("| %d | %d | %d | %s | %s | %s | %s |\n", w, m.NCandidate, m.NFilled,
			rate(m.FillRate), dist(m.Returns[20]), dist(m.MFE20), dist(m.MAE20))
	}
	p("\n")

	p("## CANDIDATE_FOLLOWUP\n\n")
	p("Every item is EVIDENCE WITH A POINTER, **not an implemented change**. EP-9 tunes nothing:\n")
	p("the 0.5-ATR zone half-width, the chase ATR multipliers, the 2R/3.5R targets, the\n")
	p("`BaseQualityScore >= 60` gate, the regime permission table and the status precedence are\n")
	p("all INPUTS here and none was touched. Each item inherits the replayed-regime caveat.\n\n")
	p("| # | Candidate | Evidence from this run |\n|---:|---|---|\n")
	for i, c := range candidateFollowups(a) {
		p("| %d | %s | %s |\n", i+1, c.what, c.evidence)
	}
	p("\n")

	p("## Regime-blind pass (COVERAGE ONLY — no metrics)\n\n```\n%s\n", a.Blind.Holds)
	p("observations %d\n", total(a.Blind.StatusCounts))
	sk := make([]string, 0, len(a.Blind.StatusCounts))
	for k := range a.Blind.StatusCounts {
		sk = append(sk, k)
	}
	sort.Strings(sk)
	for _, k := range sk {
		p("  %-20s %8d\n", k, a.Blind.StatusCounts[k])
	}
	p("blind-only eligible sessions excluded from BOTH passes so the two tables share a set: %d\n", a.BlindOnly)
	p("```\n")
	return b.String()
}

func segTable(b *strings.Builder, title string, seg map[string]study.Metrics) {
	fmt.Fprintf(b, "### %s\n\n| segment | N cand | N filled | fill rate | Return@20 | MFE20 | MAE20 | inval hit |\n", title)
	fmt.Fprintf(b, "|---|---:|---:|---|---|---|---|---|\n")
	keys := make([]string, 0, len(seg))
	for k := range seg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := seg[k]
		note := ""
		if m.NFilled > 0 && m.NFilled < 30 {
			note = " ⚠ tiny cell — no conclusion"
		}
		fmt.Fprintf(b, "| %s%s | %d | %d | %s | %s | %s | %s |\n", k, note, m.NCandidate,
			m.NFilled, rate(m.FillRate), dist(m.Returns[20]), dist(m.MFE20), dist(m.MAE20),
		)
	}
	fmt.Fprintf(b, "\n")
}

func total(m map[string]int) int {
	var n int
	for _, v := range m {
		n += v
	}
	return n
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

var _ = entryplan.RuleVersion
