package entryplanbacktest

import "github.com/deep-huang/stock-scanner/internal/fetcher"

// ── The outcome-horizon convention, fixed before any result exists ────────────────────────

// ExcursionSessions is the outcome window: TWENTY sessions after the fill session.
//
// Fixed here, once, before Phase 2 produces a number. See HorizonCloseIndex on why the
// counting rule is a contract rather than a preference.
const ExcursionSessions = 20

// HorizonCloseIndex returns the bar index whose CLOSE is Return@h for a fill on fillIdx, and
// whether the series holds it.
//
// THE CONVENTION, STATED ONCE AND NOT NEGOTIABLE AFTER RESULTS EXIST:
//
//	Return@h is measured from the FILL SESSION F, and h counts SESSIONS STRICTLY AFTER F.
//	Return@h reads Close at bar F+h. The fill session is NOT one of the h sessions.
//	Return@5 is therefore the close of the FIFTH session after the fill session.
//
// The alternative — counting the fill session as session 1, so Return@5 reads F+4 — is not
// wrong in the abstract; it is wrong HERE, because the repo already answered this question
// once and a study that answered it differently could not be compared with what exists.
// internal/validator/price.go:145-147 computes `ti := entryIdx + h` where entryIdx is the
// next-session entry bar, i.e. exactly the rule above. This function is that rule, named.
//
// WHY IT IS PINNED RATHER THAN DOCUMENTED: shifting the horizon by one session is a free
// parameter worth a visible amount of mean return on any momentum-flavoured signal, and it is
// the easiest thing in a study to change after seeing the table and describe as a correction.
// TestTheHorizonConventionCountsSessionsAfterTheFill is what makes that change a failing test
// instead of a paragraph edit. IT MUST NOT BE CHANGED AFTER PERFORMANCE HAS BEEN SEEN.
//
// h < 1 is refused. h == 0 would be the fill session's own close, which is a fill-quality
// statistic and not a forward return, and letting it through this function would put the two
// in the same column.
func HorizonCloseIndex(fillIdx, h, barCount int) (int, bool) {
	if fillIdx < 0 || h < 1 || barCount <= 0 {
		return -1, false
	}
	i := fillIdx + h
	if i >= barCount {
		return -1, false
	}
	return i, true
}

// WindowCoverage says how much of the outcome window the series actually holds.
//
// The three answers are kept apart because two of them are NOT exclusions. A truncated
// window is a trade that has not finished, and folding it into "excluded" would delete the
// most recent — and only out-of-sample — observations from the study.
type WindowCoverage string

const (
	// WindowComplete — every session of the window is held.
	WindowComplete WindowCoverage = "COMPLETE"
	// WindowTruncated — the fill is real and the series ends inside the window. PENDING, in
	// internal/validator's vocabulary: not a win, not a loss, not an exclusion.
	WindowTruncated WindowCoverage = "TRUNCATED"
	// WindowUnavailable — there is no window at all: no fill bar, or no session after it.
	WindowUnavailable WindowCoverage = "UNAVAILABLE"
)

// Valid reports whether c is one of the three defined answers.
func (c WindowCoverage) Valid() bool {
	switch c {
	case WindowComplete, WindowTruncated, WindowUnavailable:
		return true
	}
	return false
}

// ExcursionWindow returns the inclusive bar-index range the 20-session outcome window covers
// for a fill on fillIdx, together with how much of it the series holds.
//
// THE WINDOW IS [F+1, F+20]. THE FILL SESSION IS EXCLUDED, and that exclusion is the single
// most consequential line in this file:
//
//	MFE and MAE are measured RELATIVE TO THE FILL PRICE, and the fill happens INTRADAY on
//	session F. Daily OHLC does not record WHEN inside F the fill occurred, so F's own High
//	and Low cannot be attributed to before or after it. Including F would import that
//	unknown into every MAE — a plan filled near F's high would be credited with an adverse
//	excursion down to F's low that the position may never have been exposed to.
//
// internal/validator/price.go:156-165 takes the same window (`for i := entryIdx + 1; i <=
// last`), so this is the repo's existing reading rather than a new one.
//
// THE ARITHMETIC PHASE 2 MUST USE, fixed here and computed nowhere in this package:
//
//	MFE = max over i in [F+1, F+min(20, available)] of (High[i]/FillPrice - 1) * 100
//	MAE = min over i in [F+1, F+min(20, available)] of (Low[i] /FillPrice - 1) * 100
//
// NEITHER IS CLIPPED AT ZERO, and that is a deliberate divergence from
// internal/validator/price.go:163-170, whose `dd, ru := 0.0, 0.0` seeds floor MaxRunup at 0
// and cap MaxDrawdown at 0. Under that seeding a position that gapped down on F+1 and never
// traded above the fill again reports MFE = 0.00%, which reads as "it got back to breakeven"
// — a favourable excursion that never happened. A max over a non-empty window needs no seed,
// so this contract takes the plain extremum and lets MFE be negative when it was.
//
// High/Low are used "where available": a bar whose High or Low is not a positive quote
// carries no excursion and Phase 2 must skip it and count it, never read it as 0.
func ExcursionWindow(fillIdx, barCount int) (lo, hi int, cov WindowCoverage) {
	if fillIdx < 0 || barCount <= 0 || fillIdx+1 >= barCount {
		return -1, -1, WindowUnavailable
	}
	lo = fillIdx + 1
	hi = fillIdx + ExcursionSessions
	if hi >= barCount {
		return lo, barCount - 1, WindowTruncated
	}
	return lo, hi, WindowComplete
}

