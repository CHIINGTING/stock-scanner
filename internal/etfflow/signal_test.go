package etfflow

import (
	"math"
	"strings"
	"testing"
	"time"
)

// ── builders ────────────────────────────────────────────────────────────────

func leg(etf, etfType string, delta int64, buy, sell int) StockETFLeg {
	return StockETFLeg{
		ETFCode:             etf,
		ETFType:             etfType,
		DeltaShares:         delta,
		ConsecutiveBuyDays:  buy,
		ConsecutiveSellDays: sell,
	}
}

// flowWith assembles a StockFlow from legs, keeping the active/passive/total
// aggregates consistent (mirroring what CalculateFlows would produce).
func flowWith(legs ...StockETFLeg) *StockFlow {
	sf := &StockFlow{StockCode: "2330", Legs: legs}
	for _, l := range legs {
		sf.TotalDeltaShares += l.DeltaShares
		if l.ETFType == TypeActive {
			sf.TotalActiveDeltaShares += l.DeltaShares
		} else {
			sf.TotalPassiveDeltaShares += l.DeltaShares
		}
	}
	return sf
}

// classify runs Classify with a fixed valid report/as-of pair (2-day lag).
func classify(flow *StockFlow, avgVol float64) StockFlowSignal {
	return Classify(ClassifyInput{
		StockCode:   "2330",
		Flow:        flow,
		AvgVolume20: avgVol,
		ReportDate:  time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC),
		AsOfDate:    "2026-06-10",
		FetchedAt:   time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC),
	})
}

const avgVol = 1_000_000.0

// ── 1–5: signal classification ──────────────────────────────────────────────

func TestPassiveSellPressure(t *testing.T) {
	got := classify(flowWith(leg("00919", TypePassive, -300_000, 0, 4)), avgVol)
	if got.Signal != PassiveSellPressure {
		t.Errorf("signal: got %s want %s", got.Signal, PassiveSellPressure)
	}
	if got.Strength != StrengthLow { // ratio 0.3 → LOW
		t.Errorf("strength: got %s want LOW", got.Strength)
	}
}

func TestPassiveBuySupport(t *testing.T) {
	got := classify(flowWith(leg("00919", TypePassive, 600_000, 1, 0)), avgVol)
	if got.Signal != PassiveBuySupport || got.Strength != StrengthMedium { // ratio 0.6 → MEDIUM
		t.Errorf("got %s/%s want PASSIVE_ETF_BUY_SUPPORT/MEDIUM", got.Signal, got.Strength)
	}
}

func TestActiveSellPressure(t *testing.T) {
	got := classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 2)), avgVol)
	if got.Signal != ActiveSellPressure || got.Strength != StrengthLow { // ratio 0.4 → LOW
		t.Errorf("got %s/%s want ACTIVE_ETF_SELL_PRESSURE/LOW", got.Signal, got.Strength)
	}
}

func TestActiveBuySupport(t *testing.T) {
	got := classify(flowWith(leg("00981A", TypeActive, 1_200_000, 2, 0)), avgVol)
	if got.Signal != ActiveBuySupport || got.Strength != StrengthHigh { // ratio 1.2, consec 2 → HIGH
		t.Errorf("got %s/%s want ACTIVE_ETF_BUY_SUPPORT/HIGH", got.Signal, got.Strength)
	}
}

func TestAggregateSignals(t *testing.T) {
	// Neither level alone fires (each ratio 0.15) but the combined delta does (0.3).
	sell := classify(flowWith(
		leg("00981A", TypeActive, -150_000, 0, 1),
		leg("00919", TypePassive, -150_000, 0, 1),
	), avgVol)
	if sell.Signal != AggregateSellPressure || sell.Strength != StrengthLow {
		t.Errorf("aggregate sell: got %s/%s", sell.Signal, sell.Strength)
	}
	buy := classify(flowWith(
		leg("00981A", TypeActive, 150_000, 1, 0),
		leg("00919", TypePassive, 150_000, 1, 0),
	), avgVol)
	if buy.Signal != AggregateBuySupport || buy.Strength != StrengthLow {
		t.Errorf("aggregate buy: got %s/%s", buy.Signal, buy.Strength)
	}
}

// ── 6: strength boundaries 0.2 / 0.5 / 1.0 ──────────────────────────────────

