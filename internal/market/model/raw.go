package model

import "time"

// RawSnapshot is what a Provider hands back: NUMBERS ONLY. Nothing in this file knows what
// bullish means. Whether -87,911 net short contracts is bearish, crowded, or about to
// squeeze is the analyzer's problem (M6).
//
// Every dataset is a POINTER. nil means MISSING — the source was down, the field was absent,
// or the date had no trading. That is deliberately distinct from a present struct holding
// zeros, which means "the market genuinely printed zero". Analyzers must branch on nil and
// emit MissingEvidence; they must never coerce nil into a neutral reading.

// SourceStatus records how one upstream call ended, so a snapshot can be audited months
// later without re-running anything.
type SourceStatus string

const (
	SourceOK      SourceStatus = "OK"      // data returned and parsed
	SourceNoData  SourceStatus = "NO_DATA" // upstream answered, but no trading data (holiday)
	SourceError   SourceStatus = "ERROR"   // transport / parse / validation failure
	SourceSkipped SourceStatus = "SKIPPED" // not requested this run
)

// SourceMeta is the provenance of one dataset. URL is kept so a surprising number can be
// traced back to the exact request that produced it.
type SourceMeta struct {
	Name      string       `json:"name"`
	URL       string       `json:"url,omitempty"`
	Status    SourceStatus `json:"status"`
	FetchedAt time.Time    `json:"fetched_at"`
	Err       string       `json:"err,omitempty"`
	// AsOfDate is the trading date the RESPONSE claims. It must equal the requested date;
	// TWSE silently serves the nearest trading day for a holiday request, and accepting
	// that would date-shift the whole snapshot.
	AsOfDate string `json:"as_of_date,omitempty"`
}

// IndexData is one session of the market index. Note the naming: this is whatever
// Benchmark the run used, NOT necessarily TAIEX. See Benchmark in structure.go.
type IndexData struct {
	Date   string  `json:"date"` // YYYY-MM-DD
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Change float64 `json:"change"`
	// Volume/Turnover are 0 when the source does not carry them (the index OHLC feed and
	// the turnover feed are different TWSE endpoints).
	Volume   float64 `json:"volume,omitempty"`
	Turnover float64 `json:"turnover,omitempty"`
}

// BreadthData is the official TWSE advance/decline count. Per the approved design this is
// SECONDARY validation data — the primary breadth engine counts the scanner's own universe
// against its moving averages (M2), which is the only version with two years of history.
//
// Counts are the 股票 column, never 整體市場: the latter includes warrants and ETFs and
// inflates the numbers by roughly 10x.
type BreadthData struct {
	Advancing int `json:"advancing"`
	Declining int `json:"declining"`
	Unchanged int `json:"unchanged"`
	LimitUp   int `json:"limit_up"`
	LimitDown int `json:"limit_down"`
}

// CashData is the market-level institutional cash flow (TWSE BFI82U). Amounts are in NTD.
//
// ForeignNet is the headline; Others keeps 投信 / 自營商 / 合計 so a later phase can use
// them without a re-fetch. Never derive ForeignNet as Buy-Sell — the official row carries
// its own net and the two can differ by rounding.
type CashData struct {
	ForeignBuy  float64            `json:"foreign_buy"`
	ForeignSell float64            `json:"foreign_sell"`
	ForeignNet  float64            `json:"foreign_net"`
	Others      map[string]float64 `json:"others,omitempty"` // 單位名稱 → 買賣差額
}

// MarginBalanceData is the market-level margin/short balance.
//
// Named "...BalanceData" rather than MarginData because module.go still owns a MarginData
// belonging to the pre-Evidence flow (it carries a pre-derived PriceChangePct, which this
// design deliberately keeps out of raw observations). The two coexist until M6 retires the
// old one; the name is also simply more accurate about what is inside.
//
// Source: TWSE MI_MARGN 信用交易統計.
//
// The endpoint reports both 前日餘額 and 今日餘額 in the same response, so the daily change
// is a subtraction rather than a second request against yesterday — one less date to get
// wrong.
type MarginBalanceData struct {
	MarginBalanceLots float64 `json:"margin_balance_lots"` // 融資今日餘額（交易單位）
	MarginPrevLots    float64 `json:"margin_prev_lots"`    // 融資前日餘額
	MarginBalanceKTWD float64 `json:"margin_balance_ktwd"` // 融資金額（仟元）今日
	MarginPrevKTWD    float64 `json:"margin_prev_ktwd"`
	ShortBalanceLots  float64 `json:"short_balance_lots"` // 融券今日餘額
	ShortPrevLots     float64 `json:"short_prev_lots"`
}

