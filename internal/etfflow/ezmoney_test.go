package etfflow

import (
	"html"
	"strconv"
	"strings"
	"testing"
	"time"
)

const ezFixture = "ezmoney_test_holdings.html"

// ftoa formats a float for inline JSON test fixtures.
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// htmlEscapeAttr escapes JSON into an HTML attribute value, mirroring how the live
// ezMoney page renders its data-content blob (inner quotes become entities).
func htmlEscapeAttr(s string) string { return html.EscapeString(s) }

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
	if h.Amount != 29780400000 {
		t.Errorf("amount: got %v want 29780400000", h.Amount)
	}
	// A 0%-weight newly-added position must be KEPT, not dropped — its share count is
	// real and R8 flow needs it to detect the entry.
	last := holdings[2]
	if last.StockCode != "3999" || last.Weight != 0.0 || last.Quantity != 1000 {
		t.Errorf("zero-weight holding mishandled: %+v", last)
	}
}

// ParseValidated additionally reconciles sum(Amount) against the ST category Value and
// returns the audit record. The fixture is built so they match exactly.
func TestEzMoneyParseValidated(t *testing.T) {
	_, holdings, v, err := EzMoney{FundCode: "TEST1"}.ParseValidated(loadFixture(t, ezFixture))
	if err != nil {
		t.Fatalf("ParseValidated: %v", err)
	}
	if v == nil {
		t.Fatal("validation must be non-nil for a full-holdings source")
	}
	if v.StockCategory != "ST" || v.HoldingCount != len(holdings) || !v.AmountMatched {
		t.Fatalf("validation: %+v", v)
	}
	if v.SumAmount != v.CategoryValue {
		t.Errorf("sum(%.0f) should equal category value(%.0f)", v.SumAmount, v.CategoryValue)
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

// Absent / malformed holdings blob must error, never yield a partial snapshot. Each case
// isolates one guardrail; ezAsset builds a minimal DataAsset div from inline ST details.
func TestEzMoneyParseErrors(t *testing.T) {
	// one well-formed ST detail (code/name/share/amount/date), used as a baseline.
	good := `{"DetailCode":"2330","DetailName":"台積電","Share":1000.0,"Amount":1000.0,"TranDate":"2026-06-23T00:00:00","NavRate":1.0}`

	cases := map[string]string{
		"no DataAsset blob": `<html><body>nothing here</body></html>`,
		"no ST category":    ezAssetDiv(`{"AssetCode":"CASH","Value":1,"Details":null}`),
		"empty ST details":  ezAssetDiv(`{"AssetCode":"ST","Value":1000,"Details":[]}`),
		"missing code":      ezAssetDiv(stCat(1000, `{"DetailName":"x","Share":1.0,"Amount":1000.0,"TranDate":"2026-06-23T00:00:00"}`)),
		"missing name":      ezAssetDiv(stCat(1000, `{"DetailCode":"2330","Share":1.0,"Amount":1000.0,"TranDate":"2026-06-23T00:00:00"}`)),
		"missing Share":     ezAssetDiv(stCat(1000, `{"DetailCode":"2330","DetailName":"x","Amount":1000.0,"TranDate":"2026-06-23T00:00:00"}`)),
		"missing Amount":    ezAssetDiv(stCat(1000, `{"DetailCode":"2330","DetailName":"x","Share":1.0,"TranDate":"2026-06-23T00:00:00"}`)),
		"bad TranDate":      ezAssetDiv(stCat(1000, `{"DetailCode":"2330","DetailName":"x","Share":1.0,"Amount":1000.0,"TranDate":"nope"}`)),
		// sum(Amount)=1000 but the category claims 5000 → truncation-style mismatch.
		"amount mismatch": ezAssetDiv(stCat(5000, good)),
		// category Value missing / non-positive cannot be reconciled.
		"no category value": ezAssetDiv(stCat(0, good)),
	}
	for name, body := range cases {
		if _, _, err := (EzMoney{}).Parse(body); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// stCat wraps ST details into an ST asset category with the given declared Value.
func stCat(value float64, details ...string) string {
	return `{"AssetCode":"ST","Value":` + ftoa(value) + `,"Details":[` + strings.Join(details, ",") + `]}`
}

// ezAssetDiv HTML-escapes a DataAsset array of category JSON objects into a div, exactly
// as the live ezMoney page renders it.
func ezAssetDiv(categories ...string) string {
	blob := `[` + strings.Join(categories, ",") + `]`
	return `<div id="DataAsset" data-content="` + htmlEscapeAttr(blob) + `"></div>`
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
		ETFCode:  "00981A",
		ETFName:  "主動統一台股增長",
		ETFType:  TypeActive,
		Source:   ProviderEzMoney,
		FundCode: "49YTW",
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
	if res.Snapshot.Validation == nil || !res.Snapshot.Validation.AmountMatched {
		t.Fatalf("snapshot must carry a matched validation record: %+v", res.Snapshot.Validation)
	}
	if res.Snapshot.Source != "" && res.Snapshot.Source != ProviderEzMoney {
		t.Fatalf("unexpected source stamp: %q", res.Snapshot.Source)
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
	// Schema round-trips through Save→Load JSON: shares normalised, amount preserved,
	// provenance + validation persisted.
	got := snaps[0]
	if got.Holdings[0].HoldingShares != 11960000 {
		t.Errorf("normalised shares: got %d want 11960000", got.Holdings[0].HoldingShares)
	}
	if got.Holdings[0].HoldingAmount != 29780400000 {
		t.Errorf("holding_amount: got %v want 29780400000", got.Holdings[0].HoldingAmount)
	}
	if got.Source != ProviderEzMoney || got.FundCode != "49YTW" {
		t.Errorf("provenance: source=%q fund_code=%q", got.Source, got.FundCode)
	}
	if got.Validation == nil || got.Validation.HoldingCount != 3 || !got.Validation.AmountMatched {
		t.Errorf("validation did not round-trip: %+v", got.Validation)
	}
}
