package model

// ModuleScore is one module's contribution to the Market Score. Score is normalized to
// 0..100 where HIGHER = more bullish / risk-on, so all modules aggregate on one axis.
// When a data source is unavailable, Available=false and the module is excluded from the
// weighted average (the remaining weights renormalize — see analyzer.Aggregate).
type ModuleScore struct {
	Name      string  `json:"name"`      // one of the Module* constants
	Score     float64 `json:"score"`     // 0..100, higher = more bullish
	Weight    float64 `json:"weight"`    // relative weight before renormalization
	State     string  `json:"state"`     // short human label, e.g. "More Short", "Strong Buy"
	Detail    string  `json:"detail"`    // extra context, e.g. the Extreme Indicator
	Available bool    `json:"available"` // false = source missing; excluded from aggregate
}

// MarketScore is the 0..100 aggregate plus the modules that produced it. Total is the
// weighted average over AVAILABLE modules only; AvailableCount drives regime confidence.
type MarketScore struct {
	Total          float64       `json:"total"`
	AvailableCount int           `json:"available_count"`
	Modules        []ModuleScore `json:"modules"`
}
