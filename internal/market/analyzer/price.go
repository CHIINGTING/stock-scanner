package analyzer

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/indicator"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// PriceAnalyzer — layer 1 of the structural regime engine.
//
// It reads ONLY the benchmark's own price history and answers "what shape is this trend
// in". Breadth and institutional flows are deliberately invisible here: the whole point of
// the layering is that the price skeleton can be judged, tested, and back-tested on its
// own before anything else is allowed to refine it.
//
// Pure function, no I/O. The caller supplies bars already trimmed to the analysis date
// (as-of discipline — see r6backtest for the same rule): passing a bar dated after the
// analysis date is look-ahead and this package cannot detect it.

// PriceView holds every quantity the structure classifier and the decision table branch on.
// It is returned alongside the Evidence so the classifier never has to recompute anything,
// and so a stored snapshot can be re-judged offline under different thresholds.
type PriceView struct {
	Benchmark model.Benchmark
	Date      string
	Bars      int // how many sessions were available

	Close  float64
	MA20   float64
	MA60   float64
	MA120  float64
	MA200  float64 // context only — no decision rule reads it
	Ret20  float64 // percent
	Ret60  float64 // percent
	DDHigh float64 // percent below the 60-day closing high, >= 0

	MA60Rising bool
	// WorstDay20 is the most negative single-session return in the last 20 sessions,
	// in percent. It is what disqualifies a correction from being "orderly".
	WorstDay20 float64
	// FailedHighs counts "touched the 60-day high, then closed below MA20 within
	// FailedHighWindowDays" inside the lookback window.
	FailedHighs int

	RealizedVol20 float64 // annualized, percent
	VolPercentile float64 // percentile of RealizedVol20 within its own lookback, 0..100
	VolPctKnown   bool    // false when history is too short to build the distribution

	// Sufficient is false when there were fewer than MinBenchmarkBars sessions. The
	// structure is then UNKNOWN and the decision table must return UNKNOWN — it must not
	// fall through to RANGE/SIDEWAYS.
	Sufficient bool
}

// AnalyzePrice derives the price view and the price Evidence from a benchmark series.
//
// bars must be oldest-first and already limited to the analysis date. A series shorter
// than th.MinBenchmarkBars yields MissingEvidence and Sufficient=false — insufficient
// history is MISSING, never a neutral reading.
func AnalyzePrice(benchmark model.Benchmark, bars []model.Bar, th model.StructureThresholds) (model.Evidence, PriceView) {
	th = th.Defaulted()

	v := PriceView{Benchmark: benchmark, Bars: len(bars)}
	if len(bars) > 0 {
		v.Date = bars[len(bars)-1].Date
	}
	if len(bars) < th.MinBenchmarkBars {
		return model.MissingEvidence(model.EvidencePrice,
			fmt.Sprintf("benchmark 歷史僅 %d 根，未達 %d 根門檻", len(bars), th.MinBenchmarkBars)), v
	}
	v.Sufficient = true

	closes := model.Closes(bars)
	last := len(closes) - 1
	v.Close = closes[last]

	v.MA20 = lastMA(closes, 20)
	v.MA60 = lastMA(closes, 60)
	v.MA120 = lastMA(closes, 120)
	v.MA200 = lastMA(closes, 200) // may be 0 when history < 200 — context only, never gates

	v.Ret20 = pctChange(closes, 20)
	v.Ret60 = pctChange(closes, 60)
	v.DDHigh = drawdownFromHigh(closes, 60)
	v.MA60Rising = maRising(closes, 60, th.MASlopeDays)
	v.WorstDay20 = worstSession(closes, 20)
	v.FailedHighs = countFailedHighs(closes, th)
	v.RealizedVol20, v.VolPercentile, v.VolPctKnown = realizedVolPercentile(closes, th)

	structure := ClassifyStructure(v, th)
	// Bar count alone does not guarantee a classifiable view: someone who lowers
	// MinBenchmarkBars below 120 gets enough bars to pass the gate but no MA120, and the
	// classifier correctly answers UNKNOWN. Evidence must agree with it rather than ship an
	// Available reading with no direction.
	if structure == model.StructureUnknown {
		return model.MissingEvidence(model.EvidencePrice,
			fmt.Sprintf("%d 根無法產生完整均線（需要 MA120）", len(bars))), v
	}
	dir, strength := priceDirection(structure, v)

	return model.Evidence{
		Source:     model.EvidencePrice,
		Direction:  dir,
		Strength:   strength,
		Confidence: priceConfidence(v, th),
		Summary:    priceSummary(structure, v),
		Metrics:    priceMetrics(v), // single source — see structure.go
		Available:  true,
	}, v
}

