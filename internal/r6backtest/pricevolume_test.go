package r6backtest

import "testing"

// FLAT means EXACTLY zero here, the same definition the shipped label correction uses. If
// these drift apart the backtest answers a question nobody asked.
func TestPVFlatMeansExactlyZero(t *testing.T) {
	var flat PVCondition
	for _, c := range PVConditions() {
		if c.Name == "PV_FLAT_ANY_VOLUME" {
			flat = c
		}
	}
	if flat.Detect == nil {
		t.Fatal("PV_FLAT_ANY_VOLUME is missing")
	}
	if !flat.Detect(0, 1.0) {
		t.Error("an exactly-zero change must be FLAT")
	}
	for _, chg := range []float64{0.01, -0.01, 0.5, -0.5, 0.001, -0.001} {
		if flat.Detect(chg, 1.0) {
			t.Errorf("%v%% was treated as flat — the correction was exact-zero only", chg)
		}
	}
}

// The volume bands must be the SHIPPED ones, or the rows measure a grid the scanner does not use.
func TestPVVolumeBandsMatchTheScanner(t *testing.T) {
	if pvVolExpansion != 1.2 || pvVolShrink != 0.8 {
		t.Errorf("bands = %v / %v, want the live 1.2 / 0.8", pvVolExpansion, pvVolShrink)
	}
}

// The three candidate treatments must be measurable against each other, and the near-flat
// band the correction did NOT touch must be measurable too.
func TestPVConditionsCoverTheQuestion(t *testing.T) {
	names := map[string]bool{}
	for _, c := range PVConditions() {
		if c.Name == "" || c.Detect == nil {
			t.Errorf("malformed condition %q", c.Name)
		}
		if names[c.Name] {
			t.Errorf("duplicate %s", c.Name)
		}
		names[c.Name] = true
	}
	for _, want := range []string{
		"PV_BASELINE_ALL_BARS",
		"PV_FLAT_VOLUME_EXPANSION", "PV_DOWN_VOLUME_EXPANSION", "PV_UP_VOLUME_EXPANSION",
		"PV_FLAT_VOLUME_SHRINK", "PV_DOWN_VOLUME_SHRINK", "PV_UP_VOLUME_SHRINK",
		"PV_FLAT_ANY_VOLUME",
		"PV_NEAR_FLAT_DOWN_0_TO_05", "PV_NEAR_FLAT_UP_0_TO_05",
	} {
		if !names[want] {
			t.Errorf("missing %s", want)
		}
	}
}

// The three direction buckets must partition the grid: no bar may satisfy two of them, or
// comparing their forward returns would be meaningless.
func TestPVDirectionBucketsAreDisjoint(t *testing.T) {
	byName := map[string]PVCondition{}
	for _, c := range PVConditions() {
		byName[c.Name] = c
	}
	for _, half := range [][3]string{
		{"PV_FLAT_VOLUME_EXPANSION", "PV_DOWN_VOLUME_EXPANSION", "PV_UP_VOLUME_EXPANSION"},
		{"PV_FLAT_VOLUME_SHRINK", "PV_DOWN_VOLUME_SHRINK", "PV_UP_VOLUME_SHRINK"},
	} {
		for _, chg := range []float64{-5, -0.5, -0.01, 0, 0.01, 0.5, 5} {
			for _, vr := range []float64{0.3, 0.8, 1.0, 1.2, 2.5} {
				hits := 0
				for _, n := range half {
					if byName[n].Detect(chg, vr) {
						hits++
					}
				}
				if hits > 1 {
					t.Fatalf("chg=%v vol=%v satisfies %d of %v", chg, vr, hits, half)
				}
			}
		}
	}
}

// dayChangePct and dayVolRatio must read bar i and i-1 only — never ahead.
func TestPVInputsAreCausal(t *testing.T) {
	full := teStock("AAA", "族群", 200, 100, 0.004, 1000)
	i := 150
	truncated := teStock("AAA", "族群", i+1, 100, 0.004, 1000)

	a, okA := dayChangePct(full, i)
	b, okB := dayChangePct(truncated, i)
	if okA != okB || a != b {
		t.Errorf("dayChangePct reads ahead: %v/%v vs %v/%v", a, okA, b, okB)
	}
	va, okVA := dayVolRatio(full, i)
	vb, okVB := dayVolRatio(truncated, i)
	if okVA != okVB || va != vb {
		t.Errorf("dayVolRatio reads ahead: %v/%v vs %v/%v", va, okVA, vb, okVB)
	}
}

// No previous close, or no volume mean, means no signal — never a fabricated zero.
func TestPVInputsRefuseUnmeasurableBars(t *testing.T) {
	s := teStock("AAA", "族群", 60, 100, 0.004, 1000)
	if _, ok := dayChangePct(s, 0); ok {
		t.Error("bar 0 has no previous close")
	}
	if _, ok := dayChangePct(s, -1); ok {
		t.Error("a negative index must be refused")
	}
	if _, ok := dayChangePct(s, len(s.Close)); ok {
		t.Error("an out-of-range index must be refused")
	}
	// Warm-up: the 20-day volume mean is not published yet.
	if _, ok := dayVolRatio(s, 5); ok {
		t.Error("bar 5 has no 20-day volume mean")
	}
	// A setup must produce no trigger where its inputs are unavailable.
	for _, st := range PVSetups() {
		if st.Detect(nil, nil, s, 0, Params{}) != nil {
			t.Errorf("%s fired on bar 0", st.Name())
		}
	}
}

func TestPVSetupsWrapEveryCondition(t *testing.T) {
	if len(PVSetups()) != len(PVConditions()) {
		t.Fatalf("wrapped %d of %d", len(PVSetups()), len(PVConditions()))
	}
}
