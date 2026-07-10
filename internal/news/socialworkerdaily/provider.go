// Package socialworkerdaily fetches 股癌 (Gooaye) podcast notes from
// socialworkerdaily.com via its WordPress REST API (primary) with an RSS fallback.
//
// It is the PRIMARY, high-reliability news source: the WP API returns clean JSON with
// a reliable publish timestamp (date_gmt), a stable episode URL/slug
// (notes-of-gooaye-ep-NNN) and full note content — no HTML scraping required.
package socialworkerdaily

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/news"
)

const (
	defaultAPIBase  = "https://socialworkerdaily.com/wp-json/wp/v2"
	defaultFeedURL  = "https://socialworkerdaily.com/feed/"
	defaultMaxItems = 8
	searchTerm      = "股癌筆記" // WP full-text search discovers the notes category reliably
	titlePrefix     = "股癌筆記"
	name            = "socialworkerdaily"
)

// Provider implements news.NewsProvider for SocialWorkerDaily.
type Provider struct {
	apiBase  string
	feedURL  string
	maxItems int
	client   *http.Client
	ua       string
}

// New builds a Provider from config, applying defaults for any empty field.
func New(cfg news.ProviderConfig, ua string, timeout time.Duration) *Provider {
	api := cfg.APIBase
	if api == "" {
		api = defaultAPIBase
	}
	feed := defaultFeedURL
	if cfg.BaseURL != "" {
		feed = strings.TrimRight(cfg.BaseURL, "/") + "/feed/"
	}
	max := cfg.MaxItems
	if max <= 0 {
		max = defaultMaxItems
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Provider{
		apiBase:  strings.TrimRight(api, "/"),
		feedURL:  feed,
		maxItems: max,
		client:   &http.Client{Timeout: timeout},
		ua:       ua,
	}
}

// Name implements news.NewsProvider.
func (p *Provider) Name() string { return name }

// Fetch returns the most recent 股癌 podcast-note items. It tries the WP REST API
// first and falls back to RSS on any API error, so a WP-side change degrades to the
// feed rather than failing the whole source.
func (p *Provider) Fetch(ctx context.Context) ([]news.RawNewsItem, error) {
	items, err := p.fetchAPI(ctx)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	rssItems, rssErr := p.fetchRSS(ctx)
	if rssErr != nil {
		if err != nil {
			return nil, fmt.Errorf("wp api: %v; rss fallback: %w", err, rssErr)
		}
		return nil, fmt.Errorf("rss fallback: %w", rssErr)
	}
	return rssItems, nil
}

// wpPost is the trimmed WordPress REST post shape we consume.
type wpPost struct {
	ID      int    `json:"id"`
	DateGMT string `json:"date_gmt"`
	Slug    string `json:"slug"`
	Link    string `json:"link"`
	Title   struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Content struct {
		Rendered string `json:"rendered"`
	} `json:"content"`
}

func (p *Provider) fetchAPI(ctx context.Context) ([]news.RawNewsItem, error) {
	q := url.Values{}
	q.Set("search", searchTerm)
	q.Set("per_page", fmt.Sprintf("%d", p.maxItems))
	q.Set("orderby", "date")
	q.Set("order", "desc")
	q.Set("_fields", "id,date_gmt,slug,link,title,content")
	endpoint := p.apiBase + "/posts?" + q.Encode()

	body, err := p.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return parsePosts(body)
}

// parsePosts decodes a WP REST posts payload and keeps only the 股癌筆記 notes. Split
// out from fetchAPI so it can be tested offline against a captured fixture.
func parsePosts(body []byte) ([]news.RawNewsItem, error) {
	var posts []wpPost
	if err := json.Unmarshal(body, &posts); err != nil {
		return nil, fmt.Errorf("decode wp posts: %w", err)
	}
	var out []news.RawNewsItem
	for _, post := range posts {
		title := news.PlainText(post.Title.Rendered)
		if !isGooayeNote(title, post.Slug) {
			continue // WP search is fuzzy; keep only the 股癌筆記 notes
		}
		out = append(out, news.RawNewsItem{
			Source:      name,
			Title:       title,
			URL:         post.Link,
			PublishedAt: parseWPTime(post.DateGMT),
			Content:     news.PlainText(post.Content.Rendered),
			EpisodeID:   news.NormalizeEpisode(post.Slug, post.Link, title),
		})
	}
	return out, nil
}

func isGooayeNote(title, slug string) bool {
	return strings.Contains(title, titlePrefix) || strings.Contains(strings.ToLower(slug), "notes-of-gooaye")
}

// parseWPTime parses WordPress date_gmt ("2026-07-08T15:49:31", no zone) as UTC.
func parseWPTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (p *Provider) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if p.ua != "" {
		req.Header.Set("User-Agent", p.ua)
	}
	req.Header.Set("Accept", "application/json, application/rss+xml, */*")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d", endpoint, resp.StatusCode)
	}
	return readAll(resp)
}
