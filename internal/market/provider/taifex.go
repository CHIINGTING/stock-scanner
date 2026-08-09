package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// TAIFEX provider — foreign institutional open interest in 臺股期貨.
//
// The DAILY path uses the OpenAPI JSON endpoint, which is UTF-8 with English keys. That is
// the whole reason this file has no encoding dependency: the older CSV download is Big5, and
// Go's standard library cannot decode it. Big5 lives in exactly one place — the backfill
// command (see taifex_backfill.go) — and never reaches an analyzer.
//
// The JSON endpoint's one limitation, verified by request: it accepts NO date parameter and
// always returns the latest trading day. Asking it for history is therefore impossible, and
// that is precisely the gap the Big5 backfill fills.
//
// Both sources were cross-checked on 2026-08-07 and agree exactly (net OI -87,911), so the
// two paths are semantically equivalent; a test asserts that against committed fixtures.

const taifexDailyURL = "https://openapi.taifex.com.tw/v1/MarketDataOfMajorInstitutionalTradersDetailsOfFuturesContractsBytheDate"

const (
	// contractTXF is the 臺股期貨 contract as the JSON feed spells it. The feed puts the
	// Chinese product name in ContractCode (not a ticker), which is surprising enough to be
	// worth a named constant.
	contractTXF = "臺股期貨"
	itemForeign = "外資及陸資"
)

// TAIFEXProvider fetches the futures half of a RawSnapshot.
type TAIFEXProvider struct {
	Client  *http.Client
	BaseURL string // test override
}

func NewTAIFEXProvider(timeout time.Duration) *TAIFEXProvider {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &TAIFEXProvider{Client: &http.Client{Timeout: timeout}}
}

func (p *TAIFEXProvider) Name() string { return "TAIFEX" }

// Fetch returns the futures dataset for date.
//
// Because the endpoint has no date parameter, the requested date is validated against what
// the feed actually returned: asking for a past date yields ErrDateMismatch rather than
// today's data wearing yesterday's label. That single check is what stops the no-date
// limitation from silently corrupting a replay.
func (p *TAIFEXProvider) Fetch(ctx context.Context, date string) (*model.RawSnapshot, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("market: bad date %q: %w", date, err)
	}
	now := time.Now()
	url := p.BaseURL
	if url == "" {
		url = taifexDailyURL
	}
	out := &model.RawSnapshot{Date: date, FetchedAt: now}
	meta := model.SourceMeta{Name: "TAIFEX/MajorInstitutionalTraders", URL: url, FetchedAt: now}

	body, err := p.get(ctx, url)
	if err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
		return out, nil
	}
	fut, asOf, err := ParseTAIFEXDaily(body, date)
	if err != nil {
		meta.AsOfDate = asOf
		out.Sources = append(out.Sources, failed(meta, err))
		return out, nil
	}
	out.Futures = fut
	meta.Status, meta.AsOfDate = model.SourceOK, asOf
	out.Sources = append(out.Sources, meta)
	return out, nil
}

func (p *TAIFEXProvider) get(ctx context.Context, url string) ([]byte, error) {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > 32<<20 {
			break
		}
	}
	return buf, nil
}

// taifexRow mirrors the JSON feed. Keys carry parentheses, so they need explicit tags.
type taifexRow struct {
	Date         string `json:"Date"`
	ContractCode string `json:"ContractCode"`
	Item         string `json:"Item"`
	LongOI       string `json:"OpenInterest(Long)"`
	ShortOI      string `json:"OpenInterest(Short)"`
	NetOI        string `json:"OpenInterest(Net)"`
}

// ParseTAIFEXDaily extracts foreign 臺股期貨 open interest from the UTF-8 JSON feed.
//
// NetOI is taken from the official 多空未平倉口數淨額 column rather than derived from
// long-short, for the same reason as the TWSE cash net: a column-layout change should
// surface as a mismatch, not be papered over by arithmetic.
func ParseTAIFEXDaily(body []byte, wantDate string) (*model.FuturesOIData, string, error) {
	var rows []taifexRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, "", fmt.Errorf("TAIFEX: decode: %w", err)
	}
	if len(rows) == 0 {
		return nil, "", fmt.Errorf("TAIFEX: %w: empty feed", ErrNoData)
	}

	asOf, err := taifexDate(rows[0].Date)
	if err != nil {
		return nil, "", fmt.Errorf("TAIFEX: %w", err)
	}
	if asOf != wantDate {
		return nil, asOf, fmt.Errorf("%w: requested %s, feed carries %s (this endpoint has no date parameter)",
			ErrDateMismatch, wantDate, asOf)
	}

	out := &model.FuturesOIData{Contract: contractTXF, Item: itemForeign, OtherItems: map[string]float64{}}
	var found bool
	for _, r := range rows {
		if strings.TrimSpace(r.ContractCode) != contractTXF {
			continue
		}
		item := strings.TrimSpace(r.Item)
		net, err := parseAmount(r.NetOI)
		if err != nil {
			return nil, asOf, fmt.Errorf("TAIFEX: %s net OI: %w", item, err)
		}
		if item != itemForeign {
			out.OtherItems[item] = net
			continue
		}
		long, err1 := parseAmount(r.LongOI)
		short, err2 := parseAmount(r.ShortOI)
		if err1 != nil || err2 != nil {
			return nil, asOf, fmt.Errorf("TAIFEX: 外資 long/short OI unparsable")
		}
		out.LongOI, out.ShortOI, out.NetOI = long, short, net
		found = true
	}
	if !found {
		return nil, asOf, fmt.Errorf("TAIFEX: %s/%s absent — layout changed", contractTXF, itemForeign)
	}
	return out, asOf, nil
}

// taifexDate converts the feed's YYYYMMDD to ISO.
func taifexDate(s string) (string, error) {
	d, err := time.Parse("20060102", strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("unparsable feed date %q", s)
	}
	return d.Format("2006-01-02"), nil
}

// parseOI is parseAmount with an int-ish contract, kept separate so the backfill parser can
// share it without importing the TWSE file's naming.
func parseOI(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}
