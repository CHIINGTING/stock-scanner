package healthcheck

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/fundamental"
)

// The fundamental block: what the company has ALREADY REPORTED.
//
// Nothing here is derived. internal/fundamental already computed every growth rate and every
// margin from the source's own raw fields; this file turns those into Values and formats a
// period label. A second derivation would be a second answer.
//
// # The one thing this block exists to keep straight
//
// The MOPS income statement is CUMULATIVE. Its "Q2" row is Jan–Jun. So:
//
//	CumulativeEPS   reported, and labelled 累計 everywhere it appears
//	QuarterEPS      AVAILABLE for Q1 ONLY, where it is an identity rather than a derivation:
//	                the Q1 cumulative window IS January–March. For Q2–Q4 it is
//	                INSUFFICIENT_DATA — recovering it means subtracting the prior filing, and
//	                this build reads one archive per check, not a series.
//	TTM EPS         INSUFFICIENT_DATA — four consecutive quarters are required and one
//	                snapshot holds one. It is never annualised from a half year.
//	Forward EPS     NOT_IMPLEMENTED — this repo has no authorized Taiwanese estimate source,
//	                and a model's guess at consensus is not one.
//
// The two words are not interchangeable, and the difference is the whole point of carrying
// both: INSUFFICIENT_DATA says the capability exists and the evidence has not accrued yet —
// wait, and it will fill in. NOT_IMPLEMENTED says there is no authorized source at all —
// waiting will never help. TTM is the first; forward EPS is the second.
//
// A reader who takes a cumulative half-year EPS for a quarterly one halves every conclusion
// they draw, so the label travels with the number rather than sitting in a legend.
//
// The Q1 case is worth stating plainly because it is the difference between a field that is
// permanently blank and a field that is sometimes a real, exact figure: no arithmetic is
// performed, no second period is consulted, and no assumption is made. Q1 cumulative is the
// quarter, by definition of the window.

// EPSBlock keeps the four kinds of EPS visibly apart.
type EPSBlock struct {
	Status Status `json:"status"`
	// Period is the window Cumulative covers, e.g. "2026 H1（累計）".
	Period string `json:"period,omitempty"`

	Quarter    Value `json:"quarter"`
	Cumulative Value `json:"cumulative"`
	TTM        Value `json:"ttm"`
	Forward    Value `json:"forward"`

	// Note explains the cumulative convention in one line, for a UI that shows it inline.
	Note string `json:"note,omitempty"`
}

// FundamentalBlock is the reported financial picture.
type FundamentalBlock struct {
	Status     Status `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Source     string `json:"source,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`

	RevenueStatus      Status `json:"revenue_status"`
	RevenuePeriod      string `json:"revenue_period,omitempty"`
	RevenuePublishedAt string `json:"revenue_published_at,omitempty"`
	Revenue            Value  `json:"revenue"`
	RevenueYoY         Value  `json:"revenue_yoy"`
	RevenueMoM         Value  `json:"revenue_mom"`
	RevenueYTDYoY      Value  `json:"revenue_ytd_yoy"`

	FinancialStatus      Status `json:"financial_status"`
	FinancialPeriod      string `json:"financial_period,omitempty"`
	FinancialPublishedAt string `json:"financial_published_at,omitempty"`
	Revenue4Period       Value  `json:"period_revenue"`
	GrossMargin          Value  `json:"gross_margin"`
	OperatingMargin      Value  `json:"operating_margin"`
	NetMargin            Value  `json:"net_margin"`

	EPS EPSBlock `json:"eps"`
}

const fundSource = "TWSE / TPEx MOPS open data"

func fundamentalBlock(ev *Evidence) FundamentalBlock {
	reason := ev.FundamentalReason
	if reason == "" {
		reason = "no fundamental archive for this stock on or before " + ev.AsOf
	}
	none := Missing(ev.FundamentalStatus, reason)
	if ev.FundamentalStatus == StatusAvailable {
		// Only the block as a whole was available; individual metrics fill themselves in
		// below and any that cannot will overwrite this.
		none = Missing(StatusUnavailable, reason)
	}

	b := FundamentalBlock{
		Status:          ev.FundamentalStatus,
		Reason:          ev.FundamentalReason,
		RevenueStatus:   StatusUnavailable,
		FinancialStatus: StatusUnavailable,
		Revenue:         none,
		RevenueYoY:      none,
		RevenueMoM:      none,
		RevenueYTDYoY:   none,
		Revenue4Period:  none,
		GrossMargin:     none,
		OperatingMargin: none,
		NetMargin:       none,
		EPS:             epsBlock(nil, none),
	}

	v := ev.Fundamental
	if v == nil {
		return b
	}
	b.Source = v.Source
	if !v.ObservedAt.IsZero() {
		b.ObservedAt = v.ObservedAt.Format(dateLayout)
	}
	b.RevenueStatus = mapAvailability(string(v.RevenueStatus))
	b.FinancialStatus = mapAvailability(string(v.FinancialStatus))

	if v.Revenue != nil {
		r := v.Revenue
		b.RevenuePeriod = periodLabel(r.Period)
		if !r.PublishedAt.IsZero() {
			b.RevenuePublishedAt = r.PublishedAt.Format(dateLayout)
		}
		b.Revenue = Num(float64(r.Revenue), "TWD", 0, fundSource).WithAsOf(b.RevenuePeriod)
		b.RevenueYoY = ratioMetric(v.RevenueMetrics.YoY)
		b.RevenueMoM = ratioMetric(v.RevenueMetrics.MoM)
		b.RevenueYTDYoY = ratioMetric(v.RevenueMetrics.YTDYoY)
	}

	if v.Financials != nil {
		f := v.Financials
		b.FinancialPeriod = periodLabel(f.Period)
		if !f.PublishedAt.IsZero() {
			b.FinancialPublishedAt = f.PublishedAt.Format(dateLayout)
		}
		b.Revenue4Period = Num(float64(f.Revenue), "TWD", 0, fundSource).WithAsOf(b.FinancialPeriod)
		b.GrossMargin = ratioMetric(v.FinancialMetrics.GrossMargin)
		b.OperatingMargin = ratioMetric(v.FinancialMetrics.OperatingMargin)
		b.NetMargin = ratioMetric(v.FinancialMetrics.NetMargin)
	}
	b.EPS = epsBlock(v, none)
	return b
}

