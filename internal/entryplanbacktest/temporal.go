package entryplanbacktest

import (
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/r6backtest"
)

// DateLayout is the repo's session-date format, the same one entryplan.Snapshot.AsOf,
// r6backtest's date keys and the market-replay filenames use.
const DateLayout = "2006-01-02"

// ── The session axis ──────────────────────────────────────────────────────────────────────

// SessionAxis is the market-wide trading-session axis: the ascending, unique set of dates on
// which bars actually exist.
//
// Its field is unexported for the same reason entryplan.RegimeSeries's are: "ascending,
// unique and well-formed" must be a property of every value of the type rather than of the
// ones somebody remembered to check, and a struct literal is how that gets bypassed.
//
// IT IS A MARKET AXIS, NOT A SYMBOL'S AXIS. A symbol suspended for a week has no bar on days
// the axis contains, so the axis answers "did the market trade" and SymbolSessions answers
// "did THIS stock trade". Phase 2 needs both and they are deliberately separate types: using
// the market axis to index a suspended stock's bars would silently shift every forward
// session by the length of the suspension.
type SessionAxis struct {
	dates []string
	idx   map[string]int
}

// NewSessionAxis validates a run of session dates and returns the axis, or an error naming
// the first date that broke an invariant.
//
// Refused: an empty axis, a date that is not YYYY-MM-DD, a duplicate, and any date not
// strictly after its predecessor. Sorting the caller's slice silently would hide the one
// thing worth knowing — that the caller's idea of the order and the axis's disagree.
func NewSessionAxis(dates []string) (SessionAxis, error) {
	if len(dates) == 0 {
		return SessionAxis{}, fmt.Errorf("entryplanbacktest: session axis is empty — an axis " +
			"with no sessions cannot say what T+1 is, and must not be treated as one that can")
	}
	out := SessionAxis{dates: make([]string, len(dates)), idx: make(map[string]int, len(dates))}
	copy(out.dates, dates)
	for i, d := range out.dates {
		if _, err := time.Parse(DateLayout, d); err != nil {
			return SessionAxis{}, fmt.Errorf("entryplanbacktest: session axis entry %d is %q, "+
				"not YYYY-MM-DD", i, d)
		}
		if i > 0 && d <= out.dates[i-1] {
			return SessionAxis{}, fmt.Errorf("entryplanbacktest: session axis is not strictly "+
				"ascending at %d: %q follows %q", i, d, out.dates[i-1])
		}
		out.idx[d] = i
	}
	return out, nil
}

// AxisFromUniverse builds the axis from an already-loaded r6backtest universe.
//
// Universe.Axis is the sorted unique set of dates on which SOME cached symbol printed a bar
// (internal/r6backtest/engine.go:70-87). That is the repo's existing trading-session
// mechanism and this function is the whole of the reuse: no holiday list, no weekday
// arithmetic and no second calendar is introduced anywhere in this package.
//
// It copies and re-validates rather than trusting the field, because Axis is exported and a
// caller could have appended to it.
func AxisFromUniverse(u *r6backtest.Universe) (SessionAxis, error) {
	if u == nil {
		return SessionAxis{}, fmt.Errorf("entryplanbacktest: nil universe carries no session axis")
	}
	dates := make([]string, len(u.Axis))
	copy(dates, u.Axis)
	sort.Strings(dates)
	return NewSessionAxis(dates)
}

// Len is the number of sessions on the axis.
func (a SessionAxis) Len() int { return len(a.dates) }

// At returns the date at an index, or ("", false) out of range.
func (a SessionAxis) At(i int) (string, bool) {
	if i < 0 || i >= len(a.dates) {
		return "", false
	}
	return a.dates[i], true
}

// Index returns the axis position of a date. NOT FOUND IS NOT "the nearest one": a date the
// axis has never seen returns false, and the caller classifies the observation UNAVAILABLE.
//
// internal/validator's PriceSeries.signalIndex (internal/validator/price.go:104-115) does
// snap forward to "the first trading day on/after" a missing date. That is the right rule
// for grading an externally supplied signal list whose dates were typed by a human. It is the
// WRONG rule here: an EP-9 signal date is the AsOf of a plan this study itself computed from a
// bar, so a date with no bar means the reconstruction is broken, and snapping would hide it.
func (a SessionAxis) Index(date string) (int, bool) {
	i, ok := a.idx[date]
	return i, ok
}

// Next returns the next trading session strictly after date.
//
// It is the axis's successor and NOT date+1 day: a Friday's successor is the following
// Monday, and a session before a holiday has the session after the holiday as its successor.
// Neither fact is computed here — both are read off the bars that exist.
func (a SessionAxis) Next(date string) (string, bool) {
	i, ok := a.idx[date]
	if !ok || i+1 >= len(a.dates) {
		return "", false
	}
	return a.dates[i+1], true
}

// ── Per-symbol session identity ───────────────────────────────────────────────────────────

