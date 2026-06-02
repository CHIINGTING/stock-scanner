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

// ScanMarket scans all market stocks and returns results sorted by score descending.
// Only stocks passing MinPrice/MinAvgVolume filters are included.
func (s *Scanner) ScanMarket(stocks []fetcher.StockData) []StockAnalysis {
	var results []StockAnalysis
	for _, stock := range stocks {
		if len(stock.Candles) < 30 {
			continue
		}
		latest := stock.Candles[len(stock.Candles)-1]
		ind := s.calcIndicators(stock.Candles)
		n := len(stock.Candles)

		if latest.Close < s.cfg.MinPrice {
			continue
		}
		if ind.VolumeMA[n-1] < s.cfg.MinAvgVolume {
			continue
		}

		results = append(results, s.analyze(stock, ind))
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	topN := s.cfg.TopN
	if topN == 0 {
		topN = 50 // config 未設定時的預設值
	}
	// topN < 0 表示 --all，不截斷
	if topN > 0 && len(results) > topN {
		results = results[:topN]
	}

	log.Printf("market scan: %d stocks passed filters, showing top %d", len(results), len(results))
	return results
}

// ScanPortfolio analyzes all portfolio stocks (no price/volume filter applied).
func (s *Scanner) ScanPortfolio(stocks []fetcher.StockData) []StockAnalysis {
	return s.analyzeAll(stocks)
}

// ScanWatchlist analyzes all watchlist stocks (no price/volume filter applied).
func (s *Scanner) ScanWatchlist(stocks []fetcher.StockData) []StockAnalysis {
	return s.analyzeAll(stocks)
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
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

func (s *Scanner) analyze(stock fetcher.StockData, ind indicator.Result) StockAnalysis {
	n := len(stock.Candles)
	latest := stock.Candles[n-1]

	closes := closeSlice(stock.Candles)
	volumes := volumeFloatSlice(stock.Candles)

	sc, reasons := score(closes, volumes, ind)

	// Portfolio P&L context
	var pnlPct, pnlVal float64
	if stock.Source == "portfolio" && stock.CostBasis > 0 {
		pnlPct = (latest.Close - stock.CostBasis) / stock.CostBasis * 100
		pnlVal = (latest.Close - stock.CostBasis) * float64(stock.Shares)
		reasons = append(reasons, portfolioReason(stock.CostBasis, latest.Close, pnlPct, stock.Shares))
	}

	action := actionFromScore(sc, stock.Source, pnlPct)
	entry, stop, t1, t2 := priceTargets(latest.Close, ind.ATR[n-1], ind.BB)

	var volRatio float64
	if ind.VolumeMA[n-1] > 0 {
		volRatio = float64(latest.Volume) / ind.VolumeMA[n-1]
	}

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
		Action:  action,
		Reasons: reasons,

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
	}
}

func portfolioReason(cost, close, pnlPct float64, shares int) string {
	dir := "浮盈"
	if pnlPct < 0 {
		dir = "虧損"
	}
	return fmt.Sprintf("持倉成本 %.1f，現價 %.1f，%s %.1f%%（%d 股，損益 %.0f 元）",
		cost, close, dir, pnlPct, shares, (close-cost)*float64(shares))
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
