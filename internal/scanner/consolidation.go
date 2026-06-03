package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// 整理型態分析（Consolidation / Base）
//
// 核心原則：整理「時間長短」只做型態分類（bucket），不是越長越好。
// 強勢股急漲後只整理 3~5 天，只要守住支撐、量縮、高點不墜、低點墊高，
// 仍可是高品質的 base。品質看 BaseQualityScore，而非天數。
// ──────────────────────────────────────────────────────────────────────────────

// ConsolBucket classifies a base by its duration (型態分類，非加扣分依據).
type ConsolBucket string

const (
	MicroBase ConsolBucket = "MICRO_BASE" // 3~5 日：強勢急拉後短整、強勢換手
	ShortBase ConsolBucket = "SHORT_BASE" // 6~10 日：短線再攻前準備
	SwingBase ConsolBucket = "SWING_BASE" // 11~20 日：明顯平台，適合判斷突破
	MidBase   ConsolBucket = "MID_BASE"   // 21~40 日：較穩波段平台
	LongBase  ConsolBucket = "LONG_BASE"  // 41~60 日：中長底/大型平台
	NoBase    ConsolBucket = "NO_BASE"    // 無明顯整理（剛噴出或趨勢中）
)

// Consolidation holds the current base's shape and quality.
type Consolidation struct {
	Days                  int          // 整理天數
	Bucket                ConsolBucket // 型態分類
	RangePct              float64      // 整理區間 (high-low)/low（%）
	VolumeDryUpRatio      float64      // base 均量 / base 前均量（<1 = 量縮）
	PriceCompressionScore float64      // 0~100，越高越壓縮
	SupportHoldScore      float64      // 0~100，低點守住程度
	NearPreviousHigh      bool         // 收盤距前高 ≤ ~3%
	BaseQualityScore      float64      // 0~100 綜合品質
	HigherLows            bool         // 低點逐步墊高

	PivotHigh float64 // 前高 / 突破壓力（突破價）
	BaseLow   float64 // 整理區下緣（支撐價）

	BigVolDown    bool // 整理期間出現爆量長黑
	BrokePlatform bool // 收盤跌破平台下緣
}

