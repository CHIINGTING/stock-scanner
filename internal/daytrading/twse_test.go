package daytrading

import (
	"testing"
	"time"
)

// twseFixture is built from the LIVE-VERIFIED 2025-08-15 TWTB4U payload: the market-level
// summary table first, then the per-stock table. Three real rows are kept, plus a short
// row that the row parser must drop.
const twseFixture = `{
 "stat":"OK","date":"20250815",
 "tables":[
  {"title":"114年08月15日 當日沖銷交易統計資訊",
   "fields":["當日沖銷交易總成交股數","當日沖銷交易總成交股數占市場比重%","當日沖銷交易總買進成交金額","當日沖銷交易總買進成交金額占市場比重%","當日沖銷交易總賣出成交金額","當日沖銷交易總賣出成交金額占市場比重%"],
   "data":[["1,793,940,000","22.66","167,254,960,510","36.82","167,724,529,190","36.92"]]},
  {"title":"114年08月15日 當日沖銷交易標的及成交量值",
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記","當日沖銷交易成交股數","當日沖銷交易買進成交金額","當日沖銷交易賣出成交金額"],
   "data":[
    ["0050","元大台灣50","","3,066,000","162,579,450","162,954,100"],
    ["2317","鴻海","","38,208,000","7,824,912,500","7,840,545,000"],
    ["2330","台積電","","7,740,000","9,112,685,000","9,132,055,000"],
    ["9999","短行"]
   ]}
 ]}`

func TestParseTWSE_VerifiedPayload(t *testing.T) {
	snap, err := ParseTWSE([]byte(twseFixture), "2025-08-15", twseTWTB4UURL, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Market != MarketTWSE || snap.Unit != UnitSharesNTD || snap.Source != SourceTWSE {
		t.Fatalf("snapshot meta wrong: %+v", snap)
	}
	if snap.SchemaVersion != SchemaVersion || snap.AsOfDate != "2025-08-15" {
		t.Fatalf("snapshot identity wrong: %+v", snap)
	}
	if !snap.HasData() {
		t.Fatalf("a trading day must report HasData")
	}
	if len(snap.Stocks) != 3 { // the short row is dropped, never zero-filled
		t.Fatalf("want 3 stocks, got %d (%+v)", len(snap.Stocks), snap.Stocks)
	}
	// Market-level total comes off the SUMMARY table, matched exactly so that the
	// "...占市場比重%" column right next to it is not picked up instead.
	if snap.MarketTotalShares == nil || *snap.MarketTotalShares != 1793940000 {
		t.Fatalf("market total = %v, want 1793940000", snap.MarketTotalShares)
	}

	want := map[string]StockDayTrade{
		"0050": {Code: "0050", Name: "元大台灣50", DayTradeShares: 3066000, BuyAmount: 162579450, SellAmount: 162954100},
		"2317": {Code: "2317", Name: "鴻海", DayTradeShares: 38208000, BuyAmount: 7824912500, SellAmount: 7840545000},
		"2330": {Code: "2330", Name: "台積電", DayTradeShares: 7740000, BuyAmount: 9112685000, SellAmount: 9132055000},
	}
	got := map[string]StockDayTrade{}
	for _, s := range snap.Stocks {
		got[s.Code] = s
	}
	for code, w := range want {
		g, ok := got[code]
		if !ok {
			t.Errorf("%s missing from snapshot", code)
			continue
		}
		if g != w {
			t.Errorf("%s = %+v, want %+v", code, g, w)
		}
	}
	// ETFs are deliberately RETAINED: the ingester stays lossless and universe filtering
	// is the study's decision, not this layer's.
	if _, ok := got["0050"]; !ok {
		t.Errorf("ETF rows must be retained by the ingester")
	}
}

// A TWSE non-trading day still answers stat "OK", but the only table carrying 證券代號 is a
// 3-column stub with no rows. That is NO DATA, not an error and not a malformed contract.
func TestParseTWSE_NonTradingDay(t *testing.T) {
	const holiday = `{
 "stat":"OK","date":"20250816",
 "tables":[
  {"title":"114年08月16日 當日沖銷交易標的",
   "fields":["證券代號","證券名稱","暫停現股賣出後現款買進當沖註記"],
   "data":[]},
  {}
 ]}`
	snap, err := ParseTWSE([]byte(holiday), "2025-08-16", twseTWTB4UURL, time.Now())
	if err != nil {
		t.Fatalf("a non-trading day must not be an error, got %v", err)
	}
	if len(snap.Stocks) != 0 || snap.HasData() {
		t.Fatalf("want zero rows, got %d", len(snap.Stocks))
	}
	if snap.MarketTotalShares != nil {
		t.Errorf("no summary table published → market total must stay nil (MISSING ≠ ZERO)")
	}
}

// stat != "OK" (e.g. a date the source will not serve) is likewise "no data for that
// date", not a fetch failure.
func TestParseTWSE_StatNotOK(t *testing.T) {
	const body = `{"stat":"很抱歉，沒有符合條件的資料!","tables":[]}`
	snap, err := ParseTWSE([]byte(body), "2099-12-31", twseTWTB4UURL, time.Now())
	if err != nil {
		t.Fatalf("stat != OK must degrade to zero rows, got %v", err)
	}
	if len(snap.Stocks) != 0 {
		t.Fatalf("want zero rows, got %d", len(snap.Stocks))
	}
}

func TestTWSEURL(t *testing.T) {
	// TWSE wants YYYYMMDD with no separators.
	got, err := twseURL("2025-08-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "https://www.twse.com.tw/exchangeReport/TWTB4U?response=json&date=20250815&selectType=All"
	if got != want {
		t.Errorf("twseURL =\n %s\nwant\n %s", got, want)
	}
	if _, err := twseURL("2025/08/15"); err == nil {
		t.Errorf("a non YYYY-MM-DD input must be rejected")
	}
}
