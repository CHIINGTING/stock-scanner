package scanner

import (
	"math"
	"strings"
	"testing"
)

func pmClose(chgPct float64) []float64 { return []float64{100, 100 * (1 + chgPct/100)} }

// The problem this whole file exists to fix: +0.2% and +5.0% used to be the same
// "價漲量縮", and −0.2% and −5.0% the same "價跌量縮". All eight required combinations must
// now be distinguishable.
func TestEightCasesAreDistinguishable(t *testing.T) {
	th := DefaultPriceMoveThresholds()
	seen := map[string][]string{}

	for _, tc := range []struct{ chg, vr float64 }{
		{0.2, 0.6}, {5.0, 0.6}, {-0.2, 0.6}, {-5.0, 0.6},
		{0.2, 1.8}, {5.0, 1.8}, {-0.2, 1.8}, {-5.0, 1.8},
	} {
		got := classifyPriceMove(pmClose(tc.chg), 2.0, tc.vr, th)
		key := got.state
		label := ""
		switch {
		case tc.chg > 0 && tc.vr >= 1.2:
			label = "價漲量增"
		case tc.chg > 0:
			label = "價漲量縮"
		case tc.vr >= 1.2:
			label = "價跌量增"
		default:
			label = "價跌量縮"
		}
		seen[key] = append(seen[key], label)
	}

	// The legacy vocabulary collapses these into four buckets; the new state must not.
	if len(seen) < 6 {
		t.Errorf("only %d distinct states for 8 cases: %v", len(seen), seen)
	}
	// Specifically: a small drop and a large drop on thin volume must differ.
	small := classifyPriceMove(pmClose(-0.2), 2.0, 0.6, th)
	large := classifyPriceMove(pmClose(-5.0), 2.0, 0.6, th)
	if small.state == large.state {
		t.Errorf("−0.2%% and −5.0%% on thin volume both read %q — the whole point is lost", small.state)
	}
	if large.move != MoveLargeDown {
		t.Errorf("−5.0%% = %s, want LARGE_DOWN", large.move)
	}
	// And a big drop on thin volume must NOT be expressible as merely "quiet".
	if !strings.HasPrefix(large.state, MoveLargeDown) {
		t.Errorf("state %q does not lead with the magnitude", large.state)
	}
}

func TestMoveClassBoundaries(t *testing.T) {
	th := DefaultPriceMoveThresholds() // flat 0.5, large 3.0
	for _, tc := range []struct {
		chg  float64
		want string
	}{
		{0, MoveFlat},
		{0.5, MoveFlat},  // inclusive at the flat edge
		{-0.5, MoveFlat}, //
		{0.51, MoveSmallUp},
		{-0.51, MoveSmallDown},
		{2.99, MoveSmallUp},
		{3.0, MoveLargeUp}, // inclusive at the large edge
		{-3.0, MoveLargeDown},
		{-2.99, MoveSmallDown},
		{9.8, MoveLargeUp},
		{-9.8, MoveLargeDown},
	} {
		if got := moveClass(tc.chg, th); got != tc.want {
			t.Errorf("moveClass(%v) = %s, want %s", tc.chg, got, tc.want)
		}
	}
}

// The volume bands must be the SAME ones the scoring already uses, or the label and the
// score would disagree about what "量增" means.
func TestVolumeStateReusesScoringBands(t *testing.T) {
	th := DefaultPriceMoveThresholds()
	if th.VolExpansionRatio != 1.2 {
		t.Errorf("expansion band = %v, want analyzeVolume's 1.2", th.VolExpansionRatio)
	}
	if th.VolShrinkRatio != 0.8 {
		t.Errorf("shrink band = %v, want the score table's 0.8", th.VolShrinkRatio)
	}
	for _, tc := range []struct {
		ratio float64
		want  string
	}{
		{1.8, VolExpansion}, {1.2, VolExpansion},
		{1.19, VolNormalTag}, {0.81, VolNormalTag},
		{0.8, VolShrink}, {0.3, VolShrink},
		{0, MoveUnknown}, // no volume MA is not "thin trading"
	} {
		if got := volumeState(tc.ratio, th); got != tc.want {
			t.Errorf("volumeState(%v) = %s, want %s", tc.ratio, got, tc.want)
		}
	}
}

