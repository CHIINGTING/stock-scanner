package scanner

import (
	"fmt"
	"log"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// Config holds scanner parameters.
type Config struct {
	MinPrice     float64 `yaml:"min_price"`
	MinAvgVolume float64 `yaml:"min_avg_volume"`
	TopN         int     `yaml:"top_n"`

	// UseAdjustedClose toggles split/dividend-adjusted close for adjusted-aware
	// calculations (RS / new high / VCP / backtest — added in later commits).
	// Default false: every calculation keeps using raw Close, preserving today's
	// output exactly. Read prices via fetcher.PriceForCalc(candle, UseAdjustedClose).
	UseAdjustedClose bool `yaml:"use_adjusted_close"`

	// EnableSignalGuardrailScoring is the C6b MASTER switch: only when true may the
	// shadow signals (RS / NewHigh / VCP / MomentumFlow) actually influence scoring.
	// Default false → even with all four signal flags on, behaviour stays C6a
	// shadow-only (compute+attach, no scoring change). Two-layer gating:
	//   signal flags     = whether the shadow is computed
	//   this master flag = whether the shadow may affect score / action / probability
	EnableSignalGuardrailScoring bool `yaml:"enable_signal_guardrail_scoring"`

	// ── RS Rank (C2) ─────────────────────────────────────────────────────────
	// EnableRSRank gates the whole RS feature. Default false → RS is never
	// computed in the pipeline and cannot affect existing scoring / report /
	// watchlist / rotation. The RS helpers in relstrength.go are pure and only
	// run when explicitly called. Wiring RS into rocket_candidate_score is C6.
	EnableRSRank                    bool    `yaml:"enable_rs_rank"`
	RSLookbackDays                  int     `yaml:"rs_lookback_days"`                    // default 120
	RSMinHistoryDays                int     `yaml:"rs_min_history_days"`                 // default 100
	RSUniverseExcludeNonCommonStock bool    `yaml:"rs_universe_exclude_non_common_stock"`// default true (set in yaml)
	RSUseAdjustedClose              bool    `yaml:"rs_use_adjusted_close"`               // OR'd with UseAdjustedClose
	RSLeadershipThreshold           float64 `yaml:"rs_leadership_threshold"`             // config-only in C2 (not wired)
	RSWatchThreshold                float64 `yaml:"rs_watch_threshold"`                  // config-only in C2 (not wired)

	// ── New High / 52-week high (C3) ─────────────────────────────────────────
	// EnableNewHigh gates the whole feature. Default false → never computed in the
	// pipeline; cannot affect existing scoring / report / watchlist / rotation.
	// newhigh.go helpers are pure and only run when explicitly called. Wiring into
	// rocket_candidate_score is C6.
	EnableNewHigh       bool    `yaml:"enable_new_high"`
	NHLookbacks         []int   `yaml:"nh_lookbacks"`           // default [20,60,120,250]
	NHMinHistoryDays    int     `yaml:"nh_min_history_days"`    // default 60
	NHLeaderWithinPct  float64 `yaml:"nh_leader_within_pct"`  // default 25 → leadership-eligible band
	NHNear52wHighPct   float64 `yaml:"nh_near_52w_high_pct"`  // default 15 → near_52w_high band
	NHBreakoutWatchPct float64 `yaml:"nh_breakout_watch_pct"` // default 5  → breakout_watch band
	NHLeaderStrongPct  float64 `yaml:"nh_leader_strong_pct"`  // default 10 → NewHighScore top tier only
	NHLeaderFarPct     float64 `yaml:"nh_leader_far_pct"`     // default 50 → NewHighScore cap only
	NHVolConfirmRatio  float64 `yaml:"nh_vol_confirm_ratio"`  // default 1.5 (60d new-high volume)
	NHOverextRSI       float64 `yaml:"nh_overext_rsi"`        // default 75 (overextension dampener)
	NHUseAdjustedClose bool    `yaml:"nh_use_adjusted_close"` // OR'd with UseAdjustedClose

	// ── VCP / Volatility Contraction Pattern (C4) ────────────────────────────
	// EnableVCP gates the whole feature. Default false → never computed in the
	// pipeline; cannot affect existing scoring / report / watchlist / rotation.
	// vcp.go helpers are pure and only run when explicitly called. Wiring VCP into
	// rocket_candidate_score is C6.
	EnableVCP             bool    `yaml:"enable_vcp"`
	VCPLookbackDays       int     `yaml:"vcp_lookback_days"`        // default 60
	VCPMinHistoryDays     int     `yaml:"vcp_min_history_days"`     // default 40
	VCPMinContractions    int     `yaml:"vcp_min_contractions"`     // default 2
	VCPMinQualityScore    float64 `yaml:"vcp_min_quality_score"`    // default 70
	VCPUseAdjustedClose   bool    `yaml:"vcp_use_adjusted_close"`   // OR'd with UseAdjustedClose
	VCPTightnessWeight    float64 `yaml:"vcp_tightness_weight"`     // default 30
	VCPVolumeDryUpWeight  float64 `yaml:"vcp_volume_dryup_weight"`  // default 25
	VCPMonotonicWeight    float64 `yaml:"vcp_monotonic_weight"`     // default 20
	VCPSupportHoldWeight  float64 `yaml:"vcp_support_hold_weight"`  // default 15
	VCPNearBreakoutWeight float64 `yaml:"vcp_near_breakout_weight"` // default 10
	VCPZigzagReversalPct  float64 `yaml:"vcp_zigzag_reversal_pct"`  // default 1.5 (swing reversal)

	// ── MomentumFlow (C5) ────────────────────────────────────────────────────
	// EnableMomentumFlow gates the whole feature. Default false → never computed
	// in the pipeline; cannot affect existing scoring / report / watchlist /
	// rotation. momentum.go helpers are pure and only run when explicitly called.
	// The RocketStage × MomentumFlow joint decision + RocketScore modifier are C6.
	EnableMomentumFlow  bool    `yaml:"enable_momentum_flow"`
	MFMinHistoryDays    int     `yaml:"mf_min_history_days"`    // default 30
	MFAccelShortWindow  int     `yaml:"mf_accel_short_window"`  // default 3
	MFAccelLongWindow   int     `yaml:"mf_accel_long_window"`   // default 20
	MFAccelPosThresh    float64 `yaml:"mf_accel_pos_thresh"`   // default 0.0008 (待校準)
	MFAccelNegThresh    float64 `yaml:"mf_accel_neg_thresh"`   // default -0.0008 (待校準)
	MFAccelScale        float64 `yaml:"mf_accel_scale"`        // default 12000 (待校準)
	MFKeyMA             int     `yaml:"mf_key_ma"`             // default 20
	MFReclaimLookback   int     `yaml:"mf_reclaim_lookback"`   // default 5
	MFZigzagReversalPct float64 `yaml:"mf_zigzag_reversal_pct"`// default 1.5
	MFRSIDivLookback    int     `yaml:"mf_rsi_div_lookback"`   // default 20
	MFUseAdjustedClose  bool    `yaml:"mf_use_adjusted_close"` // OR'd with UseAdjustedClose

	KDJ struct {
		KPeriod int `yaml:"k_period"`
		DSmooth int `yaml:"d_smooth"`
		JSmooth int `yaml:"j_smooth"`
	} `yaml:"kdj"`

	Bollinger struct {
		Period int     `yaml:"period"`
		StdDev float64 `yaml:"std_dev"`
	} `yaml:"bollinger"`
}

type Scanner struct {
	cfg Config
}

func New(cfg Config) *Scanner {
	return &Scanner{cfg: cfg}
}

// ScanMarket scans all market stocks, applies filters, sorts by score, returns top N.
func (s *Scanner) ScanMarket(stocks []fetcher.StockData) []StockAnalysis {
	var results []StockAnalysis
	for _, stock := range stocks {
		if len(stock.Candles) < 30 {
			continue
		}
		latest := stock.Candles[len(stock.Candles)-1]
		ind := s.calcIndicators(stock.Candles)
		n := len(stock.Candles)
		if latest.Close < s.cfg.MinPrice || ind.VolumeMA[n-1] < s.cfg.MinAvgVolume {
			continue
		}
		results = append(results, s.analyze(stock, ind))
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	topN := s.cfg.TopN
	if topN == 0 {
		topN = 50
	}
	if topN > 0 && len(results) > topN {
		results = results[:topN]
	}

	log.Printf("market scan: %d passed filters, showing %d", len(results), len(results))
	return results
}

// ScanPortfolio analyzes portfolio positions with stop-loss / take-profit logic.
func (s *Scanner) ScanPortfolio(stocks []fetcher.StockData) []StockAnalysis {
	results := s.analyzeAll(stocks)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// ScanWatchlist analyzes watchlist stocks.
func (s *Scanner) ScanWatchlist(stocks []fetcher.StockData) []StockAnalysis {
	results := s.analyzeAll(stocks)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func (s *Scanner) analyzeAll(stocks []fetcher.StockData) []StockAnalysis {
	results := make([]StockAnalysis, 0, len(stocks))
	for _, stock := range stocks {
		if len(stock.Candles) < 30 {
			log.Printf("skip %s: only %d candles", stock.Symbol, len(stock.Candles))
			continue
		}
		ind := s.calcIndicators(stock.Candles)
		results = append(results, s.analyze(stock, ind))
	}
	return results
}

func (s *Scanner) analyze(stock fetcher.StockData, ind indicator.Result) StockAnalysis {
	n := len(stock.Candles)
	latest := stock.Candles[n-1]

	closes := closeSlice(stock.Candles)
	volumes := volumeFloatSlice(stock.Candles)
	highs := highSlice(stock.Candles)
	lows := lowSlice(stock.Candles)

	// Volume ratio (current vs MA20) — needed for limit-up detection.
	var volRatio float64
	if ind.VolumeMA[n-1] > 0 {
		volRatio = float64(latest.Volume) / ind.VolumeMA[n-1]
	}

	// Limit-up (漲停) chip dynamics: 量縮 ≠ 轉弱，依價格是否失守判斷。
	limitStatus, limitNote := detectLimitStatus(stock.Candles, volRatio)

	// BestFourPoint checkpoints
	bfpChecks, bfpPoints := bestFourPoint(closes, volumes, highs, lows, ind, limitStatus, limitNote)

	// Composite numeric score
	sc, scoreReasons := score(closes, volumes, ind, limitStatus, limitNote)

	// Volume analysis (for display fields)
	va := analyzeVolume(closes, volumes, ind, limitStatus, limitNote)

	// Blend BFP + score for base action
	bfpAction := actionFromBFP(bfpPoints)
	numAction := rawAction(sc)
	baseAction := blendAction(bfpAction, numAction)

	// Portfolio P&L and position-specific overrides
	var pnlPct, pnlVal float64
	finalAction := baseAction
	var positionReason string

	if stock.Source == "portfolio" && stock.CostBasis > 0 {
		pnlPct = (latest.Close - stock.CostBasis) / stock.CostBasis * 100
		pnlVal = (latest.Close - stock.CostBasis) * float64(stock.Shares)

		override, reason := positionAdvice(pnlPct, ind.RSI[n-1], ind.MA20, ind.KDJ)
		if override != "" {
			finalAction = override
			positionReason = reason
		}
	}

	// Build final reasons list
	var reasons []string
	// BFP checkpoint summary first
	passedNames := []string{}
	failedNames := []string{}
	for _, c := range bfpChecks {
		if c.Pass {
			passedNames = append(passedNames, c.Name)
		} else {
			failedNames = append(failedNames, c.Name)
		}
	}
	reasons = append(reasons, fmt.Sprintf("交易評分 %d/5 條件成立（✓ %v）", bfpPoints, passedNames))
	// Checkpoint details
	for _, c := range bfpChecks {
		mark := "✓"
		if !c.Pass {
			mark = "✗"
		}
		reasons = append(reasons, fmt.Sprintf("%s [%s] %s", mark, c.Name, c.Reason))
	}
	// Score-based reasons (volume, etc.)
	reasons = append(reasons, scoreReasons...)
	// Position-specific advice last
	if positionReason != "" {
		reasons = append(reasons, "→ "+positionReason)
	} else if stock.Source == "portfolio" && stock.CostBasis > 0 {
		dir := "浮盈"
		if pnlPct < 0 {
			dir = "虧損"
		}
		reasons = append(reasons, fmt.Sprintf(
			"→ 持倉成本 %.1f，現價 %.1f，%s %.1f%%（%d股，損益 %+.0f 元）",
			stock.CostBasis, latest.Close, dir, pnlPct, stock.Shares, pnlVal))
	}

	// Price targets
	entry, stop, t1, t2 := priceTargets(latest.Close, ind.ATR[n-1], ind.BB)

	return StockAnalysis{
		Symbol: stock.Symbol,
		Name:   stock.Name,
		Source: stock.Source,
		Date:   latest.Date,

		Close:  latest.Close,
		Volume: latest.Volume,

		CostBasis: stock.CostBasis,
		Shares:    stock.Shares,
		PnLPct:    pnlPct,
		PnLValue:  pnlVal,

		Score:   sc,
		Action:  finalAction,
		Reasons: reasons,

		BFPPoints: bfpPoints,
		BFP:       bfpChecks,

		EntryPrice: entry,
		StopLoss:   stop,
		Target1:    t1,
		Target2:    t2,

		RSI:         ind.RSI[n-1],
		MA20:        ind.MA20[n-1],
		MA20Trend:   indicator.MA20TrendLabel(ind.MA20),
		KDJK:        ind.KDJ.K[n-1],
		KDJD:        ind.KDJ.D[n-1],
		KDJJ:        ind.KDJ.J[n-1],
		BBWidth:     ind.BB.Width[n-1],
		BBUpper:     ind.BB.Upper[n-1],
		BBLower:     ind.BB.Lower[n-1],
		VolumeRatio: volRatio,
		ATR:         ind.ATR[n-1],

		VolumeScore:       va.score,
		AvgVolume20:       int64(ind.VolumeMA[n-1]),
		PriceVolumeSignal: va.signal,
		BuySellRatio:      va.buySellRatio,
		IsLargeOrder:      va.isLargeOrder,

		LimitStatus: limitStatus,
		LimitNote:   limitNote,
	}
}

func (s *Scanner) calcIndicators(candles []fetcher.Candle) indicator.Result {
	return indicator.Calculate(candles, indicator.Config{
		KDJKPeriod:      s.cfg.KDJ.KPeriod,
		KDJDSmooth:      s.cfg.KDJ.DSmooth,
		KDJJSmooth:      s.cfg.KDJ.JSmooth,
		BollingerPeriod: s.cfg.Bollinger.Period,
		BollingerStdDev: s.cfg.Bollinger.StdDev,
	})
}

func closeSlice(candles []fetcher.Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = c.Close
	}
	return out
}

func volumeFloatSlice(candles []fetcher.Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = float64(c.Volume)
	}
	return out
}

func highSlice(candles []fetcher.Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = c.High
	}
	return out
}

func lowSlice(candles []fetcher.Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = c.Low
	}
	return out
}
