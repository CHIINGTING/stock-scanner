package model

// This file holds the three-layer regime vocabulary. The classification LOGIC is M2/M3;
// M1 only fixes the vocabulary and the shape of what gets persisted.
//
// The layering exists so that a wrong call can be attributed later:
//
//	layer 1  PrimaryStructure      — benchmark price structure alone
//	layer 2  BreadthQuality        — how broad the move is
//	         InstitutionalPosture  — what the institutions are doing
//	layer 3  Regime                — the public five states, from an explicit decision table
//
// Market Score is NOT an input to any layer. The classifier signature never receives it,
// so "score >= 70 therefore bull" is impossible to write by accident.

// Benchmark names what the run used to represent "the market". The MVP uses 0050 because
// two years of it are already in .cache, but it is an ETF, not the index — mislabelling
// it TAIEX in stored history would poison every later comparison.
type Benchmark string

const (
	Benchmark0050  Benchmark = "0050"  // MVP default: ETF proxy, 487 bars already cached
	BenchmarkTAIEX Benchmark = "TAIEX" // future: the real weighted index
)

func (b Benchmark) Valid() bool { return b == Benchmark0050 || b == BenchmarkTAIEX }

// PrimaryStructure is layer 1: the shape of the benchmark's own price action. Breadth and
// institutional flows are deliberately excluded — this is the skeleton those later refine.
//
// WEAKENING is the important middle state: medium-term structure has broken (below MA20 and
// MA60) but the long-term floor still holds (above MA120). It is where BULL_PULLBACK lives,
// and keeping it separate from DOWNTREND is what stops an orderly correction from being
// read as a bear market.
type PrimaryStructure string

const (
	StructureStrongUptrend PrimaryStructure = "STRONG_UPTREND"
	StructureUptrend       PrimaryStructure = "UPTREND"
	StructureRange         PrimaryStructure = "RANGE"
	StructureWeakening     PrimaryStructure = "WEAKENING"
	StructureDowntrend     PrimaryStructure = "DOWNTREND"
	StructureUnknown       PrimaryStructure = "UNKNOWN" // insufficient history — never guess
)

func (p PrimaryStructure) Valid() bool {
	switch p {
	case StructureStrongUptrend, StructureUptrend, StructureRange,
		StructureWeakening, StructureDowntrend, StructureUnknown:
		return true
	}
	return false
}

// Bullish reports whether the structure is one the decision table treats as an uptrend
// family (used by the M3 table; defined here so the vocabulary owns its own semantics).
func (p PrimaryStructure) Bullish() bool {
	return p == StructureStrongUptrend || p == StructureUptrend
}

// BreadthQuality is layer 2a. NARROWING is the one that matters most: it specifically means
// "the index is near its high but participation is not confirming" — a comparison between
// price and internals, not a lone threshold.
type BreadthQuality string

const (
	BreadthHealthy    BreadthQuality = "HEALTHY"
	BreadthCooling    BreadthQuality = "COOLING"
	BreadthNarrowing  BreadthQuality = "NARROWING"
	BreadthCollapsing BreadthQuality = "COLLAPSING"
	BreadthUnknown    BreadthQuality = "UNKNOWN" // too few valid stocks to measure
)

func (b BreadthQuality) Valid() bool {
	switch b {
	case BreadthHealthy, BreadthCooling, BreadthNarrowing, BreadthCollapsing, BreadthUnknown:
		return true
	}
	return false
}

// Deteriorating reports whether breadth is in one of the two states that can, on its own,
// turn a resilient-looking tape into DISTRIBUTION.
func (b BreadthQuality) Deteriorating() bool {
	return b == BreadthNarrowing || b == BreadthCollapsing
}

// InstitutionalPosture is layer 2b: the combined stance of the futures, cash and margin
// evidence. UNKNOWN when fewer than two of the three are available — with one data point
// there is no posture, and calling that NEUTRAL would let a single outage read as calm.
type InstitutionalPosture string

const (
	PostureSupportive    InstitutionalPosture = "SUPPORTIVE"
	PostureNeutral       InstitutionalPosture = "NEUTRAL"
	PostureDeteriorating InstitutionalPosture = "DETERIORATING"
	PostureUnknown       InstitutionalPosture = "UNKNOWN"
)

