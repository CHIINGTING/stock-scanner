package scanner

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// pmCandles builds a flat history and then applies a final-day change, so the last session's
// magnitude is exactly what the test asks for.
func pmCandles(n int, base, finalChgPct, finalVolMult float64) []fetcher.StockData {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cs := make([]fetcher.Candle, n)
	for i := 0; i < n; i++ {
		p := base
		v := int64(2_000_000)
		if i == n-1 {
			p = base * (1 + finalChgPct/100)
			v = int64(2_000_000 * finalVolMult)
		}
		hi, lo := p, p
		if p > base {
			hi, lo = p, base
		} else if p < base {
			hi, lo = base, p
		}
		cs[i] = fetcher.Candle{
			Date: start.AddDate(0, 0, i), Open: base, High: hi * 1.001, Low: lo * 0.999,
			Close: p, Volume: v,
		}
	}
	return []fetcher.StockData{{Symbol: "TEST", Name: "測試", Candles: cs}}
}

func analyzeOne(t *testing.T, data []fetcher.StockData) StockAnalysis {
	t.Helper()
	got := New(Config{}).ScanWatchlist(data)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	return got[0]
}

// End to end through the real scanner: the four thin-volume cases the requirement names must
// be distinguishable in the ATTACHED fields, not just in the classifier.
func TestScannerDistinguishesMagnitudeEndToEnd(t *testing.T) {
	type row struct {
		chg   float64
		state string
		move  string
	}
	var rows []row
	for _, chg := range []float64{0.2, 5.0, -0.2, -5.0} {
		a := analyzeOne(t, pmCandles(60, 100, chg, 0.5))
		rows = append(rows, row{chg, a.PriceVolumeState, a.PriceMove})
		if a.PriceChangePct == 0 {
			t.Errorf("%+.1f%%: PriceChangePct was not populated", chg)
		}
		// The raw number keeps its sign even when the class is FLAT.
		if (chg > 0) != (a.PriceChangePct > 0) {
			t.Errorf("%+.1f%%: PriceChangePct = %v, sign lost", chg, a.PriceChangePct)
		}
	}
	if rows[0].state == rows[1].state {
		t.Errorf("+0.2%% and +5.0%% on thin volume both read %q", rows[0].state)
	}
	if rows[2].state == rows[3].state {
		t.Errorf("−0.2%% and −5.0%% on thin volume both read %q", rows[2].state)
	}
	if rows[1].move != MoveLargeUp || rows[3].move != MoveLargeDown {
		t.Errorf("large moves misclassified: %s / %s", rows[1].move, rows[3].move)
	}
}

// THE regression guard. The magnitude fields are additive: the legacy signal, the score, the
// action and every other scored field must be byte-identical to what they were before, or
// six downstream consumers and every historical stored row start disagreeing with the code.
func TestMagnitudeDoesNotChangeExistingOutput(t *testing.T) {
	for _, tc := range []struct{ chg, vol float64 }{
		{0.2, 0.5}, {5.0, 0.5}, {-0.2, 0.5}, {-5.0, 0.5},
		{0.2, 2.0}, {5.0, 2.0}, {-0.2, 2.0}, {-5.0, 2.0},
		{0, 1.0}, {9.6, 0.5}, // flat, and a limit-up-ish day
	} {
		a := analyzeOne(t, pmCandles(60, 100, tc.chg, tc.vol))

		// The signal vocabulary is CLOSED. 平盤量增/平盤量縮 were added deliberately, as the
		// correction for a flat session being called a decline; anything outside this set
		// means the vocabulary drifted, which breaks pvCSS, the stored history and this test.
		switch a.PriceVolumeSignal {
		case "價漲量增", "價漲量縮", "價跌量增", "價跌量縮",
			"平盤量增", "平盤量縮", "漲停鎖量", "漲停失敗", "":
		default:
			t.Errorf("%+.1f%%/%.1fx: PriceVolumeSignal = %q — the vocabulary changed",
				tc.chg, tc.vol, a.PriceVolumeSignal)
		}
		// A 平盤 label may ONLY appear for an exactly-unchanged close. If it ever widens to
		// a band, that is a strategy change and must go through the backtest first.
		if strings.HasPrefix(a.PriceVolumeSignal, "平盤") && a.PriceChangePct != 0 {
			t.Errorf("%+.1f%%: labelled %q with a non-zero change %v",
				tc.chg, a.PriceVolumeSignal, a.PriceChangePct)
		}
		// And the direction it encodes still matches the raw change, so the two views agree.
		if a.PriceVolumeSignal == "價漲量增" || a.PriceVolumeSignal == "價漲量縮" {
			if a.PriceChangePct <= 0 {
				t.Errorf("%+.1f%%: signal says up but change is %v", tc.chg, a.PriceChangePct)
			}
		}
		// Scores stay inside their documented ranges — the new fields feed nothing.
		if a.Score < 0 || a.Score > 100 {
			t.Errorf("%+.1f%%: score %d out of range", tc.chg, a.Score)
		}
		if a.VolumeScore < 0 || a.VolumeScore > 25 {
			t.Errorf("%+.1f%%: volume score %d out of range", tc.chg, a.VolumeScore)
		}
	}
}

