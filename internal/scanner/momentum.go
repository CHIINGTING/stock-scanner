package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// MomentumFlow (C5)
//
// 個股「動能方向」：累積 / 延續 / 衰退 / 結構轉折。RocketStage 描述位置，MomentumFlow
// 描述方向，兩者正交。C5 只建立「資料模型 + helper + config + 測試」，不接入既有 scoring /
// report / watchlist / rotation。所有函式皆為 pure，只在被明確呼叫時運算；
// EnableMomentumFlow=false 時 pipeline 不呼叫它們（golden regression by construction）。
// RocketStage × MomentumFlow 的聯合決策與 RocketScore 修正一律留到 C6。
//
// 價格一律以還原收盤計算；swing 偵測唯讀複用 vcp.go 的 zigzagPivots；漲停鎖量唯讀複用
// detectLimitStatus，並據此尊重「量縮 ≠ 轉弱」（漲停鎖量不得判 FADING）。
// ──────────────────────────────────────────────────────────────────────────────

// MomentumFlow is the stock's momentum direction.
type MomentumFlow string

const (
	MomentumNeutral      MomentumFlow = "MOMENTUM_NEUTRAL"
	MomentumBuilding     MomentumFlow = "MOMENTUM_BUILDING"
	MomentumContinuation MomentumFlow = "MOMENTUM_CONTINUATION"
	MomentumFading       MomentumFlow = "MOMENTUM_FADING"
	StructuralShiftUp    MomentumFlow = "STRUCTURAL_SHIFT_UP"
	StructuralShiftDown  MomentumFlow = "STRUCTURAL_SHIFT_DOWN"
)

// Structure trend labels.
const (
	structHHHL       = "HH_HL"
	structLLLH       = "LH_LL"
	structHigherLows = "HIGHER_LOWS"
	structLowerHighs = "LOWER_HIGHS"
	structMixed      = "MIXED"
	structNeutral    = "NEUTRAL"
)

// Defaults applied when config leaves a knob zero.
const (
	defaultMFMinHistoryDays   = 30
	defaultMFAccelShortWindow = 3
	defaultMFAccelLongWindow  = 20
	defaultMFAccelPosThresh   = 0.0008
	defaultMFAccelNegThresh   = -0.0008
	defaultMFAccelScale       = 12000
	defaultMFKeyMA            = 20
	defaultMFReclaimLookback  = 5
	defaultMFZigzagRevPct     = 1.5
	defaultMFRSIDivLookback   = 20
	defaultMFShiftUpMinBelow  = 2 // R5-1
	defaultMFShiftUpConfirm   = 2 // R5-1

	mfStructWindow = 60   // bars used for swing-structure / divergence
	mfRSIMidLow    = 35.0 // BUILDING RSI lower bound
	mfRSIMidHigh   = 60.0 // BUILDING RSI upper bound
	mfExtFrom5Pct  = 12.0 // BUILDING "not overextended" guard
	mfVolBiasBars  = 10   // window for up/down volume bias
)

// MomentumConfig is the resolved configuration (defaults applied).
type MomentumConfig struct {
	Enable           bool
	MinHistoryDays   int
	AccelShortWindow int
	AccelLongWindow  int
	AccelPosThresh   float64
	AccelNegThresh   float64
	AccelScale       float64
	KeyMA            int
	ReclaimLookback  int
	ZigzagReversal   float64
	RSIDivLookback   int
	UseAdjustedClose bool

	ShiftUpMinBelowDays int // R5-1
	ShiftUpConfirmDays  int // R5-1
}

