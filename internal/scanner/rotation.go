package scanner

import (
	"sort"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// Rotation (族群輪動) engine
//
// Scanner 已從「當沖選股」調整為「尋找未來 1~4 週有機會的股票」。
// 真正的大波段通常是先有「族群輪動」、再有「個股表態」。Rotation 以族群為單位，
// 找出下一波可能接棒的族群，並刻意淡化已過熱（HOT/LATE）的族群，
// 優先呈現 Early / Confirmed 階段。
// ──────────────────────────────────────────────────────────────────────────────

// RotationStage is the maturity of a sector's rotation move.
type RotationStage string

const (
	EarlyRotation     RotationStage = "EARLY"     // 開始有資金流入、量能增加
	ConfirmedRotation RotationStage = "CONFIRMED" // 多檔突破整理區
	HotRotation       RotationStage = "HOT"       // 市場焦點、多檔新高
	LateRotation      RotationStage = "LATE"      // 漲幅過大，不建議追價
)

// RotationScore holds the raw count-based rotation signals for a sector.
type RotationScore struct {
	SectorMomentum   float64 // 平均 20 日報酬（%）
	RelativeStrength float64 // 跨族群正規化後的相對強度（0–100）
	BreakoutCount    int     // 突破整理區的成員數
	NewHighCount     int     // 創 60 日新高的成員數
	VolumeTrend      float64 // 量能放大成員比例（0–1）
}

// SectorStock is one member stock's rotation snapshot (shown when expanding a sector).
type SectorStock struct {
	Symbol      string
	Name        string
	Close       float64
	Return20    float64 // 20 日報酬（%）— 相對強度來源
	NewHigh     bool    // 創 60 日新高
	Breakout    bool    // 突破 20 日整理高點 或 站上布林上軌
	VolumeRatio float64 // 當日量 / 20 日均量
	MA60Up      bool    // MA60 近 5 日上揚
	MA60Valid   bool    // 是否有足夠資料計算 MA60
	Action      Action  // 重用個股 analyze() 的交易建議
}

// SectorRotation is the aggregated rotation result for one sector.
type SectorRotation struct {
	Name     string
	Score    float64 // 0–100 原始 Sector Score（表格顯示）
	OppScore float64 // 機會調整分數（排序用，EARLY/CONFIRMED 加權、HOT/LATE 降權）
	Stage    RotationStage

	// 五大組件（皆 0–100，給 UI 顯示拆解）
	RelStrength   float64 // 30%
	NewHighRatio  float64 // 25%
	BreakoutRatio float64 // 20%
	VolExpansion  float64 // 15%
	MA60Slope     float64 // 10%

	AvgReturn20 float64 // 平均 20 日報酬（%）— 給 stage 判斷/顯示
	AvgRSI      float64

	Raw    RotationScore
	Stocks []SectorStock
}

// stage scoring weights (must sum to 1.0).
const (
	wRelStrength   = 0.30
	wNewHigh       = 0.25
	wBreakout      = 0.20
	wVolExpansion  = 0.15
	wMA60Slope     = 0.10
	volExpRatioMin = 1.5 // 量能放大門檻（倍 MA20 量）
)

// ScanRotation aggregates per-sector rotation scores from grouped member data.
// sectorData maps a sector name to the OHLCV data of its (successfully fetched) members.
// Sector order in `order` is preserved for deterministic processing; the returned slice
// is sorted by opportunity-adjusted score (EARLY/CONFIRMED first).
func (s *Scanner) ScanRotation(order []string, sectorData map[string][]fetcher.StockData) []SectorRotation {
	results := make([]SectorRotation, 0, len(order))

	for _, name := range order {
		stocks := sectorData[name]
		sr := s.buildSector(name, stocks)
		if sr == nil {
			continue
		}
		results = append(results, *sr)
	}

	// Cross-sector relative-strength normalization (min-max of AvgReturn20 → 0–100).
	normalizeRelStrength(results)

	// Final Score, Stage, and opportunity-adjusted score.
	for i := range results {
		r := &results[i]
		r.Score = wRelStrength*r.RelStrength +
			wNewHigh*r.NewHighRatio +
			wBreakout*r.BreakoutRatio +
			wVolExpansion*r.VolExpansion +
			wMA60Slope*r.MA60Slope
		r.Raw.RelativeStrength = r.RelStrength
		r.Stage = classifyStage(r.AvgReturn20, r.AvgRSI, r.NewHighRatio, r.BreakoutRatio, r.VolExpansion)
		r.OppScore = r.Score * stageWeight(r.Stage)
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].OppScore > results[j].OppScore
	})
	return results
}

