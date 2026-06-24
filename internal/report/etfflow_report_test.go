package report

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// entryWithETFFlow builds a watchlist entry carrying a real, classified ETF flow
// signal (EXTREME active sell + passive sell), so report ⑨ rendering is exercised on
// genuine generated prose. genHTML's report date is 2026-06-05 → as-of 2026-06-03 is
// a 2-day lag.
func entryWithETFFlow() scanner.WatchlistEntry {
	e := sampleEntry(false)
	e.A.Symbol = "1111"
	e.A.AvgVolume20 = 1_000_000
	flow := &etfflow.StockFlow{
		StockCode: "1111",
		Legs: []etfflow.StockETFLeg{
			{ETFCode: "00981A", ETFType: etfflow.TypeActive, AsOfDate: "2026-06-03", DeltaShares: -1_500_000, ConsecutiveSellDays: 5},
			{ETFCode: "00919", ETFType: etfflow.TypePassive, AsOfDate: "2026-06-04", DeltaShares: -300_000, ConsecutiveSellDays: 4},
		},
		TotalActiveDeltaShares:  -1_500_000,
		TotalPassiveDeltaShares: -300_000,
		TotalDeltaShares:        -1_800_000,
	}
	sig := etfflow.Classify(etfflow.ClassifyInput{
		StockCode:   "1111",
		Flow:        flow,
		AvgVolume20: 1_000_000,
		ReportDate:  time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		AsOfDate:    "2026-06-03", // oldest leg (conservative lag)
	})
	e.ETFFlow = &sig
	return e
}

// show_etf_flow=false → no ⑨ section at all.
func TestR84ETFFlowHiddenByDefault(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{entryWithETFFlow()}, GuardrailViewOptions{ShowETFFlow: false})
	for _, marker := range []string{"ETF Flow / Active Rotation Context", "延遲揭露資料"} {
		if strings.Contains(html, marker) {
			t.Errorf("show_etf_flow=false must not render %q", marker)
		}
	}
}

// show_etf_flow=true → ⑨ section with the fixed disclosure line, lag, strength, and
// a Chinese signal label (never the raw enum).
func TestR84ETFFlowShown(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{entryWithETFFlow()}, GuardrailViewOptions{ShowETFFlow: true})
	want := []string{
		"⑨ ETF Flow / Active Rotation Context",
		"此為延遲揭露資料，不代表盤中即時買賣。",
		"延遲 2 個交易日",
		"EXTREME",
		"主動式 ETF 賣壓", // etfSignalLabel, not the raw ACTIVE_ETF_SELL_PRESSURE enum
	}
	for _, m := range want {
		if !strings.Contains(html, m) {
			t.Errorf("show_etf_flow=true should render %q", m)
		}
	}
	// The raw enum (with the SELL identifier) must never reach the HTML.
	if strings.Contains(html, "ACTIVE_ETF_SELL_PRESSURE") {
		t.Error("⑨ must not render the raw signal enum value")
	}
}

// Computed=false → no ⑨ even when show_etf_flow=true.
func TestR84ETFFlowNotComputed(t *testing.T) {
	e := sampleEntry(false)
	e.ETFFlow = &etfflow.StockFlowSignal{Computed: false}
	html := genHTML(t, []scanner.WatchlistEntry{e}, GuardrailViewOptions{ShowETFFlow: true})
	if strings.Contains(html, "ETF Flow / Active Rotation Context") {
		t.Error("non-Computed ETFFlow must not render ⑨")
	}
}

// nil ETFFlow → no ⑨, no panic.
func TestR84ETFFlowNil(t *testing.T) {
	html := genHTML(t, []scanner.WatchlistEntry{sampleEntry(false)}, GuardrailViewOptions{ShowETFFlow: true})
	if strings.Contains(html, "ETF Flow / Active Rotation Context") {
		t.Error("nil ETFFlow must not render ⑨")
	}
}

// Turning on ⑨ must introduce NO trade-instruction token. Comparing token counts
// between show=true and show=false isolates the ⑨ block from the rest of the page
// (which legitimately contains BUY/SELL action labels elsewhere).
func TestR84NoForbiddenTokensInSection(t *testing.T) {
	forbidden := []string{
		"可以買", "可以賣", "買進", "賣出", "到價下單", "建議買", "建議賣",
		"BUY", "SELL", "PLACE_ORDER", "AUTO_BUY", "AUTO_SELL",
	}
	entries := []scanner.WatchlistEntry{entryWithETFFlow()}
	off := genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: false})
	on := genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: true})
	for _, tok := range forbidden {
		if strings.Count(on, tok) > strings.Count(off, tok) {
			t.Errorf("⑨ section introduced forbidden token %q", tok)
		}
	}
}

// Display-only: toggling show_etf_flow must not mutate any scoring field on entries.
func TestR84ETFFlowReadOnly(t *testing.T) {
	entries := []scanner.WatchlistEntry{entryWithETFFlow()}
	score, act, prob := entries[0].RocketScore, entries[0].WatchAction, entries[0].ExplosionProb
	_ = genHTML(t, entries, GuardrailViewOptions{ShowETFFlow: true})
	if entries[0].RocketScore != score || entries[0].WatchAction != act || entries[0].ExplosionProb != prob {
		t.Error("rendering ⑨ must not mutate entry scoring fields")
	}
}