// SymbolSessions is one symbol's own session identity: which bar index a date is, and how
// many bars there are.
//
// It is an interface so that the contract can be tested against a fake while production uses
// the real cached series through StockSessions. *r6backtest.Stock cannot be built outside its
// own package (its idxOf map is unexported and LoadUniverse is the only constructor), which
// is exactly why the adapter exists rather than a direct dependency on the concrete type.
type SymbolSessions interface {
	// IndexOf returns the bar index for a YYYY-MM-DD date key, or (-1, false).
	IndexOf(dateKey string) (int, bool)
	// BarCount is the number of bars in the series.
	BarCount() int
}

// StockSessions adapts *r6backtest.Stock to SymbolSessions.
//
// It adds no behaviour: IndexOf is r6backtest's own date→index map (types.go:54) and
// BarCount is len(Candles). The adapter is the reuse, not a reimplementation of it.
type StockSessions struct{ S *r6backtest.Stock }

// IndexOf delegates to the loaded stock's date→bar-index map.
func (a StockSessions) IndexOf(dateKey string) (int, bool) {
	if a.S == nil {
		return -1, false
	}
	return a.S.IndexOf(dateKey)
}

// BarCount is the number of cached bars for this symbol.
func (a StockSessions) BarCount() int {
	if a.S == nil {
		return 0
	}
	return len(a.S.Candles)
}

// ── Eligibility ───────────────────────────────────────────────────────────────────────────

// Eligibility is why an observation can or cannot be executed, and it keeps the three
// answers apart that would otherwise all arrive as "no trade".
type Eligibility string

const (
	// EligibleForExecution — the signal session was located and at least one session
	// follows it, so a fill window exists.
	EligibleForExecution Eligibility = "ELIGIBLE"

	// SessionUnavailable — this symbol has no bar on the signal date, so its session
	// IDENTITY cannot be reconstructed. NEVER guessed, never snapped to a neighbour: a plan
	// whose AsOf has no bar is a broken reconstruction, not a trade that did not happen.
	SessionUnavailable Eligibility = "UNAVAILABLE"

	// NoFollowingSession — the signal session IS the newest bar we hold. The trade has not
	// had its chance yet; this is the PENDING of internal/validator's evaluator, and it must
	// not be counted as a loss, a win or an exclusion for cause.
	NoFollowingSession Eligibility = "NO_FOLLOWING_SESSION"
)

// Valid reports whether e is one of the three defined answers.
func (e Eligibility) Valid() bool {
	switch e {
	case EligibleForExecution, SessionUnavailable, NoFollowingSession:
		return true
	}
	return false
}

// ── The rule: no same-bar fill ────────────────────────────────────────────────────────────

// EarliestExecutableIndex returns the bar index of the earliest session a plan signalled on
// signalDate may be filled in.
//
// It is ALWAYS signalIdx+1 when it exists. There is no branch, no option and no parameter
// through which a caller could ask for the signal bar: the contract's fourth line is enforced
// by this function having nothing else to return.
func EarliestExecutableIndex(s SymbolSessions, signalDate string) (int, Eligibility) {
	if s == nil {
		return -1, SessionUnavailable
	}
	i, ok := s.IndexOf(signalDate)
	if !ok || i < 0 {
		return -1, SessionUnavailable
	}
	if i+1 >= s.BarCount() {
		return -1, NoFollowingSession
	}
	return i + 1, EligibleForExecution
}

// MaxWaitSessions is the largest wait window this contract accepts.
//
// It is a BOUND on the observation identity, not a tuned parameter: a wait window is part of
// what makes two observations different (see ObservationID), so it has to be a declared,
// finite, checkable quantity. Twenty sessions is the outcome horizon (ExcursionSessions), and
// a wait longer than the horizon would let a plan fill after the window it is graded over has
// already closed.
const MaxWaitSessions = ExcursionSessions

// ExecutionWindow returns the inclusive bar-index range [lo, hi] in which a plan signalled on
// signalDate may be filled, given a wait window of waitSessions sessions.
//
// waitSessions counts SESSIONS AFTER T, so waitSessions=1 is "T+1 only" and waitSessions=5 is
// "T+1 through T+5". waitSessions < 1 is refused rather than clamped: zero would mean "fill on
// T", which is the thing this contract forbids, and a clamp would answer a forbidden question
// with a legal-looking number.
//
// hi is TRUNCATED to the last bar held, and the truncation is NOT reported here — a window
// that runs past the data is still a legal window to search, it simply has fewer sessions in
// it. Whether the OUTCOME can be graded over a truncated window is ExcursionWindow's and
// HorizonCloseIndex's question, and they answer it separately on purpose: a fill found on the
// last bar we hold is a real fill with an ungradeable outcome, not a fill that did not happen.
func ExecutionWindow(s SymbolSessions, signalDate string, waitSessions int) (lo, hi int, el Eligibility) {
	if waitSessions < 1 {
		return -1, -1, SessionUnavailable
	}
	first, el := EarliestExecutableIndex(s, signalDate)
	if el != EligibleForExecution {
		return -1, -1, el
	}
	last := first + waitSessions - 1
	if max := s.BarCount() - 1; last > max {
		last = max
	}
	return first, last, EligibleForExecution
}
