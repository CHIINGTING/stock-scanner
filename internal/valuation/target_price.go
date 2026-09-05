package valuation

import (
	"math"
	"strings"
)

// The deterministic target-price engine.
//
// # Target Price = EPS × PE Multiple, and both halves have to be traceable
//
// The multiplier comes from a RULE over this stock's own archived P/E distribution, never
// from an opinion. The earnings come from the exchange's own published figure. No model, no
// analyst consensus, and no annualisation is involved in either.
//
// # Which EPS, and why it is the implied trailing one
//
// R13-M5 established that the MOPS income statement reports CUMULATIVE earnings — the "Q2"
// row is Jan–Jun. This repo therefore has no TTM EPS, and manufacturing one by doubling a
// half-year figure is precisely the guess a valuation layer must not make. Using the
// cumulative figure directly would be worse still: multiplying six months of earnings by an
// annual P/E understates a fair value by roughly half, silently.
//
// So the EPS used here is the one IMPLIED by the exchange's own trailing P/E:
//
//	impliedEPS = close(TrailingDate) / trailingPE
//
// It needs nothing this repo does not already have from an authorized source — a price and a
// published ratio — and it recovers the exchange's OWN earnings base exactly, so the target
// and the current ratio are measured against one number rather than two that disagree.
//
// # Why the price in that division is NOT the current price
//
// The exchanges publish these ratios with a lag of several days, so the P/E in hand was
// computed for an EARLIER session than the one being analysed — Ratios.Date says which.
// Dividing today's close by that older P/E does not recover any earnings figure that ever
// existed: it returns the true EPS scaled by however much the price moved in between. On a
// stock that ran 8% in a week, every scenario would inherit an 8% error while presenting
// itself as the exchange's own number.
//
// The division therefore uses the close of the session the P/E was computed FOR. The current
// price still appears — in the upside, where it belongs, because upside is measured from what
// a buyer would pay now.
//
// The base price and its session date are both reported, so a reader can redo the division.
//
// Two properties of that division worth stating, because both are easy to "tidy up" wrongly:
//
//	precision  the exchanges publish the P/E to two decimals, so the recovered EPS carries a
//	           quantisation error of roughly ±0.02%. That is negligible beside the spread
//	           between P25 and P75, but it is not an exact figure and should not be shown as
//	           one to more decimals than it deserves.
//	raw close  the division uses the RAW close, not an adjusted or split-corrected one. The
//	           exchange computed its P/E from the raw close, so anything else reintroduces a
//	           scaling factor and the recovered EPS stops being the exchange's own.
//
// The implied EPS is reported on the result, labelled with its basis, so a reader can see
// exactly which earnings figure produced the number and never mistake it for a forecast.
//
// # When it refuses
//
// A P/E distribution built from fewer than MinHistoricalPESamples sessions describes those
// sessions, not a distribution. Below that threshold this returns INSUFFICIENT_DATA with the
// sample count — never a target computed from a median of one.

// ScenarioName identifies the three cases. They are the SAME earnings under three different
// multiples: this engine models a re-rating, not an earnings surprise, because an earnings
// scenario would need a forecast and there is no authorized source for one.
const (
	ScenarioBear = "BEAR"
	ScenarioBase = "BASE"
	ScenarioBull = "BULL"
)

// EPSBasis names WHICH earnings figure a target was built on. It exists so no reader ever has
// to guess, and so a future reader cannot mistake a trailing figure for a forward one.
const (
	// EPSTrailingImplied is close(EPSBaseDate) / published trailing P/E — the exchange's own
	// earnings base, recovered exactly.
	//
	// EPSBaseDate is the session the EXCHANGE computed that P/E for, which is several days
	// older than the analysis date because that is the publication lag. Dividing by the
	// analysis-date close instead would not recover any earnings figure that ever existed.
	// This constant is serialised and read downstream, so its definition is the authority on
	// which price is meant: it is the P/E's own session, never today's.
	EPSTrailingImplied = "TRAILING_IMPLIED"
	// EPSForward would be an analyst estimate. No authorized Taiwanese source exists; the
	// constant is defined so its absence is explicit rather than merely unmentioned.
	EPSForward = "FORWARD"
)

// PEMultipleRule names where the multiplier came from. Every target carries one, and there is
// deliberately no rule meaning "a sensible-looking number".
const (
	// RuleHistoricalPercentile — bear/base/bull are this stock's own P25 / median / P75.
	RuleHistoricalPercentile = "HISTORICAL_PERCENTILE"
	// RuleConfiguredRange — an explicit human-supplied range for this stock or its sector.
	// Used only when one is configured, and it names its own source.
	RuleConfiguredRange = "CONFIGURED_RANGE"
	// RuleNone — no rule could be applied, so there is no target.
	RuleNone = "NONE"
)

