package candlestick

// geometry.go — Layer 3: Geometry Detection. PURE and context-blind: it reads only
// validated Metrics (+ single-bar thresholds) and knows nothing of Stage / MA / trend /
// near-high-low / volume (SPEC §5). It is MULTI-OUTPUT: one bar-position may carry several
// geometry tags at once.
//
// Ordering contract (SPEC §5.1): the output order is the canonical `geometryOrder`, NOT the
// order conditions happen to be written. Detection collects a SET; emission walks
// geometryOrder. Any NEW geometry must be inserted into geometryOrder at its proper place —
// output order is a formal contract, not an implementation accident.
//
// Boundary policy (SPEC §5.2): all thresholds are INCLUSIVE. "long / big" bounds use `>=`,
// "short / small" bounds use `<=`. So a ratio exactly equal to its threshold SATISFIES it.

// geometryOrder is the canonical, contractual emission order for every Geometry tag.
var geometryOrder = []Geometry{
	GeometryBullishBody,
	GeometryBearishBody,
	GeometryDoji,
	GeometryMarubozu,
	GeometryLongUpperShadow,
	GeometryLongLowerShadow,
	GeometryBullishEngulf,
	GeometryBearishEngulf,
	GeometryPiercing,
	GeometryDarkCloud,
}

// orderGeometries emits the detected set in canonical geometryOrder (deterministic; never
// depends on map iteration order).
func orderGeometries(set map[Geometry]bool) []Geometry {
	if len(set) == 0 {
		return nil
	}
	out := make([]Geometry, 0, len(set))
	for _, g := range geometryOrder {
		if set[g] {
			out = append(out, g)
		}
	}
	return out
}

// DetectSingleBar returns every single-bar geometry the metrics satisfy, in canonical order.
// Input must be a validated Metrics (Range > 0). Body-color primitives (Bullish/BearishBody)
// are always present for a non-flat body so Interpretation can compose them (Marubozu +
// BullishBody → BULLISH_MARUBOZU). Overlap policy: see SPEC §5.3.
func DetectSingleBar(m Metrics, th Thresholds) []Geometry {
	th = th.Defaulted()
	set := make(map[Geometry]bool, 4)

	if m.Bullish {
		set[GeometryBullishBody] = true
	}
	if m.Bearish {
		set[GeometryBearishBody] = true
	}
	if m.BodyPct <= th.DojiBodyMax { // inclusive
		set[GeometryDoji] = true
	}
	if m.BodyPct >= th.MarubozuBodyMin &&
		m.UpperPct <= th.MarubozuShadowMax &&
		m.LowerPct <= th.MarubozuShadowMax {
		set[GeometryMarubozu] = true
	}
	if m.UpperPct >= th.LongShadowMin &&
		m.LowerPct <= th.ShortShadowMax &&
		m.BodyPct <= th.ShadowBodyMax {
		set[GeometryLongUpperShadow] = true
	}
	if m.LowerPct >= th.LongShadowMin &&
		m.UpperPct <= th.ShortShadowMax &&
		m.BodyPct <= th.ShadowBodyMax {
		set[GeometryLongLowerShadow] = true
	}

	return orderGeometries(set)
}

// DetectTwoBar returns the two-bar geometric relationships between the previous and current
// bars, in canonical order. PURE: direction comes from candle colors + body relationship
// only, never from market context.
//
// ORDERING CONTRACT: prev MUST be chronologically EARLIER than cur. The caller guarantees
// time order; this function never swaps or sorts. Passing them reversed yields wrong (but
// still deterministic) results — do not do it.
//
// Both inputs must be validated Metrics. Engulf and piercing are mutually exclusive (engulf
// closes beyond the prior body, piercing within it), and bullish/bearish require opposite
// color pairs — so at most one fires in practice (SPEC §5.3 overlap policy).
func DetectTwoBar(prev, cur Metrics) []Geometry {
	set := make(map[Geometry]bool, 1)

	if prev.Bearish && cur.Bullish && cur.Open <= prev.Close && cur.Close >= prev.Open {
		set[GeometryBullishEngulf] = true
	}
	if prev.Bullish && cur.Bearish && cur.Open >= prev.Close && cur.Close <= prev.Open {
		set[GeometryBearishEngulf] = true
	}

	mid := (prev.Open + prev.Close) / 2
	if prev.Bearish && cur.Bullish &&
		cur.Open < prev.Close && cur.Close > mid && cur.Close < prev.Open {
		set[GeometryPiercing] = true
	}
	if prev.Bullish && cur.Bearish &&
		cur.Open > prev.Close && cur.Close < mid && cur.Close > prev.Open {
		set[GeometryDarkCloud] = true
	}

	return orderGeometries(set)
}

// hasGeometry reports whether tag is present (small internal helper; also handy in tests).
func hasGeometry(gs []Geometry, tag Geometry) bool {
	for _, g := range gs {
		if g == tag {
			return true
		}
	}
	return false
}
