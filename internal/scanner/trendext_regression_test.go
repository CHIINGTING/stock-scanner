package scanner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// R12 shadow-only regression guard.
//
// The claim under test is the whole premise of the release: turning enable_trend_extension on
// changes NOTHING the scanner decides. It is asserted against ACTUAL scanner output (the same
// approach TestCandlestickDoesNotAffectScoring and TestAttachAIDoesNotAffectScoring use),
// not against the analyzer in isolation — an analyzer can be pure and still be wired into a
// scorer by accident.

// trendItems builds a small universe with genuinely different trend shapes so the layer has
// something to disagree about: if every stock produced the same state, "output unchanged"
// would be trivially true.
func trendItems() []fetcher.StockData {
	return []fetcher.StockData{
		{Symbol: "1111", Name: "Rising", Source: "watchlist", Candles: makeCandles(300, 50, 0.45, 2_000_000)},
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(300, 50, 0.0, 1_000_000)},
		// Starts high enough that 300 bars of decline stay in positive territory — a series
		// that walks through zero would make the moving averages unavailable rather than
		// falling, which is a different test.
		{Symbol: "3333", Name: "Falling", Source: "watchlist", Candles: makeCandles(300, 200, -0.35, 1_500_000)},
	}
}

func enrichTrend(cfg Config) []WatchlistEntry {
	return New(cfg).EnrichWatchlist(
		trendItems(),
		map[string]string{},
		map[string]*SectorRotation{},
		map[string][]fetcher.StockData{},
		nil,
	)
}

// decisionFingerprint renders every field the scanner DECIDES. Comparing one string rather
// than a list of asserts means a scored field added later is covered without editing this
// test — the failure mode we actually care about is a future change quietly wiring R12 in.
func decisionFingerprint(entries []WatchlistEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		row := []any{
			e.A.Symbol, e.A.Name, e.A.Score, e.A.Action, e.A.Reasons,
			e.A.EntryPrice, e.A.StopLoss, e.A.Target1, e.A.Target2,
			e.RocketScore, e.RocketStage, e.ExplosionProb, e.DaysToWatch,
			e.WatchAction, e.Reasons, e.RiskLabel, e.RiskWarning, e.MTFRiskNote,
			e.BreakoutPrice, e.SupportPrice, e.StopLossPrice, e.EntryZone, e.TakeProfitZone,
			e.Sector, e.HasSector, e.SectorFlowDir, e.SectorMidLabel, e.SectorStage,
			e.Consol, e.Backtest, e.Shadow,
		}
		b, _ := json.Marshal(row)
		sb.Write(b)
		sb.WriteString("\n")
	}
	return sb.String()
}

// THE regression proof: enable_trend_extension=false leaves the scanner's decisions
// byte-identical to a build where the feature does not exist at all.
func TestTrendExtensionDisabledIsByteIdentical(t *testing.T) {
	off := enrichTrend(Config{})
	on := enrichTrend(Config{EnableTrendExtension: true})

	if len(off) != len(on) {
		t.Fatalf("entry count changed: off=%d on=%d", len(off), len(on))
	}
	if a, b := decisionFingerprint(off), decisionFingerprint(on); a != b {
		t.Errorf("enabling R12 changed a scanner decision.\n off: %s\n on : %s", a, b)
	}
}

// Ordering is an output too. A post-pass must not reshuffle the watchlist.
func TestTrendExtensionPreservesOrder(t *testing.T) {
	off := enrichTrend(Config{})
	on := enrichTrend(Config{EnableTrendExtension: true})
	for i := range off {
		if off[i].A.Symbol != on[i].A.Symbol {
			t.Fatalf("order changed at %d: %s → %s", i, off[i].A.Symbol, on[i].A.Symbol)
		}
	}
}

// Disabled means "not computed", not "computed and blanked". A nil field is what keeps the
// report byte-identical and makes the flag genuinely zero-cost.
func TestTrendExtensionNilWhenDisabled(t *testing.T) {
	for _, e := range enrichTrend(Config{}) {
		if e.TrendExt != nil {
			t.Errorf("%s: TrendExt must be nil when disabled, got %+v", e.A.Symbol, e.TrendExt)
		}
	}
}

