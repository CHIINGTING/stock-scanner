package institution

import (
	"errors"
	"testing"
	"time"
)

// tpexFixture is built from the LIVE-VERIFIED 3105 穩懋 row on 2026-07-14 (SPEC §4),
// plus a bond-ETF row (00679B) the prefilter must drop.
const tpexFixture = `{
 "stat":"ok",
 "tables":[{
  "date":"115/07/14","totalCount":2,
  "fields":["代號","名稱","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","買進股數","賣出股數","買賣超股數","三大法人買賣超股數合計"],
  "data":[
   ["00679B","元大美債20年","7,018,000","5,675,000","1,343,000","0","0","0","7,018,000","5,675,000","1,343,000","0","0","0","0","0","0","0","0","0","0","0","0","1,343,000"],
   ["3105","穩懋","7,012,086","6,724,205","287,881","0","0","0","7,012,086","6,724,205","287,881","18,100","0","18,100","298,210","407,100","-108,890","240,099","260,895","-20,796","538,309","667,995","-129,686","176,295"]
  ]
 }]}`

func TestParseTPEX_VerifiedRow(t *testing.T) {
	snap, err := ParseTPEX([]byte(tpexFixture), "2026-07-14", tpexDailyTradeURL, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Market != MarketTPEX || snap.Unit != UnitShares {
		t.Fatalf("meta wrong: %+v", snap)
	}
	if len(snap.Stocks) != 1 { // bond ETF dropped
		t.Fatalf("want 1 stock, got %d", len(snap.Stocks))
	}
	c := snap.Stocks[0]
	if c.Code != "3105" {
		t.Fatalf("code = %q", c.Code)
	}
	if c.ForeignTotal.Net != 287881 {
		t.Errorf("foreign-total net = %d", c.ForeignTotal.Net)
	}
	if c.ForeignTotal.Buy == nil || *c.ForeignTotal.Buy != 7012086 {
		t.Errorf("TPEx foreign-total buy should be provided, got %v", c.ForeignTotal.Buy)
	}
	if c.Trust.Net != 18100 {
		t.Errorf("trust net = %d", c.Trust.Net)
	}
	if c.DealerSelf.Net != -108890 || c.DealerHedge.Net != -20796 {
		t.Errorf("dealer split wrong: self=%d hedge=%d", c.DealerSelf.Net, c.DealerHedge.Net)
	}
	if c.DealerTotal.Net != -129686 {
		t.Errorf("dealer-total net = %d", c.DealerTotal.Net)
	}
	if c.Nets().Total != 176295 {
		t.Errorf("computed total = %d, want 176295", c.Nets().Total)
	}
	if c.OfficialTotalNet == nil || *c.OfficialTotalNet != 176295 {
		t.Errorf("official = %v", c.OfficialTotalNet)
	}
	if !c.Reconciled {
		t.Errorf("verified row must reconcile (also validates positional mapping)")
	}
}

func TestParseTPEX_Holiday(t *testing.T) {
	// stat "ok" but empty data (§4) → ErrNoData.
	body := `{"stat":"ok","tables":[{"date":"","totalCount":0,"fields":[],"data":[]}]}`
	_, err := ParseTPEX([]byte(body), "2026-07-12", tpexDailyTradeURL, time.Now())
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("TPEx holiday must map to ErrNoData, got %v", err)
	}
}

// TestParseTPEX_MappingGuard proves reconciliation catches a positional mis-map: if the
// dealer-total column drifted, self+hedge != total and Reconciled must be false.
func TestParseTPEX_MappingGuard(t *testing.T) {
	c := StockChip{
		ForeignTotal: Leg{Net: 100}, ForeignExclDealer: Leg{Net: 100}, ForeignDealer: Leg{Net: 0},
		Trust:      Leg{Net: 10},
		DealerSelf: Leg{Net: -5}, DealerHedge: Leg{Net: -3},
		DealerTotal:      Leg{Net: 999}, // wrong (should be -8)
		OfficialTotalNet: ptr(105),
	}
	if c.reconciled() {
		t.Fatalf("mis-mapped dealer total must fail reconciliation")
	}
}
