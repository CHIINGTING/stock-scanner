package healthcheck

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The target-price block, and the margin of safety derived from it.
//
// The arithmetic lives in internal/valuation.ComputeTargetPrice — a pure function with no
// I/O and no clock, tested there against every refusal case. This file only decides what to
// hand it and how to print what comes back.
//
// The AI never sees this code path. Target prices are computed BEFORE any model is called,
// and the judge is given them as evidence it may explain but not alter.

// TargetScenarioBlock is one case, ready to print.
type TargetScenarioBlock struct {
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Reason      string `json:"reason,omitempty"`
	PEMultiple  Value  `json:"pe_multiple"`
	TargetPrice Value  `json:"target_price"`
	UpsidePct   Value  `json:"upside_pct"`

	// ── provenance, repeated on every row ──
	//
	// A scenario row is what a person reads and quotes. A multiple whose justification lives
	// one level up travels as a bare number the moment the row leaves the page, so each row
	// answers on its own: which earnings, from where, times what multiple, by which rule,
	// over how many samples, as of when.
	EPS         Value  `json:"eps"`
	EPSBasis    string `json:"eps_basis,omitempty"`
	EPSSource   string `json:"eps_source,omitempty"`
	Rule        string `json:"rule,omitempty"`
	RuleDetail  string `json:"rule_detail,omitempty"`
	SampleCount int    `json:"sample_count"`
	AsOf        string `json:"as_of,omitempty"`
}

