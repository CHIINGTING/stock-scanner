package daytrading

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Field names of the per-stock table, identical on both sources (live-verified). The
// table is located and its columns are resolved BY NAME, never by table index or title:
// TWSE's title varies by date and TPEx ships the per-stock table with an EMPTY title, so
// neither is a usable anchor. Column order has been stable, but name lookup costs nothing
// and turns a silent column shift into a loud error.
const (
	fieldCode       = "證券代號"
	fieldName       = "證券名稱"
	fieldNote       = "暫停現股賣出後現款買進當沖註記"
	fieldShares     = "當日沖銷交易成交股數"
	fieldBuyAmount  = "當日沖銷交易買進成交金額"
	fieldSellAmount = "當日沖銷交易賣出成交金額"
)

// fieldTotalShares is the market-level summary column. Matching is EXACT: the same table
// also carries "當日沖銷交易總成交股數占市場比重%", which a prefix match would hit first.
const fieldTotalShares = "當日沖銷交易總成交股數"

// statTable is one table of a 當日沖銷 response. Both endpoints return the same envelope
// shape; only the date format in the request differs. totalCount is TPEx-only (TWSE omits
// it), so no-data detection uses len(data) instead.
type statTable struct {
	Title      string     `json:"title"`
	Fields     []string   `json:"fields"`
	Data       [][]string `json:"data"`
	TotalCount int        `json:"totalCount"`
}

// statResponse is the shared response envelope. stat is "OK" on TWSE and "ok" on TPEx,
// and BOTH report "OK" on a non-trading day (with empty tables), so stat alone can never
// decide whether a date has data.
type statResponse struct {
	Stat   string      `json:"stat"`
	Date   string      `json:"date"`
	Tables []statTable `json:"tables"`
}

// parseSnapshot turns a 當日沖銷 response body into a Snapshot. It is pure (no network) so
// it can be unit-tested against fixtures.
//
// Non-trading days are NOT errors: TWSE returns stat "OK" with a stub 3-column table and
// no rows, TPEx returns totalCount 0. Both yield a valid snapshot with zero Stocks and a
// nil error, and callers decide (via HasData) not to persist it. An error is reserved for
// a real failure: unreadable JSON, a date the source did not echo back, or a per-stock
// table that HAS rows but no longer has the columns we need.
func parseSnapshot(body []byte, market, asOfDate, source, sourceURL string, fetchedAt time.Time) (*Snapshot, error) {
	var r statResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("daytrading: parse json (%s %s): %w", market, asOfDate, err)
	}
	if err := checkPayloadDate(r.Date, asOfDate); err != nil {
		return nil, fmt.Errorf("daytrading: %s: %w", market, err)
	}

	snap := &Snapshot{
		SchemaVersion:     SchemaVersion,
		Market:            market,
		AsOfDate:          asOfDate,
		Unit:              UnitSharesNTD,
		Source:            source,
		SourceURL:         sourceURL,
		FetchedAt:         fetchedAt,
		MarketTotalShares: marketTotalShares(r.Tables, market, asOfDate),
	}

	tbl, ok := findTable(r.Tables, fieldCode)
	if !ok || len(tbl.Data) == 0 {
		return snap, nil // non-trading day (or not yet published): zero rows, no error
	}

	cols, err := stockColumns(tbl.Fields)
	if err != nil {
		// Rows exist but the contract moved. Refusing here is the point: parsing on a
		// guessed layout would write plausible-looking wrong numbers into the archive.
		return nil, fmt.Errorf("daytrading: %s %s: %w", market, asOfDate, err)
	}

	snap.Stocks = make([]StockDayTrade, 0, len(tbl.Data))
	for _, row := range tbl.Data {
		rec, err := parseStockRow(row, cols)
		if err != nil {
			// Drop, never zero-fill: a stock absent from the snapshot is MISSING, and the
			// study treats that as a gap. A zeroed row would read as "no day trading".
			log.Printf("daytrading %s %s: skip malformed row %v: %v", strings.ToLower(market), asOfDate, row, err)
			continue
		}
		snap.Stocks = append(snap.Stocks, rec)
	}
	return snap, nil
}

// checkPayloadDate guards against a source silently answering with a DIFFERENT day (which
// would file one day's figures under another date's filename — the one corruption this
// archive could not detect after the fact). An empty echo is tolerated; a mismatch is not.
func checkPayloadDate(payloadDate, asOfDate string) error {
	payloadDate = strings.TrimSpace(payloadDate)
	if payloadDate == "" {
		return nil
	}
	d, err := time.Parse(dateLayout, asOfDate)
	if err != nil {
		return fmt.Errorf("bad as-of date %q: %w", asOfDate, err)
	}
	if want := d.Format("20060102"); payloadDate != want {
		return fmt.Errorf("source answered for %s, requested %s", payloadDate, want)
	}
	return nil
}

// stockCols holds the resolved column positions of the per-stock table.
type stockCols struct {
	code, name, shares, buy, sell int
	note                          int // -1 when the source stops publishing the marker
}

