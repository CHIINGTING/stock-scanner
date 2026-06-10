package scanner

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// HoldingHorizon (R7-1) — shadow-only 參考持有區間
//
// 根據趨勢階段 (stage) + 波動度 (ATR%)，給出一個粗粒度的「參考持有天數 bucket」，
// 純供人類閱讀。它是 scanner 既有資料的純函式衍生，與任何交易決策完全隔離：
//
//   不參與 RocketScore / WatchAction / ExplosionProb / 排序 / 停損 / backtest stop。
//   不進 ShadowSignals、不進 computeRocket、不進 sorting、不進 report（第一階段）。
//
// 概念參考自 radar 的「建議持有天數」，但規則在此用 scanner 既有指標重新實作，未搬
// radar code。第一版只用日線，不耦合 MomentumFlow / MTF / VCP / NewHigh。
// ──────────────────────────────────────────────────────────────────────────────

// HoldingHorizonBucket is a coarse holding-period bucket (人類參考，非交易指令).
type HoldingHorizonBucket string

const (
	HoldingObserve HoldingHorizonBucket = "OBSERVE"       // 0/0d  觀望
	HoldingShort   HoldingHorizonBucket = "SHORT_5_10D"   // 5/10d
	HoldingMedium  HoldingHorizonBucket = "MEDIUM_10_20D" // 10/20d
	HoldingLong    HoldingHorizonBucket = "LONG_20_30D"   // 20/30d
)

// HoldingStage is a Weinstein-inspired trend phase derived from daily data only.
type HoldingStage string

const (
	HHStageBase         HoldingStage = "BASE"         // 底部整理
	HHStageBreakout     HoldingStage = "BREAKOUT"     // 突破初期
	HHStageUptrend      HoldingStage = "UPTREND"      // 主升段中期
	HHStageLateUptrend  HoldingStage = "LATE_UPTREND" // 主升段末期
	HHStageDistribution HoldingStage = "DISTRIBUTION" // 出貨風險
)

// Stage 幾何門檻 — 第一版固定為模組常數（待 R7-2 calibration 再決定是否外露成 config）。
const (
	hhMASlopeFlatPct        = 0.3  // MA60 斜率「持平」帶（base 用 |slope|<=，distribution 用 slope<-）
	hhLateUptrendMA60DevPct = 20.0 // MA60 乖離 > 此值 → 主升段末期
	hhBreakoutNearHighPct   = 0.96 // 收盤 >= High60 × 此值 → 視為貼近 60 日高（突破）
	hhBaseMA60DevPct        = 8.0  // |MA60 乖離| <= 此值且斜率持平 → 底部整理
	hhSlopeLookback         = 10   // MA60 斜率回看根數
	hhATRPeriod             = 14   // ATR 期數（沿用 scanner 既有 14）
)

// hhStageNames maps stages to Traditional Chinese labels.
var hhStageNames = map[HoldingStage]string{
	HHStageBase:         "底部整理",
	HHStageBreakout:     "突破初期",
	HHStageUptrend:      "主升段中期",
	HHStageLateUptrend:  "主升段末期",
	HHStageDistribution: "出貨風險",
}

// HoldingHorizonResult is the structured, display-oriented output.
type HoldingHorizonResult struct {
	Computed      bool                 `json:"computed"`       // false = 資料不足（其餘欄位皆零值）
	Stage         HoldingStage         `json:"stage"`
	StageZh       string               `json:"stage_zh"`
	Bucket        HoldingHorizonBucket `json:"bucket"`
	MinDays       int                  `json:"min_days"`
	MaxDays       int                  `json:"max_days"`
	ATRPct        float64              `json:"atr_pct"`
	ATRCompressed bool                 `json:"atr_compressed"` // 是否因高波動被降一級
	Reasons       []string             `json:"reasons,omitempty"`
	Warnings      []string             `json:"warnings,omitempty"`
}

// HoldingHorizonConfig is the minimal (3-knob) config for the shadow signal.
type HoldingHorizonConfig struct {
	Enable         bool
	MinHistoryDays int
	ATRCompressPct float64
}

// holdingHorizonConfigFrom maps the scanner Config to the module config, applying
// the documented defaults when values are unset/non-positive.
func holdingHorizonConfigFrom(cfg Config) HoldingHorizonConfig {
	mh := cfg.HHMinHistoryDays
	if mh <= 0 {
		mh = 70 // 60 (MA60) + 10 (slope lookback)
	}
	atr := cfg.HHATRCompressPct
	if atr <= 0 {
		atr = 4.0 // 待校準
	}
	return HoldingHorizonConfig{
		Enable:         cfg.EnableHoldingHorizon,
		MinHistoryDays: mh,
		ATRCompressPct: atr,
	}
}

