package institution

import "testing"

func hasSignal(sigs []Signal, cat, typ string) bool {
	for _, s := range sigs {
		if s.Category == cat && s.Type == typ {
			return true
		}
	}
	return false
}

func TestClassifyContinuousBuying(t *testing.T) {
	// [-1, +100, +200, +300] → buy streak 3 with a NATURAL end at the leading negative.
	d := []string{"d0", "d1", "d2", "d3"}
	recs := mkRecords(d, allTrue(4), []int64{-1, 100, 200, 300})
	v := Compute("2330", "x", "d3", d, recs, 0, true)
	sigs := Classify(v, DefaultSignalThresholds())
	if !hasSignal(sigs, CategoryTrust, SignalContinuousBuying) {
		t.Errorf("want CONTINUOUS_BUYING for trust; got %+v", sigs)
	}
}

func TestClassifyIncompleteStreakSuppressed(t *testing.T) {
	// Gap right before a 2-day buy run → streak incomplete → NO CONTINUOUS signal.
	d := []string{"d1", "d2", "d3"}
	present := []bool{false, true, true}
	recs := mkRecords(d, present, []int64{0, 100, 200})
	v := Compute("2330", "x", "d3", d, recs, 0, true)
	// force ContinuousDays=2 so count alone would trigger if completeness were ignored
	sigs := Classify(v, SignalThresholds{ContinuousDays: 2})
	if hasSignal(sigs, CategoryTrust, SignalContinuousBuying) {
		t.Errorf("incomplete streak must NOT emit CONTINUOUS_BUYING; got %+v", sigs)
	}
}

func TestClassifyTransition(t *testing.T) {
	d := []string{"d1", "d2"}
	recs := mkRecords(d, allTrue(2), []int64{100, -200})
	v := Compute("2330", "x", "d2", d, recs, 0, true)
	sigs := Classify(v, DefaultSignalThresholds())
	if !hasSignal(sigs, CategoryForeign, SignalBuyToSell) {
		t.Errorf("want BUY_TO_SELL; got %+v", sigs)
	}
}
