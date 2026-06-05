package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
	"github.com/deep-huang/stock-scanner/internal/timeframe"
)

// ──────────────────────────────────────────────────────────────────────────────
// Multi-Timeframe shadow signal (R4-2)
//
// Daily + Weekly views built purely from daily OHLCV (weekly via timeframe.ToWeekly —
// no intraday / tick / level-2). SHADOW-ONLY: computed and attached to ShadowSignals,
// never read by any score / action / probability / sort / report. Influencing scoring
// is R4-3 and must sit behind enable_signal_guardrail_scoring.
//
// Data insufficiency always yields UNKNOWN (never bearish). A partial (unfinished)
// weekly bar can never produce a STRONG signal.
// ──────────────────────────────────────────────────────────────────────────────

// MTF period knobs — first-pass reasonable defaults.
// TODO(R5/R6): calibrate MTF periods after real-data distribution check.
const (
	mtfDailyShortMA = 10
	mtfDailyMidMA   = 20
	mtfDailyLongMA  = 60

	mtfWeeklyShortMA = 5
	mtfWeeklyMidMA   = 10
	mtfWeeklyLongMA  = 20

	mtfRSIPeriod   = 14
	mtfMA200Period = 200

	mtfSlopeLookback   = 5 // bars used to measure the long-MA slope
	mtfMomoShortWindow = 5 // bars used for the momentum return-slope

	defaultMTFStrongScore = 85 // R4-2b: STRONG needs both timeframes at/above this
)

// MTF momentum-state labels (this timeframe's own momentum; NOT the C5 MomentumFlow).
const (
	mtfMomoAccelerating = "ACCELERATING"
	mtfMomoSteady       = "STEADY"
	mtfMomoDecelerating = "DECELERATING"
	mtfMomoNegative     = "NEGATIVE"
	mtfMomoUnknown      = "UNKNOWN"
)

// MTFConfig is the resolved multi-timeframe config.
type MTFConfig struct {
	Enable           bool
	UseAdjustedClose bool
	// R4-2b: STRONG SignalStrength score thresholds (per timeframe).
	StrongDailyScore  float64
	StrongWeeklyScore float64
}

func mtfConfigFrom(cfg Config) MTFConfig {
	mc := MTFConfig{
		Enable:            cfg.EnableMultiTimeframe,
		UseAdjustedClose:  cfg.UseAdjustedClose || cfg.MTFUseAdjustedClose,
		StrongDailyScore:  cfg.MTFStrongDailyScoreThreshold,
		StrongWeeklyScore: cfg.MTFStrongWeeklyScoreThreshold,
	}
	if mc.StrongDailyScore <= 0 {
		mc.StrongDailyScore = defaultMTFStrongScore
	}
	if mc.StrongWeeklyScore <= 0 {
		mc.StrongWeeklyScore = defaultMTFStrongScore
	}
	return mc
}

// TimeframeView is one timeframe's trend + momentum read.
type TimeframeView struct {
	Timeframe     string  // "DAILY" | "WEEKLY"
	Valid         bool    // enough data to judge
	Partial       bool    // weekly: last bar unfinished (daily: always false)
	TrendScore    float64 // 0–100
	TrendState    string  // UPTREND | RANGE | DOWNTREND | UNKNOWN
	MomentumScore float64 // 0–100
	MomentumState string  // ACCELERATING | STEADY | DECELERATING | NEGATIVE | UNKNOWN
}

// MultiTimeframe is the combined Daily+Weekly read (shadow-only in R4-2).
type MultiTimeframe struct {
	Daily          TimeframeView
	Weekly         TimeframeView
	AlignmentScore float64 // 0–100
	AlignmentLabel string  // FULL_BULL | DAILY_LEADS | CONFLICT | FULL_BEAR | UNKNOWN
	SignalStrength string  // STRONG | MODERATE | WEAK | CONFLICTED | UNKNOWN
	LongTermFilter string  // BULLISH | BEARISH | UNKNOWN
}

// ComputeMultiTimeframe builds the Daily/Weekly views, alignment, signal strength and
// 200-day long-term filter. Pure: does not mutate inputs.
func ComputeMultiTimeframe(candles []fetcher.Candle, cfg MTFConfig) MultiTimeframe {
	out := MultiTimeframe{AlignmentLabel: "UNKNOWN", SignalStrength: "UNKNOWN", LongTermFilter: "UNKNOWN"}

	dPrices := mtfPrices(candles, cfg.UseAdjustedClose)
	out.Daily = computeTimeframeView("DAILY", dPrices, mtfDailyShortMA, mtfDailyMidMA, mtfDailyLongMA, false)

	wbars := timeframe.ToWeekly(candles)
	wPrices := mtfPrices(timeframe.WeeklyCandles(wbars), cfg.UseAdjustedClose)
	wPartial := len(wbars) > 0 && wbars[len(wbars)-1].Partial
	out.Weekly = computeTimeframeView("WEEKLY", wPrices, mtfWeeklyShortMA, mtfWeeklyMidMA, mtfWeeklyLongMA, wPartial)

	out.LongTermFilter = longTermFilter(dPrices)
	out.AlignmentScore, out.AlignmentLabel = mtfAlignment(out.Daily, out.Weekly)
	out.SignalStrength = mtfSignalStrength(out.Daily, out.Weekly, out.AlignmentLabel, cfg)
	return out
}