// computeHoldingHorizon is the pure entry point. It reads only the supplied
// candle window up to its last bar (as-of), so it is free of look-ahead.
func computeHoldingHorizon(candles []fetcher.Candle, cfg HoldingHorizonConfig) HoldingHorizonResult {
	n := len(candles)
	if n < cfg.MinHistoryDays || n < 60+hhSlopeLookback {
		return HoldingHorizonResult{Computed: false}
	}

	closes := make([]float64, n)
	highs := make([]float64, n)
	lows := make([]float64, n)
	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
	}

	price := closes[n-1]
	ma20 := indicator.SMA(closes, 20)[n-1]
	ma60Series := indicator.SMA(closes, 60)
	ma60 := ma60Series[n-1]
	ma60Prev := ma60Series[n-1-hhSlopeLookback]
	if ma60 == 0 || ma60Prev == 0 || price == 0 {
		return HoldingHorizonResult{Computed: false}
	}

	ma60Slope := (ma60 - ma60Prev) / ma60Prev * 100
	ma60Dev := (price/ma60 - 1) * 100
	high60 := hhHighestClose(closes, 60)
	atrPct := indicator.ATR(highs, lows, closes, hhATRPeriod)[n-1] / price * 100

	stage := hhCalcStage(price, ma20, ma60, ma60Slope, high60)
	bucket := hhStageBucket(stage)

	reasons := []string{
		fmt.Sprintf("階段：%s（MA60 斜率 %.2f%%/%d日，乖離 %.1f%%）",
			hhStageNames[stage], ma60Slope, hhSlopeLookback, ma60Dev),
	}
	var warnings []string

	// ATR 壓縮：只壓非 OBSERVE 的 bucket，且只往下一級（SHORT 不再降）。
	atrCompressed := false
	if bucket != HoldingObserve && atrPct > cfg.ATRCompressPct {
		if nb := hhCompress(bucket); nb != bucket {
			bucket = nb
			atrCompressed = true
			reasons = append(reasons,
				fmt.Sprintf("ATR %.1f%% 偏高（>%.1f%%），持有區間縮短一級", atrPct, cfg.ATRCompressPct))
		}
	}
	if stage == HHStageDistribution {
		warnings = append(warnings, "出貨風險階段，建議觀望，不給持有天數")
	}

	minD, maxD := hhBucketDays(bucket)
	return HoldingHorizonResult{
		Computed:      true,
		Stage:         stage,
		StageZh:       hhStageNames[stage],
		Bucket:        bucket,
		MinDays:       minD,
		MaxDays:       maxD,
		ATRPct:        math.Round(atrPct*100) / 100,
		ATRCompressed: atrCompressed,
		Reasons:       reasons,
		Warnings:      warnings,
	}
}

// hhCalcStage classifies the trend phase. Mirrors radar's概念順序 but re-implemented
// against scanner's own MA20/MA60/slope/High60 inputs. Order matters.
func hhCalcStage(price, ma20, ma60, ma60Slope, high60 float64) HoldingStage {
	if ma60 == 0 {
		return HHStageBase
	}
	ma60Dev := (price/ma60 - 1) * 100
	switch {
	case price < ma60 && ma60Slope < -hhMASlopeFlatPct:
		return HHStageDistribution
	case ma60Dev > hhLateUptrendMA60DevPct:
		return HHStageLateUptrend
	case price > ma60 && ma60Slope > 0 && high60 > 0 && price >= high60*hhBreakoutNearHighPct:
		return HHStageBreakout
	case math.Abs(ma60Slope) <= hhMASlopeFlatPct && math.Abs(ma60Dev) <= hhBaseMA60DevPct:
		return HHStageBase
	case price > ma20 && price > ma60 && ma60Slope > 0:
		return HHStageUptrend
	default:
		return HHStageBase
	}
}

// hhStageBucket maps a stage to its base holding bucket (before ATR compression).
func hhStageBucket(s HoldingStage) HoldingHorizonBucket {
	switch s {
	case HHStageBreakout:
		return HoldingLong
	case HHStageBase, HHStageUptrend:
		return HoldingMedium
	case HHStageLateUptrend:
		return HoldingShort
	default: // HHStageDistribution
		return HoldingObserve
	}
}

// hhCompress drops a bucket by one tier. SHORT stays SHORT; OBSERVE never reaches here.
func hhCompress(b HoldingHorizonBucket) HoldingHorizonBucket {
	switch b {
	case HoldingLong:
		return HoldingMedium
	case HoldingMedium:
		return HoldingShort
	default:
		return b
	}
}

// hhBucketDays maps a bucket to its (min, max) day range.
func hhBucketDays(b HoldingHorizonBucket) (int, int) {
	switch b {
	case HoldingShort:
		return 5, 10
	case HoldingMedium:
		return 10, 20
	case HoldingLong:
		return 20, 30
	default: // OBSERVE
		return 0, 0
	}
}

// hhHighestClose returns the highest close over the last n bars (current bar included).
func hhHighestClose(closes []float64, n int) float64 {
	if len(closes) == 0 {
		return 0
	}
	start := len(closes) - n
	if start < 0 {
		start = 0
	}
	hi := closes[start]
	for _, c := range closes[start+1:] {
		if c > hi {
			hi = c
		}
	}
	return hi
}
