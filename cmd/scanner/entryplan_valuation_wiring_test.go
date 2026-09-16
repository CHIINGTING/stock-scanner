package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func TestEntryPlanValuationRunsAfterPITResearchLoad(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	load := strings.Index(s, "fundViews, valViews := loadResearchViews")
	attach := strings.Index(s, "attachEntryPlanValuation(watchlistResults")
	if load < 0 || attach < 0 || attach < load {
		t.Fatalf("valuation wiring must consume the PIT research views after they load: load=%d attach=%d", load, attach)
	}
}

func TestEntryPlanValuationRejectsEntryBeforeResearchLoadAsOf(t *testing.T) {
	entryDate := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	loadedAsOf := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	targetPE, median, p25, p75 := 10.0, 20.0, 15.0, 25.0
	v := &valuation.Valuation{Status: valuation.Available, TrailingPE: &targetPE,
		TrailingDate: "2026-09-09", HistoricalPE: valuation.HistoricalPEStats{
			Status: valuation.Available, SampleCount: 60, Median: &median, P25: &p25, P75: &p75}}
	entries := []scanner.WatchlistEntry{{A: scanner.StockAnalysis{
		Symbol: "2330", Date: entryDate, Close: 100,
	}}}
	bars := map[string][]fetcher.Candle{"2330": {{Date: entryDate, Close: 100}}}

	got := entryPlanValuationEvidence(entries, bars, loadedAsOf, nil,
		map[string]*valuation.Valuation{"2330": v})["2330"]
	if got.Status != string(valuation.Unavailable) || got.BaseTarget != nil || got.AsOf != "2026-09-09" {
		t.Fatalf("later-loaded research could cap the older plan: %+v", got)
	}
}
