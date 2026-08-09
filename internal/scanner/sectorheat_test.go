package scanner

import (
	"math"
	"testing"
)

// heatMembers builds n members: `above` of them above their MA20, `positive` with a positive
// 20-day return, `volume` with an expanded volume ratio.
func heatMembers(n, above, positive, volume int, returns ...float64) []HeatMemberInput {
	out := make([]HeatMemberInput, n)
	for i := range out {
		out[i] = HeatMemberInput{MA20Valid: true, AboveMA20: i < above}
		if i < positive {
			out[i].Return20 = 5
		} else {
			out[i].Return20 = -5
		}
		if i < volume {
			out[i].VolumeRatio = volumeConfirmRatio + 0.1
		} else {
			out[i].VolumeRatio = 0.5
		}
	}
	for i, r := range returns {
		if i < len(out) {
			out[i].Return20 = r
		}
	}
	return out
}

func TestSectorHeatComponents(t *testing.T) {
	// 8 members: 6 above MA20, 4 positive, 2 with volume expansion.
	h := ComputeSectorHeat(10, heatMembers(8, 6, 4, 2))

	if h.Status != SectorHeatOK {
		t.Fatalf("status = %s, want OK", h.Status)
	}
	if h.Breadth != 75 {
		t.Errorf("breadth = %v, want 75 (6/8)", h.Breadth)
	}
	if h.Participation != 50 {
		t.Errorf("participation = %v, want 50 (4/8)", h.Participation)
	}
	if h.VolumeConfirmation != 25 {
		t.Errorf("volume = %v, want 25 (2/8)", h.VolumeConfirmation)
	}
	// Coverage exposes what was dropped: 8 measurable out of 10 configured.
	if h.MemberCount != 10 || h.ValidMemberCount != 8 || h.Coverage != 80 {
		t.Errorf("sample disclosure wrong: %d/%d cov=%v", h.ValidMemberCount, h.MemberCount, h.Coverage)
	}
}

// The floor exists so a two-stock "sector" cannot produce an authoritative-looking score.
func TestSectorHeatInsufficientMembers(t *testing.T) {
	for n := 0; n < MinHeatMembers; n++ {
		h := ComputeSectorHeat(n, heatMembers(n, n, n, n))
		if h.Status != SectorHeatInsufficientData {
			t.Errorf("%d members: status = %s, want INSUFFICIENT_DATA", n, h.Status)
		}
		if h.Heat != 0 || h.Breadth != 0 {
			t.Errorf("%d members: components must stay zeroed, got heat=%v breadth=%v", n, h.Heat, h.Breadth)
		}
		// The denominators are still reported — "we could not measure" is itself information.
		if h.ValidMemberCount != n {
			t.Errorf("%d members: ValidMemberCount = %d", n, h.ValidMemberCount)
		}
	}
	// Exactly at the floor it computes.
	if h := ComputeSectorHeat(MinHeatMembers, heatMembers(MinHeatMembers, 2, 2, 2)); h.Status != SectorHeatOK {
		t.Errorf("%d members should compute, got %s", MinHeatMembers, h.Status)
	}
}

// A sector whose members mostly lack MA20 must measure breadth against its OWN denominator,
// not count the unmeasurable ones as "not above".
func TestSectorHeatBreadthDenominator(t *testing.T) {
	members := []HeatMemberInput{
		{MA20Valid: true, AboveMA20: true, Return20: 3, VolumeRatio: 1},
		{MA20Valid: true, AboveMA20: true, Return20: 3, VolumeRatio: 1},
		{MA20Valid: false, Return20: 3, VolumeRatio: 1}, // no MA20 yet
		{MA20Valid: false, Return20: 3, VolumeRatio: 1},
	}
	h := ComputeSectorHeat(4, members)
	if h.Breadth != 100 {
		t.Errorf("breadth = %v, want 100 — only the 2 measurable members count", h.Breadth)
	}
}