// analyzeConsolidation derives the current base from daily candles.
// sectorInflow folds the "族群是否流入" condition into BaseQualityScore.
func analyzeConsolidation(candles []fetcher.Candle, ind indicator.Result, sectorInflow bool) Consolidation {
	n := len(candles)
	var c Consolidation
	if n < 12 {
		c.Bucket = NoBase
		return c
	}
	closes := closeSlice(candles)
	latest := candles[n-1]

	ma5 := indicator.SMA(closes, 5)
	ma10 := indicator.SMA(closes, 10)

	// ── 1. 找出當前整理視窗（由最後一根往回擴張，range 相對其長度仍算 tight）──
	maxBase := 60
	if maxBase > n-1 {
		maxBase = n - 1
	}
	days := 0
	for k := 3; k <= maxBase; k++ {
		hi, lo := windowHighLow(candles, n-k, n-1)
		if lo <= 0 {
			break
		}
		rng := (hi - lo) / lo
		cap := 0.08 + 0.004*float64(k) // 長 base 容許較寬區間
		if rng <= cap {
			days = k
		} else {
			break
		}
	}
	if days < 3 {
		// 無明顯整理（趨勢中或剛噴出）
		c.Bucket = NoBase
		c.Days = days
		c.PivotHigh, _ = windowHighLowF(candles, n-1-min(60, n-1), n-1)
		c.BaseLow = latest.Low
		return c
	}
	c.Days = days
	c.Bucket = bucketOf(days)

	baseStart := n - days
	pivotHigh, baseLow := windowHighLow(candles, baseStart, n-1)
	c.BaseLow = baseLow
	if baseLow > 0 {
		c.RangePct = (pivotHigh - baseLow) / baseLow * 100
	}

	// 前高：近 60 根最高（整理的壓力參考），作為突破價。
	prevHigh, _ := windowHighLow(candles, n-1-min(60, n-1), n-1)
	c.PivotHigh = prevHigh
	c.NearPreviousHigh = prevHigh > 0 && latest.Close >= prevHigh*0.97

	// ── 2. 量縮比 ────────────────────────────────────────────────────────────
	baseVol := avgVolume(candles, baseStart, n-1)
	preStart := baseStart - 10
	if preStart < 0 {
		preStart = 0
	}
	preVol := avgVolume(candles, preStart, baseStart-1)
	if preVol <= 0 {
		preVol = ind.VolumeMA[n-1]
	}
	if preVol > 0 {
		c.VolumeDryUpRatio = baseVol / preVol
	}

	// ── 3. 價格壓縮分數（相對其長度的 tightness + 布林收縮）──────────────────
	capAtDays := 0.08 + 0.004*float64(days)
	tightness := 1.0
	if capAtDays > 0 && baseLow > 0 {
		tightness = 1.0 - ((pivotHigh-baseLow)/baseLow)/capAtDays
	}
	comp := clampFloat(tightness*100, 0, 100)
	if bbW := ind.BB.Width[n-1]; bbW > 0 && bbW < 0.06 {
		comp = clampFloat(comp+10, 0, 100)
	}
	c.PriceCompressionScore = comp

	// ── 4. 支撐守住分數（低點守住 MA10、低點墊高）────────────────────────────
	holdN := 0
	for i := baseStart; i < n; i++ {
		ref := baseLow
		if ma10[i] > 0 {
			ref = ma10[i]
		}
		if candles[i].Low >= ref*0.985 {
			holdN++
		}
	}
	hold := float64(holdN) / float64(days) * 80
	// 低點墊高：後半段最低 ≥ 前半段最低。
	mid := baseStart + days/2
	_, lowFirst := windowHighLow(candles, baseStart, mid)
	_, lowSecond := windowHighLow(candles, mid, n-1)
	c.HigherLows = lowSecond >= lowFirst
	if c.HigherLows {
		hold += 20
	}
	c.SupportHoldScore = clampFloat(hold, 0, 100)

	// ── 5. 負面：爆量長黑 / 跌破平台 ─────────────────────────────────────────
	for i := baseStart; i < n; i++ {
		vr := 0.0
		if ind.VolumeMA[i] > 0 {
			vr = float64(candles[i].Volume) / ind.VolumeMA[i]
		}
		drop := 0.0
		if candles[i].Open > 0 {
			drop = (candles[i].Close - candles[i].Open) / candles[i].Open
		}
		if vr >= 2.0 && drop <= -0.03 {
			c.BigVolDown = true
		}
	}
	c.BrokePlatform = baseLow > 0 && latest.Close < baseLow
	_ = ma5

	// ── 6. BaseQualityScore ──────────────────────────────────────────────────
	dryScore := clampFloat((1.2-c.VolumeDryUpRatio)/0.6*100, 0, 100) // dry 0.6→100, 1.2→0
	q := 0.25*c.PriceCompressionScore +
		0.25*dryScore +
		0.25*c.SupportHoldScore
	if c.NearPreviousHigh {
		q += 10
	}
	if sectorInflow {
		q += 15
	}
	if c.BigVolDown {
		q -= 20
	}
	if c.BrokePlatform {
		q -= 30
	}
	c.BaseQualityScore = clampFloat(q, 0, 100)

	return c
}

func bucketOf(days int) ConsolBucket {
	switch {
	case days <= 5:
		return MicroBase
	case days <= 10:
		return ShortBase
	case days <= 20:
		return SwingBase
	case days <= 40:
		return MidBase
	default:
		return LongBase
	}
}

// windowHighLow returns (maxHigh, minLow) over candles[a..b] inclusive.
func windowHighLow(candles []fetcher.Candle, a, b int) (float64, float64) {
	if a < 0 {
		a = 0
	}
	hi, lo := 0.0, 0.0
	for i := a; i <= b && i < len(candles); i++ {
		if hi == 0 || candles[i].High > hi {
			hi = candles[i].High
		}
		if lo == 0 || candles[i].Low < lo {
			lo = candles[i].Low
		}
	}
	return hi, lo
}

func windowHighLowF(candles []fetcher.Candle, a, b int) (float64, float64) {
	return windowHighLow(candles, a, b)
}

func avgVolume(candles []fetcher.Candle, a, b int) float64 {
	if a < 0 {
		a = 0
	}
	var sum float64
	var cnt int
	for i := a; i <= b && i < len(candles); i++ {
		sum += float64(candles[i].Volume)
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}
