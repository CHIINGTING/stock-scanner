package daytrading

import (
	"strings"
	"testing"
	"time"
)

// tpexFixture is built from the LIVE-VERIFIED 2025-08-15 intraday/stat payload. Two
// details differ from TWSE and are both exercised here: the per-stock table's title is
// EMPTY (so it can only be found by its fields), and the summary table's percentage cells
// carry a "%" suffix. The note column is a single space on TPEx, which must trim away.
const tpexFixture = `{
 "date":"20250815","stat":"ok",
 "tables":[
  {"title":"現股當沖交易統計資訊","subtitle":"114年08月15日  當日沖銷交易統計資訊 (股/元)","date":"114/08/15",
   "fields":["當日沖銷交易總成交股數","當日沖銷交易總成交股數占市場比重","當日沖銷交易總買進成交金額","當日沖銷交易總買進成交金額占市場比重","當日沖銷交易總賣出成交金額","當日沖銷交易總賣出成交金額占市場比重"],
   "data":[["733,411,000","28.97%","88,946,002,660","51.10%","89,216,994,390","51.26%"]]},
  {"title":"","subtitle":"114年08月15日 當日沖銷交易標的及成交量值(股/元)","date":"114/08/15","totalCount":3,
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[
    ["3105","穩懋"," ","1,687,000","148,343,700","148,640,700"],
    ["5483","中美晶"," ","585,000","58,694,500","58,809,300"],
    ["6488","環球晶"," ","284,000","102,539,000","102,660,500"]
   ]}
 ]}`

func TestParseTPEX_VerifiedPayload(t *testing.T) {
	snap, err := ParseTPEX([]byte(tpexFixture), "2025-08-15", tpexIntradayStatURL, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Market != MarketTPEX || snap.Source != SourceTPEX || snap.Unit != UnitSharesNTD {
		t.Fatalf("snapshot meta wrong: %+v", snap)
	}
	if len(snap.Stocks) != 3 {
		t.Fatalf("want 3 stocks, got %d", len(snap.Stocks))
	}
	if snap.MarketTotalShares == nil || *snap.MarketTotalShares != 733411000 {
		t.Fatalf("market total = %v, want 733411000", snap.MarketTotalShares)
	}
	first := snap.Stocks[0]
	want := StockDayTrade{Code: "3105", Name: "穩懋", DayTradeShares: 1687000, BuyAmount: 148343700, SellAmount: 148640700}
	if first != want {
		t.Errorf("first row = %+v, want %+v", first, want)
	}
	if first.Note != "" {
		t.Errorf("TPEx's single-space note cell must trim to empty, got %q", first.Note)
	}
}

// TPEx signals a non-trading day with totalCount 0 and empty data arrays, while stat stays
// "ok". Zero rows, no error.
func TestParseTPEX_NonTradingDay(t *testing.T) {
	const holiday = `{
 "date":"20250816","stat":"ok",
 "tables":[
  {"title":"現股當沖交易統計資訊",
   "fields":["當日沖銷交易總成交股數","當日沖銷交易總成交股數占市場比重","當日沖銷交易總買進成交金額","當日沖銷交易總買進成交金額占市場比重","當日沖銷交易總賣出成交金額","當日沖銷交易總賣出成交金額占市場比重"],
   "data":[]},
  {"title":"","totalCount":0,
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[]}
 ]}`
	snap, err := ParseTPEX([]byte(holiday), "2025-08-16", tpexIntradayStatURL, time.Now())
	if err != nil {
		t.Fatalf("a non-trading day must not be an error, got %v", err)
	}
	if snap.HasData() {
		t.Fatalf("want zero rows, got %d", len(snap.Stocks))
	}
	if snap.MarketTotalShares != nil {
		t.Errorf("empty summary table → nil total, got %v", *snap.MarketTotalShares)
	}
}

func TestTPEXURL(t *testing.T) {
	// TPEx wants YYYY/MM/DD — the slashes matter, and this is the one place the two
	// markets' date formats diverge.
	got, err := tpexURL("2025-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "https://www.tpex.org.tw/www/zh-tw/intraday/stat?date=2025/08/15&response=json&type=Daily"
	if got != want {
		t.Errorf("tpexURL =\n %s\nwant\n %s", got, want)
	}
	if _, err := tpexURL("20250815"); err == nil {
		t.Errorf("a non YYYY-MM-DD input must be rejected")
	}
}

// The two builders must not converge on one format.
func TestURLBuildersUseDifferentDateFormats(t *testing.T) {
	tw, _ := twseURL("2026-01-02")
	tp, _ := tpexURL("2026-01-02")
	if !strings.Contains(tw, "date=20260102") {
		t.Errorf("TWSE url missing YYYYMMDD date: %s", tw)
	}
	if !strings.Contains(tp, "date=2026/01/02") {
		t.Errorf("TPEx url missing YYYY/MM/DD date: %s", tp)
	}
}
