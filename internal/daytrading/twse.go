package daytrading

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// twseTWTB4UURL is the live-verified 當日沖銷交易標的及成交量值 endpoint. date is YYYYMMDD
// and selectType=All returns every security for the day (ETFs included — the archive
// stays lossless and any universe filtering is the study's job, not the ingester's).
const twseTWTB4UURL = "https://www.twse.com.tw/exchangeReport/TWTB4U"

// TWSEProvider fetches the TWSE (上市) 當日沖銷 snapshot.
type TWSEProvider struct {
	client *http.Client
}

// NewTWSEProvider builds a provider with the given timeout (0 → 30s).
func NewTWSEProvider(timeout time.Duration) *TWSEProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &TWSEProvider{client: &http.Client{Timeout: timeout}}
}

func (p *TWSEProvider) Market() string { return MarketTWSE }

// twseURL builds the request URL for date (YYYY-MM-DD). TWSE wants YYYYMMDD, with no
// separators — the one place where it differs from TPEx's YYYY/MM/DD.
func twseURL(date string) (string, error) {
	d, err := time.Parse(dateLayout, date)
	if err != nil {
		return "", fmt.Errorf("daytrading/twse: bad date %q: %w", date, err)
	}
	return fmt.Sprintf("%s?response=json&date=%s&selectType=All", twseTWTB4UURL, d.Format("20060102")), nil
}

// Fetch downloads and parses the TWTB4U snapshot for date (YYYY-MM-DD). A non-trading day
// yields a snapshot with zero Stocks and a nil error.
func (p *TWSEProvider) Fetch(ctx context.Context, date string) (*Snapshot, error) {
	url, err := twseURL(date)
	if err != nil {
		return nil, err
	}
	body, err := get(ctx, p.client, url, "daytrading/twse")
	if err != nil {
		return nil, err
	}
	return ParseTWSE(body, date, url, time.Now())
}

// ParseTWSE parses a TWTB4U response body into a Snapshot. Pure (no network), so it is
// unit-tested against fixtures matching the verified contract. asOfDate is YYYY-MM-DD.
func ParseTWSE(body []byte, asOfDate, sourceURL string, fetchedAt time.Time) (*Snapshot, error) {
	return parseSnapshot(body, MarketTWSE, asOfDate, SourceTWSE, sourceURL, fetchedAt)
}
