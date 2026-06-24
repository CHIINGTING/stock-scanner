package etfflow

import "testing"

// snap is a terse snapshot builder for flow tests: holdings is code→shares (股),
// names/weights are not needed for most cases.
func snap(etf, etfType, asOf string, holdings map[string]int64) *Snapshot {
	hs := make([]Holding, 0, len(holdings))
	for code, sh := range holdings {
		hs = append(hs, Holding{StockCode: code, HoldingShares: sh, HoldingRawUnit: unitShare})
	}
	return &Snapshot{ETFCode: etf, ETFType: etfType, AsOfDate: asOf, Holdings: hs}
}

func legFor(sf *StockFlow, etf string) (StockETFLeg, bool) {
	for _, l := range sf.Legs {
		if l.ETFCode == etf {
			return l, true
		}
	}
	return StockETFLeg{}, false
}

func TestCalculateFlowsDelta(t *testing.T) {
	hist := History{ETFCode: "00919", ETFType: TypePassive, Snapshots: []*Snapshot{
		snap("00919", TypePassive, "2026-06-09", map[string]int64{"2330": 1_000_000}),
		snap("00919", TypePassive, "2026-06-10", map[string]int64{"2330": 800_000}),
	}}
	flows := CalculateFlows([]History{hist})

	sf := flows["2330"]
	if sf == nil {
		t.Fatal("expected flow for 2330")
	}
	leg, ok := legFor(sf, "00919")
	if !ok {
		t.Fatal("expected 00919 leg")
	}
	if leg.DeltaShares != -200_000 {
		t.Errorf("DeltaShares: got %d want -200000", leg.DeltaShares)
	}
	if leg.AsOfDate != "2026-06-10" || leg.PrevAsOfDate != "2026-06-09" {
		t.Errorf("as-of dates: got %q/%q", leg.AsOfDate, leg.PrevAsOfDate)
	}
	if sf.TotalPassiveDeltaShares != -200_000 || sf.TotalActiveDeltaShares != 0 {
		t.Errorf("aggregates: passive=%d active=%d", sf.TotalPassiveDeltaShares, sf.TotalActiveDeltaShares)
	}
}

func TestConsecutiveSellDaysAndReset(t *testing.T) {
	// 4 sell days in a row then the streak is what matters: 1000→900→800→700→600.
	sell := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("E", TypeActive, "2026-06-06", map[string]int64{"X": 1000}),
		snap("E", TypeActive, "2026-06-07", map[string]int64{"X": 900}),
		snap("E", TypeActive, "2026-06-08", map[string]int64{"X": 800}),
		snap("E", TypeActive, "2026-06-09", map[string]int64{"X": 700}),
		snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 600}),
	}}
	leg, _ := legFor(CalculateFlows([]History{sell})["X"], "E")
	if leg.ConsecutiveSellDays != 4 || leg.ConsecutiveBuyDays != 0 {
		t.Errorf("sell streak: sell=%d buy=%d want 4/0", leg.ConsecutiveSellDays, leg.ConsecutiveBuyDays)
	}

	// A buy on the newest day resets the sell streak to 0 and starts a buy streak.
	reset := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("E", TypeActive, "2026-06-08", map[string]int64{"X": 800}),
		snap("E", TypeActive, "2026-06-09", map[string]int64{"X": 700}), // sell
		snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 750}), // buy (newest)
	}}
	leg2, _ := legFor(CalculateFlows([]History{reset})["X"], "E")
	if leg2.ConsecutiveBuyDays != 1 || leg2.ConsecutiveSellDays != 0 {
		t.Errorf("reset: buy=%d sell=%d want 1/0", leg2.ConsecutiveBuyDays, leg2.ConsecutiveSellDays)
	}
}

func TestFlatNewestDayNoStreak(t *testing.T) {
	flat := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("E", TypeActive, "2026-06-09", map[string]int64{"X": 700}),
		snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 700}), // unchanged
	}}
	sf := CalculateFlows([]History{flat})["X"]
	if sf == nil { // still held today, so a leg exists with zero delta
		t.Fatal("expected a leg for a currently-held stock")
	}
	leg, _ := legFor(sf, "E")
	if leg.ConsecutiveBuyDays != 0 || leg.ConsecutiveSellDays != 0 || leg.DeltaShares != 0 {
		t.Errorf("flat day: %+v", leg)
	}
}

func TestNewAddAndFullSellOut(t *testing.T) {
	// Stock A: absent yesterday, added today → delta = +today (a fresh buy).
	// Stock B: held yesterday, gone today  → delta = -prev (full sell-out), no
	//          longer "held" but the latest move must still register a leg.
	hist := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("E", TypeActive, "2026-06-09", map[string]int64{"B": 500}),
		snap("E", TypeActive, "2026-06-10", map[string]int64{"A": 300}),
	}}
	flows := CalculateFlows([]History{hist})

	a, _ := legFor(flows["A"], "E")
	if a.DeltaShares != 300 || a.TodayShares != 300 || a.PrevShares != 0 || a.ConsecutiveBuyDays != 1 {
		t.Errorf("new add A: %+v", a)
	}
	b, _ := legFor(flows["B"], "E")
	if b.DeltaShares != -500 || b.TodayShares != 0 || b.PrevShares != 500 || b.ConsecutiveSellDays != 1 {
		t.Errorf("sell-out B: %+v", b)
	}
}

