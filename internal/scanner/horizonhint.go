package scanner

import (
	"fmt"
	"math"
	"strconv"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// ──────────────────────────────────────────────────────────────────────────────
// HoldingHorizonHint (R6-7) — Report Holding Horizon Hint，display-only。
//
// 依目前型態 + R6 回測結果，在 report 顯示「主要觀察週期 = 20 個交易日」。20d 對應近期
// 台股強勢股節奏：強勢 → 處置(~10d) → 冷卻/下跌 → 出處置 → 再攻擊 ≈ 20 交易日。
// 5d/10d = early reaction；60d = optional reference。可信度由 R6-6 近期強多頭驗證支撐。
//
// 這是「觀察週期」，不是交易指令。它是 scanner 既有 shadow / 指標的純函式衍生：
//   不參與 RocketScore / WatchAction / ExplosionProb / 排序 / 停損 / backtest stop。
//   不下單、不接 broker、不替使用者交易。
//
// 與 R7-1 HoldingHorizon（stage+ATR shadow，holdinghorizon.go）各自獨立、互不影響。
// 詳見 docs/SPEC_R6_7_REPORT_HOLDING_HORIZON_HINT.md。
// ──────────────────────────────────────────────────────────────────────────────

// HoldingHorizonHint is the structured, display-oriented R6-7 output.
type HoldingHorizonHint struct {
	Computed      bool     `json:"computed"`       // false = 資料不足（其餘欄位為零值/預設）
	PrimaryDays   int      `json:"primary_days"`   // 20
	EarlyDays     []int    `json:"early_days"`     // [5,10]
	ReferenceDays []int    `json:"reference_days"` // [60]
	MatchedSetup  string   `json:"matched_setup"`  // C_VCP_MA20_RETEST / B_PULLBACK_* / A_MA20_PULLBACK / A_MA60_PULLBACK / DEFAULT
	Confidence    string   `json:"confidence"`     // LOW / MEDIUM
	Reason        []string `json:"reason,omitempty"`
	Caveat        []string `json:"caveat,omitempty"`
}

// Horizon constants (v1 fixed; primary horizon tied to R6-6 recent-bull 20d).
const (
	hintPrimaryDays        = 20
	hintRecentHighLookback = 20 // 近期高點回看根數（判斷「近期回檔」深度）
)

// hintEarlyDays / hintReferenceDays are the displayed early-reaction / reference windows.
var (
	hintEarlyDays     = []int{5, 10}
	hintReferenceDays = []int{60}
)

// Matcher thresholds — v1 fixed module constants（待 R6-7 校準），display-only。
const (
	hintVCPQualityMin     = 70.0 // C: VCP.QualityScore 門檻
	hintRSRankMin         = 70.0 // C/B: RS percentile 門檻
	hintNewHighScoreHigh  = 60.0 // B: 「NewHighScore 高」門檻
	hint52wHighMaxDropPct = 25.0 // B: 距 52 週高 <= 此跌幅（DistanceFrom52wHighPct >= -此值）
	hintPullbackMinPct    = 5.0  // B: 近期回檔最小深度
	hintPullbackMaxPct    = 20.0 // B: 近期回檔最大深度
	hintNearMAPct         = 3.0  // A: 收盤距 MA 多少 % 內視為「接近」
)

// hintPullbackBuckets mirrors the R6 Setup B pullback-trigger depths.
var hintPullbackBuckets = []int{5, 8, 10, 15, 20}

// hintBaseCaveats are shown on every matched hint (the ⑧ 注意 block red lines).
var hintBaseCaveats = []string{
	"這是觀察週期，不是交易指令",
	"baseline stop 對此類型可能過嚴",
	"ATR_3 / PCT_15 仍是候選，不是正式預設",
}

// computeHorizonHint derives the display-only horizon hint from existing shadow
// signals + daily indicators. It mutates nothing and never reads forward bars.
// When the relevant shadow flags are off (shadow fields nil) the setup matchers
// degrade safely to DEFAULT, which still shows the 20d primary horizon.
func computeHorizonHint(a StockAnalysis, shadow *ShadowSignals, candles []fetcher.Candle) HoldingHorizonHint {
	h := HoldingHorizonHint{
		PrimaryDays:   hintPrimaryDays,
		EarlyDays:     hintEarlyDays,
		ReferenceDays: hintReferenceDays,
	}

	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}
	if len(closes) < 20 || a.Close <= 0 {
		return h // Computed stays false → caller may keep it; report skips it.
	}
	h.Computed = true

	// Priority: C → B → A → Default.
	switch {
	case hintMatchC(shadow):
		h.MatchedSetup = "C_VCP_MA20_RETEST"
		h.Confidence = "MEDIUM"
		h.Reason = []string{
			"目前具備 VCP valid / 高 RS / 接近 MA20 支撐",
			"R6 回測中 VCP_MA20 retest 樣本足夠",
			"20d 對應近期處置 / 冷卻 / 出處置 / 再攻擊循環",
		}

	case hintMatchBSetup(shadow, closes) != "":
		setup := hintMatchBSetup(shadow, closes)
		depth := hintPullbackDepthPct(closes)
		h.MatchedSetup = setup
		h.Confidence = "MEDIUM"
		h.Reason = []string{
			fmt.Sprintf("距 52 週高 %.1f%%、近期自高點回檔約 %.0f%%", shadow.NewHigh.DistanceFrom52wHighPct, depth),
			"高 RS、接近 52 週高後的回檔（NewHighScore 偏高）",
			"20d 對應近期處置 / 冷卻 / 出處置 / 再攻擊循環",
		}

	default:
		if setup, conf, ok := hintMatchA(a, closes); ok {
			h.MatchedSetup = setup
			h.Confidence = conf
			if setup == "A_MA60_PULLBACK" {
				h.Reason = []string{
					"接近 MA60 支撐（拉回）",
					"R6 顯示 A_MA20 優於 A_MA60",
				}
				h.Caveat = append(h.Caveat, "MA60 通常較慢，需確認是否仍是強勢股而非轉弱")
			} else {
				h.Reason = []string{
					"接近 MA20 支撐（拉回）",
					"R6 顯示 A_MA20 優於 A_MA60",
					"20d 對應近期處置 / 冷卻 / 出處置 / 再攻擊循環",
				}
			}
		} else {
			h.MatchedSetup = "DEFAULT"
			h.Confidence = "LOW"
			h.Reason = []string{
				"未明確匹配 setup，僅顯示主要觀察週期",
				"20d 對應近期處置 / 冷卻 / 出處置 / 再攻擊循環",
			}
		}
	}

	h.Caveat = append(h.Caveat, hintBaseCaveats...)
	return h
}