func mtfPrices(cs []fetcher.Candle, adj bool) []float64 {
	out := make([]float64, len(cs))
	for i, c := range cs {
		out[i] = fetcher.PriceForCalc(c, adj)
	}
	return out
}

// computeTimeframeView scores trend + momentum for one timeframe. Insufficient data →
// Valid=false with UNKNOWN states (never bearish).
func computeTimeframeView(label string, prices []float64, short, mid, long int, partial bool) TimeframeView {
	v := TimeframeView{Timeframe: label, Partial: partial, TrendState: "UNKNOWN", MomentumState: "UNKNOWN"}
	n := len(prices)
	if n < long+mtfSlopeLookback+1 || n < mtfRSIPeriod+1 || n < mtfMomoShortWindow+1 {
		return v // Valid=false, UNKNOWN
	}

	ms := indicator.SMA(prices, short)
	mm := indicator.SMA(prices, mid)
	ml := indicator.SMA(prices, long)
	price := prices[n-1]

	score := 0.0
	if price > ms[n-1] {
		score += 25
	}
	if ms[n-1] > mm[n-1] {
		score += 25
	}
	if mm[n-1] > ml[n-1] {
		score += 25
	}
	if ml[n-1] > ml[n-1-mtfSlopeLookback] {
		score += 25
	}
	v.TrendScore = score
	switch {
	case score >= 70:
		v.TrendState = "UPTREND"
	case score >= 30:
		v.TrendState = "RANGE"
	default:
		v.TrendState = "DOWNTREND"
	}

	rsi := indicator.RSI(prices, mtfRSIPeriod)
	past := prices[n-1-mtfMomoShortWindow]
	retPct := 0.0
	if past > 0 {
		retPct = (price/past - 1) * 100
	}
	slopeScaled := clampFloat(50+retPct*5, 0, 100)
	mscore := clampFloat(0.6*rsi[n-1]+0.4*slopeScaled, 0, 100)
	v.MomentumScore = mscore
	switch {
	case mscore >= 60:
		v.MomentumState = mtfMomoAccelerating
	case mscore >= 45:
		v.MomentumState = mtfMomoSteady
	case mscore >= 30:
		v.MomentumState = mtfMomoDecelerating
	default:
		v.MomentumState = mtfMomoNegative
	}
	v.Valid = true
	return v
}

// longTermFilter uses the 200-day MA as a coarse big-trend read. <200 bars → UNKNOWN
// (never bearish on insufficient data).
func longTermFilter(prices []float64) string {
	n := len(prices)
	if n < mtfMA200Period {
		return "UNKNOWN"
	}
	ma := indicator.SMA(prices, mtfMA200Period)
	if ma[n-1] <= 0 {
		return "UNKNOWN"
	}
	if prices[n-1] >= ma[n-1] {
		return "BULLISH"
	}
	return "BEARISH"
}

// mtfAlignment combines the two views. Any invalid view → UNKNOWN. The label set is
// limited (FULL_BULL/DAILY_LEADS/CONFLICT/FULL_BEAR); RANGE-mixed cases bucket into
// DAILY_LEADS as a coarse "transitional" read (refine in R5).
func mtfAlignment(d, w TimeframeView) (float64, string) {
	if !d.Valid || !w.Valid {
		return 0, "UNKNOWN"
	}
	score := 0.5*d.TrendScore + 0.5*w.TrendScore
	du, dd := d.TrendState == "UPTREND", d.TrendState == "DOWNTREND"
	wu, wd := w.TrendState == "UPTREND", w.TrendState == "DOWNTREND"
	switch {
	case du && wu:
		return score, "FULL_BULL"
	case dd && wd:
		return score, "FULL_BEAR"
	case (du && wd) || (dd && wu):
		return score, "CONFLICT"
	default:
		return score, "DAILY_LEADS"
	}
}

// mtfSignalStrength is shadow-only (never used for scoring/sort/action/prob).
//
// R4-2b calibration: STRONG is now reserved for a genuinely strong, fully-aligned
// setup — FULL_BULL with BOTH timeframes at/above the strong score threshold, BOTH
// timeframes' momentum ACCELERATING, and the weekly NOT partial. A merely-steady or
// score-75 FULL_BULL is MODERATE. CONFLICT stays CONFLICTED (never bearish).
func mtfSignalStrength(d, w TimeframeView, label string, cfg MTFConfig) string {
	if !d.Valid || !w.Valid || label == "UNKNOWN" {
		return "UNKNOWN"
	}
	switch label {
	case "CONFLICT":
		return "CONFLICTED"
	case "FULL_BULL":
		if d.TrendScore >= cfg.StrongDailyScore &&
			w.TrendScore >= cfg.StrongWeeklyScore &&
			d.MomentumState == mtfMomoAccelerating &&
			w.MomentumState == mtfMomoAccelerating &&
			!w.Partial {
			return "STRONG"
		}
		return "MODERATE"
	case "DAILY_LEADS":
		return "MODERATE"
	default: // FULL_BEAR / weak
		return "WEAK"
	}
}
