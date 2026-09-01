package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/analysishistory"
	"github.com/deep-huang/stock-scanner/internal/etfflow"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/fx"
	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/macro"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/news/socialworkerdaily"
	"github.com/deep-huang/stock-scanner/internal/news/twetq"
	"github.com/deep-huang/stock-scanner/internal/report"
	"github.com/deep-huang/stock-scanner/internal/research"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"gopkg.in/yaml.v3"
)

type config struct {
	Fetcher fetcher.Config `yaml:"fetcher"`
	Scanner scanner.Config `yaml:"scanner"`
	Report  report.Config  `yaml:"report"`
	// Research (R13) is a separate top-level block, deliberately not nested under scanner:
	// internal/scanner must keep no dependency on the store, and a sibling block makes that
	// visible in the config file as well as in the import graph.
	Research    research.Config `yaml:"research"`
	StocksFile  string          `yaml:"stocks_file"`
	SectorsFile string          `yaml:"sectors_file"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "config file path")
	stocksPath := flag.String("stocks", "", "portfolio/watchlist YAML (overrides stocks_file in config)")
	stocksFilePath := flag.String("stocks-file", "", "alias of -stocks")
	dateStr := flag.String("date", "", "analysis date YYYY-MM-DD (default: today)")
	skipMarket := flag.Bool("no-market", false, "skip full market scan (faster)")
	topN := flag.Int("top", 0, "market scan top N (50 | 100 | 500); 0 = use config default")
	scanAll := flag.Bool("all", false, "show all scanned stocks, no top-N limit")
	sectorsPath := flag.String("sectors", "", "sector rotation YAML (overrides sectors_file in config)")
	skipRotation := flag.Bool("no-rotation", false, "skip sector rotation analysis")
	updateWatchlist := flag.Bool("update-watchlist", false, "掃描後將 BUY/WATCH/HOLD 結果寫回 stocks.yaml 的 watchlist（positions 不變）")

	// Accept an optional leading "run-all" subcommand token so that
	//   go run ./cmd/scanner run-all --update-watchlist ...
	// parses the same as the flag-only invocation.
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run-all" {
		args = args[1:]
	}
	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Scanner.Validate(); err != nil {
		log.Fatalf("%v", err)
	}

	if *stocksFilePath != "" {
		cfg.StocksFile = *stocksFilePath
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

	// The market read is loaded ONCE, here, and shared by the AI prompt and the research
	// record. Two loads could disagree — a snapshot written between them would give the
	// model one regime and the audit trail another — and the whole point of the record is
	// that it says what the model actually saw.
	marketCtx := loadMarketContext(cfg.Research.Defaulted().MarketSnapshotDir,
		analysisDate.Format("2006-01-02"))
	if marketCtx.Regime == "" {
		log.Printf("market: no dashboard snapshot for %s — regime recorded as UNAVAILABLE, "+
			"not as neutral (run: go run ./cmd/market-fetch -date %s)",
			analysisDate.Format("2006-01-02"), analysisDate.Format("2006-01-02"))
	}

	// ── 1. Portfolio & Watchlist ──────────────────────────────────────────────
	var portfolioResults []scanner.StockAnalysis
	var watchlistResults []scanner.WatchlistEntry
	var wStocks []fetcher.StockData  // raw watchlist OHLCV (enriched after rotation)
	var stockList *fetcher.StockList // retained for the news resolver dictionary

	if _, statErr := os.Stat(cfg.StocksFile); statErr == nil {
		fmt.Printf("[1/4] 讀取 %s ...\n", cfg.StocksFile)
		sl, err := fetcher.LoadStockList(cfg.StocksFile)
		if err != nil {
			log.Fatalf("load stocks file: %v", err)
		}
		stockList = sl

		if len(sl.AllPositions()) > 0 {
			fmt.Printf("      抓取 Positions (%d 支)...\n", len(sl.AllPositions()))
			pStocks, err := f.FetchPortfolioStocks(sl.AllPositions())
			if err != nil {
				log.Printf("portfolio fetch error: %v", err)
			} else {
				portfolioResults = s.ScanPortfolio(pStocks)
			}
		}

		// AllWatchlist merges watchlist_pinned (yours, never rebuilt) ahead of watchlist
		// (machine, rebuilt every scan). Without this a pinned stock would be tracked in the
		// file but never fetched, i.e. pinning would silently do nothing.
		watch := sl.AllWatchlist()
		if len(watch) > 0 {
			fmt.Printf("      抓取 Watchlist (%d 支，其中釘選 %d)...\n", len(watch), len(sl.WatchlistPinned))
			ws, err := f.FetchWatchlistStocks(watch)
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

		// R8-4: ETF flow / active-rotation CONTEXT (display-only). Post-pass attach
		// AFTER enrichment, so it never touches score/action/probability/order. Off by
		// default; snapshot gaps degrade quietly and never interrupt the scan.
		if cfg.Scanner.EnableETFFlow {
			attachETFFlow(watchlistResults, cfg.Scanner, analysisDate)
		}

		// R10-1: Institutional Flow + confluence CONTEXT (display-only). Post-pass attach
		// AFTER enrichment, so it never touches score/action/probability/order. Off by
		// default; missing snapshots degrade quietly and never interrupt the scan.
		if cfg.Scanner.EnableInstitution {
			attachInstitution(watchlistResults, wStocks, cfg.Scanner.Institution, analysisDate)
		}

		// R11: AI explanation (shadow-only). LAST post-pass — by here every score, action
		// and sort position is final, so the model is reading a finished verdict rather
		// than participating in one. Off by default; a missing OPENAI_API_KEY, a timeout,
		// a 429/5xx or malformed output all leave the scan and the report untouched.
		attachAI(watchlistResults, cfg.Scanner, marketCtx)
	}

	// ── 3.6 News / 消息面 (Phase 1) — SHADOW MODE, display/context only ──────────
	// Gated post-pass AFTER enrichment. Never touches score/action/probability/order.
	// Provider failures are isolated & logged; the scan always completes. Off by default.
	var newsSummary *news.MarketNewsSummary
	var newsEpisodes []news.EpisodeView
	if cfg.Scanner.EnableNews {
		// Look-ahead guard: only live-fetch news when the analysis date IS today. A
		// historical run must not stamp today's fetched news with a past ObservedAt.
		if now := time.Now(); news.SameDay(analysisDate, now) {
			newsSummary, newsEpisodes = collectNews(cfg.Scanner, cfg.Report.OutputDir, sectorList, stockList, watchlistResults, now)
		} else {
			log.Printf("news: skip live fetch for historical date %s (avoid look-ahead); read stored snapshot instead",
				analysisDate.Format("2006-01-02"))
		}
	}

	// ── 4. Report ─────────────────────────────────────────────────────────────
	fmt.Println("[4/4] 產生報告...")
	r := report.New(cfg.Report)
	// USD/TWD market context. Gated on research.enabled alone — the existing research switch
	// already means "this run may do shadow research", and a second flag for one header line
	// would be config for its own sake. A missing archive is simply no context.
	var fxCtx *fx.Metrics
	if cfg.Research.Enabled {
		fxCtx = loadFXContext(cfg.Research.Defaulted().FXDir, analysisDate)
	}

	// The macro view for THIS report's date. LoadAsOf, not "the newest file": a report dated
	// in the past must show what was knowable then, or every historical page silently gains
	// hindsight.
	var macroCtx *macro.Research
	if cfg.Research.Enabled {
		if snap, err := macro.LoadAsOf(cfg.Research.Defaulted().MacroDir,
			analysisDate.Format("2006-01-02")); err != nil {
			log.Printf("macro: %v", err)
		} else if snap != nil {
			r := macro.Interpret(snap, analysisDate.Format("2006-01-02"))
			macroCtx = &r
		}
	}

	gv := report.GuardrailViewOptions{
		Macro:                       macroCtx,
		FX:                          fxCtx,
		Show:                        cfg.Scanner.ShowGuardrailSignals,
		GuardrailScoringEnabled:     cfg.Scanner.EnableSignalGuardrailScoring,
		ShowBacktestInsights:        cfg.Scanner.ShowBacktestInsights,
		ShowETFFlow:                 cfg.Scanner.ShowETFFlow,
		ShowNews:                    cfg.Scanner.ShowNews,
		ShowInstitution:             cfg.Scanner.ShowInstitution,
		ShowBias:                    cfg.Scanner.ShowBias,
		ShowCandlestick:             cfg.Scanner.ShowCandlestick,
		ShowTechnicalIndicators:     cfg.Scanner.ShowTechnicalIndicators,
		ShowAI:                      cfg.Scanner.ShowAI,
		ShowTrendExtension:          cfg.Scanner.ShowTrendExtension,
		RSWatchThreshold:            cfg.Scanner.RSWatchThreshold,
		MFScoreModifierBuilding:     cfg.Scanner.MFScoreModifierBuilding,
		MFScoreModifierContinuation: cfg.Scanner.MFScoreModifierContinuation,
		MFScoreModifierShiftUp:      cfg.Scanner.MFScoreModifierShiftUp,
		MFScoreModifierFading:       cfg.Scanner.MFScoreModifierFading,
		MFScoreModifierShiftDown:    cfg.Scanner.MFScoreModifierShiftDown,
	}
	if err := r.Generate(marketResults, portfolioResults, watchlistResults, rotationResults, marketLabel, analysisDate, gv, newsSummary, newsEpisodes); err != nil {
		log.Fatalf("report: %v", err)
	}

	// R10-1: structured history sidecar (data/analysis_history/<date>.json) — the
	// authoritative source for cohort validation, independent of the HTML template.
	if cfg.Scanner.EnableInstitution || cfg.Scanner.EnableBias {
		snap := buildAnalysisHistory(watchlistResults, analysisDate)
		if path, err := analysishistory.Write("", snap); err != nil {
			log.Printf("analysis-history: write failed: %v", err)
		} else {
			fmt.Printf("       已寫入結構化歷史 %s\n", path)
		}
	}

	// R13-M2: the same canonical in-memory results, recorded into the research store.
	// Deliberately fed from the SAME variables the report and the sidecar above were built
	// from — never by re-reading either of them, which would make the store a second
	// translation of a second source.
	recordResearch(cfg, marketResults, portfolioResults, watchlistResults, rotationResults,
		len(marketStocks), analysisDate, marketCtx)

	// Publish the latest report as index.html at the repo root so GitHub Pages
	// serves the newest report at "/".
	if html, err := os.ReadFile(r.OutputPath(analysisDate)); err != nil {
		log.Printf("index.html: read report failed: %v", err)
	} else if err := os.WriteFile("index.html", html, 0o644); err != nil {
		log.Printf("index.html: write failed: %v", err)
	} else {
		fmt.Println("       已更新 index.html（GitHub Pages 最新報告）")
	}

	// ── 5. 自動更新觀察清單（--update-watchlist）────────────────────────────────
	if *updateWatchlist {
		cands := collectWatchCandidates(marketResults)
		added, err := fetcher.UpdateWatchlistFile(cfg.StocksFile, cands)
		if err != nil {
			log.Printf("update watchlist: %v", err)
		} else if len(added) == 0 {
			fmt.Printf("[5] 觀察清單無新增（%s）\n", cfg.StocksFile)
		} else {
			fmt.Printf("[5] 已更新觀察清單：新增 %d 支 → %s\n", len(added), cfg.StocksFile)
			for _, c := range added {
				fmt.Printf("      + %s %s\n", c.Code, c.Name)
			}
		}
	}
}

// collectWatchCandidates extracts the BUY/WATCH/HOLD stocks from scan results as
// watchlist candidates (de-duplicated by code). SELL/REDUCE/empty actions are
// ignored. Positions/duplicate filtering happens in UpdateWatchlistFile.
func collectWatchCandidates(results []scanner.StockAnalysis) []fetcher.WatchCandidate {
	var out []fetcher.WatchCandidate
	seen := map[string]bool{}
	for _, r := range results {
		if !qualifiesForWatchlist(r.Action) {
			continue
		}
		if r.Symbol == "" || seen[r.Symbol] {
			continue
		}
		seen[r.Symbol] = true
		out = append(out, fetcher.WatchCandidate{Code: r.Symbol, Name: r.Name})
	}
	return out
}

// attachETFFlow (R8-4) loads each watched ETF's recent snapshots, computes per-stock
// holdings flow, and attaches the classified ETF / active-rotation CONTEXT to the
// watchlist entries. Display-only: it runs after enrichment and never affects score /
// action / probability / order. Missing or malformed snapshots degrade quietly — an
// ETF with no data is skipped, and the scan is never interrupted.
func attachETFFlow(entries []scanner.WatchlistEntry, sc scanner.Config, reportDate time.Time) {
	dir := sc.ETFFlow.SnapshotDir
	if dir == "" {
		dir = "data/etf_holdings"
	}
	var histories []etfflow.History
	for _, w := range sc.ETFFlow.WatchETFs {
		snaps, err := etfflow.LoadHistory(dir, w.Code, sc.ETFFlow.HistoryDays)
		if err != nil {
			log.Printf("etf-flow: skip %s: %v", w.Code, err)
			continue
		}
		if len(snaps) == 0 {
			continue
		}
		histories = append(histories, etfflow.History{ETFCode: w.Code, ETFType: w.Type, Snapshots: snaps})
	}
	if len(histories) == 0 {
		return
	}
	flows := etfflow.CalculateFlows(histories)
	scanner.AttachETFFlow(entries, flows, reportDate, sc.ETFFlow.Strength.Thresholds())
}

// attachInstitution (R10-1) loads recent institution snapshots, builds the trading-day
// calendar from the watchlist candles, attaches the per-stock chip view + confluence, all
// display-only (runs after enrichment, never affects score/action/probability/order).
// Missing snapshots degrade quietly — a stock with no data is left nil.
func attachInstitution(entries []scanner.WatchlistEntry, wStocks []fetcher.StockData, cfg scanner.InstitutionConfig, reportDate time.Time) {
	cfg = cfg.Defaulted()
	loaded, err := institution.LoadHistory(cfg.SnapshotDir, reportDate.Format("2006-01-02"), cfg.HistoryDays)
	if err != nil {
		log.Printf("institution: load history: %v (continuing without)", err)
	}
	candlesByCode := make(map[string][]fetcher.Candle, len(wStocks))
	for _, s := range wStocks {
		candlesByCode[s.Symbol] = s.Candles
	}
	scanner.AttachInstitution(entries, loaded, candlesByCode, reportDate, cfg)
	scanner.AttachConfluence(entries)
}

// attachAI is the R11 shadow explanation post-pass. It runs LAST, after every score, action
// and sort position is final, and it cannot fail the scan: AttachAI swallows every error
// into a per-entry status, and a disabled feature or a missing OPENAI_API_KEY simply leaves
// the AI field nil. The 60s ceiling bounds the whole stage so a hung endpoint cannot stall a
// scheduled run — the deterministic report is already complete by this point.
func attachAI(entries []scanner.WatchlistEntry, sc scanner.Config, mc research.MarketContext) {
	if !sc.EnableAI {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(sc.AI.Defaulted().MaxStocks)*sc.AI.Timeout()+30*time.Second)
	defer cancel()
	// R13-M3: the market read reaches the model. Availability is carried explicitly so a
	// missing snapshot arrives as "we could not look", never as a neutral market.
	aiMarket := scanner.AIMarketContext{Available: mc.Regime != "", Regime: mc.Regime}
	if mc.Score != nil {
		aiMarket.Score = *mc.Score
	}
	scanner.AttachAI(ctx, entries, sc.AI, sc.EnableAI, aiMarket, log.Printf)
}

// buildAnalysisHistory converts the enriched watchlist into the canonical structured
// history schema (analysishistory), the single source cohort validation reads.
func buildAnalysisHistory(entries []scanner.WatchlistEntry, date time.Time) analysishistory.Snapshot {
	snap := analysishistory.Snapshot{Date: date.Format("2006-01-02")}
	for _, e := range entries {
		sd := analysishistory.StockData{
			Code:   e.A.Symbol,
			Action: string(e.A.Action),
			Score:  float64(e.RocketScore),
		}
		if e.Institution != nil {
			dc := e.Institution.Completeness.DataComplete
			sd.Institution = &analysishistory.InstitutionPayload{
				Foreign:         legPayload(e.Institution.Foreign, dc),
				InvestmentTrust: legPayload(e.Institution.Trust, dc),
				Dealer:          legPayload(e.Institution.Dealer, dc),
				Total:           legPayload(e.Institution.Total, dc),
			}
		}
		if e.Bias != nil {
			sd.Bias = &analysishistory.BiasPayload{
				Bias5: e.Bias.Bias5, Bias10: e.Bias.Bias10, Bias20: e.Bias.Bias20, Bias60: e.Bias.Bias60,
				Risk: e.Bias.Risk,
			}
		}
		snap.Stocks = append(snap.Stocks, sd)
	}
	return snap
}

func legPayload(s institution.LegStats, dataComplete bool) analysishistory.LegPayload {
	return analysishistory.LegPayload{
		Today:               s.TodayNet,
		Sum3d:               s.Sum3d,
		Sum5d:               s.Sum5d,
		Sum10d:              s.Sum10d,
		Sum20d:              s.Sum20d,
		ConsecutiveBuyDays:  s.ConsecutiveBuyDays,
		ConsecutiveSellDays: s.ConsecutiveSellDays,
		Transition:          s.Transition,
		NetBuyRatio:         s.NetBuyRatio,
		StreakComplete:      s.StreakComplete,
		DataComplete:        dataComplete,
	}
}

// collectNews runs the Phase-1 SHADOW-MODE news pass: build the resolver dictionary from
// the sector + stock universe (+ aliases file), fetch enabled providers (isolated
// failures), classify, attach per-stock views onto entries, and return the market summary
// for the report banner. It never changes score/action/probability/order. observedAt is
// the real wall-clock time (passed only when analysisDate == today) and becomes each
// signal's ObservedAt + the snapshot filename — the look-ahead guard.
func collectNews(sc scanner.Config, reportDir string, sectorList *fetcher.SectorList, stockList *fetcher.StockList, entries []scanner.WatchlistEntry, observedAt time.Time) (*news.MarketNewsSummary, []news.EpisodeView) {
	idx := news.NewResolveIndex()
	if sectorList != nil {
		for _, sec := range sectorList.Sectors {
			idx.AddSectorName(sec.Name)
			for _, st := range sec.Stocks {
				idx.AddStock(st.Code, st.Name)
				idx.AddStockSector(st.Code, sec.Name)
			}
		}
	}
	if stockList != nil {
		for _, p := range stockList.AllPositions() {
			idx.AddStock(p.Code, p.Name)
		}
		for _, w := range stockList.AllWatchlist() {
			idx.AddStock(w.Code, w.Name)
		}
	}
	if d, err := news.LoadAliases(sc.News.AliasesFile); err != nil {
		log.Printf("news: aliases load error: %v", err)
	} else {
		idx.ApplyAliases(d)
	}

	ncfg := sc.News.Defaulted()
	if ncfg.SnapshotDir == "" {
		ncfg.SnapshotDir = reportDir
	}
	timeout := time.Duration(ncfg.TimeoutSeconds) * time.Second

	var providers []news.NewsProvider
	if ncfg.Providers.SocialWorkerDaily.Enabled {
		providers = append(providers, socialworkerdaily.New(ncfg.Providers.SocialWorkerDaily, ncfg.UserAgent, timeout))
	}
	if ncfg.Providers.TWETQ.Enabled {
		providers = append(providers, twetq.New(ncfg.Providers.TWETQ, ncfg.UserAgent, timeout))
	}
	if len(providers) == 0 {
		log.Printf("news: enabled but no providers configured — skipping")
		return nil, nil
	}

	svc := news.NewService(providers, idx, ncfg, log.Printf)
	res := svc.Collect(context.Background(), observedAt)
	scanner.AttachNews(entries, res.Views)
	return res.Summary, news.BuildEpisodes(res.Signals)
}

// recordResearch (R13-M2) persists one scan into the SQLite research store.
//
// SHADOW-ONLY and default OFF: with research.enabled false nothing is opened and nothing is
// written. It runs AFTER the report and the sidecar, takes the results by value, and returns
// nothing — so no score, action, ranking or output can depend on it. Every failure is logged
// and stepped over; a research store that cannot be written must never cost the user a scan.
func recordResearch(cfg config, market, portfolio []scanner.StockAnalysis,
	watchlist []scanner.WatchlistEntry, rotation []scanner.SectorRotation,
	universeSize int, analysisDate time.Time, marketCtx research.MarketContext) {

	if !cfg.Research.Enabled {
		return
	}
	// This runs after the report and the sidecar are already on disk, and the scan is
	// otherwise complete. A shadow layer that took the whole run down with it would be worse
	// than one that records nothing, so even a panic from the storage layer is contained.
	defer func() {
		if p := recover(); p != nil {
			log.Printf("research: recording panicked, scan unaffected: %v", p)
		}
	}()

	rc := cfg.Research.Defaulted()
	rec, closeStore, err := research.Open(rc)
	if err != nil {
		log.Printf("research: store unavailable, scan unaffected: %v", err)
		return
	}
	defer func() {
		if err := closeStore(); err != nil {
			log.Printf("research: close: %v", err)
		}
	}()
	rec = rec.WithLogger(log.Printf)

	date := analysisDate.Format("2006-01-02")
	res, err := rec.RecordScan(context.Background(), research.RunMeta{
		TradingDate: date,
		StartedAt:   time.Now().UTC(),
	}, research.Input{
		Watchlist:    watchlist,
		Market:       market,
		Portfolio:    portfolio,
		Rotation:     rotation,
		MarketCtx:    marketCtx,
		UniverseSize: universeSize,
	})
	if err != nil {
		log.Printf("research: scan not recorded: %v", err)
		return
	}
	fmt.Printf("       已寫入研究庫 %s（run %s，%d 檔快照 / %d 筆 evidence）\n",
		rc.Store.Defaulted().Path, res.RunUID, res.Snapshots, res.Evidence)

	// R13-M3: the AI reading, bound to the SAME snapshots the scanner's own decisions were
	// recorded against. It runs after RecordScan because it needs those snapshot ids —
	// binding an opinion to the snapshot rather than to a symbol+date is what makes the two
	// comparable later without a join that could go wrong.
	//
	// A failure here is logged and dropped: the scan, the report and the scan record are all
	// already complete and correct, and losing the AI row must not undo any of them.
	aiRes, err := rec.RecordAI(context.Background(), res.RunUID, res.WatchlistSnapshots, watchlist)
	if err != nil {
		log.Printf("research: AI reading not recorded: %v", err)
		return
	}
	if aiRes.Runs > 0 {
		fmt.Printf("       已寫入 AI 解讀（run %s，%d 檔 / %d 筆 agent，略過 %d）\n",
			aiRes.RunUID, aiRes.Runs, aiRes.Agents, aiRes.Skipped)
	}
}

// loadMarketContext reads the regime the market dashboard already computed for this date.
//
// It only LOADS: cmd/market-fetch is what produces the snapshot, and a scan that runs before
// it (or on a date it never covered) simply records no market evidence rather than deriving a
// regime of its own.
func loadMarketContext(dir, date string) research.MarketContext {
	snap, err := marketservice.LoadSnapshot(dir, date)
	if err != nil || snap == nil {
		return research.MarketContext{}
	}
	score, confidence := snap.Score, snap.Confidence
	return research.MarketContext{
		Regime:     string(snap.Regime),
		Score:      &score,
		Confidence: &confidence,
		AsOfDate:   snap.Date,
	}
}

// loadFXContext reads the USD/TWD archive and returns the reading a session on analysisDate
// could legitimately have had.
//
// It only LOADS. cmd/fx-fetch produces the archive; a scan that runs before it, or on a date
// the archive does not reach, simply shows no currency context. A currency feed being stale
// must never be able to stop a scan, so every failure here is a nil and a log line.
func loadFXContext(dir string, analysisDate time.Time) *fx.Metrics {
	series, err := fx.Load(dir)
	if err != nil {
		log.Printf("fx: no USD/TWD archive (%v) — run: go run ./cmd/fx-fetch", err)
		return nil
	}
	m := series.MetricsAsOf(analysisDate.Format("2006-01-02"), fx.DefaultConfig())
	if !m.Status.OK() {
		log.Printf("fx: archive reaches %s but has nothing usable for %s (%s)",
			series.Bars[len(series.Bars)-1].Date, analysisDate.Format("2006-01-02"), m.Status)
		return nil
	}
	d1, _ := m.Change(1)
	d5, _ := m.Change(5)
	d20, _ := m.Change(20)
	fmt.Printf("       USD/TWD %.3f（依 %s 收盤）1D %+.2f%% ｜5D %+.2f%% ｜20D %+.2f%% ｜%s\n",
		m.Close, m.FXDate, d1, d5, d20, m.Context)
	return &m
}

// qualifiesForWatchlist reports whether an action should land a stock on the
// watchlist. STRONG BUY is treated as a stronger BUY and included.
//
// HOLD is the weakest tier kept: it is neither a buy nor a sell, so it belongs on a
// watchlist rather than in the discard pile. It is also by far the most common of the
// three, so including it roughly doubles the list. REDUCE and SELL stay out — they are
// exit advice, and a stock being exited is not a candidate to enter.
func qualifiesForWatchlist(a scanner.Action) bool {
	switch a {
	case scanner.ActionStrongBuy, scanner.ActionBuy, scanner.ActionWatch, scanner.ActionHold:
		return true
	default:
		return false
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
