package fetcher

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PortfolioEntry is one holding in stocks.yaml.
type PortfolioEntry struct {
	Code   string  `yaml:"code"`
	Name   string  `yaml:"name"`
	Cost   float64 `yaml:"cost"`
	Shares int     `yaml:"shares"`
}

// WatchEntry is one item in the watchlist section.
type WatchEntry struct {
	Code string `yaml:"code"`
	Name string `yaml:"name"`
}

// StockList is the top-level structure of stocks.yaml.
type StockList struct {
	Portfolio []PortfolioEntry `yaml:"portfolio"`
	Watchlist []WatchEntry     `yaml:"watchlist"`
}

// LoadStockList reads and parses stocks.yaml.
func LoadStockList(path string) (*StockList, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var sl StockList
	if err := yaml.Unmarshal(data, &sl); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &sl, nil
}
