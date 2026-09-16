package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// Stratum is the adjustment stratum an observation belongs to.
//
// THE USER'S RULING, TRANSCRIBED: three strata, each with its own metrics, NOTHING DELETED,
// counts reconciling. That is why this is a segmentation and not a filter — dropping ~29% of
// the population (Phase 1 measured 7.83% with an event in the window and 21.50% with no
// adjusted series at all) would change what the study is a study of.
type Stratum string

const (
	// StratumClean — fetcher.WindowIsAdjustmentClean answered true for BOTH the plan window
	// and the outcome window. The strongest thing the repo's detector can say.
	StratumClean Stratum = "CLEAN"

	// StratumEventInWindow — an adjusted series exists and a corporate-action step falls
	// inside one of the two windows. The raw Close is discontinuous there, so a "pullback to
	// MA20", a stop touch, an MFE or a return may be a mechanical gap with no economic event
	// behind it (internal/fetcher/adjustment.go: median |gap| 3.18%, p90 6.13%).
	StratumEventInWindow Stratum = "EVENT_IN_WINDOW"

	// StratumNoAdjustedSeries — Close / AdjClose was 1.0 on every bar, so the detector CANNOT
	// TELL whether the stock never paid a dividend or whether we simply have no adjusted
	// series (adjustment.go's AdjustmentNoAdjustedSeries: 430 of 1,987 cached symbols, 21.6%).
	//
	// IT IS NOT "CLEAN". Reading it as clean would assert "no ex-dividend gap here" for every
	// symbol we merely cannot see one for, which is the exact misreading that status exists
	// to prevent.
	StratumNoAdjustedSeries Stratum = "NO_ADJUSTED_SERIES"

	// StratumUnknown — too few usable bars to form a single ratio change, or the window could
	// not be formed. Its own stratum so it is never quietly folded into any of the three above.
	StratumUnknown Stratum = "UNKNOWN"
)

// AllStrata is every stratum, in report order.
var AllStrata = []Stratum{StratumClean, StratumEventInWindow, StratumNoAdjustedSeries, StratumUnknown}

// Valid reports whether s is a defined stratum.
func (s Stratum) Valid() bool {
	for _, k := range AllStrata {
		if s == k {
			return true
		}
	}
	return false
}

// PlanWindowBars is how many trailing bars the PLAN window covers.
//
// 61, and the number is read off production rather than chosen: analyzeConsolidation builds
// PivotHigh over candles[n-1-min(60,n-1) : n-1] in both branches (consolidation.go:85 and
// :100-101), which is 61 bars, and that is the widest FINITE-window read behind any entryplan
// level. MA20 reads 20 and is covered by it.
//
// There is deliberately no term for ATR: this repo's ATR is Wilder-smoothed and reads the
// whole series, so no bar count describes it, and adjustment.go's WindowIsAdjustmentClean doc
// explicitly warns against inventing one. The consequence is stated rather than hidden: a
// CLEAN stratum vouches for the finite-window levels, not for the ATR-derived zone WIDTH.
const PlanWindowBars = 61

// ClassifyStratum classifies one observation by the adjustment state of both windows.
//
// It calls fetcher.ScanAdjustments and fetcher.WindowIsAdjustmentClean — THE PRODUCTION
// FUNCTIONS, not a replication. Phase 1 used a Python replication to size the problem; a
// number in the study has to come from the code that owns the contract.
//
// Both windows are checked because they fail differently: a step inside the PLAN window
// corrupts the levels the plan was drawn from, and a step inside the OUTCOME window
// manufactures a fill, a stop touch, an excursion or a return. Either one is enough to make
// the observation not clean.
func ClassifyStratum(bars []fetcher.Candle, signalIdx int) Stratum {
	if signalIdx < 0 || signalIdx >= len(bars) {
		return StratumUnknown
	}
	planEnd := signalIdx + 1
	outEnd := signalIdx + 1 + entryplanbacktest.ExcursionSessions
	if outEnd > len(bars) {
		outEnd = len(bars)
	}

	// The status is read from the OUTCOME prefix, which contains the plan prefix, so one scan
	// answers "is there an adjusted series at all" for both.
	switch fetcher.ScanAdjustments(bars[:outEnd]).Status {
	case fetcher.AdjustmentNoAdjustedSeries:
		return StratumNoAdjustedSeries
	case fetcher.AdjustmentInsufficientBars:
		return StratumUnknown
	}

	planClean := fetcher.WindowIsAdjustmentClean(bars[:planEnd], PlanWindowBars)
	// The outcome window is [T .. T+20] inclusive, i.e. the trailing (outEnd - signalIdx)
	// bars of the outcome prefix. Passing the count rather than a constant is what makes a
	// TRUNCATED window honest instead of silently short.
	outClean := fetcher.WindowIsAdjustmentClean(bars[:outEnd], outEnd-signalIdx)
	if planClean && outClean {
		return StratumClean
	}
	return StratumEventInWindow
}
