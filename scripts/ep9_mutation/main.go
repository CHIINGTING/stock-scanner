// Command ep9_mutation performs the EP-9 mutation audit (the outcome-study contract and engine)
// against the dirty source tree.
//
// Style follows scripts/ep8_mutation. Each mutant is one or more exact replacements in ONE file.
// For every mutant the command: requires every anchor to occur exactly once; requires at least one
// changed line to be code rather than blank or a // comment; requires the SHA-256 to change;
// requires `go build ./...` to pass on the mutant (otherwise INVALID); runs the named focused test
// anchored with ^…$; restores the file and verifies its SHA-256 equals the baseline before
// continuing. A mutant counts as KILLED only when Go reports an assertion failure in the intended
// test (`--- FAIL: <Test> `). Before the first mutant it records the SHA-256 of every watched file
// and runs every distinct test command once on the unmutated tree; after the last mutant it
// re-verifies every watched file.
//
// It then writes docs/EP9_MUTATION_AUDIT.md from what it measured, including a comparison of the
// hash blocks in the earlier audit documents with the current SHA-256 of the files they list.
// Nothing in that file's tables is typed by hand.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type mutation struct {
	id, description, file, old, replacement string
	pkg, test                               string
	also                                    [][2]string
}

const (
	temporalFile = "internal/entryplanbacktest/temporal.go"
	outcomeFile  = "internal/entryplanbacktest/outcome.go"
	identityFile = "internal/entryplanbacktest/identity.go"
	fillFile     = "internal/entryplanbacktest/study/fill.go"
	measureFile  = "internal/entryplanbacktest/study/outcome.go"
	aggFile      = "internal/entryplanbacktest/study/aggregate.go"
	metricsFile  = "internal/entryplanbacktest/study/metrics.go"
	strataFile   = "internal/entryplanbacktest/study/strata.go"
	ma60File     = "internal/entryplanbacktest/study/ma60.go"
	integFile    = "internal/entryplanbacktest/study/integrity.go"
	universeFile = "internal/entryplanbacktest/recon/universe.go"
	runFile      = "internal/entryplanbacktest/study/run.go"
	observeFile  = "internal/entryplanbacktest/study/observe.go"

	pkgContract = "./internal/entryplanbacktest"
	pkgStudy    = "./internal/entryplanbacktest/study"
	pkgRecon    = "./internal/entryplanbacktest/recon"
)

