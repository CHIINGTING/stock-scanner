package candlestick

// interpret.go — Layer 6: Interpretation. A deterministic rule engine that maps
// (Geometry set + MarketContext) → PatternSignal(s). It NEVER re-analyses OHLC and NEVER
// recomputes trend/return/volume/near — it consumes MarketContext only (SPEC §7).
//
// Phase 4 scope: Type / Direction / Geometry / Description / ContextMatched / BarOffset.
// Confidence / SuggestedAdjustment / AppliedAdjustment stay 0 (placeholders — Phase 5).
//
// Precedence (LOCKED, SPEC §7.2):
//   single-bar: DOJI > MARUBOZU(±) > {HAMMER/HANGING_MAN/INVERTED_HAMMER/SHOOTING_STAR}
//               > generic {LONG_UPPER_SHADOW/LONG_LOWER_SHADOW}. Generic body color
//               (BullishBody/BearishBody) is EVIDENCE only, never a signal on its own.
//   combined:   two-bar signals are prioritized over generic single-bar geometry; a formal
//               single-bar pattern always covers (suppresses) its generic shadow fallback.

// Interpret combines the latest bar's single-bar geometry and the (prev,cur) two-bar
// geometry into the emitted signals for the latest bar-position. P0 multi-signal policy
// (SPEC §9-interp): at most ONE two-bar + ONE single-bar signal, two-bar first, and the two
// never share a PatternType (disjoint families). Deterministic order.
func Interpret(singleGeo, twoGeo []Geometry, ctx MarketContext) []PatternSignal {
	var out []PatternSignal
	if s, ok := InterpretTwoBar(twoGeo, ctx); ok {
		out = append(out, s)
	}
	if s, ok := InterpretSingleBar(singleGeo, ctx); ok {
		out = append(out, s)
	}
	return out
}

// InterpretSingleBar returns at most one single-bar signal following the locked precedence.
// Returns ok=false when only generic body color (or nothing interpretable) is present.
func InterpretSingleBar(geo []Geometry, ctx MarketContext) (PatternSignal, bool) {
	// DOJI is independent and wins when the body is doji-small (SPEC §7: a doji+shadow bar
	// resolves to DOJI in P0; Dragonfly/Gravestone sub-types are P1).
	if hasGeometry(geo, GeometryDoji) {
		return makeSignal(PatternDoji, DirectionNeutral, []Geometry{GeometryDoji}, false), true
	}

	// MARUBOZU: directional body shape. Emitted even when context disagrees (then
	// ContextMatched=false); direction always by body color.
	if hasGeometry(geo, GeometryMarubozu) {
		if hasGeometry(geo, GeometryBullishBody) {
			return makeSignal(PatternBullishMarubozu, DirectionBullish,
				[]Geometry{GeometryMarubozu, GeometryBullishBody}, ctx.Uptrend), true
		}
		if hasGeometry(geo, GeometryBearishBody) {
			return makeSignal(PatternBearishMarubozu, DirectionBearish,
				[]Geometry{GeometryMarubozu, GeometryBearishBody}, ctx.Downtrend), true
		}
		// marubozu requires a dominant body → a color tag is always present; no flat case.
	}

	// Long-shadow family: context decides the formal name, else generic fallback.
	if hasGeometry(geo, GeometryLongUpperShadow) {
		return shadowSignal(true, ctx), true
	}
	if hasGeometry(geo, GeometryLongLowerShadow) {
		return shadowSignal(false, ctx), true
	}

	return PatternSignal{}, false
}

// shadowSignal resolves a long-shadow geometry to a context-named pattern or the neutral
// generic fallback. Context conflict (both bullish- and bearish-supporting) or absent/
// insufficient context → generic fallback (never a hard pick) — SPEC §7 conflict rule.
func shadowSignal(upper bool, ctx MarketContext) PatternSignal {
	bullishCtx := ctx.Downtrend || ctx.Near20DLow // reversal-up backdrop
	bearishCtx := ctx.Uptrend || ctx.Near20DHigh  // reversal-down backdrop
	conflict := bullishCtx && bearishCtx

	if conflict || (!bullishCtx && !bearishCtx) {
		if upper {
			return makeSignal(PatternLongUpperShadow, DirectionNeutral, []Geometry{GeometryLongUpperShadow}, false)
		}
		return makeSignal(PatternLongLowerShadow, DirectionNeutral, []Geometry{GeometryLongLowerShadow}, false)
	}
	if bullishCtx {
		if upper {
			return makeSignal(PatternInvertedHammer, DirectionBullish, []Geometry{GeometryLongUpperShadow}, true)
		}
		return makeSignal(PatternHammer, DirectionBullish, []Geometry{GeometryLongLowerShadow}, true)
	}
	// bearishCtx
	if upper {
		return makeSignal(PatternShootingStar, DirectionBearish, []Geometry{GeometryLongUpperShadow}, true)
	}
	return makeSignal(PatternHangingMan, DirectionBearish, []Geometry{GeometryLongLowerShadow}, true)
}

