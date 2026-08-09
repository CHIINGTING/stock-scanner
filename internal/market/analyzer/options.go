package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Put/call OI ratio thresholds. BASELINE, not calibrated. A low P/C = little downside
// hedging = risk-on; a high P/C = heavy hedging / fear = risk-off.
const (
	pcLowMax      = 0.7 // <= → Low P/C
	pcNeutralMax  = 1.0 // <= → Neutral P/C
	pcElevatedMax = 1.3 // <= → Elevated P/C; > → High P/C
)

// AnalyzeOptions scores the Options module from the put/call ratio.
func AnalyzeOptions(d model.OptionsData, weight float64) model.ModuleScore {
	state, score := optionsState(d.PutCallRatio)
	return model.ModuleScore{
		Name:      model.ModuleOptions,
		Score:     score,
		Weight:    weight,
		State:     state,
		Detail:    fmt.Sprintf("Put/Call %.2f", d.PutCallRatio),
		Available: true,
	}
}

func optionsState(ratio float64) (string, float64) {
	switch {
	case ratio <= pcLowMax:
		return OptionsLowPC, 65
	case ratio <= pcNeutralMax:
		return OptionsNeutralPC, 50
	case ratio <= pcElevatedMax:
		return OptionsElevatedPC, 40
	default:
		return OptionsHighPC, 30
	}
}
