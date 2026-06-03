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
	MoneyFlow   float64 // 近 5 日資金淨流向比（-1..+1，正=流入）
	Action      Action  // 重用個股 analyze() 的交易建議

	// ── 短線（1~5 日）成員指標 ───────────────────────────────────────────────
	Gain1        float64 // 近 1 日漲幅（%）
	Gain3        float64 // 近 3 日漲幅（%）
	Gain5        float64 // 近 5 日漲幅（%）
	UpToday      bool    // 今日收紅
	AboveShortMA bool    // 站上 5 日且 10 日均線
	NewHigh20    bool    // 創 20 日新高
	AboveMA60    bool    // 站上 MA60
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

	MoneyFlow float64 // 族群近 5 日資金淨流向比（-1..+1，正=流入）
	FlowState string  // "流入" | "流出" | "中性"

	// ── 三層輪動模型 ─────────────────────────────────────────────────────────
	// 1) 短線流向（1~5 日）：最早反映資金轉向，比 20 日強度更快。
	ShortTermFlowScore float64 // 0~100
	ShortTermFlowDir   string  // INFLOW | OUTFLOW | NEUTRAL
	ShortTermFlowStage string  // EARLY_ROTATION | CONFIRMED_ROTATION | OVERHEATED | WEAKENING
	ShortTermNote      string  // 一句話結論

	// 短線細項（給 UI 拆解）
	Avg1dGain         float64
	Avg3dGain         float64
	Avg5dGain         float64
	UpRatio           float64 // 上漲家數比例（%）
	AboveShortMARatio float64 // 站上 5/10 日均線比例（%）
	NewHigh20Ratio    float64 // 創 20 日新高比例（%）

	// 2) 中期強度（20 日）：即原本的 Sector Score。
	MidTermStrength float64 // = Score（0~100）
	MidTermLabel    string  // 強 | 中 | 弱

	// 3) 波段趨勢（60 日）。
	TrendStrength float64 // 0~100
	TrendLabel    string  // 確認上升 | 尚未確認 | 轉弱
	AboveMA60Ratio float64 // 站上 MA60 比例（%）

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
	flowDays       = 5   // 資金流向回看天數
	flowInThresh   = 0.20 // 族群淨流向 ≥ 此值視為「流入」

	// 短線流向綜合分數權重（和為 1.0）。
	wST1dGain        = 0.10 // 近 1 日漲幅
	wST3dGain        = 0.15 // 近 3 日漲幅
	wST5dGain        = 0.10 // 近 5 日漲幅
	wSTUpRatio       = 0.15 // 上漲家數比例
	wSTVolExp        = 0.15 // 量能放大比例
	wSTAboveMA       = 0.15 // 站上 5/10 日均線比例
	wSTNewHigh20     = 0.10 // 創 20 日新高比例
	wSTAccel         = 0.10 // 動能加速（短線 vs 中期步調）
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

		// 三層輪動：中期 = Score；短線階段需與中期強度比對。
		r.MidTermStrength = r.Score
		r.MidTermLabel = midTermLabel(r.Score)
		r.ShortTermFlowStage = classifyShortStage(r.ShortTermFlowScore, r.Score, r.ShortTermFlowDir, r.AvgRSI)
		r.ShortTermNote = shortTermConclusion(r)

		// 機會分數：混合「短線流向」與「中期強度」，讓資金剛流入但 20 日尚未反映
		// 的早期輪動族群能提早浮上來；再以中期 stage 權重淡化已過熱者。
		blended := 0.5*r.Score + 0.5*r.ShortTermFlowScore
		r.OppScore = blended * stageWeight(r.Stage)
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
		ma60UpN, ma60ValidN, ma60AboveN int
		upN, aboveShortMAN, newHigh20N  int
		sumReturn, sumRSI, sumFlow      float64
		sum1d, sum3d, sum5d             float64
		n                               int
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
		sumFlow += ms.MoneyFlow
		sum1d += ms.Gain1
		sum3d += ms.Gain3
		sum5d += ms.Gain5
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
		if ms.UpToday {
			upN++
		}
		if ms.AboveShortMA {
			aboveShortMAN++
		}
		if ms.NewHigh20 {
			newHigh20N++
		}
		if ms.MA60Valid {
			ma60ValidN++
			if ms.MA60Up {
				ma60UpN++
			}
			if ms.AboveMA60 {
				ma60AboveN++
			}
		}
	}

	if n == 0 {
		return nil
	}
	fn := float64(n)

	sr.AvgReturn20 = sumReturn / fn
	sr.AvgRSI = sumRSI / fn
	sr.MoneyFlow = sumFlow / fn
	sr.FlowState = classifyFlow(sr.MoneyFlow)
	sr.NewHighRatio = float64(newHighN) / fn * 100
	sr.BreakoutRatio = float64(breakoutN) / fn * 100
	sr.VolExpansion = float64(volExpN) / fn * 100
	if ma60ValidN > 0 {
		sr.MA60Slope = float64(ma60UpN) / float64(ma60ValidN) * 100
		sr.AboveMA60Ratio = float64(ma60AboveN) / float64(ma60ValidN) * 100
	}

	// ── 短線（1~5 日）細項 ───────────────────────────────────────────────────
	sr.Avg1dGain = sum1d / fn
	sr.Avg3dGain = sum3d / fn
	sr.Avg5dGain = sum5d / fn
	sr.UpRatio = float64(upN) / fn * 100
	sr.AboveShortMARatio = float64(aboveShortMAN) / fn * 100
	sr.NewHigh20Ratio = float64(newHigh20N) / fn * 100

	// 動能加速：近 3 日日均步調 vs 20 日日均步調（>0 代表短線正在加速 → 排名上升）。
	accel := sr.Avg3dGain/3 - sr.AvgReturn20/20
	sr.ShortTermFlowScore = shortTermFlowScore(sr, accel)
	sr.ShortTermFlowDir = shortFlowDirection(sr.Avg3dGain, sr.UpRatio)

	// ── 60 日趨勢 ────────────────────────────────────────────────────────────
	sr.TrendStrength = 0.6*sr.MA60Slope + 0.4*sr.AboveMA60Ratio
	sr.TrendLabel = trendLabel(sr.TrendStrength)

	sr.Raw = RotationScore{
		SectorMomentum: sr.AvgReturn20,
		BreakoutCount:  breakoutN,
		NewHighCount:   newHighN,
		VolumeTrend:    float64(volExpN) / fn,
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

	// MA60 slope: rising over the last ~5 days; and whether price is above MA60.
	if n >= 60 {
		ma60 := indicator.SMA(closes, 60)
		if ma60[n-1] > 0 && ma60[n-6] > 0 {
			ms.MA60Valid = true
			ms.MA60Up = ma60[n-1] > ma60[n-6]
			ms.AboveMA60 = latest.Close > ma60[n-1]
		}
	}

	// Money flow (資金流向): MFI-style signed typical-price money flow over the
	// last `flowDays` bars, normalized to [-1, +1]. Positive = 資金流入.
	ms.MoneyFlow = moneyFlowRatio(candles)

	// ── 短線（1~5 日）指標 ───────────────────────────────────────────────────
	if n >= 2 && closes[n-2] > 0 {
		ms.Gain1 = (latest.Close/closes[n-2] - 1) * 100
		ms.UpToday = latest.Close > closes[n-2]
	}
	if n >= 4 && closes[n-4] > 0 {
		ms.Gain3 = (latest.Close/closes[n-4] - 1) * 100
	}
	if n >= 6 && closes[n-6] > 0 {
		ms.Gain5 = (latest.Close/closes[n-6] - 1) * 100
	}
	// 站上 5 日且 10 日均線。
	if n >= 10 {
		ma5 := indicator.SMA(closes, 5)
		ma10 := indicator.SMA(closes, 10)
		if ma5[n-1] > 0 && ma10[n-1] > 0 {
			ms.AboveShortMA = latest.Close > ma5[n-1] && latest.Close > ma10[n-1]
		}
	}
	// 創 20 日新高（收盤 ≥ 前 20 根最高，已由 consoHigh 計算）。
	ms.NewHigh20 = consoHigh > 0 && latest.Close >= consoHigh

	return ms
}

