package pricingpower

import "fmt"

// PricedInInput holds the price/volume-derived metrics behind AlreadyPricedIn. Every
// field comes from data the scanner ALREADY has (returns, 52w-high distance, RS, MA
// deviation, Stage) — no new source. All optional; zero is treated as "neutral/unknown".
type PricedInInput struct {
	Ret20, Ret60, Ret120 float64 // % returns
	DistFrom52wHighPct   float64 // signed; -8 = 8% below the 52w high (0 = at the high)
	RSPercentile         float64 // 0–100
	MA20DevPct           float64 // Close vs MA20, % (positive = above)
	MA60DevPct           float64 // Close vs MA60, %
	Stage                string  // existing RocketStage label (optional, informational)
}

// ClassifyPricedIn returns how much of the fundamental improvement the price already
// reflects, using COMBINED evidence: no single warm metric can reach OVEREXTENDED unless
// it is extreme. Returns the state plus human-readable reasons for explainability.
func ClassifyPricedIn(in PricedInInput, th Thresholds) (PricingState, []string) {
	pts := 0
	var reasons []string
	add := func(p int, format string, args ...any) {
		pts += p
		reasons = append(reasons, fmt.Sprintf(format, args...))
	}

	switch {
	case in.Ret60 >= th.Ret60Priced:
		add(2, "60日漲幅 %.0f%% 偏高", in.Ret60)
	case in.Ret60 >= th.Ret60Partial:
		add(1, "60日漲幅 %.0f%% 中等", in.Ret60)
	}
	if in.DistFrom52wHighPct >= -th.Near52wHigh {
		add(1, "接近52週高（距高 %.1f%%）", in.DistFrom52wHighPct)
	}
	if in.MA20DevPct >= th.MA20DevHot {
		add(1, "偏離 MA20 %.0f%%", in.MA20DevPct)
	}
	if in.MA60DevPct >= th.MA60DevHot {
		add(1, "偏離 MA60 %.0f%%", in.MA60DevPct)
	}
	if in.RSPercentile >= th.RSHigh {
		add(1, "RS 百分位 %.0f 偏強", in.RSPercentile)
	}

	// Extreme single-metric override — only truly parabolic readings.
	extreme := in.Ret60 >= th.Ret60Extreme || in.MA20DevPct >= th.MA20DevExtr
	if extreme {
		reasons = append(reasons, "單一指標極端（拋物線）")
	}

	switch {
	case extreme || pts >= th.PtsOverext:
		return Overextended, reasons
	case pts >= th.PtsPricedIn:
		return PricedIn, reasons
	case pts >= 1:
		return PartiallyPricedIn, reasons
	default:
		reasons = append(reasons, "漲幅溫和、距高尚遠，尚未反映")
		return NotPricedIn, reasons
	}
}
