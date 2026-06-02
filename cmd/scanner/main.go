package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/report"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"gopkg.in/yaml.v3"
)

type config struct {
	Fetcher    fetcher.Config `yaml:"fetcher"`
	Scanner    scanner.Config `yaml:"scanner"`
	Report     report.Config  `yaml:"report"`
	StocksFile string         `yaml:"stocks_file"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "config file path")
	stocksPath := flag.String("stocks", "", "portfolio/watchlist YAML (overrides stocks_file in config)")
	dateStr := flag.String("date", "", "analysis date YYYY-MM-DD (default: today)")
	skipMarket := flag.Bool("no-market", false, "skip full market scan (faster)")
	topN := flag.Int("top", 0, "market scan top N (50 | 100 | 500); 0 = use config default")
	scanAll := flag.Bool("all", false, "show all scanned stocks, no top-N limit")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *stocksPath != "" {
		cfg.StocksFile = *stocksPath
	}
	if cfg.StocksFile == "" {
		cfg.StocksFile = "stocks.yaml"
	}

	// Resolve market scan scope: --all takes priority over --top
	var marketLabel string
	if *scanAll {
		cfg.Scanner.TopN = -1 // negative = no truncation
		marketLabel = "全部"
	} else if *topN > 0 {
		cfg.Scanner.TopN = *topN
		marketLabel = fmt.Sprintf("%d", *topN)
	} else {
		// use config value (top_n), fall back to 50
		if cfg.Scanner.TopN <= 0 {
			cfg.Scanner.TopN = 50
		}
		marketLabel = fmt.Sprintf("%d", cfg.Scanner.TopN)
	}

	analysisDate := time.Now()
	if *dateStr != "" {
		analysisDate, err = time.Parse("2006-01-02", *dateStr)
		if err != nil {
			log.Fatalf("parse date %q: %v", *dateStr, err)
		}
	}

	fmt.Printf("stock-scanner  date=%s\n\n", analysisDate.Format("2006-01-02"))

	f := fetcher.New(cfg.Fetcher)
	s := scanner.New(cfg.Scanner)

	// ── 1. Portfolio & Watchlist ──────────────────────────────────────────────
	var portfolioResults, watchlistResults []scanner.StockAnalysis

	if _, statErr := os.Stat(cfg.StocksFile); statErr == nil {
		fmt.Printf("[1/3] 讀取 %s ...\n", cfg.StocksFile)
		sl, err := fetcher.LoadStockList(cfg.StocksFile)
		if err != nil {
			log.Fatalf("load stocks file: %v", err)
		}

		if len(sl.Portfolio) > 0 {
			fmt.Printf("      抓取 Portfolio (%d 支)...\n", len(sl.Portfolio))
			pStocks, err := f.FetchPortfolioStocks(sl.Portfolio)
			if err != nil {
				log.Printf("portfolio fetch error: %v", err)
			} else {
				portfolioResults = s.ScanPortfolio(pStocks)
			}
		}

		if len(sl.Watchlist) > 0 {
			fmt.Printf("      抓取 Watchlist (%d 支)...\n", len(sl.Watchlist))
			wStocks, err := f.FetchWatchlistStocks(sl.Watchlist)
			if err != nil {
				log.Printf("watchlist fetch error: %v", err)
			} else {
				watchlistResults = s.ScanWatchlist(wStocks)
			}
		}
	} else {
		fmt.Printf("[1/3] %s 不存在，跳過 Portfolio/Watchlist\n", cfg.StocksFile)
	}

	// ── 2. Full Market Scan ───────────────────────────────────────────────────
	var marketResults []scanner.StockAnalysis
	if !*skipMarket {
		fmt.Println("[2/3] 取得台股清單 (TWSE)...")
		marketStocks, err := f.FetchAll()
		if err != nil {
			log.Fatalf("market fetch: %v", err)
		}
		fmt.Printf("      掃描 %d 支股票...\n", len(marketStocks))
		marketResults = s.ScanMarket(marketStocks)
	} else {
		fmt.Println("[2/3] 跳過市場掃描 (--no-market)")
	}

	// ── 3. Report ─────────────────────────────────────────────────────────────
	fmt.Println("[3/3] 產生報告...")
	r := report.New(cfg.Report)
	if err := r.Generate(marketResults, portfolioResults, watchlistResults, marketLabel, analysisDate); err != nil {
		log.Fatalf("report: %v", err)
	}
}

func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse yaml: %w", err)
	}
	return cfg, nil
}
