package fundamental

import "math"

// Derived metrics.
//
// Every one of these is computed here, once, from raw reported figures. Nothing downstream
// recomputes them — a renderer that did its own division would be a second implementation
// that agrees until the day it does not.
//
// The rule that shapes the whole file: a metric whose inputs are absent is UNAVAILABLE, not
// zero. A company can genuinely have zero growth; it cannot genuinely have "no comparison
// period" and a growth of zero at the same time.

// Metric is one derived value plus why it does or does not exist.
type Metric struct {
	Status Availability `json:"status"`
	// Value is meaningful only when Status is AVAILABLE.
	Value float64 `json:"value,omitempty"`
	// Reason explains a non-available status.
	Reason string `json:"reason,omitempty"`
}

func available(v float64) Metric {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		// A NaN or Inf must never leave this package: it would survive JSON encoding as an
		// error, or worse, render as a plausible-looking number.
		return Metric{Status: Unavailable, Reason: "not a finite number"}
	}
	return Metric{Status: Available, Value: v}
}

func unavailable(reason string) Metric {
	return Metric{Status: Unavailable, Reason: reason}
}

// notMeaningful marks a computation that is arithmetically possible but financially
// misleading — the negative-base growth case.
func notMeaningful(reason string) Metric {
	return Metric{Status: Unavailable, Reason: "NOT_MEANINGFUL: " + reason}
}

// RevenueMetrics are the monthly-revenue derivations.
type RevenueMetrics struct {
	// YoY compares this month with the SAME MONTH last year — seasonality makes any other
	// base misleading for a Taiwanese monthly series.
	YoY Metric `json:"yoy"`
	// MoM compares this month with the previous month.
	MoM Metric `json:"mom"`
	// YTDYoY compares the year-to-date span with the same span last year.
	YTDYoY Metric `json:"ytd_yoy"`
}

// DeriveRevenue computes growth from a monthly record.
//
// The comparison bases come from the SOURCE's own raw fields, so these are available from a
// single fetch rather than requiring an archive.
func DeriveRevenue(r *MonthlyRevenue) RevenueMetrics {
	if r == nil {
		m := unavailable("no monthly revenue record")
		return RevenueMetrics{YoY: m, MoM: m, YTDYoY: m}
	}
	return RevenueMetrics{
		YoY:    growth(float64(r.Revenue), float64(r.PriorYearMonth), "prior-year month"),
		MoM:    growth(float64(r.Revenue), float64(r.PriorMonth), "prior month"),
		YTDYoY: growth(float64(r.YTD), float64(r.PriorYearYTD), "prior-year YTD"),
	}
}

// growth is (current − base) / base, refusing the cases where that number would mislead.
//
// A base of zero has no percentage change at all. A NEGATIVE base is the subtler trap: the
// arithmetic succeeds and produces something like +200% for a swing from −1 to +1, which
// reads as spectacular growth when what actually happened is a return to profit. Reporting
// that number without qualification is worse than reporting nothing.
func growth(current, base float64, what string) Metric {
	switch {
	case base == 0:
		return unavailable("no " + what + " to compare against")
	case base < 0:
		return notMeaningful("the " + what + " was negative, so a percentage change would mislead")
	}
	return available((current - base) / base)
}

// FinancialMetrics are the income-statement derivations.
//
// Every one covers the SAME cumulative window as the statement it came from. That is what
// makes them valid despite the window not being a single quarter: numerator and denominator
// share it. A margin is a ratio within one period, so a cumulative period gives a cumulative
// margin — which is a real figure, not an approximation.
type FinancialMetrics struct {
	GrossMargin     Metric `json:"gross_margin"`
	OperatingMargin Metric `json:"operating_margin"`
	NetMargin       Metric `json:"net_margin"`
}

// DeriveFinancials computes margins from one statement.
func DeriveFinancials(f *Financials) FinancialMetrics {
	if f == nil {
		m := unavailable("no financial statement")
		return FinancialMetrics{GrossMargin: m, OperatingMargin: m, NetMargin: m}
	}
	rev := float64(f.Revenue)
	if rev <= 0 {
		// Zero or negative revenue makes every margin meaningless, not zero.
		m := unavailable("revenue is not positive, so a margin cannot be computed")
		return FinancialMetrics{GrossMargin: m, OperatingMargin: m, NetMargin: m}
	}
	return FinancialMetrics{
		GrossMargin:     available(float64(f.GrossProfit) / rev),
		OperatingMargin: available(float64(f.OperatingIncome) / rev),
		NetMargin:       available(float64(f.NetIncome) / rev),
	}
}

// ── metrics this source cannot yet support ───────────────────────────────────────────
//
// The MOPS endpoints take no parameters and return only the current period. Everything below
// needs a period that response does not contain, so it is NOT IMPLEMENTED rather than
// approximated:
//
//	standalone quarterly EPS  needs the prior quarter of the same fiscal year
//	EPS YoY                   needs the same quarter of the prior year
//	TTM EPS / TTM revenue     needs four consecutive quarters
//	margin deltas             needs a prior period's margin
//
// They become computable once the snapshot archive holds two or more observations — which is
// what SaveSnapshot exists to accumulate. Adding them before the history exists would mean
// inventing the missing period, and the only honest way to invent it is not to.
