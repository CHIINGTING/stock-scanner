package socialworkerdaily

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/news"
)

// rssFeed is the trimmed RSS 2.0 shape (WordPress /feed/). The feed mixes categories,
// so we keep only 股癌筆記 items.
type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Encoded string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Desc    string `xml:"description"`
}

// fetchRSS is the fallback path used when the WP REST API is unavailable.
func (p *Provider) fetchRSS(ctx context.Context) ([]news.RawNewsItem, error) {
	body, err := p.get(ctx, p.feedURL)
	if err != nil {
		return nil, err
	}
	return parseRSS(body, p.maxItems)
}

// parseRSS is exported-for-test via the package; it filters to 股癌筆記 notes and maps
// each item to a RawNewsItem.
func parseRSS(body []byte, maxItems int) ([]news.RawNewsItem, error) {
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("decode rss: %w", err)
	}
	var out []news.RawNewsItem
	for _, it := range feed.Channel.Items {
		title := news.PlainText(it.Title)
		if !isGooayeNote(title, it.Link) {
			continue
		}
		content := it.Encoded
		if content == "" {
			content = it.Desc
		}
		out = append(out, news.RawNewsItem{
			Source:      name,
			Title:       title,
			URL:         it.Link,
			PublishedAt: parseRSSTime(it.PubDate),
			Content:     news.PlainText(content),
			EpisodeID:   news.NormalizeEpisode(it.Link, title),
		})
		if maxItems > 0 && len(out) >= maxItems {
			break
		}
	}
	return out, nil
}

// parseRSSTime parses RFC1123Z pubDate ("Sat, 04 Jul 2026 14:35:40 +0000").
func parseRSSTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// readAll drains an HTTP response body with a sane cap.
func readAll(resp *http.Response) ([]byte, error) {
	const maxBytes = 8 << 20 // 8 MiB safety cap
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}
