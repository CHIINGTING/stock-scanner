package options

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Coverage: what the selection actually saw, kept and could not use.
//
// §10.8 requires a PCR to carry it, and the reason is that "AVAILABLE" over a response whose
// night rows never arrived, or whose put side is missing at half the strikes, is a fetch that
// succeeded and an aggregate that did not. Every number below is a description of the
// SELECTION, not a quality score: a WEEKLY-scoped view of a response carrying five monthlies
// has a low in-scope ratio on a perfectly complete day.

// Exclusion reasons. Closed set: an excluded row whose reason is not one of these is a rule
// nobody wrote down.
const (
	// ExcludedOtherProduct — a different product. §3.1's contract-selection rule is that all
	// aggregation is over one product's rows, and the by-strike response carries TXO, TEO,
	// TGO, IJO and others in one array.
	ExcludedOtherProduct = "OUT_OF_SCOPE_PRODUCT"
	// ExcludedOtherSession — a row from the other trading session.
	ExcludedOtherSession = "OUT_OF_SCOPE_SESSION"
	// ExcludedOtherFamily — a series of the other contract family.
	ExcludedOtherFamily = "OUT_OF_SCOPE_FAMILY"
	// ExcludedCombinationCode — a "/" code: one spread order across two expiries, excluded
	// from every per-series aggregate (§3.1).
	ExcludedCombinationCode = "COMBINATION_CODE"
	// ExcludedUnresolvedExpiry — a code that parses as neither. Counted in ALL and reported
	// in data health; it cannot be filed under WEEKLY or MONTHLY.
	ExcludedUnresolvedExpiry = "UNRESOLVED_EXPIRY"
	// ExcludedOtherTradingDate — a row from another session date. Never silently included:
	// that is how an aggregate acquires lots it did not measure.
	ExcludedOtherTradingDate = "OTHER_TRADING_DATE"
	// ExcludedSettlingExpiry — the expiry whose final settlement day is this trading date,
	// removed from OPEN INTEREST only (§3.1). Its volume was genuinely traded and stays.
	ExcludedSettlingExpiry = "SETTLING_EXPIRY"
	// ExcludedUnknownSide — a call/put value that is neither. The parser rejects these
	// outright; this exists so a row arriving from anywhere else cannot be counted as a put
	// by falling through an else.
	ExcludedUnknownSide = "UNKNOWN_SIDE"
)

// ExcludedRows is one exclusion reason and how many rows it accounted for.
type ExcludedRows struct {
	Reason string `json:"reason"`
	Rows   int    `json:"rows"`
	// Detail names the products, sessions or expiry codes involved, sorted, so an exclusion
	// is never just a smaller count.
	Detail []string `json:"detail,omitempty"`
}

// ExpiryDistance is one included series and how far its settlement is.
//
// Days is a pointer because the by-strike feed does not carry a settlement date: it is known
// only for expiries the caller supplied one for, from the settlement snapshot archived for the
// session. It is never inferred from the code's shape — TAIFEX defers a settlement to the next
// trading day when the third Wednesday is a holiday.
type ExpiryDistance struct {
	ExpiryCode     string `json:"expiry_code"`
	ExpiryKind     string `json:"expiry_kind"`
	SettlementDate string `json:"settlement_date,omitempty"`
	Days           *int   `json:"days_to_expiry,omitempty"`
}

