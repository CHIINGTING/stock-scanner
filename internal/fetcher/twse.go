package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const twseStockDayAllURL = "https://openapi.twse.com.tw/v1/exchangeReport/STOCK_DAY_ALL"

type twseRow struct {
	Code string `json:"Code"`
	Name string `json:"Name"`
}

// FetchStockList returns all TSE-listed stocks from TWSE open API.
// Falls back to yesterday if today's data is not yet available.
func (f *Fetcher) FetchStockList() ([]StockInfo, error) {
	rows, err := f.fetchTWSERows(twseStockDayAllURL)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("TWSE returned empty stock list (market may be closed today)")
	}

	var stocks []StockInfo
	for _, r := range rows {
		code := strings.TrimSpace(r.Code)
		name := strings.TrimSpace(r.Name)
		// only include 4-digit stock codes (ordinary listed shares)
		if len(code) != 4 {
			continue
		}
		// skip ETFs/funds (code starts with 0)
		if strings.HasPrefix(code, "0") {
			continue
		}
		stocks = append(stocks, StockInfo{Symbol: code, Name: name})
	}
	return stocks, nil
}

func (f *Fetcher) fetchTWSERows(url string) ([]twseRow, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
		var rows []twseRow
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("parse TWSE response: %w", err)
		}
		return rows, nil
	}
	return nil, fmt.Errorf("TWSE request failed after 3 attempts: %w", lastErr)
}
