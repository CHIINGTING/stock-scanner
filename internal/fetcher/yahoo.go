package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const yahooChartURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

type yahooResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol    string `json:"symbol"`
				ShortName string `json:"shortName"`
				LongName  string `json:"longName"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// fetchYahoo downloads 6 months of daily OHLCV data from Yahoo Finance.
// providedName overrides the Yahoo-returned name when non-empty.
func (f *Fetcher) fetchYahoo(symbol, providedName string) (StockData, error) {
	ticker := symbol + ".TW"
	params := url.Values{
		"interval": []string{"1d"},
		"range":    []string{"6mo"},
	}
	reqURL := yahooChartURL + ticker + "?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return StockData{Symbol: symbol}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")
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
		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("rate limited (429)")
			time.Sleep(5 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		var yr yahooResponse
		if err := json.Unmarshal(body, &yr); err != nil {
			return StockData{Symbol: symbol}, fmt.Errorf("parse: %w", err)
		}
		if yr.Chart.Error != nil {
			return StockData{Symbol: symbol}, fmt.Errorf("yahoo: %s", yr.Chart.Error.Description)
		}
		if len(yr.Chart.Result) == 0 {
			return StockData{Symbol: symbol}, fmt.Errorf("no data")
		}

		res := yr.Chart.Result[0]
		if len(res.Indicators.Quote) == 0 {
			return StockData{Symbol: symbol}, fmt.Errorf("no quote")
		}
		q := res.Indicators.Quote[0]

		// resolve display name
		name := providedName
		if name == "" {
			name = res.Meta.ShortName
		}
		if name == "" {
			name = symbol
		}

		candles := make([]Candle, 0, len(res.Timestamp))
		for i, ts := range res.Timestamp {
			if i >= len(q.Close) || q.Close[i] == nil ||
				i >= len(q.Open) || q.Open[i] == nil ||
				i >= len(q.High) || q.High[i] == nil ||
				i >= len(q.Low) || q.Low[i] == nil ||
				i >= len(q.Volume) || q.Volume[i] == nil {
				continue
			}
			c := Candle{
				Date:   time.Unix(ts, 0).UTC(),
				Open:   *q.Open[i],
				High:   *q.High[i],
				Low:    *q.Low[i],
				Close:  *q.Close[i],
				Volume: *q.Volume[i],
			}
			if math.IsNaN(c.Open) || math.IsNaN(c.High) || math.IsNaN(c.Low) || math.IsNaN(c.Close) {
				continue
			}
			candles = append(candles, c)
		}
		sort.Slice(candles, func(i, j int) bool {
			return candles[i].Date.Before(candles[j].Date)
		})

		return StockData{Symbol: symbol, Name: name, Candles: candles}, nil
	}
	return StockData{Symbol: symbol}, fmt.Errorf("yahoo fetch failed after 3 attempts: %w", lastErr)
}
