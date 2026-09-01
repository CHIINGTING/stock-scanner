// Package fundamental is the canonical layer for Taiwan ACTUAL financial data — figures a
// company has already reported. It holds no forecast, no consensus and no valuation; those
// are different kinds of claim with different provenance, and mixing them into one struct is
// how "estimated" quietly becomes "reported" three layers downstream.
//
// # Source
//
// TWSE and TPEx publish the MOPS datasets as parameterless public JSON:
//
//	上市  openapi.twse.com.tw/v1/opendata/t187ap05_L      月營收
//	      openapi.twse.com.tw/v1/opendata/t187ap06_L_ci   綜合損益表
//	上櫃  www.tpex.org.tw/openapi/v1/mopsfin_t187ap05_OA  月營收
//	      www.tpex.org.tw/openapi/v1/mopsfin_t187ap06_O_ci 綜合損益表
//
// Each returns the CURRENT period for every listed company in one response — so the whole
// universe costs two requests per market, not one per stock.
//
// # The constraint that shapes everything here
//
// Those endpoints take no parameters. There is no "give me 2025 Q2". A fetch today returns
// today's period and nothing else, which means:
//
//	derivable from one fetch    monthly revenue, its YoY and MoM (the source carries the
//	                            prior month and prior-year month as RAW fields), the current
//	                            reporting period's revenue/profit/EPS, and its margins
//	NOT derivable from one fetch standalone quarterly EPS, EPS YoY, TTM, margin deltas —
//	                            every one needs a period this response does not contain
//
// History therefore has to accrue: each fetch is archived, and metrics needing two periods
// become available once two periods have been observed. Anything not yet derivable is
// reported UNAVAILABLE. It is never estimated, and never filled with zero.
//
// # 季別 is CUMULATIVE, and this was verified rather than assumed
//
// The income-statement dataset's 季別=2 row is the FIRST HALF, not Q2 alone. Checked against
// the monthly dataset for two companies in two markets, exact to the last digit:
//
//	2330  Jan–Jul cumulative 2,872,064,238 − Jul 467,580,548 = 2,404,483,690
//	      季別2 營業收入                                        = 2,404,483,690
//	6488  Jan–Jul cumulative    34,176,866 − Jul   4,977,758 =    29,199,108
//	      季別2 營業收入                                        =    29,199,108
//
// Reading that row as a standalone quarter would overstate Q2 revenue and EPS by roughly
// double. The type system says so: the field is named CumulativeEPS, and there is no
// QuarterlyEPS field for a value this source cannot supply.
package fundamental

import "time"

// SchemaVersion is the snapshot format. Bumped when the archived JSON shape changes.
const SchemaVersion = 1

// Availability is the three-state answer every metric must be able to give.
//
// A value of 0 is a READING — a company can genuinely break even, and revenue can genuinely
// fall. Availability is therefore never inferred from a number.
type Availability string

const (
	Available   Availability = "AVAILABLE"
	Partial     Availability = "PARTIAL"
	Unavailable Availability = "UNAVAILABLE"
	Disabled    Availability = "DISABLED"
	// NotApplicable is for securities that have no financial statements at all — an ETF has
	// no EPS, and reporting that as UNAVAILABLE would suggest a feed problem.
	NotApplicable Availability = "NOT_APPLICABLE"
)

// Money is an amount in whole TWD.
//
// The sources report 千元; the provider multiplies once, at the boundary, so nothing
// downstream has to carry a unit field or remember which scale it is holding.
type Money float64

// Ratio is a fraction, not a percentage: 0.25 means 25%.
//
// One convention, repo-wide. Presentation multiplies by 100; nothing else does.
type Ratio float64

// Period identifies a reporting window.
//
// Cumulative is the field that stops the most expensive mistake this package can make. The
// income statement's Q2 row covers Jan–Jun, so a Period with Quarter=2 and Cumulative=true
// is six months of activity — and a consumer that ignores the flag will silently double
// every per-quarter figure it computes.
type Period struct {
	Year int `json:"year"`
	// Quarter is 1–4 for a financial statement, 0 for a monthly revenue record.
	Quarter int `json:"quarter,omitempty"`
	// Month is 1–12 for a monthly revenue record, 0 for a financial statement.
	Month int `json:"month,omitempty"`
	// Cumulative is true when the figures cover the fiscal year to date rather than the
	// period alone. Always true for this source's quarterly statements.
	Cumulative bool `json:"cumulative"`
}

// MonthlyRevenue is one company-month from the 月營收 dataset.
//
// PriorMonth and PriorYearMonth are the source's OWN raw figures, not values recovered from
// an archive. That is why YoY and MoM are computable from a single fetch: the comparison
// bases arrive with the observation.
type MonthlyRevenue struct {
	Symbol string `json:"symbol"`
	Period Period `json:"period"`

	Revenue        Money `json:"revenue"`
	PriorMonth     Money `json:"prior_month_revenue"`
	PriorYearMonth Money `json:"prior_year_month_revenue"`

	// YTD and PriorYearYTD are the cumulative figures for the same span.
	YTD          Money `json:"ytd_revenue"`
	PriorYearYTD Money `json:"prior_year_ytd_revenue"`

	// PublishedAt is the source's 出表日期 — when the figure became public. It is NOT the
	// time we happened to fetch it; conflating the two is how look-ahead enters a backtest.
	PublishedAt time.Time `json:"published_at"`
}

// Financials is one company-period from the 綜合損益表 dataset.
//
// Every monetary field covers the SAME cumulative window, which is what makes the margins
// below valid even though the window is not a single quarter: numerator and denominator
// share it.
type Financials struct {
	Symbol string `json:"symbol"`
	Period Period `json:"period"`

	Revenue         Money `json:"revenue"`
	GrossProfit     Money `json:"gross_profit"`
	OperatingIncome Money `json:"operating_income"`
	NetIncome       Money `json:"net_income"`

	// CumulativeEPS is the fiscal-year-to-date basic EPS in TWD per share. Named for what it
	// is: there is no standalone-quarter EPS here, because this source does not report one.
	CumulativeEPS *float64 `json:"cumulative_eps,omitempty"`

	PublishedAt time.Time `json:"published_at"`
}

// Snapshot is one stock's fundamental picture at one observation time.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Symbol        string `json:"symbol"`
	Source        string `json:"source"`
	// ObservedAt is when this data was fetched — deliberately separate from the
	// PublishedAt on each record.
	ObservedAt time.Time `json:"observed_at"`

	Status          Availability `json:"status"`
	RevenueStatus   Availability `json:"revenue_status"`
	FinancialStatus Availability `json:"financial_status"`
	// Reason explains a non-available status in one line.
	Reason string `json:"reason,omitempty"`

	Revenue    *MonthlyRevenue `json:"monthly_revenue,omitempty"`
	Financials *Financials     `json:"financials,omitempty"`
}
