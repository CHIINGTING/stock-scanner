package r6backtest

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// teStock builds a synthetic stock with a compounding drift, plus the derived series the
// conditions read. Compounding (not a fixed step) keeps the percentage slope constant, which
// is what the slope definition is supposed to measure.
func teStock(sym, sector string, n int, start, drift float64, vol float64) *Stock {
	s := &Stock{Symbol: sym, Sector: sector, idxOf: make(map[string]int, n)}
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	price := start
	for i := 0; i < n; i++ {
		d := base.AddDate(0, 0, i)
		s.Candles = append(s.Candles, fetcher.Candle{
			Date: d, Open: price, High: price * 1.01, Low: price * 0.99, Close: price,
			Volume: int64(vol),
		})
		s.Close = append(s.Close, price)
		s.High = append(s.High, price*1.01)
		s.Low = append(s.Low, price*0.99)
		s.Vol = append(s.Vol, vol)
		s.idxOf[d.Format("2006-01-02")] = i
		price *= 1 + drift
	}
	s.MA20 = sma(s.Close, 20)
	s.MA60 = sma(s.Close, 60)
	s.VolMA20 = sma(s.Vol, 20)
	return s
}

func teUniverse(stocks ...*Stock) *Universe {
	u := &Universe{bySym: map[string]*Stock{}}
	dates := map[string]bool{}
	for _, s := range stocks {
		u.Stocks = append(u.Stocks, s)
		u.bySym[s.Symbol] = s
		for _, c := range s.Candles {
			dates[c.Date.Format("2006-01-02")] = true
		}
	}
	for d := range dates {
		u.Axis = append(u.Axis, d)
	}
	// The axis must be sorted for date indices to mean anything.
	for i := 1; i < len(u.Axis); i++ {
		for j := i; j > 0 && u.Axis[j] < u.Axis[j-1]; j-- {
			u.Axis[j], u.Axis[j-1] = u.Axis[j-1], u.Axis[j]
		}
	}
	return u
}

// The backtest must measure the SHIPPED thresholds. If this drifts, the validation is
// reporting on a hypothesis nobody runs.
func TestBacktestUsesShippedThresholds(t *testing.T) {
	want := scanner.DefaultTrendExtThresholds()
	if th != want {
		t.Errorf("backtest thresholds diverged from the scanner's: %+v vs %+v", th, want)
	}
	if th.MA20SlopeLookback <= 0 || th.BiasWindow <= 0 || th.ExtendedPct <= 0 {
		t.Errorf("thresholds look unpopulated: %+v", th)
	}
}

func TestPerBarMetrics(t *testing.T) {
	s := teStock("AAA", "半導體", 400, 100, 0.004, 1000)
	i := len(s.Close) - 1

	slope, ok := ma20SlopeAt(s, i)
	if !ok || slope <= 0 {
		t.Errorf("rising series → MA20 slope %v (ok=%v), want > 0", slope, ok)
	}
	if _, ok := ma60SlopeAt(s, i); !ok {
		t.Error("400 bars should give an MA60 slope")
	}
	bias, ok := bias20At(s, i)
	if !ok || bias <= 0 {
		t.Errorf("price above a lagging MA20 → bias %v (ok=%v), want > 0", bias, ok)
	}
	p, ok := bias20PercentileAt(s, i)
	if !ok {
		t.Fatal("400 bars should give a BIAS percentile")
	}
	if p < 0 || p > 100 {
		t.Errorf("percentile out of range: %v", p)
	}
}

// Early bars must be unavailable rather than silently computed off the SMA warm-up zeros.
func TestPerBarMetricsWarmup(t *testing.T) {
	s := teStock("AAA", "半導體", 400, 100, 0.004, 1000)
	for _, i := range []int{0, 5, 18} {
		if _, ok := ma20SlopeAt(s, i); ok {
			t.Errorf("bar %d: MA20 slope must be unavailable during warm-up", i)
		}
		if _, ok := bias20At(s, i); ok {
			t.Errorf("bar %d: BIAS must be unavailable during warm-up", i)
		}
	}
	// The percentile needs a full min-sample window, well beyond the MA warm-up.
	if _, ok := bias20PercentileAt(s, 100); ok {
		t.Error("bar 100 has fewer than the minimum BIAS observations")
	}
}

// No condition may read a bar after i. Truncating the series at i must not change any
// verdict — this is the look-ahead guard, asserted rather than asserted-in-a-comment.
func TestConditionsAreCausal(t *testing.T) {
	full := teStock("AAA", "半導體", 400, 100, 0.004, 1000)
	i := 350

	truncated := teStock("AAA", "半導體", i+1, 100, 0.004, 1000)

	for _, c := range TrendExtConditions() {
		a := c.Detect(full, i, nil)
		b := c.Detect(truncated, i, nil)
		if a != b {
			t.Errorf("%s: verdict changed when future bars were removed (%v → %v) — it reads ahead",
				c.Name, a, b)
		}
	}
}

