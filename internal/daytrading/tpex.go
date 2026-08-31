package daytrading

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// tpexIntradayStatURL is the live-verified 現股當沖交易統計資訊 endpoint. date is
// YYYY/MM/DD (slash-separated, Gregorian — NOT the ROC year some other TPEx endpoints
// use) and type=Daily selects the per-day view.
const tpexIntradayStatURL = "https://www.tpex.org.tw/www/zh-tw/intraday/stat"

// TPEXProvider fetches the TPEx (上櫃) 當日沖銷 snapshot.
type TPEXProvider struct {
	client *http.Client
}

// NewTPEXProvider builds a provider with the given timeout (0 → 30s).
func NewTPEXProvider(timeout time.Duration) *TPEXProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &TPEXProvider{client: &http.Client{Timeout: timeout}}
}

func (p *TPEXProvider) Market() string { return MarketTPEX }

// tpexURL builds the request URL for date (YYYY-MM-DD). TPEx wants YYYY/MM/DD — the
// slashes matter; the same date in TWSE's YYYYMMDD form silently returns the wrong day.
func tpexURL(date string) (string, error) {
	d, err := time.Parse(dateLayout, date)
	if err != nil {
		return "", fmt.Errorf("daytrading/tpex: bad date %q: %w", date, err)
	}
	return fmt.Sprintf("%s?date=%s&response=json&type=Daily", tpexIntradayStatURL, d.Format("2006/01/02")), nil
}

// Fetch downloads and parses the intraday-stat snapshot for date (YYYY-MM-DD). A
// non-trading day yields a snapshot with zero Stocks and a nil error.
func (p *TPEXProvider) Fetch(ctx context.Context, date string) (*Snapshot, error) {
	url, err := tpexURL(date)
	if err != nil {
		return nil, err
	}
	body, err := get(ctx, p.client, url, "daytrading/tpex")
	if err != nil {
		return nil, err
	}
	return ParseTPEX(body, date, url, time.Now())
}

// ParseTPEX parses an intraday-stat response body into a Snapshot. Pure (no network).
// The per-stock table is located by field name because TPEx ships it with an EMPTY title.
func ParseTPEX(body []byte, asOfDate, sourceURL string, fetchedAt time.Time) (*Snapshot, error) {
	return parseSnapshot(body, MarketTPEX, asOfDate, SourceTPEX, sourceURL, fetchedAt)
}
