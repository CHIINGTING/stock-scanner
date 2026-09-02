// Package valuation holds the price-relative measures a stock can be judged by, and — just
// as importantly — records which of them this repo cannot yet compute and why.
//
// # What exists, and what does not
//
// A full valuation layer wants forward earnings: forward P/E, PEG, scenario fair value,
// upside. Every one of those needs an EARNINGS FORECAST, and R13-M6's source survey found no
// authorized structured feed of Taiwanese analyst estimates:
//
//	Yahoo quoteSummary   401 Invalid Crumb — behind an access control, and going around it
//	                     is not an option
//	TWSE / TPEx / MOPS   actual reported figures only; the 35 fundamental endpoints are all
//	                     statements a company has already filed
//
// So this package does NOT define ForwardEPS, PEG or FairValue fields. A struct field with no
// source behind it is how internal/pricingpower became 890 lines that nothing imports, and
// repeating that would be worse than the gap it papers over.
//
// # What it does instead
//
// Both exchanges publish each stock's trailing P/E daily, as an official figure:
//
//	上市  openapi.twse.com.tw/v1/exchangeReport/BWIBBU_ALL
//	上櫃  www.tpex.org.tw/openapi/v1/tpex_mainboard_peratio_analysis
//
// Using the exchange's own number is better than deriving one here, for a reason beyond
// convenience: R13-M5 established that the financial statements report CUMULATIVE earnings,
// so this repo has no TTM EPS, and the only way to manufacture one would be to annualise a
// half-year figure — which is exactly the guess a valuation layer must not make.
//
// The published figure is also inherently point-in-time: the exchange computed it on that
// date from the earnings it considered current then. A history of those numbers is a history
// of what was actually knowable, with no reconstruction to get wrong.
//
// # The same constraint as M5
//
// The endpoint takes no parameters and serves only the latest session — verified, including
// with a date argument, which it ignores. History therefore accrues from archived snapshots,
// and percentile statistics stay INSUFFICIENT_DATA until enough of them exist.
package valuation

import "time"

// SchemaVersion is the snapshot format.
const SchemaVersion = 1

// Availability mirrors internal/fundamental's vocabulary so a consumer reads one contract.
type Availability string

const (
	Available   Availability = "AVAILABLE"
	Partial     Availability = "PARTIAL"
	Unavailable Availability = "UNAVAILABLE"
	Disabled    Availability = "DISABLED"
	// InsufficientData means the metric is computable in principle but the archive does not
	// yet hold enough observations. Distinct from UNAVAILABLE: one is "wait", the other is
	// "there is nothing to wait for".
	InsufficientData Availability = "INSUFFICIENT_DATA"
	// NotImplemented marks a metric this repo has no source for. Reported explicitly so a
	// reader is never left wondering whether it was computed and came out empty.
	NotImplemented Availability = "NOT_IMPLEMENTED"
)

// SourceName identifies the exchange datasets.
const SourceName = "TWSE BWIBBU_ALL / TPEx peratio_analysis"

// Ratios is one stock's published price ratios for one session.
//
// These are the EXCHANGE's figures, not derived here. PERatio is trailing — computed by the
// exchange from reported earnings — and this package never annualises anything to obtain it.
type Ratios struct {
	Symbol string `json:"symbol"`
	// Date is the trading session the exchange computed these for.
	Date string `json:"date"`

	// PERatio is nil when the exchange published no P/E — which it does for a company with
	// non-positive earnings. That absence is a FACT about the company, and turning it into
	// 0 would put a loss-making stock at the cheap end of every percentile.
	PERatio       *float64 `json:"pe_ratio,omitempty"`
	PBRatio       *float64 `json:"pb_ratio,omitempty"`
	DividendYield *float64 `json:"dividend_yield,omitempty"`

	// ArchiveDate is the directory this record was read from — WHEN we observed it, as
	// opposed to Date, which is the session the exchange computed it for. Never serialised
	// (`json:"-"`): it is a property of the archive, not of the observation, and writing it
	// into the file would state the same fact in two places that could then disagree.
	//
	// It exists because the exchanges publish with a lag of several days, so the same
	// session is archived repeatedly. Deduplication needs to know which copy is the most
	// recent one the caller is allowed to see.
	ArchiveDate string `json:"-"`
}

// Snapshot is one stock's ratios at one observation.
type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Symbol        string    `json:"symbol"`
	Source        string    `json:"source"`
	ObservedAt    time.Time `json:"observed_at"`

	Status Availability `json:"status"`
	Reason string       `json:"reason,omitempty"`

	Ratios *Ratios `json:"ratios,omitempty"`
}

// HistoricalPEStats describes a P/E distribution.
//
// SampleCount is carried beside every statistic on purpose: a median over four observations
// and a median over four hundred are different claims, and a reader who cannot see which one
// they have will treat them the same.
type HistoricalPEStats struct {
	Status Availability `json:"status"`
	// WindowDays is the lookback the samples were drawn from.
	WindowDays  int `json:"window_days"`
	SampleCount int `json:"sample_count"`

	Median *float64 `json:"median,omitempty"`
	P25    *float64 `json:"p25,omitempty"`
	P75    *float64 `json:"p75,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`

	// CurrentPercentile is where the latest P/E sits in the distribution, as a RATIO in
	// [0,1] — the same convention the rest of this repo uses for a percentile. Ties count as
	// at-or-below, matching internal/indicator's PercentileRank.
	CurrentPercentile *float64 `json:"current_percentile,omitempty"`
}

// Valuation is everything this layer can say about one stock.
//
// The NotImplemented fields are deliberately present as STATUSES rather than absent: a reader
// who sees no forward P/E should learn that there is no source for one, not be left to guess
// whether it failed to compute.
type Valuation struct {
	Status Availability `json:"status"`
	Symbol string       `json:"symbol"`

	// TrailingPE is the exchange's published figure, carried through unchanged.
	TrailingPE     *float64     `json:"trailing_pe,omitempty"`
	TrailingDate   string       `json:"trailing_date,omitempty"`
	TrailingStatus Availability `json:"trailing_status"`

	PBRatio       *float64 `json:"pb_ratio,omitempty"`
	DividendYield *float64 `json:"dividend_yield,omitempty"`

	HistoricalPE HistoricalPEStats `json:"historical_pe"`

	// Everything below needs an earnings forecast, and no authorized source for Taiwanese
	// estimates was found. See the package doc.
	ForwardPEStatus Availability `json:"forward_pe_status"`
	PEGStatus       Availability `json:"peg_status"`
	FairValueStatus Availability `json:"fair_value_status"`

	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}
