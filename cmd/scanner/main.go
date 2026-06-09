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
	Fetcher     fetcher.Config `yaml:"fetcher"`
	Scanner     scanner.Config `yaml:"scanner"`
	Report      report.Config  `yaml:"report"`
	StocksFile  string         `yaml:"stocks_file"`
	SectorsFile string         `yaml:"sectors_file"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "config file path")
	stocksPath := flag.String("stocks", "", "portfolio/watchlist YAML (overrides stocks_file in config)")
	dateStr := flag.String("date", "", "analysis date YYYY-MM-DD (default: today)")
	skipMarket := flag.Bool("no-market", false, "skip full market scan (faster)")
	topN := flag.Int("top", 0, "market scan top N (50 | 100 | 500); 0 = use config default")
	scanAll := flag.Bool("all", false, "show all scanned stocks, no top-N limit")
	sectorsPath := flag.String("sectors", "", "sector rotation YAML (overrides sectors_file in config)")
	skipRotation := flag.Bool("no-rotation", false, "skip sector rotation analysis")
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

	if *sectorsPath != "" {
		cfg.SectorsFile = *sectorsPath
	}
	if cfg.SectorsFile == "" {
		cfg.SectorsFile = "configs/sectors.yaml"
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
	var portfolioResults []scanner.StockAnalysis
	var watchlistResults []scanner.WatchlistEntry
	var wStocks []fetcher.StockData // raw watchlist OHLCV (enriched after rotation)

	if _, statErr := os.Stat(cfg.StocksFile); statErr == nil {
		fmt.Printf("[1/4] 讀取 %s ...\n", cfg.StocksFile)
		sl, err := fetcher.LoadStockList(cfg.StocksFile)
		if err != nil {
			log.Fatalf("load stocks file: %v", err)
		}

		if len(sl.AllPositions()) > 0 {
			fmt.Printf("      抓取 Positions (%d 支)...\n", len(sl.AllPositions()))
			pStocks, err := f.FetchPortfolioStocks(sl.AllPositions())
			if err != nil {
				log.Printf("portfolio fetch error: %v", err)
			} else {
				portfolioResults = s.ScanPortfolio(pStocks)
			}
		}

		if len(sl.Watchlist) > 0 {
			fmt.Printf("      抓取 Watchlist (%d 支)...\n", len(sl.Watchlist))
			ws, err := f.FetchWatchlistStocks(sl.Watchlist)
			if err != nil {
				log.Printf("watchlist fetch error: %v", err)
			} else {
				wStocks = ws // enriched into rocket candidates after rotation (step 3.5)
			}
		}
	} else {
		fmt.Printf("[1/4] %s 不存在，跳過 Portfolio/Watchlist\n", cfg.StocksFile)
	}

	// ── 2. Full Market Scan ───────────────────────────────────────────────────
	var marketResults []scanner.StockAnalysis
	var marketStocks []fetcher.StockData // retained for full-market RS table (C6a)
	if !*skipMarket {
		fmt.Println("[2/4] 取得台股清單 (TWSE)...")
		var ferr error
		marketStocks, ferr = f.FetchAll()
		if ferr != nil {
			log.Fatalf("market fetch: %v", ferr)
		}
		fmt.Printf("      掃描 %d 支股票...\n", len(marketStocks))
		marketResults = s.ScanMarket(marketStocks)
	} else {
		fmt.Println("[2/4] 跳過市場掃描 (--no-market)")
	}

	// ── 3. Sector Rotation ─────────────────────────────────────────────────────
	var rotationResults []scanner.SectorRotation
	var sectorList *fetcher.SectorList
	var grouped map[string][]fetcher.StockData
	if !*skipRotation {
		if _, statErr := os.Stat(cfg.SectorsFile); statErr == nil {
			fmt.Printf("[3/4] 讀取族群清單 %s ...\n", cfg.SectorsFile)
			sl, err := fetcher.LoadSectorList(cfg.SectorsFile)
			if err != nil {
				log.Printf("load sectors file: %v", err)
			} else {
				sectorList = sl
				infos := sl.UniqueInfos()
				fmt.Printf("      抓取族群成員 (%d 支)...\n", len(infos))
				sectorStocks, err := f.FetchSectorStocks(infos)
				if err != nil {
					log.Printf("sector fetch error: %v", err)
				} else {
					var order []string
					order, grouped = groupBySector(sl, sectorStocks)
					rotationResults = s.ScanRotation(order, grouped)
				}
			}
		} else {
			fmt.Printf("[3/4] %s 不存在，跳過族群輪動\n", cfg.SectorsFile)
		}
	} else {
		fmt.Println("[3/4] 跳過族群輪動 (--no-rotation)")
	}

	// ── 3.5 Watchlist 飆股候選追蹤（連動族群輪動）────────────────────────────────
	if len(wStocks) > 0 {
		fmt.Printf("      分析 Watchlist 飆股候選 (%d 支)...\n", len(wStocks))
		sectorOf := buildSectorOf(sectorList, rotationResults)
		rotMap := make(map[string]*scanner.SectorRotation, len(rotationResults))
		for i := range rotationResults {
			rotMap[rotationResults[i].Name] = &rotationResults[i]
		}
		// C6a: full-market RS table (nil when RS disabled); attached as shadow only.
		rsTable := s.BuildRSTable(marketStocks)
		watchlistResults = s.EnrichWatchlist(wStocks, sectorOf, rotMap, grouped, rsTable)
	}

	// ── 4. Report ─────────────────────────────────────────────────────────────
	fmt.Println("[4/4] 產生報告...")
	r := report.New(cfg.Report)
	gv := report.GuardrailViewOptions{
		Show:                        cfg.Scanner.ShowGuardrailSignals,
		GuardrailScoringEnabled:     cfg.Scanner.EnableSignalGuardrailScoring,
		ShowBacktestInsights:        cfg.Scanner.ShowBacktestInsights,
		RSWatchThreshold:            cfg.Scanner.RSWatchThreshold,
		MFScoreModifierBuilding:     cfg.Scanner.MFScoreModifierBuilding,
		MFScoreModifierContinuation: cfg.Scanner.MFScoreModifierContinuation,
		MFScoreModifierShiftUp:      cfg.Scanner.MFScoreModifierShiftUp,
		MFScoreModifierFading:       cfg.Scanner.MFScoreModifierFading,
		MFScoreModifierShiftDown:    cfg.Scanner.MFScoreModifierShiftDown,
	}
	if err := r.Generate(marketResults, portfolioResults, watchlistResults, rotationResults, marketLabel, analysisDate, gv); err != nil {
		log.Fatalf("report: %v", err)
	}
}

// buildSectorOf maps each member stock code to its sector name, preferring the
// highest-ranked sector (by rotation opportunity) when a code belongs to several.
func buildSectorOf(sl *fetcher.SectorList, ranked []scanner.SectorRotation) map[string]string {
	out := map[string]string{}
	if sl == nil {
		return out
	}
	members := map[string][]string{}
	for _, sec := range sl.Sectors {
		for _, st := range sec.Stocks {
			members[sec.Name] = append(members[sec.Name], st.Code)
		}
	}
	for _, r := range ranked { // ranked order = opportunity order
		for _, code := range members[r.Name] {
			if _, ok := out[code]; !ok {
				out[code] = r.Name
			}
		}
	}
	for _, sec := range sl.Sectors { // sectors not present in ranked (rotation skipped)
		for _, st := range sec.Stocks {
			if _, ok := out[st.Code]; !ok {
				out[st.Code] = sec.Name
			}
		}
	}
	return out
}

// groupBySector distributes the de-duplicated fetched data back into each sector
// (a stock may appear in multiple sectors). Returns the sector order and the grouping.
func groupBySector(sl *fetcher.SectorList, data []fetcher.StockData) ([]string, map[string][]fetcher.StockData) {
	byCode := make(map[string]fetcher.StockData, len(data))
	for _, d := range data {
		byCode[d.Symbol] = d
	}
	order := make([]string, 0, len(sl.Sectors))
	grouped := make(map[string][]fetcher.StockData, len(sl.Sectors))
	for _, sec := range sl.Sectors {
		order = append(order, sec.Name)
		for _, st := range sec.Stocks {
			if d, ok := byCode[st.Code]; ok {
				// Preserve the sector's preferred display name.
				if st.Name != "" {
					d.Name = st.Name
				}
				grouped[sec.Name] = append(grouped[sec.Name], d)
			}
		}
	}
	return order, grouped
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
