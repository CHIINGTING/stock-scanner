package scanner

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// Limit-up (漲停) chip dynamics
//
// Taiwan's daily price limit is ±10%. We only have daily OHLCV bars (no intraday
// tick / 封單 data), so the "封板 / 打開 / 出貨" dynamics are approximated from the
// day's open/high/low/close and volume ratio.
//
// 核心原則：量縮本身不是問題，問題是「量縮時價格有沒有失守」。
//   - 漲停鎖住後量縮 → 賣壓惜售、籌碼鎖定 → 不扣分（中性偏多）。
//   - 只有漲停打開後放量下跌，才是明確警訊。
// ──────────────────────────────────────────────────────────────────────────────

// detectLimitStatus inspects the latest bar and returns a LimitStatus + interpretation,
// or ("", "") when no limit-up dynamic applies.
func detectLimitStatus(candles []fetcher.Candle, volRatio float64) (status, note string) {
	n := len(candles)
	if n < 2 {
		return "", ""
	}
	today, prev := candles[n-1], candles[n-2]
	if prev.Close <= 0 || today.High <= 0 {
		return "", ""
	}

	gain := (today.Close/prev.Close - 1) * 100     // 收盤漲幅
	highGain := (today.High/prev.Close - 1) * 100  // 盤中最高漲幅（是否曾觸及漲停）
	closedAtHigh := today.Close >= today.High*0.998 // 收在當日最高（封住）

	// 1) 漲停打開後放量下殺：盤中觸及漲停，但收盤大幅拉回且放量。
	if highGain >= 9.0 && today.Close <= today.High*0.97 && volRatio >= 1.5 {
		return LimitUpFailed,
			"⚠️ 漲停打開後放量下殺、無法重新封板，賣壓湧現 — 明確負面訊號"
	}

	// 2) 漲停後出貨：前一日漲停鎖住，今日放量下跌。
	if n >= 3 && candles[n-3].Close > 0 {
		prevGain := (prev.Close/candles[n-3].Close - 1) * 100
		prevLocked := prevGain >= 9.5 && prev.Close >= prev.High*0.998
		if prevLocked && today.Close < prev.Close && volRatio >= 1.5 {
			return LimitDistribution,
				"⚠️ 前一日漲停鎖住後，今日放量下跌，疑似漲停後出貨 — 負面訊號"
		}
	}

	// 3) 漲停鎖住量縮：價格接近/已漲停、收在最高、量縮（賣方惜售、籌碼鎖定）。
	if gain >= 9.0 && closedAtHigh && volRatio > 0 && volRatio < 1.0 {
		return LimitLockedLowVol,
			"🔒 漲停鎖住後量縮，可能代表賣壓不足、籌碼鎖定，不應直接視為轉弱（中性偏多）"
	}

	return "", ""
}

// ──────────────────────────────────────────────────────────────────────────────
// BestFourPoint-inspired Trading Advice Engine
//
// Inspired by twstock's BestFourPoint design philosophy:
//   - Don't show raw indicator numbers — give actionable trading advice.
//   - Check multiple independent conditions.
//   - Require confirmation from several factors before recommending action.
//
// 5 Trading Checkpoints:
//   1. 趨勢  (Trend)      – Is the price in an uptrend?
//   2. 動能  (Momentum)   – Is RSI in a favorable zone?
//   3. 擺盪  (Oscillator) – Does KDJ confirm the direction?
//   4. 量能  (Volume)     – Does volume confirm the price move?
//   5. 突破  (Structure)  – Does price/BB structure support entry?
//
// Each checkpoint is PASS or FAIL.
// Count of PASSes → base Action level.
// ──────────────────────────────────────────────────────────────────────────────