func TestStrengthBoundaries(t *testing.T) {
	th := DefaultStrengthThresholds()
	cases := []struct {
		ratio  float64
		consec int
		want   FlowStrength
	}{
		{0.20, 0, StrengthNone},  // not > 0.2
		{0.2001, 0, StrengthLow}, // just over 0.2
		{0.50, 0, StrengthLow},   // not > 0.5 → stays LOW
		{0.5001, 0, StrengthMedium},
		{1.00, 0, StrengthMedium}, // not > 1.0 → stays MEDIUM
		{1.0001, 0, StrengthHigh},
		{0.0, 3, StrengthLow},  // consecutive-only LOW
		{0.0, 2, StrengthNone}, // below consecutive floor
	}
	for _, c := range cases {
		if got := th.tier(c.ratio, c.consec); got != c.want {
			t.Errorf("tier(%.4f,%d)=%s want %s", c.ratio, c.consec, got, c.want)
		}
	}
}

// ── 7: EXTREME ───────────────────────────────────────────────────────────────

func TestExtreme(t *testing.T) {
	th := DefaultStrengthThresholds()
	if got := th.tier(1.5, 5); got != StrengthExtreme {
		t.Errorf("tier(1.5,5)=%s want EXTREME", got)
	}
	if got := th.tier(1.5, 4); got != StrengthHigh { // consec < 5 → only HIGH
		t.Errorf("tier(1.5,4)=%s want HIGH", got)
	}
	got := classify(flowWith(leg("00981A", TypeActive, -1_500_000, 0, 5)), avgVol)
	if got.Signal != ActiveSellPressure || got.Strength != StrengthExtreme {
		t.Errorf("classify extreme: got %s/%s", got.Signal, got.Strength)
	}
}

// ── 8: AvgVolume20 <= 0 → no panic, no Inf/NaN ──────────────────────────────

func TestZeroAvgVolumeSafe(t *testing.T) {
	got := classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 4)), 0)
	for _, r := range []float64{got.ActivePressureRatio, got.PassivePressureRatio, got.AggregatePressureRatio} {
		if math.IsInf(r, 0) || math.IsNaN(r) {
			t.Fatalf("ratio not finite: %v", r)
		}
		if r != 0 {
			t.Errorf("ratio should be 0 when avg vol unavailable, got %v", r)
		}
	}
	// Strength still rests on the 4-day sell streak (>= 3 → LOW).
	if got.Signal != ActiveSellPressure || got.Strength != StrengthLow {
		t.Errorf("consecutive-only fallback: got %s/%s", got.Signal, got.Strength)
	}
	if !got.Computed {
		t.Error("zero avg vol should still be Computed (consecutive-day basis)")
	}
	if !containsSubstr(got.Caveats, "20 日均量不可得") {
		t.Errorf("missing avg-vol caveat: %v", got.Caveats)
	}
}

// ── 9: data_lag_days ─────────────────────────────────────────────────────────

func TestDataLagDays(t *testing.T) {
	report := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if got := DataLagDays(report, "2026-06-10"); got != 2 {
		t.Errorf("DataLagDays=%d want 2", got)
	}
	if got := DataLagDays(report, "2026-06-12"); got != 0 {
		t.Errorf("same-day lag=%d want 0", got)
	}
	if got := classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 4)), avgVol); got.DataLagDays != 2 {
		t.Errorf("signal DataLagDays=%d want 2", got.DataLagDays)
	}
}

// ── 10: invalid as_of_date → safe degrade ───────────────────────────────────

func TestInvalidAsOfDegrades(t *testing.T) {
	if got := DataLagDays(time.Now(), "2026/06/10"); got != -1 {
		t.Errorf("invalid as_of DataLagDays=%d want -1", got)
	}
	got := Classify(ClassifyInput{
		StockCode:   "2330",
		Flow:        flowWith(leg("00981A", TypeActive, -400_000, 0, 4)),
		AvgVolume20: avgVol,
		ReportDate:  time.Now(),
		AsOfDate:    "not-a-date",
	})
	if got.Computed {
		t.Error("invalid as_of should set Computed=false")
	}
	if got.DataLagDays != -1 {
		t.Errorf("DataLagDays=%d want -1", got.DataLagDays)
	}
	if !containsSubstr(got.Caveats, "as_of_date 無效") {
		t.Errorf("missing invalid-as-of caveat: %v", got.Caveats)
	}
}

// ── 11: caveats always present ──────────────────────────────────────────────

func TestCaveatsAlwaysPresent(t *testing.T) {
	samples := []StockFlowSignal{
		classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 4)), avgVol), // a signal
		classify(flowWith(leg("00981A", TypeActive, 1_000, 0, 0)), avgVol),    // neutral
		classify(nil, avgVol), // nil flow
	}
	for i, s := range samples {
		for _, want := range baseCaveats {
			if !containsExact(s.Caveats, want) {
				t.Errorf("sample %d missing base caveat %q (got %v)", i, want, s.Caveats)
			}
		}
	}
}

