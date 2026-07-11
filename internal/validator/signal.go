// Package validator implements the Signal Validation Report feature.
//
// It reads the daily HTML reports the scanner has already produced, extracts the
// trading judgment that was shown to the user on that day (BUY / REDUCE / WATCH …),
// then measures what the stock actually did over the following T+3 / T+5 / T+10 /
// T+20 trading days. The goal is an honest hit-rate — including wrong cases — not a
// selective proof that the scanner is right.
//
// Anti look-ahead rule: the validator only uses the judgment that already existed
// in the historical HTML report. It never re-runs today's scanner logic over past
// data. See parser.go.
package validator

import (
	"strings"
	"time"
)

// ActionGroup normalizes the two scanner action vocabularies
// (scanner.Action and scanner.WatchAction) into three coarse buckets.
type ActionGroup string

const (
	BuyGroup    ActionGroup = "BUY_GROUP"
	ReduceGroup ActionGroup = "REDUCE_GROUP"
	WatchGroup  ActionGroup = "WATCH_GROUP"
	// EntryCautionGroup is the watchlist "太熱不要追 / OVERHEATED;追高" caution. It is
	// an ENTRY-risk warning ("don't chase now, wait for a pullback"), NOT a bearish
	// exit call, so folding it into ReduceGroup (which is graded on "must fall")
	// unfairly drags the REDUCE hit rate down. It is judged on its own terms —
	// did a pullback / better entry appear — and kept out of the headline hit rate.
	EntryCautionGroup ActionGroup = "ENTRY_CAUTION_GROUP"
	// UnknownGroup is used for action strings we could not classify; such rows
	// are parsed but excluded from hit-rate statistics.
	UnknownGroup ActionGroup = "UNKNOWN_GROUP"
)

// Result is the verdict for a single validated signal.
type Result string

const (
	ResultCorrect     Result = "CORRECT"
	ResultWrong       Result = "WRONG"
	ResultNeutral     Result = "NEUTRAL"
	ResultPending     Result = "PENDING"       // not enough forward trading days yet
	ResultNoPriceData Result = "NO_PRICE_DATA" // no OHLCV series available for the code
	ResultParseFailed Result = "PARSE_FAILED"
)

// SignalSnapshot is one trading judgment lifted verbatim from a historical report.
type SignalSnapshot struct {
	SignalDate   time.Time
	Code         string
	Name         string
	Action       string // the raw action text shown in the report (e.g. "STRONG BUY", "PULLBACK_BUY")
	ActionGroup  ActionGroup
	Stage        string  // rocket / consolidation stage label, when present
	Score        float64 // rocket / bfp score, when present (0 if absent)
	SignalPrice  float64 // reference price on the signal day (EOD close from the price store)
	ReasonTags   []string
	SourceReport string // relative path of the report the signal came from
	SourceTab    string // which tab it came from: positions | market | watchlist | rotation
}

// ValidationResult couples a snapshot with the forward-return outcome.
type ValidationResult struct {
	Signal    SignalSnapshot
	EntryDate time.Time
	// EntryPrice is the price the performance calc uses: the trading day AFTER the
	// signal, open price (fallback close). The scanner is an EOD report, so a user
	// cannot realistically enter at the signal-day close.
	EntryPrice float64

	ReturnByHorizon map[int]float64 // horizon (trading days) → return %, from EntryPrice
	ExcessByHorizon map[int]float64 // horizon → excess return % vs benchmark (NaN if no benchmark)

	MaxDrawdown float64 // most negative low vs entry over the evaluated window, %
	MaxRunup    float64 // most positive high vs entry over the evaluated window, %

	Result Result
	Reason string
}

// normalizeAction upper-cases and collapses separators so that "STRONG BUY",
// "STRONG_BUY" and "strong-buy" all compare equal.
func normalizeAction(a string) string {
	a = strings.ToUpper(strings.TrimSpace(a))
	a = strings.NewReplacer("_", " ", "-", " ", "／", " ", "/", " ").Replace(a)
	a = strings.Join(strings.Fields(a), " ")
	return a
}

// buyActions / reduceActions / watchActions cover both scanner.Action and
// scanner.WatchAction, plus the Chinese synonyms the report may surface.
var (
	buyActions = set(
		// scanner.Action
		"STRONG BUY", "BUY",
		// scanner.WatchAction (entry-oriented)
		"BREAKOUT BUY", "PULLBACK BUY", "PREPARE ENTRY",
		// generic / Chinese
		"ADD", "ADD BUY", "ACCUMULATE", "HOLD STRONG", "CONTINUE HOLD",
		"加買", "買進", "續抱", "強勢續抱", "偏多",
		// Chinese labels emitted by the report (actionLabel / watchActionText)
		"強勢買進", "準備進場", "突破買進", "拉回買進",
	)
	reduceActions = set(
		// scanner.Action
		"REDUCE", "SELL", "TAKE PROFIT", "STOP LOSS",
		// scanner.WatchAction (exit-oriented)
		"REMOVE FROM WATCHLIST",
		// generic / Chinese
		"EXIT", "FORCE EXIT", "AVOID",
		"減碼", "賣出", "出場", "停損", "停利", "避開", "風險升高", "轉弱",
		// Chinese labels emitted by the report (actionLabel / watchActionText)
		"移出觀察",
	)
	watchActions = set(
		// scanner.Action
		"WATCH", "HOLD",
		// scanner.WatchAction
		"WAIT", "WATCH CLOSELY",
		// generic / Chinese
		"NEUTRAL",
		"觀察", "等待", "中性", "震盪",
		// Chinese labels emitted by the report (actionLabel / watchActionText)
		"持有", "密切觀察",
	)
)

// GroupForAction maps a raw action string to its coarse group, using only the
// action text. Context-free; callers that have the full snapshot should prefer
// GroupForSnapshot so OVERHEATED entry cautions are split out.
func GroupForAction(action string) ActionGroup {
	n := normalizeAction(action)
	switch {
	case buyActions[n]:
		return BuyGroup
	case reduceActions[n]:
		return ReduceGroup
	case watchActions[n]:
		return WatchGroup
	default:
		return UnknownGroup
	}
}

// GroupForSnapshot classifies a fully-parsed snapshot. It is GroupForAction plus
// one context-aware override: a watchlist signal whose base group is ReduceGroup
// but which is really an OVERHEATED "don't chase" caution is reclassified as
// EntryCautionGroup. This keeps ReduceGroup measuring genuine exits.
func GroupForSnapshot(s SignalSnapshot) ActionGroup {
	base := GroupForAction(s.Action)
	if base == ReduceGroup && isOverheatedCaution(s) {
		return EntryCautionGroup
	}
	return base
}

// isOverheatedCaution reports whether a signal is a watchlist OVERHEATED "don't
// chase" entry warning rather than a real position exit. It keys specifically on
// the OVERHEATED stage/tag — NOT a bare "追高" risk sub-tag, which can also ride a
// MAIN_RUN signal — so genuine TAKE PROFIT / SELL / STOP LOSS (and non-overheated
// stages) stay in ReduceGroup. Position-tab exits carry no OVERHEATED tag anyway.
func isOverheatedCaution(s SignalSnapshot) bool {
	if !strings.EqualFold(s.SourceTab, "watchlist") {
		return false
	}
	if strings.Contains(strings.ToUpper(s.Stage), "OVERHEATED") {
		return true
	}
	for _, tag := range s.ReasonTags {
		if strings.Contains(strings.ToUpper(tag), "OVERHEATED") {
			return true
		}
	}
	return false
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