// bestFourPoint evaluates all 5 trading checkpoints and returns results.
func bestFourPoint(closes, volumes []float64, highs, lows []float64, ind indicator.Result, limitStatus, limitNote string) ([]BFPCheckpoint, int) {
	n := len(closes)
	if n < 2 {
		return nil, 0
	}

	var checks []BFPCheckpoint
	passes := 0

	// ── Checkpoint 1: 趨勢 (Trend) ───────────────────────────────────────────
	{
		ma20 := ind.MA20[n-1]
		rising := indicator.MA20ConsecutiveRising(ind.MA20)
		aboveMA := ma20 > 0 && closes[n-1] > ma20
		pass := aboveMA && rising >= 2
		reason := ""
		if pass {
			reason = fmt.Sprintf("價格 %.2f 站上MA20（%.2f），且MA20連%d日上揚", closes[n-1], ma20, rising)
		} else if aboveMA {
			reason = fmt.Sprintf("價格站上MA20，但MA20尚未連續上揚（%d日）", rising)
		} else {
			falling := indicator.MA20ConsecutiveFalling(ind.MA20)
			if falling > 0 {
				reason = fmt.Sprintf("價格跌破MA20，MA20連%d日下彎，趨勢偏空", falling)
			} else {
				reason = fmt.Sprintf("價格（%.2f）低於MA20（%.2f），趨勢不利", closes[n-1], ma20)
			}
		}
		if pass {
			passes++
		}
		checks = append(checks, BFPCheckpoint{Name: "趨勢", Pass: pass, Reason: reason})
	}

	// ── Checkpoint 2: 動能 (Momentum / RSI) ──────────────────────────────────
	{
		rsi := ind.RSI[n-1]
		// Optimal RSI zone: 25–65 (not damaged, not overbought)
		pass := rsi >= 25 && rsi <= 65
		reason := ""
		switch {
		case rsi < 20:
			reason = fmt.Sprintf("RSI=%.1f 深度超賣（<20），技術面受損嚴重，等待企穩", rsi)
		case rsi < 30:
			reason = fmt.Sprintf("RSI=%.1f 超賣區，有反彈機會但需量能確認", rsi)
		case rsi <= 65:
			reason = fmt.Sprintf("RSI=%.1f 處於最佳進場區間（25~65），動能健康", rsi)
		case rsi <= 70:
			reason = fmt.Sprintf("RSI=%.1f 偏強，接近超買邊緣，注意追高風險", rsi)
		default:
			reason = fmt.Sprintf("RSI=%.1f 超買（>70），短期回調壓力大，不宜追高", rsi)
		}
		// 超賣 (20-30) 也視為 PASS（底部反彈機會）
		if rsi >= 20 && rsi < 30 {
			pass = true
		}
		if pass {
			passes++
		}
		checks = append(checks, BFPCheckpoint{Name: "動能", Pass: pass, Reason: reason})
	}

	// ── Checkpoint 3: 擺盪 (Oscillator / KDJ) ────────────────────────────────
	{
		kn, dn := ind.KDJ.K[n-1], ind.KDJ.D[n-1]
		kp, dp := ind.KDJ.K[n-2], ind.KDJ.D[n-2]
		goldenCross := kp < dp && kn >= dn
		deathCross := kp > dp && kn <= dn
		bullish := kn > dn && kn < 80
		pass := goldenCross || bullish

		reason := ""
		switch {
		case goldenCross && kn < 30:
			reason = fmt.Sprintf("KDJ 低檔黃金交叉（K=%.1f 上穿 D=%.1f），最強買入訊號", kn, dn)
		case goldenCross:
			reason = fmt.Sprintf("KDJ 黃金交叉（K=%.1f 上穿 D=%.1f），多頭啟動訊號", kn, dn)
		case deathCross:
			reason = fmt.Sprintf("KDJ 死亡交叉（K=%.1f 下穿 D=%.1f），空頭訊號，不宜進場", kn, dn)
		case bullish:
			reason = fmt.Sprintf("KDJ 多頭排列（K=%.1f > D=%.1f），偏多格局", kn, dn)
		default:
			reason = fmt.Sprintf("KDJ 空頭排列（K=%.1f < D=%.1f），偏空格局", kn, dn)
		}
		if pass {
			passes++
		}
		checks = append(checks, BFPCheckpoint{Name: "擺盪", Pass: pass, Reason: reason})
	}

	// ── Checkpoint 4: 量能 (Volume) ──────────────────────────────────────────
	{
		vr := volumeRatio(volumes, ind.VolumeMA)
		priceUp := n >= 2 && closes[n-1] > closes[n-2]
		volUp := vr >= 1.3
		// 量能確認：價漲量增 OR 量明顯放大
		pass := (priceUp && volUp) || vr >= 2.0

		reason := ""
		switch {
		case priceUp && vr >= 2.5:
			reason = fmt.Sprintf("價漲量增，成交量暴增 %.1fx，主力積極買進", vr)
		case priceUp && volUp:
			reason = fmt.Sprintf("價漲量增（%.1fx），量能配合走揚，多頭訊號明確", vr)
		case priceUp && !volUp:
			reason = fmt.Sprintf("價漲量縮（%.1fx），漲幅缺乏量能支撐，注意假突破", vr)
		case !priceUp && vr >= 2.0:
			reason = fmt.Sprintf("價跌量增（%.1fx），賣壓沉重，需等量縮止穩", vr)
		default:
			reason = fmt.Sprintf("量能不足（%.1fx MA20量），市場觀望氣氛濃厚", vr)
		}
		// 大單偵測
		if vr >= 3.0 {
			if priceUp {
				reason += fmt.Sprintf("  ⚡ 大單偵測：成交量超過 %.0f 倍均量，疑似法人/主力積極建倉", vr)
			} else {
				reason += fmt.Sprintf("  ⚠️ 大單偵測：成交量超過 %.0f 倍均量，疑似主力出貨，謹慎因應", vr)
			}
		}
		// 漲停籌碼動態特例：量縮 ≠ 轉弱。覆寫量能判斷。
		switch limitStatus {
		case LimitLockedLowVol:
			pass = true // 漲停鎖住後量縮不視為量能轉弱，給予通過
			reason = "漲停鎖住後量縮（賣壓不足、籌碼鎖定），量能不視為轉弱"
			if limitNote != "" {
				reason = limitNote
			}
		case LimitUpFailed, LimitDistribution:
			pass = false
			if limitNote != "" {
				reason = limitNote
			}
		}
		if pass {
			passes++
		}
		checks = append(checks, BFPCheckpoint{Name: "量能", Pass: pass, Reason: reason})
	}

	// ── Checkpoint 5: 突破 (Structure / BB) ─────────────────────────────────
	{
		bbW := ind.BB.Width[n-1]
		bbU := ind.BB.Upper[n-1]
		bbL := ind.BB.Lower[n-1]
		bbM := ind.BB.Middle[n-1]
		prevW := ind.BB.Width[n-2]

		isSqueeze := bbW > 0 && bbW < 0.05
		breakout := closes[n-1] > bbU && bbU > 0
		bullExpand := prevW > 0 && bbW > prevW*1.15 && closes[n-1] > bbM
		aboveMid := closes[n-1] > bbM && bbM > 0
		pass := breakout || (isSqueeze && aboveMid) || bullExpand || aboveMid

		reason := ""
		switch {
		case isSqueeze && breakout:
			reason = fmt.Sprintf("布林帶收縮後突破上軌（%.2f），波段行情啟動", bbU)
		case breakout:
			reason = fmt.Sprintf("突破布林上軌（%.2f），短線強勢", bbU)
		case isSqueeze:
			reason = fmt.Sprintf("布林帶極度收縮（帶寬%.1f%%），蓄勢格局，等待方向確認", bbW*100)
		case bullExpand:
			reason = fmt.Sprintf("布林帶向上擴張（帶寬%.1f%%），多頭動能釋放", bbW*100)
		case closes[n-1] < bbL && bbL > 0:
			reason = fmt.Sprintf("跌破布林下軌（%.2f），超賣但空頭格局，等待止穩", bbL)
			pass = false
		case aboveMid:
			reason = fmt.Sprintf("價格站上布林中線（%.2f），結構偏多", bbM)
		default:
			reason = fmt.Sprintf("價格低於布林中線（%.2f），結構偏空", bbM)
		}
		if pass {
			passes++
		}
		checks = append(checks, BFPCheckpoint{Name: "突破", Pass: pass, Reason: reason})
	}

	return checks, passes
}