// Coverage is the selection's own account of itself.
type Coverage struct {
	Product string         `json:"product"`
	Family  ContractFamily `json:"family"`
	Session string         `json:"session"`
	Metric  MetricType     `json:"metric"`

	// RowsSeen is every row handed to the selection, including other products'.
	RowsSeen int `json:"rows_seen"`
	// RowsInScope is what survived product, session, family and settling-expiry selection.
	RowsInScope  int            `json:"rows_in_scope"`
	RowsExcluded int            `json:"rows_excluded"`
	Excluded     []ExcludedRows `json:"excluded,omitempty"`
	// RejectedRows is how many rows the PARSER rejected for this snapshot (§6.2). Supplied
	// by the caller, because a decoded slice cannot know what never made it into it. A
	// non-zero count is what makes the aggregate unpublishable, not merely noted.
	RejectedRows int `json:"rejected_rows"`

	// ExpectedContracts are the expiry codes in scope; ActualContracts are the ones that
	// contributed a value for THIS metric. On the fixture's AFTER_HOURS open interest the
	// first has two entries and the second has none, which is the whole of §10.8's night
	// session finding, visible without reading the ratio.
	ExpectedContracts []string `json:"expected_contracts,omitempty"`
	ActualContracts   []string `json:"actual_contracts,omitempty"`
	MissingContracts  []string `json:"missing_contracts,omitempty"`

	// ExpectedPairs is the number of distinct (expiry, strike) points in scope;
	// ActualPairs is how many of them contributed BOTH a call and a put value. A PCR is a
	// ratio of two sides, and a strike present on one side only is where an aggregate
	// quietly becomes asymmetric.
	ExpectedPairs int `json:"expected_pairs"`
	ActualPairs   int `json:"actual_pairs"`
	// CallOnlyPoints and PutOnlyPoints are the unpaired remainder.
	CallOnlyPoints int `json:"call_only_points"`
	PutOnlyPoints  int `json:"put_only_points"`

	// SessionsExpected is what this request's session selects; SessionsPresent is which of
	// them actually carried an in-scope row. A COMBINED volume figure built from one session
	// is a third of the activity, and this is where that shows.
	SessionsExpected []string `json:"sessions_expected"`
	SessionsPresent  []string `json:"sessions_present,omitempty"`
	SessionsMissing  []string `json:"sessions_missing,omitempty"`

	// Classification completeness over the product's rows, before the family filter.
	ClassifiedRows         int  `json:"classified_rows"`
	UnresolvedRows         int  `json:"unresolved_rows"`
	CombinationRows        int  `json:"combination_rows"`
	ClassificationComplete bool `json:"classification_complete"`

	// ObservedValues and AbsentValues split the in-scope rows by whether THIS metric's
	// column carried a number. "-" is an observation of absence and is counted here; it is
	// not a rejected row.
	ObservedValues int `json:"observed_values"`
	AbsentValues   int `json:"absent_values"`

	CallRows         int `json:"call_rows"`
	PutRows          int `json:"put_rows"`
	CallContributing int `json:"call_contributing"`
	PutContributing  int `json:"put_contributing"`

	// ExpiryDistances are the included series and, where a settlement date was supplied,
	// how far away it is.
	ExpiryDistances []ExpiryDistance `json:"expiry_distances,omitempty"`
}

// ContractCompleteness is contributing contracts over in-scope contracts, and 0 when none were
// in scope. A description, not a score.
func (c Coverage) ContractCompleteness() float64 {
	if len(c.ExpectedContracts) == 0 {
		return 0
	}
	return float64(len(c.ActualContracts)) / float64(len(c.ExpectedContracts))
}

// PairCompleteness is paired points over in-scope points.
func (c Coverage) PairCompleteness() float64 {
	if c.ExpectedPairs == 0 {
		return 0
	}
	return float64(c.ActualPairs) / float64(c.ExpectedPairs)
}

// SessionsComplete reports whether every session this request selects carried rows.
func (c Coverage) SessionsComplete() bool { return len(c.SessionsMissing) == 0 }

// excludeTally accumulates exclusions by reason without losing what was excluded.
type excludeTally struct {
	rows   map[string]int
	detail map[string]map[string]bool
	total  int
}

func newExcludeTally() *excludeTally {
	return &excludeTally{rows: map[string]int{}, detail: map[string]map[string]bool{}}
}

func (e *excludeTally) note(reason, detail string) {
	e.rows[reason]++
	e.total++
	if detail == "" {
		return
	}
	if e.detail[reason] == nil {
		e.detail[reason] = map[string]bool{}
	}
	e.detail[reason][detail] = true
}

func (e *excludeTally) list() []ExcludedRows {
	out := make([]ExcludedRows, 0, len(e.rows))
	for reason, n := range e.rows {
		x := ExcludedRows{Reason: reason, Rows: n}
		for d := range e.detail[reason] {
			x.Detail = append(x.Detail, d)
		}
		sort.Strings(x.Detail)
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason < out[j].Reason })
	return out
}

// classify is the ONE call to the expiry classifier in this package.
//
// It reads the CODE, through M3's ClassifyExpiry, and never a settlement date, a delivery
// month or a contract name. §3.1 names the trap: SettledPositionsIndexOptions reports the same
// F1 weekly as {TXU, ContractDeliveryMonth 202609, ContractName 臺指選擇權F1}, so a classifier
// fed that column calls MONTHLY the series DailyMarketReportOpt calls WEEKLY — one contract,
// two kinds, and M6's weekly/monthly split would then depend on which feed ran last.
//
// A row that arrives already carrying an ExpiryKind is CHECKED against the code rather than
// trusted: a disagreement means a second classifier exists somewhere, which is the condition
// this function is here to make impossible rather than to survive.
func classify(r provider.OptionStrikeRow) (string, error) {
	kind := provider.ClassifyExpiry(r.ExpiryCode)
	if r.ExpiryKind != "" && r.ExpiryKind != kind {
		return "", fmt.Errorf(
			"options: %s/%s arrived classified %s while its code says %s — two classifiers "+
				"disagree, and §3.1 allows only the code-shape rule",
			r.Product, r.ExpiryCode, r.ExpiryKind, kind)
	}
	return kind, nil
}