var mutations = []mutation{
	// ── SAME-BAR FILL ─────────────────────────────────────────────────────────────────────
	{"M1", "allow a fill on the signal bar T (the contract's fourth line)", temporalFile,
		"\treturn i + 1, EligibleForExecution",
		"\treturn i, EligibleForExecution",
		pkgContract, "TestTheSignalBarCanNeverBeFilled", nil},
	{"M1b", "let the execution window start on T instead of T+1", temporalFile,
		"\tlast := first + waitSessions - 1",
		"\tfirst, last := first-1, first+waitSessions-1",
		pkgContract, "TestTheExecutionWindowNeverIncludesTheSignalBar", nil},
	{"M1c", "a zero wait window is clamped to 1 instead of refused (a same-bar fill wearing a clamp)", temporalFile,
		"\tif waitSessions < 1 {\n\t\treturn -1, -1, SessionUnavailable\n\t}",
		"\tif waitSessions < 1 {\n\t\twaitSessions = 1\n\t}",
		pkgContract, "TestAZeroOrNegativeWaitWindowIsRefusedRatherThanClamped", nil},
	{"M1d", "the study's limit simulator scans from the signal bar", fillFile,
		"\tsigIdx := lo - 1\n\tfor i := lo; i <= hi && i < len(bars); i++ {",
		"\tsigIdx := lo - 1\n\tfor i := sigIdx; i <= hi && i < len(bars); i++ {",
		pkgStudy, "TestL1SignalBarNeverFillsEvenWhenItsLowIsInsideTheZone", nil},

	// ── T+1 OFF BY ONE ────────────────────────────────────────────────────────────────────
	{"M2", "skip T+1 and make T+2 the earliest executable session", temporalFile,
		"\tif i+1 >= s.BarCount() {\n\t\treturn -1, NoFollowingSession\n\t}\n\treturn i + 1, EligibleForExecution",
		"\tif i+2 >= s.BarCount() {\n\t\treturn -1, NoFollowingSession\n\t}\n\treturn i + 2, EligibleForExecution",
		pkgContract, "TestTheSignalBarCanNeverBeFilled", nil},
	{"M2b", "a Friday signal resolves to the calendar next day rather than the next session", temporalFile,
		"\ti, ok := a.idx[date]\n\tif !ok || i+1 >= len(a.dates) {\n\t\treturn \"\", false\n\t}\n\treturn a.dates[i+1], true",
		"\ti, ok := a.idx[date]\n\tif !ok || i+2 >= len(a.dates) {\n\t\treturn \"\", false\n\t}\n\treturn a.dates[i+2], true",
		pkgContract, "TestAFridaySignalIsEligibleOnTheNextSessionNotOnSaturday", nil},
	{"M2c", "off-by-one in SessionsWaited, so a T+1 fill reports 0 sessions waited", fillFile,
		"Price: price, SessionsWaited: i - sigIdx}",
		"Price: price, SessionsWaited: i - sigIdx - 1}",
		pkgStudy, "TestL2TPlusOneIsTheFirstEligibleSession", nil},

	// ── FIRST ELIGIBLE FILL ───────────────────────────────────────────────────────────────
	{"M3", "take the BEST price in the window instead of the first eligible fill", fillFile,
		"\t\treturn Fill{Outcome: FillFilled, BarIndex: i, Date: sessionDate(bars, i),\n\t\t\tPrice: price, SessionsWaited: i - sigIdx}\n\t}\n\treturn Fill{Outcome: FillMissed, BarIndex: -1}",
		"\t\tif best.Outcome != FillFilled || price < best.Price {\n\t\t\tbest = Fill{Outcome: FillFilled, BarIndex: i, Date: sessionDate(bars, i),\n\t\t\t\tPrice: price, SessionsWaited: i - sigIdx}\n\t\t}\n\t}\n\tif best.Outcome == FillFilled {\n\t\treturn best\n\t}\n\treturn Fill{Outcome: FillMissed, BarIndex: -1}",
		pkgStudy, "TestE4TheFirstEligibleSessionWinsAndALaterBetterPriceIsNotTaken",
		[][2]string{{"\tsigIdx := lo - 1\n", "\tsigIdx := lo - 1\n\tvar best Fill\n"}}},
	{"M3b", "a gap down fills at the limit rather than at the better open", fillFile,
		"\t\tprice := limit\n\t\tif b.Open > 0 && b.Open <= limit {\n\t\t\tprice = b.Open\n\t\t}",
		"\t\tprice := limit",
		pkgStudy, "TestE2AGapDownFillsAtTheOpenNotAtTheLimit", nil},
	{"M3c", "the limit touch is exclusive, so a print AT the limit does not fill", fillFile,
		"\t\tif b.Low > limit {\n\t\t\tcontinue\n\t\t}",
		"\t\tif b.Low >= limit {\n\t\t\tcontinue\n\t\t}",
		pkgStudy, "TestE3TheTouchIsInclusiveAndOneTickAboveIsNot", nil},

	// ── MISSED FABRICATION ────────────────────────────────────────────────────────────────
	{"M4", "fabricate a fill at the last bar of the window for a MISSED order", fillFile,
		"\treturn Fill{Outcome: FillMissed, BarIndex: -1}\n}",
		"\tif hi >= lo && hi < len(bars) && bars[hi].Close > 0 {\n\t\treturn Fill{Outcome: FillFilled, BarIndex: hi,\n\t\t\tDate: sessionDate(bars, hi), Price: bars[hi].Close, SessionsWaited: hi - sigIdx}\n\t}\n\treturn Fill{Outcome: FillMissed, BarIndex: -1}\n}",
		pkgStudy, "TestL7MissedFabricatesNoFillAndNoReturn", nil},
	{"M4b", "a plan that published no price is MISSED rather than NOT_APPLICABLE", aggFile,
		"\t\tif o.ZoneHigh == nil {\n\t\t\tr.Fill = Fill{Outcome: FillNotApplicable, BarIndex: -1}",
		"\t\tif o.ZoneHigh == nil {\n\t\t\tr.Fill = Fill{Outcome: FillMissed, BarIndex: -1}",
		pkgStudy, "TestE7NoPublishedPriceIsNotApplicableRatherThanMissed", nil},

	// ── HORIZON OFF BY ONE ────────────────────────────────────────────────────────────────
	{"M5", "count the fill session as session 1, so Return@5 reads F+4", outcomeFile,
		"\ti := fillIdx + h\n\tif i >= barCount {",
		"\ti := fillIdx + h - 1\n\tif i >= barCount {",
		pkgContract, "TestTheHorizonConventionCountsSessionsAfterTheFill", nil},
	{"M5b", "Return@0 becomes a forward return (the fill session's own close)", outcomeFile,
		"\tif fillIdx < 0 || h < 1 || barCount <= 0 {",
		"\tif fillIdx < 0 || h < 0 || barCount <= 0 {",
		pkgContract, "TestTheFillSessionsOwnCloseIsNotAForwardReturn", nil},
	{"M5c", "a horizon past the data is answered with the newest bar instead of being absent", outcomeFile,
		"\ti := fillIdx + h\n\tif i >= barCount {\n\t\treturn -1, false\n\t}",
		"\ti := fillIdx + h\n\tif i >= barCount {\n\t\treturn barCount - 1, true\n\t}",
		pkgContract, "TestAHorizonPastTheDataIsUnavailableAndNotTruncated", nil},

	// ── MFE / MAE WINDOW ──────────────────────────────────────────────────────────────────
	{"M6", "include the fill bar's own High/Low in the excursion window", outcomeFile,
		"\tlo = fillIdx + 1\n\thi = fillIdx + ExcursionSessions",
		"\tlo = fillIdx\n\thi = fillIdx + ExcursionSessions",
		pkgContract, "TestTheExcursionWindowIsTwentySessionsAndExcludesTheFillSession", nil},
	{"M6b", "shorten the excursion window to ten sessions", outcomeFile,
		"const ExcursionSessions = 20",
		"const ExcursionSessions = 10",
		pkgContract, "TestTheExcursionWindowIsTwentySessionsAndExcludesTheFillSession", nil},
	{"M6c", "clip MFE at zero, reporting a favourable excursion that never happened", measureFile,
		"\t\tif seen == 0 || up > mfe {\n\t\t\tmfe = up\n\t\t}",
		"\t\tif up > mfe {\n\t\t\tmfe = up\n\t\t}",
		pkgStudy, "TestMFECanBeNegativeAndIsNotClippedAtZero", nil},
	{"M6d", "clip MAE at zero", measureFile,
		"\t\tif seen == 0 || dn < mae {\n\t\t\tmae = dn\n\t\t}",
		"\t\tif dn < mae {\n\t\t\tmae = dn\n\t\t}",
		pkgStudy, "TestMFECanBeNegativeAndIsNotClippedAtZero", nil},
	{"M6e", "a truncated window is treated as complete, hiding a pending trade", outcomeFile,
		"\tif hi >= barCount {\n\t\treturn lo, barCount - 1, WindowTruncated\n\t}",
		"\tif hi >= barCount {\n\t\treturn lo, barCount - 1, WindowComplete\n\t}",
		pkgContract, "TestATruncatedExcursionWindowIsPendingAndNotAnExclusion", nil},

	// ── AMBIGUITY RESOLUTION ──────────────────────────────────────────────────────────────
	{"M7", "resolve a same-bar stop+target as a stop (manufactured pessimism)", outcomeFile,
		"\tcase hitStop && hitTarget:\n\t\treturn TouchAmbiguous",
		"\tcase hitStop && hitTarget:\n\t\treturn TouchStopOnly",
		pkgContract, "TestBothLevelsInsideOneBarIsAmbiguousAndNeverResolved", nil},
	{"M7b", "resolve a same-bar stop+target as a target (manufactured edge)", outcomeFile,
		"\tcase hitStop && hitTarget:\n\t\treturn TouchAmbiguous",
		"\tcase hitStop && hitTarget:\n\t\treturn TouchTargetOnly",
		pkgContract, "TestBothLevelsInsideOneBarIsAmbiguousAndNeverResolved", nil},
	{"M7c", "collapse the two stop-first bounds into one point estimate", aggFile,
		"\tm.StopFirstRateConservative = NewRate(a.ftStop+a.ftAmbig, a.bothDen,",
		"\tm.StopFirstRateConservative = NewRate(a.ftStop, a.bothDen,",
		pkgStudy, "TestAmbiguityIsCountedAndBoundedRatherThanResolved", nil},
	{"M7d", "an unusable bar is read as NO_TOUCH instead of UNAVAILABLE", outcomeFile,
		"\tif !(bar.High > 0) || !(bar.Low > 0) || bar.High < bar.Low {\n\t\treturn TouchUnavailable\n\t}",
		"\tif !(bar.High > 0) || !(bar.Low > 0) || bar.High < bar.Low {\n\t\treturn TouchNone\n\t}",
		pkgContract, "TestAnUnusableBarIsUnavailableAndNotNoTouch", nil},

	// ── DENOMINATOR INFLATION ─────────────────────────────────────────────────────────────
	{"M8", "compute the invalidation hit rate over every filled observation", aggFile,
		"\tm.InvalidationHitRate = NewRate(a.invHits, a.invDen,",
		"\tm.InvalidationHitRate = NewRate(a.invHits, a.nFilled,",
		pkgStudy, "TestLevelHitsUseTheirOwnDenominators", nil},
	{"M8b", "count NOT_APPLICABLE observations into the fill-rate denominator", aggFile,
		"\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\treturn",
		"\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\ta.nCandidate++\n\t\treturn",
		pkgStudy, "TestE8NotApplicableIsOutsideTheDenominatorAndMissedIsInside", nil},
	{"M8c", "count a hit for a level the plan never published", measureFile,
		"\tif invalidation != nil {\n\t\tout.InvalidationHit = boolPtr(touchedBelow(bars, lo, hi, *invalidation))\n\t}",
		"\tif invalidation != nil {\n\t\tout.InvalidationHit = boolPtr(touchedBelow(bars, lo, hi, *invalidation))\n\t} else {\n\t\tout.InvalidationHit = boolPtr(false)\n\t}",
		pkgStudy, "TestL6NoLevelTheePlanDidNotPublishIsEverMeasured", nil},
	{"M8d", "an empty rate reports 0.0% instead of no measurement", metricsFile,
		"\tr := Rate{Hits: hits, Denominator: denom, Population: population, Pct: math.NaN()}",
		"\tr := Rate{Hits: hits, Denominator: denom, Population: population}",
		pkgStudy, "TestRateCarriesItsDenominatorAndRefusesToInventOne", nil},

	// ── DUPLICATE IDENTITY ────────────────────────────────────────────────────────────────
	{"M9", "the observation registry accepts duplicates silently", identityFile,
		"\tif _, dup := s.seen[k]; dup {\n\t\treturn fmt.Errorf(\"entryplanbacktest: duplicate observation %s — an observation \"+\n\t\t\t\"recorded twice doubles that stock-session's weight in every metric\", k)\n\t}",
		"",
		pkgContract, "TestADuplicateObservationFailsLoudly", nil},
	{"M9b", "the identity key drops the arm and the wait window", identityFile,
		"\treturn fmt.Sprintf(\"%s|%s|%s|%s|%d\", o.Symbol, o.SignalAsOf, o.RuleVersion, o.Arm, o.WaitSessions)",
		"\treturn fmt.Sprintf(\"%s|%s|%s\", o.Symbol, o.SignalAsOf, o.RuleVersion)",
		pkgContract, "TestEveryIdentityFieldSeparatesObservations", nil},
	{"M9c", "the integrity checker stops reporting duplicates", integFile,
		"\tif err := ids.Add(id); err != nil {\n\t\tr.add(IntegrityDuplicateIdentity, o, row.Arm, row.Wait, err.Error())\n\t}",
		"\t_ = ids.Add(id)",
		pkgStudy, "TestADuplicateObservationIsADefectAndIsRejected", nil},
	{"M9d", "the cache keeps both files of a duplicated symbol, double-weighting it", universeFile,
		"\t\tif prev, clash := byCode[s.Data.Symbol]; clash {\n\t\t\twin, lose := prev, s\n\t\t\tif betterSeries(s, prev) {\n\t\t\t\twin, lose = s, prev\n\t\t\t}\n\t\t\tbyCode[s.Data.Symbol] = win\n\t\t\tc.DroppedDuplicates[s.Data.Symbol] = lose.file\n\t\t\tcontinue\n\t\t}\n\t\tbyCode[s.Data.Symbol] = s",
		"\t\tbyCode[s.Data.Symbol+s.file] = s",
		pkgRecon, "TestTheCacheDeduplicatesSymbolsDeterministically", nil},

	// ── BLIND / PIT ARM MIXING ────────────────────────────────────────────────────────────
	{"M10", "a missing replay file silently downgrades the PIT arm to regime-blind", "internal/entryplanbacktest/recon/regime.go",
		"\t\tr, err := a.Row(date)\n\t\tif err != nil {\n\t\t\treturn scanner.EntryPlanMarket{Available: false}, entryplanbacktest.RegimeUnavailable, err\n\t\t}",
		"\t\tr, err := a.Row(date)\n\t\tif err != nil {\n\t\t\treturn scanner.EntryPlanMarket{Available: false}, entryplanbacktest.RegimeUnavailable, nil\n\t\t}",
		pkgRecon, "TestTheReplayArmNeverSilentlyDowngradesToBlind", nil},
	{"M10b", "the integrity checker stops noticing a blind row inside a PIT pass", integFile,
		"\tif o.RegimeProvenance != expectedProvenance {",
		"\tif false && o.RegimeProvenance != expectedProvenance {",
		pkgStudy, "TestMixingRegimeProvenanceIsADefect", nil},
	{"M10c", "an UNAVAILABLE regime provenance is read as SIDEWAYS", "internal/entryplanbacktest/provenance.go",
		"\tdefault:\n\t\treturn \"\", false\n\t}\n}",
		"\tdefault:\n\t\treturn model.RegimeSideways, true\n\t}\n}",
		pkgContract, "TestAnUnavailableRegimeIsNeverSidewaysOrUnknown", nil},

	// ── ADJUSTMENT STRATA (user decision 3) ───────────────────────────────────────────────
	{"M11", "a symbol with no adjusted series is reported CLEAN", strataFile,
		"\tcase fetcher.AdjustmentNoAdjustedSeries:\n\t\treturn StratumNoAdjustedSeries",
		"\tcase fetcher.AdjustmentNoAdjustedSeries:\n\t\treturn StratumClean",
		pkgStudy, "TestNoAdjustedSeriesIsItsOwnStratumAndIsNeverReadAsClean", nil},
	{"M11b", "the outcome window is not checked for adjustments", strataFile,
		"\toutClean := fetcher.WindowIsAdjustmentClean(bars[:outEnd], outEnd-signalIdx)",
		"\toutClean := true",
		pkgStudy, "TestAnEventInsideTheOutcomeWindowIsNotClean", nil},
	{"M11c", "the plan window is not checked for adjustments", strataFile,
		"\tplanClean := fetcher.WindowIsAdjustmentClean(bars[:planEnd], PlanWindowBars)",
		"\tplanClean := planEnd >= 0 && PlanWindowBars > 0",
		pkgStudy, "TestAnEventInsideThePlanWindowIsNotClean", nil},

	// ── MA60 COUNTERFACTUAL (§31) ─────────────────────────────────────────────────────────
	{"M12", "count a price-basis mismatch as MA60-blocked, inflating §31", ma60File,
		"var blockingReasons = map[entryplan.Reason]bool{\n\tentryplan.ReasonPullbackLevelsUnavailable:         true,\n\tentryplan.ReasonPullbackLevelNotBelowCurrentPrice: true,\n}",
		"var blockingReasons = map[entryplan.Reason]bool{\n\tentryplan.ReasonPullbackLevelsUnavailable:          true,\n\tentryplan.ReasonPullbackLevelNotBelowCurrentPrice:  true,\n\tentryplan.ReasonPullbackLevelsBasisMismatch:        true,\n}",
		pkgStudy, "TestOnlyTheTwoLevelReasonsCount", nil},
	{"M12b", "label a plan MA60-blocked even when another candidate was eligible", ma60File,
		"\tfor _, c := range p.EntryTrace.Zone.Candidates {\n\t\tif c.Disposition.Eligible() {\n\t\t\treturn MA60BlockedOtherCause\n\t\t}\n\t}",
		"",
		pkgStudy, "TestAnAlreadyEligibleCandidateMeansMA60IsNotTheBlocker", nil},
	{"M12c", "the MA60 counterfactual mutates the plan it classifies", ma60File,
		"\tif p.IdealEntry != nil {\n\t\treturn MA60NotBlocked\n\t}",
		"\tif p.IdealEntry != nil {\n\t\treturn MA60NotBlocked\n\t}\n\tp.Status = entryplan.StatusNoValidEntry",
		pkgStudy, "TestMA60ChangesNoBaselineNumber", nil},

	// ── DISTRIBUTION SUMMARY ──────────────────────────────────────────────────────────────
	{"M13", "p25 and p75 are swapped", metricsFile,
		"\td.P25 = nearestRank(s, 0.25)\n\td.P75 = nearestRank(s, 0.75)",
		"\td.P25 = nearestRank(s, 0.75)\n\td.P75 = nearestRank(s, 0.25)",
		pkgStudy, "TestSummariseOnAHandComputedOddSample", nil},
	{"M13b", "a flat trade counts as a win", metricsFile,
		"\t\tif v > 0 {\n\t\t\twins++\n\t\t}",
		"\t\tif v >= 0 {\n\t\t\twins++\n\t\t}",
		pkgStudy, "TestZeroIsNotAWin", nil},
	{"M13c", "the median of an even sample takes the upper middle instead of the mean of the two", metricsFile,
		"\treturn (sorted[n/2-1] + sorted[n/2]) / 2",
		"\treturn sorted[n/2]",
		pkgStudy, "TestSummariseOnAHandComputedEvenSampleWithNegatives", nil},
	{"M13d", "an empty rate serializes as 0 instead of null", metricsFile,
		"\tif !math.IsNaN(r.Pct) && !math.IsInf(r.Pct, 0) {\n\t\tv := r.Pct\n\t\ta.Pct = &v\n\t}",
		"\tv := r.Pct\n\tif math.IsNaN(v) || math.IsInf(v, 0) {\n\t\tv = 0\n\t}\n\ta.Pct = &v",
		pkgStudy, "TestAnEmptyRateSerializesAsNullAndNotAsZero", nil},

	// ── POINT-IN-TIME RECONSTRUCTION ──────────────────────────────────────────────────────
	{"M14", "the truncation sees one bar past the signal session", universeFile,
		"\t\td.Candles = s.Data.Candles[:i+1]",
		"\t\td.Candles = s.Data.Candles[:i+2]",
		pkgRecon, "TestTruncateAtIsAPointInTimeCut", nil},
	{"M14b", "a symbol that did not trade on the session is carried forward", universeFile,
		"\t\ti, ok := s.IndexOf(date)\n\t\tif !ok || i+1 < minBars {\n\t\t\tcontinue\n\t\t}",
		"\t\ti, ok := s.IndexOf(date)\n\t\tif !ok {\n\t\t\ti = len(s.Data.Candles) - 1\n\t\t}\n\t\tif i+1 < minBars {\n\t\t\tcontinue\n\t\t}",
		pkgRecon, "TestASymbolThatDidNotTradeOnTheSessionIsOmittedNotCarriedForward", nil},
	{"M14c", "the eligible-session list leaves no room for the outcome window", universeFile,
		"\thi := len(c.Axis) - 1 - forward",
		"\thi := len(c.Axis) - 1",
		pkgRecon, "TestEligibleSessionsLeavesRoomForHistoryAndTheOutcomeWindow", nil},
	{"M14d", "the eligibility minimum drops to EnrichWatchlist's 30 bars, truncating the 60-bar pivot", "internal/entryplanbacktest/recon/session.go",
		"const EligibilityBars = 61",
		"const EligibilityBars = 30",
		pkgRecon, "TestEligibilityBarsIsTheProductionRead", nil},
	{"M14e", "the sector projection lets file order beat the ranked rotation order", "internal/entryplanbacktest/recon/session.go",
		"\tfor _, r := range ranked {\n\t\tfor _, code := range p.Members[r.Name] {\n\t\t\tif _, ok := out[code]; !ok {\n\t\t\t\tout[code] = r.Name\n\t\t\t}\n\t\t}\n\t}\n\tfor _, name := range p.Order {",
		"\tfor _, name := range p.Order {\n\t\tfor _, code := range p.Members[name] {\n\t\t\tif _, ok := out[code]; !ok {\n\t\t\t\tout[code] = name\n\t\t\t}\n\t\t}\n\t}\n\tfor _, name := range p.Order {",
		pkgRecon, "TestSectorOfMatchesTheProductionRule", nil},

	// ── RESEARCH ISOLATION ────────────────────────────────────────────────────────────────
	{"M15", "a research reader mutates the plan it reads", "internal/entryplanbacktest/recon/funnel.go",
		"\tf.PlansGenerated++\n\tf.ByStatus[p.Status]++",
		"\tf.PlansGenerated++\n\tp.Caveats = append(p.Caveats, \"read by the funnel\")\n\tf.ByStatus[p.Status]++",
		pkgRecon, "TestTheResearchReadersNeverMutateAPlan", nil},

	// ── THE STUDY DRIVER ITSELF (run.go) ──────────────────────────────────────────────────
	// These exist because a reviewer found three surviving mutants in this file: no test
	// drove Execute, so the accumulators were guarded and the ASSEMBLY of them was not. The
	// first of the three inverts EP-9 §28 Q5, the study's most consequential number.
	{"M16", "invert the Q5 missed/filled split, reversing the study's most consequential verdict", runFile,
		"\t\t\t\tif zone.Fill.Filled() {\n\t\t\t\t\tfilledN++\n\t\t\t\t\tfilledB.Add(&bench)\n\t\t\t\t} else {\n\t\t\t\t\tmissedN++\n\t\t\t\t\tmissedB.Add(&bench)\n\t\t\t\t}",
		"\t\t\t\tif !zone.Fill.Filled() {\n\t\t\t\t\tfilledN++\n\t\t\t\t\tfilledB.Add(&bench)\n\t\t\t\t} else {\n\t\t\t\t\tmissedN++\n\t\t\t\t\tmissedB.Add(&bench)\n\t\t\t\t}",
		pkgStudy, "TestExecuteProducesTheQ5SplitInTheRightDirection", nil},
	{"M16b", "arm D takes outcomes again, so the MISSED row echoes arm B's filled returns", runFile,
		"\t\t\t\td.AddCandidateOnly(&row)",
		"\t\t\t\td.Add(&row)",
		pkgStudy, "TestExecuteLeavesTheMissedArmWithNoOutcome", nil},
	{"M16c", "drop the Q5 population gate, so non-executable plans enter the opportunity split", runFile,
		"\t\t\t\tif !zone.ExecutableStatus {\n\t\t\t\t\tcontinue // EP-9 §18: not part of the headline population\n\t\t\t\t}",
		"",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter", nil},
	{"M16d", "drop the Q5 fill-outcome gate, so NOT_APPLICABLE plans enter the split", runFile,
		"\t\t\t\tif zone.Fill.Outcome != FillFilled && zone.Fill.Outcome != FillMissed {\n\t\t\t\t\tcontinue // NOT_APPLICABLE / UNAVAILABLE: arm B never had an order here\n\t\t\t\t}",
		"",
		pkgStudy, "TestExecuteProducesTheQ5SplitInTheRightDirection", nil},
	{"M16e", "a regime-blind pass computes metrics after all", runFile,
		"\t\tif !r.MetricsHeld {\n\t\t\tcontinue\n\t\t}\n\t\tr.Cells[CellKey(ArmSignalCloseBaseline, w)] = cbA.finish()",
		"\t\tr.Cells[CellKey(ArmSignalCloseBaseline, w)] = cbA.finish()",
		pkgStudy, "TestExecuteComputesNoMetricOnANonPITPass", nil},

	// ── EP-9 §18 POPULATION FILTER ────────────────────────────────────────────────────────
	{"M17", "drop the §18 population filter, so non-executable statuses enter every headline metric", aggFile,
		"\tif r.ExecutableStatus == a.nonExecutable {\n\t\ta.nExcludedByStatus++\n\t\treturn\n\t}\n\tswitch r.Fill.Outcome {\n\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\treturn",
		"\tswitch r.Fill.Outcome {\n\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\treturn",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter", nil},
	{"M17b", "widen the executable population to include INSUFFICIENT_DATA", observeFile,
		"var ExecutableStatuses = map[entryplan.EntryStatus]bool{\n\tentryplan.StatusBuyNow:       true,",
		"var ExecutableStatuses = map[entryplan.EntryStatus]bool{\n\tentryplan.StatusInsufficientData: true,\n\tentryplan.StatusBuyNow:       true,",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter", nil},
	{"M17c", "narrow the executable population by dropping TOO_EXTENDED", observeFile,
		"\tentryplan.StatusTooExtended:  true,\n}",
		"}",
		pkgStudy, "TestTheExecutablePopulationIsTheFourEntryStatuses", nil},
	{"M17d", "the excluded block silently drops its rows instead of counting them", aggFile,
		"\tif r.ExecutableStatus == a.nonExecutable {\n\t\ta.nExcludedByStatus++\n\t\treturn\n\t}\n\tswitch r.Fill.Outcome {\n\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\treturn",
		"\tif r.ExecutableStatus == a.nonExecutable {\n\t\treturn\n\t}\n\tswitch r.Fill.Outcome {\n\tcase FillNotApplicable:\n\t\ta.nNotApplicable++\n\t\treturn",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter", nil},

	{"M17e", "the §22 zone-fill count bypasses the population filter (a parallel counter)", runFile,
		"func (c *compAcc) addZone(r *Row) {\n\tif r.Fill.Filled() {\n\t\tc.acc.Add(r)\n\t}\n}",
		"func (c *compAcc) addZone(r *Row) {\n\tif r.Fill.Filled() {\n\t\tc.acc.Add(r)\n\t\tc.leaked++\n\t}\n}",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter",
		[][2]string{
			{"type compAcc struct {\n\tacc *Acc\n}", "type compAcc struct {\n\tacc    *Acc\n\tleaked int\n}"},
			{"\t\t\t\tZoneN:      zoneFills.NFilled,", "\t\t\t\tZoneN:      zc.leaked,"},
		}},
	{"M17f", "the §22 chase-only count bypasses the population filter", runFile,
		"\t\t\t\tif row.Fill.Filled() && !zoneFilled[key(&obs[i])] {\n\t\t\t\t\tchaseOnly.Add(&row)\n\t\t\t\t}",
		"\t\t\t\tif row.Fill.Filled() && !zoneFilled[key(&obs[i])] {\n\t\t\t\t\tchaseOnly.Add(&row)\n\t\t\t\t\tchaseLeak++\n\t\t\t\t}",
		pkgStudy, "TestExecuteAppliesTheSection18PopulationFilter",
		[][2]string{
			{"\t\t\tchaseOnly := NewAcc()\n", "\t\t\tchaseOnly := NewAcc()\n\t\t\tvar chaseLeak int\n"},
			{"\t\t\t\tChaseOnlyN: chaseOnlyM.NFilled,", "\t\t\t\tChaseOnlyN: chaseLeak,"},
		}},

	// ── POINT-IN-TIME, ON THE SERIALIZED PLAN ─────────────────────────────────────────────
	{"M18", "the reconstruction sees one bar past the signal session (caught on the SERIALIZED PLAN)", universeFile,
		"\t\td.Candles = s.Data.Candles[:i+1]",
		"\t\td.Candles = s.Data.Candles[:i+2]",
		pkgRecon, "TestFutureBarsCannotChangeTheSerializedPlanAtT", nil},

	// ── CSV PROVENANCE ────────────────────────────────────────────────────────────────────
	{"M19", "the metrics CSV loses its provenance comment block", "cmd/ep9-study/render.go",
		"\tfor _, line := range csvProvenanceBlock(r) {\n\t\tif _, err := fmt.Fprintln(f, line); err != nil {\n\t\t\tfmt.Printf(\"csv provenance: %v\\n\", err)\n\t\t\treturn\n\t\t}\n\t}\n",
		"",
		"./cmd/ep9-study", "TestTheMetricsCSVCarriesItsProvenance", nil},
	{"M19b", "the metrics CSV loses its per-row provenance column", "cmd/ep9-study/render.go",
		"\t\trow := []string{string(r.Provenance), pop, string(m.Arm), strconv.FormatBool(m.Executable()),",
		"\t\trow := []string{\"\", pop, string(m.Arm), strconv.FormatBool(m.Executable()),",
		"./cmd/ep9-study", "TestEveryMetricsRowCarriesTheProvenanceColumn", nil},
	{"M19c", "the CSV provenance block drops the survivorship caveat", "cmd/ep9-study/render.go",
		"\tfor i, c := range r.Caveats {\n\t\tout = append(out, fmt.Sprintf(\"%scaveat_%d: %s\", csvCommentPrefix, i+1, collapse(c)))\n\t}",
		"\tfor i, c := range r.Caveats {\n\t\tif i > 0 {\n\t\t\tbreak\n\t\t}\n\t\tout = append(out, fmt.Sprintf(\"%scaveat_%d: %s\", csvCommentPrefix, i+1, collapse(c)))\n\t}",
		"./cmd/ep9-study", "TestTheMetricsCSVCarriesItsProvenance", nil},
}

// watched are hashed before the first mutant and re-verified at the end, in addition to every
// mutated file.
var watched = []string{
	temporalFile, outcomeFile, identityFile,
	"internal/entryplanbacktest/provenance.go",
	"internal/entryplanbacktest/metadata.go",
	"internal/entryplanbacktest/doc.go",
	fillFile, measureFile, aggFile, metricsFile, strataFile, ma60File, integFile,
	universeFile, runFile, observeFile,
	"cmd/ep9-study/render.go",
	"internal/entryplanbacktest/recon/regime.go",
	"internal/entryplanbacktest/recon/session.go",
	"internal/entryplanbacktest/recon/funnel.go",
	"internal/scanner/entryplan_symbol_guard_test.go",
	"internal/scanner/entryplan_attach.go",
	"internal/entryplan/plan.go",
	"internal/entryplan/levels.go",
	"internal/entryplan/policy.go",
	"internal/fetcher/adjustment.go",
}

const auditDoc = "docs/EP9_MUTATION_AUDIT.md"

var priorAudits = []string{"docs/EP6G_MUTATION_AUDIT.md", "docs/EP7_MUTATION_AUDIT.md", "docs/EP8_MUTATION_AUDIT.md"}

var notes = []string{
	"**Coverage is by the ten classes EP-9 named**, not by file: same-bar fill (M1, M1b, M1c, M1d), " +
		"T+1 off-by-one (M2, M2b, M2c), first-eligible-fill (M3, M3b, M3c), missed fabrication (M4, M4b), " +
		"horizon off-by-one (M5, M5b, M5c), MFE/MAE window (M6, M6b, M6c, M6d, M6e), ambiguity resolution " +
		"(M7, M7b, M7c, M7d), denominator inflation (M8, M8b, M8c, M8d), duplicate identity (M9, M9b, M9c, M9d), " +
		"and blind/PIT arm mixing (M10, M10b, M10c).",
	"**M9d is the defect the study's own §24 integrity pass found on its first full-scale run.** " +
		"`.cache` holds both `5236_TW.json` and `5236_TWO.json` — one listing before and after a transfer " +
		"between the exchanges — and without de-duplication that stock entered every session twice and was " +
		"double-weighted in every metric. 324 DUPLICATE_IDENTITY defects over a 60-session slice.",
	"**M13d is the other defect §24 found.** `encoding/json` refuses NaN outright, so an empty rate broke " +
		"the whole machine-readable run file. The mutant restores the 0.0% that `NewRate`'s NaN exists to " +
		"prevent, at the output boundary where it is hardest to notice.",
	"**M7c collapses the two ambiguity bounds.** EP-9 reports a conservative and an optimistic stop-first " +
		"rate rather than a point estimate, because daily OHLC cannot order a stop and a target inside one bar.",
	"**M12c is the §31 hard gate.** The MA60 counterfactual is a labelled COLUMN; a classifier that mutated " +
		"the plan would make the counterfactual an input to the baseline.",
	"**M15 is what the EP-9 entries in `internal/scanner/entryplan_symbol_guard_test.go` promise.** " +
		"Widening that allowlist is only defensible while the research readers are read-only.",
	"**No mutant targets a strategy parameter.** The 0.5-ATR zone half-width, the chase ATR multipliers, the " +
		"2R/3.5R targets, BaseQualityScore >= 60, the regime permission table and the status precedence are " +
		"inputs to EP-9 and are not touched by this audit; EP-9 tunes nothing.",
}

type row struct {
	m                         mutation
	result, test, message     string
	before, mutated, restored string
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func main() {
	root, err := repoRoot()
	must(err)
	started := time.Now()
	fmt.Printf("started %s\n", started.Format("2006-01-02 15:04:05 MST"))
	original := map[string][]byte{}
	for _, f := range watched {
		original[f], err = os.ReadFile(filepath.Join(root, f))
		must(err)
	}
	for _, m := range mutations {
		if _, ok := original[m.file]; ok {
			continue
		}
		original[m.file], err = os.ReadFile(filepath.Join(root, m.file))
		must(err)
	}
	defer restoreAll(root, original)
	files := make([]string, 0, len(original))
	for f := range original {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Printf("baseline %s  %s\n", digest(original[f]), f)
	}

	seen := map[string]bool{}
	for _, m := range mutations {
		key := m.pkg + "\x00" + m.test
		if seen[key] {
			continue
		}
		seen[key] = true
		if out, runErr := run(root, testCmd(m)); runErr != nil {
			fmt.Fprintf(os.Stderr, "baseline failed: %s\n%s", strings.Join(testCmd(m), " "), out)
			os.Exit(2)
		}
	}

	var rows []row
	killed := 0
	for _, m := range mutations {
		path := filepath.Join(root, m.file)
		base := original[m.file]
		must(os.WriteFile(path, base, 0o644))
		r := row{m: m, before: digest(base)}
		mutant, invalid := apply(base, m)
		if invalid != "" {
			r.result, r.message = "INVALID", invalid
			fmt.Printf("| %s | INVALID | %s |\n", m.id, invalid)
			rows = append(rows, r)
			continue
		}
		must(os.WriteFile(path, mutant, 0o644))
		r.mutated = digestFile(path)
		buildOut, buildErr := run(root, []string{"go", "build", "./..."})
		var out string
		var runErr error
		if buildErr == nil {
			out, runErr = run(root, testCmd(m))
		}
		must(os.WriteFile(path, base, 0o644))
		r.restored = digestFile(path)
		fmt.Printf("hashes %s %s before=%s mutated=%s restored=%s\n", m.id, m.file, r.before, r.mutated, r.restored)
		if r.restored != r.before {
			fmt.Fprintf(os.Stderr, "%s restore hash mismatch\n", m.id)
			os.Exit(2)
		}
		switch {
		case buildErr != nil:
			r.result, r.message = "INVALID", "mutant does not build"
			fmt.Fprintf(os.Stderr, "%s build output:\n%s\n", m.id, buildOut)
		case runErr == nil:
			r.result, r.message = "SURVIVED", "intended test passed on the mutant"
		case !strings.Contains(out, "--- FAIL: "+m.test+" "):
			r.result, r.message = "INVALID", "no assertion failure in the intended test"
			fmt.Fprintf(os.Stderr, "%s output:\n%s\n", m.id, out)
		default:
			killed++
			r.result, r.test, r.message = "KILLED", m.test, firstMessage(out, root)
		}
		fmt.Printf("| %s | %s | %s | %s |\n", m.id, r.result, r.test, r.message)
		rows = append(rows, r)
	}

	for _, f := range files {
		if digestFile(filepath.Join(root, f)) != digest(original[f]) {
			fmt.Fprintf(os.Stderr, "final hash mismatch: %s\n", f)
			os.Exit(2)
		}
	}
	finished := time.Now()
	fmt.Printf("finished %s\n", finished.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("summary: %d killed, %d total; final hashes match baseline\n", killed, len(mutations))
	exit := 0
	if killed != len(mutations) {
		exit = 1
	}
	must(os.WriteFile(filepath.Join(root, auditDoc),
		[]byte(renderDoc(root, started, finished, exit, rows, files, original)), 0o644))
	fmt.Printf("wrote %s\n", auditDoc)
	os.Exit(exit)
}

func apply(base []byte, m mutation) ([]byte, string) {
	edits := append([][2]string{{m.old, m.replacement}}, m.also...)
	out := base
	code := false
	for i, e := range edits {
		if n := bytes.Count(out, []byte(e[0])); n != 1 {
			return nil, fmt.Sprintf("anchor %d count %d", i, n)
		}
		code = code || changesCode(e[0], e[1])
		out = bytes.Replace(out, []byte(e[0]), []byte(e[1]), 1)
	}
	if !code {
		return nil, "changes only comments/blank lines"
	}
	if digest(out) == digest(base) {
		return nil, "hash unchanged"
	}
	return out, ""
}

func renderDoc(root string, started, finished time.Time, exit int, rows []row, files []string, original map[string][]byte) string {
	const stamp = "2006-01-02 15:04:05 MST"
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	branch, _ := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()

	w("# EP-9 mutation audit\n\n")
	w("Generated by `go run ./scripts/ep9_mutation`. Do not edit this file by hand; re-run the runner.\n\n")
	w("Scope: EP-9 (the EntryPlan outcome-study contract and engine — `internal/entryplanbacktest`,\n")
	w("`.../recon`, `.../study`). EP-9 is a RESEARCH VALIDATION GATE: it changes no production\n")
	w("semantics and tunes no parameter, so no mutant here targets a strategy threshold.\n")
	w("This file records one audit run. It is not a review verdict.\n\n")
	w("## Run\n\n")
	w("- Started %s, finished %s, exit status %d.\n", started.Format(stamp), finished.Format(stamp), exit)
	w("- Worktree: uncommitted tree on branch `%s`.\n", strings.TrimSpace(string(branch)))
	w("- Command: `go run ./scripts/ep9_mutation` (child `go` commands use `GOCACHE=$TMPDIR/ep9-mutation-gocache`).\n\n")

	w("## What the runner checks (scripts/ep9_mutation/main.go)\n\n")
	w("Before the first mutant it records the SHA-256 of every watched file and runs every distinct test\n")
	w("command once on the unmutated tree, aborting if any fails. For each mutant (one or more exact\n")
	w("replacements in one file) it:\n\n")
	w("1. requires every anchor to occur exactly once;\n")
	w("2. requires at least one changed line to be code, not blank and not a `//` comment;\n")
	w("3. requires the file's SHA-256 to change;\n")
	w("4. requires `go build ./...` to pass on the mutant (otherwise the row is `INVALID`);\n")
	w("5. runs `go test <pkg> -run ^<Test>$ -count=1`; the row is `KILLED` only when the output contains\n")
	w("   `--- FAIL: <Test> `, i.e. an assertion failure in the intended test;\n")
	w("6. writes the original bytes back and re-checks the SHA-256 against the baseline (a mismatch aborts).\n\n")
	w("After the last mutant it re-checks every watched file against its baseline.\n\n")

	w("## Results\n\n")
	w("| Mutant | File | Mutation | Result | Red test | First assertion message |\n")
	w("| --- | --- | --- | --- | --- | --- |\n")
	killed, survived, invalid := 0, 0, 0
	for _, r := range rows {
		switch r.result {
		case "KILLED":
			killed++
		case "SURVIVED":
			survived++
		default:
			invalid++
		}
		test := ""
		if r.test != "" {
			test = "`" + r.test + "`"
		}
		w("| %s | %s | %s | %s | %s | `%s` |\n", r.m.id, strings.TrimPrefix(r.m.file, "internal/"),
			cell(r.m.description), r.result, test, strings.ReplaceAll(r.message, "`", "'"))
	}
	w("\nSummary: **%d killed, %d survived, %d invalid** of %d.\n\n", killed, survived, invalid, len(rows))

	w("### Per-mutant hashes\n\n```text\n")
	for _, r := range rows {
		w("%-5s before=%s mutated=%s restored=%s  %s\n", r.m.id, r.before, r.mutated, r.restored, r.m.file)
	}
	w("```\n\n### Notes\n\n")
	for _, n := range notes {
		w("- %s\n", n)
	}

	w("\n## Hashes\n\nSHA-256 of every watched file, recorded before the first mutant and re-verified after the last:\n\n```text\n")
	for _, f := range files {
		w("%s  %s\n", digest(original[f]), f)
	}
	w("```\n\nThese hashes describe this run only. Any later edit to these files makes the table stale.\n\n")

	w("## Earlier audits\n\n")
	w("The runner parsed the hash block of each earlier audit document and compared each entry with the\n")
	w("file's current SHA-256. A `MISMATCH` line means that document is stale and its runner must be re-run.\n")
	for _, doc := range priorAudits {
		w("\n`%s`:\n\n```text\n", doc)
		for _, line := range hashCheck(root, doc) {
			w("%s\n", line)
		}
		w("```\n")
	}
	return b.String()
}

var hashLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

func hashCheck(root, docPath string) []string {
	doc, err := os.ReadFile(filepath.Join(root, docPath))
	if err != nil {
		return []string{"UNREADABLE " + err.Error()}
	}
	var out []string
	for _, l := range strings.Split(string(doc), "\n") {
		m := hashLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, m[2]))
		switch {
		case err != nil:
			out = append(out, "MISSING  "+m[2])
		case digest(b) == m[1]:
			out = append(out, "OK       "+m[2])
		default:
			out = append(out, "MISMATCH "+m[2])
		}
	}
	if len(out) == 0 {
		return []string{"NO HASH LINES FOUND"}
	}
	return out
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func testCmd(m mutation) []string {
	return []string{"go", "test", m.pkg, "-run", "^" + m.test + "$", "-count=1"}
}

func repoRoot() (string, error) {
	b, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	return strings.TrimSpace(string(b)), err
}

func run(root string, argv []string) (string, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = root
	c.Env = append(os.Environ(), "GOCACHE="+filepath.Join(os.TempDir(), "ep9-mutation-gocache"))
	b, err := c.CombinedOutput()
	return string(b), err
}

func restoreAll(root string, original map[string][]byte) {
	for f, content := range original {
		_ = os.WriteFile(filepath.Join(root, f), content, 0o644)
	}
}

func digestFile(path string) string {
	b, err := os.ReadFile(path)
	must(err)
	return digest(b)
}

func changesCode(old, replacement string) bool {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[strings.TrimSpace(l)]++
		}
		return m
	}
	a, b := count(old), count(replacement)
	for _, pair := range [][2]map[string]int{{a, b}, {b, a}} {
		for l, n := range pair[0] {
			if n != pair[1][l] && l != "" && !strings.HasPrefix(l, "//") {
				return true
			}
		}
	}
	return false
}

func firstMessage(out, root string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "_test.go:") {
			line = strings.ReplaceAll(line, root, "...")
			if len(line) > 150 {
				line = line[:150] + "…"
			}
			return line
		}
	}
	return "(no assertion message captured)"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