func (p InstitutionalPosture) Valid() bool {
	switch p {
	case PostureSupportive, PostureNeutral, PostureDeteriorating, PostureUnknown:
		return true
	}
	return false
}

// Structure metric keys. These are persisted in every snapshot, so a validation run can
// re-derive a different verdict from the same observation. Add freely; never rename.
const (
	MetricClose          = "close"
	MetricMA20           = "ma20"
	MetricMA60           = "ma60"
	MetricMA120          = "ma120"
	MetricMA200          = "ma200" // context only — no decision rule reads it (see §9.2)
	MetricRet20          = "ret20"
	MetricRet60          = "ret60"
	MetricDDFromHigh60   = "dd_from_high60" // % below the 60-day closing high, >= 0
	MetricBreadthMA20    = "breadth_above_ma20"
	MetricBreadthMA60    = "breadth_above_ma60"
	MetricBreadthMA120   = "breadth_above_ma120"
	MetricBreadthDrop20  = "breadth_drop20_pp"   // pp below the 20-session high of breadth_above_ma20
	MetricBreadthValid   = "breadth_valid_count" // = the MA20 population
	MetricBreadthBase60  = "breadth_base60"      // population behind breadth_above_ma60
	MetricBreadthBase120 = "breadth_base120"     // population behind breadth_above_ma120
	MetricAdvRatio       = "advancing_ratio"
	MetricRealizedVol20  = "realized_vol20"
	MetricVolPercentile  = "realized_vol20_percentile"
)

// StructureView is the full layer-1 + layer-2 output. The four predicates are stored
// EXPLICITLY rather than recomputed at read time: the decision table branches on them, so
// persisting them is what makes a past call reproducible.
type StructureView struct {
	Benchmark Benchmark            `json:"benchmark"`
	Structure PrimaryStructure     `json:"structure"`
	Breadth   BreadthQuality       `json:"breadth_quality"`
	Posture   InstitutionalPosture `json:"institutional_posture"`

	TrendIntact       bool `json:"trend_intact"`       // close > MA120 && MA60 rising
	PriceResilient    bool `json:"price_resilient"`    // dd_from_high60 <= threshold || close > MA60
	OrderlyCorrection bool `json:"orderly_correction"` // correction inside [min,max] with no capitulation day
	FailedHighs       int  `json:"failed_highs"`       // touched the 60d high then lost MA20 within 3 days

	Metrics map[string]float64 `json:"metrics,omitempty"`
}

// Known reports whether both structural layers produced a real reading. When false the
// decision table must return RegimeUnknown rather than falling through to SIDEWAYS.
func (v StructureView) Known() bool {
	return v.Structure != StructureUnknown && v.Structure.Valid() &&
		v.Breadth != BreadthUnknown && v.Breadth.Valid()
}

// Metric returns a structure metric and whether it was recorded.
func (v StructureView) Metric(key string) (float64, bool) {
	if v.Metrics == nil {
		return 0, false
	}
	x, ok := v.Metrics[key]
	return x, ok
}

// Regime rule identifiers, matching the decision table in the approved plan (§9.5).
// Persisting the rule that fired is what makes a disputed call auditable: "why was
// 2026-08-07 DISTRIBUTION" is answered by "R3", not by re-reading the code as it is today.
const (
	RuleUnknown           = "R0"
	RuleBearDowntrend     = "R1"
	RuleBearCollapse      = "R2"
	RuleDistBreadth       = "R3"
	RuleDistCoolingInst   = "R4"
	RuleDistFailedHighs   = "R5"
	RuleBullStrong        = "R6"
	RuleBullUptrend       = "R7"
	RulePullbackOrderly   = "R8"
	RuleSidewaysRange     = "R9"
	RulePullbackWeakening = "R10"
	RuleSidewaysFallback  = "R11"
)

// RegimeDecision is layer 3's output: the verdict plus everything needed to defend it.
type RegimeDecision struct {
	Regime  Regime        `json:"regime"`
	RuleID  string        `json:"rule_id"` // one of the Rule* constants
	View    StructureView `json:"view"`
	Reasons []string      `json:"reasons"`
	Caveats []string      `json:"caveats"`
}