// priceDirection maps the structure onto the evidence axes. Direction follows the
// structure rather than a raw return so that "what the price evidence says" and "what the
// structure classifier saw" can never disagree.
func priceDirection(s model.PrimaryStructure, v PriceView) (model.Direction, model.Strength) {
	switch s {
	case model.StructureStrongUptrend:
		if v.Ret20 >= 10 {
			return model.DirBullish, model.StrengthExtreme
		}
		return model.DirBullish, model.StrengthStrong
	case model.StructureUptrend:
		return model.DirBullish, model.StrengthModerate
	case model.StructureRange:
		return model.DirNeutral, model.StrengthWeak
	case model.StructureWeakening:
		if v.Ret20 <= -8 {
			return model.DirBearish, model.StrengthModerate
		}
		return model.DirBearish, model.StrengthWeak
	case model.StructureDowntrend:
		if v.Ret20 <= -10 {
			return model.DirBearish, model.StrengthExtreme
		}
		return model.DirBearish, model.StrengthStrong
	}
	return model.DirUnknown, model.StrengthUnknown
}

// priceConfidence is this analyzer's own conviction, NOT the dashboard confidence. It is
// reduced when the series is barely long enough or the volatility distribution is missing,
// so a thin history lowers the weight this evidence carries instead of being silently
// treated as authoritative.
func priceConfidence(v PriceView, th model.StructureThresholds) float64 {
	c := 1.0
	if v.Bars < th.MinBenchmarkBars*2 {
		c -= 0.2
	}
	if !v.VolPctKnown {
		c -= 0.1
	}
	if v.MA200 == 0 {
		c -= 0.05 // long-term context unavailable; no rule needs it, but say so
	}
	if c < 0 {
		c = 0
	}
	return round2(c)
}

func priceSummary(s model.PrimaryStructure, v PriceView) string {
	return fmt.Sprintf("%s：收盤 %.2f，20日 %+.1f%%、60日 %+.1f%%，距 60 日高 -%.1f%%",
		structureLabel(s), v.Close, v.Ret20, v.Ret60, v.DDHigh)
}

func structureLabel(s model.PrimaryStructure) string {
	switch s {
	case model.StructureStrongUptrend:
		return "強勢多頭排列"
	case model.StructureUptrend:
		return "多頭"
	case model.StructureRange:
		return "區間震盪"
	case model.StructureWeakening:
		return "轉弱（長線支撐未破）"
	case model.StructureDowntrend:
		return "空頭"
	}
	return "資料不足"
}

// ── derived quantities ────────────────────────────────────────────────────────

// lastMA returns the final value of an n-period SMA, or 0 when history is too short.
// Reuses internal/indicator so the market layer and the scanner agree on what an MA is.
func lastMA(closes []float64, n int) float64 {
	if len(closes) < n {
		return 0
	}
	ma := indicator.SMA(closes, n)
	if len(ma) == 0 {
		return 0
	}
	return ma[len(ma)-1]
}

// pctChange is the percent return over the last n sessions.
func pctChange(closes []float64, n int) float64 {
	if len(closes) <= n {
		return 0
	}
	prev := closes[len(closes)-1-n]
	if prev <= 0 {
		return 0
	}
	return (closes[len(closes)-1]/prev - 1) * 100
}

// drawdownFromHigh is how far below the highest close of the last n sessions the latest
// close sits, as a POSITIVE percent (0 means at the high).
func drawdownFromHigh(closes []float64, n int) float64 {
	if len(closes) == 0 {
		return 0
	}
	start := len(closes) - n
	if start < 0 {
		start = 0
	}
	high := 0.0
	for _, c := range closes[start:] {
		if c > high {
			high = c
		}
	}
	if high <= 0 {
		return 0
	}
	dd := (1 - closes[len(closes)-1]/high) * 100
	if dd < 0 {
		dd = 0
	}
	return dd
}