// InterpretTwoBar returns at most one two-bar reversal signal. These are CONTEXT-GATED: the
// geometry may exist, but without a supporting prior trend / near-extreme NO formal signal is
// emitted (SPEC §7.1). Returns ok=false when gated out.
func InterpretTwoBar(geo []Geometry, ctx MarketContext) (PatternSignal, bool) {
	bullishCtx := ctx.Downtrend || ctx.Near20DLow
	bearishCtx := ctx.Uptrend || ctx.Near20DHigh

	switch {
	case hasGeometry(geo, GeometryBullishEngulf) && bullishCtx:
		return makeSignal(PatternBullishEngulfing, DirectionBullish, []Geometry{GeometryBullishEngulf}, true), true
	case hasGeometry(geo, GeometryBearishEngulf) && bearishCtx:
		return makeSignal(PatternBearishEngulfing, DirectionBearish, []Geometry{GeometryBearishEngulf}, true), true
	case hasGeometry(geo, GeometryPiercing) && bullishCtx:
		return makeSignal(PatternPiercingLine, DirectionBullish, []Geometry{GeometryPiercing}, true), true
	case hasGeometry(geo, GeometryDarkCloud) && bearishCtx:
		return makeSignal(PatternDarkCloudCover, DirectionBearish, []Geometry{GeometryDarkCloud}, true), true
	}
	return PatternSignal{}, false
}

// makeSignal builds a Phase-4 PatternSignal with Confidence/adjustments left at 0 (Phase 5).
func makeSignal(t PatternType, d Direction, geo []Geometry, ctxMatched bool) PatternSignal {
	return PatternSignal{
		Type:                t,
		Direction:           d,
		Confidence:          0, // Phase 5
		ContextMatched:      ctxMatched,
		Geometry:            geo,
		Description:         descriptionFor(t, ctxMatched),
		SuggestedAdjustment: 0, // Phase 5
		AppliedAdjustment:   0, // always 0
		BarOffset:           0, // latest bar
	}
}

// descriptionFor returns the fixed Phase-4 template per type. No confidence/strength/
// adjustment wording (that is later). Marubozu appends a position-risk note when its trend
// context did not align.
func descriptionFor(t PatternType, ctxMatched bool) string {
	switch t {
	case PatternHammer:
		return "下跌／低檔出現長下影線，符合 Hammer 型態。"
	case PatternHangingMan:
		return "上漲／高檔出現長下影線，符合 Hanging Man 型態。"
	case PatternInvertedHammer:
		return "下跌／低檔出現長上影線，符合 Inverted Hammer 型態。"
	case PatternShootingStar:
		return "上漲／高檔出現長上影線，符合 Shooting Star 型態。"
	case PatternLongLowerShadow:
		return "長下影線，但缺乏足夠反轉背景。"
	case PatternLongUpperShadow:
		return "長上影線，但缺乏足夠反轉背景。"
	case PatternDoji:
		return "十字線，多空力量接近平衡。"
	case PatternBullishMarubozu:
		s := "光頭光腳陽線（Bullish Marubozu），買方主導。"
		if !ctxMatched {
			s += "（趨勢未同向，留意位置風險）"
		}
		return s
	case PatternBearishMarubozu:
		s := "光頭光腳陰線（Bearish Marubozu），賣方主導。"
		if !ctxMatched {
			s += "（趨勢未同向，留意位置風險）"
		}
		return s
	case PatternBullishEngulfing:
		return "下跌／低檔出現多頭吞噬，符合 Bullish Engulfing 反轉型態。"
	case PatternBearishEngulfing:
		return "上漲／高檔出現空頭吞噬，符合 Bearish Engulfing 反轉型態。"
	case PatternPiercingLine:
		return "下跌／低檔出現貫穿線（Piercing Line）反轉型態。"
	case PatternDarkCloudCover:
		return "上漲／高檔出現烏雲罩頂（Dark Cloud Cover）反轉型態。"
	default:
		return ""
	}
}
