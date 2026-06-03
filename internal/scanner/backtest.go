package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// 回測（Backtest）
//
// 把「目前型態」在過去相似情境（同型態 + 同整理 bucket）的表現量化：隔日/3日/5日
// 報酬、最大回撤、勝率、風報比。個股樣本通常偏少，因此額外 pool 同族群成員歷史，
// 得到族群層級的樣本與勝率，提高信心。
// ──────────────────────────────────────────────────────────────────────────────

// Backtest holds historical stats for the current pattern.
type Backtest struct {
	PatternName      string
	StockSampleCount int
	StockWinRate     float64
	SectorSampleCount int
	SectorWinRate    float64
	AvgReturn        float64 // 5 日平均報酬（%）
	AvgDrawdown      float64 // 平均最大回撤（%，負）
	RiskReward       float64
	Confidence       string // HIGH | MEDIUM | LOW
}

// patternFn evaluates whether a named setup is present at bar i (no look-ahead).
type patternFn func(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool

type namedPattern struct {
	name string
	fn   patternFn
}

// patternsByPriority — the current bar's pattern = first that matches.
func patternsByPriority() []namedPattern {
	return []namedPattern{
		{"VOLUME_BREAKOUT_AFTER_BASE", patVolumeBreakoutAfterBase},
		{"PULLBACK_THEN_STRENGTH", patPullbackThenStrength},
		{"SECOND_ATTACK_NEAR_HIGH", patSecondAttackNearHigh},
		{"MA_BULL_PULLBACK", patMABullPullback},
		{"GENERIC_STRENGTH", patGenericStrength},
	}
}

// runBacktest detects the current pattern and backtests it on the stock + its sector.
func (s *Scanner) runBacktest(candles []fetcher.Candle, ind indicator.Result, members []fetcher.StockData) Backtest {
	n := len(candles)
	var bt Backtest
	if n < 40 {
		bt.PatternName = "GENERIC_STRENGTH"
		bt.Confidence = "LOW"
		return bt
	}
	closes := closeSlice(candles)
	ma5 := indicator.SMA(closes, 5)
	ma10 := indicator.SMA(closes, 10)
	ma20 := indicator.SMA(closes, 20)

	// Current pattern (first matching by priority).
	det := patGenericStrength
	bt.PatternName = "GENERIC_STRENGTH"
	for _, p := range patternsByPriority() {
		if p.fn(candles, ind, ma5, ma10, ma20, n-1) {
			det = p.fn
			bt.PatternName = p.name
			break
		}
	}

	bucket := bucketOf(baseDaysAt(candles, n-1))

	// Stock-level accumulation.
	var stock btAcc
	accumulateBacktest(candles, ind, ma5, ma10, ma20, det, bucket, &stock)
	bt.StockSampleCount = stock.samples
	if stock.samples > 0 {
		bt.StockWinRate = float64(stock.wins) / float64(stock.samples) * 100
	}

	// Sector-level: pool every member's history for the same pattern + bucket.
	var sector btAcc
	for _, m := range members {
		if len(m.Candles) < 40 {
			continue
		}
		mc := closeSlice(m.Candles)
		mind := s.calcIndicators(m.Candles)
		accumulateBacktest(m.Candles, mind,
			indicator.SMA(mc, 5), indicator.SMA(mc, 10), indicator.SMA(mc, 20),
			det, bucket, &sector)
	}
	bt.SectorSampleCount = sector.samples
	if sector.samples > 0 {
		bt.SectorWinRate = float64(sector.wins) / float64(sector.samples) * 100
	}

	// Avg return / drawdown / RR from the larger pool for stability.
	pool := stock
	if sector.samples > stock.samples {
		pool = sector
	}
	if pool.samples > 0 {
		bt.AvgReturn = pool.sumRet / float64(pool.samples)
		bt.AvgDrawdown = pool.sumDD / float64(pool.samples)
		if bt.AvgDrawdown < 0 {
			bt.RiskReward = bt.AvgReturn / (-bt.AvgDrawdown)
		}
	}

	bt.Confidence = backtestConfidence(stock.samples, sector.samples)
	return bt
}

type btAcc struct {
	samples int
	wins    int
	sumRet  float64
	sumDD   float64
}

// accumulateBacktest scans one stock's history for the pattern+bucket and accumulates stats.
func accumulateBacktest(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64,
	det patternFn, bucket ConsolBucket, acc *btAcc) {
	n := len(c)
	const fwd = 5
	for i := 30; i <= n-1-fwd; i++ {
		if !det(c, ind, ma5, ma10, ma20, i) {
			continue
		}
		if bucketOf(baseDaysAt(c, i)) != bucket {
			continue
		}
		base := c[i].Close
		if base <= 0 {
			continue
		}
		ret5 := (c[i+fwd].Close/base - 1) * 100
		dd := 0.0
		for j := i + 1; j <= i+fwd; j++ {
			d := (c[j].Low/base - 1) * 100
			if d < dd {
				dd = d
			}
		}
		acc.samples++
		acc.sumRet += ret5
		acc.sumDD += dd
		if ret5 > 0 {
			acc.wins++
		}
	}
}

func backtestConfidence(stockN, sectorN int) string {
	switch {
	case sectorN >= 30 || stockN >= 15:
		return "HIGH"
	case sectorN >= 12 || stockN >= 6:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// baseDaysAt measures the tight-base length ending at index `end` (same growing-window
// logic as analyzeConsolidation, evaluated as-of `end`).
func baseDaysAt(candles []fetcher.Candle, end int) int {
	maxBase := 60
	if maxBase > end {
		maxBase = end
	}
	days := 0
	for k := 3; k <= maxBase; k++ {
		hi, lo := windowHighLow(candles, end-k+1, end)
		if lo <= 0 {
			break
		}
		rng := (hi - lo) / lo
		cap := 0.08 + 0.004*float64(k)
		if rng <= cap {
			days = k
		} else {
			break
		}
	}
	if days < 3 {
		return 3
	}
	return days
}

// ── Pattern detectors ─────────────────────────────────────────────────────────

func volRatioAt(c []fetcher.Candle, ind indicator.Result, i int) float64 {
	if ind.VolumeMA[i] <= 0 {
		return 0
	}
	return float64(c[i].Volume) / ind.VolumeMA[i]
}

func patVolumeBreakoutAfterBase(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool {
	const baseLen = 10
	if i < baseLen+1 {
		return false
	}
	hi, lo := windowHighLow(c, i-baseLen, i-1)
	if lo <= 0 {
		return false
	}
	tight := (hi-lo)/lo <= 0.12
	broke := c[i].Close > hi
	return tight && broke && volRatioAt(c, ind, i) >= 1.5
}

func patPullbackThenStrength(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool {
	if i < 6 {
		return false
	}
	dipped := c[i-1].Close < c[i-3].Close
	volDry := avgVolume(c, i-3, i-1) < ind.VolumeMA[i-1]*0.9
	turnUp := c[i].Close > c[i-1].Close && volRatioAt(c, ind, i) >= 1.3
	aboveMA := ma10[i] > 0 && c[i].Close > ma10[i]
	return dipped && volDry && turnUp && aboveMA
}

func patSecondAttackNearHigh(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool {
	look := 60
	if look > i {
		look = i
	}
	prevHigh, _ := windowHighLow(c, i-look, i-1)
	if prevHigh <= 0 {
		return false
	}
	near := c[i].Close >= prevHigh*0.97 && c[i].Close <= prevHigh*1.005
	hi, lo := windowHighLow(c, i-8, i-1)
	tight := lo > 0 && (hi-lo)/lo <= 0.10
	aboveMA20 := ma20[i] > 0 && c[i].Close > ma20[i]
	return near && tight && aboveMA20
}

func patMABullPullback(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool {
	bull := ma5[i] > 0 && ma5[i] > ma10[i] && ma10[i] > ma20[i]
	pulled := ma10[i] > 0 && c[i].Low <= ma10[i]*1.02 && c[i].Close >= ma5[i]*0.99
	lowVol := volRatioAt(c, ind, i) <= 1.2
	return bull && pulled && lowVol
}

func patGenericStrength(c []fetcher.Candle, ind indicator.Result, ma5, ma10, ma20 []float64, i int) bool {
	if i < 2 {
		return false
	}
	return ma20[i] > 0 && c[i].Close > ma20[i] && c[i].Close > c[i-1].Close
}
