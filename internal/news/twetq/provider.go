// Package twetq is a BEST-EFFORT news provider for twetq.com, a Cloudflare-fronted
// Taiwan stock quant site (routes /stock/<code>, /screen, /rank, /market, /podcast).
//
// Reality (verified): twetq serves HTTP 403 to non-browser User-Agents but 200 with a
// browser UA (plain net/http — no headless browser needed). Its /stock/<code> pages
// are server-rendered (name+code in <title>/og/JSON-LD) but carry no directional
// opinion. Its /podcast page is a JS-rendered app shell with NO server-side episode
// data, so podcast episodes cannot be parsed without a headless browser (out of scope
// for v1). Therefore this provider degrades gracefully: it returns whatever podcast
// items it can parse (currently none, because JS), never an error for "reachable but
// empty", and never fabricates a signal from quant pages.
package twetq

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/news"
)

const (
	defaultBaseURL = "https://twetq.com"
	defaultUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	name           = "twetq"
)

// TODO(news): typed provider status for TWETQ — Fetch currently returns (items, error),
// so "reachable but JS-rendered (no data)" is indistinguishable from "genuinely nothing
// new" in the fetched=0 log. Add a FetchResult{Status: success|no_data|parse_unsupported|
// blocked|fetch_error} so silent breakage (e.g. Cloudflare starts 403ing) is observable.

// Provider implements news.NewsProvider for twetq.com (best-effort).
type Provider struct {
	baseURL string
	ua      string
	client  *http.Client
}

// New builds a Provider. A browser-like UA is mandatory to pass Cloudflare; an empty
// ua falls back to a built-in browser UA.
func New(cfg news.ProviderConfig, ua string, timeout time.Duration) *Provider {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	if ua == "" {
		ua = defaultUA
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Provider{
		baseURL: strings.TrimRight(base, "/"),
		ua:      ua,
		client:  &http.Client{Timeout: timeout},
	}
}

// Name implements news.NewsProvider.
func (p *Provider) Name() string { return name }

// Fetch attempts to discover podcast episodes from /podcast. Because that page is
// JS-rendered, in practice it yields zero items today; that is a graceful degrade
// (reachable source, nothing parseable), NOT an error. A genuine transport failure
// (Cloudflare 403 / network) IS returned as an error so the Service can log+skip it.
func (p *Provider) Fetch(ctx context.Context) ([]news.RawNewsItem, error) {
	body, err := p.get(ctx, p.baseURL+"/podcast")
	if err != nil {
		return nil, err
	}
	return parsePodcast(body), nil
}

// CoveredStocks fetches the sitemap and returns the set of stock codes twetq tracks.
// This is quant-coverage CONTEXT (not a directional signal); exposed for future use.
// Errors are returned so callers can decide to log+ignore.
func (p *Provider) CoveredStocks(ctx context.Context) ([]string, error) {
	body, err := p.get(ctx, p.baseURL+"/sitemap.xml")
	if err != nil {
		return nil, err
	}
	return StockCodesFromSitemap(body), nil
}

func (p *Provider) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.ua) // required: default UA → Cloudflare 403
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d (cloudflare/bot-block?)", endpoint, resp.StatusCode)
	}
	return readAll(resp)
}
