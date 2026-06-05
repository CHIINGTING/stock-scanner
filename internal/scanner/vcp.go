package scanner

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────
// VCP — Volatility Contraction Pattern (C4)
//
// 偵測「一段段收縮、越收越緊、量越縮」的盤整型態（Minervini 概念，非照抄）。
// C4 只建立「資料模型 + helper + config + 測試」，不接入既有 scoring / report /
// watchlist / rotation。所有函式皆為 pure，只在被明確呼叫時運算；EnableVCP=false 時
// pipeline 不呼叫它們（golden regression by construction）。VCP 如何影響分數屬後續 C6。
//
// 價格一律以「還原收盤」計算（close-based）：swing 偵測、peak/trough/depth、區間高低都
// 走 fetcher.PriceForCalc，絕不混用 raw High/Low（語意一致）。成交量唯讀共用 avgVolume。
// ──────────────────────────────────────────────────────────────────────────────

// VCPGrade is the validated VCP tier (by contraction count).
type VCPGrade string

const (
	VCPGradeNone        VCPGrade = "NONE"
	VCPGradeEarly       VCPGrade = "EARLY_VCP"        // 2 contractions
	VCPGradeStandard    VCPGrade = "STANDARD_VCP"     // 3 contractions
	VCPGradeHighQuality VCPGrade = "HIGH_QUALITY_VCP" // >= 4 contractions
)

// Defaults applied when config leaves a knob zero.
const (
	defaultVCPLookbackDays    = 60
	defaultVCPMinHistoryDays  = 40
	defaultVCPMinContractions = 2
	defaultVCPMinQuality      = 70
	defaultVCPTightnessW      = 30
	defaultVCPVolumeDryUpW    = 25
	defaultVCPMonotonicW      = 20
	defaultVCPSupportHoldW    = 15
	defaultVCPNearBreakoutW   = 10
	defaultVCPZigzagRevPct    = 2.5 // R5-2: raised from 1.5 (1.5 over-segmented daily noise)
	defaultVCPMinDepthPct     = 2   // R5-2: drop interior contractions shallower than this
	defaultVCPMaxContractions = 5   // R5-2: keep only the most recent N significant legs

	// Internal scoring bounds (待 R5 校準).
	vcpTightTargetPct = 5.0  // final contraction ≤ this → full tightness
	vcpTightLoosePct  = 20.0 // final contraction ≥ this → zero tightness
	vcpDryFullRatio   = 0.5  // last/first leg volume ≤ this → full dry-up
	vcpDryZeroRatio   = 1.0  // ratio ≥ this → zero dry-up
	vcpNearTargetPct  = 3.0  // within this of base high → full near-breakout
	vcpNearLoosePct   = 15.0 // beyond this → zero
	vcpBigBlackVolX   = 2.0  // volume ≥ X × window avg …
	vcpBigBlackDrop   = -0.03 // … and (close-open)/open ≤ this → destructive long black
	vcpBigBlackPenalty = 40.0
)

// VCPConfig is the resolved VCP configuration (defaults applied; weights sum 100).
type VCPConfig struct {
	Enable           bool
	LookbackDays     int
	MinHistoryDays   int
	MinContractions  int
	MinQualityScore  float64
	UseAdjustedClose bool
	ZigzagReversal   float64

	// R5-2 contraction refinement.
	MinContractionDepthPct float64 // drop interior legs shallower than this (last leg always kept)
	MaxContractions        int     // keep only the most recent N legs

	WTightness    float64
	WVolumeDryUp  float64
	WMonotonic    float64
	WSupportHold  float64
	WNearBreakout float64
}

