package analyzer

import (
	"fmt"
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// series builds n bars from a generator, oldest-first, on synthetic sequential dates.
func series(n int, f func(i int) float64) []model.Bar {
	out := make([]model.Bar, n)
	for i := 0; i < n; i++ {
		out[i] = model.Bar{Date: fmt.Sprintf("d%04d", i), Close: f(i)}
	}
	return out
}

func ramp(n int, from, step float64) []model.Bar {
	return series(n, func(i int) float64 { return from + step*float64(i) })
}

// Too little history is MISSING, not a neutral reading — the contract that keeps a thin
// series from being scored as if it were a calm market.
func TestAnalyzePriceInsufficientHistoryIsMissing(t *testing.T) {
	ev, v := AnalyzePrice(model.Benchmark0050, ramp(80, 100, 0.5), th)

	if ev.Available {
		t.Error("80 bars is below the 120-bar floor; evidence must be unavailable")
	}
	if ev.Direction != model.DirUnknown || ev.Strength != model.StrengthUnknown {
		t.Errorf("missing evidence must be UNKNOWN on both axes, got %s/%s", ev.Direction, ev.Strength)
	}
	if ev.Direction == model.DirNeutral {
		t.Fatal("insufficient history was recorded as a neutral market reading")
	}
	if ev.Usable() {
		t.Error("missing evidence must be excluded from scoring")
	}
	if v.Sufficient {
		t.Error("PriceView.Sufficient must be false")
	}
	if ClassifyStructure(v, th) != model.StructureUnknown {
		t.Error("structure must be UNKNOWN when the view is insufficient")
	}
	if ev.Reason == "" {
		t.Error("missing evidence must say why")
	}
}

func TestAnalyzePriceDerivedQuantities(t *testing.T) {
	// Steady 0.5/day rise over 300 bars: 100.0 → 249.5.
	ev, v := AnalyzePrice(model.Benchmark0050, ramp(300, 100, 0.5), th)

	if !ev.Available || !v.Sufficient {
		t.Fatalf("300 bars should be sufficient: %+v", ev)
	}
	if v.Close != 249.5 {
		t.Errorf("Close = %v, want 249.5", v.Close)
	}
	// A monotonic rise: price above every MA, MAs stacked, MA60 rising, at the high.
	if !(v.Close > v.MA20 && v.MA20 > v.MA60 && v.MA60 > v.MA120) {
		t.Errorf("MAs should stack bullishly: %v %v %v %v", v.Close, v.MA20, v.MA60, v.MA120)
	}
	if !v.MA60Rising {
		t.Error("MA60 must be rising on a monotonic advance")
	}
	if v.DDHigh != 0 {
		t.Errorf("DDHigh = %v, want 0 at a new high", v.DDHigh)
	}
	if v.Ret20 <= 0 || v.Ret60 <= 0 {
		t.Errorf("returns should be positive: 20d=%v 60d=%v", v.Ret20, v.Ret60)
	}
	if v.WorstDay20 != 0 {
		t.Errorf("WorstDay20 = %v, want 0 (no down sessions)", v.WorstDay20)
	}
	if ClassifyStructure(v, th) != model.StructureStrongUptrend {
		t.Errorf("monotonic advance should be STRONG_UPTREND, got %s", ClassifyStructure(v, th))
	}
	if ev.Direction != model.DirBullish {
		t.Errorf("price evidence should be BULLISH, got %s", ev.Direction)
	}
	// MA200 is recorded for context even though no rule reads it.
	if v.MA200 == 0 {
		t.Error("300 bars should produce an MA200 context metric")
	}
}

// MA200 must never gate a verdict: 150 bars is enough for every rule (longest is MA120).
func TestMA200AbsenceDoesNotBlockClassification(t *testing.T) {
	_, v := AnalyzePrice(model.Benchmark0050, ramp(150, 100, 0.5), th)
	if v.MA200 != 0 {
		t.Fatalf("150 bars cannot produce an MA200, got %v", v.MA200)
	}
	if got := ClassifyStructure(v, th); got == model.StructureUnknown {
		t.Error("a missing MA200 must not force UNKNOWN — no rule reads it")
	}
}

func TestDrawdownAndWorstSession(t *testing.T) {
	// Rise to 200 then fall 10% to 180 over the last few bars.
	bars := ramp(200, 100, 0.5)
	bars[len(bars)-1].Close = 180
	_, v := AnalyzePrice(model.Benchmark0050, bars, th)

	// The 60-day high is bar 198 (199.0) — bar 199 is the one we overwrote with 180.
	want := (1 - 180.0/199.0) * 100
	if math.Abs(v.DDHigh-want) > 0.01 {
		t.Errorf("DDHigh = %v, want ~%v", v.DDHigh, want)
	}
	if v.WorstDay20 >= -9 {
		t.Errorf("WorstDay20 = %v, expected a large negative session", v.WorstDay20)
	}
}

// The volatility percentile must degrade to "unknown" rather than report 0 when the
// distribution cannot be built. Tested against realizedVolPercentile directly: at every
// bar count AnalyzePrice accepts (>=120), the distribution IS buildable, so going through
// AnalyzePrice could never reach this branch — a test that only called AnalyzePrice would
// assert nothing at all.
func TestVolatilityPercentileDegradesWhenDistributionTooThin(t *testing.T) {
	// 70 closes → 69 returns → 50 vol samples, under the 60-sample floor.
	thin := make([]float64, 70)
	for i := range thin {
		thin[i] = 100 + 0.5*float64(i)
	}
	vol, pct, known := realizedVolPercentile(thin, th)
	if known {
		t.Fatalf("50 samples is under the 60-sample floor, got known=true (pct=%v)", pct)
	}
	if pct != 0 {
		t.Errorf("an unknown percentile must stay 0, not a computed-looking number: %v", pct)
	}
	if vol <= 0 {
		t.Errorf("the volatility level itself is still measurable, got %v", vol)
	}

	// Below one full window there is no volatility at all.
	if v, p2, k := realizedVolPercentile(thin[:10], th); v != 0 || p2 != 0 || k {
		t.Errorf("10 closes should yield nothing: %v %v %v", v, p2, k)
	}

	// With enough history the percentile is real and in range.
	long := make([]float64, 400)
	for i := range long {
		long[i] = 100 + 0.5*float64(i)
	}
	_, pct, known = realizedVolPercentile(long, th)
	if !known {
		t.Fatal("400 closes should build a distribution")
	}
	if pct <= 0 || pct > 100 {
		t.Errorf("percentile out of range: %v", pct)
	}
}

// ── breadth ───────────────────────────────────────────────────────────────────

// uniform builds n stocks that all share one close series.
func uniform(stocks int, closes []float64) [][]float64 {
	out := make([][]float64, stocks)
	for i := range out {
		cp := make([]float64, len(closes))
		copy(cp, closes)
		out[i] = cp
	}
	return out
}

func dates(n int) []string {
	d := make([]string, n)
	for i := range d {
		d[i] = fmt.Sprintf("d%04d", i)
	}
	return d
}

func rising(n int) []float64 {
	c := make([]float64, n)
	for i := range c {
		c[i] = 100 + 0.5*float64(i)
	}
	return c
}

func TestBreadthPanelAllAboveMA(t *testing.T) {
	n := 200
	p := BuildBreadthPanel(dates(n), uniform(600, rising(n)))
	last := n - 1

	if p.ValidCount[last] != 600 {
		t.Errorf("ValidCount = %d, want 600", p.ValidCount[last])
	}
	if p.AboveMA20[last] != 100 {
		t.Errorf("AboveMA20 = %v, want 100 on a monotonic advance", p.AboveMA20[last])
	}
	if p.AboveMA120[last] != 100 {
		t.Errorf("AboveMA120 = %v, want 100", p.AboveMA120[last])
	}
	if p.Advancing[last] != 100 {
		t.Errorf("Advancing = %v, want 100", p.Advancing[last])
	}
}

// A suspended stock must drop OUT of the count, not be averaged as if its price were zero.
// Feeding gaps into a dense SMA would put every suspended name "below its MA" and fake a
// breadth collapse.
func TestBreadthPanelExcludesGappedStocks(t *testing.T) {
	n := 200
	closes := uniform(600, rising(n))
	// Suspend the last 100 stocks for the final 5 sessions.
	for i := 500; i < 600; i++ {
		for d := n - 5; d < n; d++ {
			closes[i][d] = 0
		}
	}
	p := BuildBreadthPanel(dates(n), closes)
	last := n - 1

	if p.ValidCount[last] != 500 {
		t.Errorf("ValidCount = %d, want 500 (suspended names excluded)", p.ValidCount[last])
	}
	if p.AboveMA20[last] != 100 {
		t.Errorf("AboveMA20 = %v, want 100 — a suspension must not read as a breakdown", p.AboveMA20[last])
	}
}

// A newly listed stock has no MA20 yet; it must not be counted as "below its MA".
func TestBreadthPanelExcludesNewListings(t *testing.T) {
	n := 200
	closes := uniform(500, rising(n))
	newbies := make([][]float64, 300)
	for i := range newbies {
		s := make([]float64, n)
		for d := n - 10; d < n; d++ { // listed 10 sessions ago
			s[d] = 50 + float64(d-(n-10))
		}
		newbies[i] = s
	}
	p := BuildBreadthPanel(dates(n), append(closes, newbies...))
	last := n - 1

	if p.ValidCount[last] != 500 {
		t.Errorf("ValidCount = %d, want 500 — new listings have no MA20 to compare against", p.ValidCount[last])
	}
	if p.AboveMA20[last] != 100 {
		t.Errorf("AboveMA20 = %v, want 100", p.AboveMA20[last])
	}
}

func TestAnalyzeBreadthThinUniverseIsMissing(t *testing.T) {
	n := 200
	p := BuildBreadthPanel(dates(n), uniform(100, rising(n))) // 100 < MinBreadthUniverse 500
	ev, v := AnalyzeBreadth(p, n-1, false, th)

	if ev.Available {
		t.Error("100 measurable stocks is below the floor; evidence must be unavailable")
	}
	if ev.Direction != model.DirUnknown {
		t.Errorf("got %s, want UNKNOWN — a thin universe is not a neutral market", ev.Direction)
	}
	if v.Sufficient {
		t.Error("BreadthView.Sufficient must be false")
	}
	if ClassifyBreadth(v, false, th) != model.BreadthUnknown {
		t.Error("breadth quality must be UNKNOWN")
	}
}

func TestAnalyzeBreadthOutOfRange(t *testing.T) {
	p := BuildBreadthPanel(dates(200), uniform(600, rising(200)))
	for _, at := range []int{-1, 200, 999} {
		if ev, _ := AnalyzeBreadth(p, at, false, th); ev.Available {
			t.Errorf("index %d should yield missing evidence", at)
		}
	}
	if ev, _ := AnalyzeBreadth(nil, 0, false, th); ev.Available {
		t.Error("a nil panel should yield missing evidence")
	}
}

func TestDrop20MeasuresFallFromRecentPeak(t *testing.T) {
	s := make([]float64, 40)
	for i := range s {
		s[i] = 80
	}
	s[35] = 80
	s[39] = 55 // fell 25pp from the 20-session peak
	if got := drop20(s, 39); math.Abs(got-25) > 1e-9 {
		t.Errorf("drop20 = %v, want 25", got)
	}
	// At the peak itself the drop is zero, never negative.
	if got := drop20(s, 35); got != 0 {
		t.Errorf("drop20 at the peak = %v, want 0", got)
	}
}

func TestAnalyzeBreadthEvidenceShape(t *testing.T) {
	n := 200
	ev, v := AnalyzeBreadth(BuildBreadthPanel(dates(n), uniform(1900, rising(n))), n-1, false, th)

	if !ev.Available || !v.Sufficient {
		t.Fatalf("1900 stocks should be sufficient: %+v", ev)
	}
	if ev.Source != model.EvidenceBreadth {
		t.Errorf("Source = %q", ev.Source)
	}
	if ev.Direction != model.DirBullish {
		t.Errorf("100%% above MA20 should be BULLISH, got %s", ev.Direction)
	}
	if got, ok := ev.Metric(model.MetricBreadthMA20); !ok || got != 100 {
		t.Errorf("breadth metric missing or wrong: %v %v", got, ok)
	}
	if got, ok := ev.Metric(model.MetricBreadthValid); !ok || got != 1900 {
		t.Errorf("valid-count metric missing or wrong: %v %v", got, ok)
	}
	if ev.Confidence <= 0 || ev.Confidence > 1 {
		t.Errorf("confidence out of range: %v", ev.Confidence)
	}
}

// Each breadth measure must use its OWN denominator. A short-history stock has an MA20 but
// no MA120; if it landed in the MA120 denominator it could never reach the numerator, and
// AboveMA120 would be understated for every date — a bias that no rule reads today but that
// would silently corrupt the archived metrics future validation runs depend on.
func TestBreadthMeasuresUseIndependentDenominators(t *testing.T) {
	n := 200
	// 400 stocks with full history, all rising → above every MA.
	full := uniform(400, rising(n))
	// 400 stocks listed 70 sessions ago: they have an MA20 and an MA60, but no MA120.
	short := make([][]float64, 400)
	for i := range short {
		s := make([]float64, n)
		for d := n - 70; d < n; d++ {
			s[d] = 50 + float64(d-(n-70))
		}
		short[i] = s
	}
	p := BuildBreadthPanel(dates(n), append(full, short...))
	last := n - 1

	if p.ValidCount[last] != 800 {
		t.Fatalf("ValidCount = %d, want 800 (both cohorts have an MA20)", p.ValidCount[last])
	}
	if p.AboveMA20[last] != 100 {
		t.Errorf("AboveMA20 = %v, want 100", p.AboveMA20[last])
	}
	if p.AboveMA60[last] != 100 {
		t.Errorf("AboveMA60 = %v, want 100 (both cohorts have an MA60)", p.AboveMA60[last])
	}
	// Only the 400 full-history stocks have an MA120, and all of them are above it.
	// Diluting by the 800-stock MA20 population would give 50.
	if p.AboveMA120[last] != 100 {
		t.Errorf("AboveMA120 = %v, want 100 — short-history stocks must not dilute it", p.AboveMA120[last])
	}
}

// A measure computed from a handful of names must not be persisted as a number: 75% of 8
// stocks and 75% of 1,900 are indistinguishable once written to a snapshot. The measure is
// omitted and its population recorded, so the omission is explainable after the fact.
func TestThinPopulationSuppressesMetricButRecordsDenominator(t *testing.T) {
	n := 200
	// 600 stocks with full history (MA20/60/120 all measurable)...
	full := uniform(600, rising(n))
	// ...plus 900 that listed 30 sessions ago: they have an MA20 but no MA60/MA120.
	short := make([][]float64, 900)
	for i := range short {
		s := make([]float64, n)
		for d := n - 30; d < n; d++ {
			s[d] = 50 + float64(d-(n-30))
		}
		short[i] = s
	}
	p := BuildBreadthPanel(dates(n), append(full, short...))
	last := n - 1

	if p.Base20[last] != 1500 || p.Base120[last] != 600 {
		t.Fatalf("populations wrong: base20=%d base120=%d", p.Base20[last], p.Base120[last])
	}

	_, v := AnalyzeBreadth(p, last, false, th)
	if !v.Sufficient {
		t.Fatal("1500 stocks clears the MA20 floor")
	}
	// 600 >= MinBreadthUniverse (500) → still trustworthy, so it is written.
	if !v.Above120Known {
		t.Error("600 stocks clears the floor; MA120 should be reported")
	}

	// Now starve the MA120 population below the floor.
	few := uniform(100, rising(n))
	shortMany := make([][]float64, 1400)
	for i := range shortMany {
		s := make([]float64, n)
		for d := n - 30; d < n; d++ {
			s[d] = 50 + float64(d-(n-30))
		}
		shortMany[i] = s
	}
	p2 := BuildBreadthPanel(dates(n), append(few, shortMany...))
	_, v2 := AnalyzeBreadth(p2, last, false, th)

	if !v2.Sufficient {
		t.Fatal("1500 stocks still clears the MA20 floor")
	}
	if v2.Above120Known {
		t.Error("100 stocks is under the floor; MA120 must not be reported as known")
	}
	m := breadthMetrics(v2)
	if _, ok := m[model.MetricBreadthMA120]; ok {
		t.Error("a metric derived from 100 names must be OMITTED, not written")
	}
	if base, ok := m[model.MetricBreadthBase120]; !ok || base != 100 {
		t.Errorf("the denominator must be recorded so the omission is explainable: %v %v", base, ok)
	}
	// The headline measure is unaffected — it has its own, healthy population.
	if _, ok := m[model.MetricBreadthMA20]; !ok {
		t.Error("AboveMA20 has 1500 stocks behind it and must still be written")
	}
}

// MASlopeDays must actually be READ. It was promoted from a hard-coded 5 to a config
// threshold because layer 1 branches on it — but a knob nobody reads is worse than a
// constant, since it looks calibratable and is not. This drives the same series through
// two very different windows and requires the verdict to change.
func TestMASlopeDaysIsHonoured(t *testing.T) {
	// A series that rose for a long time and has just rolled over: the MA60 is still above
	// where it was 20 sessions ago, but below where it was yesterday.
	closes := make([]float64, 300)
	for i := 0; i < 280; i++ {
		closes[i] = 100 + 0.8*float64(i) // a long advance
	}
	for i := 280; i < 300; i++ {
		closes[i] = closes[279] - 3*float64(i-279) // a sharp 20-session roll-over
	}

	fast := th
	fast.MASlopeDays = 1 // yesterday vs today — sees the roll-over immediately
	slow := th
	slow.MASlopeDays = 40 // a long slope — still remembers the advance

	if maRising(closes, 60, fast.MASlopeDays) {
		t.Error("with a 1-session window the rolled-over MA60 must not read as rising")
	}
	if !maRising(closes, 60, slow.MASlopeDays) {
		t.Error("with a 40-session window the MA60 is still above its earlier level")
	}

	// And the knob must reach the classifier through AnalyzePrice, not just the helper.
	bars := make([]model.Bar, len(closes))
	for i, c := range closes {
		bars[i] = model.Bar{Date: fmt.Sprintf("d%04d", i), Close: c}
	}
	_, vFast := AnalyzePrice(model.Benchmark0050, bars, fast)
	_, vSlow := AnalyzePrice(model.Benchmark0050, bars, slow)
	if vFast.MA60Rising == vSlow.MA60Rising {
		t.Fatalf("MASlopeDays is not being read by AnalyzePrice: both gave MA60Rising=%v", vFast.MA60Rising)
	}
	// A zero value must fall back to the documented default rather than to "0 sessions ago".
	if maRising(closes, 60, 0) != maRising(closes, 60, 5) {
		t.Error("MASlopeDays=0 should fall back to the default of 5")
	}
}