func momentumConfigFrom(cfg Config) MomentumConfig {
	mc := MomentumConfig{
		Enable:           cfg.EnableMomentumFlow,
		MinHistoryDays:   cfg.MFMinHistoryDays,
		AccelShortWindow: cfg.MFAccelShortWindow,
		AccelLongWindow:  cfg.MFAccelLongWindow,
		AccelPosThresh:   cfg.MFAccelPosThresh,
		AccelNegThresh:   cfg.MFAccelNegThresh,
		AccelScale:       cfg.MFAccelScale,
		KeyMA:            cfg.MFKeyMA,
		ReclaimLookback:  cfg.MFReclaimLookback,
		ZigzagReversal:   cfg.MFZigzagReversalPct,
		RSIDivLookback:      cfg.MFRSIDivLookback,
		UseAdjustedClose:    cfg.UseAdjustedClose || cfg.MFUseAdjustedClose,
		ShiftUpMinBelowDays: cfg.MFShiftUpMinBelowDays,
		ShiftUpConfirmDays:  cfg.MFShiftUpConfirmDays,
	}
	if mc.MinHistoryDays <= 0 {
		mc.MinHistoryDays = defaultMFMinHistoryDays
	}
	if mc.AccelShortWindow <= 0 {
		mc.AccelShortWindow = defaultMFAccelShortWindow
	}
	if mc.AccelLongWindow <= 0 {
		mc.AccelLongWindow = defaultMFAccelLongWindow
	}
	if mc.AccelPosThresh == 0 {
		mc.AccelPosThresh = defaultMFAccelPosThresh
	}
	if mc.AccelNegThresh == 0 {
		mc.AccelNegThresh = defaultMFAccelNegThresh
	}
	if mc.AccelScale <= 0 {
		mc.AccelScale = defaultMFAccelScale
	}
	if mc.KeyMA <= 0 {
		mc.KeyMA = defaultMFKeyMA
	}
	if mc.ReclaimLookback <= 0 {
		mc.ReclaimLookback = defaultMFReclaimLookback
	}
	if mc.ZigzagReversal <= 0 {
		mc.ZigzagReversal = defaultMFZigzagRevPct
	}
	if mc.RSIDivLookback <= 0 {
		mc.RSIDivLookback = defaultMFRSIDivLookback
	}
	if mc.ShiftUpMinBelowDays <= 0 {
		mc.ShiftUpMinBelowDays = defaultMFShiftUpMinBelow
	}
	if mc.ShiftUpConfirmDays <= 0 {
		mc.ShiftUpConfirmDays = defaultMFShiftUpConfirm
	}
	return mc
}

// MomentumState is the per-stock momentum result.
type MomentumState struct {
	Computed       bool
	Flow           MomentumFlow
	Score          float64 // 0–100 bullish-momentum conviction
	SlopeAccel     float64 // short-window pace − long-window pace
	Divergence     bool    // bearish price/volume or price/RSI divergence
	StructureTrend string
	WeeklyConfirm  bool // reserved for R4; always false here
	Reason         []string
	Note           string
}

