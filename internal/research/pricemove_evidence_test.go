package research

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func pmEntryFor(sig, move, state string, chg, atr float64) scanner.WatchlistEntry {
	e := bareEntry("2330")
	e.A.PriceVolumeSignal = sig
	e.A.PriceMove = move
	e.A.PriceVolumeState = state
	e.A.PriceChangePct = chg
	e.A.PriceChangeATR = atr
	return e
}

// The magnitude must reach the evidence store, or the agents reading it are back to seeing
// only the direction.
func TestMagnitudeReachesTheEvidenceStore(t *testing.T) {
	rec, _ := openRecorder(t)
	ev := recordWithTechnical(t, rec, pmEntryFor(
		"價跌量縮", scanner.MoveLargeDown, "LARGE_DOWN_VOLUME_SHRINK", -6.2, -2.4))

	if got := ev["price_change_pct"]; got.NumValue == nil || *got.NumValue != -6.2 {
		t.Errorf("price_change_pct = %v, want -6.2", got.NumValue)
	}
	if got := ev["price_move"]; got.TextValue == nil || *got.TextValue != scanner.MoveLargeDown {
		t.Errorf("price_move = %v, want LARGE_DOWN", got.TextValue)
	}
	if got := ev["price_volume_state"]; got.TextValue == nil || *got.TextValue != "LARGE_DOWN_VOLUME_SHRINK" {
		t.Errorf("price_volume_state = %v", got.TextValue)
	}
	if got := ev["price_change_atr"]; got.NumValue == nil || *got.NumValue != -2.4 {
		t.Errorf("price_change_atr = %v, want -2.4", got.NumValue)
	}
	// The legacy signal is still stored alongside — historical rows stay comparable.
	if got := ev["price_volume_signal"]; got.TextValue == nil || *got.TextValue != "價跌量縮" {
		t.Errorf("the legacy signal was dropped from the store: %v", got.TextValue)
	}
	// Machine-readable: numbers as numbers.
	for _, k := range []string{"price_change_pct", "price_change_atr"} {
		if ev[k].NumValue == nil {
			t.Errorf("%s was stored as text, want a number", k)
		}
	}
}

// A small drop and a large drop must be different ROWS, not just different prose.
func TestSmallAndLargeDropsAreDifferentEvidence(t *testing.T) {
	recA, _ := openRecorder(t)
	small := recordWithTechnical(t, recA, pmEntryFor(
		"價跌量縮", scanner.MoveFlat, "FLAT_VOLUME_SHRINK", -0.35, -0.2))
	recB, _ := openRecorder(t)
	large := recordWithTechnical(t, recB, pmEntryFor(
		"價跌量縮", scanner.MoveLargeDown, "LARGE_DOWN_VOLUME_SHRINK", -6.2, -2.4))

	// Same legacy label …
	if *small["price_volume_signal"].TextValue != *large["price_volume_signal"].TextValue {
		t.Fatal("fixture error: the two should share the legacy label")
	}
	// … but the store can now tell them apart.
	if *small["price_volume_state"].TextValue == *large["price_volume_state"].TextValue {
		t.Error("the evidence store still cannot distinguish a drift from a break")
	}
	if *small["price_change_pct"].NumValue == *large["price_change_pct"].NumValue {
		t.Error("the change percentages are identical")
	}
}

// MISSING ≠ ZERO: an unmeasurable session writes no magnitude rows at all.
func TestUnknownMoveWritesNoMagnitudeRows(t *testing.T) {
	rec, _ := openRecorder(t)
	ev := recordWithTechnical(t, rec, pmEntryFor("價跌量縮", scanner.MoveUnknown, "", 0, 0))

	for _, k := range []string{"price_change_pct", "price_move", "price_volume_state", "price_change_atr"} {
		if _, ok := ev[k]; ok {
			t.Errorf("%q was written for a session with no previous close", k)
		}
	}
	// The legacy signal is unaffected.
	if _, ok := ev["price_volume_signal"]; !ok {
		t.Error("the legacy signal went missing")
	}
}

// A genuine 0.0% session IS a reading and must be stored — the counterpart to the test above.
func TestGenuineFlatSessionIsStored(t *testing.T) {
	rec, _ := openRecorder(t)
	ev := recordWithTechnical(t, rec, pmEntryFor("價跌量縮", scanner.MoveFlat, "FLAT_VOLUME_SHRINK", 0, 0))

	got, ok := ev["price_change_pct"]
	if !ok || got.NumValue == nil || *got.NumValue != 0 {
		t.Errorf("a real 0.0%% session must be stored as 0, got %v", got.NumValue)
	}
	// But an unavailable ATR still leaves no row.
	if _, ok := ev["price_change_atr"]; ok {
		t.Error("an unavailable ATR was stored as zero")
	}
	if !strings.HasPrefix(*ev["price_volume_state"].TextValue, scanner.MoveFlat) {
		t.Error("the state does not lead with the magnitude")
	}
}
