package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/institution"
)

func f64(v float64) *float64 { return &v }

// completeView builds an institution view with a complete streak for foreign & trust.
func viewWith(foreignBuy, trustBuy int, foreignTrans string) *institution.StockChipView {
	return &institution.StockChipView{
		Completeness: institution.ChipCompleteness{DataComplete: true},
		Foreign:      institution.LegStats{ConsecutiveBuyDays: foreignBuy, StreakComplete: true, Transition: foreignTrans},
		Trust:        institution.LegStats{ConsecutiveBuyDays: trustBuy, StreakComplete: true},
	}
}

func hasConfluence(cv *ConfluenceView, typ string) bool {
	if cv == nil {
		return false
	}
	for _, s := range cv.Signals {
		if s.Type == typ {
			return true
		}
	}
	return false
}

func TestConfluenceHealthyStrength(t *testing.T) {
	inst := viewWith(3, 3, institution.TransitionNone)
	bias := &BiasView{Bias20: f64(8), Risk: BiasRiskNormal} // not overheated
	cv := computeConfluence(inst, bias, "↑↑↑")
	if !hasConfluence(cv, ConfluenceHealthyStrength) {
		t.Errorf("want HEALTHY_STRENGTH; got %+v", cv)
	}
}

func TestConfluenceDistributionRisk(t *testing.T) {
	inst := viewWith(0, 0, institution.TransitionBuyToSell)
	bias := &BiasView{Bias20: f64(18), Risk: BiasRiskHigh}
	cv := computeConfluence(inst, bias, "↑↑↑")
	if !hasConfluence(cv, ConfluenceDistributionRisk) {
		t.Errorf("want DISTRIBUTION_RISK; got %+v", cv)
	}
	if hasConfluence(cv, ConfluenceHealthyStrength) {
		t.Errorf("overheated must not be HEALTHY_STRENGTH")
	}
}

func TestConfluenceOversoldImprovement(t *testing.T) {
	inst := viewWith(0, 0, institution.TransitionSellToBuy)
	bias := &BiasView{Bias20: f64(-15), Risk: BiasRiskDeeplyOversold}
	cv := computeConfluence(inst, bias, "↓↓↓")
	if !hasConfluence(cv, ConfluenceOversoldChipImprovement) {
		t.Errorf("want OVERSOLD_CHIP_IMPROVEMENT; got %+v", cv)
	}
}

func TestConfluenceIncompleteDataSuppressed(t *testing.T) {
	inst := viewWith(3, 3, institution.TransitionNone)
	inst.Completeness.DataComplete = false // incomplete → no combos
	bias := &BiasView{Bias20: f64(8), Risk: BiasRiskNormal}
	if computeConfluence(inst, bias, "↑↑↑") != nil {
		t.Errorf("incomplete data must produce no confluence")
	}
}

func TestConfluenceNoBias(t *testing.T) {
	inst := viewWith(3, 3, institution.TransitionNone)
	if computeConfluence(inst, nil, "↑↑↑") != nil {
		t.Errorf("nil bias → nil confluence")
	}
}
