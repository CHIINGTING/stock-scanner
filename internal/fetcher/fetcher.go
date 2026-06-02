package fetcher

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type Config struct {
	RequestDelayMs int `yaml:"request_delay_ms"`
	TimeoutSec     int `yaml:"timeout_sec"`
	Concurrency    int `yaml:"concurrency"`
}

type Fetcher struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Fetcher {
	if cfg.TimeoutSec == 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 5
	}
	if cfg.RequestDelayMs == 0 {
		cfg.RequestDelayMs = 300
	}
	return &Fetcher{
		cfg:    cfg,
		client: &http.Client{Timeout: time.Duration(cfg.TimeoutSec) * time.Second},
	}
}

// FetchAll fetches the full TWSE market and returns data for all listed stocks.
func (f *Fetcher) FetchAll() ([]StockData, error) {
	stocks, err := f.FetchStockList()
	if err != nil {
		return nil, fmt.Errorf("fetch stock list: %w", err)
	}
	log.Printf("stock list: %d symbols", len(stocks))
	return f.fetchBatch(stocks, "market")
}

// FetchPortfolioStocks fetches stocks from the portfolio section of stocks.yaml.
func (f *Fetcher) FetchPortfolioStocks(entries []PortfolioEntry) ([]StockData, error) {
	infos := make([]StockInfo, len(entries))
	for i, e := range entries {
		infos[i] = StockInfo{Symbol: e.Code, Name: e.Name}
	}
	results, err := f.fetchBatch(infos, "portfolio")
	if err != nil {
		return nil, err
	}
	// attach cost basis and shares
	costMap := make(map[string]PortfolioEntry, len(entries))
	for _, e := range entries {
		costMap[e.Code] = e
	}
	for i := range results {
		if e, ok := costMap[results[i].Symbol]; ok {
			results[i].CostBasis = e.Cost
			results[i].Shares = e.Shares
			if results[i].Name == "" {
				results[i].Name = e.Name
			}
		}
	}
	return results, nil
}

// FetchWatchlistStocks fetches stocks from the watchlist section of stocks.yaml.
func (f *Fetcher) FetchWatchlistStocks(entries []WatchEntry) ([]StockData, error) {
	infos := make([]StockInfo, len(entries))
	for i, e := range entries {
		infos[i] = StockInfo{Symbol: e.Code, Name: e.Name}
	}
	return f.fetchBatch(infos, "watchlist")
}

// fetchBatch downloads Yahoo Finance data for a list of stocks in parallel.
func (f *Fetcher) fetchBatch(stocks []StockInfo, source string) ([]StockData, error) {
	type item struct {
		info StockInfo
		idx  int
	}
	type result struct {
		data StockData
		err  error
	}

	jobs := make(chan item, len(stocks))
	for i, s := range stocks {
		jobs <- item{info: s, idx: i}
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
			time.Sleep(time.Duration(f.cfg.RequestDelayMs) * time.Millisecond)

			data, err := f.fetchYahoo(j.info.Symbol, j.info.Name)
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
		log.Printf("skipped %d symbols (error or insufficient history)", skipped)
	}
	return results, nil
}
