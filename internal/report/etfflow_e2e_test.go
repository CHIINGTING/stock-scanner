package report

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// saveETFSnapshot writes one synthetic ETF snapshot (single stock 2330) to dir. All
// data lives in t.TempDir() — no real CSV/JSON enters the repo.
func saveETFSnapshot(t *testing.T, dir, etf, etfType, asOf string, shares int64) {
	t.Helper()
	s := &etfflow.Snapshot{
		ETFCode:  etf,
		ETFName:  etf + " 測試",
		ETFType:  etfType,
		AsOfDate: asOf,
		Holdings: []etfflow.Holding{
			{StockCode: "2330", StockName: "台積電", HoldingShares: shares, HoldingRawUnit: "股", HoldingWeight: 10},
		},
		FetchedAt: time.Now(),
	}
	if err := etfflow.Save(dir, s); err != nil {
		t.Fatalf("Save %s %s: %v", etf, asOf, err)
	}
}

// buildETFFlowEntry runs the FULL R8 pipeline against synthetic on-disk snapshots and
// returns the resulting watchlist entry (with ETFFlow attached) plus the flow table,
// so individual assertions can inspect each stage.
func buildETFFlowEntry(t *testing.T) (scanner.WatchlistEntry, map[string]*etfflow.StockFlow) {
	t.Helper()
	dir := t.TempDir()

	// 00981A active: consecutive sell-down (3 sell days over 4 snapshots), latest drop
	// large → high pressure ratio against a 1M avg volume.
	for _, s := range []struct {
		date   string
		shares int64
	}{{"2026-06-01", 2_000_000}, {"2026-06-02", 1_700_000}, {"2026-06-03", 1_400_000}, {"2026-06-04", 200_000}} {
		saveETFSnapshot(t, dir, "00981A", etfflow.TypeActive, s.date, s.shares)
	}
	// 00919 passive: milder consecutive sell-down.
	for _, s := range []struct {
		date   string
		shares int64
	}{{"2026-06-01", 5_000_000}, {"2026-06-02", 4_800_000}, {"2026-06-03", 4_600_000}, {"2026-06-04", 4_500_000}} {
		saveETFSnapshot(t, dir, "00919", etfflow.TypePassive, s.date, s.shares)
	}

	// LoadHistory → CalculateFlows.
	activeHist, err := etfflow.LoadHistory(dir, "00981A", 10)
	if err != nil || len(activeHist) != 4 {
		t.Fatalf("LoadHistory active: n=%d err=%v", len(activeHist), err)
	}
	passiveHist, err := etfflow.LoadHistory(dir, "00919", 10)
	if err != nil || len(passiveHist) != 4 {
		t.Fatalf("LoadHistory passive: n=%d err=%v", len(passiveHist), err)
	}
	flows := etfflow.CalculateFlows([]etfflow.History{
		{ETFCode: "00981A", ETFType: etfflow.TypeActive, Snapshots: activeHist},
		{ETFCode: "00919", ETFType: etfflow.TypePassive, Snapshots: passiveHist},
	})

	// AttachETFFlow onto a watchlist entry for 2330 (1M avg volume).
	e := sampleEntry(false)
	e.A.Symbol = "2330"
	e.A.AvgVolume20 = 1_000_000
	entries := []scanner.WatchlistEntry{e}
	reportDate := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC) // as-of 2026-06-04 → lag 2
	scanner.AttachETFFlow(entries, flows, reportDate, etfflow.StrengthThresholds{})
	return entries[0], flows
}

