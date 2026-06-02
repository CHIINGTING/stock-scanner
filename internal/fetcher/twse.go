package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	twseStockDayAllURL = "https://openapi.twse.com.tw/v1/exchangeReport/STOCK_DAY_ALL"
	tpexQuotesURL      = "https://www.tpex.org.tw/openapi/v1/tpex_mainboard_daily_close_quotes"
)

// ── TWSE (上市) ───────────────────────────────────────────────────────────────

type twseRow struct {
	Code string `json:"Code"`
	Name string `json:"Name"`
}

// FetchStockList returns all TWSE-listed (上市) ordinary stocks.
func (f *Fetcher) FetchStockList() ([]StockInfo, error) {
	rows, err := fetchJSONSlice[twseRow](f, twseStockDayAllURL)
	if err != nil {
		return nil, fmt.Errorf("TWSE: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("TWSE returned empty list (market may be closed)")
	}

	var stocks []StockInfo
	for _, r := range rows {
		code := strings.TrimSpace(r.Code)
		name := strings.TrimSpace(r.Name)
		if !isOrdinaryStockCode(code) {
			continue
		}
		stocks = append(stocks, StockInfo{Symbol: code, Name: name, Market: "TW"})
	}
	return stocks, nil
}

// ── TPEX (上櫃) ───────────────────────────────────────────────────────────────

type tpexRow struct {
	Code    string `json:"SecuritiesCompanyCode"`
	Company string `json:"Company"`
}

// FetchTPEXList returns all TPEX-listed (上櫃) ordinary stocks.
func (f *Fetcher) FetchTPEXList() ([]StockInfo, error) {
	rows, err := fetchJSONSlice[tpexRow](f, tpexQuotesURL)
	if err != nil {
		return nil, fmt.Errorf("TPEX: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("TPEX returned empty list")
	}

	var stocks []StockInfo
	for _, r := range rows {
		code := strings.TrimSpace(r.Code)
		name := strings.TrimSpace(r.Company)
		if !isOrdinaryStockCode(code) {
			continue
		}
		stocks = append(stocks, StockInfo{Symbol: code, Name: name, Market: "TWO"})
	}
	return stocks, nil
}

// ── shared helpers ────────────────────────────────────────────────────────────

// isOrdinaryStockCode returns true for 4-digit codes that are ordinary shares.
func isOrdinaryStockCode(code string) bool {
	if len(code) != 4 {
		return false
	}
	if strings.HasPrefix(code, "0") { // ETFs / funds
		return false
	}
	return true
}

// fetchJSONSlice is a generic helper: GETs rawURL and unmarshals the body into []T.
// Go methods cannot have type parameters, so this is a package-level function.
func fetchJSONSlice[T any](f *Fetcher, rawURL string) ([]T, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stock-scanner/1.0")
	req.Header.Set("Accept", "application/json")

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var result []T
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		return result, nil
	}
	return nil, fmt.Errorf("request failed after 3 attempts: %w", lastErr)
}