// TargetPriceBlock carries the scenarios plus the provenance of every assumption behind them.
type TargetPriceBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`

	// Rule and RuleDetail make the multiplier traceable. There is no rule meaning
	// "a sensible-looking number", and no path by which a model could supply one.
	Rule       string `json:"rule"`
	RuleDetail string `json:"rule_detail,omitempty"`

	SampleCount     int `json:"sample_count"`
	RequiredSamples int `json:"required_samples"`

	CurrentPrice Value  `json:"current_price"`
	EPS          Value  `json:"eps"`
	EPSBasis     string `json:"eps_basis,omitempty"`
	EPSSource    string `json:"eps_source,omitempty"`
	// EPSBasePrice and EPSBaseDate are the close and the session the EPS was divided out of,
	// so the arithmetic can be redone by hand from the page alone.
	EPSBasePrice Value  `json:"eps_base_price"`
	EPSBaseDate  string `json:"eps_base_date,omitempty"`
	// CurrentDate is the session CurrentPrice closed on — the last bar at or before AsOf,
	// which is not AsOf itself on a holiday or behind a stale cache.
	CurrentDate string `json:"current_date,omitempty"`
	AsOf        string `json:"as_of,omitempty"`

	Scenarios []TargetScenarioBlock `json:"scenarios"`

	// MarginOfSafety is the BASE case's upside — the headline number a reader acts on, lifted
	// out so it does not have to be found inside an array.
	//
	// It is (target − price) / price, NOT the textbook (fair − price) / fair. The two differ
	// numerically, and this one goes negative for a stock trading above its base case, where
	// the textbook quantity is simply undefined. MarginOfSafetyBasis says which is meant, on
	// the block, because "安全邊際 −56%" read without it is a contradiction.
	MarginOfSafety      Value  `json:"margin_of_safety"`
	MarginOfSafetyBasis string `json:"margin_of_safety_basis,omitempty"`
}

// buildTargetPrice runs the deterministic engine over this check's evidence.
//
// The one non-obvious input is EPSBasePrice. The engine recovers the exchange's earnings
// base by dividing a close by the exchange's published P/E, and the two have to belong to
// the SAME session — the exchanges publish these ratios days late, so the P/E in hand was
// computed for an earlier session than the one being analysed. That close is looked up here,
// from the same asOf-truncated series everything else on the page is built from.
func buildTargetPrice(ev *Evidence, price PriceBlock) TargetPriceBlock {
	in := valuation.TargetInput{Symbol: ev.Identity.CanonicalSymbol, AsOf: ev.AsOf}
	if p, ok := price.Close.Float(); ok {
		in.CurrentPrice = &p
	}
	// The upside's denominator is a price from a specific session too. price.Close is the
	// last bar AT OR BEFORE asOf, which on a holiday or behind a stale cache is not asOf —
	// and a block whose only date is the analysis date gives a reader no way to tell.
	currentDate := price.BarDate
	if ev.Valuation != nil {
		in.Valuation = *ev.Valuation
		in.EPSBaseDate = ev.Valuation.TrailingDate
		if c, ok := closeOn(ev.Bars, ev.Valuation.TrailingDate); ok {
			in.EPSBasePrice = &c
		}
	}
	return projectTargetPrice(valuation.ComputeTargetPrice(in), currentDate)
}

// closeOn returns the close of the session named by date.
//
// The match is EXACT. A nearest-earlier fallback would quietly reintroduce the mismatch this
// exists to prevent — pairing a P/E with a close from a different day — and the engine
// refusing is the better outcome: a target built on the wrong session is worse than no
// target, because it looks like one.
//
// bars are already truncated at asOf, so nothing here can see past the analysis date.
func closeOn(bars []fetcher.Candle, date string) (float64, bool) {
	if date == "" {
		return 0, false
	}
	for i := len(bars) - 1; i >= 0; i-- {
		d := bars[i].Date.Format(dateLayout)
		if d == date {
			if bars[i].Close > 0 {
				return bars[i].Close, true
			}
			return 0, false
		}
		if d < date {
			break // sessions are ascending; nothing earlier can match
		}
	}
	return 0, false
}

func projectTargetPrice(tp valuation.TargetPrice, currentDate string) TargetPriceBlock {
	status := mapAvailability(string(tp.Status))
	b := TargetPriceBlock{
		Status:          status,
		Reason:          tp.Reason,
		Rule:            tp.Rule,
		RuleDetail:      tp.RuleDetail,
		SampleCount:     tp.SampleCount,
		RequiredSamples: tp.RequiredSamples,
		EPSBasis:        tp.EPSBasis,
		EPSSource:       tp.EPSSource,
		EPSBaseDate:     tp.EPSBaseDate,
		AsOf:            tp.AsOf,
		EPSBasePrice:    Missing(status, reasonOr(tp.Reason, "沒有本益比對應交易日的收盤價")),
		CurrentPrice:    Missing(StatusUnavailable, "沒有現價"),
		EPS:             Missing(status, reasonOr(tp.Reason, "沒有可用的每股盈餘基準")),
		MarginOfSafety:  Missing(status, reasonOr(tp.Reason, "沒有目標價，無法計算安全邊際")),
	}
	b.CurrentDate = currentDate
	if tp.CurrentPrice != nil {
		b.CurrentPrice = Num(*tp.CurrentPrice, "TWD", 2,
			"internal/fetcher price cache（"+dateOr(currentDate, "最後一根 K 棒")+" 收盤）").
			WithAsOf(currentDate)
	}
	if tp.EPS != nil {
		b.EPS = Num(*tp.EPS, "TWD", 2, tp.EPSSource)
	}
	if tp.EPSBasePrice != nil {
		b.EPSBasePrice = Num(*tp.EPSBasePrice, "TWD", 2,
			"internal/fetcher price cache（"+tp.EPSBaseDate+" 收盤）").WithAsOf(tp.EPSBaseDate)
	}
	for _, s := range tp.Scenarios {
		sb := TargetScenarioBlock{
			Name:        s.Name,
			Status:      mapAvailability(string(s.Status)),
			Reason:      s.Reason,
			EPSBasis:    s.EPSBasis,
			EPSSource:   s.EPSSource,
			Rule:        s.Rule,
			RuleDetail:  s.RuleDetail,
			SampleCount: s.SampleCount,
			AsOf:        s.AsOf,
		}
		st := sb.Status
		sb.EPS = Missing(st, reasonOr(s.Reason, "沒有可用的每股盈餘基準"))
		if s.EPS != nil {
			sb.EPS = Num(*s.EPS, "TWD", 2, s.EPSSource)
		}
		sb.PEMultiple = Missing(st, reasonOr(s.Reason, "沒有可用的本益比倍數"))
		sb.TargetPrice = Missing(st, reasonOr(s.Reason, "沒有目標價"))
		sb.UpsidePct = Missing(st, reasonOr(s.Reason, "沒有上檔空間"))
		if s.PEMultiple != nil {
			sb.PEMultiple = Num(*s.PEMultiple, "x", 2, tp.RuleDetail)
		}
		if s.TargetPrice != nil {
			sb.TargetPrice = Num(*s.TargetPrice, "TWD", 2, targetSource(tp))
		}
		if s.UpsidePct != nil {
			sb.UpsidePct = Num(*s.UpsidePct, "%", 1, targetSource(tp))
		}
		if s.Name == valuation.ScenarioBase && sb.UpsidePct.Status == StatusAvailable {
			b.MarginOfSafety = sb.UpsidePct
			b.MarginOfSafetyBasis = "BASE 情境目標價相對現價的上檔空間（(目標價 − 現價) ÷ 現價）；" +
				"為負代表現價已高於 BASE 情境目標價"
		}
		b.Scenarios = append(b.Scenarios, sb)
	}
	return b
}

func targetSource(tp valuation.TargetPrice) string {
	return fmt.Sprintf("EPS × PE（規則：%s）", tp.Rule)
}

func dateOr(date, fallback string) string {
	if date != "" {
		return date
	}
	return fallback
}

func reasonOr(reason, fallback string) string {
	if reason != "" {
		return reason
	}
	return fallback
}