// epsBlock builds the four-way EPS view.
//
// Three of the four are permanently or currently unavailable, and each says WHICH — a reader
// who sees one number labelled 累計 and three explanations knows exactly what they have.
func epsBlock(v *fundamental.View, fallback Value) EPSBlock {
	b := EPSBlock{
		Status: StatusUnavailable,
		Quarter: Missing(StatusInsufficientData,
			"MOPS 綜合損益表只公布累計數。單季 EPS 需要與前一期累計相減，本次檢查只讀取單一封存"),
		Cumulative: fallback,
		TTM: Missing(StatusInsufficientData,
			"TTM 需要連續四季，單一封存只有一期；用半年報年化就是猜測，不做"),
		Forward: Missing(StatusNotImplemented,
			"本 repo 沒有取得授權的台股分析師預估來源（Yahoo quoteSummary 需繞過 crumb，不採用）；"+
				"AI 也不得代為產生預估值"),
		Note: "累計 EPS 是會計年度至今的合計，不是單季。把它當單季看會讓結論差一倍。",
	}
	if v == nil || v.Financials == nil {
		return b
	}
	f := v.Financials
	b.Period = periodLabel(f.Period)
	eps := f.CumulativeEPS
	if eps == nil {
		b.Cumulative = Missing(StatusUnavailable, "這期財報沒有公布每股盈餘")
		return b
	}
	b.Cumulative = Num(*eps, "TWD", 2, fundSource).WithAsOf(b.Period)
	// Never AVAILABLE on the strength of the cumulative alone: three of the four kinds of
	// EPS on this block are still missing, and a block that called itself AVAILABLE would
	// invite exactly the substitution this file exists to prevent.
	b.Status = StatusPartial

	// Q1 is the one period where the cumulative window IS a single quarter. This is the
	// definition of the window, not a derivation from it — nothing is subtracted, no second
	// period is read, and there is no assumption to be wrong about.
	if f.Period.Quarter == 1 {
		b.Quarter = Num(*eps, "TWD", 2, fundSource).
			WithAsOf(b.Period).
			WithSource(fundSource + "（Q1 累計期間即單季，非推算）")
	}
	return b
}

// ratioMetric converts a fundamental.Metric — which carries a FRACTION, per the repo-wide
// convention — into a percentage Value.
//
// The ×100 happens here, once, at the presentation boundary. NOT_MEANINGFUL (a growth rate
// off a negative base) keeps its own explanation rather than becoming a plain UNAVAILABLE:
// "the comparison base was negative" and "we have no data" are different facts.
func ratioMetric(m fundamental.Metric) Value {
	if m.Status != fundamental.Available {
		return Missing(mapAvailability(string(m.Status)), m.Reason)
	}
	return Num(m.Value*100, "%", 2, fundSource)
}

// periodLabel renders a reporting window, carrying the cumulative marker INTO the label.
//
// "2026 H1（累計）" rather than "2026Q2": a reader who takes the second for a single quarter
// halves every conclusion, and a legend elsewhere on the page will not save them.
func periodLabel(p fundamental.Period) string {
	switch {
	case p.Month > 0:
		return fmt.Sprintf("%d-%02d", p.Year, p.Month)
	case p.Quarter > 0 && p.Cumulative:
		switch p.Quarter {
		case 1:
			return fmt.Sprintf("%d Q1（累計）", p.Year)
		case 2:
			return fmt.Sprintf("%d H1（累計 Q1–Q2）", p.Year)
		case 3:
			return fmt.Sprintf("%d Q1–Q3（累計）", p.Year)
		case 4:
			return fmt.Sprintf("%d 全年（累計）", p.Year)
		}
		return fmt.Sprintf("%d Q%d（累計）", p.Year, p.Quarter)
	case p.Quarter > 0:
		return fmt.Sprintf("%d Q%d", p.Year, p.Quarter)
	case p.Year > 0:
		return fmt.Sprintf("%d", p.Year)
	}
	return ""
}
