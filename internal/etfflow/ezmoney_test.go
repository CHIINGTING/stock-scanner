package etfflow

import (
	"testing"
	"time"
)

const ezFixture = "ezmoney_test_holdings.html"

// Parse pulls the full stock holdings + as-of date from the issuer page, reading ONLY
// the 股票 (ST) category — futures (GD), cash and the NAV summary rows are excluded.
func TestEzMoneyParse(t *testing.T) {
	asOf, holdings, err := EzMoney{FundCode: "TEST1"}.Parse(loadFixture(t, ezFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if asOf != "2026-06-23" { // from TranDate "2026-06-23T00:00:00"
		t.Fatalf("as_of_date: got %q want 2026-06-23", asOf)
	}
	if len(holdings) != 3 { // NAV/GD/CASH categories excluded, 3 stocks kept
		t.Fatalf("holdings: got %d want 3 (%+v)", len(holdings), holdings)
	}
	h := holdings[0]
	if h.StockCode != "2330" {
		t.Errorf("code: got %q want 2330", h.StockCode)
	}
	if h.StockName != "台積電" {
		t.Errorf("name: got %q want 台積電", h.StockName)
	}
	if h.Quantity != 11960000 {
		t.Errorf("shares: got %v want 11960000", h.Quantity)
	}
	if h.Weight != 10.13 {
		t.Errorf("weight: got %v want 10.13", h.Weight)
	}
	if h.RawUnit != unitShare {
		t.Errorf("unit: got %q want %q", h.RawUnit, unitShare)
	}
	// A 0%-weight newly-added position must be KEPT, not dropped — its share count is
	// real and R8 flow needs it to detect the entry.
	last := holdings[2]
	if last.StockCode != "3999" || last.Weight != 0.0 || last.Quantity != 1000 {
		t.Errorf("zero-weight holding mishandled: %+v", last)
	}
}

// Coverage must be full_holdings so the snapshot is flow-eligible — this is the whole
// point of R8-6c (MoneyDJ is top_n_only, WantGoo is quarterly_composition).
func TestEzMoneyCoverageIsFull(t *testing.T) {
	cov := EzMoney{}.Coverage()
	if cov.Type != CoverageFullHoldings {
		t.Fatalf("coverage type: got %q want %q", cov.Type, CoverageFullHoldings)
	}
	if !cov.IsFull() {
		t.Fatal("ezMoney coverage must be full (flow-eligible)")
	}
}

// URL addresses the fund by its issuer fundCode, not the listed ETF code.
func TestEzMoneyURLUsesFundCode(t *testing.T) {
	got := EzMoney{FundCode: "49YTW"}.URL("00981A")
	want := "https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=49YTW"
	if got != want {
		t.Fatalf("URL: got %q want %q", got, want)
	}
}

// Absent / malformed holdings blob must error, never yield a partial snapshot.
func TestEzMoneyParseErrors(t *testing.T) {
	cases := map[string]string{
		"no DataAsset blob": `<html><body>nothing here</body></html>`,
		"empty ST category": `<div id="DataAsset" data-content="[{&quot;AssetCode&quot;:&quot;ST&quot;,&quot;Details&quot;:[]}]"></div>`,
		"no ST category":    `<div id="DataAsset" data-content="[{&quot;AssetCode&quot;:&quot;CASH&quot;,&quot;Details&quot;:null}]"></div>`,
		"bad TranDate":      `<div id="DataAsset" data-content="[{&quot;AssetCode&quot;:&quot;ST&quot;,&quot;Details&quot;:[{&quot;DetailCode&quot;:&quot;2330&quot;,&quot;TranDate&quot;:&quot;nope&quot;,&quot;Share&quot;:1.0}]}]"></div>`,
	}
	for name, body := range cases {
		if _, _, err := (EzMoney{}).Parse(body); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// End-to-end through the Fetcher (offline fixture): a full-holdings source yields
// StatusOK, a flow-eligible snapshot, and survives the LoadHistory chokepoint that
// feeds R8 flow — proving the adapter actually reaches CalculateFlows.
func TestEzMoneyFetchIsFlowEligible(t *testing.T) {
	now := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	f := Fetcher{
		Parser: EzMoney{FundCode: "TEST1"},
		Get:    fixturePager(loadFixture(t, ezFixture)),
		Now:    func() time.Time { return now },
	}
	dir := t.TempDir()
	res := f.Fetch(dir, SnapshotMeta{
		ETFCode: "00981A",
		ETFName: "主動統一台股增長",
		ETFType: TypeActive,
	}, "", false, false)

	if res.Status != StatusOK {
		t.Fatalf("status: got %v want OK (err=%v)", res.Status, res.Err)
	}
	if res.AsOfDate != "2026-06-23" {
		t.Fatalf("as_of: got %q want 2026-06-23", res.AsOfDate)
	}
	if res.Snapshot == nil || !res.Snapshot.IsFlowEligible() {
		t.Fatalf("snapshot must be flow-eligible: %+v", res.Snapshot)
	}
	if res.Snapshot.CoverageType != CoverageFullHoldings {
		t.Fatalf("coverage stamped: got %q want %q", res.Snapshot.CoverageType, CoverageFullHoldings)
	}

	// The single chokepoint into R8 flow must return this snapshot (partial sources
	// would be skipped here).
	snaps, err := LoadHistory(dir, "00981A", 0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("LoadHistory: got %d flow-eligible snapshots want 1", len(snaps))
	}
	if got := snaps[0].Holdings[0].HoldingShares; got != 11960000 {
		t.Fatalf("normalised shares: got %d want 11960000", got)
	}
}
