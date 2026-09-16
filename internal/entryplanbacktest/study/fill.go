package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ── The four arms ─────────────────────────────────────────────────────────────────────────

// Arm is one execution assumption. Four, declared before any of them was measured.
type Arm string

const (
	// ArmSignalCloseBaseline — a COMPARISON BENCHMARK, and NEVER an EntryPlan fill.
	//
	// It buys at the signal session's own close. That is not executable and this package
	// never claims it is: a daily close is known only after the session ends, and the plan is
	// computed FROM it. It exists so that "the entry plan's fill did better/worse than simply
	// owning the stock from the moment the signal existed" has a number, which is the only
	// way to tell a real execution edge from a stock-selection edge (EP-9 §23).
	//
	// ITS OUTCOME BASE SESSION IS T, NOT T+1, and that asymmetry is deliberate and must be
	// stated wherever it is compared: Return@5 for this arm is Close[T+5], for arms B and C it
	// is Close[F+5] where F >= T+1. The arms answer different questions and the matched
	// comparison in §23 is what controls for it.
	ArmSignalCloseBaseline Arm = "A_SIGNAL_CLOSE_BASELINE"

	// ArmZoneLimit — a resting limit order at the plan's published IdealEntry.High, live from
	// T+1 for the wait window. This is the arm that tests the ENTRY ZONE, which is the thing
	// EntryPlan computes.
	ArmZoneLimit Arm = "B_ZONE_LIMIT"

	// ArmChase — a resting limit order at the plan's published MaxChasePrice, live from T+1
	// for the wait window. Strictly weaker than B: MaxChasePrice >= IdealEntry.High is a
	// production invariant, so anything B fills C fills too, at the same price or worse.
	//
	// NOT APPLICABLE when the plan publishes no MaxChasePrice — the SIDEWAYS policy refuses
	// to chase (internal/entryplan/policy.go:479-482). That is NOT_APPLICABLE, never MISSED:
	// a rule that forbade the order is not an order that failed to fill.
	ArmChase Arm = "C_CHASE"

	// ArmMissed — not an execution at all. It is the COMPLEMENT of B: the plans whose zone
	// never traded inside the wait window.
	//
	// IT FABRICATES NO FILL. There is no "assume you bought at the close of T+W" fallback,
	// because a missed trade has no entry price and inventing one would put a return in a row
	// whose whole content is that there was no trade. Only MissedRate is computed from it.
	ArmMissed Arm = "D_MISSED"
)

// AllArms is every arm, in report order.
var AllArms = []Arm{ArmSignalCloseBaseline, ArmZoneLimit, ArmChase, ArmMissed}

// Valid reports whether a is a defined arm.
func (a Arm) Valid() bool {
	for _, k := range AllArms {
		if a == k {
			return true
		}
	}
	return false
}

// Executable reports whether the arm is an order a trader could actually have placed.
// ArmSignalCloseBaseline is NOT, by construction; every output that carries it must say so.
func (a Arm) Executable() bool { return a == ArmZoneLimit || a == ArmChase }

// WaitWindows are the three wait windows, DECLARED IN ADVANCE and all three mandatory.
//
// None was chosen after seeing a result, and the point of running all three is that a single
// window chosen afterwards is a fitted parameter wearing a methodology's clothes. Any other
// window is exploratory, must be labelled as such, and must never replace these (EP-9 §32).
var WaitWindows = []int{3, 5, 10}

// ── The fill model ────────────────────────────────────────────────────────────────────────

// FillOutcome is what happened to one arm's order for one plan.
type FillOutcome string

const (
	// FillFilled — the order filled.
	FillFilled FillOutcome = "FILLED"
	// FillMissed — the order was live for the whole window and never filled.
	FillMissed FillOutcome = "MISSED"
	// FillNotApplicable — no order could be placed because the plan published no price for
	// this arm (no zone for B; no chase ceiling for C). Distinct from MISSED: one is a market
	// outcome, the other is the absence of an instruction. Merging them would inflate the
	// miss rate with plans that were never trying to fill.
	FillNotApplicable FillOutcome = "NOT_APPLICABLE"
	// FillUnavailable — the session identity or the bars needed could not be reconstructed.
	FillUnavailable FillOutcome = "UNAVAILABLE"
)

// Fill is one arm's execution result.
type Fill struct {
	Outcome FillOutcome `json:"outcome"`
	// BarIndex is the bar the fill happened on. -1 when there was no fill.
	BarIndex int `json:"bar_index"`
	// Date is that bar's session.
	Date string `json:"date,omitempty"`
	// Price is the fill price. 0 when there was no fill; readers must check Outcome.
	Price float64 `json:"price,omitempty"`
	// SessionsWaited is how many sessions after T the fill took (1 = T+1). 0 when no fill.
	SessionsWaited int `json:"sessions_waited,omitempty"`
}

// Filled reports whether this arm produced an executable position.
func (f Fill) Filled() bool { return f.Outcome == FillFilled && f.Price > 0 }