// The whole reason for medians: one runaway member must not define the sector.
func TestSectorHeatMedianResistsOutlier(t *testing.T) {
	normal := heatMembers(6, 3, 3, 3, 1, 1, 1, -1, -1, -1)
	spiked := heatMembers(6, 3, 3, 3, 300, 1, 1, -1, -1, -1) // one +300% member

	a := ComputeSectorHeat(6, normal)
	b := ComputeSectorHeat(6, spiked)
	if a.MedianReturn20 != b.MedianReturn20 {
		t.Errorf("a single outlier moved the sector return: %v → %v", a.MedianReturn20, b.MedianReturn20)
	}
	// A mean would have moved from 0 to ~50 here; assert that explicitly so the intent is
	// visible if someone swaps median() back for an average.
	var sum float64
	for _, m := range spiked {
		sum += m.Return20
	}
	if mean := sum / float64(len(spiked)); math.Abs(mean-b.MedianReturn20) < 10 {
		t.Errorf("median and mean are too close to prove robustness (mean=%v median=%v)", mean, b.MedianReturn20)
	}
}

// An abnormal single volume spike cannot dominate: the component is a member RATIO, so a
// 50× volume on one stock counts exactly as much as a 1.6× on another.
func TestSectorHeatVolumeOutlierCannotDominate(t *testing.T) {
	members := heatMembers(8, 4, 4, 1)
	members[0].VolumeRatio = 50 // absurd single-name spike
	h := ComputeSectorHeat(8, members)
	if h.VolumeConfirmation != 12.5 {
		t.Errorf("volume = %v, want 12.5 (1/8) — a spike must not scale the component", h.VolumeConfirmation)
	}
}

func TestNormalizeSectorHeatCrossSectional(t *testing.T) {
	mk := func(name string, med float64, status string) SectorRotation {
		return SectorRotation{Name: name, Heat: &SectorHeatView{
			Status: status, MedianReturn20: med,
			Breadth: 50, Participation: 50, VolumeConfirmation: 50,
			ValidMemberCount: 6, MemberCount: 6,
		}}
	}
	results := []SectorRotation{
		mk("hot", 12, SectorHeatOK),
		mk("mid", 3, SectorHeatOK),
		mk("cold", -8, SectorHeatOK),
		mk("thin", 99, SectorHeatInsufficientData), // must not enter the ranking population
	}
	NormalizeSectorHeat(results)

	if results[0].Heat.RelativeMomentum <= results[1].Heat.RelativeMomentum ||
		results[1].Heat.RelativeMomentum <= results[2].Heat.RelativeMomentum {
		t.Errorf("momentum ranking is not ordered: %v %v %v",
			results[0].Heat.RelativeMomentum, results[1].Heat.RelativeMomentum, results[2].Heat.RelativeMomentum)
	}
	if results[0].Heat.Heat <= results[2].Heat.Heat {
		t.Error("the hot sector must end with a higher composite heat than the cold one")
	}
	// An unmeasurable sector stays unmeasured — no Heat, no codes, and it did not tilt the
	// ranking of the ones that could be measured.
	thin := results[3].Heat
	if thin.Heat != 0 || thin.RelativeMomentum != 0 || len(thin.Codes) != 0 {
		t.Errorf("INSUFFICIENT_DATA sector was scored anyway: %+v", thin)
	}
}

// A single measurable sector cannot be ranked against peers; 50 is the honest answer.
func TestNormalizeSectorHeatSingleSector(t *testing.T) {
	results := []SectorRotation{{Name: "only", Heat: &SectorHeatView{
		Status: SectorHeatOK, MedianReturn20: 5, Breadth: 80, Participation: 80,
		VolumeConfirmation: 80, ValidMemberCount: 6, MemberCount: 6,
	}}}
	NormalizeSectorHeat(results)
	if results[0].Heat.RelativeMomentum != 50 {
		t.Errorf("single sector momentum = %v, want 50", results[0].Heat.RelativeMomentum)
	}
}

func TestSectorHeatCodes(t *testing.T) {
	strong := &SectorHeatView{Heat: 80, Participation: 70, VolumeConfirmation: 60}
	codes := heatCodes(strong)
	want := map[string]bool{
		CodeSectorHeatStrong: true, CodeSectorParticipationStrong: true, CodeSectorVolumeConfirm: true,
	}
	for _, c := range codes {
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing codes %v (got %v)", want, codes)
	}

	weak := heatCodes(&SectorHeatView{Heat: 20, Participation: 10, VolumeConfirmation: 10})
	if len(weak) != 1 || weak[0] != CodeSectorHeatWeak {
		t.Errorf("weak sector codes = %v, want just %s", weak, CodeSectorHeatWeak)
	}
}

func TestMedian(t *testing.T) {
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("odd median = %v, want 2", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("even median = %v, want 2.5", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("empty median = %v, want 0", got)
	}
}
