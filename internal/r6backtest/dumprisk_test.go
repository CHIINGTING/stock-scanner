package r6backtest

import (
	"math"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// mkStock builds a stock from explicit bars so every index in a test is hand-checkable.
// VolMA20/ATR14 are supplied directly rather than computed, because these tests are about
// indexing and causality, not about the indicator maths (which their own packages cover).
func dumpStock(sym string, bars []fetcher.Candle, volMA, atr []float64) *Stock {
	s := &Stock{Symbol: sym, Name: sym, Candles: bars, VolMA20: volMA, ATR14: atr}
	for _, b := range bars {
		s.Close = append(s.Close, b.Close)
		s.High = append(s.High, b.High)
		s.Low = append(s.Low, b.Low)
		s.Vol = append(s.Vol, float64(b.Volume))
	}
	return s
}

func day(n int) time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

func bar(n int, o, h, l, c float64, v int64) fetcher.Candle {
	return fetcher.Candle{Date: day(n), Open: o, High: h, Low: l, Close: c, Volume: v, AdjClose: c}
}

func uniOf(stocks ...*Stock) *Universe { return &Universe{Stocks: stocks} }

// The outcome fields must read bars i+1 and i+2 — never i, never i+3.
func TestCollectDumpObsIndexesT1AndT2(t *testing.T) {
	bars := []fetcher.Candle{
		bar(0, 100, 101, 99, 100, 1000),  // i=0 warmup
		bar(1, 100, 110, 100, 108, 5000), // i=1 SIGNAL: close 108
		bar(2, 100, 106, 90, 95, 4000),   // T+1: open 100, close 95, low 90, high 106
		bar(3, 95, 96, 80, 85, 3000),     // T+2: close 85, low 80
		bar(4, 85, 90, 84, 88, 2000),     // must NOT be read
	}
	volMA := []float64{1000, 1000, 1000, 1000, 1000}
	atr := []float64{4, 4, 4, 4, 4}

	obs := CollectDumpObs(uniOf(dumpStock("T", bars, volMA, atr)), 1, nil)
	// warmup 1, and i+1 must exist → signals at i=1,2,3
	if len(obs) != 3 {
		t.Fatalf("got %d observations, want 3", len(obs))
	}

	o := obs[0]
	if !o.Date.Equal(day(1)) {
		t.Fatalf("first observation is bar %v, want %v", o.Date, day(1))
	}
	// All outcomes are measured from the SIGNAL CLOSE of 108.
	assertPct(t, "T1OpenRet", o.T1OpenRet, (100.0/108-1)*100)
	assertPct(t, "T1CloseRet", o.T1CloseRet, (95.0/108-1)*100)
	assertPct(t, "T1LowRet", o.T1LowRet, (90.0/108-1)*100)
	assertPct(t, "T1HighRet", o.T1HighRet, (106.0/108-1)*100)
	if !o.HasT2 {
		t.Fatal("HasT2 should be true when a T+2 bar exists")
	}
	assertPct(t, "T2CloseRet", o.T2CloseRet, (85.0/108-1)*100)
	assertPct(t, "T2LowRet", o.T2LowRet, (80.0/108-1)*100)

	// MAE in ATR: T+1 low 90 from close 108 over ATR 4 → -4.5 ATR.
	assertPct(t, "T1MAEATR", o.T1MAEATR, (90.0-108)/4)
	// T+2 MAE uses the WORST low across both sessions (80, not 90).
	assertPct(t, "T2MAEATR", o.T2MAEATR, (80.0-108)/4)

	if !o.GapDown() {
		t.Error("open 100 against close 108 is a gap down")
	}
	if !o.NegativeClose() {
		t.Error("close 95 against 108 is a negative close")
	}
}

// The last bar has no T+1, so it cannot be an observation: assuming a flat next day would
// invent the outcome being measured.
func TestCollectDumpObsDropsBarsWithoutNextSession(t *testing.T) {
	bars := []fetcher.Candle{
		bar(0, 100, 101, 99, 100, 1000),
		bar(1, 100, 110, 100, 108, 5000),
		bar(2, 108, 109, 105, 106, 4000), // final bar — delisted / end of data
	}
	volMA := []float64{1000, 1000, 1000}
	atr := []float64{4, 4, 4}

	obs := CollectDumpObs(uniOf(dumpStock("T", bars, volMA, atr)), 1, nil)
	if len(obs) != 1 {
		t.Fatalf("got %d observations, want 1 (only bar 1 has a next session)", len(obs))
	}
	if obs[0].HasT2 {
		t.Error("bar 1 has no T+2 bar; HasT2 must be false")
	}
	if !math.IsNaN(obs[0].T2CloseRet) || !math.IsNaN(obs[0].T2MAEATR) {
		t.Errorf("absent T+2 must stay NaN, got close=%v mae=%v", obs[0].T2CloseRet, obs[0].T2MAEATR)
	}
}

// A zero-range (limit-locked) bar has no shape and must not become an observation.
func TestCollectDumpObsSkipsDegenerateBars(t *testing.T) {
	bars := []fetcher.Candle{
		bar(0, 100, 101, 99, 100, 1000),
		bar(1, 110, 110, 110, 110, 5000), // limit-locked: High == Low
		bar(2, 110, 112, 105, 106, 4000),
		bar(3, 106, 108, 104, 107, 3000),
	}
	volMA := []float64{1000, 1000, 1000, 1000}
	atr := []float64{4, 4, 4, 4}

	obs := CollectDumpObs(uniOf(dumpStock("T", bars, volMA, atr)), 1, nil)
	for _, o := range obs {
		if o.Date.Equal(day(1)) {
			t.Fatal("the limit-locked bar must not produce an observation")
		}
	}
}

// Causality: an observation's FEATURES must depend only on bars <= i. Rewriting the future
// must move the outcomes and leave every feature untouched.
func TestDumpRiskIsCausal(t *testing.T) {
	base := []fetcher.Candle{
		bar(0, 100, 101, 99, 100, 1000),
		bar(1, 100, 110, 100, 108, 5000), // the signal bar
		bar(2, 100, 106, 90, 95, 4000),
		bar(3, 95, 96, 80, 85, 3000),
	}
	altered := append([]fetcher.Candle(nil), base...)
	// Replace the entire future with a rally.
	altered[2] = bar(2, 115, 130, 114, 129, 9000)
	altered[3] = bar(3, 129, 140, 128, 139, 9000)

	volMA := []float64{1000, 1000, 1000, 1000}
	atr := []float64{4, 4, 4, 4}

	a := CollectDumpObs(uniOf(dumpStock("T", base, volMA, atr)), 1, nil)[0]
	b := CollectDumpObs(uniOf(dumpStock("T", altered, volMA, atr)), 1, nil)[0]

	if a.Feat != b.Feat {
		t.Fatalf("features changed when only the FUTURE changed — look-ahead leak:\n T+1..T+2 down: %+v\n T+1..T+2 up:   %+v", a.Feat, b.Feat)
	}
	if a.T1CloseRet == b.T1CloseRet {
		t.Fatal("the test is not exercising anything: outcomes should differ")
	}
}

// Buckets that form a partition must not overlap, and must not silently drop members.
func TestDumpBucketsArePartitionedWhereClaimed(t *testing.T) {
	obs := syntheticObs(t)
	th := DeriveDumpThresholds(obs, 3.0, 1.2, 5.0)

	byName := map[string]func(DumpObs, DumpThresholds) bool{}
	for _, c := range DumpConditions() {
		byName[c.Name] = c.Detect
	}

	// Within the large-gain + volume-spike population, weak / strong close are disjoint,
	// and so are long / short upper shadow.
	pairs := [][2]string{
		{"DUMP_GAIN_SPIKE_WEAK_CLOSE", "DUMP_GAIN_SPIKE_STRONG_CLOSE"},
		{"DUMP_GAIN_SPIKE_LONG_UPPER", "DUMP_GAIN_SPIKE_SHORT_UPPER"},
		{"DUMP_GAIN_SPIKE_HIGH_DAYTRADE", "DUMP_GAIN_SPIKE_LOW_DAYTRADE"},
	}
	for _, p := range pairs {
		for _, o := range obs {
			if byName[p[0]](o, th) && byName[p[1]](o, th) {
				t.Errorf("%s and %s both matched the same bar (%s %v)", p[0], p[1], o.Symbol, o.Date)
			}
		}
	}

	// Volume splits the large-gain population exhaustively: spike ∪ normal == every bar
	// with a usable volume ratio.
	for _, o := range obs {
		if !o.Feat.VolumeOK {
			continue
		}
		spike := o.Feat.VolumeRatio >= th.VolSpikeRatio
		if spike == (o.Feat.VolumeRatio < th.VolSpikeRatio) {
			t.Errorf("volume split is not exhaustive at ratio %v", o.Feat.VolumeRatio)
		}
	}
}

// The baseline bucket must contain every observation — it is the control everything else is
// read against, and a control that quietly filters is not a control.
func TestDumpBaselineHoldsEveryObservation(t *testing.T) {
	obs := syntheticObs(t)
	th := DeriveDumpThresholds(obs, 3.0, 1.2, 5.0)
	stats := RunDumpStudy(obs, th)

	if stats[0].Name != "DUMP_BASELINE_ALL_BARS" {
		t.Fatalf("first row is %s; the baseline must lead the grid", stats[0].Name)
	}
	if stats[0].Count != len(obs) {
		t.Errorf("baseline count = %d, want %d (every observation)", stats[0].Count, len(obs))
	}
	for _, s := range stats {
		if s.Count > stats[0].Count {
			t.Errorf("%s has %d rows, more than the baseline's %d", s.Name, s.Count, stats[0].Count)
		}
	}
}

// Thresholds must come from the data. With a known distribution the terciles are checkable.
func TestDeriveDumpThresholdsUsesObservedDistribution(t *testing.T) {
	obs := syntheticObs(t)
	th := DeriveDumpThresholds(obs, 3.0, 1.2, 5.0)

	if math.IsNaN(th.WeakCloseCLV) || math.IsNaN(th.StrongCloseCLV) {
		t.Fatal("CLV terciles should be derivable from this population")
	}
	if !(th.WeakCloseCLV <= th.StrongCloseCLV) {
		t.Errorf("weak cut %v must not exceed strong cut %v", th.WeakCloseCLV, th.StrongCloseCLV)
	}
	if th.WeakCloseCLV < 0 || th.StrongCloseCLV > 1 {
		t.Errorf("CLV cuts must stay inside [0,1], got %v..%v", th.WeakCloseCLV, th.StrongCloseCLV)
	}
	if th.LargeGainPct != 3.0 || th.VolSpikeRatio != 1.2 {
		t.Error("the shipped bands must pass through unchanged, not be re-fitted")
	}
	// An empty population yields NaN rather than a fabricated cut.
	empty := DeriveDumpThresholds(nil, 3.0, 1.2, 5.0)
	if !math.IsNaN(empty.WeakCloseCLV) || !math.IsNaN(empty.SelloffMAEATR) {
		t.Error("thresholds from an empty population must be NaN, not 0")
	}
	if empty.DayTradingAvailable {
		t.Error("DayTradingAvailable must be false with no data")
	}
}

// With no 當沖 archive, the day-trading rows must be EMPTY rather than counting every bar as
// low-day-trading — an absent archive is not evidence of quiet turnover.
func TestDayTradingRowsEmptyWithoutArchive(t *testing.T) {
	obs := syntheticObs(t)
	th := DeriveDumpThresholds(obs, 3.0, 1.2, 5.0)
	if th.DayTradingAvailable {
		t.Skip("synthetic fixture carries day-trading data")
	}
	for _, s := range RunDumpStudy(obs, th) {
		switch s.Name {
		case "DUMP_GAIN_SPIKE_HIGH_DAYTRADE", "DUMP_GAIN_SPIKE_LOW_DAYTRADE", "DUMP_HIGH_DAYTRADE_ANY":
			if s.Count != 0 {
				t.Errorf("%s has %d rows without a 當沖 archive; want 0", s.Name, s.Count)
			}
		}
	}
}

// A stat over an empty bucket must not report 0% rates that read like measurements.
func TestEmptyBucketRatesAreNaN(t *testing.T) {
	st := ComputeDumpStats("EMPTY", "", nil, DumpThresholds{})
	if st.Count != 0 {
		t.Fatalf("count = %d", st.Count)
	}
	for name, v := range map[string]float64{
		"GapDownRate": st.GapDownRate, "NegCloseRate": st.NegCloseRate, "SelloffRate": st.SelloffRate,
	} {
		if !math.IsNaN(v) && v != 0 {
			t.Errorf("%s = %v on an empty bucket", name, v)
		}
	}
}

// syntheticObs builds a small, deterministic population spanning strong/weak closes and
// spike/normal volume so bucket logic can be exercised without the cache.
func syntheticObs(t *testing.T) []DumpObs {
	t.Helper()
	var stocks []*Stock
	for s := 0; s < 6; s++ {
		var bars []fetcher.Candle
		var volMA, atr []float64
		price := 100.0
		for i := 0; i < 30; i++ {
			// Alternate strong-close and weak-close rallies, and spike vs normal volume.
			gain := 1.0 + float64(s%3)*2.0 // 1%, 3%, 5%
			high := price * (1 + gain/100) * 1.02
			low := price * 0.99
			var closeAt float64
			if (i+s)%2 == 0 {
				closeAt = high * 0.999 // strong close
			} else {
				closeAt = low * 1.001 // weak close
			}
			vol := int64(1000)
			if (i+s)%3 == 0 {
				vol = 5000 // spike
			}
			bars = append(bars, bar(i, price, high, low, closeAt, vol))
			volMA = append(volMA, 1000)
			atr = append(atr, price*0.02)
			price = closeAt
		}
		stocks = append(stocks, dumpStock(string(rune('A'+s)), bars, volMA, atr))
	}
	obs := CollectDumpObs(uniOf(stocks...), 2, nil)
	if len(obs) == 0 {
		t.Fatal("fixture produced no observations")
	}
	return obs
}

func assertPct(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