// MISSING ≠ ZERO: a stock with no usable previous close has not "moved 0%".
func TestInvalidInputYieldsUnknownNotZero(t *testing.T) {
	th := DefaultPriceMoveThresholds()
	for name, closes := range map[string][]float64{
		"no bars":        nil,
		"single bar":     {100},
		"zero prevClose": {0, 100},
		"negative prev":  {-5, 100},
		"NaN prev":       {math.NaN(), 100},
		"Inf last":       {100, math.Inf(1)},
	} {
		got := classifyPriceMove(closes, 2.0, 1.0, th)
		if got.move != MoveUnknown {
			t.Errorf("%s: move = %s, want UNKNOWN", name, got.move)
		}
		if got.changePct != 0 || got.changeATR != 0 {
			t.Errorf("%s: published %v%% / %v ATR for an unmeasurable session", name, got.changePct, got.changeATR)
		}
		// The combined state is unmeasurable too — never a half-formed "UNKNOWN_VOLUME_*".
		if got.state != MoveUnknown {
			t.Errorf("%s: state = %q, want a plain UNKNOWN", name, got.state)
		}
	}
	// A genuine unchanged session IS measurable and is FLAT, not UNKNOWN — the two must
	// stay distinguishable.
	real0 := classifyPriceMove([]float64{100, 100}, 2.0, 1.0, th)
	if real0.move != MoveFlat {
		t.Errorf("a real 0.0%% session = %s, want FLAT", real0.move)
	}
}

func TestPriceChangeATR(t *testing.T) {
	th := DefaultPriceMoveThresholds()
	// Price 100, ATR 2.0 → atrPct 2%. A +5% move ≈ 2.5 ATR (computed against the CLOSING
	// price, hence slightly under).
	got := classifyPriceMove(pmClose(5), 2.0, 1.0, th)
	if got.changeATR < 2.3 || got.changeATR > 2.7 {
		t.Errorf("+5%% with a 2%% ATR = %v ATR, want ~2.5", got.changeATR)
	}
	// The same percentage on a MORE volatile stock is worth fewer ATRs — the whole reason
	// this number exists.
	volatile := classifyPriceMove(pmClose(5), 10.0, 1.0, th)
	if volatile.changeATR >= got.changeATR {
		t.Errorf("a 5%% move should be worth fewer ATRs on a volatile stock: %v vs %v",
			volatile.changeATR, got.changeATR)
	}
	// Unavailable ATR must not become a division artefact.
	for _, atr := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if v := classifyPriceMove(pmClose(5), atr, 1.0, th); v.changeATR != 0 {
			t.Errorf("ATR %v gave %v", atr, v.changeATR)
		}
	}
	// The sign follows the move.
	if down := classifyPriceMove(pmClose(-5), 2.0, 1.0, th); down.changeATR >= 0 {
		t.Errorf("a down move gave %v ATR, want negative", down.changeATR)
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	th := DefaultPriceMoveThresholds()
	c := pmClose(3.7)
	if classifyPriceMove(c, 2.0, 1.4, th) != classifyPriceMove(c, 2.0, 1.4, th) {
		t.Error("same input gave different results")
	}
}

// A measurable move with no volume measurement is still worth stating: we know the move,
// we do not know the volume, and the state says exactly that.
func TestKnownMoveWithUnknownVolume(t *testing.T) {
	got := classifyPriceMove(pmClose(5), 2.0, 0, DefaultPriceMoveThresholds())
	if got.move != MoveLargeUp {
		t.Errorf("move = %s, want LARGE_UP", got.move)
	}
	if got.state != MoveLargeUp+"_"+MoveUnknown {
		t.Errorf("state = %q, want LARGE_UP_UNKNOWN", got.state)
	}
}