// moneyFlowRatio returns the net money-flow ratio over the last `flowDays` bars.
// For each day, raw money flow = typical price × volume, signed by whether the
// typical price rose or fell vs the prior day. Result is (inflow-outflow)/(inflow+outflow),
// ranging -1 (全數流出) .. +1 (全數流入).
func moneyFlowRatio(candles []fetcher.Candle) float64 {
	n := len(candles)
	start := n - flowDays
	if start < 1 {
		start = 1
	}
	var posFlow, negFlow float64
	for i := start; i < n; i++ {
		tp := (candles[i].High + candles[i].Low + candles[i].Close) / 3
		tpPrev := (candles[i-1].High + candles[i-1].Low + candles[i-1].Close) / 3
		rmf := tp * float64(candles[i].Volume)
		switch {
		case tp > tpPrev:
			posFlow += rmf
		case tp < tpPrev:
			negFlow += rmf
		}
	}
	gross := posFlow + negFlow
	if gross <= 0 {
		return 0
	}
	return (posFlow - negFlow) / gross
}

// classifyFlow maps a sector's net money-flow ratio to a 流入/流出/中性 label.
func classifyFlow(flow float64) string {
	switch {
	case flow >= flowInThresh:
		return "流入"
	case flow <= -flowInThresh:
		return "流出"
	default:
		return "中性"
	}
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

// ──────────────────────────────────────────────────────────────────────────────
// 短線流向（short_term_flow）：1~5 日資金流向，比 20 日強度更早反映輪動
// ──────────────────────────────────────────────────────────────────────────────

// Short-term flow direction / stage labels.
const (
	FlowInflow  = "INFLOW"
	FlowOutflow = "OUTFLOW"
	FlowNeutral = "NEUTRAL"

	STEarlyRotation     = "EARLY_ROTATION"     // 短線轉強但 20 日尚未反映 → 早期輪動
	STConfirmedRotation = "CONFIRMED_ROTATION" // 短線與中期同步走強 → 確認輪動
	STOverheated        = "OVERHEATED"         // 短中皆強且超買 → 過熱/成熟
	STWeakening         = "WEAKENING"          // 短線資金轉弱/流出 → 鈍化或轉出
)

// shortTermFlowScore blends the 1~5 day breadth/momentum signals into 0~100.
func shortTermFlowScore(sr *SectorRotation, accel float64) float64 {
	g1 := clampFloat(50+sr.Avg1dGain*10, 0, 100)
	g3 := clampFloat(50+sr.Avg3dGain*5, 0, 100)
	g5 := clampFloat(50+sr.Avg5dGain*3.3, 0, 100)
	ac := clampFloat(50+accel*15, 0, 100)

	score := wST1dGain*g1 +
		wST3dGain*g3 +
		wST5dGain*g5 +
		wSTUpRatio*sr.UpRatio +
		wSTVolExp*sr.VolExpansion +
		wSTAboveMA*sr.AboveShortMARatio +
		wSTNewHigh20*sr.NewHigh20Ratio +
		wSTAccel*ac
	return clampFloat(score, 0, 100)
}

// shortFlowDirection classifies INFLOW / OUTFLOW / NEUTRAL from recent breadth.
func shortFlowDirection(avg3dGain, upRatio float64) string {
	switch {
	case avg3dGain >= 1.0 && upRatio >= 50:
		return FlowInflow
	case avg3dGain <= -1.0 && upRatio < 50:
		return FlowOutflow
	default:
		return FlowNeutral
	}
}

// classifyShortStage compares short-term flow against mid-term (20d) strength.
//   - 短強、中弱       → EARLY_ROTATION（資金剛流入，尚未反映到 20 日）
//   - 短強、中也強     → CONFIRMED_ROTATION（輪動確認，主升）
//   - 短中皆強且超買   → OVERHEATED（成熟/過熱）
//   - 短弱/流出        → WEAKENING（鈍化或資金轉出）
func classifyShortStage(stScore, midScore float64, dir string, avgRSI float64) string {
	switch {
	case dir == FlowOutflow:
		return STWeakening
	case stScore >= 60 && midScore >= 65 && avgRSI >= 68:
		return STOverheated
	case stScore >= 52 && midScore >= 50:
		return STConfirmedRotation
	case stScore >= 48 && midScore < 50:
		return STEarlyRotation
	case stScore < 38:
		return STWeakening
	default:
		return STEarlyRotation
	}
}

// midTermLabel labels the 20-day composite strength.
func midTermLabel(score float64) string {
	switch {
	case score >= 60:
		return "強"
	case score >= 40:
		return "中"
	default:
		return "弱"
	}
}

// trendLabel labels the 60-day trend strength.
func trendLabel(score float64) string {
	switch {
	case score >= 60:
		return "確認上升"
	case score >= 35:
		return "尚未確認"
	default:
		return "轉弱"
	}
}

// shortTermConclusion produces a one-line, plain-language takeaway.
func shortTermConclusion(r *SectorRotation) string {
	switch r.ShortTermFlowStage {
	case STEarlyRotation:
		return "資金剛開始流入，20 日強度尚未反映，屬早期輪動候選"
	case STConfirmedRotation:
		return "短線與中期同步走強，輪動已確認（主升段）"
	case STOverheated:
		return "短中期皆強且偏超買，已進入成熟／過熱階段，追價需謹慎"
	default: // WEAKENING
		if r.MidTermStrength >= 50 {
			return "短線資金轉弱但中期仍高，留意高檔鈍化、資金可能準備轉出"
		}
		return "短線無資金進駐，暫不具輪動動能"
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
