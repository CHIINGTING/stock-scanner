package scanner

import (
	"math"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────
// New High / 52-week high (C3)
//
// 多週期新高（20/60/120/250 日）+ 距 52 週高距離 + 領導力 / 突破觀察 + NewHighScore。
// C3 只建立「資料模型 + helper + config + 測試」，不接入既有 scoring / report /
// watchlist / rotation。所有函式皆為 pure，只在被明確呼叫時運算；EnableNewHigh=false
// 時 pipeline 不呼叫它們（golden regression by construction）。後續 C6 才統一接入。
//
// 設計決策（沿用 SPEC_R2）：新高一律以「還原收盤」比價（close vs prior close），不用盤中
// High，規避 H/L 未還原問題、對波段更保守。價格走 fetcher.PriceForCalc。
// ──────────────────────────────────────────────────────────────────────────────

// Defaults applied when config leaves a knob zero.
var defaultNHLookbacks = [4]int{20, 60, 120, 250}

const (
	defaultNHMinHistoryDays  = 60
	defaultNHLeaderWithinPct = 25
	defaultNHNear52wHighPct  = 15
	defaultNHBreakoutWatch   = 5
	defaultNHLeaderStrongPct = 10
	defaultNHLeaderFarPct    = 50
	defaultNHVolConfirmRatio = 1.5
	defaultNHOverextRSI      = 75
)

// NewHighConfig is the resolved new-high configuration (defaults applied).
// Lookbacks is fixed to 4 buckets mapped to the H20/H60/H120/H250 result fields
// (the labels are conventional; actual windows come from config).
type NewHighConfig struct {
	Enable bool
	Lookbacks      [4]int
	MinHistoryDays int

	// Distance-to-52w-high bands (each compared against the |distance| magnitude).
	LeaderWithinPct float64 // → NewHighLeadershipEligible
	Near52wHighPct  float64 // → Near52wHigh
	BreakoutWatchPct float64 // → BreakoutWatch

	// Score-only knobs (do NOT define the boolean bands above).
	LeaderStrongPct float64 // NewHighScore top tier
	LeaderFarPct    float64 // NewHighScore cap when beyond

	VolConfirmRatio  float64
	OverextRSI       float64
	UseAdjustedClose bool
}

// newHighConfigFrom resolves a NewHighConfig from the scanner Config, applying
// defaults. Only the first 4 entries of nh_lookbacks are used (fixed buckets);
// missing entries fall back to the defaults.
func newHighConfigFrom(cfg Config) NewHighConfig {
	nc := NewHighConfig{
		Enable:           cfg.EnableNewHigh,
		Lookbacks:        defaultNHLookbacks,
		MinHistoryDays:   cfg.NHMinHistoryDays,
		LeaderWithinPct:  cfg.NHLeaderWithinPct,
		Near52wHighPct:   cfg.NHNear52wHighPct,
		BreakoutWatchPct: cfg.NHBreakoutWatchPct,
		LeaderStrongPct:  cfg.NHLeaderStrongPct,
		LeaderFarPct:     cfg.NHLeaderFarPct,
		VolConfirmRatio:  cfg.NHVolConfirmRatio,
		OverextRSI:       cfg.NHOverextRSI,
		UseAdjustedClose: cfg.UseAdjustedClose || cfg.NHUseAdjustedClose,
	}
	for i := 0; i < 4 && i < len(cfg.NHLookbacks); i++ {
		if cfg.NHLookbacks[i] > 0 {
			nc.Lookbacks[i] = cfg.NHLookbacks[i]
		}
	}
	if nc.MinHistoryDays <= 0 {
		nc.MinHistoryDays = defaultNHMinHistoryDays
	}
	if nc.LeaderWithinPct <= 0 {
		nc.LeaderWithinPct = defaultNHLeaderWithinPct
	}
	if nc.Near52wHighPct <= 0 {
		nc.Near52wHighPct = defaultNHNear52wHighPct
	}
	if nc.BreakoutWatchPct <= 0 {
		nc.BreakoutWatchPct = defaultNHBreakoutWatch
	}
	if nc.LeaderStrongPct <= 0 {
		nc.LeaderStrongPct = defaultNHLeaderStrongPct
	}
	if nc.LeaderFarPct <= 0 {
		nc.LeaderFarPct = defaultNHLeaderFarPct
	}
	if nc.VolConfirmRatio <= 0 {
		nc.VolConfirmRatio = defaultNHVolConfirmRatio
	}
	if nc.OverextRSI <= 0 {
		nc.OverextRSI = defaultNHOverextRSI
	}
	return nc
}

// NewHighInput is one candidate for new-high analysis.
type NewHighInput struct {
	Symbol  string
	Name    string
	Candles []fetcher.Candle
}

// NewHighResult holds the multi-period new-high analysis for one stock.
type NewHighResult struct {
	Computed bool // history sufficient + valid prices

	// Multi-period new high (close >= prior-window high, window excludes today).
	H20, H60, H120, H250         bool
	H20Valid, H60Valid           bool
	H120Valid, H250Valid         bool

	High52w                   float64 // highest adjusted close over the 52-week window
	DistanceFrom52wHighPct    float64 // (close/High52w - 1)*100, <= 0
	Near52wHigh               bool    // distance within NearHighPct of the high
	NewHighLeadershipEligible bool    // distance within LeaderWithinPct
	BreakoutWatch             bool    // near/above 60-day high with volume confirmation

	NewHighScore float64 // 0–100
}

// computeNewHigh derives the multi-period new-high state for one stock.
// volRatio / rsi are supplied by the caller (already available in the scanner
// context) and feed BreakoutWatch and NewHighScore; this avoids duplicating
// indicator logic here. Pure: does not mutate inputs.
func computeNewHigh(candles []fetcher.Candle, volRatio, rsi float64, cfg NewHighConfig) NewHighResult {
	var r NewHighResult
	n := len(candles)
	if n < cfg.MinHistoryDays {
		return r // Computed stays false
	}
	adj := make([]float64, n)
	for i, c := range candles {
		adj[i] = fetcher.PriceForCalc(c, cfg.UseAdjustedClose)
	}
	cur := adj[n-1]
	if cur <= 0 {
		return r
	}

	r.H20, r.H20Valid = isNewHigh(adj, cfg.Lookbacks[0])
	r.H60, r.H60Valid = isNewHigh(adj, cfg.Lookbacks[1])
	r.H120, r.H120Valid = isNewHigh(adj, cfg.Lookbacks[2])
	r.H250, r.H250Valid = isNewHigh(adj, cfg.Lookbacks[3])

	// 52-week window = the longest (4th) lookback; include today.
	// Three nested distance-to-52w-high bands (pct is <= 0; compare magnitude):
	//   leadership ⊇ near ⊇ breakout  (25% ⊇ 15% ⊇ 5% by default).
	pct, hi := distanceFrom52wHigh(adj, cfg.Lookbacks[3])
	r.High52w = hi
	r.DistanceFrom52wHighPct = pct
	r.NewHighLeadershipEligible = pct >= -cfg.LeaderWithinPct
	r.Near52wHigh = pct >= -cfg.Near52wHighPct
	r.BreakoutWatch = pct >= -cfg.BreakoutWatchPct

	r.Computed = true
	r.NewHighScore = newHighScore(r, volRatio, rsi, cfg)
	return r
}

// priorHigh returns the max adjusted price over the `lookback` bars BEFORE today
// (excludes the latest bar). ok=false when there is not enough history.
func priorHigh(adj []float64, lookback int) (float64, bool) {
	n := len(adj)
	if lookback <= 0 || n-1 < lookback {
		return 0, false
	}
	hi := 0.0
	for i := n - 1 - lookback; i < n-1; i++ {
		if adj[i] > hi {
			hi = adj[i]
		}
	}
	return hi, true
}

// isNewHigh reports whether today's adjusted close is at/above the prior-window
// high (window excludes today). valid=false when history is insufficient.
func isNewHigh(adj []float64, lookback int) (isHigh, valid bool) {
	ph, ok := priorHigh(adj, lookback)
	if !ok {
		return false, false
	}
	return adj[len(adj)-1] >= ph && ph > 0, true
}

// distanceFrom52wHigh returns (pct, high) where high is the max adjusted price
// over the last `win` bars INCLUDING today, and pct = (cur/high - 1)*100 (<= 0).
func distanceFrom52wHigh(adj []float64, win int) (float64, float64) {
	n := len(adj)
	start := n - win
	if start < 0 {
		start = 0
	}
	hi := 0.0
	for i := start; i < n; i++ {
		if adj[i] > hi {
			hi = adj[i]
		}
	}
	if hi <= 0 {
		return 0, 0
	}
	return (adj[n-1]/hi - 1) * 100, hi
}

// newHighScore composes the 0–100 score (SPEC_R2 §3.4):
// 60-day new high is the backbone (+40, +10 if volume-confirmed); 20-day is the
// trigger (+8); 120-day is confirmation (+12); leadership tier by distance to the
// 52w high; non-leaders (beyond LeaderFarPct) are capped; making a 250-day high
// while overextended (RSI high) is dampened ×0.6.
func newHighScore(r NewHighResult, volRatio, rsi float64, cfg NewHighConfig) float64 {
	volConfirm := volRatio >= cfg.VolConfirmRatio
	s := 0.0
	if r.H60 {
		s += 40
		if volConfirm {
			s += 10
		}
	}
	if r.H20 {
		s += 8
	}
	if r.H120 {
		s += 12
	}
	// Leadership tier by distance to 52w high (pct is <= 0).
	switch {
	case r.DistanceFrom52wHighPct >= -cfg.LeaderStrongPct:
		s += 30
	case r.DistanceFrom52wHighPct >= -cfg.LeaderWithinPct:
		s += 18
	case r.DistanceFrom52wHighPct >= -cfg.LeaderFarPct:
		s += 6
	}
	// Non-leader cap: too far from the 52w high → not a leader, cap the score.
	if r.DistanceFrom52wHighPct < -cfg.LeaderFarPct {
		s = math.Min(s, 35)
	}
	// Overextension dampener: new 250-day high while RSI overbought →追高非起漲.
	if r.H250 && rsi >= cfg.OverextRSI {
		s = math.Round(s * 0.6)
	}
	return clampFloat(s, 0, 100)
}
