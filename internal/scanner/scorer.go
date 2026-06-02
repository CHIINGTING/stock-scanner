package scanner

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// score returns a 0–100 score and human-readable reasons in Traditional Chinese.
//
// Scoring breakdown (max = 100):
//
//	RSI     max 25
//	MA20    max 25
//	KDJ     max 25
//	Volume  max 15
//	BB      max 10
func score(closes, volumes []float64, ind indicator.Result) (total int, reasons []string) {
	n := len(closes)
	if n == 0 {
		return 0, nil
	}

	// ── RSI (max 25) ─────────────────────────────────────────────────────────
	rsi := ind.RSI[n-1]
	rsiPts, rsiMsg := scoreRSI(rsi)
	total += rsiPts
	reasons = append(reasons, rsiMsg)

	// ── MA20 Trend (max 25) ───────────────────────────────────────────────────
	rising := indicator.MA20ConsecutiveRising(ind.MA20)
	falling := indicator.MA20ConsecutiveFalling(ind.MA20)
	ma20Pts, ma20Msg := scoreMA20(rising, falling)
	total += ma20Pts
	reasons = append(reasons, ma20Msg)

	// ── KDJ (max 25) ──────────────────────────────────────────────────────────
	kdjPts, kdjMsg := scoreKDJ(ind.KDJ)
	total += kdjPts
	reasons = append(reasons, kdjMsg)

	// ── Volume (max 15) ───────────────────────────────────────────────────────
	volRatio := volumeRatio(volumes, ind.VolumeMA)
	volPts, volMsg := scoreVolume(volRatio)
	total += volPts
	if volMsg != "" {
		reasons = append(reasons, volMsg)
	}

	// ── Bollinger (max 10) ────────────────────────────────────────────────────
	bbPts, bbMsg := scoreBB(closes, ind.BB)
	total += bbPts
	if bbMsg != "" {
		reasons = append(reasons, bbMsg)
	}

	if total > 100 {
		total = 100
	}
	if total < 0 {
		total = 0
	}
	return total, reasons
}

func scoreRSI(rsi float64) (int, string) {
	switch {
	case rsi < 20:
		return 25, fmt.Sprintf("RSI(14)=%.1f 深度超賣，強反彈機率高", rsi)
	case rsi < 30:
		return 20, fmt.Sprintf("RSI(14)=%.1f 超賣區，潛在底部訊號", rsi)
	case rsi < 40:
		return 14, fmt.Sprintf("RSI(14)=%.1f 偏弱，跌深後有反彈空間", rsi)
	case rsi < 50:
		return 8, fmt.Sprintf("RSI(14)=%.1f 中性偏弱", rsi)
	case rsi < 60:
		return 5, fmt.Sprintf("RSI(14)=%.1f 中性偏強", rsi)
	case rsi < 70:
		return 2, fmt.Sprintf("RSI(14)=%.1f 強勢，注意追高風險", rsi)
	default:
		return -10, fmt.Sprintf("RSI(14)=%.1f 超買（>70），回調壓力大", rsi)
	}
}

func scoreMA20(rising, falling int) (int, string) {
	switch {
	case rising >= 5:
		return 25, fmt.Sprintf("MA20 連 %d 日上揚，趨勢強勁向上", rising)
	case rising >= 3:
		return 20, fmt.Sprintf("MA20 連 %d 日翻揚，趨勢確認轉多", rising)
	case rising == 2:
		return 12, "MA20 連2日上升，短期趨勢改善"
	case rising == 1:
		return 6, "MA20 昨日止跌翻揚，尚待確認"
	case falling >= 5:
		return -20, fmt.Sprintf("MA20 連 %d 日下彎，空頭趨勢明確", falling)
	case falling >= 3:
		return -15, fmt.Sprintf("MA20 連 %d 日下彎，趨勢偏空", falling)
	case falling >= 1:
		return -8, "MA20 下彎，短期趨勢轉弱"
	default:
		return 0, "MA20 走平，趨勢不明"
	}
}

func scoreKDJ(kdj indicator.KDJResult) (int, string) {
	n := len(kdj.K)
	if n < 2 {
		return 0, "KDJ 資料不足"
	}
	k, d, j := kdj.K[n-1], kdj.D[n-1], kdj.J[n-1]
	prevK, prevD := kdj.K[n-2], kdj.D[n-2]
	goldenCross := prevK < prevD && k >= d
	deathCross := prevK > prevD && k <= d

	switch {
	case goldenCross && k < 30:
		return 25, fmt.Sprintf("KDJ 低檔黃金交叉（K=%.1f 穿越 D=%.1f），強力多頭訊號", k, d)
	case goldenCross:
		return 20, fmt.Sprintf("KDJ 黃金交叉（K=%.1f 上穿 D=%.1f），多頭轉強", k, d)
	case deathCross && k > 70:
		return -20, fmt.Sprintf("KDJ 高檔死亡交叉（K=%.1f 下穿 D=%.1f），強力空頭訊號", k, d)
	case deathCross:
		return -15, fmt.Sprintf("KDJ 死亡交叉（K=%.1f 下穿 D=%.1f），空頭轉弱", k, d)
	case k > d && j > k && k < 50:
		return 18, fmt.Sprintf("KDJ 多頭排列（K=%.1f>D=%.1f），低檔J=%.1f 動能強", k, d, j)
	case k > d && j > k:
		return 12, fmt.Sprintf("KDJ 多頭排列（K=%.1f>D=%.1f，J=%.1f）", k, d, j)
	case k > d:
		return 7, fmt.Sprintf("KDJ K=%.1f 在 D=%.1f 上方，偏多格局", k, d)
	case k < d && j < k && k > 70:
		return -12, fmt.Sprintf("KDJ 高檔空頭排列（K=%.1f<D=%.1f），賣壓沉重", k, d)
	case k < d:
		return -5, fmt.Sprintf("KDJ K=%.1f 在 D=%.1f 下方，偏空格局", k, d)
	default:
		return 0, fmt.Sprintf("KDJ 中性（K=%.1f，D=%.1f）", k, d)
	}
}

