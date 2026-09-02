package candidate

import (
	"fmt"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fundamental"
	"github.com/deep-huang/stock-scanner/internal/macro"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The single presentation projection for candidate research.
//
// It exists so the CLI, the report and anything later (a JSON API, an AI prompt) read the
// SAME interpretation of the same evidence. Without it each renderer would decide for itself
// what "no institutional data" looks like, and they would disagree — quietly, and only in the
// cases that matter.
//
// It projects. It does not compute: no indicator is recalculated here, no threshold is
// applied, and nothing it produces can reach a score or a decision.

// DataQuality summarises EVIDENCE COMPLETENESS. It is emphatically not a rating of the stock.
//
// The distinction matters enough to name here: a company with perfect data and terrible
// prospects is COMPLETE, and one with a missing news feed is PARTIAL. Anyone reading this as
// a quality score is reading it wrong, which is why there is no CandidateScore beside it.
type DataQuality string

const (
	// QualityComplete means every wired evidence block reported AVAILABLE.
	QualityComplete DataQuality = "COMPLETE"
	// QualityPartial means the stock was analysed but some blocks were unavailable or off.
	QualityPartial DataQuality = "PARTIAL"
	// QualityInsufficient means the price data itself was missing, so nothing could be built.
	QualityInsufficient DataQuality = "INSUFFICIENT"
)

// Block is one evidence area's availability plus a short human-readable detail.
type Block struct {
	Name   string
	Status Availability
	// Detail is a one-line summary shown only when Status is AVAILABLE. It is never used to
	// carry a value that would be meaningless without the status beside it.
	Detail string
}

// View is one candidate, ready to render.
type View struct {
	Symbol string
	Name   string
	Note   string

	// Status is the stock-level outcome; Reason explains a non-OK one.
	Status Status
	Reason string

	// Scanner evidence — READ-ONLY, and labelled as the scanner's throughout the UI so it is
	// never mistaken for a candidate-research verdict.
	HasScanner    bool
	Price         string
	Action        string
	Stage         string
	RocketScore   int
	ExplosionProb string

	Blocks      []Block
	DataQuality DataQuality

	// Fundamental is the ACTUAL reported financial picture, already formatted. Every value
	// here was derived by the fundamental service; this struct only carries strings, so a
	// renderer has nothing left to compute and no way to compute it differently.
	Fundamental FundamentalView
	Valuation   ValuationView
	// Macro is the shared market-wide context, already interpreted. The view carries the
	// domain's Research directly: it is already presentation-safe (states are strings,
	// missing values are nil) and re-projecting it would be a second place for "elevated"
	// to be defined.
	Macro *macro.Research
}

// ValuationView is the presentation projection of the price-relative measures.
//
// Strings throughout, for the same reason as FundamentalView: a number with a separate status
// invites a template to print the number and forget the status.
type ValuationView struct {
	Status Availability
	Source string

	TrailingPE    string
	TrailingDate  string
	PBRatio       string
	DividendYield string

	// Historical distribution.
	HistoryStatus Availability
	SampleCount   int
	WindowDays    int
	MedianPE      string
	P25PE         string
	P75PE         string
	Percentile    string

	// These have no source. Carried as explicit statuses so a reader learns there is nothing
	// to wait for, rather than assuming the computation failed.
	ForwardPE string
	PEG       string
	FairValue string
}

// FundamentalView is the presentation projection of reported financial data.
//
// Every field is a STRING, and an unavailable metric holds the word rather than a number.
// That is deliberate: a float64 with a separate status invites a template to print the float
// and forget the status, and "0.00%" is indistinguishable from a real zero.
type FundamentalView struct {
	Status Availability
	Reason string
	Source string

	// Period is the human-readable reporting window, e.g. "2026 H1 (累計)" — carrying the
	// cumulative marker into the label, because a reader who takes it for Q2 alone will halve
	// every conclusion.
	Period       string
	RevenueMonth string

	Revenue         string
	RevenueYoY      string
	RevenueMoM      string
	CumulativeEPS   string
	GrossMargin     string
	OperatingMargin string
	NetMargin       string
	PublishedAt     string
}

// BuildViews projects evidence onto views, PRESERVING ORDER.
//
// The order is the human's, expressed in candidate.yaml. Sorting here by RocketScore would
// silently replace their priorities with the scanner's, which is the one thing a research
// list is for.
func BuildViews(ev []Evidence) []View {
	out := make([]View, 0, len(ev))
	for _, e := range ev {
		out = append(out, buildView(e))
	}
	return out
}

func buildView(e Evidence) View {
	v := View{
		Symbol: e.Candidate.CanonicalSymbol(),
		Note:   e.Candidate.Note,
		Status: e.Status,
		Reason: e.Reason,
	}

	if e.Entry != nil {
		a := e.Entry.A
		v.HasScanner = true
		v.Name = a.Name
		v.Price = fmt.Sprintf("%.2f", a.Close)
		v.Action = string(e.Entry.WatchAction)
		v.Stage = string(e.Entry.RocketStage)
		v.RocketScore = e.Entry.RocketScore
		v.ExplosionProb = e.Entry.ExplosionProb
	}

	v.Fundamental = buildFundamentalView(e.Fundamental)
	v.Valuation = buildValuationView(e.Valuation)
	v.Macro = e.Macro
	v.Blocks = buildBlocks(e)
	v.DataQuality = quality(e, v.Blocks)
	return v
}

// buildFundamentalView formats a fundamental view. It performs no arithmetic beyond turning
// a ratio into a percentage for display — every metric arrives already derived.
func buildFundamentalView(f *fundamental.View) FundamentalView {
	if f == nil {
		// nil means the layer was switched off, which is a DECISION and must not read as a
		// missing feed.
		return FundamentalView{Status: Disabled}
	}
	out := FundamentalView{
		Status: Availability(f.Status), Reason: f.Reason, Source: f.Source,
		Revenue: unavailableText, RevenueYoY: unavailableText, RevenueMoM: unavailableText,
		CumulativeEPS: unavailableText, GrossMargin: unavailableText,
		OperatingMargin: unavailableText, NetMargin: unavailableText,
	}
	if f.Revenue != nil {
		out.RevenueMonth = fmt.Sprintf("%d-%02d", f.Revenue.Period.Year, f.Revenue.Period.Month)
		out.Revenue = fmt.Sprintf("%.0f 億", float64(f.Revenue.Revenue)/1e8)
		out.PublishedAt = f.Revenue.PublishedAt.Format("2006-01-02")
	}
	out.RevenueYoY = pct(f.RevenueMetrics.YoY)
	out.RevenueMoM = pct(f.RevenueMetrics.MoM)

	if f.Financials != nil {
		p := f.Financials.Period
		label := fmt.Sprintf("%d Q%d", p.Year, p.Quarter)
		if p.Cumulative {
			// The label says so explicitly. This source reports year-to-date, and a reader
			// who takes "2026 Q2" for three months of activity will be wrong by half.
			label = fmt.Sprintf("%d 前 %d 季累計", p.Year, p.Quarter)
		}
		out.Period = label
		if f.Financials.CumulativeEPS != nil {
			out.CumulativeEPS = fmt.Sprintf("%.2f", *f.Financials.CumulativeEPS)
		}
		if out.PublishedAt == "" {
			out.PublishedAt = f.Financials.PublishedAt.Format("2006-01-02")
		}
	}
	out.GrossMargin = pct(f.FinancialMetrics.GrossMargin)
	out.OperatingMargin = pct(f.FinancialMetrics.OperatingMargin)
	out.NetMargin = pct(f.FinancialMetrics.NetMargin)
	return out
}

// buildValuationView formats a valuation. No arithmetic: every number arrives computed.
func buildValuationView(v *valuation.Valuation) ValuationView {
	if v == nil {
		return ValuationView{Status: Disabled}
	}
	out := ValuationView{
		Status: Availability(v.Status), Source: v.Source,
		TrailingPE: unavailableText, PBRatio: unavailableText, DividendYield: unavailableText,
		HistoryStatus: Availability(v.HistoricalPE.Status),
		SampleCount:   v.HistoricalPE.SampleCount,
		WindowDays:    v.HistoricalPE.WindowDays,
		MedianPE:      unavailableText, P25PE: unavailableText, P75PE: unavailableText,
		Percentile: unavailableText,
		// Rendered as the STATUS itself — "NOT_IMPLEMENTED" tells a reader there is no
		// source, which "UNAVAILABLE" would not.
		ForwardPE: string(v.ForwardPEStatus),
		PEG:       string(v.PEGStatus),
		FairValue: string(v.FairValueStatus),
	}
	out.TrailingDate = v.TrailingDate
	if v.TrailingPE != nil {
		out.TrailingPE = fmt.Sprintf("%.2f", *v.TrailingPE)
	}
	if v.PBRatio != nil {
		out.PBRatio = fmt.Sprintf("%.2f", *v.PBRatio)
	}
	if v.DividendYield != nil {
		out.DividendYield = fmt.Sprintf("%.2f%%", *v.DividendYield)
	}
	h := v.HistoricalPE
	if h.Median != nil {
		out.MedianPE = fmt.Sprintf("%.2f", *h.Median)
	}
	if h.P25 != nil {
		out.P25PE = fmt.Sprintf("%.2f", *h.P25)
	}
	if h.P75 != nil {
		out.P75PE = fmt.Sprintf("%.2f", *h.P75)
	}
	if h.CurrentPercentile != nil {
		out.Percentile = fmt.Sprintf("%.0f%%", *h.CurrentPercentile*100)
	}
	return out
}

// unavailableText is what a missing metric renders as. Never "0", never "-", never "N/A" —
// each of those can be read as a value.
const unavailableText = "UNAVAILABLE"

// pct renders a ratio as a percentage, or says why it cannot.
func pct(m fundamental.Metric) string {
	if m.Status != fundamental.Available {
		if m.Reason != "" && strings.HasPrefix(m.Reason, "NOT_MEANINGFUL") {
			return "NOT_MEANINGFUL"
		}
		return unavailableText
	}
	return fmt.Sprintf("%+.1f%%", m.Value*100)
}

// buildBlocks reports each evidence area.
//
// A nil pointer means the layer did not run — which is a DISABLED feature or absent data, and
// never "the reading was zero". That is why every block reports a status rather than a value
// that a reader would have to interpret.
func buildBlocks(e Evidence) []Block {
	// A stock with no entry has no per-stock evidence at all; only the shared contexts.
	if e.Entry == nil {
		return []Block{
			{Name: "Technical", Status: Unavailable},
			{Name: "Bias", Status: Unavailable},
			{Name: "Candlestick", Status: Unavailable},
			{Name: "Trend Extension", Status: Unavailable},
			{Name: "Sector", Status: Unavailable},
			{Name: "Institution", Status: e.InstitutionStatus},
			{Name: "News", Status: e.NewsStatus},
			fundamentalBlock(e.Fundamental),
			marketBlock(e.Market),
			fxBlock(e.FX),
		}
	}

	en := e.Entry
	blocks := []Block{
		ptrBlock("Technical", en.Technical != nil, technicalDetail(e)),
		ptrBlock("Bias", en.Bias != nil, biasDetail(e)),
		ptrBlock("Candlestick", en.Candlestick != nil, candlestickDetail(e)),
		ptrBlock("Trend Extension", en.TrendExt != nil, trendDetail(e)),
	}

	sector := Block{Name: "Sector", Status: Unavailable}
	if en.HasSector {
		sector.Status = Available
		sector.Detail = en.Sector
	}
	blocks = append(blocks, sector)

	inst := Block{Name: "Institution", Status: e.InstitutionStatus}
	if inst.Status == Available && en.Institution != nil {
		// A zero net IS a reading and is shown as such — the status above already says the
		// data exists, so "0" here cannot be mistaken for "we do not know".
		inst.Detail = fmt.Sprintf("外資今日 %+d 張", en.Institution.Foreign.TodayNet)
	}
	blocks = append(blocks, inst)

	fund := Block{Name: "Fundamental", Status: Disabled}
	if e.Fundamental != nil {
		fund.Status = Availability(e.Fundamental.Status)
		if fund.Status == Available || fund.Status == Partial {
			fund.Detail = e.Fundamental.Source
		}
	}

	mac := Block{Name: "Macro", Status: Disabled}
	if e.Macro != nil {
		mac.Status = Availability(e.Macro.Status)
		if mac.Status == Available || mac.Status == Partial {
			mac.Detail = "NY Fed · Treasury · BLS · CBOE"
		}
	}

	val := Block{Name: "Valuation", Status: Disabled}
	if e.Valuation != nil {
		val.Status = Availability(e.Valuation.Status)
		if val.Status == Available || val.Status == Partial {
			val.Detail = e.Valuation.Source
		}
	}

	news := Block{Name: "News", Status: e.NewsStatus}
	if en.News != nil && en.News.Computed {
		news.Status = Available
		news.Detail = fmt.Sprintf("%d 則", len(en.News.StockItems))
	}
	blocks = append(blocks, news, fund, val, marketBlock(e.Market), fxBlock(e.FX), mac)
	return blocks
}

// fundamentalBlock reports the fundamental layer for a stock with no scanner entry.
func fundamentalBlock(f *fundamental.View) Block {
	if f == nil {
		return Block{Name: "Fundamental", Status: Disabled}
	}
	b := Block{Name: "Fundamental", Status: Availability(f.Status)}
	if b.Status == Available || b.Status == Partial {
		b.Detail = f.Source
	}
	return b
}

func ptrBlock(name string, present bool, detail string) Block {
	if !present {
		// The layer produced nothing. Whether that is a switched-off flag or thin history is
		// not knowable from the pointer alone, so the honest answer is the weaker one.
		return Block{Name: name, Status: Unavailable}
	}
	return Block{Name: name, Status: Available, Detail: detail}
}

func technicalDetail(e Evidence) string {
	if e.Entry == nil || e.Entry.Technical == nil {
		return ""
	}
	return "computed"
}

func biasDetail(e Evidence) string {
	b := e.Entry.Bias
	if b == nil {
		return ""
	}
	if b.Bias20 == nil {
		// The layer ran but this window had too little history — still not a zero.
		return "BIAS20 unavailable"
	}
	return fmt.Sprintf("BIAS20 %+.2f%% (%s)", *b.Bias20, b.Risk)
}

func candlestickDetail(e Evidence) string {
	c := e.Entry.Candlestick
	if c == nil {
		return ""
	}
	if len(c.Signals) == 0 {
		return string(c.Status)
	}
	return fmt.Sprintf("%s (%s)", c.Signals[0].Type, c.Signals[0].Direction)
}

func trendDetail(e Evidence) string {
	t := e.Entry.TrendExt
	if t == nil {
		return ""
	}
	return string(t.State)
}

func marketBlock(m MarketContext) Block {
	b := Block{Name: "Market", Status: m.Status}
	if m.Status == Available {
		b.Detail = m.Regime
		if m.Score != nil {
			b.Detail = fmt.Sprintf("%s · %.1f", m.Regime, *m.Score)
		}
	}
	return b
}

func fxBlock(f FXContext) Block {
	b := Block{Name: "FX", Status: f.Status}
	if f.Status == Available && f.USDTWD != nil {
		b.Detail = fmt.Sprintf("USD/TWD %.3f %s", f.USDTWD.Close, f.USDTWD.Context)
	}
	return b
}

// quality summarises completeness — and only completeness.
func quality(e Evidence, blocks []Block) DataQuality {
	if e.Status != StatusOK {
		return QualityInsufficient
	}
	for _, b := range blocks {
		if b.Status != Available {
			return QualityPartial
		}
	}
	return QualityComplete
}
