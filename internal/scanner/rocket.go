package scanner

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// 飆股候選判斷（Rocket Candidate）
//
// 判斷個股是否正從「普通強勢股」轉變成「飆股」。綜合：族群資金流入、個股相對強勢、
// 技術接近噴出、量能結構健康、尚未過熱。輸出 0~100 分 + 階段 + 操作建議 + 價位 + 理由風險。
// ──────────────────────────────────────────────────────────────────────────────

type RocketStage string

const (
	StageNotReady      RocketStage = "NOT_READY"
	StageBaseBuilding  RocketStage = "BASE_BUILDING"
	StagePreBreakout   RocketStage = "PRE_BREAKOUT"
	StageBreakoutStart RocketStage = "BREAKOUT_START"
	StageMainRun       RocketStage = "MAIN_RUN"
	StageOverheated    RocketStage = "OVERHEATED"
	StageFailed        RocketStage = "FAILED"
)

type WatchAction string

const (
	ActWait        WatchAction = "WAIT"
	ActWatchClose  WatchAction = "WATCH_CLOSELY"
	ActPrepare     WatchAction = "PREPARE_ENTRY"
	ActBreakoutBuy WatchAction = "BREAKOUT_BUY"
	ActPullbackBuy WatchAction = "PULLBACK_BUY"
	ActTakeProfit  WatchAction = "TAKE_PROFIT"
	ActRemove      WatchAction = "REMOVE_FROM_WATCHLIST"
)

type rocketInput struct {
	candles           []fetcher.Candle
	ind               indicator.Result
	consol            Consolidation
	bt                Backtest
	flowDir           string // INFLOW/OUTFLOW/NEUTRAL（族群短線流向）
	sectorStage       RotationStage
	sectorAvgReturn20 float64
	hasSector         bool

	// ── C6b guardrail scoring (shadow signals; only used when guardrailScoring) ──
	guardrailScoring bool       // master flag: shadow may influence scoring
	vcp              *VCPResult // C6b-1: corrects g3 base-quality (nil = no effect)
}

type rocketOutput struct {
	Score         int
	Stage         RocketStage
	ExplosionProb string
	DaysToWatch   string
	WatchAction   WatchAction

	BreakoutPrice  float64
	SupportPrice   float64
	StopLossPrice  float64
	EntryZone      string
	TakeProfitZone string

	Reasons     []string
	RiskLabel   string
	RiskWarning string
}

