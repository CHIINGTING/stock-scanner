package analyzer

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// mkScore builds a MarketScore with a given total, availability count, and modules.
func mkScore(total float64, avail int, mods ...model.ModuleScore) model.MarketScore {
	return model.MarketScore{Total: total, AvailableCount: avail, Modules: mods}
}

func mod(name, state string) model.ModuleScore {
	return model.ModuleScore{Name: name, State: state, Available: true}
}

func TestClassifyRegimes(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Regime // Bear<40, Pullback>=55, Bull>=65
	healthy := []model.ModuleScore{
		mod(model.ModuleForeignCash, CashStrongBuy),
		mod(model.ModuleVIX, VIXNormal),
	}
	cases := []struct {
		name string
		ms   model.MarketScore
		want model.Regime
	}{
		{"bull", mkScore(72, 6, healthy...), model.RegimeBull},
		{"bull-pullback", mkScore(60, 6, healthy...), model.RegimeBullPullback},
		{"sideways", mkScore(48, 6, healthy...), model.RegimeSideways},
		{"bear", mkScore(30, 6, healthy...), model.RegimeBear},
		{"distribution", mkScore(72, 6,
			mod(model.ModuleForeignCash, CashStrongSell),
			mod(model.ModuleVIX, VIXFear)), model.RegimeDistribution},
		{"unknown-too-few", mkScore(72, 1, healthy...), model.RegimeUnknown},
	}
	for _, c := range cases {
		got := Classify(c.ms, th, 5)
		if got.Regime != c.want {
			t.Errorf("%s: Regime = %q, want %q", c.name, got.Regime, c.want)
		}
		if got.Description == "" || got.Strategy == "" {
			t.Errorf("%s: missing guidance text", c.name)
		}
	}
}

func TestClassifyConfidence(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Regime
	healthy := []model.ModuleScore{mod(model.ModuleForeignCash, CashStrongBuy), mod(model.ModuleVIX, VIXNormal)}

	// Enough modules, not near a boundary → MEDIUM.
	if got := Classify(mkScore(72, 6, healthy...), th, 5); got.Confidence != model.ConfidenceMedium {
		t.Errorf("6-module bull confidence = %q, want MEDIUM", got.Confidence)
	}
	// Too few modules → LOW.
	if got := Classify(mkScore(72, 3, healthy...), th, 5); got.Confidence != model.ConfidenceLow {
		t.Errorf("3-module confidence = %q, want LOW", got.Confidence)
	}
	// Sitting on a boundary → LOW.
	if got := Classify(mkScore(55, 6, healthy...), th, 5); got.Confidence != model.ConfidenceLow {
		t.Errorf("boundary-55 confidence = %q, want LOW", got.Confidence)
	}
}

// Extreme foreign-futures short must surface as a hedging caveat, not flip the regime bearish.
func TestClassifyExtremeShortCaveat(t *testing.T) {
	th := model.MarketConfig{}.Defaulted().Regime
	fut := AnalyzeFutures(model.FuturesData{NetPosition: -83390, DailyChange: -2324}, 20,
		model.MarketConfig{}.Defaulted().Futures)
	ms := mkScore(62, 7, fut, mod(model.ModuleForeignCash, CashStrongBuy), mod(model.ModuleVIX, VIXNormal))
	got := Classify(ms, th, 5)
	if got.Regime != model.RegimeBullPullback {
		t.Errorf("Regime = %q, want Bull Pullback", got.Regime)
	}
	found := false
	for _, c := range got.Caveats {
		if strings.Contains(c, "避險") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an extreme-short hedging caveat, got %v", got.Caveats)
	}
}

// RED LINE (mirrors R9 §13): dashboard guidance is context only — never a trade instruction.
func TestGuidanceNoTradeInstructions(t *testing.T) {
	banned := []string{"建議買", "建議賣", "可以買", "可以賣", "下單", "掛單", "進場價", "到價"}
	for regime, g := range regimeGuidance {
		for _, s := range []string{g.desc, g.strategy, g.risk} {
			for _, bad := range banned {
				if strings.Contains(s, bad) {
					t.Errorf("regime %q guidance contains banned phrase %q: %q", regime, bad, s)
				}
			}
		}
	}
}