// ── 12: forbidden tokens absent from reasons/caveats ────────────────────────

func TestNoForbiddenTokens(t *testing.T) {
	forbidden := []string{
		"可以買", "可以賣", "買進", "賣出", "到價下單", "建議買", "建議賣",
		"BUY", "SELL", "PLACE_ORDER", "AUTO_BUY", "AUTO_SELL",
	}
	// Exercise many shapes so generated prose is covered broadly.
	samples := []StockFlowSignal{
		classify(flowWith(leg("00981A", TypeActive, -1_500_000, 0, 5)), avgVol),
		classify(flowWith(leg("00981A", TypeActive, 1_200_000, 2, 0)), avgVol),
		classify(flowWith(leg("00919", TypePassive, -300_000, 0, 4)), avgVol),
		classify(flowWith(leg("00919", TypePassive, 600_000, 1, 0)), avgVol),
		classify(flowWith(leg("00981A", TypeActive, -150_000, 0, 1), leg("00919", TypePassive, -150_000, 0, 1)), avgVol),
		classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 4)), 0),
		classify(flowWith(leg("00981A", TypeActive, 1, 0, 0)), avgVol), // neutral
		classify(nil, avgVol),
	}
	for i, s := range samples {
		for _, line := range append(append([]string{}, s.Reasons...), s.Caveats...) {
			for _, tok := range forbidden {
				if strings.Contains(line, tok) {
					t.Errorf("sample %d line %q contains forbidden token %q", i, line, tok)
				}
			}
		}
	}
}

// ── consensus (stub for first single-active version) ────────────────────────

func TestSingleActiveIsNotConsensus(t *testing.T) {
	got := classify(flowWith(leg("00981A", TypeActive, -400_000, 0, 4)), avgVol)
	if got.Signal == ActiveConsensusSell || got.Signal == ActiveConsensusBuy {
		t.Errorf("single active ETF must not be consensus, got %s", got.Signal)
	}
	if got.Signal != ActiveSellPressure {
		t.Errorf("got %s want ACTIVE_ETF_SELL_PRESSURE", got.Signal)
	}
}

func TestTwoActiveSameDirectionIsConsensus(t *testing.T) {
	got := classify(flowWith(
		leg("00981A", TypeActive, -400_000, 0, 3),
		leg("00980A", TypeActive, -400_000, 0, 3),
	), avgVol)
	if got.Signal != ActiveConsensusSell { // total -800k → ratio 0.8 MEDIUM
		t.Errorf("got %s want ACTIVE_ETF_CONSENSUS_SELL", got.Signal)
	}
	if got.Strength != StrengthMedium {
		t.Errorf("strength: got %s want MEDIUM", got.Strength)
	}
}

func TestTwoActiveMixedIsNotConsensus(t *testing.T) {
	// One buys, one sells more → net sell, but directions disagree → no consensus.
	got := classify(flowWith(
		leg("00981A", TypeActive, 200_000, 1, 0),
		leg("00980A", TypeActive, -600_000, 0, 2),
	), avgVol)
	if got.Signal == ActiveConsensusSell || got.Signal == ActiveConsensusBuy {
		t.Errorf("mixed active directions must not be consensus, got %s", got.Signal)
	}
}

// ── neutral & nil ───────────────────────────────────────────────────────────

func TestNeutral(t *testing.T) {
	got := classify(flowWith(leg("00981A", TypeActive, 1_000, 0, 0)), avgVol) // ratio ~0.001
	if got.Signal != FlowNeutral || got.Strength != StrengthNone {
		t.Errorf("got %s/%s want ETF_FLOW_NEUTRAL/none", got.Signal, got.Strength)
	}
	if !got.Computed {
		t.Error("a valid-but-quiet flow is still Computed")
	}
}

func TestNilFlow(t *testing.T) {
	got := classify(nil, avgVol)
	if got.Computed {
		t.Error("nil flow → Computed=false")
	}
	if got.Signal != FlowNeutral {
		t.Errorf("nil flow signal: got %s", got.Signal)
	}
}

func TestZeroThresholdsFallBackToDefault(t *testing.T) {
	in := ClassifyInput{
		StockCode:   "2330",
		Flow:        flowWith(leg("00981A", TypeActive, -400_000, 0, 4)),
		AvgVolume20: avgVol,
		ReportDate:  time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC),
		AsOfDate:    "2026-06-10",
		// Thresholds left zero → must behave as Default.
	}
	if got := Classify(in); got.Signal != ActiveSellPressure || got.Strength != StrengthLow {
		t.Errorf("zero thresholds fallback: got %s/%s", got.Signal, got.Strength)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func containsExact(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsSubstr(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