// ComputeMomentum classifies a stock's momentum direction. Pure: does not mutate
// inputs. rsi must be aligned 1:1 with candles; volRatio is today's volume ratio
// (used only for the limit-up "量縮≠轉弱" guard via detectLimitStatus).
func ComputeMomentum(candles []fetcher.Candle, rsi []float64, volRatio float64, cfg MomentumConfig) MomentumState {
	out := MomentumState{Flow: MomentumNeutral}
	n := len(candles)
	if n < cfg.MinHistoryDays || len(rsi) != n {
		return out
	}
	prices := make([]float64, n)
	vols := make([]int64, n)
	for i, c := range candles {
		prices[i] = fetcher.PriceForCalc(c, cfg.UseAdjustedClose)
		vols[i] = c.Volume
	}

	ma5 := indicator.SMA(prices, 5)
	ma10 := indicator.SMA(prices, 10)
	ma20 := indicator.SMA(prices, 20)
	keyMA := indicator.SMA(prices, cfg.KeyMA)

	cur := prices[n-1]
	accel := slopeAccel(prices, cfg.AccelShortWindow, cfg.AccelLongWindow)
	ret1 := pctChange(prices, 2)
	ret5 := pctChange(prices, 6)
	ret20 := pctChange(prices, 21)
	_ = ret1

	aboveKey := keyMA[n-1] > 0 && cur > keyMA[n-1]
	wasAboveKey := crossedKey(prices, keyMA, cfg.ReclaimLookback, false) // any recent bar above key
	loseMA := !aboveKey && wasAboveKey
	// R5-1: a genuine structural shift-up needs a sustained dip below the key MA and
	// a confirmed (multi-day) reclaim — not a single-bar wiggle back above it.
	belowDays := belowKeyDays(prices, keyMA, cfg.ReclaimLookback)
	aboveStreak := consecutiveAboveKey(prices, keyMA)

	bullAlign := ma5[n-1] > 0 && ma5[n-1] > ma10[n-1] && ma10[n-1] > ma20[n-1]
	aboveMA20 := ma20[n-1] > 0 && cur > ma20[n-1]
	extFrom5 := 0.0
	if ma5[n-1] > 0 {
		extFrom5 = (cur/ma5[n-1] - 1) * 100
	}

	// Swing structure + bearish divergence over the recent window.
	lo := n - 1 - mfStructWindow
	if lo < 0 {
		lo = 0
	}
	pivots := zigzagPivots(prices, lo, n-1, cfg.ZigzagReversal)
	out.StructureTrend = detectStructureTrend(pivots, prices)
	out.Divergence = detectBearishDivergence(pivots, prices, rsi, vols)

	out.SlopeAccel = accel

	// Limit-up lock guard: 量縮 ≠ 轉弱 → never call it FADING.
	limitStatus, _ := detectLimitStatus(candles, volRatio)
	locked := limitStatus == LimitLockedLowVol

	rsiUp := rsi[n-1] > rsi[n-1-cfg.AccelShortWindow]
	rsiMidLow := rsi[n-1] >= mfRSIMidLow && rsi[n-1] <= mfRSIMidHigh
	volBias := volUpBias(prices, vols, mfVolBiasBars)

	// Classification priority (R5-1): SHIFT_DOWN → FADING → CONTINUATION → SHIFT_UP →
	// BUILDING → NEUTRAL. CONTINUATION precedes SHIFT_UP so a steady ongoing uptrend is
	// not mislabelled a structural turn; SHIFT_UP is now a strict structural-shift test.
	shiftDown := (loseMA && ret5 < 0) || (out.StructureTrend == structLLLH && !aboveKey)
	fading := !locked && (accel < cfg.AccelNegThresh || out.Divergence) && aboveMA20 && ret20 > 0
	continuation := bullAlign && ret20 > 0 && abs(accel) <= cfg.AccelPosThresh && !out.Divergence && cur > ma10[n-1]
	shiftUp := aboveKey &&
		aboveStreak >= cfg.ShiftUpConfirmDays &&
		belowDays >= cfg.ShiftUpMinBelowDays &&
		ret5 > 0 &&
		(out.StructureTrend == structHigherLows || out.StructureTrend == structHHHL)
	building := accel > cfg.AccelPosThresh && rsiUp && rsiMidLow && volBias && extFrom5 <= mfExtFrom5Pct

	switch {
	case shiftDown:
		out.Flow = StructuralShiftDown
	case fading:
		out.Flow = MomentumFading
	case continuation:
		out.Flow = MomentumContinuation
	case shiftUp:
		out.Flow = StructuralShiftUp
	case building:
		out.Flow = MomentumBuilding
	default:
		out.Flow = MomentumNeutral
	}

	out.Computed = true
	out.Score = momentumScore(accel, out.StructureTrend, out.Divergence, volBias, cfg)
	out.Note = momentumNote(out.Flow)
	out.Reason = momentumReasons(out)
	return out
}

// slopeAccel = short-window per-day pace − long-window per-day pace (>0 accelerating).
func slopeAccel(prices []float64, sWin, lWin int) float64 {
	n := len(prices)
	if n <= lWin || prices[n-1-sWin] <= 0 || prices[n-1-lWin] <= 0 {
		return 0
	}
	short := (prices[n-1]/prices[n-1-sWin] - 1) / float64(sWin)
	long := (prices[n-1]/prices[n-1-lWin] - 1) / float64(lWin)
	return short - long
}

// crossedKey reports whether any of the last `lookback` bars (excluding today)
// was below (want=true) / above (want=false) the key MA.
func crossedKey(prices, keyMA []float64, lookback int, wantBelow bool) bool {
	n := len(prices)
	start := n - 1 - lookback
	if start < 0 {
		start = 0
	}
	for i := start; i < n-1; i++ {
		if keyMA[i] <= 0 {
			continue
		}
		if wantBelow && prices[i] < keyMA[i] {
			return true
		}
		if !wantBelow && prices[i] > keyMA[i] {
			return true
		}
	}
	return false
}

// belowKeyDays counts how many of the last `lookback` bars (excluding today) closed
// below the key MA — a measure of how sustained the dip was (R5-1).
func belowKeyDays(prices, keyMA []float64, lookback int) int {
	n := len(prices)
	start := n - 1 - lookback
	if start < 0 {
		start = 0
	}
	c := 0
	for i := start; i < n-1; i++ {
		if keyMA[i] > 0 && prices[i] < keyMA[i] {
			c++
		}
	}
	return c
}

