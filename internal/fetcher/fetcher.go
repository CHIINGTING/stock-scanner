package fetcher

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	RequestDelayMs int    `yaml:"request_delay_ms"`
	TimeoutSec     int    `yaml:"timeout_sec"`
	Concurrency    int    `yaml:"concurrency"`    // default 3
	CacheTTLMin    int    `yaml:"cache_ttl_min"`  // default 15
	CacheDir       string `yaml:"cache_dir"`      // default ".cache"
	EOFCooldownMin int    `yaml:"eof_cooldown_min"` // default 5
	HistoryRange   string `yaml:"history_range"`  // default "2y" (Yahoo range, e.g. 6mo/1y/2y)
}

type Fetcher struct {
	cfg         Config
	client      *http.Client
	marketCache sync.Map         // code → "TW" | "TWO"
	dataCache   *dataCache       // OHLCV TTL cache
	eofCooldown eofCooldownStore // per-ticker cooldown after network errors
}

func New(cfg Config) *Fetcher {
	// ── defaults ────────────────────────────────────────────────────────────
	if cfg.TimeoutSec == 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 3 // conservative default to avoid Yahoo rate-limiting
	}
	if cfg.RequestDelayMs == 0 {
		cfg.RequestDelayMs = 400
	}
	if cfg.CacheTTLMin == 0 {
		cfg.CacheTTLMin = 15
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = ".cache"
	}
	if cfg.EOFCooldownMin == 0 {
		cfg.EOFCooldownMin = 5
	}
	if cfg.HistoryRange == "" {
		cfg.HistoryRange = "2y"
	}

	log.Printf("fetcher: concurrency=%d delay=%dms cache_ttl=%dmin eof_cooldown=%dmin",
		cfg.Concurrency, cfg.RequestDelayMs, cfg.CacheTTLMin, cfg.EOFCooldownMin)

	return &Fetcher{
		cfg:       cfg,
		client:    &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second},
		dataCache: newDataCache(cfg.CacheDir, time.Duration(cfg.CacheTTLMin)*time.Minute),
	}
}

// FetchAll fetches TWSE + TPEX stock lists, then downloads OHLCV for all.
func (f *Fetcher) FetchAll() ([]StockData, error) {
	var all []StockInfo

	twse, err := f.FetchStockList()
	if err != nil {
		return nil, fmt.Errorf("TWSE stock list: %w", err)
	}
	log.Printf("TWSE: %d symbols", len(twse))
	all = append(all, twse...)

	tpex, err := f.FetchTPEXList()
	if err != nil {
		log.Printf("TPEX stock list warning (skipping): %v", err)
	} else {
		log.Printf("TPEX: %d symbols", len(tpex))
		all = append(all, tpex...)
	}

	log.Printf("total market symbols: %d", len(all))
	return f.fetchBatch(all, "market")
}

// FetchPortfolioStocks fetches OHLCV data for position/portfolio entries.
func (f *Fetcher) FetchPortfolioStocks(entries []PositionEntry) ([]StockData, error) {
	infos := make([]StockInfo, len(entries))
	for i, e := range entries {
		infos[i] = StockInfo{Symbol: e.Code, Name: e.Name, Market: e.Market}
	}
	results, err := f.fetchBatch(infos, "portfolio")
	if err != nil {
		return nil, err
	}
	entryMap := make(map[string]PositionEntry, len(entries))
	for _, e := range entries {
		entryMap[e.Code] = e
	}
	for i := range results {
		if e, ok := entryMap[results[i].Symbol]; ok {
			results[i].CostBasis = e.EntryPrice()
			results[i].Shares = e.Shares
			if results[i].Name == "" {
				results[i].Name = e.Name
			}
		}
	}
	return results, nil
}

// FetchWatchlistStocks fetches OHLCV data for watchlist entries.
func (f *Fetcher) FetchWatchlistStocks(entries []WatchEntry) ([]StockData, error) {
	infos := make([]StockInfo, len(entries))
	for i, e := range entries {
		infos[i] = StockInfo{Symbol: e.Code, Name: e.Name, Market: e.Market}
	}
	return f.fetchBatch(infos, "watchlist")
}

// fetchBatch downloads Yahoo Finance data for a list of stocks in parallel.
// Respects concurrency limit, per-request delay, and EOF cooldown.
func (f *Fetcher) fetchBatch(stocks []StockInfo, source string) ([]StockData, error) {
	type result struct {
		data StockData
		err  error
	}

	jobs := make(chan StockInfo, len(stocks))
	for _, s := range stocks {
		jobs <- s
	}
	close(jobs)

	out := make(chan result, len(stocks))
	sem := make(chan struct{}, f.cfg.Concurrency)
	var wg sync.WaitGroup

	for j := range jobs {
		wg.Add(1)
		j := j
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// inter-request delay (avoid hammering Yahoo)
			time.Sleep(time.Duration(f.cfg.RequestDelayMs) * time.Millisecond)

			data, err := f.fetchYahoo(j.Symbol, j.Name, j.Market)
			if err == nil {
				data.Source = source
			}
			out <- result{data: data, err: err}
		}()
	}

	go func() { wg.Wait(); close(out) }()

	var results []StockData
	var skipped int
	for r := range out {
		if r.err != nil {
			log.Printf("skip %s: %v", r.data.Symbol, r.err)
			skipped++
			continue
		}
		if len(r.data.Candles) < 30 {
			skipped++
			continue
		}
		results = append(results, r.data)
	}
	if skipped > 0 {
		log.Printf("skipped %d symbols", skipped)
	}
	return results, nil
}
