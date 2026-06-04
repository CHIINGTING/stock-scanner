package fetcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const yahooChartURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

// errNetworkFailure is a sentinel used to signal that the failure was a
// low-level network/connection problem (not a Yahoo API "no data" response).
// fetchYahooAutoDetect uses this to decide whether to try the other market suffix.
var errNetworkFailure = errors.New("network failure")

type yahooResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol    string `json:"symbol"`
				ShortName string `json:"shortName"`
				LongName  string `json:"longName"`
				// Latest session snapshot. Yahoo sometimes returns the most recent
				// daily bar with null OHLCV in the quote arrays while the real values
				// live here — used to backfill the latest bar (see parse loop).
				RegularMarketPrice   *float64 `json:"regularMarketPrice"`
				RegularMarketTime    int64    `json:"regularMarketTime"`
				RegularMarketOpen    *float64 `json:"regularMarketOpen"`
				RegularMarketDayHigh *float64 `json:"regularMarketDayHigh"`
				RegularMarketDayLow  *float64 `json:"regularMarketDayLow"`
				RegularMarketVolume  *int64   `json:"regularMarketVolume"`
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

// isNetworkError returns true for EOF, connection-reset and similar transient
// transport-layer errors that should NOT be retried immediately.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	s := err.Error()
	for _, needle := range []string{
		"EOF",
		"connection reset by peer",
		"connection refused",
		"use of closed network connection",
		"i/o timeout",
		"no such host",
		"TLS handshake timeout",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Public entry point
// ──────────────────────────────────────────────────────────────────────────────

// fetchYahoo dispatches to the appropriate fetch strategy based on market.
//
//   - market "TW"  → fetch {code}.TW
//   - market "TWO" → fetch {code}.TWO
//   - market ""    → auto-detect: try .TW, then .TWO (result cached)
func (f *Fetcher) fetchYahoo(code, providedName, market string) (StockData, error) {
	if market != "" {
		return f.fetchYahooTicker(code, providedName, YahooSymbol(code, market), market)
	}
	return f.fetchYahooAutoDetect(code, providedName)
}

// ──────────────────────────────────────────────────────────────────────────────
// Auto-detect (.TW → .TWO fallback)
// ──────────────────────────────────────────────────────────────────────────────

func (f *Fetcher) fetchYahooAutoDetect(code, providedName string) (StockData, error) {
	// Use cached market suffix if known
	if cached, ok := f.marketCache.Load(code); ok {
		m := cached.(string)
		return f.fetchYahooTicker(code, providedName, YahooSymbol(code, m), m)
	}

	// Try TWSE first
	data, err := f.fetchYahooTicker(code, providedName, YahooSymbol(code, "TW"), "TW")
	if err == nil && len(data.Candles) >= 5 {
		f.marketCache.Store(code, "TW")
		return data, nil
	}

	// If it was a network/cooldown error, don't attempt .TWO — the connection
	// itself is unreliable. Only try .TWO on API-level "no data" errors.
	if err != nil && errors.Is(err, errNetworkFailure) {
		return StockData{Symbol: code}, err
	}

	// API-level error (wrong market, no data) → try TPEX suffix
	data, err = f.fetchYahooTicker(code, providedName, YahooSymbol(code, "TWO"), "TWO")
	if err == nil && len(data.Candles) >= 5 {
		f.marketCache.Store(code, "TWO")
		return data, nil
	}

	return StockData{Symbol: code}, fmt.Errorf("%s: not found on .TW or .TWO", code)
}

// ──────────────────────────────────────────────────────────────────────────────
// Core fetch with cache + EOF cooldown
// ──────────────────────────────────────────────────────────────────────────────

func (f *Fetcher) fetchYahooTicker(code, providedName, ticker, market string) (StockData, error) {
	// 1. Data cache (TTL = cfg.CacheTTLMin)
	if data, ok := f.dataCache.Get(ticker); ok {
		return *data, nil
	}

	// 2. EOF cooldown — refuse request if ticker is still cooling down
	if active, until := f.eofCooldown.IsActive(ticker); active {
		remaining := time.Until(until).Round(time.Second)
		log.Printf("cooldown: skip %s for %v more", ticker, remaining)
		// Wrap as errNetworkFailure so auto-detect does not try the other suffix
		return StockData{Symbol: code}, fmt.Errorf("%w: %s cooldown active (%v remaining)",
			errNetworkFailure, ticker, remaining)
	}

	// 3. Build request
	histRange := f.cfg.HistoryRange
	if histRange == "" {
		histRange = "2y"
	}
	params := url.Values{"interval": {"1d"}, "range": {histRange}}
	reqURL := yahooChartURL + ticker + "?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return StockData{Symbol: code}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")
	req.Header.Set("Accept", "application/json")

	// 4. Attempt loop — max 2 retries for non-network errors only
	const maxAttempts = 3
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*3) * time.Second) // 3 s, 6 s
		}

		resp, doErr := f.client.Do(req)
		if doErr != nil {
			if isNetworkError(doErr) {
				// Set per-ticker cooldown and stop retrying immediately.
				f.eofCooldown.Set(ticker, time.Duration(f.cfg.EOFCooldownMin)*time.Minute)
				return StockData{Symbol: code}, fmt.Errorf("%w: %v", errNetworkFailure, doErr)
			}
			lastErr = doErr
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			if isNetworkError(readErr) {
				f.eofCooldown.Set(ticker, time.Duration(f.cfg.EOFCooldownMin)*time.Minute)
				return StockData{Symbol: code}, fmt.Errorf("%w: body read: %v", errNetworkFailure, readErr)
			}
			lastErr = readErr
			continue
		}

		// Rate-limited: wait longer and retry
		if resp.StatusCode == http.StatusTooManyRequests {
			log.Printf("yahoo: 429 rate-limit for %s, sleeping 10s", ticker)
			time.Sleep(10 * time.Second)
			lastErr = fmt.Errorf("rate-limited (429)")
			continue
		}

		if resp.StatusCode != http.StatusOK {
			// Non-retriable API error
			return StockData{Symbol: code}, fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		// 5. Parse JSON
		var yr yahooResponse
		if err := json.Unmarshal(body, &yr); err != nil {
			return StockData{Symbol: code}, fmt.Errorf("parse: %w", err)
		}
		if yr.Chart.Error != nil {
			return StockData{Symbol: code}, fmt.Errorf("yahoo API: %s", yr.Chart.Error.Description)
		}
		if len(yr.Chart.Result) == 0 {
			return StockData{Symbol: code}, fmt.Errorf("no result")
		}

		res := yr.Chart.Result[0]
		if len(res.Indicators.Quote) == 0 {
			return StockData{Symbol: code}, fmt.Errorf("no quote data")
		}
		q := res.Indicators.Quote[0]

		name := providedName
		if name == "" {
			name = res.Meta.ShortName
		}
		if name == "" {
			name = code
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

		// Yahoo quirk: the most recent daily bar can come back with null OHLCV in the
		// quote arrays even after the session has closed; the real values live in meta.
		// Backfill that bar from meta so the latest close isn't silently dropped
		// (otherwise the report shows yesterday's price).
		candles = backfillLatestFromMeta(candles, res.Meta.RegularMarketPrice, res.Meta.RegularMarketTime,
			res.Meta.RegularMarketOpen, res.Meta.RegularMarketDayHigh, res.Meta.RegularMarketDayLow,
			res.Meta.RegularMarketVolume)

		result := StockData{Symbol: code, Name: name, Market: market, Candles: candles}

		// 6. Store in cache on success
		f.dataCache.Set(ticker, result)

		return result, nil
	}

	return StockData{Symbol: code}, fmt.Errorf("yahoo %s failed after %d attempts: %w",
		ticker, maxAttempts, lastErr)
}

