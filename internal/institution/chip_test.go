package institution

import "testing"

// mkRecords builds a records map keyed by date labels; present[i]==false means a GAP
// (no institution record that day). All four categories share the same net for brevity.
func mkRecords(dates []string, present []bool, nets []int64) map[string]CategoryNets {
	m := map[string]CategoryNets{}
	for i, d := range dates {
		if present[i] {
			m[d] = CategoryNets{Foreign: nets[i], Trust: nets[i], Dealer: nets[i], Total: nets[i]}
		}
	}
	return m
}

func allTrue(n int) []bool {
	b := make([]bool, n)
	for i := range b {
		b[i] = true
	}
	return b
}

func computeTotal(dates []string, present []bool, nets []int64, vol int64) LegStats {
	recs := mkRecords(dates, present, nets)
	v := Compute("2330", "台積電", dates[len(dates)-1], dates, recs, vol, true)
	return v.Total
}

func TestConsecutiveBuy(t *testing.T) {
	d := []string{"d1", "d2", "d3"}
	st := computeTotal(d, allTrue(3), []int64{100, 200, 300}, 0)
	if st.ConsecutiveBuyDays != 3 {
		t.Errorf("ConsecutiveBuyDays = %d, want 3", st.ConsecutiveBuyDays)
	}
	if st.ConsecutiveSellDays != 0 {
		t.Errorf("ConsecutiveSellDays = %d, want 0", st.ConsecutiveSellDays)
	}
}

func TestConsecutiveSell(t *testing.T) {
	d := []string{"d1", "d2", "d3"}
	st := computeTotal(d, allTrue(3), []int64{-100, -200, -300}, 0)
	if st.ConsecutiveSellDays != 3 {
		t.Errorf("ConsecutiveSellDays = %d, want 3", st.ConsecutiveSellDays)
	}
}

func TestTransitionBuyToSell(t *testing.T) {
	d := []string{"d1", "d2"}
	st := computeTotal(d, allTrue(2), []int64{100, -200}, 0)
	if st.Transition != TransitionBuyToSell {
		t.Errorf("Transition = %q, want BUY_TO_SELL", st.Transition)
	}
}

func TestTransitionSellToBuy(t *testing.T) {
	d := []string{"d1", "d2"}
	st := computeTotal(d, allTrue(2), []int64{-100, 200}, 0)
	if st.Transition != TransitionSellToBuy {
		t.Errorf("Transition = %q, want SELL_TO_BUY", st.Transition)
	}
}

func TestZeroResetsStreak(t *testing.T) {
	// +1000, 0, -800 → transition NONE (prev=0), buy=0, sell=1.
	d := []string{"d1", "d2", "d3"}
	st := computeTotal(d, allTrue(3), []int64{1000, 0, -800}, 0)
	if st.Transition != TransitionNone {
		t.Errorf("Transition = %q, want NONE (zero must not bridge)", st.Transition)
	}
	if st.ConsecutiveBuyDays != 0 {
		t.Errorf("ConsecutiveBuyDays = %d, want 0", st.ConsecutiveBuyDays)
	}
	if st.ConsecutiveSellDays != 1 {
		t.Errorf("ConsecutiveSellDays = %d, want 1", st.ConsecutiveSellDays)
	}
}

func TestGapBreaksStreak(t *testing.T) {
	// +1000, <missing>, +2000 → count only latest, StreakComplete=false (§10).
	d := []string{"d1", "d2", "d3"}
	present := []bool{true, false, true}
	st := computeTotal(d, present, []int64{1000, 0, 2000}, 0)
	if st.ConsecutiveBuyDays != 1 {
		t.Errorf("ConsecutiveBuyDays = %d, want 1 (gap must not bridge)", st.ConsecutiveBuyDays)
	}
	if st.StreakComplete {
		t.Errorf("StreakComplete must be false across a real gap")
	}
}

func TestNetBuyRatio(t *testing.T) {
	d := []string{"d1"}
	st := computeTotal(d, allTrue(1), []int64{1000}, 10000)
	if st.NetBuyRatio == nil || *st.NetBuyRatio != 10.0 {
		t.Fatalf("NetBuyRatio = %v, want 10.0", st.NetBuyRatio)
	}
}

func TestNetBuyRatioZeroVolume(t *testing.T) {
	d := []string{"d1"}
	st := computeTotal(d, allTrue(1), []int64{1000}, 0)
	if st.NetBuyRatio != nil {
		t.Fatalf("NetBuyRatio must be nil when volume<=0, got %v", *st.NetBuyRatio)
	}
}

func TestCumulativeAndCompleteness(t *testing.T) {
	// 5 expected days, day d3 missing → Sum5d spans a gap → incomplete; ObservedDays=4.
	d := []string{"d1", "d2", "d3", "d4", "d5"}
	present := []bool{true, true, false, true, true}
	recs := mkRecords(d, present, []int64{10, 20, 0, 40, 50})
	v := Compute("2330", "x", "d5", d, recs, 0, true)
	if v.Completeness.ExpectedDays != 5 || v.Completeness.ObservedDays != 4 {
		t.Errorf("completeness = %+v", v.Completeness)
	}
	if len(v.Completeness.MissingDates) != 1 || v.Completeness.MissingDates[0] != "d3" {
		t.Errorf("MissingDates = %v", v.Completeness.MissingDates)
	}
	if !v.Completeness.DataComplete {
		t.Errorf("DataComplete should be true (d5 present)")
	}
	if v.Total.CumulativeComplete[5] {
		t.Errorf("Sum5d must be incomplete (spans the d3 gap)")
	}
	// window of 3 = last 3 = d3,d4,d5; d3 missing → also incomplete
	if v.Total.CumulativeComplete[3] {
		t.Errorf("Sum3d spans d3 gap → should be incomplete")
	}
}

func TestTodayMissingStreakIncomplete(t *testing.T) {
	d := []string{"d1", "d2"}
	present := []bool{true, false} // today missing
	st := computeTotal(d, present, []int64{100, 0}, 1000)
	if st.StreakComplete {
		t.Errorf("today missing → StreakComplete must be false")
	}
	if st.NetBuyRatio != nil {
		t.Errorf("today missing → NetBuyRatio must be nil")
	}
}