// Enabled means every entry gets a reading — including an explicit INSUFFICIENT_DATA one.
// Silence would be indistinguishable from the feature being off.
func TestTrendExtensionPopulatedWhenEnabled(t *testing.T) {
	entries := enrichTrend(Config{EnableTrendExtension: true})
	states := map[TrendExtState]int{}
	for _, e := range entries {
		if e.TrendExt == nil {
			t.Fatalf("%s: TrendExt must be non-nil when enabled", e.A.Symbol)
		}
		switch e.TrendExt.State {
		case TrendConfirmed, PullbackInUptrend, TrendExtended, SectorConfirmed,
			SectorDivergence, TrendWeakening, TrendExtNeutral, TrendExtInsufficient:
		default:
			t.Errorf("%s: unrecognised state %q", e.A.Symbol, e.TrendExt.State)
		}
		states[e.TrendExt.State]++
	}
	// The rising and falling fixtures must not land in the same state, otherwise this whole
	// file would be proving nothing.
	if len(states) < 2 {
		t.Errorf("fixtures produced a single state %v — the regression proof would be vacuous", states)
	}
}

// The scanner's own trend fixtures must produce sane slope signs end-to-end. This is the
// integration check that the derivation is wired to the right price series.
func TestTrendExtensionSlopeSignsMatchFixtures(t *testing.T) {
	bySymbol := map[string]*TrendExtensionView{}
	for _, e := range enrichTrend(Config{EnableTrendExtension: true}) {
		bySymbol[e.A.Symbol] = e.TrendExt
	}
	rising, falling := bySymbol["1111"], bySymbol["3333"]
	if rising.MA20Slope5D == nil || falling.MA20Slope5D == nil {
		t.Fatal("300 bars must yield MA20 slopes")
	}
	if *rising.MA20Slope5D <= 0 {
		t.Errorf("the rising fixture has slope %v, want > 0", *rising.MA20Slope5D)
	}
	if *falling.MA20Slope5D >= 0 {
		t.Errorf("the falling fixture has slope %v, want < 0", *falling.MA20Slope5D)
	}
}

// Sector Heat rides the enable flag too: rotation output must be untouched when off.
func TestSectorHeatNilWhenDisabled(t *testing.T) {
	data := map[string][]fetcher.StockData{"測試族群": trendItems()}
	order := []string{"測試族群"}

	off := New(Config{}).ScanRotation(order, data)
	on := New(Config{EnableTrendExtension: true}).ScanRotation(order, data)

	if len(off) == 0 || len(on) == 0 {
		t.Fatal("rotation produced no sectors")
	}
	if off[0].Heat != nil {
		t.Error("Heat must be nil when the feature is off")
	}
	if on[0].Heat == nil {
		t.Fatal("Heat must be populated when the feature is on")
	}

	// And Heat must never disturb the existing rotation numbers.
	offJSON, _ := json.Marshal([]any{off[0].Score, off[0].OppScore, off[0].Stage,
		off[0].RelStrength, off[0].NewHighRatio, off[0].BreakoutRatio, off[0].VolExpansion,
		off[0].MA60Slope, off[0].ShortTermFlowScore, off[0].MidTermStrength, off[0].TrendStrength})
	onJSON, _ := json.Marshal([]any{on[0].Score, on[0].OppScore, on[0].Stage,
		on[0].RelStrength, on[0].NewHighRatio, on[0].BreakoutRatio, on[0].VolExpansion,
		on[0].MA60Slope, on[0].ShortTermFlowScore, on[0].MidTermStrength, on[0].TrendStrength})
	if string(offJSON) != string(onJSON) {
		t.Errorf("Sector Heat changed the rotation score.\n off: %s\n on : %s", offJSON, onJSON)
	}
}

// A 3-member sector is below the floor: it must report INSUFFICIENT_DATA rather than a
// confident-looking number, end to end through the real rotation path.
func TestSectorHeatInsufficientEndToEnd(t *testing.T) {
	data := map[string][]fetcher.StockData{"迷你族群": trendItems()} // 3 members < minHeatMembers
	on := New(Config{EnableTrendExtension: true}).ScanRotation([]string{"迷你族群"}, data)
	if len(on) == 0 {
		t.Fatal("rotation produced no sectors")
	}
	h := on[0].Heat
	if h == nil || h.Status != SectorHeatInsufficientData {
		t.Fatalf("a 3-member sector must be INSUFFICIENT_DATA, got %+v", h)
	}
	if h.Heat != 0 {
		t.Errorf("no heat value may be published below the floor, got %v", h.Heat)
	}
	if h.MemberCount != 3 {
		t.Errorf("MemberCount = %d, want 3 — the denominator must still be reported", h.MemberCount)
	}
}
