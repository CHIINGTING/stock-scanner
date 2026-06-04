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