func TestGoneStockProducesNoLeg(t *testing.T) {
	// Z held only in the oldest snapshot, 0 in the last two → no leg, no noise.
	hist := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("E", TypeActive, "2026-06-08", map[string]int64{"Z": 100, "X": 500}),
		snap("E", TypeActive, "2026-06-09", map[string]int64{"X": 500}),
		snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 500}),
	}}
	flows := CalculateFlows([]History{hist})
	if _, ok := flows["Z"]; ok {
		t.Error("Z should produce no leg once long gone")
	}
	if _, ok := flows["X"]; !ok {
		t.Error("X should still have a leg")
	}
}

func TestActivePassiveAggregateAcrossETFs(t *testing.T) {
	active := History{ETFCode: "00981A", ETFType: TypeActive, Snapshots: []*Snapshot{
		snap("00981A", TypeActive, "2026-06-09", map[string]int64{"2330": 1000}),
		snap("00981A", TypeActive, "2026-06-10", map[string]int64{"2330": 1300}), // +300 active
	}}
	passive := History{ETFCode: "00919", ETFType: TypePassive, Snapshots: []*Snapshot{
		snap("00919", TypePassive, "2026-06-09", map[string]int64{"2330": 5000}),
		snap("00919", TypePassive, "2026-06-10", map[string]int64{"2330": 4500}), // -500 passive
	}}
	sf := CalculateFlows([]History{active, passive})["2330"]
	if sf.TotalActiveDeltaShares != 300 {
		t.Errorf("active total: got %d want 300", sf.TotalActiveDeltaShares)
	}
	if sf.TotalPassiveDeltaShares != -500 {
		t.Errorf("passive total: got %d want -500", sf.TotalPassiveDeltaShares)
	}
	if sf.TotalDeltaShares != -200 {
		t.Errorf("total: got %d want -200", sf.TotalDeltaShares)
	}
	if len(sf.Legs) != 2 || sf.Legs[0].ETFCode != "00919" { // sorted by ETF code
		t.Errorf("legs not sorted/complete: %+v", sf.Legs)
	}
}

func TestAggregateSectors(t *testing.T) {
	flows := map[string]*StockFlow{
		"2330": {StockCode: "2330", TotalActiveDeltaShares: 300, TotalPassiveDeltaShares: -500},
		"2303": {StockCode: "2303", TotalActiveDeltaShares: -200, TotalPassiveDeltaShares: 0},
		"2454": {StockCode: "2454", TotalActiveDeltaShares: 100, TotalPassiveDeltaShares: 50},
	}
	sectorOf := map[string]string{"2330": "半導體", "2303": "半導體", "2454": "IC設計"}
	sec := AggregateSectors(flows, sectorOf)

	semi := sec["半導體"]
	if semi == nil || semi.ActiveDeltaShares != 100 || semi.PassiveDeltaShares != -500 {
		t.Errorf("半導體 sector flow: %+v", semi)
	}
	if _, ok := sec["IC設計"]; !ok {
		t.Error("expected IC設計 sector")
	}
}

func TestAggregateSectorsSkipsUnmapped(t *testing.T) {
	flows := map[string]*StockFlow{"9999": {StockCode: "9999", TotalActiveDeltaShares: 10}}
	sec := AggregateSectors(flows, map[string]string{}) // no mapping
	if len(sec) != 0 {
		t.Errorf("unmapped stock should add no sector: %+v", sec)
	}
}

func TestPressureRatio(t *testing.T) {
	if got := PressureRatio(-200_000, 1_000_000); got != 0.2 {
		t.Errorf("ratio: got %v want 0.2", got)
	}
	if got := PressureRatio(300, 0); got != 0 { // avg vol unavailable → safe 0
		t.Errorf("zero avg vol: got %v want 0", got)
	}
	if got := PressureRatio(0, 1000); got != 0 {
		t.Errorf("zero delta: got %v want 0", got)
	}
}

func TestCalculateFlowsEdgeCases(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		if got := CalculateFlows(nil); len(got) != 0 {
			t.Errorf("nil → %v", got)
		}
	})
	t.Run("single snapshot has no delta but a leg", func(t *testing.T) {
		hist := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
			snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 500}),
		}}
		leg, ok := legFor(CalculateFlows([]History{hist})["X"], "E")
		if !ok || leg.DeltaShares != 0 || leg.PrevAsOfDate != "" {
			t.Errorf("single snapshot leg: %+v ok=%v", leg, ok)
		}
	})
	t.Run("unordered snapshots are sorted", func(t *testing.T) {
		hist := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
			snap("E", TypeActive, "2026-06-10", map[string]int64{"X": 600}), // newest given first
			snap("E", TypeActive, "2026-06-09", map[string]int64{"X": 1000}),
		}}
		leg, _ := legFor(CalculateFlows([]History{hist})["X"], "E")
		if leg.DeltaShares != -400 || leg.AsOfDate != "2026-06-10" {
			t.Errorf("sorting failed: %+v", leg)
		}
	})
}

func TestWeightDelta(t *testing.T) {
	hist := History{ETFCode: "E", ETFType: TypeActive, Snapshots: []*Snapshot{
		{ETFCode: "E", ETFType: TypeActive, AsOfDate: "2026-06-09",
			Holdings: []Holding{{StockCode: "X", HoldingShares: 1000, HoldingWeight: 5.0}}},
		{ETFCode: "E", ETFType: TypeActive, AsOfDate: "2026-06-10",
			Holdings: []Holding{{StockCode: "X", HoldingShares: 800, HoldingWeight: 3.5}}},
	}}
	leg, _ := legFor(CalculateFlows([]History{hist})["X"], "E")
	if leg.DeltaWeight != -1.5 {
		t.Errorf("DeltaWeight: got %v want -1.5", leg.DeltaWeight)
	}
}
