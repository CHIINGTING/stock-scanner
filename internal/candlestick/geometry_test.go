package candlestick

import "testing"

// mustMetrics computes metrics for a valid candle or fails the test.
func mustMetrics(t *testing.T, c Candle) Metrics {
	t.Helper()
	m, err := ComputeMetrics(c)
	if err != nil {
		t.Fatalf("ComputeMetrics(%+v): %v", c, err)
	}
	return m
}

func single(t *testing.T, c Candle) []Geometry {
	return DetectSingleBar(mustMetrics(t, c), DefaultThresholds())
}

func TestGeometryBullishMarubozu(t *testing.T) {
	// Dominant up body, negligible shadows.
	g := single(t, Candle{Open: 10, High: 20.5, Low: 9.5, Close: 20})
	if !hasGeometry(g, GeometryMarubozu) || !hasGeometry(g, GeometryBullishBody) {
		t.Fatalf("want {Marubozu, BullishBody}, got %v", g)
	}
	if hasGeometry(g, GeometryDoji) || hasGeometry(g, GeometryLongUpperShadow) || hasGeometry(g, GeometryLongLowerShadow) {
		t.Errorf("marubozu must not also be doji/long-shadow: %v", g)
	}
}

func TestGeometryLongLowerShadowMultiOutput(t *testing.T) {
	// Small up body near the top + long lower wick → {BullishBody, LongLowerShadow}.
	g := single(t, Candle{Open: 18.5, High: 20, Low: 15, Close: 19.5})
	if !hasGeometry(g, GeometryLongLowerShadow) {
		t.Fatalf("want LongLowerShadow, got %v", g)
	}
	if !hasGeometry(g, GeometryBullishBody) {
		t.Errorf("want BullishBody too (multi-output), got %v", g)
	}
	if len(g) < 2 {
		t.Errorf("geometry must be multi-output here, got %v", g)
	}
}

func TestGeometryLongUpperShadow(t *testing.T) {
	// Small up body near the bottom + long upper wick.
	g := single(t, Candle{Open: 14.8, High: 20, Low: 14.5, Close: 16})
	if !hasGeometry(g, GeometryLongUpperShadow) {
		t.Fatalf("want LongUpperShadow, got %v", g)
	}
	if hasGeometry(g, GeometryLongLowerShadow) {
		t.Errorf("must not also be long lower: %v", g)
	}
}

func TestGeometryDragonflyMultiOutput(t *testing.T) {
	// Tiny body at the top + long lower wick → BOTH Doji AND LongLowerShadow (future
	// Dragonfly). Proves geometry emits multiple tags; interpretation (P0) will pick DOJI.
	g := single(t, Candle{Open: 19.9, High: 20, Low: 15, Close: 20})
	if !hasGeometry(g, GeometryDoji) || !hasGeometry(g, GeometryLongLowerShadow) {
		t.Fatalf("want BOTH Doji and LongLowerShadow, got %v", g)
	}
}

func TestGeometryPlainDoji(t *testing.T) {
	g := single(t, Candle{Open: 15, High: 16, Low: 14, Close: 15.02})
	if !hasGeometry(g, GeometryDoji) {
		t.Fatalf("want Doji, got %v", g)
	}
	if hasGeometry(g, GeometryLongUpperShadow) || hasGeometry(g, GeometryLongLowerShadow) {
		t.Errorf("balanced doji must not be long-shadow: %v", g)
	}
}

func TestGeometryBodyColorAlwaysPresent(t *testing.T) {
	up := single(t, Candle{Open: 10, High: 11, Low: 9, Close: 10.6})
	if !hasGeometry(up, GeometryBullishBody) || hasGeometry(up, GeometryBearishBody) {
		t.Errorf("up bar: %v", up)
	}
	down := single(t, Candle{Open: 10.6, High: 11, Low: 9, Close: 10})
	if !hasGeometry(down, GeometryBearishBody) || hasGeometry(down, GeometryBullishBody) {
		t.Errorf("down bar: %v", down)
	}
	// exactly flat body → neither body-color tag (only if within doji band it's a doji)
	flat := single(t, Candle{Open: 10, High: 11, Low: 9, Close: 10})
	if hasGeometry(flat, GeometryBullishBody) || hasGeometry(flat, GeometryBearishBody) {
		t.Errorf("flat body must have no color tag: %v", flat)
	}
}

// ── two-bar ──────────────────────────────────────────────────────────────────

func twoBar(t *testing.T, prev, cur Candle) []Geometry {
	return DetectTwoBar(mustMetrics(t, prev), mustMetrics(t, cur))
}

func TestGeometryBullishEngulf(t *testing.T) {
	g := twoBar(t,
		Candle{Open: 12, High: 12.5, Low: 9.5, Close: 10}, // prev bearish
		Candle{Open: 9, High: 13.5, Low: 8.5, Close: 13})  // cur bullish, engulfs
	if !hasGeometry(g, GeometryBullishEngulf) {
		t.Fatalf("want BullishEngulf, got %v", g)
	}
	if hasGeometry(g, GeometryPiercing) {
		t.Errorf("engulf and piercing are exclusive: %v", g)
	}
}

func TestGeometryBearishEngulf(t *testing.T) {
	g := twoBar(t,
		Candle{Open: 10, High: 12.5, Low: 9.5, Close: 12}, // prev bullish
		Candle{Open: 13, High: 13.5, Low: 8.5, Close: 9})  // cur bearish, engulfs
	if !hasGeometry(g, GeometryBearishEngulf) {
		t.Fatalf("want BearishEngulf, got %v", g)
	}
}

