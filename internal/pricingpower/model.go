// Package pricingpower is the Phase-1 pure domain + scoring engine for "Pricing Power
// Mode / 漲價循環模式": finding stocks whose industry is entering a shortage → price-hike
// → margin/EPS-expansion cycle that the share price has not yet fully reflected.
//
// Phase 1 is DELIBERATELY self-contained and PURE: no data fetching, no news ingestion,
// no fundamentals, no report/attach wiring, no external calls. It defines the domain
// model, availability-normalized scoring, already-priced-in classification, cycle-state
// logic, and theme mapping — all as pure functions driven by explicit inputs. Because it
// touches no existing file, it cannot change BUY/WATCH/REDUCE, RocketScore, ranking,
// Stage, or the HTML report. Real inputs (news evidence, fundamentals, live indicators)
// and the report/attach layers arrive in later phases.
package pricingpower

// Theme is the industry pricing-cycle theme a stock belongs to.
type Theme string

const (
	ThemeMemory      Theme = "MEMORY"
	ThemePassive     Theme = "PASSIVE_COMPONENT"
	ThemePowerSemi   Theme = "POWER_SEMICONDUCTOR"
	ThemeAnalogIC    Theme = "ANALOG_IC"
	ThemeMOSFET      Theme = "MOSFET"
	ThemePackaging   Theme = "PACKAGING"
	ThemePCBMaterial Theme = "PCB_MATERIAL"
	ThemeOther       Theme = "OTHER"
)

// Availability marks whether the data behind a scoring dimension exists. NOT_AVAILABLE
// dimensions are removed from the score denominator (graceful degradation) — never
// scored zero, which would wrongly punish a stock for missing data.
type Availability string

const (
	Supported    Availability = "SUPPORTED"
	Partial      Availability = "PARTIAL"
	NotAvailable Availability = "NOT_AVAILABLE"
)

// Cycle is the pricing-cycle stage (deterministic FSM, see cycle.go).
type Cycle string

const (
	CycleNone              Cycle = "NONE"
	CycleEarlySignal       Cycle = "EARLY_SIGNAL"
	CyclePriceHike         Cycle = "PRICE_HIKE"
	CycleConfirmed         Cycle = "CONFIRMED"
	CycleEarningsExpansion Cycle = "EARNINGS_EXPANSION"
	CycleLateCycle         Cycle = "LATE_CYCLE"
	CycleReversalRisk      Cycle = "REVERSAL_RISK"
)

// PricingState is how much of the fundamental improvement the price already reflects.
type PricingState string

const (
	NotPricedIn       PricingState = "NOT_PRICED_IN"
	PartiallyPricedIn PricingState = "PARTIALLY_PRICED_IN"
	PricedIn          PricingState = "PRICED_IN"
	Overextended      PricingState = "OVEREXTENDED"
)

// Leverage is the profit-vs-revenue operating-leverage grade (fundamentals; Phase 3).
type Leverage string

const (
	LeverageStrong   Leverage = "STRONG"
	LeverageModerate Leverage = "MODERATE"
	LeverageWeak     Leverage = "WEAK"
	LeverageNone     Leverage = "NONE"
)

// EvidenceTier is the trust tier of one piece of evidence, by SOURCE type. A single
// podcast provider is LOW; MEDIUM/HIGH require genuinely independent / official sources.
type EvidenceTier string

const (
	TierHigh   EvidenceTier = "HIGH"
	TierMedium EvidenceTier = "MEDIUM"
	TierLow    EvidenceTier = "LOW"
)

// Confidence is the overall signal confidence (data completeness + source independence).
type Confidence string

const (
	ConfHigh   Confidence = "HIGH"
	ConfMedium Confidence = "MEDIUM"
	ConfLow    Confidence = "LOW"
)

// Dimension names — stable keys for the 8 scoring dimensions.
const (
	DimPriceIncrease  = "price_increase"
	DimShortage       = "shortage"
	DimUtilization    = "utilization"
	DimMargin         = "margin"
	DimProfitLeverage = "profit_leverage"
	DimEPS            = "eps"
	DimNotPricedIn    = "not_priced_in"
	DimTechnical      = "technical"
)

// DimensionMax is each dimension's maximum weight; the eight sum to 100.
var DimensionMax = map[string]float64{
	DimPriceIncrease:  20,
	DimShortage:       15,
	DimUtilization:    10,
	DimMargin:         15,
	DimProfitLeverage: 15,
	DimEPS:            10,
	DimNotPricedIn:    10,
	DimTechnical:      5,
}

// TotalWeight is the sum of all dimension weights (100).
const TotalWeight = 100.0

// fundamentalDims are the dimensions backed by financial statements; their availability
// raises the confidence ceiling (hard numbers vs. news chatter).
var fundamentalDims = map[string]bool{DimMargin: true, DimProfitLeverage: true, DimEPS: true}

// DimensionScore is one dimension's contribution — fully explainable (why it earned what
// it did, and whether its data was even available).
type DimensionScore struct {
	Name         string       `json:"name"`
	Availability Availability `json:"availability"`
	Earned       float64      `json:"earned"`
	Max          float64      `json:"max"`
	Reason       string       `json:"reason,omitempty"`
}

// Evidence is one qualitative data point (news/official). Populated in later phases.
type Evidence struct {
	Kind   string       `json:"kind"` // PRICE_HIKE / SHORTAGE / UTILIZATION / MARGIN / EPS / REVERSAL
	Tier   EvidenceTier `json:"tier"`
	Source string       `json:"source"`
	Text   string       `json:"text,omitempty"`
}

// Fundamentals holds financial-statement-derived metrics (Phase 3). Availability fields
// let the engine degrade gracefully when a metric is missing.
type Fundamentals struct {
	RevenueYoY, ProfitYoY, EPSYoY, GrossMarginDelta  float64
	RevenueAvail, ProfitAvail, EPSAvail, MarginAvail Availability
	AsOf                                             string // reporting period, e.g. "2026Q1" (lag disclosure)
}

// Signal is the full Pricing Power result for one stock. Display/analysis only; nothing
// here feeds the existing scanner score/action/rank.
type Signal struct {
	Symbol string `json:"symbol"`
	Sector string `json:"sector"`
	Theme  Theme  `json:"theme"`

	Score           int     `json:"score"`            // 0–100 normalized over available dimensions
	Earned          float64 `json:"earned"`           // raw earned weight
	AvailableWeight float64 `json:"available_weight"` // denominator actually used
	TotalWeight     float64 `json:"total_weight"`     // 100
	Coverage        float64 `json:"coverage"`         // AvailableWeight / TotalWeight

	Confidence     Confidence   `json:"confidence"`
	Cycle          Cycle        `json:"cycle"`
	PricingState   PricingState `json:"pricing_state"`
	ProfitLeverage Leverage     `json:"profit_leverage"`

	Dimensions []DimensionScore `json:"dimensions"`
	Reasons    []string         `json:"reasons,omitempty"`
	Risks      []string         `json:"risks,omitempty"`
	Evidence   []Evidence       `json:"evidence,omitempty"`

	Computed bool `json:"computed"`
}