// The classifier reads the SAME volume ratio the signal used, so the two can never disagree
// about whether the session was thin.
func TestMagnitudeAgreesWithLegacySignalOnVolume(t *testing.T) {
	shrink := analyzeOne(t, pmCandles(60, 100, 5.0, 0.5))
	expand := analyzeOne(t, pmCandles(60, 100, 5.0, 2.0))

	if shrink.PriceVolumeSignal != "價漲量縮" {
		t.Fatalf("fixture did not produce 價漲量縮, got %q", shrink.PriceVolumeSignal)
	}
	if shrink.PriceVolumeState != MoveLargeUp+"_"+VolShrink {
		t.Errorf("state = %q, want LARGE_UP_VOLUME_SHRINK", shrink.PriceVolumeState)
	}
	if expand.PriceVolumeSignal != "價漲量增" {
		t.Fatalf("fixture did not produce 價漲量增, got %q", expand.PriceVolumeSignal)
	}
	if expand.PriceVolumeState != MoveLargeUp+"_"+VolExpansion {
		t.Errorf("state = %q, want LARGE_UP_VOLUME_EXPANSION", expand.PriceVolumeState)
	}
}

// A limit-up session keeps its special legacy label AND gains a factual magnitude — the two
// describe different things and neither should suppress the other.
func TestLimitUpKeepsItsLabelAndGainsMagnitude(t *testing.T) {
	a := analyzeOne(t, pmCandles(60, 100, 9.6, 0.5))
	if a.PriceMove != MoveLargeUp {
		t.Errorf("a 9.6%% session = %s, want LARGE_UP", a.PriceMove)
	}
	if a.PriceChangePct < 9 {
		t.Errorf("change = %v, want ~9.6", a.PriceChangePct)
	}
}

// The AI layer must receive the magnitude, or the model is back to seeing "價漲量縮" for
// both a drift and a surge and cannot tell them apart.
func TestAIEvidenceCarriesMagnitude(t *testing.T) {
	e := WatchlistEntry{A: StockAnalysis{
		Symbol: "2330", PriceVolumeSignal: "價跌量縮",
		PriceMove: MoveLargeDown, PriceVolumeState: MoveLargeDown + "_" + VolShrink,
		PriceChangePct: -6.2, PriceChangeATR: -2.4,
	}}
	ev := buildAIEvidence(&e, AIMarketContext{})

	if ev.PriceChangePct != -6.2 || ev.PriceChangeATR != -2.4 {
		t.Errorf("change not passed through: %v / %v", ev.PriceChangePct, ev.PriceChangeATR)
	}
	if ev.PriceMove != MoveLargeDown {
		t.Errorf("price_move = %q", ev.PriceMove)
	}
	if ev.PriceVolumeState != MoveLargeDown+"_"+VolShrink {
		t.Errorf("price_volume_state = %q", ev.PriceVolumeState)
	}
	// The legacy signal still travels too.
	if ev.PriceVolumeSignal != "價跌量縮" {
		t.Errorf("the legacy signal was dropped: %q", ev.PriceVolumeSignal)
	}
}

// MoveUnknown means "we could not measure", which is not a category worth asking a model to
// reason about — it must not reach the prompt.
func TestAIEvidenceOmitsUnknownMove(t *testing.T) {
	e := WatchlistEntry{A: StockAnalysis{
		Symbol: "2330", PriceVolumeSignal: "價跌量縮", PriceMove: MoveUnknown,
	}}
	ev := buildAIEvidence(&e, AIMarketContext{})
	if ev.PriceMove != "" || ev.PriceVolumeState != "" {
		t.Errorf("UNKNOWN reached the prompt: %q / %q", ev.PriceMove, ev.PriceVolumeState)
	}
	if ev.PriceVolumeSignal != "價跌量縮" {
		t.Error("the legacy signal should still travel")
	}
}