// ──────────────────────────────────────────────────────────────────────────────
// Volume Analysis
// ──────────────────────────────────────────────────────────────────────────────

type volumeResult struct {
	score         int     // 0–25
	ratio         float64
	signal        string  // 價漲量增 / 價漲量縮 / 價跌量增 / 價跌量縮
	buySellRatio  float64
	isLargeOrder  bool
	avgVol20      int64
	reasons       []string
}

func analyzeVolume(closes []float64, rawVols []float64, ind indicator.Result, limitStatus, limitNote string) volumeResult {
	n := len(closes)
	res := volumeResult{}
	if n < 2 || ind.VolumeMA[n-1] == 0 {
		return res
	}

	res.ratio = rawVols[n-1] / ind.VolumeMA[n-1]
	res.avgVol20 = int64(ind.VolumeMA[n-1])
	res.isLargeOrder = res.ratio >= 3.0

	// ── 漲停籌碼動態特例：量縮 ≠ 轉弱 ─────────────────────────────────────────
	switch limitStatus {
	case LimitLockedLowVol:
		// 漲停鎖住後量縮：賣壓惜售、籌碼鎖定，給中性偏多分數，不因量縮扣分。
		res.signal = "漲停鎖量"
		res.isLargeOrder = false
		res.score = 18
		res.buySellRatio = calcBuySellRatio(closes, ind.VolumeMA)
		if limitNote != "" {
			res.reasons = append(res.reasons, limitNote)
		}
		return res
	case LimitUpFailed, LimitDistribution:
		// 漲停打開後放量下殺 / 漲停後出貨：明確負面。
		res.signal = "漲停失敗"
		res.score = 0
		res.buySellRatio = calcBuySellRatio(closes, ind.VolumeMA)
		if limitNote != "" {
			res.reasons = append(res.reasons, limitNote)
		}
		return res
	}

	priceUp := closes[n-1] > closes[n-2]
	volUp := res.ratio >= 1.2

	// Price-volume signal
	switch {
	case priceUp && volUp:
		res.signal = "價漲量增"
	case priceUp && !volUp:
		res.signal = "價漲量縮"
	case !priceUp && volUp:
		res.signal = "價跌量增"
	default:
		res.signal = "價跌量縮"
	}

	// Buy/sell ratio: approximate from recent candle position
	// Williams %R variant: (close-low)/(high-low) per bar
	// Average over last 5 bars vs MA20 period
	buySell := calcBuySellRatio(closes, ind.VolumeMA)
	res.buySellRatio = buySell

	// Volume score (max 25)
	s := 0
	switch {
	case res.ratio >= 4.0 && priceUp:
		s = 25
		res.reasons = append(res.reasons, fmt.Sprintf("超級爆量 %.1fx（大單確認），主力積極介入", res.ratio))
	case res.ratio >= 2.5 && priceUp:
		s = 20
		res.reasons = append(res.reasons, fmt.Sprintf("爆量 %.1fx，量能強勢配合漲勢", res.ratio))
	case res.ratio >= 1.5 && priceUp:
		s = 15
		res.reasons = append(res.reasons, fmt.Sprintf("放量 %.1fx，量能溫和確認漲勢", res.ratio))
	case res.ratio >= 1.5 && !priceUp:
		s = 5
		res.reasons = append(res.reasons, fmt.Sprintf("量增 %.1fx 但下跌，賣壓較重", res.ratio))
	case res.ratio >= 0.8:
		s = 8
	case res.ratio > 0:
		s = 2
		res.reasons = append(res.reasons, fmt.Sprintf("縮量（%.1fx），市場觀望，量能不足", res.ratio))
	}

	// Buy/sell ratio bonus
	if buySell > 1.5 {
		s = min(s+5, 25)
		res.reasons = append(res.reasons, fmt.Sprintf("買賣比 %.1f，買盤明顯強於賣盤", buySell))
	} else if buySell < 0.7 {
		s = max(s-5, 0)
		res.reasons = append(res.reasons, fmt.Sprintf("買賣比 %.1f，賣盤明顯強於買盤", buySell))
	}

	res.score = s
	return res
}