// maRising reports whether the n-period MA is above its own value slopeDays sessions ago.
// The window is a config threshold (StructureThresholds.MASlopeDays) because layer 1
// branches on it — see that field for why it is a slope rather than yesterday-vs-today.
func maRising(closes []float64, n, slopeDays int) bool {
	if slopeDays <= 0 {
		slopeDays = 5
	}
	if len(closes) < n+slopeDays {
		return false
	}
	ma := indicator.SMA(closes, n)
	if len(ma) < slopeDays+1 {
		return false
	}
	return ma[len(ma)-1] > ma[len(ma)-1-slopeDays]
}

// worstSession is the most negative single-session percent return in the last n sessions.
func worstSession(closes []float64, n int) float64 {
	if len(closes) < 2 {
		return 0
	}
	start := len(closes) - n
	if start < 1 {
		start = 1
	}
	worst := 0.0
	for i := start; i < len(closes); i++ {
		if closes[i-1] <= 0 {
			continue
		}
		r := (closes[i]/closes[i-1] - 1) * 100
		if r < worst {
			worst = r
		}
	}
	return worst
}

// countFailedHighs counts the "repeated failure near highs" signature: a session that sets
// (or matches) the 60-day closing high, followed within FailedHighWindowDays by a close
// below MA20. Each qualifying high counts once.
func countFailedHighs(closes []float64, th model.StructureThresholds) int {
	if len(closes) < 60+th.FailedHighLookbackDays {
		return 0
	}
	ma20 := indicator.SMA(closes, 20)
	n := 0
	start := len(closes) - th.FailedHighLookbackDays
	for i := start; i < len(closes); i++ {
		if !isHigh60(closes, i) {
			continue
		}
		for j := i + 1; j <= i+th.FailedHighWindowDays && j < len(closes); j++ {
			if ma20[j] > 0 && closes[j] < ma20[j] {
				n++
				break
			}
		}
	}
	return n
}

// isHigh60 reports whether bar i is the highest close of the trailing 60 sessions.
func isHigh60(closes []float64, i int) bool {
	start := i - 59
	if start < 0 {
		start = 0
	}
	for k := start; k < i; k++ {
		if closes[k] > closes[i] {
			return false
		}
	}
	return true
}

// realizedVolPercentile returns annualized 20-day realized volatility, its percentile
// within the trailing VolLookbackDays distribution, and whether that distribution could be
// built at all.
//
// A percentile rather than an absolute threshold, because "high volatility" in 2024 and in
// a crisis year are different numbers; the percentile adapts. When history is too short the
// percentile is simply unknown — that degrades the HighVolatility flag only, it must never
// invalidate the regime.
func realizedVolPercentile(closes []float64, th model.StructureThresholds) (vol, pct float64, known bool) {
	series := realizedVolSeries(closes, 20)
	if len(series) == 0 {
		return 0, 0, false
	}
	vol = series[len(series)-1]

	start := len(series) - th.VolLookbackDays
	if start < 0 {
		start = 0
	}
	window := series[start:]
	// Too thin a distribution makes a percentile meaningless.
	if len(window) < 60 {
		return vol, 0, false
	}
	below := 0
	for _, x := range window {
		if x <= vol {
			below++
		}
	}
	return vol, float64(below) / float64(len(window)) * 100, true
}

// realizedVolSeries is the rolling annualized stdev of daily log-ish returns.
func realizedVolSeries(closes []float64, n int) []float64 {
	if len(closes) < n+1 {
		return nil
	}
	rets := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 {
			continue
		}
		rets[i-1] = closes[i]/closes[i-1] - 1
	}
	var out []float64
	for i := n - 1; i < len(rets); i++ {
		w := rets[i-n+1 : i+1]
		mean := 0.0
		for _, r := range w {
			mean += r
		}
		mean /= float64(n)
		varSum := 0.0
		for _, r := range w {
			d := r - mean
			varSum += d * d
		}
		out = append(out, math.Sqrt(varSum/float64(n))*math.Sqrt(252)*100)
	}
	return out
}

func round2(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return math.Round(x*100) / 100
}