func computeRocket(in rocketInput) rocketOutput {
	c := in.candles
	ind := in.ind
	n := len(c)
	var out rocketOutput
	if n < 30 {
		out.Stage = StageNotReady
		out.WatchAction = ActWait
		out.ExplosionProb = "LOW"
		out.DaysToWatch = "資料不足"
		return out
	}
	closes := closeSlice(c)
	latest := c[n-1]
	ma5 := indicator.SMA(closes, 5)
	ma10 := indicator.SMA(closes, 10)
	ma20 := ind.MA20

	volRatio := volRatioAt(c, ind, n-1)
	ret1, ret5, ret20 := pctChange(closes, 2), pctChange(closes, 6), pctChange(closes, 21)
	rsi := ind.RSI[n-1]

	aboveMA5 := ma5[n-1] > 0 && latest.Close > ma5[n-1]
	aboveMA20 := ma20[n-1] > 0 && latest.Close > ma20[n-1]
	bullAlign := ma5[n-1] > 0 && ma5[n-1] > ma10[n-1] && ma10[n-1] > ma20[n-1]
	extFrom5 := 0.0
	if ma5[n-1] > 0 {
		extFrom5 = (latest.Close/ma5[n-1] - 1) * 100
	}
	breakoutLevel := in.consol.PivotHigh
	priceUp := ret1 > 0

	// candle shape
	rng := latest.High - latest.Low
	upperShadow := rng > 0 && (latest.High-latest.Close)/rng > 0.5
	openHighCloseLow := latest.Open > 0 && latest.Close < latest.Open && rng > 0 && (latest.Close-latest.Low)/rng < 0.3
	limitStatus, _ := detectLimitStatus(c, volRatio)
	climaxDistribution := limitStatus == LimitUpFailed || limitStatus == LimitDistribution

	justBroke := breakoutLevel > 0 && latest.Close > breakoutLevel &&
		c[n-2].Close <= breakoutLevel*1.001 && volRatio >= 1.3

	// ── Score: 5 組 ───────────────────────────────────────────────────────────
	// 1) 族群資金流入（max 25）
	g1 := 8.0
	if in.hasSector {
		g1 = 0
		switch in.flowDir {
		case FlowInflow:
			g1 += 15
		case FlowNeutral:
			g1 += 7
		}
		switch in.sectorStage {
		case EarlyRotation, ConfirmedRotation:
			g1 += 10
		case HotRotation:
			g1 += 5
		case LateRotation:
			g1 += 2
		}
	}
	g1 = clampFloat(g1, 0, 25)

	// 2) 個股相對強勢（max 20）
	g2 := 0.0
	if in.hasSector {
		if ret20 > in.sectorAvgReturn20 {
			g2 += 8
		} else {
			g2 += 3
		}
	} else {
		g2 += 5
	}
	switch {
	case rsi >= 40 && rsi <= 68:
		g2 += 6
	case rsi > 68 && rsi <= 75:
		g2 += 3
	default:
		g2 += 2
	}
	if in.consol.SupportHoldScore >= 60 {
		g2 += 6
	}
	g2 = clampFloat(g2, 0, 20)

	// 3) 技術接近噴出（max 25）
	// C6b-1: VCP may CORRECT (raise) the base-quality input — never a new group and
	// never lowering an already-good base. Gated by the master flag + valid VCP.
	effBQ := in.consol.BaseQualityScore
	if in.guardrailScoring && in.vcp != nil && in.vcp.Valid {
		effBQ = math.Max(in.consol.BaseQualityScore, in.vcp.QualityScore)
	}
	g3 := effBQ / 100 * 12
	if in.consol.NearPreviousHigh {
		g3 += 6
	}
	if bullAlign {
		g3 += 4
	}
	if justBroke || (breakoutLevel > 0 && latest.Close >= breakoutLevel*0.97) {
		g3 += 3
	}
	g3 = clampFloat(g3, 0, 25)

	// 4) 量能結構健康（max 15）
	g4 := 0.0
	if priceUp && volRatio >= 1.2 {
		g4 += 6
	}
	if in.consol.VolumeDryUpRatio > 0 && in.consol.VolumeDryUpRatio < 0.9 {
		g4 += 5
	}
	if !climaxDistribution && !in.consol.BigVolDown {
		g4 += 4
	}
	g4 = clampFloat(g4, 0, 15)

	// 5) 尚未過熱（max 15，扣分）
	g5 := 15.0
	if extFrom5 > 12 {
		g5 -= 8
	}
	if upperShadow {
		g5 -= 4
	}
	if openHighCloseLow {
		g5 -= 3
	}
	if in.flowDir == FlowOutflow {
		g5 -= 5
	}
	g5 = clampFloat(g5, 0, 15)

	out.Score = int(clampFloat(g1+g2+g3+g4+g5, 0, 100) + 0.5)

	// ── Stage 決策樹 ──────────────────────────────────────────────────────────
	extended := extFrom5 > 12 || ret5 > 25
	failed := in.consol.BrokePlatform ||
		(ma20[n-1] > 0 && latest.Close < ma20[n-1] && ret5 < -5) ||
		(in.flowDir == FlowOutflow && ret1 < 0 && ma10[n-1] > 0 && latest.Close < ma10[n-1])
	climax := climaxDistribution || (upperShadow && volRatio >= 2)
	mainRun := bullAlign && ret20 >= 15 && aboveMA5 && !extended
	preBreak := in.consol.NearPreviousHigh && in.consol.BaseQualityScore >= 50 &&
		in.consol.VolumeDryUpRatio < 1.0 && breakoutLevel > 0 &&
		latest.Close <= breakoutLevel*1.005 && !in.consol.BrokePlatform
	baseBuild := in.consol.Bucket != NoBase && aboveMA20 && in.consol.BaseQualityScore >= 40

	switch {
	case failed:
		out.Stage = StageFailed
	case extended || climax:
		out.Stage = StageOverheated
	case justBroke:
		out.Stage = StageBreakoutStart
	case mainRun:
		out.Stage = StageMainRun
	case preBreak:
		out.Stage = StagePreBreakout
	case baseBuild:
		out.Stage = StageBaseBuilding
	default:
		out.Stage = StageNotReady
	}

	// ── 衍生 ──────────────────────────────────────────────────────────────────
	out.ExplosionProb = explosionProb(out.Stage, out.Score)
	out.DaysToWatch = daysToWatch(out.Stage)
	out.WatchAction = watchActionFor(out.Stage, ret1, volRatio)

	// ── 價位計畫 ──────────────────────────────────────────────────────────────
	_, stop, t1, t2 := priceTargets(latest.Close, ind.ATR[n-1], ind.BB)
	out.BreakoutPrice = round1(breakoutLevel)
	support := math.Max(ma10[n-1], in.consol.BaseLow)
	if support <= 0 || support >= latest.Close {
		support = in.consol.BaseLow
	}
	out.SupportPrice = round1(support)
	stopRef := in.consol.BaseLow
	if ma5[n-1] > 0 && ma5[n-1] < stopRef {
		stopRef = ma5[n-1]
	}
	if stopRef <= 0 {
		stopRef = stop
	}
	out.StopLossPrice = round1(stopRef * 0.99)
	out.TakeProfitZone = fmt.Sprintf("%.1f ~ %.1f", t1, t2)
	out.EntryZone = entryZone(out.Stage, breakoutLevel, in.consol.BaseLow)

	// ── 理由 / 風險 ───────────────────────────────────────────────────────────
	out.Reasons = rocketReasons(in, volRatio, ret20)
	out.RiskLabel, out.RiskWarning = rocketRisk(in, out.Stage, volRatio, justBroke, upperShadow, openHighCloseLow)

	return out
}