// ── Same-bar ambiguity ────────────────────────────────────────────────────────────────────

// TouchClass is what ONE daily bar can be shown to have done to a long plan's stop and
// target. It exists so that "both were touched" has a name instead of a coin flip.
type TouchClass string

const (
	// TouchNone — the bar's range reached neither level.
	TouchNone TouchClass = "NO_TOUCH"

	// TouchTargetOnly — High reached the target and Low never reached the stop.
	TouchTargetOnly TouchClass = "TARGET_ONLY"

	// TouchStopOnly — Low reached the stop and High never reached the target.
	TouchStopOnly TouchClass = "STOP_ONLY"

	// TouchAmbiguous — BOTH levels sit inside the bar's range, and daily OHLC cannot order
	// them. The bar prints one Open, one High, one Low and one Close, in no recoverable
	// sequence.
	//
	// IT IS AN OUTCOME CLASS, NOT AN ERROR TO BE RESOLVED. The two conventional resolutions
	// are the two ways to fabricate a result: "stop first" manufactures a pessimism that
	// makes every stop profile look equally bad, and "target first" manufactures an edge
	// with no execution behind it. R6's stop study ran into the same bar and the honest
	// answer is the same one — count these, report them as their own line, and never let
	// either resolution enter a headline number silently. Phase 2 may report a
	// stop-first SENSITIVITY arm, clearly labelled as such, but the baseline carries this
	// class through to the output.
	TouchAmbiguous TouchClass = "AMBIGUOUS_STOP_TARGET_ORDER"

	// TouchUnavailable — the bar has no usable High/Low, or the levels handed in are not
	// prices. Distinct from NO_TOUCH: one says the bar did not reach them, the other says we
	// cannot tell. MISSING != ZERO.
	TouchUnavailable TouchClass = "TOUCH_UNAVAILABLE"
)

// Valid reports whether t is one of the five defined classes.
func (t TouchClass) Valid() bool {
	switch t {
	case TouchNone, TouchTargetOnly, TouchStopOnly, TouchAmbiguous, TouchUnavailable:
		return true
	}
	return false
}

// ClassifyTouch classifies one daily bar against a LONG plan's stop and target.
//
// Long-only, because every EntryPlan status this study can reach is a buy: the stop is BELOW
// the entry and the target ABOVE it, so
//
//	stop touched   iff  bar.Low  <= stop
//	target touched iff  bar.High >= target
//
// Bounds are INCLUSIVE, matching the only sense in which a resting order at a price is filled
// when the market prints it.
//
// It returns TouchUnavailable rather than guessing when the bar is unusable (High or Low not
// positive, or High < Low), when either level is not a positive price, or when stop >= target
// — the last is a malformed plan, not a market event, and classifying it would hide a bug in
// whatever built it.
func ClassifyTouch(bar fetcher.Candle, stop, target float64) TouchClass {
	if !(bar.High > 0) || !(bar.Low > 0) || bar.High < bar.Low {
		return TouchUnavailable
	}
	if !(stop > 0) || !(target > 0) || stop >= target {
		return TouchUnavailable
	}
	hitStop := bar.Low <= stop
	hitTarget := bar.High >= target
	switch {
	case hitStop && hitTarget:
		return TouchAmbiguous
	case hitStop:
		return TouchStopOnly
	case hitTarget:
		return TouchTargetOnly
	}
	return TouchNone
}