func TestGeometryPiercing(t *testing.T) {
	// prev bearish body [10,12], mid 11; cur opens 9 (<10), closes 11.5 (>11, <12).
	g := twoBar(t,
		Candle{Open: 12, High: 12.5, Low: 9.5, Close: 10},
		Candle{Open: 9, High: 11.8, Low: 8.8, Close: 11.5})
	if !hasGeometry(g, GeometryPiercing) {
		t.Fatalf("want Piercing, got %v", g)
	}
	if hasGeometry(g, GeometryBullishEngulf) {
		t.Errorf("piercing must not be a full engulf: %v", g)
	}
}

func TestGeometryDarkCloud(t *testing.T) {
	// prev bullish body [10,12], mid 11; cur opens 13 (>12), closes 10.5 (<11, >10).
	g := twoBar(t,
		Candle{Open: 10, High: 12.5, Low: 9.5, Close: 12},
		Candle{Open: 13, High: 13.5, Low: 10.2, Close: 10.5})
	if !hasGeometry(g, GeometryDarkCloud) {
		t.Fatalf("want DarkCloud, got %v", g)
	}
}

// ── contract tests (SPEC §5.1 ordering, §5.2 boundary, §5.3 overlap) ─────────────

func TestGeometryOrderingContract(t *testing.T) {
	// Dragonfly-ish bar → {BullishBody, Doji, LongLowerShadow}: output MUST follow
	// geometryOrder, not condition-write order.
	g := single(t, Candle{Open: 19.9, High: 20, Low: 15, Close: 20})
	rank := map[Geometry]int{}
	for i, tag := range geometryOrder {
		rank[tag] = i
	}
	for i := 1; i < len(g); i++ {
		if rank[g[i-1]] >= rank[g[i]] {
			t.Fatalf("output not in canonical order: %v", g)
		}
	}
	// every emitted tag must be registered in the canonical order
	for _, tag := range g {
		if _, ok := rank[tag]; !ok {
			t.Errorf("emitted geometry %q not in geometryOrder", tag)
		}
	}
}

func TestGeometryBoundaryInclusive(t *testing.T) {
	// BodyPct == DojiBodyMax exactly (0.10) → Doji fires (inclusive <=).
	// range 10, body 1 → bodyPct 0.10; shadows 0.45 each (not long).
	g := single(t, Candle{Open: 10, High: 15.5, Low: 5.5, Close: 11})
	if !hasGeometry(g, GeometryDoji) {
		t.Errorf("BodyPct==DojiBodyMax must fire Doji (inclusive), got %v", g)
	}
	// UpperPct == LongShadowMin (0.50) AND LowerPct == ShortShadowMax (0.20) exactly.
	// range 10: max(O,C)=5, min(O,C)=2 → body 3 (0.30<=ShadowBodyMax), upper 5 (0.50), lower 2 (0.20).
	g2 := single(t, Candle{Open: 2, High: 10, Low: 0, Close: 5})
	if !hasGeometry(g2, GeometryLongUpperShadow) {
		t.Errorf("Upper==LongShadowMin & Lower==ShortShadowMax must fire LongUpperShadow, got %v", g2)
	}
}

func TestGeometryOverlapCannotCoexist(t *testing.T) {
	// A single bar can never be both bullish- and bearish-body.
	for _, c := range []Candle{
		{Open: 10, High: 12, Low: 8, Close: 11},
		{Open: 11, High: 12, Low: 8, Close: 10},
		{Open: 10, High: 11, Low: 9, Close: 10},
	} {
		g := single(t, c)
		if hasGeometry(g, GeometryBullishBody) && hasGeometry(g, GeometryBearishBody) {
			t.Errorf("bull+bear body coexist: %v", g)
		}
	}
	// Two-bar: engulf and piercing never coexist (checked in piercing test); bull/bear
	// engulf never coexist by construction.
	be := twoBar(t,
		Candle{Open: 12, High: 12.5, Low: 9.5, Close: 10},
		Candle{Open: 9, High: 13.5, Low: 8.5, Close: 13})
	if hasGeometry(be, GeometryBullishEngulf) && hasGeometry(be, GeometryBearishEngulf) {
		t.Errorf("bull+bear engulf coexist: %v", be)
	}
}

func TestGeometryOverlapCanCoexist(t *testing.T) {
	// Doji + LongLowerShadow legitimately coexist (future Dragonfly).
	g := single(t, Candle{Open: 19.9, High: 20, Low: 15, Close: 20})
	if !(hasGeometry(g, GeometryDoji) && hasGeometry(g, GeometryLongLowerShadow)) {
		t.Errorf("Doji+LongLowerShadow should coexist: %v", g)
	}
	// BullishBody + LongLowerShadow coexist.
	g2 := single(t, Candle{Open: 18.5, High: 20, Low: 15, Close: 19.5})
	if !(hasGeometry(g2, GeometryBullishBody) && hasGeometry(g2, GeometryLongLowerShadow)) {
		t.Errorf("BullishBody+LongLowerShadow should coexist: %v", g2)
	}
}

func TestGeometryTwoBarNegatives(t *testing.T) {
	// Two up bars → no engulf/piercing.
	g := twoBar(t,
		Candle{Open: 10, High: 11, Low: 9.5, Close: 10.8},
		Candle{Open: 10.9, High: 12, Low: 10.5, Close: 11.8})
	if len(g) != 0 {
		t.Errorf("two up bars must yield no two-bar geometry, got %v", g)
	}
	// Down then up but cur does not cover prior body → nothing.
	g2 := twoBar(t,
		Candle{Open: 12, High: 12.5, Low: 9.5, Close: 10},
		Candle{Open: 10.2, High: 10.8, Low: 10, Close: 10.6}) // small up bar inside prior body
	if len(g2) != 0 {
		t.Errorf("non-engulfing/non-piercing up bar must yield nothing, got %v", g2)
	}
}
