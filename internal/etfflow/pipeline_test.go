package etfflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// defaultPager must transparently follow the issuer's first-request session bootstrap:
// a 302-to-self that sets a cookie, then 200 once the cookie is presented. Exercised
// against a local httptest server — NO external network. This is the ezMoney __nxquid
// behaviour, reproduced minimally.
func TestDefaultPagerFollows302WithCookie(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if c, err := r.Cookie("__nxquid"); err != nil || c.Value == "" {
			http.SetCookie(w, &http.Cookie{Name: "__nxquid", Value: "sess-abc", Path: "/"})
			http.Redirect(w, r, r.URL.String(), http.StatusFound) // 302 to self
			return
		}
		fmt.Fprint(w, "OK-BODY")
	}))
	defer srv.Close()

	body, err := defaultPager(5 * time.Second)(srv.URL)
	if err != nil {
		t.Fatalf("pager: %v", err)
	}
	if body != "OK-BODY" {
		t.Fatalf("body: got %q want OK-BODY", body)
	}
	if hits < 2 {
		t.Fatalf("expected a redirect + cookie-bearing retry, got %d hit(s)", hits)
	}
}

// LoadHistory is the single chokepoint into R8 flow. It must return ONLY flow-eligible
// (full_holdings / empty-legacy) snapshots and silently skip top_n_only,
// quarterly_composition and unknown coverage, so a partial source can never fabricate a
// sell-pressure signal. This re-verifies the R8-6 guardrail at the loader level.
func TestLoadHistorySkipsNonFullCoverage(t *testing.T) {
	dir := t.TempDir()
	mk := func(date, coverage string) *Snapshot {
		return &Snapshot{
			ETFCode: "00981A", ETFType: TypeActive, AsOfDate: date,
			CoverageType: coverage,
			Holdings:     []Holding{{StockCode: "2330", StockName: "台積電", HoldingShares: 1, HoldingRawUnit: unitShare}},
		}
	}
	for _, s := range []*Snapshot{
		mk("2026-06-18", CoverageFullHoldings),
		mk("2026-06-19", CoverageTopNOnly),
		mk("2026-06-20", CoverageQuarterlyComposition),
		mk("2026-06-21", CoverageUnknown),
		mk("2026-06-22", ""), // legacy/manual full
	} {
		if err := Save(dir, s); err != nil {
			t.Fatalf("save %s: %v", s.AsOfDate, err)
		}
	}

	snaps, err := LoadHistory(dir, "00981A", 0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	got := map[string]bool{}
	for _, s := range snaps {
		got[s.AsOfDate] = true
	}
	// full + legacy-empty pass; top_n / quarterly / unknown are skipped.
	if len(snaps) != 2 || !got["2026-06-18"] || !got["2026-06-22"] {
		t.Fatalf("LoadHistory returned %d snaps %v; want only the full + legacy ones", len(snaps), got)
	}
}

// MoneyDJ must remain top_n_only (never flow-eligible), in contrast to ezMoney's
// full_holdings. Belt-and-braces against a regression flipping its coverage.
func TestMoneyDJStaysPartial(t *testing.T) {
	if (MoneyDJ{}).Coverage().IsFull() {
		t.Fatal("MoneyDJ coverage must NOT be full (top_n_only, context-only)")
	}
	if !(EzMoney{}).Coverage().IsFull() {
		t.Fatal("ezMoney coverage must be full")
	}
}

// --dry-run fetches + validates but writes nothing: StatusOK, snapshot built, but no
// file on disk and OutputPath empty.
func TestFetcherDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	f := Fetcher{
		Parser: EzMoney{FundCode: "TEST1"},
		Get:    fixturePager(loadFixture(t, ezFixture)),
		Now:    func() time.Time { return time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC) },
		DryRun: true,
	}
	res := f.Fetch(dir, SnapshotMeta{ETFCode: "00981A", ETFName: "x", ETFType: TypeActive}, "", false, false)
	if res.Status != StatusOK {
		t.Fatalf("status: got %v want OK (err=%v)", res.Status, res.Err)
	}
	if res.OutputPath != "" {
		t.Fatalf("dry-run must not report an output path, got %q", res.OutputPath)
	}
	if snaps, _ := LoadHistory(dir, "00981A", 0); len(snaps) != 0 {
		t.Fatalf("dry-run must not persist a snapshot, found %d", len(snaps))
	}
}
