package analyzer

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// VIX level thresholds. BASELINE, not calibrated. Higher volatility = lower (more risk-off)
// score.
const (
	vixNormalMax   = 20 // < → Normal
	vixElevatedMax = 28 // < → Elevated
	vixFearMax     = 40 // < → Fear; >= → Extreme Fear
)

// AnalyzeVIX scores the VIX module. Calm markets score high; fear scores low.
func AnalyzeVIX(d model.VIXData, weight float64) model.ModuleScore {
	state, score := vixState(d.Value)
	return model.ModuleScore{
		Name:      model.ModuleVIX,
		Score:     score,
		Weight:    weight,
		State:     state,
		Detail:    fmt.Sprintf("VIX %.1f", d.Value),
		Available: true,
	}
}

func vixState(v float64) (string, float64) {
	switch {
	case v < vixNormalMax:
		return VIXNormal, 65
	case v < vixElevatedMax:
		return VIXElevated, 45
	case v < vixFearMax:
		return VIXFear, 30
	default:
		return VIXExtremeFear, 15
	}
}