func scoreVolume(ratio float64) (int, string) {
	switch {
	case ratio >= 4.0:
		return 15, fmt.Sprintf("超級爆量 %.1fx，異常大量資金進場", ratio)
	case ratio >= 2.5:
		return 12, fmt.Sprintf("爆量 %.1fx MA20，主力積極買入", ratio)
	case ratio >= 1.5:
		return 7, fmt.Sprintf("放量 %.1fx MA20，量能溫和擴張", ratio)
	case ratio >= 0.8:
		return 2, "" // 正常，不特別說明
	case ratio > 0:
		return -5, fmt.Sprintf("縮量 %.1fx MA20，市場觀望，交投清淡", ratio)
	default:
		return 0, ""
	}
}

func scoreBB(closes []float64, bb indicator.BollingerResult) (int, string) {
	n := len(closes)
	if n < 2 || bb.Upper[n-1] == 0 {
		return 0, ""
	}
	close := closes[n-1]
	width := bb.Width[n-1]
	prevWidth := bb.Width[n-2]
	upper := bb.Upper[n-1]
	lower := bb.Lower[n-1]
	middle := bb.Middle[n-1]

	isSqueeze := width > 0 && width < 0.05
	wasBreakout := close > upper
	isExpanding := prevWidth > 0 && width > prevWidth*1.2
	isBullishExpand := isExpanding && close > middle
	isBearishExpand := isExpanding && close < middle

	switch {
	case isSqueeze && wasBreakout:
		return 10, fmt.Sprintf("布林帶收斂後突破上軌（%.2f），波段啟動訊號", upper)
	case isSqueeze:
		return 7, fmt.Sprintf("布林帶極度收斂（帶寬%.1f%%），蓄勢待發", width*100)
	case isBullishExpand:
		return 8, fmt.Sprintf("布林帶向上擴張（帶寬%.1f%%），多頭動能釋放", width*100)
	case isBearishExpand:
		return -8, fmt.Sprintf("布林帶向下擴張（帶寬%.1f%%），空頭動能釋放", width*100)
	case close < lower && lower > 0:
		return 5, fmt.Sprintf("收盤跌破布林下軌（%.2f），超賣後可留意反彈", lower)
	default:
		return 0, ""
	}
}

// actionFromScore converts a score to an Action level.
// For portfolio stocks, pnlPct can further adjust the recommendation.
func actionFromScore(s int, source string, pnlPct float64) Action {
	action := rawAction(s)
	if source != "portfolio" {
		return action
	}
	// portfolio position context: adjust for large gains / losses
	switch {
	case pnlPct >= 30 && (action == ActionWatch || action == ActionHold):
		return ActionReduce
	case pnlPct <= -20 && (action == ActionHold || action == ActionWatch):
		return ActionSell
	case pnlPct <= -15 && action == ActionReduce:
		return ActionSell
	}
	return action
}

func rawAction(s int) Action {
	switch {
	case s >= 78:
		return ActionStrongBuy
	case s >= 62:
		return ActionBuy
	case s >= 47:
		return ActionWatch
	case s >= 32:
		return ActionHold
	case s >= 18:
		return ActionReduce
	default:
		return ActionSell
	}
}

// priceTargets computes entry, stop-loss, target1, target2 based on ATR and BB.
func priceTargets(close, atr float64, bb indicator.BollingerResult) (entry, stop, t1, t2 float64) {
	n := len(bb.Lower)
	var bbLower, bbUpper float64
	if n > 0 {
		bbLower = bb.Lower[n-1]
		bbUpper = bb.Upper[n-1]
	}
	if atr <= 0 {
		atr = close * 0.02 // fallback: 2% of price
	}

	entry = close

	// stop = max(lower BB − 0.5×ATR, entry − 2×ATR)  →  higher of the two (tighter)
	stopBB := bbLower - atr*0.5
	stopATR := entry - 2.0*atr
	stop = math.Max(stopBB, stopATR)
	if stop <= 0 || stop >= entry {
		stop = entry * 0.93 // fallback 7%
	}

	risk := entry - stop

	// Target 1: upper BB or 2× risk, whichever is higher
	t1Rr := entry + risk*2.0
	if bbUpper > entry && bbUpper > t1Rr {
		t1 = bbUpper
	} else {
		t1 = t1Rr
	}

	// Target 2: 3.5× risk
	t2 = entry + risk*3.5

	// round to 1 decimal
	entry = math.Round(entry*10) / 10
	stop = math.Round(stop*10) / 10
	t1 = math.Round(t1*10) / 10
	t2 = math.Round(t2*10) / 10
	return
}

func volumeRatio(volumes []float64, volumeMA []float64) float64 {
	n := len(volumes)
	if n == 0 || volumeMA[n-1] == 0 {
		return 0
	}
	return volumes[n-1] / volumeMA[n-1]
}
