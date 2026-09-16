package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ── EP-9 §31: the MA60 counterfactual, as a LABELLED COLUMN ───────────────────────────────
//
// internal/scanner/entryplan_attach.go:293 hardcodes MA60 unavailable, so no plan in this
// study has ever had an MA60 candidate. The question §31 asks is how many plans that absence
// BLOCKED, and it can be answered without implementing MA60 anywhere near production:
//
//	compute MA60 HERE, from the same truncated series the plan saw, and ask whether it would
//	have been an eligible pullback centre for the plans whose zone step failed for exactly the
//	two reasons a missing level produces.
//
// WHAT THIS IS NOT. It is not a re-run of the plan with MA60 supplied. It does not re-derive a
// zone, a stop, a target or a status, and it changes no baseline number: ClassifyMA60Block
// returns a string that is attached to the observation and reported in its own column. A
// reader must not read MA60_WOULD_UNBLOCK as "this plan would have produced a good trade" —
// it says the level rule would have had a candidate, and nothing about what happened next.

// MA60Label is the counterfactual verdict for one plan.
type MA60Label string

const (
	// MA60NotBlocked — the plan's zone step did not fail for a level reason, so MA60's
	// absence is not what stopped it. Includes every plan that GOT a zone.
	MA60NotBlocked MA60Label = "NOT_BLOCKED"

	// MA60BlockedOtherCause — the zone step failed for a level reason and MA60 would NOT have
	// been eligible either (it is not below the current price, or it cannot be computed).
	// The block is real and MA60 is not the cause.
	MA60BlockedOtherCause MA60Label = "BLOCKED_NOT_MA60"

	// MA60WouldUnblockSolely — the zone step failed for a level reason, NO other candidate was
	// eligible, and MA60 would have been: strictly below the current price and a positive
	// price. This is the population §31 asks for.
	MA60WouldUnblockSolely MA60Label = "MA60_WOULD_UNBLOCK_SOLELY"

	// MA60Unknown — the plan carries no zone trace, or the series is too short for a 60-bar
	// mean. Not folded into NOT_BLOCKED: "we could not ask" is not "the answer is no".
	MA60Unknown MA60Label = "UNKNOWN"
)

// AllMA60Labels is every label, in report order.
var AllMA60Labels = []MA60Label{MA60NotBlocked, MA60BlockedOtherCause, MA60WouldUnblockSolely, MA60Unknown}

// blockingReasons are the EXACTLY TWO reason codes a missing pullback level produces.
//
// PULLBACK_LEVELS_UNAVAILABLE is "no candidate had a usable number at all";
// PULLBACK_LEVEL_NOT_BELOW_CURRENT_PRICE is "every candidate was present and sat at or above
// the market" (internal/entryplan/reasons.go:95 and :108). A third code,
// PULLBACK_LEVELS_PRICE_BASIS_MISMATCH, is deliberately NOT here: supplying MA60 would not fix
// a basis mismatch, so counting it would inflate the answer with plans MA60 could not help.
var blockingReasons = map[entryplan.Reason]bool{
	entryplan.ReasonPullbackLevelsUnavailable:         true,
	entryplan.ReasonPullbackLevelNotBelowCurrentPrice: true,
}

// ClassifyMA60Block labels one plan, reading only the plan and the series the plan saw.
func ClassifyMA60Block(p *entryplan.Plan, bars []fetcher.Candle, signalIdx int) MA60Label {
	if p == nil {
		return MA60Unknown
	}
	if p.IdealEntry != nil {
		return MA60NotBlocked
	}
	blocked := false
	for _, r := range p.Reasons {
		if blockingReasons[r] {
			blocked = true
			break
		}
	}
	if !blocked {
		return MA60NotBlocked
	}
	if p.EntryTrace == nil {
		return MA60Unknown
	}
	// Another candidate already eligible means the zone did not fail for want of a level, so
	// MA60 is not the blocker whatever its value.
	for _, c := range p.EntryTrace.Zone.Candidates {
		if c.Disposition.Eligible() {
			return MA60BlockedOtherCause
		}
	}
	ma60, ok := ma60At(bars, signalIdx)
	if !ok {
		return MA60Unknown
	}
	price := p.EntryTrace.Decision.CurrentPrice
	if price == nil || !(*price > 0) {
		return MA60Unknown
	}
	// The same test SelectCenter applies to every pullback candidate: strictly below the
	// market, and a positive price (internal/entryplan/levels.go:362-413 via the screen).
	if ma60 > 0 && ma60 < *price {
		return MA60WouldUnblockSolely
	}
	return MA60BlockedOtherCause
}

// ma60At is SMA(60) of the RAW Close at signalIdx.
//
// RAW Close, and indicator.SMA, because that is what the production projection would have fed
// it: internal/indicator/calculator.go fills closes[i] = c.Close unconditionally, and
// entryplan.Snapshot.PriceBasis is RAW for every field the scanner bridge projects
// (entryplan_attach.go's field 3). Using AdjClose here would compare an adjusted mean against
// a raw market price.
func ma60At(bars []fetcher.Candle, signalIdx int) (float64, bool) {
	if signalIdx < 59 || signalIdx >= len(bars) {
		return 0, false
	}
	closes := make([]float64, signalIdx+1)
	for i := 0; i <= signalIdx; i++ {
		if !(bars[i].Close > 0) {
			return 0, false
		}
		closes[i] = bars[i].Close
	}
	ma := indicator.SMA(closes, 60)
	if len(ma) <= signalIdx || !(ma[signalIdx] > 0) {
		return 0, false
	}
	return ma[signalIdx], true
}
