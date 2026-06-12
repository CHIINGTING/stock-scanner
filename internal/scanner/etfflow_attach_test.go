package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

func etfWLEntry(symbol string, avgVol int64) WatchlistEntry {
	return WatchlistEntry{A: StockAnalysis{Symbol: symbol, AvgVolume20: avgVol}, RocketScore: 50}
}

func flowFor(code string, activeDelta, passiveDelta int64, asOf string, sell int) *etfflow.StockFlow {
	return &etfflow.StockFlow{
		StockCode: code,
		Legs: []etfflow.StockETFLeg{
			{ETFCode: "00981A", ETFType: etfflow.TypeActive, AsOfDate: asOf, DeltaShares: activeDelta, ConsecutiveSellDays: sell},
		},
		TotalActiveDeltaShares:  activeDelta,
		TotalPassiveDeltaShares: passiveDelta,
		TotalDeltaShares:        activeDelta + passiveDelta,
	}
}

func TestAttachETFFlowPopulatesMatch(t *testing.T) {
	entries := []WatchlistEntry{etfWLEntry("2330", 1_000_000), etfWLEntry("9999", 1_000_000)}
	flows := map[string]*etfflow.StockFlow{
		"2330": flowFor("2330", -400_000, 0, "2026-06-10", 4),
	}
	report := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)

	AttachETFFlow(entries, flows, report, etfflow.StrengthThresholds{})

	if entries[0].ETFFlow == nil {
		t.Fatal("expected ETFFlow on matched entry")
	}
	if !entries[0].ETFFlow.Computed || entries[0].ETFFlow.Signal != etfflow.ActiveSellPressure {
		t.Errorf("classified signal wrong: %+v", entries[0].ETFFlow)
	}
	if entries[0].ETFFlow.DataLagDays != 2 {
		t.Errorf("data lag: got %d want 2", entries[0].ETFFlow.DataLagDays)
	}
	if entries[1].ETFFlow != nil { // 9999 has no flow → stays nil
		t.Error("unmatched entry must keep ETFFlow nil")
	}
}

func TestAttachETFFlowConservativeAsOf(t *testing.T) {
	// Two legs with different as-of dates → the OLDEST (largest lag) is used.
	flow := &etfflow.StockFlow{
		StockCode: "2330",
		Legs: []etfflow.StockETFLeg{
			{ETFCode: "00981A", ETFType: etfflow.TypeActive, AsOfDate: "2026-06-11", DeltaShares: -400_000, ConsecutiveSellDays: 4},
			{ETFCode: "00919", ETFType: etfflow.TypePassive, AsOfDate: "2026-06-09", DeltaShares: -100_000, ConsecutiveSellDays: 1},
		},
		TotalActiveDeltaShares:  -400_000,
		TotalPassiveDeltaShares: -100_000,
		TotalDeltaShares:        -500_000,
	}
	entries := []WatchlistEntry{etfWLEntry("2330", 1_000_000)}
	AttachETFFlow(entries, map[string]*etfflow.StockFlow{"2330": flow}, time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC), etfflow.StrengthThresholds{})

	if entries[0].ETFFlow.AsOfDate != "2026-06-09" {
		t.Errorf("as-of: got %q want oldest 2026-06-09", entries[0].ETFFlow.AsOfDate)
	}
	if entries[0].ETFFlow.DataLagDays != 3 { // 2026-06-12 − 2026-06-09
		t.Errorf("lag: got %d want 3", entries[0].ETFFlow.DataLagDays)
	}
}

func TestAttachETFFlowZeroAvgVolumeNoPanic(t *testing.T) {
	entries := []WatchlistEntry{etfWLEntry("2330", 0)} // avg vol unavailable
	flows := map[string]*etfflow.StockFlow{"2330": flowFor("2330", -400_000, 0, "2026-06-10", 4)}
	AttachETFFlow(entries, flows, time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC), etfflow.StrengthThresholds{})
	if entries[0].ETFFlow == nil || entries[0].ETFFlow.ActivePressureRatio != 0 {
		t.Errorf("zero avg vol should yield 0 ratio, got %+v", entries[0].ETFFlow)
	}
}

func TestAttachETFFlowEmptyFlowsNoop(t *testing.T) {
	entries := []WatchlistEntry{etfWLEntry("2330", 1_000_000)}
	AttachETFFlow(entries, nil, time.Now(), etfflow.StrengthThresholds{})
	if entries[0].ETFFlow != nil {
		t.Error("empty flow table must attach nothing")
	}
}

func TestAttachETFFlowDoesNotChangeScore(t *testing.T) {
	entries := []WatchlistEntry{etfWLEntry("2330", 1_000_000)}
	before := entries[0].RocketScore
	flows := map[string]*etfflow.StockFlow{"2330": flowFor("2330", -400_000, 0, "2026-06-10", 4)}
	AttachETFFlow(entries, flows, time.Now(), etfflow.StrengthThresholds{})
	if entries[0].RocketScore != before {
		t.Error("AttachETFFlow must not change RocketScore")
	}
}