// stockColumns resolves the per-stock columns by field name. The note column is optional;
// every other column is required, because a record missing any of them is not a usable
// observation.
func stockColumns(fields []string) (stockCols, error) {
	c := stockCols{note: -1}
	var err error
	if c.code, err = columnIndex(fields, fieldCode); err != nil {
		return c, err
	}
	if c.name, err = columnIndex(fields, fieldName); err != nil {
		return c, err
	}
	if c.shares, err = columnIndex(fields, fieldShares); err != nil {
		return c, err
	}
	if c.buy, err = columnIndex(fields, fieldBuyAmount); err != nil {
		return c, err
	}
	if c.sell, err = columnIndex(fields, fieldSellAmount); err != nil {
		return c, err
	}
	if i, err := columnIndex(fields, fieldNote); err == nil {
		c.note = i
	}
	return c, nil
}

// parseStockRow maps one source row to a StockDayTrade. Any unparseable numeric cell
// fails the whole row: partially-parsed rows are exactly the silent-zero failure mode this
// package exists to avoid.
func parseStockRow(row []string, c stockCols) (StockDayTrade, error) {
	last := c.code
	for _, i := range []int{c.name, c.shares, c.buy, c.sell, c.note} {
		if i > last {
			last = i
		}
	}
	if len(row) <= last {
		return StockDayTrade{}, fmt.Errorf("row has %d cells, need %d", len(row), last+1)
	}
	code := strings.TrimSpace(row[c.code])
	if code == "" {
		return StockDayTrade{}, fmt.Errorf("empty 證券代號")
	}
	shares, err := parseAmount(row[c.shares])
	if err != nil {
		return StockDayTrade{}, fmt.Errorf("%s %s: %w", code, fieldShares, err)
	}
	buy, err := parseAmount(row[c.buy])
	if err != nil {
		return StockDayTrade{}, fmt.Errorf("%s %s: %w", code, fieldBuyAmount, err)
	}
	sell, err := parseAmount(row[c.sell])
	if err != nil {
		return StockDayTrade{}, fmt.Errorf("%s %s: %w", code, fieldSellAmount, err)
	}
	rec := StockDayTrade{
		Code:           code,
		Name:           strings.TrimSpace(row[c.name]),
		DayTradeShares: shares,
		BuyAmount:      buy,
		SellAmount:     sell,
	}
	if c.note >= 0 {
		rec.Note = strings.TrimSpace(row[c.note])
	}
	return rec, nil
}

// marketTotalShares reads 當日沖銷交易總成交股數 off the summary table, or returns nil when
// that table is absent/empty/unparseable. nil is a deliberate MISSING, not a 0 — the
// per-stock rows stand on their own and must not be discarded because a header figure
// failed, but neither may a failed header figure be published as a real total.
func marketTotalShares(tables []statTable, market, asOfDate string) *int64 {
	tbl, ok := findTable(tables, fieldTotalShares)
	if !ok || len(tbl.Data) == 0 {
		return nil
	}
	idx, err := columnIndex(tbl.Fields, fieldTotalShares)
	if err != nil {
		return nil
	}
	row := tbl.Data[0]
	if len(row) <= idx {
		return nil
	}
	v, err := parseAmount(row[idx])
	if err != nil {
		log.Printf("daytrading %s %s: market total unparseable (%v) — recorded as missing",
			strings.ToLower(market), asOfDate, err)
		return nil
	}
	return &v
}

// findTable returns the first table whose FIELDS contain field (exact match). Tables are
// never selected by index or title: TWSE reorders and re-titles them by date, and TPEx
// ships the per-stock table with an empty title.
func findTable(tables []statTable, field string) (statTable, bool) {
	for _, t := range tables {
		if _, err := columnIndex(t.Fields, field); err == nil {
			return t, true
		}
	}
	return statTable{}, false
}

// columnIndex returns the position of an exactly-named field.
func columnIndex(fields []string, name string) (int, error) {
	for i, f := range fields {
		if strings.TrimSpace(f) == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("missing column %q", name)
}

// parseAmount parses one source cell — a comma-thousands integer — into int64.
//
// It returns an ERROR, never a zero, for an empty or non-numeric cell. Both sources
// populate every numeric cell of every published row, so an unparseable cell means the
// row is malformed or the contract changed; silently reading it as 0 would publish
// "this stock had no day trading", which is a different and false claim.
//
// Nothing is stripped except commas and surrounding space. In particular a '%' suffix is
// NOT tolerated: every cell this package parses is a count, and the only way a percentage
// reaches here is a mis-resolved column (TPEx's 占市場比重 sits beside the totals we do
// want) — which must fail loudly rather than land in the archive as a plausible integer.
func parseAmount(s string) (int64, error) {
	raw := s
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty cell (missing, not zero)")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an integer: %q", strings.TrimSpace(raw))
	}
	return n, nil
}
