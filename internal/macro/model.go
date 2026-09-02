// Package macro is the deterministic layer for United States monetary, rate, inflation,
// labour and volatility data — the conditions Taiwanese equities trade inside but do not
// themselves produce.
//
// # Sources, all official and unauthenticated
//
//	EFFR + target range   markets.newyorkfed.org/api/rates/unsecured/effr
//	Treasury 2Y / 10Y     home.treasury.gov daily par yield curve CSV
//	CPI / core / labour   api.bls.gov/publicAPI/v2 (four series, one request)
//	VIX                   cdn.cboe.com VIX_History.csv
//
// Two things this package deliberately does NOT have, because their sources were checked and
// found unusable rather than merely unexplored:
//
//	PCE             BEA returns an empty body without an API key this repo does not hold
//	FOMC calendar   published only as HTML; a scraper for one calendar is not worth its
//	                fragility, and a wrong meeting date is worse than no meeting date
//
// # Frequency is part of the data
//
// EFFR, Treasury and VIX move daily; CPI and the labour series are monthly and arrive weeks
// after the period they describe. A snapshot taken on 2026-09-01 therefore holds a Treasury
// reading from 08-31 and a CPI reading for July — both current, in the only sense that
// matters. Every observation carries its OWN date for exactly this reason; a single
// snapshot-level date would quietly assert that the CPI figure was a September one.
//
// # Facts only
//
// Nothing here scores, ranks or recommends. It reports what the rate was, what the curve did,
// which way the last policy move went. Whether any of that is good for a given stock is a
// question this layer has no basis to answer, and inventing a weighting for it would be
// inventing a claim.
package macro

import "time"

// SchemaVersion is the snapshot format.
const SchemaVersion = 1

// Status is the availability vocabulary, shared with the fundamental and valuation layers so
// a consumer learns one contract rather than three.
type Status string

const (
	Available   Status = "AVAILABLE"
	Partial     Status = "PARTIAL"
	Unavailable Status = "UNAVAILABLE"
	// InsufficientData means the metric is computable but the history needed for it is not
	// present — a year-on-year figure with only one observation, say.
	InsufficientData Status = "INSUFFICIENT_DATA"
	// NotImplemented marks a metric whose source was investigated and found unusable.
	// Reported rather than omitted, so a reader is never left wondering.
	NotImplemented Status = "NOT_IMPLEMENTED"
)

// Observation is one dated reading. Value is nil when there is nothing to report; a zero is
// a reading, and inferring absence from it would make a genuine 0.0% inflation print
// indistinguishable from a broken feed.
type Observation struct {
	Date  string   `json:"date"`
	Value *float64 `json:"value,omitempty"`
}

// FedSnapshot is the policy rate.
//
// EFFR and the target range are kept apart on purpose. EFFR is where money actually traded;
// the range is what the Committee decided. They move for different reasons — EFFR drifts a
// basis point or two on funding pressure without any policy change at all — and collapsing
// them into one number would make that drift look like a rate decision.
type FedSnapshot struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	Date        string   `json:"date,omitempty"`
	EFFR        *float64 `json:"effr,omitempty"`
	TargetLower *float64 `json:"target_lower,omitempty"`
	TargetUpper *float64 `json:"target_upper,omitempty"`

	// History is the recent target-range readings, oldest first. It exists so the direction
	// of the last policy move can be derived without a second request.
	History []FedObservation `json:"history,omitempty"`
}

// FedObservation is one day's policy reading.
type FedObservation struct {
	Date        string   `json:"date"`
	EFFR        *float64 `json:"effr,omitempty"`
	TargetLower *float64 `json:"target_lower,omitempty"`
	TargetUpper *float64 `json:"target_upper,omitempty"`
}

// TreasurySnapshot is the yield curve's two most-watched points.
type TreasurySnapshot struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	Date     string   `json:"date,omitempty"`
	Yield2Y  *float64 `json:"yield_2y,omitempty"`
	Yield10Y *float64 `json:"yield_10y,omitempty"`
	// Spread2Y10Y is 10Y − 2Y, in PERCENTAGE POINTS. Positive is an upward-sloping curve.
	// Derived here rather than taken from anywhere, because it is a subtraction and a source
	// that disagreed with it would only raise the question of which to believe.
	Spread2Y10Y *float64 `json:"spread_2y10y,omitempty"`
}

// InflationSnapshot holds the price indices and what can be derived from them.
//
// The INDEX and the RATE are separate fields, and must stay separate. CPI is 333.918; that
// is an index level, not 333.918% inflation, and the two have been confused often enough
// that the type system should make it impossible here.
type InflationSnapshot struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	// CPIIndex and CoreCPIIndex are the raw reported index levels.
	CPIIndex     Observation `json:"cpi_index"`
	CoreCPIIndex Observation `json:"core_cpi_index"`

	// The rates below are RATIOS: 0.027 means 2.7%. One convention, repo-wide.
	CPIYoY     *float64 `json:"cpi_yoy,omitempty"`
	CoreCPIYoY *float64 `json:"core_cpi_yoy,omitempty"`
	CPIMoM     *float64 `json:"cpi_mom,omitempty"`
	CoreCPIMoM *float64 `json:"core_cpi_mom,omitempty"`

	// PriorCPIYoY is the previous month's year-on-year rate, kept so a trend can be judged
	// without re-reading the history.
	PriorCoreCPIYoY *float64 `json:"prior_core_cpi_yoy,omitempty"`
}

// LaborSnapshot holds employment.
//
// PayrollLevel is the total, in thousands of persons — 158,858 means 158.9 million jobs.
// PayrollChange is the month-on-month difference, and it is the figure markets mean by
// "nonfarm payrolls +73K". Publishing the level under that name would overstate it by three
// orders of magnitude.
type LaborSnapshot struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	UnemploymentRate  Observation `json:"unemployment_rate"`
	PriorUnemployment *float64    `json:"prior_unemployment_rate,omitempty"`

	PayrollLevel  Observation `json:"payroll_level"`
	PayrollChange *float64    `json:"payroll_change,omitempty"`
}

// VolatilitySnapshot is the VIX close.
type VolatilitySnapshot struct {
	Status Status      `json:"status"`
	Reason string      `json:"reason,omitempty"`
	VIX    Observation `json:"vix"`
}

// Snapshot is one point-in-time macro view.
//
// ArchiveDate is when this was captured; every component carries its own observation date.
// The distinction is the whole reason this type is shaped the way it is — see the package
// doc on frequency.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	ArchiveDate   string `json:"archive_date"`
	// ObservedAt is the capture instant. ArchiveDate is its date, kept as a string because
	// that is what the directory is named and what every comparison uses.
	ObservedAt time.Time `json:"observed_at"`

	Status Status `json:"status"`

	Fed        FedSnapshot        `json:"fed"`
	Treasury   TreasurySnapshot   `json:"treasury"`
	Inflation  InflationSnapshot  `json:"inflation"`
	Labor      LaborSnapshot      `json:"labor"`
	Volatility VolatilitySnapshot `json:"volatility"`

	// These were investigated and found unusable. Recorded so the absence is a statement.
	PCEStatus          Status `json:"pce_status"`
	FOMCCalendarStatus Status `json:"fomc_calendar_status"`
}