// MarginChangeLots is today minus yesterday in 交易單位. Positive = retail added leverage.
func (m MarginBalanceData) MarginChangeLots() float64 { return m.MarginBalanceLots - m.MarginPrevLots }

// ShortChangeLots is today minus yesterday for 融券.
func (m MarginBalanceData) ShortChangeLots() float64 { return m.ShortBalanceLots - m.ShortPrevLots }

// FuturesOIData is foreign institutional open interest in TAIFEX 臺股期貨.
//
// NetOI comes straight from the official 多空未平倉口數淨額 column — it is NOT derived as
// LongOI-ShortOI here, so a future column change surfaces as a mismatch instead of being
// silently papered over.
//
// There is deliberately NO DailyChange field: the change and the historical percentile are
// DERIVED by the analyzer from stored history (M6), not raw observations. Putting a derived
// value in the raw struct would make it impossible to tell what the exchange actually said.
type FuturesOIData struct {
	Contract string  `json:"contract"` // "臺股期貨"
	Item     string  `json:"item"`     // "外資及陸資"
	LongOI   float64 `json:"long_oi"`
	ShortOI  float64 `json:"short_oi"`
	NetOI    float64 `json:"net_oi"`
	// OtherItems keeps 投信 / 自營商 net OI for later phases.
	OtherItems map[string]float64 `json:"other_items,omitempty"`
}

// RawSnapshot is one day's complete observation, before any interpretation.
type RawSnapshot struct {
	Date      string    `json:"date"` // YYYY-MM-DD, the REQUESTED trading date
	FetchedAt time.Time `json:"fetched_at"`

	Index   *IndexData         `json:"index,omitempty"`
	Breadth *BreadthData       `json:"breadth,omitempty"`
	Cash    *CashData          `json:"cash,omitempty"`
	Margin  *MarginBalanceData `json:"margin,omitempty"`
	Futures *FuturesOIData     `json:"futures,omitempty"`

	Sources []SourceMeta `json:"sources,omitempty"`
}

// Has reports whether every named dataset is present. Used by the service to decide how
// much of the dashboard can be built.
func (r *RawSnapshot) Has(index, breadth, cash, margin, futures bool) bool {
	if r == nil {
		return false
	}
	if index && r.Index == nil {
		return false
	}
	if breadth && r.Breadth == nil {
		return false
	}
	if cash && r.Cash == nil {
		return false
	}
	if margin && r.Margin == nil {
		return false
	}
	if futures && r.Futures == nil {
		return false
	}
	return true
}

// PresentCount is how many of the five datasets arrived — the input to the completeness
// term of the confidence calculation (M7).
func (r *RawSnapshot) PresentCount() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, present := range []bool{
		r.Index != nil, r.Breadth != nil, r.Cash != nil, r.Margin != nil, r.Futures != nil,
	} {
		if present {
			n++
		}
	}
	return n
}

// Merge folds src into r, dataset by dataset. Two providers (TWSE and TAIFEX) each produce
// a partial RawSnapshot for the same date; this combines them without either one being able
// to blank out what the other already supplied.
func (r *RawSnapshot) Merge(src *RawSnapshot) {
	if r == nil || src == nil {
		return
	}
	if r.Index == nil {
		r.Index = src.Index
	}
	if r.Breadth == nil {
		r.Breadth = src.Breadth
	}
	if r.Cash == nil {
		r.Cash = src.Cash
	}
	if r.Margin == nil {
		r.Margin = src.Margin
	}
	if r.Futures == nil {
		r.Futures = src.Futures
	}
	r.Sources = append(r.Sources, src.Sources...)
}