// calcBuySellRatio approximates buy/sell pressure from OHLCV.
// Uses candle position: (close - low) / (high - low) averaged over recent bars.
// Returns ratio: > 1 = buying pressure dominant, < 1 = selling pressure dominant.
func calcBuySellRatio(closes []float64, volumeMA []float64) float64 {
	// Without high/low data at this call site, use close momentum as proxy.
	// A proper implementation would use (close - low) / (high - low) from Candles.
	n := len(closes)
	if n < 5 {
		return 1.0
	}
	var upDays, totalDays float64
	for i := n - 5; i < n; i++ {
		totalDays++
		if i > 0 && closes[i] > closes[i-1] {
			upDays++
		}
	}
	if totalDays == 0 {
		return 1.0
	}
	upRatio := upDays / totalDays
	if upRatio == 0 {
		return 0.2
	}
	return upRatio / (1 - upRatio + 0.01)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ──────────────────────────────────────────────────────────────────────────────
// Composite Score
// ──────────────────────────────────────────────────────────────────────────────

// score computes the composite 0–100 score and detailed reasons.
//
// Weight breakdown:
//
//	MA20    max 20
//	RSI     max 20
//	KDJ     max 20
//	Volume  max 25  ← increased weight per user request
//	BB      max 15
func score(closes, volumes []float64, ind indicator.Result, limitStatus, limitNote string) (total int, reasons []string) {
	n := len(closes)
	if n == 0 {
		return 0, nil
	}

	// MA20 (max 20)
	rising := indicator.MA20ConsecutiveRising(ind.MA20)
	falling := indicator.MA20ConsecutiveFalling(ind.MA20)
	ma20Pts, ma20Msg := scoreMA20(ind.MA20[n-1], closes[n-1], rising, falling)
	total += ma20Pts
	reasons = append(reasons, ma20Msg)

	// RSI (max 20)
	rsi := ind.RSI[n-1]
	rsiPts, rsiMsg := scoreRSI(rsi)
	total += rsiPts
	reasons = append(reasons, rsiMsg)

	// KDJ (max 20)
	kdjPts, kdjMsg := scoreKDJ(ind.KDJ)
	total += kdjPts
	reasons = append(reasons, kdjMsg)

	// Volume (max 25) – use analyzeVolume result
	va := analyzeVolume(closes, volumes, ind, limitStatus, limitNote)
	total += va.score
	for _, r := range va.reasons {
		if r != "" {
			reasons = append(reasons, r)
		}
	}

	// Bollinger (max 15)
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

func scoreMA20(ma20val, close float64, rising, falling int) (int, string) {
	aboveMA := ma20val > 0 && close > ma20val
	switch {
	case rising >= 5 && aboveMA:
		return 20, fmt.Sprintf("MA20 連 %d 日上揚，強勢趨勢；價格站上均線，多頭有利", rising)
	case rising >= 3 && aboveMA:
		return 17, fmt.Sprintf("MA20 連 %d 日翻揚，趨勢確認轉多", rising)
	case rising >= 2 && aboveMA:
		return 12, "MA20 連2日上升，短期趨勢改善，等待確認"
	case rising >= 1 && aboveMA:
		return 7, "MA20 昨日止跌翻揚，尚待觀察"
	case rising >= 1:
		return 4, "MA20 小幅上揚但價格未站上均線，力道不足"
	case falling >= 5:
		return -15, fmt.Sprintf("MA20 連 %d 日下彎，空頭趨勢明確，不宜做多", falling)
	case falling >= 3:
		return -10, fmt.Sprintf("MA20 連 %d 日下彎，趨勢偏空", falling)
	case falling >= 1:
		return -5, "MA20 下彎，短期趨勢轉弱"
	default:
		return 0, "MA20 走平，趨勢方向不明"
	}
}

func scoreRSI(rsi float64) (int, string) {
	switch {
	case rsi < 20:
		return 20, fmt.Sprintf("RSI=%.1f 深度超賣，但需注意基本面風險", rsi)
	case rsi < 30:
		return 17, fmt.Sprintf("RSI=%.1f 超賣區（<30），歷史上此區間正期望值較高", rsi)
	case rsi < 40:
		return 13, fmt.Sprintf("RSI=%.1f 偏弱但尚未超賣，有反彈空間", rsi)
	case rsi < 50:
		return 8, fmt.Sprintf("RSI=%.1f 中性偏弱，觀察是否止跌", rsi)
	case rsi < 60:
		return 5, fmt.Sprintf("RSI=%.1f 中性偏強，趨勢良好", rsi)
	case rsi < 70:
		return 2, fmt.Sprintf("RSI=%.1f 偏強，注意追高風險", rsi)
	default:
		return -12, fmt.Sprintf("RSI=%.1f 超買（>70），短期回調機率高，不宜追漲", rsi)
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
		return 20, fmt.Sprintf("KDJ 低檔黃金交叉（K=%.1f 穿越 D=%.1f）— 最強多頭訊號", k, d)
	case goldenCross:
		return 17, fmt.Sprintf("KDJ 黃金交叉（K=%.1f 上穿 D=%.1f）— 多頭轉強", k, d)
	case deathCross && k > 70:
		return -18, fmt.Sprintf("KDJ 高檔死亡交叉（K=%.1f 下穿 D=%.1f）— 強烈賣出訊號", k, d)
	case deathCross:
		return -12, fmt.Sprintf("KDJ 死亡交叉（K=%.1f 下穿 D=%.1f）— 空頭訊號，謹慎", k, d)
	case k > d && j > k && k < 50:
		return 15, fmt.Sprintf("KDJ 低檔多頭排列（K=%.1f>D=%.1f，J=%.1f）— 強勢發動準備", k, d, j)
	case k > d && j > k:
		return 10, fmt.Sprintf("KDJ 多頭排列（K=%.1f>D=%.1f，J=%.1f）", k, d, j)
	case k > d:
		return 6, fmt.Sprintf("KDJ K=%.1f 在 D=%.1f 上方，偏多格局", k, d)
	case k < d && k > 70:
		return -10, fmt.Sprintf("KDJ 高位空頭（K=%.1f<D=%.1f），賣壓沉重", k, d)
	case k < d:
		return -4, fmt.Sprintf("KDJ K=%.1f 在 D=%.1f 下方，偏空格局", k, d)
	default:
		return 0, fmt.Sprintf("KDJ 中性（K=%.1f，D=%.1f）", k, d)
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
	isExpanding := prevWidth > 0 && width > prevWidth*1.15
	isBullishExpand := isExpanding && close > middle
	isBearishExpand := isExpanding && close < middle

	switch {
	case isSqueeze && wasBreakout:
		return 15, fmt.Sprintf("布林帶收縮後突破上軌（%.2f），波段行情啟動訊號", upper)
	case isSqueeze:
		return 10, fmt.Sprintf("布林帶極度收縮（帶寬%.1f%%），蓄勢待發", width*100)
	case isBullishExpand:
		return 12, fmt.Sprintf("布林帶向上擴張（帶寬%.1f%%），多頭動能釋放", width*100)
	case isBearishExpand:
		return -8, fmt.Sprintf("布林帶向下擴張（帶寬%.1f%%），空頭動能釋放", width*100)
	case close < lower && lower > 0:
		return 5, fmt.Sprintf("跌破布林下軌（%.2f），技術超賣", lower)
	case close > middle && middle > 0:
		return 5, fmt.Sprintf("站上布林中線（%.2f），短線偏多", middle)
	default:
		return -2, fmt.Sprintf("低於布林中線（%.2f），短線偏空", middle)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Action Determination
// ──────────────────────────────────────────────────────────────────────────────

// actionFromBFP maps BFP checkpoint count to a base Action.
func actionFromBFP(points int) Action {
	switch {
	case points >= 5:
		return ActionStrongBuy
	case points >= 4:
		return ActionBuy
	case points >= 3:
		return ActionWatch
	case points >= 2:
		return ActionHold
	case points >= 1:
		return ActionReduce
	default:
		return ActionSell
	}
}

// rawAction maps a numeric score to an Action (used as secondary signal).
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

// blendAction takes BFP action and score action and returns the more conservative one
// (avoids false positives by requiring both signals to agree).
func blendAction(bfp Action, scoreAction Action) Action {
	rank := map[Action]int{
		ActionSell:       0,
		ActionReduce:     1,
		ActionHold:       2,
		ActionWatch:      3,
		ActionBuy:        4,
		ActionStrongBuy:  5,
		ActionTakeProfit: 4,
		ActionStopLoss:   0,
	}
	if rank[bfp] < rank[scoreAction] {
		return bfp
	}
	return scoreAction
}

// positionAdvice checks if a position warrants STOP LOSS or TAKE PROFIT,
// overriding the base action. Returns (overrideAction, reason) or ("", "") if no override.
func positionAdvice(pnlPct, rsi float64, ma20 []float64, kdj indicator.KDJResult) (Action, string) {
	n := len(ma20)
	if n < 2 {
		return "", ""
	}
	k, d := kdj.K[n-1], kdj.D[n-1]
	kp, dp := kdj.K[n-2], kdj.D[n-2]
	deathCross := kp > dp && k <= d
	ma20Down := indicator.MA20ConsecutiveFalling(ma20) >= 2

	// ── STOP LOSS ────────────────────────────────────────────────────────────
	switch {
	case pnlPct <= -15:
		return ActionStopLoss, fmt.Sprintf(
			"虧損已達 %.1f%%（>15%%），建議立即停損出場，保留資金等待下次機會", pnlPct)
	case pnlPct <= -7 && deathCross && ma20Down:
		return ActionStopLoss, fmt.Sprintf(
			"虧損 %.1f%% 且 MA20 下彎＋KDJ 死亡交叉雙重確認，技術面持續惡化，強烈建議停損", pnlPct)
	case pnlPct <= -10 && (deathCross || ma20Down):
		return ActionStopLoss, fmt.Sprintf(
			"虧損 %.1f%% 且技術指標走弱，建議執行停損避免損失擴大", pnlPct)
	}

	// ── TAKE PROFIT ──────────────────────────────────────────────────────────
	switch {
	case pnlPct >= 30:
		return ActionTakeProfit, fmt.Sprintf(
			"浮盈已達 %.1f%%（>30%%），建議分批獲利了結，鎖定豐厚報酬", pnlPct)
	case pnlPct >= 15 && rsi > 72:
		return ActionTakeProfit, fmt.Sprintf(
			"浮盈 +%.1f%% 且 RSI=%.1f 進入超買區，建議獲利了結", pnlPct, rsi)
	case pnlPct >= 15 && deathCross:
		return ActionTakeProfit, fmt.Sprintf(
			"浮盈 +%.1f%% 且 KDJ 死亡交叉，動能轉弱，建議獲利了結", pnlPct)
	case pnlPct >= 20 && ma20Down:
		return ActionTakeProfit, fmt.Sprintf(
			"浮盈 +%.1f%% 且 MA20 開始下彎，建議保護獲利", pnlPct)
	}

	// ── REDUCE ───────────────────────────────────────────────────────────────
	if pnlPct >= 12 && rsi > 65 {
		return ActionReduce, fmt.Sprintf(
			"浮盈 +%.1f%% 且 RSI=%.1f 偏高，可考慮分批減碼鎖定部分利潤", pnlPct, rsi)
	}

	return "", ""
}

// ──────────────────────────────────────────────────────────────────────────────
// Price Targets
// ──────────────────────────────────────────────────────────────────────────────

// priceTargets computes entry, stop-loss, target1, target2 using ATR and BB.
func priceTargets(close, atr float64, bb indicator.BollingerResult) (entry, stop, t1, t2 float64) {
	n := len(bb.Lower)
	var bbLower, bbUpper float64
	if n > 0 {
		bbLower = bb.Lower[n-1]
		bbUpper = bb.Upper[n-1]
	}
	if atr <= 0 {
		atr = close * 0.025
	}

	entry = close

	// Stop loss: tighter of ATR-based or BB-based
	stopATR := entry - 2.0*atr
	stopBB := bbLower * 0.99
	stop = math.Max(stopATR, stopBB)
	if stop <= 0 || stop >= entry {
		stop = entry * 0.93
	}

	risk := entry - stop

	// Target 1: 2× risk or upper BB, whichever is higher
	t1 = entry + risk*2.0
	if bbUpper > t1 {
		t1 = bbUpper
	}

	// Target 2: 3.5× risk
	t2 = entry + risk*3.5

	// Round to 1 decimal
	entry = math.Round(entry*10) / 10
	stop = math.Round(stop*10) / 10
	t1 = math.Round(t1*10) / 10
	t2 = math.Round(t2*10) / 10
	return
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

func volumeRatio(volumes []float64, volumeMA []float64) float64 {
	n := len(volumes)
	if n == 0 || volumeMA[n-1] == 0 {
		return 0
	}
	return volumes[n-1] / volumeMA[n-1]
}
