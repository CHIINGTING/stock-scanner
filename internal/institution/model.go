// Package institution ingests and models per-stock 三大法人買賣超 (institutional
// buy/sell) for 上市 (TWSE) and 上櫃 (TPEx), so the scanner can show institutional
// chip CONTEXT (R10-1). It is a delayed-disclosure data layer: nothing here is a
// trade instruction, and nothing here affects existing scoring / action / sort / stop.
//
// The data contract is live-verified in docs/SPEC_R10_1_INSTITUTION_BIAS_SHADOW.md
// (§3 TWSE, §4 TPEx). Units are 股 (shares) on both sources.
package institution

import "time"

// SchemaVersion is the raw-snapshot schema version. Evolution is additive: new fields
// are optional / pointer so older snapshots keep unmarshalling (§20).
const SchemaVersion = 1

// Market identifiers.
const (
	MarketTWSE = "TWSE"
	MarketTPEX = "TPEX"
)

// Unit is always shares — both sources report 股 (verified §3/§4).
const UnitShares = "shares"

// Source identifiers (provenance).
const (
	SourceTWSE = "twse_t86"
	SourceTPEX = "tpex_dailytrade"
)

// Display categories (the P0 four-way collapse of the seven raw legs).
const (
	CategoryForeign = "FOREIGN"          // = ForeignTotal
	CategoryTrust   = "INVESTMENT_TRUST" // = Trust
	CategoryDealer  = "DEALER"           // = DealerTotal
	CategoryTotal   = "TOTAL"            // = ComputedTotal
)

// Transition values (§11).
const (
	TransitionNone      = "NONE"
	TransitionBuyToSell = "BUY_TO_SELL"
	TransitionSellToBuy = "SELL_TO_BUY"
)

// Leg is one institution leg's figures for one trading day, in 股 (shares). Net is
// always present when a row exists (live verification found zero empty cells). Buy/Sell
// are pointers so "source did not provide" (TWSE FOREIGN_TOTAL / DEALER_TOTAL) is
// distinguishable from a real zero.
type Leg struct {
	Net  int64  `json:"net"`
	Buy  *int64 `json:"buy,omitempty"`
	Sell *int64 `json:"sell,omitempty"`
}

// StockChip is one stock's raw seven-leg institutional record for one trading day.
// All seven legs + the official total are retained from v1 (§22 G) so future P1 needs
// no re-fetch.
type StockChip struct {
	Code string `json:"code"`
	Name string `json:"name"`

	ForeignExclDealer Leg `json:"foreign_excl_dealer"` // 外陸資(不含外資自營)
	ForeignDealer     Leg `json:"foreign_dealer"`      // 外資自營商
	ForeignTotal      Leg `json:"foreign_total"`       // 外資合計 (TPEx explicit; TWSE computed)
	Trust             Leg `json:"trust"`               // 投信
	DealerSelf        Leg `json:"dealer_self"`         // 自營商(自行買賣)
	DealerHedge       Leg `json:"dealer_hedge"`        // 自營商(避險)
	DealerTotal       Leg `json:"dealer_total"`        // 自營商合計

	OfficialTotalNet *int64 `json:"official_total_net,omitempty"` // 三大法人官方合計 (SourceTotal)
	Reconciled       bool   `json:"reconciled"`                   // §7 checks all passed
}

// Snapshot is one market's institutional data for one trading day.
type Snapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Market        string      `json:"market"`     // MarketTWSE | MarketTPEX
	AsOfDate      string      `json:"as_of_date"` // YYYY-MM-DD (ROC normalized on TPEx)
	Unit          string      `json:"unit"`       // UnitShares
	Source        string      `json:"source"`     // SourceTWSE | SourceTPEX
	SourceURL     string      `json:"source_url"`
	FetchedAt     time.Time   `json:"fetched_at"`
	Stocks        []StockChip `json:"stocks"`
}

// CategoryNets is the P0 four-way collapse for one trading day (§6).
//
//	Foreign = ForeignTotal.Net
//	Trust   = Trust.Net
//	Dealer  = DealerTotal.Net
//	Total   = ComputedTotal (Foreign + Trust + Dealer)
type CategoryNets struct {
	Foreign int64
	Trust   int64
	Dealer  int64
	Total   int64
}

// Nets collapses a raw StockChip into the four display categories. Total is the
// ComputedTotal (§7), NOT the source official total.
func (c StockChip) Nets() CategoryNets {
	f := c.ForeignTotal.Net
	t := c.Trust.Net
	d := c.DealerTotal.Net
	return CategoryNets{Foreign: f, Trust: t, Dealer: d, Total: f + t + d}
}

// LegStats is the interpreted per-category time-series view (§6). It is derived, never
// stored raw.
type LegStats struct {
	TodayNet            int64
	Sum3d               int64
	Sum5d               int64
	Sum10d              int64
	Sum20d              int64
	ConsecutiveBuyDays  int
	ConsecutiveSellDays int
	Transition          string       // TransitionNone | TransitionBuyToSell | TransitionSellToBuy
	NetBuyRatio         *float64     // % of same-day volume; nil when volume unavailable (§12)
	StreakComplete      bool         // the counted streak had no gap and reached a natural end
	CumulativeComplete  map[int]bool // window → whether that cumulative spanned no gap
}

// ChipCompleteness records how complete the institutional history is over the lookback
// window (§8). It is derived from the price-candle trading calendar vs. observed records.
type ChipCompleteness struct {
	ExpectedDays int      // trading days (candles) in the window
	ObservedDays int      // expected days with an institution record for this stock
	MissingDates []string // expected-but-missing (YYYY-MM-DD)
	DataComplete bool     // today's own record present
}

// StockChipView is the full interpreted per-stock view attached to the report/sidecar.
type StockChipView struct {
	Code          string
	Name          string
	AsOfDate      string
	Foreign       LegStats
	Trust         LegStats
	Dealer        LegStats
	Total         LegStats
	Completeness  ChipCompleteness
	TotalMismatch bool // any §7 reconciliation check failed on today's record
}
