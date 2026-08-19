package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/technical"
)

// techFixture builds a deterministic multi-stock watchlist input with enough history for
// every R14 indicator to publish, and enough spread in behaviour that the scanner's own
// scores and ordering are non-trivial.
func techFixture() []fetcher.StockData {
	mk := func(sym, name string, n int, start, drift float64) fetcher.StockData {
		return fetcher.StockData{Symbol: sym, Name: name, Candles: makeCandles(n, start, drift, 2_000_000)}
	}
	return []fetcher.StockData{
		mk("1111", "強多", 200, 100, 0.9),
		mk("2222", "盤整", 200, 200, 0.02),
		mk("3333", "轉弱", 200, 300, -0.4),
		mk("4444", "淺多", 200, 50, 0.15),
	}
}

// scanShape is everything R14 is forbidden to move. Comparing the whole shape rather than a
// couple of sampled fields is the point: a regression guard that only checks what the author
// happened to think of is the one that misses the regression.
type scanShape struct {
	Symbol        string
	Score         int
	Action        Action
	RocketScore   int
	RocketStage   RocketStage
	WatchAction   WatchAction
	ExplosionProb string
	RiskLabel     string
	BreakoutPrice float64
	SupportPrice  float64
	StopLossPrice float64
	EntryZone     string
	TakeProfit    string
	DaysToWatch   string
}

func shapeOf(entries []WatchlistEntry) []scanShape {
	out := make([]scanShape, len(entries))
	for i, e := range entries {
		out[i] = scanShape{
			Symbol: e.A.Symbol, Score: e.A.Score, Action: e.A.Action,
			RocketScore: e.RocketScore, RocketStage: e.RocketStage,
			WatchAction: e.WatchAction, ExplosionProb: e.ExplosionProb,
			RiskLabel: e.RiskLabel, BreakoutPrice: e.BreakoutPrice,
			SupportPrice: e.SupportPrice, StopLossPrice: e.StopLossPrice,
			EntryZone: e.EntryZone, TakeProfit: e.TakeProfitZone,
			DaysToWatch: e.DaysToWatch,
		}
	}
	return out
}

// Default OFF must mean the field is nil — not an empty struct standing in for one.
func TestTechnicalDisabledLeavesNilField(t *testing.T) {
	s := New(Config{})
	entries := s.EnrichWatchlist(techFixture(), nil, nil, nil, nil)
	if len(entries) == 0 {
		t.Fatal("fixture produced no entries")
	}
	for _, e := range entries {
		if e.Technical != nil {
			t.Errorf("%s: Technical = %+v with the flag off, want nil", e.A.Symbol, e.Technical)
		}
	}
}

// THE regression guard: the same fixture, before and after R14 is switched on, must produce
// an identical scan. Score, action, stage, every price level, and the ORDER.
func TestTechnicalDoesNotChangeTheScan(t *testing.T) {
	fixture := techFixture()

	before := shapeOf(New(Config{}).EnrichWatchlist(fixture, nil, nil, nil, nil))
	after := shapeOf(New(Config{EnableTechnicalIndicators: true}).
		EnrichWatchlist(fixture, nil, nil, nil, nil))

	if len(before) != len(after) {
		t.Fatalf("entry count changed: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("entry %d changed when R14 was enabled:\n before %+v\n after  %+v",
				i, before[i], after[i])
		}
	}

	// Sort order specifically: comparing element-wise above already covers it, but a
	// same-multiset-different-order regression is worth naming explicitly.
	for i := range before {
		if before[i].Symbol != after[i].Symbol {
			t.Errorf("position %d: %s → %s — R14 reordered the watchlist",
				i, before[i].Symbol, after[i].Symbol)
		}
	}
}

