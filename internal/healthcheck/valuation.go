package healthcheck

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The valuation block: the exchange's own published price ratios, plus what this stock's own
// archived history says about where today's ratio sits.
//
// Nothing is derived here either. internal/valuation computed the distribution; this file
// formats it. The one thing it adds is the SUMMARY string, which exists so a UI cannot show
// a median of one observation as though it were a median.

// HistoricalPEBlock is the P/E distribution.
//
// SampleCount and RequiredSamples sit beside every statistic on purpose: a median over four
// observations and a median over four hundred are different claims, and a reader who cannot
// see which they have will treat them the same.
type HistoricalPEBlock struct {
	Status          Status `json:"status"`
	SampleCount     int    `json:"sample_count"`
	RequiredSamples int    `json:"required_samples"`
	WindowDays      int    `json:"window_days"`

	Median Value `json:"median"`
	P25    Value `json:"p25"`
	P75    Value `json:"p75"`
	Min    Value `json:"min"`
	Max    Value `json:"max"`
	// CurrentPercentile is where today's P/E sits in the distribution, 0–100.
	CurrentPercentile Value `json:"current_percentile"`

	// Summary is the one string a UI may print when the distribution is unusable, e.g.
	// "INSUFFICIENT_DATA（1 筆，需 20）". It exists so "0x" and "0 percentile" have nowhere
	// to come from.
	Summary string `json:"summary"`
}

// ValuationBlock is the price-relative picture.
type ValuationBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source,omitempty"`

	TrailingPE   Value  `json:"trailing_pe"`
	TrailingDate string `json:"trailing_date,omitempty"`

	PBRatio       Value `json:"pb_ratio"`
	DividendYield Value `json:"dividend_yield"`

	Historical HistoricalPEBlock `json:"historical_pe"`

	// These three have no authorized source. They are carried as explicit NOT_IMPLEMENTED
	// values rather than omitted, so a reader learns there is nothing to wait for instead of
	// assuming the computation failed.
	ForwardPE Value `json:"forward_pe"`
	PEG       Value `json:"peg"`
	FairValue Value `json:"fair_value"`
}

func valuationBlock(ev *Evidence) ValuationBlock {
	reason := ev.ValuationReason
	if reason == "" {
		reason = "no valuation archive for this stock on or before " + ev.AsOf
	}
	none := Missing(StatusUnavailable, reason)
	noSource := func(what string) Value {
		return Missing(StatusNotImplemented, what+"：本 repo 沒有取得授權的台股盈餘預估來源")
	}

	b := ValuationBlock{
		Status:        ev.ValuationStatus,
		Reason:        ev.ValuationReason,
		TrailingPE:    none,
		PBRatio:       none,
		DividendYield: none,
		ForwardPE:     noSource("預估本益比"),
		PEG:           noSource("PEG"),
		FairValue:     noSource("情境合理價"),
		Historical:    emptyHistorical(reason),
	}

	v := ev.Valuation
	if v == nil {
		return b
	}
	b.Source = v.Source
	b.TrailingDate = v.TrailingDate
	// Projected from the domain rather than hard-coded, so the day an authorized estimate
	// source does appear these follow it instead of staying frozen at NOT_IMPLEMENTED.
	b.ForwardPE = statusValue(v.ForwardPEStatus, b.ForwardPE)
	b.PEG = statusValue(v.PEGStatus, b.PEG)
	b.FairValue = statusValue(v.FairValueStatus, b.FairValue)

	if v.TrailingPE != nil {
		b.TrailingPE = Num(*v.TrailingPE, "x", 2, v.Source).WithAsOf(v.TrailingDate)
	} else {
		// The exchange omits the P/E for a company with non-positive earnings. That absence
		// is a FACT about the company; turning it into 0 would put a loss-making stock at the
		// cheap end of every percentile.
		r := v.Reason
		if r == "" {
			r = "交易所未公告本益比（虧損公司不公告）"
		}
		b.TrailingPE = Missing(mapAvailability(string(v.TrailingStatus)), r)
	}
	if v.PBRatio != nil {
		b.PBRatio = Num(*v.PBRatio, "x", 2, v.Source).WithAsOf(v.TrailingDate)
	}
	if v.DividendYield != nil {
		b.DividendYield = Num(*v.DividendYield, "%", 2, v.Source).WithAsOf(v.TrailingDate)
	}
	b.Historical = historicalBlock(v.HistoricalPE, v.Source)
	return b
}

// statusValue re-labels a placeholder with the domain's own status, keeping the explanation
// already written for it. There is no number in either case — only the reason a reader is
// looking at a blank.
func statusValue(a valuation.Availability, placeholder Value) Value {
	st := mapAvailability(string(a))
	if st == StatusAvailable {
		// The domain claims a value this projection has no field to carry. Reporting it as
		// available with nothing behind it would be the worst of both.
		return Missing(StatusNotImplemented, "來源已可提供，但本區塊尚未接上")
	}
	return Missing(st, placeholder.Reason)
}

func emptyHistorical(reason string) HistoricalPEBlock {
	none := Missing(StatusInsufficientData, reason)
	return HistoricalPEBlock{
		Status:          StatusInsufficientData,
		RequiredSamples: valuation.MinHistoricalPESamples,
		Median:          none, P25: none, P75: none, Min: none, Max: none,
		CurrentPercentile: none,
		Summary: fmt.Sprintf("%s（0 筆，需 %d）", StatusInsufficientData,
			valuation.MinHistoricalPESamples),
	}
}

func historicalBlock(h valuation.HistoricalPEStats, source string) HistoricalPEBlock {
	b := HistoricalPEBlock{
		Status:          mapAvailability(string(h.Status)),
		SampleCount:     h.SampleCount,
		RequiredSamples: valuation.MinHistoricalPESamples,
		WindowDays:      h.WindowDays,
	}
	if h.Status != valuation.Available {
		reason := fmt.Sprintf("樣本 %d 筆，需 %d 筆", h.SampleCount, valuation.MinHistoricalPESamples)
		none := Missing(b.Status, reason)
		b.Median, b.P25, b.P75, b.Min, b.Max = none, none, none, none, none
		b.CurrentPercentile = none
		b.Summary = fmt.Sprintf("%s（%d 筆，需 %d）", b.Status, h.SampleCount,
			valuation.MinHistoricalPESamples)
		return b
	}
	b.Median = peValue(h.Median, source)
	b.P25 = peValue(h.P25, source)
	b.P75 = peValue(h.P75, source)
	b.Min = peValue(h.Min, source)
	b.Max = peValue(h.Max, source)
	// The domain carries the percentile as a RATIO in [0,1]; the ×100 happens once, here.
	if h.CurrentPercentile != nil {
		b.CurrentPercentile = Num(*h.CurrentPercentile*100, "%", 0, source)
	} else {
		b.CurrentPercentile = Missing(StatusUnavailable, "沒有當期本益比可以定位")
	}
	b.Summary = fmt.Sprintf("%d 筆樣本，中位數 %s", h.SampleCount, b.Median.Display)
	return b
}

func peValue(p *float64, source string) Value {
	if p == nil {
		return Missing(StatusUnavailable, "統計值不可得")
	}
	return Num(*p, "x", 2, source)
}
