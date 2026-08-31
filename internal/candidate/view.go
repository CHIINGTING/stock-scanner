package candidate

import "fmt"

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

	v.Blocks = buildBlocks(e)
	v.DataQuality = quality(e, v.Blocks)
	return v
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

	news := Block{Name: "News", Status: e.NewsStatus}
	if en.News != nil && en.News.Computed {
		news.Status = Available
		news.Detail = fmt.Sprintf("%d 則", len(en.News.StockItems))
	}
	blocks = append(blocks, news, marketBlock(e.Market), fxBlock(e.FX))
	return blocks
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
