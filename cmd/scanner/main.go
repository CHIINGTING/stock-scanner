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
	"github.com/deep-huang/stock-scanner/internal/fundamental"
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
	"github.com/deep-huang/stock-scanner/internal/valuation"
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
		watchlistResults = buildWatchlist(s, cfg.Scanner, wStocks, marketStocks,
			sectorList, rotationResults, grouped, analysisDate, marketCtx)
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

	// Per-stock research reads for THIS report's date. LoadAsOf, not "the newest archive":
	// a report dated in the past must show what was knowable then, or every historical page
	// silently gains hindsight about earnings that had not been filed yet.
	//
	// Archive reads only — no network. The daily producers (fundamental-fetch,
	// valuation-fetch) are what populate these; a report that fetched would be slow, would
	// depend on four exchanges being up, and on a historical run would return today's
	// figures under a past date.
	fundViews, valViews := loadResearchViews(cfg, watchlistResults, analysisDate)
	attachEntryPlanValuation(watchlistResults, cfg.Scanner, wStocks, marketCtx, analysisDate, fundViews, valViews)

	gv := report.GuardrailViewOptions{
		Fundamental:                 fundViews,
		Valuation:                   valViews,
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
		ShowEntryPlan:               cfg.Scanner.ShowEntryPlan,
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

// buildWatchlist is the PRODUCTION watchlist pipeline: it enriches the fetched watchlist
// candles into WatchlistEntry values and then runs every shadow post-pass, in the order the
// binary runs them. main() calls it, and in production nothing else does.
//
// # WHY THIS IS A FUNCTION AT ALL (EP-6B)
//
// Until EP-6B this body was inline in main(), and main() is 350 lines that need a config
// file, the network and the filesystem — so no test could execute it. The consequence was
// MEASURED, not assumed: deleting the attachEntryPlan call left `go test ./cmd/scanner/...
// ./internal/scanner/...` fully green, because an unused function is legal Go. The four older
// attaches (ETF flow, institution, AI, and news further down main) had exactly the same hole.
//
// Extracting the body gives the wiring a seam a BEHAVIOURAL test can drive:
// cmd/scanner/entryplan_pipeline_test.go calls this function with fixture candles and fails
// if the returned entries carry no plan — so removing the attachEntryPlan call below is now
// caught by a test that reads RESULTS, not source text.
//
// # WHAT THIS SEAM STILL DOES NOT COVER — stated plainly rather than smoothed over
//
// Deleting the buildWatchlist CALL from main() is STILL not caught by any behavioural test.
// The hole moved up one level; it did not disappear. Closing it would mean executing main()
// itself, which needs a config file, the network and the filesystem.
//
// That one level is covered by the weakest guard available today: an AST assertion that
// main() contains a call to buildWatchlist (TestMainCallsTheProductionWatchlistSeam). That is
// STRUCTURAL, not behavioural — strictly weaker than the test below it. It would still pass
// if the call were moved somewhere that never runs, and it proves nothing about what the call
// produces. It is an early warning, not a proof, and the wiring must not be described as
// fully proven while it is what guards this layer.
//
// # PURE MOVE
//
// Every attach below keeps the arguments, the order and the gating it had inline; only the
// names of the values changed (cfg.Scanner → sc, watchlistResults → out). EP-6B changed no
// post-pass and reordered nothing. The comments are the originals with ONE exception: the
// ETF-flow and institution comments are verbatim, but the R11 comment was updated, because
// "LAST post-pass" stopped being true once EP-6's entry plan was appended after it.
func buildWatchlist(
	s *scanner.Scanner,
	sc scanner.Config,
	wStocks []fetcher.StockData, // watchlist OHLCV, the pass's subject
	marketStocks []fetcher.StockData, // full-market candles, for the C6a RS table only
	sectorList *fetcher.SectorList,
	rotationResults []scanner.SectorRotation,
	grouped map[string][]fetcher.StockData,
	analysisDate time.Time,
	marketCtx research.MarketContext,
) []scanner.WatchlistEntry {

	fmt.Printf("      分析 Watchlist 飆股候選 (%d 支)...\n", len(wStocks))
	sectorOf := buildSectorOf(sectorList, rotationResults)
	rotMap := make(map[string]*scanner.SectorRotation, len(rotationResults))
	for i := range rotationResults {
		rotMap[rotationResults[i].Name] = &rotationResults[i]
	}
	// C6a: full-market RS table (nil when RS disabled); attached as shadow only.
	rsTable := s.BuildRSTable(marketStocks)
	out := s.EnrichWatchlist(wStocks, sectorOf, rotMap, grouped, rsTable)

	// R8-4: ETF flow / active-rotation CONTEXT (display-only). Post-pass attach
	// AFTER enrichment, so it never touches score/action/probability/order. Off by
	// default; snapshot gaps degrade quietly and never interrupt the scan.
	if sc.EnableETFFlow {
		attachETFFlow(out, sc, analysisDate)
	}

	// R10-1: Institutional Flow + confluence CONTEXT (display-only). Post-pass attach
	// AFTER enrichment, so it never touches score/action/probability/order. Off by
	// default; missing snapshots degrade quietly and never interrupt the scan.
	if sc.EnableInstitution {
		attachInstitution(out, wStocks, sc.Institution, analysisDate)
	}

	// R11: AI explanation (shadow-only). The last post-pass that can reach the network,
	// and — until EP-6 — the last one full stop; EP-6's entry plan is deliberately
	// placed AFTER it so no plan can reach the prompt. By here every score, action
	// and sort position is final, so the model is reading a finished verdict rather
	// than participating in one. Off by default; a missing OPENAI_API_KEY, a timeout,
	// a 429/5xx or malformed output all leave the scan and the report untouched.
	attachAI(out, sc, marketCtx)

	// EP-6: entry plan (shadow-only). Placed here for the reason the R11 comment
	// above gives — by this point every score, action, probability and SORT POSITION
	// is final (the ordering happens at the end of EnrichWatchlist — the RocketScore
	// sort and the R4-3 MTF tie-breaker, watchlist.go:363-367, both of which run
	// before it returns), so the plan reads a finished verdict rather than joining in
	// one. It runs AFTER the AI pass as well, so an entry plan cannot reach the model
	// prompt: at the moment buildAIEvidence runs, no plan exists yet.
	//
	// Deterministic and offline: no network, no clock, no I/O. Off by default, and a
	// stock whose evidence is missing gets a plan that says which evidence was missing
	// rather than no plan at all.
	attachEntryPlan(out, sc, wStocks, marketCtx)

	return out
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

// attachEntryPlan (EP-6) is the entry-plan shadow post-pass: it projects each watchlist entry
// onto the entryplan input contract and attaches the computed plan.
//
// The flag check lives INSIDE scanner.AttachEntryPlan rather than around this call, following
// attachAI: one gate, in the function that owns the feature, so a caller cannot forget it and
// there is exactly one place to read to know what "off" means. Off means every EntryPlan field
// stays nil — feature absence, never a plan carrying a "disabled" verdict.
//
// It cannot fail the scan and cannot slow it down in any interesting way: entryplan is a pure
// domain package with no network, no filesystem, no database and no clock, and ComputePlan is
// total — defined for every input, including a snapshot with nothing in it.
//
// # EP-6C: the two inputs the projection gained, both ALREADY LOADED
//
// The per-symbol candle index is built here from wStocks — the exact series EnrichWatchlist
// analysed in this same call — exactly as attachInstitution builds its own. Nothing is
// fetched: the bridge needs the series for the previous close and the adjustment age, and
// re-reading it from anywhere else would risk handing the plan a DIFFERENT series from the one
// the analysis ran on, which is the failure the bridge's own alignment check refuses.
//
// The market read is the SAME research.MarketContext main() loaded once and handed to the AI
// pass, mapped to scanner.EntryPlanMarket with the SAME availability rule attachAI uses
// (mc.Regime != ""). One load, one meaning: the entry plan and the AI explanation cannot end
// up describing two different markets, which is the reason main() loads it once at all.
func attachEntryPlan(entries []scanner.WatchlistEntry, sc scanner.Config,
	wStocks []fetcher.StockData, mc research.MarketContext) {

	candlesByCode := make(map[string][]fetcher.Candle, len(wStocks))
	for _, s := range wStocks {
		candlesByCode[s.Symbol] = s.Candles
	}
	market := scanner.EntryPlanMarket{Available: mc.Regime != "", Regime: mc.Regime}
	scanner.AttachEntryPlan(entries, candlesByCode, market, sc.EnableEntryPlan)
}

// attachEntryPlanValuation supplies the already-loaded PIT research views to the pure
// valuation domain, then reprojects EntryPlan only. No fetch occurs here.
func attachEntryPlanValuation(entries []scanner.WatchlistEntry, sc scanner.Config,
	wStocks []fetcher.StockData, mc research.MarketContext, loadedAsOf time.Time,
	funds map[string]*fundamental.View, vals map[string]*valuation.Valuation) {

	if !sc.EnableEntryPlan {
		return
	}
	candlesByCode := make(map[string][]fetcher.Candle, len(wStocks))
	for _, stock := range wStocks {
		candlesByCode[stock.Symbol] = stock.Candles
	}
	evidence := entryPlanValuationEvidence(entries, candlesByCode, loadedAsOf, funds, vals)
	market := scanner.EntryPlanMarket{Available: mc.Regime != "", Regime: mc.Regime}
	scanner.AttachEntryPlan(entries, candlesByCode, market, true, evidence)
}

func entryPlanValuationEvidence(entries []scanner.WatchlistEntry, candlesByCode map[string][]fetcher.Candle,
	loadedAsOf time.Time, funds map[string]*fundamental.View,
	vals map[string]*valuation.Valuation) map[string]scanner.EntryPlanValuation {
	evidence := make(map[string]scanner.EntryPlanValuation, len(entries))
	loadDate := loadedAsOf.Format("2006-01-02")
	for i := range entries {
		code := entries[i].A.Symbol
		entryDate := entries[i].A.Date.Format("2006-01-02")
		// Research views were loaded once at loadDate. They are valid only for an entry
		// describing that exact session: relabelling the same loaded pointers with an older
		// entry date would not make them point-in-time data for that date.
		if entryDate != loadDate {
			evidence[code] = scanner.EntryPlanValuation{Status: string(valuation.Unavailable),
				Suitability: string(valuation.SuitabilityInsufficientData), AsOf: entryDate}
			continue
		}
		v := valuation.BuildEntryPlanEvidence(loadDate,
			code, entries[i].A.Close, candlesByCode[code], vals[code], funds[code])
		evidence[code] = scanner.EntryPlanValuation{Status: string(v.Status), BaseTarget: v.BaseTarget,
			Suitability: string(v.Suitability), AsOf: v.AsOf}
	}
	return evidence
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

// loadResearchViews reads the archived fundamental and valuation records for the stocks on
// the page, as of the report's own date.
//
// Absent entries are simply absent: a symbol with nothing archived gets no map entry, and the
// report renders no section for it. That is different from an archived record whose fields
// are missing — which does render, saying UNAVAILABLE per field.
func loadResearchViews(cfg config, entries []scanner.WatchlistEntry, date time.Time) (
	map[string]*fundamental.View, map[string]*valuation.Valuation) {

	if !cfg.Research.Enabled || len(entries) == 0 {
		return nil, nil
	}
	rc := cfg.Research.Defaulted()
	asOf := date.Format("2006-01-02")
	fundSvc := fundamental.NewService(nil, rc.FundamentalDir, nil)
	valSvc := valuation.NewService(nil, rc.ValuationDir, nil)

	funds := map[string]*fundamental.View{}
	vals := map[string]*valuation.Valuation{}
	for i := range entries {
		code := entries[i].A.Symbol
		// StockAnalysis carries no market, and the archives are keyed by (code, market). A
		// listing lives on exactly one exchange, so trying both is two lookups of which at
		// most one hits — cheaper and more honest than threading a market field through the
		// report path for this alone.
		for _, market := range []string{fetcher.MarketTWSE, fetcher.MarketTPEX} {
			symbol := fetcher.YahooSymbol(code, market)
			if _, seen := funds[code]; !seen {
				if v, err := fundSvc.LoadView(asOf, symbol); err == nil &&
					v.Status != fundamental.Unavailable {
					vc := v
					funds[code] = &vc
				}
			}
			if _, seen := vals[code]; !seen {
				if v, err := valSvc.LoadValuation(asOf, symbol); err == nil &&
					v.Status != valuation.Unavailable {
					vc := v
					vals[code] = &vc
				}
			}
		}
	}
	if len(funds) > 0 || len(vals) > 0 {
		fmt.Printf("       研究資料：基本面 %d 檔、估值 %d 檔（as of %s）\n", len(funds), len(vals), asOf)
	}
	return funds, vals
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
