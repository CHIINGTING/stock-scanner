package institution

import (
	"errors"
	"testing"
	"time"
)

// twseFixture is built from the LIVE-VERIFIED 2330 row on 2026-07-14 (SPEC §3), plus an
// ETF row (00632R) that the syntax prefilter must drop, and a short malformed row.
const twseFixture = `{
 "stat":"OK","date":"20260714",
 "fields":["證券代號","證券名稱","外陸資買進股數(不含外資自營商)","外陸資賣出股數(不含外資自營商)","外陸資買賣超股數(不含外資自營商)","外資自營商買進股數","外資自營商賣出股數","外資自營商買賣超股數","投信買進股數","投信賣出股數","投信買賣超股數","自營商買賣超股數","自營商買進股數(自行買賣)","自營商賣出股數(自行買賣)","自營商買賣超股數(自行買賣)","自營商買進股數(避險)","自營商賣出股數(避險)","自營商買賣超股數(避險)","三大法人買賣超股數"],
 "data":[
  ["00632R","元大台灣50反1","10,587,000","7,938,000","2,649,000","0","0","0","0","0","0","95,071,678","3,200,000","1,850,000","1,350,000","111,213,869","17,492,191","93,721,678","96,370,678"],
  ["2330","台積電","18,361,331","30,777,540","-12,416,209","0","0","0","1,926,459","85,390","1,841,069","2,569,779","1,073,215","580,444","492,771","3,334,832","1,257,824","2,077,008","-8,005,361"],
  ["9999","短行"]
 ]}`

func TestParseTWSE_VerifiedRow(t *testing.T) {
	snap, err := ParseTWSE([]byte(twseFixture), "2026-07-14", twseT86URL, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Market != MarketTWSE || snap.Unit != UnitShares || snap.Source != SourceTWSE {
		t.Fatalf("snapshot meta wrong: %+v", snap)
	}
	if len(snap.Stocks) != 1 { // ETF filtered, short row skipped
		t.Fatalf("want 1 stock (ETF+malformed dropped), got %d", len(snap.Stocks))
	}
	c := snap.Stocks[0]
	if c.Code != "2330" {
		t.Fatalf("code = %q", c.Code)
	}
	if c.ForeignExclDealer.Net != -12416209 {
		t.Errorf("foreign-excl net = %d", c.ForeignExclDealer.Net)
	}
	if c.ForeignTotal.Net != -12416209 { // computed = excl + dealer(0)
		t.Errorf("foreign-total net = %d", c.ForeignTotal.Net)
	}
	if c.ForeignTotal.Buy != nil || c.ForeignTotal.Sell != nil {
		t.Errorf("TWSE foreign-total buy/sell must be nil (not provided)")
	}
	if c.Trust.Net != 1841069 {
		t.Errorf("trust net = %d", c.Trust.Net)
	}
	if c.DealerTotal.Net != 2569779 {
		t.Errorf("dealer-total net = %d", c.DealerTotal.Net)
	}
	if c.DealerSelf.Net+c.DealerHedge.Net != c.DealerTotal.Net {
		t.Errorf("dealer self+hedge != total")
	}
	if c.OfficialTotalNet == nil || *c.OfficialTotalNet != -8005361 {
		t.Errorf("official total = %v", c.OfficialTotalNet)
	}
	// ComputedTotal reconciliation (§7).
	if got := c.Nets().Total; got != -8005361 {
		t.Errorf("computed total = %d, want -8005361", got)
	}
	if !c.Reconciled {
		t.Errorf("verified row must reconcile")
	}
	// Buy/Sell present for provided legs.
	if c.Trust.Buy == nil || *c.Trust.Buy != 1926459 {
		t.Errorf("trust buy = %v", c.Trust.Buy)
	}
}

func TestParseTWSE_Holiday(t *testing.T) {
	body := `{"stat":"很抱歉，沒有符合條件的資料!","total":0}`
	_, err := ParseTWSE([]byte(body), "2026-07-12", twseT86URL, time.Now())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("holiday must map to ErrNoData, got %v", err)
	}
}

func TestParseTWSE_OutOfRange(t *testing.T) {
	body := `{"stat":"查詢日期大於可查詢最大日期，請重新查詢!","total":0}`
	_, err := ParseTWSE([]byte(body), "2099-12-31", twseT86URL, time.Now())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("out-of-range still wraps ErrNoData, got %v", err)
	}
}