// vcpConfigFrom resolves a VCPConfig from the scanner Config, applying defaults.
func vcpConfigFrom(cfg Config) VCPConfig {
	vc := VCPConfig{
		Enable:           cfg.EnableVCP,
		LookbackDays:     cfg.VCPLookbackDays,
		MinHistoryDays:   cfg.VCPMinHistoryDays,
		MinContractions:  cfg.VCPMinContractions,
		MinQualityScore:  cfg.VCPMinQualityScore,
		UseAdjustedClose:       cfg.UseAdjustedClose || cfg.VCPUseAdjustedClose,
		ZigzagReversal:         cfg.VCPZigzagReversalPct,
		MinContractionDepthPct: cfg.VCPMinContractionDepthPct,
		MaxContractions:        cfg.VCPMaxContractions,
		WTightness:             cfg.VCPTightnessWeight,
		WVolumeDryUp:     cfg.VCPVolumeDryUpWeight,
		WMonotonic:       cfg.VCPMonotonicWeight,
		WSupportHold:     cfg.VCPSupportHoldWeight,
		WNearBreakout:    cfg.VCPNearBreakoutWeight,
	}
	if vc.LookbackDays <= 0 {
		vc.LookbackDays = defaultVCPLookbackDays
	}
	if vc.MinHistoryDays <= 0 {
		vc.MinHistoryDays = defaultVCPMinHistoryDays
	}
	if vc.MinContractions <= 0 {
		vc.MinContractions = defaultVCPMinContractions
	}
	if vc.MinQualityScore <= 0 {
		vc.MinQualityScore = defaultVCPMinQuality
	}
	if vc.ZigzagReversal <= 0 {
		vc.ZigzagReversal = defaultVCPZigzagRevPct
	}
	if vc.MinContractionDepthPct <= 0 {
		vc.MinContractionDepthPct = defaultVCPMinDepthPct
	}
	if vc.MaxContractions <= 0 {
		vc.MaxContractions = defaultVCPMaxContractions
	}
	if vc.WTightness <= 0 {
		vc.WTightness = defaultVCPTightnessW
	}
	if vc.WVolumeDryUp <= 0 {
		vc.WVolumeDryUp = defaultVCPVolumeDryUpW
	}
	if vc.WMonotonic <= 0 {
		vc.WMonotonic = defaultVCPMonotonicW
	}
	if vc.WSupportHold <= 0 {
		vc.WSupportHold = defaultVCPSupportHoldW
	}
	if vc.WNearBreakout <= 0 {
		vc.WNearBreakout = defaultVCPNearBreakoutW
	}
	return vc
}

// Contraction is one peak→trough leg, measured on adjusted close.
type Contraction struct {
	PeakIdx, TroughIdx     int
	PeakPrice, TroughPrice float64
	DepthPct               float64 // (peak-trough)/peak*100
	AvgVolume              float64 // average raw volume over [peakIdx, troughIdx]
}

// VCPQuality is the quality-score breakdown (each component 0–100; QualityScore 0–100).
type VCPQuality struct {
	QualityScore      float64
	TightnessScore    float64
	VolumeDryUpScore  float64
	MonotonicScore    float64
	SupportHoldScore  float64
	NearBreakoutScore float64
}

// VCPResult is the full VCP analysis for one stock.
type VCPResult struct {
	Computed bool
	Valid    bool

	ContractionCount int
	Grade            VCPGrade

	QualityScore      float64
	TightnessScore    float64
	VolumeDryUpScore  float64
	MonotonicScore    float64
	SupportHoldScore  float64
	NearBreakoutScore float64

	Depths       []float64 // per-contraction depth %, oldest-first
	LastRangePct float64
	MaxRangePct  float64
	MinRangePct  float64

	Reason  []string
	Warning []string
}

// pivot is one zigzag swing point on the adjusted-close series.
type pivot struct {
	idx    int
	price  float64
	isHigh bool
}

// ComputeVCP analyses the most recent VCP window. Pure: does not mutate inputs.
func ComputeVCP(candles []fetcher.Candle, cfg VCPConfig) VCPResult {
	var r VCPResult
	n := len(candles)
	if n < cfg.MinHistoryDays {
		return r // Computed=false
	}

	cons := detectContractions(candles, cfg)
	cons = refineContractions(cons, cfg) // R5-2: drop interior noise, keep recent legs
	r.Computed = true
	r.ContractionCount = len(cons)
	for _, c := range cons {
		r.Depths = append(r.Depths, c.DepthPct)
	}
	r.LastRangePct, r.MaxRangePct, r.MinRangePct = rangeStats(r.Depths)

	if r.ContractionCount < cfg.MinContractions {
		r.Grade = VCPGradeNone
		r.Warning = append(r.Warning, fmt.Sprintf("收縮段數不足（%d < %d）", r.ContractionCount, cfg.MinContractions))
		return r
	}

	q := vcpQualityScore(cons, candles, cfg)
	r.QualityScore = q.QualityScore
	r.TightnessScore = q.TightnessScore
	r.VolumeDryUpScore = q.VolumeDryUpScore
	r.MonotonicScore = q.MonotonicScore
	r.SupportHoldScore = q.SupportHoldScore
	r.NearBreakoutScore = q.NearBreakoutScore

	r.Valid = r.ContractionCount >= cfg.MinContractions && q.QualityScore >= cfg.MinQualityScore
	if r.Valid {
		r.Grade = gradeByCount(r.ContractionCount)
		r.Reason = append(r.Reason, vcpReason(r.Depths, q))
	} else {
		r.Grade = VCPGradeNone
	}
	r.Warning = append(r.Warning, vcpWarnings(q)...)
	return r
}

