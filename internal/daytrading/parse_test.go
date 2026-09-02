package daytrading

import (
	"strings"
	"testing"
	"time"
)

// Comma-formatted integers are the only numeric form either source publishes. A cell that
// does not parse is a MALFORMED cell — the whole point of returning an error rather than 0
// is that 0 is a meaningful, and here false, statement about a stock's day trading.
func TestParseAmount(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "comma thousands", in: "3,066,000", want: 3066000},
		{name: "billions", in: "9,112,685,000", want: 9112685000},
		{name: "plain integer", in: "1000", want: 1000},
		{name: "real zero parses as zero", in: "0", want: 0},
		{name: "surrounding whitespace", in: "  1,687,000 ", want: 1687000},
		// A '%' cell can only be a mis-resolved column, so it must fail — including the
		// integer-looking form, which is the one that could otherwise pass unnoticed.
		{name: "tpex percent suffix", in: "28.97%", wantErr: true},
		{name: "tpex integer percent suffix", in: "51%", wantErr: true},
		{name: "negative", in: "-1,234", want: -1234},
		{name: "empty is MISSING not zero", in: "", wantErr: true},
		{name: "whitespace-only is MISSING not zero", in: "   ", wantErr: true},
		{name: "single space (tpex note cell)", in: " ", wantErr: true},
		{name: "malformed digits", in: "1,2x3", wantErr: true},
		{name: "dash placeholder", in: "--", wantErr: true},
		{name: "decimal is not an integer cell", in: "1234.5", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAmount(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseAmount(%q) = %d, want error", tc.in, got)
				}
				if got != 0 {
					t.Fatalf("a failed parse must not leak a value, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAmount(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseAmount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// The per-stock table is identified by its FIELDS, never by index or title: TWSE's title
// changes with the date and TPEx's is empty. This payload puts the per-stock table FIRST
// (index 0) with an empty title, and the summary table second.
func TestFindsStockTableByFieldsRegardlessOfPosition(t *testing.T) {
	const swapped = `{
 "stat":"OK","date":"20250815",
 "tables":[
  {"title":"",
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[["2330","台積電","","7,740,000","9,112,685,000","9,132,055,000"]]},
  {"title":"some other table entirely",
   "fields":["當日沖銷交易總成交股數","當日沖銷交易總成交股數占市場比重%"],
   "data":[["1,793,940,000","22.66"]]}
 ]}`
	snap, err := ParseTWSE([]byte(swapped), "2025-08-15", "u", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Stocks) != 1 || snap.Stocks[0].Code != "2330" {
		t.Fatalf("per-stock table not found at index 0: %+v", snap.Stocks)
	}
	if snap.MarketTotalShares == nil || *snap.MarketTotalShares != 1793940000 {
		t.Fatalf("summary table not found at index 1: %v", snap.MarketTotalShares)
	}
}

// Column POSITION is not assumed either: a reordered header must still parse correctly.
func TestResolvesColumnsByNameNotPosition(t *testing.T) {
	const reordered = `{
 "stat":"OK","date":"20250815",
 "tables":[
  {"title":"",
   "fields":["當日沖銷交易賣出成交金額","證券名稱","當日沖銷交易成交股數","證券代號","當日沖銷交易買進成交金額","暫停現股賣出後現款買進當沖註記"],
   "data":[["9,132,055,000","台積電","7,740,000","2330","9,112,685,000","*"]]}
 ]}`
	snap, err := ParseTWSE([]byte(reordered), "2025-08-15", "u", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := StockDayTrade{Code: "2330", Name: "台積電", DayTradeShares: 7740000,
		BuyAmount: 9112685000, SellAmount: 9132055000, Note: "*"}
	if snap.Stocks[0] != want {
		t.Errorf("got %+v, want %+v", snap.Stocks[0], want)
	}
}

// A malformed number drops its ROW. It must never be written as a zero, and it must never
// take the healthy rows down with it.
func TestMalformedRowIsDroppedNotZeroed(t *testing.T) {
	const body = `{
 "stat":"OK","date":"20250815",
 "tables":[
  {"title":"",
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[
    ["2330","台積電","","7,740,000","9,112,685,000","9,132,055,000"],
    ["2317","鴻海","","N/A","7,824,912,500","7,840,545,000"],
    ["1101","台泥","","1,000","",""]
   ]}
 ]}`
	snap, err := ParseTWSE([]byte(body), "2025-08-15", "u", time.Now())
	if err != nil {
		t.Fatalf("one bad row must not fail the payload, got %v", err)
	}
	if len(snap.Stocks) != 1 || snap.Stocks[0].Code != "2330" {
		t.Fatalf("want only the healthy row, got %+v", snap.Stocks)
	}
	for _, s := range snap.Stocks {
		if s.Code == "2317" || s.Code == "1101" {
			t.Errorf("%s was malformed and must be ABSENT, not present as zeros", s.Code)
		}
	}
}

// Rows present but the required columns gone means the contract moved. Parsing on a
// guessed layout would silently archive wrong numbers, so this is a hard error.
func TestMissingColumnWithRowsIsError(t *testing.T) {
	const body = `{
 "stat":"OK","date":"20250815",
 "tables":[
  {"title":"",
   "fields":["證券代號","證券名稱","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[["2330","台積電","9,112,685,000","9,132,055,000"]]}
 ]}`
	_, err := ParseTWSE([]byte(body), "2025-08-15", "u", time.Now())
	if err == nil {
		t.Fatalf("a dropped required column with rows present must be an error")
	}
	if !strings.Contains(err.Error(), fieldShares) {
		t.Errorf("error should name the missing column, got %v", err)
	}
}

// A source answering for a DIFFERENT day would file one day's figures under another
// date's filename — undetectable after the fact, so it is refused up front.
func TestPayloadDateMismatchIsError(t *testing.T) {
	const body = `{
 "stat":"OK","date":"20250814",
 "tables":[
  {"title":"","fields":["證券代號","證券名稱","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[["2330","台積電","7,740,000","9,112,685,000","9,132,055,000"]]}
 ]}`
	if _, err := ParseTWSE([]byte(body), "2025-08-15", "u", time.Now()); err == nil {
		t.Fatalf("a date mismatch must be an error")
	}
	// An absent echo is tolerated — some payloads simply omit it.
	noEcho := strings.Replace(body, `"date":"20250814",`, "", 1)
	if _, err := ParseTWSE([]byte(noEcho), "2025-08-15", "u", time.Now()); err != nil {
		t.Fatalf("an absent date echo must be tolerated, got %v", err)
	}
}

func TestParseBadJSONIsError(t *testing.T) {
	if _, err := ParseTWSE([]byte("<html>go away</html>"), "2025-08-15", "u", time.Now()); err == nil {
		t.Fatalf("non-JSON body must be an error")
	}
}
