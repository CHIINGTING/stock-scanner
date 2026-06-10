package scanner

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// hhSeries builds candles from an explicit close series with a controllable
// high/low spread (as a fraction of close), so ATR% can be steered for the
// compression tests. Distinct name from the package-level makeCandles/bar.
func hhSeries(closes []float64, spreadFrac float64) []fetcher.Candle {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]fetcher.Candle, len(closes))
	for i, c := range closes {
		out[i] = fetcher.Candle{
			Date:   base.AddDate(0, 0, i),
			Open:   c,
			High:   c * (1 + spreadFrac/2),
			Low:    c * (1 - spreadFrac/2),
			Close:  c,
			Volume: 1_000_000,
		}
	}
	return out
}

func hhConst(n int, v float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func hhRamp(n int, start, step float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = start + step*float64(i)
	}
	return s
}

// ── Tests 1–5: stage → bucket → day-range rule table (deterministic scalars) ──

func TestHHCalcStageRuleTable(t *testing.T) {
	cases := []struct {
		name                          string
		price, ma20, ma60, slope, hi  float64
		wantStage                     HoldingStage
		wantBucket                    HoldingHorizonBucket
		wantMin, wantMax              int
	}{
		{"BASE", 100, 100, 100, 0.0, 100, HHStageBase, HoldingMedium, 10, 20},
		{"BREAKOUT", 110, 105, 100, 1.0, 110, HHStageBreakout, HoldingLong, 20, 30},
		{"UPTREND", 110, 105, 100, 1.0, 120, HHStageUptrend, HoldingMedium, 10, 20},
		{"LATE_UPTREND", 130, 120, 100, 1.0, 130, HHStageLateUptrend, HoldingShort, 5, 10},
		{"DISTRIBUTION", 90, 95, 100, -1.0, 120, HHStageDistribution, HoldingObserve, 0, 0},
	}
	for _, c := range cases {
		gotStage := hhCalcStage(c.price, c.ma20, c.ma60, c.slope, c.hi)
		if gotStage != c.wantStage {
			t.Errorf("%s: stage=%s want %s", c.name, gotStage, c.wantStage)
		}
		gotBucket := hhStageBucket(gotStage)
		if gotBucket != c.wantBucket {
			t.Errorf("%s: bucket=%s want %s", c.name, gotBucket, c.wantBucket)
		}
		min, max := hhBucketDays(gotBucket)
		if min != c.wantMin || max != c.wantMax {
			t.Errorf("%s: days=%d-%d want %d-%d", c.name, min, max, c.wantMin, c.wantMax)
		}
	}
}

// ── Test 6: high ATR shrinks a non-OBSERVE bucket by one tier ──

func TestHHATRCompression(t *testing.T) {
	cfg := holdingHorizonConfigFrom(Config{EnableHoldingHorizon: true})

	// BASE (constant) → base bucket MEDIUM. Low spread: no compression.
	calm := computeHoldingHorizon(hhSeries(hhConst(80, 100), 0.005), cfg)
	if !calm.Computed || calm.Stage != HHStageBase {
		t.Fatalf("calm: computed=%v stage=%s", calm.Computed, calm.Stage)
	}
	if calm.Bucket != HoldingMedium || calm.ATRCompressed {
		t.Errorf("calm: bucket=%s compressed=%v want MEDIUM/false", calm.Bucket, calm.ATRCompressed)
	}

	// Same stage, wide spread → ATR% > 4 → MEDIUM shrinks to SHORT.
	vol := computeHoldingHorizon(hhSeries(hhConst(80, 100), 0.08), cfg)
	if vol.Stage != HHStageBase {
		t.Fatalf("vol: stage=%s want BASE", vol.Stage)
	}
	if vol.Bucket != HoldingShort || !vol.ATRCompressed {
		t.Errorf("vol: bucket=%s compressed=%v want SHORT/true (atr%%=%.2f)", vol.Bucket, vol.ATRCompressed, vol.ATRPct)
	}
	if vol.MinDays != 5 || vol.MaxDays != 10 {
		t.Errorf("vol: days=%d-%d want 5-10", vol.MinDays, vol.MaxDays)
	}
}

// ── Test 7: OBSERVE is never adjusted by ATR ──

func TestHHObserveNotCompressed(t *testing.T) {
	cfg := holdingHorizonConfigFrom(Config{EnableHoldingHorizon: true})
	// Declining series below MA60 with negative slope → DISTRIBUTION → OBSERVE.
	dist := computeHoldingHorizon(hhSeries(hhRamp(80, 130, -0.5), 0.08), cfg)
	if dist.Stage != HHStageDistribution {
		t.Fatalf("stage=%s want DISTRIBUTION", dist.Stage)
	}
	if dist.Bucket != HoldingObserve {
		t.Errorf("bucket=%s want OBSERVE", dist.Bucket)
	}
	if dist.ATRCompressed {
		t.Errorf("OBSERVE must not be ATR-compressed (atr%%=%.2f)", dist.ATRPct)
	}
	if dist.MinDays != 0 || dist.MaxDays != 0 {
		t.Errorf("days=%d-%d want 0-0", dist.MinDays, dist.MaxDays)
	}
	if len(dist.Warnings) == 0 {
		t.Errorf("distribution should carry a warning")
	}
}