// detectContractions finds peak→trough legs via a close-based zigzag over the window.
func detectContractions(candles []fetcher.Candle, cfg VCPConfig) []Contraction {
	n := len(candles)
	start := n - cfg.LookbackDays
	if start < 0 {
		start = 0
	}
	prices := make([]float64, n)
	for i, c := range candles {
		prices[i] = fetcher.PriceForCalc(c, cfg.UseAdjustedClose)
	}
	pivots := zigzagPivots(prices, start, n-1, cfg.ZigzagReversal)

	var out []Contraction
	for i := 0; i+1 < len(pivots); i++ {
		if pivots[i].isHigh && !pivots[i+1].isHigh {
			peak, trough := pivots[i], pivots[i+1]
			if peak.price <= 0 || trough.price >= peak.price {
				continue
			}
			out = append(out, Contraction{
				PeakIdx:     peak.idx,
				TroughIdx:   trough.idx,
				PeakPrice:   peak.price,
				TroughPrice: trough.price,
				DepthPct:    (peak.price - trough.price) / peak.price * 100,
				AvgVolume:   avgVolume(candles, peak.idx, trough.idx),
			})
		}
	}
	return out
}

// refineContractions (R5-2) trims noisy detection to the meaningful recent legs:
//  1. drop interior legs shallower than MinContractionDepthPct — but ALWAYS keep the
//     most recent leg (the final tight compression is the VCP signal, even if small);
//  2. keep only the most recent MaxContractions legs.
//
// Order (oldest-first) is preserved for monotonic / volume-dry-up / UI.
func refineContractions(cons []Contraction, cfg VCPConfig) []Contraction {
	if len(cons) == 0 {
		return cons
	}
	last := len(cons) - 1
	filtered := make([]Contraction, 0, len(cons))
	for i, c := range cons {
		if i == last || c.DepthPct >= cfg.MinContractionDepthPct {
			filtered = append(filtered, c)
		}
	}
	if cfg.MaxContractions > 0 && len(filtered) > cfg.MaxContractions {
		filtered = filtered[len(filtered)-cfg.MaxContractions:]
	}
	return filtered
}

// zigzagPivots returns alternating swing highs/lows over prices[lo..hi] (inclusive),
// confirming a reversal when price retraces >= reversalPct from the running extreme.
func zigzagPivots(prices []float64, lo, hi int, reversalPct float64) []pivot {
	if hi <= lo {
		return nil
	}
	var piv []pivot
	startIdx, startPrice := lo, prices[lo]
	curExtIdx, curExt := lo, prices[lo]
	dir := 0 // 0 unknown, 1 up, -1 down
	for i := lo + 1; i <= hi; i++ {
		p := prices[i]
		switch dir {
		case 0:
			if startPrice > 0 && (p-startPrice)/startPrice*100 >= reversalPct {
				dir = 1
				piv = append(piv, pivot{startIdx, startPrice, false}) // start was a swing low
				curExt, curExtIdx = p, i
			} else if startPrice > 0 && (startPrice-p)/startPrice*100 >= reversalPct {
				dir = -1
				piv = append(piv, pivot{startIdx, startPrice, true}) // start was a swing high
				curExt, curExtIdx = p, i
			}
		case 1: // up-leg: track high, reverse on drop >= reversalPct
			if p > curExt {
				curExt, curExtIdx = p, i
			}
			if curExt > 0 && (curExt-p)/curExt*100 >= reversalPct {
				piv = append(piv, pivot{curExtIdx, curExt, true})
				dir = -1
				curExt, curExtIdx = p, i
			}
		case -1: // down-leg: track low, reverse on rise >= reversalPct
			if p < curExt {
				curExt, curExtIdx = p, i
			}
			if curExt > 0 && (p-curExt)/curExt*100 >= reversalPct {
				piv = append(piv, pivot{curExtIdx, curExt, false})
				dir = 1
				curExt, curExtIdx = p, i
			}
		}
	}
	// Close the final leg with the running extreme (isHigh = currently in an up-leg).
	if dir != 0 {
		piv = append(piv, pivot{curExtIdx, curExt, dir == 1})
	}
	return piv
}