// Full pipeline: snapshots → flows → attach. Verifies the data-layer stages line up.
func TestE2EPipelineAttaches(t *testing.T) {
	entry, flows := buildETFFlowEntry(t)

	sf := flows["2330"]
	if sf == nil {
		t.Fatal("CalculateFlows produced no flow for 2330")
	}
	if sf.TotalActiveDeltaShares != -1_200_000 { // 200,000 − 1,400,000
		t.Errorf("active delta: got %d want -1200000", sf.TotalActiveDeltaShares)
	}
	if sf.TotalPassiveDeltaShares != -100_000 { // 4,500,000 − 4,600,000
		t.Errorf("passive delta: got %d want -100000", sf.TotalPassiveDeltaShares)
	}
	if entry.ETFFlow == nil || !entry.ETFFlow.Computed {
		t.Fatalf("AttachETFFlow did not populate a computed signal: %+v", entry.ETFFlow)
	}
	if entry.ETFFlow.Signal != etfflow.ActiveSellPressure || entry.ETFFlow.Strength != etfflow.StrengthHigh {
		t.Errorf("classified: got %s/%s want ACTIVE_ETF_SELL_PRESSURE/HIGH", entry.ETFFlow.Signal, entry.ETFFlow.Strength)
	}
	if entry.ETFFlow.DataLagDays != 2 {
		t.Errorf("data lag: got %d want 2", entry.ETFFlow.DataLagDays)
	}
}

// report ⑨ renders end-to-end when show_etf_flow=true.
func TestE2EReportShowsSection(t *testing.T) {
	entry, _ := buildETFFlowEntry(t)
	html := genHTML(t, []scanner.WatchlistEntry{entry}, GuardrailViewOptions{ShowETFFlow: true})

	for _, m := range []string{
		"⑨ ETF Flow / Active Rotation Context",
		"此為延遲揭露資料，不代表盤中即時買賣。",
		"延遲 2 個交易日",
		"HIGH",
		"主動式 ETF 賣壓", // etfSignalLabel, not the raw enum
	} {
		if !strings.Contains(html, m) {
			t.Errorf("show=true should render %q", m)
		}
	}
	// Raw enum (carrying the SELL identifier) must never reach the HTML.
	if strings.Contains(html, "ACTIVE_ETF_SELL_PRESSURE") {
		t.Error("⑨ must not render the raw signal enum")
	}
}

// show_etf_flow=false → no ⑨, end-to-end.
func TestE2EReportHiddenWhenFalse(t *testing.T) {
	entry, _ := buildETFFlowEntry(t)
	html := genHTML(t, []scanner.WatchlistEntry{entry}, GuardrailViewOptions{ShowETFFlow: false})
	for _, m := range []string{"ETF Flow / Active Rotation Context", "延遲揭露資料"} {
		if strings.Contains(html, m) {
			t.Errorf("show=false must not render %q", m)
		}
	}
}

// Enabling ⑨ introduces no trade-instruction token (count-diff isolates the section
// from the rest of the page, which legitimately uses BUY/SELL action labels).
func TestE2ENoForbiddenTokens(t *testing.T) {
	entry, _ := buildETFFlowEntry(t)
	forbidden := []string{
		"可以買", "可以賣", "買進", "賣出", "到價下單", "建議買", "建議賣",
		"BUY", "SELL", "PLACE_ORDER", "AUTO_BUY", "AUTO_SELL",
	}
	entries := []scanner.WatchlistEntry{entry}
	off := genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: false})
	on := genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: true})
	for _, tok := range forbidden {
		if strings.Count(on, tok) > strings.Count(off, tok) {
			t.Errorf("⑨ introduced forbidden token %q", tok)
		}
	}
}

// Display-only end-to-end: attaching + rendering ⑨ changes no scoring field.
func TestE2EScoringUnchanged(t *testing.T) {
	entry, _ := buildETFFlowEntry(t)
	entries := []scanner.WatchlistEntry{entry}
	score, act, prob := entries[0].RocketScore, entries[0].WatchAction, entries[0].ExplosionProb
	_ = genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: true})
	if entries[0].RocketScore != score || entries[0].WatchAction != act || entries[0].ExplosionProb != prob {
		t.Error("e2e render must not mutate RocketScore / WatchAction / ExplosionProb")
	}
}
