// Package daytrading ingests and models per-stock 當日沖銷 (day-trading) volume for
// 上市 (TWSE) and 上櫃 (TPEx), so a research study can compute a per-stock
// DayTradingRatio (當沖成交股數 ÷ 該股當日成交股數) from local snapshots.
//
// This is a DATA INGESTION layer only: nothing here feeds the scanner, scoring, report
// or any signal. It mirrors internal/institution — same snapshot-on-disk contract, same
// "fetch offline, load at read time" split — so the two chip datasets stay operationally
// interchangeable.
//
// Both sources publish after the close, so a date's figures are known to anything running
// after the close on that same day. Units: 成交股數 is 股 (shares); 買進/賣出成交金額 is
// NTD (元) — verified live against both endpoints.
package daytrading

import "time"

// SchemaVersion is the raw-snapshot schema version. Evolution is additive: new fields are
// optional / pointer so older snapshots keep unmarshalling.
const SchemaVersion = 1

// Market identifiers.
const (
	MarketTWSE = "TWSE"
	MarketTPEX = "TPEX"
)

// Unit describes the paired units of a record: shares for the volume leg, NTD for the two
// amount legs. One constant because a snapshot carries exactly one such pairing.
const UnitSharesNTD = "shares+ntd"

// Source identifiers (provenance).
const (
	SourceTWSE = "twse_twtb4u"
	SourceTPEX = "tpex_intraday_stat"
)

// StockDayTrade is one stock's 當日沖銷 record for one trading day.
//
// DayTradeShares counts the day-traded shares ONCE (it is not buy+sell double counted),
// which is what makes it directly comparable to the stock's own 成交股數. BuyAmount and
// SellAmount are the matching NTD turnovers and differ by the day's price drift.
//
// All three figures are plain int64, never pointers: the sources populate every numeric
// cell on every published row, so an unparseable cell means a MALFORMED row, not a zero.
// Such a row is dropped with a log line rather than written as 0 — MISSING ≠ ZERO.
type StockDayTrade struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	DayTradeShares int64  `json:"day_trade_shares"` // 當日沖銷交易成交股數 (股)
	BuyAmount      int64  `json:"buy_amount"`       // 當日沖銷交易買進成交金額 (NTD)
	SellAmount     int64  `json:"sell_amount"`      // 當日沖銷交易賣出成交金額 (NTD)

	// Note is 暫停現股賣出後現款買進當沖註記 verbatim (empty on almost every row). Kept
	// because it is the only per-row eligibility marker either source publishes, and
	// re-fetching history later to recover it would be expensive.
	Note string `json:"note,omitempty"`
}

// Snapshot is one market's 當日沖銷 data for one trading day.
type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Market        string    `json:"market"`     // MarketTWSE | MarketTPEX
	AsOfDate      string    `json:"as_of_date"` // YYYY-MM-DD
	Unit          string    `json:"unit"`       // UnitSharesNTD
	Source        string    `json:"source"`     // SourceTWSE | SourceTPEX
	SourceURL     string    `json:"source_url"`
	FetchedAt     time.Time `json:"fetched_at"`

	// MarketTotalShares is the market-level 當日沖銷交易總成交股數 (股) from the summary
	// table that both endpoints ship alongside the per-stock table. It is a POINTER
	// because that summary table can be absent or empty while the per-stock table is
	// fine: nil means "the source did not publish it", which is not the same claim as 0.
	MarketTotalShares *int64 `json:"market_total_shares,omitempty"`

	Stocks []StockDayTrade `json:"stocks"`
}

// HasData reports whether the snapshot carries any per-stock row. A parse of a
// non-trading day succeeds and yields a snapshot with zero rows (see ParseTWSE), so
// callers gate on this rather than on an error.
func (s *Snapshot) HasData() bool { return s != nil && len(s.Stocks) > 0 }