func explosionProb(stage RocketStage, score int) string {
	switch {
	case stage == StageFailed || stage == StageNotReady || stage == StageOverheated:
		return "LOW"
	case (stage == StagePreBreakout || stage == StageBreakoutStart) && score >= 75:
		return "HIGH"
	case score >= 60:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

func daysToWatch(stage RocketStage) string {
	switch stage {
	case StagePreBreakout:
		return "1~3 天"
	case StageBreakoutStart:
		return "1~2 天"
	case StageBaseBuilding:
		return "3~10 天"
	case StageMainRun:
		return "持有/拉回再評估"
	case StageOverheated:
		return "等待回檔"
	case StageFailed:
		return "—"
	default:
		return "再觀察"
	}
}

func watchActionFor(stage RocketStage, ret1, volRatio float64) WatchAction {
	switch stage {
	case StageBaseBuilding:
		return ActWatchClose
	case StagePreBreakout:
		return ActPrepare
	case StageBreakoutStart:
		return ActBreakoutBuy
	case StageMainRun:
		if ret1 < 0 && volRatio < 1.0 {
			return ActPullbackBuy
		}
		return ActWatchClose
	case StageOverheated:
		return ActTakeProfit
	case StageFailed:
		return ActRemove
	default:
		return ActWait
	}
}

func entryZone(stage RocketStage, breakout, baseLow float64) string {
	switch stage {
	case StagePreBreakout:
		return fmt.Sprintf("突破前高 %.1f 放量", breakout)
	case StageBreakoutStart:
		return fmt.Sprintf("突破點 %.1f 上方、回踩不破可進", breakout)
	case StageBaseBuilding:
		return fmt.Sprintf("平台 %.1f~%.1f 區間量縮", baseLow, breakout)
	case StageMainRun:
		return "拉回 5/10 日線量縮承接"
	case StageOverheated:
		return "等回檔測 10 日線再評估"
	default:
		return "等待型態成形"
	}
}

func rocketReasons(in rocketInput, volRatio, ret20 float64) []string {
	var rs []string
	if in.hasSector && in.flowDir == FlowInflow {
		rs = append(rs, "族群短線資金流入，不是單一股票獨強")
	}
	if in.consol.VolumeDryUpRatio > 0 && in.consol.VolumeDryUpRatio < 0.9 {
		rs = append(rs, fmt.Sprintf("整理量縮（量縮比 %.2f），籌碼沉澱", in.consol.VolumeDryUpRatio))
	}
	if in.consol.SupportHoldScore >= 60 {
		rs = append(rs, "整理期間支撐守住、低點墊高")
	}
	if in.consol.NearPreviousHigh {
		rs = append(rs, "價格接近前高，逼近突破點")
	}
	if in.bt.SectorSampleCount >= 6 && in.bt.SectorWinRate >= 55 {
		rs = append(rs, fmt.Sprintf("相似型態族群回測勝率 %.0f%%（%d 筆）", in.bt.SectorWinRate, in.bt.SectorSampleCount))
	} else if in.bt.StockSampleCount >= 4 && in.bt.StockWinRate >= 55 {
		rs = append(rs, fmt.Sprintf("相似型態個股回測勝率 %.0f%%（%d 筆）", in.bt.StockWinRate, in.bt.StockSampleCount))
	}
	if len(rs) == 0 {
		rs = append(rs, "型態尚未成形，持續觀察")
	}
	return rs
}

func rocketRisk(in rocketInput, stage RocketStage, volRatio float64, justBroke, upperShadow, openHighCloseLow bool) (label, warning string) {
	switch {
	case in.consol.BrokePlatform || stage == StageFailed:
		return "跌破支撐", "已跌破整理平台下緣，型態失效，建議移出觀察清單"
	case stage == StageOverheated:
		return "追高", "短線漲幅過大、偏離 5 日線，追高風險高，等待回檔"
	case in.flowDir == FlowOutflow:
		return "族群轉弱", "族群短線資金流向轉弱，個股易受拖累"
	case justBroke && volRatio < 1.5:
		return "假突破", "突破時量能不足，可能是假突破，需量增確認"
	case stage == StagePreBreakout && volRatio < 1.2:
		return "量不足", "接近突破但量能尚未放大，突破若無量需提防假突破"
	case upperShadow || openHighCloseLow:
		return "上影/收弱", "出現長上影或開高走低，短線追價需謹慎"
	default:
		return "—", "依計畫設好停損即可"
	}
}

// pctChange returns (close[n-1]/close[n-back] - 1)*100, or 0 if insufficient data.
func pctChange(closes []float64, back int) float64 {
	n := len(closes)
	if n < back || closes[n-back] <= 0 {
		return 0
	}
	return (closes[n-1]/closes[n-back] - 1) * 100
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