// SimulateLimit fills a resting BUY LIMIT order at `limit`, live from T+1 through T+wait.
//
// THE RULES, ALL FOUR OF THEM, DECLARED HERE AND PINNED BY TEST:
//
//  1. NO FILL ON T. The window starts at T+1, whatever T's own Low did. entryplanbacktest's
//     ExecutionWindow is what enforces it; this function never computes a bound itself.
//  2. FIRST ELIGIBLE SESSION WINS. Sessions are scanned in order and the loop RETURNS on the
//     first fill. It does not look for a better price later in the window — that would be a
//     choice made with knowledge of bars the order could not have seen.
//  3. A GAP DOWN FILLS AT THE OPEN, NOT AT THE LIMIT. If Open <= limit the resting order
//     executes at the open, which is BETTER than the limit, and that is what really happens.
//     Filling at `limit` in that case would understate the position's advantage; filling at
//     `limit` when Open > limit and Low <= limit is the honest price, because the order sits
//     there and the market comes to it.
//  4. Low <= limit IS THE TOUCH, inclusive. A print at the limit fills a resting order.
//
// A bar with a non-positive Low or High, or High < Low, is UNUSABLE: the scan stops and
// returns UNAVAILABLE rather than treating a missing quote as "did not reach".
func SimulateLimit(bars []fetcher.Candle, sessions entryplanbacktest.SymbolSessions,
	signalDate string, limit float64, wait int) Fill {

	if !(limit > 0) {
		return Fill{Outcome: FillNotApplicable, BarIndex: -1}
	}
	lo, hi, el := entryplanbacktest.ExecutionWindow(sessions, signalDate, wait)
	if el != entryplanbacktest.EligibleForExecution {
		return Fill{Outcome: FillUnavailable, BarIndex: -1}
	}
	sigIdx := lo - 1
	for i := lo; i <= hi && i < len(bars); i++ {
		b := bars[i]
		if !(b.Low > 0) || !(b.High > 0) || b.High < b.Low {
			return Fill{Outcome: FillUnavailable, BarIndex: -1}
		}
		if b.Low > limit {
			continue
		}
		price := limit
		if b.Open > 0 && b.Open <= limit {
			price = b.Open
		}
		return Fill{Outcome: FillFilled, BarIndex: i, Date: sessionDate(bars, i),
			Price: price, SessionsWaited: i - sigIdx}
	}
	return Fill{Outcome: FillMissed, BarIndex: -1}
}

// SimulateSignalClose is arm A: own the stock from the signal session's close.
//
// It is not a limit order and has no wait window; it is a benchmark. It returns a fill on the
// SIGNAL BAR, which every other arm forbids, and that is exactly why Arm.Executable() answers
// false for it and why no output may present it as an EntryPlan fill.
func SimulateSignalClose(bars []fetcher.Candle, sessions entryplanbacktest.SymbolSessions,
	signalDate string) Fill {

	if sessions == nil {
		return Fill{Outcome: FillUnavailable, BarIndex: -1}
	}
	i, ok := sessions.IndexOf(signalDate)
	if !ok || i < 0 || i >= len(bars) {
		return Fill{Outcome: FillUnavailable, BarIndex: -1}
	}
	// It still needs a session AFTER T, or there is no forward outcome to measure and the
	// benchmark would silently have a different eligibility rule from the arms it is compared
	// with.
	if _, el := entryplanbacktest.EarliestExecutableIndex(sessions, signalDate); el != entryplanbacktest.EligibleForExecution {
		return Fill{Outcome: FillUnavailable, BarIndex: -1}
	}
	c := bars[i].Close
	if !(c > 0) {
		return Fill{Outcome: FillUnavailable, BarIndex: -1}
	}
	return Fill{Outcome: FillFilled, BarIndex: i, Date: sessionDate(bars, i), Price: c, SessionsWaited: 0}
}

func sessionDate(bars []fetcher.Candle, i int) string {
	if i < 0 || i >= len(bars) {
		return ""
	}
	return bars[i].Date.Format(entryplanbacktest.DateLayout)
}

// FillVsSignalCloseBpsConvention is the SIGN CONVENTION, as a sentence, meant to be printed
// beside the number rather than left in a comment.
//
// It exists because this is the one metric EP-9 §28 Q4 turns on and the sign is genuinely
// ambiguous to a reader: "the fill was 361 bps from the close" says nothing about which side.
// A reader who guesses wrong reads the study's main execution result backwards.
const FillVsSignalCloseBpsConvention = "FillVsSignalClose is (fill / signal close - 1) x 10000. " +
	"NEGATIVE means the fill was BELOW the signal close, i.e. CHEAPER and BETTER FOR A BUYER: " +
	"-361 bps is a fill 3.61% below the close. POSITIVE means the fill was ABOVE the close, " +
	"i.e. dearer and worse for a buyer. It measures PRICE, not return."

// FillVsSignalCloseBps is the fill price against the signal close, in basis points.
//
// See FillVsSignalCloseBpsConvention for the sign, which every printer must emit beside the
// value: NEGATIVE = filled below the signal close = better for a buyer.
func FillVsSignalCloseBps(fillPrice, signalClose float64) (float64, bool) {
	if !(fillPrice > 0) || !(signalClose > 0) {
		return 0, false
	}
	return (fillPrice/signalClose - 1) * 10000, true
}