// PERange is an explicitly configured multiple range.
//
// It is the escape hatch for a stock whose archive is too young, and it is deliberately
// awkward: someone has to write the three numbers down AND say where they came from. That is
// the difference between an assumption and a guess, and it is why Source is required rather
// than defaulted — a range with no attribution is an invented multiple wearing a rule name.
type PERange struct {
	Bear   float64
	Base   float64
	Bull   float64
	Source string
}

// valid reports whether a configured range is usable, attributed and ordered.
func (r *PERange) valid() bool {
	if r == nil {
		return false
	}
	// No attribution, no range. Filling in a default here would turn "someone decided this"
	// into "the system decided this", which is the whole distinction this type exists to
	// preserve.
	if strings.TrimSpace(r.Source) == "" {
		return false
	}
	for _, v := range []float64{r.Bear, r.Base, r.Bull} {
		if !(v > 0) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.Bear <= r.Base && r.Base <= r.Bull
}

// TargetScenario is one case's arithmetic, with every input it used.
//
// All four numbers are pointers: a scenario that could not be computed has none of them, and
// a zero target price would be a catastrophic thing to render as a number.
type TargetScenario struct {
	Name   string       `json:"name"`
	Status Availability `json:"status"`
	Reason string       `json:"reason,omitempty"`

	EPS         *float64 `json:"eps,omitempty"`
	PEMultiple  *float64 `json:"pe_multiple,omitempty"`
	TargetPrice *float64 `json:"target_price,omitempty"`
	// UpsidePct is (target − current) / current × 100. Negative is a real answer: a stock
	// above its own historical P75 has downside to its base case, and hiding that would make
	// this engine a marketing tool.
	UpsidePct *float64 `json:"upside_pct,omitempty"`

	// ── provenance, repeated on every row ──
	//
	// These duplicate the parent's fields on purpose. A scenario row is the unit a person
	// reads, quotes and screenshots, and a multiple whose justification lives one level up
	// travels as a bare number the moment the row is lifted out of the page. Every row here
	// answers, on its own: which earnings, from where, times what multiple, decided by which
	// rule, over how many samples, as of when.
	EPSBasis    string `json:"eps_basis,omitempty"`
	EPSSource   string `json:"eps_source,omitempty"`
	Rule        string `json:"rule,omitempty"`
	RuleDetail  string `json:"rule_detail,omitempty"`
	SampleCount int    `json:"sample_count"`
	AsOf        string `json:"as_of,omitempty"`
}

// TargetPrice is the whole scenario set plus the provenance of every assumption in it.
type TargetPrice struct {
	Symbol string       `json:"symbol"`
	Status Availability `json:"status"`
	Reason string       `json:"reason,omitempty"`

	// AsOf is the analysis date every figure here is point-in-time to.
	AsOf string `json:"as_of,omitempty"`

	CurrentPrice *float64 `json:"current_price,omitempty"`
	// EPS is the figure every scenario multiplies, and EPSBasis says what kind of figure it
	// is. Nil when no target could be built.
	EPS       *float64 `json:"eps,omitempty"`
	EPSBasis  string   `json:"eps_basis,omitempty"`
	EPSSource string   `json:"eps_source,omitempty"`
	// EPSBasePrice and EPSBaseDate are the close and the session that recovered the EPS —
	// the exchange's P/E session, NOT the analysis date. Reported so the division can be
	// checked by hand.
	EPSBasePrice *float64 `json:"eps_base_price,omitempty"`
	EPSBaseDate  string   `json:"eps_base_date,omitempty"`

	// Rule and RuleDetail make the multiplier traceable to the evidence it came from.
	Rule       string `json:"rule"`
	RuleDetail string `json:"rule_detail,omitempty"`
	// SampleCount and RequiredSamples are carried even on failure: "4 of the 20 needed" says
	// the archive is filling up, where a bare INSUFFICIENT_DATA says nothing.
	SampleCount     int `json:"sample_count"`
	RequiredSamples int `json:"required_samples"`

	// Scenarios is always BEAR, BASE, BULL in that order, present even when unavailable —
	// so a UI renders three rows and three explanations rather than a collapsing table.
	Scenarios []TargetScenario `json:"scenarios"`

	// ForwardStatus records that a forward-earnings target is not merely absent but
	// unsourceable in this repo.
	ForwardStatus Availability `json:"forward_status"`
}

// TargetInput is everything ComputeTargetPrice needs.
type TargetInput struct {
	Symbol string
	// AsOf is the analysis date, carried onto the result and every scenario row.
	AsOf string
	// CurrentPrice is the close at AsOf. It is what upside is measured FROM, and it is
	// deliberately not what the EPS is recovered from.
	CurrentPrice *float64
	// EPSBasePrice is the close on the session the exchange computed its trailing P/E for,
	// and EPSBaseDate is that session. Nil when the series does not reach that far back, in
	// which case no target is produced — an EPS recovered against the wrong session is a
	// fabricated number wearing the exchange's name.
	EPSBasePrice *float64
	EPSBaseDate  string
	// Valuation is the archived, point-in-time valuation for the same date.
	Valuation Valuation
	// ConfiguredPE is an optional explicit range, used only when the historical distribution
	// is not usable. Nil in the default configuration.
	ConfiguredPE *PERange
	// MinSamples overrides MinHistoricalPESamples. Zero uses the constant.
	MinSamples int
}

// ComputeTargetPrice is a pure function: same inputs, same output, no I/O and no clock.
//
// It refuses far more often than it answers, and that is the design. Every refusal names the
// missing input, so the page can say "4 of the 20 sessions needed" instead of drawing three
// rows of zeros.
func ComputeTargetPrice(in TargetInput) TargetPrice {
	minSamples := in.MinSamples
	if minSamples <= 0 {
		minSamples = MinHistoricalPESamples
	}
	samples := in.Valuation.HistoricalPE.SampleCount
	out := TargetPrice{
		Symbol:          in.Symbol,
		AsOf:            in.AsOf,
		Status:          InsufficientData,
		Rule:            RuleNone,
		SampleCount:     samples,
		RequiredSamples: minSamples,
		ForwardStatus:   NotImplemented,
		Scenarios:       emptyScenarios(""),
	}

	// A non-positive close is not a price. Carrying it would render "0.00 TWD" as an
	// AVAILABLE figure beside a refusal that already said there is no price — and the
	// division below would be the least of the problems.
	price, hasPrice := finite(in.CurrentPrice)
	hasPrice = hasPrice && price > 0
	if hasPrice {
		p := price
		out.CurrentPrice = &p
	}

	// ── the multiplier ────────────────────────────────────────────────────────────────
	bearPE, basePE, bullPE, rule, detail, ok := multipleRange(in, minSamples)
	out.Rule, out.RuleDetail = rule, detail
	if !ok {
		out.Status = InsufficientData
		out.Reason = detail
		out.Scenarios = emptyScenarios(detail)
		return out
	}

	// ── the earnings ──────────────────────────────────────────────────────────────────
	if !hasPrice || price <= 0 {
		out.Status = Unavailable
		out.Reason = "沒有現價，無法表示目標價"
		out.Scenarios = emptyScenarios(out.Reason)
		return out
	}
	trailing, hasTrailing := finite(in.Valuation.TrailingPE)
	if !hasTrailing || trailing <= 0 {
		// Without the exchange's own P/E there is no earnings base at all. The alternative —
		// the cumulative reported EPS — covers a different period than the multiple does, and
		// annualising it is the exact guess this package refuses to make.
		out.Status = InsufficientData
		out.Reason = "交易所未公告本益比，沒有可乘的每股盈餘基準（財報公布的是累計數，" +
			"年化它就是猜測）"
		out.Scenarios = emptyScenarios(out.Reason)
		return out
	}
	basePrice, hasBase := finite(in.EPSBasePrice)
	if !hasBase || basePrice <= 0 {
		// The exchange's P/E belongs to its own session, and only that session's close
		// recovers the earnings behind it. Substituting the analysis-date close would return
		// the true EPS scaled by whatever the price did in between — a number that never
		// existed, presented as the exchange's own.
		out.Status = InsufficientData
		out.Reason = "沒有本益比對應交易日（" + in.EPSBaseDate + "）的收盤價，無法還原交易所的每股盈餘基礎"
		out.Scenarios = emptyScenarios(out.Reason)
		return out
	}
	eps := basePrice / trailing
	if !(eps > 0) || math.IsInf(eps, 0) {
		out.Status = Unavailable
		out.Reason = "還原出的每股盈餘基準不是可用的數字"
		out.Scenarios = emptyScenarios(out.Reason)
		return out
	}
	e, bp := eps, basePrice
	out.EPS = &e
	out.EPSBasePrice = &bp
	out.EPSBaseDate = in.EPSBaseDate
	out.EPSBasis = EPSTrailingImplied
	out.EPSSource = in.EPSBaseDate + " 收盤價 ÷ 同日交易所公告本益比（" + SourceName + "）"

	prov := provenance{
		basis: out.EPSBasis, source: out.EPSSource,
		rule: rule, detail: detail, samples: samples, asOf: in.AsOf,
	}
	out.Scenarios = []TargetScenario{
		scenario(ScenarioBear, eps, bearPE, price, prov),
		scenario(ScenarioBase, eps, basePE, price, prov),
		scenario(ScenarioBull, eps, bullPE, price, prov),
	}
	out.Status = Available
	return out
}

// provenance is the answer-set every scenario row has to be able to give on its own.
type provenance struct {
	basis, source, rule, detail, asOf string
	samples                           int
}

// multipleRange applies the multiplier rules in priority order.
//
// The order is the spec's, and each step is skipped only when its evidence is absent — never
// because the answer it produced looked wrong.
func multipleRange(in TargetInput, minSamples int) (bear, base, bull float64,
	rule, detail string, ok bool) {
	h := in.Valuation.HistoricalPE

	// 1. this stock's own P/E distribution.
	if h.Status == Available && h.SampleCount >= minSamples {
		p25, okP25 := finite(h.P25)
		med, okMed := finite(h.Median)
		p75, okP75 := finite(h.P75)
		if okP25 && okMed && okP75 && p25 > 0 && med > 0 && p75 > 0 {
			return p25, med, p75, RuleHistoricalPercentile,
				"本股自身 " + itoa(h.SampleCount) + " 個交易 session 的本益比分布：P25 / 中位數 / P75",
				true
		}
	}

	// 2. an explicitly configured range, when a human wrote one down.
	if in.ConfiguredPE.valid() {
		return in.ConfiguredPE.Bear, in.ConfiguredPE.Base, in.ConfiguredPE.Bull,
			RuleConfiguredRange,
			"人工設定的本益比區間（來源：" + strings.TrimSpace(in.ConfiguredPE.Source) + "）", true
	}

	// 3. nothing. The spec's remaining candidate — "current valuation regime" — is itself
	// derived from the same historical distribution, so it cannot be a fallback FOR that
	// distribution being absent. Inventing a market-average multiple here would be exactly
	// the "AI 覺得 25x 合理" the design forbids, with a rule name attached to make it look
	// principled.
	return 0, 0, 0, RuleNone,
		"歷史本益比樣本不足（" + itoa(h.SampleCount) + " 筆，需 " + itoa(minSamples) +
			"），且未設定人工本益比區間", false
}

// scenario computes one case.
func scenario(name string, eps, pe, price float64, prov provenance) TargetScenario {
	s := TargetScenario{
		Name: name, Status: Unavailable,
		EPSBasis: prov.basis, EPSSource: prov.source,
		Rule: prov.rule, RuleDetail: prov.detail, SampleCount: prov.samples, AsOf: prov.asOf,
	}
	if !(pe > 0) || math.IsInf(pe, 0) {
		s.Reason = "本益比倍數不是可用的數字"
		return s
	}
	target := eps * pe
	if math.IsNaN(target) || math.IsInf(target, 0) {
		s.Reason = "目標價計算結果不是有限數"
		return s
	}
	e, p, tp := eps, pe, target
	s.EPS, s.PEMultiple, s.TargetPrice = &e, &p, &tp
	if up, ok := UpsidePct(price, target); ok {
		u := up
		s.UpsidePct = &u
	} else {
		s.Reason = "現價為 0，無法表示上檔空間"
	}
	s.Status = Available
	return s
}

// UpsidePct is (target − current) / current × 100.
//
// ok is false when current is not a usable base. Dividing by a zero price would produce
// ±Inf, which renders as a plausible-looking "+Inf%" and is worse than no answer.
func UpsidePct(current, target float64) (float64, bool) {
	if !(current > 0) || math.IsInf(current, 0) || math.IsNaN(current) {
		return 0, false
	}
	if math.IsNaN(target) || math.IsInf(target, 0) {
		return 0, false
	}
	v := (target - current) / current * 100
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// emptyScenarios returns the three cases with no numbers, carrying one reason.
//
// They are present rather than omitted so a UI always renders three labelled rows: a table
// that silently loses its Bear row teaches a reader that there was no downside case.
func emptyScenarios(reason string) []TargetScenario {
	st := InsufficientData
	if reason == "" {
		reason = "未計算"
	}
	return []TargetScenario{
		{Name: ScenarioBear, Status: st, Reason: reason},
		{Name: ScenarioBase, Status: st, Reason: reason},
		{Name: ScenarioBull, Status: st, Reason: reason},
	}
}

// finite dereferences a pointer only when it holds a usable number.
func finite(p *float64) (float64, bool) {
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
		return 0, false
	}
	return *p, true
}

// itoa avoids importing strconv into this file for two call sites.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
