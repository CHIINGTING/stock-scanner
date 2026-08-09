// Package candlestick is the R10-2 K 線型態 (candlestick pattern) analyzer. It is a pure,
// deterministic, shadow-only layer: it detects patterns from daily OHLC and never affects
// existing Score / Action / sort / Stage / Rocket / Momentum / VCP. See
// docs/SPEC_R10_2_CANDLESTICK_SHADOW.md.
//
// Pipeline (strict layering): Raw OHLC → Validation → Metrics → Geometry (pure, multi-
// output) → Context → Interpretation → Confidence → PatternSignal → Result. This file holds
// the foundational types only (Phase 1); the pipeline stages land in later phases.
package candlestick

import "time"

// Candle is the minimal OHLCV the analyzer needs. The scanner adapts fetcher.Candle to it.
type Candle struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// Direction is the directional lean of a pattern signal.
type Direction string

const (
	DirectionBullish Direction = "BULLISH"
	DirectionBearish Direction = "BEARISH"
	DirectionNeutral Direction = "NEUTRAL"
)

// PatternType is the formal, interpreted candlestick pattern. The 13 P0 types are listed;
// BULLISH_MARUBOZU / BEARISH_MARUBOZU are deliberately independent types (not one MARUBOZU
// + Direction) so HTML/sidecar/validator can group by type directly (SPEC §2).
type PatternType string

const (
	PatternNone PatternType = ""

	// single-candle (9)
	PatternDoji            PatternType = "DOJI"
	PatternLongUpperShadow PatternType = "LONG_UPPER_SHADOW"
	PatternLongLowerShadow PatternType = "LONG_LOWER_SHADOW"
	PatternHammer          PatternType = "HAMMER"
	PatternHangingMan      PatternType = "HANGING_MAN"
	PatternInvertedHammer  PatternType = "INVERTED_HAMMER"
	PatternShootingStar    PatternType = "SHOOTING_STAR"
	PatternBullishMarubozu PatternType = "BULLISH_MARUBOZU"
	PatternBearishMarubozu PatternType = "BEARISH_MARUBOZU"

	// two-candle (4)
	PatternBullishEngulfing PatternType = "BULLISH_ENGULFING"
	PatternBearishEngulfing PatternType = "BEARISH_ENGULFING"
	PatternPiercingLine     PatternType = "PIERCING_LINE"
	PatternDarkCloudCover   PatternType = "DARK_CLOUD_COVER"
)

// Geometry is a pure SHAPE tag emitted by the Geometry layer (Phase 2). It is context-
// blind: it never encodes Stage / trend / near-high-low / volume. A bar-position can carry
// several geometries at once (multi-output). The two-bar geometries (engulf/piercing/dark-
// cloud) are raw shape only — they become formal reversal signals ONLY when the context
// gate passes (SPEC §7.1); otherwise they remain internal evidence.
type Geometry string

const (
	GeometryBullishBody     Geometry = "BULLISH_BODY"
	GeometryBearishBody     Geometry = "BEARISH_BODY"
	GeometryDoji            Geometry = "DOJI"
	GeometryMarubozu        Geometry = "MARUBOZU"
	GeometryLongUpperShadow Geometry = "LONG_UPPER_SHADOW"
	GeometryLongLowerShadow Geometry = "LONG_LOWER_SHADOW"

	// two-bar geometric relationships (pure shape; direction from colors, not context)
	GeometryBullishEngulf Geometry = "BULLISH_ENGULF"
	GeometryBearishEngulf Geometry = "BEARISH_ENGULF"
	GeometryPiercing      Geometry = "PIERCING"
	GeometryDarkCloud     Geometry = "DARK_CLOUD"
)

// PatternSignal is one interpreted pattern (SPEC §10, field types LOCKED). Adjustments are
// int (§1 correction). AppliedAdjustment is ALWAYS 0 in P0 — the "never touches score"
// invariant is a distinct field so it is explicit and greppable. SuggestedAdjustment may be
// non-zero only when ContextMatched is true, except the generic shadow fallbacks (±2).
type PatternSignal struct {
	Type                PatternType        `json:"type"`
	Direction           Direction          `json:"direction"`
	Confidence          float64            `json:"confidence"` // [0,1]
	ConfidenceReasons   []ConfidenceReason `json:"confidenceReasons,omitempty"`
	Strength            int                `json:"strength"` // 1..5 (evidence completeness + geometry quality)
	ContextMatched      bool               `json:"contextMatched"`
	Geometry            []Geometry         `json:"geometry,omitempty"` // provenance/debug
	Description         string             `json:"description"`        // 中文
	SuggestedAdjustment int                `json:"suggestedAdjustment"`
	AppliedAdjustment   int                `json:"appliedAdjustment"` // always 0 in P0
	BarOffset           int                `json:"barOffset"`         // 0 = latest bar
}

// ConfidenceReason is one additive evidence contribution to Confidence (SPEC §9.2). Storing
// the full list makes Confidence fully reconstructable — Confidence == Σ Delta — so a
// validator / HTML can explain and re-derive it with no hidden weights.
type ConfidenceReason struct {
	Code  string  `json:"code"`
	Delta float64 `json:"delta"`
}