func TestBuildHeatPanelUsesLiveAggregation(t *testing.T) {
	// One sector with enough members to clear the floor, one that cannot.
	var stocks []*Stock
	for i := 0; i < 6; i++ {
		stocks = append(stocks, teStock(string(rune('A'+i)), "大族群", 200, 100, 0.004, 1000))
	}
	stocks = append(stocks, teStock("X", "小族群", 200, 100, 0.004, 1000))
	stocks = append(stocks, teStock("Y", "小族群", 200, 100, 0.004, 1000))

	u := teUniverse(stocks...)
	panel := BuildHeatPanel(u, 1)
	last := len(u.Axis) - 1

	big := panel.at(last, "大族群")
	if big == nil || big.Status != scanner.SectorHeatOK {
		t.Fatalf("a 6-member sector should be measurable, got %+v", big)
	}
	if big.ValidMemberCount != 6 || big.MemberCount != 6 {
		t.Errorf("member disclosure wrong: %d/%d", big.ValidMemberCount, big.MemberCount)
	}
	// All members are in the same relentless uptrend → breadth and participation maxed.
	if big.Breadth != 100 || big.Participation != 100 {
		t.Errorf("uniform uptrend should max breadth/participation, got %v/%v",
			big.Breadth, big.Participation)
	}

	small := panel.at(last, "小族群")
	if small == nil || small.Status != scanner.SectorHeatInsufficientData {
		t.Fatalf("a 2-member sector must be INSUFFICIENT_DATA, got %+v", small)
	}
	if small.Heat != 0 {
		t.Errorf("no heat may be published below the floor, got %v", small.Heat)
	}
}

// The panel must not consult future bars either: the reading for date d must be identical
// whether or not later dates exist in the universe.
func TestHeatPanelIsCausal(t *testing.T) {
	mk := func(n int) *Universe {
		var stocks []*Stock
		for i := 0; i < 5; i++ {
			stocks = append(stocks, teStock(string(rune('A'+i)), "族群", n, 100, 0.004, 1000))
		}
		return teUniverse(stocks...)
	}
	long, short := mk(200), mk(150)
	d := 149 // last date of the short universe

	a := BuildHeatPanel(long, 1).at(d, "族群")
	b := BuildHeatPanel(short, 1).at(d, "族群")
	if a == nil || b == nil {
		t.Fatal("both panels should have a reading for this date")
	}
	if a.Breadth != b.Breadth || a.Participation != b.Participation ||
		a.MedianReturn20 != b.MedianReturn20 {
		t.Errorf("the panel reads future bars: %+v vs %+v", a, b)
	}
}

// The step approximation must reuse the LAST computed panel, never a later one.
func TestHeatPanelStepReusesPastOnly(t *testing.T) {
	var stocks []*Stock
	for i := 0; i < 5; i++ {
		stocks = append(stocks, teStock(string(rune('A'+i)), "族群", 200, 100, 0.004, 1000))
	}
	u := teUniverse(stocks...)
	panel := BuildHeatPanel(u, 5)

	for d := 1; d < len(u.Axis); d++ {
		if d%5 == 0 {
			continue
		}
		anchor := d - d%5
		if panel.at(d, "族群") != panel.at(anchor, "族群") {
			t.Errorf("date %d did not reuse its most recent computed anchor %d", d, anchor)
		}
	}
}

func TestTrendExtConditionsCoverTheComparison(t *testing.T) {
	names := map[string]bool{}
	for _, c := range TrendExtConditions() {
		if c.Name == "" || c.Detect == nil {
			t.Errorf("malformed condition %+v", c.Name)
		}
		if names[c.Name] {
			t.Errorf("duplicate condition %s", c.Name)
		}
		names[c.Name] = true
	}
	// The comparison the R12 validation is required to make: a baseline, each leg alone,
	// and the combined states.
	for _, required := range []string{
		"BASELINE_ALL_BARS",
		"SLOPE_ONLY_MA20_UP",
		"BIAS_ONLY_NEAR_MA20",
		"BIAS_ONLY_EXTENDED_P90",
		"SECTOR_ONLY_HEAT_STRONG",
		"STATE_PULLBACK_IN_UPTREND",
		"STATE_EXTENDED",
	} {
		if !names[required] {
			t.Errorf("the comparison is missing %s", required)
		}
	}
}

// PULLBACK and EXTENDED must be mutually exclusive by construction — if the same bar could
// satisfy both, comparing their forward returns would be meaningless.
func TestPullbackAndExtendedAreDisjoint(t *testing.T) {
	var pullback, extended TrendExtCondition
	for _, c := range TrendExtConditions() {
		switch c.Name {
		case "STATE_PULLBACK_IN_UPTREND":
			pullback = c
		case "STATE_EXTENDED":
			extended = c
		}
	}
	heat := &scanner.SectorHeatView{Status: scanner.SectorHeatOK, Heat: 70}

	for _, drift := range []float64{-0.004, -0.001, 0, 0.001, 0.004, 0.01} {
		s := teStock("AAA", "族群", 400, 100, drift, 1000)
		for i := 300; i < len(s.Close); i++ {
			if pullback.Detect(s, i, heat) && extended.Detect(s, i, heat) {
				t.Fatalf("drift %v bar %d satisfies both states at once", drift, i)
			}
		}
	}
}

func TestLoadSectorMap(t *testing.T) {
	m, err := LoadSectorMap("../../configs/sectors.yaml")
	if err != nil {
		t.Skipf("sectors.yaml unavailable: %v", err)
	}
	if len(m) == 0 {
		t.Fatal("the sector map is empty")
	}
	// The cache stores bare codes, so the bare form must resolve.
	var bare int
	for k := range m {
		if len(k) <= 6 && k[0] >= '0' && k[0] <= '9' {
			bare++
		}
	}
	if bare == 0 {
		t.Error("no bare stock codes were mapped — the universe would get no sectors at all")
	}
}