// taipeiLoc is UTC+8, used to bucket timestamps into Taiwan trading days.
var taipeiLoc = time.FixedZone("CST", 8*3600)

// tradingDay returns a comparable YYYYMMDD integer for t in Taiwan time.
func tradingDay(t time.Time) int {
	y, m, d := t.In(taipeiLoc).Date()
	return y*10000 + int(m)*100 + d
}

// backfillLatestFromMeta appends a synthesized latest bar from Yahoo's meta snapshot
// when it represents a newer trading day than the last parsed candle. This recovers
// the most recent session, which Yahoo sometimes returns with null OHLCV in the
// quote arrays. No-op if meta is missing/older/same day.
func backfillLatestFromMeta(candles []Candle, price *float64, mktTime int64,
	open, high, low *float64, vol *int64) []Candle {
	if price == nil || *price <= 0 || mktTime <= 0 {
		return candles
	}
	metaDate := time.Unix(mktTime, 0).UTC()
	if len(candles) > 0 && tradingDay(metaDate) <= tradingDay(candles[len(candles)-1].Date) {
		return candles // quote arrays already include this (or a newer) session
	}
	p := *price
	bar := Candle{
		Date:   metaDate,
		Open:   derefOrF(open, p),
		High:   derefOrF(high, p),
		Low:    derefOrF(low, p),
		Close:  p,
		Volume: derefOrI(vol, 0),
	}
	if bar.High < bar.Close {
		bar.High = bar.Close
	}
	if bar.Low <= 0 || bar.Low > bar.Close {
		bar.Low = bar.Close
	}
	if bar.Open <= 0 {
		bar.Open = bar.Close
	}
	return append(candles, bar)
}

func derefOrF(p *float64, fallback float64) float64 {
	if p == nil || math.IsNaN(*p) {
		return fallback
	}
	return *p
}

func derefOrI(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}