// consecutiveAboveKey counts consecutive bars ending today that closed above the key
// MA — used to confirm a reclaim is multi-day, not a single-bar wiggle (R5-1).
func consecutiveAboveKey(prices, keyMA []float64) int {
	c := 0
	for i := len(prices) - 1; i >= 0; i-- {
		if keyMA[i] > 0 && prices[i] > keyMA[i] {
			c++
		} else {
			break
		}
	}
	return c
}

// detectStructureTrend classifies higher/lower highs & lows from the swing pivots.
func detectStructureTrend(pivots []pivot, prices []float64) string {
	var highs, lows []float64
	for _, p := range pivots {
		if p.isHigh {
			highs = append(highs, p.price)
		} else {
			lows = append(lows, p.price)
		}
	}
	var hh, hl, lh, ll bool
	if len(highs) >= 2 {
		hh = highs[len(highs)-1] > highs[len(highs)-2]
		lh = highs[len(highs)-1] < highs[len(highs)-2]
	}
	if len(lows) >= 2 {
		hl = lows[len(lows)-1] > lows[len(lows)-2]
		ll = lows[len(lows)-1] < lows[len(lows)-2]
	}
	switch {
	case hh && hl:
		return structHHHL
	case lh && ll:
		return structLLLH
	case hl:
		return structHigherLows
	case lh:
		return structLowerHighs
	case len(highs) < 2 && len(lows) < 2:
		return structNeutral
	default:
		return structMixed
	}
}

// detectBearishDivergence: last swing high makes a higher/equal price high but
// momentum (RSI) or volume is lower than the prior swing high.
func detectBearishDivergence(pivots []pivot, prices, rsi []float64, vols []int64) bool {
	var highs []pivot
	for _, p := range pivots {
		if p.isHigh {
			highs = append(highs, p)
		}
	}
	if len(highs) < 2 {
		return false
	}
	h1 := highs[len(highs)-2]
	h2 := highs[len(highs)-1]
	if h2.price < h1.price {
		return false // not a higher high → no bearish divergence here
	}
	rsiLower := rsi[h2.idx] < rsi[h1.idx]
	volLower := vols[h2.idx] < vols[h1.idx]
	return rsiLower || volLower
}

// volUpBias reports whether up-day volume exceeds down-day volume over the last
// `bars` sessions (a simple buying-pressure proxy).
func volUpBias(prices []float64, vols []int64, bars int) bool {
	n := len(prices)
	start := n - bars
	if start < 1 {
		start = 1
	}
	var up, down float64
	for i := start; i < n; i++ {
		if prices[i] > prices[i-1] {
			up += float64(vols[i])
		} else if prices[i] < prices[i-1] {
			down += float64(vols[i])
		}
	}
	return up > down
}

// momentumScore is a 0–100 bullish-momentum conviction (display / future C6 scaling).
func momentumScore(accel float64, structure string, divergence, volBias bool, cfg MomentumConfig) float64 {
	s := 50.0
	s += clampFloat(accel*cfg.AccelScale, -25, 25)
	if volBias {
		s += 8
	} else {
		s -= 4
	}
	switch structure {
	case structHHHL:
		s += 15
	case structHigherLows:
		s += 8
	case structLowerHighs:
		s -= 8
	case structLLLH:
		s -= 15
	}
	if divergence {
		s -= 12
	}
	return clampFloat(s, 0, 100)
}

func momentumNote(f MomentumFlow) string {
	switch f {
	case MomentumBuilding:
		return "動能正在累積"
	case MomentumContinuation:
		return "動能延續，多頭結構維持"
	case MomentumFading:
		return "動能轉弱，高檔鈍化 / 背離"
	case StructuralShiftUp:
		return "結構翻多，站回關鍵均線"
	case StructuralShiftDown:
		return "結構轉空，跌破關鍵支撐"
	default:
		return "無明確動能方向"
	}
}

func momentumReasons(s MomentumState) []string {
	var r []string
	r = append(r, s.Note)
	if s.Divergence {
		r = append(r, "出現價量 / 價-RSI 空頭背離")
	}
	if s.StructureTrend != structNeutral && s.StructureTrend != structMixed {
		r = append(r, "高低點結構："+s.StructureTrend)
	}
	return r
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
