package r6backtest

import (
	"testing"
)

// allRSPanel builds an RS panel assigning each symbol a fixed percentile.
func allRSPanel(u *Universe, byCode map[string]float64, def float64) *RSPanel {
	m := map[string]map[string]float64{}
	for _, s := range u.Stocks {
		for d := range s.idxOf {
			if m[d] == nil {
				m[d] = map[string]float64{}
			}
			v := def
			if x, ok := byCode[s.Symbol]; ok {
				v = x
			}
			m[d][s.Symbol] = v
		}
	}
	return &RSPanel{byDate: m}
}

// crashUniverse builds a universe with a 0050 proxy that crashes ~10% over 20d
// in one window, plus member stocks. proxy and members share a date axis.
func crashUniverse() (*Universe, *RegimePanel) {
	u := &Universe{bySym: map[string]*Stock{}}
	set := map[string]struct{}{}
	add := func(sym string, closes []float64) *Stock {
		s := withATR(withRSI(mkStock(sym, closes)))
		u.Stocks = append(u.Stocks, s)
		u.bySym[sym] = s
		for d := range s.idxOf {
			set[d] = struct{}{}
		}
		return s
	}
	// 0050: flat 100 for 200 bars, then drop to 88 (−12% over 20d) around bar 220.
	proxy := make([]float64, 320)
	for i := range proxy {
		switch {
		case i < 200:
			proxy[i] = 100
		case i < 220:
			proxy[i] = 100 - 0.6*float64(i-200) // glide down to ~88
		default:
			proxy[i] = 88
		}
	}
	add("0050", proxy)
	// a resilient member: holds up better than proxy in the crash window.
	mem := make([]float64, 320)
	for i := range mem {
		switch {
		case i < 200:
			mem[i] = 50
		case i < 220:
			mem[i] = 50 - 0.1*float64(i-200) // only mild dip
		default:
			mem[i] = 48
		}
	}
	add("2330", mem)
	for d := range set {
		u.Axis = append(u.Axis, d)
	}
	rp := BuildRegimePanel(u, -8.0)
	return u, rp
}

// 1. regime detection + event segmentation.
func TestBuildRegimePanel(t *testing.T) {
	_, rp := crashUniverse()
	if !rp.ProxyOK {
		t.Fatal("proxy 0050 should be present")
	}
	if rp.EventCount < 1 {
		t.Fatalf("expected ≥1 crash event, got %d", rp.EventCount)
	}
	// some date must be in regime with proxy20 ≤ -8.
	found := false
	for _, rd := range rp.byDate {
		if rd.InRegime {
			found = true
			if rd.ProxyReturn20 > -8 {
				t.Errorf("regime day proxy20 should be ≤ -8, got %v", rd.ProxyReturn20)
			}
			if rd.EventID == 0 {
				t.Errorf("regime day must have an EventID")
			}
		}
	}
	if !found {
		t.Fatal("no regime day detected")
	}
}

// 1b. event merge: two regime clusters within mergeGap = one event; far apart = two.
func TestRegimeEventMerge(t *testing.T) {
	u := &Universe{bySym: map[string]*Stock{}}
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 100
	}
	// cluster A around 120-126, cluster B around 180-186 (far apart → 2 events).
	for _, c := range [][2]int{{120, 126}, {180, 186}} {
		for i := c[0]; i <= c[1]; i++ {
			closes[i] = 80 // -20% vs 100 (20 bars back)
		}
	}
	s := mkStock("0050", closes)
	u.Stocks = []*Stock{s}
	u.bySym["0050"] = s
	for d := range s.idxOf {
		u.Axis = append(u.Axis, d)
	}
	rp := BuildRegimePanel(u, -8.0)
	if rp.EventCount != 2 {
		t.Errorf("two far-apart clusters → 2 events, got %d", rp.EventCount)
	}
}

// 2. Setup D triggers only in regime, with RS/rel/weekly/flow gates.
func TestSetupD_Triggers(t *testing.T) {
	u, rp := crashUniverse()
	s, _ := u.Get("2330")
	// find a regime day index on the member.
	var i int
	for k := 120; k < len(s.Candles); k++ {
		if rd, ok := rp.At(dateKey(s.Candles[k].Date)); ok && rd.InRegime {
			i = k
			break
		}
	}
	if i == 0 {
		t.Skip("no regime day on member axis")
	}
	// near MA20 + volume dry already hold on a flat-ish member; stub flow/weekly.
	old, oldW := flowProvider, mtfWeeklyProvider
	flowProvider = func(*Stock, int) string { return "MOMENTUM_NEUTRAL" }
	mtfWeeklyProvider = func(*Stock, int) string { return "RANGE" }
	defer func() { flowProvider, mtfWeeklyProvider = old, oldW }()

	d := SetupD{Regime: rp, RelThreshold: 5}
	// force near-MA20 + vol dry on the member at i.
	s.Low[i] = s.MA20[i]
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	tr := d.Detect(u, stubPanel(s, 85), s, i, DefaultParams())
	if tr == nil || tr.Crash == nil {
		t.Fatalf("Setup D should trigger in regime with all gates passing")
	}
	if tr.Crash.RegimeEventID == 0 || tr.Crash.ProxySymbol != "0050" {
		t.Errorf("crash context not populated: %+v", tr.Crash)
	}
}