// vcpQualityScore computes the weighted quality breakdown (each component 0–100).
func vcpQualityScore(cons []Contraction, candles []fetcher.Candle, cfg VCPConfig) VCPQuality {
	var q VCPQuality
	k := len(cons)
	if k == 0 {
		return q
	}
	depths := make([]float64, k)
	for i, c := range cons {
		depths[i] = c.DepthPct
	}

	// Tightness: final contraction depth → tighter = higher.
	last := depths[k-1]
	q.TightnessScore = clampFloat((vcpTightLoosePct-last)/(vcpTightLoosePct-vcpTightTargetPct)*100, 0, 100)

	// Volume dry-up: last leg avg volume / first leg avg volume → drier = higher.
	first := cons[0].AvgVolume
	lastVol := cons[k-1].AvgVolume
	if first > 0 {
		ratio := lastVol / first
		q.VolumeDryUpScore = clampFloat((vcpDryZeroRatio-ratio)/(vcpDryZeroRatio-vcpDryFullRatio)*100, 0, 100)
	}

	// Monotonic: fraction of adjacent pairs where depth strictly decreases.
	if k >= 2 {
		dec := 0
		for i := 1; i < k; i++ {
			if depths[i] < depths[i-1] {
				dec++
			}
		}
		q.MonotonicScore = float64(dec) / float64(k-1) * 100
	} else {
		q.MonotonicScore = 100
	}

	// Support hold: higher-lows fraction across troughs, minus destructive-bar penalty.
	hl := 0
	for i := 1; i < k; i++ {
		if cons[i].TroughPrice >= cons[i-1].TroughPrice {
			hl++
		}
	}
	support := 100.0
	if k >= 2 {
		support = float64(hl) / float64(k-1) * 100
	}
	if hasBigBlackBar(candles, cons[0].PeakIdx, len(candles)-1) {
		support -= vcpBigBlackPenalty
	}
	q.SupportHoldScore = clampFloat(support, 0, 100)

	// Near breakout: current adjusted close vs the base's highest peak.
	basePeak := 0.0
	for _, c := range cons {
		if c.PeakPrice > basePeak {
			basePeak = c.PeakPrice
		}
	}
	cur := fetcher.PriceForCalc(candles[len(candles)-1], cfg.UseAdjustedClose)
	if basePeak > 0 {
		dist := (basePeak - cur) / basePeak * 100
		if dist < 0 {
			dist = 0 // at/above the base high
		}
		q.NearBreakoutScore = clampFloat((vcpNearLoosePct-dist)/(vcpNearLoosePct-vcpNearTargetPct)*100, 0, 100)
	}

	q.QualityScore = (cfg.WTightness*q.TightnessScore +
		cfg.WVolumeDryUp*q.VolumeDryUpScore +
		cfg.WMonotonic*q.MonotonicScore +
		cfg.WSupportHold*q.SupportHoldScore +
		cfg.WNearBreakout*q.NearBreakoutScore) / 100
	return q
}

// hasBigBlackBar reports a destructive long-black candle (climax/breakdown) in
// [a, b]: volume >= X × window average and a sharp down close. Uses raw OHLCV
// (candle shape is intraday; adjustment scales open and close equally).
func hasBigBlackBar(candles []fetcher.Candle, a, b int) bool {
	if a < 0 {
		a = 0
	}
	winAvg := avgVolume(candles, a, b)
	if winAvg <= 0 {
		return false
	}
	for i := a; i <= b && i < len(candles); i++ {
		c := candles[i]
		if c.Open <= 0 {
			continue
		}
		if float64(c.Volume) >= vcpBigBlackVolX*winAvg && (c.Close-c.Open)/c.Open <= vcpBigBlackDrop {
			return true
		}
	}
	return false
}

func gradeByCount(count int) VCPGrade {
	switch {
	case count >= 4:
		return VCPGradeHighQuality
	case count == 3:
		return VCPGradeStandard
	case count == 2:
		return VCPGradeEarly
	default:
		return VCPGradeNone
	}
}

func rangeStats(depths []float64) (last, max, min float64) {
	if len(depths) == 0 {
		return 0, 0, 0
	}
	last = depths[len(depths)-1]
	max, min = depths[0], depths[0]
	for _, d := range depths {
		if d > max {
			max = d
		}
		if d < min {
			min = d
		}
	}
	return last, max, min
}

func vcpReason(depths []float64, q VCPQuality) string {
	s := "收縮"
	for i, d := range depths {
		if i > 0 {
			s += "→"
		}
		s += fmt.Sprintf("%.0f%%", d)
	}
	return fmt.Sprintf("%s（品質 %.0f，量縮 %.0f / 收緊 %.0f）", s, q.QualityScore, q.VolumeDryUpScore, q.TightnessScore)
}

func vcpWarnings(q VCPQuality) []string {
	var w []string
	if q.MonotonicScore < 60 {
		w = append(w, "收縮幅度未逐段變小（越整理越鬆散）")
	}
	if q.VolumeDryUpScore < 40 {
		w = append(w, "後段量能未明顯縮小")
	}
	if q.SupportHoldScore < 50 {
		w = append(w, "低點未守住 / 出現爆量長黑")
	}
	return w
}