// buildSector computes a sector's member snapshots and the component ratios that do
// NOT depend on cross-sector context. RelStrength is filled later by normalizeRelStrength.
// Returns nil if the sector has no usable members.
func (s *Scanner) buildSector(name string, stocks []fetcher.StockData) *SectorRotation {
	sr := &SectorRotation{Name: name}

	var (
		newHighN, breakoutN, volExpN int
		ma60UpN, ma60ValidN          int
		sumReturn, sumRSI            float64
		n                            int
	)

	for _, stock := range stocks {
		if len(stock.Candles) < 30 {
			continue
		}
		ind := s.calcIndicators(stock.Candles)
		ms := memberSnapshot(stock, ind)
		// Reuse the per-stock advice engine for the Action badge.
		ms.Action = s.analyze(stock, ind).Action
		sr.Stocks = append(sr.Stocks, ms)

		n++
		sumReturn += ms.Return20
		idx := len(ind.RSI) - 1
		sumRSI += ind.RSI[idx]
		if ms.NewHigh {
			newHighN++
		}
		if ms.Breakout {
			breakoutN++
		}
		if ms.VolumeRatio >= volExpRatioMin {
			volExpN++
		}
		if ms.MA60Valid {
			ma60ValidN++
			if ms.MA60Up {
				ma60UpN++
			}
		}
	}

	if n == 0 {
		return nil
	}

	sr.AvgReturn20 = sumReturn / float64(n)
	sr.AvgRSI = sumRSI / float64(n)
	sr.NewHighRatio = float64(newHighN) / float64(n) * 100
	sr.BreakoutRatio = float64(breakoutN) / float64(n) * 100
	sr.VolExpansion = float64(volExpN) / float64(n) * 100
	if ma60ValidN > 0 {
		sr.MA60Slope = float64(ma60UpN) / float64(ma60ValidN) * 100
	}

	sr.Raw = RotationScore{
		SectorMomentum: sr.AvgReturn20,
		BreakoutCount:  breakoutN,
		NewHighCount:   newHighN,
		VolumeTrend:    float64(volExpN) / float64(n),
	}
	return sr
}

// memberSnapshot extracts the rotation metrics for a single stock.
func memberSnapshot(stock fetcher.StockData, ind indicator.Result) SectorStock {
	candles := stock.Candles
	n := len(candles)
	closes := closeSlice(candles)
	latest := candles[n-1]

	ms := SectorStock{
		Symbol: stock.Symbol,
		Name:   stock.Name,
		Close:  latest.Close,
	}

	// 20-day return (relative strength source).
	if n >= 21 && closes[n-21] > 0 {
		ms.Return20 = (latest.Close/closes[n-21] - 1) * 100
	}

	// New 60-day high (close at/above the prior 60-bar high, excluding today).
	lookback := 60
	if n-1 < lookback {
		lookback = n - 1
	}
	priorHigh := 0.0
	for i := n - 1 - lookback; i < n-1; i++ {
		if i >= 0 && candles[i].High > priorHigh {
			priorHigh = candles[i].High
		}
	}
	ms.NewHigh = priorHigh > 0 && latest.Close >= priorHigh

	// Breakout: above prior 20-bar consolidation high OR above Bollinger upper band.
	conso := 20
	if n-1 < conso {
		conso = n - 1
	}
	consoHigh := 0.0
	for i := n - 1 - conso; i < n-1; i++ {
		if i >= 0 && candles[i].High > consoHigh {
			consoHigh = candles[i].High
		}
	}
	bbUpper := ind.BB.Upper[n-1]
	ms.Breakout = (consoHigh > 0 && latest.Close > consoHigh) || (bbUpper > 0 && latest.Close > bbUpper)

	// Volume ratio vs 20-day average.
	if ind.VolumeMA[n-1] > 0 {
		ms.VolumeRatio = float64(latest.Volume) / ind.VolumeMA[n-1]
	}

	// MA60 slope: rising over the last ~5 days.
	if n >= 60 {
		ma60 := indicator.SMA(closes, 60)
		if ma60[n-1] > 0 && ma60[n-6] > 0 {
			ms.MA60Valid = true
			ms.MA60Up = ma60[n-1] > ma60[n-6]
		}
	}

	return ms
}

// normalizeRelStrength fills RelStrength (0–100) via min-max of AvgReturn20 across sectors.
// When all sectors share the same momentum, every sector gets 50.
func normalizeRelStrength(results []SectorRotation) {
	if len(results) == 0 {
		return
	}
	min, max := results[0].AvgReturn20, results[0].AvgReturn20
	for _, r := range results {
		if r.AvgReturn20 < min {
			min = r.AvgReturn20
		}
		if r.AvgReturn20 > max {
			max = r.AvgReturn20
		}
	}
	span := max - min
	for i := range results {
		if span <= 0 {
			results[i].RelStrength = 50
			continue
		}
		results[i].RelStrength = (results[i].AvgReturn20 - min) / span * 100
	}
}

// classifyStage maps a sector's aggregate signals to a rotation stage.
// Order matters: check the most mature/overextended states first.
func classifyStage(avgReturn20, avgRSI, newHighRatio, breakoutRatio, volExpansion float64) RotationStage {
	switch {
	case avgReturn20 >= 25 && avgRSI >= 70:
		return LateRotation // 漲幅過大、超買
	case newHighRatio >= 50 && avgRSI >= 63:
		return HotRotation // 多檔新高、市場焦點
	case breakoutRatio >= 40:
		return ConfirmedRotation // 多檔突破整理區
	case volExpansion >= 40 && avgReturn20 > 0:
		return EarlyRotation // 資金開始流入、量能增加
	default:
		return EarlyRotation // 沉寂/初期
	}
}

// stageWeight returns the opportunity multiplier used for ranking.
// EARLY/CONFIRMED are boosted; HOT/LATE are discounted (避免追在中後段).
func stageWeight(stage RotationStage) float64 {
	switch stage {
	case EarlyRotation:
		return 1.15
	case ConfirmedRotation:
		return 1.10
	case HotRotation:
		return 0.85
	case LateRotation:
		return 0.60
	default:
		return 1.0
	}
}