// ── Test 8: insufficient history → Computed=false, zero-valued ──

func TestHHInsufficientHistory(t *testing.T) {
	cfg := holdingHorizonConfigFrom(Config{EnableHoldingHorizon: true}) // min 70
	r := computeHoldingHorizon(hhSeries(hhConst(50, 100), 0.01), cfg)
	if r.Computed {
		t.Errorf("expected Computed=false with 50 candles (<70)")
	}
	if r.Bucket != "" || r.Stage != "" || r.MinDays != 0 || r.MaxDays != 0 {
		t.Errorf("insufficient result should be zero-valued, got %+v", r)
	}
}

// ── Test 11: no look-ahead — only the as-of window matters ──

func TestHHNoLookAhead(t *testing.T) {
	cfg := holdingHorizonConfigFrom(Config{EnableHoldingHorizon: true})
	full := hhRamp(90, 80, 0.4)

	asof := computeHoldingHorizon(hhSeries(full[:75], 0.02), cfg)

	// Mutate the FUTURE bars (index >= 75) drastically, then recompute on the same
	// as-of prefix. The result must be identical — the function cannot peek ahead.
	mutated := append([]float64(nil), full...)
	for i := 75; i < len(mutated); i++ {
		mutated[i] = 9999
	}
	asofAgain := computeHoldingHorizon(hhSeries(mutated[:75], 0.02), cfg)

	if asof.Computed != asofAgain.Computed || asof.Stage != asofAgain.Stage ||
		asof.Bucket != asofAgain.Bucket || asof.MinDays != asofAgain.MinDays ||
		asof.MaxDays != asofAgain.MaxDays || asof.ATRPct != asofAgain.ATRPct ||
		asof.ATRCompressed != asofAgain.ATRCompressed {
		t.Errorf("look-ahead leak: %+v vs %+v", asof, asofAgain)
	}
}

// ── Tests 9 & 10: pipeline gating + zero scoring impact ──
//
// flag-off → HoldingHorizon nil; flag-on → attached & Computed; and in BOTH cases
// RocketScore / WatchAction / ExplosionProb / order are identical (no scoring impact).
func TestHHShadowGatingAndNoScoringImpact(t *testing.T) {
	items := []fetcher.StockData{
		{Symbol: "2222", Name: "Flat", Source: "watchlist", Candles: makeCandles(260, 50, 0.0, 1_000_000)},
		{Symbol: "1111", Name: "Strong", Source: "watchlist", Candles: makeCandles(260, 50, 0.4, 2_000_000)},
	}
	sectorOf := map[string]string{}
	rot := map[string]*SectorRotation{}
	members := map[string][]fetcher.StockData{}

	off := New(Config{})
	resOff := off.EnrichWatchlist(items, sectorOf, rot, members, nil)

	on := New(Config{EnableHoldingHorizon: true})
	resOn := on.EnrichWatchlist(items, sectorOf, rot, members, nil)

	if len(resOff) != len(resOn) {
		t.Fatalf("length differs: off=%d on=%d", len(resOff), len(resOn))
	}
	for i := range resOff {
		// flag-off: never computed.
		if resOff[i].HoldingHorizon != nil {
			t.Errorf("%s: HoldingHorizon must be nil when flag off", resOff[i].A.Symbol)
		}
		// flag-on: attached and computed (260 candles suffice).
		if resOn[i].HoldingHorizon == nil {
			t.Fatalf("%s: HoldingHorizon must be attached when flag on", resOn[i].A.Symbol)
		}
		if !resOn[i].HoldingHorizon.Computed {
			t.Errorf("%s: HoldingHorizon should be Computed with 260 candles", resOn[i].A.Symbol)
		}
		// HoldingHorizon is NOT inside ShadowSignals.
		if resOn[i].Shadow != nil {
			t.Errorf("%s: enabling HoldingHorizon must not populate ShadowSignals", resOn[i].A.Symbol)
		}
		// Zero scoring impact: order + score + action + probability unchanged.
		if resOff[i].A.Symbol != resOn[i].A.Symbol {
			t.Errorf("order changed at %d: %s vs %s", i, resOff[i].A.Symbol, resOn[i].A.Symbol)
		}
		if resOff[i].RocketScore != resOn[i].RocketScore {
			t.Errorf("%s RocketScore changed: %d vs %d", resOff[i].A.Symbol, resOff[i].RocketScore, resOn[i].RocketScore)
		}
		if resOff[i].WatchAction != resOn[i].WatchAction {
			t.Errorf("%s WatchAction changed: %s vs %s", resOff[i].A.Symbol, resOff[i].WatchAction, resOn[i].WatchAction)
		}
		if resOff[i].ExplosionProb != resOn[i].ExplosionProb {
			t.Errorf("%s ExplosionProb changed: %s vs %s", resOff[i].A.Symbol, resOff[i].ExplosionProb, resOn[i].ExplosionProb)
		}
	}
}
