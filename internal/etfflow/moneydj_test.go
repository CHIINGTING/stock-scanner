package etfflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// loadFixture reads a small offline HTML fixture. Tests NEVER hit the network.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// (1)(3)(4)(5)(6) end-to-end parse of the holdings-detail table.
func TestMoneyDJParseHoldings(t *testing.T) {
	asOf, holdings, err := MoneyDJ{}.Parse(loadFixture(t, "moneydj_00981A_holdings.html"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if asOf != "2026-06-05" { // (1) holdings-detail date
		t.Fatalf("as_of_date: got %q want 2026-06-05", asOf)
	}
	if len(holdings) != 3 { // 合計 (total) row excluded
		t.Fatalf("holdings: got %d want 3 (%+v)", len(holdings), holdings)
	}
	h := holdings[0]
	if h.StockCode != "2330" { // (3) code
		t.Errorf("code: got %q want 2330", h.StockCode)
	}
	if h.StockName != "台積電" { // (4) name
		t.Errorf("name: got %q want 台積電", h.StockName)
	}
	if h.Quantity != 11960000 { // (5) shares
		t.Errorf("shares: got %v want 11960000", h.Quantity)
	}
	if h.Weight != 9.75 { // (6) weight
		t.Errorf("weight: got %v want 9.75", h.Weight)
	}
	if h.RawUnit != unitShare {
		t.Errorf("unit: got %q want %q", h.RawUnit, unitShare)
	}
}

// (2) the parser must NOT pick the industry-breakdown date even though it appears first.
func TestMoneyDJIgnoresIndustryDate(t *testing.T) {
	body := `
<div>產業分布 <span>資料日期：2026/03/31</span></div>
<div>持股明細 <span>資料日期：2026/06/05</span></div>
<table>
  <tr><th>個股</th><th>權重</th><th>股數</th></tr>
  <tr><td>2330 台積電</td><td>9.75%</td><td>11,960,000</td></tr>
</table>`
	asOf, _, err := MoneyDJ{}.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if asOf == "2026-03-31" {
		t.Fatal("parser picked the 產業分布 date — must use 持股明細 date")
	}
	if asOf != "2026-06-05" {
		t.Fatalf("as_of_date: got %q want 2026-06-05", asOf)
	}
}

// (7) a holdings row with a code but no share count must error, not yield a partial snapshot.
func TestMoneyDJMissingSharesErrors(t *testing.T) {
	body := `
<div>持股明細 資料日期：2026/06/05</div>
<table>
  <tr><th>個股</th><th>持股權重(%)</th></tr>
  <tr><td>2330 台積電</td><td>9.75%</td></tr>
</table>`
	if _, _, err := (MoneyDJ{}).Parse(body); err == nil {
		t.Fatal("expected error when 持有股數 is missing")
	}
}

// detectHoldingsUnit switches to 張 when the header says so, so Ingest scales ×1000.
func TestMoneyDJLotUnit(t *testing.T) {
	body := `
<div>持股明細 資料日期：2026/06/05</div>
<table>
  <tr><th>個股</th><th>持股權重(%)</th><th>持有張數(張)</th></tr>
  <tr><td>2330 台積電</td><td>9.75%</td><td>11,960</td></tr>
</table>`
	asOf, holdings, err := MoneyDJ{}.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if asOf != "2026-06-05" || len(holdings) != 1 {
		t.Fatalf("parse: asOf=%q n=%d", asOf, len(holdings))
	}
	if holdings[0].RawUnit != unitLot {
		t.Fatalf("unit: got %q want %q", holdings[0].RawUnit, unitLot)
	}
	// Confirm normalisation through Ingest: 11,960 張 → 11,960,000 股.
	snap, err := Ingest(rawSliceSource(holdings), SnapshotMeta{
		ETFCode: "00981A", ETFName: "x", ETFType: TypeActive, AsOfDate: asOf,
	}, time.Now())
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if snap.Holdings[0].HoldingShares != 11960000 {
		t.Errorf("normalised shares: got %d want 11960000", snap.Holdings[0].HoldingShares)
	}
}

// fixturePager returns a Pager that serves body for any URL, with no network access.
func fixturePager(body string) Pager {
	return func(string) (string, error) { return body, nil }
}

func meta00981A(asOf string) SnapshotMeta {
	return SnapshotMeta{ETFCode: "00981A", ETFName: "主動統一台股增長", ETFType: TypeActive, AsOfDate: asOf}
}

// fixedNow gives deterministic data_lag_days / fetched_at in tests.
func fixedNow(date string) func() time.Time {
	return func() time.Time {
		t, _ := time.Parse(dateLayout, date)
		return t
	}
}

// fakeFullParser reuses MoneyDJ's HTML parsing but DECLARES full_holdings coverage,
// so the full-source policy paths (OK / STALE) can be exercised against the existing
// fixture. MoneyDJ itself is top_n_only and exercises the PARTIAL paths below.
type fakeFullParser struct{ MoneyDJ }

func (fakeFullParser) Coverage() Coverage { return Coverage{Type: CoverageFullHoldings} }

// (8) full source, require-date NEWER than disclosed → STALE; without --allow-stale nothing written.
func TestFetcherFullStaleNoWrite(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: fakeFullParser{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "2026-06-10", false, false)
	if res.Status != StatusStale {
		t.Fatalf("status: got %s want STALE (%+v)", res.Status, res)
	}
	if res.AsOfDate != "2026-06-05" {
		t.Errorf("as_of_date: got %q want 2026-06-05", res.AsOfDate)
	}
	if res.OutputPath != "" {
		t.Errorf("STALE without --allow-stale must not write, got %q", res.OutputPath)
	}
	if got := mustReadDir(t, dir); len(got) != 0 {
		t.Errorf("dir should be empty, has %v", got)
	}
}

// --allow-stale lets a full-source STALE result be written anyway.
func TestFetcherFullStaleAllowWrite(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: fakeFullParser{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "2026-06-10", true, false)
	if res.Status != StatusStale {
		t.Fatalf("status: got %s want STALE", res.Status)
	}
	if res.OutputPath == "" {
		t.Fatal("--allow-stale should still write the snapshot")
	}
}

// full source, require-date == disclosed date → OK, snapshot written.
func TestFetcherFullRequireMatchOK(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: fakeFullParser{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "2026-06-05", false, false)
	if res.Status != StatusOK {
		t.Fatalf("status: got %s want OK (%+v)", res.Status, res)
	}
	if res.DataLagDays != 7 { // 2026-06-12 − 2026-06-05
		t.Errorf("data_lag_days: got %d want 7", res.DataLagDays)
	}
}

// (9) a full-coverage snapshot is readable back via LoadHistory (flow-eligible).
func TestFetcherFullSnapshotLoadsBack(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: fakeFullParser{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "", false, false) // latest
	if res.Status != StatusOK || res.OutputPath == "" {
		t.Fatalf("fetch latest: %+v", res)
	}
	hist, err := LoadHistory(dir, "00981A", 10)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("LoadHistory: got %d snapshots want 1", len(hist))
	}
	got := hist[0]
	if got.AsOfDate != "2026-06-05" || got.ETFType != TypeActive || len(got.Holdings) != 3 {
		t.Errorf("loaded snapshot mismatch: %+v", got)
	}
	if got.Holdings[0].StockCode != "2330" || got.Holdings[0].HoldingShares != 11960000 {
		t.Errorf("loaded holding[0] mismatch: %+v", got.Holdings[0])
	}
}

// R8-6 guardrail: MoneyDJ is top_n_only. By default Fetch returns PARTIAL, writes
// nothing, and the (built) snapshot is tagged + NOT flow-eligible.
func TestFetcherMoneyDJPartialNoWriteByDefault(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: MoneyDJ{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "", false, false)
	if res.Status != StatusPartial {
		t.Fatalf("status: got %s want PARTIAL (%+v)", res.Status, res)
	}
	if res.Coverage.Type != CoverageTopNOnly || res.Coverage.TopN != 10 {
		t.Errorf("coverage: got %+v want top_n_only/10", res.Coverage)
	}
	if res.OutputPath != "" {
		t.Errorf("PARTIAL without --allow-partial must not write, got %q", res.OutputPath)
	}
	if got := mustReadDir(t, dir); len(got) != 0 {
		t.Errorf("dir should be empty, has %v", got)
	}
	if res.Snapshot == nil || res.Snapshot.IsFlowEligible() {
		t.Errorf("built snapshot must be tagged non-flow-eligible, got %+v", res.Snapshot)
	}
}

// --allow-partial writes a TAGGED probe (coverage_type=top_n_only) that LoadHistory
// STILL skips — saving the probe never makes it flow-eligible.
func TestFetcherMoneyDJAllowPartialWritesProbeButLoadHistorySkips(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: MoneyDJ{},
		Get:    fixturePager(loadFixture(t, "moneydj_00981A_holdings.html")),
		Now:    fixedNow("2026-06-12"),
	}
	res := f.Fetch(dir, meta00981A(""), "", false, true)
	if res.Status != StatusPartial {
		t.Fatalf("status: got %s want PARTIAL", res.Status)
	}
	if res.OutputPath == "" {
		t.Fatal("--allow-partial should write the probe snapshot")
	}
	// On-disk snapshot is tagged top_n_only and is not flow-eligible.
	snap, err := Load(dir, "00981A", "2026-06-05")
	if err != nil {
		t.Fatalf("Load probe: %v", err)
	}
	if snap.CoverageType != CoverageTopNOnly || snap.TopN != 10 {
		t.Errorf("probe coverage: got %q/%d want top_n_only/10", snap.CoverageType, snap.TopN)
	}
	if snap.IsFlowEligible() {
		t.Error("top_n_only probe must NOT be flow-eligible")
	}
	// LoadHistory must skip the probe → no flow input.
	hist, err := LoadHistory(dir, "00981A", 10)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("LoadHistory must skip top_n_only probe, got %d", len(hist))
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}