// 3. non-regime day → no trigger.
func TestSetupD_NoRegime(t *testing.T) {
	u, rp := crashUniverse()
	s, _ := u.Get("2330")
	i := 150 // pre-crash flat → not regime
	d := SetupD{Regime: rp, RelThreshold: 5}
	if tr := d.Detect(u, stubPanel(s, 85), s, i, DefaultParams()); tr != nil {
		t.Errorf("non-regime day must not trigger")
	}
}

// 4. weekly DOWNTREND and SHIFT_DOWN flow block the main setup.
func TestSetupD_GatesBlock(t *testing.T) {
	u, rp := crashUniverse()
	s, _ := u.Get("2330")
	var i int
	for k := 120; k < len(s.Candles); k++ {
		if rd, ok := rp.At(dateKey(s.Candles[k].Date)); ok && rd.InRegime {
			i = k
			break
		}
	}
	if i == 0 {
		t.Skip("no regime day")
	}
	s.Low[i] = s.MA20[i]
	s.Vol[i], s.Vol[i-1], s.Vol[i-2] = 100, 100, 100
	s.VolMA20 = sma(s.Vol, 20)
	old, oldW := flowProvider, mtfWeeklyProvider
	defer func() { flowProvider, mtfWeeklyProvider = old, oldW }()
	d := SetupD{Regime: rp, RelThreshold: 5}

	flowProvider = func(*Stock, int) string { return "MOMENTUM_NEUTRAL" }
	mtfWeeklyProvider = func(*Stock, int) string { return "DOWNTREND" }
	if d.Detect(u, stubPanel(s, 85), s, i, DefaultParams()) != nil {
		t.Errorf("weekly DOWNTREND must block")
	}
	mtfWeeklyProvider = func(*Stock, int) string { return "RANGE" }
	flowProvider = func(*Stock, int) string { return "STRUCTURAL_SHIFT_DOWN" }
	if d.Detect(u, stubPanel(s, 85), s, i, DefaultParams()) != nil {
		t.Errorf("SHIFT_DOWN flow must block")
	}
}

// 5. ForceLowConfidence: even with many trades, confidence stays LOW.
func TestSetupD_ForceLow(t *testing.T) {
	var trades []Trade
	for i := 0; i < 200; i++ {
		trades = append(trades, Trade{Return20d: 1, HoldReturn20d: 1, Crash: &CrashContext{RelativeReturn20d: 6}})
	}
	p := DefaultParams()
	p.ForceLowConfidence = true
	st := ComputeStats("D_CRASH_SURVIVOR", 0, trades, []int{20}, p)
	if st.Confidence != "LOW" {
		t.Errorf("Setup D must be LOW regardless of n=%d, got %s", st.SampleCount, st.Confidence)
	}
}

// 6. relative return + proxy return helpers.
func TestCrashAverages(t *testing.T) {
	trades := []Trade{
		{Crash: &CrashContext{RelativeReturn20d: 4, MarketProxyReturn20d: -10}},
		{Crash: &CrashContext{RelativeReturn20d: 8, MarketProxyReturn20d: -12}},
		{}, // no crash context → ignored
	}
	if got := AvgRelativeReturn(trades); got != 6 {
		t.Errorf("avg rel: got %v want 6", got)
	}
	if got := AvgProxyReturn(trades); got != -11 {
		t.Errorf("avg proxy: got %v want -11", got)
	}
}

// 7. cohort split tags HIGH_RS / LOW_RS.
func TestCohortSplit(t *testing.T) {
	u, rp := crashUniverse()
	old, oldW := flowProvider, mtfWeeklyProvider
	flowProvider = func(*Stock, int) string { return "MOMENTUM_NEUTRAL" }
	mtfWeeklyProvider = func(*Stock, int) string { return "RANGE" }
	defer func() { flowProvider, mtfWeeklyProvider = old, oldW }()
	p := DefaultParams()
	p.Warmup = 120
	panel := allRSPanel(u, map[string]float64{"2330": 85, "0050": 40}, 40)
	high, low := RunCrashCohorts(u, panel, rp, p)
	if high.Stat.Subgroup != "HIGH_RS" || low.Stat.Subgroup != "LOW_RS" {
		t.Errorf("cohort labels wrong: %q / %q", high.Stat.Subgroup, low.Stat.Subgroup)
	}
	if high.Stat.Confidence != "LOW" || low.Stat.Confidence != "LOW" {
		t.Errorf("cohorts must be LOW confidence")
	}
}

// 8. crash CSV schema fixed.
func TestCrashCSVSchema(t *testing.T) {
	want := []string{
		"setup_name", "stock_code", "stock_name", "entry_date", "entry_price",
		"signal_date", "signal_close",
		"return_5d", "return_10d", "return_20d", "return_60d",
		"hold_return_5d", "hold_return_10d", "hold_return_20d", "hold_return_60d",
		"realized_drawdown", "hit_stop", "stop_reason", "stop_date", "stop_price",
		"rs_rank_at_entry", "proxy_symbol", "market_proxy_return_20d", "stock_return_20d",
		"relative_return_vs_market_20d", "breadth_below_ma20_pct",
		"regime_event_id", "regime_start_date", "regime_end_date",
		"momentum_flow", "mtf_signal", "sector",
	}
	got := CrashCSVHeader()
	if len(got) != len(want) {
		t.Fatalf("len got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("col %d: got %q want %q", i, got[i], want[i])
		}
	}
}

// 9. real weekly-trend provider smoke (un-stubbed path).
func TestRealWeeklyProviderSmoke(t *testing.T) {
	s := withRSI(mkStock("WK", buildUptrend(300, 0.004)))
	_ = asofMTFWeekly(s, 299)
}