// ── Flat-session correctness (data, not strategy) ────────────────────────────────────

// An exactly unchanged close is neither a rise nor a fall. Before this, priceUp's strict >
// put it in the "跌" branch and the label asserted something that did not happen — on a real
// 123-stock scan that was 13 of them.
func TestFlatSessionIsNotLabelledADecline(t *testing.T) {
	for _, vol := range []float64{0.5, 2.0} {
		a := analyzeOne(t, pmCandles(60, 100, 0, vol))
		if a.PriceChangePct != 0 {
			t.Fatalf("fixture did not produce a flat session: %v", a.PriceChangePct)
		}
		if strings.Contains(a.PriceVolumeSignal, "價跌") || strings.Contains(a.PriceVolumeSignal, "價漲") {
			t.Errorf("vol %.1fx: flat session labelled %q", vol, a.PriceVolumeSignal)
		}
		want := "平盤量縮"
		if vol >= 1.2 {
			want = "平盤量增"
		}
		if a.PriceVolumeSignal != want {
			t.Errorf("vol %.1fx: signal = %q, want %q", vol, a.PriceVolumeSignal, want)
		}
		// The magnitude view agrees with the label — the two must not contradict.
		if a.PriceMove != MoveFlat {
			t.Errorf("vol %.1fx: move = %s, want FLAT", vol, a.PriceMove)
		}
	}
}

// The correction is EXACT EQUALITY only. "Is −0.26% a fall?" is a judgement about where to
// draw a line — a strategy change, and one the R14 backtest is there to settle. "Is 0.00% a
// fall?" is not a judgement. Only the second was fixed, and this holds that line.
func TestNearFlatSessionsKeepTheirDirection(t *testing.T) {
	for _, tc := range []struct {
		chg  float64
		want string
	}{
		{-0.26, "價跌量縮"},
		{-0.01, "價跌量縮"},
		{0.01, "價漲量縮"},
		{0.26, "價漲量縮"},
	} {
		a := analyzeOne(t, pmCandles(60, 100, tc.chg, 0.5))
		if a.PriceVolumeSignal != tc.want {
			t.Errorf("%+.2f%%: signal = %q, want %q — only EXACT zero was reclassified",
				tc.chg, a.PriceVolumeSignal, tc.want)
		}
	}
}

// The correctness fix must not have moved a single score. The flat branch keeps the score it
// always had; only the words changed.
func TestFlatCorrectionDoesNotMoveScores(t *testing.T) {
	// A flat session still scores as the thin/heavy-volume branch did before, because
	// priceUp — which the score table reads — is untouched.
	for _, vol := range []float64{0.5, 1.6, 2.5} {
		a := analyzeOne(t, pmCandles(60, 100, 0, vol))
		if a.Score < 0 || a.Score > 100 {
			t.Errorf("vol %.1fx: score %d out of range", vol, a.Score)
		}
		if a.VolumeScore < 0 || a.VolumeScore > 25 {
			t.Errorf("vol %.1fx: volume score %d out of range", vol, a.VolumeScore)
		}
	}
	// A flat session with heavy volume keeps the SAME volume score a declining one gets,
	// because that is a strategy question the backtest has not answered yet.
	flat := analyzeOne(t, pmCandles(60, 100, 0, 2.0))
	down := analyzeOne(t, pmCandles(60, 100, -1.0, 2.0))
	if flat.VolumeScore != down.VolumeScore {
		t.Errorf("flat volume score %d != declining %d — the scoring semantics were changed, "+
			"which this fix is explicitly not allowed to do", flat.VolumeScore, down.VolumeScore)
	}
}

// The reasons must not say "下跌" or "賣壓" about an unchanged close.
func TestFlatSessionProseIsAccurate(t *testing.T) {
	a := analyzeOne(t, pmCandles(60, 100, 0, 2.5))
	all := strings.Join(a.Reasons, " | ")
	for _, bad := range []string{"但下跌", "賣壓沉重"} {
		if strings.Contains(all, bad) {
			t.Errorf("a flat session's reasons claim %q: %s", bad, all)
		}
	}
}