// Enabling the display flag alone must not change anything either: presentation is not
// computation, and the scan is identical under all four flag combinations.
func TestTechnicalFlagCombinationsLeaveTheScanIdentical(t *testing.T) {
	fixture := techFixture()
	baseline := shapeOf(New(Config{}).EnrichWatchlist(fixture, nil, nil, nil, nil))

	for name, cfg := range map[string]Config{
		"compute only":  {EnableTechnicalIndicators: true},
		"show only":     {ShowTechnicalIndicators: true},
		"compute+show":  {EnableTechnicalIndicators: true, ShowTechnicalIndicators: true},
		"custom params": {EnableTechnicalIndicators: true, Technical: technical.Config{ADXPeriod: 7, RSIPeriod: 9}},
	} {
		got := shapeOf(New(cfg).EnrichWatchlist(fixture, nil, nil, nil, nil))
		if len(got) != len(baseline) {
			t.Fatalf("%s: entry count changed", name)
		}
		for i := range baseline {
			if baseline[i] != got[i] {
				t.Errorf("%s: entry %d changed:\n baseline %+v\n got      %+v",
					name, i, baseline[i], got[i])
			}
		}
	}
}

// Enabled means attached: a real result with real values, and the parameters recorded.
func TestTechnicalEnabledAttachesAResult(t *testing.T) {
	s := New(Config{EnableTechnicalIndicators: true})
	entries := s.EnrichWatchlist(techFixture(), nil, nil, nil, nil)
	if len(entries) == 0 {
		t.Fatal("fixture produced no entries")
	}
	for _, e := range entries {
		if e.Technical == nil {
			t.Fatalf("%s: Technical is nil with the flag on", e.A.Symbol)
		}
		r := e.Technical
		if r.ADX == nil || r.RSI == nil || r.MACD == nil || r.Keltner == nil || r.Pivot == nil {
			t.Fatalf("%s: a sub-indicator is nil; a computed result must carry all of them", e.A.Symbol)
		}
		if r.Context == nil {
			t.Fatalf("%s: no context", e.A.Symbol)
		}
		if r.Config.ADXPeriod == 0 {
			t.Errorf("%s: the result must record the parameters it was produced under", e.A.Symbol)
		}
		// 200 bars is well past every window, so everything should publish.
		for name, st := range map[string]technical.Status{
			"adx": r.ADX.Status, "rsi": r.RSI.Status, "macd": r.MACD.Status,
			"keltner": r.Keltner.Status, "pivot": r.Pivot.Status,
		} {
			if st != technical.StatusOK {
				t.Errorf("%s: %s = %s on 200 bars, want OK", e.A.Symbol, name, st)
			}
		}
	}
}

// A stock with too little history still attaches a result — with statuses that explain
// themselves, rather than a nil the caller cannot tell apart from "feature off".
func TestTechnicalThinHistoryStillAttaches(t *testing.T) {
	s := New(Config{EnableTechnicalIndicators: true})
	// EnrichWatchlist itself skips anything under 30 candles, so 32 is the thinnest input
	// that reaches the technical layer at all — and it is below the MACD requirement of 36.
	entries := s.EnrichWatchlist([]fetcher.StockData{
		{Symbol: "9999", Name: "短", Candles: makeCandles(32, 100, 0.5, 2_000_000)},
	}, nil, nil, nil, nil)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	r := entries[0].Technical
	if r == nil {
		t.Fatal("thin history must still attach a result")
	}
	if r.MACD.Status != technical.StatusInsufficientData {
		t.Errorf("MACD on 32 bars = %s, want INSUFFICIENT_DATA", r.MACD.Status)
	}
	if r.MACD.MACD != 0 || r.MACD.Histogram != 0 {
		t.Errorf("an INSUFFICIENT_DATA MACD published values: %+v", r.MACD)
	}
}

// The C6b guardrail scoring reads ShadowSignals and nothing else. R14 lives outside it, so
// there is no path from a technical reading into the score even if a future edit tried.
func TestTechnicalIsOutsideShadowSignals(t *testing.T) {
	s := New(Config{EnableTechnicalIndicators: true, EnableSignalGuardrailScoring: true})
	entries := s.EnrichWatchlist(techFixture(), nil, nil, nil, nil)
	for _, e := range entries {
		if e.Technical == nil {
			t.Fatalf("%s: Technical is nil", e.A.Symbol)
		}
		// ShadowSignals is a fixed struct of five named fields; none of them is technical.
		// If R14 were ever moved inside it, this file would stop compiling — which is the
		// point of asserting the shape rather than a value.
		if e.Shadow != nil {
			_ = e.Shadow.RS
			_ = e.Shadow.NewHigh
			_ = e.Shadow.VCP
			_ = e.Shadow.Momentum
			_ = e.Shadow.MultiTimeframe
		}
	}
}