// hintMatchC reports whether the C (VCP retest) setup matches. Requires VCP + RS +
// Momentum shadow signals; returns false (degrade) when any is nil/off.
func hintMatchC(s *ShadowSignals) bool {
	if s == nil || s.VCP == nil || s.RS == nil || s.Momentum == nil {
		return false
	}
	return s.VCP.Computed && s.VCP.Valid && s.VCP.QualityScore >= hintVCPQualityMin &&
		s.RS.RSRankPercentile >= hintRSRankMin &&
		s.Momentum.Flow != StructuralShiftDown
}

// hintMatchBSetup returns the matched B_PULLBACK_* setup name, or "" when the B
// (52-week-high pullback) setup does not match. Requires NewHigh + RS shadows.
func hintMatchBSetup(s *ShadowSignals, closes []float64) string {
	if s == nil || s.NewHigh == nil || s.RS == nil {
		return ""
	}
	nh := s.NewHigh
	if !nh.Computed ||
		nh.DistanceFrom52wHighPct < -hint52wHighMaxDropPct ||
		nh.NewHighScore < hintNewHighScoreHigh ||
		s.RS.RSRankPercentile < hintRSRankMin {
		return ""
	}
	depth := hintPullbackDepthPct(closes)
	if depth < hintPullbackMinPct || depth > hintPullbackMaxPct {
		return ""
	}
	return "B_PULLBACK_" + strconv.Itoa(hintNearestPullbackBucket(depth))
}

// hintMatchA reports an A (MA20/MA60 pullback) match. MA20 takes priority (MEDIUM);
// MA60 is the slower fallback (LOW). MA60 needs >= 60 bars. Only daily inputs.
func hintMatchA(a StockAnalysis, closes []float64) (setup, confidence string, ok bool) {
	price := a.Close
	if price <= 0 {
		return "", "", false
	}
	if a.MA20 > 0 && hintNearPct(price, a.MA20) <= hintNearMAPct {
		return "A_MA20_PULLBACK", "MEDIUM", true
	}
	if len(closes) >= 60 {
		ma60 := indicator.SMA(closes, 60)
		if m := ma60[len(ma60)-1]; m > 0 && hintNearPct(price, m) <= hintNearMAPct {
			return "A_MA60_PULLBACK", "LOW", true
		}
	}
	return "", "", false
}

// hintPullbackDepthPct returns the % drawdown of the latest close from the highest
// close over the recent lookback window (>= 0). 0 when not pulled back / no data.
func hintPullbackDepthPct(closes []float64) float64 {
	n := len(closes)
	if n == 0 {
		return 0
	}
	start := n - hintRecentHighLookback
	if start < 0 {
		start = 0
	}
	hi := closes[start]
	for _, c := range closes[start+1:] {
		if c > hi {
			hi = c
		}
	}
	last := closes[n-1]
	if hi <= 0 || last >= hi {
		return 0
	}
	return (hi - last) / hi * 100
}

// hintNearestPullbackBucket maps an observed pullback depth to the nearest R6
// Setup B trigger bucket (5/8/10/15/20).
func hintNearestPullbackBucket(depth float64) int {
	best := hintPullbackBuckets[0]
	bestDiff := math.Abs(depth - float64(best))
	for _, b := range hintPullbackBuckets[1:] {
		if d := math.Abs(depth - float64(b)); d < bestDiff {
			best, bestDiff = b, d
		}
	}
	return best
}

// hintNearPct returns |price/ma − 1| × 100.
func hintNearPct(price, ma float64) float64 {
	if ma == 0 {
		return math.Inf(1)
	}
	return math.Abs(price/ma-1) * 100
}
